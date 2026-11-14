// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blinklabs-io/adder/input/chainsync"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/blinklabs-io/adder/managed"
	"github.com/blinklabs-io/adder/pipeline"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/spf13/pflag"
)

var (
	managedOptions   managed.Options
	managedAddress   string
	managedTokenFile string
)

func bindManagedFlags(fs *pflag.FlagSet) {
	fs.StringVar(&managedOptions.ConfigDir, "managed-config-dir", "", "enable managed routing using this configuration directory")
	fs.StringVar(&managedOptions.CredentialsDir, "managed-credentials-dir", "", "restricted managed credential directory")
	fs.StringVar(&managedOptions.LogDir, "managed-log-dir", "", "trusted local managed event file directory")
	fs.StringSliceVar(&managedOptions.AllowedHosts, "managed-allow-hosts", nil, "operator-approved webhook hostnames")
	fs.BoolVar(&managedOptions.AllowLocalTargets, "managed-allow-local-targets", false, "allow loopback webhook receivers for local tests")
	fs.StringVar(&managedAddress, "managed-address", "127.0.0.1:8081", "private managed API listener (loopback only)")
	fs.StringVar(&managedTokenFile, "managed-token-file", "", "restricted file containing the operator API token")
}

func validateManagedInput(resolved *plugin.Configuration) error {
	if cfg.Input != "chainsync" {
		return errors.New("managed mode requires chainsync input")
	}
	for _, entry := range plugin.GetPlugins(plugin.PluginTypeFilter) {
		options, err := resolved.Options(plugin.PluginTypeFilter, entry.Name)
		if err != nil {
			return err
		}
		for _, option := range entry.Options {
			if option.Type == plugin.PluginOptionTypeString && options.String(option.Name) != "" {
				return errors.New("global filters are unavailable in managed mode")
			}
		}
	}
	options, err := resolved.Options(plugin.PluginTypeInput, "chainsync")
	if err != nil {
		return err
	}
	if !options.Bool("intersect-tip") || !options.Bool("auto-reconnect") || options.String("state-path") != "" || options.String("intersect-point") != "" {
		return errors.New("managed live-tail requires intersect-tip, auto-reconnect and no cursor state or intersect-point")
	}
	return nil
}

func managedToken(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", errors.New("operator token file unavailable")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || st.Size() > 4096 {
		return "", errors.New("operator token must be a restricted regular file")
	}
	data := make([]byte, st.Size())
	if _, err = io.ReadFull(f, data); err != nil {
		return "", errors.New("operator token could not be read")
	}
	return strings.TrimSpace(string(data)), nil
}

func runManaged(resolved *plugin.Configuration) error {
	if err := validateManagedInput(resolved); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(managedAddress)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("managed API must bind a literal loopback address")
	}
	token, err := managedToken(managedTokenFile)
	if err != nil {
		return err
	}
	options := managedOptions
	options.Logger = logging.GetLoggerForComponent("managed.router")
	router, err := managed.New(options)
	if err != nil {
		return err
	}
	input, err := resolved.New(plugin.PluginTypeInput, "chainsync")
	if err != nil {
		return err
	}
	source, ok := input.(*chainsync.ChainSync)
	if !ok {
		return errors.New("unexpected chainsync implementation")
	}
	pipe := pipeline.New()
	pipe.AddInput(input)
	pipe.AddOutput(router)
	handler, err := router.Handler(token, func() string {
		if !pipe.IsRunning() {
			return "stopped"
		}
		if source.Connected() {
			return "connected"
		}
		return "disconnected"
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", managedAddress)
	if err != nil {
		return errors.New("managed listener unavailable")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	apiErrors := make(chan error, 1)

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := pipe.StartContext(ctx); err != nil {
		_ = listener.Close()
		return fmt.Errorf("managed startup failed: %w", err)
	}
	go func() { apiErrors <- server.Serve(listener) }()
	go func() {
		for range pipe.ErrorChan() {
			options.Logger.Warn("source diagnostic", "component", "managed.router", "action", "source_error")
		}
	}()
	var terminal error
	select {
	case <-ctx.Done():
	case <-pipe.Failed():
		terminal = pipe.Failure()
	case err := <-apiErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			terminal = errors.New("managed API failed")
		}
	}
	if failure := pipe.Failure(); failure != nil {
		terminal = errors.Join(terminal, failure)
	}
	if err := pipe.Stop(); err != nil {
		terminal = errors.Join(terminal, err)
	}
	final := router.Status()
	options.Logger.Info("managed shutdown", "component", "managed.router", "action", "shutdown", "last_dispatched", final.LastDispatched, "counters", final.Counters)
	return terminal
}

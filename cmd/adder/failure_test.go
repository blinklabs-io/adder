// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/examples/plugins"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
)

func TestCLIExitsAfterTerminalPluginFailure(t *testing.T) {
	if os.Getenv("ADDER_TEST_TERMINAL_FAILURE") == "1" {
		plugin.Register(plugin.PluginEntry{
			Type: plugin.PluginTypeInput, Name: "test-source",
			NewFromOptionsFunc: func(plugin.Options) (plugin.ManagedPlugin, error) {
				p, err := plugins.NewTickerInput(50 * time.Millisecond)
				require.NoError(t, err)
				return p, nil
			},
		})
		plugin.Register(plugin.PluginEntry{
			Type: plugin.PluginTypeOutput, Name: "test-sink",
			NewFromOptionsFunc: func(plugin.Options) (plugin.ManagedPlugin, error) {
				p, err := plugins.NewHandlerOutput(
					func(context.Context, event.Event) error { return errors.New("test terminal failure") },
				)
				require.NoError(t, err)
				return p, nil
			},
		})
		rootCmd.SetArgs(
			[]string{
				"--input",
				"test-source",
				"--output",
				"test-sink",
				"--api-address",
				"127.0.0.1",
				"--api-port",
				"0",
			},
		)
		require.ErrorContains(t, rootCmd.Execute(), "test terminal failure")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestCLIExitsAfterTerminalPluginFailure$",
	)
	command.Env = []string{"ADDER_TEST_TERMINAL_FAILURE=1"}
	for _, key := range []string{"PATH", "HOME", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	output, err := command.CombinedOutput()
	require.NoError(
		t,
		ctx.Err(),
		"CLI waited for a signal after terminal failure: %s",
		output,
	)
	require.NoError(t, err, "%s", output)
	require.Contains(
		t,
		string(output),
		"terminal plugin failure, stopping pipeline",
	)
	require.NotContains(t, string(output), "Adder stopped gracefully")
}

// Copyright 2026 Blink Labs Software
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/plugin"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
)

func TestPublishedConfigurationExamples(t *testing.T) {
	for _, path := range []string{
		"../../config.yaml.example", "../../config-preview.yaml",
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			checkDocumentedYAML(t, data)
		})
	}
}

func checkDocumentedYAML(t *testing.T, data []byte) {
	t.Helper()
	cfg := config.New()
	require.NoError(t, yaml.UnmarshalStrict(data, cfg))
	_, err := plugin.ResolveConfig(
		cfg.Plugin, nil, func(string) (string, bool) { return "", false },
	)
	require.NoError(t, err)
}

func TestActiveDocumentationExamples(t *testing.T) {
	paths := []string{"../../README.md", "../../examples/README.md"}
	for _, pattern := range []string{
		"../../docs/*.md", "../../examples/*/README.md",
	} {
		matches, err := filepath.Glob(pattern)
		require.NoError(t, err)
		paths = append(paths, matches...)
	}
	fences := regexp.MustCompile("(?ms)^ {0,3}```([a-z]+)\\n(.*?)^ {0,3}```$")
	flags := regexp.MustCompile(`--[a-zA-Z][a-zA-Z0-9-]*`)
	rootCmd.InitDefaultHelpFlag()
	fs := rootCmd.Flags()
	adderCommand := regexp.MustCompile(
		`(?m)^(?:\$ )?(?:(?:\./)?adder(?:\s|$)|go run \./cmd/adder(?:\s|$))`,
	)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			for i, block := range fences.FindAllSubmatch(data, -1) {
				t.Run(fmt.Sprint(i+1), func(t *testing.T) {
					body := string(block[2])
					switch string(block[1]) {
					case "yaml":
						checkDocumentedYAML(t, block[2])
					case "json":
						require.True(
							t,
							json.Valid(block[2]),
							"invalid JSON: %s",
							body,
						)
					case "sh", "bash":
						if !adderCommand.MatchString(body) {
							return
						}
						for _, flag := range flags.FindAllString(body, -1) {
							name := strings.TrimPrefix(flag, "--")
							if strings.Contains(
								body,
								"notifications validate",
							) &&
								name == "json" {
								continue
							}
							require.NotNil(
								t,
								fs.Lookup(name),
								"unknown documented flag %s",
								flag,
							)
						}
					}
				})
			}
		})
	}
}

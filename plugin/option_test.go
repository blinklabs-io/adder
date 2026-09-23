// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plugin

import (
	"strings"
	"sync"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func configEntry() PluginEntry {
	return PluginEntry{
		Type: PluginTypeInput,
		Name: "demo",
		Options: []PluginOption{
			{
				Name:         "text",
				Type:         PluginOptionTypeString,
				DefaultValue: "default",
				CustomEnvVar: "DEMO_TEXT",
				CustomFlag:   "text",
			},
			{Name: "enabled", Type: PluginOptionTypeBool, DefaultValue: true},
			{Name: "count", Type: PluginOptionTypeInt, DefaultValue: 7},
			{Name: "size", Type: PluginOptionTypeUint, DefaultValue: uint(9)},
		},
	}
}

func TestResolutionPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		yaml  map[string]any
		env   map[string]string
		flags []string
		want  string
	}{
		{name: "default", want: "default"},
		{name: "yaml", yaml: map[string]any{"text": "yaml"}, want: "yaml"},
		{
			name: "env beats yaml",
			yaml: map[string]any{"text": "yaml"},
			env:  map[string]string{"INPUT_DEMO_TEXT": "env"},
			want: "env",
		},
		{
			name: "alias beats generated",
			env: map[string]string{
				"INPUT_DEMO_TEXT": "env",
				"DEMO_TEXT":       "alias",
			},
			want: "alias",
		},
		{
			name:  "CLI beats all",
			yaml:  map[string]any{"text": "yaml"},
			env:   map[string]string{"DEMO_TEXT": "alias"},
			flags: []string{"--input-text=cli"},
			want:  "cli",
		},
		{
			name:  "explicit empty CLI",
			yaml:  map[string]any{"text": "yaml"},
			flags: []string{"--input-text="},
			want:  "",
		},
		{
			name: "explicit empty env",
			yaml: map[string]any{"text": "yaml"},
			env:  map[string]string{"DEMO_TEXT": ""},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := configEntry()
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			for _, option := range entry.Options {
				require.NoError(t, option.AddToFlagSet(fs, "input", "demo"))
			}
			require.NoError(t, fs.Parse(tt.flags))
			got, err := entry.resolve(
				tt.yaml,
				fs,
				func(key string) (string, bool) { v, ok := tt.env[key]; return v, ok },
			)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.String("text"))
		})
	}
}

func TestResolutionPreservesZeroValues(t *testing.T) {
	entry := configEntry()
	for _, source := range []string{"yaml", "env", "cli"} {
		t.Run(source, func(t *testing.T) {
			data := map[string]any{}
			env := map[string]string{}
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			for _, option := range entry.Options {
				require.NoError(t, option.AddToFlagSet(fs, "input", "demo"))
			}
			switch source {
			case "yaml":
				data = map[string]any{
					"text":    "",
					"enabled": false,
					"count":   0,
					"size":    0,
				}
			case "env":
				env = map[string]string{
					"INPUT_DEMO_TEXT":    "",
					"INPUT_DEMO_ENABLED": "false",
					"INPUT_DEMO_COUNT":   "0",
					"INPUT_DEMO_SIZE":    "0",
				}
			case "cli":
				require.NoError(
					t,
					fs.Parse(
						[]string{
							"--input-text=",
							"--input-demo-enabled=false",
							"--input-demo-count=0",
							"--input-demo-size=0",
						},
					),
				)
			}
			got, err := entry.resolve(
				data,
				fs,
				func(key string) (string, bool) { v, ok := env[key]; return v, ok },
			)
			require.NoError(t, err)
			require.Empty(t, got.String("text"))
			require.False(t, got.Bool("enabled"))
			require.Zero(t, got.Int("count"))
			require.Zero(t, got.Uint("size"))
		})
	}
}

func TestResolutionRejectsInvalidScalars(t *testing.T) {
	entry := configEntry()
	for _, data := range []map[string]any{
		{"typo": true}, {"text": 1}, {"enabled": "false"}, {"count": false}, {"size": -1}, {"size": uint64(1) << 32}, {"count": int64(1)}, {"text": nil},
	} {
		_, err := entry.resolve(data, nil, nil)
		require.Error(t, err)
	}
	for _, key := range []string{"ENABLED", "COUNT", "SIZE"} {
		_, err := entry.resolve(
			nil,
			nil,
			func(name string) (string, bool) { return "secret-value", strings.HasSuffix(name, key) },
		)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret-value")
	}
	_, err := entry.resolve(
		map[string]any{"size": uint64(4294967295)},
		nil,
		nil,
	)
	require.NoError(t, err)
}

func TestIndependentOptionSnapshots(t *testing.T) {
	entry := configEntry()
	values := map[string]any{"text": "first"}
	first, err := entry.resolve(values, nil, nil)
	require.NoError(t, err)
	values["text"] = "changed"
	second, err := entry.resolve(values, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "first", first.String("text"))
	require.Equal(t, "changed", second.String("text"))
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got, err := entry.resolve(
				map[string]any{"text": "concurrent"},
				nil,
				nil,
			)
			if err != nil || got.String("text") != "concurrent" {
				t.Error("independent resolution failed")
			}
		})
	}
	wg.Wait()
	require.Equal(t, "first", first.String("text"))
}

func TestInvalidOptionDefinition(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	for _, option := range []PluginOption{{Name: "bad", Type: 99}, {Name: "bad", Type: PluginOptionTypeBool, DefaultValue: "false"}} {
		require.Error(t, option.AddToFlagSet(fs, "input", "demo"))
	}
}

func TestSplitAndTrim(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "no separator",
			input:    "addr1aaa",
			expected: []string{"addr1aaa"},
		},
		{
			name:     "no whitespace",
			input:    "addr1aaa,addr1bbb",
			expected: []string{"addr1aaa", "addr1bbb"},
		},
		{
			name:  "YAML folded scalar (spaces after commas)",
			input: "addr1aaa, addr1bbb, addr1ccc",
			expected: []string{
				"addr1aaa",
				"addr1bbb",
				"addr1ccc",
			},
		},
		{
			name:     "YAML literal scalar (newlines and tabs)",
			input:    "addr1aaa,\n\taddr1bbb\n",
			expected: []string{"addr1aaa", "addr1bbb"},
		},
		{
			// A folded scalar only replaces newlines with spaces for equally
			// indented lines; a more-indented line keeps its newline and
			// leading indent
			name:  "YAML folded scalar (more-indented line)",
			input: "addr1aaa,\n  addr1bbb,\naddr1ccc",
			expected: []string{
				"addr1aaa",
				"addr1bbb",
				"addr1ccc",
			},
		},
		{
			// A blank line inside a folded scalar also yields a newline
			name:     "YAML folded scalar (blank line)",
			input:    "addr1aaa,\naddr1bbb",
			expected: []string{"addr1aaa", "addr1bbb"},
		},
		{
			name:     "leading and trailing whitespace",
			input:    "  addr1aaa  ",
			expected: []string{"addr1aaa"},
		},
		{
			name:     "empty entries dropped",
			input:    "addr1aaa,,  ,addr1bbb,",
			expected: []string{"addr1aaa", "addr1bbb"},
		},
		{
			name:     "whitespace only",
			input:    "  ,\n , ",
			expected: nil,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := SplitAndTrim(testCase.input)
			if len(got) != len(testCase.expected) {
				t.Fatalf("got %q, want %q", got, testCase.expected)
			}
			for i := range got {
				if got[i] != testCase.expected[i] {
					t.Errorf("got %q, want %q", got, testCase.expected)
					break
				}
			}
		})
	}
}

func TestUnsignedDefaultNormalized(t *testing.T) {
	entry := configEntry()
	entry.Options[3].DefaultValue = 3
	values, err := entry.resolve(nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, uint(3), values.Uint("size"))
}

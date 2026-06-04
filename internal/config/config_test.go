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

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests use locally-constructed *Config values, never the package-level
// globalConfig singleton — that singleton is mutable shared state and would
// race across parallel test runs.

func TestLoadAppliesPopulateDefaults(t *testing.T) {
	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(""))

	assert.Equal(t, uint64(21600), c.ByronGenesis.EpochLength)
	assert.Equal(t, uint64(21600), c.ByronGenesis.ByronSlotsPerEpoch)
	require.NotNil(t, c.ByronGenesis.EndSlot)
	assert.Equal(t, uint64(4492799), *c.ByronGenesis.EndSlot)
	assert.Equal(t, uint64(432000), c.ShelleyGenesis.EpochLength)
	assert.Equal(t, int32(208), c.ShelleyTransEpoch)
}

func TestDefaultPluginConstants(t *testing.T) {
	assert.Equal(t, "chainsync", DefaultInputPlugin)
	assert.Equal(t, "log", DefaultOutputPlugin)
}

func TestLoadFromEnvVars(t *testing.T) {
	t.Setenv("INPUT", "mempool")
	t.Setenv("OUTPUT", "webhook")
	t.Setenv("API_ADDRESS", "127.0.0.1")
	t.Setenv("API_PORT", "9999")
	t.Setenv("LOGGING_LEVEL", "debug")
	t.Setenv("KUPO_URL", "http://kupo:1442")
	t.Setenv("SHELLEY_TRANS_EPOCH", "42")

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(""))

	assert.Equal(t, "mempool", c.Input)
	assert.Equal(t, "webhook", c.Output)
	assert.Equal(t, "127.0.0.1", c.Api.ListenAddress)
	assert.Equal(t, uint(9999), c.Api.ListenPort)
	assert.Equal(t, "debug", c.Logging.Level)
	assert.Equal(t, "http://kupo:1442", c.KupoUrl)
	assert.Equal(t, int32(42), c.ShelleyTransEpoch)
}

func TestLoadEnvVarTypeError(t *testing.T) {
	t.Setenv("API_PORT", "not-a-number")
	c := &Config{ShelleyTransEpoch: -1}
	err := c.Load("")
	require.Error(t, err)
	assert.ErrorContains(t, err, "error processing environment")
}

// TestLoadEnvErrorBeforeYAMLRead pins the ordering invariant established
// by the precedence fix: envconfig.Process must run before any YAML I/O.
// Bad env + unparseable YAML => env error wins. A future refactor that
// swaps the order back would fail this test (it would surface the YAML
// parse error instead).
func TestLoadEnvErrorBeforeYAMLRead(t *testing.T) {
	t.Setenv("API_PORT", "not-a-number")
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(":\n  -bad: ["), 0o600))

	c := &Config{ShelleyTransEpoch: -1}
	err := c.Load(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "error processing environment")
	assert.NotContains(t, err.Error(), "error parsing config file")
}

func TestLoadFromYAML(t *testing.T) {
	yamlBody := []byte(`
input: mempool
output: webhook
kupo_url: http://kupo:1442
api:
  address: 127.0.0.1
  port: 9999
  events:
    buffer-size: 250
logging:
  level: warn
debug:
  address: 0.0.0.0
  port: 6060
shelley_trans_epoch: 0
byron_genesis:
  epoch_length: 100
  byron_slots_per_epoch: 50
shelley_genesis:
  epoch_length: 200
`)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, yamlBody, 0o600))

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(path))

	assert.Equal(t, "mempool", c.Input)
	assert.Equal(t, "webhook", c.Output)
	assert.Equal(t, "http://kupo:1442", c.KupoUrl)
	assert.Equal(t, "127.0.0.1", c.Api.ListenAddress)
	assert.Equal(t, uint(9999), c.Api.ListenPort)
	assert.Equal(t, uint(250), c.Api.Events.BufferSize)
	assert.Equal(t, "warn", c.Logging.Level)
	assert.Equal(t, "0.0.0.0", c.Debug.ListenAddress)
	assert.Equal(t, uint(6060), c.Debug.ListenPort)
	// 0 is a valid non-default value here; populate must NOT rewrite it.
	assert.Equal(t, int32(0), c.ShelleyTransEpoch)
	assert.Equal(t, uint64(100), c.ByronGenesis.EpochLength)
	assert.Equal(t, uint64(50), c.ByronGenesis.ByronSlotsPerEpoch)
	assert.Equal(t, uint64(200), c.ShelleyGenesis.EpochLength)
}

func TestLoadYAMLReadError(t *testing.T) {
	c := &Config{ShelleyTransEpoch: -1}
	err := c.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "error reading config file")
}

func TestLoadYAMLParseError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(":\n  -bad: ["), 0o600))
	c := &Config{ShelleyTransEpoch: -1}
	err := c.Load(path)
	require.Error(t, err)
	assert.ErrorContains(t, err, "error parsing config file")
}

// Spec "set an env var and a conflicting YAML value, load, and
// assert YAML wins." Documented precedence is CLI > YAML > env.
func TestPrecedenceYAMLOverEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("input: yaml-input\n"), 0o600),
	)
	t.Setenv("INPUT", "env-input")

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(path))
	assert.Equal(t, "yaml-input", c.Input)
}

func TestPrecedenceEnvOverDefault(t *testing.T) {
	t.Setenv("INPUT", "env-input")
	c := &Config{Input: DefaultInputPlugin, ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(""))
	assert.Equal(t, "env-input", c.Input)
}

func TestPrecedenceYAMLOverDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("input: yaml-input\n"), 0o600),
	)

	c := &Config{Input: DefaultInputPlugin, ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(path))
	assert.Equal(t, "yaml-input", c.Input)
}

// CLI > YAML cannot be satisfied without a Load() signature change:
// pflag binds variables by reference, so yaml.Unmarshal overwrites the
// bound var in place. Snapshotting fs.Visit-Changed flags before YAML
// load and re-applying after would fix it; tracked as a follow-up.
func TestPrecedenceCLIOverYAML_NotYetSupported(t *testing.T) {
	t.Skip(
		"requires Load() to take *pflag.FlagSet and snapshot " +
			"Changed flags before YAML overwrites them in place",
	)
}

// TestLoadCalledTwice_DocumentsKnownLimitation_BUG pins the
// documented single-shot limitation of Load: removing a key from the
// YAML between two Load() calls on the same *Config does NOT unset
// the field. THIS TEST ASSERTS BUGGY BEHAVIOR ON PURPOSE so the
// limitation is visible to anyone running the suite. When the bug is
// fixed (Load resets c, or callers move to a "construct fresh
// Config" pattern), THIS TEST WILL FAIL — that is the expected
// signal. Flip the assertion in this test and un-Skip the paired
// TestLoadCalledTwice_ResetsRemovedKey_WhenFixed below.
func TestLoadCalledTwice_DocumentsKnownLimitation_BUG(t *testing.T) {
	dir := t.TempDir()
	withKupo := filepath.Join(dir, "with.yaml")
	withoutKupo := filepath.Join(dir, "without.yaml")
	require.NoError(t, os.WriteFile(
		withKupo,
		[]byte("kupo_url: http://kupo:1442\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		withoutKupo,
		[]byte("input: mempool\n"),
		0o600,
	))

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(withKupo))
	require.Equal(t, "http://kupo:1442", c.KupoUrl)

	require.NoError(t, c.Load(withoutKupo))
	assert.Equal(
		t,
		"http://kupo:1442",
		c.KupoUrl,
		"current (buggy) behavior: Load retains values from prior "+
			"call; see paired _WhenFixed test for the desired spec",
	)
}

// TestLoadCalledTwice_ResetsRemovedKey_WhenFixed describes the
// desired post-fix behavior of Load() for reload scenarios: a key
// present in the first YAML but absent from the second should be
// reset to the zero value (or its bound default) after the second
// Load. Skipped until the single-shot limitation is fixed; when an
// engineer fixes Load(), un-Skip this test and delete the paired
// _BUG test above.
func TestLoadCalledTwice_ResetsRemovedKey_WhenFixed(t *testing.T) {
	t.Skip(
		"desired spec; un-Skip when Load() is fixed to reset c " +
			"between calls (paired with _BUG test above)",
	)
}

func TestPopulateGenesisBranches(t *testing.T) {
	preset := uint64(7)
	tests := []struct {
		name string
		in   Config
		want func(t *testing.T, c *Config)
	}{
		{
			name: "all-zero -> defaults",
			in:   Config{ShelleyTransEpoch: -1},
			want: func(t *testing.T, c *Config) {
				assert.Equal(t, uint64(21600), c.ByronGenesis.EpochLength)
				assert.Equal(
					t,
					uint64(21600),
					c.ByronGenesis.ByronSlotsPerEpoch,
				)
				require.NotNil(t, c.ByronGenesis.EndSlot)
				assert.Equal(t, uint64(4492799), *c.ByronGenesis.EndSlot)
				assert.Equal(t, uint64(432000), c.ShelleyGenesis.EpochLength)
				assert.Equal(t, int32(208), c.ShelleyTransEpoch)
			},
		},
		{
			name: "preset values preserved",
			in: Config{
				ByronGenesis: ByronGenesisConfig{
					EpochLength:        100,
					ByronSlotsPerEpoch: 50,
					EndSlot:            &preset,
				},
				ShelleyGenesis:    ShelleyGenesisConfig{EpochLength: 200},
				ShelleyTransEpoch: 99,
			},
			want: func(t *testing.T, c *Config) {
				assert.Equal(t, uint64(100), c.ByronGenesis.EpochLength)
				assert.Equal(
					t,
					uint64(50),
					c.ByronGenesis.ByronSlotsPerEpoch,
				)
				require.NotNil(t, c.ByronGenesis.EndSlot)
				assert.Equal(t, uint64(7), *c.ByronGenesis.EndSlot)
				assert.Equal(t, uint64(200), c.ShelleyGenesis.EpochLength)
				assert.Equal(t, int32(99), c.ShelleyTransEpoch)
			},
		},
		{
			name: "epoch_length set, slots-per-epoch zero -> follows epoch_length",
			in: Config{
				ByronGenesis: ByronGenesisConfig{
					EpochLength: 1000,
				},
				ShelleyTransEpoch: -1,
			},
			want: func(t *testing.T, c *Config) {
				assert.Equal(t, uint64(1000), c.ByronGenesis.EpochLength)
				assert.Equal(
					t,
					uint64(1000),
					c.ByronGenesis.ByronSlotsPerEpoch,
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.in // local copy — avoid aliasing across subtests
			require.NoError(t, c.Load(""))
			tt.want(t, &c)
		})
	}
}

func TestGetConfigReturnsSingleton(t *testing.T) {
	a := GetConfig()
	b := GetConfig()
	require.NotNil(t, a)
	assert.Same(t, a, b)
}

func TestBindFlagsRegistersExpectedFlags(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	c := &Config{}
	require.NoError(t, c.BindFlags(fs))
	for _, name := range []string{
		"config",
		"version",
		"input",
		"output",
		"api-address",
		"api-port",
		"logging-level",
		"debug-address",
		"debug-port",
	} {
		assert.NotNil(
			t,
			fs.Lookup(name),
			"flag %q must be registered",
			name,
		)
	}
}

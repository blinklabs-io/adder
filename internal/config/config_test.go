package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var configEnvVars = []string{
	"INPUT", "OUTPUT", "KUPO_URL",
	"API_ADDRESS", "API_PORT", "API_EVENTS_BUFFER_SIZE",
	"DEBUG_ADDRESS", "DEBUG_PORT",
	"BYRON_GENESIS_END_SLOT",
	"BYRON_GENESIS_EPOCH_LENGTH",
	"BYRON_GENESIS_BYRON_SLOTS_PER_EPOCH",
	"SHELLEY_GENESIS_EPOCH_LENGTH",
	"SHELLEY_TRANS_EPOCH",
	"PLUGINS",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, v := range configEnvVars {
		orig, had := os.LookupEnv(v)
		require.NoError(t, os.Unsetenv(v))
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(v, orig)
			} else {
				_ = os.Unsetenv(v)
			}
		})
	}
}

func TestLoadAppliesPopulateDefaults(t *testing.T) {
	clearConfigEnv(t)
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
	t.Setenv("KUPO_URL", "http://kupo:1442")
	t.Setenv("SHELLEY_TRANS_EPOCH", "42")

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(""))

	assert.Equal(t, "mempool", c.Input)
	assert.Equal(t, "webhook", c.Output)
	assert.Equal(t, "127.0.0.1", c.Api.ListenAddress)
	assert.Equal(t, uint(9999), c.Api.ListenPort)
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

func TestLoadYAMLErrorBeforeEnv(t *testing.T) {
	t.Setenv("API_PORT", "not-a-number")
	path := filepath.Join(t.TempDir(), "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(":\n  -bad: ["), 0o600))

	c := &Config{ShelleyTransEpoch: -1}
	err := c.Load(path)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "error processing environment")
	assert.Contains(t, err.Error(), "error parsing config file")
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

func TestPrecedenceEnvOverYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("input: yaml-input\n"), 0o600),
	)
	t.Setenv("INPUT", "env-input")

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(path))
	assert.Equal(t, "env-input", c.Input)
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

func TestLoadWithFlagsCLIOverYAML(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(
		t,
		os.WriteFile(path, []byte("input: yaml-value\n"), 0o600),
	)

	c := &Config{ShelleyTransEpoch: -1}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, c.BindFlags(fs))
	require.NoError(t, fs.Parse([]string{"--input=cli-value"}))

	require.NoError(t, c.LoadWithFlags(path, fs))
	assert.Equal(t, "cli-value", c.Input)
}

func TestLoadWithFlagsCLIOverEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INPUT", "env-value")

	c := &Config{ShelleyTransEpoch: -1}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, c.BindFlags(fs))
	require.NoError(t, fs.Parse([]string{"--input=cli-value"}))

	require.NoError(t, c.LoadWithFlags("", fs))
	assert.Equal(t, "cli-value", c.Input)
}

func TestLoadWithFlagsUnsetFlagDefersToEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INPUT", "env-value")

	c := &Config{ShelleyTransEpoch: -1}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, c.BindFlags(fs))
	require.NoError(t, fs.Parse([]string{}))

	require.NoError(t, c.LoadWithFlags("", fs))
	assert.Equal(t, "env-value", c.Input)
}

func TestLoadDelegatesToLoadWithFlags(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("INPUT", "env-value")

	c := &Config{ShelleyTransEpoch: -1}
	require.NoError(t, c.Load(""))
	assert.Equal(t, "env-value", c.Input)
}

func TestLoadCalledTwiceResetsRemovedKey(t *testing.T) {
	clearConfigEnv(t)
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
	assert.Empty(t, c.KupoUrl)
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

func TestReloadPreservesFlagsAndResetsEnvironment(t *testing.T) {
	clearConfigEnv(t)
	c := New()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	require.NoError(t, c.BindFlags(fs))
	require.NoError(
		t,
		fs.Parse([]string{"--api-port=0", "--api-address=", "--input=cli"}),
	)
	t.Setenv("INPUT", "env")
	t.Setenv("OUTPUT", "webhook")
	require.NoError(t, c.LoadWithFlags("", fs))
	require.Equal(t, "cli", c.Input)
	require.Equal(t, "webhook", c.Output)
	require.Zero(t, c.Api.ListenPort)
	require.Empty(t, c.Api.ListenAddress)
	require.NoError(t, os.Unsetenv("OUTPUT"))
	require.NoError(t, c.LoadWithFlags("", fs))
	require.Equal(t, "cli", c.Input)
	require.Equal(t, DefaultOutputPlugin, c.Output)
	require.Equal(t, uint(0), c.Api.ListenPort)
}

func TestStrictYAMLAndAtomicFailure(t *testing.T) {
	clearConfigEnv(t)
	for _, body := range []string{"api:\n  typo: 1\n", "input: chainsync\ninput: mempool\n", "loging: {}\n", "api:\n  port: 65536\n"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		c := New()
		c.Input = "preserved"
		require.Error(t, c.Load(path))
		require.Equal(t, "preserved", c.Input)
	}
}

func TestExplicitZeroGenesis(t *testing.T) {
	clearConfigEnv(t)
	path := filepath.Join(t.TempDir(), "zero.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"byron_genesis:\n  end_slot: 0\n  epoch_length: 0\n  byron_slots_per_epoch: 0\nshelley_genesis:\n  epoch_length: 0\nshelley_trans_epoch: 0\n",
			),
			0o600,
		),
	)
	c := New()
	require.NoError(t, c.Load(path))
	require.Zero(t, *c.ByronGenesis.EndSlot)
	require.Zero(t, c.ByronGenesis.EpochLength)
	require.Zero(t, c.ByronGenesis.ByronSlotsPerEpoch)
	require.Zero(t, c.ShelleyGenesis.EpochLength)
	require.Zero(t, c.ShelleyTransEpoch)
	require.NoError(t, c.Load(""))
	require.Equal(t, int32(208), c.ShelleyTransEpoch)
}

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

package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/SundaeSwap-finance/ogmigo/v6"
	"github.com/blinklabs-io/adder/internal/config"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigureWithWriter_DefaultLevel verifies the logger's default logging behavior
// when configured with the "info" logging level.
func TestConfigureWithWriter_DefaultLevel(t *testing.T) {
	// Ensure config level is set to default/info
	config.GetConfig().Logging.Level = "info"

	var buf bytes.Buffer
	logging.ConfigureWithWriter(&buf)
	logger := logging.GetLogger()

	logger.Debug("this is debug")
	logger.Info("this is info")
	logger.Warn("this is warn")

	decoder := json.NewDecoder(&buf)
	var entries []map[string]any
	for decoder.More() {
		var entry map[string]any
		err := decoder.Decode(&entry)
		require.NoError(t, err)
		entries = append(entries, entry)
	}

	require.Len(t, entries, 2) // Info and Warn
	assert.Equal(t, "this is info", entries[0]["msg"])
	assert.Equal(t, "INFO", entries[0]["level"])
	assert.Contains(t, entries[0], "timestamp")
	assert.NotContains(t, entries[0], "time") // Should be renamed to timestamp
}

// TestConfigureWithWriter_DebugLevel verifies that when set to "debug",
// debug logs are written to the configured output.
func TestConfigureWithWriter_DebugLevel(t *testing.T) {
	config.GetConfig().Logging.Level = "debug"
	defer func() {
		config.GetConfig().Logging.Level = "info"
	}()

	var buf bytes.Buffer
	logging.ConfigureWithWriter(&buf)
	logger := logging.GetLogger()

	logger.Debug("this is debug log")

	var entry map[string]any
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	assert.Equal(t, "this is debug log", entry["msg"])
	assert.Equal(t, "DEBUG", entry["level"])
}

// TestConfigureWithWriter_WarnErrorLevels verifies that when set to "error",
// lower levels such as warning are ignored while error logs are captured.
func TestConfigureWithWriter_WarnErrorLevels(t *testing.T) {
	config.GetConfig().Logging.Level = "error"
	defer func() {
		config.GetConfig().Logging.Level = "info"
	}()

	var buf bytes.Buffer
	logging.ConfigureWithWriter(&buf)
	logger := logging.GetLogger()

	logger.Warn("this is warning log")
	logger.Error("this is error log")

	var entry map[string]any
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	assert.Equal(t, "this is error log", entry["msg"])
	assert.Equal(t, "ERROR", entry["level"])
	assert.NotContains(t, buf.String(), "this is warning log")
}

// TestConfigureWithWriter_LeakProfileAvailability checks the pprof profiles.
func TestConfigureWithWriter_LeakProfileAvailability(t *testing.T) {
	// Under Go 1.26+, if GOEXPERIMENT=goroutineleakprofile is enabled,
	// the "goroutineleak" pprof profile must be registered.
	found := false
	for _, p := range pprof.Profiles() {
		if p.Name() == "goroutineleak" {
			found = true
			break
		}
	}

	// If the profile is found, assert pprof.Lookup can find it as well.
	if found {
		assert.NotNil(t, pprof.Lookup("goroutineleak"))
	}
}

// TestLoggerCreation calls the logger setup function and asserts a non-nil
// logger is returned and behaves correctly for component specific logging.
func TestLoggerCreation(t *testing.T) {
	var buf bytes.Buffer
	logging.ConfigureWithWriter(&buf)

	logger := logging.GetLogger()
	assert.NotNil(t, logger, "global logger should not be nil")

	compLogger := logging.GetLoggerForComponent("test-component")
	assert.NotNil(t, compLogger, "component logger should not be nil")

	// Log from global logger and verify it uses "main" component
	logger.Info("global message")
	// Log from component logger and verify it uses "test-component"
	compLogger.Info("component message")

	decoder := json.NewDecoder(&buf)
	var entries []map[string]any
	for decoder.More() {
		var entry map[string]any
		err := decoder.Decode(&entry)
		require.NoError(t, err)
		entries = append(entries, entry)
	}

	require.Len(t, entries, 2)
	assert.Equal(t, "main", entries[0]["component"])
	assert.Equal(t, "global message", entries[0]["msg"])

	assert.Equal(t, "test-component", entries[1]["component"])
	assert.Equal(t, "component message", entries[1]["msg"])
}

// TestLogLevelConfiguration configures each level (debug, info, warn, error)
// dynamically in config.GetConfig().Logging.Level and asserts that only the
// appropriate logs appear in the captured output.
func TestLogLevelConfiguration(t *testing.T) {
	levels := []struct {
		configLevel string
		shouldLog   map[string]bool
	}{
		{
			configLevel: "debug",
			shouldLog: map[string]bool{
				"DEBUG": true,
				"INFO":  true,
				"WARN":  true,
				"ERROR": true,
			},
		},
		{
			configLevel: "info",
			shouldLog: map[string]bool{
				"DEBUG": false,
				"INFO":  true,
				"WARN":  true,
				"ERROR": true,
			},
		},
		{
			configLevel: "warn",
			shouldLog: map[string]bool{
				"DEBUG": false,
				"INFO":  false,
				"WARN":  true,
				"ERROR": true,
			},
		},
		{
			configLevel: "error",
			shouldLog: map[string]bool{
				"DEBUG": false,
				"INFO":  false,
				"WARN":  false,
				"ERROR": true,
			},
		},
	}

	for _, tc := range levels {
		t.Run(tc.configLevel, func(t *testing.T) {
			// Update global config and restore afterwards
			oldLevel := config.GetConfig().Logging.Level
			config.GetConfig().Logging.Level = tc.configLevel
			defer func() {
				config.GetConfig().Logging.Level = oldLevel
			}()

			var buf bytes.Buffer
			logging.ConfigureWithWriter(&buf)
			logger := logging.GetLogger()

			logger.Debug("debug msg")
			logger.Info("info msg")
			logger.Warn("warn msg")
			logger.Error("error msg")

			decoder := json.NewDecoder(&buf)
			found := map[string]bool{
				"DEBUG": false,
				"INFO":  false,
				"WARN":  false,
				"ERROR": false,
			}
			for decoder.More() {
				var entry map[string]any
				err := decoder.Decode(&entry)
				require.NoError(t, err)
				lvl, ok := entry["level"].(string)
				if ok {
					found[lvl] = true
				}
			}

			for lvl, expected := range tc.shouldLog {
				assert.Equal(t, expected, found[lvl], "level %s presence mismatch for config level %s", lvl, tc.configLevel)
			}
		})
	}
}

// TestLoggerOutput redirects output to a buffer, logs a structured message,
// and parses/asserts the resulting JSON has all required custom fields.
func TestLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	// Ensure info level is set to capture logs
	oldLevel := config.GetConfig().Logging.Level
	config.GetConfig().Logging.Level = "info"
	defer func() {
		config.GetConfig().Logging.Level = oldLevel
	}()

	logging.ConfigureWithWriter(&buf)
	logger := logging.GetLogger()

	// Log structured message with custom attributes
	logger.Info("user login", slog.String("user_id", "user-123"), slog.Int("attempts", 3))

	var entry map[string]any
	err := json.Unmarshal(buf.Bytes(), &entry)
	require.NoError(t, err)

	// Assert standard required fields
	assert.Contains(t, entry, "timestamp", "log entry must contain custom timestamp field")
	assert.NotContains(t, entry, "time", "log entry must not contain standard time field")
	assert.Equal(t, "main", entry["component"], "component must default to main")
	assert.Equal(t, "INFO", entry["level"], "level must be INFO")
	assert.Equal(t, "user login", entry["msg"], "msg must be user login")

	// Assert custom structured attributes
	assert.Equal(t, "user-123", entry["user_id"], "custom string attribute must match")
	assert.Equal(t, float64(3), entry["attempts"], "custom int attribute must match")

	// Validate timestamp format complies with RFC3339
	timestampStr, ok := entry["timestamp"].(string)
	require.True(t, ok, "timestamp must be a string")
	_, err = time.Parse(time.RFC3339, timestampStr)
	assert.NoError(t, err, "timestamp must be a valid RFC3339 formatted string")
}

// TestConfigure verifies that the Configure setup function runs cleanly without panicking.
func TestConfigure(t *testing.T) {
	logging.Configure()
	assert.NotNil(t, logging.GetLogger(), "global logger should be configured and non-nil")
}

// TestKugoCustomLogger verifies the constructor, key-value converter, and logging levels
// of the ogmigo custom logger adapter.
func TestKugoCustomLogger(t *testing.T) {
	// Verify Info-level logger filters out Debug logs using a buffer
	var bufInfo bytes.Buffer
	l := logging.NewKugoCustomLoggerWithWriter(logging.LevelInfo, &bufInfo)
	assert.NotNil(t, l, "NewKugoCustomLogger should return a valid instance")

	l.Info("test info message", ogmigo.KeyValue{Key: "foo", Value: "bar"})
	l.Debug("test debug message (should be skipped)", ogmigo.KeyValue{Key: "ignored", Value: "ignore_me"})

	// Verify Info message is logged and Debug message is skipped
	infoStr := bufInfo.String()
	assert.Contains(t, infoStr, "test info message")
	assert.Contains(t, infoStr, `"foo":"bar"`)
	assert.NotContains(t, infoStr, "test debug message (should be skipped)")

	// Verify Debug-level logger emits Debug logs using a buffer
	var bufDebug bytes.Buffer
	ld := logging.NewKugoCustomLoggerWithWriter(logging.LevelDebug, &bufDebug)
	assert.NotNil(t, ld)

	ld.Debug("test debug message (should log)", ogmigo.KeyValue{Key: "debug_foo", Value: "debug_bar"})
	debugStr := bufDebug.String()
	assert.Contains(t, debugStr, "test debug message (should log)")
	assert.Contains(t, debugStr, `"debug_foo":"debug_bar"`)

	// Verify With returns a distinct logger with context propagated
	var bufWith bytes.Buffer
	cl := logging.NewKugoCustomLoggerWithWriter(logging.LevelDebug, &bufWith)
	assert.NotNil(t, cl)

	cw := cl.With(ogmigo.KeyValue{Key: "with_foo", Value: "with_bar"})
	assert.NotNil(t, cw)
	assert.NotEqual(t, cl, cw, "With should return a distinct logger instance with context")

	// Emit a log using the contextual logger
	cw.Info("context test message")

	// Parse JSON log line to verify that key-value attributes are actually propagated
	var logData map[string]any
	err := json.Unmarshal(bufWith.Bytes(), &logData)
	assert.NoError(t, err, "emitted log should be valid JSON")
	assert.Equal(t, "context test message", logData["msg"])
	assert.Equal(t, "with_bar", logData["with_foo"], "context key-value from With() must be propagated to emitted logs")

	// Verify passing nil writer does not panic and degrades gracefully to discarding logs
	assert.NotPanics(t, func() {
		nilLogger := logging.NewKugoCustomLoggerWithWriter(logging.LevelInfo, nil)
		assert.NotNil(t, nilLogger)
		nilLogger.Info("this should be discarded without panicking")
	})
}

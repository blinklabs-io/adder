// Copyright 2023 Blink Labs Software
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

package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"time"
)

// defaultLogger returns a non-nil logger so globalLogger is never nil at declaration (satisfies nilaway).
func defaultLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("component", "main")
}

var (
	baseLogger   *slog.Logger
	globalLogger = defaultLogger()
)

// Configure initializes the global logger with the resolved output-log level.
func Configure(level slog.Level) {
	ConfigureWithWriter(os.Stderr, level)
}

// ConfigureWithWriter initializes the global logger writing to w. The GUI tray
// uses this to log to a file: when linked with -H=windowsgui there is no
// console, so os.Stderr is discarded and logs (and panics) would otherwise be
// lost.
func ConfigureWithWriter(w io.Writer, level slog.Level) {

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				// Format the time attribute to use RFC3339 or your custom format
				// Rename the time key to timestamp
				return slog.String(
					"timestamp",
					a.Value.Time().Format(time.RFC3339),
				)
			}
			return a
		},
		Level: level,
	})
	baseLogger = slog.New(handler)
	globalLogger = baseLogger.With("component", "main")
}

func GetLogger() *slog.Logger {
	if globalLogger == nil {
		Configure(slog.LevelInfo)
	}
	return globalLogger
}

func GetLoggerForComponent(component string) *slog.Logger {
	if baseLogger == nil {
		Configure(slog.LevelInfo)
	}
	return baseLogger.With("component", component)
}

// ParseLevel validates the log output plugin's named severity threshold.
func ParseLevel(value string) (slog.Level, error) {
	switch value {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, errors.New("level must be debug, info, warn, or error")
	}
}

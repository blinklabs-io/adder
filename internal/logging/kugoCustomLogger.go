// Copyright 2025 Blink Labs Software
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
	"log/slog"
	"os"

	"github.com/SundaeSwap-finance/ogmigo/v6"
)

// LogLevel represents the logging level (either INFO or DEBUG)
type LogLevel int

const (
	LevelInfo LogLevel = iota
	LevelDebug
)

// KugoCustomLogger is a custom logger that uses slog and filters based on the log level
type KugoCustomLogger struct {
	logger   *slog.Logger
	logLevel LogLevel
}

// Info logs info-level messages
func (l *KugoCustomLogger) Info(message string, kvs ...ogmigo.KeyValue) {
	l.logger.Info(message, convertKVs(kvs)...)
}

// Debug logs debug-level messages only if log level is set to DEBUG
func (l *KugoCustomLogger) Debug(message string, kvs ...ogmigo.KeyValue) {
	if l.logLevel >= LevelDebug {
		l.logger.Debug(message, convertKVs(kvs)...)
	}
}

// With returns a new logger with additional context (key-value pairs)
func (l *KugoCustomLogger) With(kvs ...ogmigo.KeyValue) ogmigo.Logger {
	return l // Here we just return the same logger, but you can add more context if needed
}

// Helper function to convert ogmigo.KeyValue to slog key-value format
// Flattens the key-value pairs into a single slice
func convertKVs(kvs []ogmigo.KeyValue) []any {
	result := make([]any, 0, len(kvs)*2)
	for _, kv := range kvs {
		result = append(result, kv.Key, kv.Value)
	}
	return result
}

func NewKugoCustomLogger(level LogLevel) *KugoCustomLogger {
	// Create a new slog logger that logs to stdout using JSON format
	handler := slog.NewJSONHandler(os.Stdout, nil)
	logger := slog.New(handler)

	return &KugoCustomLogger{
		logger:   logger,
		logLevel: level,
	}
}

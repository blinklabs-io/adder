// Copyright 2023 Blink Labs, LLC.
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
	"log"
	"time"

	"github.com/blinklabs-io/snek/internal/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger = zap.SugaredLogger

var globalLogger *Logger

func Configure() {
	cfg := config.GetConfig()
	// Build our custom logging config
	loggerConfig := zap.NewProductionConfig()
	// Change timestamp key name
	loggerConfig.EncoderConfig.TimeKey = "timestamp"
	// Use a human readable time format
	loggerConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	// Set level
	if cfg.Logging.Level != "" {
		level, err := zapcore.ParseLevel(cfg.Logging.Level)
		if err != nil {
			log.Fatalf("error configuring logger: %s", err)
		}
		loggerConfig.Level.SetLevel(level)
	}

	// Create the logger
	l, err := loggerConfig.Build()
	if err != nil {
		log.Fatal(err)
	}

	// Store the "sugared" version of the logger
	globalLogger = l.Sugar()
}

func GetLogger() *zap.SugaredLogger {
	return globalLogger
}

func GetDesugaredLogger() *zap.Logger {
	return globalLogger.Desugar()
}

func GetAccessLogger() *zap.Logger {
	return globalLogger.Desugar().With(zap.String("type", "access")).WithOptions(zap.WithCaller(false))
}

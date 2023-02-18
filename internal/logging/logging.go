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

package plugin

// Logger provides a logging interface for plugins.
type Logger interface {
	Info(string, ...any)
	Warn(string, ...any)
	Debug(string, ...any)
	Error(string, ...any)

	// Deprecated
	// Fatal(string, ...any) in favor of Error
	// With slog Fatal is replaced with Error and os.Exit(1)
}

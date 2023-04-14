package log

type LogOptionFunc func(*LogOutput)

// WithLevel specifies the logging level
func WithLevel(level string) LogOptionFunc {
	return func(o *LogOutput) {
		o.level = level
	}
}

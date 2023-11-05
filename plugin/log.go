package plugin

// Logger provides a logging interface for plugins. This happens to match the interface of uber-go/zap
type Logger interface {
	Infof(string, ...any)
	Warnf(string, ...any)
	Debugf(string, ...any)
	Errorf(string, ...any)
	Fatalf(string, ...any)
}

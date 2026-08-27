package httpserver

type Logger interface {
	Debug(msg string)
	Info(msg string)
	Warn(msg string)
	Error(msg string)
}

type NoopLogger struct{}

func (n NoopLogger) Debug(msg string) {}
func (n NoopLogger) Info(msg string)  {}
func (n NoopLogger) Warn(msg string)  {}
func (n NoopLogger) Error(msg string) {}

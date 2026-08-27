package httpserver

import (
	"log/slog"
	"os"
)

type slogLogger struct {
	logger *slog.Logger
}

func newSlogLogger() Logger {
	return &slogLogger{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)).WithGroup("HTTP_SERVER"),
	}
}

func (l *slogLogger) Debug(msg string) { l.logger.Debug(msg) }
func (l *slogLogger) Info(msg string)  { l.logger.Info(msg) }
func (l *slogLogger) Warn(msg string)  { l.logger.Warn(msg) }
func (l *slogLogger) Error(msg string) { l.logger.Error(msg) }

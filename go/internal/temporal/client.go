package temporal

import (
	"fmt"
	"log/slog"

	"go.temporal.io/sdk/client"
)

// slogTemporalLogger wraps slog.Logger to satisfy temporal client.Logger.
type slogTemporalLogger struct {
	log *slog.Logger
}

func (l *slogTemporalLogger) Debug(msg string, keyvals ...interface{}) {
	l.log.Debug(msg, keyvals...)
}
func (l *slogTemporalLogger) Info(msg string, keyvals ...interface{}) {
	l.log.Info(msg, keyvals...)
}
func (l *slogTemporalLogger) Warn(msg string, keyvals ...interface{}) {
	l.log.Warn(msg, keyvals...)
}
func (l *slogTemporalLogger) Error(msg string, keyvals ...interface{}) {
	l.log.Error(msg, keyvals...)
}

// Connect creates a Temporal client connected to hostPort. logger may be nil.
func Connect(hostPort string, logger *slog.Logger) (client.Client, error) {
	if logger == nil {
		logger = slog.Default()
	}
	opts := client.Options{
		HostPort: hostPort,
		Logger:   &slogTemporalLogger{log: logger},
	}
	c, err := client.Dial(opts)
	if err != nil {
		return nil, fmt.Errorf("temporal: connect to %s: %w", hostPort, err)
	}
	return c, nil
}

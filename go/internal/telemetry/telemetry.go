// Package telemetry configures structured logging (slog) and Prometheus metrics
// for the application. Logging uses JSON format in production and human-readable
// text format in development. The Prometheus registry uses the default registerer
// which already collects Go runtime metrics.
package telemetry

import (
	"log/slog"
	"os"
	"strings"
)

// Telemetry bundles the slog logger for the application.
// Prometheus metrics are served via the default registry through promhttp.Handler()
// mounted directly in the router, which needs no additional configuration here.
type Telemetry struct {
	Logger *slog.Logger
}

// New builds a Telemetry instance. logLevel must be one of DEBUG, INFO, WARN,
// or ERROR (case-insensitive; defaults to INFO). logFormat must be "json" or
// "console" (defaults to "json"). instanceID is attached to every log record as
// the "instance_id" field.
func New(logLevel, logFormat, instanceID string) *Telemetry {
	level := parseLevel(logLevel)

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	if strings.ToLower(logFormat) == "console" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler).With("instance_id", instanceID)

	return &Telemetry{
		Logger: logger,
	}
}

// parseLevel converts a string log level to the corresponding slog.Level,
// defaulting to INFO when the input is unrecognised.
func parseLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Package logging configures slog and redacts known secret shapes from captured
// output and defib's own logs.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Open configures defib's daemon logger: JSON records to the log file at
// path (created 0600, append), at the given config level
// ("debug"|"info"|"warn"|"error"). It returns the logger and a close func.
func Open(path, level string) (*slog.Logger, func() error, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", path, err)
	}

	return New(f, lvl), f.Close, nil
}

// ParseLevel maps the config string to a slog.Level; unknown levels error.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("parse log level %q: unknown level", s)
	}
}

// New returns a JSON logger writing to w at level — the composable core
// that Open wraps; tests and the daemon use it with custom writers.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

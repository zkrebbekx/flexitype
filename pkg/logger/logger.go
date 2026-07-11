// Package logger wraps zerolog behind flexitype's logging conventions:
// structured JSON by default, console format for development, level from
// configuration.
package logger

import (
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// Config controls logger construction.
type Config struct {
	// Level is one of trace|debug|info|warn|error; empty means info.
	Level string
	// Format is "json" (default) or "console".
	Format string
}

// Logger is flexitype's structured logger.
type Logger struct {
	zerolog.Logger
}

// New builds a logger writing to stderr.
func New(cfg Config) *Logger {
	level, err := zerolog.ParseLevel(strings.ToLower(cfg.Level))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}

	var l zerolog.Logger
	if strings.EqualFold(cfg.Format, "console") {
		l = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		l = zerolog.New(os.Stderr)
	}
	l = l.Level(level).With().Timestamp().Logger()
	return &Logger{Logger: l}
}

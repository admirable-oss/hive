package logging

import (
	"fmt"
	"log/slog"
	"strings"
)

// Format selects the log line encoding.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Defaults for a daemon log file.
const (
	DefaultMaxSizeBytes = 10 << 20
	DefaultMaxBackups   = 3
)

// Config describes where and how to log.
type Config struct {
	Level  slog.Level
	Format Format
	// Path is the log file. Empty disables file logging.
	Path string
	// MaxSizeBytes rotates the file once it would grow past this size.
	MaxSizeBytes int64
	// MaxBackups is how many rotated files (path.1 … path.N) are kept.
	MaxBackups int
	// Stderr mirrors every record to standard error.
	Stderr bool
}

func (c Config) withDefaults() Config {
	if c.Format == "" {
		c.Format = FormatText
	}
	if c.MaxSizeBytes <= 0 {
		c.MaxSizeBytes = DefaultMaxSizeBytes
	}
	if c.MaxBackups < 0 {
		c.MaxBackups = 0
	}
	return c
}

// ParseLevel accepts debug, info, warn (or warning) and error, in any case.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown log level %q (want debug, info, warn or error)", s)
}

// ParseFormat accepts text or json.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimSpace(s))); f {
	case "", FormatText:
		return FormatText, nil
	case FormatJSON:
		return f, nil
	}
	return FormatText, fmt.Errorf("unknown log format %q (want text or json)", s)
}

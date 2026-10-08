// Package logging builds the structured loggers Hive uses: log/slog handlers
// writing to a size-rotated file and, optionally, to stderr.
//
// Loggers are passed down explicitly from the composition roots; nothing in
// Hive reads a global logger. Keys follow one vocabulary so logs can be
// filtered across packages: env, process, pid, method, path, err.
package logging

import (
	"io"
	"log/slog"
	"os"
)

// New builds a logger from cfg. The returned close function flushes and
// closes the log file; call it once the logger is no longer used.
func New(cfg Config) (*slog.Logger, func() error, error) {
	cfg = cfg.withDefaults()
	var (
		handlers []slog.Handler
		closers  []io.Closer
	)
	if cfg.Path != "" {
		f, err := OpenRotating(cfg.Path, cfg.MaxSizeBytes, cfg.MaxBackups)
		if err != nil {
			return nil, nil, err
		}
		closers = append(closers, f)
		handlers = append(handlers, newHandler(f, cfg))
	}
	if cfg.Stderr {
		handlers = append(handlers, newHandler(os.Stderr, cfg))
	}

	var h slog.Handler
	switch len(handlers) {
	case 0:
		h = slog.DiscardHandler
	case 1:
		h = handlers[0]
	default:
		h = slog.NewMultiHandler(handlers...)
	}
	closeAll := func() error {
		var first error
		for _, c := range closers {
			if err := c.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	return slog.New(h), closeAll, nil
}

// Discard returns a logger that drops everything. Constructors use it when
// they are given a nil logger, so tests never need to build one.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// OrDiscard returns l, or a discarding logger when l is nil.
func OrDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return Discard()
	}
	return l
}

func newHandler(w io.Writer, cfg Config) slog.Handler {
	opts := &slog.HandlerOptions{Level: cfg.Level}
	if cfg.Format == FormatJSON {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

package terminal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/admirable-oss/hive/internal/vt"
)

// Source selects what Read returns.
type Source string

const (
	// SourceVisible is the screen, without trailing empty lines.
	SourceVisible Source = "visible"
	// SourceRecent is the last lines of history and screen together.
	SourceRecent Source = "recent"
	// SourceRecentUnwrapped is SourceRecent with wrapped lines joined
	// (inferred: a line filling the full width continues on the next).
	SourceRecentUnwrapped Source = "recent-unwrapped"
	// SourceHistory is only the lines that scrolled off the screen.
	SourceHistory Source = "history"
)

// DefaultReadLines is how many lines recent and history reads return when
// the request does not say; MaxReadLines bounds any read.
const (
	DefaultReadLines = 100
	MaxReadLines     = 10_000
	maxPatternLength = 1024
)

// ReadRequest selects part of a terminal's text.
type ReadRequest struct {
	Source Source `json:"source,omitempty"` // default visible
	// Lines limits the result to the last Lines lines (0: the default).
	Lines int `json:"lines,omitempty"`
	// ANSI keeps colours and styles as SGR sequences (not for unwrapped reads).
	ANSI bool `json:"ansi,omitempty"`
}

// WaitRequest waits for output matching Pattern (a Go regular expression,
// matched line by line).
type WaitRequest struct {
	Pattern string `json:"pattern"`
	// Anywhere also matches what is already on the screen and in recent
	// history. By default only output that appears after the wait starts
	// counts, so a prompt already on screen does not end the wait at once.
	Anywhere bool `json:"anywhere,omitempty"`
}

var (
	// ErrEnded means the terminal's output ended before a match.
	ErrEnded = errors.New("terminal output ended without a match")
	// ErrInvalidRead is returned for unknown sources or bad options.
	ErrInvalidRead = errors.New("invalid read")
)

// compilePattern validates a wait pattern.
func compilePattern(p string) (*regexp.Regexp, error) {
	if p == "" || len(p) > maxPatternLength {
		return nil, fmt.Errorf("%w: pattern must be 1..%d characters", ErrInvalidRead, maxPatternLength)
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRead, err)
	}
	return re, nil
}

// readTerminal answers a ReadRequest from an emulated terminal. Callers
// hold the terminal's lock.
func readTerminal(t *vt.Terminal, req ReadRequest) ([]string, error) {
	n := req.Lines
	if n <= 0 {
		n = DefaultReadLines
	}
	n = min(n, MaxReadLines)
	screen := t.Snapshot()
	visible := screen.Lines
	for len(visible) > 0 && vt.LineText(visible[len(visible)-1]) == "" {
		visible = visible[:len(visible)-1]
	}
	format := func(lines [][]vt.Cell) []string {
		out := make([]string, len(lines))
		for i, l := range lines {
			if req.ANSI {
				out[i] = vt.LineANSI(l)
			} else {
				out[i] = vt.LineText(l)
			}
		}
		return out
	}
	switch req.Source {
	case "", SourceVisible:
		if req.Lines > 0 && len(visible) > req.Lines {
			visible = visible[len(visible)-req.Lines:]
		}
		return format(visible), nil
	case SourceHistory:
		return format(t.Scrollback().Tail(n)), nil
	case SourceRecent, SourceRecentUnwrapped:
		lines := append(t.Scrollback().Tail(n), visible...)
		if req.Source == SourceRecentUnwrapped {
			if req.ANSI {
				return nil, fmt.Errorf("%w: unwrapped reads are plain text", ErrInvalidRead)
			}
			out := vt.Unwrap(lines, screen.Cols)
			return out[max(0, len(out)-n):], nil
		}
		return format(lines[max(0, len(lines)-n):]), nil
	}
	return nil, fmt.Errorf("%w: source %q (want visible, recent, recent-unwrapped or history)", ErrInvalidRead, req.Source)
}

// waiter is one WaitOutput call.
type waiter struct {
	re    *regexp.Regexp
	found chan string // receives the matching line
}

// matchLines returns the first line re matches.
func matchLines(re *regexp.Regexp, lines []string) (string, bool) {
	for _, l := range lines {
		if re.MatchString(l) {
			return strings.TrimRight(l, " "), true
		}
	}
	return "", false
}

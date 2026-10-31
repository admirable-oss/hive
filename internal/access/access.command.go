package access

import (
	"errors"
	"strings"
)

// ErrNotProxy is what a member's key answers when ssh is asked to run
// anything but `hive proxy`.
var ErrNotProxy = errors.New("this ssh key reaches only Hive's member socket: connect with `hive --remote` or a saved machine (`hive machine add`); commands that run on the machine as a whole are its owner's")

// CheckOriginal checks the command a member's ssh client asked for
// (SSH_ORIGINAL_COMMAND), which the forced command replaced: only
// `hive proxy`, as remote clients run it, is served. An empty command (an
// interactive login) is refused too.
func CheckOriginal(original string) error {
	words := shellWords(original)
	// Remote clients run `h=…; exec "$h" [--session s] proxy`: what
	// follows the program is what counts.
	for i := len(words) - 1; i >= 0; i-- {
		if words[i] == "$h" || strings.HasSuffix(words[i], "hive") || strings.HasSuffix(words[i], "hive.exe") {
			words = words[i+1:]
			break
		}
	}
	var args []string
	for i := 0; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--session" && i+1 < len(words):
			i++ // a member belongs to the session it was invited to
		case strings.HasPrefix(w, "--session="):
		default:
			args = append(args, w)
		}
	}
	if len(args) == 1 && args[0] == "proxy" {
		return nil
	}
	return ErrNotProxy
}

// shellWords splits s into words the way a POSIX shell would for the
// commands remote clients send: blanks and ';' separate, single and double
// quotes group. It is not a shell; anything it misreads is refused.
func shellWords(s string) []string {
	var (
		words []string
		cur   strings.Builder
		have  bool
		quote rune
	)
	flush := func() {
		if have {
			words = append(words, cur.String())
		}
		cur.Reset()
		have = false
	}
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote, have = r, true
		case r == ' ' || r == '\t' || r == '\n' || r == ';':
			flush()
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	flush()
	return words
}

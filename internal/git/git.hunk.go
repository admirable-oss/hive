package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FilePatch is one file's part of a unified diff, as `git diff` prints
// it: the file's paths, its header and its hunks.
type FilePatch struct {
	// Path is the file after the change; OldPath before it. A new file
	// has no OldPath, a deleted one no Path.
	Path    string `json:"path,omitempty"`
	OldPath string `json:"old_path,omitempty"`
	// Mode is the mode of a new or deleted file ("100644").
	Mode    string `json:"mode,omitempty"`
	New     bool   `json:"new,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
	Hunks   []Hunk `json:"hunks"`
}

// Name is the file's path: after the change, or before it when deleted.
func (f FilePatch) Name() string {
	if f.Path != "" {
		return f.Path
	}
	return f.OldPath
}

// Hunk is one change in a file: where it is, and its lines, each starting
// with ' ' (context), '-' (removed), '+' (added) or '\' (no newline at
// the end of the file).
type Hunk struct {
	// ID names the hunk by its file and its lines, not its position, so it
	// stays the same while other hunks of the file change.
	ID       string   `json:"id"`
	Header   string   `json:"header"`
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Lines    []string `json:"lines"`
}

// ParsePatch splits a unified diff into files and their hunks. What it
// cannot read (a patch cut short, say) ends the file it is in.
func ParsePatch(patch string) []FilePatch {
	var files []FilePatch
	var cur *FilePatch
	var hunk *Hunk
	finish := func() {
		if cur == nil {
			return
		}
		if hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
			hunk = nil
		}
		assignIDs(cur)
		files = append(files, *cur)
		cur = nil
	}
	lines := strings.Split(patch, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			finish()
			cur = &FilePatch{Hunks: []Hunk{}}
			cur.OldPath, cur.Path = gitHeaderPaths(strings.TrimPrefix(line, "diff --git "))
			continue
		}
		if cur == nil {
			continue
		}
		if hunk != nil {
			if remaining(hunk) && line != "" && strings.ContainsRune(" +-", rune(line[0])) {
				hunk.Lines = append(hunk.Lines, line)
				continue
			}
			if strings.HasPrefix(line, `\`) {
				hunk.Lines = append(hunk.Lines, line)
				continue
			}
			if line == "" && remaining(hunk) {
				// An empty context line whose space was lost (some tools trim
				// trailing whitespace from patches).
				hunk.Lines = append(hunk.Lines, " ")
				continue
			}
			cur.Hunks = append(cur.Hunks, *hunk)
			hunk = nil
		}
		switch {
		case strings.HasPrefix(line, "@@ "):
			h, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			hunk = &h
		case strings.HasPrefix(line, "new file mode "):
			cur.New, cur.Mode, cur.OldPath = true, strings.TrimPrefix(line, "new file mode "), ""
		case strings.HasPrefix(line, "deleted file mode "):
			cur.Deleted, cur.Mode, cur.Path = true, strings.TrimPrefix(line, "deleted file mode "), ""
		case strings.HasPrefix(line, "rename from "):
			cur.OldPath = unquotePath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			cur.Path = unquotePath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "--- "):
			if p := patchPath(strings.TrimPrefix(line, "--- "), "a/"); p != "" {
				cur.OldPath = p
			}
		case strings.HasPrefix(line, "+++ "):
			if p := patchPath(strings.TrimPrefix(line, "+++ "), "b/"); p != "" {
				cur.Path = p
			}
		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			cur.Binary = true
		}
	}
	finish()
	return files
}

// remaining reports whether a hunk still expects lines.
func remaining(h *Hunk) bool {
	old, added := 0, 0
	for _, l := range h.Lines {
		switch l[0] {
		case ' ':
			old++
			added++
		case '-':
			old++
		case '+':
			added++
		}
	}
	return old < h.OldLines || added < h.NewLines
}

// parseHunkHeader reads "@@ -a,b +c,d @@ section".
func parseHunkHeader(line string) (Hunk, bool) {
	rest, ok := strings.CutPrefix(line, "@@ -")
	if !ok {
		return Hunk{}, false
	}
	end := strings.Index(rest, " @@")
	if end < 0 {
		return Hunk{}, false
	}
	oldRange, newRange, ok := strings.Cut(rest[:end], " +")
	if !ok {
		return Hunk{}, false
	}
	h := Hunk{Header: line, Lines: []string{}}
	var err1, err2 error
	h.OldStart, h.OldLines, err1 = parseRange(oldRange)
	h.NewStart, h.NewLines, err2 = parseRange(newRange)
	return h, err1 == nil && err2 == nil
}

func parseRange(s string) (start, n int, err error) {
	a, b, found := strings.Cut(s, ",")
	if start, err = strconv.Atoi(a); err != nil {
		return 0, 0, err
	}
	if !found {
		return start, 1, nil
	}
	n, err = strconv.Atoi(b)
	return start, n, err
}

// gitHeaderPaths reads the paths of "a/x b/x", which git writes unquoted
// unless they need quoting; renames and new or deleted files correct them
// from the lines that follow.
func gitHeaderPaths(s string) (oldPath, newPath string) {
	if strings.HasPrefix(s, `"`) {
		if a, rest, ok := cutQuoted(s); ok {
			return strings.TrimPrefix(a, "a/"), strings.TrimPrefix(unquotePath(strings.TrimSpace(rest)), "b/")
		}
	}
	// Unquoted: the two halves are the same length when the path did not
	// change, which is the only case the header alone can settle.
	if n := len(s); n%2 == 1 && s[n/2] == ' ' && strings.HasPrefix(s, "a/") && strings.HasPrefix(s[n/2+1:], "b/") {
		return s[2 : n/2], s[n/2+3:]
	}
	if a, b, ok := strings.Cut(s, " b/"); ok {
		return strings.TrimPrefix(a, "a/"), b
	}
	return "", ""
}

func cutQuoted(s string) (string, string, bool) {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			u, err := strconv.Unquote(s[:i+1])
			return u, s[i+1:], err == nil
		}
	}
	return "", "", false
}

// patchPath reads a "--- a/x" or "+++ b/x" path ("" for /dev/null).
func patchPath(s, prefix string) string {
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(unquotePath(s), prefix)
}

// unquotePath undoes git's C-style quoting of unusual paths.
func unquotePath(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// assignIDs names each hunk of a file by its path and lines; identical
// hunks in one file are told apart by their order.
func assignIDs(f *FilePatch) {
	seen := map[string]int{}
	for i := range f.Hunks {
		id := HunkID(f.Name(), f.Hunks[i].Lines)
		seen[id]++
		if n := seen[id]; n > 1 {
			id = fmt.Sprintf("%s-%d", id, n)
		}
		f.Hunks[i].ID = id
	}
}

// HunkID is the ID of a hunk of the file at path with these lines.
func HunkID(path string, lines []string) string {
	h := sha256.New()
	h.Write([]byte(path))
	for _, l := range lines {
		h.Write([]byte{0})
		h.Write([]byte(l))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// FindHunk returns the hunk with that ID in a file's patch.
func (f FilePatch) FindHunk(id string) (Hunk, bool) {
	for _, h := range f.Hunks {
		if h.ID == id {
			return h, true
		}
	}
	return Hunk{}, false
}

// Single is a patch of one hunk of the file, which `git apply` takes. A
// renamed file's hunk is a change of its new path: rejecting one hunk
// does not undo the rename.
func (f FilePatch) Single(h Hunk) string {
	var sb strings.Builder
	path := quoteForPatch(f.Name())
	fmt.Fprintf(&sb, "diff --git a/%s b/%s\n", path, path)
	switch {
	case f.New:
		fmt.Fprintf(&sb, "new file mode %s\n--- /dev/null\n+++ b/%s\n", f.Mode, path)
	case f.Deleted:
		fmt.Fprintf(&sb, "deleted file mode %s\n--- a/%s\n+++ /dev/null\n", f.Mode, path)
	default:
		fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", path, path)
	}
	sb.WriteString(h.Header)
	sb.WriteByte('\n')
	for _, l := range h.Lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// quoteForPatch quotes a path the way git does when it must.
func quoteForPatch(p string) string {
	for _, r := range p {
		if r < 0x20 || r == '"' || r == '\\' || r >= 0x7f {
			return strconv.Quote(p)
		}
	}
	return p
}

// Apply applies a patch to the working tree of dir (never its index), in
// reverse with reverse. It changes nothing unless the whole patch
// applies.
func (g *Git) Apply(ctx context.Context, dir, patch string, reverse bool) error {
	root, err := g.Root(ctx, dir)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "hive-hunk-*.patch")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(patch); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := []string{"apply", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--reverse")
	}
	_, err = g.Runner.Run(ctx, root, append(args, "--", name)...)
	return err
}

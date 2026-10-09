package git

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// parseStatus reads `git status --porcelain=v2 --branch`.
func parseStatus(out []byte) *Status {
	st := &Status{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			if oid := strings.TrimPrefix(line, "# branch.oid "); oid != "(initial)" {
				st.Head = oid[:min(len(oid), 12)]
			}
		case strings.HasPrefix(line, "# branch.head "):
			if head := strings.TrimPrefix(line, "# branch.head "); head == "(detached)" {
				st.Detached = true
			} else {
				st.Branch = head
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			st.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
		case strings.HasPrefix(line, "# branch.ab "):
			for _, f := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				n, _ := strconv.Atoi(f[1:])
				if f[0] == '+' {
					st.Ahead = n
				} else {
					st.Behind = n
				}
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			if len(line) >= 4 {
				x, y := line[2], line[3]
				if x != '.' {
					st.Staged++
				}
				if y != '.' {
					st.Modified++
				}
			}
		case strings.HasPrefix(line, "u "):
			st.Conflicts++
		case strings.HasPrefix(line, "? "):
			st.Untracked++
		}
	}
	return st
}

// parseWorktrees reads `git worktree list --porcelain`: blocks of
// "key value" lines separated by blank lines; the first is the main tree.
func parseWorktrees(out []byte) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "":
			flush()
		case "worktree":
			flush()
			cur = &Worktree{Path: value, Main: len(list) == 0}
		case "HEAD":
			if cur != nil {
				cur.Head = value[:min(len(value), 12)]
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "locked":
			if cur != nil {
				cur.Locked = true
			}
		}
	}
	flush()
	return list
}

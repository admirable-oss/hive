package git_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/admirable-oss/hive/internal/git"
)

const twoHunks = `diff --git a/cart.go b/cart.go
index 1111111..2222222 100644
--- a/cart.go
+++ b/cart.go
@@ -1,3 +1,4 @@ package cart
 package cart
+// Total adds the items.
 func Total() {}

@@ -10,2 +11,2 @@ func Total() {}
-var a = 1
+var a = 2
 var b = 3
diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.txt
@@ -0,0 +1 @@
+hello
\ No newline at end of file
diff --git a/old.txt b/old.txt
deleted file mode 100755
index 4444444..0000000
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/from.go b/to.go
similarity index 90%
rename from from.go
rename to to.go
index 5555555..6666666 100644
--- a/from.go
+++ b/to.go
@@ -1 +1 @@
-x
+y
diff --git a/img.png b/img.png
index 7777777..8888888 100644
Binary files a/img.png and b/img.png differ
`

func TestParsePatch(t *testing.T) {
	files := git.ParsePatch(twoHunks)
	var names []string
	for _, f := range files {
		names = append(names, f.Name())
	}
	if want := []string{"cart.go", "new.txt", "old.txt", "to.go", "img.png"}; !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	cart := files[0]
	if len(cart.Hunks) != 2 {
		t.Fatalf("cart.go hunks: %+v", cart.Hunks)
	}
	h := cart.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 3 || h.NewStart != 1 || h.NewLines != 4 || len(h.Lines) != 4 {
		t.Fatalf("first hunk: %+v", h)
	}
	if cart.Hunks[1].Lines[0] != "-var a = 1" || cart.Hunks[1].NewStart != 11 {
		t.Fatalf("second hunk: %+v", cart.Hunks[1])
	}
	if !files[1].New || files[1].Mode != "100644" || files[1].OldPath != "" || len(files[1].Hunks[0].Lines) != 2 {
		t.Fatalf("new file: %+v", files[1])
	}
	if !files[2].Deleted || files[2].Mode != "100755" || files[2].Path != "" {
		t.Fatalf("deleted file: %+v", files[2])
	}
	if files[3].OldPath != "from.go" || files[3].Path != "to.go" {
		t.Fatalf("renamed file: %+v", files[3])
	}
	if !files[4].Binary || len(files[4].Hunks) != 0 {
		t.Fatalf("binary file: %+v", files[4])
	}
}

func TestHunkIDsDoNotDependOnPosition(t *testing.T) {
	a := git.ParsePatch(twoHunks)[0].Hunks[1]
	moved := strings.Replace(twoHunks, "@@ -10,2 +11,2 @@", "@@ -40,2 +41,2 @@", 1)
	b := git.ParsePatch(moved)[0].Hunks[1]
	if a.ID != b.ID || a.ID == "" {
		t.Fatalf("a hunk that moved keeps its ID: %q %q", a.ID, b.ID)
	}
	other := git.ParsePatch(strings.Replace(twoHunks, "+var a = 2", "+var a = 9", 1))[0].Hunks[1]
	if other.ID == a.ID {
		t.Fatal("different lines have a different ID")
	}
	dup := `diff --git a/d b/d
--- a/d
+++ b/d
@@ -1 +1 @@
-x
+y
@@ -9 +9 @@
-x
+y
`
	hs := git.ParsePatch(dup)[0].Hunks
	if hs[0].ID == hs[1].ID || hs[1].ID != hs[0].ID+"-2" {
		t.Fatalf("identical hunks in one file are told apart: %q %q", hs[0].ID, hs[1].ID)
	}
}

func TestParsePatchQuotedPaths(t *testing.T) {
	p := "diff --git \"a/caf\\303\\251.txt\" \"b/caf\\303\\251.txt\"\n--- \"a/caf\\303\\251.txt\"\n+++ \"b/caf\\303\\251.txt\"\n@@ -1 +1 @@\n-a\n+b\n" +
		"diff --git a/with space.txt b/with space.txt\n--- a/with space.txt\t\n+++ b/with space.txt\t\n@@ -1 +1 @@\n-a\n+b\n"
	files := git.ParsePatch(p)
	if len(files) != 2 || files[0].Path != "café.txt" || files[1].Path != "with space.txt" {
		t.Fatalf("quoted paths: %+v", files)
	}
}

// TestRejectHunk takes hunks out of a working tree with the patch Single
// makes, as branch.hunk does: one hunk of several, a new file, a deleted
// file.
func TestRejectHunk(t *testing.T) {
	dir := repo(t)
	g := git.New()
	ctx := context.Background()
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = "line " + string(rune('a'+i%26))
	}
	original := strings.Join(lines, "\n") + "\n"
	write(t, filepath.Join(dir, "f.txt"), original)
	write(t, filepath.Join(dir, "gone.txt"), "keep me\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "files")
	head, _ := g.Head(ctx, dir)

	changed := slices.Clone(lines)
	changed[1] = "first change"
	changed[25] = "second change"
	write(t, filepath.Join(dir, "f.txt"), strings.Join(changed, "\n")+"\n")
	write(t, filepath.Join(dir, "added.txt"), "brand new\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	snap, err := g.Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	patch, _, err := g.Patch(ctx, dir, head, snap.Commit, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]git.FilePatch{}
	for _, f := range git.ParsePatch(patch) {
		byName[f.Name()] = f
	}
	f := byName["f.txt"]
	if len(f.Hunks) != 2 {
		t.Fatalf("two hunks in f.txt:\n%s", patch)
	}
	if err := g.Apply(ctx, dir, f.Single(f.Hunks[0]), true); err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(lines)
	want[25] = "second change"
	if got, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(got) != strings.Join(want, "\n")+"\n" {
		t.Fatalf("after rejecting the first hunk:\n%s", got)
	}
	// The same hunk again no longer applies: nothing changes.
	if err := g.Apply(ctx, dir, f.Single(f.Hunks[0]), true); err == nil {
		t.Fatal("a hunk already taken out does not apply again")
	}

	added := byName["added.txt"]
	if err := g.Apply(ctx, dir, added.Single(added.Hunks[0]), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("rejecting a new file removes it: %v", err)
	}
	gone := byName["gone.txt"]
	if err := g.Apply(ctx, dir, gone.Single(gone.Hunks[0]), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "gone.txt")); string(got) != "keep me\n" {
		t.Fatalf("rejecting a deletion brings the file back: %q", got)
	}
	if st := run(t, dir, "diff", "--cached", "--name-only"); st != "" {
		t.Fatalf("the index is left alone: %q", st)
	}
}

func FuzzParsePatch(f *testing.F) {
	f.Add(twoHunks)
	f.Add("diff --git a/x b/x\n@@ -1 +1 @@\n")
	f.Add("diff --git \"a/\\\"\" \"b/\\\"\"\n")
	f.Fuzz(func(t *testing.T, patch string) {
		for _, file := range git.ParsePatch(patch) {
			ids := map[string]bool{}
			for _, h := range file.Hunks {
				if h.ID == "" || ids[h.ID] {
					t.Fatalf("hunk IDs are set and unique in a file: %+v", file.Hunks)
				}
				ids[h.ID] = true
				// A single hunk's patch parses back to that hunk.
				again := git.ParsePatch(file.Single(h))
				if len(again) != 1 || len(again[0].Hunks) != 1 || !slices.Equal(again[0].Hunks[0].Lines, h.Lines) {
					if file.Name() != "" && !strings.ContainsAny(file.Name(), "\n\r") && complete(h) {
						t.Fatalf("Single does not round-trip:\n%q\n%+v", file.Single(h), again)
					}
				}
			}
		}
	})
}

// complete reports whether a hunk has the lines its header promises.
func complete(h git.Hunk) bool {
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
	return old == h.OldLines && added == h.NewLines
}

package gitdiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fixture builds a repo: one commit on main, a feature branch with a
// committed change, plus a staged edit, an unstaged edit, and an
// untracked file.
func fixture(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-b", "main")
	write("main.go", "package main\n\nfunc main() {\n\tprintln(\"one\")\n}\n")
	write("keep.txt", "unchanged\n")
	run("add", ".")
	run("commit", "-m", "base")

	run("checkout", "-b", "feature")
	write("main.go", "package main\n\nfunc main() {\n\tprintln(\"two\")\n}\n")
	run("commit", "-am", "committed change")

	write("main.go", "package main\n\nfunc main() {\n\tprintln(\"three\")\n}\n")
	write("untracked.txt", "brand new\nfile\n")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func fileByPath(t *testing.T, d *Diff, path string) *File {
	t.Helper()
	for _, f := range d.Files {
		if f.Path() == path {
			return f
		}
	}
	t.Fatalf("file %s not in diff: %+v", path, d.Files)
	return nil
}

func TestWorktreeHead(t *testing.T) {
	repo := fixture(t)
	d, err := repo.DiffRange(RangeOptions{Base: "HEAD", Head: Worktree})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 2 {
		t.Fatalf("expected main.go + untracked.txt, got %+v", d.Files)
	}
	mod := fileByPath(t, d, "main.go")
	if mod.Status != Modified || mod.Additions != 1 || mod.Deletions != 1 {
		t.Fatalf("main.go: %+v", mod)
	}
	var sawDel, sawAdd bool
	for _, row := range mod.Hunks[0].Rows {
		switch row.Kind {
		case Del:
			sawDel = row.Text == "\tprintln(\"two\")" && row.OldLine == 4 && row.NewLine == 0
		case Add:
			sawAdd = row.Text == "\tprintln(\"three\")" && row.NewLine == 4 && row.OldLine == 0
		}
	}
	if !sawDel || !sawAdd {
		t.Fatalf("rows wrong: %+v", mod.Hunks[0].Rows)
	}

	unt := fileByPath(t, d, "untracked.txt")
	if unt.Status != Added || unt.Additions != 2 || len(unt.Hunks) != 1 {
		t.Fatalf("untracked: %+v", unt)
	}
	if d.HeadSHA != "" {
		t.Fatalf("worktree head must have no SHA: %+v", d)
	}
	if d.BaseSHA == "" {
		t.Fatal("base SHA should resolve")
	}
}

func TestCommitToCommitRange(t *testing.T) {
	repo := fixture(t)
	d, err := repo.DiffRange(RangeOptions{Base: "main", Head: "feature", MergeBase: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 || d.Files[0].Path() != "main.go" {
		t.Fatalf("commit range should only see the committed change: %+v", d.Files)
	}
	var texts []string
	for _, row := range d.Files[0].Hunks[0].Rows {
		if row.Kind == Add {
			texts = append(texts, row.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "\tprintln(\"two\")" {
		t.Fatalf("adds: %+v", texts)
	}
	if d.HeadSHA == "" || d.BaseSHA == "" {
		t.Fatalf("resolved SHAs missing: %+v", d)
	}
}

func TestMergeBaseFromWorktree(t *testing.T) {
	repo := fixture(t)
	d, err := repo.DiffRange(RangeOptions{Base: "main", Head: Worktree, MergeBase: true})
	if err != nil {
		t.Fatal(err)
	}
	mod := fileByPath(t, d, "main.go")
	var adds []string
	for _, row := range mod.Hunks[0].Rows {
		if row.Kind == Add {
			adds = append(adds, row.Text)
		}
	}
	// merge-base(main, HEAD) → worktree sees "three", not "two".
	if len(adds) != 1 || adds[0] != "\tprintln(\"three\")" {
		t.Fatalf("adds: %+v", adds)
	}
	fileByPath(t, d, "untracked.txt")
}

func TestIndexHead(t *testing.T) {
	repo := fixture(t)
	// Stage the worktree edit, then dirty the worktree again.
	if _, err := repo.git("add", "main.go"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo.Root, "main.go"),
		[]byte("package main\n\nfunc main() {\n\tprintln(\"four\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := repo.DiffRange(RangeOptions{Base: "HEAD", Head: Index})
	if err != nil {
		t.Fatal(err)
	}
	mod := fileByPath(t, d, "main.go")
	var adds []string
	for _, row := range mod.Hunks[0].Rows {
		if row.Kind == Add {
			adds = append(adds, row.Text)
		}
	}
	// The index has "three"; the worktree "four" must not appear.
	if len(adds) != 1 || adds[0] != "\tprintln(\"three\")" {
		t.Fatalf("index adds: %+v", adds)
	}
	for _, f := range d.Files {
		if f.Path() == "untracked.txt" {
			t.Fatal("untracked files must not appear for an index head")
		}
	}
}

func TestFileLines(t *testing.T) {
	repo := fixture(t)
	head, err := repo.FileLines("HEAD", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if head[3] != "\tprintln(\"two\")" {
		t.Fatalf("HEAD content: %+v", head)
	}
	wt, err := repo.FileLines(Worktree, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if wt[3] != "\tprintln(\"three\")" {
		t.Fatalf("worktree content: %+v", wt)
	}
	if _, err := repo.FileLines(Worktree, "../escape"); err == nil {
		t.Fatal("path traversal must be rejected")
	}
}

func TestListFiles(t *testing.T) {
	repo := fixture(t)
	wt, err := repo.ListFiles(Worktree)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"keep.txt": true, "main.go": true, "untracked.txt": true}
	if len(wt) != 3 {
		t.Fatalf("worktree files: %+v", wt)
	}
	for _, f := range wt {
		if !want[f] {
			t.Fatalf("unexpected file %q in %+v", f, wt)
		}
	}
	ref, err := repo.ListFiles("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(ref) != 2 {
		t.Fatalf("main files should exclude untracked: %+v", ref)
	}
}

func TestLogLanes(t *testing.T) {
	repo := fixture(t)
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo.Root,
			"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Add a merge so the graph has parallel lanes; the fixture's dirty
	// worktree is irrelevant here, so discard it.
	mustGit("checkout", "-f", "-b", "side", "main")
	if err := os.WriteFile(filepath.Join(repo.Root, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", "side.txt")
	mustGit("commit", "-m", "side work")
	mustGit("merge", "--no-ff", "feature", "-m", "merge feature")

	rows, err := repo.Log(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 4 {
		t.Fatalf("expected at least 4 commits, got %d", len(rows))
	}
	merge := rows[0]
	if merge.Subject != "merge feature" || len(merge.Parents) != 2 {
		t.Fatalf("newest row should be the merge: %+v", merge.Commit)
	}
	if len(merge.Fork) != 2 {
		t.Fatalf("merge commit should fork to two parent lanes: %+v", merge)
	}
	// Somewhere below, two lanes must be active at once.
	sawParallel := false
	for _, row := range rows {
		if row.NLanes >= 2 {
			sawParallel = true
		}
		if row.Dot >= row.NLanes {
			t.Fatalf("dot outside lanes: %+v", row)
		}
	}
	if !sawParallel {
		t.Fatal("expected parallel lanes after a merge")
	}
	// The root commit has no forks.
	root := rows[len(rows)-1]
	if len(root.Parents) != 0 || len(root.Fork) != 0 {
		t.Fatalf("root row wrong: %+v", root)
	}
}

func TestStateFingerprintChanges(t *testing.T) {
	repo := fixture(t)
	a := repo.StateFingerprint()
	if err := os.WriteFile(filepath.Join(repo.Root, "keep.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b := repo.StateFingerprint(); a == b {
		t.Fatal("fingerprint should change when the worktree changes")
	}
}

func TestIgnoreWhitespace(t *testing.T) {
	repo := fixture(t)
	// Whitespace-only edit to keep.txt: same content, extra trailing spaces.
	if err := os.WriteFile(filepath.Join(repo.Root, "keep.txt"), []byte("unchanged   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain, err := repo.DiffRange(RangeOptions{Base: "HEAD", Head: Worktree})
	if err != nil {
		t.Fatal(err)
	}
	fileByPath(t, plain, "keep.txt") // visible without -w

	ws, err := repo.DiffRange(RangeOptions{Base: "HEAD", Head: Worktree, IgnoreWhitespace: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ws.Files {
		if f.Path() == "keep.txt" {
			t.Fatalf("whitespace-only change should drop out with -w: %+v", f)
		}
	}
}

func TestHunkOrdinals(t *testing.T) {
	repo := fixture(t)
	d, err := repo.DiffRange(RangeOptions{Base: "HEAD", Head: Worktree})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range d.Files {
		for i, h := range f.Hunks {
			if h.Ordinal != i+1 {
				t.Fatalf("%s hunk %d has ordinal %d", f.Path(), i, h.Ordinal)
			}
		}
	}
	if got := HunkID("a/b.go", 2); got != "a/b.go:h2" {
		t.Fatalf("HunkID: %s", got)
	}
}

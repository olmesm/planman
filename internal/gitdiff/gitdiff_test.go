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

func TestWorkingScope(t *testing.T) {
	repo := fixture(t)
	d, err := repo.Diff(Options{Scope: ScopeWorking})
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
	if unt.Hunks[0].Rows[0].Text != "brand new" || unt.Hunks[0].Rows[0].NewLine != 1 {
		t.Fatalf("untracked rows: %+v", unt.Hunks[0].Rows)
	}
}

func TestBranchScope(t *testing.T) {
	repo := fixture(t)
	d, err := repo.Diff(Options{Scope: ScopeBranch, Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 1 || d.Files[0].Path() != "main.go" {
		t.Fatalf("branch scope should only see the committed change: %+v", d.Files)
	}
	if d.Base != "main" {
		t.Fatalf("base not recorded: %q", d.Base)
	}
	var texts []string
	for _, row := range d.Files[0].Hunks[0].Rows {
		if row.Kind == Add {
			texts = append(texts, row.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "\tprintln(\"two\")" {
		t.Fatalf("branch adds: %+v", texts)
	}
}

func TestAllScope(t *testing.T) {
	repo := fixture(t)
	d, err := repo.Diff(Options{Scope: ScopeAll, Base: "main"})
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
	// all = merge-base → worktree, so it sees "three", not "two".
	if len(adds) != 1 || adds[0] != "\tprintln(\"three\")" {
		t.Fatalf("all adds: %+v", adds)
	}
	fileByPath(t, d, "untracked.txt")
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
	wt, err := repo.FileLines("", "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if wt[3] != "\tprintln(\"three\")" {
		t.Fatalf("worktree content: %+v", wt)
	}
	if _, err := repo.FileLines("", "../escape"); err == nil {
		t.Fatal("path traversal must be rejected")
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

func TestParseScope(t *testing.T) {
	if s, err := ParseScope(""); err != nil || s != ScopeWorking {
		t.Fatalf("empty scope: %v %v", s, err)
	}
	if _, err := ParseScope("bogus"); err == nil {
		t.Fatal("bogus scope must error")
	}
}

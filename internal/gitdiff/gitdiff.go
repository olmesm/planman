// Package gitdiff produces the diff model for a repository by driving
// the git CLI and parsing its output with sourcegraph/go-diff. It knows
// three scopes, mirroring what a reviewer wants to see before a PR
// exists: the working tree vs HEAD, the branch vs a base ref, or
// everything since the merge-base including uncommitted changes.
package gitdiff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sourcegraph/go-diff/diff"
)

// Scope selects which changes a diff covers.
type Scope string

const (
	// ScopeWorking is staged + unstaged + untracked changes vs HEAD.
	ScopeWorking Scope = "working"
	// ScopeBranch is committed changes vs the merge-base with a base ref.
	ScopeBranch Scope = "branch"
	// ScopeAll is ScopeBranch plus everything uncommitted.
	ScopeAll Scope = "all"
)

// ParseScope validates a scope string, treating empty as working.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeWorking, "":
		return ScopeWorking, nil
	case ScopeBranch:
		return ScopeBranch, nil
	case ScopeAll:
		return ScopeAll, nil
	}
	return "", fmt.Errorf("invalid scope %q (working, branch, or all)", s)
}

// Repo is an opened git repository.
type Repo struct {
	Root   string // absolute path to the worktree root
	GitDir string // absolute path to the .git directory
}

// Open locates the repository containing path.
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root, err := gitOutput(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository", abs)
	}
	gitDir, err := gitOutput(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return nil, err
	}
	return &Repo{Root: root, GitDir: gitDir}, nil
}

// git runs a git command in the repo and returns whitespace-trimmed
// stdout — suitable for refs and SHAs, not file or patch content.
func (r *Repo) git(args ...string) (string, error) {
	return gitOutput(r.Root, args...)
}

// gitRaw runs a git command and returns stdout verbatim, for output
// where whitespace is significant (patches, file contents).
func (r *Repo) gitRaw(args ...string) (string, error) {
	out, err := runGit(r.Root, args...)
	return out, err
}

func gitOutput(dir string, args ...string) (string, error) {
	out, err := runGit(dir, args...)
	return strings.TrimSpace(out), err
}

func runGit(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir, "-c", "core.quotepath=false"}, args...)
	cmd := exec.Command("git", full...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out.String(), nil
}

// DefaultBase picks a sensible base ref: the remote default branch if
// known, otherwise main or master, otherwise HEAD.
func (r *Repo) DefaultBase() string {
	if ref, err := r.git("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref
	}
	for _, ref := range []string{"main", "master"} {
		if _, err := r.git("rev-parse", "--verify", "--quiet", "refs/heads/"+ref); err == nil {
			return ref
		}
	}
	return "HEAD"
}

// HeadSHA returns the current HEAD commit, or "" on an unborn branch.
func (r *Repo) HeadSHA() string {
	sha, err := r.git("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return sha
}

// StateFingerprint summarizes everything a diff depends on — HEAD, the
// worktree status, and the size/mtime of untracked files (status only
// names those, so their content edits would otherwise go unseen) — so
// callers can poll for changes cheaply.
func (r *Repo) StateFingerprint() string {
	status, _ := r.git("status", "--porcelain")
	h := sha256.New()
	h.Write([]byte(r.HeadSHA() + "\x00" + status))
	for _, line := range strings.Split(status, "\n") {
		rel, ok := strings.CutPrefix(line, "?? ")
		if !ok {
			continue
		}
		if fi, err := os.Stat(filepath.Join(r.Root, rel)); err == nil {
			fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// Options selects what to diff.
type Options struct {
	Scope Scope
	Base  string // ref for branch/all scopes; empty means DefaultBase
}

// LineKind classifies a diff row.
type LineKind int

const (
	Context LineKind = iota
	Add
	Del
)

// Row is one line of a hunk. OldLine/NewLine are 1-based line numbers on
// their side, 0 when the row does not exist on that side.
type Row struct {
	Kind    LineKind
	OldLine int
	NewLine int
	Text    string
}

// Hunk is one contiguous run of changes with its context lines.
type Hunk struct {
	OldStart, OldCount int
	NewStart, NewCount int
	Section            string
	Rows               []Row
}

// FileStatus classifies a changed file.
type FileStatus string

const (
	Added    FileStatus = "added"
	Deleted  FileStatus = "deleted"
	Modified FileStatus = "modified"
	Renamed  FileStatus = "renamed"
)

// File is one changed file in the diff.
type File struct {
	OldPath, NewPath     string
	Status               FileStatus
	Binary               bool
	Additions, Deletions int
	Hunks                []*Hunk
	// Fingerprint identifies this file's patch content, for client-side
	// state (viewed, collapsed) that should reset when the patch changes.
	Fingerprint string
}

// Path is the file's display path: the new path unless deleted.
func (f *File) Path() string {
	if f.Status == Deleted {
		return f.OldPath
	}
	return f.NewPath
}

// Diff is the parsed changeset.
type Diff struct {
	Scope Scope
	Base  string // resolved base ref ("" for working scope)
	Files []*File
}

// TotalAdditions sums additions across files.
func (d *Diff) TotalAdditions() int {
	n := 0
	for _, f := range d.Files {
		n += f.Additions
	}
	return n
}

// TotalDeletions sums deletions across files.
func (d *Diff) TotalDeletions() int {
	n := 0
	for _, f := range d.Files {
		n += f.Deletions
	}
	return n
}

// Diff computes the changeset for the given options.
func (r *Repo) Diff(opts Options) (*Diff, error) {
	scope, err := ParseScope(string(opts.Scope))
	if err != nil {
		return nil, err
	}
	base := opts.Base
	if base == "" {
		base = r.DefaultBase()
	}

	d := &Diff{Scope: scope}
	head := r.HeadSHA()
	diffArgs := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames",
		"--src-prefix=a/", "--dst-prefix=b/", "-U3"}

	var patch string
	switch scope {
	case ScopeWorking:
		if head != "" {
			patch, err = r.gitRaw(append(diffArgs, "HEAD")...)
			if err != nil {
				return nil, err
			}
		}
	case ScopeBranch, ScopeAll:
		mb, mbErr := r.git("merge-base", base, "HEAD")
		if mbErr != nil {
			return nil, fmt.Errorf("cannot resolve base %q: %w", base, mbErr)
		}
		d.Base = base
		args := append(diffArgs, mb)
		if scope == ScopeBranch {
			args = append(args, "HEAD")
		}
		patch, err = r.gitRaw(args...)
		if err != nil {
			return nil, err
		}
	}

	if strings.TrimSpace(patch) != "" {
		files, err := parsePatch(patch)
		if err != nil {
			return nil, fmt.Errorf("parse diff: %w", err)
		}
		d.Files = files
	}

	// Untracked files never appear in git diff output; synthesize them
	// as added files for the scopes that show uncommitted changes.
	if scope == ScopeWorking || scope == ScopeAll {
		untracked, err := r.untrackedFiles()
		if err != nil {
			return nil, err
		}
		d.Files = append(d.Files, untracked...)
	}

	sort.SliceStable(d.Files, func(i, j int) bool { return d.Files[i].Path() < d.Files[j].Path() })
	return d, nil
}

// parsePatch turns raw `git diff` output into the file model.
func parsePatch(patch string) ([]*File, error) {
	fds, err := diff.ParseMultiFileDiff([]byte(patch))
	if err != nil {
		return nil, err
	}
	var files []*File
	for _, fd := range fds {
		f := &File{
			OldPath: stripPrefix(fd.OrigName),
			NewPath: stripPrefix(fd.NewName),
		}
		switch {
		case fd.OrigName == "/dev/null":
			f.Status = Added
		case fd.NewName == "/dev/null":
			f.Status = Deleted
		case isRename(fd.Extended):
			f.Status = Renamed
		default:
			f.Status = Modified
		}
		f.Binary = isBinary(fd.Extended)

		hasher := sha256.New()
		hasher.Write([]byte(f.OldPath + "\x00" + f.NewPath))
		for _, h := range fd.Hunks {
			hunk := &Hunk{
				OldStart: int(h.OrigStartLine),
				OldCount: int(h.OrigLines),
				NewStart: int(h.NewStartLine),
				NewCount: int(h.NewLines),
				Section:  h.Section,
			}
			hasher.Write(h.Body)
			oldN, newN := hunk.OldStart, hunk.NewStart
			for _, raw := range strings.Split(string(h.Body), "\n") {
				if raw == "" || raw[0] == '\\' { // "\ No newline at end of file"
					continue
				}
				text := raw[1:]
				switch raw[0] {
				case '+':
					hunk.Rows = append(hunk.Rows, Row{Kind: Add, NewLine: newN, Text: text})
					newN++
					f.Additions++
				case '-':
					hunk.Rows = append(hunk.Rows, Row{Kind: Del, OldLine: oldN, Text: text})
					oldN++
					f.Deletions++
				default:
					hunk.Rows = append(hunk.Rows, Row{Kind: Context, OldLine: oldN, NewLine: newN, Text: text})
					oldN++
					newN++
				}
			}
			f.Hunks = append(f.Hunks, hunk)
		}
		f.Fingerprint = hex.EncodeToString(hasher.Sum(nil))[:12]
		files = append(files, f)
	}
	return files, nil
}

func stripPrefix(name string) string {
	if name == "/dev/null" {
		return ""
	}
	if rest, ok := strings.CutPrefix(name, "a/"); ok {
		return rest
	}
	if rest, ok := strings.CutPrefix(name, "b/"); ok {
		return rest
	}
	return name
}

func isRename(extended []string) bool {
	for _, l := range extended {
		if strings.HasPrefix(l, "rename from ") {
			return true
		}
	}
	return false
}

func isBinary(extended []string) bool {
	for _, l := range extended {
		if strings.HasPrefix(l, "Binary files ") || strings.HasPrefix(l, "GIT binary patch") {
			return true
		}
	}
	return false
}

// untrackedFiles synthesizes added-file entries for untracked files.
func (r *Repo) untrackedFiles() ([]*File, error) {
	out, err := r.git("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []*File
	for _, rel := range strings.Split(out, "\n") {
		if rel == "" {
			continue
		}
		f := &File{NewPath: rel, Status: Added}
		b, err := os.ReadFile(filepath.Join(r.Root, rel))
		if err != nil {
			continue // vanished mid-scan; skip
		}
		hasher := sha256.New()
		hasher.Write([]byte("untracked\x00" + rel + "\x00"))
		hasher.Write(b)
		f.Fingerprint = hex.EncodeToString(hasher.Sum(nil))[:12]
		if bytes.IndexByte(b, 0) >= 0 {
			f.Binary = true
			files = append(files, f)
			continue
		}
		lines := splitLines(string(b))
		hunk := &Hunk{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines)}
		for i, line := range lines {
			hunk.Rows = append(hunk.Rows, Row{Kind: Add, NewLine: i + 1, Text: line})
		}
		f.Additions = len(lines)
		if len(lines) > 0 {
			f.Hunks = []*Hunk{hunk}
		}
		files = append(files, f)
	}
	return files, nil
}

// FileLines returns a file's content as lines. ref "" reads the
// worktree; otherwise the file is read from that revision.
func (r *Repo) FileLines(ref, path string) ([]string, error) {
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid path %q", path)
	}
	if ref == "" {
		b, err := os.ReadFile(filepath.Join(r.Root, path))
		if err != nil {
			return nil, err
		}
		return splitLines(string(b)), nil
	}
	out, err := r.gitRaw("show", ref+":"+path)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

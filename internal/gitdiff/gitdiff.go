// Package gitdiff produces the diff model for a repository by driving
// the git CLI and parsing its output with sourcegraph/go-diff. A diff is
// defined by a range — any base commit compared against any head, where
// the head may also be the working tree or the index — with optional
// merge-base resolution, which is what a reviewer almost always wants
// when comparing branches.
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

// Pseudo-head refs: review endpoints that are not commits.
const (
	// Worktree compares against the working tree (uncommitted state,
	// untracked files included).
	Worktree = "@worktree"
	// Index compares against the staging area.
	Index = "@index"
)

// IsPseudo reports whether ref names a non-commit endpoint.
func IsPseudo(ref string) bool { return ref == Worktree || ref == Index }

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

// ResolveSHA resolves a ref (or pseudo-head) to a commit SHA. Pseudo
// heads resolve to "" — they have no commit identity.
func (r *Repo) ResolveSHA(ref string) (string, error) {
	if IsPseudo(ref) {
		return "", nil
	}
	return r.git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// MergeBase returns the merge base of two commits.
func (r *Repo) MergeBase(a, b string) (string, error) {
	return r.git("merge-base", a, b)
}

// ShortSHA abbreviates a SHA for display.
func ShortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// StateFingerprint summarizes everything a worktree-facing diff depends
// on — HEAD, the worktree status, and the size/mtime of untracked files
// (status only names those, so their content edits would otherwise go
// unseen) — so callers can poll for changes cheaply.
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

// RangeOptions selects what to diff.
type RangeOptions struct {
	Base string // ref or SHA; "" means DefaultBase
	Head string // ref, SHA, Worktree, or Index; "" means Worktree
	// MergeBase diffs from merge-base(Base, Head) instead of Base
	// itself — three-dot semantics, the usual choice for branch review.
	MergeBase bool
	// IgnoreWhitespace passes -w: whitespace-only changes drop out of
	// the patch. File fingerprints change with it, so client-side viewed
	// state resets for affected files.
	IgnoreWhitespace bool
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
	// Ordinal is the hunk's 1-based position within its file, giving it
	// a stable id (path:hN) for the current diff — used by walkthroughs
	// and keyboard navigation. Ids are only meaningful against the diff
	// they were computed from; any change to the range renumbers them.
	Ordinal int
}

// HunkID names a hunk within the current diff: "path:hN".
func HunkID(path string, ordinal int) string {
	return fmt.Sprintf("%s:h%d", path, ordinal)
}

// ID is the hunk's id within the given file path.
func (h *Hunk) ID(path string) string { return HunkID(path, h.Ordinal) }

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

// Diff is the parsed changeset for a range.
type Diff struct {
	Base string // requested base (symbolic)
	Head string // requested head (symbolic or pseudo)
	// BaseSHA is the resolved effective base — the merge-base when
	// MergeBase was set. "" when the repo has no commits yet.
	BaseSHA string
	// HeadSHA is the resolved head commit; "" for pseudo heads.
	HeadSHA string
	Files   []*File
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

// DiffRange computes the changeset between a base and a head.
func (r *Repo) DiffRange(o RangeOptions) (*Diff, error) {
	base := o.Base
	if base == "" {
		base = r.DefaultBase()
	}
	head := o.Head
	if head == "" {
		head = Worktree
	}
	if IsPseudo(base) {
		return nil, fmt.Errorf("base must be a commit, not %s", base)
	}

	d := &Diff{Base: base, Head: head}

	// Resolve the head commit the comparison is anchored to.
	headCommittish := head
	if IsPseudo(head) {
		headCommittish = "HEAD"
	}
	headSHA, headErr := r.ResolveSHA(headCommittish)
	if !IsPseudo(head) {
		if headErr != nil {
			return nil, fmt.Errorf("cannot resolve head %q: %w", head, headErr)
		}
		d.HeadSHA = headSHA
	}

	// Resolve the effective base: merge-base against the head when asked.
	effBase, baseErr := r.ResolveSHA(base)
	if baseErr == nil && o.MergeBase && headSHA != "" {
		if mb, err := r.MergeBase(effBase, headSHA); err == nil && mb != "" {
			effBase = mb
		}
	}
	unborn := baseErr != nil && IsPseudo(head) && r.HeadSHA() == ""
	if baseErr != nil && !unborn {
		return nil, fmt.Errorf("cannot resolve base %q: %w", base, baseErr)
	}
	d.BaseSHA = effBase
	if unborn {
		d.BaseSHA = ""
	}

	diffArgs := []string{"diff", "--no-color", "--no-ext-diff", "--find-renames",
		"--src-prefix=a/", "--dst-prefix=b/", "-U3"}
	if o.IgnoreWhitespace {
		diffArgs = append(diffArgs, "-w")
	}

	var patch string
	var err error
	switch {
	case head == Worktree:
		if !unborn {
			patch, err = r.gitRaw(append(diffArgs, d.BaseSHA)...)
		}
	case head == Index:
		args := append(diffArgs, "--cached")
		if !unborn {
			args = append(args, d.BaseSHA)
		}
		patch, err = r.gitRaw(args...)
	default:
		patch, err = r.gitRaw(append(diffArgs, d.BaseSHA, d.HeadSHA)...)
	}
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(patch) != "" {
		files, err := parsePatch(patch)
		if err != nil {
			return nil, fmt.Errorf("parse diff: %w", err)
		}
		d.Files = files
	}

	// Untracked files never appear in git diff output; synthesize them
	// as added files when the head is the working tree.
	if head == Worktree {
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
		for hi, h := range fd.Hunks {
			hunk := &Hunk{
				OldStart: int(h.OrigStartLine),
				OldCount: int(h.OrigLines),
				NewStart: int(h.NewStartLine),
				NewCount: int(h.NewLines),
				Section:  h.Section,
				Ordinal:  hi + 1,
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
		hunk := &Hunk{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines), Ordinal: 1}
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

// FileLines returns a file's content as lines at the given endpoint:
// Worktree reads the file on disk, Index reads the staged copy, and any
// other ref reads that revision.
func (r *Repo) FileLines(ref, path string) ([]string, error) {
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid path %q", path)
	}
	switch ref {
	case Worktree, "":
		b, err := os.ReadFile(filepath.Join(r.Root, path))
		if err != nil {
			return nil, err
		}
		return splitLines(string(b)), nil
	case Index:
		out, err := r.gitRaw("show", ":"+path)
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	default:
		out, err := r.gitRaw("show", ref+":"+path)
		if err != nil {
			return nil, err
		}
		return splitLines(out), nil
	}
}

// FileBytes returns a file's raw content at the given endpoint, for
// serving binary blobs (image previews).
func (r *Repo) FileBytes(ref, path string) ([]byte, error) {
	if strings.Contains(path, "..") {
		return nil, fmt.Errorf("invalid path %q", path)
	}
	switch ref {
	case Worktree, "":
		return os.ReadFile(filepath.Join(r.Root, path))
	case Index:
		out, err := r.gitRaw("show", ":"+path)
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	default:
		out, err := r.gitRaw("show", ref+":"+path)
		if err != nil {
			return nil, err
		}
		return []byte(out), nil
	}
}

// ListFiles enumerates every file present at the given endpoint —
// tracked (plus untracked, for the worktree) — for browsing beyond the
// changed set.
func (r *Repo) ListFiles(ref string) ([]string, error) {
	var out string
	var err error
	switch ref {
	case Worktree, "":
		out, err = r.git("ls-files", "--cached", "--others", "--exclude-standard")
	case Index:
		out, err = r.git("ls-files", "--cached")
	default:
		out, err = r.git("ls-tree", "-r", "--name-only", ref)
	}
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

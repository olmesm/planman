package gitdiff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Bounds for definition searches: a wide query on a huge repo must fail
// fast rather than hang the popover (mirrors codiff's runBoundedGitGrep).
const (
	grepTimeout        = 1500 * time.Millisecond
	grepMaxOutputBytes = 256 * 1024
	grepMaxPerFile     = 20
)

// GrepMatch is one git-grep hit.
type GrepMatch struct {
	Path string
	Line int
	Text string
}

// BoundedGrep runs a fixed-string git grep for ident at the given
// snapshot — a revision, the index (cached), or the worktree (with
// untracked files) — restricted to pathspecs, with a hard timeout and
// output cap. Exit status 1 (no matches) is not an error.
func (r *Repo) BoundedGrep(ident, revision string, cached, untracked bool, pathspecs []string) ([]GrepMatch, error) {
	args := []string{"-C", r.Root, "-c", "core.quotepath=false",
		"grep", "--no-recurse-submodules", "-n", "--null", "-I", "-F",
		fmt.Sprintf("--max-count=%d", grepMaxPerFile), "-e", ident}
	switch {
	case cached:
		args = append(args, "--cached")
	case untracked:
		args = append(args, "--untracked")
	}
	if revision != "" {
		args = append(args, revision)
	}
	args = append(args, "--")
	args = append(args, pathspecs...)

	ctx, cancel := context.WithTimeout(context.Background(), grepTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, grepMaxOutputBytes))
	// Drain anything beyond the cap so the child can exit, then wait.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("definition search timed out")
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = waitErr.Error()
			}
			return nil, fmt.Errorf("git grep: %s", msg)
		}
	}
	return parseGrepOutput(string(out), revision), nil
}

// parseGrepOutput parses `git grep -n --null` records: path NUL line NUL
// text NL, with revision-prefixed paths stripped.
func parseGrepOutput(out, revision string) []GrepMatch {
	var matches []GrepMatch
	for cursor := 0; cursor < len(out); {
		pathEnd := strings.IndexByte(out[cursor:], 0)
		if pathEnd < 0 {
			break
		}
		pathEnd += cursor
		lineEnd := strings.IndexByte(out[pathEnd+1:], 0)
		if lineEnd < 0 {
			break
		}
		lineEnd += pathEnd + 1
		recordEnd := strings.IndexByte(out[lineEnd+1:], '\n')
		if recordEnd < 0 {
			recordEnd = len(out)
		} else {
			recordEnd += lineEnd + 1
		}
		path := out[cursor:pathEnd]
		if revision != "" {
			path = strings.TrimPrefix(path, revision+":")
		}
		n, err := strconv.Atoi(out[pathEnd+1 : lineEnd])
		if path != "" && err == nil {
			matches = append(matches, GrepMatch{Path: path, Line: n, Text: out[lineEnd+1 : recordEnd]})
		}
		cursor = recordEnd + 1
	}
	return matches
}

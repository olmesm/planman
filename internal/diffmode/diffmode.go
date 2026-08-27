// Package diffmode is the git-diff review surface: a GitHub-style
// "files changed" view over a repository's working tree, branch, or
// combined changes, with line-anchored threads persisted in the sidecar
// store under .git/planman.
package diffmode

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/highlight"
	"github.com/olmesm/planman/internal/review"
	"github.com/olmesm/planman/internal/server"
	"github.com/olmesm/planman/internal/sidecar"
)

// Mode reviews a repository's diff.
type Mode struct {
	repo  *gitdiff.Repo
	store *sidecar.Store

	mu    sync.Mutex
	scope gitdiff.Scope
	base  string
	view  string // "unified" | "split"
}

// New opens the repository containing path and prepares a diff review.
func New(path, base, scope string) (*Mode, error) {
	repo, err := gitdiff.Open(path)
	if err != nil {
		return nil, err
	}
	sc, err := gitdiff.ParseScope(scope)
	if err != nil {
		return nil, err
	}
	if base == "" {
		base = repo.DefaultBase()
	}
	return &Mode{
		repo:  repo,
		store: sidecar.NewStore(repo.GitDir),
		scope: sc,
		base:  base,
		view:  "unified",
	}, nil
}

// Root returns the repository root.
func (m *Mode) Root() string { return m.repo.Root }

// Name implements server.Mode.
func (m *Mode) Name() string { return "diff" }

// Title implements server.Mode.
func (m *Mode) Title() string { return filepath.Base(m.repo.Root) }

// Store implements server.Mode.
func (m *Mode) Store() review.Store { return m.store }

// ApplyQuery implements server.Mode: scope, base, and view parameters
// update the session's view state.
func (m *Mode) ApplyQuery(q url.Values) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if v := q.Get("scope"); v != "" {
		if sc, err := gitdiff.ParseScope(v); err == nil {
			m.scope = sc
		}
	}
	if v := q.Get("base"); v != "" {
		m.base = v
	}
	switch q.Get("view") {
	case "split":
		m.view = "split"
	case "unified":
		m.view = "unified"
	}
}

func (m *Mode) state() (gitdiff.Scope, string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scope, m.base, m.view
}

// Content implements server.Mode: compute the diff, re-anchor the
// stored threads against it, and build the view model.
func (m *Mode) Content() (string, any, error) {
	scope, base, view := m.state()
	d, err := m.repo.Diff(gitdiff.Options{Scope: scope, Base: base})
	if err != nil {
		return "", nil, err
	}
	comments, err := m.store.List()
	if err != nil {
		return "", nil, err
	}

	data := &contentData{
		Scope:     string(scope),
		Base:      d.Base,
		View:      view,
		NumFiles:  len(d.Files),
		TotalAdds: d.TotalAdditions(),
		TotalDels: d.TotalDeletions(),
	}
	placed := m.placeThreads(d, comments, data)
	for _, f := range d.Files {
		data.Files = append(data.Files, buildFileVM(f, view, placed[f.Path()]))
	}
	return "diff", data, nil
}

// placeThreads assigns each stored thread to its diff row, re-anchoring
// by line content when the diff has shifted underneath it, and sorting
// review-level, detached, and orphaned threads into their buckets.
func (m *Mode) placeThreads(d *gitdiff.Diff, comments []*review.Comment, data *contentData) map[string]*fileThreads {
	fileByPath := map[string]*gitdiff.File{}
	for _, f := range d.Files {
		fileByPath[f.Path()] = f
		if f.OldPath != "" {
			fileByPath[f.OldPath] = f
		}
	}
	placed := map[string]*fileThreads{}
	forFile := func(f *gitdiff.File) *fileThreads {
		ft := placed[f.Path()]
		if ft == nil {
			ft = &fileThreads{byLine: map[threadKey][]*review.Comment{}}
			placed[f.Path()] = ft
		}
		return ft
	}

	for _, c := range comments {
		if c.Anchor.Page {
			data.PageComments = append(data.PageComments, c)
			continue
		}
		f := fileByPath[c.Anchor.File]
		if f == nil {
			data.Detached = append(data.Detached, c)
			continue
		}
		ft := forFile(f)
		side := string(c.Anchor.Side)
		if row, ok := findRow(f, side, c.Anchor.Line); ok && (c.Anchor.Context == "" || row.Text == c.Anchor.Context) {
			ft.byLine[threadKey{side: side, line: c.Anchor.Line}] = append(ft.byLine[threadKey{side: side, line: c.Anchor.Line}], c)
			continue
		}
		if line, ok := nearestMatch(f, side, c.Anchor.Line, c.Anchor.Context); ok {
			// The line moved: attach where its content is now and
			// persist the corrected anchor.
			a := c.Anchor
			a.File = f.Path()
			a.Line = line
			_ = m.store.UpdateAnchor(c.ID, a)
			c.Anchor = a
			ft.byLine[threadKey{side: side, line: line}] = append(ft.byLine[threadKey{side: side, line: line}], c)
			continue
		}
		ft.orphans = append(ft.orphans, &orphanVM{
			Comment: c,
			Side:    side,
			Line:    c.Anchor.Line,
			Context: c.Anchor.Context,
		})
	}
	return placed
}

// findRow locates the row at (side, line) in a file's hunks.
func findRow(f *gitdiff.File, side string, line int) (gitdiff.Row, bool) {
	for _, h := range f.Hunks {
		for _, r := range h.Rows {
			s, l := anchorFor(r)
			if s == side && l == line {
				return r, true
			}
		}
	}
	return gitdiff.Row{}, false
}

// nearestMatch finds the visible line on side whose content equals
// context, preferring the one closest to the original line number.
func nearestMatch(f *gitdiff.File, side string, origLine int, context string) (int, bool) {
	if context == "" {
		return 0, false
	}
	best, bestDist := 0, -1
	for _, h := range f.Hunks {
		for _, r := range h.Rows {
			s, l := anchorFor(r)
			if s != side || r.Text != context {
				continue
			}
			dist := l - origLine
			if dist < 0 {
				dist = -dist
			}
			if bestDist == -1 || dist < bestDist {
				best, bestDist = l, dist
			}
		}
	}
	return best, bestDist != -1
}

type formData struct {
	Fields      map[string]string
	Placeholder string
	Colspan     int
	Wrap        bool // wrap the form in a table row
}

func (m *Mode) colspan() int {
	_, _, view := m.state()
	if view == "split" {
		return 4
	}
	return 3
}

// CommentForm implements server.Mode.
func (m *Mode) CommentForm(q url.Values) (string, any, error) {
	if q.Get("page") == "1" {
		return "comment-form", formData{
			Fields:      map[string]string{"page": "1"},
			Placeholder: "Comment on the whole review…",
		}, nil
	}
	line, err := strconv.Atoi(q.Get("line"))
	if err != nil {
		return "", nil, fmt.Errorf("bad line")
	}
	side := q.Get("side")
	if side != string(review.SideOld) && side != string(review.SideNew) {
		return "", nil, fmt.Errorf("bad side")
	}
	return "diff-comment-form", formData{
		Fields: map[string]string{
			"file":    q.Get("file"),
			"side":    side,
			"line":    strconv.Itoa(line),
			"context": q.Get("context"),
		},
		Placeholder: "Leave a comment…",
		Colspan:     m.colspan(),
		Wrap:        true,
	}, nil
}

// Anchor implements server.Mode.
func (m *Mode) Anchor(v url.Values) (review.Anchor, error) {
	if v.Get("page") == "1" {
		return review.Anchor{Page: true}, nil
	}
	line, err := strconv.Atoi(v.Get("line"))
	if err != nil || line < 1 {
		return review.Anchor{}, fmt.Errorf("bad line")
	}
	side := review.Side(v.Get("side"))
	if side != review.SideOld && side != review.SideNew {
		return review.Anchor{}, fmt.Errorf("bad side")
	}
	file := v.Get("file")
	if file == "" {
		return review.Anchor{}, fmt.Errorf("missing file")
	}
	return review.Anchor{
		File:    file,
		Side:    side,
		Line:    line,
		Context: v.Get("context"),
	}, nil
}

// Watch implements server.Mode: poll the repository state (HEAD +
// worktree status) and fire on any change.
func (m *Mode) Watch(onChange func()) error {
	last := m.repo.StateFingerprint()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if fp := m.repo.StateFingerprint(); fp != last {
				last = fp
				onChange()
			}
		}
	}()
	return nil
}

// RegisterRoutes implements server.Mode: hunk-context expansion.
func (m *Mode) RegisterRoutes(mux *http.ServeMux, s *server.Server) {
	mux.HandleFunc("GET /expand", func(w http.ResponseWriter, r *http.Request) {
		m.handleExpand(w, r, s)
	})
}

// newSideRef is the revision backing the diff's new side: HEAD for
// branch scope, the worktree otherwise.
func (m *Mode) newSideRef() string {
	scope, _, _ := m.state()
	if scope == gitdiff.ScopeBranch {
		return "HEAD"
	}
	return ""
}

func (m *Mode) handleExpand(w http.ResponseWriter, r *http.Request, s *server.Server) {
	q := r.URL.Query()
	file := q.Get("file")
	oldStart, err1 := strconv.Atoi(q.Get("old"))
	newStart, err2 := strconv.Atoi(q.Get("new"))
	count, err3 := strconv.Atoi(q.Get("count"))
	if file == "" || err1 != nil || err2 != nil || err3 != nil || newStart < 1 {
		http.Error(w, "bad expand params", http.StatusBadRequest)
		return
	}
	lines, err := m.repo.FileLines(m.newSideRef(), file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	end := len(lines)
	if count >= 0 && newStart-1+count < end {
		end = newStart - 1 + count
	}
	if newStart-1 > end {
		http.Error(w, "range out of file", http.StatusBadRequest)
		return
	}
	_, _, view := m.state()
	lexer := highlight.LexerForFile(file)

	if view == "split" {
		var rows []*sRow
		for i, text := range lines[newStart-1 : end] {
			r := gitdiff.Row{Kind: gitdiff.Context, OldLine: oldStart + i, NewLine: newStart + i, Text: text}
			l := sCell{Kind: "context", No: r.OldLine, HTML: htmlOf(lexer, text), File: file, Side: string(review.SideOld), Line: r.OldLine, Context: text}
			rr := sCell{Kind: "context", No: r.NewLine, HTML: l.HTML, File: file, Side: string(review.SideNew), Line: r.NewLine, Context: text}
			rows = append(rows, &sRow{Kind: "line", L: l, R: rr})
		}
		s.RenderTemplate(w, "diff-split-rows", rows)
		return
	}
	var rows []*uRow
	for i, text := range lines[newStart-1 : end] {
		rows = append(rows, &uRow{
			Kind:     "line",
			LineKind: "context",
			Marker:   " ",
			OldNo:    oldStart + i,
			NewNo:    newStart + i,
			HTML:     htmlOf(lexer, text),
			File:     file,
			Side:     string(review.SideNew),
			Line:     newStart + i,
			Context:  text,
		})
	}
	s.RenderTemplate(w, "diff-unified-rows", rows)
}

// Healthz implements server.Mode.
func (m *Mode) Healthz() map[string]any {
	scope, base, _ := m.state()
	return map[string]any{
		"root":  m.repo.Root,
		"scope": string(scope),
		"base":  base,
	}
}

// Package diffmode is the git-diff review surface: a GitHub-style
// "files changed" view over any base⟵head comparison in a repository —
// commits, branches, the index, or the working tree — with line-anchored
// threads persisted in the sidecar store under .git/planman. A history
// navigator picks the two endpoints; preset ranges cover the common
// pre-PR cases.
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
	"github.com/olmesm/planman/internal/walkthrough"
)

// rangeState is the current comparison plus view options.
type rangeState struct {
	base      string // ref or SHA (never a pseudo head)
	head      string // ref, SHA, gitdiff.Worktree, or gitdiff.Index
	mergeBase bool
	view      string // "unified" | "split"
	allFiles  bool
	ignoreWS  bool
}

// Mode reviews a repository's diff.
type Mode struct {
	repo     *gitdiff.Repo
	store    *sidecar.Store
	walkPath string // walkthrough sidecar file

	mu sync.Mutex
	st rangeState
}

// New opens the repository containing path and prepares a diff review.
// scope picks the initial preset (working, branch, or all); base
// overrides the preset's base ref when set.
func New(path, base, scope string) (*Mode, error) {
	repo, err := gitdiff.Open(path)
	if err != nil {
		return nil, err
	}
	m := &Mode{
		repo:     repo,
		store:    sidecar.NewStore(repo.GitDir),
		walkPath: walkthrough.Path(repo.GitDir),
	}
	st, err := m.preset(scope)
	if err != nil {
		return nil, err
	}
	if base != "" {
		st.base = base
	}
	st.view = "unified"
	m.st = st
	return m, nil
}

// preset maps a named range preset onto endpoints.
func (m *Mode) preset(name string) (rangeState, error) {
	switch name {
	case "working", "":
		return rangeState{base: "HEAD", head: gitdiff.Worktree}, nil
	case "staged":
		return rangeState{base: "HEAD", head: gitdiff.Index}, nil
	case "branch":
		return rangeState{base: m.repo.DefaultBase(), head: "HEAD", mergeBase: true}, nil
	case "all":
		return rangeState{base: m.repo.DefaultBase(), head: gitdiff.Worktree, mergeBase: true}, nil
	}
	return rangeState{}, fmt.Errorf("invalid scope %q (working, staged, branch, or all)", name)
}

// Root returns the repository root.
func (m *Mode) Root() string { return m.repo.Root }

// Name implements server.Mode.
func (m *Mode) Name() string { return "diff" }

// Title implements server.Mode.
func (m *Mode) Title() string { return filepath.Base(m.repo.Root) }

// Store implements server.Mode.
func (m *Mode) Store() review.Store { return m.store }

// validRef accepts a ref usable as an endpoint.
func (m *Mode) validRef(ref string, allowPseudo bool) bool {
	if gitdiff.IsPseudo(ref) {
		return allowPseudo
	}
	sha, err := m.repo.ResolveSHA(ref)
	return err == nil && sha != ""
}

// ApplyQuery implements server.Mode: preset, base, head, merge-base,
// all-files, and view parameters update the session's state.
func (m *Mode) ApplyQuery(q url.Values) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := q.Get("preset"); p != "" {
		if st, err := m.preset(p); err == nil {
			st.view, st.allFiles, st.ignoreWS = m.st.view, m.st.allFiles, m.st.ignoreWS
			m.st = st
		}
	}
	if v := q.Get("base"); v != "" && m.validRef(v, false) {
		m.st.base = v
	}
	if v := q.Get("head"); v != "" && m.validRef(v, true) {
		m.st.head = v
	}
	if v := q.Get("mb"); v != "" {
		m.st.mergeBase = v == "1"
	}
	if v := q.Get("files"); v != "" {
		m.st.allFiles = v == "all"
	}
	if v := q.Get("ws"); v != "" {
		m.st.ignoreWS = v == "1"
	}
	switch q.Get("view") {
	case "split":
		m.st.view = "split"
	case "unified":
		m.st.view = "unified"
	}
}

func (m *Mode) state() rangeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// headLabel renders an endpoint for humans.
func headLabel(ref string) string {
	switch ref {
	case gitdiff.Worktree:
		return "Working tree"
	case gitdiff.Index:
		return "Staged"
	}
	if len(ref) == 40 {
		return gitdiff.ShortSHA(ref)
	}
	return ref
}

// Content implements server.Mode: compute the diff for the current
// range, re-anchor the stored threads against it, and build the view
// model — files, tree, history graph, and comment stack.
func (m *Mode) Content() (string, any, error) {
	st := m.state()
	d, err := m.repo.DiffRange(gitdiff.RangeOptions{
		Base: st.base, Head: st.head, MergeBase: st.mergeBase, IgnoreWhitespace: st.ignoreWS,
	})
	if err != nil {
		return "", nil, err
	}
	comments, err := m.store.List()
	if err != nil {
		return "", nil, err
	}

	data := &contentData{
		Base:      st.base,
		Head:      st.head,
		BaseLabel: headLabel(st.base),
		HeadLabel: headLabel(st.head),
		EffBase:   gitdiff.ShortSHA(d.BaseSHA),
		MergeBase: st.mergeBase,
		View:      st.view,
		AllFiles:  st.allFiles,
		IgnoreWS:  st.ignoreWS,
		NumFiles:  len(d.Files),
		TotalAdds: d.TotalAdditions(),
		TotalDels: d.TotalDeletions(),
	}
	placed := m.placeThreads(d, comments, data)
	for _, f := range d.Files {
		data.Files = append(data.Files, buildFileVM(f, st.view, placed[f.Path()]))
	}
	data.Tree = m.buildNavTree(data.Files, st)
	data.History = m.buildHistory(st, d)
	data.Stack = buildStack(comments)
	data.HasWalkthrough = m.hasWalkthrough()
	return "diff", data, nil
}

// buildNavTree builds the sidebar tree: the changed files, plus every
// other file at the head endpoint when all-files is on.
func (m *Mode) buildNavTree(changed []*fileVM, st rangeState) []*treeNode {
	entries := changed
	if st.allFiles {
		if all, err := m.repo.ListFiles(st.head); err == nil {
			changedSet := map[string]bool{}
			for _, f := range changed {
				changedSet[f.Path] = true
			}
			for _, path := range all {
				if !changedSet[path] {
					entries = append(entries, &fileVM{Path: path, Status: "unchanged"})
				}
			}
		}
	}
	return buildTree(entries)
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
			shiftRangeStart(&a, c.Anchor.Line)
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

// shiftRangeStart moves a range anchor's start by the same delta its end
// line just moved, degrading to single-line when the shifted start no
// longer makes sense. The end line stays the canonical anchor.
func shiftRangeStart(a *review.Anchor, oldEnd int) {
	if a.StartLine == 0 {
		return
	}
	a.StartLine += a.Line - oldEnd
	if a.StartLine < 1 || a.StartLine >= a.Line {
		a.StartLine = 0
	}
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
	if m.state().view == "split" {
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
	fields := map[string]string{
		"file":    q.Get("file"),
		"side":    side,
		"line":    strconv.Itoa(line),
		"context": q.Get("context"),
	}
	placeholder := "Leave a comment…"
	if start, err := strconv.Atoi(q.Get("start_line")); err == nil && start >= 1 && start < line {
		fields["start_line"] = strconv.Itoa(start)
		placeholder = fmt.Sprintf("Comment on lines %d–%d…", start, line)
	}
	return "diff-comment-form", formData{
		Fields:      fields,
		Placeholder: placeholder,
		Colspan:     m.colspan(),
		Wrap:        true,
	}, nil
}

// rangeRef resolves the current comparison for stamping onto anchors.
func (m *Mode) rangeRef() (base, head, baseSHA, headSHA string) {
	st := m.state()
	base, head = st.base, st.head
	committish := head
	if gitdiff.IsPseudo(head) {
		committish = "HEAD"
	} else {
		headSHA, _ = m.repo.ResolveSHA(head)
	}
	baseSHA, _ = m.repo.ResolveSHA(base)
	if st.mergeBase {
		if hs, err := m.repo.ResolveSHA(committish); err == nil && hs != "" && baseSHA != "" {
			if mb, err := m.repo.MergeBase(baseSHA, hs); err == nil && mb != "" {
				baseSHA = mb
			}
		}
	}
	return base, head, baseSHA, headSHA
}

// Anchor implements server.Mode: line anchors carry the comparison they
// were made against.
func (m *Mode) Anchor(v url.Values) (review.Anchor, error) {
	base, head, baseSHA, headSHA := m.rangeRef()
	if v.Get("page") == "1" {
		return review.Anchor{Page: true, Base: base, Head: head, BaseSHA: baseSHA, HeadSHA: headSHA}, nil
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
	a := review.Anchor{
		File:    file,
		Side:    side,
		Line:    line,
		Context: v.Get("context"),
		Base:    base,
		Head:    head,
		BaseSHA: baseSHA,
		HeadSHA: headSHA,
	}
	if start, err := strconv.Atoi(v.Get("start_line")); err == nil && start >= 1 && start < line {
		a.StartLine = start
	}
	return a, nil
}

// Watch implements server.Mode: poll the repository state (HEAD,
// worktree status, and the selected endpoints) and fire on any change.
func (m *Mode) Watch(onChange func()) error {
	fingerprint := func() string {
		st := m.state()
		b, _ := m.repo.ResolveSHA(st.base)
		h := ""
		if !gitdiff.IsPseudo(st.head) {
			h, _ = m.repo.ResolveSHA(st.head)
		}
		return m.repo.StateFingerprint() + "\x00" + b + "\x00" + h
	}
	last := fingerprint()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if fp := fingerprint(); fp != last {
				last = fp
				onChange()
			}
		}
	}()
	return nil
}

// newSideRef is the endpoint backing the diff's new side.
func (m *Mode) newSideRef() string {
	return m.state().head
}

// RegisterRoutes implements server.Mode: hunk-context expansion and
// full-file browsing.
func (m *Mode) RegisterRoutes(mux *http.ServeMux, s *server.Server) {
	mux.HandleFunc("GET /expand", func(w http.ResponseWriter, r *http.Request) {
		m.handleExpand(w, r, s)
	})
	mux.HandleFunc("GET /file", func(w http.ResponseWriter, r *http.Request) {
		m.handleFullFile(w, r, s)
	})
	mux.HandleFunc("GET /search", m.handleSearch)
	mux.HandleFunc("GET /mdpreview", m.handleMarkdownPreview)
	mux.HandleFunc("GET /blob", m.handleBlob)
	mux.HandleFunc("GET /defs", m.handleDefs)
	mux.HandleFunc("GET /walkthrough", func(w http.ResponseWriter, r *http.Request) {
		m.handleWalkthroughView(w, r, s)
	})
	mux.HandleFunc("GET /api/hunks", m.handleHunksManifest)
	mux.HandleFunc("GET /api/walkthrough", m.handleWalkthroughGet)
	mux.HandleFunc("POST /api/walkthrough", func(w http.ResponseWriter, r *http.Request) {
		m.handleWalkthroughPost(w, r, s)
	})
	mux.HandleFunc("DELETE /api/walkthrough", func(w http.ResponseWriter, r *http.Request) {
		m.handleWalkthroughDelete(w, r, s)
	})
	mux.HandleFunc("GET /export.md", func(w http.ResponseWriter, r *http.Request) {
		md, err := m.ExportMarkdown(time.Now())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(md))
	})
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
	view := m.state().view
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

// handleFullFile renders an unchanged file in full as a reviewable
// card, with any stored threads for it attached by content match.
func (m *Mode) handleFullFile(w http.ResponseWriter, r *http.Request, s *server.Server) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	lines, err := m.repo.FileLines(m.newSideRef(), path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	comments, _ := m.store.List()
	threadsAt := map[int][]*review.Comment{}
	for _, c := range comments {
		if c.Anchor.Page || c.Anchor.File != path || c.Anchor.Side != review.SideNew {
			continue
		}
		line := c.Anchor.Line
		if line < 1 || line > len(lines) || (c.Anchor.Context != "" && lines[line-1] != c.Anchor.Context) {
			line = 0
			for i, text := range lines {
				if c.Anchor.Context != "" && text == c.Anchor.Context {
					line = i + 1
					break
				}
			}
		}
		if line > 0 {
			threadsAt[line] = append(threadsAt[line], c)
		}
	}

	lexer := highlight.LexerForFile(path)
	vm := &fullFileVM{Path: path}
	for i, text := range lines {
		vm.Rows = append(vm.Rows, &uRow{
			Kind:     "line",
			LineKind: "context",
			Marker:   " ",
			OldNo:    i + 1,
			NewNo:    i + 1,
			HTML:     htmlOf(lexer, text),
			File:     path,
			Side:     string(review.SideNew),
			Line:     i + 1,
			Context:  text,
			Threads:  threadsAt[i+1],
		})
	}
	s.RenderTemplate(w, "full-file", vm)
}

// Healthz implements server.Mode.
func (m *Mode) Healthz() map[string]any {
	st := m.state()
	return map[string]any{
		"root":        m.repo.Root,
		"base":        st.base,
		"head":        st.head,
		"walkthrough": m.hasWalkthrough(),
	}
}

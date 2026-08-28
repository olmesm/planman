package diffmode

// Agent-authored narrative walkthroughs over the current diff. The
// agent fetches /api/hunks for stable hunk ids, POSTs a walkthrough
// referencing them, and the reviewer steps through it stop by stop with
// the live diff (and live comment threads) rendered along the way.

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/highlight"
	"github.com/olmesm/planman/internal/render"
	"github.com/olmesm/planman/internal/server"
	"github.com/olmesm/planman/internal/walkthrough"
)

// currentDiff computes the diff for the mode's current range, with an
// optional ignore-whitespace override (walkthrough rendering uses the
// setting the tour was authored under, so hunk ids stay aligned).
func (m *Mode) currentDiff(ignoreWS bool) (*gitdiff.Diff, error) {
	st := m.state()
	return m.repo.DiffRange(gitdiff.RangeOptions{
		Base: st.base, Head: st.head, MergeBase: st.mergeBase, IgnoreWhitespace: ignoreWS,
	})
}

// splitHunkID parses "path:hN" into its parts.
func splitHunkID(id string) (path string, ordinal int, ok bool) {
	i := strings.LastIndex(id, ":h")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+2:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return id[:i], n, true
}

// resolveHunk finds the file and hunk a walkthrough id refers to in the
// given diff. Binary and metadata-only files resolve their synthetic
// first hunk to (file, nil).
func resolveHunk(d *gitdiff.Diff, id string) (*gitdiff.File, *gitdiff.Hunk) {
	path, n, ok := splitHunkID(id)
	if !ok {
		return nil, nil
	}
	for _, f := range d.Files {
		if f.Path() != path {
			continue
		}
		if n <= len(f.Hunks) {
			return f, f.Hunks[n-1]
		}
		if n == 1 && len(f.Hunks) == 0 {
			return f, nil
		}
		return nil, nil
	}
	return nil, nil
}

// --- Agent API ---

type manifestHunk struct {
	ID         string   `json:"id"`
	Header     string   `json:"header"`
	OldStart   int      `json:"old_start"`
	OldCount   int      `json:"old_count"`
	NewStart   int      `json:"new_start"`
	NewCount   int      `json:"new_count"`
	Adds       int      `json:"adds"`
	Dels       int      `json:"dels"`
	FirstLines []string `json:"first_lines,omitempty"`
}

type manifestFile struct {
	Path        string         `json:"path"`
	Status      string         `json:"status"`
	Binary      bool           `json:"binary,omitempty"`
	Fingerprint string         `json:"fingerprint"`
	Hunks       []manifestHunk `json:"hunks"`
}

// handleHunksManifest serves the authoring manifest: every hunk in the
// current diff with its stable id, so the agent can reference the diff
// without embedding any of it.
func (m *Mode) handleHunksManifest(w http.ResponseWriter, r *http.Request) {
	st := m.state()
	d, err := m.currentDiff(st.ignoreWS)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	files := []manifestFile{}
	for _, f := range d.Files {
		mf := manifestFile{
			Path:        f.Path(),
			Status:      string(f.Status),
			Binary:      f.Binary,
			Fingerprint: f.Fingerprint,
			Hunks:       []manifestHunk{},
		}
		for _, h := range f.Hunks {
			mh := manifestHunk{
				ID:       h.ID(f.Path()),
				Header:   hunkHeader(h),
				OldStart: h.OldStart, OldCount: h.OldCount,
				NewStart: h.NewStart, NewCount: h.NewCount,
			}
			for _, row := range h.Rows {
				switch row.Kind {
				case gitdiff.Add:
					mh.Adds++
				case gitdiff.Del:
					mh.Dels++
				}
				if len(mh.FirstLines) < 3 {
					mh.FirstLines = append(mh.FirstLines, row.Text)
				}
			}
			mf.Hunks = append(mf.Hunks, mh)
		}
		if len(mf.Hunks) == 0 {
			// Synthetic hunk so binary/metadata-only changes stay referenceable.
			mf.Hunks = append(mf.Hunks, manifestHunk{ID: gitdiff.HunkID(f.Path(), 1), Header: "(no text hunks)"})
		}
		files = append(files, mf)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base":      d.Base,
		"head":      d.Head,
		"base_sha":  d.BaseSHA,
		"head_sha":  d.HeadSHA,
		"ignore_ws": st.ignoreWS,
		"files":     files,
	})
}

func (m *Mode) handleWalkthroughPost(w http.ResponseWriter, r *http.Request, s *server.Server) {
	var wt walkthrough.Walkthrough
	if err := json.NewDecoder(r.Body).Decode(&wt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := wt.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	st := m.state()
	d, err := m.currentDiff(st.ignoreWS)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var unknown []string
	fps := map[string]string{}
	for _, id := range wt.HunkIDs() {
		f, _ := resolveHunk(d, id)
		if f == nil {
			unknown = append(unknown, id)
			continue
		}
		fps[f.Path()] = f.Fingerprint
	}
	if len(unknown) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":            "unknown hunk ids — re-fetch /api/hunks and re-map",
			"unknown_hunk_ids": unknown,
		})
		return
	}
	stored := &walkthrough.Stored{
		CreatedAt:        time.Now().UTC().Truncate(time.Second),
		Base:             d.Base,
		Head:             d.Head,
		BaseSHA:          d.BaseSHA,
		HeadSHA:          d.HeadSHA,
		IgnoreWS:         st.ignoreWS,
		FileFingerprints: fps,
		W:                wt,
	}
	if err := walkthrough.Save(m.walkPath, stored); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The diff under review did not move; refresh viewers silently so
	// the walkthrough chip appears.
	s.BroadcastComments()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "stops": len(wt.Stops())})
}

func (m *Mode) handleWalkthroughGet(w http.ResponseWriter, r *http.Request) {
	st, err := walkthrough.Load(m.walkPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st == nil {
		http.Error(w, "no walkthrough", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (m *Mode) handleWalkthroughDelete(w http.ResponseWriter, r *http.Request, s *server.Server) {
	if err := walkthrough.Delete(m.walkPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.BroadcastComments()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Reviewer view ---

type walkCard struct {
	HunkID  string
	Path    string
	Header  string
	Missing bool
	Binary  bool
	Image   *imageVM
	Rows    []*uRow
	Note    template.HTML
}

type walkRailEntry struct {
	Idx          int
	Title        string
	Importance   string
	Current      bool
	ChapterTitle string // set on the chapter's first stop
	ChapterIcon  string
}

type walkData struct {
	Title     string
	Focus     string
	Outdated  bool
	Rail      []walkRailEntry
	StopIdx   int // 0-based
	StopCount int
	StopTitle string
	StopImp   string
	ProseHTML template.HTML
	Cards     []*walkCard
	PrevIdx   int // -1 when at an edge
	NextIdx   int
	Last      bool
	Commit    *walkthrough.Commit
	Support   []walkSupport
}

type walkSupport struct {
	Reason string
	IDs    []string
}

// stopTitle picks a display title with codiff's fallback chain.
func stopTitle(s walkthrough.Stop) string {
	if s.Title != "" {
		return s.Title
	}
	if s.Prose != "" {
		t := strings.TrimSpace(s.Prose)
		if i := strings.IndexAny(t, ".\n"); i > 0 {
			t = t[:i]
		}
		if len(t) > 60 {
			t = t[:60] + "…"
		}
		return t
	}
	if len(s.HunkIDs) > 0 {
		if p, _, ok := splitHunkID(s.HunkIDs[0]); ok {
			return p
		}
	}
	return "Stop"
}

func (m *Mode) handleWalkthroughView(w http.ResponseWriter, r *http.Request, s *server.Server) {
	stored, err := walkthrough.Load(m.walkPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if stored == nil {
		http.Error(w, "no walkthrough posted — the agent creates one via POST /api/walkthrough", http.StatusNotFound)
		return
	}
	d, err := m.currentDiff(stored.IgnoreWS)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	comments, err := m.store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	placed := m.placeThreads(d, comments, &contentData{})

	stops := stored.W.Stops()
	idx := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("stop")); err == nil && v >= 0 && v < len(stops) {
		idx = v
	}
	cur := stops[idx]

	data := &walkData{
		Title:     stored.W.Title,
		Focus:     stored.W.Focus,
		Outdated:  m.walkthroughOutdated(stored, d),
		StopIdx:   idx,
		StopCount: len(stops),
		StopTitle: stopTitle(cur),
		StopImp:   cur.Importance,
		ProseHTML: render.Inline(cur.Prose),
		PrevIdx:   idx - 1,
		NextIdx:   idx + 1,
		Last:      idx == len(stops)-1,
	}
	if data.NextIdx >= len(stops) {
		data.NextIdx = -1
	}
	if data.Last {
		data.Commit = stored.W.Commit
		for _, sup := range stored.W.Support {
			data.Support = append(data.Support, walkSupport{Reason: sup.Reason, IDs: sup.HunkIDs})
		}
	}

	// Rail: every stop, chapter-labelled on chapter boundaries.
	i := 0
	for _, c := range stored.W.Chapters {
		for si, st := range c.Stops {
			e := walkRailEntry{Idx: i, Title: stopTitle(st), Importance: st.Importance, Current: i == idx}
			if si == 0 {
				e.ChapterTitle = c.Title
				e.ChapterIcon = c.Icon
			}
			data.Rail = append(data.Rail, e)
			i++
		}
	}

	// Cards: the current stop's hunks against the live diff.
	notes := map[string]template.HTML{}
	for _, n := range cur.Notes {
		notes[n.HunkID] = render.Inline(n.Body)
	}
	for _, id := range cur.HunkIDs {
		card := &walkCard{HunkID: id, Note: notes[id]}
		f, h := resolveHunk(d, id)
		if f == nil {
			card.Missing = true
			if p, _, ok := splitHunkID(id); ok {
				card.Path = p
			}
			data.Cards = append(data.Cards, card)
			continue
		}
		card.Path = f.Path()
		if h == nil {
			card.Binary = true
			card.Image = imageURLs(f)
			data.Cards = append(data.Cards, card)
			continue
		}
		card.Header = hunkHeader(h)
		single := &gitdiff.File{
			OldPath: f.OldPath, NewPath: f.NewPath, Status: f.Status,
			Hunks: []*gitdiff.Hunk{h},
		}
		card.Rows = buildUnifiedRows(single, highlight.LexerForFile(f.Path()), false, placed[f.Path()])
		data.Cards = append(data.Cards, card)
	}

	s.RenderTemplate(w, "walkthrough", data)
}

// walkthroughOutdated reports whether the diff has drifted from the one
// the tour was authored against.
func (m *Mode) walkthroughOutdated(stored *walkthrough.Stored, d *gitdiff.Diff) bool {
	if stored.BaseSHA != d.BaseSHA || stored.HeadSHA != d.HeadSHA {
		return true
	}
	byPath := map[string]string{}
	for _, f := range d.Files {
		byPath[f.Path()] = f.Fingerprint
	}
	for path, fp := range stored.FileFingerprints {
		if byPath[path] != fp {
			return true
		}
	}
	return false
}

// hasWalkthrough reports whether a tour is stored, for the toolbar chip.
func (m *Mode) hasWalkthrough() bool {
	st, err := walkthrough.Load(m.walkPath)
	return err == nil && st != nil
}

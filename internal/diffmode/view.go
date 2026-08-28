package diffmode

import (
	"fmt"
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/highlight"
	"github.com/olmesm/planman/internal/review"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// contentData is the template payload for the "diff" content fragment.
type contentData struct {
	Base         string // selected base ref (symbolic)
	Head         string // selected head endpoint
	BaseLabel    string
	HeadLabel    string
	EffBase      string // effective (merge-)base, abbreviated
	MergeBase    bool
	View         string // "unified" | "split"
	AllFiles     bool
	IgnoreWS     bool
	Files        []*fileVM
	Tree         []*treeNode
	History      []*histRow
	Stack        []*stackEntry
	PageComments []*review.Comment
	// Detached threads whose file is no longer in the diff at all.
	Detached  []*review.Comment
	NumFiles  int
	TotalAdds int
	TotalDels int
}

// pill is a ref decoration shown on a history row.
type pill struct {
	Text string
	Kind string // "branch" | "remote" | "tag"
}

// histRow is one row of the history navigator.
type histRow struct {
	Ref         string // endpoint value: SHA, or a pseudo head
	Pseudo      bool   // working tree / staged rows
	SVG         template.HTML
	Subject     string
	Pills       []pill
	Short       string
	Age         string
	IsHead      bool
	IsBase      bool // currently selected base
	IsCompare   bool // currently selected head
	IsMergeBase bool // resolved effective base, when it differs from the selection
}

// stackEntry is one thread in the comment stack.
type stackEntry struct {
	ID         string
	Snippet    string
	Loc        string
	Author     string
	Age        string
	Resolved   bool
	RangeLabel string
	// NavBase/NavHead reproduce the comparison the thread was made
	// against; empty when the thread carries no range reference.
	NavBase string
	NavHead string
}

// buildHistory renders the log with selection markers and pseudo rows
// for the working tree and index.
func (m *Mode) buildHistory(st rangeState, d *gitdiff.Diff) []*histRow {
	baseSel, _ := m.repo.ResolveSHA(st.base)
	headSel := ""
	if !gitdiff.IsPseudo(st.head) {
		headSel, _ = m.repo.ResolveSHA(st.head)
	}

	rows := []*histRow{
		{
			Ref: gitdiff.Worktree, Pseudo: true, Subject: "Working tree",
			SVG:       pseudoSVG(),
			IsCompare: st.head == gitdiff.Worktree,
		},
		{
			Ref: gitdiff.Index, Pseudo: true, Subject: "Staged changes",
			SVG:       pseudoSVG(),
			IsCompare: st.head == gitdiff.Index,
		},
	}
	log, err := m.repo.Log(120)
	if err != nil {
		return rows
	}
	for _, g := range log {
		row := &histRow{
			Ref:         g.SHA,
			SVG:         graphSVG(g),
			Subject:     g.Subject,
			Short:       g.Short,
			Age:         humanAge(g.When),
			IsHead:      g.IsHead,
			IsBase:      g.SHA == baseSel,
			IsCompare:   headSel != "" && g.SHA == headSel,
			IsMergeBase: d.BaseSHA != "" && g.SHA == d.BaseSHA && d.BaseSHA != baseSel,
		}
		for _, ref := range g.Refs {
			p := pill{Text: ref, Kind: "branch"}
			if rest, ok := strings.CutPrefix(ref, "tag: "); ok {
				p = pill{Text: rest, Kind: "tag"}
			} else if strings.HasPrefix(ref, "origin/") {
				p.Kind = "remote"
			}
			row.Pills = append(row.Pills, p)
		}
		rows = append(rows, row)
	}
	return rows
}

const (
	laneW = 12
	rowH  = 26
)

var laneColors = 8

func laneX(l int) int { return laneW/2 + l*laneW }

// graphSVG draws one row's lane geometry.
func graphSVG(g *gitdiff.GraphRow) template.HTML {
	w := g.NLanes * laneW
	if w < laneW {
		w = laneW
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="lanes" width="%d" height="%d" viewBox="0 0 %d %d">`, w, rowH, w, rowH)
	line := func(x1, y1, x2, y2, lane int) {
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" class="lane-%d"/>`,
			x1, y1, x2, y2, lane%laneColors)
	}
	for _, p := range g.Pass {
		line(laneX(p[0]), 0, laneX(p[1]), rowH, p[1])
	}
	for _, mg := range g.Merge {
		line(laneX(mg), 0, laneX(g.Dot), rowH/2, g.Dot)
	}
	for _, f := range g.Fork {
		line(laneX(g.Dot), rowH/2, laneX(f), rowH, f)
	}
	// A lane that continues through the dot needs its top half drawn.
	if len(g.Merge) == 0 && dotHasIncoming(g) {
		line(laneX(g.Dot), 0, laneX(g.Dot), rowH/2, g.Dot)
	}
	fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="3.5" class="dot lane-%d%s"/>`,
		laneX(g.Dot), rowH/2, g.Dot%laneColors, headClass(g))
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// dotHasIncoming reports whether anything above connects to this dot —
// i.e. the commit is not a branch tip opening a fresh lane.
func dotHasIncoming(g *gitdiff.GraphRow) bool {
	return !g.IsTip
}

func headClass(g *gitdiff.GraphRow) string {
	if g.IsHead {
		return " head"
	}
	return ""
}

func pseudoSVG() template.HTML {
	return template.HTML(fmt.Sprintf(
		`<svg class="lanes" width="%d" height="%d"><circle cx="%d" cy="%d" r="3.5" class="dot pseudo"/></svg>`,
		laneW, rowH, laneX(0), rowH/2))
}

// humanAge renders a compact relative timestamp.
func humanAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Format("Jan 2006")
}

// buildStack lists every thread, open before resolved, newest first,
// each with its range reference for navigation.
func buildStack(comments []*review.Comment) []*stackEntry {
	entries := make([]*stackEntry, 0, len(comments))
	for _, c := range comments {
		e := &stackEntry{
			ID:       c.ID,
			Snippet:  trimText(c.Text, 90),
			Author:   c.Author,
			Age:      humanAge(c.Ts),
			Resolved: c.Resolved(),
			Loc:      "review",
		}
		if !c.Anchor.Page {
			if c.Anchor.StartLine > 0 {
				e.Loc = fmt.Sprintf("%s:%d–%d", c.Anchor.File, c.Anchor.StartLine, c.Anchor.Line)
			} else {
				e.Loc = fmt.Sprintf("%s:%d", c.Anchor.File, c.Anchor.Line)
			}
		}
		if c.Anchor.Base != "" {
			label := headLabel(c.Anchor.Head)
			if c.Anchor.HeadSHA != "" {
				label = gitdiff.ShortSHA(c.Anchor.HeadSHA)
			}
			e.RangeLabel = headLabel(c.Anchor.Base) + " ⟵ " + label
			e.NavBase = c.Anchor.BaseSHA
			if e.NavBase == "" {
				e.NavBase = c.Anchor.Base
			}
			e.NavHead = c.Anchor.Head
			if !gitdiff.IsPseudo(c.Anchor.Head) && c.Anchor.HeadSHA != "" {
				e.NavHead = c.Anchor.HeadSHA
			}
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Resolved != entries[j].Resolved {
			return !entries[i].Resolved
		}
		return i > j // stored order is chronological; newest first
	})
	return entries
}

func trimText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// fullFileVM renders an unchanged file in full.
type fullFileVM struct {
	Path string
	Rows []*uRow
}

// treeNode is one entry in the file-tree sidebar: a directory (possibly
// a compressed chain like "internal/server") or a changed file.
type treeNode struct {
	Name        string
	Path        string // file nodes: full path, matching the file card
	IsDir       bool
	Status      string
	Fingerprint string
	Additions   int
	Deletions   int
	Children    []*treeNode
}

// buildTree folds the (path-sorted) file list into a directory tree,
// with single-child directory chains compressed GitHub-style.
func buildTree(files []*fileVM) []*treeNode {
	root := &treeNode{IsDir: true}
	dirs := map[string]*treeNode{"": root}
	for _, f := range files {
		parts := strings.Split(f.Path, "/")
		dirPath := ""
		parent := root
		for _, p := range parts[:len(parts)-1] {
			if dirPath == "" {
				dirPath = p
			} else {
				dirPath += "/" + p
			}
			n := dirs[dirPath]
			if n == nil {
				n = &treeNode{Name: p, IsDir: true}
				dirs[dirPath] = n
				parent.Children = append(parent.Children, n)
			}
			parent = n
		}
		parent.Children = append(parent.Children, &treeNode{
			Name:        parts[len(parts)-1],
			Path:        f.Path,
			Status:      f.Status,
			Fingerprint: f.Fingerprint,
			Additions:   f.Additions,
			Deletions:   f.Deletions,
		})
	}
	compressTree(root)
	sortTree(root)
	return root.Children
}

func compressTree(n *treeNode) {
	if n.IsDir && n.Name != "" {
		for len(n.Children) == 1 && n.Children[0].IsDir {
			c := n.Children[0]
			n.Name += "/" + c.Name
			n.Children = c.Children
		}
	}
	for _, c := range n.Children {
		compressTree(c)
	}
}

// sortTree orders directories before files, each alphabetically.
func sortTree(n *treeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

type fileVM struct {
	Path        string // anchor path (new path unless deleted)
	DisplayPath string // "old → new" for renames
	Status      string
	Binary      bool
	Additions   int
	Deletions   int
	Fingerprint string
	URows       []*uRow // populated in unified view
	SRows       []*sRow // populated in split view
	Orphans     []*orphanVM
}

// orphanVM is a thread whose anchored line is not visible in the diff.
type orphanVM struct {
	Comment *review.Comment
	Side    string
	Line    int
	Context string
}

type expander struct {
	File     string
	OldStart int
	NewStart int
	Count    int // -1 = through end of file
}

type uRow struct {
	Kind     string // "line" | "hunk" | "expander"
	LineKind string // "context" | "add" | "del"
	Marker   string
	OldNo    int
	NewNo    int
	HTML     template.HTML
	Header   string
	HunkID   string // hunk rows: stable id within the current diff
	Exp      *expander
	File     string
	Side     string
	Line     int
	Context  string
	InRange  bool // covered by a range comment
	Threads  []*review.Comment
}

type sCell struct {
	Kind    string // "context" | "add" | "del" | "empty"
	No      int
	HTML    template.HTML
	File    string
	Side    string
	Line    int
	Context string
	InRange bool // covered by a range comment
}

type sRow struct {
	Kind    string // "line" | "hunk" | "expander"
	Header  string
	HunkID  string // hunk rows: stable id within the current diff
	Exp     *expander
	L       sCell
	R       sCell
	Threads []*review.Comment
}

// rowEmph carries word-diff emphasis per hunk row, keyed by row index.
type rowEmph map[int][]highlight.Range

var dmp = diffmatchpatch.New()

// wordDiffHunk computes intra-line emphasis for paired del/add runs in a
// hunk. Emphasis is only applied when the paired lines share enough
// content for a word-level diff to be meaningful.
func wordDiffHunk(h *gitdiff.Hunk) rowEmph {
	emph := rowEmph{}
	i := 0
	for i < len(h.Rows) {
		if h.Rows[i].Kind != gitdiff.Del {
			i++
			continue
		}
		delStart := i
		for i < len(h.Rows) && h.Rows[i].Kind == gitdiff.Del {
			i++
		}
		addStart := i
		for i < len(h.Rows) && h.Rows[i].Kind == gitdiff.Add {
			i++
		}
		nPairs := min(addStart-delStart, i-addStart)
		for p := 0; p < nPairs; p++ {
			di, ai := delStart+p, addStart+p
			oldR, newR := wordDiffPair(h.Rows[di].Text, h.Rows[ai].Text)
			if oldR != nil || newR != nil {
				emph[di] = oldR
				emph[ai] = newR
			}
		}
	}
	return emph
}

// wordDiffPair returns emphasis ranges for one del/add line pair, or
// nil/nil when the lines are too different to word-diff usefully.
func wordDiffPair(oldLine, newLine string) (oldR, newR []highlight.Range) {
	const maxLen = 1000
	lo, ln := len([]rune(oldLine)), len([]rune(newLine))
	if lo == 0 || ln == 0 || lo > maxLen || ln > maxLen {
		return nil, nil
	}
	diffs := dmp.DiffMain(oldLine, newLine, false)
	diffs = dmp.DiffCleanupSemantic(diffs)
	equal := 0
	oldOff, newOff := 0, 0
	for _, d := range diffs {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			equal += n
			oldOff += n
			newOff += n
		case diffmatchpatch.DiffDelete:
			oldR = append(oldR, highlight.Range{Start: oldOff, End: oldOff + n})
			oldOff += n
		case diffmatchpatch.DiffInsert:
			newR = append(newR, highlight.Range{Start: newOff, End: newOff + n})
			newOff += n
		}
	}
	// If almost nothing matches, whole-line coloring reads better than
	// a sea of emphasis.
	if longest := max(lo, ln); equal*10 < longest*3 {
		return nil, nil
	}
	return oldR, newR
}

// threadKey identifies an anchored line within a file.
type threadKey struct {
	side string
	line int
}

// fileThreads is the comment placement for one file after re-anchoring.
type fileThreads struct {
	byLine  map[threadKey][]*review.Comment
	orphans []*orphanVM
}

// rangeSet marks every row covered by a placed range comment, so the
// view can tint the whole range, not just the anchored end line.
func (ft *fileThreads) rangeSet() map[threadKey]bool {
	if ft == nil {
		return nil
	}
	var set map[threadKey]bool
	for key, cs := range ft.byLine {
		for _, c := range cs {
			if c.Anchor.StartLine == 0 {
				continue
			}
			if set == nil {
				set = map[threadKey]bool{}
			}
			// The placed end line is key.line; cover the same span of
			// rows above it that the stored range describes.
			span := c.Anchor.Line - c.Anchor.StartLine
			for l := key.line - span; l <= key.line; l++ {
				if l >= 1 {
					set[threadKey{side: key.side, line: l}] = true
				}
			}
		}
	}
	return set
}

// buildFileVM renders one file for the requested view.
func buildFileVM(f *gitdiff.File, view string, threads *fileThreads) *fileVM {
	vm := &fileVM{
		Path:        f.Path(),
		DisplayPath: f.Path(),
		Status:      string(f.Status),
		Binary:      f.Binary,
		Additions:   f.Additions,
		Deletions:   f.Deletions,
		Fingerprint: f.Fingerprint,
	}
	if f.Status == gitdiff.Renamed && f.OldPath != f.NewPath {
		vm.DisplayPath = f.OldPath + " → " + f.NewPath
	}
	if threads != nil {
		vm.Orphans = threads.orphans
	}
	if f.Binary {
		return vm
	}
	lexer := highlight.LexerForFile(f.Path())
	expandable := f.Status == gitdiff.Modified || f.Status == gitdiff.Renamed
	if view == "split" {
		vm.SRows = buildSplitRows(f, lexer, expandable, threads)
	} else {
		vm.URows = buildUnifiedRows(f, lexer, expandable, threads)
	}
	return vm
}

func threadsFor(threads *fileThreads, side string, line int) []*review.Comment {
	if threads == nil {
		return nil
	}
	return threads.byLine[threadKey{side: side, line: line}]
}

// anchorFor picks the anchor identity of a row: deletions anchor to the
// old side, everything else to the new side.
func anchorFor(r gitdiff.Row) (side string, line int) {
	if r.Kind == gitdiff.Del {
		return string(review.SideOld), r.OldLine
	}
	return string(review.SideNew), r.NewLine
}

func buildUnifiedRows(f *gitdiff.File, lexer chroma.Lexer, expandable bool, threads *fileThreads) []*uRow {
	var rows []*uRow
	path := f.Path()
	ranged := threads.rangeSet()
	addExpander := func(exp *expander) {
		if expandable && exp.Count != 0 {
			rows = append(rows, &uRow{Kind: "expander", Exp: exp})
		}
	}
	for hi, h := range f.Hunks {
		addExpander(gapBefore(f, hi, path))
		rows = append(rows, &uRow{Kind: "hunk", Header: hunkHeader(h), HunkID: h.ID(path)})
		emph := wordDiffHunk(h)
		for ri, r := range h.Rows {
			side, line := anchorFor(r)
			row := &uRow{
				Kind:     "line",
				LineKind: lineKind(r.Kind),
				Marker:   marker(r.Kind),
				OldNo:    r.OldLine,
				NewNo:    r.NewLine,
				HTML:     template.HTML(highlight.LineHTML(lexer, r.Text, emph[ri])),
				File:     path,
				Side:     side,
				Line:     line,
				Context:  r.Text,
				InRange:  ranged[threadKey{side: side, line: line}],
				Threads:  threadsFor(threads, side, line),
			}
			rows = append(rows, row)
		}
	}
	if len(f.Hunks) > 0 {
		addExpander(gapAfter(f, path))
	}
	return rows
}

func buildSplitRows(f *gitdiff.File, lexer chroma.Lexer, expandable bool, threads *fileThreads) []*sRow {
	var rows []*sRow
	path := f.Path()
	ranged := threads.rangeSet()
	addExpander := func(exp *expander) {
		if expandable && exp.Count != 0 {
			rows = append(rows, &sRow{Kind: "expander", Exp: exp})
		}
	}
	cell := func(r gitdiff.Row, emph []highlight.Range) sCell {
		side, line := anchorFor(r)
		no := r.NewLine
		if r.Kind == gitdiff.Del {
			no = r.OldLine
		}
		return sCell{
			Kind:    lineKind(r.Kind),
			No:      no,
			HTML:    template.HTML(highlight.LineHTML(lexer, r.Text, emph)),
			File:    path,
			Side:    side,
			Line:    line,
			Context: r.Text,
			InRange: ranged[threadKey{side: side, line: line}],
		}
	}
	contextCells := func(r gitdiff.Row) (sCell, sCell) {
		l := sCell{Kind: "context", No: r.OldLine, HTML: template.HTML(highlight.LineHTML(lexer, r.Text, nil)),
			File: path, Side: string(review.SideOld), Line: r.OldLine, Context: r.Text,
			InRange: ranged[threadKey{side: string(review.SideOld), line: r.OldLine}]}
		rr := sCell{Kind: "context", No: r.NewLine, HTML: l.HTML,
			File: path, Side: string(review.SideNew), Line: r.NewLine, Context: r.Text,
			InRange: ranged[threadKey{side: string(review.SideNew), line: r.NewLine}]}
		return l, rr
	}
	rowThreads := func(cells ...sCell) []*review.Comment {
		var ts []*review.Comment
		for _, c := range cells {
			if c.Kind != "empty" {
				ts = append(ts, threadsFor(threads, c.Side, c.Line)...)
			}
		}
		return ts
	}

	for hi, h := range f.Hunks {
		addExpander(gapBefore(f, hi, path))
		rows = append(rows, &sRow{Kind: "hunk", Header: hunkHeader(h), HunkID: h.ID(path)})
		emph := wordDiffHunk(h)
		i := 0
		for i < len(h.Rows) {
			r := h.Rows[i]
			switch r.Kind {
			case gitdiff.Context:
				l, rr := contextCells(r)
				rows = append(rows, &sRow{Kind: "line", L: l, R: rr, Threads: rowThreads(l, rr)})
				i++
			case gitdiff.Del:
				delStart := i
				for i < len(h.Rows) && h.Rows[i].Kind == gitdiff.Del {
					i++
				}
				addStart := i
				for i < len(h.Rows) && h.Rows[i].Kind == gitdiff.Add {
					i++
				}
				dels, adds := h.Rows[delStart:addStart], h.Rows[addStart:i]
				for p := 0; p < max(len(dels), len(adds)); p++ {
					var l, rr sCell
					if p < len(dels) {
						l = cell(dels[p], emph[delStart+p])
					} else {
						l = sCell{Kind: "empty"}
					}
					if p < len(adds) {
						rr = cell(adds[p], emph[addStart+p])
					} else {
						rr = sCell{Kind: "empty"}
					}
					rows = append(rows, &sRow{Kind: "line", L: l, R: rr, Threads: rowThreads(l, rr)})
				}
			case gitdiff.Add:
				// Adds with no preceding deletes.
				rr := cell(r, emph[i])
				rows = append(rows, &sRow{Kind: "line", L: sCell{Kind: "empty"}, R: rr, Threads: rowThreads(rr)})
				i++
			}
		}
	}
	if len(f.Hunks) > 0 {
		addExpander(gapAfter(f, path))
	}
	return rows
}

// gapBefore describes the unshown context before hunk hi.
func gapBefore(f *gitdiff.File, hi int, path string) *expander {
	h := f.Hunks[hi]
	if hi == 0 {
		return &expander{File: path, OldStart: 1, NewStart: 1, Count: h.NewStart - 1}
	}
	prev := f.Hunks[hi-1]
	return &expander{
		File:     path,
		OldStart: prev.OldStart + prev.OldCount,
		NewStart: prev.NewStart + prev.NewCount,
		Count:    h.NewStart - (prev.NewStart + prev.NewCount),
	}
}

// gapAfter describes the context after the last hunk, through EOF.
func gapAfter(f *gitdiff.File, path string) *expander {
	last := f.Hunks[len(f.Hunks)-1]
	return &expander{
		File:     path,
		OldStart: last.OldStart + last.OldCount,
		NewStart: last.NewStart + last.NewCount,
		Count:    -1,
	}
}

func hunkHeader(h *gitdiff.Hunk) string {
	head := fmt.Sprintf("@@ -%d,%d +%d,%d @@", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
	if h.Section != "" {
		head += " " + h.Section
	}
	return head
}

func htmlOf(lexer chroma.Lexer, text string) template.HTML {
	return template.HTML(highlight.LineHTML(lexer, text, nil))
}

func lineKind(k gitdiff.LineKind) string {
	switch k {
	case gitdiff.Add:
		return "add"
	case gitdiff.Del:
		return "del"
	}
	return "context"
}

func marker(k gitdiff.LineKind) string {
	switch k {
	case gitdiff.Add:
		return "+"
	case gitdiff.Del:
		return "−"
	}
	return " "
}

package diffmode

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/highlight"
	"github.com/olmesm/planman/internal/review"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// contentData is the template payload for the "diff" content fragment.
type contentData struct {
	Scope        string
	Base         string
	View         string // "unified" | "split"
	Files        []*fileVM
	Tree         []*treeNode
	PageComments []*review.Comment
	// Detached threads whose file is no longer in the diff at all.
	Detached  []*review.Comment
	NumFiles  int
	TotalAdds int
	TotalDels int
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
	Exp      *expander
	File     string
	Side     string
	Line     int
	Context  string
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
}

type sRow struct {
	Kind    string // "line" | "hunk" | "expander"
	Header  string
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
	addExpander := func(exp *expander) {
		if expandable && exp.Count != 0 {
			rows = append(rows, &uRow{Kind: "expander", Exp: exp})
		}
	}
	for hi, h := range f.Hunks {
		addExpander(gapBefore(f, hi, path))
		rows = append(rows, &uRow{Kind: "hunk", Header: hunkHeader(h)})
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
		}
	}
	contextCells := func(r gitdiff.Row) (sCell, sCell) {
		l := sCell{Kind: "context", No: r.OldLine, HTML: template.HTML(highlight.LineHTML(lexer, r.Text, nil)),
			File: path, Side: string(review.SideOld), Line: r.OldLine, Context: r.Text}
		rr := sCell{Kind: "context", No: r.NewLine, HTML: l.HTML,
			File: path, Side: string(review.SideNew), Line: r.NewLine, Context: r.Text}
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
		rows = append(rows, &sRow{Kind: "hunk", Header: hunkHeader(h)})
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

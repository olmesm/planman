// Package render turns a critic.Document into HTML, one fragment per
// top-level markdown block, so the UI can attach comments to blocks.
package render

import (
	"bytes"
	"fmt"
	"html"
	"strings"

	"github.com/olmesm/planman/internal/critic"
	"github.com/olmesm/planman/internal/review"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Block is one rendered top-level markdown block.
type Block struct {
	Index     int
	HTML      string
	StartLine int // first body line of the block
	EndLine   int // last body line of the block
	Comments  []*review.Comment
}

// Result is the fully rendered document.
type Result struct {
	Blocks       []Block
	PageComments []*review.Comment
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(
		gmhtml.WithUnsafe(),
		renderer.WithNodeRenderers(util.Prioritized(&codeRenderer{}, 100)),
	),
)

// Render parses the document body and renders each top-level block,
// assigning comments to the block containing (or nearest above) their anchor.
func Render(doc *critic.Document) (*Result, error) {
	src := []byte(doc.Body())
	root := md.Parser().Parse(text.NewReader(src))

	lineStarts := computeLineStarts(src)
	res := &Result{}
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		start, end := nodeByteSpan(child, src)
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, src, child); err != nil {
			return nil, fmt.Errorf("render block: %w", err)
		}
		endOff := end - 1
		if endOff < start {
			endOff = start
		}
		res.Blocks = append(res.Blocks, Block{
			Index:     len(res.Blocks),
			HTML:      buf.String(),
			StartLine: offsetToLine(lineStarts, start),
			EndLine:   offsetToLine(lineStarts, endOff),
		})
	}

	for _, c := range doc.Comments {
		if c.Anchor.Page {
			res.PageComments = append(res.PageComments, c)
			continue
		}
		bi := res.blockForLine(c.Anchor.Line)
		if bi < 0 {
			res.PageComments = append(res.PageComments, c)
			continue
		}
		res.Blocks[bi].Comments = append(res.Blocks[bi].Comments, c)
	}
	return res, nil
}

// blockForLine finds the block containing the line, or the closest block
// above it. Returns -1 if there are no blocks at or above the line.
func (r *Result) blockForLine(line int) int {
	best := -1
	for i, b := range r.Blocks {
		if b.StartLine <= line {
			best = i
		}
		if b.StartLine > line {
			break
		}
	}
	return best
}

// nodeByteSpan returns the [min,max) byte range covered by a node's lines,
// walking the subtree because container nodes have no segments themselves.
func nodeByteSpan(n ast.Node, src []byte) (int, int) {
	start, end := -1, -1
	_ = ast.Walk(n, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if node.Type() != ast.TypeBlock {
			return ast.WalkSkipChildren, nil
		}
		lines := node.Lines()
		if lines == nil {
			return ast.WalkContinue, nil
		}
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			if start == -1 || seg.Start < start {
				start = seg.Start
			}
			if seg.Stop > end {
				end = seg.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if start == -1 {
		return 0, 0
	}
	return start, end
}

func computeLineStarts(src []byte) []int {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func offsetToLine(starts []int, off int) int {
	lo, hi := 0, len(starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// EscapeForDisplay is used by templates to show raw markdown snippets.
func EscapeForDisplay(s string) string {
	return html.EscapeString(strings.TrimSpace(s))
}

package render

import (
	"html"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// codeRenderer renders fenced code blocks: ```mermaid becomes a
// <pre class="mermaid"> for client-side mermaid.js, everything else is
// syntax-highlighted server-side with chroma (inline styles, no JS).
type codeRenderer struct{}

func (r *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
	reg.Register(ast.KindCodeBlock, r.renderIndented)
}

func nodeSource(n ast.Node, src []byte) string {
	var sb strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		sb.Write(seg.Value(src))
	}
	return sb.String()
}

func (r *codeRenderer) renderFenced(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := ""
	if n.Info != nil {
		if fields := strings.Fields(string(n.Info.Segment.Value(src))); len(fields) > 0 {
			lang = fields[0]
		}
	}
	code := nodeSource(n, src)

	if strings.EqualFold(lang, "mermaid") {
		_, _ = w.WriteString(`<pre class="mermaid">`)
		_, _ = w.WriteString(html.EscapeString(code))
		_, _ = w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}
	writeHighlighted(w, code, lang)
	return ast.WalkSkipChildren, nil
}

func (r *codeRenderer) renderIndented(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	writeHighlighted(w, nodeSource(node, src), "")
	return ast.WalkSkipChildren, nil
}

func writeHighlighted(w util.BufWriter, code, lang string) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err == nil {
		formatter := chromahtml.New(
			chromahtml.WithClasses(false),
			chromahtml.PreventSurroundingPre(false),
		)
		var sb strings.Builder
		if err := formatter.Format(&sb, style, iterator); err == nil {
			_, _ = w.WriteString(`<div class="highlight" data-lang="` + html.EscapeString(lang) + `">`)
			_, _ = w.WriteString(sb.String())
			_, _ = w.WriteString("</div>\n")
			return
		}
	}
	_, _ = w.WriteString("<pre><code>")
	_, _ = w.WriteString(html.EscapeString(code))
	_, _ = w.WriteString("</code></pre>\n")
}

package render

import (
	"strings"
	"testing"
	"time"

	"github.com/olmesm/planman/internal/critic"
	"github.com/olmesm/planman/internal/review"
)

func TestRenderBlocksAndGFM(t *testing.T) {
	src := "# Title\n\nPara with ~~strike~~.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- [ ] task\n"
	res, err := Render(critic.Parse(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(res.Blocks))
	}
	all := ""
	for _, b := range res.Blocks {
		all += b.HTML
	}
	for _, want := range []string{"<h1", "<del>strike</del>", "<table>", `type="checkbox"`} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in output:\n%s", want, all)
		}
	}
}

func TestRenderMermaidAndCode(t *testing.T) {
	src := "```mermaid\ngraph TD;\nA-->B;\n```\n\n```go\npackage main\n```\n"
	res, err := Render(critic.Parse(src))
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, b := range res.Blocks {
		all += b.HTML
	}
	if !strings.Contains(all, `<pre class="mermaid">graph TD;`) {
		t.Errorf("mermaid block not passed through:\n%s", all)
	}
	if !strings.Contains(all, `class="highlight"`) || !strings.Contains(all, `<pre class="chroma">`) {
		t.Errorf("go block not chroma-highlighted with classes:\n%s", all)
	}
}

func TestCommentAttachesToBlock(t *testing.T) {
	doc := critic.Parse("Para one.\n\nPara two.\n\nPara three.\n")
	// anchor at line 2 = "Para two."
	doc.AddComment(review.Anchor{Line: 2}, "r", "note", time.Now())
	res, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(res.Blocks))
	}
	if len(res.Blocks[1].Comments) != 1 {
		t.Fatalf("comment should attach to block 1: %+v", res.Blocks)
	}
	if len(res.PageComments) != 0 {
		t.Fatalf("no page comments expected")
	}
}

func TestPageCommentsSeparated(t *testing.T) {
	doc := critic.Parse("Para.\n")
	doc.AddComment(review.Anchor{Page: true}, "r", "whole doc note", time.Now())
	res, err := Render(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PageComments) != 1 {
		t.Fatalf("expected 1 page comment, got %d", len(res.PageComments))
	}
}

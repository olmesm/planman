package critic

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

func TestParseCleanDocument(t *testing.T) {
	src := "# Title\n\nSome text.\n"
	doc := Parse(src)
	if len(doc.Comments) != 0 {
		t.Fatalf("expected no comments, got %d", len(doc.Comments))
	}
	if doc.Body() != "# Title\n\nSome text." {
		t.Fatalf("unexpected body: %q", doc.Body())
	}
}

func TestRoundTripStable(t *testing.T) {
	src := "# Title\n\nPara one.\n\nPara two.\n"
	doc := Parse(src)
	doc.AddBlockComment(2, "needs work", "reviewer", now)
	s1 := doc.Serialize()
	s2 := Parse(s1).Serialize()
	s3 := Parse(s2).Serialize()
	if s1 != s2 || s2 != s3 {
		t.Fatalf("round trip not stable:\n--- s1 ---\n%s\n--- s2 ---\n%s\n--- s3 ---\n%s", s1, s2, s3)
	}
	if !strings.Contains(s1, `{>>needs work<<}{id="`) {
		t.Fatalf("marker missing from serialized output:\n%s", s1)
	}
	if !strings.Contains(s1, "<!-- planman:comments") {
		t.Fatalf("endmatter missing:\n%s", s1)
	}
}

func TestMarkerAdjacencyPreserved(t *testing.T) {
	src := "Para one.\n\nPara two.\n"
	doc := Parse(src)
	doc.AddBlockComment(0, "on para one", "r", now)
	out := doc.Serialize()
	idx1 := strings.Index(out, "Para one.")
	idxM := strings.Index(out, "{>>on para one<<}")
	idx2 := strings.Index(out, "Para two.")
	if !(idx1 < idxM && idxM < idx2) {
		t.Fatalf("marker not between paragraphs:\n%s", out)
	}
	// Re-parse: anchor should still point at "Para one."
	doc2 := Parse(out)
	if len(doc2.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(doc2.Comments))
	}
	c := doc2.Comments[0]
	if got := doc2.BodyLines[c.AnchorLine]; got != "Para one." {
		t.Fatalf("anchor drifted to %q", got)
	}
}

func TestMarkerInCodeFenceIgnored(t *testing.T) {
	src := "Text.\n\n```\n{>>not a comment<<}{id=\"x1\"}\n```\n"
	doc := Parse(src)
	if len(doc.Comments) != 0 {
		t.Fatalf("marker inside fence should be ignored, got %d comments", len(doc.Comments))
	}
	if !strings.Contains(doc.Body(), `{>>not a comment<<}`) {
		t.Fatalf("fence content must be preserved:\n%s", doc.Body())
	}
}

func TestPageCommentAndReplies(t *testing.T) {
	doc := Parse("# Doc\n\nBody.\n")
	c := doc.AddPageComment("overall: too long", "reviewer", now)
	doc.AddReply(c.ID, "agree, trimming", "agent", now.Add(time.Minute))
	out := doc.Serialize()
	if strings.Contains(out, "{>>overall") {
		t.Fatalf("page comment must not create an inline marker:\n%s", out)
	}
	doc2 := Parse(out)
	if len(doc2.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(doc2.Comments))
	}
	got := doc2.Comments[0]
	if !got.Page || got.Text != "overall: too long" {
		t.Fatalf("page comment mangled: %+v", got)
	}
	if len(got.Replies) != 1 || got.Replies[0].Text != "agree, trimming" || got.Replies[0].Author != "agent" {
		t.Fatalf("replies mangled: %+v", got.Replies)
	}
}

func TestBlockCommentThreadMetadata(t *testing.T) {
	doc := Parse("Para.\n")
	c := doc.AddBlockComment(0, "fix this", "reviewer", now)
	doc.AddReply(c.ID, "done", "agent", now)
	doc2 := Parse(doc.Serialize())
	got := doc2.Comments[0]
	if got.Author != "reviewer" || !got.Ts.Equal(now) {
		t.Fatalf("metadata lost: %+v", got)
	}
	if len(got.Replies) != 1 || got.Replies[0].Text != "done" {
		t.Fatalf("reply lost: %+v", got.Replies)
	}
}

func TestDeleteComment(t *testing.T) {
	doc := Parse("Para.\n")
	c := doc.AddBlockComment(0, "temp", "r", now)
	out := doc.Serialize()
	doc2 := Parse(out)
	if !doc2.DeleteComment(c.ID) {
		t.Fatal("delete failed")
	}
	final := doc2.Serialize()
	if strings.Contains(final, "{>>") || strings.Contains(final, "planman:comments") {
		t.Fatalf("comment residue after delete:\n%s", final)
	}
	if strings.TrimSpace(final) != "Para." {
		t.Fatalf("body damaged: %q", final)
	}
}

func TestHandWrittenMarkerWithoutEndmatter(t *testing.T) {
	src := "Para.\n\n{>>agent wrote this by hand<<}{id=\"zz1\"}\n"
	doc := Parse(src)
	if len(doc.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(doc.Comments))
	}
	if doc.Comments[0].Text != "agent wrote this by hand" || doc.Comments[0].ID != "zz1" {
		t.Fatalf("bad comment: %+v", doc.Comments[0])
	}
}

func TestMultilineTextSanitized(t *testing.T) {
	doc := Parse("Para.\n")
	doc.AddBlockComment(0, "line one\nline two", "r", now)
	doc2 := Parse(doc.Serialize())
	if doc2.Comments[0].Text != "line one line two" {
		t.Fatalf("newlines should collapse: %q", doc2.Comments[0].Text)
	}
}

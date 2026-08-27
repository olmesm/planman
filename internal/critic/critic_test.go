package critic

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/olmesm/planman/internal/review"
)

var now = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

func blockAnchor(line int) review.Anchor { return review.Anchor{Line: line} }

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
	doc.AddComment(blockAnchor(2), "reviewer", "needs work", now)
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
	doc.AddComment(blockAnchor(0), "r", "on para one", now)
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
	if got := doc2.BodyLines[c.Anchor.Line]; got != "Para one." {
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
	c := doc.AddComment(review.Anchor{Page: true}, "reviewer", "overall: too long", now)
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
	if !got.Anchor.Page || got.Text != "overall: too long" {
		t.Fatalf("page comment mangled: %+v", got)
	}
	if len(got.Replies) != 1 || got.Replies[0].Text != "agree, trimming" || got.Replies[0].Author != "agent" {
		t.Fatalf("replies mangled: %+v", got.Replies)
	}
}

func TestBlockCommentThreadMetadata(t *testing.T) {
	doc := Parse("Para.\n")
	c := doc.AddComment(blockAnchor(0), "reviewer", "fix this", now)
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

func TestStatusRoundTrip(t *testing.T) {
	doc := Parse("Para.\n")
	c := doc.AddComment(blockAnchor(0), "r", "fix this", now)
	if c.Status != review.StatusOpen {
		t.Fatalf("new comment should be open, got %q", c.Status)
	}
	c.Status = review.StatusResolved
	out := doc.Serialize()
	if !strings.Contains(out, "status: resolved") {
		t.Fatalf("resolved status missing from endmatter:\n%s", out)
	}
	doc2 := Parse(out)
	if !doc2.Comments[0].Resolved() {
		t.Fatalf("status lost on reparse: %+v", doc2.Comments[0])
	}
	// Open threads carry no status key in the file.
	doc2.Comments[0].Status = review.StatusOpen
	if out := doc2.Serialize(); strings.Contains(out, "status:") {
		t.Fatalf("open status should be omitted:\n%s", out)
	}
}

func TestDeleteComment(t *testing.T) {
	doc := Parse("Para.\n")
	c := doc.AddComment(blockAnchor(0), "r", "temp", now)
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
	if doc.Comments[0].Status != review.StatusOpen {
		t.Fatalf("hand-written marker should be open: %+v", doc.Comments[0])
	}
}

func TestMultilineTextSanitized(t *testing.T) {
	doc := Parse("Para.\n")
	doc.AddComment(blockAnchor(0), "r", "line one\nline two", now)
	doc2 := Parse(doc.Serialize())
	if doc2.Comments[0].Text != "line one line two" {
		t.Fatalf("newlines should collapse: %q", doc2.Comments[0].Text)
	}
}

func TestStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/doc.md"
	if err := writeFile(path, "# Doc\n\nPara.\n"); err != nil {
		t.Fatal(err)
	}
	s := NewStore(path)

	c, err := s.Add(blockAnchor(2), "r", "first", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reply(c.ID, "agent", "on it", now); err != nil {
		t.Fatal(err)
	}
	got, err := s.SetStatus(c.ID, review.StatusResolved)
	if err != nil || !got.Resolved() {
		t.Fatalf("SetStatus: %v %+v", err, got)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 || len(list[0].Replies) != 1 || !list[0].Resolved() {
		t.Fatalf("List: %v %+v", err, list)
	}
	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(c.ID); err != review.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

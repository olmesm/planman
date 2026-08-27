package sidecar

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/olmesm/planman/internal/review"
)

var now = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

func TestCRUDRoundTrip(t *testing.T) {
	gitDir := t.TempDir()
	s := NewStore(gitDir)

	anchor := review.Anchor{File: "main.go", Side: review.SideNew, Line: 4, Context: "\tprintln(\"x\")"}
	c, err := s.Add(anchor, "olie", "  why println?  ", now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != "why println?" || c.Status != review.StatusOpen || c.Anchor != anchor {
		t.Fatalf("added comment wrong: %+v", c)
	}

	if _, err := s.Reply(c.ID, "agent", "it is a demo", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(c.ID, review.StatusResolved); err != nil {
		t.Fatal(err)
	}

	// A fresh store on the same dir sees everything.
	s2 := NewStore(gitDir)
	list, err := s2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || !list[0].Resolved() || len(list[0].Replies) != 1 {
		t.Fatalf("persisted state wrong: %+v", list)
	}
	if list[0].Anchor.Context != anchor.Context {
		t.Fatalf("anchor context lost: %+v", list[0].Anchor)
	}

	if err := s2.UpdateAnchor(c.ID, review.Anchor{File: "main.go", Side: review.SideNew, Line: 9, Context: anchor.Context}); err != nil {
		t.Fatal(err)
	}
	list, _ = s2.List()
	if list[0].Anchor.Line != 9 {
		t.Fatalf("anchor not updated: %+v", list[0].Anchor)
	}

	if err := s2.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(c.ID); err != review.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	s := NewStore(t.TempDir())
	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("empty store: %v %+v", err, list)
	}
}

func TestFileLandsUnderPlanmanDir(t *testing.T) {
	gitDir := t.TempDir()
	s := NewStore(gitDir)
	if _, err := s.Add(review.Anchor{Page: true}, "r", "overall", now); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(gitDir, "planman", "review.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("store file not at %s: %v", want, err)
	}
}

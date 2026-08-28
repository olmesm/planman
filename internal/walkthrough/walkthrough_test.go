package walkthrough

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func valid() Walkthrough {
	return Walkthrough{
		Version: Version,
		Title:   "The change",
		Chapters: []Chapter{{
			Title: "Core",
			Stops: []Stop{{
				Title:   "The heart of it",
				Prose:   "This is where it happens.",
				HunkIDs: []string{"main.go:h1"},
			}},
		}},
	}
}

func TestValidateAccepts(t *testing.T) {
	w := valid()
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Walkthrough)
		want   string
	}{
		{"wrong version", func(w *Walkthrough) { w.Version = 99 }, "version"},
		{"missing title", func(w *Walkthrough) { w.Title = "" }, "title"},
		{"no chapters", func(w *Walkthrough) { w.Chapters = nil }, "chapters"},
		{"stop without hunks", func(w *Walkthrough) { w.Chapters[0].Stops[0].HunkIDs = nil }, "hunk_ids"},
		{"bad importance", func(w *Walkthrough) { w.Chapters[0].Stops[0].Importance = "vital" }, "importance"},
		{"empty stop", func(w *Walkthrough) {
			w.Chapters[0].Stops[0].Title = ""
			w.Chapters[0].Stops[0].Prose = ""
		}, "title or prose"},
		{"commit without subject", func(w *Walkthrough) { w.Commit = &Commit{} }, "subject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := valid()
			tc.mutate(&w)
			err := w.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestHunkIDsCollectsEverything(t *testing.T) {
	w := valid()
	w.Chapters[0].Stops[0].Notes = []Note{{HunkID: "a.go:h2", Body: "note"}}
	w.Support = []Support{{HunkIDs: []string{"gen.go:h1"}, Reason: "generated"}}
	ids := w.HunkIDs()
	want := map[string]bool{"main.go:h1": true, "a.go:h2": true, "gen.go:h1": true}
	if len(ids) != 3 {
		t.Fatalf("ids: %v", ids)
	}
	for _, id := range ids {
		if !want[id] {
			t.Fatalf("unexpected id %s", id)
		}
	}
}

func TestSaveLoadDeleteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "planman", "walkthrough.json")
	if st, err := Load(path); err != nil || st != nil {
		t.Fatalf("missing file should load as nil, nil: %v %v", st, err)
	}
	w := valid()
	stored := &Stored{
		CreatedAt:        time.Now().UTC().Truncate(time.Second),
		Base:             "main",
		Head:             "@worktree",
		FileFingerprints: map[string]string{"main.go": "abc123"},
		W:                w,
	}
	if err := Save(path, stored); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.W.Title != w.Title || got.FileFingerprints["main.go"] != "abc123" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if err := Delete(path); err != nil {
		t.Fatal(err)
	}
	if st, err := Load(path); err != nil || st != nil {
		t.Fatalf("deleted file should load as nil, nil: %v %v", st, err)
	}
	if err := Delete(path); err != nil {
		t.Fatalf("double delete should be a no-op: %v", err)
	}
}

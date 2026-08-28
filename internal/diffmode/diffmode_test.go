package diffmode

import (
	"testing"

	"github.com/olmesm/planman/internal/review"
)

func TestShiftRangeStart(t *testing.T) {
	tests := []struct {
		name      string
		start     int
		oldEnd    int
		newEnd    int
		wantStart int
	}{
		{"single-line untouched", 0, 5, 8, 0},
		{"range shifts down with its end", 5, 8, 10, 7},
		{"range shifts up with its end", 5, 8, 6, 3},
		{"start clamped out of existence at top", 2, 8, 3, 0},
		{"span preserved when the whole range moves up", 7, 8, 7, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := review.Anchor{StartLine: tt.start, Line: tt.newEnd}
			shiftRangeStart(&a, tt.oldEnd)
			if a.StartLine != tt.wantStart {
				t.Fatalf("start %d end %d→%d: got StartLine %d, want %d",
					tt.start, tt.oldEnd, tt.newEnd, a.StartLine, tt.wantStart)
			}
		})
	}
}

func TestRangeSetCoversPlacedSpan(t *testing.T) {
	// A range 5–8 whose end re-anchored to line 10 must tint 7–10.
	ft := &fileThreads{byLine: map[threadKey][]*review.Comment{
		{side: "new", line: 10}: {{
			Anchor: review.Anchor{Side: review.SideNew, StartLine: 7, Line: 10},
		}},
	}}
	set := ft.rangeSet()
	for line := 7; line <= 10; line++ {
		if !set[threadKey{side: "new", line: line}] {
			t.Fatalf("line %d not in range set", line)
		}
	}
	if set[threadKey{side: "new", line: 6}] || set[threadKey{side: "old", line: 8}] {
		t.Fatal("range set leaked outside the span or side")
	}
	// Single-line threads produce no set at all.
	single := &fileThreads{byLine: map[threadKey][]*review.Comment{
		{side: "new", line: 3}: {{Anchor: review.Anchor{Side: review.SideNew, Line: 3}}},
	}}
	if single.rangeSet() != nil {
		t.Fatal("single-line thread should not build a range set")
	}
}

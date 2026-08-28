// Package walkthrough is planman's take on codiff's narrative
// walkthroughs: an agent authors a guided tour through the current
// diff — chapters of stops, each pointing at hunks by stable id with
// explanatory prose — and POSTs it to the review server. planman stays
// the passive side: it validates, stores, and renders the tour against
// the live diff, degrading gracefully when the diff has moved on.
package walkthrough

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Limits mirror the intent of codiff's schema: tours stay small enough
// to follow in one sitting.
const (
	Version        = 1
	maxChapters    = 8
	maxStops       = 20
	maxHunksPerRef = 14
)

// Walkthrough is the agent-authored tour.
type Walkthrough struct {
	Version  int       `json:"version"`
	Title    string    `json:"title"`
	Focus    string    `json:"focus,omitempty"` // one-line "what to look for"
	Chapters []Chapter `json:"chapters"`
	Support  []Support `json:"support,omitempty"`
	Commit   *Commit   `json:"commit,omitempty"`
}

// Chapter groups related stops.
type Chapter struct {
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"` // single emoji
	Blurb string `json:"blurb,omitempty"`
	Stops []Stop `json:"stops"`
}

// Stop is one step of the tour: prose about a handful of hunks.
type Stop struct {
	Title      string   `json:"title"`
	Prose      string   `json:"prose"`                // markdown
	Importance string   `json:"importance,omitempty"` // critical | normal | context
	HunkIDs    []string `json:"hunk_ids"`
	Notes      []Note   `json:"notes,omitempty"`
}

// Note attaches a remark to one specific hunk within a stop.
type Note struct {
	HunkID string `json:"hunk_id"`
	Body   string `json:"body"`
}

// Support flags hunks that back the tour without deserving a stop
// (generated files, mechanical renames).
type Support struct {
	HunkIDs []string `json:"hunk_ids"`
	Reason  string   `json:"reason"`
}

// Commit is the agent's suggested commit message for the change.
type Commit struct {
	Subject string `json:"subject"`
	Body    string `json:"body,omitempty"`
}

// Stops flattens the chapters into tour order.
func (w *Walkthrough) Stops() []Stop {
	var stops []Stop
	for _, c := range w.Chapters {
		stops = append(stops, c.Stops...)
	}
	return stops
}

// HunkIDs returns every hunk id the walkthrough references.
func (w *Walkthrough) HunkIDs() []string {
	var ids []string
	for _, c := range w.Chapters {
		for _, s := range c.Stops {
			ids = append(ids, s.HunkIDs...)
			for _, n := range s.Notes {
				ids = append(ids, n.HunkID)
			}
		}
	}
	for _, sup := range w.Support {
		ids = append(ids, sup.HunkIDs...)
	}
	return ids
}

// Validate checks structural constraints; hunk-id resolution against the
// live diff is the caller's job.
func (w *Walkthrough) Validate() error {
	if w.Version != Version {
		return fmt.Errorf("version must be %d", Version)
	}
	if w.Title == "" {
		return errors.New("title is required")
	}
	if len(w.Chapters) == 0 || len(w.Chapters) > maxChapters {
		return fmt.Errorf("need 1-%d chapters", maxChapters)
	}
	for ci, c := range w.Chapters {
		if c.Title == "" {
			return fmt.Errorf("chapter %d: title is required", ci+1)
		}
		if len(c.Stops) == 0 || len(c.Stops) > maxStops {
			return fmt.Errorf("chapter %q: need 1-%d stops", c.Title, maxStops)
		}
		for si, s := range c.Stops {
			where := fmt.Sprintf("chapter %q stop %d", c.Title, si+1)
			if s.Title == "" && s.Prose == "" {
				return fmt.Errorf("%s: needs a title or prose", where)
			}
			if len(s.HunkIDs) == 0 || len(s.HunkIDs) > maxHunksPerRef {
				return fmt.Errorf("%s: need 1-%d hunk_ids", where, maxHunksPerRef)
			}
			switch s.Importance {
			case "", "critical", "normal", "context":
			default:
				return fmt.Errorf("%s: importance must be critical, normal, or context", where)
			}
		}
	}
	for _, sup := range w.Support {
		if len(sup.HunkIDs) == 0 || len(sup.HunkIDs) > maxHunksPerRef {
			return fmt.Errorf("support group: need 1-%d hunk_ids", maxHunksPerRef)
		}
	}
	if w.Commit != nil && w.Commit.Subject == "" {
		return errors.New("commit suggestion needs a subject")
	}
	return nil
}

// Stored is the persisted envelope: the walkthrough plus the comparison
// it was authored against, so drift is detectable at render time.
type Stored struct {
	CreatedAt time.Time `json:"created_at"`
	Base      string    `json:"base"`
	Head      string    `json:"head"`
	BaseSHA   string    `json:"base_sha,omitempty"`
	HeadSHA   string    `json:"head_sha,omitempty"`
	IgnoreWS  bool      `json:"ignore_ws,omitempty"`
	// FileFingerprints maps each referenced file's path to its patch
	// fingerprint at authoring time; a mismatch means the tour is stale
	// for that file even if the SHAs still agree (worktree heads).
	FileFingerprints map[string]string `json:"file_fingerprints"`
	W                Walkthrough       `json:"walkthrough"`
}

// Path locates the walkthrough sidecar next to the review store.
func Path(gitDir string) string {
	return filepath.Join(gitDir, "planman", "walkthrough.json")
}

// Load reads the stored walkthrough; a missing file returns (nil, nil).
func Load(path string) (*Stored, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st Stored
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &st, nil
}

// Save writes the envelope atomically.
func Save(path string, st *Stored) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Delete removes the stored walkthrough; missing is not an error.
func Delete(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

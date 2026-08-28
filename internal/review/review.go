// Package review defines the shared domain model for a planman review:
// comment threads with authors, replies, and an open/resolved status,
// anchored to a target — a block in a markdown document, a line in a
// diff, or the review as a whole. Persistence is behind the Store
// interface; the markdown-file and sidecar backends implement it.
package review

import (
	"errors"
	"fmt"
	"math/rand"
	"time"
)

// ErrNotFound is returned by Store operations on an unknown comment id.
var ErrNotFound = errors.New("comment not found")

// Status is a thread's lifecycle state.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

// ParseStatus validates a status string, treating empty as open.
func ParseStatus(s string) (Status, error) {
	switch Status(s) {
	case StatusOpen, "":
		return StatusOpen, nil
	case StatusResolved:
		return StatusResolved, nil
	}
	return "", fmt.Errorf("invalid status %q", s)
}

// Side identifies which side of a diff a line anchor refers to.
type Side string

const (
	SideOld Side = "old"
	SideNew Side = "new"
)

// Anchor locates what a comment thread is attached to.
//
// Review-level comments set Page. Document anchors use Line (a body line
// of the markdown file). Diff anchors use File, Side, and Line, plus
// Context — the exact text of the anchored line — so the thread can be
// re-attached after the underlying content shifts.
type Anchor struct {
	Page    bool   `json:"page,omitempty"`
	File    string `json:"file,omitempty"`
	Side    Side   `json:"side,omitempty"`
	Line    int    `json:"line,omitempty"`
	Context string `json:"context,omitempty"`

	// Diff anchors also record the comparison the thread was created
	// against — the selected refs plus the SHAs they resolved to at the
	// time — so a review can navigate back to that exact point later.
	// HeadSHA stays empty for pseudo heads (worktree, index): those
	// endpoints have no commit identity to return to.
	Base    string `json:"base,omitempty"`
	Head    string `json:"head,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`
}

// Reply is a threaded response to a comment.
type Reply struct {
	Author string    `json:"author,omitempty"`
	Ts     time.Time `json:"ts,omitempty"`
	Text   string    `json:"text"`
}

// Comment is one review thread.
type Comment struct {
	ID      string    `json:"id"`
	Author  string    `json:"author,omitempty"`
	Ts      time.Time `json:"ts,omitempty"`
	Text    string    `json:"text"`
	Status  Status    `json:"status"`
	Anchor  Anchor    `json:"anchor"`
	Replies []Reply   `json:"replies,omitempty"`
}

// Resolved reports whether the thread is resolved.
func (c *Comment) Resolved() bool { return c.Status == StatusResolved }

// Store is a persistence backend for review comments.
type Store interface {
	// List returns all comment threads.
	List() ([]*Comment, error)
	// Add creates a new open thread and returns it.
	Add(a Anchor, author, text string, now time.Time) (*Comment, error)
	// Reply appends a reply to a thread and returns the updated thread.
	Reply(id, author, text string, now time.Time) (*Comment, error)
	// SetStatus resolves or reopens a thread and returns the updated thread.
	SetStatus(id string, s Status) (*Comment, error)
	// Delete removes a thread entirely.
	Delete(id string) error
}

// CountOpen returns how many of the given threads are open.
func CountOpen(cs []*Comment) int {
	n := 0
	for _, c := range cs {
		if !c.Resolved() {
			n++
		}
	}
	return n
}

var idRand = rand.New(rand.NewSource(time.Now().UnixNano()))

// NewID generates a short comment id. Callers must retry on collision
// with existing ids in their store.
func NewID() string {
	return fmt.Sprintf("c%04x", idRand.Intn(0x10000))
}

// UniqueID generates an id not present in taken.
func UniqueID(taken func(id string) bool) string {
	for {
		if id := NewID(); !taken(id) {
			return id
		}
	}
}

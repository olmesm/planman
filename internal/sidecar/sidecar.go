// Package sidecar persists diff-review threads in a JSON file under the
// repository's .git directory (.git/planman/review.json) — per-repo,
// invisible to the worktree, and gone with the clone. Diff comments
// cannot live inside the reviewed files the way document comments do, so
// this is their home.
package sidecar

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/olmesm/planman/internal/review"
)

const fileName = "review.json"

type state struct {
	Comments []*review.Comment `json:"comments"`
}

// Store is a review.Store backed by .git/planman/review.json.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns the store for a repository's git directory.
func NewStore(gitDir string) *Store {
	return &Store{path: filepath.Join(gitDir, "planman", fileName)}
}

// Path returns the JSON file's location.
func (s *Store) Path() string { return s.path }

func (s *Store) load() (*state, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &state{}, nil
	}
	if err != nil {
		return nil, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Store) save(st *state) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".review-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *Store) mutate(fn func(*state) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return s.save(st)
}

func (st *state) find(id string) *review.Comment {
	for _, c := range st.Comments {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// List implements review.Store.
func (s *Store) List() ([]*review.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	return st.Comments, nil
}

// Add implements review.Store.
func (s *Store) Add(a review.Anchor, author, text string, now time.Time) (*review.Comment, error) {
	var c *review.Comment
	err := s.mutate(func(st *state) error {
		c = &review.Comment{
			ID:     review.UniqueID(func(id string) bool { return st.find(id) != nil }),
			Author: author,
			Ts:     now.UTC().Truncate(time.Second),
			Text:   strings.TrimSpace(text),
			Status: review.StatusOpen,
			Anchor: a,
		}
		st.Comments = append(st.Comments, c)
		return nil
	})
	return c, err
}

// Reply implements review.Store.
func (s *Store) Reply(id, author, text string, now time.Time) (*review.Comment, error) {
	var c *review.Comment
	err := s.mutate(func(st *state) error {
		if c = st.find(id); c == nil {
			return review.ErrNotFound
		}
		c.Replies = append(c.Replies, review.Reply{
			Author: author,
			Ts:     now.UTC().Truncate(time.Second),
			Text:   strings.TrimSpace(text),
		})
		return nil
	})
	return c, err
}

// SetStatus implements review.Store.
func (s *Store) SetStatus(id string, status review.Status) (*review.Comment, error) {
	var c *review.Comment
	err := s.mutate(func(st *state) error {
		if c = st.find(id); c == nil {
			return review.ErrNotFound
		}
		c.Status = status
		return nil
	})
	return c, err
}

// Delete implements review.Store.
func (s *Store) Delete(id string) error {
	return s.mutate(func(st *state) error {
		for i, c := range st.Comments {
			if c.ID == id {
				st.Comments = append(st.Comments[:i], st.Comments[i+1:]...)
				return nil
			}
		}
		return review.ErrNotFound
	})
}

// UpdateAnchor rewrites a thread's anchor after re-anchoring against a
// fresh diff.
func (s *Store) UpdateAnchor(id string, a review.Anchor) error {
	return s.mutate(func(st *state) error {
		c := st.find(id)
		if c == nil {
			return review.ErrNotFound
		}
		c.Anchor = a
		return nil
	})
}

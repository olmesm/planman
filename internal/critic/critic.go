// Package critic persists review threads inside a markdown file. Block
// comments are CriticMarkup comment markers on their own line:
//
//	{>>needs a citation<<}{id="c1"}
//
// placed after the block they refer to. Review-level comments and all
// thread metadata (author, timestamp, status, replies) live in a YAML
// endmatter section at the bottom of the file, wrapped in an HTML comment
// so ordinary renderers ignore it:
//
//	<!-- planman:comments
//	comments:
//	  - id: c1
//	    author: reviewer
//	    ts: 2026-08-03T12:00:00Z
//	    status: resolved
//	-->
package critic

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/olmesm/planman/internal/review"
	"gopkg.in/yaml.v3"
)

const (
	endmatterOpen  = "<!-- planman:comments"
	endmatterClose = "-->"
)

var markerRe = regexp.MustCompile(`^\{>>(.*)<<\}\{id="([A-Za-z0-9_-]+)"\}\s*$`)

// emComment is the YAML shape of one thread in the endmatter.
type emComment struct {
	ID      string         `yaml:"id"`
	Author  string         `yaml:"author,omitempty"`
	Ts      time.Time      `yaml:"ts,omitempty"`
	Status  string         `yaml:"status,omitempty"` // omitted when open
	Page    bool           `yaml:"page,omitempty"`
	Text    string         `yaml:"text,omitempty"` // markers carry block text; endmatter carries page text
	Replies []review.Reply `yaml:"replies,omitempty"`
}

type endmatter struct {
	Comments []*emComment `yaml:"comments"`
}

// Document is a parsed markdown file: the body with comment markers and
// endmatter stripped out, plus the extracted threads. Block threads have
// Anchor.Line set to the body line their marker follows; review-level
// threads have Anchor.Page set.
type Document struct {
	// BodyLines is the markdown content without markers or endmatter.
	BodyLines []string
	Comments  []*review.Comment
}

// Body returns the clean markdown source.
func (d *Document) Body() string {
	return strings.Join(d.BodyLines, "\n")
}

// Comment returns the thread with the given id, or nil.
func (d *Document) Comment(id string) *review.Comment {
	for _, c := range d.Comments {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Parse extracts threads and endmatter from a markdown file's contents.
func Parse(src string) *Document {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	lines, meta := splitEndmatter(lines)

	byID := map[string]*review.Comment{}
	var order []*review.Comment
	for _, em := range meta.Comments {
		st, err := review.ParseStatus(em.Status)
		if err != nil {
			st = review.StatusOpen
		}
		c := &review.Comment{
			ID:      em.ID,
			Author:  em.Author,
			Ts:      em.Ts,
			Text:    em.Text,
			Status:  st,
			Anchor:  review.Anchor{Page: em.Page, Line: -1},
			Replies: em.Replies,
		}
		byID[c.ID] = c
		order = append(order, c)
	}

	doc := &Document{}
	seenInline := map[string]bool{}
	inFence := false
	fenceMark := ""
	lastContent := -1
	skipNextBlank := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if skipNextBlank {
			skipNextBlank = false
			if trimmed == "" {
				continue
			}
		}
		if inFence {
			if strings.HasPrefix(trimmed, fenceMark) {
				inFence = false
			}
			doc.BodyLines = append(doc.BodyLines, line)
			lastContent = len(doc.BodyLines) - 1
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = true
			fenceMark = trimmed[:3]
			doc.BodyLines = append(doc.BodyLines, line)
			lastContent = len(doc.BodyLines) - 1
			continue
		}
		if m := markerRe.FindStringSubmatch(line); m != nil {
			text, id := unescapeText(m[1]), m[2]
			c := byID[id]
			if c == nil {
				c = &review.Comment{ID: id, Status: review.StatusOpen}
				byID[id] = c
				order = append(order, c)
			}
			c.Text = text
			c.Anchor.Page = false
			c.Anchor.Line = lastContent
			seenInline[id] = true
			// Swallow the blank padding around the marker so repeated
			// parse/serialize cycles don't accumulate blank lines.
			if n := len(doc.BodyLines); n > 0 && lastContent < n-1 && strings.TrimSpace(doc.BodyLines[n-1]) == "" {
				doc.BodyLines = doc.BodyLines[:n-1]
			}
			skipNextBlank = true
			continue
		}
		doc.BodyLines = append(doc.BodyLines, line)
		if trimmed != "" {
			lastContent = len(doc.BodyLines) - 1
		}
	}

	// Keep only threads that are either review-level or had an inline
	// marker; endmatter entries whose marker was deleted are dropped.
	for _, c := range order {
		if c.Anchor.Page || seenInline[c.ID] {
			if c.Anchor.Page {
				c.Anchor.Line = -1
			}
			doc.Comments = append(doc.Comments, c)
		}
	}
	// Trim trailing blank lines left behind by stripping.
	for len(doc.BodyLines) > 0 && strings.TrimSpace(doc.BodyLines[len(doc.BodyLines)-1]) == "" {
		doc.BodyLines = doc.BodyLines[:len(doc.BodyLines)-1]
	}
	return doc
}

// Serialize renders the document back to file contents: body with markers
// re-inserted after their anchor lines, then the YAML endmatter.
func (d *Document) Serialize() string {
	markersAt := map[int][]*review.Comment{}
	var early []*review.Comment // anchor no longer exists (e.g. block deleted)
	for _, c := range d.Comments {
		if c.Anchor.Page {
			continue
		}
		if c.Anchor.Line >= 0 && c.Anchor.Line < len(d.BodyLines) {
			markersAt[c.Anchor.Line] = append(markersAt[c.Anchor.Line], c)
		} else {
			early = append(early, c)
		}
	}

	var out []string
	emit := func(c *review.Comment) {
		out = append(out, "", fmt.Sprintf(`{>>%s<<}{id=%q}`, escapeText(c.Text), c.ID))
	}
	for _, c := range early {
		emit(c)
	}
	for i, line := range d.BodyLines {
		out = append(out, line)
		nextBlank := i+1 >= len(d.BodyLines) || strings.TrimSpace(d.BodyLines[i+1]) == ""
		for _, c := range markersAt[i] {
			emit(c)
			if !nextBlank {
				out = append(out, "")
			}
		}
	}
	body := strings.Join(out, "\n")
	body = strings.TrimRight(body, "\n") + "\n"

	if len(d.Comments) > 0 {
		em := endmatter{}
		for _, c := range d.Comments {
			e := &emComment{
				ID:      c.ID,
				Author:  c.Author,
				Ts:      c.Ts,
				Page:    c.Anchor.Page,
				Replies: c.Replies,
			}
			if c.Status != review.StatusOpen {
				e.Status = string(c.Status)
			}
			if c.Anchor.Page {
				e.Text = c.Text
			}
			em.Comments = append(em.Comments, e)
		}
		y, err := yaml.Marshal(em)
		if err == nil {
			body += "\n" + endmatterOpen + "\n" + string(y) + endmatterClose + "\n"
		}
	}
	return body
}

// AddComment attaches a new open thread. Page anchors become review-level
// comments; otherwise Anchor.Line is the body line the marker follows.
func (d *Document) AddComment(a review.Anchor, author, text string, now time.Time) *review.Comment {
	if a.Page {
		a.Line = -1
		text = strings.TrimSpace(text)
	} else {
		text = sanitizeInline(text)
	}
	c := &review.Comment{
		ID:     review.UniqueID(func(id string) bool { return d.Comment(id) != nil }),
		Author: author,
		Ts:     now.UTC().Truncate(time.Second),
		Text:   text,
		Status: review.StatusOpen,
		Anchor: review.Anchor{Page: a.Page, Line: a.Line},
	}
	d.Comments = append(d.Comments, c)
	return c
}

// AddReply appends a reply to the thread with the given id.
func (d *Document) AddReply(id, text, author string, now time.Time) *review.Comment {
	c := d.Comment(id)
	if c == nil {
		return nil
	}
	c.Replies = append(c.Replies, review.Reply{
		Author: author,
		Ts:     now.UTC().Truncate(time.Second),
		Text:   strings.TrimSpace(text),
	})
	return c
}

// DeleteComment removes a thread by id.
func (d *Document) DeleteComment(id string) bool {
	for i, c := range d.Comments {
		if c.ID == id {
			d.Comments = append(d.Comments[:i], d.Comments[i+1:]...)
			return true
		}
	}
	return false
}

func splitEndmatter(lines []string) ([]string, endmatter) {
	var meta endmatter
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == endmatterOpen {
			start = i
			break
		}
	}
	if start == -1 {
		return lines, meta
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == endmatterClose {
			end = i
			break
		}
	}
	if end == -1 {
		return lines, meta
	}
	yamlSrc := strings.Join(lines[start+1:end], "\n")
	_ = yaml.Unmarshal([]byte(yamlSrc), &meta)
	rest := append([]string{}, lines[:start]...)
	rest = append(rest, lines[end+1:]...)
	return rest, meta
}

// sanitizeInline makes text safe to embed in a single-line marker.
func sanitizeInline(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func escapeText(s string) string {
	return strings.ReplaceAll(s, "<<}", "<<\\}")
}

func unescapeText(s string) string {
	return strings.ReplaceAll(s, "<<\\}", "<<}")
}

// Store is a review.Store whose backing storage is the markdown file
// itself. All operations are atomic read-modify-writes of the file.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a store for the given markdown file.
func NewStore(path string) *Store { return &Store{path: path} }

// Path returns the absolute path of the backing file.
func (s *Store) Path() string { return s.path }

// Load reads and parses the current file contents.
func (s *Store) Load() (*Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*Document, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	return Parse(string(b)), nil
}

// save writes the document atomically (temp file + rename).
func (s *Store) save(doc *Document) error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".planman-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(doc.Serialize()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Mutate runs fn against the parsed document and saves the result.
func (s *Store) Mutate(fn func(*Document) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(doc); err != nil {
		return err
	}
	return s.save(doc)
}

// List implements review.Store.
func (s *Store) List() ([]*review.Comment, error) {
	doc, err := s.Load()
	if err != nil {
		return nil, err
	}
	return doc.Comments, nil
}

// Add implements review.Store.
func (s *Store) Add(a review.Anchor, author, text string, now time.Time) (*review.Comment, error) {
	var c *review.Comment
	err := s.Mutate(func(d *Document) error {
		c = d.AddComment(a, author, text, now)
		return nil
	})
	return c, err
}

// Reply implements review.Store.
func (s *Store) Reply(id, author, text string, now time.Time) (*review.Comment, error) {
	var c *review.Comment
	err := s.Mutate(func(d *Document) error {
		if c = d.AddReply(id, text, author, now); c == nil {
			return review.ErrNotFound
		}
		return nil
	})
	return c, err
}

// SetStatus implements review.Store.
func (s *Store) SetStatus(id string, st review.Status) (*review.Comment, error) {
	var c *review.Comment
	err := s.Mutate(func(d *Document) error {
		if c = d.Comment(id); c == nil {
			return review.ErrNotFound
		}
		c.Status = st
		return nil
	})
	return c, err
}

// Delete implements review.Store.
func (s *Store) Delete(id string) error {
	return s.Mutate(func(d *Document) error {
		if !d.DeleteComment(id) {
			return review.ErrNotFound
		}
		return nil
	})
}

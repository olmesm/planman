// Package critic reads and writes review comments stored inside a markdown
// file. Block comments are CriticMarkup comment markers on their own line:
//
//	{>>needs a citation<<}{id="c1"}
//
// placed after the block they refer to. Page comments and all thread
// metadata (author, timestamp, replies) live in a YAML endmatter section at
// the bottom of the file, wrapped in an HTML comment so renderers ignore it:
//
//	<!-- planman:comments
//	comments:
//	  - id: c1
//	    author: reviewer
//	    ts: 2026-08-03T12:00:00Z
//	    replies: []
//	-->
package critic

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	endmatterOpen  = "<!-- planman:comments"
	endmatterClose = "-->"
)

var markerRe = regexp.MustCompile(`^\{>>(.*)<<\}\{id="([A-Za-z0-9_-]+)"\}\s*$`)

// Reply is a threaded response to a comment.
type Reply struct {
	Author string    `yaml:"author,omitempty" json:"author,omitempty"`
	Ts     time.Time `yaml:"ts,omitempty" json:"ts,omitempty"`
	Text   string    `yaml:"text" json:"text"`
}

// Comment is a review comment on a block or on the whole page.
type Comment struct {
	ID     string    `yaml:"id" json:"id"`
	Author string    `yaml:"author,omitempty" json:"author,omitempty"`
	Ts     time.Time `yaml:"ts,omitempty" json:"ts,omitempty"`
	Page   bool      `yaml:"page,omitempty" json:"page,omitempty"`
	// Text is stored inline in the marker for block comments and here in
	// the endmatter for page comments. After Parse it is always populated.
	Text    string  `yaml:"text,omitempty" json:"text"`
	Replies []Reply `yaml:"replies,omitempty" json:"replies,omitempty"`

	// AnchorLine is the index into Document.BodyLines of the content line
	// this comment's marker follows. -1 for page comments.
	AnchorLine int `yaml:"-" json:"-"`
}

type endmatter struct {
	Comments []*Comment `yaml:"comments"`
}

// Document is a parsed markdown file: the body with comment markers and
// endmatter stripped out, plus the extracted comments.
type Document struct {
	// BodyLines is the markdown content without markers or endmatter.
	BodyLines []string
	Comments  []*Comment
}

// Body returns the clean markdown source.
func (d *Document) Body() string {
	return strings.Join(d.BodyLines, "\n")
}

// Comment returns the comment with the given id, or nil.
func (d *Document) Comment(id string) *Comment {
	for _, c := range d.Comments {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Parse extracts comments and endmatter from a markdown file's contents.
func Parse(src string) *Document {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	lines, meta := splitEndmatter(lines)

	byID := map[string]*Comment{}
	for _, c := range meta.Comments {
		c.AnchorLine = -1
		byID[c.ID] = c
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
				c = &Comment{ID: id}
				byID[id] = c
				meta.Comments = append(meta.Comments, c)
			}
			c.Text = text
			c.Page = false
			c.AnchorLine = lastContent
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

	// Keep only comments that are either page comments or had an inline
	// marker; endmatter entries whose marker was deleted are dropped.
	for _, c := range meta.Comments {
		if c.Page || seenInline[c.ID] {
			if c.Page {
				c.AnchorLine = -1
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
	markersAt := map[int][]*Comment{}
	var early []*Comment // anchor no longer exists (e.g. block deleted)
	for _, c := range d.Comments {
		if c.Page {
			continue
		}
		if c.AnchorLine >= 0 && c.AnchorLine < len(d.BodyLines) {
			markersAt[c.AnchorLine] = append(markersAt[c.AnchorLine], c)
		} else {
			early = append(early, c)
		}
	}

	var out []string
	emit := func(c *Comment) {
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
		em := endmatter{Comments: d.Comments}
		y, err := yaml.Marshal(em)
		if err == nil {
			body += "\n" + endmatterOpen + "\n" + string(y) + endmatterClose + "\n"
		}
	}
	return body
}

// AddBlockComment attaches a new comment after the given body line.
func (d *Document) AddBlockComment(anchorLine int, text, author string, now time.Time) *Comment {
	c := &Comment{
		ID:         newID(d),
		Author:     author,
		Ts:         now.UTC().Truncate(time.Second),
		Text:       sanitizeInline(text),
		AnchorLine: anchorLine,
	}
	d.Comments = append(d.Comments, c)
	return c
}

// AddPageComment attaches a new comment to the whole document.
func (d *Document) AddPageComment(text, author string, now time.Time) *Comment {
	c := &Comment{
		ID:         newID(d),
		Author:     author,
		Ts:         now.UTC().Truncate(time.Second),
		Text:       strings.TrimSpace(text),
		Page:       true,
		AnchorLine: -1,
	}
	d.Comments = append(d.Comments, c)
	return c
}

// AddReply appends a reply to the comment with the given id.
func (d *Document) AddReply(id, text, author string, now time.Time) bool {
	c := d.Comment(id)
	if c == nil {
		return false
	}
	c.Replies = append(c.Replies, Reply{
		Author: author,
		Ts:     now.UTC().Truncate(time.Second),
		Text:   strings.TrimSpace(text),
	})
	return true
}

// DeleteComment removes a comment (and its thread) by id.
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

var idRand = rand.New(rand.NewSource(time.Now().UnixNano()))

func newID(d *Document) string {
	for {
		id := fmt.Sprintf("c%04x", idRand.Intn(0x10000))
		if d.Comment(id) == nil {
			return id
		}
	}
}

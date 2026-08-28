package diffmode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/olmesm/planman/internal/review"
)

// exportPayload is the machine-readable handback artifact.
type exportPayload struct {
	Root        string            `json:"root"`
	Base        string            `json:"base"`
	Head        string            `json:"head"`
	GeneratedAt time.Time         `json:"generated_at"`
	Comments    []*review.Comment `json:"comments"`
}

// WriteExport writes the current review as JSON and markdown under
// .git/planman and returns the JSON path. The agent reads this after
// handback to work the open threads.
func (m *Mode) WriteExport(now time.Time) (string, error) {
	comments, err := m.store.List()
	if err != nil {
		return "", err
	}
	st := m.state()
	dir := filepath.Dir(m.store.Path())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	payload := exportPayload{
		Root:        m.repo.Root,
		Base:        st.base,
		Head:        st.head,
		GeneratedAt: now.UTC().Truncate(time.Second),
		Comments:    comments,
	}
	jsonPath := filepath.Join(dir, "handback.json")
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(jsonPath, append(b, '\n'), 0o644); err != nil {
		return "", err
	}

	mdPath := filepath.Join(dir, "handback.md")
	if err := os.WriteFile(mdPath, []byte(exportMarkdown(payload)), 0o644); err != nil {
		return "", err
	}
	return jsonPath, nil
}

// ExportMarkdown renders the current review as markdown without writing
// anything — the "copy review" button serves it for the clipboard.
func (m *Mode) ExportMarkdown(now time.Time) (string, error) {
	comments, err := m.store.List()
	if err != nil {
		return "", err
	}
	st := m.state()
	return exportMarkdown(exportPayload{
		Root:        m.repo.Root,
		Base:        st.base,
		Head:        st.head,
		GeneratedAt: now.UTC().Truncate(time.Second),
		Comments:    comments,
	}), nil
}

// exportMarkdown renders the review as markdown grouped by file.
func exportMarkdown(p exportPayload) string {
	var sb strings.Builder
	open := review.CountOpen(p.Comments)
	fmt.Fprintf(&sb, "# planman review — %d open comment(s), %d total\n\n", open, len(p.Comments))
	fmt.Fprintf(&sb, "Comparing %s ⟵ %s\n", p.Base, headLabel(p.Head))

	byFile := map[string][]*review.Comment{}
	var pageLevel []*review.Comment
	for _, c := range p.Comments {
		if c.Anchor.Page {
			pageLevel = append(pageLevel, c)
			continue
		}
		byFile[c.Anchor.File] = append(byFile[c.Anchor.File], c)
	}

	writeThread := func(c *review.Comment) {
		loc := ""
		if !c.Anchor.Page {
			if c.Anchor.StartLine > 0 {
				loc = fmt.Sprintf("%s lines %d–%d, ", c.Anchor.Side, c.Anchor.StartLine, c.Anchor.Line)
			} else {
				loc = fmt.Sprintf("%s line %d, ", c.Anchor.Side, c.Anchor.Line)
			}
		}
		fmt.Fprintf(&sb, "- **[%s]** (%s%s) %s: %s\n", c.ID, loc, c.Status, c.Author, c.Text)
		if c.Anchor.Context != "" {
			fmt.Fprintf(&sb, "  > `%s`\n", c.Anchor.Context)
		}
		if c.Anchor.Base != "" {
			fmt.Fprintf(&sb, "  made against %s ⟵ %s\n", c.Anchor.Base, headLabel(c.Anchor.Head))
		}
		for _, r := range c.Replies {
			fmt.Fprintf(&sb, "  - reply %s: %s\n", r.Author, r.Text)
		}
	}

	if len(pageLevel) > 0 {
		sb.WriteString("\n## Review-level\n\n")
		for _, c := range pageLevel {
			writeThread(c)
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		fmt.Fprintf(&sb, "\n## %s\n\n", f)
		for _, c := range byFile[f] {
			writeThread(c)
		}
	}
	return sb.String()
}

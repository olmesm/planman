package diffmode

// Diff extras: rendered markdown previews for .md files and before/after
// previews for image files, both served against the current comparison.

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/olmesm/planman/internal/gitdiff"
	"github.com/olmesm/planman/internal/render"
)

func queryEscape(s string) string { return url.QueryEscape(s) }

func isMarkdownPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// imageContentTypes whitelists the extensions the blob route will serve,
// mirroring codiff's image-diff support.
var imageContentTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".avif": "image/avif",
	".svg":  "image/svg+xml",
}

func isImagePath(p string) bool {
	_, ok := imageContentTypes[strings.ToLower(path.Ext(p))]
	return ok
}

// handleMarkdownPreview renders the head-side content of a markdown file
// in the diff as prose, with the restricted renderer — repo content is
// untrusted, unlike the explicitly opened document in doc mode.
func (m *Mode) handleMarkdownPreview(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !isMarkdownPath(p) {
		http.Error(w, "not a markdown file", http.StatusBadRequest)
		return
	}
	lines, err := m.repo.FileLines(m.newSideRef(), p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="md-preview markdown-body">%s</div>`,
		render.Inline(strings.Join(lines, "\n")))
}

// handleBlob serves an image's bytes at either comparison endpoint.
func (m *Mode) handleBlob(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := q.Get("path")
	ct, ok := imageContentTypes[strings.ToLower(path.Ext(p))]
	if p == "" || !ok {
		http.Error(w, "not a previewable image", http.StatusBadRequest)
		return
	}
	var ref string
	switch q.Get("ref") {
	case "head":
		ref = m.newSideRef()
	case "base":
		_, _, baseSHA, _ := m.rangeRef()
		if baseSHA == "" {
			http.Error(w, "no base commit", http.StatusNotFound)
			return
		}
		ref = baseSHA
	default:
		http.Error(w, "ref must be base or head", http.StatusBadRequest)
		return
	}
	b, err := m.repo.FileBytes(ref, p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-store")
	if ct == "image/svg+xml" {
		// Never let repo-controlled SVG run script in our origin.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	_, _ = w.Write(b)
}

// imageVM drives the before/after preview for a binary image file.
type imageVM struct {
	OldURL string
	NewURL string
}

// imageURLs builds the blob URLs for a changed image, omitting the side
// that does not exist for added or deleted files.
func imageURLs(f *gitdiff.File) *imageVM {
	if !isImagePath(f.Path()) {
		return nil
	}
	vm := &imageVM{}
	if f.Status != gitdiff.Added {
		old := f.OldPath
		if old == "" {
			old = f.Path()
		}
		vm.OldURL = "/blob?ref=base&path=" + queryEscape(old)
	}
	if f.Status != gitdiff.Deleted {
		vm.NewURL = "/blob?ref=head&path=" + queryEscape(f.NewPath) + "&fp=" + f.Fingerprint
	}
	return vm
}

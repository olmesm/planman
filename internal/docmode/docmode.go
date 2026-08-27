// Package docmode is the markdown-document review surface: the file is
// rendered block by block, threads anchor to blocks, and comments
// persist inside the file itself as CriticMarkup (via the critic store).
package docmode

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/olmesm/planman/internal/critic"
	"github.com/olmesm/planman/internal/render"
	"github.com/olmesm/planman/internal/review"
	"github.com/olmesm/planman/internal/server"
)

// Mode reviews a single markdown file.
type Mode struct {
	store *critic.Store
}

// New creates the mode for the given file.
func New(path string) (*Mode, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	return &Mode{store: critic.NewStore(abs)}, nil
}

// Path returns the absolute path of the reviewed file.
func (m *Mode) Path() string { return m.store.Path() }

// Name implements server.Mode.
func (m *Mode) Name() string { return "doc" }

// Title implements server.Mode.
func (m *Mode) Title() string { return filepath.Base(m.store.Path()) }

// Store implements server.Mode.
func (m *Mode) Store() review.Store { return m.store }

// ApplyQuery implements server.Mode; documents have no view state.
func (m *Mode) ApplyQuery(url.Values) {}

type contentData struct {
	Blocks       []render.Block
	PageComments []*review.Comment
}

// Content implements server.Mode.
func (m *Mode) Content() (string, any, error) {
	doc, err := m.store.Load()
	if err != nil {
		return "", nil, err
	}
	res, err := render.Render(doc)
	if err != nil {
		return "", nil, err
	}
	return "doc", contentData{Blocks: res.Blocks, PageComments: res.PageComments}, nil
}

type formData struct {
	Fields      map[string]string
	Placeholder string
}

// CommentForm implements server.Mode.
func (m *Mode) CommentForm(q url.Values) (string, any, error) {
	if q.Get("page") == "1" {
		return "comment-form", formData{
			Fields:      map[string]string{"page": "1"},
			Placeholder: "Comment on the whole document…",
		}, nil
	}
	block, err := strconv.Atoi(q.Get("block"))
	if err != nil {
		return "", nil, fmt.Errorf("bad block")
	}
	return "comment-form", formData{
		Fields:      map[string]string{"block": strconv.Itoa(block)},
		Placeholder: "Comment on this block…",
	}, nil
}

// Anchor implements server.Mode: block index → last body line of the block.
func (m *Mode) Anchor(v url.Values) (review.Anchor, error) {
	if v.Get("page") == "1" {
		return review.Anchor{Page: true}, nil
	}
	blockIdx, err := strconv.Atoi(v.Get("block"))
	if err != nil {
		return review.Anchor{}, fmt.Errorf("bad block")
	}
	doc, err := m.store.Load()
	if err != nil {
		return review.Anchor{}, err
	}
	res, err := render.Render(doc)
	if err != nil {
		return review.Anchor{}, err
	}
	if blockIdx < 0 || blockIdx >= len(res.Blocks) {
		return review.Anchor{Page: true}, nil
	}
	return review.Anchor{Line: res.Blocks[blockIdx].EndLine}, nil
}

// Watch implements server.Mode: it monitors the file's directory
// (editors save via rename, which breaks per-file watches) and fires
// debounced change events.
func (m *Mode) Watch(onChange func()) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	path := m.store.Path()
	if err := watcher.Add(filepath.Dir(path)); err != nil {
		watcher.Close()
		return err
	}
	name := filepath.Base(path)
	go func() {
		var timer *time.Timer
		for {
			select {
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Base(ev.Name) != name {
					continue
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(150*time.Millisecond, onChange)
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

// RegisterRoutes implements server.Mode.
func (m *Mode) RegisterRoutes(mux *http.ServeMux, _ *server.Server) {
	mux.HandleFunc("POST /editor", func(w http.ResponseWriter, r *http.Request) {
		if err := openInEditor(m.store.Path()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// Healthz implements server.Mode.
func (m *Mode) Healthz() map[string]any {
	return map[string]any{"file": m.store.Path()}
}

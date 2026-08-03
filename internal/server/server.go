// Package server implements the localhost review server for a single
// markdown file. It renders the document with block-level comment UI,
// persists comments back into the file, live-reloads on external edits,
// and signals handback when the reviewer is done.
package server

import (
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/olmesm/planman/internal/critic"
	"github.com/olmesm/planman/internal/render"
	"github.com/olmesm/planman/web"
)

// Server serves a single markdown file for review.
type Server struct {
	Path     string // absolute path to the markdown file
	Handback chan struct{}

	mu       sync.Mutex // guards file read-modify-write
	tmpl     *template.Template
	hub      *hub
	handOnce sync.Once
	host     string // populated once listening, for origin checks
}

// New creates a server for the given file.
func New(path string) (*Server, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", path, err)
	}
	funcs := template.FuncMap{
		"raw": func(s string) template.HTML { return template.HTML(s) },
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("Jan 2, 15:04")
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	s := &Server{
		Path:     abs,
		Handback: make(chan struct{}),
		tmpl:     tmpl,
		hub:      newHub(),
	}
	if err := s.watch(); err != nil {
		return nil, err
	}
	return s, nil
}

// Listen binds to 127.0.0.1 on the given port (0 for ephemeral) and
// returns the listener and the base URL.
func (s *Server) Listen(port int) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, "", err
	}
	s.host = ln.Addr().String()
	return ln, "http://" + s.host, nil
}

// Handler returns the HTTP handler with security middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePage)
	mux.HandleFunc("GET /doc", s.handleDocFragment)
	mux.HandleFunc("GET /comment-form", s.handleCommentForm)
	mux.HandleFunc("POST /comments", s.handleAddComment)
	mux.HandleFunc("POST /comments/{id}/reply", s.handleReply)
	mux.HandleFunc("DELETE /comments/{id}", s.handleDelete)
	mux.HandleFunc("POST /editor", s.handleEditor)
	mux.HandleFunc("POST /handback", s.handleHandback)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(web.AssetsFS())))
	return s.guard(mux)
}

// guard rejects requests that are not plausibly from our own local page:
// the Host must be our bound address and, for state-changing methods, any
// Origin header must match. This blocks DNS-rebinding and cross-site
// requests even though the server is unauthenticated.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host, s.host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || !localHost(u.Host, s.host) {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

// localHost reports whether host (possibly host:port) refers to this
// server's bound address on a loopback name.
func localHost(host, bound string) bool {
	if host == bound {
		return true
	}
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, ""
	}
	_, bp, err := net.SplitHostPort(bound)
	if err != nil {
		return false
	}
	if p != bp {
		return false
	}
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "[::1]"
}

// load reads and parses the current file contents.
func (s *Server) load() (*critic.Document, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	return critic.Parse(string(b)), nil
}

// save writes the document atomically (temp file + rename).
func (s *Server) save(doc *critic.Document) error {
	dir := filepath.Dir(s.Path)
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
	return os.Rename(tmp.Name(), s.Path)
}

// mutate runs fn against the parsed document and saves the result.
func (s *Server) mutate(fn func(*critic.Document) error) error {
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

// CommentCount returns the number of comment threads currently in the file.
func (s *Server) CommentCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.load()
	if err != nil {
		return 0
	}
	return len(doc.Comments)
}

func (s *Server) triggerHandback() {
	s.handOnce.Do(func() { close(s.Handback) })
}

var _ = render.EscapeForDisplay // keep linkage explicit

func fileTitle(path string) string {
	return filepath.Base(path)
}

func trimText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

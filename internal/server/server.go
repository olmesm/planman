// Package server is the localhost review server shell. It owns
// everything common to every review surface — listening, security
// middleware, templates, the SSE hub, comment-thread routes, the JSON
// agent API, and handback — and delegates content rendering and comment
// anchoring to a Mode (document review or diff review).
package server

import (
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/olmesm/planman/internal/highlight"
	"github.com/olmesm/planman/internal/review"
	"github.com/olmesm/planman/web"
)

// Mode is one review surface. The server shell calls it for content and
// anchors; the mode calls back into the hub when its source changes.
type Mode interface {
	// Name identifies the mode ("doc" or "diff") for templates and healthz.
	Name() string
	// Title is the human label shown in the top bar.
	Title() string
	// Store is the comment persistence backend.
	Store() review.Store
	// ApplyQuery folds view-affecting query parameters (scope, base,
	// view) into the mode's current state.
	ApplyQuery(q url.Values)
	// Content returns the content template name and its data.
	Content() (string, any, error)
	// CommentForm returns the comment-form template name and data for
	// the given request parameters.
	CommentForm(q url.Values) (string, any, error)
	// Anchor derives a comment anchor from posted form values.
	Anchor(v url.Values) (review.Anchor, error)
	// Watch starts change detection, invoking onChange on every change.
	Watch(onChange func()) error
	// RegisterRoutes adds mode-specific routes.
	RegisterRoutes(mux *http.ServeMux, s *Server)
	// Healthz returns mode-specific identity fields for /healthz.
	Healthz() map[string]any
}

// Server serves one review session.
type Server struct {
	Handback chan struct{}
	Stay     bool // hide handback and run until interrupted

	mode     Mode
	tmpl     *template.Template
	hub      *hub
	handOnce sync.Once
	host     string // populated once listening, for origin checks
}

// New creates a server around the given mode.
func New(mode Mode, stay bool) (*Server, error) {
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
		Handback: make(chan struct{}),
		Stay:     stay,
		mode:     mode,
		tmpl:     tmpl,
		hub:      newHub(),
	}
	if err := mode.Watch(s.hub.broadcast); err != nil {
		return nil, err
	}
	return s, nil
}

// Mode returns the server's mode.
func (s *Server) Mode() Mode { return s.mode }

// Broadcast tells connected pages to re-fetch content.
func (s *Server) Broadcast() { s.hub.broadcast() }

// CommentCounts returns (total, open) thread counts.
func (s *Server) CommentCounts() (int, int) {
	cs, err := s.mode.Store().List()
	if err != nil {
		return 0, 0
	}
	return len(cs), review.CountOpen(cs)
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

// ListenRange binds to the first free port in [from, to], for servers
// that agents discover by port scanning.
func (s *Server) ListenRange(from, to int) (net.Listener, string, error) {
	for port := from; port <= to; port++ {
		ln, url, err := s.Listen(port)
		if err == nil {
			return ln, url, nil
		}
	}
	return nil, "", fmt.Errorf("no free port in %d-%d", from, to)
}

// Handler returns the HTTP handler with security middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePage)
	mux.HandleFunc("GET /content", s.handleContent)
	mux.HandleFunc("GET /comment-form", s.handleCommentForm)
	mux.HandleFunc("POST /comments", s.handleAddComment)
	mux.HandleFunc("POST /comments/{id}/reply", s.handleReply)
	mux.HandleFunc("POST /comments/{id}/resolve", s.handleStatus(review.StatusResolved))
	mux.HandleFunc("POST /comments/{id}/reopen", s.handleStatus(review.StatusOpen))
	mux.HandleFunc("DELETE /comments/{id}", s.handleDelete)
	mux.HandleFunc("POST /handback", s.handleHandback)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/comments", s.handleAPIList)
	mux.HandleFunc("PATCH /api/comments/{id}", s.handleAPIPatch)
	mux.HandleFunc("POST /api/comments/{id}/reply", s.handleAPIReply)
	mux.HandleFunc("GET /assets/highlight.css", handleHighlightCSS)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(web.AssetsFS())))
	s.mode.RegisterRoutes(mux, s)
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

func handleHighlightCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(highlight.Stylesheet())
}

func (s *Server) triggerHandback() {
	s.handOnce.Do(func() { close(s.Handback) })
}

var errNotFound = errors.New("not found")

func httpStatusFor(err error) int {
	if errors.Is(err, review.ErrNotFound) || errors.Is(err, errNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func trimText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

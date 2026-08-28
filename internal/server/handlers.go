package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/olmesm/planman/internal/review"
)

type pageData struct {
	Title   string
	Mode    string
	Stay    bool
	Content template.HTML
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

// RenderTemplate lets modes render server templates from their own routes.
func (s *Server) RenderTemplate(w http.ResponseWriter, name string, data any) {
	s.renderTemplate(w, name, data)
}

// renderContent executes the mode's content template into a buffer.
func (s *Server) renderContent() (template.HTML, error) {
	name, data, err := s.mode.Content()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func (s *Server) writeContent(w http.ResponseWriter) {
	html, err := s.renderContent()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.mode.ApplyQuery(r.URL.Query())
	content, err := s.renderContent()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "page.html", pageData{
		Title:   s.mode.Title(),
		Mode:    s.mode.Name(),
		Stay:    s.Stay,
		Content: content,
	})
}

func (s *Server) handleContent(w http.ResponseWriter, r *http.Request) {
	s.mode.ApplyQuery(r.URL.Query())
	s.writeContent(w)
}

func (s *Server) handleCommentForm(w http.ResponseWriter, r *http.Request) {
	name, data, err := s.mode.CommentForm(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.renderTemplate(w, name, data)
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text := r.PostFormValue("text")
	if text == "" {
		http.Error(w, "empty comment", http.StatusBadRequest)
		return
	}
	anchor, err := s.mode.Anchor(r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.mode.Store().Add(anchor, r.PostFormValue("author"), text, time.Now()); err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	s.BroadcastComments()
	s.writeContent(w)
}

func (s *Server) handleReply(w http.ResponseWriter, r *http.Request) {
	text := r.PostFormValue("text")
	if text == "" {
		http.Error(w, "empty reply", http.StatusBadRequest)
		return
	}
	_, err := s.mode.Store().Reply(r.PathValue("id"), r.PostFormValue("author"), text, time.Now())
	if err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	s.BroadcastComments()
	s.writeContent(w)
}

func (s *Server) handleStatus(status review.Status) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := s.mode.Store().SetStatus(r.PathValue("id"), status); err != nil {
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		s.BroadcastComments()
		s.writeContent(w)
	}
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mode.Store().Delete(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	s.BroadcastComments()
	s.writeContent(w)
}

func (s *Server) handleHandback(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "handback", nil)
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.triggerHandback()
	}()
}

// --- JSON agent API ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{
		"app":  "planman",
		"mode": s.mode.Name(),
	}
	for k, v := range s.mode.Healthz() {
		payload[k] = v
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAPIList(w http.ResponseWriter, r *http.Request) {
	cs, err := s.mode.Store().List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if st := r.URL.Query().Get("status"); st != "" {
		want, err := review.ParseStatus(st)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		filtered := []*review.Comment{}
		for _, c := range cs {
			if c.Status == want {
				filtered = append(filtered, c)
			}
		}
		cs = filtered
	}
	if cs == nil {
		cs = []*review.Comment{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": cs})
}

func (s *Server) handleAPIPatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	status, err := review.ParseStatus(body.Status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := s.mode.Store().SetStatus(r.PathValue("id"), status)
	if err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	s.BroadcastComments()
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleAPIReply(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Author string `json:"author"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Text == "" {
		http.Error(w, "empty reply", http.StatusBadRequest)
		return
	}
	if body.Author == "" {
		body.Author = "agent"
	}
	c, err := s.mode.Store().Reply(r.PathValue("id"), body.Author, body.Text, time.Now())
	if err != nil {
		http.Error(w, err.Error(), httpStatusFor(err))
		return
	}
	s.BroadcastComments()
	writeJSON(w, http.StatusOK, c)
}

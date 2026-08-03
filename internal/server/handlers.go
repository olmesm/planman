package server

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/olmesm/planman/internal/critic"
	"github.com/olmesm/planman/internal/render"
)

type docData struct {
	File         string
	Blocks       []render.Block
	PageComments []*critic.Comment
}

func (s *Server) docData() (*docData, error) {
	s.mu.Lock()
	doc, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	res, err := render.Render(doc)
	if err != nil {
		return nil, err
	}
	return &docData{
		File:         fileTitle(s.Path),
		Blocks:       res.Blocks,
		PageComments: res.PageComments,
	}, nil
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := s.docData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "page.html", data)
}

func (s *Server) handleDocFragment(w http.ResponseWriter, r *http.Request) {
	data, err := s.docData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "doc", data)
}

type commentFormData struct {
	Block int  // -1 for page comments
	Page  bool
}

func (s *Server) handleCommentForm(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("page") == "1" {
		s.renderTemplate(w, "comment-form", commentFormData{Block: -1, Page: true})
		return
	}
	block, err := strconv.Atoi(r.URL.Query().Get("block"))
	if err != nil {
		http.Error(w, "bad block", http.StatusBadRequest)
		return
	}
	s.renderTemplate(w, "comment-form", commentFormData{Block: block})
}

func (s *Server) handleAddComment(w http.ResponseWriter, r *http.Request) {
	text := r.PostFormValue("text")
	author := r.PostFormValue("author")
	if text == "" {
		http.Error(w, "empty comment", http.StatusBadRequest)
		return
	}
	isPage := r.PostFormValue("page") == "1"
	var blockIdx int
	if !isPage {
		var err error
		blockIdx, err = strconv.Atoi(r.PostFormValue("block"))
		if err != nil {
			http.Error(w, "bad block", http.StatusBadRequest)
			return
		}
	}
	err := s.mutate(func(doc *critic.Document) error {
		if isPage {
			doc.AddPageComment(text, author, time.Now())
			return nil
		}
		res, err := render.Render(doc)
		if err != nil {
			return err
		}
		if blockIdx < 0 || blockIdx >= len(res.Blocks) {
			doc.AddPageComment(text, author, time.Now())
			return nil
		}
		doc.AddBlockComment(res.Blocks[blockIdx].EndLine, text, author, time.Now())
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleDocFragment(w, r)
}

func (s *Server) handleReply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	text := r.PostFormValue("text")
	if text == "" {
		http.Error(w, "empty reply", http.StatusBadRequest)
		return
	}
	err := s.mutate(func(doc *critic.Document) error {
		if !doc.AddReply(id, text, r.PostFormValue("author"), time.Now()) {
			return errNotFound
		}
		return nil
	})
	if err == errNotFound {
		http.Error(w, "no such comment", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleDocFragment(w, r)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.mutate(func(doc *critic.Document) error {
		if !doc.DeleteComment(id) {
			return errNotFound
		}
		return nil
	})
	if err == errNotFound {
		http.Error(w, "no such comment", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleDocFragment(w, r)
}

func (s *Server) handleEditor(w http.ResponseWriter, r *http.Request) {
	if err := openInEditor(s.Path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHandback(w http.ResponseWriter, r *http.Request) {
	s.renderTemplate(w, "handback", nil)
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.triggerHandback()
	}()
}

package server

import (
	"net/http"
	"sync"
	"time"
)

// hub fans out change notifications to connected SSE clients.
type hub struct {
	mu      sync.Mutex
	clients map[chan struct{}]bool
}

func newHub() *hub {
	return &hub{clients: map[chan struct{}]bool{}}
}

func (h *hub) subscribe() chan struct{} {
	ch := make(chan struct{}, 4)
	h.mu.Lock()
	h.clients[ch] = true
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan struct{}) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

func (h *hub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	_, _ = w.Write([]byte("event: hello\ndata: connected\n\n"))
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			_, _ = w.Write([]byte("event: change\ndata: source\n\n"))
			flusher.Flush()
		case <-keepalive.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		}
	}
}

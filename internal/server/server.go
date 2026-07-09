package server

import (
	"io"
	"log"
	"net/http"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
	"github.com/dobbo-ca/alert-thread-proxy/internal/parse"
)

type Handler interface {
	Handle(event.AlertEvent) error
}

type Server struct {
	h   Handler
	mux *http.ServeMux
}

func New(h Handler) *Server {
	s := &Server{h: h, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /webhook", s.webhook)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return s
}

func (s *Server) Mux() *http.ServeMux { return s.mux }

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	ev, err := parse.Event(body)
	if err != nil {
		log.Printf("bad payload: %v", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	// Ack fast; process off the request path so HyperDX doesn't retry-storm us.
	go func() {
		if err := s.h.Handle(ev); err != nil {
			log.Printf("handle %s: %v", ev.ThreadKey(), err)
		}
	}()
	w.WriteHeader(http.StatusOK)
}

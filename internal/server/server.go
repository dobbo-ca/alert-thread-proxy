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
	h      Handler
	mux    *http.ServeMux
	events chan event.AlertEvent
}

func New(h Handler) *Server {
	s := &Server{h: h, mux: http.NewServeMux(), events: make(chan event.AlertEvent, 1024)}
	s.mux.HandleFunc("POST /webhook", s.webhook)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	go s.worker()
	return s
}

func (s *Server) Mux() *http.ServeMux { return s.mux }

// worker processes events on a single goroutine, preserving submission order
// and bounding concurrency against the state machine.
func (s *Server) worker() {
	for ev := range s.events {
		if err := s.h.Handle(ev); err != nil {
			log.Printf("handle %s: %v", ev.ThreadKey(), err)
		}
	}
}

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
	// Ack fast; enqueue for the single worker goroutine so HyperDX doesn't
	// retry-storm us, and never block ingest under backpressure.
	select {
	case s.events <- ev:
	default:
		log.Printf("ingest queue full, dropping %s", ev.ThreadKey())
	}
	w.WriteHeader(http.StatusOK)
}

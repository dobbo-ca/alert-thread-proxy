package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

type capture struct {
	mu  sync.Mutex
	evs []event.AlertEvent
}

func (c *capture) Handle(e event.AlertEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evs = append(c.evs, e)
	return nil
}

func TestWebhookAcksAndDispatches(t *testing.T) {
	c := &capture{}
	s := New(c)
	body := `{"eventId":"a1","state":"ALERT","title":"t","startTime":1783605600000}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	s.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// dispatch is async; wait briefly
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.evs)
		c.mu.Unlock()
		if n == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("event not dispatched")
}

func TestWebhookBadPayload(t *testing.T) {
	s := New(&capture{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("not json"))
	s.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	s := New(&capture{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Mux().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

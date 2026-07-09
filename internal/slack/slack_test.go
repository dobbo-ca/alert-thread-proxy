package slack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIPostMessage(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"ts":"1720540800.000100"}`))
	}))
	defer srv.Close()

	api := New("xoxb-abc", "C123")
	api.baseURL = srv.URL // test hook

	ts, err := api.PostMessage("hello", "")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if ts != "1720540800.000100" {
		t.Fatalf("ts = %q", ts)
	}
	if gotAuth != "Bearer xoxb-abc" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"channel":"C123"`) || !strings.Contains(gotBody, `"text":"hello"`) {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestAPIErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
	}))
	defer srv.Close()
	api := New("xoxb-abc", "C123")
	api.baseURL = srv.URL
	if _, err := api.PostMessage("x", ""); err == nil {
		t.Fatal("expected error on ok:false")
	}
}

func TestWithRetrySucceedsAfterFailures(t *testing.T) {
	f := &Fake{}
	f.FailNext = true // first post fails, second succeeds
	var slept int
	rc := WithRetry(f, 3, time.Millisecond, func(time.Duration) { slept++ })
	if _, err := rc.PostMessage("x", ""); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if slept != 1 {
		t.Fatalf("expected 1 backoff sleep, got %d", slept)
	}
}

package parse

import (
	"testing"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

func TestEventFiring(t *testing.T) {
	body := []byte(`{
		"eventId": "3f9c2b1a7e4d5f60918273645bdcafe0",
		"state": "ALERT",
		"title": "🚨 Alert for \"prod-5xx-errors\" - 42 lines found",
		"body": "Group: \"api-gateway\"\n42 lines found, which meets or exceeds the threshold of 10 lines\nTime Range (UTC): [2026-07-09 14:00:00 - 2026-07-09 14:05:00)",
		"link": "https://hyperdx.dobbo.ca/search/64f1a2b3c4d5e6f708192a3b?from=1783605600000&to=1783605900000&isLive=false",
		"startTime": 1783605600000,
		"endTime": 1783605900000
	}`)

	ev, err := Event(body)
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if ev.AlertID != "3f9c2b1a7e4d5f60918273645bdcafe0" {
		t.Errorf("AlertID = %q", ev.AlertID)
	}
	if ev.GroupKey != "" {
		t.Errorf("GroupKey = %q, want empty", ev.GroupKey)
	}
	if ev.State != event.StateFiring {
		t.Errorf("State = %q, want %q", ev.State, event.StateFiring)
	}
	if ev.Title != "🚨 Alert for \"prod-5xx-errors\" - 42 lines found" {
		t.Errorf("Title = %q", ev.Title)
	}
	if ev.Link != "https://hyperdx.dobbo.ca/search/64f1a2b3c4d5e6f708192a3b?from=1783605600000&to=1783605900000&isLive=false" {
		t.Errorf("Link = %q", ev.Link)
	}
	wantFiredAt := time.UnixMilli(1783605600000)
	if !ev.FiredAt.Equal(wantFiredAt) {
		t.Errorf("FiredAt = %v, want %v", ev.FiredAt, wantFiredAt)
	}
	if ev.ThreadKey() != "3f9c2b1a7e4d5f60918273645bdcafe0" {
		t.Errorf("ThreadKey = %q", ev.ThreadKey())
	}
}

func TestEventResolved(t *testing.T) {
	body := []byte(`{
		"eventId": "3f9c2b1a7e4d5f60918273645bdcafe0",
		"state": "OK",
		"title": "✅ Alert for \"prod-5xx-errors\" resolved",
		"link": "https://hyperdx.dobbo.ca/search/64f1a2b3c4d5e6f708192a3b?from=1783605600000&to=1783605900000&isLive=false",
		"startTime": 1783605900000,
		"endTime": 1783606200000
	}`)

	ev, err := Event(body)
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if ev.State != event.StateResolved {
		t.Errorf("State = %q, want %q", ev.State, event.StateResolved)
	}
}

func TestEventBadJSON(t *testing.T) {
	if _, err := Event([]byte(`not json`)); err == nil {
		t.Fatal("expected error on bad json")
	}
}

func TestEventMissingEventID(t *testing.T) {
	body := []byte(`{"state":"ALERT","title":"t"}`)
	if _, err := Event(body); err == nil {
		t.Fatal("expected error on missing eventId")
	}
}

func TestEventUnknownState(t *testing.T) {
	body := []byte(`{"eventId":"a1","state":"PENDING","title":"t"}`)
	if _, err := Event(body); err == nil {
		t.Fatal("expected error on unknown state")
	}
}

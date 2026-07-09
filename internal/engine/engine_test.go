package engine

import (
	"testing"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/config"
	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
	"github.com/dobbo-ca/alert-thread-proxy/internal/slack"
)

func testCfg() config.Config {
	return config.Config{
		SlackChannelID: "C1", HyperDXBaseURL: "https://h",
		StormThreshold: 10, StormWindow: 60 * time.Second,
		ReminderInterval: 30 * time.Minute, ThreadMaxAge: 24 * time.Hour,
	}
}

func firing(id, group string) event.AlertEvent {
	return event.AlertEvent{AlertID: id, GroupKey: group, State: event.StateFiring, Title: id}
}
func resolved(id, group string) event.AlertEvent {
	return event.AlertEvent{AlertID: id, GroupKey: group, State: event.StateResolved, Title: id}
}

func TestFiringPostsParent(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })
	if err := e.Handle(firing("a1", "lakshmi")); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 1 || f.Calls[0].Method != "post" || f.Calls[0].ThreadTS != "" {
		t.Fatalf("calls = %+v", f.Calls)
	}
}

func TestRefireThrottlesReminder(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })
	e.Handle(firing("a1", "x"))     // parent
	clk = clk.Add(5 * time.Minute)  // within ReminderInterval
	e.Handle(firing("a1", "x"))     // swallowed
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call (throttled), got %+v", f.Calls)
	}
	clk = clk.Add(30 * time.Minute) // now past ReminderInterval
	e.Handle(firing("a1", "x"))     // reminder reply
	if len(f.Calls) != 2 || f.Calls[1].ThreadTS == "" {
		t.Fatalf("expected threaded reminder, got %+v", f.Calls)
	}
}

func TestResolveRepliesAndCloses(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })
	e.Handle(firing("a1", "x"))
	parentTS := f.Calls[0]
	_ = parentTS
	e.Handle(resolved("a1", "x"))
	if len(f.Calls) != 2 || f.Calls[1].Method != "post" || f.Calls[1].ThreadTS == "" {
		t.Fatalf("expected threaded resolve reply, got %+v", f.Calls)
	}
	// after resolve, next firing is a NEW parent (thread closed)
	e.Handle(firing("a1", "x"))
	if len(f.Calls) != 3 || f.Calls[2].ThreadTS != "" {
		t.Fatalf("expected new parent after resolve, got %+v", f.Calls)
	}
}

func TestSweepClosesStaleIncident(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })
	e.Handle(firing("a1", "x"))
	clk = clk.Add(25 * time.Hour) // older than ThreadMaxAge
	e.Sweep()
	e.Handle(firing("a1", "x")) // should be a new parent, not a reminder
	if len(f.Calls) != 2 || f.Calls[1].ThreadTS != "" {
		t.Fatalf("expected new parent after sweep, got %+v", f.Calls)
	}
}

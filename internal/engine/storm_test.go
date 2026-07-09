package engine

import (
	"testing"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/slack"
)

func TestStormDigest(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })

	// 9 distinct firings: below threshold (10) -> 9 separate parents
	for i := 0; i < 9; i++ {
		e.Handle(firing(itoa(i), ""))
	}
	if len(f.Calls) != 9 {
		t.Fatalf("expected 9 parents pre-storm, got %d", len(f.Calls))
	}
	// 10th distinct firing within the window trips storm mode: enterStorm
	// posts the storm parent (top-level), then openStormIncident posts the
	// tripping firing as a reply AND updates the parent's count.
	n := len(f.Calls) // == 9 here
	e.Handle(firing("s10", ""))
	stormParent := f.Calls[n] // storm parent: top-level post
	if stormParent.Method != "post" || stormParent.ThreadTS != "" {
		t.Fatalf("expected top-level storm parent, got %+v", stormParent)
	}
	reply := f.Calls[n+1] // tripping firing routed as a reply
	if reply.Method != "post" || reply.ThreadTS == "" {
		t.Fatalf("expected tripping firing routed under storm parent, got %+v", reply)
	}
	// a further new firing during storm also routes as a reply (not a new parent)
	before := len(f.Calls)
	e.Handle(firing("s11", ""))
	if f.Calls[before].ThreadTS == "" {
		t.Fatalf("expected storm reply, got %+v", f.Calls[before])
	}
}

func TestStormExpiresAfterWindow(t *testing.T) {
	f := &slack.Fake{}
	clk := time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC)
	e := New(testCfg(), f, func() time.Time { return clk })
	for i := 0; i < 10; i++ {
		e.Handle(firing(itoa(i), "")) // trips storm
	}
	clk = clk.Add(2 * time.Minute) // past StormWindow with no new firings
	e.Sweep()                      // expires storm
	before := len(f.Calls)
	e.Handle(firing("later", "")) // should be a normal top-level parent again
	if f.Calls[before].ThreadTS != "" {
		t.Fatalf("expected normal parent after storm expiry, got %+v", f.Calls[before])
	}
}

func itoa(i int) string { return "id" + string(rune('0'+i)) }

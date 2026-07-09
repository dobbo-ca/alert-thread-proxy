package deeplink

import (
	"strings"
	"testing"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

func TestBuildPrefersEventLink(t *testing.T) {
	e := event.AlertEvent{AlertID: "a1", Link: "https://hyperdx/x"}
	if got := Build("https://base", e); got != "https://hyperdx/x" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildConstructs(t *testing.T) {
	e := event.AlertEvent{AlertID: "a1"}
	got := Build("https://hyperdx.tailde247c.ts.net", e)
	if !strings.HasPrefix(got, "https://hyperdx.tailde247c.ts.net") || !strings.Contains(got, "a1") {
		t.Fatalf("got %q", got)
	}
}

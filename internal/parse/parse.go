// Package parse converts HyperDX's generic-webhook body into the stable
// internal event.AlertEvent. hyperdxPayload and mapState are the single place
// the HyperDX wire format lives; every other package consumes event.AlertEvent.
package parse

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

// hyperdxPayload mirrors the JSON body HyperDX's generic-webhook sends, per
// docs/hyperdx-webhook-sample.json / docs/hyperdx-webhook-notes.md.
type hyperdxPayload struct {
	EventID   string `json:"eventId"`
	State     string `json:"state"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}

// Event decodes a HyperDX webhook body into an event.AlertEvent.
func Event(body []byte) (event.AlertEvent, error) {
	var p hyperdxPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return event.AlertEvent{}, fmt.Errorf("decode payload: %w", err)
	}
	if p.EventID == "" {
		return event.AlertEvent{}, fmt.Errorf("payload missing eventId")
	}
	state, err := mapState(p.State)
	if err != nil {
		return event.AlertEvent{}, err
	}
	return event.AlertEvent{
		AlertID:  p.EventID,
		GroupKey: "",
		State:    state,
		Title:    p.Title,
		Link:     p.Link,
		FiredAt:  time.UnixMilli(p.StartTime), // zero time is acceptable
	}, nil
}

// mapState maps HyperDX's state strings to our states. Only ALERT (firing)
// and OK (resolved) are ever sent to a webhook (confirmed against HyperDX
// source); anything else is an error.
func mapState(s string) (event.AlertState, error) {
	switch s {
	case "ALERT":
		return event.StateFiring, nil
	case "OK":
		return event.StateResolved, nil
	default:
		return "", fmt.Errorf("unknown state %q", s)
	}
}

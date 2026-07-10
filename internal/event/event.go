package event

import "time"

type AlertState string

const (
	StateFiring   AlertState = "firing"
	StateResolved AlertState = "resolved"
)

// AlertEvent is the stable internal representation. Only internal/parse knows
// HyperDX's wire format; every other package consumes AlertEvent.
type AlertEvent struct {
	AlertID  string
	GroupKey string
	State    AlertState
	Title    string
	Link     string
	FiredAt  time.Time
}

// ThreadKey identifies one incident: the alert plus its triggering group.
func (e AlertEvent) ThreadKey() string {
	if e.GroupKey == "" {
		return e.AlertID
	}
	return e.AlertID + ":" + e.GroupKey
}

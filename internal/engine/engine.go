package engine

import (
	"fmt"
	"sync"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/config"
	"github.com/dobbo-ca/alert-thread-proxy/internal/deeplink"
	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
	"github.com/dobbo-ca/alert-thread-proxy/internal/slack"
)

type Incident struct {
	TS           string
	FirstSeen    time.Time
	LastReminder time.Time
	Refires      int
	InStorm      bool
}

type Engine struct {
	mu    sync.Mutex
	cfg   config.Config
	slack slack.Client
	now   func() time.Time

	incidents map[string]*Incident

	// storm state (populated in Task 7)
	stormWindow []time.Time
	storm       *stormState
}

type stormState struct {
	TS      string
	Count   int
	Expires time.Time
}

func New(cfg config.Config, sc slack.Client, now func() time.Time) *Engine {
	return &Engine{
		cfg: cfg, slack: sc, now: now,
		incidents: make(map[string]*Incident),
	}
}

func (e *Engine) Handle(ev event.AlertEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch ev.State {
	case event.StateFiring:
		return e.handleFiring(ev)
	case event.StateResolved:
		return e.handleResolved(ev)
	default:
		return fmt.Errorf("unhandled state %q", ev.State)
	}
}

func (e *Engine) handleFiring(ev event.AlertEvent) error {
	key := ev.ThreadKey()
	if inc := e.incidents[key]; inc != nil {
		inc.Refires++
		if e.now().Sub(inc.LastReminder) >= e.cfg.ReminderInterval {
			if _, err := e.slack.PostMessage(e.reminderText(ev, inc), inc.TS); err != nil {
				return err
			}
			inc.LastReminder = e.now()
		}
		return nil
	}
	// new incident (Task 7 inserts storm handling ahead of this line)
	return e.openNormalIncident(ev, key)
}

func (e *Engine) openNormalIncident(ev event.AlertEvent, key string) error {
	ts, err := e.slack.PostMessage(e.firingText(ev), "")
	if err != nil {
		return err
	}
	now := e.now()
	e.incidents[key] = &Incident{TS: ts, FirstSeen: now, LastReminder: now}
	return nil
}

func (e *Engine) handleResolved(ev event.AlertEvent) error {
	key := ev.ThreadKey()
	inc := e.incidents[key]
	if inc == nil {
		return nil // resolve for an unknown/closed incident
	}
	if _, err := e.slack.PostMessage(e.resolvedText(ev), inc.TS); err != nil {
		return err
	}
	delete(e.incidents, key)
	return nil
}

// Sweep closes incidents older than ThreadMaxAge and expires the storm window.
func (e *Engine) Sweep() {
	e.mu.Lock()
	defer e.mu.Unlock()
	cutoff := e.now().Add(-e.cfg.ThreadMaxAge)
	for k, inc := range e.incidents {
		if inc.FirstSeen.Before(cutoff) {
			delete(e.incidents, k)
		}
	}
	if e.storm != nil && e.now().After(e.storm.Expires) {
		e.storm = nil
	}
}

func (e *Engine) firingText(ev event.AlertEvent) string {
	return fmt.Sprintf("%s\n🔗 %s", ev.Title, deeplink.Build(e.cfg.HyperDXBaseURL, ev))
}
func (e *Engine) reminderText(ev event.AlertEvent, inc *Incident) string {
	return fmt.Sprintf("still firing (×%d)", inc.Refires+1)
}
func (e *Engine) resolvedText(ev event.AlertEvent) string {
	return fmt.Sprintf("✅ resolved — %s", ev.Title)
}

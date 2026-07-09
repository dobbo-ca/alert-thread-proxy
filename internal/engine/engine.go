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
	// new incident: record it in the storm window and decide routing.
	e.recordNewFiring()
	if e.stormActive() {
		return e.openStormIncident(ev, key)
	}
	if len(e.stormWindow) >= e.cfg.StormThreshold {
		return e.enterStorm(ev, key)
	}
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

func (e *Engine) recordNewFiring() {
	now := e.now()
	cutoff := now.Add(-e.cfg.StormWindow)
	kept := e.stormWindow[:0]
	for _, t := range e.stormWindow {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.stormWindow = append(kept, now)
}

func (e *Engine) stormActive() bool {
	return e.storm != nil && !e.now().After(e.storm.Expires)
}

func (e *Engine) enterStorm(ev event.AlertEvent, key string) error {
	ts, err := e.slack.PostMessage(e.stormText(len(e.stormWindow)), "")
	if err != nil {
		return err
	}
	e.storm = &stormState{TS: ts, Count: 0, Expires: e.now().Add(e.cfg.StormWindow)}
	return e.openStormIncident(ev, key)
}

func (e *Engine) openStormIncident(ev event.AlertEvent, key string) error {
	if _, err := e.slack.PostMessage(e.firingText(ev), e.storm.TS); err != nil {
		return err
	}
	e.storm.Count++
	e.storm.Expires = e.now().Add(e.cfg.StormWindow)
	if err := e.slack.UpdateMessage(e.storm.TS, e.stormText(e.storm.Count)); err != nil {
		return err
	}
	now := e.now()
	e.incidents[key] = &Incident{TS: e.storm.TS, FirstSeen: now, LastReminder: now, InStorm: true}
	return nil
}

func (e *Engine) stormText(n int) string {
	return fmt.Sprintf("⚠️ *%d alerts firing* (storm)", n)
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

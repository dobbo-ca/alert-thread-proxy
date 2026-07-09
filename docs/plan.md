# Alert Thread Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a small Go service that turns HyperDX's fire-and-forget alert webhooks into threaded, de-noised Slack notifications (per-incident threads, flap throttle, storm digest, HyperDX deep-links).

**Architecture:** HyperDX generic webhook → `POST /webhook` on the proxy → parse to a stable internal `AlertEvent` → a mutex-guarded in-memory `Engine` state machine posts/updates Slack via the Web API (bot token). Single replica; state is in-memory. The Go service lives in repo **`dobbo-ca/containers`** under `alert-thread-proxy/`; the Kubernetes manifests live in repo **`k8s-dobbolab`** under `gitops/apps/`.

**Tech Stack:** Go (stdlib `net/http`, `encoding/json`, `testing`; no third-party deps required — Slack is called over raw HTTPS). Docker (distroless/scratch). FluxCD + External Secrets Operator for deploy.

## Global Constraints

- **Single replica only.** In-memory state owns thread mappings; two replicas double-post. Deployment `replicas: 1`, `strategy: Recreate`.
- **No third-party Go dependencies** unless a task explicitly adds one. Slack Web API is called with `net/http`. Keeps the image tiny and the build hermetic.
- **Stable internal type is `AlertEvent`;** only the parser knows HyperDX's wire format. All other packages consume `AlertEvent`.
- **Config via env vars**, defaults: `STORM_THRESHOLD=10`, `STORM_WINDOW=60s`, `REMINDER_INTERVAL=30m`, `THREAD_MAX_AGE=24h`, `LISTEN_ADDR=:8080`.
- **Clock is injected** (`now func() time.Time`) everywhere time is used, so tests are deterministic. Never call `time.Now()` directly inside `Engine`.
- **Image:** `ghcr.io/dobbo-ca/alert-thread-proxy`. **Namespace:** `clickstack`. **Slack token** from SSM `/dobbolab/eso/alert-thread-proxy/slack-bot-token` via ESO.
- Go module path: `github.com/dobbo-ca/alert-thread-proxy`. Package layout: `internal/…`, entrypoint `main.go`.
- Spec: `docs/superpowers/specs/2026-07-09-alert-thread-proxy-design.md` (in `k8s-dobbolab`).

---

> **REPO PATH OVERRIDES (standalone repo).** This plan was written assuming the
> service lived in a `dobbo-ca/containers/alert-thread-proxy/` subdirectory. In
> THIS standalone repo, apply these mappings as you execute:
> - **Files live at the repo ROOT.** Ignore the leading `alert-thread-proxy/` in
>   every file path: `alert-thread-proxy/internal/event/event.go` →
>   `internal/event/event.go`, `alert-thread-proxy/main.go` → `main.go`,
>   `alert-thread-proxy/Dockerfile` → `Dockerfile`, etc. Any `cd alert-thread-proxy`
>   step is a no-op — you are already at the repo root.
> - **Module & imports** are already updated to `github.com/dobbo-ca/alert-thread-proxy`.
> - **Image** is already updated to `ghcr.io/dobbo-ca/alert-thread-proxy`.
> - **Spec** is at `docs/design.md` in this repo (not the k8s-dobbolab path in Global Constraints).
> - **Tasks 10–12** (ESO secret, Deployment/Service, HyperDX wiring) land in the
>   `k8s-dobbolab` repo and need cluster + HyperDX access — do them there, not here.


### Task 1: Pin the HyperDX webhook contract + scaffold the Go module

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/go.mod`
- Create: `alert-thread-proxy/docs/hyperdx-webhook-sample.json`
- Create: `alert-thread-proxy/docs/hyperdx-webhook-notes.md`

**Interfaces:**
- Produces: a confirmed, concrete HyperDX generic-webhook payload sample and the HyperDX deep-link URL format, both used by Tasks 3 and 5.

- [ ] **Step 1: Confirm the payload shape.** Do ONE of:
  (a) Read the HyperDX source (`github.com/hyperdxio/hyperdx`, search for the generic/webhook alert channel send path) and copy the exact JSON body it POSTs; or
  (b) Capture a live sample: create a HyperDX generic-webhook channel pointing at a temporary catch endpoint (e.g. `kubectl -n clickstack run catch --image=mendhak/http-https-echo --port=8080` + a Service, point the channel at it, trigger a test alert), then read the pod logs.

- [ ] **Step 2: Record the sample.** Save the real JSON body to `docs/hyperdx-webhook-sample.json`. In `docs/hyperdx-webhook-notes.md`, write down: the field carrying the alert identity, the field carrying firing-vs-resolved state and its exact string values, any grouping field, title, severity, timestamp, and the URL/deep-link field if present. Also record the HyperDX UI URL format for a saved-search/alert view filtered by query + time (used to build deep-links).

- [ ] **Step 3: Init the module.**

Run: `cd alert-thread-proxy && go mod init github.com/dobbo-ca/alert-thread-proxy && go mod tidy`
Expected: `go.mod` created with `go 1.23` (or the repo's Go version).

- [ ] **Step 4: Commit.**

```bash
git add alert-thread-proxy/go.mod alert-thread-proxy/docs/
git commit -m "chore(alert-thread-proxy): scaffold module + pin HyperDX webhook contract"
```

> **If Step 2 reveals field names different from the assumed struct in Task 3, adjust `internal/parse/parse.go`'s `hyperdxPayload` struct tags to match — that struct is the only place the wire format lives.**

---

### Task 2: Internal types + config

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/event/event.go`
- Create: `alert-thread-proxy/internal/config/config.go`
- Test: `alert-thread-proxy/internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `event.AlertEvent{AlertID, GroupKey string; State AlertState; Title, Severity, Link string; FiredAt time.Time}` with `AlertState` const `StateFiring="firing"`, `StateResolved="resolved"`, and method `func (e AlertEvent) ThreadKey() string`.
  - `config.Config{SlackBotToken, SlackChannelID, HyperDXBaseURL string; StormThreshold int; StormWindow, ReminderInterval, ThreadMaxAge time.Duration; ListenAddr string}` and `func Load() (Config, error)`.

- [ ] **Step 1: Write `event.go`.**

```go
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
	Severity string
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
```

- [ ] **Step 2: Write the failing config test.**

```go
package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_CHANNEL_ID", "C123")
	t.Setenv("HYPERDX_BASE_URL", "https://hyperdx.example.com")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.StormThreshold != 10 || c.StormWindow != 60*time.Second ||
		c.ReminderInterval != 30*time.Minute || c.ThreadMaxAge != 24*time.Hour ||
		c.ListenAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when SLACK_BOT_TOKEN missing")
	}
}
```

- [ ] **Step 2b: Run to verify it fails.**

Run: `go test ./internal/config/... -run TestLoad`
Expected: FAIL (`undefined: Load`).

- [ ] **Step 3: Write `config.go`.**

```go
package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	SlackBotToken    string
	SlackChannelID   string
	HyperDXBaseURL   string
	StormThreshold   int
	StormWindow      time.Duration
	ReminderInterval time.Duration
	ThreadMaxAge     time.Duration
	ListenAddr       string
}

func Load() (Config, error) {
	c := Config{
		SlackBotToken:    os.Getenv("SLACK_BOT_TOKEN"),
		SlackChannelID:   os.Getenv("SLACK_CHANNEL_ID"),
		HyperDXBaseURL:   os.Getenv("HYPERDX_BASE_URL"),
		StormThreshold:   envInt("STORM_THRESHOLD", 10),
		StormWindow:      envDur("STORM_WINDOW", 60*time.Second),
		ReminderInterval: envDur("REMINDER_INTERVAL", 30*time.Minute),
		ThreadMaxAge:     envDur("THREAD_MAX_AGE", 24*time.Hour),
		ListenAddr:       envStr("LISTEN_ADDR", ":8080"),
	}
	if c.SlackBotToken == "" || c.SlackChannelID == "" {
		return Config{}, fmt.Errorf("SLACK_BOT_TOKEN and SLACK_CHANNEL_ID are required")
	}
	return c, nil
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/config/... ./internal/event/...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add alert-thread-proxy/internal/event alert-thread-proxy/internal/config
git commit -m "feat(alert-thread-proxy): internal AlertEvent + env config"
```

---

### Task 3: HyperDX payload parser

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/parse/parse.go`
- Test: `alert-thread-proxy/internal/parse/parse_test.go`

**Interfaces:**
- Consumes: `event.AlertEvent`, `event.StateFiring/StateResolved`.
- Produces: `func parse.Event(body []byte) (event.AlertEvent, error)`.

> Struct tags below assume the fields confirmed in Task 1. **If Task 1's sample differs, change only the `hyperdxPayload` struct tags and the state-mapping switch here.**

- [ ] **Step 1: Write the failing test** (use the real sample from Task 1 if it differs from this synthetic one).

```go
package parse

import (
	"testing"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

func TestParseFiring(t *testing.T) {
	body := []byte(`{
		"alertId":"a1","title":"HighErrorRate","status":"firing",
		"group":"lakshmi","severity":"critical",
		"url":"https://hyperdx/search?alert=a1","timestamp":"2026-07-09T16:00:00Z"
	}`)
	ev, err := Event(body)
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if ev.AlertID != "a1" || ev.GroupKey != "lakshmi" ||
		ev.State != event.StateFiring || ev.Title != "HighErrorRate" ||
		ev.Severity != "critical" || ev.Link != "https://hyperdx/search?alert=a1" {
		t.Fatalf("unexpected: %+v", ev)
	}
	if ev.ThreadKey() != "a1:lakshmi" {
		t.Fatalf("ThreadKey = %q", ev.ThreadKey())
	}
}

func TestParseResolvedAndBad(t *testing.T) {
	ev, err := Event([]byte(`{"alertId":"a1","status":"resolved","timestamp":"2026-07-09T16:05:00Z"}`))
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if ev.State != event.StateResolved {
		t.Fatalf("state = %q", ev.State)
	}
	if _, err := Event([]byte(`not json`)); err == nil {
		t.Fatal("expected error on bad json")
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `go test ./internal/parse/...`
Expected: FAIL (`undefined: Event`).

- [ ] **Step 3: Write `parse.go`.**

```go
package parse

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

// hyperdxPayload mirrors HyperDX's generic-webhook body. CONFIRM field names
// against docs/hyperdx-webhook-sample.json (Task 1); this struct is the only
// place the wire format lives.
type hyperdxPayload struct {
	AlertID   string `json:"alertId"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	Group     string `json:"group"`
	Severity  string `json:"severity"`
	URL       string `json:"url"`
	Timestamp string `json:"timestamp"`
}

func Event(body []byte) (event.AlertEvent, error) {
	var p hyperdxPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return event.AlertEvent{}, fmt.Errorf("decode payload: %w", err)
	}
	if p.AlertID == "" {
		return event.AlertEvent{}, fmt.Errorf("payload missing alertId")
	}
	state, err := mapState(p.Status)
	if err != nil {
		return event.AlertEvent{}, err
	}
	firedAt, _ := time.Parse(time.RFC3339, p.Timestamp) // zero time is acceptable
	return event.AlertEvent{
		AlertID:  p.AlertID,
		GroupKey: p.Group,
		State:    state,
		Title:    p.Title,
		Severity: p.Severity,
		Link:     p.URL,
		FiredAt:  firedAt,
	}, nil
}

// mapState maps HyperDX's status strings to our states. Adjust the cases if
// Task 1 shows different values (e.g. "alert"/"ok").
func mapState(s string) (event.AlertState, error) {
	switch s {
	case "firing", "alert", "triggered":
		return event.StateFiring, nil
	case "resolved", "ok", "recovered":
		return event.StateResolved, nil
	default:
		return "", fmt.Errorf("unknown status %q", s)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/parse/...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add alert-thread-proxy/internal/parse
git commit -m "feat(alert-thread-proxy): HyperDX webhook payload parser"
```

---

### Task 4: Deep-link builder

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/deeplink/deeplink.go`
- Test: `alert-thread-proxy/internal/deeplink/deeplink_test.go`

**Interfaces:**
- Consumes: `event.AlertEvent`.
- Produces: `func deeplink.Build(baseURL string, e event.AlertEvent) string`.

> The URL scheme below is a reasonable default; **replace with the confirmed HyperDX URL format from Task 1** if different. Behavior: if the event already carries a `Link`, prefer it; else construct one.

- [ ] **Step 1: Write the failing test.**

```go
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
	got := Build("https://hyperdx.example.com", e)
	if !strings.HasPrefix(got, "https://hyperdx.example.com") || !strings.Contains(got, "a1") {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `go test ./internal/deeplink/...`
Expected: FAIL (`undefined: Build`).

- [ ] **Step 3: Write `deeplink.go`.**

```go
package deeplink

import (
	"net/url"
	"strings"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

// Build returns a URL back to HyperDX for this alert. Prefer the link HyperDX
// already put in the payload; otherwise construct one from the base URL.
// Confirm the constructed path/query against Task 1's URL-format note.
func Build(baseURL string, e event.AlertEvent) string {
	if e.Link != "" {
		return e.Link
	}
	base := strings.TrimRight(baseURL, "/")
	q := url.Values{}
	q.Set("alertId", e.AlertID)
	return base + "/alerts?" + q.Encode()
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/deeplink/...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add alert-thread-proxy/internal/deeplink
git commit -m "feat(alert-thread-proxy): HyperDX deep-link builder"
```

---

### Task 5: Slack client (interface + Web API impl + fake)

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/slack/slack.go`
- Create: `alert-thread-proxy/internal/slack/fake.go`
- Test: `alert-thread-proxy/internal/slack/slack_test.go`

**Interfaces:**
- Produces:
  - `type slack.Client interface { PostMessage(text, threadTS string) (ts string, err error); UpdateMessage(ts, text string) error }`
  - `func slack.New(token, channelID string) *slack.API` implementing `Client` over the Slack Web API.
  - `type slack.Fake` implementing `Client`, recording calls, for use by Task 6/7/8 tests.

- [ ] **Step 1: Write `fake.go`** (test double used by later tasks).

```go
package slack

import "sync"

type Call struct {
	Method   string // "post" or "update"
	Text     string
	ThreadTS string // for post
	TS       string // for update
}

type Fake struct {
	mu      sync.Mutex
	Calls   []Call
	nextTS  int
	FailNext bool
}

func (f *Fake) PostMessage(text, threadTS string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNext {
		f.FailNext = false
		return "", errFake
	}
	f.nextTS++
	ts := tsString(f.nextTS)
	f.Calls = append(f.Calls, Call{Method: "post", Text: text, ThreadTS: threadTS})
	return ts, nil
}

func (f *Fake) UpdateMessage(ts, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Method: "update", TS: ts, Text: text})
	return nil
}
```

- [ ] **Step 2: Write the failing test** for the real API's request shaping (test what we can without hitting Slack: URL, auth header, JSON body, ts extraction from a stubbed HTTP response).

```go
package slack

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIPostMessage(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
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
```

- [ ] **Step 3: Run to verify it fails.**

Run: `go test ./internal/slack/...`
Expected: FAIL (`undefined: New` / `errFake` / helpers).

- [ ] **Step 4: Write `slack.go`.**

```go
package slack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

var errFake = errors.New("fake slack failure")

func tsString(n int) string { return "100." + strconv.Itoa(n) }

type Client interface {
	PostMessage(text, threadTS string) (ts string, err error)
	UpdateMessage(ts, text string) error
}

type API struct {
	token   string
	channel string
	http    *http.Client
	baseURL string // Slack API base; overridable in tests
}

func New(token, channelID string) *API {
	return &API{
		token:   token,
		channel: channelID,
		http:    &http.Client{Timeout: 10 * time.Second},
		baseURL: "https://slack.com/api",
	}
}

func (a *API) PostMessage(text, threadTS string) (string, error) {
	payload := map[string]any{"channel": a.channel, "text": text}
	if threadTS != "" {
		payload["thread_ts"] = threadTS
	}
	var out struct {
		OK    bool   `json:"ok"`
		TS    string `json:"ts"`
		Error string `json:"error"`
	}
	if err := a.call("chat.postMessage", payload, &out); err != nil {
		return "", err
	}
	if !out.OK {
		return "", fmt.Errorf("slack chat.postMessage: %s", out.Error)
	}
	return out.TS, nil
}

func (a *API) UpdateMessage(ts, text string) error {
	payload := map[string]any{"channel": a.channel, "ts": ts, "text": text}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := a.call("chat.update", payload, &out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("slack chat.update: %s", out.Error)
	}
	return nil
}

func (a *API) call(method string, payload any, out any) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, a.baseURL+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 5: Add a retry decorator (spec: 3× bounded backoff).** Write the failing test in `slack_test.go`:

```go
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
```

- [ ] **Step 6: Run to verify it fails.**

Run: `go test ./internal/slack/... -run Retry`
Expected: FAIL (`undefined: WithRetry`).

- [ ] **Step 7: Add `retry.go`.**

```go
package slack

import "time"

type retryClient struct {
	c        Client
	attempts int
	base     time.Duration
	sleep    func(time.Duration)
}

// WithRetry wraps c with bounded exponential backoff. sleep is injectable so
// tests don't actually wait. On exhaustion the last error is returned (caller
// logs + drops — never blocks ingest).
func WithRetry(c Client, attempts int, base time.Duration, sleep func(time.Duration)) Client {
	return &retryClient{c: c, attempts: attempts, base: base, sleep: sleep}
}

func (r *retryClient) PostMessage(text, threadTS string) (string, error) {
	var ts string
	var err error
	d := r.base
	for i := 0; i < r.attempts; i++ {
		ts, err = r.c.PostMessage(text, threadTS)
		if err == nil {
			return ts, nil
		}
		if i < r.attempts-1 {
			r.sleep(d)
			d *= 2
		}
	}
	return "", err
}

func (r *retryClient) UpdateMessage(ts, text string) error {
	var err error
	d := r.base
	for i := 0; i < r.attempts; i++ {
		err = r.c.UpdateMessage(ts, text)
		if err == nil {
			return nil
		}
		if i < r.attempts-1 {
			r.sleep(d)
			d *= 2
		}
	}
	return err
}
```

- [ ] **Step 8: Run tests to verify they pass.**

Run: `go test ./internal/slack/...`
Expected: PASS.

- [ ] **Step 9: Commit.**

```bash
git add alert-thread-proxy/internal/slack
git commit -m "feat(alert-thread-proxy): Slack Web API client + fake + retry"
```

---

### Task 6: Engine — incident lifecycle (firing / reminder / resolve / sweep)

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/engine/engine.go`
- Test: `alert-thread-proxy/internal/engine/engine_test.go`

**Interfaces:**
- Consumes: `event.AlertEvent`, `slack.Client`, `config.Config`, `deeplink.Build`.
- Produces:
  - `type engine.Engine` with `func engine.New(cfg config.Config, sc slack.Client, now func() time.Time) *Engine`.
  - `func (*Engine) Handle(e event.AlertEvent) error`
  - `func (*Engine) Sweep()`
  - internal `type Incident struct { TS string; FirstSeen, LastReminder time.Time; Refires int; InStorm bool }` (storm fields used in Task 7).

This task implements everything EXCEPT storm digest (Task 7 extends `Handle`).

- [ ] **Step 1: Write failing tests** for the four lifecycle behaviors.

```go
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
	e.Handle(firing("a1", "x"))       // parent
	clk = clk.Add(5 * time.Minute)    // within ReminderInterval
	e.Handle(firing("a1", "x"))       // swallowed
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 call (throttled), got %+v", f.Calls)
	}
	clk = clk.Add(30 * time.Minute)   // now past ReminderInterval
	e.Handle(firing("a1", "x"))       // reminder reply
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
```

- [ ] **Step 2: Run to verify they fail.**

Run: `go test ./internal/engine/...`
Expected: FAIL (`undefined: New`).

- [ ] **Step 3: Write `engine.go`** (no storm logic yet — Task 7 adds it).

```go
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
	return fmt.Sprintf("🔴 *%s* — %s (%s)\n🔗 %s",
		ev.Title, ev.GroupKey, ev.Severity, deeplink.Build(e.cfg.HyperDXBaseURL, ev))
}
func (e *Engine) reminderText(ev event.AlertEvent, inc *Incident) string {
	return fmt.Sprintf("still firing (×%d)", inc.Refires+1)
}
func (e *Engine) resolvedText(ev event.AlertEvent) string {
	return fmt.Sprintf("✅ resolved — %s", ev.Title)
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/engine/...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add alert-thread-proxy/internal/engine
git commit -m "feat(alert-thread-proxy): engine incident lifecycle + sweep"
```

---

### Task 7: Engine — storm digest

**Repo:** `dobbo-ca/containers`

**Files:**
- Modify: `alert-thread-proxy/internal/engine/engine.go` (add storm handling to `handleFiring`; add helpers)
- Test: `alert-thread-proxy/internal/engine/storm_test.go`

**Interfaces:**
- Consumes/Produces: same `Engine`; adds unexported helpers `recordNewFiring`, `stormActive`, `enterStorm`, `stormText`. No signature changes.

- [ ] **Step 1: Write failing storm tests.**

```go
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
	// 10th distinct firing within the window trips storm mode:
	// one storm parent + this firing as a reply under it.
	e.Handle(firing("s10", ""))
	last2 := f.Calls[len(f.Calls)-2:]
	if last2[0].ThreadTS != "" { // storm parent is top-level
		t.Fatalf("expected storm parent top-level, got %+v", last2[0])
	}
	if last2[1].ThreadTS == "" { // firing routed as reply
		t.Fatalf("expected firing routed under storm parent, got %+v", last2[1])
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
```

Add `itoa` helper at the bottom of `storm_test.go`:

```go
func itoa(i int) string { return "id" + string(rune('0'+i)) }
```

- [ ] **Step 2: Run to verify they fail.**

Run: `go test ./internal/engine/... -run Storm`
Expected: FAIL (storm routing not implemented — firings post as separate parents).

- [ ] **Step 3: Edit `handleFiring`** to insert storm handling before `openNormalIncident`. Replace the `// new incident ...` line and the final `return e.openNormalIncident(ev, key)` with:

```go
	// new incident: record it in the storm window and decide routing.
	e.recordNewFiring()
	if e.stormActive() {
		return e.openStormIncident(ev, key)
	}
	if len(e.stormWindow) >= e.cfg.StormThreshold {
		return e.enterStorm(ev, key)
	}
	return e.openNormalIncident(ev, key)
```

- [ ] **Step 4: Add the storm helpers** to `engine.go`:

```go
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
```

- [ ] **Step 5: Run tests to verify they pass.**

Run: `go test ./internal/engine/...`
Expected: PASS (lifecycle + storm).

- [ ] **Step 6: Commit.**

```bash
git add alert-thread-proxy/internal/engine
git commit -m "feat(alert-thread-proxy): storm digest routing"
```

---

### Task 8: HTTP server + main wiring

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/internal/server/server.go`
- Test: `alert-thread-proxy/internal/server/server_test.go`
- Create: `alert-thread-proxy/main.go`

**Interfaces:**
- Consumes: `parse.Event`, `engine.Engine` (via a small `Handler` interface), `config.Config`.
- Produces: `func server.New(h Handler) *server.Server` where `type Handler interface { Handle(event.AlertEvent) error }`; routes `POST /webhook`, `GET /healthz`.

- [ ] **Step 1: Write the failing server test.**

```go
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
	mu   sync.Mutex
	evs  []event.AlertEvent
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
	body := `{"alertId":"a1","status":"firing","group":"x","timestamp":"2026-07-09T16:00:00Z"}`
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
```

- [ ] **Step 2: Run to verify it fails.**

Run: `go test ./internal/server/...`
Expected: FAIL (`undefined: New`).

- [ ] **Step 3: Write `server.go`.**

```go
package server

import (
	"io"
	"log"
	"net/http"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
	"github.com/dobbo-ca/alert-thread-proxy/internal/parse"
)

type Handler interface {
	Handle(event.AlertEvent) error
}

type Server struct {
	h   Handler
	mux *http.ServeMux
}

func New(h Handler) *Server {
	s := &Server{h: h, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /webhook", s.webhook)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return s
}

func (s *Server) Mux() *http.ServeMux { return s.mux }

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	ev, err := parse.Event(body)
	if err != nil {
		log.Printf("bad payload: %v", err)
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	// Ack fast; process off the request path so HyperDX doesn't retry-storm us.
	go func() {
		if err := s.h.Handle(ev); err != nil {
			log.Printf("handle %s: %v", ev.ThreadKey(), err)
		}
	}()
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `go test ./internal/server/...`
Expected: PASS.

- [ ] **Step 5: Write `main.go`.**

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/config"
	"github.com/dobbo-ca/alert-thread-proxy/internal/engine"
	"github.com/dobbo-ca/alert-thread-proxy/internal/server"
	"github.com/dobbo-ca/alert-thread-proxy/internal/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	sc := slack.WithRetry(slack.New(cfg.SlackBotToken, cfg.SlackChannelID), 3, 200*time.Millisecond, time.Sleep)
	eng := engine.New(cfg, sc, time.Now)

	// periodic sweep for stale incidents + storm expiry
	go func() {
		t := time.NewTicker(time.Minute)
		for range t.C {
			eng.Sweep()
		}
	}()

	srv := server.New(eng)
	log.Printf("listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Mux()); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 6: Build + vet the whole module.**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build OK, vet clean, all tests PASS.

- [ ] **Step 7: Commit.**

```bash
git add alert-thread-proxy/internal/server alert-thread-proxy/main.go
git commit -m "feat(alert-thread-proxy): HTTP server + main wiring"
```

---

### Task 9: Dockerfile + CI

**Repo:** `dobbo-ca/containers`

**Files:**
- Create: `alert-thread-proxy/Dockerfile`
- Create/Modify: the repo's CI workflow to build + push `ghcr.io/dobbo-ca/alert-thread-proxy` (follow the existing container-build pattern in `dobbo-ca/containers`; mirror how `lakshmi` is built).

**Interfaces:**
- Produces: image `ghcr.io/dobbo-ca/alert-thread-proxy:<tag>` consumed by the k8s-dobbolab Deployment (Task 11).

- [ ] **Step 1: Write the `Dockerfile`.**

```dockerfile
# build
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/alert-thread-proxy .

# runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/alert-thread-proxy /alert-thread-proxy
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/alert-thread-proxy"]
```

- [ ] **Step 2: Verify the image builds and starts.**

Run:
```bash
cd alert-thread-proxy
docker build -t alert-thread-proxy:test .
docker run --rm -e SLACK_BOT_TOKEN=x -e SLACK_CHANNEL_ID=C1 -p 8080:8080 alert-thread-proxy:test &
sleep 1 && curl -fsS localhost:8080/healthz && echo OK
```
Expected: `OK` (healthz 200).

- [ ] **Step 3: Wire CI** to build + push on tag/merge, matching the repo's existing pattern for other containers (reuse the same workflow structure, ghcr login, and image-naming convention). Confirm the pushed image name is exactly `ghcr.io/dobbo-ca/alert-thread-proxy`.

- [ ] **Step 4: Commit.**

```bash
git add alert-thread-proxy/Dockerfile .github/
git commit -m "build(alert-thread-proxy): Dockerfile + CI image publish"
```

---

### Task 10: ExternalSecret for the Slack bot token

**Repo:** `k8s-dobbolab`

**Files:**
- Create: `gitops/apps/eso-alert-thread-proxy.yaml`

**Interfaces:**
- Consumes: SSM param `/dobbolab/eso/alert-thread-proxy/slack-bot-token` (create it out-of-band first).
- Produces: Secret `alert-thread-proxy-slack{token}` in ns `clickstack`, consumed by Task 11's Deployment.

**Precondition (out-of-band, once):** create the Slack app + bot user with `chat:write`, install to the workspace, invite it to the target channel, and put the bot token in SSM:
`aws ssm put-parameter --name /dobbolab/eso/alert-thread-proxy/slack-bot-token --type SecureString --value xoxb-...`

- [ ] **Step 1: Write the ExternalSecret** (mirror `gitops/apps/eso-alertmanager-slack.yaml`).

```yaml
---
# Slack bot token for alert-thread-proxy, from AWS SSM via ESO.
# Materializes Secret alert-thread-proxy-slack{token} in ns clickstack, consumed
# by the alert-thread-proxy Deployment as env SLACK_BOT_TOKEN.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: alert-thread-proxy-slack
  namespace: clickstack
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-parameterstore
    kind: ClusterSecretStore
  target:
    name: alert-thread-proxy-slack
    creationPolicy: Owner
  data:
    - secretKey: token
      remoteRef:
        key: /dobbolab/eso/alert-thread-proxy/slack-bot-token
```

- [ ] **Step 2: Confirm the Kustomization includes `gitops/apps/`** (it does — this is where `eso-alertmanager-slack.yaml` lives). No index edit needed if the app kustomization globs the directory; otherwise add the file to its `kustomization.yaml`.

- [ ] **Step 3: Validate the manifest.**

Run: `kubectl --context=dobbolab-local apply --dry-run=server -f gitops/apps/eso-alert-thread-proxy.yaml`
Expected: `externalsecret.external-secrets.io/alert-thread-proxy-slack created (server dry run)`.

- [ ] **Step 4: Commit.**

```bash
git add gitops/apps/eso-alert-thread-proxy.yaml
git commit -m "feat(alerts): ESO-sync Slack bot token for alert-thread-proxy"
```

---

### Task 11: Deployment + Service

**Repo:** `k8s-dobbolab`

**Files:**
- Create: `gitops/apps/alert-thread-proxy.yaml`

**Interfaces:**
- Consumes: image from Task 9, Secret from Task 10.
- Produces: Service `alert-thread-proxy.clickstack.svc:80` → the proxy `/webhook`, targeted by the HyperDX channel (Task 12).

- [ ] **Step 1: Write the Deployment + Service.**

```yaml
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: alert-thread-proxy
  namespace: clickstack
spec:
  replicas: 1              # single replica: in-memory thread state must not split
  strategy:
    type: Recreate         # never two at once
  selector:
    matchLabels:
      app.kubernetes.io/name: alert-thread-proxy
  template:
    metadata:
      labels:
        app.kubernetes.io/name: alert-thread-proxy
    spec:
      containers:
        - name: alert-thread-proxy
          image: ghcr.io/dobbo-ca/alert-thread-proxy:latest
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: SLACK_BOT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: alert-thread-proxy-slack
                  key: token
            - name: SLACK_CHANNEL_ID
              value: "REPLACE_WITH_CHANNEL_ID"   # set to the #alerts channel ID
            - name: HYPERDX_BASE_URL
              value: "https://hyperdx.example.com"
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
---
apiVersion: v1
kind: Service
metadata:
  name: alert-thread-proxy
  namespace: clickstack
spec:
  selector:
    app.kubernetes.io/name: alert-thread-proxy
  ports:
    - name: http
      port: 80
      targetPort: http
```

- [ ] **Step 2: Pin the image tag.** Replace `:latest` with the concrete tag CI produced in Task 9 (Flux/GitOps should pin, not float `latest`). Set `SLACK_CHANNEL_ID` to the real channel ID.

- [ ] **Step 3: Validate.**

Run: `kubectl --context=dobbolab-local apply --dry-run=server -f gitops/apps/alert-thread-proxy.yaml`
Expected: deployment + service `created (server dry run)`.

- [ ] **Step 4: Commit.**

```bash
git add gitops/apps/alert-thread-proxy.yaml
git commit -m "feat(alerts): deploy alert-thread-proxy (1 replica, clickstack)"
```

---

### Task 12: Wire HyperDX + end-to-end verification

**Repo:** none (cluster/HyperDX config + validation)

**Files:** none

- [ ] **Step 1: After Tasks 10–11 merge and Flux reconciles,** confirm the proxy is up:

Run: `kubectl --context=dobbolab-local -n clickstack get deploy alert-thread-proxy && kubectl --context=dobbolab-local -n clickstack logs deploy/alert-thread-proxy | tail`
Expected: 1/1 Ready, log line `listening on :8080`.

- [ ] **Step 2: Create the HyperDX generic-webhook channel** pointing at `http://alert-thread-proxy.clickstack.svc/webhook` (in the HyperDX UI or via the alerts API). Use the body template confirmed in Task 1.

- [ ] **Step 3: Fire a test alert** (a low-threshold HyperDX alert on any noisy search) and confirm in Slack: one parent message appears with a HyperDX deep-link.

- [ ] **Step 4: Verify threading** — let it re-fire (reminder ≤ 1 per 30m in-thread) and resolve (a "✅ resolved" reply in the same thread). Confirm the next firing starts a NEW thread.

- [ ] **Step 5: Verify storm digest** — trigger ≥ `STORM_THRESHOLD` distinct alerts within `STORM_WINDOW` (e.g. temporarily lower `STORM_THRESHOLD` via env) and confirm a single "⚠️ N alerts firing (storm)" thread instead of N top-level messages.

- [ ] **Step 6: Restore** `STORM_THRESHOLD` and remove any test alerts. Document the working channel + alert in the o11y migration notes.

---

## Self-Review

**Spec coverage:**
- Per-incident threading (firing→parent, reminders throttled, resolve→reply+close, next-firing=new-thread, 24h reset) → Task 6. ✓
- Storm digest → Task 7. ✓
- Deep-link enrichment → Task 4, used in Task 6. ✓
- In-memory state, single replica → Task 6 (`Engine`), Task 11 (`replicas:1`, `Recreate`). ✓
- ESO bot token → Task 10. ✓
- Deploy manifests in `gitops/apps/`, ns `clickstack` → Tasks 10–11. ✓
- Go image in `dobbo-ca/containers` → Tasks 1–9. ✓
- HyperDX generic-webhook channel → Task 12. ✓
- Error handling (ack-fast, bounded Slack retry, malformed→400) → Task 8 (ack-fast, 400), Task 5 (`WithRetry` 3× backoff, wired in `main`), engine logs on exhaustion. ✓
- Testing (state-machine table tests + smoke) → Tasks 6, 7, 8. ✓
- Payload/URL pinning → Task 1. ✓

All spec sections map to a task; no gaps.

**Placeholder scan:** no TBD/TODO in code steps; every code step has complete code. `REPLACE_WITH_CHANNEL_ID` and `:latest`→pinned-tag are explicit operator inputs called out in Task 11 Step 2, not placeholders in logic.

**Type consistency:** `event.AlertEvent`, `AlertState`, `ThreadKey()`, `slack.Client{PostMessage,UpdateMessage}`, `engine.New/Handle/Sweep`, `server.New/Mux`, `Incident{TS,FirstSeen,LastReminder,Refires,InStorm}`, `stormState{TS,Count,Expires}` are used consistently across Tasks 2–8. ✓

# Alert Thread Proxy — Design

**Date:** 2026-07-09
**Status:** Approved (brainstorming)
**Related:** ClickStack o11y migration (Task 8 — HyperDX Slack alerting). Replaces the retired kube-prometheus-stack Alertmanager → Slack path.

## Problem

HyperDX (self-hosted ClickStack) cannot thread Slack alerts, and neither can
Alertmanager. HyperDX's Slack channel is fire-and-forget: one message per alert
trigger, no message-`ts` tracking, no `chat.update`, no `thread_ts`. The result
in a busy channel is unreadable: firing, reminders, and recovery for one incident
scatter as separate top-level messages, and a broad outage floods the channel.

We want threaded, de-noised Slack alerts: each incident is one thread; a storm of
alerts collapses into a digest. HyperDX gives us no hook for this, so we add a
small proxy between HyperDX and Slack that owns the threading state.

## Goals

- One Slack thread per alert incident (firing → parent; reminders + recovery →
  replies).
- Suppress two kinds of noise: flapping/re-firing of a single alert, and storms
  of many distinct alerts at once.
- Each message links back to HyperDX for one-click context.
- Deploy through the existing GitOps + ESO patterns; no new operational surface
  beyond one small Deployment.

## Non-goals (v1)

- **Defining the HyperDX alert conditions.** This spec covers only the transport.
  The actual alerts (the `severity=critical` / `namespace=enshrouded` equivalents)
  are a separate follow-on, scriptable via the HyperDX alerts API once the proxy
  exists. They carry their own design (which searches, thresholds, windows).
- **Enrichment beyond a deep-link.** No HyperDX alerts-API lookups and no direct
  ClickHouse queries in v1 — documented as an upgrade path (see below).
- **Persistence.** State is in-memory (see State).
- **HA.** Single replica by design (see Architecture).

## Architecture

```
HyperDX alert ──(generic webhook, JSON)──▶ alert-thread-proxy (Go, 1 replica)
                                             │  in-memory state
                                             ▼
                                       Slack Web API
                              chat.postMessage / chat.update
                              (bot token, scope chat:write)
```

- **Single replica, `Recreate` strategy.** In-memory state owns the incident →
  thread-`ts` mapping. A second replica would split that state and double-post,
  so the Deployment is pinned to one replica and `Recreate` (never two at once).
- **Two repos.** The Go service (`Dockerfile`, source, CI, image push) lives in
  `dobbo-ca/containers/alert-thread-proxy` → `ghcr.io/dobbo-ca/containers/alert-thread-proxy`.
  The Kubernetes manifests live here in `k8s-dobbolab` (`gitops/apps/`).
- **Namespace `clickstack`**, co-located with HyperDX and the OTel gateway.
- **Language Go** — static binary, tiny scratch/distroless image, matches the
  org's existing service (lakshmi). HTTP server + timers + Slack client are all
  well-served by the stdlib + `slack-go` (or raw `net/http` to the Slack API).

## Components

1. **Ingest handler** (`POST /webhook`) — accepts HyperDX's generic-webhook POST,
   acks fast (`200`), hands the parsed event to the state machine. `GET /healthz`
   for the probe.
2. **Event parser** — maps the HyperDX payload to an internal `AlertEvent`
   `{alertId, state, groupKey, title, severity, link, firedAt}`. **The exact
   HyperDX field names are pinned at implementation time** from the HyperDX source
   or a captured sample (see Open Items); the parser isolates that mapping so the
   rest of the code is payload-agnostic.
3. **State machine** — the incident lifecycle + storm logic (below). The only
   stateful, testable core.
4. **Slack client** — thin wrapper over `chat.postMessage` / `chat.update`, with
   bounded retry. Injectable (interface) so tests use a fake.
5. **Sweeper** — background goroutine closing incidents older than `THREAD_MAX_AGE`
   and expiring the storm window.

## Alert incident lifecycle (state machine)

`threadKey = alertId + ":" + groupKey` (groupKey empty ⇒ just alertId).

State per incident: `{ts, firstSeen, lastReminder, refires}`.

| Event | Condition | Action |
|-------|-----------|--------|
| firing | no active thread for `threadKey` | `chat.postMessage` (parent); store incident |
| firing | active thread exists | re-fire: `refires++`; post an in-thread reminder **only if** `now - lastReminder > REMINDER_INTERVAL`, then set `lastReminder`; else swallow (flap throttle) |
| resolved | active thread exists | `chat.postMessage(thread_ts=ts)` "✅ resolved"; delete incident (thread closes) |
| resolved | no active thread | ignore (resolve without a known firing) |
| (sweeper) | incident age > `THREAD_MAX_AGE` | delete incident (safety reset so a never-resolved alert can't thread forever); next firing opens a fresh thread |

A resolve closes the incident, so the **next firing of the same alert opens a new
thread** — a new incident, read as such by on-call.

## Storm digest

A sliding window (`stormWindow`, default 60s) records the timestamps of *new*
incidents (new `threadKey`s only — not re-fires). When the count of new incidents
in the window reaches `STORM_THRESHOLD` (default 10):

- Enter **storm mode**: post one parent `⚠️ N alerts firing (storm)`.
- While in storm mode, route subsequent **new** firings as **replies** under the
  storm parent (not as N separate parents), updating the storm parent's count via
  `chat.update`. Resolves for those alerts reply into the storm thread.
- Exit storm mode when the new-incident rate stays below threshold for one full
  window; the next new firing starts a normal per-incident thread again.

This also sidesteps Slack's ~1 message/sec/channel rate limit that an un-digested
storm would hit.

Flap throttle and storm digest compose: within a storm, an individual alert's
re-fires are still throttled; outside a storm, incidents thread individually.

## Enrichment (v1: deep-link only)

Each Slack message includes a constructed **deep-link back to HyperDX**, filtered
to the alert's query + time window (URL built from `alertId` / query / `firedAt`;
scheme pinned at implementation from HyperDX's URL format). No API or ClickHouse
calls. Message body: `severity` icon + `title` + `groupKey` + the link.

**Upgrade path (documented, not built):**
- *HyperDX alerts API* — fetch the full alert object by `alertId` to embed query /
  threshold / current value if the webhook body proves thin.
- *ClickHouse direct* — query the triggering telemetry (top error samples, current
  metric value, sample trace IDs) for maximally actionable messages.

## State (in-memory)

- `incidents map[threadKey]*Incident`
- `stormWindow []time.Time` (ring/slice of recent new-incident timestamps)
- `activeStorm *Storm{ts, count, expires}`

A `sync.Mutex` guards all three (single replica, low volume). Restart (only on
deploy) loses in-flight thread mappings → at worst a resolve opens a fresh thread
(cosmetic); storm windows are <60s and trivially rebuilt. No data loss.

**Upgrade path:** SQLite on a small `ceph-block` PVC with the same schema if
restart-resilience is ever wanted; the state interface is written to allow it.

## Config + secrets

- **ESO `ExternalSecret`** (mirrors `gitops/apps/eso-alertmanager-slack.yaml`) →
  Slack **bot token** from SSM `/dobbolab/eso/alert-thread-proxy/slack-bot-token`
  into a Secret the Deployment mounts as env.
- **Env / config:**
  - `SLACK_BOT_TOKEN` (from ESO secret)
  - `SLACK_CHANNEL_ID`
  - `HYPERDX_BASE_URL` (for deep-link construction)
  - `STORM_THRESHOLD=10`, `STORM_WINDOW=60s`
  - `REMINDER_INTERVAL=30m`, `THREAD_MAX_AGE=24h`
- **Slack app:** a bot user with `chat:write` (and `chat:write.customize` if we
  set username/icon), added to the target channel. Token stored in SSM.

## Deployment (this repo)

Raw manifests in `gitops/apps/` (following `ddns-updater.yaml` / `lakshmi.yaml`):
- `Deployment` (replicas 1, `Recreate`, the ghcr image, env from the ESO secret,
  `/healthz` probe, small resource requests).
- `Service` (ClusterIP :80 → container port) — HyperDX POSTs here in-cluster.
- `ExternalSecret` (bot token).

## HyperDX side (config, not code)

Create one HyperDX **generic webhook** channel pointing at the proxy Service URL
(`http://alert-thread-proxy.clickstack.svc/webhook`), with custom headers/body as
needed. Attach it to the alert definitions (the separate follow-on). Because the
webhook body is templatable, we front-load fields there and keep the parser thin.

## Error handling

- Slack call fails → bounded retry (3× exponential backoff); then log + drop
  (homelab: never block ingest on Slack).
- Ingest acks `200` fast and processes on the state machine's goroutine so HyperDX
  doesn't time out or retry-storm us.
- Malformed / unparseable payload → log + `400`, no crash.
- All Slack API errors and dropped alerts are logged (visible in HyperDX's own
  otel_logs, ironically).

## Testing

- Table-driven unit tests on the **state machine** with a fake Slack client and an
  injected clock: firing → parent, re-fire → throttled reminder, resolve → reply +
  close, next-firing → new thread, `THREAD_MAX_AGE` sweep, and the storm
  transitions (enter at threshold, replies route to storm parent, exit after quiet
  window). This is the risky logic and gets the coverage.
- A thin integration smoke test: POST a sample HyperDX payload to `/webhook`,
  assert the fake Slack client received the expected call sequence.
- No framework beyond `testing`.

## Open items (pinned at implementation)

1. **HyperDX generic-webhook payload schema** — confirm exact field names/shape
   from HyperDX source or a captured sample; bind the parser to it.
2. **HyperDX deep-link URL format** — confirm the URL scheme for a search/alert
   view filtered by query + time.
3. **Slack bot token provisioning** — create the Slack app + bot token, put it in
   SSM at the path above.

## Success criteria

- A single alert firing, re-firing several times, then resolving produces exactly
  one Slack thread: one parent, at most one reminder per `REMINDER_INTERVAL`, one
  "resolved" reply.
- 10+ distinct alerts firing within 60s produce one storm digest message with
  replies, not 10+ top-level messages.
- Every message deep-links to HyperDX.
- Proxy survives a restart without crashing; loses only in-flight thread grouping.

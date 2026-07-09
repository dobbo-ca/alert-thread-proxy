# alert-thread-proxy

A small Go service that turns HyperDX's fire-and-forget alert webhooks into
**threaded, de-noised Slack notifications**.

HyperDX (self-hosted ClickStack) can't thread Slack alerts — one message per
trigger, no `thread_ts`, no update. This proxy sits between HyperDX and Slack and
owns the threading state:

- **Per-incident threads** — firing = parent message; reminders + recovery = replies.
- **Flap throttle** — a re-firing alert collapses into its thread (≤1 reminder / 30m).
- **Storm digest** — a burst of many distinct alerts collapses into one summary thread.
- **Deep-links** — every message links back to HyperDX.

```
HyperDX alert ──(generic webhook)──▶ alert-thread-proxy ──▶ Slack Web API
                                       (Go, 1 replica,        chat.postMessage /
                                        in-memory state)      chat.update, bot token
```

## Status

Designed and planned; **build not started**.

- Design: [`docs/design.md`](docs/design.md)
- Implementation plan (TDD, task-by-task): [`docs/plan.md`](docs/plan.md)

## Build

See `docs/plan.md`. Tasks 1–9 (this repo) build the service; Tasks 10–12 (deploy +
HyperDX wiring) live in the `k8s-dobbolab` repo and need cluster/HyperDX access.

Conventions: Go, module `github.com/dobbo-ca/alert-thread-proxy`, image
`ghcr.io/dobbo-ca/alert-thread-proxy`, no third-party Go deps, injected clock,
single replica. See `CLAUDE.md`.

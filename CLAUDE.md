# CLAUDE.md — alert-thread-proxy

Build guidance for agents working in this repo.

## What this is

A Go service: HyperDX generic-webhook → threaded, de-noised Slack alerts. See
`docs/design.md` (spec) and `docs/plan.md` (task-by-task TDD plan).

## How to build

Execute `docs/plan.md` task-by-task with `superpowers:subagent-driven-development`
(fresh subagent per task, review between). Each task is TDD: failing test → run it
fails → minimal impl → run it passes → commit. Start at **Task 1**.

- **This repo = Tasks 1–9** (the Go service + Dockerfile/CI).
- **Tasks 10–12** (ESO secret, Deployment/Service, HyperDX channel + e2e) live in
  the `k8s-dobbolab` repo and need cluster + HyperDX access — not here.

## Conventions (authoritative — override the plan where they differ)

- **Standalone repo, files at root.** The plan was written for a subdirectory;
  ignore any leading `alert-thread-proxy/` in its file paths, and treat any
  `cd alert-thread-proxy` step as a no-op.
- **Module:** `github.com/dobbo-ca/alert-thread-proxy`. **Image:** `ghcr.io/dobbo-ca/containers/alert-thread-proxy`.
- **No third-party Go dependencies** unless a task explicitly adds one. Slack is
  called over raw `net/http`.
- **Inject the clock** (`now func() time.Time`) in the engine — never call
  `time.Now()` inside stateful logic; tests depend on this.
- **Single replica by design** — in-memory state owns thread mappings.
- Stable internal type is `event.AlertEvent`; only `internal/parse` knows HyperDX's
  wire format.

## Task 1 dependency

Task 1 pins the exact HyperDX generic-webhook payload + the HyperDX deep-link URL
format. Do ONE of: read the HyperDX source, OR capture a live sample (needs access
to the HyperDX instance). If neither is available yet, proceed against the assumed
schema in the plan and adjust the single `hyperdxPayload` struct + `mapState`
switch once a real sample is in hand. Record the sample in `docs/hyperdx-webhook-sample.json`.

## Verify before done

Per task: `go test ./...` green. Whole service: `go build ./... && go vet ./... && go test ./...`.

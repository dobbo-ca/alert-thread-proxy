# HyperDX generic-webhook contract — SOURCE-CONFIRMED

Status: **SOURCE-CONFIRMED**, not the plan's assumed schema. Read directly from
`hyperdxio/hyperdx` (OSS repo), commit `c29d0df23b800d9837eebb700ee928cd61613b07`
(main, 2026-07-09):

- `packages/api/src/tasks/checkAlerts/template.ts` — `notifyChannel`,
  `handleSendGenericWebhook`, `sendGenericWebhook`, `renderAlertTemplate`
- `packages/api/src/tasks/checkAlerts/providers/default.ts` — `buildLogSearchLink`,
  `buildChartLink`
- `packages/api/src/models/alert.ts` — `AlertState`, `AlertChannel`, `AlertSource`
- `packages/api/src/tasks/util.ts` — `escapeJsonString`
- `packages/common-utils/src/types.ts` — `WebhookService`, `zAlertChannelType`

## Key finding: there is no fixed JSON schema

Unlike the plan's assumption (`alertId`, `title`, `status`, `group`, `severity`,
`url`, `timestamp` as a body HyperDX unilaterally sends), HyperDX's "webhook"
alert channel (`WebhookService.Generic` / `WebhookService.IncidentIO`) does **not**
dictate a JSON body. The operator configures a **Handlebars template string**
(`webhook.body`) in the HyperDX UI when creating the webhook channel, and HyperDX
compiles that template with a fixed set of variables at send time
(`sendGenericWebhook` in `template.ts`):

| template var | type | source |
|---|---|---|
| `{{eventId}}` | string | `objectHash({ alertId, channel: {type, id}, isGrouped, groupId? })` — a stable hash, **not** the raw alert ID |
| `{{state}}` | string | `AlertState` enum member, raw (unescaped) |
| `{{title}}` | string | pre-escaped via `escapeJsonString` (= `JSON.stringify(str).slice(1,-1)` — inner content only, template must supply the surrounding quotes) |
| `{{body}}` | string | pre-escaped via `escapeJsonString`, same caveat |
| `{{link}}` | string | pre-escaped via `escapeJsonString`; the deep-link URL (see below) |
| `{{startTime}}` | number (ms epoch) | raw, unescaped, unquoted in a numeric template slot |
| `{{endTime}}` | number (ms epoch) | raw, unescaped, unquoted in a numeric template slot |

Headers: `Content-Type: application/json` by default (operator can override),
plus any operator-configured headers, plus a HyperDX-set `Idempotency-Key` header
= hash of `{eventId, startTime, endTime, state}` (delivery is at-least-once —
useful if we ever want our own dedup, not required for parsing).

**Because we (the operator) choose the JSON shape**, `docs/hyperdx-webhook-sample.json`
in this repo is not something HyperDX emits by default — it is the JSON body
template we are pinning as the contract, built only from the variables above:

```
{
  "eventId": "{{eventId}}",
  "state": "{{state}}",
  "title": "{{title}}",
  "body": "{{body}}",
  "link": "{{link}}",
  "startTime": {{startTime}},
  "endTime": {{endTime}}
}
```

**Action for Task 12** (k8s-dobbolab repo, out of scope here): when creating the
HyperDX webhook channel, set its body to exactly this template. If it's ever
configured differently, `docs/hyperdx-webhook-sample.json` and this file are
stale and must be updated together with `internal/parse/parse.go`.

## Field-by-field (per the brief's checklist)

- **Alert identity**: no raw alert ID is ever passed to the template. Use
  `eventId` as the stable identity/thread key — it's a deterministic hash of
  `alertId + channel + group` (same alert+group always produces the same
  `eventId`, across both the firing and resolved sends), so it's exactly what
  Task 4 (thread-key derivation) needs.
- **Firing vs. resolved state**: field `state`, values are the `AlertState`
  enum (`packages/api/src/models/alert.ts`): `ALERT | DISABLED |
  INSUFFICIENT_DATA | OK | PENDING`. Confirmed via `checkAlerts/index.ts` that
  **only `ALERT` (firing) and `OK` (resolved) are ever sent to a webhook** —
  `isAlertResolved(state) = state === AlertState.OK`. `mapState` (Task 3) only
  needs to handle those two string values; treat anything else as unknown/error.
- **Grouping**: no separate structured field. If the alert has a `groupBy`,
  the group value is interpolated into free text inside `body` (e.g. `Group:
  "api-gateway"`) — it is NOT separately available to the webhook template.
  Do not try to parse it back out of `body`; `eventId`'s embedded `groupId`
  already makes group-scoped alerts thread separately.
- **Title**: field `title`. Already fully formatted by HyperDX, including an
  emoji prefix — `🚨 ` for firing, `✅ ` for resolved — plus alert description.
- **Severity**: **does not exist** in HyperDX's alert model at all. There is no
  severity/priority concept for saved-search or tile alerts — only threshold
  breach state. Do not add a severity field to `hyperdxPayload`.
- **Timestamp**: two fields, `startTime` and `endTime`, both ms-epoch numbers
  (the alert evaluation window). No single "timestamp" field.
- **URL / deep-link field**: field `link`. See format below.

## HyperDX UI deep-link URL format

Two shapes depending on alert source (`AlertSource.SAVED_SEARCH` vs.
`AlertSource.TILE`), built in `providers/default.ts`:

- **Saved search (log) alerts**:
  `{FRONTEND_URL}/search/{savedSearchId}?from={startTimeMs}&to={endTimeMs}&isLive=false`
- **Dashboard tile alerts**:
  `{FRONTEND_URL}/dashboards/{dashboardId}?from={startTimeMs - 7*granularityMs}&granularity={granularityString}&to={endTimeMs + 7*granularityMs}&highlightedTileId={tileId}`
  (`highlightedTileId` omitted if there's no `tileId`)

`docs/hyperdx-webhook-sample.json` uses the saved-search shape, since
log-alert-driven Slack threads are the primary use case here; the dashboard-tile
shape is documented above for completeness in case Task 3 needs to accept both.

## Reconciliation point

`internal/parse/parse.go`'s `hyperdxPayload` struct + `mapState` switch (Task 3)
is the single place to update if:
- the webhook body template configured in HyperDX (Task 12) changes, or
- a live HyperDX instance is later found to behave differently than this
  source read of `main` (e.g. after a HyperDX upgrade changes `template.ts`).

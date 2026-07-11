# alert-thread-proxy Helm chart

Deploys [alert-thread-proxy](../) — a single-replica proxy that turns HyperDX
generic-webhook alerts into threaded, de-noised Slack notifications.

## Design constraints baked in

- **Single replica.** `replicas: 1` + `strategy: Recreate` are hard-coded in the
  Deployment template (not values). In-memory state owns thread mappings; a
  second replica would split it and double-post.
- **Slack bot token** comes from a Kubernetes Secret, expected to be materialized
  by the External Secrets Operator (ESO) from SSM. The chart can optionally
  render that `ExternalSecret` (`externalSecret.enabled=true`).

## Install

```bash
helm install alert-thread-proxy ./chart \
  --namespace clickstack --create-namespace \
  --set config.slackChannelId=C0123456789 \
  --set config.hyperdxBaseUrl=https://hyperdx.example.com
```

This assumes the `alert-thread-proxy-slack` Secret (key `token`) already exists
(ESO-managed). To have the chart render the `ExternalSecret` instead:

```bash
helm install alert-thread-proxy ./chart -n clickstack --create-namespace \
  --set config.slackChannelId=C0123456789 \
  --set config.hyperdxBaseUrl=https://hyperdx.example.com \
  --set externalSecret.enabled=true
```

## Key values

| Key | Default | Notes |
|-----|---------|-------|
| `image.repository` | `ghcr.io/dobbo-ca/containers/alert-thread-proxy` | |
| `image.tag` | `""` → chart `appVersion` | |
| `config.slackChannelId` | `""` | **required** |
| `config.hyperdxBaseUrl` | `""` | **required** (deep-links) |
| `config.stormThreshold` / `stormWindow` / `reminderInterval` / `threadMaxAge` | `""` | optional; unset → binary defaults (10 / 60s / 30m / 24h) |
| `slackBotToken.existingSecret` / `.secretKey` | `alert-thread-proxy-slack` / `token` | Secret holding the bot token |
| `externalSecret.enabled` | `false` | render the ESO `ExternalSecret` |
| `service.port` | `80` | in-cluster webhook port (→ container `:8080`) |

## Published OCI chart

On each `v*` release tag, CI packages this chart (version + appVersion set from
the tag) and pushes it to GHCR:

```
oci://ghcr.io/dobbo-ca/charts/alert-thread-proxy
```

Install a released version directly:

```bash
helm install alert-thread-proxy oci://ghcr.io/dobbo-ca/charts/alert-thread-proxy \
  --version 0.1.0 -n clickstack --create-namespace \
  --set config.slackChannelId=C0123456789 \
  --set config.hyperdxBaseUrl=https://hyperdx.example.com
```

## GitOps (Flux) consumption

Reference the published OCI chart from a Flux `HelmRelease` via an
`OCIRepository` source pointing at `oci://ghcr.io/dobbo-ca/charts/alert-thread-proxy`
(or a `GitRepository` source pointing at this repo's `chart/` path).

package deeplink

import (
	"net/url"
	"strings"

	"github.com/dobbo-ca/alert-thread-proxy/internal/event"
)

// Build returns a URL back to HyperDX for this alert. Prefer the link HyperDX
// already put in the payload; otherwise construct one from the base URL.
// Real HyperDX webhooks always supply Link, so this is the normal path; the
// constructed fallback below is defensive-only.
func Build(baseURL string, e event.AlertEvent) string {
	if e.Link != "" {
		return e.Link
	}
	base := strings.TrimRight(baseURL, "/")
	q := url.Values{}
	q.Set("alertId", e.AlertID)
	return base + "/alerts?" + q.Encode()
}

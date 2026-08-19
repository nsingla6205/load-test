package activities

import (
	"context"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
)

// doHarvestHTTP executes req against the Harvest Farm REST API with an env-configurable timeout
// (HARVEST_POLLER_IO_TIMEOUT_SECONDS via hyperscaler.HarvestPollerIOTimeout, default 30s) so a hung
// farm no longer blocks the calling Temporal activity indefinitely. The timeout is enforced via
// http.Client.Timeout, which bounds the full request lifecycle including response body reads
// performed by the caller after Do returns. transport is optional and lets callers (e.g. poller
// rebalance delete) inject a RoundTripper for testing; pass nil to use the http.Client default transport.
func doHarvestHTTP(ctx context.Context, req *http.Request, transport http.RoundTripper) (*http.Response, error) {
	client := &http.Client{Timeout: hyperscaler.HarvestPollerIOTimeout(), Transport: transport}
	return client.Do(req.WithContext(ctx))
}

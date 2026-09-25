package ontap_rest

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// defaultHostProbeTimeoutSeconds is the fallback Phase 1 probe deadline (in
// seconds) used when ONTAP_REST_HOST_PROBE_TIMEOUT_SECONDS is unset or invalid.
const defaultHostProbeTimeoutSeconds = 3

// fastSelectConfig groups the tunables for two-phase fast host selection
// It is populated once from the environment via defaultFastSelectConfig.
type fastSelectConfig struct {
	// enabled is the deployment-level gate for two-phase fast host selection. It
	// is combined with RESTClientParams.FastHostSelect (a per-call opt-in) so the
	// behaviour can be dark-launched on the OCI side only.
	enabled bool
	// probeTimeout bounds a single host-selection probe (Phase 1). A dead host
	// therefore costs at most this long instead of the full retry budget.
	probeTimeout time.Duration
}

// defaultFastSelectConfig reads the fast-host-select tunables from the
// environment, applying safe defaults. A non-positive probe timeout is treated
// as unset and falls back to defaultHostProbeTimeoutSeconds, so a misconfigured
// "0" cannot silently collapse every probe deadline to zero (which would
// fail-fast every host and defeat selection).
func defaultFastSelectConfig() fastSelectConfig {
	probeSeconds := env.GetIntNotNegative("ONTAP_REST_HOST_PROBE_TIMEOUT_SECONDS", defaultHostProbeTimeoutSeconds)
	if probeSeconds <= 0 {
		probeSeconds = defaultHostProbeTimeoutSeconds
	}
	return fastSelectConfig{
		enabled:      env.GetBool("ONTAP_REST_FAST_HOST_SELECT", false),
		probeTimeout: time.Duration(probeSeconds) * time.Second,
	}
}

// fastSelectCfg is the process-wide fast-host-select configuration, read once at
// startup.
var fastSelectCfg = defaultFastSelectConfig()

// fastSelectDeps bundles the collaborators the fast host-select implementation
// depends on. Passing them explicitly lets tests drive every branch with fakes
// instead of mutating package-level globals.
type fastSelectDeps struct {
	// cfg carries the deployment gate and the Phase 1 probe timeout.
	cfg fastSelectConfig
	// probe performs the single-attempt Phase 1 reachability check for a host.
	probe func(rc *OntapRestClient, timeout time.Duration) error
	// newLegacy builds the standard (non-fast) client. It is used when the
	// deployment gate is disabled, and defaults to the NewOntapRestClient seam so
	// this path honours the same override var as the rest of the codebase.
	newLegacy func(params RESTClientParams) (RESTClient, error)
}

// defaultFastSelectDeps returns the production collaborators: the process-wide
// config, the real probe, and the standard client constructor (via the
// NewOntapRestClient override seam).
func defaultFastSelectDeps() fastSelectDeps {
	return fastSelectDeps{
		cfg:       fastSelectCfg,
		probe:     probeConnection,
		newLegacy: NewOntapRestClient,
	}
}

// buildProbeClient builds the Phase 1 (selection) client: a single-attempt
// client with no retry layer, used only to probe a host's reachability and then
// discarded.
func buildProbeClient(params RESTClientParams, host string) (*OntapRestClient, error) {
	return buildClientForHost(params, host, false)
}

// buildOperationClient builds the Phase 2 (operation) client: the real client
// returned to the caller, with the full retry policy for genuine operations.
func buildOperationClient(params RESTClientParams, host string) (*OntapRestClient, error) {
	return buildClientForHost(params, host, true)
}

// NewClientWithFastHostSelect builds an ONTAP REST client using two-phase fast
// host selection:
//
//   - Phase 1 (selection): probe each host with a single, short attempt (no retry
//     budget) and pick the first that responds within the configured probe timeout.
//   - Phase 2 (operation): build the real client for the chosen host WITH the full
//     retry policy, so genuine operations still get the normal resilience.
//
// It fully self-gates on BOTH the per-call opt-in (RESTClientParams.FastHostSelect)
// and the ONTAP_REST_FAST_HOST_SELECT deployment flag: unless both are set it
// transparently delegates to the standard client constructor (NewOntapRestClient).
// Callers may therefore route here unconditionally; the fast path only runs when a
// caller opted in AND the deployment (OCI) enabled it.
//
// Contract on total failure: if no host responds during Phase 1, it returns a
// retry-enabled client for the last tried host together with the last probe
// error, mirroring the legacy "all hosts failed" contract; the caller checks the
// error and its activity retry then re-runs selection.
func NewClientWithFastHostSelect(params RESTClientParams) (RESTClient, error) {
	return newClientWithFastHostSelect(params, defaultFastSelectDeps())
}

// newClientWithFastHostSelect is the dependency-injected implementation behind
// NewClientWithFastHostSelect. The collaborators (config, probe, legacy builder)
// are explicit parameters so tests can exercise every branch without touching
// package-level state.
func newClientWithFastHostSelect(params RESTClientParams, deps fastSelectDeps) (RESTClient, error) {
	// Fully enforce the combined gate here so the fast path cannot run unless the
	// caller opted in (params.FastHostSelect) AND the deployment enabled it
	// (cfg.enabled). This keeps the invariant self-contained even if a future
	// caller reaches this function without pre-checking FastHostSelect.
	if !params.FastHostSelect || !deps.cfg.enabled {
		return deps.newLegacy(params)
	}

	hosts := candidateHosts(params)
	if len(hosts) == 0 {
		// No hosts to try at all.
		return nil, fmt.Errorf("fast host-select: no ONTAP REST hosts to connect to")
	}

	var lastErr error
	var lastHost string
	for _, host := range hosts {
		probeClient, err := buildProbeClient(params, host)
		if err != nil {
			// A certificate/config error is not host-specific; abandon selection.
			return nil, err
		}
		perr := deps.probe(probeClient, deps.cfg.probeTimeout)
		// The probe client is only used to check reachability; it is never
		// returned to the caller. Release any connection it opened so a
		// successful probe does not leave a healthy socket orphaned in the
		// transport's idle pool until IdleConnTimeout. Phase 2 builds a fresh
		// client (with retries) for real operations.
		closeIdleConnections(probeClient)
		if perr != nil {
			params.Trace.Infof("fast host-select: host %s unreachable, skipping: %v", host, perr)
			lastErr = perr
			lastHost = host
			continue
		}
		params.Trace.Infof("fast host-select: selected reachable host %s", host)
		return buildOperationClient(params, host)
	}

	params.Trace.Errorf("fast host-select: no reachable ONTAP REST API host: %v", lastErr)
	rc, err := buildOperationClient(params, lastHost)
	if err != nil {
		return nil, err
	}
	params.Trace.Warnf("fast host-select: returning retry-enabled client for last tried host")
	return rc, lastErr
}

// closeIdleConnections releases any pooled connections held by a Phase 1 probe
// client's HTTP transport. Probe clients are discarded once a host's
// reachability is known, so without this a successful probe would leave its
// connection sitting in the idle pool until IdleConnTimeout instead of being
// freed promptly. It is a no-op when there is nothing to close.
func closeIdleConnections(rc *OntapRestClient) {
	if rc == nil || rc.httpRoundTripperTransport == nil {
		return
	}
	rc.httpRoundTripperTransport.CloseIdleConnections()
}

// probeConnection runs a single, deadline-bounded cluster GET to check whether a
// host is reachable during fast host selection (Phase 1). The client passed here
// must be built with withRetry=false so there is no retry loop; the timeout then
// bounds the whole probe (a dead host costs at most timeout).
func probeConnection(rc *OntapRestClient, timeout time.Duration) error {
	if rc == nil || rc.cluster == nil || rc.cluster.api == nil {
		return fmt.Errorf("cluster client not initialized")
	}

	ctx := rc.params.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	params := cluster.NewClusterGetParams().WithFields([]string{"version"}).WithContext(ctx)
	_, err := rc.cluster.api.ClusterGet(params, nil)
	return err
}

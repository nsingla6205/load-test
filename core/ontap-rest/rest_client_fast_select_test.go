package ontap_rest

import (
	"errors"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	rtclient "github.com/go-openapi/runtime/client"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest/transport"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// fastSelectBaseParams returns password-auth params with a single host, suitable
// for exercising the fast host-selection path without real network access.
func fastSelectBaseParams() RESTClientParams {
	return RESTClientParams{
		Hosts:              map[string]string{"localhost": "10.0.0.1"},
		Host:               "10.0.0.1",
		Password:           log.Secret("secret"),
		InsecureSkipVerify: true,
		Trace:              log.NewLogger().(*log.Slogger),
	}
}

// fastSelectTestDeps builds injected deps for the fast host-select tests: the
// given gate and probe, a 1s timeout, and the real legacy constructor as the
// fallback. Tests that need to assert whether the legacy path was taken override
// newLegacy themselves.
func fastSelectTestDeps(enabled bool, probe func(*OntapRestClient, time.Duration) error) fastSelectDeps {
	return fastSelectDeps{
		cfg:       fastSelectConfig{enabled: enabled, probeTimeout: time.Second},
		probe:     probe,
		newLegacy: NewOntapRestClient,
	}
}

func TestBuildClientForHost(t *testing.T) {
	params := fastSelectBaseParams()

	rc, err := buildClientForHost(params, "10.0.0.1", true)
	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.1", rc.Host())
}

// TestWithRetryTransport covers the retry-layer decision that buildClientForHost
// delegates to, so the withRetry=true/false behaviour is verified without
// inspecting a fully built client's internals.
func TestWithRetryTransport(t *testing.T) {
	trace := log.NewLogger().(*log.Slogger)
	rt := newRuntimeClient(fastSelectBaseParams())

	t.Run("withRetry=true wraps a RetryTransport", func(tt *testing.T) {
		got := withRetryTransport(trace, rt, true)
		_, ok := got.(*transport.RetryTransport)
		assert.True(tt, ok, "expected RetryTransport when withRetry=true")
	})

	t.Run("withRetry=false returns the bare runtime transport", func(tt *testing.T) {
		got := withRetryTransport(trace, rt, false)
		_, isRetry := got.(*transport.RetryTransport)
		assert.False(tt, isRetry, "expected no RetryTransport when withRetry=false")
		_, isRuntime := got.(*rtclient.Runtime)
		assert.True(tt, isRuntime, "expected the bare runtime transport when withRetry=false")
	})
}

func TestCandidateHosts(t *testing.T) {
	t.Run("SNI override mode dials IP keys", func(tt *testing.T) {
		params := RESTClientParams{
			Hosts:      map[string]string{"10.0.0.1": "node1.fqdn", "10.0.0.2": "node2.fqdn"},
			ServerName: "sni.example.com",
		}
		got := candidateHosts(params)
		assert.ElementsMatch(tt, []string{"10.0.0.1", "10.0.0.2"}, got)
	})

	t.Run("legacy mode dials FQDN values", func(tt *testing.T) {
		params := RESTClientParams{
			Hosts: map[string]string{"10.0.0.1": "node1.fqdn", "10.0.0.2": "node2.fqdn"},
		}
		got := candidateHosts(params)
		assert.ElementsMatch(tt, []string{"node1.fqdn", "node2.fqdn"}, got)
	})
}

func TestNewClientWithFastHostSelect_DisabledFallsBack(t *testing.T) {
	legacyCalled := false
	probeCalled := false
	deps := fastSelectDeps{
		cfg:   fastSelectConfig{enabled: false, probeTimeout: time.Second},
		probe: func(*OntapRestClient, time.Duration) error { probeCalled = true; return nil },
		newLegacy: func(RESTClientParams) (RESTClient, error) {
			legacyCalled = true
			return &OntapRestClient{}, nil
		},
	}

	params := fastSelectBaseParams()
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.NoError(t, err)
	assert.NotNil(t, cl)
	assert.True(t, legacyCalled, "disabled gate must delegate to the legacy client constructor")
	assert.False(t, probeCalled, "probe must not run when the deployment gate is off")
}

func TestNewClientWithFastHostSelect_OptOutFallsBack(t *testing.T) {
	legacyCalled := false
	probeCalled := false
	deps := fastSelectDeps{
		// Deployment flag enabled, but the caller did not opt in.
		cfg:   fastSelectConfig{enabled: true, probeTimeout: time.Second},
		probe: func(*OntapRestClient, time.Duration) error { probeCalled = true; return nil },
		newLegacy: func(RESTClientParams) (RESTClient, error) {
			legacyCalled = true
			return &OntapRestClient{}, nil
		},
	}

	params := fastSelectBaseParams()
	params.FastHostSelect = false

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.NoError(t, err)
	assert.NotNil(t, cl)
	assert.True(t, legacyCalled, "without the per-call opt-in it must fall back even when the deployment flag is enabled")
	assert.False(t, probeCalled, "probe must not run when the caller did not opt in")
}

func TestNewClientWithFastHostSelect_SelectsReachableHost(t *testing.T) {
	deps := fastSelectTestDeps(true, func(*OntapRestClient, time.Duration) error { return nil })
	deps.newLegacy = func(RESTClientParams) (RESTClient, error) {
		t.Fatal("legacy builder must not be used on the fast path")
		return nil, nil
	}

	params := fastSelectBaseParams()
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.NoError(t, err)
	assert.NotNil(t, cl)
	assert.Equal(t, "10.0.0.1", cl.Host())
	// The selected (Phase 2) client is built with withRetry=true; the retry-layer
	// guarantee is covered by TestWithRetryTransport.
}

func TestNewClientWithFastHostSelect_SkipsUnreachableHost(t *testing.T) {
	callCount := 0
	deps := fastSelectTestDeps(true, func(*OntapRestClient, time.Duration) error {
		callCount++
		if callCount == 1 {
			return errors.New("connection reset by peer")
		}
		return nil
	})

	params := fastSelectBaseParams()
	params.Hosts = map[string]string{"node1": "10.0.0.1", "node2": "10.0.0.2"}
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.NoError(t, err)
	assert.NotNil(t, cl)
	assert.Equal(t, 2, callCount, "should skip the first unreachable host and probe the next")
	assert.Contains(t, []string{"10.0.0.1", "10.0.0.2"}, cl.Host())
}

// TestNewClientWithFastHostSelect_ConnectionResetThenRecovers reproduces the
// production incident on the OCI reset-password path where a management endpoint
// answers the TCP handshake but then resets the connection mid-read (surfaced in
// logs as `Get "https://.../api/cluster?fields=version": read tcp ...: read:
// connection reset by peer`). The Phase 1 probe is built with withRetry=false,
// so a reset is not retried on that host — fast host-select must skip it and
// select the second, reachable endpoint. The injected error is a realistic
// *url.Error wrapping syscall.ECONNRESET, matching the error type the transport
// classifier inspects.
func TestNewClientWithFastHostSelect_ConnectionResetThenRecovers(t *testing.T) {
	resetErr := &url.Error{
		Op:  "Get",
		URL: "https://10.0.0.1/api/cluster?fields=version",
		Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
	}

	callCount := 0
	deps := fastSelectTestDeps(true, func(*OntapRestClient, time.Duration) error {
		callCount++
		if callCount == 1 {
			// Mirror the incident's probe failure: the Phase 1 cluster GET is
			// reset by the peer.
			return resetErr
		}
		return nil
	})

	params := fastSelectBaseParams()
	params.Hosts = map[string]string{"node1": "10.0.0.1", "node2": "10.0.0.2"}
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.NoError(t, err, "failover to the reachable host must succeed despite the first host's connection reset")
	assert.NotNil(t, cl)
	assert.Equal(t, 2, callCount, "should skip the connection-reset host and probe the next")
	assert.Contains(t, []string{"10.0.0.1", "10.0.0.2"}, cl.Host())
	assert.Contains(t, resetErr.Error(), "connection reset by peer", "precondition: injected error renders like the incident log")
}

func TestNewClientWithFastHostSelect_AllHostsUnreachable(t *testing.T) {
	deps := fastSelectTestDeps(true, func(*OntapRestClient, time.Duration) error {
		return errors.New("connection reset by peer")
	})

	params := fastSelectBaseParams()
	params.Hosts = map[string]string{"node1": "10.0.0.1", "node2": "10.0.0.2"}
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	// Mirrors the legacy "all hosts failed" contract: a usable client for the last
	// tried host plus the last probe error, so the caller's activity retry re-runs
	// selection. The fallback is built with withRetry=true (retry-layer guarantee
	// covered by TestWithRetryTransport).
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection reset by peer")
	assert.NotNil(t, cl)
}

func TestNewClientWithFastHostSelect_NoHosts(t *testing.T) {
	probeCalled := false
	deps := fastSelectTestDeps(true, func(*OntapRestClient, time.Duration) error {
		probeCalled = true
		return nil
	})

	params := fastSelectBaseParams()
	params.Hosts = map[string]string{}
	params.FastHostSelect = true

	cl, err := newClientWithFastHostSelect(params, deps)
	assert.Error(t, err)
	assert.Nil(t, cl)
	assert.Contains(t, err.Error(), "no ONTAP REST hosts")
	assert.False(t, probeCalled, "no hosts means no probe should run")
}

func TestProbeConnection_GuardClauses(t *testing.T) {
	t.Run("nil cluster client", func(tt *testing.T) {
		err := probeConnection(&OntapRestClient{cluster: nil}, time.Second)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster client not initialized")
	})

	t.Run("nil cluster api", func(tt *testing.T) {
		err := probeConnection(&OntapRestClient{cluster: &clusterClient{api: nil}}, time.Second)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster client not initialized")
	})

	t.Run("nil client", func(tt *testing.T) {
		err := probeConnection(nil, time.Second)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "cluster client not initialized")
	})
}

func TestProbeConnection_NilCtxFallback(t *testing.T) {
	// fastSelectBaseParams leaves Ctx nil, so this exercises the fallback branch.
	params := fastSelectBaseParams()
	rc, err := buildClientForHost(params, "10.0.0.1", false)
	assert.NoError(t, err)
	assert.Nil(t, rc.params.Ctx, "precondition: Ctx must be nil to exercise the fallback")

	// With a nil Ctx, probeConnection must fall back to context.Background()
	// rather than panicking in context.WithTimeout. The dial fails within the
	// short timeout; we only assert it returns (an error) instead of panicking.
	assert.NotPanics(t, func() {
		_ = probeConnection(rc, 50*time.Millisecond)
	})
}

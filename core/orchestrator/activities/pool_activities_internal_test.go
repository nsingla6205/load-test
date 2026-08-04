package activities

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// Internal-only helpers for OCI pool resource naming. Trivial but keeps the
// naming contract pinned so renames are explicit.
func TestOciOntapCertificateName(t *testing.T) {
	cases := []struct {
		name   string
		pool   *datamodel.Pool
		expect string
	}{
		{"simple", &datamodel.Pool{DeploymentName: "dep-abc"}, "dep-abc-cert"},
		{"empty deployment", &datamodel.Pool{DeploymentName: ""}, "-cert"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, ociOntapCertificateName(tc.pool))
		})
	}
}

func TestOciOntapAdminSecretName(t *testing.T) {
	cases := []struct {
		name   string
		pool   *datamodel.Pool
		expect string
	}{
		{"simple", &datamodel.Pool{DeploymentName: "dep-abc"}, "dep-abc-secret"},
		{"empty deployment", &datamodel.Pool{DeploymentName: ""}, "-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, ociOntapAdminSecretName(tc.pool))
		})
	}
}

// isOCISyntheticSNI classifies a persisted HostDNSName as either a synthetic
// TLS-SNI value (SNI-override path, "mgmt." prefix → skip DNS delete) or a real
// OCI Private DNS A record ("dns-N." → must be deleted). The "skip" outcome is
// the cautious one, so anything that is not clearly an SNI must classify as a
// deletable record.
func TestIsOCISyntheticSNI(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		expect bool
	}{
		{"synthetic SNI", "mgmt.ocnv-1647973570f39820.iad.vsa", true},
		{"synthetic SNI with trailing dot", "mgmt.ocnv-1647973570f39820.iad.vsa.", true},
		{"synthetic SNI with surrounding space", "  mgmt.ocnv-1647973570f39820.iad.vsa  ", true},
		{"real dns record", "dns-1.ocnv-1647973570f39820.iad.vsa.", false},
		{"real dns record no trailing dot", "dns-2.ocnv-1647973570f39820.iad.vsa", false},
		{"raw ip (legacy/default)", "10.0.0.1", false},
		{"empty string", "", false},
		{"unrelated host", "node.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, isOCISyntheticSNI(tc.host))
		})
	}
}

// ---------------------------------------------------------------------------
// WaitForNodeDNS — polling loop behaviour.
//
// These tests live in the internal package so they can patch the package-
// private waitForNodeDNSPollInterval / waitForNodeDNSPerAttemptDeadline vars
// and inject a fake DNSResolver via PoolActivity.DNSResolver. The external
// _test.go file covers the validation guards (nil pool, non-cert auth, etc);
// this block covers the four classification paths the reviewer flagged:
//   - NXDOMAIN              → keep polling
//   - timeout / temporary   → keep polling
//   - any other error       → return immediately (no retry)
//   - deadline exceeded     → return ErrOCIResourceProvisionError
//
// The ctx.Done() branch is intentionally not exercised here: Temporal's
// TestActivityEnvironment does not expose a hook to cancel an in-flight
// activity's context, and spinning up a real worker just to assert two lines
// of cancellation handling is not worth the test-fixture weight. The
// production path is straightforward (`case <-ctx.Done(): return ctx.Err()`)
// and is covered by Temporal SDK's own tests.
// ---------------------------------------------------------------------------

// fakeDNSResolver is a programmable DNSResolver. Each call to LookupHost
// advances through the script; once the script is exhausted the last entry is
// repeated indefinitely (used by the "always NXDOMAIN" deadline case).
type fakeDNSResolver struct {
	mu     sync.Mutex
	calls  int
	script []fakeDNSReply
}

type fakeDNSReply struct {
	ips []string
	err error
}

func (f *fakeDNSResolver) LookupHost(_ context.Context, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	idx := f.calls - 1
	if idx >= len(f.script) {
		idx = len(f.script) - 1
	}
	r := f.script[idx]
	return r.ips, r.err
}

func (f *fakeDNSResolver) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// withFastWaitForNodeDNS shrinks the package-private poll interval and
// per-attempt deadline for the duration of the test so loop scenarios complete
// in milliseconds rather than 10 minutes / 5 seconds per iteration. Restores
// the originals on cleanup.
//
// Tests using this helper must NOT call t.Parallel — the patched vars are
// process-global. See the comment on waitForNodeDNSPollInterval in
// pool_activities.go for the rationale.
func withFastWaitForNodeDNS(t *testing.T, poll, deadline time.Duration) {
	t.Helper()
	origPoll, origDeadline := waitForNodeDNSPollInterval, waitForNodeDNSPerAttemptDeadline
	waitForNodeDNSPollInterval = poll
	waitForNodeDNSPerAttemptDeadline = deadline
	t.Cleanup(func() {
		waitForNodeDNSPollInterval = origPoll
		waitForNodeDNSPerAttemptDeadline = origDeadline
	})
}

// assertWaitDNSAppError mirrors the external assertTemporalApplicationError
// helper but is callable from the internal package, and asserts on the VCP
// tracking ID (not the textual error type name) so the test stays robust to
// vsaerrors.CustomErrorType renames.
func assertWaitDNSAppError(t *testing.T, err error, wantTrackingID int, wantMsgSubstring string) {
	t.Helper()
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "expected *temporal.ApplicationError, got %T", err)

	var trackingID int
	var originalMsg string
	require.NoError(t, appErr.Details(&trackingID, &originalMsg))

	assert.Equal(t, wantTrackingID, trackingID, "vsaerrors tracking ID mismatch")
	assert.Contains(t, originalMsg, wantMsgSubstring)
}

// TestPoolActivity_WaitForNodeDNS_LoopBehavior covers every observable path
// through the polling loop with a deterministic fake resolver. Each sub-test
// pre-sets fast poll/deadline timings so the suite completes in milliseconds.
func TestPoolActivity_WaitForNodeDNS_LoopBehavior(t *testing.T) {
	const clusterName = "ocicluster"

	origDNSName := env.VsaDeployedDnsName
	defer func() { env.VsaDeployedDnsName = origDNSName }()
	env.VsaDeployedDnsName = "vsa.netapp.internal"

	// Single HA pair → fqdns slice will have exactly 2 entries
	// (dns-1.<cluster>.<zone> and dns-2.<cluster>.<zone>). Using one HA pair
	// keeps the per-FQDN expected-calls arithmetic obvious in the table below.
	vlmCfg := &vlm.VLMConfig{Cloud: vlm.CloudConfig{HAPairs: []vlm.HAPair{{}}}}
	certAuthPool := &datamodel.Pool{
		Name:            "p",
		DeploymentName:  "dep",
		PoolCredentials: &datamodel.PoolCredentials{AuthType: env.USER_CERTIFICATE},
	}

	cases := []struct {
		name             string
		script           []fakeDNSReply
		poll             time.Duration
		deadline         time.Duration
		wantErr          bool
		wantTrackingID   int
		wantMsgSubstring string
		wantMinCalls     int
		wantMaxCalls     int // 0 = no upper bound
	}{
		{
			name: "NXDOMAIN twice then resolves — retries past NXDOMAIN",
			script: []fakeDNSReply{
				{err: &net.DNSError{Err: "no such host", IsNotFound: true}},
				{err: &net.DNSError{Err: "no such host", IsNotFound: true}},
				{ips: []string{"10.0.0.1"}}, // FQDN #1 finally resolves
				{ips: []string{"10.0.0.2"}}, // FQDN #2 resolves on first lookup
			},
			poll: time.Millisecond, deadline: time.Second,
			wantErr:      false,
			wantMinCalls: 4, wantMaxCalls: 4,
		},
		{
			name: "timeout then resolves — retries past temporary error",
			script: []fakeDNSReply{
				{err: &net.DNSError{Err: "i/o timeout", IsTimeout: true}},
				{ips: []string{"10.0.0.1"}}, // FQDN #1
				{ips: []string{"10.0.0.2"}}, // FQDN #2
			},
			poll: time.Millisecond, deadline: time.Second,
			wantErr:      false,
			wantMinCalls: 3, wantMaxCalls: 3,
		},
		{
			name: "non-DNS error — immediate fail, no retry",
			script: []fakeDNSReply{
				{err: errors.New("non-dns wire error")},
			},
			poll: time.Millisecond, deadline: time.Second,
			wantErr:          true,
			wantTrackingID:   vsaerrors.ErrOCIResourceFetchError,
			wantMsgSubstring: "DNS lookup for",
			wantMinCalls:     1, wantMaxCalls: 1, // proves no retry on unclassified error
		},
		{
			name: "always NXDOMAIN — deadline exceeded fails with provision error",
			script: []fakeDNSReply{
				{err: &net.DNSError{Err: "no such host", IsNotFound: true}},
			},
			poll: 100 * time.Microsecond, deadline: 5 * time.Millisecond,
			wantErr:          true,
			wantTrackingID:   vsaerrors.ErrOCIResourceProvisionError,
			wantMsgSubstring: "timed out waiting for OCI DNS to publish",
			wantMinCalls:     1, // at least one lookup before the deadline check fires
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFastWaitForNodeDNS(t, tc.poll, tc.deadline)
			fake := &fakeDNSResolver{script: tc.script}

			var ts testsuite.WorkflowTestSuite
			te := ts.NewTestActivityEnvironment()
			pa := &PoolActivity{DNSResolver: fake}
			te.RegisterActivity(pa.WaitForNodeDNS)

			_, err := te.ExecuteActivity(pa.WaitForNodeDNS, certAuthPool, vlmCfg, clusterName)

			if tc.wantErr {
				assertWaitDNSAppError(t, err, tc.wantTrackingID, tc.wantMsgSubstring)
			} else {
				require.NoError(t, err)
			}

			got := fake.callCount()
			assert.GreaterOrEqual(t, got, tc.wantMinCalls,
				"resolver was called %d times, want >= %d", got, tc.wantMinCalls)
			if tc.wantMaxCalls > 0 {
				assert.LessOrEqual(t, got, tc.wantMaxCalls,
					"resolver was called %d times, want <= %d", got, tc.wantMaxCalls)
			}
		})
	}
}

// withTestOCIVMRSConfigPath points OCIVMRSConfigFilePath at the production
// catalogue for activity unit tests. Restored via t.Cleanup.
func withTestOCIVMRSConfigPath(t *testing.T) {
	t.Helper()
	path, err := filepath.Abs("../../../config/vmrs_oci.yaml")
	require.NoError(t, err)
	orig := OCIVMRSConfigFilePath
	OCIVMRSConfigFilePath = path
	t.Cleanup(func() { OCIVMRSConfigFilePath = orig })
}

func TestDecideOCIVMRS_Success_ActiveActive(t *testing.T) {
	withTestOCIVMRSConfigPath(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	pa := &PoolActivity{}
	env.RegisterActivity(pa.DecideOCIVMRS)

	// 6 TB total / 2 active VMs (1 HA pair, AA) = 3 TB/VM → fits 1600 tier (floor 2.53).
	req := DecideOCIVMRSRequest{
		PoolUUID: "pool-aa",
		Target: PoolVMRSSpec{
			SizeInBytes:     6e12,
			ThroughputMibps: 1600,
			HAPairs:         1,
			IsRegionalHA:    false,
		},
	}

	val, err := env.ExecuteActivity(pa.DecideOCIVMRS, req)
	require.NoError(t, err)
	require.True(t, val.HasValue())
}

func TestDecideOCIVMRS_Success_RegionalHA_WithCurrent(t *testing.T) {
	withTestOCIVMRSConfigPath(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	pa := &PoolActivity{}
	env.RegisterActivity(pa.DecideOCIVMRS)

	// AP: 1 active VM per pair. 3 TB total / 1 = 3 TB/VM → fits 1600 tier.
	currentIOPS := int64(20000)
	req := DecideOCIVMRSRequest{
		PoolUUID: "pool-ap",
		Current: &PoolVMRSSpec{
			SizeInBytes:     3e12,
			ThroughputMibps: 1600,
			Iops:            &currentIOPS,
			HAPairs:         1,
			IsRegionalHA:    true,
		},
		Target: PoolVMRSSpec{
			SizeInBytes:     3e12,
			ThroughputMibps: 1600,
			HAPairs:         1,
			IsRegionalHA:    true,
		},
	}

	val, err := env.ExecuteActivity(pa.DecideOCIVMRS, req)
	require.NoError(t, err)
	require.True(t, val.HasValue())
}

func TestDecideOCIVMRS_RejectsInvalidInputs(t *testing.T) {
	withTestOCIVMRSConfigPath(t)

	cases := []struct {
		name string
		req  DecideOCIVMRSRequest
		msg  string
	}{
		{
			name: "haPairs",
			req: DecideOCIVMRSRequest{
				PoolUUID: "p",
				Target:   PoolVMRSSpec{SizeInBytes: 3e12, ThroughputMibps: 800, HAPairs: 0},
			},
			msg: "haPairs must be > 0",
		},
		{
			name: "sizeInBytes",
			req: DecideOCIVMRSRequest{
				PoolUUID: "p",
				Target:   PoolVMRSSpec{SizeInBytes: 0, ThroughputMibps: 800, HAPairs: 1},
			},
			msg: "sizeInBytes must be > 0",
		},
		{
			name: "throughput",
			req: DecideOCIVMRSRequest{
				PoolUUID: "p",
				Target:   PoolVMRSSpec{SizeInBytes: 3e12, ThroughputMibps: 0, HAPairs: 1},
			},
			msg: "throughputMibps must be > 0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestActivityEnvironment()
			pa := &PoolActivity{}
			env.RegisterActivity(pa.DecideOCIVMRS)

			_, err := env.ExecuteActivity(pa.DecideOCIVMRS, tc.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
}

func TestDecideOCIVMRS_ConfigLoadFailure(t *testing.T) {
	orig := OCIVMRSConfigFilePath
	OCIVMRSConfigFilePath = "/nonexistent/vmrs_oci.yaml"
	t.Cleanup(func() { OCIVMRSConfigFilePath = orig })

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	pa := &PoolActivity{}
	env.RegisterActivity(pa.DecideOCIVMRS)

	_, err := env.ExecuteActivity(pa.DecideOCIVMRS, DecideOCIVMRSRequest{
		PoolUUID: "p",
		Target:   PoolVMRSSpec{SizeInBytes: 3e12, ThroughputMibps: 800, HAPairs: 1},
	})
	require.Error(t, err)
}

func TestDecideOCIVMRS_SelectionFailure(t *testing.T) {
	withTestOCIVMRSConfigPath(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	pa := &PoolActivity{}
	env.RegisterActivity(pa.DecideOCIVMRS)

	// 1000 MB/s is not a multiple of 800 → selector rejects.
	_, err := env.ExecuteActivity(pa.DecideOCIVMRS, DecideOCIVMRSRequest{
		PoolUUID: "p",
		Target:   PoolVMRSSpec{SizeInBytes: 3e12, ThroughputMibps: 1000, HAPairs: 1},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple of")
}

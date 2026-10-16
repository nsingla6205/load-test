package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal/workerdeployment"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func TestLoadSpec(t *testing.T) {
	t.Setenv("ROLLOUT_SPEC", `[{"ontapVersion":"9.18.1P3","taskQueue":"vsa-lifecycle-manager-9.18.1P3","deploymentName":"vsa-lifecycle-manager-9-18-1P3","newBuildId":"B","oldBuildId":"A","drainDeployment":"vlm-worker-9-18-1p3-drain-A"}]`)
	spec, err := loadSpec()
	require.NoError(t, err)
	require.Len(t, spec.Targets, 1)
	assert.Equal(t, "B", spec.Targets[0].NewBuildID)
	assert.Equal(t, "A", spec.Targets[0].OldBuildID)

	t.Setenv("ROLLOUT_SPEC", "")
	spec, err = loadSpec()
	require.NoError(t, err)
	assert.Empty(t, spec.Targets)
}

func TestWaitForBuildsPollingWaitsForOldThenNew(t *testing.T) {
	var builds []string
	var intervals []time.Duration
	wait := func(
		_ context.Context,
		_ workerdeployment.Client,
		_ string,
		_ string,
		buildID string,
		pollEvery time.Duration,
	) error {
		builds = append(builds, buildID)
		intervals = append(intervals, pollEvery)
		return nil
	}

	pollEvery := 15 * time.Second
	err := waitForBuildsPolling(
		context.Background(),
		nil,
		"task-queue",
		"deployment",
		"A",
		"B",
		pollEvery,
		wait,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, builds)
	assert.Equal(t, []time.Duration{pollEvery, pollEvery}, intervals)
}

func TestWaitForBuildsPollingStopsWhenOldIsNotPolling(t *testing.T) {
	waitErr := errors.New("not polling")
	var builds []string
	wait := func(
		_ context.Context,
		_ workerdeployment.Client,
		_ string,
		_ string,
		buildID string,
		_ time.Duration,
	) error {
		builds = append(builds, buildID)
		return waitErr
	}

	err := waitForBuildsPolling(
		context.Background(),
		nil,
		"task-queue",
		"deployment",
		"A",
		"B",
		time.Second,
		wait,
	)

	require.ErrorIs(t, err, waitErr)
	assert.Equal(t, []string{"A"}, builds)
}

type readyK8s struct {
	ready    bool
	replicas int
	err      error
}

func (f readyK8s) deploymentReady(context.Context, string) (bool, int, error) {
	return f.ready, f.replicas, f.err
}

func TestWaitForDeploymentReady(t *testing.T) {
	replicas, err := waitForDeploymentReady(
		context.Background(),
		readyK8s{ready: true, replicas: 2},
		"vlm-worker-9-18-1p3-drain-A",
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Equal(t, 2, replicas)
}

func TestRecordFinishedCutoverCreatesConfigMapWhenMissing(t *testing.T) {
	var requests []struct {
		method string
		path   string
		body   map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests = append(requests, struct {
			method string
			path   string
			body   map[string]any
		}{method: r.Method, path: r.URL.Path, body: body})
		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	k8s := &k8sClient{http: server.Client(), token: "token", namespace: "vlm", baseURL: server.URL}
	require.NoError(t, k8s.recordFinishedCutover(context.Background(), "vlm-worker-ontap-9-17-1", "9.19.1P2", "8258295", "8293640"))

	require.Len(t, requests, 2)
	assert.Equal(t, http.MethodPatch, requests[0].method)
	assert.Equal(t, "/api/v1/namespaces/vlm/configmaps/vlm-worker-ontap-9-17-1-versioning-cutover", requests[0].path)
	assert.Equal(t, http.MethodPost, requests[1].method)
	data := requests[1].body["data"].(map[string]any)
	assert.Equal(t, "done", data["9.19.1P2__8258295__8293640"])
}

func TestNewK8sClientRequiresNamespace(t *testing.T) {
	_, err := newK8sClient("  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace is required")
}

func TestValidateEnsureDrainTarget(t *testing.T) {
	target := validRolloutTarget()
	require.NoError(t, validateEnsureDrainTarget(target))

	target.DrainDeployment = ""
	assert.ErrorContains(t, validateEnsureDrainTarget(target), "drain Deployment is required")
}

type ensureK8s struct {
	readyK8s
	scaleErr  error
	scaled    []string
	recordErr error
	recorded  []string
}

func (f *ensureK8s) scaleDeployment(_ context.Context, name string, replicas int) error {
	f.scaled = append(f.scaled, name)
	if replicas < 1 {
		return errors.New("replicas must be positive")
	}
	return f.scaleErr
}

func (f *ensureK8s) recordFinishedCutover(_ context.Context, _, ontapVersion, newBuildID, oldBuildID string) error {
	f.recorded = append(f.recorded, cutoverRecordKey(ontapVersion, newBuildID, oldBuildID))
	return f.recordErr
}

func TestEnsureDrainTargetsEstablishesOldBuildBeforeUpgrade(t *testing.T) {
	k8s := &ensureK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	var waitedBuilds []string
	wait := func(
		_ context.Context,
		_ workerdeployment.Client,
		_ string,
		_ string,
		buildID string,
		_ time.Duration,
	) error {
		waitedBuilds = append(waitedBuilds, buildID)
		return nil
	}

	err := ensureDrainTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		time.Millisecond,
		time.Second,
		wait,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Empty(t, k8s.scaled)
	assert.Equal(t, []string{"A"}, waitedBuilds)
}

func TestEnsureDrainTargetsLeavesMissingDeploymentToHelm(t *testing.T) {
	k8s := &ensureK8s{readyK8s: readyK8s{err: errDeploymentNotFound}}
	waited := false
	wait := func(context.Context, workerdeployment.Client, string, string, string, time.Duration) error {
		waited = true
		return nil
	}

	err := ensureDrainTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		time.Millisecond,
		time.Second,
		wait,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.False(t, waited)
	assert.Empty(t, k8s.scaled)
}

func TestEnsureDrainTargetsStopsWhenScaleFails(t *testing.T) {
	scaleErr := errors.New("Kubernetes unavailable")
	k8s := &ensureK8s{
		readyK8s: readyK8s{ready: false, replicas: 0},
		scaleErr: scaleErr,
	}

	err := ensureDrainTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		time.Millisecond,
		time.Second,
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorIs(t, err, scaleErr)
	assert.Equal(t, []string{"drain-deployment"}, k8s.scaled)
}

func TestDeploymentReadyWaitsForCurrentGeneration(t *testing.T) {
	responses := []string{
		`{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":1,"updatedReplicas":0,"readyReplicas":1,"unavailableReplicas":0}}`,
		`{"metadata":{"generation":2},"spec":{"replicas":1},"status":{"observedGeneration":2,"updatedReplicas":1,"readyReplicas":1,"unavailableReplicas":0}}`,
	}
	request := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responses[request])
		request++
	}))
	defer server.Close()
	k8s := &k8sClient{
		http:      server.Client(),
		namespace: "default",
		baseURL:   server.URL,
	}

	ready, replicas, err := k8s.deploymentReady(context.Background(), "drain-deployment")
	require.NoError(t, err)
	assert.False(t, ready)
	assert.Equal(t, 1, replicas)

	ready, replicas, err = k8s.deploymentReady(context.Background(), "drain-deployment")
	require.NoError(t, err)
	assert.True(t, ready)
	assert.Equal(t, 1, replicas)
}

// flakyTransport returns a transport-level error (not an HTTP response) for
// the first `failures` RoundTrips, then delegates to inner. It simulates the
// istio sidecar accepting and immediately closing a connection (bare EOF)
// before the outbound passthrough path has fully warmed up.
type flakyTransport struct {
	failures int
	calls    int
	inner    http.RoundTripper
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.failures {
		return nil, io.EOF
	}
	return f.inner.RoundTrip(req)
}

func TestDeploymentReadyRetriesTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	transport := &flakyTransport{failures: 2, inner: server.Client().Transport}
	k8s := &k8sClient{
		http:       &http.Client{Transport: transport},
		namespace:  "vlm",
		baseURL:    server.URL,
		retryDelay: time.Millisecond,
	}

	_, _, err := k8s.deploymentReady(context.Background(), "drain-deployment")
	require.ErrorIs(t, err, errDeploymentNotFound)
	assert.Equal(t, 3, transport.calls, "two failed attempts plus the one that succeeded")
}

func TestDeploymentReadyGivesUpAfterMaxRetries(t *testing.T) {
	transport := &flakyTransport{failures: k8sCallMaxRetries + 5}
	k8s := &k8sClient{
		http:       &http.Client{Transport: transport},
		namespace:  "vlm",
		baseURL:    "https://example.invalid",
		retryDelay: time.Millisecond,
	}

	_, _, err := k8s.deploymentReady(context.Background(), "drain-deployment")
	require.Error(t, err)
	assert.ErrorIs(t, err, io.EOF)
	assert.Equal(t, k8sCallMaxRetries+1, transport.calls, "the initial attempt plus every retry")
}

func TestScaleDeploymentRetriesTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := &flakyTransport{failures: 1, inner: server.Client().Transport}
	k8s := &k8sClient{
		http:       &http.Client{Transport: transport},
		namespace:  "vlm",
		baseURL:    server.URL,
		retryDelay: time.Millisecond,
	}

	require.NoError(t, k8s.scaleDeployment(context.Background(), "drain-deployment", 1))
	assert.Equal(t, 2, transport.calls)
}

type retireK8s struct {
	readyK8s
	scaled    []string
	scaledTo  []int
	scaleErr  error
	recordErr error
	recorded  []string
}

func (f *retireK8s) scaleDeployment(_ context.Context, name string, replicas int) error {
	f.scaled = append(f.scaled, name)
	f.scaledTo = append(f.scaledTo, replicas)
	return f.scaleErr
}

func (f *retireK8s) recordFinishedCutover(_ context.Context, _, ontapVersion, newBuildID, oldBuildID string) error {
	f.recorded = append(f.recorded, cutoverRecordKey(ontapVersion, newBuildID, oldBuildID))
	return f.recordErr
}

type promoteRecorder struct {
	promoted []string
	drained  []string
	deleted  []string
	drainErr map[string]error
}

func (r *promoteRecorder) waitPolling(
	_ context.Context,
	_ workerdeployment.Client,
	_ string,
	_ string,
	_ string,
	_ time.Duration,
) error {
	return nil
}

func (r *promoteRecorder) promote(_ context.Context, _ workerdeployment.Client, _ string, buildID string) error {
	r.promoted = append(r.promoted, buildID)
	return nil
}

func (r *promoteRecorder) waitForDrain(_ context.Context, _ workerdeployment.Client, _ string, buildID string, _ time.Duration) error {
	if err, ok := r.drainErr[buildID]; ok {
		return err
	}
	r.drained = append(r.drained, buildID)
	return nil
}

func (r *promoteRecorder) deleteBuild(_ context.Context, _ workerdeployment.Client, _ string, buildID string) error {
	r.deleted = append(r.deleted, buildID)
	return nil
}

func TestPromoteInitialCurrent(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		wantPromo []string
		wantKey   string
		wantErr   string
	}{
		{name: "unset current", wantPromo: []string{"8258295"}, wantKey: "9.19.1P2__8258295__initial"},
		{name: "already current", current: "8258295", wantKey: "9.19.1P2__8258295__initial"},
		{name: "different current", current: "8293640", wantErr: "set drainingBuilds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			k8s := &retireK8s{}
			rec := &promoteRecorder{}
			opts := promoteTestOpts(rec, false, false)
			opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
				return test.current, nil
			}
			err := promoteInitialCurrents(context.Background(), k8s, nil, []InitialCurrent{{
				OntapVersion:   "9.19.1P2",
				TaskQueue:      "vsa-lifecycle-manager-9.19.1P2",
				DeploymentName: "vsa-lifecycle-manager-9-19-1P2",
				BuildID:        "8258295",
				ReleaseName:    "vlm-worker-ontap-9-17-1",
			}}, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				assert.Empty(t, rec.promoted)
				assert.Empty(t, k8s.recorded)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantPromo, rec.promoted)
			assert.Equal(t, []string{test.wantKey}, k8s.recorded)
		})
	}
}

func promoteTestOpts(rec *promoteRecorder, dryRun, deleteAfter bool) promoteAndRetireOptions {
	return promoteAndRetireOptions{
		dryRun:         dryRun,
		deleteAfter:    deleteAfter,
		pollEvery:      time.Millisecond,
		readyTimeout:   time.Second,
		drainTimeout:   time.Second,
		pollingTimeout: time.Second,
		waitForPolling: rec.waitPolling,
		promote:        rec.promote,
		waitForDrain:   rec.waitForDrain,
		deleteBuild:    rec.deleteBuild,
	}
}

func TestPromoteAndRetireTargetsDryRunDoesNotMutate(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		promoteTestOpts(rec, true, true),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Empty(t, rec.promoted)
	assert.Empty(t, rec.drained)
	assert.Empty(t, rec.deleted)
	assert.Empty(t, k8s.scaled)
}

func TestEnsureDrainTargetsSkipsFinishedCutover(t *testing.T) {
	k8s := &ensureK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	current := func(context.Context, workerdeployment.Client, string) (string, error) { return "B", nil }
	drained := func(context.Context, workerdeployment.Client, string, string) (bool, error) { return true, nil }

	err := ensureDrainTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		time.Millisecond,
		time.Second,
		nil,
		current,
		drained,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Empty(t, k8s.scaled)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
}

func TestPromoteAndRetireTargetsSkipsFinishedCutover(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, true)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) { return "B", nil }
	opts.buildDrained = func(context.Context, workerdeployment.Client, string, string) (bool, error) { return true, nil }

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Empty(t, rec.promoted)
	assert.Empty(t, rec.drained)
	assert.Empty(t, rec.deleted)
	assert.Empty(t, k8s.scaled)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
}

func TestPromoteAndRetireTargetsSkipsSetCurrentWhenAlreadyCurrent(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.pinUnversionedWorkflows = true
	lists := 0
	opts.currentBuild = func(_ context.Context, _ workerdeployment.Client, _ string) (string, error) {
		return "B", nil
	}
	opts.listUnversioned = func(context.Context, string) ([]string, error) {
		lists++
		return nil, nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, pinUnversionedStableEmptyPasses, lists)
	assert.Empty(t, rec.promoted)
	assert.Equal(t, []string{"A"}, rec.drained)
	assert.Equal(t, []int{0}, k8s.scaledTo)
}

func TestPromoteAndRetireTargetsRefusesToPinBackwardWhenCurrentIsNew(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.pinUnversionedWorkflows = true
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "B", nil
	}
	opts.listUnversioned = func(context.Context, string) ([]string, error) {
		return []string{"gcnv-late"}, nil
	}
	opts.pinUnversioned = func(context.Context, string, string, string, time.Duration) error {
		t.Fatal("must not pin to the old build when current is already the new build")
		return nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "gcnv-late")
	require.ErrorContains(t, err, "refusing to pin")
	assert.Empty(t, rec.promoted)
	assert.Empty(t, k8s.scaled)
}

func TestPromoteAndRetireTargetsRefusesWhenSecondScanFindsWorkflow(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.pinUnversionedWorkflows = true
	lists := 0
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "B", nil
	}
	opts.listUnversioned = func(context.Context, string) ([]string, error) {
		lists++
		if lists == 1 {
			return nil, nil
		}
		return []string{"gcnv-late"}, nil
	}
	opts.pinUnversioned = func(context.Context, string, string, string, time.Duration) error {
		t.Fatal("must not pin to the old build when current is already the new build")
		return nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "gcnv-late")
	require.ErrorContains(t, err, "refusing to pin")
	assert.Equal(t, 2, lists)
	assert.Empty(t, rec.promoted)
	assert.Empty(t, k8s.scaled)
}

func TestPromoteAndRetireTargetsSkipsPinWhenFlagDisabled(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "", nil
	}
	opts.pinUnversioned = func(context.Context, string, string, string, time.Duration) error {
		t.Fatal("pin must not run when the flag is off")
		return nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"B"}, rec.promoted)
}

func TestPromoteAndRetireTargetsPinsUnversionedBeforePromote(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	var order []string
	opts := promoteTestOpts(rec, false, false)
	opts.pinUnversionedWorkflows = true
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "", nil
	}
	opts.promote = func(_ context.Context, _ workerdeployment.Client, _ string, buildID string) error {
		order = append(order, "promote:"+buildID)
		rec.promoted = append(rec.promoted, buildID)
		return nil
	}
	opts.pinUnversioned = func(_ context.Context, taskQueue, deploymentName, oldBuildID string, _ time.Duration) error {
		order = append(order, "pin:"+oldBuildID)
		assert.Equal(t, "vsa-lifecycle-manager-9.18.1P3", taskQueue)
		assert.Equal(t, "vsa-lifecycle-manager-9-18-1P3", deploymentName)
		return nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"promote:A", "pin:A", "promote:B"}, order)
}

func TestPromoteAndRetireTargetsDoesNotPromoteWhenPinFails(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.pinUnversionedWorkflows = true
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "A", nil
	}
	opts.pinUnversioned = func(context.Context, string, string, string, time.Duration) error {
		return errors.New("pin failed")
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "pin failed")
	assert.Empty(t, rec.promoted)
	assert.Empty(t, k8s.scaled)
}

func TestListUnversionedWorkflowIDs(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		pages: []*workflowservice.ListWorkflowExecutionsResponse{
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-1", RunId: "run-1"}},
				},
				NextPageToken: []byte("next"),
			},
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-2", RunId: "run-2"}},
				},
			},
		},
	}

	ids, err := listUnversionedWorkflowIDs(context.Background(), pinner, "vsa-lifecycle-manager-9.19.1P2")

	require.NoError(t, err)
	assert.Equal(t, []string{"gcnv-1", "gcnv-2"}, ids)
	require.NotEmpty(t, pinner.requests)
	assert.Contains(t, pinner.requests[0].GetQuery(), `TemporalWorkflowVersioningBehavior IS NULL`)
}

func TestPinUnversionedWorkflowsPinsEachRunningWorkflow(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		pages: []*workflowservice.ListWorkflowExecutionsResponse{
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-1", RunId: "run-1"}},
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-closed", RunId: "run-closed"}},
				},
				NextPageToken: []byte("next"),
			},
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-2", RunId: "run-2"}},
				},
			},
			{},
			{},
		},
		notFound: map[string]bool{"gcnv-closed": true},
	}

	err := pinUnversionedWorkflows(
		context.Background(),
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"gcnv-1", "gcnv-2"}, pinner.pinned)
	require.NotEmpty(t, pinner.requests)
	assert.Contains(t, pinner.requests[0].GetQuery(), `TaskQueue="vsa-lifecycle-manager-9.19.1P2"`)
	assert.Contains(t, pinner.requests[0].GetQuery(), `TemporalWorkflowVersioningBehavior IS NULL`)
	assert.NotContains(t, pinner.requests[0].GetQuery(), `BuildIds=`)
	assert.Equal(t, "vsa-lifecycle-manager-9-19-1P2", pinner.version.DeploymentName)
	assert.Equal(t, "8258295", pinner.version.BuildID)
}

func TestPinUnversionedWorkflowsRetriesFailedWorkflow(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		failTimes: map[string]int{"gcnv-bad": 1},
		pages: []*workflowservice.ListWorkflowExecutionsResponse{
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-ok", RunId: "run-ok"}},
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-bad", RunId: "run-bad"}},
				},
			},
			{
				Executions: []*workflowpb.WorkflowExecutionInfo{
					{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-bad", RunId: "run-bad"}},
				},
			},
			{},
			{},
		},
	}

	err := pinUnversionedWorkflows(
		context.Background(),
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"gcnv-ok", "gcnv-bad"}, pinner.pinned)
}

func TestPinUnversionedWorkflowsFailsWhenPinKeepsFailing(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		failTimes: map[string]int{"gcnv-stuck": 100},
		repeatPage: &workflowservice.ListWorkflowExecutionsResponse{
			Executions: []*workflowpb.WorkflowExecutionInfo{
				{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-ok", RunId: "run-ok"}},
				{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-stuck", RunId: "run-stuck"}},
			},
		},
	}

	err := pinUnversionedWorkflows(
		context.Background(),
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.ErrorContains(t, err, "failed after 3 retries")
	assert.Contains(t, pinner.pinned, "gcnv-ok")
	assert.NotContains(t, pinner.pinned, "gcnv-stuck")
	assert.Equal(t, temporalMaxRetries+1, len(pinner.requests))
}

func TestPinUnversionedWorkflowsRetriesListError(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		listFailTimes: 1,
		pages: []*workflowservice.ListWorkflowExecutionsResponse{
			{},
			{},
		},
	}

	err := pinUnversionedWorkflows(
		context.Background(),
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.NoError(t, err)
	assert.Empty(t, pinner.pinned)
	assert.GreaterOrEqual(t, len(pinner.requests), 3)
}

func TestPinUnversionedWorkflowsStopsWhenListKeepsFailing(t *testing.T) {
	pinner := &fakeWorkflowPinner{listFailTimes: 100}

	err := pinUnversionedWorkflows(
		context.Background(),
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.ErrorContains(t, err, "list unversioned workflows")
	assert.Empty(t, pinner.pinned)
	assert.Equal(t, temporalMaxRetries+1, len(pinner.requests))
}

func TestPinUnversionedWorkflowsStopsOnTimeout(t *testing.T) {
	pinner := &fakeWorkflowPinner{
		repeatPage: &workflowservice.ListWorkflowExecutionsResponse{
			Executions: []*workflowpb.WorkflowExecutionInfo{
				{Execution: &commonpb.WorkflowExecution{WorkflowId: "gcnv-late", RunId: "run-late"}},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := pinUnversionedWorkflows(
		ctx,
		pinner,
		"vsa-lifecycle-manager-9.19.1P2",
		"vsa-lifecycle-manager-9-19-1P2",
		"8258295",
		time.Millisecond,
	)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotEmpty(t, pinner.pinned)
}

type fakeWorkflowPinner struct {
	pages         []*workflowservice.ListWorkflowExecutionsResponse
	repeatPage    *workflowservice.ListWorkflowExecutionsResponse
	notFound      map[string]bool
	failTimes     map[string]int
	listFailTimes int
	requests      []*workflowservice.ListWorkflowExecutionsRequest
	pinned        []string
	version       worker.WorkerDeploymentVersion
}

func (f *fakeWorkflowPinner) ListWorkflow(_ context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	f.requests = append(f.requests, request)
	if f.listFailTimes > 0 {
		f.listFailTimes--
		return nil, errors.New("stream terminated by RST_STREAM with error code: CANCEL")
	}
	if f.repeatPage != nil {
		return f.repeatPage, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeWorkflowPinner) UpdateWorkflowExecutionOptions(_ context.Context, options client.UpdateWorkflowExecutionOptionsRequest) (client.WorkflowExecutionOptions, error) {
	if f.notFound[options.WorkflowId] {
		return client.WorkflowExecutionOptions{}, &serviceerror.NotFound{Message: "gone"}
	}
	if remaining := f.failTimes[options.WorkflowId]; remaining > 0 {
		f.failTimes[options.WorkflowId] = remaining - 1
		return client.WorkflowExecutionOptions{}, errors.New("pin failed")
	}
	change := options.WorkflowExecutionOptionsChanges.VersioningOverride
	pinned, ok := change.Value.(*client.PinnedVersioningOverride)
	if !ok {
		return client.WorkflowExecutionOptions{}, errors.New("expected pinned override")
	}
	f.version = pinned.Version
	f.pinned = append(f.pinned, options.WorkflowId)
	return client.WorkflowExecutionOptions{}, nil
}

func TestPromoteAndRetireTargetsScalesDrainToZeroAfterDrain(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		promoteTestOpts(rec, false, false),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"B"}, rec.promoted)
	assert.Equal(t, []string{"A"}, rec.drained)
	assert.Equal(t, []string{"drain-deployment"}, k8s.scaled)
	assert.Equal(t, []int{0}, k8s.scaledTo)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
	assert.Empty(t, rec.deleted)
}

func TestPromoteAndRetireTargetsDeletesBuildOnlyWhenEnabled(t *testing.T) {
	tests := []struct {
		name        string
		dryRun      bool
		deleteAfter bool
		wantDeleted []string
	}{
		{name: "delete after drain", deleteAfter: true, wantDeleted: []string{"A"}},
		{name: "keep version after drain", deleteAfter: false},
		{name: "dry-run never deletes", dryRun: true, deleteAfter: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
			rec := &promoteRecorder{}

			err := promoteAndRetireTargets(
				context.Background(),
				k8s,
				nil,
				[]RolloutTarget{validRolloutTarget()},
				promoteTestOpts(rec, tc.dryRun, tc.deleteAfter),
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			require.NoError(t, err)
			assert.Equal(t, tc.wantDeleted, rec.deleted)
		})
	}
}

func TestPromoteAndRetireTargetsFailsWhenCutoverStateCannotBeRead(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) {
		return "", errors.New("temporal unavailable")
	}
	opts.buildDrained = func(context.Context, workerdeployment.Client, string, string) (bool, error) {
		return false, nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "read cutover state")
	assert.Empty(t, rec.promoted)
	assert.Empty(t, k8s.recorded)
}

func TestPromoteAndRetireTargetsFailsWhenFinishedCutoverCannotBeRecorded(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}, recordErr: errors.New("configmap write failed")}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) { return "B", nil }
	opts.buildDrained = func(context.Context, workerdeployment.Client, string, string) (bool, error) { return true, nil }

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "record finished cutover")
	assert.Empty(t, rec.promoted)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
}

func TestPromoteAndRetireTargetsFailsWhenRecordAfterScaleFails(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}, recordErr: errors.New("configmap write failed")}
	rec := &promoteRecorder{}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		promoteTestOpts(rec, false, false),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "record finished cutover")
	assert.Equal(t, []string{"B"}, rec.promoted)
	assert.Equal(t, []int{0}, k8s.scaledTo)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
}

func TestPromoteAndRetireTargetsContinuesAfterTargetFailure(t *testing.T) {
	first := validRolloutTarget()
	first.OntapVersion = "9.18.1P3"
	first.OldBuildID = "A1"
	first.NewBuildID = "B1"
	first.DrainDeployment = "drain-a1"

	second := validRolloutTarget()
	second.OntapVersion = "9.19.1"
	second.OldBuildID = "A2"
	second.NewBuildID = "B2"
	second.DrainDeployment = "drain-a2"

	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{drainErr: map[string]error{"A1": errors.New("drain timeout")}}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{first, second},
		promoteTestOpts(rec, false, true),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"B1", "B2"}, rec.promoted)
	assert.Equal(t, []string{"A2"}, rec.drained)
	assert.Equal(t, []string{"drain-a2"}, k8s.scaled)
	assert.Equal(t, []string{"A2"}, rec.deleted)
}

func TestShutdownIstioSidecar(t *testing.T) {
	original := istioQuitPorts
	t.Cleanup(func() { istioQuitPorts = original })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	path := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	parsed, err := url.Parse(srv.URL)
	require.NoError(t, err)
	istioQuitPorts = []string{parsed.Port()}

	shutdownIstioSidecar(log)
	select {
	case got := <-path:
		assert.Equal(t, "/quitquitquit", got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for sidecar shutdown request")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := ln.Addr().String()
	require.NoError(t, ln.Close())
	_, port, err := net.SplitHostPort(closed)
	require.NoError(t, err)
	istioQuitPorts = []string{port}
	shutdownIstioSidecar(log)
}

func TestPromoteAndRetireTargetsHandsDrainToJob(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	var handedOff int
	opts.handoffDrain = func(context.Context, []RolloutTarget) error {
		handedOff++
		return nil
	}

	err := promoteAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, 1, handedOff)
	assert.Equal(t, []string{"B"}, rec.promoted)
	assert.Empty(t, rec.drained)
	assert.Empty(t, k8s.scaled)
	assert.Empty(t, k8s.recorded)
}

func TestDrainAndRetireTargetsWaitsScalesAndRecords(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) { return "B", nil }
	opts.buildDrained = func(context.Context, workerdeployment.Client, string, string) (bool, error) { return false, nil }

	err := drainAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"A"}, rec.drained)
	assert.Equal(t, []int{0}, k8s.scaledTo)
	assert.Equal(t, []string{"9.18.1P3__B__A"}, k8s.recorded)
}

func TestDrainAndRetireTargetsSkipsBuildThatIsNotCurrent(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) { return "A", nil }

	err := drainAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Empty(t, rec.drained)
	assert.Empty(t, k8s.scaled)
}

func TestDrainAndRetireTargetsFailsWhenDrainWaitFails(t *testing.T) {
	k8s := &retireK8s{readyK8s: readyK8s{ready: true, replicas: 1}}
	rec := &promoteRecorder{drainErr: map[string]error{"A": errors.New("drain timeout")}}
	opts := promoteTestOpts(rec, false, false)
	opts.currentBuild = func(context.Context, workerdeployment.Client, string) (string, error) { return "B", nil }
	opts.buildDrained = func(context.Context, workerdeployment.Client, string, string) (bool, error) { return false, nil }

	err := drainAndRetireTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		opts,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorContains(t, err, "drain timeout")
	assert.Empty(t, k8s.scaled)
}

func TestEnsureBackgroundDrainJobCreatesWhenMissing(t *testing.T) {
	var created map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&created))
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	k8s := &k8sClient{http: server.Client(), token: "token", namespace: "vlm", baseURL: server.URL}
	t.Setenv("DRAIN_TIMEOUT_MINUTES", "360")
	err := k8s.ensureBackgroundDrainJob(context.Background(), "vlm-worker-versioning-drain-a", "rollout:test", "vlm-worker-ksa", []jobEnv{
		{Name: "MODE", Value: modeDrainAndRetire},
	})

	require.NoError(t, err)
	spec := created["spec"].(map[string]any)
	assert.Equal(t, float64(drainJobBackoffLimit), spec["backoffLimit"])
	template := spec["template"].(map[string]any)
	pod := template["spec"].(map[string]any)
	containers := pod["containers"].([]any)
	container := containers[0].(map[string]any)
	assert.Equal(t, "rollout:test", container["image"])
	env := container["env"].([]any)
	assert.Equal(t, modeDrainAndRetire, env[0].(map[string]any)["value"])
}

func TestEnsureBackgroundDrainJobLeavesActiveJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":{"active":1}}`))
	}))
	t.Cleanup(server.Close)

	k8s := &k8sClient{http: server.Client(), token: "token", namespace: "vlm", baseURL: server.URL}
	require.NoError(t, k8s.ensureBackgroundDrainJob(context.Background(), "vlm-worker-versioning-drain-a", "rollout:test", "vlm-worker-ksa", nil))
}

func TestEnsureBackgroundDrainJobReplacesFailedJob(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodGet:
			if len(methods) == 1 {
				w.WriteHeader(http.StatusOK)
				// backoffLimit is exceeded: the control plane sets the Failed
				// condition once no more pods will be started.
				_, _ = w.Write([]byte(`{"status":{"failed":1,"conditions":[{"type":"Failed","status":"True"}]}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			assert.Equal(t, "Background", r.URL.Query().Get("propagationPolicy"))
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	k8s := &k8sClient{http: server.Client(), token: "token", namespace: "vlm", baseURL: server.URL}
	require.NoError(t, k8s.ensureBackgroundDrainJob(context.Background(), "vlm-worker-versioning-drain-a", "rollout:test", "vlm-worker-ksa", nil))
	assert.Equal(t, []string{http.MethodGet, http.MethodDelete, http.MethodGet, http.MethodPost}, methods)
}

// TestEnsureBackgroundDrainJobLeavesRetryingJobInPlace is the regression case
// for drainJobBackoffLimit > 0: a pod has failed (status.failed=1) but the
// control plane is still retrying (status.active=1, no Failed condition yet).
// Deleting this Job would kill a replacement pod that is actively watching
// drainage, forcing the wait to restart from zero on another node.
func TestEnsureBackgroundDrainJobLeavesRetryingJobInPlace(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":{"failed":1,"active":1}}`))
	}))
	t.Cleanup(server.Close)

	k8s := &k8sClient{http: server.Client(), token: "token", namespace: "vlm", baseURL: server.URL}
	require.NoError(t, k8s.ensureBackgroundDrainJob(context.Background(), "vlm-worker-versioning-drain-a", "rollout:test", "vlm-worker-ksa", nil))
}

func TestRetryingPromoteDoesNotSetCurrentAgainAfterLostSuccess(t *testing.T) {
	handle := &promoteHandle{lostSuccess: true}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := retryingPromote(log, time.Millisecond)(context.Background(), promoteClient{handle: handle}, "dep", "8314229")

	require.NoError(t, err)
	assert.Equal(t, 1, handle.setCalls)
	assert.Equal(t, "8314229", handle.current)
}

func TestRetryingPromoteRetriesDescribeWithoutSetCurrent(t *testing.T) {
	handle := &promoteHandle{describeErrs: []error{errors.New("temporal unavailable")}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := retryingPromote(log, time.Millisecond)(context.Background(), promoteClient{handle: handle}, "dep", "8314229")

	require.NoError(t, err)
	assert.Equal(t, 1, handle.setCalls)
}

type promoteClient struct {
	handle client.WorkerDeploymentHandle
}

func (c promoteClient) GetHandle(string) client.WorkerDeploymentHandle { return c.handle }

func (c promoteClient) DescribeTaskQueuePollers(context.Context, string, string, string) ([]workerdeployment.PollerInfo, error) {
	return nil, nil
}

type versionResult struct {
	err     error
	drained bool
}

type promoteHandle struct {
	current        string
	describeErrs   []error
	setCalls       int
	lostSuccess    bool
	versionCalls   int
	versionResults []versionResult
}

func (h *promoteHandle) Describe(context.Context, client.WorkerDeploymentDescribeOptions) (client.WorkerDeploymentDescribeResponse, error) {
	if len(h.describeErrs) > 0 {
		err := h.describeErrs[0]
		h.describeErrs = h.describeErrs[1:]
		if err != nil {
			return client.WorkerDeploymentDescribeResponse{}, err
		}
	}
	resp := client.WorkerDeploymentDescribeResponse{}
	if h.current != "" {
		resp.Info.RoutingConfig.CurrentVersion = &worker.WorkerDeploymentVersion{BuildID: h.current}
	}
	return resp, nil
}

func (h *promoteHandle) SetCurrentVersion(_ context.Context, options client.WorkerDeploymentSetCurrentVersionOptions) (client.WorkerDeploymentSetCurrentVersionResponse, error) {
	h.setCalls++
	h.current = options.BuildID
	if h.lostSuccess {
		h.lostSuccess = false
		return client.WorkerDeploymentSetCurrentVersionResponse{}, errors.New("lost response")
	}
	return client.WorkerDeploymentSetCurrentVersionResponse{}, nil
}

func (h *promoteHandle) SetRampingVersion(context.Context, client.WorkerDeploymentSetRampingVersionOptions) (client.WorkerDeploymentSetRampingVersionResponse, error) {
	return client.WorkerDeploymentSetRampingVersionResponse{}, nil
}

func (h *promoteHandle) SetManagerIdentity(context.Context, client.WorkerDeploymentSetManagerIdentityOptions) (client.WorkerDeploymentSetManagerIdentityResponse, error) {
	return client.WorkerDeploymentSetManagerIdentityResponse{}, nil
}

func (h *promoteHandle) DescribeVersion(context.Context, client.WorkerDeploymentDescribeVersionOptions) (client.WorkerDeploymentVersionDescription, error) {
	h.versionCalls++
	if len(h.versionResults) == 0 {
		return client.WorkerDeploymentVersionDescription{}, nil
	}
	result := h.versionResults[0]
	h.versionResults = h.versionResults[1:]
	if result.err != nil {
		return client.WorkerDeploymentVersionDescription{}, result.err
	}
	if !result.drained {
		return client.WorkerDeploymentVersionDescription{}, nil
	}
	return client.WorkerDeploymentVersionDescription{
		Info: client.WorkerDeploymentVersionInfo{
			DrainageInfo: &client.WorkerDeploymentVersionDrainageInfo{
				DrainageStatus: client.WorkerDeploymentVersionDrainageStatusDrained,
			},
		},
	}, nil
}

func (h *promoteHandle) DeleteVersion(context.Context, client.WorkerDeploymentDeleteVersionOptions) (client.WorkerDeploymentDeleteVersionResponse, error) {
	return client.WorkerDeploymentDeleteVersionResponse{}, nil
}

func (h *promoteHandle) UpdateVersionMetadata(context.Context, client.WorkerDeploymentUpdateVersionMetadataOptions) (client.WorkerDeploymentUpdateVersionMetadataResponse, error) {
	return client.WorkerDeploymentUpdateVersionMetadataResponse{}, nil
}

func TestRetryingWaitForDrainRetriesEachDescribe(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unavailable := errors.New("temporal unavailable")
	handle := &promoteHandle{versionResults: []versionResult{
		{err: unavailable},
		{},
		{err: unavailable},
		{},
		{err: unavailable},
		{},
		{err: unavailable},
		{},
		{drained: true},
	}}

	err := retryingWaitForDrain(log)(context.Background(), promoteClient{handle: handle}, "dep", "A", time.Millisecond)

	require.NoError(t, err)
	assert.Equal(t, 9, handle.versionCalls)
}

func TestRetryingWaitForDrainStopsWhenDescribeKeepsFailing(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unavailable := errors.New("temporal unavailable")
	handle := &promoteHandle{versionResults: []versionResult{
		{err: unavailable},
		{err: unavailable},
		{err: unavailable},
		{err: unavailable},
		{drained: true},
	}}

	err := retryingWaitForDrain(log)(context.Background(), promoteClient{handle: handle}, "dep", "A", time.Millisecond)

	require.ErrorContains(t, err, "temporal unavailable")
	assert.Equal(t, temporalMaxRetries+1, handle.versionCalls)
}

func TestRetryTemporalRetriesUntilSuccess(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var calls int
	err := retryTemporal(context.Background(), time.Millisecond, log, "describe deployment", func() error {
		calls++
		if calls < 3 {
			return errors.New("temporal unavailable")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, calls)
}

func TestRetryTemporalStopsAfterThreeRetries(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var calls int
	err := retryTemporal(context.Background(), time.Millisecond, log, "describe deployment", func() error {
		calls++
		return errors.New("temporal unavailable")
	})

	require.ErrorContains(t, err, "temporal unavailable")
	assert.Equal(t, temporalMaxRetries+1, calls)
}

func TestRetryTemporalStopsWhenContextEnds(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	err := retryTemporal(ctx, time.Hour, log, "describe deployment", func() error {
		cancel()
		return errors.New("temporal unavailable")
	})

	require.ErrorContains(t, err, "temporal unavailable")
}

func TestDrainJobNameForUsesOldBuild(t *testing.T) {
	target := validRolloutTarget()
	assert.Equal(t, "vlm-worker-versioning-drain-a", drainJobNameFor([]RolloutTarget{target}))
}

func validRolloutTarget() RolloutTarget {
	return RolloutTarget{
		OntapVersion:    "9.18.1P3",
		TaskQueue:       "vsa-lifecycle-manager-9.18.1P3",
		DeploymentName:  "vsa-lifecycle-manager-9-18-1P3",
		NewBuildID:      "B",
		OldBuildID:      "A",
		DrainDeployment: "drain-deployment",
		DrainReplicas:   1,
		ReleaseName:     "release",
	}
}

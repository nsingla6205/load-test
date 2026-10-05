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

func TestNewK8sClientRequiresNamespace(t *testing.T) {
	_, err := newK8sClient("  ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace is required")
}

func TestValidateEnsureDrainTarget(t *testing.T) {
	target := validRolloutTarget()
	require.NoError(t, validateEnsureDrainTarget(target))

	target.DrainImage = ""
	assert.ErrorContains(t, validateEnsureDrainTarget(target), "drain image is required")
}

type ensureK8s struct {
	readyK8s
	ensureErr error
	ensured   []string
}

func (f *ensureK8s) ensureDrainResources(_ context.Context, target RolloutTarget) error {
	f.ensured = append(f.ensured, target.DrainDeployment)
	return f.ensureErr
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
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"drain-deployment"}, k8s.ensured)
	assert.Equal(t, []string{"A"}, waitedBuilds)
}

func TestEnsureDrainTargetsStopsWhenResourcesFail(t *testing.T) {
	ensureErr := errors.New("Kubernetes unavailable")
	k8s := &ensureK8s{
		readyK8s:  readyK8s{ready: true, replicas: 1},
		ensureErr: ensureErr,
	}

	err := ensureDrainTargets(
		context.Background(),
		k8s,
		nil,
		[]RolloutTarget{validRolloutTarget()},
		time.Millisecond,
		time.Second,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	require.ErrorIs(t, err, ensureErr)
}

func TestEnsureDrainResourcesCreatesConfigMapAndPatchesDeployment(t *testing.T) {
	var requests []struct {
		method      string
		path        string
		contentType string
		body        map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests = append(requests, struct {
			method      string
			path        string
			contentType string
			body        map[string]any
		}{method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: body})

		if r.Method == http.MethodPatch {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	k8s := &k8sClient{
		http:      server.Client(),
		token:     "token",
		namespace: "default",
		baseURL:   server.URL,
	}
	require.NoError(t, k8s.ensureDrainResources(context.Background(), validRolloutTarget()))

	require.Len(t, requests, 4)
	assert.Equal(t, http.MethodPatch, requests[0].method)
	assert.Equal(t, "/api/v1/namespaces/default/configmaps/drain-config", requests[0].path)
	assert.Equal(t, "application/merge-patch+json", requests[0].contentType)
	assert.Equal(t, http.MethodPost, requests[1].method)
	assert.Equal(t, "/api/v1/namespaces/default/configmaps", requests[1].path)
	assert.Equal(t, http.MethodPatch, requests[2].method)
	assert.Equal(t, "/apis/apps/v1/namespaces/default/deployments/drain-deployment", requests[2].path)
	assert.Equal(t, http.MethodPost, requests[3].method)
	assert.Equal(t, "/apis/apps/v1/namespaces/default/deployments", requests[3].path)

	configMetadata := requests[1].body["metadata"].(map[string]any)
	configAnnotations := configMetadata["annotations"].(map[string]any)
	assert.Equal(t, "release", configAnnotations["meta.helm.sh/release-name"])

	deploymentMetadata := requests[3].body["metadata"].(map[string]any)
	deploymentLabels := deploymentMetadata["labels"].(map[string]any)
	assert.Equal(t, "Helm", deploymentLabels["app.kubernetes.io/managed-by"])

	deploymentSpec := requests[3].body["spec"].(map[string]any)
	strategy := deploymentSpec["strategy"].(map[string]any)
	rollingUpdate := strategy["rollingUpdate"].(map[string]any)
	assert.Equal(t, float64(0), rollingUpdate["maxUnavailable"])
	podTemplate := deploymentSpec["template"].(map[string]any)
	podMetadata := podTemplate["metadata"].(map[string]any)
	podLabels := podMetadata["labels"].(map[string]any)
	assert.NotContains(t, podLabels, "app.kubernetes.io/managed-by")
	podSpec := podTemplate["spec"].(map[string]any)
	containers := podSpec["containers"].([]any)
	container := containers[0].(map[string]any)
	assert.Equal(t, "registry/vlm@sha256:old", container["image"])
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

type retireK8s struct {
	readyK8s
	scaled   []string
	scaledTo []int
	scaleErr error
}

func (f *retireK8s) scaleDeployment(_ context.Context, name string, replicas int) error {
	f.scaled = append(f.scaled, name)
	f.scaledTo = append(f.scaledTo, replicas)
	return f.scaleErr
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

func validRolloutTarget() RolloutTarget {
	return RolloutTarget{
		OntapVersion:         "9.18.1P3",
		TaskQueue:            "vsa-lifecycle-manager-9.18.1P3",
		DeploymentName:       "vsa-lifecycle-manager-9-18-1P3",
		NewBuildID:           "B",
		OldBuildID:           "A",
		DrainDeployment:      "drain-deployment",
		DrainConfigMap:       "drain-config",
		DrainImage:           "registry/vlm@sha256:old",
		DrainImagePullPolicy: "IfNotPresent",
		DrainReplicas:        1,
		ServiceAccountName:   "vlm-worker-ksa",
		SecretName:           "vlm-worker-secret",
		Resources: map[string]map[string]string{
			"requests": {"cpu": "100m", "memory": "128Mi"},
			"limits":   {"cpu": "500m", "memory": "256Mi"},
		},
		ConfigData: map[string]string{
			"TASK_QUEUE":                   "vsa-lifecycle-manager-9.18.1P3",
			"TEMPORAL_WORKER_BUILD_NUMBER": "A",
		},
		ReleaseName:      "release",
		ReleaseNamespace: "default",
	}
}

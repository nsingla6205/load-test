package workerdeployment

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type mockHandle struct {
	describeResp        client.WorkerDeploymentDescribeResponse
	describeErr         error
	describeVersionResp client.WorkerDeploymentVersionDescription
	describeVersionErr  error
	setCurrentErr       error
	deleteErr           error
	setCurrentCalls     int
	lastSetBuildID      string
}

func (m *mockHandle) Describe(context.Context, client.WorkerDeploymentDescribeOptions) (client.WorkerDeploymentDescribeResponse, error) {
	return m.describeResp, m.describeErr
}
func (m *mockHandle) SetCurrentVersion(_ context.Context, opts client.WorkerDeploymentSetCurrentVersionOptions) (client.WorkerDeploymentSetCurrentVersionResponse, error) {
	m.setCurrentCalls++
	m.lastSetBuildID = opts.BuildID
	if m.setCurrentErr != nil {
		return client.WorkerDeploymentSetCurrentVersionResponse{}, m.setCurrentErr
	}
	cur := worker.WorkerDeploymentVersion{BuildID: opts.BuildID}
	if existing := m.describeResp.Info.RoutingConfig.CurrentVersion; existing != nil {
		cur.DeploymentName = existing.DeploymentName
	}
	m.describeResp.Info.RoutingConfig.CurrentVersion = &cur
	return client.WorkerDeploymentSetCurrentVersionResponse{}, nil
}
func (m *mockHandle) SetRampingVersion(context.Context, client.WorkerDeploymentSetRampingVersionOptions) (client.WorkerDeploymentSetRampingVersionResponse, error) {
	panic("unexpected")
}
func (m *mockHandle) SetManagerIdentity(context.Context, client.WorkerDeploymentSetManagerIdentityOptions) (client.WorkerDeploymentSetManagerIdentityResponse, error) {
	panic("unexpected")
}
func (m *mockHandle) DescribeVersion(context.Context, client.WorkerDeploymentDescribeVersionOptions) (client.WorkerDeploymentVersionDescription, error) {
	return m.describeVersionResp, m.describeVersionErr
}
func (m *mockHandle) DeleteVersion(context.Context, client.WorkerDeploymentDeleteVersionOptions) (client.WorkerDeploymentDeleteVersionResponse, error) {
	return client.WorkerDeploymentDeleteVersionResponse{}, m.deleteErr
}
func (m *mockHandle) UpdateVersionMetadata(context.Context, client.WorkerDeploymentUpdateVersionMetadataOptions) (client.WorkerDeploymentUpdateVersionMetadataResponse, error) {
	panic("unexpected")
}

type mockWDC struct {
	handle            client.WorkerDeploymentHandle
	pollers           []PollerInfo
	taskQueueErr      error
	taskQueueRequests []struct {
		taskQueue      string
		deploymentName string
		buildID        string
	}
}

func (m *mockWDC) GetHandle(string) client.WorkerDeploymentHandle { return m.handle }
func (m *mockWDC) DescribeTaskQueuePollers(
	_ context.Context,
	taskQueue string,
	deploymentName string,
	buildID string,
) ([]PollerInfo, error) {
	m.taskQueueRequests = append(m.taskQueueRequests, struct {
		taskQueue      string
		deploymentName string
		buildID        string
	}{taskQueue: taskQueue, deploymentName: deploymentName, buildID: buildID})
	return m.pollers, m.taskQueueErr
}

func poller(deploymentName, buildID string, lastAccessTime time.Time) []PollerInfo {
	return []PollerInfo{{
		DeploymentName: deploymentName,
		BuildID:        buildID,
		LastAccessTime: lastAccessTime,
	}}
}

func TestPollersFromTaskQueueDescriptionReadsWorkerDeploymentPollers(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	desc := client.TaskQueueDescription{}
	versions := reflect.ValueOf(&desc).Elem().FieldByName("VersionsInfo")
	versionInfoType := versions.Type().Elem()
	versionInfo := reflect.New(versionInfoType).Elem()
	typesInfo := versionInfo.FieldByName("TypesInfo")
	typeInfoType := typesInfo.Type().Elem()
	typeInfo := reflect.New(typeInfoType).Elem()
	pollerValue := reflect.New(typeInfo.FieldByName("Pollers").Type().Elem()).Elem()
	pollerValue.FieldByName("LastAccessTime").Set(reflect.ValueOf(now))
	optionsField := pollerValue.FieldByName("WorkerDeploymentPollerOptions")
	options := reflect.New(optionsField.Type().Elem())
	options.Elem().FieldByName("DeploymentName").SetString("dep")
	options.Elem().FieldByName("BuildID").SetString("A")
	optionsField.Set(options)
	pollers := reflect.MakeSlice(typeInfo.FieldByName("Pollers").Type(), 1, 1)
	pollers.Index(0).Set(pollerValue)
	typeInfo.FieldByName("Pollers").Set(pollers)
	typesMap := reflect.MakeMap(typesInfo.Type())
	typesMap.SetMapIndex(reflect.ValueOf(client.TaskQueueTypeWorkflow).Convert(typesMap.Type().Key()), typeInfo)
	typesInfo.Set(typesMap)
	versionsMap := reflect.MakeMap(versions.Type())
	versionsMap.SetMapIndex(reflect.ValueOf("A"), versionInfo)
	versions.Set(versionsMap)

	got := pollersFromTaskQueueDescription(desc)
	require.Equal(t, []PollerInfo{{
		DeploymentName: "dep",
		BuildID:        "A",
		LastAccessTime: now,
	}}, got)
}

func TestResolveDeploymentName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "custom", ResolveDeploymentName("custom", "vsa-lifecycle-manager-9.18.1"))
	assert.Equal(t, "vsa-lifecycle-manager-9-18-1", ResolveDeploymentName("", "vsa-lifecycle-manager-9.18.1"))
	assert.Equal(t, DefaultDeploymentName, ResolveDeploymentName("", ""))
}

func TestVersionIDHelpers(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "dep.B", VersionID("dep", "B"))
	assert.Equal(t, "B", BuildIDFromVersion("dep.B"))
	assert.Equal(t, "", BuildIDFromVersion("__unversioned__"))
	assert.Equal(t, "", BuildIDFromVersion(""))
	assert.Equal(t, "B", BuildIDFromVersion("B"))
}

func TestPromoteAndPolling(t *testing.T) {
	t.Parallel()
	current := worker.WorkerDeploymentVersion{DeploymentName: "dep", BuildID: "A"}
	h := &mockHandle{
		describeResp: client.WorkerDeploymentDescribeResponse{
			Info: client.WorkerDeploymentInfo{
				RoutingConfig: client.WorkerDeploymentRoutingConfig{CurrentVersion: &current},
				VersionSummaries: []client.WorkerDeploymentVersionSummary{
					{Version: worker.WorkerDeploymentVersion{DeploymentName: "dep", BuildID: "A"}, CreateTime: time.Unix(1, 0)},
					{Version: worker.WorkerDeploymentVersion{DeploymentName: "dep", BuildID: "B"}, CreateTime: time.Unix(2, 0)},
				},
			},
		},
	}
	wdc := &mockWDC{
		handle:  h,
		pollers: poller("dep", "B", time.Now()),
	}

	require.NoError(t, PromoteBuild(context.Background(), wdc, "dep", "B"))
	assert.Equal(t, "B", h.lastSetBuildID)

	cur, err := CurrentBuildID(context.Background(), wdc, "dep")
	require.NoError(t, err)
	assert.Equal(t, "B", cur)

	ok, err := IsBuildPolling(context.Background(), wdc, "queue", "dep", "B", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)
	require.Len(t, wdc.taskQueueRequests, 1)
	assert.Equal(t, "queue", wdc.taskQueueRequests[0].taskQueue)
	assert.Equal(t, "dep", wdc.taskQueueRequests[0].deploymentName)
	assert.Equal(t, "B", wdc.taskQueueRequests[0].buildID)

	latest, err := LatestPolledBuildID(context.Background(), wdc, "dep")
	require.NoError(t, err)
	assert.Equal(t, "B", latest)

	h.setCurrentErr = errors.New("no pollers")
	assert.Error(t, PromoteBuild(context.Background(), wdc, "dep", "B"))
}

func TestIsBuildPollingRequiresFreshMatchingPoller(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pollers []PollerInfo
		want    bool
	}{
		{
			name:    "fresh matching poller",
			pollers: poller("dep", "A", time.Now()),
			want:    true,
		},
		{
			name:    "stale matching poller",
			pollers: poller("dep", "A", time.Now().Add(-3*time.Minute)),
			want:    false,
		},
		{
			name:    "fresh poller for another deployment",
			pollers: poller("other", "A", time.Now()),
			want:    false,
		},
		{
			name:    "fresh poller for another build",
			pollers: poller("dep", "B", time.Now()),
			want:    false,
		},
		{
			name: "version is tracked but has no pollers",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wdc := &mockWDC{
				handle:  &mockHandle{},
				pollers: tt.pollers,
			}

			got, err := IsBuildPolling(
				context.Background(),
				wdc,
				"queue",
				"dep",
				"A",
				DefaultPollerFreshness,
			)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsBuildDrainedAndWait(t *testing.T) {
	t.Parallel()
	h := &mockHandle{
		describeVersionResp: client.WorkerDeploymentVersionDescription{
			Info: client.WorkerDeploymentVersionInfo{
				DrainageInfo: &client.WorkerDeploymentVersionDrainageInfo{
					DrainageStatus: client.WorkerDeploymentVersionDrainageStatusDrained,
				},
			},
		},
		describeResp: client.WorkerDeploymentDescribeResponse{
			Info: client.WorkerDeploymentInfo{
				VersionSummaries: []client.WorkerDeploymentVersionSummary{
					{Version: worker.WorkerDeploymentVersion{DeploymentName: "dep", BuildID: "A"}},
				},
			},
		},
	}
	wdc := &mockWDC{
		handle:  h,
		pollers: poller("dep", "A", time.Now()),
	}

	ok, err := IsBuildDrained(context.Background(), wdc, "dep", "A")
	require.NoError(t, err)
	assert.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, WaitForDrain(ctx, wdc, "dep", "A", time.Millisecond))
	require.NoError(t, WaitForPolling(ctx, wdc, "queue", "dep", "A", time.Millisecond))
}

func TestWorkerDeploymentValidation(t *testing.T) {
	t.Parallel()
	wdc := &mockWDC{handle: &mockHandle{}}

	assert.Equal(t, "custom-name", ResolveDeploymentName(" custom.name ", "ignored"))
	assert.ErrorContains(t, PromoteBuild(context.Background(), wdc, "", "A"), "deployment name is required")
	assert.ErrorContains(t, PromoteBuild(context.Background(), wdc, "dep", ""), "build ID is required")

	_, err := CurrentBuildID(context.Background(), wdc, "")
	assert.ErrorContains(t, err, "deployment name is required")
	_, err = IsBuildPolling(context.Background(), wdc, "queue", "dep", "", time.Minute)
	assert.ErrorContains(t, err, "build ID is required")
	_, err = LatestPolledBuildID(context.Background(), wdc, "")
	assert.ErrorContains(t, err, "deployment name is required")
	_, err = IsBuildDrained(context.Background(), wdc, "", "A")
	assert.ErrorContains(t, err, "deployment name is required")
	assert.ErrorContains(t, DeleteBuild(context.Background(), wdc, "dep", ""), "build ID is required")
	assert.ErrorContains(t, WaitForPolling(context.Background(), wdc, "queue", "", "A", time.Millisecond), "deployment name is required")
	assert.ErrorContains(t, WaitForPolling(context.Background(), wdc, "", "dep", "A", time.Millisecond), "task queue is required")
	assert.ErrorContains(t, WaitForDrain(context.Background(), wdc, "dep", "", time.Millisecond), "build ID is required")
}

func TestWorkerDeploymentDescribeResults(t *testing.T) {
	t.Parallel()
	h := &mockHandle{}
	wdc := &mockWDC{handle: h}

	current, err := CurrentBuildID(context.Background(), wdc, "dep")
	require.NoError(t, err)
	assert.Empty(t, current)

	polling, err := IsBuildPolling(context.Background(), wdc, "queue", "dep", "A", time.Minute)
	require.NoError(t, err)
	assert.False(t, polling)

	latest, err := LatestPolledBuildID(context.Background(), wdc, "dep")
	require.NoError(t, err)
	assert.Empty(t, latest)

	drained, err := IsBuildDrained(context.Background(), wdc, "dep", "A")
	require.NoError(t, err)
	assert.False(t, drained)

	require.NoError(t, DeleteBuild(context.Background(), wdc, "dep", "A"))
}

func TestWorkerDeploymentAPIErrors(t *testing.T) {
	t.Parallel()
	apiErr := errors.New("temporal unavailable")
	h := &mockHandle{
		describeErr:        apiErr,
		describeVersionErr: apiErr,
		deleteErr:          apiErr,
	}
	wdc := &mockWDC{handle: h, taskQueueErr: apiErr}

	_, err := CurrentBuildID(context.Background(), wdc, "dep")
	assert.ErrorContains(t, err, "describe deployment")
	_, err = IsBuildPolling(context.Background(), wdc, "queue", "dep", "A", time.Minute)
	assert.ErrorContains(t, err, "describe task queue")
	_, err = LatestPolledBuildID(context.Background(), wdc, "dep")
	assert.ErrorContains(t, err, "describe deployment")
	_, err = IsBuildDrained(context.Background(), wdc, "dep", "A")
	assert.ErrorContains(t, err, "describe version")
	assert.ErrorContains(t, DeleteBuild(context.Background(), wdc, "dep", "A"), "delete version")
	assert.ErrorContains(t, WaitForPolling(context.Background(), wdc, "queue", "dep", "A", time.Millisecond), "describe task queue")
	assert.ErrorContains(t, WaitForDrain(context.Background(), wdc, "dep", "A", time.Millisecond), "describe version")
}

func TestWaitForBuildContextCancellation(t *testing.T) {
	t.Parallel()
	wdc := &mockWDC{handle: &mockHandle{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	assert.ErrorIs(t, WaitForPolling(ctx, wdc, "queue", "dep", "A", 0), context.Canceled)
	assert.ErrorIs(t, WaitForDrain(ctx, wdc, "dep", "A", 0), context.Canceled)
}

package leakedresources

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/model"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// pinLocalRegionEmpty forces env.Region to "" for the lifetime of the test
// so the pool detector's enumerator short-circuits with an error instead
// of trying to call ListAccountsForTelemetry / GetRegionZonesWorkflow on
// a mock storage that wasn't expecting either. Without this, CI runs that
// happen to have LOCAL_REGION set would fail the unrelated TestRun cases.
func pinLocalRegionEmpty(t *testing.T) {
	t.Helper()
	orig := env.Region
	env.Region = ""
	t.Cleanup(func() { env.Region = orig })
}

type mockDetector struct {
	mock.Mock
}

func (m *mockDetector) Name() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockDetector) Detect(ctx context.Context, storage database.Storage) ([]model.LeakRecord, error) {
	args := m.Called(ctx, storage)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]model.LeakRecord), args.Error(1)
}

type mockReporter struct {
	mock.Mock
}

func (m *mockReporter) Report(ctx context.Context, records []model.LeakRecord) error {
	args := m.Called(ctx, records)
	return args.Error(0)
}

func TestNewPipeline(t *testing.T) {
	p := NewPipeline()
	assert.NotNil(t, p)
	assert.Nil(t, p.registrations)
}

func TestPipeline_Register(t *testing.T) {
	p := NewPipeline()
	d := &mockDetector{}
	d.On("Name").Return("test_detector")
	p.Register(d, &mockReporter{})
	assert.Len(t, p.registrations, 1)
	p.Register(nil, &mockReporter{})
	assert.Len(t, p.registrations, 1)
	p.Register(&mockDetector{})
	assert.Len(t, p.registrations, 2)
}

// TestPipeline_Register_DefaultsToLogReporter verifies that registering a
// detector with no reporters (or only nil reporters) routes to a log-only
// LogReporter.
func TestPipeline_Register_DefaultsToLogReporter(t *testing.T) {
	p := NewPipeline()
	p.Register(&mockDetector{})
	assert.Len(t, p.registrations, 1)
	assert.IsType(t, LogReporter{}, p.registrations[0].reporter)

	p.Register(&mockDetector{}, nil, nil)
	assert.Len(t, p.registrations, 2)
	assert.IsType(t, LogReporter{}, p.registrations[1].reporter)
}

// TestCombineReporters covers the reporter routing rules used by Register.
func TestCombineReporters(t *testing.T) {
	single := &mockReporter{}
	assert.IsType(t, LogReporter{}, combineReporters())
	assert.IsType(t, LogReporter{}, combineReporters(nil, nil))
	assert.Equal(t, single, combineReporters(single))
	assert.IsType(t, &MultiReporter{}, combineReporters(single, &mockReporter{}))
}

func TestPipeline_Run_NoDetectors(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)

	p := NewPipeline()
	err := p.Run(ctx, storage)
	assert.NoError(t, err)
}

func TestPipeline_Run_DetectorReturnsRecords(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	records := []model.LeakRecord{
		{ResourceType: model.ResourceTypePool, ResourceID: "p1", Reason: "in_vcp_not_in_ccfe"},
	}
	det := &mockDetector{}
	det.On("Name").Return("pool")
	det.On("Detect", mock.Anything, storage).Return(records, nil)

	reporter := &mockReporter{}
	reporter.On("Report", mock.Anything, records).Return(nil)

	p := NewPipeline()
	p.Register(det, reporter)
	err := p.Run(ctx, storage)
	assert.NoError(t, err)
	det.AssertExpectations(t)
	reporter.AssertExpectations(t)
}

// TestPipeline_Run_DetectorFails_ContinuesAndReportsPerDetector verifies that a
// failing detector is skipped (its reporter is not called) while a healthy
// detector still reports its own records to the reporter it was registered with.
func TestPipeline_Run_DetectorFails_ContinuesAndReportsPerDetector(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	okRecords := []model.LeakRecord{{ResourceType: model.ResourceTypeVolume, ResourceID: "v1", Reason: "orphan"}}
	okDet := &mockDetector{}
	okDet.On("Name").Return("ok")
	okDet.On("Detect", mock.Anything, storage).Return(okRecords, nil)
	failDet := &mockDetector{}
	failDet.On("Name").Return("fail") // Pipeline calls Name() when logging detector failure
	failDet.On("Detect", mock.Anything, storage).Return(nil, errors.New("list failed"))

	reporter := &mockReporter{}
	reporter.On("Report", mock.Anything, okRecords).Return(nil).Once()

	p := NewPipeline()
	p.Register(okDet, reporter)
	p.Register(failDet, reporter)
	err := p.Run(ctx, storage)
	assert.NoError(t, err)
	okDet.AssertExpectations(t)
	failDet.AssertExpectations(t)
	reporter.AssertExpectations(t)
}

func TestPipeline_Run_ReporterFails_ReturnsError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	det := &mockDetector{}
	det.On("Name").Return("pool")
	det.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypePool, ResourceID: "p1", Reason: "in_vcp_not_in_ccfe"}}, nil)
	reporter := &mockReporter{}
	reporter.On("Report", mock.Anything, mock.Anything).Return(errors.New("report failed"))

	p := NewPipeline()
	p.Register(det, reporter)
	err := p.Run(ctx, storage)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "report failed")
}

// TestPipeline_Run_EmptyRecords_SkipsReporter verifies that a detector returning
// no records does not invoke its reporter (avoids noisy per-detector "no leaks"
// reporting); the per-run metrics reset still runs regardless.
func TestPipeline_Run_EmptyRecords_SkipsReporter(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	det := &mockDetector{}
	det.On("Name").Return("empty")
	det.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{}, nil)
	reporter := &mockReporter{}

	p := NewPipeline()
	p.Register(det, reporter)
	err := p.Run(ctx, storage)
	assert.NoError(t, err)
	det.AssertExpectations(t)
	reporter.AssertNotCalled(t, "Report", mock.Anything, mock.Anything)
}

// TestPipeline_Run_AccumulatesMetricsAcrossDetectors verifies the end-to-end
// per-run reset + per-detector accumulate behavior through Pipeline.Run using
// the real MetricsReporter: two detectors sharing one MetricsReporter both
// contribute to the gauge within a single run (accumulate, not clobber), and a
// subsequent run resets stale series (a resource no longer leaked drops off)
// while keeping the new run's records. This is the pool+volume scenario the
// design is built around.
func TestPipeline_Run_AccumulatesMetricsAcrossDetectors(t *testing.T) {
	resetLeakedCountsForTest()
	ctx := context.Background()
	storage := database.NewMockStorage(t)

	poolKey := leakedCountKey{resourceType: "pool", reason: "in_vcp_not_in_ccfe", resourceID: "pool-1"}
	volKey := leakedCountKey{resourceType: "volume", reason: "volume_orphan_pool_missing", resourceID: "vol-1"}

	// Run 1: pool and volume both leak; the shared metrics reporter must hold both.
	poolDet := &mockDetector{}
	poolDet.On("Name").Return("pool")
	poolDet.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypePool, ResourceID: "pool-1", Reason: "in_vcp_not_in_ccfe"}}, nil)
	volDet := &mockDetector{}
	volDet.On("Name").Return("volume")
	volDet.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypeVolume, ResourceID: "vol-1", Reason: "volume_orphan_pool_missing"}}, nil)

	metricsReporter := NewMetricsReporter()
	p1 := NewPipeline()
	p1.Register(poolDet, LogReporter{}, metricsReporter)
	p1.Register(volDet, LogReporter{}, metricsReporter)
	assert.NoError(t, p1.Run(ctx, storage))

	leakedMetricsMu.RLock()
	assert.Len(t, leakedCountsByKey, 2, "both detectors' records should coexist within a run")
	assert.Equal(t, int64(1), leakedCountsByKey[poolKey])
	assert.Equal(t, int64(1), leakedCountsByKey[volKey])
	leakedMetricsMu.RUnlock()

	// Run 2: pool no longer leaks (returns empty), volume still does. The per-run
	// reset must drop the stale pool series and keep only the volume series.
	poolDet2 := &mockDetector{}
	poolDet2.On("Name").Return("pool")
	poolDet2.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{}, nil)
	volDet2 := &mockDetector{}
	volDet2.On("Name").Return("volume")
	volDet2.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypeVolume, ResourceID: "vol-1", Reason: "volume_orphan_pool_missing"}}, nil)

	p2 := NewPipeline()
	p2.Register(poolDet2, LogReporter{}, metricsReporter)
	p2.Register(volDet2, LogReporter{}, metricsReporter)
	assert.NoError(t, p2.Run(ctx, storage))

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	assert.Len(t, leakedCountsByKey, 1, "stale pool series must be dropped after the per-run reset")
	assert.Equal(t, int64(1), leakedCountsByKey[volKey])
	assert.Zero(t, leakedCountsByKey[poolKey], "pool no longer leaks; its series must not persist")
}

// TestPipeline_Run_LogOnlyDetectorSkipsMetrics verifies per-detector routing:
// a detector registered log-only does not contribute to the metrics gauge,
// while a detector registered log+metrics does (the "volume log-only" scenario).
func TestPipeline_Run_LogOnlyDetectorSkipsMetrics(t *testing.T) {
	resetLeakedCountsForTest()
	ctx := context.Background()
	storage := database.NewMockStorage(t)

	poolKey := leakedCountKey{resourceType: "pool", reason: "in_vcp_not_in_ccfe", resourceID: "pool-1"}
	volKey := leakedCountKey{resourceType: "volume", reason: "volume_orphan_pool_missing", resourceID: "vol-1"}

	poolDet := &mockDetector{}
	poolDet.On("Name").Return("pool")
	poolDet.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypePool, ResourceID: "pool-1", Reason: "in_vcp_not_in_ccfe"}}, nil)
	volDet := &mockDetector{}
	volDet.On("Name").Return("volume")
	volDet.On("Detect", mock.Anything, storage).Return([]model.LeakRecord{{ResourceType: model.ResourceTypeVolume, ResourceID: "vol-1", Reason: "volume_orphan_pool_missing"}}, nil)

	metricsReporter := NewMetricsReporter()
	p := NewPipeline()
	p.Register(poolDet, LogReporter{}, metricsReporter) // log + metrics
	p.Register(volDet, LogReporter{})                   // log only

	assert.NoError(t, p.Run(ctx, storage))

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	assert.Len(t, leakedCountsByKey, 1, "only the log+metrics detector should reach the gauge")
	assert.Equal(t, int64(1), leakedCountsByKey[poolKey])
	assert.Zero(t, leakedCountsByKey[volKey], "log-only detector must not contribute to the metrics gauge")
}

// TestRun exercises the package-level Run (default pipeline with all detectors registered).
// Uses mock storage returning empty data so no auth/CCFE/Temporal is required; ensures the
// default pipeline path is covered. With the IP/VM/Disk detectors all submitting their
// Compute API calls via Temporal, the empty-pools fast path means no workflow is ever submitted.
func TestRun(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	pinLocalRegionEmpty(t)

	// pool detector calls ListPoolsSelective first, then fails to enumerate
	// zones/accounts because pinLocalRegionEmpty forces LOCAL_REGION empty.
	storage.EXPECT().ListPoolsSelective(ctx, mock.Anything, mock.Anything).Return(nil, nil).Once()
	// internal_reserved_ip + volume detectors
	storage.EXPECT().ListPools(ctx, mock.Anything).Return(nil, nil).Times(2)
	// snapshot + volume detectors
	storage.EXPECT().ListVolumes(ctx, mock.Anything).Return(nil, nil).Times(2)
	// snapshot detector accountID→name map
	storage.EXPECT().GetAccounts(ctx, false, mock.Anything).Return(nil, nil).Once()
	// vm_orphan + disk_orphan detectors call ListAllTpProjects and exit on empty
	storage.EXPECT().ListAllTpProjects(ctx).Return(nil, nil).Times(2)
	storage.EXPECT().GetSnapshotsWithCondition(ctx, mock.Anything).Return(nil, nil).Once()
	storage.EXPECT().GetMultipleBackupVaults(ctx, mock.Anything).Return(nil, nil).Once()

	err := Run(ctx, storage, nil)
	assert.NoError(t, err)
}

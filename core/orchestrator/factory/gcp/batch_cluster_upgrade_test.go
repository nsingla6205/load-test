package gcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"gorm.io/gorm"
)

func TestBatchUpgradeClusters(t *testing.T) {
	t.Run("SuccessfulBatchUpgradeWithPoolList", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"},
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.TotalClusters == 1 &&
				job.Status == string(models.UpgradeStatusPending) &&
				job.TargetVersion == testOntapVersion &&
				job.MaxConcurrency == 5
		})).Return(&datamodel.BatchClusterUpgradeJob{
			BaseModel:     datamodel.BaseModel{UUID: "batch-uuid-1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Status:        string(models.UpgradeStatusPending),
			TargetVersion: testOntapVersion,
		}, nil)

		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(params *workflows.BatchClusterUpgradeWorkflowParams) bool {
			return params.CorrelationID != "" &&
				len(params.SelectedClusters) == 1 &&
				params.SelectedClusters[0].PoolID == "pool-1"
		})).Return(nil, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType:  string(models.SelectionTypePoolList),
			PoolIDs:        []string{"pool-1"},
			MaxConcurrency: 5,
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.NotEmpty(t, jobID)
		assert.Equal(t, 1, response.TotalClusters)
		assert.Equal(t, models.UpgradeStatusPending, response.Status)
		mockStorage.AssertExpectations(t)
	})

	t.Run("UsesRequestCorrelationIDWhenPresent", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "request-corr-id")

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"},
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.Anything).Return(&datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-uuid-1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Status:    string(models.UpgradeStatusPending),
		}, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(params *workflows.BatchClusterUpgradeWorkflowParams) bool {
			return params.CorrelationID == "request-corr-id"
		})).Return(nil, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType:  string(models.SelectionTypePoolList),
			PoolIDs:        []string{"pool-1"},
			MaxConcurrency: 5,
		}

		_, _, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.NoError(t, err)
		mockTemporal.AssertExpectations(t)
	})

	t.Run("DefaultsConcurrencyWhenZero", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.MaxConcurrency == defaultMaxConcurrency
		})).Return(&datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-uuid", CreatedAt: time.Now(), UpdatedAt: time.Now()},
			Status:    string(models.UpgradeStatusPending),
		}, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType:  string(models.SelectionTypePoolList),
			PoolIDs:        []string{"pool-1"},
			MaxConcurrency: 0,
		}

		_, _, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("RejectsInvalidSelectionParametersBeforeResolvingImages", func(t *testing.T) {
		tests := []struct {
			name         string
			params       *commonparams.BatchUpgradeClusterParams
			errorMessage string
		}{
			{
				name: "unknown selection type",
				params: &commonparams.BatchUpgradeClusterParams{
					SelectionType: "unknown",
				},
				errorMessage: "selectionType must be either percentage or poolList",
			},
			{
				name: "missing percentage",
				params: &commonparams.BatchUpgradeClusterParams{
					SelectionType: string(models.SelectionTypePercentage),
				},
				errorMessage: "percentage must be between 1 and 100",
			},
			{
				name: "empty pool list",
				params: &commonparams.BatchUpgradeClusterParams{
					SelectionType: string(models.SelectionTypePoolList),
				},
				errorMessage: "poolIds must contain at least one pool",
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				mockStorage := database.NewMockStorage(t)
				orchestrator := &GCPOrchestrator{storage: mockStorage}

				response, jobID, err := orchestrator.BatchUpgradeClusters(context.Background(), test.params)

				assert.Error(t, err)
				assert.Nil(t, response)
				assert.Empty(t, jobID)
				assert.True(t, customerrors.IsBadRequestErr(err))
				assert.ErrorContains(t, err, test.errorMessage)
				mockStorage.AssertExpectations(t)
			})
		}
	})

	t.Run("ReturnsErrorWhenNoEligibleClusters", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-bad"}).Return([]*datamodel.Pool{}, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-bad"},
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Empty(t, jobID)
		assert.True(t, customerrors.IsConflictErr(err))
		assert.Contains(t, err.Error(), "pool pool-bad: not found")
		mockStorage.AssertExpectations(t)
	})

	t.Run("RejectsPoolListWhenAnyPoolFailsPrechecks", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		activePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-active"},
			State:     datamodel.LifeCycleStateREADY,
		}
		invalidStatePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-creating"},
			State:     "CREATING",
		}
		upToDatePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-current"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{
				VSABuildImage:      testVSAName,
				MediatorBuildImage: testMediatorName,
			},
		}
		zoneSwitchPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-zone-switch"},
			State:     datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{
				ZoneSwitchState: datamodel.ZoneSwitching,
			},
		}

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-active", "pool-creating", "pool-current", "pool-zone-switch"}).
			Return([]*datamodel.Pool{activePool, invalidStatePool, upToDatePool, zoneSwitchPool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-active").Return([]*datamodel.ClusterUpgradeJob{{
			BaseModel: datamodel.BaseModel{UUID: "active-job"},
			Status:    string(models.UpgradeStatusInProgress),
		}}, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-active", "pool-creating", "pool-current", "pool-zone-switch"},
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Empty(t, jobID)
		assert.True(t, customerrors.IsConflictErr(err))
		assert.ErrorContains(t, err, "upgrade job active-job is already IN_PROGRESS")
		assert.ErrorContains(t, err, "current state is CREATING")
		assert.ErrorContains(t, err, "already uses the target VSA and mediator images")
		assert.ErrorContains(t, err, "pool pool-zone-switch: "+zoneSwitchClusterUpgradeBlockedMsg)
		mockStorage.AssertExpectations(t)
	})

	t.Run("PercentageSelectionExcludesPoolsThatFailSharedPrechecks", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		ctx := context.Background()
		now := time.Now()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		activePool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-active", CreatedAt: now}, State: datamodel.LifeCycleStateREADY}
		currentPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-current", CreatedAt: now.Add(time.Minute)},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{VSABuildImage: testVSAName, MediatorBuildImage: testMediatorName},
		}
		zoneSwitchPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-zone-switch", CreatedAt: now.Add(2 * time.Minute)},
			State:     datamodel.LifeCycleStateREADY,
			PoolAttributes: &datamodel.PoolAttributes{
				ZoneSwitchState: datamodel.ZoneSwitched,
			},
		}
		eligiblePool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-eligible", CreatedAt: now.Add(3 * time.Minute)}, State: datamodel.LifeCycleStateREADY}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return([]*datamodel.PoolView{
			{Pool: *activePool},
			{Pool: *currentPool},
			{Pool: *zoneSwitchPool},
			{Pool: *eligiblePool},
		}, nil)
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-active", "pool-current", "pool-zone-switch", "pool-eligible"}).
			Return([]*datamodel.Pool{activePool, currentPool, zoneSwitchPool, eligiblePool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-active").Return([]*datamodel.ClusterUpgradeJob{{
			BaseModel: datamodel.BaseModel{UUID: "active-job"},
			Status:    string(models.UpgradeStatusPending),
		}}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-eligible").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.TotalClusters == 1
		})).Return(&datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-percentage", CreatedAt: now, UpdatedAt: now},
			Status:    string(models.UpgradeStatusPending),
		}, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(params *workflows.BatchClusterUpgradeWorkflowParams) bool {
			return len(params.SelectedClusters) == 1 && params.SelectedClusters[0].PoolID == "pool-eligible"
		})).Return(nil, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
		}

		response, _, err := orchestrator.BatchUpgradeClusters(ctx, params)

		assert.NoError(t, err)
		assert.Equal(t, 1, response.TotalClusters)
		assert.Equal(t, "pool-eligible", response.SelectedClusters[0].PoolID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("MarksBatchJobFailedWhenWorkflowStartFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
		}
		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-workflow-failure"},
			Status:    string(models.UpgradeStatusPending),
		}

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.Anything).Return(batchJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.UUID == "batch-workflow-failure" &&
				job.Status == string(models.UpgradeStatusFailed) &&
				job.ErrorDetails != nil &&
				job.ErrorDetails.ErrorCode == "BATCH_UPGRADE_FAILED" &&
				job.ErrorDetails.ErrorMessage == "Failed to start batch upgrade workflow: "+assert.AnError.Error()
		})).Return(nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1"},
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Empty(t, jobID)
		assert.Equal(t, string(models.UpgradeStatusFailed), batchJob.Status)
		assert.Equal(t, "BATCH_UPGRADE_ERROR", batchJob.ErrorDetails.ErrorType)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenImageResolutionFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return nil, assert.AnError
		}

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1"},
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Empty(t, jobID)
	})

	t.Run("ReturnsErrorWhenDBCreateFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		origDetermineTargetBuildImages := determineTargetBuildImages
		defer func() { determineTargetBuildImages = origDetermineTargetBuildImages }()
		determineTargetBuildImages = func(_ context.Context, _ database.Storage, _ *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
			return &TargetBuildImages{
				OntapVersion: testOntapVersion,
				VSAImagePath: testVSAImagePath,
				VSAName:      testVSAName,
				MediatorName: testMediatorName,
			}, nil
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)
		mockStorage.On("CreateBatchClusterUpgradeJob", ctx, mock.Anything).Return(nil, assert.AnError)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1"},
		}

		response, jobID, err := orchestrator.BatchUpgradeClusters(ctx, params)
		assert.Error(t, err)
		assert.Nil(t, response)
		assert.Empty(t, jobID)
		mockStorage.AssertExpectations(t)
	})
}

func TestBatchClusterUpgradeNilPoolHandling(t *testing.T) {
	t.Run("ExplicitPoolIsReportedNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-nil"}).Return([]*datamodel.Pool{nil}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-nil"},
		}

		selected, err := selectAndValidateExplicitPools(ctx, mockStorage, params, &TargetBuildImages{})

		assert.Nil(t, selected)
		assert.True(t, customerrors.IsConflictErr(err))
		assert.ErrorContains(t, err, "pool pool-nil: not found")
		mockStorage.AssertExpectations(t)
	})

	t.Run("PercentagePoolReturnsUnavailableError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-nil"}).Return([]*datamodel.Pool{nil}, nil)

		selected, err := filterPercentageSelection(
			ctx,
			mockStorage,
			[]models.SelectedCluster{{ClusterID: "pool-nil", PoolID: "pool-nil"}},
			&commonparams.BatchUpgradeClusterParams{},
			&TargetBuildImages{},
		)

		assert.Nil(t, selected)
		assert.ErrorContains(t, err, "Failed to retrieve pool pool-nil for batch upgrade")
		mockStorage.AssertExpectations(t)
	})
}

func TestSelectedClusterFromPoolUsesEnvironmentVersionWhenBuildInfoMissing(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-without-build-info"},
	}

	selected := selectedClusterFromPool(pool, 1)

	assert.Equal(t, env.CurrentOntapVersionDetails, selected.CurrentVersion)
}

func TestGetBatchUpgradeStatus(t *testing.T) {
	t.Run("ReturnsStatusWithChildJobs", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}
		childJobs := []*datamodel.ClusterUpgradeJob{
			{BaseModel: datamodel.BaseModel{UUID: "child-1"}, ClusterID: "cluster-1", Status: string(models.UpgradeStatusCompleted)},
			{BaseModel: datamodel.BaseModel{UUID: "child-2"}, ClusterID: "cluster-2", Status: string(models.UpgradeStatusInProgress)},
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("GetClusterUpgradeJobsByBatchID", ctx, "batch-1").Return(childJobs, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "batch-1")

		assert.NoError(t, err)
		assert.NotNil(t, progress)
		assert.Equal(t, "batch-1", progress.JobID)
		assert.Equal(t, models.UpgradeStatus("IN_PROGRESS"), progress.Status)
		assert.Len(t, progress.Clusters, 2)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsStatusWithErrors", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusFailed),
		}
		childJobs := []*datamodel.ClusterUpgradeJob{
			{
				BaseModel: datamodel.BaseModel{UUID: "child-1"},
				ClusterID: "cluster-1",
				Status:    string(models.UpgradeStatusFailed),
				ErrorDetails: &datamodel.UpgradeErrorDetails{
					ErrorCode:    "UPGRADE_FAILED",
					ErrorMessage: "VLM timeout",
					ErrorType:    "UPGRADE_ERROR",
					Retryable:    true,
				},
			},
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("GetClusterUpgradeJobsByBatchID", ctx, "batch-1").Return(childJobs, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "batch-1")

		assert.NoError(t, err)
		assert.Len(t, progress.Errors, 1)
		assert.Equal(t, "UPGRADE_FAILED", progress.Errors[0].Code)
		assert.Equal(t, "cluster-1", progress.Errors[0].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsBatchWorkflowStartError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusFailed),
			ErrorDetails: &datamodel.UpgradeErrorDetails{
				ErrorCode:    "BATCH_UPGRADE_FAILED",
				ErrorMessage: "Failed to start batch upgrade workflow: unavailable",
				ErrorType:    "BATCH_UPGRADE_ERROR",
				Retryable:    false,
			},
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("GetClusterUpgradeJobsByBatchID", ctx, "batch-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "batch-1")

		assert.NoError(t, err)
		assert.Equal(t, models.UpgradeStatusFailed, progress.Status)
		assert.Len(t, progress.Errors, 1)
		assert.Equal(t, "BATCH_UPGRADE_FAILED", progress.Errors[0].Code)
		assert.Equal(t, "Failed to start batch upgrade workflow: unavailable", progress.Errors[0].Message)
		assert.Empty(t, progress.Errors[0].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsWarningWhenSkippedCountPositive", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel:    datamodel.BaseModel{UUID: "batch-1"},
			Status:       string(models.UpgradeStatusFailed),
			SkippedCount: 3,
		}
		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("GetClusterUpgradeJobsByBatchID", ctx, "batch-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "batch-1")

		assert.NoError(t, err)
		assert.Len(t, progress.Warnings, 1)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsNotFoundError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "not-found").Return(nil, gorm.ErrRecordNotFound)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "not-found")

		assert.Error(t, err)
		assert.Nil(t, progress)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenChildJobsFetchFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}
		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("GetClusterUpgradeJobsByBatchID", ctx, "batch-1").Return(nil, assert.AnError)

		orchestrator := &GCPOrchestrator{storage: mockStorage}
		progress, err := orchestrator.GetBatchUpgradeStatus(ctx, "batch-1")

		assert.Error(t, err)
		assert.Nil(t, progress)
		mockStorage.AssertExpectations(t)
	})
}

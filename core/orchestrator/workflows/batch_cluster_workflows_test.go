package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupBatchTestEnv(t *testing.T) (*testsuite.WorkflowTestSuite, *testsuite.TestWorkflowEnvironment) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)
	env.SetTestTimeout(time.Minute * 10)
	return &ts, env
}

func prepareBatchUpgradeWave(
	_ context.Context,
	params *activities.PrepareBatchUpgradeWaveParams,
) ([]activities.PreparedClusterUpgrade, error) {
	prepared := make([]activities.PreparedClusterUpgrade, len(params.Clusters))
	for i, cluster := range params.Clusters {
		prepared[i] = activities.PreparedClusterUpgrade{
			ClusterID: cluster.SelectedCluster.ClusterID,
			JobID:     cluster.JobID,
			Pool:      &datamodel.Pool{},
		}
	}
	return prepared, nil
}

func TestBatchClusterUpgradeWorkflow_AllChildrenSucceed(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)
	env.RegisterActivity(batchActivity.CompleteBatchJobActivity)
	env.RegisterActivity(batchActivity.PrepareBatchUpgradeWaveActivity)
	env.RegisterActivity(clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity)
	env.RegisterWorkflow(ClusterUpgradeWorkflow)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, "batch-1", string(models.UpgradeStatusInProgress), "").Return(nil)
	env.OnActivity(batchActivity.PrepareBatchUpgradeWaveActivity, mock.Anything, mock.Anything).Return(prepareBatchUpgradeWave)
	env.OnWorkflow(ClusterUpgradeWorkflow, mock.Anything, mock.Anything).Return(func(ctx workflow.Context, params *ClusterUpgradeWorkflowParams) error {
		assert.NotNil(t, params.Pool)
		assert.Equal(t, generateChildUpgradeJobID("batch-1", params.ClusterID), params.JobID)
		return nil
	})
	env.OnActivity(batchActivity.CompleteBatchJobActivity, mock.Anything, "batch-1", string(models.UpgradeStatusCompleted), "", 2, 0, 0).Return(nil)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1", CurrentVersion: "9.14.1"},
			{ClusterID: "cluster-2", PoolID: "pool-2", CurrentVersion: "9.14.1"},
		},
		TargetVersion:  "9.15.1",
		VSAImagePath:   "gcr.io/vsa:9.15.1",
		VSAImageName:   "x-9-15-1",
		MediatorName:   "mediator-9-15-1",
		MaxConcurrency: 5,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestGenerateChildUpgradeJobID(t *testing.T) {
	jobID := generateChildUpgradeJobID("batch-1", "cluster-1")

	_, err := uuid.Parse(jobID)
	assert.NoError(t, err)
	assert.Equal(t, jobID, generateChildUpgradeJobID("batch-1", "cluster-1"))
	assert.NotEqual(t, jobID, generateChildUpgradeJobID("batch-1", "cluster-2"))
	assert.NotEqual(t, jobID, generateChildUpgradeJobID("batch-2", "cluster-1"))
}

func TestBatchClusterUpgradeWorkflow_AllChildrenFail(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)
	env.RegisterActivity(batchActivity.CompleteBatchJobActivity)
	env.RegisterActivity(batchActivity.PrepareBatchUpgradeWaveActivity)
	env.RegisterActivity(clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity)
	env.RegisterWorkflow(ClusterUpgradeWorkflow)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, "batch-1", string(models.UpgradeStatusInProgress), "").Return(nil)
	env.OnActivity(batchActivity.PrepareBatchUpgradeWaveActivity, mock.Anything, mock.Anything).Return(prepareBatchUpgradeWave)
	env.OnWorkflow(ClusterUpgradeWorkflow, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity(batchActivity.CompleteBatchJobActivity, mock.Anything, "batch-1", string(models.UpgradeStatusFailed), mock.Anything, 0, 2, 0).Return(nil)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1"},
			{ClusterID: "cluster-2", PoolID: "pool-2"},
		},
		TargetVersion:  "9.15.1",
		MaxConcurrency: 5,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestBatchClusterUpgradeWorkflow_PartialFailure(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)
	env.RegisterActivity(batchActivity.CompleteBatchJobActivity)
	env.RegisterActivity(batchActivity.PrepareBatchUpgradeWaveActivity)
	env.RegisterActivity(clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity)
	env.RegisterWorkflow(ClusterUpgradeWorkflow)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(batchActivity.PrepareBatchUpgradeWaveActivity, mock.Anything, mock.Anything).Return(prepareBatchUpgradeWave)

	callCount := 0
	env.OnWorkflow(ClusterUpgradeWorkflow, mock.Anything, mock.Anything).Return(func(ctx workflow.Context, params *ClusterUpgradeWorkflowParams) error {
		callCount++
		if callCount == 1 {
			return assert.AnError
		}
		return nil
	})

	env.OnActivity(batchActivity.CompleteBatchJobActivity, mock.Anything, "batch-1", string(models.UpgradeStatusFailed), mock.Anything, 1, 1, 0).Return(nil)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1"},
			{ClusterID: "cluster-2", PoolID: "pool-2"},
		},
		TargetVersion:  "9.15.1",
		MaxConcurrency: 5,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestBatchClusterUpgradeWorkflow_FailureThresholdAbortsBatch(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)
	env.RegisterActivity(batchActivity.CompleteBatchJobActivity)
	env.RegisterActivity(batchActivity.PrepareBatchUpgradeWaveActivity)
	env.RegisterActivity(clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity)
	env.RegisterWorkflow(ClusterUpgradeWorkflow)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(batchActivity.PrepareBatchUpgradeWaveActivity, mock.Anything, mock.Anything).Return(prepareBatchUpgradeWave)
	startedChildren := 0
	env.OnWorkflow(ClusterUpgradeWorkflow, mock.Anything, mock.Anything).Return(func(workflow.Context, *ClusterUpgradeWorkflowParams) error {
		startedChildren++
		return assert.AnError
	})
	env.OnActivity(batchActivity.CompleteBatchJobActivity, mock.Anything, "batch-1", string(models.UpgradeStatusFailed), mock.Anything, 0, 2, 2).Return(nil)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1"},
			{ClusterID: "cluster-2", PoolID: "pool-2"},
			{ClusterID: "cluster-3", PoolID: "pool-3"},
			{ClusterID: "cluster-4", PoolID: "pool-4"},
		},
		TargetVersion:    "9.15.1",
		MaxConcurrency:   2,
		FailureThreshold: 1,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Equal(t, 2, startedChildren)
}

func TestBatchClusterUpgradeWorkflow_ChildJobCreationFailure(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}
	clusterUpgradeActivity := &activities.ClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)
	env.RegisterActivity(batchActivity.CompleteBatchJobActivity)
	env.RegisterActivity(batchActivity.PrepareBatchUpgradeWaveActivity)
	env.RegisterActivity(clusterUpgradeActivity.UpdateClusterUpgradeJobStatusActivity)
	env.RegisterWorkflow(ClusterUpgradeWorkflow)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(batchActivity.PrepareBatchUpgradeWaveActivity, mock.Anything, mock.Anything).
		Return([]activities.PreparedClusterUpgrade{{
			ClusterID: "cluster-1",
			JobID:     generateChildUpgradeJobID("batch-1", "cluster-1"),
			Error:     assert.AnError.Error(),
		}}, nil)
	env.OnActivity(batchActivity.CompleteBatchJobActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1"},
		},
		TargetVersion:  "9.15.1",
		MaxConcurrency: 5,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestBatchClusterUpgradeWorkflow_StatusUpdateFailure(t *testing.T) {
	_, env := setupBatchTestEnv(t)
	mockStorage := database.NewMockStorage(t)
	batchActivity := &activities.BatchClusterUpgradeActivity{SE: mockStorage}

	env.RegisterActivity(batchActivity.UpdateBatchJobStatusActivity)

	env.OnActivity(batchActivity.UpdateBatchJobStatusActivity, mock.Anything, "batch-1", string(models.UpgradeStatusInProgress), "").Return(assert.AnError)

	params := &BatchClusterUpgradeWorkflowParams{
		BatchJobID: "batch-1",
		SelectedClusters: []models.SelectedCluster{
			{ClusterID: "cluster-1", PoolID: "pool-1"},
		},
		TargetVersion:  "9.15.1",
		MaxConcurrency: 5,
	}

	env.ExecuteWorkflow(BatchClusterUpgradeWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestBatchClusterUpgradeWorkflowParams(t *testing.T) {
	t.Run("StructFieldsAreAccessible", func(t *testing.T) {
		params := &BatchClusterUpgradeWorkflowParams{
			BatchJobID: "batch-1",
			SelectedClusters: []models.SelectedCluster{
				{ClusterID: "c1", PoolID: "p1", CurrentVersion: "9.14.1", Priority: 1},
			},
			TargetVersion:    "9.15.1",
			VSAImagePath:     "/images/vsa",
			VSAImageName:     "vsa-name",
			MediatorName:     "med-name",
			ForceUpgrade:     true,
			MaxConcurrency:   3,
			FailureThreshold: 2,
			Metadata:         map[string]string{"key": "value"},
		}

		assert.Equal(t, "batch-1", params.BatchJobID)
		assert.Len(t, params.SelectedClusters, 1)
		assert.Equal(t, "9.15.1", params.TargetVersion)
		assert.True(t, params.ForceUpgrade)
		assert.Equal(t, 3, params.MaxConcurrency)
		assert.Equal(t, 2, params.FailureThreshold)
	})
}

func TestChildUpgradeResult(t *testing.T) {
	t.Run("SuccessResult", func(t *testing.T) {
		result := ChildUpgradeResult{
			ClusterID: "cluster-1",
			JobID:     "job-1",
			Success:   true,
		}
		assert.True(t, result.Success)
		assert.Empty(t, result.Error)
	})

	t.Run("FailureResult", func(t *testing.T) {
		result := ChildUpgradeResult{
			ClusterID: "cluster-1",
			JobID:     "job-1",
			Success:   false,
			Error:     "VLM timeout",
		}
		assert.False(t, result.Success)
		assert.Equal(t, "VLM timeout", result.Error)
	})
}

// Suppresses unused import warning for datamodel
var _ = datamodel.BaseModel{}

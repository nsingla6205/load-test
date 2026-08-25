package backgroundworkflows

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type mockExpertModeVolumeSyncActivity struct {
	ListPoolsReturnValue []*database.PoolIdentifier
	ListPoolsReturnError error
}

func (m *mockExpertModeVolumeSyncActivity) ListOntapModePoolsPaginated(ctx context.Context, offset, limit int) ([]*database.PoolIdentifier, error) {
	if m.ListPoolsReturnError != nil {
		return nil, m.ListPoolsReturnError
	}
	// Honor the requested page so page-until-empty tests terminate deterministically.
	total := len(m.ListPoolsReturnValue)
	if offset >= total {
		return nil, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return m.ListPoolsReturnValue[offset:end], nil
}

func TestExpertModeVolumeSyncParentWorkflow_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	mockActivity := &mockExpertModeVolumeSyncActivity{
		ListPoolsReturnValue: []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "pool-1"},
			{UUID: "pool-2", Name: "pool-2"},
			{UUID: "pool-3", Name: "pool-3"},
		},
	}

	env.RegisterActivity(mockActivity)
	env.RegisterWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow)

	env.OnWorkflow("SyncExpertModeVolumesForPoolBatchWorkflow", mock.Anything, mock.Anything).
		Return(&backgroundactivities.SyncExpertModeVolumesBatchReturnValue{
			TotalProcessed: 3,
			Successful:     3,
			Failed:         0,
		}, nil)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result GenericParentWorkflowResult
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 3, result.TotalItemsProcessed)
	assert.Equal(t, 3, result.TotalSuccessful)
	assert.Equal(t, 0, result.TotalFailed)
}

func TestExpertModeVolumeSyncParentWorkflow_ZeroPools(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	// No pools configured: the first list page is empty, so the workflow completes with
	// zero work without spawning any batch child workflows.
	mockActivity := &mockExpertModeVolumeSyncActivity{}

	env.RegisterActivity(mockActivity)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result GenericParentWorkflowResult
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 0, result.TotalItemsProcessed)
}

func TestExpertModeVolumeSyncParentWorkflow_ListPoolsFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	mockActivity := &mockExpertModeVolumeSyncActivity{
		ListPoolsReturnError: assert.AnError,
	}

	env.RegisterActivity(mockActivity)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

// TestExpertModeVolumeSyncParentWorkflow_BatchWorkflowFailure verifies that a child
// batch workflow returning an error counts every pool in that batch as failed and does
// not abort the parent workflow.
func TestExpertModeVolumeSyncParentWorkflow_BatchWorkflowFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	mockActivity := &mockExpertModeVolumeSyncActivity{
		ListPoolsReturnValue: []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "pool-1"},
			{UUID: "pool-2", Name: "pool-2"},
		},
	}

	env.RegisterActivity(mockActivity)
	env.RegisterWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow)

	env.OnWorkflow("SyncExpertModeVolumesForPoolBatchWorkflow", mock.Anything, mock.Anything).
		Return(nil, assert.AnError)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result GenericParentWorkflowResult
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 2, result.TotalItemsProcessed)
	assert.Equal(t, 0, result.TotalSuccessful)
	assert.Equal(t, 2, result.TotalFailed)
}

// TestExpertModeVolumeSyncParentWorkflow_MultipleListPagesAndBatches exercises pagination
// of the pool list and the multi-batch fan-out across multiple pages. With list page = 100
// and pool batch = 250, 251 pools fan out into:
//
//	page 0 (100 pools → 1 batch of 100)
//	page 1 (100 pools → 1 batch of 100)
//	page 2 (51 pools  → 1 batch of 51)
//
// = 3 batch child workflow calls.
func TestExpertModeVolumeSyncParentWorkflow_MultipleListPagesAndBatches(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	const totalPools = 251
	pools := make([]*database.PoolIdentifier, totalPools)
	for i := 0; i < totalPools; i++ {
		name := fmt.Sprintf("pool-%d", i)
		pools[i] = &database.PoolIdentifier{UUID: name, Name: name}
	}
	mockActivity := &mockExpertModeVolumeSyncActivity{
		ListPoolsReturnValue: pools,
	}

	env.RegisterActivity(mockActivity)
	env.RegisterWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow)

	batchCalls := 0
	env.OnWorkflow("SyncExpertModeVolumesForPoolBatchWorkflow", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			batchCalls++
			batch := args.Get(1).([]*database.PoolIdentifier)
			assert.NotEmpty(t, batch)
		}).
		Return(func(_ workflow.Context, batch []*database.PoolIdentifier) (*backgroundactivities.SyncExpertModeVolumesBatchReturnValue, error) {
			return &backgroundactivities.SyncExpertModeVolumesBatchReturnValue{
				TotalProcessed: len(batch),
				Successful:     len(batch),
			}, nil
		}, nil)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result GenericParentWorkflowResult
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, totalPools, result.TotalItemsProcessed)
	assert.Equal(t, totalPools, result.TotalSuccessful)
	assert.Equal(t, 0, result.TotalFailed)
	assert.Equal(t, 3, batchCalls)
}

func TestExpertModeVolumeSyncParentWorkflow_MixedBatchResults(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	mockActivity := &mockExpertModeVolumeSyncActivity{
		ListPoolsReturnValue: []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "pool-1"},
			{UUID: "pool-2", Name: "pool-2"},
			{UUID: "pool-3", Name: "pool-3"},
			{UUID: "pool-4", Name: "pool-4"},
		},
	}

	env.RegisterActivity(mockActivity)
	env.RegisterWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow)

	env.OnWorkflow("SyncExpertModeVolumesForPoolBatchWorkflow", mock.Anything, mock.Anything).
		Return(&backgroundactivities.SyncExpertModeVolumesBatchReturnValue{
			TotalProcessed:      4,
			Successful:          2,
			Failed:              2,
			FailedResourceNames: []string{"pool-3", "pool-4"},
		}, nil)

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result GenericParentWorkflowResult
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 4, result.TotalItemsProcessed)
	assert.Equal(t, 2, result.TotalSuccessful)
	assert.Equal(t, 2, result.TotalFailed)
}

func TestSyncExpertModeVolumesForPoolBatchWorkflow_EmptyInput(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow, []*database.PoolIdentifier{})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result backgroundactivities.SyncExpertModeVolumesBatchReturnValue
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 0, result.TotalProcessed)
}

func TestSyncExpertModeVolumesForPoolBatchWorkflow_SinglePoolSuccess(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	syncActivity := &backgroundactivities.SyncExpertModeVolumesActivity{}

	env.RegisterActivity(syncActivity)
	env.OnActivity(syncActivity.SyncExpertModeVolumesForPoolActivity, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow, []*database.PoolIdentifier{
		{UUID: "pool-ok", Name: "pool-ok"},
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result backgroundactivities.SyncExpertModeVolumesBatchReturnValue
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 1, result.Successful)
	assert.Equal(t, 0, result.Failed)
}

func TestSyncExpertModeVolumesForPoolBatchWorkflow_PoolActivityFailure(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	syncActivity := &backgroundactivities.SyncExpertModeVolumesActivity{}

	env.RegisterActivity(syncActivity)
	env.OnActivity(syncActivity.SyncExpertModeVolumesForPoolActivity, mock.Anything, mock.Anything).Return(assert.AnError)

	env.ExecuteWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow, []*database.PoolIdentifier{
		{UUID: "pool-fail", Name: "pool-fail"},
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result backgroundactivities.SyncExpertModeVolumesBatchReturnValue
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"pool-fail"}, result.FailedResourceNames)
}

// TestSyncExpertModeVolumesForPoolBatchWorkflow_MultipleWaves verifies that more pools
// than expertModeVolumeSyncActivityConcurrency drive multiple sequential waves of
// per-pool activities (mocked with .Times to enforce the call count).
// withInvalidRetryBackoff sets the shared RetryBackoff coefficient to an unparseable value so
// workflows.PopulateRetryPolicyParams() returns an error, then restores it after the test.
func withInvalidRetryBackoff(t *testing.T) {
	t.Helper()
	original := workflows.RetryBackoff
	t.Cleanup(func() { workflows.RetryBackoff = original })
	workflows.RetryBackoff = "not-a-float"
}

func TestExpertModeVolumeSyncParentWorkflow_RetryPolicyError(t *testing.T) {
	withInvalidRetryBackoff(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(ExpertModeVolumeSyncParentWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestSyncExpertModeVolumesForPoolBatchWorkflow_RetryPolicyError(t *testing.T) {
	withInvalidRetryBackoff(t)

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow, []*database.PoolIdentifier{
		{UUID: "pool-1", Name: "pool-1"},
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

// TestRunExpertModeVolumeSyncBatch_EmptyPage covers the defensive empty-page guard in
// runExpertModeVolumeSyncBatch by invoking it from a thin wrapper workflow.
func TestRunExpertModeVolumeSyncBatch_EmptyPage(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	wrapper := func(ctx workflow.Context) (int, error) {
		processed, successful, failed := runExpertModeVolumeSyncBatch(ctx, 0, nil)
		return processed + successful + failed, nil
	}
	env.RegisterWorkflow(wrapper)

	env.ExecuteWorkflow(wrapper)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	var sum int
	assert.NoError(t, env.GetWorkflowResult(&sum))
	assert.Equal(t, 0, sum)
}

func TestSyncExpertModeVolumesForPoolBatchWorkflow_MultipleWaves(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	syncActivity := &backgroundactivities.SyncExpertModeVolumesActivity{}

	const totalPools = 25 // 25 pools / 10 wave size = 3 waves (10 + 10 + 5)
	pools := make([]*database.PoolIdentifier, totalPools)
	for i := 0; i < totalPools; i++ {
		name := fmt.Sprintf("pool-%d", i)
		pools[i] = &database.PoolIdentifier{UUID: name, Name: name}
	}

	env.RegisterActivity(syncActivity)
	env.OnActivity(syncActivity.SyncExpertModeVolumesForPoolActivity, mock.Anything, mock.Anything).
		Return(nil).Times(totalPools)

	env.ExecuteWorkflow(SyncExpertModeVolumesForPoolBatchWorkflow, pools)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	var result backgroundactivities.SyncExpertModeVolumesBatchReturnValue
	assert.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, totalPools, result.TotalProcessed)
	assert.Equal(t, totalPools, result.Successful)
	assert.Equal(t, 0, result.Failed)
}

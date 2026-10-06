package backgroundworkflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestReconcilePoolAuthTypeForDesiredCAWorkflow(t *testing.T) {
	tests := []struct {
		name        string
		activityErr error
	}{
		{name: "success"},
		{name: "activity failure", activityErr: errors.New("desired CA reconciliation failed")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			activity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
			env.RegisterWorkflow(ReconcilePoolAuthTypeForDesiredCAWorkflow)
			env.RegisterActivity(activity.ReconcileAuthType1PoolForDesiredCA)
			expectation := env.OnActivity(
				activity.ReconcileAuthType1PoolForDesiredCA,
				mock.Anything,
				"pool-1",
				"project/pool/ca",
			).Return(test.activityErr)
			if test.activityErr != nil {
				expectation.Twice()
			} else {
				expectation.Once()
			}

			env.ExecuteWorkflow(
				ReconcilePoolAuthTypeForDesiredCAWorkflow,
				"pool-1",
				"project/pool/ca",
			)

			require.True(t, env.IsWorkflowCompleted())
			if test.activityErr != nil {
				require.ErrorContains(t, env.GetWorkflowError(), test.activityErr.Error())
				return
			}
			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestReconcilePoolAuthTypeAPIWorkflow(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially)
	env.RegisterActivity(controlActivity.WaitForAuthTypeReconciliationOperation)
	env.OnActivity(
		controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially,
		mock.Anything,
		"pool-1",
		"project/pool/ca",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("time.Duration"),
	).Return(nil).Once()
	env.OnActivity(
		controlActivity.WaitForAuthTypeReconciliationOperation,
		mock.Anything,
		mock.AnythingOfType("string"),
	).Return(nil).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		ReconcilePoolAuthTypeAPIWorkflowParams{
			PoolUUID:     "pool-1",
			DesiredCAURI: "project/pool/ca",
		},
	)

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReconcilePoolAuthTypeAPIWorkflow_InvalidInput(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)

	env.ExecuteWorkflow(ReconcilePoolAuthTypeAPIWorkflow, ReconcilePoolAuthTypeAPIWorkflowParams{})

	require.ErrorContains(t, env.GetWorkflowError(), "pool UUID and desired CA URI are required")
}

func TestReconcilePoolAuthTypesAPIWorkflow_AggregatesChildResults(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	commonActivity := &activities.CommonActivities{}
	env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.UpdateJobAttributes)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(
		commonActivity.UpdateJobAttributes,
		mock.Anything,
		"job-1",
		mock.MatchedBy(func(attributes *datamodel.JobAttributes) bool {
			return authReconciliationAttributesHaveChildJobIDs(attributes, map[string]string{
				"pool-1": "job-1-pool-pool-1",
				"pool-2": "job-1-pool-pool-2",
			})
		}),
	).Return(nil)
	env.OnWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		mock.Anything,
		mock.MatchedBy(func(params ReconcilePoolAuthTypeAPIWorkflowParams) bool {
			return params.PoolUUID == "pool-1"
		}),
	).Return(func(ctx workflow.Context, _ ReconcilePoolAuthTypeAPIWorkflowParams) error {
		require.Equal(t, "job-1-pool-pool-1", workflow.GetInfo(ctx).WorkflowExecution.ID)
		return nil
	}).Once()
	env.OnWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		mock.Anything,
		mock.MatchedBy(func(params ReconcilePoolAuthTypeAPIWorkflowParams) bool {
			return params.PoolUUID == "pool-2"
		}),
	).Return(func(ctx workflow.Context, _ ReconcilePoolAuthTypeAPIWorkflowParams) error {
		require.Equal(t, "job-1-pool-pool-2", workflow.GetInfo(ctx).WorkflowExecution.ID)
		return errors.New("certificate installation failed")
	}).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypesAPIWorkflow,
		ReconcilePoolAuthTypesAPIWorkflowParams{
			JobID:     "job-1",
			BatchSize: 2,
			JobPayload: models.AuthTypeReconciliationJobPayload{
				DesiredCAURI: "project/pool/ca",
				Results: []models.AuthTypeReconciliationPoolResult{
					{PoolUUID: "pool-1", Status: "QUEUED"},
					{PoolUUID: "pool-2", Status: "QUEUED"},
				},
			},
		},
	)

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestReconcilePoolAuthTypesAPIWorkflow_RecordsParentFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	commonActivity := &activities.CommonActivities{}
	env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(jobWithAuthReconciliationState("job-1", datamodel.JobsStatePROCESSING)),
	).Return(errors.New("database unavailable")).Times(authTypeReconciliationJobActivityMaxAttempts)
	env.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(jobWithAuthReconciliationState("job-1", datamodel.JobsStateERROR)),
	).Return(errors.New("status write failed")).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypesAPIWorkflow,
		ReconcilePoolAuthTypesAPIWorkflowParams{
			JobID:     "job-1",
			BatchSize: 1,
		},
	)

	require.ErrorContains(t, env.GetWorkflowError(), "mark auth reconciliation job job-1 processing")
	env.AssertExpectations(t)
}

func TestReconcilePoolAuthTypesAPIWorkflow_InvalidInput(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)

	env.ExecuteWorkflow(ReconcilePoolAuthTypesAPIWorkflow, ReconcilePoolAuthTypesAPIWorkflowParams{})

	require.ErrorContains(t, env.GetWorkflowError(), "job ID and positive batch size are required")
}

func TestReconcilePoolAuthTypesAPIWorkflow_PersistFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	commonActivity := &activities.CommonActivities{}
	env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.UpdateJobAttributes)
	env.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(jobWithAuthReconciliationState("job-1", datamodel.JobsStatePROCESSING)),
	).Return(nil)
	env.OnActivity(commonActivity.UpdateJobAttributes, mock.Anything, "job-1", mock.Anything).
		Return(errors.New("attributes write failed"))
	env.OnActivity(
		commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(jobWithAuthReconciliationState("job-1", datamodel.JobsStateERROR)),
	).Return(nil).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypesAPIWorkflow,
		ReconcilePoolAuthTypesAPIWorkflowParams{
			JobID:     "job-1",
			BatchSize: 1,
			JobPayload: models.AuthTypeReconciliationJobPayload{
				DesiredCAURI: "project/pool/ca",
				Results: []models.AuthTypeReconciliationPoolResult{
					{PoolUUID: "pool-1", Status: "QUEUED"},
				},
			},
		},
	)

	require.ErrorContains(t, env.GetWorkflowError(), "persist auth reconciliation job job-1 results")
}

func TestReconcilePoolAuthTypeAPIWorkflow_WaitFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially)
	env.RegisterActivity(controlActivity.WaitForAuthTypeReconciliationOperation)
	env.OnActivity(
		controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially,
		mock.Anything,
		"pool-1",
		"project/pool/ca",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("time.Duration"),
	).Return(nil).Once()
	env.OnActivity(
		controlActivity.WaitForAuthTypeReconciliationOperation,
		mock.Anything,
		mock.AnythingOfType("string"),
	).Return(temporal.NewNonRetryableApplicationError(
		"child workflow failed",
		"NonRetryableErr",
		errors.New("failure"),
	)).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		ReconcilePoolAuthTypeAPIWorkflowParams{
			PoolUUID:     "pool-1",
			DesiredCAURI: "project/pool/ca",
		},
	)

	require.ErrorContains(t, env.GetWorkflowError(), "pool pool-1 auth reconciliation")
}

func TestReconcilePoolAuthTypesAPIWorkflow_ParentTimeout(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	commonActivity := &activities.CommonActivities{}
	env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.UpdateJobAttributes)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(commonActivity.UpdateJobAttributes, mock.Anything, "job-1", mock.Anything).Return(nil)
	env.OnWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		mock.Anything,
		mock.MatchedBy(func(params ReconcilePoolAuthTypeAPIWorkflowParams) bool {
			return params.PoolUUID == "pool-1"
		}),
	).Return(func(ctx workflow.Context, _ ReconcilePoolAuthTypeAPIWorkflowParams) error {
		return workflow.NewTimer(ctx, time.Hour).Get(ctx, nil)
	}).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypesAPIWorkflow,
		ReconcilePoolAuthTypesAPIWorkflowParams{
			JobID:         "job-1",
			BatchSize:     1,
			ParentTimeout: time.Nanosecond,
			JobPayload: models.AuthTypeReconciliationJobPayload{
				DesiredCAURI: "project/pool/ca",
				Results: []models.AuthTypeReconciliationPoolResult{
					{PoolUUID: "pool-1", Status: "QUEUED"},
					{PoolUUID: "pool-2", Status: "QUEUED"},
				},
			},
		},
	)

	require.ErrorContains(t, env.GetWorkflowError(), "auth-type reconciliation parent timed out")
}

func TestReconcilePoolAuthTypesAPIWorkflow_ParentTimeoutKeepsFinishedPoolResult(t *testing.T) {
	tests := []struct {
		name            string
		finishedErr     error
		wantStatus      string
		wantReasonPiece string
	}{
		{
			name:       "finished success stays done",
			wantStatus: "DONE",
		},
		{
			name:            "finished failure keeps the child error",
			finishedErr:     errors.New("certificate installation failed"),
			wantStatus:      "ERROR",
			wantReasonPiece: "certificate installation failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			commonActivity := &activities.CommonActivities{}
			env.RegisterWorkflow(ReconcilePoolAuthTypesAPIWorkflow)
			env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
			env.RegisterActivity(commonActivity.UpdateJobStatus)
			env.RegisterActivity(commonActivity.UpdateJobAttributes)
			env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
			var lastAttributes *datamodel.JobAttributes
			env.OnActivity(commonActivity.UpdateJobAttributes, mock.Anything, "job-1", mock.Anything).
				Run(func(args mock.Arguments) {
					lastAttributes = args.Get(2).(*datamodel.JobAttributes)
				}).Return(nil)
			env.OnWorkflow(
				ReconcilePoolAuthTypeAPIWorkflow,
				mock.Anything,
				mock.MatchedBy(func(params ReconcilePoolAuthTypeAPIWorkflowParams) bool {
					return params.PoolUUID == "pool-running"
				}),
			).Return(func(ctx workflow.Context, _ ReconcilePoolAuthTypeAPIWorkflowParams) error {
				return workflow.Await(ctx, func() bool { return false })
			}).Once()
			env.OnWorkflow(
				ReconcilePoolAuthTypeAPIWorkflow,
				mock.Anything,
				mock.MatchedBy(func(params ReconcilePoolAuthTypeAPIWorkflowParams) bool {
					return params.PoolUUID == "pool-finished"
				}),
			).Return(test.finishedErr).Once()

			env.ExecuteWorkflow(
				ReconcilePoolAuthTypesAPIWorkflow,
				ReconcilePoolAuthTypesAPIWorkflowParams{
					JobID:            "job-1",
					BatchSize:        2,
					OperationTimeout: 2 * time.Hour,
					ParentTimeout:    time.Minute,
					JobPayload: models.AuthTypeReconciliationJobPayload{
						DesiredCAURI: "project/pool/ca",
						Results: []models.AuthTypeReconciliationPoolResult{
							{PoolUUID: "pool-running", Status: "QUEUED"},
							{PoolUUID: "pool-finished", Status: "QUEUED"},
						},
					},
				},
			)

			require.ErrorContains(t, env.GetWorkflowError(), "auth-type reconciliation parent timed out")
			require.Equal(t, "ERROR", authReconciliationPoolField(t, lastAttributes, "pool-running", "status"))
			require.Contains(t, authReconciliationPoolField(t, lastAttributes, "pool-running", "reason"), "timed out")
			require.Equal(t, test.wantStatus, authReconciliationPoolField(t, lastAttributes, "pool-finished", "status"))
			if test.wantReasonPiece != "" {
				require.Contains(t, authReconciliationPoolField(t, lastAttributes, "pool-finished", "reason"), test.wantReasonPiece)
			}
		})
	}
}

func TestAuthTypeReconciliationRunTimeoutExceedsJobPersistBudget(t *testing.T) {
	require.Greater(t, authTypeReconciliationRunTimeoutSlack, authTypeReconciliationJobPersistBudget())
	require.Equal(t, time.Hour+authTypeReconciliationRunTimeoutSlack, AuthTypeReconciliationWorkflowRunTimeout(0))
	require.Equal(t, 30*time.Minute+authTypeReconciliationRunTimeoutSlack, AuthTypeReconciliationWorkflowRunTimeout(30*time.Minute))
}

func TestReconcilePoolAuthTypeAPIWorkflow_QueueFailure(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	env.RegisterWorkflow(ReconcilePoolAuthTypeAPIWorkflow)
	env.RegisterActivity(controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially)
	env.OnActivity(
		controlActivity.ExecutePoolAuthTypeReconciliationForDesiredCASequentially,
		mock.Anything,
		"pool-1",
		"project/pool/ca",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("time.Duration"),
	).Return(temporal.NewNonRetryableApplicationError(
		"sequencer unavailable",
		"NonRetryableErr",
		errors.New("failure"),
	)).Once()

	env.ExecuteWorkflow(
		ReconcilePoolAuthTypeAPIWorkflow,
		ReconcilePoolAuthTypeAPIWorkflowParams{
			PoolUUID:     "pool-1",
			DesiredCAURI: "project/pool/ca",
		},
	)

	require.ErrorContains(t, env.GetWorkflowError(), "queue pool pool-1 auth reconciliation")
}

func jobWithAuthReconciliationState(
	jobID string,
	state datamodel.JobState,
) func(*datamodel.Job) bool {
	return func(job *datamodel.Job) bool {
		return job != nil && job.UUID == jobID && job.State == string(state)
	}
}

func authReconciliationAttributesHaveChildJobIDs(
	attributes *datamodel.JobAttributes,
	expected map[string]string,
) bool {
	if attributes == nil || attributes.PayloadAttributes == nil {
		return false
	}
	rawResults, ok := attributes.PayloadAttributes["results"].([]interface{})
	if !ok || len(rawResults) != len(expected) {
		return false
	}
	for _, rawResult := range rawResults {
		result, ok := rawResult.(map[string]interface{})
		if !ok {
			return false
		}
		poolUUID, _ := result["poolUuid"].(string)
		jobID, _ := result["jobId"].(string)
		if expected[poolUUID] != jobID {
			return false
		}
	}
	return true
}

func authReconciliationPoolField(
	t *testing.T,
	attributes *datamodel.JobAttributes,
	poolUUID, field string,
) string {
	t.Helper()
	require.NotNil(t, attributes)
	require.NotNil(t, attributes.PayloadAttributes)
	rawResults, ok := attributes.PayloadAttributes["results"].([]interface{})
	require.True(t, ok)
	for _, rawResult := range rawResults {
		result, ok := rawResult.(map[string]interface{})
		require.True(t, ok)
		if result["poolUuid"] != poolUUID {
			continue
		}
		value, _ := result[field].(string)
		return value
	}
	t.Fatalf("pool %s not found in auth reconciliation results", poolUUID)
	return ""
}

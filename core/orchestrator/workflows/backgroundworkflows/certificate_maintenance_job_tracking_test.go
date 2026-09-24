package backgroundworkflows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func certificateMaintenanceTestActivityContext(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})
}

func updateCertificateMaintenanceJobTestWorkflow(
	ctx workflow.Context,
	jobID string,
	state datamodel.JobState,
	errorMessage string,
) error {
	ctx = certificateMaintenanceTestActivityContext(ctx)
	var jobErr error
	if errorMessage != "" {
		jobErr = errors.New(errorMessage)
	}
	return updateCertificateMaintenanceJob(ctx, jobID, state, jobErr)
}

func createCertificateMaintenanceChildJobTestWorkflow(
	ctx workflow.Context,
	parentJobID, poolUUID, workflowID string,
) (string, error) {
	ctx = certificateMaintenanceTestActivityContext(ctx)
	return createCertificateMaintenanceChildJob(
		ctx,
		parentJobID,
		poolUUID,
		workflowID,
		datamodel.JobTypeForcePoolCertificateRotation,
		coremodels.CertificateMaintenanceOperationForcedRotation,
	)
}

func waitForCertificateMaintenanceJobsTestWorkflow(
	ctx workflow.Context,
	childJobIDs []string,
	timeout time.Duration,
) error {
	ctx = certificateMaintenanceTestActivityContext(ctx)
	return waitForCertificateMaintenanceJobs(ctx, childJobIDs, timeout)
}

func finishCertificateMaintenanceJobTestWorkflow(
	ctx workflow.Context,
	jobID, runErrorMessage string,
) error {
	ctx = certificateMaintenanceTestActivityContext(ctx)
	var runErr error
	if runErrorMessage != "" {
		runErr = errors.New(runErrorMessage)
	}
	return finishCertificateMaintenanceJob(ctx, jobID, runErr)
}

func TestUpdateCertificateMaintenanceJob(t *testing.T) {
	tests := []struct {
		name         string
		state        datamodel.JobState
		errorMessage string
	}{
		{name: "processing", state: datamodel.JobsStatePROCESSING},
		{name: "error details", state: datamodel.JobsStateERROR, errorMessage: "rotation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			commonActivity := &activities.CommonActivities{}
			env.OnActivity(
				commonActivity.UpdateJobStatus,
				mock.Anything,
				mock.MatchedBy(func(job *datamodel.Job) bool {
					return job.UUID == "job-1" &&
						job.State == string(test.state) &&
						job.ErrorDetails == test.errorMessage
				}),
			).Return(nil).Once()

			env.ExecuteWorkflow(
				updateCertificateMaintenanceJobTestWorkflow,
				"job-1",
				test.state,
				test.errorMessage,
			)

			require.NoError(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestCreateCertificateMaintenanceChildJob(t *testing.T) {
	const (
		parentJobID = "parent-job"
		poolUUID    = "pool-1"
		workflowID  = "workflow-1"
	)
	t.Run("success", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.OnActivity(
			commonActivity.CreateJobWithWorkflowID,
			mock.Anything,
			mock.MatchedBy(func(job *datamodel.Job) bool {
				return job.Type == string(datamodel.JobTypeForcePoolCertificateRotation) &&
					job.State == string(datamodel.JobsStateNEW) &&
					job.IsAdminJob &&
					job.ResourceUUID == poolUUID &&
					job.JobAttributes != nil &&
					job.JobAttributes.ParentJobID == parentJobID &&
					job.JobAttributes.Operation == string(coremodels.CertificateMaintenanceOperationForcedRotation)
			}),
			workflowID,
		).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "child-job"}}, nil).Once()

		env.ExecuteWorkflow(
			createCertificateMaintenanceChildJobTestWorkflow,
			parentJobID,
			poolUUID,
			workflowID,
		)

		require.NoError(t, env.GetWorkflowError())
		var childJobID string
		require.NoError(t, env.GetWorkflowResult(&childJobID))
		assert.Equal(t, "child-job", childJobID)
		env.AssertExpectations(t)
	})

	tests := []struct {
		name        string
		createdJob  *datamodel.Job
		activityErr error
		wantError   string
	}{
		{
			name:        "activity failure",
			activityErr: temporal.NewNonRetryableApplicationError("create failed", "test", nil),
			wantError:   "create certificate maintenance child job",
		},
		{
			name:       "missing job ID",
			createdJob: &datamodel.Job{},
			wantError:  "returned no job ID",
		},
		{
			name:      "nil job",
			wantError: "returned no job ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			commonActivity := &activities.CommonActivities{}
			env.OnActivity(
				commonActivity.CreateJobWithWorkflowID,
				mock.Anything,
				mock.Anything,
				workflowID,
			).Return(test.createdJob, test.activityErr).Once()

			env.ExecuteWorkflow(
				createCertificateMaintenanceChildJobTestWorkflow,
				parentJobID,
				poolUUID,
				workflowID,
			)

			require.ErrorContains(t, env.GetWorkflowError(), test.wantError)
			env.AssertExpectations(t)
		})
	}
}

func TestWaitForCertificateMaintenanceJobs(t *testing.T) {
	t.Run("empty list skips activity", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		env.ExecuteWorkflow(
			waitForCertificateMaintenanceJobsTestWorkflow,
			[]string(nil),
			time.Minute,
		)
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("waits for child jobs", func(t *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		childJobIDs := []string{"child-1", "child-2"}
		env.OnActivity(
			commonActivity.WaitForJobs,
			mock.Anything,
			childJobIDs,
			time.Minute,
		).Return(nil).Once()

		env.ExecuteWorkflow(
			waitForCertificateMaintenanceJobsTestWorkflow,
			childJobIDs,
			time.Minute,
		)

		require.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestCertificateMaintenanceWaitActivityOptions(t *testing.T) {
	t.Run("covers tracking deadline and heartbeats", func(t *testing.T) {
		assert.Equal(t, 55*time.Minute, certificateMaintenanceTrackingTimeout)
		opts := certificateMaintenanceWaitActivityOptions(certificateMaintenanceTrackingTimeout)
		assert.Equal(t, certificateMaintenanceTrackingTimeout+certificateMaintenanceWaitTimeoutBuffer, opts.StartToCloseTimeout)
		assert.Equal(t, certificateMaintenanceWaitHeartbeatTimeout, opts.HeartbeatTimeout)
		require.NotNil(t, opts.RetryPolicy)
		assert.Equal(t, int32(2), opts.RetryPolicy.MaximumAttempts)
	})

	t.Run("falls back to tracking timeout when unset", func(t *testing.T) {
		opts := certificateMaintenanceWaitActivityOptions(0)
		assert.Equal(t, certificateMaintenanceTrackingTimeout+certificateMaintenanceWaitTimeoutBuffer, opts.StartToCloseTimeout)
	})
}

func captureCertificateMaintenanceWaitStartToCloseTimeout(ctx context.Context) (time.Duration, error) {
	return activity.GetInfo(ctx).StartToCloseTimeout, nil
}

func waitForCertificateMaintenanceJobsOverridesParentTimeoutTestWorkflow(ctx workflow.Context) (time.Duration, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	})
	ctx = workflow.WithActivityOptions(ctx, certificateMaintenanceWaitActivityOptions(certificateMaintenanceTrackingTimeout))
	var startToClose time.Duration
	err := workflow.ExecuteActivity(ctx, captureCertificateMaintenanceWaitStartToCloseTimeout).Get(ctx, &startToClose)
	return startToClose, err
}

func TestWaitForCertificateMaintenanceJobs_OverridesRotationActivityTimeout(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(captureCertificateMaintenanceWaitStartToCloseTimeout)

	env.ExecuteWorkflow(waitForCertificateMaintenanceJobsOverridesParentTimeoutTestWorkflow)

	require.NoError(t, env.GetWorkflowError())
	var startToClose time.Duration
	require.NoError(t, env.GetWorkflowResult(&startToClose))
	assert.Equal(t, certificateMaintenanceTrackingTimeout+certificateMaintenanceWaitTimeoutBuffer, startToClose)
	assert.Greater(t, startToClose, 5*time.Minute)
}

func TestFinishCertificateMaintenanceJob(t *testing.T) {
	tests := []struct {
		name              string
		runErrorMessage   string
		updateErr         error
		wantState         datamodel.JobState
		wantWorkflowError string
	}{
		{name: "success", wantState: datamodel.JobsStateDONE},
		{
			name:              "preserves run failure",
			runErrorMessage:   "rotation failed",
			wantState:         datamodel.JobsStateERROR,
			wantWorkflowError: "rotation failed",
		},
		{
			name:              "status update failure",
			updateErr:         temporal.NewNonRetryableApplicationError("update failed", "test", nil),
			wantState:         datamodel.JobsStateDONE,
			wantWorkflowError: "update parent job status",
		},
		{
			name:              "combines run and update failures",
			runErrorMessage:   "rotation failed",
			updateErr:         temporal.NewNonRetryableApplicationError("update failed", "test", nil),
			wantState:         datamodel.JobsStateERROR,
			wantWorkflowError: "rotation failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			commonActivity := &activities.CommonActivities{}
			env.OnActivity(
				commonActivity.UpdateJobStatus,
				mock.Anything,
				mock.MatchedBy(func(job *datamodel.Job) bool {
					return job.UUID == "job-1" &&
						job.State == string(test.wantState) &&
						job.ErrorDetails == test.runErrorMessage
				}),
			).Return(test.updateErr).Once()

			env.ExecuteWorkflow(
				finishCertificateMaintenanceJobTestWorkflow,
				"job-1",
				test.runErrorMessage,
			)

			if test.wantWorkflowError != "" {
				require.ErrorContains(t, env.GetWorkflowError(), test.wantWorkflowError)
			} else {
				require.NoError(t, env.GetWorkflowError())
			}
			env.AssertExpectations(t)
		})
	}
}

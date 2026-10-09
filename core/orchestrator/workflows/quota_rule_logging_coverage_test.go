package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newQuotaRuleLoggingTestEnvironment(t *testing.T) (*testsuite.TestWorkflowEnvironment, *activities.CommonActivities, *activities.QuotaRuleCommonActivity) {
	t.Helper()

	var testSuite testsuite.WorkflowTestSuite
	env := testSuite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, err := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	assert.NoError(t, err)
	env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})

	mockStorage := database.NewMockStorage(t)
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	quotaRuleCommonActivity := &activities.QuotaRuleCommonActivity{SE: mockStorage}
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(quotaRuleCommonActivity.GetVolumeByID)
	env.RegisterActivity(quotaRuleCommonActivity.UpdateQuotaRuleState)

	return env, commonActivity, quotaRuleCommonActivity
}

func TestCreateQuotaRuleWorkflow_JobFailureLogging(t *testing.T) {
	tests := []struct {
		name           string
		configureMocks func(*testsuite.TestWorkflowEnvironment, *activities.CommonActivities, *activities.QuotaRuleCommonActivity)
	}{
		{
			name: "ensure job state fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(nil, errors.New("get job failed"))
			},
		},
		{
			name: "processing job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("processing update failed"))
			},
		},
		{
			name: "error job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, quotaRuleCommonActivity *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Once()
				env.OnActivity(quotaRuleCommonActivity.GetVolumeByID, mock.Anything, int64(1), int64(1)).Return(nil, errors.New("volume lookup failed"))
				env.OnActivity(quotaRuleCommonActivity.UpdateQuotaRuleState, mock.Anything, mock.Anything, false).Return(nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("error update failed")).Once()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env, commonActivity, quotaRuleCommonActivity := newQuotaRuleLoggingTestEnvironment(t)
			test.configureMocks(env, commonActivity, quotaRuleCommonActivity)

			env.ExecuteWorkflow(CreateQuotaRuleWorkflow,
				&common.CreateQuotaRulesParam{ProjectId: "project", LocationId: "location"},
				newQuotaRuleLoggingQuotaRule(datamodel.LifeCycleStateCreating))

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestDeleteQuotaRuleWorkflow_JobFailureLogging(t *testing.T) {
	tests := []struct {
		name           string
		configureMocks func(*testsuite.TestWorkflowEnvironment, *activities.CommonActivities, *activities.QuotaRuleCommonActivity)
	}{
		{
			name: "ensure job state fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(nil, errors.New("get job failed"))
			},
		},
		{
			name: "processing job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("processing update failed"))
			},
		},
		{
			name: "error job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, quotaRuleCommonActivity *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Once()
				env.OnActivity(quotaRuleCommonActivity.GetVolumeByID, mock.Anything, int64(1), int64(1)).Return(nil, errors.New("volume lookup failed"))
				env.OnActivity(quotaRuleCommonActivity.UpdateQuotaRuleState, mock.Anything, mock.Anything, false).Return(nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("error update failed")).Once()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env, commonActivity, quotaRuleCommonActivity := newQuotaRuleLoggingTestEnvironment(t)
			test.configureMocks(env, commonActivity, quotaRuleCommonActivity)

			env.ExecuteWorkflow(DeleteQuotaRuleWorkflow,
				&common.DeleteQuotaRulesParam{ProjectId: "project", LocationId: "location"},
				newQuotaRuleLoggingQuotaRule(datamodel.LifeCycleStateDeleting))

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func TestUpdateQuotaRuleWorkflow_JobFailureLogging(t *testing.T) {
	tests := []struct {
		name           string
		configureMocks func(*testsuite.TestWorkflowEnvironment, *activities.CommonActivities, *activities.QuotaRuleCommonActivity)
	}{
		{
			name: "ensure job state fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(nil, errors.New("get job failed"))
			},
		},
		{
			name: "processing job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, _ *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("processing update failed"))
			},
		},
		{
			name: "error job update fails",
			configureMocks: func(env *testsuite.TestWorkflowEnvironment, commonActivity *activities.CommonActivities, quotaRuleCommonActivity *activities.QuotaRuleCommonActivity) {
				env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(newQuotaRuleLoggingJob(), nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Once()
				env.OnActivity(quotaRuleCommonActivity.GetVolumeByID, mock.Anything, int64(1), int64(1)).Return(nil, errors.New("volume lookup failed"))
				env.OnActivity(quotaRuleCommonActivity.UpdateQuotaRuleState, mock.Anything, mock.Anything, false).Return(nil)
				env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("error update failed")).Once()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env, commonActivity, quotaRuleCommonActivity := newQuotaRuleLoggingTestEnvironment(t)
			test.configureMocks(env, commonActivity, quotaRuleCommonActivity)

			env.ExecuteWorkflow(UpdateQuotaRuleWorkflow,
				&common.UpdateQuotaRulesParam{ProjectId: "project", LocationId: "location"},
				newQuotaRuleLoggingQuotaRule(datamodel.LifeCycleStateUpdating))

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}

func newQuotaRuleLoggingJob() *datamodel.Job {
	return &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
}

func newQuotaRuleLoggingQuotaRule(state string) *datamodel.QuotaRule {
	return &datamodel.QuotaRule{
		BaseModel: datamodel.BaseModel{UUID: "quota-rule-id"},
		VolumeID:  1,
		AccountID: 1,
		State:     state,
	}
}

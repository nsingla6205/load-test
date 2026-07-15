package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestHostGroupUpdateWorkflowWorkflow(t *testing.T) {
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
	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.HostGroupUpdateActivity{})

	hg := &datamodel.HostGroup{
		Name:        "test-hostgroup",
		Description: "Test Host Group",
		OSType:      "linux",
		Hosts: datamodel.Hosts{
			Hosts: []string{"host1", "host2"},
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStateNEW)}, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateIGroups", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateHostGroupState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(UpdateHostGroupWorkflow, hg)

	_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
	if err != nil {
		t.Fatalf("Failed to query workflow: %v", err)
	}

	// Assert workflow execution
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHostGroupUpdateWorkflowWorkflow_UpdateJobStatusFails(t *testing.T) {
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
	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.HostGroupUpdateActivity{})

	hg := &datamodel.HostGroup{
		Name:        "test-hostgroup",
		Description: "Test Host Group",
		OSType:      "linux",
		Hosts: datamodel.Hosts{
			Hosts: []string{"host1", "host2"},
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStateNEW)}, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("some error"))

	// Execute workflow
	env.ExecuteWorkflow(UpdateHostGroupWorkflow, hg)

	_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
	if err != nil {
		t.Fatalf("Failed to query workflow: %v", err)
	}

	// Assert workflow execution
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHostGroupUpdateWorkflowWorkflow_UpdateIGroupsFails(t *testing.T) {
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
	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.HostGroupUpdateActivity{})

	hg := &datamodel.HostGroup{
		Name:        "test-hostgroup",
		Description: "Test Host Group",
		OSType:      "linux",
		Hosts: datamodel.Hosts{
			Hosts: []string{"host1", "host2"},
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStateNEW)}, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateIGroups", mock.Anything, mock.Anything).Return(errors.New("some error"))
	env.OnActivity("UpdateHostGroupState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	env.ExecuteWorkflow(UpdateHostGroupWorkflow, hg)

	_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
	if err != nil {
		t.Fatalf("Failed to query workflow: %v", err)
	}

	// Assert workflow execution
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// newHostGroupWorkflowTestEnv builds a test workflow environment with the standard
// header/propagator and the activities required by UpdateHostGroupWorkflow registered.
func newHostGroupWorkflowTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.HostGroupUpdateActivity{})
	return env
}

func newHostGroupWorkflowTestInput() *datamodel.HostGroup {
	return &datamodel.HostGroup{
		Name:    "test-hostgroup",
		OSType:  "linux",
		Hosts:   datamodel.Hosts{Hosts: []string{"host1", "host2"}},
		Account: &datamodel.Account{Name: "test-account"},
	}
}

// TestHostGroupUpdateWorkflow_EnsureJobStateError verifies the workflow aborts before
// processing when the job is no longer in NEW state (stale/duplicate run guard).
func TestHostGroupUpdateWorkflow_EnsureJobStateError(t *testing.T) {
	env := newHostGroupWorkflowTestEnv(t)

	// Job is already PROCESSING, so EnsureJobState(NEW) must fail.
	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStatePROCESSING)}, nil)

	env.ExecuteWorkflow(UpdateHostGroupWorkflow, newHostGroupWorkflowTestInput())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	// UpdateJobStatus / UpdateIGroups must not run once the guard fails.
	env.AssertNotCalled(t, "UpdateIGroups", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestHostGroupUpdateWorkflow_SetReadyStateFails verifies that a failure while moving the
// host group back to READY (after a successful igroup sync) surfaces as a workflow error.
func TestHostGroupUpdateWorkflow_SetReadyStateFails(t *testing.T) {
	env := newHostGroupWorkflowTestEnv(t)

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStateNEW)}, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateIGroups", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateHostGroupState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("state update failed"))

	env.ExecuteWorkflow(UpdateHostGroupWorkflow, newHostGroupWorkflowTestInput())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestHostGroupUpdateWorkflow_UpdateIGroupsFails_SetErrorStateAlsoFails verifies that when
// the igroup sync fails and the follow-up ERROR-state write also fails, the workflow still
// completes with the original igroup error (the state write error is only logged).
func TestHostGroupUpdateWorkflow_UpdateIGroupsFails_SetErrorStateAlsoFails(t *testing.T) {
	env := newHostGroupWorkflowTestEnv(t)

	env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{State: string(datamodel.JobsStateNEW)}, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)
	env.OnActivity("UpdateIGroups", mock.Anything, mock.Anything).Return(errors.New("igroup sync failed"))
	env.OnActivity("UpdateHostGroupState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("state update failed"))

	env.ExecuteWorkflow(UpdateHostGroupWorkflow, newHostGroupWorkflowTestInput())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

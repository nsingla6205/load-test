package temporalbackend

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/api/enums/v1"
)

func TestToTemporalRetryPolicy_Nil(t *testing.T) {
	assert.Nil(t, toTemporalRetryPolicy(nil))
}

func TestToTemporalRetryPolicy_MapsFields(t *testing.T) {
	rp := &orchestrator.RetryPolicy{
		InitialInterval:    2 * time.Second,
		BackoffCoefficient: 1.7,
		MaximumInterval:    30 * time.Second,
		MaximumAttempts:    8,
	}
	got := toTemporalRetryPolicy(rp)
	require.NotNil(t, got)
	assert.Equal(t, 2*time.Second, got.InitialInterval)
	assert.Equal(t, 1.7, got.BackoffCoefficient)
	assert.Equal(t, 30*time.Second, got.MaximumInterval)
	assert.Equal(t, int32(8), got.MaximumAttempts)
}

func TestToTemporalActivityOptions_MapsAllFields(t *testing.T) {
	opts := orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 45 * time.Second,
		ActivityID:             "act-9",
		HeartbeatTimeout:       12 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 3},
	}
	got := toTemporalActivityOptions(opts)
	assert.Equal(t, 45*time.Second, got.ScheduleToCloseTimeout)
	assert.Equal(t, "act-9", got.ActivityID)
	assert.Equal(t, 12*time.Second, got.HeartbeatTimeout)
	require.NotNil(t, got.RetryPolicy)
	assert.Equal(t, int32(3), got.RetryPolicy.MaximumAttempts)
}

func TestToTemporalActivityOptions_NilRetryPolicy(t *testing.T) {
	got := toTemporalActivityOptions(orchestrator.ActivityOptions{ScheduleToCloseTimeout: time.Second})
	assert.Nil(t, got.RetryPolicy)
}

func TestToTemporalChildOptions_MapsFieldsAndParentClosePolicy(t *testing.T) {
	opts := orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: time.Minute,
		WorkflowID:               "child-id",
		RetryPolicy:              &orchestrator.RetryPolicy{MaximumAttempts: 2},
	}
	got := toTemporalChildOptions(opts)
	assert.Equal(t, time.Minute, got.WorkflowExecutionTimeout)
	assert.Equal(t, "child-id", got.WorkflowID)
	assert.Equal(t, enums.PARENT_CLOSE_POLICY_TERMINATE, got.ParentClosePolicy)
	require.NotNil(t, got.RetryPolicy)
	assert.Equal(t, int32(2), got.RetryPolicy.MaximumAttempts)
}

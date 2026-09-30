package vcm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func poolUpdateRecoveryPayload(t *testing.T) string {
	t.Helper()
	params := &commonparams.VCMUpdatePoolParams{
		UpdateMask: commonparams.VCMUpdateMaskProvisionedSizeInBytes,
		ChangeType: "SCALE",
	}
	params.DeploymentID = "gcnv-1"
	params.RequestID = "op-1"
	params.PrimaryZone = "us-central1-a"
	params.SizeInBytes = 2 << 40
	b, err := json.Marshal(params)
	require.NoError(t, err)
	return string(b)
}

func poolUpdateRecoveryJob(payload string) *vcmdb.Job {
	return &vcmdb.Job{
		ID:             "op-1",
		ResourceID:     "gcnv-1",
		Type:           vcmdb.JobTypePoolUpdate,
		State:          vcmdb.JobStateProcessing,
		RequestPayload: payload,
	}
}

func TestUpdatePoolRecoveryHandler_Resume_ReDispatchesUnderJobID(t *testing.T) {
	starter := &fakeStarter{}
	h := NewUpdatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolUpdateRecoveryJob(poolUpdateRecoveryPayload(t)))

	require.NoError(t, err)
	assert.Equal(t, "op-1", starter.gotInstance,
		"resume must re-dispatch the workflow under the job's own id so it drives its own job")

	require.Len(t, starter.gotArgs, 1)
	params, ok := starter.gotArgs[0].(*commonparams.UpdatePoolParams)
	require.True(t, ok, "resume must dispatch the update-pool workflow input")
	require.NotNil(t, params.VCM)
	assert.Equal(t, "gcnv-1", params.VCM.DeploymentID)
	assert.Equal(t, "SCALE", params.VCM.ChangeType)
	assert.Equal(t, commonparams.VCMUpdateMaskProvisionedSizeInBytes, params.VCM.UpdateMask)
	require.NotNil(t, params.VCM.Derived, "derived inputs must be recomputed from the persisted request")
	assert.Equal(t, "us-central1", params.VCM.Derived.Region)
	assert.Equal(t, "gcnv-1", orchestrator.DeploymentIDFromContext(starter.gotCtx))
}

func TestUpdatePoolRecoveryHandler_Resume_AlreadyRunning_IsNoOp(t *testing.T) {
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	h := NewUpdatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolUpdateRecoveryJob(poolUpdateRecoveryPayload(t)))

	require.NoError(t, err, "a workflow already running for this job id is a successful no-op")
}

func TestUpdatePoolRecoveryHandler_Resume_DispatchError_IsWrapped(t *testing.T) {
	starter := &fakeStarter{err: errors.New("backend down")}
	h := NewUpdatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolUpdateRecoveryJob(poolUpdateRecoveryPayload(t)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend down")
}

func TestUpdatePoolRecoveryHandler_Resume_RejectsUnrecoverableJobs(t *testing.T) {
	tests := []struct {
		name string
		job  *vcmdb.Job
	}{
		{
			name: "nil job",
			job:  nil,
		},
		{
			name: "create-pool job belongs to the create handler",
			job: &vcmdb.Job{
				ID: "op-1", Type: vcmdb.JobTypePoolCreate, State: vcmdb.JobStateProcessing,
				RequestPayload: poolUpdateRecoveryPayload(t),
			},
		},
		{
			name: "empty payload",
			job: &vcmdb.Job{
				ID: "op-2", Type: vcmdb.JobTypePoolUpdate, State: vcmdb.JobStateProcessing,
			},
		},
		{
			name: "malformed payload",
			job:  poolUpdateRecoveryJob("{not-json"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			h := NewUpdatePoolRecoveryHandler(starter, log.NewLogger())

			err := h.Resume(context.Background(), tc.job)

			require.Error(t, err)
			assert.False(t, starter.called, "an unrecoverable job must not be re-dispatched")
		})
	}
}

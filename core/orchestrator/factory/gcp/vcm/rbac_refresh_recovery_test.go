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
)

func rbacRefreshRecoveryJob(t *testing.T) *vcmdb.Job {
	t.Helper()
	payload, err := json.Marshal(&commonparams.VCMCreatePoolParams{
		DeploymentID:          "gcnv-1",
		RequestID:             "op-1",
		PrimaryZone:           "us-central1-a",
		OntapModeRbacFileURL:  "gs://bucket/rbac.txt",
		OntapModeRbacChecksum: "abc123",
	})
	require.NoError(t, err)
	return &vcmdb.Job{
		ID:             "op-1",
		ResourceID:     "gcnv-1",
		Type:           vcmdb.JobTypeOntapModeRbacRefresh,
		State:          vcmdb.JobStateProcessing,
		RequestPayload: string(payload),
	}
}

func TestRbacRefreshRecoveryHandler_Resume_ReDispatchesUnderDeploymentID(t *testing.T) {
	starter := &fakeStarter{}
	h := NewRbacRefreshRecoveryHandler(starter, log.NewLogger())

	require.NoError(t, h.Resume(context.Background(), rbacRefreshRecoveryJob(t)))

	assert.Equal(t, "gcnv-1", starter.gotInstance, "resume must re-dispatch under the deployment id, matching the primary dispatch key")
	require.Len(t, starter.gotArgs, 1)
	params, ok := starter.gotArgs[0].(*commonparams.RefreshRbacForPoolParams)
	require.True(t, ok, "resume must dispatch the RBAC refresh workflow input")
	require.NotNil(t, params.VCM)
	assert.Equal(t, "gcnv-1", params.VCM.DeploymentID)
	assert.Equal(t, "gs://bucket/rbac.txt", params.VCM.OntapModeRbacFileURL)
	assert.Equal(t, "abc123", params.VCM.OntapModeRbacChecksum)
	require.NotNil(t, params.VCM.Derived, "derived inputs must be recomputed from the persisted request")
}

func TestRbacRefreshRecoveryHandler_Resume_AlreadyRunningIsNoop(t *testing.T) {
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	h := NewRbacRefreshRecoveryHandler(starter, log.NewLogger())

	assert.NoError(t, h.Resume(context.Background(), rbacRefreshRecoveryJob(t)))
}

func TestRbacRefreshRecoveryHandler_Resume_Errors(t *testing.T) {
	tests := []struct {
		name    string
		job     func(t *testing.T) *vcmdb.Job
		starter *fakeStarter
		wantMsg string
	}{
		{
			name:    "nil job",
			job:     func(*testing.T) *vcmdb.Job { return nil },
			starter: &fakeStarter{},
			wantMsg: "job is nil",
		},
		{
			name: "wrong job type",
			job: func(t *testing.T) *vcmdb.Job {
				j := rbacRefreshRecoveryJob(t)
				j.Type = vcmdb.JobTypePoolUpdate
				return j
			},
			starter: &fakeStarter{},
			wantMsg: "unexpected job type",
		},
		{
			name: "empty payload",
			job: func(t *testing.T) *vcmdb.Job {
				j := rbacRefreshRecoveryJob(t)
				j.RequestPayload = ""
				return j
			},
			starter: &fakeStarter{},
			wantMsg: "no request payload",
		},
		{
			name: "malformed payload",
			job: func(t *testing.T) *vcmdb.Job {
				j := rbacRefreshRecoveryJob(t)
				j.RequestPayload = "{"
				return j
			},
			starter: &fakeStarter{},
			wantMsg: "failed to unmarshal",
		},
		{
			name:    "dispatch failure",
			job:     rbacRefreshRecoveryJob,
			starter: &fakeStarter{err: errors.New("boom")},
			wantMsg: "boom",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewRbacRefreshRecoveryHandler(tc.starter, log.NewLogger())

			err := h.Resume(context.Background(), tc.job(t))

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}

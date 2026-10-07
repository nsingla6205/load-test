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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func poolRecoveryPayload(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(&commonparams.VCMCreatePoolParams{
		DeploymentID: "gcnv-1",
		RequestID:    "op-1",
		PrimaryZone:  "us-central1-a",
	})
	require.NoError(t, err)
	return string(b)
}

func poolRecoveryJob(payload string) *vcmdb.Job {
	return &vcmdb.Job{
		ID:             "op-1",
		ResourceID:     "gcnv-1",
		Type:           vcmdb.JobTypePoolCreate,
		State:          vcmdb.JobStateProcessing,
		RequestPayload: payload,
	}
}

func TestCreatePoolRecoveryHandler_Resume_ReDispatchesUnderJobID(t *testing.T) {
	starter := &fakeStarter{}
	h := NewCreatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolRecoveryJob(poolRecoveryPayload(t)))

	require.NoError(t, err)
	assert.True(t, starter.called)
	assert.Equal(t, "op-1", starter.gotInstance,
		"resume must re-dispatch the workflow under the job's own id so it drives its own job")
}

func TestCreatePoolRecoveryHandler_Resume_AlreadyRunning_IsNoOp(t *testing.T) {
	starter := &fakeStarter{err: cbackend.ErrInstanceAlreadyExists}
	h := NewCreatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolRecoveryJob(poolRecoveryPayload(t)))

	require.NoError(t, err, "a workflow already running for this job id is a successful no-op")
}

func TestCreatePoolRecoveryHandler_Resume_DispatchError_IsWrapped(t *testing.T) {
	starter := &fakeStarter{err: errors.New("backend down")}
	h := NewCreatePoolRecoveryHandler(starter, log.NewLogger())

	err := h.Resume(context.Background(), poolRecoveryJob(poolRecoveryPayload(t)))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "backend down")
}

func TestCreatePoolRecoveryHandler_Resume_RestoresOntapModeInputs(t *testing.T) {
	b, err := json.Marshal(&commonparams.VCMCreatePoolParams{
		DeploymentID:          "gcnv-1",
		RequestID:             "op-1",
		PrimaryZone:           "us-central1-a",
		CaURI:                 "ca-uri",
		Mode:                  commonparams.ONTAPMode,
		OntapModeRbacFileURL:  "gs://rbac-bucket/ontap-mode.yaml",
		OntapModeRbacChecksum: "rbac-checksum",
		OntapCredentials: []commonparams.VCMOntapCredential{
			{Type: "default", SecretID: "admin-secret"},
			{Type: "ontap-mode", Username: "mode-user", SecretID: "mode-secret"},
		},
		// Stale derived inputs: Resume must recompute them from the request.
		Derived: &commonparams.VCMDerivedPoolInputs{},
	})
	require.NoError(t, err)

	starter := &fakeStarter{}
	h := NewCreatePoolRecoveryHandler(starter, log.NewLogger())

	require.NoError(t, h.Resume(context.Background(), poolRecoveryJob(string(b))))

	require.Len(t, starter.gotArgs, 1)
	params, ok := starter.gotArgs[0].(*commonparams.CreatePoolParams)
	require.True(t, ok, "expected *CreatePoolParams, got %T", starter.gotArgs[0])
	require.NotNil(t, params.VCM)
	vcm := params.VCM

	assert.Equal(t, commonparams.ONTAPMode, vcm.Mode)
	assert.Equal(t, "gs://rbac-bucket/ontap-mode.yaml", vcm.OntapModeRbacFileURL)
	assert.Equal(t, "rbac-checksum", vcm.OntapModeRbacChecksum)

	require.NotNil(t, vcm.Derived)
	assert.True(t, vcm.Derived.IsOntapMode)
	require.NotNil(t, vcm.Derived.OntapModeCredentials)
	assert.Equal(t, "mode-user", vcm.Derived.OntapModeCredentials.Username)
	assert.Equal(t, "mode-secret", vcm.Derived.OntapModeCredentials.SecretID)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, vcm.Derived.OntapModeCredentials.AuthType)
	assert.Equal(t, "ca-uri", vcm.Derived.OntapModeCredentials.CaURI)
}

func TestCreatePoolRecoveryHandler_Resume_RejectsUnrecoverableJobs(t *testing.T) {
	tests := []struct {
		name string
		job  *vcmdb.Job
	}{
		{
			name: "nil job",
			job:  nil,
		},
		{
			name: "wrong type",
			job: &vcmdb.Job{
				ID: "op-1", Type: "OTHER", State: vcmdb.JobStateProcessing,
				RequestPayload: poolRecoveryPayload(t),
			},
		},
		{
			name: "empty payload",
			job: &vcmdb.Job{
				ID: "op-2", Type: vcmdb.JobTypePoolCreate, State: vcmdb.JobStateProcessing,
			},
		},
		{
			name: "malformed payload",
			job:  poolRecoveryJob("{not-json"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			starter := &fakeStarter{}
			h := NewCreatePoolRecoveryHandler(starter, log.NewLogger())

			err := h.Resume(context.Background(), tc.job)

			require.Error(t, err)
			assert.False(t, starter.called, "an unrecoverable job must not be re-dispatched")
		})
	}
}

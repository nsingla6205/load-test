package vcm_workflows

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

func TestJobErrorFields(t *testing.T) {
	t.Run("nil error yields zero values", func(t *testing.T) {
		tid, msg, retriable := jobErrorFields(nil)
		assert.Equal(t, 0, tid)
		assert.Empty(t, msg)
		assert.False(t, retriable)
	})

	t.Run("plain error preserves message with zero tracking id", func(t *testing.T) {
		tid, msg, retriable := jobErrorFields(errors.New("boom"))
		assert.Equal(t, 0, tid)
		assert.Equal(t, "boom", msg)
		assert.False(t, retriable)
	})

	t.Run("wrapped custom error surfaces tracking id and underlying provider text", func(t *testing.T) {
		custom := vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("bad input"))
		wrapped := fmt.Errorf("provisioning failed: %w", custom)
		tid, msg, retriable := jobErrorFields(wrapped)
		assert.Equal(t, vsaerrors.ErrInputValidationError, tid)
		assert.Equal(t, "bad input", msg)
		assert.Equal(t, custom.IsRetriable(), retriable)
	})

	t.Run("orchestrator application error persists underlying cause text", func(t *testing.T) {
		appErr := orchestrator.NewNonRetryableApplicationError(
			"perm failure", vsaerrors.CustomErrorType, errors.New("cause"), vsaerrors.ErrInputValidationError)
		tid, msg, retriable := jobErrorFields(appErr)
		assert.Equal(t, vsaerrors.ErrInputValidationError, tid)
		assert.Equal(t, "cause", msg)
		assert.False(t, retriable, "non-retryable application error must not be marked retriable")
	})

	t.Run("orchestrator application error from apperror persists original provider text", func(t *testing.T) {
		originalErr := errors.New("snapshot with that name already exists")
		custom := vsaerrors.NewVCPError(vsaerrors.ErrCreateSnapshotConflict, originalErr)
		appErr := apperror.WrapAsAppError(custom, true)

		tid, msg, retriable := jobErrorFields(appErr)
		assert.Equal(t, vsaerrors.ErrCreateSnapshotConflict, tid)
		assert.Equal(t, originalErr.Error(), msg)
		assert.NotEqual(t, custom.Error(), msg)
		assert.False(t, retriable)
	})
}

func TestMapQosType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "manual", in: "MANUAL", want: utils.QosTypeManual},
		{name: "fixed maps to manual", in: "FIXED", want: utils.QosTypeManual},
		{name: "lowercase manual", in: "manual", want: utils.QosTypeManual},
		{name: "padded manual", in: "  manual  ", want: utils.QosTypeManual},
		{name: "auto", in: "auto", want: utils.QosTypeAuto},
		{name: "empty defaults to auto", in: "", want: utils.QosTypeAuto},
		{name: "unknown defaults to auto", in: "something", want: utils.QosTypeAuto},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mapQosType(tc.in))
		})
	}
}

func TestOntapNodeCredentials(t *testing.T) {
	withEnv := func(t *testing.T, value string) {
		t.Helper()
		orig := runningEnv
		runningEnv = value
		t.Cleanup(func() { runningEnv = orig })
	}

	t.Run("local env uses configured username and password", func(t *testing.T) {
		withEnv(t, localEnv)
		origUser, origPw := vcmLocalOntapUsername, vcmLocalOntapPassword
		vcmLocalOntapUsername, vcmLocalOntapPassword = "local-admin", "local-admin-pw"
		t.Cleanup(func() { vcmLocalOntapUsername, vcmLocalOntapPassword = origUser, origPw })

		wf := &createPoolWorkflow{}
		creds := wf.ontapNodeCredentials()
		require.NotNil(t, creds)
		assert.Equal(t, "local-admin", creds.Username)
		assert.Equal(t, "local-admin-pw", creds.Password)
		assert.Equal(t, env.USERNAME_PWD, creds.AuthType)
	})

	t.Run("qualified secret id is reduced to a bare name", func(t *testing.T) {
		withEnv(t, "prod")
		inputs := &datamodel.PoolCredentials{
			SecretID: "projects/123/secrets/admin-pw/versions/latest",
			Username: "vsaadmin",
			AuthType: env.USERNAME_PWD_SEC_MGR,
		}
		wf := &createPoolWorkflow{inputs: &commonparams.VCMDerivedPoolInputs{OntapCredentials: inputs}}

		creds := wf.ontapNodeCredentials()
		require.NotNil(t, creds)
		assert.Equal(t, "admin-pw", creds.SecretID)
		assert.Equal(t, "vsaadmin", creds.Username)
		assert.Equal(t, env.USERNAME_PWD_SEC_MGR, creds.AuthType)

		assert.Equal(t, "projects/123/secrets/admin-pw/versions/latest", inputs.SecretID,
			"workflow inputs must keep the qualified name for the cluster-create path")
	})

	t.Run("certificate id is reduced alongside the secret id", func(t *testing.T) {
		withEnv(t, "prod")
		inputs := &datamodel.PoolCredentials{
			CertificateID: "projects/123/secrets/admin-cert",
			SecretID:      "projects/123/secrets/admin-key",
			AuthType:      env.USER_CERTIFICATE,
		}
		wf := &createPoolWorkflow{inputs: &commonparams.VCMDerivedPoolInputs{OntapCredentials: inputs}}

		creds := wf.ontapNodeCredentials()
		require.NotNil(t, creds)
		assert.Equal(t, "admin-cert", creds.CertificateID)
		assert.Equal(t, "admin-key", creds.SecretID)
		assert.Equal(t, env.USER_CERTIFICATE, creds.AuthType)
	})

	t.Run("nil credentials stay nil", func(t *testing.T) {
		withEnv(t, "prod")
		wf := &createPoolWorkflow{}
		assert.Nil(t, wf.ontapNodeCredentials())
	})
}

func TestCheckDeadline_UsesRequestDeadline(t *testing.T) {
	withDeadlineTestLogger(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	ctx := deadlineTestContext(now)

	t.Run("nil request is a no-op", func(t *testing.T) {
		wf := &createPoolWorkflow{}
		assert.Nil(t, wf.req)
		assert.NoError(t, checkDeadline(ctx, time.Time{}, "start"))
	})

	t.Run("elapsed deadline fails", func(t *testing.T) {
		wf := &createPoolWorkflow{
			req: &commonparams.VCMCreatePoolParams{
				Deadline: now.Add(-time.Minute),
			},
		}
		err := checkDeadline(ctx, wf.req.Deadline, "start")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrOperationDeadlineExceeded, customErr.TrackingID)
	})
}

func TestNodeMgmtIPFromVLMConfig(t *testing.T) {
	t.Run("returns first node-mgmt LIF IP", func(t *testing.T) {
		cfg := vlm.VLMConfig{
			Cloud: vlm.CloudConfig{
				HAPairs: []vlm.HAPair{
					{
						VM1: vlm.VMConfig{
							SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
								vlm.LIFTypeNodeMgmt: {IP: "10.0.0.5"},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, "10.0.0.5", nodeMgmtIPFromVLMConfig(cfg))
	})

	t.Run("returns empty when no node-mgmt LIF present", func(t *testing.T) {
		assert.Equal(t, "", nodeMgmtIPFromVLMConfig(vlm.VLMConfig{}))
	})
}

func TestSvmUUIDFromVLMConfig(t *testing.T) {
	t.Run("matches by map key", func(t *testing.T) {
		cfg := vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			"svm-1": {Svmname: "svm-1", Svmuuid: "uuid-key"},
		}}
		assert.Equal(t, "uuid-key", svmUUIDFromVLMConfig(cfg, "svm-1"))
	})

	t.Run("matches by svm name when key differs", func(t *testing.T) {
		cfg := vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			"other-key": {Svmname: "svm-1", Svmuuid: "uuid-name"},
		}}
		assert.Equal(t, "uuid-name", svmUUIDFromVLMConfig(cfg, "svm-1"))
	})

	t.Run("falls back to sole entry", func(t *testing.T) {
		cfg := vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			"k": {Svmname: "different", Svmuuid: "uuid-sole"},
		}}
		assert.Equal(t, "uuid-sole", svmUUIDFromVLMConfig(cfg, "svm-1"))
	})

	t.Run("empty map returns empty", func(t *testing.T) {
		assert.Equal(t, "", svmUUIDFromVLMConfig(vlm.VLMConfig{}, "svm-1"))
	})
}

func TestPoolCreateOperationResponsePayloadFn(t *testing.T) {
	payload, err := poolCreateOperationResponsePayloadFn(nil, vlm.VLMConfig{}, "")
	require.NoError(t, err)
	require.NotNil(t, payload)
	assert.JSONEq(t, `{"clusterShape":{},"clusterLayout":{"nodes":[],"disks":[],"activeZone":"","standbyZone":""}}`, *payload)
}

func TestPoolWorkflow_MissingParams_ReturnsError(t *testing.T) {
	tests := []struct {
		name   string
		params *commonparams.CreatePoolParams
	}{
		{name: "nil params", params: nil},
		{name: "nil VCM", params: &commonparams.CreatePoolParams{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// PoolWorkflow validates params before touching the engine context,
			// so a nil orchestrator context is never dereferenced here.
			_, err := PoolWorkflow(nil, tc.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "VCM params missing")
		})
	}
}

package vcm_workflows

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
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

func TestCheckDeadline_DisabledAlwaysNil(t *testing.T) {
	// Deadline enforcement is temporarily disabled; even a past/zero deadline
	// must not abort the workflow. ctx is unused by the disabled implementation.
	wf := &createPoolWorkflow{req: &commonparams.VCMCreatePoolParams{}}
	assert.NoError(t, wf.checkDeadline(nil, "start"))
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
	payload, err := poolCreateOperationResponsePayloadFn("gcnv-test")
	require.NoError(t, err)
	require.NotNil(t, payload)
	assert.JSONEq(t, `{"pool":{"deploymentId":"gcnv-test"}}`, *payload)
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

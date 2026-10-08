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

func TestOntapLicenseForCreate(t *testing.T) {
	tests := []struct {
		name      string
		secretURI string
		want      vlm.OntapLicense
	}{
		{
			name:      "empty uri falls back to VLM default license",
			secretURI: "",
			want:      vlm.OntapLicense{},
		},
		{
			name:      "unversioned secret uri is pinned to latest version",
			secretURI: "projects/237071556299/secrets/ontap-license-secret",
			want:      vlm.OntapLicense{SecretUri: []string{"projects/237071556299/secrets/ontap-license-secret/versions/latest"}},
		},
		{
			name:      "versioned secret uri is passed through unchanged",
			secretURI: "projects/237071556299/secrets/ontap-license-secret/versions/3",
			want:      vlm.OntapLicense{SecretUri: []string{"projects/237071556299/secrets/ontap-license-secret/versions/3"}},
		},
		{
			name:      "explicit latest version is not duplicated",
			secretURI: "projects/237071556299/secrets/ontap-license-secret/versions/latest",
			want:      vlm.OntapLicense{SecretUri: []string{"projects/237071556299/secrets/ontap-license-secret/versions/latest"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ontapLicenseForCreate(tt.secretURI))
		})
	}
}

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

	t.Run("qualified secret id is preserved for the resource-name-aware lookup", func(t *testing.T) {
		withEnv(t, "prod")
		inputs := &datamodel.PoolCredentials{
			SecretID: "projects/123/secrets/admin-pw/versions/latest",
			Username: "vsaadmin",
			AuthType: env.USERNAME_PWD_SEC_MGR,
		}
		wf := &createPoolWorkflow{inputs: &commonparams.VCMDerivedPoolInputs{OntapCredentials: inputs}}

		creds := wf.ontapNodeCredentials()
		require.NotNil(t, creds)
		assert.Equal(t, "projects/123/secrets/admin-pw/versions/latest", creds.SecretID,
			"the full password resource name is kept so the lookup resolves it in the tenant project")
		assert.Equal(t, "vsaadmin", creds.Username)
		assert.Equal(t, env.USERNAME_PWD_SEC_MGR, creds.AuthType)

		assert.Equal(t, "projects/123/secrets/admin-pw/versions/latest", inputs.SecretID,
			"workflow inputs must keep the qualified name for the cluster-create path")
	})

	t.Run("certificate id is reduced but the secret id is preserved", func(t *testing.T) {
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
		assert.Equal(t, "projects/123/secrets/admin-key", creds.SecretID)
		assert.Equal(t, env.USER_CERTIFICATE, creds.AuthType)
	})

	t.Run("nil credentials stay nil", func(t *testing.T) {
		withEnv(t, "prod")
		wf := &createPoolWorkflow{}
		assert.Nil(t, wf.ontapNodeCredentials())
	})
}

func TestValidateOntapModeInputs(t *testing.T) {
	valid := func() *createPoolWorkflow {
		return &createPoolWorkflow{
			req: &commonparams.VCMCreatePoolParams{
				OntapModeRbacFileURL:  "gs://bucket/rbac",
				OntapModeRbacChecksum: "checksum",
			},
			inputs: &commonparams.VCMDerivedPoolInputs{
				IsOntapMode:          true,
				OntapModeCredentials: &datamodel.PoolCredentials{SecretID: "mode-secret"},
			},
		}
	}

	tests := []struct {
		name      string
		mutate    func(*createPoolWorkflow)
		wantError string
	}{
		{
			name:      "missing credential",
			mutate:    func(wf *createPoolWorkflow) { wf.inputs.OntapModeCredentials = nil },
			wantError: "ONTAP_MODE credential is missing",
		},
		{
			name:      "missing RBAC URL",
			mutate:    func(wf *createPoolWorkflow) { wf.req.OntapModeRbacFileURL = "" },
			wantError: "RBAC file URL is missing",
		},
		{
			name:      "missing RBAC checksum",
			mutate:    func(wf *createPoolWorkflow) { wf.req.OntapModeRbacChecksum = "" },
			wantError: "RBAC file checksum is missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wf := valid()
			tt.mutate(wf)

			err := wf.validateOntapModeInputs()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}

	t.Run("non-ONTAP mode needs nothing", func(t *testing.T) {
		wf := valid()
		wf.inputs.IsOntapMode = false
		wf.inputs.OntapModeCredentials = nil
		wf.req.OntapModeRbacFileURL = ""
		wf.req.OntapModeRbacChecksum = ""

		assert.NoError(t, wf.validateOntapModeInputs())
	})
}

// The preconditions below are checked before any activity is dispatched, so a
// nil context is sufficient to exercise them.
func TestResolveOntapModeCredentials_Preconditions(t *testing.T) {
	tests := []struct {
		name      string
		creds     *datamodel.PoolCredentials
		wantError string
	}{
		{name: "missing credentials", wantError: "credential is missing"},
		{
			name:      "password missing secret id",
			creds:     &datamodel.PoolCredentials{Username: "mode-user"},
			wantError: "secret id is missing",
		},
		{
			name:      "password missing username",
			creds:     &datamodel.PoolCredentials{SecretID: "projects/p/secrets/mode"},
			wantError: "username is missing",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := resolveOntapModeCredentials(nil, tt.creds)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

// Locally the ONTAP Mode user is provisioned from the flags as a password user,
// so the passthrough can authenticate as it without Secret Manager. No activity
// is dispatched, so a nil context is sufficient and the stored credentials are
// ignored entirely.
func TestResolveOntapModeCredentials_LocalUsesFlags(t *testing.T) {
	origEnv := runningEnv
	origUser, origPw := vcmLocalOntapModeUsername, vcmLocalOntapModePassword
	runningEnv = localEnv
	vcmLocalOntapModeUsername, vcmLocalOntapModePassword = "mode-user", "mode-pw"
	t.Cleanup(func() {
		runningEnv = origEnv
		vcmLocalOntapModeUsername, vcmLocalOntapModePassword = origUser, origPw
	})

	// A cert-configured credential still resolves to password locally: create
	// installs no client certificate on a locally deployed cluster.
	username, authType, creds, err := resolveOntapModeCredentials(nil, &datamodel.PoolCredentials{
		AuthType:      env.USER_CERTIFICATE,
		CertificateID: "projects/p/locations/l/caPools/cp/certificates/c",
	})
	require.NoError(t, err)
	assert.Equal(t, "mode-user", username)
	assert.Equal(t, vlmAuthenticationTypePassword, authType)
	assert.Equal(t, "mode-pw", creds.AdminPassword)
}

func TestProvisionOntapModeUser_Preconditions(t *testing.T) {
	wf := func() *createPoolWorkflow {
		return &createPoolWorkflow{
			req: &commonparams.VCMCreatePoolParams{
				DeploymentID:          "dep-1",
				OntapModeRbacFileURL:  "gs://bucket/rbac",
				OntapModeRbacChecksum: "checksum",
			},
			inputs: &commonparams.VCMDerivedPoolInputs{
				IsOntapMode:          true,
				OntapModeCredentials: &datamodel.PoolCredentials{SecretID: "mode-secret"},
			},
		}
	}

	tests := []struct {
		name               string
		username           string
		authenticationType string
		modeCredentials    vlm.OntapCredentials
		wantError          string
	}{
		{
			name:               "missing username",
			authenticationType: vlmAuthenticationTypePassword,
			modeCredentials:    vlm.OntapCredentials{AdminPassword: "secret"},
			wantError:          "username is missing",
		},
		{
			name:               "unsupported authentication type",
			username:           "mode-user",
			authenticationType: "unknown",
			wantError:          "unsupported authentication type",
		},
		{
			name:               "empty resolved password",
			username:           "mode-user",
			authenticationType: vlmAuthenticationTypePassword,
			wantError:          "resolved password is empty",
		},
		{
			name:               "empty resolved certificate",
			username:           "mode-user",
			authenticationType: vlmAuthenticationTypeCertificate,
			wantError:          "resolved certificate is empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := wf().provisionOntapModeUser(
				nil, vlm.OntapCredentials{}, tt.modeCredentials, tt.username, tt.authenticationType)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
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

func TestBuildVCMUpdateDeploymentRequest(t *testing.T) {
	creds := vlm.OntapCredentials{AdminPassword: "pw"}
	current := vlm.VLMConfig{}
	current.Deployment.DeploymentID = "gcnv-test"
	current.Deployment.VSAInstanceType = "n2-standard-8"
	current.Deployment.NumHAPair = 1
	current.Deployment.SPConfig = vlm.SPConfig{Size: "2TiB", IOps: 1000, Throughput: 64}

	t.Run("COMPUTE same SKU skips VLM and keeps current disks", func(t *testing.T) {
		target := current
		req, skip, err := buildVCMUpdateDeploymentRequest(current, target, creds, ClusterComputeChangeType)
		require.NoError(t, err)
		assert.True(t, skip)
		assert.Empty(t, req.NewInstanceType)
		assert.Equal(t, current.Deployment.SPConfig, req.SPConfig)
		assert.Equal(t, []int{1}, req.HAPairIndices)
		assert.Equal(t, vlmSkipAutoTierThreshold, req.AutoTierThreshold)
	})

	t.Run("COMPUTE SKU change sets NewInstanceType and keeps current SPConfig", func(t *testing.T) {
		target := current
		target.Deployment.VSAInstanceType = "c3-standard-8-lssd"
		target.Deployment.SPConfig = vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128}
		req, skip, err := buildVCMUpdateDeploymentRequest(current, target, creds, ClusterComputeChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Equal(t, "c3-standard-8-lssd", req.NewInstanceType)
		assert.Equal(t, current.Deployment.VSAInstanceType, req.VLMConfig.Deployment.VSAInstanceType,
			"embedded VSAInstanceType must stay current; VLM diffs it against NewInstanceType")
		assert.Equal(t, current.Deployment.SPConfig.Size, req.SPConfig.Size, "COMPUTE must not apply the target disk layout")
		assert.Equal(t, current.Deployment.SPConfig.IOps, req.SPConfig.IOps)
		assert.Equal(t, current.Deployment.SPConfig.Throughput, req.SPConfig.Throughput)
		assert.Equal(t, current.Deployment.SPConfig, req.VLMConfig.Deployment.SPConfig,
			"embedded SPConfig must stay current on COMPUTE")
		assert.Empty(t, req.SPConfig.HAPairConfigs, "VLM derives the requested per-HA-pair config from the pool scalars")
		assert.Empty(t, req.VLMConfig.Deployment.SPConfig.HAPairConfigs, "VLM owns compatibility backfill for the current config")
	})

	t.Run("SCALE applies target SPConfig and never sets NewInstanceType", func(t *testing.T) {
		target := current
		target.Deployment.VSAInstanceType = "c3-standard-8-lssd"
		target.Deployment.SPConfig = vlm.SPConfig{Size: "4TiB", IOps: 2000, Throughput: 128}
		req, skip, err := buildVCMUpdateDeploymentRequest(current, target, creds, ClusterScaleChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Empty(t, req.NewInstanceType)
		assert.Equal(t, target.Deployment.SPConfig.Size, req.SPConfig.Size)
		assert.Equal(t, target.Deployment.SPConfig.IOps, req.SPConfig.IOps)
		assert.Equal(t, target.Deployment.SPConfig.Throughput, req.SPConfig.Throughput)
		assert.Equal(t, current.Deployment.SPConfig, req.VLMConfig.Deployment.SPConfig,
			"embedded SPConfig must stay current; VLM diffs it against top-level SPConfig")
		assert.Empty(t, req.SPConfig.HAPairConfigs)
		assert.Empty(t, req.VLMConfig.Deployment.SPConfig.HAPairConfigs)
		assert.Equal(t, current.Deployment.VSAInstanceType, req.VLMConfig.Deployment.VSAInstanceType)
	})

	t.Run("SCALE calls VLM even when the storage config already matches", func(t *testing.T) {
		target := current
		req, skip, err := buildVCMUpdateDeploymentRequest(current, target, creds, ClusterScaleChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Empty(t, req.NewInstanceType)
		assert.Equal(t, target.Deployment.SPConfig, req.SPConfig)
	})

	t.Run("SCALE sends the disk-rounded size and leaves performance to VLM", func(t *testing.T) {
		// 1229Gi / 8 disks rounds to 154Gi each, so VLM is asked for 1232Gi.
		// IOPS and throughput stay at the VMRS values; VLM applies its own floors.
		live := current
		live.Deployment.DataDiskCount = 8
		live.Deployment.SPConfig = vlm.SPConfig{Size: "1232Gi", IOps: 24000, Throughput: 1120}
		target := live
		target.Deployment.SPConfig = vlm.SPConfig{Size: "1229Gi", IOps: 2636, Throughput: 197}
		req, skip, err := buildVCMUpdateDeploymentRequest(live, target, creds, ClusterScaleChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Empty(t, req.NewInstanceType)
		assert.Equal(t, "1232Gi", req.SPConfig.Size)
		assert.Equal(t, int64(2636), req.SPConfig.IOps)
		assert.Equal(t, int64(197), req.SPConfig.Throughput)
		assert.Equal(t, "1232Gi", req.VLMConfig.Deployment.SPConfig.Size,
			"embedded size stays at the live value so VLM can diff against the rounded request")
	})

	t.Run("SCALE rounds a larger size up to whole disks", func(t *testing.T) {
		live := current
		live.Deployment.DataDiskCount = 8
		live.Deployment.SPConfig = vlm.SPConfig{Size: "1232Gi", IOps: 24000, Throughput: 1120}
		target := live
		target.Deployment.SPConfig = vlm.SPConfig{Size: "2458Gi", IOps: 2636, Throughput: 197}
		req, skip, err := buildVCMUpdateDeploymentRequest(live, target, creds, ClusterScaleChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Equal(t, "2464Gi", req.SPConfig.Size)
	})

	t.Run("SCALE disk-rounding fallback uses 8 disks when count is unset", func(t *testing.T) {
		live := current
		live.Deployment.DataDiskCount = 0
		live.Deployment.SPConfig = vlm.SPConfig{Size: "1232Gi", IOps: 24000, Throughput: 1120}
		target := live
		target.Deployment.SPConfig = vlm.SPConfig{Size: "1229Gi", IOps: 2636, Throughput: 197}
		req, skip, err := buildVCMUpdateDeploymentRequest(live, target, creds, ClusterScaleChangeType)
		require.NoError(t, err)
		assert.False(t, skip)
		assert.Equal(t, "1232Gi", req.SPConfig.Size)
	})

	t.Run("unknown changeType is rejected", func(t *testing.T) {
		_, _, err := buildVCMUpdateDeploymentRequest(current, current, creds, "CONFIG")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported changeType")
	})
}

func TestHaPairIndices(t *testing.T) {
	assert.Equal(t, []int{1}, haPairIndices(0))
	assert.Equal(t, []int{1, 2, 3}, haPairIndices(3))
}

func TestUpdatePoolWorkflow_NilInputs_ReturnsError(t *testing.T) {
	wf := &updatePoolWorkflow{req: &commonparams.VCMUpdatePoolParams{}}

	// Both methods reject nil derived inputs before touching the engine context.
	err := wf.apply(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "derived inputs missing")

	_, err = wf.identifyTargetVLMConfig(nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "derived inputs missing")
}

func TestPoolUpdateWorkflow_MissingParams_ReturnsError(t *testing.T) {
	tests := []struct {
		name   string
		params *commonparams.UpdatePoolParams
	}{
		{name: "nil params", params: nil},
		{name: "nil VCM", params: &commonparams.UpdatePoolParams{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// PoolUpdateWorkflow validates params before touching the engine
			// context, so a nil orchestrator context is never dereferenced here.
			_, err := PoolUpdateWorkflow(nil, tc.params)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "VCM params missing")
		})
	}
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

package activities_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	oci "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

// ---------------------------------------------------------------------------
// ValidateOCIFabricPoolSecret
// ---------------------------------------------------------------------------

// withGetSecretVersionStub swaps oci.GetSecretVersion (the existing
// test seam) for the duration of one test and restores it on cleanup.
func withGetSecretVersionStub(t *testing.T,
	fn func(*oci.OciServices, string, ...int64) (*oci.OCICustomSecret, error),
) {
	t.Helper()
	orig := oci.GetSecretVersion
	oci.GetSecretVersion = fn
	t.Cleanup(func() { oci.GetSecretVersion = orig })
}

// withGetOCIServiceStub overrides hyperscaler2.GetOCIService for one test.
func withGetOCIServiceStub(t *testing.T,
	fn func(ctx context.Context) (*oci.OciServices, error),
) {
	t.Helper()
	orig := hyperscaler2.GetOCIService
	hyperscaler2.GetOCIService = fn
	t.Cleanup(func() { hyperscaler2.GetOCIService = orig })
}

// fabricPoolSecretGetResp builds a minimal GetSecret response that
// GetSecretWithLatestVersion / GetSecretWithCustomVersion consume before
// calling oci.GetSecretVersion. The body needs to look like the OCI Vault
// GetSecret JSON envelope; only fields the SDK touches matter here.
func fabricPoolSecretGetResp() string {
	return `{
		"id": "ocid1.vaultsecret.oc1..fabricpool",
		"secretName": "fabric-pool-secret",
		"lifecycleState": "ACTIVE",
		"currentVersionNumber": 1
	}`
}

func TestValidateOCIFabricPoolSecret_GetOCIServiceFails(t *testing.T) {
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) {
		return nil, errors.New("oci client init failed")
	})

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	_, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..x", (*int64)(nil))
	require.Error(t, err)
}

func TestValidateOCIFabricPoolSecret_LatestVersion_Success(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"access_key": "AKID", "secret_key": "SECRET"})
	withGetSecretVersionStub(t, func(_ *oci.OciServices, secretID string, versions ...int64) (*oci.OCICustomSecret, error) {
		assert.Empty(t, versions, "latest-version path must NOT pass a version number")
		return &oci.OCICustomSecret{
			Ocid:    secretID,
			Name:    "fabric-pool-secret",
			Value:   string(payload),
			Version: 1,
		}, nil
	})

	mockSvc := newMockOCIServiceForTest(t, func(*http.Request) (*http.Response, error) {
		return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
	})
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	val, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..ok", (*int64)(nil))
	require.NoError(t, err)
	var got *vlm.FabricPoolConfig
	require.NoError(t, val.Get(&got))
	assert.Equal(t, "ocid1.vaultsecret..ok", got.SecretOcid)
}

// When the caller pins a specific version, the activity must fetch and validate
// exactly that version (via GetSecretWithCustomVersion), not the latest one.
func TestValidateOCIFabricPoolSecret_PinnedVersion_Success(t *testing.T) {
	const pinnedVersion int64 = 3
	payload, _ := json.Marshal(map[string]string{"access_key": "AKID", "secret_key": "SECRET"})
	withGetSecretVersionStub(t, func(_ *oci.OciServices, secretID string, versions ...int64) (*oci.OCICustomSecret, error) {
		require.Len(t, versions, 1, "pinned-version path must pass exactly one version number")
		assert.Equal(t, pinnedVersion, versions[0], "activity must fetch the pinned version")
		return &oci.OCICustomSecret{
			Ocid:    secretID,
			Name:    "fabric-pool-secret",
			Value:   string(payload),
			Version: versions[0],
		}, nil
	})

	mockSvc := newMockOCIServiceForTest(t, func(*http.Request) (*http.Response, error) {
		return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
	})
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	version := pinnedVersion
	val, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..pinned", &version)
	require.NoError(t, err)
	var got *vlm.FabricPoolConfig
	require.NoError(t, val.Get(&got))
	assert.Equal(t, "ocid1.vaultsecret..pinned", got.SecretOcid)
}

func TestValidateOCIFabricPoolSecret_SecretNotFound(t *testing.T) {
	withGetSecretVersionStub(t, func(*oci.OciServices, string, ...int64) (*oci.OCICustomSecret, error) {
		// GetSecretWithLatestVersion returns nil-nil when the version lookup
		// finds nothing — that surfaces as a non-retryable "inactive or
		// pending deletion" error from the activity.
		return nil, nil
	})
	mockSvc := newMockOCIServiceForTest(t, func(*http.Request) (*http.Response, error) {
		return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
	})
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	_, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..gone", (*int64)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inactive, pending deletion, or version not found")
}

// A transient OCI Vault fetch failure must surface as a RETRYABLE temporal
// error so Temporal can retry it; only deterministic validation failures are
// non-retryable.
func TestValidateOCIFabricPoolSecret_FetchError_IsRetryable(t *testing.T) {
	withGetSecretVersionStub(t, func(*oci.OciServices, string, ...int64) (*oci.OCICustomSecret, error) {
		return nil, errors.New("oci vault temporarily unavailable")
	})
	mockSvc := newMockOCIServiceForTest(t, func(*http.Request) (*http.Response, error) {
		return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
	})
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	_, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..flaky", (*int64)(nil))
	require.Error(t, err)

	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.False(t, appErr.NonRetryable(), "fabric pool secret fetch failures must be retryable")
}

func TestValidateOCIFabricPoolSecret_PayloadNotJSON(t *testing.T) {
	withGetSecretVersionStub(t, func(*oci.OciServices, string, ...int64) (*oci.OCICustomSecret, error) {
		return &oci.OCICustomSecret{Value: "definitely-not-json", Version: 1}, nil
	})
	mockSvc := newMockOCIServiceForTest(t, func(*http.Request) (*http.Response, error) {
		return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
	})
	withGetOCIServiceStub(t, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

	act := &activities.PoolActivity{}
	tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

	_, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..bad", (*int64)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
}

func TestValidateOCIFabricPoolSecret_MissingFields(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{name: "missing access_key", payload: `{"secret_key":"SECRET"}`},
		{name: "missing secret_key", payload: `{"access_key":"AKID"}`},
		{name: "empty access_key", payload: `{"access_key":"","secret_key":"SECRET"}`},
		{name: "empty secret_key", payload: `{"access_key":"AKID","secret_key":""}`},
		{name: "both empty", payload: `{"access_key":"","secret_key":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			withGetSecretVersionStub(tt, func(*oci.OciServices, string, ...int64) (*oci.OCICustomSecret, error) {
				return &oci.OCICustomSecret{Value: tc.payload, Version: 1}, nil
			})
			mockSvc := newMockOCIServiceForTest(tt, func(*http.Request) (*http.Response, error) {
				return ociMockJSONResponse(http.StatusOK, fabricPoolSecretGetResp()), nil
			})
			withGetOCIServiceStub(tt, func(context.Context) (*oci.OciServices, error) { return mockSvc, nil })

			act := &activities.PoolActivity{}
			tEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			tEnv.RegisterActivity(act.ValidateOCIFabricPoolSecret)

			_, err := tEnv.ExecuteActivity(act.ValidateOCIFabricPoolSecret, "ocid1.vaultsecret..x", (*int64)(nil))
			require.Error(tt, err)
			assert.Contains(tt, err.Error(), "non-empty access_key and secret_key")
		})
	}
}

// PersistRotatedFabricPoolSecret was removed; the rotate workflow now reuses
// the CreatedPool activity (save VLMConfig + mark READY). Its forwarding
// behavior is covered by the workflow-level tests.

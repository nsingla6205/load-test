package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
)

// fakeVLMConfigStore records write-back calls and can inject errors.
type fakeVLMConfigStore struct {
	setJSON    string
	setCalls   int
	clearCalls int
	setErr     error
	clearErr   error
}

func (f *fakeVLMConfigStore) SetVLMConfig(_ context.Context, jsonStr string) error {
	f.setCalls++
	f.setJSON = jsonStr
	return f.setErr
}

func (f *fakeVLMConfigStore) ClearVLMConfigFetching(_ context.Context) error {
	f.clearCalls++
	return f.clearErr
}

func validGetVLMConfigParams() commonparams.GetVLMConfigParams {
	return commonparams.GetVLMConfigParams{
		DeploymentID:         "dep-1",
		ServiceTenantProject: "tenant-1",
		Zone1:                "us-central1-a",
		Zone2:                "us-central1-b",
		MediatorZone:         "us-central1-c",
		OntapCredentials: []commonparams.VCMOntapCredential{
			{Type: "admin", SecretID: "projects/p/secrets/s"},
		},
	}
}

func TestValidateGetVLMConfigInputs_Valid(t *testing.T) {
	a := &VLMConfigActivity{}
	err := a.ValidateGetVLMConfigInputs(context.Background(), validGetVLMConfigParams())
	require.NoError(t, err)
}

func TestValidateGetVLMConfigInputs_MissingFields(t *testing.T) {
	a := &VLMConfigActivity{}

	tests := map[string]func(p *commonparams.GetVLMConfigParams){
		"deploymentId":         func(p *commonparams.GetVLMConfigParams) { p.DeploymentID = "" },
		"serviceTenantProject": func(p *commonparams.GetVLMConfigParams) { p.ServiceTenantProject = "" },
		"zone1":                func(p *commonparams.GetVLMConfigParams) { p.Zone1 = "" },
		"zone2":                func(p *commonparams.GetVLMConfigParams) { p.Zone2 = "" },
		"mediatorZone":         func(p *commonparams.GetVLMConfigParams) { p.MediatorZone = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := validGetVLMConfigParams()
			mutate(&p)
			err := a.ValidateGetVLMConfigInputs(context.Background(), p)
			require.Error(t, err)
		})
	}
}

func TestValidateGetVLMConfigInputs_NoAdminSecret(t *testing.T) {
	a := &VLMConfigActivity{}
	p := validGetVLMConfigParams()
	// Admin credential without a password secret id.
	p.OntapCredentials = []commonparams.VCMOntapCredential{{Type: "admin", CertificateID: "cert-only"}}
	err := a.ValidateGetVLMConfigInputs(context.Background(), p)
	require.Error(t, err)
}

func TestFinalizeVLMConfig_SuccessStoresJSON(t *testing.T) {
	store := &fakeVLMConfigStore{}
	a := &VLMConfigActivity{Store: store}

	err := a.FinalizeVLMConfig(context.Background(), FinalizeVLMConfigRequest{
		VLMConfigJSON: `{"vlm_config":{}}`,
		Failed:        false,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, store.setCalls)
	assert.Equal(t, 0, store.clearCalls)
	assert.Equal(t, `{"vlm_config":{}}`, store.setJSON)
}

func TestFinalizeVLMConfig_FailureClearsFlag(t *testing.T) {
	store := &fakeVLMConfigStore{}
	a := &VLMConfigActivity{Store: store}

	err := a.FinalizeVLMConfig(context.Background(), FinalizeVLMConfigRequest{
		VLMConfigJSON: "ignored",
		Failed:        true,
	})
	require.NoError(t, err)
	assert.Equal(t, 0, store.setCalls)
	assert.Equal(t, 1, store.clearCalls)
}

func TestFinalizeVLMConfig_NilStore(t *testing.T) {
	a := &VLMConfigActivity{}
	err := a.FinalizeVLMConfig(context.Background(), FinalizeVLMConfigRequest{})
	require.Error(t, err)
}

func TestFinalizeVLMConfig_PropagatesStoreError(t *testing.T) {
	store := &fakeVLMConfigStore{setErr: errors.New("boom")}
	a := &VLMConfigActivity{Store: store}

	err := a.FinalizeVLMConfig(context.Background(), FinalizeVLMConfigRequest{VLMConfigJSON: "x"})
	require.Error(t, err)
}

func TestFinalizeVLMConfig_PropagatesClearError(t *testing.T) {
	store := &fakeVLMConfigStore{clearErr: errors.New("boom")}
	a := &VLMConfigActivity{Store: store}

	err := a.FinalizeVLMConfig(context.Background(), FinalizeVLMConfigRequest{Failed: true})
	require.Error(t, err)
	assert.Equal(t, 1, store.clearCalls)
}

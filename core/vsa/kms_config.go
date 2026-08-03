package vsa

import (
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
)

const (
	ErrTypeKmsConfigNotReachableVsaCluster = "KmsConfigNotReachableVsaCluster"
)

func (rc *OntapRestProvider) CreateKmsConfig(params CreateKmsConfigParams) (*CreateKmsConfigResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	gcpKmsCreateParams := &ontapRest.GcpKmsCreateParams{
		KeyName:                &params.KeyName,
		KeyRingLocation:        &params.KeyRingLocation,
		KeyRingName:            &params.KeyRingName,
		ProjectID:              &params.ProjectID,
		ApplicationCredentials: params.Credentials,
		SvmName:                &params.SvmName,
		PrivilegedAccount:      &params.PrivilegedAccount, // Cloud KMS account to impersonate.
	}
	gcpKmsResponse, err := client.Security().GcpKmsCreate(gcpKmsCreateParams)
	if err != nil {
		return nil, err
	}
	response := &CreateKmsConfigResponse{ProviderResponse: ProviderResponse{ExternalUUID: *gcpKmsResponse[0].UUID}}
	return response, nil
}

func (rc *OntapRestProvider) DeleteEkmConfig(params DeleteKmsConfigParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	gcpKmsDeleteParams := &ontapRest.GcpKmsDeleteParams{
		UUID: params.ExternalKmsConfigID,
	}
	errKmsDelete := client.Security().GcpKmsDelete(gcpKmsDeleteParams)

	return errKmsDelete
}

func (rc *OntapRestProvider) IsGcpKmsReachable(params GetKmsConfigParams) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, err
	}
	getKmsConfigParams := &ontapRest.GcpKmsGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"**"}},
		UUID:       params.ExternalKmsConfigID,
	}
	gcpKmsResponse, err := client.Security().GcpKmsGet(getKmsConfigParams)
	if err != nil {
		// Return original reachability error
		if strings.Contains(err.Error(), "permission_denied") {
			return false, errors.New("GCP KMS key is not reachable from ONTAP - Service account lacks permission, retrying again")
		}
		if strings.Contains(err.Error(), "Invalid JWT Signature") || strings.Contains(err.Error(), "InvalidJWTSignature") {
			return false, errors.New("GCP KMS key is not reachable from ONTAP - Failed to establish connectivity" +
				" with the cloud key management service, retrying again")
		}
		return false, temporal.NewNonRetryableApplicationError("GCP KMS key is not reachable from VSA Clusters", ErrTypeKmsConfigNotReachableVsaCluster, err)
	}

	if gcpKmsResponse.GoogleReachability != nil && !nillable.GetBool(gcpKmsResponse.GoogleReachability.Reachable, false) {
		if strings.Contains(strings.ToLower(*gcpKmsResponse.GoogleReachability.Message), "permission_denied") {
			return false, errors.New("permission_denied")
		}
		return false, nil
	}
	for _, reachability := range gcpKmsResponse.GcpKmsInlineEkmipReachability {
		if !nillable.GetBool(reachability.Reachable, false) {
			return false, nil
		}
	}
	return true, nil
}

func (rc *OntapRestProvider) ModifyGcpKms(externalUUID string, credentials *log.Secret) (*ontapRest.GcpKms, *string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, nil, err
	}
	gcpKmsModifyParams := &ontapRest.GcpKmsModifyParams{
		UUID:                   externalUUID,
		ApplicationCredentials: credentials,
	}

	gcpKms, ontapJob, err := client.Security().GcpKmsModify(gcpKmsModifyParams)
	if err != nil {
		return nil, nil, errors.New("Failed to establish connectivity with the cloud key management service while updating GCP KMS")
	}
	if gcpKms != nil {
		return gcpKms, nil, nil
	}
	return nil, &ontapJob.JobUUID, nil
}

// GetOntapGcpKmsConfig returns the GCP KMS key hierarchy currently configured on ONTAP
// for the SVM's external key manager identified by externalUUID.
func (rc *OntapRestProvider) GetOntapGcpKmsConfig(externalUUID string) (*GcpKmsHierarchy, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	resp, err := client.Security().GcpKmsGet(&ontapRest.GcpKmsGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"key_name", "key_ring_name", "key_ring_location", "project_id"}},
		UUID:       externalUUID,
	})
	if err != nil {
		return nil, err
	}
	return &GcpKmsHierarchy{
		ProjectID:       nillable.FromPointer(resp.ProjectID),
		KeyRingName:     nillable.FromPointer(resp.KeyRingName),
		KeyRingLocation: nillable.FromPointer(resp.KeyRingLocation),
		KeyName:         nillable.FromPointer(resp.KeyName),
	}, nil
}

// MatchesTarget reports whether the GCP KMS hierarchy currently configured on the SVM
// already matches the desired target key coordinates.
func (h *GcpKmsHierarchy) MatchesTarget(projectID, keyRingName, keyRingLocation, keyName string) bool {
	if h == nil {
		return false
	}
	return h.ProjectID == projectID &&
		h.KeyRingName == keyRingName &&
		h.KeyRingLocation == keyRingLocation &&
		gcpKmsKeyNameMatches(h.KeyName, keyName)
}

func gcpKmsKeyNameMatches(existing, target string) bool {
	return existing == target || strings.HasSuffix(existing, "/cryptoKeys/"+target)
}

// RekeyGcpKms rewraps the SVM key with a new GCP KEK using the ONTAP gcp rekey-external operation.
// The existing ONTAP GCP KMS configuration  is reused; only the key coordinates change.
func (rc *OntapRestProvider) RekeyGcpKms(params RekeyGcpKmsParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	job, err := client.Security().GcpKmsRekeyExternal(&ontapRest.GcpKmsRekeyExternalParams{
		UUID:            params.ExternalKmsConfigID,
		KeyName:         &params.KeyName,
		KeyRingLocation: &params.KeyRingLocation,
		KeyRingName:     &params.KeyRingName,
		ProjectID:       &params.ProjectID,
	})
	if err != nil {
		return err
	}

	if job != nil && job.JobUUID != "" {
		if err := client.Poll(job.JobUUID); err != nil {
			return err
		}
	}

	return nil
}

func (rc *OntapRestProvider) EnableAutoVolOfflineCronForGCPKMS() error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	err = client.Security().EnableAutoVolOfflineCronForGCPKMS()
	if err != nil {
		return err
	}

	return nil
}

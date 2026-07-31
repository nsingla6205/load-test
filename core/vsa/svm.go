package vsa

import (
	"context"
	"fmt"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
)

func (rc *OntapRestProvider) CreateSVM(params CreateSvmParams) (*ProviderResponse, error) {
	// Create the SVM
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	ipSpace := params.IPSpace
	if ipSpace == "" {
		ipSpace = ipSpaceName
	}
	svm, job, err := client.SVM().SvmCreate(&ontapRest.SvmCreateParams{
		Name:    params.Name,
		IPSpace: ipSpace,
		Protocols: ontapRest.Protocols{
			EnableIscsi: params.Protocols.EnableIscsi,
			EnableNfs:   params.Protocols.EnableNfs,
		},
	})
	if err != nil {
		return nil, err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			return nil, err
		}
	}

	// Validate the SVM response to avoid nil pointer dereferences
	if svm == nil || svm.Name == nil {
		return nil, fmt.Errorf("invalid SVM response from API")
	}

	// Return the created SVM
	return &ProviderResponse{
		Name:         *svm.Name,
		ExternalUUID: *svm.UUID,
	}, nil
}

// ModifySVMWithQoSPolicy applies or clears the QoS policy on an SVM.
// Use QoSPolicyName == "" to clear the SVM's QoS policy (required for auto→manual pool transition).
// ONTAP rejects empty string; we send "none" to clear (same semantic as volume QoS unassign).
func (rc *OntapRestProvider) ModifySVMWithQoSPolicy(params ModifySVMWithQoSPolicyParams) error {
	// Get the ONTAP client
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// ONTAP uses "none" to clear the SVM QoS policy; empty string returns "policy does not exist" (8454172)
	qosPolicyNameForONTAP := params.QoSPolicyName
	if qosPolicyNameForONTAP == "" {
		qosPolicyNameForONTAP = "none"
	}

	// Modify the SVM to apply or clear the QoS policy group
	done, job, err := client.SVM().SvmModify(&ontapRest.SvmModifyParams{
		SvmUUID:       params.SvmUUID,
		QoSPolicyName: &qosPolicyNameForONTAP,
	})
	if err != nil {
		return err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			return err
		}
	}

	// If done is true, the operation completed synchronously
	if done {
		return nil
	}

	return nil
}

// ListSVMs returns SVMs matching optional name pattern via GET /api/svm/svms.
func (rc *OntapRestProvider) ListSVMs(params ListSvmsParams) ([]*ontapRest.Svm, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	return client.SVM().SvmCollectionGet(&ontapRest.SvmGetCollectionParams{
		BaseParams: ontapRest.BaseParams{Fields: params.Fields},
		SvmName:    params.NamePattern,
	})
}

func (rc *OntapRestProvider) GetSVM(params GetSvmParams) (*ontapRest.Svm, error) {
	// Create the SVM
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	svm, err := client.SVM().SvmGet(&ontapRest.SvmGetParams{
		SvmName: params.Name,
	})
	if err != nil {
		return nil, err
	}

	return svm, nil
}

// ModifyRquota enables or disables recursive quota on an SVM.
//
// This method calls the ONTAP REST API to modify the NFS service's RquotaEnabled setting,
// which controls the 'rquota' (remote quota) protocol support for the SVM.
func (rc *OntapRestProvider) ModifyRquota(ctx context.Context, svmUUID string, rquota bool) error {
	// Get the ONTAP client
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// Modify the NFS service to enable/disable rquota
	err = client.NAS().NfsParamsModify(ctx, &ontapRest.NfsModifyParams{
		SvmUUID:       svmUUID,
		RquotaEnabled: &rquota,
	})
	if err != nil {
		return err
	}

	return nil
}

// UpdateNFSRWSizeOnSVM updates the NFS TCP max transfer size on an SVM.
func (rc *OntapRestProvider) UpdateNFSRWSizeOnSVM(ctx context.Context, svmUUID string, tcpMaxTransferSize int64) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	return client.NAS().NfsParamsModify(ctx, &ontapRest.NfsModifyParams{
		SvmUUID:            svmUUID,
		TCPMaxTransferSize: &tcpMaxTransferSize,
	})
}

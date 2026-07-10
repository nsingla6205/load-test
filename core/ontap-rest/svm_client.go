package ontap_rest

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/svm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	securitypriv "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/priv/client/operations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// SVMClient describes an SVM client
type SVMClient interface {
	SvmGet(params *SvmGetParams) (*Svm, error)
	SvmCollectionGet(params *SvmGetCollectionParams) ([]*Svm, error)
	SvmCreate(params *SvmCreateParams) (*Svm, *JobAccepted, error)
	SvmDelete(externalSvmUUID string) (bool, *JobAccepted, error)
	SvmModify(params *SvmModifyParams) (bool, *JobAccepted, error)
	SvmPeerCollectionGet(params *SvmPeerGetCollectionParams) ([]*SvmPeer, error)
	SvmPeerCreate(params *SvmPeerCreateParams) error
	SvmPeerModify(params *SvmPeerModifyParams) error
	SvmPeerDelete(params *SvmPeerDeleteParams) error
}

type svmClient struct {
	api     svm.ClientService
	apiPriv *securitypriv.ClientService
	poller  Poller
}

// SvmGet invokes pkg/ontap-rest/client/svm/Client.SvmGet
func (sc *svmClient) SvmGet(params *SvmGetParams) (*Svm, error) {
	if params == nil {
		return nil, errors.New("params for SvmGet cannot be nil")
	}

	if params.SvmName == "" {
		return nil, errors.New("params.SvmName cannot be empty")
	}

	response, err := sc.api.SvmCollectionGet(svmGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if len(response.Payload.SvmResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("svm", &params.SvmName)
	}

	if len(response.Payload.SvmResponseInlineRecords) > 1 {
		return nil, errors.New("unexpected number of svms returned")
	}

	return &Svm{Svm: *response.Payload.SvmResponseInlineRecords[0]}, nil
}

// SvmCollectionGet invokes pkg/ontap-rest/svm/Client.SvmCollectionGet
func (sc *svmClient) SvmCollectionGet(params *SvmGetCollectionParams) ([]*Svm, error) {
	// Fix me: pagination is missing
	response, err := sc.api.SvmCollectionGet(svmGetCollectionParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}
	resp := make([]*Svm, nillable.FromPointer(response.Payload.NumRecords))
	for i, currentSvm := range response.Payload.SvmResponseInlineRecords {
		resp[i] = &Svm{Svm: *currentSvm}
	}
	return resp, nil
}

// SvmCreate invokes pkg/ontap-rest/client/svm/Client.SvmCreate
func (sc *svmClient) SvmCreate(params *SvmCreateParams) (*Svm, *JobAccepted, error) {
	if params == nil {
		return nil, nil, errors.New("params for SvmCreate cannot be nil")
	}

	created, accepted, err := sc.api.SvmCreate(svm.NewSvmCreateParams().WithReturnRecords(nillable.ToPointer("true")).WithReturnTimeout(&returnTimeout).WithInfo(&models.Svm{
		Name:    &params.Name,
		Ipspace: &models.SvmInlineIpspace{Name: &params.IPSpace},
		Fcp:     &models.SvmInlineFcp{Allowed: nillable.ToPointer(false)},
		Iscsi:   &models.SvmInlineIscsi{Allowed: nillable.ToPointer(params.Protocols.EnableIscsi)},
		Nfs:     &models.SvmInlineNfs{Allowed: nillable.ToPointer(params.Protocols.EnableNfs), Enabled: nillable.ToPointer(params.Protocols.EnableNfs)},
		Ndmp:    &models.SvmInlineNdmp{Allowed: nillable.ToPointer(false)},
		Nvme:    &models.SvmInlineNvme{Allowed: nillable.ToPointer(false)},
	}), nil)
	if err != nil {
		return nil, nil, err
	}

	if created != nil {
		return &Svm{Svm: *created.Payload.Records[0]}, nil, nil
	}

	return &Svm{Svm: *accepted.Payload.Records[0]}, &JobAccepted{
		ResourceUUID: *accepted.Payload.Records[0].UUID,
		JobUUID:      string(*accepted.Payload.Job.UUID),
	}, nil
}

// SvmDelete invokes pkg/ontap-rest/client/svm/Client.SvmDelete
func (sc *svmClient) SvmDelete(externalSvmUUID string) (bool, *JobAccepted, error) {
	done, job, err := sc.api.SvmDelete(svm.NewSvmDeleteParams().WithUUID(externalSvmUUID).WithReturnTimeout(&returnTimeout), nil)
	if err != nil {
		return false, nil, err
	}
	if done != nil {
		return true, nil, nil
	}

	return false, &JobAccepted{
		JobUUID: string(*job.Payload.Job.UUID),
	}, nil
}

// SvmModify invokes clients/ontap-rest/client/svm/Client.SvmModify
func (sc *svmClient) SvmModify(params *SvmModifyParams) (bool, *JobAccepted, error) {
	_, res, err := sc.api.SvmModify(svmModifyParamsToOntap(params), nil)
	if err != nil {
		return false, nil, err
	}

	if res != nil {
		return false, &JobAccepted{JobUUID: string(*res.Payload.Job.UUID)}, nil
	}

	return true, nil, nil
}

// SvmPeerCollectionGet invokes pkg/ontap-rest/svm/Client.SvmPeerCollectionGet
func (sc *svmClient) SvmPeerCollectionGet(params *SvmPeerGetCollectionParams) ([]*SvmPeer, error) {
	response, err := sc.api.SvmPeerCollectionGet(svmPeerGetCollectionParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	resp := make([]*SvmPeer, nillable.FromPointer(response.Payload.NumRecords))
	for i, svmPeer := range response.Payload.SvmPeerResponseInlineRecords {
		resp[i] = &SvmPeer{SvmPeer: *svmPeer}
	}
	return resp, nil
}

// SvmPeerCreate invokes pkg/ontap-rest/svm/Client.SvmPeerCreate
func (sc *svmClient) SvmPeerCreate(params *SvmPeerCreateParams) error {
	_, accepted, err := sc.api.SvmPeerCreate(svmPeerCreateParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	if accepted != nil {
		if err = sc.poller.Poll(accepted.Payload.Job.UUID.String()); err != nil {
			return err
		}
	}
	return nil
}

// SvmPeerModify invokes pkg/ontap-rest/svm/Client.SvmPeerModify
func (sc *svmClient) SvmPeerModify(params *SvmPeerModifyParams) error {
	_, accepted, err := sc.api.SvmPeerModify(svmPeerModifyParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	if accepted != nil {
		if err = sc.poller.Poll(accepted.Payload.Job.UUID.String()); err != nil {
			return err
		}
	}
	return nil
}

// SvmPeerDelete invokes pkg/ontap-rest/svm/Client.SvmPeerDelete
func (sc *svmClient) SvmPeerDelete(params *SvmPeerDeleteParams) error {
	_, accepted, err := sc.api.SvmPeerDelete(svmPeerDeleteParamsToONTAP(params), nil)
	if err != nil {
		return err
	}
	if accepted != nil {
		if err = sc.poller.Poll(accepted.Payload.Job.UUID.String()); err != nil {
			return err
		}
	}
	return nil
}

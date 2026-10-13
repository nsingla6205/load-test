package ontap_rest

import (
	san "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/s_a_n"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// SANClient describes an SAN client
type SANClient interface { // generate:mock
	IscsiServiceGet(params *IscsiGetParams) (*Iscsi, error)
	IscsiServiceCreate(params *IscsiCreateParams) error
	IGroupCreate(params *IgroupCreateParams) (string, error)
	IGroupDelete(params *IgroupDeleteParams) error
	IGroupsGet(params *IgroupGetParams) ([]*Igroup, error)
	IGroupGet(params *IgroupGetParams) (*Igroup, error)
	IGroupAddInitiator(params *IgroupAddInitiatorParams) error
	IGroupDeleteInitiator(params *IgroupDeleteInitiatorParams) error
	LunCreate(params *LunCreateParams) (*Lun, error)
	LunGet(params *LunGetParams) ([]*Lun, error)
	LunUpdate(params *LunUpdateParams) (bool, *JobAccepted, error)
	LunMapCreate(params *LunMapCreateParams) error
	LunMapDelete(params *LunMapDeleteParams) error
}

var (
	paginateIgroupCollectionGet = _paginate[[]*Igroup]
)

type sanClient struct {
	api    san.ClientService
	poller Poller
}

func (t *sanClient) IscsiServiceGet(params *IscsiGetParams) (*Iscsi, error) {
	// MD: FIXME: add pagination support in case the service is slow
	response, err := t.api.IscsiServiceCollectionGet(iscsiServiceGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if len(response.Payload.IscsiServiceResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("iscsi service", nil)
	}

	if len(response.Payload.IscsiServiceResponseInlineRecords) > 1 {
		return nil, errors.New("unexpected response when querying for iscsi service")
	}

	return &Iscsi{IscsiService: *response.Payload.IscsiServiceResponseInlineRecords[0]}, nil
}

func (t *sanClient) IscsiServiceCreate(params *IscsiCreateParams) error {
	_, err := t.api.IscsiServiceCreate(iscsiServiceCreateParamsToONTAP(params), nil)
	return err
}

// IGroupCreate invokes clients/ontap-rest/client/s_a_n/Client.IGroupCreate to create an initiator group
func (t *sanClient) IGroupCreate(params *IgroupCreateParams) (string, error) {
	response, err := t.api.IgroupCreate(igroupCreateParamsToONTAP(params), nil)
	if err != nil {
		return "", err
	}
	if response == nil || response.Payload == nil || len(response.Payload.IgroupResponseInlineRecords) == 0 ||
		response.Payload.IgroupResponseInlineRecords[0] == nil || response.Payload.IgroupResponseInlineRecords[0].UUID == nil ||
		*response.Payload.IgroupResponseInlineRecords[0].UUID == "" {
		return "", errors.New("ONTAP igroup create response is missing uuid")
	}

	return *response.Payload.IgroupResponseInlineRecords[0].UUID, nil
}

// IGroupDelete invokes clients/ontap-rest/client/s_a_n/Client.IGroupDelete to delete an igroup
func (t *sanClient) IGroupDelete(params *IgroupDeleteParams) error {
	_, err := t.api.IgroupDelete(igroupDeleteParamsToONTAP(params), nil)
	if err != nil {
		return err
	}

	return nil
}

// LunCreate invokes clients/ontap-rest/client/s_a_n/Client.LunCreate to create a LUN
func (t *sanClient) LunCreate(params *LunCreateParams) (*Lun, error) {
	created, accepted, err := t.api.LunCreate(lunCreateParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if accepted != nil {
		if err = t.poller.Poll(accepted.Payload.Job.UUID.String()); err != nil {
			return nil, err
		}

		return &Lun{Lun: *accepted.Payload.Records[0]}, nil
	}

	return &Lun{Lun: *created.Payload.LunResponseInlineRecords[0]}, nil
}

// LunGet invokes clients/ontap-rest/client/s_a_n/Client.LunCollectionGet to get LUNs
func (t *sanClient) LunGet(params *LunGetParams) ([]*Lun, error) {
	response, err := t.api.LunCollectionGet(lunGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	if len(response.Payload.LunResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("lun", nil)
	}

	luns := make([]*Lun, len(response.Payload.LunResponseInlineRecords))
	for i, lun := range response.Payload.LunResponseInlineRecords {
		luns[i] = &Lun{Lun: *lun}
	}

	return luns, nil
}

// LunUpdate invokes clients/ontap-rest/client/s_a_n/Client.LunModify to update a LUN
// It returns a boolean indicating whether the operation was successful immediately
// or if it was accepted for processing, in which case a JobAccepted object is returned.
func (t *sanClient) LunUpdate(params *LunUpdateParams) (bool, *JobAccepted, error) {
	// Success code response ignored, since it does not contain any useful data
	okResponse, acceptedResponse, err := t.api.LunModify(lunModifyParamsToONTAP(params), nil)
	if err != nil {
		return false, nil, err
	}
	if okResponse != nil {
		return true, nil, nil
	}

	job := &JobAccepted{
		JobUUID: acceptedResponse.Payload.Job.UUID.String(),
	}
	return false, job, nil
}

// LunMapCreate invokes clients/ontap-rest/client/s_a_n/Client.LunMapCreate to create a LUN mapping
func (t *sanClient) LunMapCreate(params *LunMapCreateParams) error {
	_, err := t.api.LunMapCreate(lunMapCreateParamsToONTAP(params), nil)
	return err
}

// LunMapDelete invokes clients/ontap-rest/client/s_a_n/Client.LunMapDelete to  a LUN mapping
func (t *sanClient) LunMapDelete(params *LunMapDeleteParams) error {
	_, err := t.api.LunMapDelete(lunMapDeleteParamsToONTAP(params), nil)
	return err
}

// IGroupGet invokes clients/ontap-rest/client/s_a_n/Client.IGroupCreate to create an initiator group
func (t *sanClient) IGroupGet(params *IgroupGetParams) (*Igroup, error) {
	if params == nil || (params.Name == nil && params.UUID == nil) {
		return nil, errors.New("missing required parameter 'name' or 'uuid' when getting a specific Igroup")
	}

	response, err := t.IGroupsGet(params)
	if err != nil {
		return nil, err
	}

	if len(response) == 0 {
		return nil, errors.NewNotFoundErr("igroup", nil)
	}

	if len(response) > 1 {
		return nil, errors.New("unexpected response when querying igroup")
	}

	return response[0], nil
}

// IGroupsGet invokes clients/ontap-rest/client/s_a_n/Client.IGroupCreate to create an initiator group
func (t *sanClient) IGroupsGet(params *IgroupGetParams) ([]*Igroup, error) {
	otParams := igroupGetParamsToONTAP(params)
	var igroups []*Igroup
	if err := paginateIgroupCollectionGet(func(next string) ([]*Igroup, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := t.api.IgroupCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Igroup, len(rsp.Payload.IgroupResponseInlineRecords))
		for i, igroup := range rsp.Payload.IgroupResponseInlineRecords {
			resp[i] = &Igroup{Igroup: *igroup}
		}

		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, func(ig []*Igroup) error {
		igroups = append(igroups, ig...)
		return nil
	}); err != nil {
		return nil, err
	}

	return igroups, nil
}

// IGroupAddInitiator invokes clients/ontap-rest/client/s_a_n/Client.IGroupInitiator to add a new initiator to an existing initiator group
func (t *sanClient) IGroupAddInitiator(params *IgroupAddInitiatorParams) error {
	_, err := t.api.IgroupInitiatorCreate(igroupAddInitiatorParamsToONTAP(params), nil)
	if err != nil {
		return err
	}

	return nil
}

// IGroupDeleteInitiator invokes clients/ontap-rest/client/s_a_n/Client.IGroupInitiator to delete an initiator from an existing initiator group
func (t *sanClient) IGroupDeleteInitiator(params *IgroupDeleteInitiatorParams) error {
	_, err := t.api.IgroupInitiatorDelete(igroupDeleteInitiatorParamsToONTAP(params), nil)
	if err != nil {
		return err
	}

	return nil
}

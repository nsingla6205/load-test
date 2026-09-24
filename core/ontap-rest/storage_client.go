package ontap_rest

import (
	"context"
	"strconv"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/storage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

type StorageClient interface {
	AggregateCollectionGet(params *AggregateCollectionGetParams, ucbf UserCallbackFunc[[]*Aggregate]) error
	AggregateFindByName(params *AggregateCollectionGetParams) (*Aggregate, error)
	AggregateModify(params *AggregateModifyParams) (*AggregateSimulate, *JobAccepted, error)

	SnapshotCollectionGet(params *SnapshotCollectionGetParams, ucbf UserCallbackFunc[[]*Snapshot]) error
	SnapshotPolicyGet(params *SnapshotPolicyGetParams) (*SnapshotPolicy, error)

	QosPolicyGroupCollectionGet(params *QosPolicyGroupCollectionGetParams, ucbf UserCallbackFunc[[]*QosPolicy]) error
	QosPolicyGroupCollectionModify(params []*QosPolicyGroupModifyCollectionParams) (*QosPolicyModifyCollection, *JobAccepted, error)
	QoSPolicyGroupCreate(params *QoSPolicyGroupCreateParams) (*QosPolicy, *JobAccepted, error)
	QoSPolicyGroupFind(params *QoSPolicyGroupFindParams) (*QosPolicy, error)
	QoSPolicyGroupUpdate(params *QoSPolicyGroupUpdateParams) (*JobAccepted, error)
	QosPolicyDeleteCollection(params *QosPolicyDeleteCollectionParams) (*JobAccepted, error)

	CloudStoreCreate(params *storage.CloudStoreCreateParams) (*JobAccepted, error)

	RootVolumeGet(params *VolumeGetParams) (*Volume, error)

	VolumeGet(params *VolumeGetParams) (*Volume, error)
	VolumeCollectionGet(params *VolumeCollectionGetParams, ucbf UserCallbackFunc[[]*Volume]) error
	VolumeModify(params *VolumeModifyParams) (bool, *JobAccepted, error)
	VolumeModifyCloudWriteMode(params *VolumeModifyParams) (bool, *JobAccepted, error)
	FlexCacheVolumeModify(params *FlexcacheModifyParams) (bool, *JobAccepted, error)
	VolumeCreate(params *VolumeCreateParams) (*Volume, *JobAccepted, error)
	FlexCacheVolumeCreate(params *FlexCacheVolumeCreateParams) (*Flexcache, *JobAccepted, error)
	VolumeDelete(params *VolumeDeleteParams) (*JobAccepted, error)
	VolumeMount(params *VolumeMountParams) (*JobAccepted, error)
	VolumeUnmount(params *VolumeUnmountParams) (*JobAccepted, error)
	FlexCacheVolumeDelete(params *FlexCacheVolumeDeleteParams) (*JobAccepted, error)

	SnapshotCreate(params *SnapshotCreateParams) (*Snapshot, *JobAccepted, error)
	SnapshotGet(params *SnapshotGetParams) (*Snapshot, error)
	SnapshotDelete(params *SnapshotDeleteParams) (bool, *JobAccepted, error)
	SnapshotPolicyCreate(params *SnapshotPolicyCreateParams) error
	SnapshotPolicyFind(fspp *SnapshotPolicyFindParams) (*models.SnapshotPolicy, error)
	SnapshotPolicyDelete(params *SnapshotPolicyDeleteParams) error
	SnapshotPolicyModify(params *SnapshotPolicyModifyParams) error
	SnapshotPolicyScheduleCreate(params *SnapshotPolicyScheduleCreateParams) (string, error)
	SnapshotPolicyScheduleModify(params *SnapshotPolicyScheduleModifyParams) error
	SnapshotPolicyScheduleDelete(params *SnapshotPolicyScheduleDeleteParams) error
	QuotaRuleCollectionGet(ctx context.Context, params *storage.QuotaRuleCollectionGetParams) (*storage.QuotaRuleCollectionGetOK, error)
	QuotaRuleCreate(ctx context.Context, params *storage.QuotaRuleCreateParams) (*storage.QuotaRuleCreateAccepted, error)
	QuotaRuleModify(ctx context.Context, params *storage.QuotaRuleModifyParams) (*storage.QuotaRuleModifyAccepted, error)
	QuotaRuleDelete(ctx context.Context, params *storage.QuotaRuleDeleteParams) (*storage.QuotaRuleDeleteAccepted, error)
}

// DefaultVolumeDetailFields lists ONTAP volume properties fetched after a name-based collection lookup.
var DefaultVolumeDetailFields = []string{
	"uuid",
	"name",
	"is_svm_root",
	"space.*",
	"state",
	"snapshot_policy.name",
	"qos.policy.uuid",
	"qos.policy.name",
	"type",
	"snapshot_directory_access_enabled",
	"constituents",
	"nas.path",
	"nas.security_style",
	"nas.unix_permissions",
	"nas.export_policy",
	"size",
	"style",
	"clone.*",
	"create_time",
}

// FetchVolumeDetails loads full volume details by UUID; overridable in tests.
var FetchVolumeDetails = _fetchVolumeDetails

type storageClient struct {
	api storage.ClientService
}

// RootVolumeGet invokes pkg/ontap-rest/client/storage/Client.RootVolumeGet
func (sc *storageClient) RootVolumeGet(params *VolumeGetParams) (*Volume, error) {
	response, err := sc.api.VolumeCollectionGet(storage.NewVolumeCollectionGetParams().WithIsSvmRoot(nillable.ToPointer("true")).WithFields(params.Fields), nil)
	if err != nil {
		return nil, err
	}

	if len(response.GetPayload().VolumeResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("Root volume for SVM", params.SvmName)
	}

	if len(response.GetPayload().VolumeResponseInlineRecords) > 1 {
		return nil, errors.New("unexpected response from server while getting root volume in svm: " + nillable.GetString(params.SvmName, ""))
	}

	return &Volume{Volume: *response.GetPayload().VolumeResponseInlineRecords[0]}, nil
}

// VolumeModify invokes pkg/ontap-rest/client/storage/Client.VolumeModify
func (sc *storageClient) VolumeModify(params *VolumeModifyParams) (bool, *JobAccepted, error) {
	// Success code response ignored, since it does not contain any useful data
	okResponse, acceptedResponse, err := sc.api.VolumeModify(volumeModifyParamsToONTAP(params), nil)
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

func (sc *storageClient) VolumeModifyCloudWriteMode(params *VolumeModifyParams) (bool, *JobAccepted, error) {
	okResponse, acceptedResponse, err := sc.api.VolumeModify(volumeModifyCloudWriteParamToONTAP(params), nil)
	if err != nil {
		return false, nil, err
	}
	if okResponse != nil {
		return true, nil, nil
	}

	job := &JobAccepted{
		JobUUID: acceptedResponse.Payload.Job.UUID.String(),
	}

	return true, job, nil
}

var paginateAggregateCollectionGet = _paginate[[]*Aggregate]

// AggregateCollectionGet invokes pkg/ontap-rest/client/storage/Client.AggregateCollectionGet
func (sc *storageClient) AggregateCollectionGet(params *AggregateCollectionGetParams, ucbf UserCallbackFunc[[]*Aggregate]) error {
	otParams := aggregateCollectionGetParamsToONTAP(params)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return paginateAggregateCollectionGet(func(next string) ([]*Aggregate, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := sc.api.AggregateCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Aggregate, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, a := range rsp.Payload.AggregateResponseInlineRecords {
			resp[i] = &Aggregate{Aggregate: *a}
		}
		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, ucbf)
}

// AggregateFindByName invokes pkg/ontap-rest/client/storage/Client.AggregateFindByName
func (sc *storageClient) AggregateFindByName(params *AggregateCollectionGetParams) (*Aggregate, error) {
	if nillable.FromPointerWithFallback(params.Name, "") == "" {
		return nil, errors.New("Aggregate name missing")
	}

	otParams := aggregateCollectionGetParamsToONTAP(params)
	rsp, err := sc.api.AggregateCollectionGet(otParams, nil)
	if err != nil {
		return nil, err
	}

	if len(rsp.Payload.AggregateResponseInlineRecords) > 1 {
		return nil, errors.New("More than one Aggregates returned with the name")
	}

	if len(rsp.Payload.AggregateResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("aggregate", params.Name)
	}

	return &Aggregate{Aggregate: *rsp.Payload.AggregateResponseInlineRecords[0]}, nil
}

// AggregateModify invokes pkg/ontap-rest/client/storage/Client.AggregateModify
func (sc *storageClient) AggregateModify(params *AggregateModifyParams) (*AggregateSimulate, *JobAccepted, error) {
	syncResponse, asyncResponse, err := sc.api.AggregateModify(aggregateModifyParamsToONTAP(params), nil)
	if err != nil {
		return nil, nil, err
	}
	if asyncResponse != nil {
		job := &JobAccepted{
			JobUUID: asyncResponse.Payload.Job.UUID.String(),
		}
		return nil, job, nil
	}
	return &AggregateSimulate{AggregateSimulate: *syncResponse.Payload}, nil, nil
}

var paginateQosPolicyGroupCollectionGet = _paginate[[]*QosPolicy]

// QosPolicyGroupCollectionGet invokes pkg/ontap-rest/client/storage/Client.QosPolicyCollectionGet
func (sc *storageClient) QosPolicyGroupCollectionGet(params *QosPolicyGroupCollectionGetParams, ucbf UserCallbackFunc[[]*QosPolicy]) error {
	otParams := qosPolicyGroupCollectionGetParamsToONTAPCollectionGet(params)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return paginateQosPolicyGroupCollectionGet(func(next string) ([]*QosPolicy, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := sc.api.QosPolicyCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*QosPolicy, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, qos := range rsp.Payload.QosPolicyResponseInlineRecords {
			resp[i] = &QosPolicy{QosPolicy: *qos}
		}
		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, ucbf)
}

// QosPolicyGroupCollectionModify invokes pkg/ontap-rest/client/storage/Client.QosPolicyModifyCollection
func (sc *storageClient) QosPolicyGroupCollectionModify(params []*QosPolicyGroupModifyCollectionParams) (*QosPolicyModifyCollection, *JobAccepted, error) {
	qosModifyparams := qosPolicyGroupCollectionModifyParamsToONTAP(params)
	syncResponse, asyncResponse, err := sc.api.QosPolicyModifyCollection(qosModifyparams, nil)
	if err != nil {
		return nil, nil, err
	}
	if asyncResponse != nil {
		job := &JobAccepted{
			JobUUID: asyncResponse.Payload.Job.UUID.String(),
		}
		return nil, job, nil
	}
	return &QosPolicyModifyCollection{QosPolicyJobLinkResponse: *syncResponse.Payload}, nil, nil
}

// QoSPolicyGroupCreate invokes pkg/ontap-rest/client/storage/Client.QosPolicyCreate
func (sc *storageClient) QoSPolicyGroupCreate(params *QoSPolicyGroupCreateParams) (*QosPolicy, *JobAccepted, error) {
	otParams := qosPolicyGroupCreateParamsToONTAP(params)
	created, accepted, err := sc.api.QosPolicyCreate(otParams, nil)
	if err != nil {
		return nil, nil, err
	}
	if accepted != nil && accepted.Payload != nil && accepted.Payload.Job != nil {
		job := &JobAccepted{JobUUID: accepted.Payload.Job.UUID.String()}
		return nil, job, nil
	}
	if created != nil && created.Payload != nil && len(created.Payload.Records) > 0 {
		return &QosPolicy{QosPolicy: *created.Payload.Records[0]}, nil, nil
	}
	return nil, nil, errors.New("unexpected response from server while creating QoS policy - received no QoS info")
}

// QoSPolicyGroupFind invokes pkg/ontap-rest/client/storage/Client.QosPolicyCollectionGet to find a QoS policy by UUID or name
func (sc *storageClient) QoSPolicyGroupFind(params *QoSPolicyGroupFindParams) (*QosPolicy, error) {
	if params.UUID == "" && params.Name == "" {
		return nil, errors.New("either UUID or Name must be provided for QoSPolicyGroupFind")
	}
	if params.UUID != "" && params.Name != "" {
		return nil, errors.New("UUID and Name cannot both be provided for QoSPolicyGroupFind")
	}

	otParams := storage.NewQosPolicyCollectionGetParams().WithFields([]string{"fixed.max_throughput_mbps", "fixed.max_throughput_iops", "fixed.capacity_shared", "name", "uuid", "svm.name"})
	if params.UUID != "" {
		otParams.SetUUID(&params.UUID)
	} else {
		otParams.SetName(&params.Name)
		if params.SvmName != "" {
			otParams.SetSvmName(&params.SvmName)
		}
	}
	otParams.SetMaxRecords(nillable.ToPointer("1"))

	rsp, err := sc.api.QosPolicyCollectionGet(otParams, nil)
	if err != nil {
		return nil, err
	}

	if len(rsp.Payload.QosPolicyResponseInlineRecords) == 0 {
		identifier := params.UUID
		if identifier == "" {
			identifier = params.Name
		}
		return nil, errors.NewNotFoundErr("qosPolicy", &identifier)
	}

	if len(rsp.Payload.QosPolicyResponseInlineRecords) > 1 {
		return nil, errors.New("multiple QoS policies found")
	}

	return &QosPolicy{QosPolicy: *rsp.Payload.QosPolicyResponseInlineRecords[0]}, nil
}

// QoSPolicyGroupUpdate invokes pkg/ontap-rest/client/storage/Client.QosPolicyModifyCollection to update a QoS policy
func (sc *storageClient) QoSPolicyGroupUpdate(params *QoSPolicyGroupUpdateParams) (*JobAccepted, error) {
	if params.UUID == "" {
		return nil, errors.New("no UUID provided for QoSPolicyGroupUpdate")
	}

	// Create the modify collection params
	modifyParams := []*QosPolicyGroupModifyCollectionParams{
		{
			UUID:       params.UUID,
			Name:       params.Name,
			Throughput: params.MaxThroughput,
			Iops:       params.MaxIOPS,
		},
	}

	// Use the existing collection modify method
	_, job, err := sc.QosPolicyGroupCollectionModify(modifyParams)
	if err != nil {
		return nil, err
	}

	return job, nil
}

// QosPolicyDeleteCollection invokes pkg/ontap-rest/client/storage/Client.QosPolicyDeleteCollection to delete QoS policy groups
func (sc *storageClient) QosPolicyDeleteCollection(params *QosPolicyDeleteCollectionParams) (*JobAccepted, error) {
	if params.UUID == "" && params.Name == "" {
		return nil, errors.New("either UUID or Name must be provided for QosPolicyDeleteCollection")
	}
	if params.UUID != "" && params.Name != "" {
		return nil, errors.New("UUID and Name cannot both be provided for QosPolicyDeleteCollection")
	}

	deleted, err := sc.api.QosPolicyDeleteCollection(qosPolicyDeleteCollectionParamsToONTAP(params), nil)
	if err != nil {
		// Check if it's a "not found" error - make delete idempotent
		if errors.IsNotFoundErr(err) {
			return nil, nil // Policy doesn't exist, consider deletion successful
		}
		return nil, err
	}

	// deleted is *QosPolicyDeleteCollectionOK (non-nil on success, nil on unexpected response)
	// When non-nil, it indicates successful synchronous deletion (200 OK), no job returned
	// This pattern matches VolumeDeleteCollection: if OK response is non-nil, return nil job (sync success)
	// See: core/ontap-rest/storage_client.go:553-554 for precedent
	if deleted != nil {
		return nil, nil
	}

	// If we reach here, deleted is nil but err is also nil - unexpected state
	return nil, errors.New("unexpected response from server while deleting QoS policy")
}

var paginateSnapshotCollectionGet = _paginate[[]*Snapshot]

// SnapshotCollectionGet invokes pkg/ontap-rest/client/storage/Client.SnapshotCollectionGet
func (sc *storageClient) SnapshotCollectionGet(params *SnapshotCollectionGetParams, ucbf UserCallbackFunc[[]*Snapshot]) error {
	otParams := snapshotCollectionGetParamsToONTAP(params)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	return paginateSnapshotCollectionGet(func(next string) ([]*Snapshot, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := sc.api.SnapshotCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Snapshot, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, s := range rsp.Payload.SnapshotResponseInlineRecords {
			resp[i] = &Snapshot{Snapshot: *s}
		}

		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, ucbf)
}

// SnapshotPolicyGet invokes pkg/ontap-rest/client/storage/Client.SnapshotPolicyGet
func (sc *storageClient) SnapshotPolicyGet(params *SnapshotPolicyGetParams) (*SnapshotPolicy, error) {
	response, err := sc.api.SnapshotPolicyGet(snapshotPolicyGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}
	resp := &SnapshotPolicy{SnapshotPolicy: *response.Payload}
	return resp, nil
}

var paginateVolumeCollectionGet = _paginate[[]*Volume]

// VolumeCollectionGet invokes pkg/ontap-rest/client/storage/Client.VolumeCollectionGet
func (sc *storageClient) VolumeCollectionGet(params *VolumeCollectionGetParams, ucbf UserCallbackFunc[[]*Volume]) error {
	otParams := volumeCollectionGetParamsToONTAP(params)
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))

	return paginateVolumeCollectionGet(func(next string) ([]*Volume, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := sc.api.VolumeCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Volume, nillable.FromPointer(rsp.Payload.NumRecords))
		for i, v := range rsp.Payload.VolumeResponseInlineRecords {
			resp[i] = &Volume{Volume: *v}
		}

		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, ucbf)
}

// VolumeGet invokes pkg/ontap-rest/client/storage/Client.VolumeGet
func (sc *storageClient) VolumeGet(params *VolumeGetParams) (*Volume, error) {
	if params.UUID != "" {
		response, err := sc.api.VolumeGet(volumeGetParamsToONTAP(params), nil)
		if err != nil {
			return nil, err
		}
		resp := &Volume{Volume: *response.Payload}
		return resp, nil
	}

	if params.Name == "" {
		return nil, errors.New("UUID and Name parameters cannot be empty when querying for a volume")
	}

	var vol *Volume
	otParams := volumeCollectionGetParamsToONTAP(&VolumeCollectionGetParams{
		BaseParams: params.BaseParams,
		Name:       &params.Name,
		SvmName:    params.SvmName,
	})
	otParams.SetMaxRecords(getConstrainedMaxRecords(params.MaxRecords))
	if err := paginateVolumeCollectionGet(func(next string) ([]*Volume, string, error) {
		otParams.SetContext(setNext(otParams.Context, next))

		rsp, err := sc.api.VolumeCollectionGet(otParams, nil)
		if err != nil {
			return nil, "", err
		}

		resp := make([]*Volume, len(rsp.Payload.VolumeResponseInlineRecords))
		for i, v := range rsp.Payload.VolumeResponseInlineRecords {
			resp[i] = &Volume{Volume: *v}
		}

		if rsp.Payload.Links != nil && rsp.Payload.Links.Next != nil {
			return resp, nillable.FromPointer(rsp.Payload.Links.Next.Href), nil
		}

		return resp, "", nil
	}, func(volumes []*Volume) error {
		if vol == nil && len(volumes) > 0 {
			// Volume collection resp only provides volume UUID and href link, so we need to fetch the full volume details (e.g., available space)
			volResp, err := FetchVolumeDetails(sc, volumes[0])
			if err != nil {
				return err
			}
			vol = volResp
		}

		return nil
	}); err != nil {
		return nil, err
	}

	if vol == nil {
		return nil, errors.NewNotFoundErr("volume", &params.Name)
	}

	return vol, nil
}

// VolumeGetWithContext calls the underlying storage API's VolumeGet method with context
func VolumeGetWithContext(ctx context.Context, restClient RESTClient, params *VolumeGetParams) (*storage.VolumeGetOK, error) {
	// Type assert to get the concrete OntapRestClient to access storage client
	ontapClient := restClient.(*OntapRestClient)
	// Access the storage client's underlying API
	storageClient := ontapClient.storage
	return storageClient.api.VolumeGet(VolumeGetParamsToONTAPQuotaRules(ctx, params), nil)
}

func _fetchVolumeDetails(sc *storageClient, volume *Volume) (*Volume, error) {
	response, err := sc.api.VolumeGet(volumeGetParamsToONTAP(&VolumeGetParams{
		UUID: *volume.UUID,
		BaseParams: BaseParams{
			Fields: DefaultVolumeDetailFields,
		},
	}), nil)
	if err != nil {
		return nil, err
	}

	if response == nil || response.Payload == nil {
		return nil, errors.New("unexpected response from VolumeGet")
	}

	return &Volume{Volume: *response.Payload}, nil
}

// CloudStoreCreate invokes pkg/ontap-rest/client/storage/Client.CloudStoreCreate
func (sc *storageClient) CloudStoreCreate(params *storage.CloudStoreCreateParams) (*JobAccepted, error) {
	_, responseAccepted, err := sc.api.CloudStoreCreate(params, nil)
	if err != nil {
		return nil, err
	}

	if responseAccepted == nil || responseAccepted.Payload == nil || responseAccepted.Payload.Job == nil {
		return nil, errors.New("unexpected response from CloudStoreCreate")
	}

	return &JobAccepted{
		JobUUID: responseAccepted.Payload.Job.UUID.String(),
	}, nil
}

// VolumeCreate invokes pkg/ontap-rest/client/storage/Client.VolumeCreate
func (sc *storageClient) VolumeCreate(params *VolumeCreateParams) (*Volume, *JobAccepted, error) {
	created, accepted, err := sc.api.VolumeCreate(volumeCreateParamsToONTAP(params), nil)
	if err != nil {
		return nil, nil, err
	}

	if created != nil {
		if len(created.Payload.Records) == 0 {
			return nil, nil, errors.New("unexpected response from server while creating volume - received no volume info")
		}

		if len(created.Payload.Records) > 1 {
			return nil, nil, errors.New("unexpected response from server while creating volume - did not receive exactly one volume")
		}
		return &Volume{Volume: *created.Payload.Records[0]}, nil, nil
	}

	if len(accepted.Payload.Records) == 0 {
		return nil, nil, errors.New("unexpected response from server while creating volume - received no volume info")
	}

	if len(accepted.Payload.Records) > 1 {
		return nil, nil, errors.New("unexpected response from server while creating volume - did not receive exactly one volume")
	}

	return &Volume{Volume: *accepted.Payload.Records[0]}, &JobAccepted{
		JobUUID: string(*accepted.Payload.Job.UUID),
	}, nil
}

// VolumeDelete invokes pkg/ontap-rest/client/storage/Client.VolumeDelete to delete Volume
func (sc *storageClient) VolumeDelete(params *VolumeDeleteParams) (*JobAccepted, error) {
	if params.UUID != "" {
		deleted, accepted, err := sc.api.VolumeDelete(volumeDeleteParamsToONTAP(params), nil)
		if err != nil {
			return nil, err
		}

		if accepted != nil {
			return &JobAccepted{
				JobUUID: string(*accepted.Payload.Job.UUID),
			}, nil
		}

		if deleted != nil {
			return nil, nil
		}
	}

	if params.Name == "" {
		return nil, errors.New("no name filter provided for VolumeDeleteCollection")
	}

	collectionDeleted, collectionAccepted, err := sc.api.VolumeDeleteCollection(volumeDeleteParamsToONTAPCollectionDelete(params), nil)
	if err != nil {
		return nil, err
	}

	if collectionAccepted != nil {
		return &JobAccepted{
			JobUUID: string(*collectionAccepted.Payload.Job.UUID),
		}, nil
	}

	if collectionDeleted != nil {
		return nil, nil
	}

	return nil, errors.New("unexpected response from server while deleting volume")
}

// VolumeUnmount invokes pkg/ontap-rest/client/storage/Client.VolumeModify to unmount a volume
func (sc *storageClient) VolumeUnmount(params *VolumeUnmountParams) (*JobAccepted, error) {
	_, accepted, err := sc.api.VolumeModify(volumeUnmountParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	job := &JobAccepted{
		JobUUID: accepted.Payload.Job.UUID.String(),
	}
	return job, nil
}

// VolumeMount invokes pkg/ontap-rest/client/storage/Client.VolumeModify to mount a volume
func (sc *storageClient) VolumeMount(params *VolumeMountParams) (*JobAccepted, error) {
	_, accepted, err := sc.api.VolumeModify(volumeMountParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}

	job := &JobAccepted{
		JobUUID: accepted.Payload.Job.UUID.String(),
	}
	return job, nil
}

// SnapshotCreate invokes pkg/ontap-rest/client/storage/Client.CreateSnapshot to create Snapshot in a volume
func (sc *storageClient) SnapshotCreate(params *SnapshotCreateParams) (*Snapshot, *JobAccepted, error) {
	created, accepted, err := sc.api.SnapshotCreate(snapshotCreateParamsToONTAP(params), nil)
	if err != nil {
		if strings.Contains(err.Error(), "snapshot with that name already exists") {
			return nil, nil, errors.NewConflictErr("snapshot with that name already exists")
		}
		// Return BadRequest for ONTAP error when trying to create snapshot on non-RW volume
		if strings.Contains(err.Error(), "Snapshots can only be created on read/write") {
			return nil, nil, errors.NewBadRequestErr("snapshot creation operation not allowed for this volume")
		}
		return nil, nil, err
	}

	if created != nil {
		if len(created.Payload.Records) != 1 {
			return nil, nil, errors.Errorf("SnapshotCreate invalid created response from storage server - Expected a single record but got: '%d'", len(created.Payload.Records))
		}
		return &Snapshot{Snapshot: *created.Payload.Records[0]}, nil, nil
	}

	if len(accepted.Payload.Records) != 1 {
		return nil, nil, errors.Errorf("SnapshotCreate invalid accepted response from storage server - Expected a single record but got: '%d'", len(accepted.Payload.Records))
	}

	return &Snapshot{Snapshot: *accepted.Payload.Records[0]},
		&JobAccepted{
			JobUUID:      string(*accepted.Payload.Job.UUID),
			ResourceUUID: *accepted.Payload.Records[0].UUID},
		nil
}

func (sc *storageClient) SnapshotGet(params *SnapshotGetParams) (*Snapshot, error) {
	snapshot, err := sc.api.SnapshotGet(snapshotGetParamsToONTAP(params), nil)
	if err != nil {
		return nil, err
	}
	return &Snapshot{Snapshot: *snapshot.Payload}, nil
}

// SnapshotDelete invokes pkg/ontap-rest/client/storage/Client.SnapshotDelete to delete Snapshot in a volume
func (sc *storageClient) SnapshotDelete(params *SnapshotDeleteParams) (bool, *JobAccepted, error) {
	if params != nil && params.UUID != "" {
		done, res, err := sc.api.SnapshotDelete(snapshotDeleteParamsToONTAP(params), nil)
		if err != nil {
			return false, nil, err
		}
		if done != nil {
			return true, nil, nil
		}
		return false, &JobAccepted{JobUUID: string(*res.Payload.Job.UUID)}, nil
	}
	return false, nil, errors.New("no UUID provided for SnapshotDelete")
}

// SnapshotPolicyCreate invokes pkg/ontap-rest/client/storage/Client.SnapshotPolicyCreate
func (sc *storageClient) SnapshotPolicyCreate(params *SnapshotPolicyCreateParams) error {
	_, err := sc.api.SnapshotPolicyCreate(snapshotPolicyCreateParamsToONTAP(params), nil)
	return err
}

// SnapshotPolicyFind invokes SnapshotPolicyCollectionGet to find exactly one SnapshotPolicy
func (sc *storageClient) SnapshotPolicyFind(fspp *SnapshotPolicyFindParams) (*models.SnapshotPolicy, error) {
	if fspp.Name == "" {
		return nil, errors.New("no name filter provided for SnapshotPolicyFind")
	}

	resp, err := sc.api.SnapshotPolicyCollectionGet(storage.NewSnapshotPolicyCollectionGetParams().WithName(&fspp.Name).WithFields(fspp.Fields).
		WithMaxRecords(nillable.ToPointer("1")), nil)
	if err != nil {
		return nil, err
	}

	if len(resp.Payload.SnapshotPolicyResponseInlineRecords) == 0 {
		return nil, errors.NewNotFoundErr("snapshotPolicy", &fspp.Name)
	}

	return resp.Payload.SnapshotPolicyResponseInlineRecords[0], nil
}

// SnapshotPolicyDelete invokes clients/ontap-rest/client/storage/Client.SnapshotPolicyDeleteCollection
func (sc *storageClient) SnapshotPolicyDelete(params *SnapshotPolicyDeleteParams) error {
	_, err := sc.api.SnapshotPolicyDeleteCollection(snapshotPolicyDeleteParamsToONTAPCollectionDelete(params), nil)
	return err
}

// SnapshotPolicyModify invokes clients/ontap-rest/client/storage/Client.SnapshotPolicyModify
func (sc *storageClient) SnapshotPolicyModify(params *SnapshotPolicyModifyParams) error {
	_, err := sc.api.SnapshotPolicyModify(convertSnapshotPolicyModifyParamsToOntap(params), nil)
	return err
}

// SnapshotPolicyScheduleCreate invokes clients/ontap-rest/client/storage/Client.SnapshotPolicyScheduleCreate
func (sc *storageClient) SnapshotPolicyScheduleCreate(params *SnapshotPolicyScheduleCreateParams) (string, error) {
	result, err := sc.api.SnapshotPolicyScheduleCreate(convertSnapshotPolicyScheduleCreateParamsToONTAP(params), nil)
	if err != nil {
		return "", err
	}

	recordParts := strings.Split(result.Location, "/")
	return recordParts[len(recordParts)-1], nil
}

// SnapshotPolicyScheduleModify invokes clients/ontap-rest/client/storage/Client.SnapshotPolicyScheduleModify
func (sc *storageClient) SnapshotPolicyScheduleModify(params *SnapshotPolicyScheduleModifyParams) error {
	_, err := sc.api.SnapshotPolicyScheduleModify(convertSnapshotPolicyScheduleModifyParamsToONTAP(params), nil)
	return err
}

// SnapshotPolicyScheduleDelete invokes clients/ontap-rest/client/storage/Client.SnapshotPolicyScheduleDelete
func (sc *storageClient) SnapshotPolicyScheduleDelete(params *SnapshotPolicyScheduleDeleteParams) error {
	_, err := sc.api.SnapshotPolicyScheduleDelete(convertSnapshotPolicyScheduleDeleteParamsToONTAP(params), nil)
	return err
}

// QuotaRuleCollectionGet invokes clients/ontap-rest/client/storage/Client.QuotaRuleCollectionGet
func (sc *storageClient) QuotaRuleCollectionGet(ctx context.Context, params *storage.QuotaRuleCollectionGetParams) (*storage.QuotaRuleCollectionGetOK, error) {
	return sc.api.QuotaRuleCollectionGet(params, nil)
}

// QuotaRuleCreate invokes clients/ontap-rest/client/storage/Client.QuotaRuleCreate
func (sc *storageClient) QuotaRuleCreate(ctx context.Context, params *storage.QuotaRuleCreateParams) (*storage.QuotaRuleCreateAccepted, error) {
	_, response, err := sc.api.QuotaRuleCreate(params, nil)
	return response, err
}

// QuotaRuleModify invokes clients/ontap-rest/client/storage/Client.QuotaRuleModify
func (sc *storageClient) QuotaRuleModify(ctx context.Context, params *storage.QuotaRuleModifyParams) (*storage.QuotaRuleModifyAccepted, error) {
	_, acceptedResponse, err := sc.api.QuotaRuleModify(params, nil)
	return acceptedResponse, err
}

// QuotaRuleDelete invokes clients/ontap-rest/client/storage/Client.QuotaRuleDelete
func (sc *storageClient) QuotaRuleDelete(ctx context.Context, params *storage.QuotaRuleDeleteParams) (*storage.QuotaRuleDeleteAccepted, error) {
	_, acceptedResponse, err := sc.api.QuotaRuleDelete(params, nil)
	return acceptedResponse, err
}

func snapshotCreateParamsToONTAP(params *SnapshotCreateParams) *storage.SnapshotCreateParams {
	if params == nil {
		return nil
	}
	returnTimeout = strconv.FormatInt(int64(utils.GetConstraintInteger(env.GetUint("ONTAP_REST_SYNC_RETURN_TIMEOUT_SECONDS", 15), 0, 15, 15)), 10)
	return storage.NewSnapshotCreateParams().
		WithVolumeUUID(params.VolumeUUID).
		WithInfo(&models.Snapshot{
			Name:    &params.Name,
			Comment: params.Comment,
		}).
		WithReturnRecords(nillable.ToPointer("true")).
		WithReturnTimeout(&returnTimeout)
}

func snapshotGetParamsToONTAP(params *SnapshotGetParams) *storage.SnapshotGetParams {
	if params == nil {
		return nil
	}
	return storage.NewSnapshotGetParams().
		WithUUID(params.UUID).
		WithVolumeUUID(params.VolumeUUID).
		WithFields(params.Fields)
}

func snapshotDeleteParamsToONTAP(params *SnapshotDeleteParams) *storage.SnapshotDeleteParams {
	otParams := storage.NewSnapshotDeleteParams()
	otParams.SetUUID(params.UUID)
	otParams.SetVolumeUUID(params.VolumeUUID)
	otParams.SetReturnTimeout(&returnTimeout)
	return otParams
}

func snapshotPolicyCreateParamsToONTAP(params *SnapshotPolicyCreateParams) *storage.SnapshotPolicyCreateParams {
	otParams := storage.NewSnapshotPolicyCreateParams()
	if params == nil {
		return otParams
	}

	schedules := make([]*models.SnapshotPolicyInlineCopiesInlineArrayItem, len(params.Schedules))
	for i, schedule := range params.Schedules {
		count := schedule.Count
		schedules[i] = &models.SnapshotPolicyInlineCopiesInlineArrayItem{
			Count:           &count,
			Prefix:          &schedule.Prefix,
			SnapmirrorLabel: &schedule.SnapmirrorLabel,
			Schedule:        &models.SnapshotPolicyInlineCopiesInlineArrayItemInlineSchedule{Name: &schedule.Name},
		}
	}
	otParams.Info = &models.SnapshotPolicy{
		Name:                       params.Name,
		Comment:                    params.Comment,
		Enabled:                    params.Enabled,
		SnapshotPolicyInlineCopies: schedules,
	}
	return otParams
}

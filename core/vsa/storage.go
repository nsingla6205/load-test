package vsa

import (
	"fmt"
	"strings"

	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

var (
	getOntapClientFunc = getOntapClient
)

func getOntapClient(clientParams ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
	// When the caller opts into fast host selection, route through the two-phase
	// builder. It self-gates on the ONTAP_REST_FAST_HOST_SELECT deployment flag
	// and transparently falls back to the standard client when disabled, so only
	// opted-in paths (e.g. OCI reset-password) can ever exercise the fast path.
	if clientParams.FastHostSelect {
		return ontapRest.NewClientWithFastHostSelect(clientParams)
	}
	return ontapRest.NewOntapRestClient(clientParams)
}

func (rc *OntapRestProvider) IsAggregateOnline(aggregateName string) (bool, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return false, err
	}
	aggr, err := client.Storage().AggregateFindByName(&ontapRest.AggregateCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"state"},
		},
		Name: &aggregateName,
	})
	if err != nil {
		return false, err
	}
	if aggr == nil {
		return false, nil
	}
	return aggr.IsOnline(), nil
}

func (rc *OntapRestProvider) GetAggregates() ([]*Aggregate, error) {
	resultAggregates := make([]*Aggregate, 0) // Initialize as empty slice instead of nil

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	ucbf := func(aggregates []*ontapRest.Aggregate) error {
		for _, aggregate := range aggregates {
			if aggregate == nil {
				continue
			}
			agg := &Aggregate{
				Name:        nillable.GetString(aggregate.Name, ""),
				State:       nillable.GetString(aggregate.State, ""),
				VolumeCount: nillable.GetInt64(aggregate.VolumeCount, 0),
				UUID:        nillable.GetString(aggregate.UUID, ""),
			}
			if aggregate.Space != nil {
				if aggregate.Space.BlockStorage != nil {
					if aggregate.Space.BlockStorage.Size != nil {
						agg.Size = *aggregate.Space.BlockStorage.Size
					}
					if aggregate.Space.BlockStorage.Available != nil {
						agg.AvailableSize = *aggregate.Space.BlockStorage.Available
					}
					if aggregate.Space.BlockStorage.Used != nil {
						agg.UsedSize = *aggregate.Space.BlockStorage.Used
					}
				}
				if aggregate.Space.TotalProvisionedSpace != nil {
					agg.TotalProvisionedSize = *aggregate.Space.TotalProvisionedSpace
				}
			}
			resultAggregates = append(resultAggregates, agg)
		}
		return nil
	}

	err = client.Storage().AggregateCollectionGet(&ontapRest.AggregateCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "uuid", "state", "volume-count", "space"},
		},
	}, ucbf)

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultAggregates, nil
}

func (rc *OntapRestProvider) GetAggregateByName(name string) (*Aggregate, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	aggr, err := client.Storage().AggregateFindByName(&ontapRest.AggregateCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "state"},
		},
		Name: &name,
	})
	if err != nil {
		return nil, err
	}
	if aggr == nil {
		return nil, fmt.Errorf("aggregate not found")
	}
	return &Aggregate{
		Name:  *aggr.Name,
		State: *aggr.State,
		UUID:  *aggr.UUID,
	}, nil
}

// UpdateAggregate updates an aggregate by calling the ONTAP REST Client
func (rc *OntapRestProvider) UpdateAggregate(params UpdateAggregateParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	resp, job, err := client.Storage().AggregateModify(&ontapRest.AggregateModifyParams{
		UUID:                     params.UUID,
		TieringFullnessThreshold: &params.TieringFullnessThreshold,
	})
	if err != nil {
		return err
	}
	if resp != nil && resp.NumRecords != nil && *resp.NumRecords > 0 {
		return nil
	}
	return client.Poll(job.JobUUID)
}

// LunCreate creates a LUN by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunCreate(params LunCreateParams) (*LunResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	lun, err := client.SAN().LunCreate(&ontapRest.LunCreateParams{
		Name:                           params.LunName,
		SvmName:                        params.SvmName,
		OsType:                         params.OsType,
		VolumeName:                     params.VolumeName,
		Size:                           params.Size,
		ThinProvisioningSupportEnabled: nillable.ToPointer(true),
	})
	if err != nil {
		if strings.Contains(err.Error(), "A LUN or NVMe namespace already exists") {
			return nil, errors.NewConflictErr(fmt.Sprintf("LUN %s already exists in SVM %s", params.LunName, params.SvmName))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return &LunResponse{
		ProviderResponse: ProviderResponse{
			Name:         *lun.Name,
			ExternalUUID: *lun.UUID,
		},
		SerialNumber: func() string {
			if lun.SerialNumberHex != nil {
				return *lun.SerialNumberHex
			}
			return ""
		}(),
		Size: func() int64 {
			if lun.Space != nil && lun.Space.Size != nil {
				return *lun.Space.Size
			}
			return 0
		}(),
		OSType: func() string {
			if lun.OsType != nil {
				return *lun.OsType
			}
			return ""
		}(),
	}, nil
}

// LunGet retrieves a LUN by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunGet(params LunGetParams) (*LunResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	var lunName *string
	if params.LunName != "" {
		lunName = &params.LunName
	}
	lun, err := client.SAN().LunGet(&ontapRest.LunGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"status.*", "serial_number_hex", "class", "space.size", "location.*", "os_type", "lun_maps"},
		},
		SvmName:    &params.SvmName,
		VolumeName: &params.VolumeName,
		LunName:    lunName,
	})

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if len(lun) == 0 {
		identifier := fmt.Sprintf("svm=%s, volume=%s, lun=%s", params.SvmName, params.VolumeName, params.LunName)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.NewNotFoundErr("lun", &identifier))
	}
	return lunResponseFromONTAP(lun[0]), nil
}

// LunList retrieves all LUNs by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunList(params LunGetParams) ([]*LunResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	var lunName *string
	if params.LunName != "" {
		lunName = &params.LunName
	}
	luns, err := client.SAN().LunGet(&ontapRest.LunGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"status.*", "serial_number_hex", "class", "space.size", "location.*", "os_type", "lun_maps"},
		},
		SvmName:    &params.SvmName,
		VolumeName: &params.VolumeName,
		LunName:    lunName,
	})

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if len(luns) == 0 {
		identifier := fmt.Sprintf("svm=%s, volume=%s, lun=%s", params.SvmName, params.VolumeName, params.LunName)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, errors.NewNotFoundErr("lun", &identifier))
	}

	result := make([]*LunResponse, len(luns))
	for i, lun := range luns {
		result[i] = lunResponseFromONTAP(lun)
	}
	return result, nil
}

func lunResponseFromONTAP(lun *ontapRest.Lun) *LunResponse {
	if lun == nil {
		return nil
	}
	response := &LunResponse{
		ProviderResponse: ProviderResponse{
			Name: nillable.GetString(lun.Name, ""),
		},
		SerialNumber: func() string {
			if lun.SerialNumberHex != nil {
				return *lun.SerialNumberHex
			}
			return ""
		}(),
		Size: func() int64 {
			if lun.Space != nil && lun.Space.Size != nil {
				return *lun.Space.Size
			}
			return 0
		}(),
		OSType: func() string {
			if lun.OsType != nil {
				return *lun.OsType
			}
			return ""
		}(),
	}
	if lun.UUID != nil {
		response.ExternalUUID = *lun.UUID
	}
	for _, lunMap := range lun.LunInlineLunMaps {
		if lunMap != nil && lunMap.Igroup != nil && lunMap.Igroup.Name != nil {
			response.IgroupNames = append(response.IgroupNames, *lunMap.Igroup.Name)
		}
	}
	return response
}

// NamespaceList retrieves all NVMe namespaces for the given SVM and volume.
func (rc *OntapRestProvider) NamespaceList(params NvmeNamespaceGetParams) ([]*NvmeNamespaceResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	var namespaceName *string
	if params.NamespaceName != "" {
		namespaceName = &params.NamespaceName
	}

	namespaces, err := client.NVMe().NamespaceGet(&ontapRest.NvmeNamespaceGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"name", "uuid", "svm", "location"},
		},
		SvmName:       &params.SvmName,
		VolumeName:    &params.VolumeName,
		NamespaceName: namespaceName,
	})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	result := make([]*NvmeNamespaceResponse, len(namespaces))
	for i, ns := range namespaces {
		name := nillable.FromPointer(ns.Name)
		uuid := nillable.FromPointer(ns.UUID)
		result[i] = &NvmeNamespaceResponse{
			ProviderResponse: ProviderResponse{
				Name:         name,
				ExternalUUID: uuid,
			},
		}
	}
	return result, nil
}

// LunUpdate updates the LUN by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunUpdate(params LunUpdateParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	success, job, err := client.SAN().LunUpdate(&ontapRest.LunUpdateParams{
		UUID:       params.UUID,
		Name:       params.LunName,
		SvmName:    params.SvmName,
		VolumeName: params.VolumeName,
		Size:       params.Size,
	})
	if err != nil {
		if strings.Contains(err.Error(), "New LUN size is the same as the old LUN size") {
			return errors.NewConflictErr(fmt.Sprintf("LUN %s already has the specified size", params.LunName))
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrLunUpdate, err)
	}
	if success {
		return nil
	}
	return client.Poll(job.JobUUID)
}

// LunMapCreate creates a LUN mapping by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunMapCreate(params LunMapCreateParams) error {
	for i := 0; i < len(params.IGroupName); i++ {
		client, err := getOntapClientFunc(rc.ClientParams)
		if err != nil {
			return err
		}
		if err := client.SAN().LunMapCreate(&ontapRest.LunMapCreateParams{
			LunName:    params.LunName,
			SvmName:    params.SvmName,
			IGroupName: params.IGroupName[i],
		}); err != nil && !strings.Contains(err.Error(), "LUN already mapped") {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}
	return nil
}

// LunMapDelete deletes a LUN mapping by calling the ONTAP REST Client
func (rc *OntapRestProvider) LunMapDelete(params LunMapDeleteParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	if err := client.SAN().LunMapDelete(&ontapRest.LunMapDeleteParams{
		LunUUID:    params.LunUUID,
		IGroupUUID: params.IGroupUUID,
	}); err != nil && !strings.Contains(err.Error(), "was not found") {
		return err
	}
	return nil
}

// IscsiServiceCreate creates an iSCSI service by calling the ONTAP REST Client
func (rc *OntapRestProvider) IscsiServiceCreate(svmUUID string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	err = client.SAN().IscsiServiceCreate(&ontapRest.IscsiCreateParams{
		SvmUUID: svmUUID})
	if err != nil {
		return err
	}
	return nil
}

func (rc *OntapRestProvider) SnapshotGet(snapshotUUID, volumeUUID, snapshotName string) (*ontapRest.Snapshot, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	snapshot, err := client.Storage().SnapshotGet(&ontapRest.SnapshotGetParams{
		Name:       snapshotName,
		UUID:       snapshotUUID,
		VolumeUUID: volumeUUID,
	})
	if err != nil {
		return nil, err
	}

	// Validate the Snapshot response to avoid nil pointer dereferences
	if snapshot == nil || snapshot.Name == nil || snapshot.UUID == nil {
		return nil, errors.New("invalid Snapshot response from API")
	}
	return snapshot, nil
}

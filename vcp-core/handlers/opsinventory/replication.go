package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// Replication rows also carry hybrid and cluster peering states.
var replicationStates = stateSet(lifecycleStates, resourceEventStates, []string{
	string(datamodel.HybridReplicationStatusPendingClusterPeer), string(datamodel.HybridReplicationStatusPendingSVMPeer),
	string(datamodel.HybridReplicationStatusSVMPeered), string(datamodel.HybridReplicationStatusPeered),
	string(datamodel.HybridReplicationStatusPendingRemoteResync), string(datamodel.HybridReplicationStatusExternalManaged),
	string(datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING), string(datamodel.CvpClusterPeeringStatusPEERED),
})

type replicationReader struct {
	store readstore.ReadStore
}

// NewReplicationReader returns the replication reader.
func NewReplicationReader(store readstore.ReadStore) Reader {
	return &replicationReader{store: store}
}

func (r *replicationReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeReplication,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		NameParents:   []ParentType{oasgenserver.OpsParentTypeV1Volume},
		ListParents: map[ParentType]ParentSpec{
			oasgenserver.OpsParentTypeV1Volume: {Column: readstore.ColVolumeID, Owner: readstore.OwnerVolume, ByID: true, CheckOwner: true},
			// The other side of a replication may live in another region or
			// project, so these are plain uuid matches on the same table.
			oasgenserver.OpsParentTypeV1SourceVolume:      {Column: readstore.ColReplicationSourceVolumeUUID},
			oasgenserver.OpsParentTypeV1DestinationVolume: {Column: readstore.ColReplicationDestinationVolumeUUID},
		},
		NameFilter:  true,
		StateColumn: readstore.ColState,
		States:      replicationStates,
		TimeFields:  baseTimeFields,
	}
}

func (r *replicationReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		v   *datamodel.VolumeReplication
		err error
	)
	if req.UUID != "" {
		v, err = r.store.GetReplicationByUUID(ctx, req.UUID)
	} else {
		v, err = r.store.GetReplicationByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return replicationResource(v), nil
}

func (r *replicationReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListReplications(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, replicationResource(&rows[i]))
	}
	return out, nil
}

// replicationResource never maps peering passphrases, peer commands, cluster
// peer associations, host names or raw transfer errors.
func replicationResource(v *datamodel.VolumeReplication) Resource {
	details, withheld := stateDetails(v.StateDetails)
	dto := oasgenserver.OpsReplicationV1{
		ResourceType:             string(ResourceTypeReplication),
		Source:                   oasgenserver.OpsSourceV1VcpDb,
		UUID:                     v.UUID,
		Name:                     optStr(v.Name),
		Description:              optStr(v.Description),
		State:                    optStr(v.State),
		StateDetails:             details,
		StateDetailsWithheld:     withheld,
		ProjectNumber:            accountName(v.Account),
		VolumeUuid:               refUUID(v.Volume, func(vol *datamodel.Volume) string { return vol.UUID }),
		VolumeName:               refUUID(v.Volume, func(vol *datamodel.Volume) string { return vol.Name }),
		MirrorState:              optStrPtr(v.MirrorState),
		RelationshipStatus:       optStrPtr(v.RelationshipStatus),
		Healthy:                  oasgenserver.NewOptBool(v.Healthy),
		UnhealthyReasonPresent:   oasgenserver.NewOptBool(v.UnhealthyReason != ""),
		LastTransferErrorPresent: oasgenserver.NewOptBool(v.LastTransferError != ""),
		LagTime:                  oasgenserver.NewOptInt64(v.LagTime),
		TotalProgress:            oasgenserver.NewOptInt64(v.TotalProgress),
		TotalTransferBytes:       oasgenserver.NewOptInt64(v.TotalTransferBytes),
		TotalTransferTimeSecs:    oasgenserver.NewOptInt64(v.TotalTransferTimeSecs),
		LastTransferSize:         oasgenserver.NewOptInt64(v.LastTransferSize),
		LastTransferDuration:     oasgenserver.NewOptInt64(v.LastTransferDuration),
		LastTransferEndTime:      optTimePtr(v.LastTransferEndTime),
		ProgressLastUpdated:      optTimePtr(v.ProgressLastUpdated),
		LastUpdatedFromOntap:     optTime(v.LastUpdatedFromOntap),
		IsHybrid:                 oasgenserver.NewOptBool(v.HybridReplicationAttributes != nil),
		CreatedAt:                v.CreatedAt.UTC(),
		UpdatedAt:                v.UpdatedAt.UTC(),
		DeletedAt:                deletedAt(v.DeletedAt),
	}
	if a := v.ReplicationAttributes; a != nil {
		dto.EndpointType = optStr(a.EndpointType)
		dto.ReplicationType = optStr(a.ReplicationType)
		dto.ReplicationSchedule = optStr(a.ReplicationSchedule)
		dto.SourcePoolUuid = optStr(a.SourcePoolUUID)
		dto.SourceVolumeUuid = optStr(a.SourceVolumeUUID)
		dto.SourceVolumeName = optStr(a.SourceVolumeName)
		dto.SourceSvmName = optStr(a.SourceSvmName)
		dto.SourceLocation = optStr(a.SourceLocation)
		dto.DestinationPoolUuid = optStr(a.DestinationPoolUUID)
		dto.DestinationVolumeUuid = optStr(a.DestinationVolumeUUID)
		dto.DestinationVolumeName = optStr(a.DestinationVolumeName)
		dto.DestinationSvmName = optStr(a.DestinationSvmName)
		dto.DestinationLocation = optStr(a.DestinationLocation)
		dto.ExternalUuid = optStr(a.ExternalUUID)
	}
	return Resource{
		ID:             v.ID,
		OwnerAccountID: ownerID(v.AccountID),
		Value:          oasgenserver.NewOpsReplicationV1OpsResourceV1(dto),
	}
}

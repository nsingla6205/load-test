package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var volumeStates = stateSet(lifecycleStates, resourceEventStates, []string{datamodel.VolumeStateOffline})

type volumeReader struct {
	store readstore.ReadStore
}

// NewVolumeReader returns the volume reader.
func NewVolumeReader(store readstore.ReadStore) Reader {
	return &volumeReader{store: store}
}

func (r *volumeReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeVolume,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		// Account + name alone is ambiguous across zonal pools.
		NameParents: []ParentType{oasgenserver.OpsParentTypeV1Pool},
		ListParents: map[ParentType]ParentSpec{
			oasgenserver.OpsParentTypeV1Pool:                   {Column: readstore.ColPoolID, Owner: readstore.OwnerPool, ByID: true, CheckOwner: true},
			oasgenserver.OpsParentTypeV1Svm:                    {Column: readstore.ColSvmID, Owner: readstore.OwnerSvm, ByID: true, CheckOwner: true},
			oasgenserver.OpsParentTypeV1VolumePerformanceGroup: {Column: readstore.ColVolumePerformanceGroupID, Owner: readstore.OwnerVolumePerformanceGroup, ByID: true, CheckOwner: true},
			oasgenserver.OpsParentTypeV1BackupVault:            {Column: readstore.ColVolumeBackupVaultUUID, Owner: readstore.OwnerBackupVault, CheckOwner: true},
			oasgenserver.OpsParentTypeV1BackupPolicy:           {Column: readstore.ColVolumeBackupPolicyUUID, Owner: readstore.OwnerBackupPolicy, CheckOwner: true},
		},
		NameFilter:  true,
		StateColumn: readstore.ColState,
		States:      volumeStates,
		TimeFields:  baseTimeFields,
	}
}

func (r *volumeReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		v   *datamodel.Volume
		err error
	)
	if req.UUID != "" {
		v, err = r.store.GetVolumeByUUID(ctx, req.UUID)
	} else {
		v, err = r.store.GetVolumeByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return volumeResource(v), nil
}

func (r *volumeReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListVolumes(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, volumeResource(&rows[i]))
	}
	return out, nil
}

func volumeResource(v *datamodel.Volume) Resource {
	details, withheld := stateDetails(v.StateDetails)
	dto := oasgenserver.OpsVolumeV1{
		ResourceType:         string(ResourceTypeVolume),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 v.UUID,
		Name:                 optStr(v.Name),
		Description:          optStr(v.Description),
		State:                optStr(v.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		Health:               optStr(v.Health),
		ProjectNumber:        accountName(v.Account),
		PoolUuid:             refUUID(v.Pool, func(p *datamodel.Pool) string { return p.UUID }),
		PoolName:             refUUID(v.Pool, func(p *datamodel.Pool) string { return p.Name }),
		SvmUuid:              refUUID(v.Svm, func(s *datamodel.Svm) string { return s.UUID }),
		SvmName:              refUUID(v.Svm, func(s *datamodel.Svm) string { return s.Name }),
		VolumePerformanceGroupUuid: refUUID(v.VolumePerformanceGroup, func(g *datamodel.VolumePerformanceGroup) string {
			return g.UUID
		}),
		SizeInBytes:        oasgenserver.NewOptInt64(v.SizeInBytes),
		UsedBytes:          oasgenserver.NewOptInt64(int64(v.UsedBytes)),
		Throughput:         oasgenserver.NewOptInt64(v.Throughput),
		MountPath:          optStr(v.MountPath),
		AutoTieringEnabled: oasgenserver.NewOptBool(v.AutoTieringEnabled),
		HotTierSizeGib:     oasgenserver.NewOptInt64(int64(v.HotTierSizeGib)),
		ColdTierSizeGib:    oasgenserver.NewOptInt64(int64(v.ColdTierSizeGib)),
		ClonesSharedBytes:  oasgenserver.NewOptInt64(int64(v.ClonesSharedBytes)),
		CreatedAt:          v.CreatedAt.UTC(),
		UpdatedAt:          v.UpdatedAt.UTC(),
		DeletedAt:          deletedAt(v.DeletedAt),
	}
	// Volumes have no zone column: like Google Proxy, the zone comes from the
	// pool, and the secondary zone only for regional HA pools.
	if v.Pool != nil && v.Pool.PoolAttributes != nil {
		pa := v.Pool.PoolAttributes
		dto.Zone = optStr(pa.PrimaryZone)
		if pa.IsRegionalHA {
			dto.SecondaryZone = optStr(pa.SecondaryZone)
		}
	}
	if a := v.VolumeAttributes; a != nil {
		dto.CreationToken = optStr(a.CreationToken)
		dto.Protocols = a.Protocols
		dto.SecurityStyle = optStr(a.SecurityStyle)
		dto.IsDataProtection = oasgenserver.NewOptBool(a.IsDataProtection)
		dto.IsRegionalHa = oasgenserver.NewOptBool(a.IsRegionalHA)
		dto.SnapReserve = oasgenserver.NewOptInt64(a.SnapReserve)
		dto.SnapshotDirectory = oasgenserver.NewOptBool(a.SnapshotDirectory)
		dto.KerberosEnabled = oasgenserver.NewOptBool(a.KerberosEnabled)
		dto.LdapEnabled = oasgenserver.NewOptBool(a.LdapEnabled)
		dto.RestrictedActions = a.RestrictedActions
		dto.ExternalUuid = optStr(a.ExternalUUID)
	}
	// DataProtection.KmsGrant is authorization metadata and is never mapped.
	if dp := v.DataProtection; dp != nil {
		dto.BackupVaultUuid = optStr(dp.BackupVaultID)
		dto.BackupPolicyUuid = optStr(dp.BackupPolicyID)
		if dp.ScheduledBackupEnabled != nil {
			dto.ScheduledBackupEnabled = oasgenserver.NewOptBool(*dp.ScheduledBackupEnabled)
		}
	}
	return Resource{
		ID:             v.ID,
		OwnerAccountID: ownerID(v.AccountID),
		Value:          oasgenserver.NewOpsVolumeV1OpsResourceV1(dto),
	}
}

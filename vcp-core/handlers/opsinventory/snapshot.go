package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var snapshotStates = stateSet(lifecycleStates, resourceEventStates)

type snapshotReader struct {
	store readstore.ReadStore
}

// NewSnapshotReader returns the snapshot reader.
func NewSnapshotReader(store readstore.ReadStore) Reader {
	return &snapshotReader{store: store}
}

func (r *snapshotReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeSnapshot,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		NameParents:   []ParentType{oasgenserver.OpsParentTypeV1Volume},
		ListParents: map[ParentType]ParentSpec{
			// Resolved Unscoped: snapshots are soft-deleted with their volume,
			// and history must still be reachable through the deleted volume.
			oasgenserver.OpsParentTypeV1Volume: {Column: readstore.ColVolumeID, Owner: readstore.OwnerVolume, ByID: true, CheckOwner: true},
		},
		NameFilter:  true,
		StateColumn: readstore.ColState,
		States:      snapshotStates,
		TimeFields:  baseTimeFields,
	}
}

func (r *snapshotReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		s   *datamodel.Snapshot
		err error
	)
	if req.UUID != "" {
		s, err = r.store.GetSnapshotByUUID(ctx, req.UUID)
	} else {
		s, err = r.store.GetSnapshotByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return snapshotResource(s), nil
}

func (r *snapshotReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListSnapshots(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, snapshotResource(&rows[i]))
	}
	return out, nil
}

func snapshotResource(s *datamodel.Snapshot) Resource {
	details, withheld := stateDetails(s.StateDetails)
	dto := oasgenserver.OpsSnapshotV1{
		ResourceType:         string(ResourceTypeSnapshot),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 s.UUID,
		Name:                 optStr(s.Name),
		Description:          optStr(s.Description),
		State:                optStr(s.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		ProjectNumber:        accountName(s.Account),
		VolumeUuid:           refUUID(s.Volume, func(v *datamodel.Volume) string { return v.UUID }),
		VolumeName:           refUUID(s.Volume, func(v *datamodel.Volume) string { return v.Name }),
		Type:                 optStr(s.Type),
		IsAppConsistent:      oasgenserver.NewOptBool(s.IsAppConsistent),
		ExternalUuid:         optStr(s.ExternalUUID),
		CreatedAt:            s.CreatedAt.UTC(),
		UpdatedAt:            s.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(s.DeletedAt),
	}
	if a := s.SnapshotAttributes; a != nil {
		dto.SizeInBytes = oasgenserver.NewOptInt64(a.SizeInBytes)
		dto.LogicalSizeUsedInBytes = oasgenserver.NewOptInt64(a.LogicalSizeUsedInBytes)
	}
	return Resource{
		ID:             s.ID,
		OwnerAccountID: ownerID(s.AccountID),
		Value:          oasgenserver.NewOpsSnapshotV1OpsResourceV1(dto),
	}
}

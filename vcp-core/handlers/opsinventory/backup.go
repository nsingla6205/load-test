package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var backupStates = stateSet(lifecycleStates, resourceEventStates)

type backupReader struct {
	store readstore.ReadStore
}

// NewBackupReader returns the backup reader. Backups have no account column:
// ownership comes from the backup vault. A GCBDR vault can hold backups whose
// source volume is in another project, so the volume parent is only a uuid match.
func NewBackupReader(store readstore.ReadStore) Reader {
	return &backupReader{store: store}
}

func (r *backupReader) Spec() Spec {
	return Spec{
		Type:        ResourceTypeBackup,
		GetByName:   true,
		NameParents: []ParentType{oasgenserver.OpsParentTypeV1BackupVault},
		ListParents: map[ParentType]ParentSpec{
			oasgenserver.OpsParentTypeV1BackupVault: {Column: readstore.ColBackupVaultID, Owner: readstore.OwnerBackupVault, ByID: true, CheckOwner: true},
			oasgenserver.OpsParentTypeV1Volume:      {Column: readstore.ColVolumeUUID},
		},
		RequiredListParents: []ParentType{oasgenserver.OpsParentTypeV1BackupVault},
		NameFilter:          true,
		StateColumn:         readstore.ColState,
		States:              backupStates,
		TimeFields:          baseTimeFields,
	}
}

func (r *backupReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		b   *datamodel.Backup
		err error
	)
	if req.UUID != "" {
		b, err = r.store.GetBackupByUUID(ctx, req.UUID)
	} else {
		b, err = r.store.GetBackupByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return backupResource(b), nil
}

func (r *backupReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListBackups(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, backupResource(&rows[i]))
	}
	return out, nil
}

// backupResource maps the vault by uuid/name only; the vault's bucket and
// HMAC details are never reached from a backup.
func backupResource(b *datamodel.Backup) Resource {
	details, withheld := stateDetails(b.StateDetails)
	dto := oasgenserver.OpsBackupV1{
		ResourceType:            string(ResourceTypeBackup),
		Source:                  oasgenserver.OpsSourceV1VcpDb,
		UUID:                    b.UUID,
		Name:                    optStr(b.Name),
		Description:             optStr(b.Description),
		State:                   optStr(b.State),
		StateDetails:            details,
		StateDetailsWithheld:    withheld,
		VolumeUuid:              optStr(b.VolumeUUID),
		Type:                    optStr(b.Type),
		ScheduleTag:             optStrPtr(b.ScheduleTag),
		SizeInBytes:             oasgenserver.NewOptInt64(b.SizeInBytes),
		LatestLogicalBackupSize: oasgenserver.NewOptInt64(b.LatestLogicalBackupSize),
		ExternalUuid:            optStr(b.ExternalUUID),
		CreatedAt:               b.CreatedAt.UTC(),
		UpdatedAt:               b.UpdatedAt.UTC(),
		DeletedAt:               deletedAt(b.DeletedAt),
	}
	var owner *int64
	if v := b.BackupVault; v != nil {
		dto.BackupVaultUuid = optStr(v.UUID)
		dto.BackupVaultName = optStr(v.Name)
		dto.ProjectNumber = accountName(v.Account)
		owner = ownerID(v.AccountID)
	}
	if a := b.Attributes; a != nil {
		dto.VolumeName = optStr(a.VolumeName)
		dto.SourceVolumeZone = optStr(a.SourceVolumeZone)
		dto.BackupPolicyName = optStr(a.BackupPolicyName)
		dto.SnapshotName = optStr(a.SnapshotName)
		dto.CompletionTime = optStr(a.CompletionTime)
	}
	return Resource{
		ID:             b.ID,
		OwnerAccountID: owner,
		Value:          oasgenserver.NewOpsBackupV1OpsResourceV1(dto),
	}
}

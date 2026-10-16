package opsinventory

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

// Backup vaults store their state in life_cycle_state.
var backupVaultStates = stateSet(lifecycleStates, resourceEventStates)

type backupVaultReader struct {
	store readstore.ReadStore
}

// NewBackupVaultReader returns the backup vault reader.
func NewBackupVaultReader(store readstore.ReadStore) Reader {
	return &backupVaultReader{store: store}
}

func (r *backupVaultReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeBackupVault,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		NameFilter:    true,
		StateColumn:   readstore.ColLifeCycleState,
		States:        backupVaultStates,
		TimeFields:    baseTimeFields,
	}
}

func (r *backupVaultReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		v   *datamodel.BackupVault
		err error
	)
	if req.UUID != "" {
		v, err = r.store.GetBackupVaultByUUID(ctx, req.UUID)
	} else {
		v, err = r.store.GetBackupVaultByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	return backupVaultResource(v), nil
}

func (r *backupVaultReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListBackupVaults(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, backupVaultResource(&rows[i]))
	}
	return out, nil
}

// backupVaultResource maps bucket names only: HMAC key ids, HMAC secret
// locators and bucket service accounts are never returned.
func backupVaultResource(v *datamodel.BackupVault) Resource {
	details, withheld := stateDetails(v.LifeCycleStateDetails)
	dto := oasgenserver.OpsBackupVaultV1{
		ResourceType:               string(ResourceTypeBackupVault),
		Source:                     oasgenserver.OpsSourceV1VcpDb,
		UUID:                       v.UUID,
		Name:                       optStr(v.Name),
		Description:                optStrPtr(v.Description),
		State:                      optStr(v.LifeCycleState),
		StateDetails:               details,
		StateDetailsWithheld:       withheld,
		ProjectNumber:              accountName(v.Account),
		BackupVaultType:            optStr(v.BackupVaultType),
		ServiceType:                optStr(v.ServiceType),
		BackupRegionName:           optStrPtr(v.BackupRegionName),
		SourceRegionName:           optStrPtr(v.SourceRegionName),
		CrossRegionBackupVaultName: optStrPtr(v.CrossRegionBackupVaultName),
		ExternalUuid:               optStrPtr(v.ExternalUUID),
		CreatedAt:                  v.CreatedAt.UTC(),
		UpdatedAt:                  v.UpdatedAt.UTC(),
		DeletedAt:                  deletedAt(v.DeletedAt),
	}
	if c := v.CmekAttributes; c != nil {
		dto.KmsConfigResourcePath = optStrPtr(c.KmsConfigResourcePath)
		dto.EncryptionState = optStrPtr(c.EncryptionState)
		dto.BackupsPrimaryKeyVersion = optStrPtr(c.BackupsPrimaryKeyVersion)
	}
	for _, bucket := range v.BucketDetails {
		if bucket != nil && bucket.BucketName != "" {
			dto.BucketNames = append(dto.BucketNames, bucket.BucketName)
		}
	}
	if im := v.ImmutableAttributes; im != nil {
		if im.BackupMinimumEnforcedRetentionDuration != nil {
			dto.BackupMinimumEnforcedRetentionDuration = oasgenserver.NewOptInt64(*im.BackupMinimumEnforcedRetentionDuration)
		}
		dto.IsDailyBackupImmutable = oasgenserver.NewOptBool(im.IsDailyBackupImmutable)
		dto.IsWeeklyBackupImmutable = oasgenserver.NewOptBool(im.IsWeeklyBackupImmutable)
		dto.IsMonthlyBackupImmutable = oasgenserver.NewOptBool(im.IsMonthlyBackupImmutable)
		dto.IsAdhocBackupImmutable = oasgenserver.NewOptBool(im.IsAdhocBackupImmutable)
	}
	return Resource{
		ID:             v.ID,
		OwnerAccountID: ownerID(v.AccountID),
		Value:          oasgenserver.NewOpsBackupVaultV1OpsResourceV1(dto),
	}
}

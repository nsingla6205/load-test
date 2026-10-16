package opsinventory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var backupRow = datamodel.Backup{
	BaseModel: datamodel.BaseModel{ID: 51, UUID: "bk-1"},
	Name:      "b1", State: "READY", VolumeUUID: "vol-other", Type: "SCHEDULED", ScheduleTag: strPtr("daily"), SizeInBytes: 9,
	BackupVault: &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "vault-1"}, Name: "bv1", AccountID: 1, Account: &datamodel.Account{Name: "1111"},
		BucketDetails: datamodel.BucketDetailsArray{{HmacSecretID: sentinel, HmacAccessKeyID: sentinel}},
	},
	Attributes: &datamodel.BackupAttributes{VolumeName: "vo", SourceVolumeZone: "us-east4-a", BackupPolicyName: "bp1", SnapshotName: "snap", CompletionTime: "2026-10-01T00:00:00Z", ServiceAccountName: sentinel},
}

func TestNewBackupReader_Spec(t *testing.T) {
	spec := NewBackupReader(nil).Spec()
	assert.Equal(t, ResourceTypeBackup, spec.Type)
	assert.Empty(t, spec.AccountColumn, "backups have no account column")
	assert.Equal(t, []ParentType{oasgenserver.OpsParentTypeV1BackupVault}, spec.NameParents)
	assert.Equal(t, []ParentType{oasgenserver.OpsParentTypeV1BackupVault}, spec.RequiredListParents)
	assert.Equal(t, ParentSpec{Column: readstore.ColBackupVaultID, Owner: readstore.OwnerBackupVault, ByID: true, CheckOwner: true}, spec.ListParents[oasgenserver.OpsParentTypeV1BackupVault])
	assert.Equal(t, ParentSpec{Column: readstore.ColVolumeUUID}, spec.ListParents[oasgenserver.OpsParentTypeV1Volume], "source volume may be in another project")
}

func TestBackupReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUIDOwnerIsTheVaultAccount", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupByUUID(mock.Anything, "bk-1").Return(&backupRow, nil)
		res, err := NewBackupReader(store).Get(ctx, GetRequest{UUID: "bk-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(51), res.ID)
		require.NotNil(t, res.OwnerAccountID)
		assert.Equal(t, int64(1), *res.OwnerAccountID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColBackupVaultID, Value: int64(3)}}
		store.EXPECT().GetBackupByName(mock.Anything, readstore.NameQuery{Name: "b1", Eq: scope}).Return(&backupRow, nil)
		_, err := NewBackupReader(store).Get(ctx, GetRequest{Name: "b1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewBackupReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestBackupReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackups(mock.Anything, opts).Return([]datamodel.Backup{backupRow}, nil)
		rows, err := NewBackupReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackups(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewBackupReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestBackupResource(t *testing.T) {
	t.Run("WhenVaultIsLoaded", func(t *testing.T) {
		v := backupResource(&backupRow).Value
		got := encoded(t, v)
		assert.Equal(t, "backup", got["resourceType"])
		assert.Equal(t, "1111", got["projectNumber"])
		assert.Equal(t, "vault-1", got["backupVaultUuid"])
		assert.Equal(t, "bv1", got["backupVaultName"])
		assert.Equal(t, "vol-other", got["volumeUuid"])
		assert.Equal(t, "daily", got["scheduleTag"])
		assert.Equal(t, "bp1", got["backupPolicyName"])
		assert.Equal(t, "us-east4-a", got["sourceVolumeZone"])
		assert.NotContains(t, got, "serviceAccountName")
		assertNoSecrets(t, v)
	})

	t.Run("WhenVaultIsMissing", func(t *testing.T) {
		res := backupResource(&datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "b"}})
		assert.Nil(t, res.OwnerAccountID)
		assert.NotContains(t, encoded(t, res.Value), "projectNumber")
	})
}

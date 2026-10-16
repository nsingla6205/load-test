package opsinventory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
)

var retention = int64(30)

var backupVaultRow = datamodel.BackupVault{
	BaseModel: datamodel.BaseModel{ID: 61, UUID: "vault-1"},
	Name:      "bv1", AccountID: 1, LifeCycleState: "READY", LifeCycleStateDetails: datamodel.LifeCycleStateReadyDetails,
	BackupVaultType: "IN_REGION", ServiceType: "GCNV", Description: strPtr("d"), BackupRegionName: strPtr("us-west1"),
	Account:        &datamodel.Account{Name: "1111"},
	CmekAttributes: &datamodel.CmekAttributes{KmsConfigResourcePath: strPtr("projects/p/locations/l/kmsConfigs/k"), EncryptionState: strPtr(datamodel.EncryptionStateCompleted)},
	BucketDetails: datamodel.BucketDetailsArray{
		{BucketName: "bucket-1", HmacAccessKeyID: sentinel, HmacSecretID: sentinel, ServiceAccountName: sentinel},
		nil,
		{BucketName: ""},
	},
	ImmutableAttributes: &datamodel.ImmutableAttributes{BackupMinimumEnforcedRetentionDuration: &retention, IsDailyBackupImmutable: true},
}

func TestNewBackupVaultReader_Spec(t *testing.T) {
	spec := NewBackupVaultReader(nil).Spec()
	assert.Equal(t, ResourceTypeBackupVault, spec.Type)
	assert.Equal(t, readstore.ColLifeCycleState, spec.StateColumn)
	assert.True(t, spec.GetByName)
	assert.Empty(t, spec.ListParents)
}

func TestBackupVaultReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupVaultByUUID(mock.Anything, "vault-1").Return(&backupVaultRow, nil)
		res, err := NewBackupVaultReader(store).Get(ctx, GetRequest{UUID: "vault-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(61), res.ID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}}
		store.EXPECT().GetBackupVaultByName(mock.Anything, readstore.NameQuery{Name: "bv1", Eq: scope}).Return(&backupVaultRow, nil)
		_, err := NewBackupVaultReader(store).Get(ctx, GetRequest{Name: "bv1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupVaultByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewBackupVaultReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestBackupVaultReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackupVaults(mock.Anything, opts).Return([]datamodel.BackupVault{backupVaultRow}, nil)
		rows, err := NewBackupVaultReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackupVaults(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewBackupVaultReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestBackupVaultResource(t *testing.T) {
	v := backupVaultResource(&backupVaultRow).Value
	got := encoded(t, v)
	assert.Equal(t, "backupVault", got["resourceType"])
	assert.Equal(t, "READY", got["state"], "state comes from life_cycle_state")
	assert.Equal(t, datamodel.LifeCycleStateReadyDetails, got["stateDetails"])
	assert.Equal(t, []any{"bucket-1"}, got["bucketNames"], "only bucket names, no empty or nil buckets")
	assert.Equal(t, "projects/p/locations/l/kmsConfigs/k", got["kmsConfigResourcePath"])
	assert.Equal(t, float64(30), got["backupMinimumEnforcedRetentionDuration"])
	assert.Equal(t, true, got["isDailyBackupImmutable"])
	assert.Equal(t, "us-west1", got["backupRegionName"])
	assert.NotContains(t, got, "bucketDetails")
	assertNoSecrets(t, v)
}

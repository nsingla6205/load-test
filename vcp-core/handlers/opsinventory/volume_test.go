package opsinventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var volumeRow = datamodel.Volume{
	BaseModel: datamodel.BaseModel{ID: 11, UUID: "vol-1", CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
	Name:      "v1", State: "READY", StateDetails: datamodel.LifeCycleStateAvailableDetails, Health: "OK",
	AccountID: 1, SizeInBytes: 100, UsedBytes: 40, Throughput: 64, MountPath: "/v1",
	Account: &datamodel.Account{Name: "1111"},
	Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}, Name: "p1", PoolCredentials: &datamodel.PoolCredentials{Password: sentinel},
		PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-east4-a", SecondaryZone: "us-east4-b", IsRegionalHA: true}},
	Svm:                    &datamodel.Svm{BaseModel: datamodel.BaseModel{UUID: "svm-1"}, Name: "svm1"},
	VolumePerformanceGroup: &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{UUID: "vpg-1"}},
	VolumeAttributes:       &datamodel.VolumeAttributes{CreationToken: "tok", Protocols: []string{"NFSV3"}, IsDataProtection: true, SnapReserve: 5, ExternalUUID: "ext-1"},
	DataProtection:         &datamodel.DataProtection{BackupVaultID: "vault-1", BackupPolicyID: "bp-1", ScheduledBackupEnabled: boolPtr(true), KmsGrant: strPtr(sentinel)},
}

func boolPtr(b bool) *bool { return &b }

func TestNewVolumeReader_Spec(t *testing.T) {
	spec := NewVolumeReader(nil).Spec()
	assert.Equal(t, ResourceTypeVolume, spec.Type)
	assert.Equal(t, readstore.ColAccountID, spec.AccountColumn)
	assert.True(t, spec.GetByName)
	assert.Equal(t, []ParentType{oasgenserver.OpsParentTypeV1Pool}, spec.NameParents)
	assert.Equal(t, ParentSpec{Column: readstore.ColPoolID, Owner: readstore.OwnerPool, ByID: true, CheckOwner: true}, spec.ListParents[oasgenserver.OpsParentTypeV1Pool])
	assert.Equal(t, readstore.ColVolumeBackupVaultUUID, spec.ListParents[oasgenserver.OpsParentTypeV1BackupVault].Column)
	assert.False(t, spec.ListParents[oasgenserver.OpsParentTypeV1BackupVault].ByID, "JSON field holds the uuid")
	assert.Len(t, spec.ListParents, 5)
	assert.Contains(t, spec.States, datamodel.VolumeStateOffline)
	assert.Equal(t, readstore.ColState, spec.StateColumn)
}

func TestVolumeReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetVolumeByUUID(mock.Anything, "vol-1").Return(&volumeRow, nil)
		res, err := NewVolumeReader(store).Get(ctx, GetRequest{UUID: "vol-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(11), res.ID)
		assert.Equal(t, int64(1), *res.OwnerAccountID)
	})

	t.Run("WhenByNameItPassesTheScope", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}, {Column: readstore.ColPoolID, Value: int64(2)}}
		store.EXPECT().GetVolumeByName(mock.Anything, readstore.NameQuery{Name: "v1", Eq: scope}).Return(&volumeRow, nil)
		_, err := NewVolumeReader(store).Get(ctx, GetRequest{Name: "v1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		boom := errors.New("db down")
		store.EXPECT().GetVolumeByUUID(mock.Anything, "x").Return(nil, boom)
		_, err := NewVolumeReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, boom)
	})
}

func TestVolumeReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 3}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListVolumes(mock.Anything, opts).Return([]datamodel.Volume{volumeRow, {BaseModel: datamodel.BaseModel{ID: 12, UUID: "vol-2"}}}, nil)
		rows, err := NewVolumeReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		require.Len(t, rows, 2)
		assert.Equal(t, int64(12), rows[1].ID)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListVolumes(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewVolumeReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestVolumeResource(t *testing.T) {
	t.Run("WhenAllFieldsAreSet", func(t *testing.T) {
		v := volumeResource(&volumeRow).Value
		require.True(t, v.IsOpsVolumeV1())
		got := encoded(t, v)
		assert.Equal(t, "volume", got["resourceType"])
		assert.Equal(t, "vcp_db", got["source"])
		assert.Equal(t, "1111", got["projectNumber"])
		assert.Equal(t, "pool-1", got["poolUuid"])
		assert.Equal(t, "p1", got["poolName"])
		assert.Equal(t, "svm-1", got["svmUuid"])
		assert.Equal(t, "vpg-1", got["volumePerformanceGroupUuid"])
		assert.Equal(t, "vault-1", got["backupVaultUuid"])
		assert.Equal(t, "bp-1", got["backupPolicyUuid"])
		assert.Equal(t, true, got["scheduledBackupEnabled"])
		assert.Equal(t, "tok", got["creationToken"])
		assert.Equal(t, "us-east4-a", got["zone"])
		assert.Equal(t, "us-east4-b", got["secondaryZone"])
		assert.Equal(t, []any{"NFSV3"}, got["protocols"])
		assert.Equal(t, float64(40), got["usedBytes"])
		assert.Equal(t, datamodel.LifeCycleStateAvailableDetails, got["stateDetails"])
		assert.Nil(t, got["deletedAt"])
		assert.NotContains(t, got, "kmsGrant")
		assertNoSecrets(t, v)
	})

	t.Run("WhenOptionalPartsAreMissing", func(t *testing.T) {
		got := encoded(t, volumeResource(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "u"}}).Value)
		assert.NotContains(t, got, "poolUuid")
		assert.NotContains(t, got, "creationToken")
		assert.NotContains(t, got, "scheduledBackupEnabled")
		assert.Contains(t, got, "deletedAt")
	})
}

func TestVolumeZone(t *testing.T) {
	t.Run("WhenPoolIsZonal", func(t *testing.T) {
		v := datamodel.Volume{Pool: &datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-east4-a", SecondaryZone: "us-east4-a", IsTrueZonal: true}}}
		got := encoded(t, volumeResource(&v).Value)
		assert.Equal(t, "us-east4-a", got["zone"])
		assert.NotContains(t, got, "secondaryZone", "only regional HA pools have a secondary zone, like Google Proxy")
	})

	t.Run("WhenPoolHasNoAttributes", func(t *testing.T) {
		got := encoded(t, volumeResource(&datamodel.Volume{Pool: &datamodel.Pool{}}).Value)
		assert.NotContains(t, got, "zone")
	})
}

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

var backupPolicyRow = datamodel.BackupPolicy{
	BaseModel: datamodel.BaseModel{ID: 71, UUID: "bp-1"},
	Name:      "bp1", AccountID: 1, LifeCycleState: "ERROR", LifeCycleStateDetails: sentinel,
	DailyBackupsToKeep: 2, WeeklyBackupsToKeep: 3, MonthlyBackupsToKeep: 4, PolicyEnabled: true,
	Account: &datamodel.Account{Name: "1111"},
}

func TestNewBackupPolicyReader_Spec(t *testing.T) {
	spec := NewBackupPolicyReader(nil).Spec()
	assert.Equal(t, ResourceTypeBackupPolicy, spec.Type)
	assert.Equal(t, readstore.ColLifeCycleState, spec.StateColumn)
	assert.True(t, spec.GetByName)
	assert.Empty(t, spec.ListParents)
}

func TestBackupPolicyReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupPolicyByUUID(mock.Anything, "bp-1").Return(&backupPolicyRow, nil)
		res, err := NewBackupPolicyReader(store).Get(ctx, GetRequest{UUID: "bp-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(71), res.ID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}}
		store.EXPECT().GetBackupPolicyByName(mock.Anything, readstore.NameQuery{Name: "bp1", Eq: scope}).Return(&backupPolicyRow, nil)
		_, err := NewBackupPolicyReader(store).Get(ctx, GetRequest{Name: "bp1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetBackupPolicyByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewBackupPolicyReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestBackupPolicyReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackupPolicies(mock.Anything, opts).Return([]datamodel.BackupPolicy{backupPolicyRow}, nil)
		rows, err := NewBackupPolicyReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListBackupPolicies(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewBackupPolicyReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestBackupPolicyResource(t *testing.T) {
	v := backupPolicyResource(&backupPolicyRow).Value
	got := encoded(t, v)
	assert.Equal(t, "backupPolicy", got["resourceType"])
	assert.Equal(t, "ERROR", got["state"])
	assert.Equal(t, true, got["stateDetailsWithheld"])
	assert.Equal(t, float64(2), got["dailyBackupsToKeep"])
	assert.Equal(t, float64(3), got["weeklyBackupsToKeep"])
	assert.Equal(t, float64(4), got["monthlyBackupsToKeep"])
	assert.Equal(t, true, got["policyEnabled"])
	assert.Equal(t, "1111", got["projectNumber"])
	assertNoSecrets(t, v)
}

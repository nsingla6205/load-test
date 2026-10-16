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

var snapshotRow = datamodel.Snapshot{
	BaseModel: datamodel.BaseModel{ID: 31, UUID: "snap-1"},
	Name:      "s1", State: "READY", StateDetails: sentinel, AccountID: 1, Type: "MANUAL", IsAppConsistent: true, ExternalUUID: "ext-s",
	Account:            &datamodel.Account{Name: "1111"},
	Volume:             &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-1"}, Name: "v1"},
	SnapshotAttributes: &datamodel.SnapshotAttributes{SizeInBytes: 7, LogicalSizeUsedInBytes: 3},
}

func TestNewSnapshotReader_Spec(t *testing.T) {
	spec := NewSnapshotReader(nil).Spec()
	assert.Equal(t, ResourceTypeSnapshot, spec.Type)
	assert.Equal(t, []ParentType{oasgenserver.OpsParentTypeV1Volume}, spec.NameParents)
	assert.Equal(t, ParentSpec{Column: readstore.ColVolumeID, Owner: readstore.OwnerVolume, ByID: true, CheckOwner: true}, spec.ListParents[oasgenserver.OpsParentTypeV1Volume])
	assert.Len(t, spec.ListParents, 1)
	assert.True(t, spec.NameFilter)
}

func TestSnapshotReaderGet(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenByUUID", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetSnapshotByUUID(mock.Anything, "snap-1").Return(&snapshotRow, nil)
		res, err := NewSnapshotReader(store).Get(ctx, GetRequest{UUID: "snap-1"})
		require.NoError(t, err)
		assert.Equal(t, int64(31), res.ID)
	})

	t.Run("WhenByName", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColVolumeID, Value: int64(4)}}
		store.EXPECT().GetSnapshotByName(mock.Anything, readstore.NameQuery{Name: "s1", Eq: scope}).Return(&snapshotRow, nil)
		_, err := NewSnapshotReader(store).Get(ctx, GetRequest{Name: "s1", NameScope: scope})
		require.NoError(t, err)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetSnapshotByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewSnapshotReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestSnapshotReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 2}

	t.Run("WhenRowsAreReturned", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListSnapshots(mock.Anything, opts).Return([]datamodel.Snapshot{snapshotRow}, nil)
		rows, err := NewSnapshotReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListSnapshots(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewSnapshotReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestSnapshotResource(t *testing.T) {
	v := snapshotResource(&snapshotRow).Value
	got := encoded(t, v)
	assert.Equal(t, "snapshot", got["resourceType"])
	assert.Equal(t, "vol-1", got["volumeUuid"])
	assert.Equal(t, "v1", got["volumeName"])
	assert.Equal(t, float64(7), got["sizeInBytes"])
	assert.Equal(t, float64(3), got["logicalSizeUsedInBytes"])
	assert.Equal(t, true, got["isAppConsistent"])
	assert.Equal(t, true, got["stateDetailsWithheld"])
	assertNoSecrets(t, v)
}

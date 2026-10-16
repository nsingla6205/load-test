package readstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestLookupOwner(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()

	owner, err := s.LookupOwner(ctx, OwnerPool, "pool-2")
	require.NoError(t, err, "deleted parents resolve")
	assert.Equal(t, f.deletedPool.ID, owner.ID)
	require.NotNil(t, owner.AccountID)
	assert.Equal(t, f.account.ID, *owner.AccountID)

	vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{UUID: "vpg-1"}, Name: "g", PoolID: f.pool.ID, AllocationType: datamodel.AllocationTypeShared}
	require.NoError(t, db.Create(vpg).Error)
	owner, err = s.LookupOwner(ctx, OwnerVolumePerformanceGroup, "vpg-1")
	require.NoError(t, err)
	assert.Equal(t, vpg.ID, owner.ID)
	require.NotNil(t, owner.AccountID, "performance group owner comes from its pool")
	assert.Equal(t, f.account.ID, *owner.AccountID)

	host := &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: "host-1"}, Name: "h"}
	require.NoError(t, db.Create(host).Error)
	owner, err = s.LookupOwner(ctx, OwnerHost, "host-1")
	require.NoError(t, err)
	assert.Nil(t, owner.AccountID)

	_, err = s.LookupOwner(ctx, OwnerVolume, "missing")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.LookupOwner(ctx, OwnerKind("disk"), "x")
	assert.ErrorIs(t, err, ErrInvalidQuery)
}

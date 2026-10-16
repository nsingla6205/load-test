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
	"gorm.io/gorm"
)

func TestStateSet(t *testing.T) {
	set := stateSet([]string{"A", "B"}, []string{"B", "C"})
	assert.Len(t, set, 3)
	assert.Contains(t, set, "C")
	assert.Empty(t, stateSet())
}

func TestLifecycleStates(t *testing.T) {
	set := stateSet(lifecycleStates)
	for _, s := range []string{datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateError,
		datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateDiagnosing} {
		assert.Contains(t, set, s)
	}
	assert.Equal(t, []string{"OFF", "ON", "DELETE"}, resourceEventStates)
}

func TestStateDetails(t *testing.T) {
	t.Run("WhenEmpty", func(t *testing.T) {
		v, w := stateDetails("")
		assert.False(t, v.IsSet())
		assert.False(t, w.Or(true))
	})

	t.Run("WhenKnownPhrase", func(t *testing.T) {
		v, w := stateDetails(datamodel.LifeCycleStateReadyDetails)
		assert.Equal(t, datamodel.LifeCycleStateReadyDetails, v.Or(""))
		assert.False(t, w.Or(true))
	})

	t.Run("WhenFreeText", func(t *testing.T) {
		v, w := stateDetails("ONTAP error: volume /vol/secret on 10.0.0.1 failed")
		assert.False(t, v.IsSet(), "free text is never returned")
		assert.True(t, w.Or(false))
	})
}

func TestCatalogMessage(t *testing.T) {
	t.Run("WhenTrackingIDIsZero", func(t *testing.T) {
		assert.False(t, catalogMessage(0).IsSet())
	})

	t.Run("WhenTrackingIDIsUnknown", func(t *testing.T) {
		assert.False(t, catalogMessage(987654321).IsSet())
	})

	t.Run("WhenTrackingIDIsInTheCatalog", func(t *testing.T) {
		assert.Equal(t, "Resource not found", catalogMessage(1003).Or(""))
	})

	t.Run("WhenMessageHasFormatVerbs", func(t *testing.T) {
		assert.Equal(t, "volume … has … bytes", formatVerb.ReplaceAllString("volume %s has %-5d bytes", "…"))
	})
}

func TestOptionalHelpers(t *testing.T) {
	s := "x"
	empty := ""
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("X", 3600))

	assert.False(t, optStr("").IsSet())
	assert.Equal(t, "x", optStr("x").Or(""))
	assert.False(t, optStrPtr(nil).IsSet())
	assert.False(t, optStrPtr(&empty).IsSet())
	assert.Equal(t, "x", optStrPtr(&s).Or(""))

	assert.False(t, optTime(time.Time{}).IsSet())
	assert.Equal(t, time.UTC, optTime(now).Value.Location())
	assert.False(t, optTimePtr(nil).IsSet())
	assert.True(t, optTimePtr(&now).IsSet())

	assert.Equal(t, int64(5), *ownerID(5))
}

func TestDeletedAt(t *testing.T) {
	t.Run("WhenNil", func(t *testing.T) {
		assert.True(t, deletedAt(nil).Null)
	})

	t.Run("WhenNotValid", func(t *testing.T) {
		assert.True(t, deletedAt(&gorm.DeletedAt{}).Null)
	})

	t.Run("WhenSet", func(t *testing.T) {
		ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("X", 3600))
		d := deletedAt(&gorm.DeletedAt{Time: ts, Valid: true})
		assert.False(t, d.Null)
		assert.True(t, d.Value.Equal(ts))
		assert.Equal(t, time.UTC, d.Value.Location())
	})
}

func TestAccountNameAndRefUUID(t *testing.T) {
	assert.False(t, accountName(nil).IsSet())
	assert.Equal(t, "123", accountName(&datamodel.Account{Name: "123"}).Or(""))

	get := func(p *datamodel.Pool) string { return p.UUID }
	assert.False(t, refUUID[datamodel.Pool](nil, get).IsSet())
	assert.Equal(t, "u", refUUID(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}}, get).Or(""))
}

func TestLoadAccounts(t *testing.T) {
	ctx := context.Background()
	known := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}

	t.Run("WhenEveryIDIsTheKnownProject", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		accounts, err := loadAccounts(ctx, store, known, []int64{1, 1})
		require.NoError(t, err)
		assert.Same(t, known, accounts[1])
	})

	t.Run("WhenOtherIDsAreLoadedInOneQuery", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{2, 3}).
			Return(map[int64]*datamodel.Account{2: {Name: "2222"}}, nil).Once()
		accounts, err := loadAccounts(ctx, store, known, []int64{1, 2, 3, 2, 1, 3})
		require.NoError(t, err)
		assert.Equal(t, "1111", accounts[1].Name)
		assert.Equal(t, "2222", accounts[2].Name)
		assert.Nil(t, accounts[3], "missing accounts have no project")
	})

	t.Run("WhenNoIDs", func(t *testing.T) {
		accounts, err := loadAccounts(ctx, readstore.NewMockReadStore(t), nil, nil)
		require.NoError(t, err)
		assert.Empty(t, accounts)
	})

	t.Run("WhenQueryFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		boom := errors.New("db down")
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{2}).Return(nil, boom)
		_, err := loadAccounts(ctx, store, nil, []int64{2})
		assert.ErrorIs(t, err, boom)
	})
}

package readstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestGetByName(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()
	scope := []Eq{{ColAccountID, f.account.ID}, {ColPoolID, f.pool.ID}}

	t.Run("WhenLiveAndDeletedRowsMatch", func(t *testing.T) {
		vol, err := s.GetVolumeByName(ctx, NameQuery{Name: "v1", Eq: scope})
		require.NoError(t, err)
		assert.Equal(t, "vol-1", vol.UUID)
	})

	t.Run("WhenOnlyDeletedRowsMatch", func(t *testing.T) {
		require.NoError(t, db.Model(&datamodel.Volume{}).Where("uuid = ?", "vol-1").Update("deleted_at", time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)).Error)
		vol, err := s.GetVolumeByName(ctx, NameQuery{Name: "v1", Eq: scope})
		require.NoError(t, err)
		assert.Equal(t, "vol-5", vol.UUID)
	})

	t.Run("WhenSeveralLiveRowsMatch", func(t *testing.T) {
		require.NoError(t, db.Create(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-6"}, Name: "v2", AccountID: f.account.ID, PoolID: f.pool.ID}).Error)
		_, err := s.GetVolumeByName(ctx, NameQuery{Name: "v2", Eq: scope})
		assert.ErrorIs(t, err, ErrAmbiguousName)
	})

	t.Run("WhenParentPredicateExcludesTheRow", func(t *testing.T) {
		_, err := s.GetVolumeByName(ctx, NameQuery{Name: "v3", Eq: scope})
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("WhenColumnIsUnknown", func(t *testing.T) {
		_, err := s.GetVolumeByName(ctx, NameQuery{Name: "v1", Eq: []Eq{{Column("1=1; --"), 1}}})
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})
}

func TestList(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()
	acct := Eq{ColAccountID, f.account.ID}

	t.Run("WhenIncludeDeletedIsFalse", func(t *testing.T) {
		rows, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct}, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{"vol-1", "vol-2", "vol-3"}, uuids(rows, volUUID))
	})

	t.Run("WhenIncludeDeletedIsTrue", func(t *testing.T) {
		rows, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct}, IncludeDeleted: true, Limit: 10})
		require.NoError(t, err)
		assert.Len(t, rows, 5)
	})

	t.Run("WhenPagingWithAfterID", func(t *testing.T) {
		page1, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct}, IncludeDeleted: true, Limit: 2})
		require.NoError(t, err)
		require.Len(t, page1, 2)
		page2, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct}, IncludeDeleted: true, Limit: 2, AfterID: page1[1].ID})
		require.NoError(t, err)
		assert.Equal(t, []string{"vol-3", "vol-4"}, uuids(page2, volUUID))
	})

	t.Run("WhenFilteringByState", func(t *testing.T) {
		rows, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct, {ColState, "CREATING"}}, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{"vol-2"}, uuids(rows, volUUID))
	})

	t.Run("WhenFilteringByJSONField", func(t *testing.T) {
		rows, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct, {ColVolumeBackupVaultUUID, "vault-1"}}, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{"vol-1", "vol-3"}, uuids(rows, volUUID))
	})

	t.Run("WhenFilteringByTimeRange", func(t *testing.T) {
		from := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
		rows, err := s.ListVolumes(ctx, ListOptions{Eq: []Eq{acct}, TimeRanges: []TimeRange{{ColCreatedAt, &from, &to}}, Limit: 10})
		require.NoError(t, err)
		assert.Equal(t, []string{"vol-2"}, uuids(rows, volUUID))
	})

	t.Run("WhenLimitOrColumnIsInvalid", func(t *testing.T) {
		_, err := s.ListVolumes(ctx, ListOptions{Limit: 0})
		assert.ErrorIs(t, err, ErrInvalidQuery)
		_, err = s.ListVolumes(ctx, ListOptions{Limit: MaxListLimit + 1})
		assert.ErrorIs(t, err, ErrInvalidQuery)
		_, err = s.ListVolumes(ctx, ListOptions{Limit: 1, TimeRanges: []TimeRange{{Column: ColName}}})
		assert.ErrorIs(t, err, ErrInvalidQuery)
		_, err = s.ListVolumes(ctx, ListOptions{Limit: 1, Eq: []Eq{{Column("id; drop table volumes"), 1}}})
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})
}

func TestWithRetry(t *testing.T) {
	transient := &pgconn.PgError{Code: "40001"}

	t.Run("WhenFirstCallSucceeds", func(t *testing.T) {
		calls := 0
		err := withRetry(context.Background(), func() error { calls++; return nil })
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
	})

	t.Run("WhenErrorIsTransientItRetries", func(t *testing.T) {
		calls := 0
		err := withRetry(context.Background(), func() error {
			calls++
			if calls == 1 {
				return transient
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, calls)
	})

	t.Run("WhenErrorIsNotTransientItStops", func(t *testing.T) {
		calls := 0
		boom := errors.New("syntax error")
		err := withRetry(context.Background(), func() error { calls++; return boom })
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, calls)
	})

	t.Run("WhenContextIsDoneItStops", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := withRetry(ctx, func() error { calls++; return transient })
		assert.ErrorIs(t, err, transient)
		assert.Equal(t, 1, calls)
	})
}

func TestApplyEq(t *testing.T) {
	_, db := newTestStore(t)

	t.Run("WhenColumnsAreAllowed", func(t *testing.T) {
		q, err := applyEq(db.Model(&datamodel.Volume{}), []Eq{{ColAccountID, 1}, {ColVolumeBackupVaultUUID, "v"}})
		require.NoError(t, err)
		assert.NotNil(t, q)
	})

	t.Run("WhenAColumnIsNotAllowed", func(t *testing.T) {
		_, err := applyEq(db.Model(&datamodel.Volume{}), []Eq{{ColAccountID, 1}, {Column("uuid OR 1=1"), "x"}})
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})

	t.Run("WhenATimeColumnIsUsedAsEquality", func(t *testing.T) {
		_, err := applyEq(db.Model(&datamodel.Volume{}), []Eq{{ColCreatedAt, time.Now()}})
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})
}

func TestGetByIDs(t *testing.T) {
	_, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()

	t.Run("WhenRowsAreLiveDeletedOrMissing", func(t *testing.T) {
		pools, err := getByIDs[datamodel.Pool](ctx, db, []int64{f.deletedPool.ID, f.pool.ID, 999})
		require.NoError(t, err)
		assert.Equal(t, []string{"pool-1", "pool-2"}, []string{pools[0].UUID, pools[1].UUID})
	})

	t.Run("WhenNoIDs", func(t *testing.T) {
		pools, err := getByIDs[datamodel.Pool](ctx, db, nil)
		require.NoError(t, err)
		assert.Empty(t, pools)
	})
}

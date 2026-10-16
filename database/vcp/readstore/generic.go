package readstore

import (
	"context"
	"fmt"

	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"gorm.io/gorm"
)

// withRetry retries fn on transient database errors, the same way the
// storage retry engine does, and does not start a new attempt once ctx is
// done. retry.Do sleeps with time.Sleep, so a cancelled request still waits
// out the current delay (VCP_DB_RETRY_DELAY_MS) before returning.
func withRetry(ctx context.Context, fn func() error) error {
	return retry.Do(func(int) (bool, error) {
		err := fn()
		if err == nil {
			return false, nil
		}
		if ctx.Err() != nil || !dbutils.IsTransientErr(err) {
			return false, err
		}
		return true, err
	})
}

func base[T any](ctx context.Context, db *gorm.DB, preload func(*gorm.DB) *gorm.DB) *gorm.DB {
	return preload(db.WithContext(ctx).Unscoped().Model(new(T)))
}

func applyEq(q *gorm.DB, eqs []Eq) (*gorm.DB, error) {
	for _, eq := range eqs {
		if _, ok := predicateColumns[eq.Column]; !ok {
			return nil, fmt.Errorf("%w: column %q", ErrInvalidQuery, eq.Column)
		}
		q = q.Where(fmt.Sprintf("%s = ?", eq.Column), eq.Value)
	}
	return q, nil
}

// getByIDs returns the rows with the given internal ids, live or deleted, in
// one query. Missing ids are left out.
func getByIDs[T any](ctx context.Context, db *gorm.DB, ids []int64) ([]T, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []T
	err := withRetry(ctx, func() error {
		rows = nil
		return base[T](ctx, db, noPreload).Where("id IN ?", ids).Order("id ASC").Find(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// getByUUID returns the row with the given uuid, live or deleted.
// uuid is unique within each table.
func getByUUID[T any](ctx context.Context, db *gorm.DB, preload func(*gorm.DB) *gorm.DB, uuid string) (*T, error) {
	var rows []T
	err := withRetry(ctx, func() error {
		rows = nil
		return base[T](ctx, db, preload).Where("uuid = ?", uuid).Limit(1).Find(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

// getByName returns the single live row matching the name and predicates.
// More than one live match returns ErrAmbiguousName. With no live match it
// returns the most recently deleted match (deleted_at DESC, id DESC).
func getByName[T any](ctx context.Context, db *gorm.DB, preload func(*gorm.DB) *gorm.DB, q NameQuery) (*T, error) {
	eqs := append([]Eq{{Column: ColName, Value: q.Name}}, q.Eq...)

	var live []T
	err := withRetry(ctx, func() error {
		live = nil
		tx, err := applyEq(base[T](ctx, db, preload), eqs)
		if err != nil {
			return err
		}
		return tx.Where("deleted_at IS NULL").Order("id ASC").Limit(2).Find(&live).Error
	})
	if err != nil {
		return nil, err
	}
	switch {
	case len(live) > 1:
		return nil, ErrAmbiguousName
	case len(live) == 1:
		return &live[0], nil
	}

	var deleted []T
	err = withRetry(ctx, func() error {
		deleted = nil
		tx, err := applyEq(base[T](ctx, db, preload), eqs)
		if err != nil {
			return err
		}
		return tx.Where("deleted_at IS NOT NULL").Order("deleted_at DESC").Order("id DESC").Limit(1).Find(&deleted).Error
	})
	if err != nil {
		return nil, err
	}
	if len(deleted) == 0 {
		return nil, ErrNotFound
	}
	return &deleted[0], nil
}

// list returns one keyset page ordered by id. All predicates are applied in
// the database, so paging stays correct.
func list[T any](ctx context.Context, db *gorm.DB, preload func(*gorm.DB) *gorm.DB, opts ListOptions) ([]T, error) {
	if opts.Limit < 1 || opts.Limit > MaxListLimit {
		return nil, fmt.Errorf("%w: limit %d", ErrInvalidQuery, opts.Limit)
	}
	for _, tr := range opts.TimeRanges {
		if _, ok := timeColumns[tr.Column]; !ok {
			return nil, fmt.Errorf("%w: time column %q", ErrInvalidQuery, tr.Column)
		}
	}

	var rows []T
	err := withRetry(ctx, func() error {
		rows = nil
		tx, err := applyEq(base[T](ctx, db, preload), opts.Eq)
		if err != nil {
			return err
		}
		if !opts.IncludeDeleted {
			tx = tx.Where("deleted_at IS NULL")
		}
		for _, tr := range opts.TimeRanges {
			if tr.From != nil {
				tx = tx.Where(fmt.Sprintf("%s >= ?", tr.Column), tr.From.UTC())
			}
			if tr.To != nil {
				tx = tx.Where(fmt.Sprintf("%s < ?", tr.Column), tr.To.UTC())
			}
		}
		if opts.AfterID > 0 {
			tx = tx.Where("id > ?", opts.AfterID)
		}
		return tx.Order("id ASC").Limit(opts.Limit).Find(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

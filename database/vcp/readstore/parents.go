package readstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"gorm.io/gorm"
)

type ownerRow struct {
	ID        int64
	UUID      string
	AccountID sql.NullInt64
	PoolID    int64
}

// LookupOwner resolves a parent uuid to its internal id and owning account,
// live or deleted. Volume performance groups have no account column; their
// owner is the account of their pool.
func (s *store) LookupOwner(ctx context.Context, kind OwnerKind, uuid string) (*Owner, error) {
	var (
		model any
		cols  = []string{"id", "uuid", "account_id"}
	)
	switch kind {
	case OwnerPool:
		model = &datamodel.Pool{}
	case OwnerVolume:
		model = &datamodel.Volume{}
	case OwnerSvm:
		model = &datamodel.Svm{}
	case OwnerBackupVault:
		model = &datamodel.BackupVault{}
	case OwnerBackupPolicy:
		model = &datamodel.BackupPolicy{}
	case OwnerKmsConfig:
		model = &datamodel.KmsConfig{}
	case OwnerActiveDirectory:
		model = &datamodel.ActiveDirectory{}
	case OwnerVolumePerformanceGroup:
		model = &datamodel.VolumePerformanceGroup{}
		cols = []string{"id", "uuid", "pool_id"}
	case OwnerHost:
		model = &datamodel.Host{}
		cols = []string{"id", "uuid"}
	default:
		return nil, fmt.Errorf("%w: owner kind %q", ErrInvalidQuery, kind)
	}

	row, err := lookupRow(ctx, s.db, model, cols, uuid)
	if err != nil {
		return nil, err
	}
	owner := &Owner{ID: row.ID, UUID: row.UUID}
	if row.AccountID.Valid {
		accountID := row.AccountID.Int64
		owner.AccountID = &accountID
	}

	if kind == OwnerVolumePerformanceGroup {
		poolRow, err := lookupRowByID(ctx, s.db, &datamodel.Pool{}, row.PoolID)
		if err != nil {
			return nil, err
		}
		if poolRow.AccountID.Valid {
			accountID := poolRow.AccountID.Int64
			owner.AccountID = &accountID
		}
	}
	return owner, nil
}

func lookupRow(ctx context.Context, db *gorm.DB, model any, cols []string, uuid string) (*ownerRow, error) {
	return lookup(ctx, db, model, cols, "uuid = ?", uuid)
}

func lookupRowByID(ctx context.Context, db *gorm.DB, model any, id int64) (*ownerRow, error) {
	return lookup(ctx, db, model, []string{"id", "uuid", "account_id"}, "id = ?", id)
}

func lookup(ctx context.Context, db *gorm.DB, model any, cols []string, where string, arg any) (*ownerRow, error) {
	var rows []ownerRow
	err := withRetry(ctx, func() error {
		rows = nil
		return db.WithContext(ctx).Unscoped().Model(model).Select(cols).Where(where, arg).Limit(1).Scan(&rows).Error
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

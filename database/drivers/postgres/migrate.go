// Package postgres provides database migration functionality for PostgreSQL.
// It supports a two-phase migration approach:
// 1. Pre-migrations: Run before GORM AutoMigrate (schema changes)
// 2. Post-migrations: Run after GORM AutoMigrate (data changes)
package postgres

import (
	"context"
	"crypto/md5"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

var (
	VcpDbPreRollbackSteps      = env.GetInt("VCP_DB_PRE_ROLLBACK_STEPS", 0)
	VcpDbPostRollbackSteps     = env.GetInt("VCP_DB_POST_ROLLBACK_STEPS", 0)
	MetricsDbPreRollbackSteps  = env.GetInt("METRICS_DB_PRE_ROLLBACK_STEPS", 0)
	MetricsDbPostRollbackSteps = env.GetInt("METRICS_DB_POST_ROLLBACK_STEPS", 0)
)

type DatabaseType string

const (
	VcpDatabase     DatabaseType = "vcp"
	MetricsDatabase DatabaseType = "metrics"
)

type Migrator struct {
	Models       []interface{}
	Logger       log.Logger
	MigrationsFS embed.FS
	DbType       DatabaseType
}

func (m *Migrator) Migrate(db *gormwrapper.Wrapper, ctx context.Context) error {
	// Step 1: Run pre-migration SQL files
	if err := m.runPreMigrations(db, ctx); err != nil {
		return fmt.Errorf("pre-migration SQL files failed: %w", err)
	}

	// Step 2: Run GORM AutoMigrate
	if err := m.runAutoMigrations(db, ctx, m.Models); err != nil {
		return fmt.Errorf("AutoMigrate failed: %w", err)
	}

	// Step 3: Run post-migration SQL files
	if err := m.runPostMigrations(db, ctx); err != nil {
		return fmt.Errorf("post-migration SQL files failed: %w", err)
	}

	// Step 4: Run post-migration fixes
	if err := m.postMigrationFixes(ctx); err != nil {
		return fmt.Errorf("post-migration fixes failed: %w", err)
	}

	return nil
}

func (m *Migrator) runPreMigrations(db *gormwrapper.Wrapper, ctx context.Context) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(m.MigrationsFS, "migrations/pre")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	mig, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	if err := mig.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run pre-migrations: %w", err)
	}

	m.Logger.InfoContext(ctx, "Successfully executed pre-migrations")
	return nil
}

func (m *Migrator) runPostMigrations(db *gormwrapper.Wrapper, ctx context.Context) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Use a separate schema_migrations table for post-migrations to avoid conflicts
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{
		MigrationsTable: "schema_migrations_post",
	})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(m.MigrationsFS, "migrations/post")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	mig, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	if err := mig.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run post-migrations: %w", err)
	}

	m.Logger.InfoContext(ctx, "Successfully executed post-migrations")
	return nil
}

func (m *Migrator) runAutoMigrations(db *gormwrapper.Wrapper, ctx context.Context, models []interface{}) error {
	checksum, err := m.calculateChecksum(models)
	if err != nil {
		return fmt.Errorf("calculate checksum failed: %w", err)
	}

	if _, err := m.needsMigration(db, checksum); err != nil {
		return err
	} else if false {
		m.Logger.InfoContext(ctx, "Models unchanged, skipping AutoMigrate")
		return nil
	}

	m.Logger.InfoContext(ctx, "Running AutoMigrate for model changes")
	if err := db.WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("automigrate failed: %w", err)
	}

	return m.recordMigration(db, checksum)
}

func (m *Migrator) postMigrationFixes(ctx context.Context) error {
	// Example: Add any foreign keys that GORM might not handle perfectly
	// This is optional and depends on your specific needs
	return nil
}

func (m *Migrator) Rollback(db *gormwrapper.Wrapper, ctx context.Context) error {
	// Run post-rollback SQL files
	if err := m.runPostRollback(db, ctx); err != nil {
		return fmt.Errorf("post-rollback SQL files failed: %w", err)
	}

	// Run pre-rollback SQL files
	if err := m.runPreRollback(db, ctx); err != nil {
		return fmt.Errorf("pre-rollback SQL files failed: %w", err)
	}

	return nil
}

func (m *Migrator) runPreRollback(db *gormwrapper.Wrapper, ctx context.Context) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(m.MigrationsFS, "migrations/pre")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	sqlMig, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	// Check if there are any migrations to rollback
	version, dirty, err := sqlMig.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	if errors.Is(err, migrate.ErrNilVersion) {
		m.Logger.InfoContext(ctx, "No pre-migrations to rollback - database is at initial version")
		return nil
	}

	m.Logger.InfoContext(ctx, fmt.Sprintf("Current pre-migration version: %d (dirty: %v)", version, dirty))

	// Determine the number of rollback steps based on the database type
	var steps int
	if m.DbType == VcpDatabase {
		steps = VcpDbPreRollbackSteps
	} else if m.DbType == MetricsDatabase {
		steps = MetricsDbPreRollbackSteps
	}

	// If steps is 0, skip rollback
	if steps == 0 {
		m.Logger.InfoContext(ctx, "Rollback steps set to 0, skipping pre-migration rollback")
		return nil
	}

	// Rollback the specified number of steps (negative value for rolling back)
	if err := sqlMig.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to rollback pre-migration: %w", err)
	}

	m.Logger.InfoContext(ctx, fmt.Sprintf("Successfully executed pre-migration rollback (%d steps)", steps))
	return nil
}

func (m *Migrator) runPostRollback(db *gormwrapper.Wrapper, ctx context.Context) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Use the same schema_migrations table as post-migrations
	driver, err := postgres.WithInstance(sqlDB, &postgres.Config{
		MigrationsTable: "schema_migrations_post",
	})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(m.MigrationsFS, "migrations/post")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	mig, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}

	// Check if there are any migrations to rollback
	version, dirty, err := mig.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	if errors.Is(err, migrate.ErrNilVersion) {
		m.Logger.InfoContext(ctx, "No post-migrations to rollback - database is at initial version")
		return nil
	}

	m.Logger.InfoContext(ctx, fmt.Sprintf("Current post-migration version: %d (dirty: %v)", version, dirty))

	// Determine the number of rollback steps based on database type
	var steps int
	if m.DbType == VcpDatabase {
		steps = VcpDbPostRollbackSteps
	} else if m.DbType == MetricsDatabase {
		steps = MetricsDbPostRollbackSteps
	}

	// If steps is 0, skip rollback
	if steps == 0 {
		m.Logger.InfoContext(ctx, "Rollback steps set to 0, skipping post-migration rollback")
		return nil
	}

	// Rollback the specified number of steps (negative value for rolling back)
	if err := mig.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to rollback post-migration: %w", err)
	}

	m.Logger.InfoContext(ctx, fmt.Sprintf("Successfully executed post-migration rollback (%d steps)", steps))
	return nil
}

func (m *Migrator) calculateChecksum(models []interface{}) (string, error) {
	hash := md5.New()
	for _, model := range models {
		t := reflect.TypeOf(model)
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		_, err := fmt.Fprintf(hash, "Type:%s\n", t.Name())
		if err != nil {
			return "", err
		}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			_, err := fmt.Fprintf(hash, "Field:%s:%s\n", field.Name, field.Type)
			if err != nil {
				return "", err
			}
			if tag := field.Tag.Get("gorm"); tag != "" {
				_, err := fmt.Fprintf(hash, "GormTag:%s\n", tag)
				if err != nil {
					return "", err
				}
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (m *Migrator) needsMigration(db *gormwrapper.Wrapper, checksum string) (bool, error) {
	var lastChecksum string
	err := db.Raw(`
		SELECT checksum FROM schema_checksums
		ORDER BY created_at DESC
		LIMIT 1
	`).Scan(&lastChecksum).Error()

	if err != nil {
		// Table doesn't exist or empty
		return true, nil
	}
	return checksum != lastChecksum, nil
}

func (m *Migrator) recordMigration(db *gormwrapper.Wrapper, checksum string) error {
	return db.Exec(`
		INSERT INTO schema_checksums (checksum)
		VALUES (?)
	`, checksum).Error()
}

// CreateOrUpdateViews ensures all required views are created or updated after migrations.
func (m *Migrator) CreateOrUpdateViews(db *gormwrapper.Wrapper) error {
	if err := CreateOrUpdatePoolView(db); err != nil {
		return err
	}
	return nil
}

// CreateOrUpdatePoolView ensures the pool_view is always in sync with the pool table schema.
func CreateOrUpdatePoolView(db *gormwrapper.Wrapper) error {
	const viewSQL = `CREATE OR REPLACE VIEW pool_views AS
	SELECT
		p.*,
		coalesce(
			CASE
				WHEN p.qos_type = 'manual' THEN
					-- Sum throughput for non-shared VPGs (each volume gets full VPG throughput)
					COALESCE(SUM(vpg.throughput_mibps) FILTER (WHERE vpg.allocation_type = 'PER_VOLUME'), 0) +
					-- Sum throughput for shared VPGs (divided by volume count per VPG)
					COALESCE(SUM(
						CASE
							WHEN vpg.allocation_type = 'SHARED' AND vpg.id IS NOT NULL THEN
								vpg.throughput_mibps / NULLIF((
									SELECT COUNT(*) FROM volumes v2
									WHERE v2.volume_performance_group_id = vpg.id
									AND v2.deleted_at IS NULL
								), 0)
							ELSE 0
						END
					), 0)
				ELSE sum(v.throughput)
			END,
			0.0
		) as throughput,
		coalesce(
			CASE
				WHEN p.qos_type = 'manual' THEN
					-- Sum IOPS for non-shared VPGs (each volume gets full VPG IOPS)
					COALESCE(SUM(vpg.iops) FILTER (WHERE vpg.allocation_type = 'PER_VOLUME'), 0) +
					-- Sum IOPS for shared VPGs (divided by volume count per VPG)
					COALESCE(SUM(
						CASE
							WHEN vpg.allocation_type = 'SHARED' AND vpg.id IS NOT NULL THEN
								vpg.iops / NULLIF((
									SELECT COUNT(*) FROM volumes v2
									WHERE v2.volume_performance_group_id = vpg.id
									AND v2.deleted_at IS NULL
								), 0)
							ELSE 0
						END
					), 0)
				ELSE 0
			END,
			0
		) as iops,
		coalesce(greatest(0, sum(v.size_in_bytes - v.clones_shared_bytes)), 0) as quota_in_bytes,
		coalesce(count(v.id) filter (where v.clones_shared_bytes > 0), 0) as thin_clone_volume_count,
		count(v.id) as volume_count
	FROM pools p
		LEFT JOIN volumes v on v.pool_id = p.id
		and v.account_id = p.account_id
		and v.deleted_at is null
		LEFT JOIN volume_performance_groups vpg on vpg.id = v.volume_performance_group_id
	GROUP BY
		p.id,
		p.name;`

	err := db.Exec(viewSQL).Error()
	if err == nil {
		return nil
	}
	// SQLSTATE 42P16: column order/type mismatch, drop and recreate
	if strings.Contains(err.Error(), "42P16") {
		dropErr := db.Exec("DROP VIEW IF EXISTS pool_views;").Error()
		if dropErr != nil {
			return dropErr
		}
		return db.Exec(viewSQL).Error()
	}
	return err
}

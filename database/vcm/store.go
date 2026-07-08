package vcm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	spannergorm "github.com/googleapis/go-gorm-spanner"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// gormStore implements Storage on top of GORM, serving the Spanner, Postgres,
// and SQLite drivers behind one dialect-agnostic implementation.
type gormStore struct {
	driver      Driver
	config      SQLConfig
	logger      log.Logger
	displayPath string

	mu sync.RWMutex
	db *gorm.DB
}

// compile-time assurance that the store satisfies the Storage contract.
var _ Storage = (*gormStore)(nil)

// newGormStore builds an unconnected store for the given driver and DSN.
func newGormStore(driver Driver, config SQLConfig, l log.Logger) *gormStore {
	return &gormStore{
		driver:      driver,
		config:      config,
		logger:      l,
		displayPath: displayPath(driver, config.DSN),
	}
}

// dialector resolves the GORM dialector for the configured driver.
func (s *gormStore) dialector() (gorm.Dialector, error) {
	switch s.driver {
	case DriverSpanner:
		if s.config.DSN == "" {
			return nil, fmt.Errorf("vcm: spanner driver requires a database path")
		}
		return spannergorm.New(spannergorm.Config{DriverName: "spanner", DSN: s.config.DSN}), nil
	case DriverPostgres:
		if s.config.DSN == "" {
			return nil, fmt.Errorf("vcm: postgres driver requires a DSN")
		}
		return postgres.Open(s.config.DSN), nil
	case DriverSQLite:
		dsn := s.config.DSN
		if dsn == "" {
			dsn = "file:vcm?mode=memory&cache=shared"
		}
		return sqlite.Open(dsn), nil
	default:
		return nil, fmt.Errorf("vcm: gorm store: unsupported driver %q", s.driver)
	}
}

// Connect opens the GORM connection and verifies it with a ping.
func (s *gormStore) Connect(_ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return s.healthCheckLocked()
	}

	dialector, err := s.dialector()
	if err != nil {
		return err
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		// Silence GORM's stdout logger; it can leak the DSN (Postgres
		// credentials) on errors. Diagnostics flow through returned errors.
		Logger:                 logger.Default.LogMode(logger.Silent),
		SkipDefaultTransaction: true,
		TranslateError:         true,
	})
	if err != nil {
		return fmt.Errorf("vcm: open %s: %w", s.driver, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("vcm: %s sql.DB: %w", s.driver, err)
	}
	if s.config.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(s.config.MaxOpenConns)
	}
	if s.config.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(s.config.MaxIdleConns)
	}
	if s.config.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(s.config.ConnMaxLifetime)
	}

	s.db = db
	return s.healthCheckLocked()
}

// SetupDatabase is a no-op; database provisioning is managed externally (or, on
// the emulator, via the Spanner auto_config_emulator option).
func (s *gormStore) SetupDatabase(_ context.Context) error { return nil }

// Migrate is a no-op: this store bundles no schema. A domain layer migrates its
// own models on top.
func (s *gormStore) Migrate(_ context.Context) error {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()
	if db == nil {
		return fmt.Errorf("vcm: Migrate: not connected")
	}
	return nil
}

// Close releases the underlying database connection.
func (s *gormStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	sqlDB, err := s.db.DB()
	s.db = nil
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// HealthCheck verifies the connection can serve requests.
func (s *gormStore) HealthCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthCheckLocked()
}

func (s *gormStore) healthCheckLocked() error {
	if s.db == nil {
		return fmt.Errorf("vcm: not connected")
	}
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// DatabasePath returns a credential-free diagnostic identifier.
func (s *gormStore) DatabasePath() string {
	return s.displayPath
}

// displayPath returns a credential-free identifier: the Spanner path or SQLite
// path, but only the driver name for Postgres (whose DSN carries credentials).
func displayPath(driver Driver, dsn string) string {
	switch driver {
	case DriverPostgres:
		return "postgres"
	case DriverSQLite:
		if dsn == "" {
			return "file:vcm?mode=memory&cache=shared"
		}
		return dsn
	default: // spanner: a resource path; drop any query options for display
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			return dsn[:i]
		}
		return dsn
	}
}

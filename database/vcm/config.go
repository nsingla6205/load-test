package vcm

import (
	"fmt"
	"strings"
	"time"
)

// Driver identifies the persistence backend used for the VCM store.
type Driver string

const (
	DriverSpanner  Driver = "spanner" // default when Config.Driver is empty
	DriverPostgres Driver = "postgres"
	DriverSQLite   Driver = "sqlite"
)

// Config selects a backend Driver and carries its driver-specific settings.
type Config struct {
	Driver   Driver // empty defaults to DriverSpanner
	Spanner  SpannerConfig
	Postgres SQLConfig
	SQLite   SQLConfig
}

// SQLConfig holds connection settings for the GORM-backed SQL drivers.
type SQLConfig struct {
	// DSN is the data source name: a Postgres connection string or a SQLite
	// file/DSN (empty SQLite defaults to a shared in-memory database).
	DSN string

	// Connection-pool tuning; zero values leave the database/sql defaults.
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// driverOrDefault returns the configured driver, defaulting to Spanner.
func (c Config) driverOrDefault() Driver {
	if c.Driver == "" {
		return DriverSpanner
	}
	return c.Driver
}

// SpannerConfig holds Cloud Spanner connection settings.
type SpannerConfig struct {
	// DatabasePath is projects/<p>/instances/<i>/databases/<d>; when empty,
	// Project, Instance, and Database are joined instead.
	DatabasePath string
	Project      string
	Instance     string
	Database     string

	// CreateIfMissing auto-creates the instance and database on the Spanner
	// emulator (via the auto_config_emulator driver option). It has no effect
	// against production Spanner, where databases are provisioned out-of-band.
	CreateIfMissing bool
}

// ResolveDatabasePath returns the fully-qualified Spanner database path.
func (c SpannerConfig) ResolveDatabasePath() (string, error) {
	if p := strings.TrimSpace(c.DatabasePath); p != "" {
		return p, nil
	}
	project := strings.TrimSpace(c.Project)
	instance := strings.TrimSpace(c.Instance)
	database := strings.TrimSpace(c.Database)
	if project == "" || instance == "" || database == "" {
		return "", fmt.Errorf("vcm: set VCM_SPANNER_DATABASE_PATH or VCM_SPANNER_PROJECT, VCM_SPANNER_INSTANCE, and VCM_SPANNER_DATABASE")
	}
	return DatabasePath(project, instance, database), nil
}

// resolveDSN returns the go-sql-spanner DSN: the resolved database path, plus
// the emulator auto-config option when CreateIfMissing is set.
func (c SpannerConfig) resolveDSN() (string, error) {
	path, err := c.ResolveDatabasePath()
	if err != nil {
		return "", err
	}
	if c.CreateIfMissing {
		path += "?auto_config_emulator=true"
	}
	return path, nil
}

// DatabasePath builds a fully-qualified Spanner database identifier.
func DatabasePath(project, instance, database string) string {
	return fmt.Sprintf("projects/%s/instances/%s/databases/%s", project, instance, database)
}

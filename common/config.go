package common

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"golang.org/x/exp/slog"
)

const (
	DefaultDB = "postgres"
)

type Config struct {
	// Server configuration
	GCPPort             string
	GCPHost             string
	VCMPort             string
	VCMHost             string
	CorePort            string
	CoreHost            string
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ReadHeaderTimeout   time.Duration
	RunMigrationOnStart bool
	MSIEnabled          bool

	// Database connection details
	DBType     string // Type of database (e.g., "postgres", "mysql", "sqlite", "mssql").
	DBHost     string // Database hostname or IP (e.g., "localhost", "127.0.0.1", "db.example.com").
	DBPort     string // Database port (e.g., "5432" for PostgreSQL, "3306" for MySQL, "1433" for MSSQL).
	DBUser     string // Database username.
	DBPassword string // Database password.
	DBName     string // Database name.
	DBSSLMode  string
	// Possible values:
	// "disable" - No SSL.
	// "allow"   - Try SSL, fallback to non-SSL if unavailable.
	// "prefer"  - Prefer SSL, but fallback to non-SSL if necessary.
	// "require" - Always use SSL; no verification.
	// "verify-ca" - Use SSL & verify CA certificate.
	// "verify-full" - Use SSL, verify CA & hostname (most secure, recommended for production).

	DBTimeZone        *time.Location // Time zone for the database connection (e.g., "UTC", "Asia/Kolkata").
	DBMaxOpenConns    int            // Maximum number of open connections (0 = unlimited).
	DBMaxIdleConns    int            // Maximum number of idle connections (default: 2).
	DBConnMaxLifetime time.Duration  // Maximum time a connection can be reused before closing (e.g., 30m, 1h).

	// Metrics DB connection details (second DB)
	MetricsDBType            string
	MetricsDBHost            string
	MetricsDBPort            string
	MetricsDBUser            string
	MetricsDBPassword        string
	MetricsDBName            string
	MetricsDBSSLMode         string
	MetricsDBTimeZone        *time.Location
	MetricsDBMaxOpenConns    int
	MetricsDBMaxIdleConns    int
	MetricsDBConnMaxLifetime time.Duration
	MetricsServerPort        string

	// Credential files (used for security)
	CredentialPath string // Path to a credentials file (e.g., service account JSON for GCP, AWS).
	UsernameFile   string // Path to a file containing the database username.
	PasswordFile   string // Path to a file containing the database password.

	// Admin database credentials (Use only when necessary)
	DBAdminUser     string // Admin user for privileged operations.
	DBAdminPassword string // Admin password (Avoid hardcoding, use secret management tools).
	MSIDBUser       string // MSI user

	// Admin job specifications configuration
	RefreshAdminJobSpecs bool

	// Update backup schedule workflow configuration
	UpdateBackupSchedules bool

	EnableBackgroundTask bool

	// Trigger HarvestPollerUpgradeWorkFlow when the embedded harvest template version changes
	RefreshHarvestOnUpgrade bool

	// Snapshot creation mode: true for sync, false for async (default: false/async)
	SnapshotAPISyncMode bool

	// Synchronous snapshot creation ONTAP job polling configuration
	SyncSnapshotOntapJobPollTimeoutSeconds  int // Timeout in seconds for polling ONTAP job during sync snapshot creation (default: 40)
	SyncSnapshotOntapJobPollIntervalSeconds int // Interval in seconds between polling attempts for ONTAP job during sync snapshot creation (default: 2)
}

func LoadConfig() *Config {
	gcpPort := env.GetString("GCP_PROXY_PORT", "8080")
	gcpHost := env.GetString("GCP_PROXY_HOST", "")
	vcmPort := env.GetString("VCM_PROXY_PORT", "8092")
	vcmHost := env.GetString("VCM_PROXY_HOST", "")
	corePort := env.GetString("CORE_API_PORT", "8081")
	coreHost := env.GetString("CORE_API_HOST", "")
	readTimeout := parseDuration(env.GetString("READ_TIMEOUT", "30s"))
	writeTimeout := parseDuration(env.GetString("WRITE_TIMEOUT", "60s"))
	idleTimeout := parseDuration(env.GetString("IDLE_TIMEOUT", "120s"))
	readHeaderTimeout := parseDuration(env.GetString("READ_HEADER_TIMEOUT", "2s"))

	runMigrationOnStart := env.GetBool("RUN_MIGRATION_ON_START", false)
	msiEnabled := env.GetBool("MSI_ENABLED", false)
	dbType := env.GetString("DB_TYPE", "postgres")
	dbHost := env.GetString("DB_HOST", "")
	dbPort := env.GetString("DB_PORT", "5432")
	dbUser := env.GetString("DB_USER", "")
	dbPassword := env.GetString("DB_PASSWORD", "")
	dbName := env.GetString("DB_NAME", "")
	dbSSLMode := env.GetString("DB_SSL_MODE", "disable")
	dbTimeZone := env.GetString("DB_TIMEZONE", "UTC")
	dbMaxOpenConns := env.GetInt("DB_MAX_OPEN_CONNS", 25)
	dbMaxIdleConns := env.GetInt("DB_MAX_IDLE_CONNS", 25)
	dbConnMaxLifetime := parseDuration(env.GetString("DB_CONN_MAX_LIFETIME", "1h"))
	dbAdminUser := env.GetString("DB_ADMIN_USER", "")
	dbAdminPassword := env.GetString("DB_ADMIN_PASSWORD", "")
	dbMSIUser := env.GetString("DB_MSI_USER", "")
	refreshAdminJobSpecs := env.GetBool("REFRESH_ADMIN_JOB_SPECS", true)
	updateBackupSchedules := env.GetBool("UPDATE_BACKUP_SCHEDULES", true)
	enableBackgroundTask := env.GetBool("ENABLE_BACKGROUND_TASKS", true)
	refreshHarvestOnUpgrade := env.GetBool("REFRESH_HARVEST_ON_UPGRADE", false)
	snapshotAPISyncMode := env.GetBool("SNAPSHOT_API_SYNC_MODE", false)
	syncSnapshotOntapJobPollTimeoutSeconds := env.GetInt("SYNC_SNAPSHOT_ONTAP_JOB_POLL_TIMEOUT_SECONDS", 40)
	syncSnapshotOntapJobPollIntervalSeconds := env.GetInt("SYNC_SNAPSHOT_ONTAP_JOB_POLL_INTERVAL_SECONDS", 2)

	metricsDBType := env.GetString("METRICS_DB_TYPE", "postgres")
	metricsDBHost := env.GetString("METRICS_DB_HOST", "")
	metricsDBPort := env.GetString("METRICS_DB_PORT", "5432")
	metricsDBUser := env.GetString("METRICS_DB_USER", "")
	metricsDBPassword := env.GetString("METRICS_DB_PASSWORD", "")
	metricsDBName := env.GetString("METRICS_DB_NAME", "")
	metricsDBSSLMode := env.GetString("METRICS_DB_SSL_MODE", "disable")
	metricsDBTimeZone := env.GetString("METRICS_DB_TIMEZONE", "UTC")
	metricsDBMaxOpenConns := env.GetInt("METRICS_DB_MAX_OPEN_CONNS", 25)
	metricsDBMaxIdleConns := env.GetInt("METRICS_DB_MAX_IDLE_CONNS", 25)
	metricsDBConnMaxLifetime := parseDuration(env.GetString("METRICS_DB_CONN_MAX_LIFETIME", "1h"))
	metricsServerPort := env.GetString("METRICS_SERVER_PORT", "8080")

	location, err := time.LoadLocation(dbTimeZone)
	if err != nil {
		slog.Error("Invalid timezone: %v", err)
		return nil
	}
	metricsLocation, err := time.LoadLocation(metricsDBTimeZone)
	if err != nil {
		slog.Error("Invalid metrics DB timezone: %v", err)
		return nil
	}

	region := env.GetString("LOCAL_REGION", "")
	regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", utils.DefaultRegionNumberMap)

	if err := validateRegionMap(region, regionMapJsonForNodeSerialNumber); err != nil {
		slog.Error("Invalid Region: %v", err)
		os.Exit(1)
	}

	return &Config{
		GCPPort:                                 gcpPort,
		GCPHost:                                 gcpHost,
		VCMPort:                                 vcmPort,
		VCMHost:                                 vcmHost,
		CorePort:                                corePort,
		CoreHost:                                coreHost,
		ReadTimeout:                             readTimeout,
		WriteTimeout:                            writeTimeout,
		IdleTimeout:                             idleTimeout,
		ReadHeaderTimeout:                       readHeaderTimeout,
		RunMigrationOnStart:                     runMigrationOnStart,
		DBType:                                  dbType,
		DBHost:                                  dbHost,
		DBPort:                                  dbPort,
		DBUser:                                  dbUser,
		DBPassword:                              dbPassword,
		DBName:                                  dbName,
		DBSSLMode:                               dbSSLMode,
		DBTimeZone:                              location,
		DBMaxOpenConns:                          dbMaxOpenConns,
		DBMaxIdleConns:                          dbMaxIdleConns,
		DBConnMaxLifetime:                       dbConnMaxLifetime,
		DBAdminUser:                             dbAdminUser,
		DBAdminPassword:                         dbAdminPassword,
		MSIEnabled:                              msiEnabled,
		MSIDBUser:                               dbMSIUser,
		RefreshAdminJobSpecs:                    refreshAdminJobSpecs,
		UpdateBackupSchedules:                   updateBackupSchedules,
		EnableBackgroundTask:                    enableBackgroundTask,
		RefreshHarvestOnUpgrade:                 refreshHarvestOnUpgrade,
		SnapshotAPISyncMode:                     snapshotAPISyncMode,
		SyncSnapshotOntapJobPollTimeoutSeconds:  syncSnapshotOntapJobPollTimeoutSeconds,
		SyncSnapshotOntapJobPollIntervalSeconds: syncSnapshotOntapJobPollIntervalSeconds,

		MetricsDBType:            metricsDBType,
		MetricsDBHost:            metricsDBHost,
		MetricsDBPort:            metricsDBPort,
		MetricsDBUser:            metricsDBUser,
		MetricsDBPassword:        metricsDBPassword,
		MetricsDBName:            metricsDBName,
		MetricsDBSSLMode:         metricsDBSSLMode,
		MetricsDBTimeZone:        metricsLocation,
		MetricsDBMaxOpenConns:    metricsDBMaxOpenConns,
		MetricsDBMaxIdleConns:    metricsDBMaxIdleConns,
		MetricsDBConnMaxLifetime: metricsDBConnMaxLifetime,
		MetricsServerPort:        metricsServerPort,
	}
}

// LoadTelemetryConfig TODO: Add telemetry config to this function
func LoadTelemetryConfig() *Config {
	return LoadConfig()
}

func parseDuration(value string) time.Duration {
	duration, err := time.ParseDuration(value)
	if err != nil {
		slog.Error("Invalid timeout value: %v", err)
		return 0
	}
	return duration
}

func validateRegionMap(region, regionMapJsonForNodeSerialNumber string) error {
	regionMap := make(map[string]string)
	if err := json.Unmarshal([]byte(regionMapJsonForNodeSerialNumber), &regionMap); err != nil {
		return fmt.Errorf("failed to parse region code map: %w", err)
	}
	_, ok := regionMap[region]
	if !ok {
		return fmt.Errorf("failed to find region code for cluster serial number generation: region %s is not mapped in region code map", region)
	}
	// Check for duplicate values
	valueSet := make(map[string]struct{})
	for _, v := range regionMap {
		if _, exists := valueSet[v]; exists {
			return fmt.Errorf("duplicate region code value found: %s", v)
		}
		valueSet[v] = struct{}{}
	}
	return nil
}

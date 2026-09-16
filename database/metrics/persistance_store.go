package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	dblogger "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/logger"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	logSQLEnabled = env.GetBool("LOG_SQL", false)
)

type PersistenceStore struct {
	config dbutils.DbConfig
	db     *gormwrapper.Wrapper
	mu     sync.RWMutex
	logger log.Logger

	dataStore retryEngine
}

func init() {
	Register("postgres", NewStorage)
	Register("sqlite3", NewStorage)
}

func NewStorage(config dbutils.DbConfig, logger log.Logger) (Storage, error) {
	db := &PersistenceStore{
		config: config,
		logger: logger,
	}
	return db, nil
}

// NewTestStorage creates a new instance of PersistenceStore for testing with an in-memory SQLite database.
func NewTestStorage(logger log.Logger) (Storage, error) {
	db, err := SetupInMemoryDB()
	if err != nil {
		return nil, err
	}

	wrapper := gormwrapper.New(db)
	return &PersistenceStore{
		db:        wrapper,
		logger:    logger,
		dataStore: retryEngine{dataStore: NewDataStoreRepository(wrapper)},
		config: dbutils.DbConfig{
			Type: dbutils.SQLite,
		},
	}, nil
}

// SetupInMemoryDB sets up an in-memory SQLite database for testing.
func SetupInMemoryDB() (*gorm.DB, error) {
	// Use ":memory:" for an in-memory database
	db, err := SetupTestDB()
	if err != nil {
		return nil, err
	}

	// Perform any necessary migrations or setup here
	err = db.AutoMigrate(getMetricModels()...)
	if err != nil {
		return nil, err
	}

	return db, nil
}

func (s *PersistenceStore) Migrate(ctx context.Context) error {
	migrator, err := NewMigrator(s.config, s.logger)
	if err != nil {
		return err
	}
	err = migrator.Migrate(s.db, ctx)
	if err != nil {
		return err
	}
	// Ensure view is always in sync after migrations
	// if err := migrator.CreateOrUpdateViews(s.db); err != nil {
	//	s.logger.Errorf("Failed to create or update views: %v", err)
	// }
	return nil
}

func (s *PersistenceStore) Rollback(ctx context.Context) error {
	migrator, err := NewMigrator(s.config, s.logger)
	if err != nil {
		return err
	}
	return migrator.Rollback(s.db, ctx)
}

// SetupDatabase sets up the database for the PersistenceStore.
func (s *PersistenceStore) SetupDatabase(ctx context.Context) error {
	if err := s.connect(true); err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}
	// Create database and user
	if err := s.createDatabaseAndUser(); err != nil {
		return fmt.Errorf("failed to setup database: %w", err)
	}
	return nil
}

func (s *PersistenceStore) Connect(isAdmin bool) error {
	return s.connect(isAdmin)
}

func (s *PersistenceStore) connect(isAdmin bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil && s.HealthCheck() == nil {
		return nil // Already connected
	}

	db, err := s.createConnection(isAdmin)
	if err != nil {
		return fmt.Errorf("failed to create database connection: %w", err)
	}

	s.db = gormwrapper.New(db)
	s.dataStore = retryEngine{NewDataStoreRepository(s.db)}
	return nil
}

// createConnection establishes a new database connection
func (s *PersistenceStore) createConnection(isAdmin bool) (*gorm.DB, error) {
	logLevel := logger.Error
	if logSQLEnabled {
		logLevel = logger.Info
	}

	gormConfig := &gorm.Config{
		Logger: dblogger.NewGormLogger(s.logger, logLevel),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		PrepareStmt:            false,
		SkipDefaultTransaction: true,
		TranslateError:         true,
	}

	var dialector gorm.Dialector
	switch s.config.Type {
	case dbutils.Postgres:
		dsn, err := s.getPostgresDSN(isAdmin)
		if err != nil {
			return nil, fmt.Errorf("failed to get DSN: %w", err)
		}
		dialector = postgres.Open(dsn)
	case dbutils.SQLite:
		dialector = sqlite.Open(fmt.Sprintf("file:%v?mode=memory&cache=shared", utils.RandomUUID()))
	default:
		return nil, fmt.Errorf("unsupported database type: %s", s.config.Type)
	}

	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get SQL DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(s.config.MaxOpenConns)
	sqlDB.SetMaxIdleConns(s.config.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(s.config.ConnMaxLifetime)

	return db, nil
}

// getPostgresDSN constructs the database connection string
func (s *PersistenceStore) getPostgresDSN(isAdmin bool) (string, error) {
	var username, password string
	if isAdmin {
		username = s.config.AdminUser
		password = s.config.AdminPassword
	} else {
		username = s.config.User
		password = s.config.Password
	}

	query := url.Values{}
	query.Add("sslmode", s.config.SSLMode)
	query.Add("connect_timeout", strconv.Itoa(s.config.ConnectionTimeout))
	query.Add("timezone", s.config.TimeZone)
	// For admin connections, connect to the admin database
	dbName := s.config.Name
	u := &url.URL{
		Scheme:   s.config.Type,
		User:     url.UserPassword(username, password),
		Host:     fmt.Sprintf("%s:%s", s.config.Host, s.config.Port),
		Path:     dbName,
		RawQuery: query.Encode(),
	}

	return u.String(), nil
}

func (s *PersistenceStore) createDatabaseAndUser() error {
	createDBSQL := fmt.Sprintf(
		`CREATE DATABASE %s WITH OWNER = %s  CONNECTION LIMIT = -1`,
		pq.QuoteIdentifier(s.config.Name),
		pq.QuoteIdentifier(s.config.AdminUser))

	if err := s.db.Exec(createDBSQL).Error(); err != nil && !isDatabaseExistsError(err) {
		return fmt.Errorf("create database failed: %w", err)
	}

	// TODO : explore different authentication methods
	createUserSQL := fmt.Sprintf(
		`DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = %s) THEN
				CREATE USER %s WITH PASSWORD %s;
			END IF;
		END
		$$`,
		pq.QuoteLiteral(s.config.User),
		pq.QuoteIdentifier(s.config.User),
		pq.QuoteLiteral(s.config.Password),
	)

	if err := s.db.Exec(createUserSQL).Error(); err != nil {
		return fmt.Errorf("create user failed: %w", err)
	}

	// Grant privileges - NEW FIXED VERSION
	grantDatabaseSQL := fmt.Sprintf(
		`GRANT ALL PRIVILEGES ON DATABASE %s TO %s`,
		pq.QuoteIdentifier(s.config.Name),
		pq.QuoteIdentifier(s.config.User))

	if err := s.db.Exec(grantDatabaseSQL).Error(); err != nil {
		return fmt.Errorf("grant database privileges failed: %w", err)
	}

	grantSchemaSQL := fmt.Sprintf(
		`GRANT ALL PRIVILEGES ON SCHEMA public TO %s`,
		pq.QuoteIdentifier(s.config.User))

	if err := s.db.Exec(grantSchemaSQL).Error(); err != nil {
		return fmt.Errorf("grant schema privileges failed: %w", err)
	}

	return nil
}

func (s *PersistenceStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}

	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *PersistenceStore) HealthCheck() error {
	if s.db == nil {
		return fmt.Errorf("database connection is closed")
	}

	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

func (s *PersistenceStore) WithTransaction(ctx context.Context, fn func(dbutils.Transaction) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.db == nil {
		return errors.New("database connection is closed")
	}

	tx := s.db.WithContext(ctx).Begin()
	if tx.Error() != nil {
		return tx.Error()
	}

	var err error
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}

func SetupStorageForTest(logger log.Logger) (Storage, error) {
	ctx := context.Background()
	store, err := NewTestStorage(logger)
	if err != nil {
		return nil, err
	}
	err = store.Migrate(ctx)
	if err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PersistenceStore) DB() *gorm.DB {
	return s.db.GORM()
}

func (s *PersistenceStore) SQLDB() *sql.DB {
	db, err := s.db.DB()
	if err != nil {
		s.logger.Errorf("Failed to get SQL DB: %v", err)
		return nil
	}
	return db
}

func isDatabaseExistsError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == dbutils.PgDuplicateDatabase
}

// HydratedMetrics CRUD
func (s *PersistenceStore) CreateHydratedMetrics(ctx context.Context, m *datamodel.HydratedMetrics) error {
	return s.dataStore.dataStore.CreateHydratedMetrics(ctx, m)
}

func (s *PersistenceStore) CreateHydratedMetricsBatch(ctx context.Context, metrics []datamodel.HydratedMetrics, batchSize int) error {
	return s.dataStore.dataStore.CreateHydratedMetricsBatch(ctx, metrics, batchSize)
}

func (s *PersistenceStore) GetHydratedMetrics(ctx context.Context, filter map[string]interface{}) ([]datamodel.HydratedMetrics, error) {
	return s.dataStore.dataStore.GetHydratedMetrics(ctx, filter)
}

func (s *PersistenceStore) GetHydratedMetricsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.HydratedMetrics, error) {
	return s.dataStore.dataStore.GetHydratedMetricsWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) UpdateHydratedMetrics(ctx context.Context, id string, updates map[string]interface{}) error {
	return s.dataStore.dataStore.UpdateHydratedMetrics(ctx, id, updates)
}

func (s *PersistenceStore) DeleteHydratedMetrics(ctx context.Context, id string) error {
	return s.dataStore.dataStore.DeleteHydratedMetrics(ctx, id)
}

func (s *PersistenceStore) DeleteHydratedMetricsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return s.dataStore.dataStore.DeleteHydratedMetricsOlderThan(ctx, olderThan)
}

// AggregatedUsage CRUD
func (s *PersistenceStore) CreateAggregatedUsage(ctx context.Context, a *datamodel.AggregatedUsage) error {
	return s.dataStore.dataStore.CreateAggregatedUsage(ctx, a)
}

func (s *PersistenceStore) CreateAggregatedUsageBatch(ctx context.Context, usages []datamodel.AggregatedUsage, batchSize int) error {
	return s.dataStore.dataStore.CreateAggregatedUsageBatch(ctx, usages, batchSize)
}

func (s *PersistenceStore) GetAggregatedUsage(ctx context.Context, filter map[string]interface{}) ([]datamodel.AggregatedUsage, error) {
	return s.dataStore.dataStore.GetAggregatedUsage(ctx, filter)
}

func (s *PersistenceStore) GetLatestAggregatedUsageForAllResources(ctx context.Context, aggregationType string, limit int, afterResourceUUID, afterMeasuredType string) ([]datamodel.AggregatedUsage, error) {
	return s.dataStore.dataStore.GetLatestAggregatedUsageForAllResources(ctx, aggregationType, limit, afterResourceUUID, afterMeasuredType)
}

func (s *PersistenceStore) GetAggregatedUsageWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]datamodel.AggregatedUsage, error) {
	return s.dataStore.dataStore.GetAggregatedUsageWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) UpdateAggregatedUsage(ctx context.Context, id int64, updates map[string]interface{}) error {
	return s.dataStore.dataStore.UpdateAggregatedUsage(ctx, id, updates)
}

func (s *PersistenceStore) DeleteAggregatedUsage(ctx context.Context, id int64) error {
	return s.dataStore.dataStore.DeleteAggregatedUsage(ctx, id)
}

func (s *PersistenceStore) DeleteAggregatedUsageOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return s.dataStore.dataStore.DeleteAggregatedUsageOlderThan(ctx, olderThan)
}

func (s *PersistenceStore) AggregateUsageForBizOps(ctx context.Context, bizopsAggrParams *datamodel.BizOpsAggregateParams) error {
	return s.dataStore.dataStore.AggregateUsageForBizOps(ctx, bizopsAggrParams)
}

func (s *PersistenceStore) DeleteJobsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return s.dataStore.dataStore.DeleteJobsOlderThan(ctx, olderThan)
}

func (s *PersistenceStore) GetRestoreTimestamp(ctx context.Context) (*datamodel.RestoreTimestamp, error) {
	return s.dataStore.dataStore.GetRestoreTimestamp(ctx)
}

func (s *PersistenceStore) UpdateRestoreTimestamp(ctx context.Context, lastProcessedAt time.Time) error {
	return s.dataStore.dataStore.UpdateRestoreTimestamp(ctx, lastProcessedAt)
}

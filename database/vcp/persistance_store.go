package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
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

var logSQLEnabled = env.GetBool("LOG_SQL", false)

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
	err = db.AutoMigrate(getVcpModels()...)
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
	if err := migrator.CreateOrUpdateViews(s.db); err != nil {
		s.logger.Errorf("Failed to create or update views: %v", err)
	}
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

func isDatabaseExistsError(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == dbutils.PgDuplicateDatabase
}

// Implement PersistenceStore interface by delegating to repositories

func (s *PersistenceStore) CreatedPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	return s.dataStore.CreatedPool(ctx, pool)
}

func (s *PersistenceStore) CreatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	return s.dataStore.CreatingPool(ctx, pool)
}

func (s *PersistenceStore) DescribePool(ctx context.Context, poolUUID string, accountID int64) (*datamodel.PoolView, error) {
	return s.dataStore.DescribePool(ctx, poolUUID, accountID)
}

func (s *PersistenceStore) GetPool(ctx context.Context, poolUUID string, accountID int64) (*datamodel.PoolView, error) {
	return s.dataStore.GetPool(ctx, poolUUID, accountID)
}

func (s *PersistenceStore) GetPoolByUUID(ctx context.Context, poolUUID string) (*datamodel.Pool, error) {
	return s.dataStore.GetPoolByUUID(ctx, poolUUID)
}

func (s *PersistenceStore) GetPoolsByUUIDs(ctx context.Context, poolUUIDs []string) ([]*datamodel.Pool, error) {
	return s.dataStore.GetPoolsByUUIDs(ctx, poolUUIDs)
}

func (s *PersistenceStore) GetPoolByUUIDForAccount(ctx context.Context, poolUUID string, accountID int64) (*datamodel.Pool, error) {
	return s.dataStore.GetPoolByUUIDForAccount(ctx, poolUUID, accountID)
}

func (s *PersistenceStore) PoolExistsForAccount(ctx context.Context, poolUUID string, accountID int64) (bool, error) {
	return s.dataStore.PoolExistsForAccount(ctx, poolUUID, accountID)
}

func (s *PersistenceStore) GetPoolByID(ctx context.Context, poolID int64) (*datamodel.Pool, error) {
	return s.dataStore.GetPoolByID(ctx, poolID)
}

func (s *PersistenceStore) GetPoolStateByUUID(ctx context.Context, poolUUID string) (string, error) {
	return s.dataStore.GetPoolStateByUUID(ctx, poolUUID)
}

func (s *PersistenceStore) UpdatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	return s.dataStore.UpdatingPool(ctx, pool)
}

// Expert Mode Volume operations
func (s *PersistenceStore) CreateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.CreateExpertModeVolume(ctx, expertModeVolume)
}
func (s *PersistenceStore) GetExpertModePoolUsedCapacityAndVolumeCount(ctx context.Context, poolID int64) (*ExpertModePoolCapacity, error) {
	return s.dataStore.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, poolID)
}

func (s *PersistenceStore) GetExpertModeVolumeByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetExpertModeVolumeByNameAndPoolID(ctx, name, poolID)
}

func (s *PersistenceStore) GetExpertModeVolumeByUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetExpertModeVolumeByUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetExpertModeVolumeByExternalUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) UpdateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.UpdateExpertModeVolume(ctx, expertModeVolume)
}

func (s *PersistenceStore) UpdatedPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error) {
	return s.dataStore.UpdatedPool(ctx, pool)
}

func (s *PersistenceStore) UpdatePoolSubnetNames(ctx context.Context, poolUUID, snHostProject string, subnetNames []string) error {
	return s.dataStore.UpdatePoolSubnetNames(ctx, poolUUID, snHostProject, subnetNames)
}

func (s *PersistenceStore) UpdatePoolState(ctx context.Context, pool *datamodel.Pool, state string, stateDetails string) (*datamodel.Pool, error) {
	return s.dataStore.UpdatePoolState(ctx, pool, state, stateDetails)
}

func (s *PersistenceStore) UpdatePoolFields(ctx context.Context, poolUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdatePoolFields(ctx, poolUUID, updates)
}

func (s *PersistenceStore) UpdatePoolTieringConfig(ctx context.Context, poolUUID string, hotTierConsumption, coldTierConsumption, tieringThreshold *int64, tieringStatus *datamodel.TieringStatus) error {
	return s.dataStore.UpdatePoolTieringConfig(ctx, poolUUID, hotTierConsumption, coldTierConsumption, tieringThreshold, tieringStatus)
}

func (s *PersistenceStore) UpdateAutoTieringHotTierInfo(ctx context.Context, poolUUID string, hotTierSizeInBytes *int64, enableHotTierAutoResize *bool) error {
	return s.dataStore.UpdateAutoTieringHotTierInfo(ctx, poolUUID, hotTierSizeInBytes, enableHotTierAutoResize)
}

func (s *PersistenceStore) GetPoolsByAccountName(ctx context.Context, accountName string) ([]*datamodel.Pool, error) {
	return s.dataStore.GetPoolsByAccountName(ctx, accountName)
}

func (s *PersistenceStore) GetPoolsByActiveDirectoryId(ctx context.Context, activeDirectoryId string) ([]*datamodel.Pool, error) {
	return s.dataStore.GetPoolsByActiveDirectoryId(ctx, activeDirectoryId)
}

func (s *PersistenceStore) DeletePool(ctx context.Context, pool *datamodel.Pool) error {
	return s.dataStore.DeletePool(ctx, pool)
}

func (s *PersistenceStore) DeletingPool(ctx context.Context, pool *datamodel.Pool) error {
	return s.dataStore.DeletingPool(ctx, pool)
}

func (s *PersistenceStore) ListPools(ctx context.Context, filter *dbutils.Filter) ([]*datamodel.PoolView, error) {
	return s.dataStore.ListPools(ctx, filter)
}

func (s *PersistenceStore) ListPoolsSelective(ctx context.Context, filter *dbutils.Filter, opts PoolPreloadOptions) ([]*datamodel.PoolView, error) {
	return s.dataStore.ListPoolsSelective(ctx, filter, opts)
}

func (s *PersistenceStore) GetKmsConfigsByUUIDs(
	ctx context.Context,
	kmsConfigUUIDs []string,
) ([]*datamodel.KmsConfig, error) {
	return s.dataStore.GetKmsConfigsByUUIDs(ctx, kmsConfigUUIDs)
}

func (s *PersistenceStore) ListPoolsWithFilterAndPaginationOrderedByUUID(ctx context.Context, filter *dbutils.Filter, pagination *dbutils.Pagination) ([]*datamodel.PoolView, error) {
	return s.dataStore.ListPoolsWithFilterAndPaginationOrderedByUUID(ctx, filter, pagination)
}
func (s *PersistenceStore) ListPoolsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.PoolView, error) {
	return s.dataStore.ListPoolsWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) ListExpertModePools(ctx context.Context) ([]*datamodel.Pool, error) {
	return s.dataStore.ListExpertModePools(ctx)
}

func (s *PersistenceStore) ListPoolsForMetrics(ctx context.Context) ([]*PoolMetricsData, error) {
	return s.dataStore.ListPoolsForMetrics(ctx)
}

func (s *PersistenceStore) SumPoolSizesByClusterName(ctx context.Context, clusterName string) (int64, error) {
	return s.dataStore.SumPoolSizesByClusterName(ctx, clusterName)
}

func (s *PersistenceStore) ListPoolsForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*PoolResourceData, error) {
	return s.dataStore.ListPoolsForResourceData(ctx, startTime, endTime, pagination)
}

func (s *PersistenceStore) ListOntapModePoolsForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*PoolResourceData, error) {
	return s.dataStore.ListOntapModePoolsForResourceData(ctx, startTime, endTime, pagination)
}

func (s *PersistenceStore) GetBlockOnlyPoolIDs(ctx context.Context) (map[int64]bool, error) {
	return s.dataStore.GetBlockOnlyPoolIDs(ctx)
}

func (s *PersistenceStore) ListPoolUUIDs(ctx context.Context, filter *dbutils.Filter) ([]*PoolIdentifier, error) {
	return s.dataStore.ListPoolUUIDs(ctx, filter)
}

func (s *PersistenceStore) ListPoolUUIDsPaginated(ctx context.Context, filter *dbutils.Filter, offset, limit int) ([]*PoolIdentifier, error) {
	return s.dataStore.ListPoolUUIDsPaginated(ctx, filter, offset, limit)
}

func (s *PersistenceStore) GetPoolsCount(ctx context.Context, filter *dbutils.Filter) (int64, error) {
	return s.dataStore.GetPoolsCount(ctx, filter)
}

func (s *PersistenceStore) CountActivePoolsByNetwork(ctx context.Context, network string, excludePoolUUID string) (int64, error) {
	return s.dataStore.CountActivePoolsByNetwork(ctx, network, excludePoolUUID)
}

func (s *PersistenceStore) SumPoolSizeByHostUUID(ctx context.Context, hostUUID string) (int64, error) {
	return s.dataStore.SumPoolSizeByHostUUID(ctx, hostUUID)
}

func (s *PersistenceStore) GetPoolByName(ctx context.Context, conditions [][]interface{}) (*datamodel.PoolView, error) {
	return s.dataStore.GetPoolByName(ctx, conditions)
}

func (s *PersistenceStore) SavePoolWithVsaDetails(ctx context.Context, pool *datamodel.Pool, cluster *datamodel.ClusterDetails) error {
	return s.dataStore.SavePoolWithVsaDetails(ctx, pool, cluster)
}

func (s *PersistenceStore) UpdatePoolWithKmsConfigID(ctx context.Context, pool *datamodel.Pool, kmsConfigUUID string) (*datamodel.Pool, error) {
	return s.dataStore.UpdatePoolWithKmsConfigID(ctx, pool, kmsConfigUUID)
}

func (s *PersistenceStore) CreateVolume(ctx context.Context, volume *datamodel.Volume) (*datamodel.Volume, error) {
	return s.dataStore.CreateVolume(ctx, volume)
}

func (s *PersistenceStore) CreateVolumeReplication(ctx context.Context, volumeRep *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	return s.dataStore.CreateVolumeReplication(ctx, volumeRep)
}

func (s *PersistenceStore) GetVolumeReplication(ctx context.Context, id string) (*datamodel.VolumeReplication, error) {
	return s.dataStore.GetVolumeReplication(ctx, id)
}

func (s *PersistenceStore) GetVolumeReplicationByVolumeID(ctx context.Context, volumeID int64) (*datamodel.VolumeReplication, error) {
	return s.dataStore.GetVolumeReplicationByVolumeID(ctx, volumeID)
}

func (s *PersistenceStore) UpdateVolumeReplication(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	return s.dataStore.UpdateVolumeReplication(ctx, volumeRep)
}

func (s *PersistenceStore) UpdateVolumeReplicationFields(ctx context.Context, volumeRepUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateVolumeReplicationFields(ctx, volumeRepUUID, updates)
}

func (s *PersistenceStore) UpdateVolumeReplicationStates(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	return s.dataStore.UpdateVolumeReplicationStates(ctx, volumeRep)
}

func (s *PersistenceStore) UpdateVolumeReplicationTransferStats(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	return s.dataStore.UpdateVolumeReplicationTransferStats(ctx, volumeRep)
}

func (s *PersistenceStore) DeleteVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
	return s.dataStore.DeleteVolumeReplication(ctx, replication)
}

func (s *PersistenceStore) GetVolumeReplicationByProjectId(ctx context.Context, accountId int64) ([]*datamodel.VolumeReplication, error) {
	return s.dataStore.GetVolumeReplicationByProjectId(ctx, accountId)
}

func (s *PersistenceStore) GetVolumeReplicationCount(ctx context.Context, accountName string) (int64, error) {
	return s.dataStore.GetVolumeReplicationCount(ctx, accountName)
}

func (s *PersistenceStore) GetVolumeReplicationCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error) {
	return s.dataStore.GetVolumeReplicationCountByClusterPeerID(ctx, clusterPeerID)
}

func (s *PersistenceStore) GetVolumeReplicationCountByVolumeID(ctx context.Context, volumeID int64) (int64, error) {
	return s.dataStore.GetVolumeReplicationCountByVolumeID(ctx, volumeID)
}

func (s *PersistenceStore) GetReplicatedVolumeUUIDs(ctx context.Context, volumeUUIDs []string) ([]string, error) {
	return s.dataStore.GetReplicatedVolumeUUIDs(ctx, volumeUUIDs)
}

func (s *PersistenceStore) GetVolumeReplicationCountByPeerDetails(ctx context.Context, accountName string, peerSvmName string, peerVolumeName string) (int64, error) {
	return s.dataStore.GetVolumeReplicationCountByPeerDetails(ctx, accountName, peerSvmName, peerVolumeName)
}

func (s *PersistenceStore) ListVolumeReplications(ctx context.Context, filter dbutils.Filter, queryDepth int) ([]*datamodel.VolumeReplication, error) {
	return s.dataStore.ListVolumeReplications(ctx, filter, queryDepth)
}

func (s *PersistenceStore) ListVolumeReplicationsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.VolumeReplication, error) {
	return s.dataStore.ListVolumeReplicationsWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) GetVolume(ctx context.Context, id string) (*datamodel.Volume, error) {
	return s.dataStore.GetVolume(ctx, id)
}

func (s *PersistenceStore) DescribeVolume(ctx context.Context, id string) (*datamodel.Volume, error) {
	return s.dataStore.DescribeVolume(ctx, id)
}

func (s *PersistenceStore) GetVolumeWithAccountID(ctx context.Context, id string, accountID int64) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeWithAccountID(ctx, id, accountID)
}

func (s *PersistenceStore) GetVolumeByIDAndAccountID(ctx context.Context, volumeID int64, accountID int64) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByIDAndAccountID(ctx, volumeID, accountID)
}

func (s *PersistenceStore) GetVolumeByNameAndAccountID(ctx context.Context, id string, accountID int64) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByNameAndAccountID(ctx, id, accountID)
}

func (s *PersistenceStore) GetVolumeByNameAndAccountIDWithPool(ctx context.Context, name string, accountID int64) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByNameAndAccountIDWithPool(ctx, name, accountID)
}

func (s *PersistenceStore) GetVolumeByNameAccountIDAndZone(ctx context.Context, name string, accountID int64, zone string, isRegionalPool bool) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByNameAccountIDAndZone(ctx, name, accountID, zone, isRegionalPool)
}

func (s *PersistenceStore) GetVolumeByName(ctx context.Context, name string) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByName(ctx, name)
}

func (s *PersistenceStore) UpdateVolume(ctx context.Context, volume *datamodel.Volume) error {
	return s.dataStore.UpdateVolume(ctx, volume)
}

func (s *PersistenceStore) RevertedVolume(ctx context.Context, volume *datamodel.Volume, snapshot *datamodel.Snapshot) ([]*datamodel.Snapshot, error) {
	return s.dataStore.RevertedVolume(ctx, volume, snapshot)
}

func (s *PersistenceStore) UpdateVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateVolumeFields(ctx, volumeUUID, updates)
}

func (s *PersistenceStore) BatchUpdateVolumeFields(ctx context.Context, updates []datamodel.VolumeFieldUpdate) error {
	return s.dataStore.BatchUpdateVolumeFields(ctx, updates)
}

func (s *PersistenceStore) BatchUpdateVolumeTieringFields(ctx context.Context, updates map[string]datamodel.VolumeTieringUpdate) error {
	return s.dataStore.BatchUpdateVolumeTieringFields(ctx, updates)
}

func (s *PersistenceStore) UpdateKmsConfig(ctx context.Context, kmsUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateKmsConfig(ctx, kmsUUID, updates)
}

func (s *PersistenceStore) IsKmsConfigInUse(ctx context.Context, kmsConfigUUID string) (bool, error) {
	return s.dataStore.IsKmsConfigInUse(ctx, kmsConfigUUID)
}

func (s *PersistenceStore) DeleteVolume(ctx context.Context, id string) (*datamodel.Volume, error) {
	return s.dataStore.DeleteVolume(ctx, id)
}

func (s *PersistenceStore) DeleteVolumeAndChildResources(ctx context.Context, volumeUUID string) (*datamodel.Volume, error) {
	return s.dataStore.DeleteVolumeAndChildResources(ctx, volumeUUID)
}

func (s *PersistenceStore) UpdateVolumeState(ctx context.Context, id string, state string, stateDetails string) (*datamodel.Volume, error) {
	return s.dataStore.UpdateVolumeState(ctx, id, state, stateDetails)
}

func (s *PersistenceStore) ListVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error) {
	return s.dataStore.ListVolumes(ctx, conditions)
}

func (s *PersistenceStore) ListAllVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return s.dataStore.ListAllVolumes(ctx, conditions, pagination)
}

func (s *PersistenceStore) ListVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return s.dataStore.ListVolumesWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) ListVolumesForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*VolumeResourceData, error) {
	return s.dataStore.ListVolumesForResourceData(ctx, startTime, endTime, pagination)
}

func (s *PersistenceStore) GetVolumeCount(ctx context.Context, accountName string) (int64, error) {
	return s.dataStore.GetVolumeCount(ctx, accountName)
}

func (s *PersistenceStore) GetFlexCacheVolumeCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error) {
	return s.dataStore.GetFlexCacheVolumeCountByClusterPeerID(ctx, clusterPeerID)
}

func (s *PersistenceStore) GetVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	return s.dataStore.GetVolumesByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetVolumesByPoolIDNoPreload(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	return s.dataStore.GetVolumesByPoolIDNoPreload(ctx, poolID)
}

func (s *PersistenceStore) GetPoolVolumesForQosTransition(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	return s.dataStore.GetPoolVolumesForQosTransition(ctx, poolID)
}

func (s *PersistenceStore) GetVolumesByVolumePerformanceGroupID(ctx context.Context, vpgID int64) ([]*datamodel.Volume, error) {
	return s.dataStore.GetVolumesByVolumePerformanceGroupID(ctx, vpgID)
}

func (s *PersistenceStore) DereferenceVPGFromDeletedVolumes(ctx context.Context, vpgID int64) error {
	return s.dataStore.DereferenceVPGFromDeletedVolumes(ctx, vpgID)
}

func (s *PersistenceStore) DereferencePoolVolumesFromVPGs(ctx context.Context, poolID int64) (int64, error) {
	return s.dataStore.DereferencePoolVolumesFromVPGs(ctx, poolID)
}

func (s *PersistenceStore) HasDependentChildThinClone(ctx context.Context, poolID int64, parentVolumeUUID string) (bool, error) {
	return s.dataStore.HasDependentChildThinClone(ctx, poolID, parentVolumeUUID)
}

func (s *PersistenceStore) GetVolumeCountByPoolID(ctx context.Context, poolID int64) (int64, error) {
	return s.dataStore.GetVolumeCountByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetMultipleVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error) {
	return s.dataStore.GetMultipleVolumes(ctx, conditions)
}

func (s *PersistenceStore) GetMultipleVolumesSelective(ctx context.Context, conditions [][]interface{}, opts VolumePreloadOptions) ([]*datamodel.Volume, error) {
	return s.dataStore.GetMultipleVolumesSelective(ctx, conditions, opts)
}

func (s *PersistenceStore) VerifyVolumeOwnership(ctx context.Context, volumeID string, accountName string) (*datamodel.Volume, error) {
	return s.dataStore.VerifyVolumeOwnership(ctx, volumeID, accountName)
}

func (s *PersistenceStore) GetAccount(ctx context.Context, name string) (*datamodel.Account, error) {
	return s.dataStore.GetAccount(ctx, name)
}

func (s *PersistenceStore) ListPinnedHostCredentials(ctx context.Context, accountName string) ([]*HostCredential, error) {
	return s.dataStore.ListPinnedHostCredentials(ctx, accountName)
}

func (s *PersistenceStore) GetDedicatedPinnedHostCredential(ctx context.Context, accountName, clusterExternalName string) (*HostCredential, error) {
	return s.dataStore.GetDedicatedPinnedHostCredential(ctx, accountName, clusterExternalName)
}

func (s *PersistenceStore) GetAccountByUUID(ctx context.Context, uuid string) (*datamodel.Account, error) {
	return s.dataStore.GetAccountByUUID(ctx, uuid)
}

func (s *PersistenceStore) GetAccountByInternalID(ctx context.Context, accountID int64) (*datamodel.Account, error) {
	return s.dataStore.GetAccountByInternalID(ctx, accountID)
}

func (s *PersistenceStore) UpdateAccountStateForHandleResource(ctx context.Context, accountUUID string, newState string) error {
	return s.dataStore.UpdateAccountStateForHandleResource(ctx, accountUUID, newState)
}

func (s *PersistenceStore) UpdateAccountVolumeRefreshTimestamp(ctx context.Context, accountUUID string, completionTime time.Time) error {
	return s.dataStore.UpdateAccountVolumeRefreshTimestamp(ctx, accountUUID, completionTime)
}

func (s *PersistenceStore) UpdateAccountTrialMetadata(ctx context.Context, account *datamodel.Account, trial *datamodel.AccountTrialMode) error {
	return s.dataStore.UpdateAccountTrialMetadata(ctx, account, trial)
}

func (s *PersistenceStore) GetAccountsWithFilter(ctx context.Context, filter *dbutils.Filter, pagination *dbutils.Pagination) ([]*datamodel.Account, error) {
	return s.dataStore.GetAccountsWithFilter(ctx, filter, pagination)
}

func (s *PersistenceStore) CreateAccount(ctx context.Context, account *datamodel.Account) (*datamodel.Account, error) {
	return s.dataStore.CreateAccount(ctx, account)
}

func (s *PersistenceStore) GetVolumeLatestBackupMap(ctx context.Context) (map[int64]*datamodel.VolumeLatestBackup, error) {
	return s.dataStore.GetVolumeLatestBackupMap(ctx)
}

func (s *PersistenceStore) CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error) {
	return s.dataStore.CreateJob(ctx, job)
}

func (s *PersistenceStore) CreateJobWithWorkflowID(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
	return s.dataStore.CreateJobWithWorkflowID(ctx, job, workflowID)
}

func (s *PersistenceStore) CreateCMEKJob(ctx context.Context, accountId int64, job *datamodel.Job) (*datamodel.Job, error) {
	return s.dataStore.CreateCMEKJob(ctx, accountId, job)
}

func (s *PersistenceStore) UpdateJob(ctx context.Context, id string, status string, trackingID int, errorDetails string) error {
	return s.dataStore.UpdateJob(ctx, id, status, trackingID, errorDetails)
}

func (s *PersistenceStore) UpdateJobAttributes(ctx context.Context, jobUUID string, jobAttributes *datamodel.JobAttributes) error {
	return s.dataStore.UpdateJobAttributes(ctx, jobUUID, jobAttributes)
}

func (s *PersistenceStore) DeleteJob(ctx context.Context, id string, errorDetails string) error {
	return s.dataStore.DeleteJob(ctx, id, errorDetails)
}

func (s *PersistenceStore) GetJob(ctx context.Context, id string) (*datamodel.Job, error) {
	return s.dataStore.GetJob(ctx, id)
}

func (s *PersistenceStore) GetJobsWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.Job, error) {
	return s.dataStore.GetJobsWithCondition(ctx, filter)
}

func (s *PersistenceStore) GetPoolByVendorID(ctx context.Context, vendorID string, accountID int64) (*datamodel.PoolView, error) {
	return s.dataStore.GetPoolByVendorID(ctx, vendorID, accountID)
}

func (s *PersistenceStore) GetOngoingMigrateKmsConfigJob(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	return s.dataStore.GetOngoingMigrateKmsConfigJob(ctx, accountId)
}

func (s *PersistenceStore) GetOngoingEncryptPoolJobForAccount(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	return s.dataStore.GetOngoingEncryptPoolJobForAccount(ctx, accountId)
}

func (s *PersistenceStore) GetSvmForPoolID(ctx context.Context, poolID int64) (*datamodel.Svm, error) {
	return s.dataStore.GetSvmForPoolID(ctx, poolID)
}

func (s *PersistenceStore) CreateNode(ctx context.Context, node *datamodel.Node) (*datamodel.Node, error) {
	return s.dataStore.CreateNode(ctx, node)
}

func (s *PersistenceStore) EnsureHostNode(ctx context.Context, node *datamodel.Node) (*datamodel.Node, error) {
	return s.dataStore.EnsureHostNode(ctx, node)
}

func (s *PersistenceStore) GetNodesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Node, error) {
	return s.dataStore.GetNodesByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetNodesByPoolIDs(ctx context.Context, poolIDs []int64) ([]*datamodel.Node, error) {
	return s.dataStore.GetNodesByPoolIDs(ctx, poolIDs)
}

func (s *PersistenceStore) GetNodesByHostIDAndNames(ctx context.Context, hostID int64, names []string) (map[string]int64, error) {
	return s.dataStore.GetNodesByHostIDAndNames(ctx, hostID, names)
}

func (s *PersistenceStore) GetNodeByID(ctx context.Context, nodeID int64) (*datamodel.Node, error) {
	return s.dataStore.GetNodeByID(ctx, nodeID)
}

func (s *PersistenceStore) GetFaultDomainCountsByZone(ctx context.Context) (map[string]map[string]int64, error) {
	return s.dataStore.GetFaultDomainCountsByZone(ctx)
}

func (s *PersistenceStore) CreateSVM(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	return s.dataStore.CreateSVM(ctx, svm)
}

func (s *PersistenceStore) CreateSvmInCreatingState(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	return s.dataStore.CreateSvmInCreatingState(ctx, svm)
}

func (s *PersistenceStore) GetSvmsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Svm, error) {
	return s.dataStore.GetSvmsByPoolID(ctx, poolID)
}

func (s *PersistenceStore) ActiveSvmExistsByPoolID(ctx context.Context, poolID int64) (bool, error) {
	return s.dataStore.ActiveSvmExistsByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetNextSVMIndexByPoolID(ctx context.Context, poolID int64) (int64, error) {
	return s.dataStore.GetNextSVMIndexByPoolID(ctx, poolID)
}

func (s *PersistenceStore) UpdateSvmWithKmsConfigIDs(ctx context.Context, svm *datamodel.Svm, gcpKmsConfigUUID, externalGcpKmsConfigUUID string) (*datamodel.Svm, error) {
	return s.dataStore.UpdateSvmWithKmsConfigIDs(ctx, svm, gcpKmsConfigUUID, externalGcpKmsConfigUUID)
}

func (s *PersistenceStore) UpdateSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm, activeDirectoryID int64) (*datamodel.Svm, error) {
	return s.dataStore.UpdateSvmActiveDirectoryID(ctx, svm, activeDirectoryID)
}

func (s *PersistenceStore) UnsetSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	return s.dataStore.UnsetSvmActiveDirectoryID(ctx, svm)
}

func (s *PersistenceStore) CreateLif(ctx context.Context, lif *datamodel.Lif) (*datamodel.Lif, error) {
	return s.dataStore.CreateLif(ctx, lif)
}

func (s *PersistenceStore) GetLifByNodeID(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error) {
	return s.dataStore.GetLifByNodeID(ctx, nodeID, 0)
}

func (s *PersistenceStore) GetLifsForNodesWithProtocol(ctx context.Context, nodeIDs []int64, accountID int64, protocol string) ([]*datamodel.Lif, error) {
	return s.dataStore.GetLifsForNodesWithProtocol(ctx, nodeIDs, accountID, protocol)
}

func (s *PersistenceStore) DeleteSVM(ctx context.Context, svm *datamodel.Svm) error {
	return s.dataStore.DeleteSVM(ctx, svm)
}

func (s *PersistenceStore) DeletingSVM(ctx context.Context, svm *datamodel.Svm) error {
	return s.dataStore.DeletingSVM(ctx, svm)
}

func (s *PersistenceStore) TransitionSvmToDeleting(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	return s.dataStore.TransitionSvmToDeleting(ctx, svm)
}

func (s *PersistenceStore) UpdateSvmState(ctx context.Context, svm *datamodel.Svm, state string, stateDetails string, conditions ...[]interface{}) error {
	return s.dataStore.UpdateSvmState(ctx, svm, state, stateDetails, conditions...)
}

func (s *PersistenceStore) DeleteLif(ctx context.Context, lif *datamodel.Lif) error {
	return s.dataStore.DeleteLif(ctx, lif)
}

func (s *PersistenceStore) GetAllocatedIPsInIPRange(ctx context.Context, ipRangeID int64) (map[string]bool, error) {
	return s.dataStore.GetAllocatedIPsInIPRange(ctx, ipRangeID)
}

func (s *PersistenceStore) ValidateIPAddressesAvailableInIPRange(ctx context.Context, ipRangeUUID string, ipAddressCount int) error {
	return s.dataStore.ValidateIPAddressesAvailableInIPRange(ctx, ipRangeUUID, ipAddressCount)
}

func (s *PersistenceStore) CreateDataLifReservation(ctx context.Context, lif *datamodel.Lif) (*datamodel.Lif, error) {
	return s.dataStore.CreateDataLifReservation(ctx, lif)
}

func (s *PersistenceStore) AllocateDataLifIPs(ctx context.Context, ipRangeUUID string, reservations []*datamodel.Lif) ([]*datamodel.Lif, error) {
	return s.dataStore.AllocateDataLifIPs(ctx, ipRangeUUID, reservations)
}

func (s *PersistenceStore) GetLifsBySvmID(ctx context.Context, svmID int64) ([]*datamodel.Lif, error) {
	return s.dataStore.GetLifsBySvmID(ctx, svmID)
}

func (s *PersistenceStore) DeleteLifsBySvmID(ctx context.Context, svmID int64) error {
	return s.dataStore.DeleteLifsBySvmID(ctx, svmID)
}

func (s *PersistenceStore) DeleteNode(ctx context.Context, node *datamodel.Node) error {
	return s.dataStore.DeleteNode(ctx, node)
}

func (s *PersistenceStore) ErroredNode(ctx context.Context, node *datamodel.Node, errMsg string) error {
	return s.dataStore.ErroredNode(ctx, node, errMsg)
}

func (s *PersistenceStore) ErroredSVM(ctx context.Context, svm *datamodel.Svm, errMsg string) error {
	return s.dataStore.ErroredSVM(ctx, svm, errMsg)
}

func (s *PersistenceStore) DeletingNode(ctx context.Context, node *datamodel.Node) error {
	return s.dataStore.DeletingNode(ctx, node)
}

func (s *PersistenceStore) UpdateNodesInstanceType(ctx context.Context, poolID int64, newInstanceType string) error {
	return s.dataStore.UpdateNodesInstanceType(ctx, poolID, newInstanceType)
}

func (s *PersistenceStore) UpdateNodesSizeAndInstanceType(ctx context.Context, poolID int64, updatesByNodeName map[string]datamodel.NodeDetails) error {
	return s.dataStore.UpdateNodesSizeAndInstanceType(ctx, poolID, updatesByNodeName)
}

func (s *PersistenceStore) GetLifForNode(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error) {
	return s.dataStore.GetLifForNode(ctx, nodeID, accountID)
}

func (s *PersistenceStore) GetHostGroup(ctx context.Context, hostGroupUUID string, accountID int64) (*datamodel.HostGroup, error) {
	return s.dataStore.GetHostGroup(ctx, hostGroupUUID, accountID)
}

func (s *PersistenceStore) CreateHostGroup(ctx context.Context, hostGroup *datamodel.HostGroup) (*datamodel.HostGroup, error) {
	return s.dataStore.CreateHostGroup(ctx, hostGroup)
}

func (s *PersistenceStore) CreateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	return s.dataStore.CreateHost(ctx, host)
}

func (s *PersistenceStore) CountHostsWithIPAddress(ctx context.Context, ipAddress string, includeDeleted bool) (int64, error) {
	return s.dataStore.CountHostsWithIPAddress(ctx, ipAddress, includeDeleted)
}

func (s *PersistenceStore) GetHost(ctx context.Context, hostUUID string) (*datamodel.Host, error) {
	return s.dataStore.GetHost(ctx, hostUUID)
}

func (s *PersistenceStore) GetHostByName(ctx context.Context, name string) (*datamodel.Host, error) {
	return s.dataStore.GetHostByName(ctx, name)
}

func (s *PersistenceStore) GetHostsByUUIDs(ctx context.Context, hostUUIDs []string) ([]*datamodel.Host, error) {
	return s.dataStore.GetHostsByUUIDs(ctx, hostUUIDs)
}

func (s *PersistenceStore) ListHosts(ctx context.Context) ([]*datamodel.Host, error) {
	return s.dataStore.ListHosts(ctx)
}

func (s *PersistenceStore) UpdateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	return s.dataStore.UpdateHost(ctx, host)
}

func (s *PersistenceStore) DeleteHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	return s.dataStore.DeleteHost(ctx, host)
}

func (s *PersistenceStore) GetMultipleHostGroups(ctx context.Context, ids []string, accountID int64) ([]*datamodel.HostGroup, error) {
	return s.dataStore.GetMultipleHostGroups(ctx, ids, accountID)
}

func (s *PersistenceStore) GetHostGroupsByUUIDs(ctx context.Context, hostGroupUUIDs []string) ([]*datamodel.HostGroup, error) {
	return s.dataStore.GetHostGroupsByUUIDs(ctx, hostGroupUUIDs)
}

func (s *PersistenceStore) DeleteHostGroup(ctx context.Context, hostGroupUUID string, accountID int64) (*datamodel.HostGroup, error) {
	return s.dataStore.DeleteHostGroup(ctx, hostGroupUUID, accountID)
}

func (s *PersistenceStore) UpdateHostGroupsState(ctx context.Context, hostGroupUUID []string, accountID int64, state string, stateDetails string) error {
	return s.dataStore.UpdateHostGroupsState(ctx, hostGroupUUID, accountID, state, stateDetails)
}

func (s *PersistenceStore) CreatingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	return s.dataStore.CreatingSnapshot(ctx, snapshot)
}

func (s *PersistenceStore) UpdateSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	return s.dataStore.UpdateSnapshot(ctx, snapshot)
}

func (s *PersistenceStore) UpdateSnapshotForHandleResource(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	return s.dataStore.UpdateSnapshotForHandleResource(ctx, snapshot)
}

func (s *PersistenceStore) GetSnapshotByUUID(ctx context.Context, uuid string, accountID int64, volumeID int64) (*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotByUUID(ctx, uuid, accountID, volumeID)
}

func (s *PersistenceStore) GetSnapshotByNameAndVolumeId(ctx context.Context, snapshotName string, accountID int64, volumeID int64) (*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotByNameAndVolumeId(ctx, snapshotName, accountID, volumeID)
}

func (s *PersistenceStore) GetSnapshotByPoolID(ctx context.Context, uuid string, accountID int64, poolID int64, isParentSnapshot bool) (*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotByPoolID(ctx, uuid, accountID, poolID, isParentSnapshot)
}

func (s *PersistenceStore) GetSnapshotsWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotsWithCondition(ctx, filter)
}

func (s *PersistenceStore) GetWronglyDeletedSnapshot(ctx context.Context, snapshotExternalUUID string) (*datamodel.Snapshot, error) {
	return s.dataStore.GetWronglyDeletedSnapshot(ctx, snapshotExternalUUID)
}

func (s *PersistenceStore) UnDeleteSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error {
	return s.dataStore.UnDeleteSnapshot(ctx, snapshot)
}

func (s *PersistenceStore) DeletingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error {
	return s.dataStore.DeletingSnapshot(ctx, snapshot)
}

func (s *PersistenceStore) DeleteSnapshot(ctx context.Context, id string) (*datamodel.Snapshot, error) {
	return s.dataStore.DeleteSnapshot(ctx, id)
}

func (s *PersistenceStore) BatchDeleteSnapshots(ctx context.Context, snapshotIDs []int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.BatchDeleteSnapshots(ctx, snapshotIDs)
}

func (s *PersistenceStore) BatchCreateSnapshots(ctx context.Context, newSnapshots []*datamodel.Snapshot, returnCreatedSnapshotUUIDs bool) ([]string, error) {
	return s.dataStore.BatchCreateSnapshots(ctx, newSnapshots, returnCreatedSnapshotUUIDs)
}

func (s *PersistenceStore) BatchUpdateSnapshots(ctx context.Context, snapshots []*datamodel.Snapshot) error {
	return s.dataStore.BatchUpdateSnapshots(ctx, snapshots)
}

func (s *PersistenceStore) BatchUnDeleteSnapshots(ctx context.Context, snapshots []*datamodel.Snapshot) error {
	return s.dataStore.BatchUnDeleteSnapshots(ctx, snapshots)
}

func (s *PersistenceStore) BatchGetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*datamodel.Snapshot, error) {
	return s.dataStore.BatchGetSnapshotsByUUIDs(ctx, snapshotUUIDs)
}

func (s *PersistenceStore) BatchGetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error) {
	return s.dataStore.BatchGetBackupsByUUIDs(ctx, backupUUIDs)
}

func (s *PersistenceStore) BatchGetWronglyDeletedSnapshots(ctx context.Context, snapshotExternalUUIDs []string) ([]*datamodel.Snapshot, error) {
	return s.dataStore.BatchGetWronglyDeletedSnapshots(ctx, snapshotExternalUUIDs)
}

func (s *PersistenceStore) GetAppConsistentSnapshotsForVolume(ctx context.Context, accountID, volumeID int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetAppConsistentSnapshotsForVolume(ctx, accountID, volumeID)
}

func (s *PersistenceStore) GetSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotsByVolumeID(ctx, volumeID)
}

func (s *PersistenceStore) GetReplicationSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetReplicationSnapshotsByVolumeID(ctx, volumeID)
}

func (s *PersistenceStore) GetSnapshotsByVolumeIDs(ctx context.Context, volumeIDs []int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotsByVolumeIDs(ctx, volumeIDs)
}

func (s *PersistenceStore) GetSnapshotsByTypeAndVolumeID(ctx context.Context, snapshotType string, volumeID int64) ([]*datamodel.Snapshot, error) {
	return s.dataStore.GetSnapshotsByTypeAndVolumeID(ctx, snapshotType, volumeID)
}

func (s *PersistenceStore) GetMultipleKmsConfigs(ctx context.Context, conditions [][]interface{}) ([]*datamodel.KmsConfig, error) {
	return s.dataStore.GetMultipleKmsConfigs(ctx, conditions)
}

func (s *PersistenceStore) GetKmsConfig(ctx context.Context, kmsConfigUUID string) (*datamodel.KmsConfig, error) {
	return s.dataStore.GetKmsConfig(ctx, kmsConfigUUID)
}

func (s *PersistenceStore) UpdateKmsConfigState(ctx context.Context, kmsConfigUUID string, state string, stateDetails string) (*datamodel.KmsConfig, error) {
	return s.dataStore.UpdateKmsConfigState(ctx, kmsConfigUUID, state, stateDetails)
}

func (s *PersistenceStore) ListKmsConfigByAccountID(ctx context.Context, accountID int64) ([]*datamodel.KmsConfig, error) {
	return s.dataStore.ListKmsConfigByAccountID(ctx, accountID)
}

func (s *PersistenceStore) GetSvmsByKmsConfigID(ctx context.Context, kmsConfigID int64) ([]*datamodel.Svm, error) {
	return s.dataStore.GetSvmsByKmsConfigID(ctx, kmsConfigID)
}

func (s *PersistenceStore) GetSvmByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.Svm, error) {
	return s.dataStore.GetSvmByNameAndPoolID(ctx, name, poolID)
}

func (s *PersistenceStore) GetSvmByExternalUUID(ctx context.Context, externalUUID string, poolID int64) (*datamodel.Svm, error) {
	return s.dataStore.GetSvmByExternalUUID(ctx, externalUUID, poolID)
}

func (s *PersistenceStore) GetSvmByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (*datamodel.Svm, error) {
	return s.dataStore.GetSvmByExternalIdentifier(ctx, externalIdentifier, accountID)
}

func (s *PersistenceStore) SvmExistsByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (bool, error) {
	return s.dataStore.SvmExistsByExternalIdentifier(ctx, externalIdentifier, accountID)
}

func (s *PersistenceStore) CreateKmsConfig(ctx context.Context, kmsConfigParams *datamodel.KmsConfig) (*datamodel.KmsConfig, error) {
	return s.dataStore.CreateKmsConfig(ctx, kmsConfigParams)
}

func (s *PersistenceStore) DeleteKmsConfig(ctx context.Context, kmsConfigUUID, state, stateDetails string) (*datamodel.KmsConfig, error) {
	return s.dataStore.DeleteKmsConfig(ctx, kmsConfigUUID, state, stateDetails)
}

func (s *PersistenceStore) GetKmsConfigByUUID(ctx context.Context, uuid string) (*datamodel.KmsConfig, error) {
	return s.dataStore.GetKmsConfigByUUID(ctx, uuid)
}

func (s *PersistenceStore) UpdateKmsConfigAttributes(ctx context.Context, uuid string, attributes *datamodel.KmsAttributes) (*datamodel.KmsConfig, error) {
	return s.dataStore.UpdateKmsConfigAttributes(ctx, uuid, attributes)
}

func (s *PersistenceStore) GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*datamodel.Job, error) {
	return s.dataStore.GetJobByResourceUUID(ctx, resourceUUID, jobType)
}

func (s *PersistenceStore) ListOngoingPoolJobsWithKmsConfigId(ctx context.Context, kmsId, accountId int64) ([]*datamodel.Job, error) {
	return s.dataStore.ListOngoingPoolJobsWithKmsConfigId(ctx, kmsId, accountId)
}

func (s *PersistenceStore) UpdateKmsConfigStateForHandleResource(ctx context.Context, kmsConfigUUID string, stateDetails string, event string) (*datamodel.KmsConfig, error) {
	return s.dataStore.UpdateKmsConfigStateForHandleResource(ctx, kmsConfigUUID, stateDetails, event)
}

func (s *PersistenceStore) UpdateKmsConfigDetails(ctx context.Context, uuid string, keyFullPath string, resourceID string) (*datamodel.KmsConfig, error) {
	return s.dataStore.UpdateKmsConfigDetails(ctx, uuid, keyFullPath, resourceID)
}

func (s *PersistenceStore) GetKmsConfigByKeyFullPath(ctx context.Context, keyFullPath string, accountID int64) (*datamodel.KmsConfig, error) {
	return s.dataStore.GetKmsConfigByKeyFullPath(ctx, keyFullPath, accountID)
}

func (s *PersistenceStore) CreateKmsServiceAccount(ctx context.Context, serviceAccount *datamodel.ServiceAccount) (*datamodel.ServiceAccount, error) {
	return s.dataStore.CreateKmsServiceAccount(ctx, serviceAccount)
}

func (s *PersistenceStore) UpdateServiceAccountEmailAndKey(ctx context.Context, uuid string, email string, key string) (*datamodel.ServiceAccount, error) {
	return s.dataStore.UpdateServiceAccountEmailAndKey(ctx, uuid, email, key)
}

func (s *PersistenceStore) UpdateServiceAccountState(ctx context.Context, uuid string, state string, stateDetails string) (*datamodel.ServiceAccount, error) {
	return s.dataStore.UpdateServiceAccountState(ctx, uuid, state, stateDetails)
}

func (s *PersistenceStore) GetServiceAccountFromEmail(ctx context.Context, email string) (*datamodel.ServiceAccount, error) {
	return s.dataStore.GetServiceAccountFromEmail(ctx, email)
}

func (s *PersistenceStore) ListKmsServiceAccounts(ctx context.Context, filter *dbutils.Filter) ([]*datamodel.ServiceAccount, error) {
	return s.dataStore.ListKmsServiceAccounts(ctx, filter)
}

func (s *PersistenceStore) GetBackupVaultByNameAndOwnerID(ctx context.Context, backupVaultId string, account_id string) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVaultByNameAndOwnerID(ctx, backupVaultId, account_id)
}

func (s *PersistenceStore) GetBackupVaultByCrossRegionBackupVaultName(ctx context.Context, crossRegionBackupVaultName string, accountID int64) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVaultByCrossRegionBackupVaultName(ctx, crossRegionBackupVaultName, accountID)
}

func (s *PersistenceStore) CreatingBackupVault(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	return s.dataStore.CreatingBackupVault(ctx, bv)
}

func (s *PersistenceStore) ListBackupVaults(ctx context.Context, accountID int64) ([]*datamodel.BackupVault, error) {
	return s.dataStore.ListBackupVaults(ctx, accountID)
}

func (s *PersistenceStore) UpdateBackupVaultInVCP(ctx context.Context, backupVault *datamodel.BackupVault, vcpVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	return s.dataStore.UpdateBackupVaultInVCP(ctx, backupVault, vcpVault)
}

func (s *PersistenceStore) UpdateBackupVaultFields(ctx context.Context, backupVaultUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateBackupVaultFields(ctx, backupVaultUUID, updates)
}

func (s *PersistenceStore) DeleteBackupVaultInVCP(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error) {
	return s.dataStore.DeleteBackupVaultInVCP(ctx, backupVaultId)
}

func (s *PersistenceStore) UpdateBackupVaultState(ctx context.Context, bv *datamodel.BackupVault, state, stateDetails string) (*datamodel.BackupVault, error) {
	return s.dataStore.UpdateBackupVaultState(ctx, bv, state, stateDetails)
}

func (s *PersistenceStore) CreateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	return s.dataStore.CreateBackup(ctx, backup)
}

func (s *PersistenceStore) DeleteBackup(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	return s.dataStore.DeleteBackup(ctx, backupUUID)
}

func (s *PersistenceStore) UpdateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	return s.dataStore.UpdateBackup(ctx, backup)
}

func (s *PersistenceStore) FinishBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	return s.dataStore.FinishBackup(ctx, backup)
}

func (s *PersistenceStore) UpdateBackupState(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	return s.dataStore.UpdateBackupState(ctx, backup)
}

func (s *PersistenceStore) GetBackupVault(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVault(ctx, backupVaultId)
}

func (s *PersistenceStore) GetBackupVaultById(ctx context.Context, backupVaultId int64) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVaultById(ctx, backupVaultId)
}

func (s *PersistenceStore) IsBackupInCreatingorDeletingStateByVolume(ctx context.Context, volumeUUID string) (bool, error) {
	return s.dataStore.IsBackupInCreatingorDeletingStateByVolume(ctx, volumeUUID)
}

func (s *PersistenceStore) AreBackupsInProgressForVolume(ctx context.Context, volumeUUID string, excludeBackupUUIDs []string, createdBefore *time.Time) (bool, error) {
	return s.dataStore.AreBackupsInProgressForVolume(ctx, volumeUUID, excludeBackupUUIDs, createdBefore)
}

func (s *PersistenceStore) GetEarliestCreatingBackupTime(ctx context.Context, volumeUUID string) (*time.Time, error) {
	return s.dataStore.GetEarliestCreatingBackupTime(ctx, volumeUUID)
}

func (s *PersistenceStore) GetBackupsByBackupVaultOwnerIDAndFilter(ctx context.Context, backupVaultUUID string, accountID int64, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, backupVaultUUID, accountID, filters)
}

func (s *PersistenceStore) GetBackupsByBackupVaultUUIDAndFilter(ctx context.Context, backupVaultUUID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupsByBackupVaultUUIDAndFilter(ctx, backupVaultUUID, filters)
}

func (s *PersistenceStore) GetBackupCountByBackupVaultID(ctx context.Context, backupVaultID int64) (int64, error) {
	return s.dataStore.GetBackupCountByBackupVaultID(ctx, backupVaultID)
}

func (s *PersistenceStore) GetVolumeCountByBackupVaultID(ctx context.Context, backupVaultUUID string) (int64, error) {
	return s.dataStore.GetVolumeCountByBackupVaultID(ctx, backupVaultUUID)
}

func (s *PersistenceStore) GetVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.Volume, error) {
	return s.dataStore.GetVolumesByBackupVaultID(ctx, backupVaultUUID)
}

func (s *PersistenceStore) GetExpertModeVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetExpertModeVolumesByBackupVaultID(ctx, backupVaultUUID)
}

func (s *PersistenceStore) GetBackup(ctx context.Context, backupVaultUUID string, backupUUID string, accountName string) (*datamodel.Backup, error) {
	return s.dataStore.GetBackup(ctx, backupVaultUUID, backupUUID, accountName)
}

func (s *PersistenceStore) GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error) {
	return s.dataStore.GetBackupByExternalUUID(ctx, backupVaultUUID, externalUUID, accountName)
}

func (s *PersistenceStore) GetBackupVaultByUUIDndOwnerID(ctx context.Context, backupVaultUUID string, accountID int64) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVaultByUUIDndOwnerID(ctx, backupVaultUUID, accountID)
}

func (s *PersistenceStore) RestoreDeletedBackupVault(ctx context.Context, backupVaultUUID string, accountID int64, state, stateDetails string) (*datamodel.BackupVault, error) {
	return s.dataStore.RestoreDeletedBackupVault(ctx, backupVaultUUID, accountID, state, stateDetails)
}

func (s *PersistenceStore) GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, accountID int64) (*datamodel.BackupVault, error) {
	return s.dataStore.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, accountID)
}

func (s *PersistenceStore) CreateBackupVaultEntryInVCP(ctx context.Context, backupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	return s.dataStore.CreateBackupVaultEntryInVCP(ctx, backupVault)
}

func (s *PersistenceStore) UpdateBackupVaultBucketDetails(ctx context.Context, backupVault *datamodel.BackupVault) error {
	return s.dataStore.UpdateBackupVaultBucketDetails(ctx, backupVault)
}

func (s *PersistenceStore) IsLatestBackup(ctx context.Context, backupUUID, volumeUUID string) (bool, error) {
	return s.dataStore.IsLatestBackup(ctx, backupUUID, volumeUUID)
}

func (s *PersistenceStore) IsLatestBackupAnyState(ctx context.Context, backupUUID, volumeUUID string) (bool, error) {
	return s.dataStore.IsLatestBackupAnyState(ctx, backupUUID, volumeUUID)
}

func (s *PersistenceStore) IsLatestBackupInVault(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64) (bool, error) {
	return s.dataStore.IsLatestBackupInVault(ctx, backupUUID, volumeUUID, backupVaultID)
}

func (s *PersistenceStore) IsLatestBackupInVaultAndInEndpoint(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64, endpointUUID string) (bool, error) {
	return s.dataStore.IsLatestBackupInVaultAndInEndpoint(ctx, backupUUID, volumeUUID, backupVaultID, endpointUUID)
}

func (s *PersistenceStore) BackupCountByVolumeID(ctx context.Context, volumeUUID string) (int64, error) {
	return s.dataStore.BackupCountByVolumeID(ctx, volumeUUID)
}

func (s *PersistenceStore) BackupCountByVolumeIDVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error) {
	return s.dataStore.BackupCountByVolumeIDVaultAndEndpoint(ctx, volumeUUID, backupVaultID, endpointUUID)
}

func (s *PersistenceStore) CreateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error) {
	return s.dataStore.CreateBackupMetadata(ctx, backupMetadata)
}

func (s *PersistenceStore) DeleteBackupMetadata(ctx context.Context, volumeUUID string) error {
	return s.dataStore.DeleteBackupMetadata(ctx, volumeUUID)
}

func (s *PersistenceStore) GetBackupMetadataByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.BackupMetadata, error) {
	return s.dataStore.GetBackupMetadataByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) UpdateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error) {
	return s.dataStore.UpdateBackupMetadata(ctx, backupMetadata)
}

func (s *PersistenceStore) CreateSfrMetadata(ctx context.Context, sfrMetadata *datamodel.SfrMetadata) (*datamodel.SfrMetadata, error) {
	return s.dataStore.CreateSfrMetadata(ctx, sfrMetadata)
}

func (s *PersistenceStore) GetSfrMetricsByTimeRange(ctx context.Context, startTime, endTime time.Time) (map[string]datamodel.SfrMetricsAggregate, error) {
	return s.dataStore.GetSfrMetricsByTimeRange(ctx, startTime, endTime)
}

func (s *PersistenceStore) GetSfrMetadataByJobID(ctx context.Context, jobID int64) (*datamodel.SfrMetadata, error) {
	return s.dataStore.GetSfrMetadataByJobID(ctx, jobID)
}

func (s *PersistenceStore) GetBackupWithVaultByUUID(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	return s.dataStore.GetBackupWithVaultByUUID(ctx, backupUUID)
}

func (s *PersistenceStore) CreateAdminJobSpec(ctx context.Context, spec *datamodel.AdminJobSpec) (*datamodel.AdminJobSpec, error) {
	return s.dataStore.CreateAdminJobSpec(ctx, spec)
}

func (s *PersistenceStore) CreateAdminJobSpecIfNotExists(ctx context.Context, spec *datamodel.AdminJobSpec) (*datamodel.AdminJobSpec, error) {
	return s.dataStore.CreateAdminJobSpecIfNotExists(ctx, spec)
}

func (s *PersistenceStore) GetAdminJobSpecByJobType(ctx context.Context, jobType string) (*datamodel.AdminJobSpec, error) {
	return s.dataStore.GetAdminJobSpecByJobType(ctx, jobType)
}

func (s *PersistenceStore) UpdateAdminJobSpec(ctx context.Context, jobSpec *datamodel.AdminJobSpec) error {
	return s.dataStore.UpdateAdminJobSpec(ctx, jobSpec)
}

func (s *PersistenceStore) GetAdminJobSpecsByState(ctx context.Context, state string) ([]*datamodel.AdminJobSpec, error) {
	return s.dataStore.GetAdminJobSpecsByState(ctx, state)
}

func (s *PersistenceStore) UpdateAdminJobSpecWithLock(ctx context.Context, jobType, state string, lockThreshold, currentTime time.Time) (int64, error) {
	return s.dataStore.UpdateAdminJobSpecWithLock(ctx, jobType, state, lockThreshold, currentTime)
}

func (s *PersistenceStore) ErroredResource(ctx context.Context, resource interface{}, errMessage string) (interface{}, error) {
	return s.dataStore.ErroredResource(ctx, resource, errMessage)
}

func (s *PersistenceStore) CreateNodeNodeGroupMap(ctx context.Context, m *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.CreateNodeNodeGroupMap(ctx, m)
}

func (s *PersistenceStore) GetNodeNodeGroupMap(ctx context.Context, id int64) (*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.GetNodeNodeGroupMap(ctx, id)
}

func (s *PersistenceStore) UpdateNodeNodeGroupMap(ctx context.Context, m *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.UpdateNodeNodeGroupMap(ctx, m)
}

func (s *PersistenceStore) AssignTwoNodesToTwoGroups(ctx context.Context, params datamodel.NodeGroupAssignmentParams) ([]*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.AssignTwoNodesToTwoGroups(ctx, params)
}

func (s *PersistenceStore) DeleteNodeNodeGroupMap(ctx context.Context, id int64) error {
	return s.dataStore.DeleteNodeNodeGroupMap(ctx, id)
}

func (s *PersistenceStore) GetAllVolumesForHG(ctx context.Context, hostGroupUUID string, accountID int64) ([]*datamodel.Volume, error) {
	return s.dataStore.GetAllVolumesForHG(ctx, hostGroupUUID, accountID)
}

func (s *PersistenceStore) UpdateHostGroup(ctx context.Context, hostGroupUUID string, accountID int64, description *string, Hosts *[]string) (*datamodel.HostGroup, error) {
	return s.dataStore.UpdateHostGroup(ctx, hostGroupUUID, accountID, description, Hosts)
}

func (s *PersistenceStore) ListHostGroupsByAccountID(ctx context.Context, accountID int64) ([]*datamodel.HostGroup, error) {
	return s.dataStore.ListHostGroupsByAccountID(ctx, accountID)
}

func (s *PersistenceStore) UpdateHostGroupsStateForHandleResource(ctx context.Context, hostGroupUUID string, accountID int64, state, stateDetails string) error {
	return s.dataStore.UpdateHostGroupsStateForHandleResource(ctx, hostGroupUUID, accountID, state, stateDetails)
}

func (s *PersistenceStore) GetBackupPolicyByUUIDAndOwnerID(ctx context.Context, backupPolicyUUID string, accountID int64) (*datamodel.BackupPolicy, error) {
	return s.dataStore.GetBackupPolicyByUUIDAndOwnerID(ctx, backupPolicyUUID, accountID)
}

func (s *PersistenceStore) GetBackupPolicyByNameAndOwnerID(ctx context.Context, backupPolicyName string, accountID int64) (*datamodel.BackupPolicy, error) {
	return s.dataStore.GetBackupPolicyByNameAndOwnerID(ctx, backupPolicyName, accountID)
}

func (s *PersistenceStore) GetBackupPolicyUUIDsFromBackupVaultUUID(ctx context.Context, backupVaultUUID string, accountID int64) ([]string, error) {
	return s.dataStore.GetBackupPolicyUUIDsFromBackupVaultUUID(ctx, backupVaultUUID, accountID)
}

func (s *PersistenceStore) GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountID int64) ([]string, error) {
	return s.dataStore.GetBackupVaultUUIDsFromBackupPolicyUUID(ctx, backupPolicyUUID, accountID)
}

func (s *PersistenceStore) GetCmekRotationJobStatuses(ctx context.Context, startTime, endTime time.Time, limit, offset int) ([]*CmekRotationJobStatus, error) {
	return s.dataStore.GetCmekRotationJobStatuses(ctx, startTime, endTime, limit, offset)
}

func (s *PersistenceStore) GetVolumeCountByBackupPolicyID(ctx context.Context, backupPolicyUUID string) (int64, error) {
	return s.dataStore.GetVolumeCountByBackupPolicyID(ctx, backupPolicyUUID)
}

func (s *PersistenceStore) ListBackupPolicyVolumeCount(ctx context.Context, conditions [][]interface{}) (map[string]int64, error) {
	return s.dataStore.ListBackupPolicyVolumeCount(ctx, conditions)
}

func (s *PersistenceStore) ListBackupPolicies(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupPolicy, error) {
	return s.dataStore.ListBackupPolicies(ctx, conditions)
}

func (s *PersistenceStore) ListBackupPoliciesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupPolicy, error) {
	return s.dataStore.ListBackupPoliciesWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) CreateBackupPolicyEntryInVCP(ctx context.Context, backupPolicy *datamodel.BackupPolicy) (*datamodel.BackupPolicy, error) {
	return s.dataStore.CreateBackupPolicyEntryInVCP(ctx, backupPolicy)
}

func (s *PersistenceStore) DeleteBackupPolicy(ctx context.Context, backupPolicyUUID string) (*datamodel.BackupPolicy, error) {
	return s.dataStore.DeleteBackupPolicy(ctx, backupPolicyUUID)
}

func (s *PersistenceStore) FetchScheduledBackupsForDeletion(ctx context.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy, isExpertMode bool) ([]*datamodel.Backup, error) {
	return s.dataStore.FetchScheduledBackupsForDeletion(ctx, volume, backupPolicy, isExpertMode)
}

func (s *PersistenceStore) IsBackupShared(ctx context.Context, backup *datamodel.Backup) (bool, error) {
	return s.dataStore.IsBackupShared(ctx, backup)
}

func (s *PersistenceStore) GetBackupByNameAndBackupVaultID(ctx context.Context, backupName string, backupVaultID int64) (*datamodel.Backup, error) {
	return s.dataStore.GetBackupByNameAndBackupVaultID(ctx, backupName, backupVaultID)
}

func (s *PersistenceStore) GetMultipleBackupVaults(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupVault, error) {
	return s.dataStore.GetMultipleBackupVaults(ctx, conditions)
}

func (s *PersistenceStore) CreateNodeGroup(ctx context.Context, group *datamodel.NodeGroup) (*datamodel.NodeGroup, error) {
	return s.dataStore.CreateNodeGroup(ctx, group)
}

func (s *PersistenceStore) GetNodeGroup(ctx context.Context, id int64) (*datamodel.NodeGroup, error) {
	return s.dataStore.GetNodeGroup(ctx, id)
}

func (s *PersistenceStore) UpdateNodeGroup(ctx context.Context, group *datamodel.NodeGroup) (*datamodel.NodeGroup, error) {
	return s.dataStore.UpdateNodeGroup(ctx, group)
}

func (s *PersistenceStore) DeleteNodeGroup(ctx context.Context, id int64) error {
	return s.dataStore.DeleteNodeGroup(ctx, id)
}

func (s *PersistenceStore) DeleteNodeGroupMap(ctx context.Context, nodeGroupMap *datamodel.NodeNodeGroupMap) error {
	return s.dataStore.DeleteNodeGroupMap(ctx, nodeGroupMap)
}

func (s *PersistenceStore) GetNodeGroupMapNodeCount(ctx context.Context, nodeGroupID int64) (int64, error) {
	return s.dataStore.GetNodeGroupMapNodeCount(ctx, nodeGroupID)
}

func (s *PersistenceStore) GetNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64) (*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.GetNodeNodeGroupMapByNodeID(ctx, nodeID)
}

func (s *PersistenceStore) GetActiveNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64, tx dbutils.Transaction) (*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.GetActiveNodeNodeGroupMapByNodeID(ctx, nodeID, tx)
}

func (s *PersistenceStore) UpdateBackupPolicy(ctx context.Context, uuid string, updates map[string]interface{}) (*datamodel.BackupPolicy, error) {
	return s.dataStore.UpdateBackupPolicy(ctx, uuid, updates)
}

func (s *PersistenceStore) GetBackupCountByVolumeUUIDs(ctx context.Context, volumeUUIDs []string, conditions [][]interface{}) (map[string]int64, error) {
	return s.dataStore.GetBackupCountByVolumeUUIDs(ctx, volumeUUIDs, conditions)
}

func (s *PersistenceStore) GetBackupCountByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (int64, error) {
	return s.dataStore.GetBackupCountByVolumeAndVault(ctx, volumeUUID, backupVaultID)
}

func (s *PersistenceStore) GetBackupCountByVolumeVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error) {
	return s.dataStore.GetBackupCountByVolumeVaultAndEndpoint(ctx, volumeUUID, backupVaultID, endpointUUID)
}

func (s *PersistenceStore) GetDistinctBackupVaultIDsByVolumeUUID(ctx context.Context, volumeUUID string) ([]int64, error) {
	return s.dataStore.GetDistinctBackupVaultIDsByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) GetDistinctBackupVaultServiceTypesByVaultIDs(ctx context.Context, backupVaultIDs []int64) ([]string, error) {
	return s.dataStore.GetDistinctBackupVaultServiceTypesByVaultIDs(ctx, backupVaultIDs)
}

func (s *PersistenceStore) GetBackupsByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupsByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, excludeBackupUUID string) error {
	return s.dataStore.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volumeUUID, excludeBackupUUID)
}

func (s *PersistenceStore) GetLatestBackupByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.Backup, error) {
	return s.dataStore.GetLatestBackupByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) GetLatestBackupByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (*datamodel.Backup, error) {
	return s.dataStore.GetLatestBackupByVolumeAndVault(ctx, volumeUUID, backupVaultID)
}

func (s *PersistenceStore) GetLatestBackupsPerVaultByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	return s.dataStore.GetLatestBackupsPerVaultByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) GetLatestBackupsPerEndpointByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	return s.dataStore.GetLatestBackupsPerEndpointByVolumeUUID(ctx, volumeUUID)
}

func (s *PersistenceStore) SumVolumeBackupChainBytes(ctx context.Context, volumeUUID string) (int64, error) {
	return s.dataStore.SumVolumeBackupChainBytes(ctx, volumeUUID)
}

func (s *PersistenceStore) GetMostRecentlyDeletedBackupForEndpoint(ctx context.Context, volumeUUID, endpointUUID string, since time.Time) (*datamodel.Backup, error) {
	return s.dataStore.GetMostRecentlyDeletedBackupForEndpoint(ctx, volumeUUID, endpointUUID, since)
}

func (s *PersistenceStore) DecrementIntermediateBackupChainSizes(ctx context.Context, volumeUUID, endpointUUID string, pivotID, excludeLatestID, delta int64) (int64, error) {
	return s.dataStore.DecrementIntermediateBackupChainSizes(ctx, volumeUUID, endpointUUID, pivotID, excludeLatestID, delta)
}

func (s *PersistenceStore) UpdateLatestBackupLogicalSize(ctx context.Context, volumeUUID string, endpointUUID string, newLogicalSize int64) error {
	return s.dataStore.UpdateLatestBackupLogicalSize(ctx, volumeUUID, endpointUUID, newLogicalSize)
}

func (s *PersistenceStore) UpdateBackupChainHistory(ctx context.Context, volumeUUID string, endpointUUID string, newSize int64) error {
	return s.dataStore.UpdateBackupChainHistory(ctx, volumeUUID, endpointUUID, newSize)
}

func (s *PersistenceStore) DeleteBackupChainHistoryOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	return s.dataStore.DeleteBackupChainHistoryOlderThan(ctx, olderThan)
}

func (s *PersistenceStore) GetNextSerialNumber(ctx context.Context) (int64, error) {
	return s.dataStore.GetNextSerialNumber(ctx)
}

func (s *PersistenceStore) ListTpProjects(ctx context.Context) ([]string, error) {
	return s.dataStore.ListTpProjects(ctx)
}

func (s *PersistenceStore) ListAllTpProjects(ctx context.Context) ([]string, error) {
	return s.dataStore.ListAllTpProjects(ctx)
}

func (s *PersistenceStore) GetSoftDeleteAccount(ctx context.Context, name string) (*datamodel.Account, error) {
	return s.dataStore.GetSoftDeleteAccount(ctx, name)
}

func (s *PersistenceStore) GetDeletedAccounts(ctx context.Context) ([]*datamodel.Account, error) {
	return s.dataStore.GetDeletedAccounts(ctx)
}

func (s *PersistenceStore) HardDeleteResourceByTable(ctx context.Context, table string, query string, id int64) error {
	return s.dataStore.HardDeleteResourceByTable(ctx, table, query, id)
}

func (s *PersistenceStore) DeleteAccount(ctx context.Context, accountID int64) error {
	return s.dataStore.DeleteAccount(ctx, accountID)
}

func (s *PersistenceStore) ListSvmsWithAccountId(ctx context.Context, accountId int64) ([]*datamodel.Svm, error) {
	return s.dataStore.ListSvmsWithAccountId(ctx, accountId)
}

func (s *PersistenceStore) ListSvmsForHardwareClusters(ctx context.Context) ([]*datamodel.SvmView, error) {
	return s.dataStore.ListSvmsForHardwareClusters(ctx)
}

func (s *PersistenceStore) RollBackDeletedAccount(ctx context.Context, accountID int64) error {
	return s.dataStore.RollBackDeletedAccount(ctx, accountID)
}

func (s *PersistenceStore) GetBackupMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupMetrics(ctx, conditions, pagination)
}

func (s *PersistenceStore) GetBackupChainMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupChainMetrics(ctx, conditions, pagination)
}

func (s *PersistenceStore) GetDistinctVolumeGCBDRVaultPairs(ctx context.Context) ([]VolumeVaultPair, error) {
	return s.dataStore.GetDistinctVolumeGCBDRVaultPairs(ctx)
}

func (s *PersistenceStore) GetBackupResourceDataForAggregation(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination, freeTrialAccounts map[int64]*time.Time) ([]*datamodel.Backup, error) {
	return s.dataStore.GetBackupResourceDataForAggregation(ctx, conditions, pagination, freeTrialAccounts)
}

func (s *PersistenceStore) GetBackupMetadata(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupMetadata, error) {
	return s.dataStore.GetBackupMetadata(ctx, conditions, pagination)
}

func (s *PersistenceStore) ListBackupChainHistoriesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupChainHistory, error) {
	return s.dataStore.ListBackupChainHistoriesWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) ListVolumesWithAccounts(ctx context.Context) ([]*datamodel.Volume, error) {
	return s.dataStore.ListVolumesWithAccounts(ctx)
}

func (s *PersistenceStore) ListVolumesForTelemetryMetrics(ctx context.Context) ([]*VolumeMetricsData, error) {
	return s.dataStore.ListVolumesForTelemetryMetrics(ctx)
}

func (s *PersistenceStore) ListExpertModeVolumesForTelemetryMetrics(ctx context.Context, pagination *dbutils.Pagination) ([]*ExpertModeVolumeMetricsData, error) {
	return s.dataStore.ListExpertModeVolumesForTelemetryMetrics(ctx, pagination)
}

func (s *PersistenceStore) UpdateBackupFields(ctx context.Context, backupUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateBackupFields(ctx, backupUUID, updates)
}

func (s *PersistenceStore) GetLatestBackupsGroupedByVolumeUUID(ctx context.Context) ([]datamodel.Backup, error) {
	return s.dataStore.GetLatestBackupsGroupedByVolumeUUID(ctx)
}

func (s *PersistenceStore) GetAccounts(ctx context.Context, includeDelete bool, pagination *dbutils.Pagination) ([]*datamodel.Account, error) {
	return s.dataStore.GetAccounts(ctx, includeDelete, pagination)
}

func (s *PersistenceStore) ListAccountsForTelemetry(ctx context.Context, pagination *dbutils.Pagination) ([]*AccountTelemetryData, error) {
	return s.dataStore.ListAccountsForTelemetry(ctx, pagination)
}

func (s *PersistenceStore) ListFreeTrialAccountsForBilling(ctx context.Context) (map[int64]*time.Time, error) {
	return s.dataStore.ListFreeTrialAccountsForBilling(ctx)
}

func (s *PersistenceStore) CreatePendingResourceDeletion(ctx context.Context, resourceType, resourceName, errorMessage, accountName string, poolID int64) (*datamodel.PendingResourceDeletions, error) {
	return s.dataStore.CreatePendingResourceDeletion(ctx, resourceType, resourceName, errorMessage, accountName, poolID)
}

func (s *PersistenceStore) ListPendingResourceDeletions(ctx context.Context, offset, limit int) ([]*datamodel.PendingResourceDeletions, error) {
	return s.dataStore.ListPendingResourceDeletions(ctx, offset, limit)
}

func (s *PersistenceStore) UpdatePendingResourceDeletion(ctx context.Context, resourceID int64, isDeletion bool, errorMessage string) (*datamodel.PendingResourceDeletions, error) {
	return s.dataStore.UpdatePendingResourceDeletion(ctx, resourceID, isDeletion, errorMessage)
}

func (s *PersistenceStore) GetResourcesCount(ctx context.Context) (int64, error) {
	return s.dataStore.GetResourcesCount(ctx)
}

func (s *PersistenceStore) DeleteServiceAccount(ctx context.Context, serviceAccount *datamodel.ServiceAccount) error {
	return s.dataStore.DeleteServiceAccount(ctx, serviceAccount)
}

func (s *PersistenceStore) GetEligibleVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return s.dataStore.GetEligibleVolumes(ctx, conditions, pagination)
}

func (s *PersistenceStore) UpdateBackupConstituentCountFromVolume(ctx context.Context, backup *datamodel.Backup, volume *datamodel.Volume) (*datamodel.Backup, error) {
	return s.dataStore.UpdateBackupConstituentCountFromVolume(ctx, backup, volume)
}

func (s *PersistenceStore) CreateActiveDirectory(ctx context.Context, ad *datamodel.ActiveDirectory) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.CreateActiveDirectory(ctx, ad)
}

func (s *PersistenceStore) UpdateActiveDirectory(ctx context.Context, ad *datamodel.ActiveDirectory) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.UpdateActiveDirectory(ctx, ad)
}

func (s *PersistenceStore) GetActiveDirectoryByNameAndAccountID(ctx context.Context, name string, accountID int64) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.GetActiveDirectoryByNameAndAccountID(ctx, name, accountID)
}

func (s *PersistenceStore) GetActiveDirectoryByUuidAndAccountId(ctx context.Context, uuid string, accountID int64) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.GetActiveDirectoryByUuidAndAccountId(ctx, uuid, accountID)
}

// Image version methods
func (s *PersistenceStore) CreateImageVersion(ctx context.Context, imageVersion *datamodel.ImageVersion) (*datamodel.ImageVersion, error) {
	return s.dataStore.CreateImageVersion(ctx, imageVersion)
}

func (s *PersistenceStore) GetImageVersionByOntapVersion(ctx context.Context, ontapVersion string) (*datamodel.ImageVersion, error) {
	return s.dataStore.GetImageVersionByOntapVersion(ctx, ontapVersion)
}

func (s *PersistenceStore) ListImageVersions(ctx context.Context, activeOnly bool) ([]*datamodel.ImageVersion, error) {
	return s.dataStore.ListImageVersions(ctx, activeOnly)
}

func (s *PersistenceStore) UpdateImageVersion(ctx context.Context, imageVersion *datamodel.ImageVersion) error {
	return s.dataStore.UpdateImageVersion(ctx, imageVersion)
}

func (s *PersistenceStore) DeleteImageVersion(ctx context.Context, ontapVersion string) error {
	return s.dataStore.DeleteImageVersion(ctx, ontapVersion)
}

// CreateClusterUpgradeJob creates a new cluster upgrade job in the database
func (s *PersistenceStore) CreateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) (*datamodel.ClusterUpgradeJob, error) {
	return s.dataStore.CreateClusterUpgradeJob(ctx, upgradeJob)
}

// GetClusterUpgradeJobByUUID retrieves a cluster upgrade job by its UUID
func (s *PersistenceStore) GetClusterUpgradeJobByUUID(ctx context.Context, jobUUID string) (*datamodel.ClusterUpgradeJob, error) {
	return s.dataStore.GetClusterUpgradeJobByUUID(ctx, jobUUID)
}

// GetClusterUpgradeJobsByClusterID retrieves all cluster upgrade jobs for a given cluster ID
func (s *PersistenceStore) GetClusterUpgradeJobsByClusterID(ctx context.Context, clusterID string) ([]*datamodel.ClusterUpgradeJob, error) {
	return s.dataStore.GetClusterUpgradeJobsByClusterID(ctx, clusterID)
}

// UpdateClusterUpgradeJob updates an existing cluster upgrade job
func (s *PersistenceStore) UpdateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) error {
	return s.dataStore.UpdateClusterUpgradeJob(ctx, upgradeJob)
}

// CreateBatchClusterUpgradeJob creates a new batch cluster upgrade job
func (s *PersistenceStore) CreateBatchClusterUpgradeJob(ctx context.Context, batchJob *datamodel.BatchClusterUpgradeJob) (*datamodel.BatchClusterUpgradeJob, error) {
	return s.dataStore.CreateBatchClusterUpgradeJob(ctx, batchJob)
}

// GetBatchClusterUpgradeJobByUUID retrieves a batch cluster upgrade job by UUID
func (s *PersistenceStore) GetBatchClusterUpgradeJobByUUID(ctx context.Context, jobUUID string) (*datamodel.BatchClusterUpgradeJob, error) {
	return s.dataStore.GetBatchClusterUpgradeJobByUUID(ctx, jobUUID)
}

// UpdateBatchClusterUpgradeJob updates an existing batch cluster upgrade job
func (s *PersistenceStore) UpdateBatchClusterUpgradeJob(ctx context.Context, batchJob *datamodel.BatchClusterUpgradeJob) error {
	return s.dataStore.UpdateBatchClusterUpgradeJob(ctx, batchJob)
}

// GetClusterUpgradeJobsByBatchID retrieves all cluster upgrade jobs belonging to a batch
func (s *PersistenceStore) GetClusterUpgradeJobsByBatchID(ctx context.Context, batchUpgradeID string) ([]*datamodel.ClusterUpgradeJob, error) {
	return s.dataStore.GetClusterUpgradeJobsByBatchID(ctx, batchUpgradeID)
}

func (s *PersistenceStore) CheckAndFetchDuplicateJobs(ctx context.Context, jobType string, correlationID string) (*datamodel.Job, error) {
	return s.dataStore.CheckAndFetchDuplicateJobs(ctx, jobType, correlationID)
}

func (s *PersistenceStore) CancelRunningJobsForResource(ctx context.Context, resourceUUID string) error {
	return s.dataStore.CancelRunningJobsForResource(ctx, resourceUUID)
}

// Cluster Peering methods
func (s *PersistenceStore) GetClusterPeerByAccountIDExternalClusterAndPoolID(ctx context.Context, accountID int64, externalCluster string, poolID int64) (*datamodel.ClusterPeerings, error) {
	return s.dataStore.GetClusterPeerByAccountIDExternalClusterAndPoolID(ctx, accountID, externalCluster, poolID)
}

func (s *PersistenceStore) GetClusterPeeringRowByID(ctx context.Context, clusterPeerID int64) (*datamodel.ClusterPeerings, error) {
	return s.dataStore.GetClusterPeeringRowByID(ctx, clusterPeerID)
}

func (s *PersistenceStore) CreateClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) (*datamodel.ClusterPeerings, error) {
	return s.dataStore.CreateClusterPeeringRow(ctx, clusterPeeringRow)
}

func (s *PersistenceStore) UpdateClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) error {
	return s.dataStore.UpdateClusterPeeringRow(ctx, clusterPeeringRow)
}

func (s *PersistenceStore) ListClusterPeeringRowsByAccountID(ctx context.Context, accountID int64) ([]*datamodel.ClusterPeerings, error) {
	return s.dataStore.ListClusterPeeringRowsByAccountID(ctx, accountID)
}

func (s *PersistenceStore) ListNodeNodeGroupMap(ctx context.Context, includeDeleted bool, pagination *dbutils.Pagination) ([]*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.ListNodeNodeGroupMap(ctx, includeDeleted, pagination)
}

func (s *PersistenceStore) ListNodeNodeGroupMapAfterID(ctx context.Context, includeDeleted bool, afterID int64, limit int) ([]*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.ListNodeNodeGroupMapAfterID(ctx, includeDeleted, afterID, limit)
}

func (s *PersistenceStore) GetActiveDirectoryByUUID(ctx context.Context, uuid string) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.GetActiveDirectoryByUUID(ctx, uuid)
}

func (s *PersistenceStore) ListActiveDirectories(ctx context.Context, accountID int64) ([]*datamodel.ActiveDirectory, error) {
	return s.dataStore.ListActiveDirectories(ctx, accountID)
}

func (s *PersistenceStore) GetMultipleActiveDirectoriesByUUIDs(ctx context.Context, uuids []string) ([]*datamodel.ActiveDirectory, error) {
	return s.dataStore.GetMultipleActiveDirectoriesByUUIDs(ctx, uuids)
}

func (s *PersistenceStore) DeleteClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) error {
	return s.dataStore.DeleteClusterPeeringRow(ctx, clusterPeeringRow)
}

func (s *PersistenceStore) DeleteActiveDirectory(ctx context.Context, uuid string) error {
	return s.dataStore.DeleteActiveDirectory(ctx, uuid)
}

func (s *PersistenceStore) GetSVMsUsingActiveDirectory(ctx context.Context, adId int64) ([]*datamodel.Svm, error) {
	return s.dataStore.GetSVMsUsingActiveDirectory(ctx, adId)
}

func (s *PersistenceStore) GetActiveDirectoryForPoolByPoolID(ctx context.Context, poolID int64) (*datamodel.ActiveDirectory, error) {
	return s.dataStore.GetActiveDirectoryForPoolByPoolID(ctx, poolID)
}

func (s *PersistenceStore) ListClusterPeeringRowsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.ClusterPeerings, error) {
	return s.dataStore.ListClusterPeeringRowsByPoolID(ctx, poolID)
}

// Volume Performance Group (Manual QoS) methods
func (s *PersistenceStore) CreateVolumePerformanceGroupWithCap(ctx context.Context, vpg *datamodel.VolumePerformanceGroup, maxCount int) (*datamodel.VolumePerformanceGroup, error) {
	return s.dataStore.CreateVolumePerformanceGroupWithCap(ctx, vpg, maxCount)
}

func (s *PersistenceStore) UpdateVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	return s.dataStore.UpdateVolumePerformanceGroup(ctx, vpg)
}

func (s *PersistenceStore) UpdateVolumePerformanceGroupState(ctx context.Context, uuid, state, stateDetails string) error {
	return s.dataStore.UpdateVolumePerformanceGroupState(ctx, uuid, state, stateDetails)
}

func (s *PersistenceStore) DeleteVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	return s.dataStore.DeleteVolumePerformanceGroup(ctx, vpg)
}

func (s *PersistenceStore) HardDeleteVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	return s.dataStore.HardDeleteVolumePerformanceGroup(ctx, vpg)
}

func (s *PersistenceStore) GetVolumePerformanceGroupByUUID(ctx context.Context, uuid string) (*datamodel.VolumePerformanceGroup, error) {
	return s.dataStore.GetVolumePerformanceGroupByUUID(ctx, uuid)
}

func (s *PersistenceStore) GetVolumePerformanceGroupByID(ctx context.Context, id int64) (*datamodel.VolumePerformanceGroup, error) {
	return s.dataStore.GetVolumePerformanceGroupByID(ctx, id)
}

func (s *PersistenceStore) GetVolumePerformanceGroupByPoolAndName(ctx context.Context, poolID int64, name string) (*datamodel.VolumePerformanceGroup, error) {
	return s.dataStore.GetVolumePerformanceGroupByPoolAndName(ctx, poolID, name)
}

func (s *PersistenceStore) ListVolumePerformanceGroupsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.VolumePerformanceGroup, error) {
	return s.dataStore.ListVolumePerformanceGroupsByPoolID(ctx, poolID)
}

func (s *PersistenceStore) CountVolumePerformanceGroupsByPoolID(ctx context.Context, poolID int64) (int64, error) {
	return s.dataStore.CountVolumePerformanceGroupsByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetVolumeCountByVolumePerformanceGroupID(ctx context.Context, vpgID int64) (int64, error) {
	return s.dataStore.GetVolumeCountByVolumePerformanceGroupID(ctx, vpgID)
}

func (s *PersistenceStore) GetActivePrepopulateJobs(ctx context.Context) ([]*datamodel.Job, error) {
	return s.dataStore.GetActivePrepopulateJobs(ctx)
}

func (s *PersistenceStore) CancelPrepopulateJobsForVolume(ctx context.Context, volumeUUID string) error {
	return s.dataStore.CancelPrepopulateJobsForVolume(ctx, volumeUUID)
}

func (s *PersistenceStore) CreatingQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error) {
	return s.dataStore.CreatingQuotaRule(ctx, quotaRule)
}

func (s *PersistenceStore) UpdatingQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error) {
	return s.dataStore.UpdatingQuotaRule(ctx, quotaRule)
}

func (s *PersistenceStore) UpdateQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error) {
	return s.dataStore.UpdateQuotaRule(ctx, quotaRule)
}

func (s *PersistenceStore) GetQuotaRuleByUUID(ctx context.Context, uuid string, accountID int64) (*datamodel.QuotaRule, error) {
	return s.dataStore.GetQuotaRuleByUUID(ctx, uuid, accountID)
}

func (s *PersistenceStore) GetQuotaRulesByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.QuotaRule, error) {
	return s.dataStore.GetQuotaRulesByVolumeID(ctx, volumeID)
}

func (s *PersistenceStore) GetQuotaRulesWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.QuotaRule, error) {
	return s.dataStore.GetQuotaRulesWithCondition(ctx, filter)
}

func (s *PersistenceStore) GetQuotaRuleCountBySvmID(ctx context.Context, svmID int64) (int64, error) {
	return s.dataStore.GetQuotaRuleCountBySvmID(ctx, svmID)
}

func (s *PersistenceStore) DeleteQuotaRule(ctx context.Context, id string) (*datamodel.QuotaRule, error) {
	return s.dataStore.DeleteQuotaRule(ctx, id)
}

func (s *PersistenceStore) ReplaceDstQuotaRulesWithSrc(ctx context.Context, volumeID int64, accountID int64, dstQuotaRuleUUIDs []string, srcQuotaRules []*datamodel.QuotaRule) ([]*datamodel.QuotaRule, error) {
	return s.dataStore.ReplaceDstQuotaRulesWithSrc(ctx, volumeID, accountID, dstQuotaRuleUUIDs, srcQuotaRules)
}

func (s *PersistenceStore) UpdateExpertModeVolumeDataProtection(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) error {
	return s.dataStore.UpdateExpertModeVolumeDataProtection(ctx, expertModeVolume)
}

func (s *PersistenceStore) DeleteExpertModeVolume(ctx context.Context, volumeUUID string) error {
	return s.dataStore.DeleteExpertModeVolume(ctx, volumeUUID)
}

func (s *PersistenceStore) GetVolumeByJunctionPath(ctx context.Context, junctionPath string, accountID int64, poolId int64) (*datamodel.Volume, error) {
	return s.dataStore.GetVolumeByJunctionPath(ctx, junctionPath, accountID, poolId)
}

func (s *PersistenceStore) AddKeyToServiceAccount(ctx context.Context, serviceAccountUUID string, key datamodel.ServiceAccountKey) error {
	return s.dataStore.AddKeyToServiceAccount(ctx, serviceAccountUUID, key)
}

func (s *PersistenceStore) RemoveKeyFromServiceAccount(ctx context.Context, serviceAccountUUID string, keyID string) error {
	return s.dataStore.RemoveKeyFromServiceAccount(ctx, serviceAccountUUID, keyID)
}

func (s *PersistenceStore) MarkKeyForDeletion(ctx context.Context, serviceAccountUUID string, keyID string) error {
	return s.dataStore.MarkKeyForDeletion(ctx, serviceAccountUUID, keyID)
}

func (s *PersistenceStore) SetPrimaryKeyForServiceAccount(ctx context.Context, serviceAccountUUID string, keyID string) error {
	return s.dataStore.SetPrimaryKeyForServiceAccount(ctx, serviceAccountUUID, keyID)
}

func (s *PersistenceStore) UpdateServiceAccountPasswordLocation(ctx context.Context, serviceAccountUUID string, encryptedKeyData string) error {
	return s.dataStore.UpdateServiceAccountPasswordLocation(ctx, serviceAccountUUID, encryptedKeyData)
}

func (s *PersistenceStore) GetServiceAccountWithKeys(ctx context.Context, serviceAccountUUID string) (*datamodel.ServiceAccount, error) {
	return s.dataStore.GetServiceAccountWithKeys(ctx, serviceAccountUUID)
}

func (s *PersistenceStore) UpdateSvmCurrentKmsKeyID(ctx context.Context, svmUUID string, keyID string) error {
	return s.dataStore.UpdateSvmCurrentKmsKeyID(ctx, svmUUID, keyID)
}

func (s *PersistenceStore) UpdateExpertModeVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error {
	return s.dataStore.UpdateExpertModeVolumeFields(ctx, volumeUUID, updates)
}

func (s *PersistenceStore) ListExpertModeVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.ListExpertModeVolumesByPoolID(ctx, poolID)
}

func (s *PersistenceStore) GetActiveExpertModeVolumesCountByAccountID(ctx context.Context, accountID int64) (int64, error) {
	return s.dataStore.GetActiveExpertModeVolumesCountByAccountID(ctx, accountID)
}

func (s *PersistenceStore) GetEligibleExpertModeVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetEligibleExpertModeVolumes(ctx, conditions, pagination)
}

func (s *PersistenceStore) GetExpertModeBackupsByVolumeExternalUUID(ctx context.Context, volumeExternalUUID string) ([]*datamodel.Backup, error) {
	return s.dataStore.GetExpertModeBackupsByVolumeExternalUUID(ctx, volumeExternalUUID)
}

func (s *PersistenceStore) GetAppConfig(ctx context.Context, key string) (*datamodel.AppConfig, error) {
	return s.dataStore.GetAppConfig(ctx, key)
}

func (s *PersistenceStore) UpsertAppConfig(ctx context.Context, key, value string) error {
	return s.dataStore.UpsertAppConfig(ctx, key, value)
}

func (s *PersistenceStore) GetMultipleVolumesWithExpertMode(ctx context.Context, conditions [][]interface{}) ([]*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.GetMultipleVolumesWithExpertMode(ctx, conditions)
}

func (s *PersistenceStore) ListExpertModeVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error) {
	return s.dataStore.ListExpertModeVolumesWithPagination(ctx, conditions, pagination)
}

func (s *PersistenceStore) CreateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return s.dataStore.CreateAddressRange(ctx, ar)
}

func (s *PersistenceStore) CreateExternalCluster(ctx context.Context, host *datamodel.Cluster) (*datamodel.Cluster, error) {
	return s.dataStore.CreateExternalCluster(ctx, host)
}

func (s *PersistenceStore) GetExternalCluster(ctx context.Context, hostID string) (*datamodel.Cluster, error) {
	return s.dataStore.GetExternalCluster(ctx, hostID)
}

func (s *PersistenceStore) UpdateExternalCluster(ctx context.Context, host *datamodel.Cluster) (*datamodel.Cluster, error) {
	return s.dataStore.UpdateExternalCluster(ctx, host)
}

func (s *PersistenceStore) DeleteExternalCluster(ctx context.Context, hostID string) (*datamodel.Cluster, error) {
	return s.dataStore.DeleteExternalCluster(ctx, hostID)
}

func (s *PersistenceStore) ListAccountHostsByAccount(ctx context.Context, accountID int64) ([]*datamodel.AccountHost, error) {
	return s.dataStore.ListAccountHostsByAccount(ctx, accountID)
}

func (s *PersistenceStore) ListPinnedAccountHostsByAccount(ctx context.Context, accountID int64) ([]*datamodel.AccountHost, error) {
	return s.dataStore.ListPinnedAccountHostsByAccount(ctx, accountID)
}

func (s *PersistenceStore) GetAccountHost(ctx context.Context, accountHostUUID string) (*datamodel.AccountHost, error) {
	return s.dataStore.GetAccountHost(ctx, accountHostUUID)
}

func (s *PersistenceStore) GetAccountHostByAccountAndHost(ctx context.Context, accountID int64, hostUUID string) (*datamodel.AccountHost, error) {
	return s.dataStore.GetAccountHostByAccountAndHost(ctx, accountID, hostUUID)
}

func (s *PersistenceStore) GetAccountHostsByAccountAndHostUUIDs(ctx context.Context, accountID int64, hostUUIDs []string) ([]*datamodel.AccountHost, error) {
	return s.dataStore.GetAccountHostsByAccountAndHostUUIDs(ctx, accountID, hostUUIDs)
}

func (s *PersistenceStore) CreateAccountHost(ctx context.Context, row *datamodel.AccountHost) (*datamodel.AccountHost, error) {
	return s.dataStore.CreateAccountHost(ctx, row)
}

func (s *PersistenceStore) SetAccountHostPinned(ctx context.Context, accountHostUUID string, pinned bool) error {
	return s.dataStore.SetAccountHostPinned(ctx, accountHostUUID, pinned)
}

func (s *PersistenceStore) UpdateAccountHostServiceCredentials(ctx context.Context, accountHostUUID string, cred *datamodel.ExpertModeCredential) error {
	return s.dataStore.UpdateAccountHostServiceCredentials(ctx, accountHostUUID, cred)
}

func (s *PersistenceStore) GetActivePinAccountHostsJobByAccount(ctx context.Context, accountID int64) (*datamodel.Job, error) {
	return s.dataStore.GetActivePinAccountHostsJobByAccount(ctx, accountID)
}

func (s *PersistenceStore) GetAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	return s.dataStore.GetAddressRange(ctx, arID)
}

func (s *PersistenceStore) ListAddressRanges(ctx context.Context, hostProjectNumber, vpcName string, arID, lifType *string) ([]*datamodel.AddressRange, error) {
	return s.dataStore.ListAddressRanges(ctx, hostProjectNumber, vpcName, arID, lifType)
}

func (s *PersistenceStore) UpdateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return s.dataStore.UpdateAddressRange(ctx, ar)
}

func (s *PersistenceStore) UpdateAddressRangeState(ctx context.Context, arID, state string, routeAggregationApplied *bool) (*datamodel.AddressRange, error) {
	return s.dataStore.UpdateAddressRangeState(ctx, arID, state, routeAggregationApplied)
}

func (s *PersistenceStore) UpdateAddressRangeStateToCreatedIfLastPool(ctx context.Context, arUUID, network, excludePoolUUID, addressRangeCidr string) (bool, error) {
	return s.dataStore.UpdateAddressRangeStateToCreatedIfLastPool(ctx, arUUID, network, excludePoolUUID, addressRangeCidr)
}

func (s *PersistenceStore) ResetAddressRangesInUseToCreated(ctx context.Context, hostProjectNumber, vpcName string) error {
	return s.dataStore.ResetAddressRangesInUseToCreated(ctx, hostProjectNumber, vpcName)
}

func (s *PersistenceStore) DeleteAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	return s.dataStore.DeleteAddressRange(ctx, arID)
}

func (s *PersistenceStore) CreateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error) {
	return s.dataStore.CreateRegion(ctx, region)
}

func (s *PersistenceStore) ListRegions(ctx context.Context) ([]*datamodel.Region, error) {
	return s.dataStore.ListRegions(ctx)
}

func (s *PersistenceStore) UpdateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error) {
	return s.dataStore.UpdateRegion(ctx, region)
}

func (s *PersistenceStore) GetRegionByUUID(ctx context.Context, uuid string) (*datamodel.Region, error) {
	return s.dataStore.GetRegionByUUID(ctx, uuid)
}

func (s *PersistenceStore) GetRegionByName(ctx context.Context, name string) (*datamodel.Region, error) {
	return s.dataStore.GetRegionByName(ctx, name)
}

func (s *PersistenceStore) CreateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	return s.dataStore.CreateZone(ctx, zone, regionUUID)
}

func (s *PersistenceStore) ListZones(ctx context.Context) ([]*datamodel.Zone, error) {
	return s.dataStore.ListZones(ctx)
}

func (s *PersistenceStore) GetZoneByRegionName(ctx context.Context, regionName string) (*datamodel.Zone, error) {
	return s.dataStore.GetZoneByRegionName(ctx, regionName)
}

func (s *PersistenceStore) UpdateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	return s.dataStore.UpdateZone(ctx, zone, regionUUID)
}

func (s *PersistenceStore) CreateIPRange(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	return s.dataStore.CreateIPRange(ctx, ir)
}

func (s *PersistenceStore) GetIPRangeByUUID(ctx context.Context, irID string) (*datamodel.IPRange, error) {
	return s.dataStore.GetIPRangeByUUID(ctx, irID)
}

func (s *PersistenceStore) GetIPRanges(ctx context.Context, accountUUID, vendorSubnetID *string) ([]*datamodel.IPRange, error) {
	return s.dataStore.GetIPRanges(ctx, accountUUID, vendorSubnetID)
}

func (s *PersistenceStore) GetDataLIFIPRanges(ctx context.Context, accountUUID, network string) ([]*datamodel.IPRange, error) {
	return s.dataStore.GetDataLIFIPRanges(ctx, accountUUID, network)
}

func (s *PersistenceStore) UpdateIPRangeNetwork(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	return s.dataStore.UpdateIPRangeNetwork(ctx, ir)
}

func (s *PersistenceStore) DeleteIPRange(ctx context.Context, irID string) (*datamodel.IPRange, error) {
	return s.dataStore.DeleteIPRange(ctx, irID)
}

func (s *PersistenceStore) GetHarvestHaSiblingNodeGroupID(ctx context.Context, nodeID int64) (int64, error) {
	return s.dataStore.GetHarvestHaSiblingNodeGroupID(ctx, nodeID)
}

func (s *PersistenceStore) GetHarvestHaSiblingNodeID(ctx context.Context, nodeID int64) (int64, error) {
	return s.dataStore.GetHarvestHaSiblingNodeID(ctx, nodeID)
}

func (s *PersistenceStore) ListNodeGroupsWithPollerCounts(ctx context.Context) ([]datamodel.NodeGroupPollerCount, error) {
	return s.dataStore.ListNodeGroupsWithPollerCounts(ctx)
}

func (s *PersistenceStore) ListNodeNodeGroupMapsByNodeGroupID(ctx context.Context, nodeGroupID int64) ([]*datamodel.NodeNodeGroupMap, error) {
	return s.dataStore.ListNodeNodeGroupMapsByNodeGroupID(ctx, nodeGroupID)
}


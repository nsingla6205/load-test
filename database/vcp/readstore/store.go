// Package readstore is a narrow, read-only view of the VCP database used by the
// ops inventory API (POST /v1/internal/projects/{projectNumber}/locations/{locationId}/resources:get and :list).
//
// Every query starts from Unscoped(), so soft-deleted rows are visible unless a
// caller explicitly asks to hide them. The store has no write methods.
package readstore

import (
	"context"
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"gorm.io/gorm"
)

var (
	// ErrNotFound is returned when no row matches the query.
	ErrNotFound = errors.New("resource not found")
	// ErrAmbiguousName is returned when more than one live row matches a name lookup.
	ErrAmbiguousName = errors.New("ambiguous name; use uuid")
	// ErrInvalidQuery is returned when a query uses a column or limit the store does not allow.
	ErrInvalidQuery = errors.New("invalid read query")
)

// MaxListLimit is the largest number of rows a single list call may fetch.
// It is one more than the API maximum page size so callers can detect a next page.
const MaxListLimit = 51

// Column is a column or JSON expression the store allows in a predicate.
// Only the constants below are accepted; any other value returns ErrInvalidQuery.
type Column string

const (
	ColAccountID                        Column = "account_id"
	ColName                             Column = "name"
	ColState                            Column = "state"
	ColLifeCycleState                   Column = "life_cycle_state"
	ColType                             Column = "type"
	ColCorrelationID                    Column = "correlation_id"
	ColResourceUUID                     Column = "resource_uuid"
	ColPoolID                           Column = "pool_id"
	ColSvmID                            Column = "svm_id"
	ColVolumePerformanceGroupID         Column = "volume_performance_group_id"
	ColVolumeID                         Column = "volume_id"
	ColVolumeUUID                       Column = "volume_uuid"
	ColBackupVaultID                    Column = "backup_vault_id"
	ColKmsConfigID                      Column = "kms_config_id"
	ColActiveDirectoryID                Column = "active_directory_id"
	ColHostUUID                         Column = "host_uuid"
	ColVolumeBackupVaultUUID            Column = "data_protection->>'backup_vault_id'"
	ColVolumeBackupPolicyUUID           Column = "data_protection->>'backup_policy_id'"
	ColReplicationSourceVolumeUUID      Column = "replication_attributes->>'source_volume_uuid'"
	ColReplicationDestinationVolumeUUID Column = "replication_attributes->>'destination_volume_uuid'"
	ColCreatedAt                        Column = "created_at"
	ColUpdatedAt                        Column = "updated_at"
)

var predicateColumns = map[Column]struct{}{
	ColAccountID: {}, ColName: {}, ColState: {}, ColLifeCycleState: {}, ColType: {},
	ColCorrelationID: {}, ColResourceUUID: {}, ColPoolID: {}, ColSvmID: {},
	ColVolumePerformanceGroupID: {}, ColVolumeID: {}, ColVolumeUUID: {}, ColBackupVaultID: {},
	ColKmsConfigID: {}, ColActiveDirectoryID: {}, ColHostUUID: {}, ColVolumeBackupVaultUUID: {},
	ColVolumeBackupPolicyUUID: {}, ColReplicationSourceVolumeUUID: {}, ColReplicationDestinationVolumeUUID: {},
}

var timeColumns = map[Column]struct{}{
	ColCreatedAt: {},
	ColUpdatedAt: {},
}

// Eq is one equality predicate. All predicates in a query are AND-ed.
type Eq struct {
	Column Column
	Value  any
}

// TimeRange filters a timestamp column to the half-open range [From, To).
// A nil bound is open.
type TimeRange struct {
	Column Column
	From   *time.Time
	To     *time.Time
}

// ListOptions is a keyset-paginated list query on one table.
type ListOptions struct {
	Eq             []Eq
	TimeRanges     []TimeRange
	IncludeDeleted bool
	// AfterID returns rows with id > AfterID. Zero means the first page.
	AfterID int64
	// Limit is the number of rows to fetch, between 1 and MaxListLimit.
	Limit int
}

// NameQuery looks up one row by its name column plus extra AND-ed predicates
// (project and, where needed, the parent that makes the name unique).
type NameQuery struct {
	Name string
	Eq   []Eq
}

// OwnerKind names a table whose rows can be used as a list parent.
type OwnerKind string

const (
	OwnerPool                   OwnerKind = "pool"
	OwnerVolume                 OwnerKind = "volume"
	OwnerSvm                    OwnerKind = "svm"
	OwnerVolumePerformanceGroup OwnerKind = "volumePerformanceGroup"
	OwnerBackupVault            OwnerKind = "backupVault"
	OwnerBackupPolicy           OwnerKind = "backupPolicy"
	OwnerKmsConfig              OwnerKind = "cmek"
	OwnerActiveDirectory        OwnerKind = "activeDirectory"
	OwnerHost                   OwnerKind = "host"
)

// Owner is the minimal identity of a parent row: its internal id and the
// account that owns it. AccountID is nil for tables with no account (hosts).
type Owner struct {
	ID        int64
	UUID      string
	AccountID *int64
}

// ReadStore is the read-only database contract of the ops inventory API.
type ReadStore interface {
	GetAccountByName(ctx context.Context, name string) (*datamodel.Account, error)
	// GetAccountsByIDs loads accounts in one query, keyed by id. Missing ids are left out.
	GetAccountsByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.Account, error)
	LookupOwner(ctx context.Context, kind OwnerKind, uuid string) (*Owner, error)

	GetPoolByUUID(ctx context.Context, uuid string) (*datamodel.Pool, error)
	GetPoolByName(ctx context.Context, q NameQuery) (*datamodel.Pool, error)
	ListPools(ctx context.Context, opts ListOptions) ([]datamodel.Pool, error)

	GetVolumeByUUID(ctx context.Context, uuid string) (*datamodel.Volume, error)
	GetVolumeByName(ctx context.Context, q NameQuery) (*datamodel.Volume, error)
	ListVolumes(ctx context.Context, opts ListOptions) ([]datamodel.Volume, error)

	GetSnapshotByUUID(ctx context.Context, uuid string) (*datamodel.Snapshot, error)
	GetSnapshotByName(ctx context.Context, q NameQuery) (*datamodel.Snapshot, error)
	ListSnapshots(ctx context.Context, opts ListOptions) ([]datamodel.Snapshot, error)

	GetReplicationByUUID(ctx context.Context, uuid string) (*datamodel.VolumeReplication, error)
	GetReplicationByName(ctx context.Context, q NameQuery) (*datamodel.VolumeReplication, error)
	ListReplications(ctx context.Context, opts ListOptions) ([]datamodel.VolumeReplication, error)

	GetBackupByUUID(ctx context.Context, uuid string) (*datamodel.Backup, error)
	GetBackupByName(ctx context.Context, q NameQuery) (*datamodel.Backup, error)
	ListBackups(ctx context.Context, opts ListOptions) ([]datamodel.Backup, error)

	GetBackupVaultByUUID(ctx context.Context, uuid string) (*datamodel.BackupVault, error)
	GetBackupVaultByName(ctx context.Context, q NameQuery) (*datamodel.BackupVault, error)
	ListBackupVaults(ctx context.Context, opts ListOptions) ([]datamodel.BackupVault, error)

	GetBackupPolicyByUUID(ctx context.Context, uuid string) (*datamodel.BackupPolicy, error)
	GetBackupPolicyByName(ctx context.Context, q NameQuery) (*datamodel.BackupPolicy, error)
	ListBackupPolicies(ctx context.Context, opts ListOptions) ([]datamodel.BackupPolicy, error)

	GetKmsConfigByUUID(ctx context.Context, uuid string) (*datamodel.KmsConfig, error)
	ListKmsConfigs(ctx context.Context, opts ListOptions) ([]datamodel.KmsConfig, error)

	GetActiveDirectoryByUUID(ctx context.Context, uuid string) (*datamodel.ActiveDirectory, error)
	GetActiveDirectoryByName(ctx context.Context, q NameQuery) (*datamodel.ActiveDirectory, error)
	ListActiveDirectories(ctx context.Context, opts ListOptions) ([]datamodel.ActiveDirectory, error)

	GetJobByUUID(ctx context.Context, uuid string) (*datamodel.Job, error)
	ListJobs(ctx context.Context, opts ListOptions) ([]datamodel.Job, error)
}

type store struct {
	db *gorm.DB
}

// New returns a ReadStore backed by db.
func New(db *gorm.DB) ReadStore {
	return &store{db: db}
}

// preload helpers. Associations are loaded Unscoped with a minimal column set:
// readers only map identifiers from them, never whole rows.
var (
	accountCols = []string{"id", "uuid", "name"}
	refCols     = []string{"id", "uuid", "name"}
)

func preloadRef(name string, cols []string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Preload(name, func(tx *gorm.DB) *gorm.DB { return tx.Unscoped().Select(cols) })
	}
}

func preloads(fns ...func(*gorm.DB) *gorm.DB) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		for _, fn := range fns {
			db = fn(db)
		}
		return db
	}
}

var (
	noPreload           = func(db *gorm.DB) *gorm.DB { return db }
	poolPreload         = preloads(preloadRef("Account", accountCols), preloadRef("KmsConfig", []string{"id", "uuid"}), preloadRef("ActiveDirectory", refCols))
	volumePreload       = preloads(preloadRef("Account", accountCols), preloadRef("Pool", []string{"id", "uuid", "name", "pool_attributes"}), preloadRef("Svm", refCols), preloadRef("VolumePerformanceGroup", refCols))
	snapshotPreload     = preloads(preloadRef("Account", accountCols), preloadRef("Volume", refCols))
	replicationPreload  = preloads(preloadRef("Account", accountCols), preloadRef("Volume", refCols))
	backupPreload       = preloads(preloadRef("BackupVault", []string{"id", "uuid", "name", "account_id"}), preloadRef("BackupVault.Account", accountCols))
	backupVaultPreload  = preloadRef("Account", accountCols)
	backupPolicyPreload = preloadRef("Account", accountCols)
	kmsConfigPreload    = preloads(preloadRef("Account", accountCols), preloadRef("ServiceAccount", serviceAccountCols))
)

// serviceAccountCols leaves out service_account_password_location, which holds
// the encrypted key. service_account_attributes still holds the encrypted
// data of each key; readers map only key ids, flags and times from it.
var serviceAccountCols = []string{
	"id", "uuid", "name", "service_name", "service_account_email", "state", "state_details",
	"service_account_attributes", "created_at", "updated_at", "deleted_at",
}

func (s *store) GetAccountByName(ctx context.Context, name string) (*datamodel.Account, error) {
	return getByName[datamodel.Account](ctx, s.db, noPreload, NameQuery{Name: name})
}

func (s *store) GetAccountsByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.Account, error) {
	rows, err := getByIDs[datamodel.Account](ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*datamodel.Account, len(rows))
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

func (s *store) GetPoolByUUID(ctx context.Context, uuid string) (*datamodel.Pool, error) {
	return getByUUID[datamodel.Pool](ctx, s.db, poolPreload, uuid)
}

func (s *store) GetPoolByName(ctx context.Context, q NameQuery) (*datamodel.Pool, error) {
	return getByName[datamodel.Pool](ctx, s.db, poolPreload, q)
}

func (s *store) ListPools(ctx context.Context, opts ListOptions) ([]datamodel.Pool, error) {
	return list[datamodel.Pool](ctx, s.db, poolPreload, opts)
}

func (s *store) GetVolumeByUUID(ctx context.Context, uuid string) (*datamodel.Volume, error) {
	return getByUUID[datamodel.Volume](ctx, s.db, volumePreload, uuid)
}

func (s *store) GetVolumeByName(ctx context.Context, q NameQuery) (*datamodel.Volume, error) {
	return getByName[datamodel.Volume](ctx, s.db, volumePreload, q)
}

func (s *store) ListVolumes(ctx context.Context, opts ListOptions) ([]datamodel.Volume, error) {
	return list[datamodel.Volume](ctx, s.db, volumePreload, opts)
}

func (s *store) GetSnapshotByUUID(ctx context.Context, uuid string) (*datamodel.Snapshot, error) {
	return getByUUID[datamodel.Snapshot](ctx, s.db, snapshotPreload, uuid)
}

func (s *store) GetSnapshotByName(ctx context.Context, q NameQuery) (*datamodel.Snapshot, error) {
	return getByName[datamodel.Snapshot](ctx, s.db, snapshotPreload, q)
}

func (s *store) ListSnapshots(ctx context.Context, opts ListOptions) ([]datamodel.Snapshot, error) {
	return list[datamodel.Snapshot](ctx, s.db, snapshotPreload, opts)
}

func (s *store) GetReplicationByUUID(ctx context.Context, uuid string) (*datamodel.VolumeReplication, error) {
	return getByUUID[datamodel.VolumeReplication](ctx, s.db, replicationPreload, uuid)
}

func (s *store) GetReplicationByName(ctx context.Context, q NameQuery) (*datamodel.VolumeReplication, error) {
	return getByName[datamodel.VolumeReplication](ctx, s.db, replicationPreload, q)
}

func (s *store) ListReplications(ctx context.Context, opts ListOptions) ([]datamodel.VolumeReplication, error) {
	return list[datamodel.VolumeReplication](ctx, s.db, replicationPreload, opts)
}

func (s *store) GetBackupByUUID(ctx context.Context, uuid string) (*datamodel.Backup, error) {
	return getByUUID[datamodel.Backup](ctx, s.db, backupPreload, uuid)
}

func (s *store) GetBackupByName(ctx context.Context, q NameQuery) (*datamodel.Backup, error) {
	return getByName[datamodel.Backup](ctx, s.db, backupPreload, q)
}

func (s *store) ListBackups(ctx context.Context, opts ListOptions) ([]datamodel.Backup, error) {
	return list[datamodel.Backup](ctx, s.db, backupPreload, opts)
}

func (s *store) GetBackupVaultByUUID(ctx context.Context, uuid string) (*datamodel.BackupVault, error) {
	return getByUUID[datamodel.BackupVault](ctx, s.db, backupVaultPreload, uuid)
}

func (s *store) GetBackupVaultByName(ctx context.Context, q NameQuery) (*datamodel.BackupVault, error) {
	return getByName[datamodel.BackupVault](ctx, s.db, backupVaultPreload, q)
}

func (s *store) ListBackupVaults(ctx context.Context, opts ListOptions) ([]datamodel.BackupVault, error) {
	return list[datamodel.BackupVault](ctx, s.db, backupVaultPreload, opts)
}

func (s *store) GetBackupPolicyByUUID(ctx context.Context, uuid string) (*datamodel.BackupPolicy, error) {
	return getByUUID[datamodel.BackupPolicy](ctx, s.db, backupPolicyPreload, uuid)
}

func (s *store) GetBackupPolicyByName(ctx context.Context, q NameQuery) (*datamodel.BackupPolicy, error) {
	return getByName[datamodel.BackupPolicy](ctx, s.db, backupPolicyPreload, q)
}

func (s *store) ListBackupPolicies(ctx context.Context, opts ListOptions) ([]datamodel.BackupPolicy, error) {
	return list[datamodel.BackupPolicy](ctx, s.db, backupPolicyPreload, opts)
}

func (s *store) GetKmsConfigByUUID(ctx context.Context, uuid string) (*datamodel.KmsConfig, error) {
	return getByUUID[datamodel.KmsConfig](ctx, s.db, kmsConfigPreload, uuid)
}

func (s *store) ListKmsConfigs(ctx context.Context, opts ListOptions) ([]datamodel.KmsConfig, error) {
	return list[datamodel.KmsConfig](ctx, s.db, kmsConfigPreload, opts)
}

func (s *store) GetActiveDirectoryByUUID(ctx context.Context, uuid string) (*datamodel.ActiveDirectory, error) {
	return getByUUID[datamodel.ActiveDirectory](ctx, s.db, noPreload, uuid)
}

func (s *store) GetActiveDirectoryByName(ctx context.Context, q NameQuery) (*datamodel.ActiveDirectory, error) {
	return getByName[datamodel.ActiveDirectory](ctx, s.db, noPreload, q)
}

func (s *store) ListActiveDirectories(ctx context.Context, opts ListOptions) ([]datamodel.ActiveDirectory, error) {
	return list[datamodel.ActiveDirectory](ctx, s.db, noPreload, opts)
}

func (s *store) GetJobByUUID(ctx context.Context, uuid string) (*datamodel.Job, error) {
	return getByUUID[datamodel.Job](ctx, s.db, noPreload, uuid)
}

func (s *store) ListJobs(ctx context.Context, opts ListOptions) ([]datamodel.Job, error) {
	return list[datamodel.Job](ctx, s.db, noPreload, opts)
}

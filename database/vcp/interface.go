package database

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormWrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"gorm.io/gorm"
)

type DataStoreRepository struct {
	db *gormWrapper.Wrapper
}

func NewDataStoreRepository(db *gormWrapper.Wrapper) *DataStoreRepository {
	return &DataStoreRepository{db: db}
}

type (
	Storage interface {
		Connect(isAdmin bool) error
		Close() error
		HealthCheck() error
		WithTransaction(ctx context.Context, fn func(dbutils.Transaction) error) error
		Migrate(ctx context.Context) error
		Rollback(ctx context.Context) error
		DB() *gorm.DB
		SetupDatabase(ctx context.Context) error

		// Embed DataStore interface
		DataStore
	}

	// DataStore defines all operations
	DataStore interface {
		CreatedPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error)
		CreatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error)
		UpdatingPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error)
		UpdatedPool(ctx context.Context, pool *datamodel.Pool) (*datamodel.Pool, error)
		UpdatePoolSubnetNames(ctx context.Context, poolUUID, snHostProject string, subnetNames []string) error
		UpdatePoolState(ctx context.Context, pool *datamodel.Pool, state string, stateDetails string) (*datamodel.Pool, error)
		UpdatePoolFields(ctx context.Context, poolUUID string, updates map[string]interface{}) error
		UpdatePoolTieringConfig(ctx context.Context, poolUUID string, hotTierConsumption, coldTierConsumption, tieringThreshold *int64, tieringStatus *datamodel.TieringStatus) error
		UpdateAutoTieringHotTierInfo(ctx context.Context, poolUUID string, hotTierSizeInBytes *int64, enableHotTierAutoResize *bool) error
		DeletePool(ctx context.Context, pool *datamodel.Pool) error
		DeletingPool(ctx context.Context, pool *datamodel.Pool) error
		DescribePool(ctx context.Context, poolUUID string, accountID int64) (*datamodel.PoolView, error)
		GetPool(ctx context.Context, poolUUID string, accountID int64) (*datamodel.PoolView, error)
		GetPoolByUUID(ctx context.Context, poolUUID string) (*datamodel.Pool, error)
		GetPoolByID(ctx context.Context, poolID int64) (*datamodel.Pool, error)
		GetPoolStateByUUID(ctx context.Context, poolUUID string) (string, error)
		ListPools(ctx context.Context, filter *dbutils.Filter) ([]*datamodel.PoolView, error)
		// ListPoolsSelective fetches pools matching filter and only preloads relations requested via opts,
		// avoiding extra DB round-trips for callers that need only a subset of fields.
		ListPoolsSelective(ctx context.Context, filter *dbutils.Filter, opts PoolPreloadOptions) ([]*datamodel.PoolView, error)
		// ListPoolsWithFilterAndPaginationOrderedByUUID returns non-deleted pools matching the filter with limit/offset, ordered by uuid for stable pagination (cert/password rotation).
		ListPoolsWithFilterAndPaginationOrderedByUUID(ctx context.Context, filter *dbutils.Filter, pagination *dbutils.Pagination) ([]*datamodel.PoolView, error)
		// ListPoolsWithPagination includes deleted pools as well, it's using unscoped for fetching all pools.
		ListPoolsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.PoolView, error)
		ListPoolUUIDs(ctx context.Context, filter *dbutils.Filter) ([]*PoolIdentifier, error)
		ListPoolUUIDsPaginated(ctx context.Context, filter *dbutils.Filter, offset, limit int) ([]*PoolIdentifier, error)
		// ListPoolsForMetrics retrieves pools with only the fields required for telemetry metrics collection.
		// This is an optimized query that fetches minimal data compared to ListPools.
		ListPoolsForMetrics(ctx context.Context) ([]*PoolMetricsData, error)
		// ListPoolsForResourceData returns only the fields needed for aggregator resource data, optimized for telemetry with pagination.
		ListPoolsForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*PoolResourceData, error)
		// ListOntapModePoolsForResourceData returns only ONTAP mode pools with fields needed for aggregator resource data, optimized for telemetry with pagination.
		ListOntapModePoolsForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*PoolResourceData, error)
		GetBlockOnlyPoolIDs(ctx context.Context) (map[int64]bool, error)
		ListPendingResourceDeletions(ctx context.Context, offset, limit int) ([]*datamodel.PendingResourceDeletions, error)
		GetResourcesCount(ctx context.Context) (int64, error)
		GetPoolsCount(ctx context.Context, filter *dbutils.Filter) (int64, error)
		GetPoolByVendorID(ctx context.Context, vendorID string, accountID int64) (*datamodel.PoolView, error)
		GetPoolByName(ctx context.Context, conditions [][]interface{}) (*datamodel.PoolView, error)
		SavePoolWithVsaDetails(ctx context.Context, pool *datamodel.Pool, cluster *datamodel.ClusterDetails) error
		UpdatePoolWithKmsConfigID(ctx context.Context, pool *datamodel.Pool, kmsConfigUUID string) (*datamodel.Pool, error)
		GetPoolsByAccountName(ctx context.Context, accountName string) ([]*datamodel.Pool, error)
		CountActivePoolsByNetwork(ctx context.Context, network string, excludePoolUUID string) (int64, error)
		GetPoolsByActiveDirectoryId(ctx context.Context, activeDirectoryId string) ([]*datamodel.Pool, error)
		GetNextSerialNumber(ctx context.Context) (int64, error)
		ListTpProjects(ctx context.Context) ([]string, error)
		// ListAllTpProjects returns all distinct RTP values, including soft-deleted.
		ListAllTpProjects(ctx context.Context) ([]string, error)

		// GetKmsConfigsByUUIDs fetches KMS configs by UUID without relation preloads; callers derive
		// all batch-KMS fields from the base row and the KmsAttributes JSON column.
		GetKmsConfigsByUUIDs(ctx context.Context, kmsConfigUUIDs []string) ([]*datamodel.KmsConfig, error)

		CreateVolume(ctx context.Context, volume *datamodel.Volume) (*datamodel.Volume, error)
		GetVolume(ctx context.Context, id string) (*datamodel.Volume, error)
		DescribeVolume(ctx context.Context, id string) (*datamodel.Volume, error)
		GetVolumeWithAccountID(ctx context.Context, id string, accountID int64) (*datamodel.Volume, error)
		GetVolumeByIDAndAccountID(ctx context.Context, volumeID int64, accountID int64) (*datamodel.Volume, error)
		GetVolumeByNameAndAccountID(ctx context.Context, name string, accountID int64) (*datamodel.Volume, error)
		GetVolumeByNameAccountIDAndZone(ctx context.Context, name string, accountID int64, zone string, isRegionalPool bool) (*datamodel.Volume, error)
		GetVolumeByJunctionPath(ctx context.Context, junctionPath string, accountID int64, poolId int64) (*datamodel.Volume, error)
		GetVolumeCount(ctx context.Context, accountName string) (int64, error)
		GetVolumeByName(ctx context.Context, name string) (*datamodel.Volume, error)
		UpdateVolume(ctx context.Context, volume *datamodel.Volume) error
		RevertedVolume(ctx context.Context, volume *datamodel.Volume, snapshot *datamodel.Snapshot) ([]*datamodel.Snapshot, error)
		UpdateVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error
		BatchUpdateVolumeFields(ctx context.Context, updates []datamodel.VolumeFieldUpdate) error
		BatchUpdateVolumeTieringFields(ctx context.Context, updates map[string]datamodel.VolumeTieringUpdate) error
		DeleteVolume(ctx context.Context, id string) (*datamodel.Volume, error)
		DeleteVolumeAndChildResources(ctx context.Context, volumeUUID string) (*datamodel.Volume, error)
		UpdateVolumeState(ctx context.Context, id string, state string, stateDetails string) (*datamodel.Volume, error)
		ListVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error)
		ListAllVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error)
		// ListVolumesWithPagination retrieves volumes with pagination support including deleted volumes
		ListVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error)
		// ListVolumesForResourceData returns only the fields needed for aggregator resource data, optimized for telemetry with pagination.
		ListVolumesForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*VolumeResourceData, error)
		GetVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Volume, error)
		// GetPoolVolumesForQosTransition returns active pool volumes with only the fields needed for
		// manual↔auto qosType transition (no association preloads).
		GetPoolVolumesForQosTransition(ctx context.Context, poolID int64) ([]*datamodel.Volume, error)
		GetVolumesByVolumePerformanceGroupID(ctx context.Context, vpgID int64) ([]*datamodel.Volume, error)
		DereferenceVPGFromDeletedVolumes(ctx context.Context, vpgID int64) error
		DereferencePoolVolumesFromVPGs(ctx context.Context, poolID int64) (int64, error)
		HasDependentChildThinClone(ctx context.Context, poolID int64, parentVolumeUUID string) (bool, error)
		GetVolumeCountByPoolID(ctx context.Context, poolID int64) (int64, error)
		GetFlexCacheVolumeCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error)
		GetMultipleVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error)
		GetMultipleVolumesSelective(ctx context.Context, conditions [][]interface{}, opts VolumePreloadOptions) ([]*datamodel.Volume, error)
		VerifyVolumeOwnership(ctx context.Context, volumeID string, accountName string) (*datamodel.Volume, error)
		GetAllVolumesForHG(ctx context.Context, hostGroupUUID string, accountID int64) ([]*datamodel.Volume, error)
		GetEligibleVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error)

		CreateVolumeReplication(ctx context.Context, volumeRep *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error)
		GetVolumeReplication(ctx context.Context, id string) (*datamodel.VolumeReplication, error)
		GetVolumeReplicationByVolumeID(ctx context.Context, volumeID int64) (*datamodel.VolumeReplication, error)
		UpdateVolumeReplication(ctx context.Context, volumeRep *datamodel.VolumeReplication) error
		UpdateVolumeReplicationFields(ctx context.Context, volumeRepUUID string, updates map[string]interface{}) error
		UpdateVolumeReplicationStates(ctx context.Context, volumeRep *datamodel.VolumeReplication) error
		UpdateVolumeReplicationTransferStats(ctx context.Context, volumeRep *datamodel.VolumeReplication) error
		DeleteVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error)
		GetVolumeReplicationByProjectId(ctx context.Context, accountId int64) ([]*datamodel.VolumeReplication, error)
		GetVolumeReplicationCount(ctx context.Context, accountName string) (int64, error)
		GetVolumeReplicationCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error)
		GetVolumeReplicationCountByPeerDetails(ctx context.Context, accountName string, peerSvmName string, peerVolumeName string) (int64, error)
		GetVolumeReplicationCountByVolumeID(ctx context.Context, volumeID int64) (int64, error)
		GetReplicatedVolumeUUIDs(ctx context.Context, volumeUUIDs []string) ([]string, error)
		ListVolumeReplications(ctx context.Context, filter dbutils.Filter, queryDepth int) ([]*datamodel.VolumeReplication, error)
		ListVolumeReplicationsWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.VolumeReplication, error)

		GetAccount(ctx context.Context, name string) (*datamodel.Account, error)
		CreateAccount(ctx context.Context, account *datamodel.Account) (*datamodel.Account, error)
		GetAccountByUUID(ctx context.Context, uuid string) (*datamodel.Account, error)
		GetSoftDeleteAccount(ctx context.Context, name string) (*datamodel.Account, error)
		GetDeletedAccounts(ctx context.Context) ([]*datamodel.Account, error)
		DeleteAccount(ctx context.Context, accountID int64) error
		RollBackDeletedAccount(ctx context.Context, accountID int64) error
		GetAccounts(ctx context.Context, includeDelete bool, pagination *dbutils.Pagination) ([]*datamodel.Account, error)
		GetAccountsWithFilter(ctx context.Context, filter *dbutils.Filter, pagination *dbutils.Pagination) ([]*datamodel.Account, error)
		// ListAccountsForTelemetry retrieves accounts with only the fields required for telemetry/bizops operations.
		// This is an optimized query that selects only id, name, and state columns.
		ListAccountsForTelemetry(ctx context.Context, pagination *dbutils.Pagination) ([]*AccountTelemetryData, error)
		// ListFreeTrialAccountsForBilling returns account IDs and trial end from account_metadata (billing prefetch).
		ListFreeTrialAccountsForBilling(ctx context.Context) (map[int64]*time.Time, error)
		UpdateAccountStateForHandleResource(ctx context.Context, accountUUID string, newState string) error
		UpdateAccountVolumeRefreshTimestamp(ctx context.Context, accountUUID string, completionTime time.Time) error
		// UpdateAccountTrialMetadata merges trial fields into account_metadata JSONB.
		// account must be the row to update; AccountMetadata on account is the merge base (nil is treated as empty).
		// Nil fields on trial clear that metadata key; nil trial is a no-op.
		UpdateAccountTrialMetadata(ctx context.Context, account *datamodel.Account, trial *datamodel.AccountTrialMode) error

		CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error)
		CreateJobWithWorkflowID(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error)
		DeleteJob(ctx context.Context, id, errorDetails string) error
		UpdateJob(ctx context.Context, jobID string, status string, trackingID int, errorDetails string) error
		GetJob(ctx context.Context, jobID string) (*datamodel.Job, error)
		GetJobsWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.Job, error)
		GetOngoingMigrateKmsConfigJob(ctx context.Context, accountId int64) (*datamodel.Job, error)
		UpdateJobAttributes(ctx context.Context, jobUUID string, jobAttributes *datamodel.JobAttributes) error
		CheckAndFetchDuplicateJobs(ctx context.Context, jobType string, correlationID string) (*datamodel.Job, error)
		CancelRunningJobsForResource(ctx context.Context, resourceUUID string) error
		GetActivePrepopulateJobs(ctx context.Context) ([]*datamodel.Job, error)
		CancelPrepopulateJobsForVolume(ctx context.Context, volumeUUID string) error

		GetSvmForPoolID(ctx context.Context, poolID int64) (*datamodel.Svm, error)
		GetNodesByPoolID(ctx context.Context, poolId int64) ([]*datamodel.Node, error)
		GetNodeByID(ctx context.Context, nodeID int64) (*datamodel.Node, error)
		CreateNode(ctx context.Context, node *datamodel.Node) (*datamodel.Node, error)
		GetFaultDomainCountsByZone(ctx context.Context) (map[string]map[string]int64, error)

		CreateSVM(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error)
		CreateSvmInCreatingState(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error)
		GetSvmsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Svm, error)
		ActiveSvmExistsByPoolID(ctx context.Context, poolID int64) (bool, error)
		GetNextSVMIndexByPoolID(ctx context.Context, poolID int64) (int64, error)
		UpdateSvmWithKmsConfigIDs(ctx context.Context, svm *datamodel.Svm, gcpKmsConfigUUID, externalGcpKmsConfigUUID string) (*datamodel.Svm, error)
		UpdateSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm, activeDirectoryID int64) (*datamodel.Svm, error)
		UnsetSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error)
		UpdateSvmCurrentKmsKeyID(ctx context.Context, svmUUID string, keyID string) error
		ListSvmsWithAccountId(ctx context.Context, accountId int64) ([]*datamodel.Svm, error)
		GetSvmByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.Svm, error)
		GetSvmByExternalUUID(ctx context.Context, externalUUID string, poolID int64) (*datamodel.Svm, error)
		GetSvmByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (*datamodel.Svm, error)
		SvmExistsByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (bool, error)
		TransitionSvmToDeleting(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error)
		UpdateSvmState(ctx context.Context, svm *datamodel.Svm, state string, stateDetails string, conditions ...[]interface{}) error

		CreateLif(ctx context.Context, lif *datamodel.Lif) (*datamodel.Lif, error)
		GetLifForNode(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error)

		CreateHostGroup(ctx context.Context, hostGroup *datamodel.HostGroup) (*datamodel.HostGroup, error)
		CreateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error)
		CountHostsWithIPAddress(ctx context.Context, ipAddress string, includeDeleted bool) (int64, error)
		GetHost(ctx context.Context, hostUUID string) (*datamodel.Host, error)
		UpdateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error)
		DeleteHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error)
		GetHostGroup(ctx context.Context, id string, accountID int64) (*datamodel.HostGroup, error)
		GetMultipleHostGroups(ctx context.Context, ids []string, accountID int64) ([]*datamodel.HostGroup, error)
		GetHostGroupsByUUIDs(ctx context.Context, hostGroupUUIDs []string) ([]*datamodel.HostGroup, error)
		DeleteHostGroup(ctx context.Context, hostGroupUUID string, accountID int64) (*datamodel.HostGroup, error)
		UpdateHostGroupsState(ctx context.Context, hostGroupUUID []string, accountID int64, state string, stateDetails string) error
		UpdateHostGroup(ctx context.Context, hostGroupUUID string, accountID int64, description *string, Hosts *[]string) (*datamodel.HostGroup, error)
		ListHostGroupsByAccountID(ctx context.Context, accountID int64) ([]*datamodel.HostGroup, error)
		UpdateHostGroupsStateForHandleResource(ctx context.Context, hostGroupUUID string, accountID int64, state, stateDetails string) error

		GetLifsForNodesWithProtocol(ctx context.Context, nodeIDs []int64, accountID int64, protocol string) ([]*datamodel.Lif, error)
		GetLifByNodeID(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error)
		DeleteLif(ctx context.Context, lif *datamodel.Lif) error
		DeleteNode(ctx context.Context, node *datamodel.Node) error
		DeletingNode(ctx context.Context, node *datamodel.Node) error
		UpdateNodesInstanceType(ctx context.Context, poolID int64, newInstanceType string) error
		UpdateNodesSizeAndInstanceType(ctx context.Context, poolID int64, updatesByNodeName map[string]datamodel.NodeDetails) error
		DeleteSVM(ctx context.Context, svm *datamodel.Svm) error
		DeletingSVM(ctx context.Context, svm *datamodel.Svm) error
		ErroredNode(ctx context.Context, node *datamodel.Node, errMsg string) error
		ErroredSVM(ctx context.Context, svm *datamodel.Svm, errMsg string) error

		CreatingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error)
		UpdateSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error)
		UpdateSnapshotForHandleResource(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error)
		GetSnapshotByUUID(ctx context.Context, uuid string, accountID int64, volumeID int64) (*datamodel.Snapshot, error)
		GetSnapshotByNameAndVolumeId(ctx context.Context, snapshotName string, accountID int64, volumeID int64) (*datamodel.Snapshot, error)
		GetSnapshotByPoolID(ctx context.Context, SnapshotUUID string, accountID int64, poolID int64, isParentSnapshot bool) (*datamodel.Snapshot, error)
		GetSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error)
		GetWronglyDeletedSnapshot(ctx context.Context, snapshotExternalUUID string) (*datamodel.Snapshot, error)
		UnDeleteSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error
		GetSnapshotsByVolumeIDs(ctx context.Context, volumeID []int64) ([]*datamodel.Snapshot, error)
		GetReplicationSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error)
		GetSnapshotsWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.Snapshot, error)
		GetAppConsistentSnapshotsForVolume(ctx context.Context, accountID, volumeID int64) ([]*datamodel.Snapshot, error)
		GetSnapshotsByTypeAndVolumeID(ctx context.Context, snapshotType string, volumeID int64) ([]*datamodel.Snapshot, error)
		DeleteSnapshot(ctx context.Context, id string) (*datamodel.Snapshot, error)
		DeletingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error
		BatchDeleteSnapshots(ctx context.Context, snapshotIDs []int64) ([]*datamodel.Snapshot, error)
		BatchCreateSnapshots(ctx context.Context, newSnapshots []*datamodel.Snapshot, returnCreatedSnapshotUUIDs bool) ([]string, error)
		BatchUpdateSnapshots(ctx context.Context, snapshots []*datamodel.Snapshot) error
		BatchUnDeleteSnapshots(ctx context.Context, snapshots []*datamodel.Snapshot) error
		BatchGetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*datamodel.Snapshot, error)
		BatchGetWronglyDeletedSnapshots(ctx context.Context, snapshotExternalUUIDs []string) ([]*datamodel.Snapshot, error)
		CreatingQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error)
		UpdatingQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error)
		UpdateQuotaRule(ctx context.Context, quotaRule *datamodel.QuotaRule) (*datamodel.QuotaRule, error)
		GetQuotaRuleByUUID(ctx context.Context, uuid string, accountID int64) (*datamodel.QuotaRule, error)
		GetQuotaRulesByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.QuotaRule, error)
		GetQuotaRulesWithCondition(ctx context.Context, filter dbutils.Filter) ([]*datamodel.QuotaRule, error)
		GetQuotaRuleCountBySvmID(ctx context.Context, svmID int64) (int64, error)
		DeleteQuotaRule(ctx context.Context, id string) (*datamodel.QuotaRule, error)
		ReplaceDstQuotaRulesWithSrc(ctx context.Context, volumeID int64, accountID int64, dstQuotaRuleUUIDs []string, srcQuotaRules []*datamodel.QuotaRule) ([]*datamodel.QuotaRule, error)
		GetMultipleKmsConfigs(ctx context.Context, conditions [][]interface{}) ([]*datamodel.KmsConfig, error)
		GetKmsConfig(ctx context.Context, kmsConfigUUID string) (*datamodel.KmsConfig, error)
		UpdateKmsConfigState(ctx context.Context, kmsConfigUUID string, state string, stateDetails string) (*datamodel.KmsConfig, error)
		DeleteKmsConfig(ctx context.Context, kmsConfigUUID, state, stateDetails string) (*datamodel.KmsConfig, error)
		GetSvmsByKmsConfigID(ctx context.Context, kmsConfigID int64) ([]*datamodel.Svm, error)
		ListOngoingPoolJobsWithKmsConfigId(ctx context.Context, kmsId, accountId int64) ([]*datamodel.Job, error)
		UpdateKmsConfigStateForHandleResource(ctx context.Context, kmsConfigUUID string, stateDetails string, event string) (*datamodel.KmsConfig, error)

		CreateKmsConfig(ctx context.Context, kmsConfig *datamodel.KmsConfig) (*datamodel.KmsConfig, error)
		GetKmsConfigByUUID(ctx context.Context, uuid string) (*datamodel.KmsConfig, error)
		UpdateKmsConfigAttributes(ctx context.Context, uuid string, attributes *datamodel.KmsAttributes) (*datamodel.KmsConfig, error)
		GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*datamodel.Job, error)
		UpdateKmsConfigDetails(ctx context.Context, uuid string, fullKeyPath string, resourceID string) (*datamodel.KmsConfig, error)
		GetKmsConfigByKeyFullPath(ctx context.Context, keyFullPath string, accountID int64) (*datamodel.KmsConfig, error)
		UpdateKmsConfig(ctx context.Context, kmsUUID string, updates map[string]interface{}) error
		IsKmsConfigInUse(ctx context.Context, kmsConfigUUID string) (bool, error)
		ListKmsConfigByAccountID(ctx context.Context, accountID int64) ([]*datamodel.KmsConfig, error)

		CreateKmsServiceAccount(ctx context.Context, serviceAccount *datamodel.ServiceAccount) (*datamodel.ServiceAccount, error)
		UpdateServiceAccountEmailAndKey(ctx context.Context, uuid string, email string, key string) (*datamodel.ServiceAccount, error)
		UpdateServiceAccountState(ctx context.Context, uuid string, state string, stateDetails string) (*datamodel.ServiceAccount, error)
		GetServiceAccountFromEmail(ctx context.Context, email string) (*datamodel.ServiceAccount, error)
		ListKmsServiceAccounts(ctx context.Context, filter *dbutils.Filter) ([]*datamodel.ServiceAccount, error)
		DeleteServiceAccount(ctx context.Context, serviceAccount *datamodel.ServiceAccount) error

		// Multi-key management functions
		AddKeyToServiceAccount(ctx context.Context, serviceAccountUUID string, key datamodel.ServiceAccountKey) error
		RemoveKeyFromServiceAccount(ctx context.Context, serviceAccountUUID string, keyID string) error
		MarkKeyForDeletion(ctx context.Context, serviceAccountUUID string, keyID string) error
		SetPrimaryKeyForServiceAccount(ctx context.Context, serviceAccountUUID string, keyID string) error
		UpdateServiceAccountPasswordLocation(ctx context.Context, serviceAccountUUID string, encryptedKeyData string) error
		GetServiceAccountWithKeys(ctx context.Context, serviceAccountUUID string) (*datamodel.ServiceAccount, error)

		GetBackupVaultByNameAndOwnerID(ctx context.Context, backupVaultName, ownerID string) (*datamodel.BackupVault, error)
		GetBackupVaultByCrossRegionBackupVaultName(ctx context.Context, crossRegionBackupVaultName string, accountID int64) (*datamodel.BackupVault, error)
		CreatingBackupVault(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error)
		ListBackupVaults(ctx context.Context, accountID int64) ([]*datamodel.BackupVault, error)
		GetBackupVaultByUUIDndOwnerID(ctx context.Context, backupVaultUUID string, accountID int64) (*datamodel.BackupVault, error)
		GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, accountID int64) (*datamodel.BackupVault, error)
		GetBackupByNameAndBackupVaultID(ctx context.Context, backupName string, backupVaultID int64) (*datamodel.Backup, error)
		CreateBackupVaultEntryInVCP(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error)
		UpdateBackupVaultBucketDetails(ctx context.Context, backupVault *datamodel.BackupVault) error
		GetBackupVault(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error)
		GetBackupVaultById(ctx context.Context, backupVaultId int64) (*datamodel.BackupVault, error)
		UpdateBackupVaultState(ctx context.Context, bv *datamodel.BackupVault, state, stateDetails string) (*datamodel.BackupVault, error)
		UpdateBackupVaultInVCP(ctx context.Context, vault *datamodel.BackupVault, vcpVault *datamodel.BackupVault) (*datamodel.BackupVault, error)
		GetMultipleBackupVaults(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupVault, error)
		DeleteBackupVaultInVCP(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error)
		RestoreDeletedBackupVault(ctx context.Context, backupVaultUUID string, accountID int64, state, stateDetails string) (*datamodel.BackupVault, error)
		GetVolumeCountByBackupVaultID(ctx context.Context, backupVaultUUID string) (int64, error)
		GetVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.Volume, error)
		GetExpertModeVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.ExpertModeVolumes, error)
		GetBackupCountByBackupVaultID(ctx context.Context, backupVaultID int64) (int64, error)

		GetBackupPolicyByUUIDAndOwnerID(ctx context.Context, backupPolicyUUID string, accountID int64) (*datamodel.BackupPolicy, error)
		GetBackupPolicyByNameAndOwnerID(ctx context.Context, backupPolicyName string, accountID int64) (*datamodel.BackupPolicy, error)
		GetVolumeCountByBackupPolicyID(ctx context.Context, backupPolicyUUID string) (int64, error)
		GetBackupPolicyUUIDsFromBackupVaultUUID(ctx context.Context, backupVaultUUID string, accountID int64) ([]string, error)
		GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountID int64) ([]string, error)
		GetCmekRotationJobStatuses(ctx context.Context, startTime, endTime time.Time, limit, offset int) ([]*CmekRotationJobStatus, error)
		ListBackupPolicyVolumeCount(ctx context.Context, conditions [][]interface{}) (map[string]int64, error)
		ListBackupPolicies(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupPolicy, error)
		ListBackupPoliciesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupPolicy, error)
		CreateBackupPolicyEntryInVCP(ctx context.Context, backupPolicy *datamodel.BackupPolicy) (*datamodel.BackupPolicy, error)
		UpdateBackupPolicy(ctx context.Context, uuid string, updates map[string]interface{}) (*datamodel.BackupPolicy, error)
		DeleteBackupPolicy(ctx context.Context, backupPolicyUUID string) (*datamodel.BackupPolicy, error)

		CreateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error)
		GetBackup(ctx context.Context, backupVaultUUID string, backupUUID string, accountName string) (*datamodel.Backup, error)
		GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error)
		BatchGetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error)
		DeleteBackup(ctx context.Context, backupUUID string) (*datamodel.Backup, error)
		UpdateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error)
		UpdateBackupFields(ctx context.Context, backupUUID string, updates map[string]interface{}) error
		UpdateBackupConstituentCountFromVolume(ctx context.Context, backup *datamodel.Backup, volume *datamodel.Volume) (*datamodel.Backup, error)
		FinishBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error)
		UpdateBackupState(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error)
		IsBackupInCreatingorDeletingStateByVolume(ctx context.Context, volumeUUID string) (bool, error)
		AreBackupsInProgressForVolume(ctx context.Context, volumeUUID string, excludeBackupUUIDs []string, createdBefore *time.Time) (bool, error)
		GetEarliestCreatingBackupTime(ctx context.Context, volumeUUID string) (*time.Time, error)
		IsLatestBackup(ctx context.Context, backupUUID, volumeUUID string) (bool, error)
		IsLatestBackupAnyState(ctx context.Context, backupUUID, volumeUUID string) (bool, error)
		IsLatestBackupInVault(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64) (bool, error)
		IsLatestBackupInVaultAndInEndpoint(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64, endpointUUID string) (bool, error)
		BackupCountByVolumeID(ctx context.Context, volumeUUID string) (int64, error)
		BackupCountByVolumeIDVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error)
		FetchScheduledBackupsForDeletion(ctx context.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy, isExpertMode bool) ([]*datamodel.Backup, error)
		IsBackupShared(ctx context.Context, backup *datamodel.Backup) (bool, error)
		GetBackupCountByVolumeUUIDs(ctx context.Context, volumeUUIDs []string, conditions [][]interface{}) (map[string]int64, error)
		GetBackupCountByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (int64, error)
		GetBackupCountByVolumeVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error)
		GetDistinctBackupVaultIDsByVolumeUUID(ctx context.Context, volumeUUID string) ([]int64, error)
		GetDistinctBackupVaultServiceTypesByVaultIDs(ctx context.Context, backupVaultIDs []int64) ([]string, error)
		GetBackupsByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error)
		UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, excludeBackupUUID string) error
		GetLatestBackupByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.Backup, error)
		GetLatestBackupByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (*datamodel.Backup, error)
		GetLatestBackupsPerVaultByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error)
		GetLatestBackupsPerEndpointByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error)
		SumVolumeBackupChainBytes(ctx context.Context, volumeUUID string) (int64, error)
		UpdateBackupChainHistory(ctx context.Context, volumeUUID string, endpointUUID string, newSize int64) error
		DeleteBackupChainHistoryOlderThan(ctx context.Context, olderThan time.Time) (int64, error)
		GetBackupMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error)
		GetBackupChainMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error)
		GetDistinctVolumeGCBDRVaultPairs(ctx context.Context) ([]VolumeVaultPair, error)
		GetBackupResourceDataForAggregation(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination, freeTrialAccounts map[int64]*time.Time) ([]*datamodel.Backup, error)
		GetBackupMetadata(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupMetadata, error)
		ListBackupChainHistoriesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupChainHistory, error)
		// TODO: remove ListVolumesWithAccounts as it has been replaced by ListVolumesForTelemetryMetrics
		ListVolumesWithAccounts(ctx context.Context) ([]*datamodel.Volume, error)
		// ListVolumesForTelemetryMetrics retrieves volumes with only the fields required for telemetry metrics.
		// This is an optimized query that avoids JOINs with Account and Pool tables.
		ListVolumesForTelemetryMetrics(ctx context.Context) ([]*VolumeMetricsData, error)
		// ListExpertModeVolumesForTelemetryMetrics retrieves expert mode volumes with the fields required
		// for telemetry billing metrics. Account name and deployment name are resolved via JOINs.
		// Pass a non-nil Pagination to iterate in pages; nil returns all records.
		ListExpertModeVolumesForTelemetryMetrics(ctx context.Context, pagination *dbutils.Pagination) ([]*ExpertModeVolumeMetricsData, error)
		UpdateLatestBackupLogicalSize(ctx context.Context, volumeUUID string, endpointUUID string, newLogicalSize int64) error
		GetVolumeLatestBackupMap(ctx context.Context) (map[int64]*datamodel.VolumeLatestBackup, error)
		GetLatestBackupsGroupedByVolumeUUID(ctx context.Context) ([]datamodel.Backup, error)
		CreateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error)
		DeleteBackupMetadata(ctx context.Context, volumeUUID string) error
		GetBackupMetadataByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.BackupMetadata, error)
		UpdateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error)

		CreateSfrMetadata(ctx context.Context, sfrMetadata *datamodel.SfrMetadata) (*datamodel.SfrMetadata, error)
		GetSfrMetricsByTimeRange(ctx context.Context, startTime, endTime time.Time) (map[string]datamodel.SfrMetricsAggregate, error)
		GetSfrMetadataByJobID(ctx context.Context, jobID int64) (*datamodel.SfrMetadata, error)

		// GetBackupWithVaultByUUID fetches a backup by UUID including soft-deleted records, with BackupVault preloaded.
		GetBackupWithVaultByUUID(ctx context.Context, backupUUID string) (*datamodel.Backup, error)

		CreateAdminJobSpec(ctx context.Context, jobSpec *datamodel.AdminJobSpec) (*datamodel.AdminJobSpec, error)
		CreateAdminJobSpecIfNotExists(ctx context.Context, jobSpec *datamodel.AdminJobSpec) (*datamodel.AdminJobSpec, error)
		GetAdminJobSpecByJobType(ctx context.Context, jobType string) (*datamodel.AdminJobSpec, error)
		UpdateAdminJobSpec(ctx context.Context, jobSpec *datamodel.AdminJobSpec) error
		UpdateAdminJobSpecWithLock(ctx context.Context, jobType, state string, lockThreshold, currentTime time.Time) (int64, error)
		GetAdminJobSpecsByState(ctx context.Context, state string) ([]*datamodel.AdminJobSpec, error)

		CreateNodeNodeGroupMap(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error)
		GetNodeNodeGroupMap(ctx context.Context, id int64) (*datamodel.NodeNodeGroupMap, error)
		UpdateNodeNodeGroupMap(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error)
		DeleteNodeNodeGroupMap(ctx context.Context, id int64) error
		// Below one performs soft delete on nodenodegroupmap table
		DeleteNodeGroupMap(ctx context.Context, nodeGroupMap *datamodel.NodeNodeGroupMap) error
		GetNodeGroupMapNodeCount(ctx context.Context, nodeGroupID int64) (int64, error)
		GetNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64) (*datamodel.NodeNodeGroupMap, error)
		// GetActiveNodeNodeGroupMapByNodeID returns the latest node_node_group_map with deleted_at IS NULL (never Unscoped).
		// Pass tx from WithTransaction so reads see writes in the same transaction; pass nil to use the default connection.
		GetActiveNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64, tx dbutils.Transaction) (*datamodel.NodeNodeGroupMap, error)

		CreateNodeGroup(ctx context.Context, group *datamodel.NodeGroup) (*datamodel.NodeGroup, error)
		GetNodeGroup(ctx context.Context, id int64) (*datamodel.NodeGroup, error)
		UpdateNodeGroup(ctx context.Context, group *datamodel.NodeGroup) (*datamodel.NodeGroup, error)
		DeleteNodeGroup(ctx context.Context, id int64) error

		ErroredResource(ctx context.Context, resource interface{}, errorMessage string) (interface{}, error)
		GetBackupsByBackupVaultOwnerIDAndFilter(ctx context.Context, backupVaultUUID string, accountID int64, filters [][]interface{}) ([]*datamodel.Backup, error)
		GetBackupsByBackupVaultUUIDAndFilter(ctx context.Context, backupVaultUUID string, filters [][]interface{}) ([]*datamodel.Backup, error)

		// AssignTwoNodesToTwoGroups assigns two nodes to two different node groups, ensuring no group exceeds maxNodesPerGroup nodes
		// Assumes that node1 and node2 are precreated and have valid IDs
		AssignTwoNodesToTwoGroups(ctx context.Context, params datamodel.NodeGroupAssignmentParams) ([]*datamodel.NodeNodeGroupMap, error)
		ListNodeNodeGroupMap(ctx context.Context, includeDeleted bool, pagination *dbutils.Pagination) ([]*datamodel.NodeNodeGroupMap, error)
		// ListNodeNodeGroupMapAfterID returns non-deleted (or all if includeDeleted) records with id > afterID, ordered by id, limit. Used for keyset pagination.
		ListNodeNodeGroupMapAfterID(ctx context.Context, includeDeleted bool, afterID int64, limit int) ([]*datamodel.NodeNodeGroupMap, error)

		// Harvest poller rebalance planning
		ListNodeGroupsWithPollerCounts(ctx context.Context) ([]datamodel.NodeGroupPollerCount, error)
		ListNodeNodeGroupMapsByNodeGroupID(ctx context.Context, nodeGroupID int64) ([]*datamodel.NodeNodeGroupMap, error)
		GetHarvestHaSiblingNodeGroupID(ctx context.Context, nodeID int64) (int64, error)
		GetHarvestHaSiblingNodeID(ctx context.Context, nodeID int64) (int64, error)

		HardDeleteResourceByTable(ctx context.Context, table string, query string, id int64) error

		// Cluster upgrade methods
		CreateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) (*datamodel.ClusterUpgradeJob, error)
		GetClusterUpgradeJobByUUID(ctx context.Context, jobUUID string) (*datamodel.ClusterUpgradeJob, error)
		GetClusterUpgradeJobsByClusterID(ctx context.Context, clusterID string) ([]*datamodel.ClusterUpgradeJob, error)
		UpdateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) error

		// Image version methods
		CreateImageVersion(ctx context.Context, imageVersion *datamodel.ImageVersion) (*datamodel.ImageVersion, error)
		GetImageVersionByOntapVersion(ctx context.Context, ontapVersion string) (*datamodel.ImageVersion, error)
		ListImageVersions(ctx context.Context, activeOnly bool) ([]*datamodel.ImageVersion, error)
		UpdateImageVersion(ctx context.Context, imageVersion *datamodel.ImageVersion) error
		DeleteImageVersion(ctx context.Context, ontapVersion string) error

		CreatePendingResourceDeletion(ctx context.Context, resourceType, resourceName, errorMessage, accountName string, poolID int64) (*datamodel.PendingResourceDeletions, error)
		UpdatePendingResourceDeletion(ctx context.Context, resourceID int64, isDeletion bool, errorMessage string) (*datamodel.PendingResourceDeletions, error)

		CreateActiveDirectory(ctx context.Context, ad *datamodel.ActiveDirectory) (*datamodel.ActiveDirectory, error)
		UpdateActiveDirectory(ctx context.Context, ad *datamodel.ActiveDirectory) (*datamodel.ActiveDirectory, error)
		GetActiveDirectoryByNameAndAccountID(ctx context.Context, name string, accountID int64) (*datamodel.ActiveDirectory, error)
		GetActiveDirectoryByUuidAndAccountId(ctx context.Context, uuid string, accountID int64) (*datamodel.ActiveDirectory, error)

		// Cluster Peering methods
		GetClusterPeerByAccountIDExternalClusterAndPoolID(ctx context.Context, accountID int64, onPrempCluster string, poolID int64) (*datamodel.ClusterPeerings, error)
		GetClusterPeeringRowByID(ctx context.Context, clusterPeerID int64) (*datamodel.ClusterPeerings, error)
		CreateClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) (*datamodel.ClusterPeerings, error)
		UpdateClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) error
		ListClusterPeeringRowsByAccountID(ctx context.Context, accountID int64) ([]*datamodel.ClusterPeerings, error)
		// Active Directory methods
		GetActiveDirectoryByUUID(ctx context.Context, uuid string) (*datamodel.ActiveDirectory, error)
		ListActiveDirectories(ctx context.Context, accountID int64) ([]*datamodel.ActiveDirectory, error)
		GetMultipleActiveDirectoriesByUUIDs(ctx context.Context, uuids []string) ([]*datamodel.ActiveDirectory, error)
		DeleteClusterPeeringRow(ctx context.Context, clusterPeeringRow *datamodel.ClusterPeerings) error
		DeleteActiveDirectory(ctx context.Context, uuid string) error
		GetSVMsUsingActiveDirectory(ctx context.Context, adId int64) ([]*datamodel.Svm, error)
		GetActiveDirectoryForPoolByPoolID(ctx context.Context, poolID int64) (*datamodel.ActiveDirectory, error)
		ListClusterPeeringRowsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.ClusterPeerings, error)

		// Volume Performance Group (Manual QoS) methods
		// CreateVolumePerformanceGroupWithCap: lock pool, count VPGs, enforce maxCount, insert (maxCount <= 0 skips cap).
		CreateVolumePerformanceGroupWithCap(ctx context.Context, vpg *datamodel.VolumePerformanceGroup, maxCount int) (*datamodel.VolumePerformanceGroup, error)
		UpdateVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error
		UpdateVolumePerformanceGroupState(ctx context.Context, uuid, state, stateDetails string) error
		DeleteVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error
		HardDeleteVolumePerformanceGroup(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error
		GetVolumePerformanceGroupByUUID(ctx context.Context, uuid string) (*datamodel.VolumePerformanceGroup, error)
		GetVolumePerformanceGroupByID(ctx context.Context, id int64) (*datamodel.VolumePerformanceGroup, error)
		GetVolumePerformanceGroupByPoolAndName(ctx context.Context, poolID int64, name string) (*datamodel.VolumePerformanceGroup, error)
		ListVolumePerformanceGroupsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.VolumePerformanceGroup, error)
		CountVolumePerformanceGroupsByPoolID(ctx context.Context, poolID int64) (int64, error)
		GetVolumeCountByVolumePerformanceGroupID(ctx context.Context, vpgID int64) (int64, error)

		// Expert Mode Volume operations
		CreateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error)
		ListExpertModePools(ctx context.Context) ([]*datamodel.Pool, error)
		GetExpertModePoolUsedCapacityAndVolumeCount(ctx context.Context, poolID int64) (*ExpertModePoolCapacity, error)
		GetExpertModeVolumeByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.ExpertModeVolumes, error)
		GetExpertModeVolumeByUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error)
		GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error)
		UpdateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error)
		UpdateExpertModeVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error
		DeleteExpertModeVolume(ctx context.Context, volumeUUID string) error
		UpdateExpertModeVolumeDataProtection(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) error
		ListExpertModeVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.ExpertModeVolumes, error)
		GetActiveExpertModeVolumesCountByAccountID(ctx context.Context, accountID int64) (int64, error)
		GetEligibleExpertModeVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error)
		GetExpertModeBackupsByVolumeExternalUUID(ctx context.Context, volumeExternalUUID string) ([]*datamodel.Backup, error)
		GetAppConfig(ctx context.Context, key string) (*datamodel.AppConfig, error)
		UpsertAppConfig(ctx context.Context, key, value string) error

		// AddressRange
		CreateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error)
		GetAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error)
		ListAddressRanges(ctx context.Context, hostProjectNumber, vpcName string, arID, lifType *string) ([]*datamodel.AddressRange, error)
		UpdateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error)
		UpdateAddressRangeState(ctx context.Context, arID, state string, routeAggregationApplied *bool) (*datamodel.AddressRange, error)
		UpdateAddressRangeStateToCreatedIfLastPool(ctx context.Context, arUUID, network, excludePoolUUID, addressRangeCidr string) (bool, error)
		ResetAddressRangesInUseToCreated(ctx context.Context, hostProjectNumber, vpcName string) error
		DeleteAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error)

		// Regions
		CreateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error)
		ListRegions(ctx context.Context) ([]*datamodel.Region, error)
		UpdateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error)
		GetRegionByUUID(ctx context.Context, uuid string) (*datamodel.Region, error)
		GetRegionByName(ctx context.Context, name string) (*datamodel.Region, error)

		// Zones
		CreateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error)
		ListZones(ctx context.Context) ([]*datamodel.Zone, error)
		UpdateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error)

		// IPRange
		CreateIPRange(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error)
		GetIPRangeByUUID(ctx context.Context, irID string) (*datamodel.IPRange, error)
		GetIPRanges(ctx context.Context, accountUUID, network *string) ([]*datamodel.IPRange, error)
		UpdateIPRangeNetwork(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error)
		DeleteIPRange(ctx context.Context, irID string) (*datamodel.IPRange, error)

		// External cluster hosts
		CreateExternalCluster(ctx context.Context, cluster *datamodel.Cluster) (*datamodel.Cluster, error)
		GetExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error)
		UpdateExternalCluster(ctx context.Context, cluster *datamodel.Cluster) (*datamodel.Cluster, error)
		DeleteExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error)

		GetMultipleVolumesWithExpertMode(ctx context.Context, conditions [][]interface{}) ([]*datamodel.ExpertModeVolumes, error)
		ListExpertModeVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error)
	}
)

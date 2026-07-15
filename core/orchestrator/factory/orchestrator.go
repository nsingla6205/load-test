package factory

import (
	"context"

	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/client"
)

type OrchestratorFactory interface {
	CreatePool(ctx context.Context, params *commonparams.CreatePoolParams) (*models.Pool, string, error)
	CreateSvm(ctx context.Context, params *commonparams.CreateSvmParams) (operationID string, err error)
	DeleteSvm(ctx context.Context, params *commonparams.DeleteSvmParams) (operationID string, err error)
	UpdateSvm(ctx context.Context, params *commonparams.UpdateSvmParams) (operationID string, err error)
	UpdatePool(ctx context.Context, params *commonparams.UpdatePoolParams) (*models.Pool, string, error)
	DescribePool(ctx context.Context, poolId string, accountName string) (*models.Pool, error)
	GetExpertModePoolCreds(ctx context.Context, poolId string, accountName string, userName string) (*models.UserCredentials, error)
	GetDedicatedClusterPoolCreds(ctx context.Context, poolId string, accountName string, clusterExternalName string, userName string) (*models.UserCredentials, error)
	DeletePool(ctx context.Context, params *commonparams.DeletePoolParams) (*models.Pool, string, error)
	GetMultiplePools(ctx context.Context, accountName string, poolUUIDs []string) ([]*models.Pool, error)
	GetPoolsByUUIDs(ctx context.Context, poolUUIDs []string, opts commonparams.PoolFetchOptions) ([]*models.Pool, error)
	GetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*models.Snapshot, error)
	GetPoolByVendorID(ctx context.Context, vendorID string, accountName string) (*models.Pool, error)
	GetPoolByName(ctx context.Context, poolName string, accountName string, queryDepth int) (*models.Pool, error)
	ListPools(ctx context.Context, accountName string, includeDeleted bool) ([]*models.Pool, error)
	ListAllPools(ctx context.Context) ([]*models.Pool, error)
	GetPoolNodesAndCredentials(ctx context.Context, poolUUID string) ([]*datamodel.Node, *datamodel.PoolCredentials, error)

	CreateOntapHost(ctx context.Context, params *commonparams.CreateHostParams) (*datamodel.Host, error)
	GetHost(ctx context.Context, hostUUID string) (*datamodel.Host, error)
	ListHosts(ctx context.Context) ([]*datamodel.Host, error)
	UpdateHost(ctx context.Context, params *commonparams.UpdateHostParams) (*datamodel.Host, error)
	DeleteHost(ctx context.Context, hostUUID string) (*datamodel.Host, error)
	CreateHostGroup(ctx context.Context, params *commonparams.CreateHostGroupParams) (*models.HostGroup, error)
	GetHostGroup(ctx context.Context, hostGroupUUID string, accountID string) (*models.HostGroup, error)
	DeleteHostGroup(ctx context.Context, hostGroupUUID string, accountID string) (*models.HostGroup, error)
	UpdateHostGroup(ctx context.Context, params *commonparams.UpdateHostGroupParams) (*models.HostGroup, string, error)
	GetMultipleHostGroups(ctx context.Context, accountName string, hostGroupUUIDs []string) ([]*models.HostGroup, error)
	GetHostGroupsByUUIDs(ctx context.Context, hostGroupUUIDs []string) ([]*models.HostGroup, error)

	CreateVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error)
	CreateFlexCacheVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error)
	RevertVolume(ctx context.Context, params *commonparams.RevertVolumeParams) (*models.Volume, string, error)
	GetVolume(ctx context.Context, volumeId string, updateVolumeMetrics bool) (*models.Volume, error)
	GetVolumeByName(ctx context.Context, volumeName string, accountName string) (*models.Volume, error)
	UpdateVolume(ctx context.Context, param *commonparams.UpdateVolumeParams) (*models.Volume, string, error)
	UpdateVolumeV2(ctx context.Context, param *commonparams.UpdateVolumeParams) (*models.Volume, string, error)
	GetVolumeCount(ctx context.Context, projectNumber string) (int64, error)
	DeleteVolume(ctx context.Context, volumeId string) (*models.Volume, string, error)
	GetMultipleVolumes(ctx context.Context, volumeIds []string, accountName string) ([]*models.Volume, error)
	GetVolumesByUUIDs(ctx context.Context, volumeIds []string, opts commonparams.VolumeFetchOptions) ([]*models.Volume, error)
	ListVolumes(ctx context.Context, accountName string) ([]*models.Volume, error)
	EstablishFlexCacheVolumePeering(ctx context.Context, params *commonparams.EstablishVolumePeeringParams) (*models.Volume, string, error)
	EstablishReplicationPeering(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams) (*models.VolumeReplication, string, error)
	RestoreFilesFromBackup(ctx context.Context, params *commonparams.RestoreFilesFromBackupParams) (string, error)
	RestoreOntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error)
	SFROntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error)
	SplitStartVolume(ctx context.Context, params *commonparams.SplitStartVolumeParams) (*models.Volume, string, error)
	SplitStopVolume(ctx context.Context, params *commonparams.SplitStopVolumeParams) (*models.Volume, error)

	GetJob(ctx context.Context, operationId string) (*models.Job, error)
	GetReplicationJobs(ctx context.Context, projectName string, poolUUID string) ([]*models.Job, error)
	GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*models.Job, error)
	CreateJob(ctx context.Context, params *commonparams.CreateJobParams) (*datamodel.Job, error)
	UpdateJobStatus(ctx context.Context, jobID string, status string, trackingID int, errorDetails string) error
	UpdateJobAttributes(ctx context.Context, jobID string, jobAttributes *datamodel.JobAttributes) error

	CreateSnapshot(ctx context.Context, params *commonparams.CreateSnapshotParams) (*models.Snapshot, string, error)
	GetSnapshot(ctx context.Context, params *commonparams.GetSnapshotParams) (*models.Snapshot, error)
	DeleteSnapshot(ctx context.Context, params *commonparams.DeleteSnapshotParams) (*models.Snapshot, string, error)
	ListSnapshots(ctx context.Context, params *commonparams.ListSnapshotsParams) ([]*models.Snapshot, error)
	UpdateSnapshot(ctx context.Context, params *commonparams.UpdateSnapshotParams) (*models.Snapshot, string, error)
	GetMultipleSnapshots(ctx context.Context, VolumeUuId string, accountName string, snapshotUUIDs []string) ([]*models.Snapshot, error)
	DeleteSnapmirrorSnapshots(ctx context.Context, params *commonparams.SnapshotsInternalDeleteParams) (string, error)
	CreateQuotaRule(ctx context.Context, params *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, string, error)
	CreateQuotaRuleInternal(ctx context.Context, params *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error)
	UpdateQuotaRule(ctx context.Context, params *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, string, error)
	UpdateQuotaRuleInternal(ctx context.Context, params *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error)
	DeleteQuotaRule(ctx context.Context, params *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, string, error)
	DeleteQuotaRuleInternal(ctx context.Context, params *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error)
	ListQuotaRules(ctx context.Context, params *commonparams.ListQuotaRulesParams) ([]*models.QuotaRule, error)
	GetMultipleQuotaRules(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUIDs []string) ([]*models.QuotaRule, error)
	// GetQuotaRulesByUUIDs returns quota rules for the given UUIDs (batch list; no volume/account scope).
	GetQuotaRulesByUUIDs(ctx context.Context, quotaRuleUUIDs []string) ([]*models.QuotaRule, error)
	DescribeQuotaRule(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUID string) (*models.QuotaRule, error)
	CreateVolumeReplicationInternal(ctx context.Context, params *commonparams.CreateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error)
	GetReplicationCount(ctx context.Context, projectNumber string) (int64, error)
	CreateVolumeReplication(ctx context.Context, params *commonparams.CreateVolumeReplicationParams) (*models.VolumeReplication, string, error)
	UpdateVolumeReplicationInternal(ctx context.Context, params *commonparams.UpdateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error)
	UpdateVolumeReplicationAttributes(ctx context.Context, params models.UpdateVolumeReplicationAttributesParams) (*models.Job, error)
	UpdateVolumeReplicationState(ctx context.Context, params models.UpdateVolumeReplicationStateParams) (*models.VolumeReplication, error)
	GetMultipleReplicationsInternal(ctx context.Context, accountName string, replicationUUIDs []string) ([]*datamodel.VolumeReplication, error)
	GetMultipleReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error)
	GetBatchReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error)
	GetMultipleReplicationsByExternalUUID(ctx context.Context, params commonparams.GetMultipleReplicationsByExternalUUIDParams) ([]commonparams.ReplicationV1beta, error)
	AcceptClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, poolID string) (*commonparams.ClusterPeerParams, *datamodel.Job, error)
	PerformMountCheck(ctx context.Context, replicationUUID string, accountName string) (*models.Job, error)
	ResumeReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error)
	UpdateReplication(ctx context.Context, params *commonparams.UpdateReplicationParams) (*models.VolumeReplication, string, error)
	ResumeReplicationInternal(ctx context.Context, volumeReplicationId, accountName string, forceResume bool) (*models.VolumeReplication, *datamodel.Job, error)
	ReverseReplicationInternal(ctx context.Context, volumeReplicationId, accountName string) (*models.VolumeReplication, *datamodel.Job, error)
	GetReplication(ctx context.Context, volumeReplicationId string) (*models.VolumeReplication, error)
	ReleaseVolumeReplication(ctx context.Context, replicationUUID string) (*models.VolumeReplication, *datamodel.Job, error)
	DeleteReplicationInternal(ctx context.Context, volumeReplicationId string, cleanupAfterReverse bool, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error)
	StopReplicationInternal(ctx context.Context, replicationUUID string, accountName string, forceStop bool) (*models.VolumeReplication, *datamodel.Job, error)
	StopReplication(ctx context.Context, params *commonparams.StopReplicationParams) (*models.VolumeReplication, string, error)
	DeleteReplication(ctx context.Context, params *commonparams.DeleteReplicationParams, cleanupResourcesJobId string, isCleanUp bool) (*models.VolumeReplication, string, error)
	SyncReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error)
	ReverseAndResumeReplication(ctx context.Context, params *commonparams.ReverseAndResumeReplicationParams) (*models.VolumeReplication, *string, error)

	// KMS Config related methods
	gcp.KmsConfigInterface

	GetBackupVaultByNameAndOwnerID(ctx context.Context, bvName, ownerID string) (*models.BackupVaultV1beta, error)
	GetBackupPolicyByNameAndOwnerID(ctx context.Context, backupPolicyName, ownerID string) (*models.BackupPolicy, error)
	GetBackupPolicyByUUIDAndOwnerID(ctx context.Context, uuid string, ownerID string) (*models.BackupPolicy, error)
	CreateBackupPolicy(ctx context.Context, params *commonparams.CreateBackupPolicyParams) (*models.BackupPolicy, error)
	UpdateBackupPolicy(ctx context.Context, params *commonparams.UpdateBackupPolicyParams) (*models.BackupPolicy, string, error)
	ListBackupPoliciesAndVolumeCount(ctx context.Context, ownerID string, backupPolicyUUIDs []string) (map[string]int64, map[string]*models.BackupPolicy, error)
	// GetBackupPoliciesByUUIDs returns backup policies and volume counts for the given UUIDs across all accounts (batch APIs; no owner ID).
	GetBackupPoliciesByUUIDs(ctx context.Context, backupPolicyUUIDs []string) (map[string]int64, map[string]*models.BackupPolicy, error)
	DeleteBackupPolicy(ctx context.Context, params *commonparams.DeleteBackupPolicyParams) (*models.BackupPolicy, string, error)
	GetBackupPolicyUUIDsFromBackupVaultUUID(ctx context.Context, backupVaultUUID string, ownerId string) ([]string, error)
	ListBackupVaults(ctx context.Context, accountName string) ([]*models.BackupVaultV1beta, error)
	GetBackupVaultByUUID(ctx context.Context, bvUUID string, ownerID string) (*models.BackupVaultV1beta, error)
	GetBackupVaultByUUIDWithoutAccount(ctx context.Context, bvUUID string) (*models.BackupVaultV1beta, error)
	GetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error)
	UpdateBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error)
	GetMultipleBackupVaults(ctx context.Context, backupVaultUUIDList []string) ([]*models.BackupVaultV1beta, error)
	DeleteBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error)
	DeleteBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams) (string, error)
	UpdateBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams, useExternalUUID bool) (*models.BackupVaultV1beta, string, error)
	IsBackupVaultAttachedToVolume(ctx context.Context, backupVaultUUID string) (bool, error)
	GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountName string) ([]string, error)

	CreateBackupVaultEntryInVCP(ctx context.Context, bv *datamodel.BackupVault, params *commonparams.BackupVaultParams) (*datamodel.BackupVault, error)
	CreateBackupVault(ctx context.Context, params *commonparams.CreateBackupVaultParams) (*models.BackupVaultV1beta, error)
	GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, ownerID string) (*datamodel.BackupVault, error)

	// Accounts
	ListAccounts(ctx context.Context) ([]*datamodel.Account, error)
	GetAccountByUUID(ctx context.Context, accountUUID string) (*datamodel.Account, error)
	GetAccount(ctx context.Context, accountName string) (*datamodel.Account, error)
	// PersistAccountTrialMetadataIfSet validates trialMode when set and merges into account_metadata (VCP DB).
	PersistAccountTrialMetadataIfSet(ctx context.Context, accountName string, trial *commonparams.TrialModeParams) error
	UpdateResourceState(ctx context.Context, params *commonparams.UpdateResourceStateParams) (string, error)
	CreateBackup(ctx context.Context, params *commonparams.CreateBackupParams) (*models.Backup, string, error)
	CreateBackupInternal(ctx context.Context, params *commonparams.CreateBackupParams) (*models.Backup, string, error)
	GetBackup(ctx context.Context, params *commonparams.GetBackupParams) (*datamodel.Backup, error)
	GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error)
	DeleteBackup(ctx context.Context, params *commonparams.DeleteBackupParams) (*models.BaseModel, string, error)
	DeleteBackupInternal(ctx context.Context, params *commonparams.DeleteBackupParams) (string, error)
	ListBackups(ctx context.Context, backupVaultID, ownerID string, filters [][]interface{}) ([]*datamodel.Backup, error)
	ListBackupsWithoutAccountFilter(ctx context.Context, backupVaultID string, filters [][]interface{}) ([]*datamodel.Backup, error)
	UpdateBackup(ctx context.Context, params *commonparams.UpdateBackupParams) (*models.Backup, string, error)
	UpdateBackupInternal(ctx context.Context, params *commonparams.UpdateBackupParams) (*models.Backup, string, error)
	GetBackupsUnderBackupVault(ctx context.Context, backupVaultID, ownerID string, backupUUIDs []string) ([]*datamodel.Backup, error)
	UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, backupUUID string) error
	RotateCmekBackupsForBackupVault(ctx context.Context, params *commonparams.BackupVaultParams, primaryKeyVersion string) (string, error)

	CreateOrGetStartProjectEventJob(ctx context.Context, params *commonparams.StartProjectEventParams) (string, error)
	CreateOrGetFinishProjectEventJob(ctx context.Context, params *commonparams.FinishProjectEventParams) (string, error)

	CreateActiveDirectory(ctx context.Context, params *commonparams.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error)
	UpdateActiveDirectory(ctx context.Context, params *commonparams.UpdateActiveDirectoryParams) (*models.ActiveDirectory, string, error)

	// Cluster upgrade methods
	UpgradeCluster(ctx context.Context, params *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error)
	OnboardExternalClusters(ctx context.Context, params *commonparams.OnboardExternalClustersParams) ([]*datamodel.Cluster, error)
	GetExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error)
	UpdateExternalCluster(ctx context.Context, params *commonparams.UpdateExternalClusterParams) (*datamodel.Cluster, error)
	DeleteExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error)

	// Account host pinning (project-to-hosts referencing seeded hosts table rows)
	PinAccountHosts(ctx context.Context, params *commonparams.PinAccountHostsParams) (*models.SetAccountHostsResponse, error)
	GetClusterUpgradeStatus(ctx context.Context, jobUUID string) (*models.UpgradeProgress, error)
	HasActiveClusterUpgrade(ctx context.Context, clusterID string) (bool, error)
	ListAvailableVersions(ctx context.Context) (*models.ListAvailableVersionsResponse, error)
	CreateImageVersion(ctx context.Context, ontapVersion, vsaImagePath, vsaName, mediatorName string, isActive bool) (*datamodel.ImageVersion, error)
	DeleteImageVersion(ctx context.Context, ontapVersion string) error

	GetActiveDirectory(ctx context.Context, params *commonparams.GetADParams) (*models.ActiveDirectory, error)
	ListActiveDirectories(ctx context.Context, accountName string) ([]*models.ActiveDirectory, error)
	GetMultipleActiveDirectories(ctx context.Context, uuids []string) ([]*models.ActiveDirectory, error)
	BatchListActiveDirectories(ctx context.Context, params *commonparams.BatchListADsParams) ([]*models.ActiveDirectory, error)
	GetADConfig(ctx context.Context, params *commonparams.GetADParams) (*models.ActiveDirectory, error)
	GetSDEActiveDirectory(ctx context.Context, getADParams *commonparams.GetADParams) (*cvpmodels.ActiveDirectoryV1beta, error)
	DeleteActiveDirectory(ctx context.Context, params *commonparams.DeleteActiveDirectoryParams) (string, error)

	GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error)
	CreateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error
	UpdateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error
	DeleteExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error
	RenameExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeRenameParams) error
	StartExpertModeFlexCloneSplit(ctx context.Context, params *commonparams.ExpertModeFlexCloneSplitParams) error
	UpdateRbacForPools(ctx context.Context) (string, error)
	UpdateRbacForPoolById(ctx context.Context, params *commonparams.RefreshRbacForPoolParams) (string, error)
	RotateFabricPoolKeys(ctx context.Context, params *commonparams.RotateFabricPoolKeysParams) (workflowID string, err error)
	StartPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (workflowID string, err error)
	ExecutePoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (workflowID string, err error)
	FinishPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (workflowID string, err error)
	GetBackupConfigsForPool(ctx context.Context, poolID string, accountName string, locationId string) ([]*models.ExpertModeVolumeBackupConfig, error)
	ManageBackupConfigForExpertModeVolume(ctx context.Context, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) (*datamodel.DataProtection, string, error)

	// Volume Performance Group related methods
	CreateVolumePerformanceGroup(ctx context.Context, params *commonparams.CreateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error)
	ListVolumePerformanceGroups(ctx context.Context, params *commonparams.ListVolumePerformanceGroupsParams) ([]*models.VolumePerformanceGroup, error)
	GetVolumePerformanceGroup(ctx context.Context, params *commonparams.GetVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error)
	UpdateVolumePerformanceGroup(ctx context.Context, params *commonparams.UpdateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error)
	DeleteVolumePerformanceGroup(ctx context.Context, params *commonparams.DeleteVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error)

	// Quota rules
	ReplaceDstQuotaRulesWithSrc(ctx context.Context, req *commonparams.UpdateDstWithSrcQuotaRulesV1beta, params commonparams.V1betaUpdateDestinationQuotaRulesVCPParams) ([]*datamodel.QuotaRule, error)

	// Address ranges
	CreateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error)
	GetAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error)
	ListAddressRanges(ctx context.Context, hostProjectNumber, vpcName string, arID, lifType *string) ([]*datamodel.AddressRange, error)
	UpdateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error)
	UpdateAddressRangeState(ctx context.Context, arID, state string, routeAggregationApplied *bool) (*datamodel.AddressRange, error)
	DeleteAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error)

	// Regions
	CreateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error)
	ListRegions(ctx context.Context) ([]*datamodel.Region, error)
	UpdateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, string, error)

	// Zones
	CreateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error)
	ListZones(ctx context.Context) ([]*datamodel.Zone, error)
	UpdateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error)

	// IP ranges
	CreateIPRange(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error)
	GetIPRanges(ctx context.Context, accountUUID, network *string) ([]*datamodel.IPRange, error)
	UpdateIPRangeNetwork(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error)
	DeleteIPRange(ctx context.Context, irID string) (*datamodel.IPRange, error)
}

// GetOrchestratorForProvider returns the appropriate orchestrator implementation based on provider
// This is used by google-proxy to get the right orchestrator for the hyperscaler
func GetOrchestratorForProvider(storage database.Storage, temporalClient client.Client) OrchestratorFactory {
	hyperscaler := env.GetHyperscaler()
	switch hyperscaler {
	case commonparams.ProviderOCI:
		return oci.NewOCIOrchestrator(storage, temporalClient)
	case commonparams.ProviderGCP:
		fallthrough
	default:
		// GCP is the default for backward compatibility
		return gcp.NewGCPOrchestrator(storage, temporalClient)
	}
}

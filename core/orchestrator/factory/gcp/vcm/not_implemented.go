package vcm

import (
	"context"

	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// notImplementedOrchestrator stubs every OrchestratorFactory method as not implemented.
type notImplementedOrchestrator struct{}

// Pools
func (notImplementedOrchestrator) CreatePool(context.Context, *commonparams.CreatePoolParams) (*models.Pool, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateSvm(context.Context, *commonparams.CreateSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteSvm(context.Context, *commonparams.DeleteSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateSvm(context.Context, *commonparams.UpdateSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdatePool(context.Context, *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) EncryptPool(context.Context, *commonparams.EncryptPoolParams) (*models.Pool, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DescribePool(context.Context, string, string) (*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetExpertModePoolCreds(context.Context, string, string, string) (*models.UserCredentials, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetDedicatedClusterPoolCreds(context.Context, string, string, string, string) (*models.UserCredentials, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeletePool(context.Context, *commonparams.DeletePoolParams) (*models.Pool, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultiplePools(context.Context, string, []string) ([]*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetPoolsByUUIDs(context.Context, []string, commonparams.PoolFetchOptions) ([]*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetSnapshotsByUUIDs(context.Context, []string) ([]*models.Snapshot, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetPoolByVendorID(context.Context, string, string) (*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetPoolWithNodes(context.Context, string) (*datamodel.Pool, []*datamodel.Node, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetPoolByName(context.Context, string, string, int) (*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListPools(context.Context, string, bool) ([]*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListAllPools(context.Context) ([]*models.Pool, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetPoolNodesAndCredentials(context.Context, string) ([]*datamodel.Node, *datamodel.PoolCredentials, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

// Hosts and host groups
func (notImplementedOrchestrator) CreateOntapHost(context.Context, *commonparams.CreateHostParams) (*datamodel.Host, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetHost(context.Context, string) (*datamodel.Host, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListHosts(context.Context) ([]*datamodel.Host, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateHost(context.Context, *commonparams.UpdateHostParams) (*datamodel.Host, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteHost(context.Context, string) (*datamodel.Host, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateHostGroup(context.Context, *commonparams.CreateHostGroupParams) (*models.HostGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetHostGroup(context.Context, string, string) (*models.HostGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteHostGroup(context.Context, string, string) (*models.HostGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateHostGroup(context.Context, *commonparams.UpdateHostGroupParams) (*models.HostGroup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleHostGroups(context.Context, string, []string) ([]*models.HostGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetHostGroupsByUUIDs(context.Context, []string) ([]*models.HostGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Volumes
func (notImplementedOrchestrator) CreateVolume(context.Context, *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateFlexCacheVolume(context.Context, *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RevertVolume(context.Context, *commonparams.RevertVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetVolume(context.Context, string, bool) (*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetVolumeByName(context.Context, string, string) (*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolume(context.Context, *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumeV2(context.Context, *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetVolumeCount(context.Context, string) (int64, error) {
	return 0, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteVolume(context.Context, string) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleVolumes(context.Context, []string, string) ([]*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetVolumesByUUIDs(context.Context, []string, commonparams.VolumeFetchOptions) ([]*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListVolumes(context.Context, string) ([]*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) EstablishFlexCacheVolumePeering(context.Context, *commonparams.EstablishVolumePeeringParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) EstablishReplicationPeering(context.Context, *commonparams.EstablishReplicationPeeringParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RestoreFilesFromBackup(context.Context, *commonparams.RestoreFilesFromBackupParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RestoreOntapModeBackup(context.Context, *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) SFROntapModeBackup(context.Context, *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) SplitStartVolume(context.Context, *commonparams.SplitStartVolumeParams) (*models.Volume, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) SplitStopVolume(context.Context, *commonparams.SplitStopVolumeParams) (*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumeDatabaseValue(context.Context, string, *commonparams.UpdateVolumeDatabaseValueParams) (*models.Volume, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Jobs
func (notImplementedOrchestrator) GetJob(context.Context, string) (*models.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetReplicationJobs(context.Context, string, string) ([]*models.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetJobByResourceUUID(context.Context, string, string) (*models.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateJob(context.Context, *commonparams.CreateJobParams) (*datamodel.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateJobStatus(context.Context, string, string, int, string) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateJobAttributes(context.Context, string, *datamodel.JobAttributes) error {
	return customerrors.NewNotImplementedYetErr()
}

// Snapshots and quota rules
func (notImplementedOrchestrator) CreateSnapshot(context.Context, *commonparams.CreateSnapshotParams) (*models.Snapshot, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetSnapshot(context.Context, *commonparams.GetSnapshotParams) (*models.Snapshot, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteSnapshot(context.Context, *commonparams.DeleteSnapshotParams) (*models.Snapshot, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListSnapshots(context.Context, *commonparams.ListSnapshotsParams) ([]*models.Snapshot, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateSnapshot(context.Context, *commonparams.UpdateSnapshotParams) (*models.Snapshot, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleSnapshots(context.Context, string, string, []string) ([]*models.Snapshot, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteSnapmirrorSnapshots(context.Context, *commonparams.SnapshotsInternalDeleteParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateQuotaRule(context.Context, *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateQuotaRuleInternal(context.Context, *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateQuotaRule(context.Context, *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateQuotaRuleInternal(context.Context, *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteQuotaRule(context.Context, *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteQuotaRuleInternal(context.Context, *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListQuotaRules(context.Context, *commonparams.ListQuotaRulesParams) ([]*models.QuotaRule, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleQuotaRules(context.Context, string, string, []string) ([]*models.QuotaRule, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetQuotaRulesByUUIDs(context.Context, []string) ([]*models.QuotaRule, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DescribeQuotaRule(context.Context, string, string, string) (*models.QuotaRule, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Replication
func (notImplementedOrchestrator) CreateVolumeReplicationInternal(context.Context, *commonparams.CreateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetReplicationCount(context.Context, string) (int64, error) {
	return 0, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateVolumeReplication(context.Context, *commonparams.CreateVolumeReplicationParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumeReplicationInternal(context.Context, *commonparams.UpdateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumeReplicationAttributes(context.Context, models.UpdateVolumeReplicationAttributesParams) (*models.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumeReplicationState(context.Context, models.UpdateVolumeReplicationStateParams) (*models.VolumeReplication, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleReplicationsInternal(context.Context, string, []string) ([]*datamodel.VolumeReplication, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleReplications(context.Context, commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBatchReplications(context.Context, commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleReplicationsByExternalUUID(context.Context, commonparams.GetMultipleReplicationsByExternalUUIDParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) AcceptClusterPeer(context.Context, *commonparams.ClusterPeerParams, string) (*commonparams.ClusterPeerParams, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) PerformMountCheck(context.Context, string, string) (*models.Job, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ResumeReplication(context.Context, *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateReplication(context.Context, *commonparams.UpdateReplicationParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ResumeReplicationInternal(context.Context, string, string, bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ReverseReplicationInternal(context.Context, string, string) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetReplication(context.Context, string) (*models.VolumeReplication, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ReleaseVolumeReplication(context.Context, string) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteReplicationInternal(context.Context, string, bool, bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) StopReplicationInternal(context.Context, string, string, bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) StopReplication(context.Context, *commonparams.StopReplicationParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteReplication(context.Context, *commonparams.DeleteReplicationParams, string, bool) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) SyncReplication(context.Context, *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ReverseAndResumeReplication(context.Context, *commonparams.ReverseAndResumeReplicationParams) (*models.VolumeReplication, *string, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

// KMS config (gcp.KmsConfigInterface)
func (notImplementedOrchestrator) CreateKmsConfig(context.Context, *commonparams.CreateKmsConfigParams) (*models.KmsConfig, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetKmsConfig(context.Context, *commonparams.GetKmsConfigParams) (*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetExistingKmsConfig(context.Context, *commonparams.GetKmsConfigParams) (*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleKMSConfigs(context.Context, []string) ([]*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetKmsConfigsByUUIDs(context.Context, []string) ([]*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListKmsConfigs(context.Context, string) ([]*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateKmsConfig(context.Context, *commonparams.UpdateKmsConfigParams) (*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CheckAndUpdateKmsConfigHealth(context.Context, *models.KmsConfigCheck) (*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) AccessCryptoKeyAndEncryptDataWithImpersonation(context.Context, *models.KmsConfig) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteKmsConfig(context.Context, *commonparams.DeleteKmsConfigParams) (*models.KmsConfig, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) MigrateKmsConfig(context.Context, *commonparams.MigrateKmsConfigParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RotateKmsConfig(context.Context, *commonparams.RotateKmsConfigParams) (*models.KmsConfig, *models.Job, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateAndSyncKmsConfig(context.Context, *commonparams.CreateKmsConfigParams) (*models.KmsConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetSDEKmsConfiguration(context.Context, *commonparams.GetKmsConfigParams) (*cvpmodels.KmsConfigV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Backup policies and vaults
func (notImplementedOrchestrator) GetBackupVaultByNameAndOwnerID(context.Context, string, string) (*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupPolicyByNameAndOwnerID(context.Context, string, string) (*models.BackupPolicy, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupPolicyByUUIDAndOwnerID(context.Context, string, string) (*models.BackupPolicy, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateBackupPolicy(context.Context, *commonparams.CreateBackupPolicyParams) (*models.BackupPolicy, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackupPolicy(context.Context, *commonparams.UpdateBackupPolicyParams) (*models.BackupPolicy, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListBackupPoliciesAndVolumeCount(context.Context, string, []string) (map[string]int64, map[string]*models.BackupPolicy, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupPoliciesByUUIDs(context.Context, []string) (map[string]int64, map[string]*models.BackupPolicy, error) {
	return nil, nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteBackupPolicy(context.Context, *commonparams.DeleteBackupPolicyParams) (*models.BackupPolicy, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupPolicyUUIDsFromBackupVaultUUID(context.Context, string, string) ([]string, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListBackupVaults(context.Context, string) ([]*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupVaultByUUID(context.Context, string, string) (*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupVaultByUUIDWithoutAccount(context.Context, string) (*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupsByUUIDs(context.Context, []string) ([]*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackupVault(context.Context, *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleBackupVaults(context.Context, []string) ([]*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteBackupVault(context.Context, *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteBackupVaultInternal(context.Context, *commonparams.BackupVaultParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackupVaultInternal(context.Context, *commonparams.BackupVaultParams, bool) (*models.BackupVaultV1beta, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) IsBackupVaultAttachedToVolume(context.Context, string) (bool, error) {
	return false, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupVaultUUIDsFromBackupPolicyUUID(context.Context, string, string) ([]string, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateBackupVaultEntryInVCP(context.Context, *datamodel.BackupVault, *commonparams.BackupVaultParams) (*datamodel.BackupVault, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) BackfillBackupVaultInVCP(context.Context, *datamodel.BackupVault, string) (*datamodel.BackupVault, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateBackupVault(context.Context, *commonparams.CreateBackupVaultParams) (*models.BackupVaultV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupVaultByExternalUUIDAndOwnerID(context.Context, string, string) (*datamodel.BackupVault, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Accounts and backups
func (notImplementedOrchestrator) ListAccounts(context.Context) ([]*datamodel.Account, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetAccountByUUID(context.Context, string) (*datamodel.Account, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetAccount(context.Context, string) (*datamodel.Account, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) PersistAccountTrialMetadataIfSet(context.Context, string, *commonparams.TrialModeParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateResourceState(context.Context, *commonparams.UpdateResourceStateParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateBackup(context.Context, *commonparams.CreateBackupParams) (*models.Backup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateBackupInternal(context.Context, *commonparams.CreateBackupParams) (*models.Backup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackup(context.Context, *commonparams.GetBackupParams) (*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupByExternalUUID(context.Context, string, string, string) (*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteBackup(context.Context, *commonparams.DeleteBackupParams) (*models.BaseModel, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteBackupInternal(context.Context, *commonparams.DeleteBackupParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListBackups(context.Context, string, string, [][]interface{}) ([]*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListBackupsWithoutAccountFilter(context.Context, string, [][]interface{}) ([]*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackup(context.Context, *commonparams.UpdateBackupParams) (*models.Backup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackupInternal(context.Context, *commonparams.UpdateBackupParams) (*models.Backup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupsUnderBackupVault(context.Context, string, string, []string) ([]*datamodel.Backup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateBackupLatestLogicalBackupSizeByVolume(context.Context, string, string) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RotateCmekBackupsForBackupVault(context.Context, *commonparams.BackupVaultParams, string) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// Project events
func (notImplementedOrchestrator) CreateOrGetStartProjectEventJob(context.Context, *commonparams.StartProjectEventParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateOrGetFinishProjectEventJob(context.Context, *commonparams.FinishProjectEventParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// Active Directory
func (notImplementedOrchestrator) CreateActiveDirectory(context.Context, *commonparams.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateActiveDirectory(context.Context, *commonparams.UpdateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DiagnoseActiveDirectory(context.Context, *commonparams.DiagnoseActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

// Cluster upgrade and external clusters
func (notImplementedOrchestrator) UpgradeCluster(context.Context, *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) OnboardExternalClusters(context.Context, *commonparams.OnboardExternalClustersParams) ([]*datamodel.Cluster, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetExternalCluster(context.Context, string) (*datamodel.Cluster, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateExternalCluster(context.Context, *commonparams.UpdateExternalClusterParams) (*datamodel.Cluster, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteExternalCluster(context.Context, string) (*datamodel.Cluster, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) PinAccountHosts(context.Context, *commonparams.PinAccountHostsParams) (*models.SetAccountHostsResponse, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetClusterUpgradeStatus(context.Context, string) (*models.UpgradeProgress, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) HasActiveClusterUpgrade(context.Context, string) (bool, error) {
	return false, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListAvailableVersions(context.Context) (*models.ListAvailableVersionsResponse, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateImageVersion(context.Context, string, string, string, string, bool) (*datamodel.ImageVersion, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteImageVersion(context.Context, string) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetActiveDirectory(context.Context, *commonparams.GetADParams) (*models.ActiveDirectory, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListActiveDirectories(context.Context, string) ([]*models.ActiveDirectory, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetMultipleActiveDirectories(context.Context, []string) ([]*models.ActiveDirectory, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) BatchListActiveDirectories(context.Context, *commonparams.BatchListADsParams) ([]*models.ActiveDirectory, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetADConfig(context.Context, *commonparams.GetADParams) (*models.ActiveDirectory, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetSDEActiveDirectory(context.Context, *commonparams.GetADParams) (*cvpmodels.ActiveDirectoryV1beta, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteActiveDirectory(context.Context, *commonparams.DeleteActiveDirectoryParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// Expert mode volumes and pool maintenance
func (notImplementedOrchestrator) GetExpertModeVolumeByExternalUUID(context.Context, string) (*datamodel.ExpertModeVolumes, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) CreateExpertModeVolume(context.Context, *commonparams.ExpertModeVolumeParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateExpertModeVolume(context.Context, *commonparams.ExpertModeVolumeParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteExpertModeVolume(context.Context, *commonparams.ExpertModeVolumeParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RenameExpertModeVolume(context.Context, *commonparams.ExpertModeVolumeRenameParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) StartExpertModeFlexCloneSplit(context.Context, *commonparams.ExpertModeFlexCloneSplitParams) error {
	return customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListExpertModeVolumeTenancy(context.Context, commonparams.ListExpertModeVolumeTenancyParams) ([]commonparams.ExpertModeVolumeTenancy, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateRbacForPools(context.Context) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateRbacForPoolById(context.Context, *commonparams.RefreshRbacForPoolParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) RotateFabricPoolKeys(context.Context, *commonparams.RotateFabricPoolKeysParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) StartPoolMaintenance(context.Context, *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ExecutePoolMaintenance(context.Context, *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) FinishPoolMaintenance(context.Context, *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBackupConfigsForPool(context.Context, string, string, string) ([]*models.ExpertModeVolumeBackupConfig, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ManageBackupConfigForExpertModeVolume(context.Context, *commonparams.ManageBackupConfigForExpertModeVolumeParams) (*datamodel.DataProtection, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

// Volume performance groups
func (notImplementedOrchestrator) CreateVolumePerformanceGroup(context.Context, *commonparams.CreateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListVolumePerformanceGroups(context.Context, *commonparams.ListVolumePerformanceGroupsParams) ([]*models.VolumePerformanceGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetVolumePerformanceGroup(context.Context, *commonparams.GetVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateVolumePerformanceGroup(context.Context, *commonparams.UpdateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteVolumePerformanceGroup(context.Context, *commonparams.DeleteVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ReplaceDstQuotaRulesWithSrc(context.Context, *commonparams.UpdateDstWithSrcQuotaRulesV1beta, commonparams.V1betaUpdateDestinationQuotaRulesVCPParams) ([]*datamodel.QuotaRule, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Address ranges
func (notImplementedOrchestrator) CreateAddressRange(context.Context, *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetAddressRange(context.Context, string) (*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListAddressRanges(context.Context, string, string, *string, *string) ([]*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateAddressRange(context.Context, *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateAddressRangeState(context.Context, string, string, *bool) (*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteAddressRange(context.Context, string) (*datamodel.AddressRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Regions
func (notImplementedOrchestrator) CreateRegion(context.Context, *datamodel.Region) (*datamodel.Region, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListRegions(context.Context) ([]*datamodel.Region, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateRegion(context.Context, *datamodel.Region) (*datamodel.Region, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

// Zones
func (notImplementedOrchestrator) CreateZone(context.Context, *datamodel.Zone, string) (*datamodel.Zone, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) ListZones(context.Context) ([]*datamodel.Zone, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateZone(context.Context, *datamodel.Zone, string) (*datamodel.Zone, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// IP ranges
func (notImplementedOrchestrator) CreateIPRange(context.Context, *datamodel.IPRange) (*datamodel.IPRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetIPRanges(context.Context, *string, *string) ([]*datamodel.IPRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) UpdateIPRangeNetwork(context.Context, *datamodel.IPRange) (*datamodel.IPRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) DeleteIPRange(context.Context, string) (*datamodel.IPRange, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// SVMs
func (notImplementedOrchestrator) ListSvmsForHardwareClusters(context.Context) ([]*datamodel.SvmView, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// Batch cluster upgrades
func (notImplementedOrchestrator) BatchUpgradeClusters(context.Context, *commonparams.BatchUpgradeClusterParams) (*models.BatchClusterUpgradeResponse, string, error) {
	return nil, "", customerrors.NewNotImplementedYetErr()
}

func (notImplementedOrchestrator) GetBatchUpgradeStatus(context.Context, string) (*models.UpgradeProgress, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

// OCI administration
func (notImplementedOrchestrator) ResetAdminPasswordForPool(context.Context, *commonparams.ResetOciAdminPasswordParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// Inventory
func (notImplementedOrchestrator) GetProjectInventory(context.Context, *commonparams.GetProjectInventoryParams) (*commonparams.ProjectInventoryResult, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

func (V VCMOrchestrator) GetInterclusterLifs(ctx context.Context, accountUUID, ipRangeUUID *string) ([]*datamodel.Lif, error) {
	return nil, customerrors.NewNotImplementedYetErr()
}

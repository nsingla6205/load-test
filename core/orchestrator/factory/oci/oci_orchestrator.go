package oci

import (
	"context"
	"fmt"

	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

// OCIOrchestrator is the OCI implementation of OrchestratorFactory
// It overrides CreatePool with OCI-specific logic
type OCIOrchestrator struct {
	storage  database.Storage
	temporal client.Client
}

// NewOCIOrchestrator creates a new OCI orchestrator
func NewOCIOrchestrator(storage database.Storage, temporalClient client.Client) *OCIOrchestrator {
	return &OCIOrchestrator{
		storage:  storage,
		temporal: temporalClient,
	}
}

// GetNodesByPoolUUID returns the nodes belonging to the pool identified by
// poolUUID. A missing pool is reported as (nil, nil) — best-effort callers
// (e.g. the workflow-query metadata enrichment in oci-proxy) can then skip
// enrichment without surfacing a noisy "pool not found" error to the user.
// Any other storage failure is wrapped with the operation name so the layer
// of origin is visible in logs.
func (o *OCIOrchestrator) GetNodesByPoolUUID(ctx context.Context, poolUUID string) ([]*datamodel.Node, error) {
	pool, err := o.storage.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		if utilserrors.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetNodesByPoolUUID: lookup pool %q: %w", poolUUID, err)
	}
	nodes, err := o.storage.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, fmt.Errorf("GetNodesByPoolUUID: list nodes for pool %q (id=%d): %w", poolUUID, pool.ID, err)
	}
	return nodes, nil
}

func (o *OCIOrchestrator) GetPoolsByUUIDs(ctx context.Context, poolUUIDs []string, opts commonparams.PoolFetchOptions) ([]*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*models.Snapshot, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdatePool(ctx context.Context, params *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	return o.updatePool(ctx, params)
}

func (o *OCIOrchestrator) DescribePool(ctx context.Context, poolId string, accountName string) (*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetExpertModePoolCreds(ctx context.Context, poolId string, accountName string, userName string) (*models.UserCredentials, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultiplePools(ctx context.Context, accountName string, poolUUIDs []string) ([]*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetPoolByVendorID(ctx context.Context, vendorID string, accountName string) (*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetPoolByName(ctx context.Context, poolName string, accountName string, queryDepth int) (*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListPools(ctx context.Context, accountName string, includeDeleted bool) ([]*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListAllPools(ctx context.Context) ([]*models.Pool, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateHostGroup(ctx context.Context, params *commonparams.CreateHostGroupParams) (*models.HostGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetHostGroup(ctx context.Context, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteHostGroup(ctx context.Context, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateHostGroup(ctx context.Context, params *commonparams.UpdateHostGroupParams) (*models.HostGroup, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleHostGroups(ctx context.Context, accountName string, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetHostGroupsByUUIDs(ctx context.Context, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateFlexCacheVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RevertVolume(ctx context.Context, params *commonparams.RevertVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetVolume(ctx context.Context, volumeId string, updateVolumeMetrics bool) (*models.Volume, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolume(ctx context.Context, param *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolumeV2(ctx context.Context, param *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetVolumeCount(ctx context.Context, projectNumber string) (int64, error) {
	return 0, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteVolume(ctx context.Context, volumeId string) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleVolumes(ctx context.Context, volumeIds []string, accountName string) ([]*models.Volume, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetVolumesByUUIDs(ctx context.Context, volumeIds []string, opts commonparams.VolumeFetchOptions) ([]*models.Volume, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListVolumes(ctx context.Context, accountName string) ([]*models.Volume, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) EstablishFlexCacheVolumePeering(ctx context.Context, params *commonparams.EstablishVolumePeeringParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) EstablishReplicationPeering(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RestoreFilesFromBackup(ctx context.Context, params *commonparams.RestoreFilesFromBackupParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RestoreOntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) SFROntapModeBackup(ctx context.Context, params *commonparams.RestoreOntapModeBackupParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetJob(ctx context.Context, operationId string) (*models.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetReplicationJobs(ctx context.Context, projectName string, poolUUID string) ([]*models.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*models.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateJob(ctx context.Context, params *commonparams.CreateJobParams) (*datamodel.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateJobStatus(ctx context.Context, jobID string, status string, trackingID int, errorDetails string) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateJobAttributes(ctx context.Context, jobID string, jobAttributes *datamodel.JobAttributes) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateSnapshot(ctx context.Context, params *commonparams.CreateSnapshotParams) (*models.Snapshot, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetSnapshot(ctx context.Context, params *commonparams.GetSnapshotParams) (*models.Snapshot, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteSnapshot(ctx context.Context, params *commonparams.DeleteSnapshotParams) (*models.Snapshot, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListSnapshots(ctx context.Context, params *commonparams.ListSnapshotsParams) ([]*models.Snapshot, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateSnapshot(ctx context.Context, params *commonparams.UpdateSnapshotParams) (*models.Snapshot, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleSnapshots(ctx context.Context, VolumeUuId string, accountName string, snapshotUUIDs []string) ([]*models.Snapshot, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteSnapmirrorSnapshots(ctx context.Context, params *commonparams.SnapshotsInternalDeleteParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateQuotaRule(ctx context.Context, params *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateQuotaRuleInternal(ctx context.Context, params *commonparams.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateQuotaRule(ctx context.Context, params *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateQuotaRuleInternal(ctx context.Context, params *commonparams.UpdateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteQuotaRule(ctx context.Context, params *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteQuotaRuleInternal(ctx context.Context, params *commonparams.DeleteQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListQuotaRules(ctx context.Context, params *commonparams.ListQuotaRulesParams) ([]*models.QuotaRule, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleQuotaRules(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUIDs []string) ([]*models.QuotaRule, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetQuotaRulesByUUIDs(ctx context.Context, quotaRuleUUIDs []string) ([]*models.QuotaRule, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DescribeQuotaRule(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUID string) (*models.QuotaRule, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateVolumeReplicationInternal(ctx context.Context, params *commonparams.CreateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetReplicationCount(ctx context.Context, projectNumber string) (int64, error) {
	return 0, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateVolumeReplication(ctx context.Context, params *commonparams.CreateVolumeReplicationParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolumeReplicationInternal(ctx context.Context, params *commonparams.UpdateVolumeReplicationInternalParams) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolumeReplicationAttributes(ctx context.Context, params models.UpdateVolumeReplicationAttributesParams) (*models.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolumeReplicationState(ctx context.Context, params models.UpdateVolumeReplicationStateParams) (*models.VolumeReplication, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleReplicationsInternal(ctx context.Context, accountName string, replicationUUIDs []string) ([]*datamodel.VolumeReplication, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBatchReplications(ctx context.Context, params commonparams.GetMultipleReplicationsParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleReplicationsByExternalUUID(ctx context.Context, params commonparams.GetMultipleReplicationsByExternalUUIDParams) ([]commonparams.ReplicationV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) AcceptClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, poolID string) (*commonparams.ClusterPeerParams, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) PerformMountCheck(ctx context.Context, replicationUUID string, accountName string) (*models.Job, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ResumeReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateReplication(ctx context.Context, params *commonparams.UpdateReplicationParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ResumeReplicationInternal(ctx context.Context, volumeReplicationId, accountName string, forceResume bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ReverseReplicationInternal(ctx context.Context, volumeReplicationId, accountName string) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetReplication(ctx context.Context, volumeReplicationId string) (*models.VolumeReplication, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ReleaseVolumeReplication(ctx context.Context, replicationUUID string) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteReplicationInternal(ctx context.Context, volumeReplicationId string, cleanupAfterReverse bool, isCleanup bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) StopReplicationInternal(ctx context.Context, replicationUUID string, accountName string, forceStop bool) (*models.VolumeReplication, *datamodel.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) StopReplication(ctx context.Context, params *commonparams.StopReplicationParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteReplication(ctx context.Context, params *commonparams.DeleteReplicationParams, cleanupResourcesJobId string, isCleanUp bool) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) SyncReplication(ctx context.Context, params *commonparams.ResumeReplicationParams) (*models.VolumeReplication, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ReverseAndResumeReplication(ctx context.Context, params *commonparams.ReverseAndResumeReplicationParams) (*models.VolumeReplication, *string, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateKmsConfig(ctx context.Context, params *commonparams.CreateKmsConfigParams) (*models.KmsConfig, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetKmsConfig(ctx context.Context, params *commonparams.GetKmsConfigParams) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetKmsConfigByKeyFullPath(ctx context.Context, params *commonparams.GetKmsConfigParams) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleKMSConfigs(ctx context.Context, kmsConfigIDList []string) ([]*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetKmsConfigsByUUIDs(
	ctx context.Context,
	kmsConfigUUIDs []string,
) ([]*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateKmsConfig(ctx context.Context, params *commonparams.UpdateKmsConfigParams) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CheckAndUpdateKmsConfigHealth(ctx context.Context, params *models.KmsConfigCheck) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) AccessCryptoKeyAndEncryptDataWithImpersonation(ctx context.Context, kmsConfig *models.KmsConfig) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteKmsConfig(ctx context.Context, params *commonparams.DeleteKmsConfigParams) (*models.KmsConfig, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) MigrateKmsConfig(ctx context.Context, params *commonparams.MigrateKmsConfigParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RotateKmsConfig(ctx context.Context, params *commonparams.RotateKmsConfigParams) (*models.KmsConfig, *models.Job, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateAndSyncKmsConfig(ctx context.Context, params *commonparams.CreateKmsConfigParams) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetSDEKmsConfiguration(ctx context.Context, params *commonparams.GetKmsConfigParams) (*cvpmodels.KmsConfigV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetExistingKmsConfig(ctx context.Context, params *commonparams.GetKmsConfigParams) (*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListKmsConfigs(ctx context.Context, accountName string) ([]*models.KmsConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupVaultByNameAndOwnerID(ctx context.Context, bvName, ownerID string) (*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupPolicyByNameAndOwnerID(ctx context.Context, backupPolicyName, ownerID string) (*models.BackupPolicy, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupPolicyByUUIDAndOwnerID(ctx context.Context, uuid string, ownerID string) (*models.BackupPolicy, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackupPolicy(ctx context.Context, params *commonparams.CreateBackupPolicyParams) (*models.BackupPolicy, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackupPolicy(ctx context.Context, params *commonparams.UpdateBackupPolicyParams) (*models.BackupPolicy, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListBackupPoliciesAndVolumeCount(ctx context.Context, ownerID string, backupPolicyUUIDs []string) (map[string]int64, map[string]*models.BackupPolicy, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupPoliciesByUUIDs(ctx context.Context, backupPolicyUUIDs []string) (map[string]int64, map[string]*models.BackupPolicy, error) {
	return nil, nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteBackupPolicy(ctx context.Context, params *commonparams.DeleteBackupPolicyParams) (*models.BackupPolicy, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupPolicyUUIDsFromBackupVaultUUID(ctx context.Context, backupVaultUUID string, ownerId string) ([]string, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListBackupVaults(ctx context.Context, accountName string) ([]*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupVaultByUUID(ctx context.Context, bvUUID string, ownerID string) (*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupVaultByUUIDWithoutAccount(ctx context.Context, bvUUID string) (*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleBackupVaults(ctx context.Context, backupVaultUUIDList []string) ([]*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteBackupVault(ctx context.Context, params *commonparams.BackupVaultParams) (*models.BackupVaultV1beta, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackupVaultInternal(ctx context.Context, params *commonparams.BackupVaultParams, useExternalUUID bool) (*models.BackupVaultV1beta, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) IsBackupVaultAttachedToVolume(ctx context.Context, backupVaultUUID string) (bool, error) {
	return false, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountName string) ([]string, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackupVaultEntryInVCP(ctx context.Context, bv *datamodel.BackupVault, params *commonparams.BackupVaultParams) (*datamodel.BackupVault, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackupVaultEntryInVCPFromCVP(ctx context.Context, cvpBV *cvpmodels.BackupVaultV1beta, region, accountName string, tenantProject string) (*datamodel.BackupVault, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackupVault(ctx context.Context, params *commonparams.CreateBackupVaultParams) (*models.BackupVaultV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, ownerID string) (*datamodel.BackupVault, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetAccount(ctx context.Context, accountName string) (*datamodel.Account, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) PersistAccountTrialMetadataIfSet(ctx context.Context, accountName string, trial *commonparams.TrialModeParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateResourceState(ctx context.Context, params *commonparams.UpdateResourceStateParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackup(ctx context.Context, params *commonparams.CreateBackupParams) (*models.Backup, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateBackupInternal(ctx context.Context, params *commonparams.CreateBackupParams) (*models.Backup, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackup(ctx context.Context, params *commonparams.GetBackupParams) (*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteBackup(ctx context.Context, params *commonparams.DeleteBackupParams) (*models.BaseModel, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteBackupInternal(ctx context.Context, params *commonparams.DeleteBackupParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListBackups(ctx context.Context, backupVaultID, ownerID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListBackupsWithoutAccountFilter(ctx context.Context, backupVaultID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackup(ctx context.Context, params *commonparams.UpdateBackupParams) (*models.Backup, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackupInternal(ctx context.Context, params *commonparams.UpdateBackupParams) (*models.Backup, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetBackupsUnderBackupVault(ctx context.Context, backupVaultID, ownerID string, backupUUIDs []string) ([]*datamodel.Backup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, backupUUID string) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RotateCmekBackupsForBackupVault(ctx context.Context, params *commonparams.BackupVaultParams, primaryKeyVersion string) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateOrGetStartProjectEventJob(ctx context.Context, params *commonparams.StartProjectEventParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateOrGetFinishProjectEventJob(ctx context.Context, params *commonparams.FinishProjectEventParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateActiveDirectory(ctx context.Context, params *commonparams.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateActiveDirectory(ctx context.Context, params *commonparams.UpdateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetActiveDirectory(ctx context.Context, params *commonparams.GetADParams) (*models.ActiveDirectory, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListActiveDirectories(ctx context.Context, accountName string) ([]*models.ActiveDirectory, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetMultipleActiveDirectories(ctx context.Context, uuids []string) ([]*models.ActiveDirectory, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) BatchListActiveDirectories(ctx context.Context, params *commonparams.BatchListADsParams) ([]*models.ActiveDirectory, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetADConfig(ctx context.Context, params *commonparams.GetADParams) (*models.ActiveDirectory, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetSDEActiveDirectory(ctx context.Context, getADParams *commonparams.GetADParams) (*cvpmodels.ActiveDirectoryV1beta, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteActiveDirectory(ctx context.Context, params *commonparams.DeleteActiveDirectoryParams) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetClusterUpgradeStatus(ctx context.Context, jobUUID string) (*models.UpgradeProgress, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) HasActiveClusterUpgrade(ctx context.Context, clusterID string) (bool, error) {
	return false, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListAvailableVersions(ctx context.Context) (*models.ListAvailableVersionsResponse, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateImageVersion(ctx context.Context, ontapVersion, vsaImagePath, vsaName, mediatorName string, isActive bool) (*datamodel.ImageVersion, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteImageVersion(ctx context.Context, ontapVersion string) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) RenameExpertModeVolume(ctx context.Context, params *commonparams.ExpertModeVolumeRenameParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) StartExpertModeFlexCloneSplit(ctx context.Context, params *commonparams.ExpertModeFlexCloneSplitParams) error {
	return utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateRbacForPools(ctx context.Context) (string, error) {
	return "", utilserrors.NewNotImplementedYetErr()
}

// UpdateRbacForPoolById resolves the pool by OCID and dispatches the OCI RBAC refresh workflow.
func (o *OCIOrchestrator) UpdateRbacForPoolById(ctx context.Context, params *commonparams.RefreshRbacForPoolParams) (string, error) {
	logger := util.GetLogger(ctx)

	pool, err := getPoolByOCID(ctx, o.storage, params.PoolOCID, params.AccountName)
	if err != nil {
		return "", err
	}

	wfParams := &commonparams.RefreshRbacForPoolParams{
		PoolOCID:    params.PoolOCID,
		PoolID:      pool.UUID,
		AccountName: params.AccountName,
		RbacFileURL: params.RbacFileURL,
	}

	workflowID := utils.RandomUUID()
	workflowExecutor := workflows.NewWorkflowExecutor(o.temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIRefreshRbacForPoolWorkflow,
		nil,
		wfParams,
		pool,
	)
	if err != nil {
		logger.Error("RBAC refresh workflow dispatch failed", "workflowID", workflowID, "poolOCID", params.PoolOCID, "error", err)
		return "", fmt.Errorf("failed to dispatch RBAC refresh workflow for pool %s: %w", params.PoolOCID, err)
	}

	logger.Info("RBAC refresh workflow dispatched", "workflowID", workflowID, "poolOCID", params.PoolOCID, "poolUUID", pool.UUID)
	return workflowID, nil
}

func (o *OCIOrchestrator) GetBackupConfigsForPool(ctx context.Context, poolID string, accountName string, locationId string) ([]*models.ExpertModeVolumeBackupConfig, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ManageBackupConfigForExpertModeVolume(ctx context.Context, params *commonparams.ManageBackupConfigForExpertModeVolumeParams) (*datamodel.DataProtection, string, error) {
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateVolumePerformanceGroup(ctx context.Context, params *commonparams.CreateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListVolumePerformanceGroups(ctx context.Context, params *commonparams.ListVolumePerformanceGroupsParams) ([]*models.VolumePerformanceGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetVolumePerformanceGroup(ctx context.Context, params *commonparams.GetVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateVolumePerformanceGroup(ctx context.Context, params *commonparams.UpdateVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error) {
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteVolumePerformanceGroup(ctx context.Context, params *commonparams.DeleteVolumePerformanceGroupParams) (*models.VolumePerformanceGroup, string, error) {
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ReplaceDstQuotaRulesWithSrc(ctx context.Context, req *commonparams.UpdateDstWithSrcQuotaRulesV1beta, params commonparams.V1betaUpdateDestinationQuotaRulesVCPParams) ([]*datamodel.QuotaRule, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) SplitStartVolume(ctx context.Context, params *commonparams.SplitStartVolumeParams) (*models.Volume, string, error) {
	// TODO implement me
	return nil, "", utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) SplitStopVolume(ctx context.Context, params *commonparams.SplitStopVolumeParams) (*models.Volume, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) CreateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) ListAddressRanges(ctx context.Context, hostProjectNumber, vpcName string, arID, lifType *string) ([]*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateAddressRangeState(ctx context.Context, arID, state string, routeAggregationApplied *bool) (*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) OnboardExternalClusters(ctx context.Context, params *commonparams.OnboardExternalClustersParams) ([]*datamodel.Cluster, error) {
	_ = ctx
	_ = params
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) GetExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error) {
	_ = ctx
	_ = externalClusterID
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) UpdateExternalCluster(ctx context.Context, params *commonparams.UpdateExternalClusterParams) (*datamodel.Cluster, error) {
	_ = ctx
	_ = params
	return nil, utilserrors.NewNotImplementedYetErr()
}

func (o *OCIOrchestrator) DeleteExternalCluster(ctx context.Context, externalClusterID string) (*datamodel.Cluster, error) {
	_ = ctx
	_ = externalClusterID
	return nil, utilserrors.NewNotImplementedYetErr()
}

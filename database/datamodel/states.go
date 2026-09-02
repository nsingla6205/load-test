// Package datamodel hosts GORM-persisted models and the typed string enums
// that are *stored* on those models (DB columns, JSONB fields).
package datamodel

import "errors"

type HybridReplicationStatus string
type ClusterPeeringStatus string
type HybridReplicationParametersReplicationType string
type JobType string
type JobState string

// Lifecycle states for storage resources (pools, volumes, snapshots, etc.).
const (
	LifeCycleStateCreating        = "CREATING"
	LifeCycleStatePreparing       = "PREPARING"
	LifeCycleStateOngoing         = "ONGOING"
	LifeCycleStateReverting       = "REVERTING"
	LifeCycleStateUndeleting      = "UNDELETING"
	LifeCycleStateCompleted       = "COMPLETED"
	LifeCycleStateRestoring       = "RESTORING"
	LifeCycleStateSplitting       = "SPLITTING"
	LifeCycleStateAvailable       = "AVAILABLE"
	LifeCycleStateREADY           = "READY"
	LifeCycleStateInUse           = "IN_USE"
	LifeCycleStateDisabled        = "DISABLED"
	LifeCycleStateDisabling       = "DISABLING"
	LifeCycleStateEnabling        = "ENABLING"
	LifeCycleStateUpdating        = "UPDATING"
	LifeCycleStateDeleting        = "DELETING"
	LifeCycleStateDeleted         = "DELETED"
	LifeCycleStateError           = "ERROR"
	LifeCycleStateRetained        = "RETAINED"
	LifeCycleStateCreated         = "CREATED"
	LifeCycleStateKeyCheckPending = "KEY_CHECK_PENDING"
	LifeCycleStateMigrating       = "MIGRATING"
	LifeCycleStateDegraded        = "DEGRADED"
	LifeCycleStateUnknown         = "UNKNOWN"
	LifeCycleStateDiagnosing      = "DIAGNOSING"

	// "*Details" human-readable suffixes (used as StateDetails column values).
	LifeCycleStateCreatingDetails            = "Creation in progress"
	LifeCycleStateRevertingDetails           = "Revert in progress"
	LifeCycleStateUndeletingDetails          = "Undelete in progress"
	LifeCycleStateRestoringDetails           = "Restore in progress"
	LifeCycleStateAvailableDetails           = "Available for use"
	LifeCycleStateDisabledDetails            = "Disabled"
	LifeCycleStateUpdatingDetails            = "Update in progress"
	LifeCycleStateSyncDetails                = "Sync in progress"
	LifeCycleStateDeletingDetails            = "Deletion in progress"
	LifeCycleStateSplittingDetails           = "Splitting in progress"
	LifeCycleStateDeletedDetails             = "Deleted"
	LifeCycleStateCompletedDetails           = "Completed"
	LifeCycleStateRetainedDetails            = "Retained"
	LifeCycleStateOngoingDetails             = "Ongoing"
	LifeCycleStateCreationErrorDetails       = "Error in creating"
	LifeCycleStateUpdateErrorDetails         = "Error in updating"
	LifeCycleStateDeletionErrorDetails       = "Error in deleting"
	LifeCycleStateReadyDetails               = "Ready for use"
	LifeCycleStateCreatedDetails             = "Created successfully"
	LifeCycleStateUnknownDetails             = "Unknown state"
	LifeCycleStateInUseDetails               = "In use"
	LifeCycleStateMigratingDetails           = "Kms config is in migrating state"
	LifeCycleStateDegradedDetails            = "We're currently experiencing degraded performance for this resource, which may result in increased write latency. Some operations maybe restricted during this time."
	LifeCycleStateVolMigratingDetails        = "Volume encryption in progress"
	LifeCycleStateHyperscalerDisabledDetails = "Hyperscaler disabled"
	LifeCycleStateDiagnosingDetails          = "Validating Active Directory Configuration"
)

// allowedADRollbackStates is the single source of truth for the lifecycle states an Active Directory
// rollback/recovery path may write. Both the delete rollback activity and the supervisor handlers enforce
// this same contract so recovery behaviour cannot diverge across paths.
var allowedADRollbackStates = map[string]struct{}{
	LifeCycleStateREADY: {},
	LifeCycleStateInUse: {},
	LifeCycleStateError: {},
}

// IsAllowedADRollbackState reports whether state is a permitted Active Directory rollback/recovery target.
// Callers that receive a disallowed state should fall back to ERROR rather than persist it.
func IsAllowedADRollbackState(state string) bool {
	_, ok := allowedADRollbackStates[state]
	return ok
}

// Backup vault CMEK encryption states.
const (
	EncryptionStatePending    = "ENCRYPTION_STATE_PENDING"
	EncryptionStateCompleted  = "ENCRYPTION_STATE_COMPLETED"
	EncryptionStateInProgress = "ENCRYPTION_STATE_IN_PROGRESS"
	EncryptionStateFailed     = "ENCRYPTION_STATE_FAILED"
)

// Typed ClusterPeering constants (canonical, after PR-2c alias).
const (
	CvpClusterPeeringStatusCREATING              ClusterPeeringStatus = "CREATING"
	CvpClusterPeeringStatusPENDINGCLUSTERPEERING ClusterPeeringStatus = "PENDING_CLUSTER_PEERING"
	CvpClusterPeeringStatusPEERED                ClusterPeeringStatus = "PEERED"
	CvpClusterPeeringStatusDELETED               ClusterPeeringStatus = "DELETED"
	CvpClusterPeeringStatusERROR                 ClusterPeeringStatus = "ERROR"
)

const (
	HybridReplicationStatusPendingClusterPeer  HybridReplicationStatus = "PENDING_CLUSTER_PEER"
	HybridReplicationStatusPendingSVMPeer      HybridReplicationStatus = "PENDING_SVM_PEER"
	HybridReplicationStatusSVMPeered           HybridReplicationStatus = "SVM_PEERED"
	HybridReplicationStatusPeered              HybridReplicationStatus = "PEERED"
	HybridReplicationStatusPendingRemoteResync HybridReplicationStatus = "PENDING_REMOTE_RESYNC"
	HybridReplicationStatusExternalManaged     HybridReplicationStatus = "EXTERNALLY_MANAGED_REPLICATION"
)

// ONTAP-side enums referenced by the DAO layer.
const (
	OntapUninitialized         = "uninitialized"
	OntapSnapmirrored          = "snapmirrored"
	SnapmirrorRelationshipIdle = "idle"
	StateOn                    = "on"
	StateOff                   = "off"
)

const (
	JobsStateNEW             JobState = "NEW"
	JobsStatePROCESSING      JobState = "PROCESSING"
	JobsStateERROR           JobState = "ERROR"
	JobsStateDONE            JobState = "DONE"
	JobsStateWaitForTemporal JobState = "WAIT_FOR_TEMPORAL"
	JobsStateCANCELLED       JobState = "CANCELLED"
)

const (
	JobTypeCreatePool      JobType = "CREATE_POOL"
	JobTypeCreateLargePool JobType = "CREATE_LARGE_POOL"
	JobTypeUpdatePool      JobType = "UPDATE_POOL"
	JobTypeUpdateLargePool JobType = "UPDATE_LARGE_POOL"
	JobTypeDeletePool      JobType = "DELETE_POOL"
	JobTypeDeleteLargePool JobType = "DELETE_LARGE_POOL"
	JobTypeCreateSvm       JobType = "CREATE_SVM"
	JobTypeDeleteSvm       JobType = "DELETE_SVM"

	// We will use a single workflow for FC volume creation and it will handle creating/completing these jobs.
	// These 3 jobs are used to keep consistency with PO workflow/expectations.
	JobTypeFlexCacheCreateVolume     JobType = "FLEXCACHE_CREATE_VOLUME"
	JobTypeFlexCacheEstablishPeering JobType = "FLEXCACHE_ESTABLISH_PEERING"
	JobTypeFlexCacheInternalPeering  JobType = "FLEXCACHE_INTERNAL_PEERING"
	JobTypeFlexCacheDeleteVolume     JobType = "FLEXCACHE_DELETE_VOLUME"
	JobTypeFlexCachePrePopulate      JobType = "FLEXCACHE_PREPOPULATE"

	JobTypeCreateVolume                             JobType = "CREATE_VOLUME"
	JobTypeCreateLargeVolume                        JobType = "CREATE_LARGE_VOLUME"
	JobTypeUpdateVolume                             JobType = "UPDATE_VOLUME"
	JobTypeUpdateVolumeInReplication                JobType = "UPDATE_VOLUME_IN_REPLICATION"
	JobTypeUpdateVolumePerformanceGroup             JobType = "UPDATE_VOLUME_PERFORMANCE_GROUP"
	JobTypeDeleteVolumePerformanceGroup             JobType = "DELETE_VOLUME_PERFORMANCE_GROUP"
	JobTypeRevertVolume                             JobType = "REVERT_VOLUME"
	JobTypeDeleteVolume                             JobType = "DELETE_VOLUME"
	JobTypeDeleteLargeVolume                        JobType = "DELETE_LARGE_VOLUME"
	JobTypeCreateSnapshot                           JobType = "CREATE_SNAPSHOT"
	JobTypeUpdateSnapshot                           JobType = "UPDATE_SNAPSHOT"
	JobTypeDeleteSnapshot                           JobType = "DELETE_SNAPSHOT"
	JobTypeCreateQuotaRule                          JobType = "CREATE_QUOTA_RULE"
	JobTypeUpdateQuotaRule                          JobType = "UPDATE_QUOTA_RULE"
	JobTypeDeleteQuotaRule                          JobType = "DELETE_QUOTA_RULE"
	JobTypeRestoreBackup                            JobType = "RESTORE_BACKUP"
	JobTypeRestoreFilesBackup                       JobType = "RESTORE_FILES_BACKUP"
	JobTypeRestoreOntapModeBackup                   JobType = "RESTORE_ONTAP_MODE_BACKUP"
	JobTypeAcceptClusterPeer                        JobType = "ACCEPT_CLUSTER_PEER"
	JobTypeUpdateKmsConfig                          JobType = "UPDATE_KMS_CONFIG"
	JobTypeCreateKmsConfig                          JobType = "CREATE_KMS_CONFIG"
	JobTypeDeleteKmsConfig                          JobType = "DELETE_KMS_CONFIG"
	JobTypeSdeKmsCreate                             JobType = "SDE_KMS_CREATE"
	JobTypeMigrateKmsConfig                         JobType = "MIGRATE_KMS_CONFIG"
	JobTypeRotateKmsConfig                          JobType = "ROTATE_KMS_CONFIG"
	JobTypeEncryptPool                              JobType = "ENCRYPT_POOL"
	JobTypeCreateVolumeReplication                  JobType = "CREATE_VOLUME_REPLICATION"
	JobTypeCreateVolumeReplicationInternal          JobType = "CREATE_VOLUME_REPLICATION_INTERNAL"
	JobTypeDeleteVolumeReplicationInternal          JobType = "DELETE_VOLUME_REPLICATION_INTERNAL"
	JobTypeUpdateVolumeReplicationInternal          JobType = "UPDATE_VOLUME_REPLICATION_INTERNAL"
	JobTypeCreateBackupVault                        JobType = "CREATE_BACKUP_VAULT"
	JobTypeDeleteVolumeReplication                  JobType = "DELETE_VOLUME_REPLICATION"
	JobTypeUpdateVolumeReplication                  JobType = "UPDATE_VOLUME_REPLICATION"
	JobTypeResumeVolumeReplication                  JobType = "RESUME_VOLUME_REPLICATION"
	JobTypeResumeVolumeReplicationInternal          JobType = "RESUME_VOLUME_REPLICATION_INTERNAL"
	JobTypeReverseVolumeReplicationInternal         JobType = "REVERSE_VOLUME_REPLICATION_INTERNAL"
	JobTypeSyncVolumeReplication                    JobType = "SYNC_VOLUME_REPLICATION"
	JobTypeReverseResumeVolumeReplication           JobType = "REVERSE_RESUME_VOLUME_REPLICATION"
	JobTypeUpdateVolumeReplicationAttributes        JobType = "UPDATE_VOLUME_REPLICATION_ATTRIBUTES"
	JobTypeStopVolumeReplication                    JobType = "STOP_VOLUME_REPLICATION"
	JobTypeStopVolumeReplicationInternal            JobType = "STOP_VOLUME_REPLICATION_INTERNAL"
	JobTypeRefreshVolumeReplicationInternal         JobType = "REFRESH_VOLUME_REPLICATION_INTERNAL"
	JobTypeCreateBackup                             JobType = "CREATE_BACKUP"
	JobTypeDeleteBackup                             JobType = "DELETE_BACKUP"
	JobTypeCreateHostGroup                          JobType = "CREATE_HOSTGROUP"
	JobTypeUpdateHostGroup                          JobType = "UPDATE_HOSTGROUP"
	JobTypeDeleteHostGroup                          JobType = "DELETE_HOSTGROUP"
	JobTypeMountCheck                               JobType = "MOUNT_VOLUME_REPLICATION_INTERNAL"
	JobTypeRefreshAdminJobSpecs                     JobType = "REFRESH_ADMIN_JOB_SPECS"
	JobTypeStartProjectEventOffState                JobType = "START_PROJECT_EVENT_OFF_STATE"
	JobTypeStartProjectEventOnState                 JobType = "START_PROJECT_EVENT_ON_STATE"
	JobTypeFinishProjectEventDeleteState            JobType = "FINISH_PROJECT_EVENT_DELETE_STATE"
	JobTypeReleaseVolumeReplicationInternal         JobType = "RELEASE_VOLUME_REPLICATION_INTERNAL"
	JobTypeUpdateBackupVault                        JobType = "UPDATE_BACKUP_VAULT"
	JobTypeDeleteSnapmirrorSnapshotsInternal        JobType = "DELETE_SM_SNAPSHOTS_INTERNAL"
	JobTypeCreateSubnet                             JobType = "CREATE_SUBNET"
	JobTypeDeleteSubnet                             JobType = "DELETE_SUBNET"
	JobTypeCreateLargeSubnet                        JobType = "CREATE_LARGE_SUBNET"
	JobTypeHandleResourceEvent                      JobType = "HANDLE_RESOURCE_EVENT"
	JobTypeHandleResourceEventOffState              JobType = "HANDLE_RESOURCE_EVENT_OFF_STATE"
	JobTypeHandleResourceEventOnState               JobType = "HANDLE_RESOURCE_EVENT_ON_STATE"
	JobTypeHandleResourceEventDeleteState           JobType = "HANDLE_RESOURCE_EVENT_DELETE_STATE"
	JobTypeDeleteBackupVault                        JobType = "DELETE_BACKUP_VAULT"
	JobTypeInitCreateScheduledBackup                JobType = "INIT_CREATE_SCHEDULED_BACKUP"
	JobTypeCreateScheduledBackup                    JobType = "CREATE_SCHEDULED_BACKUP"
	JobTypeDeleteScheduledBackup                    JobType = "DELETE_SCHEDULED_BACKUP"
	JobTypeRefreshVolumeFields                      JobType = "REFRESH_VOLUME_FIELDS"
	JobTypeUpdateBackup                             JobType = "UPDATE_BACKUP"
	JobTypeCreateBackupPolicy                       JobType = "CREATE_BACKUP_POLICY"
	JobTypeUpdateBackupPolicy                       JobType = "UPDATE_BACKUP_POLICY"
	JobTypeDeleteBackupPolicy                       JobType = "DELETE_BACKUP_POLICY"
	JobTypeCreateActiveDirectory                    JobType = "CREATE_ACTIVE_DIRECTORY"
	JobTypeUpdateActiveDirectory                    JobType = "UPDATE_ACTIVE_DIRECTORY"
	JobTypeDeleteActiveDirectory                    JobType = "DELETE_ACTIVE_DIRECTORY"
	JobTypeDiagnoseActiveDirectory                  JobType = "DIAGNOSE_ACTIVE_DIRECTORY"
	JobTypeSplitVolume                              JobType = "SPLIT_CLONE_VOLUME"
	JobTypeCreateHybridReplication                  JobType = "CREATE_HYBRID_REPLICATION"
	JobTypeHybridReplicationDeleteVolume            JobType = "HYBRID_REPLICATION_DELETE_VOLUME"
	JobTypeHybridReplicationEstablishPeering        JobType = "HYBRID_REPLICATION_ESTABLISH_PEERING"
	JobTypeHybridReplicationInternalEstablish       JobType = "HYBRID_REPLICATION_INTERNAL_ESTABLISH"
	JobTypeReverseHybridReplicationInternal         JobType = "HYBRID_REPLICATION_INTERNAL_REVERSE"
	JobTypeReverseHybridReplicationFallbackInternal JobType = "HYBRID_REPLICATION_INTERNAL_REVERSE_FALLBACK"
	JobTypeCreateExpertModeVolume                   JobType = "RECONCILE_EXPERT_MODE_VOLUME_CREATE"
	JobTypeUpdateExpertModeVolume                   JobType = "RECONCILE_EXPERT_MODE_VOLUME_UPDATE"
	JobTypeDeleteExpertModeVolume                   JobType = "RECONCILE_EXPERT_MODE_VOLUME_DELETE"
	JobTypeExpertModeRbacRefresh                    JobType = "EXPERT_MODE_RBAC_REFRESH"
	JobTypeExpertModeFlexCloneSplit                 JobType = "RECONCILE_EXPERT_MODE_VOLUME_FLEXCLONE_SPLIT"
	JobTypeRotateCmekBackups                        JobType = "ROTATE_CMEK_BACKUPS"
	JobTypeManageBackupConfigExpertModeVolume       JobType = "MANAGE_BACKUP_CONFIG_EXPERT_MODE_VOLUME"
	JobTypePinAccountHosts                          JobType = "PIN_ACCOUNT_HOSTS"
	JobTypeCreateHost                               JobType = "CREATE_HOST"
)

// Account states (lifecycle for the Account entity in the multi-tenant model).
const (
	AccountStateDisabled            = "DISABLED"
	AccountStateEnabled             = "ENABLED"
	AccountStateDeleted             = "DELETED"
	AccountStateEnabling            = "ENABLING"
	AccountStateDisabling           = "DISABLING"
	AccountStateHyperscalerDisabled = "HYPERSCALERDISABLED"
)

// ServiceType for backup endpoint classification.
const (
	ServiceTypeGCNV         = "GCNV"
	ServiceTypeCrossProject = "CrossProject"
)

// Backup vault type values (stored on BackupVault.BackupVaultType column).
const (
	BackupVaultTypeCrossRegion = "CROSS_REGION"
)

// Volume lifecycle extras.
const (
	VolumeStateOffline = "OFFLINE"
)

// Clone split state values (stored on Volume clone state column).
const (
	CloneStateCloned           = "SPLIT_STATE_NOT_SPLITTING"
	CloneStateSplitting        = "SPLIT_STATE_IN_PROGRESS"
	CloneStateErrorInSplitting = "SPLIT_STATE_FAILED"
)

// Zone switching state values.
const (
	ZoneSwitching = "SWITCHING"
	ZoneSwitched  = "SWITCHED"
	ZonePrimary   = "PRIMARY"
)

// Cluster peering / SVM peering state_details
const (
	InitiatingClusterPeering        = "Initiating cluster peering on destination cluster"
	InitiatingSVMPeering            = "Initiating SVM peering on destination cluster"
	WaitingForClusterPeering        = "Waiting for cluster peering to be created on source cluster"
	ErrorDuringClusterPeer          = "Cluster peering failed, please try again"
	ClusterPeeringExpired           = "Cluster peering expired"
	WaitingForSVMPeering            = "Waiting for SVM peering to be accepted on source cluster"
	ErrorDuringSVMPeering           = "SVM peering failed, please try again"
	SVMPeeringExpired               = "SVM peering expired"
	ErrorUnencryptedVolume          = "Origin volume is not encrypted"
	ErrorCreatingCacheVolume        = "Error creating cache volume"
	ClusterPeeringSourceUnreachable = "Source cluster unreachable, check network connections"

	VolumeReplicationBreakRelationshipQuotaRuleFailure = "Break operation is successful and destination volume has become RW, but post break quota rule creation operation failed"
)

// Snapmirror relationship statuses (lowercase, from ONTAP REST, persisted on
// VolumeReplication.RelationshipStatus column).
const (
	SnapmirrorRelationshipSuccess      = "success"
	SnapmirrorRelationshipFinalizing   = "finalizing"
	SnapmirrorRelationshipTransferring = "transferring"
	SnapmirrorRelationshipFailed       = "failed"
	SnapmirrorRelationshipAborted      = "aborted"
	SnapmirrorRelationshipQueued       = "queued"
	SnapmirrorRelationshipHardAborted  = "hard_aborted"

	OntapBrokenOff = "broken_off" // mirror state value
)

// Replication endpoint type values
const (
	DstEndpoint = "dst"
	SrcEndpoint = "src"
)

// Hybrid replication parameters replication type values
const (
	HybridReplicationParametersReplicationTypeMIGRATION   HybridReplicationParametersReplicationType = "MIGRATION"
	HybridReplicationParametersReplicationTypeUNSPECIFIED HybridReplicationParametersReplicationType = "REPLICATION_TYPE_UNSPECIFIED"
	HybridReplicationParametersReplicationTypeCONTINUOUS  HybridReplicationParametersReplicationType = "CONTINUOUS_REPLICATION"
	HybridReplicationParametersReplicationTypeONPREM      HybridReplicationParametersReplicationType = "ONPREM_REPLICATION"
	HybridReplicationParametersReplicationTypeREVERSE     HybridReplicationParametersReplicationType = "REVERSE_ONPREM_REPLICATION"
)

// ResourceEvent state values from incoming project/resource events
// (written to e.g. KmsConfig.State via UpdateKmsConfigStateForHandleResource).
// Renamed from former core/models.StateOff/On/Delete to avoid colliding with
// the lowercase ONTAP StateOn/StateOff above.
const (
	ResourceEventStateOff    = "OFF"
	ResourceEventStateOn     = "ON"
	ResourceEventStateDelete = "DELETE"
)

// VCM durable job constants used by database/vcm, vcm-proxy, and core VCM workflows.
const (
	OperationNotFound = "Operation not found"
)

// ErrInvalidConfig indicates that VCM runtime configuration cannot serve the operation.
var ErrInvalidConfig = errors.New("invalid VCM configuration")

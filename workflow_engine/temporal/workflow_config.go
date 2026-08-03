package temporal

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
)

var (
	CMEKWorkflowGlobalTimeoutMinutes               = env.GetString("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "14")
	WorkflowGlobalTimeoutMinutes                   = env.GetString("WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "60")
	ExpertModeSyncWorkflowTimeoutMinutes           = env.GetString("EXPERT_MODE_SYNC_WORKFLOW_TIMEOUT_MINUTES", "30")
	ExpertModeFlexCloneSplitWorkflowTimeoutMinutes = env.GetString("EXPERT_MODE_FLEXCLONE_SPLIT_WORKFLOW_TIMEOUT_MINUTES", "4320")
	CreatePoolWorkflowTimeoutMinutes               = env.GetString("CREATE_POOL_WORKFLOW_TIMEOUT_MINUTES", "150")
	CreatePoolWorkflowTimeoutMinutesLV             = env.GetString("CREATE_POOL_WORKFLOW_TIMEOUT_MINUTES_LV", "150")
	UpdatePoolWorkflowTimeoutMinutes               = env.GetString("UPDATE_POOL_WORKFLOW_TIMEOUT_MINUTES", "150")
	UpdatePoolWorkflowTimeoutMinutesLV             = env.GetString("UPDATE_POOL_WORKFLOW_TIMEOUT_MINUTES_LV", "150")
	PoolMaintenanceWorkflowTimeoutMinutes          = env.GetString("POOL_MAINTENANCE_WORKFLOW_TIMEOUT_MINUTES", "240")
	DeletePoolWorkflowTimeoutMinutes               = env.GetString("DELETE_POOL_WORKFLOW_TIMEOUT_MINUTES", "60")
	RotateFabricPoolKeysWorkflowTimeoutMinutes     = env.GetString("ROTATE_FABRIC_POOL_KEYS_WORKFLOW_TIMEOUT_MINUTES", "10")
	ResetOciAdminPasswordWorkflowTimeoutMinutes    = env.GetString("RESET_OCI_ADMIN_PASSWORD_WORKFLOW_TIMEOUT_MINUTES", "10")
	CreateSVMWorkflowTimeoutMinutes                = env.GetString("CREATE_SVM_WORKFLOW_TIMEOUT_MINUTES", "60")
	DeleteSVMWorkflowTimeoutMinutes                = env.GetString("DELETE_SVM_WORKFLOW_TIMEOUT_MINUTES", "60")
	UpdateSVMWorkflowTimeoutMinutes                = env.GetString("UPDATE_SVM_WORKFLOW_TIMEOUT_MINUTES", "30")
	ClusterUpgradeWorkflowTimeoutMinutes           = env.GetString("CLUSTER_UPGRADE_WORKFLOW_TIMEOUT_MINUTES", "600")
	ClusterUpgradeWorkflowTimeoutMinutesLV         = env.GetString("CLUSTER_UPGRADE_WORKFLOW_TIMEOUT_MINUTES_LV", "720")
	RefreshRbacWorkflowTimeoutMinutes              = env.GetString("REFRESH_RBAC_WORKFLOW_TIMEOUT_MINUTES", "15")
	CreateBackupWorkflowTimeoutMinutes             = env.GetString("CREATE_BACKUP_WORKFLOW_TIMEOUT_MINUTES", "8640")
	DeleteBackupWorkflowTimeoutMinutes             = env.GetString("DELETE_BACKUP_WORKFLOW_TIMEOUT_MINUTES", "6480")
	SFRWorkflowTimeoutMinutes                      = env.GetString("SFR_WORKFLOW_TIMEOUT_MINUTES", "13680")
	CreateSnapshotWorkflowTimeoutMinutes           = env.GetString("CREATE_SNAPSHOT_WORKFLOW_TIMEOUT_MINUTES", "50")
	DeleteSnapshotWorkflowTimeoutMinutes           = env.GetString("DELETE_SNAPSHOT_WORKFLOW_TIMEOUT_MINUTES", "65")
	RevertVolumeWorkflowTimeoutMinutes             = env.GetString("REVERT_VOLUME_WORKFLOW_TIMEOUT_MINUTES", "95")
	VolumeRefreshWorkflowTimeoutMinutes            = env.GetString("VOLUME_REFRESH_WORKFLOW_TIMEOUT_MINUTES", "20")
	SplitVolumeWorkflowTimeoutMinutes              = env.GetString("SPLIT_VOLUME_WORKFLOW_TIMEOUT_MINUTES", "120")
	SplitVolumeRunContinueAsNewMinutes             = env.GetString("SPLIT_VOLUME_RUN_CONTINUE_AS_NEW_MINUTES", "60")
	UpdateBackupScheduleWorkflowTimeoutMinutes     = env.GetString("UPDATE_BACKUP_SCHEDULE_WORKFLOW_TIMEOUT_MINUTES", "180")
	// ReverseHybridReplicationPollWorkflowTimeoutMinutes must exceed the poll window; see VSCP-6543.
	ReverseHybridReplicationPollWorkflowTimeoutMinutes = env.GetString("REVERSE_HYBRID_REPLICATION_POLL_WORKFLOW_TIMEOUT_MINUTES", "150")
)

// Struct for RetryPolicy configuration
type RetryPolicyConfig struct {
	InitialInterval    time.Duration
	BackoffCoefficient float64
	MaximumInterval    time.Duration
	MaximumAttempts    int32
	NonRetryableErrors []string
}

// Struct for StartWorkflowOptions configuration
type StartWorkflowOptionsConfig struct {
	TaskQueue             string
	WorkflowID            string
	WorkflowIDReusePolicy enums.WorkflowIdReusePolicy
	RetryPolicy           *RetryPolicyConfig
}

func GetRetryPolicy(config *RetryPolicyConfig) *temporal.RetryPolicy {
	// Define a retry policy with exponential backoff
	// Initial interval of 1 second, doubling each time, up to a maximum of 100 seconds
	retryConfig := RetryPolicyConfig{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2.0,
		MaximumInterval:    time.Second * 100,
		MaximumAttempts:    0, // Unlimited
		NonRetryableErrors: []string{},
	}

	// Override defaults with provided config values if set
	if config.InitialInterval != 0 {
		retryConfig.InitialInterval = config.InitialInterval
	}
	if config.BackoffCoefficient != 0 {
		retryConfig.BackoffCoefficient = config.BackoffCoefficient
	}
	if config.MaximumInterval != 0 {
		retryConfig.MaximumInterval = config.MaximumInterval
	}
	if config.MaximumAttempts != 0 {
		retryConfig.MaximumAttempts = config.MaximumAttempts
	}
	if config.NonRetryableErrors != nil {
		retryConfig.NonRetryableErrors = config.NonRetryableErrors
	}

	// Return the temporal.RetryPolicy
	return &temporal.RetryPolicy{
		InitialInterval:        retryConfig.InitialInterval,
		BackoffCoefficient:     retryConfig.BackoffCoefficient,
		MaximumInterval:        retryConfig.MaximumInterval,
		MaximumAttempts:        retryConfig.MaximumAttempts,
		NonRetryableErrorTypes: retryConfig.NonRetryableErrors,
	}
}

func GetWorkflowGlobalTimeout() time.Duration {
	timeout, err := time.ParseDuration(WorkflowGlobalTimeoutMinutes + "m")
	if err != nil {
		// If parsing fails, default to 60 minutes
		return 60 * time.Minute
	}
	return timeout
}

func GetCMEKWorkFlowGlobalTimeout() time.Duration {
	timeout, err := time.ParseDuration(CMEKWorkflowGlobalTimeoutMinutes + "m")
	if err != nil {
		// If parsing fails, default to 14 minutes
		return 14 * time.Minute
	}
	return timeout
}

func GetExpertModeSyncWorkflowTimeout() time.Duration {
	timeout, err := time.ParseDuration(ExpertModeSyncWorkflowTimeoutMinutes + "m")
	if err != nil {
		return 10 * time.Minute
	}
	return timeout
}

// GetExpertModeFlexCloneSplitWorkflowTimeout is the workflow run timeout for FlexClone split (may run many hours).
func GetExpertModeFlexCloneSplitWorkflowTimeout() time.Duration {
	timeout, err := time.ParseDuration(ExpertModeFlexCloneSplitWorkflowTimeoutMinutes + "m")
	if err != nil {
		return 72 * time.Hour
	}
	return timeout
}

// GetExpertModeFlexCloneSplitPollInterval is the delay between Temporal activity retries while polling ONTAP for split completion.
func GetExpertModeFlexCloneSplitPollInterval() time.Duration {
	sec := env.GetUint64("EXPERT_MODE_FLEXCLONE_SPLIT_POLL_INTERVAL_SEC", 30)
	if sec < 5 {
		return 5 * time.Second
	}
	return time.Duration(sec) * time.Second
}

func getWorkflowTimeoutWithDefault(configValue string, defaultMinutes int) *time.Duration {
	timeout, err := time.ParseDuration(configValue + "m")
	if err != nil {
		defaultTimeout := time.Duration(defaultMinutes) * time.Minute
		return &defaultTimeout
	}
	return &timeout
}

func GetCreatePoolWorkflowTimeout(largeCapacity bool) *time.Duration {
	if largeCapacity {
		return getWorkflowTimeoutWithDefault(CreatePoolWorkflowTimeoutMinutesLV, 150)
	}
	return getWorkflowTimeoutWithDefault(CreatePoolWorkflowTimeoutMinutes, 150)
}

// GetCreatePoolWorkflowRunTimeout returns the workflow run timeout used when starting CreatePoolWorkflow.
// - Large capacity (LV) pools use the create-pool LV timeout
// - Standard pools use the standard create-pool timeout
func GetCreatePoolWorkflowRunTimeout(largeCapacity bool) *time.Duration {
	timeout := GetCreatePoolWorkflowTimeout(largeCapacity)
	return timeout
}

func GetRotateFabricPoolKeysWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(RotateFabricPoolKeysWorkflowTimeoutMinutes, 10)
}

func GetResetOciAdminPasswordWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(ResetOciAdminPasswordWorkflowTimeoutMinutes, 10)
}

func GetUpdatePoolWorkflowTimeout(largeCapacity bool) *time.Duration {
	if largeCapacity {
		return getWorkflowTimeoutWithDefault(UpdatePoolWorkflowTimeoutMinutesLV, 150)
	}
	return getWorkflowTimeoutWithDefault(UpdatePoolWorkflowTimeoutMinutes, 150)
}

// GetUpdatePoolWorkflowRunTimeout returns the workflow run timeout used when starting UpdatePoolWorkflow.
// - Large capacity (LV) pools use the update-pool LV timeout
// - Standard pools use the standard update-pool timeout
func GetUpdatePoolWorkflowRunTimeout(largeCapacity bool) *time.Duration {
	timeout := GetUpdatePoolWorkflowTimeout(largeCapacity)
	return timeout
}

func GetPoolMaintenanceWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(PoolMaintenanceWorkflowTimeoutMinutes, 150)
}

func GetCreateBackupWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(CreateBackupWorkflowTimeoutMinutes, 8640)
}

func GetDeleteBackupWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(DeleteBackupWorkflowTimeoutMinutes, 6480)
}

func GetSFRWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(SFRWorkflowTimeoutMinutes, 13680)
}

func GetCreateSnapshotWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(CreateSnapshotWorkflowTimeoutMinutes, 50)
}

func GetDeleteSnapshotWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(DeleteSnapshotWorkflowTimeoutMinutes, 65)
}

func GetRevertVolumeWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(RevertVolumeWorkflowTimeoutMinutes, 95)
}

func GetVolumeRefreshWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(VolumeRefreshWorkflowTimeoutMinutes, 20)
}

func GetSplitVolumeWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(SplitVolumeWorkflowTimeoutMinutes, 120)
}

func GetUpdateBackupScheduleWorkflowTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(UpdateBackupScheduleWorkflowTimeoutMinutes, 180)
}

// GetSplitVolumeRunContinueAsNewDuration returns how long a single split workflow run
// may poll before triggering ContinueAsNew. This must be less than
// SplitVolumeWorkflowTimeoutMinutes so the run always restarts before Temporal kills it.
func GetSplitVolumeRunContinueAsNewDuration() time.Duration {
	d, err := time.ParseDuration(SplitVolumeRunContinueAsNewMinutes + "m")
	if err != nil {
		return 60 * time.Minute
	}
	return d
}

// GetReverseHybridReplicationPollWorkflowTimeout returns the WorkflowRunTimeout for the
// ReverseHybridReplicationPollWorkflow. Must exceed the poll window; see VSCP-6543.
func GetReverseHybridReplicationPollWorkflowTimeout() time.Duration {
	timeout, err := time.ParseDuration(ReverseHybridReplicationPollWorkflowTimeoutMinutes + "m")
	if err != nil {
		return 150 * time.Minute
	}
	return timeout
}

func GetDeletePoolWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(DeletePoolWorkflowTimeoutMinutes, 60)
}

func GetCreateSVMWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(CreateSVMWorkflowTimeoutMinutes, 60)
}

func GetDeleteSVMWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(DeleteSVMWorkflowTimeoutMinutes, 60)
}

func GetUpdateSVMWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(UpdateSVMWorkflowTimeoutMinutes, 30)
}

// GetClusterUpgradeWorkflowRunTimeout returns the workflow run timeout for OCI cluster upgrades.
// Large capacity pools get a longer timeout to accommodate batched HA-pair upgrades.
func GetClusterUpgradeWorkflowRunTimeout(largeCapacity bool) *time.Duration {
	if largeCapacity {
		return getWorkflowTimeoutWithDefault(ClusterUpgradeWorkflowTimeoutMinutesLV, 720)
	}
	return getWorkflowTimeoutWithDefault(ClusterUpgradeWorkflowTimeoutMinutes, 600)
}

func GetRefreshRbacWorkflowRunTimeout() *time.Duration {
	return getWorkflowTimeoutWithDefault(RefreshRbacWorkflowTimeoutMinutes, 15)
}

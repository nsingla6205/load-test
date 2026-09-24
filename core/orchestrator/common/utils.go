package common

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/workflow"
)

const (
	LocalEnv                = "local"
	DaysInWeekForRetention  = 7
	DaysInMonthForRetention = 30
	ScheduleTagDaily        = "daily"
	ScheduleTagWeekly       = "weekly"
	ScheduleTagMonthly      = "monthly"
	ONTAPMode               = "ONTAP"
	DEFAULTMode             = "DEFAULT"
	BackupTypeMANUAL        = "MANUAL"
	BackupTypeSCHEDULED     = "SCHEDULED"

	// PO (Performance Offering) pool identity values.
	StorageClassHardware = "HARDWARE"
	StorageClassSoftware = "SOFTWARE"
	ServiceLevelExtreme  = "EXTREME"
	ServiceLevelPremium  = "PREMIUM"
)

var (
	SnapmirrorSnapshotPrefix             = regexp.MustCompile("^snapmirror.*$")
	ImmutablePeriodInDaysMax             = env.GetInt("IMMUTABLE_PERIOD_IN_DAYS_MAX", 5475)
	ImmutablePeriodInDaysMaxDailyEnabled = env.GetInt("IMMUTABLE_PERIOD_IN_DAYS_MAX_DAILY_ENABLED", 1000)
	regionsGroupJSON                     = env.GetString("VCP_PAIRED_REGIONS", "")
	SleepFn                              = time.Sleep
	MaxRetries                           = 3
	RetryDelay                           = 5 * time.Second
	GetRemoteRegionConfig                = _getRemoteRegionConfig
)

func CreateJunctionPath(token string) string {
	junctionPath := "/" + token
	return junctionPath
}

// ShareNameFromJunctionPath returns the customer-facing share name from an ONTAP junction path.
func ShareNameFromJunctionPath(junctionPath string) string {
	junctionPath = strings.TrimSpace(junctionPath)
	if junctionPath == "" {
		return ""
	}
	return strings.TrimPrefix(junctionPath, "/")
}

// ConvertStringSliceToPointerSlice converts []string to []*string
func ConvertStringSliceToPointerSlice(slice []string) []*string {
	if slice == nil {
		return nil
	}
	result := make([]*string, len(slice))
	for i, s := range slice {
		str := s
		result[i] = &str
	}
	return result
}

// IsPoolStateEligibleForClusterUpgrade reports whether a pool can start a cluster upgrade.
func IsPoolStateEligibleForClusterUpgrade(state string) bool {
	return state == datamodel.LifeCycleStateREADY || state == datamodel.LifeCycleStateDisabled
}

func ValidateBackupPolicyRetentionLimits(backupPolicyParams *BackupPolicyParams, retentionPolicyParams *BackupRetentionPolicyParams) error {
	// Check if any backup type is immutable
	isDailyImmutable := retentionPolicyParams.IsDailyBackupImmutable != nil && *retentionPolicyParams.IsDailyBackupImmutable
	isWeeklyImmutable := retentionPolicyParams.IsWeeklyBackupImmutable != nil && *retentionPolicyParams.IsWeeklyBackupImmutable
	isMonthlyImmutable := retentionPolicyParams.IsMonthlyBackupImmutable != nil && *retentionPolicyParams.IsMonthlyBackupImmutable

	if isDailyImmutable || isWeeklyImmutable || isMonthlyImmutable {
		// Get the minimum retention period in days
		var immutablePeriodDays int64 = 0
		if retentionPolicyParams.BackupMinimumEnforcedRetentionDuration != nil {
			immutablePeriodDays = *retentionPolicyParams.BackupMinimumEnforcedRetentionDuration
		}

		// Validate daily backup retention
		if isDailyImmutable && backupPolicyParams.DailyBackupsToKeep >= 2 {
			if backupPolicyParams.DailyBackupsToKeep > int64(ImmutablePeriodInDaysMaxDailyEnabled) {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("daily backup limit (%d) exceeds maximum allowed (%d) for immutable backup policy", backupPolicyParams.DailyBackupsToKeep, ImmutablePeriodInDaysMaxDailyEnabled))
			}
			if backupPolicyParams.DailyBackupsToKeep < immutablePeriodDays {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("daily backup retention (%d days) is less than backup vault immutable period (%d days)", backupPolicyParams.DailyBackupsToKeep, immutablePeriodDays))
			}
		}

		// Validate weekly backup retention (weeks to days conversion)
		if isWeeklyImmutable && backupPolicyParams.WeeklyBackupsToKeep > 0 {
			weeklyRetentionDays := backupPolicyParams.WeeklyBackupsToKeep * DaysInWeekForRetention
			if weeklyRetentionDays < immutablePeriodDays {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("weekly backup retention (%d days) is less than backup vault immutable period (%d days)", weeklyRetentionDays, immutablePeriodDays))
			}
		}

		// Validate monthly backup retention (months to days conversion, using 30 days per month)
		if isMonthlyImmutable && backupPolicyParams.MonthlyBackupsToKeep > 0 {
			monthlyRetentionDays := backupPolicyParams.MonthlyBackupsToKeep * DaysInMonthForRetention
			if monthlyRetentionDays < immutablePeriodDays {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("monthly backup retention (%d days) is less than backup vault immutable period (%d days)", monthlyRetentionDays, immutablePeriodDays))
			}
		}
	}

	return nil
}

func CheckIfBackupIsImmutable(backup *datamodel.Backup) bool {
	// Check if backup vault or immutable attributes are nil
	if backup.BackupVault == nil || backup.BackupVault.ImmutableAttributes == nil {
		return false
	}

	switch backup.Type {
	case BackupTypeSCHEDULED:
		// Check if schedule tag is nil
		if backup.ScheduleTag == nil {
			return true // Default to immutable when schedule tag is nil
		}

		if *backup.ScheduleTag == ScheduleTagDaily && !backup.BackupVault.ImmutableAttributes.IsDailyBackupImmutable {
			break
		} else if *backup.ScheduleTag == ScheduleTagWeekly && !backup.BackupVault.ImmutableAttributes.IsWeeklyBackupImmutable {
			break
		} else if *backup.ScheduleTag == ScheduleTagMonthly && !backup.BackupVault.ImmutableAttributes.IsMonthlyBackupImmutable {
			break
		}
		return true
	case BackupTypeMANUAL:
		if backup.BackupVault.ImmutableAttributes.IsAdhocBackupImmutable {
			return true
		}
	}
	return false
}

// _getRemoteRegionConfig gets the base path and JWT token for a remote region
func _getRemoteRegionConfig(region, projectNumber string) (string, string, error) {
	if regionsGroupJSON == "" {
		return "", "", fmt.Errorf("VCP_PAIRED_REGIONS environment variable not set")
	}

	var regionsGroup map[string]string
	if err := json.Unmarshal([]byte(regionsGroupJSON), &regionsGroup); err != nil {
		return "", "", fmt.Errorf("failed to parse VCP_PAIRED_REGIONS JSON: %w", err)
	}

	basePath, exists := regionsGroup[region]
	if !exists {
		return "", "", fmt.Errorf("no base path configured for region: %s in VCP_PAIRED_REGIONS", region)
	}

	jwtToken, err := auth.GetSignedJwtToken(projectNumber)
	if err != nil {
		return "", "", fmt.Errorf("failed to get JWT token for project %s: %w", projectNumber, err)
	}

	return basePath, jwtToken, nil
}

// SetRegionsGroupJSONForTest sets the regionsGroupJSON variable for testing purposes
func SetRegionsGroupJSONForTest(value string) string {
	original := regionsGroupJSON
	regionsGroupJSON = value
	return original
}

// GetRegionsGroupJSONForTest returns the current value of regionsGroupJSON for testing purposes
func GetRegionsGroupJSONForTest() string {
	return regionsGroupJSON
}

// HandleCancellationAndRollback is a utility function that handles the common pattern of checking
// for cancellation and executing rollback in create workflows. It checks if there's an error or
// cancellation, and if so, executes rollback with appropriate error handling.
//
// Parameters:
//   - ctx: The workflow context
//   - err: The error from the workflow (can be nil)
//   - cancellationHandler: The cancellation handler to check for cancellation
//   - rollbackManager: The rollback manager to execute rollback
//   - resourceType: The type of resource being created (e.g., "volume", "pool", "active directory")
//   - resourceUUID: The UUID of the resource being created
//   - onErrorCallback: Optional callback function to execute when there's an error but not cancellation.
//     If nil, rollback will be executed with the error. The callback receives the disconnected context
//     and the error, and should return true if rollback should be executed, false otherwise.
//   - onCancellationCallback: Optional callback function to execute when cancellation is detected,
//     before executing rollback. This allows adding activities or performing cleanup before rollback.
//     The callback receives the disconnected context and the cancellation error message.
//
// Returns:
//   - bool: true if rollback was executed (either due to cancellation or error), false otherwise
func HandleCancellationAndRollback(
	ctx workflow.Context,
	err error,
	cancellationHandler *WorkflowCancellationHandler,
	rollbackManager *RollbackManager,
	resourceType string,
	resourceUUID string,
	onErrorCallback func(disconnectedCtx workflow.Context, err error) bool,
	onCancellationCallback func(disconnectedCtx workflow.Context, cancelErr error),
) bool {
	if err == nil && !cancellationHandler.IsCancelled() {
		return false
	}

	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	logger := util.GetLogger(ctx)

	if cancellationHandler.IsCancelled() {
		logger.Infof("%s creation cancelled, executing rollback for %s: %s", resourceType, resourceType, resourceUUID)
		cancelErr := vsaerrors.New(fmt.Sprintf("%s creation cancelled by delete request", resourceType))
		if onCancellationCallback != nil {
			onCancellationCallback(disconnectedCtx, cancelErr)
		}
		rollbackManager.ExecuteRollback(disconnectedCtx, cancelErr)
		return true
	}

	// Handle error case
	if err != nil {
		if onErrorCallback != nil {
			shouldRollback := onErrorCallback(disconnectedCtx, err)
			if shouldRollback {
				rollbackManager.ExecuteRollback(disconnectedCtx, err)
				return true
			}
			return false
		}
		// Default behavior: execute rollback on error
		rollbackManager.ExecuteRollback(disconnectedCtx, err)
		return true
	}

	return false
}

// ConvertDatastoreOperationToModel converts a datamodel.Job from storage to a models.Job for API responses.
func ConvertDatastoreOperationToModel(job *datamodel.Job) *models.Job {
	if job == nil {
		return nil
	}
	modelJob := &models.Job{
		BaseModel: models.BaseModel{
			ID:        job.ID,
			UUID:      job.UUID,
			CreatedAt: job.CreatedAt,
			UpdatedAt: job.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(job.DeletedAt),
		},
		CorrelationID: job.CorrelationID,
		TrackingID:    job.TrackingID,
		Type:          datamodel.JobType(job.Type),
		State:         datamodel.JobState(job.State),
		JobAttributes: &models.JobAttributes{},
		ResourceName:  job.ResourceName,
		ErrorDetails:  []byte(job.ErrorDetails),
	}

	if job.JobAttributes != nil {
		modelJob.JobAttributes.ResourceUUID = job.JobAttributes.ResourceUUID
		modelJob.JobAttributes.PoolUUID = job.JobAttributes.PoolUUID
	}
	return modelJob
}

// ApplyRestrictedActionsToVolume copies non-empty restricted_actions onto volume_attributes.
func ApplyRestrictedActionsToVolume(volume *datamodel.Volume, restrictedActions []string) {
	if volume == nil || len(restrictedActions) == 0 {
		return
	}
	if volume.VolumeAttributes == nil {
		volume.VolumeAttributes = &datamodel.VolumeAttributes{}
	}
	volume.VolumeAttributes.RestrictedActions = restrictedActions
}

// RestrictedActionsClear returns a *[]string that clears restrictions on volume update.
func RestrictedActionsClear() *[]string {
	empty := []string{}
	return &empty
}

// RestrictedActionsSet returns a *[]string that replaces restrictions on volume update.
func RestrictedActionsSet(actions ...string) *[]string {
	v := append([]string(nil), actions...)
	return &v
}

// ChunkSlice splits slice into sub-slices of at most size elements.
// Returns nil when slice is empty. When size <= 0, returns a single chunk containing all elements.
func ChunkSlice[T any](slice []T, size int) [][]T {
	if len(slice) == 0 {
		return nil
	}
	if size <= 0 {
		size = len(slice)
	}

	batches := make([][]T, 0, (len(slice)+size-1)/size)
	for start := 0; start < len(slice); start += size {
		end := start + size
		if end > len(slice) {
			end = len(slice)
		}
		batches = append(batches, slice[start:end])
	}
	return batches
}

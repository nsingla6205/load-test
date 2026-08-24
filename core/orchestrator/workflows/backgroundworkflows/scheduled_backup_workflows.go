package backgroundworkflows

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	scheduledBackupTimestampFormat = "2006-01-02-150405"
	scheduledBackupVolumeBatchSize = 20 // Number of volumes to fetch per batch when processing scheduled backups
	PoolAccessModeOntap            = "ONTAP"
)

var (
	hydrationEnabled                          = env.GetBool("GCP_HYDRATE_ENABLED", true)
	scheduledWeeklyBackupDay                  = env.GetInt("SCHEDULED_WEEKLY_BACKUP_DAY", 1)  // Default to Monday (0=Sunday, 1=Monday, ..., 6=Saturday)
	scheduledMonthlyBackupDay                 = env.GetInt("SCHEDULED_MONTHLY_BACKUP_DAY", 1) // Default to 1st day of the month
	scheduleBackupWorkflowHeartbeatTimeoutSec = env.GetUint64("SCHEDULE_BACKUP_WORKFLOW_HEARTBEAT_TIMEOUT_SEC", 600)
	pollBackupPolicyTimeout                   = 1 * time.Hour    // Timeout for polling backup policy to reach READY state
	pollBackupPolicyInterval                  = 30 * time.Second // Interval between polling attempts for backup policy state
)

type baseScheduledBackupWorkflow struct {
	workflows.BaseWorkflow
}

type createScheduledBackupInitWorkflow struct {
	baseScheduledBackupWorkflow
}

// Enforcing workflows.WorkflowInterface interface on all the scheduled backup workflows
var (
	_ workflows.WorkflowInterface = &createScheduledBackupInitWorkflow{}
	_ workflows.WorkflowInterface = &createScheduledBackupWorkflow{}
	_ workflows.WorkflowInterface = &deleteScheduledBackupWorkflow{}
)

// CreateScheduledBackupInitWorkflow initializes the scheduled backup workflow for a given backup policy.
func CreateScheduledBackupInitWorkflow(ctx workflow.Context, backupPolicy *datamodel.BackupPolicy) error {
	correlationID := utils.RandomUUID()
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		string(middleware.RequestCorrelationID): correlationID,
	})

	createScheduledBackupInitWF := new(createScheduledBackupInitWorkflow)
	createdJob, err := createScheduledBackupInitWF.CreateJob(
		ctx, backupPolicy.AccountID, backupPolicy.Name, string(datamodel.JobTypeInitCreateScheduledBackup))
	if err != nil {
		return err
	}

	err = createScheduledBackupInitWF.Setup(ctx, createdJob)
	if err != nil {
		return err
	}
	createScheduledBackupInitWF.Status = workflows.WorkflowStatusRunning

	_, workflowErr := createScheduledBackupInitWF.Run(ctx, backupPolicy)
	if workflowErr != nil {
		createScheduledBackupInitWF.Status = workflows.WorkflowStatusFailed
		err2 := createScheduledBackupInitWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			createScheduledBackupInitWF.Logger.Errorf("Failed to update job status: %v", err2)
		}
		return workflowErr
	}
	createScheduledBackupInitWF.Status = workflows.WorkflowStatusCompleted
	err2 := createScheduledBackupInitWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		createScheduledBackupInitWF.Logger.Errorf("Failed to update job status: %v", err2)
		return err2
	}
	return nil
}

// Setup initializes the workflow with necessary parameters and sets up a query handler for status.
func (wf *createScheduledBackupInitWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

// Run executes the scheduled backup workflow for the given backup policy.
func (wf *createScheduledBackupInitWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	backupPolicy := args[0].(*datamodel.BackupPolicy)
	wf.Logger.Infof("scheduled backup workflow triggered for the backup policy: %s", backupPolicy.UUID)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(scheduleBackupWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	rollbackManager := common.NewRollbackManager()
	scheduledBackupActivities := backgroundactivities.ScheduledBackupActivity{}

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	offset := 0
	totalVolumesProcessed := 0

	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})

	// Fetch the backup policy from the database as the input backup policy in the temporal schedule could be outdated.
	// Poll until the backup policy is in READY state, handling UPDATING/DELETING states and NotFound errors.
	startTime := workflow.Now(ctx)

	for {
		// Check if timeout has been reached
		if workflow.Now(ctx).Sub(startTime) > pollBackupPolicyTimeout {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrTimeLimitExceeded, fmt.Errorf("backup policy %s did not reach READY state within %v", backupPolicy.UUID, pollBackupPolicyTimeout))
		}

		// Fetch the backup policy from the database
		err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.GetBackupPolicyByUUID,
			backupPolicy.UUID, backupPolicy.AccountID).Get(ctx, &backupPolicy)
		if err != nil {
			// Check if the backup policy was deleted (NotFound error)
			customErr := workflows.ConvertToVSAError(err)
			if customErr != nil && customErr.TrackingID == vsaerrors.ErrResourceNotFound {
				wf.Logger.Infof("Backup policy %s was deleted, exiting workflow", backupPolicy.UUID)
				return nil, nil
			}
			return nil, customErr
		}

		// Check backup policy state and decide whether to proceed or continue polling
		switch backupPolicy.LifeCycleState {
		case datamodel.LifeCycleStateREADY:
			// Backup policy is ready, proceed with scheduled backup
			wf.Logger.Infof("Backup policy %s is in READY state, proceeding with scheduled backup", backupPolicy.UUID)

		case datamodel.LifeCycleStateDeleted:
			// Backup policy was deleted, exit gracefully
			wf.Logger.Infof("Backup policy %s is DELETED, exiting workflow", backupPolicy.UUID)
			return nil, nil

		case datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateDeleting:
			// Backup policy is in transition state, continue polling
			wf.Logger.Infof("Backup policy %s is in %s state, waiting for it to reach READY state", backupPolicy.UUID, backupPolicy.LifeCycleState)
			err = workflow.Sleep(ctx, pollBackupPolicyInterval)
			if err != nil {
				return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to sleep while waiting for backup policy %s: %w", backupPolicy.UUID, err))
			}
			continue

		default:
			// Unexpected state, return error
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("backup policy %s is in %s state, expected READY state", backupPolicy.UUID, backupPolicy.LifeCycleState))
		}

		// Break out of the loop if backup policy is in READY state
		if backupPolicy.LifeCycleState == datamodel.LifeCycleStateREADY {
			break
		}
	}

	for {
		var volumes []*datamodel.Volume
		err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.GetVolumesByBackupPolicyUUID,
			backupPolicy.UUID, backupPolicy.AccountID, scheduledBackupVolumeBatchSize, offset).Get(ctx, &volumes)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if len(volumes) == 0 {
			break
		}

		wf.Logger.Infof("Fetched %d volumes for scheduled backup (backup policy %s, offset %d, batch size %d)",
			len(volumes), backupPolicy.UUID, offset, scheduledBackupVolumeBatchSize)

		for _, volume := range volumes {
			if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
				if volume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
					wf.Logger.Info("Scheduled Backup creation is skipped when the volume is splitting", "volume_id", volume.UUID)
					continue
				}
			}
			wf.Logger.Debugf("Creating scheduled backup for volume: %s with backup policy: %s (offset: %d, limit: %d)", volume.UUID, backupPolicy.UUID, offset, scheduledBackupVolumeBatchSize)
			_ = workflow.ExecuteChildWorkflow(
				ctx,
				CreateScheduledBackupWorkflow,
				volume,
				backupPolicy,
			)
		}

		totalVolumesProcessed += len(volumes)

		if len(volumes) < scheduledBackupVolumeBatchSize {
			break
		}

		// Move to next batch
		offset += scheduledBackupVolumeBatchSize
	}

	if totalVolumesProcessed == 0 {
		wf.Logger.Infof("No volumes found with scheduled backups enabled for backup policy: %s", backupPolicy.UUID)
	} else {
		wf.Logger.Infof("Completed scheduled backup initialization for %d volumes with backup policy: %s",
			totalVolumesProcessed, backupPolicy.UUID)
	}

	return nil, nil
}

type createScheduledBackupWorkflow struct {
	baseScheduledBackupWorkflow
}

// CreateScheduledBackupWorkflow creates a scheduled backup for a given volume and backup policy.
func CreateScheduledBackupWorkflow(ctx workflow.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy) error {
	createScheduledBackupWF := new(createScheduledBackupWorkflow)
	createdJob, err := createScheduledBackupWF.CreateJob(
		ctx, backupPolicy.AccountID, volume.Name, string(datamodel.JobTypeCreateScheduledBackup))
	if err != nil {
		return workflows.ConvertToVSAError(err)
	}

	err = createScheduledBackupWF.Setup(ctx, createdJob)
	if err != nil {
		return err
	}
	createScheduledBackupWF.Status = workflows.WorkflowStatusRunning

	_, workflowErr := createScheduledBackupWF.Run(ctx, volume, backupPolicy, createdJob)
	if workflowErr != nil {
		if workflow.IsContinueAsNewError(workflowErr.OriginalErr) {
			return workflowErr
		}
		createScheduledBackupWF.Status = workflows.WorkflowStatusFailed
		err2 := createScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			createScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		}
		return workflowErr
	}
	createScheduledBackupWF.Status = workflows.WorkflowStatusCompleted
	err2 := createScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		createScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		return err2
	}
	return nil
}

// CreateScheduledBackupWorkflowWithContext processes scheduled backup with context for continuation
func CreateScheduledBackupWorkflowWithContext(ctx workflow.Context, scheduledBackupContext *activities.BackupActivitiesContext) error {
	if scheduledBackupContext.CorrelationID != "" {
		ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
			string(middleware.RequestCorrelationID): scheduledBackupContext.CorrelationID,
		})
	}

	createScheduledBackupWF := new(createScheduledBackupWorkflow)
	createdJob := scheduledBackupContext.ScheduledBackupParams.Job
	err := createScheduledBackupWF.Setup(ctx, createdJob)
	if err != nil {
		return err
	}
	createScheduledBackupWF.Status = workflows.WorkflowStatusRunning

	_, workflowErr := createScheduledBackupWF.RunScheduledBackupWithContext(ctx, scheduledBackupContext)
	if workflowErr != nil {
		if workflow.IsContinueAsNewError(workflowErr.OriginalErr) {
			return workflowErr
		}
		createScheduledBackupWF.Status = workflows.WorkflowStatusFailed
		err2 := createScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			createScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		}
		return workflowErr
	}

	createScheduledBackupWF.Status = workflows.WorkflowStatusCompleted
	err2 := createScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		createScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		return err2
	}
	return nil
}

// Setup initializes the workflow with necessary parameters and sets up a query handler for status.
func (wf *createScheduledBackupWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	job := input.(*datamodel.Job)
	wf.ID = job.UUID
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

// Run executes the scheduled backup workflow for the given volume and backup policy.
func (wf *createScheduledBackupWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	volume := args[0].(*datamodel.Volume)
	backupPolicy := args[1].(*datamodel.BackupPolicy)
	job := args[2].(*datamodel.Job)
	scheduledBackupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Volume: volume,
		},
		ScheduledBackupParams: &activities.ScheduledBackupParams{
			BackupPolicy: backupPolicy,
			Job:          job,
		},
	}
	wf.Logger.Infof("create scheduled backup workflow triggered for the backup policy: %s, volume: %s", backupPolicy.UUID, volume.UUID)
	return wf.RunScheduledBackupWithContext(ctx, scheduledBackupActivitiesContext)
}

// RunScheduledBackupWithContext executes the scheduled backup workflow with context for continuation
func (wf *createScheduledBackupWorkflow) RunScheduledBackupWithContext(ctx workflow.Context, scheduledBackupContext *activities.BackupActivitiesContext) (interface{}, *vsaerrors.CustomError) {
	volume := scheduledBackupContext.BackupWorkflowInit.Volume
	backupPolicy := scheduledBackupContext.ScheduledBackupParams.BackupPolicy
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Check if this is a continuation workflow
	info := workflow.GetInfo(ctx)
	isContinuation := info.ContinuedExecutionRunID != ""
	var backups []*datamodel.Backup
	preTransferRollbackManager := common.NewRollbackManager()
	postTransferRollbackManager := common.NewRollbackManager()
	backupActivities := &activities.BackupActivity{}
	scheduledBackupActivities := backgroundactivities.ScheduledBackupActivity{}

	var preTransferErr, postTransferErr error
	defer func() {
		if postTransferErr != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			postTransferRollbackManager.ExecuteRollback(disconnectedCtx, postTransferErr)
		} else if preTransferErr != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			preTransferRollbackManager.ExecuteRollback(disconnectedCtx, preTransferErr)
		}
	}()
	if isContinuation {
		wf.Logger.Infof("Resuming backup workflow from continuation: workflowID=%s continuedFromRunID=%v snapshotName=%s transferStatus=%v",
			wf.ID, info.OriginalRunID, scheduledBackupContext.SnapshotName, scheduledBackupContext.TransferStatus)
		registerScheduledPreTransferRollback(preTransferRollbackManager, scheduledBackupContext, backupActivities, scheduledBackupActivities)
	} else {
		if cid, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
			scheduledBackupContext.CorrelationID = cid
		}

		// check if volume is expertMode or not by pool API access mode. If it's ONTAP.
		if volume.Pool != nil && volume.Pool.APIAccessMode == PoolAccessModeOntap {
			scheduledBackupContext.IsExpertMode = true
			if volume.VolumeAttributes == nil {
				return nil, workflows.ConvertToVSAError(fmt.Errorf("volume %s has no volume attributes", volume.UUID))
			}
			volume.VolumeAttributes.VendorSubnetID = volume.Pool.Network
		}

		var backupVault *datamodel.BackupVault
		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.GetBackupVault, volume.DataProtection.BackupVaultID).Get(ctx, &backupVault)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		scheduledBackupContext.BackupWorkflowInit.BackupVault = backupVault

		timestamp := workflow.Now(ctx).Format(scheduledBackupTimestampFormat)
		if backupPolicy.DailyBackupsToKeep >= 2 {
			wf.Logger.Infof("Creating daily scheduled backup: volumeUUID=%s backupPolicyUUID=%s",
				volume.UUID, backupPolicy.UUID)
			var backup *datamodel.Backup
			preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.CreateScheduledBackup, volume, backupVault, timestamp, common.ScheduleTagDaily, scheduledBackupContext.IsExpertMode).Get(ctx, &backup)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
			preTransferRollbackManager.AddActivity(backupActivities.DeleteBackup, backup.UUID)
			backups = append(backups, backup)
		}

		today := workflow.Now(ctx).Weekday()
		if backupPolicy.WeeklyBackupsToKeep > 0 && today == time.Weekday(scheduledWeeklyBackupDay) {
			wf.Logger.Infof("Creating weekly scheduled backup: volumeUUID=%s backupPolicyUUID=%s",
				volume.UUID, backupPolicy.UUID)
			var backup *datamodel.Backup
			preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.CreateScheduledBackup, volume, backupVault, timestamp, common.ScheduleTagWeekly, scheduledBackupContext.IsExpertMode).Get(ctx, &backup)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
			preTransferRollbackManager.AddActivity(backupActivities.DeleteBackup, backup.UUID)
			backups = append(backups, backup)
		}

		_, _, day := workflow.Now(ctx).Date()
		if backupPolicy.MonthlyBackupsToKeep > 0 && day == scheduledMonthlyBackupDay {
			wf.Logger.Infof("Creating monthly scheduled backup: volumeUUID=%s backupPolicyUUID=%s",
				volume.UUID, backupPolicy.UUID)
			var backup *datamodel.Backup
			preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.CreateScheduledBackup, volume, backupVault, timestamp, common.ScheduleTagMonthly, scheduledBackupContext.IsExpertMode).Get(ctx, &backup)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
			preTransferRollbackManager.AddActivity(backupActivities.DeleteBackup, backup.UUID)
			backups = append(backups, backup)
		}

		// Exit early if there are no backups to create (e.g., daily backup retention is 0 and no weekly or monthly backups are required)
		if len(backups) == 0 {
			return nil, nil
		}
		scheduledBackupContext.ScheduledBackupParams.Backups = backups

		var node *models.Node
		preTransferErr = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, volume.Pool).Get(ctx, &node)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		scheduledBackupContext.Node = node

		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.PrepareObjectStoreActivity, scheduledBackupContext).Get(ctx, &scheduledBackupContext)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		wf.Logger.Infof("Using backup vault and object store for scheduled backup: backupVaultUUID=%s objectStoreName=%s", backupVault.UUID, scheduledBackupContext.ObjStoreName)

		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.GetOrCreateObjectStoreActivity, scheduledBackupContext).Get(ctx, &scheduledBackupContext)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}

		snapmirrorRelationship := &common.SnapmirrorRelationship{}
		smSourcePath := activities.GetSmSourcePath(volume)
		if volume.VolumeAttributes == nil {
			return nil, workflows.ConvertToVSAError(fmt.Errorf("volume %s has no volume attributes", volume.UUID))
		}
		smDestinationPath, pathErr := activities.GetSmDestinationPath(backupVault, volume)
		if pathErr != nil {
			return nil, workflows.ConvertToVSAError(pathErr)
		}
		scheduledBackupContext.SmSourcePath = smSourcePath
		scheduledBackupContext.SmDestinationPath = smDestinationPath
		wf.Logger.Infof("Snapmirror paths for scheduled backup: sourcePath=%s destinationPath=%s", smSourcePath, smDestinationPath)
		SnapmirrorRelationshipParams := &common.SnapmirrorRelationshipParams{
			SourcePath:      smSourcePath,
			DestinationPath: smDestinationPath,
			SourceUUID:      nil,
			IsRestore:       false,
		}
		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.SnapmirrorGetOrCreate, scheduledBackupContext.Node, &SnapmirrorRelationshipParams).Get(ctx, &snapmirrorRelationship)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		scheduledBackupContext.SnapmirrorRelationship = snapmirrorRelationship

		// Backups from this run, excluded by the rollback below and the transfer check further down.
		excludeBackupUUIDs := scheduledBackupExcludeUUIDs(backups)

		// In ONTAP mode the customer owns volume delete, and ONTAP refuses to delete a volume that
		// still has a backup relationship, so a backup that never completes must not leave one behind.
		registerScheduledSnapmirrorRollback(preTransferRollbackManager, scheduledBackupContext, backupActivities, backups)

		var snapshotName string
		preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.GenerateScheduledSnapshotName, timestamp).Get(ctx, &snapshotName)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		scheduledBackupContext.SnapshotName = snapshotName
		var dbSnapshot *datamodel.Snapshot
		if !scheduledBackupContext.IsExpertMode {
			preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.CreateBackupSnapshotInDB, volume, snapshotName).Get(ctx, &dbSnapshot)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
			scheduledBackupContext.DbSnapshot = dbSnapshot
			preTransferRollbackManager.AddActivity(scheduledBackupActivities.DeleteBackupSnapshotInDB, dbSnapshot.UUID)
		}

		if scheduledBackupContext.IsExpertMode {
			// fetch protocol for a volume from ontap
			preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.GetVolumeProtocolsFromOntapActivity, scheduledBackupContext).Get(ctx, &scheduledBackupContext)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
			volume.VolumeAttributes.Protocols = scheduledBackupContext.BackupWorkflowInit.Volume.VolumeAttributes.Protocols
		}

		ontapSnapshot := &vsa.SnapshotProviderResponse{}
		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.SnapshotCreate, scheduledBackupContext.Node, volume.VolumeAttributes.ExternalUUID, snapshotName, activities.BackupComment).Get(ctx, &ontapSnapshot)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
		wf.Logger.Infof("Created ONTAP snapshot for scheduled backup: snapshotName=%s", snapshotName)
		scheduledBackupContext.ScheduledBackupParams.OntapSnapshot = ontapSnapshot

		preTransferRollbackManager.AddActivity(backupActivities.DeleteBackupSnapshot, scheduledBackupContext.Node, ontapSnapshot.ExternalUUID, volume.VolumeAttributes.ExternalUUID)
		if !scheduledBackupContext.IsExpertMode {
			preTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.UpdateBackupSnapshotInDB, dbSnapshot, ontapSnapshot).Get(ctx, &dbSnapshot)
			if preTransferErr != nil {
				return nil, workflows.ConvertToVSAError(preTransferErr)
			}
		}

		// Check if any backup for the volume is in CREATING/DELETING state before starting Snapmirror transfer
		// This prevents parallel transfers on the same Snapmirror instance
		checkBackupStateActivityOptions := workflow.ActivityOptions{
			StartToCloseTimeout:    30 * time.Second,
			ScheduleToCloseTimeout: 23 * time.Hour,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:        1 * time.Second,
				BackoffCoefficient:     2.0,
				MaximumInterval:        10 * time.Minute,
				NonRetryableErrorTypes: []string{"PanicError"},
			},
		}
		checkBackupStateCtx := workflow.WithActivityOptions(ctx, checkBackupStateActivityOptions)

		var volumeUUIDToCheckBackupsInProgress string
		if !scheduledBackupContext.IsExpertMode {
			volumeUUIDToCheckBackupsInProgress = volume.UUID
		} else {
			volumeUUIDToCheckBackupsInProgress = volume.VolumeAttributes.ExternalUUID
		}

		preTransferErr = workflow.ExecuteActivity(
			checkBackupStateCtx,
			scheduledBackupActivities.CheckBackupsInProgressByVolume,
			volumeUUIDToCheckBackupsInProgress,
			excludeBackupUUIDs,
			backups[0].CreatedAt,
		).Get(ctx, nil)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}

		wf.Logger.Infof("Starting snapmirror transfer for scheduled backup: snapshotName=%s", snapshotName)
		preTransferErr = workflow.ExecuteActivity(ctx, backupActivities.SnapmirrorTransfer, scheduledBackupContext.Node, snapmirrorRelationship.UUID, snapshotName).Get(ctx, nil)
		if preTransferErr != nil {
			return nil, workflows.ConvertToVSAError(preTransferErr)
		}
	}

	err = wf.PollTransferStatusWithContinueAsNew(ctx, scheduledBackupContext)
	if err != nil {
		if !workflow.IsContinueAsNewError(err) {
			preTransferErr = err
		}
		return nil, workflows.ConvertToVSAError(err)
	}
	wf.Logger.Infof("Snapmirror transfer polling complete for scheduled backup: volumeUUID=%s snapshotName=%s backupPolicyUUID=%s",
		volume.UUID, scheduledBackupContext.SnapshotName, backupPolicy.UUID)

	// Wait for 30 seconds before proceeding
	err = workflow.Sleep(ctx, 30*time.Second)
	if err != nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to sleep before getting the snapmirror: %w", err))
	}

	// Get snapmirror relationship to check health status
	var smRelationship *common.SnapmirrorRelationship
	err = workflow.ExecuteActivity(ctx, backupActivities.GetSnapmirror, scheduledBackupContext.Node, scheduledBackupContext.SmSourcePath, scheduledBackupContext.SmDestinationPath).Get(ctx, &smRelationship)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if smRelationship.State != nil && *smRelationship.State != datamodel.OntapSnapmirrored {
		unhealthyMsg := ""
		if smRelationship.Healthy != nil && !*smRelationship.Healthy && smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			unhealthyMsg = fmt.Sprintf(" Unhealthy reasons: %v", *smRelationship.UnhealthyReason)
		}
		wf.Logger.Infof("Snapmirror relationship state is %s, expected %s.%s", *smRelationship.State, datamodel.OntapSnapmirrored, unhealthyMsg)
		preTransferErr = vsaerrors.New("snapmirror relationship state is not snapmirrored")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, preTransferErr)
	}

	if smRelationship.Healthy != nil && !*smRelationship.Healthy {
		if smRelationship.UnhealthyReason != nil && len(*smRelationship.UnhealthyReason) > 0 {
			wf.Logger.Infof("Snapmirror relationship is unhealthy. Reasons: %v", *smRelationship.UnhealthyReason)
		}
		preTransferErr = vsaerrors.New("snapmirror relationship is unhealthy")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, preTransferErr)
	}

	if smRelationship.TotalTransferBytes != nil && *smRelationship.TotalTransferBytes > 0 {
		for i := range scheduledBackupContext.ScheduledBackupParams.Backups {
			scheduledBackupContext.ScheduledBackupParams.Backups[i].Attributes.TotalTransferBytes = *smRelationship.TotalTransferBytes
		}
	}

	backups = scheduledBackupContext.ScheduledBackupParams.Backups
	for _, backup := range backups {
		backup.Attributes.SnapshotName = scheduledBackupContext.SnapshotName
		backup.Attributes.SnapshotID = scheduledBackupContext.ScheduledBackupParams.OntapSnapshot.ExternalUUID
		backup.Attributes.VolumeName = volume.Name
		backup.Attributes.SourceVolumeZone = volume.Pool.PoolAttributes.PrimaryZone
		backup.Attributes.IsRegionalHA = volume.Pool.PoolAttributes.IsRegionalHA
		backup.Attributes.SnapshotCreationTime = workflow.Now(ctx).String()
		backup.Attributes.BucketName = scheduledBackupContext.BucketDetails.BucketName
		backup.Attributes.ServiceAccountName = scheduledBackupContext.BucketDetails.ServiceAccountName
		backup.Attributes.AccountIdentifier = volume.Account.Name
		backup.Attributes.BackupPolicyName = backupPolicy.Name
		backup.Attributes.Protocols = volume.VolumeAttributes.Protocols
		backup.Attributes.ObjectStoreUUID = scheduledBackupContext.ObjStore.UUID
		backup.Attributes.EndpointUUID = *scheduledBackupContext.SnapmirrorRelationship.DestinationUUID
		backup.AssetMetadata = &datamodel.AssetMetadata{
			ChildAssets: []datamodel.ChildAsset{
				{
					AssetType:  common.BackupAssetType,
					AssetNames: []string{fmt.Sprintf("//storage.googleapis.com/%s", backup.Attributes.BucketName)},
				},
			},
		}

		postTransferRollbackManager.AddActivity(backupActivities.UpdateBackupError, backup)
		postTransferErr = workflow.ExecuteActivity(ctx, backupActivities.FinishBackup, backup).Get(ctx, nil)
		if postTransferErr != nil {
			return nil, workflows.ConvertToVSAError(postTransferErr)
		}
		wf.Logger.Infof("Scheduled backup transfer finished for backup record: backupName=%s sizeInBytes=%d totalTransferBytes=%d volumeUUID=%s",
			backup.Name, backup.SizeInBytes, backup.Attributes.TotalTransferBytes, volume.UUID)
	}
	// Update ConstituentCount for a backup from Volume
	if scheduledBackupContext.BackupWorkflowInit.Volume.LargeVolumeAttributes != nil && scheduledBackupContext.BackupWorkflowInit.Volume.LargeVolumeAttributes.LargeCapacity {
		postTransferErr = workflow.ExecuteActivity(ctx, backupActivities.UpdateConstituentCountForBackup, scheduledBackupContext).Get(ctx, &scheduledBackupContext)
		if postTransferErr != nil {
			return nil, workflows.ConvertToVSAError(postTransferErr)
		}
	}

	backup := backups[len(backups)-1]
	var objectStoreEndpointInfo vsa.SmObjectStoreEndpointt
	err = workflow.ExecuteActivity(ctx, backupActivities.GetObjectStoreEndpointInfo, scheduledBackupContext.Node, scheduledBackupContext.ObjStore.UUID, backup.Attributes.EndpointUUID).Get(ctx, &objectStoreEndpointInfo)
	if err != nil {
		wf.Logger.Errorf("Failed to get object store endpoint info for volume %s: %v", volume.Name, err)
	} else {
		backup.LatestLogicalBackupSize = *objectStoreEndpointInfo.LogicalSize
	}

	var objectStoreSnapshot *vsa.SmObjectStoreEndpointSnapshot
	err = workflow.ExecuteActivity(ctx, backupActivities.GetSnapshotFromObjectStore, scheduledBackupContext.Node, scheduledBackupContext.ObjStore.UUID, backup.Attributes.EndpointUUID, backup.Attributes.SnapshotID).Get(ctx, &objectStoreSnapshot)
	if err != nil {
		wf.Logger.Errorf("Failed to get snapshot from object store for volume %s: %v", volume.Name, err)
	} else {
		if objectStoreSnapshot.LogicalSize != nil {
			backup.SizeInBytes = *objectStoreSnapshot.LogicalSize
		} else {
			wf.Logger.Errorf("Logical size is nil for snapshot %s in object store for volume %s", backup.Attributes.SnapshotID, volume.Name)
			backup.SizeInBytes = 0
		}
	}

	// Update backup size fields in both backup and volume tables.
	err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.UpdateBackupSize, backup, volume, scheduledBackupContext.IsExpertMode).Get(ctx, nil)
	if err != nil {
		wf.Logger.Errorf("Failed to update backup size fields for volume %s: %v", volume.Name, err)
	}

	// Create remote backups from VCP if this is a cross-region backup
	// This is done after UpdateBackupSize to ensure all backup fields are set
	err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.CreateRemoteScheduledBackupsFromVCPActivity, scheduledBackupContext.BackupWorkflowInit.BackupVault, backups, volume, volume.Account.Name).Get(ctx, nil)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		wf.Logger.Errorf("Failed to create remote backups from VCP for scheduled backups: %v", err)
	}

	if hydrationEnabled {
		if !scheduledBackupContext.IsExpertMode {
			location := utils.GetLocation(*scheduledBackupContext.DbSnapshot)
			err = workflow.ExecuteActivity(ctx, backupActivities.HydrateSnapshotToCCFEActivity,
				scheduledBackupContext.DbSnapshot,
				volume.Name,
				location,
				volume.Account.Name).Get(ctx, nil)
			if err != nil {
				// Log the error but don't fail the entire workflow
				wf.Logger.Errorf("Failed to hydrate snapshot to CCFE for backup %s: %v", backup.Name, err)
			}
		}
		postTransferErr = workflow.ExecuteActivity(ctx, scheduledBackupActivities.HydrateCreatedBackupsToCCFE, volume, backups, scheduledBackupContext.BackupWorkflowInit.BackupVault.Name).Get(ctx, nil)
		if postTransferErr != nil {
			return nil, workflows.ConvertToVSAError(postTransferErr)
		}
	}

	// Create BackupMetadata entry if this is the first backup for the volume
	err = workflow.ExecuteActivity(ctx, backupActivities.CreateBackupMetadataIfFirstBackupActivity, volume, scheduledBackupContext.IsExpertMode).Get(ctx, nil)
	if err != nil {
		// Log the error but don't fail the entire backup workflow
		wf.Logger.Errorf("Failed to create BackupMetadata for volume %s: %v", volume.UUID, err)
	}

	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
	})
	_ = workflow.ExecuteChildWorkflow(ctx, DeleteScheduledBackupWorkflow, volume, scheduledBackupContext.ScheduledBackupParams.BackupPolicy)
	return nil, nil
}

type deleteScheduledBackupWorkflow struct {
	baseScheduledBackupWorkflow
}

// DeleteScheduledBackupWorkflow removes older scheduled backups for a volume and backup policy according to daily, weekly, and monthly retention limits.
func DeleteScheduledBackupWorkflow(ctx workflow.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy) error {
	deleteScheduledBackupWF := new(deleteScheduledBackupWorkflow)
	createdJob, err := deleteScheduledBackupWF.CreateJob(
		ctx, backupPolicy.AccountID, volume.Name, string(datamodel.JobTypeDeleteScheduledBackup))
	if err != nil {
		return err
	}

	err = deleteScheduledBackupWF.Setup(ctx, createdJob)
	if err != nil {
		return err
	}
	deleteScheduledBackupWF.Status = workflows.WorkflowStatusRunning

	_, workflowErr := deleteScheduledBackupWF.Run(ctx, volume, backupPolicy)
	if workflowErr != nil {
		deleteScheduledBackupWF.Status = workflows.WorkflowStatusFailed
		err2 := deleteScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			deleteScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		}
		return workflowErr
	}
	deleteScheduledBackupWF.Status = workflows.WorkflowStatusCompleted
	err2 := deleteScheduledBackupWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		deleteScheduledBackupWF.Logger.Errorf("Failed to update job status: %v", err2)
		return err2
	}
	return nil
}

// Setup initializes the workflow with necessary parameters and sets up a query handler for status.
func (wf *deleteScheduledBackupWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	job := input.(*datamodel.Job)
	wf.ID = job.UUID
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

// Run executes the scheduled backup deletion workflow for the given volume and backup policy.
func (wf *deleteScheduledBackupWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	volume := args[0].(*datamodel.Volume)
	backupPolicy := args[1].(*datamodel.BackupPolicy)
	wf.Logger.Infof("delete scheduled backup workflow triggered for the backup policy: %s, volume: %s", backupPolicy.UUID, volume.UUID)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    time.Duration(scheduleBackupWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	scheduledBackupActivities := backgroundactivities.ScheduledBackupActivity{}
	backupActivities := &activities.BackupActivity{}

	var backupVault *datamodel.BackupVault
	err = workflow.ExecuteActivity(ctx, backupActivities.GetBackupVault, volume.DataProtection.BackupVaultID).Get(ctx, &backupVault)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	isExpertMode := volume.Pool != nil && volume.Pool.APIAccessMode == PoolAccessModeOntap
	var node *models.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.ResolveOntapNodeForPool, volume.Pool).Get(ctx, &node)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Cleanup older backup snapshots for this volume
	if isExpertMode {
		// cleanup expertmode backup snapshot from ontap, no snapshot metadata/entry in DB for expert mode backups snapshots
		err = workflow.ExecuteActivity(ctx, backupActivities.CleanupOldExpertModeSnapshotActivity, volume, node).Get(ctx, nil)
		if err != nil {
			// Log the error but don't fail the entire backup workflow
			wf.Logger.Errorf("Failed to cleanup older expert mode backup snapshots for volume %s: %v", volume.Name, err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, backupActivities.CleanupOldBackupSnapshotsActivity, volume, node).Get(ctx, nil)
		if err != nil {
			// Log the error but don't fail the entire backup workflow
			wf.Logger.Errorf("Failed to cleanup older backup snapshots for volume %s: %v", volume.Name, err)
		}
	}

	var backupToBeDeleted []*datamodel.Backup
	err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.FetchScheduledBackupForDeletion, volume, backupPolicy, isExpertMode).Get(ctx, &backupToBeDeleted)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	wf.Logger.Infof("Delete scheduled backup workflow: found %d backups to delete for volume %s (backup policy %s)",
		len(backupToBeDeleted), volume.UUID, backupPolicy.UUID)

	// Exit early if there are no backups to delete
	if len(backupToBeDeleted) == 0 {
		return nil, nil
	}

	var objectStoreName string
	err = workflow.ExecuteActivity(ctx, backupActivities.GetObjStoreNameActivity, backupVault, volume).Get(ctx, &objectStoreName)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var objStore *common.CloudTarget
	err = workflow.ExecuteActivity(ctx, backupActivities.GetObjectStore, node, objectStoreName).Get(ctx, &objStore)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	for _, backup := range backupToBeDeleted {
		var isSharedBackup bool
		err = workflow.ExecuteActivity(ctx, backupActivities.IsBackupShared, backup).Get(ctx, &isSharedBackup)
		if err != nil {
			wf.Logger.Errorf("Failed to check if backup %s is shared: %v", backup.Name, err)
			return nil, workflows.ConvertToVSAError(err)
		}
		wf.Logger.Infof("Deleting scheduled backup: backupName=%s backupUUID=%s shared=%v volumeUUID=%s",
			backup.Name, backup.UUID, isSharedBackup, volume.UUID)
		if !isSharedBackup {
			rollbackManager.AddActivity(backupActivities.UpdateBackupError, backup)
			var ontapAsyncResponse *vsa.OntapAsyncResponse
			err = workflow.ExecuteActivity(ctx, backupActivities.DeleteSnapshotFromObjectStore, node, objStore.UUID, backup.Attributes.EndpointUUID, backup.Attributes.SnapshotID).Get(ctx, &ontapAsyncResponse)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			err = workflows.WaitForONTAPJob(ctx, ontapAsyncResponse, node, time.Minute*120)
			if err != nil {
				return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to delete cloud endpoint: %w", err))
			}
		} else {
			rollbackManager.AddActivity(backupActivities.MarkBackupAvailable, backup)
		}

		err = workflow.ExecuteActivity(ctx, backupActivities.DeleteBackup, backup.UUID).Get(ctx, nil)
		if err != nil {
			wf.Logger.Errorf("Failed to delete backup %s: %v", backup.Name, err)
			return nil, workflows.ConvertToVSAError(fmt.Errorf("failed to delete backup %s: %w", backup.Name, err))
		}

		// Delete remote backup from VCP if this is a cross-region backup
		if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.BackupRegionName != nil {
			remoteBackupErr := workflow.ExecuteActivity(ctx, scheduledBackupActivities.DeleteRemoteScheduledBackupFromVCPActivity,
				backup.UUID,
				backupVault.UUID,
				volume.Account.Name,
				*backupVault.BackupRegionName).Get(ctx, nil)
			if remoteBackupErr != nil {
				wf.Logger.Errorf("Failed to delete remote backup from VCP for backup %s: %v", backup.UUID, remoteBackupErr)
			}
		}
	}
	wf.Logger.Infof("Completed deletion of %d scheduled backups for volume %s", len(backupToBeDeleted), volume.UUID)
	// Hydrate all deleted backups to CCFE after processing all backups
	err = workflow.ExecuteActivity(ctx, scheduledBackupActivities.HydrateDeletedBackupsToCCFE, volume, backupToBeDeleted, backupVault.Name).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}

func (wf *baseScheduledBackupWorkflow) CreateJob(ctx workflow.Context, accountID int64, resourceName, jobType string) (*datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	correlationID := ""
	if cid, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
		correlationID = cid
	}

	// The job state is set to PROCESSING here because the workflow itself is creating the job
	info := workflow.GetInfo(ctx)
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{
			UUID: info.WorkflowExecution.ID,
		},
		AccountID:     sql.NullInt64{Int64: accountID, Valid: true},
		CorrelationID: correlationID,
		ResourceName:  resourceName,
		Type:          jobType,
		State:         string(datamodel.JobsStatePROCESSING),
	}

	commonActivities := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
	})

	var createdJob *datamodel.Job
	err := workflow.ExecuteActivity(ctx, commonActivities.CreateJob, job).Get(ctx, &createdJob)
	if err != nil {
		logger.Errorf("Failed to create job: %v", err)
		return nil, workflows.ConvertToVSAError(err)
	}
	return createdJob, nil
}

func (wf *createScheduledBackupWorkflow) PollTransferStatusWithContinueAsNew(ctx workflow.Context, backupActivitiesContext *activities.BackupActivitiesContext) error {
	return workflows.PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, CreateScheduledBackupWorkflowWithContext, backupActivitiesContext)
}

func scheduledBackupExcludeUUIDs(backups []*datamodel.Backup) []string {
	excludeBackupUUIDs := make([]string, 0, len(backups))
	for _, backup := range backups {
		if backup != nil {
			excludeBackupUUIDs = append(excludeBackupUUIDs, backup.UUID)
		}
	}
	return excludeBackupUUIDs
}

func registerScheduledSnapmirrorRollback(rollbackManager *common.RollbackManager, ctx *activities.BackupActivitiesContext, backupActivities *activities.BackupActivity, backups []*datamodel.Backup) {
	if ctx == nil || !ctx.IsExpertMode || ctx.SnapmirrorRelationship == nil || len(backups) == 0 || backups[0] == nil {
		return
	}
	rollbackManager.AddActivity(backupActivities.DeleteSnapmirrorIfUnused, ctx.Node, backups[0].VolumeUUID,
		ctx.SnapmirrorRelationship.EndpointUUID(), scheduledBackupExcludeUUIDs(backups), ctx.SnapmirrorRelationship.UUID)
}

func registerScheduledPreTransferRollback(rollbackManager *common.RollbackManager, ctx *activities.BackupActivitiesContext, backupActivities *activities.BackupActivity, scheduledBackupActivities backgroundactivities.ScheduledBackupActivity) {
	if ctx == nil || ctx.ScheduledBackupParams == nil {
		return
	}
	backups := ctx.ScheduledBackupParams.Backups
	for _, backup := range backups {
		if backup != nil {
			rollbackManager.AddActivity(backupActivities.DeleteBackup, backup.UUID)
		}
	}
	registerScheduledSnapmirrorRollback(rollbackManager, ctx, backupActivities, backups)
	if !ctx.IsExpertMode && ctx.DbSnapshot != nil {
		rollbackManager.AddActivity(scheduledBackupActivities.DeleteBackupSnapshotInDB, ctx.DbSnapshot.UUID)
	}
	if snapshot := ctx.ScheduledBackupParams.OntapSnapshot; snapshot != nil && ctx.BackupWorkflowInit != nil &&
		ctx.BackupWorkflowInit.Volume != nil && ctx.BackupWorkflowInit.Volume.VolumeAttributes != nil {
		rollbackManager.AddActivity(backupActivities.DeleteBackupSnapshot, ctx.Node,
			snapshot.ExternalUUID, ctx.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID)
	}
}

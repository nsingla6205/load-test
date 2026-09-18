package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type syncBackupZizsWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &syncBackupZizsWorkflow{}

func SyncBackupZiZsWorkflow(ctx workflow.Context) error {
	syncBackupZiZsWF := new(syncBackupZizsWorkflow)
	err := syncBackupZiZsWF.Setup(ctx, nil)
	if err != nil {
		return err
	}
	syncBackupZiZsWF.Status = workflows.WorkflowStatusRunning

	_, workflowErr := syncBackupZiZsWF.Run(ctx)
	if workflowErr != nil {
		syncBackupZiZsWF.Status = workflows.WorkflowStatusFailed
		return workflowErr
	}
	syncBackupZiZsWF.Status = workflows.WorkflowStatusCompleted
	return nil
}

func (wf *syncBackupZizsWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

func (wf *syncBackupZizsWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting Sync ZiZs Workflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
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
	syncBackupZiZsActivity := backgroundactivities.SyncBackupZiZsActivity{}

	var workflowErr error
	defer func() {
		if workflowErr != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, workflowErr)
		}
	}()

	logger.Info("Getting all backup vaults from database")
	var backupVaults []*datamodel.BackupVault
	workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.GetAllBackupVaults).Get(ctx, &backupVaults)
	if workflowErr != nil {
		logger.Error("Failed to get backup vaults", "error", workflowErr)
		return nil, workflows.ConvertToVSAError(workflowErr)
	}
	logger.Info("Successfully retrieved backup vaults", "backupVaultCount", len(backupVaults))

	for i, backupVault := range backupVaults {
		logger.Info("Processing backup vault", "index", i+1, "backupVaultUUID", backupVault.UUID)

		if len(backupVault.BucketDetails) == 0 {
			logger.Warn("No bucket details found for backup vault, skipping", "backupVaultUUID", backupVault.UUID)
			continue
		}

		// Process each bucket detail
		for _, bucketDetail := range backupVault.BucketDetails {
			if bucketDetail == nil {
				logger.Warn("Bucket details entry is nil for backup vault, skipping", "backupVaultUUID", backupVault.UUID)
				continue
			}
			if bucketDetail.BucketName == "" {
				logger.Warn("Bucket name is empty in bucket details, skipping",
					"tenantProjectNumber", bucketDetail.TenantProjectNumber, "backupVaultUUID", backupVault.UUID)
				continue
			}

			logger.Info("Processing bucket", "bucketName", bucketDetail.BucketName, "backupVaultUUID", backupVault.UUID)

			var updatedBucketDetails *datamodel.BucketDetails
			workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.SyncBucketDetails, bucketDetail).Get(ctx, &updatedBucketDetails)
			if workflowErr != nil {
				logger.Error("Failed to get bucket details", "bucketName", bucketDetail.BucketName, "error", workflowErr)
				// Continue with next bucket instead of failing entire workflow
				continue
			}
			logger.Info("Successfully retrieved bucket details", "bucketName", bucketDetail.BucketName,
				"satisfiesPZI", updatedBucketDetails.SatisfiesPzi, "satisfiesPZS", updatedBucketDetails.SatisfiesPzs)

			// Update the bucket detail in the backup vault with the new information
			bucketDetail.SatisfiesPzi = updatedBucketDetails.SatisfiesPzi
			bucketDetail.SatisfiesPzs = updatedBucketDetails.SatisfiesPzs
		}

		// Update backup vault with synced ZiZs information
		logger.Info("Updating backup vault with synced ZiZs information", "backupVaultUUID", backupVault.UUID)
		workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.UpdateBackupVault, backupVault).Get(ctx, nil)
		if workflowErr != nil {
			logger.Error("Failed to update backup vault", "backupVaultUUID", backupVault.UUID, "error", workflowErr)
			// Continue with next backup vault instead of failing entire workflow
			continue
		}
		logger.Info("Successfully updated backup vault", "backupVaultUUID", backupVault.UUID)
	}

	logger.Info("Sync Backup ZiZs workflow completed successfully")
	return nil, nil
}

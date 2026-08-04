package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
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

func (wf *syncBackupZizsWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	wf.Logger.Infof("Starting Sync ZiZs Workflow")

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

	wf.Logger.Infof("Getting all backup vaults from database")
	var backupVaults []*datamodel.BackupVault
	workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.GetAllBackupVaults).Get(ctx, &backupVaults)
	if workflowErr != nil {
		wf.Logger.Errorf("Failed to get backup vaults: %v", workflowErr)
		return nil, workflows.ConvertToVSAError(workflowErr)
	}
	wf.Logger.Infof("Successfully retrieved %d backup vaults", len(backupVaults))

	for i, backupVault := range backupVaults {
		wf.Logger.Infof("Processing backup vault %d - %s", i+1, backupVault.UUID)

		if len(backupVault.BucketDetails) == 0 {
			wf.Logger.Warnf("No bucket details found for backup vault %s, skipping", backupVault.UUID)
			continue
		}

		// Process each bucket detail
		for _, bucketDetail := range backupVault.BucketDetails {
			if bucketDetail == nil {
				wf.Logger.Warnf("Bucket details entry is nil for backup vault %s, skipping", backupVault.UUID)
				continue
			}
			if bucketDetail.BucketName == "" {
				wf.Logger.Warnf("Bucket name is empty in bucket details for tenant project: %s in backup vault %s, skipping", bucketDetail.TenantProjectNumber, backupVault.UUID)
				continue
			}

			wf.Logger.Infof("Processing bucket %s for backup vault %s", bucketDetail.BucketName, backupVault.UUID)

			var updatedBucketDetails *datamodel.BucketDetails
			workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.SyncBucketDetails, bucketDetail).Get(ctx, &updatedBucketDetails)
			if workflowErr != nil {
				wf.Logger.Errorf("Failed to get bucket details for bucket %s: %v", bucketDetail.BucketName, workflowErr)
				// Continue with next bucket instead of failing entire workflow
				continue
			}
			wf.Logger.Infof("Successfully retrieved bucket details for %s - PZI: %t, PZS: %t", bucketDetail.BucketName, updatedBucketDetails.SatisfiesPzi, updatedBucketDetails.SatisfiesPzs)

			// Update the bucket detail in the backup vault with the new information
			bucketDetail.SatisfiesPzi = updatedBucketDetails.SatisfiesPzi
			bucketDetail.SatisfiesPzs = updatedBucketDetails.SatisfiesPzs
		}

		// Update backup vault with synced ZiZs information
		wf.Logger.Infof("Updating backup vault %s with synced ZiZs information", backupVault.UUID)
		workflowErr = workflow.ExecuteActivity(ctx, syncBackupZiZsActivity.UpdateBackupVault, backupVault).Get(ctx, nil)
		if workflowErr != nil {
			wf.Logger.Errorf("Failed to update backup vault %s: %v", backupVault.UUID, workflowErr)
			// Continue with next backup vault instead of failing entire workflow
			continue
		}
		wf.Logger.Infof("Successfully updated backup vault %s", backupVault.UUID)
	}

	wf.Logger.Infof("Sync Backup ZiZs workflow completed successfully")
	return nil, nil
}

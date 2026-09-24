package backgroundworkflows

import (
	"fmt"

	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ForceVsaCertificateRotationWorkflow queues forced certificate rotation for
// either all certificate-auth pools or the explicitly selected pool UUIDs.
func ForceVsaCertificateRotationWorkflow(
	ctx workflow.Context,
	params commonparams.PoolCertificateMaintenanceParams,
) (workflowErr error) {
	logger := workflow.GetLogger(ctx)
	var gates forcedRotationGates
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		return forcedRotationGates{
			ForcedRotationEnabled: env.GetBool("ENABLE_FORCED_CERTIFICATE_ROTATION", false),
			RotationEnabled:       env.GetBool("ENABLE_VSA_CERTIFICATE_ROTATION", false),
		}
	}).Get(&gates); err != nil {
		return err
	}
	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	})
	if params.JobID != "" {
		if err := updateCertificateMaintenanceJob(ctx, params.JobID, datamodel.JobsStatePROCESSING, nil); err != nil {
			return fmt.Errorf("mark forced certificate rotation job processing: %w", err)
		}
		defer func() {
			workflowErr = finishCertificateMaintenanceJob(ctx, params.JobID, workflowErr)
		}()
	}
	if !gates.ForcedRotationEnabled {
		return temporal.NewNonRetryableApplicationError(
			"forced certificate rotation is disabled (ENABLE_FORCED_CERTIFICATE_ROTATION=false)",
			"ForcedRotationDisabled",
			nil,
		)
	}
	if !gates.RotationEnabled {
		return temporal.NewNonRetryableApplicationError(
			"certificate rotation is disabled (ENABLE_VSA_CERTIFICATE_ROTATION=false)",
			"CertificateRotationDisabled",
			nil,
		)
	}

	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	transitionWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	if len(params.PoolUUIDs) > 0 {
		return queueSelectedForcedCertificateRotations(
			ctx,
			controlActivity,
			params.PoolUUIDs,
			transitionWorkflowID,
			params.JobID,
		)
	}

	batchSize := CertificateRotationBatchSize
	if batchSize <= 0 {
		batchSize = vsaCAReconciliationBatchSize
	}
	offset := 0
	for {
		var batch *backgroundactivities.ListPoolsBatchResult
		if err := workflow.ExecuteActivity(
			ctx,
			rotationActivity.ListPoolsWithCertificateAuth,
			offset,
			batchSize,
		).Get(ctx, &batch); err != nil {
			return fmt.Errorf("list certificate-auth pools for forced rotation: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("list certificate-auth pools for forced rotation returned nil")
		}
		poolUUIDs := make([]string, 0, len(batch.Pools))
		for _, pool := range batch.Pools {
			if pool != nil {
				poolUUIDs = append(poolUUIDs, pool.UUID)
			}
		}
		if err := queueSelectedForcedCertificateRotations(
			ctx,
			controlActivity,
			poolUUIDs,
			transitionWorkflowID,
			params.JobID,
		); err != nil {
			return err
		}
		if !batch.HasMore {
			logger.Info("Forced certificate rotation queued", "selectionType", params.SelectionType)
			return nil
		}
		offset += batchSize
	}
}

func queueSelectedForcedCertificateRotations(
	ctx workflow.Context,
	controlActivity *backgroundactivities.ControlWorkflowActivity,
	poolUUIDs []string,
	transitionWorkflowID string,
	parentJobID string,
) error {
	futures := make([]workflow.Future, 0, len(poolUUIDs))
	childJobIDs := make([]string, 0, len(poolUUIDs))
	for _, poolUUID := range poolUUIDs {
		if parentJobID == "" {
			futures = append(futures, workflow.ExecuteActivity(
				ctx,
				controlActivity.ExecutePoolForcedCertificateRotationSequentially,
				poolUUID,
				CertificateRotationChildWorkflowTimeout,
			))
			continue
		}
		childWorkflowID := fmt.Sprintf("PoolForcedCertRotation_%s_%s", poolUUID, transitionWorkflowID)
		childJobID, err := createCertificateMaintenanceChildJob(
			ctx,
			parentJobID,
			poolUUID,
			childWorkflowID,
			datamodel.JobTypeForcePoolCertificateRotation,
			coremodels.CertificateMaintenanceOperationForcedRotation,
		)
		if err != nil {
			return err
		}
		childJobIDs = append(childJobIDs, childJobID)
		futures = append(futures, workflow.ExecuteActivity(
			ctx,
			controlActivity.ExecuteTrackedPoolForcedCertificateRotationSequentially,
			poolUUID,
			transitionWorkflowID,
			childJobID,
			CertificateRotationChildWorkflowTimeout,
		))
	}
	for _, future := range futures {
		if err := future.Get(ctx, nil); err != nil {
			return fmt.Errorf("queue forced certificate rotation: %w", err)
		}
	}
	return waitForCertificateMaintenanceJobs(ctx, childJobIDs, certificateMaintenanceTrackingTimeout)
}

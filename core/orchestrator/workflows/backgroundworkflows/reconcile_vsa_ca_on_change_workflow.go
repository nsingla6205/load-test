package backgroundworkflows

import (
	"fmt"
	"time"

	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	vsaCAReconciliationBatchSize       = 10
	vsaCAReconciliationWorkflowTimeout = 10 * time.Minute
)

// ReconcileVsaCAOnChangeWorkflow scans certificate-auth pools against an immutable desired CA.
func ReconcileVsaCAOnChangeWorkflow(
	ctx workflow.Context,
	params commonparams.PoolCertificateMaintenanceParams,
) (workflowErr error) {
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
			return fmt.Errorf("mark CA reconciliation job processing: %w", err)
		}
		defer func() {
			workflowErr = finishCertificateMaintenanceJob(ctx, params.JobID, workflowErr)
		}()
	}

	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	controlActivity := &backgroundactivities.ControlWorkflowActivity{}
	transitionWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	if len(params.PoolUUIDs) > 0 {
		return queueSelectedPoolCAReconciliations(
			ctx,
			controlActivity,
			params.PoolUUIDs,
			params.DesiredCAURI,
			transitionWorkflowID,
			params.JobID,
		)
	}

	offset := 0
	for {
		var batch *backgroundactivities.ListPoolsBatchResult
		if err := workflow.ExecuteActivity(
			ctx,
			rotationActivity.ListPoolsWithCertificateAuth,
			offset,
			vsaCAReconciliationBatchSize,
		).Get(ctx, &batch); err != nil {
			return fmt.Errorf("list certificate-auth pools for CA reconciliation: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("list certificate-auth pools for CA reconciliation returned nil")
		}

		futures := make([]workflow.Future, 0, len(batch.Pools))
		childJobIDs := make([]string, 0, len(batch.Pools))
		for _, pool := range batch.Pools {
			if pool == nil {
				continue
			}
			if params.JobID == "" {
				futures = append(futures, workflow.ExecuteActivity(
					ctx,
					controlActivity.ExecutePoolCAReconciliationSequentially,
					pool.UUID,
					params.DesiredCAURI,
					transitionWorkflowID,
					vsaCAReconciliationWorkflowTimeout,
				))
				continue
			}
			childWorkflowID := fmt.Sprintf("PoolCAReconciliation_%s_%s", pool.UUID, transitionWorkflowID)
			childJobID, err := createCertificateMaintenanceChildJob(
				ctx,
				params.JobID,
				pool.UUID,
				childWorkflowID,
				datamodel.JobTypeReconcilePoolCertificateAuthority,
				coremodels.CertificateMaintenanceOperationCAReconciliation,
			)
			if err != nil {
				return err
			}
			childJobIDs = append(childJobIDs, childJobID)
			futures = append(futures, workflow.ExecuteActivity(
				ctx,
				controlActivity.ExecuteTrackedPoolCAReconciliationSequentially,
				pool.UUID,
				params.DesiredCAURI,
				transitionWorkflowID,
				childJobID,
				vsaCAReconciliationWorkflowTimeout,
			))
		}
		for _, future := range futures {
			if err := future.Get(ctx, nil); err != nil {
				return fmt.Errorf("queue pool CA reconciliation: %w", err)
			}
		}
		if err := waitForCertificateMaintenanceJobs(ctx, childJobIDs, certificateMaintenanceTrackingTimeout); err != nil {
			return err
		}
		if !batch.HasMore {
			return nil
		}
		offset += vsaCAReconciliationBatchSize
	}
}

func queueSelectedPoolCAReconciliations(
	ctx workflow.Context,
	controlActivity *backgroundactivities.ControlWorkflowActivity,
	poolUUIDs []string,
	desiredCAURI string,
	transitionWorkflowID string,
	parentJobID string,
) error {
	futures := make([]workflow.Future, 0, len(poolUUIDs))
	childJobIDs := make([]string, 0, len(poolUUIDs))
	for _, poolUUID := range poolUUIDs {
		if parentJobID == "" {
			futures = append(futures, workflow.ExecuteActivity(
				ctx,
				controlActivity.ExecutePoolCAReconciliationSequentially,
				poolUUID,
				desiredCAURI,
				transitionWorkflowID,
				vsaCAReconciliationWorkflowTimeout,
			))
			continue
		}
		childWorkflowID := fmt.Sprintf("PoolCAReconciliation_%s_%s", poolUUID, transitionWorkflowID)
		childJobID, err := createCertificateMaintenanceChildJob(
			ctx,
			parentJobID,
			poolUUID,
			childWorkflowID,
			datamodel.JobTypeReconcilePoolCertificateAuthority,
			coremodels.CertificateMaintenanceOperationCAReconciliation,
		)
		if err != nil {
			return err
		}
		childJobIDs = append(childJobIDs, childJobID)
		futures = append(futures, workflow.ExecuteActivity(
			ctx,
			controlActivity.ExecuteTrackedPoolCAReconciliationSequentially,
			poolUUID,
			desiredCAURI,
			transitionWorkflowID,
			childJobID,
			vsaCAReconciliationWorkflowTimeout,
		))
	}
	for _, future := range futures {
		if err := future.Get(ctx, nil); err != nil {
			return fmt.Errorf("queue pool CA reconciliation: %w", err)
		}
	}
	return waitForCertificateMaintenanceJobs(ctx, childJobIDs, certificateMaintenanceTrackingTimeout)
}

// ReconcilePoolCAWorkflow reconciles one pool against the desired CA carried in history.
func ReconcilePoolCAWorkflow(
	ctx workflow.Context,
	poolUUID, desiredCAURI string,
) error {
	return reconcilePoolCA(ctx, poolUUID, desiredCAURI, "")
}

// TrackedReconcilePoolCAWorkflow reconciles one pool and updates its child job.
func TrackedReconcilePoolCAWorkflow(
	ctx workflow.Context,
	poolUUID, desiredCAURI, childJobID string,
) error {
	return reconcilePoolCA(ctx, poolUUID, desiredCAURI, childJobID)
}

func reconcilePoolCA(
	ctx workflow.Context,
	poolUUID, desiredCAURI, childJobID string,
) (workflowErr error) {
	ctx, logger := withPoolOperationLogging(ctx, poolUUID, poolOperationCAReconciliation)
	logger.Info("Pool CA reconciliation started", "desiredCAURI", desiredCAURI)
	defer func() {
		if workflowErr != nil {
			logger.Error("Pool CA reconciliation finished", "outcome", "failure", "error", workflowErr)
			return
		}
		logger.Info("Pool CA reconciliation finished", "outcome", "success")
	}()

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
	if childJobID != "" {
		if err := updateCertificateMaintenanceJob(ctx, childJobID, datamodel.JobsStatePROCESSING, nil); err != nil {
			return fmt.Errorf("mark pool CA reconciliation job processing: %w", err)
		}
		defer func() {
			workflowErr = finishCertificateMaintenanceJob(ctx, childJobID, workflowErr)
		}()
	}

	rotationActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	var needsRotation bool
	if err := workflow.ExecuteActivity(
		ctx,
		rotationActivity.CertificateNeedsRotationForDesiredCA,
		poolUUID,
		desiredCAURI,
	).Get(ctx, &needsRotation); err != nil {
		return err
	}
	if !needsRotation {
		logger.Info("Pool CA reconciliation completed without rotation", "reason", "certificate_already_matches_desired_ca")
		return nil
	}

	var poolContext *backgroundactivities.PoolContext
	if err := workflow.ExecuteActivity(
		ctx,
		rotationActivity.GetPoolContextForDesiredCA,
		poolUUID,
		desiredCAURI,
	).Get(ctx, &poolContext); err != nil {
		return err
	}
	logger.Info("Promoting pool certificate to desired CA", "desiredCAURI", desiredCAURI)
	return workflow.ExecuteActivity(
		ctx,
		rotationActivity.RotatePoolCertificateForDesiredCAWithContext,
		poolContext,
	).Get(ctx, nil)
}

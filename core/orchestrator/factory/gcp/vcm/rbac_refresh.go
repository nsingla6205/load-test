package vcm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	cbackend "github.com/cschleiden/go-workflows/backend"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// UpdateRbacForPoolById persists a durable ONTAP-mode RBAC refresh job (in NEW),
// starts the VCM RBAC refresh workflow, and returns its operation ID. The
// request ID is the durable job / operation ID; the workflow instance is keyed
// on the deployment ID.
//
// Refreshes are serialized per deployment: an ONTAP-mode RBAC refresh deletes
// and recreates the whole role on the cluster, so two concurrent refreshes for
// the same deployment would race destructively. A new request made while a
// refresh is already in flight for the same deployment is rejected with a
// conflict error; in that case the returned string carries the operation ID of
// the refresh already in progress so the caller can report it. A retry carrying
// the same request ID is idempotent and returns the existing operation.
func (o *VCMOrchestrator) UpdateRbacForPoolById(ctx context.Context, params *commonparams.RefreshRbacForPoolParams) (string, error) {
	if params == nil || params.VCM == nil || params.VCM.DeploymentID == "" {
		return "", fmt.Errorf("UpdateRbacForPoolById: VCM deployment id missing from request")
	}
	if params.VCM.RequestID == "" {
		return "", fmt.Errorf("UpdateRbacForPoolById: VCM request id missing from request")
	}

	deploymentID := params.VCM.DeploymentID
	operationID := params.VCM.RequestID
	ctx = orchestrator.WithDeploymentID(ctx, deploymentID)
	logger := util.GetLogger(ctx)

	// Serialize refreshes per deployment. Reject a new request while another
	// refresh is still in flight for the same deployment; a retry of the same
	// request id falls through to the idempotent create-or-get below.
	inFlight, err := o.store.GetOngoingJobByResource(ctx, deploymentID, vcmdb.JobTypeOntapModeRbacRefresh)
	if err != nil {
		return "", fmt.Errorf("UpdateRbacForPoolById: failed to check in-flight RBAC refresh for deployment %q: %w", deploymentID, err)
	}
	if inFlight != nil && inFlight.ID != operationID {
		logger.Info("UpdateRbacForPoolById: RBAC refresh already in progress for deployment; rejecting new request",
			"jobId", operationID, "existingJobId", inFlight.ID, "deploymentId", deploymentID, "state", inFlight.State)
		// Return the conflicting operation id so the caller can report which
		// refresh is already running.
		return inFlight.ID, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("RBAC refresh is already in progress for deployment %q (operation %s, %s), please wait for it to complete", deploymentID, inFlight.ID, inFlight.State))
	}

	params.VCM.Derived = params.VCM.DeriveInputs()

	requestPayload, err := json.Marshal(params.VCM)
	if err != nil {
		return "", fmt.Errorf("UpdateRbacForPoolById: failed to marshal request payload for job %q: %w", operationID, err)
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, &vcmdb.Job{
		ID:             operationID,
		ResourceID:     deploymentID,
		Type:           vcmdb.JobTypeOntapModeRbacRefresh,
		State:          vcmdb.JobStateNew,
		RequestPayload: string(requestPayload),
		DeadlineAt:     params.VCM.Deadline.UTC(),
	})
	if err != nil {
		return "", fmt.Errorf("UpdateRbacForPoolById: RBAC refresh job %q: %w", operationID, err)
	}
	if alreadyExists {
		logger.Info("UpdateRbacForPoolById: RBAC refresh job already exists; returning existing operation",
			"jobId", operationID, "deploymentId", deploymentID)
		return operationID, nil
	}

	// Key the workflow instance on the deployment id so the engine atomically
	// rejects a second concurrent refresh for the same cluster, even if two
	// requests pass the in-flight check above at the same time.
	if _, err := o.wfStarter.Start(ctx, deploymentID, vcm_workflows.OntapModeRbacRefreshWorkflow, params); err != nil {
		if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
			logger.Info("UpdateRbacForPoolById: RBAC refresh already running for deployment; rejecting concurrent request",
				"jobId", operationID, "deploymentId", deploymentID)
			if uerr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
				JobID:        operationID,
				State:        vcmdb.JobStateError,
				Step:         jobStepFailedToStart,
				ErrorMessage: "RBAC refresh is already in progress for this deployment",
				Retriable:    false,
			}); uerr != nil {
				logger.Error("UpdateRbacForPoolById: failed to mark job ERROR after concurrent-refresh rejection",
					"jobId", operationID, "deploymentId", deploymentID, "error", uerr)
			}
			// Our job is now ERROR, so look up the refresh that actually won the
			// race to report its operation id back to the caller.
			conflictingID := ""
			if running, qerr := o.store.GetOngoingJobByResource(ctx, deploymentID, vcmdb.JobTypeOntapModeRbacRefresh); qerr == nil && running != nil {
				conflictingID = running.ID
			}
			return conflictingID, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("RBAC refresh is already in progress for deployment %q (operation %s), please wait for it to complete", deploymentID, conflictingID))
		}
		if uerr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); uerr != nil {
			logger.Error("UpdateRbacForPoolById: failed to mark job ERROR after dispatch failure",
				"jobId", operationID, "deploymentId", deploymentID, "error", uerr)
		}
		return "", fmt.Errorf("UpdateRbacForPoolById: failed to start RBAC refresh workflow for deployment %q: %w", deploymentID, err)
	}

	return operationID, nil
}

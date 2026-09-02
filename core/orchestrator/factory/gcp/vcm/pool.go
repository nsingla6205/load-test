package vcm

import (
	"context"
	"errors"
	"fmt"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// CreatePool persists a durable pool-create job (in NEW), starts the VCM pool
// workflow, and returns its operation ID. VCM is asynchronous, so no pool model
// is returned; the caller polls the operation for progress. The request ID is
// the durable job / operation ID and is also used as the workflow instance ID,
// so the workflow always drives exactly the job it belongs to. The deployment ID
// is the resource ID recorded on the job.
func (o *VCMOrchestrator) CreatePool(ctx context.Context, params *commonparams.CreatePoolParams) (*models.Pool, string, error) {
	if params == nil || params.VCM == nil || params.VCM.DeploymentID == "" {
		return nil, "", fmt.Errorf("CreatePool: VCM deployment id missing from request")
	}
	if params.VCM.RequestID == "" {
		return nil, "", fmt.Errorf("CreatePool: VCM request id missing from request")
	}

	deploymentID := params.VCM.DeploymentID
	operationID := params.VCM.RequestID

	// Derive the workflow inputs before dispatch so they stay stable across replays.
	params.VCM.Derived = params.VCM.DeriveInputs()

	// Create the durable job in NEW before starting the workflow.
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: deploymentID,
		Type:       vcmdb.JobTypePoolCreate,
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.VCM.Deadline.UTC(),
	}
	if err := o.store.CreateJob(ctx, job); err != nil {
		if !errors.Is(err, vcmdb.ErrJobAlreadyExists) {
			return nil, "", fmt.Errorf("CreatePool: failed to persist pool creation job %q: %w", operationID, err)
		}
		existing, gerr := o.store.GetJob(ctx, operationID)
		if gerr != nil {
			return nil, "", fmt.Errorf("CreatePool: pool creation job %q already exists but could not be read: %w", operationID, gerr)
		}
		util.GetLogger(ctx).Info("CreatePool: duplicate create-pool request rejected; job already exists",
			"jobId", operationID, "deploymentId", deploymentID, "state", existing.State)
		return nil, "", vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("create-pool request %q already exists in state %s", operationID, existing.State))
	}

	if _, err := o.wfStarter.Start(ctx, operationID, vcm_workflows.PoolWorkflow, params); err != nil {
		if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
			util.GetLogger(ctx).Info("CreatePool: pool workflow already running; reattaching to in-flight run",
				"jobId", operationID, "deploymentId", deploymentID)
			return nil, operationID, nil
		}
		// The workflow failed to start; mark the job ERROR so it is not stranded
		// in NEW. This only runs on a dispatch failure, so it lives inline rather
		// than in a defer.
		if uerr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); uerr != nil {
			util.GetLogger(ctx).Error("CreatePool: failed to mark job ERROR after dispatch failure",
				"jobId", operationID, "deploymentId", deploymentID, "error", uerr)
		}
		return nil, "", fmt.Errorf("CreatePool: failed to start pool workflow for deployment %q: %w", deploymentID, err)
	}

	return nil, operationID, nil
}

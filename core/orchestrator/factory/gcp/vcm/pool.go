package vcm

import (
	"context"
	"encoding/json"
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

	// Persist the request payload
	requestPayload, err := json.Marshal(params.VCM)
	if err != nil {
		return nil, "", fmt.Errorf("CreatePool: failed to marshal request payload for job %q: %w", operationID, err)
	}
	// Create the durable job in NEW before starting the workflow.
	job := &vcmdb.Job{
		ID:             operationID,
		ResourceID:     deploymentID,
		Type:           vcmdb.JobTypePoolCreate,
		State:          vcmdb.JobStateNew,
		RequestPayload: string(requestPayload),
		DeadlineAt:     params.VCM.Deadline.UTC(),
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

// UpdatePool persists a durable pool-update job (in NEW), starts the VCM pool
// update workflow, and returns its operation ID. VCM is asynchronous, so no
// pool model is returned; the caller polls the operation for progress. The
// request ID is the durable job / operation ID and is also used as the workflow
// instance ID, so the workflow always drives exactly the job it belongs to. The
// deployment ID is the resource ID recorded on the job. A second request for
// the same deployment is rejected while a POOL_UPDATE job is still NEW or
// PROCESSING, so two VLM updates cannot run on one cluster.
func (o *VCMOrchestrator) UpdatePool(ctx context.Context, params *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	if params == nil || params.VCM == nil || params.VCM.DeploymentID == "" {
		return nil, "", fmt.Errorf("UpdatePool: VCM deployment id missing from request")
	}
	if params.VCM.RequestID == "" {
		return nil, "", fmt.Errorf("UpdatePool: VCM request id missing from request")
	}

	deploymentID := params.VCM.DeploymentID
	operationID := params.VCM.RequestID

	inFlight, err := o.store.GetOngoingJobByResource(ctx, deploymentID, vcmdb.JobTypePoolUpdate)
	if err != nil {
		return nil, "", fmt.Errorf("UpdatePool: failed to check in-flight pool update for deployment %q: %w", deploymentID, err)
	}
	if inFlight != nil {
		util.GetLogger(ctx).Info("UpdatePool: overlapping update-pool request rejected; deployment already has an in-flight update",
			"jobId", operationID, "existingJobId", inFlight.ID, "deploymentId", deploymentID, "state", inFlight.State)
		return nil, "", vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("update-pool for deployment %q is already %s (job %s)", deploymentID, inFlight.State, inFlight.ID))
	}

	// Derive the workflow inputs before dispatch so they stay stable across replays.
	params.VCM.Derived = params.VCM.DeriveInputs()

	requestPayload, err := json.Marshal(params.VCM)
	if err != nil {
		return nil, "", fmt.Errorf("UpdatePool: failed to marshal request payload for job %q: %w", operationID, err)
	}
	job := &vcmdb.Job{
		ID:             operationID,
		ResourceID:     deploymentID,
		Type:           vcmdb.JobTypePoolUpdate,
		State:          vcmdb.JobStateNew,
		RequestPayload: string(requestPayload),
		DeadlineAt:     params.VCM.Deadline.UTC(),
	}
	if err := o.store.CreateJob(ctx, job); err != nil {
		if !errors.Is(err, vcmdb.ErrJobAlreadyExists) {
			return nil, "", fmt.Errorf("UpdatePool: failed to persist pool update job %q: %w", operationID, err)
		}
		existing, gerr := o.store.GetJob(ctx, operationID)
		if gerr != nil {
			return nil, "", fmt.Errorf("UpdatePool: pool update job %q already exists but could not be read: %w", operationID, gerr)
		}
		util.GetLogger(ctx).Info("UpdatePool: duplicate update-pool request rejected; job already exists",
			"jobId", operationID, "deploymentId", deploymentID, "state", existing.State)
		return nil, "", vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("update-pool request %q already exists in state %s", operationID, existing.State))
	}

	if _, err := o.wfStarter.Start(ctx, operationID, vcm_workflows.PoolUpdateWorkflow, params); err != nil {
		if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
			util.GetLogger(ctx).Info("UpdatePool: pool update workflow already running; reattaching to in-flight run",
				"jobId", operationID, "deploymentId", deploymentID)
			return nil, operationID, nil
		}
		if uerr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); uerr != nil {
			util.GetLogger(ctx).Error("UpdatePool: failed to mark job ERROR after dispatch failure",
				"jobId", operationID, "deploymentId", deploymentID, "error", uerr)
		}
		return nil, "", fmt.Errorf("UpdatePool: failed to start pool update workflow for deployment %q: %w", deploymentID, err)
	}

	return nil, operationID, nil
}

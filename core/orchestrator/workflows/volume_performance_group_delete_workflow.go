package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DeleteVolumePerformanceGroupWorkflowParams struct {
	VPG         *datamodel.VolumePerformanceGroup
	AccountName string
}

type vpgDeleteWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &vpgDeleteWorkflow{}

// DeleteVolumePerformanceGroupWorkflow deletes a VPG's QoS policy from ONTAP and hard-deletes the row from the database.
func DeleteVolumePerformanceGroupWorkflow(ctx workflow.Context, params *DeleteVolumePerformanceGroupWorkflowParams) error {
	log := workflow.GetLogger(ctx)
	wf := new(vpgDeleteWorkflow)
	if err := wf.Setup(ctx, params); err != nil {
		log.Error("VPG delete workflow setup error", "error", err)
		return err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}
	wf.Status = WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		log.Error("Failed to update job status to Processing", "error", err)
		return err
	}

	_, customErr := wf.Run(ctx, params)
	if customErr != nil {
		log.Error("DeleteVolumePerformanceGroupWorkflow failed", "error", customErr)
		wf.Status = WorkflowStatusFailed
		_ = wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return customErr
	}

	wf.Status = WorkflowStatusCompleted
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil); err != nil {
		log.Error("Failed to update job status to Done", "error", err)
	}
	return nil
}

func (wf *vpgDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params := input.(*DeleteVolumePerformanceGroupWorkflowParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *vpgDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*DeleteVolumePerformanceGroupWorkflowParams)
	vpg := params.VPG

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	vpgActivity := &activities.VolumePerformanceGroupActivity{}
	commonActivity := &activities.CommonActivities{}

	var pool *datamodel.PoolView
	if err := executeActivity(ctx, vpgActivity.GetPoolViewByPoolID, vpg.PoolID).Get(ctx, &pool); err != nil {
		return nil, ConvertToVSAError(err)
	}

	var dbNodes []*datamodel.Node
	if err := executeActivity(ctx, commonActivity.GetNode, vpg.PoolID).Get(ctx, &dbNodes); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if len(dbNodes) == 0 {
		return nil, vsaerrors.ExtractCustomError(errors.NewUserInputValidationErr("no node found for pool"))
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	if err := executeActivity(ctx, vpgActivity.DeleteQoSPolicyInONTAP, vpg.OntapQosPolicyID, vpg.Name, vpg.PoolID, node).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	if err := executeActivity(ctx, vpgActivity.HardDeleteVPGInDB, vpg).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

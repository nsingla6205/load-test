package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type vpgUpdateWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &vpgUpdateWorkflow{}

// UpdateVolumePerformanceGroupWorkflow updates a VPG's name, throughput, and IOPS via the VPG endpoint (ONTAP QoS policy + DB).
func UpdateVolumePerformanceGroupWorkflow(ctx workflow.Context, params *common.UpdateVolumePerformanceGroupParams, vpg *datamodel.VolumePerformanceGroup) error {
	log := workflow.GetLogger(ctx)
	wf := new(vpgUpdateWorkflow)
	if err := wf.Setup(ctx, params); err != nil {
		log.Error("VPG update workflow setup error", "error", err)
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

	_, customErr := wf.Run(ctx, params, vpg)
	if customErr != nil {
		log.Error("UpdateVolumePerformanceGroupWorkflow failed", "error", customErr)
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

func (wf *vpgUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params := input.(*common.UpdateVolumePerformanceGroupParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *vpgUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.UpdateVolumePerformanceGroupParams)
	vpg := args[1].(*datamodel.VolumePerformanceGroup)

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

	newName := params.Name
	if newName == "" {
		newName = vpg.Name
	}
	newThroughput := nillable.GetInt64(params.ThroughputMibps, vpg.ThroughputMibps)
	newIops := nillable.GetInt64(params.Iops, vpg.Iops)

	if err := executeActivity(ctx, vpgActivity.UpdateQoSPolicyInONTAP, vpg, pool, node, newName, newThroughput, newIops).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	newDescription := vpg.Description
	if params.Description != nil {
		newDescription = *params.Description
	}
	newLabels := vpg.Labels
	if params.Labels != nil {
		newLabels = params.Labels
	}

	updatedVPG := &datamodel.VolumePerformanceGroup{
		BaseModel:        vpg.BaseModel,
		Name:             newName,
		PoolID:           vpg.PoolID,
		AllocationType:   vpg.AllocationType,
		IsAutoGen:        vpg.IsAutoGen,
		ThroughputMibps:  newThroughput,
		Iops:             newIops,
		OntapQosPolicyID: vpg.OntapQosPolicyID,
		Description:      newDescription,
		State:            datamodel.LifeCycleStateREADY,
		Labels:           newLabels,
	}
	if err := executeActivity(ctx, vpgActivity.UpdateVPGInDB, updatedVPG).Get(ctx, nil); err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

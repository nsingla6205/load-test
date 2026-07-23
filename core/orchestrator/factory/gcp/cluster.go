package gcp

import (
	"context"
	"database/sql"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

var (
	acceptClusterPeer = _acceptClusterPeer
)

func (o *GCPOrchestrator) AcceptClusterPeer(ctx context.Context, params *commonparams.ClusterPeerParams, poolID string) (*commonparams.ClusterPeerParams, *datamodel.Job, error) {
	return _acceptClusterPeer(ctx, o.storage, o.temporal, params, poolID)
}

func _acceptClusterPeer(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.ClusterPeerParams, poolID string) (*commonparams.ClusterPeerParams, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, nil, err
	}
	dbPool, err := se.GetPoolByUUIDForAccount(ctx, poolID, account.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, err)
		}
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, customerrors.NewNotFoundErr("Pool", &poolID))
		}
		return nil, nil, err
	}
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeAcceptClusterPeer),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  string(datamodel.JobTypeAcceptClusterPeer),
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
	}

	createdJob, err := se.CreateJob(ctx, job)

	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, nil, err
	}

	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.AcceptClusterPeerWorkflow,
		params,
		dbPool,
	)
	if err != nil {
		logger.Error("Failed to start AcceptClusterPeer Workflow: ", "error", err)
		return nil, nil, err
	}
	return params, job, nil
}

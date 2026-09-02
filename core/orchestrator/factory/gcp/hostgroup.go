package gcp

import (
	"context"
	"database/sql"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

var (
	createHostGroup      = _createHostGroup
	getHostGroup         = _getHostGroup
	deleteHostGroup      = _deleteHostGroup
	getMultipleHostGroup = _getMultipleHostGroup
	getHostGroupsByUUIDs = _getHostGroupsByUUIDs
)

// GetHostGroup retrieves the specified host group and returns it
func (o *GCPOrchestrator) GetHostGroup(ctx context.Context, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
	return getHostGroup(ctx, o.storage, hostGroupUUID, accountID)
}

func _getHostGroup(ctx context.Context, storage database.Storage, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
	account, err := storage.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	hostGroup, err := storage.GetHostGroup(ctx, hostGroupUUID, account.ID)
	if err != nil {
		return nil, err
	}

	return convertDatastoreHostGroupToModel(hostGroup, account.Name), nil
}

// CreateHostGroup creates the specified host group and adds it to the list of host group belonging to the specified owner
func (o *GCPOrchestrator) CreateHostGroup(ctx context.Context, params *common.CreateHostGroupParams) (*models.HostGroup, error) {
	return createHostGroup(ctx, o.storage, params)
}

func _createHostGroup(ctx context.Context, storage database.Storage, params *common.CreateHostGroupParams) (*models.HostGroup, error) {
	logger := util.GetLogger(ctx)
	account, err := getOrCreateAccount(ctx, storage, params.AccountName)
	if err != nil {
		return nil, err
	}

	if err = persistAccountTrialMetadataIfSet(ctx, storage, account, params.TrialMode); err != nil {
		logger.Error("Failed to update account trial metadata", "accountUUID", account.UUID, "error", err)
		return nil, err
	}

	hostGroup := &datamodel.HostGroup{
		OSType:        params.OSType,
		Name:          params.Name,
		Description:   params.Description,
		HostGroupType: params.HostGroupType,
		Hosts: datamodel.Hosts{
			Hosts: params.Hosts,
		},
		AccountID: account.ID,
	}

	hostGroup, err = storage.CreateHostGroup(ctx, hostGroup)
	if err != nil {
		return nil, err
	}

	return convertDatastoreHostGroupToModel(hostGroup, account.Name), nil
}

func convertDatastoreHostGroupToModel(hostGroup *datamodel.HostGroup, accountName string) *models.HostGroup {
	return &models.HostGroup{
		BaseModel: models.BaseModel{
			UUID:      hostGroup.UUID,
			CreatedAt: hostGroup.CreatedAt,
			UpdatedAt: hostGroup.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(hostGroup.DeletedAt),
		},
		Name:          hostGroup.Name,
		Description:   hostGroup.Description,
		State:         hostGroup.State,
		StateDetails:  hostGroup.StateDetails,
		AccountName:   accountName,
		OSType:        hostGroup.OSType,
		Hosts:         hostGroup.Hosts.Hosts,
		HostGroupType: hostGroup.HostGroupType,
	}
}

// DeleteHostGroup deletes the host group with the specified UUID
func (o *GCPOrchestrator) DeleteHostGroup(ctx context.Context, params *common.DeleteHostGroupParams) (*models.HostGroup, error) {
	return deleteHostGroup(ctx, o.storage, params.Name, params.AccountName)
}

func _deleteHostGroup(ctx context.Context, storage database.Storage, hostGroupUUID string, accountID string) (*models.HostGroup, error) {
	account, err := storage.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	hostGroup, err := storage.DeleteHostGroup(ctx, hostGroupUUID, account.ID)
	if err != nil {
		return nil, err
	}

	return convertDatastoreHostGroupToModel(hostGroup, account.Name), nil
}

// GetMultipleHostGroups retrieves the specified host group UUID and returns it
func (o *GCPOrchestrator) GetMultipleHostGroups(ctx context.Context, accountName string, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
	return getMultipleHostGroup(ctx, o.storage, hostGroupUUIDs, accountName)
}

func _getMultipleHostGroup(ctx context.Context, storage database.Storage, hostGroupUUIDs []string, accountID string) ([]*models.HostGroup, error) {
	account, err := storage.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	hostGroups, err := storage.GetMultipleHostGroups(ctx, hostGroupUUIDs, account.ID)
	if err != nil {
		return nil, err
	}

	convHostGroups := make([]*models.HostGroup, 0)
	for _, hg := range hostGroups {
		convHostGroups = append(convHostGroups, convertDatastoreHostGroupToModel(hg, account.Name))
	}

	return convHostGroups, nil
}

func (o *GCPOrchestrator) GetHostGroupsByUUIDs(ctx context.Context, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
	return getHostGroupsByUUIDs(ctx, o.storage, hostGroupUUIDs)
}

func _getHostGroupsByUUIDs(ctx context.Context, storage database.Storage, hostGroupUUIDs []string) ([]*models.HostGroup, error) {
	hostGroups, err := storage.GetHostGroupsByUUIDs(ctx, hostGroupUUIDs)
	if err != nil {
		return nil, err
	}

	result := make([]*models.HostGroup, 0, len(hostGroups))
	for _, hg := range hostGroups {
		accountName := ""
		if hg.Account != nil {
			accountName = hg.Account.Name
		}
		result = append(result, convertDatastoreHostGroupToModel(hg, accountName))
	}

	return result, nil
}

func (o *GCPOrchestrator) UpdateHostGroup(ctx context.Context, params *common.UpdateHostGroupParams) (*models.HostGroup, string, error) {
	logger := util.GetLogger(ctx)
	account, err := o.storage.GetAccount(ctx, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	hg, err := o.storage.UpdateHostGroup(ctx, params.HostGroupUUID, account.ID, params.Description, params.Hosts)
	if err != nil {
		return nil, "", err
	}

	// Capture the pre-update lifecycle state so the workflow supervisor can revert
	// the host group if the update job stalls before the workflow runs.
	previousState := hg.State
	previousStateDetails := hg.StateDetails

	// Move the host group into a transitional UPDATING state. Without a transitional
	// state a stalled or timed-out update would be indistinguishable from a healthy
	// READY host group, leaving nothing for the supervisor to recover.
	if err = o.storage.UpdateHostGroupsState(ctx, []string{hg.UUID}, account.ID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails); err != nil {
		return nil, "", err
	}
	hg.State = datamodel.LifeCycleStateUpdating
	hg.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateHostGroup),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  hg.Name,
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		AccountID:     sql.NullInt64{Int64: hg.Account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         hg.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}
	createdJob, err := o.storage.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create hostgroup update job", "error", err)
		return nil, "", err
	}

	_, err = o.temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    createdJob.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.UpdateHostGroupWorkflow,
		hg,
	)
	if err != nil {
		logger.Error("Failed to start update hostgroup workflow: ", "error", err)
		return nil, "", err
	}

	return convertDatastoreHostGroupToModel(hg, account.Name), createdJob.UUID, nil
}

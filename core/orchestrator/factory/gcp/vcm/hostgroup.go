package vcm

import (
	"context"
	"errors"
	"fmt"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcm_workflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// GetHostGroup reads an ONTAP igroup by name and returns it.
func (o *VCMOrchestrator) GetHostGroup(ctx context.Context, hostGroupName string, _ string) (*models.HostGroup, error) {
	return getHostGroup(ctx, o.store, hostGroupName)
}

func getHostGroup(ctx context.Context, store Store, name string) (*models.HostGroup, error) {
	expectedSvmName, provider, err := ontapProviderFromStore(ctx, store)
	if err != nil {
		return nil, err
	}

	igroup, err := provider.IgroupGet(&name, &expectedSvmName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("HostGroup", &name)
		}
		return nil, fmt.Errorf("GetHostGroup: lookup igroup %q: %w", name, err)
	}
	if igroup == nil {
		return nil, customerrors.NewNotFoundErr("HostGroup", &name)
	}

	return convertOntapIgroupToHostGroup(igroup), nil
}

// GetMultipleHostGroups lists ONTAP igroups. VCM ignores account/uuid filters and lists all igroups on the deployment SVM.
func (o *VCMOrchestrator) GetMultipleHostGroups(ctx context.Context, _ string, _ []string) ([]*models.HostGroup, error) {
	return listHostGroups(ctx, o.store)
}

func listHostGroups(ctx context.Context, store Store) ([]*models.HostGroup, error) {
	expectedSvmName, provider, err := ontapProviderFromStore(ctx, store)
	if err != nil {
		return nil, err
	}

	igroups, err := provider.IgroupList(&expectedSvmName)
	if err != nil {
		return nil, fmt.Errorf("ListHostGroups: list igroups: %w", err)
	}

	return convertOntapIgroupsToHostGroups(igroups), nil
}

func convertOntapIgroupToHostGroup(igroup *ontapRest.Igroup) *models.HostGroup {
	if igroup == nil {
		return nil
	}
	hg := &models.HostGroup{
		Hosts: vsa.GetAllInitiators(igroup.IgroupInlineInitiators),
	}
	if igroup.Name != nil {
		hg.Name = *igroup.Name
	}
	if igroup.OsType != nil {
		hg.OSType = *igroup.OsType
	}
	return hg
}

func convertOntapIgroupsToHostGroups(igroups []*ontapRest.Igroup) []*models.HostGroup {
	out := make([]*models.HostGroup, 0, len(igroups))
	for _, igroup := range igroups {
		if hg := convertOntapIgroupToHostGroup(igroup); hg != nil {
			out = append(out, hg)
		}
	}
	return out
}

// CreateHostGroup persists a durable job and starts the HostGroup create workflow.
func (o *VCMOrchestrator) CreateHostGroup(ctx context.Context, params *commonparams.CreateHostGroupParams) (*models.HostGroup, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, err
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.Name,
		Type:       string(datamodel.JobTypeCreateHostGroup),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, err
	}
	if alreadyExists {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, nil
	}

	hostParams := &commonparams.HostParams{
		HostName: params.Name,
		HostIQNs: params.Hosts,
		OsType:   params.OSType,
	}
	_, err = o.wfStarter.Start(ctx, operationID, vcm_workflows.VcmCreateHostGroupWorkflow, node, hostParams, expectedSvmName)
	if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, nil
	}
	if err != nil {
		if updateErr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); updateErr != nil {
			o.logger.ErrorContext(ctx, "HostGroup create failed to mark job ERROR after workflow start failure", "requestId", operationID, "hostGroupName", params.Name, "workflowError", err, "updateError", updateErr)
		}
		return nil, fmt.Errorf("CreateHostGroup: start workflow: %w", err)
	}

	return &models.HostGroup{
		BaseModel: models.BaseModel{UUID: operationID},
		Name:      params.Name,
	}, nil
}

// DeleteHostGroup persists a durable job and starts the HostGroup delete workflow.
func (o *VCMOrchestrator) DeleteHostGroup(ctx context.Context, params *commonparams.DeleteHostGroupParams) (*models.HostGroup, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, err
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.Name,
		Type:       string(datamodel.JobTypeDeleteHostGroup),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, err
	}
	if alreadyExists {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, nil
	}

	_, err = o.wfStarter.Start(ctx, operationID, vcm_workflows.VcmDeleteHostGroupWorkflow, node, params.Name, expectedSvmName)
	if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, nil
	}
	if err != nil {
		if updateErr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); updateErr != nil {
			o.logger.ErrorContext(ctx, "HostGroup delete failed to mark job ERROR after workflow start failure", "requestId", operationID, "hostGroupName", params.Name, "workflowError", err, "updateError", updateErr)
		}
		return nil, fmt.Errorf("DeleteHostGroup: start workflow: %w", err)
	}

	return &models.HostGroup{
		BaseModel: models.BaseModel{UUID: operationID},
		Name:      params.Name,
	}, nil
}

// UpdateHostGroup persists a durable job and starts the HostGroup update workflow.
func (o *VCMOrchestrator) UpdateHostGroup(ctx context.Context, params *commonparams.UpdateHostGroupParams) (*models.HostGroup, string, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, "", err
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.Name,
		Type:       string(datamodel.JobTypeUpdateHostGroup),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, "", err
	}
	if alreadyExists {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, operationID, nil
	}

	_, err = o.wfStarter.Start(ctx, operationID, vcm_workflows.VcmUpdateHostGroupWorkflow, node, params.Name, *params.Hosts, expectedSvmName)
	if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
		return &models.HostGroup{
			BaseModel: models.BaseModel{UUID: operationID},
			Name:      params.Name,
		}, operationID, nil
	}
	if err != nil {
		if updateErr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); updateErr != nil {
			o.logger.ErrorContext(ctx, "HostGroup update failed to mark job ERROR after workflow start failure", "requestId", operationID, "hostGroupName", params.Name, "workflowError", err, "updateError", updateErr)
		}
		return nil, "", fmt.Errorf("UpdateHostGroup: start workflow: %w", err)
	}

	return &models.HostGroup{
		BaseModel: models.BaseModel{UUID: operationID},
		Name:      params.Name,
	}, operationID, nil
}

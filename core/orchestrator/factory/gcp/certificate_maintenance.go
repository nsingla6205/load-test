package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

const (
	// VSACALastAcceptedURIAppConfigKey stores the last fleet-wide CA URI
	// accepted through the Core API.
	VSACALastAcceptedURIAppConfigKey = "vsa_ca_last_accepted_uri"

	caReconciliationWorkflowPrefix = "reconcile-vsa-ca"
	forcedRotationWorkflowPrefix   = "force-vsa-certificate-rotation"
)

// StartVsaCAReconciliation starts CA reconciliation toward the Helm-configured CA.
func (o *GCPOrchestrator) StartVsaCAReconciliation(
	ctx context.Context,
	params *commonparams.PoolCertificateMaintenanceParams,
) (string, string, error) {
	if params == nil {
		return "", "", fmt.Errorf("certificate authority reconciliation parameters are required")
	}
	normalizedCAURI, err := normalizeCAURI(env.BuildCaURI("", "", ""))
	if err != nil {
		return "", "", err
	}
	if o.temporal == nil {
		return "", "", fmt.Errorf("Temporal client is required")
	}
	workflowParams := canonicalCertificateMaintenanceParams(params)
	workflowParams.DesiredCAURI = normalizedCAURI
	workflowID, jobID, err := o.createCertificateMaintenanceJob(
		ctx,
		caReconciliationWorkflowPrefix,
		datamodel.JobTypeReconcilePoolCertificateAuthority,
		coremodels.CertificateMaintenanceOperationCAReconciliation,
		&workflowParams,
	)
	if err != nil {
		return "", "", err
	}
	if err := startCertificateMaintenanceWorkflow(
		ctx,
		o.temporal,
		workflowID,
		backgroundworkflows.ReconcileVsaCAOnChangeWorkflow,
		workflowParams,
	); err != nil {
		_ = o.storage.UpdateJob(ctx, jobID, string(datamodel.JobsStateERROR), 0, err.Error())
		return "", "", err
	}
	if workflowParams.SelectionType == commonparams.PoolCertificateMaintenanceAll {
		if err := o.storage.UpsertAppConfig(ctx, VSACALastAcceptedURIAppConfigKey, normalizedCAURI); err != nil {
			return "", "", fmt.Errorf("persist last accepted VSA CA URI: %w", err)
		}
	}
	return workflowID, jobID, nil
}

// StartForcedVsaCertificateRotation starts on-demand forced certificate rotation.
func (o *GCPOrchestrator) StartForcedVsaCertificateRotation(
	ctx context.Context,
	params *commonparams.PoolCertificateMaintenanceParams,
) (string, string, error) {
	if params == nil {
		return "", "", fmt.Errorf("forced certificate rotation parameters are required")
	}
	if o.temporal == nil {
		return "", "", fmt.Errorf("Temporal client is required")
	}
	workflowParams := canonicalCertificateMaintenanceParams(params)
	workflowID, jobID, err := o.createCertificateMaintenanceJob(
		ctx,
		forcedRotationWorkflowPrefix,
		datamodel.JobTypeForcePoolCertificateRotation,
		coremodels.CertificateMaintenanceOperationForcedRotation,
		&workflowParams,
	)
	if err != nil {
		return "", "", err
	}
	if err := startCertificateMaintenanceWorkflow(
		ctx,
		o.temporal,
		workflowID,
		backgroundworkflows.ForceVsaCertificateRotationWorkflow,
		workflowParams,
	); err != nil {
		_ = o.storage.UpdateJob(ctx, jobID, string(datamodel.JobsStateERROR), 0, err.Error())
		return "", "", err
	}
	return workflowID, jobID, nil
}

func canonicalCertificateMaintenanceParams(
	params *commonparams.PoolCertificateMaintenanceParams,
) commonparams.PoolCertificateMaintenanceParams {
	canonical := *params
	canonical.PoolUUIDs = append([]string(nil), params.PoolUUIDs...)
	sort.Strings(canonical.PoolUUIDs)
	return canonical
}

func (o *GCPOrchestrator) createCertificateMaintenanceJob(
	ctx context.Context,
	workflowPrefix string,
	jobType datamodel.JobType,
	operation coremodels.CertificateMaintenanceOperation,
	params *commonparams.PoolCertificateMaintenanceParams,
) (string, string, error) {
	if o.storage == nil {
		return "", "", fmt.Errorf("database storage is required")
	}
	jobID := utils.RandomUUID()
	workflowID := workflowPrefix + "-" + jobID
	params.JobID = jobID
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: jobID},
		Type:       string(jobType),
		State:      string(datamodel.JobsStateNEW),
		IsAdminJob: true,
		JobAttributes: &datamodel.JobAttributes{
			Operation:     string(operation),
			SelectionType: string(params.SelectionType),
			DesiredCAURI:  params.DesiredCAURI,
			PayloadAttributes: map[string]interface{}{
				"pool_ids": append([]string(nil), params.PoolUUIDs...),
			},
		},
	}
	if _, err := o.storage.CreateJobWithWorkflowID(ctx, job, workflowID); err != nil {
		return "", "", fmt.Errorf("create certificate maintenance job: %w", err)
	}
	return workflowID, jobID, nil
}

// GetCertificateMaintenanceStatus retrieves aggregate and per-pool maintenance progress.
func (o *GCPOrchestrator) GetCertificateMaintenanceStatus(
	ctx context.Context,
	jobID string,
) (*coremodels.CertificateMaintenanceProgress, error) {
	parent, err := o.storage.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if parent.Type != string(datamodel.JobTypeReconcilePoolCertificateAuthority) &&
		parent.Type != string(datamodel.JobTypeForcePoolCertificateRotation) {
		return nil, fmt.Errorf("job %s is not a certificate maintenance job", jobID)
	}
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("job_attributes ->> 'parent_job_id'", "=", jobID),
	)
	children, err := o.storage.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		return nil, fmt.Errorf("list certificate maintenance child jobs: %w", err)
	}
	sort.Slice(children, func(i, j int) bool {
		return children[i].ResourceUUID < children[j].ResourceUUID
	})
	progress := &coremodels.CertificateMaintenanceProgress{
		JobID:      parent.UUID,
		WorkflowID: parent.WorkflowID,
		Status:     parent.State,
		TotalPools: len(children),
		Pools:      make([]coremodels.CertificateMaintenancePoolStatus, 0, len(children)),
	}
	if parent.JobAttributes != nil {
		progress.Operation = coremodels.CertificateMaintenanceOperation(parent.JobAttributes.Operation)
		progress.SelectionType = parent.JobAttributes.SelectionType
		progress.DesiredCAURI = parent.JobAttributes.DesiredCAURI
	}
	for _, child := range children {
		poolStatus := coremodels.CertificateMaintenancePoolStatus{
			PoolID:     child.ResourceUUID,
			JobID:      child.UUID,
			WorkflowID: child.WorkflowID,
			Status:     child.State,
			Error:      child.ErrorDetails,
		}
		progress.Pools = append(progress.Pools, poolStatus)
		switch datamodel.JobState(child.State) {
		case datamodel.JobsStateDONE:
			progress.CompletedPools++
		case datamodel.JobsStateERROR, datamodel.JobsStateCANCELLED:
			progress.FailedPools++
		default:
			progress.InProgressPools++
		}
	}
	return progress, nil
}

func startCertificateMaintenanceWorkflow(
	ctx context.Context,
	temporalClient client.Client,
	workflowID string,
	workflowFn interface{},
	params commonparams.PoolCertificateMaintenanceParams,
) error {
	if temporalClient == nil {
		return fmt.Errorf("Temporal client is required")
	}
	_, err := temporalClient.ExecuteWorkflow(
		ctx,
		client.StartWorkflowOptions{
			ID:                       workflowID,
			TaskQueue:                workflowengine.BackgroundTaskQueue,
			WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
		},
		workflowFn,
		params,
	)
	if err == nil {
		return nil
	}
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &alreadyStarted) {
		return nil
	}
	return fmt.Errorf("start certificate maintenance workflow %s: %w", workflowID, err)
}

func normalizeCAURI(caURI string) (string, error) {
	parts := strings.Split(strings.TrimSpace(caURI), "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("Helm CA URI must use project/CA-pool/CA-name format")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.TrimSpace(part) != part {
			return "", fmt.Errorf("Helm CA URI must use non-empty components without surrounding whitespace")
		}
	}
	return strings.Join(parts, "/"), nil
}

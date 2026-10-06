package gcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

const (
	// These names are what core.generateConfigMapData emits for
	// authTypeReconciliationChildWorkflowTimeoutMinutes and
	// authTypeReconciliationParentWorkflowTimeoutMinutes.
	authTypeReconciliationChildTimeoutEnv      = "AUTH_TYPE_RECONCILIATION_CHILD_WORKFLOW_TIMEOUT_MINUTES"
	authTypeReconciliationParentTimeoutEnv     = "AUTH_TYPE_RECONCILIATION_PARENT_WORKFLOW_TIMEOUT_MINUTES"
	defaultAuthTypeReconciliationBatchSize     = 20
	maxAuthTypeReconciliationBatchSize         = 100
	defaultAuthTypeReconciliationChildTimeout  = 20 * time.Minute
	defaultAuthTypeReconciliationParentTimeout = time.Hour
)

// ReconcilePoolAuthTypes starts one on-demand parent workflow for demoted pools.
func (o *GCPOrchestrator) ReconcilePoolAuthTypes(
	ctx context.Context,
	params *commonparams.ReconcilePoolAuthTypeParams,
) (*models.AuthTypeReconciliationResponse, error) {
	return reconcilePoolAuthTypes(ctx, o.storage, o.temporal, params)
}

// GetPoolAuthTypeReconciliationStatus returns aggregate and per-pool job status.
func (o *GCPOrchestrator) GetPoolAuthTypeReconciliationStatus(
	ctx context.Context,
	jobID string,
) (*models.AuthTypeReconciliationStatus, error) {
	return getPoolAuthTypeReconciliationStatus(ctx, o.storage, jobID)
}

func reconcilePoolAuthTypes(
	ctx context.Context,
	se database.Storage,
	temporalClient client.Client,
	params *commonparams.ReconcilePoolAuthTypeParams,
) (*models.AuthTypeReconciliationResponse, error) {
	if se == nil || temporalClient == nil {
		return nil, customerrors.NewUnavailableErr("auth-type reconciliation dependencies are unavailable")
	}
	if err := validateAuthTypeReconciliationParams(params); err != nil {
		return nil, err
	}
	inFlight, err := findInFlightAuthTypeReconciliationParentJob(ctx, se)
	if err != nil {
		return nil, fmt.Errorf("check in-flight auth-type reconciliation: %w", err)
	}
	if inFlight != nil {
		return nil, customerrors.NewConflictErr(fmt.Sprintf(
			"auth-type reconciliation job %s is already in progress",
			inFlight.UUID,
		))
	}

	batchSize := params.BatchSize
	if batchSize == 0 {
		batchSize = defaultAuthTypeReconciliationBatchSize
	}
	eligiblePools, skipped, err := selectAuthTypeReconciliationPools(ctx, se, params, batchSize)
	if err != nil {
		return nil, err
	}
	if len(eligiblePools) == 0 {
		return nil, customerrors.NewBadRequestErr("no eligible demoted auth_type=1 pools found")
	}

	desiredCAURI, err := helmCAURI()
	if err != nil {
		return nil, err
	}
	results := append([]models.AuthTypeReconciliationPoolResult(nil), skipped...)
	for _, pool := range eligiblePools {
		results = append(results, models.AuthTypeReconciliationPoolResult{
			PoolUUID: pool.UUID,
			Status:   "QUEUED",
		})
	}
	payload := models.AuthTypeReconciliationJobPayload{
		SelectionType: params.SelectionType,
		FromAuthType:  params.FromAuthType,
		ToAuthType:    params.ToAuthType,
		DesiredCAURI:  desiredCAURI,
		BatchSize:     batchSize,
		Results:       results,
	}
	parentJob := &datamodel.Job{
		Type:       string(datamodel.JobTypeAuthTypeReconciliation),
		State:      string(datamodel.JobsStateNEW),
		IsAdminJob: true,
		JobAttributes: &datamodel.JobAttributes{
			PayloadAttributes: payload.Attributes(),
		},
	}
	createdParent, err := se.CreateJob(ctx, parentJob)
	if err != nil {
		return nil, fmt.Errorf("create auth-type reconciliation job: %w", err)
	}

	dispatchCtx := context.WithoutCancel(ctx)
	_, err = temporalClient.ExecuteWorkflow(
		dispatchCtx,
		client.StartWorkflowOptions{
			ID:        createdParent.UUID,
			TaskQueue: workflowengine.CustomerTaskQueue,
			WorkflowRunTimeout: backgroundworkflows.AuthTypeReconciliationWorkflowRunTimeout(
				authTypeReconciliationParentTimeout(),
			),
		},
		commonparams.ReconcilePoolAuthTypesAPIWorkflowName,
		backgroundworkflows.ReconcilePoolAuthTypesAPIWorkflowParams{
			JobID:            createdParent.UUID,
			BatchSize:        batchSize,
			OperationTimeout: authTypeReconciliationChildWorkflowTimeout(),
			ParentTimeout:    authTypeReconciliationParentTimeout(),
			JobPayload:       payload,
		},
	)
	if err != nil {
		_ = se.UpdateJob(
			dispatchCtx,
			createdParent.UUID,
			string(datamodel.JobsStateERROR),
			0,
			err.Error(),
		)
		var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &alreadyStarted) {
			return nil, customerrors.NewConflictErr("an auth-type reconciliation workflow is already running")
		}
		return nil, fmt.Errorf("start auth-type reconciliation workflow: %w", err)
	}

	return &models.AuthTypeReconciliationResponse{
		JobID:        createdParent.UUID,
		Status:       "PROCESSING",
		TotalPools:   len(eligiblePools) + len(skipped),
		QueuedPools:  len(eligiblePools),
		SkippedPools: len(skipped),
		Results:      results,
		CreatedAt:    createdParent.CreatedAt,
		UpdatedAt:    createdParent.UpdatedAt,
	}, nil
}

func validateAuthTypeReconciliationParams(params *commonparams.ReconcilePoolAuthTypeParams) error {
	if params == nil {
		return customerrors.NewBadRequestErr("request body is required")
	}
	if params.FromAuthType != env.USERNAME_PWD_SEC_MGR || params.ToAuthType != env.USER_CERTIFICATE {
		return customerrors.NewBadRequestErr("only fromAuthType 1 toAuthType 2 reconciliation is supported")
	}
	switch models.AuthTypeReconciliationSelectionType(params.SelectionType) {
	case models.AuthTypeReconciliationSelectionAll:
		if len(params.PoolUUIDs) > 0 {
			return customerrors.NewBadRequestErr("poolUuids must be empty when selectionType is all")
		}
	case models.AuthTypeReconciliationSelectionPoolList:
		if len(params.PoolUUIDs) == 0 {
			return customerrors.NewBadRequestErr("poolUuids are required when selectionType is poolList")
		}
		if len(params.PoolUUIDs) > 1000 {
			return customerrors.NewBadRequestErr("poolUuids cannot contain more than 1000 entries")
		}
		seen := make(map[string]struct{}, len(params.PoolUUIDs))
		for index, poolUUID := range params.PoolUUIDs {
			poolUUID = strings.TrimSpace(poolUUID)
			if _, err := uuid.Parse(poolUUID); err != nil {
				return customerrors.NewBadRequestErr(fmt.Sprintf("pool UUID %q is invalid", poolUUID))
			}
			if _, exists := seen[poolUUID]; exists {
				return customerrors.NewBadRequestErr(fmt.Sprintf("pool UUID %q is duplicated", poolUUID))
			}
			seen[poolUUID] = struct{}{}
			params.PoolUUIDs[index] = poolUUID
		}
	default:
		return customerrors.NewBadRequestErr("selectionType must be all or poolList")
	}
	if params.BatchSize < 0 || params.BatchSize > maxAuthTypeReconciliationBatchSize {
		return customerrors.NewBadRequestErr(fmt.Sprintf(
			"batchSize must be between 1 and %d when specified",
			maxAuthTypeReconciliationBatchSize,
		))
	}
	return nil
}

func helmCAURI() (string, error) {
	caURI := strings.TrimSpace(env.BuildCaURI("", "", ""))
	parts := strings.Split(caURI, "/")
	if len(parts) != 3 {
		return "", customerrors.NewBadRequestErr("Helm CA URI must have project/caPool/ca format")
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || strings.TrimSpace(part) != part {
			return "", customerrors.NewBadRequestErr("Helm CA URI components must be non-empty and trimmed")
		}
	}
	return caURI, nil
}

func selectAuthTypeReconciliationPools(
	ctx context.Context,
	se database.Storage,
	params *commonparams.ReconcilePoolAuthTypeParams,
	batchSize int,
) ([]*datamodel.Pool, []models.AuthTypeReconciliationPoolResult, error) {
	eligibleStates := make(map[string]struct{})
	for _, state := range env.GetCertificateRotationPoolStates() {
		eligibleStates[state] = struct{}{}
	}
	if models.AuthTypeReconciliationSelectionType(params.SelectionType) == models.AuthTypeReconciliationSelectionPoolList {
		pools, err := se.GetPoolsByUUIDs(ctx, params.PoolUUIDs)
		if err != nil {
			return nil, nil, fmt.Errorf("retrieve requested pools: %w", err)
		}
		poolsByUUID := make(map[string]*datamodel.Pool, len(pools))
		for _, pool := range pools {
			if pool != nil {
				poolsByUUID[pool.UUID] = pool
			}
		}
		eligible := make([]*datamodel.Pool, 0, len(params.PoolUUIDs))
		skipped := make([]models.AuthTypeReconciliationPoolResult, 0)
		for _, poolUUID := range params.PoolUUIDs {
			pool := poolsByUUID[poolUUID]
			if err := commonparams.AuthTypeReconciliationEligibility(pool, eligibleStates); err != nil {
				skipped = append(skipped, models.AuthTypeReconciliationPoolResult{
					PoolUUID: poolUUID,
					Status:   "SKIPPED",
					Reason:   err.Error(),
				})
				continue
			}
			eligible = append(eligible, pool)
		}
		return eligible, skipped, nil
	}

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition(
			"pool_credentials->>'auth_type'",
			"=",
			fmt.Sprintf("%d", env.USERNAME_PWD_SEC_MGR),
		),
		dbutils.NewFilterCondition("state", "in", env.GetCertificateRotationPoolStates()),
	)
	eligible := make([]*datamodel.Pool, 0)
	for offset := 0; ; offset += batchSize {
		poolViews, err := se.ListPoolsWithFilterAndPaginationOrderedByUUID(
			ctx,
			filter,
			&dbutils.Pagination{Limit: batchSize, Offset: offset},
		)
		if err != nil {
			return nil, nil, fmt.Errorf("list demoted auth_type=1 pools: %w", err)
		}
		for _, poolView := range poolViews {
			pool := poolFromView(poolView)
			if commonparams.AuthTypeReconciliationEligibility(pool, eligibleStates) == nil {
				eligible = append(eligible, pool)
			}
		}
		if len(poolViews) < batchSize {
			break
		}
	}
	return eligible, nil, nil
}

func poolFromView(poolView *datamodel.PoolView) *datamodel.Pool {
	if poolView == nil {
		return nil
	}
	pool := poolView.Pool
	return &pool
}

func findInFlightAuthTypeReconciliationParentJob(
	ctx context.Context,
	se database.Storage,
) (*datamodel.Job, error) {
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("type", "=", string(datamodel.JobTypeAuthTypeReconciliation)),
		dbutils.NewFilterCondition("state", "in", []string{
			string(datamodel.JobsStateNEW),
			string(datamodel.JobsStatePROCESSING),
		}),
	)
	jobs, err := se.GetJobsWithCondition(ctx, *filter)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		switch datamodel.JobState(job.State) {
		case datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING:
			return job, nil
		}
	}
	return nil, nil
}

func authTypeReconciliationChildWorkflowTimeout() time.Duration {
	configuredMinutes := env.GetInt(authTypeReconciliationChildTimeoutEnv, 0)
	if configuredMinutes <= 0 {
		return defaultAuthTypeReconciliationChildTimeout
	}
	return time.Duration(configuredMinutes) * time.Minute
}

func authTypeReconciliationParentTimeout() time.Duration {
	configuredMinutes := env.GetInt(authTypeReconciliationParentTimeoutEnv, 0)
	if configuredMinutes <= 0 {
		return defaultAuthTypeReconciliationParentTimeout
	}
	return time.Duration(configuredMinutes) * time.Minute
}

func getPoolAuthTypeReconciliationStatus(
	ctx context.Context,
	se database.Storage,
	jobID string,
) (*models.AuthTypeReconciliationStatus, error) {
	if _, err := uuid.Parse(jobID); err != nil {
		return nil, customerrors.NewBadRequestErr("jobId must be a valid UUID")
	}
	parent, err := se.GetJob(ctx, jobID)
	if err != nil {
		if !customerrors.IsNotFoundErr(err) {
			return nil, fmt.Errorf("retrieve auth-type reconciliation job %s: %w", jobID, err)
		}
		return nil, customerrors.NewNotFoundErr("auth-type reconciliation job", &jobID)
	}
	if parent.Type != string(datamodel.JobTypeAuthTypeReconciliation) {
		return nil, customerrors.NewNotFoundErr("auth-type reconciliation job", &jobID)
	}
	status := &models.AuthTypeReconciliationStatus{
		JobID:   jobID,
		Status:  authTypeReconciliationAPIStatus(parent.State),
		Results: resultsFromAuthTypeReconciliationJob(parent),
	}
	for _, result := range status.Results {
		switch result.Status {
		case "DONE":
			status.SucceededPools++
		case "ERROR":
			status.FailedPools++
		case "PROCESSING":
			status.ProcessingPools++
		case "QUEUED":
			status.QueuedPools++
		case "SKIPPED":
			status.SkippedPools++
		}
	}
	status.TotalPools = len(status.Results)
	if status.QueuedPools+status.ProcessingPools == 0 && status.TotalPools > 0 {
		switch {
		case status.FailedPools == 0:
			status.Status = "DONE"
		case status.SucceededPools == 0:
			status.Status = "ERROR"
		default:
			status.Status = "PARTIAL"
		}
	}
	sort.Slice(status.Results, func(i, j int) bool {
		return status.Results[i].PoolUUID < status.Results[j].PoolUUID
	})
	return status, nil
}

func authTypeReconciliationAPIStatus(jobState string) string {
	switch datamodel.JobState(jobState) {
	case datamodel.JobsStateDONE:
		return "DONE"
	case datamodel.JobsStateERROR, datamodel.JobsStateCANCELLED:
		return "ERROR"
	default:
		return "PROCESSING"
	}
}

func resultsFromAuthTypeReconciliationJob(job *datamodel.Job) []models.AuthTypeReconciliationPoolResult {
	if job == nil || job.JobAttributes == nil || job.JobAttributes.PayloadAttributes == nil {
		return nil
	}
	raw, ok := job.JobAttributes.PayloadAttributes["results"]
	if !ok {
		return nil
	}
	results := make([]models.AuthTypeReconciliationPoolResult, 0)
	switch entries := raw.(type) {
	case []interface{}:
		for _, entry := range entries {
			values, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			poolUUID, _ := values["poolUuid"].(string)
			resultStatus, _ := values["status"].(string)
			resultJobID, _ := values["jobId"].(string)
			reason, _ := values["reason"].(string)
			results = append(results, models.AuthTypeReconciliationPoolResult{
				PoolUUID: poolUUID,
				Status:   resultStatus,
				JobID:    resultJobID,
				Reason:   reason,
			})
		}
	}
	return results
}

package oci

import (
	"context"
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const clusterUpgradeWorkflowIDMetadataKey = "workflowId"

// UpgradeCluster validates pool state and dispatches an async Temporal upgrade workflow.
func (o *OCIOrchestrator) UpgradeCluster(ctx context.Context, params *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error) {
	return upgradeCluster(ctx, o.storage, o.temporal, params)
}

// upgradeCluster resolves pool by OCID, validates state, and starts the upgrade workflow.
func upgradeCluster(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error) {
	logger := util.GetLogger(ctx)

	pool, err := getPoolByOCID(ctx, se, params.PoolOCID, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	clusterID := pool.UUID

	if pool.State != datamodel.LifeCycleStateREADY && pool.State != datamodel.LifeCycleStateDisabled {
		logger.Warn("Cluster is not in a valid state for upgrade", "clusterId", clusterID, "currentState", pool.State)
		return nil, "", customerrors.NewBadRequestErr("Cluster must be in READY or DISABLED state for upgrade. Current state: " + pool.State)
	}

	currentVersion := env.CurrentOntapVersionDetails
	if pool.BuildInfo != nil && pool.BuildInfo.OntapVersion != "" {
		currentVersion = pool.BuildInfo.OntapVersion
	}

	activeJob, err := common.CheckActiveUpgradeJob(ctx, se, clusterID)
	if err != nil {
		logger.Error("Failed to check for active upgrade jobs", "clusterId", clusterID, "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to check for active upgrade jobs")
	}
	if activeJob != nil {
		activeWorkflowID := clusterUpgradeWorkflowIDFromJob(activeJob)
		logger.Warn("Cluster already has an active upgrade job",
			"clusterId", clusterID,
			"activeJobId", activeJob.UUID,
			"activeWorkflowID", activeWorkflowID,
			"activeStatus", activeJob.Status)
		return nil, "", customerrors.NewConflictErr("Cluster already has an active upgrade job (workflowID=" + activeWorkflowID + ")")
	}

	mediatorImageOcid := params.MediatorBuildImage
	if mediatorImageOcid == "" {
		mediatorImageOcid = ociworkflows.GetVSAMediatorImageName()
		if mediatorImageOcid != "" {
			logger.Debug("mediatorImageOcid not provided in request, using VSA_MEDIATOR_IMAGE_NAME env",
				"mediatorImageOcid", mediatorImageOcid)
		} else {
			logger.Info("mediatorImageOcid not provided in request and VSA_MEDIATOR_IMAGE_NAME env is not set; mediator upgrade will be skipped")
		}
	}

	jobUUID := utils.RandomUUID()
	workflowID := resolveWorkflowID(params.WorkflowID)
	upgradeJob, err := createUpgradeJobInDB(ctx, se, params, pool, params.TargetOntapVersion, jobUUID, params.VSAImagePath, workflowID, mediatorImageOcid)
	if err != nil {
		logger.Error("Failed to create upgrade job in database", "clusterId", clusterID, "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to create upgrade job")
	}
	workflowParams := &ociworkflows.OCIClusterUpgradeWorkflowParams{
		JobID:             jobUUID,
		ClusterID:         clusterID,
		AccountName:       params.AccountName,
		Pool:              pool,
		TargetVersion:     params.TargetOntapVersion,
		CurrentVersion:    currentVersion,
		VSAImagePath:      params.VSAImagePath,
		MediatorImageOcid: mediatorImageOcid,
		ForceUpgrade:      params.ForceUpgrade,
		SkipUpdateRBAC:    params.SkipUpdateRBAC,
	}

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIClusterUpgradeWorkflow,
		workflowengine.GetClusterUpgradeWorkflowRunTimeout(pool.LargeCapacity),
		workflowParams,
	)
	if err != nil {
		logger.Error("Failed to start upgrade workflow", "clusterId", clusterID, "error", err)
		if updateErr := common.UpdateUpgradeJobStatus(ctx, se, jobUUID, string(models.UpgradeStatusFailed), "Failed to start upgrade workflow: "+err.Error()); updateErr != nil {
			logger.Error("Failed to update job status after workflow start failure", "clusterId", clusterID, "jobUUID", jobUUID, "error", updateErr)
		}
		return nil, "", customerrors.NewUnavailableErr("Failed to start upgrade workflow")
	}

	logger.Info("Started upgrade workflow", "clusterId", clusterID, "workflowID", workflowID,
		"targetVersion", params.TargetOntapVersion, "vsaImagePath", params.VSAImagePath,
		"mediatorImageOcid", mediatorImageOcid)

	return &models.ClusterUpgradeResponse{
		ClusterID: clusterID,
		Status:    models.UpgradeStatusInProgress,
		JobID:     upgradeJob.UUID,
		CreatedAt: upgradeJob.CreatedAt,
		UpdatedAt: upgradeJob.UpdatedAt,
	}, workflowID, nil
}

// getPoolByOCID resolves a pool by its OCID scoped to the given account.
func getPoolByOCID(ctx context.Context, se database.Storage, poolOCID, accountName string) (*datamodel.Pool, error) {
	logger := util.GetLogger(ctx)

	if poolOCID == "" || accountName == "" {
		return nil, customerrors.NewBadRequestErr("PoolOCID and AccountName are required")
	}

	account, err := common.GetAccount(ctx, se, accountName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("account", &accountName)
		}
		logger.Error("Failed to get account", "accountName", accountName, "error", err)
		return nil, err
	}

	conditions := [][]interface{}{
		{"pool_external_identifier = ?", poolOCID},
		{"account_id = ?", account.ID},
	}
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("pool", &poolOCID)
		}
		return nil, err
	}
	return database.ConvertPoolViewToPool(poolView), nil
}

// withClusterUpgradeWorkflowIDMetadata injects the Temporal workflowID into job metadata
// so blocking-error messages can surface it to callers.
func withClusterUpgradeWorkflowIDMetadata(metadata map[string]string, workflowID string) *datamodel.JSONB {
	if workflowID == "" && len(metadata) == 0 {
		return common.ConvertMetadataToJSONB(metadata)
	}
	merged := make(map[string]string, len(metadata)+1)
	for k, v := range metadata {
		merged[k] = v
	}
	if workflowID != "" {
		merged[clusterUpgradeWorkflowIDMetadataKey] = workflowID
	}
	return common.ConvertMetadataToJSONB(merged)
}

// clusterUpgradeWorkflowIDFromJob extracts the workflowID stored in job metadata.
// Returns "" for jobs created before workflowID persistence was added.
func clusterUpgradeWorkflowIDFromJob(job *datamodel.ClusterUpgradeJob) string {
	if job == nil || job.Metadata == nil {
		return ""
	}
	if workflowID, ok := (*job.Metadata)[clusterUpgradeWorkflowIDMetadataKey]; ok {
		if id, ok := workflowID.(string); ok {
			return id
		}
	}
	return ""
}

// createUpgradeJobInDB creates a new ClusterUpgradeJob record in the database with the initial PENDING status.
func createUpgradeJobInDB(ctx context.Context, se database.Storage, params *commonparams.UpgradeClusterParams, pool *datamodel.Pool, targetVersion, jobUUID, vsaImagePath, workflowID, mediatorImageOcid string) (*datamodel.ClusterUpgradeJob, error) {
	currentVersion := "unknown"
	if pool.BuildInfo != nil && pool.BuildInfo.OntapVersion != "" {
		currentVersion = pool.BuildInfo.OntapVersion
	}

	upgradeJob := &datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID: jobUUID,
		},
		ClusterID:          pool.UUID,
		PoolID:             pool.UUID,
		TargetVersion:      targetVersion,
		CurrentVersion:     currentVersion,
		VSABuildImage:      vsaImagePath,
		MediatorBuildImage: mediatorImageOcid,
		Status:             string(models.UpgradeStatusPending),
		Metadata:           withClusterUpgradeWorkflowIDMetadata(params.Metadata, workflowID),
		ForceUpgrade:       params.ForceUpgrade,
	}

	return se.CreateClusterUpgradeJob(ctx, upgradeJob)
}

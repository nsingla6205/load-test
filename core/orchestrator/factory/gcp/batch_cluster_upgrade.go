package gcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
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

const (
	defaultMaxConcurrency   = 5
	defaultFailureThreshold = 0
)

var (
	batchUpgradeClusters = _batchUpgradeClusters
)

// BatchUpgradeClusters initiates a batch upgrade of multiple VSA clusters
func (o *GCPOrchestrator) BatchUpgradeClusters(ctx context.Context, params *commonparams.BatchUpgradeClusterParams) (*models.BatchClusterUpgradeResponse, string, error) {
	return batchUpgradeClusters(ctx, o.storage, o.temporal, params)
}

func _batchUpgradeClusters(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.BatchUpgradeClusterParams) (*models.BatchClusterUpgradeResponse, string, error) {
	logger := util.GetLogger(ctx)

	if err := common.ValidateBatchClusterSelectionParams(params); err != nil {
		return nil, "", err
	}

	if params.MaxConcurrency <= 0 {
		params.MaxConcurrency = defaultMaxConcurrency
	}
	if params.FailureThreshold < 0 {
		params.FailureThreshold = defaultFailureThreshold
	}

	// Determine target build images using same logic as single upgrade
	upgradeParams := &commonparams.UpgradeClusterParams{
		VSABuildImage:      params.VSABuildImage,
		MediatorBuildImage: params.MediatorBuildImage,
		ForceUpgrade:       params.ForceUpgrade,
	}
	targetBuildImages, err := determineTargetBuildImages(ctx, se, upgradeParams)
	if err != nil {
		logger.Error("Failed to determine target build images for batch upgrade", "error", err)
		return nil, "", err
	}

	// Select clusters and apply the same per-cluster prechecks as a single upgrade.
	selectedClusters, err := selectAndValidateClustersForBatchUpgrade(ctx, se, params, targetBuildImages)
	if err != nil {
		logger.Error("Failed to select clusters for batch upgrade", "error", err)
		return nil, "", err
	}

	if len(selectedClusters) == 0 {
		return nil, "", customerrors.NewBadRequestErr("No eligible clusters found matching the selection criteria")
	}

	batchJobUUID := utils.RandomUUID()

	// Build selection data for persistence
	selectionData := make(datamodel.JSONB)
	if params.SelectionType == string(models.SelectionTypePercentage) {
		selectionData["percentage"] = params.Percentage
	} else {
		selectionData["poolIds"] = params.PoolIDs
	}

	batchJob := &datamodel.BatchClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID: batchJobUUID,
		},
		TargetVersion:    targetBuildImages.OntapVersion,
		VSABuildImage:    targetBuildImages.VSAName,
		MediatorBuild:    targetBuildImages.MediatorName,
		Status:           string(models.UpgradeStatusPending),
		TotalClusters:    len(selectedClusters),
		MaxConcurrency:   params.MaxConcurrency,
		FailureThreshold: params.FailureThreshold,
		ForceUpgrade:     params.ForceUpgrade,
		SelectionType:    params.SelectionType,
		SelectionData:    &selectionData,
		Metadata:         common.ConvertMetadataToJSONB(params.Metadata),
	}

	createdBatchJob, err := se.CreateBatchClusterUpgradeJob(ctx, batchJob)
	if err != nil {
		logger.Error("Failed to create batch upgrade job in database", "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to create batch upgrade job")
	}

	// Start the batch upgrade workflow
	workflowOptions := client.StartWorkflowOptions{
		ID:        batchJobUUID,
		TaskQueue: workflowengine.BackgroundTaskQueue,
	}

	workflowParams := &workflows.BatchClusterUpgradeWorkflowParams{
		BatchJobID:       batchJobUUID,
		SelectedClusters: selectedClusters,
		TargetVersion:    targetBuildImages.OntapVersion,
		VSAImagePath:     targetBuildImages.VSAImagePath,
		VSAImageName:     targetBuildImages.VSAName,
		MediatorName:     targetBuildImages.MediatorName,
		ForceUpgrade:     params.ForceUpgrade,
		MaxConcurrency:   params.MaxConcurrency,
		FailureThreshold: params.FailureThreshold,
		Metadata:         params.Metadata,
	}

	_, err = temporal.ExecuteWorkflow(ctx, workflowOptions, workflows.BatchClusterUpgradeWorkflow, workflowParams)
	if err != nil {
		logger.Error("Failed to start batch upgrade workflow", "batchJobId", batchJobUUID, "error", err)
		createdBatchJob.Status = string(models.UpgradeStatusFailed)
		createdBatchJob.UpdatedAt = time.Now()
		createdBatchJob.ErrorDetails = &datamodel.UpgradeErrorDetails{
			ErrorCode:    "BATCH_UPGRADE_FAILED",
			ErrorMessage: "Failed to start batch upgrade workflow: " + err.Error(),
			ErrorType:    "BATCH_UPGRADE_ERROR",
			Retryable:    false,
		}
		if updateErr := se.UpdateBatchClusterUpgradeJob(ctx, createdBatchJob); updateErr != nil {
			logger.Error("Failed to update batch job status after workflow start failure", "batchJobId", batchJobUUID, "error", updateErr)
		}
		return nil, "", customerrors.NewUnavailableErr("Failed to start batch upgrade workflow")
	}

	logger.Info("Batch upgrade workflow started successfully",
		"batchJobId", batchJobUUID,
		"totalClusters", len(selectedClusters),
		"maxConcurrency", params.MaxConcurrency)

	return &models.BatchClusterUpgradeResponse{
		BatchUpgradeID:   createdBatchJob.UUID,
		TotalClusters:    len(selectedClusters),
		SelectedClusters: selectedClusters,
		Status:           models.UpgradeStatusPending,
		JobID:            createdBatchJob.UUID,
		CreatedAt:        createdBatchJob.CreatedAt,
		UpdatedAt:        createdBatchJob.UpdatedAt,
	}, createdBatchJob.UUID, nil
}

func selectAndValidateClustersForBatchUpgrade(
	ctx context.Context,
	se database.Storage,
	params *commonparams.BatchUpgradeClusterParams,
	targetBuildImages *TargetBuildImages,
) ([]models.SelectedCluster, error) {
	if err := common.ValidateBatchClusterSelectionParams(params); err != nil {
		return nil, err
	}

	if params.SelectionType == string(models.SelectionTypePoolList) {
		return selectAndValidateExplicitPools(ctx, se, params, targetBuildImages)
	}

	allCandidateParams := *params
	allCandidateParams.Percentage = 100
	allCandidateParams.MaxClusters = 0
	selectedClusters, err := common.SelectClustersForBatchUpgrade(ctx, se, &allCandidateParams)
	if err != nil {
		return nil, customerrors.NewUnavailableErr("Failed to select clusters for batch upgrade")
	}

	eligibleClusters, err := filterPercentageSelection(ctx, se, selectedClusters, params, targetBuildImages)
	if err != nil {
		return nil, err
	}

	return applyPercentageSelection(eligibleClusters, params.Percentage, params.MaxClusters), nil
}

func selectAndValidateExplicitPools(
	ctx context.Context,
	se database.Storage,
	params *commonparams.BatchUpgradeClusterParams,
	targetBuildImages *TargetBuildImages,
) ([]models.SelectedCluster, error) {
	selectedClusters := make([]models.SelectedCluster, 0, len(params.PoolIDs))
	validationErrors := make([]string, 0)

	pools, err := se.GetPoolsByUUIDs(ctx, params.PoolIDs)
	if err != nil {
		return nil, customerrors.NewUnavailableErr("Failed to retrieve pools for batch upgrade")
	}
	poolsByUUID := indexPoolsByUUID(pools)

	for _, poolID := range params.PoolIDs {
		pool := poolsByUUID[poolID]
		if pool == nil {
			validationErrors = append(validationErrors, fmt.Sprintf("pool %s: not found", poolID))
			continue
		}

		precheck, err := checkClusterUpgradePreconditions(ctx, se, pool, targetBuildImages, params.ForceUpgrade)
		if err != nil {
			return nil, customerrors.NewUnavailableErr(fmt.Sprintf("Failed to check active upgrade jobs for pool %s", poolID))
		}
		if precheck.Status != clusterUpgradePrecheckEligible {
			validationErrors = append(validationErrors, fmt.Sprintf("pool %s: %s", poolID, precheck.Reason))
			continue
		}

		selectedClusters = append(selectedClusters, selectedClusterFromPool(pool, len(selectedClusters)+1))
	}

	if len(validationErrors) > 0 {
		return nil, customerrors.NewConflictErr("Batch upgrade validation failed: " + strings.Join(validationErrors, "; "))
	}

	return selectedClusters, nil
}

func filterPercentageSelection(
	ctx context.Context,
	se database.Storage,
	selectedClusters []models.SelectedCluster,
	params *commonparams.BatchUpgradeClusterParams,
	targetBuildImages *TargetBuildImages,
) ([]models.SelectedCluster, error) {
	logger := util.GetLogger(ctx)
	eligibleClusters := make([]models.SelectedCluster, 0, len(selectedClusters))

	poolIDs := make([]string, 0, len(selectedClusters))
	for _, cluster := range selectedClusters {
		poolIDs = append(poolIDs, cluster.PoolID)
	}
	pools, err := se.GetPoolsByUUIDs(ctx, poolIDs)
	if err != nil {
		return nil, customerrors.NewUnavailableErr("Failed to retrieve pools for batch upgrade")
	}
	poolsByUUID := indexPoolsByUUID(pools)

	for _, cluster := range selectedClusters {
		pool := poolsByUUID[cluster.PoolID]
		if pool == nil {
			return nil, customerrors.NewUnavailableErr(fmt.Sprintf("Failed to retrieve pool %s for batch upgrade", cluster.PoolID))
		}

		precheck, err := checkClusterUpgradePreconditions(ctx, se, pool, targetBuildImages, params.ForceUpgrade)
		if err != nil {
			return nil, customerrors.NewUnavailableErr(fmt.Sprintf("Failed to check active upgrade jobs for pool %s", cluster.PoolID))
		}
		if precheck.Status != clusterUpgradePrecheckEligible {
			logger.Info("Excluding pool that failed cluster upgrade prechecks from percentage selection",
				"poolId", pool.UUID, "precheckStatus", precheck.Status, "reason", precheck.Reason)
			continue
		}

		cluster.Priority = len(eligibleClusters) + 1
		eligibleClusters = append(eligibleClusters, cluster)
	}

	return eligibleClusters, nil
}

func indexPoolsByUUID(pools []*datamodel.Pool) map[string]*datamodel.Pool {
	poolsByUUID := make(map[string]*datamodel.Pool, len(pools))
	for _, pool := range pools {
		if pool != nil {
			poolsByUUID[pool.UUID] = pool
		}
	}
	return poolsByUUID
}

func selectedClusterFromPool(pool *datamodel.Pool, priority int) models.SelectedCluster {
	currentVersion := ""
	if pool.BuildInfo != nil {
		currentVersion = pool.BuildInfo.OntapVersion
	} else {
		currentVersion = env.CurrentOntapVersionDetails
	}

	return models.SelectedCluster{
		ClusterID:      pool.UUID,
		PoolID:         pool.UUID,
		CurrentVersion: currentVersion,
		Priority:       priority,
	}
}

func applyPercentageSelection(clusters []models.SelectedCluster, percentage, maxClusters int) []models.SelectedCluster {
	selectedCount := (len(clusters) * percentage) / 100
	if selectedCount == 0 && percentage > 0 && len(clusters) > 0 {
		selectedCount = 1
	}
	if maxClusters > 0 && selectedCount > maxClusters {
		selectedCount = maxClusters
	}
	if selectedCount > len(clusters) {
		selectedCount = len(clusters)
	}

	return clusters[:selectedCount]
}

// GetBatchUpgradeStatus retrieves the status of a batch cluster upgrade
func (o *GCPOrchestrator) GetBatchUpgradeStatus(ctx context.Context, batchUpgradeID string) (*models.UpgradeProgress, error) {
	batchJob, err := o.storage.GetBatchClusterUpgradeJobByUUID(ctx, batchUpgradeID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("Batch upgrade job", &batchUpgradeID)
		}
		return nil, customerrors.NewUnavailableErr("Failed to retrieve batch upgrade job")
	}

	// Get all child upgrade jobs for this batch
	childJobs, err := o.storage.GetClusterUpgradeJobsByBatchID(ctx, batchUpgradeID)
	if err != nil {
		return nil, customerrors.NewUnavailableErr("Failed to retrieve child upgrade jobs")
	}

	var clusters []models.ClusterUpgradeStatus
	var upgradeErrors []models.UpgradeError

	if batchJob.ErrorDetails != nil {
		upgradeErrors = append(upgradeErrors, models.UpgradeError{
			Code:      batchJob.ErrorDetails.ErrorCode,
			Message:   batchJob.ErrorDetails.ErrorMessage,
			Type:      batchJob.ErrorDetails.ErrorType,
			Retryable: batchJob.ErrorDetails.Retryable,
		})
	}

	for _, childJob := range childJobs {
		clusters = append(clusters, models.ClusterUpgradeStatus{
			ClusterID: childJob.ClusterID,
			Status:    childJob.Status,
			StartTime: childJob.StartedAt,
			EndTime:   childJob.CompletedAt,
		})

		if childJob.ErrorDetails != nil {
			upgradeErrors = append(upgradeErrors, models.UpgradeError{
				Code:      childJob.ErrorDetails.ErrorCode,
				Message:   childJob.ErrorDetails.ErrorMessage,
				Type:      childJob.ErrorDetails.ErrorType,
				Retryable: childJob.ErrorDetails.Retryable,
				ClusterID: childJob.ClusterID,
			})
		}
	}

	var warnings []string
	if batchJob.SkippedCount > 0 {
		warnings = append(warnings, "Some clusters were skipped during batch upgrade")
	}

	return &models.UpgradeProgress{
		JobID:    batchJob.UUID,
		Status:   models.UpgradeStatus(batchJob.Status),
		Clusters: clusters,
		Errors:   upgradeErrors,
		Warnings: warnings,
	}, nil
}

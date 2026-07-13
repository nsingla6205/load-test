package gcp

import (
	"context"
	"errors"
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

// TargetBuildImages encapsulates the target build image information for cluster upgrade
type TargetBuildImages struct {
	OntapVersion string
	VSAImagePath string
	VSAName      string
	MediatorName string
}

var (
	// Function variables for testing
	upgradeCluster             = _upgradeCluster
	checkClusterUpgradeStatus  = _checkClusterUpgradeStatus
	determineTargetBuildImages = _determineTargetBuildImages
	shouldSkipUpgrade          = _shouldSkipUpgrade
	createUpgradeJobInDB       = _createUpgradeJobInDB
	updateUpgradeJobStatus     = common.UpdateUpgradeJobStatus
)

const (
	// UpgradeJobTypeSingle Upgrade job types
	UpgradeJobTypeSingle = "single_cluster_upgrade"
	UpgradeJobTypeBatch  = "batch_cluster_upgrade"
)

// UpgradeCluster upgrades a single VSA cluster
func (o *GCPOrchestrator) UpgradeCluster(ctx context.Context, params *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error) {
	return upgradeCluster(ctx, o.storage, o.temporal, params)
}

// _upgradeCluster implements the core upgrade logic for a single cluster
func _upgradeCluster(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.UpgradeClusterParams) (*models.ClusterUpgradeResponse, string, error) {
	logger := util.GetLogger(ctx)

	// Get pool/cluster information by UUID (no account ID required)
	pool, err := se.GetPoolByUUID(ctx, params.ClusterID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", customerrors.NewNotFoundErr("Cluster", &params.ClusterID)
		}
		logger.Error("Failed to get pool by UUID", "clusterId", params.ClusterID, "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to retrieve cluster information")
	}

	if pool.State != datamodel.LifeCycleStateREADY && pool.State != datamodel.LifeCycleStateDisabled {
		logger.Warn("Cluster is not in a valid state for upgrade", "clusterId", params.ClusterID, "currentState", pool.State)
		return nil, "", customerrors.NewBadRequestErr("Cluster must be in READY or DISABLED state for upgrade. Current state: " + pool.State)
	}
	// Determine target build images
	targetBuildImages, err := determineTargetBuildImages(ctx, se, params)
	if err != nil {
		logger.Error("Failed to determine target build images", "clusterId", params.ClusterID, "error", err)
		return nil, "", err
	}

	// Check if cluster is already upgraded (unless force upgrade)
	shouldSkip := shouldSkipUpgrade(pool, targetBuildImages.VSAName, targetBuildImages.MediatorName, params.ForceUpgrade)

	if shouldSkip {
		logger.Info("Cluster is already upgraded to target build images", "clusterId", params.ClusterID, "targetVSABuildImage", targetBuildImages.VSAName, "targetMediatorBuildImage", targetBuildImages.MediatorName)
		return &models.ClusterUpgradeResponse{
			ClusterID: params.ClusterID,
			Status:    models.UpgradeStatusCompleted,
			JobID:     "",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}, "", nil
	}

	// Check if there's already an active upgrade job for this cluster
	activeJob, err := common.CheckActiveUpgradeJob(ctx, se, params.ClusterID)
	if err != nil {
		logger.Error("Failed to check for active upgrade jobs", "clusterId", params.ClusterID, "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to check for active upgrade jobs")
	}

	if activeJob != nil {
		logger.Warn("Cluster already has an active upgrade job", "clusterId", params.ClusterID, "activeJobId", activeJob.UUID, "activeStatus", activeJob.Status)
		return &models.ClusterUpgradeResponse{
			ClusterID: params.ClusterID,
			Status:    models.UpgradeStatusInProgress,
			JobID:     activeJob.UUID,
			CreatedAt: activeJob.CreatedAt,
			UpdatedAt: activeJob.UpdatedAt,
		}, activeJob.UUID, nil
	}

	// Generate job UUID first
	jobUUID := utils.RandomUUID()

	// Create upgrade job record in database first
	upgradeJob, err := createUpgradeJobInDB(ctx, se, params, pool, targetBuildImages.OntapVersion, jobUUID, targetBuildImages.VSAName, targetBuildImages.MediatorName)
	if err != nil {
		logger.Error("Failed to create upgrade job in database", "clusterId", params.ClusterID, "error", err)
		return nil, "", customerrors.NewUnavailableErr("Failed to create upgrade job")
	}

	// Start upgrade workflow
	workflowOptions := client.StartWorkflowOptions{
		ID:        jobUUID,
		TaskQueue: workflowengine.BackgroundTaskQueue,
	}
	currentVersion := ""
	if pool.BuildInfo != nil {
		currentVersion = pool.BuildInfo.OntapVersion
	} else {
		currentVersion = env.CurrentOntapVersionDetails
	}

	workflowParams := &workflows.ClusterUpgradeWorkflowParams{
		JobID:             jobUUID,
		ClusterID:         params.ClusterID,
		Pool:              pool,
		TargetVersion:     targetBuildImages.OntapVersion,
		CurrentVersion:    currentVersion,
		VSAImagePath:      targetBuildImages.VSAImagePath, // VSA uses image path
		VSAImageName:      targetBuildImages.VSAName,
		MediatorImageName: targetBuildImages.MediatorName, // Mediator uses image name
		ForceUpgrade:      params.ForceUpgrade,
		Metadata:          params.Metadata,
	}

	_, err = temporal.ExecuteWorkflow(ctx, workflowOptions, workflows.ClusterUpgradeWorkflow, workflowParams)
	if err != nil {
		logger.Error("Failed to start upgrade workflow", "clusterId", params.ClusterID, "error", err)
		// Update job status to failed
		updateErr := updateUpgradeJobStatus(ctx, se, jobUUID, string(models.UpgradeStatusFailed), "Failed to start upgrade workflow: "+err.Error())
		if updateErr != nil {
			logger.Error("Failed to update job status after workflow start failure", "clusterId", params.ClusterID, "error", updateErr)
		}
		return nil, "", customerrors.NewUnavailableErr("Failed to start upgrade workflow")
	}

	return &models.ClusterUpgradeResponse{
		ClusterID: params.ClusterID,
		Status:    models.UpgradeStatusInProgress,
		JobID:     upgradeJob.UUID,
		CreatedAt: upgradeJob.CreatedAt,
		UpdatedAt: upgradeJob.UpdatedAt,
	}, upgradeJob.UUID, nil
}

// _checkClusterUpgradeStatus checks if a cluster is already upgraded to the target build images
func _checkClusterUpgradeStatus(pool *datamodel.Pool, targetVSABuildImage, targetMediatorBuildImage string) bool {
	if pool.BuildInfo == nil {
		return false
	}
	if pool.BuildInfo.VSABuildImage != targetVSABuildImage {
		return false
	}
	// True-zonal pools have no mediator VM; VSA image match is sufficient.
	if pool.IsActiveActive() {
		return true
	}
	return pool.BuildInfo.MediatorBuildImage == targetMediatorBuildImage
}

// _determineTargetBuildImages determines the target build images for the upgrade
func _determineTargetBuildImages(ctx context.Context, se database.Storage, params *commonparams.UpgradeClusterParams) (*TargetBuildImages, error) {
	logger := util.GetLogger(ctx)

	// Get VSA image from environment
	vsaImageFromEnv := env.GetString("VSA_IMAGE_NAME", "")
	if vsaImageFromEnv == "" {
		return nil, customerrors.NewUnavailableErr("VSA_IMAGE_NAME not configured in environment")
	}

	// If VSA build is not sent in request, use environment VSA image
	if params.VSABuildImage == "" {
		// Find the version details from image_versions table using environment VSA image
		imageVersions, err := se.ListImageVersions(ctx, true)
		if err != nil {
			return nil, customerrors.NewUnavailableErr("Failed to retrieve available image versions")
		}

		var targetVersion *datamodel.ImageVersion
		for i := range imageVersions {
			if imageVersions[i].VSAName == vsaImageFromEnv && imageVersions[i].OntapVersion == env.CurrentOntapVersionDetails {
				targetVersion = imageVersions[i]
				break
			}
		}

		if targetVersion == nil {
			return nil, customerrors.NewUnavailableErr("VSA image/Ontap version from environment not found in available versions")
		}

		logger.Info("Using VSA image from environment for normal upgrade",
			"ontapVersion", targetVersion.OntapVersion,
			"vsaImagePath", targetVersion.VSAImagePath,
			"vsaName", targetVersion.VSAName,
			"mediatorName", targetVersion.MediatorName)

		return &TargetBuildImages{
			OntapVersion: targetVersion.OntapVersion,
			VSAImagePath: targetVersion.VSAImagePath,
			VSAName:      targetVersion.VSAName,
			MediatorName: targetVersion.MediatorName,
		}, nil
	}

	// VSA build is sent in request - validate force flag
	if params.VSABuildImage != vsaImageFromEnv && !params.ForceUpgrade {
		return nil, customerrors.NewBadRequestErr("Force flag must be true when specifying a VSA build image different from environment")
	}

	// Validate that both VSA and mediator build images are provided
	if params.VSABuildImage == "" || params.MediatorBuildImage == "" {
		return nil, customerrors.NewBadRequestErr("Both VSA and mediator build images are required when specifying build images")
	}

	// Find the version details from image_versions table using specified VSA and mediator build combination
	imageVersions, err := se.ListImageVersions(ctx, true)
	if err != nil {
		return nil, customerrors.NewUnavailableErr("Failed to retrieve available image versions")
	}

	var targetVersion *datamodel.ImageVersion
	for i := range imageVersions {
		if imageVersions[i].VSAName == params.VSABuildImage && imageVersions[i].MediatorName == params.MediatorBuildImage {
			targetVersion = imageVersions[i]
			break
		}
	}

	if targetVersion == nil {
		return nil, customerrors.NewBadRequestErr("Specified VSA and mediator build image combination not found in available versions")
	}

	logger.Info("Using specified VSA build image for upgrade",
		"ontapVersion", targetVersion.OntapVersion,
		"vsaImagePath", targetVersion.VSAImagePath,
		"vsaName", targetVersion.VSAName,
		"mediatorName", targetVersion.MediatorName)

	return &TargetBuildImages{
		OntapVersion: targetVersion.OntapVersion,
		VSAImagePath: targetVersion.VSAImagePath,
		VSAName:      targetVersion.VSAName,
		MediatorName: targetVersion.MediatorName,
	}, nil
}

// _shouldSkipUpgrade determines if the upgrade should be skipped
func _shouldSkipUpgrade(pool *datamodel.Pool, targetVSABuildImage, targetMediatorBuildImage string, forceUpgrade bool) bool {
	if forceUpgrade {
		return false // Force upgrade - don't skip
	}

	isAlreadyUpgraded := checkClusterUpgradeStatus(pool, targetVSABuildImage, targetMediatorBuildImage)

	return isAlreadyUpgraded
}

// _createUpgradeJobInDB creates a new upgrade job record in the database
func _createUpgradeJobInDB(ctx context.Context, se database.Storage, params *commonparams.UpgradeClusterParams, pool *datamodel.Pool, targetVersion, jobUUID, vsaBuildImage, mediatorBuildImage string) (*datamodel.ClusterUpgradeJob, error) {
	currentVersion := "unknown"
	if pool.BuildInfo != nil {
		currentVersion = pool.BuildInfo.OntapVersion
	}

	upgradeJob := &datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID: jobUUID,
		},
		ClusterID:          params.ClusterID,
		PoolID:             pool.UUID,
		TargetVersion:      targetVersion,
		CurrentVersion:     currentVersion,
		VSABuildImage:      vsaBuildImage,
		MediatorBuildImage: mediatorBuildImage,
		Status:             string(models.UpgradeStatusPending),
		Metadata:           common.ConvertMetadataToJSONB(params.Metadata),
		ForceUpgrade:       params.ForceUpgrade,
	}

	createdJob, err := se.CreateClusterUpgradeJob(ctx, upgradeJob)
	if err != nil {
		return nil, err
	}

	return createdJob, nil
}

// HasActiveClusterUpgrade delegates to the shared common helper.
func HasActiveClusterUpgrade(ctx context.Context, se database.Storage, clusterID string) (bool, error) {
	return common.HasActiveClusterUpgrade(ctx, se, clusterID)
}

// GetClusterUpgradeStatus retrieves the status of a cluster upgrade
func (o *GCPOrchestrator) GetClusterUpgradeStatus(ctx context.Context, jobUUID string) (*models.UpgradeProgress, error) {
	upgradeJob, err := o.storage.GetClusterUpgradeJobByUUID(ctx, jobUUID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("Upgrade job", &jobUUID)
		}
		return nil, customerrors.NewUnavailableErr("Failed to retrieve upgrade job")
	}

	clusters := []models.ClusterUpgradeStatus{
		{
			ClusterID: upgradeJob.ClusterID,
			Status:    upgradeJob.Status,
			StartTime: upgradeJob.StartedAt,
			EndTime:   upgradeJob.CompletedAt,
		},
	}

	var errors []models.UpgradeError
	if upgradeJob.ErrorDetails != nil {
		errors = append(errors, models.UpgradeError{
			Code:      upgradeJob.ErrorDetails.ErrorCode,
			Message:   upgradeJob.ErrorDetails.ErrorMessage,
			Type:      upgradeJob.ErrorDetails.ErrorType,
			Retryable: upgradeJob.ErrorDetails.Retryable,
			ClusterID: upgradeJob.ClusterID,
		})
	}

	return &models.UpgradeProgress{
		JobID:    upgradeJob.UUID,
		Status:   models.UpgradeStatus(upgradeJob.Status),
		Clusters: clusters,
		Errors:   errors,
	}, nil
}

// HasActiveClusterUpgrade returns true if the cluster (pool UUID) has an active upgrade job (PENDING or IN_PROGRESS).
func (o *GCPOrchestrator) HasActiveClusterUpgrade(ctx context.Context, clusterID string) (bool, error) {
	return HasActiveClusterUpgrade(ctx, o.storage, clusterID)
}

// ListAvailableVersions lists all available ONTAP versions for upgrade
func (o *GCPOrchestrator) ListAvailableVersions(ctx context.Context) (*models.ListAvailableVersionsResponse, error) {
	return listAvailableVersions(ctx, o.storage)
}

// listAvailableVersions implements the core logic for listing available versions
func listAvailableVersions(ctx context.Context, se database.Storage) (*models.ListAvailableVersionsResponse, error) {
	logger := util.GetLogger(ctx)

	// Get all active versions from database (including current version if it exists)
	imageVersions, err := se.ListImageVersions(ctx, true) // activeOnly = true
	if err != nil {
		logger.Error("Failed to list image versions from database", "error", err)
		return nil, customerrors.NewUnavailableErr("Failed to retrieve available versions")
	}

	// Convert database versions to API models
	var versions []models.AvailableVersion

	// Process database versions
	for _, imageVersion := range imageVersions {
		isCurrent := imageVersion.OntapVersion == env.CurrentOntapVersionDetails
		versions = append(versions, models.AvailableVersion{
			OntapVersion: imageVersion.OntapVersion,
			VSAImagePath: imageVersion.VSAImagePath,
			VSAName:      imageVersion.VSAName,
			MediatorName: imageVersion.MediatorName,
			IsCurrent:    isCurrent,
			IsActive:     imageVersion.IsActive,
		})
	}

	return &models.ListAvailableVersionsResponse{
		Versions: versions,
		Current:  env.CurrentOntapVersionDetails,
	}, nil
}

// CreateImageVersion creates a new image version entry in the database
func (o *GCPOrchestrator) CreateImageVersion(ctx context.Context, ontapVersion, vsaImagePath, vsaName, mediatorName string, isActive bool) (*datamodel.ImageVersion, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Creating new image version", "ontapVersion", ontapVersion)

	// Ensure UUID is populated
	// And validate uniqueness on (ontapVersion) and (vsaName, mediatorName)
	if existing, err := o.storage.GetImageVersionByOntapVersion(ctx, ontapVersion); err == nil && existing != nil {
		return nil, customerrors.NewBadRequestErr("Image version with this ONTAP version already exists")
	}

	// Create the image version model
	imageVersion := &datamodel.ImageVersion{
		BaseModel:    datamodel.BaseModel{UUID: utils.RandomUUID()},
		OntapVersion: ontapVersion,
		VSAImagePath: vsaImagePath,
		VSAName:      vsaName,
		MediatorName: mediatorName,
		IsActive:     isActive,
	}

	// Create in database
	createdVersion, err := o.storage.CreateImageVersion(ctx, imageVersion)
	if err != nil {
		logger.Error("Failed to create image version", "error", err, "ontapVersion", ontapVersion)

		// Check if it's a duplicate error
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, customerrors.NewBadRequestErr("Image version with this ONTAP version already exists")
		}

		return nil, customerrors.NewUnavailableErr("Failed to create image version")
	}

	logger.Info("Successfully created image version", "ontapVersion", createdVersion.OntapVersion)
	return createdVersion, nil
}

// DeleteImageVersion deletes an image version entry from the database
func (o *GCPOrchestrator) DeleteImageVersion(ctx context.Context, ontapVersion string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Deleting image version", "ontapVersion", ontapVersion)

	// First, check if the image version exists
	_, err := o.storage.GetImageVersionByOntapVersion(ctx, ontapVersion)
	if err != nil {
		logger.Error("Image version not found", "error", err, "ontapVersion", ontapVersion)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return customerrors.NewNotFoundErr("ImageVersion", &ontapVersion)
		}
		return customerrors.NewUnavailableErr("Failed to retrieve image version")
	}

	// Delete from database
	err = o.storage.DeleteImageVersion(ctx, ontapVersion)
	if err != nil {
		logger.Error("Failed to delete image version", "error", err, "ontapVersion", ontapVersion)
		return customerrors.NewUnavailableErr("Failed to delete image version")
	}

	logger.Info("Successfully deleted image version", "ontapVersion", ontapVersion)
	return nil
}

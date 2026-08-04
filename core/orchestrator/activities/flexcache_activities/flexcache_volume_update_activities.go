package flexcache_activities

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

type FlexCacheVolumeUpdateActivity struct {
	SE database.Storage
}

var (
	verifyAndGetFlexCacheUpdateParams = _verifyAndGetFlexCacheUpdateParams
)

// UpdateFlexCacheVolumeInONTAP updates an existing FlexCache volume in ONTAP
func (a FlexCacheVolumeUpdateActivity) UpdateFlexCacheVolumeInONTAP(ctx context.Context, volume *datamodel.Volume,
	params *common.UpdateVolumeParams, node *models.Node) (*vsa.OntapAsyncResponse, error) {
	logger := utilGetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("getting provider for FlexCache volume %s update", volume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return nil, err
	}

	flexCacheUpdateVolumeParams, err := verifyAndGetFlexCacheUpdateParams(volume, params)
	if err != nil {
		return nil, err
	}

	recordHeartbeat(ctx, fmt.Sprintf("updating FlexCache volume %s in ONTAP", volume.UUID))
	ontapAsyncResponse, err := provider.UpdateFlexCacheVolume(*flexCacheUpdateVolumeParams)
	if err != nil {
		return nil, err
	}

	logger.Debug("FlexCache volume update initiated successfully")
	return ontapAsyncResponse, nil
}

// CreatePrepopulateJob creates a job record to track the prepopulate operation
// The ONTAP job UUID should already be stored in the volume before calling this
// Returns the created job UUID for tracking
func (a FlexCacheVolumeUpdateActivity) CreatePrepopulateJob(
	ctx context.Context,
	volume *datamodel.Volume,
	ontapJobUUID string,
) (string, error) {
	logger := utilGetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("starting prepopulate job creation for FlexCache volume %s", volume.UUID))

	if ontapJobUUID == "" {
		return "", fmt.Errorf("ontapJobUUID is required")
	}

	job := &datamodel.Job{
		Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		State:        string(datamodel.JobsStateNEW),
		ResourceName: volume.UUID,
		IsAdminJob:   false,
		AccountID:    sql.NullInt64{Int64: volume.AccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: ontapJobUUID,
		},
	}

	recordHeartbeat(ctx, fmt.Sprintf("creating prepopulate job for FlexCache volume %s in database", volume.UUID))
	createdJob, err := a.SE.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job record: %v", err)
		return "", fmt.Errorf("failed to create job record: %w", err)
	}

	logger.Infof("Created prepopulate job %s for volume %s (ONTAP job: %s)",
		createdJob.UUID, volume.UUID, ontapJobUUID)
	return createdJob.UUID, nil
}

// StartFlexCachePrepopulate triggers the prepopulate operation by calling
// the FlexCache volume update API with ONLY prepopulate parameters.
func (a FlexCacheVolumeUpdateActivity) StartFlexCachePrepopulate(
	ctx context.Context,
	volume *datamodel.Volume,
	params *common.UpdateVolumeParams,
	node *models.Node,
) (string, error) {
	logger := utilGetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("getting provider for FlexCache volume %s prepopulate", volume.UUID))
	provider, err := hyperscalerGetProviderByNode(ctx, node)
	if err != nil {
		return "", fmt.Errorf("failed to get provider: %w", err)
	}

	prepopulateOnlyParams, err := buildPrepopulateOnlyParams(volume, params)
	if err != nil {
		return "", fmt.Errorf("failed to build prepopulate params: %w", err)
	}

	logger.Infof("Starting prepopulate for volume %s (UUID: %s) with paths: %v",
		volume.Name, volume.UUID, prepopulateOnlyParams.PrepopulateDirPaths)

	recordHeartbeat(ctx, fmt.Sprintf("starting prepopulate for FlexCache volume %s in ONTAP", volume.UUID))
	ontapAsyncResponse, err := provider.UpdateFlexCacheVolume(*prepopulateOnlyParams)
	if err != nil {
		return "", fmt.Errorf("ONTAP prepopulate request failed: %w", err)
	}

	// Check if completed synchronously
	if ontapAsyncResponse == nil || ontapAsyncResponse.JobUUID == "" {
		logger.Infof("Prepopulate completed synchronously for volume %s", volume.Name)
		return "", nil
	}

	logger.Infof("Prepopulate job created in ONTAP: %s", ontapAsyncResponse.JobUUID)
	return ontapAsyncResponse.JobUUID, nil
}

// UpdatePrepopulateState updates the prepopulate state in the database
func (a FlexCacheVolumeUpdateActivity) UpdatePrepopulateState(
	ctx context.Context,
	volumeUUID string,
	state string,
) error {
	logger := utilGetLogger(ctx)
	logger.Debugf("Updating prepopulate state for volume %s to %s", volumeUUID, state)

	recordHeartbeat(ctx, fmt.Sprintf("getting FlexCache volume %s for prepopulate state update", volumeUUID))
	volume, err := a.SE.GetVolume(ctx, volumeUUID)
	if err != nil {
		return fmt.Errorf("failed to get volume: %w", err)
	}

	if volume.CacheParameters == nil {
		return fmt.Errorf("cannot update prepopulate state: volume %s is not a FlexCache volume (CacheParameters is nil)", volumeUUID)
	}

	if volume.CacheParameters.CacheConfig == nil {
		logger.Debugf("CacheConfig is nil for volume %s, initializing", volumeUUID)
		volume.CacheParameters.CacheConfig = &datamodel.CacheConfig{}
	}

	volume.CacheParameters.CacheConfig.CachePrePopulateState = state

	updates := map[string]interface{}{
		"cache_parameters": volume.CacheParameters,
	}

	recordHeartbeat(ctx, fmt.Sprintf("persisting prepopulate state for FlexCache volume %s", volumeUUID))
	if err := a.SE.UpdateVolumeFields(ctx, volumeUUID, updates); err != nil {
		return fmt.Errorf("failed to update prepopulate state: %w", err)
	}

	return nil
}

// buildPrepopulateOnlyParams creates update params with prepopulate fields set
func buildPrepopulateOnlyParams(volume *datamodel.Volume, params *common.UpdateVolumeParams) (*vsa.UpdateFlexCacheVolumeParams, error) {
	if volume == nil {
		return nil, fmt.Errorf("volume is nil")
	}
	if params == nil || params.CacheParameters == nil || params.CacheParameters.CacheConfig == nil {
		return nil, fmt.Errorf("params or cache config is nil")
	}
	prePop := params.CacheParameters.CacheConfig.CachePrePopulate
	if prePop == nil {
		return nil, fmt.Errorf("prepopulate config is nil")
	}

	// Create params with prepopulate fields
	prepopulateParams := &vsa.UpdateFlexCacheVolumeParams{
		UUID: volume.VolumeAttributes.ExternalUUID,

		// Only set prepopulate fields
		PrepopulateDirPaths:        nil,
		PrepopulateExcludeDirPaths: nil,
		IsRecursionEnabled:         nil,
		// Explicitly leave other fields unset
		// This ensures we don't accidentally modify other FlexCache config
		WritebackEnabled:        nil,
		RelativeSizeEnabled:     nil,
		RelativeSizePercentage:  nil,
		AtimeScrubEnabled:       nil,
		AtimeScrubPeriod:        nil,
		CifsChangeNotifyEnabled: nil,
	}
	// Prepopulate-specific fields from the request
	if prePop.PathList != nil && len(prePop.PathList) > 0 {
		prepopulateParams.PrepopulateDirPaths = common.ConvertStringSliceToPointerSlice(prePop.PathList)
	}
	if prePop.ExcludePathList != nil && len(prePop.ExcludePathList) > 0 {
		prepopulateParams.PrepopulateExcludeDirPaths = common.ConvertStringSliceToPointerSlice(prePop.ExcludePathList)
	}
	prepopulateParams.IsRecursionEnabled = prePop.Recursion
	return prepopulateParams, nil
}

func _verifyAndGetFlexCacheUpdateParams(volume *datamodel.Volume, params *common.UpdateVolumeParams) (*vsa.UpdateFlexCacheVolumeParams, error) {
	if volume == nil {
		return nil, fmt.Errorf("volume is nil")
	}
	if params == nil || params.CacheParameters == nil || params.CacheParameters.CacheConfig == nil {
		return nil, fmt.Errorf("params or cache config is nil")
	}

	flexCacheUpdateVolumeParams := vsa.UpdateFlexCacheVolumeParams{}

	// NOTE: Prepopulate is not included here
	// Prepopulate updates are handled separately
	if params.CacheParameters.CacheConfig.WritebackEnabled != nil {
		flexCacheUpdateVolumeParams.WritebackEnabled = params.CacheParameters.CacheConfig.WritebackEnabled
	}
	if params.CacheParameters.CacheConfig.AtimeScrubEnabled != nil {
		flexCacheUpdateVolumeParams.AtimeScrubEnabled = params.CacheParameters.CacheConfig.AtimeScrubEnabled
	}
	if params.CacheParameters.CacheConfig.AtimeScrubDays != nil {
		flexCacheUpdateVolumeParams.AtimeScrubPeriod = params.CacheParameters.CacheConfig.AtimeScrubDays
	}
	if params.CacheParameters.CacheConfig.CifsChangeNotifyEnabled != nil {
		flexCacheUpdateVolumeParams.CifsChangeNotifyEnabled = params.CacheParameters.CacheConfig.CifsChangeNotifyEnabled
	}
	flexCacheUpdateVolumeParams.UUID = volume.VolumeAttributes.ExternalUUID
	return &flexCacheUpdateVolumeParams, nil
}

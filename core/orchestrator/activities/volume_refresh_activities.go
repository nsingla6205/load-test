package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

var (
	volumeSyncChunkSize         = env.GetInt("VOLUME_SYNC_CHUNK_SIZE", 200)
	GetOntapRestProviderForPool = _getOntapRestProviderForPool
	enableCloneInfoRefresh      = env.GetBool("ENABLE_CLONE_INFO_REFRESH", false)
)

type VolumeRefreshActivity struct {
	SE database.Storage
}

// GetVolumesForRefreshInput identifies volumes to hydrate by account and UUID.
type GetVolumesForRefreshInput struct {
	AccountID int64
	VolumeIDs []string
}

// GetVolumesForRefresh loads volume records required by the volume refresh workflow.
func (a *VolumeRefreshActivity) GetVolumesForRefresh(ctx context.Context, input *GetVolumesForRefreshInput) ([]*datamodel.Volume, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Starting GetVolumesForRefresh activity")

	if input == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("GetVolumesForRefresh input cannot be nil"))
	}
	if input.AccountID == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("GetVolumesForRefresh account ID cannot be zero"))
	}
	if len(input.VolumeIDs) == 0 {
		return []*datamodel.Volume{}, nil
	}

	conditions := [][]interface{}{
		{"account_id = ?", input.AccountID},
		{"uuid in ?", input.VolumeIDs},
	}
	volumes, err := a.SE.GetMultipleVolumes(ctx, conditions)
	if err != nil {
		logger.Errorf("Failed to get volumes for refresh: %v", err)
		return nil, err
	}

	recordHeartbeat(ctx, fmt.Sprintf("Finished GetVolumesForRefresh activity, loaded %d volumes", len(volumes)))
	return volumes, nil
}

type PoolDBVolumesMap struct {
	DBVolumesByExternalUUID map[string]*datamodel.Volume
}

func (a *VolumeRefreshActivity) GetDBVolumesForPool(ctx context.Context, pool *datamodel.Pool) (*PoolDBVolumesMap, error) {
	se := a.SE
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching database volumes for pool %s", pool.UUID))
	dbVolumes, err := se.GetVolumesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf(fmt.Sprintf("Failed to get volumes from database for pool %s: %v", pool.UUID, err))
		return nil, err
	}

	dbVolumeMap := make(map[string]*datamodel.Volume, len(dbVolumes))
	for _, v := range dbVolumes {
		dbVolumeMap[v.VolumeAttributes.ExternalUUID] = v
	}

	recordHeartbeat(ctx, fmt.Sprintf("Finished fetching %d database volumes for pool %s", len(dbVolumes), pool.UUID))
	return &PoolDBVolumesMap{
		DBVolumesByExternalUUID: dbVolumeMap,
	}, nil
}

func (a *VolumeRefreshActivity) GetOntapVolumes(ctx context.Context, pool *datamodel.Pool) (*GetOntapVolumesReturnValue, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for pool %s", pool.UUID))
	provider, err := GetOntapRestProviderForPool(ctx, se, pool)
	if err != nil || provider == nil {
		errMsg := fmt.Sprintf("Failed to get ONTAP rest provider for pool %v: %v", pool.UUID, err)
		logger.Errorf(errMsg)
		return nil, err
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP volumes for pool %s", pool.UUID))
	ontapVolumes, err := provider.GetVolumes()
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get ONTAP volumes for the pool: %s, %v", pool.UUID, err)
		logger.Errorf(errMsg)
		return nil, err
	}
	logger.Debugf("Successfully Fetched %d volumes from ONTAP for pool %s", len(ontapVolumes), pool.UUID)

	ontapVolumeMap := getFilteredOntapVolumesMapByUUID(ontapVolumes)

	recordHeartbeat(ctx, "Finished GetOntapVolumes activity")
	return &GetOntapVolumesReturnValue{
		OntapVolumeMap: ontapVolumeMap,
	}, nil
}

func getFilteredOntapVolumesMapByUUID(volumes []*vsa.Volume) map[string]*vsa.Volume {
	volumeMap := make(map[string]*vsa.Volume, len(volumes))
	for _, volume := range volumes {
		if volume == nil || volume.IsSvmRoot == nil || *volume.IsSvmRoot {
			continue
		}
		if volume.UUID == nil || volume.Svm == nil || volume.Svm.Name == nil || volume.Name == nil {
			continue
		}
		volumeMap[*volume.UUID] = volume
	}
	return volumeMap
}

type GetOntapVolumesReturnValue struct {
	OntapVolumeMap map[string]*vsa.Volume
}

func (a *VolumeRefreshActivity) SyncUpdatedVolumesToDatabase(ctx context.Context, input *SyncUpdatedVolumesInput) error {
	if input == nil {
		return nil
	}
	return _syncUpdatedVolumesToDatabase(ctx, a.SE, input.UpdatedVolumeByUUID, input.VolumesWithClonesSharedBytesReset)
}

// ProcessVolumePoolMappingInput represents input for pool mapping processing
type ProcessVolumePoolMappingInput struct {
	Volumes []*datamodel.Volume
}

// ProcessVolumePoolMappingResult represents the result of pool mapping processing
type ProcessVolumePoolMappingResult struct {
	PoolByUUID map[string]*datamodel.Pool
	PoolUUIDs  []string
}

// ProcessVolumePoolMapping builds pool UUID mappings from the provided volumes
// This activity extracts and organizes pool information for efficient processing
func (a *VolumeRefreshActivity) ProcessVolumePoolMapping(ctx context.Context, input *ProcessVolumePoolMappingInput) (*ProcessVolumePoolMappingResult, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Starting ProcessVolumePoolMapping activity")

	if input == nil || len(input.Volumes) == 0 {
		return &ProcessVolumePoolMappingResult{
			PoolByUUID: make(map[string]*datamodel.Pool),
			PoolUUIDs:  []string{},
		}, nil
	}

	// Build pool mappings with validation
	poolByUUID := make(map[string]*datamodel.Pool)
	for i, vol := range input.Volumes {
		recordHeartbeat(ctx, fmt.Sprintf("Building pool mapping for volume %d of %d", i+1, len(input.Volumes)))
		if vol == nil {
			logger.Warn("Encountered nil volume in input, skipping")
			continue
		}

		if vol.Pool == nil {
			logger.Warnf("Volume %s has no associated pool, skipping", vol.UUID)
			continue
		}

		if vol.Pool.UUID == "" {
			logger.Warnf("Volume %s has pool with empty UUID, skipping", vol.UUID)
			continue
		}

		poolByUUID[vol.Pool.UUID] = vol.Pool
	}

	// Extract pool UUIDs for ordered processing
	poolUUIDs := make([]string, 0, len(poolByUUID))
	for poolUUID := range poolByUUID {
		poolUUIDs = append(poolUUIDs, poolUUID)
	}

	logger.Infof("Processed %d unique pools from %d volumes", len(poolByUUID), len(input.Volumes))

	recordHeartbeat(ctx, "Finished ProcessVolumePoolMapping activity")

	return &ProcessVolumePoolMappingResult{
		PoolByUUID: poolByUUID,
		PoolUUIDs:  poolUUIDs,
	}, nil
}

// ProcessOntapVolumeMatchingInput represents input for ONTAP volume matching
type ProcessOntapVolumeMatchingInput struct {
	DbVolumes           []*datamodel.Volume
	OntapVolumesResults map[string]*GetOntapVolumesReturnValue
}

// ProcessOntapVolumeMatchingResult represents the result of ONTAP volume matching
type ProcessOntapVolumeMatchingResult struct {
	UpdatedVolumeByUUID               map[string]*datamodel.Volume
	OntapVolResponse                  map[string]*vsa.VolumeResponse
	VolumesNotFoundInONTAP            []*datamodel.Volume
	VolumesNotCloneInONTAP            []*datamodel.Volume // Volumes that are clones in DB but are regular volumes in ONTAP
	VolumesWithClonesSharedBytesReset map[string]bool     // UUIDs of volumes whose clones_shared_bytes must be zeroed
	MatchedCount                      int
	NotFoundCount                     int
}

// SyncUpdatedVolumesInput is the input to SyncUpdatedVolumesToDatabase.
// It carries both the volumes to update and the set of UUIDs whose clones_shared_bytes
// must be reset to 0 because ONTAP reports them as no longer being flexclones.
type SyncUpdatedVolumesInput struct {
	UpdatedVolumeByUUID               map[string]*datamodel.Volume
	VolumesWithClonesSharedBytesReset map[string]bool
}

// ProcessOntapVolumeMatching matches database volumes with ONTAP volumes and prepares updates
// This activity handles the complex logic of correlating volumes and extracting update data
func (a *VolumeRefreshActivity) ProcessOntapVolumeMatching(ctx context.Context, input *ProcessOntapVolumeMatchingInput) (*ProcessOntapVolumeMatchingResult, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, "Starting ProcessOntapVolumeMatching activity")

	if input == nil {
		return nil, fmt.Errorf("ProcessOntapVolumeMatching input cannot be nil")
	}

	result := &ProcessOntapVolumeMatchingResult{
		UpdatedVolumeByUUID:               make(map[string]*datamodel.Volume),
		OntapVolResponse:                  make(map[string]*vsa.VolumeResponse),
		VolumesNotFoundInONTAP:            make([]*datamodel.Volume, 0),
		VolumesNotCloneInONTAP:            make([]*datamodel.Volume, 0),
		VolumesWithClonesSharedBytesReset: make(map[string]bool),
	}

	// Process each database volume
	for i, dbVolume := range input.DbVolumes {
		recordHeartbeat(ctx, fmt.Sprintf("Matching database volume %d of %d against ONTAP", i+1, len(input.DbVolumes)))
		if err := a.processIndividualVolume(ctx, dbVolume, input.OntapVolumesResults, result); err != nil {
			logger.Errorf("Failed to process volume %s: %v", dbVolume.UUID, err)
			return nil, fmt.Errorf("failed to process volume %s: %w", dbVolume.UUID, err)
		}
	}

	result.MatchedCount = len(result.UpdatedVolumeByUUID)
	result.NotFoundCount = len(result.VolumesNotFoundInONTAP)

	logger.Infof("Volume matching completed: %d matched, %d not found in ONTAP",
		result.MatchedCount, result.NotFoundCount)

	// Log volumes that are clones in DB but not clones in ONTAP
	if len(result.VolumesNotCloneInONTAP) > 0 {
		logger.Warnf("%d volumes are clones in database but are regular volumes in ONTAP (not clones): %v",
			len(result.VolumesNotCloneInONTAP),
			getVolumeUUIDs(result.VolumesNotCloneInONTAP))
	}

	recordHeartbeat(ctx, "Finished ProcessOntapVolumeMatching activity")
	return result, nil
}

// processIndividualVolume handles the matching logic for a single volume
func (a *VolumeRefreshActivity) processIndividualVolume(
	ctx context.Context,
	dbVolume *datamodel.Volume,
	ontapVolumesResults map[string]*GetOntapVolumesReturnValue,
	result *ProcessOntapVolumeMatchingResult,
) error {
	logger := util.GetLogger(ctx)

	// Validate volume prerequisites
	if dbVolume.Pool == nil {
		logger.Warnf("Volume %s has no associated pool, skipping", dbVolume.UUID)
		return nil
	}

	if dbVolume.VolumeAttributes == nil {
		logger.Warnf("Volume %s has no volume attributes, skipping", dbVolume.UUID)
		return nil
	}

	if dbVolume.VolumeAttributes.ExternalUUID == "" {
		logger.Warnf("Volume %s has no external UUID, skipping", dbVolume.UUID)
		return nil
	}

	// Find ONTAP volumes for this volume's pool
	ontapVolumes, poolExists := ontapVolumesResults[dbVolume.Pool.UUID]
	if !poolExists {
		logger.Warnf("No ONTAP volumes found for pool %s, volume %s cannot be updated",
			dbVolume.Pool.UUID, dbVolume.UUID)
		result.VolumesNotFoundInONTAP = append(result.VolumesNotFoundInONTAP, dbVolume)
		return nil
	}

	// Look for this volume in ONTAP results using ExternalUUID
	ontapVolume, volumeExists := ontapVolumes.OntapVolumeMap[dbVolume.VolumeAttributes.ExternalUUID]
	if !volumeExists {
		logger.Warnf("Volume %s (ExternalUUID: %s) not found in ONTAP for pool %s",
			dbVolume.UUID, dbVolume.VolumeAttributes.ExternalUUID, dbVolume.Pool.Name)
		result.VolumesNotFoundInONTAP = append(result.VolumesNotFoundInONTAP, dbVolume)
		return nil
	}

	// Validate ONTAP volume has required fields
	if err := a.validateOntapVolume(ontapVolume); err != nil {
		logger.Warnf("ONTAP volume validation failed for %s: %v", dbVolume.UUID, err)
		result.VolumesNotFoundInONTAP = append(result.VolumesNotFoundInONTAP, dbVolume)
		return nil
	}

	// If volume is a clone in database, validate ONTAP has complete clone info
	var skipCloneInfoUpdate bool
	if enableCloneInfoRefresh && dbVolume.ClonesSharedBytes > 0 && ontapVolume != nil {
		if ontapVolume.Clone == nil {
			logger.Warnf("Volume %s is a clone in database (ClonesSharedBytes: %d) but is not a clone in ONTAP (missing clone info), treating as regular volume",
				dbVolume.UUID, dbVolume.ClonesSharedBytes)
			result.VolumesNotCloneInONTAP = append(result.VolumesNotCloneInONTAP, dbVolume)
			skipCloneInfoUpdate = true
		} else if ontapVolume.Clone != nil && (ontapVolume.Clone.ParentVolume == nil || ontapVolume.Clone.ParentVolume.Name == nil) {
			logger.Warnf("Volume %s is a clone in database but ONTAP is missing parent volume name, treating as regular volume",
				dbVolume.UUID)
			result.VolumesNotCloneInONTAP = append(result.VolumesNotCloneInONTAP, dbVolume)
			skipCloneInfoUpdate = true
		} else if ontapVolume.Clone != nil && (ontapVolume.Clone.ParentSnapshot == nil || ontapVolume.Clone.ParentSnapshot.Name == nil) {
			logger.Warnf("Volume %s is a clone in database but ONTAP is missing parent snapshot name, treating as regular volume",
				dbVolume.UUID)
			result.VolumesNotCloneInONTAP = append(result.VolumesNotCloneInONTAP, dbVolume)
			skipCloneInfoUpdate = true
		}
	}

	// Extract new values from ONTAP
	newUsedBytes := uint64(*ontapVolume.Space.LogicalSpace.Used)

	// Check if there are any differences between database and ONTAP values
	hasChanges := false
	if dbVolume.UsedBytes != newUsedBytes {
		hasChanges = true
		logger.Debugf("Volume %s UsedBytes changed: %d -> %d", dbVolume.UUID, dbVolume.UsedBytes, newUsedBytes)
	}

	// Check if volume is a clone and clone details are missing
	needsCloneUpdate := false
	var parentVolumeUUID, parentSnapshotUUID string
	var splitCompletePercent *int64
	var isFlexclone bool
	// cloneState and cloneStateDetails carry the state/stateDetails to write into CloneParentInfo.
	// They default to the existing DB values so we never silently drop them on unrelated updates.
	var cloneState, cloneStateDetails string

	// Get is_flexclone and split_complete_percent from ONTAP if available
	// Only set split_complete_percent if is_flexclone is true
	if ontapVolume != nil && ontapVolume.Clone != nil {
		if ontapVolume.Clone.IsFlexclone != nil {
			isFlexclone = *ontapVolume.Clone.IsFlexclone
		}
		// Only get split_complete_percent if is_flexclone is true
		if isFlexclone && ontapVolume.Clone.SplitCompletePercent != nil {
			splitCompletePercent = ontapVolume.Clone.SplitCompletePercent
		}
	}

	// Seed cloneState/cloneStateDetails from the existing DB record so they are preserved
	// across refresh cycles unless explicitly overwritten below.
	if dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.CloneParentInfo != nil {
		cloneState = dbVolume.VolumeAttributes.CloneParentInfo.State
		cloneStateDetails = dbVolume.VolumeAttributes.CloneParentInfo.StateDetails
	}

	// If is_flexclone is false and CloneParentInfo exists in DB, we need to remove it.
	// Also reset clones_shared_bytes to 0 — ONTAP is the source of truth for clone state.
	if !isFlexclone {
		if dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.CloneParentInfo != nil {
			needsCloneUpdate = true
			hasChanges = true
			logger.Debugf("Volume %s is_flexclone is false, removing CloneParentInfo from database", dbVolume.UUID)
		}
		if dbVolume.ClonesSharedBytes > 0 {
			hasChanges = true
			result.VolumesWithClonesSharedBytesReset[dbVolume.UUID] = true
			logger.Warnf("Volume %s is not a flexclone in ONTAP but has ClonesSharedBytes=%d in DB; resetting to 0",
				dbVolume.UUID, dbVolume.ClonesSharedBytes)
		}
	}

	// Only process clone info if volume is a clone in database and is_flexclone is true
	// If is_flexclone is false, we'll handle removal of CloneParentInfo separately
	if enableCloneInfoRefresh && dbVolume.ClonesSharedBytes > 0 && !skipCloneInfoUpdate && isFlexclone {
		if ontapVolume != nil && ontapVolume.Clone != nil &&
			ontapVolume.Clone.ParentVolume != nil && ontapVolume.Clone.ParentVolume.Name != nil &&
			ontapVolume.Clone.ParentSnapshot != nil && ontapVolume.Clone.ParentSnapshot.Name != nil {
			parentVolumeName := *ontapVolume.Clone.ParentVolume.Name
			parentSnapshotName := *ontapVolume.Clone.ParentSnapshot.Name

			// Look up parent volume UUID from database using parent volume name
			recordHeartbeat(ctx, fmt.Sprintf("Fetching parent volume for clone %s", dbVolume.UUID))
			parentVolume, err := a.SE.GetVolumeByNameAndAccountID(ctx, parentVolumeName, dbVolume.AccountID)
			if err != nil {
				logger.Warnf("Failed to get parent volume %s for clone volume %s: %v", parentVolumeName, dbVolume.UUID, err)
				// Continue without clone info update if parent volume not found
			} else {
				parentVolumeUUID = parentVolume.UUID

				// Look up parent snapshot UUID from database using parent snapshot name and parent volume ID
				recordHeartbeat(ctx, fmt.Sprintf("Fetching parent snapshot for clone %s", dbVolume.UUID))
				parentSnapshot, err := a.SE.GetSnapshotByNameAndVolumeId(ctx, parentSnapshotName, dbVolume.AccountID, parentVolume.ID)
				if err != nil {
					logger.Warnf("Failed to get parent snapshot %s for clone volume %s (parent volume: %s): %v",
						parentSnapshotName, dbVolume.UUID, parentVolumeName, err)
					// Continue without clone info update if parent snapshot not found
				} else {
					parentSnapshotUUID = parentSnapshot.UUID

					// Check if clone details are missing in database
					if dbVolume.VolumeAttributes.CloneParentInfo == nil ||
						dbVolume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID == "" ||
						dbVolume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID == "" {
						needsCloneUpdate = true
						hasChanges = true
						logger.Debugf("Volume %s is a clone with missing clone details. Parent Volume: %s (UUID: %s), Parent Snapshot: %s (UUID: %s)",
							dbVolume.UUID, parentVolumeName, parentVolumeUUID, parentSnapshotName, parentSnapshotUUID)
					} else {
						// Check if the UUIDs don't match (update if different)
						if dbVolume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID != parentVolumeUUID ||
							dbVolume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID != parentSnapshotUUID {
							needsCloneUpdate = true
							hasChanges = true
							logger.Infof("Volume %s clone details mismatch. Updating Parent Volume UUID: %s -> %s, Parent Snapshot UUID: %s -> %s",
								dbVolume.UUID,
								dbVolume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID, parentVolumeUUID,
								dbVolume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID, parentSnapshotUUID)
						}
					}
				}
			}
		}
	}

	// Only process split_complete_percent if is_flexclone is true
	if isFlexclone && splitCompletePercent != nil {
		// Check if volume has CloneParentInfo in DB (indicating it's a clone, even if splitting)
		if dbVolume.VolumeAttributes != nil && dbVolume.VolumeAttributes.CloneParentInfo != nil {
			// Check if split_complete_percent changed (handle both non-zero and zero values)
			currentSplitPercent := dbVolume.VolumeAttributes.CloneParentInfo.SplitCompletePercent
			if currentSplitPercent == nil || *currentSplitPercent != *splitCompletePercent {
				needsCloneUpdate = true
				hasChanges = true
				logger.Debugf("Volume %s split_complete_percent changed: %v -> %d",
					dbVolume.UUID, currentSplitPercent, *splitCompletePercent)
				// Preserve existing parent UUIDs if we don't have them from the above block
				if parentVolumeUUID == "" {
					parentVolumeUUID = dbVolume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID
				}
				if parentSnapshotUUID == "" {
					parentSnapshotUUID = dbVolume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID
				}
			}

			if *splitCompletePercent != 0 && currentSplitPercent != nil && *currentSplitPercent < *splitCompletePercent {
				if cloneState == datamodel.CloneStateErrorInSplitting {
					cloneState = datamodel.CloneStateSplitting
					cloneStateDetails = ""
					needsCloneUpdate = true
					hasChanges = true
					logger.Debugf("Volume %s split_complete_percent advanced to %d, clearing stale SPLIT_FAILED state",
						dbVolume.UUID, *splitCompletePercent)
				}
			}
		}
	}

	// Detect split error: is_flexclone is still true but split_complete_percent dropped to nil/0
	// after previously being non-zero. This means ONTAP encountered an error while splitting.
	// Look up the SPLIT_CLONE_VOLUME job for this volume to get the actual error details; there
	// is at most one such job per volume (a new split cannot start while one is in progress).
	if isFlexclone &&
		(splitCompletePercent == nil || *splitCompletePercent == 0) &&
		dbVolume.VolumeAttributes != nil &&
		dbVolume.VolumeAttributes.CloneParentInfo != nil &&
		dbVolume.VolumeAttributes.CloneParentInfo.SplitCompletePercent != nil &&
		*dbVolume.VolumeAttributes.CloneParentInfo.SplitCompletePercent != 0 &&
		cloneState != datamodel.CloneStateErrorInSplitting {
		needsCloneUpdate = true
		hasChanges = true
		cloneState = datamodel.CloneStateErrorInSplitting
		// Attempt to fetch the split job error details from the DB.
		recordHeartbeat(ctx, fmt.Sprintf("Fetching split job for volume %s", dbVolume.UUID))
		splitJob, jobErr := a.SE.GetJobByResourceUUID(ctx, dbVolume.UUID, string(datamodel.JobTypeSplitVolume))
		if jobErr != nil {
			logger.Warnf("Volume %s: could not fetch split job for error details: %v", dbVolume.UUID, jobErr)
			cloneStateDetails = "Split operation encountered an error in ONTAP"
		} else if splitJob != nil && splitJob.ErrorDetails != "" {
			cloneStateDetails = splitJob.ErrorDetails
		} else {
			cloneStateDetails = "Split operation encountered an error in ONTAP"
		}
		// Preserve parent UUIDs
		if parentVolumeUUID == "" {
			parentVolumeUUID = dbVolume.VolumeAttributes.CloneParentInfo.ParentVolumeUUID
		}
		if parentSnapshotUUID == "" {
			parentSnapshotUUID = dbVolume.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID
		}
		logger.Warnf("Volume %s split_complete_percent dropped to nil/0 while is_flexclone is still true; marking as SPLIT_FAILED (details: %s)",
			dbVolume.UUID, cloneStateDetails)
	}

	// Only add to update list if there are actual changes
	if hasChanges {
		// Create updated volume with ONTAP data
		updatedVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: dbVolume.UUID,
				ID:   dbVolume.ID,
			},
			UsedBytes: newUsedBytes,
		}

		// Add clone parent info if needed (only if is_flexclone is true)
		// If is_flexclone is false, needsCloneUpdate will be true but we won't set CloneParentInfo (removing it)
		if needsCloneUpdate && isFlexclone {
			cloneParentInfo := &datamodel.CloneParentInfo{
				ParentVolumeUUID:   parentVolumeUUID,
				ParentSnapshotUUID: parentSnapshotUUID,
				State:              cloneState,
				StateDetails:       cloneStateDetails,
			}
			if splitCompletePercent != nil {
				cloneParentInfo.SplitCompletePercent = splitCompletePercent
			}
			updatedVolume.VolumeAttributes = &datamodel.VolumeAttributes{
				CloneParentInfo: cloneParentInfo,
			}
		} else if needsCloneUpdate && !isFlexclone {
			// is_flexclone is false, remove CloneParentInfo by setting it to nil
			updatedVolume.VolumeAttributes = &datamodel.VolumeAttributes{
				CloneParentInfo: nil,
			}
		}

		// Create volume response for additional processing
		volResponse := &vsa.VolumeResponse{
			UsedBytes: *ontapVolume.Space.LogicalSpace.Used,
		}

		// Store results
		result.UpdatedVolumeByUUID[dbVolume.UUID] = updatedVolume
		result.OntapVolResponse[dbVolume.UUID] = volResponse

		if needsCloneUpdate {
			logger.Debugf("Successfully matched volume %s with ONTAP data (UsedBytes: %d, Clone Parent Volume: %s, Clone Parent Snapshot: %s)",
				dbVolume.UUID, updatedVolume.UsedBytes, parentVolumeUUID, parentSnapshotUUID)
		} else {
			logger.Debugf("Successfully matched volume %s with ONTAP data (UsedBytes: %d)",
				dbVolume.UUID, updatedVolume.UsedBytes)
		}
	} else {
		logger.Debugf("Volume %s has no changes, skipping database update", dbVolume.UUID)
	}

	return nil
}

// validateOntapVolume validates that the ONTAP volume has all required fields
func (a *VolumeRefreshActivity) validateOntapVolume(ontapVolume *vsa.Volume) error {
	if ontapVolume == nil {
		return fmt.Errorf("ONTAP volume is nil")
	}

	if ontapVolume.Space == nil {
		return fmt.Errorf("ONTAP volume space information is nil")
	}

	if ontapVolume.Space.LogicalSpace == nil {
		return fmt.Errorf("ONTAP volume logical space information is nil")
	}

	if ontapVolume.Space.LogicalSpace.Used == nil {
		return fmt.Errorf("ONTAP volume used space information is nil")
	}

	return nil
}

func _syncUpdatedVolumesToDatabase(ctx context.Context, se database.Storage, dbVols map[string]*datamodel.Volume, clonesSharedBytesResetUUIDs map[string]bool) error {
	log := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Starting SyncUpdatedVolumesToDatabase activity")

	if len(dbVols) == 0 {
		log.Debugf("No volumes to update in the database")
		return nil
	}

	// Separate volumes into two groups:
	// 1. Volumes that only need used_bytes updates (can use efficient batch update)
	// 2. Volumes that need volume_attributes updates (clone info) - these will get BOTH
	//    used_bytes AND volume_attributes updated via individual UpdateVolumeFields calls
	volumesForBatchUpdate := make([]datamodel.VolumeFieldUpdate, 0, len(dbVols))
	volumesForCloneUpdate := make([]*datamodel.Volume, 0)

	for _, vol := range dbVols {
		// Check if volume needs clone info update (either to add/update or to remove CloneParentInfo)
		// or needs clones_shared_bytes reset. Both require individual UpdateVolumeFields calls.
		// If VolumeAttributes is set (even if CloneParentInfo is nil), it means we need to update volume_attributes.
		if vol.VolumeAttributes != nil || clonesSharedBytesResetUUIDs[vol.UUID] {
			// Volume needs clone info and/or clones_shared_bytes update - will get both
			// used_bytes AND volume_attributes/clones_shared_bytes updated via individual UpdateVolumeFields calls
			volumesForCloneUpdate = append(volumesForCloneUpdate, vol)
		} else {
			// Only used_bytes update needed - can use efficient batch update
			fieldUpdate := datamodel.VolumeFieldUpdate{
				UUID: vol.UUID,
				Fields: map[string]interface{}{
					"used_bytes": vol.UsedBytes,
				},
			}
			volumesForBatchUpdate = append(volumesForBatchUpdate, fieldUpdate)
		}
	}

	// Batch update volumes that only need used_bytes
	if len(volumesForBatchUpdate) > 0 {
		for i := 0; i < len(volumesForBatchUpdate); i += volumeSyncChunkSize {
			end := i + volumeSyncChunkSize
			if end > len(volumesForBatchUpdate) {
				end = len(volumesForBatchUpdate)
			}

			fieldUpdateChunk := volumesForBatchUpdate[i:end]

			recordHeartbeat(ctx, fmt.Sprintf("Updating database volume chunk %d-%d of %d", i+1, end, len(volumesForBatchUpdate)))
			if err := se.BatchUpdateVolumeFields(ctx, fieldUpdateChunk); err != nil {
				return err
			}
		}
		log.Debugf("Batch updated used_bytes for %d volumes", len(volumesForBatchUpdate))
	}

	// Individual updates for volumes that need clone info and/or clones_shared_bytes reset
	for _, vol := range volumesForCloneUpdate {
		recordHeartbeat(ctx, fmt.Sprintf("Updating clone data for volume %s", vol.UUID))
		updateFields := map[string]interface{}{
			"used_bytes": vol.UsedBytes,
		}

		// Include clones_shared_bytes reset when ONTAP reports the volume is no longer a flexclone
		if clonesSharedBytesResetUUIDs[vol.UUID] {
			updateFields["clones_shared_bytes"] = uint64(0)
		}

		// Include volume_attributes update only when clone info needs to change
		if vol.VolumeAttributes != nil {
			// Fetch existing volume to get current volume_attributes for merging
			dbVolume, err := se.GetVolume(ctx, vol.UUID)
			if err != nil {
				log.Errorf("Failed to get volume %s for clone info update: %v", vol.UUID, err)
				continue
			}

			// Merge clone parent info into existing volume_attributes.
			// Copy the entire existing struct so every field (including any future additions)
			// is preserved, then overwrite only CloneParentInfo with the new value.
			var updatedAttributes *datamodel.VolumeAttributes
			if dbVolume.VolumeAttributes != nil {
				// Shallow-copy the whole struct — all scalar fields and pointer fields are
				// preserved.  CloneParentInfo is the only field the refresh workflow is
				// allowed to change, so we overwrite it right after.
				attrsCopy := *dbVolume.VolumeAttributes
				updatedAttributes = &attrsCopy
				// If vol.VolumeAttributes.CloneParentInfo is nil, remove it from DB (set to nil).
				// If it's not nil, update it.
				updatedAttributes.CloneParentInfo = vol.VolumeAttributes.CloneParentInfo
				// OriginalSharedBytes is only meaningful while a volume is still
				// a thin clone. When the refresh transitions the volume out of
				// clone state (ONTAP no longer reports it as a flexclone), drop
				// the now-orphaned baseline so it cannot mislead a future
				// splitStop response.
				if vol.VolumeAttributes.CloneParentInfo == nil {
					updatedAttributes.OriginalSharedBytes = nil
				}
			} else {
				// No existing attributes; create new with clone info only.
				updatedAttributes = &datamodel.VolumeAttributes{
					CloneParentInfo: vol.VolumeAttributes.CloneParentInfo,
				}
			}

			updateFields["volume_attributes"] = updatedAttributes

			if vol.VolumeAttributes.CloneParentInfo != nil {
				log.Debugf("Updated volume %s with clone parent info (Parent Volume: %s, Parent Snapshot: %s)",
					vol.UUID,
					vol.VolumeAttributes.CloneParentInfo.ParentVolumeUUID,
					vol.VolumeAttributes.CloneParentInfo.ParentSnapshotUUID)
			} else {
				log.Debugf("Updated volume %s: removed CloneParentInfo from database", vol.UUID)
			}
		}

		recordHeartbeat(ctx, fmt.Sprintf("Updating clone data for volume %s in database", vol.UUID))
		if err := se.UpdateVolumeFields(ctx, vol.UUID, updateFields); err != nil {
			log.Errorf("Failed to update volume %s: %v", vol.UUID, err)
			return err
		}
	}

	if len(volumesForCloneUpdate) > 0 {
		log.Debugf("Updated clone info for %d volumes", len(volumesForCloneUpdate))
	}

	recordHeartbeat(ctx, "Finished SyncUpdatedVolumesToDatabase activity")
	log.Debugf("Successfully updated %d volumes using field updates", len(dbVols))
	return nil
}

func _getOntapRestProviderForPool(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(nodes) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("no nodes found for pool %s", pool.UUID))
	}

	if pool.PoolCredentials == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool credentials not found for pool %s", pool.UUID))
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	// Node now contains CA fields from PoolCredentials, so we can use GetProviderByNode directly
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for pool %s", pool.UUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider, nil
}

// UpdateAccountVolumeRefreshTimestampInput represents input for updating account metadata
type UpdateAccountVolumeRefreshTimestampInput struct {
	AccountUUID string
	CompletedAt time.Time
}

// UpdateAccountVolumeRefreshTimestamp updates the VolumeRefreshWorkflowLastCompletionAt timestamp in AccountMetadata
// This activity should be called at the end of VolumeRefreshWorkflow to record the completion time
func (a *VolumeRefreshActivity) UpdateAccountVolumeRefreshTimestamp(ctx context.Context, input *UpdateAccountVolumeRefreshTimestampInput) error {
	logger := util.GetLogger(ctx)
	se := a.SE
	recordHeartbeat(ctx, "Starting UpdateAccountVolumeRefreshTimestamp activity")

	if input == nil {
		return fmt.Errorf("UpdateAccountVolumeRefreshTimestamp input cannot be nil")
	}

	if input.AccountUUID == "" {
		return fmt.Errorf("account UUID cannot be empty")
	}

	logger.Infof("Updating VolumeRefreshWorkflow completion timestamp for account %s to %v",
		input.AccountUUID, input.CompletedAt)

	err := se.UpdateAccountVolumeRefreshTimestamp(ctx, input.AccountUUID, input.CompletedAt)
	if err != nil {
		logger.Errorf("Failed to update account volume refresh timestamp for account %s: %v",
			input.AccountUUID, err)
		return fmt.Errorf("failed to update account volume refresh timestamp: %w", err)
	}

	logger.Infof("Successfully updated VolumeRefreshWorkflow completion timestamp for account %s",
		input.AccountUUID)

	recordHeartbeat(ctx, "Finished UpdateAccountVolumeRefreshTimestamp activity")
	return nil
}

// getVolumeUUIDs extracts UUIDs from a slice of volumes for logging
func getVolumeUUIDs(volumes []*datamodel.Volume) []string {
	uuids := make([]string, 0, len(volumes))
	for _, vol := range volumes {
		if vol != nil {
			uuids = append(uuids, vol.UUID)
		}
	}
	return uuids
}

package expertmodeactivities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type ExpertModeVolumeActivity struct {
	SE database.Storage
}

var (
	fetchOntapVolumeByUUID      = _fetchOntapVolumeByUUID
	fetchOntapCloneVolumeByUUID = _fetchOntapCloneVolumeByUUID
)

func recordHeartbeat(ctx context.Context, details string) {
	if activity.IsActivity(ctx) {
		activity.RecordHeartbeat(ctx, details)
	}
}

// expertModeOntapSizeToleranceBytes: max symmetric |ONTAP size − DB SizeInBytes| (1 decimal GB, 10^9).
const expertModeOntapSizeToleranceBytes int64 = 1000 * 1000 * 1000

// expertModeVolumeSizesMatchForValidation: true if |ontap.Size-dbVol.SizeInBytes| <= tolerance, or DB size <= 0 (skip); false if nil.
func expertModeVolumeSizesMatchForValidation(dbVol *datamodel.ExpertModeVolumes, ontap *vsa.VolumeResponse) bool {
	if dbVol == nil || ontap == nil {
		return false
	}
	db := dbVol.SizeInBytes
	if db <= 0 {
		return true
	}
	d := ontap.Size - db
	if d < 0 {
		d = -d
	}
	return d <= expertModeOntapSizeToleranceBytes
}

// FetchOntapVolumeByName fetches a volume from ONTAP by name and updates the volume with ONTAP data.
func (a *ExpertModeVolumeActivity) FetchOntapVolumeByName(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (*datamodel.ExpertModeVolumes, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for volume %s", volume.Name))

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get ONTAP provider from node: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var svmName string
	if volume.Svm != nil {
		svmName = volume.Svm.Name
	}

	getVolumeParams := vsa.GetVolumeParams{
		VolumeName: volume.Name,
		SvmName:    svmName,
		IsRestore:  false,
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting volume %s from ONTAP", volume.Name))
	volumeResponse, err := provider.GetVolumeForExpertMode(getVolumeParams)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.Infof("Volume %s not found in ONTAP, will retry", volume.Name)
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err))
		}
		logger.Errorf("Failed to get volume %s from ONTAP: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if !expertModeVolumeSizesMatchForValidation(volume, volumeResponse) {
		logger.Infof("Volume %s ONTAP size %d not within 1 GB of DB size %d (state=%q, style=%q), will retry",
			volume.Name, volumeResponse.Size, volume.SizeInBytes, volumeResponse.State, volumeResponse.Style)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("volume %s ONTAP size %d not within 1 GB of DB size %d, still provisioning", volume.Name, volumeResponse.Size, volume.SizeInBytes)))
	}

	logger.Infof("Volume %s found in ONTAP", volume.Name)

	volume.Name = volumeResponse.Name
	volume.SizeInBytes = volumeResponse.Size
	volume.Style = volumeResponse.Style
	volume.State = datamodel.LifeCycleStateAvailable
	volume.ExternalUUID = volumeResponse.ExternalUUID

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.IsFlexclone {
		recordHeartbeat(ctx, fmt.Sprintf("Getting clone metadata for volume %s from ONTAP", volume.Name))
		cloneVolumeResponse, cloneErr := provider.GetCloneVolumeForExpertMode(vsa.GetVolumeParams{
			UUID:      volume.ExternalUUID,
			SvmName:   svmName,
			IsRestore: false,
		})
		if cloneErr != nil {
			logger.Errorf("Failed to fetch clone metadata for volume %s (%s): %v", volume.Name, volume.ExternalUUID, cloneErr)
			return nil, vsaerrors.WrapAsTemporalApplicationError(cloneErr)
		}
		var parentVolUUID, parentSnapUUID string
		if cloneVolumeResponse.Clone != nil {
			parentVolUUID = cloneVolumeResponse.Clone.ParentVolumeUUID
			parentSnapUUID = cloneVolumeResponse.Clone.ParentSnapshotUUID
		}
		sharedBytes, sharedErr := resolveExpertModeFlexcloneSharedBytes(
			ctx,
			provider,
			parentVolUUID,
			parentSnapUUID,
		)
		if sharedErr != nil {
			logger.Errorf("Failed to resolve clone shared bytes from parent snapshot for volume %s: %v", volume.Name, sharedErr)
			return nil, vsaerrors.WrapAsTemporalApplicationError(sharedErr)
		}
		volume.SharedBytes = sharedBytes
	}

	return volume, nil
}

// resolveExpertModeFlexcloneSharedBytes sets shared space to the parent snapshot logical size from ONTAP.
func resolveExpertModeFlexcloneSharedBytes(ctx context.Context, provider vsa.Provider, parentVolumeUUID, parentSnapshotUUID string) (int64, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "Resolving FlexClone shared bytes from parent snapshot")
	if parentVolumeUUID == "" {
		logger.Warnf("flexclone parentVolume UUID missing in clone metadata; sharedBytes=0")
		return 0, nil
	}
	if parentSnapshotUUID == "" {
		logger.Warnf("flexclone parentSnapshot UUID missing in clone metadata; sharedBytes=0")
		return 0, nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting parent snapshot %s from ONTAP", parentSnapshotUUID))
	snapResp, err := provider.GetSnapshot(parentSnapshotUUID, parentVolumeUUID)
	if err != nil {
		return 0, err
	}
	if snapResp == nil {
		return 0, fmt.Errorf("nil snapshot response for parent snapshot uuid %s", parentSnapshotUUID)
	}
	return snapResp.LogicalSizeInBytes, nil
}

// CheckVolumeDeletedInOntap checks if a volume is deleted in ONTAP.
// If the volume is found, it returns an error to trigger activity retry.
// If the volume is not found, it returns nil (success) indicating deletion is complete.
func (a *ExpertModeVolumeActivity) CheckVolumeDeletedInOntap(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider to check deletion of volume %s", volume.Name))

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get ONTAP provider from node: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var svmName string
	if volume.Svm != nil {
		svmName = volume.Svm.Name
	}

	getVolumeParams := vsa.GetVolumeParams{
		VolumeName: volume.Name,
		SvmName:    svmName,
		IsRestore:  false,
	}

	recordHeartbeat(ctx, fmt.Sprintf("Polling ONTAP deletion status for volume %s", volume.Name))
	volumeResponse, err := provider.GetVolumeForExpertMode(getVolumeParams)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			// Volume is not found - deletion is complete, return success
			logger.Infof("Volume %s not found in ONTAP, deletion is complete", volume.Name)
			return nil
		}
		// Other errors (network, etc.) should be retried
		logger.Errorf("Failed to get volume %s from ONTAP: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Volume is still found - return error to trigger activity retry
	logger.Infof("Volume %s still exists in ONTAP (UUID: %s), deletion may be in progress. Will retry.", volume.Name, volumeResponse.ExternalUUID)
	return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("volume %s still exists in ONTAP, deletion may be in progress", volume.Name)))
}

// UpdateExpertModeVolumeStateInDB updates the lifecycle state of an expert mode volume by UUID.
// Only state is updated; Style is not changed (Style is a volume type/display attribute, not state details).
func (a *ExpertModeVolumeActivity) UpdateExpertModeVolumeStateInDB(ctx context.Context, volumeUUID, state string) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Getting expert mode volume %s from DB", volumeUUID))
	volume, err := a.SE.GetExpertModeVolumeByUUID(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get expert mode volume by UUID %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	volume.State = state

	recordHeartbeat(ctx, fmt.Sprintf("Updating expert mode volume %s state in DB", volumeUUID))
	_, err = a.SE.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Errorf("Failed to update expert mode volume state for UUID %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated expert mode volume state to %s for UUID %s", state, volumeUUID)
	return nil
}

// UpdateExpertModeVolumeInDB updates the expert mode volume in the database using UpdateExpertModeVolume.
func (a *ExpertModeVolumeActivity) UpdateExpertModeVolumeInDB(ctx context.Context, volume *datamodel.ExpertModeVolumes) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Updating volume %s state in DB", volume.Name))

	updatedVolume, err := a.SE.UpdateExpertModeVolume(ctx, volume)
	if err != nil {
		logger.Errorf("Failed to update expert mode volume %s: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated expert mode volume %s (UUID: %s) with state=%s, size=%d, style=%s, state=%s",
		updatedVolume.Name, updatedVolume.UUID, updatedVolume.State, updatedVolume.SizeInBytes, updatedVolume.Style, updatedVolume.State)

	return nil
}

// DeleteExpertModeVolumeInDB soft deletes the expert mode volume in the database.
// It sets the DeletedAt timestamp and updates the state to deleted.
func (a *ExpertModeVolumeActivity) DeleteExpertModeVolumeInDB(ctx context.Context, volumeUUID string) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Deleting volume %s in DB", volumeUUID))

	err := a.SE.DeleteExpertModeVolume(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to delete expert mode volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully deleted expert mode volume (UUID: %s)", volumeUUID)

	return nil
}

// ValidateONTAPVolumeUpdate fetches the volume from ONTAP by external UUID and succeeds when the name matches
// and ONTAP size is within expertModeOntapSizeToleranceBytes of the DB size (symmetric; all styles).
func (a *ExpertModeVolumeActivity) ValidateONTAPVolumeUpdate(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (*datamodel.ExpertModeVolumes, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP volume %s for update validation", volume.Name))

	ontapVolume, err := fetchOntapVolumeByUUID(ctx, volume, node)
	if err != nil {
		return nil, err
	}

	// Name must match ONTAP; size must be within expertModeOntapSizeToleranceBytes of DB (absolute delta, either direction; all styles—see expertModeVolumeSizesMatchForValidation).
	if expertModeVolumeSizesMatchForValidation(volume, ontapVolume) && ontapVolume.Name == volume.Name {
		logger.Infof("ONTAP volume update validated successfully for volume %s (UUID: %s): SizeInBytes=%d, name=%s ontapVolume.State:%s",
			ontapVolume.Name, ontapVolume.ExternalUUID, ontapVolume.Size, ontapVolume.Name, ontapVolume.State)
		volume.SizeInBytes = ontapVolume.Size
		return volume, nil
	}

	logger.Infof("Volume %s still not updated in ONTAP (UUID: %s), update may be in progress. Will retry.", volume.Name, volume.ExternalUUID)
	return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("Volume %s still not updated in ONTAP (UUID: %s), update may be in progress. Will retry.", volume.Name, volume.ExternalUUID)))
}

// FetchOntapVolumeByUUID fetches a volume from ONTAP by external UUID and merges ONTAP fields into a datamodel volume.
func (a *ExpertModeVolumeActivity) FetchOntapVolumeByUUID(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (*datamodel.ExpertModeVolumes, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP volume by UUID for external UUID: %s and Name : %s", volume.ExternalUUID, volume.Name))

	logger.Debugf("Fetching volume from ONTAP Name: %s, external UUID: %s, UUID: %s", volume.Name, volume.ExternalUUID, volume.UUID)
	ontapVolume, err := fetchOntapVolumeByUUID(ctx, volume, node)
	if err != nil {
		logger.Errorf("Failed to fetch volume from ONTAP: %v", err)
		return nil, err
	}
	logger.Debugf("Successfully fetched volume from ONTAP Name: %s, external UUID: %s, size: %d", ontapVolume.Name, ontapVolume.ExternalUUID, ontapVolume.Size)
	return convertOntapToONTAPModeVol(ontapVolume, volume), nil
}

func convertOntapToONTAPModeVol(ontapVol *vsa.VolumeResponse, dbVolume *datamodel.ExpertModeVolumes) *datamodel.ExpertModeVolumes {
	var volume datamodel.ExpertModeVolumes

	volume.Name = ontapVol.Name
	volume.SizeInBytes = ontapVol.Size
	volume.ExternalUUID = dbVolume.ExternalUUID
	volume.UUID = dbVolume.UUID
	volume.Svm = dbVolume.Svm
	volume.Style = dbVolume.Style
	volume.State = datamodel.LifeCycleStateAvailable
	volume.Description = dbVolume.Description
	volume.AccountID = dbVolume.AccountID
	volume.PoolID = dbVolume.PoolID
	volume.SvmID = dbVolume.SvmID
	volume.Account = dbVolume.Account
	volume.Pool = dbVolume.Pool
	volume.BackupConfig = dbVolume.BackupConfig
	volume.VolumeAttributes = dbVolume.VolumeAttributes

	return &volume
}

// UpdateExpertModeVolumeBackupConfigInDB persists the BackupConfig on an expert mode volume.
func (a *ExpertModeVolumeActivity) UpdateExpertModeVolumeBackupConfigInDB(ctx context.Context, volume *datamodel.ExpertModeVolumes) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Updating backup configuration for expert mode volume %s in DB", volume.UUID))
	if err := a.SE.UpdateExpertModeVolumeDataProtection(ctx, volume); err != nil {
		logger.Errorf("Failed to update backup config for expert mode volume %s: %v", volume.UUID, err)
		return err
	}
	logger.Infof("Successfully updated backup config for expert mode volume %s", volume.UUID)
	return nil
}

func _fetchOntapVolumeByUUID(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (*vsa.VolumeResponse, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for volume %s lookup", volume.Name))

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get ONTAP provider from node: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var svmName string
	if volume.Svm != nil {
		svmName = volume.Svm.Name
	}

	getVolumeParams := vsa.GetVolumeParams{
		SvmName:   svmName,
		IsRestore: false,
		UUID:      volume.ExternalUUID,
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting volume %s by UUID from ONTAP", volume.Name))
	volumeResponse, err := provider.GetVolumeForExpertMode(getVolumeParams)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.Infof("Volume external UUID : %s not found in ONTAP, will retry", volume.UUID)
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err))
		}
		logger.Errorf("Failed to get volume external UUID : %s from ONTAP: %v", volume.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if !expertModeVolumeSizesMatchForValidation(volume, volumeResponse) {
		logger.Infof("Volume %s (external UUID %s) ONTAP size %d not within 1 GB of DB size %d (state=%q, style=%q), will retry",
			volume.Name, volumeResponse.ExternalUUID, volumeResponse.Size, volume.SizeInBytes, volumeResponse.State, volumeResponse.Style)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("volume %s ONTAP size %d not within 1 GB of DB size %d, still provisioning", volume.Name, volumeResponse.Size, volume.SizeInBytes)))
	}

	logger.Infof("Volume external UUID : %s found in ONTAP", volumeResponse.ExternalUUID)
	return volumeResponse, nil
}

// _fetchOntapCloneVolumeByUUID loads a volume from ONTAP by external UUID including clone.* fields (FlexClone split polling, clone metadata).
func _fetchOntapCloneVolumeByUUID(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (*vsa.VolumeResponse, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider for clone volume %s lookup", volume.Name))

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get ONTAP provider from node: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var svmName string
	if volume.Svm != nil {
		svmName = volume.Svm.Name
	}

	getVolumeParams := vsa.GetVolumeParams{
		SvmName:   svmName,
		IsRestore: false,
		UUID:      volume.ExternalUUID,
	}

	recordHeartbeat(ctx, fmt.Sprintf("Getting clone volume %s by UUID from ONTAP", volume.Name))
	volumeResponse, err := provider.GetCloneVolumeForExpertMode(getVolumeParams)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.Infof("Clone volume external UUID : %s not found in ONTAP, will retry", volume.UUID)
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err))
		}
		logger.Errorf("Failed to get clone volume external UUID : %s from ONTAP: %v", volume.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Clone volume external UUID : %s found in ONTAP", volumeResponse.ExternalUUID)
	return volumeResponse, nil
}

// WaitForExpertModeFlexCloneSplitComplete performs one ONTAP poll for split status. The workflow retries this activity
// (see volume_flexclone_split_workflow) until split completes or ScheduleToClose elapses.
// On success, it returns the latest ONTAP size for the split volume.
func (a *ExpertModeVolumeActivity) WaitForExpertModeFlexCloneSplitComplete(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) (int64, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("fetching clone volume %s for split poll", volume.Name))

	resp, err := fetchOntapCloneVolumeByUUID(ctx, volume, node)
	if err != nil {
		ce := vsaerrors.ExtractCustomError(err)
		if ce != nil && ce.IsError(vsaerrors.ErrResourceNotFound) {
			logger.Errorf("Clone volume not found in ONTAP while waiting for flexclone split: %v", err)
			return 0, vsaerrors.WrapAsNonRetryableTemporalApplicationError(ce)
		}
		logger.Warnf("Failed to fetch volume from ONTAP while waiting for split (will retry): %v", err)
		return 0, err
	}

	// Split complete: is_flexclone sent as false (non-nil pointer).
	if c := resp.Clone; c != nil && c.IsFlexclone != nil && !*c.IsFlexclone {
		logger.Infof("Flexclone split complete for volume %s (is_flexclone=false)", volume.Name)
		return resp.Size, nil
	}

	// Split abandoned: both fields present, split_initiated false and is_flexclone still true.
	if c := resp.Clone; c != nil &&
		c.SplitInitiated != nil && !*c.SplitInitiated &&
		c.IsFlexclone != nil && *c.IsFlexclone {
		err := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, errors.New("flexclone split was aborted or stopped on ONTAP before completion"))
		return 0, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}

	return 0, temporal.NewApplicationError("flexclone split still in progress on ONTAP", "FlexCloneSplitPending")
}

// RecoverExpertModeVolumeAfterFlexCloneSplitFailure sets volume state to AVAILABLE when polling for split
// fails (e.g. split aborted on ONTAP, poll timeout), and refreshes SharedBytes from ONTAP clone metadata.
// Successful split completion is handled by
// CompleteExpertModeFlexCloneSplitInDB (AVAILABLE + isFlexclone cleared).
func (a *ExpertModeVolumeActivity) RecoverExpertModeVolumeAfterFlexCloneSplitFailure(ctx context.Context, volume *datamodel.ExpertModeVolumes, node *models.Node) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Getting expert mode volume %s from DB for split recovery", volume.UUID))
	vol, err := a.SE.GetExpertModeVolumeByUUID(ctx, volume.UUID)
	if err != nil {
		logger.Errorf("Failed to load volume for recovery: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching clone volume %s from ONTAP for split recovery", vol.Name))
	cloneResp, err := fetchOntapCloneVolumeByUUID(ctx, vol, node)
	if err != nil {
		logger.Errorf("Failed to re-fetch clone volume from ONTAP during recovery: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	sharedBytes := int64(0)
	if c := cloneResp.Clone; c != nil && c.ParentVolumeUUID != "" && c.ParentSnapshotUUID != "" {
		recordHeartbeat(ctx, fmt.Sprintf("Getting ONTAP provider to recover clone volume %s", vol.Name))
		provider, providerErr := vsa.GetProviderByNode(ctx, node)
		if providerErr != nil {
			logger.Errorf("Failed to get ONTAP provider from node for recovery: %v", providerErr)
			return vsaerrors.WrapAsTemporalApplicationError(providerErr)
		}
		sharedBytes, err = resolveExpertModeFlexcloneSharedBytes(ctx, provider, c.ParentVolumeUUID, c.ParentSnapshotUUID)
		if err != nil {
			logger.Errorf("Failed to recalculate shared bytes during recovery for volume %s: %v", vol.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	vol.SharedBytes = sharedBytes
	vol.State = datamodel.LifeCycleStateAvailable
	recordHeartbeat(ctx, fmt.Sprintf("Persisting split recovery for expert mode volume %s in DB", vol.UUID))
	if _, err := a.SE.UpdateExpertModeVolume(ctx, vol); err != nil {
		logger.Errorf("Failed to persist volume recovery state: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// CompleteExpertModeFlexCloneSplitInDB clears FlexClone flags after ONTAP reports split complete
// and persists the split volume size returned by the split-wait activity.
func (a *ExpertModeVolumeActivity) CompleteExpertModeFlexCloneSplitInDB(ctx context.Context, volumeUUID string, splitVolumeSize int64) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Getting expert mode volume %s from DB for split completion", volumeUUID))
	vol, err := a.SE.GetExpertModeVolumeByUUID(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to load volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	vol.SizeInBytes = splitVolumeSize

	if vol.VolumeAttributes == nil {
		vol.VolumeAttributes = &datamodel.ExpertModeVolumeAttributes{}
	}
	vol.VolumeAttributes.IsFlexclone = false
	vol.SharedBytes = 0
	vol.VolumeAttributes.Clone = nil
	vol.State = datamodel.LifeCycleStateAvailable

	recordHeartbeat(ctx, fmt.Sprintf("Persisting completed FlexClone split for expert mode volume %s in DB", volumeUUID))
	if _, err := a.SE.UpdateExpertModeVolume(ctx, vol); err != nil {
		logger.Errorf("Failed to update volume after flexclone split: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

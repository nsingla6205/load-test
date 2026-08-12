package vsa

import (
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// Error message constants
const (
	// ErrMsgVolumeMaxSizeExceeded is the error message pattern returned by ONTAP when volume size exceeds maximum
	ErrMsgVolumeMaxSizeExceeded = "failed because the resulting volume size is greater than the maximum size"
	// ErrMsgVolumeSizeTooSmall is the error message pattern returned by ONTAP when volume size is too small to hold current data
	ErrMsgVolumeSizeTooSmall = "Selected volume size is too small to hold the current volume data"
	// ErrMsgMaximumVolumeCountOnNode is the error message pattern when ONTAP node hits per-node volume limit
	ErrMsgMaximumVolumeCountOnNode = "maximum number of volumes reached on node"
)

var (
	enableCloneInfoRefresh = env.GetBool("ENABLE_CLONE_INFO_REFRESH", false)
)

// CreateVolume creates a volume by calling the ONTAP REST Client
func (rc *OntapRestProvider) CreateVolume(params CreateVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	volumeCreateParams := &ontapRest.VolumeCreateParams{
		Name:                           params.VolumeName,
		Type:                           params.VolumeType,
		Size:                           params.Size,
		Svm:                            params.SvmName,
		Aggregates:                     params.Aggregates,
		ConstituentsPerAggregate:       params.ConstituentsPerAggregate,
		Style:                          params.Style,
		SnapshotPolicy:                 params.SnapshotPolicyName,
		SnapshotReservePercent:         params.SnapReserve,
		SnapshotDirectoryAccessEnabled: params.SnapshotDirectory,
		ExportPolicy:                   params.ExportPolicy,
		JunctionPath:                   params.JunctionPath,
		TieringSupported:               params.TieringSupported,
	}
	if params.QosPolicy != nil {
		volumeCreateParams.QosPolicy = *params.QosPolicy
	}
	if params.SecurityStyle != nil && *params.SecurityStyle != "" {
		volumeCreateParams.SecurityStyle = *params.SecurityStyle
	}
	if params.UnixPermissions != nil && *params.UnixPermissions != "" {
		volumeCreateParams.UnixPermissions = params.UnixPermissions
	}
	if params.RestoreFromSnapshot != nil && params.RestoreFromSnapshot.SnapshotUUID != "" {
		volumeCreateParams.RestoreFromSnapshot = &ontapRest.RestoreFromSnapshotParams{
			ParentVolumeExternalUUID: params.RestoreFromSnapshot.ParentVolumeExternalUUID,
			ParentVolumeName:         params.RestoreFromSnapshot.ParentVolumeName,
			SnapshotUUID:             params.RestoreFromSnapshot.SnapshotUUID,
			SnapshotName:             params.RestoreFromSnapshot.SnapshotName,
			ParentVolumeSvmName:      params.RestoreFromSnapshot.ParentVolumeSvmName,
		}
		volumeCreateParams.Size = 0
	}

	if params.TieringPolicy != nil {
		volumeCreateParams.TieringPolicy = &ontapRest.TieringPolicy{
			CoolAccessTieringPolicy: params.TieringPolicy.CoolAccessTieringPolicy,
			MinCoolingDays:          params.TieringPolicy.CoolnessPeriod,
			CloudRetrievalPolicy:    params.TieringPolicy.CoolAccessRetrievalPolicy,
			CloudWriteModeEnabled:   params.TieringPolicy.CloudWriteModeEnabled,
		}
	}

	vol, job, err := client.Storage().VolumeCreate(volumeCreateParams)
	if err != nil {
		if strings.Contains(err.Error(), "Duplicate volume name") {
			return nil, errors.NewConflictErr(params.VolumeName + " already exists")
		}
		if strings.Contains(err.Error(), "Maximum clone hierarchy") {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrNestedCloneLimitExceeded, err))
		}
		// ONTAP error format: "Request to create volume ... failed because there is not enough space in aggregate ..."
		// This occurs when hot tier is full even though pool has capacity
		if strings.Contains(err.Error(), "not enough space") || strings.Contains(err.Error(), "insufficient space") {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHotTierCapacityExhausted, errors.NewUserInputValidationErr("The hot tier (local storage) capacity is full. Free up space in the hot tier or enable hot tier auto-resize to automatically expand the hot tier when needed.")))
		}
		// Check for parent volume not found error when restoring from snapshot
		// ONTAP error format: "Volume \"parentvol2\" in SVM \"svm-name\" does not exist."
		if strings.Contains(err.Error(), "Volume") && strings.Contains(err.Error(), "does not exist") && params.RestoreFromSnapshot != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrParentVolumeNotFound, errors.NewUserInputValidationErr("Cannot create volume from snapshot: parent volume does not exist. Please verify that the parent volume is available and try again.")))
		}
		// ONTAP error format: "Failed to create the clone volume. Reason: Maximum number of volumes reached on node ..."
		// This error occurs when ONTAP node has hit its per-node maximum volume count
		if strings.Contains(strings.ToLower(err.Error()), ErrMsgMaximumVolumeCountOnNode) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMaximumVolumeCount, err))
		}
		return nil, err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			// ONTAP error format: "Request to create volume ... failed because there is not enough space in aggregate ..."
			// This occurs when hot tier is full even though pool has capacity
			if strings.Contains(err.Error(), "not enough space") || strings.Contains(err.Error(), "insufficient space") {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHotTierCapacityExhausted, errors.NewUserInputValidationErr("The hot tier (local storage) capacity is full. Free up space in the hot tier or enable hot tier auto-resize to automatically expand the hot tier when needed.")))
			}
			if strings.Contains(strings.ToLower(err.Error()), ErrMsgMaximumVolumeCountOnNode) {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMaximumVolumeCount, err))
			}
			return nil, err
		}
	}

	// Validate the Volume response to avoid nil pointer dereferences
	if vol == nil {
		return nil, errors.New("invalid Volume response from API: volume is nil")
	}
	if vol.Name == nil {
		return nil, errors.New("invalid Volume response from API: name is nil")
	}
	if vol.UUID == nil {
		return nil, errors.New("invalid Volume response from API: UUID is nil")
	}

	// Perform additional GET call if state is nil (necessary workaround for large FlexGroup volumes with many constituents)
	if vol.State == nil {
		getVol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
			UUID: *vol.UUID,
			Name: *vol.Name,
			BaseParams: ontapRest.BaseParams{
				Fields: []string{"state", "space.*", "size", "constituents"},
			},
		})
		if err != nil {
			return nil, err
		}
		if getVol == nil || getVol.State == nil {
			return nil, errors.New("invalid Volume response from API: state is nil")
		}
		vol = getVol
	}

	// When AllSquash is enabled with an explicit AnonUid, ONTAP requires a post-create
	// VolumeModify to assign nas.uid so the volume root directory inode reflects the correct
	// owner UID. ONTAP does not accept nas.uid during volume create.
	if params.AllSquashUid != nil {
		success, job, modifyErr := client.Storage().VolumeModify(&ontapRest.VolumeModifyParams{
			UUID:   *vol.UUID,
			NasUid: params.AllSquashUid,
		})
		if modifyErr != nil {
			return nil, fmt.Errorf("volume created but failed to set NAS UID for AllSquash: %w", modifyErr)
		}
		if !success && job != nil {
			if modifyErr = client.Poll(job.JobUUID); modifyErr != nil {
				return nil, fmt.Errorf("volume created but NAS UID job failed for AllSquash: %w", modifyErr)
			}
		}
	}

	// Return the created volume
	volRes := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		State: *vol.State,
	}

	// adding nil pointer checks as in some cases it may not be populated like FlexGroup volumes with large number of constituents
	if vol.Space != nil {
		if vol.Space.Available != nil {
			volRes.AvailableSpace = *vol.Space.Available
		}
		if vol.Space.SizeAvailableForSnapshots != nil {
			volRes.SnapReserve = *vol.Space.SizeAvailableForSnapshots
		}
		if vol.Space.AfsTotal != nil {
			volRes.AFSSize = *vol.Space.AfsTotal
		}
		if vol.Space.Metadata != nil {
			volRes.MetadataSize = *vol.Space.Metadata
		}
	}
	if vol.Size != nil {
		volRes.Size = *vol.Size
	}

	// Extract constituent count from the created volume
	if vol.ConstituentCount != nil {
		count := int32(*vol.ConstituentCount)
		volRes.ConstituentCount = &count
	} else if vol.VolumeInlineConstituents != nil {
		// If constituent_count is not available but constituents array is available (even if empty), use the length
		count := int32(len(vol.VolumeInlineConstituents))
		volRes.ConstituentCount = &count
	}

	return volRes, nil
}

// DeleteVolume deletes a volume by calling the ONTAP REST Client
// It polls the job internally if the deletion is async, similar to CreateVolume
func (rc *OntapRestProvider) DeleteVolume(volumeUUID, volumeName string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}

	// First, get the volume details to check if it's in use for splitting or has clones
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "clone.*", "snapmirror.*"},
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "entry doesn't exist") || strings.Contains(err.Error(), "entry not found") || strings.Contains(err.Error(), "UUID and Name parameters cannot be empty when querying for a volume") {
			return nil
		}
		return err
	}
	// Check if the volume is in use for splitting or has clones
	if vol != nil && vol.Clone != nil {
		// Check if volume has FlexClone volumes or a split is initiated
		if (vol.Clone.HasFlexclone != nil && *vol.Clone.HasFlexclone) ||
			(vol.Clone.SplitInitiated != nil && *vol.Clone.SplitInitiated) {
			return vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeWhenInSplitState, errors.New("Cannot delete a volume that is being actively used in a Volume Replication relationship or a file clone split triggered by Snapshot RestoreFiles operation or used as a reference snapshot for a backup"))
		}
	}

	// If all checks pass, proceed with volume deletion
	job, err := client.Storage().VolumeDelete(&ontapRest.VolumeDeleteParams{
		UUID: volumeUUID,
		Name: volumeName,
	})

	if err != nil {
		return err
	}

	// Poll the job if it exists
	if job != nil {
		if err = client.Poll(job.JobUUID); err != nil {
			return err
		}
	}

	return nil
}

func (rc *OntapRestProvider) UnmountVolume(volumeUUID string) (*OntapAsyncResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	jobAccepted, err := client.Storage().VolumeUnmount(&ontapRest.VolumeUnmountParams{
		UUID: volumeUUID,
	})
	if err != nil {
		return nil, err
	}

	if jobAccepted != nil {
		return &OntapAsyncResponse{JobUUID: jobAccepted.JobUUID}, nil
	}

	return nil, nil
}

func (rc *OntapRestProvider) MountVolume(params MountVolumeParams) (*OntapAsyncResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	jobAccepted, err := client.Storage().VolumeMount(&ontapRest.VolumeMountParams{
		UUID:         params.UUID,
		JunctionPath: params.JunctionPath,
	})
	if err != nil {
		return nil, err
	}

	// Poll the job if it exists
	if jobAccepted != nil {
		if err = client.Poll(jobAccepted.JobUUID); err != nil {
			return nil, err
		}
	}

	return &OntapAsyncResponse{JobUUID: jobAccepted.JobUUID}, nil
}

// GetVolume returns a volume by calling the ONTAP REST Client
func (rc *OntapRestProvider) GetVolume(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:                           params.UUID,
		Name:                           params.VolumeName,
		SvmName:                        &params.SvmName,
		SnapshotDirectoryAccessEnabled: &params.SnapshotDirectory,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "space.*", "state", "snapshot_policy.name", "type", "snapshot_directory_access_enabled", "constituents"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Name == nil || vol.UUID == nil || vol.Space == nil {
		return nil, errors.NewNotFoundErr("volume", nil)
	}
	if !params.IsRestore && vol.Space.LogicalSpace == nil {
		return nil, errors.NewNotFoundErr("volume", nil)
	}
	usedBytes := int64(0)
	if vol.Space.LogicalSpace != nil && vol.Space.LogicalSpace.Used != nil {
		usedBytes = nillable.FromPointer(vol.Space.LogicalSpace.Used)
	}
	volType := ""
	if vol.Type != nil {
		volType = *vol.Type
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		AvailableSpace: *vol.Space.Available,
		Size:           *vol.Space.Size,
		State:          *vol.State,
		UsedBytes:      usedBytes,
		// by default, volume will always have none as the snapshot policy
		SnapshotPolicyName:             *vol.SnapshotPolicy.Name,
		Type:                           volType,
		SnapshotDirectoryAccessEnabled: *vol.SnapshotDirectoryAccessEnabled,
	}
	if vol.Space.SizeAvailableForSnapshots != nil {
		res.SnapReserve = *vol.Space.SizeAvailableForSnapshots
	}
	if vol.Space.Metadata != nil {
		res.MetadataSize = *vol.Space.Metadata
	}
	if vol.Space.AfsTotal != nil {
		res.AFSSize = *vol.Space.AfsTotal
	}
	if vol.VolumeInlineConstituents != nil {
		if len(vol.VolumeInlineConstituents) > 0 {
			count := int32(len(vol.VolumeInlineConstituents))
			res.ConstituentCount = &count
		} else {
			zero := int32(0)
			res.ConstituentCount = &zero
		}
	}
	return res, nil
}

func (rc *OntapRestProvider) GetVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:    params.UUID,
		Name:    params.VolumeName,
		SvmName: &params.SvmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "state", "type", "size", "style"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Name == nil || vol.UUID == nil || vol.Size == nil || vol.State == nil {
		return nil, fmt.Errorf("GetVolumeForExpertMode: incomplete ONTAP volume response: missing required fields (name/uuid/size/state)")
	}
	volStyle := ""
	if vol.Style != nil {
		volStyle = *vol.Style
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Size:  *vol.Size,
		State: *vol.State,
		Style: volStyle,
	}
	return res, nil
}

// GetCloneVolumeForExpertMode fetches clone metadata for expert mode workflows.
func (rc *OntapRestProvider) GetCloneVolumeForExpertMode(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID:    params.UUID,
		Name:    params.VolumeName,
		SvmName: &params.SvmName,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"uuid", "name", "state", "type", "size", "style", "clone.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol.Name == nil || vol.UUID == nil || vol.Size == nil || vol.State == nil {
		return nil, fmt.Errorf("GetCloneVolumeForExpertMode: incomplete ONTAP volume response: missing required fields (name/uuid/size/state)")
	}
	volStyle := ""
	if vol.Style != nil {
		volStyle = *vol.Style
	}
	res := &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Size:  *vol.Size,
		State: *vol.State,
		Style: volStyle,
	}
	if vol.Clone != nil {
		res.Clone = &VolumeResponseClone{}
		if vol.Clone.ParentVolume != nil {
			res.Clone.ParentVolumeName = nillable.FromPointer(vol.Clone.ParentVolume.Name)
			res.Clone.ParentVolumeUUID = nillable.FromPointer(vol.Clone.ParentVolume.UUID)
		}
		if vol.Clone.ParentSnapshot != nil {
			res.Clone.ParentSnapshotName = nillable.FromPointer(vol.Clone.ParentSnapshot.Name)
			res.Clone.ParentSnapshotUUID = nillable.FromPointer(vol.Clone.ParentSnapshot.UUID)
		}
		if vol.Clone.IsFlexclone != nil {
			res.Clone.IsFlexclone = vol.Clone.IsFlexclone
		}
		if vol.Clone.SplitInitiated != nil {
			res.Clone.SplitInitiated = vol.Clone.SplitInitiated
		}
		if vol.Clone.SplitCompletePercent != nil {
			res.Clone.SplitCompletePercent = vol.Clone.SplitCompletePercent
		}
	}
	return res, nil
}

func (rc *OntapRestProvider) GetVolumes() ([]*Volume, error) {
	var resultVolumes []*Volume

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	ucbf := func(volumes []*ontapRest.Volume) error {
		for _, volume := range volumes {
			vol := &Volume{
				Volume: models.Volume{
					Name:      volume.Name,
					UUID:      volume.UUID,
					Svm:       volume.Svm,
					IsSvmRoot: volume.IsSvmRoot,
					Style:     volume.Style,
					Space:     volume.Space,
					Type:      volume.Type,
					Clone:     volume.Clone,
				},
				ExternalUUID: *volume.UUID,
			}
			resultVolumes = append(resultVolumes, vol)
		}
		return nil
	}

	// Build base fields list
	fields := []string{"uuid", "name", "space.*", "svm", "is_svm_root", "style", "type", "clone.split_complete_percent", "clone.is_flexclone"}

	// Conditionally add clone fields if feature flag is enabled
	if enableCloneInfoRefresh {
		fields = append(fields, "clone.parent_snapshot.name", "clone.parent_volume.name")
	}

	err = client.Storage().VolumeCollectionGet(&ontapRest.VolumeCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields: fields,
		},
	}, ucbf)

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultVolumes, nil
}

// GetVolumesForInventory returns the volume fields needed to build project
// inventory, scoped to SVMs matching svmNamePattern.
func (rc *OntapRestProvider) GetVolumesForInventory(svmNamePattern string) ([]*Volume, error) {
	var resultVolumes []*Volume

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	collectVolumes := func(volumes []*ontapRest.Volume) error {
		for _, volume := range volumes {
			if volume == nil {
				continue
			}
			resultVolumes = append(resultVolumes, &Volume{
				Volume:       volume.Volume,
				ExternalUUID: nillable.GetString(volume.UUID, ""),
			})
		}
		return nil
	}

	const maxInventoryRecords int64 = 10000
	err = client.Storage().VolumeCollectionGet(&ontapRest.VolumeCollectionGetParams{
		BaseParams: ontapRest.BaseParams{
			Fields:     []string{"name", "uuid", "svm.name", "svm.uuid", "space.physical_used", "space.size", "aggregates.name"},
			MaxRecords: nillable.ToPointer(maxInventoryRecords),
		},
		SvmName: nillable.ToPointer(svmNamePattern),
	}, collectVolumes)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	return resultVolumes, nil
}

func (rc *OntapRestProvider) UpdateVolume(params UpdateVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:        params.UUID,
		SnapReserve: params.SnapReserve,
	}

	if params.TieringPolicy != nil {
		volumeModifyParams.TieringPolicy = &ontapRest.TieringPolicy{
			CoolAccessTieringPolicy: params.TieringPolicy.CoolAccessTieringPolicy,
			MinCoolingDays:          params.TieringPolicy.CoolnessPeriod,
			CloudRetrievalPolicy:    params.TieringPolicy.CoolAccessRetrievalPolicy,
			CloudWriteModeEnabled:   params.TieringPolicy.CloudWriteModeEnabled,
		}
	}

	if params.InitiateSplit {
		volumeModifyParams.SplitInitiated = &params.InitiateSplit
		volumeModifyParams.MatchParentStorageTier = false // TODO: add this `params.TieringPolicy == "auto"`, when autotier is supported
	} else {
		if params.Size != 0 {
			volumeModifyParams.Size = nillable.ToPointer(uint64(params.Size))
		}
		volumeModifyParams.SnapshotPolicyName = nillable.GetStringPtr(params.SnapshotPolicyName)
	}

	if params.ExportPolicy != nil {
		volumeModifyParams.ExportPolicy = params.ExportPolicy
	}
	if params.JunctionPath != nil {
		volumeModifyParams.Path = params.JunctionPath
	}
	if params.SnapshotDirectoryAccess != nil {
		volumeModifyParams.SnapshotDirectoryAccessEnabled = params.SnapshotDirectoryAccess
	}

	// Handle QoS policy assignment/unassignment
	// If QosPolicyName is provided (not nil), set it:
	// - Use "none" to unassign (no policy) per ONTAP API specification
	// - Use policy name to assign that policy
	// - Empty string will be rejected by ONTAP with an appropriate error
	// If QosPolicyName is nil, don't change the policy
	if params.QosPolicyName != nil {
		volumeModifyParams.QosPolicy = params.QosPolicyName
	}

	if params.UnixPermissions != nil {
		volumeModifyParams.UnixPermissions = params.UnixPermissions
	}

	if params.NasUid != nil {
		volumeModifyParams.NasUid = params.NasUid
	}

	err = handleVolumeCloudWriteModeDisableIfProvided(client, volumeModifyParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	success, job, err := client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		// Check for maximum volume size error
		if strings.Contains(err.Error(), ErrMsgVolumeMaxSizeExceeded) {
			// Extract the maximum size from the error message
			maxSizeStart := strings.Index(err.Error(), "The maximum possible size is ")
			if maxSizeStart > 0 {
				maxSizeStr := err.Error()[maxSizeStart:]
				// Create a sanitized error message without exposing SVM name
				sanitizedError := fmt.Sprintf("Volume size exceeds the maximum allowed size. %s", maxSizeStr)
				return vsaerrors.NewVCPError(vsaerrors.ErrVolumeExceedsMaximumSize,
					errors.NewUserInputValidationErr(sanitizedError))
			}
		}
		// Check for volume size too small error
		if strings.Contains(err.Error(), ErrMsgVolumeSizeTooSmall) {
			// Extract the minimum required size from the error message if available
			minSizeStart := strings.Index(err.Error(), "New volume size must be at least ")
			var sanitizedError string
			if minSizeStart >= 0 {
				// Extract the minimum size requirement
				minSizeEnd := strings.Index(err.Error()[minSizeStart:], " to hold")
				if minSizeEnd > 0 {
					minSizeStr := err.Error()[minSizeStart : minSizeStart+minSizeEnd]
					sanitizedError = fmt.Sprintf("Selected volume size is too small to hold the current volume data. %s", minSizeStr)
				} else {
					sanitizedError = "Selected volume size is too small to hold the current volume data. Please increase the volume size."
				}
			} else {
				sanitizedError = "Selected volume size is too small to hold the current volume data. Please increase the volume size."
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrVolumeSizeTooSmall,
				errors.NewUserInputValidationErr(sanitizedError))
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if success || params.InitiateSplit { // Split operation can run in background without polling
		return nil
	}
	return client.Poll(job.JobUUID)
}

// InitiateSplitVolume sends the split-initiation request to ONTAP for the given volume UUID
// and returns the ONTAP job UUID that tracks the background data-movement operation.
// this method does NOT poll the job to completion — the caller is
// responsible for polling via GetOntapJob / WaitForONTAPJob.
func (rc *OntapRestProvider) InitiateSplitVolume(volumeUUID string) (string, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return "", err
	}
	splitInitiated := true
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:                   volumeUUID,
		SplitInitiated:         &splitInitiated,
		MatchParentStorageTier: false,
	}
	success, job, err := client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		return "", err
	}
	if success || job == nil {
		// ONTAP completed synchronously or returned no job — treat as success with no job UUID.
		return "", nil
	}
	return job.JobUUID, nil
}

// StopSplitVolume sends a stop-split request to ONTAP for the given volume UUID by
// PATCHing volume.split_initiated=false. The operation is synchronous from ONTAP's
// perspective: ONTAP halts the background data-movement, partial split progress is
// preserved, and the volume remains a thin clone of its parent. Callers should
// validate that a split is currently in progress before invoking this; ONTAP will
// return an error like "Operation is not in progress" otherwise.
func (rc *OntapRestProvider) StopSplitVolume(volumeUUID string) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	splitInitiated := false
	volumeModifyParams := &ontapRest.VolumeModifyParams{
		UUID:                   volumeUUID,
		SplitInitiated:         &splitInitiated,
		MatchParentStorageTier: false,
	}
	_, _, err = client.Storage().VolumeModify(volumeModifyParams)
	if err != nil {
		return err
	}
	return nil
}

// GetVolumeCloneInfo fetches just the clone-related fields for a volume identified
// by its UUID. Used by the synchronous splitStop path to read split progress
// (split_complete_percent, split_initiated) immediately before issuing the stop so
// the caller can return a meaningful CloneDetails in the API response.
func (rc *OntapRestProvider) GetVolumeCloneInfo(volumeUUID string) (*VolumeResponseClone, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"clone.*"},
		},
	})
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Clone == nil {
		return nil, nil
	}
	res := &VolumeResponseClone{
		SplitInitiated:       vol.Clone.SplitInitiated,
		SplitCompletePercent: vol.Clone.SplitCompletePercent,
	}
	if vol.Clone.ParentVolume != nil {
		res.ParentVolumeName = nillable.FromPointer(vol.Clone.ParentVolume.Name)
		res.ParentVolumeUUID = nillable.FromPointer(vol.Clone.ParentVolume.UUID)
	}
	if vol.Clone.ParentSnapshot != nil {
		res.ParentSnapshotName = nillable.FromPointer(vol.Clone.ParentSnapshot.Name)
		res.ParentSnapshotUUID = nillable.FromPointer(vol.Clone.ParentSnapshot.UUID)
	}
	if vol.Clone.IsFlexclone != nil {
		isFlexclone := *vol.Clone.IsFlexclone
		res.IsFlexclone = &isFlexclone
	}
	return res, nil
}

// UnassignQoSPolicyFromVolume unassigns the QoS policy from a volume by setting it to "none".
// This is a convenience function that wraps UpdateVolume with the correct "none" value
// as required by the ONTAP API specification.
func (rc *OntapRestProvider) UnassignQoSPolicyFromVolume(volumeUUID string) error {
	none := "none"
	params := UpdateVolumeParams{
		UUID:          volumeUUID,
		QosPolicyName: &none,
	}
	return rc.UpdateVolume(params)
}

func handleVolumeCloudWriteModeDisableIfProvided(client ontapRest.RESTClient, params *ontapRest.VolumeModifyParams) error {
	// For files volume, when changing ontap auto-tiering policy from all to auto/none/snapshot_only
	// policy along with disabling cloud write mode, ontap throws error. It is required to
	// disable the cloud write mode first and then change the policy.
	if params.TieringPolicy != nil &&
		params.TieringPolicy.CloudWriteModeEnabled != nil &&
		!*params.TieringPolicy.CloudWriteModeEnabled &&
		params.TieringPolicy.CoolAccessTieringPolicy != models.VolumeInlineTieringPolicyAll {
		success, job, err := client.Storage().VolumeModifyCloudWriteMode(params)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		// Omit cloud_write_enabled from the subsequent VolumeModify. Re-sending it is rejected
		// on SnapMirror DP read-only volumes (ONTAP error 917628) even after a successful
		// VolumeModifyCloudWriteMode call.
		params.TieringPolicy.CloudWriteModeEnabled = nil
		if success {
			return nil
		}
		return client.Poll(job.JobUUID)
	}

	return nil
}

func (rc *OntapRestProvider) RevertVolume(params RevertVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}

	revertParams := &ontapRest.VolumeModifyParams{
		UUID:                  params.VolumeID,
		RestoreToSnapshotUUID: nillable.GetStringPtr(params.SnapshotID),
	}

	done, jj, err := client.Storage().VolumeModify(revertParams)
	if err != nil {
		if strings.Contains(err.Error(), "Failed to restore snapshot") || strings.Contains(err.Error(), "Volume snap restore error") {
			return vsaerrors.NewVCPError(vsaerrors.ErrRevertVolumeWhenSnapshotInUse, errors.NewUserInputValidationErr("Cannot revert a Volume when snapshot is in use by the cloned volume"))
		}
		if errors.IsNotFoundErr(err) {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Volume", nil))
		}
		if strings.Contains(err.Error(), "Only a Snapshot copy of a read/write volume can be promoted") {
			return vsaerrors.NewVCPError(vsaerrors.ErrRevertReplicationDestinationVolume, errors.NewUserInputValidationErr("Cannot revert a Volume Replication Destination Volume"))
		}
		if strings.Contains(err.Error(), "entry doesn't exist") || strings.Contains(err.Error(), "entry not found") {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("Snapshot", nil))
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	if !done {
		if err = client.Poll(jj.JobUUID); err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
	}
	return nil
}

func (rc *OntapRestProvider) UpdateVolumeEnableEncryption(params UpdateVolumeParams) error {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return err
	}
	success, job, err := client.Storage().VolumeModify(
		&ontapRest.VolumeModifyParams{
			UUID:             params.UUID,
			EncryptionEnable: &params.EncryptionEnable,
		})
	if err != nil {
		return err
	}
	if success {
		return nil
	}
	return client.Poll(job.JobUUID)
}

// GetVolumeNASDetails fetches NAS-related properties (path, security style, export policy name)
// for a volume identified by its UUID.
func (rc *OntapRestProvider) GetVolumeNASDetails(volumeUUID string) (*VolumeNASDetails, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	vol, err := client.Storage().VolumeGet(&ontapRest.VolumeGetParams{
		UUID: volumeUUID,
		BaseParams: ontapRest.BaseParams{
			Fields: []string{"nas.path", "nas.security_style", "nas.export_policy"},
		},
	})
	if err != nil {
		return nil, err
	}

	result := &VolumeNASDetails{}
	if vol != nil && vol.Nas != nil {
		if vol.Nas.Path != nil {
			result.NASPath = *vol.Nas.Path
		}
		if vol.Nas.SecurityStyle != nil {
			result.SecurityStyle = *vol.Nas.SecurityStyle
		}
		if vol.Nas.ExportPolicy != nil && vol.Nas.ExportPolicy.Name != nil {
			result.ExportPolicyName = *vol.Nas.ExportPolicy.Name
		}
	}
	return result, nil
}

// GetVolumeSANDetails fetches SAN-related properties (LUN and NVMe namespace presence)
// for a volume identified by its SVM name and volume name.
func (rc *OntapRestProvider) GetVolumeSANDetails(svmName, volumeName string) (*VolumeSANDetails, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}

	result := &VolumeSANDetails{}

	luns, lunErr := client.SAN().LunGet(&ontapRest.LunGetParams{
		SvmName:    &svmName,
		VolumeName: &volumeName,
	})
	if lunErr != nil && !errors.IsNotFoundErrForObjectTypeInChain(lunErr, "lun") {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, lunErr)
	}
	result.HasLUNs = len(luns) > 0

	namespaces, nsErr := client.NVMe().NamespaceGet(&ontapRest.NvmeNamespaceGetParams{
		SvmName:    &svmName,
		VolumeName: &volumeName,
	})
	if nsErr != nil && !errors.IsNotFoundErrForObjectTypeInChain(nsErr, "nvme namespace") {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, nsErr)
	}
	result.HasNamespaces = len(namespaces) > 0

	return result, nil
}

func (rc *OntapRestProvider) GetVolumeEncryptionStatus(params GetVolumeParams) (*VolumeResponse, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	volumeGetParams := &ontapRest.VolumeGetParams{
		BaseParams: ontapRest.BaseParams{Fields: []string{"encryption.*"}},
		UUID:       params.UUID,
		Name:       params.VolumeName,
		SvmName:    &params.SvmName,
	}
	vol, err := client.Storage().VolumeGet(volumeGetParams)
	if err != nil {
		return nil, err
	}
	if vol == nil || vol.Name == nil || vol.UUID == nil {
		return nil, errors.NewNotFoundErr("volume", nil)
	}
	if vol.Encryption == nil {
		return nil, errors.New("Encryption field is not populated in Get Volume from VSA")
	}

	return &VolumeResponse{
		ProviderResponse: ProviderResponse{
			Name:         *vol.Name,
			ExternalUUID: *vol.UUID,
		},
		Encryption: Encryption{
			Enabled: vol.Encryption.Enabled,
			State:   vol.Encryption.State,
			Type:    vol.Encryption.Type,
		},
	}, nil
}

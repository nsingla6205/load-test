package vsa

import (
	"context"
	"strings"
	"time"

	"github.com/sosodev/duration"
	clientmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	doWaitForSnapmirrorState                  = waitForSnapmirrorState
	doEnsureSvmPeering                        = ensureSvmPeering
	doValidateAndDeleteVolumeReplication      = validateAndDeleteVolumeReplication
	doValidateResyncVolumeReplication         = validateResyncVolumeReplication
	listSnapmirrorDestinations                = _listSnapmirrorDestinations
	doCreateVolumeReplicationScheduleIfNeeded = createVolumeReplicationSchedule
	doUnmountVolume                           = _unmountVolume
	doCleanupSvmPeering                       = cleanupSvmPeering
	getSvmPeer                                = _getSvmPeer
	deleteSvmPeer                             = _deleteSvmPeer
	waitForMirrorStatePollInterval            = env.GetInt("WAIT_FOR_MIRROR_STATE_POLL_INTERVAL_SECONDS", 6)
	snapmirrorErrorIntervalRetrySeconds       = 10
)

const (
	waitForReplicationStateMaxRetries      = 10
	waitForReplicationStateAbortMaxRetries = 30
)

// Volume states
const (
	VolumeStateOffline = "offline"
	VolumeStateOnline  = "online"
)

// Volume replication endpoint types
const (
	VolumeReplicationEndpointTypeDestination = "dst"
	VolumeReplicationEndpointTypeSource      = "src"
)

// Snapmirror states
const (
	SnapmirrorStateUninitialized             = "uninitialized"
	SnapmirrorStateMirrored                  = "snapmirrored"
	SnapmirrorStateBroken                    = "broken_off"
	SnapMirrorRelationshipStatusTransferring = "transferring"
	SnapmirrorRelationshipStateIdle          = "idle"
)

// Volume replication schedules
const (
	VolumeReplicationSchedule10Minutely = "10minutely"
	VolumeReplicationScheduleHourly     = "hourly"
	VolumeReplicationScheduleDaily      = "daily"
)

var (
	nillableParseStringTimeTotimeTime = nillable.ParseStringTimeTotimeTime
	nillableParseDurationInSeconds    = nillable.ParseDurationInSeconds
)

func createVolumeReplicationSchedule(provider *OntapRestProvider, schedule string) (err error) {
	var cronSchedule *ontaprest.Schedule
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return err
	}
	if err := client.Cluster().ScheduleCollectionGet(&ontaprest.ScheduleCollectionGetParams{
		Name: schedule,
	}, func(schedules []*ontaprest.Schedule) error {
		if len(schedules) == 1 {
			cronSchedule = schedules[0]
		}
		return nil
	}); err != nil && !errors.IsNotFoundErrForObjectType(err, "Cron schedule") {
		return err
	}

	if cronSchedule != nil {
		return nil
	}
	switch schedule {
	case VolumeReplicationSchedule10Minutely:
		err = client.Cluster().ScheduleCreate(&ontaprest.ScheduleCreateParams{
			Name:        schedule,
			Months:      nil,
			DaysOfMonth: nil,
			DaysOfWeek:  nil,
			Hours:       nil,
			Minutes:     []int{0, 10, 20, 30, 40, 50},
		})
	case VolumeReplicationScheduleHourly:
		err = client.Cluster().ScheduleCreate(&ontaprest.ScheduleCreateParams{
			Name:        schedule,
			Months:      nil,
			DaysOfMonth: nil,
			DaysOfWeek:  nil,
			Hours:       nil,
			Minutes:     []int{5},
		})
	case VolumeReplicationScheduleDaily:
		err = client.Cluster().ScheduleCreate(&ontaprest.ScheduleCreateParams{
			Name:        schedule,
			Months:      nil,
			DaysOfMonth: nil,
			DaysOfWeek:  nil,
			Hours:       []int{0},
			Minutes:     []int{5},
		})
	default:
		err = errors.NewUserInputValidationErr("Unknown replication schedule")
	}
	if err != nil {
		return err
	}
	return nil
}

const flexGroupGeometryMismatchMsg = "Geometry of the source FlexGroup does not match that of the destination FlexGroup"

// CreateVolumeReplication creates the Volume Replication for the provider's owner
func (rc *OntapRestProvider) CreateVolumeReplication(params *CreateVolumeReplicationParams) (*VolumeReplication, error) {
	initialize, err := validateCreateSnapmirror(rc, params)
	if err != nil {
		return nil, err
	}

	replication, err := createVsaVolumeReplication(rc, params, initialize)
	if err != nil {
		if strings.Contains(err.Error(), flexGroupGeometryMismatchMsg) {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrFlexGroupGeometryMismatchForReplication, err))
		}
		return nil, err
	}
	return replication, nil
}

func createVsaVolumeReplication(provider *OntapRestProvider, params *CreateVolumeReplicationParams, isInitialize bool) (*VolumeReplication, error) {
	err := doCreateVolumeReplicationScheduleIfNeeded(provider, params.VolumeReplication.ReplicationSchedule)
	if err != nil {
		return nil, err
	}

	createParams := &ontaprest.SnapmirrorRelationshipCreateParams{
		SourcePath:      params.VolumeReplication.SourcePath(),
		DestinationPath: params.VolumeReplication.DestinationPath(),
		Policy:          params.VolumeReplication.ReplicationPolicy,
		Schedule:        &params.VolumeReplication.ReplicationSchedule,
	}

	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}

	// Check if a snapmirror relationship already exists for this source and destination
	listParams := &ontaprest.SnapmirrorRelationshipListParams{
		SourcePath:      createParams.SourcePath,
		DestinationPath: createParams.DestinationPath,
	}
	existingList, err := client.Snapmirror().SnapmirrorRelationshipList(listParams)
	if err != nil {
		return nil, err
	}

	var snapmirror *ontaprest.SnapmirrorRelationship
	if len(existingList) > 0 {
		// Use existing relationship
		snapmirror = existingList[0]
	} else {
		// Create new relationship
		createdSm, job, createErr := client.Snapmirror().SnapmirrorRelationshipCreate(createParams)
		if createErr != nil {
			return nil, createErr
		}
		err = waitForJobIfNeeded(provider, job)
		if err != nil {
			return nil, err
		}
		snapmirror, err = client.Snapmirror().SnapmirrorRelationshipGet(&ontaprest.SnapmirrorRelationshipGetParams{UUID: createdSm.UUID.String()})
		if err != nil {
			return nil, err
		}
	}

	_, job, err := client.Snapmirror().SnapmirrorRelationshipResyncOrInitializeOrResume(snapmirror.UUID.String())
	if err != nil {
		return nil, err
	}
	err = waitForJobIfNeeded(provider, job)
	if err != nil {
		return nil, err
	}

	snapmirror, err = client.Snapmirror().SnapmirrorRelationshipGet(&ontaprest.SnapmirrorRelationshipGetParams{UUID: snapmirror.UUID.String()})
	if err != nil {
		return nil, err
	}

	if !isInitialize {
		snapmirror, err = doWaitForSnapmirrorState(provider, time.Duration(waitForMirrorStatePollInterval)*time.Second, 10, models.SnapmirrorRelationshipStateSnapmirrored, snapmirror.UUID.String())
		if err != nil {
			return nil, err
		}
	}

	if isInitialize {
		if snapmirror != nil && snapmirror.State != nil && *snapmirror.State == models.SnapmirrorRelationshipStateSnapmirrored {
			volumeGetParams := &ontaprest.VolumeGetParams{BaseParams: ontaprest.BaseParams{Fields: []string{"language"}}, UUID: params.VolumeReplication.Volume.ExternalUUID}
			volume, err := client.Storage().VolumeGet(volumeGetParams)
			if err != nil && !errors.IsNotFoundErr(err) {
				return nil, err
			}
			if volume != nil && volume.Language != nil && params.VolumeReplication.Volume != nil {
				language := *volume.Language
				language = strings.ToLower(strings.ReplaceAll(language, "_", "-"))
				params.VolumeReplication.Volume.Language = &language
			}
		}
	}
	return convertSnapMirrorToVolumeReplication(*snapmirror, params.VolumeReplication)
}

func waitForJobIfNeeded(provider *OntapRestProvider, job *ontaprest.JobAccepted) error {
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return err
	}
	if job != nil {
		if err := client.Poll(job.JobUUID); err != nil {
			return err
		}
	}
	return nil
}

func convertSnapMirrorToVolumeReplication(snapmirror ontaprest.SnapmirrorRelationship, in *VolumeReplication) (*VolumeReplication, error) {
	var lagTime, lastTransferTimeSecs, totalTransferTimeSecs float64
	var unhealthyReason, transferSchedule, state, transferUUID string
	var bytesTransferred int64
	var endTime *time.Time
	if snapmirror.TotalTransferDuration != nil {
		totalTransferTimeSecsParsed, err := duration.Parse(*snapmirror.TotalTransferDuration)
		if err != nil {
			return nil, err
		}
		totalTransferTimeSecs = totalTransferTimeSecsParsed.ToTimeDuration().Seconds()
	}
	if snapmirror.Transfer != nil {
		if snapmirror.Transfer.UUID != nil {
			transferUUID = snapmirror.Transfer.UUID.String()
		}
		if snapmirror.Transfer.TotalDuration != nil {
			lastTransferTimeSecsParsed, err := duration.Parse(*snapmirror.Transfer.TotalDuration)
			if err != nil {
				return nil, err
			}
			lastTransferTimeSecs = lastTransferTimeSecsParsed.ToTimeDuration().Seconds()
		}
		if snapmirror.Transfer.State != nil {
			state = *snapmirror.Transfer.State
		}
		if snapmirror.Transfer.BytesTransferred != nil {
			bytesTransferred = nillable.FromPointer(snapmirror.Transfer.BytesTransferred)
		}
		if snapmirror.Transfer.EndTime != nil {
			endTime = (*time.Time)(snapmirror.Transfer.EndTime)
		}
	}
	if snapmirror.LagTime != nil {
		lagTimeParsed, err := duration.Parse(*snapmirror.LagTime)
		if err != nil {
			return nil, err
		}
		lagTime = lagTimeParsed.ToTimeDuration().Seconds()
	}
	if len(snapmirror.SnapmirrorRelationshipInlineUnhealthyReason) > 0 {
		unhealthyReason = nillable.FromPointer(snapmirror.SnapmirrorRelationshipInlineUnhealthyReason[0].Message)
	}
	if snapmirror.TransferSchedule != nil {
		transferSchedule = nillable.FromPointer(snapmirror.TransferSchedule.Name)
	}
	replicationPolicy := in.ReplicationPolicy
	if snapmirror.Policy != nil {
		replicationPolicy = nillable.FromPointer(snapmirror.Policy.Name)
	}
	healthy := snapmirror.Healthy
	return &VolumeReplication{
		UUID:                  in.UUID,
		EndpointType:          in.EndpointType,
		RemoteRegion:          in.RemoteRegion,
		RemoteResourceID:      in.RemoteResourceID,
		SourceHostName:        in.SourceHostName,
		SourceSVMName:         in.SourceSVMName,
		SourceVolumeName:      in.SourceVolumeName,
		DestinationHostName:   in.DestinationHostName,
		DestinationSVMName:    in.DestinationSVMName,
		DestinationVolumeName: in.DestinationVolumeName,
		ReplicationPolicy:     replicationPolicy,
		ReplicationSchedule:   transferSchedule,
		RelationshipID:        snapmirror.UUID.String(),
		LifeCycleState:        in.LifeCycleState,
		LifeCycleStateDetails: in.LifeCycleStateDetails,
		MirrorState:           nillable.FromPointer(snapmirror.State),
		RelationshipStatus:    state,
		Healthy:               *healthy,
		UnhealthyReason:       unhealthyReason,
		Jobs:                  in.Jobs,
		Mounted:               in.Mounted,
		TotalTransferBytes:    nillable.FromPointer(snapmirror.TotalTransferBytes),
		TotalTransferTimeSecs: int64(totalTransferTimeSecs),
		LastTransferSize:      bytesTransferred,
		LastTransferDuration:  int64(lastTransferTimeSecs),
		LastTransferEndTime:   endTime,
		LagTime:               int64(lagTime),
		Tags:                  in.Tags,
		CreatedAt:             in.CreatedAt,
		UpdatedAt:             in.UpdatedAt,
		DeletedAt:             in.DeletedAt,
		Description:           in.Description,
		Volume:                in.Volume,
		ReplicationType:       in.ReplicationType,
		TransferUUID:          transferUUID,
	}, nil
}

func waitForSnapmirrorState(provider *OntapRestProvider, retryInterval time.Duration, maxRetries int, mirrorState, snapmirrorUUID string) (snapmirror *ontaprest.SnapmirrorRelationship, err error) {
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}
	for retries := 0; retries < maxRetries; retries++ {
		snapmirror, err = client.Snapmirror().SnapmirrorRelationshipGet(&ontaprest.SnapmirrorRelationshipGetParams{UUID: snapmirrorUUID})
		if err != nil {
			return
		}
		if snapmirror.State != nil && *snapmirror.State == mirrorState {
			break
		}

		time.Sleep(retryInterval)
	}
	return
}

// validateCreateSnapmirror validates all the create replication parameters
func validateCreateSnapmirror(provider *OntapRestProvider, params *CreateVolumeReplicationParams) (bool, error) {
	// Step 1.: Ensure that SVM peering exists
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return false, err
	}
	err = doEnsureSvmPeering(provider, params)
	if err != nil {
		return false, err
	}

	volumeGetParams := &ontaprest.VolumeGetParams{UUID: params.VolumeReplication.Volume.ExternalUUID}
	volume, err := client.Storage().VolumeGet(volumeGetParams)
	if err != nil {
		return false, err
	}

	destinations, err := client.Snapmirror().SnapmirrorRelationshipListDestinations(nil)
	if err != nil {
		return false, err
	}
	pathConflict := false
	// sourcePathConflict is to check for how many source paths are the same as in the request
	sourcePathConflict := 0
	// Check if creation is possible - (enforce 1-1 topology)
	for _, dest := range destinations {
		if *dest.Source.Path == params.VolumeReplication.DestinationPath() ||
			*dest.Destination.Path == params.VolumeReplication.DestinationPath() ||
			*dest.Destination.Path == params.VolumeReplication.SourcePath() {
			pathConflict = true
			break
		}
		if *dest.Source.Path == params.VolumeReplication.SourcePath() {
			pathConflict = true
			break
		}
	}
	snapmirrors, err := client.Snapmirror().SnapmirrorRelationshipList(&ontaprest.SnapmirrorRelationshipListParams{})
	if err != nil {
		return false, err
	}
	for _, sm := range snapmirrors {
		if *sm.Source.Path == params.VolumeReplication.DestinationPath() ||
			*sm.Destination.Path == params.VolumeReplication.DestinationPath() ||
			*sm.Destination.Path == params.VolumeReplication.SourcePath() {
			pathConflict = true
			break
		}
		if *sm.Source.Path == params.VolumeReplication.SourcePath() {
			pathConflict = true
			break
		}
	}

	resync := false
	if pathConflict {
		for _, dest := range destinations {
			if *dest.Source.Path == params.VolumeReplication.DestinationPath() &&
				*dest.Destination.Path == params.VolumeReplication.SourcePath() {
				resync = true
				break
			}
		}
	}

	if pathConflict && !resync {
		return false, errors.NewConflictErr("One or both volumes in the request are in a pre-existing volume replication")
	}
	if sourcePathConflict > 1 {
		return false, errors.NewConflictErr("Source volume with more than 2 destinations is not supported")
	}

	initialize := !resync && !params.ReverseResync
	if initialize {
		if *volume.Type != models.VolumeTypeDp {
			return false, errors.NewUserInputValidationErr("Destination is not a Data Protection volume")
		}
	}
	return initialize, nil
}

// AuthorizeVolumeReplication authorizes the Volume Replication
func (rc *OntapRestProvider) AuthorizeVolumeReplication(params *CreateVolumeReplicationParams) (*VolumeReplication, error) {
	err := doEnsureSvmPeering(rc, params)
	return nil, err
}

func ensureSvmPeering(provider *OntapRestProvider, params *CreateVolumeReplicationParams) error {
	if params.VolumeReplication.EndpointType == VolumeReplicationEndpointTypeDestination {
		return createSvmPeering(provider, params.VolumeReplication.SourceHostName, params.VolumeReplication.SourceSVMName,
			params.VolumeReplication.DestinationSVMName)
	}
	return provider.AcceptSvmPeering(params.VolumeReplication.SourceSVMName, params.VolumeReplication.DestinationSVMName)
}

// DeleteVolumeReplication deletes the Volume Replication
func (rc *OntapRestProvider) DeleteVolumeReplication(params *DeleteVolumeReplicationParams) (*VolumeReplication, error) {
	volRep := params.VolumeReplication
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	var mirrorState, relationshipStatus, snapmirrorUUID string
	var snapmirror ontaprest.SnapmirrorRelationship

	if params.VolumeReplication.RelationshipID == "" {
		getParams := ontaprest.SnapmirrorRelationshipListParams{
			DestinationPath: params.VolumeReplication.DestinationPath(),
			SourcePath:      params.VolumeReplication.SourcePath(),
		}
		snapmirrorList, err := client.Snapmirror().SnapmirrorRelationshipList(&getParams)
		if err != nil {
			return nil, err
		}
		if len(snapmirrorList) > 0 {
			snapmirror = *snapmirrorList[0]
		}
	} else {
		getParams := ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID}
		snapmirrorRel, err := client.Snapmirror().SnapmirrorRelationshipGet(&getParams)
		if err != nil {
			if !errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") && !strings.Contains(err.Error(), "entry not found") && !strings.Contains(err.Error(), "The first character must be a letter or underscore") {
				return nil, err
			}
		}
		if snapmirrorRel != nil {
			snapmirror = *snapmirrorRel
		}
	}

	if snapmirror.UUID != nil {
		snapmirrorRelationshipStatus := SnapmirrorRelationshipStateIdle
		if snapmirror.Transfer != nil {
			snapmirrorRelationshipStatus = *snapmirror.Transfer.State
		}
		mirrorState = *snapmirror.State
		relationshipStatus = snapmirrorRelationshipStatus
		snapmirrorUUID = snapmirror.UUID.String()
	}

	err = doValidateAndDeleteVolumeReplication(rc, mirrorState, relationshipStatus, snapmirrorUUID, params)
	if err != nil {
		return nil, err
	}

	return volRep, nil
}

// validateAndDeleteVolumeReplication validates and destroys Volume Replication
func validateAndDeleteVolumeReplication(provider *OntapRestProvider, mirrorState, relationshipStatus, snapmirrorUUID string, params *DeleteVolumeReplicationParams) error {
	if !params.VolumeReplication.IsCleanup && (mirrorState == SnapmirrorStateMirrored ||
		(mirrorState == SnapmirrorStateUninitialized && relationshipStatus == SnapMirrorRelationshipStatusTransferring)) {
		return errors.NewConflictErr("Cannot delete a relationship in the current mirror state")
	}

	snapmirrorRelationshipParams := ontaprest.SnapmirrorRelationshipDeleteParams{
		UUID: snapmirrorUUID,
	}

	if nillable.GetBool(params.DestinationOnly, false) && nillable.GetBool(params.SourceOnly, false) {
		return errors.New("Can't have both DestinationOnly and SourceOnly set")
	} else if nillable.GetBool(params.DestinationOnly, false) {
		snapmirrorRelationshipParams.DestinationOnly = params.DestinationOnly
	} else if nillable.GetBool(params.SourceOnly, false) {
		snapmirrorRelationshipParams.SourceOnly = params.SourceOnly
	}

	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return err
	}
	if snapmirrorUUID != "" {
		_, jobAccepted, err := client.Snapmirror().SnapmirrorRelationshipDelete(&snapmirrorRelationshipParams)
		if err != nil && (!errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") && !strings.Contains(err.Error(), "entry not found")) {
			return err
		}
		if jobAccepted != nil {
			if err = client.Poll(jobAccepted.JobUUID); err != nil && (!errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") && !strings.Contains(err.Error(), "entry not found")) {
				return err
			}
		}
	}

	err = doCleanupSvmPeering(provider, params)
	if err != nil && !errors.IsNotFoundErrForObjectType(err, "SVM peer") {
		return err
	}

	return nil
}

// UpdateVolumeReplication updates the Volume Replication
func (rc *OntapRestProvider) UpdateVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error) {
	if volRep.ReplicationSchedule == "" {
		return volRep, nil
	}

	err := doCreateVolumeReplicationScheduleIfNeeded(rc, volRep.ReplicationSchedule)
	if err != nil {
		return nil, err
	}

	return modifyVsaVolumeReplication(rc, volRep)
}

func (rc *OntapRestProvider) BreakVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{
		UUID:  volRep.RelationshipID,
		State: &volRep.MirrorState,
	}
	_, job, err := client.Snapmirror().SnapmirrorRelationshipModify(modifyParams)
	if err != nil {
		rc.Logger.Error("Failed to break volume replication in ontap", "error", err)
		return nil, err
	}

	err = waitForJobIfNeeded(rc, job)
	if err != nil {
		return nil, err
	}
	return nil, nil
}

func (rc *OntapRestProvider) AbortVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	modifyTransferParams := &ontaprest.SnapmirrorRelationshipTransferModifyParams{
		UUID:         volRep.RelationshipID,
		TransferUUID: volRep.TransferUUID,
		State:        &volRep.RelationshipStatus,
	}
	err = client.Snapmirror().SnapmirrorRelationshipTransferModify(modifyTransferParams)
	if err != nil {
		rc.Logger.Error("Failed to abort volume replication in ontap", "error", err)
		return nil, err
	}
	for retries := 0; retries < waitForReplicationStateAbortMaxRetries; retries++ {
		snapmirror, err := client.Snapmirror().SnapmirrorRelationshipGet(&ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID})
		if err != nil {
			rc.Logger.Error("Failed to get volume replication in ontap", "error", err)
			return nil, err
		}
		if snapmirror.TransferState() != SnapMirrorRelationshipStatusTransferring {
			return volRep, nil
		}

		time.Sleep(time.Duration(waitForMirrorStatePollInterval) * time.Second)
	}
	rc.Logger.Error("Transfer abort did not finish in time")
	return nil, errors.New("Transfer abort did not finish in time")
}

func modifyVsaVolumeReplication(provider *OntapRestProvider, volRep *VolumeReplication) (*VolumeReplication, error) {
	modifyParams := &ontaprest.SnapmirrorRelationshipModifyParams{
		UUID:             volRep.RelationshipID,
		TransferSchedule: &volRep.ReplicationSchedule,
	}

	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}
	snapMirror, job, err := client.Snapmirror().SnapmirrorRelationshipModify(modifyParams)
	if err != nil {
		return nil, err
	}

	err = waitForJobIfNeeded(provider, job)
	if err != nil {
		return nil, err
	}

	if snapMirror == nil {
		getParams := &ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID}
		snapMirror, err = client.Snapmirror().SnapmirrorRelationshipGet(getParams)
		if err != nil {
			return nil, err
		}
	}

	return convertSnapMirrorToVolumeReplication(*snapMirror, volRep)
}

func _listSnapmirrorDestinations(provider *OntapRestProvider, params *ontaprest.SnapmirrorRelationshipListDestinationsParams) ([]*SnapmirrorDestination, error) {
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}
	destinations, err := client.Snapmirror().SnapmirrorRelationshipListDestinations(params)
	if err != nil {
		return nil, err
	}

	storageDestinations := make([]*SnapmirrorDestination, 0, len(destinations))
	for _, dest := range destinations {
		storageDest := &SnapmirrorDestination{RelationshipUUID: nillable.GetStringFromUUID(dest.UUID, "")}

		if dest.Destination != nil {
			storageDest.DestinationPath = nillable.GetString(dest.Destination.Path, "")
			if dest.Destination.Svm != nil {
				storageDest.DestinationSVMName = nillable.GetString(dest.Destination.Svm.Name, "")
			}
		}

		if dest.Source != nil {
			storageDest.SourcePath = nillable.GetString(dest.Source.Path, "")
			if dest.Source.Svm != nil {
				storageDest.SourceSVMName = nillable.GetString(dest.Source.Svm.Name, "")
			}
		}
		storageDestinations = append(storageDestinations, storageDest)
	}

	return storageDestinations, nil
}

// ListSnapmirrorDestinations lists snapmirror destinations from ONTAP
// If params is nil, lists all destinations. Otherwise, filters by the provided parameters.
func (rc *OntapRestProvider) ListSnapmirrorDestinations(params *ontaprest.SnapmirrorRelationshipListDestinationsParams) ([]*SnapmirrorDestination, error) {
	return listSnapmirrorDestinations(rc, params)
}

// ReleaseVolumeReplication releases the Volume Replication
func (rc *OntapRestProvider) ReleaseVolumeReplication(params *ReleaseVolumeReplicationParams) (*VolumeReplication, error) {
	volRep := params.VolumeReplication

	listDestinationParams := &ontaprest.SnapmirrorRelationshipListDestinationsParams{
		DestinationPath: nillable.GetStringPtr(params.VolumeReplication.DestinationPath()),
		SourcePath:      nillable.GetStringPtr(params.VolumeReplication.SourcePath()),
	}

	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	listDestinations, err := client.Snapmirror().SnapmirrorRelationshipListDestinations(listDestinationParams)
	if err != nil {
		return nil, err
	}

	if len(listDestinations) == 1 {
		releaseParams := ontaprest.SnapmirrorRelationshipReleaseParams{
			UUID: listDestinations[0].UUID.String(),
		}

		retryErrors := []string{"Another SnapMirror operation is in progress"}
		// Retry for 6 minutes
		for retryCount := 1; retryCount < 30; retryCount++ {
			_, jobAccepted, err := client.Snapmirror().SnapmirrorRelationshipRelease(&releaseParams)
			if err == nil && jobAccepted != nil {
				err = client.Poll(jobAccepted.JobUUID)
			}

			if err != nil {
				if shouldRetry(err, retryErrors) {
					time.Sleep(time.Duration(snapmirrorErrorIntervalRetrySeconds) * time.Second)
					continue
				}

				if !errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") && !strings.Contains(err.Error(), "entry not found") {
					return nil, err
				}
			}
			break
		}
	}

	if params.VolumeReplication.ReplicationType == clientmodel.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION && params.VolumeReplication.EndpointType == VolumeReplicationEndpointTypeSource {
		svmCleanupParams := &DeleteVolumeReplicationParams{
			VolumeReplication: params.VolumeReplication,
		}
		err = doCleanupSvmPeering(rc, svmCleanupParams)
		if err != nil && !errors.IsNotFoundErrForObjectType(err, "SVM peer") {
			rc.Logger.Error("Error while cleaning up svm peering", err.Error())
			return nil, err
		}
	}
	return volRep, nil
}

// ResyncVolumeReplication resyncs the Volume Replication
func (rc *OntapRestProvider) ResyncVolumeReplication(volRep *VolumeReplication) (*VolumeReplication, error) {
	getParams := ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID}
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	snapmirror, err := client.Snapmirror().SnapmirrorRelationshipGet(&getParams)
	if err != nil {
		return nil, err
	}

	volume, err := doValidateResyncVolumeReplication(rc, snapmirror, volRep)
	if err != nil {
		return nil, err
	}

	_, job, err := client.Snapmirror().SnapmirrorRelationshipResyncOrInitializeOrResume(snapmirror.UUID.String())
	if err != nil {
		return nil, err
	}
	err = waitForJobIfNeeded(rc, job)
	if err != nil {
		return nil, err
	}

	_, err = doWaitForSnapmirrorState(rc, time.Duration(waitForMirrorStatePollInterval)*time.Second, waitForReplicationStateMaxRetries, SnapmirrorStateMirrored, volRep.RelationshipID)
	if err != nil {
		return nil, err
	}

	// resync is performed from the destination, unmount it because it will be a DP volume after resync
	err = doUnmountVolume(rc, volume, volRep)
	if err != nil {
		return nil, err
	}

	getParams = ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID}
	snapmirror, err = client.Snapmirror().SnapmirrorRelationshipGet(&getParams)
	if err != nil {
		return nil, err
	}

	return convertSnapMirrorToVolumeReplication(*snapmirror, volRep)
}

func (rc *OntapRestProvider) ReverseVolumeReplication(volRep *VolumeReplication) (*SnapmirrorDestination, error) {
	getParams := ontaprest.SnapmirrorRelationshipGetParams{UUID: volRep.RelationshipID}
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	snapmirror, err := client.Snapmirror().SnapmirrorRelationshipGet(&getParams)
	if err != nil {
		return nil, err
	}

	if snapmirror == nil {
		return nil, errors.NewNotFoundErr("snapmirror relationship", &volRep.RelationshipID)
	}

	// Validate that the snapmirror can be reversed (typically should be in broken-off state)
	if snapmirror.State == nil || *snapmirror.State != models.SnapmirrorRelationshipStateBrokenOff {
		return nil, errors.NewConflictErr("Cannot perform a reverse operation unless the relationship is in broken-off state")
	}

	reverseParams := ontaprest.SnapmirrorRelationshipReverseParams{
		UUID:            volRep.RelationshipID,
		SourcePath:      *snapmirror.Destination.Path, // Current destination becomes new source
		DestinationPath: *snapmirror.Source.Path,      // Current source becomes new destination
	}

	_, job, err := client.Snapmirror().SnapmirrorRelationshipReverse(&reverseParams)
	if err != nil {
		return nil, err
	}

	if job != nil {
		err = waitForJobIfNeeded(rc, job)
		if err != nil {
			return nil, err
		}
	}

	return &SnapmirrorDestination{}, nil
}

// validateResyncVolumeReplication validates if snapmirror is eligible for resync
func validateResyncVolumeReplication(provider *OntapRestProvider, snapmirror *ontaprest.SnapmirrorRelationship, volRep *VolumeReplication) (*ontaprest.Volume, error) {
	// Check to see if a resync operation can be performed. Can be performed if the mirror state is broken, if the force flag is set to true or if the replication was stopped during initial transfer.
	if volRep.MirrorState != SnapmirrorStateBroken && !nillable.GetBool(volRep.Force, false) && *snapmirror.State != models.SnapmirrorRelationshipStateUninitialized {
		return nil, errors.NewConflictErr("Cannot perform a resync operation in this mirror state")
	}

	volumeGetParams := &ontaprest.VolumeGetParams{UUID: volRep.Volume.ExternalUUID}
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}
	volume, err := client.Storage().VolumeGet(volumeGetParams)
	if err != nil {
		return nil, err
	}

	return volume, nil
}

// unmountVolume unmounts volume after snapmirror resync
func _unmountVolume(provider *OntapRestProvider, volume *ontaprest.Volume, volRep *VolumeReplication) error {
	if volRep.Volume.HasProtocolType(string(utils.ProtocolISCSI)) {
		return nil
	}

	// If volume is SMB, delete CIFS share before unmounting
	if volRep.Volume.HasProtocolType(string(utils.ProtocolSMB)) {
		// Get junction path and SVM UUID from volume
		var junctionPath string
		var svmUUID string

		junctionPath = volRep.Volume.JunctionPath

		if volume.Svm != nil && volume.Svm.UUID != nil {
			svmUUID = *volume.Svm.UUID
		}

		// Delete CIFS share if junction path and SVM UUID are available
		// Share name is derived from junction path by removing leading '/'
		shareName := junctionPath
		if len(shareName) > 0 && shareName[0] == '/' {
			shareName = shareName[1:]
		}

		client, err := getOntapClientFunc(provider.ClientParams)
		if err != nil {
			provider.Logger.Errorf("Failed to get ONTAP client for CIFS share deletion: %v", err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		provider.Logger.Debugf("Deleting CIFS share %s for volume %s", shareName, nillable.GetString(volume.Name, ""))
		err = client.NAS().CifsShareDelete(&ontaprest.CifsShareDeleteParams{
			ShareName: shareName,
			SvmUUID:   svmUUID,
		})
		if err != nil {
			// Log error but continue with unmount - share might not exist or already deleted
			if !errors.IsNotFoundErr(err) && !strings.Contains(err.Error(), "entry doesn't exist") && !strings.Contains(err.Error(), "entry not found") {
				provider.Logger.Errorf("Failed to delete CIFS share %s for volume %s: %v", shareName, nillable.GetString(volume.Name, ""), err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
			provider.Logger.Debugf("CIFS share %s not found or already deleted for volume %s", shareName, nillable.GetString(volume.Name, ""))
		} else {
			provider.Logger.Debugf("Successfully deleted CIFS share %s for volume %s", shareName, nillable.GetString(volume.Name, ""))
		}
	}

	// If volume is currently mounted (has existing junction path), unmount it first
	provider.Logger.Debugf("Unmounting volume %s from junction path %s", nillable.GetString(volume.Name, ""), volRep.Volume.JunctionPath)
	_, err := provider.UnmountVolume(*volume.UUID)
	if err != nil {
		provider.Logger.Errorf("Failed to unmount volume %s: %v", nillable.GetString(volume.Name, ""), err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	provider.Logger.Debugf("Volume %s unmounted successfully", nillable.GetString(volume.Name, ""))
	return nil
}

// HasProtocolType checks whether the volume has the specified protocol type
func (v *Volume) HasProtocolType(protocolType string) bool {
	for _, typ := range v.ProtocolTypes {
		if typ == protocolType {
			return true
		}
	}
	return false
}

func cleanupSvmPeering(provider *OntapRestProvider, params *DeleteVolumeReplicationParams) error {
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return err
	}
	remainingSnapmirrors, err := client.Snapmirror().SnapmirrorRelationshipList(&ontaprest.SnapmirrorRelationshipListParams{})
	if err != nil {
		return err
	}
	destinations, err := client.Snapmirror().SnapmirrorRelationshipListDestinations(&ontaprest.SnapmirrorRelationshipListDestinationsParams{})
	if err != nil {
		return err
	}

	if !shouldDeleteSvmPeer(remainingSnapmirrors, destinations, params.VolumeReplication) {
		return nil
	}

	t1 := time.Now().Add(time.Duration(svmPeerTimeoutMinutes) * time.Minute)
	force := false
	var svmPeer *SvmPeer
	for time.Now().Before(t1) {
		if params.VolumeReplication.ReplicationType == clientmodel.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION && params.VolumeReplication.EndpointType == "src" {
			svmPeer, err = getSvmPeer(provider, params.VolumeReplication.SourceSVMName, params.VolumeReplication.DestinationSVMName)
			if err != nil {
				return err
			}
			err = deleteSvmPeer(provider, svmPeer.UUID, force)
		} else {
			svmPeer, err = getSvmPeer(provider, params.VolumeReplication.DestinationSVMName, params.VolumeReplication.SourceSVMName)
			if err != nil {
				return err
			}
			err = deleteSvmPeer(provider, svmPeer.UUID, force)
		}

		if err != nil && !isNonExistentVserverEntryError(err) {
			if strings.Contains(err.Error(), "Relationship is in use by SnapMirror in local cluster") {
				return nil
			}

			// Ignore error if peer relationship is in use by FlexCache
			if strings.Contains(err.Error(), "The peer relationship is in use by FlexCache") {
				return nil
			}

			if strings.Contains(err.Error(), "Relationship is in use by SnapMirror in peer cluster") {
				time.Sleep(time.Duration(svmPeerPollIntervalSeconds) * time.Second)
				continue
			}
			if strings.Contains(err.Error(), "A relationship on the peer cluster needs to be released") {
				return errors.NewConflictErrWithTrackingID("A source relationship on the Vserver peer needs to be released in peer cluster", errors.StaleSnapmirrorCleanupNeeded)
			}
			if strings.Contains(err.Error(), "Failed to load job for Deleting a Vserver peer relationship") {
				time.Sleep(time.Duration(svmPeerPollIntervalSeconds) * time.Second)
				continue
			}
			if (strings.Contains(err.Error(), "Failed to contact peer cluster") && params.VolumeReplication.Volume != nil && params.VolumeReplication.Volume.IsOnPremMigration) ||
				(strings.Contains(err.Error(), "Failed to contact peer cluster") && isHybridReplication(params.VolumeReplication)) {
				force = true
				continue
			}
			return err
		}
		return nil
	}

	return errors.New("Timeout during cleanup of peering infrastructure.")
}

func createSvmPeering(provider *OntapRestProvider, srcClusterName, srcSVMName, dstSVMName string) error {
	var snapmirrorApplication = models.SvmPeerApplicationsSnapmirror
	return provider.CreateSvmPeering(srcClusterName, srcSVMName, dstSVMName, snapmirrorApplication)
}

func _getSvmPeer(provider *OntapRestProvider, srcSVMName, dstSVMName string) (*SvmPeer, error) {
	svmPeer, err := provider.GetSVMPeer(&srcSVMName, &dstSVMName)
	if err != nil {
		return nil, err
	}
	return svmPeer, nil
}

func _deleteSvmPeer(provider *OntapRestProvider, svmPeerUUID string, force bool) error {
	return provider.DeleteSVMPeer(svmPeerUUID, force)
}

func shouldDeleteSvmPeer(snapmirrors []*ontaprest.SnapmirrorRelationship, destinations []*ontaprest.SnapmirrorRelationship, vr *VolumeReplication) bool {
	// This is added to check whether to deleteSvmPeer during release operation when replicationType is HybridReplication & EndpointType is Src
	if vr.ReplicationType == clientmodel.HybridReplicationParametersV1betaHybridReplicationTypeONPREMREPLICATION && vr.EndpointType == "src" {
		for _, sm := range snapmirrors {
			if *sm.Source.Svm.Name == vr.DestinationSVMName && *sm.Destination.Svm.Name == vr.SourceSVMName {
				return false
			}
		}
		for _, dest := range destinations {
			if *dest.Source.Svm.Name == vr.SourceSVMName && *dest.Destination.Svm.Name == vr.DestinationSVMName {
				return false
			}
		}
		return true
	}

	for _, sm := range snapmirrors {
		if *sm.Source.Svm.Name == vr.SourceSVMName && *sm.Destination.Svm.Name == vr.DestinationSVMName {
			return false
		}
	}
	for _, dest := range destinations {
		if *dest.Source.Svm.Name == vr.DestinationSVMName && *dest.Destination.Svm.Name == vr.SourceSVMName {
			return false
		}
	}
	return true
}

func isHybridReplication(vr *VolumeReplication) bool {
	if vr == nil {
		return false
	}
	return vr.ReplicationType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) ||
		vr.ReplicationType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
}

func isNonExistentVserverEntryError(err error) bool {
	return strings.Contains(err.Error(), "entry doesn't exist") ||
		strings.Contains(err.Error(), "entry not found") ||
		strings.Contains(err.Error(), "Vserver peer relationship does not exist on the local cluster")
}

// GetReplicationDetails retrieves the details of a specific Volume Replication
func (rc *OntapRestProvider) GetReplicationDetails(ctx context.Context, volRep *VolumeReplication) (*VolumeReplication, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	snapmirrorOkResp, err := client.Snapmirror().SnapmirrorGetPriv(context.TODO(), volRep.DestinationPath(), volRep.ExternalUUID, nil)
	if err != nil {
		return nil, err
	}
	if snapmirrorOkResp == nil || snapmirrorOkResp.Payload == nil || len(snapmirrorOkResp.Payload.Records) < 1 {
		return nil, errors.NewNotFoundErr("snapmirror", &volRep.ExternalUUID)
	}

	// set snapmirror stats to model
	volRep.ExternalUUID = snapmirrorOkResp.Payload.Records[0].RelationshipID
	volRep.MirrorState = snapmirrorOkResp.Payload.Records[0].State
	volRep.RelationshipStatus = snapmirrorOkResp.Payload.Records[0].Status
	volRep.TotalProgress = snapmirrorOkResp.Payload.Records[0].TotalProgress
	volRep.Healthy = snapmirrorOkResp.Payload.Records[0].Healthy
	volRep.UnhealthyReason = snapmirrorOkResp.Payload.Records[0].UnhealthyReason
	volRep.CurrentTransferType = snapmirrorOkResp.Payload.Records[0].CurrentTransferType
	volRep.CurrentTransferError = snapmirrorOkResp.Payload.Records[0].CurrentTransferError
	volRep.TotalTransferBytes = snapmirrorOkResp.Payload.Records[0].TotalTransferBytes
	volRep.TotalTransferTimeSecs = snapmirrorOkResp.Payload.Records[0].TotalTransferTimeSecs
	volRep.LastTransferSize = snapmirrorOkResp.Payload.Records[0].LastTransferSize
	volRep.LastTransferError = snapmirrorOkResp.Payload.Records[0].LastTransferError
	volRep.LastTransferEndTime, err = nillableParseStringTimeTotimeTime(snapmirrorOkResp.Payload.Records[0].LastTransferEndTimestamp)
	if err != nil {
		logger := util.GetLogger(ctx)
		logger.Errorf("Error in ontap.GetSnapMirror(VolumeReplicationID=%s), err=%s ", volRep.UUID, err)
		return nil, err
	}
	volRep.ProgressLastUpdated, err = nillableParseStringTimeTotimeTime(snapmirrorOkResp.Payload.Records[0].ProgressLastUpdated)
	if err != nil {
		logger := util.GetLogger(ctx)
		logger.Errorf("Error in ontap.GetSnapMirror(VolumeReplicationID=%s), err=%s ", volRep.UUID, err)
		return nil, err
	}
	if snapmirrorOkResp.Payload.Records[0].LastTransferDuration != "" {
		volRep.LastTransferDuration = nillableParseDurationInSeconds(snapmirrorOkResp.Payload.Records[0].LastTransferDuration)
	}
	if snapmirrorOkResp.Payload.Records[0].LagTime != "" {
		volRep.LagTime = nillableParseDurationInSeconds(snapmirrorOkResp.Payload.Records[0].LagTime)
	}

	return volRep, nil
}

func (rc *OntapRestProvider) GetVolumeReplication(replication *VolumeReplication) (*VolumeReplication, error) {
	client, err := getOntapClientFunc(rc.ClientParams)
	if err != nil {
		return nil, err
	}
	snapmirror, err := client.Snapmirror().SnapmirrorRelationshipGet(&ontaprest.SnapmirrorRelationshipGetParams{UUID: replication.ExternalUUID})
	if err != nil {
		return nil, err
	}
	return convertSnapMirrorToVolumeReplication(*snapmirror, replication)
}

func (provider *OntapRestProvider) GetVolumeReplicationFromSrcAndDstPath(replication *VolumeReplication) (*VolumeReplication, error) {
	client, err := getOntapClientFunc(provider.ClientParams)
	if err != nil {
		return nil, err
	}
	getParams := ontaprest.SnapmirrorRelationshipListParams{
		DestinationPath: replication.DestinationPath(),
		SourcePath:      replication.SourcePath(),
	}
	snapmirrorList, err := client.Snapmirror().SnapmirrorRelationshipList(&getParams)
	if err != nil {
		return nil, err
	}
	if len(snapmirrorList) > 0 {
		getParams := ontaprest.SnapmirrorRelationshipGetParams{UUID: snapmirrorList[0].UUID.String()}
		snapmirror, err := client.Snapmirror().SnapmirrorRelationshipGet(&getParams)
		if err != nil {
			return nil, err
		}
		return convertSnapMirrorToVolumeReplication(*snapmirror, replication)
	}
	return nil, errors.NewNotFoundErr("snapmirror", nillable.GetStringPtr(replication.SourcePath()))
}

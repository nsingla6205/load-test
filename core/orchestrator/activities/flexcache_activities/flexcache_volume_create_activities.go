package flexcache_activities

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-openapi/strfmt"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontaprestmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type FlexCacheVolumeCreateActivity struct {
	SE database.Storage
}

var (
	hydrationEnabled   = env.GetBool("GCP_HYDRATE_ENABLED", false)
	isHydrationEnabled = _isHydrationEnabled

	utilGetLogger                = util.GetLogger
	hyperscalerGetProviderByNode = vsa.GetProviderByNode
	commonHydrateFlexCacheState  = common.HydrateFlexCacheState
	authGenerateCallbackToken    = auth.GenerateCallbackToken
)

type completeJobOpts struct {
	WorkflowID    string
	ResourceUUID  string
	JobType       string
	GetErrCode    int
	UpdateErrCode int
}

func _isHydrationEnabled() bool {
	return hydrationEnabled
}

// CreateFlexCacheVolumeInOntapActivity creates a FlexCache volume in ONTAP
func (a *FlexCacheVolumeCreateActivity) CreateFlexCacheVolumeInOntapActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	cacheParams := volume.CacheParameters
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	params := vsa.CreateFlexCacheVolumeParams{
		Name:                     volume.Name,
		Size:                     volume.SizeInBytes,
		SvmName:                  volume.Svm.Name,
		AggregateName:            activities.AggregateName,
		OriginSVMName:            cacheParams.PeerSvmName,
		OriginVolumeName:         cacheParams.PeerVolumeName,
		GlobalFileLockingEnabled: cacheParams.EnableGlobalFileLock,
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil {
		params.JunctionPath = &volume.VolumeAttributes.FileProperties.JunctionPath
		if volume.VolumeAttributes.FileProperties.ExportPolicy != nil {
			params.ExportPolicy = &volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
		}
	}

	if cacheParams.CacheConfig != nil {
		config := cacheParams.CacheConfig
		params.WritebackEnabled = config.WritebackEnabled
		params.AtimeScrubEnabled = config.AtimeScrubEnabled
		params.AtimeScrubDays = config.AtimeScrubDays
		params.CifsChangeNotifyEnabled = config.CifsChangeNotifyEnabled
	}

	res, err := provider.CreateFlexCacheVolume(params)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingFlexCacheVolume, err))
	}

	logger.Debug("flexcache volume created successfully")

	result.VolumeResponse = res
	if volume.VolumeAttributes == nil {
		volume.VolumeAttributes = &datamodel.VolumeAttributes{}
	}
	volume.VolumeAttributes.ExternalUUID = res.ExternalUUID

	// TODO: Add rollback logic when DB updates fail - VSCP-6386
	if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
		"volume_attributes": volume.VolumeAttributes,
	}); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) VerifyVolumeEncryptionActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	volumeAttributes := volume.VolumeAttributes
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	params := vsa.GetVolumeParams{
		UUID: volumeAttributes.ExternalUUID,
	}

	res, err := provider.GetVolumeEncryptionStatus(params)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if res.Encryption.Enabled != nil && !*res.Encryption.Enabled {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrUnencryptedVolume, fmt.Errorf("origin volume is not encrypted")))
	}

	logger.Debug("flexcache volume encryption verified successfully")

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) CreateClusterPeerInOntapActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	cacheParams := volume.CacheParameters
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	roleName := activities.OnPremPeerRoleName
	err = EnsureExternalPeerRole(ctx, provider)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	params := vsa.CreateClusterPeerParams{
		PeerAddresses: cacheParams.PeerIpAddresses,
		PeerName:      cacheParams.PeerClusterName,
		IPSpace:       activities.IpSpace,
		LocalRole:     &roleName,
	}

	if cacheParams.CommandExpiryTime != nil {
		expiry := strfmt.DateTime(*cacheParams.CommandExpiryTime)
		params.ExpiryTime = &expiry
	}

	clusterPeer, err := provider.CreateClusterPeer(params)
	if err != nil {
		volume.CacheParameters.CacheStateDetailsCode = coremodels.ErrorDuringClusterPeerCode
		volume.CacheParameters.CacheStateDetails = datamodel.ErrorDuringClusterPeer
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerError, err),
		)
	}

	result.ClusterPeer = clusterPeer
	logger.Infof("cluster peer created successfully with UUID: %s", clusterPeer.ExternalUUID)

	// TODO: Add rollback logic when DB updates fail - VSCP-6386
	if _, err := a.persistVolumeFieldsAfterClusterPeerCreated(ctx, result); err != nil {
		return nil, err
	}
	if _, err := a.updateClusterPeeringRowStateInDBActivity(ctx, result, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING); err != nil {
		return nil, err
	}

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) persistVolumeFieldsAfterClusterPeerCreated(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	clusterPeer := result.ClusterPeer
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	interClusterLifs, err := provider.GetInterclusterLIFs(vsa.InterclusterServicePolicyName)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
	}
	var icLifs []string
	for _, icLif := range interClusterLifs {
		icLifs = append(icLifs, string(icLif.Address))
	}

	peerCommand := fmt.Sprintf(
		"cluster peer create -peer-addrs %s -initial-allowed-vserver-peers %s",
		strings.Join(icLifs, ","),
		volume.CacheParameters.PeerSvmName,
	)

	convertedTime := time.Time(*clusterPeer.ExpiryTime)

	volume.CacheParameters.Passphrase = (*string)(clusterPeer.Passphrase)
	volume.CacheParameters.CommandExpiryTime = &convertedTime
	volume.CacheParameters.Command = &peerCommand
	volume.CacheParameters.CacheStateDetailsCode = coremodels.WaitingForClusterPeeringCode
	volume.CacheParameters.CacheStateDetails = datamodel.WaitingForClusterPeering
	a.setCacheStates(result, cvpModels.FlexCacheV1betaCacheStatePENDINGCLUSTERPEERING)

	updates := map[string]interface{}{
		"cache_parameters": volume.CacheParameters,
	}
	if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, updates); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	logger.Debug("cluster peer command updated successfully")

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) UpdateFlexCacheVolumeForClusterPeeringActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	return a.persistVolumeFieldsAfterClusterPeerCreated(ctx, result)
}

func (a *FlexCacheVolumeCreateActivity) WaitForClusterPeerActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	clusterPeer, err := provider.GetClusterPeer(result.ClusterPeeringRow.OntapPeerUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}

	if clusterPeer.AuthenticationState == vsa.ClusterPeerAuthenticationStateProblem || clusterPeer.AuthenticationState == vsa.ClusterPeerAuthenticationStateAbsent {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerError, fmt.Errorf("cluster peer authentication state is %s", clusterPeer.AuthenticationState)))
	}

	if clusterPeer.AuthenticationState == vsa.ClusterPeerAuthenticationStateOK && clusterPeer.Availability == vsa.ClusterPeerAvailabilityStateAvailable {
		return result, nil
	}
	// Peer not ready: return a retryable Temporal application error so retries carry structured ErrClusterPeerError
	// instead of ending with a generic DeadlineExceeded. Temporal keeps the last non timeout error (backoff/retry.go),
	// preserving tracking ID and readiness context. Temporal will only return a timeout error if the timeout occurs
	// while no activity is running or no activity has generated an error.
	return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerError, fmt.Errorf("cluster peer is not ready yet")))
}

func (a *FlexCacheVolumeCreateActivity) CreateSVMPeeringInOntapActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	volume := result.DBVolume
	cacheParams := volume.CacheParameters
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	params := vsa.CreateSVMPeerParams{
		LocalSVMName:    volume.Svm.Name,
		PeerSVMName:     cacheParams.PeerSvmName,
		PeerClusterName: cacheParams.PeerClusterName,
		// Adding support for both FlexCache and SnapMirror peering applications for reusing svm peer
		Applications: []ontaprestmodels.SvmPeerApplications{ontaprestmodels.SvmPeerApplicationsFlexcache, ontaprestmodels.SvmPeerApplicationsSnapmirror},
	}

	svmPeer, err := provider.CreateSVMPeer(params)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	result.SVMPeer = svmPeer

	// TODO: Add rollback logic when DB updates fail - VSCP-6386
	return a.persistVolumeFieldsAfterSVMPeerCreated(ctx, result)
}

func (a *FlexCacheVolumeCreateActivity) persistVolumeFieldsAfterSVMPeerCreated(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	cacheParams := volume.CacheParameters

	peerCommand := fmt.Sprintf("vserver peer accept -vserver %s -peer-vserver %s", cacheParams.PeerSvmName, volume.Svm.Name)

	cacheParams.Passphrase = nil
	cacheParams.CommandExpiryTime = nil
	cacheParams.Command = &peerCommand
	cacheParams.CacheStateDetailsCode = coremodels.WaitingForSVMPeeringCode
	cacheParams.CacheStateDetails = datamodel.WaitingForSVMPeering
	a.setCacheStates(result, cvpModels.FlexCacheV1betaCacheStatePENDINGSVMPEERING)

	updates := map[string]interface{}{
		"cache_parameters": volume.CacheParameters,
	}
	if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, updates); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	logger.Debug("svm peer command updated successfully")

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) UpdateFlexCacheVolumeForSVMPeeringActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	return a.persistVolumeFieldsAfterSVMPeerCreated(ctx, result)
}

func (a *FlexCacheVolumeCreateActivity) WaitForSVMPeeringActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	svmPeer, err := provider.GetSVMPeer(&result.DBVolume.Svm.Name, &result.DBVolume.CacheParameters.PeerSvmName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if svmPeer.State == vsa.SvmPeerStateRejected || svmPeer.State == vsa.SvmPeerStateSuspended {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSVMPeerError, fmt.Errorf("svm peer state is %s", svmPeer.State)))
	}

	if svmPeer.State == vsa.SvmPeerStatePeered {
		return result, nil
	}

	return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSVMPeerError, fmt.Errorf("svm peer is not ready yet")))
}

// UpdateFlexCacheVolumeLifecycleStateActivity updates the volume lifecycle state and any cache-related fields based
// on the provided targetState.
//
// Supported target states:
//
//	coremodels.LifeCycleStateCreating -> sets state to CREATING, clears peering command/passphrase, resets details
//	coremodels.LifeCycleStateREADY    -> sets state to READY, marks cache state PEERED
func (a *FlexCacheVolumeCreateActivity) UpdateFlexCacheVolumeLifecycleStateActivity(
	ctx context.Context,
	result *flexcache.CreateFlexCacheResult,
	targetState string,
) (*flexcache.CreateFlexCacheResult, error) {
	volume := result.DBVolume

	switch targetState {
	case datamodel.LifeCycleStateCreating:
		volume.State = datamodel.LifeCycleStateCreating
		volume.StateDetails = datamodel.LifeCycleStateCreatingDetails
		// Reset cache-specific transitional fields
		volume.CacheParameters.CacheStateDetailsCode = coremodels.DefaultCode
		volume.CacheParameters.CacheStateDetails = ""
		volume.CacheParameters.Command = nil
		volume.CacheParameters.CommandExpiryTime = nil
		volume.CacheParameters.Passphrase = nil
	case datamodel.LifeCycleStateREADY:
		volume.State = datamodel.LifeCycleStateREADY
		volume.StateDetails = datamodel.LifeCycleStateAvailableDetails
		// Move cache state to PEERED when volume is READY
		a.setCacheStates(result, cvpModels.FlexCacheV1betaCacheStatePEERED)
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("unsupported target lifecycle state: %s", targetState))
	}

	updates := map[string]interface{}{
		"cache_parameters": volume.CacheParameters,
		"state":            volume.State,
		"state_details":    volume.StateDetails,
	}
	if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, updates); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) HydrateFlexCacheState(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	if !isHydrationEnabled() {
		logger.Debugf("hydration is disabled, skipping HydrateFlexCacheState")
		return result, nil
	}

	volume := result.DBVolume
	callbackToken, err := authGenerateCallbackToken(ctx)
	if err != nil {
		logger.Error("Error when getting callback token", err)
		return result, err
	}

	err = commonHydrateFlexCacheState(ctx, logger, result.Event.LocationID, result.Event.ProjectNumber, volume.Name, volume.CacheParameters.CacheState, volume.State, callbackToken)
	if err != nil {
		logger.Errorf("Error when hydrating flexcache state: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrHydrateFlexCacheVolume, err)
	}

	logger.Debugf("hydration completed successfully for volume: %s", volume.Name)

	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) UpdateVolumeDetailsOnErrorActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) error {
	volume := result.DBVolume
	// Clear out peering fields on error except for state details
	volume.CacheParameters.Command = nil
	volume.CacheParameters.CommandExpiryTime = nil
	volume.CacheParameters.Passphrase = nil
	updates := map[string]interface{}{}

	if a.shouldSetErrorState(result) {
		a.setCacheStates(result, cvpModels.FlexCacheV1betaCacheStateERROR)
		updates["state"] = datamodel.LifeCycleStateError
		updates["state_details"] = datamodel.LifeCycleStateCreationErrorDetails
	}
	updates["cache_parameters"] = volume.CacheParameters

	if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, updates); err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (a *FlexCacheVolumeCreateActivity) setCacheStates(result *flexcache.CreateFlexCacheResult, state string) {
	result.DBVolume.CacheParameters.PreviousCacheState = result.DBVolume.CacheParameters.CacheState
	result.DBVolume.CacheParameters.CacheState = state
}

// shouldSetErrorState determines if the volume state should be set to error based on the CacheStateDetailsCode
func (a *FlexCacheVolumeCreateActivity) shouldSetErrorState(result *flexcache.CreateFlexCacheResult) bool {
	nonErrorCodes := []int{
		coremodels.ClusterPeeringExpiredCode,
		coremodels.SourceClusterUnreachableCode,
		coremodels.SVMPeeringExpiredCode,
		coremodels.ErrorDuringClusterPeerCode,
		coremodels.ErrorDuringSVMPeeringCode,
	}

	for _, code := range nonErrorCodes {
		if result.DBVolume.CacheParameters.CacheStateDetailsCode == code {
			return false
		}
	}

	return true
}

// CompletePeeringJobActivity completes the peering job when we reach PENDING_CLUSTER_PEERING.
func (a *FlexCacheVolumeCreateActivity) CompletePeeringJobActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) error {
	logger := utilGetLogger(ctx)
	resourceUUID := result.DBVolume.UUID
	clusterPeerRow := result.ClusterPeeringRow

	// Only complete the job if we are in PENDING_CLUSTER_PEERING (new cluster peer) or PEERED state (reuse cluster peer)
	if clusterPeerRow.State != datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING && clusterPeerRow.State != datamodel.CvpClusterPeeringStatusPEERED {
		logger.Debugf("Peering job for %s remains in %s)",
			resourceUUID, clusterPeerRow.State)
		return nil
	}

	_, err := a.completeJob(ctx, completeJobOpts{
		ResourceUUID:  resourceUUID,
		JobType:       string(datamodel.JobTypeFlexCacheEstablishPeering),
		GetErrCode:    vsaerrors.ErrDescribingJobNotFound,
		UpdateErrCode: vsaerrors.ErrEstablishPeeringJobFailed,
	})
	return err
}

// StartInternalJobActivity creates and starts the internal monitoring job
func (a *FlexCacheVolumeCreateActivity) StartInternalJobActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	input := result.JobInput
	logger.Debugf("Starting internal job for resource: %s (%s)", input.ResourceName, input.ResourceUUID)

	if existing, err := a.SE.GetJobByResourceUUID(ctx, input.ResourceUUID, string(datamodel.JobTypeFlexCacheInternalPeering)); err == nil && existing != nil {
		if existing.State == string(datamodel.JobsStatePROCESSING) {
			logger.Debugf("Internal job already exists for %s (state=%s)", input.ResourceUUID, existing.State)
			return result, nil
		}
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeFlexCacheInternalPeering),
		State:         string(datamodel.JobsStatePROCESSING),
		ResourceName:  input.ResourceName,
		AccountID:     sql.NullInt64{Int64: input.AccountID, Valid: true},
		CorrelationID: input.CorrelationID,
		RequestID:     input.RequestID,
		ScheduledAt:   time.Now(),
		IsAdminJob:    true,
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: input.ResourceUUID,
		},
	}

	createdJob, err := a.SE.CreateJobWithWorkflowID(ctx, job, input.WorkflowID)
	if err != nil {
		logger.Errorf("Failed to create internal job: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalPeeringJobFailed, err)
	}

	logger.Debugf("Started internal job: %s", createdJob.UUID)
	return result, nil
}

// CompleteInternalJobActivity completes the internal job once volume creation succeeds.
func (a *FlexCacheVolumeCreateActivity) CompleteInternalJobActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) error {
	logger := utilGetLogger(ctx)
	resourceUUID := result.DBVolume.UUID
	observedCacheState := result.DBVolume.CacheParameters.CacheState
	target := cvpModels.FlexCacheV1betaCacheStatePEERED

	if observedCacheState != target {
		logger.Debugf("Peering job for %s remains PROCESSING; waiting for state=%s (observed=%s)",
			resourceUUID, target, observedCacheState)
		return nil
	}

	_, err := a.completeJob(ctx, completeJobOpts{
		ResourceUUID:  resourceUUID,
		JobType:       string(datamodel.JobTypeFlexCacheInternalPeering),
		GetErrCode:    vsaerrors.ErrDescribingJobNotFound,
		UpdateErrCode: vsaerrors.ErrInternalPeeringJobFailed,
	})
	return err
}

// FailJobActivity marks the specified job as ERROR.
func (a *FlexCacheVolumeCreateActivity) FailJobActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) error {
	logger := utilGetLogger(ctx)

	resourceUUID := result.DBVolume.UUID
	jobType := result.ActiveJobType
	trackingID := result.ErrorTrackingID
	errorDetails := result.ErrorMessage

	logger.Warnf("Failing job for resource=%s type=%s", resourceUUID, jobType)

	jobs, err := a.getActiveJobs(ctx, resourceUUID, jobType)
	if err != nil {
		logger.Errorf("FailJobActivity: unable to get job for resource=%s type=%s: %v", resourceUUID, jobType, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobNotFound, err)
	}
	if jobs == nil || len(jobs) == 0 {
		logger.Errorf("FailJobActivity: job not found for resource=%s type=%s", resourceUUID, jobType)
		return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobNotFound,
			fmt.Errorf("job not found for resource=%s type=%s", resourceUUID, jobType))
	}
	// Ideally it should return only one active job of this type for the resourceUUID
	job := jobs[0]
	// Default to existing values if none provided, to avoid blanking info.
	if trackingID == 0 {
		trackingID = job.TrackingID
	}
	if errorDetails == "" {
		if job.ErrorDetails != "" {
			errorDetails = job.ErrorDetails
		} else {
			errorDetails = "unspecified error"
		}
	}

	if err := a.SE.UpdateJob(ctx, job.UUID, string(datamodel.JobsStateERROR), trackingID, errorDetails); err != nil {
		logger.Errorf("FailJobActivity: failed to update job %s to ERROR: %v", job.UUID, err)
		return vsaerrors.NewVCPError(mapJobTypeToError(string(jobType)), err)
	}

	logger.Warnf("FailJobActivity: job %s marked ERROR (type=%s; trackingID=%d)", job.UUID, jobType, trackingID)
	return nil
}

// completeJob is an internal helper to complete a job based on various criteria.
func (a *FlexCacheVolumeCreateActivity) completeJob(ctx context.Context, opts completeJobOpts) (*datamodel.Job, error) {
	logger := utilGetLogger(ctx)

	var (
		job  *datamodel.Job
		err  error
		jobs []*datamodel.Job
	)

	// Fetch the job  resourceUUID+jobType
	switch {
	case opts.ResourceUUID != "" && opts.JobType != "":
		// Ideally it should return only one active job of this type for the resourceUUID
		jobs, err = a.getActiveJobs(ctx, opts.ResourceUUID, datamodel.JobType(opts.JobType))
		if jobs != nil && len(jobs) > 0 {
			if len(jobs) > 1 {
				logger.Warnf("multiple active jobs found (resourceUUID=%s jobType=%s count=%d); using first job uuid=%s",
					opts.ResourceUUID, opts.JobType, len(jobs), jobs[0].UUID)
			}
			job = jobs[0]
		}
	default:
		logger.Errorf("invalid completeJob options: missing job identifiers")
		return nil, vsaerrors.NewVCPError(opts.GetErrCode, fmt.Errorf("invalid completeJob options: missing identifiers"))
	}

	if err != nil {
		logger.Errorf("failed to get job: %v", err)
		return nil, vsaerrors.NewVCPError(opts.GetErrCode, err)
	}
	if job == nil {
		logger.Errorf("job not found")
		return nil, vsaerrors.NewVCPError(opts.GetErrCode, fmt.Errorf("job not found"))
	}

	// Idempotent short-circuit
	switch job.State {
	case string(datamodel.JobsStateDONE):
		logger.Debugf("job already completed: %s", job.UUID)
		return job, nil
	case string(datamodel.JobsStateERROR):
		logger.Debugf("job is in error state: %s", job.UUID)
		return job, nil
	}

	// Mark as DONE
	if err := a.SE.UpdateJob(ctx, job.UUID, string(datamodel.JobsStateDONE), job.TrackingID, job.ErrorDetails); err != nil {
		logger.Errorf("failed to mark job %s as completed: %v", job.UUID, err)
		return nil, vsaerrors.NewVCPError(opts.UpdateErrCode, err)
	}
	job.State = string(datamodel.JobsStateDONE)
	logger.Debugf("marked job as completed: %s", job.UUID)

	return job, nil
}

// mapJobTypeToError maps job types to the closest VCP error code for reporting.
func mapJobTypeToError(jobType string) int {
	switch jobType {
	case string(datamodel.JobTypeFlexCacheEstablishPeering):
		return vsaerrors.ErrEstablishPeeringJobFailed
	case string(datamodel.JobTypeFlexCacheInternalPeering):
		return vsaerrors.ErrInternalPeeringJobFailed
	case string(datamodel.JobTypeFlexCacheCreateVolume):
		return vsaerrors.ErrCreatingFlexCacheVolume
	default:
		// Fallback to a generic error code
		return vsaerrors.ErrInternalPeeringJobFailed
	}
}

// EnsureClusterPeerInOntapActivity checks the status of an existing cluster peer or decides to create a new one.
func (a *FlexCacheVolumeCreateActivity) EnsureClusterPeerInOntapActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)

	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// No stored UUID -> create
	if result.ClusterPeeringRow == nil || result.ClusterPeeringRow.OntapPeerUUID == "" {
		result.ClusterPeerAction = flexcache.ActionCreate
		logger.Debug("cluster peer UUID absent; will create")
		return result, nil
	}

	clusterPeerUUID := result.ClusterPeeringRow.OntapPeerUUID
	// Fetch existing
	cp, getErr := provider.GetClusterPeer(clusterPeerUUID)
	if getErr != nil || cp == nil {
		result.ClusterPeerAction = flexcache.ActionCreate
		logger.Infof("stored cluster peer UUID not found; will create new (err=%v)", getErr)
		return result, nil
	}
	result.ClusterPeer = cp
	now := time.Now().UTC()

	switch {
	// Unrecoverable states -> recreate
	case cp.AuthenticationState == vsa.ClusterPeerAuthenticationStateProblem,
		cp.AuthenticationState == vsa.ClusterPeerAuthenticationStateAbsent,
		cp.Availability == vsa.ClusterPeerAvailabilityStateUnidentified:
		logger.Warnf("cluster peer invalid (auth=%s availability=%s); recreating",
			cp.AuthenticationState, cp.Availability)
		result.ClusterPeerAction = flexcache.ActionCreate
		return result, nil

	// Expired command and not yet peered -> recreate
	case cp.ExpiryTime != nil &&
		now.After(time.Time(*cp.ExpiryTime).UTC()) &&
		!(cp.AuthenticationState == vsa.ClusterPeerAuthenticationStateOK &&
			cp.Availability == vsa.ClusterPeerAvailabilityStateAvailable):
		logger.Infof("cluster peer command expired (uuid=%s); scheduling recreation", clusterPeerUUID)
		result.ClusterPeerAction = flexcache.ActionCreate
		return result, nil

	// Ready -> reuse
	case cp.AuthenticationState == vsa.ClusterPeerAuthenticationStateOK &&
		cp.Availability == vsa.ClusterPeerAvailabilityStateAvailable:
		result.ClusterPeerAction = flexcache.ActionReady
		logger.Debug("reusing existing cluster peer (ready)")
		return result, nil

	// Wait (pending or partial)
	case cp.AuthenticationState == vsa.ClusterPeerAuthenticationStatePending ||
		cp.Availability == vsa.ClusterPeerAvailabilityStatePending ||
		cp.Availability == vsa.ClusterPeerAvailabilityStatePartial:
		result.ClusterPeerAction = flexcache.ActionWait
		logger.Debugf("cluster peer not ready (auth=%s availability=%s); will wait",
			cp.AuthenticationState, cp.Availability)
		return result, nil

	// Fallback -> recreate
	default:
		logger.Warnf("unknown cluster peer state (auth=%s availability=%s); recreating",
			cp.AuthenticationState, cp.Availability)
		result.ClusterPeerAction = flexcache.ActionCreate
		return result, nil
	}
}

// EnsureSVMPeerInOntapActivity checks the status of an existing SVM peer or decides to create a new one.
func (a *FlexCacheVolumeCreateActivity) EnsureSVMPeerInOntapActivity(ctx context.Context, result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume

	provider, err := hyperscalerGetProviderByNode(ctx, result.Node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Fetch existing (lookup by SVM names; UUID retained for later persistence steps)
	svmPeer, err := provider.GetSVMPeer(&volume.Svm.Name, &volume.CacheParameters.PeerSvmName)
	if err != nil {
		// retryable if not found and we have no existing peer
		if customerrors.IsNotFoundErr(err) {
			result.SVMPeerAction = flexcache.ActionCreate
			logger.Infof("stored SVM peer UUID not found; will create new (err=%v)", err)
			return result, nil
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	result.SVMPeer = svmPeer

	switch {
	// Invalid states -> recreate
	case svmPeer.State == vsa.SvmPeerStateRejected,
		svmPeer.State == vsa.SvmPeerStateSuspended:
		logger.Warnf("SVM peer invalid (state=%s); recreating", svmPeer.State)
		result.SVMPeerAction = flexcache.ActionCreate
		return result, nil

	// Ready -> reuse
	case svmPeer.State == vsa.SvmPeerStatePeered:
		result.SVMPeerAction = flexcache.ActionReady
		logger.Debug("reusing existing SVM peer (ready)")
		return result, nil

	// Wait
	case svmPeer.State == vsa.SvmPeerStatePending:
		result.SVMPeerAction = flexcache.ActionWait
		logger.Debugf("SVM peer not ready (state=%s); will wait", svmPeer.State)
		return result, nil

	// Fallback unknown -> recreate
	default:
		logger.Warnf("unknown SVM peer state (state=%s); recreating", svmPeer.State)
		result.SVMPeerAction = flexcache.ActionCreate
		return result, nil
	}
}

func (a *FlexCacheVolumeCreateActivity) GetClusterPeeringRowFromDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	volume := result.DBVolume
	cacheParams := volume.CacheParameters

	existingPeer, err := a.SE.GetClusterPeerByAccountIDExternalClusterAndPoolID(ctx, volume.Account.ID,
		cacheParams.PeerClusterName, volume.Pool.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Debugf("Cluster peering row not available (account=%d cluster=%s pool=%d)",
				volume.Account.ID, cacheParams.PeerClusterName, volume.Pool.ID)
			return result, nil
		}
		logger.Errorf("Failed to get cluster peering row from database: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if existingPeer.State == datamodel.CvpClusterPeeringStatusERROR {
		logger.Infof("Cluster peering row in error state (uuid=%s account=%d cluster=%s pool=%d); skipping",
			existingPeer.UUID, volume.Account.ID, cacheParams.PeerClusterName, volume.Pool.ID)
		result.ClusterPeeringRow = nil
		return result, nil
	}

	result.ClusterPeeringRow = existingPeer
	logger.Debugf("Found existing cluster peering row in database: %s", existingPeer.UUID)
	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) CreateClusterPeeringRowInDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	if result.ClusterPeeringRow != nil {
		logger.Debugf("CreateClusterPeeringRowInDBActivity: existing row uuid=%s (no-op)", result.ClusterPeeringRow.UUID)
		return result, nil
	}

	vol := result.DBVolume
	cp := vol.CacheParameters

	newRow := &datamodel.ClusterPeerings{
		State:          datamodel.CvpClusterPeeringStatusCREATING,
		AccountID:      vol.Account.ID,
		PoolID:         vol.Pool.ID,
		OnprempCluster: cp.PeerClusterName,
	}
	newRow.UUID = utils.RandomUUID()

	created, err := a.SE.CreateClusterPeeringRow(ctx, newRow)
	if err != nil {
		logger.Errorf("CreateClusterPeeringRowInDBActivity: create failed: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	result.ClusterPeeringRow = created
	logger.Infof("CreateClusterPeeringRowInDBActivity: created row uuid=%s", created.UUID)
	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) updateClusterPeeringRowStateInDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult, targetState datamodel.ClusterPeeringStatus) (*flexcache.CreateFlexCacheResult, error) {
	logger := utilGetLogger(ctx)
	row := result.ClusterPeeringRow
	vol := result.DBVolume

	switch targetState {
	case datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING:
		if result.ClusterPeer != nil {
			row.OntapPeerUUID = result.ClusterPeer.ExternalUUID
			row.OnprempCluster = result.ClusterPeer.PeerClusterName
		}
		row.ClusterPeeringAttributes = &datamodel.ClusterPeeringAttributes{
			PassPhrase: vol.CacheParameters.Passphrase,
			Command:    vol.CacheParameters.Command,
			ExpiryTime: vol.CacheParameters.CommandExpiryTime,
		}
		row.State = datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING

	case datamodel.CvpClusterPeeringStatusPEERED:
		row.State = datamodel.CvpClusterPeeringStatusPEERED

	case datamodel.CvpClusterPeeringStatusERROR:
		row.State = datamodel.CvpClusterPeeringStatusERROR

	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("unsupported target state: %s", targetState))
	}

	if err := a.SE.UpdateClusterPeeringRow(ctx, row); err != nil {
		logger.Errorf("Failed to update cluster peering row (state=%s): %v", targetState, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Cluster peering row %s updated to state %s", row.UUID, row.State)
	return result, nil
}

func (a *FlexCacheVolumeCreateActivity) UpdateClusterPeeringRowStatePendingInDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	return a.updateClusterPeeringRowStateInDBActivity(ctx, result, datamodel.CvpClusterPeeringStatusPENDINGCLUSTERPEERING)
}

func (a *FlexCacheVolumeCreateActivity) UpdateClusterPeeringRowStatePeeredInDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	return a.updateClusterPeeringRowStateInDBActivity(ctx, result, datamodel.CvpClusterPeeringStatusPEERED)
}

func (a *FlexCacheVolumeCreateActivity) UpdateClusterPeeringRowStateErrorInDBActivity(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	return a.updateClusterPeeringRowStateInDBActivity(ctx, result, datamodel.CvpClusterPeeringStatusERROR)
}

func (a *FlexCacheVolumeCreateActivity) UpdateClusterPeeringInVolume(ctx context.Context,
	result *flexcache.CreateFlexCacheResult) (*flexcache.CreateFlexCacheResult, error) {
	se := a.SE
	logger := utilGetLogger(ctx)
	logger.Debugf("UpdateClusterPeeringInVolume - Starting update of cluster peering in volume")
	dbVolume := result.DBVolume
	clusterPeerID := sql.NullInt64{Int64: result.ClusterPeeringRow.ID, Valid: true}
	dbVolume.ClusterPeerID = clusterPeerID

	update := map[string]interface{}{
		"cluster_peer_id": clusterPeerID,
	}
	// Update the volume in the database
	if err := se.UpdateVolumeFields(ctx, dbVolume.UUID, update); err != nil {
		logger.Errorf("Failed to update volume cluster peer ID: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	result.DBVolume = dbVolume
	logger.Debugf("Volume %s cluster peer reference updated successfully in the database", dbVolume.Name)
	return result, nil
}

// getActiveJobs builds the active (non-terminal) job filter and executes the query.
// Returns all jobs that are not DONE or ERROR for the given resource/jobType.
func (a *FlexCacheVolumeCreateActivity) getActiveJobs(ctx context.Context, resourceUUID string,
	jobType datamodel.JobType) ([]*datamodel.Job, error) {
	resourceUUIDCol := "job_attributes ->> 'resource_uuid'"
	if utils.EnableJobResourceUUIDIndex {
		resourceUUIDCol = "resource_uuid"
	}
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition(resourceUUIDCol, "=", resourceUUID),
		dbutils.NewFilterCondition("type", "=", jobType),
		dbutils.NewFilterCondition("state", "!=", string(datamodel.JobsStateDONE)),
		dbutils.NewFilterCondition("state", "!=", string(datamodel.JobsStateERROR)),
	)
	return a.SE.GetJobsWithCondition(ctx, *filter)
}

// EnsureExternalPeerRole creates the "external-peer" role if it doesn't exist.
// This role is used for both FlexCache and hybrid replication cluster peers.
// It's idempotent - safe to call multiple times.
func EnsureExternalPeerRole(ctx context.Context, provider vsa.Provider) error {
	logger := utilGetLogger(ctx)

	rolePrivileges := activities.GetExternalPeerRolePrivileges()
	roleParams := vsa.CreateRoleParams{
		Name:       activities.OnPremPeerRoleName,
		Privileges: rolePrivileges,
	}

	_, err := provider.CreateRole(roleParams)
	if err != nil {
		// Role already exists - that's fine, continue
		if strings.Contains(err.Error(), "Role already exists") ||
			strings.Contains(err.Error(), "Role already exists in legacy role table") {
			logger.Debugf("Role %s already exists, skipping creation", activities.OnPremPeerRoleName)
			return nil
		}
		// Other errors are real failures
		logger.Errorf("Failed to create role %s: %v", activities.OnPremPeerRoleName, err)
		return err
	}

	logger.Infof("Successfully created role %s", activities.OnPremPeerRoleName)
	return nil
}

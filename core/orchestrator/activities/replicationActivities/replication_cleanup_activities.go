package replicationActivities

import (
	"context"
	"strings"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type CleanupVolumeReplicationActivity struct {
	SE database.Storage
}

func (a *CleanupVolumeReplicationActivity) GetSrcBasePathCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	srcBasePath, err := GetBasePath(ctx, result.Event.ReplicationModel.ReplicationAttributes.SourceLocation)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGetSrcBasePath, err)
	}
	result.SrcBasePath = srcBasePath
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) GetDstBasePathCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	dstBasePath, err := GetBasePath(ctx, result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGetDstBasePath, err)
	}
	result.DstBasePath = dstBasePath
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) GetSignedSrcTokenCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	srcJwt, err := GetSignedToken(ctx, *result.SrcProjectNumber)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
	}
	result.SrcJwtToken = srcJwt
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) GetSignedDstTokenCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	if *result.SrcProjectNumber == *result.DstProjectNumber {
		result.DstJwtToken = result.SrcJwtToken
		return result, nil
	}
	dstJwt, err := GetSignedToken(ctx, *result.DstProjectNumber)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGetSignedToken, err)
	}
	result.DstJwtToken = dstJwt
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) DeleteReplicationOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("DeleteReplicationOnDestinationForCleanup")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
		ProjectNumber:       *result.DstProjectNumber,
		LocationId:          result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeReplicationId: result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
		XCorrelationID:      googleproxyclient.NewOptString(*result.CorrelationID),
		IsCleanup:           googleproxyclient.NewOptBool(true),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.VolumeReplicationInternalV1beta:
		result.JobId = r.Jobs[0].JobId.Value
		return result, nil
	case *googleproxyclient.V1betaInternalDeleteVolumeReplicationBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeReplicationInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeReplicationUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeReplicationForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeReplicationNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeReplicationError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) DeleteSnapmirrorSnapshotsOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("DeleteSnapmirrorSnapshotsOnDestination For cleanup")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	deleteSmSnapshotsParam := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeId:       result.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(*result.CorrelationID),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParam)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrDeleteSnapshot, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		result.JobId = strings.Split(r.Name.Value, "/")[7]
		return result, nil
	case *googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalDeleteVolumeSnapmirrorSnapshotDestinationError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) GetReplicationOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("GetReplicationOnDestinationForCleanup")
	if result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID == "" {
		logger.Debugf("DestinationReplicationUUID is empty, skipping replication retrieval")
		return result, nil
	}
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	params := &googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		XCorrelationID: googleproxyclient.NewOptString(*result.CorrelationID),
	}
	body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID}}
	res, err := googleProxyClient.Invoker.V1betaGetMultipleReplicationsInternal(ctx, &body, *params)
	if err != nil {
		logger.Error("Failed to get multiple replications from Google Proxy", "error", err)
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalOK:
		result.DstReplication = &r.Replications[0]
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsBadRequest, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsInternalServerError, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsUnauthorized, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsForbidden, errors.New(r.Message))
	case *googleproxyclient.V1betaGetMultipleReplicationsInternalNotFound:
		return result, nil
	}
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) GetDestinationVolumeForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("GetDestinationVolumeForCleanup")
	if result.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID == "" {
		logger.Debugf("DestinationVolumeUUID is empty, skipping volume retrieval")
		return result, nil
	}
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	params := &googleproxyclient.V1betaDescribeVolumeParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeId:       result.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(*result.CorrelationID),
	}
	res, err := googleProxyClient.Invoker.V1betaDescribeVolume(ctx, *params)
	if err != nil {
		logger.Error("Failed to get volume from Google Proxy", "error", err)
		return nil, errors.NewVCPError(errors.ErrListVolumes, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.V1betaDescribeVolumeNotFound:
		return result, nil
	case *googleproxyclient.VolumeV1beta:
		volumeResponse := res.(*googleproxyclient.VolumeV1beta)
		result.DstVolume = volumeResponse
	case *googleproxyclient.V1betaDescribeVolumeBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsBadRequest, errors.New(r.Message))
	case *googleproxyclient.V1betaDescribeVolumeInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsInternalServerError, errors.New(r.Message))
	case *googleproxyclient.V1betaDescribeVolumeUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsUnauthorized, errors.New(r.Message))
	case *googleproxyclient.V1betaDescribeVolumeForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalGetMultipleReplicationsForbidden, errors.New(r.Message))
	}
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) DescribeRemoteJobForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) error {
	err := activities.DescribeJob(ctx, &result.JobId, result.DstBasePath, result.DstJwtToken, result.DstProjectNumber, &result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation, result.Event.XCorrelationID)
	if err != nil {
		return err
	}
	return nil
}

func (a *CleanupVolumeReplicationActivity) UpdateReplicationOnDestinationToErrorStateForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	return a.updateReplicationToErrorStateForCleanup(ctx, result, "destination")
}

func (a *CleanupVolumeReplicationActivity) UpdateReplicationOnSourceToErrorStateForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	return a.updateReplicationToErrorStateForCleanup(ctx, result, "source")
}

func (a *CleanupVolumeReplicationActivity) updateReplicationToErrorStateForCleanup(ctx context.Context, result *replication.DeleteReplicationResult, target string) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Release ReplicationOn %s", target)

	var basePath, jwtToken, projectNumber, locationId, replicationUUID string
	if target == "source" {
		basePath = *result.SrcBasePath
		jwtToken = *result.SrcJwtToken
		projectNumber = *result.SrcProjectNumber
		locationId = result.Event.ReplicationModel.ReplicationAttributes.SourceLocation
		replicationUUID = result.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID
	} else {
		basePath = *result.DstBasePath
		jwtToken = *result.DstJwtToken
		projectNumber = *result.DstProjectNumber
		locationId = result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation
		replicationUUID = result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID
	}

	if target == "destination" && replicationUUID == "" {
		logger.Infof("DestinationReplicationUUID is empty, skipping replication state update")
		return result, nil
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
		State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
		StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
	}
	updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
		ProjectNumber:       projectNumber,
		LocationId:          locationId,
		VolumeReplicationId: replicationUUID,
		XCorrelationID:      googleproxyclient.NewOptString(*result.CorrelationID),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalUpdateState(ctx, &updateRequest, updateParams)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.VolumeReplicationUpdateStateInternalV1beta:
		return result, nil
	case *googleproxyclient.V1betaInternalUpdateStateBadRequest:
		if strings.Contains(strings.ToLower(r.Message), "volume replication not found") {
			logger.Infof("Volume replication not found, treating as successful no-op: %s", r.Message)
			return result, nil
		}
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalUpdateStateInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalUpdateStateUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalUpdateStateForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalUpdateStateNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyUpdateReplicationState, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) StopReplicationOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("StopReplicationOnDestinationForCleanup")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	stopReplicationParams := &googleproxyclient.V1betaInternalStopVolumeReplicationParams{
		ProjectNumber:       *result.DstProjectNumber,
		LocationId:          result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeReplicationId: result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
		XCorrelationID:      googleproxyclient.NewOptString(*result.CorrelationID),
	}
	stopReplicationReq := &googleproxyclient.V1betaInternalStopVolumeReplicationReq{
		Force: googleproxyclient.NewOptBool(true),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalStopVolumeReplication(ctx, stopReplicationReq, *stopReplicationParams)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopReplication, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.VolumeReplicationInternalV1beta:
		result.DstReplication = r
		result.JobId = r.Jobs[0].JobId.Value
		return result, nil
	case *googleproxyclient.V1betaInternalStopVolumeReplicationBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalStopVolumeReplicationInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalStopVolumeReplicationUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalStopVolumeReplicationForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalStopVolumeReplicationNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalStopVolumeReplicationError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) UpdateReplicationRecordOnSourceForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("ReleaseReplicationOnSourceForCleanup")
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.SrcBasePath, *result.SrcJwtToken, logger)
	releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
		ProjectNumber:       *result.SrcProjectNumber,
		LocationId:          result.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
		VolumeReplicationId: result.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
		XCorrelationID:      googleproxyclient.NewOptString(*result.CorrelationID),
		IsCleanup:           googleproxyclient.NewOptBool(true),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		result.JobId = strings.Split(r.Name.Value, "/")[7]
		return result, nil
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) UpdateReplicationRecordOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Release Replication On Destination")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
		ProjectNumber:       *result.DstProjectNumber,
		LocationId:          result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeReplicationId: result.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
		XCorrelationID:      googleproxyclient.NewOptString(*result.CorrelationID),
		IsCleanup:           googleproxyclient.NewOptBool(true),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		result.JobId = strings.Split(r.Name.Value, "/")[7]
		return result, nil
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	case *googleproxyclient.V1betaInternalReleaseVolumeReplicationNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyInternalReleaseVolumeReplicationError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) DescribeSourceJobForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) error {
	err := activities.DescribeJob(ctx, &result.JobId, result.SrcBasePath, result.SrcJwtToken, result.SrcProjectNumber, &result.Event.ReplicationModel.ReplicationAttributes.SourceLocation, result.Event.XCorrelationID)
	if err != nil {
		return err
	}
	return nil
}

func (a *CleanupVolumeReplicationActivity) DeHydrateDestinationVolumeReplicationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Dehydrating destination volume replication for cleanup")

	if hydrationEnabled {
		currentLocation := result.Event.Location
		var remoteLocation, remoteVolume, remoteProject string
		var err error
		if currentLocation == result.Event.ReplicationModel.ReplicationAttributes.SourceLocation {
			remoteLocation = result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation
			remoteVolume = result.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeName
			remoteProject = result.Event.DestinationProjectNumber
		} else {
			remoteLocation = result.Event.ReplicationModel.ReplicationAttributes.SourceLocation
			remoteVolume = result.Event.ReplicationModel.ReplicationAttributes.SourceVolumeName
			remoteProject = result.Event.SourceProjectNumber
		}
		err = deHydrateVolumeReplication(ctx, convertVolumeReplicationV1BetaToVolumeModel(result.Event.ReplicationModel.Name, remoteLocation, remoteVolume), remoteProject)
		if err != nil {
			if !vsaerrors.IsNotFoundErr(err) {
				return nil, errors.WrapAsNonRetryableTemporalApplicationError(errors.NewVCPError(errors.ErrDeHydrateVolumeReplication, err))
			}
		}
	}
	return result, nil
}

func (a *CleanupVolumeReplicationActivity) DeleteVolumeOnDestinationForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("DeleteVolumeOnDestination")
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)
	params := &googleproxyclient.V1betaDeleteVolumeParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
		VolumeId:       result.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(*result.CorrelationID),
	}
	body := googleproxyclient.OptV1betaDeleteVolumeReq{
		Set:   true,
		Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
	}
	res, err := googleProxyClient.Invoker.V1betaDeleteVolume(ctx, body, *params)
	if err != nil {
		return nil, errors.NewVCPError(errors.ErrDeleteVolume, err)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		volume := googleproxyclient.VolumeV1beta{}
		err := replication.JsonUnMarshal(r.Response, &volume)
		if err != nil {
			return nil, errors.NewVCPError(errors.ErrorFailedToUnmarshal, err)
		}
		result.JobId = strings.Split(r.Name.Value, "/")[7]
		result.DstVolume = &volume
		return result, nil
	case *googleproxyclient.V1betaDeleteVolumeBadRequest:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New(r.Message))
	case *googleproxyclient.V1betaDeleteVolumeInternalServerError:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New(r.Message))
	case *googleproxyclient.V1betaDeleteVolumeUnauthorized:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New(r.Message))
	case *googleproxyclient.V1betaDeleteVolumeForbidden:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New(r.Message))
	case *googleproxyclient.V1betaDeleteVolumeNotFound:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New(r.Message))
	default:
		return nil, errors.NewVCPError(errors.ErrGoogleProxyDeleteVolumeError, errors.New("unknown response type"))
	}
}

func (a *CleanupVolumeReplicationActivity) DeHydrateDestinationVolumeForCleanup(ctx context.Context, result *replication.DeleteReplicationResult) (*replication.DeleteReplicationResult, error) {
	if hydrationEnabled {
		err := deHydrateVolume(ctx, convertVolumeV1BetaToVolumeModelForCleanup(*result.DstVolume, result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation), *result.DstProjectNumber)
		if err != nil {
			return nil, errors.WrapAsNonRetryableTemporalApplicationError(errors.NewVCPError(errors.ErrDeHydrateVolume, err))
		}
	}
	return result, nil
}

func convertVolumeV1BetaToVolumeModelForCleanup(vol googleproxyclient.VolumeV1beta, dstLocation string) models.Volume {
	protocols := make([]string, 0)
	for _, protocol := range vol.Protocols {
		protocolStr, err := protocol.MarshalText()
		if err != nil {
			return models.Volume{}
		}
		protocols = append(protocols, string(protocolStr))
	}
	return models.Volume{
		BaseModel: models.BaseModel{
			UUID: vol.VolumeId.Value,
		},
		DisplayName:    vol.ResourceId,
		QuotaInBytes:   uint64(vol.QuotaInBytes.Value),
		LifeCycleState: string(vol.VolumeState.Value),
		ProtocolTypes:  protocols,
		Region:         dstLocation,
	}
}

package replicationActivities

import (
	"context"
	"strings"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	convertReplicationScheduleToInternalReplicationSchedule = _convertReplicationScheduleToInternalReplicationSchedule
	convertVolumeReplicationCreateParams                    = _convertVolumeReplicationCreateParams
	hydrateVolume                                           = HydrateVolume
	describeVolume                                          = DescribeVolume
)

const (
	snapmirrorFirewallName      = "ingress-snapmirror"
	snapmirrorFirewallPriority  = int64(1000)
	snapmirrorFirewallDirection = "INGRESS"
)

var (
	snapmirrorFirewallSourceRanges = env.GetString("DATA_FIREWALL_SOURCE_RANGES", "")
	snapmirrorFirewallPortRules    = env.GetString("INTERCLUSTER_FIREWALL_PORT_RULES", "tcp,10566,11104,11105")
)

type VolumeReplicationCreateActivity struct {
	SE database.Storage
}

func (a *VolumeReplicationCreateActivity) GetSourceInterclusterLifs(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("GetSourcePoolDetails for pool: %s", result.Event.SourcePool.Name)
	safeRecordHeartbeat(ctx, "GetSourceInterclusterLifs started")
	provider, err := vsa.GetProviderByNode(ctx, result.SrcNode)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	interClusterLifs, err := provider.GetInterclusterLIFs(vsa.InterclusterServicePolicyName)
	if err != nil {
		logger.Error("Failed to get interCluster lifs", "error", err)
		return nil, err
	}
	var icLifs []string
	for _, icLif := range interClusterLifs {
		icLifs = append(icLifs, string(icLif.Address))
	}
	result.SrcIps = icLifs
	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetDestinationPoolDetails(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("GetDestinationPoolDetails for pool: %s", result.Event.DestinationPoolName)
	safeRecordHeartbeat(ctx, "GetDestinationPoolDetails started")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
		PoolName:       result.Event.DestinationPoolName,
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.DestinationLocationID,
		XCorrelationID: googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalDescribePool(ctx, *describePoolParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.PoolInternalV1beta:
		if r.GetHasActiveClusterUpgrade().IsSet() && r.GetHasActiveClusterUpgrade().Value {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrStoragePoolTemporarilyUnavailable, errors.New("storage pool is temporarily unavailable, please try again later")))
		}
		result.DstPool = r
		result.DstIps = r.InterclusterLifs
		return result, nil
	case *googleproxyclient.V1betaInternalDescribePoolBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolMethodNotAllowed:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribePoolInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalDescribePoolAPI, errors.New("unexpected response type from Google Proxy")))
	}
}

func (j *VolumeReplicationCreateActivity) CreateClusterPeering(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("CreateClusterPeering")
	safeRecordHeartbeat(ctx, "CreateClusterPeering started")

	node := result.SrcNode
	expiryTime := time.Now().Add(time.Minute * 10) // Default expiry time of 10 mins
	clustePeerParams := &commonparams.ClusterPeerParams{
		PeerName:      result.DstPool.ClusterName.Value,
		PeerAddresses: result.DstIps,
		ExpiryTime:    &expiryTime,
	}
	resp, err := activities.CreateClusterPeer(ctx, clustePeerParams, node)
	if err != nil {
		return nil, err
	}
	result.ClusterPeerUUID = &resp.UUID
	result.Passphrase = (*string)(resp.Passphrase)
	return result, nil
}

func (a *VolumeReplicationCreateActivity) AcceptClusterPeering(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	if result.Passphrase == nil {
		return result, nil
	}
	logger := util.GetLogger(ctx)
	logger.Debugf("AcceptClusterPeering")
	safeRecordHeartbeat(ctx, "AcceptClusterPeering started")
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	accpetClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.DestinationLocationID,
		XCorrelationID: googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	expiryTime := time.Now().Add(time.Minute * 10)
	body := &googleproxyclient.ClusterPeerV1{
		PeerAddresses:   result.SrcIps,
		PeerClusterName: result.Event.SourcePool.ClusterDetails.ExternalName,
		Passphrase:      *result.Passphrase,
		PoolUUID:        result.DstPool.PoolId.Value,
		ExpiryTime:      googleproxyclient.NewOptNilDateTime(expiryTime),
	}
	res, err := googleProxyClient.Invoker.V1betaInternalAcceptClusterPeer(ctx, body, *accpetClusterPeerParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.ClusterPeerV1:
		result.JobId = &r.Jobs[0].JobId.Value
		return result, nil
	case *googleproxyclient.V1betaInternalAcceptClusterPeerBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalAcceptClusterPeerUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalAcceptClusterPeerForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalAcceptClusterPeerNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalAcceptClusterPeerConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalAcceptClusterPeerInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalAcceptClusterPeerAPI, errors.New("unexpected response type from Google Proxy")))
	}
}

func (a *VolumeReplicationCreateActivity) CreateDestinationVolume(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("CreateDestinationVolume")
	safeRecordHeartbeat(ctx, "CreateDestinationVolume started")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.DestinationLocationID,
		XCorrelationID: googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	body := &googleproxyclient.VolumeCreateV1beta{
		Volume:     convertSourceVolumeToDestinationVolume(result),
		VolumeType: googleproxyclient.OptVolumeCreateV1betaVolumeType{Value: googleproxyclient.VolumeCreateV1betaVolumeTypeSECONDARY, Set: true},
	}

	res, err := googleProxyClient.Invoker.V1betaCreateVolume(ctx, body, *createVolumeParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		volume := gcpserver.VolumeV1beta{}
		err := replication.JsonUnMarshal(r.Response, &volume)
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrorFailedToUnmarshal, err)
		}
		result.JobId = &strings.Split(r.Name.Value, "/")[7]
		result.DstVolume = &volume
		return result, nil
	case *googleproxyclient.V1betaCreateVolumeBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaCreateVolumeUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaCreateVolumeForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaCreateVolumeConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaCreateVolumeInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreatingDestinationVolume, errors.New("unexpected response type from Google Proxy")))
	}
}

func DescribeVolume(ctx context.Context, result *replication.CreateReplicationResult) (*googleproxyclient.InternalVolumeV1beta, error) {
	logger := util.GetLogger(ctx)
	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	createVolumeParams := &googleproxyclient.V1betaInternalDescribeVolumeParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.DestinationLocationID,
		VolumeId:       result.DstVolume.VolumeId.Value,
		XCorrelationID: googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalDescribeVolume(ctx, *createVolumeParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.InternalVolumeV1beta:
		return r, nil
	case *googleproxyclient.V1betaInternalDescribeVolumeBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribeVolumeUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribeVolumeForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribeVolumeNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalDescribeVolumeInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDescribingDestinationVolume, errors.New("unexpected response type from Google Proxy")))
	}
}

func (a *VolumeReplicationCreateActivity) HydrateDestinationVolume(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	if hydrationEnabled {
		logger := util.GetLogger(ctx)
		logger.Debugf("HydrateDestinationVolume")
		safeRecordHeartbeat(ctx, "HydrateDestinationVolume started")
		err := hydrateVolume(ctx, convertVolumeV1BetaToVolumeModel(*result.DstVolume, result.Event.DestinationLocationID), *result.DstProjectNumber, result.DstPool.ResourceId)
		if err != nil {
			logger.Errorf("Failed to hydrate destination volume: %v", err)
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHydrateVolumeCreate, err))
		}
	}
	return result, nil
}

func convertVolumeV1BetaToVolumeModel(vol gcpserver.VolumeV1beta, dstLocation string) models.Volume {
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

func (a *VolumeReplicationCreateActivity) CreateReplicationOnDestination(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("CreateReplicationOnDestination")
	safeRecordHeartbeat(ctx, "CreateReplicationOnDestination started")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	createVolumeParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
		ProjectNumber:  *result.DstProjectNumber,
		LocationId:     result.Event.DestinationLocationID,
		XCorrelationID: googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	body := convertVolumeReplicationCreateParams(*result)

	res, err := googleProxyClient.Invoker.V1betaInternalCreateVolumeReplication(ctx, &body, *createVolumeParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.VolumeReplicationInternalV1beta:
		result.DstReplication = r
		result.JobId = &r.Jobs[0].JobId.Value
		return result, nil
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalCreateVolumeReplicationConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrCreateInternalReplication, errors.New("unexpected response type from Google Proxy")))
	}
}

func (a *VolumeReplicationCreateActivity) UpdateReplicationState(ctx context.Context, volumeRep datamodel.VolumeReplication) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	err := se.UpdateVolumeReplicationStates(ctx, &volumeRep)
	if err != nil {
		return err
	}
	logger.Debugf("Volume Replication state: %s updated successfully in the db", volumeRep.Name)

	return nil
}

func (a *VolumeReplicationCreateActivity) UpdateDestinationVolumeDetails(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	volumeRep := result.DbVolReplication
	volumeRep.ReplicationAttributes.DestinationVolumeUUID = result.DstVolume.VolumeId.Value
	volumeRep.ReplicationAttributes.DestinationVolumeName = result.DstVolume.ResourceId
	err := se.UpdateVolumeReplication(ctx, volumeRep)
	if err != nil {
		return nil, err
	}
	result.DbVolReplication = volumeRep

	logger.Debugf("Volume Replication state: %s updated successfully in the db", volumeRep.Name)

	return result, nil
}

func (a *VolumeReplicationCreateActivity) UpdateDestinationVolumeReplicationDetails(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	volumeRep := result.DbVolReplication
	volumeRep.ReplicationAttributes.DestinationPoolUUID = result.DstPool.PoolId.Value
	volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
	volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
	err := se.UpdateVolumeReplication(ctx, volumeRep)
	if err != nil {
		return nil, err
	}
	result.DbVolReplication = volumeRep

	logger.Debugf("Volume Replication state: %s update successfully in the db", volumeRep.Name)

	return result, nil
}

func (a *VolumeReplicationCreateActivity) UpdateReplicationDetails(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	volumeRep := result.DbVolReplication
	volumeRep.State = datamodel.LifeCycleStateAvailable
	volumeRep.StateDetails = datamodel.LifeCycleStateAvailableDetails
	volumeRep.ReplicationAttributes.SourceSvmName = *result.SrcSvm
	volumeRep.ReplicationAttributes.DestinationSvmName = *result.DstSvm
	volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
	volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
	volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)

	err := se.UpdateVolumeReplication(ctx, volumeRep)
	if err != nil {
		return nil, err
	}
	result.DbVolReplication = volumeRep
	logger.Debugf("Volume Replication state: %s update successfully in the db", volumeRep.Name)

	return result, nil
}

func (a *VolumeReplicationCreateActivity) AcceptSvmPeer(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("AcceptSvmPeer")
	safeRecordHeartbeat(ctx, "AcceptSvmPeer started")

	provider, err := vsa.GetProviderByNode(ctx, result.SrcNode)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	svmPeer, err := provider.GetSVMPeer(result.SrcSvm, result.DstSvm)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGettingSvmPeer, err)
	}
	if svmPeer.State == "peered" && svmPeer.PeerSvmName == *result.DstSvm {
		// SVMs are already peered
		logger.Infof("SVMs already peered")
		return result, nil
	}
	err = provider.AcceptSvmPeering(*result.SrcSvm, *result.DstSvm)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrAcceptSvmPeer, err)
	}

	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetVolumeSVMNames(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("GetVolumeSVMNames")

	se := a.SE
	srcVol, err := se.DescribeVolume(ctx, result.Event.SourceVolume.UUID)
	if err != nil {
		logger.Error("Failed to describe source volume", "error", err)
		return nil, err
	}
	if srcVol.Svm == nil || srcVol.Svm.Name == "" {
		logger.Error("Source volume SVM name not found")
		return nil, vsaerrors.New("Source volume SVM name not found")
	}

	dstVol, err := describeVolume(ctx, result)
	if err != nil {
		logger.Error("Failed to describe destination volume", "error", err)
		return nil, err
	}
	if !dstVol.SvmName.IsSet() {
		logger.Error("Destination volume SVM name not found")
		return nil, vsaerrors.New("Destination volume SVM name not found")
	}

	dstSvm := dstVol.SvmName.Value
	result.SrcSvm = &srcVol.Svm.Name
	result.DstSvm = &dstSvm
	logger.Infof("Src svm name : %s, Dst svm name : %s", *result.SrcSvm, *result.DstSvm)
	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetSrcBasePath(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	srcBasePath, err := GetBasePath(ctx, result.Event.SourceRegion)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGetSrcBasePath, err)
	}
	result.SrcBasePath = srcBasePath
	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetDstBasePath(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	dstBasePath, err := GetBasePath(ctx, result.Event.DestinationRegion)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGetDstBasePath, err)
	}
	result.DstBasePath = dstBasePath
	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetSignedSrcToken(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	srcJwt, err := GetSignedToken(ctx, result.Event.SourceProjectNumber)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGetSignedToken, err)
	}
	result.SrcJwtToken = srcJwt
	return result, nil
}

func (a *VolumeReplicationCreateActivity) GetSignedDstToken(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	dstJwt, err := GetSignedToken(ctx, *result.DstProjectNumber)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGetSignedToken, err)
	}
	result.DstJwtToken = dstJwt
	return result, nil
}

func (a *VolumeReplicationCreateActivity) MountReplication(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("MountReplication")
	safeRecordHeartbeat(ctx, "MountReplication started")

	googleProxyClient := googleproxyclient.GetGProxyClient(*result.DstBasePath, *result.DstJwtToken, logger)

	createVolumeParams := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
		ProjectNumber:       *result.DstProjectNumber,
		LocationId:          result.Event.DestinationLocationID,
		VolumeReplicationId: result.DstReplication.VolumeReplicationUuid.Value,
		XCorrelationID:      googleproxyclient.NewOptString(*result.Event.XCorrelationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalMountVolumeReplication(ctx, *createVolumeParams)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, err)
	}
	switch r := res.(type) {
	case *googleproxyclient.InternalJobV1beta:
		result.JobId = &r.JobUuid.Value
		return result, nil
	case *googleproxyclient.V1betaInternalMountVolumeReplicationBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalMountVolumeReplicationUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalMountVolumeReplicationForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalMountVolumeReplicationNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalMountVolumeReplicationConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	case *googleproxyclient.V1betaInternalMountVolumeReplicationInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMountingVolumeReplication, errors.New("unexpected response type from Google Proxy")))
	}
}

// DescribeRemoteJob gives the status of a remote job
func (a *VolumeReplicationCreateActivity) DescribeRemoteJob(ctx context.Context, result *replication.CreateReplicationResult) error {
	safeRecordHeartbeat(ctx, "DescribeRemoteJob started")
	err := activities.DescribeJob(ctx, result.JobId, result.DstBasePath, result.DstJwtToken, result.DstProjectNumber, &result.Event.DestinationLocationID, result.Event.XCorrelationID)
	if err != nil {
		return err
	}
	return nil
}

func convertSourceVolumeToDestinationVolume(result *replication.CreateReplicationResult) googleproxyclient.VolumeV1beta {
	srcVol := result.Event.SourceVolume
	protocols := make([]googleproxyclient.ProtocolsV1beta, 0)
	var isBlockVolume bool

	for _, value := range srcVol.VolumeAttributes.Protocols {
		var protocolsV1beta googleproxyclient.ProtocolsV1beta
		_ = protocolsV1beta.UnmarshalText([]byte(value))

		if protocolsV1beta == googleproxyclient.ProtocolsV1betaISCSI {
			isBlockVolume = true
		}

		protocols = append(protocols, protocolsV1beta)
	}

	// Convert BlockDevices
	blockDevices := make([]googleproxyclient.BlockDeviceV1beta, 0)
	if srcVol.VolumeAttributes.BlockDevices != nil {
		for _, blockDevice := range *srcVol.VolumeAttributes.BlockDevices {
			blockDeviceV1beta := googleproxyclient.BlockDeviceV1beta{
				OsType: googleproxyclient.NewOptBlockDeviceV1betaOsType(convertBlockDeviceOsType(blockDevice.OSType)),
			}
			blockDevices = append(blockDevices, blockDeviceV1beta)
		}
	}

	var creationToken string
	if !isBlockVolume {
		if result.Event.CreateReplicationParams.DestinationVolumeParameters.ShareName != "" {
			creationToken = result.Event.CreateReplicationParams.DestinationVolumeParameters.ShareName
		} else {
			creationToken = result.Event.SourceVolume.VolumeAttributes.CreationToken
		}
	}

	var resourceId *string
	if resourceId = &result.Event.CreateReplicationParams.DestinationVolumeParameters.VolumeID; result.Event.CreateReplicationParams.DestinationVolumeParameters.VolumeID == "" {
		resourceId = &result.Event.SourceVolume.Name
	}
	destVolParams := result.Event.CreateReplicationParams.DestinationVolumeParameters
	var tieringPolicyV1beta googleproxyclient.OptTieringPolicyV1beta
	if destVolParams.TieringPolicy != nil {
		tieringPolicyV1beta = googleproxyclient.NewOptTieringPolicyV1beta(*destVolParams.TieringPolicy)
	}

	volume := googleproxyclient.VolumeV1beta{
		ResourceId:    *resourceId,
		CreationToken: googleproxyclient.NewOptString(creationToken),
		PoolId:        googleproxyclient.NewNilString(result.DstPool.PoolId.Value),
		QuotaInBytes:  googleproxyclient.NewOptFloat64(float64(srcVol.SizeInBytes)),
		Network:       googleproxyclient.NewOptString(result.DstPool.Network),
		Description:   googleproxyclient.NewOptNilString(nillable.GetString(result.Event.CreateReplicationParams.DestinationVolumeParameters.Description, "")),
		Protocols:     protocols,
		BlockDevices:  blockDevices,
		TieringPolicy: tieringPolicyV1beta,
	}
	if !isBlockVolume && srcVol.VolumeAttributes != nil && srcVol.VolumeAttributes.FileProperties != nil {
		fileProps := srcVol.VolumeAttributes.FileProperties
		if fileProps.UnixPermissions != "" {
			volume.UnixPermissions = googleproxyclient.NewOptNilString(fileProps.UnixPermissions)
		}
		effectiveStyle := utils.ResolveEffectiveUnixSecurityStyle(
			srcVol.VolumeAttributes.Protocols,
			fileProps.SecurityStyle,
			fileProps.SecurityStyle != "",
			fileProps.UnixPermissions,
		)
		switch strings.ToLower(effectiveStyle) {
		case utils.UnixSecurityStyle:
			volume.SecurityStyle = googleproxyclient.NewOptVolumeV1betaSecurityStyle(googleproxyclient.VolumeV1betaSecurityStyleUNIX)
		case "ntfs":
			volume.SecurityStyle = googleproxyclient.NewOptVolumeV1betaSecurityStyle(googleproxyclient.VolumeV1betaSecurityStyleNTFS)
		}
	}

	if srcVol.LargeVolumeAttributes != nil && srcVol.LargeVolumeAttributes.LargeCapacity && srcVol.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
		volume.LargeCapacity = googleproxyclient.NewOptNilBool(srcVol.LargeVolumeAttributes.LargeCapacity)
		volume.LargeVolumeConstituentCount = googleproxyclient.NewOptNilInt32(*srcVol.LargeVolumeAttributes.LargeVolumeConstituentCount)
	}
	if result.Event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy != nil {
		tieringPolicyParam := result.Event.CreateReplicationParams.DestinationVolumeParameters.TieringPolicy

		var tieringPolicy googleproxyclient.TieringPolicyV1beta
		if tieringPolicyParam.TierAction.IsSet() {
			tieringPolicy.SetTierAction(googleproxyclient.NewOptNilTieringPolicyV1betaTierAction(googleproxyclient.TieringPolicyV1betaTierAction(tieringPolicyParam.TierAction.Value)))
		}
		if tieringPolicyParam.CoolingThresholdDays.IsSet() {
			tieringPolicy.SetCoolingThresholdDays(googleproxyclient.NewOptNilInt32(tieringPolicyParam.CoolingThresholdDays.Value))
		}
		if tieringPolicyParam.HotTierBypassModeEnabled.IsSet() {
			tieringPolicy.SetHotTierBypassModeEnabled(googleproxyclient.NewOptNilBool(tieringPolicyParam.HotTierBypassModeEnabled.Value))
		}
		volume.TieringPolicy = googleproxyclient.NewOptTieringPolicyV1beta(tieringPolicy)
	}

	if destVolParams.ThroughputMibps != nil {
		volume.ThroughputMibps = googleproxyclient.NewOptNilFloat64(float64(*destVolParams.ThroughputMibps))
	}
	if destVolParams.Iops != nil {
		volume.Iops = googleproxyclient.NewOptNilInt64(*destVolParams.Iops)
	}
	if destVolParams.VolumePerformanceGroupId != nil {
		volume.VolumePerformanceGroupId = googleproxyclient.NewOptNilString(*destVolParams.VolumePerformanceGroupId)
	}

	return volume
}

// determineReplicationType determines the replication type based on source and destination locations
func determineReplicationType(sourceLocation, destinationLocation string) googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationType {
	if sourceLocation == destinationLocation {
		return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTRAZONEREPLICATION
	}

	sourceRegion, _, _ := utils.ParseRegionAndZone(sourceLocation)
	destRegion, _, _ := utils.ParseRegionAndZone(destinationLocation)

	if sourceRegion != destRegion {
		return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION
	}
	return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION
}

func _convertVolumeReplicationCreateParams(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
	// Determine replication type based on source and destination locations
	sourceLocation := result.Event.LocationID
	destinationLocation := result.Event.DestinationLocationID
	replicationType := determineReplicationType(sourceLocation, destinationLocation)

	createReplicationParams := googleproxyclient.VolumeReplicationCreateInternalV1beta{
		RemoteRegion:          result.Event.LocationID,
		EndpointType:          "dst",
		ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationCreateInternalV1betaReplicationSchedule(convertReplicationScheduleToInternalReplicationSchedule(*result.Event.CreateReplicationParams.ReplicationSchedule)),
		SourceVolumeUuid:      googleproxyclient.NewOptString(result.Event.SourceVolume.UUID),
		SourcePoolUuid:        googleproxyclient.NewOptString(result.Event.SourcePool.UUID),
		SourceHostName:        result.Event.SourcePool.ClusterDetails.ExternalName,
		SourceServerName:      *result.SrcSvm,
		SourceVolumeName:      result.Event.SourceVolume.Name,
		DestinationHostName:   result.DstPool.ClusterName.Value,
		DestinationServerName: *result.DstSvm,
		DestinationVolumeName: result.DstVolume.ResourceId,
		VolumeReplicationUuid: googleproxyclient.NewOptString(result.DbVolReplication.UUID),
		DestinationVolumeUuid: googleproxyclient.NewOptString(result.DstVolume.VolumeId.Value),
		DestinationPoolUuid:   googleproxyclient.NewOptString(result.DstPool.PoolId.Value),
		Name:                  googleproxyclient.NewOptString(*result.Event.CreateReplicationParams.ResourceID),
		Description:           googleproxyclient.NewOptString(nillable.GetString(result.Event.CreateReplicationParams.Description, "")),
		ReplicationPolicy:     googleproxyclient.NewOptVolumeReplicationCreateInternalV1betaReplicationPolicy(googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationPolicyMirrorAllSnapshots),
		ReplicationType:       googleproxyclient.NewOptVolumeReplicationCreateInternalV1betaReplicationType(replicationType),
		ReverseResume:         googleproxyclient.NewOptBool(false),
		CcfeURI:               googleproxyclient.NewOptString(result.DbVolReplication.Uri),
		CcfeRemoteURI:         googleproxyclient.NewOptString(result.DbVolReplication.RemoteUri),
		Labels:                googleproxyclient.NewOptVolumeReplicationCreateInternalV1betaLabels(result.Event.CreateReplicationParams.Labels),
	}

	return createReplicationParams
}

func _convertReplicationScheduleToInternalReplicationSchedule(in string) googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationSchedule {
	switch in {
	case "EVERY_10_MINUTES":
		return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationSchedule10minutely
	case "HOURLY":
		return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationScheduleHourly
	case "DAILY":
		return googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationScheduleDaily
	default:
		return ""
	}
}

func convertBlockDeviceOsType(in string) googleproxyclient.BlockDeviceV1betaOsType {
	switch in {
	case "LINUX":
		return googleproxyclient.BlockDeviceV1betaOsTypeLINUX
	case "WINDOWS":
		return googleproxyclient.BlockDeviceV1betaOsTypeWINDOWS
	case "ESXI":
		return googleproxyclient.BlockDeviceV1betaOsTypeESXI
	default:
		return googleproxyclient.BlockDeviceV1betaOsTypeOSTYPEUNSPECIFIED
	}
}

func (a *VolumeReplicationCreateActivity) CreateSnapmirrorFirewall(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	safeRecordHeartbeat(ctx, "CreateSnapmirrorFirewall started")

	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	snHostProject := result.Event.SourcePool.SnHostProject
	if snHostProject == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("SnHostProject", nil))
	}

	network := result.Event.SourcePool.ClusterDetails.Network
	if network == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("SnHostProjectNetwork", nil))
	}

	op, err := activities.InsertFirewall(cloudService, snHostProject, snapmirrorFirewallName, network, snapmirrorFirewallPriority, snapmirrorFirewallDirection, strings.Split(snapmirrorFirewallSourceRanges, ","), strings.Split(snapmirrorFirewallPortRules, ","))
	if err != nil {
		logger.Errorf("Failed to create firewall %s: %v", snapmirrorFirewallName, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// If operation name is empty, it means the firewall already exists or no operation was needed
	if op == "" {
		logger.Infof("Firewall %s already exists", snapmirrorFirewallName)
		operation := commonparams.Operations{
			OperationName:      "",
			OperationType:      "firewall",
			IsDone:             true,
			IsRegionalResource: false,
			Project:            snHostProject,
		}
		result.Operation = &operation
		return result, nil
	}

	operation := commonparams.Operations{
		OperationName:      op,
		OperationType:      "firewall",
		IsDone:             false,
		IsRegionalResource: false,
		Project:            snHostProject,
	}
	result.Operation = &operation
	return result, nil
}

func (a *VolumeReplicationCreateActivity) PollSnapmirrorFirewallOperation(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)

	if result.Operation == nil || result.Operation.IsDone == true {
		return result, nil
	}
	safeRecordHeartbeat(ctx, "PollSnapmirrorFirewallOperation polling")

	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	opStatus, err := activities.GetComputeOpStatus(cloudService, result.Event.SourcePool.SnHostProject, result.Operation.IsRegionalResource, result.Operation.OperationName)
	if err != nil {
		logger.Errorf("Failed to get operation status for %s: %v", result.Operation.OperationName, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if opStatus == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, vsaerrors.New("Failed to get operation status"))
	}

	if opStatus.Status == "DONE" {
		result.Operation.IsDone = true
		return result, nil
	}

	return nil, vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, vsaerrors.Newf("Firewall operation %s is not completed. Status: %s", result.Operation.OperationName, opStatus.Status))
}

func (a *VolumeReplicationCreateActivity) ListQuotaRulesLocal(ctx context.Context, result *replication.CreateReplicationResult) ([]*datamodel.QuotaRule, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Extract source volume ID from the result
	sourceVolumeID := result.Event.SourceVolume.ID

	// Fetch quota rules from database
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, sourceVolumeID)
	if err != nil {
		logger.Errorf("Failed to fetch quota rules for source volume: %d, error: %v", sourceVolumeID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully fetched %d quota rules for source volume: %d", len(quotaRules), sourceVolumeID)
	return quotaRules, nil
}

func (a *VolumeReplicationCreateActivity) CreateQuotaRulesOnDestination(ctx context.Context, result *replication.CreateReplicationResult) (*replication.CreateReplicationResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("CreateQuotaRulesOnDestination")
	safeRecordHeartbeat(ctx, "CreateQuotaRulesOnDestination started")

	// If source quota rules is empty, skip creating quota rules on destination
	if len(result.SourceQuotaRules) == 0 {
		logger.Info("No source quota rules found, skipping quota rule creation on destination")
		result.DestinationQuotaRules = nil
		return result, nil
	}

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Call the generic helper function and receive the quota rules returned from the API
	destinationQuotaRules, err := CreateQuotaRulesRemote(
		ctx,
		logger,
		*result.DstBasePath,
		*result.DstJwtToken,
		*result.DstProjectNumber,
		result.Event.DestinationLocationID,
		result.DstVolume.VolumeId.Value,
		correlationID,
		result.SourceQuotaRules,
		result.DestinationQuotaRules,
	)
	if err != nil {
		return nil, err
	}

	// Store the quota rules returned from the API in the result
	result.DestinationQuotaRules = destinationQuotaRules
	logger.Infof("Stored %d destination quota rules from CreateQuotaRulesRemote response", len(destinationQuotaRules))

	return result, nil
}

// HydrateQuotaRules hydrates quota rules on the destination volume by calling the callback API.
// This activity takes the destination quota rules (with their UUIDs) and hydrates them to CCFE.
func (a *VolumeReplicationCreateActivity) HydrateQuotaRules(ctx context.Context, quotaRules []*datamodel.QuotaRule, volumeResourceId string, location string, projectNumber string) error {
	if hydrationEnabled {
		logger := util.GetLogger(ctx)
		logger.Debugf("HydrateQuotaRules")

		// Call the common hydration function
		return HydrateQuotaRulesList(ctx, logger, quotaRules, volumeResourceId, location, projectNumber)
	}
	return nil
}

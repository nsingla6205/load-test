package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	jsonUnmarshal                         = json.Unmarshal
	convertLabelsMapToJSONB               = utils.ConvertLabelsMapToJSONB
	_convertToBackupVaultDataModel        = activities.ConvertToBackupVaultDataModel
	convertInternalBackupVaultToDataModel = _convertInternalBackupVaultToDataModel
)

func (h Handler) V1betaInternalDescribePool(ctx context.Context, params gcpgenserver.V1betaInternalDescribePoolParams) (gcpgenserver.V1betaInternalDescribePoolRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	queryDepth := 1
	pool, err := h.Orchestrator.GetPoolByName(ctx, params.PoolName, params.ProjectNumber, queryDepth)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("Pool not found", "name", params.PoolName)
			return &gcpgenserver.V1betaInternalDescribePoolNotFound{
				Code:    404,
				Message: "Pool not found",
			}, nil
		}
		logger.Error("Failed to describe pool", "error", err.Error())
		return &gcpgenserver.V1betaInternalDescribePoolInternalServerError{
			Code:    500,
			Message: "Internal server error while describing pool",
		}, err
	}

	resp := convertToPoolInternalV1Beta(pool)
	hasActive, err := h.Orchestrator.HasActiveClusterUpgrade(ctx, pool.UUID)
	if err != nil {
		logger.Warn("Failed to check cluster upgrade status for pool, assuming no active upgrade", "poolUUID", pool.UUID, "error", err)
		hasActive = false
	}
	resp.HasActiveClusterUpgrade = gcpgenserver.NewOptBool(hasActive)
	return resp, nil
}

func (h Handler) V1betaGetMultipleReplicationsInternal(ctx context.Context, req *gcpgenserver.ReplicationIDListV1beta, params gcpgenserver.V1betaGetMultipleReplicationsInternalParams) (gcpgenserver.V1betaGetMultipleReplicationsInternalRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	replications, err := h.Orchestrator.GetMultipleReplicationsInternal(ctx, params.ProjectNumber, req.GetReplicationUUIDs())
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("No replications found for the provided UUIDs", "replicationUUIDs", req.GetReplicationUUIDs())
			return &gcpgenserver.V1betaGetMultipleReplicationsInternalNotFound{
				Code:    404,
				Message: "No replications found for the provided UUIDs",
			}, nil
		}
		logger.Error("Failed to get multiple replications", "error", err.Error())
		return &gcpgenserver.V1betaGetMultipleReplicationsInternalInternalServerError{
			Code:    500,
			Message: "Internal server error while getting multiple replications",
		}, err
	}

	return &gcpgenserver.V1betaGetMultipleReplicationsInternalOK{
		Replications: convertToVolumeReplicationsInternalV1Beta(replications),
	}, nil
}

func (h Handler) V1betaInternalStopVolumeReplication(ctx context.Context, request *gcpgenserver.V1betaInternalStopVolumeReplicationReq, params gcpgenserver.V1betaInternalStopVolumeReplicationParams) (gcpgenserver.V1betaInternalStopVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	forceStop := false
	if request.GetForce().Value {
		forceStop = request.GetForce().Value
	}

	replication, job, err := h.Orchestrator.StopReplicationInternal(ctx, params.VolumeReplicationId, params.ProjectNumber, forceStop)
	if err != nil {
		logger.Error("Failed to stop replication", "error", err.Error())
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalStopVolumeReplicationNotFound{
				Code:    404,
				Message: "Volume replication not found",
			}, nil
		} else if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaInternalStopVolumeReplicationBadRequest{
				Code:    400,
				Message: "Invalid request parameters",
			}, nil
		}
		return &gcpgenserver.V1betaInternalStopVolumeReplicationInternalServerError{
			Code:    500,
			Message: "Internal server error while stopping replication",
		}, nil
	}

	return convertToInternalV1betaVolumeReplication(replication, job), nil
}

func (h Handler) V1betaInternalCreateVolumeReplication(ctx context.Context, req *gcpgenserver.VolumeReplicationCreateInternalV1beta, params gcpgenserver.V1betaInternalCreateVolumeReplicationParams) (gcpgenserver.V1betaInternalCreateVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	if req.EndpointType != "dst" {
		code := float64(400)
		msg := "Incorrect endpoint type"
		return &gcpgenserver.V1betaInternalCreateVolumeReplicationBadRequest{
			Code:    code,
			Message: msg,
		}, nil
	}

	param := prepareCreateVolumeReplicationInternalParams(req, params)
	volumeReplication, job, err := h.Orchestrator.CreateVolumeReplicationInternal(ctx, param)
	if err != nil {
		logger.Error("Failed to create volume replication", "error", err.Error())
		return nil, err
	}
	ans := convertToInternalV1betaVolumeReplication(volumeReplication, job)
	return ans, nil
}

func (h Handler) V1betaInternalReleaseVolumeReplication(ctx context.Context, params gcpgenserver.V1betaInternalReleaseVolumeReplicationParams) (gcpgenserver.V1betaInternalReleaseVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	_, job, err := h.Orchestrator.ReleaseVolumeReplication(ctx, params.VolumeReplicationId)
	if err != nil {
		logger.Error("Failed to release volume replication", "error", err.Error())
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalReleaseVolumeReplicationBadRequest{
				Code:    404,
				Message: "Volume replication not found",
			}, nil
		}
		return &gcpgenserver.V1betaInternalReleaseVolumeReplicationInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)),
		Response: nil,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func (h Handler) V1betaInternalDeleteVolumeReplication(ctx context.Context, params gcpgenserver.V1betaInternalDeleteVolumeReplicationParams) (gcpgenserver.V1betaInternalDeleteVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	var cleanupAfterReverse bool
	var isCleanup bool

	if params.CleanupAfterReverse.Set {
		cleanupAfterReverse = params.CleanupAfterReverse.Value
	} else {
		cleanupAfterReverse = false
	}

	if params.IsCleanup.Set {
		isCleanup = params.IsCleanup.Value
	} else {
		isCleanup = false
	}

	volumeReplication, job, err := h.Orchestrator.DeleteReplicationInternal(ctx, params.VolumeReplicationId, cleanupAfterReverse, isCleanup)
	if err != nil {
		logger.Error("Failed to delete replication", "error", err.Error())
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalDeleteVolumeReplicationBadRequest{
				Code:    404,
				Message: "Volume replication not found",
			}, nil
		}
		return &gcpgenserver.V1betaInternalDeleteVolumeReplicationInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	ans := convertToInternalV1betaVolumeReplication(volumeReplication, job)
	return ans, nil
}

// V1betaInternalDeleteVolumeSnapmirrorSnapshot handles the request to delete a ssnapshots with prefix snapmirror. from a spicific volume.
func (h Handler) V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx context.Context, params gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams) (gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	volumeId := params.VolumeId
	region, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	deleteSnapshotParams := &commonparams.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: commonparams.SnapshotBaseParams{
			VolumeID:    volumeId,
			AccountName: params.ProjectNumber,
		},
		Location: region,
	}

	// Delete the snapmirror snapshots
	operationID, err := h.Orchestrator.DeleteSnapmirrorSnapshots(ctx, deleteSnapshotParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest{
				Code:    404,
				Message: "Snapshot not found",
			}, nil
		} else if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to delete snapshot", "error", err.Error())
		return &gcpgenserver.V1betaInternalDeleteVolumeSnapmirrorSnapshotInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, err
	}

	logger.Infof("Snapshots deleted in progress for volume %s in region %s", volumeId, region)
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
		Response: nil,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func (h Handler) V1betaInternalUpdateVolumeReplication(ctx context.Context, req *gcpgenserver.VolumeReplicationUpdateInternalV1beta, params gcpgenserver.V1betaInternalUpdateVolumeReplicationParams) (gcpgenserver.V1betaInternalUpdateVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)

	param := prepareUpdateVolumeReplicationInternalParams(req, params)
	volumeReplication, job, err := h.Orchestrator.UpdateVolumeReplicationInternal(ctx, param)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("No replications found to update", "replicationUUID", params.VolumeReplicationId)
			return &gcpgenserver.V1betaInternalUpdateVolumeReplicationBadRequest{
				Code:    404,
				Message: "No replications found for the provided UUIDs",
			}, nil
		}
		logger.Error("Failed to update volume replication", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateVolumeReplicationInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	res := convertToInternalV1betaVolumeReplication(volumeReplication, job)
	return res, nil
}

func (h Handler) V1betaInternalUpdateVolumeReplicationAttributes(ctx context.Context, req *gcpgenserver.VolumeReplicationInternalV1beta, params gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesParams) (gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Convert the request to internal parameters for updating volume replication attributes
	updateParams := models.UpdateVolumeReplicationAttributesParams{
		ProjectNumber:             params.ProjectNumber,
		LocationId:                params.LocationId,
		VolumeReplicationId:       params.VolumeReplicationId,
		VolumeReplicationInternal: req,
	}

	// Call the orchestrator to update volume replication attributes
	job, err := h.Orchestrator.UpdateVolumeReplicationAttributes(ctx, updateParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update volume replication attributes", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateVolumeReplicationAttributesInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, err
	}

	// Return operation response instead of 204 No Content
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)),
		Response: nil,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func convertToInternalV1betaVolumeReplication(volumeReplication *models.VolumeReplication, job *datamodel.Job) *gcpgenserver.VolumeReplicationInternalV1beta {
	return &gcpgenserver.VolumeReplicationInternalV1beta{
		VolumeReplicationUuid: gcpgenserver.NewOptString(volumeReplication.UUID),
		EndpointType:          gcpgenserver.VolumeReplicationInternalV1betaEndpointType(volumeReplication.ReplicationAttributes.EndpointType),
		RemoteRegion:          volumeReplication.ReplicationAttributes.SourceRegion,
		SourceHostName:        volumeReplication.ReplicationAttributes.SourceHostName,
		SourceServerName:      volumeReplication.ReplicationAttributes.SourceSvmName,
		SourceVolumeName:      volumeReplication.ReplicationAttributes.SourceVolumeName,
		SourceVolumeUuid:      gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourceVolumeUUID),
		SourcePoolUuid:        gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.SourcePoolUUID),
		DestinationHostName:   volumeReplication.ReplicationAttributes.DestinationHostName,
		DestinationServerName: volumeReplication.ReplicationAttributes.DestinationSvmName,
		DestinationVolumeName: volumeReplication.ReplicationAttributes.DestinationVolumeName,
		DestinationVolumeUuid: gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationVolumeUUID),
		DestinationPoolUuid:   gcpgenserver.NewOptString(volumeReplication.ReplicationAttributes.DestinationPoolUUID),
		ReplicationType: gcpgenserver.OptVolumeReplicationInternalV1betaReplicationType{
			Value: gcpgenserver.VolumeReplicationInternalV1betaReplicationType(volumeReplication.ReplicationAttributes.ReplicationType),
			Set:   true,
		},
		Jobs: []gcpgenserver.JobV1beta{
			{
				JobId:    gcpgenserver.NewOptString(job.UUID),
				Created:  gcpgenserver.NewOptDateTime(job.CreatedAt),
				WorkerId: gcpgenserver.NewOptString(job.WorkflowID),
			},
		},
	}
}

func (h Handler) V1betaInternalUpdateState(ctx context.Context, req *gcpgenserver.VolumeReplicationUpdateStateInternalV1beta, params gcpgenserver.V1betaInternalUpdateStateParams) (gcpgenserver.V1betaInternalUpdateStateRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Convert the request to internal parameters for updating volume replication attributes
	updateParams := models.UpdateVolumeReplicationStateParams{
		ProjectNumber:       params.ProjectNumber,
		LocationId:          params.LocationId,
		VolumeReplicationId: params.VolumeReplicationId,
		State:               req.State.Value,
		StateDetails:        req.StateDetails.Value,
	}

	// Call the orchestrator to update volume replication attributes
	volumeReplication, err := h.Orchestrator.UpdateVolumeReplicationState(ctx, updateParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateStateBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update volume replication attributes", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateStateInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, err
	}

	res := &gcpgenserver.VolumeReplicationUpdateStateInternalV1beta{
		State:        gcpgenserver.NewOptString(volumeReplication.State),
		StateDetails: gcpgenserver.NewOptString(volumeReplication.StateDetails),
	}
	return res, nil
}

func prepareCreateVolumeReplicationInternalParams(req *gcpgenserver.VolumeReplicationCreateInternalV1beta, params gcpgenserver.V1betaInternalCreateVolumeReplicationParams) *commonparams.CreateVolumeReplicationInternalParams {
	volRepParams := &models.VolumeReplication{
		Name:        req.Name.Value,
		Description: req.Description.Value,
		Uri:         req.CcfeURI.Value,
		RemoteUri:   req.CcfeRemoteURI.Value,
		Account: &models.Account{
			Name: params.ProjectNumber,
		},
		ReplicationAttributes: &models.ReplicationDetails{
			EndpointType:          string(req.EndpointType),
			ReplicationSchedule:   string(req.ReplicationSchedule.Value),
			ReplicationType:       string(req.ReplicationType.Value),
			SourceRegion:          req.RemoteRegion,
			SourceHostName:        req.SourceHostName,
			SourceReplicationUUID: req.VolumeReplicationUuid.Value,
			SourceSvmName:         req.SourceServerName,
			SourceVolumeName:      req.SourceVolumeName,
			SourceVolumeUUID:      req.SourceVolumeUuid.Value,
			SourcePoolUUID:        req.SourcePoolUuid.Value,
			DestinationVolumeUUID: req.DestinationVolumeUuid.Value,
			DestinationRegion:     params.LocationId,
			DestinationHostName:   req.DestinationHostName,
			DestinationSvmName:    req.DestinationServerName,
			DestinationVolumeName: req.DestinationVolumeName,
			DestinationPoolUUID:   req.DestinationPoolUuid.Value,
			ReplicationPolicy:     string(req.ReplicationPolicy.Value),
			Labels:                map[string]string(req.Labels.Value),
		},
	}

	param := &commonparams.CreateVolumeReplicationInternalParams{
		ReverseResync:     req.ReverseResume.Value,
		VolumeReplication: volRepParams,
	}

	return param
}

func prepareUpdateVolumeReplicationInternalParams(req *gcpgenserver.VolumeReplicationUpdateInternalV1beta, params gcpgenserver.V1betaInternalUpdateVolumeReplicationParams) *commonparams.UpdateVolumeReplicationInternalParams {
	param := &commonparams.UpdateVolumeReplicationInternalParams{
		VolumeReplicationUuid: params.VolumeReplicationId,
		AccountName:           params.ProjectNumber,
		XCorrelationID:        params.XCorrelationID.Value,
		LocationId:            params.LocationId,
	}
	if req.Description.IsSet() {
		param.Description = &req.Description.Value
	}
	if req.ReplicationSchedule.IsSet() {
		schedule := string(req.ReplicationSchedule.Value)
		param.ReplicationSchedule = &schedule
	}
	if req.Labels.IsSet() {
		param.Labels = convertLabelsMapToJSONB(req.Labels.Value)
	}
	if req.ClusterLocation.IsSet() {
		param.ClusterLocation = &req.ClusterLocation.Value
	}
	return param
}

func (h Handler) V1betaInternalGetReplicationJobs(ctx context.Context, params gcpgenserver.V1betaInternalGetReplicationJobsParams) (gcpgenserver.V1betaInternalGetReplicationJobsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	poolUUID := ""
	if params.PoolUUID.Set {
		poolUUID = params.PoolUUID.Value
	}

	jobs, err := h.Orchestrator.GetReplicationJobs(ctx, params.ProjectNumber, poolUUID)
	if err != nil {
		logger.Error("Failed to get replication jobs", "error", err.Error())
		return &gcpgenserver.V1betaInternalGetReplicationJobsInternalServerError{
			Code:    500,
			Message: "Internal server error while getting replication jobs",
		}, err
	}
	jobsList := make([]gcpgenserver.InternalJobV1beta, 0, len(jobs))
	for _, job := range jobs {
		jobsList = append(jobsList, convertJobToInternalJobV1Beta(job))
	}
	return &gcpgenserver.V1betaInternalGetReplicationJobsOK{
		Jobs: jobsList,
	}, nil
}

func (h Handler) V1betaInternalMountVolumeReplication(ctx context.Context, params gcpgenserver.V1betaInternalMountVolumeReplicationParams) (gcpgenserver.V1betaInternalMountVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	job, err := h.Orchestrator.PerformMountCheck(ctx, params.VolumeReplicationId, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to PerformMountCheck", "error", err.Error())
		return &gcpgenserver.V1betaInternalMountVolumeReplicationInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	internalJob := convertJobToInternalJobV1Beta(job)
	return &internalJob, nil
}

func convertJobToInternalJobV1Beta(job *models.Job) gcpgenserver.InternalJobV1beta {
	return gcpgenserver.InternalJobV1beta{
		JobUuid:       gcpgenserver.NewOptString(job.UUID),
		CorrelationId: gcpgenserver.NewOptString(job.CorrelationID),
		State:         gcpgenserver.NewOptString(string(job.State)),
		StateDetails:  gcpgenserver.NewOptString(job.StateDetails),
		JobType:       gcpgenserver.NewOptString(string(job.Type)),
		CreatedAt:     gcpgenserver.NewOptDateTime(job.CreatedAt),
		UpdatedAt:     gcpgenserver.NewOptDateTime(job.UpdatedAt),
		ScheduledAt:   gcpgenserver.NewOptDateTime(job.ScheduledAt),
		ResourceName:  gcpgenserver.NewOptString(job.ResourceName),
	}
}

func (h Handler) V1betaInternalResumeVolumeReplication(ctx context.Context, params gcpgenserver.V1betaInternalResumeVolumeReplicationParams) (gcpgenserver.V1betaInternalResumeVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	forceResume := false
	if params.ForceResume.Set {
		forceResume = params.ForceResume.Value
	}
	volumeReplication, job, err := h.Orchestrator.ResumeReplicationInternal(ctx, params.VolumeReplicationId, params.ProjectNumber, forceResume)
	if err != nil {
		logger.Error("Failed to resume replication", "error", err.Error())
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalResumeVolumeReplicationNotFound{
				Code:    404,
				Message: "Volume replication not found",
			}, nil
		} else if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaInternalResumeVolumeReplicationBadRequest{
				Code:    400,
				Message: "Invalid request parameters",
			}, nil
		}
		return &gcpgenserver.V1betaInternalResumeVolumeReplicationInternalServerError{
			Code:    500,
			Message: "Internal server error while resuming replication",
		}, nil
	}

	return convertToInternalV1betaVolumeReplication(volumeReplication, job), nil
}

func (h Handler) V1betaInternalReverseVolumeReplication(ctx context.Context, params gcpgenserver.V1betaInternalReverseVolumeReplicationParams) (gcpgenserver.V1betaInternalReverseVolumeReplicationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	volumeReplication, job, err := h.Orchestrator.ReverseReplicationInternal(ctx, params.VolumeReplicationId, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to reverse replication", "error", err.Error())
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalReverseVolumeReplicationNotFound{
				Code:    404,
				Message: "Volume replication not found",
			}, nil
		} else if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaInternalReverseVolumeReplicationBadRequest{
				Code:    400,
				Message: "Invalid request parameters",
			}, nil
		}
		return &gcpgenserver.V1betaInternalReverseVolumeReplicationInternalServerError{
			Code:    500,
			Message: "Internal server error while reversing replication",
		}, nil
	}

	return convertToInternalV1betaVolumeReplication(volumeReplication, job), nil
}

func (h Handler) V1betaInternalDescribeVolume(ctx context.Context, params gcpgenserver.V1betaInternalDescribeVolumeParams) (gcpgenserver.V1betaInternalDescribeVolumeRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	volume, err := h.Orchestrator.GetVolume(ctx, params.VolumeId, true)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalDescribeVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}
		logger.Error("Failed to describe volume", "error", err.Error())
		return &gcpgenserver.V1betaInternalDescribeVolumeInternalServerError{Code: 500, Message: "Internal server error"}, err
	}

	volumeRes := convertModelToVCPVolume(volume)
	resp, err := jsonMarshal(volumeRes)
	if err != nil {
		return &gcpgenserver.V1betaInternalDescribeVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	res := gcpgenserver.InternalVolumeV1beta{}
	err = jsonUnmarshal(resp, &res)
	if err != nil {
		return &gcpgenserver.V1betaInternalDescribeVolumeInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	res.SvmName = gcpgenserver.NewOptNilString(volume.SvmName)
	return &res, nil
}

func (h Handler) V1betaInternalUpdateVolume(ctx context.Context, req *gcpgenserver.VolumeUpdateV1beta, params gcpgenserver.V1betaInternalUpdateVolumeParams) (gcpgenserver.V1betaInternalUpdateVolumeRes, error) {
	logger := util.GetLogger(ctx)
	region, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	volumeUpdateParams := gcpgenserver.V1betaUpdateVolumeParams{
		ProjectNumber:  params.ProjectNumber,
		LocationId:     params.LocationId,
		VolumeId:       params.VolumeId,
		XCorrelationID: params.XCorrelationID,
	}

	volume, err := h.Orchestrator.GetVolume(ctx, params.VolumeId, false)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateVolumeNotFound{
				Code:    404,
				Message: "Volume not found",
			}, nil
		}
		logger.Error("Failed to get volume before update", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, nil
	}

	param, err := prepareUpdateVolumeParams(req, volumeUpdateParams, region, volume)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to update volume", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{Code: 500, Message: err.Error()}, err
	}

	volume, jobUUID, err := h.Orchestrator.UpdateVolume(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateVolumeBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to update volume", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateVolumeInternalServerError{Code: 500, Message: err.Error()}, err
	}

	resp, err := encodeVolumeV1(convertModelToVCPVolume(volume))
	if err != nil {
		return nil, err
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + jobUUID
	if volume.LifeCycleState == datamodel.LifeCycleStateUpdating {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

// V1betaInternalCreateBackupVault implements the internal endpoint for creating a BackupVault entry in the VCP database
// This is used for cross-region operations where the BackupVault needs to be created in a remote region's database
// It fetches the BackupVault from the local CVP using the provided backupVaultId (ExternalUUID) and creates an entry in VCP
func (h Handler) V1betaInternalCreateBackupVault(ctx context.Context, req *gcpgenserver.BackupVaultInternalV1beta, params gcpgenserver.V1betaInternalCreateBackupVaultParams) (gcpgenserver.V1betaInternalCreateBackupVaultRes, error) {
	logger := util.GetLogger(ctx)

	if req == nil || req.BackupVaultId == "" {
		return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
			Code: 400,
		}, errors.New("backupVaultId is required")
	}

	if params.ProjectNumber == "" {
		return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
			Code: 400,
		}, errors.New("projectNumber is required")
	}

	if params.LocationId == "" {
		return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
			Code: 400,
		}, errors.New("locationId is required")
	}

	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	isCrossProjectVault := len(req.BucketDetails) > 0 && req.BucketDetails[0].TenantProjectNumber.Set

	backupVault := convertInternalBackupVaultToDataModel(req)
	if !utils.IsCVPHostConfigured() || isCrossProjectVault {
		backupVault.UUID = utils.RandomUUID()
		backupVault.Name = utils.ConvertSourceBackupVaultNameToRemoteBackupVaultName(req.ResourceId, req.BackupVaultId)
		backupVault.CrossRegionBackupVaultName = nillable.ToPointer(
			fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", params.ProjectNumber, req.SourceRegion.Value, req.ResourceId))
		if isCrossProjectVault {
			backupVault.ServiceType = datamodel.ServiceTypeCrossProject
		}
	} else if env.EnableBackupVaultVCPOrchestration {
		// VCP orchestration: generate a destination UUID and create the cross-region backup vault in
		// the destination region's SDE with that UUID, then persist it in VCP and hydrate to CCFE below.
		destinationUUID := utils.RandomUUID()
		remoteName := utils.ConvertSourceBackupVaultNameToRemoteBackupVaultName(req.ResourceId, req.BackupVaultId)

		sdeBody := &cvpmodels.BackupVaultCreateV1beta{
			ResourceID:    remoteName,
			BackupVaultID: &destinationUUID,
			ExternalUUID:  &req.BackupVaultId,
		}
		if req.Description.IsSet() {
			sdeBody.Description = nillable.ToPointer(req.Description.Value)
		}
		if req.BackupRegion.IsSet() {
			sdeBody.BackupRegion = nillable.ToPointer(req.BackupRegion.Value)
		}
		if req.SourceRegion.IsSet() {
			sdeBody.SourceRegion = nillable.ToPointer(req.SourceRegion.Value)
		}
		if req.KmsConfigResourcePath.IsSet() {
			sdeBody.KmsConfigResourcePath = nillable.ToPointer(req.KmsConfigResourcePath.Value)
		}
		if req.BackupsPrimaryKeyVersion.IsSet() {
			sdeBody.BackupsPrimaryKeyVersion = nillable.ToPointer(req.BackupsPrimaryKeyVersion.Value)
		}
		if req.ImmutableAttributes.IsSet() {
			immutable := req.ImmutableAttributes.Value
			retentionPolicy := &cvpmodels.BackupRetentionPolicyV1beta{
				DailyBackupImmutable:   immutable.IsDailyBackupImmutable.Value,
				WeeklyBackupImmutable:  immutable.IsWeeklyBackupImmutable.Value,
				MonthlyBackupImmutable: immutable.IsMonthlyBackupImmutable.Value,
				ManualBackupImmutable:  immutable.IsAdhocBackupImmutable.Value,
			}
			if immutable.BackupMinimumEnforcedRetentionDuration.IsSet() {
				retentionDays := int64(immutable.BackupMinimumEnforcedRetentionDuration.Value)
				retentionPolicy.BackupMinimumEnforcedRetentionDays = &retentionDays
			}
			sdeBody.BackupRetentionPolicy = retentionPolicy
		}
		_, sdeErrRes, sdeErr := createBackupVaultInSDE(ctx, sdeBody, params.LocationId, params.ProjectNumber, logger)
		if sdeErr != nil {
			logger.Error("Failed to create destination backup vault in SDE", "error", sdeErr)
			return &gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError{
				Code:    500,
				Message: fmt.Sprintf("Failed to create destination backup vault in SDE: %v", sdeErr),
			}, sdeErr
		}
		if sdeErrRes != nil {
			logger.Error("Failed to create destination backup vault in SDE", "response", sdeErrRes)
			switch e := sdeErrRes.(type) {
			case *gcpgenserver.V1betaCreateBackupVaultBadRequest:
				return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{Code: e.Code, Message: e.Message},
					fmt.Errorf("SDE create backup vault failed: %v", e.Message)
			case *gcpgenserver.V1betaCreateBackupVaultConflict:
				return &gcpgenserver.V1betaInternalCreateBackupVaultConflict{Code: e.Code, Message: e.Message},
					fmt.Errorf("SDE create backup vault failed: %v", e.Message)
			case *gcpgenserver.V1betaCreateBackupVaultForbidden:
				return &gcpgenserver.V1betaInternalCreateBackupVaultForbidden{Code: e.Code, Message: e.Message},
					fmt.Errorf("SDE create backup vault failed: %v", e.Message)
			case *gcpgenserver.V1betaCreateBackupVaultUnauthorized:
				return &gcpgenserver.V1betaInternalCreateBackupVaultUnauthorized{Code: e.Code, Message: e.Message},
					fmt.Errorf("SDE create backup vault failed: %v", e.Message)
			case *gcpgenserver.V1betaCreateBackupVaultUnprocessableEntity:
				return &gcpgenserver.V1betaInternalCreateBackupVaultUnprocessableEntity{Code: e.Code, Message: e.Message},
					fmt.Errorf("SDE create backup vault failed: %v", e.Message)
			default:
				return &gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError{
					Code:    500,
					Message: fmt.Sprintf("Failed to create destination backup vault in SDE: %v", sdeErrRes),
				}, fmt.Errorf("SDE create backup vault failed: %v", sdeErrRes)
			}
		}

		backupVault.UUID = destinationUUID
		backupVault.Name = remoteName
		backupVault.CrossRegionBackupVaultName = nillable.ToPointer(
			fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", params.ProjectNumber, req.SourceRegion.Value, req.ResourceId))
	} else {
		jwtToken := utils.GetJWTTokenFromContext(ctx)
		cvpClient := cvpCreateClient(logger, jwtToken)
		correlationID := utils.GetCoRelationIDFromContext(ctx)

		listParams := &backup_vault.V1betaListBackupVaultsParams{
			LocationID:     params.LocationId,
			ProjectNumber:  params.ProjectNumber,
			XCorrelationID: &correlationID,
		}

		vaults, err := cvpClient.BackupVault.V1betaListBackupVaults(listParams)
		if err != nil {
			logger.Error("Failed to list backup vaults from CVP", "error", err.Error())
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
					Code:    404,
					Message: fmt.Sprintf("No backup vaults found in CVP for project %s in region %s", params.ProjectNumber, params.LocationId),
				}, nil
			}
			return &gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError{
				Code:    500,
				Message: fmt.Sprintf("Failed to list backup vaults from CVP: %v", err),
			}, err
		}

		if vaults == nil || vaults.Payload == nil || vaults.Payload.BackupVaults == nil {
			return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
				Code:    404,
				Message: "No backup vaults found in CVP",
			}, nil
		}

		var cvpBackupVault *cvpmodels.BackupVaultV1beta
		for _, bv := range vaults.Payload.BackupVaults {
			if bv.BackupVaultType != nil && *bv.BackupVaultType != activities.CrossRegionBackupType {
				continue
			}
			if bv.SourceBackupVault != nil && strings.HasSuffix(*bv.SourceBackupVault, req.ResourceId) {
				cvpBackupVault = bv
				logger.Info("Found Remote Backup Vault with matching resource IDs", "cvpResourceID", *bv.ResourceID, "reqResourceID", req.ResourceId)
				break
			}
		}

		if cvpBackupVault == nil {
			logger.Error("BackupVault not found in CVP", "backupVaultId", req.BackupVaultId)
			return &gcpgenserver.V1betaInternalCreateBackupVaultBadRequest{
				Code:    404,
				Message: fmt.Sprintf("BackupVault %s not found in CVP", req.BackupVaultId),
			}, nil
		}

		backupVault.Name = *cvpBackupVault.ResourceID
		backupVault.UUID = cvpBackupVault.BackupVaultID
		backupVault.CrossRegionBackupVaultName = cvpBackupVault.SourceBackupVault // overriding for CRB destination case
	}

	backupVault.ExternalUUID = &req.BackupVaultId // Setting External UUID CRB destination case

	backupVaultParams := &commonparams.BackupVaultParams{
		OwnerID:     params.ProjectNumber,
		Region:      params.LocationId,
		AccountName: params.ProjectNumber,
	}
	createdBackupVault, err := h.Orchestrator.CreateBackupVaultEntryInVCP(ctx, backupVault, backupVaultParams)
	if err != nil {
		if errors.IsConflictErr(err) {
			logger.Info("BackupVault already exists in VCP", "uuid", backupVault.UUID)
			return &gcpgenserver.V1betaInternalCreateBackupVaultConflict{
				Code:    409,
				Message: "BackupVault already exists in VCP",
			}, nil
		}
		logger.Error("Failed to create BackupVault entry in VCP", "error", err.Error())
		return &gcpgenserver.V1betaInternalCreateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to create BackupVault entry in VCP database",
		}, err
	}

	result := convertDataModelToBackupVaultInternal(createdBackupVault)
	logger.Info("Successfully created BackupVault in VCP",
		"backupVaultId", createdBackupVault.UUID,
		"backupVaultName", createdBackupVault.Name,
		"backupRegion", params.LocationId)
	return &result, nil
}

// V1betaInternalDescribeBackupVault implements the internal endpoint for fetching a BackupVault from the VCP database
// This is used for cross-region operations to retrieve BackupVault details from a remote region's database
func (h Handler) V1betaInternalDescribeBackupVault(ctx context.Context, params gcpgenserver.V1betaInternalDescribeBackupVaultParams) (gcpgenserver.V1betaInternalDescribeBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	backupVault, err := h.Orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, params.BackupVaultId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Info("BackupVault not found", "uuid", params.BackupVaultId)
			return &gcpgenserver.V1betaInternalDescribeBackupVaultNotFound{
				Code:    404,
				Message: "BackupVault not found",
			}, nil
		}
		logger.Error("Failed to get BackupVault from VCP database", "error", err.Error())
		return &gcpgenserver.V1betaInternalDescribeBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to get BackupVault from VCP database",
		}, err
	}
	result := convertDataModelToBackupVaultInternal(backupVault)
	return &result, nil
}

// convertDataModelToBackupVaultInternal converts datamodel.BackupVault to API BackupVaultInternal model
func convertDataModelToBackupVaultInternal(bv *datamodel.BackupVault) gcpgenserver.BackupVaultInternalV1beta {
	result := gcpgenserver.BackupVaultInternalV1beta{
		BackupVaultId:   bv.UUID,
		ResourceId:      bv.Name,
		AccountVendorId: bv.AccountVendorID,
		LifeCycleState:  gcpgenserver.BackupVaultInternalV1betaLifeCycleState(bv.LifeCycleState),
		BackupVaultType: gcpgenserver.BackupVaultInternalV1betaBackupVaultType(bv.BackupVaultType),
		CreatedAt:       gcpgenserver.NewOptDateTime(bv.CreatedAt),
		UpdatedAt:       gcpgenserver.NewOptDateTime(bv.UpdatedAt),
	}

	if bv.ServiceType != "" {
		result.ServiceType = gcpgenserver.NewOptString(bv.ServiceType)
	}
	if bv.Description != nil {
		result.Description = gcpgenserver.NewOptString(*bv.Description)
	}
	if bv.BackupRegionName != nil {
		result.BackupRegion = gcpgenserver.NewOptString(*bv.BackupRegionName)
	}
	if bv.SourceRegionName != nil {
		result.SourceRegion = gcpgenserver.NewOptString(*bv.SourceRegionName)
	}
	if bv.CrossRegionBackupVaultName != nil {
		result.CrossRegionBackupVaultName = gcpgenserver.NewOptString(*bv.CrossRegionBackupVaultName)
	}
	if bv.ExternalUUID != nil {
		result.ExternalUuid = gcpgenserver.NewOptString(*bv.ExternalUUID)
	}
	if bv.LifeCycleStateDetails != "" {
		result.LifeCycleStateDetails = gcpgenserver.NewOptString(bv.LifeCycleStateDetails)
	}
	if bv.DeletedAt != nil {
		result.DeletedAt = gcpgenserver.NewOptDateTime(bv.DeletedAt.Time)
	}

	// Convert immutable attributes
	if bv.ImmutableAttributes != nil {
		immutableAttrs := gcpgenserver.BackupVaultInternalV1betaImmutableAttributes{}

		if bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil {
			duration := int(*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration)
			immutableAttrs.BackupMinimumEnforcedRetentionDuration = gcpgenserver.NewOptInt(duration)
		}
		immutableAttrs.IsDailyBackupImmutable = gcpgenserver.NewOptBool(bv.ImmutableAttributes.IsDailyBackupImmutable)
		immutableAttrs.IsWeeklyBackupImmutable = gcpgenserver.NewOptBool(bv.ImmutableAttributes.IsWeeklyBackupImmutable)
		immutableAttrs.IsMonthlyBackupImmutable = gcpgenserver.NewOptBool(bv.ImmutableAttributes.IsMonthlyBackupImmutable)
		immutableAttrs.IsAdhocBackupImmutable = gcpgenserver.NewOptBool(bv.ImmutableAttributes.IsAdhocBackupImmutable)

		result.ImmutableAttributes = gcpgenserver.NewOptBackupVaultInternalV1betaImmutableAttributes(immutableAttrs)
	}

	// Convert bucket details
	if len(bv.BucketDetails) > 0 {
		var bucketDetails []gcpgenserver.BackupVaultInternalV1betaBucketDetailsItem
		for _, bucket := range bv.BucketDetails {
			bucketItem := gcpgenserver.BackupVaultInternalV1betaBucketDetailsItem{
				BucketName:          gcpgenserver.NewOptString(bucket.BucketName),
				ServiceAccountName:  gcpgenserver.NewOptString(bucket.ServiceAccountName),
				VendorSubnetId:      gcpgenserver.NewOptString(bucket.VendorSubnetID),
				TenantProjectNumber: gcpgenserver.NewOptString(bucket.TenantProjectNumber),
			}
			bucketDetails = append(bucketDetails, bucketItem)
		}
		result.BucketDetails = bucketDetails
	}

	// Convert CMEK attributes
	if bv.CmekAttributes != nil {
		if bv.CmekAttributes.KmsConfigResourcePath != nil {
			result.KmsConfigResourcePath = gcpgenserver.NewOptString(*bv.CmekAttributes.KmsConfigResourcePath)
		}
		if bv.CmekAttributes.EncryptionState != nil {
			result.EncryptionState = gcpgenserver.NewOptBackupVaultInternalV1betaEncryptionState(gcpgenserver.BackupVaultInternalV1betaEncryptionState(*bv.CmekAttributes.EncryptionState))
		}
		if bv.CmekAttributes.BackupsPrimaryKeyVersion != nil {
			result.BackupsPrimaryKeyVersion = gcpgenserver.NewOptString(*bv.CmekAttributes.BackupsPrimaryKeyVersion)
		}
	}

	return result
}

// _convertInternalBackupVaultToDataModel converts API BackupVaultInternal model to datamodel.BackupVault.
func _convertInternalBackupVaultToDataModel(req *gcpgenserver.BackupVaultInternalV1beta) *datamodel.BackupVault {
	result := &datamodel.BackupVault{
		Name:            req.ResourceId,
		AccountVendorID: req.AccountVendorId,
		LifeCycleState:  string(req.LifeCycleState),
		BackupVaultType: string(req.BackupVaultType),
	}

	if req.Description.IsSet() {
		description := req.Description.Value
		result.Description = &description
	}
	if req.BackupRegion.IsSet() {
		backupRegion := req.BackupRegion.Value
		result.BackupRegionName = &backupRegion
	}
	if req.SourceRegion.IsSet() {
		sourceRegion := req.SourceRegion.Value
		result.SourceRegionName = &sourceRegion
	}
	if req.LifeCycleStateDetails.IsSet() {
		result.LifeCycleStateDetails = req.LifeCycleStateDetails.Value
	}

	if req.ImmutableAttributes.IsSet() {
		immutableAttrs := req.ImmutableAttributes.Value
		result.ImmutableAttributes = &datamodel.ImmutableAttributes{}
		if immutableAttrs.IsDailyBackupImmutable.IsSet() {
			result.ImmutableAttributes.IsDailyBackupImmutable = immutableAttrs.IsDailyBackupImmutable.Value
		}
		if immutableAttrs.IsWeeklyBackupImmutable.IsSet() {
			result.ImmutableAttributes.IsWeeklyBackupImmutable = immutableAttrs.IsWeeklyBackupImmutable.Value
		}
		if immutableAttrs.IsMonthlyBackupImmutable.IsSet() {
			result.ImmutableAttributes.IsMonthlyBackupImmutable = immutableAttrs.IsMonthlyBackupImmutable.Value
		}
		if immutableAttrs.IsAdhocBackupImmutable.IsSet() {
			result.ImmutableAttributes.IsAdhocBackupImmutable = immutableAttrs.IsAdhocBackupImmutable.Value
		}
		if immutableAttrs.BackupMinimumEnforcedRetentionDuration.IsSet() {
			duration := int64(immutableAttrs.BackupMinimumEnforcedRetentionDuration.Value)
			result.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration = &duration
		}
	}

	if len(req.BucketDetails) > 0 {
		bucketDetails := make(datamodel.BucketDetailsArray, 0, len(req.BucketDetails))
		for _, bucket := range req.BucketDetails {
			bucketDetail := &datamodel.BucketDetails{}
			if bucket.BucketName.IsSet() {
				bucketDetail.BucketName = bucket.BucketName.Value
			}
			if bucket.ServiceAccountName.IsSet() {
				bucketDetail.ServiceAccountName = bucket.ServiceAccountName.Value
			}
			if bucket.VendorSubnetId.IsSet() {
				bucketDetail.VendorSubnetID = bucket.VendorSubnetId.Value
			}
			if bucket.TenantProjectNumber.IsSet() {
				bucketDetail.TenantProjectNumber = bucket.TenantProjectNumber.Value
			}
			if bucket.SatisfiesPzs.IsSet() {
				bucketDetail.SatisfiesPzs = bucket.SatisfiesPzs.Value
			}
			if bucket.SatisfiesPzi.IsSet() {
				bucketDetail.SatisfiesPzi = bucket.SatisfiesPzi.Value
			}
			bucketDetails = append(bucketDetails, bucketDetail)
		}
		result.BucketDetails = bucketDetails
	}

	if req.KmsConfigResourcePath.IsSet() || req.EncryptionState.IsSet() || req.BackupsPrimaryKeyVersion.IsSet() {
		cmekAttrs := &datamodel.CmekAttributes{}
		if req.KmsConfigResourcePath.IsSet() {
			kmsConfigResourcePath := req.KmsConfigResourcePath.Value
			cmekAttrs.KmsConfigResourcePath = &kmsConfigResourcePath
		}
		if req.EncryptionState.IsSet() {
			encryptionState := string(req.EncryptionState.Value)
			cmekAttrs.EncryptionState = &encryptionState
		}
		if req.BackupsPrimaryKeyVersion.IsSet() {
			backupsPrimaryKeyVersion := req.BackupsPrimaryKeyVersion.Value
			cmekAttrs.BackupsPrimaryKeyVersion = &backupsPrimaryKeyVersion
		}
		result.CmekAttributes = cmekAttrs
	}
	result.ServiceType = datamodel.ServiceTypeGCNV
	if req.ServiceType.IsSet() && req.ServiceType.Value != "" {
		result.ServiceType = req.ServiceType.Value
	}

	return result
}

// V1betaInternalCreateBackup implements the internal endpoint for creating a backup
// This is used for cross-region operations where the backup needs to be created in a remote region's database
func (h Handler) V1betaInternalCreateBackup(ctx context.Context, req *gcpgenserver.InternalBackupCreateV1beta, params gcpgenserver.V1betaInternalCreateBackupParams) (gcpgenserver.V1betaInternalCreateBackupRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalCreateBackupBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	// If the request belongs to VSA, we will create the backup using the orchestrator
	vsaParams := createInternalBackupParams(req, params)
	_, jobId, err := h.Orchestrator.CreateBackupInternal(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalCreateBackupBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to create backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	// Get the backup from database using ExternalUUID (backupUUID from request becomes ExternalUUID for cross-region backups)
	dbBackup, err := h.Orchestrator.GetBackupByExternalUUID(ctx, params.BackupVaultId, req.BackupUUID, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to get backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}

	// Set LatestLogicalBackupSize to 0 for all previous backups of the same volume in a single query
	// This ensures that only the latest backup has the correct size
	// Update only if the latest logical backup size is not zero for the current backup
	if dbBackup.LatestLogicalBackupSize != 0 {
		err = h.Orchestrator.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, dbBackup.VolumeUUID, dbBackup.UUID)
		if err != nil {
			logger.Errorf("Failed to reset LatestLogicalBackupSize for previous backups of volume %s: %v", dbBackup.VolumeUUID, err)
			return &gcpgenserver.V1betaInternalCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
		}
	}

	resp := convertBackupDataModelToInternalBackupsV1beta(dbBackup, false) // isRestoring not needed for create

	if jobId == "" {
		// Return the backup directly for synchronous operations
		return &resp, nil
	}
	// Return operation for asynchronous operations
	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

// V1betaInternalUpdateBackup implements the internal endpoint for updating a backup
// This is used for cross-region operations where the backup needs to be updated in a remote region's database
func (h Handler) V1betaInternalUpdateBackup(ctx context.Context, req *gcpgenserver.BackupUpdateV1beta, params gcpgenserver.V1betaInternalUpdateBackupParams) (gcpgenserver.V1betaInternalUpdateBackupRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalUpdateBackupBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	// If the request belongs to VSA, we will update the backup using the orchestrator
	vsaParams := &commonparams.UpdateBackupParams{
		AccountName:     params.ProjectNumber,
		BackupVaultUUID: params.BackupVaultId,
		BackupUUID:      params.BackupId,
		Description:     req.Description,
	}
	_, jobId, err := h.Orchestrator.UpdateBackupInternal(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalUpdateBackupBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	// Get the backup from database using ExternalUUID (backupId in params is ExternalUUID for cross-region backups)
	dbBackup, err := h.Orchestrator.GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to get backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	resp := convertBackupDataModelToInternalBackupsV1beta(dbBackup, false) // isRestoring not needed for update

	if jobId == "" {
		// Return the backup directly for synchronous operations
		return &resp, nil
	}
	// Return operation for asynchronous operations
	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

// createInternalBackupParams converts InternalBackupCreateV1beta to CreateBackupParams for internal cross-region operations
func createInternalBackupParams(req *gcpgenserver.InternalBackupCreateV1beta, params gcpgenserver.V1betaInternalCreateBackupParams) *commonparams.CreateBackupParams {
	// Convert protocols
	protocols := make([]string, len(req.Protocols))
	for i, p := range req.Protocols {
		protocols[i] = string(p)
	}

	// Initialize backupParams with all fields, using correct values from request
	backupParams := commonparams.CreateBackupParams{
		AccountName:   params.ProjectNumber,
		Region:        params.LocationId,
		BackupVaultID: params.BackupVaultId,
		VolumeUUID:    req.VolumeId,
		BackupName:    req.ResourceId,
		BackupUUID:    req.BackupUUID, // ExternalUUID for cross-region backups
		LocationID:    params.LocationId,
		// Volume information (required for cross-region)
		VolumeName: req.VolumeName,
		Protocols:  protocols,
		// Optional fields - set from request if available
		Description: func() string {
			if req.Description.IsSet() {
				return req.Description.Value
			}
			return ""
		}(),
		SnapshotID: func() string {
			if req.SnapshotId.IsSet() {
				return req.SnapshotId.Value
			}
			return ""
		}(),
		SnapshotName: func() string {
			if req.SnapshotName.IsSet() {
				return req.SnapshotName.Value
			}
			return ""
		}(),
		UseExistingSnapshot: func() bool {
			if req.UseExistingSnapshot.IsSet() {
				return req.UseExistingSnapshot.Value
			}
			return false
		}(),
		XCorrelationID: func() string {
			if params.XCorrelationID.IsSet() {
				return params.XCorrelationID.Value
			}
			return ""
		}(),
		// Backup attributes for cross-region operations
		BucketName: func() string {
			if req.BucketName.IsSet() {
				return req.BucketName.Value
			}
			return ""
		}(),
		EndpointUUID: func() string {
			if req.EndpointUuid.IsSet() {
				return req.EndpointUuid.Value
			}
			return ""
		}(),
		IsRegionalHA: func() bool {
			if req.IsRegionalHa.IsSet() {
				return req.IsRegionalHa.Value
			}
			return false
		}(),
		CompletionTime: func() string {
			if req.CompletionTime.IsSet() {
				return req.CompletionTime.Value.Format(time.RFC3339)
			}
			return ""
		}(),
		BackupPolicyName: func() string {
			if req.BackupPolicyName.IsSet() {
				return req.BackupPolicyName.Value
			}
			return ""
		}(),
		OntapVolumeStyle: func() string {
			if req.OntapVolumeStyle.IsSet() {
				return req.OntapVolumeStyle.Value
			}
			return ""
		}(),
		IsExpertModeVolume: req.IsOntapBackup.IsSet() && req.IsOntapBackup.Value,
		SourceVolumeZone: func() string {
			if req.SourceVolumeZone.IsSet() {
				return req.SourceVolumeZone.Value
			}
			return ""
		}(),
		ServiceAccountName: func() string {
			if req.ServiceAccountName.IsSet() {
				return req.ServiceAccountName.Value
			}
			return ""
		}(),
		SourceStoragePool: func() string {
			if req.SourceStoragePool.IsSet() {
				return req.SourceStoragePool.Value
			}
			return ""
		}(),
		SnapshotCreationTime: func() string {
			if req.SnapshotCreationTime.IsSet() {
				return req.SnapshotCreationTime.Value.Format(time.RFC3339)
			}
			return ""
		}(),
		ConstituentCountOfBackup: func() int32 {
			if req.ConstituentCountOfBackup.IsSet() {
				return req.ConstituentCountOfBackup.Value
			}
			return 0
		}(),
		VolumeUsageBytes: func() int64 {
			if req.VolumeUsageBytes.IsSet() {
				return req.VolumeUsageBytes.Value
			}
			return 0
		}(),
		BackupType: func() string {
			if req.BackupType.IsSet() {
				return string(req.BackupType.Value)
			}
			return ""
		}(),
		BackupChainBytes: func() int64 {
			if req.BackupChainBytes.IsSet() {
				return req.BackupChainBytes.Value
			}
			return 0
		}(),
	}

	return &backupParams
}

func (h Handler) V1betaInternalDeleteBackupVault(ctx context.Context, params gcpgenserver.V1betaInternalDeleteBackupVaultParams) (gcpgenserver.V1betaInternalDeleteBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalDeleteBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	param := &commonparams.BackupVaultParams{
		BackupVaultID: params.BackupVaultId,
		AccountName:   params.ProjectNumber,
		OwnerID:       params.ProjectNumber,
		Region:        params.LocationId,
	}

	operationID, err := h.Orchestrator.DeleteBackupVaultInternal(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			logger.Error("Failed to delete backup vault - validation error", "error", err.Error())
			return &gcpgenserver.V1betaInternalDeleteBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to delete backup vault", "error", err.Error())
		return &gcpgenserver.V1betaInternalDeleteBackupVaultInternalServerError{
			Code:    500,
			Message: "Internal server error while deleting backup vault",
		}, nil
	}

	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Done: gcpgenserver.NewOptBool(false),
		}, nil
	}

	return &gcpgenserver.OperationV1beta{}, nil
}

func (h Handler) V1betaInternalUpdateBackupVault(ctx context.Context, req *gcpgenserver.BackupVaultInternalUpdateV1beta, params gcpgenserver.V1betaInternalUpdateBackupVaultParams) (gcpgenserver.V1betaInternalUpdateBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	if req == nil {
		return &gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest{
			Code:    400,
			Message: "Request body is required",
		}, errors.New("request body is required")
	}

	if params.BackupVaultId == "" {
		return &gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest{
			Code:    400,
			Message: "BackupVaultId is required",
		}, errors.New("backupVaultId is required")
	}

	if params.ProjectNumber == "" {
		return &gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest{
			Code:    400,
			Message: "ProjectNumber is required",
		}, errors.New("projectNumber is required")
	}

	var description string
	if req.Description.IsSet() {
		description = req.Description.Value
	}

	var backupMinimumEnforcedRetentionDuration *int64
	var dailyBackupImmutable, weeklyBackupImmutable, monthlyBackupImmutable, adhocBackupImmutable *bool

	if req.BackupRetentionPolicy.IsSet() {
		brp := req.BackupRetentionPolicy.Value
		if brp.BackupMinimumEnforcedRetentionDays.IsSet() {
			val := int64(brp.BackupMinimumEnforcedRetentionDays.Value)
			backupMinimumEnforcedRetentionDuration = &val
		}
		if brp.DailyBackupImmutable.IsSet() {
			dailyBackupImmutable = &brp.DailyBackupImmutable.Value
		}
		if brp.WeeklyBackupImmutable.IsSet() {
			weeklyBackupImmutable = &brp.WeeklyBackupImmutable.Value
		}
		if brp.MonthlyBackupImmutable.IsSet() {
			monthlyBackupImmutable = &brp.MonthlyBackupImmutable.Value
		}
		if brp.ManualBackupImmutable.IsSet() {
			adhocBackupImmutable = &brp.ManualBackupImmutable.Value
		}
	}

	updateParams := &commonparams.BackupVaultParams{
		BackupVaultID: params.BackupVaultId,
		AccountName:   params.ProjectNumber,
		OwnerID:       params.ProjectNumber,
		Region:        params.LocationId,
		Description:   &description,
		BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
			BackupMinimumEnforcedRetentionDuration: backupMinimumEnforcedRetentionDuration,
			IsDailyBackupImmutable:                 dailyBackupImmutable,
			IsWeeklyBackupImmutable:                weeklyBackupImmutable,
			IsMonthlyBackupImmutable:               monthlyBackupImmutable,
			IsAdhocBackupImmutable:                 adhocBackupImmutable,
		},
	}

	if req.BucketDetails != nil {
		var bucketDetails datamodel.BucketDetailsArray
		for _, bucket := range req.BucketDetails {
			bucketDetail := &datamodel.BucketDetails{}
			if bucket.BucketName.IsSet() {
				bucketDetail.BucketName = bucket.BucketName.Value
			}
			if bucket.ServiceAccountName.IsSet() {
				bucketDetail.ServiceAccountName = bucket.ServiceAccountName.Value
			}
			if bucket.VendorSubnetId.IsSet() {
				bucketDetail.VendorSubnetID = bucket.VendorSubnetId.Value
			}
			if bucket.TenantProjectNumber.IsSet() {
				bucketDetail.TenantProjectNumber = bucket.TenantProjectNumber.Value
			}
			if bucket.SatisfiesPzs.IsSet() {
				bucketDetail.SatisfiesPzs = bucket.SatisfiesPzs.Value
			}
			if bucket.SatisfiesPzi.IsSet() {
				bucketDetail.SatisfiesPzi = bucket.SatisfiesPzi.Value
			}
			bucketDetails = append(bucketDetails, bucketDetail)
		}
		updateParams.BucketDetails = bucketDetails
	}

	// Map CMEK-related fields into params so the orchestrator can update the
	// underlying VCP backup vault's CMEK attributes when requested.
	if req.EncryptionState.IsSet() {
		state := string(req.EncryptionState.Value)
		updateParams.CmekEncryptionState = &state
	}
	if req.BackupsPrimaryKeyVersion.IsSet() {
		pkv := req.BackupsPrimaryKeyVersion.Value
		updateParams.CmekBackupsPrimaryKeyVersion = &pkv
	}

	useExternalUUID := true
	// If the request looks like a "pure CMEK update" (only CMEK fields, no metadata):
	if (req.EncryptionState.IsSet() || req.BackupsPrimaryKeyVersion.IsSet()) &&
		!req.Description.IsSet() &&
		!req.BackupRetentionPolicy.IsSet() &&
		req.BucketDetails == nil {
		// then treat BackupVaultId as a concrete UUID in the target region
		useExternalUUID = false
	}

	updated, operationID, err := h.Orchestrator.UpdateBackupVaultInternal(ctx, updateParams, useExternalUUID)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			logger.Error("Failed to update backup vault - validation error", "error", err.Error())
			return &gcpgenserver.V1betaInternalUpdateBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		if errors.IsNotFoundErr(err) {
			logger.Error("BackupVault not found", "error", err.Error())
			return &gcpgenserver.V1betaInternalUpdateBackupVaultNotFound{
				Code:    404,
				Message: "BackupVault not found",
			}, nil
		}
		if errors.IsConflictErr(err) {
			logger.Error("Conflict updating backup vault", "error", err.Error())
			return &gcpgenserver.V1betaInternalUpdateBackupVaultConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update BackupVault in VCP database", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to update BackupVault in VCP database",
		}, err
	}

	resp := convertCoreModelsToBackupVaultV1beta(updated)
	bvJSON, err := encodeBackupVaultConfigV1(resp)
	if err != nil {
		logger.Error("Failed to marshal backup vault", "error", err.Error())
		return &gcpgenserver.V1betaInternalUpdateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to marshal Backup vault",
		}, nil
	}

	logger.Info("Successfully updated BackupVault",
		"backupVaultId", params.BackupVaultId,
		"operationId", operationID)

	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Response: bvJSON,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}

	return &gcpgenserver.OperationV1beta{
		Response: bvJSON,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func (h Handler) V1betaInternalDescribeBackup(ctx context.Context, params gcpgenserver.V1betaInternalDescribeBackupParams) (gcpgenserver.V1betaInternalDescribeBackupRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalDescribeBackupInternalServerError{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	backup, err := h.Orchestrator.GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalDescribeBackupNotFound{
				Code:    404,
				Message: "Backup not found",
			}, nil
		}
		logger.Error("Failed to get backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalDescribeBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}

	isRestoring := backup.Attributes != nil && backup.Attributes.RestoreVolumeCount > 0
	resp := convertBackupDataModelToInternalBackupsV1beta(backup, isRestoring)

	return &gcpgenserver.V1betaInternalDescribeBackupOK{
		Backups: []gcpgenserver.InternalBackupV1beta{resp},
	}, nil
}

func (h Handler) V1betaInternalDeleteBackupUnderBackupVault(ctx context.Context, params gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultParams) (gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	_, err := h.Orchestrator.GetBackupByExternalUUID(ctx, params.BackupVaultId, params.BackupId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultNotFound{
				Code:    404,
				Message: fmt.Sprintf("Backup %s not found in backup vault %s", params.BackupId, params.BackupVaultId),
			}, nil
		}
		logger.Errorf("Failed to get backup: %s", err.Error())
		return &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError{Code: 500, Message: err.Error()}, err
	}
	// If the request belongs to VSA, we will delete the backup using the orchestrator
	vsaParams := &commonparams.DeleteBackupParams{
		AccountName:     params.ProjectNumber,
		BackupVaultUUID: params.BackupVaultId,
		BackupUUID:      params.BackupId,
		Region:          params.LocationId,
	}
	jobId, err := h.Orchestrator.DeleteBackupInternal(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to delete backup", "error", err.Error())
		return &gcpgenserver.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError{Code: 500, Message: err.Error()}, err
	}
	if jobId == "" {
		jobId = uuid.New().String()
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(operationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}
	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

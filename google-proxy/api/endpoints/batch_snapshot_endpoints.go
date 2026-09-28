package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var fetchBatchSnapshotsFromCVPFn = fetchBatchSnapshotsFromCVP

func (h Handler) V1betaBatchListSnapshots(ctx context.Context, req *gcpgenserver.BatchSnapshotUUIDListV1beta, params gcpgenserver.V1betaBatchListSnapshotsParams) (gcpgenserver.V1betaBatchListSnapshotsRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListSnapshotsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListSnapshotsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListSnapshotsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if len(req.SnapshotUuids) == 0 {
		return &gcpgenserver.V1betaBatchListSnapshotsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "snapshotUuids is required and must have at least 1 item",
		}, nil
	}

	if len(req.SnapshotUuids) > env.MaxBatchSnapshotUUIDs {
		return &gcpgenserver.V1betaBatchListSnapshotsBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("snapshotUuids in body should have at most %d items", env.MaxBatchSnapshotUUIDs),
		}, nil
	}

	fieldSet := batchSnapshotFieldsAsSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListSnapshotsVCPOnly(ctx, req.SnapshotUuids, fieldSet)
	}

	return h.batchListSnapshotsVCPAndCVPParallel(ctx, req.SnapshotUuids, params, fieldSet)
}

func (h Handler) batchListSnapshotsVCPOnly(ctx context.Context, snapshotUUIDs []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListSnapshotsRes, error) {
	logger := util.GetLogger(ctx)

	snapshots, err := h.Orchestrator.GetSnapshotsByUUIDs(ctx, snapshotUUIDs)
	if err != nil {
		logger.Error("Failed to get snapshots by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListSnapshotsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting snapshots",
		}, nil
	}

	out := make([]gcpgenserver.BatchSnapshotV1beta, 0, len(snapshots))
	for _, snap := range snapshots {
		out = append(out, convertSnapshotToBatchSnapshot(snap, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListSnapshotsOK{Snapshots: out}, nil
}

func (h Handler) batchListSnapshotsVCPAndCVPParallel(ctx context.Context, snapshotUUIDs []string, params gcpgenserver.V1betaBatchListSnapshotsParams, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListSnapshotsRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpSnaps []*models.Snapshot
		vcpErr   error
		cvpSnaps []gcpgenserver.BatchSnapshotV1beta
		cvpErr   error
		wg       sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpSnaps, vcpErr = h.Orchestrator.GetSnapshotsByUUIDs(ctx, snapshotUUIDs)
	}()

	go func() {
		defer wg.Done()
		cvpSnaps, cvpErr = fetchBatchSnapshotsFromCVPFn(ctx, snapshotUUIDs, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch snapshot queries failed", "vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListSnapshotsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "An internal error occurred while getting snapshots from both CVP and VCP",
		}, nil
	}

	var vcpBatch []gcpgenserver.BatchSnapshotV1beta
	if vcpErr != nil {
		logger.Warn("VCP batch snapshot query failed, returning SDE results only", "error", vcpErr.Error())
	} else {
		vcpBatch = make([]gcpgenserver.BatchSnapshotV1beta, 0, len(vcpSnaps))
		for _, snap := range vcpSnaps {
			vcpBatch = append(vcpBatch, convertSnapshotToBatchSnapshot(snap, fieldSet))
		}
	}

	if cvpErr != nil {
		logger.Warn("CVP batch snapshot query failed, returning VCP results only", "error", cvpErr.Error())
	}

	all := append(vcpBatch, cvpSnaps...)
	return &gcpgenserver.V1betaBatchListSnapshotsOK{Snapshots: all}, nil
}

func fetchBatchSnapshotsFromCVP(ctx context.Context, snapshotUUIDs []string, params gcpgenserver.V1betaBatchListSnapshotsParams, fieldSet map[string]bool) ([]gcpgenserver.BatchSnapshotV1beta, error) {
	cvpClient := cvpClientFromContext(ctx)

	cvpParams := cvpBatch.NewV1betaBatchListSnapshotsParamsWithContext(ctx)
	cvpParams.SetBody(&cvpmodels.SnapshotIDListV1beta{
		SnapshotUUIDs: snapshotUUIDs,
	})
	applyBatchCvpListCommonParams(cvpParams, params.LocationId, batchListFieldStrings(params.Fields), params.XCorrelationID)

	cviResponse, err := cvpClient.Batch.V1betaBatchListSnapshots(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list snapshots failed: %w", err)
	}

	var result []gcpgenserver.BatchSnapshotV1beta
	if cviResponse != nil && cviResponse.Payload != nil {
		for _, p := range cviResponse.Payload.Snapshots {
			if p != nil {
				bp := convertCVPBatchSnapshotToGCPBatchSnapshot(p)
				applyBatchSnapshotFieldQuery(&bp, fieldSet)
				result = append(result, bp)
			}
		}
	}

	return result, nil
}

func batchSnapshotFieldsAsSet(fields []gcpgenserver.V1betaBatchListSnapshotsFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

func convertSnapshotToBatchSnapshot(snap *models.Snapshot, fieldSet map[string]bool) gcpgenserver.BatchSnapshotV1beta {
	bp := gcpgenserver.BatchSnapshotV1beta{
		SnapshotId: gcpgenserver.NewOptString(snap.UUID),
	}
	if fieldSet == nil {
		applyBatchSnapshotFieldQuery(&bp, nil)
		return bp
	}

	if !snap.CreatedAt.IsZero() {
		bp.Created = gcpgenserver.NewOptDateTime(snap.CreatedAt)
	}
	if snap.Name != "" {
		bp.ResourceId = gcpgenserver.NewOptString(snap.Name)
	}
	if snap.LifeCycleState != "" {
		bp.SnapshotState = gcpgenserver.NewOptBatchSnapshotV1betaSnapshotState(mapLifecycleToBatchSnapshotState(snap.LifeCycleState))
	}
	if snap.LifeCycleStateDetails != "" {
		bp.SnapshotStateDetails = gcpgenserver.NewOptString(snap.LifeCycleStateDetails)
	}
	if snap.VolumeUUID != "" {
		bp.VolumeId = gcpgenserver.NewOptString(snap.VolumeUUID)
	}
	bp.UsedBytes = gcpgenserver.NewOptFloat64(float64(snap.SizeInBytes))
	if snap.Description != "" {
		bp.Description = gcpgenserver.NewOptString(snap.Description)
	}
	bp.IsAppConsistent = gcpgenserver.NewOptBool(snap.IsAppConsistent)
	applyBatchSnapshotFieldQuery(&bp, fieldSet)
	return bp
}

func mapLifecycleToBatchSnapshotState(s string) gcpgenserver.BatchSnapshotV1betaSnapshotState {
	if s == "" {
		return gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED
	}
	v := gcpgenserver.BatchSnapshotV1betaSnapshotState(s)
	switch v {
	case gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateCREATING,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateUPDATING,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateRESTORING,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateDELETED,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateDISABLED,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateDELETING,
		gcpgenserver.BatchSnapshotV1betaSnapshotStateERROR:
		return v
	default:
		return gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED
	}
}

func convertCVPBatchSnapshotToGCPBatchSnapshot(p *cvpmodels.BatchSnapshotV1beta) gcpgenserver.BatchSnapshotV1beta {
	bp := gcpgenserver.BatchSnapshotV1beta{}
	if p.SnapshotID != "" {
		bp.SnapshotId = gcpgenserver.NewOptString(p.SnapshotID)
	}
	if p.Created != nil {
		bp.Created = gcpgenserver.NewOptDateTime(time.Time(*p.Created))
	}
	if p.ResourceID != nil {
		bp.ResourceId = gcpgenserver.NewOptString(*p.ResourceID)
	}
	if p.SnapshotState != "" {
		bp.SnapshotState = gcpgenserver.NewOptBatchSnapshotV1betaSnapshotState(
			gcpgenserver.BatchSnapshotV1betaSnapshotState(p.SnapshotState))
	}
	if p.SnapshotStateDetails != nil {
		bp.SnapshotStateDetails = gcpgenserver.NewOptString(*p.SnapshotStateDetails)
	}
	if p.VolumeID != nil {
		bp.VolumeId = gcpgenserver.NewOptString(*p.VolumeID)
	}
	if p.UsedBytes != nil {
		bp.UsedBytes = gcpgenserver.NewOptFloat64(*p.UsedBytes)
	}
	if p.IsAppConsistent != nil {
		bp.IsAppConsistent = gcpgenserver.NewOptBool(*p.IsAppConsistent)
	}
	if p.Description != nil {
		bp.Description = gcpgenserver.NewOptString(*p.Description)
	}
	return bp
}

// applyBatchSnapshotFieldQuery applies the batch `fields` query in one pass.
// - fieldSet nil: strip all optional fields so only snapshotId remains in JSON.
// - fieldSet set: drop unrequested fields; for each requested field that is still unset, set an empty value so it appears in JSON.
func applyBatchSnapshotFieldQuery(bp *gcpgenserver.BatchSnapshotV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		bp.Created.Reset()
		bp.ResourceId.Reset()
		bp.SnapshotState.Reset()
		bp.SnapshotStateDetails.Reset()
		bp.VolumeId.Reset()
		bp.UsedBytes.Reset()
		bp.IsAppConsistent.Reset()
		bp.Description.Reset()
		return
	}
	if fieldSet["created"] {
		if !bp.Created.Set {
			bp.Created = gcpgenserver.NewOptDateTime(time.Time{})
		}
	} else {
		bp.Created.Reset()
	}
	if fieldSet["resourceId"] {
		if !bp.ResourceId.Set {
			bp.ResourceId = gcpgenserver.NewOptString("")
		}
	} else {
		bp.ResourceId.Reset()
	}
	if fieldSet["snapshotState"] {
		if !bp.SnapshotState.Set {
			bp.SnapshotState = gcpgenserver.NewOptBatchSnapshotV1betaSnapshotState(
				gcpgenserver.BatchSnapshotV1betaSnapshotState(""))
		}
	} else {
		bp.SnapshotState.Reset()
	}
	if fieldSet["snapshotStateDetails"] {
		if !bp.SnapshotStateDetails.Set {
			bp.SnapshotStateDetails = gcpgenserver.NewOptString("")
		}
	} else {
		bp.SnapshotStateDetails.Reset()
	}
	if fieldSet["volumeId"] {
		if !bp.VolumeId.Set {
			bp.VolumeId = gcpgenserver.NewOptString("")
		}
	} else {
		bp.VolumeId.Reset()
	}
	if fieldSet["usedBytes"] {
		if !bp.UsedBytes.Set {
			bp.UsedBytes = gcpgenserver.NewOptFloat64(0)
		}
	} else {
		bp.UsedBytes.Reset()
	}
	if fieldSet["isAppConsistent"] {
		if !bp.IsAppConsistent.Set {
			bp.IsAppConsistent = gcpgenserver.NewOptBool(false)
		}
	} else {
		bp.IsAppConsistent.Reset()
	}
	if fieldSet["description"] {
		if !bp.Description.Set {
			bp.Description = gcpgenserver.NewOptString("")
		}
	} else {
		bp.Description.Reset()
	}
}

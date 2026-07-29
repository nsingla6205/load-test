package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// V1BatchUpgradeClusters handles POST /v1/clusters/batchUpgrade.
func (h *Handler) V1BatchUpgradeClusters(ctx context.Context, req *oasgenserver.BatchClusterUpgradeRequestV1) (oasgenserver.V1BatchUpgradeClustersRes, error) {
	log := util.GetLogger(ctx)
	log.Info("Received batch cluster upgrade request")

	orchestratorParams := &commonparams.BatchUpgradeClusterParams{
		SelectionType:      string(req.SelectionType),
		Percentage:         req.Percentage.Value,
		PoolIDs:            req.PoolIds,
		MaxClusters:        req.MaxClusters.Value,
		MaxConcurrency:     req.MaxConcurrency.Value,
		FailureThreshold:   req.FailureThreshold.Value,
		VSABuildImage:      req.VsaBuildImage.Value,
		MediatorBuildImage: req.MediatorBuildImage.Value,
		ForceUpgrade:       req.ForceUpgrade.Value,
		Metadata:           req.Metadata.Value,
	}

	if filters, ok := req.Filters.Get(); ok {
		orchestratorParams.Filters = &commonparams.ClusterFilters{
			Zones:         filters.Zones,
			OntapVersions: filters.OntapVersions,
			ClusterStates: filters.ClusterStates,
		}
	}

	switch orchestratorParams.SelectionType {
	case string(models.SelectionTypePercentage):
		if orchestratorParams.Percentage <= 0 || orchestratorParams.Percentage > 100 {
			return &oasgenserver.V1BatchUpgradeClustersBadRequest{
				Code:    400,
				Message: "Percentage must be between 1 and 100",
			}, nil
		}
	case string(models.SelectionTypePoolList):
		if len(orchestratorParams.PoolIDs) == 0 {
			return &oasgenserver.V1BatchUpgradeClustersBadRequest{
				Code:    400,
				Message: "Pool IDs are required when selection type is 'poolList'",
			}, nil
		}
		if err := validateBatchUpgradePoolIDs(orchestratorParams.PoolIDs); err != nil {
			return &oasgenserver.V1BatchUpgradeClustersBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
	default:
		return &oasgenserver.V1BatchUpgradeClustersBadRequest{
			Code:    400,
			Message: "Selection type must be 'percentage' or 'poolList'",
		}, nil
	}

	response, jobID, err := h.Orchestrator.BatchUpgradeClusters(ctx, orchestratorParams)
	if err != nil {
		log.Error("Failed to initiate batch cluster upgrade", "error", err)
		switch {
		case errors.IsBadRequestErr(err):
			return &oasgenserver.V1BatchUpgradeClustersBadRequest{Code: 400, Message: err.Error()}, nil
		case errors.IsNotFoundErr(err):
			return &oasgenserver.V1BatchUpgradeClustersNotFound{Code: 404, Message: err.Error()}, nil
		case errors.IsConflictErr(err):
			return &oasgenserver.V1BatchUpgradeClustersConflict{Code: 409, Message: err.Error()}, nil
		default:
			return &oasgenserver.V1BatchUpgradeClustersInternalServerError{
				Code:    500,
				Message: fmt.Sprintf("Failed to initiate batch upgrade: %v", err),
			}, nil
		}
	}

	log.Info("Batch cluster upgrade initiated successfully",
		"batchUpgradeId", response.BatchUpgradeID,
		"jobId", jobID,
		"totalClusters", response.TotalClusters)

	return &oasgenserver.BatchClusterUpgradeResponseV1{
		BatchUpgradeId:   response.BatchUpgradeID,
		TotalClusters:    response.TotalClusters,
		SelectedClusters: convertSelectedClustersToAPI(response.SelectedClusters),
		Status:           oasgenserver.BatchClusterUpgradeResponseV1Status(response.Status),
		JobId:            response.JobID,
	}, nil
}

// V1GetBatchUpgradeStatus handles GET /v1/clusters/batchUpgrade/{batchUpgradeId}.
func (h *Handler) V1GetBatchUpgradeStatus(ctx context.Context, params oasgenserver.V1GetBatchUpgradeStatusParams) (oasgenserver.V1GetBatchUpgradeStatusRes, error) {
	log := util.GetLogger(ctx)
	log.Info("Received batch upgrade status request", "batchUpgradeId", params.BatchUpgradeId)

	progress, err := h.Orchestrator.GetBatchUpgradeStatus(ctx, params.BatchUpgradeId)
	if err != nil {
		log.Error("Failed to get batch upgrade status", "batchUpgradeId", params.BatchUpgradeId, "error", err)
		if errors.IsNotFoundErr(err) {
			return &oasgenserver.V1GetBatchUpgradeStatusNotFound{
				Code:    404,
				Message: fmt.Sprintf("Batch upgrade job not found: %s", params.BatchUpgradeId),
			}, nil
		}
		return &oasgenserver.V1GetBatchUpgradeStatusInternalServerError{
			Code:    500,
			Message: fmt.Sprintf("Failed to get batch upgrade status: %v", err),
		}, nil
	}

	log.Info("Batch upgrade status retrieved successfully",
		"batchUpgradeId", params.BatchUpgradeId,
		"status", progress.Status)

	return &oasgenserver.UpgradeProgressV1{
		JobId:    progress.JobID,
		Status:   oasgenserver.UpgradeProgressV1Status(progress.Status),
		Clusters: convertClusterStatusesToAPI(progress.Clusters),
		Errors:   convertUpgradeErrorsToAPI(progress.Errors),
		Warnings: progress.Warnings,
	}, nil
}

func convertSelectedClustersToAPI(clusters []models.SelectedCluster) []oasgenserver.SelectedClusterV1 {
	apiClusters := make([]oasgenserver.SelectedClusterV1, len(clusters))
	for i, cluster := range clusters {
		apiClusters[i] = oasgenserver.SelectedClusterV1{
			ClusterId:      cluster.ClusterID,
			PoolId:         cluster.PoolID,
			CurrentVersion: cluster.CurrentVersion,
			Priority:       cluster.Priority,
		}
	}
	return apiClusters
}

func validateBatchUpgradePoolIDs(poolIDs []string) error {
	validationErrors := make([]string, 0)
	seenPoolIDs := make(map[string]struct{}, len(poolIDs))

	for _, poolID := range poolIDs {
		if strings.TrimSpace(poolID) != poolID {
			validationErrors = append(validationErrors,
				fmt.Sprintf("%q: leading or trailing whitespace is not allowed", poolID))
			continue
		}
		if _, err := uuid.Parse(poolID); err != nil {
			validationErrors = append(validationErrors,
				fmt.Sprintf("%q: must be a valid UUID", poolID))
			continue
		}
		if _, exists := seenPoolIDs[poolID]; exists {
			validationErrors = append(validationErrors,
				fmt.Sprintf("%q: duplicate pool ID", poolID))
			continue
		}
		seenPoolIDs[poolID] = struct{}{}
	}

	if len(validationErrors) > 0 {
		return fmt.Errorf("invalid pool IDs: %s", strings.Join(validationErrors, "; "))
	}
	return nil
}

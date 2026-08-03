package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-faster/jx"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// encryptPoolResponse is the pool payload embedded under the operation response.
type encryptPoolResponse struct {
	PoolId                  string `json:"poolId,omitempty"`
	ResourceId              string `json:"resourceId,omitempty"`
	StoragePoolState        string `json:"storagePoolState,omitempty"`
	StoragePoolStateDetails string `json:"storagePoolStateDetails,omitempty"`
	KmsConfigId             string `json:"kmsConfigId,omitempty"`
}

// V1EncryptPool is the internal (SRE-facing) endpoint that encrypts or migrates the CMEK policy of a single VSA pool.
func (h Handler) V1EncryptPool(ctx context.Context, req *oasgenserver.EncryptPoolRequestV1, params oasgenserver.V1EncryptPoolParams) (oasgenserver.V1EncryptPoolRes, error) {
	logger := util.GetLogger(ctx)
	ownerUUID := req.OwnerID

	hasUpgrade, err := h.Orchestrator.HasActiveClusterUpgrade(ctx, params.PoolId)
	if err != nil {
		logger.Errorf("Failed to check cluster upgrade status for pool %s: %v", params.PoolId, err)
		return &oasgenserver.V1EncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	if hasUpgrade {
		return &oasgenserver.V1EncryptPoolConflict{
			Code:    http.StatusConflict,
			Message: "Storage pool is temporarily unavailable, please try again later",
		}, nil
	}

	kmsConfigID := req.KmsConfigId.String()
	kmsConfigs, err := h.Orchestrator.GetMultipleKMSConfigs(ctx, []string{kmsConfigID})
	if err != nil {
		logger.Errorf("Failed to look up CMEK policy %s: %v", kmsConfigID, err)
		return &oasgenserver.V1EncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	if len(kmsConfigs) == 0 || kmsConfigs[0] == nil {
		return &oasgenserver.V1EncryptPoolNotFound{
			Code:    http.StatusNotFound,
			Message: fmt.Sprintf("CMEK policy with UUID %s not found", kmsConfigID),
		}, nil
	}

	encryptParams := &commonparams.EncryptPoolParams{
		PoolUUID:       params.PoolId,
		KmsConfigID:    kmsConfigID,
		KmsConfigState: kmsConfigs[0].State,
		AccountName:    ownerUUID,
	}
	if params.XCorrelationID.IsSet() {
		encryptParams.XCorrelationID = params.XCorrelationID.Value
	}

	updatedPool, operationID, err := h.Orchestrator.EncryptPool(ctx, encryptParams)
	if err != nil {
		logger.Errorf("Failed to encrypt pool %s: %v", params.PoolId, err)
		switch {
		case errors.IsNotFoundErr(err):
			return &oasgenserver.V1EncryptPoolNotFound{
				Code:    http.StatusNotFound,
				Message: "Pool not found",
			}, nil
		case errors.IsBadRequestErr(err):
			return &oasgenserver.V1EncryptPoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		case errors.IsConflictErr(err):
			return &oasgenserver.V1EncryptPoolConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		default:
			return &oasgenserver.V1EncryptPoolInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}, nil
		}
	}

	resp, err := encodeEncryptPoolResponse(updatedPool, kmsConfigID)
	if err != nil {
		logger.Errorf("Failed to encode encrypt pool response: %v", err)
		return &oasgenserver.V1EncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	operationName := fmt.Sprintf("/v1/pools/%s/operations/%s", params.PoolId, operationID)
	return &oasgenserver.OperationV1{
		Name:     oasgenserver.NewOptString(operationName),
		Response: resp,
		Done:     oasgenserver.NewOptBool(false),
	}, nil
}

func encodeEncryptPoolResponse(pool *coremodels.Pool, kmsConfigID string) (jx.Raw, error) {
	poolResp := encryptPoolResponse{KmsConfigId: kmsConfigID}
	if pool != nil {
		poolResp.PoolId = pool.UUID
		poolResp.ResourceId = pool.Name
		poolResp.StoragePoolState = pool.State
		poolResp.StoragePoolStateDetails = pool.StateDetails
	}
	data, err := json.Marshal(poolResp)
	if err != nil {
		return nil, err
	}
	return data, nil
}

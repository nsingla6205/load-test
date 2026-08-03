package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-faster/jx"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
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

// V1betaEncryptPool encrypts or migrates the CMEK policy of a single VSA pool.
// The owning account UUID is resolved from the project number and the request is
// handled directly by the shared orchestrator (same pattern as the KMS config
// create/update/delete and encryptVolumes endpoints) — the orchestrator enqueues
// the ENCRYPT_POOL job and starts the Temporal workflow that the worker executes.
func (h Handler) V1betaEncryptPool(ctx context.Context, req *gcpgenserver.EncryptPoolRequestV1beta, params gcpgenserver.V1betaEncryptPoolParams) (gcpgenserver.V1betaEncryptPoolRes, error) {
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	logger := util.GetLogger(ctx)

	if _, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId); parsingErr != nil {
		return &gcpgenserver.V1betaEncryptPoolBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Resolve the owning account UUID from the project number. The orchestrator's
	// EncryptPool contract expects the account UUID, whereas Google callers only
	// supply the project number in the path.
	account, err := h.Orchestrator.GetAccount(ctx, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Warnf("Account not found for project number %s", params.ProjectNumber)
			return &gcpgenserver.V1betaEncryptPoolNotFound{
				Code:    http.StatusNotFound,
				Message: "Account not found",
			}, nil
		}
		logger.Errorf("Failed to resolve account for project number %s: %v", params.ProjectNumber, err)
		return &gcpgenserver.V1betaEncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	hasUpgrade, err := h.Orchestrator.HasActiveClusterUpgrade(ctx, params.PoolId)
	if err != nil {
		logger.Errorf("Failed to check cluster upgrade status for pool %s: %v", params.PoolId, err)
		return &gcpgenserver.V1betaEncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	if hasUpgrade {
		return &gcpgenserver.V1betaEncryptPoolConflict{
			Code:    http.StatusConflict,
			Message: "Storage pool is temporarily unavailable, please try again later",
		}, nil
	}

	kmsConfigID := req.KmsConfigId.String()
	kmsConfigs, err := h.Orchestrator.GetMultipleKMSConfigs(ctx, []string{kmsConfigID})
	if err != nil {
		logger.Errorf("Failed to look up CMEK policy %s: %v", kmsConfigID, err)
		return &gcpgenserver.V1betaEncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	if len(kmsConfigs) == 0 || kmsConfigs[0] == nil {
		return &gcpgenserver.V1betaEncryptPoolNotFound{
			Code:    http.StatusNotFound,
			Message: fmt.Sprintf("CMEK policy with UUID %s not found", kmsConfigID),
		}, nil
	}

	encryptParams := &commonparams.EncryptPoolParams{
		PoolUUID:       params.PoolId,
		KmsConfigID:    kmsConfigID,
		KmsConfigState: kmsConfigs[0].State,
		AccountName:    account.UUID,
		ProjectNumber:  params.ProjectNumber,
	}
	if params.XCorrelationID.IsSet() {
		encryptParams.XCorrelationID = params.XCorrelationID.Value
	}

	updatedPool, operationID, err := h.Orchestrator.EncryptPool(ctx, encryptParams)
	if err != nil {
		logger.Errorf("Failed to encrypt pool %s: %v", params.PoolId, err)
		switch {
		case errors.IsNotFoundErr(err):
			return &gcpgenserver.V1betaEncryptPoolNotFound{
				Code:    http.StatusNotFound,
				Message: err.Error(),
			}, nil
		case errors.IsBadRequestErr(err):
			return &gcpgenserver.V1betaEncryptPoolBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		case errors.IsConflictErr(err):
			return &gcpgenserver.V1betaEncryptPoolConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		default:
			return &gcpgenserver.V1betaEncryptPoolInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}, nil
		}
	}

	resp, err := encodeEncryptPoolResponse(updatedPool, kmsConfigID)
	if err != nil {
		logger.Errorf("Failed to encode encrypt pool response: %v", err)
		return &gcpgenserver.V1betaEncryptPoolInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(utils.GenerateOperationURL(params.ProjectNumber, params.LocationId, operationID)),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(false),
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

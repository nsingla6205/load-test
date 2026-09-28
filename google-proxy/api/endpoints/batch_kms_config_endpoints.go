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

var (
	fetchBatchKmsConfigsFromCVPFn = fetchBatchKmsConfigsFromCVP
	maxBatchKmsConfigIDs          = func() int { return env.MaxBatchKmsConfigUUIDs }
)

func (h Handler) V1betaBatchListKmsConfigs(
	ctx context.Context,
	req *gcpgenserver.BatchKmsConfigIdListV1beta,
	params gcpgenserver.V1betaBatchListKmsConfigsParams,
) (gcpgenserver.V1betaBatchListKmsConfigsRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListKmsConfigsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListKmsConfigsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListKmsConfigsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req == nil || len(req.KmsConfigIds) == 0 {
		return &gcpgenserver.V1betaBatchListKmsConfigsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "kmsConfigIds is required and must have at least 1 item",
		}, nil
	}

	ids := utils.DeduplicateSlice(req.KmsConfigIds)
	if len(ids) > maxBatchKmsConfigIDs() {
		return &gcpgenserver.V1betaBatchListKmsConfigsBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("kmsConfigIds in body should have at most %d items", maxBatchKmsConfigIDs()),
		}, nil
	}

	// Reject malformed UUIDs up front so we don't hit the VCP DB or CVP for
	// inputs that can never match a real KMS configuration. Validation runs
	// against the original (pre-dedup) list so the offending index in the
	// message matches what the caller sent.
	if msg := validateUUIDList(req.KmsConfigIds, "kmsConfigIds"); msg != "" {
		return &gcpgenserver.V1betaBatchListKmsConfigsBadRequest{
			Code:    http.StatusBadRequest,
			Message: msg,
		}, nil
	}

	fieldSet := buildBatchKmsConfigFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListKmsConfigsVCPOnly(ctx, ids, fieldSet)
	}

	return h.batchListKmsConfigsParallel(ctx, ids, params, fieldSet)
}

func (h Handler) batchListKmsConfigsVCPOnly(
	ctx context.Context,
	kmsConfigIDs []string,
	fieldSet map[string]bool,
) (gcpgenserver.V1betaBatchListKmsConfigsRes, error) {
	logger := util.GetLogger(ctx)

	kmsConfigs, err := h.Orchestrator.GetKmsConfigsByUUIDs(ctx, kmsConfigIDs)
	if err != nil {
		logger.Error("Failed to get KMS configs by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListKmsConfigsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting KMS configs",
		}, nil
	}

	vcpBatchKmsConfigs := make([]gcpgenserver.BatchKmsConfigV1beta, 0, len(kmsConfigs))
	for _, kmsConfig := range kmsConfigs {
		vcpBatchKmsConfigs = append(vcpBatchKmsConfigs, convertKmsConfigToBatchKmsConfig(kmsConfig, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListKmsConfigsOK{KmsConfigs: vcpBatchKmsConfigs}, nil
}

func (h Handler) batchListKmsConfigsParallel(
	ctx context.Context,
	kmsConfigIDs []string,
	params gcpgenserver.V1betaBatchListKmsConfigsParams,
	fieldSet map[string]bool,
) (gcpgenserver.V1betaBatchListKmsConfigsRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpKmsConfigs []*models.KmsConfig
		vcpErr        error
		cvpKmsConfigs []gcpgenserver.BatchKmsConfigV1beta
		cvpErr        error
		wg            sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpKmsConfigs, vcpErr = h.Orchestrator.GetKmsConfigsByUUIDs(ctx, kmsConfigIDs)
	}()

	go func() {
		defer wg.Done()
		cvpKmsConfigs, cvpErr = fetchBatchKmsConfigsFromCVPFn(ctx, kmsConfigIDs, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch KMS config queries failed",
			"vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListKmsConfigsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting KMS configs from both systems",
		}, nil
	}

	var vcpBatchKmsConfigs []gcpgenserver.BatchKmsConfigV1beta
	vcpIDs := make(map[string]struct{})
	if vcpErr != nil {
		logger.Warn("VCP batch KMS config query failed, returning SDE results only", "error", vcpErr.Error())
	} else {
		vcpBatchKmsConfigs = make([]gcpgenserver.BatchKmsConfigV1beta, 0, len(vcpKmsConfigs))
		for _, kmsConfig := range vcpKmsConfigs {
			if kmsConfig == nil {
				continue
			}
			vcpIDs[kmsConfig.UUID] = struct{}{}
			vcpBatchKmsConfigs = append(vcpBatchKmsConfigs, convertKmsConfigToBatchKmsConfig(kmsConfig, fieldSet))
		}
	}

	if cvpErr != nil {
		logger.Warn("CVP batch KMS config query failed, returning VCP results only", "error", cvpErr.Error())
	}

	allKmsConfigs := make([]gcpgenserver.BatchKmsConfigV1beta, 0, len(vcpBatchKmsConfigs)+len(cvpKmsConfigs))
	allKmsConfigs = append(allKmsConfigs, vcpBatchKmsConfigs...)
	for _, kmsConfig := range cvpKmsConfigs {
		if kmsConfig.UUID.Set {
			if _, exists := vcpIDs[kmsConfig.UUID.Value]; exists {
				continue
			}
		}
		allKmsConfigs = append(allKmsConfigs, kmsConfig)
	}
	return &gcpgenserver.V1betaBatchListKmsConfigsOK{KmsConfigs: allKmsConfigs}, nil
}

func fetchBatchKmsConfigsFromCVP(
	ctx context.Context,
	kmsConfigIDs []string,
	params gcpgenserver.V1betaBatchListKmsConfigsParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchKmsConfigV1beta, error) {
	cvpClient := cvpClientFromContext(ctx)
	fields := batchListFieldStrings(params.Fields)

	cvpParams := cvpBatch.NewV1betaBatchListKmsConfigsParamsWithContext(ctx)
	applyBatchCvpListCommonParams(cvpParams, params.LocationId, fields, params.XCorrelationID)
	cvpParams.SetBody(&cvpmodels.KmsConfigIDListV1beta{
		KmsConfigIDs: kmsConfigIDs,
	})

	cvpResponse, err := cvpClient.Batch.V1betaBatchListKmsConfigs(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list KMS configs failed: %w", err)
	}

	var result []gcpgenserver.BatchKmsConfigV1beta
	if cvpResponse != nil && cvpResponse.Payload != nil && cvpResponse.Payload.KmsConfigurations != nil {
		for _, cvpKmsConfig := range cvpResponse.Payload.KmsConfigurations {
			if cvpKmsConfig != nil {
				result = append(result, convertCVPBatchKmsConfigToGCPBatchKmsConfig(cvpKmsConfig, fieldSet))
			}
		}
	}

	return result, nil
}

func buildBatchKmsConfigFieldSet(fields []gcpgenserver.V1betaBatchListKmsConfigsFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

func batchKmsConfigFieldRequested(fieldSet map[string]bool, field string) bool {
	return fieldSet != nil && fieldSet[field]
}

func convertKmsConfigToBatchKmsConfig(
	kmsConfig *models.KmsConfig,
	fieldSet map[string]bool,
) gcpgenserver.BatchKmsConfigV1beta {
	bk := gcpgenserver.BatchKmsConfigV1beta{
		UUID: gcpgenserver.NewOptNilString(kmsConfig.UUID),
	}

	if fieldSet == nil {
		return bk
	}

	if batchKmsConfigFieldRequested(fieldSet, "serviceAccountEmail") {
		if serviceAccountEmail := kmsConfig.KmsAttributes.GetServiceAccountEmail(); serviceAccountEmail != "" {
			bk.ServiceAccountEmail = gcpgenserver.NewOptNilString(serviceAccountEmail)
		} else {
			bk.ServiceAccountEmail.SetToNull()
		}
	}

	if batchKmsConfigFieldRequested(fieldSet, "keyFullPath") {
		keyFullPath := utils.ParsedKeyFullPathResource{
			ProjectID: kmsConfig.KeyProjectID,
			KeyRing:   kmsConfig.KeyRing,
			Location:  kmsConfig.KeyRingLocation,
			CryptoKey: kmsConfig.KeyName,
		}.String()
		if keyFullPath != "" {
			bk.KeyFullPath = gcpgenserver.NewOptNilString(keyFullPath)
		} else {
			bk.KeyFullPath.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "kmsState") {
		if kmsConfig.State != "" {
			bk.KmsState = gcpgenserver.NewOptNilBatchKmsConfigV1betaKmsState(
				gcpgenserver.BatchKmsConfigV1betaKmsState(kmsConfig.State))
		} else {
			bk.KmsState = gcpgenserver.NewOptNilBatchKmsConfigV1betaKmsState(
				gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED)
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "kmsStateDetails") {
		if kmsConfig.StateDetails != "" {
			bk.KmsStateDetails = gcpgenserver.NewOptNilString(kmsConfig.StateDetails)
		} else {
			bk.KmsStateDetails.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "description") {
		if kmsConfig.Description != "" {
			bk.Description = gcpgenserver.NewOptNilString(kmsConfig.Description)
		} else {
			bk.Description.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "createdTime") {
		if kmsConfig.CreatedAt.IsZero() {
			bk.CreatedTime.SetToNull()
		} else {
			bk.CreatedTime = gcpgenserver.NewOptNilDateTime(kmsConfig.CreatedAt)
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "updatedTime") {
		if kmsConfig.UpdatedAt.IsZero() {
			bk.UpdatedTime.SetToNull()
		} else {
			bk.UpdatedTime = gcpgenserver.NewOptNilDateTime(kmsConfig.UpdatedAt)
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "deletedTime") {
		if kmsConfig.DeletedAt != nil {
			bk.DeletedTime = gcpgenserver.NewOptNilDateTime(*kmsConfig.DeletedAt)
		} else {
			bk.DeletedTime.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "instructions") {
		if instructions := getKmsInstructions(kmsConfig); instructions != "" {
			bk.Instructions = gcpgenserver.NewOptNilString(instructions)
		} else {
			bk.Instructions.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "resourceId") {
		if kmsConfig.ResourceID != "" {
			bk.ResourceId = gcpgenserver.NewOptNilString(kmsConfig.ResourceID)
		} else {
			bk.ResourceId.SetToNull()
		}
	}
	return bk
}

func convertCVPBatchKmsConfigToGCPBatchKmsConfig(
	k *cvpmodels.BatchKmsConfigV1beta,
	fieldSet map[string]bool,
) gcpgenserver.BatchKmsConfigV1beta {
	bk := gcpgenserver.BatchKmsConfigV1beta{}

	if k.UUID != nil {
		bk.UUID = gcpgenserver.NewOptNilString(*k.UUID)
	} else {
		bk.UUID.SetToNull()
	}

	if fieldSet == nil {
		return bk
	}

	if batchKmsConfigFieldRequested(fieldSet, "serviceAccountEmail") {
		if k.ServiceAccountEmail != "" {
			bk.ServiceAccountEmail = gcpgenserver.NewOptNilString(k.ServiceAccountEmail)
		} else {
			bk.ServiceAccountEmail.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "keyFullPath") {
		if k.KeyFullPath != nil {
			bk.KeyFullPath = gcpgenserver.NewOptNilString(*k.KeyFullPath)
		} else {
			bk.KeyFullPath.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "kmsState") {
		if k.KmsState != nil {
			bk.KmsState = gcpgenserver.NewOptNilBatchKmsConfigV1betaKmsState(
				gcpgenserver.BatchKmsConfigV1betaKmsState(*k.KmsState))
		} else {
			bk.KmsState = gcpgenserver.NewOptNilBatchKmsConfigV1betaKmsState(
				gcpgenserver.BatchKmsConfigV1betaKmsStateSTATEUNSPECIFIED)
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "kmsStateDetails") {
		if k.KmsStateDetails != nil {
			bk.KmsStateDetails = gcpgenserver.NewOptNilString(*k.KmsStateDetails)
		} else {
			bk.KmsStateDetails.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "description") {
		if k.Description != nil {
			bk.Description = gcpgenserver.NewOptNilString(*k.Description)
		} else {
			bk.Description.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "createdTime") {
		if k.CreatedTime != nil {
			bk.CreatedTime = gcpgenserver.NewOptNilDateTime(time.Time(*k.CreatedTime))
		} else {
			bk.CreatedTime.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "updatedTime") {
		if k.UpdatedTime != nil {
			bk.UpdatedTime = gcpgenserver.NewOptNilDateTime(time.Time(*k.UpdatedTime))
		} else {
			bk.UpdatedTime.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "deletedTime") {
		if k.DeletedTime != nil {
			bk.DeletedTime = gcpgenserver.NewOptNilDateTime(time.Time(*k.DeletedTime))
		} else {
			bk.DeletedTime.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "instructions") {
		if k.Instructions != nil {
			bk.Instructions = gcpgenserver.NewOptNilString(*k.Instructions)
		} else {
			bk.Instructions.SetToNull()
		}
	}
	if batchKmsConfigFieldRequested(fieldSet, "resourceId") {
		if k.ResourceID != nil {
			bk.ResourceId = gcpgenserver.NewOptNilString(*k.ResourceID)
		} else {
			bk.ResourceId.SetToNull()
		}
	}
	return bk
}

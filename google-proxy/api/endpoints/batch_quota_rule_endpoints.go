package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var fetchBatchQuotaRulesFromCVPFn = fetchBatchQuotaRulesFromCVP

// V1betaBatchListQuotaRules batch-lists quota rules by UUID (VCP DB + optional CVP/SDE merge).
func (h Handler) V1betaBatchListQuotaRules(ctx context.Context, req *gcpgenserver.QuotaRuleIdListV1beta, params gcpgenserver.V1betaBatchListQuotaRulesParams) (gcpgenserver.V1betaBatchListQuotaRulesRes, error) {
	if !enableQuotaRule {
		return &gcpgenserver.V1betaBatchListQuotaRulesBadRequest{
			Code:    http.StatusBadRequest,
			Message: quotaRuleAPIDisabledMessage,
		}, nil
	}

	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListQuotaRulesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListQuotaRulesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListQuotaRulesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	uuids := req.GetQuotaRuleUuids()
	if len(uuids) == 0 {
		return &gcpgenserver.V1betaBatchListQuotaRulesBadRequest{
			Code:    http.StatusBadRequest,
			Message: "quotaRuleUuids is required and must have at least 1 item",
		}, nil
	}

	if len(uuids) > env.MaxBatchQuotaRuleUUIDs {
		return &gcpgenserver.V1betaBatchListQuotaRulesBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("quotaRuleUuids in body should have at most %d items", env.MaxBatchQuotaRuleUUIDs),
		}, nil
	}

	fieldSet := buildBatchQuotaRuleFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListQuotaRulesVCPOnly(ctx, uuids, fieldSet)
	}

	return h.batchListQuotaRulesParallel(ctx, uuids, params, fieldSet)
}

func (h Handler) batchListQuotaRulesVCPOnly(ctx context.Context, quotaRuleUUIDs []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListQuotaRulesRes, error) {
	logger := util.GetLogger(ctx)

	rules, err := h.Orchestrator.GetQuotaRulesByUUIDs(ctx, quotaRuleUUIDs)
	if err != nil {
		logger.Error("Failed to get quota rules by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListQuotaRulesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting quota rules",
		}, nil
	}

	out := make([]gcpgenserver.BatchQuotaRuleV1beta, 0, len(rules))
	for _, qr := range rules {
		if qr == nil {
			continue
		}
		v := convertQuotaRuleToV1beta(qr)
		out = append(out, projectBatchQuotaRuleForRequestedFields(v, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListQuotaRulesOK{QuotaRules: out}, nil
}

func (h Handler) batchListQuotaRulesParallel(ctx context.Context, quotaRuleUUIDs []string, params gcpgenserver.V1betaBatchListQuotaRulesParams, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListQuotaRulesRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpRules []*models.QuotaRule
		vcpErr   error
		cvpRules []gcpgenserver.BatchQuotaRuleV1beta
		cvpErr   error
		wg       sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpRules, vcpErr = h.Orchestrator.GetQuotaRulesByUUIDs(ctx, quotaRuleUUIDs)
	}()

	go func() {
		defer wg.Done()
		cvpRules, cvpErr = fetchBatchQuotaRulesFromCVPFn(ctx, quotaRuleUUIDs, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch quota rule queries failed",
			"vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListQuotaRulesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting quota rules from both systems",
		}, nil
	}

	var vcpOut []gcpgenserver.BatchQuotaRuleV1beta
	if vcpErr != nil {
		logger.Warn("VCP batch quota rule query failed, returning SDE results only", "error", vcpErr.Error())
	} else {
		vcpOut = make([]gcpgenserver.BatchQuotaRuleV1beta, 0, len(vcpRules))
		for _, qr := range vcpRules {
			if qr == nil {
				continue
			}
			v := convertQuotaRuleToV1beta(qr)
			vcpOut = append(vcpOut, projectBatchQuotaRuleForRequestedFields(v, fieldSet))
		}
	}

	if cvpErr != nil {
		// VCP succeeded (otherwise we returned above when both failed). Return VCP rows; CVP/SDE merge is best-effort.
		logger.Warn("CVP batch quota rule query failed, returning VCP results only", "error", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListQuotaRulesOK{QuotaRules: vcpOut}, nil
	}

	all := append(vcpOut, cvpRules...)
	return &gcpgenserver.V1betaBatchListQuotaRulesOK{QuotaRules: all}, nil
}

func fetchBatchQuotaRulesFromCVP(
	ctx context.Context,
	quotaRuleUUIDs []string,
	params gcpgenserver.V1betaBatchListQuotaRulesParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchQuotaRuleV1beta, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	var fields []string
	for _, f := range params.Fields {
		fields = append(fields, string(f))
	}
	// Keep quotaId populated in merged batch responses even when callers omit it.
	if len(fields) > 0 {
		hasQuotaID := false
		for _, f := range fields {
			if f == "quotaId" {
				hasQuotaID = true
				break
			}
		}
		if !hasQuotaID {
			fields = append(fields, "quotaId")
		}
	}

	cvpParams := cvpBatch.NewV1betaBatchListQuotaRulesParamsWithContext(ctx)
	cvpParams.SetLocationID(params.LocationId)
	cvpParams.SetBody(&cvpmodels.QuotaRuleIDListV1beta{
		QuotaRuleUUIDs: quotaRuleUUIDs,
	})
	if len(fields) > 0 {
		cvpParams.SetFields(fields)
	}
	if params.XCorrelationID.IsSet() {
		correlationID := params.XCorrelationID.Value
		cvpParams.SetXCorrelationID(&correlationID)
	}

	resp, err := cvpClient.Batch.V1betaBatchListQuotaRules(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list quota rules failed: %w", err)
	}

	var result []gcpgenserver.BatchQuotaRuleV1beta
	if resp != nil && resp.Payload != nil {
		for _, r := range resp.Payload {
			if r != nil {
				converted := convertCVPQuotaRuleToV1beta(r)
				projected := projectBatchQuotaRuleForRequestedFields(&converted, fieldSet)
				result = append(result, projected)
			}
		}
	}

	return result, nil
}

func buildBatchQuotaRuleFieldSet(fields []gcpgenserver.V1betaBatchListQuotaRulesFieldsItem) map[string]bool {
	if len(fields) == 0 {
		return nil
	}
	set := make(map[string]bool, len(fields))
	for _, f := range fields {
		set[string(f)] = true
	}
	return set
}

func projectBatchQuotaRuleForRequestedFields(rule *gcpgenserver.QuotaRulesV1beta, fieldSet map[string]bool) gcpgenserver.BatchQuotaRuleV1beta {
	out := gcpgenserver.BatchQuotaRuleV1beta{}
	if rule == nil {
		return out
	}

	if rule.QuotaId.IsSet() {
		out.QuotaId = gcpgenserver.NewOptNilString(rule.QuotaId.Value)
	}

	if fieldSet == nil {
		applyBatchQuotaRuleFieldSelection(&out, fieldSet)
		return out
	}

	if rule.ResourceId != "" {
		out.ResourceId = gcpgenserver.NewOptNilString(rule.ResourceId)
	}
	if rule.QuotaType != "" {
		out.QuotaType = gcpgenserver.NewOptNilBatchQuotaRuleV1betaQuotaType(
			gcpgenserver.BatchQuotaRuleV1betaQuotaType(rule.QuotaType))
	}
	if rule.DiskLimitInMib != 0 {
		out.DiskLimitInMib = gcpgenserver.NewOptNilInt64(rule.DiskLimitInMib)
	}
	if rule.QuotaTarget.IsSet() && rule.QuotaTarget.Value != "" {
		out.QuotaTarget = gcpgenserver.NewOptNilString(rule.QuotaTarget.Value)
	}
	if rule.State.IsSet() {
		out.State = gcpgenserver.NewOptNilBatchQuotaRuleV1betaState(
			gcpgenserver.BatchQuotaRuleV1betaState(rule.State.Value))
	}
	if rule.StateDetails.IsSet() && rule.StateDetails.Value != "" {
		out.StateDetails = gcpgenserver.NewOptNilString(rule.StateDetails.Value)
	}
	if rule.Description.IsSet() && rule.Description.Value != "" {
		out.Description = gcpgenserver.NewOptNilString(rule.Description.Value)
	}
	if rule.CreatedAt.IsSet() {
		out.CreatedAt = gcpgenserver.NewOptNilDateTime(rule.CreatedAt.Value)
	}
	if rule.UpdatedAt.IsSet() {
		out.UpdatedAt = gcpgenserver.NewOptNilDateTime(rule.UpdatedAt.Value)
	}

	applyBatchQuotaRuleFieldSelection(&out, fieldSet)
	return out
}

func ensureRequestedQuotaRuleFieldsPresent(bq *gcpgenserver.BatchQuotaRuleV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		return
	}
	if fieldSet["resourceId"] && !bq.ResourceId.Set {
		bq.ResourceId.SetToNull()
	}
	if fieldSet["quotaType"] && !bq.QuotaType.Set {
		bq.QuotaType = gcpgenserver.NewOptNilBatchQuotaRuleV1betaQuotaType(
			gcpgenserver.BatchQuotaRuleV1betaQuotaTypeINDIVIDUALUSERQUOTA)
	}
	if fieldSet["diskLimitInMib"] && !bq.DiskLimitInMib.Set {
		bq.DiskLimitInMib.SetToNull()
	}
	if fieldSet["quotaTarget"] && !bq.QuotaTarget.Set {
		bq.QuotaTarget.SetToNull()
	}
	if fieldSet["state"] && !bq.State.Set {
		bq.State = gcpgenserver.NewOptNilBatchQuotaRuleV1betaState(
			gcpgenserver.BatchQuotaRuleV1betaStateSTATEUNSPECIFIED)
	}
	if fieldSet["stateDetails"] && !bq.StateDetails.Set {
		bq.StateDetails.SetToNull()
	}
	if fieldSet["description"] && !bq.Description.Set {
		bq.Description.SetToNull()
	}
	if fieldSet["createdAt"] && !bq.CreatedAt.Set {
		bq.CreatedAt.SetToNull()
	}
	if fieldSet["updatedAt"] && !bq.UpdatedAt.Set {
		bq.UpdatedAt.SetToNull()
	}
}

func applyBatchQuotaRuleFieldSelection(bq *gcpgenserver.BatchQuotaRuleV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		bq.ResourceId.Reset()
		bq.QuotaType.Reset()
		bq.DiskLimitInMib.Reset()
		bq.QuotaTarget.Reset()
		bq.State.Reset()
		bq.StateDetails.Reset()
		bq.Description.Reset()
		bq.CreatedAt.Reset()
		bq.UpdatedAt.Reset()
		return
	}

	if !fieldSet["resourceId"] {
		bq.ResourceId.Reset()
	}
	if !fieldSet["quotaType"] {
		bq.QuotaType.Reset()
	}
	if !fieldSet["diskLimitInMib"] {
		bq.DiskLimitInMib.Reset()
	}
	if !fieldSet["quotaTarget"] {
		bq.QuotaTarget.Reset()
	}
	if !fieldSet["state"] {
		bq.State.Reset()
	}
	if !fieldSet["stateDetails"] {
		bq.StateDetails.Reset()
	}
	if !fieldSet["description"] {
		bq.Description.Reset()
	}
	if !fieldSet["createdAt"] {
		bq.CreatedAt.Reset()
	}
	if !fieldSet["updatedAt"] {
		bq.UpdatedAt.Reset()
	}

	ensureRequestedQuotaRuleFieldsPresent(bq, fieldSet)
}

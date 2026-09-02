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
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var fetchBatchBackupPoliciesFromCVPFn = fetchBatchBackupPoliciesFromCVP

// cvpBatchBackupPolicyFieldWhitelist is the set of field names accepted by the checked-in CVP batch backup policy API
// (see clients/cvp/swagger-gcp.yaml). Extra proxy-only fields are applied from VCP DB results, not sent to CVP.
var cvpBatchBackupPolicyFieldWhitelist = map[string]struct{}{
	"resourceId":         {},
	"backupPolicyId":     {},
	"description":        {},
	"createdAt":          {},
	"enabled":            {},
	"volumeCount":        {},
	"dailyBackupLimit":   {},
	"weeklyBackupLimit":  {},
	"monthlyBackupLimit": {},
	"state":              {},
}

func (h Handler) V1betaBatchListBackupPolicies(ctx context.Context, req *gcpgenserver.BatchBackupPolicyUUIDListV1beta, params gcpgenserver.V1betaBatchListBackupPoliciesParams) (gcpgenserver.V1betaBatchListBackupPoliciesRes, error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaBatchListBackupPoliciesBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}

	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListBackupPoliciesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	if batchAuthFn(httpReq) != nil {
		return &gcpgenserver.V1betaBatchListBackupPoliciesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListBackupPoliciesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req == nil || len(req.BackupPolicyUUIDs) == 0 {
		return &gcpgenserver.V1betaBatchListBackupPoliciesBadRequest{
			Code:    http.StatusBadRequest,
			Message: "backupPolicyUUIDs is required and must have at least 1 item",
		}, nil
	}

	uuids := utils.DeduplicateSlice(req.BackupPolicyUUIDs)
	if len(uuids) > env.MaxBatchBackupPolicyUUIDs {
		return &gcpgenserver.V1betaBatchListBackupPoliciesBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("backupPolicyUUIDs in body should have at most %d items", env.MaxBatchBackupPolicyUUIDs),
		}, nil
	}

	// Reject malformed UUIDs up front so we don't hit the VCP DB or CVP for
	// inputs that can never match a real backup policy. Validation runs against the
	// original (pre-dedup) list so the offending index in the message matches
	// what the caller sent.
	if msg := validateUUIDList(req.BackupPolicyUUIDs, "backupPolicyUUIDs"); msg != "" {
		return &gcpgenserver.V1betaBatchListBackupPoliciesBadRequest{
			Code:    http.StatusBadRequest,
			Message: msg,
		}, nil
	}

	// When ?fields= is omitted, behave like CVP: minimal rows (backupPolicyId only) and do not send a field mask to CVP.
	fieldSet := buildBackupPolicyFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListBackupPoliciesVCPOnly(ctx, uuids, fieldSet)
	}
	return h.batchListBackupPoliciesParallel(ctx, uuids, params, fieldSet)
}

func (h Handler) batchListBackupPoliciesVCPOnly(
	ctx context.Context,
	backupPolicyUUIDs []string,
	fieldSet map[string]bool,
) (gcpgenserver.V1betaBatchListBackupPoliciesRes, error) {
	logger := util.GetLogger(ctx)

	volumeCounts, policies, err := h.Orchestrator.GetBackupPoliciesByUUIDs(ctx, backupPolicyUUIDs)
	if err != nil {
		logger.Error("Failed to list backup policies from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListBackupPoliciesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting backup policies",
		}, nil
	}

	out := make([]gcpgenserver.BatchBackupPolicyV1beta, 0, len(backupPolicyUUIDs))
	for _, id := range backupPolicyUUIDs {
		pol, ok := policies[id]
		if !ok || pol == nil {
			continue
		}
		vc := volumeCounts[id]
		out = append(out, convertBackupPolicyModelToBatchBackupPolicy(pol, vc, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListBackupPoliciesOK{BackupPolicies: out}, nil
}

func (h Handler) batchListBackupPoliciesParallel(
	ctx context.Context,
	backupPolicyUUIDs []string,
	params gcpgenserver.V1betaBatchListBackupPoliciesParams,
	fieldSet map[string]bool,
) (gcpgenserver.V1betaBatchListBackupPoliciesRes, error) {
	logger := util.GetLogger(ctx)

	// fieldSetForMerge always includes backupPolicyId so rows stay keyed for merge/indexing; the client's
	// field mask is applied after merge (see applyClientBatchBackupPolicyFieldMask).
	fieldSetForMerge := fieldSetWithBackupPolicyIDForMerge(fieldSet)

	var (
		vcpDBPolicies []gcpgenserver.BatchBackupPolicyV1beta
		vcpDBErr      error
		sdePolicies   []gcpgenserver.BatchBackupPolicyV1beta
		sdeErr        error
		wg            sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		volumeCounts, policies, err := h.Orchestrator.GetBackupPoliciesByUUIDs(ctx, backupPolicyUUIDs)
		if err != nil {
			vcpDBErr = err
			return
		}
		for _, id := range backupPolicyUUIDs {
			pol, ok := policies[id]
			if !ok || pol == nil {
				continue
			}
			vc := volumeCounts[id]
			vcpDBPolicies = append(vcpDBPolicies, convertBackupPolicyModelToBatchBackupPolicy(pol, vc, fieldSetForMerge))
		}
	}()

	go func() {
		defer wg.Done()
		sdePolicies, sdeErr = fetchBatchBackupPoliciesFromCVPFn(ctx, backupPolicyUUIDs, params, fieldSetForMerge)
	}()

	wg.Wait()

	if vcpDBErr != nil && sdeErr != nil {
		logger.Error("Both VCP and CVP batch backup policy queries failed",
			"vcpError", vcpDBErr.Error(), "cvpError", sdeErr.Error())
		return &gcpgenserver.V1betaBatchListBackupPoliciesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting backup policies from both systems",
		}, nil
	}

	if vcpDBErr != nil {
		logger.Warn("VCP batch backup policy query failed, returning SDE results only", "error", vcpDBErr.Error())
		applyClientBatchBackupPolicyFieldMask(sdePolicies, fieldSet)
		return &gcpgenserver.V1betaBatchListBackupPoliciesOK{BackupPolicies: sdePolicies}, nil
	}
	if sdeErr != nil {
		logger.Warn("CVP batch backup policy query failed, returning VCP results only", "error", sdeErr.Error())
		applyClientBatchBackupPolicyFieldMask(vcpDBPolicies, fieldSet)
		return &gcpgenserver.V1betaBatchListBackupPoliciesOK{BackupPolicies: vcpDBPolicies}, nil
	}

	merged := mergeBatchBackupPolicyParallelLists(backupPolicyUUIDs, vcpDBPolicies, sdePolicies, fieldSet)
	applyClientBatchBackupPolicyFieldMask(merged, fieldSet)
	return &gcpgenserver.V1betaBatchListBackupPoliciesOK{BackupPolicies: merged}, nil
}

// mergeBatchBackupPolicyParallelLists combines VCP (control-plane DB) and SDE (CVP API) batch results
// for the same requested UUIDs. When a policy exists in both systems, entries are merged into one row:
// VCP is authoritative for policy metadata (state, limits, etc.); volumeCount values are summed because
// they count disjoint volume sets per system.
func mergeBatchBackupPolicyParallelLists(
	orderedUUIDs []string,
	vcpPolicies []gcpgenserver.BatchBackupPolicyV1beta,
	sdePolicies []gcpgenserver.BatchBackupPolicyV1beta,
	fieldSet map[string]bool,
) []gcpgenserver.BatchBackupPolicyV1beta {
	vcpByID := indexBatchBackupPoliciesByUUID(vcpPolicies)
	sdeByID := indexBatchBackupPoliciesByUUID(sdePolicies)

	out := make([]gcpgenserver.BatchBackupPolicyV1beta, 0, len(orderedUUIDs))
	for _, id := range orderedUUIDs {
		vcpPol, vcpOk := vcpByID[id]
		sdePol, sdeOk := sdeByID[id]
		switch {
		case vcpOk && sdeOk:
			out = append(out, mergeBatchBackupPolicyVCPAndSDE(vcpPol, sdePol, fieldSet))
		case vcpOk:
			out = append(out, vcpPol)
		case sdeOk:
			out = append(out, sdePol)
		default:
			// Neither side returned this UUID (e.g. not found in either system).
		}
	}
	return out
}

func indexBatchBackupPoliciesByUUID(policies []gcpgenserver.BatchBackupPolicyV1beta) map[string]gcpgenserver.BatchBackupPolicyV1beta {
	m := make(map[string]gcpgenserver.BatchBackupPolicyV1beta, len(policies))
	for _, p := range policies {
		if !p.BackupPolicyId.Set || p.BackupPolicyId.Value == "" {
			continue
		}
		m[p.BackupPolicyId.Value] = p
	}
	return m
}

func mergeBatchBackupPolicyVCPAndSDE(
	vcp, sde gcpgenserver.BatchBackupPolicyV1beta,
	fieldSet map[string]bool,
) gcpgenserver.BatchBackupPolicyV1beta {
	out := vcp
	if fieldSet == nil {
		return out
	}

	// Volume usage is tracked per system; total is the sum when both report a count.
	if fieldSet["volumeCount"] {
		switch {
		case vcp.VolumeCount.Set && sde.VolumeCount.Set:
			out.VolumeCount = gcpgenserver.NewOptNilInt(vcp.VolumeCount.Value + sde.VolumeCount.Value)
		case !vcp.VolumeCount.Set && sde.VolumeCount.Set:
			out.VolumeCount = sde.VolumeCount
		}
	}

	// Prefer VCP fields when set; otherwise fill from SDE so merged rows stay complete.
	if fieldSet["resourceId"] && !vcp.ResourceId.Set && sde.ResourceId.Set {
		out.ResourceId = sde.ResourceId
	}
	if fieldSet["description"] && !vcp.Description.Set && sde.Description.Set {
		out.Description = sde.Description
	}
	if fieldSet["createdAt"] && !vcp.CreatedAt.Set && sde.CreatedAt.Set {
		out.CreatedAt = sde.CreatedAt
	}
	if fieldSet["enabled"] && !vcp.Enabled.Set && sde.Enabled.Set {
		out.Enabled = sde.Enabled
	}
	if fieldSet["dailyBackupLimit"] && !vcp.DailyBackupLimit.Set && sde.DailyBackupLimit.Set {
		out.DailyBackupLimit = sde.DailyBackupLimit
	}
	if fieldSet["weeklyBackupLimit"] && !vcp.WeeklyBackupLimit.Set && sde.WeeklyBackupLimit.Set {
		out.WeeklyBackupLimit = sde.WeeklyBackupLimit
	}
	if fieldSet["monthlyBackupLimit"] && !vcp.MonthlyBackupLimit.Set && sde.MonthlyBackupLimit.Set {
		out.MonthlyBackupLimit = sde.MonthlyBackupLimit
	}
	if fieldSet["state"] && !vcp.State.Set && sde.State.Set {
		out.State = sde.State
	}

	ensureRequestedFieldsPresentBatchBackupPolicy(&out, fieldSet)
	return out
}

func fetchBatchBackupPoliciesFromCVP(
	ctx context.Context,
	backupPolicyUUIDs []string,
	params gcpgenserver.V1betaBatchListBackupPoliciesParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	var fieldsForCVP []string
	for _, s := range params.Fields {
		name := string(s)
		if _, ok := cvpBatchBackupPolicyFieldWhitelist[name]; ok {
			fieldsForCVP = append(fieldsForCVP, name)
		}
	}

	cvpParams := cvpBatch.NewV1betaBatchListBackupPoliciesParamsWithContext(ctx)
	cvpParams.SetLocationID(params.LocationId)
	cvpParams.SetBody(&cvpmodels.BackupPolicyIDListV1beta{
		BackupPolicyUUIDs: backupPolicyUUIDs,
	})
	if len(fieldsForCVP) > 0 {
		cvpParams.SetFields(fieldsForCVP)
	}
	if params.XCorrelationID.IsSet() {
		correlationID := params.XCorrelationID.Value
		cvpParams.SetXCorrelationID(&correlationID)
	}

	resp, err := cvpClient.Batch.V1betaBatchListBackupPolicies(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list backup policies failed: %w", err)
	}

	var result []gcpgenserver.BatchBackupPolicyV1beta
	if resp != nil && resp.Payload != nil {
		for _, p := range resp.Payload.BackupPolicies {
			if p == nil {
				continue
			}
			bp := convertCVPBatchBackupPolicyToGCP(p)
			applyBatchBackupPolicyFieldSelection(&bp, fieldSet)
			result = append(result, bp)
		}
	}
	return result, nil
}

func buildBackupPolicyFieldSet(fields []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

// fieldSetWithBackupPolicyIDForMerge returns a copy of fieldSet with backupPolicyId forced true, for use
// only while building/indexing parallel VCP + SDE rows before the client's mask is applied.
func fieldSetWithBackupPolicyIDForMerge(fieldSet map[string]bool) map[string]bool {
	if fieldSet == nil {
		return nil
	}
	out := make(map[string]bool, len(fieldSet)+1)
	for k, v := range fieldSet {
		out[k] = v
	}
	out["backupPolicyId"] = true
	return out
}

func applyClientBatchBackupPolicyFieldMask(policies []gcpgenserver.BatchBackupPolicyV1beta, fieldSet map[string]bool) {
	for i := range policies {
		applyBatchBackupPolicyFieldSelection(&policies[i], fieldSet)
	}
}

// defaultBatchBackupPolicyFullFieldSet returns all batch API field names (for tests and callers that need explicit full projection).
func defaultBatchBackupPolicyFullFieldSet() map[string]bool {
	var zero gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem
	set := make(map[string]bool)
	for _, f := range zero.AllValues() {
		set[string(f)] = true
	}
	return set
}

func convertBackupPolicyModelToBatchBackupPolicy(bp *coremodels.BackupPolicy, volumeCount int64, fieldSet map[string]bool) gcpgenserver.BatchBackupPolicyV1beta {
	out := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(bp.BackupPolicyUUID),
	}
	if fieldSet == nil {
		return out
	}

	out.ResourceId = gcpgenserver.NewOptNilString(bp.ResourceID)
	if bp.Description != nil {
		out.Description = gcpgenserver.NewOptNilString(*bp.Description)
	}
	out.CreatedAt = gcpgenserver.NewOptNilDateTime(bp.CreatedAt)
	out.Enabled = gcpgenserver.NewOptNilBool(bp.Enabled)
	out.VolumeCount = gcpgenserver.NewOptNilInt(int(volumeCount))
	out.DailyBackupLimit = gcpgenserver.NewOptNilInt(int(bp.DailyBackupLimit))
	out.WeeklyBackupLimit = gcpgenserver.NewOptNilInt(int(bp.WeeklyBackupLimit))
	out.MonthlyBackupLimit = gcpgenserver.NewOptNilInt(int(bp.MonthlyBackupLimit))
	if bp.State != "" {
		out.State = gcpgenserver.NewOptNilBatchBackupPolicyV1betaState(gcpgenserver.BatchBackupPolicyV1betaState(bp.State))
	}

	applyBatchBackupPolicyFieldSelection(&out, fieldSet)
	return out
}

func convertCVPBatchBackupPolicyToGCP(p *cvpmodels.BatchBackupPolicyV1beta) gcpgenserver.BatchBackupPolicyV1beta {
	bp := gcpgenserver.BatchBackupPolicyV1beta{}
	if p.BackupPolicyID != "" {
		bp.BackupPolicyId = gcpgenserver.NewOptNilString(p.BackupPolicyID)
	}
	if p.ResourceID != nil {
		bp.ResourceId = gcpgenserver.NewOptNilString(*p.ResourceID)
	}
	if p.Description != nil {
		bp.Description = gcpgenserver.NewOptNilString(*p.Description)
	}
	if p.CreatedAt != nil {
		bp.CreatedAt = gcpgenserver.NewOptNilDateTime(time.Time(*p.CreatedAt))
	}
	if p.Enabled != nil {
		bp.Enabled = gcpgenserver.NewOptNilBool(*p.Enabled)
	}
	if p.VolumeCount != nil {
		bp.VolumeCount = gcpgenserver.NewOptNilInt(int(*p.VolumeCount))
	}
	if p.DailyBackupLimit != nil {
		bp.DailyBackupLimit = gcpgenserver.NewOptNilInt(int(*p.DailyBackupLimit))
	}
	if p.WeeklyBackupLimit != nil {
		bp.WeeklyBackupLimit = gcpgenserver.NewOptNilInt(int(*p.WeeklyBackupLimit))
	}
	if p.MonthlyBackupLimit != nil {
		bp.MonthlyBackupLimit = gcpgenserver.NewOptNilInt(int(*p.MonthlyBackupLimit))
	}
	if p.State != nil && *p.State != "" {
		bp.State = gcpgenserver.NewOptNilBatchBackupPolicyV1betaState(
			gcpgenserver.BatchBackupPolicyV1betaState(*p.State))
	}
	return bp
}

func ensureRequestedFieldsPresentBatchBackupPolicy(bp *gcpgenserver.BatchBackupPolicyV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		return
	}
	if fieldSet["backupPolicyId"] && !bp.BackupPolicyId.Set {
		bp.BackupPolicyId.SetToNull()
	}
	if fieldSet["resourceId"] && !bp.ResourceId.Set {
		bp.ResourceId.SetToNull()
	}
	if fieldSet["description"] && !bp.Description.Set {
		bp.Description.SetToNull()
	}
	if fieldSet["createdAt"] && !bp.CreatedAt.Set {
		bp.CreatedAt.SetToNull()
	}
	if fieldSet["enabled"] && !bp.Enabled.Set {
		bp.Enabled.SetToNull()
	}
	if fieldSet["volumeCount"] && !bp.VolumeCount.Set {
		bp.VolumeCount.SetToNull()
	}
	if fieldSet["dailyBackupLimit"] && !bp.DailyBackupLimit.Set {
		bp.DailyBackupLimit.SetToNull()
	}
	if fieldSet["weeklyBackupLimit"] && !bp.WeeklyBackupLimit.Set {
		bp.WeeklyBackupLimit.SetToNull()
	}
	if fieldSet["monthlyBackupLimit"] && !bp.MonthlyBackupLimit.Set {
		bp.MonthlyBackupLimit.SetToNull()
	}
	if fieldSet["state"] && !bp.State.Set {
		bp.State = gcpgenserver.NewOptNilBatchBackupPolicyV1betaState(
			gcpgenserver.BatchBackupPolicyV1betaStateSTATEUNSPECIFIED)
	}
}

func applyBatchBackupPolicyFieldSelection(bp *gcpgenserver.BatchBackupPolicyV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		bp.ResourceId.Reset()
		bp.Description.Reset()
		bp.CreatedAt.Reset()
		bp.Enabled.Reset()
		bp.VolumeCount.Reset()
		bp.DailyBackupLimit.Reset()
		bp.WeeklyBackupLimit.Reset()
		bp.MonthlyBackupLimit.Reset()
		bp.State.Reset()
		return
	}
	if !fieldSet["backupPolicyId"] {
		bp.BackupPolicyId.Reset()
	}
	if !fieldSet["resourceId"] {
		bp.ResourceId.Reset()
	}
	if !fieldSet["description"] {
		bp.Description.Reset()
	}
	if !fieldSet["createdAt"] {
		bp.CreatedAt.Reset()
	}
	if !fieldSet["enabled"] {
		bp.Enabled.Reset()
	}
	if !fieldSet["volumeCount"] {
		bp.VolumeCount.Reset()
	}
	if !fieldSet["dailyBackupLimit"] {
		bp.DailyBackupLimit.Reset()
	}
	if !fieldSet["weeklyBackupLimit"] {
		bp.WeeklyBackupLimit.Reset()
	}
	if !fieldSet["monthlyBackupLimit"] {
		bp.MonthlyBackupLimit.Reset()
	}
	if !fieldSet["state"] {
		bp.State.Reset()
	}
	ensureRequestedFieldsPresentBatchBackupPolicy(bp, fieldSet)
}

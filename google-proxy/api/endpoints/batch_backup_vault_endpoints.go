package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var fetchBatchBackupVaultsFromCVPFn = fetchBatchBackupVaultsFromCVP

func (h Handler) V1betaBatchListBackupVaults(ctx context.Context, req *gcpgenserver.BatchBackupVaultUUIDListV1beta, params gcpgenserver.V1betaBatchListBackupVaultsParams) (gcpgenserver.V1betaBatchListBackupVaultsRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListBackupVaultsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListBackupVaultsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListBackupVaultsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req == nil || len(req.BackupVaultUuids) == 0 {
		return &gcpgenserver.V1betaBatchListBackupVaultsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "backupVaultUuids is required and must have at least 1 item",
		}, nil
	}

	uuids := utils.DeduplicateSlice(req.BackupVaultUuids)
	if len(uuids) > env.MaxBatchBackupVaultUUIDs {
		return &gcpgenserver.V1betaBatchListBackupVaultsBadRequest{
			Code: http.StatusBadRequest,
			Message: fmt.Sprintf("backupVaultUuids in body should have at most %d items",
				env.MaxBatchBackupVaultUUIDs),
		}, nil
	}

	if msg := validateUUIDList(req.BackupVaultUuids, "backupVaultUuids"); msg != "" {
		return &gcpgenserver.V1betaBatchListBackupVaultsBadRequest{
			Code:    http.StatusBadRequest,
			Message: msg,
		}, nil
	}

	fieldSet := buildBVFieldSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListBackupVaultsVCPOnly(ctx, uuids, fieldSet)
	}

	return h.batchListBackupVaultsParallel(ctx, uuids, params, fieldSet)
}

func (h Handler) batchListBackupVaultsVCPOnly(ctx context.Context, uuids []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListBackupVaultsRes, error) {
	logger := util.GetLogger(ctx)

	vaults, err := h.Orchestrator.GetMultipleBackupVaults(ctx, uuids)
	if err != nil {
		logger.Error("Failed to get backup vaults by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListBackupVaultsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting backup vaults",
		}, nil
	}

	result := make([]gcpgenserver.BatchBackupVaultV1beta, 0, len(vaults))
	for _, bv := range vaults {
		if bv == nil || bv.DeletedAt != nil {
			continue
		}
		result = append(result, convertBackupVaultToBatchBackupVault(bv, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListBackupVaultsOK{BackupVaults: result}, nil
}

func (h Handler) batchListBackupVaultsParallel(ctx context.Context, uuids []string, params gcpgenserver.V1betaBatchListBackupVaultsParams, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListBackupVaultsRes, error) {
	logger := util.GetLogger(ctx)

	var (
		vcpVaults []*models.BackupVaultV1beta
		vcpErr    error
		cvpVaults []gcpgenserver.BatchBackupVaultV1beta
		cvpErr    error
		wg        sync.WaitGroup
	)

	wg.Add(2)

	go func() {
		defer wg.Done()
		vcpVaults, vcpErr = h.Orchestrator.GetMultipleBackupVaults(ctx, uuids)
	}()

	go func() {
		defer wg.Done()
		cvpVaults, cvpErr = fetchBatchBackupVaultsFromCVPFn(ctx, uuids, params, fieldSet)
	}()

	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch backup vault queries failed",
			"vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListBackupVaultsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting backup vaults from both systems",
		}, nil
	}

	var vcpBatchVaults []gcpgenserver.BatchBackupVaultV1beta
	vcpIDs := make(map[string]struct{}, len(vcpVaults))
	if vcpErr != nil {
		logger.Warn("VCP batch backup vault query failed, returning SDE results only", "error", vcpErr.Error())
	} else {
		vcpBatchVaults = make([]gcpgenserver.BatchBackupVaultV1beta, 0, len(vcpVaults))
		for _, bv := range vcpVaults {
			if bv == nil || bv.DeletedAt != nil {
				continue
			}
			vcpIDs[bv.BackupVaultID] = struct{}{}
			vcpBatchVaults = append(vcpBatchVaults, convertBackupVaultToBatchBackupVault(bv, fieldSet))
		}
	}

	if cvpErr != nil {
		logger.Warn("CVP batch backup vault query failed, returning VCP results only", "error", cvpErr.Error())
	}

	allVaults := make([]gcpgenserver.BatchBackupVaultV1beta, 0, len(vcpBatchVaults)+len(cvpVaults))
	allVaults = append(allVaults, vcpBatchVaults...)
	for _, bv := range cvpVaults {
		if bv.BackupVaultId.Set {
			if _, exists := vcpIDs[bv.BackupVaultId.Value]; exists {
				continue
			}
		}
		allVaults = append(allVaults, bv)
	}
	return &gcpgenserver.V1betaBatchListBackupVaultsOK{BackupVaults: allVaults}, nil
}

func fetchBatchBackupVaultsFromCVP(
	ctx context.Context,
	uuids []string,
	params gcpgenserver.V1betaBatchListBackupVaultsParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchBackupVaultV1beta, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)

	cvpClient := createClient(logger, jwtToken)

	cvpParams := cvpBatch.NewV1betaBatchListBackupVaultsParamsWithContext(ctx)
	cvpParams.SetBody(&cvpmodels.BackupVaultUUIDListV1beta{
		BackupVaultUUIDs: uuids,
	})
	applyBatchCvpListCommonParams(cvpParams, params.LocationId, batchListFieldStrings(params.Fields), params.XCorrelationID)

	resp, err := cvpClient.Batch.V1betaBatchListBackupVaults(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list backup vaults failed: %w", err)
	}

	var result []gcpgenserver.BatchBackupVaultV1beta
	if resp != nil && resp.Payload != nil {
		result = make([]gcpgenserver.BatchBackupVaultV1beta, 0, len(resp.Payload.BackupVaults))
		for _, cvpBV := range resp.Payload.BackupVaults {
			if cvpBV == nil || cvpBV.BackupVaultID == "" {
				continue
			}
			bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(cvpBV)
			applyBatchBVFieldSelection(&bp, fieldSet)
			result = append(result, bp)
		}
	}

	return result, nil
}

func buildBVFieldSet(fields []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

// --- VCP model -> ogen response conversion ---

func convertBackupVaultToBatchBackupVault(bv *models.BackupVaultV1beta, fieldSet map[string]bool) gcpgenserver.BatchBackupVaultV1beta {
	bp := gcpgenserver.BatchBackupVaultV1beta{
		BackupVaultId: gcpgenserver.NewOptString(bv.BackupVaultID),
	}

	if fieldSet == nil {
		return bp
	}

	if bv.Name != "" {
		bp.ResourceId = gcpgenserver.NewOptNilString(bv.Name)
	}
	if bv.Description != nil {
		bp.Description = gcpgenserver.NewOptNilString(*bv.Description)
	}
	if !bv.CreatedAt.IsZero() {
		bp.CreatedAt = gcpgenserver.NewOptNilDateTime(bv.CreatedAt.UTC())
	}
	if bv.LifeCycleState != "" {
		bp.State = gcpgenserver.NewOptNilBatchBackupVaultV1betaState(
			normalizeBatchBVState(bv.LifeCycleState))
	}
	if bv.LifeCycleStateDetails != "" {
		bp.StateDetails = gcpgenserver.NewOptNilString(bv.LifeCycleStateDetails)
	}
	if bv.BackupVaultType != nil && *bv.BackupVaultType != "" {
		bp.BackupVaultType = gcpgenserver.NewOptNilBatchBackupVaultV1betaBackupVaultType(
			gcpgenserver.BatchBackupVaultV1betaBackupVaultType(*bv.BackupVaultType))
		if *bv.BackupVaultType == "CROSS_REGION" {
			if bv.SourceRegion != nil {
				bp.SourceRegion = gcpgenserver.NewOptNilString(*bv.SourceRegion)
			}
			if bv.BackupRegion != nil {
				bp.BackupRegion = gcpgenserver.NewOptNilString(*bv.BackupRegion)
			}
			if bv.SourceRegion != nil && bv.BackupRegion != nil && bv.AccountName != "" &&
				bv.CrossRegionBackupVaultName != nil {
				peerRegion := extractRegionFromResourcePath(*bv.CrossRegionBackupVaultName)
				switch peerRegion {
				case *bv.BackupRegion:
					bp.SourceBackupVault = gcpgenserver.NewOptNilString(
						fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", bv.AccountName, *bv.SourceRegion, bv.Name))
					bp.DestinationBackupVault = gcpgenserver.NewOptNilString(*bv.CrossRegionBackupVaultName)
				case *bv.SourceRegion:
					bp.SourceBackupVault = gcpgenserver.NewOptNilString(*bv.CrossRegionBackupVaultName)
					bp.DestinationBackupVault = gcpgenserver.NewOptNilString(
						fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", bv.AccountName, *bv.BackupRegion, bv.Name))
				}
			}
		}
	}
	if bv.KmsConfigResourcePath != nil {
		bp.KmsConfigResourcePath = gcpgenserver.NewOptNilString(*bv.KmsConfigResourcePath)
	}
	if bv.BackupsPrimaryKeyVersion != nil {
		bp.BackupsPrimaryKeyVersion = gcpgenserver.NewOptNilString(*bv.BackupsPrimaryKeyVersion)
	}
	bp.EncryptionState = gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(
		gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED)
	if bv.EncryptionState != nil && *bv.EncryptionState != "" &&
		*bv.EncryptionState != string(gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED) {
		bp.EncryptionState = gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(
			gcpgenserver.BatchBackupVaultV1betaEncryptionState(*bv.EncryptionState))
	}

	if bv.ServiceType == datamodel.ServiceTypeCrossProject {
		bp.CrossProjectVault = gcpgenserver.NewOptNilBool(true)
		if bv.TenantProject != nil && *bv.TenantProject != "" {
			bp.TenantProject = gcpgenserver.NewOptNilString(*bv.TenantProject)
		} else {
			bp.TenantProject.SetToNull()
		}
	} else {
		bp.CrossProjectVault.SetToNull()
		bp.TenantProject.SetToNull()
	}

	retPolicy := gcpgenserver.BackupRetentionPolicyV1beta{}
	if bv.BackupRetentionPolicy.IsDailyBackupImmutable {
		retPolicy.DailyBackupImmutable = gcpgenserver.NewOptBool(true)
	}
	if bv.BackupRetentionPolicy.IsWeeklyBackupImmutable {
		retPolicy.WeeklyBackupImmutable = gcpgenserver.NewOptBool(true)
	}
	if bv.BackupRetentionPolicy.IsMonthlyBackupImmutable {
		retPolicy.MonthlyBackupImmutable = gcpgenserver.NewOptBool(true)
	}
	if bv.BackupRetentionPolicy.IsAdhocBackupImmutable {
		retPolicy.ManualBackupImmutable = gcpgenserver.NewOptBool(true)
	}
	retentionDays := 0
	if bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil {
		retentionDays = int(*bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
	}
	retPolicy.BackupMinimumEnforcedRetentionDays = gcpgenserver.NewOptInt(retentionDays)
	bp.BackupRetentionPolicy = gcpgenserver.NewOptBackupRetentionPolicyV1beta(retPolicy)

	applyBatchBVFieldSelection(&bp, fieldSet)
	return bp
}

// --- CVP model -> ogen response conversion ---

func convertCVPBatchBackupVaultToGCPBatchBackupVault(p *cvpmodels.BatchBackupVaultV1beta) gcpgenserver.BatchBackupVaultV1beta {
	bp := gcpgenserver.BatchBackupVaultV1beta{}

	if p.BackupVaultID != "" {
		bp.BackupVaultId = gcpgenserver.NewOptString(p.BackupVaultID)
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
	if p.State != nil {
		bp.State = gcpgenserver.NewOptNilBatchBackupVaultV1betaState(
			gcpgenserver.BatchBackupVaultV1betaState(*p.State))
	}
	if p.StateDetails != nil {
		bp.StateDetails = gcpgenserver.NewOptNilString(*p.StateDetails)
	}
	if p.BackupVaultType != nil {
		bp.BackupVaultType = gcpgenserver.NewOptNilBatchBackupVaultV1betaBackupVaultType(
			gcpgenserver.BatchBackupVaultV1betaBackupVaultType(*p.BackupVaultType))
	}
	if p.SourceRegion != nil {
		bp.SourceRegion = gcpgenserver.NewOptNilString(*p.SourceRegion)
	}
	if p.BackupRegion != nil {
		bp.BackupRegion = gcpgenserver.NewOptNilString(*p.BackupRegion)
	}
	if p.SourceBackupVault != nil {
		bp.SourceBackupVault = gcpgenserver.NewOptNilString(*p.SourceBackupVault)
	}
	if p.DestinationBackupVault != nil {
		bp.DestinationBackupVault = gcpgenserver.NewOptNilString(*p.DestinationBackupVault)
	}
	if p.KmsConfigResourcePath != nil {
		bp.KmsConfigResourcePath = gcpgenserver.NewOptNilString(*p.KmsConfigResourcePath)
	}
	if p.BackupsPrimaryKeyVersion != nil {
		bp.BackupsPrimaryKeyVersion = gcpgenserver.NewOptNilString(*p.BackupsPrimaryKeyVersion)
	}
	bp.EncryptionState = gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(
		gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED)
	if p.EncryptionState != nil && *p.EncryptionState != "" &&
		*p.EncryptionState != string(gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED) {
		bp.EncryptionState = gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(
			gcpgenserver.BatchBackupVaultV1betaEncryptionState(*p.EncryptionState))
	}
	if p.CrossProjectVault != nil {
		bp.CrossProjectVault = gcpgenserver.NewOptNilBool(*p.CrossProjectVault)
	} else {
		bp.CrossProjectVault.SetToNull()
	}
	if p.CrossProjectVault != nil && *p.CrossProjectVault && p.TenantProject != nil && *p.TenantProject != "" {
		bp.TenantProject = gcpgenserver.NewOptNilString(*p.TenantProject)
	} else {
		bp.TenantProject.SetToNull()
	}
	if p.BackupRetentionPolicy != nil {
		retPolicy := gcpgenserver.BackupRetentionPolicyV1beta{}
		if p.BackupRetentionPolicy.DailyBackupImmutable {
			retPolicy.DailyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if p.BackupRetentionPolicy.WeeklyBackupImmutable {
			retPolicy.WeeklyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if p.BackupRetentionPolicy.MonthlyBackupImmutable {
			retPolicy.MonthlyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if p.BackupRetentionPolicy.ManualBackupImmutable {
			retPolicy.ManualBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		retentionDays := 0
		if p.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays != nil {
			retentionDays = int(*p.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays)
		}
		retPolicy.BackupMinimumEnforcedRetentionDays = gcpgenserver.NewOptInt(retentionDays)
		bp.BackupRetentionPolicy = gcpgenserver.NewOptBackupRetentionPolicyV1beta(retPolicy)
	}

	return bp
}

// extractRegionFromResourcePath extracts the location from a resource path
// of the form "projects/{project}/locations/{location}/backupVaults/{name}".
func extractRegionFromResourcePath(resourcePath string) string {
	parts := strings.SplitN(resourcePath, "/", 5)
	if len(parts) >= 4 {
		return parts[3]
	}
	return ""
}

func normalizeBatchBVState(state string) gcpgenserver.BatchBackupVaultV1betaState {
	if state == "" {
		return gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED
	}
	st := gcpgenserver.BatchBackupVaultV1betaState(state)
	switch st {
	case gcpgenserver.BatchBackupVaultV1betaStateCREATING,
		gcpgenserver.BatchBackupVaultV1betaStateUPDATING,
		gcpgenserver.BatchBackupVaultV1betaStateDELETING,
		gcpgenserver.BatchBackupVaultV1betaStateREADY,
		gcpgenserver.BatchBackupVaultV1betaStateDELETED,
		gcpgenserver.BatchBackupVaultV1betaStateERROR,
		gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED:
		return st
	default:
		return gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED
	}
}

func ensureRequestedBVFieldsPresent(bp *gcpgenserver.BatchBackupVaultV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		return
	}
	if fieldSet["backupVaultId"] && !bp.BackupVaultId.Set {
		bp.BackupVaultId = gcpgenserver.NewOptString("")
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
	if fieldSet["state"] && !bp.State.Set {
		bp.State = gcpgenserver.NewOptNilBatchBackupVaultV1betaState(
			gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED)
	}
	if fieldSet["stateDetails"] && !bp.StateDetails.Set {
		bp.StateDetails.SetToNull()
	}
	if fieldSet["backupVaultType"] && !bp.BackupVaultType.Set {
		bp.BackupVaultType = gcpgenserver.NewOptNilBatchBackupVaultV1betaBackupVaultType(
			gcpgenserver.BatchBackupVaultV1betaBackupVaultTypeTYPEUNSPECIFIED)
	}
	if fieldSet["sourceRegion"] && !bp.SourceRegion.Set {
		bp.SourceRegion.SetToNull()
	}
	if fieldSet["backupRegion"] && !bp.BackupRegion.Set {
		bp.BackupRegion.SetToNull()
	}
	if fieldSet["sourceBackupVault"] && !bp.SourceBackupVault.Set {
		bp.SourceBackupVault.SetToNull()
	}
	if fieldSet["destinationBackupVault"] && !bp.DestinationBackupVault.Set {
		bp.DestinationBackupVault.SetToNull()
	}
	if fieldSet["kmsConfigResourcePath"] && !bp.KmsConfigResourcePath.Set {
		bp.KmsConfigResourcePath.SetToNull()
	}
	if fieldSet["backupsPrimaryKeyVersion"] && !bp.BackupsPrimaryKeyVersion.Set {
		bp.BackupsPrimaryKeyVersion.SetToNull()
	}
	if fieldSet["encryptionState"] && !bp.EncryptionState.Set {
		bp.EncryptionState = gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(
			gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED)
	}
	if fieldSet["backupRetentionPolicy"] && !bp.BackupRetentionPolicy.Set {
		bp.BackupRetentionPolicy = gcpgenserver.NewOptBackupRetentionPolicyV1beta(
			gcpgenserver.BackupRetentionPolicyV1beta{})
	}
	if fieldSet["crossProjectVault"] && !bp.CrossProjectVault.Set {
		bp.CrossProjectVault.SetToNull()
	}
	if fieldSet["tenantProject"] && !bp.TenantProject.Set {
		bp.TenantProject.SetToNull()
	}
}

func applyBatchBVFieldSelection(bp *gcpgenserver.BatchBackupVaultV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		bp.ResourceId.Reset()
		bp.Description.Reset()
		bp.CreatedAt.Reset()
		bp.State.Reset()
		bp.StateDetails.Reset()
		bp.BackupVaultType.Reset()
		bp.SourceRegion.Reset()
		bp.BackupRegion.Reset()
		bp.SourceBackupVault.Reset()
		bp.DestinationBackupVault.Reset()
		bp.KmsConfigResourcePath.Reset()
		bp.BackupsPrimaryKeyVersion.Reset()
		bp.EncryptionState.Reset()
		bp.BackupRetentionPolicy.Reset()
		bp.CrossProjectVault.Reset()
		bp.TenantProject.Reset()
		return
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
	if !fieldSet["state"] {
		bp.State.Reset()
	}
	if !fieldSet["stateDetails"] {
		bp.StateDetails.Reset()
	}
	if !fieldSet["backupVaultType"] {
		bp.BackupVaultType.Reset()
	}
	if !fieldSet["sourceRegion"] {
		bp.SourceRegion.Reset()
	}
	if !fieldSet["backupRegion"] {
		bp.BackupRegion.Reset()
	}
	if !fieldSet["sourceBackupVault"] {
		bp.SourceBackupVault.Reset()
	}
	if !fieldSet["destinationBackupVault"] {
		bp.DestinationBackupVault.Reset()
	}
	if !fieldSet["kmsConfigResourcePath"] {
		bp.KmsConfigResourcePath.Reset()
	}
	if !fieldSet["backupsPrimaryKeyVersion"] {
		bp.BackupsPrimaryKeyVersion.Reset()
	}
	if !fieldSet["encryptionState"] {
		bp.EncryptionState.Reset()
	}
	if !fieldSet["backupRetentionPolicy"] {
		bp.BackupRetentionPolicy.Reset()
	}
	if !fieldSet["crossProjectVault"] {
		bp.CrossProjectVault.Reset()
	}
	if !fieldSet["tenantProject"] {
		bp.TenantProject.Reset()
	}

	ensureRequestedBVFieldsPresent(bp, fieldSet)
}

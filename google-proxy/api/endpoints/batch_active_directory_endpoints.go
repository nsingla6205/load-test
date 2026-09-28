package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

func (h Handler) V1betaBatchListActiveDirectories(ctx context.Context, req *gcpgenserver.BatchActiveDirectoryUUIDListV1beta, params gcpgenserver.V1betaBatchListActiveDirectoriesParams) (gcpgenserver.V1betaBatchListActiveDirectoriesRes, error) {
	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListActiveDirectoriesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	responder := batchAuthFn(httpReq)
	if responder != nil {
		return &gcpgenserver.V1betaBatchListActiveDirectoriesUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if len(req.ActiveDirectoryUuids) == 0 {
		return &gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest{
			Code:    http.StatusBadRequest,
			Message: "activeDirectoryUuids is required and must have at least 1 item",
		}, nil
	}

	if len(req.ActiveDirectoryUuids) > env.MaxBatchActiveDirectoryUUIDs {
		return &gcpgenserver.V1betaBatchListActiveDirectoriesBadRequest{
			Code: http.StatusBadRequest,
			Message: fmt.Sprintf("activeDirectoryUuids in body should have at most %d items",
				env.MaxBatchActiveDirectoryUUIDs),
		}, nil
	}

	logger := util.GetLogger(ctx)
	fieldSet := buildADBatchFieldSet(params.Fields)

	var fieldsForCVP []string
	if len(params.Fields) > 0 {
		fieldsForCVP = make([]string, len(params.Fields))
		for i, f := range params.Fields {
			fieldsForCVP[i] = string(f)
		}
	}

	batchParams := &commonparams.BatchListADsParams{
		UUIDs:      req.ActiveDirectoryUuids,
		LocationID: params.LocationId,
		Fields:     fieldsForCVP,
	}
	if params.XCorrelationID.IsSet() {
		batchParams.CorrelationID = params.XCorrelationID.Value
	}

	ads, err := h.Orchestrator.BatchListActiveDirectories(ctx, batchParams)
	if err != nil {
		logger.Error("BatchListActiveDirectories failed", "error", err.Error())
		return &gcpgenserver.V1betaBatchListActiveDirectoriesInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting active directories",
		}, nil
	}

	out := make([]gcpgenserver.BatchActiveDirectoryV1beta, 0, len(ads))
	for _, ad := range ads {
		if ad == nil || ad.DeletedAt != nil {
			continue
		}
		out = append(out, convertADToBatchAD(ad, fieldSet))
	}

	return &gcpgenserver.V1betaBatchListActiveDirectoriesOK{ActiveDirectories: out}, nil
}

func buildADBatchFieldSet(fields []gcpgenserver.V1betaBatchListActiveDirectoriesFieldsItem) map[string]bool {
	return utils.BuildFieldSet(fields)
}

func isValidBatchADState(s gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryState) bool {
	switch s {
	case gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateCREATING,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateREADY,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateUPDATING,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateINUSE,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateDELETING,
		gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateERROR:
		return true
	default:
		return false
	}
}

func normalizeBatchADStateString(state string) gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryState {
	if state == "" {
		return gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED
	}
	st := gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryState(state)
	if isValidBatchADState(st) {
		return st
	}
	return gcpgenserver.BatchActiveDirectoryV1betaActiveDirectoryStateSTATEUNSPECIFIED
}

// convertADToBatchAD converts a VCP Active Directory model to BatchActiveDirectoryV1beta.
// When fieldSet is nil (no fields requested), only activeDirectoryId is returned.
// When fieldSet is provided, only requested fields are populated; missing scalar values are null after selection.
// Empty activeDirectoryState in storage maps to STATE_UNSPECIFIED (never JSON null when the field is requested).
// Empty string arrays map to JSON null, not [].
func convertADToBatchAD(ad *models.ActiveDirectory, fieldSet map[string]bool) gcpgenserver.BatchActiveDirectoryV1beta {
	ba := gcpgenserver.BatchActiveDirectoryV1beta{
		ActiveDirectoryId: gcpgenserver.NewOptNilString(ad.UUID),
	}

	if fieldSet == nil {
		applyBatchADFieldSelection(&ba, fieldSet)
		return ba
	}

	attrs := ad.ActiveDirectoryAttributes

	if ad.AdName != "" {
		ba.ResourceId = gcpgenserver.NewOptNilString(ad.AdName)
	}
	if ad.Username != "" {
		ba.Username = gcpgenserver.NewOptNilString(ad.Username)
	}
	ba.Password = gcpgenserver.NewOptNilString(PasswordMask)
	if ad.Domain != "" {
		ba.Domain = gcpgenserver.NewOptNilString(ad.Domain)
	}
	if ad.DNS != "" {
		ba.DNS = gcpgenserver.NewOptNilString(ad.DNS)
	}
	if ad.NetBIOS != "" {
		ba.NetBIOS = gcpgenserver.NewOptNilString(ad.NetBIOS)
	}
	if attrs != nil {
		if attrs.OrganizationalUnit != "" {
			ba.OrganizationalUnit = gcpgenserver.NewOptNilString(attrs.OrganizationalUnit)
		}
		if attrs.Site != "" {
			ba.Site = gcpgenserver.NewOptNilString(attrs.Site)
		}
		if attrs.KdcIP != "" {
			ba.KdcIP = gcpgenserver.NewOptNilString(attrs.KdcIP)
		}
		if attrs.KdcHostname != "" {
			ba.KdcHostname = gcpgenserver.NewOptNilString(attrs.KdcHostname)
		}
		ba.EncryptDCConnections = gcpgenserver.NewOptNilBool(attrs.EncryptDCConnections)
		ba.AesEncryption = gcpgenserver.NewOptNilBool(attrs.AesEncryption)
		ba.LdapSigning = gcpgenserver.NewOptNilBool(attrs.LdapSigning)
		ba.AllowLocalNFSUsersWithLdap = gcpgenserver.NewOptNilBool(attrs.AllowLocalNFSUsersWithLdap)
		if attrs.Description != "" {
			ba.Description = gcpgenserver.NewOptNilString(attrs.Description)
		}
		if len(attrs.BackupOperators) > 0 {
			ba.BackupOperators = gcpgenserver.NewOptNilStringArray(attrs.BackupOperators)
		} else {
			ba.BackupOperators.SetToNull()
		}
		if len(attrs.SecurityOperators) > 0 {
			ba.SecurityOperators = gcpgenserver.NewOptNilStringArray(attrs.SecurityOperators)
		} else {
			ba.SecurityOperators.SetToNull()
		}
		if len(attrs.Administrators) > 0 {
			ba.Administrators = gcpgenserver.NewOptNilStringArray(attrs.Administrators)
		} else {
			ba.Administrators.SetToNull()
		}
	}
	ba.ActiveDirectoryState = gcpgenserver.NewOptNilBatchActiveDirectoryV1betaActiveDirectoryState(normalizeBatchADStateString(ad.State))
	ba.ActiveDirectoryStateDetails = gcpgenserver.NewOptNilString(ad.StateDetails)
	if !ad.CreatedAt.IsZero() {
		ba.CreatedAt = gcpgenserver.NewOptNilDateTime(ad.CreatedAt)
	}

	applyBatchADFieldSelection(&ba, fieldSet)
	return ba
}

func ensureRequestedADFieldsPresent(ba *gcpgenserver.BatchActiveDirectoryV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		return
	}
	if fieldSet["resourceId"] && !ba.ResourceId.Set {
		ba.ResourceId.SetToNull()
	}
	if fieldSet["username"] && !ba.Username.Set {
		ba.Username.SetToNull()
	}
	if fieldSet["password"] && !ba.Password.Set {
		ba.Password.SetToNull()
	}
	if fieldSet["domain"] && !ba.Domain.Set {
		ba.Domain.SetToNull()
	}
	if fieldSet["DNS"] && !ba.DNS.Set {
		ba.DNS.SetToNull()
	}
	if fieldSet["netBIOS"] && !ba.NetBIOS.Set {
		ba.NetBIOS.SetToNull()
	}
	if fieldSet["organizationalUnit"] && !ba.OrganizationalUnit.Set {
		ba.OrganizationalUnit.SetToNull()
	}
	if fieldSet["site"] && !ba.Site.Set {
		ba.Site.SetToNull()
	}
	if fieldSet["kdcIP"] && !ba.KdcIP.Set {
		ba.KdcIP.SetToNull()
	}
	if fieldSet["kdcHostname"] && !ba.KdcHostname.Set {
		ba.KdcHostname.SetToNull()
	}
	if fieldSet["activeDirectoryState"] && !ba.ActiveDirectoryState.Set {
		ba.ActiveDirectoryState.SetToNull()
	}
	if fieldSet["activeDirectoryStateDetails"] && !ba.ActiveDirectoryStateDetails.Set {
		ba.ActiveDirectoryStateDetails.SetToNull()
	}
	if fieldSet["createdAt"] && !ba.CreatedAt.Set {
		ba.CreatedAt.SetToNull()
	}
	if fieldSet["encryptDCConnections"] && !ba.EncryptDCConnections.Set {
		ba.EncryptDCConnections.SetToNull()
	}
	if fieldSet["backupOperators"] && !ba.BackupOperators.Set {
		ba.BackupOperators.SetToNull()
	}
	if fieldSet["aesEncryption"] && !ba.AesEncryption.Set {
		ba.AesEncryption.SetToNull()
	}
	if fieldSet["ldapSigning"] && !ba.LdapSigning.Set {
		ba.LdapSigning.SetToNull()
	}
	if fieldSet["securityOperators"] && !ba.SecurityOperators.Set {
		ba.SecurityOperators.SetToNull()
	}
	if fieldSet["allowLocalNFSUsersWithLdap"] && !ba.AllowLocalNFSUsersWithLdap.Set {
		ba.AllowLocalNFSUsersWithLdap.SetToNull()
	}
	if fieldSet["description"] && !ba.Description.Set {
		ba.Description.SetToNull()
	}
	if fieldSet["administrators"] && !ba.Administrators.Set {
		ba.Administrators.SetToNull()
	}
}

func applyBatchADFieldSelection(ba *gcpgenserver.BatchActiveDirectoryV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		ba.ResourceId.Reset()
		ba.Username.Reset()
		ba.Password.Reset()
		ba.Domain.Reset()
		ba.DNS.Reset()
		ba.NetBIOS.Reset()
		ba.OrganizationalUnit.Reset()
		ba.Site.Reset()
		ba.KdcIP.Reset()
		ba.KdcHostname.Reset()
		ba.ActiveDirectoryState.Reset()
		ba.ActiveDirectoryStateDetails.Reset()
		ba.CreatedAt.Reset()
		ba.EncryptDCConnections.Reset()
		ba.BackupOperators.Reset()
		ba.AesEncryption.Reset()
		ba.LdapSigning.Reset()
		ba.SecurityOperators.Reset()
		ba.AllowLocalNFSUsersWithLdap.Reset()
		ba.Description.Reset()
		ba.Administrators.Reset()
		return
	}

	if !fieldSet["resourceId"] {
		ba.ResourceId.Reset()
	}
	if !fieldSet["username"] {
		ba.Username.Reset()
	}
	if !fieldSet["password"] {
		ba.Password.Reset()
	}
	if !fieldSet["domain"] {
		ba.Domain.Reset()
	}
	if !fieldSet["DNS"] {
		ba.DNS.Reset()
	}
	if !fieldSet["netBIOS"] {
		ba.NetBIOS.Reset()
	}
	if !fieldSet["organizationalUnit"] {
		ba.OrganizationalUnit.Reset()
	}
	if !fieldSet["site"] {
		ba.Site.Reset()
	}
	if !fieldSet["kdcIP"] {
		ba.KdcIP.Reset()
	}
	if !fieldSet["kdcHostname"] {
		ba.KdcHostname.Reset()
	}
	if !fieldSet["activeDirectoryState"] {
		ba.ActiveDirectoryState.Reset()
	}
	if !fieldSet["activeDirectoryStateDetails"] {
		ba.ActiveDirectoryStateDetails.Reset()
	}
	if !fieldSet["createdAt"] {
		ba.CreatedAt.Reset()
	}
	if !fieldSet["encryptDCConnections"] {
		ba.EncryptDCConnections.Reset()
	}
	if !fieldSet["backupOperators"] {
		ba.BackupOperators.Reset()
	}
	if !fieldSet["aesEncryption"] {
		ba.AesEncryption.Reset()
	}
	if !fieldSet["ldapSigning"] {
		ba.LdapSigning.Reset()
	}
	if !fieldSet["securityOperators"] {
		ba.SecurityOperators.Reset()
	}
	if !fieldSet["allowLocalNFSUsersWithLdap"] {
		ba.AllowLocalNFSUsersWithLdap.Reset()
	}
	if !fieldSet["description"] {
		ba.Description.Reset()
	}
	if !fieldSet["administrators"] {
		ba.Administrators.Reset()
	}

	ensureRequestedADFieldsPresent(ba, fieldSet)
}

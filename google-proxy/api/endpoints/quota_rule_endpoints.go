package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-faster/jx"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/quota_rules"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	orchestratorcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	enableQuotaRule              = env.GetBool("ENABLE_QUOTA_RULE", true)
	getMultipleQuotaRulesFromCVP = _getMultipleQuotaRulesFromCVP
)

// convertQuotaRuleToV1beta converts models.QuotaRule to gcpgenserver.QuotaRulesV1beta
func convertQuotaRuleToV1beta(quotaRule *models.QuotaRule) *gcpgenserver.QuotaRulesV1beta {
	// Convert quota type and lifecycle state
	quotaType := QuotaRuleQuotaTypeV1Beta(quotaRule.QuotaType)
	state := QuotaRuleLifeCycleV1Beta(quotaRule.LifeCycleState)

	return &gcpgenserver.QuotaRulesV1beta{
		QuotaId:        gcpgenserver.NewOptString(quotaRule.UUID),
		ResourceId:     quotaRule.Name,
		QuotaType:      quotaType,
		DiskLimitInMib: quotaRule.DiskLimitInMib,
		QuotaTarget:    gcpgenserver.NewOptString(quotaRule.QuotaTarget),
		State:          state,
		StateDetails:   gcpgenserver.NewOptString(quotaRule.LifeCycleStateDetails),
		Description:    gcpgenserver.NewOptString(quotaRule.Description),
		CreatedAt:      gcpgenserver.NewOptDateTime(quotaRule.CreatedAt),
		UpdatedAt:      gcpgenserver.NewOptDateTime(quotaRule.UpdatedAt),
	}
}

// encodeQuotaRuleV1 encodes a QuotaRulesV1beta struct to JSON.
func encodeQuotaRuleV1(quotaRuleV1beta *gcpgenserver.QuotaRulesV1beta) (jx.Raw, error) {
	data, err := json.Marshal(quotaRuleV1beta)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// convertToVCPQuotaRulesV1Beta converts an array of models.QuotaRule to []gcpgenserver.QuotaRulesV1beta
func convertToVCPQuotaRulesV1Beta(quotaRules []*models.QuotaRule) []gcpgenserver.QuotaRulesV1beta {
	quotaRulesV1Beta := make([]gcpgenserver.QuotaRulesV1beta, len(quotaRules))
	for i, quotaRule := range quotaRules {
		quotaRulesV1Beta[i] = *convertQuotaRuleToV1beta(quotaRule)
	}
	return quotaRulesV1Beta
}

// convertDatastoreQuotaRuleToModel converts a datamodel.QuotaRule to models.QuotaRule
func convertDatastoreQuotaRuleToModel(quotaRule *datamodel.QuotaRule) *models.QuotaRule {
	if quotaRule == nil {
		return nil
	}

	// Convert disk limit from KiB (database storage) back to MiB (API response)
	const kibToMibDivisor = 1024
	diskLimitInMib := quotaRule.DiskLimitInKib / kibToMibDivisor

	// Helper function to convert DeletedAt
	var deletedAt *time.Time
	if quotaRule.DeletedAt != nil && quotaRule.DeletedAt.Valid {
		deletedAt = &quotaRule.DeletedAt.Time
	}

	result := &models.QuotaRule{
		BaseModel: models.BaseModel{
			UUID:      quotaRule.UUID,
			CreatedAt: quotaRule.CreatedAt,
			UpdatedAt: quotaRule.UpdatedAt,
			DeletedAt: deletedAt,
		},
		Name:                  quotaRule.Name,
		Description:           quotaRule.Description,
		LifeCycleState:        quotaRule.State,
		LifeCycleStateDetails: quotaRule.StateDetails,
		QuotaType:             quotaRule.QuotaType,
		QuotaTarget:           quotaRule.QuotaTarget,
		DiskLimitInMib:        diskLimitInMib,
	}
	return result
}

// QuotaRuleQuotaTypeV1Beta converts a quota type string to gcpgenserver.QuotaRulesV1betaQuotaType
func QuotaRuleQuotaTypeV1Beta(quotaType string) gcpgenserver.QuotaRulesV1betaQuotaType {
	switch quotaType {
	case "INDIVIDUAL_USER_QUOTA":
		return gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA
	case "INDIVIDUAL_GROUP_QUOTA":
		return gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALGROUPQUOTA
	case "DEFAULT_USER_QUOTA":
		return gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA
	case "DEFAULT_GROUP_QUOTA":
		return gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTGROUPQUOTA
	default:
		return gcpgenserver.QuotaRulesV1betaQuotaType(quotaType)
	}
}

// QuotaRuleLifeCycleV1Beta converts a lifecycle state to gcpgenserver.OptQuotaRulesV1betaState
func QuotaRuleLifeCycleV1Beta(lifeCycleState string) gcpgenserver.OptQuotaRulesV1betaState {
	switch lifeCycleState {
	case datamodel.LifeCycleStateCreating:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateCREATING)
	case datamodel.LifeCycleStateREADY:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateREADY)
	case datamodel.LifeCycleStateUpdating:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateUPDATING)
	case datamodel.LifeCycleStateDeleting:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateDELETING)
	case datamodel.LifeCycleStateError:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateERROR)
	default:
		return gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateSTATEUNSPECIFIED)
	}
}

// QuotaRuleQuotaTypeVCPV1Beta converts a quota type string to gcpgenserver.QuotaRulesVCPV1betaQuotaType
func QuotaRuleQuotaTypeVCPV1Beta(quotaType string) gcpgenserver.QuotaRulesVCPV1betaQuotaType {
	switch quotaType {
	case "INDIVIDUAL_USER_QUOTA":
		return gcpgenserver.QuotaRulesVCPV1betaQuotaTypeINDIVIDUALUSERQUOTA
	case "INDIVIDUAL_GROUP_QUOTA":
		return gcpgenserver.QuotaRulesVCPV1betaQuotaTypeINDIVIDUALGROUPQUOTA
	case "DEFAULT_USER_QUOTA":
		return gcpgenserver.QuotaRulesVCPV1betaQuotaTypeDEFAULTUSERQUOTA
	case "DEFAULT_GROUP_QUOTA":
		return gcpgenserver.QuotaRulesVCPV1betaQuotaTypeDEFAULTGROUPQUOTA
	default:
		return gcpgenserver.QuotaRulesVCPV1betaQuotaType(quotaType)
	}
}

// QuotaRuleLifeCycleVCPV1Beta converts a lifecycle state to gcpgenserver.OptQuotaRulesVCPV1betaState
func QuotaRuleLifeCycleVCPV1Beta(lifeCycleState string) gcpgenserver.OptQuotaRulesVCPV1betaState {
	switch lifeCycleState {
	case datamodel.LifeCycleStateCreating:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateCREATING)
	case datamodel.LifeCycleStateREADY:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateREADY)
	case datamodel.LifeCycleStateUpdating:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateUPDATING)
	case datamodel.LifeCycleStateDeleting:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateDELETING)
	case datamodel.LifeCycleStateError:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateERROR)
	default:
		return gcpgenserver.NewOptQuotaRulesVCPV1betaState(gcpgenserver.QuotaRulesVCPV1betaStateSTATEUNSPECIFIED)
	}
}

// convertQuotaRuleToVCPResponse converts models.QuotaRule and datamodel.Job to gcpgenserver.QuotaRulesVCPV1beta
func convertQuotaRuleToVCPResponse(quotaRule *models.QuotaRule, job *datamodel.Job) *gcpgenserver.QuotaRulesVCPV1beta {
	// Convert quota type and lifecycle state
	quotaType := QuotaRuleQuotaTypeVCPV1Beta(quotaRule.QuotaType)
	state := QuotaRuleLifeCycleVCPV1Beta(quotaRule.LifeCycleState)

	// Build jobs array
	jobsList := make([]gcpgenserver.JobV1beta, 0)
	if job != nil {
		jobState := JobStateToVCPV1Beta(datamodel.JobState(job.State))
		jobsList = append(jobsList, gcpgenserver.JobV1beta{
			JobId:    gcpgenserver.NewOptString(job.UUID),
			Created:  gcpgenserver.NewOptDateTime(job.CreatedAt),
			WorkerId: gcpgenserver.NewOptString(job.WorkflowID),
			ObjectId: gcpgenserver.NewOptString(quotaRule.UUID),
			State:    jobState,
		})
	}

	// Build response
	return &gcpgenserver.QuotaRulesVCPV1beta{
		QuotaId:        gcpgenserver.NewOptString(quotaRule.UUID),
		ResourceId:     quotaRule.Name,
		QuotaType:      quotaType,
		DiskLimitInMib: quotaRule.DiskLimitInMib,
		QuotaTarget:    gcpgenserver.NewOptString(quotaRule.QuotaTarget),
		State:          state,
		StateDetails:   gcpgenserver.NewOptString(quotaRule.LifeCycleStateDetails),
		Description:    gcpgenserver.NewOptString(quotaRule.Description),
		CreatedAt:      gcpgenserver.NewOptDateTime(quotaRule.CreatedAt),
		UpdatedAt:      gcpgenserver.NewOptDateTime(quotaRule.UpdatedAt),
		Jobs:           jobsList,
	}
}

// JobStateToVCPV1Beta converts a job state to gcpgenserver.OptJobV1betaState
func JobStateToVCPV1Beta(jobState datamodel.JobState) gcpgenserver.OptJobV1betaState {
	switch jobState {
	case datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING:
		return gcpgenserver.NewOptJobV1betaState(gcpgenserver.JobV1betaStateOngoing)
	case datamodel.JobsStateDONE:
		return gcpgenserver.NewOptJobV1betaState(gcpgenserver.JobV1betaStateDone)
	case datamodel.JobsStateERROR:
		return gcpgenserver.NewOptJobV1betaState(gcpgenserver.JobV1betaStateError)
	default:
		return gcpgenserver.NewOptJobV1betaState(gcpgenserver.JobV1betaStateOngoing)
	}
}

// convertCVPQuotaRuleToV1beta converts CVP QuotaRulesV1beta to gcpgenserver.QuotaRulesV1beta
func convertCVPQuotaRuleToV1beta(cvpRule *cvpmodels.QuotaRulesV1beta) gcpgenserver.QuotaRulesV1beta {
	quotaRule := gcpgenserver.QuotaRulesV1beta{
		ResourceId:     nillable.GetString(cvpRule.ResourceID, ""),
		DiskLimitInMib: nillable.GetInt64(cvpRule.DiskLimitInMib, 0),
		QuotaTarget:    gcpgenserver.NewOptString(nillable.GetString(cvpRule.QuotaTarget, "")),
		StateDetails:   gcpgenserver.NewOptString(cvpRule.StateDetails),
		Description:    gcpgenserver.NewOptString(nillable.GetString(cvpRule.Description, "")),
	}
	if cvpRule.QuotaID != "" {
		quotaRule.QuotaId = gcpgenserver.NewOptString(cvpRule.QuotaID)
	}

	// Convert quota type
	if cvpRule.QuotaType != nil {
		quotaRule.QuotaType = QuotaRuleQuotaTypeV1Beta(*cvpRule.QuotaType)
	}

	// Convert state
	if cvpRule.State != "" {
		quotaRule.State = QuotaRuleLifeCycleV1Beta(cvpRule.State)
	} else {
		quotaRule.State = gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateSTATEUNSPECIFIED)
	}

	// Convert timestamps
	if !cvpRule.CreatedAt.IsZero() {
		quotaRule.CreatedAt = gcpgenserver.NewOptDateTime(time.Time(cvpRule.CreatedAt))
	}
	if !cvpRule.UpdatedAt.IsZero() {
		quotaRule.UpdatedAt = gcpgenserver.NewOptDateTime(time.Time(cvpRule.UpdatedAt))
	}

	return quotaRule
}

// _getMultipleQuotaRulesFromCVP fetches quota rules from CVP when not found in VCP
func _getMultipleQuotaRulesFromCVP(ctx context.Context, req *gcpgenserver.QuotaRuleIdListV1beta, params gcpgenserver.V1betaGetMultipleQuotaRulesParams, vcpQuotaRules []gcpgenserver.QuotaRulesV1beta) (gcpgenserver.V1betaGetMultipleQuotaRulesRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	reqParams := &quota_rules.V1betaGetMultipleQuotaRulesParams{
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
		VolumeID:      params.VolumeId,
		Body: &cvpmodels.QuotaRuleIDListV1beta{
			QuotaRuleUUIDs: req.QuotaRuleUuids,
		},
	}
	if params.XCorrelationID.IsSet() {
		reqParams.XCorrelationID = &params.XCorrelationID.Value
	}
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createCVPClient(logger, jwtToken)
	resp, err := cvpClient.QuotaRules.V1betaGetMultipleQuotaRules(reqParams)
	if err != nil {
		logger.Errorf("Received error from CVP client for the V1betaGetMultipleQuotaRules call: %v", err)
		switch e := err.(type) {
		case *quota_rules.V1betaGetMultipleQuotaRulesNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := nillable.GetFloat64(&e.Payload.Code, 0)
			return &gcpgenserver.V1betaGetMultipleQuotaRulesNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *quota_rules.V1betaGetMultipleQuotaRulesBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := nillable.GetFloat64(&e.Payload.Code, 0)
			return &gcpgenserver.V1betaGetMultipleQuotaRulesBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *quota_rules.V1betaGetMultipleQuotaRulesUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := nillable.GetFloat64(&e.Payload.Code, 0)
			return &gcpgenserver.V1betaGetMultipleQuotaRulesUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *quota_rules.V1betaGetMultipleQuotaRulesForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := nillable.GetFloat64(&e.Payload.Code, 0)
			return &gcpgenserver.V1betaGetMultipleQuotaRulesForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *quota_rules.V1betaGetMultipleQuotaRulesTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := nillable.GetFloat64(&e.Payload.Code, 0)
			return &gcpgenserver.V1betaGetMultipleQuotaRulesTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *quota_rules.V1betaGetMultipleQuotaRulesDefault:
			return &gcpgenserver.V1betaGetMultipleQuotaRulesInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}

	// Converting CVP model to gcpgenserver.QuotaRulesV1beta
	quotaRuleResponse := gcpgenserver.V1betaGetMultipleQuotaRulesOK{
		QuotaRules: []gcpgenserver.QuotaRulesV1beta{},
	}

	if resp != nil && resp.Payload != nil && len(resp.Payload.QuotaRules) != 0 {
		for _, quotaRule := range resp.Payload.QuotaRules {
			quotaRuleResponse.QuotaRules = append(quotaRuleResponse.QuotaRules, convertCVPQuotaRuleToV1beta(quotaRule))
		}
	}

	// Append VCP quota rules if any
	if len(vcpQuotaRules) > 0 {
		quotaRuleResponse.QuotaRules = append(quotaRuleResponse.QuotaRules, vcpQuotaRules...)
	}
	return &quotaRuleResponse, nil
}

// quotaRuleAPIDisabledMessage is returned when ENABLE_QUOTA_RULE is false
const quotaRuleAPIDisabledMessage = "quota rule API is disabled"

// V1betaCreateQuotaRule is a handler for creating a quota rule
func (h Handler) V1betaCreateQuotaRule(ctx context.Context, req *gcpgenserver.QuotaRuleCreateV1beta, params gcpgenserver.V1betaCreateQuotaRuleParams) (gcpgenserver.V1betaCreateQuotaRuleRes, error) {
	logger := util.GetLogger(ctx)
	if !enableQuotaRule {
		return &gcpgenserver.V1betaCreateQuotaRuleBadRequest{
			Code:    http.StatusBadRequest,
			Message: quotaRuleAPIDisabledMessage,
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateQuotaRuleBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.CreateQuotaRulesParam{
		Name:           req.ResourceId,
		VolumeUUID:     params.VolumeId,
		QuotaType:      string(req.QuotaType),
		DiskLimitInMib: req.DiskLimitInMib,
		QuotaTarget:    req.QuotaTarget.Value,
		ProjectId:      params.ProjectNumber,
		Description:    req.Description.Value,
		LocationId:     params.LocationId,
	}

	// Call an orchestrator to create the quota rule
	quotaRule, operationID, err := h.Orchestrator.CreateQuotaRule(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateQuotaRuleBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaCreateQuotaRuleConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to create quota rule: %v", err)
		return &gcpgenserver.V1betaCreateQuotaRuleInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, err
	}

	quotaRuleRes, err := encodeQuotaRuleV1(convertQuotaRuleToV1beta(quotaRule))
	if err != nil {
		return &gcpgenserver.V1betaCreateQuotaRuleInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	// Build operation ID
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
		Response: quotaRuleRes,
		Done:     gcpgenserver.NewOptBool(false),
	}, nil
}

// V1betaCreateQuotaRuleVCP is a handler for creating a quota rule via internal VCP API
func (h Handler) V1betaCreateQuotaRuleVCP(ctx context.Context, req *gcpgenserver.QuotaRuleCreateV1beta, params gcpgenserver.V1betaCreateQuotaRuleVCPParams) (gcpgenserver.V1betaCreateQuotaRuleVCPRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateQuotaRuleVCPBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.CreateQuotaRulesParam{
		Name:           req.ResourceId,
		VolumeUUID:     params.VolumeId,
		QuotaType:      string(req.QuotaType),
		DiskLimitInMib: req.DiskLimitInMib,
		QuotaTarget:    req.QuotaTarget.Value,
		ProjectId:      params.ProjectNumber,
		Description:    req.Description.Value,
		LocationId:     params.LocationId,
	}

	// Call internal orchestrator function (skips replication validation)
	quotaRule, job, err := h.Orchestrator.CreateQuotaRuleInternal(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateQuotaRuleVCPBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaCreateQuotaRuleVCPConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to create quota rule via internal API: %v", err)
		return &gcpgenserver.V1betaCreateQuotaRuleVCPInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, err
	}

	return convertQuotaRuleToVCPResponse(quotaRule, job), nil
}

// V1betaUpdateQuotaRule is a handler for updating a quota rule
func (h Handler) V1betaUpdateQuotaRule(ctx context.Context, req *gcpgenserver.QuotaRulesUpdateV1beta, params gcpgenserver.V1betaUpdateQuotaRuleParams) (gcpgenserver.V1betaUpdateQuotaRuleRes, error) {
	logger := util.GetLogger(ctx)
	if !enableQuotaRule {
		return &gcpgenserver.V1betaUpdateQuotaRuleBadRequest{
			Code:    http.StatusBadRequest,
			Message: quotaRuleAPIDisabledMessage,
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateQuotaRuleBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.UpdateQuotaRulesParam{
		QuotaRuleUUID: params.QuotaRuleId,
		ProjectId:     params.ProjectNumber,
		LocationId:    params.LocationId,
	}

	// Set DiskLimitInMib only if provided in request
	if req.DiskLimitInMib.IsSet() {
		quotaParam.DiskLimitInMib = req.DiskLimitInMib.Value
	}

	// Set Description only if provided in request
	if req.Description.IsSet() {
		quotaParam.Description = req.Description.Value
	}

	// Call orchestrator to update the quota rule
	quotaRule, operationID, err := h.Orchestrator.UpdateQuotaRule(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateQuotaRuleBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdateQuotaRuleConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to update quota rule: %v", err)
		return &gcpgenserver.V1betaUpdateQuotaRuleInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, err
	}

	quotaRuleRes, err := encodeQuotaRuleV1(convertQuotaRuleToV1beta(quotaRule))
	if err != nil {
		return &gcpgenserver.V1betaUpdateQuotaRuleInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	// Build operation ID - use empty string if operationID is empty (synchronous update)
	operationName := ""
	if operationID != "" {
		operationName = fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationName),
		Response: quotaRuleRes,
		Done:     gcpgenserver.NewOptBool(false),
	}, nil
}

// V1betaDeleteQuotaRule is a handler for deleting a quota rule
func (h Handler) V1betaDeleteQuotaRule(ctx context.Context, params gcpgenserver.V1betaDeleteQuotaRuleParams) (gcpgenserver.V1betaDeleteQuotaRuleRes, error) {
	logger := util.GetLogger(ctx)
	if !enableQuotaRule {
		return &gcpgenserver.V1betaDeleteQuotaRuleBadRequest{
			Code:    http.StatusBadRequest,
			Message: quotaRuleAPIDisabledMessage,
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeleteQuotaRuleBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.DeleteQuotaRulesParam{
		QuotaRuleUUID: params.QuotaRuleId,
		ProjectId:     params.ProjectNumber,
		LocationId:    params.LocationId,
	}

	// Call orchestrator to delete the quota rule
	quotaRule, operationID, err := h.Orchestrator.DeleteQuotaRule(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDeleteQuotaRuleBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaDeleteQuotaRuleConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to delete quota rule: %v", err)
		return &gcpgenserver.V1betaDeleteQuotaRuleInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, err
	}

	quotaRuleRes, err := encodeQuotaRuleV1(convertQuotaRuleToV1beta(quotaRule))
	if err != nil {
		return &gcpgenserver.V1betaDeleteQuotaRuleInternalServerError{Code: 500, Message: err.Error()}, nil
	}
	// Build operation ID - use empty string if operationID is empty (synchronous delete)
	operationName := ""
	if operationID != "" {
		operationName = fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationName),
		Response: quotaRuleRes,
		Done:     gcpgenserver.NewOptBool(false),
	}, nil
}

// V1betaUpdateQuotaRuleVCP is a handler for updating a quota rule via internal VCP API
func (h Handler) V1betaUpdateQuotaRuleVCP(ctx context.Context, req *gcpgenserver.QuotaRulesUpdateV1beta, params gcpgenserver.V1betaUpdateQuotaRuleVCPParams) (gcpgenserver.V1betaUpdateQuotaRuleVCPRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateQuotaRuleVCPBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.UpdateQuotaRulesParam{
		QuotaRuleUUID: params.QuotaRuleId,
		ProjectId:     params.ProjectNumber,
		LocationId:    params.LocationId,
	}

	// Set DiskLimitInMib only if provided in request
	if req.DiskLimitInMib.IsSet() {
		quotaParam.DiskLimitInMib = req.DiskLimitInMib.Value
	}

	// Set Description only if provided in request
	if req.Description.IsSet() {
		quotaParam.Description = req.Description.Value
	}

	// Call internal orchestrator function (skips replication validation)
	quotaRule, job, err := h.Orchestrator.UpdateQuotaRuleInternal(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateQuotaRuleVCPBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdateQuotaRuleVCPConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to update quota rule via internal API: %v", err)
		return &gcpgenserver.V1betaUpdateQuotaRuleVCPInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, err
	}

	return convertQuotaRuleToVCPResponse(quotaRule, job), nil
}

// V1betaDeleteQuotaRuleVCP is a handler for deleting a quota rule via internal VCP API
func (h Handler) V1betaDeleteQuotaRuleVCP(ctx context.Context, params gcpgenserver.V1betaDeleteQuotaRuleVCPParams) (gcpgenserver.V1betaDeleteQuotaRuleVCPRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeleteQuotaRuleVCPBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create a parameter struct for orchestrator
	quotaParam := &orchestratorcommon.DeleteQuotaRulesParam{
		QuotaRuleUUID: params.QuotaRuleId,
		ProjectId:     params.ProjectNumber,
		LocationId:    params.LocationId,
	}

	// Call internal orchestrator function (skips replication validation)
	quotaRule, job, err := h.Orchestrator.DeleteQuotaRuleInternal(ctx, quotaParam)

	if err != nil {
		// Handle validation and not found errors
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDeleteQuotaRuleVCPBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaDeleteQuotaRuleVCPConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		logger.Errorf("Failed to delete quota rule via internal API: %v", err)
		return &gcpgenserver.V1betaDeleteQuotaRuleVCPInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, err
	}

	return convertQuotaRuleToVCPResponse(quotaRule, job), nil
}

// V1betaListAllQuotaRules is a handler for listing all quota rules for a volume
func (h Handler) V1betaListAllQuotaRules(ctx context.Context, params gcpgenserver.V1betaListAllQuotaRulesParams) (gcpgenserver.V1betaListAllQuotaRulesRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Validate location
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaListAllQuotaRulesBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Create parameter struct for orchestrator
	listParams := &orchestratorcommon.ListQuotaRulesParams{
		AccountName: params.ProjectNumber,
		VolumeID:    params.VolumeId,
	}

	// Call orchestrator to list quota rules
	quotaRuleList, err := h.Orchestrator.ListQuotaRules(ctx, listParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaListAllQuotaRulesNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		}
		logger.Errorf("Failed to list quota rules for volume %s with error: %v", params.VolumeId, err.Error())
		return &gcpgenserver.V1betaListAllQuotaRulesInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, err
	}

	// Convert to API response format
	return &gcpgenserver.V1betaListAllQuotaRulesOK{
		QuotaRules: convertToVCPQuotaRulesV1Beta(quotaRuleList),
	}, nil
}

// V1betaGetMultipleQuotaRules is a handler for getting multiple quota rules by UUIDs
func (h Handler) V1betaGetMultipleQuotaRules(ctx context.Context, req *gcpgenserver.QuotaRuleIdListV1beta, params gcpgenserver.V1betaGetMultipleQuotaRulesParams) (gcpgenserver.V1betaGetMultipleQuotaRulesRes, error) {
	logger := util.GetLogger(ctx)
	if !enableQuotaRule {
		return &gcpgenserver.V1betaGetMultipleQuotaRulesBadRequest{
			Code:    http.StatusBadRequest,
			Message: quotaRuleAPIDisabledMessage,
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaGetMultipleQuotaRulesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	quotaRuleModelVCP, err := h.Orchestrator.GetMultipleQuotaRules(ctx, params.VolumeId, params.ProjectNumber, req.QuotaRuleUuids)
	if err != nil {
		// When volume or account does not exist in VCP, fallback to CVP
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrVolumeOrAccountNotFoundInVCP) {
			// SDE gate: skip CVP fallback when CVP_HOST is not configured.
			if !utils.IsCVPHostConfigured() {
				logger.Info("CVP_HOST is not configured, skipping CVP call",
					"volumeId", params.VolumeId, "projectNumber", params.ProjectNumber)
				return &gcpgenserver.V1betaGetMultipleQuotaRulesOK{
					QuotaRules: []gcpgenserver.QuotaRulesV1beta{},
				}, nil
			}
			return getMultipleQuotaRulesFromCVP(ctx, req, params, []gcpgenserver.QuotaRulesV1beta{})
		}
		logger.Error("Failed to fetch quota rules", "error", err.Error())
		return &gcpgenserver.V1betaGetMultipleQuotaRulesInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}

	quotaRulesVCP := make([]gcpgenserver.QuotaRulesV1beta, 0, len(quotaRuleModelVCP))
	for _, quotaRule := range quotaRuleModelVCP {
		response := convertQuotaRuleToV1beta(quotaRule)
		quotaRulesVCP = append(quotaRulesVCP, *response)
	}
	return &gcpgenserver.V1betaGetMultipleQuotaRulesOK{
		QuotaRules: quotaRulesVCP,
	}, nil
}

// V1betaDescribeQuotaRule is a handler for describing a single quota rule by ID
func (h Handler) V1betaDescribeQuotaRule(ctx context.Context, params gcpgenserver.V1betaDescribeQuotaRuleParams) (gcpgenserver.V1betaDescribeQuotaRuleRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDescribeQuotaRuleBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	quotaRule, err := h.Orchestrator.DescribeQuotaRule(ctx, params.VolumeId, params.ProjectNumber, params.QuotaRuleId)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDescribeQuotaRuleNotFound{
				Code:    404,
				Message: "Quota rule not found",
			}, nil
		}
		logger.Error("Failed to fetch quota rule", "error", err.Error())
		return &gcpgenserver.V1betaDescribeQuotaRuleInternalServerError{Code: 500, Message: "Internal server error"}, nil
	}

	if quotaRule == nil {
		return &gcpgenserver.V1betaDescribeQuotaRuleNotFound{
			Code:    404,
			Message: "Quota rule not found",
		}, nil
	}

	return convertQuotaRuleToV1beta(quotaRule), nil
}

// V1betaUpdateDestinationQuotaRulesVCP is a handler for updating destination quota rules with source quota rules via internal VCP API
func (h Handler) V1betaUpdateDestinationQuotaRulesVCP(ctx context.Context, req *gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta, params gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams) (gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// Validate locationId
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPBadRequest{
			Code:    400,
			Message: parsingErr.GetMessage(),
		}, nil
	}

	// Convert gcpgenserver types to common types
	commonReq := convertGcpUpdateDstWithSrcQuotaRulesToCommon(req)
	commonParams := convertGcpV1betaUpdateDestinationQuotaRulesVCPParamsToCommon(params)

	// Replace destination quota rules with source quota rules via orchestrator (handles transaction)
	createdQuotaRules, err := h.Orchestrator.ReplaceDstQuotaRulesWithSrc(ctx, commonReq, commonParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPNotFound{
				Code:    404,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPUnprocessableEntity{
				Code:    422,
				Message: err.Error(),
			}, nil
		}
		logger.Errorf("Failed to replace destination quota rules: %v", err)
		return &gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}, err
	}

	// Convert datamodel.QuotaRule to models.QuotaRule, then to API response format
	quotaRulesV1Beta := make([]gcpgenserver.QuotaRulesV1beta, 0, len(createdQuotaRules))
	for _, quotaRule := range createdQuotaRules {
		// Convert datamodel.QuotaRule to models.QuotaRule
		modelsQuotaRule := convertDatastoreQuotaRuleToModel(quotaRule)
		// Convert models.QuotaRule to API response format
		apiQuotaRule := convertQuotaRuleToV1beta(modelsQuotaRule)
		quotaRulesV1Beta = append(quotaRulesV1Beta, *apiQuotaRule)
	}

	// Return success response with quota rules
	return &gcpgenserver.UpdateDestinationQuotaRulesResponseV1beta{
		State:      gcpgenserver.NewOptString("SUCCESS"),
		QuotaRules: quotaRulesV1Beta,
	}, nil
}

// convertGcpUpdateDstWithSrcQuotaRulesToCommon converts gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta to commonparams.UpdateDstWithSrcQuotaRulesV1beta
func convertGcpUpdateDstWithSrcQuotaRulesToCommon(req *gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta) *commonparams.UpdateDstWithSrcQuotaRulesV1beta {
	commonReq := &commonparams.UpdateDstWithSrcQuotaRulesV1beta{
		SrcQuotaRules: make([]commonparams.QuotaRulesV1beta, 0, len(req.SrcQuotaRules)),
		DstQuotaRules: make([]commonparams.QuotaRulesV1beta, 0, len(req.DstQuotaRules)),
	}

	for _, rule := range req.SrcQuotaRules {
		commonRule := convertGcpQuotaRulesV1betaToCommon(rule)
		commonReq.SrcQuotaRules = append(commonReq.SrcQuotaRules, commonRule)
	}

	for _, rule := range req.DstQuotaRules {
		commonRule := convertGcpQuotaRulesV1betaToCommon(rule)
		commonReq.DstQuotaRules = append(commonReq.DstQuotaRules, commonRule)
	}

	return commonReq
}

// convertGcpQuotaRulesV1betaToCommon converts gcpgenserver.QuotaRulesV1beta to commonparams.QuotaRulesV1beta
func convertGcpQuotaRulesV1betaToCommon(rule gcpgenserver.QuotaRulesV1beta) commonparams.QuotaRulesV1beta {
	commonRule := commonparams.QuotaRulesV1beta{
		ResourceId:     rule.ResourceId,
		QuotaType:      string(rule.QuotaType),
		DiskLimitInMib: rule.DiskLimitInMib,
	}

	if rule.QuotaId.IsSet() {
		val := rule.QuotaId.Value
		commonRule.QuotaId = &val
	}
	if rule.QuotaTarget.IsSet() {
		val := rule.QuotaTarget.Value
		commonRule.QuotaTarget = &val
	}
	if rule.State.IsSet() {
		val := string(rule.State.Value)
		commonRule.State = &val
	}
	if rule.StateDetails.IsSet() {
		val := rule.StateDetails.Value
		commonRule.StateDetails = &val
	}
	if rule.Description.IsSet() {
		val := rule.Description.Value
		commonRule.Description = &val
	}
	if rule.CreatedAt.IsSet() {
		val := rule.CreatedAt.Value
		commonRule.CreatedAt = &val
	}
	if rule.UpdatedAt.IsSet() {
		val := rule.UpdatedAt.Value
		commonRule.UpdatedAt = &val
	}

	return commonRule
}

// convertGcpV1betaUpdateDestinationQuotaRulesVCPParamsToCommon converts gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams to commonparams.V1betaUpdateDestinationQuotaRulesVCPParams
func convertGcpV1betaUpdateDestinationQuotaRulesVCPParamsToCommon(params gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams) commonparams.V1betaUpdateDestinationQuotaRulesVCPParams {
	commonParams := commonparams.V1betaUpdateDestinationQuotaRulesVCPParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		VolumeId:      params.VolumeId,
	}

	if params.XCorrelationID.IsSet() {
		val := params.XCorrelationID.Value
		commonParams.XCorrelationID = &val
	}

	return commonParams
}

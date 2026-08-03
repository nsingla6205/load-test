package api

import (
	"context"
	"encoding/json"
	goErrors "errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	roleName                  = "cmekNetAppVolumesRole"
	parseKmsConfigResponse    = _parseKmsConfigResponse
	encodeEncryptVolumeV1beta = _encodeEncryptVolumeV1beta
	cmekSupervisorGracePeriod = parseCmekSupervisorGracePeriod()
)

const (
	uriFormat    = "^projects\\/[^\\/]+\\/locations\\/[^\\/]+\\/keyRings\\/[^\\/]+\\/cryptoKeys.+$"
	regionGlobal = "global"
)

func parseCmekSupervisorGracePeriod() time.Duration {
	timeoutStr := env.GetString("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "14")
	duration, err := time.ParseDuration(timeoutStr + "m")
	if err != nil {
		return 14 * time.Minute
	}
	return duration
}

func shouldUseSDEForExistingKMSModel(kmsConfig *coremodel.KmsConfig) bool {
	if !utils.IsCVPHostConfigured() || kmsConfig == nil {
		return false
	}
	return kmsConfig.KmsAttributes == nil || !kmsConfig.KmsAttributes.IsVCPCreated()
}

func (h Handler) V1betaCheckKmsConfig(ctx context.Context, params gcpgenserver.V1betaCheckKmsConfigParams) (gcpgenserver.V1betaCheckKmsConfigRes, error) {
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCheckKmsConfigBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	logger := util.GetLogger(ctx)
	getKmsConfigParams := &common.GetKmsConfigParams{
		AccountName:   params.ProjectNumber,
		UUID:          params.KmsConfigId,
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
	}

	// Route UUID-based checks using KMS creation mode when local config exists.
	kmsConfigUUID := params.KmsConfigId
	kmsConfig, err := h.Orchestrator.GetKmsConfig(ctx, getKmsConfigParams)
	if err != nil {
		var notFoundErr *errors.NotFoundErr
		if goErrors.As(err, &notFoundErr) {
			if !utils.IsCVPHostConfigured() {
				return h.v1betaCheckKmsConfigVCP(ctx, params)
			}
		} else {
			return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}, nil
		}
	} else {
		if !shouldUseSDEForExistingKMSModel(kmsConfig) {
			return h.v1betaCheckKmsConfigVCP(ctx, params)
		}
		if kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.SdeKmsConfigUUID != "" {
			kmsConfigUUID = kmsConfig.KmsAttributes.SdeKmsConfigUUID
		}
	}

	// SDE/CVP path.
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	checkKmsConfigParams := &kms_configurations.V1betaCheckKmsConfigParams{
		KmsConfigID:    kmsConfigUUID,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}
	res, err := cvpClient.KmsConfigurations.V1betaCheckKmsConfig(checkKmsConfigParams)
	if err != nil {
		return categorizeCvpClientErrorsForCheckKmsConfigs(err)
	}
	if res == nil || res.Payload == nil {
		return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the check kms config",
		}, nil
	}

	checkKmsConfigResponse := convertToKmsConfigCheckV1beta(res)
	if kmsConfig != nil && kmsConfig.ServiceAccount != nil {
		checkParams := &coremodel.KmsConfigCheck{
			KmsConfig:   kmsConfig,
			Email:       kmsConfig.ServiceAccount.ServiceAccountEmail,
			IsHealthy:   checkKmsConfigResponse.KmsConfigHealthCheck.Value.IsHealthy,
			HealthError: checkKmsConfigResponse.KmsConfigHealthCheck.Value.HealthError.Value,
			ProxyType:   coremodel.ProxyTypeCvp,
		}

		if !checkKmsConfigResponse.KmsConfigHealthCheck.Value.IsHealthy {
			// CVP already reported unhealthy — update and return
			if _, healthErr := h.Orchestrator.CheckAndUpdateKmsConfigHealth(ctx, checkParams); healthErr != nil {
				return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
					Code:    http.StatusInternalServerError,
					Message: "Failed to update KMS config health",
				}, nil
			}
			return checkKmsConfigResponse, nil
		}

		// Verify crypto key access and update health
		if errResp, _ := h.verifyKmsKeyAccessAndUpdateHealth(ctx, kmsConfig, checkParams); errResp != nil {
			return errResp, nil
		}
	}
	return checkKmsConfigResponse, nil
}

// v1betaCheckKmsConfigVCP handles the VCP path for checking KMS config health.
// Gets the KMS config from VCP DB, accesses the KMS key directly (no impersonation),
// and updates the health status accordingly.
func (h Handler) v1betaCheckKmsConfigVCP(ctx context.Context, params gcpgenserver.V1betaCheckKmsConfigParams) (gcpgenserver.V1betaCheckKmsConfigRes, error) {
	getKmsConfigParams := &common.GetKmsConfigParams{
		AccountName:   params.ProjectNumber,
		UUID:          params.KmsConfigId,
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
	}

	kmsConfig, err := h.Orchestrator.GetKmsConfig(ctx, getKmsConfigParams)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCheckKmsConfigNotFound{
				Code:    http.StatusNotFound,
				Message: "KMS configuration not found",
			}, nil
		}
		return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	saEmail := ""
	if kmsConfig.KmsAttributes != nil {
		saEmail = kmsConfig.KmsAttributes.GetServiceAccountEmail()
	}

	checkParams := &coremodel.KmsConfigCheck{
		KmsConfig: kmsConfig,
		Email:     saEmail,
		IsHealthy: true,
		ProxyType: coremodel.ProxyTypeVcp,
	}

	// Verify crypto key access (direct, no impersonation) and update health
	if errResp, _ := h.verifyKmsKeyAccessAndUpdateHealth(ctx, kmsConfig, checkParams); errResp != nil {
		return errResp, nil
	}

	return &gcpgenserver.KmsConfigCheckV1beta{
		ServiceAccount: gcpgenserver.NewOptString(saEmail),
		KmsConfigHealthCheck: gcpgenserver.NewOptKmsConfigHealthCheckV1beta(gcpgenserver.KmsConfigHealthCheckV1beta{
			IsHealthy:    true,
			Instructions: gcpgenserver.NewOptString(getKmsInstructions(kmsConfig)),
		}),
	}, nil
}

// verifyKmsKeyAccessAndUpdateHealth accesses the KMS crypto key and updates the health status.
// Returns a non-nil response only on failure (access error or health update error).
// On success returns (nil, nil) — the caller builds the success response.
func (h Handler) verifyKmsKeyAccessAndUpdateHealth(ctx context.Context, kmsConfig *coremodel.KmsConfig, checkParams *coremodel.KmsConfigCheck) (gcpgenserver.V1betaCheckKmsConfigRes, error) {
	accessErr := h.Orchestrator.AccessCryptoKeyAndEncryptDataWithImpersonation(ctx, kmsConfig)
	if accessErr != nil {
		checkParams.IsHealthy = false
		checkParams.HealthError = accessErr.Error()
		if _, healthErr := h.Orchestrator.CheckAndUpdateKmsConfigHealth(ctx, checkParams); healthErr != nil {
			return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: "Failed to update KMS config health",
			}, nil
		}
		return convertErrorToKmsConfigCheckV1beta(accessErr, kmsConfig), nil
	}

	// Update the KMS config health as healthy
	if _, healthErr := h.Orchestrator.CheckAndUpdateKmsConfigHealth(ctx, checkParams); healthErr != nil {
		return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Failed to update KMS config health",
		}, nil
	}
	return nil, nil
}

func categorizeCvpClientErrorsForCheckKmsConfigs(cvpErr error) (gcpgenserver.V1betaCheckKmsConfigRes, error) {
	getMsg := func(msg *string) string {
		return nillable.GetString(msg, "")
	}
	getCode := func(floatVal *float64) float64 {
		return nillable.GetFloat64(floatVal, 0)
	}
	switch e := cvpErr.(type) {
	case *kms_configurations.V1betaCheckKmsConfigConflict:
		return &gcpgenserver.V1betaCheckKmsConfigConflict{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigBadRequest:
		return &gcpgenserver.V1betaCheckKmsConfigBadRequest{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigUnprocessableEntity:
		return &gcpgenserver.V1betaCheckKmsConfigUnprocessableEntity{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigUnauthorized:
		return &gcpgenserver.V1betaCheckKmsConfigUnauthorized{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigForbidden:
		return &gcpgenserver.V1betaCheckKmsConfigForbidden{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigNotFound:
		return &gcpgenserver.V1betaCheckKmsConfigNotFound{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigTooManyRequests:
		return &gcpgenserver.V1betaCheckKmsConfigTooManyRequests{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCheckKmsConfigDefault:
		return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	default:
		return &gcpgenserver.V1betaCheckKmsConfigInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the check kms config",
		}, nil
	}
}

func categorizeCvpClientErrorsForCreateKmsConfigs(cvpErr error) (gcpgenserver.V1betaCreateKmsConfigurationRes, error) {
	getMsg := func(msg *string) string {
		return nillable.GetString(msg, "")
	}
	getCode := func(floatVal *float64) float64 {
		return nillable.GetFloat64(floatVal, 0)
	}
	switch e := cvpErr.(type) {
	case *kms_configurations.V1betaCreateKmsConfigurationUnprocessableEntity:
		return &gcpgenserver.V1betaCreateKmsConfigurationUnprocessableEntity{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationConflict:
		return &gcpgenserver.V1betaCreateKmsConfigurationConflict{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationBadRequest:
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationUnauthorized:
		return &gcpgenserver.V1betaCreateKmsConfigurationUnauthorized{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationForbidden:
		return &gcpgenserver.V1betaCreateKmsConfigurationForbidden{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationTooManyRequests:
		return &gcpgenserver.V1betaCreateKmsConfigurationTooManyRequests{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	case *kms_configurations.V1betaCreateKmsConfigurationDefault:
		return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}, nil
	default:
		return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the create kms config",
		}, nil
	}
}

func (h Handler) V1betaCreateKmsConfiguration(ctx context.Context, req *gcpgenserver.KmsConfigV1beta, params gcpgenserver.V1betaCreateKmsConfigurationParams) (gcpgenserver.V1betaCreateKmsConfigurationRes, error) {
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	parsedKmsKeyFullPath, err := utils.ParseKeyFullPathResource(req.KeyFullPath)
	if err != nil {
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Code:    400,
			Message: "Invalid KeyFullPath format",
		}, nil
	}

	if parsedKmsKeyFullPath.Location == regionGlobal || parsedKmsKeyFullPath.Location != region {
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Code:    400,
			Message: "KMS configuration not supported for global region keys. Keys from other regions are also not supported",
		}, nil
	}

	getKmsConfigParams := &common.GetKmsConfigParams{
		AccountName: params.ProjectNumber,
		KeyFullPath: req.KeyFullPath,
	}
	kmsConfig, err := h.Orchestrator.GetExistingKmsConfig(ctx, getKmsConfigParams)
	if err != nil {
		var notFoundErr *errors.NotFoundErr
		var conflictErr *errors.ConflictErr
		switch {
		case goErrors.As(err, &notFoundErr):
			var createKmsConfigParams *common.CreateKmsConfigParams

			if !utils.IsCVPHostSet() {
				// VCP path: generate UUID locally, no SDE involvement
				createKmsConfigParams = &common.CreateKmsConfigParams{
					KeyFullPath:    req.KeyFullPath,
					ResourceID:     req.ResourceId.Value,
					AccountName:    params.ProjectNumber,
					LocationID:     params.LocationId,
					ProjectNumber:  params.ProjectNumber,
					UUID:           uuid.New().String(),
					XCorrelationID: params.XCorrelationID.Value,
					Description:    req.Description.Value,
				}
			} else {
				// SDE path: call CVP, create SDE job, parse response
				var errResp gcpgenserver.V1betaCreateKmsConfigurationRes
				createKmsConfigParams, errResp = h.createSDEKmsConfig(ctx, req, params)
				if errResp != nil {
					return errResp, nil
				}
			}
			createKmsConfigParams.TrialMode = newTrialModeParamsFromOpt(req.TrialMode)

			kmsConfig, operationID, err := h.Orchestrator.CreateKmsConfig(ctx, createKmsConfigParams)
			if err != nil {
				return categorizeCreateKmsConfigOrchestratorErrors(err)
			}

			resp, err := encodeKmsConfigV1(convertModelToKmsConfigV1Beta(kmsConfig))
			if err != nil {
				return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
					Code:    http.StatusInternalServerError,
					Message: err.Error(),
				}, nil
			}
			if operationID != "" {
				return &gcpgenserver.OperationV1beta{
					Name:     gcpgenserver.NewOptString(utils.GenerateOperationURL(params.ProjectNumber, params.LocationId, operationID)),
					Response: resp,
					Done:     gcpgenserver.NewOptBool(false),
				}, nil
			}
			return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: "Internal error while creating KMS configuration",
			}, nil
		case goErrors.As(err, &conflictErr):
			return &gcpgenserver.V1betaCreateKmsConfigurationConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		default:
			return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: err.Error(),
			}, nil
		}
	}

	checkResourceID := func() (gcpgenserver.V1betaCreateKmsConfigurationRes, bool) {
		if kmsConfig.ResourceID != req.ResourceId.Value {
			return &gcpgenserver.V1betaCreateKmsConfigurationConflict{
				Message: fmt.Sprintf("A KMS configuration with resource ID %s already exists for this region and project", kmsConfig.ResourceID),
				Code:    http.StatusConflict,
			}, false
		}
		return nil, true
	}

	var done bool
	switch kmsConfig.State {
	case datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateMigrating:
		return &gcpgenserver.V1betaCreateKmsConfigurationConflict{
			Message: "A KMS configuration already exists for this region and project and another operation is in progress.",
			Code:    http.StatusConflict,
		}, nil
	case datamodel.LifeCycleStateCreating:
		if res, ok := checkResourceID(); !ok {
			return res, nil
		}
		done = false
	default:
		// For all other states (ERROR, READY, CREATED, IN_USE, DISABLED, etc.), a KMS config already exists.
		// Verify resource ID to ensure idempotent creates succeed but conflicting creates are rejected.
		if res, ok := checkResourceID(); !ok {
			return res, nil
		}
		done = true
	}

	job, err := h.Orchestrator.GetJobByResourceUUID(ctx, kmsConfig.UUID, string(datamodel.JobTypeCreateKmsConfig))
	if err != nil {
		return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Error while fetching job for Create KMS configuration",
		}, nil
	}

	resp, err := encodeKmsConfigV1(convertModelToKmsConfigV1Beta(kmsConfig))
	if err != nil {
		return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(utils.GenerateOperationURL(params.ProjectNumber, params.LocationId, job.UUID)),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(done),
	}, nil
}

// createSDEKmsConfig handles KMS config creation via SDE: creates an SDE job, calls CVP client,
// parses the response, and returns the orchestrator params.
// On failure, returns a non-nil errResp (the HTTP error response to send back).
func (h Handler) createSDEKmsConfig(ctx context.Context, req *gcpgenserver.KmsConfigV1beta,
	params gcpgenserver.V1betaCreateKmsConfigurationParams) (_ *common.CreateKmsConfigParams, errResp gcpgenserver.V1betaCreateKmsConfigurationRes) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	body := &models.KmsConfigV1beta{
		ResourceID:  &req.ResourceId.Value,
		KeyFullPath: &req.KeyFullPath,
		Description: &req.Description.Value,
	}

	cvpCreateKmsConfigParams := &kms_configurations.V1betaCreateKmsConfigurationParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
		Body:           body,
	}

	jobPayloadAttributes := map[string]interface{}{
		"keyFullPath":   req.KeyFullPath,
		"projectNumber": params.ProjectNumber,
		"locationId":    params.LocationId,
	}

	createJobParams := &common.CreateJobParams{
		AccountName:  params.ProjectNumber,
		Type:         datamodel.JobTypeSdeKmsCreate,
		ResourceName: req.ResourceId.Value,
		JobAttributes: &datamodel.JobAttributes{
			PayloadAttributes: jobPayloadAttributes,
			SupervisorAttributes: &datamodel.SupervisorAttributes{
				OverrideGracePeriod: cmekSupervisorGracePeriod,
			},
		},
	}
	if params.XCorrelationID.Set {
		createJobParams.CorrelationID = params.XCorrelationID.Value
	}

	sdeJob, jobErr := h.Orchestrator.CreateJob(ctx, createJobParams)
	if jobErr != nil {
		logger.Error("Failed to create SDE KMS job", "error", jobErr.Error())
		return nil, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Failed to enqueue SDE KMS creation workflow",
		}
	}
	logger.Debug("Created SDE KMS job", "jobUUID", sdeJob.UUID)

	markSdeJobError := func(failure error) {
		if failure == nil || sdeJob == nil {
			return
		}
		if updateErr := h.Orchestrator.UpdateJobStatus(
			ctx, sdeJob.UUID, string(datamodel.JobsStateERROR), sdeJob.TrackingID, failure.Error(),
		); updateErr != nil {
			logger.Warn("Failed to mark SDE job as error", "jobUUID", sdeJob.UUID, "error", updateErr)
		}
	}

	clearSdeJobGracePeriod := func() {
		if sdeJob == nil {
			return
		}
		attrs := &datamodel.JobAttributes{
			PayloadAttributes: jobPayloadAttributes,
			SupervisorAttributes: &datamodel.SupervisorAttributes{
				OverrideGracePeriod: 0,
			},
		}
		if updateErr := h.Orchestrator.UpdateJobAttributes(ctx, sdeJob.UUID, attrs); updateErr != nil {
			logger.Warn("Failed to clear override grace period for SDE job", "jobUUID", sdeJob.UUID, "error", updateErr)
		}
	}

	cvpResponse, err := cvpClient.KmsConfigurations.V1betaCreateKmsConfiguration(cvpCreateKmsConfigParams)
	if err != nil {
		markSdeJobError(err)
		resp, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		return nil, resp
	}

	parsedCvpResponse, err := parseKmsConfigResponse(cvpResponse.Payload.Response)
	if err != nil {
		clearSdeJobGracePeriod()
		return nil, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Failed to parse KMS configuration response",
		}
	}

	return &common.CreateKmsConfigParams{
		KeyFullPath:    req.KeyFullPath,
		ResourceID:     req.ResourceId.Value,
		AccountName:    params.ProjectNumber,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		OperationUri:   cvpResponse.Payload.Name,
		OperationDone:  *cvpResponse.Payload.Done,
		UUID:           parsedCvpResponse.UUID,
		XCorrelationID: params.XCorrelationID.Value,
		Description:    req.Description.Value,
		SdeJobUUID:     sdeJob.UUID,
	}, nil
}

// categorizeCreateKmsConfigOrchestratorErrors maps orchestrator errors to HTTP responses.
func categorizeCreateKmsConfigOrchestratorErrors(err error) (gcpgenserver.V1betaCreateKmsConfigurationRes, error) {
	var conflictErr *errors.ConflictErr
	var badRequestErr *errors.BadRequestErr
	switch {
	case goErrors.As(err, &conflictErr):
		return &gcpgenserver.V1betaCreateKmsConfigurationConflict{
			Message: conflictErr.Error(),
			Code:    http.StatusConflict,
		}, nil
	case goErrors.As(err, &badRequestErr):
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Message: badRequestErr.Error(),
			Code:    http.StatusBadRequest,
		}, nil
	case errors.IsUserInputValidationErr(err):
		return &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{
			Message: err.Error(),
			Code:    http.StatusBadRequest,
		}, nil
	default:
		return &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{
			Message: "An unexpected error occurred",
			Code:    http.StatusInternalServerError,
		}, nil
	}
}

func categorizeCvpClientErrorsForDescribeKmsConfigs(cvpErr error) (gcpgenserver.V1betaDescribeKmsConfigurationRes, error) {
	getMsg := func(msg *string) string {
		return nillable.GetString(msg, "")
	}
	getCode := func(code *float64) float64 {
		return nillable.GetFloat64(code, 0)
	}
	switch e := cvpErr.(type) {
	case *kms_configurations.V1betaDescribeKmsConfigurationNotFound:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationNotFound{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationUnprocessableEntity:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationUnprocessableEntity{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationConflict:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationConflict{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationBadRequest:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationBadRequest{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationForbidden:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationForbidden{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationUnauthorized:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationUnauthorized{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationTooManyRequests:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationTooManyRequests{
			Code:    code,
			Message: msg,
		}, nil
	case *kms_configurations.V1betaDescribeKmsConfigurationDefault:
		msg := getMsg(&e.Payload.Message)
		code := getCode(&e.Payload.Code)
		return &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{
			Code:    code,
			Message: msg,
		}, nil
	default:
		return &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the describe kms config",
		}, nil
	}
}
func (h Handler) V1betaDescribeKmsConfiguration(ctx context.Context, params gcpgenserver.V1betaDescribeKmsConfigurationParams) (gcpgenserver.V1betaDescribeKmsConfigurationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDescribeKmsConfigurationBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	getKmsConfigParams := &common.GetKmsConfigParams{
		AccountName:   params.ProjectNumber,
		UUID:          params.KmsConfigId,
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
	}

	kmsConfig, err := h.Orchestrator.GetKmsConfig(ctx, getKmsConfigParams)
	if err == nil && !shouldUseSDEForExistingKMSModel(kmsConfig) {
		return convertModelToKmsConfigV1Beta(kmsConfig), nil
	}
	if err != nil && !errors.IsNotFoundErr(err) {
		return &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{
			Message: err.Error(),
			Code:    http.StatusInternalServerError,
		}, nil
	}
	if err != nil && errors.IsNotFoundErr(err) && !utils.IsCVPHostConfigured() {
		return &gcpgenserver.V1betaDescribeKmsConfigurationNotFound{
			Code:    http.StatusNotFound,
			Message: "KMS configuration not found",
		}, nil
	}

	// SDE path: get from SDE first, then enrich with VCP data when available.
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	kmsConfigID := params.KmsConfigId
	if kmsConfig != nil && kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.SdeKmsConfigUUID != "" {
		kmsConfigID = kmsConfig.KmsAttributes.SdeKmsConfigUUID
	}

	describeKmsConfigParams := &kms_configurations.V1betaDescribeKmsConfigurationParams{
		KmsConfigID:    kmsConfigID,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}
	res, err := cvpClient.KmsConfigurations.V1betaDescribeKmsConfiguration(describeKmsConfigParams)
	if err != nil {
		return categorizeCvpClientErrorsForDescribeKmsConfigs(err)
	}
	if res == nil || res.Payload == nil {
		return &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the describe kms configuration",
		}, nil
	}

	if kmsConfig == nil {
		kmsConfig, err = h.Orchestrator.GetKmsConfig(ctx, getKmsConfigParams)
	}
	if err != nil {
		var notFoundErr *errors.NotFoundErr
		switch {
		case goErrors.As(err, &notFoundErr):
			return convertToKmsConfigV1beta(res.Payload), nil
		default:
			return &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{
				Message: err.Error(),
				Code:    http.StatusInternalServerError,
			}, nil
		}
	}
	switch res.Payload.KmsState {
	case datamodel.LifeCycleStateError:
		kmsConfig.State = datamodel.LifeCycleStateError
	case datamodel.LifeCycleStateInUse:
		kmsConfig.State = datamodel.LifeCycleStateInUse
	}
	return convertModelToKmsConfigV1Beta(kmsConfig), nil
}

func (h Handler) V1betaListKmsConfigurations(ctx context.Context, params gcpgenserver.V1betaListKmsConfigurationsParams) (gcpgenserver.V1betaListKmsConfigurationsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	// VCP path: list directly from VCP DB, no SDE involvement
	if !utils.IsCVPHostSet() {
		kmsConfigs, err := h.Orchestrator.ListKmsConfigs(ctx, params.ProjectNumber)
		if err != nil {
			logger.Error("Failed to list KMS configurations from VCP database", "error", err)
			return &gcpgenserver.V1betaListKmsConfigurationsInternalServerError{
				Code:    http.StatusInternalServerError,
				Message: "Failed to list KMS configurations",
			}, nil
		}
		operationResponse := gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON{}
		for _, kmsConfig := range kmsConfigs {
			operationResponse = append(operationResponse, *convertModelToKmsConfigV1Beta(kmsConfig))
		}
		return &operationResponse, nil
	}

	// SDE path: list from SDE, enrich with VCP data
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	listKmsConfigParams := &kms_configurations.V1betaListKmsConfigurationsParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}
	res, err := cvpClient.KmsConfigurations.V1betaListKmsConfigurations(listKmsConfigParams)
	if err != nil {
		switch e := err.(type) {
		case *kms_configurations.V1betaListKmsConfigurationsConflict:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsConflict{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *kms_configurations.V1betaListKmsConfigurationsDefault:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListKmsConfigurationsInternalServerError{
				Code:    code,
				Message: msg,
			}, nil
		}
	}
	if res == nil || res.Payload == nil {
		return &gcpgenserver.V1betaListKmsConfigurationsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the list kms configurations",
		}, nil
	}
	operationResponse := gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON{}
	for _, kmsConfig := range res.Payload {
		getKmsConfigParams := &common.GetKmsConfigParams{
			AccountName:   params.ProjectNumber,
			UUID:          kmsConfig.UUID,
			LocationID:    params.LocationId,
			ProjectNumber: params.ProjectNumber,
		}
		vcpKmsConfig, err := h.Orchestrator.GetKmsConfig(ctx, getKmsConfigParams)
		if err != nil {
			var notFoundErr *errors.NotFoundErr
			if !goErrors.As(err, &notFoundErr) {
				return &gcpgenserver.V1betaListKmsConfigurationsInternalServerError{
					Code:    http.StatusInternalServerError,
					Message: "unknown error during the list kms configurations",
				}, nil
			}
		}

		// Always use VCP state when KMS config exists in VCP DB
		if vcpKmsConfig != nil {
			// Update VCP state with SDE state for ERROR and IN_USE cases
			switch kmsConfig.KmsState {
			case datamodel.LifeCycleStateError, datamodel.LifeCycleStateInUse:
				vcpKmsConfig.State = kmsConfig.KmsState
			}
			operationResponse = append(operationResponse, *convertModelToKmsConfigV1Beta(vcpKmsConfig))
		} else {
			operationResponse = append(operationResponse, *convertToKmsConfigV1beta(kmsConfig))
		}
	}
	return &operationResponse, nil
}

func (h Handler) V1betaUpdateKmsConfiguration(ctx context.Context, req *gcpgenserver.KmsConfigUpdateV1beta, params gcpgenserver.V1betaUpdateKmsConfigurationParams) (gcpgenserver.V1betaUpdateKmsConfigurationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateKmsConfigurationBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	param := &common.UpdateKmsConfigParams{
		KmsConfigID:    params.KmsConfigId,
		AccountName:    params.ProjectNumber,
		ResourceID:     req.ResourceId.Value,
		XCorrelationID: params.XCorrelationID.Value,
		Region:         region,
	}
	if req.Description.IsSet() {
		param.Description = &req.Description.Value
	}

	var URISplits []string
	if !nillable.IsNilOrEmpty(&req.KeyFullPath.Value) {
		// Sample KeyFull Path Value : projects/projectID/locations/us/keyRings/keyRing/cryptoKeys/keyName
		if regex := regexp.MustCompile(uriFormat).MatchString(req.KeyFullPath.Value); !regex {
			return &gcpgenserver.V1betaUpdateKmsConfigurationBadRequest{
				Code:    400,
				Message: "KeyFullPath is not as expected sample : 'projects/projectID/locations/us-east1/keyRings/keyRing/cryptoKeys/keyName'",
			}, nil
		}

		URISplits = strings.Split(req.KeyFullPath.Value, "/")
		param.KeyName = URISplits[7]
		param.KeyRing = URISplits[5]
		param.KeyProjectID = URISplits[1]
		param.KeyRingLocation = URISplits[3]
		param.KeyUri = req.KeyFullPath.Value
	}

	kmsConfig, err := h.Orchestrator.UpdateKmsConfig(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaUpdateKmsConfigurationBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		}
		if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateKmsConfigurationNotFound{
				Code:    http.StatusNotFound,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaUpdateKmsConfigurationConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}
		return categorizeCvpClientErrorsForUpdate(err, logger), nil
	}

	return convertModelToKmsConfigV1Beta(kmsConfig), nil
}

func (h Handler) V1betaGetMultipleKmsConfigs(ctx context.Context, req *gcpgenserver.KmsConfigIdListV1beta, params gcpgenserver.V1betaGetMultipleKmsConfigsParams) (gcpgenserver.V1betaGetMultipleKmsConfigsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	kmsConfigUUIDList := req.KmsConfigIds
	kmsConfigVSAList, vsaErr := h.Orchestrator.GetMultipleKMSConfigs(ctx, kmsConfigUUIDList)

	if vsaErr != nil {
		logger.Error("Get Multiple KMS Configurations API call failed with error", "Error", vsaErr.Error())
		return &gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Unknown error encountered during Get Multiple KMS configurations operation",
		}, nil
	}
	operationResponse := gcpgenserver.V1betaGetMultipleKmsConfigsOK{
		KmsConfigurations: []gcpgenserver.KmsConfigV1beta{},
	}
	for _, kmsConfigVSA := range kmsConfigVSAList {
		operationResponse.KmsConfigurations = append(operationResponse.KmsConfigurations, *convertOrchestratorModelToKmsConfigV1beta(kmsConfigVSA))
	}

	// If CVP is not configured, all configs should live in VCP DB.
	if !utils.IsCVPHostConfigured() {
		return &operationResponse, nil
	}

	// SDE path: for UUIDs not found in VCP, fall back to SDE
	kmsConfigUUIDMissingList := missingKmsConfigIdsInVcp(kmsConfigUUIDList, kmsConfigVSAList)

	// Proceed to call CVP client for all UUIDs
	jwtToken := utils.GetJWTTokenFromContext(ctx)

	body := &models.KmsConfigIDListV1beta{
		KmsConfigIDs: kmsConfigUUIDList,
	}
	getMultipleKmsConfigsParams := &kms_configurations.V1betaGetMultipleKmsConfigsParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
		Body:           body,
	}

	cvpClient := createClient(logger, jwtToken)
	cvpResponse, cvpErr := cvpClient.KmsConfigurations.V1betaGetMultipleKmsConfigs(getMultipleKmsConfigsParams)
	if cvpErr != nil {
		gcpgenserverResponse := categorizeCvpClientErrorsForGetMultipleKmsConfigs(cvpErr, logger)
		return gcpgenserverResponse, nil
	}
	if cvpResponse == nil {
		return &gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Unknown error encountered during Get Multiple KMS configurations operation",
		}, nil
	}
	if cvpResponse.Payload != nil {
		for _, sdeKmsConfig := range cvpResponse.Payload.KmsConfigurations {
			if sdeKmsConfig == nil {
				continue
			}
			// Add the missing UUID KMSConfigs into the final list
			if slices.Contains(kmsConfigUUIDMissingList, sdeKmsConfig.UUID) {
				operationResponse.KmsConfigurations = append(operationResponse.KmsConfigurations, *convertToKmsConfigV1beta(sdeKmsConfig))
			} else {
				// For overlapping UUIDs between VSA and SDE, update the State and State details field
				sdeKmsConfigState := sdeKmsConfig.KmsState
				if sdeKmsConfigState == datamodel.LifeCycleStateInUse || sdeKmsConfigState == datamodel.LifeCycleStateError {
					for i := range operationResponse.KmsConfigurations {
						finalKmsConfig := &operationResponse.KmsConfigurations[i]
						if sdeKmsConfig.UUID != finalKmsConfig.UUID.Value {
							continue
						}
						vcpKmsState, vcpKmsStateOk := finalKmsConfig.KmsState.Get()
						// VCP KMSConfig in Error overrides everything
						if vcpKmsStateOk && vcpKmsState == gcpgenserver.KmsConfigV1betaKmsStateERROR {
							break
						}
						// VCP already IN_USE: only SDE ERROR may override
						if vcpKmsStateOk && vcpKmsState == gcpgenserver.KmsConfigV1betaKmsStateINUSE {
							if sdeKmsConfigState != datamodel.LifeCycleStateError {
								break
							}
						}
						if sdeKmsConfigState == datamodel.LifeCycleStateInUse {
							finalKmsConfig.KmsState = gcpgenserver.NewOptKmsConfigV1betaKmsState(gcpgenserver.KmsConfigV1betaKmsStateINUSE)
						} else {
							finalKmsConfig.KmsState = gcpgenserver.NewOptKmsConfigV1betaKmsState(gcpgenserver.KmsConfigV1betaKmsStateERROR)
							finalKmsConfig.KmsStateDetails = gcpgenserver.NewOptString(sdeKmsConfig.KmsStateDetails)
						}
						break
					}
				}
			}
		}
	}
	// Returning empty list for zero matches is acceptable for GetMultiple API
	return &operationResponse, nil
}

func (h Handler) V1betaEncryptVolumes(ctx context.Context, params gcpgenserver.V1betaEncryptVolumesParams) (gcpgenserver.V1betaEncryptVolumesRes, error) {
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaEncryptVolumesBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	getMultipleKmsParams := gcpgenserver.V1betaGetMultipleKmsConfigsParams{
		ProjectNumber:  params.ProjectNumber,
		LocationId:     params.LocationId,
		XCorrelationID: params.XCorrelationID,
	}
	kmsConfigIdArray := []string{params.KmsConfigId}
	getMultipleKmsBody := gcpgenserver.KmsConfigIdListV1beta{
		KmsConfigIds: kmsConfigIdArray,
	}

	getMultipleKmsResponse, err := h.V1betaGetMultipleKmsConfigs(ctx, &getMultipleKmsBody, getMultipleKmsParams)
	if err != nil {
		return nil, err
	}
	if _, ok := getMultipleKmsResponse.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK); !ok {
		return &gcpgenserver.V1betaEncryptVolumesInternalServerError{
			Code:    500,
			Message: fmt.Sprintf("Unknown error encountered during GetMultipleKmsConfigs with CMEK policy UUID %s", params.KmsConfigId),
		}, nil
	}
	if len(getMultipleKmsResponse.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations) == 0 {
		return &gcpgenserver.V1betaEncryptVolumesBadRequest{
			Code:    404,
			Message: fmt.Sprintf("CMEK policy with UUID %s not found", params.KmsConfigId),
		}, nil
	}

	migrateKmsConfigParams := &common.MigrateKmsConfigParams{
		UUID:          params.KmsConfigId,
		LocationID:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
		AccountName:   params.ProjectNumber,
		State:         string(getMultipleKmsResponse.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[0].KmsState.Value),
	}

	operationID, err := h.Orchestrator.MigrateKmsConfig(ctx, migrateKmsConfigParams)
	if err != nil {
		if errors.IsBadRequestErr(err) {
			return &gcpgenserver.V1betaEncryptVolumesBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaEncryptVolumesConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}
		return &gcpgenserver.V1betaEncryptVolumesInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	if operationID == "" {
		return &gcpgenserver.V1betaEncryptVolumesInternalServerError{
			Code:    500,
			Message: "Job ID not returned by VCP for CMEK policy migration",
		}, nil
	}

	operationV1Beta, err := convertEncryptVolumesToOperationV1Beta(params, operationID)
	if err != nil {
		return nil, err
	}
	return operationV1Beta, nil
}

func convertEncryptVolumesToOperationV1Beta(params gcpgenserver.V1betaEncryptVolumesParams, operationID string) (*gcpgenserver.OperationV1beta, error) {
	encryptStatus := models.EncryptVolumeStatusV1beta{
		UUID:   params.KmsConfigId,
		Status: datamodel.LifeCycleStateUpdating,
	}
	encryptVolume := models.EncryptVolumeV1beta{
		EncryptionStatus: &encryptStatus,
	}

	jsonRaw, err := encodeEncryptVolumeV1beta(&encryptVolume)
	if err != nil {
		return nil, err
	}

	operationResponse := gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
		Response: jsonRaw,
		Done:     gcpgenserver.NewOptBool(false),
	}
	return &operationResponse, nil
}

func (h Handler) V1betaDeleteKmsConfiguration(ctx context.Context, params gcpgenserver.V1betaDeleteKmsConfigurationParams) (gcpgenserver.V1betaDeleteKmsConfigurationRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	region, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeleteKmsConfigurationBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	param := &common.DeleteKmsConfigParams{
		AccountName:    params.ProjectNumber,
		Region:         region,
		XCorrelationID: params.XCorrelationID.Value,
		KmsConfigID:    params.KmsConfigId,
	}

	kmsConfig, jobUUID, err := h.Orchestrator.DeleteKmsConfig(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaDeleteKmsConfigurationBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		} else if errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDeleteKmsConfigurationNotFound{
				Code:    http.StatusNotFound,
				Message: err.Error(),
			}, nil
		} else if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaDeleteKmsConfigurationConflict{
				Code:    http.StatusConflict,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to delete kms configuration", err.Error())
		return &gcpgenserver.V1betaDeleteKmsConfigurationInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	var resp jx.Raw
	if kmsConfig != nil {
		resp, err = encodeKmsConfigV1(convertVcpKmsConfigToKmsConfigV1beta(kmsConfig))
		if err != nil {
			return nil, err
		}
	}

	operationID := utils.GenerateOperationURL(params.ProjectNumber, params.LocationId, jobUUID)
	if kmsConfig != nil && kmsConfig.State == datamodel.LifeCycleStateDeleting {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(false),
	}, nil
}

func convertToKmsConfigCheckV1beta(res *kms_configurations.V1betaCheckKmsConfigOK) *gcpgenserver.KmsConfigCheckV1beta {
	kmsConfigHealthCheckV1beta := gcpgenserver.KmsConfigHealthCheckV1beta{
		IsHealthy:    *res.Payload.KmsConfigHealthCheck.IsHealthy,
		Instructions: gcpgenserver.NewOptString(res.Payload.KmsConfigHealthCheck.Instructions),
	}
	if res.Payload.KmsConfigHealthCheck.HealthError != "" {
		kmsConfigHealthCheckV1beta.HealthError = gcpgenserver.NewOptString(res.Payload.KmsConfigHealthCheck.HealthError)
	}
	return &gcpgenserver.KmsConfigCheckV1beta{
		ServiceAccount:       gcpgenserver.NewOptString(res.Payload.ServiceAccount),
		KmsConfigHealthCheck: gcpgenserver.NewOptKmsConfigHealthCheckV1beta(kmsConfigHealthCheckV1beta),
	}
}

func convertErrorToKmsConfigCheckV1beta(err error, kmsConfig *coremodel.KmsConfig) *gcpgenserver.KmsConfigCheckV1beta {
	serviceAccount := ""
	if kmsConfig.KmsAttributes != nil {
		serviceAccount = kmsConfig.KmsAttributes.GetServiceAccountEmail()
	}
	return &gcpgenserver.KmsConfigCheckV1beta{
		ServiceAccount: gcpgenserver.NewOptString(serviceAccount),
		KmsConfigHealthCheck: gcpgenserver.NewOptKmsConfigHealthCheckV1beta(gcpgenserver.KmsConfigHealthCheckV1beta{
			IsHealthy:    false,
			HealthError:  gcpgenserver.NewOptString(err.Error()),
			Instructions: gcpgenserver.NewOptString(getKmsInstructions(kmsConfig))}),
	}
}

func convertToOperationV1beta(res *models.OperationV1beta) *gcpgenserver.OperationV1beta {
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(res.Name),
		Done: gcpgenserver.NewOptBool(*res.Done),
	}
}

func convertToKmsConfigV1beta(res *models.KmsConfigV1beta) *gcpgenserver.KmsConfigV1beta {
	newState, stateDetails := gcp.ConvertKmsConfigStateV1beta(res.KmsState, res.KmsStateDetails)
	state := gcpgenserver.KmsConfigV1betaKmsState(newState)
	kmsConfigV1beta := &gcpgenserver.KmsConfigV1beta{
		UUID:            gcpgenserver.NewOptString(res.UUID),
		KmsState:        gcpgenserver.NewOptKmsConfigV1betaKmsState(state),
		KmsStateDetails: gcpgenserver.NewOptString(stateDetails),
		CreatedTime:     gcpgenserver.NewOptDateTime(time.Time(res.CreatedTime)),
	}
	if res.Description != nil {
		kmsConfigV1beta.Description = gcpgenserver.NewOptString(*res.Description)
	}
	if res.KeyFullPath != nil {
		kmsConfigV1beta.KeyFullPath = *res.KeyFullPath
	}
	if res.DeletedTime != nil {
		kmsConfigV1beta.DeletedTime = gcpgenserver.NewOptDateTime(time.Time(*res.DeletedTime))
	}
	if res.Instructions != "" {
		kmsConfigV1beta.Instructions = gcpgenserver.NewOptString(res.Instructions)
	}
	if res.ServiceAccountEmail != "" {
		kmsConfigV1beta.ServiceAccountEmail = gcpgenserver.NewOptString(res.ServiceAccountEmail)
	}
	if res.UpdatedTime != nil {
		kmsConfigV1beta.UpdatedTime = gcpgenserver.NewOptDateTime(time.Time(*res.UpdatedTime))
	}
	if res.ResourceID != nil {
		kmsConfigV1beta.ResourceId = gcpgenserver.NewOptString(*res.ResourceID)
	}
	return kmsConfigV1beta
}

func convertOrchestratorModelToKmsConfigV1beta(kmsConfig *coremodel.KmsConfig) *gcpgenserver.KmsConfigV1beta {
	state := gcpgenserver.KmsConfigV1betaKmsState(kmsConfig.State)
	keyFullPath := utils.ParsedKeyFullPathResource{ProjectID: kmsConfig.KeyProjectID,
		Location: kmsConfig.KeyRingLocation, KeyRing: kmsConfig.KeyRing, CryptoKey: kmsConfig.KeyName,
		Grant: kmsConfig.KmsGrant}.StringWithGrant()
	instructions := getKmsInstructions(kmsConfig)

	res := &gcpgenserver.KmsConfigV1beta{
		UUID:            gcpgenserver.NewOptString(kmsConfig.UUID),
		KeyFullPath:     keyFullPath,
		KmsState:        gcpgenserver.NewOptKmsConfigV1betaKmsState(state),
		KmsStateDetails: gcpgenserver.NewOptString(kmsConfig.StateDetails),
		Description:     gcpgenserver.NewOptString(kmsConfig.Description),
		CreatedTime:     gcpgenserver.NewOptDateTime(kmsConfig.CreatedAt),
		UpdatedTime:     gcpgenserver.NewOptDateTime(kmsConfig.UpdatedAt),
		Instructions:    gcpgenserver.NewOptString(instructions),
		ResourceId:      gcpgenserver.NewOptString(kmsConfig.ResourceID),
	}
	if kmsConfig.DeletedAt != nil {
		res.DeletedTime = gcpgenserver.OptDateTime{Value: *kmsConfig.DeletedAt}
	}
	if kmsConfig.KmsAttributes != nil {
		if saEmail := kmsConfig.KmsAttributes.GetServiceAccountEmail(); saEmail != "" {
			res.ServiceAccountEmail = gcpgenserver.NewOptString(saEmail)
		}
	}
	return res
}

func convertVcpKmsConfigToKmsConfigV1beta(res *coremodel.KmsConfig) *gcpgenserver.KmsConfigV1beta {
	state := gcpgenserver.KmsConfigV1betaKmsState(res.State)
	kmsConfigV1beta := &gcpgenserver.KmsConfigV1beta{
		UUID:            gcpgenserver.NewOptString(res.UUID),
		KmsState:        gcpgenserver.NewOptKmsConfigV1betaKmsState(state),
		KmsStateDetails: gcpgenserver.NewOptString(res.StateDetails),
		Description:     gcpgenserver.NewOptString(nillable.GetString(&res.Description, "")),
		CreatedTime:     gcpgenserver.NewOptDateTime(time.Time(res.CreatedAt)),
		UpdatedTime:     gcpgenserver.NewOptDateTime(time.Time(res.UpdatedAt)),
	}

	KeyFullPath := utils.ParsedKeyFullPathResource{ProjectID: res.KeyProjectID,
		Location: res.KeyRingLocation, KeyRing: res.KeyRing, CryptoKey: res.KeyName,
		Grant: res.KmsGrant}.StringWithGrant()
	if KeyFullPath != "" {
		kmsConfigV1beta.KeyFullPath = KeyFullPath
	}
	if res.DeletedAt != nil {
		kmsConfigV1beta.DeletedTime = gcpgenserver.NewOptDateTime(time.Time(*res.DeletedAt))
	}
	if res.Name != "" {
		kmsConfigV1beta.ResourceId = gcpgenserver.NewOptString(res.Name)
	}
	return kmsConfigV1beta
}

func getKmsInstructions(kmsConfig *coremodel.KmsConfig) (instructions string) {
	if kmsConfig.KmsAttributes == nil {
		return ""
	}

	saEmail := kmsConfig.KmsAttributes.GetServiceAccountEmail()
	if saEmail == "" {
		return ""
	}

	keyProjectID := kmsConfig.KeyProjectID
	if keyProjectID == "" {
		keyProjectID = kmsConfig.CustomerProjectID
	}

	return fmt.Sprintf(`Please copy and paste the commands listed below into Google Cloud Shell in the project that contains the key ring. The commands create a KMS role and assign it to the CVS service account so that it can access the key.
## CREATE KMS role ## gcloud iam roles create %[1]s --project=%[2]s --title='%[1]s' --description='custom cmek cvs role' --permissions=cloudkms.cryptoKeyVersions.get,cloudkms.cryptoKeyVersions.list,cloudkms.cryptoKeyVersions.useToDecrypt,cloudkms.cryptoKeyVersions.useToEncrypt,cloudkms.cryptoKeys.get,cloudkms.keyRings.get,cloudkms.locations.get,cloudkms.locations.list,resourcemanager.projects.get --stage=GA
 ## ASSIGN role and give KEY ACCESS to CVS service account ## gcloud kms keys add-iam-policy-binding %[3]s --project=%[2]s --keyring %[4]s --location %[5]s --member serviceAccount:%[6]s --role projects/%[2]s/roles/%[1]s`, roleName, keyProjectID, kmsConfig.KeyName, kmsConfig.KeyRing, kmsConfig.KeyRingLocation, saEmail)
}

func missingKmsConfigIdsInVcp(kmsConfigUUIDList []string, kmsConfigVSAList []*coremodel.KmsConfig) []string {
	var kmsConfigUUIDMissingList []string

	if kmsConfigVSAList != nil {
		// Create map from id of kmsConfigStruct Array
		kmsConfigUUIDMap := make(map[string]string)
		for _, kmsConfig := range kmsConfigVSAList {
			kmsConfigUUIDMap[kmsConfig.UUID] = ""
		}
		// Iterate through UUID List, find missing ones, and append to Missing list
		for _, uuid := range kmsConfigUUIDList {
			if _, exists := kmsConfigUUIDMap[uuid]; !exists {
				kmsConfigUUIDMissingList = append(kmsConfigUUIDMissingList, uuid)
			}
		}
	} else {
		kmsConfigUUIDMissingList = kmsConfigUUIDList
	}
	return kmsConfigUUIDMissingList
}

func categorizeCvpClientErrorsForGetMultipleKmsConfigs(cvpErr error, logger log.Logger) gcpgenserver.V1betaGetMultipleKmsConfigsRes {
	getMsg := func(msg *string) string {
		return nillable.GetString(msg, "")
	}
	getCode := func(floatVal *float64) float64 {
		return nillable.GetFloat64(floatVal, 0)
	}
	switch e := cvpErr.(type) {
	case *kms_configurations.V1betaGetMultipleKmsConfigsBadRequest:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsBadRequest{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaGetMultipleKmsConfigsUnauthorized:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsUnauthorized{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaGetMultipleKmsConfigsForbidden:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsForbidden{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaGetMultipleKmsConfigsNotFound:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsNotFound{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaGetMultipleKmsConfigsTooManyRequests:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsTooManyRequests{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaGetMultipleKmsConfigsDefault:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	default:
		return &gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Unknown error encountered during Get Multiple KMS configurations operation",
		}
	}
}

func categorizeCvpClientErrorsForUpdate(cvpErr error, logger log.Logger) gcpgenserver.V1betaUpdateKmsConfigurationRes {
	getMsg := func(msg *string) string {
		return nillable.GetString(msg, "")
	}
	getCode := func(floatVal *float64) float64 {
		return nillable.GetFloat64(floatVal, 0)
	}
	switch e := cvpErr.(type) {
	case *kms_configurations.V1betaUpdateKmsConfigurationBadRequest:
		return &gcpgenserver.V1betaUpdateKmsConfigurationBadRequest{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaUpdateKmsConfigurationUnauthorized:
		return &gcpgenserver.V1betaUpdateKmsConfigurationUnauthorized{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaUpdateKmsConfigurationForbidden:
		return &gcpgenserver.V1betaUpdateKmsConfigurationForbidden{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaUpdateKmsConfigurationNotFound:
		return &gcpgenserver.V1betaUpdateKmsConfigurationNotFound{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaUpdateKmsConfigurationConflict:
		return &gcpgenserver.V1betaUpdateKmsConfigurationConflict{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	case *kms_configurations.V1betaUpdateKmsConfigurationInternalServerError:
		return &gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError{
			Code:    getCode(&e.Payload.Code),
			Message: getMsg(&e.Payload.Message),
		}
	default:
		return &gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Unknown error encountered during Update KMS configurations operation",
		}
	}
}

// encodeKmsConfigV1 encodes a KmsConfigV1 struct to JSON.
func encodeKmsConfigV1(kmsConfigV1beta *gcpgenserver.KmsConfigV1beta) (jx.Raw, error) {
	data, err := json.Marshal(kmsConfigV1beta)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func _encodeEncryptVolumeV1beta(encryptVolumeV1beta *models.EncryptVolumeV1beta) (jx.Raw, error) {
	data, err := json.Marshal(encryptVolumeV1beta)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// convertModelToKmsConfigV1Beta converts a vsaModel.KmsConfig to gcpgenserver.KmsConfigV1beta
func convertModelToKmsConfigV1Beta(kmsConfig *coremodel.KmsConfig) *gcpgenserver.KmsConfigV1beta {
	state, stateDetails := gcp.ConvertKmsConfigStateV1beta(kmsConfig.State, kmsConfig.StateDetails)
	model := &gcpgenserver.KmsConfigV1beta{
		UUID:            gcpgenserver.NewOptString(kmsConfig.UUID),
		KmsState:        gcpgenserver.NewOptKmsConfigV1betaKmsState(gcpgenserver.KmsConfigV1betaKmsState(state)),
		KmsStateDetails: gcpgenserver.NewOptString(stateDetails),
		KeyFullPath: utils.ParsedKeyFullPathResource{ProjectID: kmsConfig.KeyProjectID,
			KeyRing: kmsConfig.KeyRing, Location: kmsConfig.KeyRingLocation, CryptoKey: kmsConfig.KeyName,
			Grant: kmsConfig.KmsGrant}.StringWithGrant(),
		ResourceId:  gcpgenserver.NewOptString(kmsConfig.ResourceID),
		CreatedTime: gcpgenserver.NewOptDateTime(kmsConfig.CreatedAt),
		UpdatedTime: gcpgenserver.NewOptDateTime(kmsConfig.UpdatedAt),
	}
	if saEmail := kmsConfig.KmsAttributes.GetServiceAccountEmail(); saEmail != "" {
		model.ServiceAccountEmail = gcpgenserver.NewOptString(saEmail)
	}
	if kmsConfig.Description != "" {
		model.Description = gcpgenserver.NewOptString(kmsConfig.Description)
	}
	instruction := getKmsInstructions(kmsConfig)
	if instruction != "" {
		model.Instructions = gcpgenserver.NewOptString(instruction)
	}
	return model
}

func _parseKmsConfigResponse(payloadResponse interface{}) (*models.KmsConfigV1beta, error) {
	var cvpResponse models.KmsConfigV1beta

	// Marshal the response field back to JSON
	responseJSON, err := json.Marshal(payloadResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload response: %w", err)
	}

	// Unmarshal the JSON back into the KmsConfigV1beta struct
	err = json.Unmarshal(responseJSON, &cvpResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON to KmsConfigV1beta: %w", err)
	}

	return &cvpResponse, nil
}

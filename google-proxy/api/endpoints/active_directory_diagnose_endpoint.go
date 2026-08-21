// Active Directory diagnose endpoint - POST .../pools/{poolId}/directoryService.
//
// Behavior:
//   - Gated by the AD_DIAGNOSTIC_ENABLED feature flag (default false); when off,
//     returns 400 with the disabled-API message.
//   - Validates req.Action == connectivityCheck and req.ResourceType == AD;
//     resourceType == LDAP is rejected with a 400 (SDE parity).
//   - If the pool is not in the VCP DB and CVP_HOST is configured, forwards the
//     request to CVP and converts the response into the gcpgenserver shape.
//   - Otherwise delegates to Orchestrator.DiagnoseActiveDirectory and returns an
//     Operation_v1beta with a compact Diagnosis payload (SDE/CVP shape).
//   - Error mapping: 400 for input-validation/bad-request, 404 for not-found,
//     500 otherwise. CVP-forwarded errors preserve CVP status codes.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/pools"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// adDiagnosticEnabled gates the entire diagnose endpoint. Default false; set
// AD_DIAGNOSTIC_ENABLED=true to enable.
var adDiagnosticEnabled = env.GetBool("AD_DIAGNOSTIC_ENABLED", false)

const (
	adDiagnosisDisabledMessage  = "directory service diagnosis API is disabled"
	adDiagnosisLDAPNotSupported = "Non-AD directory service diagnosis is not supported"
	adDiagnosisErrorPrefix      = "Error diagnosing directory service"

	// SDE API job/action names for directory service diagnosis (CVS Diagnosis_v2 / Job_v2).
	diagnoseDirectoryServicesAction = "diagnoseDirectoryServices"
	diagnoseJobObjectType           = "ActiveDirectory"
	diagnoseJobStatePendingDetails  = "Job is pending"
	diagnoseJobStateOngoing         = "ongoing"
)

// directoryServiceDiagnosisV1 mirrors CVS models.DiagnosisV2 / CVP Operation response.
type directoryServiceDiagnosisV1 struct {
	PoolID            string                           `json:"poolId"`
	ActiveDirectoryID string                           `json:"activeDirectoryId"`
	LifeCycleState    string                           `json:"lifeCycleState"`
	Jobs              []directoryServiceDiagnosisJobV1 `json:"jobs"`
}

type directoryServiceDiagnosisJobV1 struct {
	Action       string `json:"action"`
	Created      string `json:"created"`
	JobID        string `json:"jobId"`
	ObjectID     string `json:"objectId"`
	ObjectType   string `json:"objectType"`
	State        string `json:"state"`
	StateDetails string `json:"stateDetails"`
}

// V1betaDirectoryServiceDiagnosis implements the directoryService endpoint
// declared in the generated server interface (gcp-servergen/oas_server_gen.go).
func (h Handler) V1betaDirectoryServiceDiagnosis(
	ctx context.Context,
	req *gcpgenserver.PoolConnectivityCheckV1beta,
	params gcpgenserver.V1betaDirectoryServiceDiagnosisParams,
) (gcpgenserver.V1betaDirectoryServiceDiagnosisRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	if !adDiagnosticEnabled {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    http.StatusBadRequest,
			Message: adDiagnosisDisabledMessage,
		}, nil
	}

	if req == nil {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    http.StatusBadRequest,
			Message: "request body is required",
		}, nil
	}
	if req.Action != gcpgenserver.PoolConnectivityCheckV1betaActionConnectivityCheck {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    http.StatusBadRequest,
			Message: "action must be \"connectivityCheck\"",
		}, nil
	}
	if req.ResourceType != gcpgenserver.PoolConnectivityCheckV1betaResourceTypeAD {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    http.StatusBadRequest,
			Message: adDiagnosisLDAPNotSupported,
		}, nil
	}

	// Native path when the pool exists in the VCP DB; otherwise, if CVP_HOST is
	// configured, fall back to CVP (same pattern as V1betaDescribeOperation).
	if _, err := h.Orchestrator.DescribePool(ctx, params.PoolId, params.ProjectNumber); err != nil {
		if errors.IsNotFoundErr(err) {
			if cvp.CVP_HOST != "" {
				return h.diagnoseDirectoryServiceViaCVP(ctx, req, params)
			}
			return &gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound{
				Code:    http.StatusNotFound,
				Message: adDiagnosisErrorPrefix + " - Pool '" + params.PoolId + "' not found",
			}, nil
		}
		logger.Errorf("DescribePool failed during AD diagnose: %v", err)
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}

	orchParams := &commonparams.DiagnoseActiveDirectoryParams{
		ProjectNumber:  params.ProjectNumber,
		LocationId:     params.LocationId,
		PoolUUID:       params.PoolId,
		XCorrelationId: params.XCorrelationID.Value,
		ResourceType:   string(req.ResourceType),
	}

	ad, jobUUID, err := h.Orchestrator.DiagnoseActiveDirectory(ctx, orchParams)
	if err != nil {
		return mapDiagnoseErrorToResponse(err), nil
	}

	resp, encErr := encodeDirectoryServiceDiagnosisV1(
		buildDirectoryServiceDiagnosisV1(params.PoolId, ad, jobUUID),
	)
	if encErr != nil {
		logger.Errorf("Failed to encode directory service diagnosis response: %v", encErr)
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: encErr.Error(),
		}, nil
	}

	operationID := "/v1beta/projects/" + params.ProjectNumber +
		"/locations/" + params.LocationId +
		"/operations/" + jobUUID

	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(false),
	}, nil
}

// buildDirectoryServiceDiagnosisV1 builds the compact Diagnosis payload returned in
// Operation.response for directory service diagnosis (SDE/CVP parity).
func buildDirectoryServiceDiagnosisV1(
	poolID string,
	ad *coremodels.ActiveDirectory,
	jobUUID string,
) directoryServiceDiagnosisV1 {
	jobCreated := ad.UpdatedAt
	if jobCreated.IsZero() {
		jobCreated = ad.CreatedAt
	}
	if jobCreated.IsZero() {
		jobCreated = time.Now().UTC()
	}

	return directoryServiceDiagnosisV1{
		PoolID:            poolID,
		ActiveDirectoryID: ad.UUID,
		LifeCycleState:    strings.ToLower(ad.State),
		Jobs: []directoryServiceDiagnosisJobV1{
			{
				Action:       diagnoseDirectoryServicesAction,
				Created:      formatDiagnosisJobCreated(jobCreated),
				JobID:        jobUUID,
				ObjectID:     ad.UUID,
				ObjectType:   diagnoseJobObjectType,
				State:        diagnoseJobStateOngoing,
				StateDetails: diagnoseJobStatePendingDetails,
			},
		},
	}
}

// formatDiagnosisJobCreated returns a standard RFC3339 UTC timestamp for the
// diagnosis job "created" field. Use time.RFC3339 only — custom layouts such as
// "2006-01-02T15:04:05.000Z07:00" are invalid because the literal "Z" is
// emitted alongside the numeric offset (e.g. "...Z+00:00").
func formatDiagnosisJobCreated(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func encodeDirectoryServiceDiagnosisV1(
	diagnosis directoryServiceDiagnosisV1,
) (jx.Raw, error) {
	data, err := json.Marshal(diagnosis)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// diagnoseDirectoryServiceViaCVP forwards a diagnose request to CVP when the
// pool lives in SDE (not in VCP DB). Mirrors how createActiveDirectorySyncViaCVP
// builds the CVP request and how the operations endpoint converts CVP results
// back into the gcpgenserver shape.
func (h Handler) diagnoseDirectoryServiceViaCVP(
	ctx context.Context,
	req *gcpgenserver.PoolConnectivityCheckV1beta,
	params gcpgenserver.V1betaDirectoryServiceDiagnosisParams,
) (gcpgenserver.V1betaDirectoryServiceDiagnosisRes, error) {
	logger := util.GetLogger(ctx)

	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	action := string(req.Action)
	resourceType := string(req.ResourceType)
	body := &cvpModels.PoolConnectivityCheckV1beta{
		Action:       &action,
		ResourceType: &resourceType,
	}

	cvpParams := pools.NewV1betaDirectoryServiceDiagnosisParams()
	cvpParams.LocationID = params.LocationId
	cvpParams.ProjectNumber = params.ProjectNumber
	cvpParams.PoolID = params.PoolId
	corrID := params.XCorrelationID.Value
	cvpParams.XCorrelationID = &corrID
	cvpParams.Body = body

	resp, err := cvpClient.Pools.V1betaDirectoryServiceDiagnosis(cvpParams)
	if err != nil {
		return mapCVPDiagnoseErrorToResponse(err), nil
	}
	if resp == nil || resp.Payload == nil {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "unknown error during the directory service diagnosis",
		}, nil
	}

	return convertOperationToOperationV1Beta(resp.Payload), nil
}

// mapDiagnoseErrorToResponse maps an orchestrator error into the appropriate
// V1betaDirectoryServiceDiagnosisRes variant: 400 for input-validation /
// bad-request errors, 404 for not-found, and 500 for everything else.
func mapDiagnoseErrorToResponse(err error) gcpgenserver.V1betaDirectoryServiceDiagnosisRes {
	prefix := adDiagnosisErrorPrefix + " - "
	if errors.IsUserInputValidationErr(err) || errors.IsBadRequestErr(err) {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    http.StatusBadRequest,
			Message: prefix + err.Error(),
		}
	}
	if errors.IsNotFoundErr(err) {
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound{
			Code:    http.StatusNotFound,
			Message: prefix + err.Error(),
		}
	}
	return &gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError{
		Code:    http.StatusInternalServerError,
		Message: prefix + err.Error(),
	}
}

// mapCVPDiagnoseErrorToResponse converts a CVP pools.V1betaDirectoryServiceDiagnosis
// error into the gcpgenserver response variant. Type-switches on the generated
// go-swagger error types so the HTTP status code from CVP is preserved.
func mapCVPDiagnoseErrorToResponse(err error) gcpgenserver.V1betaDirectoryServiceDiagnosisRes {
	switch e := err.(type) {
	case *pools.V1betaDirectoryServiceDiagnosisBadRequest:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisBadRequest{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	case *pools.V1betaDirectoryServiceDiagnosisNotFound:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisNotFound{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	case *pools.V1betaDirectoryServiceDiagnosisConflict:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisConflict{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	case *pools.V1betaDirectoryServiceDiagnosisUnprocessableEntity:
		return &gcpgenserver.ErrorStatusCode{
			StatusCode: http.StatusUnprocessableEntity,
			Response: gcpgenserver.Error{
				Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
				Message: nillable.GetString(&e.Payload.Message, ""),
			},
		}
	case *pools.V1betaDirectoryServiceDiagnosisUnauthorized:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisUnauthorized{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	case *pools.V1betaDirectoryServiceDiagnosisForbidden:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisForbidden{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	case *pools.V1betaDirectoryServiceDiagnosisTooManyRequests:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisTooManyRequests{
			Code:    float64(nillable.GetFloat64(&e.Payload.Code, 0)),
			Message: nillable.GetString(&e.Payload.Message, ""),
		}
	default:
		return &gcpgenserver.V1betaDirectoryServiceDiagnosisInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}
	}
}

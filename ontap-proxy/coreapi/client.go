package coreapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	coreapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/core-api"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	ontaputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

var (
	coreAPIHost         = env.GetString("CORE_API_HOST", "")
	createCoreAPIClient = coreapi.GetCoreAPIClient
)

func FetchCredentials(ctx context.Context, poolDetails *models.PoolDetails, jwtToken string, logger log.Logger) (*coreapi.OntapCredentialsV1, error) {
	logger.DebugContext(ctx, "Fetching credentials from Core API",
		"poolID", poolDetails.PoolID,
		"accountName", poolDetails.AccountName,
		"userName", poolDetails.UserName)

	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)

	params := coreapi.V1GetOntapCredentialsParams{
		PoolId:      poolDetails.PoolID,
		AccountName: coreapi.NewOptString(poolDetails.AccountName),
		UserName:    coreapi.NewOptString(poolDetails.UserName),
	}
	// Forward the x-netapp-cluster-name header to Core for dedicated-capacity pools.
	if strings.TrimSpace(poolDetails.ClusterName) != "" {
		params.XNetappClusterName = coreapi.NewOptString(poolDetails.ClusterName)
	}

	response, err := client.Invoker.V1GetOntapCredentials(ctx, params)
	if err != nil {
		logger.ErrorContext(ctx, "Core API call failed", "error", err, "poolID", poolDetails.PoolID)
		return nil, &ontaputils.HTTPError{Status: http.StatusServiceUnavailable, Message: "Service unavailable"}
	}

	switch resp := response.(type) {
	case *coreapi.OntapCredentialsV1:
		// Marshal the full response to JSON for logging
		respJSON, err := json.Marshal(resp)
		respJSONStr := ""
		if err != nil {
			respJSONStr = fmt.Sprintf("<error marshaling response: %v>", err)
		} else {
			respJSONStr = string(respJSON)
		}

		logger.InfoContext(ctx, "Core API credentials fetched", "poolID", poolDetails.PoolID)
		logger.DebugContext(ctx, "Core API credential response",
			"poolID", poolDetails.PoolID,
			"authType", resp.AuthType.Value,
			"caURI", getStringValue(resp.CaURI),
			"fullResponse", log.Sanitize(respJSONStr))
		return resp, nil

	case *coreapi.V1GetOntapCredentialsNotFound:
		logger.ErrorContext(ctx, "Pool not found", "poolID", poolDetails.PoolID, "message", resp.Message)
		return nil, &ontaputils.HTTPError{Status: http.StatusNotFound, Message: "Pool not found"}

	case *coreapi.V1GetOntapCredentialsBadRequest:
		logger.ErrorContext(ctx, "Invalid pool details", "poolID", poolDetails.PoolID, "message", resp.Message)
		msg := strings.TrimSpace(resp.Message)
		if msg == "" {
			msg = "Invalid pool details"
		}
		return nil, &ontaputils.HTTPError{Status: http.StatusBadRequest, Message: msg}

	case *coreapi.V1GetOntapCredentialsUnauthorized:
		logger.ErrorContext(ctx, "Unauthorized access", "poolID", poolDetails.PoolID, "message", resp.Message)
		return nil, &ontaputils.HTTPError{Status: http.StatusUnauthorized, Message: "Unauthorized access"}

	case *coreapi.V1GetOntapCredentialsForbidden:
		logger.ErrorContext(ctx, "Forbidden access", "poolID", poolDetails.PoolID, "message", resp.Message)
		return nil, &ontaputils.HTTPError{Status: http.StatusForbidden, Message: "Forbidden access"}

	case *coreapi.V1GetOntapCredentialsInternalServerError:
		logger.ErrorContext(ctx, "Internal server error", "poolID", poolDetails.PoolID, "message", resp.Message)
		return nil, &ontaputils.HTTPError{Status: http.StatusInternalServerError, Message: "Internal server error"}

	default:
		logger.ErrorContext(ctx, "Unexpected response from Core API", "responseType", fmt.Sprintf("%T", resp), "poolID", poolDetails.PoolID)
		return nil, &ontaputils.HTTPError{Status: http.StatusInternalServerError, Message: "Internal server error"}
	}
}

// getStringValue safely extracts string value from OptString, returning empty string if not set
func getStringValue(opt coreapi.OptString) string {
	if opt.IsSet() {
		return opt.Value
	}
	return ""
}

// SubmitExpertModeVolumeOperation submits a volume operation (Create/Update/Delete) to the Core API.
func SubmitExpertModeVolumeOperation(ctx context.Context, request *coreapi.ExpertModeVolumeV1, jwtToken string, logger log.Logger) error {
	logger.DebugContext(ctx, "Submitting expert mode volume operation",
		"projectNumber", request.ProjectNumber,
		"poolUUID", request.PoolUUID,
		"volumeName", request.VolumeName,
		"action", request.Action,
		"style", request.Style)

	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)
	correlationID, _ := ctx.Value(middleware.CorrelationContextKey).(string)
	params := coreapi.V1ExpertModeVolumeParams{}
	if correlationID != "" {
		params.XCorrelationID = coreapi.NewOptString(correlationID)
	}

	response, err := client.Invoker.V1ExpertModeVolume(ctx, request, params)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to submit expert mode volume operation",
			"error", err,
			"volumeName", request.VolumeName,
			"poolUUID", request.PoolUUID,
			"action", request.Action)
		return err
	}

	// Handle different response types
	switch resp := response.(type) {
	case *coreapi.V1ExpertModeVolumeOK:
		logger.InfoContext(ctx, "Expert mode volume operation submitted",
			"poolUUID", request.PoolUUID,
			"action", request.Action)
		logger.DebugContext(ctx, "Expert mode volume operation submitted details",
			"volumeName", request.VolumeName,
			"projectNumber", request.ProjectNumber,
			"style", request.Style)
		return nil

	case *coreapi.V1ExpertModeVolumeBadRequest:
		logger.ErrorContext(ctx, "Bad request when submitting expert mode volume operation",
			"errorMessage", resp.Message,
			"volumeName", request.VolumeName,
			"action", request.Action)
		return customerrors.NewBadRequestErr(fmt.Sprintf("bad request: %s", resp.Message))

	case *coreapi.V1ExpertModeVolumeConflict:
		logger.ErrorContext(ctx, "Conflict when submitting expert mode volume operation",
			"errorMessage", resp.Message,
			"volumeName", request.VolumeName,
			"action", request.Action)
		return customerrors.NewConflictErr(fmt.Sprintf("conflict: %s", resp.Message))

	default:
		logger.ErrorContext(ctx, "Unexpected response from Core API",
			"responseType", fmt.Sprintf("%T", resp),
			"volumeName", request.VolumeName)
		return err
	}
}

// SubmitExpertModeVolumeRename submits a volume rename to the Core API.
// Params.Name is the current volume name (path); request.Name is the new name.
func SubmitExpertModeVolumeRename(ctx context.Context, request *coreapi.ExpertModeVolumeRenameV1, params coreapi.V1ExpertModeVolumeRenameParams, jwtToken string, logger log.Logger) error {
	logger.DebugContext(ctx, "Submitting expert mode volume rename",
		"projectNumber", request.ProjectNumber,
		"poolUUID", request.PoolUUID,
		"currentName", params.Name,
		"newName", request.Name)

	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)
	correlationID, _ := ctx.Value(middleware.CorrelationContextKey).(string)
	if correlationID != "" {
		params.XCorrelationID = coreapi.NewOptString(correlationID)
	}

	response, err := client.Invoker.V1ExpertModeVolumeRename(ctx, request, params)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to submit expert mode volume rename",
			"error", err,
			"currentName", params.Name,
			"newName", request.Name)
		return err
	}

	switch resp := response.(type) {
	case *coreapi.V1ExpertModeVolumeRenameOK:
		logger.InfoContext(ctx, "Expert mode volume rename submitted", "poolUUID", request.PoolUUID)
		logger.DebugContext(ctx, "Expert mode volume rename submitted details",
			"currentName", params.Name,
			"newName", request.Name)
		return nil

	case *coreapi.V1ExpertModeVolumeRenameBadRequest:
		logger.ErrorContext(ctx, "Bad request when submitting expert mode volume rename",
			"errorMessage", resp.Message, "currentName", params.Name)
		return customerrors.NewBadRequestErr(fmt.Sprintf("bad request: %s", resp.Message))

	case *coreapi.V1ExpertModeVolumeRenameConflict:
		logger.ErrorContext(ctx, "Conflict when submitting expert mode volume rename",
			"errorMessage", resp.Message, "currentName", params.Name)
		return customerrors.NewConflictErr(fmt.Sprintf("conflict: %s", resp.Message))

	case *coreapi.V1ExpertModeVolumeRenameNotFound:
		logger.ErrorContext(ctx, "Volume not found when submitting expert mode volume rename",
			"errorMessage", resp.Message, "currentName", params.Name)
		return fmt.Errorf("volume not found: %s", resp.Message)

	default:
		logger.ErrorContext(ctx, "Unexpected response from Core API",
			"responseType", fmt.Sprintf("%T", resp),
			"currentName", params.Name)
		return err
	}
}

// SubmitExpertModeFlexCloneSplit starts expert-mode FlexClone split for a volume
// identified by UUID and/or name in the request body.
func SubmitExpertModeFlexCloneSplit(ctx context.Context, volumeUUID, volumeName, projectNumber, poolUUID, jwtToken string, logger log.Logger) error {
	logger.InfoContext(ctx, "Submitting expert mode flexclone split",
		"volumeUUID", volumeUUID,
		"volumeName", volumeName,
		"projectNumber", projectNumber,
		"poolUUID", poolUUID)

	client := createCoreAPIClient(coreAPIHost, jwtToken, logger)
	request := &coreapi.ExpertModeVolumeFlexCloneSplitV1{
		ProjectNumber: projectNumber,
		PoolUUID:      poolUUID,
	}
	if volumeUUID != "" {
		request.VolumeUUID = coreapi.NewOptString(volumeUUID)
	}
	if volumeName != "" {
		request.VolumeName = coreapi.NewOptString(volumeName)
	}
	params := coreapi.V1ExpertModeVolumeFlexCloneSplitParams{}
	if correlationID, _ := ctx.Value(middleware.CorrelationContextKey).(string); correlationID != "" {
		params.XCorrelationID = coreapi.NewOptString(correlationID)
	}

	response, err := client.Invoker.V1ExpertModeVolumeFlexCloneSplit(ctx, request, params)
	if err != nil {
		logger.ErrorContext(ctx, "Failed to submit expert mode flexclone split",
			"error", err,
			"volumeUUID", volumeUUID,
			"volumeName", volumeName,
			"poolUUID", poolUUID)
		return err
	}

	switch resp := response.(type) {
	case *coreapi.V1ExpertModeVolumeFlexCloneSplitAccepted:
		logger.InfoContext(ctx, "Successfully submitted expert mode flexclone split",
			"volumeUUID", volumeUUID,
			"volumeName", volumeName,
			"poolUUID", poolUUID)
		return nil

	case *coreapi.V1ExpertModeVolumeFlexCloneSplitBadRequest:
		logger.ErrorContext(ctx, "Bad request when submitting expert mode flexclone split",
			"errorMessage", resp.Message, "volumeUUID", volumeUUID, "volumeName", volumeName)
		return customerrors.NewBadRequestErr(fmt.Sprintf("bad request: %s", resp.Message))

	default:
		logger.ErrorContext(ctx, "Unexpected response from Core API",
			"responseType", fmt.Sprintf("%T", resp),
			"volumeUUID", volumeUUID,
			"volumeName", volumeName)
		return fmt.Errorf("unexpected response from Core API")
	}
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-faster/jx"
	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backups"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	backupEnabled                     = env.GetBool("BACKUP_ENABLED", false)
	utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
	listBackupsToCVP                  = _listBackupsToCVP
	getBackupsFromCVP                 = _getBackupsFromCVP
	checkIfBackupExistInCVP           = _checkIfBackupExistInCVP
	ONTAPMode                         = "ONTAP"
	ExpertModeBackupEnabled           = env.GetBool("EXPERT_MODE_BACKUP_ENABLED", false)
)

func (h Handler) V1betaGetMultipleBackups(ctx context.Context, req *gcpgenserver.BackupUuidListV1beta, params gcpgenserver.V1betaGetMultipleBackupsParams) (gcpgenserver.V1betaGetMultipleBackupsRes, error) {
	logger := util.GetLogger(ctx)
	if !backupEnabled {
		return &gcpgenserver.V1betaGetMultipleBackupsBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	listBackups, err := h.Orchestrator.GetBackupsUnderBackupVault(ctx, params.BackupVaultId, params.ProjectNumber, req.BackupUuids)
	if err != nil && errors.IsUserInputValidationErr(err) {
		return &gcpgenserver.V1betaGetMultipleBackupsBadRequest{
			Code:    400,
			Message: err.Error(),
		}, nil
	}
	// If err is not nil and it is not a NotFound error, return an internal server error
	// For NotFoundErr we will return the list from CVP
	if err != nil && !errors.IsNotFoundErr(err) {
		return &gcpgenserver.V1betaGetMultipleBackupsInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	// Need to fetch the UUIDs not found in VCP to CVP
	uuids := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, req.BackupUuids)
	operationResponse := gcpgenserver.V1betaGetMultipleBackupsOK{
		Backups: []gcpgenserver.BackupV1beta{},
	}
	if len(uuids) != 0 && utils.IsCVPHostConfigured() {
		jwtToken := utils.GetJWTTokenFromContext(ctx)
		cvpClient := createClient(logger, jwtToken)

		var backupUUIDs []string
		backupUUIDs = append(backupUUIDs, uuids...)

		body := &models.BackupUUIDListV1beta{
			BackupUUIDs: backupUUIDs,
		}
		getMultipleBackupParams := &backups.V1betaGetMultipleBackupsParams{
			BackupVaultID:  params.BackupVaultId,
			Body:           body,
			LocationID:     params.LocationId,
			ProjectNumber:  params.ProjectNumber,
			XCorrelationID: &params.XCorrelationID.Value,
		}

		resp, err := cvpClient.Backups.V1betaGetMultipleBackups(getMultipleBackupParams)
		if err != nil {
			switch e := err.(type) {
			case *backups.V1betaGetMultipleBackupsBadRequest:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsBadRequest{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaGetMultipleBackupsUnauthorized:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsUnauthorized{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaGetMultipleBackupsForbidden:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsForbidden{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaGetMultipleBackupsNotFound:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsNotFound{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaGetMultipleBackupsTooManyRequests:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsTooManyRequests{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaGetMultipleBackupsNotImplemented:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaGetMultipleBackupsNotImplemented{
					Code:    code,
					Message: msg,
				}, nil
			default:
				return &gcpgenserver.V1betaGetMultipleBackupsInternalServerError{
					Code:    500,
					Message: err.Error(),
				}, nil
			}
		}
		if resp != nil && resp.Payload != nil {
			for _, bp := range resp.Payload.Backups {
				operationResponse.Backups = append(operationResponse.Backups, convertToBackupsV1beta(bp))
			}
		}
	}
	for _, bp := range listBackups {
		operationResponse.Backups = append(operationResponse.Backups, convertBackupDataModelToBackupsV1beta(bp))
	}
	return &operationResponse, nil
}

// V1betaCreateBackup creates a backup for a given volume.
func (h Handler) V1betaCreateBackup(ctx context.Context, req *gcpgenserver.BackupCreateV1beta, params gcpgenserver.V1betaCreateBackupParams) (gcpgenserver.V1betaCreateBackupRes, error) {
	logger := util.GetLogger(ctx)
	if !backupEnabled {
		return &gcpgenserver.V1betaCreateBackupBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := utils.ParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateBackupBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	var fetchFromCVP bool

	var isExpertMode bool
	var err error

	vcpBv, err := h.Orchestrator.GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId)
	if err != nil && !errors.IsNotFoundErr(err) {
		logger.Error("Failed to get backup vault from VCP", "error", err.Error())
		return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, nil
	}

	backupVaultServiceType := "GCNV"
	isGCBDRVault := false
	if vcpBv != nil && vcpBv.ServiceType != "" {
		backupVaultServiceType = vcpBv.ServiceType
		isGCBDRVault = backupVaultServiceType == datamodel.ServiceTypeCrossProject
	}

	// For non-GCBDR vaults found in VCP, validate that vault belongs to the requesting account
	if !isGCBDRVault && vcpBv != nil && vcpBv.AccountName != params.ProjectNumber {
		msg := fmt.Sprintf("Backup vault %s does not belong to account %s", params.BackupVaultId, params.ProjectNumber)
		logger.Error(msg)
		return &gcpgenserver.V1betaCreateBackupBadRequest{
			Code:    400,
			Message: msg,
		}, nil
	}

	// Check if poolID is provided
	if req.PoolId.IsSet() && req.PoolId.Value != "" {
		pool, err := h.Orchestrator.DescribePool(ctx, req.PoolId.Value, params.ProjectNumber)
		if err != nil {
			logger.Error("Failed to get pool", "error", err.Error())
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    400,
					Message: fmt.Sprintf("Pool with ID %s not found", req.PoolId.Value),
				}, nil
			}
			return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, nil
		}
		isExpertMode = pool.APIAccessMode == ONTAPMode
	}
	if isExpertMode && !ExpertModeBackupEnabled {
		msg := "Backup for ONTAP mode pools is currently not enabled."
		logger.Error(msg)
		return &gcpgenserver.V1betaCreateBackupBadRequest{
			Code:    400,
			Message: msg,
		}, nil
	}
	var vol *coremodels.Volume
	var expertModeVol *datamodel.ExpertModeVolumes
	// If pool is ONTAP mode, fetch from ExpertModeVolumes table
	if isExpertMode {
		expertModeVol, err = h.Orchestrator.GetExpertModeVolumeByExternalUUID(ctx, req.VolumeId)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				logger.Error("Expert mode volume not found", "volumeID", req.VolumeId)
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    400,
					Message: fmt.Sprintf("Expert mode volume with ID %s not found", req.VolumeId),
				}, nil
			} else {
				return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, nil
			}
		}
		if expertModeVol != nil {
			if expertModeVol.BackupConfig != nil && expertModeVol.BackupConfig.BackupVaultID != "" && expertModeVol.BackupConfig.BackupVaultID != params.BackupVaultId {
				msg := fmt.Sprintf("Expert mode volume %s is associated with a different backup vault %s", req.VolumeId, expertModeVol.BackupConfig.BackupVaultID)
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    400,
					Message: msg,
				}, nil
			}
			// Expert mode volume found - check if backup vault is attached
			if expertModeVol.BackupConfig == nil || expertModeVol.BackupConfig.BackupVaultID == "" {
				// Backup vault not attached, check if it exists in VCP, if not fetch from CVP
				backupVault, err := checkAndFetchBackupVault(ctx, &h, expertModeVol, params.BackupVaultId, params.LocationId)
				if err != nil && !errors.IsNotFoundErr(err) {
					logger.Error("Failed to check/attach backup vault to expert mode volume", "error", err.Error())
					return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
				}
				if backupVault == nil {
					return &gcpgenserver.V1betaCreateBackupBadRequest{
						Code:    400,
						Message: fmt.Sprintf("Backup vault %s not found", params.BackupVaultId),
					}, nil
				}
			}
		}
	} else {
		// For non-ONTAP pools or when poolID is not provided, use regular volume lookup
		// For GCBDR vaults, fetch volume without account filter
		// For non-GCBDR vaults, we'll validate account ownership after fetching
		vol, err = h.Orchestrator.GetVolume(ctx, req.VolumeId, false)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				if !utils.IsCVPHostConfigured() {
					return &gcpgenserver.V1betaCreateBackupBadRequest{
						Code:    400,
						Message: fmt.Sprintf("Volume with ID %s not found", req.VolumeId),
					}, nil
				}
				fetchFromCVP = true
			} else if errors.IsUserInputValidationErr(err) {
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    400,
					Message: err.Error(),
				}, nil
			} else {
				logger.Error("Failed to get volume", "error", err.Error())
				return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
			}
		}
		// For non-GCBDR vaults, validate that volume belongs to the requesting account
		if vol != nil && !isGCBDRVault {
			if vol.AccountName != params.ProjectNumber {
				msg := fmt.Sprintf("Volume %s does not belong to account %s", req.VolumeId, params.ProjectNumber)
				logger.Error(msg)
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    400,
					Message: msg,
				}, nil
			}
		}
	}

	// Check if volume exist in the VSA if not call CVP API to create the backup
	if !isGCBDRVault && ((err != nil && fetchFromCVP) || (vol == nil && !isExpertMode)) {
		// Check if backup already exists in VCP under the same vault before creating in CVP
		filters := [][]interface{}{{"name = ?", req.ResourceId}}
		existingBackups, err := h.Orchestrator.ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, filters)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				logger.Error("No existing backups found in VCP", "resourceID", req.ResourceId)
			} else {
				logger.Error("Failed to check for existing backups", "error", err.Error())
				return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
			}
		}
		if len(existingBackups) > 0 {
			msg := fmt.Sprintf("Backup with resource ID %s already exists in backup vault %s", req.ResourceId, params.BackupVaultId)
			logger.Error(msg)
			return &gcpgenserver.V1betaCreateBackupConflict{
				Code:    409,
				Message: msg,
			}, nil
		}
		jwtToken := utils.GetJWTTokenFromContext(ctx)
		cvpClient := createClient(logger, jwtToken)

		cvpParams := &backups.V1betaCreateBackupParams{
			BackupVaultID:  params.BackupVaultId,
			LocationID:     params.LocationId,
			ProjectNumber:  params.ProjectNumber,
			XCorrelationID: &params.XCorrelationID.Value,
			Body: &models.BackupCreateV1beta{
				ResourceNameV1beta: models.ResourceNameV1beta{
					ResourceID: &req.ResourceId,
				},
				DescriptionV1beta: models.DescriptionV1beta{
					Description: &req.Description.Value,
				},
				VolumeIDV1beta: models.VolumeIDV1beta{
					VolumeID: &req.VolumeId,
				},
				SnapshotIDV1beta: models.SnapshotIDV1beta{
					SnapshotID: req.SnapshotId.Value,
				},
			},
		}
		cvpBackupCreated, cvpBackupAccepted, err := cvpClient.Backups.V1betaCreateBackup(cvpParams)
		if err != nil {
			switch e := err.(type) {
			case *backups.V1betaCreateBackupBadRequest:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupBadRequest{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupUnauthorized:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupUnauthorized{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupForbidden:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupForbidden{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupConflict:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupConflict{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupUnprocessableEntity:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupUnprocessableEntity{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupTooManyRequests:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupTooManyRequests{
					Code:    code,
					Message: msg,
				}, nil
			case *backups.V1betaCreateBackupNotImplemented:
				msg := nillable.GetString(&e.Payload.Message, "")
				code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
				return &gcpgenserver.V1betaCreateBackupNotImplemented{
					Code:    code,
					Message: msg,
				}, nil
			default:
				code := float64(500)
				msg := err.Error()
				return &gcpgenserver.V1betaCreateBackupInternalServerError{
					Code:    code,
					Message: msg,
				}, nil
			}
		}

		if cvpBackupCreated != nil {
			pl := cvpBackupCreated.Payload
			backupV1beta := convertToBackupsV1beta(pl)
			operationID := "/v1beta/projects/" + params.ProjectNumber + "/locations/" + params.LocationId + "/operations/" + uuid.UUID{}.String()

			done := true
			resp := &models.OperationV1beta{
				Name:     operationID,
				Done:     &done,
				Response: backupV1beta,
			}
			return convertToOperationV1beta(resp), nil
		}
		if cvpBackupAccepted != nil {
			return convertOperationToOperationV1Beta(cvpBackupAccepted.Payload), nil
		}

		msg := "An unexpected error occurred while creating the backup"
		return &gcpgenserver.V1betaCreateBackupInternalServerError{
			Code:    float64(500),
			Message: msg,
		}, nil
	}
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaCreateBackupBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to get volume", "error", err.Error())
		return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	// For GCBDR vaults, skip CVP check and only check VCP
	if utils.IsCVPHostConfigured() && !isGCBDRVault {
		exist, err := checkIfBackupExistInCVP(ctx, &req.ResourceId, params)
		if err != nil {
			logger.Error("Failed to check if backup exists in CVP", "error", err.Error())
			return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
		}
		if exist {
			msg := fmt.Sprintf("Backup with resource ID %s already exists in the backup vault %s", req.ResourceId, params.BackupVaultId)
			logger.Error(msg)
			return &gcpgenserver.V1betaCreateBackupConflict{
				Code:    409,
				Message: msg,
			}, nil
		}
	} else {
		logger.Info("Skipping CVP backup existence check for GCBDR vault, checking VCP only", "backupVaultId", params.BackupVaultId)
		filters := [][]interface{}{{"name = ?", req.ResourceId}}

		var existingBackups []*datamodel.Backup
		if isGCBDRVault {
			// For GCBDR vaults, do not apply project/account filtering when checking for existing backups
			// This ensures we detect backups created by other projects in the same GCBDR vault
			existingBackups, err = h.Orchestrator.ListBackupsWithoutAccountFilter(ctx, params.BackupVaultId, filters)
			if err != nil && !errors.IsNotFoundErr(err) {
				logger.Error("Failed to check for existing backups in VCP", "error", err.Error())
				return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
			}
		} else {
			existingBackups, err = h.Orchestrator.ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, filters)
			if err != nil && !errors.IsNotFoundErr(err) {
				logger.Error("Failed to check for existing backups in VCP", "error", err.Error())
				return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
			}
		}

		if len(existingBackups) > 0 {
			msg := fmt.Sprintf("Backup with resource ID %s already exists in backup vault %s", req.ResourceId, params.BackupVaultId)
			logger.Error(msg)
			return &gcpgenserver.V1betaCreateBackupConflict{
				Code:    409,
				Message: msg,
			}, nil
		}
	}
	// If the request belongs to VSA, we will create the backup using the orchestrator
	vsaParams := createBackupParams(req, params, isExpertMode && expertModeVol != nil, backupVaultServiceType)

	backup, jobID, err := h.Orchestrator.CreateBackup(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) || errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaCreateBackupBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		if errors.IsConflictErr(err) {
			return &gcpgenserver.V1betaCreateBackupConflict{
				Code:    409,
				Message: err.Error(),
			}, nil
		}

		logger.Error("Failed to create backup", "error", err.Error())
		return &gcpgenserver.V1betaCreateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	resp, err := encodeBackupV1(convertBackupModelToBackupsV1beta(backup))
	if err != nil {
		logger.Error("Failed to marshal backup", err.Error())
		return &gcpgenserver.V1betaCreateBackupInternalServerError{}, err
	}

	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobID)
	if backup.LifeCycleState == datamodel.LifeCycleStateCreating {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(operationID),
			Response: resp,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Response: resp,
		Done:     gcpgenserver.NewOptBool(true),
	}, nil
}

func (h Handler) V1betaDeleteBackupUnderBackupVault(ctx context.Context, params gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams) (gcpgenserver.V1betaDeleteBackupUnderBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	if !backupEnabled {
		return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	_, err := h.Orchestrator.GetBackup(ctx, &common.GetBackupParams{
		BackupVaultID: params.BackupVaultId,
		BackupUUID:    params.BackupId,
		AccountName:   params.ProjectNumber,
	})
	if err != nil {
		// Delegate to CVP/SDE only when the backup is not found in VSA.
		if errors.IsNotFoundErr(err) {
			if !utils.IsCVPHostConfigured() {
				return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound{
					Code:    404,
					Message: fmt.Sprintf("Backup with ID %s not found", params.BackupId),
				}, nil
			}
			return deleteBackupToCVP(ctx, params)
		}
		logger.Error("Failed to get backup", "error", err.Error())
		return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{Code: 500, Message: err.Error()}, err
	}
	// If the request belongs to VSA, we will delete the backup using the orchestrator
	vsaParams := &common.DeleteBackupParams{
		AccountName:     params.ProjectNumber,
		BackupVaultUUID: params.BackupVaultId,
		BackupUUID:      params.BackupId,
		Region:          params.LocationId,
	}
	_, jobId, err := h.Orchestrator.DeleteBackup(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to delete backup", "error", err.Error())
		return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{Code: 500, Message: err.Error()}, err
	}
	if jobId == "" {
		jobId = uuid.UUID{}.String()
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(operationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}
	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(operationID),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

func (h Handler) V1betaDescribeBackup(ctx context.Context, params gcpgenserver.V1betaDescribeBackupParams) (gcpgenserver.V1betaDescribeBackupRes, error) {
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDescribeBackupInternalServerError{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	backup, err := h.Orchestrator.GetBackup(ctx, &common.GetBackupParams{
		BackupVaultID: params.BackupVaultId,
		BackupUUID:    params.BackupId,
		AccountName:   params.ProjectNumber,
	})
	if err != nil {
		if errors.IsNotFoundErr(err) {
			if !utils.IsCVPHostConfigured() {
				return &gcpgenserver.V1betaDescribeBackupNotFound{
					Code:    404,
					Message: fmt.Sprintf("Backup with ID %s not found", params.BackupId),
				}, nil
			}
			// Delegate to CVP/SDE so NotFound errors are returned with proper HTTP status.
			return getBackupsFromCVP(ctx, params)
		}
		return &gcpgenserver.V1betaDescribeBackupInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	resp := convertBackupDataModelToBackupsV1beta(backup)

	return &gcpgenserver.V1betaDescribeBackupOK{
		Backups: []gcpgenserver.BackupV1beta{resp},
	}, nil
}

func (h Handler) V1betaUpdateBackup(ctx context.Context, req *gcpgenserver.BackupUpdateV1beta, params gcpgenserver.V1betaUpdateBackupParams) (gcpgenserver.V1betaUpdateBackupRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	if !backupEnabled {
		return &gcpgenserver.V1betaUpdateBackupBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	// Validate region and zone
	_, _, parsingErr := utilParseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateBackupBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Fetch the backup from VSA
	_, err := h.Orchestrator.GetBackup(ctx, &common.GetBackupParams{
		BackupVaultID: params.BackupVaultId,
		BackupUUID:    params.BackupId,
		AccountName:   params.ProjectNumber,
	})
	if err != nil {
		// Delegate to CVP/SDE only when the backup is not found in VSA
		if errors.IsNotFoundErr(err) {
			if !utils.IsCVPHostConfigured() {
				return &gcpgenserver.V1betaUpdateBackupNotFound{
					Code:    404,
					Message: fmt.Sprintf("Backup with ID %s not found", params.BackupId),
				}, nil
			}
			return updateBackupToCVP(ctx, req, params)
		}
		// For all other errors, treat as internal failures to avoid misrouting
		logger.Error("Failed to get backup from orchestrator", "error", err.Error())
		return &gcpgenserver.V1betaUpdateBackupInternalServerError{
			Code:    500,
			Message: "Internal error while fetching backup from VSA",
		}, err
	}

	// If the request belongs to VSA, update the backup using the orchestrator
	vsaParams := &common.UpdateBackupParams{
		AccountName:     params.ProjectNumber,
		BackupVaultUUID: params.BackupVaultId,
		BackupUUID:      params.BackupId,
		Description:     req.Description,
		Region:          params.LocationId,
	}
	backupResp, jobId, err := h.Orchestrator.UpdateBackup(ctx, vsaParams)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaUpdateBackupBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update backup", "error", err.Error())
		return &gcpgenserver.V1betaUpdateBackupInternalServerError{Code: 500, Message: err.Error()}, err
	}
	bResp := convertBackupModelToBackupsV1beta(backupResp)
	backupResponse, err := json.Marshal(bResp)
	if err != nil {
		logger.Error("Failed to marshal backup", err.Error())
		return &gcpgenserver.V1betaUpdateBackupInternalServerError{
			Code:    500,
			Message: "Failed to marshal backup response",
		}, nil
	}

	operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, jobId)
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(operationID),
		Done:     gcpgenserver.NewOptBool(false),
		Response: backupResponse,
	}, nil
}

func updateBackupToCVP(ctx context.Context, req *gcpgenserver.BackupUpdateV1beta, params gcpgenserver.V1betaUpdateBackupParams) (gcpgenserver.V1betaUpdateBackupRes, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)

	var description string
	if req.Description != "" {
		description = req.Description
	} else {
		description = ""
	}
	cvpParams := &backups.V1betaUpdateBackupParams{
		BackupVaultID:  params.BackupVaultId,
		BackupID:       params.BackupId,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
		Body: &models.BackupUpdateV1beta{
			Description: &description,
		},
	}

	cvpBackupOK, cvpBackupAccepted, cvpBackupNoContent, err := cvpClient.Backups.V1betaUpdateBackup(cvpParams)
	if err != nil {
		switch e := err.(type) {
		case *backups.V1betaUpdateBackupBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupUnprocessableEntity:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupUnprocessableEntity{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaUpdateBackupNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			code := float64(500)
			msg := err.Error()
			return &gcpgenserver.V1betaUpdateBackupInternalServerError{
				Code:    code,
				Message: msg,
			}, nil
		}
	}

	if cvpBackupOK != nil {
		backupV1beta := convertToBackupsV1beta(cvpBackupOK.Payload)
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.New().String())
		done := true
		resp := &models.OperationV1beta{
			Name:     operationID,
			Done:     &done,
			Response: backupV1beta,
		}
		return convertToOperationV1beta(resp), nil
	}
	if cvpBackupAccepted != nil {
		return convertOperationToOperationV1Beta(cvpBackupAccepted.Payload), nil
	}
	if cvpBackupNoContent != nil {
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, uuid.New().String())
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(operationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}
	msg := "An unexpected error occurred while updating the backup"
	return &gcpgenserver.V1betaUpdateBackupInternalServerError{
		Code:    500,
		Message: msg,
	}, nil
}

func (h Handler) V1betaListBackups(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	var response gcpgenserver.V1betaListBackupsOK
	// Check if backup vault exists and determine if it's a GCBDR vault
	vcpBv, err := h.Orchestrator.GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			logger.Error("Failed to get backup vault", "error", err)
			return &gcpgenserver.V1betaListBackupsInternalServerError{
				Code:    500,
				Message: "Failed to get Backup Vault",
			}, nil
		}
	}

	// Determine if this is a GCBDR vault
	isGCBDRVault := false
	if vcpBv != nil {
		isGCBDRVault = vcpBv.ServiceType == datamodel.ServiceTypeCrossProject
	}

	// For GCBDR vaults, skip CVP call and only list from VCP
	var cvpBackups *gcpgenserver.V1betaListBackupsOK
	if utils.IsCVPHostConfigured() && !isGCBDRVault {
		listBackupParams := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  params.BackupVaultId,
			LocationId:     params.LocationId,
			ProjectNumber:  params.ProjectNumber,
			XCorrelationID: params.XCorrelationID,
			BackupName:     params.BackupName,
			VolumeId:       params.VolumeId,
		}
		listBackupsResp, err := listBackupsToCVP(ctx, listBackupParams)
		if err != nil {
			logger.Error("Failed to list backups", "error", err.Error())
			return listBackupsResp, err
		}
		switch resp := listBackupsResp.(type) {
		case *gcpgenserver.V1betaListBackupsOK:
			cvpBackups = resp
		default:
			logger.Error("Unexpected response type from listBackupsToCVP", "responseType", fmt.Sprintf("%T", listBackupsResp))
			return listBackupsResp, nil
		}
	}

	if vcpBv != nil {
		var filters [][]interface{}
		if params.BackupName.IsSet() && params.BackupName.Value != "" {
			filters = append(filters, []interface{}{"name = ?", params.BackupName.Value})
		}
		if params.VolumeId.IsSet() && params.VolumeId.Value != "" {
			filters = append(filters, []interface{}{"volume_uuid = ?", params.VolumeId.Value})
		}

		// For GCBDR vaults, list backups without account filtering to show backups from all projects
		var backupList []*datamodel.Backup
		if isGCBDRVault {
			logger.Info("Listing backups for GCBDR vault without account filtering", "backupVaultId", params.BackupVaultId)
			backupList, err = h.Orchestrator.ListBackupsWithoutAccountFilter(ctx, params.BackupVaultId, filters)
		} else {
			backupList, err = h.Orchestrator.ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, filters)
		}
		if err != nil {
			logger.Error("Failed to list backups", "error", err)
			return &gcpgenserver.V1betaListBackupsInternalServerError{
				Code:    500,
				Message: "failed to list backups",
			}, err
		}
		for _, backup := range backupList {
			response.Backups = append(response.Backups, convertBackupDataModelToBackupsV1beta(backup))
		}
	}

	// Only append CVP backups if this is not a GCBDR vault
	if cvpBackups != nil {
		response.Backups = append(response.Backups, cvpBackups.GetBackups()...)
	}

	return &response, nil
}

func convertToBackupsV1beta(backup *models.BackupV1beta) gcpgenserver.BackupV1beta {
	res := gcpgenserver.BackupV1beta{
		ResourceId:               utils.GetOptString(&backup.ResourceID),
		VolumeId:                 utils.GetOptString(&backup.VolumeID),
		State:                    gcpgenserver.NewOptBackupV1betaState(gcpgenserver.BackupV1betaState(backup.State)),
		Created:                  utils.GetOptDateTime(&backup.Created),
		EnforcedRetentionEndTime: utils.GetOptDateTime(backup.EnforcedRetentionEndTime),
		BackupId:                 utils.GetOptString(&backup.BackupID),
		VolumeUsageBytes:         utils.GetOptInt64(backup.VolumeUsageBytes),
		SourceVolume:             utils.GetOptString(&backup.SourceVolume),
		BackupVaultId:            utils.GetOptString(backup.BackupVaultID),
		Description:              utils.GetOptString(backup.Description),
		SourceSnapshot:           utils.GetOptString(backup.SourceSnapshot),
		BackupType:               gcpgenserver.NewOptBackupV1betaBackupType(gcpgenserver.BackupV1betaBackupType(backup.BackupType)),
		BackupChainBytes:         utils.GetOptInt64(backup.BackupChainBytes),
		SatisfiesPzs:             utils.GetOptBool(backup.SatisfiesPzs),
		SatisfiesPzi:             utils.GetOptBool(backup.SatisfiesPzi),
		VolumeRegion:             utils.GetOptString(backup.VolumeRegion),
		BackupRegion:             utils.GetOptString(backup.BackupRegion),
		BucketName:               utils.GetOptString(&backup.BucketName),
		SnapshotName: func() gcpgenserver.OptString {
			backupName := ""
			if backup.ResourceID != "" {
				backupName = backup.ResourceID
			}
			snapshotName := utils.ExtractSnapshotNameFromCVPBackup(backup, backupName)
			if snapshotName != "" {
				return gcpgenserver.NewOptString(snapshotName)
			}
			return gcpgenserver.OptString{}
		}(),
		EndPointUUID: utils.GetOptString(backup.EndPointUUID),
		SnapshotUuid: utils.GetOptString(backup.SnapshotUUID),
		Protocols:    nil, // CVP models don't have protocols field, will be empty for backups fetched from CVP
		AssetLocationMetadata: func() gcpgenserver.OptAssetLocationMetadataV2 {
			if backup.AssetLocationMetadata != nil {
				var assets []gcpgenserver.ChildAssetV2
				for _, asset := range backup.AssetLocationMetadata.ChildAssets {
					assets = append(assets, gcpgenserver.ChildAssetV2{
						AssetType:  utils.GetOptString(&asset.AssetType),
						AssetNames: asset.AssetNames,
					})
				}
				return gcpgenserver.NewOptAssetLocationMetadataV2(gcpgenserver.AssetLocationMetadataV2{
					ChildAssets: assets,
				})
			}
			return gcpgenserver.OptAssetLocationMetadataV2{}
		}(),
	}
	return res
}

func convertBackupModelToBackupsV1beta(backup *coremodels.Backup) *gcpgenserver.BackupV1beta {
	sourceSnapshot := utils.RenameSnapshotName(backup.SnapshotName)
	backupV1 := &gcpgenserver.BackupV1beta{
		ResourceId:     utils.GetOptString(&backup.Name),
		VolumeId:       utils.GetOptString(&backup.VolumeID),
		State:          gcpgenserver.NewOptBackupV1betaState(gcpgenserver.BackupV1betaState(backup.LifeCycleState)),
		BackupId:       utils.GetOptString(&backup.BackupID),
		SourceVolume:   utils.GetOptString(&backup.VolumeName),
		BackupVaultId:  utils.GetOptString(&backup.BackupVaultID),
		Description:    utils.GetOptString(backup.Description),
		SourceSnapshot: utils.GetOptString(&sourceSnapshot),
		BackupType:     gcpgenserver.NewOptBackupV1betaBackupType(gcpgenserver.BackupV1betaBackupType(backup.Type)),
		BucketName:     utils.GetOptString(&backup.BucketName),
		SnapshotName:   utils.GetOptString(&backup.SnapshotName),
		EndPointUUID:   utils.GetOptString(&backup.EndpointUUID),
		Protocols: func() []gcpgenserver.ProtocolsV1beta {
			if len(backup.Protocols) > 0 {
				protocols := make([]gcpgenserver.ProtocolsV1beta, 0, len(backup.Protocols))
				for _, p := range backup.Protocols {
					protocols = append(protocols, gcpgenserver.ProtocolsV1beta(p))
				}
				return protocols
			}
			return nil
		}(),
		AssetLocationMetadata: gcpgenserver.OptAssetLocationMetadataV2{},
	}
	if backup.MinimumEnforcedRetentionDuration != nil && *backup.MinimumEnforcedRetentionDuration > 0 && backup.IsBackupImmutable {
		expirationDate := backup.CreationTime.AddDate(0, 0, int(*backup.MinimumEnforcedRetentionDuration))
		backupV1.EnforcedRetentionEndTime = gcpgenserver.OptDateTime{
			Value: expirationDate,
			Set:   true,
		}
	}
	return backupV1
}

func convertBackupDataModelToBackupsV1beta(backup *datamodel.Backup) gcpgenserver.BackupV1beta {
	var state gcpgenserver.BackupV1betaState
	// Need to convert states as DB models and API models have different states
	switch backup.State {
	case datamodel.LifeCycleStateAvailable:
		state = gcpgenserver.BackupV1betaStateREADY
	case datamodel.LifeCycleStateUpdating:
		state = gcpgenserver.BackupV1betaStateUPDATING
	default:
		state = gcpgenserver.BackupV1betaState(backup.State)
	}
	sourceVolumePath := utils.GetSourceVolumePathFromBackup(backup)
	if backup.Attributes != nil && backup.Attributes.IsExpertModeBackup && backup.Attributes.VolumeName != "" {
		// Keep the full path format but replace the volume name segment with the ONTAP volume UUID.
		if parts := strings.Split(sourceVolumePath, "/"); len(parts) > 0 && parts[len(parts)-1] == backup.Attributes.VolumeName {
			parts[len(parts)-1] = backup.VolumeUUID
			sourceVolumePath = strings.Join(parts, "/")
		}
	}
	sourceSnapshotPath := utils.GetSourceSnapshotPathFromBackup(backup)

	var satisfiesPzi, satisfiesPzs bool
	for _, bucket := range backup.BackupVault.BucketDetails {
		if bucket.BucketName == backup.Attributes.BucketName {
			satisfiesPzi = bucket.SatisfiesPzi
			satisfiesPzs = bucket.SatisfiesPzs
			break
		}
	}

	var backupRegion string
	if backup.BackupVault.BackupRegionName != nil && *backup.BackupVault.BackupRegionName != "" {
		backupRegion = *backup.BackupVault.BackupRegionName
	} else if backup.BackupVault.SourceRegionName != nil {
		backupRegion = *backup.BackupVault.SourceRegionName
	}

	backupV1 := gcpgenserver.BackupV1beta{
		ResourceId: gcpgenserver.OptString{
			Value: backup.Name,
			Set:   true,
		},
		VolumeId: gcpgenserver.OptString{
			Value: backup.VolumeUUID,
			Set:   true,
		},
		State: gcpgenserver.OptBackupV1betaState{
			Value: state,
			Set:   true,
		},
		Created: gcpgenserver.OptDateTime{
			Value: backup.CreatedAt,
			Set:   true,
		},
		BackupId: gcpgenserver.OptString{
			Value: backup.UUID,
			Set:   true,
		},
		VolumeUsageBytes: gcpgenserver.OptInt64{
			Value: backup.SizeInBytes,
			Set:   true,
		},
		BackupVaultId: gcpgenserver.OptString{
			Value: backup.BackupVault.UUID,
			Set:   true,
		},
		Description: gcpgenserver.OptString{
			Value: backup.Description,
			Set:   true,
		},
		BackupType: gcpgenserver.OptBackupV1betaBackupType{
			Value: gcpgenserver.BackupV1betaBackupType(backup.Type),
			Set:   true,
		},
		SourceSnapshot: gcpgenserver.OptString{
			Value: sourceSnapshotPath,
			Set:   backup.Attributes.UseExistingSnapshot && backup.Attributes.SnapshotName != "",
		},
		SourceVolume: gcpgenserver.OptString{
			Value: sourceVolumePath,
			Set:   true,
		},
		BackupRegion: gcpgenserver.OptString{
			Value: backupRegion,
			Set:   backupRegion != "",
		},
		VolumeRegion: gcpgenserver.OptString{
			Value: *backup.BackupVault.SourceRegionName,
			Set:   backup.BackupVault.SourceRegionName != nil,
		},
		BucketName: gcpgenserver.OptString{
			Value: backup.Attributes.BucketName,
			Set:   backup.Attributes != nil && backup.Attributes.BucketName != "",
		},
		SnapshotName: gcpgenserver.OptString{
			Value: backup.Attributes.SnapshotName,
			Set:   backup.Attributes != nil && backup.Attributes.SnapshotName != "",
		},
		EndPointUUID: gcpgenserver.OptString{
			Value: backup.Attributes.EndpointUUID,
			Set:   backup.Attributes != nil && backup.Attributes.EndpointUUID != "",
		},
		SnapshotUuid: gcpgenserver.OptString{
			Value: backup.Attributes.SnapshotID,
			Set:   backup.Attributes != nil && backup.Attributes.SnapshotID != "",
		},
		SatisfiesPzi: gcpgenserver.OptBool{
			Value: satisfiesPzi,
			Set:   true,
		},
		SatisfiesPzs: gcpgenserver.OptBool{
			Value: satisfiesPzs,
			Set:   true,
		},
		BackupChainBytes: gcpgenserver.OptInt64{
			Value: backup.LatestLogicalBackupSize,
			Set:   backup.LatestLogicalBackupSize != 0,
		},
		Protocols: func() []gcpgenserver.ProtocolsV1beta {
			if backup.Attributes != nil && backup.Attributes.Protocols != nil && len(backup.Attributes.Protocols) > 0 {
				protocols := make([]gcpgenserver.ProtocolsV1beta, 0, len(backup.Attributes.Protocols))
				for _, p := range backup.Attributes.Protocols {
					protocols = append(protocols, gcpgenserver.ProtocolsV1beta(p))
				}
				return protocols
			}
			return nil
		}(),
	}
	if backup.BackupVault.ImmutableAttributes != nil && *backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration > 0 && common.CheckIfBackupIsImmutable(backup) {
		expirationDate := backup.CreatedAt.AddDate(0, 0, int(*backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration))
		backupV1.EnforcedRetentionEndTime = gcpgenserver.OptDateTime{
			Value: expirationDate,
			Set:   true,
		}
	}
	if backup.ExternalUUID != "" && backup.BackupVault.BackupVaultType == gcp.CrossRegionBackupType {
		backupV1.CrossRegionBackupId = gcpgenserver.OptString{
			Value: backup.ExternalUUID,
			Set:   true,
		}
	}
	if backup.AssetMetadata != nil {
		backupV1.AssetLocationMetadata = gcpgenserver.OptAssetLocationMetadataV2{
			Value: gcpgenserver.AssetLocationMetadataV2{
				ChildAssets: func() []gcpgenserver.ChildAssetV2 {
					var assets []gcpgenserver.ChildAssetV2
					for _, asset := range backup.AssetMetadata.ChildAssets {
						assets = append(assets, gcpgenserver.ChildAssetV2{
							AssetType:  gcpgenserver.OptString{Value: asset.AssetType, Set: true},
							AssetNames: asset.AssetNames,
						})
					}
					return assets
				}(),
			},
			Set: true,
		}
	}
	return backupV1
}

func createBackupParams(req *gcpgenserver.BackupCreateV1beta, params gcpgenserver.V1betaCreateBackupParams, isExpertModeVolume bool, backupVaultServiceType string) *common.CreateBackupParams {
	backupParams := common.CreateBackupParams{
		AccountName:            params.ProjectNumber,
		BackupVaultID:          params.BackupVaultId,
		VolumeUUID:             req.VolumeId,
		BackupName:             req.ResourceId,
		BackupType:             utils.BackupTypeMANUAL, // Default to MANUAL, later can be changed based on the request
		LocationID:             params.LocationId,
		UseExistingSnapshot:    false,                  // Default to false, can be changed based on the request
		SnapshotID:             "",                     // Default to empty, can be changed based on the request
		IsExpertModeVolume:     isExpertModeVolume,     // Indicates if this is an expert mode volume
		BackupVaultServiceType: backupVaultServiceType, // Service type of the backup vault (GCBDR, GCNV, etc.)
	}
	if req.Description.IsSet() {
		backupParams.Description = req.Description.Value
	}
	if req.SnapshotId.IsSet() {
		backupParams.SnapshotID = req.SnapshotId.Value
	}
	if params.XCorrelationID.IsSet() {
		backupParams.XCorrelationID = params.XCorrelationID.Value
	}
	// For public API, volume and snapshot information is retrieved from database
	// VolumeName, Protocols, and SnapshotName are only available in InternalBackupCreateV1beta
	// Derive UseExistingSnapshot from whether SnapshotID is provided
	backupParams.UseExistingSnapshot = backupParams.SnapshotID != ""
	return &backupParams
}

// encodeBackupV1 encodes a backupV1 struct to JSON.
func encodeBackupV1(backupV1beta *gcpgenserver.BackupV1beta) (jx.Raw, error) {
	data, err := json.Marshal(backupV1beta)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func deleteBackupToCVP(ctx context.Context, params gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams) (gcpgenserver.V1betaDeleteBackupUnderBackupVaultRes, error) {
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpParams := &backups.V1betaDeleteBackupUnderBackupVaultParams{
		BackupVaultID:  params.BackupVaultId,
		BackupID:       params.BackupId,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}

	cvpDeleted, cvpAccepted, err := cvpClient.Backups.V1betaDeleteBackupUnderBackupVault(cvpParams)
	if err != nil {
		switch e := err.(type) {
		case *backups.V1betaDeleteBackupUnderBackupVaultBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultConflict:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultConflict{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDeleteBackupUnderBackupVaultNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			code := float64(500)
			msg := err.Error()
			return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{
				Code:    code,
				Message: msg,
			}, nil
		}
	}
	if cvpDeleted != nil {
		pl := cvpDeleted.Payload
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, pl.Name)
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(operationID),
			Done: gcpgenserver.NewOptBool(true),
		}, nil
	}

	if cvpAccepted != nil {
		return convertOperationToOperationV1Beta(cvpAccepted.Payload), nil
	}
	msg := "An unexpected error occurred while deleting the backup"
	return &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{
		Code:    float64(500),
		Message: msg,
	}, nil
}

func _getBackupsFromCVP(ctx context.Context, params gcpgenserver.V1betaDescribeBackupParams) (gcpgenserver.V1betaDescribeBackupRes, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpParams := &backups.V1betaDescribeBackupParams{
		BackupVaultID:  params.BackupVaultId,
		BackupID:       params.BackupId,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}

	backup, err := cvpClient.Backups.V1betaDescribeBackup(cvpParams)
	if err != nil {
		switch e := err.(type) {
		case *backups.V1betaDescribeBackupBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDescribeBackupUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDescribeBackupForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDescribeBackupNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDescribeBackupTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaDescribeBackupNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			code := float64(500)
			msg := err.Error()
			return &gcpgenserver.V1betaDescribeBackupInternalServerError{
				Code:    code,
				Message: msg,
			}, err
		}
	}
	if backup != nil && backup.Payload != nil {
		pl := backup.Payload
		backupsV1beta := convertToBackupsV1beta(pl)
		return &gcpgenserver.V1betaDescribeBackupOK{
			Backups: []gcpgenserver.BackupV1beta{backupsV1beta},
		}, nil
	}
	msg := "An unexpected error occurred while listing the backups"
	return &gcpgenserver.V1betaDescribeBackupInternalServerError{
		Code:    float64(500),
		Message: msg,
	}, nil
}

func _listBackupsToCVP(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
	logger := util.GetLogger(ctx)
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpParams := &backups.V1betaListBackupsParams{
		BackupVaultID:  params.BackupVaultId,
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}
	if params.BackupName.IsSet() && params.BackupName.Value != "" {
		cvpParams.BackupName = params.BackupName.Value
	}
	if params.VolumeId.IsSet() && params.VolumeId.Value != "" {
		cvpParams.VolumeID = params.VolumeId.Value
	}

	backup, err := cvpClient.Backups.V1betaListBackups(cvpParams)
	if err != nil {
		switch e := err.(type) {
		case *backups.V1betaListBackupsBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaListBackupsUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaListBackupsForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaListBackupsNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaListBackupsTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backups.V1betaListBackupsNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupsNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			code := float64(500)
			msg := err.Error()
			return &gcpgenserver.V1betaListBackupsInternalServerError{
				Code:    code,
				Message: msg,
			}, err
		}
	}
	var backupV1beta []gcpgenserver.BackupV1beta
	if backup != nil && backup.Payload != nil {
		pl := backup.Payload
		for i := range pl.Backups {
			backupV1beta = append(backupV1beta, convertToBackupsV1beta(pl.Backups[i]))
		}
		return &gcpgenserver.V1betaListBackupsOK{
			Backups: backupV1beta,
		}, nil
	}
	msg := "An unexpected error occurred while listing the backup"
	return &gcpgenserver.V1betaListBackupsInternalServerError{
		Code:    float64(500),
		Message: msg,
	}, nil
}

func _checkIfBackupExistInCVP(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
	logger := util.GetLogger(ctx)
	listBackupParams := gcpgenserver.V1betaListBackupsParams{
		BackupVaultId:  params.BackupVaultId,
		LocationId:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: gcpgenserver.NewOptString(params.XCorrelationID.Value),
	}
	listBackupsResp, err := listBackupsToCVP(ctx, listBackupParams)
	if err != nil {
		logger.Error("Failed to list backups", "error", err.Error())
		return false, err
	}
	var cvpBackups *gcpgenserver.V1betaListBackupsOK
	switch resp := listBackupsResp.(type) {
	case *gcpgenserver.V1betaListBackupsOK:
		cvpBackups = resp
	default:
		logger.Error("Unexpected response type from listBackupsToCVP", "responseType", fmt.Sprintf("%T", listBackupsResp))
		return false, fmt.Errorf("unexpected response type: %T", listBackupsResp)
	}
	for _, cvpBackup := range cvpBackups.GetBackups() {
		if cvpBackup.ResourceId == utils.GetOptString(backupID) {
			msg := fmt.Sprintf("Backup with resource ID %s already exists", *backupID)
			logger.Error(msg)
			return true, nil
		}
	}
	return false, nil
}

func fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups []*datamodel.Backup, backupUUIDs []string) []string {
	// Create a map to store UUIDs from listBackups for quick lookup
	backupUUIDMap := make(map[string]bool)
	for _, backup := range listBackups {
		backupUUIDMap[backup.UUID] = true
	}
	// Filter UUIDs from backupUUIDs that are not in listBackups
	var uuids []string
	for _, backupUUID := range backupUUIDs {
		if !backupUUIDMap[backupUUID] {
			uuids = append(uuids, backupUUID)
		}
	}
	return uuids
}

func checkAndFetchBackupVault(ctx context.Context, handler *Handler, expertModeVol *datamodel.ExpertModeVolumes, backupVaultID, region string) (*datamodel.BackupVault, error) {
	backupVaultModel, err := handler.Orchestrator.GetBackupVaultByUUID(ctx, backupVaultID, expertModeVol.Account.Name)
	if err == nil && backupVaultModel != nil {
		return &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: backupVaultID,
			},
		}, nil
	}

	// If error is not a NotFound error, propagate it (e.g., database connection failures, timeouts)
	if err != nil {
		if !utils.IsCVPHostConfigured() && errors.IsNotFoundErr(err) {
			return nil, errors.NewNotFoundErr("Backup vault", &backupVaultID)
		} else if !errors.IsNotFoundErr(err) {
			return nil, err
		}
	}

	// Backup vault not found locally, fetch from CVP
	backupVault, err := gcp.GetBackupVaultFromCVP(ctx, backupVaultID, region, expertModeVol.Account.Name)
	if err != nil {
		return nil, err
	}

	param := &common.BackupVaultParams{
		OwnerID:     expertModeVol.Account.Name,
		Region:      region,
		AccountName: expertModeVol.Account.Name,
	}
	createdBackupVault, err := handler.Orchestrator.CreateBackupVaultEntryInVCP(ctx, backupVault, param)
	if err != nil {
		return nil, fmt.Errorf("failed to create backup vault entry in VCP: %w", err)
	}

	return createdBackupVault, nil
}

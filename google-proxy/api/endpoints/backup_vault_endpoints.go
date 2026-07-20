package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-faster/jx"
	"github.com/go-openapi/strfmt"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	sde "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/helper"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	utilsConvertJsonToModel = utils.ConvertJsonToModel
	cvpCreateClient         = cvp.CreateClient
	updateBackupVaultInSDE  = _updateBackupVaultInSDE
	jsonMarshal             = json.Marshal
	deleteBackupVaultInSDE  = _deleteBackupVaultInSDE
	createBackupVaultInSDE  = _createBackupVaultInSDE
	GCBDRVaultEnabled     = env.GetBool("GCBDR_VAULT_ENABLED", true)
	GCBDRVaultCMEKEnabled = env.GetBool("GCBDR_VAULT_CMEK_ENABLED", false)
)

// _validateRetentionParameters validates retention policy updates against current backup vault settings
func _validateRetentionParameters(currentBackupVault *coremodels.BackupVaultV1beta, newRetentionParams *commonparams.BackupRetentionPolicyParams) error {
	// Constants
	maxImmutablePeriodWhenDailyImmutability := int64(commonparams.ImmutablePeriodInDaysMaxDailyEnabled)
	maxImmutablePeriod := int64(commonparams.ImmutablePeriodInDaysMax)

	// Get current immutable attributes
	currentAttrs := &currentBackupVault.BackupRetentionPolicy

	// Validate retention duration - no decrease allowed
	if newRetentionParams.BackupMinimumEnforcedRetentionDuration != nil {
		newRetentionDays := *newRetentionParams.BackupMinimumEnforcedRetentionDuration

		if currentAttrs.BackupMinimumEnforcedRetentionDuration != nil {
			currentRetentionDays := *currentAttrs.BackupMinimumEnforcedRetentionDuration
			if currentRetentionDays > 0 && newRetentionDays < currentRetentionDays {
				return fmt.Errorf("cannot decrease backup minimum enforced retention duration from %d to %d days",
					currentRetentionDays, newRetentionDays)
			}
		}
	}

	// Validate immutability cannot be disabled once enabled
	if newRetentionParams.IsDailyBackupImmutable != nil {
		if currentAttrs.IsDailyBackupImmutable && !*newRetentionParams.IsDailyBackupImmutable {
			return fmt.Errorf("cannot disable daily backup immutability once enabled")
		}
	}

	if newRetentionParams.IsWeeklyBackupImmutable != nil {
		if currentAttrs.IsWeeklyBackupImmutable && !*newRetentionParams.IsWeeklyBackupImmutable {
			return fmt.Errorf("cannot disable weekly backup immutability once enabled")
		}
	}

	if newRetentionParams.IsMonthlyBackupImmutable != nil {
		if currentAttrs.IsMonthlyBackupImmutable && !*newRetentionParams.IsMonthlyBackupImmutable {
			return fmt.Errorf("cannot disable monthly backup immutability once enabled")
		}
	}

	if newRetentionParams.IsAdhocBackupImmutable != nil {
		if currentAttrs.IsAdhocBackupImmutable && !*newRetentionParams.IsAdhocBackupImmutable {
			return fmt.Errorf("cannot disable manual/adhoc backup immutability once enabled")
		}
	}

	// Determine final immutability states (merge current + new)
	finalDailyImmutable := determineImmutableState(currentAttrs.IsDailyBackupImmutable, newRetentionParams.IsDailyBackupImmutable)
	finalWeeklyImmutable := determineImmutableState(currentAttrs.IsWeeklyBackupImmutable, newRetentionParams.IsWeeklyBackupImmutable)
	finalMonthlyImmutable := determineImmutableState(currentAttrs.IsMonthlyBackupImmutable, newRetentionParams.IsMonthlyBackupImmutable)
	finalAdhocImmutable := determineImmutableState(currentAttrs.IsAdhocBackupImmutable, newRetentionParams.IsAdhocBackupImmutable)

	// Ensure at least one backup type remains immutable
	if !finalDailyImmutable && !finalWeeklyImmutable && !finalMonthlyImmutable && !finalAdhocImmutable {
		return fmt.Errorf("at least one backup type must remain immutable")
	}

	// Validate retention duration range based on daily and non daily immutability
	finalRetentionDays := getFinalRetentionDays(currentAttrs, newRetentionParams)
	if finalRetentionDays > 0 {
		if finalDailyImmutable {
			if finalRetentionDays > maxImmutablePeriodWhenDailyImmutability {
				return fmt.Errorf("retention period in days should be ≤ %d when daily backups are immutable", maxImmutablePeriodWhenDailyImmutability)
			}
		} else {
			if finalRetentionDays > maxImmutablePeriod {
				return fmt.Errorf("retention period in days should be ≤ %d when daily backups are not immutable", maxImmutablePeriod)
			}
		}
	}

	return nil // validation passed
}

// determineImmutableState determines the final immutable state by preferring new state over current
func determineImmutableState(currentState bool, newState *bool) bool {
	if newState != nil {
		return *newState
	}
	return currentState
}

// getFinalRetentionDays gets the final retention days by preferring new value over current
func getFinalRetentionDays(currentAttrs *coremodels.BackupRetentionPolicyparams, newParams *commonparams.BackupRetentionPolicyParams) int64 {
	if newParams.BackupMinimumEnforcedRetentionDuration != nil {
		return *newParams.BackupMinimumEnforcedRetentionDuration
	}

	if currentAttrs.BackupMinimumEnforcedRetentionDuration != nil {
		return *currentAttrs.BackupMinimumEnforcedRetentionDuration
	}

	return 0
}

// extractRetentionPolicyParams extracts retention policy parameters from the request
func extractRetentionPolicyParams(req *gcpgenserver.BackupVaultUpdateV1beta) *commonparams.BackupRetentionPolicyParams {
	var backupMinimumEnforcedRetentionDuration *int64
	var dailyBackupImmutable, weeklyBackupImmutable, monthlyBackupImmutable, adhocBackupImmutable *bool

	if req.BackupRetentionPolicy.IsSet() && req.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.IsSet() {
		val := int64(req.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.Value)
		backupMinimumEnforcedRetentionDuration = &val
	}
	if req.BackupRetentionPolicy.IsSet() && req.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet() {
		val := req.BackupRetentionPolicy.Value.DailyBackupImmutable.Value
		dailyBackupImmutable = &val
	}
	if req.BackupRetentionPolicy.IsSet() && req.BackupRetentionPolicy.Value.WeeklyBackupImmutable.IsSet() {
		val := req.BackupRetentionPolicy.Value.WeeklyBackupImmutable.Value
		weeklyBackupImmutable = &val
	}
	if req.BackupRetentionPolicy.IsSet() && req.BackupRetentionPolicy.Value.MonthlyBackupImmutable.IsSet() {
		val := req.BackupRetentionPolicy.Value.MonthlyBackupImmutable.Value
		monthlyBackupImmutable = &val
	}
	if req.BackupRetentionPolicy.IsSet() && req.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet() {
		val := req.BackupRetentionPolicy.Value.ManualBackupImmutable.Value
		adhocBackupImmutable = &val
	}

	return &commonparams.BackupRetentionPolicyParams{
		BackupMinimumEnforcedRetentionDuration: backupMinimumEnforcedRetentionDuration,
		IsDailyBackupImmutable:                 dailyBackupImmutable,
		IsWeeklyBackupImmutable:                weeklyBackupImmutable,
		IsMonthlyBackupImmutable:               monthlyBackupImmutable,
		IsAdhocBackupImmutable:                 adhocBackupImmutable,
	}
}

func (h Handler) V1betaCreateBackupVault(ctx context.Context, req *gcpgenserver.BackupVaultCreateV1beta, params gcpgenserver.V1betaCreateBackupVaultParams) (gcpgenserver.V1betaCreateBackupVaultRes, error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	// Validate cross-project vault creation: crossProjectVault and tenantProject must both be present together
	hasCrossProjectFlag := req.CrossProjectVault.IsSet() && req.CrossProjectVault.Value
	hasTenantProject := req.TenantProject.IsSet() && req.TenantProject.Value != ""

	if hasTenantProject && !hasCrossProjectFlag {
		return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
			Code:    400,
			Message: "crossProjectVault must be set to true when tenantProject is provided.",
		}, nil
	}
	if hasCrossProjectFlag && !hasTenantProject {
		return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
			Code:    400,
			Message: "tenantProject is required when creating a cross-project backup vault.",
		}, nil
	}

	isCrossProjectVault := hasCrossProjectFlag && hasTenantProject
	if isCrossProjectVault {
		if !GCBDRVaultEnabled {
			return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
				Code:    400,
				Message: "Cross-project backup vault creation is not enabled.",
			}, nil
		}
		if !GCBDRVaultCMEKEnabled &&
			req.KmsConfigResourcePath.IsSet() && req.KmsConfigResourcePath.Value != "" {
			return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
				Code:    400,
				Message: "CMEK is not supported for cross-project backup vaults.",
			}, nil
		}
		if req.BackupRetentionPolicy.IsSet() &&
			req.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.IsSet() &&
			req.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.Value > 0 {
			return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
				Code:    400,
				Message: "Immutable backup vaults are not supported for cross-project backup vaults.",
			}, nil
		}
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Validate immutability settings if backup retention policy is provided
	if req.BackupRetentionPolicy.IsSet() {
		policy := req.BackupRetentionPolicy.Value

		// Extract retention days and daily backup immutable flag
		var retentionDays int = 0
		var isDailyBackupImmutable bool = false

		if policy.BackupMinimumEnforcedRetentionDays.IsSet() {
			retentionDays = policy.BackupMinimumEnforcedRetentionDays.Value
		}

		if policy.DailyBackupImmutable.IsSet() {
			isDailyBackupImmutable = policy.DailyBackupImmutable.Value
		}

		// Validate retention period based on daily backup immutability
		if retentionDays > 0 {
			if isDailyBackupImmutable {
				if retentionDays > commonparams.ImmutablePeriodInDaysMaxDailyEnabled {
					return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
						Code:    400,
						Message: fmt.Sprintf("Retention period in days should be ≤ %d when considering daily backups for retention.", commonparams.ImmutablePeriodInDaysMaxDailyEnabled),
					}, nil
				}
			} else {
				if retentionDays > commonparams.ImmutablePeriodInDaysMax {
					return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
						Code:    400,
						Message: fmt.Sprintf("Retention period in days should be ≤ %d when considering no daily backups for retention.", commonparams.ImmutablePeriodInDaysMax),
					}, nil
				}
			}
		}
	}

	var resourceID string
	if req.ResourceId.IsSet() {
		resourceID = req.ResourceId.Value
	} else {
		resourceID = "" // Or handle the unset case appropriately
	}
	req.ResourceId.Value = resourceID
	var description string
	if req.Description.IsSet() {
		description = req.Description.Value
	}
	req.Description.Value = description

	var backupRegion *string
	if req.BackupRegion.IsSet() {
		backupRegion = &req.BackupRegion.Value
	}
	if err := h.Orchestrator.PersistAccountTrialMetadataIfSet(ctx, params.ProjectNumber, newTrialModeParamsFromOpt(req.TrialMode)); err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
				Code:    http.StatusBadRequest,
				Message: err.Error(),
			}, nil
		}
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: err.Error(),
		}, nil
	}
	// Check if the BackupVault already exists
	existingBackupVault, err := h.Orchestrator.GetBackupVaultByNameAndOwnerID(ctx, req.ResourceId.Value, params.ProjectNumber)
	if err == nil && existingBackupVault != nil {
		logger.Infof("backupVault with name: %s already exists ", resourceID)
		resp := convertCoreModelsToBackupVaultV1beta(existingBackupVault)
		bvJSON, err := encodeBackupVaultConfigV1(resp)
		if err != nil {
			logger.Error("Failed to marshal backup vault", "error", err)
			return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: "Failed to marshal Backup vault",
			}, err
		}
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.OptString{Value: "operation-id"},
			Done:     gcpgenserver.NewOptBool(true),
			Response: bvJSON,
		}, nil
	} else if err != nil && !errors.IsNotFoundErr(err) {
		logger.Error("Failed to check existing backupVault", "error", err)
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to check existing Backup vault",
		}, err
	}

	if !utils.IsCVPHostConfigured() || isCrossProjectVault {
		// VCP-only region (CVP_HOST not set): create backup vault in VCP via workflow
		createParams := buildCreateBackupVaultParams(req, params, backupRegion)
		vault, err := h.Orchestrator.CreateBackupVault(ctx, createParams)
		if err != nil {
			if errors.IsUserInputValidationErr(err) {
				return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
					Code:    400,
					Message: err.Error(),
				}, nil
			}
			logger.Error("Failed to create backup vault", "error", err)
			return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, err
		}
		resp := convertCoreModelsToBackupVaultV1beta(vault)
		bvJSON, err := encodeBackupVaultConfigV1(resp)
		if err != nil {
			logger.Errorf("Failed to marshal backup vault. Error: %v", err.Error())
			return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: "Failed to marshal Backup vault",
			}, err
		}
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(vault.Name),
			Done:     gcpgenserver.NewOptBool(true),
			Response: bvJSON,
		}, nil
	}

	// USE_VCP_REGION is off: call SDE to create the backup vault.
	// When ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION is on, VCP generates the UUID and sends it to SDE (guaranteeing a 1-1 mapping).
	body := &models.BackupVaultCreateV1beta{
		BackupRegion: backupRegion,
		Description:  &req.Description.Value,
		ResourceID:   req.ResourceId.Value,
	}
	brPolicy := convertBackupRetentionPolicyToCvpModelForCreate(req.BackupRetentionPolicy)
	if brPolicy != nil {
		body.BackupRetentionPolicy = brPolicy
	}
	if req.KmsConfigResourcePath.IsSet() {
		body.KmsConfigResourcePath = &req.KmsConfigResourcePath.Value
	}
	if req.BackupsPrimaryKeyVersion.IsSet() {
		body.BackupsPrimaryKeyVersion = &req.BackupsPrimaryKeyVersion.Value
	}

	var backupVaultUUID string
	if env.EnableBackupVaultVCPOrchestration {
		backupVaultUUID = utils.RandomUUID()
		body.BackupVaultID = &backupVaultUUID
	}

	vault, errRes, err := createBackupVaultInSDE(ctx, body, params.LocationId, params.ProjectNumber, logger)
	if errRes != nil {
		return errRes, nil
	}
	if err != nil {
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	// VCP orchestration: SDE create succeeded with the supplied UUID; persist in VCP DB with the same
	// UUID and, for cross-region vaults, hydrate to the destination region via CreateBackupVaultWorkflow.
	if env.EnableBackupVaultVCPOrchestration {
		createParams := buildCreateBackupVaultParams(req, params, backupRegion)
		createParams.BackupVaultID = backupVaultUUID
		vcpVault, err := h.Orchestrator.CreateBackupVault(ctx, createParams)
		if err != nil {
			if errors.IsUserInputValidationErr(err) {
				return &gcpgenserver.V1betaCreateBackupVaultBadRequest{
					Code:    400,
					Message: err.Error(),
				}, nil
			}
			logger.Errorf("Failed to create backup vault in VCP. Error: %v", err.Error())
			return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, err
		}
		bvJSON, err := jsonMarshal(convertCoreToCvpBackupVault(vcpVault))
		if err != nil {
			logger.Errorf("Failed to marshal backup vault. Error: %v", err.Error())
			return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
				Code:    500,
				Message: "Failed to marshal Backup vault",
			}, err
		}
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(vault.Payload.Name),
			Done:     gcpgenserver.NewOptBool(true),
			Response: bvJSON,
		}, nil
	}

	responseBytes, err := json.MarshalIndent(vault.Payload.Response, "", "  ")
	if err != nil {
		logger.Error("Failed to marshal response from SDE BackupVault creation", "error", err)
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to marshal response from SDE BackupVault creation",
		}, err
	}
	data := models.BackupVaultV1beta{}
	err = utilsConvertJsonToModel(responseBytes, &data)
	if err != nil {
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to convert response from SDE BackupVault creation to model",
		}, err
	}

	bvJSON, err := jsonMarshal(data)
	if err != nil {
		logger.Error("Failed to marshal backup vault", err.Error())
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to marshal Backup vault",
		}, err
	}
	return &gcpgenserver.OperationV1beta{
		Name:     gcpgenserver.NewOptString(vault.Payload.Name),
		Done:     gcpgenserver.NewOptBool(true),
		Response: bvJSON,
	}, nil
}

func buildCreateBackupVaultParams(req *gcpgenserver.BackupVaultCreateV1beta, params gcpgenserver.V1betaCreateBackupVaultParams, backupRegion *string) *commonparams.CreateBackupVaultParams {
	createParams := &commonparams.CreateBackupVaultParams{
		ResourceId:    req.ResourceId.Value,
		Description:   req.Description.Value,
		BackupRegion:  backupRegion,
		LocationId:    params.LocationId,
		ProjectNumber: params.ProjectNumber,
	}
	if req.BackupRetentionPolicy.IsSet() {
		br := req.BackupRetentionPolicy.Value
		if br.BackupMinimumEnforcedRetentionDays.IsSet() {
			d := int64(br.BackupMinimumEnforcedRetentionDays.Value)
			createParams.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration = &d
		}
		if br.DailyBackupImmutable.IsSet() {
			createParams.BackupRetentionPolicy.IsDailyBackupImmutable = &br.DailyBackupImmutable.Value
		}
		if br.ManualBackupImmutable.IsSet() {
			createParams.BackupRetentionPolicy.IsAdhocBackupImmutable = &br.ManualBackupImmutable.Value
		}
		if br.MonthlyBackupImmutable.IsSet() {
			createParams.BackupRetentionPolicy.IsMonthlyBackupImmutable = &br.MonthlyBackupImmutable.Value
		}
		if br.WeeklyBackupImmutable.IsSet() {
			createParams.BackupRetentionPolicy.IsWeeklyBackupImmutable = &br.WeeklyBackupImmutable.Value
		}
	}
	if req.KmsConfigResourcePath.IsSet() {
		createParams.KmsConfigResourcePath = &req.KmsConfigResourcePath.Value
	}
	if req.BackupsPrimaryKeyVersion.IsSet() {
		createParams.BackupsPrimaryKeyVersion = &req.BackupsPrimaryKeyVersion.Value
	}
	if req.TenantProject.IsSet() {
		createParams.TenantProject = &req.TenantProject.Value
	}
	return createParams
}

func convertBackupRetentionPolicyToCvpModelForCreate(brPolicy gcpgenserver.OptBackupRetentionPolicyV1beta) *models.BackupRetentionPolicyV1beta {
	if brPolicy.IsSet() {
		brPolicyValue := brPolicy.Value
		brModel := &models.BackupRetentionPolicyV1beta{}
		if brPolicyValue.BackupMinimumEnforcedRetentionDays.IsSet() {
			retentionDays := int64(brPolicyValue.BackupMinimumEnforcedRetentionDays.Value)
			brModel.BackupMinimumEnforcedRetentionDays = &retentionDays
		}
		if brPolicy.Value.DailyBackupImmutable.IsSet() {
			brModel.DailyBackupImmutable = brPolicyValue.DailyBackupImmutable.Value
		}
		if brPolicy.Value.ManualBackupImmutable.IsSet() {
			brModel.ManualBackupImmutable = brPolicyValue.ManualBackupImmutable.Value
		}
		if brPolicy.Value.MonthlyBackupImmutable.IsSet() {
			brModel.MonthlyBackupImmutable = brPolicyValue.MonthlyBackupImmutable.Value
		}
		if brPolicy.Value.WeeklyBackupImmutable.IsSet() {
			brModel.WeeklyBackupImmutable = brPolicyValue.WeeklyBackupImmutable.Value
		}
		return brModel
	}
	return nil
}

func _deleteBackupVaultInSDE(ctx context.Context, params gcpgenserver.V1betaDeleteBackupVaultParams, logger log.Logger) (r gcpgenserver.V1betaDeleteBackupVaultRes, _ error) {
	return sde.DeleteSDEBackupVault(ctx, params.LocationId, params.ProjectNumber, params.XCorrelationID.Value, params.BackupVaultId, logger)
}

func _createBackupVaultInSDE(ctx context.Context, body *models.BackupVaultCreateV1beta, locationID, projectNumber string, logger log.Logger) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
	return sde.CreateSDEBackupVault(ctx, body, locationID, projectNumber, logger)
}

func (h Handler) V1betaDeleteBackupVault(ctx context.Context, params gcpgenserver.V1betaDeleteBackupVaultParams) (r gcpgenserver.V1betaDeleteBackupVaultRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaDeleteBackupVaultBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaDeleteBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	_, err := h.Orchestrator.GetBackupVaultByUUID(ctx, params.BackupVaultId, params.ProjectNumber)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaDeleteBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
		// Backup vault not found in the VCP DB.
		if !utils.IsCVPHostConfigured() {
			// SDE does not exist in this region, so there is nothing else to delete.
			return &gcpgenserver.V1betaDeleteBackupVaultNotFound{
				Code:    404,
				Message: "Backup vault not found",
			}, nil
		}
		if !env.EnableBackupVaultVCPOrchestration {
			// Legacy path: VCP does not orchestrate deletes, so let SDE handle both
			// in-region and cross-region BV cases through pubsub.
			sdeBvResponse, err := deleteBackupVaultInSDE(ctx, params, logger)
			if err != nil {
				return nil, err
			}
			return sdeBvResponse, nil
		}
	}

	param := &commonparams.BackupVaultParams{
		BackupVaultID: params.BackupVaultId,
		AccountName:   params.ProjectNumber,
		OwnerID:       params.ProjectNumber,
		Region:        params.LocationId,
	}
	_, operationID, err := h.Orchestrator.DeleteBackupVault(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			logger.Error("Failed to update backupVault", err.Error())
			return &gcpgenserver.V1betaDeleteBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to delete backupVault", err.Error())
		return &gcpgenserver.V1betaDeleteBackupVaultInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name: gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Done: gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{}, nil
}

func (h Handler) V1betaDescribeBackupVault(ctx context.Context, params gcpgenserver.V1betaDescribeBackupVaultParams) (r gcpgenserver.V1betaDescribeBackupVaultRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaDescribeBackupVaultBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)

	vcpBackupVaultDetails, err := h.Orchestrator.GetBackupVaultByUUID(ctx, params.BackupVaultId, params.ProjectNumber)
	if !utils.IsCVPHostConfigured() {
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.V1betaDescribeBackupVaultNotFound{
					Code:    404,
					Message: "Backup vault not found",
				}, nil
			}
			return &gcpgenserver.V1betaDescribeBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
		response := convertCoreModelsToBackupVaultV1beta(vcpBackupVaultDetails)
		return response, nil
	}

	if err != nil && !errors.IsNotFoundErr(err) {
		return &gcpgenserver.V1betaDescribeBackupVaultInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	if vcpBackupVaultDetails != nil {
		response := convertCoreModelsToBackupVaultV1beta(vcpBackupVaultDetails)
		return response, nil
	}

	// VCP not found: try CVP; if CVP also not found, return 404.
	describeParams := &backup_vault.V1betaDescribeBackupVaultParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
		BackupVaultID:  params.BackupVaultId,
	}
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpResponse, err := cvpClient.BackupVault.V1betaDescribeBackupVault(describeParams)
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaDescribeBackupVaultUnprocessableEntity:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultUnprocessableEntity{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaDescribeBackupVaultNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaDescribeBackupVaultInternalServerError{
				Code:    code,
				Message: msg,
			}, nil
		default:
			return &gcpgenserver.V1betaDescribeBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}

	// CVP found: return CVP payload (no VCP overlay when we fell back from VCP-not-found).
	response := convertBackupVaultV1Beta(cvpResponse.Payload)
	return &response, nil
}

func (h Handler) V1betaGetMultipleBackupVaults(ctx context.Context, req *gcpgenserver.BackupVaultUuidListV1beta, params gcpgenserver.V1betaGetMultipleBackupVaultsParams) (r gcpgenserver.V1betaGetMultipleBackupVaultsRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaGetMultipleBackupVaultsBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	if !utils.IsCVPHostConfigured() {
		vaultsDataModel, err := h.Orchestrator.GetMultipleBackupVaults(ctx, req.BackupVaultUuids)
		if err != nil {
			return &gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
		bvResponse := gcpgenserver.V1betaGetMultipleBackupVaultsOK{
			BackupVaults: []gcpgenserver.BackupVaultV1beta{},
		}
		for _, bv := range vaultsDataModel {
			converted := convertCoreModelsToBackupVaultV1beta(bv)
			if converted != nil {
				bvResponse.BackupVaults = append(bvResponse.BackupVaults, *converted)
			}
		}
		return &bvResponse, nil
	}
	body := &models.BackupVaultUUIDListV1beta{
		BackupVaultUUIDs: req.BackupVaultUuids,
	}
	listParams := &backup_vault.V1betaGetMultipleBackupVaultsParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
		Body:           body,
	}
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpResponse, err := cvpClient.BackupVault.V1betaGetMultipleBackupVaults(listParams)
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaGetMultipleBackupVaultsBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaGetMultipleBackupVaultsUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaGetMultipleBackupVaultsForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaGetMultipleBackupVaultsNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaGetMultipleBackupVaultsTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaGetMultipleBackupVaultsNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaGetMultipleBackupVaultsNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			return &gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}
	if cvpResponse == nil || cvpResponse.Payload == nil {
		return &gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError{
			Code:    500,
			Message: "unknown error during the get multiple backup vaults",
		}, nil
	}
	bvResponse := gcpgenserver.V1betaGetMultipleBackupVaultsOK{
		BackupVaults: []gcpgenserver.BackupVaultV1beta{},
	}
	vaultsDataModel, err := h.Orchestrator.GetMultipleBackupVaults(ctx, req.BackupVaultUuids)
	if err != nil {
		return &gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	vcpModels := make([]*models.BackupVaultV1beta, 0, len(vaultsDataModel))
	for _, bv := range vaultsDataModel {
		vcpModels = append(vcpModels, convertCoreToCvpBackupVault(bv))
	}
	res := removeDuplicateSDEBackupVault(vcpModels, cvpResponse.Payload.BackupVaults)
	for _, bv := range res {
		bvResponse.BackupVaults = append(bvResponse.BackupVaults, convertBackupVaultV1Beta(bv))
	}
	return &bvResponse, nil
}

func (h Handler) V1betaListBackupVaults(ctx context.Context, params gcpgenserver.V1betaListBackupVaultsParams) (r gcpgenserver.V1betaListBackupVaultsRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaListBackupVaultsBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	if !utils.IsCVPHostConfigured() {
		bvs, err := h.Orchestrator.ListBackupVaults(ctx, params.ProjectNumber)
		if err != nil {
			logger.Error("Failed to list backup vaults", "error", err)
			return &gcpgenserver.V1betaListBackupVaultsInternalServerError{
				Code:    500,
				Message: "failed to list backup vaults",
			}, nil
		}
		bvResponse := gcpgenserver.V1betaListBackupVaultsOK{
			BackupVaults: []gcpgenserver.BackupVaultV1beta{},
		}
		for _, bv := range bvs {
			converted := convertCoreModelsToBackupVaultV1beta(bv)
			if converted != nil {
				bvResponse.BackupVaults = append(bvResponse.BackupVaults, *converted)
			}
		}
		return &bvResponse, nil
	}
	listParams := &backup_vault.V1betaListBackupVaultsParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &params.XCorrelationID.Value,
	}
	jwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, jwtToken)
	cvpResponse, err := cvpClient.BackupVault.V1betaListBackupVaults(listParams)
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaListBackupVaultsBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaListBackupVaultsUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaListBackupVaultsForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaListBackupVaultsNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaListBackupVaultsTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaListBackupVaultsNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaListBackupVaultsNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			return &gcpgenserver.V1betaListBackupVaultsInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}
	// Converting CVP model to gcpgenserver.BackupVaultV1beta
	bvResponse := gcpgenserver.V1betaListBackupVaultsOK{
		BackupVaults: []gcpgenserver.BackupVaultV1beta{},
	}

	bvs, err := h.Orchestrator.ListBackupVaults(ctx, params.ProjectNumber)
	if err != nil {
		logger.Error("Failed to list backup vaults", "error", err)
		return &gcpgenserver.V1betaListBackupVaultsInternalServerError{
			Code:    500,
			Message: "failed to list backup vaults",
		}, nil
	}
	vcpModels := make([]*models.BackupVaultV1beta, 0, len(bvs))
	for _, bv := range bvs {
		vcpModels = append(vcpModels, convertCoreToCvpBackupVault(bv))
	}
	res := removeDuplicateSDEBackupVault(vcpModels, cvpResponse.Payload.BackupVaults)
	for _, bv := range res {
		bvResponse.BackupVaults = append(bvResponse.BackupVaults, convertBackupVaultV1Beta(bv))
	}
	return &bvResponse, nil
}

// removeDuplicateSDEBackupVault returns a list containing all VCP backup vaults and only those
// CVP backup vaults whose UUID is not present in VCP. Duplication is judged by backup vault
// UUID (BackupVaultID). The result has no duplicate UUIDs: VCP wins when present in both.
func removeDuplicateSDEBackupVault(vcpVaults []*models.BackupVaultV1beta, cvpVaults []*models.BackupVaultV1beta) []*models.BackupVaultV1beta {
	vcpUUIDs := make(map[string]struct{})
	for _, bv := range vcpVaults {
		if bv != nil && bv.BackupVaultID != "" {
			vcpUUIDs[bv.BackupVaultID] = struct{}{}
		}
	}
	out := make([]*models.BackupVaultV1beta, 0, len(vcpVaults)+len(cvpVaults))
	out = append(out, vcpVaults...)
	for _, bv := range cvpVaults {
		if bv == nil {
			continue
		}
		if _, inVCP := vcpUUIDs[bv.BackupVaultID]; !inVCP && bv.BackupVaultID != "" {
			out = append(out, bv)
		}
	}
	return out
}

func updateBackupVaultStateDetails(bvs []*coremodels.BackupVaultV1beta, cvpBvs []*models.BackupVaultV1beta) []*models.BackupVaultV1beta {
	// Create a map for quick lookup of cvpBvs by ResourceID
	cvpBvMap := make(map[string]*models.BackupVaultV1beta)
	for _, cvpBv := range cvpBvs {
		if cvpBv.ResourceID != nil {
			cvpBvMap[*cvpBv.ResourceID] = cvpBv
		}
	}

	// Update cvpBvs using the map
	for _, bv := range bvs {
		if cvpBv, exists := cvpBvMap[bv.Name]; exists {
			cvpBv.State = bv.LifeCycleState
			cvpBv.StateDetails = bv.LifeCycleStateDetails

			// Overlay CMEK state and backups key version from VCP when available.
			// This ensures external APIs follow VCP's view for attached vaults.
			if bv.EncryptionState != nil {
				cvpBv.EncryptionState = bv.EncryptionState
			}
			if bv.BackupsPrimaryKeyVersion != nil {
				cvpBv.BackupsPrimaryKeyVersion = bv.BackupsPrimaryKeyVersion
			}
			if bv.ServiceType == datamodel.ServiceTypeCrossProject {
				crossProject := true
				cvpBv.CrossProjectVault = &crossProject
			}
		}
	}

	// Return the updated slice
	return cvpBvs
}

// backfillBackupVaultFromSDE describes a backup vault from SDE, inserts it into the VCP database
// without CCFE hydration, and returns the core model view needed by the update flow.
// Used when VCP orchestration is ON and the vault is SDE-only (flag was OFF at creation time).
func backfillBackupVaultFromSDE(
	ctx context.Context,
	params gcpgenserver.V1betaUpdateBackupVaultParams,
	orchestrator factory.OrchestratorFactory,
	logger log.Logger,
) (*coremodels.BackupVaultV1beta, error) {
	sdeBV, err := sde.DescribeSDEBackupVault(ctx, params.LocationId, params.ProjectNumber, params.BackupVaultId, logger)
	if err != nil {
		if _, ok := err.(*backup_vault.V1betaDescribeBackupVaultNotFound); ok {
			return nil, errors.NewNotFoundErr("backup vault", &params.BackupVaultId)
		}
		return nil, fmt.Errorf("describe SDE backup vault %s: %w", params.BackupVaultId, err)
	}
	if sdeBV == nil {
		return nil, errors.NewNotFoundErr("backup vault", &params.BackupVaultId)
	}
	bvDataModel, err := activities.ConvertToBackupVaultDataModel(sdeBV, params.LocationId)
	if err != nil {
		return nil, fmt.Errorf("convert SDE backup vault %s to data model: %w", params.BackupVaultId, err)
	}
	if _, err = orchestrator.BackfillBackupVaultInVCP(ctx, bvDataModel, params.ProjectNumber); err != nil {
		return nil, fmt.Errorf("insert backup vault %s into VCP database: %w", params.BackupVaultId, err)
	}
	// Re-fetch so the caller gets the canonical core-model view (with correct account etc.).
	return orchestrator.GetBackupVaultByUUID(ctx, params.BackupVaultId, params.ProjectNumber)
}

func _updateBackupVaultInSDE(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := createClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)
	body := &models.BackupVaultUpdateV1beta{
		Description: &description,
	}
	brPolicy := convertBackupRetentionPolicyToCvpModelForUpdate(req.BackupRetentionPolicy)
	if brPolicy != nil {
		body.BackupRetentionPolicy = brPolicy
	}
	if req.KmsConfigResourcePath.IsSet() {
		body.KmsConfigResourcePath = &req.KmsConfigResourcePath.Value
	}
	if req.BackupsPrimaryKeyVersion.IsSet() {
		body.BackupsPrimaryKeyVersion = &req.BackupsPrimaryKeyVersion.Value
	}

	vault, err := cvpClient.BackupVault.V1betaUpdateBackupVault(&backup_vault.V1betaUpdateBackupVaultParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &xCorrelationID,
		BackupVaultID:  params.BackupVaultId,
		Body:           body,
	})
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaUpdateBackupVaultUnprocessableEntity:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultUnprocessableEntity{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultConflict:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultConflict{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaUpdateBackupVaultNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
				Code:    code,
				Message: msg,
			}, nil
		default:
			return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}
	return convertOperationToOperationV1Beta(vault.Payload), nil
}

func _validateBackupPoliciesForBackupVault(ctx context.Context, backupVault *coremodels.BackupVaultV1beta, newRetentionParams *commonparams.BackupRetentionPolicyParams, o factory.OrchestratorFactory) error {
	return _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, o, commonparams.MaxRetries, commonparams.RetryDelay)
}

func _validateBackupPoliciesForBackupVaultWithRetry(ctx context.Context, backupVault *coremodels.BackupVaultV1beta, newRetentionParams *commonparams.BackupRetentionPolicyParams, o factory.OrchestratorFactory, maxRetries int, retryInterval time.Duration) error {
	logger := util.GetLogger(ctx)

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := _performBackupPolicyValidation(ctx, backupVault, newRetentionParams, o)
		if err == nil {
			return nil // Success
		}

		// Check if this is a retryable error (backup policy in updating state)
		if isBackupPolicyRetryableError(err) {
			if attempt < maxRetries {
				logger.Warn("Backup policy validation failed due to concurrent update, retrying",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"retryAfter", retryInterval,
					"error", err)
				commonparams.SleepFn(retryInterval)
				continue
			} else {
				logger.Error("Backup policy validation failed after all retry attempts",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"error", err)
				return err
			}
		}

		// Non-retryable error, return immediately
		return err
	}

	return fmt.Errorf("backup policy validation failed after %d attempts", maxRetries)
}

func isBackupPolicyRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Check if the error is related to backup policy being in updating state
	var customError *vsaerrors.CustomError
	if vsaerrors.As(err, &customError) {
		if customError.TrackingID == vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy {
			return true
		}
	}
	return false
}

func _performBackupPolicyValidation(ctx context.Context, backupVault *coremodels.BackupVaultV1beta, newRetentionParams *commonparams.BackupRetentionPolicyParams, o factory.OrchestratorFactory) error {
	logger := util.GetLogger(ctx)
	// Get all backup policy UUIDs associated with this backup vault
	backupPolicyUUIDs, err := o.GetBackupPolicyUUIDsFromBackupVaultUUID(ctx, backupVault.BackupVaultID, backupVault.AccountName)
	if err != nil {
		return fmt.Errorf("failed to get backup policy UUIDs from backup vault: %w", err)
	}

	// If no backup policies are associated, no validation needed
	if len(backupPolicyUUIDs) == 0 {
		logger.Info("No backup policies associated with backup vault", "backupVaultID", backupVault.BackupVaultID)
		return nil
	}

	// Fetch all backup policies for the given UUIDs (only non-deleted ones)
	_, backupPolicies, err := o.ListBackupPoliciesAndVolumeCount(ctx, backupVault.AccountName, backupPolicyUUIDs)
	if err != nil {
		return fmt.Errorf("failed to list backup policies for validation: %v", err)
	}

	logger.Info("Found backup policies for validation", "count", len(backupPolicies), "backupVaultID", backupVault.BackupVaultID)

	// Validate each backup policy
	for _, backupPolicy := range backupPolicies {
		if backupPolicy.State != datamodel.LifeCycleStateDeleted {
			// Check if backup policy is in updating state
			if backupPolicy.State == datamodel.LifeCycleStateUpdating {
				return vsaerrors.NewVCPError(vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy, fmt.Errorf("Cannot update backup vault: backup policy '%s' is currently being updated. Please wait for the policy update to complete.", backupPolicy.BackupPolicyUUID))
			}
			// Validate the backup policy against new immutability settings
			if err := validateBackupPolicyAgainstNewSettings(backupVault, backupPolicy, newRetentionParams); err != nil {
				return fmt.Errorf("backup policy '%s' validation failed: %w", backupPolicy.BackupPolicyUUID, err)
			}
		}
	}

	return nil
}

func validateBackupPolicyAgainstNewSettings(backupVault *coremodels.BackupVaultV1beta, backupPolicy *coremodels.BackupPolicy, newRetentionParams *commonparams.BackupRetentionPolicyParams) error {
	// Get current immutable attributes from BackupRetentionPolicy
	currentAttrs := &backupVault.BackupRetentionPolicy

	// Determine final retention days
	var finalRetentionDays int64 = 0
	if newRetentionParams.BackupMinimumEnforcedRetentionDuration != nil {
		finalRetentionDays = *newRetentionParams.BackupMinimumEnforcedRetentionDuration
	} else if currentAttrs.BackupMinimumEnforcedRetentionDuration != nil {
		finalRetentionDays = *currentAttrs.BackupMinimumEnforcedRetentionDuration
	}

	if finalRetentionDays <= 0 {
		return nil // No immutable period specified
	}

	// Determine final immutability states
	finalDailyImmutable := determineImmutableState(currentAttrs.IsDailyBackupImmutable, newRetentionParams.IsDailyBackupImmutable)
	finalWeeklyImmutable := determineImmutableState(currentAttrs.IsWeeklyBackupImmutable, newRetentionParams.IsWeeklyBackupImmutable)
	finalMonthlyImmutable := determineImmutableState(currentAttrs.IsMonthlyBackupImmutable, newRetentionParams.IsMonthlyBackupImmutable)

	backupPolicyParams := &commonparams.BackupPolicyParams{
		DailyBackupsToKeep:   backupPolicy.DailyBackupLimit,
		WeeklyBackupsToKeep:  backupPolicy.WeeklyBackupLimit,
		MonthlyBackupsToKeep: backupPolicy.MonthlyBackupLimit,
	}
	retentionPolicyParams := &commonparams.BackupRetentionPolicyParams{
		BackupMinimumEnforcedRetentionDuration: &finalRetentionDays,
		IsDailyBackupImmutable:                 &finalDailyImmutable,
		IsWeeklyBackupImmutable:                &finalWeeklyImmutable,
		IsMonthlyBackupImmutable:               &finalMonthlyImmutable,
	}

	err := commonparams.ValidateBackupPolicyRetentionLimits(backupPolicyParams, retentionPolicyParams)
	if err != nil {
		return fmt.Errorf("backup policy '%s' has retention limits that conflict with the new backup vault settings", backupPolicy.BackupPolicyUUID)
	}

	return nil
}

func (h Handler) V1betaUpdateBackupVault(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}
	var description string
	if req.Description.IsSet() {
		description = req.Description.Value
	}
	backupVault, err := h.Orchestrator.GetBackupVaultByUUID(ctx, params.BackupVaultId, params.ProjectNumber)
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
		if !utils.IsCVPHostConfigured() {
			return &gcpgenserver.ErrorStatusCode{
				StatusCode: 404,
				Response:   gcpgenserver.Error{Code: 404, Message: "Backup vault not found"},
			}, nil
		}
		if !env.EnableBackupVaultVCPOrchestration {
			// Flag off: proxy directly to SDE
			sdeBvResponse, sdeErr := updateBackupVaultInSDE(ctx, req, params, description)
			if sdeErr != nil {
				return nil, sdeErr
			}
			return sdeBvResponse, nil
		}
		// Flag on: backfill the SDE-only vault into VCP DB, then fall through to the normal workflow.
		logger.Info("Backup vault not in VCP DB; backfilling from SDE before update", "backupVaultId", params.BackupVaultId)
		backupVault, err = backfillBackupVaultFromSDE(ctx, params, h.Orchestrator, logger)
		if err != nil {
			logger.Error("Failed to backfill backup vault from SDE", "backupVaultId", params.BackupVaultId, "error", err)
			if errors.IsNotFoundErr(err) {
				return &gcpgenserver.ErrorStatusCode{
					StatusCode: 404,
					Response:   gcpgenserver.Error{Code: 404, Message: "Backup vault not found"},
				}, nil
			}
			return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
				Code:    500,
				Message: fmt.Sprintf("failed to register backup vault %s from SDE: %s", params.BackupVaultId, err.Error()),
			}, nil
		}
	}

	// CMEK Policy Update Restriction: Prevent adding or changing KmsConfigResourcePath
	if req.KmsConfigResourcePath.IsSet() {
		requestedKmsConfigPath := req.KmsConfigResourcePath.Value
		currentKmsConfigPath := backupVault.KmsConfigResourcePath

		// Case 1: Trying to add CMEK to a backup vault that doesn't have CMEK
		if currentKmsConfigPath == nil {
			return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
				Code:    400,
				Message: "CMEK Policy cannot be updated on Backup vault",
			}, nil
		}

		// Case 2: Trying to change to a different KmsConfigResourcePath
		if *currentKmsConfigPath != requestedKmsConfigPath {
			return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
				Code:    400,
				Message: "CMEK Policy cannot be updated on Backup vault",
			}, nil
		}
	}

	// Extract retention policy parameters from request
	retentionParams := extractRetentionPolicyParams(req)
	if retentionParams == nil {
		retentionParams = &commonparams.BackupRetentionPolicyParams{}
	}
	if req.BackupRetentionPolicy.IsSet() {
		if !utils.IsImmutableBackupEnabled() {
			// Check if backup retention policy is being updated and vault is attached to volumes
			isAttached, err := h.Orchestrator.IsBackupVaultAttachedToVolume(ctx, params.BackupVaultId)
			if err != nil {
				logger.Errorf("Failed to check if backup vault %s is attached to vsa volume: %v", params.BackupVaultId, err)
				return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
					Code:    500,
					Message: "Failed to check backup vault attachment status",
				}, nil
			}
			if isAttached {
				return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
					Code:    400,
					Message: utils.ImmutableBackupVaultErrMsg,
				}, nil
			}
		} else {
			// Validate retention parameters against current backup vault
			if validationErr := _validateRetentionParameters(backupVault, retentionParams); validationErr != nil {
				return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
					Code:    400,
					Message: validationErr.Error(),
				}, nil
			}
			// Validate all associated backup policies
			logger.Info("Validating backup policies associated with backup vault", "backupVaultID", params.BackupVaultId)
			if policyValidationErr := _validateBackupPoliciesForBackupVault(ctx, backupVault, retentionParams, h.Orchestrator); policyValidationErr != nil {
				logger.Error("Backup policy validation failed", "error", policyValidationErr)
				return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
					Code:    400,
					Message: policyValidationErr.Error(),
				}, nil
			}
		}
	}

	param := &commonparams.BackupVaultParams{
		BackupVaultID:         params.BackupVaultId,
		Description:           &description,
		OwnerID:               params.ProjectNumber,
		Region:                params.LocationId,
		BackupRetentionPolicy: *retentionParams,
		Name:                  backupVault.Name,
		AccountName:           params.ProjectNumber,
	}
	updated, operationID, err := h.Orchestrator.UpdateBackupVault(ctx, param)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			logger.Error("Failed to update backupVault", err.Error())
			return &gcpgenserver.V1betaUpdateBackupVaultBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		logger.Error("Failed to update backupVault", err.Error())
		return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}
	resp := convertCoreModelsToBackupVaultV1beta(updated)
	bvJSON, err := encodeBackupVaultConfigV1(resp)
	if err != nil {
		logger.Error("Failed to marshal backup vault", err.Error())
		return &gcpgenserver.V1betaUpdateBackupVaultInternalServerError{
			Code:    500,
			Message: "Failed to marshal Backup vault",
		}, nil
	}
	if operationID != "" {
		return &gcpgenserver.OperationV1beta{
			Name:     gcpgenserver.NewOptString(fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID)),
			Response: bvJSON,
			Done:     gcpgenserver.NewOptBool(false),
		}, nil
	}
	return &gcpgenserver.OperationV1beta{}, nil
}

func convertBackupRetentionPolicyToCvpModelForUpdate(brPolicy gcpgenserver.OptBackupRetentionPolicyUpdateV1beta) *models.BackupRetentionPolicyUpdateV1beta {
	if brPolicy.IsSet() {
		brPolicyValue := brPolicy.Value
		brModel := &models.BackupRetentionPolicyUpdateV1beta{}
		if brPolicyValue.BackupMinimumEnforcedRetentionDays.IsSet() {
			retentionDays := int64(brPolicyValue.BackupMinimumEnforcedRetentionDays.Value)
			brModel.BackupMinimumEnforcedRetentionDays = &retentionDays
		}
		if brPolicy.Value.DailyBackupImmutable.IsSet() {
			brModel.DailyBackupImmutable = &brPolicyValue.DailyBackupImmutable.Value
		}
		if brPolicy.Value.ManualBackupImmutable.IsSet() {
			brModel.ManualBackupImmutable = &brPolicyValue.ManualBackupImmutable.Value
		}
		if brPolicy.Value.MonthlyBackupImmutable.IsSet() {
			brModel.MonthlyBackupImmutable = &brPolicyValue.MonthlyBackupImmutable.Value
		}
		if brPolicy.Value.WeeklyBackupImmutable.IsSet() {
			brModel.WeeklyBackupImmutable = &brPolicyValue.WeeklyBackupImmutable.Value
		}
		return brModel
	}
	return nil
}

func convertBackupVaultV1Beta(bv *models.BackupVaultV1beta) gcpgenserver.BackupVaultV1beta {
	backupRetentionPolicy := gcpgenserver.BackupRetentionPolicyV1beta{}
	if bv.BackupRetentionPolicy != nil {
		if bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays != nil {
			backupRetentionPolicy.BackupMinimumEnforcedRetentionDays = gcpgenserver.NewOptInt(int(*bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays))
		}
		if bv.BackupRetentionPolicy.DailyBackupImmutable {
			backupRetentionPolicy.DailyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if bv.BackupRetentionPolicy.ManualBackupImmutable {
			backupRetentionPolicy.ManualBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if bv.BackupRetentionPolicy.MonthlyBackupImmutable {
			backupRetentionPolicy.MonthlyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
		if bv.BackupRetentionPolicy.WeeklyBackupImmutable {
			backupRetentionPolicy.WeeklyBackupImmutable = gcpgenserver.NewOptBool(true)
		}
	}
	convertedBackupVault := gcpgenserver.BackupVaultV1beta{
		BackupVaultId:         gcpgenserver.NewOptString(bv.BackupVaultID),
		State:                 gcpgenserver.NewOptBackupVaultV1betaState(gcpgenserver.BackupVaultV1betaState(bv.State)),
		StateDetails:          gcpgenserver.NewOptString(bv.StateDetails),
		CreatedAt:             gcpgenserver.NewOptDateTime(time.Time(bv.CreatedAt)),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(backupRetentionPolicy),
	}
	if bv.BackupRegion != nil {
		convertedBackupVault.BackupRegion = gcpgenserver.NewOptString(*bv.BackupRegion)
	}
	if bv.DestinationBackupVault != nil {
		convertedBackupVault.DestinationBackupVault = gcpgenserver.NewOptString(*bv.DestinationBackupVault)
	}
	if bv.SourceBackupVault != nil {
		convertedBackupVault.SourceBackupVault = gcpgenserver.NewOptString(*bv.SourceBackupVault)
	}
	if bv.SourceRegion != nil {
		convertedBackupVault.SourceRegion = gcpgenserver.NewOptString(*bv.SourceRegion)
	}
	if bv.Description != nil {
		convertedBackupVault.Description = gcpgenserver.NewOptString(*bv.Description)
	}
	if bv.ResourceID != nil {
		convertedBackupVault.ResourceId = *bv.ResourceID
	}
	if bv.BackupVaultType != nil {
		convertedBackupVault.BackupVaultType = gcpgenserver.NewOptBackupVaultV1betaBackupVaultType(gcpgenserver.BackupVaultV1betaBackupVaultType(*bv.BackupVaultType))
	}
	if bv.KmsConfigResourcePath != nil {
		convertedBackupVault.KmsConfigResourcePath = gcpgenserver.NewOptString(*bv.KmsConfigResourcePath)
	}
	if bv.BackupsPrimaryKeyVersion != nil {
		convertedBackupVault.BackupsPrimaryKeyVersion = gcpgenserver.NewOptString(*bv.BackupsPrimaryKeyVersion)
	}
	if bv.EncryptionState != nil {
		convertedBackupVault.EncryptionState = gcpgenserver.NewOptBackupVaultV1betaEncryptionState(gcpgenserver.BackupVaultV1betaEncryptionState(*bv.EncryptionState))
	}
	if bv.CrossProjectVault != nil {
		convertedBackupVault.CrossProjectVault = gcpgenserver.NewOptBool(*bv.CrossProjectVault)
	}
	return convertedBackupVault
}

func convertCoreToCvpBackupVault(coreBV *coremodels.BackupVaultV1beta) *models.BackupVaultV1beta {
	backupRetentionPolicy := &models.BackupRetentionPolicyV1beta{
		BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(0),
	}
	if coreBV.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil ||
		coreBV.BackupRetentionPolicy.IsDailyBackupImmutable ||
		coreBV.BackupRetentionPolicy.IsWeeklyBackupImmutable ||
		coreBV.BackupRetentionPolicy.IsMonthlyBackupImmutable ||
		coreBV.BackupRetentionPolicy.IsAdhocBackupImmutable {
		backupRetentionPolicy = &models.BackupRetentionPolicyV1beta{
			BackupMinimumEnforcedRetentionDays: coreBV.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration,
			DailyBackupImmutable:               coreBV.BackupRetentionPolicy.IsDailyBackupImmutable,
			WeeklyBackupImmutable:              coreBV.BackupRetentionPolicy.IsWeeklyBackupImmutable,
			MonthlyBackupImmutable:             coreBV.BackupRetentionPolicy.IsMonthlyBackupImmutable,
			ManualBackupImmutable:              coreBV.BackupRetentionPolicy.IsAdhocBackupImmutable,
		}
	}

	// Create the CVP backup vault model
	cvpBV := &models.BackupVaultV1beta{
		BackupVaultID:            coreBV.BackupVaultID,
		ResourceID:               &coreBV.Name,
		Description:              coreBV.Description,
		BackupVaultType:          coreBV.BackupVaultType,
		State:                    coreBV.LifeCycleState,
		StateDetails:             coreBV.LifeCycleStateDetails,
		CreatedAt:                strfmt.DateTime(coreBV.CreatedAt),
		DeletedAt:                (*strfmt.DateTime)(coreBV.DeletedAt),
		BackupRetentionPolicy:    backupRetentionPolicy,
		KmsConfigResourcePath:    coreBV.KmsConfigResourcePath,
		BackupsPrimaryKeyVersion: coreBV.BackupsPrimaryKeyVersion,
		EncryptionState:          coreBV.EncryptionState,
	}
	if coreBV.BackupVaultType != nil && *coreBV.BackupVaultType == activities.CrossRegionBackupType {
		cvpBV.BackupRegion = coreBV.BackupRegion
		cvpBV.SourceRegion = coreBV.SourceRegion
		cvpBV.DestinationBackupVault = coreBV.DestinationBackupVault
		cvpBV.SourceBackupVault = coreBV.SourceBackupVault
	}
	if coreBV.ServiceType == datamodel.ServiceTypeCrossProject {
		cvpBV.CrossProjectVault = nillable.ToPointer(true)
	}
	return cvpBV
}

func convertCoreModelsToBackupVaultV1beta(beta *coremodels.BackupVaultV1beta) *gcpgenserver.BackupVaultV1beta {
	var (
		description, sourceBackupVault, destinationBackupVault, sourceRegion, backupRegion, backupVaultType string
	)
	var backupMinimumEnforcedRetentionDuration int
	if beta.Description != nil {
		description = *beta.Description
	}
	if beta.BackupVaultType != nil {
		backupVaultType = *beta.BackupVaultType
	}
	if beta.SourceBackupVault != nil {
		sourceBackupVault = *beta.SourceBackupVault
	}
	if beta.DestinationBackupVault != nil {
		destinationBackupVault = *beta.DestinationBackupVault
	}
	if beta.SourceRegion != nil {
		sourceRegion = *beta.SourceRegion
	}
	if beta.BackupRegion != nil {
		backupRegion = *beta.BackupRegion
	}
	if beta.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil {
		backupMinimumEnforcedRetentionDuration = int(*beta.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
	}
	result := &gcpgenserver.BackupVaultV1beta{
		BackupVaultId:   gcpgenserver.NewOptString(beta.BackupVaultID),
		State:           gcpgenserver.NewOptBackupVaultV1betaState(gcpgenserver.BackupVaultV1betaState(beta.LifeCycleState)),
		StateDetails:    gcpgenserver.NewOptString(beta.LifeCycleStateDetails),
		CreatedAt:       gcpgenserver.NewOptDateTime(time.Time(beta.CreatedAt)),
		Description:     gcpgenserver.NewOptString(description),
		ResourceId:      beta.Name,
		BackupVaultType: gcpgenserver.NewOptBackupVaultV1betaBackupVaultType(gcpgenserver.BackupVaultV1betaBackupVaultType(backupVaultType)),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
			BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(backupMinimumEnforcedRetentionDuration),
			DailyBackupImmutable:               gcpgenserver.NewOptBool(beta.BackupRetentionPolicy.IsDailyBackupImmutable),
			ManualBackupImmutable:              gcpgenserver.NewOptBool(beta.BackupRetentionPolicy.IsAdhocBackupImmutable),
			MonthlyBackupImmutable:             gcpgenserver.NewOptBool(beta.BackupRetentionPolicy.IsMonthlyBackupImmutable),
			WeeklyBackupImmutable:              gcpgenserver.NewOptBool(beta.BackupRetentionPolicy.IsWeeklyBackupImmutable),
		}),
	}

	if beta.BackupVaultType != nil && *beta.BackupVaultType == activities.CrossRegionBackupType {
		result.BackupRegion = gcpgenserver.NewOptString(backupRegion)
		result.SourceRegion = gcpgenserver.NewOptString(sourceRegion)
		result.DestinationBackupVault = gcpgenserver.NewOptString(destinationBackupVault)
		result.SourceBackupVault = gcpgenserver.NewOptString(sourceBackupVault)
		if beta.ExternalUUID != nil && *beta.ExternalUUID != "" {
			result.CrossRegionBackupVaultId = gcpgenserver.NewOptString(*beta.ExternalUUID)
		}
	}

	if beta.KmsConfigResourcePath != nil {
		result.KmsConfigResourcePath = gcpgenserver.NewOptString(*beta.KmsConfigResourcePath)
	}
	if beta.BackupsPrimaryKeyVersion != nil {
		result.BackupsPrimaryKeyVersion = gcpgenserver.NewOptString(*beta.BackupsPrimaryKeyVersion)
	}
	if beta.EncryptionState != nil {
		result.EncryptionState = gcpgenserver.NewOptBackupVaultV1betaEncryptionState(gcpgenserver.BackupVaultV1betaEncryptionState(*beta.EncryptionState))
	}
	if beta.ServiceType == datamodel.ServiceTypeCrossProject {
		result.CrossProjectVault = gcpgenserver.NewOptBool(true)
	}
	return result
}

func encodeBackupVaultConfigV1(BackupVault *gcpgenserver.BackupVaultV1beta) (jx.Raw, error) {
	data, err := jsonMarshal(BackupVault)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (h Handler) V1betaRotateCmekBackups(ctx context.Context, req *gcpgenserver.BackupVaultRotateCMEKBackupsV1beta, params gcpgenserver.V1betaRotateCmekBackupsParams) (r gcpgenserver.V1betaRotateCmekBackupsRes, _ error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaRotateCmekBackupsBadRequest{
			Code:    400,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}
	if !cmekBackupEnabled {
		return &gcpgenserver.V1betaRotateCmekBackupsBadRequest{
			Code:    400,
			Message: "CMEK backup feature is currently not enabled.",
		}, nil
	}
	logger := util.GetLogger(ctx)
	helper.AddLabelerAttributes(ctx, params.ProjectNumber, params.LocationId, nil)
	_, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId)
	if parsingErr != nil {
		return &gcpgenserver.V1betaRotateCmekBackupsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	// Try VCP path for VCP-tracked backup vaults.
	bv, err := h.Orchestrator.GetBackupVaultByUUID(ctx, params.BackupVaultId, params.ProjectNumber)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			if !utils.IsCVPHostConfigured() {
				return &gcpgenserver.V1betaRotateCmekBackupsNotFound{
					Code:    404,
					Message: "Backup vault not found",
				}, nil
			}
			// If backup vault is not in VCP, fall back to SDE rotation.
			return _rotateCmekBackupsInSDE(ctx, req, params, logger)
		}
		return &gcpgenserver.V1betaRotateCmekBackupsInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	// Sanity: ensure vault state allows rotation (basic check – workflow will also validate).
	if bv.LifeCycleState == datamodel.LifeCycleStateDeleting || bv.LifeCycleState == datamodel.LifeCycleStateUpdating {
		return &gcpgenserver.V1betaRotateCmekBackupsConflict{
			Code:    409,
			Message: "backup vault is in transition state",
		}, nil
	}

	// Build common backup vault params for orchestrator.
	bvParams := commonparams.BackupVaultParams{
		BackupVaultID: params.BackupVaultId,
		AccountName:   params.ProjectNumber,
		OwnerID:       params.ProjectNumber,
		Region:        params.LocationId,
		Name:          bv.Name,
	}

	operationID, err := h.Orchestrator.RotateCmekBackupsForBackupVault(ctx, &bvParams, req.PrimaryKeyVersion)
	if err != nil {
		if errors.IsUserInputValidationErr(err) {
			return &gcpgenserver.V1betaRotateCmekBackupsBadRequest{
				Code:    400,
				Message: err.Error(),
			}, nil
		}
		return &gcpgenserver.V1betaRotateCmekBackupsInternalServerError{
			Code:    500,
			Message: err.Error(),
		}, nil
	}

	// Return a long-running operation reference backed by the VCP job.
	return &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(
			fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID),
		),
		Done: gcpgenserver.NewOptBool(false),
	}, nil
}

// _rotateCmekBackupsInSDE delegates CMEK backup rotation to SDE/CVP for the given backup vault.
func _rotateCmekBackupsInSDE(
	ctx context.Context,
	req *gcpgenserver.BackupVaultRotateCMEKBackupsV1beta,
	params gcpgenserver.V1betaRotateCmekBackupsParams,
	logger log.Logger,
) (gcpgenserver.V1betaRotateCmekBackupsRes, error) {
	GetSignedJwtToken := utils.GetJWTTokenFromContext(ctx)
	cvpClient := sde.BackupVaultCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	body := &models.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: &req.PrimaryKeyVersion,
	}

	vault, err := cvpClient.BackupVault.V1betaRotateCmekBackups(&backup_vault.V1betaRotateCmekBackupsParams{
		LocationID:     params.LocationId,
		ProjectNumber:  params.ProjectNumber,
		XCorrelationID: &xCorrelationID,
		BackupVaultID:  params.BackupVaultId,
		Body:           body,
	})
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaRotateCmekBackupsBadRequest:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsBadRequest{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsUnauthorized:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsUnauthorized{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsForbidden:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsForbidden{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsNotFound:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsNotFound{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsConflict:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsConflict{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsUnprocessableEntity:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsUnprocessableEntity{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsTooManyRequests:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsTooManyRequests{
				Code:    code,
				Message: msg,
			}, nil
		case *backup_vault.V1betaRotateCmekBackupsNotImplemented:
			msg := nillable.GetString(&e.Payload.Message, "")
			code := float64(nillable.GetFloat64(&e.Payload.Code, 0))
			return &gcpgenserver.V1betaRotateCmekBackupsNotImplemented{
				Code:    code,
				Message: msg,
			}, nil
		default:
			return &gcpgenserver.V1betaRotateCmekBackupsInternalServerError{
				Code:    500,
				Message: err.Error(),
			}, nil
		}
	}
	return convertOperationToOperationV1Beta(vault.Payload), nil
}

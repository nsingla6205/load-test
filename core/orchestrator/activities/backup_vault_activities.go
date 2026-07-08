package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	retryutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
)

var (
	utilsConvertJsonToModel       = utils.ConvertJsonToModel
	ConvertToBackupVaultDataModel = _convertToBackupVaultDataModel
	cvpCreateClient               = cvp.CreateClient
	updateBackupVaultInSDE        = _updateBackupVaultInSDE
	deleteBackupVaultInSDE        = _deleteBackupVaultInSDE
	getBackupVaultFromSDE         = _getBackupVaultFromSDE
	utilsGetRemoteRegionConfig    = common.GetRemoteRegionConfig
	googleProxyClientGet          = googleproxyclient.GetGProxyClient
)

type BackupVaultActivity struct {
	SE                 database.Storage
	CmekMetricsEmitter metricsinterface.CmekBackupMetricsEmitter
}

// RotateBucketCmekActivity rotates CMEK for objects in a single GCS bucket.
func (j *BackupVaultActivity) RotateBucketCmekActivity(ctx context.Context, bucketName, primaryKeyVersion, ownerID, backupVaultUUID string) error {
	logger := util.GetLogger(ctx)

	pageSize := env.GetInt("CMEK_ROTATION_PAGE_SIZE", 1000)
	maxWorkers := env.GetInt("CMEK_ROTATION_MAX_WORKERS", 20)
	maxPasses := env.GetInt("MAX_CMEK_ROTATION_PASSES", 10)

	if bucketName == "" {
		bucketErr := fmt.Errorf("bucket name must not be empty")
		return temporal.NewNonRetryableApplicationError(
			"bucket name is empty for CMEK rotation",
			"RotateBucketCmekActivityInvalidBucket",
			bucketErr,
		)
	}

	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		logger.Errorf("Failed to get cloud service for CMEK rotation: %v", err)
		return errors.WrapAsTemporalApplicationError(err)
	}

	totalProcessed, totalRotated, err := cloudService.RotateBucketCmek(ctx, bucketName, primaryKeyVersion, pageSize, maxWorkers, maxPasses)
	if err != nil {
		logger.Errorf("Failed to rotate CMEK for bucket %s: %v", bucketName, err)
		isRetriable := retryutils.ShouldRetry(err)
		customErr := errors.NewVCPError(errors.ErrGCPResourceProvisionError, err)
		customErr.Retriable = isRetriable
		return errors.WrapAsTemporalApplicationError(customErr)
	}

	logger.Infof("CMEK rotation completed for bucket %s: totalProcessed=%d totalRotated=%d", bucketName, totalProcessed, totalRotated)
	return nil
}

// EmitCmekRotationFailureMetric emits a single Prometheus gauge metric for a
// CMEK rotation failure. It is called from the workflow (not inside retried
// activities) so that exactly one metric is emitted per rotation failure,
// regardless of how many Temporal activity retries occurred.
func (j *BackupVaultActivity) EmitCmekRotationFailureMetric(ctx context.Context, bucketName, ownerID, backupVaultUUID, failureType string) error {
	if j.CmekMetricsEmitter != nil {
		j.CmekMetricsEmitter.AddCMEKRewriteErrorResult(bucketName, ownerID, backupVaultUUID, failureType)
	}
	return nil
}

// UpdateBackupVaultCmekInVCPActivity updates CMEK metadata for a backup vault in
// the VCP database once all bucket CMEK rotations have completed successfully.
func (j *BackupVaultActivity) UpdateBackupVaultCmekInVCPActivity(ctx context.Context, backupVault *datamodel.BackupVault, primaryKeyVersion string) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, backupVault.UUID, backupVault.AccountID)
	if err != nil {
		logger.Errorf("Failed to load backup vault for CMEK metadata update in VCP: %v", err)
		return errors.WrapAsTemporalApplicationError(err)
	}

	updateBv := *dbBv
	if updateBv.CmekAttributes == nil {
		updateBv.CmekAttributes = &datamodel.CmekAttributes{}
	}

	updateBv.CmekAttributes.BackupsPrimaryKeyVersion = &primaryKeyVersion
	stateCompleted := datamodel.EncryptionStateCompleted
	updateBv.CmekAttributes.EncryptionState = &stateCompleted

	_, err = se.UpdateBackupVaultInVCP(ctx, &updateBv, dbBv)
	if err != nil {
		logger.Errorf("Failed to update backup vault CMEK metadata in VCP: %v", err)
		return temporal.NewNonRetryableApplicationError(
			"failed to update backup vault CMEK metadata in VCP",
			"UpdateBackupVaultCmekInVCPActivityError",
			err,
		)
	}

	return nil
}

// UpdateBackupVaultEncryptionStateInVCPActivity updates only the encryption state
// for a backup vault in the VCP database. It is used to mirror the CMEK rotation
// lifecycle (PENDING, IN_PROGRESS, FAILED) for VCP-managed rotations.
func (j *BackupVaultActivity) UpdateBackupVaultEncryptionStateInVCPActivity(ctx context.Context, backupVault *datamodel.BackupVault, encryptionState string) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, backupVault.UUID, backupVault.AccountID)
	if err != nil {
		logger.Errorf("Failed to load backup vault for encryption state update in VCP: %v", err)
		return errors.WrapAsTemporalApplicationError(err)
	}

	updateBv := *dbBv
	if updateBv.CmekAttributes == nil && dbBv.CmekAttributes != nil {
		updateBv.CmekAttributes = &datamodel.CmekAttributes{
			KmsConfigResourcePath:    dbBv.CmekAttributes.KmsConfigResourcePath,
			EncryptionState:          dbBv.CmekAttributes.EncryptionState,
			BackupsPrimaryKeyVersion: dbBv.CmekAttributes.BackupsPrimaryKeyVersion,
		}
	} else if updateBv.CmekAttributes == nil {
		updateBv.CmekAttributes = &datamodel.CmekAttributes{}
	}
	updateBv.CmekAttributes.EncryptionState = &encryptionState

	_, err = se.UpdateBackupVaultInVCP(ctx, &updateBv, dbBv)
	if err != nil {
		logger.Errorf("Failed to update backup vault encryption state in VCP: %v", err)
		return temporal.NewNonRetryableApplicationError(
			"failed to update backup vault encryption state in VCP",
			"UpdateBackupVaultEncryptionStateInVCPActivityError",
			err,
		)
	}

	return nil
}

// StartSDECmekRotationForBackupVault starts SDE-side CMEK rotation for SDE-managed
// buckets by calling the SDE/CBS rotate endpoint via CVP. This creates an async job
// in SDE/CBS and returns once the job is accepted.
func (j *BackupVaultActivity) StartSDECmekRotationForBackupVault(ctx context.Context, params *common.BackupVaultParams, primaryKeyVersion string) error {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	body := &models.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: &primaryKeyVersion,
	}

	_, err := cvpClient.BackupVault.V1betaRotateCmekBackups(&backup_vault.V1betaRotateCmekBackupsParams{
		LocationID:     params.Region,
		ProjectNumber:  params.OwnerID,
		XCorrelationID: &xCorrelationID,
		BackupVaultID:  params.BackupVaultID,
		Body:           body,
	})
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaRotateCmekBackupsBadRequest:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Bad request starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsBadRequest",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsUnauthorized:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unauthorized to start SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsUnauthorized",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsForbidden:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Forbidden to start SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsForbidden",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsConflict:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Conflict starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsConflict",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsUnprocessableEntity:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unprocessable entity starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsUnprocessableEntity",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsInternalServerError:
			return temporal.NewApplicationError(
				fmt.Sprintf("Internal server error starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsInternalServerError",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsTooManyRequests:
			return temporal.NewApplicationError(
				fmt.Sprintf("Too many requests starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsTooManyRequests",
				err,
			)
		case *backup_vault.V1betaRotateCmekBackupsDefault:
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unexpected error starting SDE CMEK rotation %s: %s", params.BackupVaultID, e.Error()),
				"V1betaRotateCmekBackupsDefault",
				err,
			)
		default:
			logger.Warnf("Unknown error type for SDE CMEK rotation %s: %T - %s", params.BackupVaultID, err, err.Error())
			return err
		}
	}

	return nil
}

// WaitForSDECmekRotationCompletion polls SDE (via CVP) until the backup vault
// encryption state reaches a terminal value (COMPLETED or FAILED). It returns
// true if SDE rotation completed successfully, false otherwise.
func (j *BackupVaultActivity) WaitForSDECmekRotationCompletion(ctx context.Context, params *common.BackupVaultParams) (bool, error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	const (
		maxAttempts = 60
		sleepSec    = 10
	)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		res, err := cvpClient.BackupVault.V1betaListBackupVaults(&backup_vault.V1betaListBackupVaultsParams{
			LocationID:     params.Region,
			ProjectNumber:  params.OwnerID,
			XCorrelationID: &xCorrelationID,
		})
		if err != nil {
			logger.Errorf("Failed to list backup vaults from SDE while waiting for CMEK rotation completion: %v", err)
			return false, temporal.NewApplicationError(
				"failed to list backup vaults from SDE",
				"V1betaListBackupVaultsError",
				err,
			)
		}

		if res.Payload != nil && res.Payload.BackupVaults != nil {
			for _, bv := range res.Payload.BackupVaults {
				if bv == nil || bv.BackupVaultID == "" || bv.BackupVaultID != params.BackupVaultID {
					continue
				}

				// Found our vault; inspect its encryption state.
				if bv.EncryptionState == nil {
					break
				}

				state := *bv.EncryptionState
				switch state {
				case datamodel.EncryptionStateCompleted:
					return true, nil
				case datamodel.EncryptionStateFailed:
					return false, nil
				default:
					// PENDING / IN_PROGRESS – keep polling.
					break
				}
			}
		}

		// No terminal state yet; wait and retry.
		time.Sleep(sleepSec * time.Second)
	}

	logger.Errorf("Timed out waiting for SDE CMEK rotation completion for backup vault %s", params.BackupVaultID)
	return false, nil
}

func (j *BackupVaultActivity) DeleteBackupVaultInSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	return deleteBackupVaultInSDE(ctx, paramz)
}

func _deleteBackupVaultInSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	vault, _, err := cvpClient.BackupVault.V1betaDeleteBackupVault(&backup_vault.V1betaDeleteBackupVaultParams{
		LocationID:     paramz.Region,
		ProjectNumber:  paramz.OwnerID,
		XCorrelationID: &xCorrelationID,
		BackupVaultID:  paramz.BackupVaultID,
	})
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaDeleteBackupVaultBadRequest:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Bad request deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultBadRequest",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultUnauthorized:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unauthorized to delete backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultUnauthorized",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultForbidden:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Forbidden to delete backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultForbidden",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultNotFound:
			logger.Infof("Backup vault %s not found in SDE; treating as already deleted", paramz.BackupVaultID)
			return nil, nil

		case *backup_vault.V1betaDeleteBackupVaultConflict:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Conflict deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultConflict",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultUnprocessableEntity:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unprocessable entity deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultUnprocessableEntity",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultInternalServerError:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Internal server error deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultInternalServerError",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultTooManyRequests:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Too many requests deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultTooManyRequests",
				err,
			)

		case *backup_vault.V1betaDeleteBackupVaultNotImplemented:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Not implemented deleting backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDeleteBackupVaultNotImplemented",
				err,
			)

		default:
			logger.Warnf("Unknown error type for backup vault deletion %s: %T - %s", paramz.BackupVaultID, err, err.Error())
			return nil, err
		}
	}

	responseBytes, err := json.MarshalIndent(vault.Payload.Response, "", "  ")
	if err != nil {
		return nil, errors.New("failed to marshal response from SDE BackupVault Deletion")
	}
	data := models.BackupVaultV1beta{}
	err = utilsConvertJsonToModel(responseBytes, &data)
	if err != nil {
		return nil, err
	}

	model, err := ConvertToBackupVaultDataModel(&data, paramz.Region)
	if err != nil {
		return nil, err
	}
	return model, nil
}

func (j *BackupVaultActivity) GetBackupVaultFromSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	return getBackupVaultFromSDE(ctx, paramz)
}

func _getBackupVaultFromSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	payload, err := sde.DescribeSDEBackupVault(ctx, paramz.Region, paramz.OwnerID, paramz.BackupVaultID, logger)
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaDescribeBackupVaultNotFound:
			logger.Infof("Backup vault %s not found in SDE", paramz.BackupVaultID)
			return nil, nil

		case *backup_vault.V1betaDescribeBackupVaultBadRequest:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Bad request describing backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultBadRequest",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultUnauthorized:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unauthorized to describe backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultUnauthorized",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultForbidden:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Forbidden to describe backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultForbidden",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultUnprocessableEntity:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unprocessable entity describing backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultUnprocessableEntity",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultInternalServerError:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Internal server error describing backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultInternalServerError",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultTooManyRequests:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Too many requests describing backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultTooManyRequests",
				err,
			)

		case *backup_vault.V1betaDescribeBackupVaultNotImplemented:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Not implemented describing backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaDescribeBackupVaultNotImplemented",
				err,
			)

		default:
			logger.Warnf("Unknown error type for backup vault describe %s: %T - %s", paramz.BackupVaultID, err, err.Error())
			return nil, err
		}
	}
	if payload == nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Backup vault %s describe returned empty payload from SDE", paramz.BackupVaultID),
			"DescribeBackupVaultEmptyPayload",
			nil,
		)
	}
	return ConvertToBackupVaultDataModel(payload, paramz.Region)
}

// UpdateBackupVaultInSDE ensures idempotency by checking existing BackupVault before creation.
func (j *BackupVaultActivity) UpdateBackupVaultInSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	return updateBackupVaultInSDE(ctx, paramz)
}

func convertToSDEBackupRetentionPolicy(attrs common.BackupRetentionPolicyParams) *models.BackupRetentionPolicyUpdateV1beta {
	var backupMinimumEnforcedRetentionDuration *int64
	if attrs.BackupMinimumEnforcedRetentionDuration != nil {
		backupMinimumEnforcedRetentionDuration = attrs.BackupMinimumEnforcedRetentionDuration
	}

	var dailyBackupImmutable *bool
	if attrs.IsDailyBackupImmutable != nil {
		dailyBackupImmutable = attrs.IsDailyBackupImmutable
	}
	var weeklyBackupImmutable *bool
	if attrs.IsWeeklyBackupImmutable != nil {
		weeklyBackupImmutable = attrs.IsWeeklyBackupImmutable
	}
	var monthlyBackupImmutable *bool
	if attrs.IsMonthlyBackupImmutable != nil {
		monthlyBackupImmutable = attrs.IsMonthlyBackupImmutable
	}
	var adhocBackupImmutable *bool
	if attrs.IsAdhocBackupImmutable != nil {
		adhocBackupImmutable = attrs.IsAdhocBackupImmutable
	}

	return &models.BackupRetentionPolicyUpdateV1beta{
		BackupMinimumEnforcedRetentionDays: backupMinimumEnforcedRetentionDuration,
		DailyBackupImmutable:               dailyBackupImmutable,
		ManualBackupImmutable:              adhocBackupImmutable,
		MonthlyBackupImmutable:             monthlyBackupImmutable,
		WeeklyBackupImmutable:              weeklyBackupImmutable,
	}
}

func _updateBackupVaultInSDE(ctx context.Context, paramz *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)
	GetSignedJwtToken := utils.GetAuthTokenFromContext(ctx)
	cvpClient := cvpCreateClient(logger, GetSignedJwtToken)
	xCorrelationID := utils.GetCoRelationIDFromContext(ctx)

	body := &models.BackupVaultUpdateV1beta{
		Description: paramz.Description,
	}
	// Update the assignment
	brp := convertToSDEBackupRetentionPolicy(paramz.BackupRetentionPolicy)
	if brp != nil {
		body.BackupRetentionPolicy = brp
	}

	vault, err := cvpClient.BackupVault.V1betaUpdateBackupVault(&backup_vault.V1betaUpdateBackupVaultParams{
		LocationID:     paramz.Region,
		ProjectNumber:  paramz.OwnerID,
		XCorrelationID: &xCorrelationID,
		BackupVaultID:  paramz.BackupVaultID,
		Body:           body,
	})
	if err != nil {
		switch e := err.(type) {
		case *backup_vault.V1betaUpdateBackupVaultBadRequest:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Bad request updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultBadRequest",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultUnauthorized:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unauthorized to update backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultUnauthorized",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultForbidden:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Forbidden to update backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultForbidden",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultConflict:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Conflict updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultConflict",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultUnprocessableEntity:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Unprocessable entity updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultUnprocessableEntity",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultInternalServerError:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Internal server error updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultInternalServerError",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultTooManyRequests:
			return nil, temporal.NewApplicationError(
				fmt.Sprintf("Too many requests updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultTooManyRequests",
				err,
			)

		case *backup_vault.V1betaUpdateBackupVaultNotImplemented:
			return nil, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("Not implemented updating backup vault %s: %s", paramz.BackupVaultID, e.Error()),
				"V1betaUpdateBackupVaultNotImplemented",
				err,
			)

		default:
			logger.Warnf("Unknown error type for backup vault Updation %s: %T - %s", paramz.BackupVaultID, err, err.Error())
			return nil, err
		}
	}

	responseBytes, err := json.MarshalIndent(vault.Payload.Response, "", "  ")
	if err != nil {
		return nil, errors.New("failed to marshal response from SDE BackupVault Updation")
	}
	data := models.BackupVaultV1beta{}
	err = utilsConvertJsonToModel(responseBytes, &data)
	if err != nil {
		return nil, err
	}

	model, err := ConvertToBackupVaultDataModel(&data, paramz.Region)
	if err != nil {
		return nil, err
	}
	return model, nil
}

// ApplyBackupVaultUpdateParams merges update params into the current backup vault and returns
// the result. Used in a VCP-only region (CVP_HOST not set) and cross-project vaults so VCP is updated with the requested fields (description,
// retention policy, etc.) without calling SDE.
func (j *BackupVaultActivity) ApplyBackupVaultUpdateParams(ctx context.Context, backupVault *datamodel.BackupVault, params *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	updated := &datamodel.BackupVault{
		BaseModel:    backupVault.BaseModel,
		AccountID:    backupVault.AccountID,
		ExternalUUID: backupVault.ExternalUUID,
	}

	if params.Description != nil {
		updated.Description = params.Description
	} else {
		updated.Description = backupVault.Description
	}

	brp := params.BackupRetentionPolicy
	if brp.BackupMinimumEnforcedRetentionDuration != nil ||
		brp.IsDailyBackupImmutable != nil ||
		brp.IsWeeklyBackupImmutable != nil ||
		brp.IsMonthlyBackupImmutable != nil ||
		brp.IsAdhocBackupImmutable != nil {
		if backupVault.ImmutableAttributes != nil {
			updated.ImmutableAttributes = &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: backupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration,
				IsDailyBackupImmutable:                 backupVault.ImmutableAttributes.IsDailyBackupImmutable,
				IsWeeklyBackupImmutable:                backupVault.ImmutableAttributes.IsWeeklyBackupImmutable,
				IsMonthlyBackupImmutable:               backupVault.ImmutableAttributes.IsMonthlyBackupImmutable,
				IsAdhocBackupImmutable:                 backupVault.ImmutableAttributes.IsAdhocBackupImmutable,
			}
		} else {
			updated.ImmutableAttributes = &datamodel.ImmutableAttributes{}
		}
		if brp.BackupMinimumEnforcedRetentionDuration != nil {
			updated.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration = brp.BackupMinimumEnforcedRetentionDuration
		}
		if brp.IsDailyBackupImmutable != nil {
			updated.ImmutableAttributes.IsDailyBackupImmutable = *brp.IsDailyBackupImmutable
		}
		if brp.IsWeeklyBackupImmutable != nil {
			updated.ImmutableAttributes.IsWeeklyBackupImmutable = *brp.IsWeeklyBackupImmutable
		}
		if brp.IsMonthlyBackupImmutable != nil {
			updated.ImmutableAttributes.IsMonthlyBackupImmutable = *brp.IsMonthlyBackupImmutable
		}
		if brp.IsAdhocBackupImmutable != nil {
			updated.ImmutableAttributes.IsAdhocBackupImmutable = *brp.IsAdhocBackupImmutable
		}
	} else {
		updated.ImmutableAttributes = backupVault.ImmutableAttributes
	}

	if backupVault.CmekAttributes != nil {
		updated.CmekAttributes = &datamodel.CmekAttributes{
			KmsConfigResourcePath:    backupVault.CmekAttributes.KmsConfigResourcePath,
			EncryptionState:          backupVault.CmekAttributes.EncryptionState,
			BackupsPrimaryKeyVersion: backupVault.CmekAttributes.BackupsPrimaryKeyVersion,
		}
	}
	if params.CmekEncryptionState != nil || params.CmekBackupsPrimaryKeyVersion != nil {
		if updated.CmekAttributes == nil {
			updated.CmekAttributes = &datamodel.CmekAttributes{}
		}
		if params.CmekEncryptionState != nil {
			updated.CmekAttributes.EncryptionState = params.CmekEncryptionState
		}
		if params.CmekBackupsPrimaryKeyVersion != nil {
			updated.CmekAttributes.BackupsPrimaryKeyVersion = params.CmekBackupsPrimaryKeyVersion
		}
	}

	updated.LifeCycleState = datamodel.LifeCycleStateREADY
	updated.LifeCycleStateDetails = datamodel.LifeCycleStateAvailableDetails
	return updated, nil
}

// CreateBackupVaultInVCP creates a backup vault entry in the VCP database
func (j *BackupVaultActivity) CreateBackupVaultInVCP(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	created, err := j.SE.CreateBackupVaultEntryInVCP(ctx, bv)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (j *BackupVaultActivity) CreateRemoteBackupVault(ctx context.Context, projectNumber string, backupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	return CreateRemoteBackupVault(ctx, projectNumber, backupVault, nil)
}

func (j *BackupVaultActivity) UpdateBackupVaultInVCP(ctx context.Context, bvParams *datamodel.BackupVault, vcpBvParams *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	se := j.SE
	BackupVault, err := se.UpdateBackupVaultInVCP(ctx, bvParams, vcpBvParams)
	if err != nil {
		return nil, err
	}
	return BackupVault, nil
}

// UpdateBackupVaultFields updates only the given columns on a backup vault, identified by UUID.
func (j *BackupVaultActivity) UpdateBackupVaultFields(ctx context.Context, backupVaultUUID string, updates map[string]interface{}) error {
	return j.SE.UpdateBackupVaultFields(ctx, backupVaultUUID, updates)
}

func (j *BackupVaultActivity) DeleteBackupVaultInVCP(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error) {
	se := j.SE
	logger := util.GetLogger(ctx)

	backupVault, err := se.DeleteBackupVaultInVCP(ctx, backupVaultId)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Infof("Backup vault %s not found in VCP; either it is an SDE backup vault or it is already deleted", backupVaultId)
			return nil, nil
		}
		return nil, err
	}
	return backupVault, nil
}

func (j *BackupVaultActivity) DeleteRemoteBackupVaultInVCP(ctx context.Context, params *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	return DeleteRemoteBackupVaultInVCP(ctx, params)
}

func DeleteRemoteBackupVaultInVCP(ctx context.Context, params *common.BackupVaultParams) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	if params.BackupRegion == nil || *params.BackupRegion == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			"BackupRegion not provided in params",
			"BackupRegionMissing",
			fmt.Errorf("backup region is required for cross-region backupVault deletion"),
		)
	}

	basePath, jwtToken, err := utilsGetRemoteRegionConfig(*params.BackupRegion, params.OwnerID)
	if err != nil {
		logger.Errorf("Failed to get remote region configuration for region %s: %v", *params.BackupRegion, err)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to get remote region configuration: %v", err),
			"InvalidRemoteRegionConfig",
			err,
		)
	}

	googleProxyClient := googleProxyClientGet(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	deleteParams := &googleproxyclient.V1betaInternalDeleteBackupVaultParams{
		ProjectNumber:  params.OwnerID,
		LocationId:     *params.BackupRegion,
		BackupVaultId:  params.BackupVaultID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalDeleteBackupVault(ctx, *deleteParams)
	if err != nil {
		logger.Errorf("Failed to call V1betaInternalDeleteBackupVault: %v", err)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to delete remote backup vault: %v", err),
			"InternalDeleteBackupVaultFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		isDone := r.Done.Value
		logger.Infof("Delete operation returned for remote backup vault %s in region %s. Operation: %s, Done: %v",
			params.BackupVaultID, *params.BackupRegion, r.GetName(), isDone)

		if !isDone {
			logger.Warnf("Delete operation for remote backup vault %s not marked as done, but treating as synchronous", params.BackupVaultID)
		}

		logger.Infof("Successfully deleted remote backup vault %s (external UUID) in region %s",
			params.BackupVaultID, *params.BackupRegion)
		return nil, nil

	case *googleproxyclient.V1betaInternalDeleteBackupVaultNoContent:
		logger.Infof("Successfully deleted remote backup vault %s (external UUID) in region %s - NoContent response",
			params.BackupVaultID, *params.BackupRegion)
		return nil, nil

	case *googleproxyclient.V1betaInternalDeleteBackupVaultBadRequest:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request deleting remote backup vault: %s", r.Message),
			"V1betaInternalDeleteBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupVaultUnauthorized:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to delete remote backup vault: %s", r.Message),
			"V1betaInternalDeleteBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupVaultForbidden:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to delete remote backup vault: %s", r.Message),
			"V1betaInternalDeleteBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupVaultNotFound:
		logger.Warnf("Remote backup vault (corresponding to %s) not found when attempting to delete: %s", params.BackupVaultID, r.Message)
		return nil, nil

	case *googleproxyclient.V1betaInternalDeleteBackupVaultConflict:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict deleting remote backup vault: %s", r.Message),
			"V1betaInternalDeleteBackupVaultConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupVaultInternalServerError:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Internal server error deleting remote backup vault: %s", r.Message),
			"V1betaInternalDeleteBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	default:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unexpected response type from internal delete backup vault endpoint: %T", r),
			"UnexpectedDeleteResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

func (j *BackupVaultActivity) UpdateRemoteBackupVaultInVCP(ctx context.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	return UpdateRemoteBackupVaultInVCP(ctx, params, backupVault)
}

func UpdateRemoteBackupVaultInVCP(ctx context.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	logger := util.GetLogger(ctx)

	if params.BackupRegion == nil || *params.BackupRegion == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			"BackupRegion not provided in params",
			"BackupRegionMissing",
			fmt.Errorf("backup region is required for cross-region backupVault update"),
		)
	}

	basePath, jwtToken, err := utilsGetRemoteRegionConfig(*params.BackupRegion, params.OwnerID)
	if err != nil {
		logger.Errorf("Failed to get remote region configuration for region %s: %v", *params.BackupRegion, err)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to get remote region configuration: %v", err),
			"InvalidRemoteRegionConfig",
			err,
		)
	}

	googleProxyClient := googleProxyClientGet(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	updateBody := googleproxyclient.BackupVaultInternalUpdateV1beta{}

	if params.Description != nil {
		updateBody.Description = googleproxyclient.NewOptString(*params.Description)
	}

	if len(params.BucketDetails) > 0 {
		bucketDetailsItems := make([]googleproxyclient.BackupVaultInternalUpdateV1betaBucketDetailsItem, 0, len(params.BucketDetails))
		for _, bd := range params.BucketDetails {
			bucketDetailsItems = append(bucketDetailsItems, googleproxyclient.BackupVaultInternalUpdateV1betaBucketDetailsItem{
				BucketName:          googleproxyclient.NewOptString(bd.BucketName),
				ServiceAccountName:  googleproxyclient.NewOptString(bd.ServiceAccountName),
				VendorSubnetId:      googleproxyclient.NewOptString(bd.VendorSubnetID),
				TenantProjectNumber: googleproxyclient.NewOptString(bd.TenantProjectNumber),
			})
		}
		updateBody.BucketDetails = bucketDetailsItems
	}

	if params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil ||
		params.BackupRetentionPolicy.IsDailyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil ||
		params.BackupRetentionPolicy.IsAdhocBackupImmutable != nil {
		brp := googleproxyclient.BackupRetentionPolicyUpdateV1beta{}

		if params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil {
			brp.BackupMinimumEnforcedRetentionDays = googleproxyclient.NewOptInt(int(*params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration))
		}
		if params.BackupRetentionPolicy.IsDailyBackupImmutable != nil {
			brp.DailyBackupImmutable = googleproxyclient.NewOptBool(*params.BackupRetentionPolicy.IsDailyBackupImmutable)
		}
		if params.BackupRetentionPolicy.IsWeeklyBackupImmutable != nil {
			brp.WeeklyBackupImmutable = googleproxyclient.NewOptBool(*params.BackupRetentionPolicy.IsWeeklyBackupImmutable)
		}
		if params.BackupRetentionPolicy.IsMonthlyBackupImmutable != nil {
			brp.MonthlyBackupImmutable = googleproxyclient.NewOptBool(*params.BackupRetentionPolicy.IsMonthlyBackupImmutable)
		}
		if params.BackupRetentionPolicy.IsAdhocBackupImmutable != nil {
			brp.ManualBackupImmutable = googleproxyclient.NewOptBool(*params.BackupRetentionPolicy.IsAdhocBackupImmutable)
		}

		updateBody.BackupRetentionPolicy = googleproxyclient.NewOptBackupRetentionPolicyUpdateV1beta(brp)
	}

	// Hydrate CMEK fields into the remote (source-region) VCP backup vault for CRB
	// scenarios so that both regions reflect consistent encryption state and key
	// version.
	if backupVault != nil && backupVault.CmekAttributes != nil {
		if backupVault.CmekAttributes.EncryptionState != nil {
			updateBody.EncryptionState = googleproxyclient.NewOptBackupVaultInternalUpdateV1betaEncryptionState(
				googleproxyclient.BackupVaultInternalUpdateV1betaEncryptionState(*backupVault.CmekAttributes.EncryptionState),
			)
		}
		if backupVault.CmekAttributes.BackupsPrimaryKeyVersion != nil {
			updateBody.BackupsPrimaryKeyVersion = googleproxyclient.NewOptString(*backupVault.CmekAttributes.BackupsPrimaryKeyVersion)
		}
	}

	updateParams := googleproxyclient.V1betaInternalUpdateBackupVaultParams{
		ProjectNumber:  params.OwnerID,
		LocationId:     *params.BackupRegion,
		BackupVaultId:  params.BackupVaultID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	res, err := googleProxyClient.Invoker.V1betaInternalUpdateBackupVault(ctx, &updateBody, updateParams)
	if err != nil {
		logger.Errorf("Failed to call V1betaInternalUpdateBackupVault: %v", err)
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to update remote backup vault: %v", err),
			"InternalUpdateBackupVaultFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.OperationV1beta:
		isDone := r.Done.Value

		if !isDone {
			logger.Warnf("Update operation for remote backup vault %s not marked as done, but treating as synchronous", params.BackupVaultID)
		}

		logger.Infof("Successfully updated remote backup vault %s (external UUID: %s) in region %s",
			params.BackupVaultID, params.BackupVaultID, *params.BackupRegion)
		return &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				UUID: params.BackupVaultID,
			},
		}, nil

	case *googleproxyclient.V1betaInternalUpdateBackupVaultBadRequest:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultUnauthorized:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to update remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultForbidden:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to update remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultNotFound:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Remote backup vault not found: %s", r.Message),
			"V1betaInternalUpdateBackupVaultNotFound",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultConflict:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultUnprocessableEntity:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalUpdateBackupVaultInternalServerError:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Internal server error updating remote backup vault: %s", r.Message),
			"V1betaInternalUpdateBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	default:
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unexpected response type from internal update backup vault endpoint: %T", r),
			"UnexpectedUpdateResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

func _convertToBackupVaultDataModel(bv *models.BackupVaultV1beta, locationId string) (*datamodel.BackupVault, error) {
	var resourceID, backupVaultType string
	var sourceRegion, backupRegion, description *string
	if bv.ResourceID != nil {
		resourceID = *bv.ResourceID
	}
	if bv.BackupRegion != nil {
		backupRegion = bv.BackupRegion
	}
	if bv.BackupVaultType != nil {
		backupVaultType = *bv.BackupVaultType
	}
	if bv.Description != nil && *bv.Description != "" {
		description = bv.Description
	}

	// SourceRegion and BackupRegion are 'nil' in SDE for IN_REGION backup vaults. They're set only for CROSS_REGION backup vaults.
	if bv.SourceRegion != nil {
		// CROSS_REGION Backup Vault
		sourceRegion = bv.SourceRegion
	} else {
		// IN_REGION Backup Vault
		sourceRegion = nillable.ToPointer(locationId)
	}

	var minEnforcedRetentionDuration *int64
	var isDaily, isWeekly, isMonthly, isAdhoc bool
	if bv.BackupRetentionPolicy != nil {
		minEnforcedRetentionDuration = bv.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDays
		isDaily = bv.BackupRetentionPolicy.DailyBackupImmutable
		isWeekly = bv.BackupRetentionPolicy.WeeklyBackupImmutable
		isMonthly = bv.BackupRetentionPolicy.MonthlyBackupImmutable
		isAdhoc = bv.BackupRetentionPolicy.ManualBackupImmutable
	}

	immutableFields := &datamodel.ImmutableAttributes{
		BackupMinimumEnforcedRetentionDuration: minEnforcedRetentionDuration,
		IsDailyBackupImmutable:                 isDaily,
		IsWeeklyBackupImmutable:                isWeekly,
		IsMonthlyBackupImmutable:               isMonthly,
		IsAdhocBackupImmutable:                 isAdhoc,
	}

	var cmekFields *datamodel.CmekAttributes
	if bv.KmsConfigResourcePath != nil {
		cmekFields = &datamodel.CmekAttributes{
			KmsConfigResourcePath:    bv.KmsConfigResourcePath,
			EncryptionState:          bv.EncryptionState,
			BackupsPrimaryKeyVersion: bv.BackupsPrimaryKeyVersion,
		}
	}

	return &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			UUID:      bv.BackupVaultID,
			CreatedAt: time.Time(bv.CreatedAt),
			UpdatedAt: time.Time(bv.CreatedAt),
			DeletedAt: nil,
		},
		Name:                       resourceID,
		BackupRegionName:           backupRegion,
		SourceRegionName:           sourceRegion,
		LifeCycleState:             bv.State,
		LifeCycleStateDetails:      bv.StateDetails,
		BackupVaultType:            backupVaultType,
		Description:                description,
		ImmutableAttributes:        immutableFields,
		CmekAttributes:             cmekFields,
		CrossRegionBackupVaultName: bv.DestinationBackupVault,
		ServiceType:                datamodel.ServiceTypeGCNV,
	}, nil
}

func (j *BackupVaultActivity) UpdateBackupVaultStateInCaseOfError(ctx context.Context, backupVault *datamodel.BackupVault, state, stateDetails string) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	// Always reload the latest backup vault from the database so that we do not
	// accidentally overwrite CMEK attributes (e.g. BackupsPrimaryKeyVersion)
	// with a partially-populated in-memory struct from the workflow.
	dbBv, err := se.GetBackupVaultByUUIDndOwnerID(ctx, backupVault.UUID, backupVault.AccountID)
	if err != nil {
		logger.Errorf("Failed to load backup vault for state update in case of error: %v", err)
		return err
	}

	logger.Infof("UpdateBackupVaultStateInCaseOfError: updating backup vault %s to state=%q stateDetails=%q (currentState=%q currentDetails=%q)",
		dbBv.UUID, state, stateDetails, dbBv.LifeCycleState, dbBv.LifeCycleStateDetails)

	_, err = se.UpdateBackupVaultState(ctx, dbBv, state, stateDetails)
	if err != nil {
		logger.Errorf("Failed to update backup vault state in case of error: %v", err)
		return err
	}

	return nil
}

func (j *BackupVaultActivity) UpdateDeletedBackupVaultStateInCaseOfError(ctx context.Context, backupVault *datamodel.BackupVault, state, stateDetails string) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	logger.Infof("UpdateDeletedBackupVaultStateInCaseOfError: restoring backup vault %s to state=%q stateDetails=%q",
		backupVault.UUID, state, stateDetails)

	_, err := se.RestoreDeletedBackupVault(ctx, backupVault.UUID, backupVault.AccountID, state, stateDetails)
	if err != nil {
		logger.Errorf("Failed to restore deleted backup vault state in case of error: %v", err)
		return err
	}

	return nil
}

// DeleteBackupVaultBuckets deletes all buckets associated with a backup vault
func (j *BackupVaultActivity) DeleteBackupVaultBuckets(ctx context.Context, backupVault *datamodel.BackupVault) error {
	if backupVault == nil {
		return errors.New("backupVault parameter is nil")
	}

	logger := util.GetLogger(ctx)

	// Get cloud service for bucket deletion
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return errors.WrapAsTemporalApplicationError(err)
	}

	// Delete each bucket associated with the backup vault
	for _, bucketDetail := range backupVault.BucketDetails {
		if bucketDetail.BucketName != "" {
			logger.Infof("Deleting bucket %s for backup vault %s", bucketDetail.BucketName, backupVault.Name)
			_, err := DeleteGCPBucket(ctx, bucketDetail.BucketName, cloudService)
			if err != nil {
				logger.Errorf("Failed to delete bucket %s: %v", bucketDetail.BucketName, err)
				return errors.WrapAsTemporalApplicationError(err)
			}
			logger.Infof("Successfully deleted bucket %s", bucketDetail.BucketName)
		}
	}
	return nil
}

// CleanupBackupVaultsForAccount fetches all backup vaults for an account and cleans them up
func (a *BackupVaultActivity) CleanupBackupVaultsForAccount(ctx context.Context, projectNumber string) error {
	logger := util.GetLogger(ctx)

	// Get account ID from project number
	account, err := a.SE.GetAccount(ctx, projectNumber)
	if err != nil {
		return errors.WrapAsTemporalApplicationError(err)
	}

	// Fetch all backup vaults for the account
	backupVaults, err := a.SE.ListBackupVaults(ctx, account.ID)
	if err != nil {
		return errors.WrapAsTemporalApplicationError(err)
	}

	if len(backupVaults) > 0 {
		logger.Infof("Cleaning up %d backup vaults for project %s", len(backupVaults), projectNumber)
	}

	// Cleanup each backup vault
	for _, vault := range backupVaults {
		err = a.cleanupBackupVault(ctx, vault)
		if err != nil {
			logger.Errorf("Failed to cleanup backup vault %s: %v", vault.UUID, err)
			return errors.WrapAsTemporalApplicationError(err)
		}
	}

	return nil
}

// cleanupBackupVault handles the cleanup of a single backup vault
func (a *BackupVaultActivity) cleanupBackupVault(ctx context.Context, vault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)

	// For cross-project vaults, detach volumes in other projects before cleanup
	if vault.ServiceType == datamodel.ServiceTypeCrossProject {
		if err := a.detachCrossProjectVolumesFromVault(ctx, vault); err != nil {
			logger.Errorf("Failed to detach cross-project volumes from vault %s: %v", vault.UUID, err)
			return errors.WrapAsTemporalApplicationError(err)
		}
	}

	// 1. Delete GCP buckets associated with this vault
	err := a.deleteGCPBucketsForVault(ctx, vault)
	if err != nil {
		logger.Errorf("Failed to delete GCP buckets for vault %s: %v", vault.UUID, err)
		// Continue with database deletion even if bucket deletion fails
	}

	// 2. Fetch and cleanup all backups for this vault
	err = a.cleanupBackupsForVault(ctx, vault)
	if err != nil {
		logger.Errorf("Failed to cleanup backups for vault %s: %v", vault.UUID, err)
		return errors.WrapAsTemporalApplicationError(err)
	}

	// 3. Soft delete the backup vault in database
	_, err = a.SE.DeleteBackupVaultInVCP(ctx, vault.UUID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to soft delete backup vault %s: %v", vault.UUID, err),
			"DeleteBackupVaultError",
			err,
		)
	}

	return nil
}

// detachCrossProjectVolumesFromVault finds all volumes in other projects that reference
// this backup vault and clears their backup vault, backup policy, and scheduled backup
// references. This prevents orphaned references and failing scheduled backup workflows
// after the vault's owning project is wiped out.
func (a *BackupVaultActivity) detachCrossProjectVolumesFromVault(ctx context.Context, vault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)

	// Detach regular volumes
	volumes, err := a.SE.GetVolumesByBackupVaultID(ctx, vault.UUID)
	if err != nil {
		return fmt.Errorf("failed to get volumes for backup vault %s: %w", vault.UUID, err)
	}

	for _, volume := range volumes {
		if volume.AccountID == vault.AccountID {
			continue
		}
		if volume.DataProtection == nil {
			continue
		}

		logger.Infof("Detaching cross-project vault %s from volume %s (account %d)", vault.UUID, volume.UUID, volume.AccountID)

		scheduledBackupDisabled := false
		updates := map[string]interface{}{
			"data_protection": &datamodel.DataProtection{
				ScheduledBackupEnabled: &scheduledBackupDisabled,
				BackupVaultID:          "",
				BackupPolicyID:         "",
				BackupChainBytes:       volume.DataProtection.BackupChainBytes,
				KmsGrant:               volume.DataProtection.KmsGrant,
			},
		}
		if err := a.SE.UpdateVolumeFields(ctx, volume.UUID, updates); err != nil {
			return fmt.Errorf("failed to detach vault from volume %s: %w", volume.UUID, err)
		}
	}

	// Detach expert mode volumes (same vault/policy/schedule clear as regular volumes; do not alter BackupChainBytes here).
	expertModeVolumes, err := a.SE.GetExpertModeVolumesByBackupVaultID(ctx, vault.UUID)
	if err != nil {
		return fmt.Errorf("failed to get expert mode volumes for backup vault %s: %w", vault.UUID, err)
	}

	for _, emv := range expertModeVolumes {
		if emv.AccountID == vault.AccountID {
			continue
		}
		if emv.BackupConfig == nil {
			continue
		}

		logger.Infof("Detaching cross-project vault %s from expert mode volume %s (account %d)", vault.UUID, emv.UUID, emv.AccountID)

		scheduledBackupDisabled := false
		prev := emv.BackupConfig
		emv.BackupConfig = &datamodel.DataProtection{
			ScheduledBackupEnabled: &scheduledBackupDisabled,
			BackupVaultID:          "",
			BackupPolicyID:         "",
			BackupChainBytes:       prev.BackupChainBytes,
			KmsGrant:               prev.KmsGrant,
		}
		if err := a.SE.UpdateExpertModeVolumeDataProtection(ctx, emv); err != nil {
			return fmt.Errorf("failed to detach vault from expert mode volume %s: %w", emv.UUID, err)
		}
	}

	return nil
}

// deleteGCPBucketsForVault deletes all GCP buckets associated with a backup vault
func (a *BackupVaultActivity) deleteGCPBucketsForVault(ctx context.Context, vault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)

	if len(vault.BucketDetails) == 0 {
		return nil
	}
	// Initialize cloud service
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return errors.WrapAsTemporalApplicationError(err)
	}

	for _, bucketDetail := range vault.BucketDetails {
		if bucketDetail.BucketName != "" {
			err := cloudService.EmptyBucket(ctx, bucketDetail.BucketName)
			if err != nil {
				logger.Errorf("Failed to empty bucket %s: %v", bucketDetail.BucketName, err)
				return errors.NewVCPError(errors.ErrGCPResourceDeprovisionError, err)
			}

			_, err = cloudService.DeleteBucketWithLifecyclePolicy(ctx, bucketDetail.BucketName)
			if err != nil {
				logger.Errorf("Failed to delete bucket %s: %v", bucketDetail.BucketName, err)
				return errors.NewVCPError(errors.ErrGCPResourceDeprovisionError, err)
			}
		}
	}
	return nil
}

// cleanupBackupsForVault fetches and soft deletes all backups for a vault
func (a *BackupVaultActivity) cleanupBackupsForVault(ctx context.Context, vault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)

	// Fetch all backups for this vault
	backups, err := a.SE.GetBackupsByBackupVaultOwnerIDAndFilter(ctx, vault.UUID, vault.AccountID, [][]interface{}{})
	if err != nil {
		return errors.WrapAsTemporalApplicationError(err)
	}

	// Soft delete each backup
	for _, backup := range backups {
		err = a.cleanupBackup(ctx, backup)
		if err != nil {
			logger.Errorf("Failed to cleanup backup %s: %v", backup.UUID, err)
			return errors.WrapAsTemporalApplicationError(err)
		}
	}

	return nil
}

// cleanupBackup handles the soft deletion of a single backup
func (a *BackupVaultActivity) cleanupBackup(ctx context.Context, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)

	// Soft delete the backup in database
	_, err := a.SE.DeleteBackup(ctx, backup.UUID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to soft delete backup %s: %v", backup.UUID, err),
			"DeleteBackupError",
			err,
		)
	}

	logger.Debugf("Successfully soft deleted backup %s", backup.UUID)
	return nil
}

package sde

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// BackupVaultCreateClient is the CVP client factory for backup vault SDE operations.
// Tests may override this variable to inject a mock client.
var BackupVaultCreateClient func(logger log.Logger, jwtToken string) cvpapi.Cvp = cvp.CreateClient

// CreateSDEBackupVault creates a backup vault in SDE via the CVP client.
// The JWT token and correlation ID are fetched from ctx.
func CreateSDEBackupVault(
	ctx context.Context,
	body *models.BackupVaultCreateV1beta,
	locationID, projectNumber string,
	logger log.Logger,
) (*backup_vault.V1betaCreateBackupVaultAccepted, gcpgenserver.V1betaCreateBackupVaultRes, error) {
	jwtToken := utils.GetCVPJWTFromContext(ctx)
	cvpClient := BackupVaultCreateClient(logger, jwtToken)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	vault, err := cvpClient.BackupVault.V1betaCreateBackupVault(&backup_vault.V1betaCreateBackupVaultParams{
		LocationID:     locationID,
		ProjectNumber:  projectNumber,
		XCorrelationID: &correlationID,
		Body:           body,
	})
	if err != nil {
		return nil, convertCvpCreateBackupVaultErrorToRes(err), nil
	}
	return vault, nil, nil
}

// ListSDEBackupVaults returns all backup vaults from SDE for the given project and location.
// The JWT token and correlation ID are fetched from ctx.
func ListSDEBackupVaults(
	ctx context.Context,
	locationID, projectNumber string,
	logger log.Logger,
) ([]*models.BackupVaultV1beta, error) {
	jwtToken := utils.GetCVPJWTFromContext(ctx)
	cvpClient := BackupVaultCreateClient(logger, jwtToken)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	res, err := cvpClient.BackupVault.V1betaListBackupVaults(&backup_vault.V1betaListBackupVaultsParams{
		LocationID:     locationID,
		ProjectNumber:  projectNumber,
		XCorrelationID: &correlationID,
	})
	if err != nil {
		return nil, err
	}
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return res.Payload.BackupVaults, nil
}

// DeleteSDEBackupVault deletes a backup vault in SDE via the CVP client.
// The JWT token is fetched from ctx.
func DeleteSDEBackupVault(
	ctx context.Context,
	locationID, projectNumber, correlationID, backupVaultID string,
	logger log.Logger,
) (gcpgenserver.V1betaDeleteBackupVaultRes, error) {
	jwtToken := utils.GetCVPJWTFromContext(ctx)
	cvpClient := BackupVaultCreateClient(logger, jwtToken)

	deleted, _, err := cvpClient.BackupVault.V1betaDeleteBackupVault(&backup_vault.V1betaDeleteBackupVaultParams{
		LocationID:     locationID,
		ProjectNumber:  projectNumber,
		XCorrelationID: &correlationID,
		BackupVaultID:  backupVaultID,
	})
	if err != nil {
		return convertCvpDeleteBackupVaultErrorToRes(err), nil
	}
	return convertDeleteOperationToOperationV1beta(deleted.Payload), nil
}

// DescribeSDEBackupVault fetches a single backup vault from SDE via the CVP client and
// returns the raw CVP payload.
func DescribeSDEBackupVault(
	ctx context.Context,
	locationID, projectNumber, backupVaultID string,
	logger log.Logger,
) (*models.BackupVaultV1beta, error) {
	jwtToken := utils.GetCVPJWTFromContext(ctx)
	cvpClient := BackupVaultCreateClient(logger, jwtToken)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	res, err := cvpClient.BackupVault.V1betaDescribeBackupVault(&backup_vault.V1betaDescribeBackupVaultParams{
		LocationID:     locationID,
		ProjectNumber:  projectNumber,
		XCorrelationID: &correlationID,
		BackupVaultID:  backupVaultID,
	})
	if err != nil {
		return nil, err
	}
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return res.Payload, nil
}

// GetSDEBackupVaultByExternalUUID returns the backup vault from SDE whose ExternalUUID matches
// externalUUID, or nil when none matches.
func GetSDEBackupVaultByExternalUUID(
	ctx context.Context,
	locationID, projectNumber, externalUUID string,
	logger log.Logger,
) (*models.BackupVaultV1beta, error) {
	vaults, err := ListSDEBackupVaults(ctx, locationID, projectNumber, logger)
	if err != nil {
		return nil, err
	}
	for _, bv := range vaults {
		if bv != nil && bv.ExternalUUID != nil && *bv.ExternalUUID == externalUUID {
			return bv, nil
		}
	}
	return nil, nil
}

func convertCvpCreateBackupVaultErrorToRes(cvpErr error) gcpgenserver.V1betaCreateBackupVaultRes {
	getMsg := func(msg *string) string { return nillable.GetString(msg, "") }
	getCode := func(f *float64) float64 { return nillable.GetFloat64(f, 0) }
	switch e := cvpErr.(type) {
	case *backup_vault.V1betaCreateBackupVaultUnprocessableEntity:
		return &gcpgenserver.V1betaCreateBackupVaultUnprocessableEntity{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultConflict:
		return &gcpgenserver.V1betaCreateBackupVaultConflict{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultBadRequest:
		return &gcpgenserver.V1betaCreateBackupVaultBadRequest{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultUnauthorized:
		return &gcpgenserver.V1betaCreateBackupVaultUnauthorized{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultForbidden:
		return &gcpgenserver.V1betaCreateBackupVaultForbidden{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultTooManyRequests:
		return &gcpgenserver.V1betaCreateBackupVaultTooManyRequests{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaCreateBackupVaultNotImplemented:
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	default:
		return &gcpgenserver.V1betaCreateBackupVaultInternalServerError{Code: 500, Message: cvpErr.Error()}
	}
}

func convertCvpDeleteBackupVaultErrorToRes(cvpErr error) gcpgenserver.V1betaDeleteBackupVaultRes {
	getMsg := func(msg *string) string { return nillable.GetString(msg, "") }
	getCode := func(f *float64) float64 { return nillable.GetFloat64(f, 0) }
	switch e := cvpErr.(type) {
	case *backup_vault.V1betaDeleteBackupVaultUnprocessableEntity:
		return &gcpgenserver.V1betaDeleteBackupVaultUnprocessableEntity{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultConflict:
		return &gcpgenserver.V1betaDeleteBackupVaultConflict{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultBadRequest:
		return &gcpgenserver.V1betaDeleteBackupVaultBadRequest{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultUnauthorized:
		return &gcpgenserver.V1betaDeleteBackupVaultUnauthorized{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultNotFound:
		return &gcpgenserver.V1betaDeleteBackupVaultNotFound{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultForbidden:
		return &gcpgenserver.V1betaDeleteBackupVaultForbidden{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultTooManyRequests:
		return &gcpgenserver.V1betaDeleteBackupVaultTooManyRequests{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	case *backup_vault.V1betaDeleteBackupVaultNotImplemented:
		return &gcpgenserver.V1betaDeleteBackupVaultInternalServerError{Code: getCode(&e.Payload.Code), Message: getMsg(&e.Payload.Message)}
	default:
		return &gcpgenserver.V1betaDeleteBackupVaultInternalServerError{Code: 500, Message: cvpErr.Error()}
	}
}

func convertDeleteOperationToOperationV1beta(op *models.OperationV1beta) *gcpgenserver.OperationV1beta {
	result := &gcpgenserver.OperationV1beta{
		Name: gcpgenserver.NewOptString(op.Name),
	}
	if op.Done != nil {
		result.Done = gcpgenserver.NewOptBool(*op.Done)
	}
	return result
}

package sde

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// setupBackupVaultClient replaces BackupVaultCreateClient with a mock and returns a restore func.
func setupBackupVaultClient(t *testing.T, mockClient backup_vault.ClientService) func() {
	t.Helper()
	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	orig := BackupVaultCreateClient
	BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}
	return func() { BackupVaultCreateClient = orig }
}

func TestDeleteSDEBackupVault(tt *testing.T) {
	const (
		locationID    = "valid-location"
		projectNumber = "1234567890"
		correlationID = "correlation-id"
		backupVaultID = "vault-id"
	)

	tt.Run("WhenSuccessful", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockResp := &models.OperationV1beta{Name: "operation-id", Done: nillable.GetBoolPtr(true)}
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			&backup_vault.V1betaDeleteBackupVaultAccepted{Payload: mockResp}, nil, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	tt.Run("WhenNotFoundError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultNotFound{
				Payload: &models.Error{Code: 400, Message: "Backup vault not found"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultNotFound{}, result)
	})

	tt.Run("WhenForbiddenError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultForbidden{
				Payload: &models.Error{Code: 403, Message: "forbidden"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultForbidden{}, result)
	})

	tt.Run("WhenBadRequestError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultBadRequest{
				Payload: &models.Error{Code: 400, Message: "Invalid request"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultBadRequest{}, result)
	})

	tt.Run("WhenUnauthorizedError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultUnauthorized{
				Payload: &models.Error{Code: 401, Message: "Unauthorized access"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultUnauthorized{}, result)
	})

	tt.Run("WhenTooManyRequestsError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultTooManyRequests{
				Payload: &models.Error{Code: 429, Message: "Too many requests"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultTooManyRequests{}, result)
	})

	tt.Run("WhenConflictError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultConflict{
				Payload: &models.Error{Code: 409, Message: "Conflict error"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultConflict{}, result)
	})

	tt.Run("WhenUnprocessableEntityError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultUnprocessableEntity{
				Payload: &models.Error{Code: 422, Message: "Unprocessable entity"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultUnprocessableEntity{}, result)
	})

	tt.Run("WhenDeleteBackupVaultDefault", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultDefault{
				Payload: &models.Error{Code: 500, Message: "Internal server error"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDeleteBackupVaultInternalServerError{}, result)
	})

	tt.Run("WhenNotImplementedError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultNotImplemented{
				Payload: &models.Error{Code: 501, Message: "Not implemented"},
			}).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DeleteSDEBackupVault(context.Background(), locationID, projectNumber, correlationID, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		internalErr, ok := result.(*gcpgenserver.V1betaDeleteBackupVaultInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(501), internalErr.Code)
		assert.Equal(t, "Not implemented", internalErr.Message)
	})
}

func TestCreateSDEBackupVault(tt *testing.T) {
	const (
		locationID    = "us-west1"
		projectNumber = "test-project-123"
	)

	body := &models.BackupVaultCreateV1beta{
		ResourceID:   "my-vault",
		BackupRegion: nillable.GetStringPtr("us-east1"),
	}

	tt.Run("WhenSuccessful_ReturnsAcceptedAndNilErrRes", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		defer setupBackupVaultClient(t, mockClient)()

		done := true
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(p *backup_vault.V1betaCreateBackupVaultParams) bool {
				return p.LocationID == locationID &&
					p.ProjectNumber == projectNumber &&
					p.Body == body &&
					p.XCorrelationID != nil
			})).
			Return(&backup_vault.V1betaCreateBackupVaultAccepted{
				Payload: &models.OperationV1beta{
					Name: "operations/op-1",
					Done: &done,
					Response: &models.BackupVaultV1beta{
						ResourceID:    nillable.GetStringPtr("my-vault"),
						BackupVaultID: "generated-uuid",
					},
				},
			}, nil)

		accepted, errRes, err := CreateSDEBackupVault(context.Background(), body, locationID, projectNumber, log.NewLogger())

		assert.NoError(t, err)
		assert.Nil(t, errRes)
		assert.NotNil(t, accepted)
		assert.Equal(t, "operations/op-1", accepted.Payload.Name)
		assert.True(t, *accepted.Payload.Done)
	})

	tt.Run("WhenConflictError_ReturnsConflictResAndNilError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		defer setupBackupVaultClient(t, mockClient)()

		code := float64(409)
		msg := "backup vault already exists"
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultConflict{
				Payload: &models.Error{Code: code, Message: msg},
			})

		accepted, errRes, err := CreateSDEBackupVault(context.Background(), body, locationID, projectNumber, log.NewLogger())

		assert.NoError(t, err)
		assert.Nil(t, accepted)
		result, ok := errRes.(*gcpgenserver.V1betaCreateBackupVaultConflict)
		assert.True(t, ok)
		assert.Equal(t, float64(409), result.Code)
		assert.Equal(t, "backup vault already exists", result.Message)
	})

	tt.Run("WhenBadRequestError_ReturnsBadRequestResAndNilError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		defer setupBackupVaultClient(t, mockClient)()

		code := float64(400)
		msg := "invalid request"
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultBadRequest{
				Payload: &models.Error{Code: code, Message: msg},
			})

		accepted, errRes, err := CreateSDEBackupVault(context.Background(), body, locationID, projectNumber, log.NewLogger())

		assert.NoError(t, err)
		assert.Nil(t, accepted)
		result, ok := errRes.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), result.Code)
		assert.Equal(t, "invalid request", result.Message)
	})

	tt.Run("WhenUnknownError_ReturnsInternalServerErrorResAndNilError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		defer setupBackupVaultClient(t, mockClient)()

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, fmt.Errorf("transport error"))

		accepted, errRes, err := CreateSDEBackupVault(context.Background(), body, locationID, projectNumber, log.NewLogger())

		assert.NoError(t, err)
		assert.Nil(t, accepted)
		result, ok := errRes.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), result.Code)
		assert.Contains(t, result.Message, "transport error")
	})
}

func TestGetSDEBackupVaultByExternalUUID(tt *testing.T) {
	const (
		locationID    = "us-west1"
		projectNumber = "1234567890"
		externalUUID  = "external-bv-uuid"
		sdeBvID       = "sde-internal-bv-id"
	)

	tt.Run("WhenMatchFound_ReturnsMatchingVault", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		ext := externalUUID
		other := "some-other-uuid"
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			&backup_vault.V1betaListBackupVaultsOK{Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*models.BackupVaultV1beta{
					{BackupVaultID: "other-id", ExternalUUID: &other},
					{BackupVaultID: sdeBvID, ExternalUUID: &ext},
				},
			}}, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := GetSDEBackupVaultByExternalUUID(context.Background(), locationID, projectNumber, externalUUID, log.NewLogger())
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, sdeBvID, result.BackupVaultID)
	})

	tt.Run("WhenNoMatch_ReturnsNil", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		other := "some-other-uuid"
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			&backup_vault.V1betaListBackupVaultsOK{Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*models.BackupVaultV1beta{
					{BackupVaultID: "other-id", ExternalUUID: &other},
				},
			}}, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := GetSDEBackupVaultByExternalUUID(context.Background(), locationID, projectNumber, externalUUID, log.NewLogger())
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	tt.Run("WhenListFails_ReturnsError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			nil, fmt.Errorf("list failed")).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := GetSDEBackupVaultByExternalUUID(context.Background(), locationID, projectNumber, externalUUID, log.NewLogger())
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestDescribeSDEBackupVault(tt *testing.T) {
	const (
		locationID    = "us-west1"
		projectNumber = "1234567890"
		backupVaultID = "vault-id"
	)

	tt.Run("WhenSuccessful_ReturnsPayload", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		resourceID := "my-vault"
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.MatchedBy(func(p *backup_vault.V1betaDescribeBackupVaultParams) bool {
				return p.LocationID == locationID &&
					p.ProjectNumber == projectNumber &&
					p.BackupVaultID == backupVaultID &&
					p.XCorrelationID != nil
			})).
			Return(&backup_vault.V1betaDescribeBackupVaultOK{
				Payload: &models.BackupVaultV1beta{
					BackupVaultID: backupVaultID,
					ResourceID:    &resourceID,
				},
			}, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DescribeSDEBackupVault(context.Background(), locationID, projectNumber, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, backupVaultID, result.BackupVaultID)
		assert.Equal(t, resourceID, *result.ResourceID)
	})

	tt.Run("WhenEmptyPayload_ReturnsNilNil", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDescribeBackupVault", mock.Anything).Return(
			&backup_vault.V1betaDescribeBackupVaultOK{Payload: nil}, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DescribeSDEBackupVault(context.Background(), locationID, projectNumber, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	tt.Run("WhenNilResponse_ReturnsNilNil", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDescribeBackupVault", mock.Anything).Return(
			nil, nil).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DescribeSDEBackupVault(context.Background(), locationID, projectNumber, backupVaultID, log.NewLogger())
		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	tt.Run("WhenNotFoundError_ReturnsTypedErrorUnwrapped", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		notFound := &backup_vault.V1betaDescribeBackupVaultNotFound{
			Payload: &models.Error{Code: 404, Message: "not found"},
		}
		mockClient.On("V1betaDescribeBackupVault", mock.Anything).Return(
			nil, notFound).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DescribeSDEBackupVault(context.Background(), locationID, projectNumber, backupVaultID, log.NewLogger())
		assert.Nil(t, result)
		assert.IsType(t, &backup_vault.V1betaDescribeBackupVaultNotFound{}, err)
	})

	tt.Run("WhenGenericError_ReturnsError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockClient.On("V1betaDescribeBackupVault", mock.Anything).Return(
			nil, fmt.Errorf("transport error")).Once()
		defer setupBackupVaultClient(t, mockClient)()

		result, err := DescribeSDEBackupVault(context.Background(), locationID, projectNumber, backupVaultID, log.NewLogger())
		assert.Nil(t, result)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "transport error")
	})
}

func TestConvertCvpCreateBackupVaultErrorToRes(tt *testing.T) {
	makePayload := func(code float64, msg string) *models.Error {
		return &models.Error{Code: code, Message: msg}
	}

	tests := []struct {
		name         string
		inputErr     error
		expectedType interface{}
		expectedCode float64
		expectedMsg  string
	}{
		{
			name:         "UnprocessableEntity",
			inputErr:     &backup_vault.V1betaCreateBackupVaultUnprocessableEntity{Payload: makePayload(422, "unprocessable")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultUnprocessableEntity{},
			expectedCode: 422,
			expectedMsg:  "unprocessable",
		},
		{
			name:         "Conflict",
			inputErr:     &backup_vault.V1betaCreateBackupVaultConflict{Payload: makePayload(409, "conflict")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultConflict{},
			expectedCode: 409,
			expectedMsg:  "conflict",
		},
		{
			name:         "BadRequest",
			inputErr:     &backup_vault.V1betaCreateBackupVaultBadRequest{Payload: makePayload(400, "bad request")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultBadRequest{},
			expectedCode: 400,
			expectedMsg:  "bad request",
		},
		{
			name:         "Unauthorized",
			inputErr:     &backup_vault.V1betaCreateBackupVaultUnauthorized{Payload: makePayload(401, "unauthorized")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultUnauthorized{},
			expectedCode: 401,
			expectedMsg:  "unauthorized",
		},
		{
			name:         "Forbidden",
			inputErr:     &backup_vault.V1betaCreateBackupVaultForbidden{Payload: makePayload(403, "forbidden")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultForbidden{},
			expectedCode: 403,
			expectedMsg:  "forbidden",
		},
		{
			name:         "TooManyRequests",
			inputErr:     &backup_vault.V1betaCreateBackupVaultTooManyRequests{Payload: makePayload(429, "too many requests")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultTooManyRequests{},
			expectedCode: 429,
			expectedMsg:  "too many requests",
		},
		{
			// NotImplemented maps to InternalServerError (not a distinct VCP type).
			name:         "NotImplemented_MapsToInternalServerError",
			inputErr:     &backup_vault.V1betaCreateBackupVaultNotImplemented{Payload: makePayload(501, "not implemented")},
			expectedType: &gcpgenserver.V1betaCreateBackupVaultInternalServerError{},
			expectedCode: 501,
			expectedMsg:  "not implemented",
		},
		{
			// Default case: unknown error type → 500 InternalServerError.
			name:         "UnknownError_MapsToInternalServerError500",
			inputErr:     fmt.Errorf("unexpected transport failure"),
			expectedType: &gcpgenserver.V1betaCreateBackupVaultInternalServerError{},
			expectedCode: 500,
			expectedMsg:  "unexpected transport failure",
		},
	}

	for _, tc := range tests {
		tt.Run(tc.name, func(t *testing.T) {
			res := convertCvpCreateBackupVaultErrorToRes(tc.inputErr)

			assert.IsType(t, tc.expectedType, res)

			// Extract Code and Message via the concrete types.
			switch r := res.(type) {
			case *gcpgenserver.V1betaCreateBackupVaultUnprocessableEntity:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultConflict:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultBadRequest:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultUnauthorized:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultForbidden:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultTooManyRequests:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			case *gcpgenserver.V1betaCreateBackupVaultInternalServerError:
				assert.Equal(t, tc.expectedCode, r.Code)
				assert.Equal(t, tc.expectedMsg, r.Message)
			}
		})
	}
}

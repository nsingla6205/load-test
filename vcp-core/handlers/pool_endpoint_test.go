package api

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1GetOntapCredentials(t *testing.T) {
	t.Run("WhenSuccessful", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		poolCredentials := &models.UserCredentials{
			Username:      "test-user",
			SecretID:      "test-secret-id",
			CertificateID: "test-cert-id",
			Password:      "test-password",
			AuthType:      1,
			OntapEndpoints: []models.OntapEndpoint{
				{IP: "10.0.0.1", DNS: "host1.example.com"},
				{IP: "10.0.0.2", DNS: "host2.example.com"},
			},
		}

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		// Expert-mode returns creds → dedicated path is never reached.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(poolCredentials, nil)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		response, ok := result.(*oasgenserver.OntapCredentialsV1)
		assert.True(t, ok)
		assert.Equal(t, "test-user", response.Username.Value)
		assert.Equal(t, "test-secret-id", response.SecretID.Value)
		assert.Equal(t, "test-cert-id", response.CertificateID.Value)
		assert.Equal(t, "test-password", response.Password.Value)
		assert.Equal(t, 1, response.AuthType.Value)
		assert.Len(t, response.OntapEndpoints, 2)
		assert.Equal(t, "10.0.0.1", response.OntapEndpoints[0].IP)
		assert.Equal(t, "host1.example.com", response.OntapEndpoints[0].DNS)
		assert.Equal(t, "10.0.0.2", response.OntapEndpoints[1].IP)
		assert.Equal(t, "host2.example.com", response.OntapEndpoints[1].DNS)
	})
	t.Run("WhenDedicatedCapacityPool_ReturnsDedicatedCreds", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		clusterName := "cluster-a"
		dedicatedCreds := &models.UserCredentials{
			Username:     "account-host-user",
			SecretID:     "secret-a",
			AuthType:     1,
			ServiceLevel: "STANDARD",
			OntapEndpoints: []models.OntapEndpoint{{IP: "10.0.0.1", DNS: "10.0.0.1"}},
		}

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:             poolID,
			AccountName:        oasgenserver.NewOptString(accountName),
			UserName:           oasgenserver.NewOptString(userName),
			XNetappClusterName: oasgenserver.NewOptString(clusterName),
		}

		// Expert-mode returns nil for dedicated pools → fall through to dedicated path.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, nil)
		mockOrch.EXPECT().GetDedicatedClusterPoolCreds(mock.Anything, poolID, accountName, clusterName, userName).Return(dedicatedCreds, nil)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.OntapCredentialsV1)
		assert.True(t, ok)
		assert.Equal(t, "account-host-user", response.Username.Value)
		assert.Equal(t, "secret-a", response.SecretID.Value)
		assert.True(t, response.ServiceLevel.IsSet())
		assert.Equal(t, "STANDARD", response.ServiceLevel.Value)
	})
	t.Run("WhenDedicatedClusterUnregistered_ReturnsBadRequest", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		missingErr := vsaerrors.NewVCPError(vsaerrors.ErrMissingRequiredInputError, stderrors.New("cluster not registered"))

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		// Expert-mode returns nil → dedicated path reached → returns error.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, nil)
		mockOrch.EXPECT().GetDedicatedClusterPoolCreds(mock.Anything, poolID, accountName, "", userName).Return(nil, missingErr)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), response.Code)
	})
	t.Run("WhenNoCredentialsFound_Returns404", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      "test-pool-uuid",
			AccountName: oasgenserver.NewOptString("test-account"),
			UserName:    oasgenserver.NewOptString("test-user"),
		}

		// Both paths return nil → 404.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, "test-pool-uuid", "test-account", "test-user").Return(nil, nil)
		mockOrch.EXPECT().GetDedicatedClusterPoolCreds(mock.Anything, "test-pool-uuid", "test-account", "", "test-user").Return(nil, nil)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsNotFound)
		assert.True(t, ok)
		assert.Equal(t, "Pool credentials not found", response.Message)
		assert.Equal(t, float64(404), response.Code)
	})
	t.Run("WhenAccountNameMissing", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      "test-pool-uuid",
			AccountName: oasgenserver.OptString{},
			UserName:    oasgenserver.NewOptString("test-user"),
		}

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, "Account name is required", response.Message)
		assert.Equal(t, float64(400), response.Code)
	})
	t.Run("WhenPoolNotFound", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "non-existent-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		poolNotFoundError := vsaerrors.NewVCPError(vsaerrors.ErrPoolNotFound, stderrors.New("pool not found"))

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		// Expert-mode is called first; pool lookup there returns not-found.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, poolNotFoundError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsNotFound)
		assert.True(t, ok)
		assert.Equal(t, "Pool not found", response.Message)
		assert.Equal(t, float64(404), response.Code)
	})
	t.Run("WhenPoolInCreatingState", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		poolCreatingError := vsaerrors.NewVCPError(vsaerrors.ErrPoolInCreatingState, stderrors.New("pool is in creating state"))

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, poolCreatingError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, "Pool is in creating state", response.Message)
		assert.Equal(t, float64(400), response.Code)
	})
	t.Run("WhenPoolInDeletingState", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		poolDeletingError := vsaerrors.NewVCPError(vsaerrors.ErrPoolInDeletingState, stderrors.New("pool is in deleting state"))

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, poolDeletingError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, "Pool is in deleting state", response.Message)
		assert.Equal(t, float64(400), response.Code)
	})
	t.Run("WhenPoolInCreatingState_WithEmptyMessage", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolCreatingError := &vsaerrors.CustomError{
			TrackingID: vsaerrors.ErrPoolInCreatingState,
			Message:    "",
		}

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      "test-pool-uuid",
			AccountName: oasgenserver.NewOptString("test-account"),
			UserName:    oasgenserver.NewOptString("test-user"),
		}
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, "test-pool-uuid", "test-account", "test-user").Return(nil, poolCreatingError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsBadRequest)
		assert.True(t, ok)
		assert.Equal(t, "Pool is in creating state", response.Message)
		assert.Equal(t, float64(400), response.Code)
	})
	t.Run("WhenOtherVSAError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		otherError := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, stderrors.New("database connection failed"))

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, otherError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsInternalServerError)
		assert.True(t, ok)
		assert.Contains(t, response.Message, "Failed to get pool credentials")
		assert.Equal(t, float64(500), response.Code)
	})
	t.Run("WhenStandardError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		standardError := stderrors.New("standard go error")

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(nil, standardError)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.V1GetOntapCredentialsInternalServerError)
		assert.True(t, ok)
		assert.Contains(t, response.Message, "Failed to get pool credentials")
		assert.Contains(t, response.Message, "standard go error")
		assert.Equal(t, float64(500), response.Code)
	})
	t.Run("WhenEmptyCredentials", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := NewHandler(mockOrch)

		poolID := "test-pool-uuid"
		accountName := "test-account"
		userName := "test-user"
		poolCredentials := &models.UserCredentials{
			Username:       "",
			SecretID:       "",
			CertificateID:  "",
			Password:       "",
			AuthType:       0,
			OntapEndpoints: []models.OntapEndpoint{},
		}

		params := oasgenserver.V1GetOntapCredentialsParams{
			PoolId:      poolID,
			AccountName: oasgenserver.NewOptString(accountName),
			UserName:    oasgenserver.NewOptString(userName),
		}

		// Non-nil return from expert-mode → dedicated path is never reached.
		mockOrch.EXPECT().GetExpertModePoolCreds(mock.Anything, poolID, accountName, userName).Return(poolCredentials, nil)

		result, err := handler.V1GetOntapCredentials(context.Background(), params)

		assert.NoError(t, err)
		response, ok := result.(*oasgenserver.OntapCredentialsV1)
		assert.True(t, ok)
		assert.True(t, response.Username.IsSet())
		assert.Equal(t, "", response.Username.Value)
		assert.True(t, response.SecretID.IsSet())
		assert.Equal(t, "", response.SecretID.Value)
		assert.True(t, response.CertificateID.IsSet())
		assert.Equal(t, "", response.CertificateID.Value)
		assert.True(t, response.Password.IsSet())
		assert.Equal(t, "", response.Password.Value)
		assert.True(t, response.AuthType.IsSet())
		assert.Equal(t, 0, response.AuthType.Value)
	})
}

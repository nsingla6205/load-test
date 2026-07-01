package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/replications"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	models2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestV1betaGetMultipleReplications(t *testing.T) {
	t.Run("WhenReplicationURIsAreEmpty", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{},
		}

		errorMessage := "Replication URIs cannot be empty"
		errorCode := float64(400)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Message)
	})
	t.Run("WhenAccountNameDoesNotMatch", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/stargate/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "replicationURIs projectNumber in body does not match projectNumber in parameter"
		errorCode := float64(400)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Message)
	})
	t.Run("WhenLocationDoesNotMatch", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "replicationURIs locationId in body does not match locationId in parameter"
		errorCode := float64(400)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Message)
	})
	t.Run("WhenVolumeDoesNotMatch", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource/replications/replication-name-6"},
		}

		errorMessage := "replicationURIs volumeId in body does not match volumeResourceId in parameter"
		errorCode := float64(400)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Message)
	})
	t.Run("WhenGetMultipleReplicationsReturnsError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(nil, errors.New("Error retrieving replications from VCP"))

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Code)
		assert.Equal(tt, "Error retrieving replications from VCP", result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Message)
	})
	t.Run("WhenRelicationsFoundInVCP", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}
		replicationId := "replication-id-1"
		resourceId := "resource-id-1"
		mirrorState := "MIRRORED"
		expResp := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId,
				ResourceId:    &resourceId,
				MirrorState:   &mirrorState,
			},
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(expResp, nil)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 1, len(result.(*gcpgenserver.V1betaGetMultipleReplicationsOK).Replications))
	})
	t.Run("WhenGetMultipleReplicationsFailsWithBadRequest", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "BadRequest error"
		errorCode := float64(400)
		mockError := &replications.V1betaGetMultipleReplicationsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsBadRequest).Message)
	})
	t.Run("WhenGetMultipleReplicationsFailsWithUnauthorized", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "Unauthorized error"
		errorCode := float64(401)
		mockError := &replications.V1betaGetMultipleReplicationsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsUnauthorized).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsUnauthorized).Message)
	})
	t.Run("WhenGetMultipleReplicationsFailsWithForbidden", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "Forbidden error"
		errorCode := float64(403)
		mockError := &replications.V1betaGetMultipleReplicationsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsForbidden).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsForbidden).Message)
	})
	t.Run("WhenGetMultipleReplicationsFailsWithNotFound", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "NotFound error"
		errorCode := float64(404)
		mockError := &replications.V1betaGetMultipleReplicationsNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsNotFound).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsNotFound).Message)
	})
	t.Run("WhenGetMultipleReplicationsFailsWithTooManyRequests", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "TooManyRequests error"
		errorCode := float64(429)
		mockError := &replications.V1betaGetMultipleReplicationsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsTooManyRequests).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsTooManyRequests).Message)
	})
	t.Run("WhenGetMultipleReplicationsFailsWithDefault", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		errorMessage := "default error"
		errorCode := float64(500)
		mockError := &replications.V1betaGetMultipleReplicationsDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleReplicationsResponseIsNil", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(nil, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Code)
		assert.Equal(tt, "unknown error during the get multiple replications", result.(*gcpgenserver.V1betaGetMultipleReplicationsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleReplicationsSucceeds", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}
		clusterLocation := "cluster-location"
		description := "description"
		destination := models.ReplicationVolumeInformationV1beta{
			VolumeName: "volume-name",
			VolumeID:   "volume-id",
		}
		timestamp := strfmt.DateTime(time.Unix(1627847261, 0))

		transferState := models.TransferStatsV1beta{
			LagTime:               1234,
			LastTransferDuration:  2134,
			LastTransferEndTime:   &timestamp,
			LastTransferError:     "",
			LastTransferSize:      1234,
			ProgressLastUpdated:   nil,
			TotalProgress:         1234,
			TotalTransferBytes:    1234,
			TotalTransferTimeSecs: 1234,
		}
		mockResponse := &replications.V1betaGetMultipleReplicationsOK{
			Payload: &replications.V1betaGetMultipleReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{
					{
						ClusterLocation: &clusterLocation,
						ReplicationID:   "replication-id-1",
						Description:     &description,
						Destination:     &destination,
						TransferStats:   &transferState,
					},
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultipleReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, "replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})
	t.Run("WhenGetMultipleReplicationsSucceedsWithBlankReplId", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-6"},
		}
		clusterLocation := "cluster-location"
		description := "description"
		destination := models.ReplicationVolumeInformationV1beta{
			VolumeName: "volume-name",
			VolumeID:   "volume-id",
		}
		mockResponse := &replications.V1betaGetMultipleReplicationsOK{
			Payload: &replications.V1betaGetMultipleReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{
					{
						ClusterLocation: &clusterLocation,
						ReplicationID:   "",
						ResourceID:      "replication-id-1",
						Description:     &description,
						Destination:     &destination,
					},
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaGetMultipleReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultipleReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, "replication-id-1", successResult.Replications[0].ResourceId.Value)
	})
	t.Run("WhenVolumeResourceIdInvalidForCVP", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume_resource_id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume_resource_id/replications/replication-name-6"},
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(nil, nil)

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 0, len(result.(*gcpgenserver.V1betaGetMultipleReplicationsOK).Replications))
	})
	t.Run("WhenCVPHostIsNotConfigured_VCPPartial_ReturnsVCPOnly", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{
				"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-1",
				"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-2",
			},
		}

		replicationId := "vcp-replication-id-1"
		resourceId := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId,
				ResourceId:    &resourceId,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		// CVP client must not be reached when CVP_HOST is unset.
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			tt.Fatal("createClient should not be called when CVP_HOST is not configured")
			return cvpapi.Cvp{}
		}

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultipleReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 1, len(successResult.Replications))
		assert.Equal(tt, "vcp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})
	t.Run("WhenCVPHostIsNotConfigured_VCPEmpty_ReturnsEmpty", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetMultipleReplicationsParams{
			LocationId:       "location-id",
			ProjectNumber:    "project-number",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationURIListV1beta{
			ReplicationUris: []string{"projects/project-number/locations/location-id/volumes/volume-resource-id/replications/replication-name-1"},
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		// CVP client must not be reached when CVP_HOST is unset.
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			tt.Fatal("createClient should not be called when CVP_HOST is not configured")
			return cvpapi.Cvp{}
		}

		result, err := handler.V1betaGetMultipleReplications(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaGetMultipleReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 0, len(successResult.Replications))
	})
}

func TestV1betaGetReplicationCount(t *testing.T) {
	t.Run("WhenGetReplicationCountSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.On("GetReplicationCount", mock.Anything, "project-number").Return(int64(5), nil)

		params := gcpgenserver.V1betaGetReplicationCountParams{
			ProjectNumber: "project-number",
			LocationId:    "location-id",
		}

		result, err := handler.V1betaGetReplicationCount(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 5, result.(*gcpgenserver.V1betaGetReplicationCountOK).ReplicationCount)
	})

	t.Run("WhenGetReplicationCountFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.On("GetReplicationCount", mock.Anything, "project-number").Return(int64(0), assert.AnError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaGetReplicationCountParams{
			ProjectNumber: "project-number",
			LocationId:    "location-id",
		}

		result, err := handler.V1betaGetReplicationCount(context.Background(), params)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestV1betaCreateReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			crrEnabled = env.GetBool("CRR_ENABLED", true)
		}()
		crrEnabled = false
		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}
		result, _ := handler.V1betaCreateReplication(context.Background(), req, params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Code)
	})
	t.Run("WhenCreateReplicationSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("CreateVolumeReplication", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenCreateReplicationSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateCreating,
		}

		replicationParams := &common.CreateVolumeReplicationParams{
			AccountName:      params.ProjectNumber,
			Region:           "location-id",
			LocationId:       "location-id",
			Name:             req.ResourceId,
			SourceVolumeName: params.VolumeResourceId,
			CorrelationId:    params.XCorrelationID.Value,
		}

		replicationParams.Body = req
		if req.Description.IsSet() {
			replicationParams.Description, _ = req.Description.Get()
		}

		mockOrchestrator.On("CreateVolumeReplication", context.Background(), replicationParams).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenReplicationIsInErrorState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State:        datamodel.LifeCycleStateError,
			StateDetails: datamodel.LifeCycleStateCreationErrorDetails,
		}

		mockOrchestrator.On("CreateVolumeReplication", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)
		expectedErr := errors2.GetErrorMessageByTrackingID(errors2.ErrValidateCreateDummyReplication)
		mockOrchestrator.On("GetJob", mock.Anything, "job-uuid").Return(&models2.Job{TrackingID: errors2.ErrValidateCreateDummyReplication}, nil)

		result, err := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		operation := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", operation.Name.Value)
		assert.True(tt, operation.Done.Value)
		assert.True(tt, operation.Error.Set)
		assert.Equal(tt, float64(*expectedErr.HttpCode), operation.Error.Value.Code.Value)
		assert.Equal(tt, expectedErr.Message, operation.Error.Value.Message.Value)
	})
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		result, _ := handler.V1betaCreateReplication(context.Background(), req, params)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Message)
	})
	t.Run("WhenCreateReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("CreateVolumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Message)
	})
	t.Run("WhenCreateReplicationFailsWithSomeOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("CreateVolumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))

		result, _ := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaCreateReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaCreateReplicationInternalServerError).Message)
	})
	t.Run("WhenCreateReplicationFailsWithCustomVCPError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaCreateReplicationParams{
			ProjectNumber:    "project-number",
			LocationId:       "location-id",
			VolumeResourceId: "volume-resource-id",
			XCorrelationID:   gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := &gcpgenserver.ReplicationCreateV1beta{
			ResourceId:  "resource-id",
			Description: gcpgenserver.NewOptString("description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("CreateVolumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrParseDestinationLocation, errors2.New("some error")))

		result, _ := handler.V1betaCreateReplication(context.Background(), req, params)

		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Code)
		assert.Equal(tt, "Failed to parse destination location", result.(*gcpgenserver.V1betaCreateReplicationBadRequest).Message)
	})
}

func TestV1betaResumeReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			crrEnabled = env.GetBool("CRR_ENABLED", true)
		}()
		crrEnabled = false
		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		result, _ := handler.V1betaResumeReplication(context.Background(), params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaResumeReplicationBadRequest).Code)
	})
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		resp, _ := handler.V1betaResumeReplication(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", resp.(*gcpgenserver.V1betaResumeReplicationBadRequest).Message)
	})
	t.Run("WhenResumeReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("ResumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaResumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaResumeReplicationBadRequest).Message)
	})
	t.Run("WhenResumeReplicationFailsWithSomeOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("ResumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))

		result, err := handler.V1betaResumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaResumeReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaResumeReplicationInternalServerError).Message)
	})
	t.Run("WhenResumeReplicationSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("ResumeReplication", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaResumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenResumeReplicationSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		mockOrchestrator.On("ResumeReplication", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaResumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenResumeReplicationFailsWithCustomVCPError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("ResumeReplication", mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrorCvpReplicationJobAlreadyInProcess, errors2.New("some error")))

		result, err := handler.V1betaResumeReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Another replication job is already in progress for this resource", result.(*gcpgenserver.V1betaResumeReplicationBadRequest).Message)
	})
}

func TestV1betaStopReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			crrEnabled = env.GetBool("CRR_ENABLED", true)
		}()
		crrEnabled = false
		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationStopV1beta{}
		result, _ := handler.V1betaStopReplication(context.Background(), req, params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaStopReplicationBadRequest).Code)
	})
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "invalid-location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationStopV1beta{}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "Invalid location ID"}
		}

		result, _ := handler.V1betaStopReplication(context.Background(), req, params)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaStopReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaStopReplicationBadRequest).Message)
	})

	t.Run("WhenStopReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationStopV1beta{}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("StopReplication", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaStopReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaStopReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaStopReplicationBadRequest).Message)
	})
	t.Run("WhenStopReplicationFailsWithInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		req := &gcpgenserver.ReplicationStopV1beta{}

		mockOrchestrator.On("StopReplication", mock.Anything, mock.Anything).Return(nil, "", errors.New("internal error"))

		result, err := handler.V1betaStopReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaStopReplicationInternalServerError).Code)
		assert.Equal(tt, "internal error", result.(*gcpgenserver.V1betaStopReplicationInternalServerError).Message)
	})
	t.Run("WhenStopReplicationSucceeds", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationStopV1beta{}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}
		mockOrchestrator.On("StopReplication", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaStopReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenStopReplicationFailsWithCustomVcpError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		params := gcpgenserver.V1betaStopReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		req := &gcpgenserver.ReplicationStopV1beta{}

		mockOrchestrator.On("StopReplication", mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrorCvpReplicationJobAlreadyInProcess, errors2.New("some error")))

		result, err := handler.V1betaStopReplication(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaStopReplicationBadRequest).Code)
		assert.Equal(tt, "Another replication job is already in progress for this resource", result.(*gcpgenserver.V1betaStopReplicationBadRequest).Message)
	})
}

// google-proxy/api/endpoints/replication_endpoints_test.go

func TestV1betaDeleteReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			crrEnabled = env.GetBool("CRR_ENABLED", true)
		}()
		crrEnabled = false
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString("123"),
		}
		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		result, _ := handler.V1betaDeleteReplication(context.Background(), &req, params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Code)
	})
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString("123"),
		}
		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		resp, _ := handler.V1betaDeleteReplication(context.Background(), &req, params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Message)
	})
	t.Run("WhenDeleteReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Message)
	})
	t.Run("WhenDeleteReplicationFailsWithSomeOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(500), resp.(*gcpgenserver.V1betaDeleteReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", resp.(*gcpgenserver.V1betaDeleteReplicationInternalServerError).Message)
	})

	t.Run("WhenDeleteReplicationSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", resp.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenDeleteReplicationSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateDeleting,
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", resp.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenReplicationIsInErrorState", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State:        datamodel.LifeCycleStateError,
			StateDetails: datamodel.LifeCycleStateDeletionErrorDetails,
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)
		expectedErr := errors2.GetErrorMessageByTrackingID(errors2.ErrDeleteVolumeReplication)
		mockOrchestrator.On("GetJob", mock.Anything, "job-uuid").Return(&models2.Job{TrackingID: errors2.ErrDeleteVolumeReplication}, nil)

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		operation := resp.(*gcpgenserver.OperationV1beta)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", operation.Name.Value)
		assert.True(tt, operation.Done.Value)
		assert.True(tt, operation.Error.Set)
		assert.Equal(tt, float64(*expectedErr.HttpCode), operation.Error.Value.Code.Value)
		assert.Equal(tt, expectedErr.Message, operation.Error.Value.Message.Value)
	})
	t.Run("WhenDeleteReplicationSucceedsWithJobWithCleanup", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString("dsfffd"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateDeleting,
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", resp.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenDeleteReplicationFailsWithCustomVcpError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaDeleteReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := gcpgenserver.ReplicationDeleteV1beta{
			CleanupResourcesJobId: gcpgenserver.NewOptString(""),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("DeleteReplication", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrorCvpReplicationJobAlreadyInProcess, errors2.New("some error")))

		resp, err := handler.V1betaDeleteReplication(context.Background(), &req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Code)
		assert.Equal(tt, "Another replication job is already in progress for this resource", resp.(*gcpgenserver.V1betaDeleteReplicationBadRequest).Message)
	})
}

func TestV1betaSyncReplication(t *testing.T) {
	t.Run("WhenCRRIsDisabled", func(tt *testing.T) {
		crrEnabled = false
		defer func() { crrEnabled = true }()

		handler := Handler{}
		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Code)
		assert.Equal(tt, "CRR is not enabled", result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Message)
	})

	t.Run("WhenLocationParsingFails", func(tt *testing.T) {
		handler := Handler{}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "invalid-location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Message)
	})

	t.Run("WhenSyncFailsWithUserInputError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("SyncReplication", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Message)
	})

	t.Run("WhenSyncFailsWithOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("SyncReplication", mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaSyncReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaSyncReplicationInternalServerError).Message)
	})
	t.Run("WhenSyncSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("SyncReplication", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenSyncSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		mockOrchestrator.On("SyncReplication", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenSyncFailsWithCustomVcpError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaSyncReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("SyncReplication", mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrorCvpReplicationJobAlreadyInProcess, errors2.New("some error")))

		result, err := handler.V1betaSyncReplication(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Code)
		assert.Equal(tt, "Another replication job is already in progress for this resource", result.(*gcpgenserver.V1betaSyncReplicationBadRequest).Message)
	})
}

func TestV1betaUpdateReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			crrEnabled = env.GetBool("CRR_ENABLED", true)
		}()
		crrEnabled = false
		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		result, _ := handler.V1betaUpdateReplication(context.Background(), req, params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Code)
	})
	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		resp, _ := handler.V1betaUpdateReplication(context.Background(), req, params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", resp.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Message)
	})
	t.Run("WhenResumeReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Message)
	})
	t.Run("WhenResumeReplicationFailsWithSomeOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaUpdateReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaUpdateReplicationInternalServerError).Message)
	})
	t.Run("WhenResumeReplicationSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenResumeReplicationSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		labels := map[string]string{
			"key1": "value1",
			"key2": "value2",
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
			Labels:      gcpgenserver.NewOptReplicationUpdateV1betaLabels(labels),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenResumeReplicationFailsWithCustomVcpError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description: gcpgenserver.NewOptString("new description"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(nil, "", errors2.NewVCPError(errors2.ErrorEmptyUpdateReplicationPayload, errors2.New("some error")))

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Code)
		assert.Equal(tt, "Update replication payload is empty", result.(*gcpgenserver.V1betaUpdateReplicationBadRequest).Message)
	})
	t.Run("WhenUpdateReplicationSucceedsWithClusterLocation", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaUpdateReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.ReplicationUpdateV1beta{
			Description:     gcpgenserver.NewOptString("new description"),
			ClusterLocation: gcpgenserver.NewOptString("us-west1"),
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		mockOrchestrator.On("UpdateReplication", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaUpdateReplication(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
}

func TestV1betaReverseAndResumeReplication(t *testing.T) {
	t.Run("WhenCRRNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { crrEnabled = true }()
		crrEnabled = false

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		result, _ := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Code)
		assert.Equal(tt, "CRR is not enabled", result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Message)
	})

	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{Code: 400, Message: "Invalid location ID"}
		}

		resp, _ := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NotNil(tt, resp)
		assert.Equal(tt, float64(400), resp.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", resp.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Message)
	})

	t.Run("WhenReverseAndResumeReplicationFailsWithBadRequest", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("ReverseAndResumeReplication", mock.Anything, mock.Anything).Return(nil, nil, errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Message)
	})

	t.Run("WhenReverseAndResumeReplicationFailsWithSomeOtherError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("ReverseAndResumeReplication", mock.Anything, mock.Anything).Return(nil, nil, errors.New("some error"))

		result, err := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaReverseAndResumeReplicationInternalServerError).Code)
		assert.Equal(tt, "some error", result.(*gcpgenserver.V1betaReverseAndResumeReplicationInternalServerError).Message)
	})

	t.Run("WhenReverseAndResumeReplicationSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			convertVcpReplicationModelToVCPVolumeReplicationV1beta = _convertVcpReplicationModelToVCPVolumeReplicationV1beta
		}()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{
				ReplicationId: gcpgenserver.NewOptString("rep-id"),
				ResourceId:    gcpgenserver.NewOptString("resource-id"),
			}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateAvailable,
		}

		jobuuid := "job-uuid"
		mockOrchestrator.On("ReverseAndResumeReplication", mock.Anything, mock.Anything).Return(repResponse, &jobuuid, nil)

		result, err := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.True(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})

	t.Run("WhenReverseAndResumeReplicationSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			convertVcpReplicationModelToVCPVolumeReplicationV1beta = _convertVcpReplicationModelToVCPVolumeReplicationV1beta
		}()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		convertVcpReplicationModelToVCPVolumeReplicationV1beta = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{
				ReplicationId: gcpgenserver.NewOptString("rep-id"),
				ResourceId:    gcpgenserver.NewOptString("resource-id"),
			}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		jobuuid := "job-uuid"
		mockOrchestrator.On("ReverseAndResumeReplication", mock.Anything, mock.Anything).Return(repResponse, &jobuuid, nil)

		result, err := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.False(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})

	t.Run("WhenReverseAndResumeReplicationFailsWithCustomVcpError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() { parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone }()

		params := gcpgenserver.V1betaReverseAndResumeReplicationParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-id",
			ReplicationResourceId: "replication-id",
			XCorrelationID:        gcpgenserver.NewOptString("correlation-id"),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}

		mockOrchestrator.On("ReverseAndResumeReplication", mock.Anything, mock.Anything).Return(nil, nil, errors2.NewVCPError(errors2.ErrorCvpReplicationJobAlreadyInProcess, errors2.New("some error")))

		result, err := handler.V1betaReverseAndResumeReplication(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Code)
		assert.Equal(tt, "Another replication job is already in progress for this resource", result.(*gcpgenserver.V1betaReverseAndResumeReplicationBadRequest).Message)
	})
}

func TestV1betaEstablishPeering(t *testing.T) {
	t.Run("WhenHybridReplicationNotEnabled", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Mock hybridReplicationEnabled to be false
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = false
		defer func() { hybridReplicationEnabled = originalHybridReplicationEnabled }()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(http.StatusBadRequest), result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Code)
		assert.Equal(tt, "Hybrid migration is not enabled", result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Message)
	})

	t.Run("WhenLocationValidationFails", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "invalid-location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Code)
		assert.Equal(tt, "Invalid location ID", result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Message)
	})

	t.Run("WhenEstablishPeeringSucceedsWithNoJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			convertModelToVCPVolumeReplication = _convertModelToVCPVolumeReplication
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.True(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})

	t.Run("WhenEstablishPeeringSucceedsWithJob", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			convertModelToVCPVolumeReplication = _convertModelToVCPVolumeReplication
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		repResponse := &models2.VolumeReplication{
			State: datamodel.LifeCycleStateUpdating,
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(repResponse, "job-uuid", nil)

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
		assert.False(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
	})

	t.Run("WhenEstablishPeeringFailsWithConflict", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("Conflict error"))

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(409), result.(*gcpgenserver.V1betaEstablishPeeringConflict).Code)
		assert.Equal(tt, "Conflict error", result.(*gcpgenserver.V1betaEstablishPeeringConflict).Message)
	})

	t.Run("WhenEstablishPeeringFailsWithUserInputValidationError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("Invalid input"))

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Code)
		assert.Equal(tt, "Invalid input", result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Message)
	})

	t.Run("WhenEstablishPeeringFailsWithNotFoundError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(nil, "", errors.NewNotFoundErr("replication", nil))

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaEstablishPeeringBadRequest).Code)
	})

	t.Run("WhenEstablishPeeringFailsWithInternalServerError", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(nil, "", errors.New("internal error"))

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaEstablishPeeringInternalServerError).Code)
		assert.Equal(tt, "internal error", result.(*gcpgenserver.V1betaEstablishPeeringInternalServerError).Message)
	})

	t.Run("WhenEstablishPeeringSucceedsWithPeerIPAddresses", func(tt *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Ensure hybrid replication is enabled for this test
		originalHybridReplicationEnabled := hybridReplicationEnabled
		hybridReplicationEnabled = true
		defer func() {
			hybridReplicationEnabled = originalHybridReplicationEnabled
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			convertModelToVCPVolumeReplication = _convertModelToVCPVolumeReplication
		}()

		params := gcpgenserver.V1betaEstablishPeeringParams{
			ProjectNumber:         "project-number",
			LocationId:            "location-id",
			VolumeResourceId:      "volume-resource-id",
			ReplicationResourceId: "replication-resource-id",
			XCorrelationID:        gcpgenserver.NewOptString("X-Correlation-ID"),
		}
		peerIPs := []string{"10.0.0.1", "10.0.0.2"}
		req := &gcpgenserver.EstablishPeeringRequestV1beta{
			PeerVolumeName:  "peer-volume-name",
			PeerClusterName: "peer-cluster-name",
			PeerSvmName:     "peer-svm-name",
			PeerIpAddresses: gcpgenserver.NewOptNilStringArray(peerIPs),
		}

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", nil
		}
		convertModelToVCPVolumeReplication = func(volumeReplication *models2.VolumeReplication) *gcpgenserver.ReplicationV1beta {
			return &gcpgenserver.ReplicationV1beta{}
		}

		mockOrchestrator.On("EstablishReplicationPeering", mock.Anything, mock.Anything).Return(&models2.VolumeReplication{}, "job-uuid", nil)

		result, err := handler.V1betaEstablishPeering(context.Background(), req, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/location-id/operations/job-uuid", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
}

func TestV1betaListReplications(t *testing.T) {
	t.Run("WhenVCPGetMultipleReplicationsReturnsError", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(nil, errors.New("Error retrieving replications from VCP"))
		// Mock CVP client to avoid HTTP errors during parallel execution
		// Since VCP error is returned first, CVP error won't be processed, but we still need to mock it
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, errors.New("CVP error"))
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Code)
		assert.Equal(tt, "Error retrieving replications from VCP", result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithBadRequest", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		errorMessage := "BadRequest error"
		errorCode := float64(400)
		mockError := &replications.V1betaListReplicationsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaListReplicationsBadRequest).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaListReplicationsBadRequest).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithUnauthorized", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		errorMessage := "Unauthorized error"
		errorCode := float64(401)
		mockError := &replications.V1betaListReplicationsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaListReplicationsUnauthorized).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaListReplicationsUnauthorized).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithForbidden", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		errorMessage := "Forbidden error"
		errorCode := float64(403)
		mockError := &replications.V1betaListReplicationsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaListReplicationsForbidden).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaListReplicationsForbidden).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithTooManyRequests", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		errorMessage := "TooManyRequests error"
		errorCode := float64(429)
		mockError := &replications.V1betaListReplicationsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaListReplicationsTooManyRequests).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaListReplicationsTooManyRequests).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithDefault", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		errorMessage := "default error"
		errorCode := float64(500)
		mockError := &replications.V1betaListReplicationsDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, errorCode, result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Code)
		assert.Equal(tt, errorMessage, result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Message)
	})

	t.Run("WhenCVPListReplicationsFailsWithUnexpectedError", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		replicationId1 := "vcp-replication-id-1"
		resourceId1 := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId1,
				ResourceId:    &resourceId1,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(nil, errors.New("unexpected error"))
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 1, len(successResult.Replications))
		assert.Equal(tt, "vcp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})

	t.Run("WhenBothVCPAndCVPSucceed", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		replicationId1 := "vcp-replication-id-1"
		resourceId1 := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId1,
				ResourceId:    &resourceId1,
			},
		}

		clusterLocation := "cluster-location"
		description := "description"
		destination := models.ReplicationVolumeInformationV1beta{
			VolumeName: "volume-name",
			VolumeID:   "volume-id",
		}
		mockResponse := &replications.V1betaListReplicationsOK{
			Payload: &replications.V1betaListReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{
					{
						ClusterLocation: &clusterLocation,
						ReplicationID:   "cvp-replication-id-1",
						ResourceID:      "cvp-resource-id-1",
						Description:     &description,
						Destination:     &destination,
					},
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 2, len(successResult.Replications))
		// First should be CVP replication
		assert.Equal(tt, "cvp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
		// Second should be VCP replication
		assert.Equal(tt, "vcp-replication-id-1", successResult.Replications[1].ReplicationId.Value)
	})

	t.Run("WhenOnlyVCPSucceeds", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		replicationId1 := "vcp-replication-id-1"
		resourceId1 := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId1,
				ResourceId:    &resourceId1,
			},
		}

		mockResponse := &replications.V1betaListReplicationsOK{
			Payload: &replications.V1betaListReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 1, len(successResult.Replications))
		assert.Equal(tt, "vcp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})

	t.Run("WhenOnlyCVPSucceeds", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		clusterLocation := "cluster-location"
		description := "description"
		destination := models.ReplicationVolumeInformationV1beta{
			VolumeName: "volume-name",
			VolumeID:   "volume-id",
		}
		mockResponse := &replications.V1betaListReplicationsOK{
			Payload: &replications.V1betaListReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{
					{
						ClusterLocation: &clusterLocation,
						ReplicationID:   "cvp-replication-id-1",
						ResourceID:      "cvp-resource-id-1",
						Description:     &description,
						Destination:     &destination,
					},
				},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 1, len(successResult.Replications))
		assert.Equal(tt, "cvp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})

	t.Run("WhenBothVCPAndCVPReturnEmpty", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		mockResponse := &replications.V1betaListReplicationsOK{
			Payload: &replications.V1betaListReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{},
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return([]commonparams.ReplicationV1beta{}, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 0, len(successResult.Replications))
	})

	t.Run("WhenBothCallsExecuteInParallel", func(tt *testing.T) {
		setTestCVPHost(tt, "http://test-cvp-host")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		replicationId1 := "vcp-replication-id-1"
		resourceId1 := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId1,
				ResourceId:    &resourceId1,
			},
		}

		clusterLocation := "cluster-location"
		description := "description"
		destination := models.ReplicationVolumeInformationV1beta{
			VolumeName: "volume-name",
			VolumeID:   "volume-id",
		}
		mockResponse := &replications.V1betaListReplicationsOK{
			Payload: &replications.V1betaListReplicationsOKBody{
				Replications: []*models.ReplicationV1beta{
					{
						ClusterLocation: &clusterLocation,
						ReplicationID:   "cvp-replication-id-1",
						ResourceID:      "cvp-resource-id-1",
						Description:     &description,
						Destination:     &destination,
					},
				},
			},
		}
		// Both mocks should be called (in parallel)
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		mockClient.EXPECT().V1betaListReplications(mock.Anything).Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		// Should have both CVP and VCP replications
		assert.Equal(tt, 2, len(successResult.Replications))
		// Verify both calls were made by checking the results contain both
		replicationIDs := make(map[string]bool)
		for _, rep := range successResult.Replications {
			if rep.ReplicationId.Value != "" {
				replicationIDs[rep.ReplicationId.Value] = true
			}
		}
		assert.True(tt, replicationIDs["cvp-replication-id-1"], "CVP replication should be present")
		assert.True(tt, replicationIDs["vcp-replication-id-1"], "VCP replication should be present")
	})

	t.Run("WhenCVPHostIsNotConfigured_SkipsCVPAndReturnsVCPOnly", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockClient := replications.NewMockClientService(tt)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		replicationId1 := "vcp-replication-id-1"
		resourceId1 := "vcp-resource-id-1"
		vcpReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: &replicationId1,
				ResourceId:    &resourceId1,
			},
		}
		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(vcpReplications, nil)
		// CVP client must not be invoked when CVP_HOST is unset; deliberately install
		// a client whose call would fail the test if it were reached.
		cvpClient := &cvpapi.Cvp{Replications: mockClient}
		originalClient := createClient
		defer func() {
			createClient = originalClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			tt.Fatal("createClient should not be called when CVP_HOST is not configured")
			return *cvpClient
		}

		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		successResult, ok := result.(*gcpgenserver.V1betaListReplicationsOK)
		assert.True(tt, ok)
		assert.Equal(tt, 1, len(successResult.Replications))
		assert.Equal(tt, "vcp-replication-id-1", successResult.Replications[0].ReplicationId.Value)
	})

	t.Run("WhenCVPHostIsNotConfigured_VCPError_Returns500", func(tt *testing.T) {
		setTestCVPHost(tt, "")
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		params := gcpgenserver.V1betaListReplicationsParams{
			LocationId:     "location-id",
			ProjectNumber:  "project-number",
			XCorrelationID: gcpgenserver.NewOptString("X-Correlation-ID"),
		}

		mockOrchestrator.EXPECT().GetMultipleReplications(mock.Anything, mock.Anything).Return(nil, errors.New("VCP DB down"))

		result, err := handler.V1betaListReplications(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Code)
		assert.Equal(tt, "Error retrieving replications from VCP", result.(*gcpgenserver.V1betaListReplicationsInternalServerError).Message)
	})
}

func TestConvertCommonReplicationV1betaToGcp(t *testing.T) {
	t.Run("WhenAllOptionalFieldsAreSet", func(tt *testing.T) {
		now := time.Now()
		state := "READY"
		stateDetails := "Active state"
		stateDetailsCode := int32(1)
		role := "SOURCE"
		replicationSchedule := "HOURLY"
		mirrorState := "MIRRORED"
		healthy := true
		sourceVolumeName := "source-vol"
		sourceVolumeId := "source-vol-id"
		destVolumeName := "dest-vol"
		destVolumeId := "dest-vol-id"
		description := "Test replication"
		clusterLocation := "us-east1"
		hybridReplicationType := "ONPREM_REPLICATION"

		commonReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: nillable.ToPointer("repl-1"),
				ResourceId:    nillable.ToPointer("resource-1"),
				Description:   &description,
				Source: &commonparams.ReplicationVolumeInformationV1beta{
					VolumeName: &sourceVolumeName,
					VolumeId:   &sourceVolumeId,
				},
				Destination: &commonparams.ReplicationVolumeInformationV1beta{
					VolumeName: &destVolumeName,
					VolumeId:   &destVolumeId,
				},
				State:               &state,
				StateDetails:        &stateDetails,
				StateDetailsCode:    &stateDetailsCode,
				Role:                &role,
				ReplicationSchedule: &replicationSchedule,
				MirrorState:         &mirrorState,
				Healthy:             &healthy,
				TransferStats: &commonparams.TransferStatsV1beta{
					TotalTransferBytes:    nillable.ToPointer(float64(1000.0)),
					TotalTransferTimeSecs: nillable.ToPointer(float64(100.0)),
					LastTransferSize:      nillable.ToPointer(float64(500.0)),
					LastTransferError:     nillable.ToPointer(""),
					LastTransferDuration:  nillable.ToPointer(float64(50.0)),
					LastTransferEndTime:   &now,
					TotalProgress:         nillable.ToPointer(float64(75.0)),
					ProgressLastUpdated:   &now,
					LagTime:               nillable.ToPointer(float64(10.0)),
				},
				Created:               &now,
				Labels:                map[string]string{"key": "value"},
				ClusterLocation:       &clusterLocation,
				HybridReplicationType: &hybridReplicationType,
			},
		}

		result := convertCommonReplicationV1betaToGcp(commonReplications)

		assert.Len(tt, result, 1)
		repl := result[0]
		assert.True(tt, repl.ReplicationId.IsSet())
		assert.Equal(tt, "repl-1", repl.ReplicationId.Value)
		assert.True(tt, repl.ResourceId.IsSet())
		assert.Equal(tt, "resource-1", repl.ResourceId.Value)
		assert.True(tt, repl.Description.IsSet())
		assert.Equal(tt, "Test replication", repl.Description.Value)
		assert.True(tt, repl.Source.IsSet())
		assert.True(tt, repl.Source.Value.VolumeName.IsSet())
		assert.Equal(tt, "source-vol", repl.Source.Value.VolumeName.Value)
		assert.True(tt, repl.Source.Value.VolumeId.IsSet())
		assert.Equal(tt, "source-vol-id", repl.Source.Value.VolumeId.Value)
		assert.True(tt, repl.Destination.IsSet())
		assert.True(tt, repl.Destination.Value.VolumeName.IsSet())
		assert.Equal(tt, "dest-vol", repl.Destination.Value.VolumeName.Value)
		assert.True(tt, repl.Destination.Value.VolumeId.IsSet())
		assert.Equal(tt, "dest-vol-id", repl.Destination.Value.VolumeId.Value)
		assert.True(tt, repl.State.IsSet())
		assert.Equal(tt, gcpgenserver.ReplicationV1betaState("READY"), repl.State.Value)
		assert.True(tt, repl.StateDetails.IsSet())
		assert.Equal(tt, "Active state", repl.StateDetails.Value)
		assert.True(tt, repl.StateDetailsCode.IsSet())
		assert.Equal(tt, int32(1), repl.StateDetailsCode.Value)
		assert.True(tt, repl.Role.IsSet())
		assert.Equal(tt, gcpgenserver.ReplicationV1betaRole("SOURCE"), repl.Role.Value)
		assert.True(tt, repl.ReplicationSchedule.IsSet())
		assert.Equal(tt, gcpgenserver.ReplicationV1betaReplicationSchedule("HOURLY"), repl.ReplicationSchedule.Value)
		assert.True(tt, repl.MirrorState.IsSet())
		assert.Equal(tt, gcpgenserver.ReplicationV1betaMirrorState("MIRRORED"), repl.MirrorState.Value)
		assert.True(tt, repl.Healthy.IsSet())
		assert.True(tt, repl.Healthy.Value)
		assert.True(tt, repl.TransferStats.IsSet())
		assert.True(tt, repl.TransferStats.Value.TotalTransferBytes.IsSet())
		assert.Equal(tt, float64(1000.0), repl.TransferStats.Value.TotalTransferBytes.Value)
		assert.True(tt, repl.TransferStats.Value.TotalTransferTimeSecs.IsSet())
		assert.Equal(tt, float64(100.0), repl.TransferStats.Value.TotalTransferTimeSecs.Value)
		assert.True(tt, repl.TransferStats.Value.LastTransferSize.IsSet())
		assert.Equal(tt, float64(500.0), repl.TransferStats.Value.LastTransferSize.Value)
		assert.True(tt, repl.TransferStats.Value.LastTransferError.IsSet())
		assert.Equal(tt, "", repl.TransferStats.Value.LastTransferError.Value)
		assert.True(tt, repl.TransferStats.Value.LastTransferDuration.IsSet())
		assert.Equal(tt, float64(50.0), repl.TransferStats.Value.LastTransferDuration.Value)
		assert.True(tt, repl.TransferStats.Value.LastTransferEndTime.IsSet())
		assert.Equal(tt, now, repl.TransferStats.Value.LastTransferEndTime.Value)
		assert.True(tt, repl.TransferStats.Value.TotalProgress.IsSet())
		assert.Equal(tt, float64(75.0), repl.TransferStats.Value.TotalProgress.Value)
		assert.True(tt, repl.TransferStats.Value.ProgressLastUpdated.IsSet())
		assert.Equal(tt, now, repl.TransferStats.Value.ProgressLastUpdated.Value)
		assert.True(tt, repl.TransferStats.Value.LagTime.IsSet())
		assert.Equal(tt, float64(10.0), repl.TransferStats.Value.LagTime.Value)
		assert.True(tt, repl.Created.IsSet())
		assert.Equal(tt, now, repl.Created.Value)
		assert.True(tt, repl.Labels.IsSet())
		assert.Equal(tt, "value", repl.Labels.Value["key"])
		assert.True(tt, repl.ClusterLocation.IsSet())
		assert.Equal(tt, "us-east1", repl.ClusterLocation.Value)
		assert.True(tt, repl.HybridReplicationType.IsSet())
		assert.Equal(tt, gcpgenserver.ReplicationV1betaHybridReplicationType("ONPREM_REPLICATION"), repl.HybridReplicationType.Value)
	})

	t.Run("WhenOptionalFieldsAreNotSet", func(tt *testing.T) {
		commonReplications := []commonparams.ReplicationV1beta{
			{
				ReplicationId: nillable.ToPointer("repl-2"),
				ResourceId:    nillable.ToPointer("resource-2"),
			},
		}

		result := convertCommonReplicationV1betaToGcp(commonReplications)

		assert.Len(tt, result, 1)
		repl := result[0]
		assert.True(tt, repl.ReplicationId.IsSet())
		assert.Equal(tt, "repl-2", repl.ReplicationId.Value)
		assert.False(tt, repl.Description.IsSet())
		assert.False(tt, repl.Source.IsSet())
		assert.False(tt, repl.Destination.IsSet())
		assert.False(tt, repl.State.IsSet())
		assert.False(tt, repl.TransferStats.IsSet())
		assert.False(tt, repl.Labels.IsSet())
	})

	t.Run("WhenEmptyArray", func(tt *testing.T) {
		commonReplications := []commonparams.ReplicationV1beta{}

		result := convertCommonReplicationV1betaToGcp(commonReplications)

		assert.Len(tt, result, 0)
	})
}

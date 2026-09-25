package replicationActivities

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestGetSrcBasePathCleanup(t *testing.T) {
	t.Run("ValidSrcBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "us-central1",
						},
					},
				},
			},
		}
		activity := CleanupVolumeReplicationActivity{}

		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "", nil
		}
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "https://src-base-path.example.com", nil
		}

		updatedResult, err := activity.GetSrcBasePathCleanup(context.Background(), result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcBasePath)
		assert.Equal(tt, "https://src-base-path.example.com", *updatedResult.SrcBasePath)
	})
	t.Run("ErrorSrcBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "us-central1",
						},
					},
				},
			},
		}
		activity := CleanupVolumeReplicationActivity{}

		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "", nil
		}
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		updatedResult, err := activity.GetSrcBasePathCleanup(context.Background(), result)

		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetDstBasePathCleanup(t *testing.T) {
	t.Run("ValidDstBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation: "us-east1",
						},
					},
				},
			},
		}
		activity := CleanupVolumeReplicationActivity{}

		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-east1", "", nil
		}
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "https://dst-base-path.example.com", nil
		}

		updatedResult, err := activity.GetDstBasePathCleanup(context.Background(), result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstBasePath)
		assert.Equal(tt, "https://dst-base-path.example.com", *updatedResult.DstBasePath)
	})
	t.Run("ErrorDstBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation: "us-east1",
						},
					},
				},
			},
		}
		activity := CleanupVolumeReplicationActivity{}

		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-east1", "", nil
		}
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		updatedResult, err := activity.GetDstBasePathCleanup(context.Background(), result)

		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetSignedSrcTokenCleanup(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "location-id",
						},
					},
				},
			},
			SrcProjectNumber: &srcPrj,
		}

		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "signed-token", nil
		}

		updatedResult, err := activity.GetSignedSrcTokenCleanup(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcJwtToken)
		assert.Equal(tt, "signed-token", *updatedResult.SrcJwtToken)
	})
	t.Run("WhenError", func(tt *testing.T) {
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "location-id",
						},
					},
				},
			},
			SrcProjectNumber: &srcPrj,
		}

		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		updatedResult, err := activity.GetSignedSrcTokenCleanup(ctx, result)

		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetSignedDstTokenCleanup(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		dstPrj := "dstPrj"
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "location-id",
						},
					},
				},
			},
			DstProjectNumber: &dstPrj,
			SrcProjectNumber: &srcPrj,
		}

		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "signed-token", nil
		}

		updatedResult, err := activity.GetSignedDstTokenCleanup(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstJwtToken)
		assert.Equal(tt, "signed-token", *updatedResult.DstJwtToken)
	})
	t.Run("WhenSuccessSameProject", func(tt *testing.T) {
		prj := "prj"
		token := "signed-token"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "location-id",
						},
					},
				},
			},
			SrcJwtToken:      &token,
			SrcProjectNumber: &prj,
			DstProjectNumber: &prj,
		}

		updatedResult, err := activity.GetSignedDstTokenCleanup(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstJwtToken)
		assert.Equal(tt, "signed-token", *updatedResult.DstJwtToken)
	})
	t.Run("WhenError", func(tt *testing.T) {
		dstPrj := "dstPrj"
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "location-id",
						},
					},
				},
			},
			DstProjectNumber: &dstPrj,
			SrcProjectNumber: &srcPrj,
		}

		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		updatedResult, err := activity.GetSignedDstTokenCleanup(ctx, result)

		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestDeleteReplicationOnDestinationForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		res := &googleproxyclient.VolumeReplicationInternalV1beta{
			Jobs: []googleproxyclient.JobV1beta{
				googleproxyclient.JobV1beta{
					JobId: googleproxyclient.NewOptString("job-uuid"),
				},
			},
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(res, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.JobId, "job-uuid")
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		// Return a different response type that's not handled in the switch
		unknownResp := &googleproxyclient.V1betaInternalDeleteVolumeReplicationNoContent{}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error deleting volume replication")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			DstReplication:   &googleproxyclient.VolumeReplicationInternalV1beta{},
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalDeleteVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeReplication(ctx, *deleteReplicationParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteReplicationOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Error deleting volume replication", err.Error())
	})
}

func TestDeleteSnapmirrorSnapshotsOnDestinationForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		res := &googleproxyclient.OperationV1beta{
			Name: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol-uuid/operations/job-uuid"),
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(res, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-uuid", result.JobId)
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unknownResp := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotTooManyRequests{}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error deleting volume snapmirror snapshot on destination")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:   "location-id",
							DestinationVolumeUUID: "vol-uuid",
						},
					},
				},
			},
		}
		deleteSmSnapshotsParams := &googleproxyclient.V1betaInternalDeleteVolumeSnapmirrorSnapshotParams{
			ProjectNumber:  *inputResult.DstProjectNumber,
			LocationId:     inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeId:       inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationVolumeUUID,
			XCorrelationID: googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDeleteVolumeSnapmirrorSnapshot(ctx, *deleteSmSnapshotsParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Cannot delete snapshot while snapshot is in use", err.Error())
	})
}

func TestGetReplicationOnDestinationForCleanup(t *testing.T) {
	dstPrj := "dstPrj"
	dstPath := "dstPath"
	dstToken := "dstToken"
	replicationUUID := "replication-uuid"
	locationID := "location-id"
	correlationID := "correlation-id"

	t.Run("WhenDestinationReplicationUUIDEmpty", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: "", // Empty UUID
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.Equal(tt, result, updatedResult) // Should return the same result without making API call
	})

	t.Run("Success", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		replicationObj := googleproxyclient.VolumeReplicationInternalV1beta{}
		okResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{replicationObj},
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(okResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstReplication)
	})

	t.Run("Error", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(nil, errors.New("some-error"))

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
	t.Run("SuccessNotFound", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		okResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalNotFound{}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(okResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.Equal(tt, updatedResult.Error, nil)
	})
	t.Run("SuccessBadRequest", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalBadRequest{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(badRequestResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		result, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
	})
	t.Run("SuccessInternalServerError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		internalServerErrorResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalInternalServerError{
			Message: "Internal server error message",
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(internalServerErrorResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		result, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
	})
	t.Run("SuccessUnauthorized", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		unauthorizedResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalUnauthorized{
			Message: "Unauthorized error message",
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(unauthorizedResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		result, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
	})
	t.Run("SuccessForbidden", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		forbiddenResp := &googleproxyclient.V1betaGetMultipleReplicationsInternalForbidden{
			Message: "Forbidden error message",
		}
		params := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{replicationUUID}}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(
			mock.Anything, &body, params,
		).Return(forbiddenResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
						},
					},
				},
			},
		}
		result, err := activity.GetReplicationOnDestinationForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
	})
}

func TestGetDestinationVolumeForCleanup(t *testing.T) {
	dstPrj := "dstPrj"
	dstPath := "dstPath"
	dstToken := "dstToken"
	replicationUUID := "replication-uuid"
	locationID := "location-id"
	volumeId := "vol-1"
	correlationID := "correlation-id"
	t.Run("Success", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		okResp := &googleproxyclient.VolumeV1beta{
			ResourceId: "vol-1",
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			mock.Anything, params,
		).Return(okResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstVolume)
	})

	t.Run("Error", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			mock.Anything, params,
		).Return(nil, errors.New("some-error"))

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
	t.Run("WhenDestinationVolumeUUIDIsEmpty", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      "",
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
	})
	t.Run("SuccessNotFound", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		okResp := &googleproxyclient.V1betaDescribeVolumeNotFound{}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			mock.Anything, params,
		).Return(okResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		updatedResult, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.NoError(tt, err)
		assert.Equal(tt, updatedResult.Error, nil)
	})
	t.Run("SuccessBadRequest", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		badRequestResp := &googleproxyclient.V1betaDescribeVolumeBadRequest{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			ctx, params,
		).Return(badRequestResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		result, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
	})
	t.Run("SuccessInternalServerError", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaDescribeVolumeInternalServerError{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			ctx, params,
		).Return(badRequestResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		result, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
	})
	t.Run("SuccessUnauthorized", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		ctx := context.Background()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaDescribeVolumeUnauthorized{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			ctx, params,
		).Return(badRequestResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		result, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
	})
	t.Run("SuccessForbidden", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		ctx := context.Background()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaDescribeVolumeForbidden{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaDescribeVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volumeId,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		mockClient.EXPECT().V1betaDescribeVolume(
			ctx, params,
		).Return(badRequestResp, nil)

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        locationID,
							DestinationReplicationUUID: replicationUUID,
							DestinationVolumeUUID:      volumeId,
						},
					},
				},
			},
		}
		result, err := activity.GetDestinationVolumeForCleanup(context.Background(), result)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
	})
}

func TestReleaseReplicationOnSourceForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}

		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(&googleproxyclient.OperationV1beta{Name: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol-uuid/operations/job-uuid"), Done: googleproxyclient.NewOptBool(true)}, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.JobId, "job-uuid")
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		// Return a different response type that's not handled in the switch
		unknownResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationMethodNotAllowed{}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &dstPath,
			SrcProjectNumber: &dstProj,
			SrcJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		deleteReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *deleteReplicationParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnSourceForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Error releasing volume replication", err.Error())
	})
}

func TestDeHydrateDestinationVolumeReplicationForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"
	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		hydrationEnabled = true
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					Location:                 "us-east4",
					SourceProjectNumber:      "src-proj",
					DestinationProjectNumber: "dst-proj",
					ReplicationModel: &datamodel.VolumeReplication{
						Name: "replication-name",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "us-central1-a",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
							DestinationVolumeName:      "volume-name",
							SourceLocation:             "us-central1",
							SourceVolumeName:           "src-volume-name",
						},
					},
				},
			},
		}
		originalHydrateVolumeReplication := deHydrateVolumeReplication
		originalReplicationInternalParseRegionAndZone := replicationInternalParseRegionAndZone
		defer func() {
			deHydrateVolumeReplication = originalHydrateVolumeReplication
			replicationInternalParseRegionAndZone = originalReplicationInternalParseRegionAndZone
			hydrationEnabled = false
		}()

		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			if location == "us-central1-a" {
				return "us-central1", "us-central1-a", nil
			}
			return "us-central1", "", nil
		}
		deHydrateVolumeReplication = func(ctx context.Context, createReplicationResponse models.VolumeReplication, project string) error {
			return errors.New("hydration error")
		}
		_, err := activity.DeHydrateDestinationVolumeReplicationForCleanup(ctx, inputResult)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		hydrationEnabled = true
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					Location:                 "us-central1-a",
					SourceProjectNumber:      "src-proj",
					DestinationProjectNumber: "dst-proj",
					ReplicationModel: &datamodel.VolumeReplication{
						Name: "replication-name",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "us-central1-a",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
							DestinationVolumeName:      "volume-name",
							SourceLocation:             "us-east4-a",
							SourceVolumeName:           "src-volume-name",
						},
					},
				},
			},
		}
		originalHydrateVolumeReplication := deHydrateVolumeReplication
		defer func() {
			deHydrateVolumeReplication = originalHydrateVolumeReplication
			hydrationEnabled = false
		}()

		deHydrateVolumeReplication = func(ctx context.Context, createReplicationResponse models.VolumeReplication, project string) error {
			return nil
		}
		_, err := activity.DeHydrateDestinationVolumeReplicationForCleanup(ctx, inputResult)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccessForDestinationRegion", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		hydrationEnabled = true
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					Location:                 "us-east4-a",
					SourceProjectNumber:      "src-proj",
					DestinationProjectNumber: "dst-proj",
					ReplicationModel: &datamodel.VolumeReplication{
						Name: "replication-name",
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "us-central1-a",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
							DestinationVolumeName:      "volume-name",
							SourceLocation:             "us-east4-a",
							SourceVolumeName:           "src-volume-name",
						},
					},
				},
			},
		}
		originalHydrateVolumeReplication := deHydrateVolumeReplication
		defer func() {
			deHydrateVolumeReplication = originalHydrateVolumeReplication
			hydrationEnabled = false
		}()

		deHydrateVolumeReplication = func(ctx context.Context, createReplicationResponse models.VolumeReplication, project string) error {
			return nil
		}
		_, err := activity.DeHydrateDestinationVolumeReplicationForCleanup(ctx, inputResult)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestDeHydrateDestinationVolumeForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"
	volume := &googleproxyclient.VolumeV1beta{}

	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		hydrationEnabled = true
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			DstVolume:        volume,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		originalDeHydrateVolume := deHydrateVolume
		defer func() {
			deHydrateVolume = originalDeHydrateVolume
			hydrationEnabled = false
		}()
		deHydrateVolume = func(ctx context.Context, destVolume models.Volume, project string) error {
			return errors.New("hydration error")
		}
		_, err := activity.DeHydrateDestinationVolumeForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		hydrationEnabled = true
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			DstVolume:        volume,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		originalDeHydrateVolume := deHydrateVolume
		defer func() {
			deHydrateVolume = originalDeHydrateVolume
			hydrationEnabled = false
		}()
		deHydrateVolume = func(ctx context.Context, destVolume models.Volume, project string) error {
			return nil
		}
		_, err := activity.DeHydrateDestinationVolumeForCleanup(ctx, inputResult)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestStopReplicationOnDestinationForCleanup(t *testing.T) {
	dstPrj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	replicationUUID := "replication-uuid"
	locationID := "location-id"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		res := &googleproxyclient.VolumeReplicationInternalV1beta{
			Jobs: []googleproxyclient.JobV1beta{
				{JobId: googleproxyclient.NewOptString("job-uuid")},
			},
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(res, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-uuid", result.JobId)
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaInternalStopVolumeReplicationBadRequest{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalStopVolumeReplicationInternalServerError{
			Message: "Internal server error message",
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalStopVolumeReplicationUnauthorized{
			Message: "Unauthorized error message",
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		forbiddenResp := &googleproxyclient.V1betaInternalStopVolumeReplicationForbidden{
			Message: "Forbidden error message",
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		notFoundResp := &googleproxyclient.V1betaInternalStopVolumeReplicationNotFound{
			Message: "Not found error message",
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		// Return a different response type that's not handled in the switch
		unknownResp := &googleproxyclient.V1betaInternalStopVolumeReplicationMethodNotAllowed{}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error stopping volume replication")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := googleproxyclient.V1betaInternalStopVolumeReplicationParams{
			ProjectNumber:       dstPrj,
			LocationId:          locationID,
			VolumeReplicationId: replicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(correlationID),
		}
		req := googleproxyclient.V1betaInternalStopVolumeReplicationReq{
			Force: googleproxyclient.NewOptBool(true),
		}
		mockClient.EXPECT().V1betaInternalStopVolumeReplication(ctx, &req, params).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.StopReplicationOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Failed to stop replication", err.Error())
	})
}

func TestDeleteVolumeOnDestinationForCleanup(t *testing.T) {
	dstPrj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	volUUID := "vol-uuid"
	locationID := "location-id"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		volume := &googleproxyclient.VolumeV1beta{}
		byte, _ := json.Marshal(volume)
		operation := &googleproxyclient.OperationV1beta{
			Name:     googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol-uuid/operations/job-uuid"),
			Response: byte,
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(operation, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "job-uuid", result.JobId)
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		badRequestResp := &googleproxyclient.V1betaDeleteVolumeBadRequest{
			Message: "Bad request error message",
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		internalServerErrorResp := &googleproxyclient.V1betaDeleteVolumeInternalServerError{
			Message: "Internal server error message",
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		unauthorizedResp := &googleproxyclient.V1betaDeleteVolumeUnauthorized{
			Message: "Unauthorized error message",
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		forbiddenResp := &googleproxyclient.V1betaDeleteVolumeForbidden{
			Message: "Forbidden error message",
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		notFoundResp := &googleproxyclient.V1betaDeleteVolumeNotFound{
			Message: "Not found error message",
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		// Return a different response type that's not handled in the switch
		unknownResp := &googleproxyclient.V1betaDeleteVolumeNoContent{}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error deleting volume")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := googleproxyclient.V1betaDeleteVolumeParams{
			ProjectNumber:  dstPrj,
			LocationId:     locationID,
			VolumeId:       volUUID,
			XCorrelationID: googleproxyclient.NewOptString(correlationID),
		}
		body := googleproxyclient.OptV1betaDeleteVolumeReq{
			Set:   true,
			Value: googleproxyclient.V1betaDeleteVolumeReq{DeleteAssociatedBackups: googleproxyclient.OptBool{Set: true, Value: false}},
		}
		mockClient.EXPECT().V1betaDeleteVolume(ctx, body, params).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstPrj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
							DestinationVolumeUUID:      "vol-uuid",
						},
					},
				},
			},
		}
		result, err := activity.DeleteVolumeOnDestinationForCleanup(ctx, inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "Failed to delete volume", err.Error())
	})
}

func TestDescribeRemoteJobForCleanup(t *testing.T) {
	t.Run("DescribeJobSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation: "test-location-id",
						},
					},
				},
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(true)}, nil)

		err := activity.DescribeRemoteJobForCleanup(ctx, result)

		assert.NoError(tt, err)
	})
	t.Run("DescribeJobNotFinished", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation: "test-location-id",
						},
					},
				},
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}
		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(false)}, nil)
		err := activity.DescribeRemoteJobForCleanup(ctx, result)

		assert.Error(tt, err)
	})
	t.Run("DescribeJobError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation: "test-location-id",
						},
					},
				},
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(nil, errors.New("some error"))
		err := activity.DescribeRemoteJobForCleanup(ctx, result)

		assert.Error(tt, err)
	})
}

func TestDescribeSourceJobForCleanup(t *testing.T) {
	t.Run("DescribeJobSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			SrcProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "test-location-id",
						},
					},
				},
			},
			SrcBasePath: nillable.GetStringPtr("base-path"),
			SrcJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.SrcProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(true)}, nil)

		err := activity.DescribeSourceJobForCleanup(ctx, result)

		assert.NoError(tt, err)
	})
	t.Run("DescribeJobNotFinished", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			SrcProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "test-location-id",
						},
					},
				},
			},
			SrcBasePath: nillable.GetStringPtr("base-path"),
			SrcJwtToken: nillable.GetStringPtr("jwt-token"),
		}
		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.SrcProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(false)}, nil)
		err := activity.DescribeSourceJobForCleanup(ctx, result)

		assert.Error(tt, err)
	})
	t.Run("DescribeJobError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result := &replication.DeleteReplicationResult{
			JobId:            "test-job-id",
			SrcProjectNumber: nillable.GetStringPtr("test-project-number"),
			CorrelationID:    nillable.GetStringPtr("test-correlation-id"),
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					XCorrelationID: nillable.GetStringPtr("test-xcorrelation-id"),
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation: "test-location-id",
						},
					},
				},
			},
			SrcBasePath: nillable.GetStringPtr("base-path"),
			SrcJwtToken: nillable.GetStringPtr("jwt-token"),
		}
		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    result.JobId,
			ProjectNumber:  *result.SrcProjectNumber,
			LocationId:     result.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(nil, errors.New("some error"))
		err := activity.DescribeSourceJobForCleanup(ctx, result)

		assert.Error(tt, err)
	})
}

func TestUpdateReplicationRecordOnDestinationForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}

		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(&googleproxyclient.OperationV1beta{Name: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol-uuid/operations/job-uuid"), Done: googleproxyclient.NewOptBool(true)}, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.JobId, "job-uuid")
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unknownResp := &googleproxyclient.V1betaInternalReleaseVolumeReplicationMethodNotAllowed{}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		releaseReplicationParams := &googleproxyclient.V1betaInternalReleaseVolumeReplicationParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
			IsCleanup:           googleproxyclient.NewOptBool(true),
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalReleaseVolumeReplication(ctx, *releaseReplicationParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationRecordOnDestinationForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Error releasing volume replication")
	})
}

func TestUpdateReplicationOnSourceToErrorStateForCleanup(t *testing.T) {
	srcProj := "projSrc"
	srcPath := "srcPath"
	srcToken := "srcToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}

		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(&googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{}, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, inputResult, result)
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalUpdateStateBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalUpdateStateInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalUpdateStateUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalUpdateStateForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalUpdateStateNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unknownResp := &googleproxyclient.V1betaInternalUpdateStateMethodNotAllowed{}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			SrcBasePath:      &srcPath,
			SrcProjectNumber: &srcProj,
			SrcJwtToken:      &srcToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							SourceLocation:        "location-id",
							SourceReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.SrcProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.SourceLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.SourceReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnSourceToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})
}

func TestUpdateReplicationOnDestinationToErrorStateForCleanup(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	correlationID := "correlation-id"

	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}

		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(&googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{}, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, inputResult, result)
	})

	t.Run("WhenBadRequestError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalUpdateStateBadRequest{
			Message: "Bad request error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Bad request error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		internalServerErrorResp := &googleproxyclient.V1betaInternalUpdateStateInternalServerError{
			Message: "Internal server error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(internalServerErrorResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Internal server error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenUnauthorizedError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unauthorizedResp := &googleproxyclient.V1betaInternalUpdateStateUnauthorized{
			Message: "Unauthorized error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(unauthorizedResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Unauthorized error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenForbiddenError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		forbiddenResp := &googleproxyclient.V1betaInternalUpdateStateForbidden{
			Message: "Forbidden error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(forbiddenResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Forbidden error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		notFoundResp := &googleproxyclient.V1betaInternalUpdateStateNotFound{
			Message: "Not found error message",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(notFoundResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "Not found error message")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenUnknownResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		unknownResp := &googleproxyclient.V1betaInternalUpdateStateMethodNotAllowed{}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(unknownResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "unknown response type")
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenGenericError", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(nil, errors.New("some-error"))
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Failed to update volume replication state on")
	})

	t.Run("WhenDestinationReplicationUUIDIsEmpty", func(tt *testing.T) {
		mockStorage := &database.MockStorage{}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "",
						},
					},
				},
			},
		}
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, inputResult, result)
	})

	t.Run("WhenBadRequestWithVolumeReplicationNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mockStorage := &database.MockStorage{}
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		badRequestResp := &googleproxyclient.V1betaInternalUpdateStateBadRequest{
			Message: "volume replication not found",
		}
		inputResult := &replication.DeleteReplicationResult{
			DstBasePath:      &dstPath,
			DstProjectNumber: &dstProj,
			DstJwtToken:      &dstToken,
			CorrelationID:    &correlationID,
			Event: &replication.DeleteReplicationEvent{
				CommonReplicationEventParams: replication.CommonReplicationEventParams{
					ReplicationModel: &datamodel.VolumeReplication{
						ReplicationAttributes: &datamodel.ReplicationDetails{
							DestinationLocation:        "location-id",
							DestinationReplicationUUID: "replication-uuid",
						},
					},
				},
			},
		}
		updateRequest := googleproxyclient.VolumeReplicationUpdateStateInternalV1beta{
			State:        googleproxyclient.NewOptString(datamodel.LifeCycleStateError),
			StateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateDeletionErrorDetails),
		}
		updateParams := googleproxyclient.V1betaInternalUpdateStateParams{
			ProjectNumber:       *inputResult.DstProjectNumber,
			LocationId:          inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationLocation,
			VolumeReplicationId: inputResult.Event.ReplicationModel.ReplicationAttributes.DestinationReplicationUUID,
			XCorrelationID:      googleproxyclient.NewOptString(*inputResult.CorrelationID),
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalUpdateState(ctx, &updateRequest, updateParams).Return(badRequestResp, nil)
		activity := CleanupVolumeReplicationActivity{SE: mockStorage}
		result, err := activity.UpdateReplicationOnDestinationToErrorStateForCleanup(context.Background(), inputResult)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, inputResult, result)
	})
}

package replicationActivities

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/temporal"
)

func TestGetDestinationPoolDetails(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		icLifs := []string{"10.1.1.1", "10.1.1.2"}

		res := &googleproxyclient.PoolInternalV1beta{
			InterclusterLifs: icLifs,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, poolDetails)
		assert.Equal(tt, poolDetails.DstIps, icLifs)
	})
	t.Run("WhenDestinationPoolHasActiveClusterUpgrade", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		res := &googleproxyclient.PoolInternalV1beta{
			InterclusterLifs:        []string{"10.1.1.1"},
			HasActiveClusterUpgrade: googleproxyclient.NewOptBool(true),
		}

		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		appErr, ok := err.(*temporal.ApplicationError)
		assert.True(tt, ok, "expected Temporal ApplicationError, got %T", err)
		if ok && appErr.Type() == vsaerrors.CustomErrorType {
			var trackingID int
			var errorDetails string
			errDetails := appErr.Details(&trackingID, &errorDetails)
			assert.NoError(tt, errDetails)
			assert.Equal(tt, vsaerrors.ErrStoragePoolTemporarilyUnavailable, trackingID)
		}
	})
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(nil, errors.New("some error"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "some error")
	})
	t.Run("WhenBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolBadRequest{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenUnauthorized", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolUnauthorized{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenForbidden", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolForbidden{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolInternalServerError{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolNotFound{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenUnprocessableEntity", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolUnprocessableEntity{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
	t.Run("WhenMethodNotAllowed", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(&googleproxyclient.V1betaInternalDescribePoolMethodNotAllowed{Code: 400, Message: "some error"}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})

	t.Run("WhenPoolNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
		}

		describePoolParams := &googleproxyclient.V1betaInternalDescribePoolParams{
			PoolName:       replicationResult.Event.DestinationPoolName,
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalDescribePool(ctx, *describePoolParams).Return(nil, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		poolDetails, err := activity.GetDestinationPoolDetails(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, poolDetails)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to describe pool")
	})
}

func TestAcceptClusterPeering(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		res := googleproxyclient.ClusterPeerV1{
			Jobs: []googleproxyclient.JobV1beta{
				googleproxyclient.JobV1beta{JobId: googleproxyclient.NewOptString("job-uuid")},
			},
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, *result.JobId, "job-uuid")
	})
	t.Run("WhenPassphraseNotPresent", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Nil(tt, result.JobId)
	})
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(nil, errors.New("some error"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "some error")
	})
	t.Run("WhenClusterPeerNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(nil, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerBadRequest{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenUnauthorized", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerUnauthorized{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenForbidden", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerForbidden{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerInternalServerError{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenUnprocessableEntity", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerUnprocessableEntity{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
	t.Run("WhenConflict", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		passphrase := "pass"
		srcIps := []string{"10.1.1.1", "10.1.1.2"}
		dstPoolUuid := "dst-pool-uuid"
		xCorrelationID := "test-correlation-id"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				XCorrelationID:        &xCorrelationID,
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			Passphrase:       &passphrase,
			SrcIps:           srcIps,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(dstPoolUuid),
			},
		}

		acceptClusterPeerParams := &googleproxyclient.V1betaInternalAcceptClusterPeerParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString(*replicationResult.Event.XCorrelationID),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalAcceptClusterPeer(ctx, mock.Anything, *acceptClusterPeerParams).Return(&googleproxyclient.V1betaInternalAcceptClusterPeerConflict{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.AcceptClusterPeering(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to accept cluster peer")
	})
}

func TestCreateDestinationVolume(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}

		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		volume := &googleproxyclient.VolumeV1beta{}
		byte, _ := json.Marshal(volume)
		res := &googleproxyclient.OperationV1beta{
			Name:     googleproxyclient.NewOptString("/v1beta/projects/45110233509/locations/australia-southeast1/operations/job-uuid"),
			Response: byte,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, *result.JobId, "job-uuid")
	})
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}

		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(nil, errors.New("some error"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "some error")
	})
	t.Run("WhenOperationNotReturned", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}

		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(nil, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenJsonUnmarshalError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}

		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		// Create invalid JSON that will cause unmarshaling error
		invalidJSON := []byte(`{"invalid": "json", "missing": "closing"`)
		res := &googleproxyclient.OperationV1beta{
			Name:     googleproxyclient.NewOptString("/v1beta/projects/45110233509/locations/australia-southeast1/operations/job-uuid"),
			Response: invalidJSON,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Check that the error is a VCPError with ErrorFailedToUnmarshal
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrorFailedToUnmarshal, customErr.TrackingID)
	})
	t.Run("WhenBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}
		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.V1betaCreateVolumeBadRequest{Message: "bad request error"}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient { return mc }
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenUnauthorized", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}
		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.V1betaCreateVolumeUnauthorized{Message: "unauthorized error"}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient { return mc }
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenForbidden", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}
		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.V1betaCreateVolumeForbidden{Message: "forbidden error"}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient { return mc }
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenConflict", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}
		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.V1betaCreateVolumeConflict{Message: "conflict error"}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient { return mc }
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		destPoolUuid := "uuid"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId: googleproxyclient.NewOptString(destPoolUuid),
			},
		}
		createVolumeParams := &googleproxyclient.V1betaCreateVolumeParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.V1betaCreateVolumeInternalServerError{Message: "internal server error"}
		mc := &googleproxyclient.ProxyClient{Invoker: mockClient}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient { return mc }
		mockClient.EXPECT().V1betaCreateVolume(ctx, mock.Anything, *createVolumeParams).Return(res, nil)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateDestinationVolume(ctx, replicationResult)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestCreateReplicationOnDestination(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	destPoolUuid := "uuid"
	destVolUuid := "vol-uuid"
	resourceId := "rep-1"
	repSchedule := "HOURLY"
	srcSvm := "src-svm"
	dstSvm := "dst-svm"
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
			DbVolReplication: &datamodel.VolumeReplication{
				Uri:       "projects/srcPrj/locations/us-east4/volumes/crrsrc2/replications/replication2",
				RemoteUri: "projects/dstPrj/locations/australia-southeast1/volumes/crrdst2/replications/replication2",
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		res := &googleproxyclient.VolumeReplicationInternalV1beta{
			Jobs: []googleproxyclient.JobV1beta{
				googleproxyclient.JobV1beta{
					JobId: googleproxyclient.NewOptString("job-uuid"),
				},
			},
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, mock.Anything, *internalCreateVolumeReplicationParams).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, *result.JobId, "job-uuid")
	})
	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(nil, errors.New("some-error"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*vsaerrors.CustomError).OriginalErr.Error(), "some-error")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})
	t.Run("WhenResponseEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(nil, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})
	t.Run("WhenBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationBadRequest{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenUnauthorized", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationUnauthorized{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenForbidden", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationForbidden{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationNotFound{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationInternalServerError{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenUnprocessableEntity", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationUnprocessableEntity{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})

	t.Run("WhenConflict", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationPoolName:   "pool1",
				DestinationLocationID: "us-est1",
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "srcCluster",
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID:                  &resourceId,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{},
					ReplicationSchedule:         &repSchedule,
				},
				SourceVolume: datamodel.Volume{
					Name: "src-vol",
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "src-token",
						Protocols:     []string{"iscsi"},
						BlockProperties: &datamodel.BlockProperties{
							OSType: "linux",
						},
					},
				},
			},
			SrcSvm:           &srcSvm,
			DstSvm:           &dstSvm,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
			DstProjectNumber: &dstProj,
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:      googleproxyclient.NewOptString(destPoolUuid),
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstVolume: &gcpserver.VolumeV1beta{
				ResourceId: "dst-vol-name",
				VolumeId:   gcpserver.NewOptString(destVolUuid),
			},
		}

		internalCreateVolumeReplicationParams := &googleproxyclient.V1betaInternalCreateVolumeReplicationParams{
			ProjectNumber:  *replicationResult.DstProjectNumber,
			LocationId:     replicationResult.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		body := googleproxyclient.VolumeReplicationCreateInternalV1beta{}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		convertVolumeReplicationCreateParams = func(result replication.CreateReplicationResult) googleproxyclient.VolumeReplicationCreateInternalV1beta {
			return body
		}
		mockClient.EXPECT().V1betaInternalCreateVolumeReplication(ctx, &body, *internalCreateVolumeReplicationParams).Return(&googleproxyclient.V1betaInternalCreateVolumeReplicationConflict{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateReplicationOnDestination(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Internal error while creating replication")
		convertVolumeReplicationCreateParams = _convertVolumeReplicationCreateParams
	})
}

func TestUpdateKmsConfigState_Success(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := VolumeReplicationCreateActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	replication := &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test", State: datamodel.LifeCycleStateUpdating, StateDetails: "updated"}

	mockStorage.On("UpdateVolumeReplicationStates", ctx, replication).Return(nil)

	// Act
	err := activity.UpdateReplicationState(ctx, *replication)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestUpdateKmsConfigState_Error(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := VolumeReplicationCreateActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	replication := &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test", State: datamodel.LifeCycleStateUpdating, StateDetails: "updated"}

	mockStorage.On("UpdateVolumeReplicationStates", ctx, replication).Return(errors.New("some error"))

	// Act
	err := activity.UpdateReplicationState(ctx, *replication)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestUpdateReplicationDetails(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	destPoolUuid := "uuid"
	destVolUuid := "vol-uuid"
	resourceId := "rep-1"
	repSchedule := "HOURLY"
	srcSvm := "src-svm"
	dstSvm := "dst-svm"
	dstRepUuid := "dst-rep-uuid"
	result := &replication.CreateReplicationResult{
		Event: &replication.CreateReplicationEvent{
			DestinationPoolName:   "pool1",
			DestinationLocationID: "us-est1",
			SourcePool: datamodel.Pool{
				ClusterDetails: datamodel.ClusterDetails{
					ExternalName: "srcCluster",
				},
			},
			CreateReplicationParams: &replication.CreateReplicationParamsBody{
				ResourceID:                  &resourceId,
				DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				ReplicationSchedule:         &repSchedule,
			},
			SourceVolume: datamodel.Volume{
				Name: "src-vol",
				VolumeAttributes: &datamodel.VolumeAttributes{
					CreationToken: "src-token",
					Protocols:     []string{"iscsi"},
					BlockProperties: &datamodel.BlockProperties{
						OSType: "linux",
					},
				},
			},
		},
		SrcSvm:           &srcSvm,
		DstSvm:           &dstSvm,
		DstBasePath:      &dstPath,
		DstJwtToken:      &dstToken,
		DstProjectNumber: &dstProj,
		DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString(dstRepUuid),
			ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSPROJECTREPLICATION),
		},
		DstPool: &googleproxyclient.PoolInternalV1beta{
			PoolId:      googleproxyclient.NewOptString(destPoolUuid),
			ClusterName: googleproxyclient.NewOptString("dst-cluster"),
		},
		DstVolume: &gcpserver.VolumeV1beta{
			ResourceId: "dst-vol-name",
			VolumeId:   gcpserver.NewOptString(destVolUuid),
		},
		DbVolReplication: &datamodel.VolumeReplication{
			Name:                  "rep-1",
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		},
	}

	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volumeRep := result.DbVolReplication
		volumeRep.State = datamodel.LifeCycleStateCreated
		volumeRep.StateDetails = datamodel.LifeCycleStateCreatedDetails
		volumeRep.ReplicationAttributes.DestinationPoolUUID = result.DstPool.PoolId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeUUID = result.DstVolume.VolumeId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeName = result.DstVolume.ResourceId
		volumeRep.ReplicationAttributes.SourceSvmName = *result.SrcSvm
		volumeRep.ReplicationAttributes.DestinationSvmName = *result.DstSvm
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)
		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(nil)
		_, err := activity.UpdateReplicationDetails(ctx, result)

		// Assert
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volumeRep := result.DbVolReplication
		volumeRep.State = datamodel.LifeCycleStateCreated
		volumeRep.StateDetails = datamodel.LifeCycleStateCreatedDetails
		volumeRep.ReplicationAttributes.DestinationPoolUUID = result.DstPool.PoolId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeUUID = result.DstVolume.VolumeId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeName = result.DstVolume.ResourceId
		volumeRep.ReplicationAttributes.SourceSvmName = *result.SrcSvm
		volumeRep.ReplicationAttributes.DestinationSvmName = *result.DstSvm
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)
		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(errors.New("some error"))
		_, err := activity.UpdateReplicationDetails(ctx, result)

		// Assert
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateDestinationVolumeDetails(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	destPoolUuid := "uuid"
	destVolUuid := "vol-uuid"
	resourceId := "rep-1"
	repSchedule := "HOURLY"
	srcSvm := "src-svm"
	dstSvm := "dst-svm"
	dstRepUuid := "dst-rep-uuid"
	result := &replication.CreateReplicationResult{
		Event: &replication.CreateReplicationEvent{
			DestinationPoolName:   "pool1",
			DestinationLocationID: "us-est1",
			SourcePool: datamodel.Pool{
				ClusterDetails: datamodel.ClusterDetails{
					ExternalName: "srcCluster",
				},
			},
			CreateReplicationParams: &replication.CreateReplicationParamsBody{
				ResourceID:                  &resourceId,
				DestinationVolumeParameters: &replication.DestinationVolumeParams{},
				ReplicationSchedule:         &repSchedule,
			},
			SourceVolume: datamodel.Volume{
				Name: "src-vol",
				VolumeAttributes: &datamodel.VolumeAttributes{
					CreationToken: "src-token",
					Protocols:     []string{"iscsi"},
					BlockProperties: &datamodel.BlockProperties{
						OSType: "linux",
					},
				},
			},
		},
		SrcSvm:           &srcSvm,
		DstSvm:           &dstSvm,
		DstBasePath:      &dstPath,
		DstJwtToken:      &dstToken,
		DstProjectNumber: &dstProj,
		DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString(dstRepUuid),
			ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSPROJECTREPLICATION),
		},
		DstPool: &googleproxyclient.PoolInternalV1beta{
			PoolId:      googleproxyclient.NewOptString(destPoolUuid),
			ClusterName: googleproxyclient.NewOptString("dst-cluster"),
		},
		DstVolume: &gcpserver.VolumeV1beta{
			ResourceId: "dst-vol-name",
			VolumeId:   gcpserver.NewOptString(destVolUuid),
		},
		DbVolReplication: &datamodel.VolumeReplication{
			Name:                  "rep-1",
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		},
	}

	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volumeRep := result.DbVolReplication
		volumeRep.State = datamodel.LifeCycleStateCreated
		volumeRep.StateDetails = datamodel.LifeCycleStateCreatedDetails
		volumeRep.ReplicationAttributes.DestinationPoolUUID = result.DstPool.PoolId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeUUID = result.DstVolume.VolumeId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeName = result.DstVolume.ResourceId
		volumeRep.ReplicationAttributes.SourceSvmName = *result.SrcSvm
		volumeRep.ReplicationAttributes.DestinationSvmName = *result.DstSvm
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)
		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(nil)
		_, err := activity.UpdateDestinationVolumeDetails(ctx, result)

		// Assert
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		volumeRep := result.DbVolReplication
		volumeRep.State = datamodel.LifeCycleStateCreated
		volumeRep.StateDetails = datamodel.LifeCycleStateCreatedDetails
		volumeRep.ReplicationAttributes.DestinationPoolUUID = result.DstPool.PoolId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeUUID = result.DstVolume.VolumeId.Value
		volumeRep.ReplicationAttributes.DestinationVolumeName = result.DstVolume.ResourceId
		volumeRep.ReplicationAttributes.SourceSvmName = *result.SrcSvm
		volumeRep.ReplicationAttributes.DestinationSvmName = *result.DstSvm
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)
		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(errors.New("some error"))
		_, err := activity.UpdateDestinationVolumeDetails(ctx, result)

		// Assert
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateDestinationVolumeReplicationDetails(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	destPoolUuid := "uuid"
	destVolUuid := "vol-uuid"
	srcSvm := "src-svm"
	dstSvm := "dst-svm"
	dstRepUuid := "dst-rep-uuid"
	result := &replication.CreateReplicationResult{
		Event: &replication.CreateReplicationEvent{
			DestinationLocationID: "us-central1-b",
			DestinationPoolName:   "test-pool",
			SourcePool: datamodel.Pool{
				ClusterDetails: datamodel.ClusterDetails{
					ExternalName: "src-cluster",
				},
			},
		},
		DstProjectNumber: &dstProj,
		DstBasePath:      &dstPath,
		DstJwtToken:      &dstToken,
		DstPool: &googleproxyclient.PoolInternalV1beta{
			PoolId:      googleproxyclient.NewOptString(destPoolUuid),
			ClusterName: googleproxyclient.NewOptString("dst-cluster"),
		},
		DstVolume: &gcpserver.VolumeV1beta{
			ResourceId: "dst-vol-name",
			VolumeId:   gcpserver.NewOptString(destVolUuid),
		},
		DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString(dstRepUuid),
			ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSPROJECTREPLICATION),
		},
		DbVolReplication: &datamodel.VolumeReplication{
			Name:                  "rep-1",
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		},
		SrcSvm: &srcSvm,
		DstSvm: &dstSvm,
	}

	t.Run("WhenSuccess", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		volumeRep := result.DbVolReplication
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)

		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(nil)

		// Act
		updatedResult, err := activity.UpdateDestinationVolumeReplicationDetails(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		assert.Equal(tt, result.Event.SourcePool.ClusterDetails.ExternalName, updatedResult.DbVolReplication.ReplicationAttributes.SourceHostName)
		assert.Equal(tt, result.DstPool.ClusterName.Value, updatedResult.DbVolReplication.ReplicationAttributes.DestinationHostName)
		assert.Equal(tt, result.DstReplication.VolumeReplicationUuid.Value, updatedResult.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID)
		assert.Equal(tt, volumeRep.UUID, updatedResult.DbVolReplication.ReplicationAttributes.SourceReplicationUUID)
		assert.Equal(tt, string(result.DstReplication.ReplicationType.Value), updatedResult.DbVolReplication.ReplicationAttributes.ReplicationType)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenError", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := database.NewMockStorage(t)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		volumeRep := result.DbVolReplication
		volumeRep.ReplicationAttributes.SourceHostName = result.Event.SourcePool.ClusterDetails.ExternalName
		volumeRep.ReplicationAttributes.DestinationHostName = result.DstPool.ClusterName.Value
		volumeRep.ReplicationAttributes.DestinationReplicationUUID = result.DstReplication.VolumeReplicationUuid.Value
		volumeRep.ReplicationAttributes.SourceReplicationUUID = volumeRep.UUID
		volumeRep.ReplicationAttributes.ReplicationType = string(result.DstReplication.ReplicationType.Value)

		mockStorage.On("UpdateVolumeReplication", ctx, volumeRep).Return(errors.New("some error"))

		// Act
		updatedResult, err := activity.UpdateDestinationVolumeReplicationDetails(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		mockStorage.AssertExpectations(tt)
	})
}

func TestVolumeReplicationCreateActivity_GetVolumeSVMNames(t *testing.T) {
	t.Run("Success_ValidSVMNames", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Setup mock volume response from storage
		srcVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "src-svm-name",
			},
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		// Setup result with required fields
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &xCorrelationID,
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "src-cluster",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstProjectNumber: &[]string{"123456789"}[0],
			DstBasePath:      &[]string{"https://test-base-path.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-dst-volume-id"),
			},
		}

		// Mock the destination volume response
		expectedDestVolume := &googleproxyclient.InternalVolumeV1beta{
			SvmName: googleproxyclient.NewOptNilString("dst-svm-name"),
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "",
			VolumeId:       "test-dst-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(expectedDestVolume, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcSvm)
		assert.NotNil(tt, updatedResult.DstSvm)
		assert.Equal(tt, "src-svm-name", *updatedResult.SrcSvm)
		assert.Equal(tt, "dst-svm-name", *updatedResult.DstSvm)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Error_SourceVolumeDescribeFailed", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		// Setup mock storage to return error
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(nil, errors.New("source volume not found"))

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "src-cluster",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.Contains(tt, err.Error(), "source volume not found")

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Error_DestinationVolumeDescribeFailed", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Setup mock volume response from storage
		srcVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "src-svm-name",
			},
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &xCorrelationID,
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "src-cluster",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("dst-cluster"),
			},
			DstProjectNumber: &[]string{"123456789"}[0],
			DstBasePath:      &[]string{"https://test-base-path.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-dst-volume-id"),
			},
		}

		// Mock the GetGProxyClient function to return error
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "",
			VolumeId:       "test-dst-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("destination volume not found"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "destination volume not found")

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_EmptyClusterNames", func(tt *testing.T) {
		// Arrange
		mockStorage := database.NewMockStorage(t)
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Mock DescribeVolume call that happens in GetVolumeSVMNames
		mockVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}
		mockStorage.On("DescribeVolume", mock.Anything, "test-source-volume-uuid").Return(mockVolume, nil)

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedDstVolume := &googleproxyclient.InternalVolumeV1beta{
			ResourceId: googleproxyclient.NewOptString("test-volume"),
			SvmName:    googleproxyclient.NewOptNilString("test-dst-svm"),
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			VolumeId:       "test-dst-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(mock.Anything, expectedParams).Return(expectedDstVolume, nil)

		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &xCorrelationID,
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "test-source-volume-uuid"},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "",
					},
				},
			},
			DstProjectNumber: &[]string{"123456789"}[0],
			DstBasePath:      &[]string{"https://test-base-path.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-dst-volume-id"),
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString(""),
			},
		}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(context.Background(), result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcSvm)
		assert.NotNil(tt, updatedResult.DstSvm)
		assert.Equal(tt, "test-svm", *updatedResult.SrcSvm)
		assert.Equal(tt, "test-dst-svm", *updatedResult.DstSvm)

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NilDstPool", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Mock source volume
		mockVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}
		sourceVolumeUUID := "source-volume-uuid"

		// Mock destination fields
		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		dstLocationID := "us-central1-b"
		dstVolumeID := "dst-volume-id"

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &[]string{"test-correlation-id"}[0],
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: sourceVolumeUUID},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "src-cluster",
					},
				},
				DestinationLocationID: dstLocationID,
			},
			DstPool:          nil,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstProjectNumber: &dstProjectNumber,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString(dstVolumeID),
			},
		}

		// Setup mocks
		mockStorage.On("DescribeVolume", ctx, sourceVolumeUUID).Return(mockVolume, nil)

		// Mock google proxy client
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		expectedDescribeParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  dstProjectNumber,
			LocationId:     dstLocationID,
			VolumeId:       dstVolumeID,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		mockDescribeResponse := &googleproxyclient.InternalVolumeV1beta{
			SvmName: googleproxyclient.NewOptNilString("test-dst-svm"),
		}

		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedDescribeParams).Return(mockDescribeResponse, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcSvm)
		assert.NotNil(tt, updatedResult.DstSvm)
		assert.Equal(tt, "test-svm", *updatedResult.SrcSvm)
		assert.Equal(tt, "test-dst-svm", *updatedResult.DstSvm)
	})

	t.Run("Success_LongClusterNames", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Mock source volume
		mockVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "test-svm",
			},
		}
		sourceVolumeUUID := "source-volume-uuid"

		// Mock destination fields
		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		dstLocationID := "us-central1-b"
		dstVolumeID := "dst-volume-id"

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &[]string{"test-correlation-id"}[0],
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: sourceVolumeUUID},
				},
				SourcePool: datamodel.Pool{
					ClusterDetails: datamodel.ClusterDetails{
						ExternalName: "very-long-source-cluster-name-for-testing",
					},
				},
				DestinationLocationID: dstLocationID,
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("very-long-destination-cluster-name-for-testing"),
			},
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstProjectNumber: &dstProjectNumber,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString(dstVolumeID),
			},
		}

		// Setup mocks
		mockStorage.On("DescribeVolume", ctx, sourceVolumeUUID).Return(mockVolume, nil)

		// Mock google proxy client
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		expectedDescribeParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  dstProjectNumber,
			LocationId:     dstLocationID,
			VolumeId:       dstVolumeID,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		mockDescribeResponse := &googleproxyclient.InternalVolumeV1beta{
			SvmName: googleproxyclient.NewOptNilString("test-dst-svm"),
		}

		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedDescribeParams).Return(mockDescribeResponse, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcSvm)
		assert.NotNil(tt, updatedResult.DstSvm)
		assert.Equal(tt, "test-svm", *updatedResult.SrcSvm)
		assert.Equal(tt, "test-dst-svm", *updatedResult.DstSvm)
	})

	t.Run("Error_SourceVolumeNilSvm", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		// Setup mock volume response from storage with nil SVM
		srcVolume := &datamodel.Volume{
			Svm: nil, // SVM is nil
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.Contains(tt, err.Error(), "Source volume SVM name not found")

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Error_SourceVolumeEmptySvmName", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		// Setup mock volume response from storage with empty SVM name
		srcVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "", // SVM name is empty
			},
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.Contains(tt, err.Error(), "Source volume SVM name not found")

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Error_DestinationVolumeSvmNameNotSet", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Setup mock volume response from storage
		srcVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "src-svm-name",
			},
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &[]string{"test-correlation-id"}[0],
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &[]string{"123456789"}[0],
			DstBasePath:      &[]string{"https://test-base-path.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-dst-volume-id"),
			},
		}

		// Mock the destination volume response with SvmName not set
		expectedDestVolume := &googleproxyclient.InternalVolumeV1beta{
			SvmName: googleproxyclient.OptNilString{Set: false}, // SvmName is not set
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "us-central1",
			VolumeId:       "test-dst-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(expectedDestVolume, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.Contains(tt, err.Error(), "Destination volume SVM name not found")

		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_DestinationVolumeEmptySvmName", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Setup mock volume response from storage
		srcVolume := &datamodel.Volume{
			Svm: &datamodel.Svm{
				Name: "src-svm-name",
			},
		}
		mockStorage.On("DescribeVolume", ctx, "test-source-uuid").Return(srcVolume, nil)

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID: &[]string{"test-correlation-id"}[0],
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{
						UUID: "test-source-uuid",
					},
				},
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &[]string{"123456789"}[0],
			DstBasePath:      &[]string{"https://test-base-path.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-dst-volume-id"),
			},
		}

		// Mock the destination volume response with empty SvmName but set
		expectedDestVolume := &googleproxyclient.InternalVolumeV1beta{
			SvmName: googleproxyclient.NewOptNilString(""), // Empty but set SvmName
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "us-central1",
			VolumeId:       "test-dst-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(expectedDestVolume, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}

		// Act
		updatedResult, err := activity.GetVolumeSVMNames(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcSvm)
		assert.NotNil(tt, updatedResult.DstSvm)
		assert.Equal(tt, "src-svm-name", *updatedResult.SrcSvm)
		assert.Equal(tt, "", *updatedResult.DstSvm) // Empty destination SVM name

		mockStorage.AssertExpectations(tt)
	})
}

func TestVolumeReplicationCreateActivity_DescribeVolume(t *testing.T) {
	t.Run("Success_ValidVolumeDescription", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        &xCorrelationID,
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &dstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		expectedVolume := &googleproxyclient.InternalVolumeV1beta{
			ResourceId:  googleproxyclient.NewOptString("test-volume"),
			UsedBytes:   googleproxyclient.NewOptNilFloat64(1024000000000),
			VolumeState: googleproxyclient.NewOptInternalVolumeV1betaVolumeState(googleproxyclient.InternalVolumeV1betaVolumeStateREADY),
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "us-central1",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(expectedVolume, nil)

		// Act
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, resultVolume)
		assert.Equal(tt, "test-volume", resultVolume.ResourceId.Value)
		assert.Equal(tt, float64(1024000000000), resultVolume.UsedBytes.Value)
		assert.Equal(tt, googleproxyclient.InternalVolumeV1betaVolumeStateREADY, resultVolume.VolumeState.Value)
	})

	t.Run("Error_ClientError", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        &xCorrelationID,
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &dstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  "123456789",
			LocationId:     "us-central1",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("volume not found"))

		// Act
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, resultVolume)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "volume not found")
	})

	t.Run("Error_EmptyProjectNumber", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Provide empty project number but valid other fields to avoid nil pointer dereference
		emptyProjectNumber := ""
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        &xCorrelationID,
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &emptyProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  emptyProjectNumber,
			LocationId:     "us-central1",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		// Mock client to return an error for empty project number
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("invalid project number"))

		// Act
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, resultVolume)
	})

	t.Run("Error_EmptyLocationID", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        &xCorrelationID,
				DestinationLocationID: "",
			},
			DstProjectNumber: &dstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  dstProjectNumber,
			LocationId:     "",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		// Mock client to return an error for empty location ID
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("invalid location ID"))

		// Act
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, resultVolume)
	})

	t.Run("Error_MissingEvent", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProjectNumber := "123456789"
		dstBasePath := "https://test-base-path.com"
		dstJwtToken := "test-jwt-token"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        &xCorrelationID,
				DestinationLocationID: "us-central1",
			},
			DstProjectNumber: &dstProjectNumber,
			DstBasePath:      &dstBasePath,
			DstJwtToken:      &dstJwtToken,
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		// Mock the GetGProxyClient function
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  dstProjectNumber,
			LocationId:     "us-central1",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		// Mock client to return an error to simulate missing event or other failure
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("event processing error"))

		// Act
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, resultVolume)
	})

	t.Run("Success_ValidParametersConstruction", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockClient := googleproxyclient.NewMockInvoker(t)

		// Mock the googleproxyclient.GetGProxyClient to return a client that will cause an error
		// but after parameter validation, so we can verify parameter construction is correct
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return &googleproxyclient.ProxyClient{
				Invoker: mockClient,
			}
		}

		dstProjectNumber := "123456789"
		xCorrelationID := "test-correlation-id"
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "us-central1",
				XCorrelationID:        &xCorrelationID,
			},
			DstProjectNumber: &dstProjectNumber,
			DstBasePath:      &[]string{"https://test.example.com"}[0],
			DstJwtToken:      &[]string{"test-jwt-token"}[0],
			DstVolume: &gcpserver.VolumeV1beta{
				VolumeId: gcpserver.NewOptString("test-volume-id"),
			},
		}

		expectedParams := googleproxyclient.V1betaInternalDescribeVolumeParams{
			ProjectNumber:  dstProjectNumber,
			LocationId:     "us-central1",
			VolumeId:       "test-volume-id",
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		// Mock client to return an error to test parameter construction
		mockClient.EXPECT().V1betaInternalDescribeVolume(ctx, expectedParams).Return(nil, errors.New("parameter construction test error"))

		// Act - This will fail due to mock error, but we test that parameters are constructed correctly
		resultVolume, err := DescribeVolume(ctx, result)

		// Assert - We expect an error due to mock error, but parameters were validated
		assert.Error(tt, err)
		assert.Nil(tt, resultVolume)
	})
}

func TestGetSrcBasePath(t *testing.T) {
	t.Run("ValidSrcBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		// Arrange
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceRegion: "us-east1",
			},
		}
		activity := VolumeReplicationCreateActivity{}

		// Mock the InternalParseRegionAndZone function
		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "", nil
		}
		// Mock the InternalUtilGetPairedRegionURI function
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "https://src-base-path.example.com", nil
		}

		// Act
		updatedResult, err := activity.GetSrcBasePath(context.Background(), result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcBasePath)
		assert.Equal(tt, "https://src-base-path.example.com", *updatedResult.SrcBasePath)
	})

	t.Run("ErrorSrcBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		// Arrange
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				LocationID: "invalid-location",
			},
		}
		activity := VolumeReplicationCreateActivity{}

		// Mock the InternalParseRegionAndZone function
		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "", "", errors.New("failed to parse location")
		}
		// Mock the InternalUtilGetPairedRegionURI function
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		// Act
		updatedResult, err := activity.GetSrcBasePath(context.Background(), result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetDstBasePath(t *testing.T) {
	t.Run("ValidDstBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		// Arrange
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationRegion: "us-central1",
			},
		}
		activity := VolumeReplicationCreateActivity{}

		// Mock the InternalParseRegionAndZone function
		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "us-central1", "", nil
		}
		// Mock the InternalUtilGetPairedRegionURI function
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "https://dst-base-path.example.com", nil
		}

		// Act
		updatedResult, err := activity.GetDstBasePath(context.Background(), result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstBasePath)
		assert.Equal(tt, "https://dst-base-path.example.com", *updatedResult.DstBasePath)
	})

	t.Run("ErrorDstBasePath", func(tt *testing.T) {
		defer func() {
			replicationInternalParseRegionAndZone = replication.InternalParseRegionAndZone
			replicationInternalUtilGetPairedRegionURI = replication.InternalUtilGetPairedRegionURI
		}()

		// Arrange
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "invalid-location",
			},
		}
		activity := VolumeReplicationCreateActivity{}

		// Mock the InternalParseRegionAndZone function
		replicationInternalParseRegionAndZone = func(location string) (string, string, error) {
			return "", "", errors.New("failed to parse location")
		}
		// Mock the InternalUtilGetPairedRegionURI function
		replicationInternalUtilGetPairedRegionURI = func(locationID string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		// Act
		updatedResult, err := activity.GetDstBasePath(context.Background(), result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetSignedSrcToken(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				LocationID: "src-location-id",
			},
			SrcProjectNumber: &srcPrj,
		}

		// Mock the InternalUtilGetSignedToken function
		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "signed-token", nil
		}

		// Act
		updatedResult, err := activity.GetSignedSrcToken(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.SrcJwtToken)
		assert.Equal(tt, "signed-token", *updatedResult.SrcJwtToken)
	})

	t.Run("WhenError", func(tt *testing.T) {
		// Arrange
		srcPrj := "srcPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				LocationID: "src-location-id",
			},
			SrcProjectNumber: &srcPrj,
		}

		// Mock the InternalUtilGetSignedToken function
		replication.InternalUtilGetSignedToken = func(locationID string) (string, error) {
			return "", errors.New("some error")
		}

		// Act
		updatedResult, err := activity.GetSignedSrcToken(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestGetSignedDstToken(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		dstPrj := "dstPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstProjectNumber: &dstPrj,
		}

		// Mock the InternalUtilGetSignedToken function
		replication.InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "signed-dst-token", nil
		}

		// Act
		updatedResult, err := activity.GetSignedDstToken(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult.DstJwtToken)
		assert.Equal(tt, "signed-dst-token", *updatedResult.DstJwtToken)
	})

	t.Run("WhenError", func(tt *testing.T) {
		// Arrange
		dstPrj := "dstPrj"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstProjectNumber: &dstPrj,
		}

		// Mock the InternalUtilGetSignedToken function
		replication.InternalUtilGetSignedToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		// Act
		updatedResult, err := activity.GetSignedDstToken(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func TestAcceptSvmPeer(t *testing.T) {
	t.Run("WhenAlreadyPeered", func(tt *testing.T) {
		// Arrange
		dstSvm := "dst-svm"
		srcSvm := "src-svm"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockProvider := new(vsa.MockProvider)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstSvm: &dstSvm,
			SrcSvm: &srcSvm,
		}

		svmPeer := &vsa.SvmPeer{
			PeerSvmName: dstSvm,
			State:       "peered",
		}
		mockProvider.On("GetSVMPeer", result.SrcSvm, result.DstSvm).Return(svmPeer, nil)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.AcceptSvmPeer(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		// Arrange
		dstSvm := "dst-svm"
		srcSvm := "src-svm"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockProvider := &vsa.MockProvider{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstSvm: &dstSvm,
			SrcSvm: &srcSvm,
		}

		svmPeer := &vsa.SvmPeer{
			PeerSvmName: dstSvm,
		}
		mockProvider.On("GetSVMPeer", result.SrcSvm, result.DstSvm).Return(svmPeer, nil)
		mockProvider.On("AcceptSvmPeering", srcSvm, dstSvm).Return(nil)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.AcceptSvmPeer(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenError", func(tt *testing.T) {
		// Arrange
		dstSvm := "dst-svm"
		srcSvm := "src-svm"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockProvider := &vsa.MockProvider{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstSvm: &dstSvm,
			SrcSvm: &srcSvm,
		}

		svmPeer := &vsa.SvmPeer{
			PeerSvmName: dstSvm,
		}
		mockProvider.On("GetSVMPeer", &srcSvm, &dstSvm).Return(svmPeer, nil)
		mockProvider.On("AcceptSvmPeering", srcSvm, dstSvm).Return(errors.New("some-error"))
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.AcceptSvmPeer(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenGetSvmPeerError", func(tt *testing.T) {
		// Arrange
		dstSvm := "dst-svm"
		srcSvm := "src-svm"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockProvider := &vsa.MockProvider{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstSvm: &dstSvm,
			SrcSvm: &srcSvm,
		}

		mockProvider.On("GetSVMPeer", &srcSvm, &dstSvm).Return(nil, errors.New("some-error"))
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.AcceptSvmPeer(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenProviderByNodeError", func(tt *testing.T) {
		// Arrange
		dstSvm := "dst-svm"
		srcSvm := "src-svm"
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockProvider := &vsa.MockProvider{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstSvm: &dstSvm,
			SrcSvm: &srcSvm,
		}

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		// Act
		updatedResult, err := activity.AcceptSvmPeer(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.EqualError(tt, err, "provider error")
		mockProvider.AssertExpectations(tt)
	})
}

func TestGetSourceInterclusterLifs(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockProvider := new(vsa.MockProvider)
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					Name: "source-pool",
				},
			},
			SrcNode: &models.Node{},
		}

		interclusterLifs := []*vsa.InterclusterLif{
			&vsa.InterclusterLif{Address: "10.1.1.1"},
			&vsa.InterclusterLif{Address: "10.1.1.2"},
		}
		mockProvider.On("GetInterclusterLIFs", "default-intercluster").Return(interclusterLifs, nil)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.GetSourceInterclusterLifs(ctx, result)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		assert.Equal(tt, []string{"10.1.1.1", "10.1.1.2"}, updatedResult.SrcIps)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenError", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockProvider := new(vsa.MockProvider)
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					Name: "source-pool",
				},
			},
			SrcNode: &models.Node{},
		}

		mockProvider.On("GetInterclusterLIFs", "default-intercluster").Return(nil, errors.New("failed to fetch intercluster LIFs"))
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Act
		updatedResult, err := activity.GetSourceInterclusterLifs(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeError", func(tt *testing.T) {
		// Arrange
		ctx := context.Background()
		mockProvider := new(vsa.MockProvider)
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					Name: "source-pool",
				},
			},
			SrcNode: &models.Node{},
		}

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to fetch intercluster LIFs")
		}

		// Act
		updatedResult, err := activity.GetSourceInterclusterLifs(ctx, result)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
		assert.EqualError(tt, err, "failed to fetch intercluster LIFs")
		mockProvider.AssertExpectations(tt)
	})
}

func TestHydrateDestinationVolume(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ResourceId: "pool-resource-id",
			},
			DstVolume: &gcpserver.VolumeV1beta{},
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "location-id",
			},
			DstProjectNumber: nillable.GetStringPtr("project-number"),
		}
		hydrationEnabled = true

		originalDeHydrateVolume := hydrateVolume
		defer func() {
			hydrateVolume = originalDeHydrateVolume
			hydrationEnabled = false
		}()
		// Mock the hydrateVolume function
		hydrateVolume = func(ctx context.Context, destVolume models.Volume, project string, poolResourceId string) error {
			return nil
		}
		updatedResult, err := activity.HydrateDestinationVolume(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
	})

	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ResourceId: "pool-resource-id",
			},
			DstVolume: &gcpserver.VolumeV1beta{},
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "location-id",
			},
			DstProjectNumber: nillable.GetStringPtr("project-number"),
		}
		hydrationEnabled = true
		originalDeHydrateVolume := hydrateVolume
		defer func() {
			hydrateVolume = originalDeHydrateVolume
			hydrationEnabled = false
		}()
		hydrateVolume = func(ctx context.Context, destVolume models.Volume, project string, poolResourceId string) error {
			return errors.New("hydration error")
		}
		updatedResult, err := activity.HydrateDestinationVolume(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, updatedResult)
	})
}

func Test_convertVolumeV1BetaToVolumeModel(t *testing.T) {
	// Arrange
	vol := gcpserver.VolumeV1beta{
		Protocols: []gcpserver.ProtocolsV1beta{
			gcpserver.ProtocolsV1betaISCSI,
		},
		VolumeId:     gcpserver.OptString{Value: "volume-id"},
		ResourceId:   "vol-1",
		QuotaInBytes: gcpserver.NewOptFloat64(float64(1234)),
		VolumeState:  gcpserver.NewOptVolumeV1betaVolumeState(gcpserver.VolumeV1betaVolumeStateREADY),
	}
	dstLocation := "us-central1"

	// Act
	result := convertVolumeV1BetaToVolumeModel(vol, dstLocation)

	// Assert
	assert.Equal(t, "volume-id", result.UUID)
	assert.Equal(t, "vol-1", result.DisplayName)
	assert.Equal(t, dstLocation, result.Region)
	assert.Equal(t, "READY", result.LifeCycleState)
	assert.ElementsMatch(t, []string{"ISCSI"}, result.ProtocolTypes)
}

func TestCreateClusterPeer(t *testing.T) {
	t.Run("TestCreateClusterPeer_Success", func(t *testing.T) {
		// Arrange
		mockProvider := new(vsa.MockProvider) // Use the mock provider
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

		// Mock GetProviderByNode to return the mock provider
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := VolumeReplicationCreateActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		expectedResponse := &vsa.ClusterPeer{
			UUID:         "12345",
			ExternalUUID: "12345",
		}
		result := replication.CreateReplicationResult{
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("cluster1"),
			},
		}
		mockProvider.On("ListClusterPeers").Return([]*vsa.ClusterPeer{
			{
				PeerClusterName: "peer1",
				PeerAddresses:   []string{"192.168.1.2"},
				Availability:    "Available",
			},
		}, nil)
		mockProvider.On("CreateClusterPeer", mock.Anything).Return(expectedResponse, nil)
		_, err := activity.CreateClusterPeering(ctx, &result)

		assert.NoError(t, err)
		mockProvider.AssertExpectations(t)
	})
	t.Run("CreateClusterPeerReturnsErrorWhenProviderFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := VolumeReplicationCreateActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		result := replication.CreateReplicationResult{
			DstPool: &googleproxyclient.PoolInternalV1beta{
				ClusterName: googleproxyclient.NewOptString("cluster1"),
			},
		}

		mockProvider.On("ListClusterPeers").Return([]*vsa.ClusterPeer{
			{
				PeerClusterName: "peer1",
				PeerAddresses:   []string{"192.168.1.2"},
				Availability:    "Available",
			},
		}, nil)
		mockProvider.On("CreateClusterPeer", mock.Anything).Return(nil, errors.New("provider error"))
		_, err := activity.CreateClusterPeering(ctx, &result)

		assert.Error(t, err)
		assert.Equal(t, "provider error", err.Error())
		mockProvider.AssertExpectations(t)
	})
}

func TestDescribeRemoteJob(t *testing.T) {
	t.Run("DescribeJob_Success", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			JobId:            nillable.GetStringPtr("test-job-id"),
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "test-location-id",
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    *result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(true)}, nil)

		err := activity.DescribeRemoteJob(ctx, result)

		assert.NoError(tt, err)
	})

	t.Run("DescribeJob_Error", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			JobId:            nillable.GetStringPtr("test-job-id"),
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			Event: &replication.CreateReplicationEvent{
				DestinationLocationID: "test-location-id",
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    *result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(nil, errors.New("some error"))
		err := activity.DescribeRemoteJob(ctx, result)

		assert.Error(tt, err)
	})

	t.Run("DescribeJob_NotFinished", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			JobId:            nillable.GetStringPtr("test-job-id"),
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "test-location-id",
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    *result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(false)}, nil)
		err := activity.DescribeRemoteJob(ctx, result)

		assert.Error(tt, err)
	})
	t.Run("DescribeJob_FinishedWithError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result := &replication.CreateReplicationResult{
			JobId:            nillable.GetStringPtr("test-job-id"),
			DstProjectNumber: nillable.GetStringPtr("test-project-number"),
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "test-location-id",
			},
			DstBasePath: nillable.GetStringPtr("base-path"),
			DstJwtToken: nillable.GetStringPtr("jwt-token"),
		}

		describeOperationParams := googleproxyclient.V1betaInternalDescribeOperationParams{
			OperationId:    *result.JobId,
			ProjectNumber:  *result.DstProjectNumber,
			LocationId:     result.Event.DestinationLocationID,
			XCorrelationID: googleproxyclient.NewOptString("test-xcorrelation-id"),
		}

		mockClient.EXPECT().V1betaInternalDescribeOperation(ctx, describeOperationParams).Return(&googleproxyclient.InternalOperationV1beta{Done: googleproxyclient.NewOptBool(true), Error: googleproxyclient.NewOptStatusV1Beta(googleproxyclient.StatusV1Beta{Message: googleproxyclient.NewOptString("failed")})}, nil)

		err := activity.DescribeRemoteJob(ctx, result)

		assert.Error(tt, err)
	})
}

func TestMountReplication(t *testing.T) {
	t.Run("WhenSuccessful", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		res := &googleproxyclient.InternalJobV1beta{
			JobUuid: googleproxyclient.NewOptString("job-uuid"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(res, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
	})

	t.Run("WhenError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(nil, errors.New("mount error"))

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
	t.Run("WhenBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationBadRequest{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenUnauthorized", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationUnauthorized{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenForbidden", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationForbidden{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationNotFound{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenConflict", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationConflict{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationInternalServerError{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})

	t.Run("WhenUnexpectedResponseType", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}
		mockClient := googleproxyclient.NewMockInvoker(t)

		dstProj := "projDst"
		dstPath := "dstPath"
		dstToken := "dstToken"
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				XCorrelationID:        nillable.GetStringPtr("test-xcorrelation-id"),
				DestinationLocationID: "us-central1",
			},
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid"),
			},
			DstProjectNumber: &dstProj,
			DstBasePath:      &dstPath,
			DstJwtToken:      &dstToken,
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		params := &googleproxyclient.V1betaInternalMountVolumeReplicationParams{
			ProjectNumber:       dstProj,
			LocationId:          "us-central1",
			VolumeReplicationId: replicationResult.DstReplication.VolumeReplicationUuid.Value,
			XCorrelationID:      googleproxyclient.NewOptString("test-xcorrelation-id"),
		}
		// Return an unexpected response type to trigger the default case
		mockClient.EXPECT().V1betaInternalMountVolumeReplication(ctx, *params).Return(&googleproxyclient.V1betaInternalMountVolumeReplicationMethodNotAllowed{}, nil)

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.MountReplication(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, err.(*temporal.ApplicationError).Message(), "Failed to mount volume replication")
	})
}

func TestConvertSourceVolumeToDestinationVolume(t *testing.T) {
	t.Run("WithInheritedQos_ShouldSetDestinationPerformanceLimits", func(tt *testing.T) {
		throughputMibps := int64(80)
		iops := int64(1280)
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:             "source-volume",
					SizeInBytes:      1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:        "destination-volume",
						ThroughputMibps: &throughputMibps,
						Iops:            &iops,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		result := convertSourceVolumeToDestinationVolume(replicationResult)

		assert.True(tt, result.ThroughputMibps.IsSet())
		assert.Equal(tt, float64(80), result.ThroughputMibps.Value)
		assert.True(tt, result.Iops.IsSet())
		assert.Equal(tt, int64(1280), result.Iops.Value)
		assert.False(tt, result.VolumePerformanceGroupId.IsSet())
	})

	t.Run("WithBlockVolume_ShouldNotSetCreationToken", func(tt *testing.T) {
		// Arrange
		volumeID := "block-volume-id"
		resourceID := "block-resource-id"
		shareName := "block-share-name"
		creationToken := "block-creation-token"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"ISCSI"}, // Block volume
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  volumeID,
						ShareName: shareName,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, volumeID, result.ResourceId)
		assert.Equal(tt, "", result.CreationToken.Value) // Should be empty for block volumes
		assert.Len(tt, result.Protocols, 1)
		assert.Equal(tt, googleproxyclient.ProtocolsV1betaISCSI, result.Protocols[0])
	})

	t.Run("WithCompleteNonBlockVolumeData_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange
		blockDevices := []datamodel.BlockDevice{
			{
				Name:       "test-lun-1",
				Identifier: "test-id-1",
				Size:       1073741824, // 1GB
				OSType:     "LINUX",
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "hg-uuid-1",
						HostQNs:       []string{"iqn.1993-08.org.example:host1"},
					},
				},
			},
			{
				Name:       "test-lun-2",
				Identifier: "test-id-2",
				Size:       2147483648, // 2GB
				OSType:     "WINDOWS",
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "hg-uuid-2",
						HostQNs:       []string{"iqn.1993-08.org.example:host2"},
					},
				},
			},
		}

		creationToken := "test-creation-token"
		volumeID := "test-volume-id"
		resourceID := "test-resource-id"
		shareName := "test-share-name"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 5368709120, // 5GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"ISCSI"},
						BlockDevices:  &blockDevices,
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:    volumeID,
						ShareName:   shareName,
						Description: func() *string { s := "Test destination volume"; return &s }(),
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, volumeID, result.ResourceId)
		assert.Empty(tt, result.CreationToken.Value)
		assert.Equal(tt, "pool-123", result.PoolId.Value)
		assert.Equal(tt, float64(5368709120), result.QuotaInBytes.Value)
		assert.Equal(tt, "test-network", result.Network.Value)
		assert.Equal(tt, "Test destination volume", result.Description.Value)

		// Test protocols conversion
		assert.Len(tt, result.Protocols, 1)
		protocolsMap := make(map[string]bool)
		for _, p := range result.Protocols {
			protocolsMap[string(p)] = true
		}
		assert.True(tt, protocolsMap["ISCSI"])

		// Test BlockDevices conversion
		assert.Len(tt, result.BlockDevices, 2)

		// First block device
		blockDevice1 := result.BlockDevices[0]
		assert.Equal(tt, googleproxyclient.BlockDeviceV1betaOsTypeLINUX, blockDevice1.OsType.Value)

		// Second block device
		blockDevice2 := result.BlockDevices[1]
		assert.Equal(tt, googleproxyclient.BlockDeviceV1betaOsTypeWINDOWS, blockDevice2.OsType.Value)
	})

	t.Run("WithEmptyShareName_ShouldUseCreationToken", func(tt *testing.T) {
		// Arrange
		creationToken := "fallback-creation-token"
		volumeID := "test-volume-id"
		resourceID := "test-resource-id"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"NFSV3"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  volumeID,
						ShareName: "", // Empty share name
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-456"),
				Network: "test-network-2",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, creationToken, result.CreationToken.Value)
	})

	t.Run("WithNfsUnixPermissionsAndEmptySecurityStyle_ShouldSetDestinationSecurityStyleUnix", func(tt *testing.T) {
		creationToken := "nfs-token"
		volumeID := "src-vol-id"
		resourceID := "repl-res-id"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"NFSV3"},
						FileProperties: &datamodel.FileProperties{
							UnixPermissions: "0755",
						},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID: volumeID,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		result := convertSourceVolumeToDestinationVolume(replicationResult)

		assert.True(tt, result.SecurityStyle.IsSet())
		assert.Equal(tt, googleproxyclient.VolumeV1betaSecurityStyleUNIX, result.SecurityStyle.Value)
		assert.True(tt, result.UnixPermissions.IsSet())
		assert.Equal(tt, "0755", result.UnixPermissions.Value)
	})

	t.Run("WithEmptyVolumeID_ShouldUseSourceVolumeName", func(tt *testing.T) {
		// Arrange
		creationToken := "test-creation-token"
		sourceName := "source-volume-name"
		resourceID := "test-resource-id"
		shareName := "test-share-name"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        sourceName,
					SizeInBytes: 2147483648,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"NFSV3", "NFSV4"}, // Non-block protocols only
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  "", // Empty volume ID
						ShareName: shareName,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-789"),
				Network: "test-network-3",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, sourceName, result.ResourceId)         // Should use source name when destination volume ID is empty
		assert.Equal(tt, shareName, result.CreationToken.Value) // Should use share name for non-block volumes
	})
	t.Run("WithEmptyVolumeID_ShouldUseSourceVolumeNameForBlockProtocol", func(tt *testing.T) {
		// Arrange
		creationToken := "test-creation-token"
		sourceName := "source-volume-name"
		resourceID := "test-resource-id"
		shareName := "test-share-name"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        sourceName,
					SizeInBytes: 2147483648,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"ISCSI"}, // Non-block protocols only
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  "", // Empty volume ID
						ShareName: shareName,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-789"),
				Network: "test-network-3",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, sourceName, result.ResourceId) // Should use source name when destination volume ID is empty
		assert.Empty(tt, result.CreationToken.Value)    // Should use share name for non-block volumes
	})
	t.Run("WithMixedProtocols_ShouldIdentifyAsBlockVolume", func(tt *testing.T) {
		// Arrange
		volumeID := "mixed-volume-id"
		resourceID := "mixed-resource-id"
		shareName := "mixed-share-name"
		creationToken := "mixed-creation-token"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"ISCSI", "NFSV3", "NFSV4"}, // Mixed protocols including iSCSI
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  volumeID,
						ShareName: shareName,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Equal(tt, volumeID, result.ResourceId)
		assert.Equal(tt, "", result.CreationToken.Value) // Should be empty because ISCSI is present
		assert.Len(tt, result.Protocols, 3)
	})

	t.Run("WithNilBlockDevices_ShouldHandleGracefully", func(tt *testing.T) {
		// Arrange
		creationToken := "test-creation-token"
		volumeID := "test-volume-id"
		resourceID := "test-resource-id"
		shareName := "test-share-name"

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        volumeID,
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: creationToken,
						Protocols:     []string{"NFSV3"},
						BlockDevices:  nil, // Nil block devices
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					ResourceID: &resourceID,
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  volumeID,
						ShareName: shareName,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-nil"),
				Network: "test-network-nil",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Empty(tt, result.BlockDevices)
		assert.Equal(tt, volumeID, result.ResourceId)
		assert.Equal(tt, shareName, result.CreationToken.Value)
	})

	t.Run("WithESXIBlockDevice_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange
		blockDevices := []datamodel.BlockDevice{
			{
				Name:       "esxi-lun",
				Identifier: "esxi-id",
				Size:       10737418240, // 10GB
				OSType:     "ESXI",
			},
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "esxi-volume",
					SizeInBytes: 10737418240,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "esxi-token",
						Protocols:     []string{"ISCSI"},
						BlockDevices:  &blockDevices,
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:  "esxi-volume",
						ShareName: "esxi-share",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("esxi-pool"),
				Network: "esxi-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.Len(tt, result.BlockDevices, 1)
		assert.Equal(tt, googleproxyclient.BlockDeviceV1betaOsTypeESXI, result.BlockDevices[0].OsType.Value)
	})

	t.Run("WithTieringPolicyAllFields_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange
		tierAction := googleproxyclient.TieringPolicyV1betaTierActionENABLED
		coolingThreshold := int32(30)
		hotTierBypass := true

		tieringPolicy := &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Value: tierAction,
				Set:   true,
				Null:  false,
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{
				Value: coolingThreshold,
				Set:   true,
				Null:  false,
			},
			HotTierBypassModeEnabled: googleproxyclient.OptNilBool{
				Value: hotTierBypass,
				Set:   true,
				Null:  false,
			},
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSV3"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:      "test-volume",
						ShareName:     "test-share",
						TieringPolicy: tieringPolicy,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.True(tt, result.TieringPolicy.IsSet())
		tieringPolicyResult := result.TieringPolicy.Value

		assert.True(tt, tieringPolicyResult.GetTierAction().IsSet())
		assert.Equal(tt, googleproxyclient.TieringPolicyV1betaTierActionENABLED, tieringPolicyResult.GetTierAction().Value)

		assert.True(tt, tieringPolicyResult.GetCoolingThresholdDays().IsSet())
		assert.Equal(tt, coolingThreshold, tieringPolicyResult.GetCoolingThresholdDays().Value)

		assert.True(tt, tieringPolicyResult.GetHotTierBypassModeEnabled().IsSet())
		assert.Equal(tt, hotTierBypass, tieringPolicyResult.GetHotTierBypassModeEnabled().Value)
	})

	t.Run("WithTieringPolicyPartialFields_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange - Only TierAction is set
		tierAction := googleproxyclient.TieringPolicyV1betaTierActionPAUSED

		tieringPolicy := &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Value: tierAction,
				Set:   true,
				Null:  false,
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{
				Set: false, // Not set
			},
			HotTierBypassModeEnabled: googleproxyclient.OptNilBool{
				Set: false, // Not set
			},
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 2147483648,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"ISCSI"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:      "test-volume",
						ShareName:     "test-share",
						TieringPolicy: tieringPolicy,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-456"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.True(tt, result.TieringPolicy.IsSet())
		tieringPolicyResult := result.TieringPolicy.Value

		// Only TierAction should be set
		assert.True(tt, tieringPolicyResult.GetTierAction().IsSet())
		assert.Equal(tt, googleproxyclient.TieringPolicyV1betaTierActionPAUSED, tieringPolicyResult.GetTierAction().Value)

		// Other fields should not be set
		assert.False(tt, tieringPolicyResult.GetCoolingThresholdDays().IsSet())
		assert.False(tt, tieringPolicyResult.GetHotTierBypassModeEnabled().IsSet())
	})

	t.Run("WithNilTieringPolicy_ShouldNotSetTieringPolicy", func(tt *testing.T) {
		// Arrange
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSV3"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:      "test-volume",
						ShareName:     "test-share",
						TieringPolicy: nil, // Nil tiering policy
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-789"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.False(tt, result.TieringPolicy.IsSet())
	})

	t.Run("WithTieringPolicyOnlyCoolingThreshold_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange - Only CoolingThresholdDays is set
		coolingThreshold := int32(90)

		tieringPolicy := &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Set: false, // Not set
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{
				Value: coolingThreshold,
				Set:   true,
				Null:  false,
			},
			HotTierBypassModeEnabled: googleproxyclient.OptNilBool{
				Set: false, // Not set
			},
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 5368709120,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSV4"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:      "test-volume",
						ShareName:     "test-share",
						TieringPolicy: tieringPolicy,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-cooling"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.True(tt, result.TieringPolicy.IsSet())
		tieringPolicyResult := result.TieringPolicy.Value

		// Only CoolingThresholdDays should be set
		assert.False(tt, tieringPolicyResult.GetTierAction().IsSet())
		assert.True(tt, tieringPolicyResult.GetCoolingThresholdDays().IsSet())
		assert.Equal(tt, coolingThreshold, tieringPolicyResult.GetCoolingThresholdDays().Value)
		assert.False(tt, tieringPolicyResult.GetHotTierBypassModeEnabled().IsSet())
	})

	t.Run("WithTieringPolicyOnlyHotTierBypass_ShouldConvertCorrectly", func(tt *testing.T) {
		// Arrange - Only HotTierBypassModeEnabled is set
		hotTierBypass := false

		tieringPolicy := &googleproxyclient.TieringPolicyV1beta{
			TierAction: googleproxyclient.OptNilTieringPolicyV1betaTierAction{
				Set: false, // Not set
			},
			CoolingThresholdDays: googleproxyclient.OptNilInt32{
				Set: false, // Not set
			},
			HotTierBypassModeEnabled: googleproxyclient.OptNilBool{
				Value: hotTierBypass,
				Set:   true,
				Null:  false,
			},
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 3221225472,
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"SMB"},
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:      "test-volume",
						ShareName:     "test-share",
						TieringPolicy: tieringPolicy,
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-bypass"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert
		assert.True(tt, result.TieringPolicy.IsSet())
		tieringPolicyResult := result.TieringPolicy.Value

		// Only HotTierBypassModeEnabled should be set
		assert.False(tt, tieringPolicyResult.GetTierAction().IsSet())
		assert.False(tt, tieringPolicyResult.GetCoolingThresholdDays().IsSet())
		assert.True(tt, tieringPolicyResult.GetHotTierBypassModeEnabled().IsSet())
		assert.Equal(tt, hotTierBypass, tieringPolicyResult.GetHotTierBypassModeEnabled().Value)
	})
}

func TestConvertBlockDeviceOsType(t *testing.T) {
	t.Run("ShouldConvertAllOSTypes", func(tt *testing.T) {
		testCases := []struct {
			input    string
			expected googleproxyclient.BlockDeviceV1betaOsType
		}{
			{"LINUX", googleproxyclient.BlockDeviceV1betaOsTypeLINUX},
			{"WINDOWS", googleproxyclient.BlockDeviceV1betaOsTypeWINDOWS},
			{"ESXI", googleproxyclient.BlockDeviceV1betaOsTypeESXI},
			{"UNKNOWN", googleproxyclient.BlockDeviceV1betaOsTypeOSTYPEUNSPECIFIED},
			{"", googleproxyclient.BlockDeviceV1betaOsTypeOSTYPEUNSPECIFIED},
		}

		for _, tc := range testCases {
			tt.Run(tc.input, func(ttt *testing.T) {
				result := convertBlockDeviceOsType(tc.input)
				assert.Equal(ttt, tc.expected, result)
			})
		}
	})
}

func TestCreateSnapmirrorFirewall(t *testing.T) {
	t.Run("WhenSuccessfulWithNewFirewall", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		originalInsertFirewall := activities.InsertFirewall
		defer func() { activities.InsertFirewall = originalInsertFirewall }()

		expectedOperationName := "operation-123"
		activities.InsertFirewall = func(service hyperscaler.Services, project, name, network string, priority int64, direction string, sourceRanges, portRules []string) (string, error) {
			return expectedOperationName, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
					ClusterDetails: datamodel.ClusterDetails{
						Network: "test-network",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Operation)
		assert.Equal(tt, expectedOperationName, result.Operation.OperationName)
		assert.Equal(tt, "firewall", result.Operation.OperationType)
		assert.False(tt, result.Operation.IsDone)
		assert.False(tt, result.Operation.IsRegionalResource)
		assert.Equal(tt, "test-project-123", result.Operation.Project)
		assert.Equal(tt, replicationResult, result)
	})

	t.Run("WhenSuccessful_FirewallAlreadyExists", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		originalInsertFirewall := activities.InsertFirewall
		defer func() { activities.InsertFirewall = originalInsertFirewall }()

		activities.InsertFirewall = func(service hyperscaler.Services, project, name, network string, priority int64, direction string, sourceRanges, portRules []string) (string, error) {
			return "", nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
					ClusterDetails: datamodel.ClusterDetails{
						Network: "test-network",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.NotNil(tt, result.Operation)
		assert.Equal(tt, "", result.Operation.OperationName)
		assert.Equal(tt, "firewall", result.Operation.OperationType)
		assert.True(tt, result.Operation.IsDone)
		assert.False(tt, result.Operation.IsRegionalResource)
		assert.Equal(tt, "test-project-123", result.Operation.Project)
		assert.Equal(tt, replicationResult, result)
	})

	t.Run("WhenGCPServiceError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		expectedError := errors.New("GCP service error")
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return nil, expectedError
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
					ClusterDetails: datamodel.ClusterDetails{
						Network: "test-network",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "GCP service error")
	})

	t.Run("WhenSnHostProjectMissing", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "", // Missing SnHostProject
					ClusterDetails: datamodel.ClusterDetails{
						Network: "test-network",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Resource not found")
	})

	t.Run("WhenNetworkMissing", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
					ClusterDetails: datamodel.ClusterDetails{
						Network: "",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Resource not found")
	})

	t.Run("WhenInsertFirewallError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		originalInsertFirewall := activities.InsertFirewall
		defer func() { activities.InsertFirewall = originalInsertFirewall }()

		expectedError := errors.New("firewall creation failed")
		activities.InsertFirewall = func(service hyperscaler.Services, project, name, network string, priority int64, direction string, sourceRanges, portRules []string) (string, error) {
			return "", expectedError
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
					ClusterDetails: datamodel.ClusterDetails{
						Network: "test-network",
					},
				},
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.CreateSnapmirrorFirewall(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "firewall creation failed")
	})
}

func TestPollSnapmirrorFirewallOperation(t *testing.T) {
	t.Run("WhenSuccessfulOperationPending", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetComputeOpStatus := activities.GetComputeOpStatus
		defer func() { activities.GetComputeOpStatus = originalGetComputeOpStatus }()

		mockOpStatus := &hyperscaler_models.ComputeOperation{
			Status: "RUNNING",
		}
		activities.GetComputeOpStatus = func(gcpService hyperscaler.Services, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
			return mockOpStatus, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "operation-123",
				OperationType:      "firewall",
				IsDone:             false,
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "An internal error occurred")
	})

	t.Run("WhenNoOperation", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: nil, // No operation
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, replicationResult, result)
	})

	t.Run("WhenFirewallexists", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "", // Empty operation name
				OperationType:      "firewall",
				IsDone:             true,
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, replicationResult, result)
	})

	t.Run("WhenGetComputeOpStatusError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		// Mock activities.GetComputeOpStatus to return error
		originalGetComputeOpStatus := activities.GetComputeOpStatus
		defer func() { activities.GetComputeOpStatus = originalGetComputeOpStatus }()

		expectedError := errors.New("GCP API error")
		activities.GetComputeOpStatus = func(gcpService hyperscaler.Services, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
			return nil, expectedError
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "operation-123",
				OperationType:      "firewall",
				IsDone:             false,
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenOpStatusNil", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetComputeOpStatus := activities.GetComputeOpStatus
		defer func() { activities.GetComputeOpStatus = originalGetComputeOpStatus }()

		activities.GetComputeOpStatus = func(gcpService hyperscaler.Services, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
			return nil, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "operation-123",
				OperationType:      "firewall",
				IsDone:             false,
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "An internal error occurred")
	})
	t.Run("WhenGCPServiceError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		expectedError := errors.New("GCP service initialization failed")
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return nil, expectedError
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "operation-123",
				OperationType:      "firewall",
				IsDone:             false,
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "GCP service initialization failed")
	})
	t.Run("WhenSuccessfulOperationCompleted", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := &database.MockStorage{}

		// Mock hyperscaler.ResolveCloudService to return success
		originalGetGCPService := hyperscaler.ResolveCloudService
		defer func() { hyperscaler.ResolveCloudService = originalGetGCPService }()

		mockGcpService := &google.GcpServices{}
		hyperscaler.ResolveCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockGcpService, nil
		}

		// Mock activities.GetComputeOpStatus to return DONE status
		originalGetComputeOpStatus := activities.GetComputeOpStatus
		defer func() { activities.GetComputeOpStatus = originalGetComputeOpStatus }()

		mockOpStatus := &hyperscaler_models.ComputeOperation{
			Status: "DONE",
		}
		activities.GetComputeOpStatus = func(gcpService hyperscaler.Services, project string, isRegionalResource bool, operation string) (*hyperscaler_models.ComputeOperation, error) {
			return mockOpStatus, nil
		}

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourcePool: datamodel.Pool{
					SnHostProject: "test-project-123",
				},
			},
			Operation: &commonparams.Operations{
				OperationName:      "operation-123",
				OperationType:      "firewall",
				IsDone:             false, // Initially false
				Project:            "test-project-123",
				IsRegionalResource: false,
			},
		}

		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		result, err := activity.PollSnapmirrorFirewallOperation(ctx, replicationResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Verify that the operation status was updated to done
		assert.True(tt, result.Operation.IsDone)
		// Verify that the original result was returned
		assert.Equal(tt, replicationResult, result)
	})
}

// TestConvertSourceVolumeToDestinationVolume_LargeVolumeAttributes_NewIfLoop tests the newly added if loop condition
func TestConvertSourceVolumeToDestinationVolume_LargeVolumeAttributes_NewIfLoop(t *testing.T) {
	t.Run("WithLargeCapacityTrueAndConstituentCountSet_ShouldSetBothParameters", func(tt *testing.T) {
		// Arrange
		largeCapacity := true
		constituentCount := int32(4)

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824, // 1GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSv3"},
					},
					LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
						LargeCapacity:               largeCapacity,
						LargeVolumeConstituentCount: &constituentCount,
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID: "test-volume-id",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert - Both parameters should be set because all conditions are met
		assert.True(tt, result.LargeCapacity.IsSet())
		assert.Equal(tt, largeCapacity, result.LargeCapacity.Value)

		assert.True(tt, result.LargeVolumeConstituentCount.IsSet())
		assert.Equal(tt, constituentCount, result.LargeVolumeConstituentCount.Value)
	})
	t.Run("WithLargeCapacityFalse_ShouldNotSetParameters", func(tt *testing.T) {
		// Arrange
		largeCapacity := false
		constituentCount := int32(4)

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824, // 1GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSv3"},
					},
					LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
						LargeCapacity:               largeCapacity, // false
						LargeVolumeConstituentCount: &constituentCount,
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID: "test-volume-id",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert - Parameters should NOT be set because LargeCapacity is false
		assert.False(tt, result.LargeCapacity.IsSet())
		assert.False(tt, result.LargeVolumeConstituentCount.IsSet())
	})
	t.Run("WithNilLargeVolumeConstituentCount_ShouldNotSetParameters", func(tt *testing.T) {
		// Arrange
		largeCapacity := true

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824, // 1GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSv3"},
					},
					LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
						LargeCapacity:               largeCapacity,
						LargeVolumeConstituentCount: nil, // nil constituent count
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID: "test-volume-id",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert - Parameters should NOT be set because LargeVolumeConstituentCount is nil
		assert.False(tt, result.LargeCapacity.IsSet())
		assert.False(tt, result.LargeVolumeConstituentCount.IsSet())
	})
	t.Run("WithNilLargeVolumeAttributes_ShouldNotSetParameters", func(tt *testing.T) {
		// Arrange
		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 1073741824, // 1GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSv3"},
					},
					LargeVolumeAttributes: nil, // nil LargeVolumeAttributes
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID: "test-volume-id",
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert - Parameters should NOT be set because LargeVolumeAttributes is nil
		assert.False(tt, result.LargeCapacity.IsSet())
		assert.False(tt, result.LargeVolumeConstituentCount.IsSet())
	})
	t.Run("WithVariousConstituentCounts_ShouldSetCorrectly", func(tt *testing.T) {
		testCases := []struct {
			name             string
			largeCapacity    bool
			constituentCount int32
			shouldBeSet      bool
		}{
			{"LargeCapacityTrue_ConstituentCount1", true, 1, true},
			{"LargeCapacityTrue_ConstituentCount2", true, 2, true},
			{"LargeCapacityTrue_ConstituentCount4", true, 4, true},
			{"LargeCapacityTrue_ConstituentCount8", true, 8, true},
			{"LargeCapacityTrue_ConstituentCount16", true, 16, true},
			{"LargeCapacityFalse_ConstituentCount4", false, 4, false}, // Should not be set
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(ttt *testing.T) {
				// Arrange
				replicationResult := &replication.CreateReplicationResult{
					Event: &replication.CreateReplicationEvent{
						SourceVolume: datamodel.Volume{
							Name:        "test-volume",
							SizeInBytes: 1073741824, // 1GB
							VolumeAttributes: &datamodel.VolumeAttributes{
								CreationToken: "test-token",
								Protocols:     []string{"NFSv3"},
							},
							LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
								LargeCapacity:               tc.largeCapacity,
								LargeVolumeConstituentCount: &tc.constituentCount,
							},
						},
						CreateReplicationParams: &replication.CreateReplicationParamsBody{
							DestinationVolumeParameters: &replication.DestinationVolumeParams{
								VolumeID: "test-volume-id",
							},
						},
					},
					DstPool: &googleproxyclient.PoolInternalV1beta{
						PoolId:  googleproxyclient.NewOptString("pool-123"),
						Network: "test-network",
					},
				}

				// Act
				result := convertSourceVolumeToDestinationVolume(replicationResult)

				// Assert
				if tc.shouldBeSet {
					assert.True(ttt, result.LargeCapacity.IsSet())
					assert.Equal(ttt, tc.largeCapacity, result.LargeCapacity.Value)
					assert.True(ttt, result.LargeVolumeConstituentCount.IsSet())
					assert.Equal(ttt, tc.constituentCount, result.LargeVolumeConstituentCount.Value)
				} else {
					assert.False(ttt, result.LargeCapacity.IsSet())
					assert.False(ttt, result.LargeVolumeConstituentCount.IsSet())
				}
			})
		}
	})
	t.Run("IntegrationWithOtherVolumeAttributes_ShouldWorkCorrectly", func(tt *testing.T) {
		// Arrange - Test that the new if loop works correctly with other volume attributes
		largeCapacity := true
		constituentCount := int32(4)

		replicationResult := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					Name:        "test-volume",
					SizeInBytes: 5368709120, // 5GB
					VolumeAttributes: &datamodel.VolumeAttributes{
						CreationToken: "test-token",
						Protocols:     []string{"NFSv3", "NFSv4"},
					},
					LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
						LargeCapacity:               largeCapacity,
						LargeVolumeConstituentCount: &constituentCount,
					},
				},
				CreateReplicationParams: &replication.CreateReplicationParamsBody{
					DestinationVolumeParameters: &replication.DestinationVolumeParams{
						VolumeID:    "test-volume-id",
						Description: func() *string { s := "Destination description"; return &s }(),
					},
				},
			},
			DstPool: &googleproxyclient.PoolInternalV1beta{
				PoolId:  googleproxyclient.NewOptString("pool-123"),
				Network: "test-network",
			},
		}

		// Act
		result := convertSourceVolumeToDestinationVolume(replicationResult)

		// Assert - Verify all attributes are set correctly
		assert.Equal(tt, "test-volume-id", result.ResourceId)
		assert.Equal(tt, "test-token", result.CreationToken.Value)
		assert.Equal(tt, "pool-123", result.PoolId.Value)
		assert.Equal(tt, float64(5368709120), result.QuotaInBytes.Value)
		assert.Equal(tt, "test-network", result.Network.Value)
		assert.Equal(tt, "Destination description", result.Description.Value)

		// Verify LargeVolumeAttributes are set (because all conditions are met)
		assert.True(tt, result.LargeCapacity.IsSet())
		assert.Equal(tt, largeCapacity, result.LargeCapacity.Value)
		assert.True(tt, result.LargeVolumeConstituentCount.IsSet())
		assert.Equal(tt, constituentCount, result.LargeVolumeConstituentCount.Value)

		// Verify protocols
		assert.Len(tt, result.Protocols, 2)
		protocolsMap := make(map[string]bool)
		for _, p := range result.Protocols {
			protocolsMap[string(p)] = true
		}
	})
}

func TestCreateQuotaRulesOnDestination(t *testing.T) {
	dstProj := "projDst"
	dstPath := "dstPath"
	dstToken := "dstToken"
	destVolUuid := "vol-uuid"
	locationID := "us-east1"

	baseResult := &replication.CreateReplicationResult{
		Event: &replication.CreateReplicationEvent{
			DestinationLocationID: locationID,
			SourceVolume: datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
			},
		},
		DstBasePath:      &dstPath,
		DstJwtToken:      &dstToken,
		DstProjectNumber: &dstProj,
		DstVolume: &gcpserver.VolumeV1beta{
			ResourceId: "dst-vol-name",
			VolumeId:   gcpserver.NewOptString(destVolUuid),
		},
		SourceQuotaRules: []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "src-quota-uuid-1"},
				Name:      "src-quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		},
		DestinationQuotaRules: []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "dst-quota-uuid-1"},
				Name:      "dst-quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		},
	}

	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:                 baseResult.Event,
			DstBasePath:           baseResult.DstBasePath,
			DstJwtToken:           baseResult.DstJwtToken,
			DstProjectNumber:      baseResult.DstProjectNumber,
			DstVolume:             baseResult.DstVolume,
			SourceQuotaRules:      baseResult.SourceQuotaRules,
			DestinationQuotaRules: baseResult.DestinationQuotaRules,
		}

		// Mock the Google Proxy client
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			assert.Equal(tt, *result.DstBasePath, basePath)
			assert.Equal(tt, *result.DstJwtToken, jwt)
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		expectedResponse := &googleproxyclient.UpdateDestinationQuotaRulesResponseV1beta{
			State: googleproxyclient.NewOptString("SUCCESS"),
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					QuotaId:        googleproxyclient.NewOptString("created-quota-uuid-1"),
					ResourceId:     "created-quota-1",
					QuotaType:      googleproxyclient.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
					DiskLimitInMib: 1024,
				},
			},
		}

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.MatchedBy(func(req *googleproxyclient.UpdateDstWithSrcQuotaRulesV1beta) bool {
				return len(req.SrcQuotaRules) == 1 && len(req.DstQuotaRules) == 1
			}),
			mock.MatchedBy(func(params googleproxyclient.V1betaUpdateDestinationQuotaRulesVCPParams) bool {
				return params.ProjectNumber == *result.DstProjectNumber &&
					params.LocationId == result.Event.DestinationLocationID &&
					params.VolumeId == result.DstVolume.VolumeId.Value
			}),
		).Return(expectedResponse, nil)

		updatedResult, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		assert.Len(tt, updatedResult.DestinationQuotaRules, 1)
		assert.Equal(tt, "created-quota-uuid-1", updatedResult.DestinationQuotaRules[0].UUID)
		assert.Equal(tt, "created-quota-1", updatedResult.DestinationQuotaRules[0].Name)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateQuotaRulesRemoteFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:            baseResult.Event,
			DstBasePath:      baseResult.DstBasePath,
			DstJwtToken:      baseResult.DstJwtToken,
			DstProjectNumber: baseResult.DstProjectNumber,
			DstVolume:        baseResult.DstVolume,
			SourceQuotaRules: baseResult.SourceQuotaRules,
		}

		// Mock the Google Proxy client to return error
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.Anything,
			mock.Anything,
		).Return(nil, errors.New("API call failed"))

		_, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSourceQuotaRulesIsEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:                 baseResult.Event,
			DstBasePath:           baseResult.DstBasePath,
			DstJwtToken:           baseResult.DstJwtToken,
			DstProjectNumber:      baseResult.DstProjectNumber,
			DstVolume:             baseResult.DstVolume,
			SourceQuotaRules:      []*datamodel.QuotaRule{},
			DestinationQuotaRules: nil,
		}

		updatedResult, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		assert.Nil(tt, updatedResult.DestinationQuotaRules)
		assert.Equal(tt, result, updatedResult)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenMultipleQuotaRules", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:            baseResult.Event,
			DstBasePath:      baseResult.DstBasePath,
			DstJwtToken:      baseResult.DstJwtToken,
			DstProjectNumber: baseResult.DstProjectNumber,
			DstVolume:        baseResult.DstVolume,
			SourceQuotaRules: []*datamodel.QuotaRule{
				{
					BaseModel: datamodel.BaseModel{UUID: "src-quota-uuid-1"},
					Name:      "src-quota-1",
					QuotaType: "INDIVIDUAL_USER_QUOTA",
				},
				{
					BaseModel: datamodel.BaseModel{UUID: "src-quota-uuid-2"},
					Name:      "src-quota-2",
					QuotaType: "DEFAULT_USER_QUOTA",
				},
			},
			DestinationQuotaRules: []*datamodel.QuotaRule{
				{
					BaseModel: datamodel.BaseModel{UUID: "dst-quota-uuid-1"},
					Name:      "dst-quota-1",
					QuotaType: "INDIVIDUAL_USER_QUOTA",
				},
			},
		}

		// Mock the Google Proxy client
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		expectedResponse := &googleproxyclient.UpdateDestinationQuotaRulesResponseV1beta{
			State: googleproxyclient.NewOptString("SUCCESS"),
			QuotaRules: []googleproxyclient.QuotaRulesV1beta{
				{
					QuotaId:        googleproxyclient.NewOptString("created-quota-uuid-1"),
					ResourceId:     "created-quota-1",
					QuotaType:      googleproxyclient.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
					DiskLimitInMib: 1024,
				},
				{
					QuotaId:        googleproxyclient.NewOptString("created-quota-uuid-2"),
					ResourceId:     "created-quota-2",
					QuotaType:      googleproxyclient.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA,
					DiskLimitInMib: 2048,
				},
			},
		}

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.Anything,
			mock.Anything,
		).Return(expectedResponse, nil)

		updatedResult, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, updatedResult)
		assert.Len(tt, updatedResult.DestinationQuotaRules, 2)
		assert.Equal(tt, "created-quota-uuid-1", updatedResult.DestinationQuotaRules[0].UUID)
		assert.Equal(tt, "created-quota-1", updatedResult.DestinationQuotaRules[0].Name)
		assert.Equal(tt, "INDIVIDUAL_USER_QUOTA", updatedResult.DestinationQuotaRules[0].QuotaType)
		assert.Equal(tt, "created-quota-uuid-2", updatedResult.DestinationQuotaRules[1].UUID)
		assert.Equal(tt, "created-quota-2", updatedResult.DestinationQuotaRules[1].Name)
		assert.Equal(tt, "DEFAULT_USER_QUOTA", updatedResult.DestinationQuotaRules[1].QuotaType)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAPIReturnsBadRequest", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:            baseResult.Event,
			DstBasePath:      baseResult.DstBasePath,
			DstJwtToken:      baseResult.DstJwtToken,
			DstProjectNumber: baseResult.DstProjectNumber,
			DstVolume:        baseResult.DstVolume,
			SourceQuotaRules: baseResult.SourceQuotaRules,
		}

		// Mock the Google Proxy client to return BadRequest
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		badRequestResponse := &googleproxyclient.V1betaUpdateDestinationQuotaRulesVCPBadRequest{
			Code:    400,
			Message: "Bad request",
		}

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.Anything,
			mock.Anything,
		).Return(badRequestResponse, nil)

		_, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAPIReturnsNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:            baseResult.Event,
			DstBasePath:      baseResult.DstBasePath,
			DstJwtToken:      baseResult.DstJwtToken,
			DstProjectNumber: baseResult.DstProjectNumber,
			DstVolume:        baseResult.DstVolume,
			SourceQuotaRules: baseResult.SourceQuotaRules,
		}

		// Mock the Google Proxy client to return NotFound
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		notFoundResponse := &googleproxyclient.V1betaUpdateDestinationQuotaRulesVCPNotFound{
			Code:    404,
			Message: "Not found",
		}

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.Anything,
			mock.Anything,
		).Return(notFoundResponse, nil)

		_, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAPIReturnsInternalServerError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:            baseResult.Event,
			DstBasePath:      baseResult.DstBasePath,
			DstJwtToken:      baseResult.DstJwtToken,
			DstProjectNumber: baseResult.DstProjectNumber,
			DstVolume:        baseResult.DstVolume,
			SourceQuotaRules: baseResult.SourceQuotaRules,
		}

		// Mock the Google Proxy client to return InternalServerError
		mockClient := googleproxyclient.NewMockInvoker(tt)
		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		defer func() {
			googleproxyclient.GetGProxyClient = originalGetGProxyClient
		}()

		internalServerErrorResponse := &googleproxyclient.V1betaUpdateDestinationQuotaRulesVCPInternalServerError{
			Code:    500,
			Message: "Internal server error",
		}

		mockClient.EXPECT().V1betaUpdateDestinationQuotaRulesVCP(
			ctx,
			mock.Anything,
			mock.Anything,
		).Return(internalServerErrorResponse, nil)

		_, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSourceQuotaRulesIsEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		result := &replication.CreateReplicationResult{
			Event:                 baseResult.Event,
			DstBasePath:           baseResult.DstBasePath,
			DstJwtToken:           baseResult.DstJwtToken,
			DstProjectNumber:      baseResult.DstProjectNumber,
			DstVolume:             baseResult.DstVolume,
			SourceQuotaRules:      []*datamodel.QuotaRule{}, // Empty source quota rules
			DestinationQuotaRules: nil,
		}

		updatedResult, err := activity.CreateQuotaRulesOnDestination(ctx, result)

		assert.NoError(tt, err)
		assert.Nil(tt, updatedResult.DestinationQuotaRules)
		assert.Equal(tt, result, updatedResult)
		mockStorage.AssertExpectations(tt)
	})
}

func TestHydrateQuotaRules(t *testing.T) {
	volumeResourceId := "vol-resource-id"
	location := "us-east1"
	projectNumber := "proj-123"

	quotaRules := []*datamodel.QuotaRule{
		{
			BaseModel: datamodel.BaseModel{UUID: "quota-uuid-1"},
			Name:      "quota-1",
			QuotaType: "INDIVIDUAL_USER_QUOTA",
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "quota-uuid-2"},
			Name:      "quota-2",
			QuotaType: "DEFAULT_USER_QUOTA",
		},
	}

	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// Mock hydrateQuotaRuleCreate directly (since it's assigned at package init)
		callCount := 0
		originalHydrateQuotaRuleCreate := hydrateQuotaRuleCreate
		hydrateQuotaRuleCreate = func(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
			callCount++
			if callCount == 1 {
				assert.Equal(tt, "quota-1", quotaRule.ResourceId)
				assert.Equal(tt, "quota-uuid-1", quotaRule.QuotaRuleId)
			} else if callCount == 2 {
				assert.Equal(tt, "quota-2", quotaRule.ResourceId)
				assert.Equal(tt, "quota-uuid-2", quotaRule.QuotaRuleId)
			}
			assert.Equal(tt, volumeResourceId, volumeResourceID)
			assert.Equal(tt, location, location)
			assert.Equal(tt, projectNumber, projectId)
			assert.Equal(tt, "test-token", token)
			return nil
		}
		defer func() {
			hydrateQuotaRuleCreate = originalHydrateQuotaRuleCreate
		}()

		err := activity.HydrateQuotaRules(ctx, quotaRules, volumeResourceId, location, projectNumber)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGenerateCallbackTokenFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Mock auth.GenerateCallbackToken to return error
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", errors.New("failed to generate token")
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		err := activity.HydrateQuotaRules(ctx, quotaRules, volumeResourceId, location, projectNumber)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleMissingUUID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		quotaRulesWithoutUUID := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: ""},
				Name:      "quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		err := activity.HydrateQuotaRules(ctx, quotaRulesWithoutUUID, volumeResourceId, location, projectNumber)

		assert.Error(tt, err)
		// The error is wrapped as TemporalApplicationError with CustomError, so check the underlying error
		// Try to get the CustomError and check its OriginalErr
		if appErr, ok := err.(*temporal.ApplicationError); ok {
			// If it's a Temporal application error, get the cause
			cause := appErr.Unwrap()
			if customErr, ok := cause.(*vsaerrors.CustomError); ok {
				assert.Contains(tt, customErr.OriginalErr.Error(), "quota rule missing UUID")
			} else if cause != nil {
				// Fallback: check the cause error message
				assert.Contains(tt, cause.Error(), "quota rule missing UUID")
			}
		} else if customErr, ok := err.(*vsaerrors.CustomError); ok {
			// Fallback: check if it's a CustomError directly
			assert.Contains(tt, customErr.OriginalErr.Error(), "quota rule missing UUID")
		} else {
			// Last resort: check the error message (should contain some indication of the error)
			assert.Contains(tt, err.Error(), "hydrate")
		}
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleCreateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// Mock hydrateQuotaRuleCreate directly (since it's assigned at package init) to return error
		originalHydrateQuotaRuleCreate := hydrateQuotaRuleCreate
		hydrateQuotaRuleCreate = func(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
			return errors.New("hydration failed")
		}
		defer func() {
			hydrateQuotaRuleCreate = originalHydrateQuotaRuleCreate
		}()

		err := activity.HydrateQuotaRules(ctx, quotaRules, volumeResourceId, location, projectNumber)

		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRulesIsEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		emptyQuotaRules := []*datamodel.QuotaRule{}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// When quota rules is empty, HydrateQuotaRuleCreate should not be called
		err := activity.HydrateQuotaRules(ctx, emptyQuotaRules, volumeResourceId, location, projectNumber)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRulesIsNil", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// When quota rules is nil, HydrateQuotaRuleCreate should not be called
		err := activity.HydrateQuotaRules(ctx, nil, volumeResourceId, location, projectNumber)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSingleQuotaRule", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		singleQuotaRule := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-1"},
				Name:      "quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// Mock hydrateQuotaRuleCreate directly (since it's assigned at package init)
		callCount := 0
		originalHydrateQuotaRuleCreate := hydrateQuotaRuleCreate
		hydrateQuotaRuleCreate = func(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
			callCount++
			assert.Equal(tt, "quota-1", quotaRule.ResourceId)
			assert.Equal(tt, "quota-uuid-1", quotaRule.QuotaRuleId)
			return nil
		}
		defer func() {
			hydrateQuotaRuleCreate = originalHydrateQuotaRuleCreate
		}()

		err := activity.HydrateQuotaRules(ctx, singleQuotaRule, volumeResourceId, location, projectNumber)

		assert.NoError(tt, err)
		assert.Equal(tt, 1, callCount, "HydrateQuotaRuleCreate should be called once")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenMultipleQuotaRules", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		multipleQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-1"},
				Name:      "quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-2"},
				Name:      "quota-2",
				QuotaType: "DEFAULT_USER_QUOTA",
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-3"},
				Name:      "quota-3",
				QuotaType: "INDIVIDUAL_GROUP_QUOTA",
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// Mock hydrateQuotaRuleCreate directly (since it's assigned at package init)
		callCount := 0
		originalHydrateQuotaRuleCreate := hydrateQuotaRuleCreate
		hydrateQuotaRuleCreate = func(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
			callCount++
			assert.Contains(tt, []string{"quota-1", "quota-2", "quota-3"}, quotaRule.ResourceId)
			assert.Contains(tt, []string{"quota-uuid-1", "quota-uuid-2", "quota-uuid-3"}, quotaRule.QuotaRuleId)
			return nil
		}
		defer func() {
			hydrateQuotaRuleCreate = originalHydrateQuotaRuleCreate
		}()

		err := activity.HydrateQuotaRules(ctx, multipleQuotaRules, volumeResourceId, location, projectNumber)

		assert.NoError(tt, err)
		assert.Equal(tt, 3, callCount, "HydrateQuotaRuleCreate should be called 3 times")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSecondQuotaRuleFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Ensure hydration is enabled for this test
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() {
			hydrationEnabled = originalHydrationEnabled
		}()

		multipleQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-1"},
				Name:      "quota-1",
				QuotaType: "INDIVIDUAL_USER_QUOTA",
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "quota-uuid-2"},
				Name:      "quota-2",
				QuotaType: "DEFAULT_USER_QUOTA",
			},
		}

		// Mock auth.GenerateCallbackToken
		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() {
			auth.GenerateCallbackToken = originalGenerateCallbackToken
		}()

		// Mock hydrateQuotaRuleCreate directly (since it's assigned at package init) to fail on second call
		callCount := 0
		originalHydrateQuotaRuleCreate := hydrateQuotaRuleCreate
		hydrateQuotaRuleCreate = func(ctx context.Context, logger log.Logger, quotaRule models.QuotaRuleHydrateObject, volumeResourceID string, location string, projectId string, token string) error {
			callCount++
			if callCount == 2 {
				return errors.New("hydration failed for second rule")
			}
			return nil
		}
		defer func() {
			hydrateQuotaRuleCreate = originalHydrateQuotaRuleCreate
		}()

		err := activity.HydrateQuotaRules(ctx, multipleQuotaRules, volumeResourceId, location, projectNumber)

		assert.Error(tt, err)
		assert.Equal(tt, 2, callCount, "HydrateQuotaRuleCreate should be called twice before failing")
		mockStorage.AssertExpectations(tt)
	})
}

func TestListQuotaRulesLocal(t *testing.T) {
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		sourceVolumeID := int64(123)
		expectedQuotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-uuid-1",
					ID:   1,
				},
				Name:           "quota-rule-1",
				QuotaType:      "INDIVIDUAL_USER_QUOTA",
				QuotaTarget:    "user:alice",
				DiskLimitInKib: 100 * 1024,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-uuid-2",
					ID:   2,
				},
				Name:           "quota-rule-2",
				QuotaType:      "DEFAULT_USER_QUOTA",
				DiskLimitInKib: 200 * 1024,
			},
		}

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{ID: sourceVolumeID},
				},
			},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", ctx, sourceVolumeID).Return(expectedQuotaRules, nil)

		quotaRules, err := activity.ListQuotaRulesLocal(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRules)
		assert.Len(tt, quotaRules, 2)
		assert.Equal(tt, expectedQuotaRules, quotaRules)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		sourceVolumeID := int64(123)
		expectedError := errors.New("database error")

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{ID: sourceVolumeID},
				},
			},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", ctx, sourceVolumeID).Return(nil, expectedError)

		quotaRules, err := activity.ListQuotaRulesLocal(ctx, result)

		assert.Error(tt, err)
		assert.Nil(tt, quotaRules)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNoQuotaRulesFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := VolumeReplicationCreateActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		sourceVolumeID := int64(123)
		emptyQuotaRules := []*datamodel.QuotaRule{}

		result := &replication.CreateReplicationResult{
			Event: &replication.CreateReplicationEvent{
				SourceVolume: datamodel.Volume{
					BaseModel: datamodel.BaseModel{ID: sourceVolumeID},
				},
			},
		}

		mockStorage.On("GetQuotaRulesByVolumeID", ctx, sourceVolumeID).Return(emptyQuotaRules, nil)

		quotaRules, err := activity.ListQuotaRulesLocal(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, quotaRules)
		assert.Len(tt, quotaRules, 0)
		mockStorage.AssertExpectations(tt)
	})
}

func TestDetermineReplicationType(t *testing.T) {
	tests := []struct {
		name                string
		sourceLocation      string
		destinationLocation string
		expected            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationType
	}{
		{
			name:                "Different regions - us-east4-a to us-central1-a",
			sourceLocation:      "us-east4-a",
			destinationLocation: "us-central1-a",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION,
		},
		{
			name:                "Different regions - us-east4 to australia-southeast1",
			sourceLocation:      "us-east4",
			destinationLocation: "australia-southeast1",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION,
		},
		{
			name:                "Different regions - australia-southeast1-a to us-central1",
			sourceLocation:      "australia-southeast1-a",
			destinationLocation: "us-central1",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeCROSSREGIONREPLICATION,
		},
		{
			name:                "Same region, different zones - us-east4-a to us-east4-b",
			sourceLocation:      "us-east4-a",
			destinationLocation: "us-east4-b",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION,
		},
		{
			name:                "Same region, one with zone - us-central1-a to us-central1",
			sourceLocation:      "us-central1-a",
			destinationLocation: "us-central1",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION,
		},
		{
			name:                "Same region, one with zone - us-central1 to us-central1-b",
			sourceLocation:      "us-central1",
			destinationLocation: "us-central1-b",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION,
		},
		{
			name:                "Same region and zone - us-east4-a to us-east4-a",
			sourceLocation:      "us-east4-a",
			destinationLocation: "us-east4-a",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTRAZONEREPLICATION,
		},
		{
			name:                "Same region, no zones - us-east4 to us-east4",
			sourceLocation:      "us-east4",
			destinationLocation: "us-east4",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTRAZONEREPLICATION,
		},
		{
			name:                "Same region, different zones - australia-southeast1-a to australia-southeast1-c",
			sourceLocation:      "australia-southeast1-a",
			destinationLocation: "australia-southeast1-c",
			expected:            googleproxyclient.VolumeReplicationCreateInternalV1betaReplicationTypeINTERZONEREPLICATION,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := determineReplicationType(tt.sourceLocation, tt.destinationLocation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

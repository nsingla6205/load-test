package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

func rbacRefreshRequest() *vcmserver.OntapModeRbacV1 {
	return &vcmserver.OntapModeRbacV1{
		RbacFileUrl:      vcmserver.NewOptString("gs://rbac-bucket/refreshed.txt"),
		RbacFileCheckSum: vcmserver.NewOptString("refreshed-checksum"),
	}
}

func rbacRefreshParams() vcmserver.ClusterRefreshRbacV1Params {
	return vcmserver.ClusterRefreshRbacV1Params{
		RequestId: testOperationRequestID,
		Timeout:   testOperationTimeout,
	}
}

func TestClusterRefreshRbacV1_ForwardsRequestPairAndStoredConfig(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)

	var got *commonparams.RefreshRbacForPoolParams
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(*commonparams.RefreshRbacForPoolParams)
		}).
		Return(testOperationRequestID, nil)

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/"+testOperationRequestID, opRes.Name)
	assert.False(t, opRes.Done)

	require.NotNil(t, got)
	require.NotNil(t, got.VCM)
	assert.Equal(t, testDeploymentID, got.PoolID)
	assert.Equal(t, testDeploymentID, got.VCM.DeploymentID)
	assert.Equal(t, testOperationRequestID, got.VCM.RequestID)
	assert.False(t, got.VCM.Deadline.IsZero())
	// The request pair overrides the pair stored with the required shape.
	assert.Equal(t, "gs://rbac-bucket/refreshed.txt", got.VCM.OntapModeRbacFileURL)
	assert.Equal(t, "refreshed-checksum", got.VCM.OntapModeRbacChecksum)
	assert.NotEmpty(t, got.VCM.OntapCredentials, "credentials come from the stored config")
	mockOrch.AssertExpectations(t)
}

func TestClusterRefreshRbacV1_ForwardsEmptyPairUnvalidated(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)

	var got *commonparams.RefreshRbacForPoolParams
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			got = args.Get(1).(*commonparams.RefreshRbacForPoolParams)
		}).
		Return(testOperationRequestID, nil)

	res, err := handler.ClusterRefreshRbacV1(context.Background(), &vcmserver.OntapModeRbacV1{}, rbacRefreshParams())

	require.NoError(t, err)
	_, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "VCM must not validate the RBAC pair; got %T", res)
	require.NotNil(t, got)
	assert.Empty(t, got.VCM.OntapModeRbacFileURL)
	assert.Empty(t, got.VCM.OntapModeRbacChecksum)
	mockOrch.AssertExpectations(t)
}

func TestClusterRefreshRbacV1_ReturnsOrchestratorOperation(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).
		Return("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil)

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", opRes.Name)
	mockOrch.AssertExpectations(t)
}

func TestClusterRefreshRbacV1_EmptyOperationID_FallsBackToRequestID(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).Return("", nil)

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/"+testOperationRequestID, opRes.Name)
}

func TestClusterRefreshRbacV1_InvalidRequest_ReturnsBadRequest(t *testing.T) {
	tests := []struct {
		name   string
		req    *vcmserver.OntapModeRbacV1
		params vcmserver.ClusterRefreshRbacV1Params
	}{
		{name: "nil body", req: nil, params: rbacRefreshParams()},
		{
			name:   "missing request id",
			req:    rbacRefreshRequest(),
			params: vcmserver.ClusterRefreshRbacV1Params{RequestId: " ", Timeout: testOperationTimeout},
		},
		{
			name:   "invalid timeout",
			req:    rbacRefreshRequest(),
			params: vcmserver.ClusterRefreshRbacV1Params{RequestId: testOperationRequestID, Timeout: "soon"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, mockOrch := configuredClusterHandler(t)

			res, err := handler.ClusterRefreshRbacV1(context.Background(), tc.req, tc.params)

			require.NoError(t, err)
			badRequest, ok := res.(*vcmserver.ClusterRefreshRbacV1BadRequest)
			require.True(t, ok, "expected a BadRequest response, got %T", res)
			assert.EqualValues(t, 400, badRequest.Code)
			assert.False(t, badRequest.Retriable.Or(true))
			mockOrch.AssertNotCalled(t, "UpdateRbacForPoolById", mock.Anything, mock.Anything)
		})
	}
}

func TestClusterRefreshRbacV1_UnseededConfig_ReturnsRetriableBadRequest(t *testing.T) {
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-cluster-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{Project: "p", Instance: "i", Database: "d"},
	})
	waitForConfigured(t, store)
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{ConfigStore: store, Orchestrator: mockOrch}

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	badRequest, ok := res.(*vcmserver.ClusterRefreshRbacV1BadRequest)
	require.True(t, ok, "expected a BadRequest response, got %T", res)
	assert.True(t, badRequest.Retriable.Or(false), "CLH can re-seed config and retry")
	assert.Contains(t, badRequest.Message, "deploymentId is not configured")
	mockOrch.AssertNotCalled(t, "UpdateRbacForPoolById", mock.Anything, mock.Anything)
}

func TestClusterRefreshRbacV1_OrchestratorError_Returns500(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).Return("", errors.New("dispatch failed"))

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	errRes, ok := res.(*vcmserver.ClusterRefreshRbacV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
	assert.True(t, errRes.Retriable.Or(false))
	mockOrch.AssertExpectations(t)
}

func TestClusterRefreshRbacV1_RefreshInProgress_Returns500WithConflictingOperation(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	conflict := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
		errors.New("a RBAC refresh is already in progress"))
	mockOrch.On("UpdateRbacForPoolById", mock.Anything, mock.Anything).Return("running-op-id", conflict)

	res, err := handler.ClusterRefreshRbacV1(context.Background(), rbacRefreshRequest(), rbacRefreshParams())

	require.NoError(t, err)
	errRes, ok := res.(*vcmserver.ClusterRefreshRbacV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
	assert.True(t, errRes.Retriable.Or(false))
	assert.Contains(t, errRes.Message, "already in progress")
	assert.Contains(t, errRes.Message, "running-op-id", "the conflicting operation id must be surfaced to CLH")
	mockOrch.AssertExpectations(t)
}

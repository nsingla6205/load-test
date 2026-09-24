package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	common "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

const testDeploymentID = "gcnv-8d0ff6a0b456f30"

type stubDBOpener struct {
	dsn string
}

func (s *stubDBOpener) Open(ctx context.Context, _ vcmserver.StateDbConfigV1) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: s.dsn},
	}, log.NewLogger(), true)
}

func waitForConfigured(t *testing.T, store *vcmconfig.Store) {
	t.Helper()
	require.Eventually(t, store.Configured, 2*time.Second, 10*time.Millisecond)
}

// seededSetConfig is the configuration CLH pushes through POST /v1/config. The
// create-cluster request carries none of these fields, so the handler can only
// dispatch when they are present in memory.
func seededSetConfig() *vcmserver.SetConfigRequestV1 {
	seed := vcmserver.SeedConfigV1{
		DeploymentId:            vcmserver.NewOptString(testDeploymentID),
		Zone1:                   vcmserver.NewOptString("us-central1-a"),
		Zone2:                   vcmserver.NewOptString("us-central1-c"),
		MediatorZone:            vcmserver.NewOptString("us-central1-b"),
		ConsumerProjectNum:      vcmserver.NewOptInt64(1029876893740),
		ServiceTenantProjectNum: vcmserver.NewOptInt64(807186998501),
		SnHostProjectNum:        vcmserver.NewOptInt64(303030303030),
		ServiceAccountEmail:     vcmserver.NewOptString("sa@example.iam.gserviceaccount.com"),
		CaUri:                   vcmserver.NewOptString("ca-uri"),
		NetConfig: vcmserver.NewOptNetConfigV1(vcmserver.NetConfigV1{
			Nodemgmt: vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "mgmt-vpc", Subnet: "mgmt-subnet"}),
			Data:     vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "data-vpc", Subnet: "data-subnet"}),
		}),
		OntapCredentials: []vcmserver.OntapCredentialV1{
			{
				Role:           vcmserver.OntapCredentialV1RoleADMIN,
				ActiveAuthType: vcmserver.NewOptOntapCredentialV1ActiveAuthType(vcmserver.OntapCredentialV1ActiveAuthTypePWD),
				PasswordAuthCreds: vcmserver.NewOptPasswordAuthCredsV1(vcmserver.PasswordAuthCredsV1{
					Username:         vcmserver.NewOptString("root"),
					PasswordSecretId: vcmserver.NewOptString("admin-secret"),
				}),
			},
		},
	}

	req := &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{
			Project:  "test-project",
			Instance: "test-instance",
			Database: "test-db",
		},
	}
	req.ClusterConfig.SetTo(vcmserver.ClusterConfigV1{
		SeedConfig: vcmserver.NewOptSeedConfigV1(seed),
	})
	return req
}

func configuredClusterHandler(t *testing.T) (*Handler, *vcmconfig.Store, *factory.MockOrchestratorFactory) {
	t.Helper()
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-cluster-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), seededSetConfig())
	waitForConfigured(t, store)
	mockOrch := factory.NewMockOrchestratorFactory(t)
	return &Handler{ConfigStore: store, Orchestrator: mockOrch}, store, mockOrch
}

// clusterBody is the create-cluster request body, which carries only the
// CLH-computed shape; requestId and timeout arrive as query parameters.
func clusterBody() *vcmserver.ClusterV1 {
	return &vcmserver.ClusterV1{
		ClusterShape: vcmserver.ClusterShapeV1{
			ProvisionedThroughputMibps: vcmserver.NewOptInt64(1024),
			ProvisionedIops:            vcmserver.NewOptInt64(5000),
			ProvisionedSizeInBytes:     vcmserver.NewOptInt64(1099511627776),
			QosType:                    vcmserver.NewOptString("auto"),
			Mode:                       vcmserver.NewOptClusterShapeV1Mode(vcmserver.ClusterShapeV1ModeONTAP),
			RegionIdentifier:           vcmserver.NewOptString("34"),
			AutoTieringConfig: vcmserver.NewOptAutoTieringConfigV1(vcmserver.AutoTieringConfigV1{
				AutoTierBucketName: vcmserver.NewOptString("bucket-1"),
			}),
		},
	}
}

func clusterParams(requestID string, timeout time.Duration) vcmserver.ClusterCreateV1Params {
	return vcmserver.ClusterCreateV1Params{RequestId: requestID, Timeout: timeout}
}

func fullClusterParams() vcmserver.ClusterCreateV1Params {
	return clusterParams("3fa85f64-5717-4562-b3fc-2c963f66afa6", time.Hour)
}

func TestClusterCreateV1_MergesRequestShapeWithStoredConfig(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	params := fullClusterParams()

	var dispatched *common.CreatePoolParams
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			dispatched = args.Get(1).(*common.CreatePoolParams)
		}).
		Return(nil, params.RequestId, nil)

	_, err := handler.ClusterCreateV1(context.Background(), clusterBody(), params)
	require.NoError(t, err)
	require.NotNil(t, dispatched)
	require.NotNil(t, dispatched.VCM)
	vcm := dispatched.VCM

	// Request-level controls, which arrive as query parameters. The relative
	// timeout is resolved to an absolute UTC deadline at the API edge.
	assert.Equal(t, params.RequestId, vcm.RequestID)
	assert.WithinDuration(t, time.Now().UTC().Add(params.Timeout), vcm.Deadline, time.Minute)

	// Sourced from the request's cluster shape.
	assert.Equal(t, "ONTAP", vcm.Mode)
	assert.Equal(t, "auto", vcm.QosType)
	assert.Equal(t, int64(1024), vcm.ThroughputMibps)
	assert.Equal(t, int64(5000), vcm.Iops)
	assert.Equal(t, int64(1099511627776), vcm.SizeInBytes)
	assert.Equal(t, "34", vcm.RegionIdentifier)
	assert.Equal(t, "bucket-1", vcm.AutoTierBucketName)

	// Sourced from the in-memory setConfig seed, not from the request.
	assert.Equal(t, testDeploymentID, vcm.DeploymentID)
	assert.Equal(t, "us-central1-a", vcm.PrimaryZone)
	assert.Equal(t, "us-central1-c", vcm.SecondaryZone)
	assert.Equal(t, "us-central1-b", vcm.MediatorZone)
	assert.Equal(t, "1029876893740", vcm.ProjectID)
	assert.Equal(t, "807186998501", vcm.ServiceTenantProject)
	assert.Equal(t, "303030303030", vcm.SNHostProject)
	assert.Equal(t, "sa@example.iam.gserviceaccount.com", vcm.ServiceAccountEmail)
	assert.Equal(t, "ca-uri", vcm.CaURI)
	assert.Equal(t, "mgmt-vpc", vcm.NodeMgmt.VPC)
	assert.Equal(t, "mgmt-subnet", vcm.NodeMgmt.Subnet)
	require.NotNil(t, vcm.Data)
	assert.Equal(t, "data-vpc", vcm.Data.VPC)
	require.Len(t, vcm.OntapCredentials, 1)
	assert.Equal(t, "admin", vcm.OntapCredentials[0].Type)
	assert.Equal(t, "admin-secret", vcm.OntapCredentials[0].SecretID)
	assert.Empty(t, vcm.OntapCredentials[0].CertificateID, "ADMIN resolves via password, not certificate")

	mockOrch.AssertExpectations(t)
}

func TestClusterCreateV1ForwardsToOrchestrator(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	requestID := uuid.NewString()

	// The orchestrator now owns durable job creation; the endpoint only
	// validates and forwards, returning the operation ID it gets back.
	mockOrch.On("CreatePool", mock.Anything, mock.MatchedBy(func(p *common.CreatePoolParams) bool {
		return p != nil && p.VCM != nil &&
			p.VCM.RequestID == requestID &&
			p.VCM.DeploymentID == testDeploymentID
	})).Return(nil, requestID, nil)

	result, err := handler.ClusterCreateV1(context.Background(),
		clusterBody(), clusterParams(requestID, 2*time.Hour))

	require.NoError(t, err)
	opRes, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok, "expected OperationV1, got %T", result)
	assert.Equal(t, "/v1/operations/"+requestID, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestClusterCreateV1_EmptyOperationID_FallsBackToRequestID(t *testing.T) {
	// When the orchestrator returns no operation id (e.g. it reattached to an
	// in-flight run), the endpoint must fall back to the caller-supplied
	// requestId so the returned operation name is still pollable.
	handler, _, mockOrch := configuredClusterHandler(t)
	params := fullClusterParams()
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", nil)

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), params)

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an OperationV1, got %T", res)
	assert.Equal(t, "/v1/operations/"+params.RequestId, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestClusterCreateV1RequiresRequestFields(t *testing.T) {
	handler, _, _ := configuredClusterHandler(t)

	for _, tc := range []struct {
		name   string
		body   *vcmserver.ClusterV1
		params vcmserver.ClusterCreateV1Params
	}{
		{name: "nil body", body: nil, params: fullClusterParams()},
		{name: "empty requestId", body: clusterBody(), params: clusterParams("", time.Hour)},
		{name: "zero timeout", body: clusterBody(), params: clusterParams(uuid.NewString(), 0)},
		{name: "negative timeout", body: clusterBody(), params: clusterParams(uuid.NewString(), -time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.ClusterCreateV1(context.Background(), tc.body, tc.params)
			require.NoError(t, err)

			badRequest, ok := result.(*vcmserver.ClusterCreateV1BadRequest)
			require.True(t, ok)
			assert.EqualValues(t, 400, badRequest.Code)
			assert.False(t, badRequest.Retriable.Or(true))
		})
	}
}

func TestClusterCreateV1_UnseededConfig_ReturnsRetriableBadRequest(t *testing.T) {
	// Only stateDbConfig has been pushed, so the seed the handler needs is
	// absent; the request must be rejected before reaching the orchestrator.
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-cluster-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{Project: "p", Instance: "i", Database: "d"},
	})
	waitForConfigured(t, store)
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{ConfigStore: store, Orchestrator: mockOrch}

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), fullClusterParams())

	require.NoError(t, err)
	badRequest, ok := res.(*vcmserver.ClusterCreateV1BadRequest)
	require.True(t, ok, "expected a BadRequest response, got %T", res)
	assert.EqualValues(t, 400, badRequest.Code)
	assert.True(t, badRequest.Retriable.Or(false), "CLH can re-seed config and retry")
	assert.Contains(t, badRequest.Message, "deploymentId is not configured")
	mockOrch.AssertNotCalled(t, "CreatePool", mock.Anything, mock.Anything)
}

func TestClusterCreateV1_NilConfigStore_Returns500(t *testing.T) {
	handler := Handler{}

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), fullClusterParams())

	require.NoError(t, err)
	errRes, ok := res.(*vcmserver.ClusterCreateV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
}

func TestClusterCreateV1_Success_ReturnsOperation(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "op-123", nil)

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), fullClusterParams())

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/op-123", opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestClusterCreateV1_OrchestratorError_Returns500(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", errors.New("dispatch failed"))

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), fullClusterParams())

	require.NoError(t, err, "handler surfaces the failure through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.ClusterCreateV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
	// The endpoint deliberately returns a generic message so internal error
	// details (e.g. "dispatch failed") are not leaked to the caller.
	assert.Contains(t, errRes.Message, "Internal error occurred")
	assert.True(t, errRes.Retriable.Or(false))
	mockOrch.AssertExpectations(t)
}

func TestClusterCreateV1_DuplicateRequest_ReturnsNonRetriableClientError(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	// The orchestrator rejects a duplicate requestId with a resource-state
	// conflict; the endpoint must surface it as a clear, non-retriable client
	// error rather than a retriable 500 the caller would keep polling.
	conflict := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
		errors.New(`create-pool request "3fa85f64-5717-4562-b3fc-2c963f66afa6" already exists in state PROCESSING`))
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", conflict)

	res, err := handler.ClusterCreateV1(context.Background(), clusterBody(), fullClusterParams())

	require.NoError(t, err, "handler surfaces the conflict through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.ClusterCreateV1Conflict)
	require.True(t, ok, "expected a Conflict response, got %T", res)
	assert.EqualValues(t, 409, errRes.Code)
	assert.False(t, errRes.Retriable.Or(true), "a duplicate request must be non-retriable")
	assert.Contains(t, errRes.Message, "already exists in state PROCESSING")
	mockOrch.AssertExpectations(t)
}

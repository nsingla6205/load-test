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

type stubDBOpener struct {
	dsn string
}

func (s *stubDBOpener) Open(ctx context.Context, _ vcmserver.DbConfigV1) (vcmdb.Storage, error) {
	return vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSQLite,
		SQLite: vcmdb.SQLConfig{DSN: s.dsn},
	}, log.NewLogger(), true)
}

func waitForConfigured(t *testing.T, store *vcmconfig.Store) {
	t.Helper()
	require.Eventually(t, store.Configured, 2*time.Second, 10*time.Millisecond)
}

func configuredPoolHandler(t *testing.T) (*Handler, *vcmconfig.Store, *factory.MockOrchestratorFactory) {
	t.Helper()
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-pool-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		DbConfig: vcmserver.DbConfigV1{
			Project:  "test-project",
			Instance: "test-instance",
			Database: "test-db",
		},
	})
	waitForConfigured(t, store)
	mockOrch := factory.NewMockOrchestratorFactory(t)
	return &Handler{ConfigStore: store, Orchestrator: mockOrch}, store, mockOrch
}

func testPoolCreateRequest(requestID, deploymentID string, deadline time.Time) *vcmserver.PoolRequestV1 {
	return &vcmserver.PoolRequestV1{
		RequestId: requestID,
		Deadline:  deadline,
		Pool: vcmserver.PoolV1{
			DeploymentId: deploymentID,
		},
	}
}

func fullPoolRequest() *vcmserver.PoolRequestV1 {
	return &vcmserver.PoolRequestV1{
		RequestId: "3fa85f64-5717-4562-b3fc-2c963f66afa6",
		Deadline:  time.Date(2099, 7, 29, 13, 30, 0, 0, time.UTC),
		Pool: vcmserver.PoolV1{
			DeploymentId:         "gcnv-abc",
			RegionIdentifier:     "34",
			PrimaryZone:          "us-central1-a",
			SecondaryZone:        "us-central1-c",
			MediatorZone:         "us-central1-b",
			ServiceTenantProject: "807186998501",
			SNHostProject:        "host-proj",
			ServiceAccountEmail:  "sa@example.iam.gserviceaccount.com",
			AutoTierBucketName:   "bucket-1",
			ProjectId:            vcmserver.NewOptString("1029876893740"),
			Mode:                 vcmserver.NewOptPoolV1Mode(vcmserver.PoolV1ModeONTAP),
			QosType:              vcmserver.NewOptNilString("auto"),
			ThroughputMibps:      vcmserver.NewNilInt64(1024),
			Iops:                 vcmserver.NewNilInt64(5000),
			SizeInBytes:          vcmserver.NewNilFloat64(1099511627776),
			CaUri:                vcmserver.NewOptString("ca-uri"),
			NetConfig: vcmserver.NetConfigV1{
				Nodemgmt: vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "mgmt-vpc", Subnet: "mgmt-subnet"}),
				Data:     vcmserver.NewOptNetInterfaceConfigV1(vcmserver.NetInterfaceConfigV1{Vpc: "data-vpc", Subnet: "data-subnet"}),
			},
			OntapCredentials: []vcmserver.OntapCredentialsV1{
				{Type: vcmserver.OntapCredentialsV1TypeAdmin, SecretId: vcmserver.NewOptString("admin-secret")},
				{Type: vcmserver.OntapCredentialsV1TypeDefault, CertificateId: vcmserver.NewOptString("cert-1")},
			},
		},
	}
}

func TestToCreatePoolParams_MapsEnvelopeAndPool(t *testing.T) {
	req := fullPoolRequest()

	params := toCreatePoolParams(req)

	require.NotNil(t, params)
	require.NotNil(t, params.VCM)
	vcm := params.VCM

	// Envelope-level fields.
	assert.Equal(t, "3fa85f64-5717-4562-b3fc-2c963f66afa6", vcm.RequestID)
	assert.Equal(t, time.Date(2099, 7, 29, 13, 30, 0, 0, time.UTC), vcm.Deadline)

	// Pool scalar fields.
	assert.Equal(t, "gcnv-abc", vcm.DeploymentID)
	assert.Equal(t, "34", vcm.RegionIdentifier)
	assert.Equal(t, "us-central1-a", vcm.PrimaryZone)
	assert.Equal(t, "us-central1-c", vcm.SecondaryZone)
	assert.Equal(t, "us-central1-b", vcm.MediatorZone)
	assert.Equal(t, "807186998501", vcm.ServiceTenantProject)
	assert.Equal(t, "host-proj", vcm.SNHostProject)
	assert.Equal(t, "sa@example.iam.gserviceaccount.com", vcm.ServiceAccountEmail)
	assert.Equal(t, "bucket-1", vcm.AutoTierBucketName)
	assert.Equal(t, "1029876893740", vcm.ProjectID)
	assert.Equal(t, "ONTAP", vcm.Mode)
	assert.Equal(t, "auto", vcm.QosType)
	assert.Equal(t, int64(1024), vcm.ThroughputMibps)
	assert.Equal(t, int64(5000), vcm.Iops)
	assert.Equal(t, int64(1099511627776), vcm.SizeInBytes)
	assert.Equal(t, "ca-uri", vcm.CaURI)

	// Net config.
	assert.Equal(t, "mgmt-vpc", vcm.NodeMgmt.VPC)
	assert.Equal(t, "mgmt-subnet", vcm.NodeMgmt.Subnet)
	require.NotNil(t, vcm.Data)
	assert.Equal(t, "data-vpc", vcm.Data.VPC)
	assert.Equal(t, "data-subnet", vcm.Data.Subnet)

	// ONTAP credentials.
	require.Len(t, vcm.OntapCredentials, 2)
	assert.Equal(t, "admin", vcm.OntapCredentials[0].Type)
	assert.Equal(t, "admin-secret", vcm.OntapCredentials[0].SecretID)
	assert.Equal(t, "default", vcm.OntapCredentials[1].Type)
	assert.Equal(t, "cert-1", vcm.OntapCredentials[1].CertificateID)
}

func TestToCreatePoolParams_DefaultsForUnsetOptionals(t *testing.T) {
	req := &vcmserver.PoolRequestV1{
		RequestId: "r",
		Pool:      vcmserver.PoolV1{DeploymentId: "d"},
	}

	vcm := toCreatePoolParams(req).VCM

	require.NotNil(t, vcm)
	assert.Equal(t, "d", vcm.DeploymentID)
	assert.Equal(t, "DEFAULT", vcm.Mode, "unset mode must default to DEFAULT")
	assert.Equal(t, "", vcm.QosType)
	assert.Equal(t, int64(0), vcm.ThroughputMibps)
	assert.Equal(t, int64(0), vcm.SizeInBytes)
	assert.Nil(t, vcm.Data, "absent data net config must stay nil")
	assert.Empty(t, vcm.OntapCredentials)
}

func TestPoolCreateV1ForwardsToOrchestrator(t *testing.T) {
	handler, _, mockOrch := configuredPoolHandler(t)
	ctx := context.Background()
	requestID := uuid.NewString()
	deploymentID := "gcnv-8d0ff6a0b456f30"
	deadline := time.Now().UTC().Add(2 * time.Hour)

	// The orchestrator now owns durable job creation; the endpoint only
	// validates and forwards, returning the operation ID it gets back.
	mockOrch.On("CreatePool", mock.Anything, mock.MatchedBy(func(p *common.CreatePoolParams) bool {
		return p != nil && p.VCM != nil &&
			p.VCM.RequestID == requestID &&
			p.VCM.DeploymentID == deploymentID
	})).Return(nil, requestID, nil)

	result, err := handler.PoolCreateV1(ctx, testPoolCreateRequest(requestID, deploymentID, deadline))
	require.NoError(t, err)
	opRes, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok, "expected OperationV1, got %T", result)
	assert.Equal(t, "/v1/operations/"+requestID, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestPoolCreateV1_EmptyOperationID_FallsBackToRequestID(t *testing.T) {
	// When the orchestrator returns no operation id (e.g. it reattached to an
	// in-flight run), the endpoint must fall back to the caller-supplied
	// requestId so the returned operation name is still pollable.
	handler, _, mockOrch := configuredPoolHandler(t)
	req := fullPoolRequest()
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", nil)

	res, err := handler.PoolCreateV1(context.Background(), req)

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an OperationV1, got %T", res)
	assert.Equal(t, "/v1/operations/"+req.RequestId, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestPoolCreateV1RequiresRequestFields(t *testing.T) {
	handler, _, _ := configuredPoolHandler(t)
	future := time.Now().UTC().Add(time.Hour)

	for _, tc := range []struct {
		name string
		req  *vcmserver.PoolRequestV1
	}{
		{name: "nil request", req: nil},
		{name: "empty requestId", req: testPoolCreateRequest("", "gcnv-8d0ff6a0b456f30", future)},
		{name: "empty deploymentId", req: testPoolCreateRequest(uuid.NewString(), "", future)},
		{name: "zero deadline", req: testPoolCreateRequest(uuid.NewString(), "gcnv-8d0ff6a0b456f30", time.Time{})},
		{name: "past deadline", req: testPoolCreateRequest(uuid.NewString(), "gcnv-8d0ff6a0b456f30", time.Now().UTC().Add(-time.Minute))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.PoolCreateV1(context.Background(), tc.req)
			require.NoError(t, err)

			badRequest, ok := result.(*vcmserver.PoolCreateV1BadRequest)
			require.True(t, ok)
			assert.EqualValues(t, 400, badRequest.Code)
		})
	}
}

func TestPoolCreateV1_Success_ReturnsOperation(t *testing.T) {
	handler, _, mockOrch := configuredPoolHandler(t)
	req := fullPoolRequest()
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "op-123", nil)

	res, err := handler.PoolCreateV1(context.Background(), req)

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/op-123", opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestPoolCreateV1_OrchestratorError_Returns500(t *testing.T) {
	handler, _, mockOrch := configuredPoolHandler(t)
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", errors.New("dispatch failed"))

	res, err := handler.PoolCreateV1(context.Background(), fullPoolRequest())

	require.NoError(t, err, "handler surfaces the failure through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.PoolCreateV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
	// The endpoint deliberately returns a generic message so internal error
	// details (e.g. "dispatch failed") are not leaked to the caller.
	assert.Contains(t, errRes.Message, "Internal error occurred")
	assert.True(t, errRes.Retriable.Or(false))
	mockOrch.AssertExpectations(t)
}

func TestPoolCreateV1_DuplicateRequest_ReturnsNonRetriableClientError(t *testing.T) {
	handler, _, mockOrch := configuredPoolHandler(t)
	// The orchestrator rejects a duplicate requestId with a resource-state
	// conflict; the endpoint must surface it as a clear, non-retriable client
	// error rather than a retriable 500 the caller would keep polling.
	conflict := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
		errors.New(`create-pool request "3fa85f64-5717-4562-b3fc-2c963f66afa6" already exists in state PROCESSING`))
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", conflict)

	res, err := handler.PoolCreateV1(context.Background(), fullPoolRequest())

	require.NoError(t, err, "handler surfaces the conflict through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.PoolCreateV1BadRequest)
	require.True(t, ok, "expected a BadRequest response, got %T", res)
	assert.EqualValues(t, 400, errRes.Code)
	assert.False(t, errRes.Retriable.Or(true), "a duplicate request must be non-retriable")
	assert.Contains(t, errRes.Message, "already exists in state PROCESSING")
	mockOrch.AssertExpectations(t)
}

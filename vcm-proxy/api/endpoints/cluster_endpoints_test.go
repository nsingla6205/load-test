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
		OntapCredentials: func() vcmserver.OptOntapCredentialsV1 {
			def := vcmserver.UserCredentialsV1{}
			def.UserPurpose.SetTo(vcmserver.UserCredentialsV1UserPurposePURPOSEVCMADMIN)
			def.Username.SetTo("root")
			def.ActiveAuthMode.SetTo(vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODEPASSWORD)
			def.PasswordSecret.SetTo("vcm-secret")
			mode := vcmserver.UserCredentialsV1{}
			mode.UserPurpose.SetTo(vcmserver.UserCredentialsV1UserPurposePURPOSEONTAPMODE)
			mode.Username.SetTo("mode-user")
			mode.ActiveAuthMode.SetTo(vcmserver.UserCredentialsV1ActiveAuthModeAUTHMODEPASSWORD)
			mode.PasswordSecret.SetTo("mode-secret")
			oc := vcmserver.OntapCredentialsV1{}
			oc.AdminPassSecretId.SetTo("admin-secret")
			oc.DefaultModeUserCredentials.SetTo(def)
			oc.OntapModeUserCredentials.SetTo(mode)
			return vcmserver.NewOptOntapCredentialsV1(oc)
		}(),
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

func clusterRequest() *vcmserver.ClusterV1 {
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
			OntapModeRbac: vcmserver.NewOptOntapModeRbacV1(vcmserver.OntapModeRbacV1{
				RbacFileUrl:      vcmserver.NewOptString("gs://rbac-bucket/ontap-mode.yaml"),
				RbacFileCheckSum: vcmserver.NewOptString("rbac-checksum"),
			}),
		},
	}
}

func fullClusterCreateParams() vcmserver.ClusterCreateV1Params {
	return testOperationClusterCreateParams(testOperationRequestID, testOperationTimeout)
}

const testUpdateMask = "clusterShape.provisionedThroughputMibps,clusterShape.provisionedIops,clusterShape.provisionedSizeInBytes"

func testChangeType() vcmserver.OptChangeTypeV1 {
	return vcmserver.NewOptChangeTypeV1(vcmserver.ChangeTypeV1SCALE)
}

func fullClusterUpdateParams() vcmserver.ClusterUpdateV1Params {
	return testOperationClusterUpdateParams(testOperationRequestID, testOperationTimeout, testUpdateMask, testChangeType())
}

func clusterRequestWithoutSize() *vcmserver.ClusterV1 {
	req := clusterRequest()
	req.ClusterShape.ProvisionedSizeInBytes.Reset()
	return req
}

func TestClusterCreateV1_MergesRequestShapeWithStoredConfig(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	req := clusterRequest()
	params := fullClusterCreateParams()

	var dispatched *common.CreatePoolParams
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			dispatched = args.Get(1).(*common.CreatePoolParams)
		}).
		Return(nil, params.RequestId, nil)

	_, err := handler.ClusterCreateV1(context.Background(), req, params)
	require.NoError(t, err)
	require.NotNil(t, dispatched)
	require.NotNil(t, dispatched.VCM)
	vcm := dispatched.VCM

	// Request-level controls.
	assert.Equal(t, params.RequestId, vcm.RequestID)
	assert.WithinDuration(t, time.Now().UTC().Add(3600*time.Second), vcm.Deadline, 2*time.Second)

	// Sourced from the request's cluster shape.
	assert.Equal(t, "ONTAP", vcm.Mode)
	assert.Equal(t, "auto", vcm.QosType)
	assert.Equal(t, int64(1024), vcm.ThroughputMibps)
	assert.Equal(t, int64(5000), vcm.Iops)
	assert.Equal(t, int64(1099511627776), vcm.SizeInBytes)
	assert.Equal(t, "34", vcm.RegionIdentifier)
	assert.Equal(t, "bucket-1", vcm.AutoTierBucketName)
	assert.Equal(t, "gs://rbac-bucket/ontap-mode.yaml", vcm.OntapModeRbacFileURL)
	assert.Equal(t, "rbac-checksum", vcm.OntapModeRbacChecksum)

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
	// admin (adminPassSecretId) + default (VCM_ADMIN) + ontap-mode entries.
	require.Len(t, vcm.OntapCredentials, 3)
	assert.Equal(t, "admin-secret", common.AdminPasswordSecret(vcm.OntapCredentials))
	credentialsByType := make(map[string]common.VCMOntapCredential, len(vcm.OntapCredentials))
	for _, credential := range vcm.OntapCredentials {
		credentialsByType[credential.Type] = credential
	}
	assert.Equal(t, "vcm-secret", credentialsByType["default"].SecretID)
	assert.Empty(t, credentialsByType["default"].CertificateID, "password auth resolves via password, not certificate")
	assert.Equal(t, "mode-secret", credentialsByType["ontap-mode"].SecretID)
	assert.Equal(t, "mode-user", credentialsByType["ontap-mode"].Username)

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
		clusterRequest(),
		testOperationClusterCreateParams(requestID, "7200s"))

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
	params := fullClusterCreateParams()
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "", nil)

	res, err := handler.ClusterCreateV1(context.Background(), clusterRequest(), params)

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
		req    *vcmserver.ClusterV1
		params vcmserver.ClusterCreateV1Params
	}{
		{name: "nil request", req: nil, params: fullClusterCreateParams()},
		{name: "empty requestId", req: clusterRequest(), params: testOperationClusterCreateParams("", testOperationTimeout)},
		{name: "empty timeout", req: clusterRequest(), params: testOperationClusterCreateParams(uuid.NewString(), "")},
		{name: "invalid timeout unit", req: clusterRequest(), params: testOperationClusterCreateParams(uuid.NewString(), "10m")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.ClusterCreateV1(context.Background(), tc.req, tc.params)
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

	res, err := handler.ClusterCreateV1(context.Background(), clusterRequest(), fullClusterCreateParams())

	require.NoError(t, err)
	badRequest, ok := res.(*vcmserver.ClusterCreateV1BadRequest)
	require.True(t, ok, "expected a BadRequest response, got %T", res)
	assert.EqualValues(t, 400, badRequest.Code)
	assert.True(t, badRequest.Retriable.Or(false), "CLH can re-seed config and retry")
	assert.Contains(t, badRequest.Message, "deploymentId is not configured")
	mockOrch.AssertNotCalled(t, "CreatePool", mock.Anything, mock.Anything)
}

func TestClusterCreateV1_OntapModeWithoutRbac_ReturnsNonRetriableBadRequest(t *testing.T) {
	tests := []struct {
		name    string
		rbac    vcmserver.OptOntapModeRbacV1
		wantMsg string
	}{
		{name: "pair omitted", wantMsg: "rbacFileUrl"},
		{
			name: "checksum omitted",
			rbac: vcmserver.NewOptOntapModeRbacV1(vcmserver.OntapModeRbacV1{
				RbacFileUrl: vcmserver.NewOptString("gs://rbac-bucket/ontap-mode.yaml"),
			}),
			wantMsg: "rbacFileCheckSum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, _, mockOrch := configuredClusterHandler(t)
			req := clusterRequest()
			req.ClusterShape.OntapModeRbac = tt.rbac

			res, err := handler.ClusterCreateV1(context.Background(), req, fullClusterCreateParams())

			require.NoError(t, err)
			badRequest, ok := res.(*vcmserver.ClusterCreateV1BadRequest)
			require.True(t, ok, "expected a BadRequest response, got %T", res)
			assert.EqualValues(t, 400, badRequest.Code)
			assert.False(t, badRequest.Retriable.Or(true), "the same request body cannot succeed on retry")
			assert.Contains(t, badRequest.Message, tt.wantMsg)
			mockOrch.AssertNotCalled(t, "CreatePool", mock.Anything, mock.Anything)
		})
	}
}

func TestClusterCreateV1_Success_ReturnsOperation(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("CreatePool", mock.Anything, mock.Anything).Return(nil, "op-123", nil)

	res, err := handler.ClusterCreateV1(context.Background(), clusterRequest(), fullClusterCreateParams())

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

	res, err := handler.ClusterCreateV1(context.Background(), clusterRequest(), fullClusterCreateParams())

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

	res, err := handler.ClusterCreateV1(context.Background(), clusterRequest(), fullClusterCreateParams())

	require.NoError(t, err, "handler surfaces the conflict through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.ClusterCreateV1Conflict)
	require.True(t, ok, "expected a Conflict response, got %T", res)
	assert.EqualValues(t, 409, errRes.Code)
	assert.False(t, errRes.Retriable.Or(true), "a duplicate request must be non-retriable")
	assert.Contains(t, errRes.Message, "already exists in state PROCESSING")
	mockOrch.AssertExpectations(t)
}

func TestClusterUpdateV1_MergesRequestShapeWithStoredConfig(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	params := fullClusterUpdateParams()

	var dispatched *common.UpdatePoolParams
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			dispatched = args.Get(1).(*common.UpdatePoolParams)
		}).
		Return(nil, params.RequestId, nil)

	_, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), params)
	require.NoError(t, err)
	require.NotNil(t, dispatched)
	require.NotNil(t, dispatched.VCM)
	vcm := dispatched.VCM

	assert.Equal(t, params.RequestId, vcm.RequestID)
	assert.WithinDuration(t, time.Now().UTC().Add(3600*time.Second), vcm.Deadline, 2*time.Second)
	assert.Equal(t, testUpdateMask, vcm.UpdateMask)
	assert.Equal(t, string(vcmserver.ChangeTypeV1SCALE), vcm.ChangeType)

	assert.Equal(t, "ONTAP", vcm.Mode)
	assert.Equal(t, testDeploymentID, vcm.DeploymentID)
	assert.Equal(t, "us-central1-a", vcm.PrimaryZone)

	assert.Equal(t, int64(5000), vcm.Iops)
	assert.Equal(t, int64(1099511627776), vcm.SizeInBytes)
	assert.Equal(t, int64(1024), vcm.ThroughputMibps)

	mockOrch.AssertExpectations(t)
}

func TestValidateMaskedClusterShape(t *testing.T) {
	shapeWith := func(mutate func(*vcmserver.ClusterShapeV1)) vcmserver.ClusterShapeV1 {
		shape := clusterRequest().ClusterShape
		mutate(&shape)
		return shape
	}

	for _, tc := range []struct {
		name       string
		updateMask string
		shape      vcmserver.ClusterShapeV1
		wantErr    string
	}{
		{
			name:       "masked fields are set",
			updateMask: testUpdateMask,
			shape:      clusterRequest().ClusterShape,
		},
		{
			name:       "masked size is absent",
			updateMask: testUpdateMask,
			shape: shapeWith(func(s *vcmserver.ClusterShapeV1) {
				s.ProvisionedSizeInBytes.Reset()
			}),
			wantErr: `updateMask names "clusterShape.provisionedSizeInBytes" but the field is not set in clusterShape`,
		},
		{
			name:       "masked iops is zero",
			updateMask: testUpdateMask,
			shape: shapeWith(func(s *vcmserver.ClusterShapeV1) {
				s.ProvisionedIops = vcmserver.NewOptInt64(0)
			}),
			wantErr: `updateMask names "clusterShape.provisionedIops" but its value 0 is not greater than zero`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMaskedClusterShape(tc.updateMask, tc.shape)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestValidateProvisionedShapeGroup(t *testing.T) {
	shapeWith := func(mutate func(*vcmserver.ClusterShapeV1)) vcmserver.ClusterShapeV1 {
		shape := clusterRequest().ClusterShape
		mutate(&shape)
		return shape
	}

	for _, tc := range []struct {
		name    string
		shape   vcmserver.ClusterShapeV1
		wantErr bool
	}{
		{name: "all fields present", shape: clusterRequest().ClusterShape},
		{
			name: "all fields absent",
			shape: shapeWith(func(s *vcmserver.ClusterShapeV1) {
				s.ProvisionedIops.Reset()
				s.ProvisionedThroughputMibps.Reset()
				s.ProvisionedSizeInBytes.Reset()
			}),
		},
		{
			name: "only IOPS present",
			shape: shapeWith(func(s *vcmserver.ClusterShapeV1) {
				s.ProvisionedThroughputMibps.Reset()
				s.ProvisionedSizeInBytes.Reset()
			}),
			wantErr: true,
		},
		{
			name: "size missing",
			shape: shapeWith(func(s *vcmserver.ClusterShapeV1) {
				s.ProvisionedSizeInBytes.Reset()
			}),
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProvisionedShapeGroup(tc.shape)
			if tc.wantErr {
				require.EqualError(t, err,
					"provisionedIops, provisionedThroughputMibps, and provisionedSizeInBytes must be provided together")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestClusterUpdateV1ForwardsToOrchestrator(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	requestID := uuid.NewString()

	mockOrch.On("UpdatePool", mock.Anything, mock.MatchedBy(func(p *common.UpdatePoolParams) bool {
		return p != nil && p.VCM != nil &&
			p.VCM.RequestID == requestID &&
			p.VCM.DeploymentID == testDeploymentID &&
			p.VCM.UpdateMask == testUpdateMask
	})).Return(nil, requestID, nil)

	result, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(),
		testOperationClusterUpdateParams(requestID, "7200s", testUpdateMask, testChangeType()))

	require.NoError(t, err)
	opRes, ok := result.(*vcmserver.OperationV1)
	require.True(t, ok, "expected OperationV1, got %T", result)
	assert.Equal(t, "/v1/operations/"+requestID, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestClusterUpdateV1_EmptyOperationID_FallsBackToRequestID(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	params := fullClusterUpdateParams()
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).Return(nil, "", nil)

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), params)

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an OperationV1, got %T", res)
	assert.Equal(t, "/v1/operations/"+params.RequestId, opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestValidateClusterUpdateMask(t *testing.T) {
	for _, tc := range []struct {
		name         string
		updateMask   string
		wantContains string
	}{
		{
			name:       "supported provisioned shape group",
			updateMask: testUpdateMask,
		},
		{
			name:         "partial provisioned shape group",
			updateMask:   "clusterShape.provisionedThroughputMibps",
			wantContains: "updateMask must include provisionedIops, provisionedThroughputMibps, and provisionedSizeInBytes together",
		},
		{
			// preferredVsaInstanceFamily lives under infrastructureSpec, so the
			// flat path is not a field of clusterShape at all.
			name:         "flat preferredVsaInstanceFamily is not a schema path",
			updateMask:   "clusterShape.preferredVsaInstanceFamily",
			wantContains: `invalid updateMask field path "clusterShape.preferredVsaInstanceFamily"`,
		},
		{
			name:         "nested preferredVsaInstanceFamily not supported",
			updateMask:   "clusterShape.infrastructureSpec.preferredVsaInstanceFamily",
			wantContains: `updateMask field path "clusterShape.infrastructureSpec.preferredVsaInstanceFamily" is not supported`,
		},
		{
			name:         "valid parent path not supported",
			updateMask:   "clusterShape",
			wantContains: `updateMask field path "clusterShape" is not supported`,
		},
		{
			name:         "valid nested path not supported",
			updateMask:   "clusterShape.infrastructureSpec.haPairCount",
			wantContains: `updateMask field path "clusterShape.infrastructureSpec.haPairCount" is not supported`,
		},
		{
			name:         "unknown field",
			updateMask:   "clusterShape.provisionedABCDWrongThroughputMibps",
			wantContains: `invalid updateMask field path "clusterShape.provisionedABCDWrongThroughputMibps"`,
		},
		{
			name:         "output-only field",
			updateMask:   "clusterLayout",
			wantContains: `invalid updateMask field path "clusterLayout"`,
		},
		{
			name:         "request id",
			updateMask:   "requestId",
			wantContains: `invalid updateMask field path "requestId"`,
		},
		{
			name:         "empty path",
			updateMask:   "clusterShape.provisionedIops,",
			wantContains: `invalid updateMask field path ""`,
		},
		{
			name:         "whitespace around path",
			updateMask:   "clusterShape.provisionedIops, clusterShape.provisionedSizeInBytes",
			wantContains: `invalid updateMask field path " clusterShape.provisionedSizeInBytes"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClusterUpdateMask(tc.updateMask)
			if tc.wantContains == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantContains)
		})
	}
}

func TestValidateClusterChangeType(t *testing.T) {
	for _, tc := range []struct {
		name       string
		changeType vcmserver.ChangeTypeV1
		wantErr    string
	}{
		{name: "compute", changeType: vcmserver.ChangeTypeV1COMPUTE},
		{name: "scale", changeType: vcmserver.ChangeTypeV1SCALE},
		{name: "unspecified", changeType: vcmserver.ChangeTypeV1CHANGETYPEUNSPECIFIED, wantErr: `changeType "CHANGE_TYPE_UNSPECIFIED" is not supported`},
		{name: "config", changeType: vcmserver.ChangeTypeV1CONFIG, wantErr: `changeType "CONFIG" is not supported`},
		{name: "empty", changeType: "", wantErr: `changeType "" is not supported`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClusterChangeType(tc.changeType)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestClusterUpdateV1ValidatesRequest(t *testing.T) {
	handler, _, _ := configuredClusterHandler(t)

	for _, tc := range []struct {
		name        string
		req         *vcmserver.ClusterV1
		params      vcmserver.ClusterUpdateV1Params
		wantMessage string
	}{
		{name: "nil request", req: nil, params: fullClusterUpdateParams(), wantMessage: "request body is required"},
		{name: "empty requestId", req: clusterRequest(), params: testOperationClusterUpdateParams("", testOperationTimeout, testUpdateMask, testChangeType()), wantMessage: "requestId is required"},
		{name: "whitespace requestId", req: clusterRequest(), params: testOperationClusterUpdateParams("   ", testOperationTimeout, testUpdateMask, testChangeType()), wantMessage: "requestId is required"},
		{name: "empty updateMask", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, "", testChangeType()), wantMessage: "updateMask is required"},
		{name: "invalid updateMask field", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, "clusterShape.provisionedABCDWrongThroughputMibps", testChangeType()), wantMessage: `invalid updateMask field path "clusterShape.provisionedABCDWrongThroughputMibps"`},
		{name: "unsupported updateMask field", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, "clusterShape.qosType", testChangeType()), wantMessage: `updateMask field path "clusterShape.qosType" is not supported`},
		{name: "masked field missing from shape", req: clusterRequestWithoutSize(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, testUpdateMask, testChangeType()), wantMessage: "provisionedIops, provisionedThroughputMibps, and provisionedSizeInBytes must be provided together"},
		{name: "unsupported changeType unspecified", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, testUpdateMask, vcmserver.NewOptChangeTypeV1(vcmserver.ChangeTypeV1CHANGETYPEUNSPECIFIED)), wantMessage: `changeType "CHANGE_TYPE_UNSPECIFIED" is not supported`},
		{name: "unsupported changeType config", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), testOperationTimeout, testUpdateMask, vcmserver.NewOptChangeTypeV1(vcmserver.ChangeTypeV1CONFIG)), wantMessage: `changeType "CONFIG" is not supported`},
		{name: "invalid timeout", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), "10m", testUpdateMask, testChangeType()), wantMessage: "timeout must be specified in seconds (for example 600s)"},
		{name: "zero timeout", req: clusterRequest(), params: testOperationClusterUpdateParams(uuid.NewString(), "0s", testUpdateMask, testChangeType()), wantMessage: "timeout must be greater than 0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := handler.ClusterUpdateV1(context.Background(), tc.req, tc.params)
			require.NoError(t, err)

			badRequest, ok := result.(*vcmserver.ClusterUpdateV1BadRequest)
			require.True(t, ok)
			assert.EqualValues(t, 400, badRequest.Code)
			assert.False(t, badRequest.Retriable.Or(true))
			assert.Equal(t, tc.wantMessage, badRequest.Message)
		})
	}
}

func TestClusterUpdateV1_UnseededConfig_ReturnsRetriableBadRequest(t *testing.T) {
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-cluster-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{Project: "p", Instance: "i", Database: "d"},
	})
	waitForConfigured(t, store)
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{ConfigStore: store, Orchestrator: mockOrch}

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), fullClusterUpdateParams())

	require.NoError(t, err)
	badRequest, ok := res.(*vcmserver.ClusterUpdateV1BadRequest)
	require.True(t, ok, "expected a BadRequest response, got %T", res)
	assert.EqualValues(t, 400, badRequest.Code)
	assert.True(t, badRequest.Retriable.Or(false), "CLH can re-seed config and retry")
	assert.Contains(t, badRequest.Message, "deploymentId is not configured")
	mockOrch.AssertNotCalled(t, "UpdatePool", mock.Anything, mock.Anything)
}

func TestClusterUpdateV1_Success_ReturnsOperation(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).Return(nil, "op-123", nil)

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), fullClusterUpdateParams())

	require.NoError(t, err)
	opRes, ok := res.(*vcmserver.OperationV1)
	require.True(t, ok, "expected an Operation_v1, got %T", res)
	assert.Equal(t, "/v1/operations/op-123", opRes.Name)
	assert.False(t, opRes.Done)
	mockOrch.AssertExpectations(t)
}

func TestClusterUpdateV1_OrchestratorError_Returns500(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).Return(nil, "", errors.New("dispatch failed"))

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), fullClusterUpdateParams())

	require.NoError(t, err, "handler surfaces the failure through the typed response, not a Go error")
	errRes, ok := res.(*vcmserver.ClusterUpdateV1InternalServerError)
	require.True(t, ok, "expected an InternalServerError response, got %T", res)
	assert.EqualValues(t, 500, errRes.Code)
	assert.Contains(t, errRes.Message, "Internal error occurred")
	assert.True(t, errRes.Retriable.Or(false))
	mockOrch.AssertExpectations(t)
}

func TestClusterUpdateV1_DuplicateRequest_ReturnsNonRetriableClientError(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	conflict := vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
		errors.New(`update-pool request "3fa85f64-5717-4562-b3fc-2c963f66afa6" already exists in state PROCESSING`))
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).Return(nil, "", conflict)

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), fullClusterUpdateParams())

	require.NoError(t, err)
	errRes, ok := res.(*vcmserver.ClusterUpdateV1Conflict)
	require.True(t, ok, "expected a Conflict response, got %T", res)
	assert.EqualValues(t, 409, errRes.Code)
	assert.False(t, errRes.Retriable.Or(true))
	assert.Contains(t, errRes.Message, "already exists in state PROCESSING")
	mockOrch.AssertExpectations(t)
}

func TestClusterUpdateV1_NotFound_Returns404(t *testing.T) {
	handler, _, mockOrch := configuredClusterHandler(t)
	notFound := vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
		errors.New(`cluster "gcnv-8d0ff6a0b456f30" not found`))
	mockOrch.On("UpdatePool", mock.Anything, mock.Anything).Return(nil, "", notFound)

	res, err := handler.ClusterUpdateV1(context.Background(), clusterRequest(), fullClusterUpdateParams())

	require.NoError(t, err)
	errRes, ok := res.(*vcmserver.ClusterUpdateV1NotFound)
	require.True(t, ok, "expected a NotFound response, got %T", res)
	assert.EqualValues(t, 404, errRes.Code)
	assert.False(t, errRes.Retriable.Or(true))
	assert.Contains(t, errRes.Message, "not found")
	mockOrch.AssertExpectations(t)
}

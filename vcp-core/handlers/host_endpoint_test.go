package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"gorm.io/gorm"
)

func TestConvertDatamodelHostToV1(t *testing.T) {
	t.Run("nil host returns empty response", func(t *testing.T) {
		got, err := convertDatamodelHostToV1(nil)
		require.NoError(t, err)
		assert.Equal(t, oasgenserver.HostV1{}, got)
	})

	t.Run("maps all fields including capacity and attributes", func(t *testing.T) {
		now := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
		deletedAt := gorm.DeletedAt{Time: now.Add(time.Hour), Valid: true}
		vendorID := "projects/test/locations/us-central1/keyRings/kr/cryptoKeys/key"
		port := 443
		efficiencyFactor := 1.4
		provisioningModel := datamodel.ProvisioningModelThin

		host := &datamodel.Host{
			BaseModel: datamodel.BaseModel{
				UUID:      "host-table-uuid-123",
				CreatedAt: now,
				UpdatedAt: now,
				DeletedAt: &deletedAt,
			},
			ExternalUUID:      "cluster-uuid-123",
			ExternalName:      "vsim-cluster",
			Name:              "host-01",
			VendorID:          &vendorID,
			Port:              &port,
			Protocol:          "INSECURE_HTTPS",
			Username:          "admin",
			Password:          "encrypted-password",
			State:             datamodel.LifeCycleStateCreated,
			StateDetails:      datamodel.LifeCycleStateCreatedDetails,
			Label:             "type=VSIM",
			IPAddress:         "10.0.0.1",
			Capacity:          68317667328,
			EfficiencyFactor:  efficiencyFactor,
			ProvisioningModel: &provisioningModel,
			Attributes: &datamodel.HostAttributes{
				MaintenanceMode:   true,
				PublicAccess:      true,
				Version:           "9.15.1",
				VersionGeneration: 9,
				VersionMajor:      15,
				VersionMinor:      1,
			},
		}

		got, err := convertDatamodelHostToV1(host)
		require.NoError(t, err)

		assert.Equal(t, "host-01", got.Name)
		assert.Equal(t, oasgenserver.HostV1ProtocolINSECUREHTTPS, got.Protocol)
		assert.Empty(t, got.Username)
		assert.Empty(t, got.Password)
		assert.Equal(t, now, got.CreatedAt.Value)
		assert.Equal(t, now, got.UpdatedAt.Value)
		assert.Equal(t, "host-table-uuid-123", got.UUID.Value)
		assert.Equal(t, "cluster-uuid-123", got.ExternalClusterId.Value)
		assert.Equal(t, "vsim-cluster", got.ExternalName.Value)
		assert.Equal(t, vendorID, got.VendorID.Value)
		assert.Equal(t, int32(443), got.Port.Value)
		assert.Equal(t, oasgenserver.HostV1LifeCycleStateCREATED, got.LifeCycleState.Value)
		assert.Equal(t, datamodel.LifeCycleStateCreatedDetails, got.LifeCycleStateDetails.Value)
		assert.Equal(t, "10.0.0.1", got.IpAddress.Value)
		assert.Equal(t, "type=VSIM", got.Label.Value)
		assert.True(t, got.Capacity.IsSet())
		assert.Equal(t, int64(68317667328), got.Capacity.Value)
		assert.True(t, got.MaintenanceMode.Value)
		assert.True(t, got.PublicAccess.Value)
		assert.Equal(t, "9.15.1", got.OntapVersion.Value)
		assert.Equal(t, int32(9), got.VersionTuple.Value.Generation.Value)
		assert.Equal(t, int32(15), got.VersionTuple.Value.Major.Value)
		assert.Equal(t, int32(1), got.VersionTuple.Value.Minor.Value)
		assert.True(t, got.EfficiencyFactor.IsSet())
		assert.Equal(t, 1.4, got.EfficiencyFactor.Value)
		assert.True(t, got.ProvisioningModel.IsSet())
		assert.Equal(t, oasgenserver.HostV1ProvisioningModelTHIN, got.ProvisioningModel.Value)
		assert.True(t, got.DeletedAt.IsSet())
		assert.False(t, got.DeletedAt.Null)
		assert.Equal(t, deletedAt.Time, got.DeletedAt.Value)
	})

	t.Run("omits optional fields when unset", func(t *testing.T) {
		host := &datamodel.Host{
			Name:             "host-minimal",
			Protocol:         "HTTPS",
			Username:         "admin",
			Password:         "secret",
			Capacity:         0,
			EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor,
		}

		got, err := convertDatamodelHostToV1(host)
		require.NoError(t, err)

		assert.Equal(t, "host-minimal", got.Name)
		assert.Empty(t, got.Username)
		assert.Empty(t, got.Password)
		assert.False(t, got.VendorID.IsSet())
		assert.False(t, got.Port.IsSet())
		assert.False(t, got.LifeCycleState.IsSet())
		assert.False(t, got.LifeCycleStateDetails.IsSet())
		assert.False(t, got.IpAddress.IsSet())
		assert.False(t, got.Label.IsSet())
		assert.True(t, got.Capacity.IsSet())
		assert.Equal(t, int64(0), got.Capacity.Value)
		assert.False(t, got.MaintenanceMode.IsSet())
		assert.False(t, got.PublicAccess.IsSet())
		assert.False(t, got.OntapVersion.IsSet())
		assert.True(t, got.EfficiencyFactor.IsSet())
		assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, got.EfficiencyFactor.Value)
		assert.True(t, got.ProvisioningModel.IsSet())
		assert.Equal(t, oasgenserver.HostV1ProvisioningModelTHICK, got.ProvisioningModel.Value)
		assert.False(t, got.DeletedAt.IsSet())
	})

	t.Run("unknown provisioning model returns error", func(t *testing.T) {
		unknown := datamodel.ProvisioningModel("UNKNOWN")
		_, err := convertDatamodelHostToV1(&datamodel.Host{ProvisioningModel: &unknown})
		require.Error(t, err)
	})
}

func TestV1ListHosts_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	hosts := []*datamodel.Host{sampleDatamodelHost()}
	mockOrch.On("ListHosts", mock.Anything).Return(hosts, nil)

	result, err := handler.V1ListHosts(context.Background(), oasgenserver.V1ListHostsParams{})

	require.NoError(t, err)
	res, ok := result.(*oasgenserver.V1ListHostsOKApplicationJSON)
	require.True(t, ok)
	require.Len(t, *res, 1)
	assert.Equal(t, "host-01", (*res)[0].Name)
	assert.True(t, (*res)[0].ProvisioningModel.IsSet())
	assert.Equal(t, oasgenserver.HostV1ProvisioningModelTHICK, (*res)[0].ProvisioningModel.Value)
}

func TestV1ListHosts_NotImplemented(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("ListHosts", mock.Anything).Return(nil, utilserrors.NewNotImplementedYetErr())

	result, err := handler.V1ListHosts(context.Background(), oasgenserver.V1ListHostsParams{})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1ListHostsInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "not implemented yet")
}

func TestV1ListHosts_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("ListHosts", mock.Anything).Return(nil, errors.New("database unavailable"))

	result, err := handler.V1ListHosts(context.Background(), oasgenserver.V1ListHostsParams{})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1ListHostsInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "failed to list hosts")
	assert.Contains(t, serverErr.Message, "database unavailable")
}

func TestV1CreateHost_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	maintenanceMode := true
	publicAccess := false
	port := int32(443)
	protocol := oasgenserver.HostV1ProtocolHTTPS
	vendorID := "vendor-123"
	label := "type=VSIM"
	efficiencyFactor := 1.4
	provisioningModel := datamodel.ProvisioningModelThin

	req := &oasgenserver.HostV1{
		Name:              "host-01",
		Username:          "admin",
		Password:          "secret",
		ExternalClusterId: oasgenserver.NewOptString("cluster-uuid-123"),
		ExternalName:      oasgenserver.NewOptString("vsim-cluster"),
		VendorID:          oasgenserver.NewOptString(vendorID),
		MaintenanceMode:   oasgenserver.NewOptBool(maintenanceMode),
		PublicAccess:      oasgenserver.NewOptBool(publicAccess),
		EfficiencyFactor:  oasgenserver.NewOptFloat64(efficiencyFactor),
		ProvisioningModel: oasgenserver.NewOptHostV1ProvisioningModel(
			oasgenserver.HostV1ProvisioningModelTHIN,
		),
		IpAddress: oasgenserver.NewOptString("10.0.0.1"),
		Port:      oasgenserver.NewOptInt32(port),
		Protocol:  protocol,
		Label:     oasgenserver.NewOptString(label),
	}

	now := time.Now().UTC()
	created := &datamodel.Host{
		BaseModel: datamodel.BaseModel{
			CreatedAt: now,
			UpdatedAt: now,
		},
		ExternalUUID:      "cluster-uuid-123",
		ExternalName:      "vsim-cluster",
		Name:              "host-01",
		VendorID:          &vendorID,
		Port:              func() *int { p := int(port); return &p }(),
		Protocol:          string(protocol),
		Username:          "admin",
		Password:          "encrypted-password",
		State:             datamodel.LifeCycleStateCreating,
		StateDetails:      datamodel.LifeCycleStateCreatingDetails,
		Label:             label,
		IPAddress:         "10.0.0.1",
		Capacity:          68317667328,
		EfficiencyFactor:  efficiencyFactor,
		ProvisioningModel: &provisioningModel,
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: maintenanceMode,
			PublicAccess:    publicAccess,
		},
	}

	mockOrch.On("CreateOntapHost", mock.Anything, mock.MatchedBy(func(p *commonparams.CreateHostParams) bool {
		return p != nil &&
			p.Name == "host-01" &&
			p.Username == "admin" &&
			p.Password == log.Secret("secret") &&
			p.ExternalUUID == "" &&
			p.ExternalName == "" &&
			p.VendorID != nil && *p.VendorID == vendorID &&
			p.MaintenanceMode != nil && *p.MaintenanceMode == maintenanceMode &&
			p.PublicAccess != nil && *p.PublicAccess == publicAccess &&
			p.IPAddress != nil && *p.IPAddress == "10.0.0.1" &&
			p.Port != nil && *p.Port == int(port) &&
			p.Protocol != nil && *p.Protocol == string(protocol) &&
			p.Label != nil && *p.Label == label &&
			p.EfficiencyFactor != nil && *p.EfficiencyFactor == efficiencyFactor &&
			p.ProvisioningModel != nil && *p.ProvisioningModel == datamodel.ProvisioningModelThin
	})).Return(created, "job-uuid-123", nil)

	result, err := handler.V1CreateHost(context.Background(), req, oasgenserver.V1CreateHostParams{})

	require.NoError(t, err)
	op, ok := result.(*oasgenserver.OperationV1)
	require.True(t, ok)
	assert.True(t, op.Name.IsSet())
	assert.Equal(t, "/v1/operations/job-uuid-123", op.Name.Value)
	assert.True(t, op.Done.IsSet())
	assert.False(t, op.Done.Value)
	assert.NotNil(t, op.Response)
}

func TestV1CreateHost_NilRequest(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	result, err := handler.V1CreateHost(context.Background(), nil, oasgenserver.V1CreateHostParams{})

	require.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1CreateHostBadRequest)
	require.True(t, ok)
	assert.Equal(t, float64(commonparams.HTTPStatusBadRequest), badReq.Code)
	assert.Equal(t, "request body is required", badReq.Message)
	mockOrch.AssertNotCalled(t, "CreateOntapHost", mock.Anything, mock.Anything)
}

func TestV1CreateHost_ErrorResponses(t *testing.T) {
	validReq := &oasgenserver.HostV1{
		Name:     "host-01",
		Username: "admin",
		Password: "secret",
	}

	tests := []struct {
		name         string
		err          error
		wantCode     float64
		wantContains string
	}{
		{
			name:         "not implemented",
			err:          utilserrors.NewNotImplementedYetErr(),
			wantCode:     commonparams.HTTPStatusInternalServerError,
			wantContains: "not implemented yet",
		},
		{
			name: "resource state conflict",
			err: vsaerrors.NewVCPError(
				vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf(`host %q already exists`, "host-01"),
			),
			wantCode:     commonparams.HTTPStatusConflict,
			wantContains: `host "host-01" already exists`,
		},
		{
			name:         "conflict from utils error",
			err:          utilserrors.NewConflictErr("host already exists"),
			wantCode:     commonparams.HTTPStatusConflict,
			wantContains: "host already exists",
		},
		{
			name:         "bad request",
			err:          utilserrors.NewBadRequestErr("invalid protocol"),
			wantCode:     commonparams.HTTPStatusBadRequest,
			wantContains: "invalid protocol",
		},
		{
			name:         "user input validation",
			err:          utilserrors.NewUserInputValidationErr("name is required"),
			wantCode:     commonparams.HTTPStatusBadRequest,
			wantContains: "name is required",
		},
		{
			name:         "not found",
			err:          utilserrors.NewNotFoundErr("Host", nil),
			wantCode:     commonparams.HTTPStatusNotFound,
			wantContains: "Host",
		},
		{
			name:         "internal server error",
			err:          errors.New("database unavailable"),
			wantCode:     commonparams.HTTPStatusInternalServerError,
			wantContains: "failed to create host",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockOrch := factory.NewMockOrchestratorFactory(t)
			handler := NewHandler(mockOrch)

			mockOrch.On("CreateOntapHost", mock.Anything, mock.Anything).Return(nil, "", tc.err)

			result, err := handler.V1CreateHost(context.Background(), validReq, oasgenserver.V1CreateHostParams{})

			require.NoError(t, err)
			require.NotNil(t, result)

			switch res := result.(type) {
			case *oasgenserver.V1CreateHostBadRequest:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1CreateHostConflict:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1CreateHostNotFound:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1CreateHostInternalServerError:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			default:
				t.Fatalf("unexpected response type %T", result)
			}
		})
	}
}

func TestV1CreateHost_PasswordWrappedAsSecret(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	req := &oasgenserver.HostV1{
		Name:     "host-01",
		Username: "admin",
		Password: "top-secret",
	}

	mockOrch.On("CreateOntapHost", mock.Anything, mock.MatchedBy(func(p *commonparams.CreateHostParams) bool {
		return p != nil && p.Password == log.Secret("top-secret")
	})).Return(&datamodel.Host{
		Name:     "host-01",
		Username: "admin",
		Password: "encrypted",
		State:    datamodel.LifeCycleStateCreating,
	}, "job-uuid-123", nil)

	_, err := handler.V1CreateHost(context.Background(), req, oasgenserver.V1CreateHostParams{})
	require.NoError(t, err)
}

const testHostUUID = "host-uuid-123"

func sampleDatamodelHost() *datamodel.Host {
	now := time.Now().UTC()
	vendorID := "vendor-123"
	port := 443
	return &datamodel.Host{
		BaseModel: datamodel.BaseModel{
			UUID:      testHostUUID,
			CreatedAt: now,
			UpdatedAt: now,
		},
		ExternalUUID: "cluster-uuid-123",
		ExternalName: "vsim-cluster",
		Name:         "host-01",
		VendorID:     &vendorID,
		Port:         &port,
		Protocol:     "HTTPS",
		Username:     "admin",
		Password:     "encrypted-password",
		State:        datamodel.LifeCycleStateCreated,
		StateDetails: datamodel.LifeCycleStateCreatedDetails,
		Label:        "type=VSIM",
		IPAddress:    "10.0.0.1",
		Capacity:     68317667328,
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: true,
			PublicAccess:    false,
		},
	}
}

func TestV1DescribeHost_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	host := sampleDatamodelHost()
	mockOrch.On("GetHost", mock.Anything, testHostUUID).Return(host, nil)

	result, err := handler.V1DescribeHost(context.Background(), oasgenserver.V1DescribeHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	res, ok := result.(*oasgenserver.HostV1)
	require.True(t, ok)
	assert.Equal(t, "host-01", res.Name)
	assert.Equal(t, testHostUUID, host.UUID)
	assert.Equal(t, "cluster-uuid-123", res.ExternalClusterId.Value)
	assert.Equal(t, oasgenserver.HostV1LifeCycleStateCREATED, res.LifeCycleState.Value)
	assert.True(t, res.ProvisioningModel.IsSet())
	assert.Equal(t, oasgenserver.HostV1ProvisioningModelTHICK, res.ProvisioningModel.Value)
}

func TestV1DescribeHost_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("GetHost", mock.Anything, "missing-id").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	result, err := handler.V1DescribeHost(context.Background(), oasgenserver.V1DescribeHostParams{
		UUID: "missing-id",
	})

	require.NoError(t, err)
	notFound, ok := result.(*oasgenserver.V1DescribeHostNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(commonparams.HTTPStatusNotFound), notFound.Code)
	assert.Equal(t, "Host not found", notFound.Message)
}

func TestV1DescribeHost_NotImplemented(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("GetHost", mock.Anything, testHostUUID).
		Return(nil, utilserrors.NewNotImplementedYetErr())

	result, err := handler.V1DescribeHost(context.Background(), oasgenserver.V1DescribeHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1DescribeHostInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "not implemented yet")
}

func TestV1DescribeHost_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("GetHost", mock.Anything, testHostUUID).
		Return(nil, errors.New("database unavailable"))

	result, err := handler.V1DescribeHost(context.Background(), oasgenserver.V1DescribeHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1DescribeHostInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "failed to get host")
	assert.Contains(t, serverErr.Message, "database unavailable")
}

func TestV1UpdateHost_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	maintenanceMode := true
	publicAccess := false
	port := int32(443)
	protocol := oasgenserver.HostV1ProtocolHTTPS
	vendorID := "vendor-123"
	label := "type=VSIM"
	efficiencyFactor := 2.5

	req := &oasgenserver.HostV1{
		Name:              "host-01",
		Username:          "admin",
		Password:          "secret",
		ExternalClusterId: oasgenserver.NewOptString("cluster-uuid-123"),
		ExternalName:      oasgenserver.NewOptString("vsim-cluster"),
		VendorID:          oasgenserver.NewOptString(vendorID),
		MaintenanceMode:   oasgenserver.NewOptBool(maintenanceMode),
		PublicAccess:      oasgenserver.NewOptBool(publicAccess),
		EfficiencyFactor:  oasgenserver.NewOptFloat64(efficiencyFactor),
		IpAddress:         oasgenserver.NewOptString("10.0.0.1"),
		Port:              oasgenserver.NewOptInt32(port),
		Protocol:          protocol,
		Label:             oasgenserver.NewOptString(label),
	}

	updated := sampleDatamodelHost()
	updated.EfficiencyFactor = efficiencyFactor
	mockOrch.On("UpdateHost", mock.Anything, mock.MatchedBy(func(p *commonparams.UpdateHostParams) bool {
		return p != nil &&
			p.HostUUID == testHostUUID &&
			p.Name == "host-01" &&
			p.Username == "admin" &&
			p.Password == log.Secret("secret") &&
			p.ExternalUUID == "cluster-uuid-123" &&
			p.ExternalName == "vsim-cluster" &&
			p.VendorID != nil && *p.VendorID == vendorID &&
			p.MaintenanceMode != nil && *p.MaintenanceMode == maintenanceMode &&
			p.PublicAccess != nil && *p.PublicAccess == publicAccess &&
			p.EfficiencyFactor != nil && *p.EfficiencyFactor == efficiencyFactor &&
			p.IPAddress != nil && *p.IPAddress == "10.0.0.1" &&
			p.Port != nil && *p.Port == int(port) &&
			p.Protocol != nil && *p.Protocol == string(protocol) &&
			p.Label != nil && *p.Label == label
	})).Return(updated, nil)

	result, err := handler.V1UpdateHost(context.Background(), req, oasgenserver.V1UpdateHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	res, ok := result.(*oasgenserver.HostV1)
	require.True(t, ok)
	assert.Equal(t, "host-01", res.Name)
	assert.Equal(t, oasgenserver.HostV1ProtocolHTTPS, res.Protocol)
	assert.Equal(t, int64(68317667328), res.Capacity.Value)
	assert.True(t, res.EfficiencyFactor.IsSet())
	assert.Equal(t, efficiencyFactor, res.EfficiencyFactor.Value)
}

func TestV1UpdateHost_NilRequest(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	result, err := handler.V1UpdateHost(context.Background(), nil, oasgenserver.V1UpdateHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	badReq, ok := result.(*oasgenserver.V1UpdateHostBadRequest)
	require.True(t, ok)
	assert.Equal(t, float64(commonparams.HTTPStatusBadRequest), badReq.Code)
	assert.Equal(t, "request body is required", badReq.Message)
	mockOrch.AssertNotCalled(t, "UpdateHost", mock.Anything, mock.Anything)
}

func TestV1UpdateHost_ProvisioningModelImmutable(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)
	req := &oasgenserver.HostV1{
		Name:     "host-01",
		Username: "admin",
		Password: "secret",
		ProvisioningModel: oasgenserver.NewOptHostV1ProvisioningModel(
			oasgenserver.HostV1ProvisioningModelTHICK,
		),
	}
	mockOrch.On("UpdateHost", mock.Anything, mock.MatchedBy(func(params *commonparams.UpdateHostParams) bool {
		return params != nil && params.ProvisioningModelSet
	})).Return(nil, vsaerrors.NewVCPError(
		vsaerrors.ErrProvisioningModelImmutable,
		errors.New("provisioningModel is immutable and cannot be supplied in a host update"),
	))

	result, err := handler.V1UpdateHost(context.Background(), req, oasgenserver.V1UpdateHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	res, ok := result.(*oasgenserver.V1UpdateHostUnprocessableEntity)
	require.True(t, ok)
	assert.Equal(t, float64(vsaerrors.ErrProvisioningModelImmutable), res.Code)
	assert.Equal(t, "provisioningModel is immutable and cannot be supplied in a host update", res.Message)
	mockOrch.AssertExpectations(t)
}

func TestV1UpdateHost_ErrorResponses(t *testing.T) {
	validReq := &oasgenserver.HostV1{
		Name:     "host-01",
		Username: "admin",
		Password: "secret",
	}

	tests := []struct {
		name         string
		err          error
		wantCode     float64
		wantContains string
	}{
		{
			name:         "not implemented",
			err:          utilserrors.NewNotImplementedYetErr(),
			wantCode:     commonparams.HTTPStatusInternalServerError,
			wantContains: "not implemented yet",
		},
		{
			name: "database not found",
			err: vsaerrors.NewVCPError(
				vsaerrors.ErrDatabaseDataNotFoundError,
				errors.New("not found"),
			),
			wantCode:     commonparams.HTTPStatusNotFound,
			wantContains: "Host not found",
		},
		{
			name: "resource state conflict",
			err: vsaerrors.NewVCPError(
				vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf(`host %q already exists`, "host-01"),
			),
			wantCode:     commonparams.HTTPStatusConflict,
			wantContains: `host "host-01" already exists`,
		},
		{
			name:         "conflict from utils error",
			err:          utilserrors.NewConflictErr("IP address already assigned to host"),
			wantCode:     commonparams.HTTPStatusConflict,
			wantContains: "IP address already assigned to host",
		},
		{
			name:         "bad request",
			err:          utilserrors.NewBadRequestErr("invalid protocol"),
			wantCode:     commonparams.HTTPStatusBadRequest,
			wantContains: "invalid protocol",
		},
		{
			name:         "user input validation",
			err:          utilserrors.NewUserInputValidationErr("name is required"),
			wantCode:     commonparams.HTTPStatusBadRequest,
			wantContains: "name is required",
		},
		{
			name:         "not found from utils error",
			err:          utilserrors.NewNotFoundErr("Host", nil),
			wantCode:     commonparams.HTTPStatusNotFound,
			wantContains: "Host",
		},
		{
			name:         "internal server error",
			err:          errors.New("database unavailable"),
			wantCode:     commonparams.HTTPStatusInternalServerError,
			wantContains: "failed to update host",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockOrch := factory.NewMockOrchestratorFactory(t)
			handler := NewHandler(mockOrch)

			mockOrch.On("UpdateHost", mock.Anything, mock.Anything).Return(nil, tc.err)

			result, err := handler.V1UpdateHost(context.Background(), validReq, oasgenserver.V1UpdateHostParams{
				UUID: testHostUUID,
			})

			require.NoError(t, err)
			require.NotNil(t, result)

			switch res := result.(type) {
			case *oasgenserver.V1UpdateHostBadRequest:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1UpdateHostConflict:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1UpdateHostNotFound:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			case *oasgenserver.V1UpdateHostInternalServerError:
				assert.Equal(t, tc.wantCode, res.Code)
				assert.Contains(t, res.Message, tc.wantContains)
			default:
				t.Fatalf("unexpected response type %T", result)
			}
		})
	}
}

func TestV1DeleteHost_Success(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	deleted := sampleDatamodelHost()
	deleted.State = datamodel.LifeCycleStateDeleted
	deleted.StateDetails = datamodel.LifeCycleStateDeletedDetails

	mockOrch.On("DeleteHost", mock.Anything, testHostUUID).Return(deleted, nil)

	result, err := handler.V1DeleteHost(context.Background(), oasgenserver.V1DeleteHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	res, ok := result.(*oasgenserver.V1DeleteHostOK)
	require.True(t, ok)
	assert.Equal(t, "host-01", res.Name)
	assert.Equal(t, oasgenserver.HostV1LifeCycleStateDELETED, res.LifeCycleState.Value)
}

func TestV1DeleteHost_NotFound(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("DeleteHost", mock.Anything, "missing-id").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	result, err := handler.V1DeleteHost(context.Background(), oasgenserver.V1DeleteHostParams{
		UUID: "missing-id",
	})

	require.NoError(t, err)
	notFound, ok := result.(*oasgenserver.V1DeleteHostNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(commonparams.HTTPStatusNotFound), notFound.Code)
	assert.Equal(t, "Host not found", notFound.Message)
}

func TestV1DeleteHost_NotImplemented(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("DeleteHost", mock.Anything, testHostUUID).
		Return(nil, utilserrors.NewNotImplementedYetErr())

	result, err := handler.V1DeleteHost(context.Background(), oasgenserver.V1DeleteHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1DeleteHostInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "not implemented yet")
}

func TestV1DeleteHost_InternalServerError(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := NewHandler(mockOrch)

	mockOrch.On("DeleteHost", mock.Anything, testHostUUID).
		Return(nil, errors.New("delete failed"))

	result, err := handler.V1DeleteHost(context.Background(), oasgenserver.V1DeleteHostParams{
		UUID: testHostUUID,
	})

	require.NoError(t, err)
	serverErr, ok := result.(*oasgenserver.V1DeleteHostInternalServerError)
	require.True(t, ok)
	assert.Contains(t, serverErr.Message, "failed to delete host")
	assert.Contains(t, serverErr.Message, "delete failed")
}

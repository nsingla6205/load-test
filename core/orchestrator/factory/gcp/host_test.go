package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

const testHostOnboardJobUUID = "job-uuid-123"

func newHostCreateOrchestrator(t *testing.T, mockStorage *database.MockStorage) (*GCPOrchestrator, *workflowenginemock.MockTemporalTestClient) {
	t.Helper()
	temporal := workflowenginemock.NewMockTemporalTestClient(t)
	return &GCPOrchestrator{storage: mockStorage, temporal: temporal}, temporal
}

func expectHostOnboardJobAndWorkflow(
	t *testing.T,
	mockStorage *database.MockStorage,
	temporal *workflowenginemock.MockTemporalTestClient,
) {
	t.Helper()
	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job != nil &&
			job.Type == string(datamodel.JobTypeCreateHost) &&
			job.State == string(datamodel.JobsStateNEW)
	})).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: testHostOnboardJobUUID},
		WorkflowID: "host-onboard-workflow-id",
	}, nil)
	temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)
}

func validCreateHostParams() *commonparams.CreateHostParams {
	protocol := datamodel.ExternalClusterProtocolInsecureHTTPS
	ip := "10.0.0.1"
	return &commonparams.CreateHostParams{
		Name:      "host-01",
		Username:  "admin",
		Password:  log.Secret("secret"),
		IPAddress: &ip,
		Protocol:  &protocol,
	}
}

func stubOntapProviderForHost(
	t *testing.T,
	setup func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, mockStorage *ontaprest.MockStorageClient),
) {
	t.Helper()

	original := getOntapProviderForHost
	t.Cleanup(func() { getOntapProviderForHost = original })

	mockProvider := vsa.NewMockProvider(t)
	mockREST := ontaprest.NewMockRESTClient(t)
	mockCluster := ontaprest.NewMockClusterClient(t)
	mockStorage := ontaprest.NewMockStorageClient(t)

	setup(mockProvider, mockREST, mockCluster, mockStorage)

	getOntapProviderForHost = func(context.Context, vsa.ProviderDetails) vsa.Provider {
		return mockProvider
	}
}

func mockClusterGetResponse() *ontaprest.Cluster {
	clusterUUID := strfmt.UUID("11111111-1111-1111-1111-111111111111")
	clusterName := "vsim-cluster"
	clusterVersion := "9.15.1"
	generation := int64(9)
	major := int64(15)
	minor := int64(1)
	return &ontaprest.Cluster{
		Cluster: models.Cluster{
			UUID: &clusterUUID,
			Name: &clusterName,
			Version: &models.ClusterInlineVersion{
				Full:       &clusterVersion,
				Generation: &generation,
				Major:      &major,
				Minor:      &minor,
			},
		},
	}
}

func TestCreateHost_ValidationErrors(t *testing.T) {
	orch := &GCPOrchestrator{storage: database.NewMockStorage(t)}

	tests := []struct {
		name   string
		params *commonparams.CreateHostParams
	}{
		{name: "nil params", params: nil},
		{
			name: "missing name",
			params: &commonparams.CreateHostParams{
				Username: "admin",
				Password: log.Secret("secret"),
			},
		},
		{
			name: "missing username",
			params: &commonparams.CreateHostParams{
				Name:     "host-01",
				Password: log.Secret("secret"),
			},
		},
		{
			name: "missing password",
			params: &commonparams.CreateHostParams{
				Name:     "host-01",
				Username: "admin",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := orch.CreateOntapHost(context.Background(), tc.params)
			require.Error(t, err)
			assert.True(t, utilserrors.IsBadRequestErr(err))
		})
	}
}

func TestCreateHost_InvalidProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	protocol := "NFS"
	params := validCreateHostParams()
	params.Protocol = &protocol

	_, _, err := orch.CreateOntapHost(context.Background(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	mockStorage.AssertNotCalled(t, "CountHostsWithIPAddress", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateHost_InvalidProvisioningModel(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	params := validCreateHostParams()
	unknown := datamodel.ProvisioningModel("UNKNOWN")
	params.ProvisioningModel = &unknown

	_, _, err := orch.CreateOntapHost(context.Background(), params)

	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unknown provisioning model")
	mockStorage.AssertNotCalled(t, "CountHostsWithIPAddress", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateHost_EncryptPasswordError(t *testing.T) {
	originalEncrypt := utils.EncryptPassword
	t.Cleanup(func() { utils.EncryptPassword = originalEncrypt })
	utils.EncryptPassword = func(log.Secret) (*string, error) {
		return nil, errors.New("encrypt failed")
	}

	orch := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt failed")
}

func TestCreateHost_CountHostsWithIPAddressError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), errors.New("db read failed"))

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed checking host ip uniqueness")
}

func TestCreateHost_IPAlreadyAssigned(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(1), nil)

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "IP address already assigned")
}

func TestCreateHost_UsesNameAsIPAddressWhenUnset(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch, temporal := newHostCreateOrchestrator(t, mockStorage)

	params := validCreateHostParams()
	params.IPAddress = nil
	params.Name = "10.0.0.50"

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.50", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.IPAddress == "10.0.0.50"
	})).Return(&datamodel.Host{Name: "10.0.0.50", State: datamodel.LifeCycleStateCreating}, nil)
	expectHostOnboardJobAndWorkflow(t, mockStorage, temporal)

	_, jobUUID, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
	assert.Equal(t, testHostOnboardJobUUID, jobUUID)
}

func TestCreateHost_StorageCreateError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).Return(nil, errors.New("db insert failed"))

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create host")
}

func TestCreateHost_DuplicateIPOnInsert(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).Return(nil, vsaerrors.NewVCPError(
		vsaerrors.ErrResourceStateConflictError,
		utilserrors.NewConflictErr("IP address already assigned to host"),
	))

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestCreateHost_CreateJobError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch, _ := newHostCreateOrchestrator(t, mockStorage)

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).
		Return(&datamodel.Host{Name: "host-01", State: datamodel.LifeCycleStateCreating}, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).
		Return(nil, errors.New("job create failed"))

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create host onboard job")
}

func TestCreateHost_TemporalNotConfigured(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).
		Return(&datamodel.Host{Name: "host-01", State: datamodel.LifeCycleStateCreating}, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: testHostOnboardJobUUID},
		WorkflowID: "host-onboard-workflow-id",
	}, nil)

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal client is not configured")
}

func TestCreateHost_WorkflowStartFailure(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch, temporal := newHostCreateOrchestrator(t, mockStorage)

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).
		Return(&datamodel.Host{
			BaseModel: datamodel.BaseModel{UUID: "host-uuid-123"},
			Name:      "host-01",
			State:     datamodel.LifeCycleStateCreating,
		}, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: testHostOnboardJobUUID},
		WorkflowID: "host-onboard-workflow-id",
	}, nil)
	temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("workflow start failed"))
	mockStorage.On("UpdateJob", mock.Anything, testHostOnboardJobUUID, string(datamodel.JobsStateERROR), 0, mock.Anything).
		Return(nil)
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.State == datamodel.LifeCycleStateError
	})).Return(&datamodel.Host{}, nil)

	_, _, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start host onboard workflow")
}

func TestCreateHost_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch, temporal := newHostCreateOrchestrator(t, mockStorage)
	params := validCreateHostParams()
	label := "type=VSIM"
	params.Label = &label
	maintenanceMode := true
	params.MaintenanceMode = &maintenanceMode
	efficiencyFactor := 1.4
	params.EfficiencyFactor = &efficiencyFactor
	provisioningModel := datamodel.ProvisioningModelThin
	params.ProvisioningModel = &provisioningModel

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		if host.Name != "host-01" || host.Username != "admin" || host.IPAddress != "10.0.0.1" {
			return false
		}
		if host.State != datamodel.LifeCycleStateCreating || host.StateDetails != datamodel.LifeCycleStateCreatingDetails {
			return false
		}
		if host.ExternalUUID != "" || host.ExternalName != "" || host.Capacity != 0 {
			return false
		}
		if host.Attributes == nil || !host.Attributes.MaintenanceMode {
			return false
		}
		if host.EfficiencyFactor != efficiencyFactor {
			return false
		}
		if host.ProvisioningModel == nil || *host.ProvisioningModel != datamodel.ProvisioningModelThin {
			return false
		}
		if host.Password == "" || host.Password == "secret" {
			return false
		}
		decrypted, err := utils.DecryptPassword(log.Secret(host.Password))
		return err == nil && decrypted != nil && *decrypted == "secret"
	})).Return(&datamodel.Host{
		Name:              "host-01",
		State:             datamodel.LifeCycleStateCreating,
		ProvisioningModel: &provisioningModel,
	}, nil)
	expectHostOnboardJobAndWorkflow(t, mockStorage, temporal)

	got, jobUUID, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, testHostOnboardJobUUID, jobUUID)
	assert.Equal(t, datamodel.LifeCycleStateCreating, got.State)
	require.NotNil(t, got.ProvisioningModel)
	assert.Equal(t, datamodel.ProvisioningModelThin, *got.ProvisioningModel)
}

func TestCreateHost_DefaultEfficiencyFactor(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch, temporal := newHostCreateOrchestrator(t, mockStorage)
	params := validCreateHostParams()

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.EfficiencyFactor == datamodel.DefaultHostEfficiencyFactor &&
			host.ProvisioningModel != nil &&
			*host.ProvisioningModel == datamodel.ProvisioningModelThick
	})).Return(&datamodel.Host{
		Name:              "host-01",
		EfficiencyFactor:  datamodel.DefaultHostEfficiencyFactor,
		State:             datamodel.LifeCycleStateCreating,
		ProvisioningModel: func() *datamodel.ProvisioningModel { m := datamodel.ProvisioningModelThick; return &m }(),
	}, nil)
	expectHostOnboardJobAndWorkflow(t, mockStorage, temporal)

	got, _, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, got.EfficiencyFactor)
	require.NotNil(t, got.ProvisioningModel)
	assert.Equal(t, datamodel.ProvisioningModelThick, *got.ProvisioningModel)
}

func existingStoredHost() *datamodel.Host {
	protocol := datamodel.ExternalClusterProtocolInsecureHTTPS
	port := 443
	encrypted, err := utils.EncryptPassword(log.Secret("stored-secret"))
	if err != nil {
		panic(err)
	}
	return &datamodel.Host{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "host-uuid-123",
		},
		Name:      "host-01",
		Username:  "admin",
		Password:  *encrypted,
		Protocol:  protocol,
		Port:      &port,
		IPAddress: "10.0.0.1",
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: false,
			PublicAccess:    false,
		},
	}
}

func validUpdateHostParams() *commonparams.UpdateHostParams {
	return &commonparams.UpdateHostParams{
		HostUUID: "host-uuid-123",
		CreateHostParams: commonparams.CreateHostParams{
			Name:     "host-01",
			Username: "admin",
			Password: log.Secret("new-secret"),
		},
	}
}

func setupSuccessfulOntapClusterGet(
	mockProvider *vsa.MockProvider,
	mockREST *ontaprest.MockRESTClient,
	mockCluster *ontaprest.MockClusterClient,
	_ *ontaprest.MockStorageClient,
) {
	mockProvider.On("CreateRESTClient").Return(mockREST, nil)
	mockREST.On("Cluster").Return(mockCluster)
	mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockClusterGetResponse(), nil)
}

func TestListHosts_DelegatesToStorage(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	hosts := []*datamodel.Host{{Name: "host-01"}}
	mockStorage.On("ListHosts", mock.Anything).Return(hosts, nil)

	got, err := orch.ListHosts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, hosts, got)
}

func TestGetHost_DelegatesToStorage(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	got, err := orch.GetHost(context.Background(), "host-uuid-123")
	require.NoError(t, err)
	assert.Equal(t, host, got)
}

func TestGetHost_PropagatesStorageError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "missing-id").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	_, err := orch.GetHost(context.Background(), "missing-id")
	require.Error(t, err)
}

func TestDeleteHost_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	deleted := *host
	deleted.State = datamodel.LifeCycleStateDeleted

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	mockStorage.On("DeleteHost", mock.Anything, host).Return(&deleted, nil)

	got, err := orch.DeleteHost(context.Background(), "host-uuid-123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, datamodel.LifeCycleStateDeleted, got.State)
}

func TestDeleteHost_GetHostError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "missing-id").
		Return(nil, errors.New("not found"))

	_, err := orch.DeleteHost(context.Background(), "missing-id")
	require.Error(t, err)
	mockStorage.AssertNotCalled(t, "DeleteHost", mock.Anything, mock.Anything)
}

func TestDeleteHost_DeleteError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	mockStorage.On("DeleteHost", mock.Anything, host).Return(nil, errors.New("delete failed"))

	_, err := orch.DeleteHost(context.Background(), "host-uuid-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestUpdateHost_HostNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
}

func TestUpdateHost_ProvisioningModelImmutable(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	params := validUpdateHostParams()
	params.ProvisioningModelSet = true

	_, err := orch.UpdateHost(context.Background(), params)

	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrProvisioningModelImmutable))
	mockStorage.AssertNotCalled(t, "GetHost", mock.Anything, mock.Anything)
}

func TestUpdateHost_InvalidProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	protocol := "NFS"
	params := validUpdateHostParams()
	params.Protocol = &protocol

	_, err := orch.UpdateHost(context.Background(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestUpdateHost_EncryptPasswordError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	originalEncrypt := utils.EncryptPassword
	t.Cleanup(func() { utils.EncryptPassword = originalEncrypt })
	utils.EncryptPassword = func(log.Secret) (*string, error) {
		return nil, errors.New("encrypt failed")
	}

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt failed")
}

func TestUpdateHost_CreateRESTClientError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, _ *ontaprest.MockRESTClient, _ *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(nil, errors.New("connection refused"))
	})

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to connect to host")
}

func TestUpdateHost_ClusterGetError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(mockREST, nil)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(nil, errors.New("cluster get failed"))
	})

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to fetch cluster details")
}

func TestUpdateHost_StorageUpdateError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).Return(nil, errors.New("update failed"))

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestUpdateHost_SuccessWithNewPassword(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	storedPassword := host.Password
	params := validUpdateHostParams()
	label := "type=UPDATED"
	vendorID := "vendor-updated"
	maintenanceMode := true
	publicAccess := true
	ip := "10.0.0.2"
	port := 8443
	protocol := datamodel.ExternalClusterProtocolHTTPS
	params.Label = &label
	params.VendorID = &vendorID
	params.MaintenanceMode = &maintenanceMode
	params.PublicAccess = &publicAccess
	params.IPAddress = &ip
	params.Port = &port
	params.Protocol = &protocol
	params.ExternalUUID = "external-uuid-updated"
	params.ExternalName = "cluster-updated"

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updated *datamodel.Host) bool {
		if updated.Name != "host-01" || updated.Username != "admin" || updated.IPAddress != "10.0.0.2" {
			return false
		}
		if updated.Protocol != protocol || updated.Port == nil || *updated.Port != port {
			return false
		}
		if updated.Label != label || updated.VendorID == nil || *updated.VendorID != vendorID {
			return false
		}
		if updated.Attributes == nil || !updated.Attributes.MaintenanceMode || !updated.Attributes.PublicAccess {
			return false
		}
		if updated.ExternalUUID != "11111111-1111-1111-1111-111111111111" || updated.ExternalName != "vsim-cluster" {
			return false
		}
		if updated.Password == storedPassword || updated.Password == "new-secret" {
			return false
		}
		decrypted, err := utils.DecryptPassword(log.Secret(updated.Password))
		return err == nil && decrypted != nil && *decrypted == "new-secret"
	})).Return(&datamodel.Host{Name: "host-01"}, nil)

	got, err := orch.UpdateHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestUpdateHost_SuccessWithPasswordMask(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	storedPassword := host.Password
	params := validUpdateHostParams()
	params.Password = log.Secret(log.PasswordMask)

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updated *datamodel.Host) bool {
		return updated.Password == storedPassword
	})).Return(&datamodel.Host{Name: "host-01", Password: storedPassword}, nil)

	got, err := orch.UpdateHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestUpdateHost_UsesInsecureSkipVerifyForInsecureProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	var captured vsa.ProviderDetails
	original := getOntapProviderForHost
	t.Cleanup(func() { getOntapProviderForHost = original })
	getOntapProviderForHost = func(_ context.Context, details vsa.ProviderDetails) vsa.Provider {
		captured = details
		mockProvider := vsa.NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		setupSuccessfulOntapClusterGet(mockProvider, mockREST, mockCluster, nil)
		return mockProvider
	}
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).Return(&datamodel.Host{Name: "host-01"}, nil)

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.NoError(t, err)
	assert.True(t, captured.InsecureSkipVerify)
	assert.Equal(t, "new-secret", captured.Password)
}

func TestApplyUpdateParamsToHost(t *testing.T) {
	t.Run("applies full spec and preserves existing protocol when unset", func(t *testing.T) {
		port := 443
		host := &datamodel.Host{
			Name:      "old-name",
			Username:  "old-user",
			Protocol:  datamodel.ExternalClusterProtocolInsecureHTTPS,
			Port:      &port,
			IPAddress: "10.0.0.1",
			Attributes: &datamodel.HostAttributes{
				MaintenanceMode: false,
				PublicAccess:    false,
			},
		}
		label := "type=VSIM"
		vendorID := "vendor-123"
		maintenanceMode := true
		publicAccess := true
		ip := "10.0.0.2"
		newPort := 8443
		protocol := datamodel.ExternalClusterProtocolHTTPS

		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:            "host-01",
				Username:        "admin",
				Password:        log.Secret("secret"),
				ExternalUUID:    "external-uuid",
				ExternalName:    "external-name",
				VendorID:        &vendorID,
				Label:           &label,
				MaintenanceMode: &maintenanceMode,
				PublicAccess:    &publicAccess,
				IPAddress:       &ip,
				Port:            &newPort,
				Protocol:        &protocol,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "host-01", host.Name)
		assert.Equal(t, "admin", host.Username)
		assert.Equal(t, protocol, host.Protocol)
		require.NotNil(t, host.Port)
		assert.Equal(t, 8443, *host.Port)
		assert.Equal(t, "10.0.0.2", host.IPAddress)
		assert.Equal(t, "external-uuid", host.ExternalUUID)
		assert.Equal(t, "external-name", host.ExternalName)
		assert.Equal(t, label, host.Label)
		require.NotNil(t, host.VendorID)
		assert.Equal(t, vendorID, *host.VendorID)
		require.NotNil(t, host.Attributes)
		assert.True(t, host.Attributes.MaintenanceMode)
		assert.True(t, host.Attributes.PublicAccess)
	})

	t.Run("uses host name when stored and request ip are empty", func(t *testing.T) {
		host := &datamodel.Host{}
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:     "10.0.0.50",
				Username: "admin",
				Password: log.Secret("secret"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.50", host.IPAddress)
	})

	t.Run("uses request ip when provided", func(t *testing.T) {
		host := &datamodel.Host{IPAddress: "10.0.0.1"}
		ip := "10.0.0.2"
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:      "host-01",
				Username:  "admin",
				Password:  log.Secret("secret"),
				IPAddress: &ip,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.2", host.IPAddress)
	})

	t.Run("returns bad request for invalid protocol", func(t *testing.T) {
		host := &datamodel.Host{Protocol: datamodel.ExternalClusterProtocolHTTPS}
		protocol := "NFS"
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:     "host-01",
				Username: "admin",
				Password: log.Secret("secret"),
				Protocol: &protocol,
			},
		})
		require.Error(t, err)
		assert.True(t, utilserrors.IsBadRequestErr(err))
	})

	t.Run("updates efficiency factor when set", func(t *testing.T) {
		host := &datamodel.Host{
			EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor,
		}
		efficiencyFactor := 2.5
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:             "host-01",
				Username:         "admin",
				Password:         log.Secret("secret"),
				EfficiencyFactor: &efficiencyFactor,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, efficiencyFactor, host.EfficiencyFactor)
	})
}

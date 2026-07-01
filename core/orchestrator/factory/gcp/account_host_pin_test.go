package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

const (
	testOnboardJobUUID = "d3456789-01ab-cdef-0123-456789abcdef"
	testAccountUUID    = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
)

func expectPinAccountHostsWorkflowStart(temporal *workflowEngineMock.MockTemporalTestClient) {
	temporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
}

func expectOnboardJobCreate(mockStorage *database.MockStorage) {
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: testOnboardJobUUID},
		WorkflowID: testOnboardJobUUID,
	}, nil)
}

func expectAccountLookup(mockStorage *database.MockStorage, accountName string) {
	mockStorage.On("GetAccount", mock.Anything, accountName).Return(&datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 42, UUID: testAccountUUID},
		Name:      accountName,
	}, nil)
}

func expectNoActivePinAccountHostsJob(mockStorage *database.MockStorage) {
	mockStorage.On("GetActivePinAccountHostsJobByAccount", mock.Anything, int64(42)).
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("job", nil)))
}

func expectPinAccountHostsReconcilePreflight(mockStorage *database.MockStorage) {
	expectNoActivePinAccountHostsJob(mockStorage)
	mockStorage.On("ListPinnedAccountHostsByAccount", mock.Anything, int64(42)).
		Return([]*datamodel.AccountHost{}, nil)
}

func TestPinAccountHosts_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	expectAccountLookup(mockStorage, "123456789")
	expectPinAccountHostsReconcilePreflight(mockStorage)
	expectOnboardJobCreate(mockStorage)
	expectPinAccountHostsWorkflowStart(temporal)
	orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}

	hostUUIDs := []string{
		"9760acf5-4638-11e7-9bdb-020073ca3333",
		"7a8b1c0e-1234-5678-90ab-cdef12345678",
	}

	mockStorage.On("GetHostsByUUIDs", mock.Anything, hostUUIDs).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: hostUUIDs[0]}, Name: "host-us-central1"},
			{BaseModel: datamodel.BaseModel{UUID: hostUUIDs[1]}, Name: "host-us-central1"},
		}, nil)

	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), hostUUIDs).
		Return([]*datamodel.AccountHost{}, nil)

	mockStorage.On("CreateAccountHost", mock.Anything, mock.MatchedBy(func(p *datamodel.AccountHost) bool {
		return p != nil && p.AccountID == 42 && !p.Pinned && p.OntapModeCredentials == nil
	})).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: "account-host-1"},
		HostID:    hostUUIDs[0],
		Pinned:    false,
	}, nil).Once()

	mockStorage.On("CreateAccountHost", mock.Anything, mock.MatchedBy(func(p *datamodel.AccountHost) bool {
		return p.HostID == hostUUIDs[1] && !p.Pinned
	})).Return(&datamodel.AccountHost{
		BaseModel: datamodel.BaseModel{UUID: "account-host-2"},
		HostID:    hostUUIDs[1],
		Pinned:    false,
	}, nil).Once()

	response, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "123456789",
		HostUUIDs:     hostUUIDs,
	})
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, string(datamodel.JobsStatePROCESSING), response.Status)
	assert.Equal(t, testOnboardJobUUID, response.JobID)
}

func TestPinAccountHosts_AccountNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	notFound := customerrors.NewNotFoundErr("account", nil)
	mockStorage.On("GetAccount", mock.Anything, "p1").Return(nil, notFound)
	mockStorage.On("CreateAccount", mock.Anything, mock.Anything).Return(nil, notFound)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"uuid-1"},
	})
	require.Error(t, err)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func TestPinAccountHosts_EmptyHostsUnpinsAll(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	expectAccountLookup(mockStorage, "p1")
	expectNoActivePinAccountHostsJob(mockStorage)
	orch := &GCPOrchestrator{storage: mockStorage}

	pinned := []*datamodel.AccountHost{
		{BaseModel: datamodel.BaseModel{UUID: "ah-1"}, HostID: "host-1", Pinned: true},
	}
	mockStorage.On("ListPinnedAccountHostsByAccount", mock.Anything, int64(42)).Return(pinned, nil)
	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), []string{}).
		Return([]*datamodel.AccountHost{}, nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, "ah-1", false).Return(nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: testOnboardJobUUID},
		WorkflowID: testOnboardJobUUID,
	}, nil)
	mockStorage.On("UpdateJob", mock.Anything, testOnboardJobUUID, string(datamodel.JobsStateDONE), 0, "").
		Return(nil)

	response, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{},
	})
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, string(datamodel.JobsStateDONE), response.Status)
}

func TestPinAccountHosts_TemporalNotConfigured(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	expectAccountLookup(mockStorage, "p1")
	expectPinAccountHostsReconcilePreflight(mockStorage)
	orch := &GCPOrchestrator{storage: mockStorage, temporal: nil}

	hostUUID := "9760acf5-4638-11e7-9bdb-020073ca3333"
	mockStorage.On("GetHostsByUUIDs", mock.Anything, []string{hostUUID}).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: hostUUID}, Name: "host-1"},
		}, nil)
	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), []string{hostUUID}).
		Return([]*datamodel.AccountHost{}, nil)
	mockStorage.On("CreateAccountHost", mock.Anything, mock.Anything).
		Return(&datamodel.AccountHost{
			BaseModel: datamodel.BaseModel{UUID: "account-host-1"},
			HostID:    hostUUID,
		}, nil)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{hostUUID},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "temporal client is not configured")
	mockStorage.AssertNotCalled(t, "CreateJob", mock.Anything, mock.Anything)
}

func TestPinAccountHosts_HostsMissing(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	expectAccountLookup(mockStorage, "p1")
	expectNoActivePinAccountHostsJob(mockStorage)

	mockStorage.On("GetHostsByUUIDs", mock.Anything, mock.Anything).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: "uuid-1"}, Name: "host-1"},
		}, nil)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"uuid-1", "uuid-missing"},
	})
	require.Error(t, err)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func TestPinAccountHosts_StorageLookupError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	expectAccountLookup(mockStorage, "p1")
	expectNoActivePinAccountHostsJob(mockStorage)

	mockStorage.On("GetHostsByUUIDs", mock.Anything, mock.Anything).
		Return(nil, errors.New("boom"))

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"uuid-1"},
	})
	require.Error(t, err)
}

func TestPinAccountHosts_RePinWithCredentialsSync(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	expectAccountLookup(mockStorage, "p1")
	expectNoActivePinAccountHostsJob(mockStorage)
	expectOnboardJobCreate(mockStorage)
	mockStorage.On("UpdateJob", mock.Anything, testOnboardJobUUID, string(datamodel.JobsStateDONE), 0, "").
		Return(nil)
	orch := &GCPOrchestrator{storage: mockStorage}

	hostUUID := "uuid-1"
	mockStorage.On("GetHostsByUUIDs", mock.Anything, []string{hostUUID}).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: hostUUID}, Name: "host-1"},
		}, nil)
	mockStorage.On("ListPinnedAccountHostsByAccount", mock.Anything, int64(42)).Return([]*datamodel.AccountHost{}, nil)
	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), []string{hostUUID}).
		Return([]*datamodel.AccountHost{{
			BaseModel:            datamodel.BaseModel{UUID: "ah-1"},
			HostID:               hostUUID,
			Pinned:               false,
			OntapModeCredentials: &datamodel.ExpertModeCredential{
				AuthType: env.USERNAME_PWD_SEC_MGR,
				Username: "u_gadmin",
				SecretID: "secret",
			},
		}}, nil)
	mockStorage.On("SetAccountHostPinned", mock.Anything, "ah-1", true).Return(nil)

	response, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{hostUUID},
	})
	require.NoError(t, err)
	assert.Equal(t, string(datamodel.JobsStateDONE), response.Status)
}

func TestPinAccountHosts_HostsAcrossRegionsAllowed(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	expectAccountLookup(mockStorage, "p1")
	expectNoActivePinAccountHostsJob(mockStorage)
	expectOnboardJobCreate(mockStorage)
	expectPinAccountHostsWorkflowStart(temporal)
	orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}

	mockStorage.On("GetHostsByUUIDs", mock.Anything, []string{"uuid-1", "uuid-2"}).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: "uuid-1"}, Name: "host-us-central1"},
			{BaseModel: datamodel.BaseModel{UUID: "uuid-2"}, Name: "host-us-east1"},
		}, nil)
	mockStorage.On("ListPinnedAccountHostsByAccount", mock.Anything, int64(42)).Return([]*datamodel.AccountHost{}, nil)
	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), []string{"uuid-1", "uuid-2"}).
		Return([]*datamodel.AccountHost{}, nil)
	mockStorage.On("CreateAccountHost", mock.Anything, mock.Anything).
		Return(&datamodel.AccountHost{BaseModel: datamodel.BaseModel{UUID: "account-host-1"}}, nil)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"uuid-1", "uuid-2"},
	})
	require.NoError(t, err)
}

func TestPinAccountHosts_ActiveJobReturns409(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	expectAccountLookup(mockStorage, "p1")
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetActivePinAccountHostsJobByAccount", mock.Anything, int64(42)).
		Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: testOnboardJobUUID},
			State:     string(datamodel.JobsStatePROCESSING),
		}, nil)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"uuid-1"},
	})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestPinAccountHosts_PreservesInputOrder(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	expectAccountLookup(mockStorage, "p1")
	expectPinAccountHostsReconcilePreflight(mockStorage)
	expectOnboardJobCreate(mockStorage)
	expectPinAccountHostsWorkflowStart(temporal)
	orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}

	mockStorage.On("GetHostsByUUIDs", mock.Anything, []string{"b", "a", "c"}).
		Return([]*datamodel.Host{
			{BaseModel: datamodel.BaseModel{UUID: "a"}, Name: "host-1"},
			{BaseModel: datamodel.BaseModel{UUID: "b"}, Name: "host-1"},
			{BaseModel: datamodel.BaseModel{UUID: "c"}, Name: "host-1"},
		}, nil)

	mockStorage.On("GetAccountHostsByAccountAndHostUUIDs", mock.Anything, int64(42), []string{"b", "a", "c"}).
		Return([]*datamodel.AccountHost{}, nil)

	var createOrder []string
	mockStorage.On("CreateAccountHost", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			p := args.Get(1).(*datamodel.AccountHost)
			createOrder = append(createOrder, p.HostID)
		}).
		Return(&datamodel.AccountHost{BaseModel: datamodel.BaseModel{UUID: "account-host-1"}}, nil)

	_, err := orch.PinAccountHosts(context.Background(), &commonparams.PinAccountHostsParams{
		AccountName: "p1",
		HostUUIDs:     []string{"b", "a", "c"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a", "c"}, createOrder)
}

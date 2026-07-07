package gcp

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func dedicatedPoolView(accountID int64, externalName string, serviceLevel string) *datamodel.PoolView {
	return &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "pool-uuid"},
			AccountID:      accountID,
			State:          datamodel.LifeCycleStateREADY,
			ServiceLevel:   serviceLevel,
			ClusterDetails: datamodel.ClusterDetails{ExternalName: externalName},
		},
	}
}

func TestGetDedicatedClusterPoolCreds_UsesAccountHostUsername(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "STANDARD"), nil)
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(&database.HostCredential{
			Host: &datamodel.Host{
				BaseModel:    datamodel.BaseModel{UUID: "host-uuid"},
				ExternalName: "cluster-a",
				IPAddress:    "10.0.0.1",
			},
			Username: "account-host-user",
			SecretId: "secret-a",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "account-host-user", creds.Username)
	assert.Equal(t, "secret-a", creds.SecretID)
	assert.Equal(t, env.USERNAME_PWD_SEC_MGR, creds.AuthType)
	assert.Equal(t, "STANDARD", creds.ServiceLevel)
	require.Len(t, creds.OntapEndpoints, 1)
	assert.Equal(t, "10.0.0.1", creds.OntapEndpoints[0].IP)
}

func TestGetDedicatedClusterPoolCreds_HeaderClusterOverridesPoolCluster(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	// Pool's own cluster is "cluster-a", but the request carries header "cluster-b".
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "STANDARD"), nil)
	// The pinned-host lookup must use the header cluster ("cluster-b"), not the pool's.
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-b").
		Return(&database.HostCredential{
			Host: &datamodel.Host{
				BaseModel:    datamodel.BaseModel{UUID: "host-uuid"},
				ExternalName: "cluster-b",
				IPAddress:    "10.0.0.2",
			},
			Username: "account-host-user",
			SecretId: "secret-b",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "cluster-b", "")

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "account-host-user", creds.Username)
	assert.Equal(t, "secret-b", creds.SecretID)
	assert.Equal(t, "STANDARD", creds.ServiceLevel)
	require.Len(t, creds.OntapEndpoints, 1)
	assert.Equal(t, "10.0.0.2", creds.OntapEndpoints[0].IP)
}

func TestGetDedicatedClusterPoolCreds_FlexPool_WithHeader_UsesPinnedHost(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "FLEX"), nil)
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(&database.HostCredential{
			Host: &datamodel.Host{
				BaseModel:    datamodel.BaseModel{UUID: "host-uuid"},
				ExternalName: "cluster-a",
				IPAddress:    "10.0.0.1",
			},
			Username: "account-host-user",
			SecretId: "secret-a",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "cluster-a", "")

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "account-host-user", creds.Username)
	assert.Equal(t, "secret-a", creds.SecretID)
	assert.Equal(t, "FLEX", creds.ServiceLevel)
}

func TestGetDedicatedClusterPoolCreds_FlexPool_NoHeader_UsesPoolCluster(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "FLEX"), nil)
	// No header: pinned-host lookup falls back to the pool's own cluster name.
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(&database.HostCredential{
			Host: &datamodel.Host{
				BaseModel:    datamodel.BaseModel{UUID: "host-uuid"},
				ExternalName: "cluster-a",
				IPAddress:    "10.0.0.1",
			},
			Username: "account-host-user",
			SecretId: "secret-a",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "account-host-user", creds.Username)
	assert.Equal(t, "secret-a", creds.SecretID)
	assert.Equal(t, "FLEX", creds.ServiceLevel)
}

func TestGetDedicatedClusterPoolCreds_GetAccountError_ReturnsError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(nil, stderrors.New("account lookup failed"))

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "account lookup failed")
}

func TestGetDedicatedClusterPoolCreds_GetPoolError_ReturnsError(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(nil, stderrors.New("pool lookup failed"))

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "pool lookup failed")
}

func TestGetDedicatedClusterPoolCreds_NoClusterName_ReturnsBadRequest(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	// Dedicated pool with no cluster name on the pool and no header override.
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "", "STANDARD"), nil)

	o := &GCPOrchestrator{storage: mockStorage}
	_, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.Error(t, err)
	var vcpErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &vcpErr)
	assert.True(t, vcpErr.IsError(vsaerrors.ErrMissingRequiredInputError))
}

func TestGetDedicatedClusterPoolCreds_HostCredentialLookupError_ReturnsError(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "STANDARD"), nil)
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(nil, stderrors.New("host credential lookup failed"))

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "host credential lookup failed")
}

func TestGetDedicatedClusterPoolCreds_UnregisteredCluster_ReturnsBadRequest(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(dedicatedPoolView(account.ID, "cluster-a", "STANDARD"), nil)
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(nil, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	_, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", "")

	require.Error(t, err)
	var vcpErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &vcpErr)
	assert.True(t, vcpErr.IsError(vsaerrors.ErrMissingRequiredInputError))
}

func TestGetDedicatedClusterPoolCreds_AdminUser_UsesHostCredentials(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}
	poolView := dedicatedPoolView(account.ID, "cluster-a", "STANDARD")

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(poolView, nil)
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(&database.HostCredential{
			Host: &datamodel.Host{
				BaseModel:    datamodel.BaseModel{UUID: "host-uuid"},
				ExternalName: "cluster-a",
				IPAddress:    "10.0.0.1",
				Username:     "cluster-admin",
				Password:     "cluster-password",
			},
			Username: "account-host-user",
			SecretId: "secret-a",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", AdminUserName)

	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "cluster-admin", creds.Username)
	assert.Equal(t, "cluster-password", creds.Password)
	assert.Equal(t, env.USERNAME_PWD, creds.AuthType)
	assert.Equal(t, "STANDARD", creds.ServiceLevel)
	require.Len(t, creds.OntapEndpoints, 1)
	assert.Equal(t, "10.0.0.1", creds.OntapEndpoints[0].IP)
}

func TestGetDedicatedClusterPoolCreds_AdminUser_NilHost_ReturnsNil(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "proj-1",
	}
	poolView := dedicatedPoolView(account.ID, "cluster-a", "STANDARD")

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().GetAccount(mock.Anything, "proj-1").Return(account, nil)
	mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", account.ID).Return(poolView, nil)
	// Pinned host credential has no backing Host row: admin path returns (nil, nil).
	mockStorage.EXPECT().
		GetDedicatedPinnedHostCredential(mock.Anything, "proj-1", "cluster-a").
		Return(&database.HostCredential{
			Host:     nil,
			Username: "account-host-user",
			SecretId: "secret-a",
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	creds, err := o.GetDedicatedClusterPoolCreds(context.Background(), "pool-uuid", "proj-1", "", AdminUserName)

	require.NoError(t, err)
	assert.Nil(t, creds)
}

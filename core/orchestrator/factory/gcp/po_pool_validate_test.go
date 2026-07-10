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
)

func poValidateParams() *commonparams.CreatePoolParams {
	return &commonparams.CreatePoolParams{
		ClusterName:  "vsim-cluster",
		ServiceLevel: commonparams.ServiceLevelExtreme,
		Mode:         commonparams.ONTAPMode,
		SizeInBytes:  100,
	}
}

func pinnedAccountHost() *datamodel.AccountHost {
	return &datamodel.AccountHost{
		Pinned: true,
		OntapModeCredentials: &datamodel.ExpertModeCredential{
			Username: "admin",
			Password: "secret",
			AuthType: env.USERNAME_PWD,
		},
	}
}

func poValidateHost() *datamodel.Host {
	return &datamodel.Host{
		BaseModel:        datamodel.BaseModel{UUID: "host-uuid-1"},
		Name:             "vsim-cluster",
		ExternalName:     "vsim-cluster",
		IPAddress:        "10.0.0.1",
		Capacity:         1000,
		EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor,
	}
}

func TestValidatePOPoolHost(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}

	t.Run("nil params", func(t *testing.T) {
		err := validatePOPoolHost(ctx, database.NewMockStorage(t), account, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PO pool request")
	})

	t.Run("nil account", func(t *testing.T) {
		err := validatePOPoolHost(ctx, database.NewMockStorage(t), nil, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PO pool request")
	})

	t.Run("host not found", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(nil,
			vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("Host", nil)))

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), `clusterName "vsim-cluster" does not match a registered host`)
		se.AssertExpectations(t)
	})

	t.Run("host lookup error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(nil, errors.New("db down"))

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db down")
		se.AssertExpectations(t)
	})

	t.Run("missing management IP", func(t *testing.T) {
		host := poValidateHost()
		host.IPAddress = ""
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(host, nil)

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), POValidationHostMgmtIPMsg)
		se.AssertExpectations(t)
	})

	t.Run("account host not pinned", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").Return(nil,
			vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("AccountHost", nil)))

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), POValidationAccountHostPinMsg)
		se.AssertExpectations(t)
	})

	t.Run("account host lookup error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(nil, errors.New("lookup failed"))

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup failed")
		se.AssertExpectations(t)
	})

	t.Run("account host not pinned with credentials", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(&datamodel.AccountHost{Pinned: false}, nil)

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), POValidationAccountHostPinMsg)
		se.AssertExpectations(t)
	})

	t.Run("capacity exceeded", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(pinnedAccountHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(800), nil)

		params := poValidateParams()
		params.SizeInBytes = 700
		err := validatePOPoolHost(ctx, se, account, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationHostCapacityMsg)
		se.AssertExpectations(t)
	})

	t.Run("capacity respects host efficiency factor", func(t *testing.T) {
		host := poValidateHost()
		host.EfficiencyFactor = 2.0

		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(host, nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(pinnedAccountHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(1500), nil)

		params := poValidateParams()
		params.SizeInBytes = 400
		err := validatePOPoolHost(ctx, se, account, params)
		require.NoError(t, err)

		params.SizeInBytes = 600
		err = validatePOPoolHost(ctx, se, account, params)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationHostCapacityMsg)
		se.AssertExpectations(t)
	})

	t.Run("sum pool size error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(pinnedAccountHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(0), errors.New("sum failed"))

		err := validatePOPoolHost(ctx, se, account, poValidateParams())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sum failed")
		se.AssertExpectations(t)
	})

	t.Run("success sets host uuid", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHostByName", mock.Anything, "vsim-cluster").Return(poValidateHost(), nil)
		se.On("GetAccountHostByAccountAndHost", mock.Anything, int64(42), "host-uuid-1").
			Return(pinnedAccountHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(0), nil)

		params := poValidateParams()
		err := validatePOPoolHost(ctx, se, account, params)
		require.NoError(t, err)
		assert.Equal(t, "host-uuid-1", params.HostUUID)
		assert.Equal(t, "vsim-cluster", params.ClusterName)
		se.AssertExpectations(t)
	})
}

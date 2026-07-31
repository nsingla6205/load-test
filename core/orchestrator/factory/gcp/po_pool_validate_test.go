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

func TestValidatePOPoolSizeChange(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
		HostUUID:    "host-uuid-1",
		SizeInBytes: 500,
		ServiceLevel: commonparams.ServiceLevelExtreme,
		PoolAttributes: &datamodel.PoolAttributes{ServiceLevel: commonparams.ServiceLevelExtreme},
	}

	t.Run("same size skips checks", func(t *testing.T) {
		se := database.NewMockStorage(t)
		err := validatePOPoolSizeChange(ctx, se, pool, 500)
		require.NoError(t, err)
	})

	t.Run("grow within capacity", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(poValidateHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(800), nil)

		err := validatePOPoolSizeChange(ctx, se, pool, 600)
		require.NoError(t, err)
		se.AssertExpectations(t)
	})

	t.Run("thick shrink rejects below effective usage", func(t *testing.T) {
		se := database.NewMockStorage(t)
		host := poValidateHost()
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(host, nil)
		se.On("GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, int64(7)).
			Return(&database.ExpertModePoolCapacity{TotalSize: 400, VolumeCount: 2}, nil)

		err := validatePOPoolSizeChange(ctx, se, pool, 300)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationPoolShrinkMsg)
		se.AssertExpectations(t)
	})

	t.Run("thin shrink skips volume usage", func(t *testing.T) {
		se := database.NewMockStorage(t)
		host := poValidateHost()
		thin := datamodel.ProvisioningModelThin
		host.ProvisioningModel = &thin
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(host, nil)

		err := validatePOPoolSizeChange(ctx, se, pool, 100)
		require.NoError(t, err)
		se.AssertExpectations(t)
	})

	t.Run("unknown provisioning model rejects shrink", func(t *testing.T) {
		se := database.NewMockStorage(t)
		host := poValidateHost()
		unknown := datamodel.ProvisioningModel("UNKNOWN")
		host.ProvisioningModel = &unknown
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(host, nil)

		err := validatePOPoolSizeChange(ctx, se, pool, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown provisioning model")
		se.AssertExpectations(t)
	})

	t.Run("nil pool", func(t *testing.T) {
		err := validatePOPoolSizeChange(ctx, database.NewMockStorage(t), nil, 100)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PO pool")
	})

	t.Run("missing host uuid", func(t *testing.T) {
		badPool := *pool
		badPool.HostUUID = ""
		err := validatePOPoolSizeChange(ctx, database.NewMockStorage(t), &badPool, 600)
		require.Error(t, err)
		assert.True(t, customerrors.IsNonRetryableErr(err))
		assert.Contains(t, err.Error(), commonparams.POValidationHostUUIDMsg)
	})

	t.Run("get host error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(nil, errors.New("host lookup failed"))

		err := validatePOPoolSizeChange(ctx, se, pool, 600)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "host lookup failed")
		se.AssertExpectations(t)
	})

	t.Run("grow sum pools error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(poValidateHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(0), errors.New("sum failed"))

		err := validatePOPoolSizeChange(ctx, se, pool, 600)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sum failed")
		se.AssertExpectations(t)
	})

	t.Run("grow exceeds host capacity", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(poValidateHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(950), nil)

		err := validatePOPoolSizeChange(ctx, se, pool, 1000)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationHostCapacityMsg)
		se.AssertExpectations(t)
	})

	t.Run("expert mode capacity lookup error", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(poValidateHost(), nil)
		se.On("GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, int64(7)).
			Return(nil, errors.New("capacity lookup failed"))

		err := validatePOPoolSizeChange(ctx, se, pool, 300)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "capacity lookup failed")
		se.AssertExpectations(t)
	})
}

func TestValidatePOPoolUpdate(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 7, UUID: "pool-uuid"},
		HostUUID:      "host-uuid-1",
		SizeInBytes:   500,
		LargeCapacity: false,
		ServiceLevel:  commonparams.ServiceLevelExtreme,
	}

	t.Run("nil params", func(t *testing.T) {
		err := validatePOPoolUpdate(ctx, database.NewMockStorage(t), nil, pool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PO pool update request")
	})

	t.Run("nil pool", func(t *testing.T) {
		err := validatePOPoolUpdate(ctx, database.NewMockStorage(t), &commonparams.UpdatePoolParams{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid PO pool update request")
	})

	t.Run("active directory rejected", func(t *testing.T) {
		params := &commonparams.UpdatePoolParams{ActiveDirectoryConfigId: "ad-id"}
		err := validatePOPoolUpdate(ctx, database.NewMockStorage(t), params, pool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationActiveDirectoryMsg)
	})

	t.Run("large capacity change rejected", func(t *testing.T) {
		large := true
		params := &commonparams.UpdatePoolParams{LargeCapacity: &large}
		err := validatePOPoolUpdate(ctx, database.NewMockStorage(t), params, pool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), commonparams.POValidationLargeCapacityUpdateMsg)
	})

	t.Run("size unchanged skips size validation", func(t *testing.T) {
		params := &commonparams.UpdatePoolParams{SizeInBytes: 500, Description: "ok"}
		err := validatePOPoolUpdate(ctx, database.NewMockStorage(t), params, pool)
		require.NoError(t, err)
	})

	t.Run("size change delegates to size validator", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.On("GetHost", mock.Anything, "host-uuid-1").Return(poValidateHost(), nil)
		se.On("SumPoolSizeByHostUUID", mock.Anything, "host-uuid-1").Return(int64(800), nil)

		params := &commonparams.UpdatePoolParams{SizeInBytes: 600}
		err := validatePOPoolUpdate(ctx, se, params, pool)
		require.NoError(t, err)
		se.AssertExpectations(t)
	})
}

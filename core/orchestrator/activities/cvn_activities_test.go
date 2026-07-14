package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

// fakeCVN embeds sdk.CVN so only exercised methods need implementations.
type fakeCVN struct {
	sdk.CVN
	createResp   *sdk.DataPathResponse
	createErr    error
	lastSDKParams *sdk.DataPathParams
}

func (f *fakeCVN) CreateDataPathV1(p *sdk.DataPathParams) (*sdk.DataPathResponse, error) {
	f.lastSDKParams = p
	return f.createResp, f.createErr
}

const resolvedZoneUUID = "5c82619a-cdcb-4318-bc3f-7a8a6f9c88af"

func zoneForLookup() *datamodel.Zone {
	return &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: resolvedZoneUUID},
		Name:      "us-central1-a",
	}
}

func expectZoneLookup(mockSE *database.MockStorage) {
	mockSE.EXPECT().
		GetZoneByRegionName(mock.Anything, "us-central1").
		Return(zoneForLookup(), nil).
		Once()
}

func withLocalRegion(t *testing.T, region string) {
	t.Helper()
	orig := env.Region
	env.Region = region
	t.Cleanup(func() { env.Region = orig })
}

func TestCVNActivity_CreateDataPath(t *testing.T) {
	withLocalRegion(t, "us-central1")

	params := common.DataPathParams{
		AccountUUID:     "acct-uuid",
		RemoteAccountID: "projects/p/global/networks/net",
		MicrozoneUUID:   "host-uuid",
		SubnetMaskSize:  27,
		VlanBandwidth:   common.VlanBandwidth50G,
		PoolUUID:        "pool-uuid",
	}

	ipRange := &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{UUID: "range-1"},
		StartIP:   "10.1.1.4",
		Netmask:   "255.255.255.0",
		Gateway:   "10.1.1.1",
	}

	t.Run("creates datapath", func(t *testing.T) {
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 100, IPrangeUUID: "range-1"},
		}
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)
		mockSE.EXPECT().GetIPRangeByUUID(mock.Anything, "range-1").Return(ipRange, nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, accountID string) (sdk.CVN, error) {
				require.Equal(t, params.AccountUUID, accountID)
				return fake, nil
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		val, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.NoError(t, err)

		var resp common.DataPathResponse
		require.NoError(t, val.Get(&resp))
		require.Equal(t, "range-1", resp.DatapathID)
		require.Equal(t, "100", resp.VlanID)
		require.Equal(t, "10.1.1.0/24", resp.IPRange)
		require.Equal(t, "10.1.1.1", resp.Gateway)
		require.NotNil(t, fake.lastSDKParams)
		require.Equal(t, resolvedZoneUUID, fake.lastSDKParams.ZoneUUID)
	})

	t.Run("invalid params", func(t *testing.T) {
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{SE: database.NewMockStorage(t)}
		env.RegisterActivity(act.CreateDataPath)

		bad := params
		bad.AccountUUID = ""
		_, err := env.ExecuteActivity(act.CreateDataPath, bad)
		require.Error(t, err)
	})

	t.Run("client creation fails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return nil, errors.New("CVN_HOST is not configured")
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("create data path fails", func(t *testing.T) {
		fake := &fakeCVN{createErr: errors.New("create failed")}
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return fake, nil
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("sdk returns nil response", func(t *testing.T) {
		fake := &fakeCVN{createResp: nil}
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return fake, nil
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("ip range lookup fails", func(t *testing.T) {
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 100, IPrangeUUID: "range-1"},
		}
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)
		mockSE.EXPECT().GetIPRangeByUUID(mock.Anything, "range-1").Return(nil, errors.New("not found")).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return fake, nil
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("storage not configured", func(t *testing.T) {
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 100, IPrangeUUID: "range-1"},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return fake, nil
			},
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("zone lookup fails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.EXPECT().
			GetZoneByRegionName(mock.Anything, "us-central1").
			Return(nil, errors.New("zone not found")).
			Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{SE: mockSE}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("local region not configured", func(t *testing.T) {
		withLocalRegion(t, "")

		mockSE := database.NewMockStorage(t)
		actEnv := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{SE: mockSE}
		actEnv.RegisterActivity(act.CreateDataPath)

		_, err := actEnv.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})

	t.Run("empty ip range uuid", func(t *testing.T) {
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 100},
		}
		mockSE := database.NewMockStorage(t)
		expectZoneLookup(mockSE)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &CVNActivity{
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return fake, nil
			},
			SE: mockSE,
		}
		env.RegisterActivity(act.CreateDataPath)

		_, err := env.ExecuteActivity(act.CreateDataPath, params)
		require.Error(t, err)
	})
}

func TestCreateDataPathViaSDK(t *testing.T) {
	params := common.DataPathParams{
		AccountUUID:     "acct",
		RemoteAccountID: "projects/p/global/networks/net",
		ZoneUUID:        resolvedZoneUUID,
		MicrozoneUUID:   "host",
		SubnetMaskSize:  common.MinSubnetMaskSize,
		VlanBandwidth:   common.VlanBandwidth50G,
	}

	t.Run("returns sdk outputs only", func(t *testing.T) {
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 42, IPrangeUUID: "range-uuid"},
		}
		out, err := createDataPathViaSDK(fake, params)
		require.NoError(t, err)
		require.Equal(t, "range-uuid", out.DatapathID)
		require.Equal(t, "42", out.VlanID)
		require.Equal(t, "range-uuid", out.IpRangeUUID)
		require.Empty(t, out.IPRange)
		require.Empty(t, out.Gateway)
	})

	t.Run("passes pre-selected ip range uuid", func(t *testing.T) {
		withRange := params
		withRange.IpRangeUUID = "preselected-range"
		fake := &fakeCVN{
			createResp: &sdk.DataPathResponse{SouthboundVlanID: 42, IPrangeUUID: "preselected-range"},
		}
		_, err := createDataPathViaSDK(fake, withRange)
		require.NoError(t, err)
		require.NotNil(t, fake.lastSDKParams)
		require.Equal(t, "preselected-range", fake.lastSDKParams.IpRangeUUID)
	})

	t.Run("create fails", func(t *testing.T) {
		fake := &fakeCVN{createErr: errors.New("create failed")}
		_, err := createDataPathViaSDK(fake, params)
		require.Error(t, err)
	})
}

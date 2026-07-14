package activities

import (
	"context"
	"testing"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/testsuite"
)

func TestIPRangeActivity_PreSelectDataLifIPRange(t *testing.T) {
	in := PreSelectDataLifIPRangeInput{
		AccountUUID:    "acct-uuid",
		VendorSubnetID: "projects/p/global/networks/net",
		HostUUID:       "host-uuid",
		SubnetMaskSize: 27,
		IPAddressCount: 2,
	}

	t.Run("reuses available range", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, in.AccountUUID, in.VendorSubnetID).
			Return([]*datamodel.IPRange{{BaseModel: datamodel.BaseModel{UUID: "range-1"}}}, nil).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-1", in.IPAddressCount).
			Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &IPRangeActivity{
			SE: mockSE,
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return nil, nil
			},
		}
		env.RegisterActivity(act.PreSelectDataLifIPRange)

		val, err := env.ExecuteActivity(act.PreSelectDataLifIPRange, in)
		require.NoError(t, err)

		var selected string
		require.NoError(t, val.Get(&selected))
		require.Equal(t, "range-1", selected)
	})

	t.Run("creates secondary range when exhausted", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, in.AccountUUID, in.VendorSubnetID).
			Return([]*datamodel.IPRange{{BaseModel: datamodel.BaseModel{UUID: "range-1"}}}, nil).Once()
		mockSE.On("ValidateIPAddressesAvailableInIPRange", mock.Anything, "range-1", in.IPAddressCount).
			Return(vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				customerrors.NewUnavailableErr(common.ErrNoIPAddressesAvailable))).Once()

		mockCVN := sdk.NewMockCVN(t)
		defer mockCVN.CloseMockCVN()

		remoteAccountID := in.VendorSubnetID
		expectedSecParams := &sdk.SecondaryIPRangeParams{
			AccountUUID:     in.AccountUUID,
			RemoteAccountID: &remoteAccountID,
			SubnetMaskSize:  in.SubnetMaskSize,
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &IPRangeActivity{
			SE: mockSE,
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return mockCVN, nil
			},
		}
		env.RegisterActivity(act.PreSelectDataLifIPRange)

		var selected string
		var execErr error
		go func() {
			defer mockCVN.MockCVNDone()
			val, err := env.ExecuteActivity(act.PreSelectDataLifIPRange, in)
			execErr = err
			if err == nil {
				execErr = val.Get(&selected)
			}
		}()

		mockCVN.AssertCreateSecondaryIPRangeWithPolling(expectedSecParams, "range-2", nil)
		mockCVN.AssertMockCVNDone()

		require.NoError(t, execErr)
		require.Equal(t, "range-2", selected)
	})

	t.Run("no ranges returns empty", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetDataLIFIPRanges", mock.Anything, in.AccountUUID, in.VendorSubnetID).
			Return(nil, nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &IPRangeActivity{
			SE: mockSE,
			NewCVN: func(_ context.Context, _ string) (sdk.CVN, error) {
				return nil, nil
			},
		}
		env.RegisterActivity(act.PreSelectDataLifIPRange)

		val, err := env.ExecuteActivity(act.PreSelectDataLifIPRange, in)
		require.NoError(t, err)

		var selected string
		require.NoError(t, val.Get(&selected))
		require.Empty(t, selected)
	})

	t.Run("storage not configured", func(t *testing.T) {
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &IPRangeActivity{}
		env.RegisterActivity(act.PreSelectDataLifIPRange)

		_, err := env.ExecuteActivity(act.PreSelectDataLifIPRange, in)
		require.Error(t, err)
	})
}

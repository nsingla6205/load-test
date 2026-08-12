package api

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	orchestratorMocks "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

func TestV1GetAllVolumeTenancy_MissingClusterName(t *testing.T) {
	h := Handler{}
	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{})
	assert.NoError(t, err)

	badRequest, ok := res.(*oasgenserver.V1GetAllVolumeTenancyBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(vsaerrors.ErrMissingRequiredInputError), badRequest.Code)
}

func TestV1GetAllVolumeTenancy_Success(t *testing.T) {
	mockOrch := orchestratorMocks.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrch}

	mockOrch.EXPECT().
		ListExpertModeVolumeTenancy(mock.Anything, commonparams.ListExpertModeVolumeTenancyParams{
			ClusterName: "cluster-01",
			CursorID:    10,
			Limit:       commonparams.DefaultExpertModeVolumeTenancyPageLimit,
		}).
		Return([]commonparams.ExpertModeVolumeTenancy{
			{
				UUID:           "uuid-1",
				SVM:            "svm_a",
				Volume:         "vol_1",
				Project:        "111",
				PoolName:       "pool-a",
				TenantProject:  "222",
				DeploymentName: "dep-a",
			},
		}, "42", nil)

	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{
		ClusterName: "cluster-01",
		Cursor:      oasgenserver.NewOptString("10"),
	})
	assert.NoError(t, err)

	ok, isOK := res.(*oasgenserver.V1GetAllVolumeTenancyOK)
	assert.True(t, isOK)
	assert.Len(t, ok.Items, 1)
	assert.Equal(t, "uuid-1", ok.Items[0].UUID)
	assert.Equal(t, "42", ok.NextCursor.Value)
}

func TestV1GetAllVolumeTenancy_OmitsNextCursorOnLastPage(t *testing.T) {
	mockOrch := orchestratorMocks.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrch}

	mockOrch.EXPECT().
		ListExpertModeVolumeTenancy(mock.Anything, mock.Anything).
		Return([]commonparams.ExpertModeVolumeTenancy{}, "", nil)

	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{
		ClusterName: "cluster-01",
	})
	require.NoError(t, err)

	ok, isOK := res.(*oasgenserver.V1GetAllVolumeTenancyOK)
	require.True(t, isOK)
	assert.False(t, ok.NextCursor.IsSet())
}

func TestV1GetAllVolumeTenancy_InvalidCursor(t *testing.T) {
	h := Handler{}
	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{
		ClusterName: "cluster-01",
		Cursor:      oasgenserver.NewOptString("not-a-number"),
	})
	assert.NoError(t, err)

	badRequest, ok := res.(*oasgenserver.V1GetAllVolumeTenancyBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(vsaerrors.ErrInputValidationError), badRequest.Code)
}

func TestV1GetAllVolumeTenancy_DatabaseErrorUsesTrackingCode(t *testing.T) {
	mockOrch := orchestratorMocks.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrch}

	mockOrch.EXPECT().
		ListExpertModeVolumeTenancy(mock.Anything, mock.Anything).
		Return(nil, "", vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.New("connection refused")))

	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{
		ClusterName: "cluster-01",
	})
	assert.NoError(t, err)

	serverErr, ok := res.(*oasgenserver.V1GetAllVolumeTenancyInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(vsaerrors.ErrDatabaseDataReadError), serverErr.Code)
	assert.NotContains(t, serverErr.Message, "connection refused")
}

func TestV1GetAllVolumeTenancy_NotImplemented(t *testing.T) {
	mockOrch := orchestratorMocks.NewMockOrchestratorFactory(t)
	h := Handler{Orchestrator: mockOrch}

	mockOrch.EXPECT().
		ListExpertModeVolumeTenancy(mock.Anything, mock.Anything).
		Return(nil, "", utilserrors.NewNotImplementedYetErr())

	res, err := h.V1GetAllVolumeTenancy(context.Background(), oasgenserver.V1GetAllVolumeTenancyParams{
		ClusterName: "cluster-01",
	})
	assert.NoError(t, err)

	notImplemented, ok := res.(*oasgenserver.V1GetAllVolumeTenancyNotImplemented)
	require.True(t, ok)
	assert.Equal(t, float64(commonparams.HttpStatusNotImplemented), notImplemented.Code)
	assert.Contains(t, notImplemented.Message, "not supported")
}

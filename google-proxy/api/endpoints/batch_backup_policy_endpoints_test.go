package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestV1betaBatchListBackupPolicies_BackupDisabled(t *testing.T) {
	orig := backupEnabled
	backupEnabled = false
	defer func() { backupEnabled = orig }()

	h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
	ctx := authContext()
	req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{"00000000-0000-4000-8000-000000000001"}}
	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{
		LocationId: "us-east4",
		Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
		},
	}

	res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
	require.NoError(t, err)
	bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
	require.True(t, ok)
	assert.Contains(t, bad.Message, "not enabled")
}

func TestV1betaBatchListBackupPolicies_VCPOnly(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()

	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()

	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	policyUUID := "11111111-1111-4111-8111-111111111111"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
		Return(map[string]int64{policyUUID: 2}, map[string]*coremodels.BackupPolicy{
			policyUUID: {
				ResourceID:       "pol-name",
				BackupPolicyUUID: policyUUID,
				State:            datamodel.LifeCycleStateREADY,
				DailyBackupLimit: 1, WeeklyBackupLimit: 0, MonthlyBackupLimit: 0,
				Enabled: true,
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, http.Header{})
	req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}}
	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{
		LocationId: "us-east4",
		Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemVolumeCount,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemState,
		},
	}

	res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 1)
	assert.Equal(t, policyUUID, okRes.BackupPolicies[0].BackupPolicyId.Value)
	assert.Equal(t, "pol-name", okRes.BackupPolicies[0].ResourceId.Value)
	assert.Equal(t, 2, okRes.BackupPolicies[0].VolumeCount.Value)
}

func TestMergeBatchBackupPolicyParallelLists(t *testing.T) {
	u1 := "11111111-1111-4111-8111-111111111111"
	u2 := "22222222-2222-4222-8222-222222222222"
	fieldSet := map[string]bool{
		"backupPolicyId": true,
		"volumeCount":    true,
		"resourceId":     true,
	}

	vcp1 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u1),
		ResourceId:     gcpgenserver.NewOptNilString("vcp-res"),
		VolumeCount:    gcpgenserver.NewOptNilInt(3),
	}
	sde1 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u1),
		ResourceId:     gcpgenserver.NewOptNilString("sde-res"),
		VolumeCount:    gcpgenserver.NewOptNilInt(5),
	}
	vcp2 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u2),
		ResourceId:     gcpgenserver.NewOptNilString("only-vcp"),
		VolumeCount:    gcpgenserver.NewOptNilInt(1),
	}
	sde3 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString("33333333-3333-4333-8333-333333333333"),
		ResourceId:     gcpgenserver.NewOptNilString("only-sde"),
		VolumeCount:    gcpgenserver.NewOptNilInt(7),
	}

	out := mergeBatchBackupPolicyParallelLists(
		[]string{u1, u2, "33333333-3333-4333-8333-333333333333"},
		[]gcpgenserver.BatchBackupPolicyV1beta{vcp1, vcp2},
		[]gcpgenserver.BatchBackupPolicyV1beta{sde1, sde3},
		fieldSet,
	)
	require.Len(t, out, 3)

	assert.Equal(t, u1, out[0].BackupPolicyId.Value)
	assert.Equal(t, 8, out[0].VolumeCount.Value, "same UUID in VCP and SDE: volume counts should sum")
	assert.Equal(t, "vcp-res", out[0].ResourceId.Value, "VCP wins for resourceId when both set")

	assert.Equal(t, u2, out[1].BackupPolicyId.Value)
	assert.Equal(t, 1, out[1].VolumeCount.Value)
	assert.Equal(t, "only-vcp", out[1].ResourceId.Value)

	assert.Equal(t, "33333333-3333-4333-8333-333333333333", out[2].BackupPolicyId.Value)
	assert.Equal(t, 7, out[2].VolumeCount.Value)
	assert.Equal(t, "only-sde", out[2].ResourceId.Value)
}

func TestV1betaBatchListBackupPolicies_Parallel_MergesDuplicateUUID(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()

	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = origHost }()

	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	policyUUID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
		Return(map[string]int64{policyUUID: 4}, map[string]*coremodels.BackupPolicy{
			policyUUID: {
				ResourceID:       "merged-name",
				BackupPolicyUUID: policyUUID,
				State:            datamodel.LifeCycleStateREADY,
				DailyBackupLimit: 1, WeeklyBackupLimit: 0, MonthlyBackupLimit: 0,
				Enabled: true,
			},
		}, nil)

	origFetch := fetchBatchBackupPoliciesFromCVPFn
	defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()
	fetchBatchBackupPoliciesFromCVPFn = func(ctx context.Context, uuids []string, params gcpgenserver.V1betaBatchListBackupPoliciesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
		return []gcpgenserver.BatchBackupPolicyV1beta{
			{
				BackupPolicyId: gcpgenserver.NewOptNilString(policyUUID),
				ResourceId:     gcpgenserver.NewOptNilString("merged-name"),
				VolumeCount:    gcpgenserver.NewOptNilInt(6),
			},
		}, nil
	}

	h := Handler{Orchestrator: mockOrch}
	ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, http.Header{})
	req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}}
	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{
		LocationId: "us-east4",
		Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemVolumeCount,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemState,
		},
	}

	res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 1)
	assert.Equal(t, policyUUID, okRes.BackupPolicies[0].BackupPolicyId.Value)
	assert.Equal(t, 10, okRes.BackupPolicies[0].VolumeCount.Value, "4 VCP + 6 SDE")
}

func TestV1betaBatchListBackupPolicies_Parallel_FieldMaskOmitsBackupPolicyId_StillMerges(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()

	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = origHost }()

	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	policyUUID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
		Return(map[string]int64{policyUUID: 4}, map[string]*coremodels.BackupPolicy{
			policyUUID: {
				ResourceID:       "merged-name",
				BackupPolicyUUID: policyUUID,
				State:            datamodel.LifeCycleStateREADY,
				DailyBackupLimit: 1, WeeklyBackupLimit: 0, MonthlyBackupLimit: 0,
				Enabled: true,
			},
		}, nil)

	origFetch := fetchBatchBackupPoliciesFromCVPFn
	defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()
	fetchBatchBackupPoliciesFromCVPFn = func(ctx context.Context, uuids []string, params gcpgenserver.V1betaBatchListBackupPoliciesParams, fieldSet map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
		assert.True(t, fieldSet["backupPolicyId"], "internal merge field set must keep backupPolicyId for indexing")
		return []gcpgenserver.BatchBackupPolicyV1beta{
			{
				BackupPolicyId: gcpgenserver.NewOptNilString(policyUUID),
				ResourceId:     gcpgenserver.NewOptNilString("merged-name"),
				VolumeCount:    gcpgenserver.NewOptNilInt(6),
			},
		}, nil
	}

	h := Handler{Orchestrator: mockOrch}
	ctx := context.WithValue(context.Background(), utilsmiddleware.HeaderContextKey, http.Header{})
	req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}}
	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{
		LocationId: "us-east4",
		Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemVolumeCount,
		},
	}

	res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 1)
	assert.False(t, okRes.BackupPolicies[0].BackupPolicyId.Set, "client omitted backupPolicyId from field mask")
	assert.Equal(t, "merged-name", okRes.BackupPolicies[0].ResourceId.Value)
	assert.Equal(t, 10, okRes.BackupPolicies[0].VolumeCount.Value)
}

func TestV1betaBatchListBackupPolicies_RequestValidationAndAuth(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()

	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	t.Run("NoHTTPRequest_Unauthorized", func(t *testing.T) {
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := context.Background()
		req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{"00000000-0000-4000-8000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
		require.NoError(t, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesUnauthorized)
		require.True(t, ok)
	})

	t.Run("BatchAuthFails_Unauthorized", func(t *testing.T) {
		restoreAuth := stubBatchAuth(false)
		defer restoreAuth()
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := authContext()
		req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{"00000000-0000-4000-8000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
		require.NoError(t, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesUnauthorized)
		require.True(t, ok)
	})

	t.Run("InvalidLocation_BadRequest", func(t *testing.T) {
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := authContext()
		req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{"00000000-0000-4000-8000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "invalid location!"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
		require.NoError(t, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(400), bad.Code)
	})

	t.Run("EmptyBackupPolicyUuids_BadRequest", func(t *testing.T) {
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := authContext()
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, &gcpgenserver.BatchBackupPolicyUUIDListV1beta{}, params)
		require.NoError(t, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
		require.True(t, ok)
		assert.Contains(t, bad.Message, "at least 1 item")
	})

	t.Run("TooManyUuids_BadRequest", func(t *testing.T) {
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := authContext()
		uuids := make([]string, env.MaxBatchBackupPolicyUUIDs+1)
		for i := range uuids {
			uuids[i] = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: uuids}, params)
		require.NoError(t, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
		require.True(t, ok)
		assert.Contains(t, bad.Message, "at most")
	})

	t.Run("MalformedUUID_ReturnsBadRequestBeforeFetching", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		h := Handler{Orchestrator: mockOrch}
		ctx := authContext()
		req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{
			"11111111-1111-4111-8111-111111111111",
			"not-a-uuid",
		}}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
		require.NoError(t, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
		require.True(t, ok)
		assert.Equal(t, float64(http.StatusBadRequest), bad.Code)
		assert.Contains(t, bad.Message, "backupPolicyUuids.1 in body should match")
		assert.Contains(t, bad.Message, "[a-fA-F0-9]{8}")
		mockOrch.AssertNotCalled(t, "GetBackupPoliciesByUUIDs", mock.Anything, mock.Anything)
	})

	t.Run("EmptyStringUUID_ReturnsBadRequest", func(t *testing.T) {
		h := Handler{Orchestrator: factory.NewMockOrchestratorFactory(t)}
		ctx := authContext()
		req := &gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{""}}
		params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
		res, err := h.V1betaBatchListBackupPolicies(ctx, req, params)
		require.NoError(t, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesBadRequest)
		require.True(t, ok)
		assert.Contains(t, bad.Message, "backupPolicyUuids.0 in body should match")
	})
}

func TestV1betaBatchListBackupPolicies_VCPOnly_OrchestratorError(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, mock.Anything).
		Return(nil, nil, errors.New("db error"))

	h := Handler{Orchestrator: mockOrch}
	ctx := authContext()
	policyUUID := "11111111-1111-4111-8111-111111111111"
	res, err := h.V1betaBatchListBackupPolicies(ctx,
		&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}},
		gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4", Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
		}})
	require.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesInternalServerError)
	require.True(t, ok)
}

func TestV1betaBatchListBackupPolicies_Parallel_ErrorPaths(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = origHost }()
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	policyUUID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	t.Run("BothFail_InternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
			Return(nil, nil, errors.New("vcp err"))
		origFetch := fetchBatchBackupPoliciesFromCVPFn
		fetchBatchBackupPoliciesFromCVPFn = func(context.Context, []string, gcpgenserver.V1betaBatchListBackupPoliciesParams, map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
			return nil, errors.New("sde err")
		}
		defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()

		h := Handler{Orchestrator: mockOrch}
		ctx := authContext()
		res, err := h.V1betaBatchListBackupPolicies(ctx,
			&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}},
			gcpgenserver.V1betaBatchListBackupPoliciesParams{
				LocationId: "us-east4",
				Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
					gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
				},
			})
		require.NoError(t, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesInternalServerError)
		require.True(t, ok)
	})

	t.Run("VCPFails_ReturnsSDEOnly", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
			Return(nil, nil, errors.New("vcp err"))
		origFetch := fetchBatchBackupPoliciesFromCVPFn
		fetchBatchBackupPoliciesFromCVPFn = func(context.Context, []string, gcpgenserver.V1betaBatchListBackupPoliciesParams, map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
			return []gcpgenserver.BatchBackupPolicyV1beta{
				{BackupPolicyId: gcpgenserver.NewOptNilString(policyUUID), ResourceId: gcpgenserver.NewOptNilString("sde-only")},
			}, nil
		}
		defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()

		h := Handler{Orchestrator: mockOrch}
		ctx := authContext()
		res, err := h.V1betaBatchListBackupPolicies(ctx,
			&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}},
			gcpgenserver.V1betaBatchListBackupPoliciesParams{
				LocationId: "us-east4",
				Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
					gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
					gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
				},
			})
		require.NoError(t, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
		require.True(t, ok)
		require.Len(t, okRes.BackupPolicies, 1)
		assert.Equal(t, "sde-only", okRes.BackupPolicies[0].ResourceId.Value)
	})

	t.Run("SDEFails_ReturnsVCPOnly", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
			Return(map[string]int64{policyUUID: 1}, map[string]*coremodels.BackupPolicy{
				policyUUID: {BackupPolicyUUID: policyUUID, ResourceID: "vcp-only", State: datamodel.LifeCycleStateREADY},
			}, nil)
		origFetch := fetchBatchBackupPoliciesFromCVPFn
		fetchBatchBackupPoliciesFromCVPFn = func(context.Context, []string, gcpgenserver.V1betaBatchListBackupPoliciesParams, map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
			return nil, errors.New("sde err")
		}
		defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()

		h := Handler{Orchestrator: mockOrch}
		ctx := authContext()
		res, err := h.V1betaBatchListBackupPolicies(ctx,
			&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}},
			gcpgenserver.V1betaBatchListBackupPoliciesParams{
				LocationId: "us-east4",
				Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
					gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
					gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
				},
			})
		require.NoError(t, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
		require.True(t, ok)
		require.Len(t, okRes.BackupPolicies, 1)
		assert.Equal(t, "vcp-only", okRes.BackupPolicies[0].ResourceId.Value)
	})
}

func TestV1betaBatchListBackupPolicies_NoFields_MatchesCVPMinimal(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	policyUUID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{policyUUID}).
		Return(map[string]int64{policyUUID: 0}, map[string]*coremodels.BackupPolicy{
			policyUUID: {
				BackupPolicyUUID: policyUUID,
				ResourceID:       "full",
				State:            datamodel.LifeCycleStateREADY,
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	ctx := authContext()
	res, err := h.V1betaBatchListBackupPolicies(ctx,
		&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{policyUUID}},
		gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"})
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 1)
	assert.Equal(t, policyUUID, okRes.BackupPolicies[0].BackupPolicyId.Value)
	assert.False(t, okRes.BackupPolicies[0].ResourceId.Set)
	assert.False(t, okRes.BackupPolicies[0].State.Set)
}

func TestMergeBatchBackupPolicyVCPAndSDE_FillsStateFromSDE(t *testing.T) {
	u := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	fieldSet := map[string]bool{
		"backupPolicyId": true,
		"state":          true,
	}
	vcp := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
	}
	sde := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
		State:          gcpgenserver.NewOptNilBatchBackupPolicyV1betaState(gcpgenserver.BatchBackupPolicyV1betaStateREADY),
	}
	out := mergeBatchBackupPolicyVCPAndSDE(vcp, sde, fieldSet)
	assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateREADY, out.State.Value)
}

func TestMergeBatchBackupPolicyParallelLists_SkipsUUIDNotInEitherSystem(t *testing.T) {
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	out := mergeBatchBackupPolicyParallelLists(
		[]string{id},
		[]gcpgenserver.BatchBackupPolicyV1beta{{BackupPolicyId: gcpgenserver.NewOptNilString("")}},
		nil,
		map[string]bool{"backupPolicyId": true},
	)
	assert.Empty(t, out)
}

func TestBatchBackupPolicyHelpers(t *testing.T) {
	t.Run("DefaultBatchBackupPolicyFullFieldSet", func(t *testing.T) {
		fs := defaultBatchBackupPolicyFullFieldSet()
		assert.NotEmpty(t, fs)
		assert.True(t, fs["backupPolicyId"])
	})

	t.Run("FieldSetWithBackupPolicyIDForMerge", func(t *testing.T) {
		fs := map[string]bool{"resourceId": true}
		merged := fieldSetWithBackupPolicyIDForMerge(fs)
		assert.True(t, merged["backupPolicyId"])
		assert.True(t, merged["resourceId"])
	})

	t.Run("ConvertCVPBatchBackupPolicyToGCP_WithState", func(t *testing.T) {
		st := string(gcpgenserver.BatchBackupPolicyV1betaStateREADY)
		in := &cvpmodels.BatchBackupPolicyV1beta{BackupPolicyID: "f0f0f0f0-f0f0-40f0-80f0-f0f0f0f0f0f0", State: &st}
		out := convertCVPBatchBackupPolicyToGCP(in)
		assert.True(t, out.State.Set)
		assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateREADY, out.State.Value)
	})

	t.Run("ApplyBatchBackupPolicyFieldSelection_NilFieldSet_ClearsOptionals", func(t *testing.T) {
		bp := gcpgenserver.BatchBackupPolicyV1beta{
			BackupPolicyId: gcpgenserver.NewOptNilString("id"),
			ResourceId:     gcpgenserver.NewOptNilString("res"),
		}
		applyBatchBackupPolicyFieldSelection(&bp, nil)
		assert.True(t, bp.BackupPolicyId.Set)
		assert.False(t, bp.ResourceId.Set)
	})
}

func TestFetchBatchBackupPoliciesFromCVP_OmitsFieldQueryWhenParamsFieldsEmpty(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListBackupPolicies", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListBackupPoliciesParams) bool {
		if p.LocationID != "us-east4" || p.Body == nil || len(p.Body.BackupPolicyUUIDs) != 1 {
			return false
		}
		// Match CVP: no ?fields= when the client did not pass fields (minimal response).
		if len(p.Fields) != 0 {
			return false
		}
		return p.XCorrelationID == nil
	})).Return(&cvpBatch.V1betaBatchListBackupPoliciesOK{
		Payload: &cvpBatch.V1betaBatchListBackupPoliciesOKBody{
			BackupPolicies: []*cvpmodels.BatchBackupPolicyV1beta{
				nil,
				{BackupPolicyID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
			},
		},
	}, nil)

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	ctx := context.Background()
	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"}
	fieldSet := map[string]bool{"backupPolicyId": true}

	out, err := fetchBatchBackupPoliciesFromCVP(ctx, []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, params, fieldSet)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.True(t, out[0].BackupPolicyId.Set)
}

func TestFetchBatchBackupPoliciesFromCVP_WhitelistFieldsAndCorrelationID(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListBackupPolicies", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListBackupPoliciesParams) bool {
		if p.LocationID != "loc-1" || p.XCorrelationID == nil || *p.XCorrelationID != "corr-xyz" {
			return false
		}
		return assert.ObjectsAreEqual([]string{"backupPolicyId", "resourceId"}, p.Fields)
	})).Return(&cvpBatch.V1betaBatchListBackupPoliciesOK{
		Payload: &cvpBatch.V1betaBatchListBackupPoliciesOKBody{
			BackupPolicies: []*cvpmodels.BatchBackupPolicyV1beta{
				func() *cvpmodels.BatchBackupPolicyV1beta {
					rid := "res-from-cvp"
					desc := "desc"
					en := true
					vc := int64(2)
					dt := strfmt.DateTime(time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC))
					dl, wl, ml := int64(1), int64(0), int64(3)
					st := string(gcpgenserver.BatchBackupPolicyV1betaStateREADY)
					return &cvpmodels.BatchBackupPolicyV1beta{
						BatchBackupPolicyScheduleV1beta: cvpmodels.BatchBackupPolicyScheduleV1beta{
							DailyBackupLimit:   &dl,
							WeeklyBackupLimit:  &wl,
							MonthlyBackupLimit: &ml,
						},
						BackupPolicyID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
						ResourceID:     &rid,
						Description:    &desc,
						CreatedAt:      &dt,
						Enabled:        &en,
						VolumeCount:    &vc,
						State:          &st,
					}
				}(),
			},
		},
	}, nil)

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	params := gcpgenserver.V1betaBatchListBackupPoliciesParams{
		LocationId:     "loc-1",
		XCorrelationID: gcpgenserver.NewOptString("corr-xyz"),
		Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
			gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
		},
	}
	fieldSet := map[string]bool{
		"backupPolicyId": true,
		"resourceId":     true,
	}

	out, err := fetchBatchBackupPoliciesFromCVP(context.Background(), []string{"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}, params, fieldSet)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "res-from-cvp", out[0].ResourceId.Value)
}

func TestFetchBatchBackupPoliciesFromCVP_APIError(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListBackupPolicies", mock.Anything).Return(nil, errors.New("cvp down"))

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	_, err := fetchBatchBackupPoliciesFromCVP(
		context.Background(),
		[]string{"11111111-1111-4111-8111-111111111111"},
		gcpgenserver.V1betaBatchListBackupPoliciesParams{LocationId: "us-east4"},
		map[string]bool{"backupPolicyId": true},
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CVP batch list backup policies failed")
}

func TestV1betaBatchListBackupPolicies_VCPOnly_SkipsMissingUUID(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origHost }()
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	u1 := "11111111-1111-4111-8111-111111111111"
	u2 := "22222222-2222-4222-8222-222222222222"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{u1, u2}).
		Return(map[string]int64{u1: 1}, map[string]*coremodels.BackupPolicy{
			u1: {BackupPolicyUUID: u1, ResourceID: "only-u1", State: datamodel.LifeCycleStateREADY},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	ctx := authContext()
	res, err := h.V1betaBatchListBackupPolicies(ctx,
		&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{u1, u2}},
		gcpgenserver.V1betaBatchListBackupPoliciesParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
				gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
			},
		})
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 1)
	assert.Equal(t, u1, okRes.BackupPolicies[0].BackupPolicyId.Value)
}

func TestV1betaBatchListBackupPolicies_Parallel_SkipsMissingUUIDInVCP(t *testing.T) {
	origBE := backupEnabled
	backupEnabled = true
	defer func() { backupEnabled = origBE }()
	origHost := cvp.CVP_HOST
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = origHost }()
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	u1 := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	u2 := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.On("GetBackupPoliciesByUUIDs", mock.Anything, []string{u1, u2}).
		Return(map[string]int64{u1: 1}, map[string]*coremodels.BackupPolicy{
			u1: {BackupPolicyUUID: u1, ResourceID: "vcp-a", State: datamodel.LifeCycleStateREADY},
		}, nil)

	origFetch := fetchBatchBackupPoliciesFromCVPFn
	defer func() { fetchBatchBackupPoliciesFromCVPFn = origFetch }()
	fetchBatchBackupPoliciesFromCVPFn = func(context.Context, []string, gcpgenserver.V1betaBatchListBackupPoliciesParams, map[string]bool) ([]gcpgenserver.BatchBackupPolicyV1beta, error) {
		return []gcpgenserver.BatchBackupPolicyV1beta{
			{BackupPolicyId: gcpgenserver.NewOptNilString(u1), ResourceId: gcpgenserver.NewOptNilString("sde-a")},
			{BackupPolicyId: gcpgenserver.NewOptNilString(u2), ResourceId: gcpgenserver.NewOptNilString("sde-b")},
		}, nil
	}

	h := Handler{Orchestrator: mockOrch}
	ctx := authContext()
	res, err := h.V1betaBatchListBackupPolicies(ctx,
		&gcpgenserver.BatchBackupPolicyUUIDListV1beta{BackupPolicyUuids: []string{u1, u2}},
		gcpgenserver.V1betaBatchListBackupPoliciesParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListBackupPoliciesFieldsItem{
				gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemBackupPolicyId,
				gcpgenserver.V1betaBatchListBackupPoliciesFieldsItemResourceId,
			},
		})
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupPoliciesOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupPolicies, 2)
}

func TestMergeBatchBackupPolicyVCPAndSDE_NilFieldSet(t *testing.T) {
	u := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	vcp := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
		ResourceId:     gcpgenserver.NewOptNilString("keep-me"),
	}
	sde := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
		ResourceId:     gcpgenserver.NewOptNilString("sde-only"),
	}
	out := mergeBatchBackupPolicyVCPAndSDE(vcp, sde, nil)
	assert.Equal(t, "keep-me", out.ResourceId.Value)
}

func TestMergeBatchBackupPolicyVCPAndSDE_FillsFromSDEAndVolumeOnlyOnSDE(t *testing.T) {
	u := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	fieldSet := map[string]bool{
		"backupPolicyId":     true,
		"volumeCount":        true,
		"resourceId":         true,
		"description":        true,
		"createdAt":          true,
		"enabled":            true,
		"dailyBackupLimit":   true,
		"weeklyBackupLimit":  true,
		"monthlyBackupLimit": true,
		"state":              true,
	}
	vcp := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
	}
	sde := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId:     gcpgenserver.NewOptNilString(u),
		ResourceId:         gcpgenserver.NewOptNilString("from-sde"),
		Description:        gcpgenserver.NewOptNilString("sd"),
		CreatedAt:          gcpgenserver.NewOptNilDateTime(time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)),
		Enabled:            gcpgenserver.NewOptNilBool(true),
		VolumeCount:        gcpgenserver.NewOptNilInt(9),
		DailyBackupLimit:   gcpgenserver.NewOptNilInt(2),
		WeeklyBackupLimit:  gcpgenserver.NewOptNilInt(3),
		MonthlyBackupLimit: gcpgenserver.NewOptNilInt(4),
		State:              gcpgenserver.NewOptNilBatchBackupPolicyV1betaState(gcpgenserver.BatchBackupPolicyV1betaStateREADY),
	}
	out := mergeBatchBackupPolicyVCPAndSDE(vcp, sde, fieldSet)
	assert.Equal(t, "from-sde", out.ResourceId.Value)
	assert.Equal(t, 9, out.VolumeCount.Value)
	assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateREADY, out.State.Value)

	vcp2 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
		VolumeCount:    gcpgenserver.NewOptNilInt(2),
	}
	sde2 := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString(u),
		VolumeCount:    gcpgenserver.NewOptNilInt(7),
	}
	out2 := mergeBatchBackupPolicyVCPAndSDE(vcp2, sde2, fieldSet)
	assert.Equal(t, 9, out2.VolumeCount.Value)
}

func TestConvertBackupPolicyModelToBatchBackupPolicy_FieldSetNilAndOptionalFields(t *testing.T) {
	bp := &coremodels.BackupPolicy{
		BackupPolicyUUID: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
		ResourceID:       "r1",
		Description:      nil,
		State:            "",
		DailyBackupLimit: 1, WeeklyBackupLimit: 2, MonthlyBackupLimit: 3,
		Enabled: true,
	}
	minimal := convertBackupPolicyModelToBatchBackupPolicy(bp, 4, nil)
	assert.True(t, minimal.BackupPolicyId.Set)
	assert.False(t, minimal.ResourceId.Set)

	full := map[string]bool{
		"backupPolicyId": true, "resourceId": true, "description": true, "createdAt": true,
		"enabled": true, "volumeCount": true, "dailyBackupLimit": true, "weeklyBackupLimit": true,
		"monthlyBackupLimit": true, "state": true,
	}
	out := convertBackupPolicyModelToBatchBackupPolicy(bp, 4, full)
	assert.True(t, out.Description.Null)
	assert.True(t, out.State.Set)
	assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateSTATEUNSPECIFIED, out.State.Value)

	t.Run("NonNilDescriptionAndLifecycleState", func(t *testing.T) {
		desc := "policy-desc"
		bp2 := &coremodels.BackupPolicy{
			BackupPolicyUUID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
			ResourceID:       "r2",
			Description:      &desc,
			State:            datamodel.LifeCycleStateREADY,
			CreatedAt:        time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			DailyBackupLimit: 1, WeeklyBackupLimit: 0, MonthlyBackupLimit: 0,
			Enabled: true,
		}
		out2 := convertBackupPolicyModelToBatchBackupPolicy(bp2, 0, full)
		assert.Equal(t, "policy-desc", out2.Description.Value)
		assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateREADY, out2.State.Value)
	})
}

func TestEnsureAndApplyBatchBackupPolicyFieldHelpers(t *testing.T) {
	t.Run("EnsureRequestedNilFieldSetIsNoOp", func(t *testing.T) {
		bp := gcpgenserver.BatchBackupPolicyV1beta{BackupPolicyId: gcpgenserver.NewOptNilString("x")}
		ensureRequestedFieldsPresentBatchBackupPolicy(&bp, nil)
		assert.Equal(t, "x", bp.BackupPolicyId.Value)
	})

	bp := gcpgenserver.BatchBackupPolicyV1beta{}
	fs := map[string]bool{
		"backupPolicyId": true, "resourceId": true, "description": true, "createdAt": true,
		"enabled": true, "volumeCount": true, "dailyBackupLimit": true, "weeklyBackupLimit": true,
		"monthlyBackupLimit": true, "state": true,
	}
	ensureRequestedFieldsPresentBatchBackupPolicy(&bp, fs)
	assert.True(t, bp.BackupPolicyId.Null)
	assert.True(t, bp.State.Set)
	assert.Equal(t, gcpgenserver.BatchBackupPolicyV1betaStateSTATEUNSPECIFIED, bp.State.Value)

	partial := gcpgenserver.BatchBackupPolicyV1beta{
		BackupPolicyId: gcpgenserver.NewOptNilString("id"),
		ResourceId:     gcpgenserver.NewOptNilString("res"),
		Description:    gcpgenserver.NewOptNilString("d"),
	}
	applyBatchBackupPolicyFieldSelection(&partial, map[string]bool{
		"backupPolicyId": true,
		"resourceId":     true,
	})
	assert.True(t, partial.BackupPolicyId.Set)
	assert.True(t, partial.ResourceId.Set)
	assert.False(t, partial.Description.Set)
}

func TestFieldSetWithBackupPolicyIDForMerge_NilReturnsNil(t *testing.T) {
	assert.Nil(t, fieldSetWithBackupPolicyIDForMerge(nil))
}

func TestConvertCVPBatchBackupPolicyToGCP_AllPointerFields(t *testing.T) {
	rid := "res"
	desc := "d"
	en := false
	vc := int64(11)
	dl, wl, ml := int64(5), int64(6), int64(7)
	dt := strfmt.DateTime(time.Date(2022, 8, 9, 0, 0, 0, 0, time.UTC))
	st := string(gcpgenserver.BatchBackupPolicyV1betaStateREADY)
	in := &cvpmodels.BatchBackupPolicyV1beta{
		BatchBackupPolicyScheduleV1beta: cvpmodels.BatchBackupPolicyScheduleV1beta{
			DailyBackupLimit:   &dl,
			WeeklyBackupLimit:  &wl,
			MonthlyBackupLimit: &ml,
		},
		BackupPolicyID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		ResourceID:     &rid,
		Description:    &desc,
		CreatedAt:      &dt,
		Enabled:        &en,
		VolumeCount:    &vc,
		State:          &st,
	}
	out := convertCVPBatchBackupPolicyToGCP(in)
	assert.True(t, out.ResourceId.Set)
	assert.True(t, out.VolumeCount.Set)
	assert.True(t, out.DailyBackupLimit.Set)
	assert.True(t, out.WeeklyBackupLimit.Set)
	assert.True(t, out.MonthlyBackupLimit.Set)
	assert.True(t, out.State.Set)
}

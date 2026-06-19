package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func stubFetchBatchQuotaRulesFromCVP(rules []gcpgenserver.BatchQuotaRuleV1beta, err error) func() {
	orig := fetchBatchQuotaRulesFromCVPFn
	fetchBatchQuotaRulesFromCVPFn = func(_ context.Context, _ []string, _ gcpgenserver.V1betaBatchListQuotaRulesParams, _ map[string]bool) ([]gcpgenserver.BatchQuotaRuleV1beta, error) {
		return rules, err
	}
	return func() { fetchBatchQuotaRulesFromCVPFn = orig }
}

func makeVCPQuotaRule(uuid, name, state string) *models.QuotaRule {
	return &models.QuotaRule{
		BaseModel:             models.BaseModel{UUID: uuid, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:                  name,
		QuotaType:             "INDIVIDUAL_USER_QUOTA",
		DiskLimitInMib:        1024,
		QuotaTarget:           "/vol",
		LifeCycleState:        state,
		LifeCycleStateDetails: "Available",
		Description:           "desc",
	}
}

func makeSDEQuotaRule(uuid, resourceID string) gcpgenserver.BatchQuotaRuleV1beta {
	return gcpgenserver.BatchQuotaRuleV1beta{
		QuotaId:        gcpgenserver.NewOptNilString(uuid),
		ResourceId:     gcpgenserver.NewOptNilString(resourceID),
		QuotaType:      gcpgenserver.NewOptNilBatchQuotaRuleV1betaQuotaType(gcpgenserver.BatchQuotaRuleV1betaQuotaTypeINDIVIDUALUSERQUOTA),
		DiskLimitInMib: gcpgenserver.NewOptNilInt64(2048),
		State:          gcpgenserver.NewOptNilBatchQuotaRuleV1betaState(gcpgenserver.BatchQuotaRuleV1betaStateREADY),
	}
}

func TestProjectBatchQuotaRuleForRequestedFields(t *testing.T) {
	t.Run("SetsDefaultStateWhenRequestedAndMissing", func(tt *testing.T) {
		rule := &gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "rule-1",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA,
			DiskLimitInMib: 1024,
		}
		fieldSet := map[string]bool{"state": true}

		projected := projectBatchQuotaRuleForRequestedFields(rule, fieldSet)

		require.True(tt, projected.State.IsSet())
		assert.Equal(tt, gcpgenserver.BatchQuotaRuleV1betaStateSTATEUNSPECIFIED, projected.State.Value)
	})

	t.Run("SetsDefaultQuotaTypeWhenRequestedAndMissing", func(tt *testing.T) {
		rule := &gcpgenserver.QuotaRulesV1beta{
			ResourceId:     "rule-1",
			DiskLimitInMib: 1024,
		}
		fieldSet := map[string]bool{"quotaType": true}

		projected := projectBatchQuotaRuleForRequestedFields(rule, fieldSet)

		assert.True(tt, projected.QuotaType.IsSet())
		assert.Equal(tt, gcpgenserver.BatchQuotaRuleV1betaQuotaTypeINDIVIDUALUSERQUOTA, projected.QuotaType.Value)
	})

	t.Run("ReturnsOnlyQuotaIdWhenNoFieldSet", func(tt *testing.T) {
		rule := &gcpgenserver.QuotaRulesV1beta{
			QuotaId:        gcpgenserver.NewOptString("uuid-1"),
			ResourceId:     "rule-1",
			DiskLimitInMib: 1024,
		}

		projected := projectBatchQuotaRuleForRequestedFields(rule, nil)

		assert.True(tt, projected.QuotaId.IsSet())
		assert.Equal(tt, "uuid-1", projected.QuotaId.Value)
		assert.False(tt, projected.ResourceId.IsSet())
		assert.False(tt, projected.State.IsSet())
		assert.False(tt, projected.QuotaType.IsSet())
	})

	t.Run("NilRule_ReturnsEmptyBatchRow", func(tt *testing.T) {
		out := projectBatchQuotaRuleForRequestedFields(nil, map[string]bool{"resourceId": true})
		assert.False(tt, out.QuotaId.IsSet())
	})

	t.Run("ProjectsOptionalFieldsWhenRequested", func(tt *testing.T) {
		now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		rule := &gcpgenserver.QuotaRulesV1beta{
			QuotaId:        gcpgenserver.NewOptString("qid-1"),
			ResourceId:     "res-1",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALGROUPQUOTA,
			DiskLimitInMib: 4096,
			QuotaTarget:    gcpgenserver.NewOptString("/unix"),
			State:          gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaStateREADY),
			StateDetails:   gcpgenserver.NewOptString("ready detail"),
			Description:    gcpgenserver.NewOptString("rule desc"),
			CreatedAt:      gcpgenserver.NewOptDateTime(now),
			UpdatedAt:      gcpgenserver.NewOptDateTime(now.Add(time.Hour)),
		}
		fs := map[string]bool{
			"resourceId": true, "quotaType": true, "diskLimitInMib": true, "quotaTarget": true,
			"state": true, "stateDetails": true, "description": true, "createdAt": true, "updatedAt": true,
		}
		out := projectBatchQuotaRuleForRequestedFields(rule, fs)
		assert.True(tt, out.ResourceId.IsSet())
		assert.Equal(tt, gcpgenserver.BatchQuotaRuleV1betaQuotaTypeINDIVIDUALGROUPQUOTA, out.QuotaType.Value)
		assert.Equal(tt, int64(4096), out.DiskLimitInMib.Value)
		assert.True(tt, out.QuotaTarget.IsSet())
		assert.Equal(tt, gcpgenserver.BatchQuotaRuleV1betaStateREADY, out.State.Value)
		assert.True(tt, out.StateDetails.IsSet())
		assert.True(tt, out.Description.IsSet())
		assert.True(tt, out.CreatedAt.IsSet())
		assert.True(tt, out.UpdatedAt.IsSet())
	})

	t.Run("RequestedEmptyOptionalFieldsSerializeAsNull", func(tt *testing.T) {
		rule := &gcpgenserver.QuotaRulesV1beta{
			QuotaId:        gcpgenserver.NewOptString("qid-null"),
			ResourceId:     "res-null",
			QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA,
			DiskLimitInMib: 0,
			QuotaTarget:    gcpgenserver.NewOptString(""),
			StateDetails:   gcpgenserver.NewOptString(""),
			Description:    gcpgenserver.NewOptString(""),
		}
		fs := map[string]bool{
			"quotaType":      true,
			"diskLimitInMib": true,
			"quotaTarget":    true,
			"stateDetails":   true,
			"description":    true,
		}
		out := projectBatchQuotaRuleForRequestedFields(rule, fs)
		raw, err := json.Marshal(&out)
		require.NoError(tt, err)
		assert.Contains(tt, string(raw), `"quotaTarget":null`)
		assert.Contains(tt, string(raw), `"diskLimitInMib":null`)
		assert.Contains(tt, string(raw), `"stateDetails":null`)
		assert.Contains(tt, string(raw), `"description":null`)
		assert.Contains(tt, string(raw), `"quotaType":"DEFAULT_USER_QUOTA"`)
	})
}

func TestV1betaBatchListQuotaRules_NoHTTPRequest_ReturnsUnauthorized(t *testing.T) {
	orig := enableQuotaRule
	defer func() { enableQuotaRule = orig }()
	enableQuotaRule = true
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}

	req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"qr-1"}}
	params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

	res, err := handler.V1betaBatchListQuotaRules(context.Background(), req, params)
	require.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesUnauthorized)
	assert.True(t, ok)
}

func TestV1betaBatchListQuotaRules_AuthFails_ReturnsUnauthorized(t *testing.T) {
	orig := enableQuotaRule
	defer func() { enableQuotaRule = orig }()
	enableQuotaRule = true
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(false)
	defer restoreAuth()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}

	req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"qr-1"}}
	params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

	res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
	require.NoError(t, err)
	_, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesUnauthorized)
	assert.True(t, ok)
}

func TestV1betaBatchListQuotaRules_FeatureDisabled(t *testing.T) {
	orig := enableQuotaRule
	defer func() { enableQuotaRule = orig }()
	enableQuotaRule = false

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()

	req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"qr-1"}}
	params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

	res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
	require.NoError(t, err)
	bad, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesBadRequest)
	require.True(t, ok)
	assert.Equal(t, quotaRuleAPIDisabledMessage, bad.Message)
}

func TestV1betaBatchListQuotaRules_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesBadRequest)
		assert.True(tt, ok)
	})

	t.Run("NilQuotaRuleUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: nil}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "quotaRuleUuids is required")
	})

	t.Run("EmptyQuotaRuleUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "quotaRuleUuids is required")
	})

	t.Run("TooManyUUIDs_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, env.MaxBatchQuotaRuleUUIDs+1)
		for i := range uuids {
			uuids[i] = "uuid"
		}
		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: uuids}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, fmt.Sprintf("at most %d", env.MaxBatchQuotaRuleUUIDs))
	})
}

func TestV1betaBatchListQuotaRules_VCPOnly(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		qr := makeVCPQuotaRule("vcp-qr-1", "qr-name", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetQuotaRulesByUUIDs", ctx, []string{"vcp-qr-1"}).
			Return([]*models.QuotaRule{qr}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"vcp-qr-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListQuotaRulesFieldsItem{gcpgenserver.V1betaBatchListQuotaRulesFieldsItemResourceId},
		}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 1)
		assert.Equal(tt, "vcp-qr-1", okRes.QuotaRules[0].QuotaId.Value)
		assert.True(tt, okRes.QuotaRules[0].ResourceId.IsSet())
		assert.Equal(tt, "qr-name", okRes.QuotaRules[0].ResourceId.Value)
	})

	t.Run("VCPFails_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetQuotaRulesByUUIDs", ctx, []string{"uuid-1"}).
			Return(nil, errors.New("database error"))

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("EmptyResult_ReturnsEmptyArray", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"nonexistent"}).
			Return([]*models.QuotaRule{}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"nonexistent"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.QuotaRules)
	})

	t.Run("SkipsNilQuotaRuleInVCPResults", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		qr := makeVCPQuotaRule("vcp-qr-1", "qr-name", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"vcp-qr-1"}).
			Return([]*models.QuotaRule{nil, qr}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"vcp-qr-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 1)
		assert.Equal(tt, "vcp-qr-1", okRes.QuotaRules[0].QuotaId.Value)
	})
}

func TestV1betaBatchListQuotaRules_Parallel(t *testing.T) {
	t.Run("BothSucceed_CombinesResults", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		sdeRule := makeSDEQuotaRule("sde-qr-1", "sde-resource")
		restoreCVP := stubFetchBatchQuotaRulesFromCVP([]gcpgenserver.BatchQuotaRuleV1beta{sdeRule}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpRule := makeVCPQuotaRule("vcp-qr-1", "vcp-resource", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"vcp-qr-1", "sde-qr-1"}).
			Return([]*models.QuotaRule{vcpRule}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"vcp-qr-1", "sde-qr-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 2)

		ids := map[string]bool{}
		for _, q := range okRes.QuotaRules {
			ids[q.QuotaId.Value] = true
		}
		assert.True(tt, ids["vcp-qr-1"])
		assert.True(tt, ids["sde-qr-1"])
	})

	t.Run("SkipsNilVCPRuleWhenMergingWithSDE", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		sdeRule := makeSDEQuotaRule("sde-qr-1", "sde-resource")
		restoreCVP := stubFetchBatchQuotaRulesFromCVP([]gcpgenserver.BatchQuotaRuleV1beta{sdeRule}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		good := makeVCPQuotaRule("vcp-qr-1", "vcp-resource", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"vcp-qr-1", "sde-qr-1"}).
			Return([]*models.QuotaRule{nil, good}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"vcp-qr-1", "sde-qr-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 2)
	})

	t.Run("VCPFails_SDESucceeds_ReturnsSDEOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		sdeRule := makeSDEQuotaRule("sde-qr-1", "sde-resource")
		restoreCVP := stubFetchBatchQuotaRulesFromCVP([]gcpgenserver.BatchQuotaRuleV1beta{sdeRule}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"uuid-1"}).
			Return(nil, errors.New("VCP database error"))

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 1)
		assert.Equal(tt, "sde-qr-1", okRes.QuotaRules[0].QuotaId.Value)
	})

	t.Run("VCPSucceeds_CVPFails_ReturnsVCPOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchQuotaRulesFromCVP(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpRule := makeVCPQuotaRule("vcp-qr-1", "vcp-resource", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"vcp-qr-1"}).
			Return([]*models.QuotaRule{vcpRule}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"vcp-qr-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		require.Len(tt, okRes.QuotaRules, 1)
		assert.Equal(tt, "vcp-qr-1", okRes.QuotaRules[0].QuotaId.Value)
	})

	t.Run("BothFail_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchQuotaRulesFromCVP(nil, errors.New("CVP down"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"uuid-1"}).
			Return(nil, errors.New("VCP down"))

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("BothReturnEmpty_ReturnsEmptyArray", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchQuotaRulesFromCVP([]gcpgenserver.BatchQuotaRuleV1beta{}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetQuotaRulesByUUIDs", mock.Anything, []string{"nonexistent"}).
			Return([]*models.QuotaRule{}, nil)

		req := &gcpgenserver.QuotaRuleIdListV1beta{QuotaRuleUuids: []string{"nonexistent"}}
		params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListQuotaRules(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListQuotaRulesOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.QuotaRules)
	})
}

func TestFetchBatchQuotaRulesFromCVP_NoFieldsNoCorrelation(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListQuotaRules", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListQuotaRulesParams) bool {
		if p.LocationID != "us-east4" || p.Body == nil || len(p.Body.QuotaRuleUUIDs) != 1 {
			return false
		}
		if len(p.Fields) != 0 || p.XCorrelationID != nil {
			return false
		}
		return true
	})).Return(&cvpBatch.V1betaBatchListQuotaRulesOK{Payload: []*cvpmodels.QuotaRulesV1beta{}}, nil)

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	ctx := context.Background()
	params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}
	out, err := fetchBatchQuotaRulesFromCVP(ctx, []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, params, map[string]bool{"quotaId": true})
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestFetchBatchQuotaRulesFromCVP_FieldsCorrelationAndPayload(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	rid := "my-rule"
	qt := "INDIVIDUAL_USER_QUOTA"
	dl := int64(512)
	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListQuotaRules", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListQuotaRulesParams) bool {
		if p.LocationID != "eu-west1" || p.XCorrelationID == nil || *p.XCorrelationID != "corr-1" {
			return false
		}
		return assert.ObjectsAreEqual([]string{"resourceId", "quotaType", "quotaId"}, p.Fields)
	})).Return(&cvpBatch.V1betaBatchListQuotaRulesOK{
		Payload: []*cvpmodels.QuotaRulesV1beta{
			nil,
			{QuotaID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ResourceID: &rid, QuotaType: &qt, DiskLimitInMib: &dl},
		},
	}, nil)

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	params := gcpgenserver.V1betaBatchListQuotaRulesParams{
		LocationId:     "eu-west1",
		XCorrelationID: gcpgenserver.NewOptString("corr-1"),
		Fields: []gcpgenserver.V1betaBatchListQuotaRulesFieldsItem{
			gcpgenserver.V1betaBatchListQuotaRulesFieldsItemResourceId,
			gcpgenserver.V1betaBatchListQuotaRulesFieldsItemQuotaType,
		},
	}
	fieldSet := map[string]bool{"resourceId": true, "quotaType": true}
	out, err := fetchBatchQuotaRulesFromCVP(context.Background(), []string{"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}, params, fieldSet)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.True(t, out[0].ResourceId.IsSet())
	assert.Equal(t, "my-rule", out[0].ResourceId.Value)
}

func TestFetchBatchQuotaRulesFromCVP_APIError(t *testing.T) {
	origClient := createClient
	defer func() { createClient = origClient }()

	batchMock := cvpBatch.NewMockClientService(t)
	batchMock.On("V1betaBatchListQuotaRules", mock.Anything).Return(nil, errors.New("cvp unavailable"))

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: batchMock}
	}

	params := gcpgenserver.V1betaBatchListQuotaRulesParams{LocationId: "us-east4"}
	_, err := fetchBatchQuotaRulesFromCVP(context.Background(), []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, params, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CVP batch list quota rules failed")
}

func TestBuildBatchQuotaRuleFieldSet(t *testing.T) {
	assert.Nil(t, buildBatchQuotaRuleFieldSet(nil))
	assert.Nil(t, buildBatchQuotaRuleFieldSet([]gcpgenserver.V1betaBatchListQuotaRulesFieldsItem{}))
	fs := buildBatchQuotaRuleFieldSet([]gcpgenserver.V1betaBatchListQuotaRulesFieldsItem{
		gcpgenserver.V1betaBatchListQuotaRulesFieldsItemDiskLimitInMib,
		gcpgenserver.V1betaBatchListQuotaRulesFieldsItemDescription,
	})
	require.Len(t, fs, 2)
	assert.True(t, fs["diskLimitInMib"])
	assert.True(t, fs["description"])
}

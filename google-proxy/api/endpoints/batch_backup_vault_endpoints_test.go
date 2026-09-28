package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func stubCVPBackupVaultFetch(vaults []gcpgenserver.BatchBackupVaultV1beta, err error) func() {
	orig := fetchBatchBackupVaultsFromCVPFn
	fetchBatchBackupVaultsFromCVPFn = func(_ context.Context, _ []string, _ gcpgenserver.V1betaBatchListBackupVaultsParams, fieldSet map[string]bool) ([]gcpgenserver.BatchBackupVaultV1beta, error) {
		result := make([]gcpgenserver.BatchBackupVaultV1beta, 0, len(vaults))
		for _, v := range vaults {
			bv := v
			applyBatchBVFieldSelection(&bv, fieldSet)
			result = append(result, bv)
		}
		return result, err
	}
	return func() { fetchBatchBackupVaultsFromCVPFn = orig }
}

func makeVCPBackupVault(uuid, name, state string) *models.BackupVaultV1beta {
	vaultType := "IN_REGION"
	desc := "test backup vault"
	return &models.BackupVaultV1beta{
		BackupVaultID:         uuid,
		Name:                  name,
		LifeCycleState:        state,
		LifeCycleStateDetails: "ready",
		Description:           &desc,
		BackupVaultType:       &vaultType,
		CreatedAt:             time.Now(),
		BackupRetentionPolicy: models.BackupRetentionPolicyparams{
			BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
			IsDailyBackupImmutable:                 true,
		},
	}
}

func makeCVPBatchBackupVault(uuid, resourceId, state string) gcpgenserver.BatchBackupVaultV1beta {
	return gcpgenserver.BatchBackupVaultV1beta{
		BackupVaultId: gcpgenserver.NewOptString(uuid),
		ResourceId:    gcpgenserver.NewOptNilString(resourceId),
		State:         gcpgenserver.NewOptNilBatchBackupVaultV1betaState(gcpgenserver.BatchBackupVaultV1betaState(state)),
	}
}

// ============================================================
// Auth Tests
// ============================================================

func TestV1betaBatchListBackupVaults_Auth(t *testing.T) {
	t.Run("InvalidJWT_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(false)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		logger := log.NewLogger()
		ctx := context.WithValue(context.Background(), utilsmiddleware.ContextSLoggerKey, logger)
		ctx = context.WithValue(ctx, utilsmiddleware.HeaderContextKey, http.Header{
			"Authorization": []string{"invalid-jwt-token"},
		})

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsUnauthorized)
		require.True(tt, ok, "expected Unauthorized response")
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
		assert.Equal(tt, "Authentication failure", unauthRes.Message)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := context.Background()

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsUnauthorized)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
	})
}

// ============================================================
// Validation Tests
// ============================================================

func TestV1betaBatchListBackupVaults_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListBackupVaults(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptyBackupVaultUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "backupVaultUuids is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, env.MaxBatchBackupVaultUUIDs+1)
		for i := range uuids {
			uuids[i] = "uuid-" + time.Now().Format("150405.000000000") + "-" + toString(i)
		}
		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: uuids}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most")
	})

	t.Run("MalformedUUID_ReturnsBadRequestBeforeFetching", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		// The orchestrator must never be called when the request fails UUID
		// format validation; an unset mock would error out if it were.
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{
			"11111111-1111-1111-1111-111111111111",
			"not-a-uuid",
		}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsBadRequest)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		// The message must call out the offending index in the original request
		// (1 here) and embed the UUID regex so clients know what to fix.
		assert.Contains(tt, badReq.Message, "backupVaultUuids.1 in body should match")
		assert.Contains(tt, badReq.Message, "[a-fA-F0-9]{8}")
	})

	t.Run("EmptyStringUUID_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{""}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "backupVaultUuids.0 in body should match")
	})
}

// ============================================================
// VCP-Only Tests
// ============================================================

func TestV1betaBatchListBackupVaults_VCPOnly(t *testing.T) {
	t.Run("Success_WithFields", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "my-vault", "READY")
		mockOrch.On("GetMultipleBackupVaults", ctx, []string{"11111111-1111-1111-1111-111111111111"}).
			Return([]*models.BackupVaultV1beta{bv}, nil)

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"11111111-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{
				gcpgenserver.V1betaBatchListBackupVaultsFieldsItem("resourceId"),
				gcpgenserver.V1betaBatchListBackupVaultsFieldsItem("state"),
			},
		}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 1)
		assert.True(tt, okRes.BackupVaults[0].BackupVaultId.Set, "backupVaultId must always be present")
		assert.Equal(tt, "11111111-1111-1111-1111-111111111111", okRes.BackupVaults[0].BackupVaultId.Value)
		assert.Equal(tt, "my-vault", okRes.BackupVaults[0].ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchBackupVaultV1betaStateREADY, okRes.BackupVaults[0].State.Value)
		assert.False(tt, okRes.BackupVaults[0].Description.Set, "not requested")
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

		mockOrch.On("GetMultipleBackupVaults", ctx, []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}).
			Return(nil, errors.New("database error"))

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("NoFieldsRequested_ReturnsOnlyBackupVaultId", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "my-vault", "READY")
		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"11111111-1111-1111-1111-111111111111"}).
			Return([]*models.BackupVaultV1beta{bv}, nil)

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"11111111-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 1)
		assert.Equal(tt, "11111111-1111-1111-1111-111111111111", okRes.BackupVaults[0].BackupVaultId.Value)
		assert.False(tt, okRes.BackupVaults[0].ResourceId.Set)
		assert.False(tt, okRes.BackupVaults[0].State.Set)
	})

	t.Run("DeletedVaults_AreFilteredOut", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		activeBV := makeVCPBackupVault("44444444-4444-4444-4444-444444444444", "active-vault", "READY")
		deletedAt := time.Now()
		deletedBV := makeVCPBackupVault("55555555-5555-5555-5555-555555555555", "deleted-vault", "DELETED")
		deletedBV.DeletedAt = &deletedAt

		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555"}).
			Return([]*models.BackupVaultV1beta{activeBV, deletedBV}, nil)

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 1, "deleted backup vault must be filtered out")
		assert.Equal(tt, "44444444-4444-4444-4444-444444444444", okRes.BackupVaults[0].BackupVaultId.Value)
	})
}

// ============================================================
// Parallel (CVP+VCP) Tests
// ============================================================

func TestV1betaBatchListBackupVaults_Parallel(t *testing.T) {
	t.Run("BothSucceed_CombinesResults", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		cvpBV := makeCVPBatchBackupVault("99999999-9999-9999-9999-999999999999", "sde-vault", "READY")
		restoreCVP := stubCVPBackupVaultFetch([]gcpgenserver.BatchBackupVaultV1beta{cvpBV}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpBV := makeVCPBackupVault("88888888-8888-8888-8888-888888888888", "vcp-vault", "READY")
		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"88888888-8888-8888-8888-888888888888", "99999999-9999-9999-9999-999999999999"}).
			Return([]*models.BackupVaultV1beta{vcpBV}, nil)

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"88888888-8888-8888-8888-888888888888", "99999999-9999-9999-9999-999999999999"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{
				gcpgenserver.V1betaBatchListBackupVaultsFieldsItem("resourceId"),
				gcpgenserver.V1betaBatchListBackupVaultsFieldsItem("state"),
			},
		}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 2)

		resourceIDs := map[string]bool{}
		for _, bv := range okRes.BackupVaults {
			assert.True(tt, bv.BackupVaultId.Set, "backupVaultId must always be present")
			resourceIDs[bv.ResourceId.Value] = true
		}
		assert.True(tt, resourceIDs["vcp-vault"], "VCP vault should be in results")
		assert.True(tt, resourceIDs["sde-vault"], "SDE vault should be in results")
	})

	t.Run("VCPFails_SDESucceeds_ReturnsSDEOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		cvpBV := makeCVPBatchBackupVault("99999999-9999-9999-9999-999999999999", "sde-vault", "READY")
		restoreCVP := stubCVPBackupVaultFetch([]gcpgenserver.BatchBackupVaultV1beta{cvpBV}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}).
			Return(nil, errors.New("VCP database error"))

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 1)
		assert.Equal(tt, "99999999-9999-9999-9999-999999999999", okRes.BackupVaults[0].BackupVaultId.Value)
	})

	t.Run("VCPSucceeds_SDEFails_ReturnsVCPOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubCVPBackupVaultFetch(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpBV := makeVCPBackupVault("88888888-8888-8888-8888-888888888888", "vcp-vault", "READY")
		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"88888888-8888-8888-8888-888888888888"}).
			Return([]*models.BackupVaultV1beta{vcpBV}, nil)

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"88888888-8888-8888-8888-888888888888"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{gcpgenserver.V1betaBatchListBackupVaultsFieldsItem("resourceId")},
		}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.BackupVaults, 1)
		assert.True(tt, okRes.BackupVaults[0].BackupVaultId.Set, "backupVaultId must always be present")
		assert.Equal(tt, "88888888-8888-8888-8888-888888888888", okRes.BackupVaults[0].BackupVaultId.Value)
		assert.Equal(tt, "vcp-vault", okRes.BackupVaults[0].ResourceId.Value)
	})

	t.Run("BothFail_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubCVPBackupVaultFetch(nil, errors.New("CVP down"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}).
			Return(nil, errors.New("VCP down"))

		req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}
		params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsInternalServerError)
		assert.True(tt, ok)
	})
}

// ============================================================
// State + Field Selection Tests
// ============================================================

func TestConvertBackupVaultToBatchBackupVault_UnknownStateDefaultsToStateUnspecified(t *testing.T) {
	bv := makeVCPBackupVault("33333333-3333-3333-3333-333333333333", "res", "NOT_A_VALID_STATE")
	fieldSet := map[string]bool{"state": true}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
	require.True(t, bp.State.Set)
	assert.False(t, bp.State.Null)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED, bp.State.Value)
}

func TestEnsureRequestedBVFieldsPresent_SetsUnsetFieldsToNull(t *testing.T) {
	bp := gcpgenserver.BatchBackupVaultV1beta{}
	fieldSet := map[string]bool{
		"backupVaultId":            true,
		"resourceId":               true,
		"description":              true,
		"createdAt":                true,
		"state":                    true,
		"stateDetails":             true,
		"backupVaultType":          true,
		"sourceRegion":             true,
		"backupRegion":             true,
		"sourceBackupVault":        true,
		"destinationBackupVault":   true,
		"backupRetentionPolicy":    true,
		"encryptionState":          true,
		"backupsPrimaryKeyVersion": true,
		"kmsConfigResourcePath":    true,
		"crossProjectVault":        true,
		"tenantProject":            true,
	}

	ensureRequestedBVFieldsPresent(&bp, fieldSet)

	assert.True(t, bp.BackupVaultId.Set, "backupVaultId should be present when requested")
	assert.True(t, bp.ResourceId.Set && bp.ResourceId.Null)
	assert.True(t, bp.Description.Set && bp.Description.Null)
	assert.True(t, bp.CreatedAt.Set && bp.CreatedAt.Null)
	assert.True(t, bp.StateDetails.Set && bp.StateDetails.Null)
	assert.True(t, bp.SourceRegion.Set && bp.SourceRegion.Null)
	assert.True(t, bp.BackupRegion.Set && bp.BackupRegion.Null)
	assert.True(t, bp.SourceBackupVault.Set && bp.SourceBackupVault.Null)
	assert.True(t, bp.DestinationBackupVault.Set && bp.DestinationBackupVault.Null)
	assert.True(t, bp.BackupsPrimaryKeyVersion.Set && bp.BackupsPrimaryKeyVersion.Null)
	assert.True(t, bp.KmsConfigResourcePath.Set && bp.KmsConfigResourcePath.Null)
	assert.True(t, bp.CrossProjectVault.Set && bp.CrossProjectVault.Null)
	assert.True(t, bp.TenantProject.Set && bp.TenantProject.Null)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED, bp.State.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaBackupVaultTypeTYPEUNSPECIFIED, bp.BackupVaultType.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED, bp.EncryptionState.Value)
	assert.True(t, bp.BackupRetentionPolicy.Set, "backupRetentionPolicy should be present as empty object when requested")
}

func TestEnsureRequestedBVFieldsPresent_NilFieldSet(t *testing.T) {
	bp := gcpgenserver.BatchBackupVaultV1beta{
		ResourceId: gcpgenserver.NewOptNilString("should-stay"),
	}
	ensureRequestedBVFieldsPresent(&bp, nil)
	assert.True(t, bp.ResourceId.Set)
	assert.Equal(t, "should-stay", bp.ResourceId.Value)
}

// ============================================================
// Dedup Tests
// ============================================================

func TestV1betaBatchListBackupVaults_Dedup_VCPPriority(t *testing.T) {
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = "" }()

	cvpBV := makeCVPBatchBackupVault("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "cvp-name", "READY")
	restoreCVP := stubCVPBackupVaultFetch([]gcpgenserver.BatchBackupVaultV1beta{cvpBV}, nil)
	defer restoreCVP()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}
	ctx := authContext()

	vcpBV := makeVCPBackupVault("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "vcp-name", "READY")
	mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}).
		Return([]*models.BackupVaultV1beta{vcpBV}, nil)

	req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}}
	params := gcpgenserver.V1betaBatchListBackupVaultsParams{
		LocationId: "us-east4",
		Fields:     []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{"resourceId"},
	}

	res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupVaults, 1, "duplicate should be removed")
	assert.Equal(t, "vcp-name", okRes.BackupVaults[0].ResourceId.Value, "VCP should take priority")
}

// ============================================================
// CROSS_REGION Conversion Tests
// ============================================================

func makeCrossRegionVCPBackupVault(uuid, name, accountName, sourceRegion, backupRegion, crossRegionName string) *models.BackupVaultV1beta {
	vaultType := "CROSS_REGION"
	return &models.BackupVaultV1beta{
		BackupVaultID:              uuid,
		Name:                       name,
		AccountName:                accountName,
		LifeCycleState:             "READY",
		LifeCycleStateDetails:      "ready",
		BackupVaultType:            &vaultType,
		SourceRegion:               &sourceRegion,
		BackupRegion:               &backupRegion,
		CrossRegionBackupVaultName: &crossRegionName,
		CreatedAt:                  time.Now(),
	}
}

func TestConvertBackupVault_CrossRegion_SourceVault(t *testing.T) {
	bv := makeCrossRegionVCPBackupVault(
		"cr-1", "source-vault", "my-project",
		"us-central1", "us-east4",
		"projects/my-project/locations/us-east4/backupVaults/dest-vault",
	)
	fieldSet := map[string]bool{
		"sourceBackupVault":      true,
		"destinationBackupVault": true,
		"sourceRegion":           true,
		"backupRegion":           true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.SourceBackupVault.Set)
	assert.Equal(t, "projects/my-project/locations/us-central1/backupVaults/source-vault", bp.SourceBackupVault.Value)
	assert.True(t, bp.DestinationBackupVault.Set)
	assert.Equal(t, "projects/my-project/locations/us-east4/backupVaults/dest-vault", bp.DestinationBackupVault.Value)
	assert.Equal(t, "us-central1", bp.SourceRegion.Value)
	assert.Equal(t, "us-east4", bp.BackupRegion.Value)
}

func TestConvertBackupVault_CrossRegion_DestinationVault(t *testing.T) {
	bv := makeCrossRegionVCPBackupVault(
		"cr-2", "dest-vault", "my-project",
		"us-central1", "us-east4",
		"projects/my-project/locations/us-central1/backupVaults/source-vault",
	)
	fieldSet := map[string]bool{
		"sourceBackupVault":      true,
		"destinationBackupVault": true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.SourceBackupVault.Set)
	assert.Equal(t, "projects/my-project/locations/us-central1/backupVaults/source-vault", bp.SourceBackupVault.Value)
	assert.True(t, bp.DestinationBackupVault.Set)
	assert.Equal(t, "projects/my-project/locations/us-east4/backupVaults/dest-vault", bp.DestinationBackupVault.Value)
}

func TestConvertBackupVault_InRegion_NullsCrossRegionFields(t *testing.T) {
	bv := makeVCPBackupVault("ir-1", "my-vault", "READY")
	bv.SourceRegion = nillable.GetStringPtr("us-east4")
	fieldSet := map[string]bool{
		"sourceRegion":           true,
		"backupRegion":           true,
		"sourceBackupVault":      true,
		"destinationBackupVault": true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.SourceRegion.Set && bp.SourceRegion.Null, "sourceRegion should be null for IN_REGION")
	assert.True(t, bp.BackupRegion.Set && bp.BackupRegion.Null, "backupRegion should be null for IN_REGION")
	assert.True(t, bp.SourceBackupVault.Set && bp.SourceBackupVault.Null)
	assert.True(t, bp.DestinationBackupVault.Set && bp.DestinationBackupVault.Null)
}

// ============================================================
// EncryptionState Tests
// ============================================================

func TestConvertBackupVault_EncryptionState_DefaultUnspecified(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	fieldSet := map[string]bool{"encryptionState": true}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.EncryptionState.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED, bp.EncryptionState.Value)
}

func TestConvertBackupVault_EncryptionState_RealValue(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	enc := "ENCRYPTION_STATE_COMPLETED"
	bv.EncryptionState = &enc
	fieldSet := map[string]bool{"encryptionState": true}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.EncryptionState.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionState("ENCRYPTION_STATE_COMPLETED"), bp.EncryptionState.Value)
}

// ============================================================
// CMEK Attributes Tests
// ============================================================

func TestConvertBackupVault_CmekAttributes(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	bv.KmsConfigResourcePath = nillable.GetStringPtr("projects/p/locations/l/kmsConfigs/k")
	bv.BackupsPrimaryKeyVersion = nillable.GetStringPtr("projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1")
	enc := "ENCRYPTION_STATE_COMPLETED"
	bv.EncryptionState = &enc

	fieldSet := map[string]bool{
		"kmsConfigResourcePath":    true,
		"backupsPrimaryKeyVersion": true,
		"encryptionState":          true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.Equal(t, "projects/p/locations/l/kmsConfigs/k", bp.KmsConfigResourcePath.Value)
	assert.Equal(t, "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1", bp.BackupsPrimaryKeyVersion.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionState("ENCRYPTION_STATE_COMPLETED"), bp.EncryptionState.Value)
}

// ============================================================
// CrossProjectVault Tests
// ============================================================

func TestConvertBackupVault_CrossProjectVault(t *testing.T) {
	t.Run("CrossProject_True", func(tt *testing.T) {
		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
		bv.ServiceType = datamodel.ServiceTypeCrossProject
		fieldSet := map[string]bool{"crossProjectVault": true}
		bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
		assert.True(tt, bp.CrossProjectVault.Set)
		assert.False(tt, bp.CrossProjectVault.Null)
		assert.True(tt, bp.CrossProjectVault.Value)
	})

	t.Run("GCNV_Null", func(tt *testing.T) {
		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
		bv.ServiceType = datamodel.ServiceTypeGCNV
		fieldSet := map[string]bool{"crossProjectVault": true}
		bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
		assert.True(tt, bp.CrossProjectVault.Set)
		assert.True(tt, bp.CrossProjectVault.Null)
	})
}

func TestConvertBackupVault_TenantProject(t *testing.T) {
	t.Run("CrossProject_WithTenantProject_PopulatesValue", func(tt *testing.T) {
		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
		bv.ServiceType = datamodel.ServiceTypeCrossProject
		bv.TenantProject = nillable.GetStringPtr("596181058421")
		fieldSet := map[string]bool{"crossProjectVault": true, "tenantProject": true}
		bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
		assert.True(tt, bp.TenantProject.Set)
		assert.False(tt, bp.TenantProject.Null)
		assert.Equal(tt, "596181058421", bp.TenantProject.Value)
	})

	t.Run("CrossProject_WithoutTenantProject_IsNull", func(tt *testing.T) {
		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
		bv.ServiceType = datamodel.ServiceTypeCrossProject
		fieldSet := map[string]bool{"crossProjectVault": true, "tenantProject": true}
		bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
		assert.True(tt, bp.TenantProject.Set)
		assert.True(tt, bp.TenantProject.Null)
	})

	t.Run("GCNV_TenantProjectIsNull", func(tt *testing.T) {
		bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
		bv.ServiceType = datamodel.ServiceTypeGCNV
		bv.TenantProject = nillable.GetStringPtr("596181058421")
		fieldSet := map[string]bool{"crossProjectVault": true, "tenantProject": true}
		bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)
		assert.True(tt, bp.TenantProject.Set)
		assert.True(tt, bp.TenantProject.Null, "non-CrossProject vaults should have null tenantProject")
	})
}

// ============================================================
// BackupRetentionPolicy Tests
// ============================================================

func TestConvertBackupVault_RetentionPolicy_OnlyTrueBooleans(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	bv.BackupRetentionPolicy = models.BackupRetentionPolicyparams{
		IsDailyBackupImmutable:                 true,
		IsWeeklyBackupImmutable:                false,
		IsMonthlyBackupImmutable:               false,
		IsAdhocBackupImmutable:                 true,
		BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(7),
	}
	fieldSet := map[string]bool{"backupRetentionPolicy": true}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	require.True(t, bp.BackupRetentionPolicy.Set)
	rp := bp.BackupRetentionPolicy.Value
	assert.True(t, rp.DailyBackupImmutable.Set)
	assert.True(t, rp.DailyBackupImmutable.Value)
	assert.False(t, rp.WeeklyBackupImmutable.Set, "false booleans should not be set")
	assert.False(t, rp.MonthlyBackupImmutable.Set, "false booleans should not be set")
	assert.True(t, rp.ManualBackupImmutable.Set)
	assert.True(t, rp.ManualBackupImmutable.Value)
	assert.Equal(t, 7, rp.BackupMinimumEnforcedRetentionDays.Value)
}

func TestConvertBackupVault_RetentionPolicy_NilDuration_DefaultsToZero(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	bv.BackupRetentionPolicy = models.BackupRetentionPolicyparams{}
	fieldSet := map[string]bool{"backupRetentionPolicy": true}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	require.True(t, bp.BackupRetentionPolicy.Set)
	assert.Equal(t, 0, bp.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.Value)
}

// ============================================================
// CVP Conversion Tests
// ============================================================

func TestConvertCVPBackupVault_EncryptionState_DefaultUnspecified(t *testing.T) {
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID: "cvp-1",
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	assert.True(t, bp.EncryptionState.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED, bp.EncryptionState.Value)
}

func TestConvertCVPBackupVault_EncryptionState_RealValue(t *testing.T) {
	enc := "ENCRYPTION_STATE_COMPLETED"
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID:   "cvp-1",
		EncryptionState: &enc,
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionState("ENCRYPTION_STATE_COMPLETED"), bp.EncryptionState.Value)
}

func TestConvertCVPBackupVault_RetentionPolicy_OnlyTrueBooleans(t *testing.T) {
	days := int64(14)
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID: "cvp-1",
		BackupRetentionPolicy: &cvpmodels.BackupRetentionPolicyV1beta{
			DailyBackupImmutable:               true,
			WeeklyBackupImmutable:              false,
			ManualBackupImmutable:              true,
			BackupMinimumEnforcedRetentionDays: &days,
		},
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)

	require.True(t, bp.BackupRetentionPolicy.Set)
	rp := bp.BackupRetentionPolicy.Value
	assert.True(t, rp.DailyBackupImmutable.Set)
	assert.True(t, rp.DailyBackupImmutable.Value)
	assert.False(t, rp.WeeklyBackupImmutable.Set)
	assert.True(t, rp.ManualBackupImmutable.Set)
	assert.Equal(t, 14, rp.BackupMinimumEnforcedRetentionDays.Value)
}

func TestConvertCVPBackupVault_CrossProjectVault_Nil_IsNull(t *testing.T) {
	p := &cvpmodels.BatchBackupVaultV1beta{BackupVaultID: "cvp-1"}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	assert.True(t, bp.CrossProjectVault.Set)
	assert.True(t, bp.CrossProjectVault.Null)
}

func TestConvertCVPBackupVault_CrossProjectVault_True(t *testing.T) {
	val := true
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID:     "cvp-1",
		CrossProjectVault: &val,
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	assert.True(t, bp.CrossProjectVault.Set)
	assert.False(t, bp.CrossProjectVault.Null)
	assert.True(t, bp.CrossProjectVault.Value)
}

func TestConvertCVPBackupVault_TenantProject(t *testing.T) {
	t.Run("CrossProject_WithTenantProject_ForwardsValue", func(tt *testing.T) {
		crossProject := true
		tenant := "596181058421"
		p := &cvpmodels.BatchBackupVaultV1beta{
			BackupVaultID:     "cvp-1",
			CrossProjectVault: &crossProject,
			TenantProject:     &tenant,
		}
		bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
		assert.True(tt, bp.TenantProject.Set)
		assert.False(tt, bp.TenantProject.Null)
		assert.Equal(tt, "596181058421", bp.TenantProject.Value)
	})

	t.Run("NotCrossProject_TenantProjectIsNull", func(tt *testing.T) {
		tenant := "596181058421"
		p := &cvpmodels.BatchBackupVaultV1beta{
			BackupVaultID: "cvp-1",
			TenantProject: &tenant,
		}
		bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
		assert.True(tt, bp.TenantProject.Set)
		assert.True(tt, bp.TenantProject.Null, "tenantProject should be null when not a cross-project vault")
	})

	t.Run("CrossProject_NilTenantProject_IsNull", func(tt *testing.T) {
		crossProject := true
		p := &cvpmodels.BatchBackupVaultV1beta{
			BackupVaultID:     "cvp-1",
			CrossProjectVault: &crossProject,
		}
		bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
		assert.True(tt, bp.TenantProject.Set)
		assert.True(tt, bp.TenantProject.Null)
	})
}

// ============================================================
// extractRegionFromResourcePath Tests
// ============================================================

func TestExtractRegionFromResourcePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"valid", "projects/p/locations/us-east4/backupVaults/bv", "us-east4"},
		{"valid_long_region", "projects/p/locations/us-central1/backupVaults/bv", "us-central1"},
		{"too_short", "projects/p/locations", ""},
		{"empty", "", ""},
		{"no_trailing", "projects/p/locations/us-east4", "us-east4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.expected, extractRegionFromResourcePath(tc.path))
		})
	}
}

// ============================================================
// applyBatchBVFieldSelection Tests
// ============================================================

func TestApplyBatchBVFieldSelection_NilFieldSet_ResetsAll(t *testing.T) {
	bp := gcpgenserver.BatchBackupVaultV1beta{
		BackupVaultId:         gcpgenserver.NewOptString("11111111-1111-1111-1111-111111111111"),
		ResourceId:            gcpgenserver.NewOptNilString("res"),
		Description:           gcpgenserver.NewOptNilString("desc"),
		SourceRegion:          gcpgenserver.NewOptNilString("us-east4"),
		EncryptionState:       gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{}),
	}
	applyBatchBVFieldSelection(&bp, nil)

	assert.True(t, bp.BackupVaultId.Set, "backupVaultId should remain")
	assert.False(t, bp.ResourceId.Set)
	assert.False(t, bp.Description.Set)
	assert.False(t, bp.SourceRegion.Set)
	assert.False(t, bp.EncryptionState.Set)
	assert.False(t, bp.BackupRetentionPolicy.Set)
}

func TestApplyBatchBVFieldSelection_AllFieldsRequested(t *testing.T) {
	bp := gcpgenserver.BatchBackupVaultV1beta{
		BackupVaultId:            gcpgenserver.NewOptString("11111111-1111-1111-1111-111111111111"),
		ResourceId:               gcpgenserver.NewOptNilString("res"),
		Description:              gcpgenserver.NewOptNilString("desc"),
		State:                    gcpgenserver.NewOptNilBatchBackupVaultV1betaState(gcpgenserver.BatchBackupVaultV1betaStateREADY),
		StateDetails:             gcpgenserver.NewOptNilString("ok"),
		BackupVaultType:          gcpgenserver.NewOptNilBatchBackupVaultV1betaBackupVaultType("IN_REGION"),
		SourceRegion:             gcpgenserver.NewOptNilString("us-east4"),
		BackupRegion:             gcpgenserver.NewOptNilString("us-central1"),
		SourceBackupVault:        gcpgenserver.NewOptNilString("projects/p/locations/l/backupVaults/bv"),
		DestinationBackupVault:   gcpgenserver.NewOptNilString("projects/p/locations/l2/backupVaults/bv2"),
		KmsConfigResourcePath:    gcpgenserver.NewOptNilString("kms-path"),
		BackupsPrimaryKeyVersion: gcpgenserver.NewOptNilString("key-v1"),
		EncryptionState:          gcpgenserver.NewOptNilBatchBackupVaultV1betaEncryptionState(gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED),
		BackupRetentionPolicy:    gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{}),
		CrossProjectVault:        gcpgenserver.NewOptNilBool(true),
	}
	fieldSet := map[string]bool{
		"resourceId": true, "description": true, "createdAt": true,
		"state": true, "stateDetails": true, "backupVaultType": true,
		"sourceRegion": true, "backupRegion": true,
		"sourceBackupVault": true, "destinationBackupVault": true,
		"kmsConfigResourcePath": true, "backupsPrimaryKeyVersion": true,
		"encryptionState": true, "backupRetentionPolicy": true,
		"crossProjectVault": true,
	}
	applyBatchBVFieldSelection(&bp, fieldSet)

	assert.True(t, bp.BackupVaultId.Set)
	assert.True(t, bp.ResourceId.Set)
	assert.True(t, bp.Description.Set)
	assert.True(t, bp.State.Set)
	assert.True(t, bp.StateDetails.Set)
	assert.True(t, bp.BackupVaultType.Set)
	assert.True(t, bp.SourceRegion.Set)
	assert.True(t, bp.BackupRegion.Set)
	assert.True(t, bp.SourceBackupVault.Set)
	assert.True(t, bp.DestinationBackupVault.Set)
	assert.True(t, bp.KmsConfigResourcePath.Set)
	assert.True(t, bp.BackupsPrimaryKeyVersion.Set)
	assert.True(t, bp.EncryptionState.Set)
	assert.True(t, bp.BackupRetentionPolicy.Set)
	assert.True(t, bp.CrossProjectVault.Set)
}

func TestApplyBatchBVFieldSelection_SpecificFields(t *testing.T) {
	bp := gcpgenserver.BatchBackupVaultV1beta{
		BackupVaultId: gcpgenserver.NewOptString("11111111-1111-1111-1111-111111111111"),
		ResourceId:    gcpgenserver.NewOptNilString("res"),
		Description:   gcpgenserver.NewOptNilString("desc"),
		SourceRegion:  gcpgenserver.NewOptNilString("us-east4"),
	}
	fieldSet := map[string]bool{"resourceId": true}
	applyBatchBVFieldSelection(&bp, fieldSet)

	assert.True(t, bp.BackupVaultId.Set, "backupVaultId always present")
	assert.True(t, bp.ResourceId.Set, "requested field should remain")
	assert.False(t, bp.Description.Set, "unrequested field should be reset")
	assert.False(t, bp.SourceRegion.Set, "unrequested field should be reset")
}

// ============================================================
// normalizeBatchBVState Tests
// ============================================================

func TestNormalizeBatchBVState(t *testing.T) {
	tests := []struct {
		input    string
		expected gcpgenserver.BatchBackupVaultV1betaState
	}{
		{"READY", gcpgenserver.BatchBackupVaultV1betaStateREADY},
		{"CREATING", gcpgenserver.BatchBackupVaultV1betaStateCREATING},
		{"UPDATING", gcpgenserver.BatchBackupVaultV1betaStateUPDATING},
		{"DELETING", gcpgenserver.BatchBackupVaultV1betaStateDELETING},
		{"DELETED", gcpgenserver.BatchBackupVaultV1betaStateDELETED},
		{"ERROR", gcpgenserver.BatchBackupVaultV1betaStateERROR},
		{"STATE_UNSPECIFIED", gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED},
		{"", gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED},
		{"INVALID", gcpgenserver.BatchBackupVaultV1betaStateSTATEUNSPECIFIED},
	}
	for _, tc := range tests {
		t.Run("state_"+tc.input, func(tt *testing.T) {
			assert.Equal(tt, tc.expected, normalizeBatchBVState(tc.input))
		})
	}
}

// ============================================================
// buildBVFieldSet Tests
// ============================================================

func TestBuildBVFieldSet(t *testing.T) {
	t.Run("Empty_ReturnsNil", func(tt *testing.T) {
		assert.Nil(tt, buildBVFieldSet(nil))
		assert.Nil(tt, buildBVFieldSet([]gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{}))
	})

	t.Run("NonEmpty_ReturnsMap", func(tt *testing.T) {
		fields := []gcpgenserver.V1betaBatchListBackupVaultsFieldsItem{"resourceId", "state"}
		result := buildBVFieldSet(fields)
		require.NotNil(tt, result)
		assert.True(tt, result["resourceId"])
		assert.True(tt, result["state"])
		assert.False(tt, result["description"])
	})
}

// ============================================================
// convertBackupVaultToBatchBackupVault: nil fieldSet (early return)
// ============================================================

func TestConvertBackupVault_NilFieldSet_ReturnsOnlyBackupVaultId(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "my-vault", "READY")
	bp := convertBackupVaultToBatchBackupVault(bv, nil)

	assert.True(t, bp.BackupVaultId.Set)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", bp.BackupVaultId.Value)
	assert.False(t, bp.ResourceId.Set)
	assert.False(t, bp.Description.Set)
	assert.False(t, bp.State.Set)
	assert.False(t, bp.EncryptionState.Set)
	assert.False(t, bp.BackupRetentionPolicy.Set)
}

// ============================================================
// convertBackupVaultToBatchBackupVault: nil BackupVaultType
// ============================================================

func TestConvertBackupVault_NilBackupVaultType(t *testing.T) {
	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	bv.BackupVaultType = nil
	fieldSet := map[string]bool{
		"backupVaultType":   true,
		"sourceRegion":      true,
		"crossProjectVault": true,
		"encryptionState":   true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.True(t, bp.BackupVaultType.Set, "nil type should be set via ensureRequestedBVFieldsPresent")
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaBackupVaultTypeTYPEUNSPECIFIED, bp.BackupVaultType.Value)
	assert.True(t, bp.SourceRegion.Set && bp.SourceRegion.Null)
	assert.True(t, bp.EncryptionState.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionStateENCRYPTIONSTATEUNSPECIFIED, bp.EncryptionState.Value)
}

// ============================================================
// CROSS_REGION: missing crossRegionBackupVaultName (nil)
// ============================================================

func TestConvertBackupVault_CrossRegion_NilCrossRegionName(t *testing.T) {
	vaultType := "CROSS_REGION"
	src := "us-central1"
	br := "us-east4"
	bv := &models.BackupVaultV1beta{
		BackupVaultID:   "cr-nil",
		Name:            "vault",
		LifeCycleState:  "READY",
		BackupVaultType: &vaultType,
		SourceRegion:    &src,
		BackupRegion:    &br,
		AccountName:     "my-project",
		CreatedAt:       time.Now(),
	}
	fieldSet := map[string]bool{
		"sourceBackupVault":      true,
		"destinationBackupVault": true,
		"sourceRegion":           true,
		"backupRegion":           true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.Equal(t, "us-central1", bp.SourceRegion.Value)
	assert.Equal(t, "us-east4", bp.BackupRegion.Value)
	assert.True(t, bp.SourceBackupVault.Set && bp.SourceBackupVault.Null, "should be null when crossRegionBackupVaultName is nil")
	assert.True(t, bp.DestinationBackupVault.Set && bp.DestinationBackupVault.Null)
}

// ============================================================
// CVP Conversion: all fields populated
// ============================================================

func TestConvertCVPBackupVault_AllFields(t *testing.T) {
	desc := "cvp-desc"
	state := "READY"
	stateDetails := "ok"
	vaultType := "IN_REGION"
	srcRegion := "us-central1"
	bkRegion := "us-east4"
	srcBV := "projects/p/locations/l/backupVaults/src"
	dstBV := "projects/p/locations/l2/backupVaults/dst"
	kms := "kms-path"
	keyVer := "key-ver"
	enc := "ENCRYPTION_STATE_COMPLETED"
	crossProject := true
	now := time.Now()
	cvpCreatedAt := strfmt.DateTime(now)

	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID:            "cvp-full",
		ResourceID:               &desc,
		Description:              &desc,
		CreatedAt:                &cvpCreatedAt,
		State:                    &state,
		StateDetails:             &stateDetails,
		BackupVaultType:          &vaultType,
		SourceRegion:             &srcRegion,
		BackupRegion:             &bkRegion,
		SourceBackupVault:        &srcBV,
		DestinationBackupVault:   &dstBV,
		KmsConfigResourcePath:    &kms,
		BackupsPrimaryKeyVersion: &keyVer,
		EncryptionState:          &enc,
		CrossProjectVault:        &crossProject,
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)

	assert.Equal(t, "cvp-full", bp.BackupVaultId.Value)
	assert.Equal(t, desc, bp.ResourceId.Value)
	assert.Equal(t, desc, bp.Description.Value)
	assert.True(t, bp.CreatedAt.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaState("READY"), bp.State.Value)
	assert.Equal(t, "ok", bp.StateDetails.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaBackupVaultType("IN_REGION"), bp.BackupVaultType.Value)
	assert.Equal(t, "us-central1", bp.SourceRegion.Value)
	assert.Equal(t, "us-east4", bp.BackupRegion.Value)
	assert.Equal(t, srcBV, bp.SourceBackupVault.Value)
	assert.Equal(t, dstBV, bp.DestinationBackupVault.Value)
	assert.Equal(t, "kms-path", bp.KmsConfigResourcePath.Value)
	assert.Equal(t, "key-ver", bp.BackupsPrimaryKeyVersion.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionState("ENCRYPTION_STATE_COMPLETED"), bp.EncryptionState.Value)
	assert.True(t, bp.CrossProjectVault.Value)
}

// ============================================================
// CVP Conversion: nil retention policy -> not set
// ============================================================

func TestConvertCVPBackupVault_NilRetentionPolicy(t *testing.T) {
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID: "cvp-no-rp",
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	assert.False(t, bp.BackupRetentionPolicy.Set, "nil retention policy should not be set")
}

// ============================================================
// CVP Conversion: retention policy with nil days defaults to 0
// ============================================================

func TestConvertCVPBackupVault_RetentionPolicy_NilDays(t *testing.T) {
	p := &cvpmodels.BatchBackupVaultV1beta{
		BackupVaultID: "cvp-rp",
		BackupRetentionPolicy: &cvpmodels.BackupRetentionPolicyV1beta{
			DailyBackupImmutable: true,
		},
	}
	bp := convertCVPBatchBackupVaultToGCPBatchBackupVault(p)
	require.True(t, bp.BackupRetentionPolicy.Set)
	assert.Equal(t, 0, bp.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.Value)
	assert.True(t, bp.BackupRetentionPolicy.Value.DailyBackupImmutable.Set)
}

// ============================================================
// VCP conversion: all fields for full conversion coverage
// ============================================================

func TestConvertBackupVault_AllFieldsPopulated(t *testing.T) {
	bv := makeCrossRegionVCPBackupVault(
		"77777777-7777-7777-7777-777777777777", "my-vault", "my-project",
		"us-central1", "us-east4",
		"projects/my-project/locations/us-east4/backupVaults/peer-vault",
	)
	bv.LifeCycleStateDetails = "ready"
	desc := "full vault"
	bv.Description = &desc
	kms := "projects/p/locations/l/kmsConfigs/k"
	bv.KmsConfigResourcePath = &kms
	keyVer := "key-v1"
	bv.BackupsPrimaryKeyVersion = &keyVer
	enc := "ENCRYPTION_STATE_COMPLETED"
	bv.EncryptionState = &enc
	bv.ServiceType = datamodel.ServiceTypeCrossProject
	bv.BackupRetentionPolicy = models.BackupRetentionPolicyparams{
		IsDailyBackupImmutable:                 true,
		IsWeeklyBackupImmutable:                true,
		IsMonthlyBackupImmutable:               true,
		IsAdhocBackupImmutable:                 true,
		BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
	}

	fieldSet := map[string]bool{
		"resourceId": true, "description": true, "createdAt": true,
		"state": true, "stateDetails": true, "backupVaultType": true,
		"sourceRegion": true, "backupRegion": true,
		"sourceBackupVault": true, "destinationBackupVault": true,
		"kmsConfigResourcePath": true, "backupsPrimaryKeyVersion": true,
		"encryptionState": true, "backupRetentionPolicy": true,
		"crossProjectVault": true,
	}
	bp := convertBackupVaultToBatchBackupVault(bv, fieldSet)

	assert.Equal(t, "77777777-7777-7777-7777-777777777777", bp.BackupVaultId.Value)
	assert.Equal(t, "my-vault", bp.ResourceId.Value)
	assert.Equal(t, "full vault", bp.Description.Value)
	assert.True(t, bp.CreatedAt.Set)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaStateREADY, bp.State.Value)
	assert.Equal(t, "ready", bp.StateDetails.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaBackupVaultType("CROSS_REGION"), bp.BackupVaultType.Value)
	assert.Equal(t, "us-central1", bp.SourceRegion.Value)
	assert.Equal(t, "us-east4", bp.BackupRegion.Value)
	assert.True(t, bp.SourceBackupVault.Set && !bp.SourceBackupVault.Null)
	assert.True(t, bp.DestinationBackupVault.Set && !bp.DestinationBackupVault.Null)
	assert.Equal(t, "projects/p/locations/l/kmsConfigs/k", bp.KmsConfigResourcePath.Value)
	assert.Equal(t, "key-v1", bp.BackupsPrimaryKeyVersion.Value)
	assert.Equal(t, gcpgenserver.BatchBackupVaultV1betaEncryptionState("ENCRYPTION_STATE_COMPLETED"), bp.EncryptionState.Value)
	assert.True(t, bp.CrossProjectVault.Value)
	require.True(t, bp.BackupRetentionPolicy.Set)
	rp := bp.BackupRetentionPolicy.Value
	assert.True(t, rp.DailyBackupImmutable.Value)
	assert.True(t, rp.WeeklyBackupImmutable.Value)
	assert.True(t, rp.MonthlyBackupImmutable.Value)
	assert.True(t, rp.ManualBackupImmutable.Value)
	assert.Equal(t, 30, rp.BackupMinimumEnforcedRetentionDays.Value)
}

// ============================================================
// Nil vault in VCP results is skipped (VCP-only path)
// ============================================================

func TestV1betaBatchListBackupVaults_NilVaultInResults(t *testing.T) {
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()
	cvp.CVP_HOST = ""

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}
	ctx := authContext()

	bv := makeVCPBackupVault("11111111-1111-1111-1111-111111111111", "vault", "READY")
	mockOrch.On("GetMultipleBackupVaults", mock.Anything, []string{"11111111-1111-1111-1111-111111111111", "66666666-6666-6666-6666-666666666666"}).
		Return([]*models.BackupVaultV1beta{bv, nil}, nil)

	req := &gcpgenserver.BatchBackupVaultUUIDListV1beta{BackupVaultUuids: []string{"11111111-1111-1111-1111-111111111111", "66666666-6666-6666-6666-666666666666"}}
	params := gcpgenserver.V1betaBatchListBackupVaultsParams{LocationId: "us-east4"}

	res, err := handler.V1betaBatchListBackupVaults(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupVaultsOK)
	require.True(t, ok)
	require.Len(t, okRes.BackupVaults, 1, "nil vaults should be filtered")
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", okRes.BackupVaults[0].BackupVaultId.Value)
}

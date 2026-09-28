package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	commonutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func withBackupEnabled(t *testing.T) {
	t.Helper()
	orig := backupEnabled
	backupEnabled = true
	t.Cleanup(func() { backupEnabled = orig })
}

func stubFetchBatchBackupsFromCVP(rows []gcpgenserver.BatchBackupV1beta, err error) func() {
	orig := fetchBatchBackupsFromCVPFn
	fetchBatchBackupsFromCVPFn = func(_ context.Context, _ []string, _ gcpgenserver.V1betaBatchListBackupsParams, fieldSet map[string]bool) ([]gcpgenserver.BatchBackupV1beta, error) {
		out := make([]gcpgenserver.BatchBackupV1beta, 0, len(rows))
		for _, r := range rows {
			rc := r
			applyBatchBackupFieldQuery(&rc, fieldSet)
			out = append(out, rc)
		}
		return out, err
	}
	return func() { fetchBatchBackupsFromCVPFn = orig }
}

func makeVCPBackup(uuid, name, state string) *datamodel.Backup {
	srcRegion := "us-east4"
	return &datamodel.Backup{
		BaseModel: datamodel.BaseModel{
			UUID:      uuid,
			CreatedAt: time.Now(),
		},
		Name:                    name,
		Description:             "desc-" + uuid,
		State:                   state,
		Type:                    "MANUAL",
		VolumeUUID:              "vol-" + uuid,
		SizeInBytes:             1024,
		LatestLogicalBackupSize: 2048,
		Attributes: &datamodel.BackupAttributes{
			SnapshotID: "snap-" + uuid,
			BucketName: "bucket-" + uuid,
		},
		BackupVault: &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-" + uuid},
			SourceRegionName: &srcRegion,
			BucketDetails: datamodel.BucketDetailsArray{
				{BucketName: "bucket-" + uuid, SatisfiesPzi: true, SatisfiesPzs: true},
			},
		},
	}
}

func makeCVPBatchBackupModel(uuid, resourceID string) gcpgenserver.BatchBackupV1beta {
	return gcpgenserver.BatchBackupV1beta{
		BackupId:   gcpgenserver.NewOptString(uuid),
		ResourceId: gcpgenserver.NewOptNilString(resourceID),
	}
}

// ============================================================
// Auth
// ============================================================

func TestV1betaBatchListBackups_Auth(t *testing.T) {
	t.Run("InvalidJWT_ReturnsUnauthorized", func(tt *testing.T) {
		withBackupEnabled(tt)
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

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"deadbeef-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsUnauthorized)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"deadbeef-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(context.Background(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupsUnauthorized)
		require.True(tt, ok)
	})
}

// ============================================================
// Validation
// ============================================================

func TestV1betaBatchListBackups_Validation(t *testing.T) {
	t.Run("BackupFeatureDisabled_ReturnsBadRequest", func(tt *testing.T) {
		orig := backupEnabled
		backupEnabled = false
		defer func() { backupEnabled = orig }()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"deadbeef-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		bad, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, bad.Message, "not enabled")
	})

	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"deadbeef-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptyBackupUuids_ReturnsBadRequest", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "backupUuids is required")
	})

	t.Run("NilBody_ReturnsBadRequest", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}
		res, err := handler.V1betaBatchListBackups(authContext(), nil, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, env.MaxBatchBackupUUIDs+1)
		for i := range uuids {
			// Every entry needs to be unique so dedup does not reduce the list under the cap.
			uuids[i] = "uuid-" + time.Now().Format("150405.000000000") + "-" + toString(i)
		}
		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: uuids}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most")
	})

	t.Run("MalformedUUID_ReturnsBadRequestBeforeFetching", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		// The orchestrator must never be called when the request fails UUID
		// format validation; an unset mock would error out if it were.
		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{
			"10000000-0000-0000-0000-000000000001",
			"not-a-uuid",
		}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusBadRequest), badReq.Code)
		// The message must call out the offending index in the original request
		// (1 here) and embed the UUID regex so clients know what to fix.
		assert.Contains(tt, badReq.Message, "backupUuids.1 in body should match")
		assert.Contains(tt, badReq.Message, "[a-fA-F0-9]{8}")
	})

	t.Run("EmptyStringUUID_ReturnsBadRequest", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{""}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListBackupsBadRequest)
		require.True(tt, ok)
		assert.Contains(tt, badReq.Message, "backupUuids.0 in body should match")
	})
}

func toString(i int) string {
	// small helper so we don't pull strconv into test-only lib just for this
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// ============================================================
// VCP-only path
// ============================================================

func TestV1betaBatchListBackups_VCPOnly(t *testing.T) {
	t.Run("Success_WithFields_PopulatesRequested", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "resource-1", datamodel.LifeCycleStateAvailable)
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001"}).
			Return([]*datamodel.Backup{b}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListBackupsFieldsItem{
				"resourceId", "state", "volumeId", "volumeUsageBytes", "backupVaultId",
			},
		}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 1)
		got := okRes.Backups[0]
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", got.BackupId.Value)
		assert.Equal(tt, "resource-1", got.ResourceId.Value)
		assert.Equal(tt, "vol-10000000-0000-0000-0000-000000000001", got.VolumeId.Value)
		assert.Equal(tt, int64(1024), got.VolumeUsageBytes.Value)
		assert.Equal(tt, "bv-10000000-0000-0000-0000-000000000001", got.BackupVaultId.Value)
		assert.Equal(tt, gcpgenserver.BatchBackupV1betaStateREADY, got.State.Value)
		assert.False(tt, got.Description.Set, "description not requested, must be stripped")
	})

	t.Run("NoFieldsRequested_ReturnsOnlyBackupId", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "resource-1", datamodel.LifeCycleStateAvailable)
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001"}).
			Return([]*datamodel.Backup{b}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 1)
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", okRes.Backups[0].BackupId.Value)
		assert.False(tt, okRes.Backups[0].ResourceId.Set)
		assert.False(tt, okRes.Backups[0].State.Set)
	})

	t.Run("VCPFails_Returns500", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"deadbeef-1111-1111-1111-111111111111"}).
			Return(nil, errors.New("database error"))

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"deadbeef-1111-1111-1111-111111111111"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("PreservesRequestOrder_WhenDatastoreReturnsDifferentOrder", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		// Datastore returns rows in a different order than the request. The response must still
		// match request order so positional consumers see stable indexing across all modes.
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002", "30000000-0000-0000-0000-000000000003"}).
			Return([]*datamodel.Backup{
				makeVCPBackup("30000000-0000-0000-0000-000000000003", "r3", datamodel.LifeCycleStateAvailable),
				makeVCPBackup("10000000-0000-0000-0000-000000000001", "r1", datamodel.LifeCycleStateAvailable),
				makeVCPBackup("20000000-0000-0000-0000-000000000002", "r2", datamodel.LifeCycleStateAvailable),
			}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002", "30000000-0000-0000-0000-000000000003"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 3)
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", okRes.Backups[0].BackupId.Value)
		assert.Equal(tt, "20000000-0000-0000-0000-000000000002", okRes.Backups[1].BackupId.Value)
		assert.Equal(tt, "30000000-0000-0000-0000-000000000003", okRes.Backups[2].BackupId.Value)
	})

	t.Run("DuplicateUUIDs_AreDeduplicatedBeforeFetch", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		// Expect the deduplicated list only.
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"}).
			Return([]*datamodel.Backup{
				makeVCPBackup("10000000-0000-0000-0000-000000000001", "r1", datamodel.LifeCycleStateAvailable),
				makeVCPBackup("20000000-0000-0000-0000-000000000002", "r2", datamodel.LifeCycleStateAvailable),
			}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001", "10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		assert.Len(tt, okRes.Backups, 2)
	})
}

// ============================================================
// VCP + CVP in parallel
// ============================================================

func TestV1betaBatchListBackups_Parallel(t *testing.T) {
	t.Run("BothSucceed_VCPAuthoritativeOnDuplicates", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		// CVP returns rows for both UUIDs; VCP also returns the first one.
		// Post-merge we expect the VCP-owned row for bk-1 and the CVP row for cvp-only.
		restoreCVP := stubFetchBatchBackupsFromCVP([]gcpgenserver.BatchBackupV1beta{
			makeCVPBatchBackupModel("10000000-0000-0000-0000-000000000001", "cvp-version-should-be-overridden"),
			makeCVPBatchBackupModel("c0000000-0000-0000-0000-cccccccccccc", "cvp-only-res"),
		}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001", "c0000000-0000-0000-0000-cccccccccccc"}).
			Return([]*datamodel.Backup{
				makeVCPBackup("10000000-0000-0000-0000-000000000001", "vcp-res", datamodel.LifeCycleStateAvailable),
			}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001", "c0000000-0000-0000-0000-cccccccccccc"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListBackupsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 2)

		// Ordered by the request UUID order.
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", okRes.Backups[0].BackupId.Value)
		assert.Equal(tt, "vcp-res", okRes.Backups[0].ResourceId.Value, "VCP data must win on merge")
		assert.Equal(tt, "c0000000-0000-0000-0000-cccccccccccc", okRes.Backups[1].BackupId.Value)
		assert.Equal(tt, "cvp-only-res", okRes.Backups[1].ResourceId.Value)
	})

	t.Run("VCPFails_CVPSucceeds_ReturnsCVPOnly", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchBackupsFromCVP([]gcpgenserver.BatchBackupV1beta{
			makeCVPBatchBackupModel("10000000-0000-0000-0000-000000000001", "cvp-res"),
		}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001"}).
			Return(nil, errors.New("VCP database error"))

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListBackupsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 1)
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", okRes.Backups[0].BackupId.Value)
		assert.Equal(tt, "cvp-res", okRes.Backups[0].ResourceId.Value)
	})

	t.Run("VCPSucceeds_CVPFails_ReturnsVCPOnly", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchBackupsFromCVP(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001"}).
			Return([]*datamodel.Backup{
				makeVCPBackup("10000000-0000-0000-0000-000000000001", "vcp-res", datamodel.LifeCycleStateAvailable),
			}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListBackupsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Backups, 1)
		assert.Equal(tt, "vcp-res", okRes.Backups[0].ResourceId.Value)
	})

	t.Run("BothFail_Returns500", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchBackupsFromCVP(nil, errors.New("CVP down"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001"}).
			Return(nil, errors.New("VCP down"))

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListBackupsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("PartialFailure_PreservesRequestOrderAndNonNilBackups", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		// CVP fails so we fall back to the VCP branch. Return VCP rows in a different order
		// than the request so we can prove the fallback still honors request-UUID order.
		restoreCVP := stubFetchBatchBackupsFromCVP(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002", "30000000-0000-0000-0000-000000000003"}).
			Return([]*datamodel.Backup{
				makeVCPBackup("30000000-0000-0000-0000-000000000003", "r3", datamodel.LifeCycleStateAvailable),
				makeVCPBackup("10000000-0000-0000-0000-000000000001", "r1", datamodel.LifeCycleStateAvailable),
				makeVCPBackup("20000000-0000-0000-0000-000000000002", "r2", datamodel.LifeCycleStateAvailable),
			}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002", "30000000-0000-0000-0000-000000000003"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		require.NotNil(tt, okRes.Backups, "Backups slice must be non-nil so the encoder emits \"backups\": [...]")
		require.Len(tt, okRes.Backups, 3)
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", okRes.Backups[0].BackupId.Value)
		assert.Equal(tt, "20000000-0000-0000-0000-000000000002", okRes.Backups[1].BackupId.Value)
		assert.Equal(tt, "30000000-0000-0000-0000-000000000003", okRes.Backups[2].BackupId.Value)
	})

	t.Run("BothReturnEmpty_ReturnsEmptyArray", func(tt *testing.T) {
		withBackupEnabled(tt)
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchBackupsFromCVP([]gcpgenserver.BatchBackupV1beta{}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		mockOrch.On("GetBackupsByUUIDs", mock.Anything, []string{"f0000000-0000-0000-0000-ffffffffffff"}).
			Return([]*datamodel.Backup{}, nil)

		req := &gcpgenserver.BatchBackupUUIDListV1beta{BackupUuids: []string{"f0000000-0000-0000-0000-ffffffffffff"}}
		params := gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListBackups(authContext(), req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListBackupsOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.Backups)
	})
}

// ============================================================
// Helpers
// ============================================================

func TestBatchBackupFieldsAsSet(t *testing.T) {
	t.Run("NilFields_ReturnsNil", func(tt *testing.T) {
		assert.Nil(tt, batchBackupFieldsAsSet(nil))
	})
	t.Run("EmptyFields_ReturnsNil", func(tt *testing.T) {
		assert.Nil(tt, batchBackupFieldsAsSet([]gcpgenserver.V1betaBatchListBackupsFieldsItem{}))
	})
	t.Run("PopulatesSet", func(tt *testing.T) {
		fs := batchBackupFieldsAsSet([]gcpgenserver.V1betaBatchListBackupsFieldsItem{"resourceId", "created"})
		assert.True(tt, fs["resourceId"])
		assert.True(tt, fs["created"])
		assert.False(tt, fs["volumeId"])
	})
}

func TestMapBackupStateToBatchState(t *testing.T) {
	// Table covers every BatchBackupV1betaState enum value plus the two custom inputs ("" and
	// LifeCycleStateUpdating) that must collapse to STATE_UNSPECIFIED, and an unknown value.
	cases := []struct {
		in   string
		want gcpgenserver.BatchBackupV1betaState
	}{
		{"", gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED},
		{datamodel.LifeCycleStateAvailable, gcpgenserver.BatchBackupV1betaStateREADY},
		{datamodel.LifeCycleStateUpdating, gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED},
		{string(gcpgenserver.BatchBackupV1betaStateCREATING), gcpgenserver.BatchBackupV1betaStateCREATING},
		{string(gcpgenserver.BatchBackupV1betaStateREADY), gcpgenserver.BatchBackupV1betaStateREADY},
		{string(gcpgenserver.BatchBackupV1betaStateUPLOADING), gcpgenserver.BatchBackupV1betaStateUPLOADING},
		{string(gcpgenserver.BatchBackupV1betaStateRESTORING), gcpgenserver.BatchBackupV1betaStateRESTORING},
		{string(gcpgenserver.BatchBackupV1betaStateDISABLED), gcpgenserver.BatchBackupV1betaStateDISABLED},
		{string(gcpgenserver.BatchBackupV1betaStateDELETING), gcpgenserver.BatchBackupV1betaStateDELETING},
		{string(gcpgenserver.BatchBackupV1betaStateDELETED), gcpgenserver.BatchBackupV1betaStateDELETED},
		{string(gcpgenserver.BatchBackupV1betaStateERROR), gcpgenserver.BatchBackupV1betaStateERROR},
		{string(gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED), gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED},
		{"some-garbage", gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, mapBackupStateToBatchState(tc.in), "state=%q", tc.in)
	}
}

func TestConvertBackupToBatchBackup_FieldMaskRemovesUnrequested(t *testing.T) {
	b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "resource-1", datamodel.LifeCycleStateAvailable)
	fieldSet := map[string]bool{"resourceId": true, "volumeId": true}

	out := convertBackupToBatchBackup(b, fieldSet)

	assert.Equal(t, "10000000-0000-0000-0000-000000000001", out.BackupId.Value)
	assert.Equal(t, "resource-1", out.ResourceId.Value)
	assert.Equal(t, "vol-10000000-0000-0000-0000-000000000001", out.VolumeId.Value)
	assert.False(t, out.Description.Set)
	assert.False(t, out.VolumeUsageBytes.Set)
	assert.False(t, out.BackupVaultId.Set)
}

func TestApplyBatchBackupFieldQuery_MissingRequestedFieldsEmittedAsNull(t *testing.T) {
	bp := gcpgenserver.BatchBackupV1beta{BackupId: gcpgenserver.NewOptString("10000000-0000-0000-0000-000000000001")}
	applyBatchBackupFieldQuery(&bp, map[string]bool{
		"resourceId":               true,
		"description":              true,
		"volumeUsageBytes":         true,
		"satisfiesPzi":             true,
		"state":                    true,
		"created":                  true,
		"enforcedRetentionEndTime": true,
	})

	raw, err := json.Marshal(&bp)
	require.NoError(t, err)
	js := string(raw)
	// Each requested-but-missing field must be present as JSON null, not as an empty-string / zero
	// placeholder, so clients can distinguish "no value" from "value is empty".
	assert.Contains(t, js, `"resourceId":null`)
	assert.Contains(t, js, `"description":null`)
	assert.Contains(t, js, `"volumeUsageBytes":null`)
	assert.Contains(t, js, `"satisfiesPzi":null`)
	assert.Contains(t, js, `"state":null`)
	assert.Contains(t, js, `"created":null`)
	assert.Contains(t, js, `"enforcedRetentionEndTime":null`)
	// backupId is always preserved and carries the real value.
	assert.Contains(t, js, `"backupId":"10000000-0000-0000-0000-000000000001"`)
	// Non-requested fields must be omitted entirely.
	assert.NotContains(t, js, `"volumeId"`)
	assert.NotContains(t, js, `"backupType"`)
}

func TestIndexBatchBackupsByUUID_SkipsUnsetIDs(t *testing.T) {
	rows := []gcpgenserver.BatchBackupV1beta{
		{BackupId: gcpgenserver.NewOptString("a")},
		{}, // no ID, must be skipped
		{BackupId: gcpgenserver.NewOptString("b")},
	}
	m := indexBatchBackupsByUUID(rows)
	assert.Len(t, m, 2)
	assert.Contains(t, m, "a")
	assert.Contains(t, m, "b")
}

func TestConvertBackupToBatchBackup_AssetLocationMetadata(t *testing.T) {
	t.Run("WithChildAssets_Emitted", func(tt *testing.T) {
		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "res-1", datamodel.LifeCycleStateAvailable)
		b.AssetMetadata = &datamodel.AssetMetadata{
			ChildAssets: []datamodel.ChildAsset{
				{AssetType: "TABLE", AssetNames: []string{"orders", "users"}},
			},
		}

		out := convertBackupToBatchBackup(b, map[string]bool{"assetLocationMetadata": true})
		require.True(tt, out.AssetLocationMetadata.Set)
		require.Len(tt, out.AssetLocationMetadata.Value.ChildAssets, 1)
		assert.Equal(tt, "TABLE", out.AssetLocationMetadata.Value.ChildAssets[0].AssetType.Value)
		assert.Equal(tt, []string{"orders", "users"}, out.AssetLocationMetadata.Value.ChildAssets[0].AssetNames)
	})

	t.Run("AssetMetadataNil_FieldRequested_EmitsEmptyObject", func(tt *testing.T) {
		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "res-1", datamodel.LifeCycleStateAvailable)
		b.AssetMetadata = nil

		out := convertBackupToBatchBackup(b, map[string]bool{"assetLocationMetadata": true})
		require.True(tt, out.AssetLocationMetadata.Set,
			"requested assetLocationMetadata must be present in response even when source has none")
		assert.Empty(tt, out.AssetLocationMetadata.Value.ChildAssets)
	})

	t.Run("AssetMetadataPresentWithEmptyChildren_StillEmitted", func(tt *testing.T) {
		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "res-1", datamodel.LifeCycleStateAvailable)
		b.AssetMetadata = &datamodel.AssetMetadata{ChildAssets: nil}

		out := convertBackupToBatchBackup(b, map[string]bool{"assetLocationMetadata": true})
		assert.True(tt, out.AssetLocationMetadata.Set)
	})

	t.Run("FieldNotRequested_Omitted", func(tt *testing.T) {
		b := makeVCPBackup("10000000-0000-0000-0000-000000000001", "res-1", datamodel.LifeCycleStateAvailable)
		b.AssetMetadata = &datamodel.AssetMetadata{
			ChildAssets: []datamodel.ChildAsset{{AssetType: "TABLE"}},
		}

		out := convertBackupToBatchBackup(b, map[string]bool{"resourceId": true})
		assert.False(tt, out.AssetLocationMetadata.Set, "field not requested must be stripped")
	})
}

func TestConvertCVPBatchBackupToGCPBatchBackup_CopiesAssetLocationMetadata(t *testing.T) {
	assetType := "storage.googleapis.com/Bucket"
	cvpRow := &cvpmodels.BatchBackupV1beta{
		BackupID: "10000000-0000-0000-0000-000000000001",
		AssetLocationMetadata: &cvpmodels.AssetLocationMetadataV2{
			ChildAssets: []*cvpmodels.ChildAssetV2{
				{
					AssetType:  assetType,
					AssetNames: []string{"//storage.googleapis.com/sde-backup-m1c31zxn"},
				},
				nil, // must be skipped without panicking
			},
		},
	}

	out := convertCVPBatchBackupToGCPBatchBackup(cvpRow)
	require.True(t, out.AssetLocationMetadata.Set, "CVP AssetLocationMetadata must be forwarded")
	require.Len(t, out.AssetLocationMetadata.Value.ChildAssets, 1)
	ca := out.AssetLocationMetadata.Value.ChildAssets[0]
	assert.Equal(t, assetType, ca.AssetType.Value)
	assert.Equal(t, []string{"//storage.googleapis.com/sde-backup-m1c31zxn"}, ca.AssetNames)
}

func TestMergeBatchBackupParallelLists_PreservesOrderAndPrefersVCP(t *testing.T) {
	vcp := []gcpgenserver.BatchBackupV1beta{
		{BackupId: gcpgenserver.NewOptString("a"), ResourceId: gcpgenserver.NewOptNilString("vcp-a")},
	}
	cvp := []gcpgenserver.BatchBackupV1beta{
		{BackupId: gcpgenserver.NewOptString("a"), ResourceId: gcpgenserver.NewOptNilString("cvp-a-ignored")},
		{BackupId: gcpgenserver.NewOptString("b"), ResourceId: gcpgenserver.NewOptNilString("cvp-b")},
	}
	out := mergeBatchBackupParallelLists([]string{"a", "b", "missing"}, vcp, cvp)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].BackupId.Value)
	assert.Equal(t, "vcp-a", out[0].ResourceId.Value)
	assert.Equal(t, "b", out[1].BackupId.Value)
	assert.Equal(t, "cvp-b", out[1].ResourceId.Value)
}

// makeRichVCPBackup returns a backup with every optional attribute populated so that each conditional
// branch in convertBackupToBatchBackup (gate checks, fallbacks, bucket matching, immutability) is hit.
func makeRichVCPBackup() *datamodel.Backup {
	srcRegion := "us-east4"
	backupRegion := "us-east1"
	retention := int64(7)
	return &datamodel.Backup{
		BaseModel: datamodel.BaseModel{
			UUID:      "bk-rich",
			CreatedAt: time.Now(),
		},
		Name:                    "rich-name",
		Description:             "rich-desc",
		State:                   datamodel.LifeCycleStateAvailable,
		Type:                    commonutils.BackupTypeMANUAL,
		VolumeUUID:              "vol-rich",
		SizeInBytes:             1024,
		LatestLogicalBackupSize: 2048,
		Attributes: &datamodel.BackupAttributes{
			SnapshotID:          "snap-id-rich",
			SnapshotName:        "snap-name-rich",
			UseExistingSnapshot: true,
			BucketName:          "bucket-rich",
			VolumeName:          "vol-rich",
			AccountIdentifier:   "acct-rich",
			SourceVolumeZone:    "us-east4-a",
		},
		BackupVault: &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-rich"},
			SourceRegionName: &srcRegion,
			BackupRegionName: &backupRegion,
			BucketDetails: datamodel.BucketDetailsArray{
				{BucketName: "bucket-rich", SatisfiesPzi: true, SatisfiesPzs: true},
			},
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &retention,
				IsAdhocBackupImmutable:                 true,
			},
		},
	}
}

// TestConvertBackupToBatchBackup_BranchCoverage exercises all per-field conditional branches in
// convertBackupToBatchBackup that the existing "handler happy-path" tests don't cover: snapshot
// gate, backup-region fallback, bucket-match satisfiesPz*, immutable retention end time, and the
// optional-field-unset paths.
func TestConvertBackupToBatchBackup_BranchCoverage(t *testing.T) {
	// requestAll is the superset of every known field so the mask doesn't strip anything we're
	// trying to assert on.
	requestAll := map[string]bool{
		"created": true, "volumeUsageBytes": true, "backupType": true, "sourceVolume": true,
		"backupVaultId": true, "sourceSnapshot": true, "description": true, "state": true,
		"resourceId": true, "snapshotId": true, "volumeId": true, "backupChainBytes": true,
		"satisfiesPzs": true, "satisfiesPzi": true, "volumeRegion": true, "backupRegion": true,
		"enforcedRetentionEndTime": true, "assetLocationMetadata": true,
	}

	t.Run("FullyPopulated_AllBranchesHit", func(tt *testing.T) {
		b := makeRichVCPBackup()
		out := convertBackupToBatchBackup(b, requestAll)

		assert.Equal(tt, "bk-rich", out.BackupId.Value)
		assert.Equal(tt, "rich-name", out.ResourceId.Value)
		assert.Equal(tt, "vol-rich", out.VolumeId.Value)
		assert.Equal(tt, "bv-rich", out.BackupVaultId.Value)
		assert.Equal(tt, "rich-desc", out.Description.Value)
		assert.Equal(tt, int64(1024), out.VolumeUsageBytes.Value)
		assert.Equal(tt, int64(2048), out.BackupChainBytes.Value, "LatestLogicalBackupSize!=0 must populate backupChainBytes")
		assert.Equal(tt, "snap-id-rich", out.SnapshotId.Value, "non-empty SnapshotID must populate snapshotId")
		assert.Equal(tt, gcpgenserver.BatchBackupV1betaStateREADY, out.State.Value)
		assert.Equal(tt, gcpgenserver.BatchBackupV1betaBackupType(commonutils.BackupTypeMANUAL), out.BackupType.Value)

		// sourceVolume path format — includes AccountIdentifier, zone, volume name.
		assert.Equal(tt, "projects/acct-rich/locations/us-east4-a/volumes/vol-rich", out.SourceVolume.Value)
		// sourceSnapshot gate passes (UseExistingSnapshot && SnapshotName != ""); path includes the
		// renamed snapshot name.
		assert.True(tt, out.SourceSnapshot.Set, "sourceSnapshot must be emitted when gate passes")
		assert.Equal(tt, "projects/acct-rich/locations/us-east4-a/volumes/vol-rich/snapshots/snap-name-rich", out.SourceSnapshot.Value)

		// Regions: dedicated BackupRegionName takes precedence, volumeRegion is SourceRegionName.
		assert.Equal(tt, "us-east4", out.VolumeRegion.Value)
		assert.Equal(tt, "us-east1", out.BackupRegion.Value)

		// Bucket details match → both flags are taken from the matched row.
		assert.True(tt, out.SatisfiesPzi.Value)
		assert.True(tt, out.SatisfiesPzs.Value)

		// Immutable-retention path: MANUAL + IsAdhocBackupImmutable + duration>0 → EnforcedRetentionEndTime
		// set to CreatedAt + duration days.
		require.True(tt, out.EnforcedRetentionEndTime.Set, "manual+adhoc-immutable must emit enforcedRetentionEndTime")
		assert.Equal(tt, b.CreatedAt.AddDate(0, 0, 7), out.EnforcedRetentionEndTime.Value)
	})

	t.Run("BackupRegionFallsBackToSourceRegion", func(tt *testing.T) {
		b := makeRichVCPBackup()
		b.BackupVault.BackupRegionName = nil // force the fallback branch

		out := convertBackupToBatchBackup(b, requestAll)

		assert.Equal(tt, "us-east4", out.BackupRegion.Value, "no dedicated backup region → fall back to source region")
		assert.Equal(tt, "us-east4", out.VolumeRegion.Value)
	})

	t.Run("MinimalBackup_OptionalsUnset", func(tt *testing.T) {
		b := makeRichVCPBackup()
		b.Attributes.UseExistingSnapshot = false // gate fails
		b.Attributes.BucketName = "other-bucket" // bucket-details mismatch
		b.LatestLogicalBackupSize = 0            // chainBytes branch off
		b.Attributes.SnapshotID = ""             // snapshotId branch off
		b.BackupVault.ImmutableAttributes = nil  // retention branch off

		out := convertBackupToBatchBackup(b, requestAll)

		// The converter leaves optional fields unset when their gate fails. The field mask then
		// promotes unset-but-requested fields to explicit JSON null (Set=true && Null=true), so we
		// assert on Null rather than Set.
		assert.True(tt, out.SourceSnapshot.Null, "gate failed → converter must not set sourceSnapshot (null after mask)")
		assert.True(tt, out.BackupChainBytes.Null, "LatestLogicalBackupSize==0 → backupChainBytes null after mask")
		assert.True(tt, out.SnapshotId.Null, "empty SnapshotID → snapshotId null after mask")
		assert.True(tt, out.EnforcedRetentionEndTime.Null, "no immutable attrs → enforcedRetentionEndTime null after mask")

		// Bucket-details mismatch → flags default to false. satisfiesPz* are always set by the
		// converter when BackupVault is non-nil, so here the important check is that the *value* is
		// false (and Null is false — these are plain booleans set by the converter).
		assert.True(tt, out.SatisfiesPzi.Set)
		assert.False(tt, out.SatisfiesPzi.Null)
		assert.False(tt, out.SatisfiesPzi.Value)
		assert.True(tt, out.SatisfiesPzs.Set)
		assert.False(tt, out.SatisfiesPzs.Null)
		assert.False(tt, out.SatisfiesPzs.Value)
	})
}

// TestConvertCVPBatchBackupToGCPBatchBackup_BranchCoverage exercises every `if p.X != nil` branch in
// the CVP → proxy field mapper. One subtest fills every field, the other leaves them nil/empty to
// hit the skip-branches.
func TestConvertCVPBatchBackupToGCPBatchBackup_BranchCoverage(t *testing.T) {
	t.Run("AllFieldsPopulated", func(tt *testing.T) {
		created := strfmt.DateTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
		retention := strfmt.DateTime(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
		volUsage := int64(1024)
		chainBytes := int64(4096)
		pzi := true
		pzs := false
		bType := "MANUAL"
		state := string(gcpgenserver.BatchBackupV1betaStateREADY)
		srcVol := "projects/acct/locations/us-east4/volumes/vol"
		srcSnap := "snap-name"
		bvID := "bv-1"
		desc := "a description"
		resID := "res-1"
		snapID := "00000000-0000-0000-0000-000000000001"
		volID := "00000000-0000-0000-0000-000000000002"
		volRegion := "us-east4"
		bkRegion := "us-east1"

		p := &cvpmodels.BatchBackupV1beta{
			BackupID:                 "10000000-0000-0000-0000-000000000001",
			Created:                  &created,
			VolumeUsageBytes:         &volUsage,
			BackupType:               &bType,
			SourceVolume:             &srcVol,
			BackupVaultID:            &bvID,
			SourceSnapshot:           &srcSnap,
			Description:              &desc,
			State:                    &state,
			ResourceID:               &resID,
			SnapshotID:               &snapID,
			VolumeID:                 &volID,
			BackupChainBytes:         &chainBytes,
			SatisfiesPzs:             &pzs,
			SatisfiesPzi:             &pzi,
			VolumeRegion:             &volRegion,
			BackupRegion:             &bkRegion,
			EnforcedRetentionEndTime: &retention,
		}

		out := convertCVPBatchBackupToGCPBatchBackup(p)

		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", out.BackupId.Value)
		assert.Equal(tt, time.Time(created), out.Created.Value)
		assert.Equal(tt, volUsage, out.VolumeUsageBytes.Value)
		assert.Equal(tt, gcpgenserver.BatchBackupV1betaBackupType(bType), out.BackupType.Value)
		assert.Equal(tt, srcVol, out.SourceVolume.Value)
		assert.Equal(tt, bvID, out.BackupVaultId.Value)
		assert.Equal(tt, srcSnap, out.SourceSnapshot.Value)
		assert.Equal(tt, desc, out.Description.Value)
		assert.Equal(tt, gcpgenserver.BatchBackupV1betaState(state), out.State.Value)
		assert.Equal(tt, resID, out.ResourceId.Value)
		assert.Equal(tt, snapID, out.SnapshotId.Value)
		assert.Equal(tt, volID, out.VolumeId.Value)
		assert.Equal(tt, chainBytes, out.BackupChainBytes.Value)
		assert.Equal(tt, pzs, out.SatisfiesPzs.Value)
		assert.Equal(tt, pzi, out.SatisfiesPzi.Value)
		assert.Equal(tt, volRegion, out.VolumeRegion.Value)
		assert.Equal(tt, bkRegion, out.BackupRegion.Value)
		assert.Equal(tt, time.Time(retention), out.EnforcedRetentionEndTime.Value)
	})

	t.Run("AllFieldsNilOrEmpty_NothingSet", func(tt *testing.T) {
		// Empty BackupID + empty enum strings exercise the `!= ""` guards that sit on top of the
		// nil checks for BackupType and State.
		empty := ""
		p := &cvpmodels.BatchBackupV1beta{
			BackupID:   "", // != "" guard
			BackupType: &empty,
			State:      &empty,
		}
		out := convertCVPBatchBackupToGCPBatchBackup(p)

		assert.False(tt, out.BackupId.Set)
		assert.False(tt, out.BackupType.Set, "empty BackupType string is treated as absent")
		assert.False(tt, out.State.Set, "empty State string is treated as absent")
		assert.False(tt, out.Created.Set)
		assert.False(tt, out.VolumeUsageBytes.Set)
		assert.False(tt, out.SourceVolume.Set)
		assert.False(tt, out.BackupVaultId.Set)
		assert.False(tt, out.SourceSnapshot.Set)
		assert.False(tt, out.Description.Set)
		assert.False(tt, out.ResourceId.Set)
		assert.False(tt, out.SnapshotId.Set)
		assert.False(tt, out.VolumeId.Set)
		assert.False(tt, out.BackupChainBytes.Set)
		assert.False(tt, out.SatisfiesPzs.Set)
		assert.False(tt, out.SatisfiesPzi.Set)
		assert.False(tt, out.VolumeRegion.Set)
		assert.False(tt, out.BackupRegion.Set)
		assert.False(tt, out.EnforcedRetentionEndTime.Set)
		assert.False(tt, out.AssetLocationMetadata.Set)
	})
}

// TestApplyBatchBackupFieldQuery_NilFieldSet verifies the "no fields requested" path: every
// optional field must be stripped, only BackupId survives.
func TestApplyBatchBackupFieldQuery_NilFieldSet(t *testing.T) {
	// Pre-populate every field; applyBatchBackupFieldQuery(nil) must reset them all except BackupId.
	bp := gcpgenserver.BatchBackupV1beta{
		BackupId:                 gcpgenserver.NewOptString("10000000-0000-0000-0000-000000000001"),
		Created:                  gcpgenserver.NewOptNilDateTime(time.Now()),
		VolumeUsageBytes:         gcpgenserver.NewOptNilInt64(1),
		BackupType:               gcpgenserver.NewOptNilBatchBackupV1betaBackupType("MANUAL"),
		SourceVolume:             gcpgenserver.NewOptNilString("sv"),
		BackupVaultId:            gcpgenserver.NewOptNilString("bv"),
		SourceSnapshot:           gcpgenserver.NewOptNilString("ss"),
		Description:              gcpgenserver.NewOptNilString("desc"),
		State:                    gcpgenserver.NewOptNilBatchBackupV1betaState(gcpgenserver.BatchBackupV1betaStateREADY),
		ResourceId:               gcpgenserver.NewOptNilString("res"),
		SnapshotId:               gcpgenserver.NewOptNilString("snap"),
		VolumeId:                 gcpgenserver.NewOptNilString("vol"),
		BackupChainBytes:         gcpgenserver.NewOptNilInt64(2),
		SatisfiesPzs:             gcpgenserver.NewOptNilBool(true),
		SatisfiesPzi:             gcpgenserver.NewOptNilBool(true),
		VolumeRegion:             gcpgenserver.NewOptNilString("vr"),
		BackupRegion:             gcpgenserver.NewOptNilString("br"),
		EnforcedRetentionEndTime: gcpgenserver.NewOptNilDateTime(time.Now()),
		AssetLocationMetadata:    gcpgenserver.NewOptAssetLocationMetadataV2(gcpgenserver.AssetLocationMetadataV2{}),
	}

	applyBatchBackupFieldQuery(&bp, nil)

	assert.True(t, bp.BackupId.Set, "backupId must always survive")
	assert.Equal(t, "10000000-0000-0000-0000-000000000001", bp.BackupId.Value)
	assert.False(t, bp.Created.Set)
	assert.False(t, bp.VolumeUsageBytes.Set)
	assert.False(t, bp.BackupType.Set)
	assert.False(t, bp.SourceVolume.Set)
	assert.False(t, bp.BackupVaultId.Set)
	assert.False(t, bp.SourceSnapshot.Set)
	assert.False(t, bp.Description.Set)
	assert.False(t, bp.State.Set)
	assert.False(t, bp.ResourceId.Set)
	assert.False(t, bp.SnapshotId.Set)
	assert.False(t, bp.VolumeId.Set)
	assert.False(t, bp.BackupChainBytes.Set)
	assert.False(t, bp.SatisfiesPzs.Set)
	assert.False(t, bp.SatisfiesPzi.Set)
	assert.False(t, bp.VolumeRegion.Set)
	assert.False(t, bp.BackupRegion.Set)
	assert.False(t, bp.EnforcedRetentionEndTime.Set)
	assert.False(t, bp.AssetLocationMetadata.Set)
}

// TestFetchBatchBackupsFromCVP drives the real fetchBatchBackupsFromCVP function (not the
// test-only `fetchBatchBackupsFromCVPFn` stub) using a mocked CVP client service. It verifies
// request construction (body, location, fields, correlation ID) and response handling (error
// wrapping, nil payload, nil rows). stubCreateClient is defined in batch_pool_endpoints_test.go.
func TestFetchBatchBackupsFromCVP(t *testing.T) {
	t.Run("Success_ForwardsBodyFieldsAndCorrelationID", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restore := stubCreateClient(mockBatch)
		defer restore()

		resp := &cvpBatch.V1betaBatchListBackupsOK{
			Payload: &cvpBatch.V1betaBatchListBackupsOKBody{
				Backups: []*cvpmodels.BatchBackupV1beta{{BackupID: "10000000-0000-0000-0000-000000000001"}},
			},
		}
		// Match on the constructed params so we assert location, uuids, fields, and correlation ID
		// were forwarded correctly. This also exercises the `if len(fields) > 0` and
		// `XCorrelationID.IsSet()` branches.
		mockBatch.On("V1betaBatchListBackups", mock.MatchedBy(func(p *cvpBatch.V1betaBatchListBackupsParams) bool {
			if p.LocationID != "us-east4" {
				return false
			}
			if p.Body == nil || len(p.Body.BackupUUIDs) != 2 ||
				p.Body.BackupUUIDs[0] != "10000000-0000-0000-0000-000000000001" || p.Body.BackupUUIDs[1] != "20000000-0000-0000-0000-000000000002" {
				return false
			}
			if len(p.Fields) != 2 || p.Fields[0] != "resourceId" || p.Fields[1] != "state" {
				return false
			}
			if p.XCorrelationID == nil || *p.XCorrelationID != "corr-42" {
				return false
			}
			return true
		})).Return(resp, nil)

		ctx := context.Background()
		params := gcpgenserver.V1betaBatchListBackupsParams{
			LocationId:     "us-east4",
			Fields:         []gcpgenserver.V1betaBatchListBackupsFieldsItem{"resourceId", "state"},
			XCorrelationID: gcpgenserver.NewOptString("corr-42"),
		}
		out, err := fetchBatchBackupsFromCVP(ctx, []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"}, params, batchBackupFieldsAsSet(params.Fields))
		require.NoError(tt, err)
		require.Len(tt, out, 1)
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", out[0].BackupId.Value)
	})

	t.Run("CVPError_Wrapped", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restore := stubCreateClient(mockBatch)
		defer restore()

		mockBatch.On("V1betaBatchListBackups", mock.Anything).Return(nil, errors.New("connection refused"))

		out, err := fetchBatchBackupsFromCVP(context.Background(), []string{"10000000-0000-0000-0000-000000000001"},
			gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}, nil)

		assert.Nil(tt, out)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "CVP batch list backups failed")
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("NilPayload_ReturnsEmpty", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restore := stubCreateClient(mockBatch)
		defer restore()

		mockBatch.On("V1betaBatchListBackups", mock.Anything).
			Return(&cvpBatch.V1betaBatchListBackupsOK{Payload: nil}, nil)

		out, err := fetchBatchBackupsFromCVP(context.Background(), []string{"10000000-0000-0000-0000-000000000001"},
			gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}, nil)

		require.NoError(tt, err)
		assert.Empty(tt, out)
		assert.NotNil(tt, out, "expected non-nil slice so the JSON encoder emits \"backups\": [] instead of omitting the field")
	})

	t.Run("NilRowInPayload_Skipped", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		restore := stubCreateClient(mockBatch)
		defer restore()

		mockBatch.On("V1betaBatchListBackups", mock.Anything).Return(
			&cvpBatch.V1betaBatchListBackupsOK{
				Payload: &cvpBatch.V1betaBatchListBackupsOKBody{
					Backups: []*cvpmodels.BatchBackupV1beta{
						{BackupID: "10000000-0000-0000-0000-000000000001"},
						nil,
						{BackupID: "20000000-0000-0000-0000-000000000002"},
					},
				},
			}, nil)

		out, err := fetchBatchBackupsFromCVP(context.Background(), []string{"10000000-0000-0000-0000-000000000001", "20000000-0000-0000-0000-000000000002"},
			gcpgenserver.V1betaBatchListBackupsParams{LocationId: "us-east4"}, nil)

		require.NoError(tt, err)
		require.Len(tt, out, 2, "nil row must be skipped without collapsing the other rows")
		assert.Equal(tt, "10000000-0000-0000-0000-000000000001", out[0].BackupId.Value)
		assert.Equal(tt, "20000000-0000-0000-0000-000000000002", out[1].BackupId.Value)
	})
}

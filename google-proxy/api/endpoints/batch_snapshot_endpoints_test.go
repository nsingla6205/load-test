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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// allBatchSnapshotQueryableFieldKeys is the set of optional fields supported by the batch `fields` query
// (see applyBatchSnapshotFieldQuery). Used by tests that request every field.
func allBatchSnapshotQueryableFieldKeys() map[string]bool {
	return map[string]bool{
		"created":              true,
		"resourceId":           true,
		"snapshotState":        true,
		"snapshotStateDetails": true,
		"volumeId":             true,
		"usedBytes":            true,
		"isAppConsistent":      true,
		"description":          true,
	}
}

// assertBatchSnapshotRequestedFieldsInWireJSON asserts each key in fieldSet is present in the JSON
// encoding (not omitted). Values match applyBatchSnapshotFieldQuery empty defaults when CVP omitted data.
func assertBatchSnapshotRequestedFieldsInWireJSON(tt *testing.T, bp gcpgenserver.BatchSnapshotV1beta, fieldSet map[string]bool) {
	tt.Helper()
	raw, err := json.Marshal(&bp)
	require.NoError(tt, err)
	js := string(raw)
	for k := range fieldSet {
		switch k {
		case "created":
			assert.Contains(tt, js, `"created":`, "requested created must not be omitted when CVP omits it")
			assert.Regexp(tt, `"created"\s*:\s*"`, js)
		case "resourceId":
			assert.Contains(tt, js, `"resourceId":""`, "requested resourceId must appear as empty string when CVP omits it")
		case "snapshotState":
			assert.Contains(tt, js, `"snapshotState":""`)
		case "snapshotStateDetails":
			assert.Contains(tt, js, `"snapshotStateDetails":""`)
		case "volumeId":
			assert.Contains(tt, js, `"volumeId":""`)
		case "usedBytes":
			assert.Contains(tt, js, `"usedBytes":0`)
		case "isAppConsistent":
			assert.Contains(tt, js, `"isAppConsistent":false`)
		case "description":
			assert.Contains(tt, js, `"description":""`)
		default:
			tt.Fatalf("unknown batch snapshot field key %q", k)
		}
	}
}

func stubFetchBatchSnapshotsFromCVP(snaps []gcpgenserver.BatchSnapshotV1beta, err error) func() {
	orig := fetchBatchSnapshotsFromCVPFn
	fetchBatchSnapshotsFromCVPFn = func(_ context.Context, _ []string, _ gcpgenserver.V1betaBatchListSnapshotsParams, fieldSet map[string]bool) ([]gcpgenserver.BatchSnapshotV1beta, error) {
		out := make([]gcpgenserver.BatchSnapshotV1beta, 0, len(snaps))
		for _, s := range snaps {
			bp := s
			applyBatchSnapshotFieldQuery(&bp, fieldSet)
			out = append(out, bp)
		}
		return out, err
	}
	return func() { fetchBatchSnapshotsFromCVPFn = orig }
}

func makeVCPModelSnapshot(uuid, resourceId, state string) *models.Snapshot {
	return &models.Snapshot{
		BaseModel:             models.BaseModel{UUID: uuid, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:                  resourceId,
		LifeCycleState:        state,
		LifeCycleStateDetails: "details",
		VolumeUUID:            "vol-uuid",
		VolumeName:            "vol-name",
		SizeInBytes:           42,
		Description:           "desc",
		StorageClass:          "software",
	}
}

func makeCVPBatchSnapshotModel(snapID, resourceID string) gcpgenserver.BatchSnapshotV1beta {
	return gcpgenserver.BatchSnapshotV1beta{
		SnapshotId: gcpgenserver.NewOptString(snapID),
		ResourceId: gcpgenserver.NewOptString(resourceID),
	}
}

func TestV1betaBatchListSnapshots_Auth(t *testing.T) {
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

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		unauthRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsUnauthorized)
		require.True(tt, ok)
		assert.Equal(tt, float64(http.StatusUnauthorized), unauthRes.Code)
	})

	t.Run("NilHTTPRequest_ReturnsUnauthorized", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(context.Background(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsUnauthorized)
		require.True(tt, ok)
	})
}

func TestV1betaBatchListSnapshots_Validation(t *testing.T) {
	t.Run("InvalidLocation_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "invalid location!"}

		res, err := handler.V1betaBatchListSnapshots(authContext(), req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsBadRequest)
		assert.True(tt, ok)
	})

	t.Run("EmptySnapshotUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "snapshotUuids is required")
	})

	t.Run("TooManyUuids_ReturnsBadRequest", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}

		uuids := make([]string, env.MaxBatchSnapshotUUIDs+1)
		for i := range uuids {
			uuids[i] = "uuid"
		}
		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: uuids}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(authContext(), req, params)
		require.NoError(tt, err)
		badReq, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsBadRequest)
		assert.True(tt, ok)
		assert.Contains(tt, badReq.Message, "at most")
	})
}

func TestV1betaBatchListSnapshots_VCPOnly(t *testing.T) {
	t.Run("Success_WithFields", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		snap := makeVCPModelSnapshot("snap-1", "res-1", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"snap-1"}, mock.Anything).
			Return([]*models.Snapshot{snap}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"snap-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{"resourceId", "snapshotState"},
		}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 1)
		// SnapshotId is always populated for VCP snapshots; field mask only trims other attributes.
		assert.True(tt, okRes.Snapshots[0].SnapshotId.Set)
		assert.Equal(tt, "snap-1", okRes.Snapshots[0].SnapshotId.Value)
		assert.Equal(tt, "res-1", okRes.Snapshots[0].ResourceId.Value)
		assert.Equal(tt, gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY, okRes.Snapshots[0].SnapshotState.Value)
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

		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("database error"))

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("NoFieldsRequested_ReturnsOnlySnapshotId", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		snap := makeVCPModelSnapshot("snap-1", "res-1", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"snap-1"}, mock.Anything).
			Return([]*models.Snapshot{snap}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"snap-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 1)
		assert.Equal(tt, "snap-1", okRes.Snapshots[0].SnapshotId.Value)
		assert.False(tt, okRes.Snapshots[0].ResourceId.Set)
	})

	t.Run("NilSnapshotInResult_IsSkipped", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		snap := makeVCPModelSnapshot("snap-1", "res-1", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"snap-1"}).
			Return([]*models.Snapshot{nil, snap}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"snap-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 1)
		assert.Equal(tt, "snap-1", okRes.Snapshots[0].SnapshotId.Value)
	})
}

func TestV1betaBatchListSnapshots_Parallel(t *testing.T) {
	t.Run("BothSucceed_CombinesResults", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchSnapshotsFromCVP([]gcpgenserver.BatchSnapshotV1beta{
			makeCVPBatchSnapshotModel("sde-1", "sde-res"),
		}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpSnap := makeVCPModelSnapshot("vcp-1", "vcp-res", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"vcp-1", "sde-1"}, mock.Anything).
			Return([]*models.Snapshot{vcpSnap}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"vcp-1", "sde-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 2)
		ids := map[string]bool{}
		for _, s := range okRes.Snapshots {
			if s.ResourceId.Set {
				ids[s.ResourceId.Value] = true
			}
		}
		assert.True(tt, ids["vcp-res"])
		assert.True(tt, ids["sde-res"])
	})

	t.Run("VCPFails_SDESucceeds_ReturnsSDEOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchSnapshotsFromCVP([]gcpgenserver.BatchSnapshotV1beta{
			makeCVPBatchSnapshotModel("sde-1", "sde-res"),
		}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("VCP database error"))

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 1)
		assert.Equal(tt, "sde-1", okRes.Snapshots[0].SnapshotId.Value)
	})

	t.Run("VCPSucceeds_SDEFails_ReturnsVCPOnly", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchSnapshotsFromCVP(nil, errors.New("CVP timeout"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		vcpSnap := makeVCPModelSnapshot("vcp-1", "vcp-res", datamodel.LifeCycleStateREADY)
		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"vcp-1"}, mock.Anything).
			Return([]*models.Snapshot{vcpSnap}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"vcp-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId: "us-east4",
			Fields:     []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{"resourceId"},
		}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		require.Len(tt, okRes.Snapshots, 1)
		assert.Equal(tt, "vcp-res", okRes.Snapshots[0].ResourceId.Value)
	})

	t.Run("BothFail_Returns500", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchSnapshotsFromCVP(nil, errors.New("CVP down"))
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"uuid-1"}, mock.Anything).
			Return(nil, errors.New("VCP down"))

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"uuid-1"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		_, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsInternalServerError)
		assert.True(tt, ok)
	})

	t.Run("BothReturnEmpty_ReturnsEmptyArray", func(tt *testing.T) {
		restore := stubParseRegionAndZone()
		defer restore()
		restoreAuth := stubBatchAuth(true)
		defer restoreAuth()
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = "" }()

		restoreCVP := stubFetchBatchSnapshotsFromCVP([]gcpgenserver.BatchSnapshotV1beta{}, nil)
		defer restoreCVP()

		mockOrch := factory.NewMockOrchestratorFactory(tt)
		handler := &Handler{Orchestrator: mockOrch}
		ctx := authContext()

		mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"nonexistent"}, mock.Anything).
			Return([]*models.Snapshot{}, nil)

		req := &gcpgenserver.BatchSnapshotUUIDListV1beta{SnapshotUuids: []string{"nonexistent"}}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}

		res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
		require.NoError(tt, err)
		okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
		require.True(tt, ok)
		assert.Empty(tt, okRes.Snapshots)
	})
}

func TestBatchSnapshotFieldsAsSet(t *testing.T) {
	t.Run("NilFields_ReturnsNil", func(tt *testing.T) {
		assert.Nil(tt, batchSnapshotFieldsAsSet(nil))
	})

	t.Run("EmptyFields_ReturnsNil", func(tt *testing.T) {
		assert.Nil(tt, batchSnapshotFieldsAsSet([]gcpgenserver.V1betaBatchListSnapshotsFieldsItem{}))
	})

	t.Run("PopulatesSet", func(tt *testing.T) {
		fs := batchSnapshotFieldsAsSet([]gcpgenserver.V1betaBatchListSnapshotsFieldsItem{"resourceId", "created"})
		assert.True(tt, fs["resourceId"])
		assert.True(tt, fs["created"])
		assert.False(tt, fs["volumeId"])
	})
}

func TestMapLifecycleToBatchSnapshotState(t *testing.T) {
	assert.Equal(t, gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED, mapLifecycleToBatchSnapshotState(""))
	assert.Equal(t, gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY, mapLifecycleToBatchSnapshotState(string(gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY)))
	states := []string{
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateCREATING),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateUPDATING),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateRESTORING),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateDELETED),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateDISABLED),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateDELETING),
		string(gcpgenserver.BatchSnapshotV1betaSnapshotStateERROR),
	}
	for _, s := range states {
		assert.Equal(t, gcpgenserver.BatchSnapshotV1betaSnapshotState(s), mapLifecycleToBatchSnapshotState(s))
	}
	assert.Equal(t, gcpgenserver.BatchSnapshotV1betaSnapshotStateSTATEUNSPECIFIED, mapLifecycleToBatchSnapshotState("UNKNOWN_STATE_XYZ"))
}

func TestConvertSnapshotToBatchSnapshot(t *testing.T) {
	snap := makeVCPModelSnapshot("id-1", "res", datamodel.LifeCycleStateREADY)
	t.Run("nilFieldSet", func(tt *testing.T) {
		out := convertSnapshotToBatchSnapshot(snap, nil)
		assert.True(tt, out.SnapshotId.Set)
		assert.False(tt, out.ResourceId.Set)
	})
	t.Run("withFieldSet", func(tt *testing.T) {
		fs := map[string]bool{"resourceId": true, "created": true}
		out := convertSnapshotToBatchSnapshot(snap, fs)
		assert.Equal(tt, "res", out.ResourceId.Value)
		assert.True(tt, out.Created.Set)
	})
}

func TestBatchSnapshot_RequestedFieldMissingValue_JSONWireFormat(t *testing.T) {
	t.Run("VCP_datastore_emptyDescription_requested_emitsEmptyString", func(tt *testing.T) {
		snap := &models.Snapshot{
			BaseModel:   models.BaseModel{UUID: "11111111-1111-1111-1111-111111111111", CreatedAt: time.Now()},
			Name:        "snap-name",
			Description: "",
		}
		fieldSet := map[string]bool{"description": true}
		out := convertSnapshotToBatchSnapshot(snap, fieldSet)
		require.True(tt, out.Description.Set)
		assert.Equal(tt, "", out.Description.Value)

		raw, err := json.Marshal(&out)
		require.NoError(tt, err)
		assert.Contains(tt, string(raw), `"description":""`)
	})

	t.Run("CVP_upstream_nilOptionalPointers_requested_emitsEmptyStrings", func(tt *testing.T) {
		p := &cvpmodels.BatchSnapshotV1beta{
			SnapshotID: "22222222-2222-2222-2222-222222222222",
			// Description and ResourceID nil — simulates CVP omitting empty values
		}
		bp := convertCVPBatchSnapshotToGCPBatchSnapshot(p)
		fieldSet := map[string]bool{"description": true, "resourceId": true}
		applyBatchSnapshotFieldQuery(&bp, fieldSet)
		require.True(tt, bp.Description.Set)
		require.True(tt, bp.ResourceId.Set)
		assert.Equal(tt, "", bp.Description.Value)
		assert.Equal(tt, "", bp.ResourceId.Value)

		raw, err := json.Marshal(&bp)
		require.NoError(tt, err)
		assert.Contains(tt, string(raw), `"description":""`)
		assert.Contains(tt, string(raw), `"resourceId":""`)
	})
}

func TestApplyBatchSnapshotFieldQuery(t *testing.T) {
	bp := gcpgenserver.BatchSnapshotV1beta{
		SnapshotId: gcpgenserver.NewOptString("s"),
		ResourceId: gcpgenserver.NewOptString("r"),
		Created:    gcpgenserver.NewOptDateTime(time.Now()),
	}
	t.Run("nilFieldSet_resetsOptional", func(tt *testing.T) {
		applyBatchSnapshotFieldQuery(&bp, nil)
		assert.True(tt, bp.SnapshotId.Set)
		assert.False(tt, bp.ResourceId.Set)
		assert.False(tt, bp.Created.Set)
	})
	t.Run("partialFieldSet", func(tt *testing.T) {
		bp2 := gcpgenserver.BatchSnapshotV1beta{
			SnapshotId: gcpgenserver.NewOptString("s"),
			ResourceId: gcpgenserver.NewOptString("r"),
			Created:    gcpgenserver.NewOptDateTime(time.Now()),
		}
		applyBatchSnapshotFieldQuery(&bp2, map[string]bool{"resourceId": true})
		assert.False(tt, bp2.Created.Set)
		assert.True(tt, bp2.ResourceId.Set)
	})
}

func TestConvertCVPBatchSnapshotToGCPBatchSnapshot(t *testing.T) {
	dt := strfmt.DateTime(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	rid := "res"
	vid := "vol-id"
	ub := float64(99)
	desc := "d"
	appConsistent := true
	p := &cvpmodels.BatchSnapshotV1beta{
		SnapshotID:           "sid",
		Created:              &dt,
		ResourceID:           &rid,
		SnapshotState:        string(gcpgenserver.BatchSnapshotV1betaSnapshotStateREADY),
		SnapshotStateDetails: &desc,
		VolumeID:             &vid,
		UsedBytes:            &ub,
		IsAppConsistent:      &appConsistent,
		Description:          &desc,
	}
	out := convertCVPBatchSnapshotToGCPBatchSnapshot(p)
	assert.Equal(t, "sid", out.SnapshotId.Value)
	assert.True(t, out.Created.Set)
	assert.True(t, out.IsAppConsistent.Set)
	assert.True(t, out.IsAppConsistent.Value)
}

func TestConvertCVPBatchSnapshotToGCPBatchSnapshot_minimal(t *testing.T) {
	out := convertCVPBatchSnapshotToGCPBatchSnapshot(&cvpmodels.BatchSnapshotV1beta{})
	assert.False(t, out.SnapshotId.Set)
}

func TestFetchBatchSnapshotsFromCVP(t *testing.T) {
	t.Run("clientError", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(nil, errors.New("boom"))

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		logger := log.NewLogger()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, logger)

		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}
		_, err := fetchBatchSnapshotsFromCVP(ctx, []string{"a"}, params, nil)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "CVP batch list snapshots failed")
	})

	t.Run("success_nilResponse", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(nil, nil)

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, log.NewLogger())

		out, err := fetchBatchSnapshotsFromCVP(ctx, []string{"snap-1"}, gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}, nil)
		require.NoError(tt, err)
		assert.Empty(tt, out)
	})

	t.Run("success_nilPayload", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(&cvpBatch.V1betaBatchListSnapshotsOK{}, nil)

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, log.NewLogger())

		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId:     "us-east4",
			Fields:         []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{"resourceId"},
			XCorrelationID: gcpgenserver.NewOptString("corr-1"),
		}
		out, err := fetchBatchSnapshotsFromCVP(ctx, []string{"snap-1"}, params, map[string]bool{"resourceId": true})
		require.NoError(tt, err)
		assert.Empty(tt, out)
	})

	t.Run("success_withPayload_skipsNilEntries", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		resX := "res-x"
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(&cvpBatch.V1betaBatchListSnapshotsOK{
			Payload: &cvpBatch.V1betaBatchListSnapshotsOKBody{
				Snapshots: []*cvpmodels.BatchSnapshotV1beta{
					nil,
					{SnapshotID: "x", ResourceID: &resX},
				},
			},
		}, nil)

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, log.NewLogger())

		params := gcpgenserver.V1betaBatchListSnapshotsParams{LocationId: "us-east4"}
		out, err := fetchBatchSnapshotsFromCVP(ctx, []string{"snap-1"}, params, nil)
		require.NoError(tt, err)
		require.Len(tt, out, 1)
		assert.Equal(tt, "x", out[0].SnapshotId.Value)
	})

	// CVP returns only snapshotId; every optional field was omitted from the upstream payload.
	// Query params request all optional fields — response must include each with empty defaults, not omit keys.
	t.Run("success_minimalCVPPayload_allRequestedFieldsPresentInWireJSON", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(&cvpBatch.V1betaBatchListSnapshotsOK{
			Payload: &cvpBatch.V1betaBatchListSnapshotsOKBody{
				Snapshots: []*cvpmodels.BatchSnapshotV1beta{
					{SnapshotID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
				},
			},
		}, nil)

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, log.NewLogger())

		fs := allBatchSnapshotQueryableFieldKeys()
		fieldItems := make([]gcpgenserver.V1betaBatchListSnapshotsFieldsItem, 0, len(fs))
		for k := range fs {
			fieldItems = append(fieldItems, gcpgenserver.V1betaBatchListSnapshotsFieldsItem(k))
		}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId: "us-east4",
			Fields:     fieldItems,
		}

		out, err := fetchBatchSnapshotsFromCVP(ctx, []string{"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}, params, fs)
		require.NoError(tt, err)
		require.Len(tt, out, 1)
		bp := out[0]
		assert.Equal(tt, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", bp.SnapshotId.Value)
		assertBatchSnapshotRequestedFieldsInWireJSON(tt, bp, fs)
	})

	// CVP returns resourceId but omits description (nil). Client requested both — description must still appear in JSON.
	t.Run("success_partialCVP_requestedDescriptionMissingFromUpstream_notOmittedInJSON", func(tt *testing.T) {
		mockBatch := cvpBatch.NewMockClientService(tt)
		resFromCVP := "resource-from-cvp"
		mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(&cvpBatch.V1betaBatchListSnapshotsOK{
			Payload: &cvpBatch.V1betaBatchListSnapshotsOKBody{
				Snapshots: []*cvpmodels.BatchSnapshotV1beta{
					{
						SnapshotID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
						ResourceID: &resFromCVP,
					},
				},
			},
		}, nil)

		origClient := createClient
		defer func() { createClient = origClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Batch: mockBatch}
		}

		ctx := authContext()
		ctx = context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, log.NewLogger())

		fieldSet := map[string]bool{
			"resourceId":  true,
			"description": true,
		}
		params := gcpgenserver.V1betaBatchListSnapshotsParams{
			LocationId: "us-east4",
			Fields: []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{
				"resourceId", "description",
			},
		}

		out, err := fetchBatchSnapshotsFromCVP(ctx, []string{"bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}, params, fieldSet)
		require.NoError(tt, err)
		require.Len(tt, out, 1)
		bp := out[0]
		assert.Equal(tt, resFromCVP, bp.ResourceId.Value)
		assert.True(tt, bp.Description.Set)
		assert.Equal(tt, "", bp.Description.Value)

		raw, err := json.Marshal(&bp)
		require.NoError(tt, err)
		js := string(raw)
		assert.Contains(tt, js, `"resourceId":"resource-from-cvp"`)
		assert.Contains(tt, js, `"description":""`)
	})
}

// TestV1betaBatchListSnapshots_CVPMissingRequestedFields exercises the handler + parallel CVP path:
// mocked CVP omits optional fields that are still listed in query params — they must appear in the HTTP JSON body.
func TestV1betaBatchListSnapshots_CVPMissingRequestedFields(t *testing.T) {
	restore := stubParseRegionAndZone()
	defer restore()
	restoreAuth := stubBatchAuth(true)
	defer restoreAuth()
	cvp.CVP_HOST = "http://cvp-host"
	defer func() { cvp.CVP_HOST = "" }()

	mockBatch := cvpBatch.NewMockClientService(t)
	mockBatch.EXPECT().V1betaBatchListSnapshots(mock.Anything).Return(&cvpBatch.V1betaBatchListSnapshotsOK{
		Payload: &cvpBatch.V1betaBatchListSnapshotsOKBody{
			Snapshots: []*cvpmodels.BatchSnapshotV1beta{
				{SnapshotID: "cccccccc-cccc-cccc-cccc-cccccccccccc"},
			},
		},
	}, nil)

	origClient := createClient
	defer func() { createClient = origClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Batch: mockBatch}
	}

	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := &Handler{Orchestrator: mockOrch}
	ctx := authContext()

	mockOrch.On("GetSnapshotsByUUIDs", mock.Anything, []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"}, mock.Anything).
		Return([]*models.Snapshot{}, nil)

	fs := map[string]bool{
		"resourceId":      true,
		"description":     true,
		"isAppConsistent": true,
	}
	req := &gcpgenserver.BatchSnapshotUUIDListV1beta{
		SnapshotUuids: []string{"cccccccc-cccc-cccc-cccc-cccccccccccc"},
	}
	params := gcpgenserver.V1betaBatchListSnapshotsParams{
		LocationId: "us-east4",
		Fields: []gcpgenserver.V1betaBatchListSnapshotsFieldsItem{
			"resourceId", "description", "isAppConsistent",
		},
	}

	res, err := handler.V1betaBatchListSnapshots(ctx, req, params)
	require.NoError(t, err)
	okRes, ok := res.(*gcpgenserver.V1betaBatchListSnapshotsOK)
	require.True(t, ok)
	require.Len(t, okRes.Snapshots, 1)
	bp := okRes.Snapshots[0]
	assert.Equal(t, "cccccccc-cccc-cccc-cccc-cccccccccccc", bp.SnapshotId.Value)
	assertBatchSnapshotRequestedFieldsInWireJSON(t, bp, fs)
}

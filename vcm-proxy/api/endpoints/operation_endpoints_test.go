package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
	vcmconstants "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/utils/constants"
)

func configuredOperationHandler(t *testing.T) (*Handler, *vcmconfig.Store) {
	t.Helper()
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-operation-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		DbConfig: vcmserver.DbConfigV1{
			Project:  "test-project",
			Instance: "test-instance",
			Database: "test-db",
		},
	})
	waitForConfigured(t, store)
	return &Handler{ConfigStore: store}, store
}

func TestOperationGetV1_ReturnsPersistedJobState(t *testing.T) {
	tests := []struct {
		name       string
		state      vcmdb.JobState
		done       bool
		wantError  bool
		trackingID int
		wantCode   int
	}{
		{name: "new", state: vcmdb.JobStateNew, done: false},
		{name: "processing", state: vcmdb.JobStateProcessing, done: false},
		{name: "done", state: vcmdb.JobStateDone, done: true},
		{
			name:       "error preserves cause code",
			state:      vcmdb.JobStateError,
			done:       true,
			wantError:  true,
			trackingID: vsaerrors.ErrSnapshotNotAllowedForVolume,
			wantCode:   http.StatusBadRequest,
		},
		{
			name:      "legacy error defaults to internal server error",
			state:     vcmdb.JobStateError,
			done:      true,
			wantError: true,
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store := configuredOperationHandler(t)
			operationID := uuid.NewString()
			require.NoError(t, store.CreateJob(context.Background(), &vcmdb.Job{
				ID:           operationID,
				ResourceID:   "gcnv-test",
				Type:         vcmconstants.JobTypePoolCreate,
				State:        tt.state,
				TrackingID:   tt.trackingID,
				ErrorMessage: "pool create failed",
				Retriable:    tt.wantError,
			}))

			result, err := handler.OperationGetV1(context.Background(), vcmserver.OperationGetV1Params{
				OperationId: operationID,
			})
			require.NoError(t, err)

			response, ok := result.(*vcmserver.OperationV1)
			require.True(t, ok)
			assert.Equal(t, "/v1/operations/"+operationID, response.Name)
			assert.Equal(t, tt.done, response.Done)
			assert.True(t, response.Metadata.IsSet())
			assert.Equal(t, tt.wantError, response.Error.IsSet())
			assert.Equal(t, tt.state == vcmdb.JobStateDone, response.Response.IsSet())
			if tt.wantError {
				operationError, ok := response.Error.Get()
				require.True(t, ok)
				assert.Equal(t, float64(tt.wantCode), operationError.Code)
				assert.Equal(t, "pool create failed", operationError.Message)
				retriable, ok := operationError.Retriable.Get()
				require.True(t, ok)
				assert.True(t, retriable)
			}
		})
	}
}

func TestOperationGetV1_ReturnsBadRequestForInvalidOperationID(t *testing.T) {
	result, err := (Handler{}).OperationGetV1(context.Background(), vcmserver.OperationGetV1Params{
		OperationId: "invalid",
	})

	require.NoError(t, err)
	assert.IsType(t, &vcmserver.OperationGetV1BadRequest{}, result)
}

func TestOperationGetV1_ReturnsNotFoundForUnknownOperation(t *testing.T) {
	handler, _ := configuredOperationHandler(t)

	result, err := handler.OperationGetV1(context.Background(), vcmserver.OperationGetV1Params{
		OperationId: uuid.NewString(),
	})

	require.NoError(t, err)
	notFound, ok := result.(*vcmserver.OperationGetV1NotFound)
	require.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
	assert.Equal(t, vcmconstants.OperationNotFound, notFound.Message)
}

func TestOperationGetV1_ReturnsInternalServerErrorWhenStoreUnavailable(t *testing.T) {
	tests := []struct {
		name    string
		handler Handler
	}{
		{name: "nil config store", handler: Handler{}},
		{
			name: "database not configured",
			handler: Handler{
				ConfigStore: vcmconfig.NewStore(log.NewLogger(), vcmconfig.DefaultDBValidateTimeout),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.handler.OperationGetV1(context.Background(), vcmserver.OperationGetV1Params{
				OperationId: uuid.NewString(),
			})

			require.NoError(t, err)
			assert.IsType(t, &vcmserver.OperationGetV1InternalServerError{}, result)
		})
	}
}

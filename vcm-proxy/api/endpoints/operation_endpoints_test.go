package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

func configuredOperationHandler(t *testing.T) (*Handler, *vcmconfig.Store) {
	t.Helper()
	store := vcmconfig.NewStoreWithOpener(log.NewLogger(), &stubDBOpener{
		dsn: fmt.Sprintf("file:vcm-operation-endpoint-%s?mode=memory&cache=shared", t.Name()),
	})
	store.Apply(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{
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
		name                     string
		state                    vcmdb.JobState
		done                     bool
		wantError                bool
		wantResponse             bool
		responsePayload          string
		trackingID               int
		wantCode                 int
		wantMessage              string
		wantResponseDeploymentID string
	}{
		{name: "new", state: vcmdb.JobStateNew, done: false},
		{name: "processing", state: vcmdb.JobStateProcessing, done: false},
		{
			// A payload on a still-running job must stay hidden until the job
			// is terminal, so callers cannot read a result while done is false.
			name:            "processing with response payload omits response",
			state:           vcmdb.JobStateProcessing,
			done:            false,
			responsePayload: `{"pool":{"deploymentId":"gcnv-test","ipConfig":{"dataLif":{"ilbnas":"10.33.182.243"}}}}`,
		},
		{name: "done without response payload", state: vcmdb.JobStateDone, done: true},
		{
			name:                     "done with response payload",
			state:                    vcmdb.JobStateDone,
			done:                     true,
			wantResponse:             true,
			responsePayload:          `{"pool":{"deploymentId":"gcnv-test"}}`,
			wantResponseDeploymentID: "gcnv-test",
		},
		{
			name:            "done with flat volume response payload",
			state:           vcmdb.JobStateDone,
			done:            true,
			wantResponse:    true,
			responsePayload: `{"uuid":"9760acf5-4638-11e7-9bdb-020073ca0001","name":"vol-1","sizeInBytes":107374182400}`,
		},
		{
			name:            "done with invalid response payload omits response",
			state:           vcmdb.JobStateDone,
			done:            true,
			responsePayload: `not-json`,
		},
		{
			name:            "done with json null payload omits response",
			state:           vcmdb.JobStateDone,
			done:            true,
			responsePayload: "null",
		},
		{
			name:            "done with empty object payload omits response",
			state:           vcmdb.JobStateDone,
			done:            true,
			responsePayload: "{}",
		},
		{
			name:        "allow-listed error surfaces stored debug message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			trackingID:  vsaerrors.ErrSnapshotNotAllowedForVolume,
			wantCode:    http.StatusBadRequest,
			wantMessage: "pool create failed",
		},
		{
			name:        "host group in use surfaces catalog message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			trackingID:  vsaerrors.ErrHostGroupHasLUNMaps,
			wantCode:    http.StatusBadRequest,
			wantMessage: vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrHostGroupHasLUNMaps).Message,
		},
		{
			name:        "host group not found surfaces catalog message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			trackingID:  vsaerrors.ErrHostGroupNotFound,
			wantCode:    http.StatusNotFound,
			wantMessage: vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrHostGroupNotFound).Message,
		},
		{
			name:        "host group already exists surfaces catalog message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			trackingID:  vsaerrors.ErrHostGroupAlreadyExists,
			wantCode:    http.StatusConflict,
			wantMessage: vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrHostGroupAlreadyExists).Message,
		},
		{
			name:        "unmapped error uses catalog message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			wantCode:    http.StatusInternalServerError,
			wantMessage: vsaerrors.GetErrorMessageByTrackingID(0).Message,
		},
		{
			name:        "non allow-listed error uses catalog message",
			state:       vcmdb.JobStateError,
			done:        true,
			wantError:   true,
			trackingID:  vsaerrors.ErrSVMNotFound,
			wantCode:    http.StatusInternalServerError,
			wantMessage: vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrSVMNotFound).Message,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, store := configuredOperationHandler(t)
			operationID := uuid.NewString()
			require.NoError(t, store.CreateJob(context.Background(), &vcmdb.Job{
				ID:              operationID,
				ResourceID:      "gcnv-test",
				Type:            string(datamodel.JobTypeCreatePool),
				State:           tt.state,
				TrackingID:      tt.trackingID,
				ErrorMessage:    "pool create failed",
				ResponsePayload: tt.responsePayload,
				Retriable:       tt.wantError,
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
			assert.Equal(t, tt.wantResponse, response.Response.IsSet())
			if tt.wantResponse {
				opResponse, ok := response.Response.Get()
				require.True(t, ok)
				if tt.wantResponseDeploymentID != "" {
					poolRaw, ok := opResponse["pool"]
					require.True(t, ok)
					assert.Contains(t, string(poolRaw), tt.wantResponseDeploymentID)
				}
				if tt.name == "done with flat volume response payload" {
					assert.Contains(t, string(opResponse["uuid"]), "9760acf5-4638-11e7-9bdb-020073ca0001")
					assert.Contains(t, string(opResponse["name"]), "vol-1")
					assert.NotContains(t, opResponse, "volume")
				}
			}
			if tt.wantError {
				operationError, ok := response.Error.Get()
				require.True(t, ok)
				assert.Equal(t, float64(tt.wantCode), operationError.Code)
				assert.Equal(t, tt.wantMessage, operationError.Message)
				retriable, ok := operationError.Retriable.Get()
				require.True(t, ok)
				assert.True(t, retriable)
			}
		})
	}
}

func TestDescribeVCMOperationSurfaceJobErrorDetails(t *testing.T) {
	assert.False(t, describeVCMOperationSurfaceJobErrorDetails(nil))

	rotateWithDetails := &vcmdb.Job{
		Type:         string(datamodel.JobTypeRotateCmekBackups),
		ErrorMessage: "any",
	}
	assert.True(t, describeVCMOperationSurfaceJobErrorDetails(rotateWithDetails))

	provisionErrOnly := &vcmdb.Job{
		TrackingID:   vsaerrors.ErrGCPResourceProvisionError,
		ErrorMessage: "internal",
	}
	assert.False(t, describeVCMOperationSurfaceJobErrorDetails(provisionErrOnly))

	allowListedButEmptyDetails := &vcmdb.Job{
		TrackingID: vsaerrors.ErrRestoreVolumeValidation,
	}
	assert.False(t, describeVCMOperationSurfaceJobErrorDetails(allowListedButEmptyDetails))

	allowListedWithDetails := &vcmdb.Job{
		TrackingID:   vsaerrors.ErrRestoreVolumeValidation,
		ErrorMessage: "restore failed: size too small",
	}
	assert.True(t, describeVCMOperationSurfaceJobErrorDetails(allowListedWithDetails))
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
	assert.Equal(t, datamodel.OperationNotFound, notFound.Message)
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

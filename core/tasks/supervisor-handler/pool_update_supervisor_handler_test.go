package supervisorhandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestPoolUpdateHandler_JobTypes(t *testing.T) {
	handler := NewPoolUpdateHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 3)
	require.Contains(t, jobTypes, datamodel.JobTypeUpdatePool)
	require.Contains(t, jobTypes, datamodel.JobTypeUpdateLargePool)
	require.Contains(t, jobTypes, datamodel.JobTypeEncryptPool)
}

func TestNewPoolUpdateHandler(t *testing.T) {
	handler := NewPoolUpdateHandler()
	require.NotNil(t, handler)
	require.IsType(t, &PoolUpdateHandler{}, handler)
}

func TestPoolUpdateHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "pool-uuid"},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
}

func TestPoolUpdateHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
}

func TestPoolUpdateHandler_Handle_SkipsNilJobAttributes(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{JobAttributes: nil}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
}

func TestPoolUpdateHandler_Handle_PoolNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "pool-uuid"},
	}

	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return((*datamodel.Pool)(nil), vsaerrors.NewNotFoundErr("pool", nil)).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPoolUpdateHandler_Handle_GetPoolError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "pool-uuid"},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return((*datamodel.Pool)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load pool")
}

func TestPoolUpdateHandler_Handle_SkipsNonUpdatingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "pool-uuid"},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateAvailable,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestPoolUpdateHandler_Handle_SuccessWithPreviousState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "pool-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, previousState, previousStateDetails).Return(pool, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestPoolUpdateHandler_Handle_SuccessWithFallbackToAvailable(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
			// PreviousState is empty, should fallback to AVAILABLE
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestPoolUpdateHandler_Handle_UpdatePoolStateError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "pool-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, previousState, previousStateDetails).Return((*datamodel.Pool)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert pool state")
}

func TestPoolUpdateHandler_Handle_WithPreviousStateDetails(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := "Custom state details"

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "pool-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, previousState, previousStateDetails).Return(pool, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestPoolUpdateHandler_Handle_UpdateLargePoolJobType(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	job := &datamodel.Job{
		Type: string(datamodel.JobTypeUpdateLargePool),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "pool-uuid",
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(pool, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestPoolUpdateHandler_Handle_CompleteSuccessPath(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewPoolUpdateHandler()

	// Test the complete success path including the final logger.Infof call
	job := &datamodel.Job{
		BaseModel: datamodel.BaseModel{
			UUID: "test-job-uuid",
		},
		Type: string(datamodel.JobTypeUpdatePool),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "pool-uuid",
			PreviousState:        datamodel.LifeCycleStateREADY,
			PreviousStateDetails: datamodel.LifeCycleStateReadyDetails,
		},
	}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()

	// This should execute all code including logger initialization and final logger.Infof
	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

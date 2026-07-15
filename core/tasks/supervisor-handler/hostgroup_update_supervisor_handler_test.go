package supervisorhandler

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

const (
	testHGUUID      = "hg-uuid"
	testHGAccountID = int64(7)
)

func hgUpdateJob(state datamodel.JobState, attrs *datamodel.JobAttributes) *datamodel.Job {
	return &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateHostGroup),
		State:         string(state),
		AccountID:     sql.NullInt64{Int64: testHGAccountID, Valid: true},
		JobAttributes: attrs,
	}
}

func TestHostGroupUpdateHandler_JobTypes(t *testing.T) {
	handler := NewHostGroupUpdateHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeUpdateHostGroup)
}

func TestNewHostGroupUpdateHandler(t *testing.T) {
	handler := NewHostGroupUpdateHandler()
	require.NotNil(t, handler)
	require.IsType(t, &HostGroupUpdateHandler{}, handler)
}

func TestHostGroupUpdateHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{ResourceUUID: testHGUUID})

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetHostGroup", mock.Anything, mock.Anything, mock.Anything)
}

func TestHostGroupUpdateHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetHostGroup", mock.Anything, mock.Anything, mock.Anything)
}

func TestHostGroupUpdateHandler_Handle_SkipsMissingAccountID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := &datamodel.Job{
		State:         string(datamodel.JobsStateNEW),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: testHGUUID},
	}

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetHostGroup", mock.Anything, mock.Anything, mock.Anything)
}

func TestHostGroupUpdateHandler_Handle_HostGroupNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{ResourceUUID: testHGUUID})

	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).
		Return((*datamodel.HostGroup)(nil), vsaerrors.NewNotFoundErr("host group", nil)).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateHostGroupsState", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestHostGroupUpdateHandler_Handle_GetHostGroupError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{ResourceUUID: testHGUUID})

	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).
		Return((*datamodel.HostGroup)(nil), errors.New("database error")).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load host group for update cleanup")
}

func TestHostGroupUpdateHandler_Handle_SkipsNonUpdatingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{ResourceUUID: testHGUUID})

	hg := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: testHGUUID},
		State:     datamodel.LifeCycleStateREADY,
	}
	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).Return(hg, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateHostGroupsState", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestHostGroupUpdateHandler_Handle_NewStateRevertsToPreviousState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{
		ResourceUUID:         testHGUUID,
		PreviousState:        datamodel.LifeCycleStateDisabled,
		PreviousStateDetails: datamodel.LifeCycleStateDisabledDetails,
	})

	hg := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: testHGUUID},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).Return(hg, nil).Once()
	storage.EXPECT().UpdateHostGroupsState(mock.Anything, []string{testHGUUID}, testHGAccountID,
		datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestHostGroupUpdateHandler_Handle_NewStateFallsBackToReady(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStateNEW, &datamodel.JobAttributes{ResourceUUID: testHGUUID})

	hg := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: testHGUUID},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).Return(hg, nil).Once()
	storage.EXPECT().UpdateHostGroupsState(mock.Anything, []string{testHGUUID}, testHGAccountID,
		datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestHostGroupUpdateHandler_Handle_ProcessingStateTransitionsToError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewHostGroupUpdateHandler()

	job := hgUpdateJob(datamodel.JobsStatePROCESSING, &datamodel.JobAttributes{
		ResourceUUID:  testHGUUID,
		PreviousState: datamodel.LifeCycleStateREADY,
	})

	hg := &datamodel.HostGroup{
		BaseModel: datamodel.BaseModel{UUID: testHGUUID},
		State:     datamodel.LifeCycleStateUpdating,
	}
	storage.EXPECT().GetHostGroup(mock.Anything, testHGUUID, testHGAccountID).Return(hg, nil).Once()
	storage.EXPECT().UpdateHostGroupsState(mock.Anything, []string{testHGUUID}, testHGAccountID,
		datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails).Return(nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

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

func TestActiveDirectoryDeleteHandler_JobTypes(t *testing.T) {
	handler := NewActiveDirectoryDeleteHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeDeleteActiveDirectory)
}

func TestActiveDirectoryDeleteHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_SkipsMissingAccountID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	// A job without an account ID cannot be scoped to a tenant, so the handler must skip rather than fall
	// back to an account-wildcard lookup that could touch another tenant's Active Directory.
	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_ADNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).
		Return((*datamodel.ActiveDirectory)(nil), vsaerrors.NewNotFoundErr("active_directory", nil)).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateActiveDirectory", mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_GetADError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).
		Return((*datamodel.ActiveDirectory)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load Active Directory for delete cleanup")
}

func TestActiveDirectoryDeleteHandler_Handle_SkipsNonDeletingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateREADY,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateActiveDirectory", mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_SuccessWithPreviousState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "ad-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
		return a.UUID == "ad-uuid" && a.State == previousState && a.StateDetails == previousStateDetails
	})).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestActiveDirectoryDeleteHandler_Handle_SuccessWithFallbackToError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "ad-uuid",
			// PreviousState is empty, should fall back to ERROR
		},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
		return a.UUID == "ad-uuid" && a.State == datamodel.LifeCycleStateError &&
			a.StateDetails == datamodel.LifeCycleStateDeletionErrorDetails
	})).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestActiveDirectoryDeleteHandler_Handle_DisallowedPreviousStateFallsBackToError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	// A tampered/corrupted job record with a non-allow-listed PreviousState must not bypass the state
	// machine; the handler falls back to ERROR instead of restoring the arbitrary value.
	job := &datamodel.Job{
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "ad-uuid",
			PreviousState:        datamodel.LifeCycleStateCreating,
			PreviousStateDetails: datamodel.LifeCycleStateCreatingDetails,
		},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
		return a.UUID == "ad-uuid" && a.State == datamodel.LifeCycleStateError &&
			a.StateDetails == datamodel.LifeCycleStateDeletionErrorDetails
	})).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestActiveDirectoryDeleteHandler_Handle_ProcessingTimeoutMovesToError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	// PROCESSING-state delete that timed out: the delete intent must be preserved, so the AD moves to
	// ERROR rather than reverting to its previous (READY) state.
	job := &datamodel.Job{
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
		State:     string(datamodel.JobsStatePROCESSING),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "ad-uuid",
			PreviousState:        datamodel.LifeCycleStateREADY,
			PreviousStateDetails: datamodel.LifeCycleStateReadyDetails,
		},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
		return a.UUID == "ad-uuid" && a.State == datamodel.LifeCycleStateError &&
			a.StateDetails == datamodel.LifeCycleStateDeletionErrorDetails
	})).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestActiveDirectoryDeleteHandler_Handle_ProcessingTimeoutSkipsNonDeletingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		State:         string(datamodel.JobsStatePROCESSING),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateREADY,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateActiveDirectory", mock.Anything, mock.Anything)
}

func TestActiveDirectoryDeleteHandler_Handle_ProcessingTimeoutUpdateError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		State:         string(datamodel.JobsStatePROCESSING),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.Anything).
		Return((*datamodel.ActiveDirectory)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition Active Directory")
}

func TestActiveDirectoryDeleteHandler_Handle_UpdateError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryDeleteHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "ad-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateDeleting,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.Anything).
		Return((*datamodel.ActiveDirectory)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert Active Directory")
}

func TestNewActiveDirectoryDeleteHandler(t *testing.T) {
	handler := NewActiveDirectoryDeleteHandler()
	require.NotNil(t, handler)
	require.IsType(t, &ActiveDirectoryDeleteHandler{}, handler)
}

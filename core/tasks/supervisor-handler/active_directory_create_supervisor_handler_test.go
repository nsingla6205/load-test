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

func TestActiveDirectoryCreateHandler_JobTypes(t *testing.T) {
	handler := NewActiveDirectoryCreateHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeCreateActiveDirectory)
}

func TestActiveDirectoryCreateHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryCreateHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryCreateHandler_Handle_SkipsMissingAccountID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	// A job without an account ID cannot be scoped to a tenant, so the handler must skip rather than fall
	// back to an account-wildcard lookup that could touch another tenant's Active Directory.
	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetActiveDirectoryByUuidAndAccountId", mock.Anything, mock.Anything, mock.Anything)
}

func TestActiveDirectoryCreateHandler_Handle_ADNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

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

func TestActiveDirectoryCreateHandler_Handle_GetADError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).
		Return((*datamodel.ActiveDirectory)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load Active Directory for create cleanup")
}

func TestActiveDirectoryCreateHandler_Handle_SkipsNonCreatingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

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

func TestActiveDirectoryCreateHandler_Handle_MovesToError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	// A timed-out create must transition the AD from CREATING to ERROR (never to a healthy state), since
	// partial external resources may have been provisioned.
	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		State:         string(datamodel.JobsStatePROCESSING),
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateCreating,
	}
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
		return a.UUID == "ad-uuid" && a.State == datamodel.LifeCycleStateError &&
			a.StateDetails == datamodel.LifeCycleStateCreationErrorDetails
	})).Return(ad, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestActiveDirectoryCreateHandler_Handle_UpdateError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewActiveDirectoryCreateHandler()

	job := &datamodel.Job{
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "ad-uuid"},
	}

	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		State:     datamodel.LifeCycleStateCreating,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetActiveDirectoryByUuidAndAccountId(mock.Anything, "ad-uuid", testAccountID).Return(ad, nil).Once()
	storage.EXPECT().UpdateActiveDirectory(mock.Anything, mock.Anything).
		Return((*datamodel.ActiveDirectory)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transition Active Directory")
}

func TestNewActiveDirectoryCreateHandler(t *testing.T) {
	handler := NewActiveDirectoryCreateHandler()
	require.NotNil(t, handler)
	require.IsType(t, &ActiveDirectoryCreateHandler{}, handler)
}

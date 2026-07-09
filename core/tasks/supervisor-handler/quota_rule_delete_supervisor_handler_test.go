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

func TestQuotaRuleDeleteHandler_JobTypes(t *testing.T) {
	handler := NewQuotaRuleDeleteHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeDeleteQuotaRule)
}

func TestNewQuotaRuleDeleteHandler(t *testing.T) {
	handler := NewQuotaRuleDeleteHandler()
	require.NotNil(t, handler)
	require.IsType(t, &QuotaRuleDeleteHandler{}, handler)
}

func TestQuotaRuleDeleteHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetQuotaRuleByUUID", mock.Anything, mock.Anything, mock.Anything)
}

func TestQuotaRuleDeleteHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetQuotaRuleByUUID", mock.Anything, mock.Anything, mock.Anything)
}

func TestQuotaRuleDeleteHandler_Handle_QuotaRuleNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).
		Return((*datamodel.QuotaRule)(nil), vsaerrors.NewNotFoundErr("quota rule", nil)).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateQuotaRule", mock.Anything, mock.Anything)
}

func TestQuotaRuleDeleteHandler_Handle_GetQuotaRuleError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).
		Return((*datamodel.QuotaRule)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load quota rule for delete cleanup")
}

func TestQuotaRuleDeleteHandler_Handle_SkipsNonDeletingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	quotaRule := &datamodel.QuotaRule{
		BaseModel:    datamodel.BaseModel{UUID: "quota-rule-uuid"},
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateAvailableDetails,
	}
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "UpdateQuotaRule", mock.Anything, mock.Anything)
}

func TestQuotaRuleDeleteHandler_Handle_SuccessWithPreviousState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "quota-rule-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	quotaRule := &datamodel.QuotaRule{
		BaseModel:    datamodel.BaseModel{UUID: "quota-rule-uuid"},
		State:        datamodel.LifeCycleStateDeleting,
		StateDetails: datamodel.LifeCycleStateDeletingDetails,
	}
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
		return qr.UUID == "quota-rule-uuid" && qr.State == previousState && qr.StateDetails == previousStateDetails
	})).Return(quotaRule, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestQuotaRuleDeleteHandler_Handle_SuccessWithFallbackToReady(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	quotaRule := &datamodel.QuotaRule{
		BaseModel:    datamodel.BaseModel{UUID: "quota-rule-uuid"},
		State:        datamodel.LifeCycleStateDeleting,
		StateDetails: datamodel.LifeCycleStateDeletingDetails,
	}
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
		return qr.UUID == "quota-rule-uuid" && qr.State == datamodel.LifeCycleStateREADY && qr.StateDetails == datamodel.LifeCycleStateAvailableDetails
	})).Return(quotaRule, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestQuotaRuleDeleteHandler_Handle_UpdateQuotaRuleError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleDeleteHandler()

	previousState := datamodel.LifeCycleStateREADY
	previousStateDetails := datamodel.LifeCycleStateReadyDetails

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "quota-rule-uuid",
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
		AccountID: sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	quotaRule := &datamodel.QuotaRule{
		BaseModel:    datamodel.BaseModel{UUID: "quota-rule-uuid"},
		State:        datamodel.LifeCycleStateDeleting,
		StateDetails: datamodel.LifeCycleStateDeletingDetails,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.Anything).
		Return((*datamodel.QuotaRule)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert quota rule")
}

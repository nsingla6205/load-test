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

func TestQuotaRuleUpdateHandler_JobTypes(t *testing.T) {
	handler := NewQuotaRuleUpdateHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeUpdateQuotaRule)
}

func TestNewQuotaRuleUpdateHandler(t *testing.T) {
	handler := NewQuotaRuleUpdateHandler()
	require.NotNil(t, handler)
	require.IsType(t, &QuotaRuleUpdateHandler{}, handler)
}

func TestQuotaRuleUpdateHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetQuotaRuleByUUID", mock.Anything, mock.Anything, mock.Anything)
}

func TestQuotaRuleUpdateHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetQuotaRuleByUUID", mock.Anything, mock.Anything, mock.Anything)
}

func TestQuotaRuleUpdateHandler_Handle_QuotaRuleNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

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

func TestQuotaRuleUpdateHandler_Handle_GetQuotaRuleError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).
		Return((*datamodel.QuotaRule)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load quota rule for update cleanup")
}

func TestQuotaRuleUpdateHandler_Handle_SkipsNonUpdatingState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

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

func TestQuotaRuleUpdateHandler_Handle_SuccessWithPreviousState(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

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
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateUpdatingDetails,
	}
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
		return qr.UUID == "quota-rule-uuid" && qr.State == previousState && qr.StateDetails == previousStateDetails
	})).Return(quotaRule, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestQuotaRuleUpdateHandler_Handle_SuccessWithFallbackToReady(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
		AccountID:     sql.NullInt64{Int64: testAccountID, Valid: true},
	}

	quotaRule := &datamodel.QuotaRule{
		BaseModel:    datamodel.BaseModel{UUID: "quota-rule-uuid"},
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateUpdatingDetails,
	}
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
		return qr.UUID == "quota-rule-uuid" && qr.State == datamodel.LifeCycleStateREADY && qr.StateDetails == datamodel.LifeCycleStateAvailableDetails
	})).Return(quotaRule, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestQuotaRuleUpdateHandler_Handle_UpdateQuotaRuleError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleUpdateHandler()

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
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateUpdatingDetails,
	}
	expectedErr := errors.New("update failed")
	storage.EXPECT().GetQuotaRuleByUUID(mock.Anything, "quota-rule-uuid", testAccountID).Return(quotaRule, nil).Once()
	storage.EXPECT().UpdateQuotaRule(mock.Anything, mock.Anything).
		Return((*datamodel.QuotaRule)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert quota rule")
}

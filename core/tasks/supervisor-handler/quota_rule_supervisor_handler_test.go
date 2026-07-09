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

func TestQuotaRuleHandler_JobTypes(t *testing.T) {
	handler := NewQuotaRuleHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeCreateQuotaRule)
}

func TestNewQuotaRuleHandler(t *testing.T) {
	handler := NewQuotaRuleHandler()
	require.NotNil(t, handler)
	require.IsType(t, &QuotaRuleHandler{}, handler)
}

func TestQuotaRuleHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
	}

	err := handler.Handle(context.Background(), job, Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "DeleteQuotaRule", mock.Anything, mock.Anything)
}

func TestQuotaRuleHandler_Handle_SkipsMissingResourceUUID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "DeleteQuotaRule", mock.Anything, mock.Anything)
}

func TestQuotaRuleHandler_Handle_QuotaRuleNotFound(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
	}

	storage.EXPECT().DeleteQuotaRule(mock.Anything, "quota-rule-uuid").
		Return((*datamodel.QuotaRule)(nil), vsaerrors.NewNotFoundErr("quota rule", nil)).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

func TestQuotaRuleHandler_Handle_DeleteQuotaRuleError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleHandler()

	job := &datamodel.Job{
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
	}

	expectedErr := errors.New("database error")
	storage.EXPECT().DeleteQuotaRule(mock.Anything, "quota-rule-uuid").
		Return((*datamodel.QuotaRule)(nil), expectedErr).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "delete quota rule from VCP")
}

func TestQuotaRuleHandler_Handle_Success(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewQuotaRuleHandler()

	job := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
		JobAttributes: &datamodel.JobAttributes{ResourceUUID: "quota-rule-uuid"},
	}

	storage.EXPECT().DeleteQuotaRule(mock.Anything, "quota-rule-uuid").
		Return(&datamodel.QuotaRule{BaseModel: datamodel.BaseModel{UUID: "quota-rule-uuid"}}, nil).Once()

	err := handler.Handle(context.Background(), job, EventTimeout, storage)
	require.NoError(t, err)
}

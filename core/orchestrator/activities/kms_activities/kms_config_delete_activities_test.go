package kms_activities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
)

func TestDeleteSDEKmsConfig_Success(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{Name: "test-pool"}

	params := &common.DeleteKmsConfigParams{}
	sdeDeleteSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.DeleteKmsConfigParams) (gcpgenserver.V1betaDeleteKmsConfigurationRes, error) {
		return nil, nil
	}
	defer func() { sdeDeleteSDEKmsConfiguration = sde.DeleteSDEKmsConfiguration }()

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSDEKmsConfig)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteSDEKmsConfig, kms, params)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSDEKmsConfig_Error(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{Name: "test-pool"}

	params := &common.DeleteKmsConfigParams{}
	sdeDeleteSDEKmsConfiguration = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.DeleteKmsConfigParams) (gcpgenserver.V1betaDeleteKmsConfigurationRes, error) {
		return nil, errors.New("some error")
	}
	defer func() { sdeDeleteSDEKmsConfiguration = sde.DeleteSDEKmsConfiguration }()
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSDEKmsConfig)
	// Act
	_, err := env.ExecuteActivity(activity.DeleteSDEKmsConfig, kms, params)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteKmsConfig_Success(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test-kms", KeyName: "key1"}

	params := &common.DeleteKmsConfigParams{
		KmsConfigID: "uuid",
	}
	mockStorage.On("DeleteKmsConfig", mock.Anything, "uuid", datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateDeletedDetails).Return(kms, nil)

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteKmsConfig)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteKmsConfig, kms, params)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteKmsConfig_Error(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test-kms", KeyName: "key1"}

	params := &common.DeleteKmsConfigParams{
		KmsConfigID: "uuid",
	}
	mockStorage.On("DeleteKmsConfig", mock.Anything, "uuid", datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateDeletedDetails).Return(nil, errors.New("some error"))

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteKmsConfig)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteKmsConfig, kms, params)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteKmsConfig_NotFoundReturnsSuccess(t *testing.T) {
	// Arrange - Test idempotent delete behavior when record is already deleted
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test-kms", KeyName: "key1"}

	params := &common.DeleteKmsConfigParams{
		KmsConfigID: "uuid",
	}
	// Return NotFoundErr to simulate already deleted record
	mockStorage.On("DeleteKmsConfig", mock.Anything, "uuid", datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateDeletedDetails).Return(nil, errors.NewNotFoundErr("KMS Configuration", nil))

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteKmsConfig)

	// Act
	_, err := env.ExecuteActivity(activity.DeleteKmsConfig, kms, params)

	// Assert - Should succeed (idempotent)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteKmsConfigState_Success(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test-kms", KeyName: "key1"}

	mockStorage.On("UpdateKmsConfigState", mock.Anything, "uuid", datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(kms, nil)

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UpdateKmsConfigState)

	// Act
	_, err := env.ExecuteActivity(activity.UpdateKmsConfigState, kms, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)

	// Assert
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteKmsConfigState_Error(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	kms := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, Name: "test-kms", KeyName: "key1"}

	mockStorage.On("UpdateKmsConfigState", mock.Anything, "uuid", datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(nil, errors.New("some error"))

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UpdateKmsConfigState)

	// Act
	_, err := env.ExecuteActivity(activity.UpdateKmsConfigState, kms, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)

	// Assert
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestUpdateServiceAccountAsDisabled_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	accountID := int64(100)
	saID := int64(10)
	kms := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid"},
		AccountID: accountID,
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   saID,
				UUID: "sa-uuid",
			},
		},
	}

	mockStorage.On("ListKmsConfigByAccountID", mock.Anything, accountID).Return([]*datamodel.KmsConfig{kms}, nil)
	mockStorage.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(kms.ServiceAccount, nil)

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DisableKmsServiceAccount)

	_, err := env.ExecuteActivity(activity.DisableKmsServiceAccount, kms)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestUpdateServiceAccountAsDisabled_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	accountID := int64(100)
	saID := int64(10)
	kms := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid"},
		AccountID: accountID,
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   saID,
				UUID: "sa-uuid",
			},
		},
	}

	mockStorage.On("ListKmsConfigByAccountID", mock.Anything, accountID).Return([]*datamodel.KmsConfig{kms}, nil)
	mockStorage.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(nil, errors.New("update error"))

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DisableKmsServiceAccount)

	_, err := env.ExecuteActivity(activity.DisableKmsServiceAccount, kms)
	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDisableKmsServiceAccount_SkipsWhenSiblingSharesServiceAccount(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	accountID := int64(100)
	saID := int64(10)
	kms := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-delete"},
		AccountID: accountID,
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   saID,
				UUID: "sa-uuid",
			},
		},
	}
	sibling := &datamodel.KmsConfig{
		BaseModel:        datamodel.BaseModel{UUID: "kms-uuid-sibling"},
		AccountID:        accountID,
		ServiceAccountID: &saID,
	}

	mockStorage.On("ListKmsConfigByAccountID", mock.Anything, accountID).Return([]*datamodel.KmsConfig{kms, sibling}, nil)

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DisableKmsServiceAccount)

	_, err := env.ExecuteActivity(activity.DisableKmsServiceAccount, kms)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDisableKmsServiceAccount_DoesNotSkipWhenSiblingIsDeleting(t *testing.T) {
	// A sibling that is itself being deleted must not block disabling the SA,
	// otherwise concurrent deletes of configs sharing the SA would both skip
	// and leak the service account. Deleting siblings are ignored.
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	accountID := int64(100)
	saID := int64(10)
	kms := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-delete"},
		AccountID: accountID,
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   saID,
				UUID: "sa-uuid",
			},
		},
	}
	deletingSibling := &datamodel.KmsConfig{
		BaseModel:        datamodel.BaseModel{UUID: "kms-uuid-sibling"},
		AccountID:        accountID,
		ServiceAccountID: &saID,
		State:            datamodel.LifeCycleStateDeleting,
	}

	mockStorage.On("ListKmsConfigByAccountID", mock.Anything, accountID).Return([]*datamodel.KmsConfig{kms, deletingSibling}, nil)
	mockStorage.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateDisabled, datamodel.LifeCycleStateDisabledDetails).Return(kms.ServiceAccount, nil)

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DisableKmsServiceAccount)

	_, err := env.ExecuteActivity(activity.DisableKmsServiceAccount, kms)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSDEDeleteJob_NilJobUuid(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	params := &common.DeleteKmsConfigParams{
		Region:         "us-central1",
		AccountName:    "test-account",
		XCorrelationID: "corr-id",
	}

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DescribeSDEDeleteJob)

	_, err := env.ExecuteActivity(activity.DescribeSDEDeleteJob, nil, params)
	assert.NoError(t, err)
}

func TestDeleteSDEDeleteJob_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	params := &common.DeleteKmsConfigParams{
		Region:         "us-central1",
		AccountName:    "test-account",
		XCorrelationID: "corr-id",
	}
	jobUuid := "job-uuid"
	called := false
	describeSDEJob = func(ctx context.Context, jobUuid, region, accountName, xCorrelationID string) error {
		called = true
		assert.Equal(t, "job-uuid", jobUuid)
		assert.Equal(t, "us-central1", region)
		assert.Equal(t, "test-account", accountName)
		assert.Equal(t, "corr-id", xCorrelationID)
		return nil
	}
	defer func() { describeSDEJob = sde.DescribeSDEJob }()

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DescribeSDEDeleteJob)

	_, err := env.ExecuteActivity(activity.DescribeSDEDeleteJob, &jobUuid, params)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestDeleteSDEDeleteJob_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	params := &common.DeleteKmsConfigParams{
		Region:         "us-central1",
		AccountName:    "test-account",
		XCorrelationID: "corr-id",
	}
	jobUuid := "job-uuid"
	describeSDEJob = func(ctx context.Context, jobUuid, region, accountName, xCorrelationID string) error {
		return errors.New("describe error")
	}
	defer func() { describeSDEJob = sde.DescribeSDEJob }()

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DescribeSDEDeleteJob)

	_, err := env.ExecuteActivity(activity.DescribeSDEDeleteJob, &jobUuid, params)
	assert.Error(t, err)
}

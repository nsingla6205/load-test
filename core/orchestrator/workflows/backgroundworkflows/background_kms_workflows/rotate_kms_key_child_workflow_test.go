package background_kms_workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/testsuite"
)

type RotateKmsKeyChildWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *RotateKmsKeyChildWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)
}

func (s *RotateKmsKeyChildWorkflowTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

const testLockClientID = "test-lock-client-id"

// registerLockActivities registers AcquireKmsRotationLockActivity, RenewKmsRotationLockActivity, and ReleaseKmsRotationLockActivity with mocks so the workflow can run without a real K8s cluster.
func (s *RotateKmsKeyChildWorkflowTestSuite) registerLockActivities(activity *backgroundactivities.RotateKmsSAKeyActivity, lockClientID string) {
	s.env.RegisterActivity(activity.AcquireKmsRotationLockActivity)
	s.env.RegisterActivity(activity.RenewKmsRotationLockActivity)
	s.env.RegisterActivity(activity.ReleaseKmsRotationLockActivity)
	s.env.OnActivity(activity.AcquireKmsRotationLockActivity, mock.Anything, mock.Anything).Return(lockClientID, nil)
	s.env.OnActivity(activity.RenewKmsRotationLockActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(activity.ReleaseKmsRotationLockActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

// setupDecryptPasswordMock mocks utils.DecryptPassword to return a valid decrypted value
// This is needed because the workflow calls DecryptPassword and dereferences the result
func (s *RotateKmsKeyChildWorkflowTestSuite) setupDecryptPasswordMock() func() {
	originalDecryptPassword := utils.DecryptPassword
	decryptedValue := "decrypted-key-data"
	utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
		return &decryptedValue, nil
	}
	return func() {
		utils.DecryptPassword = originalDecryptPassword
	}
}

func TestRotateKmsKeyChildWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(RotateKmsKeyChildWorkflowTestSuite))
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_Success() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	lockClientID := testLockClientID

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountEmail:            "sa@project.iam.gserviceaccount.com",
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
		State: string(gcpserver.KmsConfigV1betaKmsStateINUSE),
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required - all validations passed",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"},
			Name:      "pool-2",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	migrationResult1 := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-1",
		Success: true,
	}

	migrationResult2 := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-2",
		Success: true,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, lockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(activity.CompleteKeyRotationActivity)
	s.env.RegisterActivity(activity.DeleteOldSAKeyFromGCPActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult1, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-2", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult2, nil)
	s.env.OnActivity(activity.CompleteKeyRotationActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "new-key-id-456", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.DeleteOldSAKeyFromGCPActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "old-key-id-123").Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_RotationNotRequired() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:           datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountEmail: "sa@project.iam.gserviceaccount.com",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
		State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: false,
		CurrentKeyID:     "",
		Reason:           "Key rotation not needed - key is recent",
		ServiceAccount:   serviceAccount,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)

	// Mock activity call
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_ValidationFails() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid-1"},
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)

	// Mock activity call to return error
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(nil, errors.New("database error"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "database error")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_CreateKeyFails() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid-1"},
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(nil, errors.New("GCP API error"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "GCP API error")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_AcquireLockFails() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid-1"},
	}
	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
	}
	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.env.RegisterActivity(activity.AcquireKmsRotationLockActivity)
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.AcquireKmsRotationLockActivity, mock.Anything, serviceAccount.UUID).Return("", errors.New("lock acquire failed"))

	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "lock acquire failed")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_StoreKeyFails() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid-1"},
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(errors.New("database insert error"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "database insert error")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_BatchPoolsFails() {
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid-1"},
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(nil, errors.New("failed to list pools"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to list pools")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_MigrationFails() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	migrationResult := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-1",
		Success: false,
		Error:   "ONTAP update failed",
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(backgroundactivities.EmitKmsRotationFailureMetric)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult, nil)
	s.env.OnActivity(backgroundactivities.EmitKmsRotationFailureMetric, mock.Anything, kmsConfig.UUID, mock.Anything, failureTypePoolMigration).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert - workflow should complete successfully but rotation should not be completed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	// CompleteKeyRotationActivity should not be called when migration fails
	s.env.AssertNotCalled(s.T(), "CompleteKeyRotationActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_MigrationActivityFails() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(backgroundactivities.EmitKmsRotationFailureMetric)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(nil, errors.New("activity execution failed"))
	s.env.OnActivity(backgroundactivities.EmitKmsRotationFailureMetric, mock.Anything, kmsConfig.UUID, mock.Anything, failureTypePoolMigration).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert - workflow should complete successfully but rotation should not be completed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	// CompleteKeyRotationActivity should not be called when migration activity fails
	s.env.AssertNotCalled(s.T(), "CompleteKeyRotationActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_CompleteRotationFails() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	migrationResult := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-1",
		Success: true,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(activity.CompleteKeyRotationActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult, nil)
	s.env.OnActivity(activity.CompleteKeyRotationActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "new-key-id-456", "old-key-id-123").Return(errors.New("failed to complete rotation"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to complete rotation")
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_DeleteOldKeyFailsNonFatal() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	migrationResult := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-1",
		Success: true,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(activity.CompleteKeyRotationActivity)
	s.env.RegisterActivity(activity.DeleteOldSAKeyFromGCPActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult, nil)
	s.env.OnActivity(activity.CompleteKeyRotationActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "new-key-id-456", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.DeleteOldSAKeyFromGCPActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "old-key-id-123").Return(errors.New("GCP delete failed"))

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert - workflow should complete successfully even if delete fails (non-fatal)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_KeyAlreadyExists() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	// Key already exists in keys array
	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "",
		KeyExists:  true, // Key already exists
	}

	pools := []*datamodel.Pool{
		{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "pool-1",
			State:     string(gcpserver.PoolV1betaStoragePoolStateREADY),
		},
	}

	migrationResult := &backgroundactivities.SvmMigrationResult{
		SvmUUID: "svm-uuid-1",
		Success: true,
	}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.MigratePoolToNewKeyActivity)
	s.env.RegisterActivity(activity.CompleteKeyRotationActivity)
	s.env.RegisterActivity(activity.DeleteOldSAKeyFromGCPActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.MigratePoolToNewKeyActivity, mock.Anything, "pool-uuid-1", mock.Anything, mock.Anything, "new-key-id-456").Return(migrationResult, nil)
	s.env.OnActivity(activity.CompleteKeyRotationActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "new-key-id-456", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.DeleteOldSAKeyFromGCPActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "old-key-id-123").Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyChildWorkflowTestSuite) TestRotateKmsKeyChildWorkflow_NoPools() {
	defer s.setupDecryptPasswordMock()()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid-1"},
		ServiceAccountPasswordLocation: "encrypted-old-key",
	}

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{
			UUID: "kms-uuid-1",
			ID:   123,
		},
	}

	validationResult := &backgroundactivities.ValidateKeyRotationRequiredResult{
		RotationRequired: true,
		CurrentKeyID:     "old-key-id-123",
		Reason:           "Rotation required",
		ServiceAccount:   serviceAccount,
	}

	createKeyResult := &backgroundactivities.CreateServiceAccountKeyResult{
		NewKeyID:   "new-key-id-456",
		NewKeyData: "encrypted-new-key-data",
		GcpKeyName: "projects/test-project/serviceAccounts/sa@project.iam.gserviceaccount.com/keys/new-key-id-456",
		KeyExists:  false,
	}

	// No pools
	pools := []*datamodel.Pool{}

	// Register activities
	s.env.RegisterActivity(activity.ValidateKeyRotationRequiredActivity)
	s.registerLockActivities(activity, testLockClientID)
	s.env.RegisterActivity(activity.CreateServiceAccountKeyActivity)
	s.env.RegisterActivity(activity.StoreNewKeyInDBActivity)
	s.env.RegisterActivity(activity.BatchPoolsForServiceAccountActivity)
	s.env.RegisterActivity(activity.CompleteKeyRotationActivity)
	s.env.RegisterActivity(activity.DeleteOldSAKeyFromGCPActivity)

	// Mock activity calls
	s.env.OnActivity(activity.ValidateKeyRotationRequiredActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID).Return(validationResult, nil)
	s.env.OnActivity(activity.CreateServiceAccountKeyActivity, mock.Anything, serviceAccount.UUID, kmsConfig, "old-key-id-123").Return(createKeyResult, nil)
	s.env.OnActivity(activity.StoreNewKeyInDBActivity, mock.Anything, serviceAccount.UUID, "new-key-id-456", "encrypted-new-key-data", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.BatchPoolsForServiceAccountActivity, mock.Anything, serviceAccount.UUID).Return(pools, nil)
	s.env.OnActivity(activity.CompleteKeyRotationActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "new-key-id-456", "old-key-id-123").Return(nil)
	s.env.OnActivity(activity.DeleteOldSAKeyFromGCPActivity, mock.Anything, serviceAccount.UUID, kmsConfig.UUID, "old-key-id-123").Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RotateKmsKeyChildWorkflow, serviceAccount, kmsConfig)

	// Assert
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	// Migration activity should not be called when there are no pools
	s.env.AssertNotCalled(s.T(), "MigratePoolToNewKeyActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

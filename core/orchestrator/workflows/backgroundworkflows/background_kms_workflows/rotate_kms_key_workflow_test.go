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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
)

type RotateKmsKeyTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *RotateKmsKeyTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterWorkflow(RotateKmsSAKeyWorkflow)
}

func (s *RotateKmsKeyTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func TestRotateKmsKeyTestSuite(t *testing.T) {
	suite.Run(t, new(RotateKmsKeyTestSuite))
}

func inUseKmsConfig(uuid, saUUID, saEmail string) *datamodel.KmsConfig {
	return &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: uuid},
		State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: saUUID},
			ServiceAccountEmail: saEmail,
		},
	}
}

func (s *RotateKmsKeyTestSuite) enableRotation() func() {
	original := kmsRotationEnabled
	kmsRotationEnabled = true
	return func() {
		kmsRotationEnabled = original
	}
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_Success() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-1", "sa-uuid-1", "sa1@project.iam.gserviceaccount.com"),
		inUseKmsConfig("kms-2", "sa-uuid-2", "sa2@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_PoolCreatingState() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-1", "sa-uuid-1", "sa1@project.iam.gserviceaccount.com"),
		inUseKmsConfig("kms-2", "sa-uuid-2", "sa2@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)

	errPoolCreating := utilserrors.NewConflictErr(utils.StoragePoolCreatingStateError + ": test-pool")
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(errPoolCreating)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_ListKmsConfigsFails() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(nil, errors.New("db error"))

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "db error")
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_ChildWorkflowFails() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-1", "sa-uuid-1", "sa1@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("child workflow failed"))

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "key rotation failed for one or more service accounts")
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_RotationDisabled() {
	defer func() {
		kmsRotationEnabled = env.GetBool("GCP_KMS_KEY_ROTATION_ENABLED", false)
	}()
	kmsRotationEnabled = false

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	s.env.RegisterActivity(activity.ListKmsConfigs)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "ListKmsConfigs")
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_RotationEnabled() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-enabled-1", "sa-uuid-enabled", "sa-enabled@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertCalled(s.T(), "ListKmsConfigs", mock.Anything)
	s.env.AssertCalled(s.T(), "RotateKmsKeyChildWorkflow", mock.Anything, mock.Anything, mock.Anything)
}

// TestRotateKmsSAKeyWorkflow_CompletesWithSingleInUseConfig verifies the workflow runs to
// completion when there is a single IN_USE KMS config, dispatching one child workflow. It does
// not assert on ActivityOptions/HeartbeatTimeout, which is exercised through the child workflow.
func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_CompletesWithSingleInUseConfig() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-1", "sa-uuid-1", "sa1@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_GroupsByServiceAccount() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		inUseKmsConfig("kms-1", "sa-uuid-shared", "shared@project.iam.gserviceaccount.com"),
		inUseKmsConfig("kms-2", "sa-uuid-shared", "shared@project.iam.gserviceaccount.com"),
		inUseKmsConfig("kms-3", "sa-uuid-other", "other@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(2)

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_SkipsNonInUseKmsConfig() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		{
			BaseModel: datamodel.BaseModel{UUID: "kms-ready"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateREADY),
			ServiceAccount: &datamodel.ServiceAccount{
				BaseModel:           datamodel.BaseModel{UUID: "sa-uuid-ready"},
				ServiceAccountEmail: "ready@project.iam.gserviceaccount.com",
			},
		},
		inUseKmsConfig("kms-in-use", "sa-uuid-in-use", "inuse@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_SkipsNilServiceAccount() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	kmsConfigs := []*datamodel.KmsConfig{
		{
			BaseModel:      datamodel.BaseModel{UUID: "kms-no-sa"},
			State:          string(gcpserver.KmsConfigV1betaKmsStateINUSE),
			ServiceAccount: nil,
		},
		inUseKmsConfig("kms-with-sa", "sa-uuid-1", "sa1@project.iam.gserviceaccount.com"),
	}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(RotateKmsKeyChildWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *RotateKmsKeyTestSuite) TestRotateKmsSAKeyWorkflow_PrefersInUseConfigWhenGrouping() {
	defer s.enableRotation()()

	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	readyConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-ready-first"},
		State:     string(gcpserver.KmsConfigV1betaKmsStateREADY),
		ServiceAccount: &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid-shared"},
			ServiceAccountEmail: "shared@project.iam.gserviceaccount.com",
		},
	}
	inUseConfig := inUseKmsConfig("kms-in-use-second", "sa-uuid-shared", "shared@project.iam.gserviceaccount.com")
	kmsConfigs := []*datamodel.KmsConfig{readyConfig, inUseConfig}

	s.env.RegisterActivity(activity.ListKmsConfigs)
	s.env.RegisterWorkflow(RotateKmsKeyChildWorkflow)

	s.env.OnActivity(activity.ListKmsConfigs, mock.Anything).Return(kmsConfigs, nil)
	s.env.OnWorkflow(
		RotateKmsKeyChildWorkflow,
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(config *datamodel.KmsConfig) bool {
			return config != nil && config.UUID == "kms-in-use-second"
		}),
	).Return(nil).Once()

	s.env.ExecuteWorkflow(RotateKmsSAKeyWorkflow)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func TestIsInUseKmsConfig(t *testing.T) {
	assert.False(t, isInUseKmsConfig(nil))
	assert.False(t, isInUseKmsConfig(&datamodel.KmsConfig{
		State: string(gcpserver.KmsConfigV1betaKmsStateREADY),
	}))
	assert.True(t, isInUseKmsConfig(&datamodel.KmsConfig{
		State: string(gcpserver.KmsConfigV1betaKmsStateINUSE),
	}))
}

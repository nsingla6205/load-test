package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type VolumeRevertUnitTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *VolumeRevertUnitTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)

	// Register workflow
	s.env.RegisterWorkflow(RevertVolumeWorkflow)
}

func (s *VolumeRevertUnitTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_GetNodeError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get node"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_RevertVolumeError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.VolumeResponse{}, errors.New("failed to revert volume"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_UpdateJobStatusProcessingError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil)

	// Set up mock expectations - fail on UpdateJobStatus
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update job status"))

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_UpdateJobStatusDoneError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update job status")).Once()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.VolumeResponse{}, nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_UpdateJobStatusErrorDetailsError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update job status")).Once()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.VolumeResponse{}, errors.New("revert volume failed"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_SetupError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	// Create a volume with nil Account to cause setup error
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		// Account is nil to cause setup error
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_WithCertificateAuth() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "",
				SecretID:      "",
				CertificateID: "cert-id",
				AuthType:      env.USER_CERTIFICATE,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_WithSecretManagerAuth() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "",
				SecretID:      "secret-id",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD_SEC_MGR,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_UpdateVolumeStateInDBError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob for CheckJobStateBeforeProcessing
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.VolumeResponse{}, errors.New("revert volume failed"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume state"))

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_FailsOnJobInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)

	// Mock GetJob to return job in ERROR state
	jobInErrorState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateERROR),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInErrorState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInErrorState, nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "job default-test-workflow-id is in state ERROR; expected NEW")
}

func (s *VolumeRevertUnitTestSuite) Test_RevertVolumeWorkflow_SucceedsWhenJobNotInErrorState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeRevertActivity := activities.VolumeRevertActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	params := &common.RevertVolumeParams{
		AccountName: "test-account",
		Region:      "us-central1",
		VolumeID:    "volume-uuid",
		SnapshotID:  "snapshot-uuid",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
				AuthType:      env.USERNAME_PWD,
			},
			DeploymentName: "test-deployment",
		},
		Account: &datamodel.Account{
			Name: "test-account",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	// Register activities
	s.env.RegisterActivity(&commonActivity)
	s.env.RegisterActivity(&volumeRevertActivity)
	s.env.RegisterActivity(&volumeCreateActivity)

	// Mock GetJob to return job in NEW state (not ERROR)
	jobInNewState := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()
	// Also mock the storage call as fallback
	mockStorage.On("GetJob", mock.Anything, "default-test-workflow-id").Return(jobInNewState, nil).Maybe()

	// Set up mock expectations
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{
		{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name:            "test-node",
			EndpointAddress: "127.0.0.1",
		},
	}, nil)
	s.env.OnActivity(volumeRevertActivity.RevertVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RevertVolumeWorkflow, params, volume, snapshot)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func TestVolumeRevertUnitTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeRevertUnitTestSuite))
}

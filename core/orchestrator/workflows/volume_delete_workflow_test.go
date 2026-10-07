package workflows

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	common "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type VolumeDeleteTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *VolumeDeleteTestSuite) SetupTest() {
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
	s.env.RegisterWorkflow(DeleteVolumeWorkflow)

	// Register all activities that might be used across tests
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	// Skip default DeleteCifsShareIfSMB mock when test will override it (e.g. DeleteCifsShareIfSMBError)
	if !strings.Contains(s.T().Name(), "DeleteCifsShareIfSMBError") {
		s.env.RegisterActivity(deleteActivity.DeleteCifsShareIfSMB)
		s.env.OnActivity(deleteActivity.DeleteCifsShareIfSMB, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	}

	// Default allow EnforceDeleteProtection; SMB denial test registers the real activity.
	if !strings.Contains(s.T().Name(), "EnforceDeleteProtectionSMBDenied") {
		s.env.RegisterActivity(deleteActivity.EnforceDeleteProtection)
		s.env.OnActivity(deleteActivity.EnforceDeleteProtection, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	}

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
		JobAttributes: &datamodel.JobAttributes{
			PreviousState:        datamodel.LifeCycleStateREADY,
			PreviousStateDetails: "ready-details",
		},
	}, nil).Maybe()
}

func (s *VolumeDeleteTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.GetVolumeByVolumeID)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.OnActivity(volumeCreateActivity.GetVolumeByVolumeID, mock.Anything, volume.UUID).Return(volume, nil)
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume.UUID)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_ResolutionFailureMarksJobError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity.GetVolumeByVolumeID)
	s.env.OnActivity(volumeCreateActivity.GetVolumeByVolumeID, mock.Anything, "missing-volume").Return(nil, errors.New("volume lookup failed"))
	mockStorage.On("UpdateJob", mock.Anything, "default-test-workflow-id", string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, "missing-volume")

	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "volume lookup failed")
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 1)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_CleanupAutoGeneratedVPG() {
	originalEnableMqos, hasEnableMqos := os.LookupEnv("ENABLE_MQOS")
	_ = os.Setenv("ENABLE_MQOS", "true")
	defer func() {
		if hasEnableMqos {
			_ = os.Setenv("ENABLE_MQOS", originalEnableMqos)
		} else {
			_ = os.Unsetenv("ENABLE_MQOS")
		}
	}()

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.CleanupAutoGeneratedVPG)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.CleanupAutoGeneratedVPG, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
		VolumePerformanceGroupID: sql.NullInt64{Int64: 99, Valid: true},
	}

	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_WhenJobInErrorState() {
	s.env = s.NewTestWorkflowEnvironment()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateERROR),
	}, nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "job default-test-workflow-id is in state ERROR; expected NEW")
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SuccessWithBP() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteIgroupsFromBlockProperties)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteIgroupsFromBlockProperties, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{
						HostGroupUUID: "test-hostgroup-uuid",
						HostQNs:       []string{"test-hostgroup-uuid"},
					},
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateJobFailsAfterWorkflowExecution() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	var jobStatusUpdates []string

	mockStorage.EXPECT().UpdateJob(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, status string, _ int, _ string, _ ...database.UpdateJobOpts) error {
			jobStatusUpdates = append(jobStatusUpdates, status)
			if status == string(datamodel.JobsStateERROR) {
				return errors.New("failed updating job")
			}
			return nil
		})

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(errors.New("failed to update volume details"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		PoolID:    1,
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
	assert.Contains(s.T(), jobStatusUpdates, string(datamodel.JobsStateERROR))
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_FirstUpdateJobFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed updating job"))

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(errors.New("failed to update volume details"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 10)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteVolumeError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "PROCESSING", mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "ERROR", mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(fmt.Errorf("failed to delete volume"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteVolumeInONTAPError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "PROCESSING", mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, "ERROR", mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("failed to delete volume in ONTAP"))
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteCifsShareIfSMBError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteCifsShareIfSMB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsShareIfSMB, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("CIFS share delete failed"))
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateVolumeStateInDBError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("failed to delete volume in ONTAP"))
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("failed to update volume state in DB"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteSnapshotPolicyInONTAPFailure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	var jobStatusUpdates []string

	mockStorage.EXPECT().UpdateJob(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, status string, _ int, _ string, _ ...database.UpdateJobOpts) error {
			jobStatusUpdates = append(jobStatusUpdates, status)
			if status == string(datamodel.JobsStateERROR) {
				return errors.New("failed updating job")
			}
			return nil
		})

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete snapshot policy"))
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		PoolID:    1,
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
		SnapshotPolicy: &datamodel.SnapshotPolicy{
			Name:      "policy1",
			IsEnabled: true,
			Schedules: []*datamodel.SnapshotPolicySchedule{
				{
					DaysOfMonth:     []int{1, 15},
					DaysOfWeek:      []int{2},
					Hours:           []int{3},
					Minutes:         []int{0},
					SnapmirrorLabel: "label1",
					Count:           5,
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "workflow execution error")
	assert.Contains(s.T(), jobStatusUpdates, string(datamodel.JobsStateERROR))
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_WithExportPolicy() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteExportPolicy)
	s.env.RegisterActivity(deleteActivity.DeleteAssociatedQuotaRules)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteAssociatedQuotaRules, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with a volume that has export policy rules
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
					ExportRules: []*datamodel.ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							Index:          1,
							UnixReadOnly:   true,
						},
					},
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that DeleteExportPolicy was called
	s.env.AssertNumberOfCalls(s.T(), "DeleteExportPolicy", 1)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_WithQuotaRules() {
	// Save original value and enable flag
	originalEnableQuotaRule := enableQuotaRule
	defer func() { enableQuotaRule = originalEnableQuotaRule }()
	enableQuotaRule = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(activities.CommonActivities.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteAssociatedQuotaRules)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteAssociatedQuotaRules, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with a volume that has FileProperties (file volume)
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "test-external-uuid",
			FileProperties: &datamodel.FileProperties{},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that DeleteAssociatedQuotaRules was called
	s.env.AssertNumberOfCalls(s.T(), "DeleteAssociatedQuotaRules", 1)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_WithoutQuotaRules_BlockVolume() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(activities.CommonActivities.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteAssociatedQuotaRules)
	s.env.RegisterActivity(deleteActivity.DeleteIgroups)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteIgroups, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with a block volume (no FileProperties)
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			BlockDevices: &[]datamodel.BlockDevice{
				{
					HostGroupDetails: []datamodel.HostGroupDetail{
						{HostGroupUUID: "hg-uuid-1"},
					},
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that DeleteAssociatedQuotaRules was NOT called for block volume
	s.env.AssertNumberOfCalls(s.T(), "DeleteAssociatedQuotaRules", 0)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_QuotaRulesError() {
	// Save original value and enable flag
	originalEnableQuotaRule := enableQuotaRule
	defer func() { enableQuotaRule = originalEnableQuotaRule }()
	enableQuotaRule = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(activities.CommonActivities.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteAssociatedQuotaRules)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	quotaRuleError := errors.New("quota rule deletion error")
	s.env.OnActivity(deleteActivity.DeleteAssociatedQuotaRules, mock.Anything, mock.Anything).Return(vsaerrors.WrapAsTemporalApplicationError(quotaRuleError))
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with a volume that has FileProperties
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "test-external-uuid",
			FileProperties: &datamodel.FileProperties{},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that DeleteAssociatedQuotaRules was called (called 3 times due to retries)
	s.env.AssertNumberOfCalls(s.T(), "DeleteAssociatedQuotaRules", 3)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_QuotaRuleFeatureFlagDisabled() {
	// Save original value and ensure flag is disabled
	originalEnableQuotaRule := enableQuotaRule
	defer func() { enableQuotaRule = originalEnableQuotaRule }()
	enableQuotaRule = false

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(activities.CommonActivities.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteAssociatedQuotaRules)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)

	// Mock activities - DeleteAssociatedQuotaRules should NOT be called
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow with a volume that has FileProperties (file volume) but flag is disabled
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: int64(1)},
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "test-external-uuid",
			FileProperties: &datamodel.FileProperties{},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify that DeleteAssociatedQuotaRules was NOT called when flag is disabled
	s.env.AssertNotCalled(s.T(), "DeleteAssociatedQuotaRules", mock.Anything, mock.Anything)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_ExportPolicyError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteExportPolicy)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(activities.CommonActivities.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	deletionError := errors.New("export policy deletion error")
	s.env.OnActivity(deleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(vsaerrors.WrapAsTemporalApplicationError(deletionError))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with a volume that has export policy rules
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		Svm: &datamodel.Svm{
			Name: "test_svm",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "test-export-policy",
					ExportRules: []*datamodel.ExportRule{
						{
							AllowedClients: "0.0.0.0/0",
							Index:          1,
							UnixReadOnly:   true,
						},
					},
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// With retries, DeleteExportPolicy will be called 3 times in failure cases
	// We're just asserting that it was called, not the exact number of times
	s.env.AssertCalled(s.T(), "DeleteExportPolicy", mock.Anything, mock.Anything, mock.Anything)
}

func TestVolumeDeleteTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeDeleteTestSuite))
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SmbTeardownFeatureFlagDisabled() {
	// Save original value and ensure flag is disabled
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = false

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities - SMB teardown activities should NOT be called
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify SMB teardown activities were NOT called
	s.env.AssertNotCalled(s.T(), "DetermineSmbTeardownContext", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "DeleteCifsServerIfUnused", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "DeleteDnsRecordIfUnused", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "GetSVM", mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "UnsetSvmActiveDirectory", mock.Anything, mock.Anything)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SmbTeardownFeatureFlagEnabled_ShouldDeleteTrue() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetSVM)
	s.env.RegisterActivity(commonActivity.UnsetSvmActiveDirectory)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Create test SVM
	testSvm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
		PoolID:    1,
	}

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: true,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetSVM, mock.Anything, int64(1)).Return(testSvm, nil)
	s.env.OnActivity(commonActivity.UnsetSvmActiveDirectory, mock.Anything, testSvm).Return(testSvm, nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify SMB teardown activities were called
	s.env.AssertCalled(s.T(), "DetermineSmbTeardownContext", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "DeleteCifsServerIfUnused", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "DeleteDnsRecordIfUnused", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "GetSVM", mock.Anything, int64(1))
	s.env.AssertCalled(s.T(), "UnsetSvmActiveDirectory", mock.Anything, testSvm)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SmbTeardownFeatureFlagEnabled_ShouldDeleteFalse() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetSVM)
	s.env.RegisterActivity(commonActivity.UnsetSvmActiveDirectory)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: false,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify SMB teardown activities were called
	s.env.AssertCalled(s.T(), "DetermineSmbTeardownContext", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "DeleteCifsServerIfUnused", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "DeleteDnsRecordIfUnused", mock.Anything, mock.Anything, mock.Anything)
	// But GetSVM and UnsetSvmActiveDirectory should NOT be called when ShouldDelete is false
	s.env.AssertNotCalled(s.T(), "GetSVM", mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "UnsetSvmActiveDirectory", mock.Anything, mock.Anything)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SmbTeardownFeatureFlagEnabled_GetSvmFails() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetSVM)
	s.env.RegisterActivity(commonActivity.UnsetSvmActiveDirectory)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: true,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetSVM, mock.Anything, int64(1)).Return(nil, errors.New("failed to get SVM"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify GetSVM was called but UnsetSvmActiveDirectory was not
	s.env.AssertCalled(s.T(), "GetSVM", mock.Anything, int64(1))
	s.env.AssertNotCalled(s.T(), "UnsetSvmActiveDirectory", mock.Anything, mock.Anything)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_SmbTeardownFeatureFlagEnabled_UnsetSvmActiveDirectoryFails() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetSVM)
	s.env.RegisterActivity(commonActivity.UnsetSvmActiveDirectory)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Create test SVM
	testSvm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
		PoolID:    1,
	}

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: true,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetSVM, mock.Anything, int64(1)).Return(testSvm, nil)
	s.env.OnActivity(commonActivity.UnsetSvmActiveDirectory, mock.Anything, testSvm).Return(nil, errors.New("failed to unset SVM Active Directory"))
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify both GetSVM and UnsetSvmActiveDirectory were called
	s.env.AssertCalled(s.T(), "GetSVM", mock.Anything, int64(1))
	s.env.AssertCalled(s.T(), "UnsetSvmActiveDirectory", mock.Anything, testSvm)
}

func (s *VolumeDeleteTestSuite) Test_DeleteSnapmirrorInONTAPFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to delete snapmirror"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{
			SecretID:      "",
			CertificateID: "",
			Password:      "password",
		}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to delete snapmirror")
}

func (s *VolumeDeleteTestSuite) Test_WaitForONTAPJobFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{
		State: "failure",
	}, errors.New("ONTAP job failed"))
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateJobStatusDoneError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Mock UpdateJobStatus to succeed for PROCESSING state
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStatePROCESSING)
	})).Return(nil)

	// Mock UpdateJobStatus to fail for DONE state (successful completion)
	expectedError := errors.New("failed to update job status to DONE")
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateDONE) && job.ErrorDetails == ""
	})).Return(expectedError)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), expectedError.Error())
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateJobStatusErrorDetailsError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	deleteVolError := errors.New("failed to get hosts from ONTAP")
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(deleteVolError)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Mock UpdateJobStatus to succeed for PROCESSING state
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStatePROCESSING)
	})).Return(nil)

	// Mock UpdateJobStatus to fail for DONE state with error details
	errorDetailsUpdateError := errors.New("failed to update job status with error details")
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateERROR) && job.ErrorDetails != ""
	})).Return(errorDetailsUpdateError)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), errorDetailsUpdateError.Error())
}

func (s *SnapshotDeleteTestSuite) TestShouldUpdateVolumeStateToError() {
	// Returns false for legitimate business errors
	err := &vsaerrors.CustomError{TrackingID: vsaerrors.ErrDeleteVolumeWhenInSplitState}
	assert.False(s.T(), shouldUpdateVolumeStateToError(err))

	err = &vsaerrors.CustomError{TrackingID: vsaerrors.ErrDeleteVolumeRestrictedAction}
	assert.False(s.T(), shouldUpdateVolumeStateToError(err))

	// Returns true for other errors
	err = &vsaerrors.CustomError{TrackingID: 999}
	assert.True(s.T(), shouldUpdateVolumeStateToError(err))

	err = &vsaerrors.CustomError{TrackingID: 1000}
	assert.True(s.T(), shouldUpdateVolumeStateToError(err))
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateVolumeStateInDBError_ShouldUpdateVolumeStateToError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("delete volume error"))
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// Mock UpdateVolumeStateInDB to fail (line 160-162)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update volume state error"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateVolumeStateInDBError_ShouldNotUpdateVolumeStateToError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(utilerrors.NewNotFoundErr("volume not found", nil))
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// Mock UpdateVolumeStateInDB to fail (line 167)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update volume state error"))

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_EnforceDeleteProtectionSMBDenied() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.EnforceDeleteProtection)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
		},
		Account: &datamodel.Account{Name: "test_account"},
		Name:    "smb-vol",
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols:         []string{utils.ProtocolSMB},
			RestrictedActions: []string{activities.RestrictedActionDelete},
			ExternalUUID:      "ext-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "SMB")
}

func (s *VolumeDeleteTestSuite) Test_SmbTeardownWorkflow_DetermineSmbTeardownContextError() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to determine SMB teardown context"))
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_SmbTeardownWorkflow_DeleteCifsServerIfUnusedError() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: true,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete CIFS server"))
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_SmbTeardownWorkflow_DeleteDnsRecordIfUnusedError() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register child workflow
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: true,
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete DNS record"))
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_RestoreVolumeStateToPreviousState_WhenErrDeleteVolumeWhenInSplitState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Create error with ErrDeleteVolumeWhenInSplitState tracking ID and wrap it as Temporal application error
	splitStateError := vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeWhenInSplitState, errors.New("volume has clones/replication"))
	wrappedError := vsaerrors.WrapAsTemporalApplicationError(splitStateError)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, wrappedError)
	// Mock UpdateVolumeStateInDB to succeed when restoring volume state (lines 201-203)
	// Use Maybe() since the call might not happen if error extraction fails
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details").Return(nil).Maybe()
	// Also mock the error state update in case the error is not recognized (should not be called)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "volume-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Return(nil).Maybe()
	s.env.OnGetVersion(restoreVolumeStateFromJobChangeID, workflow.DefaultVersion, restoreVolumeStateFromJobVersion).
		Return(workflow.DefaultVersion)

	// Execute workflow with volume that has State and StateDetails set
	volume := &datamodel.Volume{
		BaseModel:    datamodel.BaseModel{UUID: "volume-uuid"},
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: "ready-details",
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify UpdateVolumeStateInDB was called with volume's previous state (not error state)
	// Check that it was called with READY state (the else branch) and not ERROR state (the if branch)
	s.env.AssertCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details")
	s.env.AssertNotCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_RestoreVolumeStateToPreviousState_Fails_WhenErrDeleteVolumeWhenInSplitState() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Create error with ErrDeleteVolumeWhenInSplitState tracking ID and wrap it as Temporal application error
	splitStateError := vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeWhenInSplitState, errors.New("volume has clones/replication"))
	wrappedError := vsaerrors.WrapAsTemporalApplicationError(splitStateError)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, wrappedError)
	// Mock UpdateVolumeStateInDB to fail when restoring volume state (lines 201-203)
	// Use Maybe() since the call might not happen if error extraction fails
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details").Return(errors.New("failed to restore volume state to previous state")).Maybe()
	// Also mock the error state update in case the error is not recognized (should not be called)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "volume-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Return(nil).Maybe()

	// Execute workflow with volume that has State and StateDetails set
	volume := &datamodel.Volume{
		BaseModel:    datamodel.BaseModel{UUID: "volume-uuid"},
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: "ready-details",
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)

	// Verify UpdateVolumeStateInDB was called with volume's previous state (not error state)
	// Even though it fails, the call should still be made (line 201)
	// Check that it was called with READY state (the else branch) and not ERROR state (the if branch)
	s.env.AssertCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details")
	s.env.AssertNotCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails)
}

func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_RestoreVolumeStateFromJob_WhenResolvedVolumeIsDeleting() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.GetVolumeByVolumeID)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	splitStateError := vsaerrors.NewVCPError(vsaerrors.ErrDeleteVolumeWhenInSplitState, errors.New("volume has clones/replication"))
	wrappedError := vsaerrors.WrapAsTemporalApplicationError(splitStateError)

	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
		State:            datamodel.LifeCycleStateDeleting,
		StateDetails:     datamodel.LifeCycleStateDeletingDetails,
		Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}},
		Account:          &datamodel.Account{Name: "test_account"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-external-uuid"},
	}

	s.env.OnActivity(volumeCreateActivity.GetVolumeByVolumeID, mock.Anything, "volume-uuid").Return(volume, nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil, wrappedError)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details").Return(nil).Maybe()
	s.env.OnGetVersion(restoreVolumeStateFromJobChangeID, workflow.DefaultVersion, restoreVolumeStateFromJobVersion).
		Return(restoreVolumeStateFromJobVersion)

	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume.UUID)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())

	s.env.AssertCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateREADY, "ready-details")
	s.env.AssertNotCalled(s.T(), "UpdateVolumeStateInDB", mock.Anything, "volume-uuid", datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails)
}

// Test_DeleteVolumeWorkflow_LargeVolumeCreateJobType tests line 202: setting createJobType to JobTypeCreateLargeVolume for large volumes
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_LargeVolumeCreateJobType() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(poolActivity.GetCreateJobByResourceUUID)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(poolActivity.GetCreateJobByResourceUUID, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CreateJobResult{}, nil).Maybe()

	// Execute workflow with large volume
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name:  "test_volume",
		State: datamodel.LifeCycleStateCreating,
		LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
			LargeCapacity: true,
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	// Verify GetCreateJobByResourceUUID was called with JobTypeCreateLargeVolume (line 202)
	s.env.AssertCalled(s.T(), "GetCreateJobByResourceUUID", mock.Anything, mock.Anything, mock.Anything, string(datamodel.JobTypeCreateLargeVolume))
}

// Test_DeleteVolumeWorkflow_CancellationHandlingError tests lines 221, 223: HandleCancellationInDeleteWorkflow call and error logging
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_CancellationHandlingError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	poolActivity := activities.PoolActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(poolActivity.GetCreateJobByResourceUUID)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// Make GetCreateJobByResourceUUID return error to trigger warning log (line 223)
	s.env.OnActivity(poolActivity.GetCreateJobByResourceUUID, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get create job"))

	// Execute workflow with volume in CREATING state to trigger cancellation handling (lines 221, 223)
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name:  "test_volume",
		State: datamodel.LifeCycleStateCreating,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully (error in cancellation handling is logged but doesn't fail workflow)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteVolumeWorkflow_WaitForONTAPJobFails tests line 275: error return from WaitForONTAPJob failure
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_WaitForONTAPJobFails() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	// Return a response with JobUUID so WaitForONTAPJob will actually call GetOntapJob
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
	// Make GetOntapJob fail to trigger WaitForONTAPJob error (line 275)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("ONTAP job failed"))
	// DeleteVolumeInONTAP should not be called if WaitForONTAPJob fails, but mock it just in case
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete volume in ontap")).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to delete snapmirror in ontap")
}

// Test_DeleteVolumeWorkflow_DeleteIgroupsError tests lines 293-295: DeleteIgroups error handling
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteIgroupsError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteIgroups)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	// Make DeleteIgroups fail (lines 293-295)
	s.env.OnActivity(deleteActivity.DeleteIgroups, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete igroups"))
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			BlockDevices: &[]datamodel.BlockDevice{
				{Name: "lun1"},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to delete igroups")
}

// Test_DeleteVolumeWorkflow_DeleteIgroupsFromBlockPropertiesError tests line 300: DeleteIgroupsFromBlockProperties error handling
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteIgroupsFromBlockPropertiesError() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteIgroupsFromBlockProperties)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	// Make DeleteIgroupsFromBlockProperties fail (line 300)
	s.env.OnActivity(deleteActivity.DeleteIgroupsFromBlockProperties, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete igroups from block properties"))
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			}},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			BlockProperties: &datamodel.BlockProperties{
				HostGroupDetails: []datamodel.HostGroupDetail{
					{HostGroupUUID: "hg1"},
				},
			},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to delete igroups from block properties")
}

// Test_DeleteVolumeWorkflow_DeleteLDAPConfiguration tests lines 319-320, 324-327: DetermineIfVolumeIsLastFilesVolume and DeleteLDAPConfiguration
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DeleteLDAPConfiguration() {
	// Save original value and enable LDAP
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineIfVolumeIsLastFilesVolume)
	s.env.RegisterActivity(deleteActivity.DeleteLDAPConfiguration)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)
	// Make DetermineIfVolumeIsLastFilesVolume return true (lines 319-320)
	s.env.OnActivity(deleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	// DeleteLDAPConfiguration should be called (lines 324-327)
	s.env.OnActivity(deleteActivity.DeleteLDAPConfiguration, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SmbTeardownContext{}, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
			PoolAttributes: &datamodel.PoolAttributes{
				LdapEnabled: true,
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	// Verify DetermineIfVolumeIsLastFilesVolume was called (lines 319-320)
	s.env.AssertCalled(s.T(), "DetermineIfVolumeIsLastFilesVolume", mock.Anything, mock.Anything, mock.Anything)
	// Verify DeleteLDAPConfiguration was called (lines 324-327)
	s.env.AssertCalled(s.T(), "DeleteLDAPConfiguration", mock.Anything, mock.Anything, mock.Anything)
}

// Test_DeleteVolumeWorkflow_UpdatesActiveDirectoryStateToReady tests that when the last SMB/LDAP-enabled NFS volume
// is deleted from a pool, the Active Directory state is updated from IN_USE to READY.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdatesActiveDirectoryStateToReady() {
	// Save and enable SMB flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock SmbTeardownWorkflow as a child workflow to avoid complexity
	s.env.OnWorkflow(SmbTeardownWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// This is the key test: GetSvmsForAd returns empty list (no more SVMs using this AD)
	emptySvmList := []*datamodel.Svm{}
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(emptySvmList, nil)

	// UpdateActiveDirectoryState should be called to change state from IN_USE to READY
	s.env.OnActivity(adActivity.UpdateActiveDirectoryState,
		mock.Anything,
		"ad-uuid",
		datamodel.LifeCycleStateREADY,
		datamodel.LifeCycleStateReadyDetails).Return(nil).Once()

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_smb_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolSMB}, // SMB volume
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())

	// Verify UpdateActiveDirectoryState was called to update state to READY
	s.env.AssertExpectations(s.T())
}

// Test_DeleteVolumeWorkflow_DoesNotUpdateActiveDirectoryIfOtherSvmsExist tests that when an SMB volume
// is deleted but other SVMs still use the Active Directory, the state is NOT updated.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_DoesNotUpdateActiveDirectoryIfOtherSvmsExist() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineSmbTeardownContext)
	s.env.RegisterActivity(deleteActivity.DeleteCifsServerIfUnused)
	s.env.RegisterActivity(deleteActivity.DeleteDnsRecordIfUnused)
	s.env.RegisterActivity(commonActivity.GetSVM)
	s.env.RegisterActivity(commonActivity.UnsetSvmActiveDirectory)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterWorkflow(SmbTeardownWorkflow)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(activities.CommonActivities.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// Mock SmbTeardownWorkflow activities
	smbTeardownCtx := &activities.SmbTeardownContext{
		ShouldDelete: false, // Should not delete because other SVMs exist
		PoolID:       1,
	}
	s.env.OnActivity(deleteActivity.DetermineSmbTeardownContext, mock.Anything, mock.Anything, mock.Anything).Return(smbTeardownCtx, nil)
	s.env.OnActivity(deleteActivity.DeleteCifsServerIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteDnsRecordIfUnused, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd returns non-empty list (other SVMs still use this AD)
	existingSvms := []*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "other-svm"},
	}
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(existingSvms, nil)

	// UpdateActiveDirectoryState should NOT be called because other SVMs still use this AD
	// We intentionally don't set up a mock for UpdateActiveDirectoryState

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_smb_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolSMB}, // SMB volume
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteVolumeWorkflow_GetSvmsForAdError tests that when GetSvmsForAd fails,
// the workflow returns an error and AD state is not updated.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_GetSvmsForAdError() {
	// Save and enable SMB flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock SmbTeardownWorkflow as a child workflow
	s.env.OnWorkflow(SmbTeardownWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd fails with an error
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(nil, errors.New("database query failed"))

	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_smb_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolSMB},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "database query failed")
}

// Test_DeleteVolumeWorkflow_UpdateActiveDirectoryStateError tests that when UpdateActiveDirectoryState fails,
// the workflow returns an error.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_UpdateActiveDirectoryStateError() {
	// Save and enable SMB flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock SmbTeardownWorkflow as a child workflow
	s.env.OnWorkflow(SmbTeardownWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd returns empty list (no more SVMs)
	emptySvmList := []*datamodel.Svm{}
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(emptySvmList, nil)

	// UpdateActiveDirectoryState fails
	s.env.OnActivity(adActivity.UpdateActiveDirectoryState,
		mock.Anything,
		"ad-uuid",
		datamodel.LifeCycleStateREADY,
		datamodel.LifeCycleStateReadyDetails).Return(errors.New("failed to update AD state"))

	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_smb_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolSMB},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed with error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to update AD state")
}

// Test_DeleteVolumeWorkflow_NFSWithLDAP_UpdatesActiveDirectoryStateToReady tests that when
// an NFS volume with LDAP enabled is deleted and no more SVMs use the AD, the AD state transitions to READY.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_NFSWithLDAP_UpdatesActiveDirectoryStateToReady() {
	// Save and enable LDAP flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(deleteActivity.DetermineIfVolumeIsLastFilesVolume)
	s.env.RegisterActivity(deleteActivity.DeleteLDAPConfiguration)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// DetermineIfVolumeIsLastFilesVolume returns true (this is the last files volume)
	s.env.OnActivity(deleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(deleteActivity.DeleteLDAPConfiguration, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd returns empty list (no more SVMs using this AD)
	emptySvmList := []*datamodel.Svm{}
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(emptySvmList, nil)

	// UpdateActiveDirectoryState should be called to change state to READY
	s.env.OnActivity(adActivity.UpdateActiveDirectoryState,
		mock.Anything,
		"ad-uuid",
		datamodel.LifeCycleStateREADY,
		datamodel.LifeCycleStateReadyDetails).Return(nil).Once()

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with NFS volume and LDAP enabled
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			PoolAttributes: &datamodel.PoolAttributes{
				LdapEnabled: true, // LDAP enabled
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_nfs_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolNFS}, // NFS volume
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())

	// Verify UpdateActiveDirectoryState was called
	s.env.AssertExpectations(s.T())
}

// Test_DeleteVolumeWorkflow_NFSWithoutLDAP_DoesNotUpdateActiveDirectory tests that when
// an NFS volume without LDAP is deleted, the AD state is NOT updated.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_NFSWithoutLDAP_DoesNotUpdateActiveDirectory() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd and UpdateActiveDirectoryState should NOT be called because LDAP is not enabled
	// We intentionally don't set up mocks for these activities

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with NFS volume and LDAP disabled
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			PoolAttributes: &datamodel.PoolAttributes{
				LdapEnabled: false, // LDAP disabled
			},
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_nfs_volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolNFS}, // NFS volume
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteVolumeWorkflow_NoActiveDirectory_DoesNotCheckSvms tests that when
// a volume has no Active Directory configured, no AD state updates are attempted.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_NoActiveDirectory_DoesNotCheckSvms() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd and UpdateActiveDirectoryState should NOT be called because no AD configured
	// We intentionally don't set up mocks for these activities

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with volume that has no Active Directory
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			ActiveDirectory: nil, // No Active Directory
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_volume_no_ad",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolNFS},
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteVolumeWorkflow_NilPoolAttributes_SMBVolume tests that when
// Pool.PoolAttributes is nil but volume is SMB, AD state transition logic still runs.
func (s *VolumeDeleteTestSuite) Test_DeleteVolumeWorkflow_NilPoolAttributes_SMBVolume() {
	// Save and enable SMB flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	adActivity := active_directory_activities.ActiveDirectoryActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotPolicyInONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteVolumeAssociatedSnapshots)
	s.env.RegisterActivity(adActivity.GetSvmsForAd)
	s.env.RegisterActivity(adActivity.UpdateActiveDirectoryState)
	s.env.RegisterActivity(deleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetOntapJob)

	// Mock SmbTeardownWorkflow as a child workflow
	s.env.OnWorkflow(SmbTeardownWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock activities
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil).Maybe()
	s.env.OnActivity(deleteActivity.DeleteVolumeAssociatedSnapshots, mock.Anything, mock.Anything).Return(nil)

	// GetSvmsForAd should be called even with nil PoolAttributes because it's SMB
	emptySvmList := []*datamodel.Svm{}
	s.env.OnActivity(adActivity.GetSvmsForAd, mock.Anything, int64(1)).Return(emptySvmList, nil)

	// UpdateActiveDirectoryState should be called
	s.env.OnActivity(adActivity.UpdateActiveDirectoryState,
		mock.Anything,
		"ad-uuid",
		datamodel.LifeCycleStateREADY,
		datamodel.LifeCycleStateReadyDetails).Return(nil).Once()

	s.env.OnActivity(deleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow with SMB volume and nil PoolAttributes
	volume := &datamodel.Volume{
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
			PoolAttributes: nil, // nil PoolAttributes
			ActiveDirectory: &datamodel.ActiveDirectory{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "ad-uuid"},
			},
		},
		Account: &datamodel.Account{
			Name: "test_account",
		},
		Name: "test_smb_volume_nil_attrs",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "test-external-uuid",
			Protocols:    []string{utils.ProtocolSMB}, // SMB volume
		},
	}
	s.env.ExecuteWorkflow(DeleteVolumeWorkflow, volume)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())

	// Verify UpdateActiveDirectoryState was called
	s.env.AssertExpectations(s.T())
}

func TestVolumeDeleteWorkflowSuite(t *testing.T) {
	suite.Run(t, new(VolumeDeleteTestSuite))
}

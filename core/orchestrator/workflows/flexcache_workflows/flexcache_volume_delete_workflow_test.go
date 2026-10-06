package flexcache_workflows

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type FlexCacheDeleteUnitTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment

	// store activities to use in tests so we don't need to mock them every time
	commonActivity                *activities.CommonActivities
	volumeCreateActivity          *activities.VolumeCreateActivity
	volumeDeleteActivity          *activities.VolumeDeleteActivity
	flexCacheVolumeDeleteActivity *flexcache_activities.FlexCacheVolumeDeleteActivity
	getJobState                   datamodel.JobState
}

func (s *FlexCacheDeleteUnitTestSuite) SetupTest() {
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
	s.env.RegisterWorkflow(DeleteFlexCacheVolumeWorkflow)

	// Register activities
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("DescribeVolume", mock.Anything, mock.Anything).Return(CreateTestVolumeForDelete(), nil).Maybe()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	volumeDeleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	flexCacheVolumeDeleteActivity := flexcache_activities.FlexCacheVolumeDeleteActivity{SE: mockStorage}

	s.commonActivity = &commonActivity
	s.volumeCreateActivity = &volumeCreateActivity
	s.volumeDeleteActivity = &volumeDeleteActivity
	s.flexCacheVolumeDeleteActivity = &flexCacheVolumeDeleteActivity

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(volumeDeleteActivity.DeleteVolume)
	s.env.RegisterActivity(volumeDeleteActivity.DeleteExportPolicy)
	s.env.RegisterActivity(volumeDeleteActivity.DetermineIfVolumeIsLastFilesVolume)
	s.env.RegisterActivity(volumeDeleteActivity.DeleteLDAPConfiguration)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.DisableFlexCacheWritebackInOntapActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity)
	s.env.RegisterActivity(flexCacheVolumeDeleteActivity.WaitForFlexCacheCreateWorkflowTerminalActivity)
	s.env.OnActivity(flexCacheVolumeDeleteActivity.WaitForFlexCacheCreateWorkflowTerminalActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Default state for EnsureJobState checks. Tests that simulate stale/failed jobs
	// set s.getJobState to a non-NEW value before calling ExecuteWorkflow.
	s.getJobState = datamodel.JobsStateNEW
	s.env.OnActivity(s.commonActivity.GetJob, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, jobUUID string) (*datamodel.Job, error) {
			return &datamodel.Job{State: string(s.getJobState)}, nil
		}).Maybe()
}

func CreateTestVolumeForDelete() *datamodel.Volume {
	return &datamodel.Volume{
		Account:   &datamodel.Account{Name: "account-1"},
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-flexcache-volume",
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "password",
				SecretID:      "",
				CertificateID: "",
			},
			DeploymentName: "test-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				LdapEnabled: false, // Default value, tests can override if needed
			},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{
					ExportPolicyName: "", // Empty is fine, activity checks for this
				},
			},
			Protocols: []string{}, // Initialize empty slice to avoid nil
		},
	}
}

func (s *FlexCacheDeleteUnitTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_Success() {
	volume := CreateTestVolumeForDelete()
	writebackEnabled := true
	volume.CacheParameters = &datamodel.CacheParameters{
		CacheConfig: &datamodel.CacheConfig{WritebackEnabled: &writebackEnabled},
	}
	disableWritebackJobUuid := "disable-writeback-job-uuid"
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DisableFlexCacheWritebackInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DisableWritebackJobResponse: &vsa.OntapAsyncResponse{JobUUID: disableWritebackJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, disableWritebackJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)
	heartbeatTimeout := captureActivityHeartbeatTimeout(s.env, "CancelPrepopulateJobsForVolume")
	expectedPolicy, policyErr := workflows.PopulateRetryPolicyParams()
	s.Require().NoError(policyErr)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	assert.Equal(s.T(), expectedPolicy.HeartBeatTimeout, *heartbeatTimeout)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_StaleJob_Error() {
	volume := CreateTestVolumeForDelete()

	s.getJobState = datamodel.JobsStateERROR
	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func writebackEnabledVolume() *datamodel.Volume {
	volume := CreateTestVolumeForDelete()
	writebackEnabled := true
	volume.CacheParameters = &datamodel.CacheParameters{
		CacheConfig: &datamodel.CacheConfig{WritebackEnabled: &writebackEnabled},
	}
	return volume
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DisableWritebackFailure() {
	volume := writebackEnabledVolume()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DisableFlexCacheWritebackInOntapActivity, mock.Anything, mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DisableWritebackWaitFailure() {
	volume := writebackEnabledVolume()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DisableFlexCacheWritebackInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DisableWritebackJobResponse: &vsa.OntapAsyncResponse{JobUUID: "disable-writeback-job-uuid"},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "disable-writeback-job-uuid", mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_GetNodeFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UnmountVolumeFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "127.0.0.1",
	}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UnmountVolumeWaitFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "127.0.0.1",
	}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: "unmount-job-uuid"},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "unmount-job-uuid", mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteFlexCacheVolumeFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "127.0.0.1",
	}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteFlexCacheVolumeWaitFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "127.0.0.1",
	}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: "delete-job-uuid"},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "delete-job-uuid", mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteExportPolicyFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{
		BaseModel:       datamodel.BaseModel{ID: 1},
		EndpointAddress: "127.0.0.1",
	}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: "delete-job-uuid"},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "delete-job-uuid", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteSVMPeeringFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1", BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_GetFlexCacheAndReplicationCountsOnClusterPeeringActivityFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1", BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UpdateClusterPeeringRowStateDeletedInDBActivityFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1", BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		VolumeReplicationCountOnClusterPeering: 0,
		FlexCacheVolumeCountOnClusterPeering:   1,
	}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteClusterPeeringRowInDBActivityFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1", BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		VolumeReplicationCountOnClusterPeering: 0,
		FlexCacheVolumeCountOnClusterPeering:   1,
	}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteClusterPeerFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1", BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		VolumeReplicationCountOnClusterPeering: 0,
		FlexCacheVolumeCountOnClusterPeering:   1,
	}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(nil, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_SmbTeardownWorkflowFailure() {
	// Save original value and enable flag
	originalEnableSmb := enableSmb
	defer func() { enableSmb = originalEnableSmb }()
	enableSmb = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolSMB}

	// Register child workflow
	s.env.RegisterWorkflow(workflows.SmbTeardownWorkflow)

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	// Mock SmbTeardownWorkflow to return an error
	s.env.OnWorkflow(workflows.SmbTeardownWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("smb_teardown_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_DeleteVolumeFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UpdateVolumeStateInDBFailure() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("some_error"))

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UpdateJobStatusFailureBeforeRun() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("some_error"))

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UpdateJobStatusFailureAfterFailedRun() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(func(ctx context.Context, job *datamodel.Job) error {
		if job.State == string(datamodel.JobsStatePROCESSING) {
			return nil
		}

		return errors.New("some_error")
	})
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{}}, errors.New("some_error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_UpdateJobStatusFailureAfterCompletedRun() {
	volume := CreateTestVolumeForDelete()

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(func(ctx context.Context, job *datamodel.Job) error {
		if job.State == string(datamodel.JobsStatePROCESSING) {
			return nil
		}

		return errors.New("some_error")
	})
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: ""},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, "", mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapEnabled_LastFilesVolume() {
	// Save original value and enable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: true,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteLDAPConfiguration, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertCalled(s.T(), "DetermineIfVolumeIsLastFilesVolume", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertCalled(s.T(), "DeleteLDAPConfiguration", mock.Anything, mock.Anything, mock.Anything)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapEnabled_NotLastFilesVolume() {
	// Save original value and enable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: true,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertCalled(s.T(), "DetermineIfVolumeIsLastFilesVolume", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "DeleteLDAPConfiguration", mock.Anything, mock.Anything, mock.Anything)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapDisabled() {
	// Save original value and disable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = false

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: true,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "DetermineIfVolumeIsLastFilesVolume", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "DeleteLDAPConfiguration", mock.Anything, mock.Anything, mock.Anything)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapEnabled_PoolNotLdapEnabled() {
	// Save original value and enable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: false,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "DetermineIfVolumeIsLastFilesVolume", mock.Anything, mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "DeleteLDAPConfiguration", mock.Anything, mock.Anything, mock.Anything)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapEnabled_DetermineIfVolumeIsLastFilesVolumeFailure() {
	// Save original value and enable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: true,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("determine last files volume error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_LdapEnabled_DeleteLDAPConfigurationFailure() {
	// Save original value and enable flag
	originalEnableLdap := enableLdap
	defer func() { enableLdap = originalEnableLdap }()
	enableLdap = true

	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"
	volume.VolumeAttributes.Protocols = []string{utils.ProtocolNFSv3}
	volume.Pool.PoolAttributes = &datamodel.PoolAttributes{
		LdapEnabled: true,
	}

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DetermineIfVolumeIsLastFilesVolume, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteLDAPConfiguration, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("delete LDAP configuration error"))
	s.env.OnActivity(s.volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteFlexCacheVolumeWorkflow_CancellationHandling tests lines 98-101, 104, 113-115, 117, 124: cancellation handling when volume is in CREATING state
func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_CancellationHandling() {
	volume := CreateTestVolumeForDelete()
	volume.State = datamodel.LifeCycleStateCreating

	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Register cancellation activities
	cancellationActivity := &activities.CancellationActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.RegisterActivity(cancellationActivity)
	s.env.RegisterActivity(poolActivity)

	// Mock GetCreateJobByResourceUUID to return nil (no create job found)
	s.env.OnActivity("GetCreateJobByResourceUUID", mock.Anything, volume.UUID, mock.Anything, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteFlexCacheVolumeWorkflow_CancellationHandlingError tests line 124: when HandleCancellationInDeleteWorkflow encounters an error
// Note: HandleCancellationInDeleteWorkflow currently always returns nil, so line 124 may be unreachable with current implementation.
// This test exercises the cancellation handling path to ensure the workflow continues normally even when cancellation handling encounters issues.
func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_CancellationHandlingError() {
	volume := CreateTestVolumeForDelete()
	volume.State = datamodel.LifeCycleStateCreating

	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Register cancellation activities
	cancellationActivity := &activities.CancellationActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.RegisterActivity(cancellationActivity)
	s.env.RegisterActivity(poolActivity)

	// Mock GetCreateJobByResourceUUID to return an error, which will cause HandleCancellationInDeleteWorkflow
	// to log a warning and return nil (line 124 would be hit if HandleCancellationInDeleteWorkflow returned an error)
	s.env.OnActivity("GetCreateJobByResourceUUID", mock.Anything, volume.UUID, mock.Anything, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, errors.New("cancellation handling error"))

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError()) // Should proceed with normal delete despite cancellation error
}

// Test_DeleteFlexCacheVolumeWorkflow_CorrelationIDError tests lines 98-101: when GetCorrelationIDFromWorkflowContextLoggerFields returns an error
func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_CorrelationIDError() {
	volume := CreateTestVolumeForDelete()
	volume.State = datamodel.LifeCycleStateCreating

	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	// Register cancellation activities
	cancellationActivity := &activities.CancellationActivity{}
	poolActivity := &activities.PoolActivity{}
	s.env.RegisterActivity(cancellationActivity)
	s.env.RegisterActivity(poolActivity)

	// Mock GetCreateJobByResourceUUID to return nil (no create job found)
	s.env.OnActivity("GetCreateJobByResourceUUID", mock.Anything, volume.UUID, "", string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_CancelPrepopulateJobsSuccess() {
	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, volume.UUID).Return(nil).Once()
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	s.env.AssertCalled(s.T(), "CancelPrepopulateJobsForVolume", mock.Anything, volume.UUID)
}

func (s *FlexCacheDeleteUnitTestSuite) Test_DeleteFlexCacheVolumeWorkflow_CancelPrepopulateJobsFails_ContinuesDelete() {
	volume := CreateTestVolumeForDelete()
	unmountJobUuid := "unmount-job-uuid"
	deleteJobUuid := "delete-job-uuid"

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// First call fails (tested); Temporal retries, so allow a second call that succeeds to avoid mock panic
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, volume.UUID).Return(errors.New("cancel failed")).Once()
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelPrepopulateJobsForVolume, mock.Anything, volume.UUID).Return(nil).Maybe()
	s.env.OnActivity(s.commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UnmountVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		UnmountJobResponse: &vsa.OntapAsyncResponse{JobUUID: unmountJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, unmountJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteFlexCacheVolumeInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{
		DeleteJobResponse: &vsa.OntapAsyncResponse{JobUUID: deleteJobUuid},
	}, nil)
	s.env.OnActivity(s.commonActivity.GetOntapJob, mock.Anything, deleteJobUuid, mock.Anything).Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteExportPolicy, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.RefreshDBVolumeForDeleteActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{DBVolume: volume}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetClusterPeeringFromDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.UpdateClusterPeeringRowStateDeletedInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity, mock.Anything, mock.Anything).Return(&flexcache.DeleteFlexCacheResult{}, nil)
	s.env.OnActivity(s.flexCacheVolumeDeleteActivity.CancelFlexCacheCreateWorkflowIfPreparingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.volumeDeleteActivity.DeleteVolume, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteFlexCacheVolumeWorkflow, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError()) // Delete should succeed even when cancel fails
	s.env.AssertCalled(s.T(), "CancelPrepopulateJobsForVolume", mock.Anything, volume.UUID)
}

func TestFlexCacheDeleteUnitTestSuite(t *testing.T) {
	suite.Run(t, new(FlexCacheDeleteUnitTestSuite))
}

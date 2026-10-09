package replicationWorkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type SnapshotsDeleteTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *SnapshotsDeleteTestSuite) SetupTest() {
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
	s.env.RegisterWorkflow(DeleteInternalSnapshotWorkflow)
}

func (s *SnapshotsDeleteTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func (s *SnapshotsDeleteTestSuite) Test_DeleteSnapshotWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := replicationActivities.InternalSnapshotsDeleteActivity{SE: mockStorage}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
	}
	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.ListSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.GetNodeFromDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotsInONTAP)
	s.env.RegisterActivity(deleteActivity.ListSnapshotFromDB)
	s.env.RegisterActivity(deleteActivity.DehydrateSnapshots)
	s.env.RegisterActivity(deleteActivity.UpdateSnapshotRecordInDB)

	// Mock activities
	s.env.OnActivity(deleteActivity.GetNodeFromDB, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.ListSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotsInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.ListSnapshotFromDB, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.DehydrateSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.UpdateSnapshotRecordInDB, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow

	s.env.ExecuteWorkflow(DeleteInternalSnapshotWorkflow, params)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertNumberOfCalls(s.T(), "UpdateJob", 2)
}

func (s *SnapshotsDeleteTestSuite) Test_DeleteSnapshotWorkflow_Failure() {
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := replicationActivities.InternalSnapshotsDeleteActivity{SE: mockStorage}
	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
	}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)

	// Register activities
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(deleteActivity.ListSnapshotInONTAP)
	s.env.RegisterActivity(deleteActivity.GetNodeFromDB)
	s.env.RegisterActivity(deleteActivity.DeleteSnapshotsInONTAP)
	s.env.RegisterActivity(deleteActivity.ListSnapshotFromDB)
	s.env.RegisterActivity(deleteActivity.DehydrateSnapshots)
	s.env.RegisterActivity(deleteActivity.UpdateSnapshotRecordInDB)

	// Mock activities
	s.env.OnActivity(deleteActivity.GetNodeFromDB, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.ListSnapshotInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapshotsInONTAP, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.ListSnapshotFromDB, mock.Anything, mock.Anything).Return(params, nil)
	s.env.OnActivity(deleteActivity.DehydrateSnapshots, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(deleteActivity.UpdateSnapshotRecordInDB, mock.Anything, mock.Anything).Return(errors.New("failed to update snapshot details"))

	// Execute workflow

	s.env.ExecuteWorkflow(DeleteInternalSnapshotWorkflow, params)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")

	assert.Nil(s.T(), err)
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func TestSnapshotsDeleteTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotsDeleteTestSuite))
}

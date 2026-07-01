package workflows

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"gorm.io/gorm"
)

type UnRegisterUnitTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *UnRegisterUnitTestSuite) SetupTest() {
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
	s.env.RegisterWorkflow(UnRegisterNodeFromHarvestFarmWorkflow)
}

func (s *UnRegisterUnitTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func TestUnRegisterUnitTestSuite(t *testing.T) {
	suite.Run(t, new(UnRegisterUnitTestSuite))
}

const (
	nodeCount int = 2
)

func getUnRegisterNodes() []*datamodel.Node {
	var nodes []*datamodel.Node
	createdAt := time.Now()
	for i := 0; i < nodeCount; i++ {
		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:        int64(i),
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
				UUID:      "test-node-uuid-" + strconv.Itoa(i)},
			State: datamodel.LifeCycleStateDeleted,
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func getNodeGroupMap(isDelete bool) []*datamodel.NodeNodeGroupMap {
	var nodeGroupMap []*datamodel.NodeNodeGroupMap
	createdAt := time.Now()
	for i := 0; i < nodeCount; i++ {
		groupMap := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{
				ID:        int64(i),
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
				UUID:      "test-nodegroup-map-uuid-" + strconv.Itoa(i)},
			NodeID:      int64(i),
			NodeGroupID: int64(i),
			NodeGroup: &datamodel.NodeGroup{
				BaseModel: datamodel.BaseModel{ID: int64(i), CreatedAt: createdAt, UpdatedAt: createdAt},
				LeaseName: "test-harvest-lease-" + strconv.Itoa(i),
				Name:      "test-harvest-name-" + strconv.Itoa(i),
			},
		}
		if isDelete {
			groupMap.DeletedAt = &gorm.DeletedAt{Time: createdAt, Valid: true}
		}
		nodeGroupMap = append(nodeGroupMap, groupMap)
	}
	return nodeGroupMap
}

func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeFromHarvestFarmWorkflowSuccess() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	nodesInfo := getUnRegisterNodes()
	nodeGroupMapInfo := getNodeGroupMap(false)

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}
	testActParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID: testParams.PoolID,
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, testActParams.PoolID).Return(nodesInfo, nil)
	for index, nodeInfo := range nodesInfo {
		mockStorage.On("GetNodeNodeGroupMapByNodeID", mock.Anything, nodeInfo.ID).Return(nodeGroupMapInfo[index], nil)
		mockStorage.On("DeleteNodeNodeGroupMap", mock.Anything, nodeInfo.ID).Return(nil)
		mockStorage.On("GetNodeGroupMapNodeCount", mock.Anything, nodeGroupMapInfo[index].NodeGroupID).Return(int64(1), nil)
	}
	// Mock activities
	s.env.OnActivity(unRegisterHarvestActivity.DeletePollersFromHarvestFarm, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow success
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

// Below test case will validate when wf received empty nodes with no error
func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeFromHarvestFarmWorkflow_EmptyNodes() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}
	testActParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID: testParams.PoolID,
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, testActParams.PoolID).Return([]*datamodel.Node{}, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow success
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

// Below test case will validate when wf received empty nodegroupmap with no error
func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeFromHarvestFarmWorkflow_EmptyNodeGroupMap() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	nodesInfo := getUnRegisterNodes()

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}
	testActParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID: testParams.PoolID,
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, testActParams.PoolID).Return(nodesInfo, nil)

	// Mock activities
	s.env.OnActivity(unRegisterHarvestActivity.GetNodeGroupMapping, mock.Anything, mock.Anything).Return(nil, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow success
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

// Below test case will validate when wf received non-retryable error from GetNodes activity
func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeToHarvestFailWithNoNodes() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}

	mockStorage.On("GetNodesByPoolID", mock.Anything, testParams.PoolID).Return(nil,
		vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.NewNotFoundErr("node", nil)))

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

// Below test case validates when all nodes have no nodeGroupMap: the activity skips each
// NotFound and returns an empty list, so the workflow completes successfully.
func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeToHarvestSucceedsWithNoNodeGroups() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	nodesInfo := getUnRegisterNodes()

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}
	testActParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID: testParams.PoolID,
	}

	notFoundErr := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, errors.NewNotFoundErr("nodegroupmap", nil))

	mockStorage.On("GetNodesByPoolID", mock.Anything, testActParams.PoolID).Return(nodesInfo, nil)
	for _, nodeInfo := range nodesInfo {
		mockStorage.On("GetNodeNodeGroupMapByNodeID", mock.Anything, nodeInfo.ID).Return(nil, notFoundErr)
	}

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow success
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

func (s *UnRegisterUnitTestSuite) Test_UnRegisterNodeToHarvestFarmFailToDeleteNodeGroup() {
	mockStorage := database.NewMockStorage(s.T())
	unRegisterHarvestActivity := &activities.UnRegisterNodeFromHarvestActivity{SE: mockStorage}
	s.env.RegisterActivity(unRegisterHarvestActivity)

	nodesInfo := getUnRegisterNodes()
	nodeGroupMapInfo := getNodeGroupMap(false)

	testParams := &unRegisterNodeFromHarvestFarmParams{PoolID: int64(1)}

	testActParams := &activities.UnRegisterNodeFromHarvestActivityParams{
		PoolID: testParams.PoolID,
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, testActParams.PoolID).Return(nodesInfo, nil)
	for index, nodeInfo := range nodesInfo {
		mockStorage.On("GetNodeNodeGroupMapByNodeID", mock.Anything, nodeInfo.ID).Return(nodeGroupMapInfo[index], nil)
	}
	mockStorage.On("DeleteNodeNodeGroupMap", mock.Anything, nodesInfo[0].ID).Return(errors.New("db-error"))
	// Mock activities
	s.env.OnActivity(unRegisterHarvestActivity.DeletePollersFromHarvestFarm, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(UnRegisterNodeFromHarvestFarmWorkflow, testParams)
	// Assert workflow failure
	wfErr := s.env.GetWorkflowError()
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), wfErr)
	assert.Contains(s.T(), wfErr.Error(), "db-error")
	mockStorage.AssertExpectations(s.T())
}

package workflows

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRegisterNodeToHarvestFarmWorkflowInput(t *testing.T) {
	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 123, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}
	assert.Equal(t, int64(123), input.PoolID)
	assert.Equal(t, 5, input.MaxNodesPerGroup)
}

func newTestActivityWithMockStorage(mockStorage *database.MockStorage) *activities.RegisterNodeToHarvestFarmActivity {
	return &activities.RegisterNodeToHarvestFarmActivity{
		SE: mockStorage,
	}
}

func mockMarkCreateRetryState(env *testsuite.TestWorkflowEnvironment, activity *activities.RegisterNodeToHarvestFarmActivity) {
	env.OnActivity(activity.MarkCreateRetryState, mock.Anything, mock.Anything).Return(nil)
}

func newRegisterHarvestWorkflowTestEnvironment(ts *testsuite.WorkflowTestSuite) *testsuite.TestWorkflowEnvironment {
	return ts.NewTestWorkflowEnvironment()
}

func TestRegisterNodeToHarvestFarmWorkflow_ExecutesSuccessfully(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	// Mock the UploadHarvestTemplate activity to return success
	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.Anything).Return(nil)

	// Mock the PoolActivity.GetOnTapCredentials activity
	poolActivity := &activities.PoolActivity{}
	env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).Return(&vlm.OntapCredentials{}, nil)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)

	// Mock validate and create lease activity
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, mock.Anything).Return(nodeMappings, nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_ActivityFails(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)

	// Simulate error in GetNodesByPoolID
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return(nil, assert.AnError)
	// Do not mock AssignTwoNodesToTwoGroups, as it should not be called if GetNodesByPoolID fails

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNotCalled(t, "MarkCreateRetryState", mock.Anything, mock.Anything)
	assert.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_InvalidInput(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 0, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  "customer-project",
		TenantProject:    input.TenantProjectID,
	}).Return(nil, assert.AnError)

	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNotCalled(t, "MarkCreateRetryState", mock.Anything, mock.Anything)
	assert.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_SameNodeIDs(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}} // same ID
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  "customer-project",
		TenantProject:    input.TenantProjectID,
	}).Return(nil, assert.AnError)

	// mock Activity to alert on failure
	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNotCalled(t, "MarkCreateRetryState", mock.Anything, mock.Anything)
	assert.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_LargeGroupSize(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	// Test with a large group size
	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 1000, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	// Mock the UploadHarvestTemplate activity to return success
	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.Anything).Return(nil)

	// Mock the PoolActivity.GetOnTapCredentials activity
	poolActivity := &activities.PoolActivity{}
	env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).Return(&vlm.OntapCredentials{}, nil)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  "customer-project",
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)

	// mock Activities
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, mock.Anything).Return(nodeMappings, nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_LeaseCreateFailure(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return([]*datamodel.NodeNodeGroupMap{{NodeID: node1.ID, NodeGroup: nil}, {NodeID: node2.ID}}, nil)
	mockMarkCreateRetryState(env, activity)
	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	wfErr := env.GetWorkflowError()
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, wfErr)
	assert.Contains(t, wfErr.Error(), "failed to fetch node group details from nodeGroup Map table")
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_UpdateNodeNodeGroupMapFails(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	// Mock the UploadHarvestTemplate activity
	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Mock the PoolActivity.GetOnTapCredentials activity
	poolActivity := &activities.PoolActivity{}
	env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).Return(&vlm.OntapCredentials{}, nil)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeGroup := &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 201}, Name: "test-group"}

	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, NodeGroup: nodeGroup, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, NodeGroup: nodeGroup, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)

	// Mock validate and create lease activity to fail with DB error
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, nodeMappings).Return(nil, errors.New("Failed to update k8s lease info in DB for node"))

	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to update k8s lease info in DB for node")
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_UploadHarvestTemplateFails(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{
		PoolID:            1,
		MaxNodesPerGroup:  5,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	uploadErr := errors.New("upload harvest template failed")
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.Anything).Return(uploadErr)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)

	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, mock.Anything).Return(nodeMappings, nil)
	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), uploadErr.Error())
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_GetCredentialsReturnsNil(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{
		PoolID:            1,
		MaxNodesPerGroup:  5,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	// Mock the UploadHarvestTemplate activity (in case it gets called)
	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.Anything).
		Return(fmt.Errorf("failed to get credentials for pool %d", input.PoolID))

	// Mock the PoolActivity.GetOnTapCredentials activity to return nil credentials
	poolActivity := &activities.PoolActivity{}
	var nilCredentials *vlm.OntapCredentials = nil
	env.OnActivity(poolActivity.GetOnTapCredentials, mock.Anything, mock.Anything).Return(nilCredentials, nil)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeGroup := &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 201}, Name: "test-group"}

	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, NodeGroup: nodeGroup, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, NodeGroup: nodeGroup, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)

	// Mock validate and create lease activity to succeed
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, nodeMappings).Return(nodeMappings, nil)

	env.OnActivity(activity.AlertHarvestRegisterFailure, mock.Anything, mock.Anything).Return(nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get credentials for pool 1")
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_Version1UploadsPerNode(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{
		PoolID:            1,
		MaxNodesPerGroup:  5,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	env.OnGetVersion(harvestRegisterRetryStateChangeID, workflow.DefaultVersion, harvestRegisterRetryStateVersion).
		Return(harvestRegisterRetryStateVersion)

	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.MatchedBy(func(input activities.UploadHarvestTemplateInput) bool {
		return len(input.NodeMappings) == 1
	})).Return(nil).Times(2)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, mock.Anything).Return(nodeMappings, nil)
	mockMarkCreateRetryState(env, activity)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_DefaultVersionUsesBatchUpload(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{
		PoolID:            1,
		MaxNodesPerGroup:  5,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	env.OnGetVersion(harvestRegisterRetryStateChangeID, workflow.DefaultVersion, harvestRegisterRetryStateVersion).
		Return(workflow.DefaultVersion)

	uploadActivity := &activities.UploadHarvestTemplateActivity{}
	env.OnActivity(uploadActivity.UploadHarvestTemplate, mock.Anything, mock.MatchedBy(func(input activities.UploadHarvestTemplateInput) bool {
		return len(input.NodeMappings) == 2
	})).Return(nil).Once()

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	nodeMappings := []*datamodel.NodeNodeGroupMap{
		{NodeID: node1.ID, HarvestConfig: &datamodel.HarvestConfig{}},
		{NodeID: node2.ID, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1, node2}, nil)
	mockStorage.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: input.MaxNodesPerGroup,
		CustomerProject:  input.CustomerProjectID,
		TenantProject:    input.TenantProjectID,
	}).Return(nodeMappings, nil)
	env.OnActivity(activity.ValidateAndCreateKubernetesLease, mock.Anything, mock.Anything).Return(nodeMappings, nil)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertActivityNotCalled(t, "MarkCreateRetryState", mock.Anything, mock.Anything)
	require.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterNodeToHarvestFarmWorkflow_LoadsMappingsFromDBOnFailureWhenEmpty(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := newRegisterHarvestWorkflowTestEnvironment(ts)

	input := RegisterNodeToHarvestFarmWorkflowInput{
		PoolID:            1,
		MaxNodesPerGroup:  5,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	}

	mockStorage := &database.MockStorage{}
	activity := newTestActivityWithMockStorage(mockStorage)
	env.RegisterActivity(activity)

	env.OnGetVersion(harvestRegisterRetryStateChangeID, workflow.DefaultVersion, harvestRegisterRetryStateVersion).
		Return(harvestRegisterRetryStateVersion)

	cancelErr := errors.New("activity cancelled")
	env.OnActivity(activity.RegisterNodeToHarvestFarm, mock.Anything, mock.Anything).Return(nil, cancelErr)

	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	loadedMapping := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 55}, NodeID: 101}
	mockStorage.On("GetNodesByPoolID", mock.Anything, input.PoolID).Return([]*datamodel.Node{node1}, nil)
	mockStorage.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, int64(101), mock.Anything).Return(loadedMapping, nil)

	env.OnActivity(activity.MarkCreateRetryState, mock.Anything, mock.MatchedBy(func(in activities.MarkCreateRetryStateInput) bool {
		return len(in.Mappings) == 1 &&
			in.Mappings[0].NodeID == 101 &&
			!in.Success &&
			strings.Contains(in.Message, cancelErr.Error())
	})).Return(nil)

	env.ExecuteWorkflow(RegisterNodeToHarvestFarmWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.True(t, env.AssertExpectations(t))
	mockStorage.AssertExpectations(t)
}

func TestRegisterHarvestUploadDestination(t *testing.T) {
	oldBucket := harvestPollerBucketName
	defer func() { harvestPollerBucketName = oldBucket }()

	harvestPollerBucketName = ""
	assert.Equal(t, harvestPodUploadURL, registerHarvestUploadDestination())

	harvestPollerBucketName = "harvest-poller-bucket"
	assert.Equal(t, "gs://harvest-poller-bucket", registerHarvestUploadDestination())
}

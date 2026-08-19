package activities

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

func TestRegisterNodeToHarvestFarm_Success(t *testing.T) {
	mockSE := new(database.MockStorage)
	nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", PoolID: 42}, {BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", PoolID: 42}}
	maps := []*datamodel.NodeNodeGroupMap{
		{HarvestConfig: &datamodel.HarvestConfig{}},
		{HarvestConfig: &datamodel.HarvestConfig{}},
	}
	mockSE.On("GetNodesByPoolID", mock.Anything, int64(42)).Return(nodes, nil)
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, mock.AnythingOfType("datamodel.NodeGroupAssignmentParams")).Return(maps, nil)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	result, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{PoolID: 42, MaxNodesPerGroup: 10, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"})
	assert.NoError(t, err)

	var resultMaps []*datamodel.NodeNodeGroupMap
	err = result.Get(&resultMaps)
	assert.NoError(t, err)
	assert.Len(t, resultMaps, len(maps))
}

func TestRegisterNodeToHarvestFarm_NoNodes(t *testing.T) {
	mockSE := new(database.MockStorage)
	mockSE.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{}, nil)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not enough nodes found for pool")
}

func TestRegisterNodeToHarvestFarm_DBError(t *testing.T) {
	mockSE := new(database.MockStorage)
	mockSE.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(nil, errors.New("db error"))
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"})
	assert.Error(t, err)
}

func TestRegisterNodeToHarvestFarm_AssignError(t *testing.T) {
	mockSE := new(database.MockStorage)
	nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, Name: "n1", PoolID: 1}, {BaseModel: datamodel.BaseModel{ID: 2}, Name: "n2", PoolID: 1}}
	mockSE.On("GetNodesByPoolID", mock.Anything, int64(1)).Return(nodes, nil)
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, mock.AnythingOfType("datamodel.NodeGroupAssignmentParams")).Return(nil, errors.New("assign error"))
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{PoolID: 1, MaxNodesPerGroup: 5, CustomerProjectID: "customer-project", TenantProjectID: "tenant-project"})
	assert.Error(t, err)
}

// TestRegisterNodeToHarvestFarm_MultipleHAPairs tests Large Volumes pool with multiple HA pairs (6 nodes = 3 HA pairs)
// This test requires the multi-HA pair registration feature flag to be enabled
func TestRegisterNodeToHarvestFarm_MultipleHAPairs(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 6 nodes (3 HA pairs) for a Large Volumes pool
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 100},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 100},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 100},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 100},
		{BaseModel: datamodel.BaseModel{ID: 5}, Name: "node5", PoolID: 100},
		{BaseModel: datamodel.BaseModel{ID: 6}, Name: "node6", PoolID: 100},
	}

	// Create mock mappings for each HA pair
	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 2}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	pair2Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 3}, NodeID: 3, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 4}, NodeID: 4, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	pair3Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 5}, NodeID: 5, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 6}, NodeID: 6, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(100)).Return(nodes, nil)

	// Expect AssignTwoNodesToTwoGroups to be called 3 times (once per HA pair)
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
		DeploymentName:   "lv-deployment",
		PoolName:         "lv-pool",
		IsRegionalHA:     false,
	}).Return(pair1Maps, nil).Once()

	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
		DeploymentName:   "lv-deployment",
		PoolName:         "lv-pool",
		IsRegionalHA:     false,
	}).Return(pair2Maps, nil).Once()

	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[4],
		Node2:            nodes[5],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
		DeploymentName:   "lv-deployment",
		PoolName:         "lv-pool",
		IsRegionalHA:     false,
	}).Return(pair3Maps, nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	result, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            100,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
		DeploymentName:    "lv-deployment",
		PoolName:          "lv-pool",
		IsRegionalHA:      false,
	})
	assert.NoError(t, err)

	var resultMaps []*datamodel.NodeNodeGroupMap
	err = result.Get(&resultMaps)
	assert.NoError(t, err)
	assert.Len(t, resultMaps, 6, "Should return 6 node mappings for 3 HA pairs")

	// Verify all 3 HA pairs were processed
	mockSE.AssertNumberOfCalls(t, "AssignTwoNodesToTwoGroups", 3)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_OddNodeCount tests handling of odd number of nodes (last node skipped)
// This test requires the multi-HA pair registration feature flag to be enabled
func TestRegisterNodeToHarvestFarm_OddNodeCount(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 5 nodes (2 complete HA pairs + 1 orphan node)
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 101},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 101},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 101},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 101},
		{BaseModel: datamodel.BaseModel{ID: 5}, Name: "node5", PoolID: 101}, // Orphan node
	}

	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 2}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	pair2Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 3}, NodeID: 3, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 4}, NodeID: 4, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(101)).Return(nodes, nil)

	// Only 2 calls expected (last odd node is skipped)
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair1Maps, nil).Once()

	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair2Maps, nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	result, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            101,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	})
	assert.NoError(t, err)

	var resultMaps []*datamodel.NodeNodeGroupMap
	err = result.Get(&resultMaps)
	assert.NoError(t, err)
	assert.Len(t, resultMaps, 4, "Should return 4 node mappings (2 complete HA pairs, orphan node skipped)")

	// Verify only 2 HA pairs were processed (5th node skipped)
	mockSE.AssertNumberOfCalls(t, "AssignTwoNodesToTwoGroups", 2)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_RollbackOnSecondPairFailure tests that when the second HA pair fails,
// the first HA pair's mappings are rolled back (deleted) to maintain atomicity
func TestRegisterNodeToHarvestFarm_RollbackOnSecondPairFailure(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 4 nodes (2 HA pairs)
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 200},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 200},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 200},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 200},
	}

	// First HA pair succeeds
	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 101}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 102}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(200)).Return(nodes, nil)

	// First HA pair assignment succeeds
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
		DeploymentName:   "lv-deployment",
		PoolName:         "lv-pool",
		IsRegionalHA:     false,
	}).Return(pair1Maps, nil).Once()

	// Second HA pair assignment fails
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
		DeploymentName:   "lv-deployment",
		PoolName:         "lv-pool",
		IsRegionalHA:     false,
	}).Return(nil, errors.New("assignment failed for second pair")).Once()

	// Expect rollback: DeleteNodeNodeGroupMap should be called for both mappings from pair 1
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(101)).Return(nil).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(102)).Return(nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            200,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
		DeploymentName:    "lv-deployment",
		PoolName:          "lv-pool",
		IsRegionalHA:      false,
	})

	// Verify error is returned
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error assigning nodes to groups")

	// Verify rollback was called
	mockSE.AssertNumberOfCalls(t, "DeleteNodeNodeGroupMap", 2)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_RollbackOnThirdPairFailure tests rollback with 3 HA pairs where third fails
func TestRegisterNodeToHarvestFarm_RollbackOnThirdPairFailure(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 6 nodes (3 HA pairs)
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 201},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 201},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 201},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 201},
		{BaseModel: datamodel.BaseModel{ID: 5}, Name: "node5", PoolID: 201},
		{BaseModel: datamodel.BaseModel{ID: 6}, Name: "node6", PoolID: 201},
	}

	// First two HA pairs succeed
	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 201}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 202}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}
	pair2Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 203}, NodeID: 3, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 204}, NodeID: 4, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(201)).Return(nodes, nil)

	// First HA pair assignment succeeds
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair1Maps, nil).Once()

	// Second HA pair assignment succeeds
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair2Maps, nil).Once()

	// Third HA pair assignment fails
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[4],
		Node2:            nodes[5],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(nil, errors.New("assignment failed for third pair")).Once()

	// Expect rollback: DeleteNodeNodeGroupMap should be called for all 4 mappings from pairs 1 and 2
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(201)).Return(nil).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(202)).Return(nil).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(203)).Return(nil).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(204)).Return(nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            201,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	})

	// Verify error is returned
	assert.Error(t, err)

	// Verify rollback was called for all 4 mappings
	mockSE.AssertNumberOfCalls(t, "DeleteNodeNodeGroupMap", 4)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_RollbackContinuesOnDeleteError tests that rollback continues
// even if some delete operations fail (best effort cleanup)
func TestRegisterNodeToHarvestFarm_RollbackContinuesOnDeleteError(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 4 nodes (2 HA pairs)
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 202},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 202},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 202},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 202},
	}

	// First HA pair succeeds
	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 301}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 302}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(202)).Return(nodes, nil)

	// First HA pair assignment succeeds
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair1Maps, nil).Once()

	// Second HA pair assignment fails
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(nil, errors.New("assignment failed")).Once()

	// First delete fails, second succeeds - rollback should continue
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(301)).Return(errors.New("delete failed")).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(302)).Return(nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            202,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	})

	// Verify error is returned (original assignment error)
	assert.Error(t, err)

	// Verify both delete calls were attempted despite first one failing
	mockSE.AssertNumberOfCalls(t, "DeleteNodeNodeGroupMap", 2)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_RollbackOnInsufficientMappings tests rollback when
// AssignTwoNodesToTwoGroups returns fewer than 2 mappings
func TestRegisterNodeToHarvestFarm_RollbackOnInsufficientMappings(t *testing.T) {
	// Enable multi-HA pair registration for this test
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = true
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 4 nodes (2 HA pairs)
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 203},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 203},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 203},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 203},
	}

	// First HA pair succeeds
	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 401}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 402}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	// Second HA pair returns only 1 mapping (insufficient)
	pair2Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 403}, NodeID: 3, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(203)).Return(nodes, nil)

	// First HA pair assignment succeeds
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair1Maps, nil).Once()

	// Second HA pair returns insufficient mappings
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[2],
		Node2:            nodes[3],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair2Maps, nil).Once()

	// Expect rollback of first pair's mappings
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(401)).Return(nil).Once()
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(402)).Return(nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	_, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            203,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	})

	// Verify error is returned
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "insufficient mappings")

	// Verify rollback was called for the first pair
	mockSE.AssertNumberOfCalls(t, "DeleteNodeNodeGroupMap", 2)
	mockSE.AssertExpectations(t)
}

// TestRegisterNodeToHarvestFarm_MultiHaPairFlagDisabled tests that only first HA pair is registered
// when ENABLE_MULTI_HA_PAIR_REGISTRATION feature flag is disabled (default behavior)
func TestRegisterNodeToHarvestFarm_MultiHaPairFlagDisabled(t *testing.T) {
	// Ensure multi-HA pair registration is disabled (default)
	oldValue := enableMultiHaPairRegistration
	enableMultiHaPairRegistration = false
	defer func() { enableMultiHaPairRegistration = oldValue }()

	mockSE := new(database.MockStorage)

	// Create 6 nodes (3 HA pairs) - but only first pair should be registered
	nodes := []*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node1", PoolID: 102},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node2", PoolID: 102},
		{BaseModel: datamodel.BaseModel{ID: 3}, Name: "node3", PoolID: 102},
		{BaseModel: datamodel.BaseModel{ID: 4}, Name: "node4", PoolID: 102},
		{BaseModel: datamodel.BaseModel{ID: 5}, Name: "node5", PoolID: 102},
		{BaseModel: datamodel.BaseModel{ID: 6}, Name: "node6", PoolID: 102},
	}

	pair1Maps := []*datamodel.NodeNodeGroupMap{
		{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		{BaseModel: datamodel.BaseModel{ID: 2}, NodeID: 2, HarvestConfig: &datamodel.HarvestConfig{}},
	}

	mockSE.On("GetNodesByPoolID", mock.Anything, int64(102)).Return(nodes, nil)

	// Only expect 1 call for the first HA pair when flag is disabled
	mockSE.On("AssignTwoNodesToTwoGroups", mock.Anything, datamodel.NodeGroupAssignmentParams{
		Node1:            nodes[0],
		Node2:            nodes[1],
		MaxNodesPerGroup: 10,
		CustomerProject:  "customer-project",
		TenantProject:    "tenant-project",
	}).Return(pair1Maps, nil).Once()

	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RegisterNodeToHarvestFarm)

	result, err := env.ExecuteActivity(activity.RegisterNodeToHarvestFarm, RegisterNodeToHarvestFarmInput{
		PoolID:            102,
		MaxNodesPerGroup:  10,
		CustomerProjectID: "customer-project",
		TenantProjectID:   "tenant-project",
	})
	assert.NoError(t, err)

	var resultMaps []*datamodel.NodeNodeGroupMap
	err = result.Get(&resultMaps)
	assert.NoError(t, err)
	assert.Len(t, resultMaps, 2, "Should return only 2 node mappings (first HA pair) when multi-HA registration is disabled")

	// Verify only 1 HA pair was processed
	mockSE.AssertNumberOfCalls(t, "AssignTwoNodesToTwoGroups", 1)
	mockSE.AssertExpectations(t)
}

func TestUploadHarvestTemplate_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		assert.NoError(t, err)
		file, _, err := r.FormFile("file")
		assert.NoError(t, err)
		defer func() {
			cerr := file.Close()
			assert.NoError(t, cerr)
		}()
		content, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "fake-yaml")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    ts.URL,
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                        mockSE,
		LoadHarvestTemplateFunc:   func() (string, error) { return "template: {{.Fake}}", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) { return "fake-yaml", nil },
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.NoError(t, err)
}

// TestUploadHarvestTemplate_PartialFailure_ContinuesRemaining (VSCP-6752): one node mapping fails
// to render/upload; the loop still processes the remaining mapping and the failure is surfaced as
// an aggregated error carrying per-node detail.
func TestUploadHarvestTemplate_PartialFailure_ContinuesRemaining(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{
			{NodeID: 100, HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "fail-lease"}, NodeGroup: &datamodel.NodeGroup{LeaseName: "fail-lease"}},
			{NodeID: 200, HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "ok-lease"}, NodeGroup: &datamodel.NodeGroup{LeaseName: "ok-lease"}},
		},
		UploadURL: ts.URL,
		PoolUUID:  pool.UUID,
		AccountID: pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)

	// processed records which node mappings the loop reached. It is written only from the render
	// callback, which runs synchronously in the activity goroutine, so reading it after
	// ExecuteActivity returns is race-free.
	var processed []string
	activity := &UploadHarvestTemplateActivity{
		SE:                      mockSE,
		LoadHarvestTemplateFunc: func() (string, error) { return "template", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) {
			processed = append(processed, cfg.LEASE_NAME)
			if cfg.LEASE_NAME == "fail-lease" {
				return "", errors.New("render boom")
			}
			return "ok-yaml", nil
		},
	}

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "render boom")
	assert.Contains(t, err.Error(), "node mapping 0")
	// The second mapping must still have been processed despite the first failing.
	assert.Equal(t, []string{"fail-lease", "ok-lease"}, processed)
}

func TestUploadHarvestTemplate_PoolNotFound_ReturnsNonRetryableError(t *testing.T) {
	mockSE := new(database.MockStorage)
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    "http://localhost",
		PoolUUID:     "missing-uuid",
		AccountID:    123,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, input.PoolUUID, input.AccountID).Return(nil, gorm.ErrRecordNotFound)
	activity := &UploadHarvestTemplateActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Pool Record not found")
}

func TestUploadHarvestTemplate_PoolFetchOtherError_ReturnsError(t *testing.T) {
	mockSE := new(database.MockStorage)
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    "http://localhost",
		PoolUUID:     "uuid",
		AccountID:    123,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, input.PoolUUID, input.AccountID).Return(nil, errors.New("db down"))
	activity := &UploadHarvestTemplateActivity{SE: mockSE}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
	assert.NotContains(t, err.Error(), "Pool Record not found")
	assert.Contains(t, err.Error(), "db down")
}

// Below test case will test when auth type is default creds(userName and password)
func TestUploadHarvestTemplate_WithCredentials(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		assert.NoError(t, err)
		file, _, err := r.FormFile("file")
		assert.NoError(t, err)
		defer func() {
			cerr := file.Close()
			assert.NoError(t, cerr)
		}()
		content, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "fake-yaml")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	harvestConfig := &datamodel.HarvestConfig{}
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "test-password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: harvestConfig, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    ts.URL,
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                      mockSE,
		LoadHarvestTemplateFunc: func() (string, error) { return "template: {{.Fake}}", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) {
			// Verify that the password was set from credentials
			assert.Equal(t, strconv.Quote("test-password"), cfg.PASSWORD)
			return "fake-yaml", nil
		},
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.NoError(t, err)
	// Note: We verify the password inside RenderHarvestTemplateFunc since
	// ExecuteActivity serializes/deserializes data, so the original harvestConfig isn't modified
}

// Below test case validates whether special characters in passwords are embedded with quotes
func TestUploadHarvestTemplate_WithCreds_SpecialChars(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		assert.NoError(t, err)
		file, _, err := r.FormFile("file")
		assert.NoError(t, err)
		defer func() {
			cerr := file.Close()
			assert.NoError(t, cerr)
		}()
		content, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "fake-yaml")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	harvestConfig := &datamodel.HarvestConfig{}
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "]yq9$r50Kz5^",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: harvestConfig, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    ts.URL,
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                      mockSE,
		LoadHarvestTemplateFunc: func() (string, error) { return "template: {{.Fake}}", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) {
			// Verify that the password was set from credentials
			assert.Equal(t, strconv.Quote("]yq9$r50Kz5^"), cfg.PASSWORD)
			return "fake-yaml", nil
		},
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.NoError(t, err)
	// Note: We verify the password inside RenderHarvestTemplateFunc since
	// ExecuteActivity serializes/deserializes data, so the original harvestConfig isn't modified
}

// Below test case will test when auth type is secretManager
func TestUploadHarvestTemplate_WithSMCredentials(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := r.ParseMultipartForm(10 << 20)
		assert.NoError(t, err)
		file, _, err := r.FormFile("file")
		assert.NoError(t, err)
		defer func() {
			cerr := file.Close()
			assert.NoError(t, cerr)
		}()
		content, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Contains(t, string(content), "fake-yaml")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	harvestConfig := &datamodel.HarvestConfig{}
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 1,
			SecretID: "test-secret-id",
		},
		AccountID: 1,
	}

	originalGetPassword := vsa.GetPasswordFromCacheOrSecretManager
	oldsmHarvestAuthEnabled := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() {
		vsa.GetPasswordFromCacheOrSecretManager = originalGetPassword
		smHarvestAuthEnabled = oldsmHarvestAuthEnabled
	}()

	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: harvestConfig, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    ts.URL,
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                      mockSE,
		LoadHarvestTemplateFunc: func() (string, error) { return "template: {{.Fake}}", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) {
			// Verify that the password was set from credentials
			assert.Equal(t, "", cfg.PASSWORD)
			assert.Equal(t, 1, cfg.AUTH_TYPE)
			assert.Equal(t, "test-secret-id", cfg.SECRET_ID)
			return "fake-yaml", nil
		},
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.NoError(t, err)
	// Note: We verify the config values inside RenderHarvestTemplateFunc since
	// ExecuteActivity serializes/deserializes data, so the original harvestConfig isn't modified
}

// Below test case will test when smAuth is disabled and returns error
func TestUploadHarvestTemplate_WithSMCredentialsError(t *testing.T) {
	harvestConfig := &datamodel.HarvestConfig{}
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 1,
		},
		AccountID: 1,
	}

	originalGetPassword := vsa.GetPasswordFromCacheOrSecretManager
	oldsmHarvestAuthEnabled := smHarvestAuthEnabled
	smHarvestAuthEnabled = false
	defer func() {
		vsa.GetPasswordFromCacheOrSecretManager = originalGetPassword
		smHarvestAuthEnabled = oldsmHarvestAuthEnabled
	}()
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "", errors.New("creds-fetch-error")
	}

	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: harvestConfig, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    "",
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	activity := &UploadHarvestTemplateActivity{
		SE: mockSE,
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "creds-fetch-error")
}

func TestUploadHarvestTemplate_RenderError(t *testing.T) {
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "test-password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}}},
		UploadURL:    "http://localhost",
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                        mockSE,
		LoadHarvestTemplateFunc:   func() (string, error) { return "template", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) { return "", errors.New("render error") },
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
}

func TestUploadHarvestTemplate_LoadTemplateError(t *testing.T) {
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "test-password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    "http://localhost",
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                      mockSE,
		LoadHarvestTemplateFunc: func() (string, error) { return "", errors.New("load error") },
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
}

func TestUploadHarvestTemplate_HTTPError(t *testing.T) {
	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "test-password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}}},
		UploadURL:    "http://localhost:0", // invalid port
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                        mockSE,
		LoadHarvestTemplateFunc:   func() (string, error) { return "template", nil },
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) { return "fake-yaml", nil },
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
}

// Below test case will test whether k8's lease is been created
func TestValidateAndCreateKubernetesLease_Success(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node maps with proper initialization
	nodeGroup1 := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-uuid-1"},
		Name:      "test-group-1",
	}
	nodeGroup2 := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 2, UUID: "test-uuid-2"},
		Name:      "test-group-2",
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup1,
			NodeGroupID:   nodeGroup1.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
		{
			BaseModel:     datamodel.BaseModel{ID: 2},
			NodeID:        2,
			NodeGroup:     nodeGroup2,
			NodeGroupID:   nodeGroup2.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Use mock.Anything since ExecuteActivity serializes/deserializes the data
	mockSE.On("UpdateNodeGroup", mock.Anything, mock.AnythingOfType("*datamodel.NodeGroup")).Return(nodeGroup1, nil).Once()
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nodeGroupsMap[0], nil).Once()
	mockSE.On("UpdateNodeGroup", mock.Anything, mock.AnythingOfType("*datamodel.NodeGroup")).Return(nodeGroup2, nil).Once()
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nodeGroupsMap[1], nil).Once()

	oldCreateKubernetesLease := createKubernetesLease
	// Mock create lease which is in utils
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		return nil
	}
	defer func() { createKubernetesLease = oldCreateKubernetesLease }()

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	result, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.NoError(t, err)

	var updatedMappings []*datamodel.NodeNodeGroupMap
	err = result.Get(&updatedMappings)
	assert.NoError(t, err)
	assert.NotNil(t, updatedMappings)
	assert.Len(t, updatedMappings, len(nodeGroupsMap))
	mockSE.AssertExpectations(t)
}

// Below test case will test for leaseClient failure
func TestValidateAndCreateKubernetesLease_Failure(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node map with proper initialization
	nodeGroup := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-uuid-1"},
		Name:      "test-group-1",
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup,
			NodeGroupID:   nodeGroup.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock lease creation to fail first
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		return errors.New("lease-client-error")
	}
	t.Cleanup(func() { createKubernetesLease = oldCreateKubernetesLease })

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	_, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "lease-client-error")
	mockSE.AssertExpectations(t)
}

// Below test case will test that no k8's lease is getting created as LeaseName is already updated
func TestValidateAndCreateKubernetesLease(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node map with lease name already set
	nodeGroup := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-uuid-1"},
		Name:      "test-group-1",
		LeaseName: "harvest-test-lease-1",
	}

	// Create mapping with lease name already set in both NodeGroup and HarvestConfig
	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup,
			NodeGroupID:   nodeGroup.ID,
			HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "harvest-test-lease-1"},
		},
	}

	// Mock the leaseExists function to return true (lease exists in Kubernetes)
	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil // Lease exists in Kubernetes
	}
	defer func() { leaseExists = oldLeaseExists }()

	// No need to mock updates since lease already exists and no changes should be made
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		t.Fatal("createKubernetesLease should not be called when lease already exists")
		return nil
	}
	defer func() {
		createKubernetesLease = oldCreateKubernetesLease
	}()

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	result, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.NoError(t, err)

	var updatedMappings []*datamodel.NodeNodeGroupMap
	err = result.Get(&updatedMappings)
	assert.NoError(t, err)
	assert.NotNil(t, updatedMappings)
	assert.Len(t, updatedMappings, len(nodeGroupsMap))
	mockSE.AssertExpectations(t)
}

// Below test case will test when GetNodeGroup call to DB fails
func TestValidateAndCreateKubernetesLease_DBError(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node map with proper initialization
	nodeGroup := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-uuid-1"},
		Name:      "test-group-1",
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup,
			NodeGroupID:   nodeGroup.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock DB error for UpdateNodeGroup - this should be called after lease creation
	mockSE.On("UpdateNodeGroup", mock.Anything, mock.AnythingOfType("*datamodel.NodeGroup")).Return(nil, gorm.ErrRecordNotFound)

	// Override createKubernetesLease to return success since DB error is our test case
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		return nil
	}
	t.Cleanup(func() { createKubernetesLease = oldCreateKubernetesLease })

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	_, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "record not found")
	mockSE.AssertExpectations(t)
}

// Tests the case where UpdateNodeNodeGroupMap fails after UpdateNodeGroup success
func TestValidateAndCreateKubernetesLease_UpdateNodeNodeGroupMapError(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	nodeGroup := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-uuid-1"},
		Name:      "test-group-1",
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup,
			NodeGroupID:   nodeGroup.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock UpdateNodeGroup to succeed
	mockSE.On("UpdateNodeGroup", mock.Anything, mock.AnythingOfType("*datamodel.NodeGroup")).Return(nodeGroup, nil)

	// Mock UpdateNodeNodeGroupMap to fail
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, errors.New("failed to update node group map"))

	// Override createKubernetesLease to return success
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		return nil
	}
	t.Cleanup(func() { createKubernetesLease = oldCreateKubernetesLease })

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	_, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update node group map")
	// Note: We can't verify nodeGroup.LeaseName here because ExecuteActivity
	// serializes/deserializes the data, so the original nodeGroup isn't modified.
	mockSE.AssertExpectations(t)
}

// TestUploadHarvestTemplate_HTTPNon2xx covers the error path for non-2xx HTTP response in UploadHarvestTemplate
func TestUploadHarvestTemplate_HTTPNon2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer ts.Close()

	mockSE := new(database.MockStorage)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		PoolCredentials: &datamodel.PoolCredentials{
			AuthType: 3,
			Password: "test-password",
		},
		AccountID: 1,
	}
	input := UploadHarvestTemplateInput{
		NodeMappings: []*datamodel.NodeNodeGroupMap{{HarvestConfig: &datamodel.HarvestConfig{}, NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-1"}, NodeID: 1}},
		UploadURL:    ts.URL,
		PoolUUID:     pool.UUID,
		AccountID:    pool.AccountID,
	}
	mockSE.On("GetPoolByUUIDForAccount", mock.Anything, pool.UUID, pool.AccountID).Return(pool, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.AnythingOfType("*datamodel.NodeNodeGroupMap")).Return(nil, nil)
	activity := &UploadHarvestTemplateActivity{
		SE:                        mockSE,
		RenderHarvestTemplateFunc: func(cfg *datamodel.HarvestConfig) (string, error) { return "fake-yaml", nil },
	}

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UploadHarvestTemplate)

	_, err := env.ExecuteActivity(activity.UploadHarvestTemplate, input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upload failed for node id 1")
}

// Test case for when lease exists in database but not in Kubernetes
func TestValidateAndCreateKubernetesLease_LeaseExistsInDBButNotInK8s(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node group with existing lease name
	existingLeaseName := "harvest-existing-uuid"
	nodeGroup1 := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "existing-uuid"},
		Name:      "test-group-1",
		LeaseName: existingLeaseName, // Lease name already exists in DB
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup1,
			NodeGroupID:   nodeGroup1.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock the leaseExists function to return false (lease doesn't exist in Kubernetes)
	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return false, nil // Lease doesn't exist in Kubernetes
	}
	defer func() { leaseExists = oldLeaseExists }()

	// Mock createKubernetesLease to succeed
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		return nil
	}
	defer func() { createKubernetesLease = oldCreateKubernetesLease }()

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	result, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.NoError(t, err)

	var updatedMappings []*datamodel.NodeNodeGroupMap
	err = result.Get(&updatedMappings)
	assert.NoError(t, err)
	assert.NotNil(t, updatedMappings)
	assert.Len(t, updatedMappings, 1)
}

// Test case for when lease exists in both database and Kubernetes
func TestValidateAndCreateKubernetesLease_LeaseExistsInBothDBAndK8s(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node group with existing lease name
	existingLeaseName := "harvest-existing-uuid"
	nodeGroup1 := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "existing-uuid"},
		Name:      "test-group-1",
		LeaseName: existingLeaseName, // Lease name already exists in DB
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup1,
			NodeGroupID:   nodeGroup1.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock the leaseExists function to return true (lease exists in Kubernetes)
	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil // Lease exists in Kubernetes
	}
	defer func() { leaseExists = oldLeaseExists }()

	// createKubernetesLease should not be called in this case
	oldCreateKubernetesLease := createKubernetesLease
	createKubernetesLease = func(ctx context.Context, leaseNameSpace, leaseName string) error {
		t.Error("createKubernetesLease should not be called when lease already exists")
		return nil
	}
	defer func() { createKubernetesLease = oldCreateKubernetesLease }()

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	result, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.NoError(t, err)

	var updatedMappings []*datamodel.NodeNodeGroupMap
	err = result.Get(&updatedMappings)
	assert.NoError(t, err)
	assert.NotNil(t, updatedMappings)
	assert.Len(t, updatedMappings, 1)
}

// Test case for when lease check fails
func TestValidateAndCreateKubernetesLease_LeaseCheckError(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}

	// Create test node group with existing lease name
	existingLeaseName := "harvest-existing-uuid"
	nodeGroup1 := &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "existing-uuid"},
		Name:      "test-group-1",
		LeaseName: existingLeaseName, // Lease name already exists in DB
	}

	nodeGroupsMap := []*datamodel.NodeNodeGroupMap{
		{
			BaseModel:     datamodel.BaseModel{ID: 1},
			NodeID:        1,
			NodeGroup:     nodeGroup1,
			NodeGroupID:   nodeGroup1.ID,
			HarvestConfig: &datamodel.HarvestConfig{},
		},
	}

	// Mock the leaseExists function to return an error
	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return false, errors.New("kubernetes connection error")
	}
	defer func() { leaseExists = oldLeaseExists }()

	// Use Temporal test suite to provide proper activity context for heartbeat
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ValidateAndCreateKubernetesLease)

	_, err := env.ExecuteActivity(activity.ValidateAndCreateKubernetesLease, nodeGroupsMap)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kubernetes connection error")
}

func TestAlertHarvestRegisterFailure(t *testing.T) {
	mockSE := new(database.MockStorage)
	activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}
	ctx := context.Background()
	err := activity.AlertHarvestRegisterFailure(ctx, "test-error-details")
	assert.NoError(t, err)
}

func TestUploadHarvestNodeMapping_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, SecretID: "s"},
	}

	err := uploadHarvestNodeMapping(ctx, nil, nil, "http://example/upload", pool, nil, nil, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil mapping")

	err = uploadHarvestNodeMapping(ctx, nil, &datamodel.NodeNodeGroupMap{
		NodeGroup:     &datamodel.NodeGroup{LeaseName: ""},
		HarvestConfig: &datamodel.HarvestConfig{},
	}, "http://x", pool, nil, nil, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LeaseName")

	err = uploadHarvestNodeMapping(ctx, nil, &datamodel.NodeNodeGroupMap{
		NodeGroup: &datamodel.NodeGroup{LeaseName: "L"},
	}, "http://x", pool, nil, nil, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HarvestConfig")
}

func TestUploadHarvestNodeMapping_RenderAndUploadErrors(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, SecretID: "s"},
	}
	mapping := &datamodel.NodeNodeGroupMap{
		NodeID:      1,
		NodeGroupID: 2,
		NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-1"},
		HarvestConfig: &datamodel.HarvestConfig{
			PORT: "9999",
		},
	}

	t.Run("persist update fails", func(t *testing.T) {
		mockSE := new(database.MockStorage)
		mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(nil, errors.New("db update failed"))
		err := uploadHarvestNodeMapping(ctx, mockSE, mapping, "http://example/upload", pool, nil, nil, true)
		assert.Error(t, err)
		mockSE.AssertExpectations(t)
	})

	t.Run("render fails", func(t *testing.T) {
		mockSE := new(database.MockStorage)
		err := uploadHarvestNodeMapping(ctx, mockSE, mapping, "http://example/upload", pool, nil,
			func(*datamodel.HarvestConfig) (string, error) { return "", errors.New("render fail") }, false)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "render fail")
	})
}

// testUploadYAMLFileActivity wraps uploadYAMLFile for Temporal test env (RecordHeartbeat needs activity ctx).
func testUploadYAMLFileActivity(ctx context.Context, input UploadYAMLFileInput) error {
	_, err := uploadYAMLFile(ctx, input)
	return err
}

// TestUploadYAMLFile_TimesOutOnSlowServer verifies uploadYAMLFile (VSCP-6750) fails fast instead of
// hanging when the Harvest farm is slow to respond.
func TestUploadYAMLFile_TimesOutOnSlowServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	t.Setenv("HARVEST_POLLER_IO_TIMEOUT_SECONDS", "1")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(testUploadYAMLFileActivity)

	start := time.Now()
	_, err := env.ExecuteActivity(testUploadYAMLFileActivity, UploadYAMLFileInput{
		URL: ts.URL, YAML: "fake-yaml", LeaseName: "lease-1", NodeID: 1,
	})
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deadline exceeded", "expected upload timeout error")
	assert.Less(t, elapsed, 2*time.Second, "upload should time out before the server responds")
}

func TestUploadHarvestNodeMapping_GCSBucket(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, SecretID: "s"},
	}
	mapping := &datamodel.NodeNodeGroupMap{
		NodeID:        42,
		NodeGroup:     &datamodel.NodeGroup{LeaseName: "harvest-lease-a"},
		HarvestConfig: &datamodel.HarvestConfig{PORT: "9999"},
	}

	var uploadedBucket, uploadedObject string
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return &stubRebalanceBucketClient{
			uploadFn: func(ctx context.Context, bucketName, objectName string, payload []byte, contentType string) error {
				uploadedBucket = bucketName
				uploadedObject = objectName
				assert.Equal(t, "yaml-body", string(payload))
				assert.Equal(t, "application/x-yaml", contentType)
				return nil
			},
		}, nil
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	err := uploadHarvestNodeMapping(ctx, nil, mapping, "gs://rebalance-bucket", pool, nil,
		func(*datamodel.HarvestConfig) (string, error) { return "yaml-body", nil }, false)
	assert.NoError(t, err)
	assert.Equal(t, "rebalance-bucket", uploadedBucket)
	assert.Equal(t, "harvest-lease-a/harvest-42.yaml", uploadedObject)
}

func TestResolveHarvestCreateRetryForMapping(t *testing.T) {
	mapA := &datamodel.NodeNodeGroupMap{
		NodeID:    1,
		NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-a"},
	}
	mapB := &datamodel.NodeNodeGroupMap{
		NodeID:    2,
		NodeGroup: &datamodel.NodeGroup{LeaseName: "lease-b"},
	}
	uploaded := map[int64]struct{}{
		1: {},
	}

	success, msg := resolveHarvestCreateRetryForMapping(mapA, true, "", nil)
	assert.True(t, success)

	success, msg = resolveHarvestCreateRetryForMapping(mapA, false, "upload failed", uploaded)
	assert.True(t, success)
	assert.Equal(t, "success", msg)

	success, msg = resolveHarvestCreateRetryForMapping(mapB, false, "upload failed", uploaded)
	assert.False(t, success)
	assert.Equal(t, "upload failed", msg)

	success, msg = resolveHarvestCreateRetryForMapping(mapB, false, "upload failed", nil)
	assert.False(t, success)
	assert.Equal(t, "upload failed", msg)
}

func TestLoadActiveHarvestMappingsForPool(t *testing.T) {
	ctx := context.Background()
	poolID := int64(9)
	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 101}}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 102}}
	map1 := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 101}

	t.Run("returns active mappings for pool nodes", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", mock.Anything, poolID).Return([]*datamodel.Node{node1, node2}, nil)
		mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, int64(101), mock.Anything).Return(map1, nil)
		mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, int64(102), mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("node_node_group_map", nil))

		activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}
		got, err := activity.LoadActiveHarvestMappingsForPool(ctx, poolID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, map1, got[0])
	})

	t.Run("GetNodesByPoolID error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodesByPoolID", mock.Anything, poolID).Return(nil, errors.New("db down"))

		activity := &RegisterNodeToHarvestFarmActivity{SE: mockSE}
		_, err := activity.LoadActiveHarvestMappingsForPool(ctx, poolID)
		assert.Error(t, err)
	})
}

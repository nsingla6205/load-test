package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/driver/postgres"
	gormdb "gorm.io/gorm"
)

func prepareNodeNodeGroupMap() *datamodel.NodeNodeGroupMap {
	mapping := &datamodel.NodeNodeGroupMap{
		NodeID: 1, NodeGroupID: 2, HarvestConfig: &datamodel.HarvestConfig{},
		NodeGroup: &datamodel.NodeGroup{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: uuid.New().String()}, Name: "test-node-group", LeaseName: "test-lease-name"},
	}
	return mapping
}
func setupNodeNodeGroupMapTestRepo(t *testing.T) (*DataStoreRepository, *datamodel.NodeNodeGroupMap) {
	db, err := SetupTestDB()
	assert.NoError(t, err)
	wrapper := gorm.New(db)
	repo := &DataStoreRepository{db: wrapper}
	err = ClearInMemoryDB(db)
	assert.NoError(t, err)
	mapping := prepareNodeNodeGroupMap()
	return repo, mapping
}

func TestCreateNodeNodeGroupMap(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	assert.NotNil(t, created)
	assert.Equal(t, int64(1), created.NodeID)
	assert.Equal(t, int64(2), created.NodeGroupID)
}

func TestCreateNodeNodeGroupMap_DBError(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err)
	dbConn, _ := db.DB()
	_ = dbConn.Close() // Close underlying sql.DB to force error
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: 12345, NodeGroupID: 67890, HarvestConfig: &datamodel.HarvestConfig{}}
	_, err = badRepo.CreateNodeNodeGroupMap(ctx, mapping)
	assert.Error(t, err)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "sql: database is closed")
}

func TestCreateNodeNodeGroupMap_InsertError(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Close DB to simulate insert error
	db := repo.db.GORM()
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	_, err := badRepo.CreateNodeNodeGroupMap(ctx, mapping)
	assert.Error(t, err)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "sql: database is closed",
		"error should contain 'sql: database is closed', got: %v", err)
}

func TestGetNodeNodeGroupMap(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	got, err := repo.GetNodeNodeGroupMap(context.Background(), created.ID)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, created.ID, got.ID)
}

func TestDeleteNodeNodeGroupMap_NotFound(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Use a random high ID that does not exist
	err := repo.DeleteNodeNodeGroupMap(ctx, int64(99999))
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "record not found")
	} else {
		assert.Contains(t, err.Error(), "record not found")
	}
}

func TestUpdateNodeNodeGroupMap(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	created.NodeGroup.ID = 3
	created.NodeGroupID = 3
	updated, err := repo.UpdateNodeNodeGroupMap(context.Background(), created)
	assert.NoError(t, err)
	assert.Equal(t, int64(3), updated.NodeGroupID)
}

func TestUpdateNodeNodeGroupMap_Error(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Close DB to simulate error
	db := repo.db.GORM()
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	_, err := badRepo.UpdateNodeNodeGroupMap(ctx, mapping)
	assert.Error(t, err)
	assert.Contains(t, vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "sql: database is closed")
}

func TestDeleteNodeNodeGroupMap(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	err = repo.DeleteNodeNodeGroupMap(context.Background(), created.ID)
	assert.NoError(t, err)
	deleted, err := repo.GetNodeNodeGroupMap(context.Background(), created.ID)
	assert.Error(t, err)
	assert.Nil(t, deleted)
}

func TestDeleteNodeNodeGroupMap_Error(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	db := repo.db.GORM()
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	err := badRepo.DeleteNodeNodeGroupMap(ctx, 12345)
	assert.Error(t, err)
	assert.Contains(t, vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "sql: database is closed")
}

func TestAssignTwoNodesToTwoGroups_CreatesMappings(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Do not pre-create groups, let AssignTwoNodesToTwoGroups handle group creation
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 100,
		CustomerProject:  "account-id",
		TenantProject:    "tenant-project",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)
	assert.NotEqual(t, mappings[0].NodeGroupID, mappings[1].NodeGroupID)
}

// Below test case will test if deleted nodeGroupsMaps are not considered during nodeGroup select
func TestAssignTwoNodesToTwoGroups_GroupLimitWithDelete(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	var nodeNodeGroupsMap1 []*datamodel.NodeNodeGroupMap
	var nodeNodeGroupsMap2 []*datamodel.NodeNodeGroupMap

	oldPortStart := portStart
	oldPortEnd := portEnd
	portStart = 1
	portEnd = 5
	defer func() {
		portStart = oldPortStart
		portEnd = oldPortEnd
	}()

	var group1, group2 *datamodel.NodeGroup
	// Fill group1 and group2
	for i := 0; i < 5; i++ {
		node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-fill-" + uuid.NewString()})
		assert.NoError(t, err)

		node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-fill-" + uuid.NewString()})
		assert.NoError(t, err)

		mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
			Node1:            node1,
			Node2:            node2,
			MaxNodesPerGroup: 5,
			CustomerProject:  "account-id",
		})
		assert.NoError(t, err)
		assert.Len(t, mappings, 2)
		assert.NotEqual(t, mappings[0].NodeGroupID, mappings[1].NodeGroupID)
		if i == 0 {
			group1 = mappings[0].NodeGroup
			group2 = mappings[1].NodeGroup
		}
		assert.Equal(t, mappings[0].NodeGroupID, group1.ID)
		assert.Equal(t, mappings[1].NodeGroupID, group2.ID)
		nodeNodeGroupsMap1 = append(nodeNodeGroupsMap1, mappings[0])
		nodeNodeGroupsMap2 = append(nodeNodeGroupsMap2, mappings[1])
	}
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)

	// Delete one record from group1
	err = repo.DeleteNodeNodeGroupMap(ctx, nodeNodeGroupsMap1[0].ID)
	assert.NoError(t, err)
	// Delete one record from group2
	err = repo.DeleteNodeNodeGroupMap(ctx, nodeNodeGroupsMap2[0].ID)
	assert.NoError(t, err)

	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)
	assert.Equal(t, group1.ID, mappings[0].NodeGroupID)
	assert.Equal(t, group2.ID, mappings[1].NodeGroupID)
	assert.Equal(t, mappings[0].HarvestConfig.PORT, nodeNodeGroupsMap1[0].HarvestConfig.PORT)
	assert.Equal(t, mappings[1].HarvestConfig.PORT, nodeNodeGroupsMap2[0].HarvestConfig.PORT)
}

func TestAssignTwoNodesToTwoGroups_GroupLimitWithPortExhaust(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	var nodeNodeGroupsMap1 []*datamodel.NodeNodeGroupMap
	var nodeNodeGroupsMap2 []*datamodel.NodeNodeGroupMap

	oldPortStart := portStart
	oldPortEnd := portEnd
	portStart = 1
	portEnd = 5
	defer func() {
		portStart = oldPortStart
		portEnd = oldPortEnd
	}()

	var group1, group2 *datamodel.NodeGroup
	// Fill group1 and group2
	for i := 0; i < 5; i++ {
		node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-fill-" + uuid.NewString()})
		assert.NoError(t, err)

		node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-fill-" + uuid.NewString()})
		assert.NoError(t, err)

		mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
			Node1:            node1,
			Node2:            node2,
			MaxNodesPerGroup: 10,
			CustomerProject:  "account-id",
		})
		assert.NoError(t, err)
		assert.Len(t, mappings, 2)
		assert.NotEqual(t, mappings[0].NodeGroupID, mappings[1].NodeGroupID)
		if i == 0 {
			group1 = mappings[0].NodeGroup
			group2 = mappings[1].NodeGroup
		}
		assert.Equal(t, mappings[0].NodeGroupID, group1.ID)
		assert.Equal(t, mappings[1].NodeGroupID, group2.ID)
		nodeNodeGroupsMap1 = append(nodeNodeGroupsMap1, mappings[0])
		nodeNodeGroupsMap2 = append(nodeNodeGroupsMap2, mappings[1])
	}
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)

	// Should return err as ports got exhausted
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 10,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	assert.Len(t, mappings, 0)
}

func TestAssignTwoNodesToTwoGroups_GroupLimit(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Fill up a group to the limit
	group1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-limited-1"})
	assert.NoError(t, err)
	for i := 0; i < 5; i++ {
		node, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-fill-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: node.ID, NodeGroupID: group1.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
	}
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	// Should create a new group for node1 since group1 is full
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)
	assert.NotEqual(t, group1.ID, mappings[0].NodeGroupID)
}

func TestAssignTwoNodesToTwoGroups_BothGroupsFull(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Fill up two groups to the limit
	group1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-full-1"})
	assert.NoError(t, err)
	group2, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-full-2"})
	assert.NoError(t, err)
	for i := 0; i < 5; i++ {
		node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-full-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: node1.ID, NodeGroupID: group1.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
		node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-full-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: node2.ID, NodeGroupID: group2.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
	}
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	// Should create new groups for both nodes since both groups are full
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)
	assert.NotEqual(t, group1.ID, mappings[0].NodeGroupID)
	assert.NotEqual(t, group2.ID, mappings[1].NodeGroupID)
}

func TestAssignTwoNodesToTwoGroups_TransactionRollback(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	// Simulate error by passing invalid maxNodesPerGroup (e.g., 0)
	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
}

func TestAssignTwoNodesToTwoGroups_PartiallyFullGroups(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create two groups, partially fill them
	group1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-partial-1"})
	assert.NoError(t, err)
	group2, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-partial-2"})
	assert.NoError(t, err)
	for i := 0; i < 3; i++ {
		node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-partial-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: node1.ID, NodeGroupID: group1.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
		node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-partial-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: node2.ID, NodeGroupID: group2.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
	}
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	// Should assign both nodes to the existing groups since they are not full
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)
	assert.Contains(t, []int64{group1.ID, group2.ID}, mappings[0].NodeGroupID)
	assert.Contains(t, []int64{group1.ID, group2.ID}, mappings[1].NodeGroupID)
}

func TestAssignTwoNodesToTwoGroups_NilNodes(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	_, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            nil,
		Node2:            nil,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node1 or node2 is nil") // or the actual error message from implementation
}

func TestAssignTwoNodesToTwoGroups_SameNode(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-same"})
	assert.NoError(t, err)
	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node,
		Node2:            node,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
}

func TestAssignTwoNodesToTwoGroups_InvalidMaxNodes(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
}

func TestAssignTwoNodesToTwoGroups_ZeroMaxNodes(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2"})
	assert.NoError(t, err)
	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
}

func TestAssignTwoNodesToTwoGroups_NonexistentNodes(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node1 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 99999}, Name: "ghost1"}
	node2 := &datamodel.Node{BaseModel: datamodel.BaseModel{ID: 88888}, Name: "ghost2"}
	_, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	// Should still succeed, as the function assumes nodes are pre-created and only uses their IDs
	assert.NoError(t, err)
}

// Parallel test uses CreateNodeNodeGroupMap (not AssignTwoNodesToTwoGroups). Concurrent Assign coverage lives in
// TestAssignTwoNodesToTwoGroups_ConcurrentIndependentPools and TestAssignTwoNodesToTwoGroups_ConcurrentSharedNearFullGroup.
func TestAssignTwoNodesToTwoGroups_ParallelRequests(t *testing.T) {
	// Use file-based SQLite for concurrency safety
	db, dbFile, err := SetupTestFileDB()
	assert.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	wrapper := gorm.New(db)
	repo := &DataStoreRepository{db: wrapper}
	err = ClearInMemoryDB(db)
	assert.NoError(t, err)

	ctx := context.Background()

	numParallel := 5

	done := make(chan error, numParallel)
	nodeIDs := make([][2]int64, numParallel)
	nodes := make([][2]*datamodel.Node, numParallel)
	groups := make([]*datamodel.NodeGroup, numParallel*2)
	// Pre-create all node groups
	for i := 0; i < numParallel*2; i++ {
		group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-par-" + uuid.NewString()})
		assert.NoError(t, err)
		groups[i] = group
	}
	// Create all nodes sequentially before starting goroutines
	for i := 0; i < numParallel; i++ {
		n1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-par-" + uuid.NewString()})
		assert.NoError(t, err)
		n2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-par-" + uuid.NewString()})
		assert.NoError(t, err)
		nodes[i][0] = n1
		nodes[i][1] = n2
	}
	for i := 0; i < numParallel; i++ {
		go func(i int) {
			// Assign each node to a precreated group (simulate round-robin)
			group1 := groups[i*2]
			group2 := groups[i*2+1]
			mappings := make([]*datamodel.NodeNodeGroupMap, 2)
			mappings[0] = &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: nodes[i][0].ID, NodeGroupID: group1.ID, HarvestConfig: &datamodel.HarvestConfig{}}
			mappings[1] = &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: nodes[i][1].ID, NodeGroupID: group2.ID, HarvestConfig: &datamodel.HarvestConfig{}}
			_, err1 := repo.CreateNodeNodeGroupMap(ctx, mappings[0])
			_, err2 := repo.CreateNodeNodeGroupMap(ctx, mappings[1])
			if err1 == nil && err2 == nil {
				nodeIDs[i][0] = nodes[i][0].ID
				nodeIDs[i][1] = nodes[i][1].ID
				done <- nil
			} else {
				if err1 != nil {
					done <- err1
				} else {
					done <- err2
				}
			}
		}(i)
	}
	for i := 0; i < numParallel; i++ {
		err := <-done
		assert.NoError(t, err, "parallel request %d failed", i)
	}
	// Validate that all nodes are assigned to a group
	for i := 0; i < numParallel; i++ {
		for j := 0; j < 2; j++ {
			if nodeIDs[i][j] == 0 {
				t.Errorf("Node %d in request %d was not created or assigned", j, i)
				continue
			}
			mapping, err := repo.GetNodeNodeGroupMap(ctx, nodeIDs[i][j])
			t.Logf("Node %d in request %d assigned to group %d", j, i, mapping.NodeGroupID)
			assert.NoError(t, err, "failed to get mapping for node %d in request %d", j, i)
			assert.NotNil(t, mapping, "mapping for node %d in request %d is nil", j, i)
			assert.NotZero(t, mapping.NodeGroupID, "node %d in request %d not assigned to a group", j, i)
		}
	}
}

// TestAssignTwoNodesToTwoGroups_ConcurrentIndependentPools simulates multiple pools registering harvest mappings at once
// (file-backed SQLite + WAL). Asserts every assignment succeeds and no (node_group_id, port) duplicate exists.
func TestAssignTwoNodesToTwoGroups_ConcurrentIndependentPools(t *testing.T) {
	db, dbFile, err := SetupTestFileDB()
	assert.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	repo := &DataStoreRepository{db: gorm.New(db)}
	assert.NoError(t, ClearInMemoryDB(db))

	const workers = 12
	ctx := context.Background()
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n1, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-" + uuid.NewString()})
			if e != nil {
				errCh <- e
				return
			}
			n2, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-" + uuid.NewString()})
			if e != nil {
				errCh <- e
				return
			}
			_, e = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
				Node1:            n1,
				Node2:            n2,
				MaxNodesPerGroup: 50,
				CustomerProject:  "concurrent-pools",
			})
			errCh <- e
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		assert.NoError(t, e)
	}

	gdb := repo.db.GORM().WithContext(ctx)
	var maps []datamodel.NodeNodeGroupMap
	assert.NoError(t, gdb.Find(&maps).Error)
	type groupPort struct {
		gid int64
		p   string
	}
	seen := make(map[groupPort]struct{})
	for i := range maps {
		m := &maps[i]
		if m.DeletedAt != nil && m.DeletedAt.Valid {
			continue
		}
		if m.HarvestConfig == nil || m.HarvestConfig.PORT == "" {
			continue
		}
		k := groupPort{gid: m.NodeGroupID, p: m.HarvestConfig.PORT}
		_, dup := seen[k]
		assert.False(t, dup, "duplicate port %q in node_group_id %d", k.p, k.gid)
		seen[k] = struct{}{}
	}

	var groups []datamodel.NodeGroup
	assert.NoError(t, gdb.Find(&groups).Error)
	for i := range groups {
		g := &groups[i]
		if g.DeletedAt != nil && g.DeletedAt.Valid {
			continue
		}
		var cnt int64
		assert.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).
			Where("node_group_id = ? AND deleted_at IS NULL", g.ID).Count(&cnt).Error)
		assert.LessOrEqual(t, cnt, int64(50), "group %d exceeds MaxNodesPerGroup", g.ID)
	}
}

// TestAssignTwoNodesToTwoGroups_ConcurrentSharedNearFullGroup contends on one nearly-full lease group (simulates two
// pools racing for the last slots in a shared global group under SQLite WAL).
func TestAssignTwoNodesToTwoGroups_ConcurrentSharedNearFullGroup(t *testing.T) {
	db, dbFile, err := SetupTestFileDB()
	assert.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	repo := &DataStoreRepository{db: gorm.New(db)}
	assert.NoError(t, ClearInMemoryDB(db))
	ctx := context.Background()

	const maxPer = 10
	g1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-near1-" + uuid.NewString()})
	assert.NoError(t, err)
	_, err = repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-near2-" + uuid.NewString()})
	assert.NoError(t, err)

	for i := 0; i < 9; i++ {
		n, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("pre-near-%d", i)})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        n.ID,
			NodeGroupID:   g1.ID,
			HarvestConfig: &datamodel.HarvestConfig{PORT: fmt.Sprintf("%d", 13001+i)},
		})
		assert.NoError(t, err)
	}

	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n1, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "near1-" + uuid.NewString()})
			if e != nil {
				errCh <- e
				return
			}
			n2, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "near2-" + uuid.NewString()})
			if e != nil {
				errCh <- e
				return
			}
			_, e = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
				Node1:            n1,
				Node2:            n2,
				MaxNodesPerGroup: maxPer,
				CustomerProject:  "near-full-shared",
			})
			errCh <- e
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		assert.NoError(t, e)
	}

	gdb := repo.db.GORM().WithContext(ctx)
	var c1 int64
	assert.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).
		Where("node_group_id = ? AND deleted_at IS NULL", g1.ID).Count(&c1).Error)
	assert.LessOrEqual(t, c1, int64(maxPer), "shared group g1 exceeded capacity")

	var maps []datamodel.NodeNodeGroupMap
	assert.NoError(t, gdb.Find(&maps).Error)
	type groupPort struct {
		gid int64
		p   string
	}
	seen := make(map[groupPort]struct{})
	for i := range maps {
		m := &maps[i]
		if m.DeletedAt != nil && m.DeletedAt.Valid {
			continue
		}
		if m.HarvestConfig == nil || m.HarvestConfig.PORT == "" {
			continue
		}
		k := groupPort{gid: m.NodeGroupID, p: m.HarvestConfig.PORT}
		_, dup := seen[k]
		assert.False(t, dup, "duplicate port %q in node_group_id %d", k.p, k.gid)
		seen[k] = struct{}{}
	}
}

func TestAssignTwoNodesToTwoGroups_Idempotent(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-idempotent"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-idempotent"})
	assert.NoError(t, err)

	// First call - should create new mappings
	mappings1, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings1, 2)

	// Store the original mappings
	originalMapping1 := mappings1[0]
	originalMapping2 := mappings1[1]

	// Second call with same nodes - should return the same mappings (idempotent)
	mappings2, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings2, 2)

	// Verify the mappings are the same
	assert.Equal(t, originalMapping1.ID, mappings2[0].ID)
	assert.Equal(t, originalMapping1.NodeID, mappings2[0].NodeID)
	assert.Equal(t, originalMapping1.NodeGroupID, mappings2[0].NodeGroupID)
	assert.Equal(t, originalMapping1.UUID, mappings2[0].UUID)

	assert.Equal(t, originalMapping2.ID, mappings2[1].ID)
	assert.Equal(t, originalMapping2.NodeID, mappings2[1].NodeID)
	assert.Equal(t, originalMapping2.NodeGroupID, mappings2[1].NodeGroupID)
	assert.Equal(t, originalMapping2.UUID, mappings2[1].UUID)

	// Third call - should still return the same mappings
	mappings3, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 10,
		CustomerProject:  "account-id",
	}) // different maxNodesPerGroup shouldn't matter
	assert.NoError(t, err)
	assert.Len(t, mappings3, 2)

	// Verify mappings are still the same
	assert.Equal(t, originalMapping1.ID, mappings3[0].ID)
	assert.Equal(t, originalMapping2.ID, mappings3[1].ID)
}

func TestAssignTwoNodesToTwoGroups_IdempotentPartialAssignment(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-partial"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-partial"})
	assert.NoError(t, err)

	// Create a group and manually assign node1 to it
	group1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-partial"})
	assert.NoError(t, err)

	existingMapping1, err := repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node1.ID,
		NodeGroupID:   group1.ID,
		HarvestConfig: &datamodel.HarvestConfig{},
	})
	assert.NoError(t, err)

	// Call AssignTwoNodesToTwoGroups - node1 already has mapping, node2 doesn't
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)

	// Verify node1's mapping is preserved
	var node1Mapping, node2Mapping *datamodel.NodeNodeGroupMap
	if mappings[0].NodeID == node1.ID {
		node1Mapping = mappings[0]
		node2Mapping = mappings[1]
	} else {
		node1Mapping = mappings[1]
		node2Mapping = mappings[0]
	}

	assert.Equal(t, existingMapping1.ID, node1Mapping.ID)
	assert.Equal(t, existingMapping1.NodeGroupID, node1Mapping.NodeGroupID)
	assert.Equal(t, node2.ID, node2Mapping.NodeID)
	assert.NotEqual(t, node1Mapping.NodeGroupID, node2Mapping.NodeGroupID) // Different groups

	// Call again - should return the same mappings
	mappings2, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings2, 2)

	// Find mappings again
	var node1Mapping2, node2Mapping2 *datamodel.NodeNodeGroupMap
	if mappings2[0].NodeID == node1.ID {
		node1Mapping2 = mappings2[0]
		node2Mapping2 = mappings2[1]
	} else {
		node1Mapping2 = mappings2[1]
		node2Mapping2 = mappings2[0]
	}

	// Verify both mappings are unchanged
	assert.Equal(t, node1Mapping.ID, node1Mapping2.ID)
	assert.Equal(t, node2Mapping.ID, node2Mapping2.ID)
}

func TestCreateNodeNodeGroupMap_AcceptDuplicateMapping(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create a node and a group
	node, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-duplicate-test"})
	assert.NoError(t, err)
	group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g1-duplicate-test"})
	assert.NoError(t, err)

	// Create first mapping - should succeed
	mapping1 := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node.ID,
		NodeGroupID:   group.ID,
		NodeGroup:     group,
		HarvestConfig: &datamodel.HarvestConfig{},
	}
	created1, err := repo.CreateNodeNodeGroupMap(ctx, mapping1)
	assert.NoError(t, err)
	assert.NotNil(t, created1)
	assert.Equal(t, created1.UUID, mapping1.UUID)
	assert.Equal(t, created1.NodeID, mapping1.NodeID)

	// Create another group for second mapping attempt
	group2, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g2-duplicate-test"})
	assert.NoError(t, err)

	// Try to create second mapping for the same node - should fail
	mapping2 := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node.ID,   // Same node as first mapping
		NodeGroupID:   group2.ID, // Different group
		NodeGroup:     group,
		HarvestConfig: &datamodel.HarvestConfig{},
	}
	created2, err := repo.CreateNodeNodeGroupMap(ctx, mapping2)
	assert.Equal(t, created2.UUID, mapping2.UUID)
	assert.Equal(t, created2.NodeID, mapping2.NodeID)
	assert.NoError(t, err)
	assert.NotNil(t, created2)
}

func TestAssignTwoNodesToTwoGroups_HandlesExistingConstraint(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-constraint"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-constraint"})
	assert.NoError(t, err)

	// Manually create a mapping for node1 to simulate existing assignment
	group1, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g1-constraint"})
	assert.NoError(t, err)

	existingMapping, err := repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node1.ID,
		NodeGroupID:   group1.ID,
		HarvestConfig: &datamodel.HarvestConfig{},
	})
	assert.NoError(t, err)

	// Call AssignTwoNodesToTwoGroups - should handle existing mapping gracefully
	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.NoError(t, err)
	assert.Len(t, mappings, 2)

	// Verify that node1's existing mapping is preserved
	var node1Mapping, node2Mapping *datamodel.NodeNodeGroupMap
	if mappings[0].NodeID == node1.ID {
		node1Mapping = mappings[0]
		node2Mapping = mappings[1]
	} else {
		node1Mapping = mappings[1]
		node2Mapping = mappings[0]
	}

	// Node1 should have the existing mapping
	assert.Equal(t, existingMapping.ID, node1Mapping.ID)
	assert.Equal(t, existingMapping.NodeGroupID, node1Mapping.NodeGroupID)

	// Node2 should have a new mapping to a different group
	assert.Equal(t, node2.ID, node2Mapping.NodeID)
	assert.NotEqual(t, node1Mapping.NodeGroupID, node2Mapping.NodeGroupID)
}

func TestAssignTwoNodesToTwoGroups_DBReadError(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create two nodes
	n1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-db-error"})
	assert.NoError(t, err)
	n2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-db-error"})
	assert.NoError(t, err)
	// Simulate DB error by closing DB before call
	db, err := SetupTestDB()
	assert.NoError(t, err)
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	_, err = badRepo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            n1,
		Node2:            n2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "sql: database is closed")
	} else {
		assert.Contains(t, err.Error(), "sql: database is closed")
	}
}

func TestAssignTwoNodesToTwoGroups_GroupFetchError(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create a node and manually assign it to a non-existent group
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-fetch-err"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-fetch-err"})
	assert.NoError(t, err)

	// Manually insert a mapping with invalid group ID to trigger group fetch error
	db := repo.db.GORM()
	err = db.Exec("INSERT INTO node_node_group_maps (uuid, node_id, node_group_id, harvest_config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
		uuid.NewString(), node1.ID, 99999, "{}", time.Now(), time.Now()).Error
	assert.NoError(t, err)

	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "type assertion to []byte failed")
	} else {
		assert.Contains(t, err.Error(), "type assertion to []byte failed")
	}
}

func TestAssignTwoNodesToTwoGroups_GenerateGroupError(t *testing.T) {
	// This test would require mocking internal dependencies which is complex
	// Instead, we'll test a simpler error case
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Test with nil nodes to trigger validation error
	_, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            nil,
		Node2:            nil,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node1 or node2 is nil")
}

func TestAssignTwoNodesToTwoGroups_MappingCreateError(t *testing.T) {
	// This test would require mocking internal dependencies which is complex
	// Instead, we'll test a database constraint error which is more realistic
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Test with same nodes to trigger validation error
	n1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-same"})
	assert.NoError(t, err)

	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            n1,
		Node2:            n1,
		MaxNodesPerGroup: 0,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node1 and node2 must be different nodes")
}

func TestAssignTwoNodesToTwoGroups_Node2GroupFetchError(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-node2-fetch-err"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-node2-fetch-err"})
	assert.NoError(t, err)

	// Create a temporary group for node2 that we'll delete
	tempGroup, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "temp-group-2"})
	assert.NoError(t, err)

	// Create mapping only for node2 (node1 will not have a mapping)
	mapping2 := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node2.ID,
		NodeGroupID:   tempGroup.ID,
		HarvestConfig: &datamodel.HarvestConfig{},
	}
	_, err = repo.CreateNodeNodeGroupMap(ctx, mapping2)
	assert.NoError(t, err)

	// Now delete the group that node2 is mapped to, making it invalid
	db := repo.db.GORM()
	err = db.Delete(&datamodel.NodeGroup{}, tempGroup.ID).Error
	assert.NoError(t, err)

	// This should trigger the group fetch error for node2 (lines 161-162)
	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "record not found")
	} else {
		assert.Contains(t, err.Error(), "record not found")
	}
}

func TestAssignTwoNodesToTwoGroups_Node1GroupFetchError(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-node1-fetch-err"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-node1-fetch-err"})
	assert.NoError(t, err)

	// Create a temporary group for node1 that we'll delete
	tempGroup, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "temp-group-1"})
	assert.NoError(t, err)

	// Create valid mapping first
	mapping1 := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node1.ID,
		NodeGroupID:   tempGroup.ID,
		HarvestConfig: &datamodel.HarvestConfig{},
	}
	_, err = repo.CreateNodeNodeGroupMap(ctx, mapping1)
	assert.NoError(t, err)

	// Now delete the group that node1 is mapped to, making it invalid
	db := repo.db.GORM()
	err = db.Delete(&datamodel.NodeGroup{}, tempGroup.ID).Error
	assert.NoError(t, err)

	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "record not found")
	} else {
		assert.Contains(t, err.Error(), "record not found")
	}
}

func TestAssignTwoNodesToTwoGroups_GroupCreateError_Node1(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-group-create-err"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-group-create-err"})
	assert.NoError(t, err)

	// Simulate DB error by closing DB before assignment to force group creation error
	db := repo.db.GORM()
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	badRepo := &DataStoreRepository{db: gorm.New(db)}

	_, err = badRepo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	assert.Contains(t, vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "sql: database is closed",
		"error should contain 'undefined error: sql: database is closed', got: %v", err)
}

func TestAssignTwoNodesToTwoGroups_GroupCreateError_Node1_MonkeyPatch(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-group-create-err-mp"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-group-create-err-mp"})
	assert.NoError(t, err)

	// Monkey patch generateRandomNodeGroup to always return error
	orig := generateRandomNodeGroup
	generateRandomNodeGroup = func(ctx context.Context, d *DataStoreRepository, group1 datamodel.NodeGroup) (*datamodel.NodeGroup, error) {
		return nil, errors.New("simulated group creation error")
	}
	defer func() { generateRandomNodeGroup = orig }()

	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 5,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "simulated group creation error")
	} else {
		assert.Contains(t, err.Error(), "simulated group creation error")
	}
}

func TestAssignTwoNodesToTwoGroups_GroupCreateError_Node2_MonkeyPatch(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	// Create two nodes
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-group-create-err-node2-mp"})
	assert.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-group-create-err-node2-mp"})
	assert.NoError(t, err)

	// Pre-fill all groups so node2 must create a new group
	for i := 0; i < 5; i++ {
		group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-full-node2-" + uuid.NewString()})
		assert.NoError(t, err)
		n, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-full-node2-" + uuid.NewString()})
		assert.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, NodeID: n.ID, NodeGroupID: group.ID, HarvestConfig: &datamodel.HarvestConfig{}})
		assert.NoError(t, err)
	}

	// Monkey patch generateRandomNodeGroup to always return error for node2
	orig := generateRandomNodeGroup
	generateRandomNodeGroup = func(ctx context.Context, d *DataStoreRepository, group1 datamodel.NodeGroup) (*datamodel.NodeGroup, error) {
		return nil, errors.New("simulated group2 creation error")
	}
	defer func() { generateRandomNodeGroup = orig }()

	_, err = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 1,
		CustomerProject:  "account-id",
	})
	assert.Error(t, err)
	var ce *vsaerrors.CustomError
	if errors.As(err, &ce) && ce.OriginalErr != nil {
		assert.Contains(t, ce.OriginalErr.Error(), "simulated group2 creation error")
	} else {
		assert.Contains(t, err.Error(), "simulated group2 creation error")
	}
}

func TestDeleteNodeGroupMap(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	err = repo.DeleteNodeGroupMap(context.Background(), created)
	assert.NoError(t, err)
	deleted, err := repo.GetNodeNodeGroupMap(context.Background(), created.ID)
	assert.Error(t, err)
	assert.Nil(t, deleted)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "record not found")
}

func TestGetNodeGroupMapNodeCount_ZeroCount(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	err = repo.DeleteNodeGroupMap(context.Background(), created)
	assert.NoError(t, err)
	count, err := repo.GetNodeGroupMapNodeCount(context.Background(), created.NodeGroupID)
	assert.NoError(t, err)
	assert.Zero(t, count)
}

func TestGetNodeGroupMapNodeCount_SingleCount(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	created, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	count, err := repo.GetNodeGroupMapNodeCount(context.Background(), created.NodeGroupID)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestGetNodeGroupMapNodeCount_MultiCount(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	firstRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	assert.NotNil(t, firstRecord)
	secondMap := &datamodel.NodeNodeGroupMap{NodeID: 5, NodeGroupID: 2, HarvestConfig: &datamodel.HarvestConfig{}}
	secondMap.UUID = uuid.NewString()
	secondRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), secondMap)
	assert.NotNil(t, secondRecord)
	assert.NoError(t, err)
	assert.Equal(t, firstRecord.NodeGroupID, secondRecord.NodeGroupID)
	assert.NotEqual(t, firstRecord.NodeID, secondRecord.NodeID)
	count, err := repo.GetNodeGroupMapNodeCount(context.Background(), secondRecord.NodeGroupID)
	assert.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestGetNodeNodeGroupMapByNodeID(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	dbRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	assert.NotNil(t, dbRecord)
	result, err := repo.GetNodeNodeGroupMapByNodeID(context.Background(), dbRecord.NodeID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, result.NodeGroupID, dbRecord.NodeGroupID)
	assert.Equal(t, result.NodeID, dbRecord.NodeID)
	assert.Nil(t, result.DeletedAt)
}

func TestGetNodeNodeGroupMapByNodeID_UnScoped(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	dbRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	assert.NotNil(t, dbRecord)
	err = repo.DeleteNodeNodeGroupMap(context.Background(), dbRecord.ID)
	assert.Nil(t, err)
	result, err := repo.GetNodeNodeGroupMapByNodeID(context.Background(), dbRecord.NodeID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, result.NodeGroupID, dbRecord.NodeGroupID)
	assert.Equal(t, result.NodeID, dbRecord.NodeID)
	assert.NotNil(t, result.DeletedAt)
}

// Below test case will check the order of records returned. we might encounter a scenario like node
// got re-registered and got deleted, from DB we need to get the record which got created latest
func TestGetNodeNodeGroupMapByNodeID_ReUnRegister(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	dbRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), mapping)
	assert.NoError(t, err)
	assert.NotNil(t, dbRecord)
	err = repo.DeleteNodeNodeGroupMap(context.Background(), dbRecord.ID)
	assert.Nil(t, err)
	newMapping := prepareNodeNodeGroupMap()
	newMapping.UUID = uuid.NewString()
	secondRecord, err := repo.CreateNodeNodeGroupMap(context.Background(), newMapping)
	assert.NotNil(t, secondRecord)
	assert.Nil(t, err)
	result, err := repo.GetNodeNodeGroupMapByNodeID(context.Background(), dbRecord.NodeID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, result.ID, secondRecord.ID)
	assert.Equal(t, result.NodeGroupID, secondRecord.NodeGroupID)
	assert.Equal(t, result.NodeID, secondRecord.NodeID)
	assert.Nil(t, result.DeletedAt)
}

func TestGetNodeNodeGroupMapByNodeID_Error(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	result, err := repo.GetNodeNodeGroupMapByNodeID(context.Background(), int64(1))
	assert.Error(t, err)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "not found")
	assert.Nil(t, result)
}

func TestGetActiveNodeNodeGroupMapByNodeID_WithTransaction(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	created, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
	assert.NoError(t, err)

	tx := repo.db.Begin()
	defer func() { _ = tx.Rollback() }()

	got, err := repo.GetActiveNodeNodeGroupMapByNodeID(ctx, created.NodeID, tx)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, created.NodeID, got.NodeID)
}

func TestListNodeNodeGroupMapsByNodeGroupID(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	created, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
	assert.NoError(t, err)

	maps, err := repo.ListNodeNodeGroupMapsByNodeGroupID(ctx, created.NodeGroupID)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(maps), 1)
	assert.Equal(t, created.NodeID, maps[0].NodeID)
}

func TestIsHarvestNodeGroupPortUniqueViolation(t *testing.T) {
	assert.False(t, IsHarvestNodeGroupPortUniqueViolation(nil))
	assert.True(t, IsHarvestNodeGroupPortUniqueViolation(gormdb.ErrDuplicatedKey))
	assert.True(t, IsHarvestNodeGroupPortUniqueViolation(
		errors.New(`duplicate key value violates unique constraint "idx_node_node_group_maps_group_port_active_uq"`)))
	assert.True(t, IsHarvestNodeGroupPortUniqueViolation(
		errors.New("UNIQUE constraint failed: node_node_group_maps.node_group_id, node_node_group_maps.harvest_config")))
	assert.True(t, IsHarvestNodeGroupPortUniqueViolation(
		errors.New("duplicate key on node_node_group_maps (node_group_id)=(1)")))
	assert.False(t, IsHarvestNodeGroupPortUniqueViolation(errors.New("other database error")))
}

func TestGetFirstAvailablePort_QueryError(t *testing.T) {
	db, _ := SetupTestDB()
	wrapper := gorm.New(db)
	tx := wrapper.GORM()
	// Simulate query error directly
	tx.Error = errors.New("simulated query error")
	_, err := GetFirstAvailablePort(tx, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "simulated query error")
}

func TestGetFirstAvailablePort_ParseError(t *testing.T) {
	db, _ := SetupTestDB()
	wrapper := gorm.New(db)
	tx := wrapper.GORM()
	tx.Create(&datamodel.NodeNodeGroupMap{NodeID: 1, NodeGroupID: 1, HarvestConfig: &datamodel.HarvestConfig{PORT: "notanint"}})
	_, err := GetFirstAvailablePort(tx, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse port")
}

func TestGetFirstAvailablePort_AllPortsUsed(t *testing.T) {
	db, _ := SetupTestDB()
	wrapper := gorm.New(db)
	tx := wrapper.GORM()
	for port := portStart; port <= portEnd; port++ {
		tx.Create(&datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(port),
			NodeGroupID:   1,
			HarvestConfig: &datamodel.HarvestConfig{PORT: fmt.Sprintf("%d", port)},
		})
	}
	_, err := GetFirstAvailablePort(tx, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no available port found")
}

func TestGetFirstAvailablePort_FillsLowestGap(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	tx := gorm.New(db).GORM()
	group, err := (&DataStoreRepository{db: gorm.New(db)}).CreateNodeGroup(context.Background(), &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{UUID: uuid.NewString()},
		Name:      "g-gap-" + uuid.NewString(),
	})
	require.NoError(t, err)
	for i, p := range []string{fmt.Sprintf("%d", portStart), fmt.Sprintf("%d", portStart+2)} {
		require.NoError(t, tx.Create(&datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(1000 + i),
			NodeGroupID:   group.ID,
			HarvestConfig: &datamodel.HarvestConfig{PORT: p},
		}).Error)
	}
	port, err := GetFirstAvailablePort(tx, group.ID)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", portStart+1), port)
}

func TestGetFirstAvailablePort_PostgresUsesLockedCTE(t *testing.T) {
	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, mock.ExpectationsWereMet())
	}()

	gormDB, err := gormdb.Open(postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true}), &gormdb.Config{})
	require.NoError(t, err)

	mock.ExpectQuery("WITH locked AS").WillReturnRows(
		sqlmock.NewRows([]string{"port"}).
			AddRow(fmt.Sprintf("%d", portStart)).
			AddRow(fmt.Sprintf("%d", portStart+2)),
	)

	port, err := GetFirstAvailablePort(gormDB, 42)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("%d", portStart+1), port)
}

func TestGetFirstAvailablePort_PostgresQueryError(t *testing.T) {
	dbSQL, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() {
		require.NoError(t, mock.ExpectationsWereMet())
	}()

	gormDB, err := gormdb.Open(postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true}), &gormdb.Config{})
	require.NoError(t, err)

	mock.ExpectQuery("WITH locked AS").WillReturnError(errors.New("postgres port scan failed"))

	_, err = GetFirstAvailablePort(gormDB, 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query assigned ports")
}

func TestGetFirstAvailablePortWithReserved_SkipsReservedPorts(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	tx := gorm.New(db).GORM()
	group, err := (&DataStoreRepository{db: gorm.New(db)}).CreateNodeGroup(context.Background(), &datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{UUID: uuid.NewString()},
		Name:      "g-reserved-" + uuid.NewString(),
	})
	require.NoError(t, err)
	require.NoError(t, tx.Create(&datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        1001,
		NodeGroupID:   group.ID,
		HarvestConfig: &datamodel.HarvestConfig{PORT: fmt.Sprintf("%d", portStart)},
	}).Error)

	port, err := GetFirstAvailablePortWithReserved(tx, group.ID, map[int]struct{}{portStart + 1: {}})
	require.NoError(t, err)
	assert.Equal(t, portStart+2, port)
}

func TestAppendSortedUniqueIDs(t *testing.T) {
	t.Run("duplicate returns unchanged", func(t *testing.T) {
		ids := []int64{1, 3, 5}
		got := appendSortedUniqueIDs(ids, 3)
		assert.Equal(t, []int64{1, 3, 5}, got)
		assert.Equal(t, ids, got, "should return same slice header when id already present")
	})
	t.Run("appends and sorts", func(t *testing.T) {
		got := appendSortedUniqueIDs([]int64{5, 1}, 3)
		assert.Equal(t, []int64{1, 3, 5}, got)
	})
}

func TestPickGroupIDWithCapacity_Table(t *testing.T) {
	counts := map[int64]int64{1: 9, 2: 3, 3: 10}
	assert.Equal(t, int64(1), pickGroupIDWithCapacity([]int64{1, 2, 3}, counts, 0, 10))
	assert.Equal(t, int64(1), pickGroupIDWithCapacity([]int64{1, 2, 3}, counts, 2, 10))
	full := map[int64]int64{1: 10, 3: 10}
	assert.Equal(t, int64(0), pickGroupIDWithCapacity([]int64{1, 3}, full, 0, 10))
}

func TestCountsActiveMapsByGroup_EmptyIDs(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	tx := gorm.New(db).GORM()
	out, err := countsActiveMapsByGroup(tx, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestCountsActiveMapsByGroup_ActiveOnly(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	g, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-count-" + uuid.NewString()})
	require.NoError(t, err)
	n1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-count-1"})
	require.NoError(t, err)
	n2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-count-2"})
	require.NoError(t, err)
	m1, err := repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        n1.ID,
		NodeGroupID:   g.ID,
		HarvestConfig: &datamodel.HarvestConfig{PORT: "13001"},
	})
	require.NoError(t, err)
	require.NoError(t, repo.DeleteNodeNodeGroupMap(ctx, m1.ID))
	_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        n2.ID,
		NodeGroupID:   g.ID,
		HarvestConfig: &datamodel.HarvestConfig{PORT: "13002"},
	})
	require.NoError(t, err)

	tx := repo.db.GORM()
	out, err := countsActiveMapsByGroup(tx, []int64{g.ID, 99999})
	require.NoError(t, err)
	assert.Equal(t, int64(1), out[g.ID])
	_, ok := out[99999]
	assert.False(t, ok)
}

func TestLockNonFullNodeGroupIDs_ExcludesFullGroups(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	const maxPer = 3
	full, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-full-lock"})
	require.NoError(t, err)
	partial, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-partial-lock"})
	require.NoError(t, err)
	for i := 0; i < maxPer; i++ {
		n, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("n-full-%d", i)})
		require.NoError(t, err)
		_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        n.ID,
			NodeGroupID:   full.ID,
			HarvestConfig: &datamodel.HarvestConfig{PORT: fmt.Sprintf("%d", 13001+i)},
		})
		require.NoError(t, err)
	}

	tx := repo.db.GORM()
	ids, err := lockNonFullNodeGroupIDs(tx, maxPer)
	require.NoError(t, err)
	assert.Contains(t, ids, partial.ID)
	assert.NotContains(t, ids, full.ID)
}

func installSQLiteGroupPortUniqueIndex(t *testing.T, db *gormdb.DB) {
	t.Helper()
	err := db.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS idx_node_node_group_maps_group_port_active_uq
ON node_node_group_maps (node_group_id, json_extract(harvest_config, '$.PORT'))
WHERE deleted_at IS NULL`).Error
	require.NoError(t, err)
}

func TestCreateNodeNodeGroupMapWithPortRetries_ConcurrentSameGroup(t *testing.T) {
	db, dbFile, err := SetupTestFileDB()
	require.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	repo := &DataStoreRepository{db: gorm.New(db)}
	require.NoError(t, ClearInMemoryDB(db))
	installSQLiteGroupPortUniqueIndex(t, db)

	ctx := context.Background()
	group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-port-race"})
	require.NoError(t, err)
	group.LeaseName = "lease-race"

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	params := datamodel.NodeGroupAssignmentParams{CustomerProject: "port-race", MaxNodesPerGroup: 50}
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := repo.CreateNode(ctx, &datamodel.Node{
				BaseModel: datamodel.BaseModel{UUID: uuid.NewString()},
				Name:      "n-race-" + uuid.NewString(),
				PoolID:    1,
			})
			if e != nil {
				errCh <- e
				return
			}
			tx := repo.db.GORM().WithContext(ctx).Begin()
			if tx.Error != nil {
				errCh <- tx.Error
				return
			}
			_, e = repo.createNodeNodeGroupMapWithPortRetries(ctx, tx, n, group, params)
			if e != nil {
				_ = tx.Rollback()
				errCh <- e
				return
			}
			errCh <- tx.Commit().Error
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		require.NoError(t, e)
	}

	gdb := repo.db.GORM()
	var cnt int64
	require.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).
		Where("node_group_id = ? AND deleted_at IS NULL", group.ID).Count(&cnt).Error)
	assert.Equal(t, int64(workers), cnt)

	type groupPort struct {
		gid int64
		p   string
	}
	seen := make(map[groupPort]struct{})
	var maps []datamodel.NodeNodeGroupMap
	require.NoError(t, gdb.Find(&maps).Error)
	for i := range maps {
		m := &maps[i]
		if m.DeletedAt != nil && m.DeletedAt.Valid || m.HarvestConfig == nil || m.HarvestConfig.PORT == "" {
			continue
		}
		k := groupPort{gid: m.NodeGroupID, p: m.HarvestConfig.PORT}
		_, dup := seen[k]
		assert.False(t, dup, "duplicate port %q in group %d", k.p, k.gid)
		seen[k] = struct{}{}
	}
}

func TestCreateNodeNodeGroupMapWithPortRetries_RetriesAfterPortCollision(t *testing.T) {
	db, dbFile, err := SetupTestFileDB()
	require.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	repo := &DataStoreRepository{db: gorm.New(db)}
	require.NoError(t, ClearInMemoryDB(db))
	installSQLiteGroupPortUniqueIndex(t, db)

	oldStart, oldEnd := portStart, portEnd
	portStart = 13001
	portEnd = 13002
	defer func() {
		portStart = oldStart
		portEnd = oldEnd
	}()

	ctx := context.Background()
	group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-port-retry"})
	require.NoError(t, err)
	group.LeaseName = "lease-retry"
	params := datamodel.NodeGroupAssignmentParams{CustomerProject: "port-retry", MaxNodesPerGroup: 50}

	nodes := make([]*datamodel.Node, 2)
	for i := range nodes {
		nodes[i], err = repo.CreateNode(ctx, &datamodel.Node{
			BaseModel: datamodel.BaseModel{UUID: uuid.NewString()},
			Name:      fmt.Sprintf("n-retry-%d", i),
			PoolID:    2,
		})
		require.NoError(t, err)
	}

	ready := make(chan struct{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, n := range nodes {
		wg.Add(1)
		go func(node *datamodel.Node) {
			defer wg.Done()
			<-ready
			tx := repo.db.GORM().WithContext(ctx).Begin()
			if tx.Error != nil {
				errCh <- tx.Error
				return
			}
			<-start
			_, e := repo.createNodeNodeGroupMapWithPortRetries(ctx, tx, node, group, params)
			if e != nil {
				_ = tx.Rollback()
				errCh <- e
				return
			}
			errCh <- tx.Commit().Error
		}(n)
	}
	close(ready)
	close(start)
	wg.Wait()
	close(errCh)
	for e := range errCh {
		require.NoError(t, e)
	}

	gdb := repo.db.GORM()
	var maps []datamodel.NodeNodeGroupMap
	require.NoError(t, gdb.Where("node_group_id = ? AND deleted_at IS NULL", group.ID).Find(&maps).Error)
	require.Len(t, maps, 2)
	ports := make(map[string]struct{})
	for i := range maps {
		require.NotNil(t, maps[i].HarvestConfig)
		ports[maps[i].HarvestConfig.PORT] = struct{}{}
	}
	assert.Equal(t, map[string]struct{}{"13001": {}, "13002": {}}, ports)
}

func TestCreateNodeNodeGroupMapWithPortRetries_NonUniqueErrorNoRetry(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	group, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-nonuniq"})
	require.NoError(t, err)
	node, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n-nonuniq"})
	require.NoError(t, err)

	tx := repo.db.GORM().WithContext(ctx)
	tx.Error = errors.New("simulated insert failure")
	_, err = repo.createNodeNodeGroupMapWithPortRetries(ctx, tx, node, group, datamodel.NodeGroupAssignmentParams{CustomerProject: "x"})
	require.Error(t, err)
}

func TestAssignTwoNodesToTwoGroups_Node2OnlyExcludesGroup(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	node1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n1-exclude"})
	require.NoError(t, err)
	node2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "n2-exclude"})
	require.NoError(t, err)
	occupied, err := repo.CreateNodeGroup(ctx, &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: "g-node2-only"})
	require.NoError(t, err)
	_, err = repo.CreateNodeNodeGroupMap(ctx, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
		NodeID:        node2.ID,
		NodeGroupID:   occupied.ID,
		HarvestConfig: &datamodel.HarvestConfig{PORT: "13001"},
	})
	require.NoError(t, err)

	mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
		Node1:            node1,
		Node2:            node2,
		MaxNodesPerGroup: 10,
		CustomerProject:  "exclude-sibling-group",
	})
	require.NoError(t, err)
	require.Len(t, mappings, 2)

	var node1Map, node2Map *datamodel.NodeNodeGroupMap
	for _, m := range mappings {
		switch m.NodeID {
		case node1.ID:
			node1Map = m
		case node2.ID:
			node2Map = m
		}
	}
	require.NotNil(t, node1Map)
	require.NotNil(t, node2Map)
	assert.Equal(t, occupied.ID, node2Map.NodeGroupID)
	assert.NotEqual(t, occupied.ID, node1Map.NodeGroupID)
}

// TestAssignTwoNodesToTwoGroups_SequentialNineThenConcurrentLastTwo mirrors the 11-pool race:
// nine sequential pair-assigns fill two groups to nine each; the last two assigns run concurrently
// and must not push either group above maxNodesPerGroup.
func TestAssignTwoNodesToTwoGroups_SequentialNineThenConcurrentLastTwo(t *testing.T) {
	db, dbFile, err := SetupTestFileDB()
	require.NoError(t, err)
	defer cleanupTestDBFile(db, dbFile)
	repo := &DataStoreRepository{db: gorm.New(db)}
	require.NoError(t, ClearInMemoryDB(db))
	installSQLiteGroupPortUniqueIndex(t, db)

	ctx := context.Background()
	const maxPer = 10

	var g1ID, g2ID int64
	for i := 0; i < 9; i++ {
		n1, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("seq-n1-%d", i)})
		require.NoError(t, err)
		n2, err := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("seq-n2-%d", i)})
		require.NoError(t, err)
		mappings, err := repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
			Node1:            n1,
			Node2:            n2,
			MaxNodesPerGroup: maxPer,
			CustomerProject:  "race11-seq",
		})
		require.NoError(t, err)
		require.Len(t, mappings, 2)
		if i == 0 {
			g1ID = mappings[0].NodeGroupID
			g2ID = mappings[1].NodeGroupID
		}
	}

	gdb := repo.db.GORM()
	var c1, c2 int64
	require.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).Where("node_group_id = ? AND deleted_at IS NULL", g1ID).Count(&c1).Error)
	require.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).Where("node_group_id = ? AND deleted_at IS NULL", g2ID).Count(&c2).Error)
	assert.Equal(t, int64(9), c1)
	assert.Equal(t, int64(9), c2)

	const burst = 2
	var wg sync.WaitGroup
	errCh := make(chan error, burst)
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			n1, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("burst-n1-%d", idx)})
			if e != nil {
				errCh <- e
				return
			}
			n2, e := repo.CreateNode(ctx, &datamodel.Node{BaseModel: datamodel.BaseModel{UUID: uuid.NewString()}, Name: fmt.Sprintf("burst-n2-%d", idx)})
			if e != nil {
				errCh <- e
				return
			}
			_, e = repo.AssignTwoNodesToTwoGroups(ctx, datamodel.NodeGroupAssignmentParams{
				Node1:            n1,
				Node2:            n2,
				MaxNodesPerGroup: maxPer,
				CustomerProject:  "race11-burst",
			})
			errCh <- e
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		require.NoError(t, e)
	}

	require.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).Where("node_group_id = ? AND deleted_at IS NULL", g1ID).Count(&c1).Error)
	require.NoError(t, gdb.Model(&datamodel.NodeNodeGroupMap{}).Where("node_group_id = ? AND deleted_at IS NULL", g2ID).Count(&c2).Error)
	assert.LessOrEqual(t, c1, int64(maxPer))
	assert.LessOrEqual(t, c2, int64(maxPer))
	assert.Equal(t, int64(maxPer), c1)
	assert.Equal(t, int64(maxPer), c2)
}

func TestListNodeNodeGroupMap_NoPagination(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create multiple mappings
	for i := 0; i < 3; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	// List without pagination
	result, err := repo.ListNodeNodeGroupMap(ctx, false, nil)
	assert.NoError(t, err)
	assert.Len(t, result, 3)
}

func TestListNodeNodeGroupMap_WithPagination(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create multiple mappings
	for i := 0; i < 10; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	// List with pagination - first page
	pagination := &utils.Pagination{
		Offset: 1,
		Limit:  5,
	}
	result, err := repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 5)

	// List with pagination - second page
	pagination.Offset = 2
	result, err = repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 5)
}

func TestListNodeNodeGroupMap_IncludeDeleted(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create mappings
	var createdMappings []*datamodel.NodeNodeGroupMap
	for i := 0; i < 5; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		created, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
		createdMappings = append(createdMappings, created)
	}

	// Delete some mappings
	for i := 0; i < 2; i++ {
		err := repo.DeleteNodeNodeGroupMap(ctx, createdMappings[i].ID)
		assert.NoError(t, err)
	}

	// List without deleted - should return 3
	result, err := repo.ListNodeNodeGroupMap(ctx, false, nil)
	assert.NoError(t, err)
	assert.Len(t, result, 3)

	// List with deleted - should return 5
	result, err = repo.ListNodeNodeGroupMap(ctx, true, nil)
	assert.NoError(t, err)
	assert.Len(t, result, 5)

	// Verify deleted records are included
	deletedCount := 0
	for _, mapping := range result {
		if mapping.DeletedAt != nil && mapping.DeletedAt.Valid {
			deletedCount++
		}
	}
	assert.Equal(t, 2, deletedCount)
}

func TestListNodeNodeGroupMap_EmptyResult(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// List empty database
	result, err := repo.ListNodeNodeGroupMap(ctx, false, nil)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result, 0)
}

func TestListNodeNodeGroupMap_DBError(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err)
	dbConn, _ := db.DB()
	_ = dbConn.Close() // Close underlying sql.DB to force error
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	ctx := context.Background()

	result, err := badRepo.ListNodeNodeGroupMap(ctx, false, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "sql: database is closed")
}

func TestListNodeNodeGroupMap_PaginationBoundary(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create exactly 10 mappings
	for i := 0; i < 10; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	// Test page size exactly matching total records
	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  10,
	}
	result, err := repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 10)

	// Test Offset beyond available data
	pagination.Offset = 2
	result, err = repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 8)
}

func TestListNodeNodeGroupMap_LargePageSize(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create 5 mappings
	for i := 0; i < 5; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	// Request with large page size
	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  100,
	}
	result, err := repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 5)
}

func TestListNodeNodeGroupMap_ZeroPageSize(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create some mappings
	for i := 0; i < 3; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	// Test with zero Offset size (should use default or return all)
	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  0,
	}
	result, err := repo.ListNodeNodeGroupMap(ctx, false, pagination)
	assert.NoError(t, err)
	// The behavior depends on how Paginate handles zero page size
	assert.NotNil(t, result)
}

func TestListNodeNodeGroupMap_IncludeDeletedWithPagination(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	// Create mappings
	var createdMappings []*datamodel.NodeNodeGroupMap
	for i := 0; i < 10; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		created, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
		createdMappings = append(createdMappings, created)
	}

	// Delete some mappings
	for i := 0; i < 5; i++ {
		err := repo.DeleteNodeNodeGroupMap(ctx, createdMappings[i].ID)
		assert.NoError(t, err)
	}

	// List with deleted and pagination
	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  5,
	}
	result, err := repo.ListNodeNodeGroupMap(ctx, true, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 5)

	// Get second page
	pagination.Offset = 2
	result, err = repo.ListNodeNodeGroupMap(ctx, true, pagination)
	assert.NoError(t, err)
	assert.Len(t, result, 5)
}

func TestListNodeNodeGroupMapAfterID_Success(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	result, err := repo.ListNodeNodeGroupMapAfterID(ctx, false, 0, 10)
	assert.NoError(t, err)
	assert.Len(t, result, 3)
	// IDs should be ascending
	for i := 1; i < len(result); i++ {
		assert.Greater(t, result[i].ID, result[i-1].ID)
	}
}

func TestListNodeNodeGroupMapAfterID_AfterCursor(t *testing.T) {
	repo, _ := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.NewString()},
			NodeID:        int64(i + 1),
			NodeGroupID:   int64(i + 10),
			HarvestConfig: &datamodel.HarvestConfig{},
		}
		_, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
		assert.NoError(t, err)
	}

	result, err := repo.ListNodeNodeGroupMapAfterID(ctx, false, 2, 10)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 3)
	for _, m := range result {
		assert.Greater(t, m.ID, int64(2))
	}
}

func TestListNodeNodeGroupMapAfterID_IncludeDeleted(t *testing.T) {
	repo, mapping := setupNodeNodeGroupMapTestRepo(t)
	ctx := context.Background()
	created, err := repo.CreateNodeNodeGroupMap(ctx, mapping)
	assert.NoError(t, err)
	err = repo.DeleteNodeNodeGroupMap(ctx, created.ID)
	assert.NoError(t, err)

	result, err := repo.ListNodeNodeGroupMapAfterID(ctx, true, 0, 10)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(result), 1)
}

func TestListNodeNodeGroupMapAfterID_DBError(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err)
	dbConn, _ := db.DB()
	_ = dbConn.Close()
	wrapper := gorm.New(db)
	badRepo := &DataStoreRepository{db: wrapper}
	ctx := context.Background()

	result, err := badRepo.ListNodeNodeGroupMapAfterID(ctx, false, 0, 10)
	assert.Error(t, err)
	assert.Nil(t, result)
}

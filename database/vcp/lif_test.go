package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGetLifByNodeID(t *testing.T) {
	t.Run("WhenLifExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}
		err = store.db.Create(lif).Error()
		if err != nil {
			tt.Fatalf("Failed to create lif: %v", err)
		}

		result, err := store.GetLifByNodeID(context.Background(), lif.NodeID, lif.AccountID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, lif.Name, result.Name, "Expected lif name %v, got %v", lif.Name, result.Name)
		assert.Equal(tt, lif.NodeID, result.NodeID, "Expected lif node id %v, got %v", lif.NodeID, result.NodeID)
		assert.Equal(tt, lif.AccountID, result.AccountID, "Expected lif account id %v, got %v", lif.AccountID, result.AccountID)
	})
	t.Run("WhenLifDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		_, err1 := store.GetLifByNodeID(context.Background(), 1, 1234)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err1, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "lif not found")
		} else {
			tt.Fatalf("Expected a CustomError, got %v", err1)
		}
	})
}

func TestCreateLif(t *testing.T) {
	t.Run("WhenLifIsCreatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}

		createdLif, err := store.CreateLif(context.Background(), lif)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, lif.Name, createdLif.Name, "Expected lif name %v, got %v", lif.Name, createdLif.Name)
		assert.Equal(tt, lif.NodeID, createdLif.NodeID, "Expected lif node id %v, got %v", lif.NodeID, createdLif.NodeID)
		assert.Equal(tt, lif.AccountID, createdLif.AccountID, "Expected lif account id %v, got %v", lif.AccountID, createdLif.AccountID)
	})
	t.Run("WhenLifAlreadyExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}
		err = store.db.Create(lif).Error()
		if err != nil {
			tt.Fatalf("Failed to create lif: %v", err)
		}

		_, err = store.CreateLif(context.Background(), lif)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "lif already exists")
		} else {
			tt.Fatalf("Expected a CustomError, got %v", err)
		}
	})
}

func TestDeleteLif(t *testing.T) {
	t.Run("WhenLifIsDeletedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}
		err = store.db.Create(lif).Error()
		if err != nil {
			tt.Fatalf("Failed to create lif: %v", err)
		}

		err = store.DeleteLif(context.Background(), lif)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}

		deletedLif := &datamodel.Lif{}
		err = store.db.GORM().First(deletedLif, "uuid = ?", lif.UUID).Error
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			tt.Errorf("Expected record not found error, got %v", err)
		}
	})
}

func TestGetLifForNode(t *testing.T) {
	t.Run("WhenLifExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-svm-uuid",
			},
			Name:      "test_lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}
		err = store.db.Create(lif).Error()
		if err != nil {
			tt.Fatalf("Failed to create lif: %v", err)
		}

		result, err := store.GetLifForNode(context.Background(), lif.NodeID, lif.AccountID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, lif.Name, result.Name, "Expected lif name %v, got %v", lif.Name, result.Name)
		assert.Equal(tt, lif.NodeID, result.NodeID, "Expected lif node id %v, got %v", lif.NodeID, result.NodeID)
		assert.Equal(tt, lif.AccountID, result.AccountID, "Expected lif account id %v, got %v", lif.AccountID, result.AccountID)
	})

	t.Run("ExcludesDirectoryDiagnosisLif", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "diagnosis-filter-node"},
			Name:      "diagnosis-filter-node",
			AccountID: 1234,
			PoolID:    1234,
		}
		require.NoError(tt, store.db.Create(node).Error())

		diagnosisLif := &datamodel.Lif{
			BaseModel:            datamodel.BaseModel{ID: 1, UUID: "diagnosis-lif"},
			Name:                 "diagnosis-lif",
			NodeID:               node.ID,
			AccountID:            node.AccountID,
			IsDirectoryDiagnosis: true,
		}
		operationalLif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "operational-lif"},
			Name:      "operational-lif",
			NodeID:    node.ID,
			AccountID: node.AccountID,
		}
		require.NoError(tt, store.db.Create(diagnosisLif).Error())
		require.NoError(tt, store.db.Create(operationalLif).Error())

		result, err := store.GetLifForNode(context.Background(), node.ID, node.AccountID)
		require.NoError(tt, err)
		assert.Equal(tt, operationalLif.UUID, result.UUID)
		assert.False(tt, result.IsDirectoryDiagnosis)
	})

	t.Run("ReturnsNotFoundWhenOnlyDirectoryDiagnosisLifExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		diagnosisLif := &datamodel.Lif{
			BaseModel:            datamodel.BaseModel{ID: 1, UUID: "only-diagnosis-lif"},
			Name:                 "only-diagnosis-lif",
			NodeID:               1,
			AccountID:            1234,
			IsDirectoryDiagnosis: true,
		}
		require.NoError(tt, store.db.Create(diagnosisLif).Error())

		result, err := store.GetLifForNode(context.Background(), diagnosisLif.NodeID, diagnosisLif.AccountID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestGetLifsForNodesWithProtocol(t *testing.T) {
	t.Run("WhenMultipleNodesExistWithProtocol", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create first node
		node1 := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node1-uuid",
			},
			Name:      "test_node1",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node1).Error()
		if err != nil {
			tt.Fatalf("Failed to create node1: %v", err)
		}

		// Create second node
		node2 := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-node2-uuid",
			},
			Name:      "test_node2",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node2).Error()
		if err != nil {
			tt.Fatalf("Failed to create node2: %v", err)
		}

		// Create third node
		node3 := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "test-node3-uuid",
			},
			Name:      "test_node3",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node3).Error()
		if err != nil {
			tt.Fatalf("Failed to create node3: %v", err)
		}

		// Create NFS LIFs for each node
		nfsLif1 := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-nfs-lif1-uuid",
			},
			Name:      "test_nfs_lif1",
			NodeID:    node1.ID,
			AccountID: node1.AccountID,
			LifDetails: &datamodel.LifDetails{
				ProtocolType: "nfs",
			},
		}
		err = store.db.Create(nfsLif1).Error()
		if err != nil {
			tt.Fatalf("Failed to create nfs lif1: %v", err)
		}

		nfsLif2 := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-nfs-lif2-uuid",
			},
			Name:      "test_nfs_lif2",
			NodeID:    node2.ID,
			AccountID: node2.AccountID,
			LifDetails: &datamodel.LifDetails{
				ProtocolType: "nfs",
			},
		}
		err = store.db.Create(nfsLif2).Error()
		if err != nil {
			tt.Fatalf("Failed to create nfs lif2: %v", err)
		}

		// Create CIFS LIF for the third node
		cifsLif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "test-cifs-lif-uuid",
			},
			Name:      "test_cifs_lif",
			NodeID:    node3.ID,
			AccountID: node3.AccountID,
			LifDetails: &datamodel.LifDetails{
				ProtocolType: "cifs",
			},
		}
		err = store.db.Create(cifsLif).Error()
		if err != nil {
			tt.Fatalf("Failed to create cifs lif: %v", err)
		}

		// Mock the function to avoid JSONB query issues in SQLite
		originalFunc := getLifsWithProtocolDetails
		getLifsWithProtocolDetails = func(query *gorm.DB, protocol string) ([]*datamodel.Lif, error) {
			// For test, simulate the protocol filtering behavior
			switch protocol {
			case "nfs":
				return []*datamodel.Lif{nfsLif1, nfsLif2}, nil
			case "cifs":
				return []*datamodel.Lif{cifsLif}, nil
			default:
				return []*datamodel.Lif{nfsLif1, nfsLif2, cifsLif}, nil
			}
		}
		defer func() { getLifsWithProtocolDetails = originalFunc }()

		// Test getting NFS LIFs for nodes 1 and 2
		result, err := store.GetLifsForNodesWithProtocol(context.Background(), []int64{node1.ID, node2.ID}, node1.AccountID, "nfs")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(result), "Expected 2 lifs, got %d", len(result))

		// Test getting CIFS LIFs for node 3
		result, err = store.GetLifsForNodesWithProtocol(context.Background(), []int64{node3.ID}, node3.AccountID, "cifs")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(result), "Expected 1 lif, got %d", len(result))
		assert.Equal(tt, "cifs", result[0].LifDetails.ProtocolType, "Expected protocol type %v, got %v", "cifs", result[0].LifDetails.ProtocolType)

		// Test getting LIFs for all nodes with no protocol filter
		result, err = store.GetLifsForNodesWithProtocol(context.Background(), []int64{node1.ID, node2.ID, node3.ID}, node1.AccountID, "")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 3, len(result), "Expected 3 lifs, got %d", len(result))
	})

	t.Run("WhenNoNodesProvided", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Test with empty node list
		result, err := store.GetLifsForNodesWithProtocol(context.Background(), []int64{}, int64(1234), "nfs")
		assert.NotNil(tt, err)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "nodeIDs cannot be empty")
			assert.Nil(tt, result, "Expected nil lifs")
		} else {
			t.Fatalf("Expected CustomError, got %v", err)
		}
	})

	t.Run("WhenNoMatchingLifsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a node
		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-node-uuid",
			},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		originalFunc := getLifsWithProtocolDetails
		getLifsWithProtocolDetails = func(query *gorm.DB, protocol string) ([]*datamodel.Lif, error) {
			return []*datamodel.Lif{}, nil
		}
		defer func() { getLifsWithProtocolDetails = originalFunc }()

		// Test with a non-existent protocol
		result, err := store.GetLifsForNodesWithProtocol(context.Background(), []int64{node.ID}, node.AccountID, "non-existent-protocol")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(result), "Expected 0 lifs, got %d", len(result))
	})

	t.Run("ExcludesSoftDeletedLifs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		node := &datamodel.Node{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-node-uuid"},
			Name:      "test_node",
			AccountID: int64(1234),
			PoolID:    1234,
		}
		err = store.db.Create(node).Error()
		assert.NoError(tt, err)

		activeLif := &datamodel.Lif{
			BaseModel:  datamodel.BaseModel{ID: 1, UUID: "active-lif-uuid"},
			Name:       "active_lif",
			NodeID:     node.ID,
			AccountID:  node.AccountID,
			LifDetails: &datamodel.LifDetails{ProtocolType: "nas"},
			IPAddress:  "10.0.0.1",
		}
		err = store.db.Create(activeLif).Error()
		assert.NoError(tt, err)

		deletedLif := &datamodel.Lif{
			BaseModel:  datamodel.BaseModel{ID: 2, UUID: "deleted-lif-uuid"},
			Name:       "deleted_lif",
			NodeID:     node.ID,
			AccountID:  node.AccountID,
			LifDetails: &datamodel.LifDetails{ProtocolType: "nas"},
			IPAddress:  "10.0.0.2",
		}
		err = store.db.Create(deletedLif).Error()
		assert.NoError(tt, err)
		err = store.DeleteLif(context.Background(), deletedLif)
		assert.NoError(tt, err)

		// Empty protocol keeps the query off the JSONB operator, which SQLite lacks.
		result, err := store.GetLifsForNodesWithProtocol(context.Background(), []int64{node.ID}, node.AccountID, "")
		assert.NoError(tt, err)
		assert.Len(tt, result, 1)
		assert.Equal(tt, activeLif.UUID, result[0].UUID)
	})

	t.Run("ExcludesDirectoryDiagnosisLifs", func(tt *testing.T) {
		db, err := SetupTestDB()
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(tt, ClearInMemoryDB(store.db.GORM()))

		operationalLif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "list-operational-lif"},
			Name:      "list-operational-lif",
			NodeID:    1,
			AccountID: 1234,
		}
		diagnosisLif := &datamodel.Lif{
			BaseModel:            datamodel.BaseModel{ID: 2, UUID: "list-diagnosis-lif"},
			Name:                 "list-diagnosis-lif",
			NodeID:               2,
			AccountID:            1234,
			IsDirectoryDiagnosis: true,
		}
		require.NoError(tt, store.db.Create(operationalLif).Error())
		require.NoError(tt, store.db.Create(diagnosisLif).Error())

		result, err := store.GetLifsForNodesWithProtocol(
			context.Background(),
			[]int64{operationalLif.NodeID, diagnosisLif.NodeID},
			operationalLif.AccountID,
			"",
		)
		require.NoError(tt, err)
		require.Len(tt, result, 1)
		assert.Equal(tt, operationalLif.UUID, result[0].UUID)
		assert.False(tt, result[0].IsDirectoryDiagnosis)
	})
}

func TestApplyPoolNasLifIPs(t *testing.T) {
	ctx := context.Background()
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-1"}, Name: "account"}
	require.NoError(t, store.db.Create(account).Error())
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		Name:      "pool",
		AccountID: account.ID,
		VLMConfig: `{"svm":{"svm1":{"svm_lifs":{"ilbnas":[{"lif_name":"svm1-ilbnas","ip":"10.0.0.5"}]}}}}`,
	}
	require.NoError(t, store.db.Create(pool).Error())
	node := &datamodel.Node{
		BaseModel: datamodel.BaseModel{UUID: "node-1"},
		Name:      "node-1",
		PoolID:    pool.ID,
		AccountID: account.ID,
	}
	require.NoError(t, store.db.Create(node).Error())
	lif := &datamodel.Lif{
		BaseModel: datamodel.BaseModel{UUID: "lif-1"},
		Name:      "svm1-ilbnas",
		IPAddress: "10.0.0.5",
		NodeID:    node.ID,
		AccountID: account.ID,
		LifDetails: &datamodel.LifDetails{
			ProtocolType: "nas",
		},
	}
	require.NoError(t, store.db.Create(lif).Error())

	updatedConfig := `{"svm":{"svm1":{"svm_lifs":{"ilbnas":[{"ip":"10.0.0.9","lif_name":"svm1-ilbnas"}]}}}}`
	err = store.ApplyPoolNasLifIPs(ctx, pool.UUID, updatedConfig, []commonparams.NasLifIPWrite{{
		UUID:      lif.UUID,
		IPAddress: "10.0.0.9",
	}})
	require.NoError(t, err)

	storedPool, err := store.GetPoolByUUID(ctx, pool.UUID)
	require.NoError(t, err)
	assert.Equal(t, updatedConfig, storedPool.VLMConfig)

	var storedLif datamodel.Lif
	require.NoError(t, store.db.Where("uuid = ?", lif.UUID).First(&storedLif).Error())
	assert.Equal(t, "10.0.0.9", storedLif.IPAddress)
}

func TestApplyPoolNasLifIPs_MissingLifRollsBackVLMConfig(t *testing.T) {
	ctx := context.Background()
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-1"}, Name: "account"}
	require.NoError(t, store.db.Create(account).Error())
	original := `{"svm":{}}`
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		Name:      "pool",
		AccountID: account.ID,
		VLMConfig: original,
	}
	require.NoError(t, store.db.Create(pool).Error())

	err = store.ApplyPoolNasLifIPs(ctx, pool.UUID, `{"svm":{"changed":true}}`, []commonparams.NasLifIPWrite{{
		UUID:      "missing-lif",
		IPAddress: "10.0.0.9",
	}})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &customErr)
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))

	storedPool, err := store.GetPoolByUUID(ctx, pool.UUID)
	require.NoError(t, err)
	assert.Equal(t, original, storedPool.VLMConfig)
}

func TestApplyPoolNasLifIPUpdates_PatchesLatestConfigForSequentialUpdates(t *testing.T) {
	ctx := context.Background()
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-1"}, Name: "account"}
	require.NoError(t, store.db.Create(account).Error())
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		Name:      "pool",
		AccountID: account.ID,
		VLMConfig: `{"svm":{"svm1":{"svm_lifs":{"ilbnas":[{"lif_name":"lif-a","ip":"10.0.0.5/32"},{"lif_name":"lif-b","ip":"10.0.0.6/32"}]}}}}`,
	}
	require.NoError(t, store.db.Create(pool).Error())
	node := &datamodel.Node{
		BaseModel: datamodel.BaseModel{UUID: "node-1"},
		Name:      "node-1",
		PoolID:    pool.ID,
		AccountID: account.ID,
	}
	require.NoError(t, store.db.Create(node).Error())
	lifs := []*datamodel.Lif{
		{
			BaseModel:  datamodel.BaseModel{UUID: "lif-row-a"},
			Name:       "lif-a",
			IPAddress:  "10.0.0.5",
			NodeID:     node.ID,
			AccountID:  account.ID,
			LifDetails: &datamodel.LifDetails{ProtocolType: "nas"},
		},
		{
			BaseModel:  datamodel.BaseModel{UUID: "lif-row-b"},
			Name:       "lif-b",
			IPAddress:  "10.0.0.6",
			NodeID:     node.ID,
			AccountID:  account.ID,
			LifDetails: &datamodel.LifDetails{ProtocolType: "nas"},
		},
	}
	for _, lif := range lifs {
		require.NoError(t, store.db.Create(lif).Error())
	}

	_, err = store.ApplyPoolNasLifIPUpdates(ctx, pool.UUID, []commonparams.NasLifIPUpdate{{
		LifName:   "lif-a",
		IPAddress: "10.0.0.50",
	}})
	require.NoError(t, err)
	_, err = store.ApplyPoolNasLifIPUpdates(ctx, pool.UUID, []commonparams.NasLifIPUpdate{{
		LifName:   "lif-b",
		IPAddress: "10.0.0.60",
	}})
	require.NoError(t, err)

	storedPool, err := store.GetPoolByUUID(ctx, pool.UUID)
	require.NoError(t, err)
	assert.Contains(t, storedPool.VLMConfig, `"lif_name":"lif-a"`)
	assert.Contains(t, storedPool.VLMConfig, `"ip":"10.0.0.50/32"`)
	assert.Contains(t, storedPool.VLMConfig, `"lif_name":"lif-b"`)
	assert.Contains(t, storedPool.VLMConfig, `"ip":"10.0.0.60/32"`)

	var storedLifs []datamodel.Lif
	require.NoError(t, store.db.GORM().Where("uuid IN ?", []string{"lif-row-a", "lif-row-b"}).Order("uuid").Find(&storedLifs).Error)
	require.Len(t, storedLifs, 2)
	assert.Equal(t, "10.0.0.50", storedLifs[0].IPAddress)
	assert.Equal(t, "10.0.0.60", storedLifs[1].IPAddress)
}

func Test_getLifsWithProtocolDetails_WithProtocol_PostgresMock(t *testing.T) {
	t.Run("Uses JSONBProtocolTypeExtract", func(tt *testing.T) {
		// Setup sqlmock
		dbSQL, mock, err := sqlmock.New()
		assert.NoError(tt, err)
		defer func() {
			if err := mock.ExpectationsWereMet(); err != nil {
				tt.Fatalf("there were unfulfilled expectations: %v", err)
			}
		}()

		// Open GORM with postgres driver using the sqlmock db
		dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
		gormDB, err := gorm.Open(dialector, &gorm.Config{})
		if err != nil {
			tt.Fatalf("failed to open gorm db: %v", err)
		}

		rows := sqlmock.NewRows([]string{"id", "name", "lif_details", "account_id", "node_id"}).
			AddRow(1, "lif1", []byte(`{"protocol_type":"nfs"}`), 100, 1)
		mock.ExpectQuery(`lif_details->>'protocol_type' =`).WillReturnRows(rows)

		// Build dbQuery without pre-adding the protocol Where clause; _getLifsWithProtocolDetails adds it
		dbQuery := gormDB.Model(&datamodel.Lif{})

		lifs, err := _getLifsWithProtocolDetails(dbQuery, "nfs")
		assert.NoError(tt, err)
		if assert.Equal(tt, 1, len(lifs)) {
			if lifs[0].LifDetails != nil {
				assert.Equal(tt, "nfs", lifs[0].LifDetails.ProtocolType)
			}
		}
	})
}

func Test_getLifsWithProtocolDetails_EmptyProtocol_SQLite(t *testing.T) {
	t.Run("ReturnsAllWhenProtocolEmpty", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		// Create a lif with protocol nfs
		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "lif-1-uuid"},
			Name:      "lif1",
			NodeID:    1,
			AccountID: 100,
			LifDetails: &datamodel.LifDetails{
				ProtocolType: "nfs",
			},
		}
		if err := store.db.Create(lif).Error(); err != nil {
			tt.Fatalf("Failed to create lif: %v", err)
		}

		// Call the function with empty protocol
		lifs, err := _getLifsWithProtocolDetails(store.db.GORM(), "")
		assert.NoError(tt, err)
		assert.GreaterOrEqual(tt, len(lifs), 1)
	})
}

func Test_getLifsWithProtocolDetails_DBError_PostgresMock(t *testing.T) {
	t.Run("DBErrorPropagated", func(tt *testing.T) {
		dbSQL, mock, err := sqlmock.New()
		assert.NoError(tt, err)
		defer func() {
			if err := mock.ExpectationsWereMet(); err != nil {
				tt.Fatalf("there were unfulfilled expectations: %v", err)
			}
		}()

		dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
		gormDB, err := gorm.Open(dialector, &gorm.Config{})
		if err != nil {
			tt.Fatalf("failed to open gorm db: %v", err)
		}

		// Simulate a DB error when the query is executed
		errorMsg := fmt.Errorf("simulated db failure")
		// Expect any SELECT from lifs to return an error
		mock.ExpectQuery("SELECT .* FROM .*lifs").WillReturnError(errorMsg)

		dbQuery := gormDB.Model(&datamodel.Lif{})
		query := &datamodel.Lif{NodeID: 1, AccountID: 100}
		lif, err := _getLifWithDetails(dbQuery, query)
		assert.Error(tt, err)
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Contains(tt, customErr.Unwrap().Error(), "simulated db failure")
			assert.Nil(tt, lif, "Expected nil lif")
		} else {
			tt.Fatalf("Expected CustomError, got %v", err)
		}
	})
}

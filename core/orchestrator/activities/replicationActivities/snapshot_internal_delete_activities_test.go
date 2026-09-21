package replicationActivities

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestDeleteSnapshotInONTAP_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	activity := InternalSnapshotsDeleteActivity{
		SE: database.NewMockStorage(t),
	}

	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &vsa.SnapshotListResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "uuid-123",
			Name:         "test-snapshot",
		},
		VolumeExternalUUID: "volume-uuid-123",
	}
	node := &models.Node{}
	var snapshotList []*common.SnapshotListResponse
	snapshotList = append(snapshotList, &common.SnapshotListResponse{
		Name:               snapshot.Name,
		ExternalUUID:       snapshot.ExternalUUID,
		VolumeExternalUUID: snapshot.VolumeExternalUUID,
	})
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
		SnapshotsFromOntap: snapshotList,
	}

	// Mock the DeleteSnapshot method
	mockProvider.On("DeleteSnapshot", snapshot.ExternalUUID, snapshot.VolumeExternalUUID).Return(nil)

	err := activity.DeleteSnapshotsInONTAP(ctx, params, node)

	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotInONTAP_Failure(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := InternalSnapshotsDeleteActivity{
		SE: database.NewMockStorage(t),
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &vsa.SnapshotListResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "uuid-123",
			Name:         "test-snapshot",
		},
		VolumeExternalUUID: "volume-uuid-123",
	}
	node := &models.Node{}
	var snapshotList []*common.SnapshotListResponse
	snapshotList = append(snapshotList, &common.SnapshotListResponse{
		Name:               snapshot.Name,
		ExternalUUID:       snapshot.ExternalUUID,
		VolumeExternalUUID: snapshot.VolumeExternalUUID,
	})
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
		SnapshotsFromOntap: snapshotList,
	}
	expectedError := errors.New("failed to delete snapshot in ONTAP")

	// Mock the DeleteSnapshot method
	mockProvider.On("DeleteSnapshot", snapshot.ExternalUUID, snapshot.VolumeExternalUUID).Return(expectedError)

	err := activity.DeleteSnapshotsInONTAP(ctx, params, node)

	assert.Error(t, err)
	assert.EqualError(t, err, expectedError.Error())
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotsInDB_Failure(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := InternalSnapshotsDeleteActivity{
		SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	snapshot1 := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot1",
	}
	Node1 := &datamodel.Node{EndpointAddress: "127.0.0.1"}

	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Nodes: []*datamodel.Node{Node1},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
		SnapshotsFromDB: []*datamodel.Snapshot{snapshot, snapshot1},
	}

	expectedError := errors.New("snapshot not found")

	mockStorage.On("DeleteSnapshot", ctx, snapshot.UUID).Return(nil, expectedError)

	err := activity.UpdateSnapshotRecordInDB(ctx, params)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapshotsInDB_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := InternalSnapshotsDeleteActivity{
		SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	Node1 := &datamodel.Node{EndpointAddress: "127.0.0.1"}

	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Nodes: []*datamodel.Node{Node1},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
		SnapshotsFromDB: []*datamodel.Snapshot{snapshot},
	}

	expectedSnapshot := &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshot.UUID}}

	mockStorage.On("DeleteSnapshot", ctx, snapshot.UUID).Return(expectedSnapshot, nil)

	err := activity.UpdateSnapshotRecordInDB(ctx, params)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

// Test for SnapshotsDehydration
func TestSnapshotsDehydration(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &InternalSnapshotsDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	Node1 := &datamodel.Node{EndpointAddress: "127.0.0.1"}

	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Nodes: []*datamodel.Node{Node1},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
		SnapshotsFromDB: []*datamodel.Snapshot{snapshot},
	}

	// Mock the dehydration function to succeed
	originalDehydration := hydrateBatchSnapshotsToCCFE
	defer func() { hydrateBatchSnapshotsToCCFE = originalDehydration }()
	hydrateBatchSnapshotsToCCFE = func(ctx context.Context, createdSnapshots []*datamodel.Snapshot, deletedSnapshots []*datamodel.Snapshot) error {
		return nil
	}

	err := activity.DehydrateSnapshots(ctx, params)
	assert.NoError(t, err)
}

func TestSnapshotsDehydration_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &InternalSnapshotsDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	hydrationEnabled = true
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	Node1 := &datamodel.Node{EndpointAddress: "127.0.0.1"}

	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Nodes: []*datamodel.Node{Node1},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
		SnapshotsFromDB: []*datamodel.Snapshot{snapshot},
	}

	// Mock the dehydration function to return an error
	originalDehydration := hydrateBatchSnapshotsToCCFE
	defer func() {
		hydrateBatchSnapshotsToCCFE = originalDehydration
		hydrationEnabled = false
	}()
	hydrateBatchSnapshotsToCCFE = func(ctx context.Context, createdSnapshots []*datamodel.Snapshot, deletedSnapshots []*datamodel.Snapshot) error {
		return fmt.Errorf("dehydration failed")
	}

	err := activity.DehydrateSnapshots(ctx, params)
	assert.Error(t, err)
	var customErr *errors2.CustomError
	assert.True(t, errors2.As(err, &customErr), "Expected a CustomError")
	assert.ErrorContains(t, customErr.OriginalErr, "dehydration failed")
}

func TestInternalSnapshotsDeleteActivity_ListSnapshotFromDB(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &InternalSnapshotsDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	Node1 := &datamodel.Node{EndpointAddress: "127.0.0.1"}

	params := &common.SnapshotsInternalDeleteParams{
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    "test-volume-uuid",
			AccountName: "test_account",
		},
		Nodes: []*datamodel.Node{Node1},
		Volume: &datamodel.Volume{
			Pool:             &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: int64(1)}, PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
			Svm:              &datamodel.Svm{Name: "svm_test"},
			VolumeAttributes: &datamodel.VolumeAttributes{BlockProperties: &datamodel.BlockProperties{OSType: "LINUX"}},
		},
		SnapshotsFromDB: []*datamodel.Snapshot{snapshot},
	}

	expectedSnapshots := []*datamodel.Snapshot{snapshot}
	mockStorage.On("GetReplicationSnapshotsByVolumeID", ctx, mock.Anything).Return(expectedSnapshots, nil)

	out, err := activity.ListSnapshotFromDB(ctx, params)
	assert.NoError(t, err)
	assert.Equal(t, expectedSnapshots, out.SnapshotsFromDB)
	mockStorage.AssertExpectations(t)

	// Test error path
	mockStorage2 := database.NewMockStorage(t)
	activity2 := &InternalSnapshotsDeleteActivity{SE: mockStorage2}
	mockStorage2.On("GetReplicationSnapshotsByVolumeID", ctx, mock.Anything).Return(nil, fmt.Errorf("db error"))
	_, err = activity2.ListSnapshotFromDB(ctx, params)
	assert.Error(t, err)
}

func TestInternalSnapshotsDeleteActivity_GetNodeFromDB(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &InternalSnapshotsDeleteActivity{SE: mockStorage}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	params := &common.SnapshotsInternalDeleteParams{
		Volume: &datamodel.Volume{PoolID: 42},
	}

	node := &datamodel.Node{}
	mockStorage.On("GetNodesByPoolID", ctx, int64(42)).Return([]*datamodel.Node{node}, nil)

	out, err := activity.GetNodeFromDB(ctx, params)
	assert.NoError(t, err)
	assert.Equal(t, node, out.Nodes[0])
	mockStorage.AssertExpectations(t)

	// Test: no nodes found
	mockStorage2 := database.NewMockStorage(t)
	activity2 := &InternalSnapshotsDeleteActivity{SE: mockStorage2}
	mockStorage2.On("GetNodesByPoolID", ctx, int64(42)).Return([]*datamodel.Node{}, nil)
	_, err = activity2.GetNodeFromDB(ctx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no node found")

	// Test: error from storage
	mockStorage3 := database.NewMockStorage(t)
	activity3 := &InternalSnapshotsDeleteActivity{SE: mockStorage3}
	mockStorage3.On("GetNodesByPoolID", ctx, int64(42)).Return(nil, fmt.Errorf("db error"))
	_, err = activity3.GetNodeFromDB(ctx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestListSnapshotInONTAP_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	activity := InternalSnapshotsDeleteActivity{
		SE: database.NewMockStorage(t),
	}

	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &vsa.SnapshotListResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "uuid-123",
			Name:         "test-snapshot",
		},
		VolumeExternalUUID: "volume-uuid-123",
	}
	snapshotsList := []*vsa.SnapshotListResponse{
		snapshot,
	}
	node := &models.Node{}
	var snapshotList []*common.SnapshotListResponse
	snapshotList = append(snapshotList, &common.SnapshotListResponse{
		Name:               snapshot.Name,
		ExternalUUID:       snapshot.ExternalUUID,
		VolumeExternalUUID: snapshot.VolumeExternalUUID,
	})
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
		SnapshotsFromOntap: snapshotList,
	}

	// Mock the DeleteSnapshot method
	mockProvider.On("ListSnapmirrorSnapshots", mock.Anything).Return(snapshotsList, nil)

	res, err := activity.ListSnapshotInONTAP(ctx, params, node)
	assert.NoError(t, err)
	assert.Len(t, res.SnapshotsFromOntap, 1)
	mockProvider.AssertExpectations(t)
}

func TestListSnapshotInONTAP_Failure(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := InternalSnapshotsDeleteActivity{
		SE: database.NewMockStorage(t),
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	snapshot := &vsa.SnapshotListResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "uuid-123",
			Name:         "test-snapshot",
		},
		VolumeExternalUUID: "volume-uuid-123",
	}
	node := &models.Node{}
	var snapshotList []*common.SnapshotListResponse
	snapshotList = append(snapshotList, &common.SnapshotListResponse{
		Name:               snapshot.Name,
		ExternalUUID:       snapshot.ExternalUUID,
		VolumeExternalUUID: snapshot.VolumeExternalUUID,
	})

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
		SnapshotsFromOntap: snapshotList,
	}
	expectedError := errors.New("failed to delete snapshot in ONTAP")

	// Mock the DeleteSnapshot method
	mockProvider.On("ListSnapmirrorSnapshots", mock.Anything).Return(nil, expectedError)

	_, err := activity.ListSnapshotInONTAP(ctx, params, node)

	assert.Error(t, err)
	assert.EqualError(t, err, expectedError.Error())
	mockProvider.AssertExpectations(t)
}

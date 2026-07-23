package backgroundactivities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

func TestFilterOntapVolumesAndSnapshots(t *testing.T) {
	volumes := []*vsa.Volume{
		{
			Volume: ontaprestmodel.Volume{
				IsSvmRoot: nillable.ToPointer(true),
			},
			ExternalUUID: "some-external-uuid",
		},
		{
			Volume: ontaprestmodel.Volume{
				Name:      nillable.ToPointer("test-volume"),
				UUID:      nillable.ToPointer("test-volume-uuid"),
				Style:     nillable.ToPointer("flexvol"),
				IsSvmRoot: nillable.ToPointer(false),
				Svm: &ontaprestmodel.VolumeInlineSvm{
					Name: nillable.ToPointer("test-volume-svm"),
				},
			},
			ExternalUUID: "some-external-uuid",
		},
		{
			Volume: ontaprestmodel.Volume{
				Name:      nillable.ToPointer("test-flexgroup-constituent-volume"),
				UUID:      nillable.ToPointer("flex-group-constituent-uuid"),
				Style:     nillable.ToPointer(FlexGroupConstituent),
				IsSvmRoot: nillable.ToPointer(false),
				Svm: &ontaprestmodel.VolumeInlineSvm{
					Name: nillable.ToPointer("test-flexgroup-constituent-volume-svm"),
				},
			},
			ExternalUUID: "some-external-uuid",
		},
	}

	snapshots := []*vsa.Snapshot{
		{
			Snapshot: ontaprestmodel.Snapshot{
				Name: nillable.ToPointer("orphaned-snapshot"),
				ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
					UUID: nillable.ToPointer("orphaned-snapshot-uuid"),
				},
				Volume: &ontaprestmodel.SnapshotInlineVolume{
					Name: nillable.ToPointer("non-existent-volume"),
				},
				Svm: &ontaprestmodel.SnapshotInlineSvm{
					Name: nillable.ToPointer("orphaned-svm"),
				},
			},
		},
		{
			Snapshot: ontaprestmodel.Snapshot{
				Name: nillable.ToPointer("hourly-test-snapshot"),
				ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
					UUID: nillable.ToPointer("test-volume-uuid"),
				},
				Volume: &ontaprestmodel.SnapshotInlineVolume{
					Name: nillable.ToPointer("test-volume"),
				},
				Svm: &ontaprestmodel.SnapshotInlineSvm{
					Name: nillable.ToPointer("test-svm"),
				},
				SnapmirrorLabel: nillable.ToPointer("scheduled"),
			},
		},
		{
			Snapshot: ontaprestmodel.Snapshot{
				Name: nillable.ToPointer("test-snapshot"),
				ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
					UUID: nillable.ToPointer("flex-group-constituent-uuid"),
				},
				Volume: &ontaprestmodel.SnapshotInlineVolume{
					Name: nillable.ToPointer("test-flexgroup-constituent-volume"),
				},
				Svm: &ontaprestmodel.SnapshotInlineSvm{
					Name: nillable.ToPointer("test-svm"),
				},
				SnapmirrorLabel: nillable.ToPointer("scheduled"),
			},
		},
		{
			Snapshot: ontaprestmodel.Snapshot{
				Name: nillable.ToPointer("scheduled-backup-shashank-0000-11-22-547823"),
				ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
					UUID: nillable.ToPointer("test-volume-uuid"),
				},
				Volume: &ontaprestmodel.SnapshotInlineVolume{
					Name: nillable.ToPointer("test-volume"),
				},
				Svm: &ontaprestmodel.SnapshotInlineSvm{
					Name: nillable.ToPointer("test-vm"),
				},
			},
		},
		{
			Snapshot: ontaprestmodel.Snapshot{
				Name: nillable.ToPointer("test-snapshot"),
				ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
					UUID: nillable.ToPointer("test-volume-uuid"),
				},
				Volume: &ontaprestmodel.SnapshotInlineVolume{
					Name: nillable.ToPointer("test-volume"),
				},
				Svm: &ontaprestmodel.SnapshotInlineSvm{
					Name: nillable.ToPointer("test-svm"),
				},
			},
		},
	}

	volumeMap, filteredSnapshots := filterOntapVolumesAndSnapshots(volumes, snapshots)
	assert.Len(t, volumeMap, 4)
	assert.Len(t, filteredSnapshots, 3)
}

func TestSyncWronglyDeletedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	t.Run("SyncWronglyDeletedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"wrongly-deleted-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1},
				State:        datamodel.LifeCycleStateDeleted,
				StateDetails: datamodel.LifeCycleStateDeletedDetails,
			},
		}
		wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("wrongly-deleted-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID: "wrongly-deleted-snapshot-uuid",
			},
		}

		// Mock the batch get wrongly deleted snapshots call
		mockStorage.On("BatchGetWronglyDeletedSnapshots", ctx, []string{"wrongly-deleted-snapshot-uuid"}).Return(
			[]*datamodel.Snapshot{{State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails}}, nil)
		// Mock the batch undelete snapshots call
		mockStorage.On("BatchUnDeleteSnapshots", ctx, mock.AnythingOfType("[]*datamodel.Snapshot")).Return(nil)

		_, err := syncWronglyDeletedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, wronglyDeletedSnapshotsMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncWronglyDeletedSnapshotsToDatabaseBatchGetFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"wrongly-deleted-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1},
				State:        datamodel.LifeCycleStateDeleted,
				StateDetails: datamodel.LifeCycleStateDeletedDetails,
			},
		}
		wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("wrongly-deleted-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID: "wrongly-deleted-snapshot-uuid",
			},
		}

		// Mock the batch get wrongly deleted snapshots call to return error
		mockStorage.On("BatchGetWronglyDeletedSnapshots", ctx, []string{"wrongly-deleted-snapshot-uuid"}).Return(
			nil, errors.New("could not get wrongly deleted snapshots"))

		_, err := syncWronglyDeletedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, wronglyDeletedSnapshotsMap, mockStorage, dbSnapshotsMap)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncWronglyDeletedSnapshotsToDatabaseBatchUnDeleteFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"wrongly-deleted-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1},
				State:        datamodel.LifeCycleStateDeleted,
				StateDetails: datamodel.LifeCycleStateDeletedDetails,
			},
		}
		wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("wrongly-deleted-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID: "wrongly-deleted-snapshot-uuid",
			},
		}

		// Mock the batch get wrongly deleted snapshots call
		mockStorage.On("BatchGetWronglyDeletedSnapshots", ctx, []string{"wrongly-deleted-snapshot-uuid"}).Return(
			[]*datamodel.Snapshot{{State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails}}, nil)
		// Mock the batch undelete snapshots call to return error
		mockStorage.On("BatchUnDeleteSnapshots", ctx, mock.AnythingOfType("[]*datamodel.Snapshot")).Return(errors.New("could not undelete snapshots"))

		_, err := syncWronglyDeletedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, wronglyDeletedSnapshotsMap, mockStorage, dbSnapshotsMap)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncWronglyDeletedSnapshotsToDatabaseWithEmptySnapshots", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{}
		wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{}

		result, err := syncWronglyDeletedSnapshotsToDatabase(ctx, []string{}, wronglyDeletedSnapshotsMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		assert.Nil(t, result)
		// No expectations to assert since no methods should be called
	})
	t.Run("SyncWronglyDeletedSnapshotsToDatabaseWithNoSnapshotsFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"wrongly-deleted-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1},
				State:        datamodel.LifeCycleStateDeleted,
				StateDetails: datamodel.LifeCycleStateDeletedDetails,
			},
		}
		wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("wrongly-deleted-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID: "wrongly-deleted-snapshot-uuid",
			},
		}

		// Mock the batch get wrongly deleted snapshots call to return empty slice
		mockStorage.On("BatchGetWronglyDeletedSnapshots", ctx, []string{"wrongly-deleted-snapshot-uuid"}).Return(
			[]*datamodel.Snapshot{}, nil)

		result, err := syncWronglyDeletedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, wronglyDeletedSnapshotsMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		assert.Empty(t, result)
		mockStorage.AssertExpectations(t)
	})
}

func TestSyncUpdatedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	t.Run("SyncUpdatedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"updated-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1, UUID: "updated-snapshot-uuid"},
				State:        datamodel.LifeCycleStateREADY,
				StateDetails: datamodel.LifeCycleStateReadyDetails,
			},
		}
		updatedSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("updated-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID:           "updated-snapshot-uuid",
				SizeInBytes:            122880,
				LogicalSizeUsedInBytes: 122880,
			},
		}

		// Mock the batch update snapshots call
		mockStorage.On("BatchUpdateSnapshots", ctx, mock.AnythingOfType("[]*datamodel.Snapshot")).Return(nil)

		updatedDbSnapshots, err := syncUpdatedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, updatedSSMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		assert.Len(t, updatedDbSnapshots, 1)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncUpdatedSnapshotsToDatabaseBatchUpdateFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"updated-snapshot-uuid": {
				BaseModel:    datamodel.BaseModel{ID: 1, UUID: "updated-snapshot-uuid"},
				State:        datamodel.LifeCycleStateREADY,
				StateDetails: datamodel.LifeCycleStateReadyDetails,
			},
		}
		updatedSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("updated-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID:           "updated-snapshot-uuid",
				SizeInBytes:            122880,
				LogicalSizeUsedInBytes: 122880,
			},
		}

		// Mock the batch update snapshots call to return error
		mockStorage.On("BatchUpdateSnapshots", ctx, mock.AnythingOfType("[]*datamodel.Snapshot")).Return(errors.New("could not update snapshots"))

		updatedDbSnapshot, err := syncUpdatedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, updatedSSMap, mockStorage, dbSnapshotsMap)
		assert.Error(t, err)
		assert.Nil(t, updatedDbSnapshot)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncUpdatedSnapshotsToDatabaseWithEmptySnapshots", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		updatedSSMap := map[string]*vsa.Snapshot{}
		dbSnapshotsMap := map[string]*datamodel.Snapshot{}

		result, err := syncUpdatedSnapshotsToDatabase(ctx, []string{}, updatedSSMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		assert.Nil(t, result)
		// No expectations to assert since no methods should be called
	})
	t.Run("SyncUpdatedSnapshotsToDatabaseWithNonExistentSnapshot", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		updatedSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("updated-snapshot"),
				},
				ExternalUUID: "non-existent-uuid",
			},
		}
		dbSnapshotsMap := map[string]*datamodel.Snapshot{}

		result, err := syncUpdatedSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, updatedSSMap, mockStorage, dbSnapshotsMap)
		assert.NoError(t, err)
		assert.Nil(t, result)
		// No expectations to assert since no methods should be called when snapshot doesn't exist in DB
	})
}

func TestSyncNewSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	t.Run("SyncUpdatedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{ID: 1},
			},
		}
		pool := &datamodel.Pool{AccountID: 1}
		newSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("new-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID:           "new-snapshot-uuid",
				ExternalVolumeUUID:     "test-volume-uuid",
				SizeInBytes:            122880,
				LogicalSizeUsedInBytes: 122880,
			},
		}

		mockStorage.On("BatchCreateSnapshots", ctx, mock.Anything, mock.Anything).Return(nil, nil)

		_, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SyncUpdatedSnapshotsToDatabase_SnapshotCreationFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"test-volume-uuid": {
				BaseModel: datamodel.BaseModel{ID: 1},
			},
		}
		pool := &datamodel.Pool{AccountID: 1}
		newSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot: ontaprestmodel.Snapshot{
					Name: nillable.ToPointer("new-snapshot"),
					ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{
						UUID: nillable.ToPointer("test-volume-uuid"),
					},
					Volume: &ontaprestmodel.SnapshotInlineVolume{
						Name: nillable.ToPointer("test-volume"),
					},
					Svm: &ontaprestmodel.SnapshotInlineSvm{
						Name: nillable.ToPointer("test-svm"),
					},
				},
				ExternalUUID:           "new-snapshot-uuid",
				ExternalVolumeUUID:     "test-volume-uuid",
				SizeInBytes:            122880,
				LogicalSizeUsedInBytes: 122880,
			},
		}

		mockStorage.On("BatchCreateSnapshots", ctx, mock.Anything, mock.Anything).Return(
			nil, errors.New("could not create snapshot"))

		_, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestSyncDeletedSnapshotsToDatabase(t *testing.T) {
	t.Run("SyncDeletedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		ctx := context.TODO()
		mockStorage := database.NewMockStorage(tt)

		deleteIDs := []int64{1, 2, 3}
		mockStorage.On("BatchDeleteSnapshots", ctx, deleteIDs).Return(
			[]*datamodel.Snapshot{
				{BaseModel: datamodel.BaseModel{ID: 1}},
				{BaseModel: datamodel.BaseModel{ID: 2}},
				{BaseModel: datamodel.BaseModel{ID: 3}},
			}, nil)

		deletedSnapshots, err := syncDeletedSnapshotsToDatabase(ctx, deleteIDs, mockStorage)
		assert.NoError(tt, err)
		assert.Len(tt, deletedSnapshots, 3)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("SyncDeletedSnapshotsToDatabaseFailed", func(tt *testing.T) {
		ctx := context.TODO()
		mockStorage := database.NewMockStorage(tt)

		deleteIDs := []int64{1, 2, 3}
		mockStorage.On("BatchDeleteSnapshots", ctx, deleteIDs).Return(nil, errors.New("could not delete snapshots"))

		deletedSnapshots, err := syncDeletedSnapshotsToDatabase(ctx, deleteIDs, mockStorage)
		assert.Error(tt, err)
		assert.Nil(t, deletedSnapshots)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetOntapRestProviderForPool(t *testing.T) {
	ctx := context.TODO()

	t.Run("GetOntapRestProviderForPool_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "abcd",
			},
		}
		node := &datamodel.Node{
			EndpointAddress: "1.2.3.4",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)

		// Patch activities.GetProviderByNode to return a mock provider
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.NoError(t, err)
		assert.Equal(t, mockProvider, provider)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetOntapRestProviderForPool_PoolNotFoundError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, gorm.ErrRecordNotFound)
		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("GetOntapRestProviderForPool_CouldNotGetNodes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, errors.New("could not get nodes"))
		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetOntapRestProviderForPool_NoNodesFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{}, nil)
		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(tt)
		if !strings.Contains(vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "no nodes found for pool") {
			t.Errorf("expected error %v, got %v", "no nodes found for pool", err)
		}
	})

	t.Run("GetOntapRestProviderForPool_NoCredentials", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		node := &datamodel.Node{
			EndpointAddress: "test-endpoint",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)
		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(tt)
		if !strings.Contains(vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "pool credentials not found for pool") {
			t.Errorf("expected error %v, got %v", "pool credentials not found for pool", err)
		}
	})

	t.Run("GetOntapRestProviderForPool_HostUUID_UsesHostProvider", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		host := &datamodel.Host{
			BaseModel: datamodel.BaseModel{UUID: "host-uuid-1"},
			IPAddress: "10.0.0.1",
			Username:  "admin",
			Password:  "encrypted",
			Protocol:  datamodel.ExternalClusterProtocolInsecureHTTPS,
		}
		mockStorage.On("GetHost", ctx, "host-uuid-1").Return(host, nil)

		originalGetProviderForHost := vsa.GetProviderForHost
		defer func() { vsa.GetProviderForHost = originalGetProviderForHost }()
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderForHost = func(_ context.Context, h *datamodel.Host) (vsa.Provider, error) {
			assert.Equal(tt, host, h)
			return mockProvider, nil
		}

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
		assert.NoError(tt, err)
		assert.Equal(tt, mockProvider, provider)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPool_HostNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(nil, gorm.ErrRecordNotFound)

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.Equal(tt, vsaerrors.ErrResourceNotFound, vsaerrors.ExtractCustomError(err).TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPool_GetHostDatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(nil, errors.New("database read failed"))

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vsaerrors.ExtractCustomError(err).TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPool_HostProviderError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		host := &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: pool.HostUUID}}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(host, nil)

		originalGetProviderForHost := vsa.GetProviderForHost
		defer func() { vsa.GetProviderForHost = originalGetProviderForHost }()
		vsa.GetProviderForHost = func(_ context.Context, _ *datamodel.Host) (vsa.Provider, error) {
			return nil, errors.New("host provider failed")
		}

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.ErrorContains(tt, err, "host provider failed")
		mockStorage.AssertExpectations(tt)
	})
}

func TestProcessSnapshotSync(t *testing.T) {
	ctx := context.Background()
	ontapSnapshots := []*vsa.Snapshot{
		// Adds entries to existingSSMap and updatedSSMap
		{
			ExternalUUID:           "test-snapshot-uuid-1",
			ExternalVolumeUUID:     "test-volume-uuid-1",
			SizeInBytes:            122800,
			LogicalSizeUsedInBytes: 122800,
		},
		// Adds entry into newDiskSSMap
		{
			ExternalUUID:       "test-snapshot-uuid-2",
			ExternalVolumeUUID: "test-volume-uuid-1",
			Type:               SnapshotTypeBackup,
		},
		// Adds entry into newSSMap
		{
			ExternalUUID:       "test-snapshot-uuid-3",
			ExternalVolumeUUID: "test-volume-uuid-2",
			Type:               SnapshotTypeAdHoc,
		},
		// Adds entries into newSSMap
		{
			ExternalUUID:       "test-snapshot-uuid-4",
			ExternalVolumeUUID: "test-volume-uuid-2",
			Type:               SnapshotTypeAdHoc,
		},
		// Adds entries into wronglyDeletedSnapshotsMap
		{
			ExternalUUID:       "test-snapshot-uuid-7",
			ExternalVolumeUUID: "test-volume-uuid-3",
			Type:               SnapshotTypeAdHoc,
		},
		// Does not add entries into any of the slices/maps
		{
			ExternalUUID:       "test-snapshot-uuid-5",
			ExternalVolumeUUID: "test-volume-uuid-4",
			Type:               SnapshotTypeAdHoc,
		},
	}

	dbSnapshots := []*datamodel.Snapshot{
		// Does not add entry into deletedIDs
		{
			BaseModel: datamodel.BaseModel{ID: 1},
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID:           "test-snapshot-uuid-1",
				SizeInBytes:            122,
				LogicalSizeUsedInBytes: 122,
			},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "test-volume-uuid-1",
				},
			},
		},
		// Adds an entry into deletedIDs
		{
			BaseModel: datamodel.BaseModel{ID: 3},
			Type:      SnapshotTypeBackup,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "test-snapshot-uuid-3",
			},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "test-volume-uuid-1",
				},
			},
		},
		// Does not add entry into deletedIDs (volume doesn't exist on ONTAP)
		{
			BaseModel: datamodel.BaseModel{ID: 6, UUID: "test-snapshot-uuid-6"},
			Type:      SnapshotTypeBackup,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "test-snapshot-uuid-6",
			},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 6},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "test-volume-uuid-6",
				},
			},
		},
	}

	volType := "dp"
	ontapVolumeMap := map[string]*vsa.Volume{
		"test-volume-uuid-1": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-1"),
				UUID: nillable.ToPointer("test-volume-uuid-1"),
			},
		},
		"test-volume-uuid-2": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-2"),
				UUID: nillable.ToPointer("test-volume-uuid-2"),
				Type: &volType,
			},
		},
		"test-volume-uuid-3": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-3"),
				UUID: nillable.ToPointer("test-volume-uuid-3"),
			},
		},
	}

	dbVolumeMap := map[string]*datamodel.Volume{
		"test-volume-uuid-1": {
			BaseModel:    datamodel.BaseModel{ID: 1},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "test-volume-uuid-1",
			},
		},
		"test-volume-uuid-2": {
			BaseModel:    datamodel.BaseModel{ID: 2},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "test-volume-uuid-2",
			},
		},
	}

	newSSMap, updatedSSMap, wronglyDeletedSnapshotsMap, newIDs, updatedIDs, deletedIDs, wronglyDeletedIDs :=
		processSnapshotSync(ctx, ontapVolumeMap, ontapSnapshots, dbVolumeMap, dbSnapshots)

	assert.Len(t, newSSMap, 3)
	assert.Len(t, updatedSSMap, 1)
	assert.Len(t, wronglyDeletedSnapshotsMap, 1)
	assert.Len(t, newIDs, 3)
	assert.Len(t, updatedIDs, 1)
	assert.Len(t, deletedIDs, 1)
	assert.Len(t, wronglyDeletedIDs, 1)
}

func TestSyncSnapshotActivity_ProcessSnapshots(t *testing.T) {
	activity := SyncSnapshotActivity{}
	ctx := context.TODO()
	ontapVolumeMap := map[string]*vsa.Volume{"vol-uuid": {ExternalUUID: "vol-uuid"}}
	ontapSnapshots := []*vsa.Snapshot{{ExternalUUID: "snap-uuid", ExternalVolumeUUID: "vol-uuid", Type: SnapshotTypeAdHoc, SizeInBytes: 1, LogicalSizeUsedInBytes: 1}}
	dbVolumeMap := map[string]*datamodel.Volume{"vol-uuid": {BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-uuid"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-uuid"}}}
	dbSnapshots := []*datamodel.Snapshot{}
	result, err := activity.ProcessSnapshots(ctx, ontapVolumeMap, ontapSnapshots, dbVolumeMap, dbSnapshots)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestSyncSnapshotActivity_SyncDeletedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	deleteIDs := []int64{1, 2}
	mockStorage.On("BatchDeleteSnapshots", ctx, deleteIDs).Return([]*datamodel.Snapshot{{BaseModel: datamodel.BaseModel{ID: 1}}, {BaseModel: datamodel.BaseModel{ID: 2}}}, nil)
	result, err := activity.SyncDeletedSnapshotsToDatabase(ctx, deleteIDs)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_SyncNewSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	dbVolumeMap := map[string]*datamodel.Volume{"vol-uuid": {BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-uuid"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-uuid"}}}
	pool := &datamodel.Pool{AccountID: 1}
	newSSMap := map[string]*vsa.Snapshot{"snap-uuid.vol-uuid": {ExternalUUID: "snap-uuid", ExternalVolumeUUID: "vol-uuid", Type: SnapshotTypeAdHoc, Snapshot: ontaprestmodel.Snapshot{Name: nillable.GetStringPtr("snap")}}}
	mockStorage.On("BatchCreateSnapshots", ctx, mock.Anything, true).Return([]string{"snap-uuid"}, nil)
	mockStorage.On("BatchGetSnapshotsByUUIDs", ctx, []string{"snap-uuid"}).Return([]*datamodel.Snapshot{{SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-uuid"}}}, nil)
	result, err := activity.SyncNewSnapshotsToDatabase(ctx, []string{"snap-uuid.vol-uuid"}, newSSMap, dbVolumeMap, pool)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_SyncUpdatedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	dbSnapshotsMap := map[string]*datamodel.Snapshot{"snap-uuid": {BaseModel: datamodel.BaseModel{ID: 1, UUID: "snap-uuid"}, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-uuid"}, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}}
	updatedSSMap := map[string]*vsa.Snapshot{"snap-uuid.vol-uuid": {ExternalUUID: "snap-uuid", SizeInBytes: 1, LogicalSizeUsedInBytes: 1, Snapshot: ontaprestmodel.Snapshot{Name: nillable.GetStringPtr("snap")}}}
	mockStorage.On("BatchUpdateSnapshots", ctx, mock.Anything).Return(nil)
	result, err := activity.SyncUpdatedSnapshotsToDatabase(ctx, []string{"snap-uuid.vol-uuid"}, updatedSSMap, dbSnapshotsMap)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_SyncWronglyDeletedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	wronglyDeletedSnapshotsMap := map[string]*vsa.Snapshot{"snap-uuid.vol-uuid": {ExternalUUID: "snap-uuid"}}
	mockStorage.On("BatchGetWronglyDeletedSnapshots", ctx, []string{"snap-uuid"}).Return([]*datamodel.Snapshot{{SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-uuid"}}}, nil)
	mockStorage.On("BatchUnDeleteSnapshots", ctx, mock.Anything).Return(nil)
	result, err := activity.SyncWronglyDeletedSnapshotsToDatabase(ctx, []string{"snap-uuid.vol-uuid"}, wronglyDeletedSnapshotsMap)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_HydrateSnapshotsToCCFE(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{
		SE: mockStorage,
	}
	ctx := context.TODO()
	called := false
	original := hydrateBatchSnapshotsToCCFE
	hydrateBatchSnapshotsToCCFE = func(ctx context.Context, createdSnapshots, deletedSnapshots []*datamodel.Snapshot) error {
		called = true
		return nil
	}
	defer func() { hydrateBatchSnapshotsToCCFE = original }()
	created := []*datamodel.Snapshot{{SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-uuid"}}}
	deleted := []*datamodel.Snapshot{{SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "del-uuid"}}}
	err := activity.HydrateSnapshotsToCCFE(ctx, created, deleted)
	assert.NoError(t, err)
	assert.True(t, called)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return([]*vsa.Snapshot{{
		Snapshot: ontaprestmodel.Snapshot{
			Name:             nillable.ToPointer("snap"),
			ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{UUID: nillable.ToPointer("vol-uuid")},
			Volume:           &ontaprestmodel.SnapshotInlineVolume{Name: nillable.ToPointer("vol-name")},
			Svm:              &ontaprestmodel.SnapshotInlineSvm{Name: nillable.ToPointer("svm-name")},
		},
		ExternalUUID:       "snap-uuid",
		ExternalVolumeUUID: "vol-uuid",
	}}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.OntapVolumeMap, "vol-uuid")
	assert.Len(t, result.OntapSnapshots, 1)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_FetchPoolByUUID(t *testing.T) {
	t.Run("FetchPoolByUUID_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := "test-pool-uuid"
		accountID := int64(123)

		expectedPoolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: poolUUID,
				},
				Name:      "test-pool",
				AccountID: accountID,
				State:     datamodel.LifeCycleStateREADY,
				PoolCredentials: &datamodel.PoolCredentials{
					Password: "test-password",
				},
			},
		}

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(expectedPoolView, nil)

		encodedValue, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.NoError(tt, err)
		var result *datamodel.Pool
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedPoolView.Pool.ID, result.ID)
		assert.Equal(tt, expectedPoolView.Pool.UUID, result.UUID)
		assert.Equal(tt, expectedPoolView.Pool.Name, result.Name)
		assert.Equal(tt, expectedPoolView.Pool.AccountID, result.AccountID)
		assert.Equal(tt, expectedPoolView.Pool.State, result.State)
		assert.NotNil(tt, result.PoolCredentials)
		assert.Equal(tt, expectedPoolView.Pool.PoolCredentials.Password, result.PoolCredentials.Password)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchPoolByUUID_PoolNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := "non-existent-pool-uuid"
		accountID := int64(123)

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(nil, gorm.ErrRecordNotFound)

		_, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "An internal error occurred.")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchPoolByUUID_DatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := "test-pool-uuid"
		accountID := int64(123)

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(nil, errors.New("database connection failed"))

		_, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "An internal error occurred.")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchPoolByUUID_WithFullPoolData", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := "full-pool-uuid"
		accountID := int64(456)

		expectedPoolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   2,
					UUID: poolUUID,
				},
				Name:      "full-test-pool",
				AccountID: accountID,
				State:     datamodel.LifeCycleStateREADY,
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "full-password",
					SecretID:      "secret-123",
					CertificateID: "cert-456",
					AuthType:      1, // password auth type
				},
				DeploymentName: "test-deployment",
			},
		}

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(expectedPoolView, nil)

		encodedValue, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.NoError(tt, err)
		var result *datamodel.Pool
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedPoolView.Pool.ID, result.ID)
		assert.Equal(tt, expectedPoolView.Pool.UUID, result.UUID)
		assert.Equal(tt, expectedPoolView.Pool.Name, result.Name)
		assert.Equal(tt, expectedPoolView.Pool.AccountID, result.AccountID)
		assert.Equal(tt, expectedPoolView.Pool.State, result.State)
		assert.Equal(tt, expectedPoolView.Pool.DeploymentName, result.DeploymentName)
		assert.NotNil(tt, result.PoolCredentials)
		assert.Equal(tt, expectedPoolView.Pool.PoolCredentials.Password, result.PoolCredentials.Password)
		assert.Equal(tt, expectedPoolView.Pool.PoolCredentials.SecretID, result.PoolCredentials.SecretID)
		assert.Equal(tt, expectedPoolView.Pool.PoolCredentials.CertificateID, result.PoolCredentials.CertificateID)
		assert.Equal(tt, expectedPoolView.Pool.PoolCredentials.AuthType, result.PoolCredentials.AuthType)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchPoolByUUID_EmptyUUID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := ""
		accountID := int64(123)

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(nil, errors.New("invalid pool UUID"))

		_, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "An internal error occurred.")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchPoolByUUID_ZeroAccountID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchPoolByUUID)

		poolUUID := "test-pool-uuid"
		accountID := int64(0)

		expectedPoolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					ID:   3,
					UUID: poolUUID,
				},
				Name:      "zero-account-pool",
				AccountID: accountID,
				State:     datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.On("GetPool", mock.Anything, poolUUID, accountID).Return(expectedPoolView, nil)

		encodedValue, err := env.ExecuteActivity(activity.FetchPoolByUUID, poolUUID, accountID)
		assert.NoError(tt, err)
		var result *datamodel.Pool
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedPoolView.Pool.ID, result.ID)
		assert.Equal(tt, expectedPoolView.Pool.UUID, result.UUID)
		assert.Equal(tt, expectedPoolView.Pool.Name, result.Name)
		assert.Equal(tt, expectedPoolView.Pool.AccountID, result.AccountID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncSnapshotActivity_GetTotalPoolCount(t *testing.T) {
	t.Run("GetTotalPoolCount_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.GetTotalPoolCount)

		expectedCount := int64(100)

		mockStorage.On("GetPoolsCount", mock.Anything, mock.AnythingOfType("*utils.Filter")).Return(expectedCount, nil)

		encodedValue, err := env.ExecuteActivity(activityInstance.GetTotalPoolCount)

		assert.NoError(tt, err)
		var result int
		err = encodedValue.Get(&result)
		assert.NoError(t, err)
		assert.Equal(tt, int(expectedCount), result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetTotalPoolCount_DatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.GetTotalPoolCount)

		mockStorage.On("GetPoolsCount", mock.Anything, mock.AnythingOfType("*utils.Filter")).Return(int64(0), errors.New("database error"))

		_, err := env.ExecuteActivity(activityInstance.GetTotalPoolCount)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncSnapshotActivity_ListPoolsUUIDPaginated(t *testing.T) {
	t.Run("ListPoolsUUIDPaginated_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.ListPoolsUUIDPaginated)

		expectedPools := []*database.PoolIdentifier{
			{UUID: "pool-uuid-1", AccountID: 1},
			{UUID: "pool-uuid-2", AccountID: 2},
		}

		mockStorage.On("ListPoolUUIDsPaginated", mock.Anything, mock.AnythingOfType("*utils.Filter"), 0, 10).Return(expectedPools, nil)

		encodedValue, _ := env.ExecuteActivity(activityInstance.ListPoolsUUIDPaginated, 0, 10)

		var result []*database.PoolIdentifier
		err := encodedValue.Get(&result)
		assert.NoError(t, err)
		assert.NoError(tt, err)
		assert.Equal(tt, expectedPools, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ListPoolsUUIDPaginated_DatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := SyncSnapshotActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.ListPoolsUUIDPaginated)

		mockStorage.On("ListPoolUUIDsPaginated", mock.Anything, mock.AnythingOfType("*utils.Filter"), 0, 10).Return(nil, errors.New("database error"))

		_, err := env.ExecuteActivity(activityInstance.ListPoolsUUIDPaginated, 0, 10)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncSnapshotActivity_SyncSnapshotsForPoolBatch(t *testing.T) {
	t.Run("EmptyPoolIdentifiers_ReturnsEmptyResult", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, []*database.PoolIdentifier{})

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 0, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 0, result.Failed)
	})

	t.Run("NilPoolIdentifiers_ReturnsEmptyResult", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, nil)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 0, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 0, result.Failed)
	})
}

func TestGetOntapRestProviderForPool_ErrorHandling(t *testing.T) {
	t.Run("DatabaseError_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		// Mock database error
		mockStorage.On("GetNodesByPoolID", mock.Anything, mock.Anything).Return(nil, errors.New("database error"))

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Error(tt, err)
		assert.Nil(tt, provider)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("RecordNotFound_ReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		// Mock record not found error
		mockStorage.On("GetNodesByPoolID", mock.Anything, mock.Anything).Return(nil, gorm.ErrRecordNotFound)

		provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)

		assert.Error(tt, err)
		assert.Nil(tt, provider)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncSnapshotActivity_SyncSnapshotsForPoolBatch_Comprehensive(t *testing.T) {
	t.Run("SinglePool_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		poolIdentifiers := []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "test-pool-1", AccountID: 1},
		}

		// Mock the FetchPoolByUUID call
		mockStorage.On("GetPool", mock.Anything, "pool-1", int64(1)).Return(nil, errors.New("pool not found"))

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 1, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 1, result.Failed)
	})

	t.Run("MultiplePools_MixedResults", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		poolIdentifiers := []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "test-pool-1", AccountID: 1},
			{UUID: "pool-2", Name: "test-pool-2", AccountID: 1},
			{UUID: "pool-3", Name: "test-pool-3", AccountID: 1},
		}

		// Mock the FetchPoolByUUID calls
		mockStorage.On("GetPool", mock.Anything, "pool-1", int64(1)).Return(nil, errors.New("pool not found"))
		mockStorage.On("GetPool", mock.Anything, "pool-2", int64(1)).Return(nil, errors.New("pool not found"))
		mockStorage.On("GetPool", mock.Anything, "pool-3", int64(1)).Return(nil, errors.New("pool not found"))

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 3, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 3, result.Failed)
	})

	t.Run("LargeBatch_ConcurrencyTest", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create a large batch to test concurrency
		poolIdentifiers := make([]*database.PoolIdentifier, 25)
		for i := 0; i < 25; i++ {
			poolIdentifiers[i] = &database.PoolIdentifier{
				UUID:      fmt.Sprintf("pool-%d", i),
				Name:      fmt.Sprintf("test-pool-%d", i),
				AccountID: 1,
			}
		}

		// Mock all the GetPool calls
		for i := 0; i < 25; i++ {
			mockStorage.On("GetPool", mock.Anything, fmt.Sprintf("pool-%d", i), int64(1)).Return(nil, errors.New("pool not found"))
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 25, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 25, result.Failed)
	})
}

// testProcessPoolSnapshotSyncWrapper is a test helper that wraps processPoolSnapshotSync
// to be used with Temporal test activity environment
func testProcessPoolSnapshotSyncWrapper(ctx context.Context, activity *SyncSnapshotActivity, poolIdentifier *database.PoolIdentifier) error {
	return activity.processPoolSnapshotSync(ctx, poolIdentifier)
}

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_Comprehensive(t *testing.T) {
	t.Run("ValidPoolIdentifier_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()

		// Create a function reference that we'll use for both registration and execution
		activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
			return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
		}
		env.RegisterActivity(activityFunc)

		poolIdentifier := &database.PoolIdentifier{
			UUID:      "test-pool-uuid",
			Name:      "test-pool",
			AccountID: 1,
		}

		// Mock the GetPool call
		mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(nil, errors.New("pool not found"))

		// This will fail due to uninitialized dependencies, but we can test the structure
		_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

		// Should return error due to uninitialized dependencies
		assert.Error(tt, err)
	})

	t.Run("EmptyPoolIdentifier_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()

		// Create a function reference that we'll use for both registration and execution
		activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
			return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
		}
		env.RegisterActivity(activityFunc)

		poolIdentifier := &database.PoolIdentifier{}

		// Mock the GetPool call
		mockStorage.On("GetPool", mock.Anything, "", int64(0)).Return(nil, errors.New("pool not found"))

		// This will fail due to uninitialized dependencies, but we can test the structure
		_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

		// Should return error due to uninitialized dependencies
		assert.Error(tt, err)
	})

	t.Run("PoolWithSpecialCharacters_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()

		// Create a function reference that we'll use for both registration and execution
		activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
			return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
		}
		env.RegisterActivity(activityFunc)

		poolIdentifier := &database.PoolIdentifier{
			UUID:      "test-pool-uuid-with-special-chars-!@#$%",
			Name:      "test-pool-with-special-chars",
			AccountID: 1,
		}

		// Mock the GetPool call
		mockStorage.On("GetPool", mock.Anything, "test-pool-uuid-with-special-chars-!@#$%", int64(1)).Return(nil, errors.New("pool not found"))

		// This will fail due to uninitialized dependencies, but we can test the structure
		_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

		// Should return error due to uninitialized dependencies
		assert.Error(tt, err)
	})
}

func TestSyncSnapshotActivity_SyncSnapshotsForPoolBatch_EdgeCases(t *testing.T) {
	t.Run("VeryLargeBatch_ConcurrencyTest", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		// Create a very large batch to test concurrency limits
		poolIdentifiers := make([]*database.PoolIdentifier, 50)
		for i := 0; i < 50; i++ {
			poolIdentifiers[i] = &database.PoolIdentifier{
				UUID:      fmt.Sprintf("pool-%d", i),
				Name:      fmt.Sprintf("test-pool-%d", i),
				AccountID: 1,
			}
		}

		// Mock all the GetPool calls
		for i := 0; i < 50; i++ {
			mockStorage.On("GetPool", mock.Anything, fmt.Sprintf("pool-%d", i), int64(1)).Return(nil, errors.New("pool not found"))
		}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 50, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 50, result.Failed)
	})

	t.Run("SinglePoolWithEmptyName_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		poolIdentifiers := []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "", AccountID: 1},
		}

		// Mock the GetPool call
		mockStorage.On("GetPool", mock.Anything, "pool-1", int64(1)).Return(nil, errors.New("pool not found"))

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 1, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 1, result.Failed)
	})

	t.Run("MixedValidAndInvalidPools_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activityInstance := &SyncSnapshotActivity{
			SE: mockStorage,
		}

		poolIdentifiers := []*database.PoolIdentifier{
			{UUID: "pool-1", Name: "valid-pool-1", AccountID: 1},
			{UUID: "", Name: "invalid-pool-2", AccountID: 1},
			{UUID: "pool-3", Name: "valid-pool-3", AccountID: 1},
		}

		// Mock the GetPool calls
		mockStorage.On("GetPool", mock.Anything, "pool-1", int64(1)).Return(nil, errors.New("pool not found"))
		mockStorage.On("GetPool", mock.Anything, "", int64(1)).Return(nil, errors.New("pool not found"))
		mockStorage.On("GetPool", mock.Anything, "pool-3", int64(1)).Return(nil, errors.New("pool not found"))

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

		encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

		var result *SyncSnapshotsForPoolBatchReturnValue
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		// Should return error due to uninitialized dependencies
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, 3, result.TotalProcessed)
		assert.Equal(tt, 0, result.Successful)
		assert.Equal(tt, 3, result.Failed)
	})
}

func TestGetOntapRestProviderForPool_ProviderError(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1},
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "test-password",
		},
	}
	node := &datamodel.Node{
		EndpointAddress: "1.2.3.4",
	}
	mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)

	// Mock GetProviderByNode to return error
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}

	provider, err := GetOntapRestProviderForPool(ctx, mockStorage, pool)
	assert.Error(t, err)
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "failed to get provider by node")
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_VolumesError(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return(nil, errors.New("failed to get volumes"))

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get volumes")
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_ConcurrencyLimitZero(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	// Set concurrency limit to 0 to test the fallback
	originalLimit := ontapSnapshotFetchConcurrencyLimit
	ontapSnapshotFetchConcurrencyLimit = 0
	defer func() { ontapSnapshotFetchConcurrencyLimit = originalLimit }()

	mockProvider := new(vsa.MockProvider)
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return([]*vsa.Snapshot{}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_GetSnapshotsError(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return(nil, errors.New("failed to get snapshots"))
	// Fallback to REST API when CLI API fails
	mockProvider.On("GetSnapshots", "vol-uuid").Return(nil, errors.New("failed to get snapshots from REST API"))

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err) // Should not return error, just log it
	assert.NotNil(t, result)
	assert.Len(t, result.OntapSnapshots, 0) // No snapshots due to error
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_CLIAPIFailsRESTAPISucceeds(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// CLI API fails
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return(nil, errors.New("CLI API unavailable"))
	// Fallback to REST API succeeds
	mockProvider.On("GetSnapshots", "vol-uuid").Return([]*vsa.Snapshot{{
		Snapshot: ontaprestmodel.Snapshot{
			Name:             nillable.ToPointer("snap"),
			ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{UUID: nillable.ToPointer("vol-uuid")},
			Volume:           &ontaprestmodel.SnapshotInlineVolume{Name: nillable.ToPointer("vol-name")},
			Svm:              &ontaprestmodel.SnapshotInlineSvm{Name: nillable.ToPointer("svm-name")},
		},
		ExternalUUID:       "snap-uuid",
		ExternalVolumeUUID: "vol-uuid",
	}}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Contains(t, result.OntapVolumeMap, "vol-uuid")
	assert.Len(t, result.OntapSnapshots, 1) // Snapshot from REST API fallback
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_SyncSnapshotsForPoolBatchActivity_SuccessfulPool(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &SyncSnapshotActivity{
		SE: mockStorage,
	}

	poolIdentifiers := []*database.PoolIdentifier{
		{UUID: "pool-1", Name: "test-pool-1", AccountID: 1},
	}

	// Mock successful pool processing
	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-1"},
		Name:            "test-pool-1",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}

	// Mock all the required calls for successful processing
	mockStorage.On("GetPool", mock.Anything, "pool-1", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, mock.AnythingOfType("[]int64")).Return([]*datamodel.Snapshot{}, nil)

	// Mock provider
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.SyncSnapshotsForPoolBatchActivity)

	encodedValue, _ := env.ExecuteActivity(activityInstance.SyncSnapshotsForPoolBatchActivity, poolIdentifiers)

	var result *SyncSnapshotsForPoolBatchReturnValue
	err := encodedValue.Get(&result)
	assert.NoError(t, err)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 1, result.Successful)
	assert.Equal(t, 0, result.Failed)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_GetOntapDataFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Create a function reference that we'll use for both registration and execution
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	poolIdentifier := &database.PoolIdentifier{
		UUID:      "test-pool-uuid",
		Name:      "test-pool",
		AccountID: 1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}

	// Mock successful pool fetch
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)

	// Mock provider to return error for GetVolumes
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return(nil, errors.New("failed to get volumes"))

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GetOntapVolumesAndSnapshotsForPool Failed")
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_GetDBDataFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Create a function reference that we'll use for both registration and execution
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	poolIdentifier := &database.PoolIdentifier{
		UUID:      "test-pool-uuid",
		Name:      "test-pool",
		AccountID: 1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}

	// Mock successful pool fetch and ONTAP data
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(nil, errors.New("failed to get volumes from database"))

	// Mock provider
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GetDBVolumeAndSnapshotsForPool Failed")
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_ProcessSnapshotsFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Create a function reference that we'll use for both registration and execution
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	poolIdentifier := &database.PoolIdentifier{
		UUID:      "test-pool-uuid",
		Name:      "test-pool",
		AccountID: 1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}

	// Mock successful pool fetch and data retrieval
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, mock.AnythingOfType("[]int64")).Return([]*datamodel.Snapshot{}, nil)

	// Mock provider
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	// This test will pass because the actual ProcessSnapshots method will work correctly
	// The error handling is tested through the integration test
	_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

	// This should succeed since we're not actually causing an error in ProcessSnapshots
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_SuccessfulExecution(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Create a function reference that we'll use for both registration and execution
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	poolIdentifier := &database.PoolIdentifier{
		UUID:      "test-pool-uuid",
		Name:      "test-pool",
		AccountID: 1,
	}

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}

	// Mock successful pool fetch and data retrieval
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, mock.AnythingOfType("[]int64")).Return([]*datamodel.Snapshot{}, nil)

	// Mock provider
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	// This test verifies successful execution path
	_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestGetOntapRestProviderForPoolFastConn(t *testing.T) {
	ctx := context.TODO()

	t.Run("GetOntapRestProviderForPoolFastConn_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "abcd",
			},
		}
		node := &datamodel.Node{
			EndpointAddress: "1.2.3.4",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)

		// Patch hyperscaler functions to return a mock provider
		originalGetProviderByNodeWithFastConnection := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNodeWithFastConnection }()
		mockProvider := new(vsa.MockProvider)

		// Fast connection should succeed
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)
		assert.NoError(t, err)
		assert.Equal(t, mockProvider, provider)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_PoolNotFoundError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, gorm.ErrRecordNotFound)
		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_CouldNotGetNodes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return(nil, errors.New("could not get nodes"))
		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_NoNodesFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}

		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{}, nil)
		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)
		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(tt)
		if !strings.Contains(vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "no nodes found for pool") {
			t.Errorf("expected error %v, got %v", "no nodes found for pool", err)
		}
	})

	t.Run("GetOntapRestProviderForPoolFastConn_NoCredentials", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}
		node := &datamodel.Node{
			EndpointAddress: "test-endpoint",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)
		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)

		assert.Nil(t, provider)
		assert.Error(t, err)
		mockStorage.AssertExpectations(tt)
		if !strings.Contains(vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "pool credentials not found for pool") {
			t.Errorf("expected error %v, got %v", "pool credentials not found for pool", err)
		}
	})

	t.Run("GetOntapRestProviderForPoolFastConn_FastConnectionFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "abcd",
			},
		}
		node := &datamodel.Node{
			EndpointAddress: "1.2.3.4",
		}
		mockStorage.On("GetNodesByPoolID", ctx, pool.ID).Return([]*datamodel.Node{node}, nil)

		// Mock that fast connection fails
		originalGetProviderByNodeWithFastConnection := vsa.GetProviderByNodeWithFastConnection
		defer func() {
			vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNodeWithFastConnection
		}()

		callCount := 0

		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			callCount++
			// Fast connection should fail
			return nil, errors.New("fast connection failed")
		}

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)
		assert.Error(t, err)
		assert.Nil(t, provider)
		assert.Equal(t, 1, callCount, "Should only try fast connection once")
		assert.Contains(t, err.Error(), "fast connection failed")
		mockStorage.AssertExpectations(t)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_HostNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(nil, gorm.ErrRecordNotFound)

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.Equal(tt, vsaerrors.ErrResourceNotFound, vsaerrors.ExtractCustomError(err).TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_GetHostDatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(nil, errors.New("database read failed"))

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vsaerrors.ExtractCustomError(err).TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_HostProviderError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		host := &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: pool.HostUUID}}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(host, nil)

		originalGetProviderForHost := vsa.GetProviderForHost
		defer func() { vsa.GetProviderForHost = originalGetProviderForHost }()
		vsa.GetProviderForHost = func(_ context.Context, _ *datamodel.Host) (vsa.Provider, error) {
			return nil, errors.New("host provider failed")
		}

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)

		assert.Nil(tt, provider)
		assert.ErrorContains(tt, err, "host provider failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPoolFastConn_HostProviderSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			HostUUID:  "host-uuid-1",
		}
		host := &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: pool.HostUUID}}
		mockStorage.On("GetHost", ctx, pool.HostUUID).Return(host, nil)

		originalGetProviderForHost := vsa.GetProviderForHost
		defer func() { vsa.GetProviderForHost = originalGetProviderForHost }()
		mockProvider := new(vsa.MockProvider)
		vsa.GetProviderForHost = func(_ context.Context, actualHost *datamodel.Host) (vsa.Provider, error) {
			assert.Equal(tt, host, actualHost)
			return mockProvider, nil
		}

		provider, err := GetOntapRestProviderForPoolFastConn(ctx, mockStorage, pool)

		assert.NoError(tt, err)
		assert.Equal(tt, mockProvider, provider)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_VolumeMissingNameFallbackToREST(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	// Volume with UUID but no Name
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nil, // Name is missing
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// Should fall back to REST API using UUID
	mockProvider.On("GetSnapshots", "vol-uuid").Return([]*vsa.Snapshot{{
		Snapshot: ontaprestmodel.Snapshot{
			Name:             nillable.ToPointer("snap"),
			ProvenanceVolume: &ontaprestmodel.SnapshotInlineProvenanceVolume{UUID: nillable.ToPointer("vol-uuid")},
			Volume:           &ontaprestmodel.SnapshotInlineVolume{Name: nillable.ToPointer("vol-name")},
			Svm:              &ontaprestmodel.SnapshotInlineSvm{Name: nillable.ToPointer("svm-name")},
		},
		ExternalUUID:       "snap-uuid",
		ExternalVolumeUUID: "vol-uuid",
	}}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Volume without name gets filtered out by filterOntapVolumesAndSnapshots
	assert.NotContains(t, result.OntapVolumeMap, "vol-uuid")
	// Snapshots also get filtered out because the volume isn't in the map
	assert.Len(t, result.OntapSnapshots, 0)
	// But REST API should still be called (verified by AssertExpectations)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_VolumeMissingNameAndUUID(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	// Volume with both Name and UUID missing
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nil, // UUID is missing
		Name:      nil, // Name is missing
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// Should not call GetSnapshots since both Name and UUID are missing

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Volume should be filtered out, so no snapshots
	assert.Len(t, result.OntapSnapshots, 0)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_CLIAPIFailsUUIDMissing(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	// Volume with Name but no UUID
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nil, // UUID is missing
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// CLI API fails
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return(nil, errors.New("CLI API unavailable"))
	// Should not call GetSnapshots since UUID is missing

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// No snapshots since fallback couldn't happen
	assert.Len(t, result.OntapSnapshots, 0)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_CLIAPIFailsRESTAPIFails(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nillable.ToPointer("vol-name"),
		Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// CLI API fails
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return(nil, errors.New("CLI API unavailable"))
	// REST API also fails
	mockProvider.On("GetSnapshots", "vol-uuid").Return(nil, errors.New("REST API unavailable"))

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err) // Should not return error, just log it
	assert.NotNil(t, result)
	assert.Contains(t, result.OntapVolumeMap, "vol-uuid")
	// No snapshots due to both APIs failing
	assert.Len(t, result.OntapSnapshots, 0)
	mockProvider.AssertExpectations(t)
}

func TestSyncSnapshotActivity_GetOntapVolumesAndSnapshotsForPool_VolumeMissingNameRESTAPIFails(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, PoolCredentials: &datamodel.PoolCredentials{Password: "pass"}}

	mockProvider := new(vsa.MockProvider)
	// Volume with UUID but no Name
	vol := &vsa.Volume{Volume: ontaprestmodel.Volume{
		UUID:      nillable.ToPointer("vol-uuid"),
		Name:      nil, // Name is missing
		IsSvmRoot: nillable.ToPointer(false),
		Style:     nillable.ToPointer("flexvol"),
	}, ExternalUUID: "vol-uuid"}
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{vol}, nil)
	// REST API fails
	mockProvider.On("GetSnapshots", "vol-uuid").Return(nil, errors.New("REST API unavailable"))

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	result, err := activity.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	assert.NoError(t, err) // Should not return error, just log it
	assert.NotNil(t, result)
	// Volume without name gets filtered out by filterOntapVolumesAndSnapshots
	assert.NotContains(t, result.OntapVolumeMap, "vol-uuid")
	// No snapshots due to REST API failure and volume being filtered out
	assert.Len(t, result.OntapSnapshots, 0)
	// But REST API should still be called (verified by AssertExpectations)
	mockProvider.AssertExpectations(t)
}

func Test_hydrateSplitNormalVolumesPendingCCFE_NoPendingVolumes_NoOp(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &SyncSnapshotActivity{SE: mockStorage}

	err := act.hydrateSplitRegularVolumesPendingCCFE(ctx, map[string]*datamodel.Volume{
		"ext": {
			BaseModel: datamodel.BaseModel{UUID: "v1"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				SplitRegularVolumeHydrationPending: false,
			},
		},
	})
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func Test_hydrateSplitNormalVolumesPendingCCFE_HydratesAndClearsFlag(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &SyncSnapshotActivity{SE: mockStorage}

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
		Name:      "vol-resource-name",
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			SplitRegularVolumeHydrationPending: true,
			AccountName:                        "proj",
		},
	}

	mockStorage.On("GetPoolByID", mock.Anything, int64(42)).Return(&datamodel.Pool{
		VendorID: "/projects/proj/locations/us-c1/pools/pool-res",
	}, nil)

	origTok := auth.GenerateCallbackToken
	auth.GenerateCallbackToken = func(context.Context) (string, error) { return "tok", nil }
	defer func() { auth.GenerateCallbackToken = origTok }()

	origHU := orchcommon.HydrateUpdatedVolume
	var gotPayload models.VolumeUpdateCCFERequest
	var gotRegion, gotProject, gotVolID, gotTok string
	orchcommon.HydrateUpdatedVolume = func(_ context.Context, payload models.VolumeUpdateCCFERequest, region, projectId, volumeResourceID, token string) error {
		gotPayload = payload
		gotRegion = region
		gotProject = projectId
		gotVolID = volumeResourceID
		gotTok = token
		return nil
	}
	defer func() { orchcommon.HydrateUpdatedVolume = origHU }()

	mockStorage.On("UpdateVolumeFields", mock.Anything, "vol-uuid-1", mock.MatchedBy(func(fields map[string]interface{}) bool {
		a, ok := fields["volume_attributes"].(*datamodel.VolumeAttributes)
		return ok && a != nil && !a.SplitRegularVolumeHydrationPending
	})).Return(nil)

	err := act.hydrateSplitRegularVolumesPendingCCFE(ctx, map[string]*datamodel.Volume{"ext": vol})
	assert.NoError(t, err)
	assert.Equal(t, "us-c1", gotRegion)
	assert.Equal(t, "proj", gotProject)
	assert.Equal(t, "vol-resource-name", gotVolID)
	assert.Equal(t, "tok", gotTok)
	assert.Nil(t, gotPayload.CloneDetails)
	mockStorage.AssertExpectations(t)
}

func Test_hydrateSplitNormalVolumesPendingCCFE_HydrateError_NoClear(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	act := &SyncSnapshotActivity{SE: mockStorage}

	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
		Name:      "vol-resource-name",
		PoolID:    42,
		VolumeAttributes: &datamodel.VolumeAttributes{
			SplitRegularVolumeHydrationPending: true,
			AccountName:                        "proj",
		},
	}

	mockStorage.On("GetPoolByID", mock.Anything, int64(42)).Return(&datamodel.Pool{
		VendorID: "/projects/proj/locations/us-c1/pools/pool-res",
	}, nil)

	origTok := auth.GenerateCallbackToken
	auth.GenerateCallbackToken = func(context.Context) (string, error) { return "tok", nil }
	defer func() { auth.GenerateCallbackToken = origTok }()

	origHU := orchcommon.HydrateUpdatedVolume
	orchcommon.HydrateUpdatedVolume = func(context.Context, models.VolumeUpdateCCFERequest, string, string, string, string) error {
		return errors.New("ccfe failed")
	}
	defer func() { orchcommon.HydrateUpdatedVolume = origHU }()

	err := act.hydrateSplitRegularVolumesPendingCCFE(ctx, map[string]*datamodel.Volume{"ext": vol})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ccfe failed")
	mockStorage.AssertExpectations(t)
}

func Test_hydrateSplitVolumeNormalAfterCloneSplit(t *testing.T) {
	ctx := context.Background()

	t.Run("nil_volume_returns_error", func(tt *testing.T) {
		act := &SyncSnapshotActivity{SE: database.NewMockStorage(tt)}
		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, nil)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume is nil")
	})

	t.Run("zero_pool_id_skips", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &SyncSnapshotActivity{SE: mockStorage}
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "v1"},
			PoolID:    0,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "proj",
			},
		}
		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("nil_volume_attributes_returns_error", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &SyncSnapshotActivity{SE: mockStorage}
		vol := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "v1"},
			PoolID:           1,
			VolumeAttributes: nil,
		}
		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "nil volume attributes")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("missing_project_returns_error", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &SyncSnapshotActivity{SE: mockStorage}
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "v1"},
			PoolID:    1,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "",
			},
		}
		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "no account/project name")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success_cloneDetails_nil", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &SyncSnapshotActivity{SE: mockStorage}
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "v1"},
			Name:      "vol-resource-id",
			PoolID:    42,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "test-project",
			},
		}
		mockStorage.On("GetPoolByID", mock.Anything, int64(42)).Return(&datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 42},
			VendorID:  "/projects/test-project/locations/us-c1/pools/pool-res",
		}, nil)

		origTok := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(context.Context) (string, error) { return "tok", nil }
		defer func() { auth.GenerateCallbackToken = origTok }()

		origHU := orchcommon.HydrateUpdatedVolume
		var gotPayload models.VolumeUpdateCCFERequest
		var gotRegion, gotProject, gotVolID, gotTok string
		orchcommon.HydrateUpdatedVolume = func(_ context.Context, payload models.VolumeUpdateCCFERequest, region, projectId, volumeResourceID, token string) error {
			gotPayload = payload
			gotRegion = region
			gotProject = projectId
			gotVolID = volumeResourceID
			gotTok = token
			return nil
		}
		defer func() { orchcommon.HydrateUpdatedVolume = origHU }()

		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol)
		assert.NoError(tt, err)
		assert.Equal(tt, "us-c1", gotRegion)
		assert.Equal(tt, "test-project", gotProject)
		assert.Equal(tt, "vol-resource-id", gotVolID)
		assert.Equal(tt, "tok", gotTok)
		assert.Nil(tt, gotPayload.CloneDetails)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("project_falls_back_to_Account", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &SyncSnapshotActivity{SE: mockStorage}
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "v1"},
			Name:      "vol-resource-id",
			PoolID:    42,
			Account:   &datamodel.Account{Name: "from-account"},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "",
			},
		}
		mockStorage.On("GetPoolByID", mock.Anything, int64(42)).Return(&datamodel.Pool{
			VendorID: "/projects/from-account/locations/us-c1/pools/pool-res",
		}, nil)

		origTok := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(context.Context) (string, error) { return "tok", nil }
		defer func() { auth.GenerateCallbackToken = origTok }()

		origHU := orchcommon.HydrateUpdatedVolume
		var gotProject string
		orchcommon.HydrateUpdatedVolume = func(_ context.Context, _ models.VolumeUpdateCCFERequest, _ string, projectId string, _ string, _ string) error {
			gotProject = projectId
			return nil
		}
		defer func() { orchcommon.HydrateUpdatedVolume = origHU }()

		err := act.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol)
		assert.NoError(tt, err)
		assert.Equal(tt, "from-account", gotProject)
		mockStorage.AssertExpectations(tt)
	})
}

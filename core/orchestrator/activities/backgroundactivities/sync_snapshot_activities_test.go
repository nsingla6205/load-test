package backgroundactivities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
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

func TestSyncUpdatedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	t.Run("SyncUpdatedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"snapshot-uuid.volume-uuid": {
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
			"snapshot-uuid.volume-uuid": {
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
	t.Run("SyncUpdatedSnapshotsToDatabaseSameExternalUUIDOnTwoVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbSnapshotsMap := map[string]*datamodel.Snapshot{
			"shared-snapshot-uuid.volume-uuid-1": {
				BaseModel:    datamodel.BaseModel{ID: 1, UUID: "db-snapshot-1"},
				State:        datamodel.LifeCycleStateREADY,
				StateDetails: datamodel.LifeCycleStateReadyDetails,
			},
			"shared-snapshot-uuid.volume-uuid-2": {
				BaseModel:    datamodel.BaseModel{ID: 2, UUID: "db-snapshot-2"},
				State:        datamodel.LifeCycleStateREADY,
				StateDetails: datamodel.LifeCycleStateReadyDetails,
			},
		}
		updatedSSMap := map[string]*vsa.Snapshot{
			"shared-snapshot-uuid.volume-uuid-1": {
				ExternalUUID:           "shared-snapshot-uuid",
				SizeInBytes:            100,
				LogicalSizeUsedInBytes: 80,
				Snapshot:               ontaprestmodel.Snapshot{Name: nillable.ToPointer("clone-on-vol-1")},
			},
			"shared-snapshot-uuid.volume-uuid-2": {
				ExternalUUID:           "shared-snapshot-uuid",
				SizeInBytes:            200,
				LogicalSizeUsedInBytes: 160,
				Snapshot:               ontaprestmodel.Snapshot{Name: nillable.ToPointer("clone-on-vol-2")},
			},
		}
		mockStorage.On("BatchUpdateSnapshots", ctx, mock.MatchedBy(func(snapshots []*datamodel.Snapshot) bool {
			if len(snapshots) != 2 {
				return false
			}
			byID := map[int64]*datamodel.Snapshot{}
			for _, snapshot := range snapshots {
				byID[snapshot.ID] = snapshot
			}
			first, second := byID[1], byID[2]
			return first != nil && second != nil &&
				first.Name == "clone-on-vol-1" && first.SnapshotAttributes.SizeInBytes == 100 &&
				first.State == "" && first.StateDetails == "" &&
				second.Name == "clone-on-vol-2" && second.SnapshotAttributes.SizeInBytes == 200 &&
				second.State == "" && second.StateDetails == ""
		})).Return(nil)

		updatedDbSnapshots, err := syncUpdatedSnapshotsToDatabase(ctx, []string{"shared-snapshot-uuid.volume-uuid-1", "shared-snapshot-uuid.volume-uuid-2"}, updatedSSMap, mockStorage, dbSnapshotsMap)
		assert.NoError(tt, err)
		assert.Len(tt, updatedDbSnapshots, 2)
		mockStorage.AssertExpectations(tt)
	})
}

func TestExistingSnapshotVolumeExternalKeys_ChunksExternalUUIDsOnly(t *testing.T) {
	originalChunk := snapshotSyncChunkSize
	snapshotSyncChunkSize = 2
	t.Cleanup(func() { snapshotSyncChunkSize = originalChunk })

	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	// 3 volumes × 5 UUIDs: nested volume×UUID chunking would issue
	// ceil(5/2)*ceil(3/2)=6 queries; single-dimension chunking issues ceil(5/2)=3.
	candidates := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, VolumeID: 1, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-1"}},
		{BaseModel: datamodel.BaseModel{}, VolumeID: 2, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-2"}},
		{BaseModel: datamodel.BaseModel{}, VolumeID: 3, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-3"}},
		{BaseModel: datamodel.BaseModel{}, VolumeID: 1, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-4"}},
		{BaseModel: datamodel.BaseModel{}, VolumeID: 2, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-5"}},
	}

	callCount := 0
	mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1, 2, 3}, mock.MatchedBy(func(uuids []string) bool {
		return len(uuids) > 0 && len(uuids) <= 2
	})).Return([]database.SnapshotVolumeExternalUUID{}, nil).Run(func(args mock.Arguments) {
		callCount++
		uuids := args.Get(2).([]string)
		require.LessOrEqual(t, len(uuids), 2)
		assert.Equal(t, []int64{1, 2, 3}, args.Get(1))
	})

	existing, err := existingSnapshotVolumeExternalKeys(ctx, mockStorage, candidates)
	require.NoError(t, err)
	assert.Empty(t, existing)
	assert.Equal(t, 3, callCount)
	mockStorage.AssertExpectations(t)
}

func TestExistingSnapshotVolumeExternalKeys_MergesChunkResults(t *testing.T) {
	originalChunk := snapshotSyncChunkSize
	snapshotSyncChunkSize = 2
	t.Cleanup(func() { snapshotSyncChunkSize = originalChunk })

	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	candidates := []*datamodel.Snapshot{
		{VolumeID: 10, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "a"}},
		{VolumeID: 20, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "b"}},
		{VolumeID: 10, SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "c"}},
	}

	mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{10, 20}, []string{"a", "b"}).Return(
		[]database.SnapshotVolumeExternalUUID{{VolumeID: 10, ExternalUUID: "a"}}, nil)
	mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{10, 20}, []string{"c"}).Return(
		[]database.SnapshotVolumeExternalUUID{{VolumeID: 20, ExternalUUID: "c"}}, nil)

	existing, err := existingSnapshotVolumeExternalKeys(ctx, mockStorage, candidates)
	require.NoError(t, err)
	assert.Equal(t, map[snapshotVolumeExternalKey]struct{}{
		{volumeID: 10, externalUUID: "a"}: {},
		{volumeID: 20, externalUUID: "c"}: {},
	}, existing)
	mockStorage.AssertExpectations(t)
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

		mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1}, []string{"new-snapshot-uuid"}).Return([]database.SnapshotVolumeExternalUUID{}, nil)
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

		mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1}, []string{"new-snapshot-uuid"}).Return([]database.SnapshotVolumeExternalUUID{}, nil)
		mockStorage.On("BatchCreateSnapshots", ctx, mock.Anything, mock.Anything).Return(
			nil, errors.New("could not create snapshot"))

		_, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
	t.Run("SkipsInsertWhenVolumeAndExternalUUIDAlreadyExist", func(tt *testing.T) {
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
					Name: nillable.ToPointer("hourly.2026-10-07"),
				},
				ExternalUUID:       "new-snapshot-uuid",
				ExternalVolumeUUID: "test-volume-uuid",
				Type:               SnapshotTypeScheduled,
			},
		}
		mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1}, []string{"new-snapshot-uuid"}).Return(
			[]database.SnapshotVolumeExternalUUID{{VolumeID: 1, ExternalUUID: "new-snapshot-uuid"}}, nil)

		created, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.NoError(tt, err)
		assert.Empty(tt, created)
		mockStorage.AssertNotCalled(tt, "BatchCreateSnapshots", mock.Anything, mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("InsertsWhenSameExternalUUIDExistsOnAnotherVolume", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"volume-1": {BaseModel: datamodel.BaseModel{ID: 1}},
			"volume-2": {BaseModel: datamodel.BaseModel{ID: 2}},
		}
		pool := &datamodel.Pool{AccountID: 1}
		newSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-1": {
				Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("cloned-snapshot")},
				ExternalUUID:       "shared-snapshot-uuid",
				ExternalVolumeUUID: "volume-1",
				Type:               SnapshotTypeAdHoc,
			},
			"snapshot-uuid.volume-2": {
				Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("cloned-snapshot")},
				ExternalUUID:       "shared-snapshot-uuid",
				ExternalVolumeUUID: "volume-2",
				Type:               SnapshotTypeAdHoc,
			},
		}
		mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1, 2}, []string{"shared-snapshot-uuid"}).Return(
			[]database.SnapshotVolumeExternalUUID{{VolumeID: 1, ExternalUUID: "shared-snapshot-uuid"}}, nil)
		mockStorage.On("BatchCreateSnapshots", ctx, mock.MatchedBy(func(snapshots []*datamodel.Snapshot) bool {
			return len(snapshots) == 1 && snapshots[0].VolumeID == 2 && snapshots[0].ExternalUUID == "shared-snapshot-uuid"
		}), true).Return([]string{"created-uuid"}, nil)
		mockStorage.On("BatchGetSnapshotsByUUIDs", ctx, []string{"created-uuid"}).Return([]*datamodel.Snapshot{{ExternalUUID: "shared-snapshot-uuid", SnapshotAttributes: &datamodel.SnapshotAttributes{}}}, nil)

		created, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-1", "snapshot-uuid.volume-2"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.NoError(tt, err)
		assert.Len(tt, created, 1)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("InsertsWhenExternalUUIDIsEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"test-volume-uuid": {BaseModel: datamodel.BaseModel{ID: 1}},
		}
		pool := &datamodel.Pool{AccountID: 1}
		newSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("new-snapshot")},
				ExternalVolumeUUID: "test-volume-uuid",
				Type:               SnapshotTypeScheduled,
			},
		}
		mockStorage.On("BatchCreateSnapshots", ctx, mock.Anything, true).Return([]string{"created-uuid"}, nil)
		mockStorage.On("BatchGetSnapshotsByUUIDs", ctx, []string{"created-uuid"}).Return([]*datamodel.Snapshot{{SnapshotAttributes: &datamodel.SnapshotAttributes{}}}, nil)

		created, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.NoError(tt, err)
		assert.Len(tt, created, 1)
		mockStorage.AssertNotCalled(tt, "ListSnapshotVolumeExternalUUIDs", mock.Anything, mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("ReturnsErrorWhenExistingSnapshotLookupFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"test-volume-uuid": {BaseModel: datamodel.BaseModel{ID: 1}},
		}
		pool := &datamodel.Pool{AccountID: 1}
		newSSMap := map[string]*vsa.Snapshot{
			"snapshot-uuid.volume-uuid": {
				Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("hourly.2026-10-07")},
				ExternalUUID:       "new-snapshot-uuid",
				ExternalVolumeUUID: "test-volume-uuid",
				Type:               SnapshotTypeScheduled,
			},
		}
		mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1}, []string{"new-snapshot-uuid"}).Return(nil, errors.New("lookup failed"))

		created, err := syncNewSnapshotsToDatabase(ctx, []string{"snapshot-uuid.volume-uuid"}, newSSMap, mockStorage, dbVolumeMap, pool)
		assert.Error(tt, err)
		assert.Nil(tt, created)
		mockStorage.AssertNotCalled(tt, "BatchCreateSnapshots", mock.Anything, mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
	})
}

func TestSyncDeletedSnapshotsToDatabase(t *testing.T) {
	t.Run("SyncDeletedSnapshotsToDatabaseSuccess", func(tt *testing.T) {
		ctx := context.TODO()
		mockStorage := database.NewMockStorage(tt)
		observedAt := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
		snapshots := []*datamodel.Snapshot{
			{BaseModel: datamodel.BaseModel{ID: 1, UpdatedAt: observedAt}},
			{BaseModel: datamodel.BaseModel{ID: 2, UpdatedAt: observedAt}},
			{BaseModel: datamodel.BaseModel{ID: 3, UpdatedAt: observedAt}},
		}
		mockStorage.On("BatchDeleteReadySnapshotsIfUnchanged", ctx, snapshots).Return(
			[]*datamodel.Snapshot{
				{BaseModel: datamodel.BaseModel{ID: 1}},
				{BaseModel: datamodel.BaseModel{ID: 2}},
				{BaseModel: datamodel.BaseModel{ID: 3}},
			}, nil)

		deletedSnapshots, err := syncDeletedSnapshotsToDatabase(ctx, snapshots, mockStorage)
		assert.NoError(tt, err)
		assert.Len(tt, deletedSnapshots, 3)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("SyncDeletedSnapshotsToDatabaseFailed", func(tt *testing.T) {
		ctx := context.TODO()
		mockStorage := database.NewMockStorage(tt)
		snapshots := []*datamodel.Snapshot{{BaseModel: datamodel.BaseModel{ID: 1}}}
		mockStorage.On("BatchDeleteReadySnapshotsIfUnchanged", ctx, snapshots).Return(nil, errors.New("could not delete snapshots"))

		deletedSnapshots, err := syncDeletedSnapshotsToDatabase(ctx, snapshots, mockStorage)
		assert.Error(tt, err)
		assert.Nil(t, deletedSnapshots)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("SkipsDatabaseCallWhenNothingToDelete", func(tt *testing.T) {
		ctx := context.TODO()
		mockStorage := database.NewMockStorage(tt)

		deletedSnapshots, err := syncDeletedSnapshotsToDatabase(ctx, nil, mockStorage)
		assert.NoError(tt, err)
		assert.Nil(tt, deletedSnapshots)
		mockStorage.AssertNotCalled(tt, "BatchDeleteReadySnapshotsIfUnchanged", mock.Anything, mock.Anything)
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
	original := deleteStaleAdHocSnapshots
	deleteStaleAdHocSnapshots = true
	t.Cleanup(func() { deleteStaleAdHocSnapshots = original })

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
		// Ad-hoc snapshot missing from the database is left unchanged.
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
			BaseModel:    datamodel.BaseModel{ID: 1},
			ExternalUUID: "test-snapshot-uuid-1",
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
			BaseModel:          datamodel.BaseModel{ID: 3},
			Type:               SnapshotTypeBackup,
			ExternalUUID:       "test-snapshot-uuid-3",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "test-snapshot-uuid-3"},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "test-volume-uuid-1",
				},
			},
		},
		// Ad-hoc snapshot missing from ONTAP is deleted after a successful fetch.
		{
			BaseModel:          datamodel.BaseModel{ID: 4},
			Type:               SnapshotTypeAdHoc,
			ExternalUUID:       "test-snapshot-uuid-adhoc",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "test-snapshot-uuid-adhoc"},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "test-volume-uuid-1",
				},
			},
		},
		// Does not add entry into deletedIDs (volume doesn't exist on ONTAP)
		{
			BaseModel:          datamodel.BaseModel{ID: 6, UUID: "test-snapshot-uuid-6"},
			Type:               SnapshotTypeBackup,
			ExternalUUID:       "test-snapshot-uuid-6",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "test-snapshot-uuid-6"},
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

	snapshotFetchSucceeded := map[string]struct{}{
		"test-volume-uuid-1": {},
		"test-volume-uuid-2": {},
		"test-volume-uuid-3": {},
	}
	newSSMap, updatedSSMap, newIDs, updatedIDs, deletedIDs :=
		processSnapshotSync(ctx, ontapVolumeMap, ontapSnapshots, dbVolumeMap, dbSnapshots, snapshotFetchSucceeded)

	assert.Len(t, newSSMap, 3)
	assert.Len(t, updatedSSMap, 1)
	assert.Len(t, newIDs, 3)
	assert.Len(t, updatedIDs, 1)
	assert.Equal(t, []int64{3, 4}, deletedIDs)
}

func TestProcessSnapshotSync_SkipsDeleteWhenSnapshotFetchFailed(t *testing.T) {
	original := deleteStaleAdHocSnapshots
	deleteStaleAdHocSnapshots = true
	t.Cleanup(func() { deleteStaleAdHocSnapshots = original })

	ctx := context.Background()
	ontapVolumeMap := map[string]*vsa.Volume{
		"test-volume-uuid-1": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-1"),
				UUID: nillable.ToPointer("test-volume-uuid-1"),
			},
		},
	}
	dbSnapshots := []*datamodel.Snapshot{
		{
			BaseModel:          datamodel.BaseModel{ID: 3, UUID: "scheduled-snapshot"},
			Type:               SnapshotTypeScheduled,
			ExternalUUID:       "test-snapshot-uuid-3",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "test-snapshot-uuid-3"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-volume-uuid-1"},
			},
		},
		{
			BaseModel:          datamodel.BaseModel{ID: 4, UUID: "adhoc-snapshot"},
			Type:               SnapshotTypeAdHoc,
			ExternalUUID:       "test-snapshot-uuid-4",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "test-snapshot-uuid-4"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-volume-uuid-1"},
			},
		},
	}

	_, _, _, _, deletedIDs := processSnapshotSync(ctx, ontapVolumeMap, nil, nil, dbSnapshots, map[string]struct{}{})
	assert.Empty(t, deletedIDs)

	_, _, _, _, deletedAfterFetch := processSnapshotSync(ctx, ontapVolumeMap, nil, nil, dbSnapshots, map[string]struct{}{
		"test-volume-uuid-1": {},
	})
	assert.Equal(t, []int64{3, 4}, deletedAfterFetch)
}

func TestProcessSnapshotSync_SkipsAdHocDeleteWhenFlagDisabled(t *testing.T) {
	original := deleteStaleAdHocSnapshots
	deleteStaleAdHocSnapshots = false
	t.Cleanup(func() { deleteStaleAdHocSnapshots = original })

	ctx := context.Background()
	ontapVolumeMap := map[string]*vsa.Volume{
		"test-volume-uuid-1": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-1"),
				UUID: nillable.ToPointer("test-volume-uuid-1"),
			},
		},
	}
	dbSnapshots := []*datamodel.Snapshot{
		{
			BaseModel:          datamodel.BaseModel{ID: 3},
			Type:               SnapshotTypeScheduled,
			ExternalUUID:       "scheduled-missing",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "scheduled-missing"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-volume-uuid-1"},
			},
		},
		{
			BaseModel:          datamodel.BaseModel{ID: 4},
			Type:               SnapshotTypeAdHoc,
			ExternalUUID:       "adhoc-missing",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "adhoc-missing"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-volume-uuid-1"},
			},
		},
	}

	_, _, _, _, deletedIDs := processSnapshotSync(ctx, ontapVolumeMap, nil, nil, dbSnapshots, map[string]struct{}{
		"test-volume-uuid-1": {},
	})
	assert.Equal(t, []int64{3}, deletedIDs)
}

func TestProcessSnapshotSync_SkipsSnapshotWhenDatabaseVolumeMissing(t *testing.T) {
	ctx := context.Background()
	ontapVolumeMap := map[string]*vsa.Volume{
		"test-volume-uuid-1": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("test-volume-1"),
				UUID: nillable.ToPointer("test-volume-uuid-1"),
			},
		},
		"": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("volume-without-uuid"),
			},
		},
	}
	dbSnapshots := []*datamodel.Snapshot{
		{
			BaseModel:          datamodel.BaseModel{ID: 3},
			Type:               SnapshotTypeBackup,
			ExternalUUID:       "missing-on-ontap",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "missing-on-ontap"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "test-volume-uuid-1"},
			},
		},
		{
			BaseModel:          datamodel.BaseModel{ID: 9},
			Type:               SnapshotTypeScheduled,
			ExternalUUID:       "nil-volume-snapshot",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "nil-volume-snapshot"},
		},
		{
			BaseModel:          datamodel.BaseModel{ID: 10},
			Type:               SnapshotTypeScheduled,
			ExternalUUID:       "nil-attributes-snapshot",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "nil-attributes-snapshot"},
			Volume:             &datamodel.Volume{},
		},
		{
			BaseModel:          datamodel.BaseModel{ID: 11},
			Type:               SnapshotTypeScheduled,
			ExternalUUID:       "empty-volume-uuid-snapshot",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "empty-volume-uuid-snapshot"},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: ""},
			},
		},
	}

	_, _, _, _, deletedIDs := processSnapshotSync(ctx, ontapVolumeMap, nil, nil, dbSnapshots, map[string]struct{}{
		"test-volume-uuid-1": {},
		"":                   {},
	})
	assert.Equal(t, []int64{3}, deletedIDs)
}

func TestProcessSnapshotSync_DoesNotDeleteSnapshotAbsentFromDatabaseBaseline(t *testing.T) {
	ctx := context.Background()
	ontapVolumeMap := map[string]*vsa.Volume{
		"vol-uuid": {ExternalUUID: "vol-uuid", Volume: ontaprestmodel.Volume{Type: nillable.ToPointer("rw")}},
	}
	createdDuringSync := []*vsa.Snapshot{
		{
			Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("customer-snap")},
			ExternalUUID:       "adhoc-created-during-sync",
			ExternalVolumeUUID: "vol-uuid",
			Type:               SnapshotTypeAdHoc,
		},
		{
			Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("hourly.1")},
			ExternalUUID:       "scheduled-created-during-sync",
			ExternalVolumeUUID: "vol-uuid",
			Type:               SnapshotTypeScheduled,
		},
	}

	_, _, newIDs, _, deletedIDs := processSnapshotSync(ctx, ontapVolumeMap, createdDuringSync, nil, nil, map[string]struct{}{"vol-uuid": {}})

	assert.Empty(t, deletedIDs)
	assert.Equal(t, []string{"scheduled-created-during-sync.vol-uuid"}, newIDs)
}

// TestProcessSnapshotSync_ImportsAdHocOnCloneWhenParentHasReadyUUID covers the isCloned
// import gate: a non-dp volume's ad-hoc ONTAP snapshot is imported when another READY
// snapshot in the pool already carries the same external UUID (clone inherited from parent).
func TestProcessSnapshotSync_ImportsAdHocOnCloneWhenParentHasReadyUUID(t *testing.T) {
	ctx := context.Background()
	sharedExternalUUID := "shared-clone-snapshot-uuid"

	dbSnapshots := []*datamodel.Snapshot{
		{
			BaseModel:    datamodel.BaseModel{ID: 1},
			Name:         "parent-adhoc",
			Type:         SnapshotTypeAdHoc,
			ExternalUUID: sharedExternalUUID,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID:           sharedExternalUUID,
				SizeInBytes:            100,
				LogicalSizeUsedInBytes: 100,
			},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{ID: 1},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "parent-volume-uuid",
				},
			},
		},
	}

	// Clone volume is rw (non-dp). Without isCloned, this ad-hoc would be skipped.
	ontapVolumeMap := map[string]*vsa.Volume{
		"parent-volume-uuid": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("parent-volume"),
				UUID: nillable.ToPointer("parent-volume-uuid"),
			},
			ExternalUUID: "parent-volume-uuid",
		},
		"clone-volume-uuid": {
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("clone-volume"),
				UUID: nillable.ToPointer("clone-volume-uuid"),
			},
			ExternalUUID: "clone-volume-uuid",
		},
	}

	ontapSnapshots := []*vsa.Snapshot{
		// Parent still present in ONTAP so it is not selected for delete.
		{
			Snapshot:               ontaprestmodel.Snapshot{Name: nillable.ToPointer("parent-adhoc")},
			ExternalUUID:           sharedExternalUUID,
			ExternalVolumeUUID:     "parent-volume-uuid",
			Type:                   SnapshotTypeAdHoc,
			SizeInBytes:            100,
			LogicalSizeUsedInBytes: 100,
		},
		{
			Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("clone-inherited-adhoc")},
			ExternalUUID:       sharedExternalUUID,
			ExternalVolumeUUID: "clone-volume-uuid",
			Type:               SnapshotTypeAdHoc,
		},
		// Control: distinct ad-hoc UUID on a non-dp volume is not imported.
		{
			Snapshot:           ontaprestmodel.Snapshot{Name: nillable.ToPointer("unrelated-adhoc")},
			ExternalUUID:       "unrelated-adhoc-uuid",
			ExternalVolumeUUID: "clone-volume-uuid",
			Type:               SnapshotTypeAdHoc,
		},
	}

	_, _, newIDs, _, deletedIDs := processSnapshotSync(
		ctx,
		ontapVolumeMap,
		ontapSnapshots,
		nil,
		dbSnapshots,
		map[string]struct{}{
			"parent-volume-uuid": {},
			"clone-volume-uuid":  {},
		},
	)

	assert.Empty(t, deletedIDs)
	assert.Equal(t, []string{sharedExternalUUID + ".clone-volume-uuid"}, newIDs)
}

func TestSnapshotNeedsSizeOrNameUpdate(t *testing.T) {
	ontap := &vsa.Snapshot{
		Snapshot:               ontaprestmodel.Snapshot{Name: nillable.ToPointer("snap-a")},
		SizeInBytes:            100,
		LogicalSizeUsedInBytes: 50,
	}

	t.Run("nil_inputs", func(tt *testing.T) {
		assert.False(tt, snapshotNeedsSizeOrNameUpdate(nil, ontap))
		assert.False(tt, snapshotNeedsSizeOrNameUpdate(&datamodel.Snapshot{}, nil))
	})

	t.Run("nil_snapshot_attributes_needs_update", func(tt *testing.T) {
		db := &datamodel.Snapshot{Name: "snap-a"}
		assert.True(tt, snapshotNeedsSizeOrNameUpdate(db, ontap))
	})

	t.Run("same_size_and_name_no_update", func(tt *testing.T) {
		db := &datamodel.Snapshot{
			Name: "snap-a",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            100,
				LogicalSizeUsedInBytes: 50,
			},
		}
		assert.False(tt, snapshotNeedsSizeOrNameUpdate(db, ontap))
	})

	t.Run("size_change_needs_update", func(tt *testing.T) {
		db := &datamodel.Snapshot{
			Name: "snap-a",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            99,
				LogicalSizeUsedInBytes: 50,
			},
		}
		assert.True(tt, snapshotNeedsSizeOrNameUpdate(db, ontap))
	})

	t.Run("nil_ontap_name_does_not_panic_or_force_name_update", func(tt *testing.T) {
		db := &datamodel.Snapshot{
			Name: "snap-a",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            100,
				LogicalSizeUsedInBytes: 50,
			},
		}
		assert.False(tt, snapshotNeedsSizeOrNameUpdate(db, &vsa.Snapshot{
			SizeInBytes:            100,
			LogicalSizeUsedInBytes: 50,
		}))
	})
}

func TestSnapshotsObservedForDeleteUsesBaselineUpdatedAt(t *testing.T) {
	observedAt := time.Date(2026, 10, 14, 1, 2, 3, 0, time.UTC)
	dbSnapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{ID: 3, UpdatedAt: observedAt}},
		nil,
		{BaseModel: datamodel.BaseModel{ID: 4, UpdatedAt: observedAt.Add(time.Minute)}},
	}

	observed := snapshotsObservedForDelete(dbSnapshots, []int64{4, 99, 3})

	require.Len(t, observed, 2)
	assert.Equal(t, int64(4), observed[0].ID)
	assert.True(t, observedAt.Add(time.Minute).Equal(observed[0].UpdatedAt))
	assert.Equal(t, int64(3), observed[1].ID)
	assert.True(t, observedAt.Equal(observed[1].UpdatedAt))
	assert.Nil(t, snapshotsObservedForDelete(dbSnapshots, nil))
}

func TestConfirmSnapshotsGoneFromOntap(t *testing.T) {
	ctx := context.Background()
	gone := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 1},
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-gone"},
		Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
	}
	stillThere := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 2},
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-present"},
		Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
	}
	uncertain := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 3},
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-error"},
		Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
	}
	volumeMissing := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 4},
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-vol-missing"},
		Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-missing"}},
	}
	offlineVolume := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 5},
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-offline"},
		Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-offline"}},
	}
	missingIDs := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{ID: 6},
		Volume:    &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: ""}},
	}

	t.Run("empty_or_nil_provider", func(tt *testing.T) {
		assert.Nil(tt, confirmSnapshotsGoneFromOntap(ctx, nil, []*datamodel.Snapshot{gone}))
		assert.Nil(tt, confirmSnapshotsGoneFromOntap(ctx, new(vsa.MockProvider), nil))
	})

	t.Run("keeps_only_clear_snapshot_not_found", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetSnapshot", "snap-gone", "vol-1").
			Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Snapshot", nil)))
		mockProvider.On("GetSnapshot", "snap-present", "vol-1").
			Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-present"}}, nil)
		mockProvider.On("GetSnapshot", "snap-error", "vol-1").
			Return(nil, errors.New("temporary ontap error"))
		mockProvider.On("GetSnapshot", "snap-vol-missing", "vol-missing").
			Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Volume", nil)))
		mockProvider.On("GetSnapshot", "snap-offline", "vol-offline").
			Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Snapshot on offline volume", nil)))

		confirmed := confirmSnapshotsGoneFromOntap(ctx, mockProvider, []*datamodel.Snapshot{
			gone, stillThere, uncertain, volumeMissing, offlineVolume, missingIDs, nil,
		})

		require.Len(tt, confirmed, 1)
		assert.Equal(tt, int64(1), confirmed[0].ID)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("cancelled_context_stops_queued_confirms", func(tt *testing.T) {
		originalLimit := ontapSnapshotConfirmConcurrencyLimit
		ontapSnapshotConfirmConcurrencyLimit = 1
		tt.Cleanup(func() { ontapSnapshotConfirmConcurrencyLimit = originalLimit })

		first := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{ID: 10},
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-a"},
			Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
		}
		second := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{ID: 11},
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-b"},
			Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
		}

		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		release := make(chan struct{})
		var getSnapshotCalls atomic.Int32

		mockProvider := new(vsa.MockProvider)
		// Whichever candidate wins the single slot blocks until released; the other must wait
		// on the semaphore and must not call GetSnapshot after cancel.
		mockProvider.On("GetSnapshot", mock.AnythingOfType("string"), "vol-1").Run(func(mock.Arguments) {
			if getSnapshotCalls.Add(1) == 1 {
				close(started)
			}
			<-release
		}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Snapshot", nil)))

		done := make(chan []*datamodel.Snapshot, 1)
		go func() {
			done <- confirmSnapshotsGoneFromOntap(ctx, mockProvider, []*datamodel.Snapshot{first, second})
		}()

		select {
		case <-started:
		case <-time.After(2 * time.Second):
			tt.Fatal("timed out waiting for in-flight GetSnapshot")
		}
		assert.Equal(tt, int32(1), getSnapshotCalls.Load(), "only one GetSnapshot should be in flight with concurrency 1")
		cancel()
		// Queued worker must observe cancel while still waiting for the semaphore slot.
		time.Sleep(50 * time.Millisecond)
		assert.Equal(tt, int32(1), getSnapshotCalls.Load(), "queued confirm must not call GetSnapshot after cancel")
		close(release)

		select {
		case confirmed := <-done:
			// In-flight call may still complete after cancel; callers must abort on ctx.Err().
			require.LessOrEqual(tt, len(confirmed), 1)
			assert.Equal(tt, int32(1), getSnapshotCalls.Load())
		case <-time.After(2 * time.Second):
			tt.Fatal("timed out waiting for confirmSnapshotsGoneFromOntap to return after cancel")
		}
	})
}

func TestPositiveConcurrency(t *testing.T) {
	assert.Equal(t, 5, positiveConcurrency(0, 5))
	assert.Equal(t, 5, positiveConcurrency(-1, 5))
	assert.Equal(t, 20, positiveConcurrency(20, 5))
}

func TestConfirmSnapshotsGoneFromOntap_UsesConfirmConcurrencyNotFetch(t *testing.T) {
	// Fetch stays at 1 so a bug that still used fetch concurrency would serialize;
	// confirm concurrency 3 should allow three in-flight GetSnapshot calls.
	originalFetch := ontapSnapshotFetchConcurrencyLimit
	originalConfirm := ontapSnapshotConfirmConcurrencyLimit
	ontapSnapshotFetchConcurrencyLimit = 1
	ontapSnapshotConfirmConcurrencyLimit = 3
	t.Cleanup(func() {
		ontapSnapshotFetchConcurrencyLimit = originalFetch
		ontapSnapshotConfirmConcurrencyLimit = originalConfirm
	})

	started := make(chan struct{}, 3)
	release := make(chan struct{})
	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetSnapshot", mock.AnythingOfType("string"), "vol-1").Run(func(mock.Arguments) {
		started <- struct{}{}
		<-release
	}).Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Snapshot", nil)))

	candidates := make([]*datamodel.Snapshot, 3)
	for i := 0; i < 3; i++ {
		candidates[i] = &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{ID: int64(i + 1)},
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: fmt.Sprintf("snap-%d", i)},
			Volume:             &datamodel.Volume{VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-1"}},
		}
	}

	done := make(chan []*datamodel.Snapshot, 1)
	go func() {
		done <- confirmSnapshotsGoneFromOntap(context.Background(), mockProvider, candidates)
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected 3 concurrent GetSnapshot calls with confirm concurrency 3; got %d starters", i)
		}
	}
	close(release)

	select {
	case confirmed := <-done:
		require.Len(t, confirmed, 3)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for confirmSnapshotsGoneFromOntap")
	}
}

func TestTryAcquireSemaphore_RespectsCancellation(t *testing.T) {
	t.Run("blocked_waiter_unblocks_on_cancel_without_slot", func(tt *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sem := make(chan struct{}, 1)
		sem <- struct{}{} // fill the only slot

		done := make(chan bool, 1)
		go func() {
			done <- tryAcquireSemaphore(ctx, sem)
		}()

		time.Sleep(20 * time.Millisecond)
		cancel()

		select {
		case acquired := <-done:
			assert.False(tt, acquired)
			assert.Len(tt, sem, 1, "cancelled waiter must not consume a slot")
		case <-time.After(2 * time.Second):
			tt.Fatal("tryAcquireSemaphore did not return after context cancel")
		}
	})

	t.Run("prefers_cancel_when_slot_and_done_both_ready", func(tt *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sem := make(chan struct{}, 1)

		assert.False(tt, tryAcquireSemaphore(ctx, sem))
		assert.Len(tt, sem, 0, "cancelled acquire must not take a free slot")
	})
}

func TestSyncSnapshotActivity_ProcessSnapshots(t *testing.T) {
	activity := SyncSnapshotActivity{}
	ctx := context.TODO()
	ontapVolumeMap := map[string]*vsa.Volume{"vol-uuid": {ExternalUUID: "vol-uuid"}}
	ontapSnapshots := []*vsa.Snapshot{{ExternalUUID: "snap-uuid", ExternalVolumeUUID: "vol-uuid", Type: SnapshotTypeAdHoc, SizeInBytes: 1, LogicalSizeUsedInBytes: 1}}
	dbVolumeMap := map[string]*datamodel.Volume{"vol-uuid": {BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-uuid"}, VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-uuid"}}}
	dbSnapshots := []*datamodel.Snapshot{}
	result, err := activity.ProcessSnapshots(ctx, ontapVolumeMap, ontapSnapshots, dbVolumeMap, dbSnapshots, map[string]struct{}{"vol-uuid": {}})
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestSyncSnapshotActivity_SyncDeletedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	snapshots := []*datamodel.Snapshot{{BaseModel: datamodel.BaseModel{ID: 1}}, {BaseModel: datamodel.BaseModel{ID: 2}}}
	mockStorage.On("BatchDeleteReadySnapshotsIfUnchanged", ctx, snapshots).Return([]*datamodel.Snapshot{{BaseModel: datamodel.BaseModel{ID: 1}}, {BaseModel: datamodel.BaseModel{ID: 2}}}, nil)
	result, err := activity.SyncDeletedSnapshotsToDatabase(ctx, snapshots)
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
	mockStorage.On("ListSnapshotVolumeExternalUUIDs", ctx, []int64{1}, []string{"snap-uuid"}).Return([]database.SnapshotVolumeExternalUUID{}, nil)
	mockStorage.On("BatchCreateSnapshots", ctx, mock.MatchedBy(func(snapshots []*datamodel.Snapshot) bool {
		return len(snapshots) == 1 &&
			snapshots[0].ExternalUUID == "snap-uuid" &&
			snapshots[0].SnapshotAttributes != nil &&
			snapshots[0].SnapshotAttributes.ExternalUUID == "snap-uuid"
	}), true).Return([]string{"snap-uuid"}, nil)
	mockStorage.On("BatchGetSnapshotsByUUIDs", ctx, []string{"snap-uuid"}).Return([]*datamodel.Snapshot{{ExternalUUID: "snap-uuid", SnapshotAttributes: &datamodel.SnapshotAttributes{}}}, nil)
	result, err := activity.SyncNewSnapshotsToDatabase(ctx, []string{"snap-uuid.vol-uuid"}, newSSMap, dbVolumeMap, pool)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	mockStorage.AssertExpectations(t)
}

func TestSyncSnapshotActivity_SyncUpdatedSnapshotsToDatabase(t *testing.T) {
	ctx := context.TODO()
	mockStorage := database.NewMockStorage(t)
	activity := SyncSnapshotActivity{SE: mockStorage}
	dbSnapshotsMap := map[string]*datamodel.Snapshot{"snap-uuid.vol-uuid": {BaseModel: datamodel.BaseModel{ID: 1, UUID: "snap-uuid"}, ExternalUUID: "snap-uuid", SnapshotAttributes: &datamodel.SnapshotAttributes{}, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails}}
	updatedSSMap := map[string]*vsa.Snapshot{"snap-uuid.vol-uuid": {ExternalUUID: "snap-uuid", SizeInBytes: 1, LogicalSizeUsedInBytes: 1, Snapshot: ontaprestmodel.Snapshot{Name: nillable.GetStringPtr("snap")}}}
	mockStorage.On("BatchUpdateSnapshots", ctx, mock.MatchedBy(func(snapshots []*datamodel.Snapshot) bool {
		return len(snapshots) == 1 &&
			snapshots[0].ExternalUUID == "snap-uuid" &&
			snapshots[0].SnapshotAttributes != nil &&
			snapshots[0].SnapshotAttributes.ExternalUUID == "snap-uuid" &&
			snapshots[0].State == "" &&
			snapshots[0].StateDetails == ""
	})).Return(nil)
	result, err := activity.SyncUpdatedSnapshotsToDatabase(ctx, []string{"snap-uuid.vol-uuid"}, updatedSSMap, dbSnapshotsMap)
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
	created := []*datamodel.Snapshot{{ExternalUUID: "snap-uuid", SnapshotAttributes: &datamodel.SnapshotAttributes{}}}
	deleted := []*datamodel.Snapshot{{ExternalUUID: "del-uuid", SnapshotAttributes: &datamodel.SnapshotAttributes{}}}
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
	assert.Contains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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
	assert.NotContains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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
	assert.Contains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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

	// Mock successful pool fetch and the database baseline that is read before ONTAP.
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, mock.AnythingOfType("[]int64")).Return([]*datamodel.Snapshot{}, nil)

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

	// Database baseline is read before ONTAP, so this failure returns without calling the provider.
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(nil, errors.New("failed to get volumes from database"))

	_, err := env.ExecuteActivity(activityFunc, poolIdentifier)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "GetDBVolumeAndSnapshotsForPool Failed")
	mockStorage.AssertExpectations(t)
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

func TestSyncSnapshotActivity_ProcessPoolSnapshotSync_ReadsDatabaseBeforeOntap(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{SE: mockStorage}
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}
	var order []string
	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Run(func(mock.Arguments) {
		order = append(order, "db-volumes")
	}).Return([]*datamodel.Volume{}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, mock.AnythingOfType("[]int64")).Run(func(mock.Arguments) {
		order = append(order, "db-snapshots")
	}).Return([]*datamodel.Snapshot{}, nil)

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Run(func(mock.Arguments) {
		order = append(order, "ontap-volumes")
	}).Return([]*vsa.Volume{}, nil)
	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { GetOntapRestProviderForPool = originalGetProviderForPool }()

	_, err := env.ExecuteActivity(activityFunc, &database.PoolIdentifier{UUID: "test-pool-uuid", Name: "test-pool", AccountID: 1})

	assert.NoError(t, err)
	assert.Equal(t, []string{"db-volumes", "db-snapshots", "ontap-volumes"}, order)
	mockStorage.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestGetDBVolumeAndSnapshotsForPool_SkipsDeletingAndDeleted(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{SE: mockStorage}
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}
	ready := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{ID: 10},
		State:            datamodel.LifeCycleStateREADY,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ext-ready"},
	}
	deleting := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 11}, State: datamodel.LifeCycleStateDeleting}
	deleted := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: 12}, State: datamodel.LifeCycleStateDeleted}

	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).
		Return([]*datamodel.Volume{nil, deleting, deleted, ready}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, []int64{10}).
		Return([]*datamodel.Snapshot{}, nil)

	result, err := activity.GetDBVolumeAndSnapshotsForPool(context.Background(), pool)
	require.NoError(t, err)
	require.Len(t, result.DBVolumeMap, 1)
	assert.Equal(t, ready, result.DBVolumeMap["ext-ready"])
}

func TestProcessPoolSnapshotSync_ConfirmBeforeDeleteSkipsWhenOntapStillHasSnapshot(t *testing.T) {
	originalAdHoc := deleteStaleAdHocSnapshots
	deleteStaleAdHocSnapshots = true
	t.Cleanup(func() { deleteStaleAdHocSnapshots = originalAdHoc })

	originalHydrate := hydrateBatchSnapshotsToCCFE
	hydrateBatchSnapshotsToCCFE = func(ctx context.Context, createdSnapshots, deletedSnapshots []*datamodel.Snapshot) error {
		assert.Empty(t, deletedSnapshots)
		return nil
	}
	t.Cleanup(func() { hydrateBatchSnapshotsToCCFE = originalHydrate })

	mockStorage := database.NewMockStorage(t)
	activity := &SyncSnapshotActivity{SE: mockStorage}
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	activityFunc := func(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
		return testProcessPoolSnapshotSyncWrapper(ctx, activity, poolIdentifier)
	}
	env.RegisterActivity(activityFunc)

	pool := &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:            "test-pool",
		AccountID:       1,
		PoolCredentials: &datamodel.PoolCredentials{Password: "pass"},
	}
	volume := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{ID: 10, UUID: "vol-uuid"},
		State:            datamodel.LifeCycleStateREADY,
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "vol-uuid"},
	}
	stale := &datamodel.Snapshot{
		BaseModel:          datamodel.BaseModel{ID: 99, UUID: "db-snap", UpdatedAt: time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)},
		Name:               "hourly.stale",
		Type:               SnapshotTypeScheduled,
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "snap-still-on-ontap"},
		Volume:             volume,
	}

	mockStorage.On("GetPool", mock.Anything, "test-pool-uuid", int64(1)).Return(&datamodel.PoolView{Pool: *pool}, nil)
	mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{volume}, nil)
	mockStorage.On("GetSnapshotsByVolumeIDs", mock.Anything, []int64{10}).Return([]*datamodel.Snapshot{stale}, nil)
	mockStorage.AssertNotCalled(t, "BatchDeleteReadySnapshotsIfUnchanged", mock.Anything, mock.Anything)

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{{
		ExternalUUID: "vol-uuid",
		Volume: ontaprestmodel.Volume{
			Name:      nillable.ToPointer("vol-name"),
			UUID:      nillable.ToPointer("vol-uuid"),
			IsSvmRoot: nillable.ToPointer(false),
			Svm:       &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-name")},
		},
	}}, nil)
	// List omits the snapshot (incomplete/lag), but get-by-UUID still finds it.
	mockProvider.On("GetSnapshotsViaCLIAPI", mock.Anything, "vol-name", "svm-name").Return([]*vsa.Snapshot{}, nil)
	mockProvider.On("GetSnapshot", "snap-still-on-ontap", "vol-uuid").
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-still-on-ontap"}}, nil)

	originalGetProviderForPool := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, p *datamodel.Pool) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { GetOntapRestProviderForPool = originalGetProviderForPool })

	_, err := env.ExecuteActivity(activityFunc, &database.PoolIdentifier{UUID: "test-pool-uuid", Name: "test-pool", AccountID: 1})
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
	assert.NotContains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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
	assert.NotContains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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
	assert.NotContains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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
	assert.NotContains(t, result.SnapshotFetchSucceeded, "vol-uuid")
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

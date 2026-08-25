package backgroundactivities

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
)

func TestFilterReconcilableVolumes(t *testing.T) {
	t.Run("FiltersOutSvmRootVolumes", func(tt *testing.T) {
		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					IsSvmRoot: nillable.ToPointer(true),
					Name:      nillable.ToPointer("root_vol"),
					UUID:      nillable.ToPointer("root-uuid"),
				},
				ExternalUUID: "root-uuid",
			},
			{
				Volume: ontaprestmodel.Volume{
					IsSvmRoot: nillable.ToPointer(false),
					Name:      nillable.ToPointer("user_vol"),
					UUID:      nillable.ToPointer("user-uuid"),
				},
				ExternalUUID: "user-uuid",
			},
		}

		result := filterReconcilableVolumes(volumes)
		assert.Len(tt, result, 1)
		assert.Equal(tt, "user-uuid", result[0].ExternalUUID)
	})

	t.Run("KeepsBothDpAndRwVolumes", func(tt *testing.T) {
		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Type: nillable.ToPointer("dp"),
					Name: nillable.ToPointer("dp_vol"),
					UUID: nillable.ToPointer("dp-uuid"),
				},
				ExternalUUID: "dp-uuid",
			},
			{
				Volume: ontaprestmodel.Volume{
					Type: nillable.ToPointer("rw"),
					Name: nillable.ToPointer("rw_vol"),
					UUID: nillable.ToPointer("rw-uuid"),
				},
				ExternalUUID: "rw-uuid",
			},
		}

		result := filterReconcilableVolumes(volumes)
		assert.Len(tt, result, 2)
		uuids := []string{result[0].ExternalUUID, result[1].ExternalUUID}
		assert.ElementsMatch(tt, []string{"dp-uuid", "rw-uuid"}, uuids)
	})

	t.Run("FiltersOutFlexGroupConstituents", func(tt *testing.T) {
		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Style: nillable.ToPointer("flexgroup_constituent"),
					Name:  nillable.ToPointer("constituent"),
					UUID:  nillable.ToPointer("c-uuid"),
				},
				ExternalUUID: "c-uuid",
			},
			{
				Volume: ontaprestmodel.Volume{
					Style: nillable.ToPointer("flexvol"),
					Name:  nillable.ToPointer("flexvol"),
					UUID:  nillable.ToPointer("f-uuid"),
				},
				ExternalUUID: "f-uuid",
			},
		}

		result := filterReconcilableVolumes(volumes)
		assert.Len(tt, result, 1)
		assert.Equal(tt, "f-uuid", result[0].ExternalUUID)
	})

	t.Run("EmptyInput_ReturnsNil", func(tt *testing.T) {
		result := filterReconcilableVolumes(nil)
		assert.Nil(tt, result)
	})
}

func TestShouldReconcileExpertModeVolumeSize(t *testing.T) {
	assert.False(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateCreating))
	assert.False(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateUpdating))
	assert.False(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateDeleting))
	assert.False(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateDeleted))
	assert.True(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateAvailable))
	assert.True(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateREADY))
	assert.True(t, shouldReconcileExpertModeVolumeSize(datamodel.LifeCycleStateError))
}

func TestBuildSvmNameToIDMap(t *testing.T) {
	svms := []*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm-1"},
		{BaseModel: datamodel.BaseModel{ID: 2}, Name: "svm-2"},
	}

	m := buildSvmNameToIDMap(svms)
	assert.Len(t, m, 2)
	assert.Equal(t, int64(1), m["svm-1"])
	assert.Equal(t, int64(2), m["svm-2"])
}

func TestReconcileExpertModeVolumes(t *testing.T) {
	ctx := context.Background()
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 100},
		AccountID: 10,
		Name:      "test-pool",
	}
	svmNameToID := map[string]int64{"svm-1": 1}

	t.Run("AddsVolumePresentInOntapButNotDB", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("new-vol"),
					UUID:  nillable.ToPointer("new-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(1024))},
					Style: nillable.ToPointer("flexvol"),
				},
				ExternalUUID: "new-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{}

		mockSE.On("CreateExpertModeVolume", mock.Anything, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.Name == "new-vol" &&
				v.ExternalUUID == "new-ext-uuid" &&
				v.PoolID == int64(100) &&
				v.AccountID == int64(10) &&
				v.SvmID == int64(1) &&
				v.SizeInBytes == int64(1024) &&
				v.Style == "flexvol" &&
				v.State == datamodel.LifeCycleStateAvailable
		})).Return(&datamodel.ExpertModeVolumes{}, nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 1, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeletesVolumeInDBButNotInOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-1"},
				Name:         "stale-vol",
				ExternalUUID: "stale-ext-uuid",
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-1").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 1, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("FinalizesDeletingVolumeAbsentFromOntapAsDeleted", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleting"},
				Name:         "deleting-vol",
				ExternalUUID: "deleting-ext-uuid",
				State:        datamodel.LifeCycleStateDeleting,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-deleting").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 1, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SkipsTerminalDeletedVolumeAbsentFromOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		// A terminal DELETED row is left as-is: no delete call.
		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleted"},
				Name:         "deleted-vol",
				ExternalUUID: "deleted-ext-uuid",
				State:        datamodel.LifeCycleStateDeleted,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", mock.Anything, mock.Anything)
	})

	t.Run("FinalizesDeletingButSkipsDeletedWhenAbsentFromOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleted"},
				Name:         "deleted-vol",
				ExternalUUID: "deleted-ext-uuid",
				State:        datamodel.LifeCycleStateDeleted,
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleting"},
				Name:         "deleting-vol",
				ExternalUUID: "deleting-ext-uuid",
				State:        datamodel.LifeCycleStateDeleting,
			},
		}

		// Only the DELETING row is finalized; the terminal DELETED row is left untouched.
		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-deleting").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 1, deleted)
		mockSE.AssertExpectations(tt)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", mock.Anything, "db-uuid-deleted")
	})

	t.Run("LeavesDeletingVolumeStillPresentInOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		// A DELETING row whose volume is still present in ONTAP is owned by the in-flight delete
		// workflow: it must not be finalized here (no delete) and its size must not be reconciled.
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("deleting-vol"),
					UUID:  nillable.ToPointer("deleting-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(4096))},
				},
				ExternalUUID: "deleting-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleting"},
				Name:         "deleting-vol",
				ExternalUUID: "deleting-ext-uuid",
				SizeInBytes:  2048,
				State:        datamodel.LifeCycleStateDeleting,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", mock.Anything, mock.Anything)
		mockSE.AssertNotCalled(tt, "UpdateExpertModeVolumeFields", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("AggregatesDeleteErrorWhenFinalizingDeletingFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-deleting-fail"},
				Name:         "deleting-fail-vol",
				ExternalUUID: "deleting-fail-ext-uuid",
				State:        datamodel.LifeCycleStateDeleting,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-deleting-fail").Return(errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "0 update errors")
		assert.Contains(tt, err.Error(), "1 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeletesStaleVolumeInNonTerminalLifecycleState", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-creating"},
				Name:         "creating-vol",
				ExternalUUID: "creating-ext-uuid",
				State:        datamodel.LifeCycleStateCreating,
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-updating"},
				Name:         "updating-vol",
				ExternalUUID: "updating-ext-uuid",
				State:        datamodel.LifeCycleStateUpdating,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-creating").Return(nil)
		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-updating").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 2, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("LeavesFreshCreatingRowPresentInOntapAndSkipsDuplicateAdd", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// ONTAP has the real volume (with an external UUID) under the same name+SVM as the row.
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("ghost-vol"),
					UUID:  nillable.ToPointer("real-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
					Style: nillable.ToPointer("flexvol"),
				},
				ExternalUUID: "real-ext-uuid",
			},
		}
		// Row is fresh (within the grace window): the create workflow still owns it, so it is left
		// alone even though the volume already exists in ONTAP.
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-1 * time.Minute)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		// The row is left untouched, and the add pass must NOT create a duplicate AVAILABLE row for
		// the matching ONTAP volume (its external_uuid is claimed by the creating row). No
		// DeleteExpertModeVolume, UpdateExpertModeVolume, or CreateExpertModeVolume is expected.
		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("AdoptsStaleCreatingRowPresentInOntapInPlace", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// ONTAP has the real volume (with an external UUID) under the same name+SVM as the row.
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("ghost-vol"),
					UUID:  nillable.ToPointer("real-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
					Style: nillable.ToPointer("flexvol"),
				},
				ExternalUUID: "real-ext-uuid",
			},
		}
		// Row is stale (older than the grace window): the create landed in ONTAP but the workflow
		// never wrote back the external_uuid. It is adopted in place (same row UUID) rather than
		// deleted+recreated, so the workflow's later UpdateExpertModeVolumeInDB (keyed by UUID)
		// still finds the row, and no duplicate AVAILABLE row is created.
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		// The SAME row is updated in place: its UUID is preserved and it gains the real external
		// UUID + AVAILABLE state. No Delete and no Create (which would change the row identity).
		mockSE.On("UpdateExpertModeVolume", mock.Anything, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == "ghost-uuid" &&
				v.Name == "ghost-vol" &&
				v.ExternalUUID == "real-ext-uuid" &&
				v.SvmID == int64(1) &&
				v.SizeInBytes == int64(2048) &&
				v.Style == "flexvol" &&
				v.State == datamodel.LifeCycleStateAvailable
		})).Return(&datamodel.ExpertModeVolumes{}, nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 1, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SoftDeletesStaleCreatingGhostWhenOntapVolumeAlreadyBoundToExistingRow", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// ONTAP has the real volume "vol1" (external UUID "real-ext-uuid") in svm-1.
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("vol1"),
					UUID:  nillable.ToPointer("real-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
					Style: nillable.ToPointer("flexvol"),
				},
				ExternalUUID: "real-ext-uuid",
			},
		}
		// DB has both: a legit AVAILABLE row already bound to "real-ext-uuid", AND a stale CREATING
		// ghost (empty external_uuid) for the same name+SVM. The ghost must be soft-deleted as a
		// duplicate, NOT adopted into a second AVAILABLE row.
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "available-uuid"},
				Name:         "vol1",
				ExternalUUID: "real-ext-uuid",
				SvmID:        1,
				SizeInBytes:  2048,
				State:        datamodel.LifeCycleStateAvailable,
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "vol1",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		// Only the ghost is soft-deleted. The legit row is untouched: no Update (sizes match) and
		// no second AVAILABLE row created.
		mockSE.On("DeleteExpertModeVolume", mock.Anything, "ghost-uuid").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 1, deleted)
		mockSE.AssertNotCalled(tt, "UpdateExpertModeVolume", mock.Anything, mock.Anything)
		mockSE.AssertNotCalled(tt, "CreateExpertModeVolume", mock.Anything, mock.Anything)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SoftDeletesStaleCreatingGhostAbsentFromOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "ghost-uuid").Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 1, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("LeavesFreshCreatingGhostAbsentFromOntap", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-10 * time.Minute)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		// No DeleteExpertModeVolume expected: fresh ghost is still owned by the create workflow.
		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DoesNotAdoptGhostWhenOntapSameNameInDifferentSvm", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)
		svmNameToIDMulti := map[string]int64{"svm-1": 1, "svm-2": 2}

		// ONTAP has a same-named volume but in a different SVM (svm-2, id 2).
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("ghost-vol"),
					UUID:  nillable.ToPointer("other-svm-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-2")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
				},
				ExternalUUID: "other-svm-ext-uuid",
			},
		}
		// Fresh ghost in svm-1: not adopted (different SVM) and not stale, so it must be left alone.
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-10 * time.Minute)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		// The svm-2 ONTAP volume is still added; the ghost is left untouched (no delete).
		mockSE.On("CreateExpertModeVolume", mock.Anything, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.Name == "ghost-vol" && v.ExternalUUID == "other-svm-ext-uuid" && v.SvmID == int64(2)
		})).Return(&datamodel.ExpertModeVolumes{}, nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToIDMulti)
		assert.NoError(tt, err)
		assert.Equal(tt, 1, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("NoChangesWhenInSync", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("vol-1"),
					UUID:  nillable.ToPointer("ext-uuid-1"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
				},
				ExternalUUID: "ext-uuid-1",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-1"},
				Name:         "vol-1",
				ExternalUUID: "ext-uuid-1",
				SizeInBytes:  2048,
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SkipsVolumeWithUnknownSvm", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name: nillable.ToPointer("vol-unknown-svm"),
					UUID: nillable.ToPointer("unknown-svm-ext-uuid"),
					Svm:  &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("unknown-svm")},
				},
				ExternalUUID: "unknown-svm-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("AggregatesCreateErrors", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name: nillable.ToPointer("fail-vol"),
					UUID: nillable.ToPointer("fail-ext-uuid"),
					Svm:  &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
				},
				ExternalUUID: "fail-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{}

		mockSE.On("CreateExpertModeVolume", mock.Anything, mock.Anything).Return((*datamodel.ExpertModeVolumes)(nil), errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "1 create errors")
		assert.Contains(tt, err.Error(), "0 update errors")
		assert.Contains(tt, err.Error(), "0 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("AggregatesDeleteErrors", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-fail"},
				Name:         "fail-del-vol",
				ExternalUUID: "fail-del-ext-uuid",
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "db-uuid-fail").Return(errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "0 update errors")
		assert.Contains(tt, err.Error(), "1 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ReconcilesSizeWhenOntapAndDBDiffer", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("resized-vol"),
					UUID:  nillable.ToPointer("resized-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(8192))},
				},
				ExternalUUID: "resized-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-resized"},
				Name:         "resized-vol",
				ExternalUUID: "resized-ext-uuid",
				SizeInBytes:  4096,
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		mockSE.On("UpdateExpertModeVolumeFields", mock.Anything, "resized-ext-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
			size, ok := updates["size_in_bytes"].(int64)
			return ok && size == int64(8192) && len(updates) == 1
		})).Return(nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 1, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SkipsSizeReconcileWhenStateIsInFlight", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("vol-updating"),
					UUID:  nillable.ToPointer("ext-uuid-updating"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(8192))},
				},
				ExternalUUID: "ext-uuid-updating",
			},
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("vol-creating"),
					UUID:  nillable.ToPointer("ext-uuid-creating"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(8192))},
				},
				ExternalUUID: "ext-uuid-creating",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-updating"},
				Name:         "vol-updating",
				ExternalUUID: "ext-uuid-updating",
				SizeInBytes:  4096,
				State:        datamodel.LifeCycleStateUpdating,
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-creating"},
				Name:         "vol-creating",
				ExternalUUID: "ext-uuid-creating",
				SizeInBytes:  4096,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertNotCalled(tt, "UpdateExpertModeVolumeFields", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("SkipsSizeReconcileWhenOntapSizeMissing", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name: nillable.ToPointer("vol-no-size"),
					UUID: nillable.ToPointer("ext-uuid-no-size"),
					Svm:  &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
				},
				ExternalUUID: "ext-uuid-no-size",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-no-size"},
				Name:         "vol-no-size",
				ExternalUUID: "ext-uuid-no-size",
				SizeInBytes:  4096,
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertNotCalled(tt, "UpdateExpertModeVolumeFields", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("AggregatesUpdateErrors", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("resize-fail-vol"),
					UUID:  nillable.ToPointer("resize-fail-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(8192))},
				},
				ExternalUUID: "resize-fail-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-resize-fail"},
				Name:         "resize-fail-vol",
				ExternalUUID: "resize-fail-ext-uuid",
				SizeInBytes:  4096,
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		mockSE.On("UpdateExpertModeVolumeFields", mock.Anything, "resize-fail-ext-uuid", mock.Anything).Return(errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "1 update errors")
		assert.Contains(tt, err.Error(), "0 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SkipsEmptyExternalUUIDRowInNonCreatingState", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		// A row with an empty external_uuid that is NOT in CREATING state is unexpected: it is
		// logged and skipped (never added to dbMap, never deleted).
		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "db-uuid-orphan"},
				Name:         "orphan-vol",
				ExternalUUID: "",
				State:        datamodel.LifeCycleStateAvailable,
			},
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertNotCalled(tt, "DeleteExpertModeVolume", mock.Anything, mock.Anything)
		mockSE.AssertNotCalled(tt, "UpdateExpertModeVolume", mock.Anything, mock.Anything)
	})

	t.Run("AggregatesUpdateErrorWhenAdoptingStaleCreatingRowFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// Stale creating ghost present in ONTAP: adoption in place is attempted but the DB update
		// fails, so an update error is aggregated. The matching ONTAP volume is then created by the
		// add pass (its external_uuid was not claimed because adoption failed).
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("ghost-vol"),
					UUID:  nillable.ToPointer("real-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
					Style: nillable.ToPointer("flexvol"),
				},
				ExternalUUID: "real-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		mockSE.On("UpdateExpertModeVolume", mock.Anything, mock.Anything).Return((*datamodel.ExpertModeVolumes)(nil), errors.New("db error"))
		mockSE.On("CreateExpertModeVolume", mock.Anything, mock.Anything).Return(&datamodel.ExpertModeVolumes{}, nil)

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "1 update errors")
		assert.Contains(tt, err.Error(), "0 delete errors")
		assert.Equal(tt, 1, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("AggregatesDeleteErrorWhenSoftDeletingStaleGhostFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// Stale creating ghost absent from ONTAP: the soft-delete is attempted but fails, so a
		// delete error is aggregated.
		ontapVolumes := []*vsa.Volume{}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "ghost-vol",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "ghost-uuid").Return(errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "0 update errors")
		assert.Contains(tt, err.Error(), "1 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("AggregatesDeleteErrorWhenSoftDeletingDuplicateGhostFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		fixedNow := setFixedNowAndGhostMaxAge(tt, time.Hour)

		// ONTAP volume already bound to an existing AVAILABLE row plus a stale duplicate CREATING
		// ghost for the same name+SVM: the ghost soft-delete is attempted but fails, so a delete
		// error is aggregated.
		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer("vol1"),
					UUID:  nillable.ToPointer("real-ext-uuid"),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
				},
				ExternalUUID: "real-ext-uuid",
			},
		}
		dbVolumes := []*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "available-uuid"},
				Name:         "vol1",
				ExternalUUID: "real-ext-uuid",
				SvmID:        1,
				SizeInBytes:  2048,
				State:        datamodel.LifeCycleStateAvailable,
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "ghost-uuid", CreatedAt: fixedNow.Add(-2 * time.Hour)},
				Name:         "vol1",
				ExternalUUID: "",
				SvmID:        1,
				State:        datamodel.LifeCycleStateCreating,
			},
		}

		mockSE.On("DeleteExpertModeVolume", mock.Anything, "ghost-uuid").Return(errors.New("db error"))

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "0 create errors")
		assert.Contains(tt, err.Error(), "0 update errors")
		assert.Contains(tt, err.Error(), "1 delete errors")
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
		mockSE.AssertExpectations(tt)
	})

	t.Run("RecordsHeartbeatWhenScanningManyVolumes", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)

		// More than expertModeVolumeReconcileHeartbeatInterval (50) in-sync volumes so both the
		// ONTAP-scan loop and the DB-scan loop cross a heartbeat boundary. Sizes match, so no SE
		// calls are made.
		const n = expertModeVolumeReconcileHeartbeatInterval + 1
		ontapVolumes := make([]*vsa.Volume, n)
		dbVolumes := make([]*datamodel.ExpertModeVolumes, n)
		for i := 0; i < n; i++ {
			ext := fmt.Sprintf("ext-%d", i)
			name := fmt.Sprintf("vol-%d", i)
			ontapVolumes[i] = &vsa.Volume{
				Volume: ontaprestmodel.Volume{
					Name:  nillable.ToPointer(name),
					UUID:  nillable.ToPointer(ext),
					Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
					Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
				},
				ExternalUUID: ext,
			}
			dbVolumes[i] = &datamodel.ExpertModeVolumes{
				BaseModel:    datamodel.BaseModel{UUID: fmt.Sprintf("db-%d", i)},
				Name:         name,
				ExternalUUID: ext,
				SizeInBytes:  2048,
				State:        datamodel.LifeCycleStateAvailable,
			}
		}

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
		assert.Equal(tt, 0, added)
		assert.Equal(tt, 0, updated)
		assert.Equal(tt, 0, deleted)
	})
}

// setFixedNowAndGhostMaxAge pins nowUTC to a fixed instant and sets the creating-ghost grace
// window for the duration of the test, restoring both afterward. It returns the fixed "now".
func setFixedNowAndGhostMaxAge(t *testing.T, maxAge time.Duration) time.Time {
	t.Helper()
	originalNow := nowUTC
	originalMaxAge := expertModeCreatingGhostMaxAge
	t.Cleanup(func() {
		nowUTC = originalNow
		expertModeCreatingGhostMaxAge = originalMaxAge
	})
	fixedNow := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	nowUTC = func() time.Time { return fixedNow }
	expertModeCreatingGhostMaxAge = maxAge
	return fixedNow
}

func TestBuildOntapVolumesBySvmAndName(t *testing.T) {
	svmNameToID := map[string]int64{"svm-1": 1, "svm-2": 2}
	ontapVolumes := []*vsa.Volume{
		{
			Volume:       ontaprestmodel.Volume{Name: nillable.ToPointer("vol-a"), Svm: &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")}},
			ExternalUUID: "ext-a",
		},
		{
			Volume:       ontaprestmodel.Volume{Name: nillable.ToPointer("vol-b"), Svm: &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-2")}},
			ExternalUUID: "ext-b",
		},
		// No external UUID -> excluded.
		{Volume: ontaprestmodel.Volume{Name: nillable.ToPointer("vol-c"), Svm: &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")}}},
		// Unknown SVM -> excluded.
		{Volume: ontaprestmodel.Volume{Name: nillable.ToPointer("vol-d"), Svm: &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-unknown")}}, ExternalUUID: "ext-d"},
	}

	got := buildOntapVolumesBySvmAndName(ontapVolumes, svmNameToID)

	require.NotNil(t, got[1]["vol-a"])
	assert.Equal(t, "ext-a", got[1]["vol-a"].ExternalUUID)
	require.NotNil(t, got[2]["vol-b"])
	assert.Equal(t, "ext-b", got[2]["vol-b"].ExternalUUID)
	_, hasVolC := got[1]["vol-c"]
	assert.False(t, hasVolC, "volume without external UUID should be excluded")
	if _, ok := got[0]; ok {
		t.Error("unknown SVM should not appear in the index")
	}
}

func TestFindOntapVolumeForCreatingRow(t *testing.T) {
	volA := &vsa.Volume{
		Volume:       ontaprestmodel.Volume{Name: nillable.ToPointer("vol-a")},
		ExternalUUID: "ext-a",
	}
	index := map[int64]map[string]*vsa.Volume{1: {"vol-a": volA}}

	got, ok := findOntapVolumeForCreatingRow(index, &datamodel.ExpertModeVolumes{Name: "vol-a", SvmID: 1})
	assert.True(t, ok)
	assert.Equal(t, volA, got)

	_, ok = findOntapVolumeForCreatingRow(index, &datamodel.ExpertModeVolumes{Name: "vol-a", SvmID: 2})
	assert.False(t, ok, "same name in a different SVM must not match")

	_, ok = findOntapVolumeForCreatingRow(index, &datamodel.ExpertModeVolumes{Name: "vol-z", SvmID: 1})
	assert.False(t, ok)

	_, ok = findOntapVolumeForCreatingRow(index, nil)
	assert.False(t, ok)
}

func TestHasCreatingGhostGraceAgePassed(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	maxAge := time.Hour

	assert.True(t, hasCreatingGhostGraceAgePassed(&datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{CreatedAt: now.Add(-2 * time.Hour)}}, now, maxAge))
	assert.False(t, hasCreatingGhostGraceAgePassed(&datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{CreatedAt: now.Add(-10 * time.Minute)}}, now, maxAge))
	assert.False(t, hasCreatingGhostGraceAgePassed(&datamodel.ExpertModeVolumes{}, now, maxAge), "zero CreatedAt is never past grace age")
	assert.False(t, hasCreatingGhostGraceAgePassed(&datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{CreatedAt: now.Add(-2 * time.Hour)}}, now, 0), "non-positive maxAge disables grace-age check")
	assert.False(t, hasCreatingGhostGraceAgePassed(nil, now, maxAge))
}

func TestSyncExpertModeVolumesForPoolActivity_NilIdentifier(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	err := a.SyncExpertModeVolumesForPoolActivity(ctx, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pool identifier is nil")
	mockSE.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
}

func TestSyncExpertModeVolumesForPoolActivity_DelegatesToSyncForPool(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	mockSE.On("GetPoolByUUID", mock.Anything, "missing-pool").Return(nil, errors.New("not found"))

	err := a.SyncExpertModeVolumesForPoolActivity(ctx, &database.PoolIdentifier{UUID: "missing-pool", Name: "missing"})
	assert.Error(t, err)
	mockSE.AssertExpectations(t)
}

func TestListOntapModePoolsPaginated(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	var env testsuite.WorkflowTestSuite
	testEnv := env.NewTestActivityEnvironment()
	testEnv.RegisterActivity(a.ListOntapModePoolsPaginated)

	expectedPools := []*database.PoolIdentifier{
		{UUID: "pool-1", Name: "pool-name-1", AccountID: 1},
	}
	mockSE.On("ListPoolUUIDsPaginated", mock.Anything, mock.Anything, 0, 100).Return(expectedPools, nil)

	result, err := testEnv.ExecuteActivity(a.ListOntapModePoolsPaginated, 0, 100)
	assert.NoError(t, err)

	var pools []*database.PoolIdentifier
	assert.NoError(t, result.Get(&pools))
	assert.Len(t, pools, 1)
	assert.Equal(t, "pool-1", pools[0].UUID)
	mockSE.AssertExpectations(t)
}

func TestListOntapModePoolsPaginated_Error(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	var env testsuite.WorkflowTestSuite
	testEnv := env.NewTestActivityEnvironment()
	testEnv.RegisterActivity(a.ListOntapModePoolsPaginated)

	mockSE.On("ListPoolUUIDsPaginated", mock.Anything, mock.Anything, 10, 50).Return(nil, errors.New("db error"))

	_, err := testEnv.ExecuteActivity(a.ListOntapModePoolsPaginated, 10, 50)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list ONTAP mode pools")
	mockSE.AssertExpectations(t)
}

func mockGetOntapRestProviderForPool(t *testing.T, provider vsa.Provider) {
	t.Helper()
	original := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(_ context.Context, _ database.Storage, _ *datamodel.Pool) (vsa.Provider, error) {
		return provider, nil
	}
	t.Cleanup(func() { GetOntapRestProviderForPool = original })
}

func TestSyncExpertModeVolumesForPool_Success(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-1"},
		AccountID: 10,
		Name:      "test-pool",
	}
	poolIdentifier := &database.PoolIdentifier{UUID: "pool-uuid-1", Name: "test-pool"}

	ontapVolumes := []*vsa.Volume{
		{
			Volume: ontaprestmodel.Volume{
				Name:  nillable.ToPointer("new-vol"),
				UUID:  nillable.ToPointer("new-ext-uuid"),
				Svm:   &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
				Space: &ontaprestmodel.VolumeInlineSpace{Size: nillable.ToPointer(int64(2048))},
				Style: nillable.ToPointer("flexvol"),
			},
			ExternalUUID: "new-ext-uuid",
		},
	}

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.On("GetVolumes").Return(ontapVolumes, nil)
	mockGetOntapRestProviderForPool(t, mockProvider)

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-1").Return(pool, nil)
	mockSE.On("ListExpertModeVolumesByPoolID", mock.Anything, int64(100)).Return([]*datamodel.ExpertModeVolumes{}, nil)
	mockSE.On("GetSvmsByPoolID", mock.Anything, int64(100)).Return([]*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm-1"},
	}, nil)
	mockSE.On("CreateExpertModeVolume", mock.Anything, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
		return v.Name == "new-vol" && v.ExternalUUID == "new-ext-uuid"
	})).Return(&datamodel.ExpertModeVolumes{}, nil)

	err := a.syncExpertModeVolumesForPool(ctx, poolIdentifier)
	assert.NoError(t, err)
	mockSE.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_GetPoolError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	mockSE.On("GetPoolByUUID", mock.Anything, "missing-pool").Return(nil, errors.New("not found"))

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "missing-pool", Name: "missing"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get pool")
	mockSE.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_OntapProviderError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-2"},
		Name:      "test-pool",
	}

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-2").Return(pool, nil)

	original := GetOntapRestProviderForPool
	GetOntapRestProviderForPool = func(_ context.Context, _ database.Storage, _ *datamodel.Pool) (vsa.Provider, error) {
		return nil, errors.New("provider unavailable")
	}
	t.Cleanup(func() { GetOntapRestProviderForPool = original })

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "pool-uuid-2", Name: "test-pool"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ONTAP volumes")
	mockSE.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_GetVolumesError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-3"},
		Name:      "test-pool",
	}

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-3").Return(pool, nil)

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.On("GetVolumes").Return(nil, errors.New("ontap unreachable"))
	mockGetOntapRestProviderForPool(t, mockProvider)

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "pool-uuid-3", Name: "test-pool"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get ONTAP volumes")
	mockSE.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_ListExpertModeVolumesError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-4"}, Name: "test-pool"}

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)
	mockGetOntapRestProviderForPool(t, mockProvider)

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-4").Return(pool, nil)
	mockSE.On("ListExpertModeVolumesByPoolID", mock.Anything, int64(100)).Return(nil, errors.New("db error"))

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "pool-uuid-4", Name: "test-pool"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get DB expert mode volumes")
	mockSE.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_GetSvmsError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-5"}, Name: "test-pool"}

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.On("GetVolumes").Return([]*vsa.Volume{}, nil)
	mockGetOntapRestProviderForPool(t, mockProvider)

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-5").Return(pool, nil)
	mockSE.On("ListExpertModeVolumesByPoolID", mock.Anything, int64(100)).Return([]*datamodel.ExpertModeVolumes{}, nil)
	mockSE.On("GetSvmsByPoolID", mock.Anything, int64(100)).Return(nil, errors.New("db error"))

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "pool-uuid-5", Name: "test-pool"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get SVMs")
	mockSE.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

func TestSyncExpertModeVolumesForPool_ReconcileError(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	a := &SyncExpertModeVolumesActivity{SE: mockSE}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 100, UUID: "pool-uuid-6"}, AccountID: 10, Name: "test-pool"}

	// One ONTAP volume needing a create; the create fails so reconcileExpertModeVolumes returns an
	// aggregate error that syncExpertModeVolumesForPool propagates.
	ontapVolumes := []*vsa.Volume{
		{
			Volume: ontaprestmodel.Volume{
				Name: nillable.ToPointer("new-vol"),
				UUID: nillable.ToPointer("new-ext-uuid"),
				Svm:  &ontaprestmodel.VolumeInlineSvm{Name: nillable.ToPointer("svm-1")},
			},
			ExternalUUID: "new-ext-uuid",
		},
	}

	mockProvider := vsa.NewMockProvider(t)
	mockProvider.On("GetVolumes").Return(ontapVolumes, nil)
	mockGetOntapRestProviderForPool(t, mockProvider)

	mockSE.On("GetPoolByUUID", mock.Anything, "pool-uuid-6").Return(pool, nil)
	mockSE.On("ListExpertModeVolumesByPoolID", mock.Anything, int64(100)).Return([]*datamodel.ExpertModeVolumes{}, nil)
	mockSE.On("GetSvmsByPoolID", mock.Anything, int64(100)).Return([]*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 1}, Name: "svm-1"},
	}, nil)
	mockSE.On("CreateExpertModeVolume", mock.Anything, mock.Anything).Return((*datamodel.ExpertModeVolumes)(nil), errors.New("db error"))

	err := a.syncExpertModeVolumesForPool(ctx, &database.PoolIdentifier{UUID: "pool-uuid-6", Name: "test-pool"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconciliation incomplete")
	mockSE.AssertExpectations(t)
	mockProvider.AssertExpectations(t)
}

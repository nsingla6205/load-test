package backgroundactivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func TestShouldSkipExpertModeVolumeReconcile(t *testing.T) {
	assert.False(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateCreating))
	assert.False(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateUpdating))
	assert.True(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateDeleting))
	assert.True(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateDeleted))
	assert.False(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateSplitting))
	assert.False(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateAvailable))
	assert.False(t, shouldSkipExpertModeVolumeReconcile(datamodel.LifeCycleStateREADY))
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

	t.Run("SkipsVolumesDeletingOrDeleted", func(tt *testing.T) {
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

		added, updated, deleted, err := reconcileExpertModeVolumes(ctx, mockSE, pool, ontapVolumes, dbVolumes, svmNameToID)
		assert.NoError(tt, err)
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

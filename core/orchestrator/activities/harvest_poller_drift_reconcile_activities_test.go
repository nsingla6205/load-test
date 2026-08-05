package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func overrideHarvestEndpoint(t *testing.T, srv *httptest.Server) {
	t.Helper()
	oldHost, oldProto := harvestEndPoint, harvestRestProtocol
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	harvestRestProtocol = "http"
	t.Cleanup(func() {
		harvestEndPoint = oldHost
		harvestRestProtocol = oldProto
	})
}

func newSQLMockGormDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	return gormDB, smock
}

func deletedAt() *gorm.DeletedAt {
	return &gorm.DeletedAt{Time: time.Now().UTC(), Valid: true}
}

func nodeWithPoolID(nodeID, poolID int64) *datamodel.Node {
	return &datamodel.Node{
		BaseModel: datamodel.BaseModel{ID: nodeID},
		PoolID:    poolID,
	}
}

func expectPoolResolutionByNode(mockSE *database.MockStorage, nodeID, poolID int64, pool *datamodel.Pool, callTimes ...int) {
	times := 1
	if len(callTimes) > 0 {
		times = callTimes[0]
	}
	mockSE.On("GetNodeByID", mock.Anything, nodeID).Return(nodeWithPoolID(nodeID, poolID), nil).Times(times)
	mockSE.On("GetPoolByID", mock.Anything, poolID).Return(pool, nil).Times(times)
}

func emptyLeaseCleanupContext() (map[string]struct{}, map[string]int64) {
	return map[string]struct{}{}, map[string]int64{}
}

func expectEmptyNodeGroupSoftDelete(mockSE *database.MockStorage, nodeGroupID int64, leaseName string) {
	mockSE.On("GetNodeGroup", mock.Anything, nodeGroupID).Return(&datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{ID: nodeGroupID},
		LeaseName: leaseName,
	}, nil)
	mockSE.On("DeleteNodeGroup", mock.Anything, nodeGroupID).Return(nil)
}

func simulateRetryStateUpdateOnMap(mapRow *datamodel.NodeNodeGroupMap, mutate database.HarvestRetryStateMutator) {
	if mapRow == nil || mutate == nil {
		return
	}
	current := cloneHarvestRetryState(mapRow.RetryState)
	next, err := mutate(current)
	if err != nil {
		panic(err)
	}
	mapRow.RetryState = cloneHarvestRetryState(next)
}

func expectRetryStateUpdate(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, callTimes ...int) {
	call := mockSE.On("UpdateNodeNodeGroupMapRetryState", mock.Anything, mapRow.ID, mock.Anything).
		Run(func(args mock.Arguments) {
			mutate := args.Get(2).(database.HarvestRetryStateMutator)
			simulateRetryStateUpdateOnMap(mapRow, mutate)
		}).Return(nil)
	if len(callTimes) > 0 {
		call.Times(callTimes[0])
	}
}

func expectCreateRetryStateUpdate(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, callTimes ...int) {
	expectRetryStateUpdate(mockSE, mapRow, callTimes...)
}

func expectCreateRetryStateUpdateError(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, err error) {
	mockSE.On("UpdateNodeNodeGroupMapRetryState", mock.Anything, mapRow.ID, mock.Anything).Return(err)
}

func expectDeleteRetrySuccessUpdate(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, callTimes ...int) {
	expectRetryStateUpdate(mockSE, mapRow, callTimes...)
}

func expectDeleteRetryFailureUpdate(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, callTimes ...int) {
	expectRetryStateUpdate(mockSE, mapRow, callTimes...)
}

func expectDeleteRetryStateUpdateError(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, err error) {
	mockSE.On("UpdateNodeNodeGroupMapRetryState", mock.Anything, mapRow.ID, mock.Anything).Return(err)
}

func expectHarvestConfigPersistUpdate(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap) {
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapRow, nil).Once()
}

func expectActiveNodeNodeGroupMapForUpload(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap, callTimes ...int) {
	times := 1
	if len(callTimes) > 0 {
		times = callTimes[0]
	}
	mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapRow.NodeID, mock.Anything).Return(mapRow, nil).Times(times)
}

func expectActiveThenInactiveNodeNodeGroupMapForUpload(mockSE *database.MockStorage, mapRow *datamodel.NodeNodeGroupMap) {
	mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapRow.NodeID, mock.Anything).
		Return(mapRow, nil).Once()
	mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapRow.NodeID, mock.Anything).
		Return(nil, utilerrors.NewNotFoundErr("node_node_group_map", nil)).Once()
}

func expectNoActiveNodeNodeGroupMapForUpload(mockSE *database.MockStorage, nodeID int64) {
	mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, nodeID, mock.Anything).
		Return(nil, utilerrors.NewNotFoundErr("node_node_group_map", nil))
}

func expectLeaseCleanupDiscovery(mockSE *database.MockStorage) {
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)
}

func stubLeaseCleanupDeletes(t *testing.T) {
	t.Helper()
	oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
	oldDeleteLease := deleteKubernetesLeaseForDriftReconcile
	deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error { return nil }
	deleteKubernetesLeaseForDriftReconcile = func(ctx context.Context, leaseNS, leaseName string) error { return nil }
	t.Cleanup(func() {
		deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder
		deleteKubernetesLeaseForDriftReconcile = oldDeleteLease
	})
}

func expectPaginatedActiveMapPasses(mockSE *database.MockStorage, passes ...[]*datamodel.NodeNodeGroupMap) {
	for _, maps := range passes {
		expectPaginatedActiveMaps(mockSE, maps)
	}
}

func expectPaginatedActiveMaps(mockSE *database.MockStorage, maps []*datamodel.NodeNodeGroupMap) {
	afterID := int64(0)
	remaining := maps
	for {
		var page []*datamodel.NodeNodeGroupMap
		if len(remaining) > harvestDriftReconcileMapPageSize {
			page = remaining[:harvestDriftReconcileMapPageSize]
			remaining = remaining[harvestDriftReconcileMapPageSize:]
		} else {
			page = remaining
			remaining = nil
		}
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, afterID, harvestDriftReconcileMapPageSize).
			Return(page, nil).Once()
		if len(page) == 0 || len(page) < harvestDriftReconcileMapPageSize {
			return
		}
		afterID = page[len(page)-1].ID
		if len(remaining) == 0 {
			mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, afterID, harvestDriftReconcileMapPageSize).
				Return([]*datamodel.NodeNodeGroupMap{}, nil).Once()
			return
		}
	}
}

func TestParseHarvestInventory_Formats(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		entries, emptyLeases, err := parseHarvestInventory([]byte("  "))
		require.NoError(t, err)
		assert.Nil(t, entries)
		assert.Nil(t, emptyLeases)
	})

	t.Run("array of objects", func(t *testing.T) {
		body := []byte(`[{"leaseName":"lease-a","node_id":42,"file_name":"harvest-42.yaml"}]`)
		entries, emptyLeases, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Empty(t, emptyLeases)
		assert.Equal(t, "lease-a", entries[0].LeaseName)
		assert.Equal(t, int64(42), entries[0].NodeID)
	})

	t.Run("wrapped items and files", func(t *testing.T) {
		body := []byte(`{"items":[{"lease_name":"l1","nodeID":1}],"files":[{"file":"harvest-9.yaml"}]}`)
		entries, emptyLeases, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Empty(t, emptyLeases)
		assert.Equal(t, int64(1), entries[0].NodeID)
		assert.Equal(t, int64(9), entries[1].NodeID)
	})

	t.Run("empty lease markers", func(t *testing.T) {
		body := []byte(`[
			{"leaseName":"lease-a","node_id":42,"file_name":"harvest-42.yaml"},
			{"leaseName":"lease-empty","empty":true}
		]`)
		entries, emptyLeases, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, []string{"lease-empty"}, emptyLeases)
	})

	t.Run("string filenames", func(t *testing.T) {
		body := []byte(`["harvest-5.yaml","invalid.txt"]`)
		entries, emptyLeases, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Empty(t, emptyLeases)
		assert.Equal(t, int64(5), entries[0].NodeID)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, _, err := parseHarvestInventory([]byte(`{"broken":`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unable to parse harvest inventory response")
	})
}

func TestNodeIDFromFileName(t *testing.T) {
	nodeID, ok := nodeIDFromFileName("harvest-123.yaml")
	assert.True(t, ok)
	assert.Equal(t, int64(123), nodeID)

	_, ok = nodeIDFromFileName("not-a-harvest.yaml")
	assert.False(t, ok)
}

func TestFlattenInventoryObjectList_SkipsInvalidRows(t *testing.T) {
	entries, emptyLeases := flattenInventoryObjectList([]map[string]interface{}{
		{"leaseName": "l1", "node_id": float64(10)},
		{"file_name": "harvest-20.yaml"},
		{"node_id": 0},
		{"leaseName": "lease-empty", "empty": true},
	})
	require.Len(t, entries, 2)
	assert.Equal(t, int64(10), entries[0].NodeID)
	assert.Equal(t, int64(20), entries[1].NodeID)
	assert.Equal(t, []string{"lease-empty"}, emptyLeases)
}

func TestMapHelpers(t *testing.T) {
	item := map[string]interface{}{
		"lease_name": "lease-x",
		"node_id":    "77",
	}
	assert.Equal(t, "lease-x", getStringFromMap(item, "leaseName", "lease_name"))
	nodeID, ok := getInt64FromMap(item, "nodeID", "node_id")
	assert.True(t, ok)
	assert.Equal(t, int64(77), nodeID)
	assert.Equal(t, "lease-x|77", pollerInventoryKey(" lease-x ", 77))
}

func TestIsPoolDeleted(t *testing.T) {
	assert.False(t, isPoolDeleted(nil))
	assert.False(t, isPoolDeleted(&datamodel.Pool{}))
	assert.True(t, isPoolDeleted(&datamodel.Pool{BaseModel: datamodel.BaseModel{DeletedAt: deletedAt()}}))
}

func TestIsCheckpointNotFoundErr(t *testing.T) {
	assert.True(t, isCheckpointNotFoundErr(utilerrors.NewNotFoundErr("app config", nil)))
	assert.True(t, isCheckpointNotFoundErr(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.New("missing"))))
	assert.False(t, isCheckpointNotFoundErr(errors.New("other")))
}

func TestRecordDriftReconcileHeartbeat_NoPanicOutsideActivity(t *testing.T) {
	require.NotPanics(t, func() {
		recordDriftReconcileHeartbeat(context.Background(), "unit-test heartbeat")
	})
}

func TestFlowRetryStateAndSetters(t *testing.T) {
	mapping := &datamodel.NodeNodeGroupMap{
		RetryState: &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{Retries: 2, ShouldRetry: true},
			Delete: &datamodel.HarvestFlowRetryState{Retries: 1, ShouldRetry: true},
		},
	}
	assert.True(t, hasFlowRetryRemaining(mapping, harvestRetryFlowCreate))
	assert.True(t, hasFlowRetryRemaining(mapping, harvestRetryFlowDelete))

	mapping.RetryState.Create = nil
	setFlowRetryState(mapping, harvestRetryFlowCreate, &datamodel.HarvestFlowRetryState{
		Retries:     3,
		ShouldRetry: true,
		LastRunMsg:  "queued",
	})
	require.NotNil(t, mapping.RetryState.Create)
	assert.Equal(t, 3, mapping.RetryState.Create.Retries)
	assert.Equal(t, "queued", mapping.RetryState.Create.LastRunMsg)

	assert.True(t, shouldProcessDeleteCandidate(nil))
	assert.True(t, shouldProcessDeleteCandidate(&datamodel.NodeNodeGroupMap{}))
	assert.True(t, shouldProcessDeleteCandidate(mapping))

	exhausted := &datamodel.NodeNodeGroupMap{
		RetryState: &datamodel.HarvestRetryState{
			Delete: &datamodel.HarvestFlowRetryState{
				Retries:     harvestDriftReconcileRetryLimit,
				ShouldRetry: false,
				LastRunMsg:  "farm delete failed",
			},
		},
	}
	assert.False(t, shouldProcessDeleteCandidate(exhausted))
}

func TestShouldProcessDeleteCandidate(t *testing.T) {
	pending := &datamodel.NodeNodeGroupMap{
		RetryState: &datamodel.HarvestRetryState{
			Delete: &datamodel.HarvestFlowRetryState{Retries: 2, ShouldRetry: true},
		},
	}
	assert.True(t, shouldProcessDeleteCandidate(pending))

	succeeded := &datamodel.NodeNodeGroupMap{
		RetryState: &datamodel.HarvestRetryState{
			Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: false, LastRunMsg: "success"},
		},
	}
	assert.True(t, shouldProcessDeleteCandidate(succeeded))
}

func TestDeleteHarvestPollerFromFarmClient(t *testing.T) {
	originalBucketName := pollerRebalanceBucketName
	pollerRebalanceBucketName = ""
	t.Cleanup(func() { pollerRebalanceBucketName = originalBucketName })

	t.Run("empty lease is no-op", func(t *testing.T) {
		require.NoError(t, deleteHarvestPollerFromFarmClient(context.Background(), "", 1))
	})

	t.Run("delegates to farm delete helper", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromPod
		deleteHarvestPollerFromPod = func(ctx context.Context, leaseName string, nodeID int64) error {
			assert.Equal(t, "lease-a", leaseName)
			assert.Equal(t, int64(7), nodeID)
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromPod = oldDelete })
		require.NoError(t, deleteHarvestPollerFromFarmClient(context.Background(), "lease-a", 7))
	})
}

func TestListHarvestPollerFiles_HTTP(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, harvestConfigListPath, r.URL.Path)
			_, _ = w.Write([]byte(`["harvest-11.yaml"]`))
		}))
		overrideHarvestEndpoint(t, srv)

		entries, emptyLeases, err := listHarvestPollerFiles(context.Background())
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Empty(t, emptyLeases)
		assert.Equal(t, int64(11), entries[0].NodeID)
	})

	t.Run("non-success status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
		}))
		overrideHarvestEndpoint(t, srv)

		_, _, err := listHarvestPollerFiles(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list harvest config files failed")
	})
}

func TestGetWindowStart(t *testing.T) {
	ctx := context.Background()
	runStart := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("missing checkpoint uses full historical scan", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
			Return(nil, utilerrors.NewNotFoundErr("app config", nil))
		act.SE = mockSE

		start, fullScan, err := act.getWindowStart(ctx, runStart)
		require.NoError(t, err)
		assert.True(t, fullScan)
		assert.True(t, start.IsZero())
	})

	t.Run("invalid checkpoint uses full historical scan", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
			Return(&datamodel.AppConfig{Value: "not-a-timestamp"}, nil)
		act.SE = mockSE

		start, fullScan, err := act.getWindowStart(ctx, runStart)
		require.NoError(t, err)
		assert.True(t, fullScan)
		assert.True(t, start.IsZero())
	})

	t.Run("valid checkpoint", func(t *testing.T) {
		checkpoint := runStart.Add(-2 * time.Hour).Format(time.RFC3339Nano)
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
			Return(&datamodel.AppConfig{Value: checkpoint}, nil)
		act.SE = mockSE

		start, fullScan, err := act.getWindowStart(ctx, runStart)
		require.NoError(t, err)
		assert.False(t, fullScan)
		assert.Equal(t, checkpoint, start.Format(time.RFC3339Nano))
	})
}

func TestListReadyPoolIDsInWindow(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}
	windowStart := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)

	t.Run("full historical scan omits time filter", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT "id" FROM "pools"`).
			WithArgs(datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateUpdating).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)).AddRow(int64(202)))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		ids, err := act.listReadyPoolIDsInWindow(ctx, time.Time{}, windowEnd, true)
		require.NoError(t, err)
		assert.Equal(t, []int64{101, 202}, ids)
		require.NoError(t, smock.ExpectationsWereMet())
	})

	t.Run("incremental window applies created and updated bounds", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT "id" FROM "pools"`).
			WithArgs(
				datamodel.LifeCycleStateREADY,
				datamodel.LifeCycleStateUpdating,
				windowStart, windowEnd,
				windowStart, windowEnd,
			).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(303)))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		ids, err := act.listReadyPoolIDsInWindow(ctx, windowStart, windowEnd, false)
		require.NoError(t, err)
		assert.Equal(t, []int64{303}, ids)
		require.NoError(t, smock.ExpectationsWereMet())
	})
}

func TestIsPoolEligibleForHarvestCreateDrift(t *testing.T) {
	assert.True(t, isPoolEligibleForHarvestCreateDrift(readyPool(1, datamodel.LifeCycleStateREADY)))
	assert.True(t, isPoolEligibleForHarvestCreateDrift(readyPool(1, datamodel.LifeCycleStateUpdating)))

	assert.False(t, isPoolEligibleForHarvestCreateDrift(nil))
	assert.False(t, isPoolEligibleForHarvestCreateDrift(readyPool(1, datamodel.LifeCycleStateCreating)))

	deletedPool := readyPool(1, datamodel.LifeCycleStateREADY)
	deletedPool.DeletedAt = deletedAt()
	assert.False(t, isPoolEligibleForHarvestCreateDrift(deletedPool))
}

func TestResolvePoolForMapCached(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}
	cache := newHarvestDriftPoolCache()
	cachedPool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 42},
		Name:      "pool-cached",
		State:     datamodel.LifeCycleStateREADY,
	}
	cache.byNodeID[10] = cachedPool

	t.Run("cache hit avoids storage lookup", func(t *testing.T) {
		pool, err := act.resolvePoolForMapCached(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10}, cache)
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(42), pool.ID)
	})

	t.Run("cache miss populates cache", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		expectPoolResolutionByNode(mockSE, 11, 55, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 55},
			Name:      "pool-miss",
			State:     datamodel.LifeCycleStateREADY,
		})
		act.SE = mockSE

		pool, err := act.resolvePoolForMapCached(ctx, &datamodel.NodeNodeGroupMap{NodeID: 11}, cache)
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(55), pool.ID)
		assert.Same(t, pool, cache.byNodeID[11])
		assert.Same(t, pool, cache.byID[55])
	})
}

func TestResolvePoolForMap(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("missing node id", func(t *testing.T) {
		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})

	t.Run("pool resolved by node pool_id", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		expectPoolResolutionByNode(mockSE, 10, 99, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 99},
			State:     datamodel.LifeCycleStateREADY,
		})
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(99), pool.ID)
	})

	t.Run("soft-deleted pool fallback", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT (.+) FROM (.+)pools(.+)`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "state", "deleted_at"}).
				AddRow(55, "pool-b", datamodel.LifeCycleStateREADY, time.Now()))

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nodeWithPoolID(10, 55), nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(55)).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(55), pool.ID)
	})

	t.Run("unscoped fallback not found returns nil pool", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT (.+) FROM (.+)pools(.+)`).
			WillReturnError(gorm.ErrRecordNotFound)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nodeWithPoolID(10, 77), nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(77)).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})
}

func TestHandleInventoryDeleteCandidate(t *testing.T) {
	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10, FileName: "harvest-10.yaml"}

	t.Run("orphan yaml", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).
			Return(nil, utilerrors.NewNotFoundErr("map", nil))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.True(t, orphan)
	})

	t.Run("soft-deleted map", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1, DeletedAt: deletedAt()},
			NodeID:      10,
			NodeGroupID: 42,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, nodeGroupID, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
		assert.Equal(t, int64(42), nodeGroupID)
	})

	t.Run("active map with ready pool is skipped", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 2},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectPoolResolutionByNode(mockSE, 10, 3, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 3},
			State:     datamodel.LifeCycleStateREADY,
		})
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
	})

	t.Run("stale delete retry on live pool is cleared", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 6},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 1},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7},
			State:     datamodel.LifeCycleStateREADY,
		})
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
		require.NotNil(t, mapRow.RetryState.Delete)
		assert.False(t, mapRow.RetryState.Delete.ShouldRetry)
		assert.Contains(t, mapRow.RetryState.Delete.LastRunMsg, "pool not deleted")
	})

	t.Run("empty lease name is skipped", func(t *testing.T) {
		act := &HarvestPollerDriftReconcileActivity{SE: database.NewMockStorage(t)}
		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, HarvestPollerFileEntry{NodeID: 10})
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
	})

	t.Run("deleted pool triggers cleanup", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 4},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectPoolResolutionByNode(mockSE, 10, 5, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 5, DeletedAt: deletedAt()},
			State:     datamodel.LifeCycleStateREADY,
		})
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(4)).Return(nil)
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
	})

	t.Run("recorded delete success on soft-deleted map still retries cleanup", func(t *testing.T) {
		deleteCalls := 0
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			deleteCalls++
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 5, DeletedAt: deletedAt()},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{
					ShouldRetry: false,
					LastRunMsg:  "success",
				},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, nodeGroupID, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
		assert.Equal(t, int64(0), nodeGroupID)
		assert.Equal(t, 1, deleteCalls)
	})

	t.Run("exhausted delete retry on soft-deleted map is skipped", func(t *testing.T) {
		deleteCalls := 0
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			deleteCalls++
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 5, DeletedAt: deletedAt()},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{
					Retries:     harvestDriftReconcileRetryLimit,
					ShouldRetry: false,
					LastRunMsg:  "farm delete failed",
				},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, nodeGroupID, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
		assert.Equal(t, int64(0), nodeGroupID)
		assert.Equal(t, 0, deleteCalls)
	})

	t.Run("exhausted delete retry on deleted pool is skipped", func(t *testing.T) {
		deleteCalls := 0
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			deleteCalls++
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 8},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{
					Retries:     harvestDriftReconcileRetryLimit,
					ShouldRetry: false,
					LastRunMsg:  "farm delete failed",
				},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectPoolResolutionByNode(mockSE, 10, 5, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 5, DeletedAt: deletedAt()},
			State:     datamodel.LifeCycleStateREADY,
		})
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
		assert.Equal(t, 0, deleteCalls)
	})
}

func TestApplyHarvestFlowRetryOutcome(t *testing.T) {
	t.Run("first failure increments retries and records source run id", func(t *testing.T) {
		state := &datamodel.HarvestFlowRetryState{}
		applyHarvestFlowRetryOutcome(state, false, "upload failed", "run-abc")
		assert.Equal(t, 1, state.Retries)
		assert.Equal(t, "run-abc", state.LastSourceRunID)
		assert.Equal(t, "upload failed", state.LastRunMsg)
		assert.True(t, state.ShouldRetry)
	})

	t.Run("same source run id is idempotent on activity retry", func(t *testing.T) {
		state := &datamodel.HarvestFlowRetryState{
			Retries:         1,
			LastSourceRunID: "run-abc",
			ShouldRetry:     true,
		}
		applyHarvestFlowRetryOutcome(state, false, "upload failed again", "run-abc")
		assert.Equal(t, 1, state.Retries)
		assert.Equal(t, "upload failed again", state.LastRunMsg)
		assert.True(t, state.ShouldRetry)
	})

	t.Run("new source run id increments again", func(t *testing.T) {
		state := &datamodel.HarvestFlowRetryState{
			Retries:         1,
			LastSourceRunID: "run-abc",
			ShouldRetry:     true,
		}
		applyHarvestFlowRetryOutcome(state, false, "still failing", "run-def")
		assert.Equal(t, 2, state.Retries)
		assert.Equal(t, "run-def", state.LastSourceRunID)
		assert.Equal(t, "still failing", state.LastRunMsg)
	})

	t.Run("success clears should retry", func(t *testing.T) {
		state := &datamodel.HarvestFlowRetryState{
			Retries:         3,
			LastSourceRunID: "run-abc",
			ShouldRetry:     true,
			LastRunMsg:      "old failure",
		}
		applyHarvestFlowRetryOutcome(state, true, "", "run-xyz")
		assert.Equal(t, 3, state.Retries)
		assert.Equal(t, "success", state.LastRunMsg)
		assert.False(t, state.ShouldRetry)
	})

	t.Run("empty source run id always increments", func(t *testing.T) {
		state := &datamodel.HarvestFlowRetryState{Retries: 1}
		applyHarvestFlowRetryOutcome(state, false, "retry", "")
		assert.Equal(t, 2, state.Retries)
	})
}

func TestMergePersistedFlowRetryState(t *testing.T) {
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 10},
		NodeID:    20,
	}
	persisted := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 10},
		NodeID:    20,
		RetryState: &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{
				Retries:         1,
				LastSourceRunID: "run-abc",
				ShouldRetry:     true,
				LastRunMsg:      "upload failed",
			},
		},
	}

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeNodeGroupMap", mock.Anything, int64(10)).Return(persisted, nil)

	require.NoError(t, mergePersistedFlowRetryState(ctx, mockSE, mapping, harvestRetryFlowCreate))
	require.NotNil(t, mapping.RetryState.Create)
	assert.Equal(t, 1, mapping.RetryState.Create.Retries)
	assert.Equal(t, "run-abc", mapping.RetryState.Create.LastSourceRunID)

	applyHarvestFlowRetryOutcome(mapping.RetryState.Create, false, "upload failed again", "run-abc")
	assert.Equal(t, 1, mapping.RetryState.Create.Retries)
	assert.Equal(t, "upload failed again", mapping.RetryState.Create.LastRunMsg)
}

// TestFetchNodeNodeGroupMapForRetryMerge_SoftDeletedRow verifies that once GetNodeNodeGroupMap is
// Unscoped, the merge helper can still read a row that has already been soft-deleted (the state
// after DeleteNodeGroupMapping runs, before MarkDeleteRetryState persists harvest_retry_state.delete).
func TestFetchNodeNodeGroupMapForRetryMerge_SoftDeletedRow(t *testing.T) {
	ctx := context.Background()
	deletedAt := time.Now().UTC()
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 23},
		NodeID:    27,
		RetryState: &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{
				LastRunMsg:  "success",
				ShouldRetry: false,
			},
		},
	}
	deletedAtField := gorm.DeletedAt{Time: deletedAt, Valid: true}
	persisted := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 23, DeletedAt: &deletedAtField},
		NodeID:    27,
		RetryState: &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{
				LastRunMsg:  "success",
				ShouldRetry: false,
			},
		},
	}

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeNodeGroupMap", mock.Anything, int64(23)).Return(persisted, nil).Once()

	got, err := fetchNodeNodeGroupMapForRetryMerge(ctx, mockSE, mapping)
	require.NoError(t, err)
	require.Equal(t, persisted, got)
}

// TestMergePersistedFlowRetryState_AfterSoftDeletePreservesCreateAndMarksDelete covers the reported
// bug end to end: unregister soft-deletes the map row, then MarkDeleteRetryState must still merge
// the persisted create state and add delete state on the same row.
func TestMergePersistedFlowRetryState_AfterSoftDeletePreservesCreateAndMarksDelete(t *testing.T) {
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 23},
		NodeID:    27,
		RetryState: &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{
				LastRunMsg:  "success",
				ShouldRetry: false,
			},
		},
	}

	mockSE := database.NewMockStorage(t)
	mockSE.On("UpdateNodeNodeGroupMapRetryState", mock.Anything, int64(23), mock.Anything).
		Run(func(args mock.Arguments) {
			mutate := args.Get(2).(database.HarvestRetryStateMutator)
			current := &datamodel.HarvestRetryState{
				Create: &datamodel.HarvestFlowRetryState{
					LastRunMsg:  "success",
					ShouldRetry: false,
				},
			}
			next, mutateErr := mutate(current)
			require.NoError(t, mutateErr)
			require.NotNil(t, next)
			require.NotNil(t, next.Create)
			assert.Equal(t, "success", next.Create.LastRunMsg)
			assert.False(t, next.Create.ShouldRetry)
			require.NotNil(t, next.Delete)
			assert.Equal(t, "success", next.Delete.LastRunMsg)
			assert.False(t, next.Delete.ShouldRetry)
		}).Return(nil).Once()

	require.NoError(t, setHarvestNodeMapDeleteRetryState(ctx, mockSE, mapping, true, "success"))
}

// TestFetchNodeNodeGroupMapForRetryMerge_FallsBackToNodeIDWhenIDMissing covers the case where the
// caller only has NodeID (no map ID yet); merge falls back to GetNodeNodeGroupMapByNodeID.
func TestFetchNodeNodeGroupMapForRetryMerge_FallsBackToNodeIDWhenIDMissing(t *testing.T) {
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{
		NodeID: 27,
	}
	persisted := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 23},
		NodeID:    27,
	}

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(27)).Return(persisted, nil).Once()

	got, err := fetchNodeNodeGroupMapForRetryMerge(ctx, mockSE, mapping)
	require.NoError(t, err)
	require.Equal(t, persisted, got)
}

func TestMarkRetryHelpers(t *testing.T) {
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel:  datamodel.BaseModel{ID: 1},
		RetryState: &datamodel.HarvestRetryState{},
	}
	mockSE := database.NewMockStorage(t)
	expectCreateRetryStateUpdate(mockSE, mapping, 6)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	queued, exhausted, err := act.markRetryFailure(ctx, mapping, "upload failed")
	require.NoError(t, err)
	assert.Equal(t, 1, queued)
	assert.Equal(t, 0, exhausted)
	require.NotNil(t, mapping.RetryState.Create)
	assert.True(t, mapping.RetryState.Create.ShouldRetry)

	mapping.RetryState.Create.Retries = harvestDriftReconcileRetryLimit - 1
	queued, exhausted, err = act.markRetryFailure(ctx, mapping, "final failure")
	require.NoError(t, err)
	assert.Equal(t, 0, queued)
	assert.Equal(t, 1, exhausted)
	assert.False(t, mapping.RetryState.Create.ShouldRetry)

	require.NoError(t, act.markRetrySuccess(ctx, mapping))
	assert.False(t, mapping.RetryState.Create.ShouldRetry)
	assert.Equal(t, "success", mapping.RetryState.Create.LastRunMsg)

	require.NoError(t, act.markRetryTerminal(ctx, mapping, "pool is non-READY; stopping create retry"))
	assert.False(t, mapping.RetryState.Create.ShouldRetry)

	require.NoError(t, act.markDeleteRetryFailure(ctx, mapping, "delete failed"))
	require.NotNil(t, mapping.RetryState.Delete)
	assert.True(t, mapping.RetryState.Delete.ShouldRetry)

	require.NoError(t, act.markDeleteRetrySuccess(ctx, mapping))
	assert.False(t, mapping.RetryState.Delete.ShouldRetry)
}

func TestReconcileReadyPoolsAndRetries_NoCandidates(t *testing.T) {
	ctx := context.Background()
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, nil)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	created, queued, exhausted, err := act.reconcileReadyPoolsAndRetries(
		ctx,
		map[string]struct{}{},
		time.Now().Add(-time.Hour),
		time.Now(),
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, queued)
	assert.Equal(t, 0, exhausted)
}

func TestReconcileHarvestPollerDrift_EmptyLeaseInventoryNotDeleted(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "", "node_id": 99, "file_name": "harvest-99.yaml"},
		{"leaseName": "lease-valid", "node_id": 100, "file_name": "harvest-100.yaml"},
	})
	require.NoError(t, err)

	deleteCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write(inventoryBody)
		case r.Method == http.MethodDelete:
			deleteCalls++
			assert.Contains(t, r.URL.Path, "/config/lease-valid/harvest-100")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(100)).
		Return(nil, utilerrors.NewNotFoundErr("map", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 1, deleteCalls)
	mockSE.AssertNotCalled(t, "GetNodeNodeGroupMapByNodeID", mock.Anything, int64(99))
}

func TestReconcileHarvestPollerDrift_EmptyInventory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.DeletedYAMLs)
	assert.Equal(t, 0, result.CreatedYAMLs)
	mockSE.AssertExpectations(t)
}

func TestReconcileHarvestPollerDrift_OrphanDelete(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-orphan", "node_id": 88, "file_name": "harvest-88.yaml"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write(inventoryBody)
		case r.Method == http.MethodDelete:
			assert.Contains(t, r.URL.Path, "/config/lease-orphan/harvest-88")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(88)).
		Return(nil, utilerrors.NewNotFoundErr("map", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 1, result.OrphanYAMLHandled)
	assert.Equal(t, 1, result.DeletedLeases)
	mockSE.AssertExpectations(t)
}

func TestReconcileHarvestPollerDrift_BucketEmptyLeaseCleanup(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-empty", "empty": true},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(inventoryBody)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.DeletedYAMLs)
	assert.Equal(t, 1, result.DeletedLeases)
}

func TestReconcileHarvestPollerDrift_CheckpointPersistError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).
		Return(errors.New("checkpoint write failed"))

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	_, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint write failed")
}

func TestListActiveNodeNodeGroupMapsPaginated(t *testing.T) {
	t.Run("single page", func(t *testing.T) {
		m1 := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 10}
		mockSE := database.NewMockStorage(t)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{m1})
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		rows, err := act.listActiveNodeNodeGroupMapsPaginated(context.Background())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, int64(10), rows[0].NodeID)
	})

	t.Run("multi page", func(t *testing.T) {
		page := make([]*datamodel.NodeNodeGroupMap, harvestDriftReconcileMapPageSize)
		for i := range page {
			page[i] = &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: int64(i + 1)}, NodeID: int64(i + 1)}
		}
		tail := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: harvestDriftReconcileMapPageSize + 1}, NodeID: harvestDriftReconcileMapPageSize + 1}

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, int64(0), harvestDriftReconcileMapPageSize).
			Return(page, nil).Once()
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, int64(harvestDriftReconcileMapPageSize), harvestDriftReconcileMapPageSize).
			Return([]*datamodel.NodeNodeGroupMap{tail}, nil).Once()
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		rows, err := act.listActiveNodeNodeGroupMapsPaginated(context.Background())
		require.NoError(t, err)
		assert.Len(t, rows, harvestDriftReconcileMapPageSize+1)
	})

	t.Run("list error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, int64(0), harvestDriftReconcileMapPageSize).
			Return(nil, errors.New("db list failed"))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		_, err := act.listActiveNodeNodeGroupMapsPaginated(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db list failed")
	})
}

func TestListHarvestPollerInventory_FiltersEmptyLeaseName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"leaseName":"","node_id":1,"file_name":"harvest-1.yaml"},
			{"leaseName":"lease-a","node_id":2,"file_name":"harvest-2.yaml"}
		]`))
	}))
	overrideHarvestEndpoint(t, srv)

	inventory, err := listHarvestPollerInventory(context.Background())
	require.NoError(t, err)
	require.Len(t, inventory.FileEntries, 1)
	assert.Equal(t, "lease-a", inventory.FileEntries[0].LeaseName)
	assert.Equal(t, int64(2), inventory.FileEntries[0].NodeID)
	_, ok := inventory.InventorySet["lease-a|2"]
	assert.True(t, ok)
	_, ok = inventory.InventorySet["|1"]
	assert.False(t, ok)
}

func TestListHarvestPollerInventory_ParsesEmptyLeases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"leaseName":"lease-a","node_id":2,"file_name":"harvest-2.yaml"},
			{"leaseName":"lease-empty","empty":true}
		]`))
	}))
	overrideHarvestEndpoint(t, srv)

	inventory, err := listHarvestPollerInventory(context.Background())
	require.NoError(t, err)
	require.Len(t, inventory.FileEntries, 1)
	assert.Equal(t, []string{"lease-empty"}, inventory.EmptyLeases)
}

func TestReconcileReadyPoolsAndRetries_CredentialFetchFailureQueuesRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(201, "pool-cred", "lease-cred", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 9, readyPool(9, datamodel.LifeCycleStateREADY), 2)
	expectActiveNodeNodeGroupMapForUpload(mockSE, mapping)
	expectCreateRetryStateUpdate(mockSE, mapping)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = false
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldFetch := fetchOnTapCredentialsForDriftReconcile
	fetchOnTapCredentialsForDriftReconcile = func(ctx context.Context, pool *datamodel.Pool) (*vlm.OntapCredentials, error) {
		return nil, errors.New("secret manager unavailable")
	}
	t.Cleanup(func() { fetchOnTapCredentialsForDriftReconcile = oldFetch })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.CreatedYAMLs)
	assert.Equal(t, 1, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
	require.NotNil(t, mapping.RetryState.Create)
	assert.True(t, mapping.RetryState.Create.ShouldRetry)
	assert.Contains(t, mapping.RetryState.Create.LastRunMsg, "credential fetch failed")
}

func TestClearStaleDeleteRetry(t *testing.T) {
	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:  datamodel.BaseModel{ID: 1},
		RetryState: &datamodel.HarvestRetryState{Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 2}},
	}
	mockSE := database.NewMockStorage(t)
	expectDeleteRetrySuccessUpdate(mockSE, mapRow)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	require.NoError(t, act.clearStaleDeleteRetry(context.Background(), mapRow))
	assert.False(t, mapRow.RetryState.Delete.ShouldRetry)
	assert.Contains(t, mapRow.RetryState.Delete.LastRunMsg, "pool not deleted")
}

func TestProcessDeleteCandidate_DeleteFailureMarksRetry(t *testing.T) {
	oldDelete := deleteHarvestPollerFromFarm
	deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
		return fmt.Errorf("network down")
	}
	t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 9},
		NodeID:    10,
		RetryState: &datamodel.HarvestRetryState{
			Delete: &datamodel.HarvestFlowRetryState{},
		},
	}
	mockSE := database.NewMockStorage(t)
	expectDeleteRetryFailureUpdate(mockSE, mapRow)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	handled, orphan, err := act.processDeleteCandidate(context.Background(), HarvestPollerFileEntry{
		LeaseName: "lease-a",
		NodeID:    10,
	}, mapRow, false)
	require.Error(t, err)
	assert.False(t, handled)
	assert.False(t, orphan)
	require.NotNil(t, mapRow.RetryState.Delete)
	assert.True(t, mapRow.RetryState.Delete.ShouldRetry)
}

func readyPool(nodeID int64, state string) *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: nodeID},
		State:           state,
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, SecretID: "secret"},
	}
}

func readyPoolMapping(nodeID int64, poolName, leaseName string, retryCreate bool) *datamodel.NodeNodeGroupMap {
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: nodeID},
		NodeID:      nodeID,
		NodeGroupID: 1,
		NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 1}, LeaseName: leaseName},
		HarvestConfig: &datamodel.HarvestConfig{
			POOL_NAME:  poolName,
			PORT:       "13001",
			LEASE_NAME: leaseName,
		},
	}
	if retryCreate {
		mapping.RetryState = &datamodel.HarvestRetryState{
			Create: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 1},
		}
	}
	return mapping
}

func TestReconcileHarvestPollerDrift_UploadFailureQueuesRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			http.Error(w, "upload failed", http.StatusInternalServerError)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(101, "pool-1", "lease-1", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 7, readyPool(7, datamodel.LifeCycleStateREADY), 3)
	expectActiveNodeNodeGroupMapForUpload(mockSE, mapping, 2)
	expectHarvestConfigPersistUpdate(mockSE, mapping)
	expectCreateRetryStateUpdate(mockSE, mapping)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.CreatedYAMLs)
	assert.Equal(t, 1, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
}

func TestReconcileReadyPoolsAndRetries_SkipsUploadWhenMapNoLongerActive(t *testing.T) {
	ctx := context.Background()
	postCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		w.WriteHeader(http.StatusOK)
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(105, "pool-stale", "lease-stale", true)
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 16, readyPool(16, datamodel.LifeCycleStateUpdating))
	expectNoActiveNodeNodeGroupMapForUpload(mockSE, mapping.NodeID)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	created, queued, exhausted, err := act.reconcileReadyPoolsAndRetries(
		ctx,
		map[string]struct{}{},
		time.Now().Add(-time.Hour),
		time.Now(),
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, queued)
	assert.Equal(t, 0, exhausted)
	assert.Equal(t, 0, postCalls)
}

func TestReadyForHarvestCreateUpload(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("returns false when mapping is no longer active", func(t *testing.T) {
		mapping := readyPoolMapping(105, "pool-stale", "lease-stale", true)
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapping.NodeID, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("node_node_group_map", nil))
		act.SE = mockSE

		pool, ready, err := act.readyForHarvestCreateUpload(ctx, mapping)
		require.NoError(t, err)
		assert.False(t, ready)
		assert.Nil(t, pool)
	})

	t.Run("marks terminal and skips when pool deleted before upload", func(t *testing.T) {
		mapping := readyPoolMapping(106, "pool-del", "lease-del", true)
		deletedPool := readyPool(16, datamodel.LifeCycleStateREADY)
		deletedPool.DeletedAt = deletedAt()

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapping.NodeID, mock.Anything).Return(mapping, nil)
		expectPoolResolutionByNode(mockSE, mapping.NodeID, 16, deletedPool)
		expectCreateRetryStateUpdate(mockSE, mapping)
		act.SE = mockSE

		pool, ready, err := act.readyForHarvestCreateUpload(ctx, mapping)
		require.NoError(t, err)
		assert.False(t, ready)
		assert.Nil(t, pool)
	})

	t.Run("returns fresh eligible pool before upload", func(t *testing.T) {
		mapping := readyPoolMapping(107, "pool-ok", "lease-ok", false)
		eligiblePool := readyPool(17, datamodel.LifeCycleStateUpdating)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetActiveNodeNodeGroupMapByNodeID", mock.Anything, mapping.NodeID, mock.Anything).Return(mapping, nil)
		expectPoolResolutionByNode(mockSE, mapping.NodeID, 17, eligiblePool)
		act.SE = mockSE

		pool, ready, err := act.readyForHarvestCreateUpload(ctx, mapping)
		require.NoError(t, err)
		assert.True(t, ready)
		require.NotNil(t, pool)
		assert.Equal(t, eligiblePool, pool)
	})
}

func TestReconcileReadyPoolsAndRetries_SkipsUploadWhenMapInactiveBeforeUpload(t *testing.T) {
	postCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		w.WriteHeader(http.StatusOK)
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(108, "pool-race", "lease-race", true)
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 18, readyPool(18, datamodel.LifeCycleStateREADY), 2)
	expectActiveThenInactiveNodeNodeGroupMapForUpload(mockSE, mapping)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.CreatedYAMLs)
	assert.Equal(t, 0, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
	assert.Equal(t, 0, postCalls)
}

func TestReconcileReadyPoolsAndRetries_SkipsUploadWhenPoolDeletedBeforeUpload(t *testing.T) {
	postCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls++
		}
		w.WriteHeader(http.StatusOK)
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(109, "pool-race-del", "lease-race-del", true)
	eligiblePool := readyPool(19, datamodel.LifeCycleStateREADY)
	deletedPool := readyPool(19, datamodel.LifeCycleStateREADY)
	deletedPool.DeletedAt = deletedAt()

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetNodeByID", mock.Anything, mapping.NodeID).Return(nodeWithPoolID(mapping.NodeID, 19), nil).Times(3)
	mockSE.On("GetPoolByID", mock.Anything, int64(19)).Return(eligiblePool, nil).Twice()
	mockSE.On("GetPoolByID", mock.Anything, int64(19)).Return(deletedPool, nil).Once()
	expectActiveNodeNodeGroupMapForUpload(mockSE, mapping, 2)
	expectCreateRetryStateUpdate(mockSE, mapping)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.CreatedYAMLs)
	assert.Equal(t, 0, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
	assert.Equal(t, 0, postCalls)
	assert.False(t, mapping.RetryState.Create.ShouldRetry)
}

func TestReconcileReadyPoolsAndRetries_DeletedPoolMarksCreateRetryTerminal(t *testing.T) {
	ctx := context.Background()
	mapping := readyPoolMapping(104, "pool-deleted-retry", "lease-del-retry", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 12, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 12, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	expectCreateRetryStateUpdate(mockSE, mapping)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	created, queued, exhausted, err := act.reconcileReadyPoolsAndRetries(
		ctx,
		map[string]struct{}{},
		time.Now().Add(-time.Hour),
		time.Now(),
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, queued)
	assert.Equal(t, 0, exhausted)
	require.NotNil(t, mapping.RetryState.Create)
	assert.False(t, mapping.RetryState.Create.ShouldRetry)
	assert.Contains(t, mapping.RetryState.Create.LastRunMsg, "pool is deleted")
}

func TestReconcileReadyPoolsAndRetries_IneligiblePoolStateMarksRetryTerminal(t *testing.T) {
	ctx := context.Background()
	mapping := readyPoolMapping(102, "pool-2", "lease-2", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 8, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 8},
		State:     datamodel.LifeCycleStateCreating,
	})
	expectCreateRetryStateUpdate(mockSE, mapping)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	created, queued, exhausted, err := act.reconcileReadyPoolsAndRetries(
		ctx,
		map[string]struct{}{},
		time.Now().Add(-time.Hour),
		time.Now(),
		false,
	)
	require.NoError(t, err)
	assert.Equal(t, 0, created)
	assert.Equal(t, 0, queued)
	assert.Equal(t, 0, exhausted)
	require.NotNil(t, mapping.RetryState.Create)
	assert.False(t, mapping.RetryState.Create.ShouldRetry)
	assert.Contains(t, mapping.RetryState.Create.LastRunMsg, "not eligible for create retry")
	assert.Contains(t, mapping.RetryState.Create.LastRunMsg, datamodel.LifeCycleStateCreating)
}

func TestReconcileReadyPoolsAndRetries_YamlAlreadyPresentSkipsCreate(t *testing.T) {
	mapping := readyPoolMapping(103, "pool-3", "lease-3", false)
	mapping.NodeGroup.LeaseName = "lease-3"

	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-3", "node_id": 103, "file_name": "harvest-103.yaml"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(inventoryBody)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))

	pool := readyPool(8, datamodel.LifeCycleStateREADY)
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(103)).Return(mapping, nil)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	// Delete pass + cleanup + create-pass candidate discovery resolve pool via node; ready-pool
	// discovery loads the same pool ID directly from listReadyPoolIDsInWindow.
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 8, pool, 3)
	mockSE.On("GetPoolByID", mock.Anything, int64(8)).Return(pool, nil).Once()
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.CreatedYAMLs)
	assert.Equal(t, 0, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
}

func TestReconcileHarvestPollerDrift_InventoryListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable"))
	}))
	overrideHarvestEndpoint(t, srv)

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	_, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list harvest config files failed")
}

func TestGetWindowStart_DBError(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, errors.New("db unavailable"))
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	_, _, err := act.getWindowStart(context.Background(), time.Now().UTC())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db unavailable")
}

func TestHandleInventoryDeleteCandidate_MapLookupError(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).
		Return(nil, errors.New("db read failed"))
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	_, _, _, err := act.handleInventoryDeleteCandidate(context.Background(), HarvestPollerFileEntry{
		LeaseName: "lease-a",
		NodeID:    10,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db read failed")
}

func TestCountInventoryEntriesForLease(t *testing.T) {
	inventorySet := map[string]struct{}{
		"lease-a|1": {},
		"lease-a|2": {},
		"lease-b|3": {},
	}
	assert.Equal(t, 2, countInventoryEntriesForLease(inventorySet, "lease-a"))
	assert.Equal(t, 1, countInventoryEntriesForLease(inventorySet, "lease-b"))
	assert.Equal(t, 0, countInventoryEntriesForLease(inventorySet, "lease-c"))
	assert.Equal(t, 0, countInventoryEntriesForLease(inventorySet, "  "))
}

func TestNodeGroupIDFromMap(t *testing.T) {
	assert.Equal(t, int64(0), nodeGroupIDFromMap(nil))
	assert.Equal(t, int64(7), nodeGroupIDFromMap(&datamodel.NodeNodeGroupMap{NodeGroupID: 7}))
}

func TestNodeGroupIDsByLeaseName(t *testing.T) {
	rows := []datamodel.NodeGroupPollerCount{
		{NodeGroupID: 1, LeaseName: " lease-a ", Count: 0},
		{NodeGroupID: 2, LeaseName: "lease-b", Count: 3},
		{NodeGroupID: 0, LeaseName: "", Count: 0},
	}
	m := nodeGroupIDsByLeaseName(rows)
	assert.Equal(t, int64(1), m["lease-a"])
	assert.Equal(t, int64(2), m["lease-b"])
	assert.NotContains(t, m, "")
}

func TestRecordLeaseCleanupCandidate(t *testing.T) {
	candidates := map[string]int64{}

	recordLeaseCleanupCandidate(candidates, "  ", 1)
	assert.Empty(t, candidates)

	recordLeaseCleanupCandidate(candidates, "lease-a", 0)
	assert.Equal(t, int64(0), candidates["lease-a"])

	recordLeaseCleanupCandidate(candidates, "lease-a", 5)
	assert.Equal(t, int64(5), candidates["lease-a"])

	recordLeaseCleanupCandidate(candidates, "lease-b", 0)
	recordLeaseCleanupCandidate(candidates, "lease-b", 0)
	assert.Equal(t, int64(0), candidates["lease-b"])
}

func TestNodeGroupIDForLease(t *testing.T) {
	index := map[string]int64{"lease-a": 5, "lease-b": 9}
	assert.Equal(t, int64(0), nodeGroupIDForLease("", index))
	assert.Equal(t, int64(0), nodeGroupIDForLease("lease-a", nil))
	assert.Equal(t, int64(5), nodeGroupIDForLease("lease-a", index))
	assert.Equal(t, int64(9), nodeGroupIDForLease(" lease-b ", index))
	assert.Equal(t, int64(0), nodeGroupIDForLease("missing", index))
}

func TestResolveNodeGroupIDForLease(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("empty lease", func(t *testing.T) {
		assert.Equal(t, int64(0), act.resolveNodeGroupIDForLease(ctx, "  "))
	})

	t.Run("list error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return(nil, errors.New("list failed"))
		act.SE = mockSE
		assert.Equal(t, int64(0), act.resolveNodeGroupIDForLease(ctx, "lease-x"))
	})

	t.Run("match found", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{
				{NodeGroupID: 3, LeaseName: " lease-y ", Count: 0},
			}, nil)
		act.SE = mockSE
		assert.Equal(t, int64(3), act.resolveNodeGroupIDForLease(ctx, "lease-y"))
	})

	t.Run("no match", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{
				{NodeGroupID: 1, LeaseName: "other", Count: 0},
			}, nil)
		act.SE = mockSE
		assert.Equal(t, int64(0), act.resolveNodeGroupIDForLease(ctx, "lease-z"))
	})
}

func TestCleanupEmptyLeasesAfterDeletePass(t *testing.T) {
	ctx := context.Background()

	t.Run("discovers zero-poller node groups", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{
				{NodeGroupID: 9, LeaseName: "lease-empty", Count: 0},
				{NodeGroupID: 2, LeaseName: "lease-busy", Count: 3},
			}, nil)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(9)).Return(int64(0), nil).Twice()
		expectEmptyNodeGroupSoftDelete(mockSE, 9, "lease-empty")

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{}, nil)
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
	})

	t.Run("list error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return(nil, errors.New("db list failed"))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		_, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db list failed")
	})

	t.Run("merges bucket-reported empty leases", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{}, nil)

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{}, []string{"lease-from-bucket"})
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
	})

	t.Run("bucket empty lease resolves node group from fetched rows", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{
				{NodeGroupID: 42, LeaseName: "lease-from-bucket", Count: 0},
			}, nil).Once()
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(42)).Return(int64(0), nil).Twice()
		expectEmptyNodeGroupSoftDelete(mockSE, 42, "lease-from-bucket")

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{}, []string{"lease-from-bucket"})
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
	})

	t.Run("bucket empty lease skips cleanup when node group still has pollers", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1},
			NodeID:      10,
			NodeGroupID: 42,
			NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 42}, LeaseName: "lease-busy"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{
				{NodeGroupID: 42, LeaseName: "lease-busy", Count: 3},
			}, nil).Once()
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(42)).Return(int64(3), nil)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(42)).Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, readyPool(7, datamodel.LifeCycleStateREADY))

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{}, []string{"lease-busy"})
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})

	t.Run("cleanup error is warn-and-continue", func(t *testing.T) {
		oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
		deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error {
			return errors.New("folder delete failed")
		}
		t.Cleanup(func() { deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder })

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{}, nil)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{"lease-a": 5}, nil)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})
}

func TestTryCleanupEmptyLease(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("skips when inventory still has yaml for lease", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{}, nil)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		inventorySet := map[string]struct{}{"lease-a|1": {}}
		candidates := map[string]int64{"lease-a": 5}
		deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, inventorySet, candidates, nil)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})

	t.Run("deletes k8s lease when inventory empty and node group has no active pollers", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil).Twice()
		expectEmptyNodeGroupSoftDelete(mockSE, 5, "lease-a")
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		cleaned, nodeGroupDeleted, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.NoError(t, err)
		assert.True(t, cleaned)
		assert.True(t, nodeGroupDeleted)
	})

	t.Run("skips when node group still has active pollers on live pool", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1},
			NodeID:      10,
			NodeGroupID: 5,
			NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 5}, LeaseName: "lease-a"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(2), nil)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(5)).Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, readyPool(7, datamodel.LifeCycleStateREADY))
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		cleaned, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("empty lease name is no-op", func(t *testing.T) {
		inv, cand := emptyLeaseCleanupContext()
		cleaned, _, err := act.tryCleanupEmptyLease(ctx, "  ", 5, inv, cand, nil)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("resolves node group when id is zero", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(11)).Return(int64(0), nil).Twice()
		expectEmptyNodeGroupSoftDelete(mockSE, 11, "lease-r")
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		leaseIndex := map[string]int64{"lease-r": 11}
		cleaned, nodeGroupDeleted, err := act.tryCleanupEmptyLease(ctx, "lease-r", 0, inv, cand, leaseIndex)
		require.NoError(t, err)
		assert.True(t, cleaned)
		assert.True(t, nodeGroupDeleted)
		mockSE.AssertNotCalled(t, "ListNodeGroupsWithPollerCounts", mock.Anything)
	})

	t.Run("skips k8s lease when harvest folder not empty", func(t *testing.T) {
		oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
		deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error {
			return fmt.Errorf("delete harvest lease folder %s: folder not empty (HTTP 409)", leaseName)
		}
		t.Cleanup(func() { deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder })

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		cleaned, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("returns folder delete error when not not-empty", func(t *testing.T) {
		oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
		deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error {
			return errors.New("filesystem error")
		}
		t.Cleanup(func() { deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder })

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		_, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filesystem error")
	})

	t.Run("k8s lease already gone is warn-and-continue", func(t *testing.T) {
		oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
		oldDeleteLease := deleteKubernetesLeaseForDriftReconcile
		deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error { return nil }
		deleteKubernetesLeaseForDriftReconcile = func(ctx context.Context, leaseNS, leaseName string) error {
			return errors.New(`leases.coordination.k8s.io "lease-a" not found`)
		}
		t.Cleanup(func() {
			deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder
			deleteKubernetesLeaseForDriftReconcile = oldDeleteLease
		})

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		cleaned, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("k8s lease delete error propagates", func(t *testing.T) {
		oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
		oldDeleteLease := deleteKubernetesLeaseForDriftReconcile
		deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error { return nil }
		deleteKubernetesLeaseForDriftReconcile = func(ctx context.Context, leaseNS, leaseName string) error {
			return errors.New("forbidden")
		}
		t.Cleanup(func() {
			deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder
			deleteKubernetesLeaseForDriftReconcile = oldDeleteLease
		})

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		_, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forbidden")
	})

	t.Run("node count lookup error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).
			Return(int64(0), errors.New("count failed"))
		act.SE = mockSE

		inv, cand := emptyLeaseCleanupContext()
		_, _, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5, inv, cand, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count failed")
	})
}

func TestReconcileHarvestPollerDrift_DeletedPoolLeaseCleanup(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-del", "node_id": 55, "file_name": "harvest-55.yaml"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(inventoryBody)
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{ID: 20},
		NodeID:        55,
		NodeGroupID:   8,
		HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-gone"},
	}

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(55)).Return(mapRow, nil)
	expectPoolResolutionByNode(mockSE, 55, 30, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 30, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(20)).Return(nil)
	expectDeleteRetrySuccessUpdate(mockSE, mapRow)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(8)).Return(int64(0), nil)
	expectEmptyNodeGroupSoftDelete(mockSE, 8, "lease-del")
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 1, result.DeletedLeases)
	assert.Equal(t, 1, result.SoftDeletedNodeGroups)
}

func TestReconcileHarvestPollerDrift_LeaseCleanupSkippedWhenFolderNotEmpty(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-stub", "node_id": 88, "file_name": "harvest-88.yaml"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(inventoryBody)
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	oldDeleteFolder := deleteHarvestLeaseConfigFolderForDriftReconcile
	deleteHarvestLeaseConfigFolderForDriftReconcile = func(ctx context.Context, leaseName string) error {
		return fmt.Errorf("delete harvest lease folder %s: folder not empty (HTTP 409)", leaseName)
	}
	t.Cleanup(func() { deleteHarvestLeaseConfigFolderForDriftReconcile = oldDeleteFolder })

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(88)).
		Return(nil, utilerrors.NewNotFoundErr("map", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 0, result.DeletedLeases)
}

func TestIsEmptyLeaseInventoryItem(t *testing.T) {
	assert.False(t, isEmptyLeaseInventoryItem(map[string]interface{}{"leaseName": "l1"}))
	assert.True(t, isEmptyLeaseInventoryItem(map[string]interface{}{"empty": true}))
	assert.True(t, isEmptyLeaseInventoryItem(map[string]interface{}{"empty": "true"}))
	assert.True(t, isEmptyLeaseInventoryItem(map[string]interface{}{"empty": " TRUE "}))
	assert.False(t, isEmptyLeaseInventoryItem(map[string]interface{}{"empty": "false"}))
	assert.False(t, isEmptyLeaseInventoryItem(map[string]interface{}{"empty": 1}))
}

func TestProcessDeleteCandidate(t *testing.T) {
	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10, FileName: "harvest-10.yaml"}

	t.Run("delete failure marks delete retry", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return errors.New("farm delete failed")
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 1, DeletedAt: deletedAt()},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		expectDeleteRetryFailureUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, err := act.processDeleteCandidate(ctx, entry, mapRow, false)
		require.Error(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
		require.NotNil(t, mapRow.RetryState.Delete)
		assert.True(t, mapRow.RetryState.Delete.ShouldRetry)
		assert.Contains(t, mapRow.RetryState.Delete.LastRunMsg, "farm delete failed")
	})

	t.Run("soft-deleted map marks delete retry success", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 2, DeletedAt: deletedAt()},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, err := act.processDeleteCandidate(ctx, entry, mapRow, false)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
		require.NotNil(t, mapRow.RetryState.Delete)
		assert.False(t, mapRow.RetryState.Delete.ShouldRetry)
	})

	t.Run("active map with delete retry marks delete retry success", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return nil
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 3},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 2},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(3)).Return(nil)
		expectDeleteRetrySuccessUpdate(mockSE, mapRow)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, err := act.processDeleteCandidate(ctx, entry, mapRow, false)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
		require.NotNil(t, mapRow.RetryState.Delete)
		assert.False(t, mapRow.RetryState.Delete.ShouldRetry)
		assert.Equal(t, "success", mapRow.RetryState.Delete.LastRunMsg)
	})
}

func TestHandleInventoryDeleteCandidate_SoftDeletedNodeDeletedPool(t *testing.T) {
	oldDelete := deleteHarvestPollerFromFarm
	deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
		return nil
	}
	t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10, FileName: "harvest-10.yaml"}
	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 4},
		NodeID:    10,
	}

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT (.+) FROM (.+)pools(.+)`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "state", "deleted_at"}).
			AddRow(5, "pool-gone", datamodel.LifeCycleStateREADY, time.Now()))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
	softDeletedNode := nodeWithPoolID(10, 5)
	softDeletedNode.DeletedAt = deletedAt()
	mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(softDeletedNode, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(5)).
		Return(nil, utilerrors.NewNotFoundErr("pool", nil))
	mockSE.On("DB").Return(gormDB)
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(4)).Return(nil)
	expectDeleteRetrySuccessUpdate(mockSE, mapRow)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
	require.NoError(t, err)
	assert.True(t, handled)
	assert.False(t, orphan)
}

func TestResolvePoolForMap_NodeLookupEdgeCases(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("node not found returns nil pool", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).
			Return(nil, utilerrors.NewNotFoundErr("node", nil))
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})

	t.Run("node with zero pool_id returns nil pool", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).
			Return(&datamodel.Node{BaseModel: datamodel.BaseModel{ID: 10}, PoolID: 0}, nil)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})

	t.Run("node lookup error propagates", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).
			Return(nil, errors.New("node db error"))
		act.SE = mockSE

		_, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node db error")
	})
}

func TestReconcileHarvestPollerDrift_SuccessfulCreate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapping := readyPoolMapping(301, "pool-create", "lease-create", false)
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(15)))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("DB").Return(gormDB)
	mockSE.On("GetPoolByID", mock.Anything, int64(15)).
		Return(readyPool(15, datamodel.LifeCycleStateREADY), nil).Once()
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapping}, []*datamodel.NodeNodeGroupMap{mapping})
	expectPoolResolutionByNode(mockSE, mapping.NodeID, 15, readyPool(15, datamodel.LifeCycleStateREADY), 3)
	expectActiveNodeNodeGroupMapForUpload(mockSE, mapping, 2)
	expectHarvestConfigPersistUpdate(mockSE, mapping)
	expectCreateRetryStateUpdate(mockSE, mapping)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldLeaseExists := leaseExists
	leaseExists = func(ctx context.Context, leaseNameSpace, leaseName string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { leaseExists = oldLeaseExists })

	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	t.Cleanup(func() { smHarvestAuthEnabled = oldAuth })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.CreatedYAMLs)
	assert.Equal(t, 0, result.RetryQueued)
	assert.Equal(t, 0, result.RetryExhausted)
}

func TestCleanupEmptyLeasesAfterDeletePass_MergesDBAndBucketCandidates(t *testing.T) {
	ctx := context.Background()
	stubLeaseCleanupDeletes(t)

	mockSE := database.NewMockStorage(t)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{
			{NodeGroupID: 9, LeaseName: "lease-db-empty", Count: 0},
		}, nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(9)).Return(int64(0), nil).Twice()
	expectEmptyNodeGroupSoftDelete(mockSE, 9, "lease-db-empty")
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(0)).Return(int64(0), nil).Maybe()

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(
		ctx,
		map[string]struct{}{},
		map[string]int64{},
		[]string{"lease-bucket-empty"},
	)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted)
}

func TestCleanupEmptyLeasesAfterDeletePass_SkipsBucketEmptyWhenYAMLPresent(t *testing.T) {
	ctx := context.Background()
	stubLeaseCleanupDeletes(t)

	mockSE := database.NewMockStorage(t)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inventorySet := map[string]struct{}{"lease-busy|42": {}}
	deleted, _, err := act.cleanupEmptyLeasesAfterDeletePass(
		ctx,
		inventorySet,
		map[string]int64{},
		[]string{"lease-busy"},
	)
	require.NoError(t, err)
	assert.Equal(t, 0, deleted)
}

func TestReconcileHarvestPollerDrift_MergedEmptyLeaseCleanup(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-del", "node_id": 55, "file_name": "harvest-55.yaml"},
		{"leaseName": "lease-bucket-only", "empty": true},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(inventoryBody)
		case http.MethodDelete:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{ID: 20},
		NodeID:        55,
		NodeGroupID:   8,
		HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-gone"},
	}

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(55)).Return(mapRow, nil)
	expectPoolResolutionByNode(mockSE, 55, 30, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 30, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(20)).Return(nil)
	expectDeleteRetrySuccessUpdate(mockSE, mapRow)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{
			{NodeGroupID: 12, LeaseName: "lease-db-empty", Count: 0},
		}, nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(8)).Return(int64(0), nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(12)).Return(int64(0), nil)
	expectEmptyNodeGroupSoftDelete(mockSE, 8, "lease-del")
	expectEmptyNodeGroupSoftDelete(mockSE, 12, "lease-db-empty")
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 3, result.DeletedLeases)
	assert.Equal(t, 2, result.SoftDeletedNodeGroups)
}

func TestCleanupMapsForDeletedPools_SoftDeletesMapWithoutYAML(t *testing.T) {
	ctx := context.Background()
	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: 1},
		NodeID:      10,
		NodeGroupID: 5,
		NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 5}, LeaseName: "lease-a"},
	}
	mockSE := database.NewMockStorage(t)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapRow})
	expectPoolResolutionByNode(mockSE, 10, 7, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 7, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(1)).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inventorySet := map[string]struct{}{}
	candidates := map[string]int64{}
	softDeleted, yamlDeleted, err := act.cleanupMapsForDeletedPools(ctx, inventorySet, candidates)
	require.NoError(t, err)
	assert.Equal(t, 1, softDeleted)
	assert.Equal(t, 0, yamlDeleted)
	assert.Equal(t, int64(5), candidates["lease-a"])
}

func TestCleanupMapsForDeletedPools_DeletesYAMLWhenInInventory(t *testing.T) {
	ctx := context.Background()
	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: 2},
		NodeID:      20,
		NodeGroupID: 6,
		NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 6}, LeaseName: "lease-b"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mockSE := database.NewMockStorage(t)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapRow})
	expectPoolResolutionByNode(mockSE, 20, 8, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 8, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(2)).Return(nil)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inventorySet := map[string]struct{}{"lease-b|20": {}}
	candidates := map[string]int64{}
	softDeleted, yamlDeleted, err := act.cleanupMapsForDeletedPools(ctx, inventorySet, candidates)
	require.NoError(t, err)
	assert.Equal(t, 1, softDeleted)
	assert.Equal(t, 1, yamlDeleted)
	_, ok := inventorySet["lease-b|20"]
	assert.False(t, ok)
}

func TestTryCleanupEmptyLease_DeletedPoolMapsRemovedThenLeaseCleaned(t *testing.T) {
	ctx := context.Background()
	stubLeaseCleanupDeletes(t)

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: 3},
		NodeID:      30,
		NodeGroupID: 7,
		NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 7}, LeaseName: "lease-c"},
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(7)).Return(int64(1), nil).Once()
	mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(7)).Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
	expectPoolResolutionByNode(mockSE, 30, 9, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 9, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(3)).Return(nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(7)).Return(int64(0), nil).Twice()
	expectEmptyNodeGroupSoftDelete(mockSE, 7, "lease-c")

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inv, cand := emptyLeaseCleanupContext()
	cleaned, nodeGroupDeleted, err := act.tryCleanupEmptyLease(ctx, "lease-c", 7, inv, cand, nil)
	require.NoError(t, err)
	assert.True(t, cleaned)
	assert.True(t, nodeGroupDeleted)
}

func TestSoftDeleteNodeGroupIfEmpty(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(11)).Return(int64(0), nil)
	expectEmptyNodeGroupSoftDelete(mockSE, 11, "lease-z")

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 11, "lease-z")
	require.NoError(t, err)
	assert.True(t, deleted)
}

func TestEnsureMapLeaseName(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("nil mapping", func(t *testing.T) {
		lease, err := act.ensureMapLeaseName(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, lease)
	})

	t.Run("lease from embedded node group", func(t *testing.T) {
		mapping := &datamodel.NodeNodeGroupMap{
			NodeGroup: &datamodel.NodeGroup{LeaseName: " lease-x "},
		}
		lease, err := act.ensureMapLeaseName(ctx, mapping)
		require.NoError(t, err)
		assert.Equal(t, "lease-x", lease)
	})

	t.Run("fetches node group when not embedded", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroup", mock.Anything, int64(5)).Return(&datamodel.NodeGroup{
			BaseModel: datamodel.BaseModel{ID: 5},
			LeaseName: "lease-fetched",
		}, nil)
		act.SE = mockSE

		lease, err := act.ensureMapLeaseName(ctx, &datamodel.NodeNodeGroupMap{NodeGroupID: 5})
		require.NoError(t, err)
		assert.Equal(t, "lease-fetched", lease)
	})

	t.Run("zero node group id", func(t *testing.T) {
		lease, err := act.ensureMapLeaseName(ctx, &datamodel.NodeNodeGroupMap{})
		require.NoError(t, err)
		assert.Empty(t, lease)
	})

	t.Run("get node group error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroup", mock.Anything, int64(5)).Return(nil, errors.New("db error"))
		act.SE = mockSE

		_, err := act.ensureMapLeaseName(ctx, &datamodel.NodeNodeGroupMap{NodeGroupID: 5})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
	})

	t.Run("nil node group returned", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroup", mock.Anything, int64(5)).Return(nil, nil)
		act.SE = mockSE

		lease, err := act.ensureMapLeaseName(ctx, &datamodel.NodeNodeGroupMap{NodeGroupID: 5})
		require.NoError(t, err)
		assert.Empty(t, lease)
	})

	t.Run("empty embedded lease falls through to fetch", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroup", mock.Anything, int64(6)).Return(&datamodel.NodeGroup{LeaseName: "from-db"}, nil)
		act.SE = mockSE

		mapping := &datamodel.NodeNodeGroupMap{
			NodeGroupID: 6,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "  "},
		}
		lease, err := act.ensureMapLeaseName(ctx, mapping)
		require.NoError(t, err)
		assert.Equal(t, "from-db", lease)
	})
}

func TestSoftDeleteNodeGroupIfEmpty_EdgeCases(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("zero node group id", func(t *testing.T) {
		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 0, "lease")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("active pollers remain", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(2), nil)
		act.SE = mockSE

		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("node count error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), errors.New("count err"))
		act.SE = mockSE

		_, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "count err")
	})

	t.Run("get node group error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(nil, errors.New("get err"))
		act.SE = mockSE

		_, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get err")
	})

	t.Run("nil node group", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(nil, nil)
		act.SE = mockSE

		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("lease name mismatch", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(&datamodel.NodeGroup{LeaseName: "other"}, nil)
		act.SE = mockSE

		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease-a")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("delete not found is no-op", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(&datamodel.NodeGroup{LeaseName: "lease-a"}, nil)
		mockSE.On("DeleteNodeGroup", mock.Anything, int64(1)).Return(utilerrors.NewNotFoundErr("node group", nil))
		act.SE = mockSE

		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease-a")
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("delete error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(&datamodel.NodeGroup{LeaseName: "lease-a"}, nil)
		mockSE.On("DeleteNodeGroup", mock.Anything, int64(1)).Return(errors.New("delete failed"))
		act.SE = mockSE

		_, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "lease-a")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete failed")
	})

	t.Run("empty lease name skips mismatch check", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(1)).Return(int64(0), nil)
		mockSE.On("GetNodeGroup", mock.Anything, int64(1)).Return(&datamodel.NodeGroup{LeaseName: "any"}, nil)
		mockSE.On("DeleteNodeGroup", mock.Anything, int64(1)).Return(nil)
		act.SE = mockSE

		deleted, err := act.softDeleteNodeGroupIfEmpty(ctx, 1, "")
		require.NoError(t, err)
		assert.True(t, deleted)
	})
}

func TestReconcileDeletedPoolMapsOnNodeGroup(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("zero node group id returns true", func(t *testing.T) {
		ok, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 0, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("list maps error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).
			Return(nil, errors.New("list err"))
		act.SE = mockSE

		_, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, map[string]struct{}{}, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list err")
	})

	t.Run("live pool blocks cleanup", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1},
			NodeID:      10,
			NodeGroupID: 1,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).
			Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, readyPool(7, datamodel.LifeCycleStateREADY))
		act.SE = mockSE

		ok, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("all deleted pools soft-deletes maps", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1},
			NodeID:      10,
			NodeGroupID: 1,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-a"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).
			Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 7, DeletedAt: deletedAt()},
		})
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(1)).Return(nil)
		act.SE = mockSE

		ok, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("skips nil and already soft-deleted maps", func(t *testing.T) {
		maps := []*datamodel.NodeNodeGroupMap{
			nil,
			{BaseModel: datamodel.BaseModel{ID: 2, DeletedAt: deletedAt()}, NodeID: 11},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).Return(maps, nil)
		act.SE = mockSE

		ok, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("pool resolve error propagates", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 1},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).
			Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nil, errors.New("node err"))
		act.SE = mockSE

		_, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, map[string]struct{}{}, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node err")
	})

	t.Run("soft delete error propagates", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return errors.New("yaml delete failed")
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 3},
			NodeID:      20,
			NodeGroupID: 1,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-b"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).
			Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
		expectPoolResolutionByNode(mockSE, 20, 8, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 8, DeletedAt: deletedAt()},
		})
		act.SE = mockSE

		inventorySet := map[string]struct{}{"lease-b|20": {}}
		_, err := act.reconcileDeletedPoolMapsOnNodeGroup(ctx, 1, inventorySet, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "yaml delete failed")
	})
}

func TestSoftDeleteMapForDeletedPool(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("nil mapping", func(t *testing.T) {
		yamlDeleted, err := act.softDeleteMapForDeletedPool(ctx, nil, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.False(t, yamlDeleted)
	})

	t.Run("missing lease name", func(t *testing.T) {
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1},
			NodeID:      10,
			NodeGroupID: 5,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroup", mock.Anything, int64(5)).Return(&datamodel.NodeGroup{LeaseName: ""}, nil)
		act.SE = mockSE

		_, err := act.softDeleteMapForDeletedPool(ctx, mapping, map[string]struct{}{}, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease name missing")
	})

	t.Run("yaml delete error", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return errors.New("farm delete failed")
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 2},
			NodeID:      20,
			NodeGroupID: 6,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-c"},
		}
		act.SE = database.NewMockStorage(t)
		inventorySet := map[string]struct{}{"lease-c|20": {}}

		_, err := act.softDeleteMapForDeletedPool(ctx, mapping, inventorySet, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "farm delete failed")
	})

	t.Run("map soft-delete error after yaml removed", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 3},
			NodeID:      30,
			NodeGroupID: 7,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-d"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(3)).Return(errors.New("map delete failed"))
		act.SE = mockSE

		inventorySet := map[string]struct{}{"lease-d|30": {}}
		yamlDeleted, err := act.softDeleteMapForDeletedPool(ctx, mapping, inventorySet, map[string]int64{})
		require.Error(t, err)
		assert.True(t, yamlDeleted)
		assert.Contains(t, err.Error(), "map delete failed")
	})

	t.Run("already soft-deleted map skips map delete", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 4, DeletedAt: deletedAt()},
			NodeID:      40,
			NodeGroupID: 8,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-e"},
		}
		act.SE = database.NewMockStorage(t)
		inventorySet := map[string]struct{}{"lease-e|40": {}}
		candidates := map[string]int64{}

		yamlDeleted, err := act.softDeleteMapForDeletedPool(ctx, mapping, inventorySet, candidates)
		require.NoError(t, err)
		assert.True(t, yamlDeleted)
		assert.Equal(t, int64(8), candidates["lease-e"])
	})
}

func TestCleanupMapsForDeletedPools_EdgeCases(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("list error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, int64(0), harvestDriftReconcileMapPageSize).
			Return(nil, errors.New("paginate failed"))
		act.SE = mockSE

		_, _, err := act.cleanupMapsForDeletedPools(ctx, map[string]struct{}{}, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "paginate failed")
	})

	t.Run("skips live pool and nil mapping", func(t *testing.T) {
		liveMap := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 1},
			NodeID:    10,
		}
		mockSE := database.NewMockStorage(t)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{nil, liveMap})
		expectPoolResolutionByNode(mockSE, 10, 7, readyPool(7, datamodel.LifeCycleStateREADY))
		act.SE = mockSE

		softDeleted, yamlDeleted, err := act.cleanupMapsForDeletedPools(ctx, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.Equal(t, 0, softDeleted)
		assert.Equal(t, 0, yamlDeleted)
	})

	t.Run("cleanup error is warn-and-continue", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
			return errors.New("delete failed")
		}
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 2},
			NodeID:      20,
			NodeGroupID: 6,
			NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-f"},
		}
		mockSE := database.NewMockStorage(t)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapRow})
		expectPoolResolutionByNode(mockSE, 20, 8, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 8, DeletedAt: deletedAt()},
		})
		act.SE = mockSE

		inventorySet := map[string]struct{}{"lease-f|20": {}}
		softDeleted, yamlDeleted, err := act.cleanupMapsForDeletedPools(ctx, inventorySet, map[string]int64{})
		require.NoError(t, err)
		assert.Equal(t, 0, softDeleted)
		assert.Equal(t, 0, yamlDeleted)
	})
}

func TestDeleteCandidateAndPersist(t *testing.T) {
	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10}
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("map delete error", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 10}
		mockSE := database.NewMockStorage(t)
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(1)).Return(errors.New("map delete failed"))
		act.SE = mockSE

		handled, orphan, err := act.deleteCandidateAndPersist(ctx, entry, mapRow, false)
		require.Error(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
	})

	t.Run("orphan yaml without map row", func(t *testing.T) {
		oldDelete := deleteHarvestPollerFromFarm
		deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
		t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

		act.SE = database.NewMockStorage(t)
		handled, orphan, err := act.deleteCandidateAndPersist(ctx, entry, nil, true)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.True(t, orphan)
	})
}

func TestGetInt64FromMap_AllTypes(t *testing.T) {
	val, ok := getInt64FromMap(map[string]interface{}{"node_id": float64(42)}, "node_id")
	assert.True(t, ok)
	assert.Equal(t, int64(42), val)

	val, ok = getInt64FromMap(map[string]interface{}{"node_id": int64(99)}, "node_id")
	assert.True(t, ok)
	assert.Equal(t, int64(99), val)

	val, ok = getInt64FromMap(map[string]interface{}{"node_id": 7}, "node_id")
	assert.True(t, ok)
	assert.Equal(t, int64(7), val)

	_, ok = getInt64FromMap(map[string]interface{}{"node_id": "not-a-number"}, "node_id")
	assert.False(t, ok)

	_, ok = getInt64FromMap(map[string]interface{}{"other": 1}, "node_id")
	assert.False(t, ok)
}

func TestFlowRetryState_DefaultFlow(t *testing.T) {
	mapping := &datamodel.NodeNodeGroupMap{
		RetryState: &datamodel.HarvestRetryState{},
	}
	assert.Nil(t, flowRetryState(mapping, "unknown"))
}

func TestResolvePoolForMapCached_EdgeCases(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("nil map row", func(t *testing.T) {
		pool, err := act.resolvePoolForMapCached(ctx, nil, newHarvestDriftPoolCache())
		require.NoError(t, err)
		assert.Nil(t, pool)
	})

	t.Run("resolve error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nil, errors.New("node err"))
		act.SE = mockSE

		_, err := act.resolvePoolForMapCached(ctx, &datamodel.NodeNodeGroupMap{NodeID: 10}, newHarvestDriftPoolCache())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node err")
	})
}

func TestResolvePoolByID_DBError(t *testing.T) {
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT (.+) FROM (.+)pools(.+)`).
		WillReturnError(errors.New("db connection lost"))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetPoolByID", mock.Anything, int64(55)).
		Return(nil, utilerrors.NewNotFoundErr("pool", nil))
	mockSE.On("DB").Return(gormDB)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	_, err := act.resolvePoolByID(context.Background(), 55)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection lost")
}

func TestListReadyPoolIDsInWindow_DBError(t *testing.T) {
	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT "id" FROM "pools"`).
		WillReturnError(errors.New("query failed"))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	_, err := act.listReadyPoolIDsInWindow(context.Background(), time.Now(), time.Now(), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query failed")
}

func TestListHarvestPollerFiles_Errors(t *testing.T) {
	t.Run("request creation uses invalid endpoint", func(t *testing.T) {
		oldHost, oldProto := harvestEndPoint, harvestRestProtocol
		harvestEndPoint = "127.0.0.1:1"
		harvestRestProtocol = "http"
		t.Cleanup(func() {
			harvestEndPoint = oldHost
			harvestRestProtocol = oldProto
		})

		_, _, err := listHarvestPollerFiles(context.Background())
		require.Error(t, err)
	})

	t.Run("invalid protocol in url", func(t *testing.T) {
		oldHost, oldProto := harvestEndPoint, harvestRestProtocol
		harvestEndPoint = "example.com"
		harvestRestProtocol = "://bad"
		t.Cleanup(func() {
			harvestEndPoint = oldHost
			harvestRestProtocol = oldProto
		})

		_, _, err := listHarvestPollerFiles(context.Background())
		require.Error(t, err)
	})
}

func TestHandleInventoryDeleteCandidate_AdditionalCases(t *testing.T) {
	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10, FileName: "harvest-10.yaml"}

	t.Run("pool resolve error", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 1}, NodeID: 10}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nil, errors.New("node err"))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		_, _, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node err")
	})

	t.Run("unresolved pool is skipped", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{BaseModel: datamodel.BaseModel{ID: 2}, NodeID: 10}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("GetNodeByID", mock.Anything, int64(10)).
			Return(nil, utilerrors.NewNotFoundErr("node", nil))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
	})

	t.Run("clear stale delete retry error", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel: datamodel.BaseModel{ID: 3},
			NodeID:    10,
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 1},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		expectPoolResolutionByNode(mockSE, 10, 7, readyPool(7, datamodel.LifeCycleStateREADY))
		expectDeleteRetryStateUpdateError(mockSE, mapRow, errors.New("update failed"))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		_, _, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
	})
}

func TestProcessDeleteCandidate_RetryStateUpdateError(t *testing.T) {
	oldDelete := deleteHarvestPollerFromFarm
	deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error {
		return errors.New("farm delete failed")
	}
	t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 1},
		NodeID:    10,
	}
	mockSE := database.NewMockStorage(t)
	expectDeleteRetryStateUpdateError(mockSE, mapRow, errors.New("state update failed"))
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	_, _, err := act.processDeleteCandidate(context.Background(), HarvestPollerFileEntry{
		LeaseName: "lease-a",
		NodeID:    10,
	}, mapRow, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state update failed")
}

func TestReconcileReadyPoolsAndRetries_AdditionalCases(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("ready pool list error", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnError(errors.New("pool query failed"))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(ctx, map[string]struct{}{}, time.Now(), time.Now(), true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pool query failed")
	})

	t.Run("get pool by id error", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(15)))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		mockSE.On("GetPoolByID", mock.Anything, int64(15)).Return(nil, errors.New("pool fetch failed"))
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(ctx, map[string]struct{}{}, time.Now(), time.Now(), true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "pool fetch failed")
	})

	t.Run("active map list error", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		mockSE.On("ListNodeNodeGroupMapAfterID", mock.Anything, false, int64(0), harvestDriftReconcileMapPageSize).
			Return(nil, errors.New("map list failed"))
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(ctx, map[string]struct{}{}, time.Now(), time.Now(), true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "map list failed")
	})
}

func TestTryCleanupEmptyLease_NodeGroupDeleteErrorAfterLeaseRemoved(t *testing.T) {
	stubLeaseCleanupDeletes(t)

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil).Twice()
	mockSE.On("GetNodeGroup", mock.Anything, int64(5)).Return(&datamodel.NodeGroup{LeaseName: "lease-a"}, nil)
	mockSE.On("DeleteNodeGroup", mock.Anything, int64(5)).Return(errors.New("node group delete failed"))

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inv, cand := emptyLeaseCleanupContext()
	cleaned, nodeGroupDeleted, err := act.tryCleanupEmptyLease(context.Background(), "lease-a", 5, inv, cand, nil)
	require.Error(t, err)
	assert.True(t, cleaned)
	assert.False(t, nodeGroupDeleted)
	assert.Contains(t, err.Error(), "node group delete failed")
}

func TestIsPoolDeleted_InvalidDeletedAt(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{DeletedAt: &gorm.DeletedAt{Valid: false}},
	}
	assert.False(t, isPoolDeleted(pool))
}

func TestMarkRetryFailure_UpdateError(t *testing.T) {
	mapping := &datamodel.NodeNodeGroupMap{RetryState: &datamodel.HarvestRetryState{}}
	mockSE := database.NewMockStorage(t)
	mockSE.On("UpdateNodeNodeGroupMapRetryState", mock.Anything, int64(0), mock.Anything).
		Return(errors.New("update failed"))
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	_, _, err := act.markRetryFailure(context.Background(), mapping, "upload failed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestMarkRetryTerminal_NilState(t *testing.T) {
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel:  datamodel.BaseModel{ID: 1},
		RetryState: &datamodel.HarvestRetryState{},
	}
	mockSE := database.NewMockStorage(t)
	expectCreateRetryStateUpdate(mockSE, mapping)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	require.NoError(t, act.markRetryTerminal(context.Background(), mapping, "stopped"))
	require.NotNil(t, mapping.RetryState.Create)
	assert.False(t, mapping.RetryState.Create.ShouldRetry)
}

func TestReconcileReadyPoolsAndRetries_MoreBranches(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("skips mapping with unresolved pool", func(t *testing.T) {
		mapping := readyPoolMapping(304, "pool-skip", "lease-skip", false)
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
		mockSE.On("GetNodeByID", mock.Anything, int64(304)).
			Return(nil, utilerrors.NewNotFoundErr("node", nil))
		act.SE = mockSE

		created, queued, exhausted, err := act.reconcileReadyPoolsAndRetries(
			ctx, map[string]struct{}{}, time.Now(), time.Now(), true,
		)
		require.NoError(t, err)
		assert.Equal(t, 0, created)
		assert.Equal(t, 0, queued)
		assert.Equal(t, 0, exhausted)
	})

	t.Run("pool resolve error in candidate scan", func(t *testing.T) {
		mapping := readyPoolMapping(301, "pool-x", "lease-x", false)
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(15)))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		mockSE.On("GetPoolByID", mock.Anything, int64(15)).
			Return(readyPool(15, datamodel.LifeCycleStateREADY), nil)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
		mockSE.On("GetNodeByID", mock.Anything, int64(301)).Return(nil, errors.New("node lookup failed"))
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(
			ctx, map[string]struct{}{}, time.Now().Add(-time.Hour), time.Now(), false,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node lookup failed")
	})

	t.Run("get node group error before lease validation", func(t *testing.T) {
		mapping := readyPoolMapping(302, "pool-y", "lease-y", true)
		mapping.NodeGroup = nil

		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
		expectPoolResolutionByNode(mockSE, mapping.NodeID, 9, readyPool(9, datamodel.LifeCycleStateREADY))
		mockSE.On("GetNodeGroup", mock.Anything, mapping.NodeGroupID).
			Return(nil, errors.New("node group missing"))
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(
			ctx, map[string]struct{}{}, time.Now().Add(-time.Hour), time.Now(), false,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "node group missing")
	})

	t.Run("mark retry terminal error on deleted pool", func(t *testing.T) {
		mapping := readyPoolMapping(303, "pool-z", "lease-z", true)
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

		mockSE := database.NewMockStorage(t)
		mockSE.On("DB").Return(gormDB)
		expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
		expectPoolResolutionByNode(mockSE, mapping.NodeID, 12, &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 12, DeletedAt: deletedAt()},
			State:     datamodel.LifeCycleStateREADY,
		})
		expectCreateRetryStateUpdateError(mockSE, mapping, errors.New("terminal persist failed"))
		act.SE = mockSE

		_, _, _, err := act.reconcileReadyPoolsAndRetries(
			ctx, map[string]struct{}{}, time.Now().Add(-time.Hour), time.Now(), false,
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terminal persist failed")
	})
}

func TestTryCleanupEmptyLease_StillActiveAfterDeletedPoolCleanup(t *testing.T) {
	oldDelete := deleteHarvestPollerFromFarm
	deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
	t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: 1},
		NodeID:      10,
		NodeGroupID: 7,
		NodeGroup:   &datamodel.NodeGroup{LeaseName: "lease-c"},
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(7)).Return(int64(1), nil).Once()
	mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(7)).
		Return([]*datamodel.NodeNodeGroupMap{mapRow}, nil)
	expectPoolResolutionByNode(mockSE, 10, 9, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 9, DeletedAt: deletedAt()},
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(1)).Return(nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(7)).Return(int64(1), nil).Once()

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	inv, cand := emptyLeaseCleanupContext()
	cleaned, _, err := act.tryCleanupEmptyLease(context.Background(), "lease-c", 7, inv, cand, nil)
	require.NoError(t, err)
	assert.False(t, cleaned)
}

func TestNodeIDFromFileName_InvalidID(t *testing.T) {
	_, ok := nodeIDFromFileName("harvest-notanumber.yaml")
	assert.False(t, ok)
}

func TestReconcileHarvestPollerDrift_DeleteCandidateWarnAndContinue(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{
		{"leaseName": "lease-a", "node_id": 10, "file_name": "harvest-10.yaml"},
		{"leaseName": "lease-b", "node_id": 20, "file_name": "harvest-20.yaml"},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(inventoryBody)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).
		Return(nil, errors.New("transient map lookup failure"))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(20)).
		Return(nil, utilerrors.NewNotFoundErr("map", nil))
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, nil, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)

	oldDelete := deleteHarvestPollerFromFarm
	deleteHarvestPollerFromFarm = func(ctx context.Context, leaseName string, nodeID int64) error { return nil }
	t.Cleanup(func() { deleteHarvestPollerFromFarm = oldDelete })

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	stubLeaseCleanupDeletes(t)
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 1, result.DeletedYAMLs)
	assert.Equal(t, 1, result.OrphanYAMLHandled)
}

func TestReconcileHarvestPollerDrift_DeletedPoolMapWithoutYAML(t *testing.T) {
	inventoryBody, err := json.Marshal([]map[string]interface{}{})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write(inventoryBody)
		}
	}))
	overrideHarvestEndpoint(t, srv)

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel:   datamodel.BaseModel{ID: 40},
		NodeID:      77,
		NodeGroupID: 15,
		NodeGroup:   &datamodel.NodeGroup{BaseModel: datamodel.BaseModel{ID: 15}, LeaseName: "lease-orphan"},
	}

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	expectPoolResolutionByNode(mockSE, 77, 50, &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 50, DeletedAt: deletedAt()},
		State:     datamodel.LifeCycleStateREADY,
	})
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(40)).Return(nil)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMapPasses(mockSE, []*datamodel.NodeNodeGroupMap{mapRow}, nil)
	expectLeaseCleanupDiscovery(mockSE)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(15)).Return(int64(0), nil)
	expectEmptyNodeGroupSoftDelete(mockSE, 15, "lease-orphan")
	mockSE.On("UpsertAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey, mock.Anything).Return(nil)
	stubLeaseCleanupDeletes(t)

	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ReconcileHarvestPollerDrift)

	res, err := env.ExecuteActivity(act.ReconcileHarvestPollerDrift)
	require.NoError(t, err)

	var result HarvestPollerDriftReconcileResult
	require.NoError(t, res.Get(&result))
	assert.Equal(t, 0, result.DeletedYAMLs)
	assert.Equal(t, 1, result.SoftDeletedMaps)
	assert.Equal(t, 1, result.DeletedLeases)
	assert.Equal(t, 1, result.SoftDeletedNodeGroups)
}

package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		entries, err := parseHarvestInventory([]byte("  "))
		require.NoError(t, err)
		assert.Nil(t, entries)
	})

	t.Run("array of objects", func(t *testing.T) {
		body := []byte(`[{"leaseName":"lease-a","node_id":42,"file_name":"harvest-42.yaml"}]`)
		entries, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "lease-a", entries[0].LeaseName)
		assert.Equal(t, int64(42), entries[0].NodeID)
	})

	t.Run("wrapped items and files", func(t *testing.T) {
		body := []byte(`{"items":[{"lease_name":"l1","nodeID":1}],"files":[{"file":"harvest-9.yaml"}]}`)
		entries, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		assert.Equal(t, int64(1), entries[0].NodeID)
		assert.Equal(t, int64(9), entries[1].NodeID)
	})

	t.Run("string filenames", func(t *testing.T) {
		body := []byte(`["harvest-5.yaml","invalid.txt"]`)
		entries, err := parseHarvestInventory(body)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, int64(5), entries[0].NodeID)
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := parseHarvestInventory([]byte(`{"broken":`))
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
	entries := flattenInventoryObjectList([]map[string]interface{}{
		{"leaseName": "l1", "node_id": float64(10)},
		{"file_name": "harvest-20.yaml"},
		{"node_id": 0},
	})
	require.Len(t, entries, 2)
	assert.Equal(t, int64(10), entries[0].NodeID)
	assert.Equal(t, int64(20), entries[1].NodeID)
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
}

func TestDeleteHarvestPollerYAML(t *testing.T) {
	t.Run("empty lease is no-op", func(t *testing.T) {
		require.NoError(t, deleteHarvestPollerYAML(context.Background(), "  ", 1))
	})

	t.Run("success and not found", func(t *testing.T) {
		for _, code := range []int{http.StatusOK, http.StatusNotFound} {
			code := code
			t.Run(http.StatusText(code), func(t *testing.T) {
				oldDelete := deletePollerRestResponse
				deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
					assert.Contains(t, url, "/config/lease-a/harvest-7")
					return &http.Response{
						StatusCode: code,
						Body:       io.NopCloser(strings.NewReader("")),
					}, nil
				}
				t.Cleanup(func() { deletePollerRestResponse = oldDelete })
				require.NoError(t, deleteHarvestPollerYAML(context.Background(), "lease-a", 7))
			})
		}
	})

	t.Run("non-success status", func(t *testing.T) {
		oldDelete := deletePollerRestResponse
		deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}
		t.Cleanup(func() { deletePollerRestResponse = oldDelete })
		err := deleteHarvestPollerYAML(context.Background(), "lease-a", 7)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "delete harvest yaml failed")
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

		entries, err := listHarvestPollerFiles(context.Background())
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, int64(11), entries[0].NodeID)
	})

	t.Run("non-success status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
		}))
		overrideHarvestEndpoint(t, srv)

		_, err := listHarvestPollerFiles(context.Background())
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
			WithArgs(datamodel.LifeCycleStateREADY).
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

func TestResolvePoolForMapCached(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}
	cache := newHarvestDriftPoolCache()
	cachedPool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 42},
		Name:      "pool-cached",
		State:     datamodel.LifeCycleStateREADY,
	}
	cache.byName["pool-cached"] = cachedPool

	t.Run("cache hit avoids storage lookup", func(t *testing.T) {
		pool, err := act.resolvePoolForMapCached(ctx, &datamodel.NodeNodeGroupMap{
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-cached"},
		}, cache)
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(42), pool.ID)
	})

	t.Run("cache miss populates cache", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 55}}}, nil).Once()
		mockSE.On("GetPoolByID", mock.Anything, int64(55)).
			Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 55}, Name: "pool-miss", State: datamodel.LifeCycleStateREADY}, nil).Once()
		act.SE = mockSE

		pool, err := act.resolvePoolForMapCached(ctx, &datamodel.NodeNodeGroupMap{
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-miss"},
		}, cache)
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(55), pool.ID)
		assert.Same(t, pool, cache.byName["pool-miss"])
		assert.Same(t, pool, cache.byID[55])
	})
}

func TestResolvePoolForMap(t *testing.T) {
	ctx := context.Background()
	act := &HarvestPollerDriftReconcileActivity{}

	t.Run("nil harvest config", func(t *testing.T) {
		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})

	t.Run("pool resolved by name", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 99}}}, nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(99)).
			Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 99}, State: datamodel.LifeCycleStateREADY}, nil)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-a"},
		})
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
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-b"},
		})
		require.NoError(t, err)
		require.NotNil(t, pool)
		assert.Equal(t, int64(55), pool.ID)
	})

	t.Run("unscoped fallback not found returns nil pool", func(t *testing.T) {
		gormDB, smock := newSQLMockGormDB(t)
		smock.ExpectQuery(`SELECT (.+) FROM (.+)pools(.+)`).
			WillReturnError(gorm.ErrRecordNotFound)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(nil, utilerrors.NewNotFoundErr("pool", nil))
		mockSE.On("DB").Return(gormDB)
		act.SE = mockSE

		pool, err := act.resolvePoolForMap(ctx, &datamodel.NodeNodeGroupMap{
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "missing-pool"},
		})
		require.NoError(t, err)
		assert.Nil(t, pool)
	})
}

func TestHandleInventoryDeleteCandidate(t *testing.T) {
	ctx := context.Background()
	entry := HarvestPollerFileEntry{LeaseName: "lease-a", NodeID: 10, FileName: "harvest-10.yaml"}

	t.Run("orphan yaml", func(t *testing.T) {
		oldDelete := deletePollerRestResponse
		deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		t.Cleanup(func() { deletePollerRestResponse = oldDelete })

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
		oldDelete := deletePollerRestResponse
		deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		t.Cleanup(func() { deletePollerRestResponse = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:   datamodel.BaseModel{ID: 1, DeletedAt: deletedAt()},
			NodeID:      10,
			NodeGroupID: 42,
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapRow, nil)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, nodeGroupID, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
		assert.Equal(t, int64(42), nodeGroupID)
	})

	t.Run("active map with ready pool is skipped", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{ID: 2},
			NodeID:        10,
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-ready"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 3}}}, nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(3)).
			Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 3}, State: datamodel.LifeCycleStateREADY}, nil)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.False(t, handled)
		assert.False(t, orphan)
	})

	t.Run("stale delete retry on live pool is cleared", func(t *testing.T) {
		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{ID: 6},
			NodeID:        10,
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-live"},
			RetryState: &datamodel.HarvestRetryState{
				Delete: &datamodel.HarvestFlowRetryState{ShouldRetry: true, Retries: 1},
			},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}}}, nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(7)).
			Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, State: datamodel.LifeCycleStateREADY}, nil)
		mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapRow, nil)
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
		oldDelete := deletePollerRestResponse
		deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		t.Cleanup(func() { deletePollerRestResponse = oldDelete })

		mapRow := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{ID: 4},
			NodeID:        10,
			HarvestConfig: &datamodel.HarvestConfig{POOL_NAME: "pool-deleted"},
		}
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(10)).Return(mapRow, nil)
		mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
			Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 5}}}, nil)
		mockSE.On("GetPoolByID", mock.Anything, int64(5)).
			Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 5, DeletedAt: deletedAt()}, State: datamodel.LifeCycleStateREADY}, nil)
		mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(4)).Return(nil)
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		handled, orphan, _, err := act.handleInventoryDeleteCandidate(ctx, entry)
		require.NoError(t, err)
		assert.True(t, handled)
		assert.False(t, orphan)
	})
}

func TestMarkRetryHelpers(t *testing.T) {
	ctx := context.Background()
	mapping := &datamodel.NodeNodeGroupMap{
		BaseModel:  datamodel.BaseModel{ID: 1},
		RetryState: &datamodel.HarvestRetryState{},
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapping, nil)
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
	expectPaginatedActiveMaps(mockSE, nil)
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
	expectPaginatedActiveMaps(mockSE, nil)
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
	expectPaginatedActiveMaps(mockSE, nil)
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
	expectPaginatedActiveMaps(mockSE, nil)
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

	entries, inventorySet, err := listHarvestPollerInventory(context.Background())
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "lease-a", entries[0].LeaseName)
	assert.Equal(t, int64(2), entries[0].NodeID)
	_, ok := inventorySet["lease-a|2"]
	assert.True(t, ok)
	_, ok = inventorySet["|1"]
	assert.False(t, ok)
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
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: *readyPool(9, datamodel.LifeCycleStateREADY)}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(9)).
		Return(readyPool(9, datamodel.LifeCycleStateREADY), nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapping, nil)
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
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapRow, nil)
	act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

	require.NoError(t, act.clearStaleDeleteRetry(context.Background(), mapRow))
	assert.False(t, mapRow.RetryState.Delete.ShouldRetry)
	assert.Contains(t, mapRow.RetryState.Delete.LastRunMsg, "pool not deleted")
}

func TestProcessDeleteCandidate_DeleteFailureMarksRetry(t *testing.T) {
	oldDelete := deletePollerRestResponse
	deletePollerRestResponse = func(ctx context.Context, url string) (*http.Response, error) {
		return nil, fmt.Errorf("network down")
	}
	t.Cleanup(func() { deletePollerRestResponse = oldDelete })

	mapRow := &datamodel.NodeNodeGroupMap{
		BaseModel: datamodel.BaseModel{ID: 9},
		NodeID:    10,
		RetryState: &datamodel.HarvestRetryState{
			Delete: &datamodel.HarvestFlowRetryState{},
		},
	}
	mockSE := database.NewMockStorage(t)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapRow, nil)
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
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: *readyPool(7, datamodel.LifeCycleStateREADY)}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(7)).
		Return(readyPool(7, datamodel.LifeCycleStateREADY), nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapping, nil)
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

func TestReconcileReadyPoolsAndRetries_DeletedPoolMarksCreateRetryTerminal(t *testing.T) {
	ctx := context.Background()
	mapping := readyPoolMapping(104, "pool-deleted-retry", "lease-del-retry", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 12}}}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(12)).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 12, DeletedAt: deletedAt()}, State: datamodel.LifeCycleStateREADY}, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapping, nil)

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

func TestReconcileReadyPoolsAndRetries_NonReadyPoolMarksRetryTerminal(t *testing.T) {
	ctx := context.Background()
	mapping := readyPoolMapping(102, "pool-2", "lease-2", true)

	gormDB, smock := newSQLMockGormDB(t)
	smock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))

	mockSE := database.NewMockStorage(t)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 8}, State: datamodel.LifeCycleStateCreating}}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(8)).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 8}, State: datamodel.LifeCycleStateCreating}, nil)
	mockSE.On("UpdateNodeNodeGroupMap", mock.Anything, mock.Anything).Return(mapping, nil)

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
	assert.Contains(t, mapping.RetryState.Create.LastRunMsg, "non-READY")
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

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetAppConfig", mock.Anything, harvestDriftCheckpointAppConfigKey).
		Return(nil, utilerrors.NewNotFoundErr("app config", nil))
	mockSE.On("GetNodeNodeGroupMapByNodeID", mock.Anything, int64(103)).Return(mapping, nil)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, []*datamodel.NodeNodeGroupMap{mapping})
	mockSE.On("GetPoolByID", mock.Anything, int64(8)).
		Return(readyPool(8, datamodel.LifeCycleStateREADY), nil)
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: *readyPool(8, datamodel.LifeCycleStateREADY)}, nil)
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
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(9)).Return(int64(0), nil)

		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}
		deleted, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{})
		require.NoError(t, err)
		assert.Equal(t, 1, deleted)
	})

	t.Run("list error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return(nil, errors.New("db list failed"))
		act := &HarvestPollerDriftReconcileActivity{SE: mockSE}

		_, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db list failed")
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
		deleted, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, map[string]struct{}{}, map[string]int64{"lease-a": 5})
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
		deleted, err := act.cleanupEmptyLeasesAfterDeletePass(ctx, inventorySet, candidates)
		require.NoError(t, err)
		assert.Equal(t, 0, deleted)
	})

	t.Run("deletes k8s lease when inventory empty and node group has no active pollers", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
		act.SE = mockSE

		cleaned, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
		require.NoError(t, err)
		assert.True(t, cleaned)
	})

	t.Run("skips when node group still has active pollers", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(2), nil)
		act.SE = mockSE

		cleaned, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("empty lease name is no-op", func(t *testing.T) {
		cleaned, err := act.tryCleanupEmptyLease(ctx, "  ", 5)
		require.NoError(t, err)
		assert.False(t, cleaned)
	})

	t.Run("resolves node group when id is zero", func(t *testing.T) {
		stubLeaseCleanupDeletes(t)

		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
			Return([]datamodel.NodeGroupPollerCount{{NodeGroupID: 11, LeaseName: "lease-r", Count: 0}}, nil)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(11)).Return(int64(0), nil)
		act.SE = mockSE

		cleaned, err := act.tryCleanupEmptyLease(ctx, "lease-r", 0)
		require.NoError(t, err)
		assert.True(t, cleaned)
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

		cleaned, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
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

		_, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
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

		cleaned, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
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

		_, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forbidden")
	})

	t.Run("node count lookup error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).
			Return(int64(0), errors.New("count failed"))
		act.SE = mockSE

		_, err := act.tryCleanupEmptyLease(ctx, "lease-a", 5)
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
	mockSE.On("GetPoolByName", mock.Anything, mock.Anything).
		Return(&datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 30}}}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(30)).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 30, DeletedAt: deletedAt()}, State: datamodel.LifeCycleStateREADY}, nil)
	mockSE.On("DeleteNodeNodeGroupMap", mock.Anything, int64(20)).Return(nil)
	mockSE.On("DB").Return(gormDB)
	expectPaginatedActiveMaps(mockSE, nil)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).
		Return([]datamodel.NodeGroupPollerCount{}, nil)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(8)).Return(int64(0), nil)
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
	expectPaginatedActiveMaps(mockSE, nil)
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

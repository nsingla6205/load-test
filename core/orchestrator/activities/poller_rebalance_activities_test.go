package activities

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDeleteHarvestLeaseConfigFolder_EmptyLeaseNoOp(t *testing.T) {
	err := deleteHarvestLeaseConfigFolder(context.Background(), "   ")
	require.NoError(t, err)
}

func TestDeleteHarvestLeaseConfigFolder_SuccessStatuses(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusNoContent, http.StatusNotFound} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				b, _ := io.ReadAll(r.Body)
				assert.Contains(t, string(b), `"leaseName":"my-lease"`)
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(srv.Close)

			oldHost, oldProto := harvestEndPoint, harvestRestProtocol
			harvestEndPoint = strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")
			harvestRestProtocol = "http"
			t.Cleanup(func() {
				harvestEndPoint = oldHost
				harvestRestProtocol = oldProto
			})

			err := deleteHarvestLeaseConfigFolder(context.Background(), "my-lease")
			require.NoError(t, err)
		})
	}
}

func TestDeleteHarvestLeaseConfigFolder_ErrorStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"bad_request", http.StatusBadRequest},
		{"conflict", http.StatusConflict},
		{"server_error", http.StatusInternalServerError},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("details"))
			}))
			t.Cleanup(srv.Close)

			oldHost, oldProto := harvestEndPoint, harvestRestProtocol
			harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
			harvestRestProtocol = "http"
			t.Cleanup(func() {
				harvestEndPoint = oldHost
				harvestRestProtocol = oldProto
			})

			err := deleteHarvestLeaseConfigFolder(context.Background(), "lease-x")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "lease-x")
		})
	}
}

func TestDeleteRebalanceTargetHarvestPoller_EmptyLease(t *testing.T) {
	err := deleteRebalanceTargetHarvestPoller(context.Background(), "", 99)
	require.NoError(t, err)
}

func TestDeleteRebalanceTargetHarvestPoller_OKAndNotFound(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusNotFound} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Contains(t, r.URL.Path, "/config/lease-a/")
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)

			oldHost, oldProto := harvestEndPoint, harvestRestProtocol
			harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
			harvestRestProtocol = "http"
			t.Cleanup(func() {
				harvestEndPoint = oldHost
				harvestRestProtocol = oldProto
			})

			err := deleteRebalanceTargetHarvestPoller(context.Background(), "lease-a", 7)
			require.NoError(t, err)
		})
	}
}

func TestDeleteRebalanceTargetHarvestPoller_NonSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	oldHost, oldProto := harvestEndPoint, harvestRestProtocol
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	harvestRestProtocol = "http"
	t.Cleanup(func() {
		harvestEndPoint = oldHost
		harvestRestProtocol = oldProto
	})

	err := deleteRebalanceTargetHarvestPoller(context.Background(), "lease-b", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "418")
}

type failThenPassDeleteRoundTripper struct {
	fallback http.RoundTripper
	calls    int
	firstErr error
}

func (rt *failThenPassDeleteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.calls++
	if rt.calls == 1 {
		firstErr := rt.firstErr
		if firstErr == nil {
			firstErr = syscall.ECONNRESET
		}
		return nil, &neturl.Error{
			Op:  "Delete",
			URL: req.URL.String(),
			Err: &net.OpError{
				Op:  "read",
				Net: "tcp",
				Err: firstErr,
			},
		}
	}
	return rt.fallback.RoundTrip(req)
}

func TestDeleteRebalanceTargetHarvestPoller_RetryableTransportErrorThenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	oldHost, oldProto := harvestEndPoint, harvestRestProtocol
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	harvestRestProtocol = "http"
	t.Cleanup(func() {
		harvestEndPoint = oldHost
		harvestRestProtocol = oldProto
	})

	originalAttempts := rebalancePollerDeleteMaxAttempts
	originalInterval := rebalancePollerDeleteRetryIntervalM
	rebalancePollerDeleteMaxAttempts = 2
	rebalancePollerDeleteRetryIntervalM = 1
	t.Cleanup(func() {
		rebalancePollerDeleteMaxAttempts = originalAttempts
		rebalancePollerDeleteRetryIntervalM = originalInterval
	})

	rt := &failThenPassDeleteRoundTripper{fallback: http.DefaultTransport}
	originalTransport := rebalanceHarvestDeleteHTTPTransport
	rebalanceHarvestDeleteHTTPTransport = rt
	t.Cleanup(func() { rebalanceHarvestDeleteHTTPTransport = originalTransport })

	err := deleteRebalanceTargetHarvestPoller(context.Background(), "lease-retry", 42)
	require.NoError(t, err)
	assert.Equal(t, 2, rt.calls)
}

func TestDeleteRebalanceTargetHarvestPoller_RetryableConnectionRefusedThenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	oldHost, oldProto := harvestEndPoint, harvestRestProtocol
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	harvestRestProtocol = "http"
	t.Cleanup(func() {
		harvestEndPoint = oldHost
		harvestRestProtocol = oldProto
	})

	originalAttempts := rebalancePollerDeleteMaxAttempts
	originalInterval := rebalancePollerDeleteRetryIntervalM
	rebalancePollerDeleteMaxAttempts = 2
	rebalancePollerDeleteRetryIntervalM = 1
	t.Cleanup(func() {
		rebalancePollerDeleteMaxAttempts = originalAttempts
		rebalancePollerDeleteRetryIntervalM = originalInterval
	})

	rt := &failThenPassDeleteRoundTripper{
		fallback: http.DefaultTransport,
		firstErr: syscall.ECONNREFUSED,
	}
	originalTransport := rebalanceHarvestDeleteHTTPTransport
	rebalanceHarvestDeleteHTTPTransport = rt
	t.Cleanup(func() { rebalanceHarvestDeleteHTTPTransport = originalTransport })

	err := deleteRebalanceTargetHarvestPoller(context.Background(), "lease-refused", 1035)
	require.NoError(t, err)
	assert.Equal(t, 2, rt.calls)
}

func TestCloneHarvestConfig(t *testing.T) {
	_, err := cloneHarvestConfig(nil)
	require.Error(t, err)

	hc := &datamodel.HarvestConfig{PORT: "12345", LEASE_NAME: "L"}
	out, err := cloneHarvestConfig(hc)
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "12345", out.PORT)
	assert.Equal(t, "L", out.LEASE_NAME)
}

func TestPollerRebalanceActivities_GetNodeGroupsWithPollerCountsActivity(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	want := []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, LeaseName: "a", Count: 2}}
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).Return(want, nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.GetNodeGroupsWithPollerCountsActivity)

	res, err := env.ExecuteActivity(act.GetNodeGroupsWithPollerCountsActivity)
	require.NoError(t, err)

	var got HarvestNodeGroupsSnapshotResult
	require.NoError(t, res.Get(&got))
	require.Len(t, got.Groups, 1)
	assert.Equal(t, int64(1), got.Groups[0].NodeGroupID)
	mockSE.AssertExpectations(t)
}

func TestPollerRebalanceActivities_GetNodeGroupsWithPollerCountsActivity_Error(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).Return(nil, errors.New("list failed"))

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.GetNodeGroupsWithPollerCountsActivity)

	_, err := env.ExecuteActivity(act.GetNodeGroupsWithPollerCountsActivity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list failed")
	mockSE.AssertExpectations(t)
}

func TestListEmptyHarvestLeasesForCleanupActivity_ReturnsOnlyEmpty(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	rows := []datamodel.NodeGroupPollerCount{
		{NodeGroupID: 1, LeaseName: "harvest-l1", Count: 0},
		{NodeGroupID: 2, LeaseName: "harvest-l2", Count: 5},
		{NodeGroupID: 3, LeaseName: "", Count: 0},
		{NodeGroupID: 4, LeaseName: "harvest-l4", Count: 0},
	}
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).Return(rows, nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ListEmptyHarvestLeasesForCleanupActivity)

	res, err := env.ExecuteActivity(act.ListEmptyHarvestLeasesForCleanupActivity)
	require.NoError(t, err)

	var got []EmptyHarvestLeaseCandidate
	require.NoError(t, res.Get(&got))
	require.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].NodeGroupID)
	assert.Equal(t, "harvest-l1", got[0].LeaseName)
	assert.Equal(t, int64(4), got[1].NodeGroupID)
}

func TestListEmptyHarvestLeasesForCleanupActivity_DBError(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("ListNodeGroupsWithPollerCounts", mock.Anything).Return(nil, errors.New("db error"))

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.ListEmptyHarvestLeasesForCleanupActivity)

	_, err := env.ExecuteActivity(act.ListEmptyHarvestLeasesForCleanupActivity)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestCleanupEmptyLeaseActivity_NilParams(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, (*CleanupEmptyLeaseParams)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cleanup empty lease params required")
}

func TestCleanupEmptyLeaseActivity_EmptyLeaseName(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 1, LeaseName: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node group id and lease name required")
}

func TestCleanupEmptyLeaseActivity_SkipWhenPollersExist(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(3), nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.NoError(t, err)
	mockSE.AssertExpectations(t)
}

func TestCleanupEmptyLeaseActivity_GetNodeGroupMapNodeCountError(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), errors.New("db err"))

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db err")
}

func TestCleanupEmptyLeaseActivity_FirstTransactionError(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(errors.New("tx error")).Once()

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "precheck empty lease cleanup")
}

func TestCleanupEmptyLeaseActivity_FirstTxSucceedsButNotProceed(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
	// WithTransaction succeeds but shouldProceedCleanup stays false (simulates cnt>0 inside tx)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(nil).Once()

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	// WithTransaction returns nil but shouldProceedCleanup = false → return nil early
	require.NoError(t, err)
}

func TestCleanupEmptyLeaseActivity_HarvestDeleteError(t *testing.T) {
	originalBucketName := pollerRebalanceBucketName
	pollerRebalanceBucketName = ""
	defer func() { pollerRebalanceBucketName = originalBucketName }()

	// Setup: harvest REST endpoint returns error
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("disk full"))
	}))
	defer srv.Close()

	oldProto, oldEndpoint := harvestRestProtocol, harvestEndPoint
	harvestRestProtocol = "http"
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	defer func() { harvestRestProtocol = oldProto; harvestEndPoint = oldEndpoint }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
	// First WithTransaction: simulate shouldProceedCleanup = true by running callback
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(1).(func(dbutils.Transaction) error)
		// Create a sqlmock-backed gorm.DB for the transaction
		db, smock, _ := sqlmock.New()
		defer func() { _ = db.Close() }()
		gormDB, _ := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		mockTx := dbutils.NewMockTransaction(t)
		mockTx.On("GORM").Return(gormDB)

		// Expect the SELECT FOR UPDATE query
		smock.ExpectQuery(`SELECT`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
		)
		// Expect the COUNT query → 0 pollers
		smock.ExpectQuery(`SELECT count`).WillReturnRows(
			sqlmock.NewRows([]string{"count"}).AddRow(0),
		)
		_ = fn(mockTx)
	}).Return(nil).Once()

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete harvest lease folder")
}

func TestCleanupEmptyLeaseActivity_BucketModeSkipsHarvestDelete(t *testing.T) {
	originalBucketName := pollerRebalanceBucketName
	pollerRebalanceBucketName = "test-rebalance-bucket"
	defer func() { pollerRebalanceBucketName = originalBucketName }()
	oldDeleteBucketPrefix := deleteRebalanceLeasePrefixInBucket
	deleteRebalanceLeasePrefixInBucket = func(ctx context.Context, bucketName, leaseName string) error { return nil }
	defer func() { deleteRebalanceLeasePrefixInBucket = oldDeleteBucketPrefix }()

	harvestDeleteCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		harvestDeleteCalled = true
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("should not be called in bucket mode"))
	}))
	defer srv.Close()

	oldProto, oldEndpoint := harvestRestProtocol, harvestEndPoint
	harvestRestProtocol = "http"
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	defer func() { harvestRestProtocol = oldProto; harvestEndPoint = oldEndpoint }()

	oldDeleteK8sLease := deleteKubernetesLeaseForEmptyHarvestPollers
	deleteKubernetesLeaseForEmptyHarvestPollers = func(ctx context.Context, ns, name string) error { return nil }
	defer func() { deleteKubernetesLeaseForEmptyHarvestPollers = oldDeleteK8sLease }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)

	txCallCount := 0
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		txCallCount++
		fn := args.Get(1).(func(dbutils.Transaction) error)
		db, smock, _ := sqlmock.New()
		defer func() { _ = db.Close() }()
		gormDB, _ := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		mockTx := dbutils.NewMockTransaction(t)
		mockTx.On("GORM").Return(gormDB)

		if txCallCount == 1 {
			smock.ExpectQuery(`SELECT`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
			)
			smock.ExpectQuery(`SELECT count`).WillReturnRows(
				sqlmock.NewRows([]string{"count"}).AddRow(0),
			)
		} else {
			smock.ExpectQuery(`SELECT`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
			)
			smock.ExpectQuery(`SELECT count`).WillReturnRows(
				sqlmock.NewRows([]string{"count"}).AddRow(0),
			)
			smock.ExpectExec(`UPDATE`).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		_ = fn(mockTx)
	}).Return(nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.NoError(t, err)
	assert.Equal(t, 2, txCallCount)
	assert.False(t, harvestDeleteCalled, "harvest lease folder delete should be skipped in bucket mode")
}

func TestCleanupEmptyLeaseActivity_BucketModeBucketCleanupError(t *testing.T) {
	originalBucketName := pollerRebalanceBucketName
	pollerRebalanceBucketName = "test-rebalance-bucket"
	defer func() { pollerRebalanceBucketName = originalBucketName }()

	oldDeleteBucketPrefix := deleteRebalanceLeasePrefixInBucket
	deleteRebalanceLeasePrefixInBucket = func(ctx context.Context, bucketName, leaseName string) error {
		return errors.New("gcs cleanup failed")
	}
	defer func() { deleteRebalanceLeasePrefixInBucket = oldDeleteBucketPrefix }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(1).(func(dbutils.Transaction) error)
		db, smock, _ := sqlmock.New()
		defer func() { _ = db.Close() }()
		gormDB, _ := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		mockTx := dbutils.NewMockTransaction(t)
		mockTx.On("GORM").Return(gormDB)
		smock.ExpectQuery(`SELECT`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
		)
		smock.ExpectQuery(`SELECT count`).WillReturnRows(
			sqlmock.NewRows([]string{"count"}).AddRow(0),
		)
		_ = fn(mockTx)
	}).Return(nil).Once()

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete rebalance bucket objects")
}

func TestCleanupEmptyLeaseActivity_FullHappyPath(t *testing.T) {
	// Harvest DELETE returns OK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	oldProto, oldEndpoint := harvestRestProtocol, harvestEndPoint
	harvestRestProtocol = "http"
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	defer func() { harvestRestProtocol = oldProto; harvestEndPoint = oldEndpoint }()

	// Override deleteKubernetesLeaseForEmptyHarvestPollers
	oldDeleteK8sLease := deleteKubernetesLeaseForEmptyHarvestPollers
	deleteKubernetesLeaseForEmptyHarvestPollers = func(ctx context.Context, ns, name string) error { return nil }
	defer func() { deleteKubernetesLeaseForEmptyHarvestPollers = oldDeleteK8sLease }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)

	txCallCount := 0
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		txCallCount++
		fn := args.Get(1).(func(dbutils.Transaction) error)
		db, smock, _ := sqlmock.New()
		defer func() { _ = db.Close() }()
		gormDB, _ := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		mockTx := dbutils.NewMockTransaction(t)
		mockTx.On("GORM").Return(gormDB)

		if txCallCount == 1 {
			// First tx: SELECT FOR UPDATE + COUNT → proceed
			smock.ExpectQuery(`SELECT`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
			)
			smock.ExpectQuery(`SELECT count`).WillReturnRows(
				sqlmock.NewRows([]string{"count"}).AddRow(0),
			)
		} else {
			// Second tx: SELECT FOR UPDATE + COUNT + DELETE
			smock.ExpectQuery(`SELECT`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
			)
			smock.ExpectQuery(`SELECT count`).WillReturnRows(
				sqlmock.NewRows([]string{"count"}).AddRow(0),
			)
			smock.ExpectExec(`UPDATE`).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		_ = fn(mockTx)
	}).Return(nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	require.NoError(t, err)
	assert.Equal(t, 2, txCallCount)
}

func TestCleanupEmptyLeaseActivity_K8sLeaseNotFoundIgnored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	oldProto, oldEndpoint := harvestRestProtocol, harvestEndPoint
	harvestRestProtocol = "http"
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	defer func() { harvestRestProtocol = oldProto; harvestEndPoint = oldEndpoint }()

	oldDeleteK8sLease := deleteKubernetesLeaseForEmptyHarvestPollers
	deleteKubernetesLeaseForEmptyHarvestPollers = func(ctx context.Context, ns, name string) error {
		return errors.New("lease not found")
	}
	defer func() { deleteKubernetesLeaseForEmptyHarvestPollers = oldDeleteK8sLease }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeGroupMapNodeCount", mock.Anything, int64(5)).Return(int64(0), nil)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(1).(func(dbutils.Transaction) error)
		db, smock, _ := sqlmock.New()
		defer func() { _ = db.Close() }()
		gormDB, _ := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		mockTx := dbutils.NewMockTransaction(t)
		mockTx.On("GORM").Return(gormDB)
		smock.ExpectQuery(`SELECT`).WillReturnRows(
			sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(5, "harvest-l5"),
		)
		smock.ExpectQuery(`SELECT count`).WillReturnRows(
			sqlmock.NewRows([]string{"count"}).AddRow(0),
		)
		if smock.ExpectationsWereMet() != nil {
			// Second tx: soft delete
			smock.ExpectExec(`UPDATE`).WillReturnResult(sqlmock.NewResult(0, 1))
		}
		_ = fn(mockTx)
	}).Return(nil)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.CleanupEmptyLeaseActivity)

	_, err := env.ExecuteActivity(act.CleanupEmptyLeaseActivity, &CleanupEmptyLeaseParams{NodeGroupID: 5, LeaseName: "harvest-l5"})
	// "not found" in k8s lease error is logged but not returned
	require.NoError(t, err)
}

func TestUploadValidateCommitRebalanceActivity_NilParams(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, (*UploadValidateCommitRebalanceParams)(nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload+validate+commit params required")
}

func TestUploadValidateCommitRebalanceActivity_InvalidMaxNodes(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{1}}},
		UploadURL:        "http://localhost:3000/upload",
		MaxNodesPerGroup: 0,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max nodes per group must be > 0")
}

func TestUploadValidateCommitRebalanceActivity_EmptyMoves(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	res, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{},
		UploadURL:        "http://localhost:3000/upload",
		MaxNodesPerGroup: 200,
	})
	require.NoError(t, err)
	var got RebalanceUploadStageResult
	require.NoError(t, res.Get(&got))
	assert.Empty(t, got.Staged)
}

func TestUploadValidateCommitRebalanceActivity_EmptyUploadURL(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{1}}},
		UploadURL:        "",
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload URL required")
}

func TestUploadValidateCommitRebalanceActivity_TransactionError(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(errors.New("tx failed"))
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{1}}},
		UploadURL:        "http://localhost:3000/upload",
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tx failed")
}

func TestUploadValidateCommitRebalanceActivity_TargetGroupMissingAfterLock(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(
		func(_ context.Context, fn func(dbutils.Transaction) error) error {
			db, smock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)
			mockTx := dbutils.NewMockTransaction(t)
			mockTx.On("GORM").Return(gormDB)

			smock.ExpectQuery(`SELECT.*FROM "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}),
			)
			return fn(mockTx)
		},
	)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
		UploadURL:        "http://localhost:3000/upload",
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target node group 2 missing or has empty lease name")
}

func TestUploadValidateCommitRebalanceActivity_SourceMismatch(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetActiveNodeNodeGroupMapsByNodeIDs", mock.Anything, []int64{10}, mock.Anything).Return(
		map[int64]*datamodel.NodeNodeGroupMap{
			10: {
				NodeID:        10,
				NodeGroupID:   99,
				HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "src-lease", PORT: "13001"},
			},
		}, nil)
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(
		func(_ context.Context, fn func(dbutils.Transaction) error) error {
			db, smock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)
			mockTx := dbutils.NewMockTransaction(t)
			mockTx.On("GORM").Return(gormDB)

			smock.ExpectQuery(`SELECT.*FROM "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(2, "tgt-lease"),
			)
			return fn(mockTx)
		},
	)
	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
		UploadURL:        "http://localhost:3000/upload",
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "move expects source 1")
}

// rebalanceUploadServer returns an httptest server that accepts the harvest YAML
// upload (POST) used by the upload step and answers any DELETE with 200.
func rebalanceUploadServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		w.WriteHeader(http.StatusOK)
	}))
}

// stubRebalanceSEForUpload wires the storage mock calls performed inside the upload
// transaction of UploadValidateCommitRebalanceActivity for a single node on source group 1.
// Node and pool are now read on the open transaction (g), so they are stubbed as
// ordered sqlmock queries via expectRebalanceNodePoolReads, not via the storage mock.
func stubRebalanceSEForUpload(mockSE *database.MockStorage, nodeID int64, _ int64, mapping *datamodel.NodeNodeGroupMap) {
	mockSE.On("GetActiveNodeNodeGroupMapsByNodeIDs", mock.Anything, []int64{nodeID}, mock.Anything).Return(
		map[int64]*datamodel.NodeNodeGroupMap{nodeID: mapping}, nil)
}

// expectRebalanceNodePoolReads sets the ordered sqlmock expectations for the in-transaction
// node (Unscoped) and pool reads done in the upload loop. The pool row carries a jsonb
// pool_credentials column so uploadHarvestNodeMapping can read AuthType/SecretID.
func expectRebalanceNodePoolReads(t *testing.T, smock sqlmock.Sqlmock, nodeID, poolID int64) {
	t.Helper()
	smock.ExpectQuery(`SELECT.*FROM "nodes"`).WillReturnRows(
		sqlmock.NewRows([]string{"id", "pool_id"}).AddRow(nodeID, poolID),
	)
	pc, err := json.Marshal(datamodel.PoolCredentials{AuthType: 1, SecretID: "sec"})
	require.NoError(t, err)
	smock.ExpectQuery(`SELECT.*FROM "pools"`).WillReturnRows(
		sqlmock.NewRows([]string{"id", "pool_credentials"}).AddRow(poolID, pc),
	)
}

func TestUploadValidateCommitRebalanceActivity_CapacityChanged(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	srv := rebalanceUploadServer(t)
	defer srv.Close()

	mockSE := database.NewMockStorage(t)
	stubRebalanceSEForUpload(mockSE, 10, 5, &datamodel.NodeNodeGroupMap{
		NodeID:        10,
		NodeGroupID:   1,
		HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "tgt-lease", PORT: "13001"},
	})
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(
		func(_ context.Context, fn func(dbutils.Transaction) error) error {
			db, smock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)
			mockTx := dbutils.NewMockTransaction(t)
			mockTx.On("GORM").Return(gormDB)

			// 1) lock target node groups FOR UPDATE
			smock.ExpectQuery(`SELECT.*FROM "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(2, "tgt-lease"),
			)
			// 2) port lookup (no assigned ports -> first free port chosen)
			smock.ExpectQuery(`harvest_config`).WillReturnRows(sqlmock.NewRows([]string{"port"}))
			// 2b) in-tx node + pool reads
			expectRebalanceNodePoolReads(t, smock, 10, 5)
			// 3) current count per target already at capacity
			smock.ExpectQuery(`COUNT`).WillReturnRows(
				sqlmock.NewRows([]string{"node_group_id", "cnt"}).AddRow(2, 200),
			)
			return fn(mockTx)
		},
	)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
		UploadURL:        srv.URL,
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "capacity changed")
}

func TestUploadValidateCommitRebalanceActivity_Success(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	srv := rebalanceUploadServer(t)
	defer srv.Close()

	mockSE := database.NewMockStorage(t)
	// Source lease equals target lease so the post-commit source delete is skipped.
	// A non-zero primary key makes g.Save issue an UPDATE rather than an INSERT.
	stubRebalanceSEForUpload(mockSE, 10, 5, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{ID: 100},
		NodeID:        10,
		NodeGroupID:   1,
		HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "tgt-lease", PORT: "13001"},
	})
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(
		func(_ context.Context, fn func(dbutils.Transaction) error) error {
			db, smock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)
			mockTx := dbutils.NewMockTransaction(t)
			mockTx.On("GORM").Return(gormDB)

			smock.ExpectQuery(`SELECT.*FROM "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(2, "tgt-lease"),
			)
			smock.ExpectQuery(`harvest_config`).WillReturnRows(sqlmock.NewRows([]string{"port"}))
			expectRebalanceNodePoolReads(t, smock, 10, 5)
			smock.ExpectQuery(`COUNT`).WillReturnRows(
				sqlmock.NewRows([]string{"node_group_id", "cnt"}).AddRow(2, 0),
			)
			// g.Save upserts the NodeGroup belongs-to association before updating the map row.
			smock.ExpectBegin()
			smock.ExpectQuery(`INSERT INTO "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id"}).AddRow(2),
			)
			smock.ExpectExec(`UPDATE "node_node_group_maps"`).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()
			return fn(mockTx)
		},
	)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	res, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
		UploadURL:        srv.URL,
		MaxNodesPerGroup: 200,
	})
	require.NoError(t, err)
	var out RebalanceUploadStageResult
	require.NoError(t, res.Get(&out))
	require.Len(t, out.Staged, 1)
	assert.Equal(t, int64(10), out.Staged[0].NodeID)
	assert.Equal(t, int64(2), out.Staged[0].TargetGroupID)
	assert.Equal(t, "tgt-lease", out.Staged[0].TargetLeaseName)
}

func TestUploadValidateCommitRebalanceActivity_PortConflict(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	srv := rebalanceUploadServer(t)
	defer srv.Close()

	mockSE := database.NewMockStorage(t)
	stubRebalanceSEForUpload(mockSE, 10, 5, &datamodel.NodeNodeGroupMap{
		BaseModel:     datamodel.BaseModel{ID: 100},
		NodeID:        10,
		NodeGroupID:   1,
		HarvestConfig: &datamodel.HarvestConfig{LEASE_NAME: "tgt-lease", PORT: "13001"},
	})
	mockSE.On("WithTransaction", mock.Anything, mock.Anything).Return(
		func(_ context.Context, fn func(dbutils.Transaction) error) error {
			db, smock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			gormDB, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
			require.NoError(t, err)
			mockTx := dbutils.NewMockTransaction(t)
			mockTx.On("GORM").Return(gormDB)

			smock.ExpectQuery(`SELECT.*FROM "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id", "lease_name"}).AddRow(2, "tgt-lease"),
			)
			smock.ExpectQuery(`harvest_config`).WillReturnRows(sqlmock.NewRows([]string{"port"}))
			expectRebalanceNodePoolReads(t, smock, 10, 5)
			smock.ExpectQuery(`COUNT`).WillReturnRows(
				sqlmock.NewRows([]string{"node_group_id", "cnt"}).AddRow(2, 0),
			)
			smock.ExpectBegin()
			smock.ExpectQuery(`INSERT INTO "node_groups"`).WillReturnRows(
				sqlmock.NewRows([]string{"id"}).AddRow(2),
			)
			smock.ExpectExec(`UPDATE "node_node_group_maps"`).WillReturnError(
				errors.New(`pq: duplicate key value violates unique constraint "idx_node_node_group_maps_group_port_active_uq"`),
			)
			smock.ExpectRollback()
			return fn(mockTx)
		},
	)

	act := &PollerRebalanceActivities{SE: mockSE}
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(act.UploadValidateCommitRebalanceActivity)

	_, err := env.ExecuteActivity(act.UploadValidateCommitRebalanceActivity, &UploadValidateCommitRebalanceParams{
		Moves:            []HarvestRebalanceMove{{SourceGroupID: 1, TargetGroupID: 2, NodeIDs: []int64{10}}},
		UploadURL:        srv.URL,
		MaxNodesPerGroup: 200,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "port conflict")
}

func TestPrefetchRebalanceCredentials_SMHarvestAuthSkipsFetch(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	creds, err := prefetchRebalanceCredentials(context.Background(), mockSE, []HarvestRebalanceMove{
		{NodeIDs: []int64{10}},
	})
	require.NoError(t, err)
	assert.Nil(t, creds)
}

func TestPrefetchRebalanceCredentials_DedupesByPool(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	oldGetPassword := vsa.GetPasswordFromCacheOrSecretManager
	smHarvestAuthEnabled = false
	defer func() {
		smHarvestAuthEnabled = oldAuth
		vsa.GetPasswordFromCacheOrSecretManager = oldGetPassword
	}()

	var fetchCount int
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		fetchCount++
		return "secret-pw", nil
	}

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(&datamodel.Node{BaseModel: datamodel.BaseModel{ID: 10}, PoolID: 5}, nil)
	mockSE.On("GetNodeByID", mock.Anything, int64(11)).Return(&datamodel.Node{BaseModel: datamodel.BaseModel{ID: 11}, PoolID: 5}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(5)).Return(&datamodel.Pool{
		BaseModel:       datamodel.BaseModel{ID: 5},
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 1, SecretID: "sec"},
	}, nil).Once()

	creds, err := prefetchRebalanceCredentials(context.Background(), mockSE, []HarvestRebalanceMove{
		{NodeIDs: []int64{10, 11}},
	})
	require.NoError(t, err)
	require.Len(t, creds, 1)
	assert.Equal(t, "secret-pw", creds[5].AdminPassword)
	assert.Equal(t, 1, fetchCount)
}

func TestRollbackTargetHarvestUploads_ReverseOrderAndContinueOnError(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.Path)
		if strings.Contains(r.URL.Path, "harvest-2") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	oldProto, oldEndpoint := harvestRestProtocol, harvestEndPoint
	harvestRestProtocol = "http"
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	defer func() { harvestRestProtocol = oldProto; harvestEndPoint = oldEndpoint }()

	rollbackTargetHarvestUploads(context.Background(), []RebalanceStagedNode{
		{NodeID: 1, TargetLeaseName: "lease-a"},
		{NodeID: 2, TargetLeaseName: "lease-a"},
		{NodeID: 3, TargetLeaseName: ""},
		{NodeID: 4, TargetLeaseName: "lease-a"},
	})

	require.Len(t, got, 3)
	assert.Contains(t, got[0], "/config/lease-a/harvest-4")
	assert.Contains(t, got[1], "/config/lease-a/harvest-2")
	assert.Contains(t, got[2], "/config/lease-a/harvest-1")
}

func TestValidateRebalanceStagedUploads(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(validateRebalanceStagedUploads)

	_, err := env.ExecuteActivity(validateRebalanceStagedUploads, []RebalanceStagedNode{{NodeID: 0, TargetLeaseName: "l", Port: "13001"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid node id")

	_, err = env.ExecuteActivity(validateRebalanceStagedUploads, []RebalanceStagedNode{
		{NodeID: 1, TargetLeaseName: "l", Port: "13001"},
		{NodeID: 1, TargetLeaseName: "l", Port: "13002"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate staged node id")

	_, err = env.ExecuteActivity(validateRebalanceStagedUploads, []RebalanceStagedNode{{NodeID: 1, TargetLeaseName: "", Port: "13001"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty target lease name")

	_, err = env.ExecuteActivity(validateRebalanceStagedUploads, []RebalanceStagedNode{{NodeID: 1, TargetLeaseName: "l", Port: "   "}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty poller port")

	_, err = env.ExecuteActivity(validateRebalanceStagedUploads, []RebalanceStagedNode{
		{NodeID: 1, TargetLeaseName: "l1", Port: "13001"},
		{NodeID: 2, TargetLeaseName: "l2", Port: "13002"},
	})
	require.NoError(t, err)
}

func TestBuildPollerRebalancePlanActivity(t *testing.T) {
	t.Run("nil params", func(t *testing.T) {
		act := &PollerRebalanceActivities{SE: database.NewMockStorage(t)}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		_, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, (*BuildPollerRebalancePlanParams)(nil))
		require.Error(t, err)
	})

	t.Run("empty node groups", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		act := &PollerRebalanceActivities{SE: mockSE}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		res, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, &BuildPollerRebalancePlanParams{NodeGroups: nil})
		require.NoError(t, err)
		var out HarvestRebalancePlanOutput
		require.NoError(t, res.Get(&out))
		require.Empty(t, out.Moves)
	})

	t.Run("list maps error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(10)).Return(nil, errors.New("maps err"))
		act := &PollerRebalanceActivities{SE: mockSE}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		_, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, &BuildPollerRebalancePlanParams{
			NodeGroups:       []datamodel.NodeGroupPollerCount{{NodeGroupID: 10, LeaseName: "L", Count: 1}},
			MaxNodesPerGroup: 200,
			EvictThreshold:   20,
			SoftThreshold:    50,
		})
		require.Error(t, err)
	})

	t.Run("ha sibling error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(11)).Return([]*datamodel.NodeNodeGroupMap{
			{NodeID: 99, NodeGroupID: 11, HarvestConfig: &datamodel.HarvestConfig{}},
		}, nil)
		mockSE.On("GetHarvestHaSiblingNodeID", mock.Anything, int64(99)).Return(int64(0), errors.New("sibling err"))
		act := &PollerRebalanceActivities{SE: mockSE}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		_, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, &BuildPollerRebalancePlanParams{
			NodeGroups:       []datamodel.NodeGroupPollerCount{{NodeGroupID: 11, LeaseName: "L", Count: 1}},
			MaxNodesPerGroup: 200,
			EvictThreshold:   20,
			SoftThreshold:    50,
		})
		require.Error(t, err)
	})

	t.Run("count zero skips list", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		act := &PollerRebalanceActivities{SE: mockSE}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		res, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, &BuildPollerRebalancePlanParams{
			NodeGroups:       []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, LeaseName: "a", Count: 0}},
			MaxNodesPerGroup: 200,
			EvictThreshold:   20,
			SoftThreshold:    50,
		})
		require.NoError(t, err)
		var out HarvestRebalancePlanOutput
		require.NoError(t, res.Get(&out))
		require.Empty(t, out.Moves)
	})

	t.Run("success minimal", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("ListNodeNodeGroupMapsByNodeGroupID", mock.Anything, int64(1)).Return([]*datamodel.NodeNodeGroupMap{
			{NodeID: 10, NodeGroupID: 1, HarvestConfig: &datamodel.HarvestConfig{}},
		}, nil)
		mockSE.On("GetHarvestHaSiblingNodeID", mock.Anything, int64(10)).Return(int64(0), nil)
		act := &PollerRebalanceActivities{SE: mockSE}
		ts := &testsuite.WorkflowTestSuite{}
		env := ts.NewTestActivityEnvironment()
		env.RegisterActivity(act.BuildPollerRebalancePlanActivity)
		_, err := env.ExecuteActivity(act.BuildPollerRebalancePlanActivity, &BuildPollerRebalancePlanParams{
			NodeGroups:       []datamodel.NodeGroupPollerCount{{NodeGroupID: 1, LeaseName: "L", Count: 1}},
			MaxNodesPerGroup: 200,
			EvictThreshold:   20,
			SoftThreshold:    50,
		})
		require.NoError(t, err)
	})
}

type stubRebalanceBucketClient struct {
	uploadFn       func(ctx context.Context, bucketName, objectName string, payload []byte, contentType string) error
	deleteFn       func(ctx context.Context, bucketName, objectName string) error
	deletePrefixFn func(ctx context.Context, bucketName, objectPrefix string) error
}

func (s *stubRebalanceBucketClient) UploadObject(ctx context.Context, bucketName, objectName string, payload []byte, contentType string) error {
	if s.uploadFn != nil {
		return s.uploadFn(ctx, bucketName, objectName, payload, contentType)
	}
	return nil
}

func (s *stubRebalanceBucketClient) DeleteObject(ctx context.Context, bucketName, objectName string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, bucketName, objectName)
	}
	return nil
}

func (s *stubRebalanceBucketClient) DeleteObjectsWithPrefix(ctx context.Context, bucketName, objectPrefix string) error {
	if s.deletePrefixFn != nil {
		return s.deletePrefixFn(ctx, bucketName, objectPrefix)
	}
	return nil
}

func TestParseGCSURL(t *testing.T) {
	bucket, prefix, isGCS, err := parseGCSURL("http://localhost/upload")
	require.NoError(t, err)
	assert.False(t, isGCS)
	assert.Empty(t, bucket)

	bucket, prefix, isGCS, err = parseGCSURL("gs://my-bucket/some/prefix")
	require.NoError(t, err)
	assert.True(t, isGCS)
	assert.Equal(t, "my-bucket", bucket)
	assert.Equal(t, "some/prefix", prefix)

	_, _, isGCS, err = parseGCSURL("gs:///no-bucket")
	require.Error(t, err)
	assert.False(t, isGCS)
}

func TestGcsRebalanceObject(t *testing.T) {
	assert.Equal(t, "lease-a/harvest-7.yaml", gcsRebalanceObject("", "lease-a", 7))
	assert.Equal(t, "prefix/lease-b/harvest-9.yaml", gcsRebalanceObject("prefix", "lease-b", 9))
	assert.Equal(t, "trimmed/lease-c/harvest-1.yaml", gcsRebalanceObject("/trimmed/", "/lease-c/", 1))
}

func TestRenderRebalanceYAML_nilPoolCredentials(t *testing.T) {
	_, err := renderRebalanceYAML(&datamodel.HarvestConfig{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool is required")

	_, err = renderRebalanceYAML(&datamodel.HarvestConfig{}, &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 9}}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool 9 has nil credentials")
}

func TestRenderRebalanceYAML_smHarvestAuthEnabled(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = true
	defer func() { smHarvestAuthEnabled = oldAuth }()

	hc := &datamodel.HarvestConfig{PORT: "13001", POLLER_NAME: "p1", DATACENTER: "dc1", NODE_IP: "1.2.3.4"}
	pool := &datamodel.Pool{
		PoolCredentials: &datamodel.PoolCredentials{AuthType: 2, SecretID: "sec-1"},
	}
	out, err := renderRebalanceYAML(hc, pool, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.Equal(t, 2, hc.AUTH_TYPE)
	assert.Equal(t, "sec-1", hc.SECRET_ID)
}

func TestUploadRebalanceTarget_GCS(t *testing.T) {
	var uploadedBucket, uploadedObject, uploadedType string
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return &stubRebalanceBucketClient{
			uploadFn: func(_ context.Context, bucketName, objectName string, payload []byte, contentType string) error {
				uploadedBucket = bucketName
				uploadedObject = objectName
				uploadedType = contentType
				assert.Equal(t, []byte("yaml-body"), payload)
				return nil
			},
		}, nil
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	err := uploadRebalanceTarget(context.Background(), "gs://rebalance-bucket/prefix", "lease-a", 42, "yaml-body")
	require.NoError(t, err)
	assert.Equal(t, "rebalance-bucket", uploadedBucket)
	assert.Equal(t, "prefix/lease-a/harvest-42.yaml", uploadedObject)
	assert.Equal(t, "application/x-yaml", uploadedType)
}

func TestUploadRebalanceTarget_GCSClientError(t *testing.T) {
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return nil, errors.New("bucket client unavailable")
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	err := uploadRebalanceTarget(context.Background(), "gs://bucket", "lease-a", 1, "yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket client unavailable")
}

func TestDeleteUploadedRebalanceTarget_GCS(t *testing.T) {
	var deletedBucket, deletedObject string
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return &stubRebalanceBucketClient{
			deleteFn: func(_ context.Context, bucketName, objectName string) error {
				deletedBucket = bucketName
				deletedObject = objectName
				return nil
			},
		}, nil
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	err := deleteUploadedRebalanceTarget(context.Background(), "gs://bucket/prefix", "lease-a", 7)
	require.NoError(t, err)
	assert.Equal(t, "bucket", deletedBucket)
	assert.Equal(t, "prefix/lease-a/harvest-7.yaml", deletedObject)
}

func TestDeleteUploadedRebalanceTarget_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	oldHost, oldProto := harvestEndPoint, harvestRestProtocol
	harvestEndPoint = strings.TrimPrefix(srv.URL, "http://")
	harvestRestProtocol = "http"
	t.Cleanup(func() { harvestEndPoint = oldHost; harvestRestProtocol = oldProto })

	err := deleteUploadedRebalanceTarget(context.Background(), "http://localhost/upload", "lease-a", 7)
	require.NoError(t, err)
}

func TestDeleteRebalanceLeasePrefixInBucketClient(t *testing.T) {
	require.NoError(t, deleteRebalanceLeasePrefixInBucketClient(context.Background(), "", "lease"))
	require.NoError(t, deleteRebalanceLeasePrefixInBucketClient(context.Background(), "bucket", ""))

	var gotBucket, gotPrefix string
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return &stubRebalanceBucketClient{
			deletePrefixFn: func(_ context.Context, bucketName, objectPrefix string) error {
				gotBucket = bucketName
				gotPrefix = objectPrefix
				return nil
			},
		}, nil
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	require.NoError(t, deleteRebalanceLeasePrefixInBucketClient(context.Background(), "bucket", "lease-a"))
	assert.Equal(t, "bucket", gotBucket)
	assert.Equal(t, "lease-a/", gotPrefix)
}

func TestRollbackUploadedTargets_GCSAndHTTP(t *testing.T) {
	var deletedObjects []string
	oldClient := getRebalanceBucketClient
	getRebalanceBucketClient = func(ctx context.Context) (hyperscaler.RebalanceBucketClient, error) {
		return &stubRebalanceBucketClient{
			deleteFn: func(_ context.Context, _, objectName string) error {
				deletedObjects = append(deletedObjects, objectName)
				return nil
			},
		}, nil
	}
	defer func() { getRebalanceBucketClient = oldClient }()

	rollbackUploadedTargets(context.Background(), "gs://bucket", []RebalanceStagedNode{
		{NodeID: 1, TargetLeaseName: "lease-a"},
		{NodeID: 2, TargetLeaseName: ""},
		{NodeID: 3, TargetLeaseName: "lease-b"},
	})
	assert.Equal(t, []string{"lease-b/harvest-3.yaml", "lease-a/harvest-1.yaml"}, deletedObjects)
}

func TestPrefetchRebalanceCredentials_NodeLookupError(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = false
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(nil, errors.New("node missing"))

	_, err := prefetchRebalanceCredentials(context.Background(), mockSE, []HarvestRebalanceMove{{NodeIDs: []int64{10}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node missing")
}

func TestPrefetchRebalanceCredentials_PoolLookupError(t *testing.T) {
	oldAuth := smHarvestAuthEnabled
	smHarvestAuthEnabled = false
	defer func() { smHarvestAuthEnabled = oldAuth }()

	mockSE := database.NewMockStorage(t)
	mockSE.On("GetNodeByID", mock.Anything, int64(10)).Return(&datamodel.Node{BaseModel: datamodel.BaseModel{ID: 10}, PoolID: 5}, nil)
	mockSE.On("GetPoolByID", mock.Anything, int64(5)).Return(nil, errors.New("pool missing"))

	_, err := prefetchRebalanceCredentials(context.Background(), mockSE, []HarvestRebalanceMove{{NodeIDs: []int64{10}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool missing")
}

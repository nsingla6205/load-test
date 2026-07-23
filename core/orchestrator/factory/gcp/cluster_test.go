package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

func TestAcceptClusterPeer(t *testing.T) {
	origGetAccountWithName := getAccountWithName
	t.Cleanup(func() { getAccountWithName = origGetAccountWithName })

	temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
	t.Run("WhenGetOrCreateAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")
		pass := "testpass"
		var params = &common.ClusterPeerParams{
			PeerAddresses:      []string{"10.91.0.0", "10.92.0.0"},
			PeerName:           "testPeer",
			AccountName:        "testAccount",
			GeneratePassphrase: false,
			Passphrase:         &pass,
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		_, _, err = acceptClusterPeer(ctx, store, temporal, params, "poolID")
		assert.EqualError(tt, err, "account not found")
	})
	t.Run("WhenGetPoolFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to clean up test storage")

		pass := "testpass"
		var params = &common.ClusterPeerParams{
			PeerAddresses:      []string{"10.91.0.0", "10.92.0.0"},
			PeerName:           "testPeer",
			AccountName:        "testAccount",
			GeneratePassphrase: false,
			Passphrase:         &pass,
		}
		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
			},
			Name: "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: 123,
		}
		_ = store.DB().Create(pool).Error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}

		_, _, err = acceptClusterPeer(ctx, store, temporal, params, "poolID")
		var customErr *vsaerrors.CustomError
		if errors.As(err, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "Pool 'poolID' not found")
		} else {
			tt.Fatalf("Expected a CustomError, got %v", err)
		}
	})
	t.Run("WhenSucceed", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal1 := workflow_engine_mock.NewMockTemporalTestClient(t)
		mockStorage := new(database.MockStorage)

		pass := "testpass"
		var params = &common.ClusterPeerParams{
			PeerAddresses:      []string{"10.91.0.0", "10.92.0.0"},
			PeerName:           "testPeer",
			AccountName:        "testAccount",
			GeneratePassphrase: false,
			Passphrase:         &pass,
		}
		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
				ID:   123,
			},
			Name: "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "poolID"},
			Name:      "test_pool",
			AccountID: 123,
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}
		poolView := &datamodel.PoolView{
			Pool: *pool,
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
			WorkflowID: "workflow-id",
		}
		mockStorage.On("GetPoolByUUIDForAccount", ctx, "poolID", mock.Anything).Return(pool, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		temporal1.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, &poolView.Pool).Return(nil, nil)

		_, _, err := acceptClusterPeer(ctx, mockStorage, temporal1, params, "poolID")
		if err != nil {
			t.Errorf("Expected nil, got error")
		}
	})
	t.Run("WhenTemporalFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to clean up test storage")

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("temporal error"))

		pass := "testpass"
		var params = &common.ClusterPeerParams{
			PeerAddresses:      []string{"10.91.0.0", "10.92.0.0"},
			PeerName:           "testPeer",
			AccountName:        "testAccount",
			GeneratePassphrase: false,
			Passphrase:         &pass,
		}
		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "test-uuid",
				ID:   123,
			},
			Name: "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "poolID"},
			Name:      "test_pool",
			AccountID: 123,
		}
		_ = store.DB().Create(pool).Error
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return dbAccount, nil
		}

		_, _, err = acceptClusterPeer(ctx, store, temporal, params, "poolID")
		assert.EqualError(tt, err, "temporal error")
	})
}

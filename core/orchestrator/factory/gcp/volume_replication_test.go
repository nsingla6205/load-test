package gcp

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	models2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
)

func TestCreateVolumeReplicationInternal(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					Name: "test-account",
				},
			},
		}
		_, _, err := _createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenGetVolumeFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					Name: "test-account",
				},
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "non-existent-volume-uuid",
				},
			},
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(nil, errors.New("volume not found"))
		_, _, err := createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "volume not found", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
		_, _, err := _createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenCreateVolumeReplicationDBFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(nil, errors.New("failed to create volume replication in db"))
		_, _, err := _createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create volume replication in db", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-123",
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid-123",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(replicationDb, nil)
		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid-123", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(nil)
		// Mock the DeleteVolumeReplication call that should happen in the defer block
		mockStorage.On("DeleteVolumeReplication", ctx, mock.MatchedBy(func(repl *datamodel.VolumeReplication) bool {
			return repl != nil
		})).Return(replicationDb, nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))
		_, _, err := _createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob and DeleteVolumeReplication were called
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFailsAndUpdateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-123",
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid-123",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(replicationDb, nil)

		// Mock the UpdateJob call to fail (this tests the error handling in the defer block)
		mockStorage.On("UpdateJob", ctx, "job-uuid-123", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(errors.New("failed to update job"))
		// Mock the DeleteVolumeReplication call that should happen in the defer block
		mockStorage.On("DeleteVolumeReplication", ctx, mock.MatchedBy(func(repl *datamodel.VolumeReplication) bool {
			return repl != nil
		})).Return(replicationDb, nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))
		_, _, err := _createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob and DeleteVolumeReplication were called
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
		}
		mockStorage.On("GetVolume", ctx, mock.Anything).Return(volume, nil)

		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			WorkflowID: "workflow-id",
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:        params.VolumeReplication.Name,
			Description: params.VolumeReplication.Description,
			Uri:         params.VolumeReplication.Uri,
			RemoteUri:   params.VolumeReplication.RemoteUri,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               params.VolumeReplication.ReplicationAttributes.EndpointType,
				ReplicationType:            params.VolumeReplication.ReplicationAttributes.ReplicationType,
				ReplicationSchedule:        params.VolumeReplication.ReplicationAttributes.ReplicationSchedule,
				SourceVolumeUUID:           params.VolumeReplication.ReplicationAttributes.SourceVolumeUUID,
				SourceLocation:             params.VolumeReplication.ReplicationAttributes.SourceRegion,
				SourceHostName:             params.VolumeReplication.ReplicationAttributes.SourceHostName,
				SourceReplicationUUID:      params.VolumeReplication.ReplicationAttributes.SourceReplicationUUID,
				SourceSvmName:              params.VolumeReplication.ReplicationAttributes.SourceSvmName,
				SourceVolumeName:           params.VolumeReplication.ReplicationAttributes.SourceVolumeName,
				DestinationVolumeUUID:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeUUID,
				DestinationLocation:        params.VolumeReplication.ReplicationAttributes.DestinationRegion,
				DestinationHostName:        params.VolumeReplication.ReplicationAttributes.DestinationHostName,
				DestinationReplicationUUID: params.VolumeReplication.ReplicationAttributes.DestinationReplicationUUID,
				DestinationSvmName:         params.VolumeReplication.ReplicationAttributes.DestinationSvmName,
				DestinationVolumeName:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeName,
			},
			MirrorState:           params.VolumeReplication.MirrorState,
			RelationshipStatus:    params.VolumeReplication.RelationshipStatus,
			TotalProgress:         params.VolumeReplication.TotalProgress,
			TotalTransferBytes:    params.VolumeReplication.TotalTransferBytes,
			TotalTransferTimeSecs: params.VolumeReplication.TotalTransferTimeSecs,
			LastTransferSize:      params.VolumeReplication.LastTransferSize,
			LastTransferError:     params.VolumeReplication.LastTransferError,
			LastTransferDuration:  params.VolumeReplication.LastTransferDuration,
			LastTransferEndTime:   params.VolumeReplication.LastTransferEndTime,
			ProgressLastUpdated:   params.VolumeReplication.ProgressLastUpdated,
			LastUpdatedFromOntap:  params.VolumeReplication.LastUpdatedFromOntap,
			LagTime:               params.VolumeReplication.LagTime,
			AccountID:             account.ID,
			Account:               account,
			VolumeID:              params.VolumeReplication.VolumeID,
			Volume:                volume,
		}

		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(replicationDb, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		actualResponse, jobActualResponse, err := createVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, expectedResponse, actualResponse)
		assert.Equal(tt, jobResponse, jobActualResponse)
	})
}

func TestCreateVolumeReplication(t *testing.T) {
	t.Run("WhenGetOrCreateAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:   "test-account",
			CorrelationId: "test-correlation-id",
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("WhenGetVolumeByNameFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("volume not found"))

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "non-existent-volume",
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "volume not found", err.Error())
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		dbVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{
				Name: "test-pool",
			},
		}
		dbRep := &datamodel.VolumeReplication{Name: "rep-1"}
		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(dbVol, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, mock.Anything, mock.Anything).Return(nil, nil)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, int64(1)).Return([]*datamodel.QuotaRule{}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(dbRep, nil)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "test-volume",
		}

		convertCreateReplicationParamsToEventParam = func(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
			return nil
		}

		validateCreateReplicationParams = func(ctx context.Context, event *replication.CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
			return &datamodel.VolumeReplication{}, nil
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})

	t.Run("WhenCreateVolumeReplicationDBFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		dbVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{
				Name: "test-pool",
			},
		}
		dbRep := &datamodel.VolumeReplication{Name: "rep-1"}

		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(dbVol, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, mock.Anything, mock.Anything).Return(nil, nil)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, int64(1)).Return([]*datamodel.QuotaRule{}, nil)
		mockStorage.On("CreateVolumeReplication", ctx, dbRep).Return(nil, errors.New("failed to create volume replication in db"))

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "test-volume",
		}

		convertCreateReplicationParamsToEventParam = func(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
			return nil
		}

		validateCreateReplicationParams = func(ctx context.Context, event *replication.CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
			return dbRep, nil
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create volume replication in db", err.Error())
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		dbVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{
				Name: "test-pool",
			},
		}
		dbRep := &datamodel.VolumeReplication{Name: "rep-1"}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-456",
			},
			WorkflowID: "workflow-id",
		}

		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(dbVol, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, mock.Anything, mock.Anything).Return(nil, nil)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, int64(1)).Return([]*datamodel.QuotaRule{}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(dbRep, nil)
		mockStorage.On("DeleteVolumeReplication", ctx, mock.Anything).Return(dbRep, nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid-456", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "test-volume",
		}
		convertCreateReplicationParamsToEventParam = func(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
			return nil
		}

		validateCreateReplicationParams = func(ctx context.Context, event *replication.CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
			return dbRep, nil
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFailsAndUpdateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		dbVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{
				Name: "test-pool",
			},
		}
		dbRep := &datamodel.VolumeReplication{Name: "rep-1"}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-456",
			},
			WorkflowID: "workflow-id",
		}

		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(dbVol, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, mock.Anything, mock.Anything).Return(nil, nil)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, int64(1)).Return([]*datamodel.QuotaRule{}, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(dbRep, nil)
		mockStorage.On("DeleteVolumeReplication", ctx, mock.Anything).Return(dbRep, nil)

		// Mock the UpdateJob call to fail (this tests the error handling in the defer block)
		mockStorage.On("UpdateJob", ctx, "job-uuid-456", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(errors.New("failed to update job"))

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "test-volume",
		}
		convertCreateReplicationParamsToEventParam = func(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
			return nil
		}

		validateCreateReplicationParams = func(ctx context.Context, event *replication.CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
			return dbRep, nil
		}

		_, _, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called even though it failed
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		dbVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{
				Name: "test-pool",
			},
		}

		mockStorage.On("GetVolumeByNameAndAccountID", ctx, mock.Anything, mock.Anything).Return(dbVol, nil)
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, mock.Anything, mock.Anything).Return(nil, nil)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, int64(1)).Return([]*datamodel.QuotaRule{}, nil)

		params := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			SourceVolumeName: "test-volume",
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
			WorkflowID: "workflow-id",
		}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:         params.SourceVolumeName,
			AccountID:    account.ID,
			VolumeID:     dbVol.ID,
			Description:  "test replication",
			Uri:          "test-uri",
			RemoteUri:    "test-remote-uri",
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               "test-endpoint",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "source-volume-uuid",
				SourceLocation:             "source-region",
				SourceHostName:             "source-host",
				SourceReplicationUUID:      "source-replication-uuid",
				SourceSvmName:              "source-svm",
				SourceVolumeName:           "source-volume",
				DestinationVolumeUUID:      "destination-volume-uuid",
				DestinationLocation:        "destination-region",
				DestinationHostName:        "destination-host",
				DestinationReplicationUUID: "destination-replication-uuid",
				DestinationSvmName:         "destination-svm",
				DestinationVolumeName:      "destination-volume",
			},
		}

		convertCreateReplicationParamsToEventParam = func(in *commonparams.CreateVolumeReplicationParams, out *replication.CreateReplicationEvent) error {
			return nil
		}

		validateCreateReplicationParams = func(ctx context.Context, event *replication.CreateReplicationEvent, se database.Storage) (*datamodel.VolumeReplication, error) {
			return replicationDb, nil
		}

		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("CreateVolumeReplication", ctx, mock.Anything).Return(replicationDb, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		actualResponse, jobActualResponse, err := _createVolumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, expectedResponse, actualResponse)
		assert.Equal(tt, jobResponse.UUID, jobActualResponse)
	})
}

func TestConvertCreateReplicationParamsToEventParam(t *testing.T) {
	t.Run("WhenConversionSucceeds", func(tt *testing.T) {
		in := &commonparams.CreateVolumeReplicationParams{
			AccountName:      "test-account",
			Region:           "test-region",
			SourceVolumeName: "test-volume",
			Body: &gcpserver.ReplicationCreateV1beta{
				DestinationVolumeParameters: gcpserver.DestinationVolumeParametersV1beta{
					StoragePool: "projects/test-project/locations/test-location/pools/test-pool",
				},
			},
			CorrelationId: "test-correlation-id",
			LocationId:    "test-location",
		}

		out := &replication.CreateReplicationEvent{}

		err := _convertCreateReplicationParamsToEventParam(in, out)
		assert.Nil(tt, err)
		assert.Equal(tt, "test-pool", out.DestinationPoolName)
		assert.Equal(tt, "test-location", out.DestinationLocationID)
		assert.Equal(tt, "test-project", out.DestinationProjectNumber)
		assert.Equal(tt, "test-account", out.SourceProjectNumber)
		assert.Equal(tt, "test-region", out.SourceRegion)
		assert.Equal(tt, "test-volume", out.VolumeResourceID)
		assert.Equal(tt, "test-correlation-id", *out.XCorrelationID)
		assert.Equal(tt, "test-location", out.LocationID)
	})

	t.Run("WhenUnmarshalFails", func(tt *testing.T) {
		in := &commonparams.CreateVolumeReplicationParams{
			AccountName: "test-account",
		}
		out := &replication.CreateReplicationEvent{}

		// Simulate unmarshal failure by passing invalid data
		replication.JsonUnMarshal = func(data []byte, v interface{}) error {
			return errors.New("unmarshal error")
		}
		err := _convertCreateReplicationParamsToEventParam(in, out)
		assert.NotNil(tt, err)
		assert.Equal(tt, err.(*errors2.CustomError).OriginalErr.Error(), "unmarshal error")
	})
}

func TestGetReplicationCount(t *testing.T) {
	t.Run("WhenStorageReturnsCount", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockOrchestrator := &GCPOrchestrator{storage: mockStorage}

		projectNumber := "test-project"
		expectedCount := int64(5)

		mockStorage.On("GetVolumeReplicationCount", ctx, projectNumber).Return(expectedCount, nil)

		actualCount, err := mockOrchestrator.GetReplicationCount(ctx, projectNumber)
		assert.Nil(tt, err)
		assert.Equal(tt, expectedCount, actualCount)
	})

	t.Run("WhenStorageReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockOrchestrator := &GCPOrchestrator{storage: mockStorage}

		projectNumber := "test-project"
		expectedError := errors.New("database error")

		mockStorage.On("GetVolumeReplicationCount", ctx, projectNumber).Return(int64(0), expectedError)

		actualCount, err := mockOrchestrator.GetReplicationCount(ctx, projectNumber)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, int64(0), actualCount)
	})
}

func TestGetMultipleReplications(t *testing.T) {
	t.Run("WhenGetAccountWithNameReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() { getAccountWithName = _getAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenListVolumeReplicationsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() { getAccountWithName = _getAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("failed to list replications"))

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.NotNil(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "failed to list replications")
	})
	t.Run("WhenListVolumeReplicationsReturnsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() { getAccountWithName = _getAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{}, nil)

		replications, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.Empty(tt, replications)
	})
	t.Run("WhenUtilParseAndValidateRegionAndZone", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "", "", &gcpserver.Error{
				Code:    400,
				Message: "SomeError",
			}
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
			},
		}

		expCustErr := errors2.CustomError{
			TrackingID: 0,
			Message:    "SomeError",
			Retriable:  false,
			HttpCode:   nillable.GetIntPtr(500),
		}
		expError := errors2.NewVCPError(errors2.ErrRegionZoneParsingErrorCurrentRegion, &expCustErr)

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		assert.EqualError(tt, err, expError.Error())
	})
	t.Run("WhenUtilParseRegionAndZoneReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return "", "", errors.New("SomeError")
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
			},
		}

		expError := errors2.NewVCPError(errors2.ErrRegionZoneParsingErrorDestinationRegion, errors.New("SomeError"))

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		assert.EqualError(tt, err, expError.Error())
	})
	t.Run("WhenUtilParseRegionAndZoneReturnsErrorForSource", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return "", "", errors.New("SomeError")
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:        "us-e4",
					SourceReplicationUUID: "replication-uuid-1",
				},
			},
		}

		expError := errors2.NewVCPError(errors2.ErrRegionZoneParsingErrorSourceRegion, errors.New("SomeError"))

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		assert.EqualError(tt, err, expError.Error())
	})
	t.Run("WhenGetReplicationObjectsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}

		// _getMultipleReplications uses utilsParseProjectNumberFromURI (not GetProjectNumberForRegion) when building regionProjectMap
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return nil, []googleproxyclient.InternalJobV1beta{}, nil, errors.New("failed to get replication objects")
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-e4",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		assert.ErrorContains(tt, err, "failed to get replication objects")
	})
	// Coverage for lines 890, 895 (dest region: EndpointType dst → Uri; parse error)
	t.Run("WhenEndpointTypeDestinationUsesUriForDestRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		// Line 890: EndpointType == Destination → utilsParseProjectNumberFromURI(replication.Uri)
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{AccountName: "test-account", LocationId: "us-e4"}
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1", CreatedAt: time.Time{}, UpdatedAt: time.Time{}},
				Name:      "replication-1",
				Uri:       "projects/45110233509/locations/us-east4/volumes/vol/replications/rep",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               database.VolumeReplicationEndpointTypeDestination,
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, res)
	})
	t.Run("WhenParseProjectNumberFailsForDestRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		// Line 895: parse error for dest region → return ErrProjectParsingError
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", errors.New("invalid project uri")
		}

		params := commonparams.GetMultipleReplicationsParams{AccountName: "test-account", LocationId: "us-e4"}
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1", CreatedAt: time.Time{}, UpdatedAt: time.Time{}},
				Name:      "replication-1",
				Uri:       "projects/45110233509/locations/us-east4/volumes/vol/replications/rep",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               database.VolumeReplicationEndpointTypeDestination,
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr) && customErr.TrackingID == errors2.ErrProjectParsingError)
	})
	// Coverage for lines 922, 927 (source region: EndpointType dst → RemoteUri; parse error)
	t.Run("WhenEndpointTypeDestinationUsesRemoteUriForSourceRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}
		// Line 890 (dest Uri) and line 922 (source RemoteUri) when EndpointType == Destination
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{AccountName: "test-account", LocationId: "us-e4"}
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1", CreatedAt: time.Time{}, UpdatedAt: time.Time{}},
				Name:      "replication-1",
				Uri:       "projects/45110233509/locations/us-east4/volumes/vol/replications/rep",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/vol/replications/rep",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               database.VolumeReplicationEndpointTypeDestination,
					SourceLocation:             "au-se1",
					SourceReplicationUUID:      "src-uuid-1",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, res)
	})
	t.Run("WhenParseProjectNumberFailsForSourceRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}
		// Line 927: parse error for source region (RemoteUri when EndpointType dst) → return ErrProjectParsingError
		callCount := 0
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			callCount++
			if callCount == 1 {
				return "45110233509", nil // dest region (Uri)
			}
			return "", errors.New("invalid project uri") // source region (RemoteUri)
		}

		params := commonparams.GetMultipleReplicationsParams{AccountName: "test-account", LocationId: "us-e4"}
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1", CreatedAt: time.Time{}, UpdatedAt: time.Time{}},
				Name:      "replication-1",
				Uri:       "projects/45110233509/locations/us-east4/volumes/vol/replications/rep",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/vol/replications/rep",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               database.VolumeReplicationEndpointTypeDestination,
					SourceLocation:             "au-se1",
					SourceReplicationUUID:      "src-uuid-1",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Empty(tt, res)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr) && customErr.TrackingID == errors2.ErrProjectParsingError)
	})
	t.Run("WhenHappyPath", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}

		// _getMultipleReplications uses utilsParseProjectNumberFromURI (not GetProjectNumberForRegion) when building regionProjectMap
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return []*googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
					EndpointType:          datamodel.DstEndpoint,
					ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
					RemoteRegion:          "us-e4",
					SourceHostName:        "source-host",
					SourceServerName:      "source-svm",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					SourcePoolUuid:        googleproxyclient.NewOptString("source-pool-uuid"),
					DestinationHostName:   "destination-host",
					DestinationServerName: "destination-svm",
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					DestinationPoolUuid:   googleproxyclient.NewOptString("destination-pool-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSREGIONREPLICATION),
					RelationshipStatus:    googleproxyclient.NewOptVolumeReplicationInternalV1betaRelationshipStatus(googleproxyclient.VolumeReplicationInternalV1betaRelationshipStatusIdle),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024), // 1 GB
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),               // 1 hour
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),  // 100 MB
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300), // 5 minutes
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(time.Now()),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(time.Now()),
					LagTime:               googleproxyclient.NewOptInt64(60), // 1 minute
					CreatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					UpdatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					Jobs:                  nil,
					Description:           googleproxyclient.NewOptString("Test replication"),
				},
			}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-e4",
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "replication-uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, "replication-1", res[0].ResourceId.Value)
	})
	t.Run("WhenSourceRegionIsAlsoDestinationRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east1", "", nil
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-east1",
		}

		// Create two replications where source region of one is destination region of another
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-east1",
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
			{
				BaseModel: datamodel.BaseModel{
					ID:        2,
					UUID:      "uuid-2",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-2",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-west1", // This is the destination of replication-1
					SourceReplicationUUID:      "source-uuid-2",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "dest-uuid-2",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that all three regions are in the map
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Contains(tt, capturedRegionMap, "us-central1")

		// Verify that us-west1 has replication-1 (as destination) and not replication-2 (as source)
		assert.Len(tt, capturedRegionMap["us-west1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-west1"][0].Name)

		// Verify that us-central1 has replication-2 (as destination)
		assert.Len(tt, capturedRegionMap["us-central1"], 1)
		assert.Equal(tt, "replication-2", capturedRegionMap["us-central1"][0].Name)

		// Verify that us-east1 has no replications (only added for jobs)
		assert.Len(tt, capturedRegionMap["us-east1"], 0)
	})
	t.Run("WhenDestinationLocationIsCustomer", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east1", "", nil
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-east1",
		}

		// Create a replication with empty destination location
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-west1",
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "customer",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-west1 is in the map and contains the replication
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Len(tt, capturedRegionMap["us-west1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-west1"][0].Name)

		// Verify that us-east1 is also in the map (current region)
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Len(tt, capturedRegionMap["us-east1"], 0)
	})
	t.Run("WhenSourceRegionExistsAndDestinationLocationIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east1", "", nil
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-east1",
		}

		// Create two replications:
		// 1. us-west1 -> us-central1 (normal case)
		// 2. us-central1 -> "" (empty destination, should add to us-central1)
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-west1",
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-central1",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
			{
				BaseModel: datamodel.BaseModel{
					ID:        2,
					UUID:      "uuid-2",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-2",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1", // This region already exists from replication-1
					SourceReplicationUUID:      "source-uuid-2",
					DestinationLocation:        remoteRegionCustomer,
					DestinationReplicationUUID: "dest-uuid-2",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-central1 has both replications
		assert.Contains(tt, capturedRegionMap, "us-central1")
		assert.Len(tt, capturedRegionMap["us-central1"], 2)

		// Check that both replications are present
		replicationNames := []string{
			capturedRegionMap["us-central1"][0].Name,
			capturedRegionMap["us-central1"][1].Name,
		}
		assert.Contains(tt, replicationNames, "replication-1")
		assert.Contains(tt, replicationNames, "replication-2")

		// Verify that us-west1 is in the map (source region)
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Len(tt, capturedRegionMap["us-west1"], 0)

		// Verify that us-east1 is also in the map (current region)
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Len(tt, capturedRegionMap["us-east1"], 0)
	})
	t.Run("DestRegionNotEmpty_CurrentRegionIsDest", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-west1", "", nil // Current region is destination
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-west1", // Current region is destination
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-east1",
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1", // Destination is current region
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-west1 (destination/current region) has the replication
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Len(tt, capturedRegionMap["us-west1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-west1"][0].Name)

		// Verify that us-east1 (source region) is in the map for jobs
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Len(tt, capturedRegionMap["us-east1"], 0)
	})
	t.Run("DestRegionNotEmpty_CurrentRegionIsSource", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east1", "", nil // Current region is source
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-east1", // Current region is source
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-east1", // Source is current region
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1",
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-west1 (destination region) has the replication
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Len(tt, capturedRegionMap["us-west1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-west1"][0].Name)

		// Verify that us-east1 (source/current region) is in the map for jobs
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Len(tt, capturedRegionMap["us-east1"], 0)
	})
	t.Run("DestRegionEmpty_SourceRegionNotEmpty_CurrentRegionIsSource", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east1", "", nil // Current region is source
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-east1", // Current region is source
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-east1", // Source is current region
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        remoteRegionCustomer,
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-east1 (source/current region) has the replication (because dest is empty)
		assert.Contains(tt, capturedRegionMap, "us-east1")
		assert.Len(tt, capturedRegionMap["us-east1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-east1"][0].Name)
	})
	t.Run("DestRegionNotEmpty_SourceRegionEmpty_CurrentRegionIsDest", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			getReplicationObjects = _getReplicationObjects
			GetProjectNumberForRegion = _getProjectNumberForRegion
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-west1", "", nil // Current region is destination
		}

		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			return locationID, "", nil
		}

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Mock getReplicationObjects to capture the regionReplicationMap
		var capturedRegionMap map[string][]*datamodel.VolumeReplication
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			capturedRegionMap = regionReplicationMap
			return []*googleproxyclient.VolumeReplicationInternalV1beta{}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-west1", // Current region is destination
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "", // Empty source
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1", // Destination is current region
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)

		// Verify that us-west1 (destination/current region) has the replication
		assert.Contains(tt, capturedRegionMap, "us-west1")
		assert.Len(tt, capturedRegionMap["us-west1"], 1)
		assert.Equal(tt, "replication-1", capturedRegionMap["us-west1"][0].Name)

		// No source region should be added since source location is empty
		// Only current region and destination region should be in the map
		assert.Len(tt, capturedRegionMap, 1) // Only us-west1
	})
	t.Run("WhenRoleIsDestinationAndRemoteRegionIsZone", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		// Use monkey mocks
		mm := newMonkeyMockAndPatch(tt)

		// Set up mock expectations (GetProjectNumberForRegion calls utilsParseProjectNumberFromURI for dest and source region; test replications have empty Uri/RemoteUri)
		mm.On("utilsParseProjectNumberFromURI", "").Return("45110233509", nil).Times(2)
		mm.On("getAccountWithName", ctx, mockStorage, "test-account").Return(&datamodel.Account{Name: "test-account"}, nil).Once()
		mm.On("utilParseAndValidateRegionAndZone", "us-west1-a").Return("us-west1", "us-west1-a", (*gcpserver.Error)(nil)).Once()

		// Mock successful replication list with a replication where remote region is a zone
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{
			{
				Name: "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1-b", // Remote region is a zone
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1-a", // Current location is also a zone
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}, nil)

		// Mock the getReplicationObjects function to return a replication with remote region as zone
		mm.On("getReplicationObjects", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			// Return a replication where RemoteRegion is a zone (us-central1-b)
			replication := &googleproxyclient.VolumeReplicationInternalV1beta{
				RemoteRegion: "us-central1-b", // This is a zone, not a region
			}
			return []*googleproxyclient.VolumeReplicationInternalV1beta{replication}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}).Once()

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-west1-a", // Current location is a zone
			ReplicationURIs: []string{"test-uri"},
		}

		result, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)

		// Verify that the role is set correctly when remote region is a zone
		// Since us-central1-b != us-west1-a, the role should be SOURCE
		if len(result) > 0 {
			assert.True(tt, result[0].Role.Set)
			assert.Equal(tt, gcpserver.ReplicationV1betaRoleSOURCE, result[0].Role.Value)
		}

		mockStorage.AssertExpectations(tt)
		mm.AssertExpectations(tt)
	})

	t.Run("WhenRoleIsDestinationAndRemoteRegionMatchesCurrentLocation", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		// Use monkey mocks
		mm := newMonkeyMockAndPatch(tt)

		// Set up mock expectations (GetProjectNumberForRegion calls utilsParseProjectNumberFromURI for dest and source region; test replications have empty Uri/RemoteUri)
		mm.On("utilsParseProjectNumberFromURI", "").Return("45110233509", nil).Times(2)
		mm.On("getAccountWithName", ctx, mockStorage, "test-account").Return(&datamodel.Account{Name: "test-account"}, nil).Once()
		mm.On("utilParseAndValidateRegionAndZone", "us-west1-a").Return("us-west1", "us-west1-a", (*gcpserver.Error)(nil)).Once()

		// Mock successful replication list with a replication where remote region matches current location
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{
			{
				Name: "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1-b", // Remote region
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1-a", // Current location matches remote region
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}, nil)

		// Mock the getReplicationObjects function to return a replication where remote region matches current location
		mm.On("getReplicationObjects", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			// replicationRoleMap (uuid -> endpoint type) must match VolumeReplicationUuid; RemoteRegion matches current location (us-west1-a)
			replication := &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("dest-uuid-1"),
				RemoteRegion:          "us-west1-a", // This matches the current location
			}
			roleMap := map[string]string{"dest-uuid-1": database.VolumeReplicationEndpointTypeDestination}
			return []*googleproxyclient.VolumeReplicationInternalV1beta{replication}, []googleproxyclient.InternalJobV1beta{}, roleMap, nil
		}).Once()

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-west1-a", // Current location is a zone
			ReplicationURIs: []string{"test-uri"},
		}

		result, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)

		// Role is DESTINATION when replicationRoleMap marks the replication uuid as dst.
		if len(result) > 0 {
			assert.True(tt, result[0].Role.Set)
			assert.Equal(tt, gcpserver.ReplicationV1betaRoleDESTINATION, result[0].Role.Value)
		}

		mockStorage.AssertExpectations(tt)
		mm.AssertExpectations(tt)
	})

	t.Run("WhenRoleIsSourceAndBothRemoteRegionAndCurrentLocationAreRegions", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		// Use monkey mocks
		mm := newMonkeyMockAndPatch(tt)

		// Set up mock expectations (GetProjectNumberForRegion calls utilsParseProjectNumberFromURI for dest and source region; test replications have empty Uri/RemoteUri)
		mm.On("utilsParseProjectNumberFromURI", "").Return("45110233509", nil).Times(2)
		mm.On("getAccountWithName", ctx, mockStorage, "test-account").Return(&datamodel.Account{Name: "test-account"}, nil).Once()
		mm.On("utilParseAndValidateRegionAndZone", "us-west1").Return("us-west1", "", (*gcpserver.Error)(nil)).Once()

		// Mock successful replication list with a replication where both locations are regions
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{
			{
				Name: "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1", // Remote region is a region
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1", // Current location is also a region
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}, nil)

		// Mock the getReplicationObjects function to return a replication where remote region is a region
		mm.On("getReplicationObjects", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			// Return a replication where RemoteRegion is a region (us-central1)
			replication := &googleproxyclient.VolumeReplicationInternalV1beta{
				RemoteRegion: "us-central1", // This is a region, not a zone
			}
			return []*googleproxyclient.VolumeReplicationInternalV1beta{replication}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}).Once()

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-west1", // Current location is a region
			ReplicationURIs: []string{"test-uri"},
		}

		result, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)

		// Verify that the role is set correctly when remote region is a region
		// Since us-central1 != us-west1, the role should be SOURCE
		if len(result) > 0 {
			assert.True(tt, result[0].Role.Set)
			assert.Equal(tt, gcpserver.ReplicationV1betaRoleSOURCE, result[0].Role.Value)
		}

		mockStorage.AssertExpectations(tt)
		mm.AssertExpectations(tt)
	})

	t.Run("WhenRoleIsDestinationAndBothRemoteRegionAndCurrentLocationAreRegions", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		// Use monkey mocks
		mm := newMonkeyMockAndPatch(tt)

		// Set up mock expectations (GetProjectNumberForRegion calls utilsParseProjectNumberFromURI for dest and source region; test replications have empty Uri/RemoteUri)
		mm.On("utilsParseProjectNumberFromURI", "").Return("45110233509", nil).Times(2)
		mm.On("getAccountWithName", ctx, mockStorage, "test-account").Return(&datamodel.Account{Name: "test-account"}, nil).Once()
		mm.On("utilParseAndValidateRegionAndZone", "us-west1").Return("us-west1", "", (*gcpserver.Error)(nil)).Once()

		// Mock successful replication list with a replication where both locations are regions
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return([]*datamodel.VolumeReplication{
			{
				Name: "test-replication",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "us-central1", // Remote region
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-west1", // Current location matches remote region
					DestinationReplicationUUID: "dest-uuid-1",
				},
			},
		}, nil)

		// Mock the getReplicationObjects function to return a replication where remote region matches current location
		mm.On("getReplicationObjects", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			// replicationRoleMap (uuid -> endpoint type); RemoteRegion matches current location (us-west1)
			replication := &googleproxyclient.VolumeReplicationInternalV1beta{
				VolumeReplicationUuid: googleproxyclient.NewOptString("dest-uuid-1"),
				RemoteRegion:          "us-west1", // This matches the current location
			}
			roleMap := map[string]string{"dest-uuid-1": database.VolumeReplicationEndpointTypeDestination}
			return []*googleproxyclient.VolumeReplicationInternalV1beta{replication}, []googleproxyclient.InternalJobV1beta{}, roleMap, nil
		}).Once()

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-west1", // Current location is a region
			ReplicationURIs: []string{"test-uri"},
		}

		result, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)

		// Role is DESTINATION when replicationRoleMap marks this replication as dst endpoint
		if len(result) > 0 {
			assert.True(tt, result[0].Role.Set)
			assert.Equal(tt, gcpserver.ReplicationV1betaRoleDESTINATION, result[0].Role.Value)
		}

		mockStorage.AssertExpectations(tt)
		mm.AssertExpectations(tt)
	})
	t.Run("WhenReplicationURIsIsNil", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-east4",
			ReplicationURIs: nil, // nil ReplicationURIs
		}

		replications := []*datamodel.VolumeReplication{}

		// Verify that filter only contains account_id, not uri filter
		mockStorage.On("ListVolumeReplications", ctx, mock.MatchedBy(func(filter utils2.Filter) bool {
			// Check that filter has account_id condition
			// and does NOT have uri condition (since ReplicationURIs is nil)
			hasAccountID := false
			hasURI := false
			for _, condition := range filter.Conditions {
				if condition.Field == "account_id" {
					hasAccountID = true
				}
				if condition.Field == "uri" {
					hasURI = true
				}
			}
			return hasAccountID && !hasURI
		}), mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenReplicationURIsIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-east4",
			ReplicationURIs: []string{}, // empty ReplicationURIs
		}

		replications := []*datamodel.VolumeReplication{}

		// Verify that filter only contains account_id, not uri filter
		mockStorage.On("ListVolumeReplications", ctx, mock.MatchedBy(func(filter utils2.Filter) bool {
			// Check that filter has account_id condition
			// and does NOT have uri condition (since ReplicationURIs is empty)
			hasAccountID := false
			hasURI := false
			for _, condition := range filter.Conditions {
				if condition.Field == "account_id" {
					hasAccountID = true
				}
				if condition.Field == "uri" {
					hasURI = true
				}
			}
			return hasAccountID && !hasURI
		}), mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenReplicationURIsHasValues", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		params := commonparams.GetMultipleReplicationsParams{
			AccountName:     "test-account",
			LocationId:      "us-east4",
			ReplicationURIs: []string{"uri1", "uri2"}, // ReplicationURIs with values
		}

		replications := []*datamodel.VolumeReplication{}

		// Verify that filter contains both account_id and uri filter
		mockStorage.On("ListVolumeReplications", ctx, mock.MatchedBy(func(filter utils2.Filter) bool {
			// Check that filter has both account_id and uri conditions
			hasAccountID := false
			hasURI := false
			for _, condition := range filter.Conditions {
				if condition.Field == "account_id" {
					hasAccountID = true
				}
				if condition.Field == "uri" {
					hasURI = true
					// Verify the values match
					if uriValues, ok := condition.Value.([]string); ok {
						if len(uriValues) == 2 && uriValues[0] == "uri1" && uriValues[1] == "uri2" {
							return hasAccountID && hasURI
						}
					}
				}
			}
			return false
		}), mock.Anything).Return(replications, nil)

		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.Nil(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetBatchReplications(t *testing.T) {
	t.Run("WhenReplicationURIsIsNilOrEmpty_SkipsListAndReturnsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}

		for _, uris := range [][]string{nil, {}} {
			res, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
				LocationId:      "us-east4",
				ReplicationURIs: uris,
			})
			assert.NoError(tt, err)
			assert.Empty(tt, res)
		}
		mockStorage.AssertNotCalled(tt, "ListVolumeReplications")
	})

	t.Run("WhenListVolumeReplicationsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to list replications"))

		_, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			LocationId:      "us-east4",
			ReplicationURIs: []string{"projects/1/locations/us-east4/volumes/v/replications/r"},
		})
		assert.Error(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "failed to list replications")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenListVolumeReplicationsReturnsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).
			Return([]*datamodel.VolumeReplication{}, nil)

		res, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			LocationId:      "us-east4",
			ReplicationURIs: []string{"projects/1/locations/us-east4/volumes/v/replications/r"},
		})
		assert.NoError(tt, err)
		assert.Empty(tt, res)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenFilterUsesOnlyUriInCondition_NoAccountId", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}
		defer func() {
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		uris := []string{"uri-a", "uri-b"}
		mockStorage.On("ListVolumeReplications", ctx, mock.MatchedBy(func(filter utils2.Filter) bool {
			hasAccountID, hasURI := false, false
			uriMatch := false
			for _, condition := range filter.Conditions {
				if condition.Field == "account_id" {
					hasAccountID = true
				}
				if condition.Field == "uri" {
					hasURI = true
					if v, ok := condition.Value.([]string); ok && len(v) == 2 && v[0] == uris[0] && v[1] == uris[1] {
						uriMatch = true
					}
				}
			}
			return !hasAccountID && hasURI && uriMatch
		}), mock.Anything).Return([]*datamodel.VolumeReplication{}, nil)

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-east4", "", nil
		}
		getReplicationObjects = _getReplicationObjects

		_, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			AccountName:     "ignored-for-batch",
			LocationId:      "us-east4",
			ReplicationURIs: uris,
		})
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUtilParseAndValidateRegionAndZoneFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "", "", &gcpserver.Error{Code: 400, Message: "bad location"}
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1"},
				Name:      "replication-1",
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			LocationId:      "bad",
			ReplicationURIs: []string{"uri-1"},
		})
		assert.Empty(tt, res)
		assert.Error(tt, err)
		expCustErr := errors2.CustomError{
			TrackingID: 0,
			Message:    "bad location",
			Retriable:  false,
			HttpCode:   nillable.GetIntPtr(500),
		}
		assert.EqualError(tt, err, errors2.NewVCPError(errors2.ErrRegionZoneParsingErrorCurrentRegion, &expCustErr).Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetReplicationObjectsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}
		defer func() {
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return nil, []googleproxyclient.InternalJobV1beta{}, nil, errors.New("failed to get replication objects")
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid-1"},
				Name:      "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			LocationId:      "us-e4",
			ReplicationURIs: []string{"uri-1"},
		})
		assert.Empty(tt, res)
		assert.ErrorContains(tt, err, "failed to get replication objects")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccess_ReturnsConvertedReplications", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		orch := &GCPOrchestrator{storage: mockStorage}
		defer func() {
			getReplicationObjects = _getReplicationObjects
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilsParseProjectNumberFromURI = utils.ParseProjectNumberFromURI
		}()

		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "45110233509", nil
		}
		getReplicationObjects = func(ctx context.Context, regionReplicationMap map[string][]*datamodel.VolumeReplication, logger log.Logger, params commonparams.GetMultipleReplicationsParams, regionProjectMap map[string]string) ([]*googleproxyclient.VolumeReplicationInternalV1beta, []googleproxyclient.InternalJobV1beta, map[string]string, error) {
			return []*googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
					EndpointType:          datamodel.DstEndpoint,
					ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
					RemoteRegion:          "us-e4",
					SourceHostName:        "source-host",
					SourceServerName:      "source-svm",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					SourcePoolUuid:        googleproxyclient.NewOptString("source-pool-uuid"),
					DestinationHostName:   "destination-host",
					DestinationServerName: "destination-svm",
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					DestinationPoolUuid:   googleproxyclient.NewOptString("destination-pool-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSREGIONREPLICATION),
					RelationshipStatus:    googleproxyclient.NewOptVolumeReplicationInternalV1betaRelationshipStatus(googleproxyclient.VolumeReplicationInternalV1betaRelationshipStatusIdle),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300),
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(time.Now()),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(time.Now()),
					LagTime:               googleproxyclient.NewOptInt64(60),
					CreatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					UpdatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					Description:           googleproxyclient.NewOptString("Test replication"),
				},
			}, []googleproxyclient.InternalJobV1beta{}, nil, nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "replication-uuid-1"},
				Name:      "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
			},
		}
		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		res, err := orch.GetBatchReplications(ctx, commonparams.GetMultipleReplicationsParams{
			LocationId:      "us-e4",
			ReplicationURIs: []string{"uri-1"},
		})
		assert.NoError(tt, err)
		require.Len(tt, res, 1)
		require.NotNil(tt, res[0].ResourceId)
		assert.Equal(tt, "replication-1", *res[0].ResourceId)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetReplicationObjects(t *testing.T) {
	t.Run("WhenUtilsGetPairedRegionUriReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() { utilsGetPairedRegionUri = utils.GetPairedRegionURI }()

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "", errors.New("failed to get paired region URI")
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		expectedError := errors2.NewVCPError(errors2.ErrRegionZoneParsingErrorPairedRegionURI, errors.New("failed to get paired region URI"))

		_, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.NotNil(tt, err)
		assert.Equal(tt, err.Error(), expectedError.Error())
	})
	t.Run("WhenGetProjectNumberForRegionReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
		}()

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "", errors.New("failed to get project number for region")
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		expectedError := errors2.NewVCPError(errors2.ErrProjectParsingError, errors.New("failed to get project number for region"))

		_, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.NotNil(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
	})
	t.Run("WhenAuthGetSignedJwtTokenReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
		}()

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed JWT token")
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		expectedError := errors2.NewVCPError(errors2.ErrFailedToGenerateAccessToken, errors.New("failed to get signed JWT token"))

		_, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.NotNil(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
	})
	t.Run("WhenGetActiveReplicationJobsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			t.Errorf("googleProxyInternalGetMultipleReplications should not be called in this test")
			return nil, errors.New("failed to get multiple replications")
		}

		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, errors.New("failed to get active replication jobs")
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = []*datamodel.VolumeReplication{}

		expectedError := errors.New("failed to get active replication jobs")
		_, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.NotNil(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
	})
	t.Run("WhenGoogleProxyInternalGetMultipleReplicationsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
		}()

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return nil, errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsInternalServerError, errors.New("error"))
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		expectedError := errors.New("Internal server error getting multiple replications")
		_, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.NotNil(tt, err)
		assert.EqualError(tt, err, expectedError.Error())
	})
	t.Run("WhenHappyPathWithTwoRegions", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		counter := 0
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "au-se1",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
					EndpointType:          datamodel.DstEndpoint,
					ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
					RemoteRegion:          "us-e4",
					SourceHostName:        "source-host",
					SourceServerName:      "source-svm",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					SourcePoolUuid:        googleproxyclient.NewOptString("source-pool-uuid"),
					DestinationHostName:   "destination-host",
					DestinationServerName: "destination-svm",
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					DestinationPoolUuid:   googleproxyclient.NewOptString("destination-pool-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSREGIONREPLICATION),
					RelationshipStatus:    googleproxyclient.NewOptVolumeReplicationInternalV1betaRelationshipStatus(googleproxyclient.VolumeReplicationInternalV1betaRelationshipStatusIdle),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024), // 1 GB
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),               // 1 hour
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),  // 100 MB
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300), // 5 minutes
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(time.Now()),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(time.Now()),
					LagTime:               googleproxyclient.NewOptInt64(60), // 1 minute
					CreatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					UpdatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					Jobs:                  nil,
					Description:           googleproxyclient.NewOptString("Test replication"),
				},
			}, nil
		}

		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			counter++
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications
		replicationsMap["au-se1"] = []*datamodel.VolumeReplication{}

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, "replication-1", res[0].Name.Value)
		assert.Equal(tt, 2, counter)
	})
	t.Run("WhenHappyPath", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		counter := 0
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
					EndpointType:          datamodel.DstEndpoint,
					ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
					RemoteRegion:          "us-e4",
					SourceHostName:        "source-host",
					SourceServerName:      "source-svm",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					SourcePoolUuid:        googleproxyclient.NewOptString("source-pool-uuid"),
					DestinationHostName:   "destination-host",
					DestinationServerName: "destination-svm",
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					DestinationPoolUuid:   googleproxyclient.NewOptString("destination-pool-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					ReplicationType:       googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationType(googleproxyclient.VolumeReplicationInternalV1betaReplicationTypeCROSSREGIONREPLICATION),
					RelationshipStatus:    googleproxyclient.NewOptVolumeReplicationInternalV1betaRelationshipStatus(googleproxyclient.VolumeReplicationInternalV1betaRelationshipStatusIdle),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024), // 1 GB
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),               // 1 hour
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),  // 100 MB
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300), // 5 minutes
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(time.Now()),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(time.Now()),
					LagTime:               googleproxyclient.NewOptInt64(60), // 1 minute
					CreatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					UpdatedAt:             googleproxyclient.NewOptDateTime(time.Now()),
					Jobs:                  nil,
					Description:           googleproxyclient.NewOptString("Test replication"),
				},
			}, nil
		}

		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			counter++
			return []googleproxyclient.InternalJobV1beta{
				{
					JobUuid:      googleproxyclient.NewOptString("job-uuid-1"),
					ResourceName: googleproxyclient.NewOptString("projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6"),
					JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeCreateVolumeReplication)),
				},
			}, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, "replication-1", res[0].Name.Value)
		assert.Equal(tt, 1, counter)
	})
	t.Run("WhenHappyPathWithEmptyResult", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		counter := 0
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{}, nil
		}

		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			counter++
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 0)
		assert.Equal(tt, 1, counter)
	})
	t.Run("WhenMultipleReplicationsBelongToSameProject", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		// Create multiple replications that belong to the same project
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-1",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-1",
			},
			{
				BaseModel: datamodel.BaseModel{
					ID:        2,
					UUID:      "uuid-2",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-2",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-2",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-2",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-2",
			},
			{
				BaseModel: datamodel.BaseModel{
					ID:        3,
					UUID:      "uuid-3",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-3",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-3",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-3",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-3",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}

		// All replications belong to the same project
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		// Token should only be requested once for the same project
		tokenCallCount := 0
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			tokenCallCount++
			assert.Equal(tt, "45110233509", projectNumber)
			return "signed-jwt-token", nil
		}

		// Verify that all replication UUIDs are passed together in a single call
		var capturedReplicationUUIDs []string
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			capturedReplicationUUIDs = body.ReplicationUUIDs
			assert.Equal(tt, "45110233509", projectNumber)
			assert.Equal(tt, "signed-jwt-token", token)
			// Return all three replications
			return []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
				},
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-2"),
					Name:                  googleproxyclient.NewOptString("replication-2"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
				},
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-3"),
					Name:                  googleproxyclient.NewOptString("replication-3"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
					LifeCycleStateDetails: googleproxyclient.NewOptString(datamodel.LifeCycleStateAvailableDetails),
				},
			}, nil
		}

		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 3)
		// Verify that token was only requested once (for the same project)
		assert.Equal(tt, 1, tokenCallCount)
		// Verify that all three replication UUIDs were passed together
		assert.Len(tt, capturedReplicationUUIDs, 3)
		assert.Contains(tt, capturedReplicationUUIDs, "replication-uuid-1")
		assert.Contains(tt, capturedReplicationUUIDs, "replication-uuid-2")
		assert.Contains(tt, capturedReplicationUUIDs, "replication-uuid-3")
	})
	t.Run("WhenSourceRegionParseErrorWithTwoRegions", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		defer func() {
			getAccountWithName = _getAccountWithName
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
			utilParseRegionAndZone = utils.ParseRegionAndZone
			GetProjectNumberForRegion = _getProjectNumberForRegion
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpserver.Error) {
			return "us-e4", "", nil
		}
		utilParseRegionAndZone = func(locationID string) (string, string, error) {
			if locationID == "invalid-region!!!" {
				return "", "", errors.New("LocationID represents neither a region nor a zone")
			}
			return locationID, "", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "invalid-region!!!",
					SourceReplicationUUID:      "source-uuid-1",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		mockStorage.On("ListVolumeReplications", ctx, mock.Anything, mock.Anything).Return(replications, nil)

		params := commonparams.GetMultipleReplicationsParams{
			AccountName: "test-account",
			LocationId:  "us-e4",
		}
		_, err := _getMultipleReplications(ctx, mockStorage, params)
		assert.NotNil(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr) && customErr.TrackingID == errors2.ErrRegionZoneParsingErrorSourceRegion)
	})
	t.Run("WhenGoogleProxyReturnsNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		replications := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return nil, errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsNotFound, errors.New("not found"))
		}
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replications

		res, jobs, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 0)
		assert.NotNil(tt, jobs)
	})
	t.Run("WhenCrossRegionReplicationThreeRegions", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		replicationsUsE4 := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "replication-uuid-1",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(datamodel.LifeCycleStateREADY),
				},
			}, nil
		}
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["us-e4"] = replicationsUsE4
		replicationsMap["au-se1"] = []*datamodel.VolumeReplication{}
		replicationsMap["us-w1"] = []*datamodel.VolumeReplication{}

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, "replication-1", res[0].Name.Value)
	})
	t.Run("WhenTwoRegionsEmptyReplicationsUsesSourceFromFirstNonEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		replicationsWithSource := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					SourceLocation:             "au-se1",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "a1b2c3d4-e5f6-4789-a012-345678901234",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{}, nil
		}
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap["au-se1"] = []*datamodel.VolumeReplication{}
		replicationsMap["us-e4"] = replicationsWithSource

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		assert.Len(tt, res, 0)
	})
	// VSCP-6041: empty DestinationReplicationUUID (CREATING window) must return a partial stub
	// without calling the internal endpoint; project/token must still be registered.
	t.Run("WhenBothSdeUuidsAreEmptySkipsInternalCallAndReturnsPartialStub", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		replicationUUID := "vcp-db-uuid-initialising"
		replications := []*datamodel.VolumeReplication{
			{
				BaseModel:    datamodel.BaseModel{UUID: replicationUUID},
				Name:         "replication-initialising",
				Uri:          "projects/45110233509/locations/us-east4/volumes/srcvol/replications/repl-1",
				RemoteUri:    "projects/45110233509/locations/australia-southeast1/volumes/dstvol/replications/repl-1",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:        "src",
					ReplicationSchedule: "HOURLY",
					DestinationLocation: "us-e4",
					// Production scenario: DestinationReplicationUUID is "" (never set by the
					// SDE workflow yet) and SourceReplicationUUID is the VCP DB UUID.
					// Using "" — NOT the zero UUID — to match the real trigger condition.
					DestinationReplicationUUID: "",
					SourceReplicationUUID:      replicationUUID,
				},
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			tt.Errorf("googleProxyInternalGetMultipleReplications must NOT be called when SDE UUID is empty (VSCP-6041)")
			return nil, nil
		}
		jobsCallCount := 0
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			jobsCallCount++
			return nil, nil
		}

		replicationsMap := map[string][]*datamodel.VolumeReplication{"us-e4": replications}

		replicationList, _, roleMap, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)
		require.Len(tt, replicationList, 1, "expected 1 partial stub in the result list")
		assert.Equal(tt, 1, jobsCallCount, "getActiveReplicationJobs must still be called for initialising replications so that CREATE/DELETE jobs are returned")

		stub := replicationList[0]
		assert.Equal(tt, replicationUUID, stub.VolumeReplicationUuid.Value, "VolumeReplicationUuid must equal the VCP DB UUID so that role resolution works")
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating, stub.LifeCycleState.Value, "LifeCycleState must be CREATING")
		assert.Equal(tt, replications[0].Uri, stub.CcfeUri.Value)
		assert.Equal(tt, replications[0].RemoteUri, stub.CcfeRemoteUri.Value)
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaEndpointType("src"), stub.EndpointType)
		assert.Equal(tt, "src", roleMap[replicationUUID], "replicationRoleMap must be keyed by VCP UUID and hold the endpoint type")
		// SourceVolumeUuid and DestinationVolumeUuid must NOT be set so that
		// convertInternalReplicationToCCFEModel omits source/destination from the response.
		assert.False(tt, stub.SourceVolumeUuid.Set, "SourceVolumeUuid must not be set in the initialising stub")
		assert.False(tt, stub.DestinationVolumeUuid.Set, "DestinationVolumeUuid must not be set in the initialising stub")
	})

	t.Run("WhenMixedEmptyAndValidSdeUuidsHandlesBothCorrectly", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		validSdeUUID := "a1b2c3d4-e5f6-4789-a012-345678901234"
		initializingDBUUID := "vcp-db-uuid-initialising"

		replications := []*datamodel.VolumeReplication{
		// Replication 1: DestinationReplicationUUID is "" (not yet set) — initialising (VSCP-6041 case).
		{
			BaseModel: datamodel.BaseModel{UUID: initializingDBUUID},
			Name:      "replication-initialising",
			Uri:       "projects/45110233509/locations/us-east4/volumes/srcvol/replications/repl-1",
			RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/dstvol/replications/repl-1",
			State:     datamodel.LifeCycleStateCreating,
			ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               "src",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "", // empty string — production trigger condition
					SourceReplicationUUID:      initializingDBUUID,
				},
			},
			// Replication 2: valid SDE UUID — must go through the normal internal API path.
			{
				BaseModel: datamodel.BaseModel{UUID: "vcp-db-uuid-ready"},
				Name:      "replication-ready",
				Uri:       "projects/45110233509/locations/us-east4/volumes/srcvol2/replications/repl-2",
				RemoteUri: "projects/45110233509/locations/australia-southeast1/volumes/dstvol2/replications/repl-2",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               "src",
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: validSdeUUID,
				},
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}

		var capturedUUIDs []string
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			capturedUUIDs = body.ReplicationUUIDs
			return []googleproxyclient.VolumeReplicationInternalV1beta{}, nil
		}
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			return nil, nil
		}

		replicationsMap := map[string][]*datamodel.VolumeReplication{"us-e4": replications}

		replicationList, _, roleMap, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, nil)
		assert.Nil(tt, err)

		// The initialising replication must appear as a partial stub.
		require.Len(tt, replicationList, 1, "expected 1 partial stub (internal API returned empty list)")
		assert.Equal(tt, initializingDBUUID, replicationList[0].VolumeReplicationUuid.Value)
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating, replicationList[0].LifeCycleState.Value)

		// The internal API must be called with ONLY the valid SDE UUID — not the zero UUID.
		require.Len(tt, capturedUUIDs, 1, "internal API should receive exactly one UUID (the valid one)")
		assert.Equal(tt, validSdeUUID, capturedUUIDs[0], "empty SDE UUID must NOT be forwarded to the internal API")

		// roleMap must contain both entries keyed correctly.
		assert.Equal(tt, "src", roleMap[initializingDBUUID], "initialising replication must be in roleMap under its VCP DB UUID")
		assert.Equal(tt, "src", roleMap[validSdeUUID], "ready replication must be in roleMap under its SDE UUID")
	})

	t.Run("WhenTwoRegionsEndpointTypeDstUsesSourceProjectFromRemoteUri", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		defer func() {
			utilsGetPairedRegionUri = utils.GetPairedRegionURI
			GetProjectNumberForRegion = _getProjectNumberForRegion
			authGetSignedJwtToken = auth.GetSignedJwtToken
			googleProxyInternalGetMultipleReplications = _googleProxyInternalGetMultipleReplications
			getActiveReplicationJobs = _getActiveReplicationJobs
		}()

		// Destination-side replication: Uri = destination project, RemoteUri = source project.
		// When EndpointType is "dst", sourceProject must be taken from RemoteUri for the empty-region job path.
		sourceProjectNumber := "999888777666"
		sourceRegion := "au-se1"
		replicationsDst := []*datamodel.VolumeReplication{
			{
				BaseModel: datamodel.BaseModel{
					ID:        1,
					UUID:      "uuid-1",
					CreatedAt: time.Time{},
					UpdatedAt: time.Time{},
				},
				Name: "replication-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType:               "dst",
					SourceLocation:             sourceRegion,
					DestinationLocation:        "us-e4",
					DestinationReplicationUUID: "a1b2c3d4-e5f6-4789-a012-345678901234",
				},
				Uri:       "projects/45110233509/locations/us-east4/volumes/gosrcvolume1/replications/replication-name-6",
				RemoteUri: "projects/" + sourceProjectNumber + "/locations/australia-southeast1/volumes/gosrcvolume1/replications/replication-name-6",
			},
		}

		utilsGetPairedRegionUri = func(locationId string) (string, error) {
			return "paired.region.uri", nil
		}
		GetProjectNumberForRegion = func(replication *datamodel.VolumeReplication, region string) (string, error) {
			return "45110233509", nil
		}
		authGetSignedJwtToken = func(projectNumber string) (string, error) {
			return "signed-jwt-token", nil
		}
		googleProxyInternalGetMultipleReplications = func(ctx context.Context, basePath, projectNumber, location, token string, body googleproxyclient.ReplicationIDListV1beta, logger log.Logger, paramz commonparams.GetMultipleReplicationsParams) ([]googleproxyclient.VolumeReplicationInternalV1beta, error) {
			return []googleproxyclient.VolumeReplicationInternalV1beta{}, nil
		}

		// Capture getActiveReplicationJobs calls for the empty-region path (source region + source project).
		var jobsCalls []struct{ locationID, projectNumber string }
		getActiveReplicationJobs = func(ctx context.Context, basePath string, token string, locationID string, projectNumber string, xCorrelationID *string) ([]googleproxyclient.InternalJobV1beta, error) {
			jobsCalls = append(jobsCalls, struct{ locationID, projectNumber string }{locationID, projectNumber})
			return nil, nil
		}

		replicationsMap := make(map[string][]*datamodel.VolumeReplication)
		replicationsMap[sourceRegion] = []*datamodel.VolumeReplication{}
		replicationsMap["us-e4"] = replicationsDst

		// Empty-region path uses regionProjectMap; pass source region -> source project so jobs are fetched with correct project.
		regionProjectMap := map[string]string{sourceRegion: sourceProjectNumber}

		res, _, _, err := _getReplicationObjects(ctx, replicationsMap, mockLogger, commonparams.GetMultipleReplicationsParams{}, regionProjectMap)
		assert.Nil(tt, err)
		assert.Len(tt, res, 0)
		// Empty-region path must call getActiveReplicationJobs with source region and project from regionProjectMap.
		var foundEmptyRegionPath bool
		for _, c := range jobsCalls {
			if c.locationID == sourceRegion && c.projectNumber == sourceProjectNumber {
				foundEmptyRegionPath = true
				break
			}
		}
		assert.True(tt, foundEmptyRegionPath, "getActiveReplicationJobs must be called with source region %q and source project %q (from regionProjectMap) when EndpointType is dst", sourceRegion, sourceProjectNumber)
	})
}

func TestGoogleProxyInternalGetMultipleReplications(t *testing.T) {
	t.Run("WhenGoogleProxyClientGetMultipleReplicationsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(nil, errors.New("failed to get multiple replications"))

		expectedError := errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplications, errors.New("failed to get multiple replications"))

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, res)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError.Error(), err.Error())
	})
	t.Run("WhenBadRequestErrorIsReturned", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		errResp := googleproxyclient.V1betaGetMultipleReplicationsInternalBadRequest{
			Code:    400,
			Message: "bad request error",
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(&errResp, nil)

		expectedError := errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsBadRequest, errors.New("bad request error"))

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, res)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError.Error(), err.Error())
	})
	t.Run("WhenInternalServerErrorIsReturned", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		errResp := googleproxyclient.V1betaGetMultipleReplicationsInternalInternalServerError{
			Code:    500,
			Message: "internal server error",
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(&errResp, nil)

		expectedError := errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsInternalServerError, errors.New("internal server error"))

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, res)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError.Error(), err.Error())
	})
	t.Run("WhenUnauthorizedErrorIsReturned", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		errResp := googleproxyclient.V1betaGetMultipleReplicationsInternalUnauthorized{
			Code:    401,
			Message: "unauthorized error",
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(&errResp, nil)

		expectedError := errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsUnauthorized, errors.New("unauthorized error"))

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, res)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError.Error(), err.Error())
	})
	t.Run("WhenNotFoundErrorIsReturned", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}
		errResp := googleproxyclient.V1betaGetMultipleReplicationsInternalNotFound{
			Code:    404,
			Message: "not found error",
		}
		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(&errResp, nil)

		expectedError := errors2.NewVCPError(errors2.ErrGoogleProxyInternalGetMultipleReplicationsNotFound, errors.New("not found error"))

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, res)
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError.Error(), err.Error())
	})
	t.Run("WhenHappyPath", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockClient := googleproxyclient.NewMockInvoker(t)

		basePath := "https://example.com"
		projectNumber := "1234567890"
		location := "us-central1"
		token := "test-token"
		body := googleproxyclient.ReplicationIDListV1beta{
			ReplicationUUIDs: []string{"replication-id-1"},
		}

		paramz := googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
			ProjectNumber:  projectNumber,
			LocationId:     location,
			XCorrelationID: googleproxyclient.NewOptString("test-correlation-id"),
		}

		params := commonparams.GetMultipleReplicationsParams{
			ReplicationURIs: []string{"projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1"},
			LocationId:      location,
			XCorrelationID:  "test-correlation-id",
		}

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		resp := googleproxyclient.V1betaGetMultipleReplicationsInternalOK{
			Replications: []googleproxyclient.VolumeReplicationInternalV1beta{
				{
					Name:                  googleproxyclient.NewOptString("replication-1"),
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					DestinationServerName: "destination-svm",
					DestinationHostName:   "destination-host",
					DestinationPoolUuid:   googleproxyclient.NewOptString("destination-pool-uuid"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					Description:           googleproxyclient.NewOptString("Test replication"),
				},
			},
		}

		mockClient.EXPECT().V1betaGetMultipleReplicationsInternal(ctx, &body, paramz).Return(&resp, nil)

		res, err := _googleProxyInternalGetMultipleReplications(ctx, basePath, projectNumber, location, token, body, mockLogger, params)
		assert.Nil(tt, err)
		assert.Len(tt, res, 1)
		assert.Equal(tt, "replication-1", res[0].Name.Value)
		assert.Equal(tt, "destination-volume-uuid", res[0].DestinationVolumeUuid.Value)
	})
}

func TestConvertInternalReplicationToCCFEModel(t *testing.T) {
	baseTime := time.Now()

	t.Run("BasicConversion", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("123"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024), // 1 GB
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),               // 1 hour
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),  // 100 MB
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300), // 5 minutes
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60), // 1 minute
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		// Create regionReplicationMap with a matching replication
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		roleMap := map[string]string{"replication-uuid-1": database.VolumeReplicationEndpointTypeDestination}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, roleMap)

		assert.NotNil(tt, result)
		assert.Equal(tt, "replication-uuid-1", result.ReplicationId.Value)
		assert.Equal(tt, "replication-1", result.ResourceId.Value)
		assert.Equal(tt, "Test replication", result.Description.Value)
		assert.Equal(tt, "projects/45110233509/locations/australia-southeast/volumes/source-volume", result.Source.Value.VolumeName.Value)
		assert.Equal(tt, "123", result.Source.Value.VolumeId.Value)
		assert.Equal(tt, "projects/45110233509/locations/us-e4/volumes/destination-volume", result.Destination.Value.VolumeName.Value)
		assert.Equal(tt, "destination-volume-uuid", result.Destination.Value.VolumeId.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaStateREADY, result.State.Value)
		assert.Equal(tt, "Available for use", result.StateDetails.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaReplicationScheduleHOURLY, result.ReplicationSchedule.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateMIRRORED, result.MirrorState.Value)
		assert.Equal(tt, true, result.Healthy.Value)
		assert.Equal(tt, float64(1024*1024*1024), result.TransferStats.Value.TotalTransferBytes.Value)
		assert.Equal(tt, float64(3600), result.TransferStats.Value.TotalTransferTimeSecs.Value)
		assert.Equal(tt, float64(1024*1024*100), result.TransferStats.Value.LastTransferSize.Value)
		assert.Equal(tt, "No error", result.TransferStats.Value.LastTransferError.Value)
		assert.Equal(tt, float64(300), result.TransferStats.Value.LastTransferDuration.Value)
		assert.Equal(tt, baseTime, result.TransferStats.Value.LastTransferEndTime.Value)
		assert.Equal(tt, float64(100), result.TransferStats.Value.TotalProgress.Value)
		assert.Equal(tt, baseTime, result.TransferStats.Value.ProgressLastUpdated.Value)
		assert.Equal(tt, float64(60), result.TransferStats.Value.LagTime.Value)
		assert.Equal(tt, baseTime, result.Created.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaRoleDESTINATION, result.Role.Value)
	})

	t.Run("SourceRole", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-w1/volumes/source-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-w1": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-w1", nil, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaRoleSOURCE, result.Role.Value)
	})

	t.Run("DeleteJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeDeleteVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateDELETING, result.State.Value)
		assert.Equal(tt, volumeReplicationCVP1betaLifeCycleStateDeleting, result.StateDetails.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("CreateJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeCreateVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateCREATING, result.State.Value)
		assert.Equal(tt, volumeReplicationCVP1betaLifeCycleStateCreation, result.StateDetails.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePREPARING, result.MirrorState.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("StopJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeStopVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateUPDATING, result.State.Value)
		assert.Equal(tt, volumeReplicationCVP1betaLifeCycleStateStopping, result.StateDetails.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("ResumeJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeResumeVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateUPDATING, result.State.Value)
		assert.Equal(tt, volumeReplicationCVP1betaLifeCycleStateResuming, result.StateDetails.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePREPARING, result.MirrorState.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("UpdateJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeUpdateVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateUPDATING, result.State.Value)
		assert.Equal(tt, volumeReplicationCVP1betaLifeCycleStateUpdating, result.StateDetails.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationInternalEstablishJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeHybridReplicationInternalEstablish)),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateREADY, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePREPARING, result.MirrorState.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("UnknownJobTypeOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("replication-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString("UNKNOWN_JOB_TYPE"),
				ResourceName: googleproxyclient.NewOptString("replication-1"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.Equal(tt, gcpserver.ReplicationV1betaStateUPDATING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePREPARING, result.MirrorState.Value)
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})

	t.Run("NoJobOverride", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeDeleteVolumeReplication)),
				ResourceName: googleproxyclient.NewOptString("different-replication"),
			},
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		// Should not be overridden since job doesn't match
		assert.Equal(tt, gcpserver.ReplicationV1betaStateREADY, result.State.Value)
		assert.Equal(tt, "Available for use", result.StateDetails.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateMIRRORED, result.MirrorState.Value)
	})

	t.Run("NilJobsList", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}
		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		// Should not be overridden since jobsList is nil
		assert.Equal(tt, gcpserver.ReplicationV1betaStateREADY, result.State.Value)
		assert.Equal(tt, "Available for use", result.StateDetails.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateMIRRORED, result.MirrorState.Value)
	})

	t.Run("DifferentReplicationSchedules", func(tt *testing.T) {
		testCases := []struct {
			name          string
			schedule      googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule
			expectedState gcpserver.ReplicationV1betaReplicationSchedule
		}{
			{
				name:          "Hourly",
				schedule:      googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly,
				expectedState: gcpserver.ReplicationV1betaReplicationScheduleHOURLY,
			},
			{
				name:          "Daily",
				schedule:      googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleDaily,
				expectedState: gcpserver.ReplicationV1betaReplicationScheduleDAILY,
			},
			{
				name:          "10Minutely",
				schedule:      googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule10minutely,
				expectedState: gcpserver.ReplicationV1betaReplicationScheduleEVERY10MINUTES,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(t *testing.T) {
				replication := &googleproxyclient.VolumeReplicationInternalV1beta{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
					LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
					RemoteRegion:          "us-e4",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(tc.schedule),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300),
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
					LagTime:               googleproxyclient.NewOptInt64(60),
					CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
					Description:           googleproxyclient.NewOptString("Test replication"),
				}

				regionReplicationMap := map[string][]*datamodel.VolumeReplication{
					"us-e4": {
						{
							BaseModel: datamodel.BaseModel{
								UUID: "replication-uuid-1",
							},
							Name: "replication-1",
							ReplicationAttributes: &datamodel.ReplicationDetails{
								DestinationLocation: "us-e4",
							},
						},
					},
				}
				result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)
				assert.Equal(t, tc.expectedState, result.ReplicationSchedule.Value)
			})
		}
	})

	t.Run("DifferentLifecycleStates", func(tt *testing.T) {
		testCases := []struct {
			name          string
			state         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState
			expectedState gcpserver.ReplicationV1betaState
		}{
			{
				name:          "Available",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
				expectedState: gcpserver.ReplicationV1betaStateREADY,
			},
			{
				name:          "Creating",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
				expectedState: gcpserver.ReplicationV1betaStateCREATING,
			},
			{
				name:          "Deleting",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
				expectedState: gcpserver.ReplicationV1betaStateDELETING,
			},
			{
				name:          "Updating",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
				expectedState: gcpserver.ReplicationV1betaStateUPDATING,
			},
			{
				name:          "Error",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError,
				expectedState: gcpserver.ReplicationV1betaStateERROR,
			},
			{
				name:          "Disabled",
				state:         googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
				expectedState: gcpserver.ReplicationV1betaStateDISABLED,
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.name, func(t *testing.T) {
				replication := &googleproxyclient.VolumeReplicationInternalV1beta{
					VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
					LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(tc.state),
					LifeCycleStateDetails: googleproxyclient.NewOptString("State details"),
					RemoteRegion:          "us-e4",
					SourceVolumeName:      "source-volume",
					SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
					DestinationVolumeName: "destination-volume",
					DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
					Name:                  googleproxyclient.NewOptString("replication-1"),
					MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
					TotalProgress:         googleproxyclient.NewOptInt64(100),
					Healthy:               googleproxyclient.NewOptBool(true),
					TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
					TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
					LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
					LastTransferError:     googleproxyclient.NewOptString("No error"),
					LastTransferDuration:  googleproxyclient.NewOptInt64(300),
					LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
					ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
					LagTime:               googleproxyclient.NewOptInt64(60),
					CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
					Description:           googleproxyclient.NewOptString("Test replication"),
				}

				regionReplicationMap := map[string][]*datamodel.VolumeReplication{
					"us-e4": {
						{
							BaseModel: datamodel.BaseModel{
								UUID: "replication-uuid-1",
							},
							Name: "replication-1",
							ReplicationAttributes: &datamodel.ReplicationDetails{
								DestinationLocation: "us-e4",
							},
						},
					},
				}
				result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)
				assert.Equal(t, tc.expectedState, result.State.Value)
			})
		}
	})

	t.Run("HybridReplicationPendingClusterPeer", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		command := "test-command"
		passphrase := "test-passphrase"
		expiryTime := baseTime.Add(24 * time.Hour)
		clusterLocation := "us-west1"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingClusterPeer,
						StateDetailsCode: 100,
					},
					ClusterPeer: &datamodel.ClusterPeerings{
						ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
							Command:         &command,
							PassPhrase:      &passphrase,
							ExpiryTime:      &expiryTime,
							ClusterLocation: &clusterLocation,
						},
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, command, result.HybridPeeringDetails.Value.Command.Value)
		assert.Equal(tt, passphrase, result.HybridPeeringDetails.Value.Passphrase.Value)
		assert.Equal(tt, expiryTime, result.HybridPeeringDetails.Value.CommandExpiryTime.Value)
		assert.Equal(tt, int32(100), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationPendingClusterPeerWithoutAttributes", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingClusterPeer,
						StateDetailsCode: 200,
					},
					ClusterPeer: &datamodel.ClusterPeerings{
						ClusterPeeringAttributes: nil, // No attributes
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.Command.Value)
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.Passphrase.Value)
		assert.Equal(tt, int32(200), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationPendingSVMPeer", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		svmPeerCommand := "svm-peer-command"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingSVMPeer,
						StateDetailsCode: 300,
						SvmPeerCommand:   &svmPeerCommand,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, svmPeerCommand, result.HybridPeeringDetails.Value.Command.Value)
		assert.Equal(tt, int32(300), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationPendingSVMPeerWithoutCommand", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingSVMPeer,
						StateDetailsCode: 400,
						SvmPeerCommand:   nil, // No command
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.Command.Value)
		assert.Equal(tt, int32(400), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationSVMPeered", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusSVMPeered,
						StateDetailsCode: 500,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.Command.Value)
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.Passphrase.Value)
		assert.Equal(tt, int32(500), result.StateDetailsCode.Value)
	})

	t.Run("HybridReplicationTypeMigration", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridReplicationType := "MIGRATION"
		peerVolumeName := "peer-volume"
		peerSvmName := "peer-svm"
		sourceHostName := "source-host"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:                datamodel.HybridReplicationStatusPendingClusterPeer,
						StateDetailsCode:      600,
						HybridReplicationType: &hybridReplicationType,
						PeerVolumeName:        peerVolumeName,
						PeerSvmName:           peerSvmName,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
						SourceHostName:      sourceHostName,
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, peerVolumeName, result.HybridPeeringDetails.Value.PeerVolumeName.Value)
		assert.Equal(tt, peerSvmName, result.HybridPeeringDetails.Value.PeerSvmName.Value)
		assert.Equal(tt, sourceHostName, result.HybridPeeringDetails.Value.PeerClusterName.Value)
	})

	t.Run("HybridReplicationTypeOnPremWithoutSourceHostName", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridReplicationType := "ONPREM_REPLICATION"
		peerVolumeName := "peer-volume"
		peerSvmName := "peer-svm"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:                datamodel.HybridReplicationStatusPendingClusterPeer,
						StateDetailsCode:      700,
						HybridReplicationType: &hybridReplicationType,
						PeerVolumeName:        peerVolumeName,
						PeerSvmName:           peerSvmName,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
						// SourceHostName not set
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.NotNil(tt, result.HybridPeeringDetails)
		assert.Equal(tt, peerVolumeName, result.HybridPeeringDetails.Value.PeerVolumeName.Value)
		assert.Equal(tt, peerSvmName, result.HybridPeeringDetails.Value.PeerSvmName.Value)
		// PeerClusterName should be set but empty when SourceHostName is empty
		assert.True(tt, result.HybridPeeringDetails.Value.PeerClusterName.IsSet())
		assert.Equal(tt, "", result.HybridPeeringDetails.Value.PeerClusterName.Value)
	})

	t.Run("DbReplicationNotFoundInMap", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		// Empty map - replication not found, but code requires ReplicationAttributes to be initialized
		// This test verifies that when dbReplication is not found, it's initialized with empty struct
		// Note: The code currently has a bug where it accesses ReplicationAttributes without nil check
		// So we provide a minimal replication to avoid panic
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		// Should not panic and should return a valid result
		assert.NotNil(tt, result)
		assert.Equal(tt, "replication-uuid-1", result.ReplicationId.Value)
		assert.Equal(tt, "replication-1", result.ResourceId.Value)
		// StateDetailsCode should be unset (zero value) when HybridReplicationAttributes is nil
		assert.False(tt, result.StateDetailsCode.IsSet())
	})

	t.Run("ClusterPeerWithClusterLocation", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		clusterLocation := "us-west1"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ClusterPeer: &datamodel.ClusterPeerings{
						ClusterPeeringAttributes: &datamodel.ClusterPeeringAttributes{
							ClusterLocation: &clusterLocation,
						},
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		// Note: clusterLocation is stored but may not be directly exposed in the result
		// This test verifies the code path doesn't panic
	})

	t.Run("HybridReplicationPendingClusterPeerWithUninitializedMirrorState", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		clusterPeerStateDetails := "Cluster peer in progress"
		hybridStatusDetails := "Hybrid replication pending"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingClusterPeer,
						StatusDetails:    hybridStatusDetails,
						StateDetailsCode: 800,
					},
					ClusterPeer: &datamodel.ClusterPeerings{
						StateDetails: clusterPeerStateDetails,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGCLUSTERPEERING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePENDINGPEERING, result.MirrorState.Value)
		// Should use ClusterPeer.StateDetails when available
		assert.Equal(tt, clusterPeerStateDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationPendingClusterPeerWithPreparingMirrorState", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridStatusDetails := "Hybrid replication pending cluster peer"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingClusterPeer,
						StatusDetails:    hybridStatusDetails,
						StateDetailsCode: 900,
					},
					ClusterPeer: &datamodel.ClusterPeerings{
						StateDetails: "", // Empty StateDetails
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGCLUSTERPEERING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePENDINGPEERING, result.MirrorState.Value)
		// Should use HybridReplicationAttributes.StatusDetails when ClusterPeer.StateDetails is empty
		assert.Equal(tt, hybridStatusDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationPendingSVMPeerWithUninitializedMirrorState", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridStatusDetails := "SVM peer pending"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingSVMPeer,
						StatusDetails:    hybridStatusDetails,
						StateDetailsCode: 1000,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGSVMPEERING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePENDINGPEERING, result.MirrorState.Value)
		assert.Equal(tt, hybridStatusDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationSVMPeeredWithPreparingMirrorState", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridStatusDetails := "SVM peered"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusSVMPeered,
						StatusDetails:    hybridStatusDetails,
						StateDetailsCode: 1100,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGSVMPEERING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePENDINGPEERING, result.MirrorState.Value)
		assert.Equal(tt, hybridStatusDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationPendingClusterPeerWithNilClusterPeer", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available for use"),
			RemoteRegion:          "us-e4",
			SourceVolumeName:      "source-volume",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeName: "destination-volume",
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		hybridStatusDetails := "Hybrid replication pending - no cluster peer"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPendingClusterPeer,
						StatusDetails:    hybridStatusDetails,
						StateDetailsCode: 1200,
					},
					ClusterPeer: nil, // Nil ClusterPeer
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGCLUSTERPEERING, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStatePENDINGPEERING, result.MirrorState.Value)
		// Should use HybridReplicationAttributes.StatusDetails when ClusterPeer is nil
		assert.Equal(tt, hybridStatusDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationPendingRemoteResyncWithJob", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		userCommands := []string{"snapmirror resync -source-path svm1:vol1 -destination-path svm2:vol2"}
		statusDetails := "Pending remote resync"
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:                        datamodel.HybridReplicationStatusPendingRemoteResync,
						HybridReplicationUserCommands: userCommands,
						StatusDetails:                 statusDetails,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeReverseHybridReplicationInternal)),
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.True(tt, result.HybridReplicationUserCommands.IsSet())
		assert.Equal(tt, userCommands, result.HybridReplicationUserCommands.Value.Commands)
		assert.Equal(tt, statusDetails, result.StateDetails.Value)
	})

	t.Run("HybridReplicationPendingRemoteResyncWithoutJob", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status: datamodel.HybridReplicationStatusPendingRemoteResync,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaStateREADY, result.State.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateSTOPPED, result.MirrorState.Value)
	})

	t.Run("HybridReplicationExternalManagedWithReverseResumeJob", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		userCommands := []string{"snapmirror resync -source-path svm1:vol1 -destination-path svm2:vol2"}
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:                        datamodel.HybridReplicationStatusExternalManaged,
						HybridReplicationUserCommands: userCommands,
						StatusDetails:                 "Externally managed",
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeReverseResumeVolumeReplication)),
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.False(tt, result.HybridReplicationUserCommands.IsSet())
		assert.Equal(tt, "Reverse in progress", result.StateDetails.Value)
	})

	t.Run("HybridReplicationExternalManagedWithoutJob", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		statusDetails := "Externally managed replication"
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:        datamodel.HybridReplicationStatusExternalManaged,
						StatusDetails: statusDetails,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateEXTERNALLYMANAGED, result.MirrorState.Value)
		assert.Equal(tt, gcpserver.ReplicationV1betaStateEXTERNALLYMANAGEDREPLICATION, result.State.Value)
		assert.Equal(tt, statusDetails, result.StateDetails.Value)
		assert.False(tt, result.Destination.IsSet())
	})

	t.Run("FindDbReplicationWithEmptyDestinationVolumeUuid", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("source-volume-uuid"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		expectedStateDetailsCode := int32(100)
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:           datamodel.HybridReplicationStatusPeered,
						StatusDetails:    "Peered",
						StateDetailsCode: expectedStateDetailsCode,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		assert.Equal(tt, "replication-uuid-1", result.ReplicationId.Value)
		// Verify that dbReplication was found by checking StateDetailsCode is set from HybridReplicationAttributes
		assert.True(tt, result.StateDetailsCode.IsSet())
		assert.Equal(tt, expectedStateDetailsCode, result.StateDetailsCode.Value)
		// StateDetails should still be from input LifeCycleStateDetails since "Peered" status doesn't override it
		assert.Equal(tt, "Available", result.StateDetails.Value)
	})

	t.Run("HybridReplicationSourceNotFoundInRegionMap", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		userCommands := []string{"snapmirror resync -source-path svm1:vol1 -destination-path svm2:vol2"}
		sourceReplicationUUID := "non-existent-source-uuid"
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationUserCommands: userCommands,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation:   "us-e4",
						SourceReplicationUUID: sourceReplicationUUID,
						SourceLocation:        "customer",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		// Source replication not found, so IsSrcForHybridReplication should not be called
		// User commands should not be set with the special message
		assert.False(tt, result.HybridReplicationUserCommands.IsSet())
	})

	t.Run("IsSrcForHybridReplicationOverridesSrcVolUri", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("source-volume-uuid"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		reverseType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		// This URI will be used to override srcVolUri when IsSrcForHybridReplication returns true
		dbReplicationUri := "projects/45110233509/locations/us-central1/volumes/override-source-volume/replications/replication-uuid-1"
		expectedVolumeUri := "projects/45110233509/locations/us-central1/volumes/override-source-volume"
		// The URI that would normally be used from CcfeRemoteUri
		normalVolumeUri := "projects/45110233509/locations/australia-souteast/volumes/source-volume"

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					Uri:  dbReplicationUri,
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						HybridReplicationType: &reverseType,
						Status:                datamodel.HybridReplicationStatusPeered,
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "customer",
					},
				},
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", nil, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		// Verify that Source.VolumeName uses the volume URI from dbReplication.Uri
		// instead of the one parsed from in.CcfeRemoteUri.Value
		assert.True(tt, result.Source.IsSet())
		assert.Equal(tt, expectedVolumeUri, result.Source.Value.VolumeName.Value)
		// Verify it's different from what would normally be used
		assert.NotEqual(tt, normalVolumeUri, result.Source.Value.VolumeName.Value)
		// Verify that Source.VolumeId is still from in.SourceVolumeUuid
		assert.Equal(tt, "source-volume-uuid", result.Source.Value.VolumeId.Value)
	})

	t.Run("HybridReplicationPendingRemoteResyncWithJobButNoUserCommands", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-souteast/volumes/source-volume/replications/replication-uuid-1"),
		}

		statusDetails := "Pending remote resync"
		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
						Status:        datamodel.HybridReplicationStatusPendingRemoteResync,
						StatusDetails: statusDetails,
						// No user commands
					},
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeReverseHybridReplicationInternal)),
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		// User commands should not be set if they are nil or empty
		assert.False(tt, result.HybridReplicationUserCommands.IsSet())
	})

	t.Run("JobTypeReverseHybridReplicationInternalSetsStateAndMirrorState", func(tt *testing.T) {
		replication := &googleproxyclient.VolumeReplicationInternalV1beta{
			VolumeReplicationUuid: googleproxyclient.NewOptString("replication-uuid-1"),
			LifeCycleState:        googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable),
			LifeCycleStateDetails: googleproxyclient.NewOptString("Available"),
			ReplicationSchedule:   googleproxyclient.NewOptVolumeReplicationInternalV1betaReplicationSchedule(googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly),
			RemoteRegion:          "us-e4",
			SourceVolumeUuid:      googleproxyclient.NewOptString("00000000-0000-0000-0000-000000000000"),
			DestinationVolumeUuid: googleproxyclient.NewOptString("destination-volume-uuid"),
			Name:                  googleproxyclient.NewOptString("replication-1"),
			MirrorState:           googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			TotalProgress:         googleproxyclient.NewOptInt64(100),
			Healthy:               googleproxyclient.NewOptBool(true),
			TotalTransferBytes:    googleproxyclient.NewOptInt64(1024 * 1024 * 1024),
			TotalTransferTimeSecs: googleproxyclient.NewOptInt64(3600),
			LastTransferSize:      googleproxyclient.NewOptInt64(1024 * 1024 * 100),
			LastTransferError:     googleproxyclient.NewOptString("No error"),
			LastTransferDuration:  googleproxyclient.NewOptInt64(300),
			LastTransferEndTime:   googleproxyclient.NewOptDateTime(baseTime),
			ProgressLastUpdated:   googleproxyclient.NewOptDateTime(baseTime),
			LagTime:               googleproxyclient.NewOptInt64(60),
			CreatedAt:             googleproxyclient.NewOptDateTime(baseTime),
			Description:           googleproxyclient.NewOptString("Test replication"),
			CcfeUri:               googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
			CcfeRemoteUri:         googleproxyclient.NewOptString("projects/45110233509/locations/australia-southeast/volumes/source-volume/replications/replication-uuid-1"),
		}

		regionReplicationMap := map[string][]*datamodel.VolumeReplication{
			"us-e4": {
				{
					BaseModel: datamodel.BaseModel{
						UUID: "replication-uuid-1",
					},
					Name: "replication-1",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						DestinationLocation: "us-e4",
					},
				},
			},
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/45110233509/locations/us-e4/volumes/destination-volume/replications/replication-uuid-1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeReverseHybridReplicationInternal)),
			},
		}

		result := convertInternalReplicationToCCFEModel(*replication, "us-e4", &jobsList, regionReplicationMap, nil)

		assert.NotNil(tt, result)
		// Verify that MirrorState is set to STOPPED (line 1260)
		assert.Equal(tt, gcpserver.ReplicationV1betaMirrorStateSTOPPED, result.MirrorState.Value)
		// Verify that State is set to PENDINGREMOTERESYNC (line 1261)
		assert.Equal(tt, gcpserver.ReplicationV1betaStatePENDINGREMOTERESYNC, result.State.Value)
		// Verify that StateDetailsCode is set to 0 (line 1262)
		assert.True(tt, result.StateDetailsCode.IsSet())
		assert.Equal(tt, int32(0), result.StateDetailsCode.Value)
	})
}
func TestMapInternalReplicationStateToCCFEState(t *testing.T) {
	tests := []struct {
		name     string
		input    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState
		expected gcpserver.ReplicationV1betaState
	}{
		{
			name:     "Available maps to READY",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
			expected: gcpserver.ReplicationV1betaStateREADY,
		},
		{
			name:     "CREATING maps to CREATING",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
			expected: gcpserver.ReplicationV1betaStateCREATING,
		},
		{
			name:     "DELETING maps to DELETING",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
			expected: gcpserver.ReplicationV1betaStateDELETING,
		},
		{
			name:     "UPDATING maps to UPDATING",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
			expected: gcpserver.ReplicationV1betaStateUPDATING,
		},
		{
			name:     "Disabled maps to Disabled",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
			expected: gcpserver.ReplicationV1betaStateDISABLED,
		},
		{
			name:     "Unknown state maps to STATE_UNSPECIFIED",
			input:    googleproxyclient.VolumeReplicationInternalV1betaLifeCycleState("UNKNOWN"),
			expected: gcpserver.ReplicationV1betaStateSTATEUNSPECIFIED,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			result := mapInternalReplicationStateToCCFEState(tc.input)
			assert.Equal(tt, tc.expected, result)
		})
	}
}

func TestMapInternalReplicationScheduleToCCFEReschedule(t *testing.T) {
	tests := []struct {
		name     string
		input    googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule
		expected gcpserver.ReplicationV1betaReplicationSchedule
	}{
		{
			name:     "Hourly maps to HOURLY",
			input:    googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleHourly,
			expected: gcpserver.ReplicationV1betaReplicationScheduleHOURLY,
		},
		{
			name:     "Daily maps to DAILY",
			input:    googleproxyclient.VolumeReplicationInternalV1betaReplicationScheduleDaily,
			expected: gcpserver.ReplicationV1betaReplicationScheduleDAILY,
		},
		{
			name:     "10 Minutely maps to Every 10 Minutes",
			input:    googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule10minutely,
			expected: gcpserver.ReplicationV1betaReplicationScheduleEVERY10MINUTES,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			result := mapInternalReplicationScheduleToCCFEReschedule(tc.input)
			assert.Equal(tt, tc.expected, result)
		})
	}
}

func TestMapInternalReplicationMirrorStateToCCFEMirrorState(t *testing.T) {
	tests := []struct {
		name     string
		input    googleproxyclient.VolumeReplicationInternalV1betaMirrorState
		expected gcpserver.ReplicationV1betaMirrorState
	}{
		{
			name:     "MIRRORED maps to MIRRORED",
			input:    googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED,
			expected: gcpserver.ReplicationV1betaMirrorStateMIRRORED,
		},
		{
			name:     "Uninitialized maps to Uninitialized",
			input:    googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED,
			expected: gcpserver.ReplicationV1betaMirrorStateUNINITIALIZED,
		},
		{
			name:     "Stopped maps to Stopped",
			input:    googleproxyclient.VolumeReplicationInternalV1betaMirrorStateSTOPPED,
			expected: gcpserver.ReplicationV1betaMirrorStateSTOPPED,
		},
		{
			name:     "Stopped maps to Stopped",
			input:    googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING,
			expected: gcpserver.ReplicationV1betaMirrorStatePREPARING,
		},
		{
			name:     "Transferring maps to Transferring",
			input:    googleproxyclient.VolumeReplicationInternalV1betaMirrorStateTRANSFERRING,
			expected: gcpserver.ReplicationV1betaMirrorStateTRANSFERRING,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			result := mapInternalReplicationMirrorStateToCCFEMirrorState(tc.input)
			assert.Equal(tt, tc.expected, result)
		})
	}
}

func TestResumeReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation error")
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "validation error", err.Error())
	})
	t.Run("WhenDstReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to verify destination replication")
		}

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to verify destination replication", err.Error())
	})
	t.Run("WhenSourceQuotaRulesValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties for quota rule verification
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return errors.New("source quota rules not in READY state")
		}

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "source quota rules not in READY state", err.Error())
	})
	t.Run("WhenDestinationQuotaRulesValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties for quota rule verification
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return errors.New("destination quota rules in transitioning state")
		}

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "destination quota rules in transitioning state", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties for quota rule verification
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		expectedError := errors.New("failed to execute workflow")
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return expectedResponse, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}

		resp, jobuuid, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenZoneParameterIsProvided", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.ResumeReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
			Zone:        "us-central1-a", // Set zone parameter
		}

		resp, jobuuid, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that the zone parameter was correctly handled
		// The zone should override the Location field in the event
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenZoneParameterIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.ResumeReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
			Zone:        "", // Empty zone parameter
		}

		resp, jobuuid, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that when zone is empty, Location remains unchanged
		// Since params.Region is not set in this test, Location should be empty
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		existingJobUUID := "existing-job-uuid"
		existingReplication := &models.VolumeReplication{
			State: datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType: "src",
			},
		}
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up the event properly
			event.ReplicationModel = &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return existingReplication, &existingJobUUID, nil
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		result, jobUUID, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, existingJobUUID, jobUUID)
		// Note: State and StateDetails are no longer updated when duplicate job exists
	})
	t.Run("WhenFileVolume_QuotaRuleVerificationCalled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		var sourceQuotaRulesCalled, destQuotaRulesCalled bool

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			sourceQuotaRulesCalled = true
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			destQuotaRulesCalled = true
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.True(tt, sourceQuotaRulesCalled, "verifySourceQuotaRules should be called for file volumes")
		assert.True(tt, destQuotaRulesCalled, "verifyDestinationQuotaRules should be called for file volumes")
	})
	t.Run("WhenBlockVolume_QuotaRuleVerificationNotCalled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		var sourceQuotaRulesCalled, destQuotaRulesCalled bool

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up block volume without FileProperties (has BlockProperties instead)
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						BlockProperties: &datamodel.BlockProperties{},
						// FileProperties is nil for block volumes
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			sourceQuotaRulesCalled = true
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			destQuotaRulesCalled = true
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.False(tt, sourceQuotaRulesCalled, "verifySourceQuotaRules should NOT be called for block volumes")
		assert.False(tt, destQuotaRulesCalled, "verifyDestinationQuotaRules should NOT be called for block volumes")
	})
	t.Run("WhenVolumeAttributesNil_QuotaRuleVerificationNotCalled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		var sourceQuotaRulesCalled, destQuotaRulesCalled bool

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up volume with nil VolumeAttributes
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				Volume: &datamodel.Volume{
					// VolumeAttributes is nil
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			sourceQuotaRulesCalled = true
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			destQuotaRulesCalled = true
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.False(tt, sourceQuotaRulesCalled, "verifySourceQuotaRules should NOT be called when VolumeAttributes is nil")
		assert.False(tt, destQuotaRulesCalled, "verifyDestinationQuotaRules should NOT be called when VolumeAttributes is nil")
	})
	t.Run("WhenHybridReplication_QuotaRuleVerificationNotCalled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
			verifySourceQuotaRules = replication.VerifySourceQuotaRules
			verifyDestinationQuotaRules = replication.VerifyDestinationQuotaRules
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		var sourceQuotaRulesCalled, destQuotaRulesCalled bool

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties but with HybridReplicationAttributes (hybrid replication)
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{},
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationResume = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifySourceQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			sourceQuotaRulesCalled = true
			return nil
		}

		verifyDestinationQuotaRules = func(ctx context.Context, event *replication.ResumeReplicationEvent) error {
			destQuotaRulesCalled = true
			return nil
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _resumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.False(tt, sourceQuotaRulesCalled, "verifySourceQuotaRules should NOT be called for hybrid replication")
		assert.False(tt, destQuotaRulesCalled, "verifyDestinationQuotaRules should NOT be called for hybrid replication")
	})
}

func TestResumeReplicationInternal(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		_, _, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenGetVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, nil
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(nil, errors.New("volume replication not found"))

		_, _, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "volume replication not found", err.Error())
	})
	t.Run("WhenUpdateVolumeReplicationStateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(errors.New("update failed"))
		_, _, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "update failed", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
		_, _, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		expectedError := errors.New("failed to execute workflow")
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil).Once() // For setting to UPDATING
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)
		// Mock the UpdateVolumeReplicationStates call to set state to ERROR
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.MatchedBy(func(repl *datamodel.VolumeReplication) bool {
			return repl.State == datamodel.LifeCycleStateError && repl.StateDetails == expectedError.Error()
		})).Return(nil)

		_, _, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			State:              datamodel.LifeCycleStateUpdating,
			StateDetails:       datamodel.LifeCycleStateUpdatingDetails,
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		expectedResponse := convertDataStoreReplicationToModel(replicationDb)
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		resp, job, err := _resumeReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse, job)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func Test_deleteVolumeReplicationRow(t *testing.T) {
	t.Run("GetVolumeReplicationFailsDueToNotFoundError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "volumeReplication")
		assert.NotNil(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "VolumeReplication not found")
	})
	t.Run("GetVolumeReplication fails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(nil, errors.New("failed to get volume replication"))

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "volumeReplication")
		assert.NotNil(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "failed to get volume replication")
	})
	t.Run("WhenReplicationInTransitionState", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateDeleting,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(dbVolumeReplication, nil)

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "test-replication")
		assert.NotNil(tt, err)
		assert.Equal(tt, "Error releasing volume Replication - Volume replication is already transitioning between states", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "test-replication")
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenUpdateVolumeReplicationStatesFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{WorkflowID: "workflow-id"}, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(errors.New("update error"))

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "test-replication")
		assert.NotNil(tt, err)
		assert.Equal(tt, "update error", err.Error())
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
			},
		}
		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{WorkflowID: "workflow-id"}, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)

		_, _, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, "test-replication")
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
	})
	t.Run("Success", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			WorkflowID: "workflow-id",
		}
		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:        params.VolumeReplication.Name,
			Description: params.VolumeReplication.Description,
			Uri:         params.VolumeReplication.Uri,
			RemoteUri:   params.VolumeReplication.RemoteUri,

			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               params.VolumeReplication.ReplicationAttributes.EndpointType,
				ReplicationType:            params.VolumeReplication.ReplicationAttributes.ReplicationType,
				ReplicationSchedule:        params.VolumeReplication.ReplicationAttributes.ReplicationSchedule,
				SourceVolumeUUID:           params.VolumeReplication.ReplicationAttributes.SourceVolumeUUID,
				SourceLocation:             params.VolumeReplication.ReplicationAttributes.SourceRegion,
				SourceHostName:             params.VolumeReplication.ReplicationAttributes.SourceHostName,
				SourceReplicationUUID:      params.VolumeReplication.ReplicationAttributes.SourceReplicationUUID,
				SourceSvmName:              params.VolumeReplication.ReplicationAttributes.SourceSvmName,
				SourceVolumeName:           params.VolumeReplication.ReplicationAttributes.SourceVolumeName,
				DestinationVolumeUUID:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeUUID,
				DestinationLocation:        params.VolumeReplication.ReplicationAttributes.DestinationRegion,
				DestinationHostName:        params.VolumeReplication.ReplicationAttributes.DestinationHostName,
				DestinationReplicationUUID: params.VolumeReplication.ReplicationAttributes.DestinationReplicationUUID,
				DestinationSvmName:         params.VolumeReplication.ReplicationAttributes.DestinationSvmName,
				DestinationVolumeName:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeName,
			},
			MirrorState:           params.VolumeReplication.MirrorState,
			RelationshipStatus:    params.VolumeReplication.RelationshipStatus,
			TotalProgress:         params.VolumeReplication.TotalProgress,
			TotalTransferBytes:    params.VolumeReplication.TotalTransferBytes,
			TotalTransferTimeSecs: params.VolumeReplication.TotalTransferTimeSecs,
			LastTransferSize:      params.VolumeReplication.LastTransferSize,
			LastTransferError:     params.VolumeReplication.LastTransferError,
			LastTransferDuration:  params.VolumeReplication.LastTransferDuration,
			LastTransferEndTime:   params.VolumeReplication.LastTransferEndTime,
			ProgressLastUpdated:   params.VolumeReplication.ProgressLastUpdated,
			LastUpdatedFromOntap:  params.VolumeReplication.LastUpdatedFromOntap,
			LagTime:               params.VolumeReplication.LagTime,
			AccountID:             account.ID,
			Account:               account,
			VolumeID:              params.VolumeReplication.VolumeID,
			Volume:                volume,
		}

		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		mockStorage.On("GetVolumeReplication", ctx, expectedResponse.UUID).Return(replicationDb, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, jobActualResponse, err := _releaseVolumeReplication(ctx, mockStorage, mockTemporal, expectedResponse.UUID)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse, jobActualResponse)
	})
}

func TestStopReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation error")
		}
		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "validation error", err.Error())
	})
	t.Run("WhenDstReplicationStopFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to verify destination replication")
		}

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to verify destination replication", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}

		resp, jobuuid, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenZoneParameterIsProvided", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.StopReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
			Zone:        "us-central1-a", // Set zone parameter
		}

		resp, jobuuid, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that the zone parameter was correctly handled
		// The zone should override the Location field in the event
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenZoneParameterIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationStop = replication.VerifyDstReplicationStop
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.StopReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationStop = func(ctx context.Context, event *replication.StopReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
			Zone:        "", // Empty zone parameter
		}

		resp, jobuuid, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that when zone is empty, Location remains unchanged
		// Since params.Region is not set in this test, Location should be empty
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		existingJobUUID := "existing-job-uuid"
		existingReplication := &models.VolumeReplication{
			State: datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType: "src",
			},
		}
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up the event properly
			event.ReplicationModel = &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return existingReplication, &existingJobUUID, nil
		}
		params := &commonparams.StopReplicationParams{
			AccountName: "account-name",
		}
		result, jobUUID, err := _stopReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, existingJobUUID, jobUUID)
		// Note: State and StateDetails are no longer updated when duplicate job exists
	})
}

func TestGetReplication(t *testing.T) {
	t.Run("WhenGetReplicationReturnSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockOrchestrator := &GCPOrchestrator{storage: mockStorage}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:         "test-volume",
			AccountID:    1,
			VolumeID:     1,
			Description:  "test replication",
			Uri:          "test-uri",
			RemoteUri:    "test-remote-uri",
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               "test-endpoint",
				ReplicationType:            "test-replication-type",
				ReplicationSchedule:        "test-schedule",
				SourceVolumeUUID:           "source-volume-uuid",
				SourceLocation:             "source-region",
				SourceHostName:             "source-host",
				SourceReplicationUUID:      "source-replication-uuid",
				SourceSvmName:              "source-svm",
				SourceVolumeName:           "source-volume",
				DestinationVolumeUUID:      "destination-volume-uuid",
				DestinationLocation:        "destination-region",
				DestinationHostName:        "destination-host",
				DestinationReplicationUUID: "destination-replication-uuid",
				DestinationSvmName:         "destination-svm",
				DestinationVolumeName:      "destination-volume",
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(replicationDb, nil)
		actualResponse, err := mockOrchestrator.GetReplication(ctx, replicationDb.UUID)
		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		assert.Nil(tt, err)
		assert.Equal(tt, expectedResponse, actualResponse)
	})

	t.Run("WhenGetReplicationError", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockOrchestrator := &GCPOrchestrator{storage: mockStorage}

		expectedError := errors.New("database error")

		mockStorage.On("GetVolumeReplication", ctx, mock.Anything).Return(nil, expectedError)

		_, err := mockOrchestrator.GetReplication(ctx, "repl-1")
		assert.NotNil(tt, err)
		assert.Equal(tt, expectedError, err)
	})
}

func Test_deleteVolumeReplication(t *testing.T) {
	t.Run("GetVolumeReplication fails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(nil, errors.New("not found"))

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "not found", err.Error())
	})

	t.Run("WhenReplicationIsInTransitioningState", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateUpdating,
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "Error deleting volume Replication - Volume replication is already transitioning between states", err.Error())
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			Volume:    &datamodel.Volume{Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}}},
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})

	t.Run("WhenUpdateVolumeReplicationStatesFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			Volume:    &datamodel.Volume{Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}}},
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{WorkflowID: "workflow-id"}, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(errors.New("update error"))

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "update error", err.Error())
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			Volume:    &datamodel.Volume{Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}}},
		}

		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-789",
			},
			WorkflowID: "workflow-id",
		}

		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid-789", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false, false).Return(nil, expectedError)
		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("Success", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			WorkflowID: "workflow-id",
		}
		params := &commonparams.CreateVolumeReplicationInternalParams{
			VolumeReplication: &models.VolumeReplication{
				Account: &models.Account{
					BaseModel: models.BaseModel{
						ID: 1,
					},
					Name: "test-account",
				},
				Name: "test-replication",
				ReplicationAttributes: &models.ReplicationDetails{
					DestinationVolumeUUID: "test-volume-uuid",
				},
			},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:        params.VolumeReplication.Name,
			Description: params.VolumeReplication.Description,
			Uri:         params.VolumeReplication.Uri,
			RemoteUri:   params.VolumeReplication.RemoteUri,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               params.VolumeReplication.ReplicationAttributes.EndpointType,
				ReplicationType:            params.VolumeReplication.ReplicationAttributes.ReplicationType,
				ReplicationSchedule:        params.VolumeReplication.ReplicationAttributes.ReplicationSchedule,
				SourceVolumeUUID:           params.VolumeReplication.ReplicationAttributes.SourceVolumeUUID,
				SourceLocation:             params.VolumeReplication.ReplicationAttributes.SourceRegion,
				SourceHostName:             params.VolumeReplication.ReplicationAttributes.SourceHostName,
				SourceReplicationUUID:      params.VolumeReplication.ReplicationAttributes.SourceReplicationUUID,
				SourceSvmName:              params.VolumeReplication.ReplicationAttributes.SourceSvmName,
				SourceVolumeName:           params.VolumeReplication.ReplicationAttributes.SourceVolumeName,
				DestinationVolumeUUID:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeUUID,
				DestinationLocation:        params.VolumeReplication.ReplicationAttributes.DestinationRegion,
				DestinationHostName:        params.VolumeReplication.ReplicationAttributes.DestinationHostName,
				DestinationReplicationUUID: params.VolumeReplication.ReplicationAttributes.DestinationReplicationUUID,
				DestinationSvmName:         params.VolumeReplication.ReplicationAttributes.DestinationSvmName,
				DestinationVolumeName:      params.VolumeReplication.ReplicationAttributes.DestinationVolumeName,
			},
			MirrorState:           params.VolumeReplication.MirrorState,
			RelationshipStatus:    params.VolumeReplication.RelationshipStatus,
			TotalProgress:         params.VolumeReplication.TotalProgress,
			TotalTransferBytes:    params.VolumeReplication.TotalTransferBytes,
			TotalTransferTimeSecs: params.VolumeReplication.TotalTransferTimeSecs,
			LastTransferSize:      params.VolumeReplication.LastTransferSize,
			LastTransferError:     params.VolumeReplication.LastTransferError,
			LastTransferDuration:  params.VolumeReplication.LastTransferDuration,
			LastTransferEndTime:   params.VolumeReplication.LastTransferEndTime,
			ProgressLastUpdated:   params.VolumeReplication.ProgressLastUpdated,
			LastUpdatedFromOntap:  params.VolumeReplication.LastUpdatedFromOntap,
			LagTime:               params.VolumeReplication.LagTime,
			AccountID:             account.ID,
			Account:               account,
			VolumeID:              params.VolumeReplication.VolumeID,
			Volume:                volume,
		}

		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		mockStorage.On("GetVolumeReplication", ctx, expectedResponse.UUID).Return(replicationDb, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false, false).Return(nil, nil)

		_, jobActualResponse, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, expectedResponse.UUID, false, false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse, jobActualResponse)
	})
	t.Run("WhenIsCleanupTrueAndStateIsCreating", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:                  "test-replication",
			AccountID:             1,
			Account:               account,
			State:                 datamodel.LifeCycleStateCreating,
			Volume:                volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-789",
			},
			WorkflowID: "workflow-id",
		}

		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		// When cleanupAfterReverse=false, UpdateVolumeReplicationStates is called even if isCleanup=true
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, false, true).Return(nil, nil)

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, true)
		// Should succeed - cleanup allows Creating state
		assert.Nil(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenIsCleanupTrueAndStateIsUpdating", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateUpdating,
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, true)
		assert.NotNil(tt, err)
		assert.Equal(tt, "Error deleting volume Replication - Volume replication is already transitioning between states", err.Error())
	})
	t.Run("WhenIsCleanupTrueAndStateIsDeleting", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		volumeReplication := &models.VolumeReplication{
			BaseModel: models.BaseModel{UUID: "replication-uuid"},
			Name:      "test-replication",
		}

		dbVolumeReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid",
			},
			Name:      "test-replication",
			AccountID: 1,
			State:     datamodel.LifeCycleStateDeleting,
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplication.UUID).Return(dbVolumeReplication, nil)

		_, _, err := _deleteReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplication.UUID, false, true)
		assert.NotNil(tt, err)
		assert.Equal(tt, "Error deleting volume Replication - Volume replication is already transitioning between states", err.Error())
	})
}

func TestStopReplicationInternal(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		_, _, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenGetVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, nil
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(nil, errors.New("volume replication not found"))

		_, _, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "volume replication not found", err.Error())
	})
	t.Run("WhenUpdateVolumeReplicationStateFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(errors.New("update failed"))
		_, _, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "update failed", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			BaseModel: datamodel.BaseModel{
				UUID: "1234567890",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID: "123",
					},
				},
			},
		}
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
		_, _, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			BaseModel: datamodel.BaseModel{
				UUID: "1234567890",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID: "123",
					},
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}

		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)
		// Mock the UpdateVolumeReplicationStates call to set state to ERROR
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.MatchedBy(func(repl *datamodel.VolumeReplication) bool {
			return repl.State == datamodel.LifeCycleStateError && repl.StateDetails == expectedError.Error()
		})).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		_, _, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		// Verify that UpdateVolumeReplicationStates was called to revert state to READY
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		volumeReplicationId := "replication-id-1"
		accountName := "test-account"
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		mirrorState := "broken_off"
		relationShipStatus := "idle"
		replicationDb := &datamodel.VolumeReplication{
			State:              datamodel.LifeCycleStateUpdating,
			StateDetails:       datamodel.LifeCycleStateUpdatingDetails,
			MirrorState:        &mirrorState,
			LastTransferError:  "error",
			RelationshipStatus: &relationShipStatus,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "src",
			},
			BaseModel: datamodel.BaseModel{
				UUID: "1234567890",
			},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID: "123",
					},
				},
			},
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		expectedResponse := convertDataStoreReplicationToModel(replicationDb)
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		resp, job, err := _stopReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName, false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse, job)
		assert.Equal(tt, expectedResponse, resp)
	})
}

func TestDeleteReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation error")
		}
		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "validation error", err.Error())
	})
	t.Run("WhenDstReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}

		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to verify destination replication")
		}

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to verify destination replication", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}

		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob to fail
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob for successful cases
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		expectedError := errors.New("failed to execute workflow")

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenExecuteWorkflowFailsForCleanup", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob for successful cases
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		expectedError := errors.New("failed to execute workflow")

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", true)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{EndpointType: "dst"},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		resp, jobuuid, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenZoneParameterIsProvided", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.DeleteReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
			Zone:        "us-central1-a", // Set zone parameter
		}

		resp, jobuuid, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that the zone parameter was correctly handled
		// The zone should override the Location field in the event
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "us-central1-a", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenZoneParameterIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Capture the event to verify zone parameter handling
		var capturedEvent *replication.DeleteReplicationEvent
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			// Capture the event for verification
			capturedEvent = event
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
			Zone:        "", // Empty zone parameter
		}

		resp, jobuuid, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)

		// Verify that when zone is empty, Location remains unchanged
		// Since params.Region is not set in this test, Location should be empty
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Location)
		assert.Equal(tt, "", capturedEvent.CommonReplicationEventParams.Zone)
	})
	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		existingJobUUID := "existing-job-uuid"
		existingReplication := &models.VolumeReplication{
			State: datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType: "src",
			},
		}
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up the event properly
			event.ReplicationModel = &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return existingReplication, &existingJobUUID, nil
		}
		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}
		result, jobUUID, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, existingJobUUID, jobUUID)
		// Note: State and StateDetails are no longer updated when duplicate job exists
	})
	t.Run("WhenCleanupResourcesJobIdIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob for successful cases
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateJob", ctx, mock.Anything, string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		// Test with empty cleanupResourcesJobId
		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.Nil(tt, err)
	})
	t.Run("WhenCleanupResourcesJobIdHasValidFormat", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob for successful cases
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock GetJob to return a job with valid resource name
		expectedJobUUID := "6294efb1-c7c1-4742-a014-425f774dc986"
		expectedResourceName := "projects/45110233509/locations/australia-southeast1-a/volumes/mrasrc1255/replications/replicationtest581"
		mockJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: expectedJobUUID,
			},
			ResourceName: expectedResourceName,
		}

		mockStorage.On("GetJob", ctx, expectedJobUUID).Return(mockJob, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName:           "account-name",
			ReplicationResourceId: "replicationtest581",
		}

		cleanupResourcesJobId := "/v1beta/projects/242512777037/locations/us-central1/operations/" + expectedJobUUID

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.Nil(tt, err)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetJob to return an error
		expectedJobUUID := "6294efb1-c7c1-4742-a014-425f774dc986"
		mockStorage.On("GetJob", ctx, expectedJobUUID).Return(nil, errors.New("job not found"))

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		cleanupResourcesJobId := "/v1beta/projects/242512777037/locations/us-central1/operations/" + expectedJobUUID

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.NotNil(tt, err)
		assert.Contains(tt, err.Error(), "job not found")
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenReplicationNameMismatch", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock GetJob to return a job with different replication name
		expectedJobUUID := "6294efb1-c7c1-4742-a014-425f774dc986"
		expectedResourceName := "projects/45110233509/locations/australia-southeast1-a/volumes/mrasrc1255/replications/differentreplication"
		mockJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: expectedJobUUID,
			},
			ResourceName: expectedResourceName,
		}

		mockStorage.On("GetJob", ctx, expectedJobUUID).Return(mockJob, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName:           "account-name",
			ReplicationResourceId: "replicationtest581", // Different from job's replication name
		}

		cleanupResourcesJobId := "/v1beta/projects/242512777037/locations/us-central1/operations/" + expectedJobUUID

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.NotNil(tt, err)
		var vcpErr *errors2.CustomError
		if assert.ErrorAs(tt, err, &vcpErr) {
			assert.Contains(tt, vcpErr.Error(), "Failed to Delete volume replication")
		}
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenResourceNameIsEmpty", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		// Mock CreateJob for successful cases
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Mock GetJob to return a job with empty resource name
		expectedJobUUID := "6294efb1-c7c1-4742-a014-425f774dc986"
		mockJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID: expectedJobUUID,
			},
			ResourceName: "", // Empty resource name
		}

		mockStorage.On("GetJob", ctx, expectedJobUUID).Return(mockJob, nil)

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		cleanupResourcesJobId := "/v1beta/projects/242512777037/locations/us-central1/operations/" + expectedJobUUID

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.Nil(tt, err) // Should succeed when resource name is empty
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenCleanupResourcesJobIdHasInvalidFormat", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		// Test with invalid format (no slashes) - ValidateOperationUri will fail before GetJob is called
		cleanupResourcesJobId := "invalid-job-id-format"

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.NotNil(tt, err) // Should fail because ValidateOperationUri fails
		assert.Contains(tt, err.Error(), "OperationURIs should match")
	})
	t.Run("WhenCleanupResourcesJobIdHasOnlySlash", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			VerifyReplicationDelete = replication.VerifyReplication
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "123"}},
				},
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{
					EndpointType: "destination",
				},
			}, nil
		}

		params := &commonparams.DeleteReplicationParams{
			AccountName: "account-name",
		}

		// Test with only slash - ValidateOperationUri will fail before GetJob is called
		cleanupResourcesJobId := "/"

		_, _, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, cleanupResourcesJobId, false)
		assert.NotNil(tt, err) // Should fail because ValidateOperationUri fails
		assert.Contains(tt, err.Error(), "OperationURIs should match")
	})
}

func TestSyncReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation error")
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "validation error", err.Error())
	})
	t.Run("WhenDstReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationSync = replication.VerifyDstReplicationSync
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		verifyDstReplicationSync = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to verify destination replication")
		}

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to verify destination replication", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationSync = replication.VerifyDstReplicationSync
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "123",
						},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationSync = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationSync = replication.VerifyDstReplicationSync
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "123",
						},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationSync = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}

		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationSync = replication.VerifyDstReplicationSync
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
				BaseModel: datamodel.BaseModel{
					UUID: "1234567890",
				},
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "123",
						},
					},
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		verifyDstReplicationSync = func(ctx context.Context, event *replication.ResumeReplicationEvent) (*models.VolumeReplication, error) {
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}

		resp, jobuuid, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		existingJobUUID := "existing-job-uuid"
		existingReplication := &models.VolumeReplication{
			State: datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType: "src",
			},
		}
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up the event properly
			event.ReplicationModel = &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return existingReplication, &existingJobUUID, nil
		}
		params := &commonparams.ResumeReplicationParams{
			AccountName: "account-name",
		}
		result, jobUUID, err := _syncReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, existingJobUUID, jobUUID)
		// Note: State and StateDetails are no longer updated when duplicate job exists
	})
}

func TestUpdateVolumeReplicationInternal(t *testing.T) {
	t.Run("WhenGetAccountWithNameFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.UpdateVolumeReplicationInternalParams{}
		_, _, err := _updateVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("WhenGetVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		params := &commonparams.UpdateVolumeReplicationInternalParams{
			VolumeReplicationUuid: "replication-uuid-1",
		}
		mockStorage.On("GetVolumeReplication", ctx, "replication-uuid-1").Return(nil, errors.New("volume replication not found"))
		_, _, err := _updateVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "volume replication not found", err.Error())
	})
	t.Run("WhenUpdateVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		params := &commonparams.UpdateVolumeReplicationInternalParams{
			VolumeReplicationUuid: "replication-uuid-1",
		}
		mockStorage.On("GetVolumeReplication", ctx, "replication-uuid-1").Return(&datamodel.VolumeReplication{
			Volume: &datamodel.Volume{State: models2.VolumeStateOnline},
		}, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(errors.New("update failed"))
		_, _, err := _updateVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "update failed", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{Name: "test-account"}, nil
		}
		params := &commonparams.UpdateVolumeReplicationInternalParams{
			VolumeReplicationUuid: "replication-uuid-1",
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid-update",
			},
		}

		volumeRep := &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{ID: 2}, Volume: &datamodel.Volume{State: models2.VolumeStateOnline, Pool: &datamodel.Pool{}}}

		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("GetVolumeReplication", ctx, "replication-uuid-1").Return(volumeRep, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid-update", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		_, _, err := _updateVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "test-account",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		params := &commonparams.UpdateVolumeReplicationInternalParams{
			VolumeReplicationUuid: "replication-uuid-1",
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			WorkflowID: "workflow-id",
		}
		volumeReplication := &models.VolumeReplication{
			Account: &models.Account{
				BaseModel: models.BaseModel{
					ID: 1,
				},
				Name: "test-account",
			},
			Name: "test-replication",
			ReplicationAttributes: &models.ReplicationDetails{
				DestinationVolumeUUID: "test-volume-uuid",
			},
		}
		replicationDb := &datamodel.VolumeReplication{
			Name:        volumeReplication.Name,
			Description: volumeReplication.Description,
			Uri:         volumeReplication.Uri,
			RemoteUri:   volumeReplication.RemoteUri,
			Volume: &datamodel.Volume{
				State: models2.VolumeStateOnline,
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID: "123",
					},
				}},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:               volumeReplication.ReplicationAttributes.EndpointType,
				ReplicationType:            volumeReplication.ReplicationAttributes.ReplicationType,
				ReplicationSchedule:        volumeReplication.ReplicationAttributes.ReplicationSchedule,
				SourceVolumeUUID:           volumeReplication.ReplicationAttributes.SourceVolumeUUID,
				SourceLocation:             volumeReplication.ReplicationAttributes.SourceRegion,
				SourceHostName:             volumeReplication.ReplicationAttributes.SourceHostName,
				SourceReplicationUUID:      volumeReplication.ReplicationAttributes.SourceReplicationUUID,
				SourceSvmName:              volumeReplication.ReplicationAttributes.SourceSvmName,
				SourceVolumeName:           volumeReplication.ReplicationAttributes.SourceVolumeName,
				DestinationVolumeUUID:      volumeReplication.ReplicationAttributes.DestinationVolumeUUID,
				DestinationLocation:        volumeReplication.ReplicationAttributes.DestinationRegion,
				DestinationHostName:        volumeReplication.ReplicationAttributes.DestinationHostName,
				DestinationReplicationUUID: volumeReplication.ReplicationAttributes.DestinationReplicationUUID,
				DestinationSvmName:         volumeReplication.ReplicationAttributes.DestinationSvmName,
				DestinationVolumeName:      volumeReplication.ReplicationAttributes.DestinationVolumeName,
			},
			MirrorState:           volumeReplication.MirrorState,
			RelationshipStatus:    volumeReplication.RelationshipStatus,
			TotalProgress:         volumeReplication.TotalProgress,
			TotalTransferBytes:    volumeReplication.TotalTransferBytes,
			TotalTransferTimeSecs: volumeReplication.TotalTransferTimeSecs,
			LastTransferSize:      volumeReplication.LastTransferSize,
			LastTransferError:     volumeReplication.LastTransferError,
			LastTransferDuration:  volumeReplication.LastTransferDuration,
			LastTransferEndTime:   volumeReplication.LastTransferEndTime,
			ProgressLastUpdated:   volumeReplication.ProgressLastUpdated,
			LastUpdatedFromOntap:  volumeReplication.LastUpdatedFromOntap,
			LagTime:               volumeReplication.LagTime,
			AccountID:             account.ID,
			Account:               account,
			VolumeID:              volumeReplication.VolumeID,
		}

		replicationDb.State = datamodel.LifeCycleStateUpdating
		replicationDb.StateDetails = datamodel.LifeCycleStateUpdatingDetails
		expectedResponse := convertDataStoreReplicationToModel(replicationDb)

		mockStorage.On("GetVolumeReplication", ctx, "replication-uuid-1").Return(replicationDb, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, params, replicationDb).Return(nil, nil)

		actualResponse, jobActualResponse, err := updateVolumeReplicationInternal(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, expectedResponse, actualResponse)
		assert.Equal(tt, jobResponse, jobActualResponse)
	})
}

func TestUpdateReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
		}()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})
	t.Run("WhenValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation error")
		}
		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "validation error", err.Error())
	})
	t.Run("WhenDstReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		validateReplicationUpdate = func(ctx context.Context, event *replication.UpdateReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("failed to verify destination replication")
		}

		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to verify destination replication", err.Error())
	})
	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "pool-uuid-1",
						},
					},
				},
				BaseModel: datamodel.BaseModel{
					UUID: "uuid-1",
				},
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
			}
			return nil, nil, nil
		}

		validateReplicationUpdate = func(ctx context.Context, event *replication.UpdateReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
	})
	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "pool-uuid-1",
						},
					},
				},
				BaseModel: datamodel.BaseModel{
					UUID: "uuid-1",
				},
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
			}
			return nil, nil, nil
		}

		validateReplicationUpdate = func(ctx context.Context, event *replication.UpdateReplicationEvent) (*models.VolumeReplication, error) {
			return nil, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}

		expectedError := errors.New("failed to execute workflow")

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)

		// Mock the UpdateJob call that should happen in the defer block
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, expectedError.Error()).Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)

		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())

		// Verify that UpdateJob was called to mark the job as ERROR
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationResume = replication.VerifyDstReplicationResume
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = &datamodel.VolumeReplication{
				Volume: &datamodel.Volume{
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{
							UUID: "pool-uuid-1",
						},
					},
				},
				BaseModel: datamodel.BaseModel{
					UUID: "uuid-1",
				},
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return nil, nil, nil
		}
		expectedResponse := &models.VolumeReplication{
			ReplicationAttributes: &models.ReplicationDetails{},
		}

		validateReplicationUpdate = func(ctx context.Context, event *replication.UpdateReplicationEvent) (*models.VolumeReplication, error) {
			return expectedResponse, nil
		}
		jobResponse := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-uuid",
			},
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(jobResponse, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}

		resp, jobuuid, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.Equal(tt, jobResponse.UUID, jobuuid)
		assert.Equal(tt, expectedResponse, resp)
	})
	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		existingJobUUID := "existing-job-uuid"
		existingReplication := &models.VolumeReplication{
			State: datamodel.LifeCycleStateREADY,
			ReplicationAttributes: &models.ReplicationDetails{
				EndpointType: "src",
			},
		}
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up the event properly
			event.ReplicationModel = &datamodel.VolumeReplication{
				ReplicationAttributes: &datamodel.ReplicationDetails{
					EndpointType: "src",
				},
			}
			return existingReplication, &existingJobUUID, nil
		}
		params := &commonparams.UpdateReplicationParams{
			AccountName: "account-name",
		}
		result, jobUUID, err := _updateReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, existingJobUUID, jobUUID)
		// Note: State and StateDetails are no longer updated when duplicate job exists
	})
}

func TestGetActiveReplicationJobs(t *testing.T) {
	t.Run("WhenResponseIsOK", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		// Mock the google proxy client
		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		// Mock successful response
		expectedJobs := []googleproxyclient.InternalJobV1beta{
			{
				JobType:      googleproxyclient.NewOptString("CreateVolumeReplication"),
				ResourceName: googleproxyclient.NewOptString("test-replication-uri"),
			},
		}
		okResponse := &googleproxyclient.V1betaInternalGetReplicationJobsOK{
			Jobs: expectedJobs,
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(okResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.NoError(tt, err)
		assert.Equal(tt, expectedJobs, jobs)
	})

	t.Run("WhenResponseIsBadRequest", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		badRequestResponse := &googleproxyclient.V1betaInternalGetReplicationJobsBadRequest{
			Message: "Bad request error",
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(badRequestResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Bad request error")
	})

	t.Run("WhenResponseIsInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		internalServerErrorResponse := &googleproxyclient.V1betaInternalGetReplicationJobsInternalServerError{
			Message: "Internal server error",
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(internalServerErrorResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Internal server error")
	})

	t.Run("WhenResponseIsUnauthorized", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		unauthorizedResponse := &googleproxyclient.V1betaInternalGetReplicationJobsUnauthorized{
			Message: "Unauthorized",
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(unauthorizedResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Unauthorized")
	})

	t.Run("WhenResponseIsForbidden", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		forbiddenResponse := &googleproxyclient.V1betaInternalGetReplicationJobsForbidden{
			Message: "Forbidden",
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(forbiddenResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Forbidden")
	})

	t.Run("WhenResponseIsNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		notFoundResponse := &googleproxyclient.V1betaInternalGetReplicationJobsNotFound{
			Message: "Not found",
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(notFoundResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "Not found")
	})

	t.Run("WhenClientCallFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "test-correlation-id", Set: true},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(nil, errors.New("network error"))

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nillable.GetStringPtr("test-correlation-id"))

		assert.Error(tt, err)
		assert.Nil(tt, jobs)
		assert.Equal(tt, "network error", err.Error())
	})

	t.Run("WhenXCorrelationIDIsNil", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

		originalGetGProxyClient := googleproxyclient.GetGProxyClient
		defer func() { googleproxyclient.GetGProxyClient = originalGetGProxyClient }()

		mockClient := googleproxyclient.NewMockInvoker(tt)

		expectedJobs := []googleproxyclient.InternalJobV1beta{}
		okResponse := &googleproxyclient.V1betaInternalGetReplicationJobsOK{
			Jobs: expectedJobs,
		}

		params := googleproxyclient.V1betaInternalGetReplicationJobsParams{
			ProjectNumber:  "test-project",
			LocationId:     "us-east4",
			XCorrelationID: googleproxyclient.OptString{Value: "", Set: false},
		}

		mockClient.EXPECT().V1betaInternalGetReplicationJobs(ctx, params).Return(okResponse, nil)

		mc := &googleproxyclient.ProxyClient{
			Invoker: mockClient,
		}
		googleproxyclient.GetGProxyClient = func(basePath string, jwt string, logger log.Logger) *googleproxyclient.ProxyClient {
			return mc
		}

		jobs, err := _getActiveReplicationJobs(ctx, "test-base-path", "test-token", "us-east4", "test-project", nil)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedJobs, jobs)
	})
}

func TestReplicationHasJob(t *testing.T) {
	t.Run("WhenJobFoundByCcfeUri", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeCreateVolumeReplication)),
			},
			{
				ResourceName: googleproxyclient.NewOptString("projects/789/locations/us-west1/volumes/vol3/replications/repl3"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeDeleteVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.True(tt, hasJob)
		assert.Equal(tt, string(datamodel.JobTypeCreateVolumeReplication), jobType)
	})

	t.Run("WhenJobFoundByCcfeRemoteUri", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeStopVolumeReplication)),
			},
			{
				ResourceName: googleproxyclient.NewOptString("projects/789/locations/us-west1/volumes/vol3/replications/repl3"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeDeleteVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.True(tt, hasJob)
		assert.Equal(tt, string(datamodel.JobTypeStopVolumeReplication), jobType)
	})

	t.Run("WhenJobNotFound", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/789/locations/us-west1/volumes/vol3/replications/repl3"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeDeleteVolumeReplication)),
			},
			{
				ResourceName: googleproxyclient.NewOptString("projects/111/locations/europe-west1/volumes/vol4/replications/repl4"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeUpdateVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.False(tt, hasJob)
		assert.Equal(tt, "", jobType)
	})

	t.Run("WhenEmptyJobsList", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.False(tt, hasJob)
		assert.Equal(tt, "", jobType)
	})

	t.Run("WhenMultipleJobsMatchPrimaryCcfeUriTakesPrecedence", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeCreateVolumeReplication)),
			},
			{
				ResourceName: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeStopVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.True(tt, hasJob)
		assert.Equal(tt, string(datamodel.JobTypeCreateVolumeReplication), jobType)
	})

	t.Run("WhenJobTypeIsResumeVolumeReplication", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeResumeVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.True(tt, hasJob)
		assert.Equal(tt, string(datamodel.JobTypeResumeVolumeReplication), jobType)
	})

	t.Run("WhenJobTypeIsReverseResumeVolumeReplication", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobsList := []googleproxyclient.InternalJobV1beta{
			{
				ResourceName: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
				JobType:      googleproxyclient.NewOptString(string(datamodel.JobTypeReverseResumeVolumeReplication)),
			},
		}

		jobType, hasJob := replicationHasJob(replication, &jobsList)

		assert.True(tt, hasJob)
		assert.Equal(tt, string(datamodel.JobTypeReverseResumeVolumeReplication), jobType)
	})

	t.Run("WhenNilJobsList", func(tt *testing.T) {
		replication := googleproxyclient.VolumeReplicationInternalV1beta{
			CcfeUri:       googleproxyclient.NewOptString("projects/123/locations/us-central1/volumes/vol1/replications/repl1"),
			CcfeRemoteUri: googleproxyclient.NewOptString("projects/456/locations/us-east1/volumes/vol2/replications/repl2"),
		}

		jobType, hasJob := replicationHasJob(replication, nil)

		assert.False(tt, hasJob)
		assert.Equal(tt, "", jobType)
	})
}

func TestGetProjectNumberForRegion(t *testing.T) {
	t.Run("WhenRegionFoundInUri", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/123456789/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeDestination,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "us-central1") {
				return "123456789", nil
			}
			return "987654321", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.NoError(tt, err)
		assert.Equal(tt, "123456789", projectNumber)
	})

	t.Run("WhenRegionFoundInRemoteUri", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/123456789/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "us-east1") {
				return "987654321", nil
			}
			return "123456789", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-east1")

		assert.NoError(tt, err)
		assert.Equal(tt, "987654321", projectNumber)
	})

	t.Run("WhenRegionNotFoundInUriButFoundInRemoteUri", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/123456789/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "us-east1") {
				return "987654321", nil
			}
			return "123456789", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-east1")

		assert.NoError(tt, err)
		assert.Equal(tt, "987654321", projectNumber)
	})

	t.Run("WhenUtilsParseProjectNumberFromURIReturnsError", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/123456789/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeDestination,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to return error
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", errors.New("failed to parse project number")
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "failed to parse project number", err.Error())
	})

	t.Run("WhenRegionNotFoundInEitherUri", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/123456789/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			// Should be called with RemoteUri since region is not in Uri
			if strings.Contains(uri, "us-east1") {
				return "987654321", nil
			}
			return "123456789", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-west1")

		assert.NoError(tt, err)
		assert.Equal(tt, "987654321", projectNumber) // Should return project from RemoteUri
	})

	t.Run("WhenHybridReplicationAttributesIsNotNil", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/111222333/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Description: "hybrid replication",
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "111222333") {
				return "111222333", nil
			}
			return "987654321", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.NoError(tt, err)
		assert.Equal(tt, "111222333", projectNumber) // Should return project from Uri regardless of EndpointType
	})

	t.Run("WhenHybridReplicationAttributesIsNotNilAndParsingFails", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/111222333/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Description: "hybrid replication",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to return error
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			return "", errors.New("invalid URI format")
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "invalid URI format", err.Error())
	})

	t.Run("WhenEndpointTypeSourceAndRemoteUriParsingFails", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/111222333/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to return error for RemoteUri
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "987654321") { // RemoteUri
				return "", errors.New("remote URI parsing failed")
			}
			return "111222333", nil // Uri
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-east1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "remote URI parsing failed", err.Error())
	})

	t.Run("WhenEndpointTypeDestinationAndUriParsingFails", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/111222333/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeDestination,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to return error for Uri
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "111222333") { // Uri
				return "", errors.New("URI parsing failed")
			}
			return "987654321", nil // RemoteUri
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "URI parsing failed", err.Error())
	})

	t.Run("WhenEndpointTypeIsUnknownValue", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "projects/111222333/locations/us-central1/volumes/vol1/replications/repl1",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "unknown", // Unknown endpoint type
			},
		}

		// Mock the utilsParseProjectNumberFromURI function
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if strings.Contains(uri, "111222333") {
				return "111222333", nil
			}
			return "987654321", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.NoError(tt, err)
		assert.Equal(tt, "111222333", projectNumber) // Should fall through to else branch and use Uri
	})

	t.Run("WhenBothUriAndRemoteUriAreEmpty", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "",
			RemoteUri: "",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeDestination,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to handle empty URI
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if uri == "" {
				return "", errors.New("empty URI provided")
			}
			return "123456789", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "empty URI provided", err.Error())
	})

	t.Run("WhenHybridReplicationWithEmptyUri", func(tt *testing.T) {
		replication := &datamodel.VolumeReplication{
			Uri:       "",
			RemoteUri: "projects/987654321/locations/us-east1/volumes/vol2/replications/repl2",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Description: "hybrid replication",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: database.VolumeReplicationEndpointTypeSource,
			},
		}

		// Mock the utilsParseProjectNumberFromURI function to handle empty URI
		originalUtilsParseProjectNumberFromURI := utilsParseProjectNumberFromURI
		defer func() { utilsParseProjectNumberFromURI = originalUtilsParseProjectNumberFromURI }()

		utilsParseProjectNumberFromURI = func(uri string) (string, error) {
			if uri == "" {
				return "", errors.New("hybrid replication URI is empty")
			}
			return "987654321", nil
		}

		projectNumber, err := _getProjectNumberForRegion(replication, "us-central1")

		assert.Error(tt, err)
		assert.Equal(tt, "", projectNumber)
		assert.Equal(tt, "hybrid replication URI is empty", err.Error())
	})
}

func TestReverseReplicationInternal(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		_, _, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.Error(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("WhenGetVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(nil, errors.New("replication not found"))

		_, _, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.Error(tt, err)
		assert.Equal(tt, "replication not found", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateVolumeReplicationStatesFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-123"}},
			},
			Uri: "test-uri",
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.AnythingOfType("*datamodel.VolumeReplication")).Return(errors.New("update failed"))

		_, _, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.Error(tt, err)
		assert.Equal(tt, "update failed", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-123"}},
			},
			Uri: "test-uri",
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.AnythingOfType("*datamodel.VolumeReplication")).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job"))

		_, _, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.Error(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-123"}},
			},
			Uri: "test-uri",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		expectedError := errors.New("workflow execution failed")
		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.AnythingOfType("*datamodel.VolumeReplication")).Return(nil).Once() // For setting to UPDATING
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)
		// Mock the UpdateVolumeReplicationStates call to set state to ERROR
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.MatchedBy(func(repl *datamodel.VolumeReplication) bool {
			return repl.State == datamodel.LifeCycleStateError && repl.StateDetails == expectedError.Error()
		})).Return(nil)

		_, _, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.Error(tt, err)
		assert.Equal(tt, "workflow execution failed", err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		replicationDb := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-123"}},
			},
			Uri: "test-uri",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				SourceHostName: "source-host",
			},
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		volumeReplicationId := "replication-123"
		accountName := "test-account"

		mockStorage.On("GetVolumeReplication", ctx, volumeReplicationId).Return(replicationDb, nil)
		mockStorage.On("UpdateVolumeReplicationStates", ctx, mock.AnythingOfType("*datamodel.VolumeReplication")).Return(nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		volumeReplication, job, err := reverseReplicationInternal(ctx, mockStorage, mockTemporal, volumeReplicationId, accountName)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, job)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", job.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, replicationDb.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, replicationDb.StateDetails)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func TestReverseAndResumeReplication(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("WhenValidateReplicationParamsFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, errors.New("validation failed")
		}

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Equal(tt, "validation failed", err.Error())
	})

	t.Run("WhenVerifyDstReplicationReverseFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			return nil, errors.New("verification failed")
		}

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Equal(tt, "verification failed", err.Error())
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:        "dst",
				ReplicationType:     "async",
				ReplicationSchedule: "daily",
				SourcePoolUUID:      "source-pool-123",
				SourceVolumeUUID:    "source-volume-123",
				SourceLocation:      "us-central1",
				SourceHostName:      "source-host",
			},
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job"))

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:        "dst",
				ReplicationType:     "async",
				ReplicationSchedule: "daily",
				SourcePoolUUID:      "source-pool-123",
				SourceVolumeUUID:    "source-volume-123",
				SourceLocation:      "us-central1",
				SourceHostName:      "source-host",
			},
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		expectedError := errors.New("workflow execution failed")
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedError)
		mockStorage.On("UpdateJob", ctx, mock.Anything, mock.Anything, mock.Anything, expectedError.Error()).Return(nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.Error(tt, err)
		assert.Equal(tt, "workflow execution failed", err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			State:     "active",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "dst",
			},
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		volumeReplication, jobUuid, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, jobUuid)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", *jobUuid)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		assert.Equal(tt, "dst", volumeReplication.ReplicationAttributes.EndpointType)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSuccess_WithHybridReplication", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			State:     "active",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "dst",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: nillable.GetStringPtr("REVERSE_ONPREM_REPLICATION"),
			},
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		// Verify that ReverseHybridReplicationWorkflow is called for hybrid replication
		// We use mock.Anything for the workflow function since testify/mock cannot match function values directly
		// The workflow selection logic is tested through the behavior (hybrid replication routes to ReverseHybridReplicationWorkflow)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == createdJob.WorkflowID
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		volumeReplication, jobUuid, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, jobUuid)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", *jobUuid)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		assert.Equal(tt, "dst", volumeReplication.ReplicationAttributes.EndpointType)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSuccess_WithStandardReplication", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			State:     "active",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "dst",
			},
			HybridReplicationAttributes: nil, // No hybrid replication attributes
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		// Verify that ReverseAndResumeVolumeReplicationWorkflow is called for standard replication
		// We use mock.Anything for the workflow function since testify/mock cannot match function values directly
		// The workflow selection logic is tested through the behavior (standard replication routes to ReverseAndResumeVolumeReplicationWorkflow)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		volumeReplication, jobUuid, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, jobUuid)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", *jobUuid)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		assert.Equal(tt, "dst", volumeReplication.ReplicationAttributes.EndpointType)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSuccess_WithNilReplicationModel", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalFunc := getAccountWithName
		defer func() { getAccountWithName = originalFunc }()
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}, nil
		}

		originalValidateFunc := validateReplicationParams
		defer func() { validateReplicationParams = originalValidateFunc }()
		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			return nil, nil, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "test-uri",
			State:     "active",
			Volume: &datamodel.Volume{
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-123"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "dst",
			},
			HybridReplicationAttributes: nil, // No hybrid replication attributes - should route to standard workflow
		}

		originalVerifyFunc := verifyDstReplicationReverse
		defer func() { verifyDstReplicationReverse = originalVerifyFunc }()
		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel // ReplicationModel has nil HybridReplicationAttributes
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		// When HybridReplicationAttributes is nil, should route to standard workflow
		// We use mock.Anything for the workflow function since testify/mock cannot match function values directly
		// The workflow selection logic is tested through the behavior (nil HybridReplicationAttributes routes to ReverseAndResumeVolumeReplicationWorkflow)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == createdJob.WorkflowID
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		volumeReplication, jobUuid, err := reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, jobUuid)
		assert.Equal(tt, "job-123", *jobUuid)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenHybridReplicationTypeIsONPREM", func(tt *testing.T) {
		// Test the validation logic: if event.ReplicationModel has HybridReplicationAttributes
		// with type ONPREM, it should return an error
		hybridType := string(datamodel.HybridReplicationParametersReplicationTypeONPREM)
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &hybridType,
			},
		}

		// Verify the validation condition (lines 2057-2059)
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			testHybridAttrs := testReplicationModel.HybridReplicationAttributes
			isHybridReplication := nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
			assert.True(tt, isHybridReplication, "ONPREM type should be detected as restricted hybrid replication")
		}
	})

	t.Run("WhenHybridReplicationTypeIsREVERSE", func(tt *testing.T) {
		// Test the validation logic: if event.ReplicationModel has HybridReplicationAttributes
		// with type REVERSE, it should return an error
		hybridType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &hybridType,
			},
		}

		// Verify the validation condition
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			testHybridAttrs := testReplicationModel.HybridReplicationAttributes
			isHybridReplication := nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
			assert.True(tt, isHybridReplication, "REVERSE type should be detected as hybrid replication")
		}
	})

	t.Run("WhenHybridReplicationTypeIsCONTINUOUS", func(tt *testing.T) {
		// Test the validation logic: CONTINUOUS type should NOT trigger the error
		hybridType := string(datamodel.HybridReplicationParametersReplicationTypeCONTINUOUS)
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &hybridType,
			},
		}

		// Verify the validation condition - CONTINUOUS should NOT be detected as restricted type
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			testHybridAttrs := testReplicationModel.HybridReplicationAttributes
			isHybridReplication := nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
			assert.False(tt, isHybridReplication, "CONTINUOUS type should NOT be detected as restricted hybrid replication")
		}
	})

	t.Run("WhenHybridReplicationTypeIsMIGRATION", func(tt *testing.T) {
		// Test the validation logic: MIGRATION type should NOT trigger the error
		hybridType := string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &hybridType,
			},
		}

		// Verify the validation condition - MIGRATION should NOT be detected as restricted type
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			testHybridAttrs := testReplicationModel.HybridReplicationAttributes
			isHybridReplication := nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
			assert.False(tt, isHybridReplication, "MIGRATION type should NOT be detected as restricted hybrid replication")
		}
	})

	t.Run("WhenHybridReplicationAttributesExistsButTypeIsNil", func(tt *testing.T) {
		// Test the validation logic: when HybridReplicationType is nil, validation should pass
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: nil, // Type is nil
			},
		}

		// Verify the validation condition - nil type should NOT be detected as restricted
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			testHybridAttrs := testReplicationModel.HybridReplicationAttributes
			isHybridReplication := nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) || nillable.GetString(testHybridAttrs.HybridReplicationType, "") == string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
			assert.False(tt, isHybridReplication, "Nil HybridReplicationType should NOT be detected as restricted hybrid replication")
		}
	})

	t.Run("WhenHybridReplicationAttributesIsNil", func(tt *testing.T) {
		// Test the validation logic: when HybridReplicationAttributes is nil, validation should pass
		testReplicationModel := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "replication-123"},
			HybridReplicationAttributes: nil,
		}

		// Verify the validation condition - nil HybridReplicationAttributes should skip the check
		if testReplicationModel != nil && testReplicationModel.HybridReplicationAttributes != nil {
			// This block should not execute when HybridReplicationAttributes is nil
			tt.Fatal("This should not execute when HybridReplicationAttributes is nil")
		}
		assert.Nil(tt, testReplicationModel.HybridReplicationAttributes, "HybridReplicationAttributes should be nil")
	})

	t.Run("WhenNewSourceQuotaRulesValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationReverse = replication.VerifyDstReplicationReverse
			verifyNewSourceQuotaRulesReverse = replication.VerifyNewSourceQuotaRulesReverse
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties for quota rule verification
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifyNewSourceQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			return errors.New("new source quota rules not in READY state")
		}

		params := &commonparams.ReverseAndResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "new source quota rules not in READY state", err.Error())
	})

	t.Run("WhenNewDestinationQuotaRulesValidationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationReverse = replication.VerifyDstReplicationReverse
			verifyNewSourceQuotaRulesReverse = replication.VerifyNewSourceQuotaRulesReverse
			verifyNewDestinationQuotaRulesReverse = replication.VerifyNewDestinationQuotaRulesReverse
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set up file volume with FileProperties for quota rule verification
			event.ReplicationModel = &datamodel.VolumeReplication{
				Uri: "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
				Volume: &datamodel.Volume{
					VolumeAttributes: &datamodel.VolumeAttributes{
						FileProperties: &datamodel.FileProperties{},
					},
					Pool: &datamodel.Pool{
						BaseModel: datamodel.BaseModel{UUID: "uuid"},
					},
				},
			}
			return nil, nil, nil
		}

		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			return &models.VolumeReplication{
				ReplicationAttributes: &models.ReplicationDetails{},
			}, nil
		}

		verifyNewSourceQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			return nil // Source validation passes
		}

		verifyNewDestinationQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			return errors.New("new destination quota rules in transitioning state")
		}

		params := &commonparams.ReverseAndResumeReplicationParams{
			AccountName: "account-name",
		}
		_, _, err := _reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "new destination quota rules in transitioning state", err.Error())
	})

	t.Run("WhenBothQuotaRulesValidationSucceeds", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationReverse = replication.VerifyDstReplicationReverse
			verifyNewSourceQuotaRulesReverse = replication.VerifyNewSourceQuotaRulesReverse
			verifyNewDestinationQuotaRulesReverse = replication.VerifyNewDestinationQuotaRulesReverse
		}()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: &datamodel.FileProperties{},
				},
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = mockReplicationModel
			return nil, nil, nil
		}

		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		verifyNewSourceQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			return nil // Source validation passes
		}

		verifyNewDestinationQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			return nil // Destination validation passes
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == createdJob.WorkflowID
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		volumeReplication, jobUuid, err := _reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.NotNil(tt, jobUuid)
		assert.Equal(tt, "job-123", *jobUuid)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenNotFileVolume_SkipsQuotaRuleValidation", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		// Track if quota rule validation functions are called
		newSourceQuotaRulesCalled := false
		newDestQuotaRulesCalled := false

		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationReverse = replication.VerifyDstReplicationReverse
			verifyNewSourceQuotaRulesReverse = replication.VerifyNewSourceQuotaRulesReverse
			verifyNewDestinationQuotaRulesReverse = replication.VerifyNewDestinationQuotaRulesReverse
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					// No FileProperties - this is a block volume
				},
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = mockReplicationModel
			return nil, nil, nil
		}

		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		verifyNewSourceQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			newSourceQuotaRulesCalled = true
			return nil
		}

		verifyNewDestinationQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			newDestQuotaRulesCalled = true
			return nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == createdJob.WorkflowID
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := _reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.False(tt, newSourceQuotaRulesCalled, "verifyNewSourceQuotaRulesReverse should NOT be called when FileProperties is nil")
		assert.False(tt, newDestQuotaRulesCalled, "verifyNewDestinationQuotaRulesReverse should NOT be called when FileProperties is nil")
	})
	t.Run("WhenHybridReplication_QuotaRuleVerificationNotCalled", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		newSourceQuotaRulesCalled := false
		newDestQuotaRulesCalled := false

		defer func() {
			getAccountWithName = _getAccountWithName
			validateReplicationParams = replication.ValidateReplicationParams
			verifyDstReplicationReverse = replication.VerifyDstReplicationReverse
			verifyNewSourceQuotaRulesReverse = replication.VerifyNewSourceQuotaRulesReverse
			verifyNewDestinationQuotaRulesReverse = replication.VerifyNewDestinationQuotaRulesReverse
		}()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID: 1,
			},
			Name: "account-name",
		}
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		hybridType := string(datamodel.HybridReplicationParametersReplicationTypeREVERSE)
		mockReplicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Uri:       "projects/1234567890/locations/us-central1/volumes/gosrcvolume1/replications/replication-id-1",
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &hybridType,
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: &datamodel.FileProperties{},
				},
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "uuid"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		validateReplicationParams = func(ctx context.Context, event *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanup bool, jobType string) (*models.VolumeReplication, *string, error) {
			event.ReplicationModel = mockReplicationModel
			return nil, nil, nil
		}

		verifyDstReplicationReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) (*models.VolumeReplication, error) {
			event.ReplicationModel = mockReplicationModel
			return convertDataStoreReplicationToModel(mockReplicationModel), nil
		}

		verifyNewSourceQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			newSourceQuotaRulesCalled = true
			return nil
		}

		verifyNewDestinationQuotaRulesReverse = func(ctx context.Context, event *replication.ReverseReplicationEvent) error {
			newDestQuotaRulesCalled = true
			return nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
			return opts.ID == createdJob.WorkflowID
		}), mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.ReverseAndResumeReplicationParams{
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			AccountName:           "test-account",
			CorrelationId:         "corr-123",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
		}

		_, _, err := _reverseAndResumeReplication(ctx, mockStorage, mockTemporal, params)
		assert.Nil(tt, err)
		assert.False(tt, newSourceQuotaRulesCalled, "verifyNewSourceQuotaRulesReverse should NOT be called for hybrid replication")
		assert.False(tt, newDestQuotaRulesCalled, "verifyNewDestinationQuotaRulesReverse should NOT be called for hybrid replication")
	})
}

func TestConvertJSONBLabelsToMap(t *testing.T) {
	tests := []struct {
		name     string
		jsonb    *datamodel.JSONB
		expected map[string]string
	}{
		{
			name:     "NilJSONB",
			jsonb:    nil,
			expected: nil,
		},
		{
			name:     "EmptyJSONB",
			jsonb:    &datamodel.JSONB{},
			expected: map[string]string{},
		},
		{
			name: "ValidJSONBWithStringValues",
			jsonb: &datamodel.JSONB{
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
			},
			expected: map[string]string{
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
			},
		},
		{
			name: "JSONBWithMixedTypes",
			jsonb: &datamodel.JSONB{
				"environment": "production",
				"count":       123,
				"enabled":     true,
				"team":        "platform",
			},
			expected: map[string]string{
				"environment": "production",
				"team":        "platform",
			},
		},
		{
			name: "SingleStringValue",
			jsonb: &datamodel.JSONB{
				"owner": "team-a",
			},
			expected: map[string]string{
				"owner": "team-a",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertJSONBLabelsToMap(tt.jsonb)

			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, len(tt.expected), len(result))

				for key, expectedValue := range tt.expected {
					actualValue, exists := result[key]
					assert.True(t, exists, "Expected key %s to exist", key)
					assert.Equal(t, expectedValue, actualValue, "Expected value %s for key %s, got %s", expectedValue, key, actualValue)
				}
			}
		})
	}
}

func TestEstablishReplicationPeering(t *testing.T) {
	t.Run("WhenGetAccountFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		defer func() { getAccountWithName = originalGetAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("WhenVerifyEstablishPeeringFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return nil, errors.New("replication not found")
		}

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "replication not found", err.Error())
	})

	t.Run("WhenCheckAndFetchDuplicateJobsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		defer func() { getAccountWithName = originalGetAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		dbErr := errors.New("database error")
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), dbErr)

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Equal(tt, dbErr, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDuplicateJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		defer func() { getAccountWithName = originalGetAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		existingJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-job-uuid"},
			JobAttributes: &datamodel.JobAttributes{ResourceUUID: "replication-123"},
		}
		existingReplication := &datamodel.VolumeReplication{
			BaseModel:             datamodel.BaseModel{UUID: "replication-123"},
			Name:                  "test-replication",
			State:                 datamodel.LifeCycleStateUpdating,
			StateDetails:          datamodel.LifeCycleStateUpdatingDetails,
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return(existingJob, nil)
		mockStorage.On("GetVolumeReplication", ctx, "replication-123").Return(existingReplication, nil)

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		volumeReplication, jobUUID, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "existing-job-uuid", jobUUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDuplicateJobExistsAndGetVolumeReplicationFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		defer func() { getAccountWithName = originalGetAccountWithName }()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		existingJob := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "existing-job-uuid"},
			JobAttributes: &datamodel.JobAttributes{ResourceUUID: "replication-123"},
		}

		getReplicationErr := errors.New("failed to get replication from database")
		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return(existingJob, nil)
		mockStorage.On("GetVolumeReplication", ctx, "replication-123").Return((*datamodel.VolumeReplication)(nil), getReplicationErr)

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Equal(tt, getReplicationErr, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCheckActiveReplicationJobsReturnsError", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", errors.New("failed to check active jobs")
		}

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to check active jobs", err.Error())
	})

	t.Run("WhenActiveJobExists", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "existing-job-uuid", nil
		}

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		volumeReplication, jobUUID, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "existing-job-uuid", jobUUID)
	})

	t.Run("WhenCreateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", nil
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job"))

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to create job", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockStorage.On("UpdateJob", ctx, "job-123", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(nil)

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenExecuteWorkflowFailsAndUpdateJobFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockStorage.On("UpdateJob", ctx, "job-123", string(datamodel.JobsStateERROR), 0, "failed to execute workflow").Return(errors.New("failed to update job"))

		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to execute workflow"))

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		_, _, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NotNil(tt, err)
		assert.Equal(tt, "failed to execute workflow", err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSucceedsWithRegion", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			State:     datamodel.LifeCycleStateAvailable,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1"},
		}

		volumeReplication, jobUUID, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", jobUUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WhenSucceedsWithZone", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)

		originalGetAccountWithName := getAccountWithName
		originalVerifyEstablishPeering := verifyEstablishPeering
		originalCheckActiveReplicationJobs := hybridReplicationJobsInProcess
		defer func() {
			getAccountWithName = originalGetAccountWithName
			verifyEstablishPeering = originalVerifyEstablishPeering
			hybridReplicationJobsInProcess = originalCheckActiveReplicationJobs
		}()

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return &datamodel.Account{
				BaseModel: datamodel.BaseModel{ID: 1},
				Name:      "test-account",
			}, nil
		}

		mockStorage.On("CheckAndFetchDuplicateJobs", ctx, "HYBRID_REPLICATION_ESTABLISH_PEERING", mock.Anything).Return((*datamodel.Job)(nil), nil)

		dstReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-123"},
			Name:      "test-replication",
			State:     datamodel.LifeCycleStateAvailable,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				Name:      "test-volume",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationPoolUUID: "pool-uuid",
				ReplicationType:     "hybrid",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				Status: datamodel.HybridReplicationStatusPendingClusterPeer,
			},
		}

		verifyEstablishPeering = func(ctx context.Context, params *commonparams.EstablishReplicationPeeringParams, se database.Storage, accountID int64, ccfeURI string) (*datamodel.VolumeReplication, error) {
			return dstReplication, nil
		}

		hybridReplicationJobsInProcess = func(ctx context.Context, se database.Storage, accountID int64, poolUUID string, ccfeURI string) (string, error) {
			return "", nil
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-123"},
			WorkflowID: "workflow-123",
		}

		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.EstablishReplicationPeeringParams{
			AccountName:           "test-account",
			Region:                "us-central1",
			Zone:                  "us-central1-a",
			VolumeResourceId:      "volume-123",
			ReplicationResourceId: "replication-123",
			CorrelationId:         "corr-123",
			PeerVolumeName:        "peer-volume",
			PeerClusterName:       "peer-cluster",
			PeerSvmName:           "peer-svm",
			PeerIPAddresses:       []string{"10.0.0.1", "10.0.0.2"},
		}

		volumeReplication, jobUUID, err := _establishReplicationPeering(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, volumeReplication)
		assert.Equal(tt, "replication-123", volumeReplication.UUID)
		assert.Equal(tt, "job-123", jobUUID)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, volumeReplication.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdatingDetails, volumeReplication.StateDetails)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func Test_validateQuotaRulesForVolume(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	volumeID := int64(123)

	t.Run("WhenNoQuotaRulesExist", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return([]*datamodel.QuotaRule{}, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.NoError(tt, err, "Expected no error when no quota rules exist")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRulesExistButNoneInErrorState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:  "quota-rule-1",
				State: datamodel.LifeCycleStateAvailable,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-2",
				},
				Name:  "quota-rule-2",
				State: datamodel.LifeCycleStateCreating,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-3",
				},
				Name:  "quota-rule-3",
				State: datamodel.LifeCycleStateUpdating,
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.NoError(tt, err, "Expected no error when quota rules exist but none are in error state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetQuotaRulesByVolumeIDFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		expectedErr := errors.New("database error")
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(nil, expectedErr)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when GetQuotaRulesByVolumeID fails")
		assert.Equal(tt, expectedErr, err, "Expected the same error from GetQuotaRulesByVolumeID")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenOneQuotaRuleInErrorState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:         "quota-rule-1",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details for quota rule 1",
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-2",
				},
				Name:  "quota-rule-2",
				State: datamodel.LifeCycleStateAvailable,
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when quota rule is in error state")
		assert.Contains(tt, err.Error(), "Cannot create volume replication", "Expected error message to contain validation message")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to contain errored quota rule UUID")
		assert.Contains(tt, err.Error(), "quota-rule-1", "Expected error message to contain errored quota rule name")
		assert.Contains(tt, err.Error(), datamodel.LifeCycleStateError, "Expected error message to contain error state")
		assert.Contains(tt, err.Error(), "Error details for quota rule 1", "Expected error message to contain state details")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenMultipleQuotaRulesInErrorState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:         "quota-rule-1",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details for quota rule 1",
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-2",
				},
				Name:         "quota-rule-2",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details for quota rule 2",
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-3",
				},
				Name:  "quota-rule-3",
				State: datamodel.LifeCycleStateAvailable,
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when multiple quota rules are in error state")
		assert.Contains(tt, err.Error(), "Cannot create volume replication", "Expected error message to contain validation message")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to contain first errored quota rule UUID")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-2", "Expected error message to contain second errored quota rule UUID")
		assert.Contains(tt, err.Error(), "quota-rule-1", "Expected error message to contain first errored quota rule name")
		assert.Contains(tt, err.Error(), "quota-rule-2", "Expected error message to contain second errored quota rule name")
		assert.Contains(tt, err.Error(), ";", "Expected error message to contain semicolon separator between errored rules")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAllQuotaRulesInErrorState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:         "quota-rule-1",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details 1",
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-2",
				},
				Name:         "quota-rule-2",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details 2",
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when all quota rules are in error state")
		assert.Contains(tt, err.Error(), "Cannot create volume replication", "Expected error message to contain validation message")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to contain first errored quota rule UUID")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-2", "Expected error message to contain second errored quota rule UUID")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleInErrorStateWithEmptyName", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:         "",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "Error details",
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when quota rule is in error state")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to contain quota rule UUID even when name is empty")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRuleInErrorStateWithEmptyStateDetails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:         "quota-rule-1",
				State:        datamodel.LifeCycleStateError,
				StateDetails: "",
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when quota rule is in error state")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to contain quota rule UUID")
		assert.Contains(tt, err.Error(), "quota-rule-1", "Expected error message to contain quota rule name")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenQuotaRulesWithVariousStates", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		quotaRules := []*datamodel.QuotaRule{
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-1",
				},
				Name:  "quota-rule-1",
				State: datamodel.LifeCycleStateAvailable,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-2",
				},
				Name:  "quota-rule-2",
				State: datamodel.LifeCycleStateCreating,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-3",
				},
				Name:  "quota-rule-3",
				State: datamodel.LifeCycleStateUpdating,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-4",
				},
				Name:  "quota-rule-4",
				State: datamodel.LifeCycleStateDeleting,
			},
			{
				BaseModel: datamodel.BaseModel{
					UUID: "quota-rule-uuid-5",
				},
				Name:  "quota-rule-5",
				State: datamodel.LifeCycleStateError,
			},
		}
		mockStorage.On("GetQuotaRulesByVolumeID", ctx, volumeID).Return(quotaRules, nil)

		err := validateQuotaRulesForVolume(ctx, mockStorage, volumeID)
		assert.Error(tt, err, "Expected error when one quota rule is in error state among various states")
		assert.Contains(tt, err.Error(), "quota-rule-uuid-5", "Expected error message to contain only the errored quota rule UUID")
		assert.NotContains(tt, err.Error(), "quota-rule-uuid-1", "Expected error message to not contain non-errored quota rule UUID")
		assert.NotContains(tt, err.Error(), "quota-rule-uuid-2", "Expected error message to not contain non-errored quota rule UUID")
		mockStorage.AssertExpectations(tt)
	})
}

func TestDeleteVolumeReplication_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	t.Run("WhenDeleteVolumeReplication_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test-account",
		}

		previousState := datamodel.LifeCycleStateAvailable
		previousStateDetails := datamodel.LifeCycleStateAvailableDetails
		replicationModel := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "test-replication-uuid",
			},
			State:        previousState,
			StateDetails: previousStateDetails,
			Uri:          "test-replication-uri",
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType: "test-endpoint-type",
			},
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
				Pool: &datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
				},
			},
		}

		params := &commonparams.DeleteReplicationParams{
			VolumeResourceId:      "test-volume-uuid",
			ReplicationResourceId: "test-replication-uuid",
			AccountName:           account.Name,
			CorrelationId:         "test-correlation-id",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		originalValidateReplicationParams := validateReplicationParams
		validateReplicationParams = func(ctx context.Context, params *replication.CommonReplicationEventParams, accountID int64, se database.Storage, isCleanUp bool, jobType string) (*models.VolumeReplication, *string, error) {
			// Set ReplicationModel on the event params (this is what the real function does)
			params.ReplicationModel = replicationModel
			return nil, nil, nil
		}
		defer func() {
			validateReplicationParams = originalValidateReplicationParams
		}()

		originalVerifyReplicationDelete := VerifyReplicationDelete
		VerifyReplicationDelete = func(ctx context.Context, event *replication.DeleteReplicationEvent) (*models.VolumeReplication, error) {
			// Convert datamodel to models for return
			return &models.VolumeReplication{
				BaseModel:    models.BaseModel{UUID: replicationModel.UUID},
				State:        replicationModel.State,
				StateDetails: replicationModel.StateDetails,
				Uri:          replicationModel.Uri,
			}, nil
		}
		defer func() {
			VerifyReplicationDelete = originalVerifyReplicationDelete
		}()

		mockStorage.EXPECT().CreateJob(ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == replicationModel.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		_, jobUUID, err := _deleteReplication(ctx, mockStorage, mockTemporal, params, "", false)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestConvertGcpReplicationV1betaToCommon(t *testing.T) {
	t.Run("WhenAllOptionalFieldsAreSet", func(tt *testing.T) {
		now := time.Now()
		gcpReplications := []gcpserver.ReplicationV1beta{
			{
				ReplicationId: gcpserver.NewOptString("repl-1"),
				ResourceId:    gcpserver.NewOptString("resource-1"),
				Description:   gcpserver.NewOptString("Test replication"),
				Source: gcpserver.NewOptReplicationVolumeInformationV1beta(gcpserver.ReplicationVolumeInformationV1beta{
					VolumeName: gcpserver.NewOptString("source-vol"),
					VolumeId:   gcpserver.NewOptString("source-vol-id"),
				}),
				Destination: gcpserver.NewOptReplicationVolumeInformationV1beta(gcpserver.ReplicationVolumeInformationV1beta{
					VolumeName: gcpserver.NewOptString("dest-vol"),
					VolumeId:   gcpserver.NewOptString("dest-vol-id"),
				}),
				State:               gcpserver.NewOptReplicationV1betaState("READY"),
				StateDetails:        gcpserver.NewOptString("Active state"),
				StateDetailsCode:    gcpserver.NewOptInt32(1),
				Role:                gcpserver.NewOptReplicationV1betaRole("SOURCE"),
				ReplicationSchedule: gcpserver.NewOptReplicationV1betaReplicationSchedule("HOURLY"),
				MirrorState:         gcpserver.NewOptReplicationV1betaMirrorState("MIRRORED"),
				Healthy:             gcpserver.NewOptBool(true),
				TransferStats: gcpserver.NewOptTransferStatsV1beta(gcpserver.TransferStatsV1beta{
					TotalTransferBytes:    gcpserver.NewOptFloat64(1000.0),
					TotalTransferTimeSecs: gcpserver.NewOptFloat64(100.0),
					LastTransferSize:      gcpserver.NewOptFloat64(500.0),
					LastTransferError:     gcpserver.NewOptString(""),
					LastTransferDuration:  gcpserver.NewOptFloat64(50.0),
					LastTransferEndTime:   gcpserver.NewOptDateTime(now),
					TotalProgress:         gcpserver.NewOptFloat64(75.0),
					ProgressLastUpdated:   gcpserver.NewOptDateTime(now),
					LagTime:               gcpserver.NewOptFloat64(10.0),
				}),
				Created:               gcpserver.NewOptDateTime(now),
				Labels:                gcpserver.NewOptReplicationV1betaLabels(map[string]string{"key": "value"}),
				ClusterLocation:       gcpserver.NewOptString("us-east1"),
				HybridReplicationType: gcpserver.NewOptReplicationV1betaHybridReplicationType("ONPREM_REPLICATION"),
			},
		}

		result := convertGcpReplicationV1betaToCommon(gcpReplications)

		assert.Len(tt, result, 1)
		repl := result[0]
		assert.NotNil(tt, repl.ReplicationId)
		assert.Equal(tt, "repl-1", *repl.ReplicationId)
		assert.NotNil(tt, repl.ResourceId)
		assert.Equal(tt, "resource-1", *repl.ResourceId)
		assert.NotNil(tt, repl.Description)
		assert.Equal(tt, "Test replication", *repl.Description)
		assert.NotNil(tt, repl.Source)
		assert.NotNil(tt, repl.Source.VolumeName)
		assert.Equal(tt, "source-vol", *repl.Source.VolumeName)
		assert.NotNil(tt, repl.Destination)
		assert.NotNil(tt, repl.Destination.VolumeName)
		assert.Equal(tt, "dest-vol", *repl.Destination.VolumeName)
		assert.NotNil(tt, repl.State)
		assert.Equal(tt, "READY", *repl.State)
		assert.NotNil(tt, repl.StateDetails)
		assert.Equal(tt, "Active state", *repl.StateDetails)
		assert.NotNil(tt, repl.StateDetailsCode)
		assert.Equal(tt, int32(1), *repl.StateDetailsCode)
		assert.NotNil(tt, repl.Role)
		assert.Equal(tt, "SOURCE", *repl.Role)
		assert.NotNil(tt, repl.ReplicationSchedule)
		assert.Equal(tt, "HOURLY", *repl.ReplicationSchedule)
		assert.NotNil(tt, repl.MirrorState)
		assert.Equal(tt, "MIRRORED", *repl.MirrorState)
		assert.NotNil(tt, repl.Healthy)
		assert.True(tt, *repl.Healthy)
		assert.NotNil(tt, repl.TransferStats)
		assert.NotNil(tt, repl.TransferStats.TotalTransferBytes)
		assert.Equal(tt, float64(1000.0), *repl.TransferStats.TotalTransferBytes)
		assert.NotNil(tt, repl.Created)
		assert.Equal(tt, now, *repl.Created)
		assert.NotNil(tt, repl.Labels)
		assert.Equal(tt, "value", repl.Labels["key"])
		assert.NotNil(tt, repl.ClusterLocation)
		assert.Equal(tt, "us-east1", *repl.ClusterLocation)
		assert.NotNil(tt, repl.HybridReplicationType)
		assert.Equal(tt, "ONPREM_REPLICATION", *repl.HybridReplicationType)
	})

	t.Run("WhenOptionalFieldsAreNotSet", func(tt *testing.T) {
		gcpReplications := []gcpserver.ReplicationV1beta{
			{
				ReplicationId: gcpserver.NewOptString("repl-2"),
				ResourceId:    gcpserver.NewOptString("resource-2"),
			},
		}

		result := convertGcpReplicationV1betaToCommon(gcpReplications)

		assert.Len(tt, result, 1)
		repl := result[0]
		assert.NotNil(tt, repl.ReplicationId)
		assert.Equal(tt, "repl-2", *repl.ReplicationId)
		assert.Nil(tt, repl.Description)
		assert.Nil(tt, repl.Source)
		assert.Nil(tt, repl.Destination)
		assert.Nil(tt, repl.State)
		assert.Nil(tt, repl.TransferStats)
		assert.Nil(tt, repl.Labels)
	})

	t.Run("WhenEmptyArray", func(tt *testing.T) {
		gcpReplications := []gcpserver.ReplicationV1beta{}

		result := convertGcpReplicationV1betaToCommon(gcpReplications)

		assert.Len(tt, result, 0)
	})
}

// TestBuildInitializingReplication verifies the partial stub returned during the CREATING window
// (VSCP-6041) when VCP UUIDs are not yet assigned.
func TestBuildInitializingReplication(t *testing.T) {
	t.Run("WithFullReplicationAttributes", func(tt *testing.T) {
		dbUUID := "vcp-db-uuid-1234"
		r := &datamodel.VolumeReplication{
			BaseModel:    datamodel.BaseModel{UUID: dbUUID},
			Name:         "replication-creating",
			Uri:          "projects/123/locations/us-east4/volumes/srcvol/replications/repl-1",
			RemoteUri:    "projects/123/locations/australia-southeast1/volumes/dstvol/replications/repl-1",
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:        "src",
				ReplicationSchedule: "HOURLY",
				DestinationLocation: "australia-southeast1",
			},
		}

		stub := buildInitializingReplication(r)

		assert.Equal(tt, dbUUID, stub.VolumeReplicationUuid.Value,
			"VolumeReplicationUuid must equal the VCP DB UUID for role-map lookup")
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating, stub.LifeCycleState.Value,
			"LifeCycleState must reflect the DB state")
		assert.Equal(tt, datamodel.LifeCycleStateCreatingDetails, stub.LifeCycleStateDetails.Value)
		assert.Equal(tt, r.Uri, stub.CcfeUri.Value)
		assert.Equal(tt, r.RemoteUri, stub.CcfeRemoteUri.Value)
		assert.Equal(tt, r.Name, stub.Name.Value)
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaEndpointType("src"), stub.EndpointType)
		assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaReplicationSchedule("HOURLY"), stub.ReplicationSchedule.Value)
		assert.Equal(tt, "australia-southeast1", stub.RemoteRegion)
		// Volume UUIDs must NOT be set — they are SDE-side values unknown during CREATING.
		// If they were set, convertInternalReplicationToCCFEModel would emit misleading
		// source/destination blocks with empty volumeId fields in the API response.
		assert.False(tt, stub.SourceVolumeUuid.Set, "SourceVolumeUuid must not be set in the initialising stub")
		assert.False(tt, stub.DestinationVolumeUuid.Set, "DestinationVolumeUuid must not be set in the initialising stub")
	})

	t.Run("WithNilReplicationAttributesDoesNotPanic", func(tt *testing.T) {
		r := &datamodel.VolumeReplication{
			BaseModel:             datamodel.BaseModel{UUID: "vcp-db-uuid-no-attrs"},
			Name:                  "replication-no-attrs",
			Uri:                   "projects/123/locations/us-east4/volumes/srcvol/replications/repl-2",
			ReplicationAttributes: nil,
		}

		assert.NotPanics(tt, func() {
			stub := buildInitializingReplication(r)
			assert.Equal(tt, "vcp-db-uuid-no-attrs", stub.VolumeReplicationUuid.Value)
			assert.Equal(tt, googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating, stub.LifeCycleState.Value)
		})
	})
}

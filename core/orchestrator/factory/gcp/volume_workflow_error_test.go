package gcp

import (
	"context"
	errors2 "errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"gorm.io/gorm"
)

func TestCreateVolume_JobUpdateOnWorkflowFailure(t *testing.T) {
	t.Run("ShouldMarkJobAsErrorWhenWorkflowFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:        "test-pool",
				AccountID:   1,
				Account:     account,
				SizeInBytes: 1000000000000,
				Network:     "test-network",
				State:       datamodel.LifeCycleStateREADY,
				VendorID:    "/projects/test-project/locations/us-west1/pools/test-pool",
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-west1-a",
				},
				APIAccessMode: common.DEFAULTMode,
			},
			QuotaInBytes: 0,
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.CreateVolumeParams{
			Name:         "test-volume",
			AccountName:  "test-account",
			Zone:         "us-west1-a",
			VendorID:     "/projects/test-project/locations/us-west1-a/volumes/test-volume", // Valid VendorID
			PoolID:       "pool-uuid",
			QuotaInBytes: 100000000000,
			Network:      "test-network",
			Protocols:    []string{"iscsi"},
		}

		// Mock successful setup calls
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateCreateVolumeParams = func(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, pool *datamodel.PoolView) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateVolumeParams = _validateCreateVolumeParams
		}()

		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(pool, nil)
		mockStorage.On("GetVolumeByNameAccountIDAndZone", ctx, params.Name, account.ID, pool.PoolAttributes.PrimaryZone, pool.PoolAttributes.IsRegionalHA).Return(nil, errors.New("volume not found"))
		mockStorage.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)
		mockStorage.On("CreateVolume", ctx, mock.AnythingOfType("*datamodel.Volume")).Return(&datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			AccountID: account.ID,
			Pool:      &pool.Pool,
			PoolID:    pool.ID,
		}, nil)
		mockStorage.On("DeleteVolume", ctx, "volume-uuid").Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to mark job as error
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil).Once()

		// Execute test
		_, _, err := _createVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

func TestRevertVolume_JobUpdateOnWorkflowFailure(t *testing.T) {
	t.Run("ShouldMarkJobAsErrorWhenWorkflowFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		volume := &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: "volume-uuid"},
			Name:         "test-volume",
			AccountID:    1,
			Account:      account,
			Pool:         pool,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.RevertVolumeParams{
			AccountName: "test-account",
			VolumeID:    "volume-uuid",
			SnapshotID:  "snapshot-uuid",
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			getAccountWithName = _getAccountWithName
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolumeWithAccountID", ctx, params.VolumeID, account.ID).Return(volume, nil)
		mockStorage.On("GetSnapshotByUUID", ctx, params.SnapshotID, volume.Account.ID, volume.ID).Return(snapshot, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock DeleteJob call to delete job on error
		mockStorage.On("DeleteJob", ctx, job.UUID, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to revert volume back to READY state
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateREADY,
			"state_details": datamodel.LifeCycleStateAvailableDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _revertVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

func TestDeleteVolume_JobUpdateOnWorkflowFailure(t *testing.T) {
	t.Run("ShouldMarkJobAsErrorWhenWorkflowFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool:      pool,
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		validateDeleteVolumeParams = func(ctx context.Context, se database.Storage, volume *datamodel.Volume) error {
			return nil
		}
		defer func() {
			validateDeleteVolumeParams = _validateDeleteVolumeParams
		}()

		mockStorage.On("GetVolume", ctx, "volume-uuid").Return(volume, nil)
		// Mock GetJobByResourceUUID to return nil (no existing job) since code checks for existing jobs
		mockStorage.On("GetJobByResourceUUID", ctx, "volume-uuid", string(datamodel.JobTypeDeleteVolume)).Return(nil, errors.New("Job not found"))
		mockStorage.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateDeleting,
			"state_details": datamodel.LifeCycleStateDeletingDetails,
		}).Return(nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to mark job as error
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to mark volume as error
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateDeletionErrorDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _deleteVolume(ctx, mockStorage, mockTemporal, "volume-uuid")

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateVolume_JobUpdateOnWorkflowFailure(t *testing.T) {
	t.Run("ShouldMarkJobAsErrorWhenWorkflowFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:             "test-pool",
				AccountID:        1,
				Account:          account,
				SizeInBytes:      1000000000000,
				AllowAutoTiering: false,
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-west1-a",
				},
			},
			QuotaInBytes: 0,
		}

		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Name:             "test-volume",
			AccountID:        1,
			Account:          account,
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      100000000000,
			VolumeAttributes: &datamodel.VolumeAttributes{},
			Pool:             database.ConvertPoolViewToPool(pool),
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.UpdateVolumeParams{
			VolumeId:     "volume-uuid",
			PoolID:       "pool-uuid",
			QuotaInBytes: 200000000000,
		}

		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolume", ctx, params.VolumeId).Return(volume, nil)
		mockStorage.On("GetPool", ctx, "pool-uuid", volume.AccountID).Return(pool, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr)

		// Mock UpdateJob call to mark job as error
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to mark volume as error
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateUpdateErrorDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _updateVolume(ctx, mockStorage, mockTemporal, params, false)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

// Test for line 253: Failed to update volume state to DELETED during createVolume
func TestCreateVolume_FailedVolumeDeleteOnError(t *testing.T) {
	t.Run("ShouldLogErrorWhenVolumeDeleteFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:        "test-pool",
				AccountID:   1,
				Account:     account,
				SizeInBytes: 1000000000000,
				Network:     "test-network",
				State:       datamodel.LifeCycleStateREADY,
				VendorID:    "/projects/test-project/locations/us-west1/pools/test-pool",
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-west1-a",
				},
				APIAccessMode: common.DEFAULTMode,
			},
			QuotaInBytes: 0,
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.CreateVolumeParams{
			Name:         "test-volume",
			AccountName:  "test-account",
			Zone:         "us-west1-a",
			VendorID:     "/projects/test-project/locations/us-west1-a/volumes/test-volume", // Valid VendorID
			PoolID:       "pool-uuid",
			QuotaInBytes: 100000000000,
			Network:      "test-network",
			Protocols:    []string{"iscsi"},
		}

		// Mock successful setup calls
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateCreateVolumeParams = func(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, pool *datamodel.PoolView) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateVolumeParams = _validateCreateVolumeParams
		}()

		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(pool, nil)
		mockStorage.On("GetVolumeByNameAccountIDAndZone", ctx, params.Name, account.ID, pool.PoolAttributes.PrimaryZone, pool.PoolAttributes.IsRegionalHA).Return(nil, gorm.ErrRecordNotFound)
		mockStorage.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)
		mockStorage.On("CreateVolume", ctx, mock.AnythingOfType("*datamodel.Volume")).Return(&datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			AccountID: account.ID,
			Pool:      &pool.Pool,
			PoolID:    pool.ID,
		}, nil)
		mockStorage.On("DeleteVolume", ctx, "volume-uuid").Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to mark job as error
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil)

		// Execute test
		_, _, err := _createVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 284: Failed to update job state to ERROR during createVolume
func TestCreateVolume_FailedJobUpdateOnError(t *testing.T) {
	t.Run("ShouldLogErrorWhenJobUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:   datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:        "test-pool",
				AccountID:   1,
				Account:     account,
				SizeInBytes: 1000000000000,
				Network:     "test-network",
				State:       datamodel.LifeCycleStateREADY,
				VendorID:    "/projects/test-project/locations/us-west1/pools/test-pool",
				PoolAttributes: &datamodel.PoolAttributes{
					PrimaryZone: "us-west1-a",
				},
				APIAccessMode: common.DEFAULTMode,
			},
			QuotaInBytes: 0,
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.CreateVolumeParams{
			Name:         "test-volume",
			AccountName:  "test-account",
			Zone:         "us-west1-a",
			VendorID:     "/projects/test-project/locations/us-west1-a/volumes/test-volume", // Valid VendorID
			PoolID:       "pool-uuid",
			QuotaInBytes: 100000000000,
			Network:      "test-network",
			Protocols:    []string{"iscsi"},
		}

		// Mock successful setup calls
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		validateCreateVolumeParams = func(ctx context.Context, se database.Storage, params *common.CreateVolumeParams, pool *datamodel.PoolView) error {
			return nil
		}
		defer func() {
			getOrCreateAccount = _getOrCreateAccount
			validateCreateVolumeParams = _validateCreateVolumeParams
		}()

		mockStorage.On("GetPool", ctx, params.PoolID, account.ID).Return(pool, nil)
		mockStorage.On("GetVolumeByNameAccountIDAndZone", ctx, params.Name, account.ID, pool.PoolAttributes.PrimaryZone, pool.PoolAttributes.IsRegionalHA).Return(nil, gorm.ErrRecordNotFound)
		mockStorage.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)
		mockStorage.On("CreateVolume", ctx, mock.AnythingOfType("*datamodel.Volume")).Return(&datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			AccountID: account.ID,
			Pool:      &pool.Pool,
			PoolID:    pool.ID,
		}, nil)
		mockStorage.On("DeleteVolume", ctx, "volume-uuid").Return(&datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to fail - this is what triggers line 480
		jobUpdateErr := errors.New("failed to update job")
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(jobUpdateErr)

		// Execute test
		_, _, err := _createVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 376: Failed to update job state to ERROR during revertVolume
func TestRevertVolume_FailedJobUpdateOnError_Line376(t *testing.T) {
	t.Run("ShouldLogErrorWhenJobUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		volume := &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: "volume-uuid"},
			Name:         "test-volume",
			AccountID:    1,
			Account:      account,
			Pool:         pool,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.RevertVolumeParams{
			AccountName: "test-account",
			VolumeID:    "volume-uuid",
			SnapshotID:  "snapshot-uuid",
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			getAccountWithName = _getAccountWithName
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolumeWithAccountID", ctx, params.VolumeID, account.ID).Return(volume, nil)
		mockStorage.On("GetSnapshotByUUID", ctx, params.SnapshotID, volume.Account.ID, volume.ID).Return(snapshot, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock DeleteJob call to fail - this tests error handling when DeleteJob fails
		jobDeleteErr := errors.New("failed to delete job")
		mockStorage.On("DeleteJob", ctx, job.UUID, workflowErr.Error()).Return(jobDeleteErr)

		// Mock UpdateVolumeFields call to succeed
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateREADY,
			"state_details": datamodel.LifeCycleStateAvailableDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _revertVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 396: Failed to update volume state back to READY during revertVolume
func TestRevertVolume_FailedVolumeUpdateBackToReady(t *testing.T) {
	t.Run("ShouldLogErrorWhenVolumeUpdateBackToReadyFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		volume := &datamodel.Volume{
			BaseModel:    datamodel.BaseModel{UUID: "volume-uuid"},
			Name:         "test-volume",
			AccountID:    1,
			Account:      account,
			Pool:         pool,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid"},
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.RevertVolumeParams{
			AccountName: "test-account",
			VolumeID:    "volume-uuid",
			SnapshotID:  "snapshot-uuid",
		}

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			getAccountWithName = _getAccountWithName
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolumeWithAccountID", ctx, params.VolumeID, account.ID).Return(volume, nil)
		mockStorage.On("GetSnapshotByUUID", ctx, params.SnapshotID, volume.Account.ID, volume.ID).Return(snapshot, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock DeleteJob call to succeed
		mockStorage.On("DeleteJob", ctx, job.UUID, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to fail - this tests error handling when volume update fails
		volumeUpdateErr := errors.New("failed to update volume fields")
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateREADY,
			"state_details": datamodel.LifeCycleStateAvailableDetails,
		}).Return(volumeUpdateErr)

		// Execute test
		_, _, err := _revertVolume(ctx, mockStorage, mockTemporal, params)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 985: Failed to update job state to ERROR during deleteVolume
func TestDeleteVolume_FailedJobUpdateOnError_Line985(t *testing.T) {
	t.Run("ShouldLogErrorWhenJobUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool:      pool,
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		validateDeleteVolumeParams = func(ctx context.Context, se database.Storage, volume *datamodel.Volume) error {
			return nil
		}
		defer func() {
			validateDeleteVolumeParams = _validateDeleteVolumeParams
		}()

		mockStorage.On("GetVolume", ctx, "volume-uuid").Return(volume, nil)
		mockStorage.On("GetJobByResourceUUID", ctx, "volume-uuid", mock.Anything).Return(nil, nil)
		mockStorage.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateDeleting,
			"state_details": datamodel.LifeCycleStateDeletingDetails,
		}).Return(nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to fail - this is what triggers line 985
		jobUpdateErr := errors.New("failed to update job")
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(jobUpdateErr)

		// Mock UpdateVolumeFields call to succeed for error state
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateDeletionErrorDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _deleteVolume(ctx, mockStorage, mockTemporal, "volume-uuid")

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 1014: Failed to update volume state to ERROR during deleteVolume
func TestDeleteVolume_FailedVolumeUpdateOnError_Line1014(t *testing.T) {
	t.Run("ShouldLogErrorWhenVolumeUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
			VendorID:  "/projects/test-project/locations/us-west1/pools/test-pool",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool:      pool,
			State:     datamodel.LifeCycleStateREADY,
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		validateDeleteVolumeParams = func(ctx context.Context, se database.Storage, volume *datamodel.Volume) error {
			return nil
		}
		defer func() {
			validateDeleteVolumeParams = _validateDeleteVolumeParams
		}()

		mockStorage.On("GetVolume", ctx, "volume-uuid").Return(volume, nil)
		// Mock GetJobByResourceUUID for DELETE_VOLUME (called for non-transitional states)
		mockStorage.On("GetJobByResourceUUID", ctx, volume.UUID, string(datamodel.JobTypeDeleteVolume)).Return(nil, nil)
		mockStorage.On("GetJobsWithCondition", mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateDeleting,
			"state_details": datamodel.LifeCycleStateDeletingDetails,
		}).Return(nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return workflowErr
		}
		defer func() {
			workflows.ExecuteWorkflowSeq = workflows.ExecuteWorkflowSequentially
		}()

		// Mock UpdateJob call to succeed
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to fail - this is what triggers line 1014
		volumeUpdateErr := errors.New("failed to update volume fields")
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateDeletionErrorDetails,
		}).Return(volumeUpdateErr)

		// Execute test
		_, _, err := _deleteVolume(ctx, mockStorage, mockTemporal, "volume-uuid")

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
	})
}

// Test for line 1163: Failed to update job state to ERROR during updateVolume
func TestUpdateVolume_FailedJobUpdateOnError_Line1163(t *testing.T) {
	t.Run("ShouldLogErrorWhenJobUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:             "test-pool",
				AccountID:        1,
				Account:          account,
				SizeInBytes:      1000000000000,
				AllowAutoTiering: false,
			},
			QuotaInBytes: 0,
		}

		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Name:             "test-volume",
			AccountID:        1,
			Account:          account,
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      100000000000,
			VolumeAttributes: &datamodel.VolumeAttributes{},
			Pool:             database.ConvertPoolViewToPool(pool),
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.UpdateVolumeParams{
			VolumeId:     "volume-uuid",
			PoolID:       "pool-uuid",
			QuotaInBytes: 200000000000,
		}

		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolume", ctx, params.VolumeId).Return(volume, nil)
		mockStorage.On("GetPool", ctx, params.PoolID, volume.AccountID).Return(pool, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr)

		// Mock UpdateJob call to fail - this is what triggers line 1163
		jobUpdateErr := errors.New("failed to update job")
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(jobUpdateErr)

		// Mock UpdateVolumeFields call to succeed
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateUpdateErrorDetails,
		}).Return(nil)

		// Execute test
		_, _, err := _updateVolume(ctx, mockStorage, mockTemporal, params, false)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

// TestExtractONTAPErrorDetails_NilCustomError covers line 3505: nil customErr returns empty strings.
func TestExtractONTAPErrorDetails_NilCustomError(t *testing.T) {
	msg, code := extractONTAPErrorDetails(nil)
	assert.Equal(t, "", msg)
	assert.Equal(t, "", code)
}

// TestExtractONTAPErrorDetails_NilOriginalErr covers line 3505: customErr with nil OriginalErr returns empty strings.
func TestExtractONTAPErrorDetails_NilOriginalErr(t *testing.T) {
	customErr := &vsaerrors.CustomError{TrackingID: vsaerrors.ErrSplitCloneJobFailed}
	msg, code := extractONTAPErrorDetails(customErr)
	assert.Equal(t, "", msg)
	assert.Equal(t, "", code)
}

// TestExtractONTAPErrorDetails_ValidJSON covers lines 3513,3519,3523: OriginalErr whose Error()
// contains a valid JSON payload with an ONTAP error object. The raw string has no '{' before
// the JSON payload so the JSON-start detection works correctly.
func TestExtractONTAPErrorDetails_ValidJSON(t *testing.T) {
	rawMsg := `[PATCH /storage/volumes/abc123][500] volume_modify default {"error":{"code":"460765","message":"job killed by administrator"}}`
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrSplitCloneJobFailed,
		OriginalErr: fmt.Errorf("%s", rawMsg),
	}
	msg, code := extractONTAPErrorDetails(customErr)
	assert.Equal(t, "job killed by administrator", msg)
	assert.Equal(t, "460765", code)
}

// TestExtractONTAPErrorDetails_InvalidJSON covers lines 3519,3521: OriginalErr whose Error()
// starts with '{' but is not parseable JSON — falls back to the raw string.
func TestExtractONTAPErrorDetails_InvalidJSON(t *testing.T) {
	rawMsg := `{not valid json}`
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrSplitCloneJobFailed,
		OriginalErr: fmt.Errorf("%s", rawMsg),
	}
	msg, code := extractONTAPErrorDetails(customErr)
	assert.Equal(t, rawMsg, msg)
	assert.Equal(t, "", code)
}

// TestExtractONTAPErrorDetails_EmptyErrorMessage covers lines 3519,3521: JSON parses but
// payload.Error.Message is empty — falls back to the raw string.
func TestExtractONTAPErrorDetails_EmptyErrorMessage(t *testing.T) {
	rawMsg := `{"error":{"code":"460765","message":""}}`
	customErr := &vsaerrors.CustomError{
		TrackingID:  vsaerrors.ErrSplitCloneJobFailed,
		OriginalErr: fmt.Errorf("%s", rawMsg),
	}
	msg, code := extractONTAPErrorDetails(customErr)
	assert.Equal(t, rawMsg, msg)
	assert.Equal(t, "", code)
}

// Test for line 1185: Failed to update volume state to ERROR during updateVolume
func TestUpdateVolume_FailedVolumeUpdateOnError_Line1185(t *testing.T) {
	t.Run("ShouldLogErrorWhenVolumeUpdateFailsDuringErrorHandling", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				Name:             "test-pool",
				AccountID:        1,
				Account:          account,
				SizeInBytes:      1000000000000,
				AllowAutoTiering: false,
			},
			QuotaInBytes: 0,
		}

		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Name:             "test-volume",
			AccountID:        1,
			Account:          account,
			State:            datamodel.LifeCycleStateREADY,
			SizeInBytes:      100000000000,
			VolumeAttributes: &datamodel.VolumeAttributes{},
			Pool:             database.ConvertPoolViewToPool(pool),
		}

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "job-uuid",
		}

		params := &common.UpdateVolumeParams{
			VolumeId:     "volume-uuid",
			PoolID:       "pool-uuid",
			QuotaInBytes: 200000000000,
		}

		updateVolumeStatus = func(ctx context.Context, se database.Storage, dbVolume *datamodel.Volume, state string, stateDetails string) (*datamodel.Volume, error) {
			volume.State = state
			volume.StateDetails = stateDetails
			return volume, nil
		}
		defer func() {
			updateVolumeStatus = _updateVolumeStatus
		}()

		mockStorage.On("GetVolume", ctx, params.VolumeId).Return(volume, nil)
		mockStorage.On("GetPool", ctx, params.PoolID, volume.AccountID).Return(pool, nil)
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)

		// Mock workflow failure
		workflowErr := errors.New("workflow execution failed")
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr)

		// Mock UpdateJob call to succeed
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, workflowErr.Error()).Return(nil)

		// Mock UpdateVolumeFields call to fail - this is what triggers line 1185
		volumeUpdateErr := errors.New("failed to update volume fields")
		mockStorage.On("UpdateVolumeFields", ctx, volume.UUID, map[string]interface{}{
			"state":         datamodel.LifeCycleStateError,
			"state_details": datamodel.LifeCycleStateUpdateErrorDetails,
		}).Return(volumeUpdateErr)

		// Execute test
		_, _, err := _updateVolume(ctx, mockStorage, mockTemporal, params, false)

		// Assertions
		assert.Error(tt, err)
		assert.Equal(tt, workflowErr.Error(), err.Error())
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

// setupSplitStartVolumeBase wires up the common mocks needed to drive _splitStartVolume
// up to (and including) the second defer registration, then triggers an ONTAP-range error
// from GetNodesByPoolID so that the clone-state-update defer path fires.
// It returns the mock storage, a volume that has CloneParentInfo (so the defer runs),
// a created-job UUID, and the ONTAP error that will be returned.
func setupSplitStartVolumeBase(t *testing.T, ctx context.Context) (mockStorage *database.MockStorage, vol *datamodel.Volume, jobUUID string, ontapErr error) {
	t.Helper()

	mockStorage = database.NewMockStorage(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
	}
	poolView := &datamodel.PoolView{
		Pool:         *pool,
		QuotaInBytes: 0,
	}
	vol = &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}
	jobUUID = job.UUID

	// Mock function vars so the test controls account lookup, param validation, and clone-state update.
	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error {
		return nil
	}
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStartVolumeParams = origValidate
		updateCloneState = origUpdateCloneState
	})

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	// Reserve clones_shared_bytes to 0 after clone-state update.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)

	// ONTAP-range error from GetNodesByPoolID causes isOntapErr = true in the defer.
	ontapErr = vsaerrors.NewVCPError(vsaerrors.ErrSplitCloneJobFailed, fmt.Errorf("ontap backend error"))
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return(nil, ontapErr)

	// First defer deletes the job when an error occurs.
	mockStorage.On("DeleteJob", mock.Anything, job.UUID, mock.AnythingOfType("string")).Return(nil)

	// Second defer reverts clones_shared_bytes back to the original value since split was never initiated.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": vol.ClonesSharedBytes,
	}).Return(nil)

	return mockStorage, vol, jobUUID, ontapErr
}

// TestSplitStartVolume_DeferGetVolumeFails covers line 3715: when GetVolume for the clone-state
// update in the defer returns an error, the function logs the failure and proceeds without panic.
func TestSplitStartVolume_DeferGetVolumeFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage, vol, _, ontapErr := setupSplitStartVolumeBase(t, ctx)

	// GetVolume in the defer fails → line 3715 is hit.
	fetchErr := errors.New("db unavailable")
	mockStorage.On("GetVolume", mock.Anything, vol.UUID).Return(nil, fetchErr)

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, nil, params)
	assert.Error(t, err)
	// The returned error is the ONTAP error that triggered the defer.
	assert.Equal(t, ontapErr.Error(), err.Error())
	mockStorage.AssertExpectations(t)
}

// TestSplitStartVolume_WorkflowExecutionFails_MockBased covers lines 3803-3807 and 3815:
// GetNodesByPoolID succeeds, GetProviderByNode succeeds, InitiateSplitVolume succeeds,
// but ExecuteWorkflow returns an error, causing _splitStartVolume to return that error.
func TestSplitStartVolume_WorkflowExecutionFails_MockBased(t *testing.T) {
	withFastWorkflowRetry(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage := database.NewMockStorage(t)
	mockTemporalClient := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
	}
	poolView := &datamodel.PoolView{
		Pool:         *pool,
		QuotaInBytes: 0,
	}
	dbNode := &datamodel.Node{
		BaseModel:       datamodel.BaseModel{UUID: "node-uuid"},
		PoolID:          pool.ID,
		Name:            "node-host",
		EndpointAddress: "10.0.0.1",
	}
	vol := &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error {
		return nil
	}
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStartVolumeParams = origValidate
		updateCloneState = origUpdateCloneState
	})

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("InitiateSplitVolume", "ext-uuid").Return("ontap-job-uuid", nil)

	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = origGetProviderByNode })

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	workflowErr := errors.New("temporal unavailable")
	mockTemporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, workflowErr).Maybe()

	// Defer: job deleted; split was initiated so clones_shared_bytes stays 0.
	// Non-ONTAP error after split initiated → clone state stays SPLITTING (no further UpdateVolumeFields).
	// SplitJobUUID persist only runs inside the WAIT_FOR_TEMPORAL goroutine, which is disabled here.
	mockStorage.On("DeleteJob", mock.Anything, job.UUID, mock.AnythingOfType("string")).Return(nil)

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "temporal unavailable")
	mockProvider.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
}

// TestSplitStartVolume_DeferCloneStateUpdateFails covers line 3725: when GetVolume in the
// defer succeeds but UpdateVolumeFields for the clone-state update returns an error, the
// function logs the failure without affecting the returned error.
func TestSplitStartVolume_DeferCloneStateUpdateFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage, vol, _, ontapErr := setupSplitStartVolumeBase(t, ctx)

	// GetVolume in the defer succeeds and returns a volume with CloneParentInfo.
	currentVol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: vol.UUID},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	mockStorage.On("GetVolume", mock.Anything, vol.UUID).Return(currentVol, nil)

	// UpdateVolumeFields for the clone-state update fails → line 3725 is hit.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(errors.New("clone state update failed"))

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, nil, params)
	assert.Error(t, err)
	// The returned error is still the ONTAP error; the clone-state update failure is only logged.
	assert.Equal(t, ontapErr.Error(), err.Error())
	mockStorage.AssertExpectations(t)
}

// TestSplitStartVolume_Success covers line 3816: the happy-path return after
// ExecuteWorkflow succeeds, ensuring the function returns a non-nil Volume and
// a non-empty job UUID with no error.
func TestSplitStartVolume_Success(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage := database.NewMockStorage(t)
	mockTemporalClient := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-west1-a",
		},
	}
	poolView := &datamodel.PoolView{
		Pool:         *pool,
		QuotaInBytes: 0,
	}
	dbNode := &datamodel.Node{
		BaseModel:       datamodel.BaseModel{UUID: "node-uuid"},
		PoolID:          pool.ID,
		Name:            "node-host",
		EndpointAddress: "10.0.0.1",
	}
	vol := &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Account:           account,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error {
		return nil
	}
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStartVolumeParams = origValidate
		updateCloneState = origUpdateCloneState
	})

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("InitiateSplitVolume", "ext-uuid").Return("ontap-job-uuid", nil)

	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = origGetProviderByNode })

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	// ExecuteWorkflow succeeds — this drives the function to the happy-path return.
	// SplitJobUUID persist only runs inside the WAIT_FOR_TEMPORAL goroutine (when ExecuteWorkflow
	// fails), so no volume_attributes UpdateVolumeFields call is expected here.
	mockTemporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	resultVol, jobUUID, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	assert.NoError(t, err)
	assert.NotNil(t, resultVol)
	assert.Equal(t, job.UUID, jobUUID)
	mockProvider.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// TestSplitStartVolume_DeferRevertClonesSharedBytesFails covers line 3764:
// when splitInitiated=false and the UpdateVolumeFields call that reverts
// clones_shared_bytes returns an error, the function logs the failure without
// panicking and still propagates the original error.
// ---------------------------------------------------------------------------

func TestSplitStartVolume_DeferRevertClonesSharedBytesFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage := database.NewMockStorage(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
	}
	poolView := &datamodel.PoolView{
		Pool:         *pool,
		QuotaInBytes: 0,
	}
	// CloneParentInfo.State is deliberately empty so that previousCloneState="" and the
	// clone-state revert branch in the defer (line 3770) is skipped. This lets the test
	// focus exclusively on the revert-clones_shared_bytes failure path (line 3764).
	vol := &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            "",
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error {
		return nil
	}
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStartVolumeParams = origValidate
		updateCloneState = origUpdateCloneState
	})

	triggerErr := errors.New("nodes not found")

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	// Reserve clones_shared_bytes to 0 — succeeds.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)
	// GetNodesByPoolID fails, triggering the defer with splitInitiated=false.
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return(nil, triggerErr)
	// Job cleanup defer.
	mockStorage.On("DeleteJob", mock.Anything, job.UUID, mock.AnythingOfType("string")).Return(nil)
	// Defer revert of clones_shared_bytes fails — this is the line-3764 path.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": vol.ClonesSharedBytes,
	}).Return(errors.New("db write error"))

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, nil, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nodes not found")
	mockStorage.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// setupSplitAfterInitiatedBase sets up mocks for the scenario where
// InitiateSplitVolume succeeds (splitInitiated=true) and ExecuteWorkflow
// returns an ONTAP-range error, driving the defer's error-in-splitting path.
// ---------------------------------------------------------------------------

func setupSplitAfterInitiatedBase(t *testing.T, ctx context.Context) (mockStorage *database.MockStorage, mockTemporalClient *workflowEngineMock.MockTemporalTestClient, vol *datamodel.Volume, ontapErr error) {
	t.Helper()

	mockStorage = database.NewMockStorage(t)
	mockTemporalClient = workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
	}
	poolView := &datamodel.PoolView{
		Pool:         *pool,
		QuotaInBytes: 0,
	}
	dbNode := &datamodel.Node{
		BaseModel:       datamodel.BaseModel{UUID: "node-uuid"},
		PoolID:          pool.ID,
		Name:            "node-host",
		EndpointAddress: "10.0.0.1",
	}
	vol = &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error {
		return nil
	}
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStartVolumeParams = origValidate
		updateCloneState = origUpdateCloneState
	})

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("InitiateSplitVolume", "ext-uuid").Return("ontap-job-uuid", nil)

	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() {
		vsa.GetProviderByNode = origGetProviderByNode
		mockProvider.AssertExpectations(t)
	})

	// An ONTAP-range error returned by ExecuteWorkflow drives isOntapErr=true in the defer.
	ontapErr = vsaerrors.NewVCPError(vsaerrors.ErrSplitCloneJobFailed, fmt.Errorf("ontap polling error"))

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)
	mockStorage.On("DeleteJob", mock.Anything, job.UUID, mock.AnythingOfType("string")).Return(nil)

	mockTemporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, ontapErr).Maybe()

	return mockStorage, mockTemporalClient, vol, ontapErr
}

// ---------------------------------------------------------------------------
// TestSplitStartVolume_OntapErrorAfterSplit_GetVolumeFails covers lines
// 3803-3807: when splitInitiated=true and an ONTAP error occurs, the defer
// attempts to mark the clone as ERROR_IN_SPLITTING but GetVolume fails,
// causing the error path on line 3811 to be taken.
// ---------------------------------------------------------------------------

func TestSplitStartVolume_OntapErrorAfterSplit_GetVolumeFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage, mockTemporalClient, vol, _ := setupSplitAfterInitiatedBase(t, ctx)

	// GetVolume in the defer fails — exercises the 3811 logger.Errorf path.
	mockStorage.On("GetVolume", mock.Anything, vol.UUID).Return(nil, errors.New("db unavailable"))

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Clone split failed in the backend")
	mockStorage.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// TestSplitStartVolume_OntapErrorAfterSplit_StateUpdated covers lines
// 3803-3807 and 3815: when splitInitiated=true and an ONTAP error occurs,
// the defer successfully fetches the current volume and sets clone state to
// ERROR_IN_SPLITTING (line 3815) without any further failures.
// ---------------------------------------------------------------------------

func TestSplitStartVolume_OntapErrorAfterSplit_StateUpdated(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage, mockTemporalClient, vol, _ := setupSplitAfterInitiatedBase(t, ctx)

	currentVol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: vol.UUID},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateSplitting,
			},
		},
	}
	mockStorage.On("GetVolume", mock.Anything, vol.UUID).Return(currentVol, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(nil)

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Clone split failed in the backend")
	mockStorage.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// TestSplitStartVolume_OntapErrorAfterSplit_StateUpdateFails covers lines
// 3803-3807, 3815, and 3821: when splitInitiated=true and an ONTAP error
// occurs, the defer fetches the volume successfully but the UpdateVolumeFields
// call for the clone-state update returns an error (line 3821).
// ---------------------------------------------------------------------------

func TestSplitStartVolume_OntapErrorAfterSplit_StateUpdateFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	mockStorage, mockTemporalClient, vol, _ := setupSplitAfterInitiatedBase(t, ctx)

	currentVol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: vol.UUID},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateSplitting,
			},
		},
	}
	mockStorage.On("GetVolume", mock.Anything, vol.UUID).Return(currentVol, nil)
	// The UpdateVolumeFields for volume_attributes fails — line 3821.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(errors.New("clone state write error"))

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	_, _, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	assert.Error(t, err)
	// The function still returns the original ONTAP error; the update failure is only logged.
	assert.Contains(t, err.Error(), "Clone split failed in the backend")
	mockStorage.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// TestSplitStartVolume_WaitForTemporal_AllRetriesExhausted_DeleteJobFails
// covers lines 3939-3941 in volume.go:
//
// When splitWaitForTemporalEnabled=true and ExecuteWorkflow fails, the
// background goroutine retries UpdateJob up to waitForTemporalUpdateMaxRetries
// times. When all retries are exhausted, the goroutine logs the exhaustion
// (line 3939) and calls DeleteJob (line 3940). If DeleteJob also fails,
// the error is logged (line 3941) and the goroutine exits silently.
//
// A sync.WaitGroup is used to block the test until the goroutine completes,
// which it does once DeleteJob is called (the last statement in the goroutine).
// ---------------------------------------------------------------------------

func TestSplitStartVolume_WaitForTemporal_AllRetriesExhausted_DeleteJobFails(t *testing.T) {
	withFastWorkflowRetry(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool := &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-west1-a",
		},
	}
	poolView := &datamodel.PoolView{Pool: *pool, QuotaInBytes: 0}
	dbNode := &datamodel.Node{
		BaseModel:       datamodel.BaseModel{UUID: "node-uuid"},
		PoolID:          pool.ID,
		Name:            "node-host",
		EndpointAddress: "10.0.0.1",
	}
	vol := &datamodel.Volume{
		BaseModel:         datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:              "test-volume",
		AccountID:         1,
		Account:           account,
		Pool:              pool,
		PoolID:            pool.ID,
		State:             datamodel.LifeCycleStateREADY,
		ClonesSharedBytes: 500,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned,
			},
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "job-uuid",
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = origGetAccountWithName }()

	origValidate := validateSplitStartVolumeParams
	validateSplitStartVolumeParams = func(_ context.Context, _ database.Storage, _ *datamodel.Volume, _ *datamodel.PoolView) error {
		return nil
	}
	defer func() { validateSplitStartVolumeParams = origValidate }()

	origUpdateCloneState := updateCloneState
	updateCloneState = func(_ context.Context, _ database.Storage, _ string, _ string) error { return nil }
	defer func() { updateCloneState = origUpdateCloneState }()

	mockProvider := new(vsa.MockProvider)
	mockProvider.On("InitiateSplitVolume", "ext-uuid").Return("ontap-job-uuid", nil)

	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	defer func() { vsa.GetProviderByNode = origGetProviderByNode }()

	origFlag := splitWaitForTemporalEnabled
	splitWaitForTemporalEnabled = true
	defer func() { splitWaitForTemporalEnabled = origFlag }()

	origRetries := waitForTemporalUpdateMaxRetries
	origInitDelay := waitForTemporalUpdateInitDelay
	origMaxDelay := waitForTemporalUpdateMaxDelay
	waitForTemporalUpdateMaxRetries = 3
	waitForTemporalUpdateInitDelay = time.Millisecond
	waitForTemporalUpdateMaxDelay = time.Millisecond
	defer func() {
		waitForTemporalUpdateMaxRetries = origRetries
		waitForTemporalUpdateInitDelay = origInitDelay
		waitForTemporalUpdateMaxDelay = origMaxDelay
	}()

	// wg is released when DeleteJob is called, which is the last statement in the
	// goroutine's exhaustion path (line 3940-3941). This lets the test wait for
	// the goroutine to reach lines 3939-3941 without polling or arbitrary sleeps.
	var wg sync.WaitGroup
	wg.Add(1)

	mockStorage := database.NewMockStorage(t)
	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetPool", mock.Anything, pool.UUID, account.ID).Return(poolView, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, map[string]interface{}{
		"clones_shared_bytes": uint64(0),
	}).Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)
	// UpdateJob always fails — drives all waitForTemporalUpdateMaxRetries attempts (line 3926-3934).
	mockStorage.On("UpdateJob", mock.Anything, job.UUID, string(datamodel.JobsStateWaitForTemporal), mock.AnythingOfType("int"), mock.AnythingOfType("string")).
		Return(errors2.New("db write error"))
	// DeleteJob also fails — exercises the error log on line 3941.
	mockStorage.On("DeleteJob", mock.Anything, job.UUID, mock.AnythingOfType("string")).
		Return(errors2.New("delete also failed")).
		Run(func(args mock.Arguments) { wg.Done() })

	mockTemporalClient := workflowEngineMock.NewMockTemporalTestClient(t)
	mockTemporalClient.EXPECT().
		ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors2.New("temporal unavailable"))

	params := &common.SplitStartVolumeParams{
		AccountName: "test-account",
		VolumeID:    vol.UUID,
	}

	resultVol, jobUUID, err := _splitStartVolume(ctx, mockStorage, mockTemporalClient, params)
	// WAIT_FOR_TEMPORAL path clears err so the API can return 200.
	assert.NoError(t, err)
	assert.NotNil(t, resultVol)
	assert.Equal(t, job.UUID, jobUUID)

	// Wait for the background goroutine to exhaust all retries and call DeleteJob.
	// Retries use short test delays (see overrides above); keep a modest timeout
	// for CI scheduling jitter.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for background goroutine to exhaust retries and call DeleteJob")
	}

	mockProvider.AssertExpectations(t)
	mockTemporalClient.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// ===========================================================================
// _validateSplitStopVolumeParams tests
//
// The validator gates the synchronous splitStop endpoint with two
// user-facing branches:
//   - non-clone volume (missing CloneParentInfo) -> 400 UserInputValidationErr
//   - clone state != SPLITTING                   -> 409 ConflictErr
// The happy path (state == SPLITTING) must return nil.
// ===========================================================================

func TestValidateSplitStopVolumeParams_NilVolumeAttributes(t *testing.T) {
	ctx := context.Background()
	vol := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: "vol-uuid"},
		Name:             "test-volume",
		VolumeAttributes: nil,
	}

	err := _validateSplitStopVolumeParams(ctx, vol)
	assert.Error(t, err)
	assert.True(t, errors.IsUserInputValidationErr(err), "expected UserInputValidationErr, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "not a thin clone volume")
}

func TestValidateSplitStopVolumeParams_NilCloneParentInfo(t *testing.T) {
	ctx := context.Background()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:    "ext-uuid",
			CloneParentInfo: nil,
		},
	}

	err := _validateSplitStopVolumeParams(ctx, vol)
	assert.Error(t, err)
	assert.True(t, errors.IsUserInputValidationErr(err), "expected UserInputValidationErr, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "not a thin clone volume")
}

func TestValidateSplitStopVolumeParams_StateNotSplitting(t *testing.T) {
	ctx := context.Background()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateCloned, // anything other than SPLITTING is a 409
			},
		},
	}

	err := _validateSplitStopVolumeParams(ctx, vol)
	assert.Error(t, err)
	assert.True(t, errors.IsConflictErr(err), "expected ConflictErr, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "volume split is not in progress")
	// The current state must be surfaced in the message so the caller knows why.
	assert.Contains(t, err.Error(), datamodel.CloneStateCloned)
}

func TestValidateSplitStopVolumeParams_StateSplitting_OK(t *testing.T) {
	ctx := context.Background()
	vol := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-volume",
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateSplitting,
			},
		},
	}

	assert.NoError(t, _validateSplitStopVolumeParams(ctx, vol))
}

// ===========================================================================
// _splitStopVolume tests
//
// splitStop is synchronous (no Temporal workflow). The function:
//  1. Resolves the account and volume.
//  2. Validates clone state (must be SPLITTING).
//  3. Resolves the pool's ONTAP node and provider.
//  4. Best-effort reads the current split progress via GetVolumeCloneInfo.
//  5. Issues StopSplitVolume (PATCH split_initiated=false).
//  6. Persists clone state CLONED.
//  7. Returns the volume model with the captured percent (response-only).
//
// The helper below sets up a deterministic "ready-to-stop" world that the
// individual tests then mutate to exercise specific failure branches.
// ===========================================================================

// setupSplitStopVolumeBase wires up an account/pool/node/volume in
// SPLIT_STATE_IN_PROGRESS so each test only has to override the specific
// dependency it cares about. Function-variable overrides (getAccountWithName,
// validateSplitStopVolumeParams, hyperscaler.GetProviderByNode,
// convertDatastoreVolumeToModel) are restored via t.Cleanup.
func setupSplitStopVolumeBase(t *testing.T) (
	mockStorage *database.MockStorage,
	mockProvider *vsa.MockProvider,
	vol *datamodel.Volume,
	account *datamodel.Account,
	pool *datamodel.Pool,
	dbNode *datamodel.Node,
) {
	t.Helper()

	mockStorage = database.NewMockStorage(t)

	account = &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	pool = &datamodel.Pool{
		BaseModel:   datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:        "test-pool",
		AccountID:   1,
		SizeInBytes: 10000000,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-west1-a",
		},
	}
	dbNode = &datamodel.Node{
		BaseModel:       datamodel.BaseModel{UUID: "node-uuid"},
		PoolID:          pool.ID,
		Name:            "node-host",
		EndpointAddress: "10.0.0.1",
	}
	vol = &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 5, UUID: "vol-uuid"},
		Name:      "test-volume",
		AccountID: 1,
		Account:   account,
		Pool:      pool,
		PoolID:    pool.ID,
		State:     datamodel.LifeCycleStateREADY,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "ext-uuid",
			CloneParentInfo: &datamodel.CloneParentInfo{
				ParentVolumeUUID: "parent-uuid",
				State:            datamodel.CloneStateSplitting,
				StateDetails:     "in progress",
			},
		},
	}

	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	origValidate := validateSplitStopVolumeParams
	validateSplitStopVolumeParams = func(_ context.Context, _ *datamodel.Volume) error { return nil }
	t.Cleanup(func() {
		getAccountWithName = origGetAccountWithName
		validateSplitStopVolumeParams = origValidate
	})

	mockProvider = new(vsa.MockProvider)
	origGetProviderByNode := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = origGetProviderByNode })

	// Replace the heavy datamodel->model conversion with a stub that only
	// preserves the fields _splitStopVolume actually touches after the call.
	// CloneSharedBytes is forwarded from the DB value so the legacy-clone
	// fallback (no OriginalSharedBytes baseline) can be exercised end-to-end.
	origConvert := convertDatastoreVolumeToModel
	convertDatastoreVolumeToModel = func(v *datamodel.Volume, _ *[]string) *models.Volume {
		return &models.Volume{
			BaseModel:        models.BaseModel{UUID: v.UUID},
			DisplayName:      v.Name,
			CloneParentInfo:  &models.CloneParentInfo{},
			CloneSharedBytes: v.ClonesSharedBytes,
		}
	}
	t.Cleanup(func() { convertDatastoreVolumeToModel = origConvert })

	return mockStorage, mockProvider, vol, account, pool, dbNode
}

// TestSplitStopVolume_GetAccountFails: when getAccountWithName fails, the
// error must propagate verbatim and no downstream interaction must happen.
func TestSplitStopVolume_GetAccountFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage := database.NewMockStorage(t)

	wantErr := errors.New("account lookup failed")
	origGetAccountWithName := getAccountWithName
	getAccountWithName = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return nil, wantErr
	}
	t.Cleanup(func() { getAccountWithName = origGetAccountWithName })

	params := &common.SplitStopVolumeParams{AccountName: "test-account", VolumeID: "vol-uuid"}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, wantErr, err)
}

// TestSplitStopVolume_GetVolumeFails: when GetVolumeWithAccountID fails,
// the error must propagate verbatim.
func TestSplitStopVolume_GetVolumeFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, _, _ := setupSplitStopVolumeBase(t)

	wantErr := errors.New("volume lookup failed")
	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(nil, wantErr)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, wantErr, err)
}

// TestSplitStopVolume_ValidationFails: when the injected validator returns an
// error (e.g. non-clone -> 400, not splitting -> 409), the orchestrator must
// return it untouched without hitting the provider or the DB beyond the
// initial fetches.
func TestSplitStopVolume_ValidationFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, _, _ := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)

	conflict := errors.NewConflictErr("volume split is not in progress")
	validateSplitStopVolumeParams = func(_ context.Context, _ *datamodel.Volume) error { return conflict }

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, conflict, err)
	assert.True(t, errors.IsConflictErr(err))
}

// TestSplitStopVolume_GetNodesByPoolIDFails: DB error while listing pool
// nodes must propagate verbatim.
func TestSplitStopVolume_GetNodesByPoolIDFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, pool, _ := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	wantErr := errors.New("nodes query failed")
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return(([]*datamodel.Node)(nil), wantErr)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, wantErr, err)
}

// TestSplitStopVolume_NoNodesForPool: an empty node slice must be reported
// as ErrUnexpectedNodeCountForPool wrapped in a VCPError.
func TestSplitStopVolume_NoNodesForPool(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, pool, _ := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{}, nil)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Error(t, err)

	var vcpErr *vsaerrors.CustomError
	require := errors2.As(err, &vcpErr)
	assert.True(t, require, "expected CustomError (VCPError), got %T: %v", err, err)
	if vcpErr != nil {
		assert.Equal(t, vsaerrors.ErrUnexpectedNodeCountForPool, vcpErr.TrackingID)
	}
}

// TestSplitStopVolume_GetProviderFails: failure to construct the ONTAP
// provider for the node must propagate verbatim.
func TestSplitStopVolume_GetProviderFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	wantErr := errors.New("provider unavailable")
	vsa.GetProviderByNode = func(_ context.Context, _ *models.Node) (vsa.Provider, error) {
		return nil, wantErr
	}

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, wantErr, err)
}

// TestSplitStopVolume_MissingExternalUUID: even if a volume passes validation
// (clone metadata present, state == SPLITTING), it must not be sent to ONTAP
// without an ExternalUUID -- a UserInputValidationErr (400) is returned.
func TestSplitStopVolume_MissingExternalUUID(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, _, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	vol.VolumeAttributes.ExternalUUID = ""

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.True(t, errors.IsUserInputValidationErr(err), "expected UserInputValidationErr, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "external UUID")
}

// TestSplitStopVolume_GetVolumeCloneInfoFails_BestEffort: a failure from
// GetVolumeCloneInfo is non-fatal. The function must still issue
// StopSplitVolume, persist the new clone state, and return success -- only
// the SplitCompletePercent will be missing from the response.
func TestSplitStopVolume_GetVolumeCloneInfoFails_BestEffort(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return((*vsa.VolumeResponseClone)(nil), errors.New("ontap describe failed"))
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)

	// The CLONED-state persistence must still happen even though the
	// pre-stop describe failed.
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		attrsAny, ok := fields["volume_attributes"]
		if !ok {
			return false
		}
		attrs, ok := attrsAny.(*datamodel.VolumeAttributes)
		if !ok || attrs.CloneParentInfo == nil {
			return false
		}
		return attrs.CloneParentInfo.State == datamodel.CloneStateCloned && attrs.CloneParentInfo.StateDetails == ""
	})).Return(nil)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	if assert.NotNil(t, result.CloneParentInfo) {
		assert.Nil(t, result.CloneParentInfo.SplitCompletePercent,
			"no percent should be returned when GetVolumeCloneInfo fails")
	}
	mockProvider.AssertExpectations(t)
}

// TestSplitStopVolume_GetVolumeCloneInfo_NilCloneOrNoPercent: when ONTAP
// returns a nil VolumeResponseClone or a clone without SplitCompletePercent,
// the function must still complete the stop successfully and return no
// percent on the response.
func TestSplitStopVolume_GetVolumeCloneInfo_NilCloneOrNoPercent(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	// Clone object without SplitCompletePercent set.
	cloneInfo := &vsa.VolumeResponseClone{ParentVolumeUUID: "parent-uuid"}
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(cloneInfo, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)

	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(nil)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.NoError(t, err)
	if assert.NotNil(t, result) && assert.NotNil(t, result.CloneParentInfo) {
		assert.Nil(t, result.CloneParentInfo.SplitCompletePercent)
	}
	mockProvider.AssertExpectations(t)
}

// TestSplitStopVolume_StopSplitFails: a StopSplitVolume failure must be
// wrapped as vsaerrors.NewVCPError(ErrOntapRestAPIError, err) and the DB
// clone-state update must NOT run.
func TestSplitStopVolume_StopSplitFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	// Allow either order (best-effort describe first, then stop).
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return((*vsa.VolumeResponseClone)(nil), nil).Maybe()
	ontapErr := errors.New("ontap rest patch failed")
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(ontapErr)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Error(t, err)

	var vcpErr *vsaerrors.CustomError
	asOK := errors2.As(err, &vcpErr)
	assert.True(t, asOK, "expected CustomError (VCPError), got %T: %v", err, err)
	if vcpErr != nil {
		assert.Equal(t, vsaerrors.ErrOntapRestAPIError, vcpErr.TrackingID)
		// The underlying ONTAP error must be preserved in the wrap chain.
		assert.ErrorIs(t, err, ontapErr)
	}

	// UpdateVolumeFields must NOT be invoked when the stop fails; the mock is
	// already strict (no expectations set), so AssertExpectations covers this.
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_DBUpdateFails: when StopSplitVolume succeeds but the
// follow-up clone-state UpdateVolumeFields fails, the orchestrator must
// return that DB error to the caller (so the caller knows the in-memory
// view may be stale).
func TestSplitStopVolume_DBUpdateFails(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	pct := int64(42)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{SplitCompletePercent: &pct}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)

	dbErr := errors.New("db update failed")
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		_, ok := fields["volume_attributes"]
		return ok
	})).Return(dbErr)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.Nil(t, result)
	assert.Equal(t, dbErr, err)
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_Success_WithSplitPercent: the happy path -- every
// dependency succeeds, the persisted clone state is CLONED with empty
// state details, and the captured SplitCompletePercent flows through into
// the response model.
func TestSplitStopVolume_Success_WithSplitPercent(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	pct := int64(73)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{SplitCompletePercent: &pct}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)

	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		attrsAny, ok := fields["volume_attributes"]
		if !ok {
			return false
		}
		attrs, ok := attrsAny.(*datamodel.VolumeAttributes)
		if !ok || attrs.CloneParentInfo == nil {
			return false
		}
		return attrs.CloneParentInfo.State == datamodel.CloneStateCloned &&
			attrs.CloneParentInfo.StateDetails == "" &&
			attrs.CloneParentInfo.ParentVolumeUUID == "parent-uuid"
	})).Return(nil)

	params := &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID}
	result, err := _splitStopVolume(ctx, mockStorage, params)
	assert.NoError(t, err)
	if assert.NotNil(t, result) && assert.NotNil(t, result.CloneParentInfo) {
		if assert.NotNil(t, result.CloneParentInfo.SplitCompletePercent) {
			assert.Equal(t, pct, *result.CloneParentInfo.SplitCompletePercent)
		}
	}

	// The orchestrator must also update the in-memory CloneParentInfo it
	// returns to its caller so that subsequent reads in the same request
	// observe the new CLONED state without a re-fetch.
	assert.Equal(t, datamodel.CloneStateCloned, vol.VolumeAttributes.CloneParentInfo.State)
	assert.Equal(t, "", vol.VolumeAttributes.CloneParentInfo.StateDetails)

	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// ===========================================================================
// OriginalSharedBytes baseline tests
//
// These tests cover the new VolumeAttributes.OriginalSharedBytes plumbing:
//   - the pure computeRemainingSharedBytes formula across the percent matrix,
//   - the splitStop graft that overwrites response CloneSharedBytes when the
//     baseline is present, and
//   - the legacy fallback when the baseline is absent.
// ===========================================================================

// uint64Ptr is a tiny helper for declaring inline OriginalSharedBytes pointers
// in table-driven tests below.
func uint64Ptr(v uint64) *uint64 { return &v }

// int64Ptr returns a pointer to the given int64 — used for SplitCompletePercent.
func int64Ptr(v int64) *int64 { return &v }

// TestComputeRemainingSharedBytes_Matrix exercises the pure formula across the
// boundaries that splitStop actually encounters. The formula is
//
//	remaining = original * (100 - percent) / 100
//
// with the contractual edge cases:
//   - percent nil       -> return original (no progress signal observed)
//   - percent <= 0      -> clamp to 0   -> return original
//   - percent >= 100    -> clamp to 100 -> return 0
//   - 0 < percent < 100 -> linear interpolation
func TestComputeRemainingSharedBytes_Matrix(t *testing.T) {
	cases := []struct {
		name     string
		original uint64
		percent  *int64
		want     uint64
	}{
		{"nil percent returns original", 1000, nil, 1000},
		{"zero percent returns original", 1000, int64Ptr(0), 1000},
		{"negative percent clamped to zero", 1000, int64Ptr(-5), 1000},
		{"50% returns half", 1000, int64Ptr(50), 500},
		{"73% returns 270", 1000, int64Ptr(73), 270},
		{"100% returns zero", 1000, int64Ptr(100), 0},
		{"over 100 clamped to zero", 1000, int64Ptr(150), 0},
		{"zero baseline stays zero", 0, int64Ptr(40), 0},
		// Large value sanity check: multiply-before-divide order keeps precision
		// without overflow for realistic volume sizes (16 TiB original × 100
		// fits comfortably in uint64).
		{"large baseline 25% remaining", 16 * 1024 * 1024 * 1024 * 1024, int64Ptr(75), 4 * 1024 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := computeRemainingSharedBytes(tc.original, tc.percent)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestSplitStopVolume_CloneSharedBytes_GraftFromBaseline: when the volume row
// carries an OriginalSharedBytes baseline and ONTAP reports a partial split
// progress, the response cloneSharedBytes must equal the formula-derived
// remainder, even though the DB row's clones_shared_bytes is still 0 (the
// splitStart-reserved value).
func TestSplitStopVolume_CloneSharedBytes_GraftFromBaseline(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	// The clone was created with 1000 shared bytes; splitStart zeroed the DB
	// row's clones_shared_bytes. The baseline must persist on VolumeAttributes.
	const baseline uint64 = 1000
	vol.VolumeAttributes.OriginalSharedBytes = uint64Ptr(baseline)
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	pct := int64(40)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{SplitCompletePercent: &pct}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.Anything).Return(nil)

	result, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		// 1000 * (100 - 40) / 100 = 600.
		assert.Equal(t, uint64(600), result.CloneSharedBytes,
			"response cloneSharedBytes must be derived from OriginalSharedBytes and the captured percent")
	}
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_CloneSharedBytes_NoPercentReturnsBaseline: when ONTAP
// does not return a split percent (clone nil or SplitCompletePercent nil), the
// formula falls through to the baseline value — semantically "no observed
// progress, all bytes still shared".
func TestSplitStopVolume_CloneSharedBytes_NoPercentReturnsBaseline(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	const baseline uint64 = 4096
	vol.VolumeAttributes.OriginalSharedBytes = uint64Ptr(baseline)
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	// Provider returns a clone object with no SplitCompletePercent.
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{ParentVolumeUUID: "parent-uuid"}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.Anything).Return(nil)

	result, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Equal(t, baseline, result.CloneSharedBytes,
			"without a percent the response cloneSharedBytes must equal the baseline")
	}
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_CloneSharedBytes_DescribeFailedReturnsBaseline: a
// best-effort describe failure (network blip, transient ONTAP error) must NOT
// suppress the graft. The stop still runs and the response falls back to the
// baseline (percent is unknown → assume nothing copied yet).
func TestSplitStopVolume_CloneSharedBytes_DescribeFailedReturnsBaseline(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	const baseline uint64 = 2048
	vol.VolumeAttributes.OriginalSharedBytes = uint64Ptr(baseline)
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return((*vsa.VolumeResponseClone)(nil), errors.New("ontap describe failed"))
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.Anything).Return(nil)

	result, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Equal(t, baseline, result.CloneSharedBytes)
	}
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_CloneSharedBytes_FullySplitReturnsZero: when ONTAP
// reports 100% progress, the formula returns zero remaining bytes — the
// expected steady-state for a clone that has effectively finished splitting
// at the moment splitStop was invoked.
func TestSplitStopVolume_CloneSharedBytes_FullySplitReturnsZero(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	vol.VolumeAttributes.OriginalSharedBytes = uint64Ptr(10_000)
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	pct := int64(100)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{SplitCompletePercent: &pct}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.Anything).Return(nil)

	result, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Equal(t, uint64(0), result.CloneSharedBytes)
	}
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_CloneSharedBytes_LegacyCloneNoBaseline: legacy clones
// (created before OriginalSharedBytes existed AND not yet picked up by the
// backfill migration) must degrade gracefully — the response cloneSharedBytes
// keeps the raw DB value (typically 0 during a split) rather than being
// overwritten with a fabricated number.
func TestSplitStopVolume_CloneSharedBytes_LegacyCloneNoBaseline(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	// Legacy clone: no OriginalSharedBytes on VolumeAttributes; DB row still
	// has the splitStart-reserved zero.
	vol.VolumeAttributes.OriginalSharedBytes = nil
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)

	pct := int64(50)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{SplitCompletePercent: &pct}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.Anything).Return(nil)

	result, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	if assert.NotNil(t, result) {
		assert.Equal(t, uint64(0), result.CloneSharedBytes,
			"legacy clones with no baseline must keep the raw DB value (0 mid-split)")
	}
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

// TestSplitStopVolume_PreservesOriginalSharedBytes: the persisted volume_attributes
// after splitStop must still carry the OriginalSharedBytes baseline — splitStop
// only transitions the clone state back to CLONED and must not erase the
// baseline (a clone that resumes a split later will need it again).
func TestSplitStopVolume_PreservesOriginalSharedBytes(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockStorage, mockProvider, vol, account, pool, dbNode := setupSplitStopVolumeBase(t)

	const baseline uint64 = 8192
	vol.VolumeAttributes.OriginalSharedBytes = uint64Ptr(baseline)
	vol.ClonesSharedBytes = 0

	mockStorage.On("GetVolumeWithAccountID", mock.Anything, vol.UUID, account.ID).Return(vol, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, pool.ID).Return([]*datamodel.Node{dbNode}, nil)
	mockProvider.On("GetVolumeCloneInfo", "ext-uuid").Return(&vsa.VolumeResponseClone{ParentVolumeUUID: "parent-uuid"}, nil)
	mockProvider.On("StopSplitVolume", "ext-uuid").Return(nil)
	mockStorage.On("UpdateVolumeFields", mock.Anything, vol.UUID, mock.MatchedBy(func(fields map[string]interface{}) bool {
		attrsAny, ok := fields["volume_attributes"]
		if !ok {
			return false
		}
		attrs, ok := attrsAny.(*datamodel.VolumeAttributes)
		if !ok {
			return false
		}
		// Baseline must survive the CLONED-state write.
		return attrs.OriginalSharedBytes != nil && *attrs.OriginalSharedBytes == baseline
	})).Return(nil)

	_, err := _splitStopVolume(ctx, mockStorage, &common.SplitStopVolumeParams{AccountName: account.Name, VolumeID: vol.UUID})
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

package supervisorhandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func encryptPoolJob() *datamodel.Job {
	return &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid"},
		Type:      string(datamodel.JobTypeEncryptPool),
		State:     string(datamodel.JobsStateNEW),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         "pool-uuid",
			PreviousState:        datamodel.LifeCycleStateREADY,
			PreviousStateDetails: datamodel.LifeCycleStateReadyDetails,
			PayloadAttributes: map[string]interface{}{
				encryptPoolKmsConfigIDAttribute: "kms-uuid",
			},
		},
	}
}

func updatingPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateUpdating,
	}
}

func migratingKmsConfig() *datamodel.KmsConfig {
	return &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid"},
		State:     datamodel.LifeCycleStateMigrating,
	}
}

func TestEncryptPoolHandler_JobTypes(t *testing.T) {
	handler := NewEncryptPoolHandler()
	jobTypes := handler.JobTypes()

	require.Len(t, jobTypes, 1)
	require.Contains(t, jobTypes, datamodel.JobTypeEncryptPool)
}

func TestNewEncryptPoolHandler(t *testing.T) {
	handler := NewEncryptPoolHandler()
	require.NotNil(t, handler)
	require.IsType(t, &EncryptPoolHandler{}, handler)
}

func TestEncryptPoolHandler_Handle_SkipsNonTimeout(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	err := handler.Handle(context.Background(), encryptPoolJob(), Event("OTHER"), storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
	storage.AssertNotCalled(t, "GetKmsConfig", mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_SkipsNilJobAttributes(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	err := handler.Handle(context.Background(), &datamodel.Job{JobAttributes: nil}, EventTimeout, storage)
	require.NoError(t, err)
	storage.AssertNotCalled(t, "GetPoolByUUID", mock.Anything, mock.Anything)
	storage.AssertNotCalled(t, "GetKmsConfig", mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_RevertsPoolAndKmsConfigToReady(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	kmsConfig := migratingKmsConfig()

	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(kmsConfig, nil).Once()
	storage.EXPECT().IsKmsConfigInUse(mock.Anything, "kms-uuid").Return(false, nil).Once()
	storage.EXPECT().UpdateKmsConfigState(mock.Anything, "kms-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(kmsConfig, nil).Once()

	require.NoError(t, handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage))
}

func TestEncryptPoolHandler_Handle_RevertsKmsConfigToInUseWhenStillUsed(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	kmsConfig := migratingKmsConfig()

	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(kmsConfig, nil).Once()
	storage.EXPECT().IsKmsConfigInUse(mock.Anything, "kms-uuid").Return(true, nil).Once()
	storage.EXPECT().UpdateKmsConfigState(mock.Anything, "kms-uuid", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateAvailableDetails).Return(kmsConfig, nil).Once()

	require.NoError(t, handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage))
}

// The KMS config must still be released when the pool was already moved out of UPDATING,
// otherwise a partially compensated dispatch failure keeps the account blocked.
func TestEncryptPoolHandler_Handle_RevertsKmsConfigWhenPoolAlreadyReverted(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		State:     datamodel.LifeCycleStateREADY,
	}
	kmsConfig := migratingKmsConfig()

	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(kmsConfig, nil).Once()
	storage.EXPECT().IsKmsConfigInUse(mock.Anything, "kms-uuid").Return(false, nil).Once()
	storage.EXPECT().UpdateKmsConfigState(mock.Anything, "kms-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(kmsConfig, nil).Once()

	require.NoError(t, handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage))
	storage.AssertNotCalled(t, "UpdatePoolState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_SkipsKmsConfigNotInMigrating(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "kms-uuid"},
		State:     datamodel.LifeCycleStateInUse,
	}

	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(kmsConfig, nil).Once()

	require.NoError(t, handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage))
	storage.AssertNotCalled(t, "IsKmsConfigInUse", mock.Anything, mock.Anything)
	storage.AssertNotCalled(t, "UpdateKmsConfigState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_SkipsMissingKmsConfigID(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	job := encryptPoolJob()
	job.JobAttributes.PayloadAttributes = nil

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()

	require.NoError(t, handler.Handle(context.Background(), job, EventTimeout, storage))
	storage.AssertNotCalled(t, "GetKmsConfig", mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_SkipsDeletedKmsConfig(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return((*datamodel.KmsConfig)(nil), vsaerrors.NewNotFoundErr("kms config", nil)).Once()

	require.NoError(t, handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage))
	storage.AssertNotCalled(t, "UpdateKmsConfigState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestEncryptPoolHandler_Handle_GetKmsConfigError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return((*datamodel.KmsConfig)(nil), errors.New("database error")).Once()

	err := handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "load KMS config for encrypt pool cleanup")
}

func TestEncryptPoolHandler_Handle_IsKmsConfigInUseError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(migratingKmsConfig(), nil).Once()
	storage.EXPECT().IsKmsConfigInUse(mock.Anything, "kms-uuid").Return(false, errors.New("database error")).Once()

	err := handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "is in use")
}

func TestEncryptPoolHandler_Handle_UpdateKmsConfigStateError(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(pool, nil).Once()
	storage.EXPECT().GetKmsConfig(mock.Anything, "kms-uuid").Return(migratingKmsConfig(), nil).Once()
	storage.EXPECT().IsKmsConfigInUse(mock.Anything, "kms-uuid").Return(false, nil).Once()
	storage.EXPECT().UpdateKmsConfigState(mock.Anything, "kms-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).
		Return((*datamodel.KmsConfig)(nil), errors.New("update failed")).Once()

	err := handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert KMS config")
}

// A pool revert failure must abort before the KMS revert so the retry on the next scan
// still sees both resources in their claimed states.
func TestEncryptPoolHandler_Handle_PoolRevertFailureSkipsKmsCleanup(t *testing.T) {
	storage := database.NewMockStorage(t)
	handler := NewEncryptPoolHandler()

	pool := updatingPool()
	storage.EXPECT().GetPoolByUUID(mock.Anything, "pool-uuid").Return(pool, nil).Once()
	storage.EXPECT().UpdatePoolState(mock.Anything, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).
		Return((*datamodel.Pool)(nil), errors.New("update failed")).Once()

	err := handler.Handle(context.Background(), encryptPoolJob(), EventTimeout, storage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "revert pool state")
	storage.AssertNotCalled(t, "GetKmsConfig", mock.Anything, mock.Anything)
}

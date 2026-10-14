package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/mocks"
)

func TestDeleteKmsConfigCoreLoadsTheVcpRow(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
		BaseModel:         datamodel.BaseModel{UUID: kmsConfigUUID},
		ResourceID:        "cmek-east4",
		AccountID:         71,
		CustomerProjectID: "771149402920",
		KeyRingLocation:   "us-east4",
	}, nil)
	store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
	otherPath := "projects/846223794136/locations/us-east4/kmsConfigs/cmek-updated-2-east4"
	store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{
		{},
		{CmekAttributes: &datamodel.CmekAttributes{KmsConfigResourcePath: &otherPath}},
	}, nil)
	store.On("ListOngoingPoolJobsWithKmsConfigId", mock.Anything, mock.Anything, int64(71)).Return([]*datamodel.Job{}, nil)
	store.On("GetJobByResourceUUID", mock.Anything, kmsConfigUUID, string(datamodel.JobTypeDeleteKmsConfig)).Return((*datamodel.Job)(nil), errors.NewNotFoundErr("Job", nil))
	store.On("UpdateKmsConfigState", mock.Anything, kmsConfigUUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(&datamodel.KmsConfig{
		BaseModel:         datamodel.BaseModel{UUID: kmsConfigUUID},
		ResourceID:        "cmek-east4",
		AccountID:         71,
		CustomerProjectID: "771149402920",
		KeyRingLocation:   "us-east4",
		State:             datamodel.LifeCycleStateDeleting,
		Account:           &datamodel.Account{Name: "771149402920"},
	}, nil)
	store.On("CreateJob", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.Type == string(datamodel.JobTypeDeleteKmsConfig) &&
			job.JobAttributes != nil &&
			job.JobAttributes.ResourceUUID == kmsConfigUUID
	})).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-1"},
		WorkflowID: "job-1",
		Type:       string(datamodel.JobTypeDeleteKmsConfig),
		State:      string(datamodel.JobsStateNEW),
	}, nil)
	temporalClient := new(mocks.Client)
	temporalClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(params *common.DeleteKmsConfigParams) bool {
		return params.SkipSdeDelete && params.KmsConfigID == kmsConfigUUID && params.AccountName == "771149402920"
	})).Return(nil, nil)

	got, job, err := deleteKmsConfigCore(context.Background(), store, temporalClient, kmsConfigUUID)

	assert.NoError(t, err)
	assert.Equal(t, kmsConfigUUID, got.UUID)
	assert.Equal(t, "cmek-east4", got.ResourceID)
	assert.Equal(t, int64(71), got.AccountID)
	assert.Equal(t, "771149402920", got.CustomerProjectID)
	assert.Equal(t, datamodel.LifeCycleStateDeleting, got.State)
	assert.Equal(t, "job-1", job.UUID)
	store.AssertNotCalled(t, "DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreReturnsNotFoundWhenTheRowIsMissing(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	notFound := errors.NewNotFoundErr("KMS Configuration", nil)
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return((*datamodel.KmsConfig)(nil), notFound)

	got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

	assert.Nil(t, got)
	assert.Nil(t, job)
	assert.ErrorIs(t, err, notFound)
	store.AssertNotCalled(t, "IsKmsConfigInUse", mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreRejectsAConfigStillUsedByAPool(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID},
	}, nil)
	store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(true, nil)

	got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

	assert.Nil(t, got)
	assert.Nil(t, job)
	assert.True(t, errors.IsConflictErr(err))
	assert.EqualError(t, err, "can not delete this policy as it is still in use by one or more pools")
	store.AssertNotCalled(t, "DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "ListBackupVaults", mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreRejectsAConfigStillUsedByABackupVault(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
		BaseModel:         datamodel.BaseModel{UUID: kmsConfigUUID},
		ResourceID:        "cmek-east4",
		AccountID:         71,
		CustomerProjectID: "771149402920",
		KeyRingLocation:   "us-east4",
	}, nil)
	store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
	resourcePath := "projects/771149402920/locations/us-east4/kmsConfigs/cmek-east4"
	store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{
		{CmekAttributes: &datamodel.CmekAttributes{KmsConfigResourcePath: &resourcePath}},
	}, nil)

	got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

	assert.Nil(t, got)
	assert.Nil(t, job)
	assert.True(t, errors.IsConflictErr(err))
	assert.EqualError(t, err, "can not delete this policy as it is still in use by one or more backup vaults")
	store.AssertNotCalled(t, "DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "ListOngoingPoolJobsWithKmsConfigId", mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreRejectsAnOngoingPoolCreateJob(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID, ID: 71},
		AccountID: 71,
	}, nil)
	store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
	store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{}, nil)
	store.On("ListOngoingPoolJobsWithKmsConfigId", mock.Anything, int64(71), int64(71)).Return([]*datamodel.Job{{
		Type:  string(datamodel.JobTypeCreatePool),
		State: string(datamodel.JobsStatePROCESSING),
	}}, nil)

	got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

	assert.Nil(t, got)
	assert.Nil(t, job)
	assert.True(t, errors.IsConflictErr(err))
	assert.EqualError(t, err, "can not delete this policy as there are ongoing pool creation jobs using it")
	store.AssertNotCalled(t, "DeleteKmsConfig", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreReturnsAnInProgressDeleteJob(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID, ID: 71},
		AccountID: 71,
		State:     datamodel.LifeCycleStateDeleting,
	}, nil)
	store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
	store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{}, nil)
	store.On("ListOngoingPoolJobsWithKmsConfigId", mock.Anything, int64(71), int64(71)).Return([]*datamodel.Job{}, nil)
	store.On("GetJobByResourceUUID", mock.Anything, kmsConfigUUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-existing"},
		Type:      string(datamodel.JobTypeDeleteKmsConfig),
		State:     string(datamodel.JobsStatePROCESSING),
	}, nil)

	got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

	assert.NoError(t, err)
	assert.Equal(t, kmsConfigUUID, got.UUID)
	assert.Equal(t, datamodel.LifeCycleStateDeleting, got.State)
	assert.Equal(t, "job-existing", job.UUID)
	assert.Equal(t, datamodel.JobsStatePROCESSING, job.State)
	store.AssertNotCalled(t, "UpdateKmsConfigState", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "CreateJob", mock.Anything, mock.Anything)
}

func TestDeleteKmsConfigCoreStartsANewDeleteWhenThePreviousJobIsFinished(t *testing.T) {
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	for _, state := range []datamodel.JobState{datamodel.JobsStateDONE, datamodel.JobsStateERROR} {
		t.Run(string(state), func(t *testing.T) {
			store := database.NewMockStorage(t)
			store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(&datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID, ID: 71},
				AccountID: 71,
			}, nil)
			store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
			store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{}, nil)
			store.On("ListOngoingPoolJobsWithKmsConfigId", mock.Anything, int64(71), int64(71)).Return([]*datamodel.Job{}, nil)
			store.On("GetJobByResourceUUID", mock.Anything, kmsConfigUUID, string(datamodel.JobTypeDeleteKmsConfig)).Return(&datamodel.Job{
				BaseModel: datamodel.BaseModel{UUID: "job-finished"},
				Type:      string(datamodel.JobTypeDeleteKmsConfig),
				State:     string(state),
			}, nil)
			stateErr := assert.AnError
			store.On("UpdateKmsConfigState", mock.Anything, kmsConfigUUID, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return((*datamodel.KmsConfig)(nil), stateErr)

			got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

			assert.Nil(t, got)
			assert.Nil(t, job)
			assert.ErrorIs(t, err, stateErr)
			store.AssertNotCalled(t, "CreateJob", mock.Anything, mock.Anything)
		})
	}
}

func TestDeleteKmsConfigCoreUsesTheOrchestrator(t *testing.T) {
	store := database.NewMockStorage(t)
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	notFound := errors.NewNotFoundErr("KMS Configuration", nil)
	store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return((*datamodel.KmsConfig)(nil), notFound)

	got, job, err := (&GCPOrchestrator{storage: store}).DeleteKmsConfigCore(context.Background(), kmsConfigUUID)

	assert.Nil(t, got)
	assert.Nil(t, job)
	assert.ErrorIs(t, err, notFound)
}

func TestDeleteKmsConfigCoreReturnsStorageErrors(t *testing.T) {
	kmsConfigUUID := "a5286416-15c0-4286-9ee7-a2e9f679cf4c"
	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID, ID: 71},
		AccountID: 71,
	}
	storageErr := assert.AnError

	t.Run("InUseCheck", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(kmsConfig, nil)
		store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, storageErr)

		got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

		assert.Nil(t, got)
		assert.Nil(t, job)
		assert.ErrorIs(t, err, storageErr)
	})

	t.Run("BackupVaultList", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(kmsConfig, nil)
		store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
		store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault(nil), storageErr)

		got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

		assert.Nil(t, got)
		assert.Nil(t, job)
		assert.ErrorIs(t, err, storageErr)
	})

	t.Run("PoolJobList", func(t *testing.T) {
		store := database.NewMockStorage(t)
		store.On("GetKmsConfig", mock.Anything, kmsConfigUUID).Return(kmsConfig, nil)
		store.On("IsKmsConfigInUse", mock.Anything, kmsConfigUUID).Return(false, nil)
		store.On("ListBackupVaults", mock.Anything, int64(71)).Return([]*datamodel.BackupVault{}, nil)
		store.On("ListOngoingPoolJobsWithKmsConfigId", mock.Anything, int64(71), int64(71)).Return([]*datamodel.Job(nil), storageErr)

		got, job, err := deleteKmsConfigCore(context.Background(), store, nil, kmsConfigUUID)

		assert.Nil(t, got)
		assert.Nil(t, job)
		assert.ErrorIs(t, err, storageErr)
	})
}

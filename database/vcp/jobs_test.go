package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestGetJob(t *testing.T) {
	t.Run("WhenJobExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
		}
		err = store.db.Create(job).Error()
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		result, err := store.GetJob(context.Background(), job.UUID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, job.UUID, result.UUID, "Expected job id %v, got %v", job.UUID, result.UUID)
	})
	t.Run("WhenJobDoesNotExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		_, err1 := store.GetJob(context.Background(), "test-job-uuid")
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err1, &customErr) {
			assert.EqualError(tt, customErr.Unwrap(), "job not found")
		} else {
			tt.Fatalf("Expected a CustomError, got %v", err)
		}
	})
}

func TestCreateJob(t *testing.T) {
	t.Run("WhenJobIsCreatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
		}

		createdJob, err := store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, job.UUID, createdJob.UUID, "Expected job uuid %v, got %v", job.UUID, createdJob.UUID)
	})
	t.Run("WhenWorkflowIDProvidedOnJob_IsIgnored", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{ID: 1, UUID: "establish-job-uuid"},
			WorkflowID: "should-not-persist-from-input",
		}

		createdJob, err := store.CreateJob(context.Background(), job)
		assert.NoError(tt, err)
		assert.Equal(tt, "establish-job-uuid", createdJob.UUID)
		assert.Equal(tt, "establish-job-uuid", createdJob.WorkflowID)
	})
	t.Run("WhenJobAlreadyExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
		}
		err = store.db.Create(job).Error()
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		_, err1 := store.CreateJob(context.Background(), job)
		assert.EqualError(tt, err1, "UNIQUE constraint failed: jobs.id")
	})
}

func TestCreateJobWithWorkflowID(t *testing.T) {
	t.Run("UsesProvidedWorkflowID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "job-uuid-1"},
			Type:      string(datamodel.JobTypeFlexCacheEstablishPeering),
		}

		createdJob, err := store.CreateJobWithWorkflowID(context.Background(), job, "shared-wf-id")
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid-1", createdJob.UUID)
		assert.Equal(tt, "shared-wf-id", createdJob.WorkflowID)
	})
}

func TestUpdateJob(t *testing.T) {
	t.Run("WhenJobIsUpdatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			State: datamodel.LifeCycleStateCreating,
		}

		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)
		err = store.UpdateJob(context.Background(), job.UUID, datamodel.LifeCycleStateREADY, 0, "")
		assert.NoError(tt, err, "Failed to update job: %v", err)
		updatedJob, err := store.GetJob(context.Background(), job.UUID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, job.UUID, updatedJob.UUID, "Expected job UUID %v, got %v", job.UUID, updatedJob.UUID)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, updatedJob.State, "Expected job state %v, got %v", datamodel.LifeCycleStateREADY, updatedJob.State)
	})
}

func TestDeleteJob(t *testing.T) {
	t.Run("WhenJobIsMarkedAsDeletedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			State: datamodel.LifeCycleStateCreating,
		}

		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Delete the job
		err = store.DeleteJob(context.Background(), job.UUID, "")
		assert.NoError(tt, err, "Failed to delete job: %v", err)

		// Attempt to retrieve the deleted job
		deletedJob, err := store.GetJob(context.Background(), job.UUID)
		assert.Error(tt, err, "Expected an error when retrieving a deleted job")
		assert.Nil(tt, deletedJob, "Expected no job to be retrieved after deletion")
	})
}

func TestGetJobsWithCondition(t *testing.T) {
	t.Run("WhenNoJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Empty conditions
		filter := utils.CreateFilterWithConditions(&utils.FilterCondition{})
		jobs, err := store.GetJobsWithCondition(context.Background(), *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected empty jobs list, got %v", jobs)
	})

	t.Run("WhenJobsExistMatchingCondition", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create jobs with different states
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-1-uuid",
			},
			State: datamodel.LifeCycleStateCreating,
			Type:  "test-type",
		}
		job2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "job-2-uuid",
			},
			State: datamodel.LifeCycleStateREADY,
			Type:  "test-type",
		}
		job3 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "job-3-uuid",
			},
			State: datamodel.LifeCycleStateCreating,
			Type:  "other-type",
		}

		_, err = store.CreateJob(context.Background(), job1)
		assert.NoError(tt, err, "Failed to create job1: %v", err)
		_, err = store.CreateJob(context.Background(), job2)
		assert.NoError(tt, err, "Failed to create job2: %v", err)
		_, err = store.CreateJob(context.Background(), job3)
		assert.NoError(tt, err, "Failed to create job3: %v", err)

		// Filter by state
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("state", "=", datamodel.LifeCycleStateCreating))
		jobs, err := store.GetJobsWithCondition(context.Background(), *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 2, "Expected 2 jobs, got %d", len(jobs))

		// Verify the correct jobs were returned
		jobUUIDs := []string{jobs[0].UUID, jobs[1].UUID}
		assert.Contains(tt, jobUUIDs, job1.UUID, "Expected job1 UUID in results")
		assert.Contains(tt, jobUUIDs, job3.UUID, "Expected job3 UUID in results")
		assert.NotContains(tt, jobUUIDs, job2.UUID, "Did not expect job2 UUID in results")

		// Filter by state and type
		filter = utils.CreateFilterWithConditions(
			utils.NewFilterCondition("state", "=", datamodel.LifeCycleStateCreating),
			utils.NewFilterCondition("type", "=", "test-type"))
		jobs, err = store.GetJobsWithCondition(context.Background(), *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 job, got %d", len(jobs))
		assert.Equal(tt, job1.UUID, jobs[0].UUID, "Expected job1 UUID, got %v", jobs[0].UUID)
	})

	t.Run("WhenJobsExistButNoneMatchCondition", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			State: datamodel.LifeCycleStateCreating,
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Filter with non-matching condition
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("state", "=", "non-existent-state"))
		jobs, err := store.GetJobsWithCondition(context.Background(), *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected empty jobs list, got %v", jobs)
	})
}

func TestListOngoingPoolJobsWithKmsConfigId(t *testing.T) {
	t.Run("WhenNoJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create pool and job with matching kms_config_id
		pool := &datamodel.Pool{
			Name:        "test-pool",
			KmsConfigID: sql.NullInt64{Int64: 123, Valid: true},
			AccountID:   456,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool: %v", err)

		jobs, err := store.ListOngoingPoolJobsWithKmsConfigId(context.Background(), 123, 456)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected no jobs, got %v", jobs)
	})

	t.Run("WhenJobsExistWithMatchingKmsId", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create pool and job with matching kms_config_id
		pool := &datamodel.Pool{
			Name:        "test-pool",
			KmsConfigID: sql.NullInt64{Int64: 123, Valid: true},
			AccountID:   456,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool: %v", err)

		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "job-uuid"},
			ResourceName: "test-pool",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(string(datamodel.JobTypeCreatePool)),
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		jobs, err := store.ListOngoingPoolJobsWithKmsConfigId(context.Background(), 123, 456)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 job, got %d", len(jobs))
		assert.Equal(tt, job.UUID, jobs[0].UUID, "Expected job UUID %v, got %v", job.UUID, jobs[0].UUID)
	})

	t.Run("WhenLargeCapacityPoolJobExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create pool and large capacity job with matching kms_config_id
		pool := &datamodel.Pool{
			Name:        "test-large-pool",
			KmsConfigID: sql.NullInt64{Int64: 123, Valid: true},
			AccountID:   456,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool: %v", err)

		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "large-job-uuid"},
			ResourceName: "test-large-pool",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(datamodel.JobTypeCreateLargePool), // Large capacity job type
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		jobs, err := store.ListOngoingPoolJobsWithKmsConfigId(context.Background(), 123, 456)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 job, got %d", len(jobs))
		assert.Equal(tt, job.UUID, jobs[0].UUID, "Expected job UUID %v, got %v", job.UUID, jobs[0].UUID)
		assert.Equal(tt, string(datamodel.JobTypeCreateLargePool), jobs[0].Type, "Expected job type %v, got %v", datamodel.JobTypeCreateLargePool, jobs[0].Type)
	})

	t.Run("WhenBothRegularAndLargeCapacityJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create pools for both regular and large capacity
		regularPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "regular-pool-uuid"},
			Name:           "test-regular-pool",
			KmsConfigID:    sql.NullInt64{Int64: 123, Valid: true},
			AccountID:      456,
			DeploymentName: "regular-deployment",
		}
		err = store.db.Create(regularPool).Error()
		assert.NoError(tt, err, "Failed to create regular pool: %v", err)

		largePool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "large-pool-uuid"},
			Name:           "test-large-pool",
			KmsConfigID:    sql.NullInt64{Int64: 123, Valid: true},
			AccountID:      456,
			DeploymentName: "large-deployment",
		}
		err = store.db.Create(largePool).Error()
		assert.NoError(tt, err, "Failed to create large pool: %v", err)

		// Create both types of jobs
		regularJob := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "regular-job-uuid"},
			ResourceName: "test-regular-pool",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(string(datamodel.JobTypeCreatePool)),
		}
		_, err = store.CreateJob(context.Background(), regularJob)
		assert.NoError(tt, err, "Failed to create regular job: %v", err)

		largeJob := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "large-job-uuid"},
			ResourceName: "test-large-pool",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(datamodel.JobTypeCreateLargePool),
		}
		_, err = store.CreateJob(context.Background(), largeJob)
		assert.NoError(tt, err, "Failed to create large job: %v", err)

		jobs, err := store.ListOngoingPoolJobsWithKmsConfigId(context.Background(), 123, 456)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 2, "Expected 2 jobs, got %d", len(jobs))

		// Verify both job types are returned
		jobTypes := make(map[string]bool)
		for _, job := range jobs {
			jobTypes[job.Type] = true
		}
		assert.True(tt, jobTypes[string(string(datamodel.JobTypeCreatePool))], "Expected regular pool job type to be present")
		assert.True(tt, jobTypes[string(datamodel.JobTypeCreateLargePool)], "Expected large pool job type to be present")
	})

	t.Run("WhenJobsExistButKmsIdDoesNotMatch", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create pool and job with different kms_config_id
		pool := &datamodel.Pool{
			Name:        "test-pool",
			KmsConfigID: sql.NullInt64{Int64: 999, Valid: true},
			AccountID:   456,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool: %v", err)

		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "job-uuid-2"},
			ResourceName: "test-pool",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(string(datamodel.JobTypeCreatePool)),
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		jobs, err := store.ListOngoingPoolJobsWithKmsConfigId(context.Background(), 123, 456)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected no jobs, got %v", jobs)
	})
}

func TestGetOngoingMigrateKmsConfigJob(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	jobs := []*datamodel.Job{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1"}, Type: "MIGRATE_KMS_CONFIG", State: "NEW",
			AccountID: sql.NullInt64{Int64: 1, Valid: true}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2"}, Type: "CREATE_POOL", State: "NEW",
			AccountID: sql.NullInt64{Int64: 2, Valid: true}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid3"}, Type: "MIGRATE_KMS_CONFIG", State: "DONE",
			AccountID: sql.NullInt64{Int64: 3, Valid: true}},
	}

	err = store.db.Create(jobs).Error()
	if err != nil {
		t.Fatalf("Failed to create Jobs table: %v", err)
	}
	t.Run("WhenQueriedJobIsPresent", func(tt *testing.T) {
		result, errQuery := store.GetOngoingMigrateKmsConfigJob(context.Background(), int64(1))
		assert.NoError(tt, errQuery)
		assert.NotNil(tt, result)
		assert.Equal(tt, result.UUID, "uuid1")
	})
	t.Run("WhenQueriedJobIsPerformingAnotherAction", func(tt *testing.T) {
		result, errQuery := store.GetOngoingMigrateKmsConfigJob(context.Background(), int64(2))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
	t.Run("WhenQueriedJobStateIsNeitherNewNorProcessing", func(tt *testing.T) {
		result, errQuery := store.GetOngoingMigrateKmsConfigJob(context.Background(), int64(3))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
	t.Run("WhenQueriedJobAccountIdIsNotPresent", func(tt *testing.T) {
		result, errQuery := store.GetOngoingMigrateKmsConfigJob(context.Background(), int64(4))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
}

func TestGetOngoingEncryptPoolJobForAccount(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	jobs := []*datamodel.Job{
		{BaseModel: datamodel.BaseModel{UUID: "ep1"}, Type: string(datamodel.JobTypeEncryptPool), State: "PROCESSING",
			AccountID: sql.NullInt64{Int64: 1, Valid: true}},
		{BaseModel: datamodel.BaseModel{UUID: "ep2"}, Type: string(datamodel.JobTypeEncryptPool), State: "NEW",
			AccountID: sql.NullInt64{Int64: 2, Valid: true}},
		{BaseModel: datamodel.BaseModel{UUID: "ep3"}, Type: string(datamodel.JobTypeEncryptPool), State: "DONE",
			AccountID: sql.NullInt64{Int64: 3, Valid: true}},
		{BaseModel: datamodel.BaseModel{UUID: "mk1"}, Type: string(datamodel.JobTypeMigrateKmsConfig), State: "NEW",
			AccountID: sql.NullInt64{Int64: 4, Valid: true}},
	}

	err = store.db.Create(jobs).Error()
	if err != nil {
		t.Fatalf("Failed to create Jobs table: %v", err)
	}

	t.Run("WhenProcessingEncryptPoolJobPresent", func(tt *testing.T) {
		result, errQuery := store.GetOngoingEncryptPoolJobForAccount(context.Background(), int64(1))
		assert.NoError(tt, errQuery)
		assert.NotNil(tt, result)
		assert.Equal(tt, "ep1", result.UUID)
	})
	t.Run("WhenNewEncryptPoolJobPresent", func(tt *testing.T) {
		result, errQuery := store.GetOngoingEncryptPoolJobForAccount(context.Background(), int64(2))
		assert.NoError(tt, errQuery)
		assert.NotNil(tt, result)
		assert.Equal(tt, "ep2", result.UUID)
	})
	t.Run("WhenEncryptPoolJobStateIsNeitherNewNorProcessing", func(tt *testing.T) {
		result, errQuery := store.GetOngoingEncryptPoolJobForAccount(context.Background(), int64(3))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
	t.Run("WhenOnlyMigrateJobPresentForAccount", func(tt *testing.T) {
		result, errQuery := store.GetOngoingEncryptPoolJobForAccount(context.Background(), int64(4))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
	t.Run("WhenAccountHasNoJobs", func(tt *testing.T) {
		result, errQuery := store.GetOngoingEncryptPoolJobForAccount(context.Background(), int64(99))
		assert.Nil(tt, result)
		assert.Error(tt, errQuery)
		assert.EqualError(tt, errQuery.(*vsaerrors.CustomError).OriginalErr, "job not found")
	})
}

func TestCreateCMEKJob(t *testing.T) {
	setup := func(tt *testing.T) *DataStoreRepository {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		accounts := []*datamodel.Account{
			{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-1"}, Name: "acct-1"},
			{BaseModel: datamodel.BaseModel{ID: 2, UUID: "acct-2"}, Name: "acct-2"},
			{BaseModel: datamodel.BaseModel{ID: 3, UUID: "acct-3"}, Name: "acct-3"},
		}
		err = store.db.Create(accounts).Error()
		assert.NoError(tt, err, "Failed to create accounts table")

		return store
	}

	t.Run("WhenNoOngoingJob_CreatesJob", func(tt *testing.T) {
		store := setup(tt)
		job := &datamodel.Job{Type: string(datamodel.JobTypeEncryptPool), State: string(datamodel.JobsStateNEW)}

		created, err := store.CreateCMEKJob(context.Background(), int64(1), job)
		assert.NoError(tt, err)
		assert.NotNil(tt, created)
		assert.NotEmpty(tt, created.UUID)
	})

	t.Run("WhenEncryptPoolJobAlreadyOngoing_ReturnsConflict", func(tt *testing.T) {
		store := setup(tt)
		err := store.db.Create(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "ep1"}, Type: string(datamodel.JobTypeEncryptPool),
			State: string(datamodel.JobsStatePROCESSING), AccountID: sql.NullInt64{Int64: 2, Valid: true}}).Error()
		assert.NoError(tt, err)

		job := &datamodel.Job{Type: string(datamodel.JobTypeMigrateKmsConfig), State: string(datamodel.JobsStateNEW)}
		created, errCreate := store.CreateCMEKJob(context.Background(), int64(2), job)
		assert.Nil(tt, created)
		assert.True(tt, customerrors.IsConflictErr(errCreate), "expected a ConflictErr, got %v (%T)", errCreate, errCreate)

		var count int64
		countErr := store.db.GORM().Model(&datamodel.Job{}).Where("account_id = ?", int64(2)).Count(&count).Error
		assert.NoError(tt, countErr)
		assert.Equal(tt, int64(1), count, "no additional job should have been created")
	})

	t.Run("WhenMigrateJobAlreadyOngoing_ReturnsConflict", func(tt *testing.T) {
		store := setup(tt)
		err := store.db.Create(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "mk1"}, Type: string(datamodel.JobTypeMigrateKmsConfig),
			State: string(datamodel.JobsStateNEW), AccountID: sql.NullInt64{Int64: 3, Valid: true}}).Error()
		assert.NoError(tt, err)

		job := &datamodel.Job{Type: string(datamodel.JobTypeEncryptPool), State: string(datamodel.JobsStateNEW)}
		created, errCreate := store.CreateCMEKJob(context.Background(), int64(3), job)
		assert.Nil(tt, created)
		assert.True(tt, customerrors.IsConflictErr(errCreate), "expected a ConflictErr, got %v (%T)", errCreate, errCreate)
	})

	t.Run("WhenOnlyCompletedJobPresent_CreatesJob", func(tt *testing.T) {
		store := setup(tt)
		err := store.db.Create(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "ep-done"}, Type: string(datamodel.JobTypeEncryptPool),
			State: string(datamodel.JobsStateDONE), AccountID: sql.NullInt64{Int64: 1, Valid: true}}).Error()
		assert.NoError(tt, err)

		job := &datamodel.Job{Type: string(datamodel.JobTypeEncryptPool), State: string(datamodel.JobsStateNEW)}
		created, errCreate := store.CreateCMEKJob(context.Background(), int64(1), job)
		assert.NoError(tt, errCreate)
		assert.NotNil(tt, created)
	})

	t.Run("WhenAccountDoesNotExist_ReturnsNotFoundErr", func(tt *testing.T) {
		store := setup(tt)
		job := &datamodel.Job{Type: string(datamodel.JobTypeEncryptPool), State: string(datamodel.JobsStateNEW)}

		created, errCreate := store.CreateCMEKJob(context.Background(), int64(99), job)
		assert.Nil(tt, created)
		assert.True(tt, customerrors.IsNotFoundErr(errCreate), "expected a NotFoundErr, got %v (%T)", errCreate, errCreate)
	})
}

func TestUpdateJobAttributes(t *testing.T) {
	t.Run("SuccessfullyUpdatesJobAttributes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid"},
			JobAttributes: &datamodel.JobAttributes{},
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err)

		newAttrs := &datamodel.JobAttributes{ResourceUUID: "updated"}
		err = store.UpdateJobAttributes(context.Background(), job.UUID, newAttrs)
		assert.NoError(tt, err)

		updatedJob, err := store.GetJob(context.Background(), job.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, "updated", updatedJob.JobAttributes.ResourceUUID)
	})

	t.Run("ReturnsErrorIfJobNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		newAttrs := &datamodel.JobAttributes{}
		err = store.UpdateJobAttributes(context.Background(), "non-existent-uuid", newAttrs)
		assert.Error(tt, err)
	})

	t.Run("ReturnsErrorOnDBFailure", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		newAttrs := &datamodel.JobAttributes{ResourceUUID: "updated"}
		err = store.UpdateJobAttributes(context.Background(), "any-uuid", newAttrs)
		assert.Error(tt, err)
	})
}

func TestCheckAndFetchDuplicateJobs(t *testing.T) {
	t.Run("WhenDuplicateJobExistsWithNewState", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with NEW state and specific correlation ID
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "test-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job")
		assert.Equal(tt, job.UUID, duplicateJob.UUID, "Expected job UUID %v, got %v", job.UUID, duplicateJob.UUID)
		assert.Equal(tt, job.CorrelationID, duplicateJob.CorrelationID, "Expected correlation ID %v, got %v", job.CorrelationID, duplicateJob.CorrelationID)
		assert.Equal(tt, job.Type, duplicateJob.Type, "Expected job type %v, got %v", job.Type, duplicateJob.Type)
	})

	t.Run("WhenDuplicateJobExistsWithProcessingState", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with PROCESSING state and specific correlation ID
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStatePROCESSING),
			CorrelationID: "test-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job")
		assert.Equal(tt, job.UUID, duplicateJob.UUID, "Expected job UUID %v, got %v", job.UUID, duplicateJob.UUID)
		assert.Equal(tt, job.State, duplicateJob.State, "Expected job state %v, got %v", job.State, duplicateJob.State)
	})

	t.Run("WhenNoDuplicateJobExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Check for duplicate job that doesn't exist
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "non-existent-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Nil(tt, duplicateJob, "Expected no duplicate job to be found")
	})

	t.Run("WhenJobExistsButWithDifferentCorrelationID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with different correlation ID
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "different-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job with different correlation ID
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Nil(tt, duplicateJob, "Expected no duplicate job to be found")
	})

	t.Run("WhenJobExistsButWithDifferentJobType", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with different job type
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreatePool)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "test-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job with different job type
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Nil(tt, duplicateJob, "Expected no duplicate job to be found")
	})

	t.Run("WhenJobExistsButWithNonTransientState", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with DONE state (non-transient)
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateDONE),
			CorrelationID: "test-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job - should find it because current implementation returns all jobs regardless of state
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job")
		assert.Equal(tt, job.UUID, duplicateJob.UUID, "Expected job UUID %v, got %v", job.UUID, duplicateJob.UUID)
		assert.Equal(tt, job.State, duplicateJob.State, "Expected job state %v, got %v", job.State, duplicateJob.State)
	})

	t.Run("WhenJobExistsButWithErrorState", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with ERROR state (non-transient)
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateERROR),
			CorrelationID: "test-correlation-id",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job - should find it because current implementation returns all jobs regardless of state
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job")
		assert.Equal(tt, job.UUID, duplicateJob.UUID, "Expected job UUID %v, got %v", job.UUID, duplicateJob.UUID)
		assert.Equal(tt, job.State, duplicateJob.State, "Expected job state %v, got %v", job.State, duplicateJob.State)
	})

	t.Run("WhenMultipleJobsExistWithSameCorrelationIDAndType", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create multiple jobs with same correlation ID and type but different states
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "job-1-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateDONE), // Non-transient state
			CorrelationID: "test-correlation-id",
		}
		job2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "job-2-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateNEW), // Transient state
			CorrelationID: "test-correlation-id",
		}
		job3 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "job-3-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStatePROCESSING), // Transient state
			CorrelationID: "test-correlation-id",
		}

		_, err = store.CreateJob(context.Background(), job1)
		assert.NoError(tt, err, "Failed to create job1: %v", err)
		_, err = store.CreateJob(context.Background(), job2)
		assert.NoError(tt, err, "Failed to create job2: %v", err)
		_, err = store.CreateJob(context.Background(), job3)
		assert.NoError(tt, err, "Failed to create job3: %v", err)

		// Check for duplicate job - should find any job since current implementation returns all jobs regardless of state
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job")
		assert.Contains(tt, []string{job1.UUID, job2.UUID, job3.UUID}, duplicateJob.UUID, "Expected to find any of the jobs")
		assert.Contains(tt, []string{string(datamodel.JobsStateDONE), string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING)}, duplicateJob.State, "Expected to find any job state")
	})

	t.Run("WhenEmptyCorrelationID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a job with empty correlation ID
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "",
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err, "Failed to create job: %v", err)

		// Check for duplicate job with empty correlation ID
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate job with empty correlation ID")
		assert.Equal(tt, job.UUID, duplicateJob.UUID, "Expected job UUID %v, got %v", job.UUID, duplicateJob.UUID)
	})

	t.Run("WhenDifferentJobTypesWithSameCorrelationID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create jobs with different types but same correlation ID
		replicationJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreateVolumeReplication)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "test-correlation-id",
		}
		poolJob := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "pool-job-uuid",
			},
			Type:          string(string(datamodel.JobTypeCreatePool)),
			State:         string(datamodel.JobsStateNEW),
			CorrelationID: "test-correlation-id",
		}

		_, err = store.CreateJob(context.Background(), replicationJob)
		assert.NoError(tt, err, "Failed to create replication job: %v", err)
		_, err = store.CreateJob(context.Background(), poolJob)
		assert.NoError(tt, err, "Failed to create pool job: %v", err)

		// Check for duplicate replication job
		duplicateJob, err := store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreateVolumeReplication), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate replication job")
		assert.Equal(tt, replicationJob.UUID, duplicateJob.UUID, "Expected replication job UUID %v, got %v", replicationJob.UUID, duplicateJob.UUID)
		assert.Equal(tt, string(string(datamodel.JobTypeCreateVolumeReplication)), duplicateJob.Type, "Expected replication job type")

		// Check for duplicate pool job
		duplicateJob, err = store.CheckAndFetchDuplicateJobs(context.Background(), string(datamodel.JobTypeCreatePool), "test-correlation-id")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, duplicateJob, "Expected to find duplicate pool job")
		assert.Equal(tt, poolJob.UUID, duplicateJob.UUID, "Expected pool job UUID %v, got %v", poolJob.UUID, duplicateJob.UUID)
		assert.Equal(tt, string(string(datamodel.JobTypeCreatePool)), duplicateJob.Type, "Expected pool job type")
	})
}

func TestCancelPrepopulateJobsForVolume(t *testing.T) {
	t.Run("WhenActiveJobsExist_CancelsOnlyMatchingJobs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumeUUID := "test-volume-uuid"

		// NEW prepopulate job for target volume - should be cancelled
		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "job-1-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStateNEW),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}
		// PROCESSING prepopulate job for target volume - should be cancelled
		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "job-2-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}
		// DONE prepopulate job for target volume - should NOT be changed
		job3 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 3, UUID: "job-3-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStateDONE),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}
		// NEW prepopulate job for different volume - should NOT be changed
		job4 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 4, UUID: "job-4-uuid"},
			ResourceName: "other-volume-uuid",
			State:        string(datamodel.JobsStateNEW),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}
		// NEW job of different type for target volume - should NOT be changed
		job5 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 5, UUID: "job-5-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStateNEW),
			Type:         string(datamodel.JobTypeCreatePool),
		}

		_, err = store.CreateJob(context.Background(), job1)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), job2)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), job3)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), job4)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), job5)
		assert.NoError(tt, err)

		err = store.CancelPrepopulateJobsForVolume(context.Background(), volumeUUID)
		assert.NoError(tt, err)

		expectedErrorDetails := "Volume test-volume-uuid was deleted, prepopulate job cannot complete"

		// job1 (NEW prepopulate, target volume) → ERROR
		updated1, err := store.GetJob(context.Background(), job1.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateERROR), updated1.State)
		assert.Equal(tt, expectedErrorDetails, updated1.ErrorDetails)

		// job2 (PROCESSING prepopulate, target volume) → ERROR
		updated2, err := store.GetJob(context.Background(), job2.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateERROR), updated2.State)
		assert.Equal(tt, expectedErrorDetails, updated2.ErrorDetails)

		// job3 (DONE prepopulate, target volume) → unchanged
		updated3, err := store.GetJob(context.Background(), job3.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), updated3.State)

		// job4 (NEW prepopulate, different volume) → unchanged
		updated4, err := store.GetJob(context.Background(), job4.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateNEW), updated4.State)

		// job5 (NEW different type, target volume) → unchanged
		updated5, err := store.GetJob(context.Background(), job5.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateNEW), updated5.State)
	})

	t.Run("WhenNoActiveJobsExist_NoError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// No jobs in DB at all
		err = store.CancelPrepopulateJobsForVolume(context.Background(), "non-existent-volume")
		assert.NoError(tt, err, "Expected no error when no jobs match")
	})

	t.Run("WhenOnlyNonActiveJobsExist_NoneChanged", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumeUUID := "test-volume-uuid"

		// DONE and ERROR jobs should not be affected
		doneJob := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "done-job-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStateDONE),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}
		errorJob := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "error-job-uuid"},
			ResourceName: volumeUUID,
			State:        string(datamodel.JobsStateERROR),
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
		}

		_, err = store.CreateJob(context.Background(), doneJob)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), errorJob)
		assert.NoError(tt, err)

		err = store.CancelPrepopulateJobsForVolume(context.Background(), volumeUUID)
		assert.NoError(tt, err)

		// Verify neither was changed
		updatedDone, err := store.GetJob(context.Background(), doneJob.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), updatedDone.State)

		updatedError, err := store.GetJob(context.Background(), errorJob.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateERROR), updatedError.State)
	})

	t.Run("WhenDBIsClosed_ReturnsError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		sqlDB, _ := db.DB()
		err = sqlDB.Close()
		assert.NoError(tt, err, "Failed to close test database")

		err = store.CancelPrepopulateJobsForVolume(context.Background(), "test-volume-uuid")
		assert.Error(tt, err, "Expected an error due to closed DB")
	})
}

func TestCancelRunningJobsForResource(t *testing.T) {
	t.Run("WhenRunningJobsExistForResource", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// NEW and PROCESSING match CancelRunningJobsForResource; terminal states do not.
		jobDone := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "job-done-uuid"},
			ResourceName: "test-resource",
			State:        string(datamodel.JobsStateDONE),
			Type:         string(datamodel.JobTypeFlexCacheCreateVolume),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "resource-uuid",
			},
		}
		jobProcessing := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "job-processing-uuid"},
			ResourceName: "test-resource",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(datamodel.JobTypeFlexCacheInternalPeering),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "resource-uuid",
			},
		}
		jobNew := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 3, UUID: "job-new-uuid"},
			ResourceName: "test-resource",
			State:        string(datamodel.JobsStateNEW),
			Type:         string(datamodel.JobTypeFlexCacheCreateVolume),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "resource-uuid",
			},
		}
		_, err = store.CreateJob(context.Background(), jobDone)
		assert.NoError(tt, err, "Failed to create jobDone: %v", err)
		_, err = store.CreateJob(context.Background(), jobProcessing)
		assert.NoError(tt, err, "Failed to create jobProcessing: %v", err)
		_, err = store.CreateJob(context.Background(), jobNew)
		assert.NoError(tt, err, "Failed to create jobNew: %v", err)

		err = store.CancelRunningJobsForResource(context.Background(), "resource-uuid")
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedDone, err := store.GetJob(context.Background(), jobDone.UUID)
		assert.NoError(tt, err, "Failed to get updated jobDone: %v", err)
		assert.Equal(tt, string(datamodel.JobsStateDONE), updatedDone.State, "Expected DONE job unchanged")

		updatedProcessing, err := store.GetJob(context.Background(), jobProcessing.UUID)
		assert.NoError(tt, err, "Failed to get updated jobProcessing: %v", err)
		assert.Equal(tt, string(datamodel.JobsStateCANCELLED), updatedProcessing.State, "Expected PROCESSING job cancelled")

		updatedNew, err := store.GetJob(context.Background(), jobNew.UUID)
		assert.NoError(tt, err, "Failed to get updated jobNew: %v", err)
		assert.Equal(tt, string(datamodel.JobsStateCANCELLED), updatedNew.State, "Expected NEW job cancelled")
	})

	t.Run("WhenErrorOccursDuringUpdate", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		sqlDB, _ := db.DB()
		err = sqlDB.Close()
		assert.NoError(tt, err, "Failed to close test database")

		err = store.CancelRunningJobsForResource(context.Background(), "non-existent-resource-uuid")
		assert.Error(tt, err, "Expected an error due to closed DB")
	})
}

func TestGetJobByResourceUUID(t *testing.T) {
	t.Run("WhenJobFoundViaJSONB", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "job-uuid-jsonb"},
			Type:      string(datamodel.JobTypeCreatePool),
			State:     string(datamodel.JobsStateNEW),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "res-jsonb",
			},
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err)

		result, err := store.GetJobByResourceUUID(context.Background(), "res-jsonb", string(datamodel.JobTypeCreatePool))
		assert.NoError(tt, err)
		assert.Equal(tt, job.UUID, result.UUID)
	})

	t.Run("WhenEnableJobResourceUUIDIndex_JobFoundViaColumn", func(tt *testing.T) {
		vcputils.EnableJobResourceUUIDIndex = true
		defer func() { vcputils.EnableJobResourceUUIDIndex = false }()

		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "job-uuid-col"},
			Type:      string(datamodel.JobTypeCreatePool),
			State:     string(datamodel.JobsStateNEW),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "res-col",
			},
		}
		_, err = store.CreateJob(context.Background(), job)
		assert.NoError(tt, err)

		result, err := store.GetJobByResourceUUID(context.Background(), "res-col", string(datamodel.JobTypeCreatePool))
		assert.NoError(tt, err)
		assert.Equal(tt, job.UUID, result.UUID)
	})

	t.Run("WhenJobNotFound_ReturnsNotFoundError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		_, err = store.GetJobByResourceUUID(context.Background(), "non-existent-uuid", "")
		assert.Error(tt, err)
	})
}

func TestCancelRunningJobsForResource_WithIndexFlag(t *testing.T) {
	t.Run("WhenEnableJobResourceUUIDIndex_CancelsViaColumn", func(tt *testing.T) {
		vcputils.EnableJobResourceUUIDIndex = true
		defer func() { vcputils.EnableJobResourceUUIDIndex = false }()

		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		jobProcessing := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "job-uuid-idx-processing"},
			ResourceName: "test-resource",
			State:        string(datamodel.JobsStatePROCESSING),
			Type:         string(datamodel.JobTypeFlexCacheCreateVolume),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "resource-uuid-idx",
			},
		}
		jobNew := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "job-uuid-idx-new"},
			ResourceName: "test-resource",
			State:        string(datamodel.JobsStateNEW),
			Type:         string(datamodel.JobTypeFlexCacheInternalPeering),
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "resource-uuid-idx",
			},
		}
		_, err = store.CreateJob(context.Background(), jobProcessing)
		assert.NoError(tt, err)
		_, err = store.CreateJob(context.Background(), jobNew)
		assert.NoError(tt, err)

		err = store.CancelRunningJobsForResource(context.Background(), "resource-uuid-idx")
		assert.NoError(tt, err)

		updatedProcessing, err := store.GetJob(context.Background(), jobProcessing.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateCANCELLED), updatedProcessing.State)

		updatedNew, err := store.GetJob(context.Background(), jobNew.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateCANCELLED), updatedNew.State)
	})
}

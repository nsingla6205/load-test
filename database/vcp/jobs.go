package database

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	getJobWithDetails = _getJobWithDetails
)

func (d *DataStoreRepository) CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error) {
	return d.createJobImpl(ctx, job, "")
}

// CreateJobWithWorkflowID persists a job with workflow_id set to workflowID (FlexCache child jobs).
func (d *DataStoreRepository) CreateJobWithWorkflowID(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
	return d.createJobImpl(ctx, job, workflowID)
}

func (d *DataStoreRepository) createJobImpl(ctx context.Context, job *datamodel.Job, workflowID string) (*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	if job.UUID == "" {
		job.UUID = utils.RandomUUID()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	if workflowID != "" {
		job.WorkflowID = workflowID
	} else {
		job.WorkflowID = job.UUID
	}
	if job.JobAttributes != nil {
		job.ResourceUUID = job.JobAttributes.ResourceUUID
	}

	if err := tx.Create(job).Error; err != nil {
		return nil, err
	}

	dbJob, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: job.UUID}})
	if err != nil {
		return nil, err
	}

	return dbJob, nil
}

func (d *DataStoreRepository) GetJob(ctx context.Context, id string) (*datamodel.Job, error) {
	return getJobWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
}

func (d *DataStoreRepository) UpdateJob(ctx context.Context, id, status string, trackingID int, errorDetails string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
	if err != nil {
		return err
	}

	job.UpdatedAt = time.Now()
	job.State = status
	job.TrackingID = trackingID
	job.ErrorDetails = errorDetails
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (d *DataStoreRepository) UpdateJobAttributes(ctx context.Context, uuid string, jobAttributes *datamodel.JobAttributes) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: uuid}})
	if err != nil {
		return err
	}
	job.UpdatedAt = time.Now()
	job.JobAttributes = jobAttributes
	if jobAttributes != nil {
		job.ResourceUUID = jobAttributes.ResourceUUID
	}
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (d *DataStoreRepository) DeleteJob(ctx context.Context, id, errorDetails string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	job, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: id}})
	if err != nil {
		return err
	}

	job.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	job.State = string(datamodel.JobsStateERROR)
	job.ErrorDetails = errorDetails
	if err = tx.Updates(job).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (d *DataStoreRepository) ListOngoingPoolJobsWithKmsConfigId(ctx context.Context, kmsId, accountId int64) ([]*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	jobs := make([]*datamodel.Job, 0)

	err := db.Joins("INNER JOIN pools on pools.name = jobs.resource_name").Where("jobs.state = ? and jobs.type IN (?, ?) and pools.kms_config_id = ? and pools.account_id = ?", datamodel.JobsStatePROCESSING, datamodel.JobTypeCreatePool, datamodel.JobTypeCreateLargePool, kmsId, accountId).Find(&jobs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return jobs, nil
}

func (d *DataStoreRepository) GetJobsWithCondition(ctx context.Context, filter utils2.Filter) ([]*datamodel.Job, error) {
	db := d.db.ApplyFilter(filter.Apply()).GORM().WithContext(ctx)
	jobs := make([]*datamodel.Job, 0)
	err := db.Find(&jobs).Error
	if err != nil {
		return nil, err
	}
	return jobs, nil
}

// GetJobByResourceUUID retrieves the job by its resource UUID.
// When utils.EnableJobResourceUUIDIndex is true, queries the indexed resource_uuid column.
// Otherwise uses the JSONB path job_attributes->>'resource_uuid'.
func (d *DataStoreRepository) GetJobByResourceUUID(ctx context.Context, resourceUUID string, jobType string) (*datamodel.Job, error) {
	job := &datamodel.Job{}
	var query *gorm.DB
	if utils.EnableJobResourceUUIDIndex {
		query = d.db.GORM().WithContext(ctx).Where("resource_uuid = ?", resourceUUID)
	} else {
		query = d.db.GORM().WithContext(ctx).Where("job_attributes ->> 'resource_uuid' = ?", resourceUUID)
	}
	if jobType != "" {
		query = query.Where("type = ?", jobType)
	}

	err := query.First(job).Error
	if err != nil {
		return nil, errors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "Job", nil)
	}
	return job, nil
}

func _getJobWithDetails(db *gorm.DB, query *datamodel.Job) (*datamodel.Job, error) {
	job := &datamodel.Job{}
	err := db.First(&job, query).Error
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return job, nil
}

func (d *DataStoreRepository) GetOngoingMigrateKmsConfigJob(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().WithContext(ctx).Where(
		"account_id = ? AND type = ? AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeMigrateKmsConfig, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

func (d *DataStoreRepository) GetOngoingEncryptPoolJobForAccount(ctx context.Context, accountId int64) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().WithContext(ctx).Where(
		"account_id = ? AND type = ? AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeEncryptPool, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

func cmekJobConflictMessage(jobType string) string {
	switch jobType {
	case string(datamodel.JobTypeEncryptPool):
		return "A pool encryption (EncryptPool) is already in progress for this account"
	case string(datamodel.JobTypeMigrateKmsConfig):
		return "A CMEK migration (EncryptVolumes) is already in progress for this account"
	default:
		return fmt.Sprintf("a %s job is already in progress for this account", jobType)
	}
}

// CreateCMEKJob atomically enforces the account-wide invariant that at most one CMEK job
// (ENCRYPT_POOL or MIGRATE_KMS_CONFIG) may be NEW/PROCESSING per account at a time,
func (d *DataStoreRepository) CreateCMEKJob(ctx context.Context, accountId int64, job *datamodel.Job) (*datamodel.Job, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var account datamodel.Account
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", accountId).
		First(&account).Error; err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			err = errors.NewNotFoundErr("account", nil)
		}
		return nil, err
	}

	var ongoing datamodel.Job
	findErr := tx.Where(
		"account_id = ? AND type IN (?, ?) AND (state = ? OR state = ?)",
		accountId, datamodel.JobTypeEncryptPool, datamodel.JobTypeMigrateKmsConfig,
		datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING,
	).First(&ongoing).Error
	if findErr == nil {
		err = errors.NewConflictErr(cmekJobConflictMessage(ongoing.Type))
		return nil, err
	}
	if !vsaerrors.Is(findErr, gorm.ErrRecordNotFound) {
		err = findErr
		return nil, err
	}

	if job.UUID == "" {
		job.UUID = utils.RandomUUID()
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = job.CreatedAt
	job.WorkflowID = job.UUID
	if job.JobAttributes != nil {
		job.ResourceUUID = job.JobAttributes.ResourceUUID
	}

	if err = tx.Create(job).Error; err != nil {
		return nil, err
	}

	dbJob, err := getJobWithDetails(tx, &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: job.UUID}})
	if err != nil {
		return nil, err
	}

	return dbJob, nil
}

func (d *DataStoreRepository) CheckAndFetchDuplicateJobs(ctx context.Context, jobType string, correlationID string) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().Unscoped().WithContext(ctx).Where(
		"correlation_id = ? AND type = ?",
		correlationID, jobType,
	).First(&job).Error

	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			// do not return any error if no job is found
			return nil, nil
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &job, nil
}

func (d *DataStoreRepository) CancelRunningJobsForResource(ctx context.Context, resourceUUID string) error {
	db := d.db.GORM().WithContext(ctx)
	var whereClause string
	if utils.EnableJobResourceUUIDIndex {
		whereClause = "resource_uuid = ? AND state IN (?, ?)"
	} else {
		whereClause = "job_attributes ->> 'resource_uuid' = ? AND state IN (?, ?)"
	}
	err := db.Model(&datamodel.Job{}).
		Where(whereClause, resourceUUID, datamodel.JobsStateNEW, datamodel.JobsStatePROCESSING).
		Update("state", string(datamodel.JobsStateCANCELLED)).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// CancelPrepopulateJobsForVolume cancels all active prepopulate jobs for a given volume UUID
// This is called when a volume is deleted to prevent orphaned prepopulate jobs
func (d *DataStoreRepository) CancelPrepopulateJobsForVolume(ctx context.Context, volumeUUID string) error {
	db := d.db.GORM().WithContext(ctx)
	errorDetails := fmt.Sprintf("Volume %s was deleted, prepopulate job cannot complete", volumeUUID)

	// Update all active prepopulate jobs for this volume to ERROR state
	err := db.Model(&datamodel.Job{}).
		Where("type = ? AND resource_name = ? AND state IN ?",
			datamodel.JobTypeFlexCachePrePopulate,
			volumeUUID,
			[]string{string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING)}).
		Updates(map[string]interface{}{
			"state":         string(datamodel.JobsStateERROR),
			"error_details": errorDetails,
		}).Error

	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

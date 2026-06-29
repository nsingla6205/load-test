package jobmanageractivities

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows/background_kms_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const (
	SyncVsaSnapshots                = "SYNC_VSA_SNAPSHOTS"
	RotateKmsServiceAccounts        = "ROTATE_KMS_SERVICE_ACCOUNTS"
	RotateVsaCertificateAndPassword = "ROTATE_VSA_CERTIFICATE_AND_PASSWORD"
	VolumeDetailsTotal              = "VOLUME_DETAILS_TOTAL"
	OrphanJobScheduler              = "ORPHANED_JOB_SCHEDULER"
	SyncLatestBackupLogicalSize     = "SYNC_LATEST_BACKUP_LOGICAL_SIZE"
	HardDeleteResourcesAndAccount   = "HARD_DELETE_RESOURCES_AND_ACCOUNT"
	CleanupHydratedMetricsTable     = "CLEANUP_HYDRATED_METRICS_TABLE"
	CleanupAggregatedUsageTable     = "CLEANUP_AGGREGATED_USAGE_TABLE"
	CleanupJobsTable                = "CLEANUP_JOBS_TABLE"
	CleanupBackupChainHistory       = "CLEANUP_BACKUP_CHAIN_HISTORY"
	SyncVsaAutoTiering              = "SYNC_VSA_AUTO_TIERING"
	DeleteResources                 = "DELETE_RESOURCES"
	SyncBackupZiZsMetadata          = "SYNC_BACKUP_ZIZS_METADATA"
	SyncPoolCompliance              = "SYNC_POOL_COMPLIANCE"
	EligibilityStringJob            = "ELIGIBILITY_STRING_JOB"
	FlexCachePrepopulate            = "SYNC_FLEXCACHE_PREPOPULATE_JOBS"
	BackupSizeJob                   = "BACKUP_SIZE_JOB"
	PollerRebalanceJob              = "POLLER_REBALANCE_JOB"
	HarvestPollerDriftReconcileJob  = "HARVEST_POLLER_DRIFT_RECONCILE_JOB"
	TrialAccountSync                = "TRIAL_ACCOUNT_SYNC"
	SyncExpertModeVolumes           = "SYNC_EXPERT_MODE_VOLUMES"
)

// JobTypeToWorkflow maps job types to their corresponding workflow functions.
var JobTypeToWorkflow = map[string]interface{}{
	SyncVsaSnapshots:                backgroundworkflows.SnapshotsSyncParentWorkflow,
	RotateKmsServiceAccounts:        background_kms_workflows.RotateKmsSAKeyWorkflow,
	RotateVsaCertificateAndPassword: backgroundworkflows.RotateVsaCertificateAndPasswordWorkflow,
	VolumeDetailsTotal:              backgroundworkflows.VolumeDetailsWorkflow,
	OrphanJobScheduler:              backgroundworkflows.OrphanJobSchedulerWorkflow,
	SyncLatestBackupLogicalSize:     backgroundworkflows.SyncLatestBackupLogicalSizeWorkflow,
	HardDeleteResourcesAndAccount:   backgroundworkflows.HardDeleteResourcesAndAccountWorkflow,
	CleanupHydratedMetricsTable:     backgroundworkflows.CleanupHydratedMetricsTableWorkflow,
	CleanupAggregatedUsageTable:     backgroundworkflows.CleanupAggregatedUsageTableWorkflow,
	CleanupJobsTable:                backgroundworkflows.CleanupJobsTableWorkflow,
	CleanupBackupChainHistory:       backgroundworkflows.CleanupBackupChainHistoryWorkflow,
	SyncVsaAutoTiering:              backgroundworkflows.SyncVSAAutoTieringWorkflow,
	DeleteResources:                 backgroundworkflows.ResourceCleanupParentWorkflow,
	SyncBackupZiZsMetadata:          backgroundworkflows.SyncBackupZiZsWorkflow,
	SyncPoolCompliance:              backgroundworkflows.SyncPoolZIZSDetailsWorkflow,
	EligibilityStringJob:            backgroundworkflows.EligibilityStringWorkflow,
	FlexCachePrepopulate:            backgroundworkflows.SyncFlexCachePrepopulateWorkflow,
	BackupSizeJob:                   backgroundworkflows.BackupSizeDetailsWorkflow,
	PollerRebalanceJob:              backgroundworkflows.PollerRebalanceWorkflow,
	HarvestPollerDriftReconcileJob:  backgroundworkflows.HarvestPollerDriftReconcileWorkflow,
	TrialAccountSync:                backgroundworkflows.TrialAccountSyncWorkflow,
	SyncExpertModeVolumes:           backgroundworkflows.ExpertModeVolumeSyncParentWorkflow,
}

type JobManagerActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

func (j *JobManagerActivity) CreateScheduleActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	se := j.SE
	jobs, err := se.GetAdminJobSpecsByState(ctx, scheduler.JobStatusCreating)
	if err != nil {
		logger.Errorf("Failed to fetch admin jobs by state: %v", err)
		return err
	}

	for _, job := range jobs {
		workflowFunc, ok := JobTypeToWorkflow[job.JobType]
		if !ok {
			logger.Warnf("No workflow function found for job type: %s", job.JobType)
			continue
		}

		createParams := scheduler.CreateScheduleParams{
			ScheduleParams: scheduler.ScheduleParams{
				ScheduleID: job.UUID,
			},
			TemporalScheduleOptions: scheduler.TemporalCreateScheduleParams{
				WorkflowID: job.UUID,
				Workflow:   workflowFunc,
				Spec: client.ScheduleSpec{
					CronExpressions: []string{job.CronExpression},
				},
			},
		}

		_, err := j.Scheduler.Create(ctx, createParams)
		if err != nil {
			logger.Errorf("Failed to create schedule for job type %s: %v", job.JobType, err)
			return err
		}

		err = updateJobSpecState(ctx, se, job)
		if err != nil {
			return err
		}
	}

	return nil
}

func (j *JobManagerActivity) UpdateScheduleActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	se := j.SE
	jobs, err := se.GetAdminJobSpecsByState(ctx, scheduler.JobStatusUpdating)
	if err != nil {
		logger.Errorf("Failed to fetch admin jobs by state: %v", err)
		return err
	}

	for _, job := range jobs {
		updateParams := scheduler.UpdateScheduleParams{
			ScheduleParams: scheduler.ScheduleParams{
				ScheduleID: job.UUID,
				Args:       nil,
			},
			TemporalScheduleOptions: scheduler.TemporalUpdateScheduleParams{
				Spec: client.ScheduleSpec{
					CronExpressions: []string{job.CronExpression},
				},
			},
		}

		_, err := j.Scheduler.Update(ctx, updateParams)
		if err != nil {
			logger.Errorf("Failed to update schedule for job type %s: %v", job.JobType, err)
			return err
		}

		err = updateJobSpecState(ctx, se, job)
		if err != nil {
			return err
		}
	}

	return nil
}

func (j *JobManagerActivity) DeleteScheduleActivity(ctx context.Context) error {
	logger := util.GetLogger(ctx)

	se := j.SE
	jobs, err := se.GetAdminJobSpecsByState(ctx, scheduler.JobStatusDeleting)
	if err != nil {
		logger.Errorf("Failed to fetch admin jobs by state: %v", err)
		return err
	}

	for _, job := range jobs {
		deleteParams := scheduler.DeleteScheduleParams{
			ScheduleParams: scheduler.ScheduleParams{
				ScheduleID: job.UUID,
			},
		}

		_, err := j.Scheduler.Delete(ctx, deleteParams)
		if err != nil {
			logger.Errorf("Failed to delete schedule for job type %s: %v", job.JobType, err)
			return err
		}

		err = updateJobSpecState(ctx, se, job)
		if err != nil {
			return err
		}
	}

	return nil
}

func updateJobSpecState(ctx context.Context, se database.Storage, adminJobSpec *datamodel.AdminJobSpec) error {
	logger := util.GetLogger(ctx)

	switch adminJobSpec.State {
	case scheduler.JobStatusCreating, scheduler.JobStatusUpdating:
		adminJobSpec.State = scheduler.JobStatusScheduled
	case scheduler.JobStatusDeleting:
		adminJobSpec.State = scheduler.JobStatusDeleted
		adminJobSpec.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	}

	err := se.UpdateAdminJobSpec(ctx, adminJobSpec)
	if err != nil {
		logger.Error("Failed to update admin adminJobSpec state", "Error", err)
		return err
	}

	return nil
}

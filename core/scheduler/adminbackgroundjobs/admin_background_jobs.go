package adminbackgroundjobs

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/jobmanagerworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflow_engine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

//go:embed admin_background_jobs.json
var data []byte

// adminJobSpecs stores the in-memory admin job specifications (read from admin_background_jobs.json).
// To schedule a new admin job, add it to the admin_background_jobs.json file with the state set to CREATING.
// To update a job's cron expression, modify the cron expression in admin_background_jobs.json and set the state to UPDATING.
// To delete a job, set its state to DELETING in admin_background_jobs.json.
// On application deployment, these in-memory job specs are synchronized with the database and Temporal Server.
// Usage Documentation: https://confluence.ngage.netapp.com/display/VSCP/Guide%3A+How+to+Create+and+Alter+Admin+Job+Schedules
var adminJobSpecs map[string]*datamodel.AdminJobSpec

// LoadJobSpecs loads the admin job specifications from the embedded JSON file into memory.
// It unmarshals the JSON data into the adminJobSpecs map. Returns an error if unmarshalling fails.
func LoadJobSpecs() error {
	err := json.Unmarshal(data, &adminJobSpecs)
	if err != nil {
		return err
	}

	applyHarvestPollerDriftReconcileConfig()
	// Override DELETE_RESOURCES cron expression from environment variable if set
	if deleteResourcesSpec, exists := adminJobSpecs["DELETE_RESOURCES"]; exists {
		if envCronExpr := env.GetString("DELETE_RESOURCES_CRON_EXPRESSION", ""); envCronExpr != "" {
			deleteResourcesSpec.CronExpression = envCronExpr
		}
	}

	// Override ROTATE_VSA_CERTIFICATE_AND_PASSWORD cron expression from environment variable if set
	if certRotationSpec, exists := adminJobSpecs["ROTATE_VSA_CERTIFICATE_AND_PASSWORD"]; exists {
		if envCronExpr := env.GetString("CERTIFICATE_ROTATION_CRON_EXPRESSION", ""); envCronExpr != "" {
			certRotationSpec.CronExpression = envCronExpr
		}
	}

	// Remove the SYNC_AUTO_TIERING_POOLS job spec if auto tiering feature is not enabled.
	if !env.GetBool("AUTO_TIERING_ENABLED", false) {
		delete(adminJobSpecs, "SYNC_VSA_AUTO_TIERING")
	}

	// Remove metrics database cleanup job specs if metrics DB cleanup is not enabled.
	if !env.GetBool("METRICS_DB_CLEANUP_ENABLED", false) {
		delete(adminJobSpecs, "CLEANUP_HYDRATED_METRICS_TABLE")
		delete(adminJobSpecs, "CLEANUP_AGGREGATED_USAGE_TABLE")
		delete(adminJobSpecs, "CLEANUP_JOBS_TABLE")
		delete(adminJobSpecs, "CLEANUP_BACKUP_CHAIN_HISTORY")
	}

	if !env.GetBool("ENABLE_POLLER_REBALANCING", false) {
		delete(adminJobSpecs, "POLLER_REBALANCE_JOB")
	}

	if !env.GetBool("TRIAL_ACCOUNT_SYNC_ENABLED", false) {
		delete(adminJobSpecs, "TRIAL_ACCOUNT_SYNC")
	}

	// TRIAL_ACCOUNT_SYNC_CRON_EXPRESSION overrides admin_background_jobs.json when non-empty (google-proxy Helm default: "0 * * * *").
	if trialSyncSpec, exists := adminJobSpecs["TRIAL_ACCOUNT_SYNC"]; exists {
		if envCronExpr := strings.TrimSpace(env.GetString("TRIAL_ACCOUNT_SYNC_CRON_EXPRESSION", "")); envCronExpr != "" {
			trialSyncSpec.CronExpression = envCronExpr
		}
	}

	return nil
}

func applyHarvestPollerDriftReconcileConfig() {
	const jobType = "HARVEST_POLLER_DRIFT_RECONCILE_JOB"

	// Feature-gate schedule creation for this new reconciliation job.
	if !env.GetBool("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE", false) {
		delete(adminJobSpecs, jobType)
		return
	}

	spec, exists := adminJobSpecs[jobType]
	if !exists {
		return
	}

	cronExpr := strings.TrimSpace(env.GetString("HARVEST_RECONCILE_CRON_EXPRESSION", spec.CronExpression))
	timezone := strings.TrimSpace(env.GetString("HARVEST_RECONCILE_TIMEZONE", "Asia/Kolkata"))

	// Temporal supports CRON_TZ prefix in cron expressions.
	spec.CronExpression = fmt.Sprintf("CRON_TZ=%s %s", timezone, cronExpr)
}

// LaunchJobManagerWorkflow initiates the background job manager workflow using the Temporal client.
// The workflow is started in the background-workflows task queue and returns an error if it fails to launch.
func LaunchJobManagerWorkflow(ctx context.Context, temporalClient client.Client, logger log.Logger) error {
	_, err := temporalClient.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:                workflow_engine.BackgroundTaskQueue,
			ID:                       scheduler.JobManagerWorkflowID,
			WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_FAIL,
			WorkflowRunTimeout:       workflow_engine.GetWorkflowGlobalTimeout(),
		},
		jobmanagerworkflows.JobManagerWorkflow,
	)
	if err != nil {
		logger.Errorf("Failed to start background job manager workflow: %v", err)
		return err
	}
	return nil
}

// cleanupSingleJobSpec handles the cleanup of a single admin job spec by deleting its Temporal schedule
// and marking it as deleted in the database. Returns an error if the cleanup fails.
func cleanupSingleJobSpec(ctx context.Context, se database.Storage, temporal client.Client, spec *datamodel.AdminJobSpec, logger log.Logger) error {
	// Initialize a Temporal scheduler using the provided Temporal client
	temporalScheduler := scheduler.NewTemporalScheduler(temporal.ScheduleClient())

	// Attempt to delete the corresponding Temporal schedule if present
	_, delErr := temporalScheduler.Delete(ctx, scheduler.DeleteScheduleParams{
		ScheduleParams: scheduler.ScheduleParams{ScheduleID: spec.UUID},
	})
	if delErr != nil {
		// Log and continue to mark the spec as deleted to ensure no schedules remain
		logger.Errorf("Failed to delete schedule for jobType %s (UUID: %s): %v", spec.JobType, spec.UUID, delErr)
	}

	// Mark the admin job spec as deleted (soft delete)
	now := time.Now()
	spec.State = scheduler.JobStatusDeleted
	spec.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
	if updErr := se.UpdateAdminJobSpec(ctx, spec); updErr != nil {
		return fmt.Errorf("failed to mark admin job spec as deleted for jobType %s: %w", spec.JobType, updErr)
	}

	return nil
}

// DeleteAllAdminSchedules deletes all existing admin job schedules in Temporal and
// marks their corresponding AdminJobSpec records as DELETED with a soft delete.
// This is used when RefreshAdminJobSpecs is set to false so that no admin schedules remain active.
// The function continues processing even if individual cleanup operations fail, collecting all errors
// and returning them as a composite error at the end.
func DeleteAllAdminSchedules(ctx context.Context, se database.Storage, temporal client.Client, logger log.Logger) error {
	var errors []error

	statesToCleanup := []string{
		scheduler.JobStatusScheduled,
		scheduler.JobStatusCreating,
		scheduler.JobStatusUpdating,
		scheduler.JobStatusDeleting,
	}

	for _, state := range statesToCleanup {
		adminJobSpecs, err := se.GetAdminJobSpecsByState(ctx, state)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to fetch specs for state %s: %w", state, err))
			continue
		}

		for _, spec := range adminJobSpecs {
			if err := cleanupSingleJobSpec(ctx, se, temporal, spec, logger); err != nil {
				errors = append(errors, fmt.Errorf("failed to cleanup job %s: %w", spec.JobType, err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("cleanup completed with errors: %v", errors)
	}

	logger.Info("All admin schedules have been deleted and specs marked as DELETED")
	return nil
}

// RecreateAdminSchedules deletes any existing Temporal schedules for admin jobs without marking
// DB specs as deleted, then ensures that specs from the embedded JSON are present in DB with
// state CREATING so that they will be recreated by the JobManager workflow.
func RecreateAdminSchedules(ctx context.Context, se database.Storage, temporal client.Client, logger log.Logger) error {
	// Initialize a Temporal scheduler using the provided Temporal client
	temporalScheduler := scheduler.NewTemporalScheduler(temporal.ScheduleClient())

	// 1) Best-effort delete of any existing schedules for non-deleted specs
	statesToReset := []string{
		scheduler.JobStatusScheduled,
		scheduler.JobStatusCreating,
		scheduler.JobStatusUpdating,
	}

	for _, state := range statesToReset {
		adminSpecs, err := se.GetAdminJobSpecsByState(ctx, state)
		if err != nil {
			logger.Errorf("Failed to fetch admin job specs by state %s: %v", state, err)
			return err
		}

		for _, spec := range adminSpecs {
			_, delErr := temporalScheduler.Delete(ctx, scheduler.DeleteScheduleParams{
				ScheduleParams: scheduler.ScheduleParams{ScheduleID: spec.UUID},
			})
			if delErr != nil {
				// Log and continue; we still want to reset DB state to CREATING
				logger.Errorf("Failed to delete schedule for jobType %s (UUID: %s): %v", spec.JobType, spec.UUID, delErr)
			}
		}
	}

	// 2) Upsert/refresh DB specs from embedded JSON, forcing state to CREATING so schedules will be recreated
	for jobType, latestSpec := range adminJobSpecs {
		existingJob, err := se.GetAdminJobSpecByJobType(ctx, jobType)
		if err != nil {
			if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
				// Create new spec
				latestSpec.UUID = uuid.NewString()
				latestSpec.State = scheduler.JobStatusCreating
				latestSpec.DeletedAt = nil
				if _, createErr := se.CreateAdminJobSpec(ctx, latestSpec); createErr != nil {
					logger.Errorf("Failed to create admin job spec for jobType %s: %v", jobType, createErr)
					return createErr
				}
				logger.Infof("Created admin job spec for jobType %s with state CREATING", jobType)
				continue
			}
			logger.Errorf("Failed to get existing job spec for jobType %s: %v", jobType, err)
			return err
		}

		// Update existing spec to force re-creation
		latestSpec.ID = existingJob.ID
		// Preserve existing UUID so schedule ID remains stable across versions
		if latestSpec.UUID == "" {
			latestSpec.UUID = existingJob.UUID
		}
		latestSpec.State = scheduler.JobStatusCreating
		latestSpec.DeletedAt = nil
		if updErr := se.UpdateAdminJobSpec(ctx, latestSpec); updErr != nil {
			logger.Errorf("Failed to update admin job spec to CREATING for jobType %s: %v", jobType, updErr)
			return updErr
		}
		logger.Infof("Updated admin job spec to CREATING for jobType %s", jobType)
	}

	logger.Info("All relevant admin schedules deleted and specs set to CREATING for re-creation")
	return nil
}

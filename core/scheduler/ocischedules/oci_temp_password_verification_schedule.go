package ocischedules

import (
	"context"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
)

const OciTempPasswordVerificationScheduleID = "temp-password-verification"

// EnsureOciTempPasswordVerificationSchedule idempotently reconciles the Temporal
// Schedule that triggers the OCI temp-password verification dispatcher
// (VerifyOciTempPasswordsWorkflow) on the customer task queue. It is safe to call
// on every startup.
//
// Behavior:
//   - ENABLE_OCI_TEMP_PASSWORD_RESET_CRON=false: best-effort delete of any
//     existing schedule so it stops firing.
//   - ENABLE_OCI_TEMP_PASSWORD_RESET_CRON=true: create the schedule if missing,
//     otherwise sync its cron expression so config changes take effect on redeploy.
//
// A lookup failure other than NotFound aborts the reconcile rather than assuming
// the schedule is absent, so a transient error cannot silently skip the delete.
func EnsureOciTempPasswordVerificationSchedule(ctx context.Context, temporalClient client.Client, logger log.Logger) error {
	scheduleClient := temporalClient.ScheduleClient()
	temporalScheduler := scheduler.NewTemporalScheduler(scheduleClient)

	exists, err := scheduleExists(ctx, scheduleClient, OciTempPasswordVerificationScheduleID)
	if err != nil {
		logger.Error("Failed to look up OCI temp-password verification schedule", "error", err)
		return err
	}

	if !env.EnableOCITempPasswordResetCron {
		if !exists {
			logger.Info("OCI temp-password verification disabled; no schedule to remove")
			return nil
		}
		if _, err := temporalScheduler.Delete(ctx, scheduler.DeleteScheduleParams{
			ScheduleParams: scheduler.ScheduleParams{ScheduleID: OciTempPasswordVerificationScheduleID},
		}); err != nil {
			logger.Error("Failed to remove OCI temp-password verification schedule after disable", "error", err)
			return err
		}
		logger.Info("OCI temp-password verification disabled; removed existing schedule")
		return nil
	}

	cron := env.GetOCITempPasswordVerificationCronExpression()
	spec := client.ScheduleSpec{CronExpressions: []string{cron}}

	if exists {
		if err := syncScheduleSpecAndTaskQueue(ctx, scheduleClient, OciTempPasswordVerificationScheduleID, spec, workflowengine.CustomerTaskQueue); err != nil {
			logger.Error("Failed to sync OCI temp-password verification schedule", "error", err)
			return err
		}
		logger.Info("OCI temp-password verification schedule in sync", "scheduleID", OciTempPasswordVerificationScheduleID, "cron", cron, "taskQueue", workflowengine.CustomerTaskQueue)
		return nil
	}

	if _, err := temporalScheduler.Create(ctx, scheduler.CreateScheduleParams{
		ScheduleParams: scheduler.ScheduleParams{ScheduleID: OciTempPasswordVerificationScheduleID},
		TemporalScheduleOptions: scheduler.TemporalCreateScheduleParams{
			WorkflowID: OciTempPasswordVerificationScheduleID,
			Workflow:   common.VerifyOciTempPasswordsWorkflowName,
			TaskQueue:  workflowengine.CustomerTaskQueue,
			Spec:       spec,
		},
	}); err != nil {
		// Tolerate a create race / Describe false-negative: another instance may
		// have created it concurrently.
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			logger.Info("OCI temp-password verification schedule already exists", "scheduleID", OciTempPasswordVerificationScheduleID)
			return nil
		}
		logger.Error("Failed to create OCI temp-password verification schedule", "error", err)
		return err
	}
	logger.Info("Created OCI temp-password verification schedule", "scheduleID", OciTempPasswordVerificationScheduleID, "cron", cron)
	return nil
}

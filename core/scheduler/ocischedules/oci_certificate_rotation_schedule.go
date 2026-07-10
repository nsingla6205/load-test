// Package ocischedules creates and reconciles Temporal Schedules for OCI background
// maintenance that must run on the customer task queue. OCI production deploys only a
// customer worker (no background worker), so these schedules cannot use the
// background-bound admin-job framework.
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

// OciCertificateRotationScheduleID is the stable Temporal Schedule ID for the OCI
// certificate-rotation dispatcher. It is also used as the dispatcher's workflow ID base.
const OciCertificateRotationScheduleID = "oci-vsa-certificate-rotation"

// EnsureOciCertificateRotationSchedule idempotently reconciles the Temporal Schedule that
// triggers the OCI certificate-rotation dispatcher (RotateOciCertificatesWorkflow) on the
// customer task queue. It is safe to call on every startup.
//
// Behavior:
//   - ENABLE_OCI_VSA_CERTIFICATE_ROTATION=false: best-effort delete of any existing schedule
//     so it stops firing.
//   - ENABLE_OCI_VSA_CERTIFICATE_ROTATION=true: create the schedule if missing, otherwise sync
//     its cron expression so config changes take effect on redeploy.
func EnsureOciCertificateRotationSchedule(ctx context.Context, temporalClient client.Client, logger log.Logger) error {
	scheduleClient := temporalClient.ScheduleClient()
	temporalScheduler := scheduler.NewTemporalScheduler(scheduleClient)

	exists := false
	if _, err := scheduleClient.GetHandle(ctx, OciCertificateRotationScheduleID).Describe(ctx); err == nil {
		exists = true
	}

	if !env.EnableOCIVsaCertificateRotation {
		if !exists {
			logger.Info("OCI certificate rotation disabled; no schedule to remove")
			return nil
		}
		if _, err := temporalScheduler.Delete(ctx, scheduler.DeleteScheduleParams{
			ScheduleParams: scheduler.ScheduleParams{ScheduleID: OciCertificateRotationScheduleID},
		}); err != nil {
			logger.Error("Failed to remove OCI certificate rotation schedule after disable", "error", err)
			return err
		}
		logger.Info("OCI certificate rotation disabled; removed existing schedule")
		return nil
	}

	cron := env.GetOCICertificateRotationCronExpression()
	spec := client.ScheduleSpec{CronExpressions: []string{cron}}

	if exists {
		if _, err := temporalScheduler.Update(ctx, scheduler.UpdateScheduleParams{
			ScheduleParams:          scheduler.ScheduleParams{ScheduleID: OciCertificateRotationScheduleID},
			TemporalScheduleOptions: scheduler.TemporalUpdateScheduleParams{Spec: spec},
		}); err != nil {
			logger.Error("Failed to sync OCI certificate rotation schedule", "error", err)
			return err
		}
		logger.Info("OCI certificate rotation schedule in sync", "scheduleID", OciCertificateRotationScheduleID, "cron", cron)
		return nil
	}

	if _, err := temporalScheduler.Create(ctx, scheduler.CreateScheduleParams{
		ScheduleParams: scheduler.ScheduleParams{ScheduleID: OciCertificateRotationScheduleID},
		TemporalScheduleOptions: scheduler.TemporalCreateScheduleParams{
			WorkflowID: OciCertificateRotationScheduleID,
			Workflow:   common.RotateOciCertificatesWorkflowName,
			TaskQueue:  workflowengine.CustomerTaskQueue,
			Spec:       spec,
		},
	}); err != nil {
		// Tolerate a create race / Describe false-negative: another instance may have created
		// it concurrently.
		if strings.Contains(strings.ToLower(err.Error()), "already") {
			logger.Info("OCI certificate rotation schedule already exists", "scheduleID", OciCertificateRotationScheduleID)
			return nil
		}
		logger.Error("Failed to create OCI certificate rotation schedule", "error", err)
		return err
	}
	logger.Info("Created OCI certificate rotation schedule", "scheduleID", OciCertificateRotationScheduleID, "cron", cron)
	return nil
}

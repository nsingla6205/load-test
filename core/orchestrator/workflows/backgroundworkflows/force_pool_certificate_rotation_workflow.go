package backgroundworkflows

import (
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// forcedRotationGates carries the feature-flag snapshot taken inside a SideEffect so the
// decision is recorded in workflow history and stays stable across replays.
type forcedRotationGates struct {
	ForcedRotationEnabled bool `json:"forced_rotation_enabled"`
	RotationEnabled       bool `json:"rotation_enabled"`
}

// ForcePoolCertificateRotationWorkflow is the on-demand entry point for rotating the
// certificate of exactly one pool, regardless of how much lifetime the current
// certificate has left.
//
// It is meant for CD verification and operator-driven recovery: a freshly created pool
// never satisfies the remaining-lifetime check, so the scheduled fleet workflow can never
// be used to prove that rotation works end to end.
//
// Two guards keep this off the normal serving path:
//   - ENABLE_FORCED_CERTIFICATE_ROTATION must be true, so environments that have not
//     opted in cannot be forced at all.
//   - ENABLE_VSA_CERTIFICATE_ROTATION must be true, so forcing cannot bypass the
//     top-level rotation kill switch.
//
// The rotation itself is queued through the per-pool control workflow, so it serializes
// against pool update, delete, and scheduled rotation just like the fleet path.
func ForcePoolCertificateRotationWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)

	poolUUID = strings.TrimSpace(poolUUID)
	if poolUUID == "" {
		return temporal.NewNonRetryableApplicationError("pool UUID is required for forced certificate rotation", "InvalidArgument", nil)
	}

	var gates forcedRotationGates
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		return forcedRotationGates{
			ForcedRotationEnabled: env.GetBool("ENABLE_FORCED_CERTIFICATE_ROTATION", false),
			RotationEnabled:       env.GetBool("ENABLE_VSA_CERTIFICATE_ROTATION", false),
		}
	}).Get(&gates); err != nil {
		return err
	}

	if !gates.ForcedRotationEnabled {
		logger.Warn("Forced certificate rotation is disabled, skipping", "poolUUID", poolUUID)
		return temporal.NewNonRetryableApplicationError(
			"forced certificate rotation is disabled (ENABLE_FORCED_CERTIFICATE_ROTATION=false)",
			"ForcedRotationDisabled",
			nil,
		)
	}
	if !gates.RotationEnabled {
		logger.Warn("Certificate rotation is disabled, refusing forced rotation", "poolUUID", poolUUID)
		return temporal.NewNonRetryableApplicationError(
			"certificate rotation is disabled (ENABLE_VSA_CERTIFICATE_ROTATION=false)",
			"CertificateRotationDisabled",
			nil,
		)
	}

	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	logger.Info("Queueing forced certificate rotation", "poolUUID", poolUUID)

	controlWorkflowActivity := &backgroundactivities.ControlWorkflowActivity{}
	if err := workflow.ExecuteActivity(
		ctx,
		controlWorkflowActivity.ExecutePoolForcedCertificateRotationSequentially,
		poolUUID,
		CertificateRotationChildWorkflowTimeout,
	).Get(ctx, nil); err != nil {
		logger.Error("Failed to run forced certificate rotation", "poolUUID", poolUUID, "error", err)
		return fmt.Errorf("forced certificate rotation for pool %s: %w", poolUUID, err)
	}

	logger.Info("Forced certificate rotation completed", "poolUUID", poolUUID)
	return nil
}

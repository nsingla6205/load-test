package backgroundworkflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ──────────────────────────────────────────────────────────────────────────────
// OCI VSA certificate-rotation workflows.
//
// Mirrors the GCP rotation workflow shape but for the OCI version-based flow:
//   - RotateOciCertificatesWorkflow: a feature-gated dispatcher that lists OCI
//     certificate-auth pools in batches and, for each pool, dispatches an
//     independent top-level rotation workflow via DispatchOciPoolCertificateRotation.
//     Dispatch is fire-and-forget (the dispatcher waits only for each rotation to
//     start, not to finish), so per-pool concurrency is bounded by customer-worker
//     capacity rather than the batch size — matching the GCP dispatch model.
//   - RotateOciPoolCertificateWorkflow: the per-pool rotation that drives the actual
//     rotation activities and emits a Prometheus failure metric on error. It runs as
//     an independent top-level workflow on the customer task queue.
//
// Unlike GCP there is no password-rotation leg here — OCI ONTAP admin passwords
// live in OCI Vault and are not rotated by this flow.
// ──────────────────────────────────────────────────────────────────────────────

const ociCertificateRotationFailureType = "oci_certificate_rotation"

// ociUnknownPoolName labels the rotation-failure metric when the pool name has not
// been resolved yet (e.g. GetPoolContext failed). The poolUUID is always known, so
// the metric remains attributable to a specific pool.
const ociUnknownPoolName = "unknown"

// Bounded error_type label values for the rotation-failure metric. Using the
// failing phase (a small fixed set) instead of the raw error string keeps the
// Prometheus series count bounded: OCI/ONTAP errors embed OCIDs / version numbers /
// serials, so passing cause.Error() would spawn a new time series per distinct
// message. The full error is preserved in the per-phase log line at each call site.
const (
	ociRotationCategoryPoolLookup     = "pool_lookup"
	ociRotationCategoryRotationCheck  = "rotation_check"
	ociRotationCategoryStaging        = "staging"
	ociRotationCategoryInstallVerify  = "install_verify"
	ociRotationCategoryPromoteCommit  = "promote_commit"
	ociRotationCategoryClientRotation = "client_rotation"
)

// ociRotationActivityCount is the number of sequential activities in the per-pool
// rotation (RotateOciPoolCertificateWorkflow): GetPoolContext,
// OciONTAPCertificateNeedsRotation, CleanupAndStageOciONTAPCertificateVersion,
// InstallAndVerifyOciONTAPCertificateVersion, PromoteAndCommitOciONTAPCertificateVersion.
// Used only to sanity-check the per-pool WorkflowRunTimeout against the activity
// retry budget at dispatch time.
const ociRotationActivityCount = 5

// RotateOciCertificatesWorkflow is the gated dispatcher that rotates VCP-to-VSA
// certificates for OCI certificate-auth pools. It is a no-op unless
// ENABLE_OCI_VSA_CERTIFICATE_ROTATION is set. Pools are listed in batches and each
// pool's rotation is dispatched as an independent top-level workflow (fire-and-forget);
// one pool failing to dispatch does not abort the sweep.
func RotateOciCertificatesWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	if !env.EnableOCIVsaCertificateRotation {
		logger.Debug("OCI certificate rotation disabled (ENABLE_OCI_VSA_CERTIFICATE_ROTATION=false). Skipping workflow execution.")
		return nil
	}

	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}

	// Determinism note: batchSize and childWorkflowTimeout (below) are read directly
	// from process-level env.* package vars inside the workflow body. These vars are
	// initialized once at process start and never mutate, so a given worker always
	// reads the same value and replay is stable in practice — this also matches the
	// existing GCP rotation workflow. It is, however, technically non-deterministic:
	// because the values are not recorded in Temporal history, a replay on a worker
	// configured with a different value (config drift mid-run, e.g. a rolling deploy
	// that changes the env var while a run is in flight) could schedule a different
	// number/shape of activities than history recorded and trip a non-determinism
	// error. The strict fix would be to resolve these in an activity (whose result is
	// recorded) or pass them as workflow input (fixed at start). We deliberately keep
	// the direct read for parity with GCP; revisit if these become dynamically tuned.
	batchSize := env.OCICertificateRotationBatchSize
	if batchSize <= 0 {
		batchSize = 10
	}

	// childWorkflowTimeout bounds each per-pool rotation run (Temporal
	// WorkflowRunTimeout). It must stay comfortably above the rotation's chained
	// per-phase activity budget — each phase has its own StartToCloseTimeout ×
	// MaximumAttempts (see PopulateRotationRetryPolicyParams). If it drops below
	// even a single phase's full retry budget, a slow phase can exhaust the run
	// before later phases or their retries execute. That is non-fatal (the next
	// sweep idempotently reuses the PENDING version), so we warn rather than fail.
	childWorkflowTimeout := time.Duration(env.OCICertificateRotationChildWorkflowTimeoutMinutes) * time.Minute
	singlePhaseRetryBudget := time.Duration(retryPolicy.MaximumAttempts) * retryPolicy.StartToCloseTimeout
	if childWorkflowTimeout < singlePhaseRetryBudget {
		logger.Warn("OCI certificate rotation child-workflow timeout is below a single phase's activity retry budget; slow rotations may be terminated mid-run and retried on the next sweep",
			"childWorkflowTimeout", childWorkflowTimeout.String(),
			"singlePhaseRetryBudget", singlePhaseRetryBudget.String(),
			"activityStartToCloseTimeout", retryPolicy.StartToCloseTimeout.String(),
			"activityMaximumAttempts", retryPolicy.MaximumAttempts,
			"rotationActivityCount", ociRotationActivityCount)
	}

	logger.Info("Starting OCI certificate rotation dispatch for certificate-auth pools (batched listing)")
	totalPoolsProcessed := 0
	dispatchFailed := false
	offset := 0
	batchNumber := 0
	for {
		batchNumber++
		var batchResult *backgroundactivities.ListPoolsBatchResult
		listFuture := workflow.ExecuteActivity(ctx, rotateActivity.ListOciPoolsWithCertificateAuth, offset, batchSize)
		if err := listFuture.Get(ctx, &batchResult); err != nil {
			logger.Error("ListOciPoolsWithCertificateAuth failed", "error", err)
			return fmt.Errorf("oci certificate pool listing: %w", err)
		}

		pools := batchResult.Pools
		hasMore := batchResult.HasMore
		if len(pools) == 0 && batchNumber == 1 {
			logger.Info("No OCI pools with certificate authentication found. Skipping certificate rotation.")
			break
		}
		logger.Info("OCI certificate pool batch retrieved", "batchNumber", batchNumber, "count", len(pools), "hasMore", hasMore)

		if len(pools) > 0 {
			// Fire-and-forget dispatch: each per-pool rotation is started as an
			// independent top-level workflow via the dispatch activity. We wait only
			// for each dispatch (start) to complete, not for the rotation itself, so
			// execution concurrency is bounded by customer-worker capacity, not the
			// batch size.
			var batchFutures []workflow.Future
			var batchPoolUUIDs []string
			for _, pool := range pools {
				future := workflow.ExecuteActivity(ctx, rotateActivity.DispatchOciPoolCertificateRotation,
					pool.UUID, childWorkflowTimeout)
				batchFutures = append(batchFutures, future)
				batchPoolUUIDs = append(batchPoolUUIDs, pool.UUID)
			}
			for batchIndex, future := range batchFutures {
				if err := future.Get(ctx, nil); err != nil {
					dispatchFailed = true
					logger.Error("Failed to dispatch OCI certificate rotation for pool", "poolUUID", batchPoolUUIDs[batchIndex], "error", err)
				}
			}
			totalPoolsProcessed += len(pools)
		}

		if !hasMore {
			break
		}
		offset += batchSize
	}

	if dispatchFailed {
		logger.Warn("OCI certificate rotation dispatch completed with one or more dispatch failures", "totalPools", totalPoolsProcessed)
	} else if totalPoolsProcessed > 0 {
		logger.Info("OCI certificate rotation dispatched for all certificate-auth pools", "totalPools", totalPoolsProcessed)
	}

	logger.Info("OCI certificate rotation dispatcher completed - per-pool rotations run independently on the customer worker")
	return nil
}

// RotateOciPoolCertificateWorkflow is the per-pool rotation workflow. It is dispatched
// as an independent top-level workflow by the dispatcher (not awaited as a child). It
// resolves the pool context, gates on whether the active certificate version is due for
// rotation, then runs the staged rotation. On rotation failure it emits a Prometheus
// failure metric (exactly once, from the workflow rather than inside retried activities)
// and returns the error so the failure is visible in the Temporal UI.
func RotateOciPoolCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting OCI certificate rotation workflow", "poolUUID", poolUUID)

	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}

	// Step 0: resolve pool context once. On failure the pool name is not yet known,
	// so the failure metric is emitted with the poolUUID and an "unknown" pool name —
	// this keeps a pool that consistently fails the pre-checks visible on the
	// rotation-failure metric (symmetry with the staging/install/promote phases).
	var poolContext *backgroundactivities.PoolContext
	if err := workflow.ExecuteActivity(ctx, rotateActivity.GetPoolContext, poolUUID).Get(ctx, &poolContext); err != nil {
		logger.Error("GetPoolContext activity failed", "poolUUID", poolUUID, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, ociUnknownPoolName, ociRotationCategoryPoolLookup)
		return err
	}
	if poolContext == nil || poolContext.Pool == nil {
		logger.Error("PoolContext or Pool is nil after GetPoolContext", "poolUUID", poolUUID)
		emitOciCertificateRotationFailure(ctx, poolUUID, ociUnknownPoolName, ociRotationCategoryPoolLookup)
		return fmt.Errorf("pool context or pool is nil for pool UUID: %s", poolUUID)
	}

	poolName := poolContext.Pool.Name

	// Client mTLS cert rotation (cache-only, independent of the server cert cadence). Best-effort: a
	// failure here is logged and metered but does not block the server-cert rotation below.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.RotateOciVCPCertificate, poolContext).Get(ctx, nil); err != nil {
		logger.Error("OCI client certificate rotation failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolName, ociRotationCategoryClientRotation)
	}

	// Step 1: skip pools whose active certificate version is not yet due. A failure
	// here (vs. a clean "not due" result) is a genuine pre-rotation failure, so emit
	// the metric for symmetry with the later phases.
	var needsRotation bool
	if err := workflow.ExecuteActivity(ctx, rotateActivity.OciONTAPCertificateNeedsRotation, poolUUID).Get(ctx, &needsRotation); err != nil {
		logger.Error("OciONTAPCertificateNeedsRotation activity failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolName, ociRotationCategoryRotationCheck)
		return err
	}
	if !needsRotation {
		logger.Info("OCI certificate does not need rotation, skipping", "poolUUID", poolUUID, "poolName", poolName)
		return nil
	}

	logger.Info("OCI certificate needs rotation, proceeding", "poolUUID", poolUUID, "poolName", poolName)

	// Step 2: cleanup DEPRECATED versions + stage (or reuse) a PENDING version.
	// Only the version number crosses the workflow boundary; the bundle (with the
	// private key) is re-fetched by the later phases and never enters history.
	var stagedVersion int64
	if err := workflow.ExecuteActivity(ctx, rotateActivity.CleanupAndStageOciONTAPCertificateVersion, poolContext).Get(ctx, &stagedVersion); err != nil {
		logger.Error("OCI certificate staging failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolName, ociRotationCategoryStaging)
		return err
	}
	logger.Info("OCI certificate staged", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)

	// Step 3: install the staged version on ONTAP and verify it via an mTLS round-trip.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.InstallAndVerifyOciONTAPCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI certificate install/verify failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolName, ociRotationCategoryInstallVerify)
		return err
	}

	// Step 4: promote the verified version to CURRENT, commit it, and refresh the cache.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.PromoteAndCommitOciONTAPCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI certificate promote/commit failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolName, ociRotationCategoryPromoteCommit)
		return err
	}

	logger.Info("OCI certificate rotation completed successfully", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)
	return nil
}

// emitOciCertificateRotationFailure emits the rotation failure metric exactly once
// per failed phase (reusing the shared certificate-rotation counter with an
// OCI-specific failure type label).
//
// errorCategory MUST come from the bounded ociRotationCategory* set, not a raw error
// string: the error_type label feeds a Prometheus counter, and OCI/ONTAP errors
// embed OCIDs / version numbers / serials, so passing cause.Error() would spawn a
// new time series per distinct message. The full error is logged at the call site.
func emitOciCertificateRotationFailure(ctx workflow.Context, poolUUID, poolName, errorCategory string) {
	_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitCertificateRotationFailureMetric,
		poolUUID, poolName, ociCertificateRotationFailureType, errorCategory).Get(ctx, nil)
}

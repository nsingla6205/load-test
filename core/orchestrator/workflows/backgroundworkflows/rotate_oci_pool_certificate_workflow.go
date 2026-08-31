package backgroundworkflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	sdklog "go.temporal.io/sdk/log"
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
//   - RotateOciPoolCertificateWorkflow: the per-pool rotation parent. It runs as an
//     independent top-level workflow on the customer task queue and orchestrates two
//     child workflows — RotateOciPoolClientCertificateWorkflow (client mTLS cert,
//     best-effort) and RotateOciPoolServerCertificateWorkflow (server cert). Each
//     child emits a Prometheus failure metric on error.
//
// Unlike GCP there is no password-rotation leg here — OCI ONTAP admin passwords
// live in OCI Vault and are not rotated by this flow.
// ──────────────────────────────────────────────────────────────────────────────

const ociCertificateRotationFailureType = "oci_certificate_rotation"

// ociUnknownPoolExternalIdentifier labels the OCI rotation-failure metric's
// pool_external_identifier when the pool has not been resolved yet (e.g. GetPoolContext
// failed, so the pool row and its OCID are unknown). The poolUUID is always known, so
// the metric remains attributable to a specific pool.
const ociUnknownPoolExternalIdentifier = "unknown"

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
	ociRotationCategoryClientCheck    = "client_rotation_check"
	ociRotationCategoryClientStaging  = "client_staging"
	ociRotationCategoryClientPromote  = "client_promote_commit"
	ociRotationCategoryClientVerify   = "client_verify"
	ociRotationCategoryServiceCheck   = "service_rotation_check"
	ociRotationCategoryServiceStaging = "service_staging"
	ociRotationCategoryServicePromote = "service_promote_commit"
	ociRotationCategoryServiceVerify  = "service_verify"
)

// ociCertType is the bounded set of cert_type label values for the rotation-success
// metric. Using a named type (rather than a bare string) makes the compiler enforce
// the bounded set at every call site.
type ociCertType string

// Bounded cert_type label values for the rotation-success metric. Emitted only on
// an actual completed rotation (never on "not due"/skipped paths), so the success
// counter reflects real rotations. The set is small and fixed, keeping the
// Prometheus series count bounded.
const (
	ociCertTypeServer  ociCertType = "server"
	ociCertTypeClient  ociCertType = "client"
	ociCertTypeService ociCertType = "service"
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
			var batchPoolExternalIDs []string
			for _, pool := range pools {
				future := workflow.ExecuteActivity(ctx, rotateActivity.DispatchOciPoolCertificateRotation,
					pool.UUID, childWorkflowTimeout)
				batchFutures = append(batchFutures, future)
				batchPoolUUIDs = append(batchPoolUUIDs, pool.UUID)
				batchPoolExternalIDs = append(batchPoolExternalIDs, pool.PoolExternalIdentifier)
			}
			for batchIndex, future := range batchFutures {
				if err := future.Get(ctx, nil); err != nil {
					dispatchFailed = true
					logger.Error("Failed to dispatch OCI certificate rotation for pool", "poolUUID", batchPoolUUIDs[batchIndex], "poolExternalIdentifier", batchPoolExternalIDs[batchIndex], "error", err)
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

// rotationActivityOptions returns the shared ActivityOptions (timeout + retry
// policy) used by the per-pool rotation workflows.
func rotationActivityOptions() (workflow.ActivityOptions, error) {
	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return workflow.ActivityOptions{}, err
	}
	return workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}, nil
}

// RotateOciPoolCertificateWorkflow is the per-pool rotation parent. It is dispatched
// as an independent top-level workflow by the dispatcher (not awaited as a child) and
// orchestrates two child workflows:
//   - RotateOciPoolClientCertificateWorkflow — client mTLS certificate rotation.
//     Best-effort: a failure is logged but does not fail the parent or block the
//     server-cert rotation (the client cert cadence is independent).
//   - RotateOciPoolServerCertificateWorkflow — server certificate rotation. A failure
//     fails the parent so it is visible in the Temporal UI.
func RotateOciPoolCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting OCI certificate rotation workflow", "poolUUID", poolUUID)

	runID := workflow.GetInfo(ctx).WorkflowExecution.RunID

	// Client mTLS cert rotation child (best-effort, independent of the server cadence).
	if env.OCISeparateMTLSClientCert {
		clientCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("oci-cert-rotation-%s-client-%s", poolUUID, runID),
		})
		if err := workflow.ExecuteChildWorkflow(clientCtx, RotateOciPoolClientCertificateWorkflow, poolUUID).Get(clientCtx, nil); err != nil {
			logger.Error("OCI client certificate rotation workflow failed (best-effort, continuing)", "poolUUID", poolUUID, "error", err)
		}
	}

	// Read-only service client cert rotation child (best-effort, independent cadence).
	if env.OCIEnableServiceONTAPCert {
		serviceCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("oci-cert-rotation-%s-service-%s", poolUUID, runID),
		})
		if err := workflow.ExecuteChildWorkflow(serviceCtx, RotateOciPoolServiceCertificateWorkflow, poolUUID).Get(serviceCtx, nil); err != nil {
			logger.Error("OCI service certificate rotation workflow failed (best-effort, continuing)", "poolUUID", poolUUID, "error", err)
		}
	}

	// Server cert rotation child (fails the parent on error).
	serverCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: fmt.Sprintf("oci-cert-rotation-%s-server-%s", poolUUID, runID),
	})
	if err := workflow.ExecuteChildWorkflow(serverCtx, RotateOciPoolServerCertificateWorkflow, poolUUID).Get(serverCtx, nil); err != nil {
		logger.Error("OCI server certificate rotation workflow failed", "poolUUID", poolUUID, "error", err)
		return err
	}

	logger.Info("OCI certificate rotation workflow completed", "poolUUID", poolUUID)
	return nil
}

// RotateOciPoolClientCertificateWorkflow rotates the dedicated client mTLS certificate
func RotateOciPoolClientCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting OCI client certificate rotation workflow", "poolUUID", poolUUID)

	ao, err := rotationActivityOptions()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}

	poolContext, poolName, poolExternalID, err := resolveOciPoolContext(ctx, rotateActivity, poolUUID)
	if err != nil {
		return err
	}
	// Bind the pool external identifier (OCID) to the logger so every subsequent line
	// in this workflow carries it alongside poolUUID/poolName.
	logger = sdklog.With(logger, "poolExternalIdentifier", poolExternalID)

	// Step 1: is the client cert due for rotation? (also reconciles a stale persisted
	// version left by a prior promote that failed before persisting).
	var needsRotation bool
	if err := workflow.ExecuteActivity(ctx, rotateActivity.OciVCPClientCertificateNeedsRotation, poolContext).Get(ctx, &needsRotation); err != nil {
		logger.Error("OCI client certificate rotation check failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryClientCheck)
		return err
	}
	if !needsRotation {
		logger.Info("OCI client certificate does not need rotation, skipping", "poolUUID", poolUUID, "poolName", poolName)
		return nil
	}

	// Step 2: cleanup DEPRECATED versions + stage a fresh PENDING version.
	var stagedVersion int64
	if err := workflow.ExecuteActivity(ctx, rotateActivity.CleanupAndStageOciVCPClientCertificateVersion, poolContext).Get(ctx, &stagedVersion); err != nil {
		logger.Error("OCI client certificate staging failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryClientStaging)
		return err
	}
	logger.Info("OCI client certificate staged", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)

	// Step 3: verify the staged PENDING version over mTLS BEFORE committing. On
	// failure the version stays PENDING and the old CURRENT client cert keeps serving.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.VerifyOciVCPClientCertificatePending, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI client certificate verify failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryClientVerify)
		return err
	}

	// Step 4: only after a successful verify, promote to CURRENT, commit (persist), and refresh the cache.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.PromoteAndCommitOciVCPClientCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI client certificate promote/commit failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryClientPromote)
		return err
	}

	logger.Info("OCI client certificate rotation completed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)
	emitOciCertificateRotationSuccess(ctx, poolUUID, poolExternalID, ociCertTypeClient)
	return nil
}

// RotateOciPoolServiceCertificateWorkflow rotates the dedicated read-only service
// client certificate (used by an external service to call ONTAP) for a single OCI
// cert-auth pool. It mirrors RotateOciPoolClientCertificateWorkflow: gate on due →
// cleanup+stage → verify (mTLS as the read-only account) → promote+commit. It is
// best-effort and independent of the server/client cadences.
func RotateOciPoolServiceCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting OCI service certificate rotation workflow", "poolUUID", poolUUID)

	ao, err := rotationActivityOptions()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}

	poolContext, poolName, poolExternalID, err := resolveOciPoolContext(ctx, rotateActivity, poolUUID)
	if err != nil {
		return err
	}
	// Bind the pool external identifier (OCID) to the logger so every subsequent line
	// in this workflow carries it alongside poolUUID/poolName.
	logger = sdklog.With(logger, "poolExternalIdentifier", poolExternalID)

	// Step 1: is the service cert due for rotation? (also reconciles a stale
	// persisted version left by a prior promote that failed before persisting).
	var needsRotation bool
	if err := workflow.ExecuteActivity(ctx, rotateActivity.OciServiceCertificateNeedsRotation, poolContext).Get(ctx, &needsRotation); err != nil {
		logger.Error("OCI service certificate rotation check failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryServiceCheck)
		return err
	}
	if !needsRotation {
		logger.Info("OCI service certificate does not need rotation, skipping", "poolUUID", poolUUID, "poolName", poolName)
		return nil
	}

	// Step 2: cleanup DEPRECATED versions + stage a fresh PENDING version.
	var stagedVersion int64
	if err := workflow.ExecuteActivity(ctx, rotateActivity.CleanupAndStageOciServiceCertificateVersion, poolContext).Get(ctx, &stagedVersion); err != nil {
		logger.Error("OCI service certificate staging failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryServiceStaging)
		return err
	}
	logger.Info("OCI service certificate staged", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)

	// Step 3: verify the staged PENDING version over mTLS BEFORE committing. On
	// failure the version stays PENDING and the old CURRENT service cert keeps serving.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.VerifyOciServiceCertificatePending, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI service certificate verify failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryServiceVerify)
		return err
	}

	// Step 4: only after a successful verify, promote to CURRENT and commit (persist).
	if err := workflow.ExecuteActivity(ctx, rotateActivity.PromoteAndCommitOciServiceCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI service certificate promote/commit failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryServicePromote)
		return err
	}

	logger.Info("OCI service certificate rotation completed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)
	emitOciCertificateRotationSuccess(ctx, poolUUID, poolExternalID, ociCertTypeService)
	return nil
}

// RotateOciPoolServerCertificateWorkflow rotates the server certificate for a single
// OCI cert-auth pool. It gates on whether the active version is due, then runs the
// staged rotation (cleanup+stage → install+verify → promote+commit), emitting a
// bounded failure metric per failed phase and returning the error so it surfaces in
// the Temporal UI.
func RotateOciPoolServerCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting OCI server certificate rotation workflow", "poolUUID", poolUUID)

	ao, err := rotationActivityOptions()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rotateActivity := &backgroundactivities.OciCertificateRotationActivity{}

	poolContext, poolName, poolExternalID, err := resolveOciPoolContext(ctx, rotateActivity, poolUUID)
	if err != nil {
		return err
	}
	// Bind the pool external identifier (OCID) to the logger so every subsequent line
	// in this workflow carries it alongside poolUUID/poolName.
	logger = sdklog.With(logger, "poolExternalIdentifier", poolExternalID)

	// Step 1: skip pools whose active certificate version is not yet due. A failure
	// here (vs. a clean "not due" result) is a genuine pre-rotation failure, so emit
	// the metric for symmetry with the later phases.
	var needsRotation bool
	if err := workflow.ExecuteActivity(ctx, rotateActivity.OciONTAPCertificateNeedsRotation, poolUUID).Get(ctx, &needsRotation); err != nil {
		logger.Error("OciONTAPCertificateNeedsRotation activity failed", "poolUUID", poolUUID, "poolName", poolName, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryRotationCheck)
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
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryStaging)
		return err
	}
	logger.Info("OCI certificate staged", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)

	// Step 3: install the staged version on ONTAP and verify it via an mTLS round-trip.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.InstallAndVerifyOciONTAPCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI certificate install/verify failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryInstallVerify)
		return err
	}

	// Step 4: promote the verified version to CURRENT, commit it, and refresh the cache.
	if err := workflow.ExecuteActivity(ctx, rotateActivity.PromoteAndCommitOciONTAPCertificateVersion, poolContext, stagedVersion).Get(ctx, nil); err != nil {
		logger.Error("OCI certificate promote/commit failed", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, poolExternalID, ociRotationCategoryPromoteCommit)
		return err
	}

	logger.Info("OCI server certificate rotation completed successfully", "poolUUID", poolUUID, "poolName", poolName, "version", stagedVersion)
	emitOciCertificateRotationSuccess(ctx, poolUUID, poolExternalID, ociCertTypeServer)
	return nil
}

// resolveOciPoolContext resolves the pool context for a per-pool rotation workflow.
// It returns the pool context, the pool name (for logs), and the pool external
// identifier / OCID (for the metric label). On failure it emits the pool-lookup
// failure metric with the ociUnknownPoolExternalIdentifier sentinel, since the pool
// row (and its OCID) is not yet resolved, and returns the error.
func resolveOciPoolContext(ctx workflow.Context, rotateActivity *backgroundactivities.OciCertificateRotationActivity, poolUUID string) (*backgroundactivities.PoolContext, string, string, error) {
	logger := workflow.GetLogger(ctx)

	var poolContext *backgroundactivities.PoolContext
	if err := workflow.ExecuteActivity(ctx, rotateActivity.GetPoolContext, poolUUID).Get(ctx, &poolContext); err != nil {
		logger.Error("GetPoolContext activity failed", "poolUUID", poolUUID, "error", err)
		emitOciCertificateRotationFailure(ctx, poolUUID, ociUnknownPoolExternalIdentifier, ociRotationCategoryPoolLookup)
		return nil, "", "", err
	}
	if poolContext == nil || poolContext.Pool == nil {
		logger.Error("PoolContext or Pool is nil after GetPoolContext", "poolUUID", poolUUID)
		emitOciCertificateRotationFailure(ctx, poolUUID, ociUnknownPoolExternalIdentifier, ociRotationCategoryPoolLookup)
		return nil, "", "", fmt.Errorf("pool context or pool is nil for pool UUID: %s", poolUUID)
	}
	return poolContext, poolContext.Pool.Name, poolContext.Pool.PoolExternalIdentifier, nil
}

// emitOciCertificateRotationFailure emits the rotation failure metric exactly once
// per failed phase (reusing the shared certificate-rotation counter with an
// OCI-specific failure type label).
//
// errorCategory MUST come from the bounded ociRotationCategory* set, not a raw error
// string: the error_type label feeds a Prometheus counter, and OCI/ONTAP errors
// embed OCIDs / version numbers / serials, so passing cause.Error() would spawn a
// new time series per distinct message. The full error is logged at the call site.
// poolExternalIdentifier is the OCI-native pool identifier (OCID), or the
// ociUnknownPoolExternalIdentifier sentinel when the pool has not been resolved.
//
// This writes to the OCI-specific failure counter (EmitOciCertificateRotationFailureMetric),
// which is separate from the shared GCP counter so it can carry the external-identifier
// label without affecting GCP series.
//
// The activity error is intentionally ignored (best-effort metric): a failure to
// emit the metric must not mask or alter the rotation failure the caller is already
// handling and returning.
func emitOciCertificateRotationFailure(ctx workflow.Context, poolUUID, poolExternalIdentifier, errorCategory string) {
	_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitOciCertificateRotationFailureMetric,
		poolUUID, poolExternalIdentifier, ociCertificateRotationFailureType, errorCategory).Get(ctx, nil)
}

// emitOciCertificateRotationSuccess emits the rotation success metric exactly once
// when a certificate has actually completed rotation (promote+commit succeeded).
// It is NOT emitted on "not due"/skipped paths. certType MUST come from the bounded
// ociCertType* set (server | client | service) to keep the metric's cert_type label
// cardinality low. poolExternalIdentifier is the OCI-native pool identifier (OCID),
// carried so OCI operators can correlate the metric with their tooling.
//
// The activity error is intentionally ignored (best-effort metric): a failure to
// emit the metric must not fail a rotation that has already completed successfully.
func emitOciCertificateRotationSuccess(ctx workflow.Context, poolUUID, poolExternalIdentifier string, certType ociCertType) {
	_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitOciCertificateRotationSuccessMetric,
		poolUUID, poolExternalIdentifier, string(certType)).Get(ctx, nil)
}

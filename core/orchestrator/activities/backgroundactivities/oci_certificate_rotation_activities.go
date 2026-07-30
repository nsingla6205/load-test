package backgroundactivities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/oci"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

// ──────────────────────────────────────────────────────────────────────────────
// OCI VSA certificate-rotation activities.
//
// Implements the per-pool rotation flow from doc/design-doc/oci-certificate-rotation.md.
// Unlike GCP (mint a new cert resource and swap two DB IDs), OCI rotates by adding
// a new *version* to the same certificate (stable OCID + name) and using OCI's
// native PENDING → CURRENT staging. The high-level cycle (per §4):
//
//	cleanup DEPRECATED versions → reuse/create a PENDING version → install + verify
//	on ONTAP → promote PENDING to CURRENT → commit active version → refresh cache.
//
// Failure handling (§5): a staged version that reached ACTIVE is essentially never
// the fault. If install/verify on ONTAP fails we leave the version PENDING (the
// old CURRENT keeps serving), surface the error, and let the next cycle reuse the
// same PENDING version — we never re-mint.
// ──────────────────────────────────────────────────────────────────────────────

// Test-seam vars: overridable in unit tests to isolate the activity from the OCI
// SDK and the provider factory. The cert-auth cache seams (addToCertAuthCache /
// removeFromCertAuthCache) and ConvertPoolViewToPool are shared with the GCP
// rotation activities (see rotation_shared_activities.go) and reused as-is.
var (
	getOCIServiceForCertRotation   = hyperscaler2.GetOCIService
	renewOciCertificateVersion     = vsa.RenewOciCertificateVersionForVSACluster
	getPendingOciCertificateBundle = vsa.GetPendingOciCertificateResponse
)

// OciCertificateRotationActivity rotates the VCP-to-VSA (ONTAP) certificate
// for OCI cert-auth pools. It is the OCI counterpart to
// RotateVcpToVsaCertificateActivity and shares the package-level helpers
// (PoolContext, ListPoolsBatchResult, resolveVserverName, deriveCANameFromPEM).
type OciCertificateRotationActivity struct {
	SE database.Storage

	// installOciCertFunc / verifyOciCertFunc are test seams so the orchestration
	// in RotateOciPoolCertificateWithContext can be unit-tested without reaching
	// a live ONTAP. Production code leaves them nil.
	installOciCertFunc      func(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error
	verifyOciCertFunc       func(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error
	verifyOciClientCertFunc func(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error
}

// GetPoolContext retrieves pool information once and returns it in a shared
// context, mirroring RotateVcpToVsaCertificateActivity.GetPoolContext so the OCI
// child workflow can pass it straight into RotateOciPoolCertificateWithContext.
func (a *OciCertificateRotationActivity) GetPoolContext(ctx context.Context, poolUUID string) (*PoolContext, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Getting OCI pool context for UUID: %s", poolUUID)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)

	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found", poolUUID)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool %s not found", poolUUID))
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	logger.Infof("OCI pool details - UUID: %s, ID: %d, State: %s, DeploymentName: %s",
		pool.UUID, pool.ID, pool.State, pool.DeploymentName)

	return &PoolContext{
		Pool:     pool,
		PoolUUID: poolUUID,
	}, nil
}

// DispatchOciPoolCertificateRotation starts the per-pool OCI certificate rotation as an
// independent top-level workflow on the customer task queue and returns as soon as it is
// started (fire-and-forget). This decouples dispatch from execution: the parent sweep does
// not block on the rotation, so per-pool concurrency is bounded by customer-worker capacity
// rather than the dispatch batch size. It mirrors the GCP dispatch pattern
// (ExecutePoolCertificateRotationSequentially) but starts the workflow directly instead of
// through a control workflow, since OCI pool operations do not participate in the per-pool
// sequence.
//
// The WorkflowID is deterministic per pool and WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING gives
// single-flight semantics: if a rotation for the pool is already running (overlapping sweeps or
// a slow prior run) this attaches to the existing run instead of starting a duplicate.
func (a *OciCertificateRotationActivity) DispatchOciPoolCertificateRotation(ctx context.Context, poolUUID string, workflowTimeout time.Duration) error {
	logger := util.GetLogger(ctx)

	temporalClient, err := workflowengine.FetchTemporalClient()
	if err != nil {
		logger.Errorf("Failed to get Temporal client for OCI certificate rotation dispatch (pool %s): %v", poolUUID, err)
		return err
	}

	workflowID := fmt.Sprintf("oci-cert-rotation-%s", poolUUID)
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		TaskQueue:                workflowengine.CustomerTaskQueue,
		ID:                       workflowID,
		WorkflowIDReusePolicy:    enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowRunTimeout:       workflowTimeout,
	}, common.RotateOciPoolCertificateWorkflowName, poolUUID)
	if err != nil {
		logger.Errorf("Failed to dispatch OCI certificate rotation workflow for pool %s: %v", poolUUID, err)
		return err
	}

	logger.Infof("Dispatched OCI certificate rotation for pool %s (workflowID=%s, runID=%s)",
		poolUUID, workflowID, run.GetRunID())
	return nil
}

// ListOciPoolsWithCertificateAuth returns one batch of OCI pools that use
// certificate authentication (auth_type == USER_CERTIFICATE) and are in a
// rotation-eligible state (OCI_CERTIFICATE_ROTATION_POOL_STATES). Pagination
// avoids the Temporal activity result-size limit.
func (a *OciCertificateRotationActivity) ListOciPoolsWithCertificateAuth(ctx context.Context, offset, limit int) (*ListPoolsBatchResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Listing OCI certificate-auth pools (offset=%d, limit=%d)", offset, limit)

	authTypeValue := fmt.Sprintf("%d", env.USER_CERTIFICATE)
	readyStates := env.GetOCICertificateRotationPoolStates()
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("pool_credentials->>'auth_type'", "=", authTypeValue),
		dbutils.NewFilterCondition("state", "in", readyStates),
	)
	pagination := &dbutils.Pagination{Limit: limit, Offset: offset}

	poolViews, err := a.SE.ListPoolsWithFilterAndPaginationOrderedByUUID(ctx, filter, pagination)
	if err != nil {
		logger.Errorf("Failed to list OCI certificate-auth pools: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	hasMore := len(poolViews) == limit
	var pools []*datamodel.Pool
	for _, poolView := range poolViews {
		pools = append(pools, ConvertPoolViewToPool(poolView))
	}
	logger.Debugf("Retrieved batch of %d OCI certificate-auth pools (hasMore=%v)", len(pools), hasMore)
	return &ListPoolsBatchResult{Pools: pools, HasMore: hasMore}, nil
}

// OciONTAPCertificateNeedsRotation checks whether the active (CURRENT) certificate
// version of an OCI pool is due for rotation. Returns false when the pool is not
// found, not eligible, or still within its rotation threshold.
func (a *OciCertificateRotationActivity) OciONTAPCertificateNeedsRotation(ctx context.Context, poolUUID string) (bool, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("Checking if OCI certificate needs rotation for pool: %s", poolUUID)

	readyStates := env.GetOCICertificateRotationPoolStates()
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
		dbutils.NewFilterCondition("state", "in", readyStates),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to get pool %s: %v", poolUUID, err)
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found or not in an eligible state", poolUUID)
		return false, nil
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	if !ociPoolEligibleForRotation(pool) {
		logger.Warnf("Pool %s is not eligible for OCI certificate rotation", poolUUID)
		return false, nil
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		logger.Errorf("Failed to get OCI service for pool %s: %v", poolUUID, err)
		return false, err
	}

	return a.ociCertificateNeedsRotation(ctx, ociService, pool)
}

// CleanupAndStageOciONTAPCertificateVersion is phase 1 of the rotation. It sweeps any
// DEPRECATED versions left by previous rotations (best-effort), then reuses a
// healthy PENDING version or stages a fresh one, and returns that version number.
//
// Only the version number crosses the activity/workflow boundary — the staged
// bundle (which contains the private key) is discarded here and re-fetched inside
// the later phases by version, keeping key material out of Temporal history.
func (a *OciCertificateRotationActivity) CleanupAndStageOciONTAPCertificateVersion(ctx context.Context, poolContext *PoolContext) (int64, error) {
	logger := util.GetLogger(ctx)

	pool, certOCID, _, err := ociRotationContext(poolContext)
	if err != nil {
		return 0, err
	}

	// Supply the proxy X-Proxy-Request-Id for the background rotation.
	ctx, err = a.ctxWithOCIProxyRequestID(ctx, poolContext)
	if err != nil {
		logger.Errorf("Failed to resolve OCI proxy request id for pool %s: %v", pool.UUID, err)
		return 0, err
	}
	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		logger.Errorf("Failed to get OCI service for pool %s: %v", pool.UUID, err)
		return 0, err
	}

	// Sweep any DEPRECATED versions left by previous rotations. Best-effort.
	a.sweepDeprecatedVersions(ctx, ociService, certOCID)

	// Reuse a healthy PENDING version or stage a fresh one, then read its bundle
	// (used only to learn the version number — not returned to the workflow).
	certResp, err := renewOciCertificateVersion(ociService, certOCID, env.OCICertificateValidityDays, true)
	if err != nil {
		logger.Errorf("Failed to stage PENDING certificate version for pool %s (cert %s): %v",
			pool.UUID, certOCID, err)
		return 0, err
	}

	version := certResp.Certificate.VersionNumber
	logger.Infof("Staged PENDING certificate version %d for pool %s (cert %s)", version, pool.UUID, certOCID)
	return version, nil
}

// InstallAndVerifyOciONTAPCertificateVersion is phase 2 of the rotation. It re-reads
// the staged PENDING bundle by version, installs it on ONTAP, and verifies it via
// an mTLS round-trip. On failure the version is left PENDING (no re-mint) and the
// old CURRENT certificate keeps serving.
func (a *OciCertificateRotationActivity) InstallAndVerifyOciONTAPCertificateVersion(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	logger := util.GetLogger(ctx)

	pool, certOCID, _, err := ociRotationContext(poolContext)
	if err != nil {
		return err
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		logger.Errorf("Failed to get OCI service for pool %s: %v", pool.UUID, err)
		return err
	}

	certResp, err := a.fetchPendingForVersion(ctx, ociService, certOCID, versionNumber)
	if err != nil {
		return err
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get nodes for pool %s: %v", pool.UUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if err := a.installOciCertificateOnVSA(ctx, pool, dbNodes, certResp); err != nil {
		logger.Errorf("Install of staged certificate version %d failed for pool %s — leaving version PENDING, not promoting: %v",
			versionNumber, pool.UUID, err)
		return err
	}
	if err := a.verifyOciCertificateOnVSA(ctx, pool, dbNodes, certResp); err != nil {
		logger.Errorf("Verify of staged certificate version %d failed for pool %s — leaving version PENDING, not promoting: %v",
			versionNumber, pool.UUID, err)
		return err
	}

	logger.Infof("Installed and verified staged certificate version %d for pool %s", versionNumber, pool.UUID)
	return nil
}

// PromoteAndCommitOciONTAPCertificateVersion is phase 3 of the rotation. It promotes
// the staged version to CURRENT, persists the now-active version number, and
// refreshes the cert-auth cache. After a successful promote it best-effort prunes
// the now-demoted prior version's ONTAP server certificate (see
// cleanupPreviousOntapCertificateVersion) so the ONTAP server-certificate table
// does not grow unbounded across rotations.
//
// It is idempotent across activity retries: if the version is already CURRENT
// (a previous attempt promoted but failed afterward) it skips the promote and
// just re-commits and re-warms the cache.
func (a *OciCertificateRotationActivity) PromoteAndCommitOciONTAPCertificateVersion(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	logger := util.GetLogger(ctx)

	pool, certOCID, certName, err := ociRotationContext(poolContext)
	if err != nil {
		return err
	}

	// Capture the currently-active version before updateOciCertificateVersion
	// overwrites it in place. After the promote succeeds this is the version we
	// best-effort prune from ONTAP (the version being demoted from CURRENT).
	previousVersion := pool.PoolCredentials.ExternalCertificate.Version

	// Supply the proxy X-Proxy-Request-Id for the background rotation
	ctx, err = a.ctxWithOCIProxyRequestID(ctx, poolContext)
	if err != nil {
		logger.Errorf("Failed to resolve OCI proxy request id for pool %s: %v", pool.UUID, err)
		return err
	}
	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		logger.Errorf("Failed to get OCI service for pool %s: %v", pool.UUID, err)
		return err
	}

	// Fetch the PENDING bundle (pre-promote) so we can warm the cache after the
	// promote. If it's already gone, a previous attempt likely promoted it.
	certResp, err := getPendingOciCertificateBundle(ociService, certOCID)
	if err != nil {
		logger.Errorf("Failed to read PENDING bundle for pool %s (cert %s): %v", pool.UUID, certOCID, err)
		return err
	}

	switch {
	case certResp != nil && certResp.Certificate.VersionNumber == versionNumber:
		// Pre-promote guard: bail if the pool left an eligible state after verify.
		// Promote is the point of no return, so re-check right before it to avoid
		// racing a delete/upgrade that began since staging.
		eligible, err := a.poolStillEligibleForPromote(ctx, pool.UUID)
		if err != nil {
			return err
		}
		if !eligible {
			logger.Warnf("Skipping promote for pool %s — pool left eligible state; leaving version %d PENDING for the next cycle",
				pool.UUID, versionNumber)
			return nil
		}

		if err := ociService.PromoteCertificateVersion(certOCID, versionNumber); err != nil {
			logger.Errorf("Failed to promote certificate version %d to CURRENT for pool %s: %v",
				versionNumber, pool.UUID, err)
			return err
		}
		// Commit the now-active version, then warm the cache with the bundle we hold.
		if err := a.updateOciCertificateVersion(ctx, pool, versionNumber); err != nil {
			logger.Errorf("Failed to persist active certificate version %d for pool %s: %v",
				versionNumber, pool.UUID, err)
			return err
		}
		a.refreshOciCertCache(ctx, certName, certResp)

	case certResp == nil:
		// No PENDING version: treat as already-promoted on a prior attempt. Re-commit
		// (idempotent) and invalidate the cache so the next provider build lazily
		// loads the new CURRENT bundle. We avoid re-warming here because we don't
		// hold the bundle.
		logger.Warnf("No PENDING version for pool %s (cert %s); assuming version %d was already promoted, re-committing",
			pool.UUID, certOCID, versionNumber)
		if err := a.updateOciCertificateVersion(ctx, pool, versionNumber); err != nil {
			logger.Errorf("Failed to persist active certificate version %d for pool %s: %v",
				versionNumber, pool.UUID, err)
			return err
		}
		removeFromCertAuthCache(certName)

	default:
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s: staged PENDING version %d does not match expected %d",
				pool.UUID, certResp.Certificate.VersionNumber, versionNumber))
	}

	// Rotation is now committed (version is CURRENT). Best-effort prune the prior
	// version's ONTAP server certificate so the table does not grow unbounded.
	a.cleanupPreviousOntapCertificateVersion(ctx, pool, certOCID, previousVersion, versionNumber)

	logger.Infof("OCI certificate rotation complete for pool %s — active version is now %d", pool.UUID, versionNumber)
	return nil
}

// cleanupPreviousOntapCertificateVersion best-effort deletes the previous OCI
// certificate version's ONTAP server certificate (installed under the
// version-unique name <ocid>-v<previousVersion>) after a successful promote.
//
// Unlike OCI Vault versions (swept by sweepDeprecatedVersions), ONTAP has no
// auto-expiry, so without this the server-certificate table grows by one entry
// per rotation. This runs only after the new version is CURRENT and
// serving, so deleting the demoted version is safe. It never returns an error:
// any failure (provider build, ONTAP delete, not-found) is logged and ignored so
// a cleanup miss cannot fail a rotation that already succeeded — the orphaned
// entry is simply retried on the next rotation.
func (a *OciCertificateRotationActivity) cleanupPreviousOntapCertificateVersion(ctx context.Context, pool *datamodel.Pool, certOCID string, previousVersion, currentVersion int64) {
	logger := util.GetLogger(ctx)

	// Nothing to prune on the first rotation (no prior version), and never delete
	// the version we just promoted to CURRENT.
	if previousVersion <= 0 || previousVersion == currentVersion {
		return
	}

	prevName := ontapServerCertName(certOCID, previousVersion)

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Warnf("Best-effort ONTAP cert cleanup: failed to get nodes for pool %s, skipping deletion of %q: %v", pool.UUID, prevName, err)
		return
	}
	if len(dbNodes) == 0 {
		logger.Warnf("Best-effort ONTAP cert cleanup: no nodes for pool %s, skipping deletion of %q", pool.UUID, prevName)
		return
	}

	node := &models.Node{
		EndpointAddressesToHostNameMap: ociEndpointMap(dbNodes),
		DeploymentName:                 pool.DeploymentName,
		AuthType:                       env.USER_CERTIFICATE,
		Password:                       pool.PoolCredentials.Password,
		CaURI:                          pool.PoolCredentials.GetCaURIWithFallback(),
		ExternalCertificate:            pool.PoolCredentials.ExternalCertificate,
		ExternalSecret:                 pool.PoolCredentials.ExternalSecret,
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil || provider == nil {
		logger.Warnf("Best-effort ONTAP cert cleanup: failed to get provider for pool %s, skipping deletion of %q: %v", pool.UUID, prevName, err)
		return
	}

	// The OCI server certificate is installed at CLUSTER scope (owned by the admin
	// vserver), not the data SVM. ONTAP's collection DELETE treats cluster-scoped
	// certs as having no svm, so a delete filtered by svm.name matches zero records
	// and silently no-ops (returns 200 with nothing deleted). The version-unique
	// name (<ocid>-v<version>) already identifies exactly one row cluster-wide, so
	// we delete by name + type only and deliberately omit SvmName.
	delParams := vsa.DeleteServerCertificateParams{
		CertificateName: prevName,
		CertificateType: "server",
	}

	if err := provider.DeleteServerCertificate(delParams); err != nil {
		logger.Warnf("Best-effort ONTAP cert cleanup: failed to delete prior server certificate %q for pool %s: %v", prevName, pool.UUID, err)
		return
	}

	logger.Infof("Best-effort ONTAP cert cleanup: deleted prior server certificate %q for pool %s", prevName, pool.UUID)
}

// ociRotationContext validates the pool context and returns the pool plus its
// certificate OCID and name, shared by the three rotation phases.
func ociRotationContext(poolContext *PoolContext) (pool *datamodel.Pool, certOCID, certName string, err error) {
	if poolContext == nil || poolContext.Pool == nil {
		return nil, "", "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("poolContext and pool are required"))
	}
	pool = poolContext.Pool
	if !ociPoolEligibleForRotation(pool) {
		return nil, "", "", vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s is not eligible for OCI certificate rotation", pool.UUID))
	}
	return pool, pool.PoolCredentials.ExternalCertificate.ExternalIdentifier,
		pool.PoolCredentials.ExternalCertificate.Name, nil
}

// resolveOntapNodeUUIDs returns the pool's ONTAP node UUIDs
// (datamodel.Node.NodeAttributes.ExternalUUID) for all HA members in the order
// returned by GetNodesByPoolID.
func (a *OciCertificateRotationActivity) resolveOntapNodeUUIDs(ctx context.Context, pool *datamodel.Pool) ([]string, error) {
	if pool == nil {
		return nil, nil
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list ONTAP nodes for pool %s: %w", pool.UUID, err)
	}

	nodes := make([]*datamodel.Node, 0, len(dbNodes))
	for _, n := range dbNodes {
		if n != nil {
			nodes = append(nodes, n)
		}
	}

	uuids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.NodeAttributes == nil {
			continue
		}
		if id := strings.TrimSpace(n.NodeAttributes.ExternalUUID); id != "" {
			uuids = append(uuids, id)
		}
	}

	return uuids, nil
}

// ctxWithOCIProxyRequestID enriches ctx so proxy-routed certificate mutations
// carry a valid X-Proxy-Request-Id.
//
// When the proxy is enabled the header is mandatory.
// When the proxy is disabled the SDK transport ignores the header, so it is a no-op (nil error).
func (a *OciCertificateRotationActivity) ctxWithOCIProxyRequestID(ctx context.Context, poolContext *PoolContext) (context.Context, error) {
	if !oci.ProxyEnabled() {
		return ctx, nil
	}

	poolUUID := ""
	if poolContext != nil && poolContext.Pool != nil {
		poolUUID = poolContext.Pool.UUID
	}

	if poolContext != nil && len(poolContext.OntapNodeUUIDs) == 0 && poolContext.Pool != nil {
		uuids, err := a.resolveOntapNodeUUIDs(ctx, poolContext.Pool)
		if err != nil {
			return ctx, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
				fmt.Errorf("cannot route OCI certificate rotation through the proxy for pool %q: %w", poolUUID, err))
		}
		poolContext.OntapNodeUUIDs = uuids
	}

	var id string
	if poolContext != nil && len(poolContext.OntapNodeUUIDs) > 0 {
		id = strings.TrimSpace(poolContext.OntapNodeUUIDs[0])
	}
	if id == "" {
		return ctx, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError,
			fmt.Errorf("cannot route OCI certificate rotation through the proxy for pool %q: no ONTAP node UUID available for X-Proxy-Request-Id", poolUUID))
	}

	util.GetLogger(ctx).Infof("Background OCI cert rotation: using ONTAP node UUID %q as X-Proxy-Request-Id", id)
	return oci.ContextWithProxyRequestID(ctx, id), nil
}

// poolStillEligibleForPromote re-reads the pool's lifecycle state immediately
// before the promote (the point of no return) and reports whether it is still
// safe to proceed. It is a softer guard than GCP's per-pool control-workflow
// mutex, but closes most of the window where a delete/upgrade could begin after
// the staged version was verified. A transient read error does not block an
// already-verified certificate; a missing pool or a mid-lifecycle state aborts.
func (a *OciCertificateRotationActivity) poolStillEligibleForPromote(ctx context.Context, poolUUID string) (bool, error) {
	logger := util.GetLogger(ctx)

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "=", poolUUID),
	)
	poolViews, err := a.SE.ListPools(ctx, filter)
	if err != nil {
		// Don't block an already-verified cert on a transient read error.
		logger.Warnf("Pre-promote state re-check failed for pool %s, proceeding: %v", poolUUID, err)
		return true, nil
	}
	if len(poolViews) == 0 {
		logger.Warnf("Pool %s not found during pre-promote re-check, aborting promote", poolUUID)
		return false, nil
	}

	pool := ConvertPoolViewToPool(poolViews[0])
	if !ociPoolEligibleForRotation(pool) {
		logger.Warnf("Pool %s is no longer eligible for rotation (state=%s) during pre-promote re-check; aborting promote to avoid racing a lifecycle op",
			poolUUID, pool.State)
		return false, nil
	}
	return true, nil
}

// fetchPendingForVersion reads the PENDING bundle and asserts it matches the
// version the workflow staged, guarding against a concurrently re-staged version.
func (a *OciCertificateRotationActivity) fetchPendingForVersion(ctx context.Context, ociService *oci.OciServices, certOCID string, versionNumber int64) (*hyperscalermodels.CustomCertificateResponse, error) {
	logger := util.GetLogger(ctx)

	certResp, err := getPendingOciCertificateBundle(ociService, certOCID)
	if err != nil {
		logger.Errorf("Failed to read PENDING bundle for cert %s: %v", certOCID, err)
		return nil, err
	}
	if certResp == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no PENDING version found for cert %s (expected version %d)", certOCID, versionNumber))
	}
	if certResp.Certificate.VersionNumber != versionNumber {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("PENDING version mismatch for cert %s: expected %d, found %d",
				certOCID, versionNumber, certResp.Certificate.VersionNumber))
	}
	return certResp, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// ociCertificateNeedsRotation decides rotation purely from the active (CURRENT)
// certificate version's validity window. Rotation is due when the cert is
// missing, expired, has no validity window, or has elapsed past the configured
// threshold percentage of its lifetime.
func (a *OciCertificateRotationActivity) ociCertificateNeedsRotation(ctx context.Context, ociService *oci.OciServices, pool *datamodel.Pool) (bool, error) {
	return a.ociCertVersionNeedsRotation(ctx, ociService, pool.PoolCredentials.ExternalCertificate.ExternalIdentifier, pool.UUID)
}

// ociCertVersionNeedsRotation reports whether the CURRENT version of the given OCI certificate OCID is past the rotation threshold.
func (a *OciCertificateRotationActivity) ociCertVersionNeedsRotation(ctx context.Context, ociService *oci.OciServices, certOCID, poolUUID string) (bool, error) {
	logger := util.GetLogger(ctx)

	cert, err := ociService.GetCertificate(certOCID)
	if err != nil {
		logger.Errorf("Failed to fetch OCI certificate %s for pool %s: %v", certOCID, poolUUID, err)
		return false, err
	}
	if cert == nil {
		logger.Warnf("OCI certificate %s not found for pool %s, needs rotation", certOCID, poolUUID)
		return true, nil
	}
	if cert.NotBefore == nil || cert.NotAfter == nil {
		logger.Warnf("OCI certificate %s for pool %s has no validity window, needs rotation", certOCID, poolUUID)
		return true, nil
	}

	now := time.Now()
	if now.After(*cert.NotAfter) {
		logger.Warnf("OCI certificate %s for pool %s is expired (notAfter=%v), needs rotation", certOCID, poolUUID, *cert.NotAfter)
		return true, nil
	}

	lifetime := cert.NotAfter.Sub(*cert.NotBefore)
	thresholdDecimal := env.GetOCICertificateRotationThresholdPercentage()
	threshold := time.Duration(float64(lifetime) * thresholdDecimal)
	elapsed := now.Sub(*cert.NotBefore)

	needsRotation := elapsed >= threshold
	logger.Debugf("OCI certificate %s for pool %s: lifetime=%v elapsed=%v threshold(%.1f%%)=%v needsRotation=%t",
		certOCID, poolUUID, lifetime, elapsed, thresholdDecimal*100, threshold, needsRotation)
	return needsRotation, nil
}

// ociVCPClientRotationContext validates the pool context for client mTLS cert
// rotation and returns the pool plus its client certificate reference. ok is false
// when the separate client mTLS feature is disabled, the pool is not cert-auth, or
// no client certificate handle is present (all "nothing to rotate" cases).
func ociVCPClientRotationContext(poolContext *PoolContext) (pool *datamodel.Pool, ref *datamodel.ExternalCredRef, ok bool) {
	if !env.OCISeparateMTLSClientCert || poolContext == nil || poolContext.Pool == nil {
		return nil, nil, false
	}
	pool = poolContext.Pool
	if pool.PoolCredentials == nil || pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		return nil, nil, false
	}
	ref = pool.PoolCredentials.ExternalClientCertificate
	if ref == nil || ref.ExternalIdentifier == "" {
		return nil, nil, false
	}
	return pool, ref, true
}

// OciVCPClientCertificateNeedsRotation reports whether the pool's client mTLS
// certificate version is due for rotation. When it is not due it best-effort
// reconciles a stale persisted version (a prior attempt that promoted a new version
// but failed to persist it) and returns false. Returns false with no error for the
// "nothing to rotate" cases (feature disabled, not cert-auth, no client cert).
func (a *OciCertificateRotationActivity) OciVCPClientCertificateNeedsRotation(ctx context.Context, poolContext *PoolContext) (bool, error) {
	pool, ref, ok := ociVCPClientRotationContext(poolContext)
	if !ok {
		return false, nil
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return false, err
	}

	needs, err := a.ociCertVersionNeedsRotation(ctx, ociService, ref.ExternalIdentifier, pool.UUID)
	if err != nil {
		return false, err
	}
	if !needs {
		if reconcileErr := a.reconcileVCPCertificateVersion(ctx, ociService, pool, ref); reconcileErr != nil {
			return false, reconcileErr
		}
		return false, nil
	}
	return true, nil
}

// CleanupAndStageOciVCPClientCertificateVersion sweeps DEPRECATED versions
// (best-effort) and stages a fresh client certificate version, returning only the
// version number (the staged bundle is re-fetched by the promote phase and never
// crosses the workflow boundary).
func (a *OciCertificateRotationActivity) CleanupAndStageOciVCPClientCertificateVersion(ctx context.Context, poolContext *PoolContext) (int64, error) {
	logger := util.GetLogger(ctx)

	pool, ref, ok := ociVCPClientRotationContext(poolContext)
	if !ok {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI client certificate rotation"))
	}

	// Supply the proxy X-Proxy-Request-Id for the background rotation
	ctx, err := a.ctxWithOCIProxyRequestID(ctx, poolContext)
	if err != nil {
		return 0, err
	}
	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return 0, err
	}

	a.sweepDeprecatedVersions(ctx, ociService, ref.ExternalIdentifier)

	certResp, err := renewOciCertificateVersion(ociService, ref.ExternalIdentifier, env.OCICertificateValidityDays, false)
	if err != nil {
		return 0, err
	}
	version := certResp.Certificate.VersionNumber
	logger.Infof("Staged client certificate version %d for pool %s (cert %s)", version, pool.UUID, ref.ExternalIdentifier)
	return version, nil
}

// PromoteAndCommitOciVCPClientCertificateVersion promotes the staged version to
// CURRENT, persists the now-active version on pool credentials, and refreshes the
// cert-auth cache. It re-fetches the PENDING bundle by version and asserts it matches
// the staged version before promoting.
func (a *OciCertificateRotationActivity) PromoteAndCommitOciVCPClientCertificateVersion(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	logger := util.GetLogger(ctx)

	pool, ref, ok := ociVCPClientRotationContext(poolContext)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI client certificate rotation"))
	}

	// Supply the proxy X-Proxy-Request-Id for the background rotation
	ctx, err := a.ctxWithOCIProxyRequestID(ctx, poolContext)
	if err != nil {
		return err
	}
	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return err
	}

	certResp, err := getPendingOciCertificateBundle(ociService, ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if certResp == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no PENDING client cert version for cert %s (expected version %d)", ref.ExternalIdentifier, versionNumber))
	}
	if certResp.Certificate.VersionNumber != versionNumber {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s: staged client PENDING version %d does not match expected %d",
				pool.UUID, certResp.Certificate.VersionNumber, versionNumber))
	}

	if err := ociService.PromoteCertificateVersion(ref.ExternalIdentifier, versionNumber); err != nil {
		return err
	}
	if err := a.persistOciCertificateVersion(ctx, pool, ref, versionNumber); err != nil {
		return err
	}

	a.refreshOciCertCache(ctx, ref.Name, certResp)
	logger.Infof("Promoted client certificate version %d for pool %s", versionNumber, pool.UUID)
	return nil
}

// VerifyOciVCPClientCertificatePending performs the pre-commit mTLS round-trip using
// the staged PENDING client certificate version, confirming ONTAP accepts it BEFORE
// it is promoted to CURRENT and persisted. It presents the PENDING bundle directly
// (the cert-auth cache still holds the CURRENT client cert at this point) and trusts
// ONTAP's server certificate via the pool's existing server certificate chain. On
// failure the version is left PENDING and the old CURRENT client cert keeps serving.
func (a *OciCertificateRotationActivity) VerifyOciVCPClientCertificatePending(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	pool, ref, ok := ociVCPClientRotationContext(poolContext)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI client certificate rotation"))
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return err
	}

	certResp, err := getPendingOciCertificateBundle(ociService, ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if certResp == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no PENDING client cert version for cert %s (expected version %d)", ref.ExternalIdentifier, versionNumber))
	}
	if certResp.Certificate.VersionNumber != versionNumber {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s: staged client PENDING version %d does not match expected %d",
				pool.UUID, certResp.Certificate.VersionNumber, versionNumber))
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return a.verifyOciClientCertificateOnVSA(ctx, pool, dbNodes, certResp)
}

// verifyOciClientCertificateOnVSA performs an mTLS round-trip that presents the given
// (PENDING) client certificate bundle as VCP's identity, confirming ONTAP accepts it.
func (a *OciCertificateRotationActivity) verifyOciClientCertificateOnVSA(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error {
	if a.verifyOciClientCertFunc != nil {
		return a.verifyOciClientCertFunc(ctx, pool, dbNodes, certResp)
	}
	logger := util.GetLogger(ctx)

	provider, err := a.buildOciClientVerifyProvider(ctx, pool, dbNodes, certResp)
	if err != nil {
		logger.Errorf("Client certificate verification: failed to build provider for pool %s: %v", pool.UUID, err)
		return err
	}

	version, err := provider.GetONTAPVersion()
	if err != nil {
		logger.Errorf("Client certificate verification (GetONTAPVersion) failed for pool %s: %v", pool.UUID, err)
		return err
	}
	if version != nil {
		logger.Infof("Client certificate verification passed for pool %s — ONTAP version: %s", pool.UUID, *version)
	}
	return nil
}

// buildOciClientVerifyProvider builds a provider that presents the staged (PENDING)
// client certificate bundle as VCP's identity while trusting ONTAP's server
// certificate via the pool's existing server certificate chain. Mirrors
// _getProviderByNode's OCI client-cert path, but sourced from the PENDING bundle
// directly rather than the cert-auth cache (which still holds CURRENT pre-commit).
func (a *OciCertificateRotationActivity) buildOciClientVerifyProvider(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, clientCertResp *hyperscalermodels.CustomCertificateResponse) (vsa.Provider, error) {
	if len(dbNodes) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no nodes found for pool %s (ID: %d)", pool.UUID, pool.ID))
	}

	// RootCAs come from the SERVER certificate chain (unchanged by client rotation):
	// this is what validates ONTAP's server certificate at the TLS handshake.
	serverCert, err := vsa.GetCertificateFromCacheOrCAS(ctx, pool.PoolCredentials.ExternalCertificate)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceFetchError, err)
	}
	if serverCert == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("server certificate is nil for pool %s", pool.UUID))
	}

	hosts := ociEndpointMap(dbNodes)
	var ipAddress string
	for ip := range hosts {
		ipAddress = ip
		break
	}

	var serverName string
	if env.OCIUseTLSSNIOverride && pool.DeploymentName != "" && env.VsaDeployedDnsName != "" {
		serverName = fmt.Sprintf("mgmt.%s.%s", pool.DeploymentName, env.VsaDeployedDnsName)
	}

	// Identity = PENDING client bundle; RootCAs = server cert chain. No Password:
	// this is a REST/mTLS round-trip (GetONTAPVersion), not SSH.
	provider := vsa.NewProvider(ctx, vsa.ProviderDetails{
		IPAddress: ipAddress,
		Hosts:     hosts,
		Certificate: &vsa.Certificate{
			SignedCertificate:        clientCertResp.Certificate.PemCertificate,
			InterMediateCertificates: serverCert.InterMediateCertificates,
			CommonName:               clientCertResp.Certificate.SubjectCommonName,
			PrivateKey:               clientCertResp.Secret.SecretVersion.Value,
		},
		InsecureSkipVerify: false,
		AuthType:           env.USER_CERTIFICATE,
		ServerName:         serverName,
	})
	return provider, nil
}

// reconcileVCPCertificateVersion converges the persisted client-cert version with the CURRENT OCI version so a
// promote that succeeded but failed to persist (then retried) does not leave ExternalClientCertificate.Version stale.
func (a *OciCertificateRotationActivity) reconcileVCPCertificateVersion(ctx context.Context, ociService *oci.OciServices, pool *datamodel.Pool, ref *datamodel.ExternalCredRef) error {
	cert, err := ociService.GetCertificate(ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if cert == nil || cert.VersionNumber == ref.Version {
		return nil
	}
	util.GetLogger(ctx).Infof("Reconciling stale client cert version for pool %s: db=%d current=%d",
		pool.UUID, ref.Version, cert.VersionNumber)
	removeFromCertAuthCache(ref.Name)
	return a.persistOciCertificateVersion(ctx, pool, ref, cert.VersionNumber)
}

// ociServiceRotationContext validates the pool context for the dedicated read-only
// OCI service client-cert rotation and returns the pool plus its service certificate
// reference. ok is false when the service ONTAP cert feature is disabled, the pool
// is not cert-auth, or no service certificate handle is present (all "nothing to
// rotate" cases). Mirrors ociVCPClientRotationContext but sources the service cert.
func ociServiceRotationContext(poolContext *PoolContext) (pool *datamodel.Pool, ref *datamodel.ExternalCredRef, ok bool) {
	if !env.OCIEnableServiceONTAPCert || poolContext == nil || poolContext.Pool == nil {
		return nil, nil, false
	}
	pool = poolContext.Pool
	if pool.PoolCredentials == nil || pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		return nil, nil, false
	}
	ref = pool.PoolCredentials.ExternalServiceCertificate
	if ref == nil || ref.ExternalIdentifier == "" {
		return nil, nil, false
	}
	return pool, ref, true
}

// OciServiceCertificateNeedsRotation reports whether the pool's read-only service
// client certificate version is due for rotation, best-effort reconciling a stale
// persisted version when it is not. Returns false with no error for the "nothing to
// rotate" cases (feature disabled, not cert-auth, no service cert).
func (a *OciCertificateRotationActivity) OciServiceCertificateNeedsRotation(ctx context.Context, poolContext *PoolContext) (bool, error) {
	pool, ref, ok := ociServiceRotationContext(poolContext)
	if !ok {
		return false, nil
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return false, err
	}

	needs, err := a.ociCertVersionNeedsRotation(ctx, ociService, ref.ExternalIdentifier, pool.UUID)
	if err != nil {
		return false, err
	}
	if !needs {
		if reconcileErr := a.reconcileServiceCertificateVersion(ctx, ociService, pool, ref); reconcileErr != nil {
			return false, reconcileErr
		}
		return false, nil
	}
	return true, nil
}

// CleanupAndStageOciServiceCertificateVersion sweeps DEPRECATED versions
// (best-effort) and stages a fresh service certificate version, returning only the
// version number (the staged bundle is re-fetched by the promote phase).
func (a *OciCertificateRotationActivity) CleanupAndStageOciServiceCertificateVersion(ctx context.Context, poolContext *PoolContext) (int64, error) {
	logger := util.GetLogger(ctx)

	pool, ref, ok := ociServiceRotationContext(poolContext)
	if !ok {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI service certificate rotation"))
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return 0, err
	}

	a.sweepDeprecatedVersions(ctx, ociService, ref.ExternalIdentifier)

	certResp, err := renewOciCertificateVersion(ociService, ref.ExternalIdentifier, env.OCICertificateValidityDays, false)
	if err != nil {
		return 0, err
	}
	version := certResp.Certificate.VersionNumber
	logger.Infof("Staged service certificate version %d for pool %s (cert %s)", version, pool.UUID, ref.ExternalIdentifier)
	return version, nil
}

// VerifyOciServiceCertificatePending performs the pre-commit mTLS round-trip using
// the staged PENDING service certificate version, confirming ONTAP accepts it as the
// read-only account BEFORE it is promoted to CURRENT and persisted. It reuses the
// VCP client verify path (identity = PENDING bundle, RootCAs = server cert chain);
// a successful GetONTAPVersion is a valid read-only operation. On failure the version
// stays PENDING and the old CURRENT service cert keeps serving.
func (a *OciCertificateRotationActivity) VerifyOciServiceCertificatePending(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	pool, ref, ok := ociServiceRotationContext(poolContext)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI service certificate rotation"))
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return err
	}

	certResp, err := getPendingOciCertificateBundle(ociService, ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if certResp == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no PENDING service cert version for cert %s (expected version %d)", ref.ExternalIdentifier, versionNumber))
	}
	if certResp.Certificate.VersionNumber != versionNumber {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s: staged service PENDING version %d does not match expected %d",
				pool.UUID, certResp.Certificate.VersionNumber, versionNumber))
	}

	dbNodes, err := a.SE.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return a.verifyOciClientCertificateOnVSA(ctx, pool, dbNodes, certResp)
}

// PromoteAndCommitOciServiceCertificateVersion promotes the staged version to CURRENT
// and persists the now-active version on pool credentials. Unlike the VCP client cert,
// the service cert is not held in VCP's cert-auth cache (it is used by an external
// service, not VCP), so no cache refresh is performed.
func (a *OciCertificateRotationActivity) PromoteAndCommitOciServiceCertificateVersion(ctx context.Context, poolContext *PoolContext, versionNumber int64) error {
	logger := util.GetLogger(ctx)

	pool, ref, ok := ociServiceRotationContext(poolContext)
	if !ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool is not eligible for OCI service certificate rotation"))
	}

	ociService, err := getOCIServiceForCertRotation(ctx)
	if err != nil {
		return err
	}

	certResp, err := getPendingOciCertificateBundle(ociService, ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if certResp == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no PENDING service cert version for cert %s (expected version %d)", ref.ExternalIdentifier, versionNumber))
	}
	if certResp.Certificate.VersionNumber != versionNumber {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s: staged service PENDING version %d does not match expected %d",
				pool.UUID, certResp.Certificate.VersionNumber, versionNumber))
	}

	if err := ociService.PromoteCertificateVersion(ref.ExternalIdentifier, versionNumber); err != nil {
		return err
	}
	if err := a.persistOciCertificateVersion(ctx, pool, ref, versionNumber); err != nil {
		return err
	}

	logger.Infof("Promoted service certificate version %d for pool %s", versionNumber, pool.UUID)
	return nil
}

// reconcileServiceCertificateVersion converges the persisted service-cert version
// with the CURRENT OCI version so a promote that succeeded but failed to persist
// does not leave ExternalServiceCertificate.Version stale. Unlike the VCP client
// reconcile, it does not touch the cert-auth cache (the service cert is not cached
// by VCP).
func (a *OciCertificateRotationActivity) reconcileServiceCertificateVersion(ctx context.Context, ociService *oci.OciServices, pool *datamodel.Pool, ref *datamodel.ExternalCredRef) error {
	cert, err := ociService.GetCertificate(ref.ExternalIdentifier)
	if err != nil {
		return err
	}
	if cert == nil || cert.VersionNumber == ref.Version {
		return nil
	}
	util.GetLogger(ctx).Infof("Reconciling stale service cert version for pool %s: db=%d current=%d",
		pool.UUID, ref.Version, cert.VersionNumber)
	return a.persistOciCertificateVersion(ctx, pool, ref, cert.VersionNumber)
}

// installOciCertificateOnVSA installs the staged certificate version on ONTAP and
// points SSL at its serial. The install/SSL connection authenticates with the
// CURRENT certificate (via the cert-auth cache through GetProviderByNode); the
// material being installed is the new PENDING version's bundle.
func (a *OciCertificateRotationActivity) installOciCertificateOnVSA(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error {
	if a.installOciCertFunc != nil {
		return a.installOciCertFunc(ctx, pool, dbNodes, certResp)
	}
	logger := util.GetLogger(ctx)

	if len(dbNodes) == 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no nodes found for pool %s (ID: %d)", pool.UUID, pool.ID))
	}

	username := ociRotationUsername(pool)
	node := &models.Node{
		EndpointAddressesToHostNameMap: ociEndpointMap(dbNodes),
		DeploymentName:                 pool.DeploymentName,
		AuthType:                       env.USER_CERTIFICATE,
		Password:                       pool.PoolCredentials.Password,
		CaURI:                          pool.PoolCredentials.GetCaURIWithFallback(),
		ExternalCertificate:            pool.PoolCredentials.ExternalCertificate,
		ExternalSecret:                 pool.PoolCredentials.ExternalSecret,
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get VSA provider for pool %s: %v", pool.UUID, err)
		return err
	}
	if provider == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("provider is nil for pool %s", pool.UUID))
	}

	vserverName := resolveVserverName(pool)
	// Install under a version-unique ONTAP name. The OCI certificate's OCID and
	// name are stable across versions, so reusing the bare OCID would collide
	// with the previously-installed version (ONTAP 409 "duplicate entry"). The
	// name is derived deterministically from the OCID + version so a Temporal
	// retry of this same version re-targets the same ONTAP cert.
	certName := ontapServerCertName(certResp.Certificate.CertificateID, certResp.Certificate.VersionNumber)
	installParams := vsa.InstallServerCertificateParams{
		SvmName:         vserverName,
		CertificateName: certName,
		Certificate:     certResp.Certificate.PemCertificate,
		PrivateKey:      certResp.Secret.SecretVersion.Value,
		CertificateType: "server",
		CommonName:      username,
	}

	if _, err := provider.InstallServerCertificate(installParams); err != nil {
		// Retry with the deployment name when the resolved vserver name is not found.
		if isSVMNotFoundError(err) && vserverName != pool.DeploymentName {
			logger.Warnf("SVM %q not found, retrying install with deployment name %q", vserverName, pool.DeploymentName)
			vserverName = pool.DeploymentName
			installParams.SvmName = pool.DeploymentName
			if _, retryErr := provider.InstallServerCertificate(installParams); retryErr != nil {
				err = retryErr
			} else {
				err = nil
			}
		}

		// Idempotency: this activity is retried by Temporal. The ONTAP name is
		// deterministic per version (ontapServerCertName), so a retry re-installs
		// the *same* version under the *same* name and ONTAP returns "duplicate
		// entry". Treat that as an installed no-op and fall through to SSL config;
		// the subsequent ModifySSL + mTLS verify step confirm the certificate is
		// actually live before promotion.
		if err != nil {
			if isDuplicateCertificateError(err) {
				logger.Infof("Staged certificate already installed on pool %s (svm=%s); treating ONTAP duplicate entry as success", pool.UUID, vserverName)
			} else {
				logger.Errorf("Failed to install certificate on VSA for pool %s: %v", pool.UUID, err)
				return err
			}
		}
	}
	logger.Infof("Installed staged certificate on pool %s (svm=%s)", pool.UUID, vserverName)

	// Derive the serial from the certificate PEM rather than the OCI-reported serial
	// string: OCI's certificates API returns the serial as a decimal value (despite
	// documenting octet-hex), which ONTAP rejects as an invalid "-serial". The PEM is
	// the same DER ONTAP hashes when it stores the cert, so its serial is guaranteed
	// to match ONTAP's recorded value.
	serialForSSL, err := serialHexFromPEM(certResp.Certificate.PemCertificate)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("staged certificate for pool %s has an unusable serial number, cannot configure SSL: %w", pool.UUID, err))
	}
	caForSSL := deriveCANameFromPEM(certResp.Certificate.PemCertificate)
	if caForSSL == "" {
		caForSSL = certResp.Certificate.IssuerCertificateAuthority
	}
	if caForSSL == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("staged certificate for pool %s has empty CA name, cannot configure SSL", pool.UUID))
	}

	sslParams := vsa.ModifySSLParams{
		SvmName:       vserverName,
		ServerEnabled: true,
		CA:            caForSSL,
		Serial:        serialForSSL,
	}
	sslResp, sslErr := provider.ModifySSL(sslParams)
	if sslErr != nil {
		logger.Errorf("Failed to configure SSL for pool %s: %v", pool.UUID, sslErr)
		return sslErr
	}
	if sslResp != nil && !sslResp.Success {
		return vsaerrors.NewVCPError(vsaerrors.ErrOCIResourceProvisionError,
			fmt.Errorf("SSL configuration failed for pool %s: %s", pool.UUID, sslResp.Message))
	}

	logger.Infof("Configured SSL to use the staged certificate for pool %s (svm=%s)", pool.UUID, vserverName)
	return nil
}

// verifyOciCertificateOnVSA performs the post-install mTLS round-trip using the
// newly installed certificate as VCP's client identity, confirming ONTAP accepts
// it before promotion.
func (a *OciCertificateRotationActivity) verifyOciCertificateOnVSA(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) error {
	if a.verifyOciCertFunc != nil {
		return a.verifyOciCertFunc(ctx, pool, dbNodes, certResp)
	}
	logger := util.GetLogger(ctx)

	provider, err := a.buildOciVerifyProvider(ctx, pool, dbNodes, certResp)
	if err != nil {
		return err
	}

	version, err := provider.GetONTAPVersion()
	if err != nil {
		logger.Errorf("Certificate verification (GetONTAPVersion) failed for pool %s: %v", pool.UUID, err)
		return err
	}
	if version != nil {
		logger.Infof("Certificate verification passed for pool %s — ONTAP version: %s", pool.UUID, *version)
	}
	return nil
}

// buildOciVerifyProvider constructs a provider authenticated with the staged
// certificate bundle directly (not via the cert-auth cache, which still holds the
// CURRENT cert keyed by the stable cert name). This lets verification prove the
// new version works before it is promoted and cached.
func (a *OciCertificateRotationActivity) buildOciVerifyProvider(ctx context.Context, pool *datamodel.Pool, dbNodes []*datamodel.Node, certResp *hyperscalermodels.CustomCertificateResponse) (vsa.Provider, error) {
	if len(dbNodes) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound,
			fmt.Errorf("no nodes found for pool %s (ID: %d)", pool.UUID, pool.ID))
	}

	hosts := ociEndpointMap(dbNodes)
	var ipAddress string
	for ip := range hosts {
		ipAddress = ip
		break
	}

	// OCI SNI override: connect by IP yet satisfy hostname verification against
	// the wildcard SAN. Mirrors _getProviderByNode's OCI cert-auth path.
	var serverName string
	if env.OCIUseTLSSNIOverride && pool.DeploymentName != "" && env.VsaDeployedDnsName != "" {
		serverName = fmt.Sprintf("mgmt.%s.%s", pool.DeploymentName, env.VsaDeployedDnsName)
	}

	// No Password is set: verification is a REST/mTLS round-trip (GetONTAPVersion)
	// authenticated by the staged client certificate. The admin password lives in
	// OCI Vault and is only needed for SSH, which this probe never uses.
	provider := vsa.NewProvider(ctx, vsa.ProviderDetails{
		IPAddress: ipAddress,
		Hosts:     hosts,
		Certificate: &vsa.Certificate{
			SignedCertificate:        certResp.Certificate.PemCertificate,
			InterMediateCertificates: certResp.Certificate.PemCertificateChain,
			CommonName:               certResp.Certificate.SubjectCommonName,
			PrivateKey:               certResp.Secret.SecretVersion.Value,
		},
		InsecureSkipVerify: false,
		AuthType:           env.USER_CERTIFICATE,
		ServerName:         serverName,
	})
	return provider, nil
}

// sweepDeprecatedVersions schedules deletion of every DEPRECATED version of the
// certificate. Best-effort: failures are logged and never block rotation (it is a
// no-op for the first two rotations, before any version becomes DEPRECATED).
func (a *OciCertificateRotationActivity) sweepDeprecatedVersions(ctx context.Context, ociService *oci.OciServices, certOCID string) {
	logger := util.GetLogger(ctx)

	versions, err := ociService.ListCertificateVersions(certOCID)
	if err != nil {
		logger.Warnf("Best-effort cleanup: failed to list versions for certificate %s: %v", certOCID, err)
		return
	}

	for _, v := range versions {
		if !v.IsDeprecated() {
			continue
		}
		// A schedule-deleted version keeps its DEPRECATED stage for the whole
		// retention window, so IsDeprecated() alone would re-issue the deletion
		// call every sweep (each returning a wasteful 409 "already scheduled").
		// TimeOfDeletion, already populated by ListCertificateVersions, tells us
		// the version is queued — skip it locally to avoid the redundant call,
		// which is what drives the per-tenant 429s and the open circuit breaker.
		if v.IsScheduledForDeletion() {
			continue
		}
		if err := ociService.ScheduleCertificateVersionDeletion(certOCID, v.VersionNumber); err != nil {
			logger.Warnf("Best-effort cleanup: failed to schedule deletion of DEPRECATED version %d for certificate %s: %v",
				v.VersionNumber, certOCID, err)
			continue
		}
		logger.Infof("Scheduled deletion of DEPRECATED certificate version %d for certificate %s", v.VersionNumber, certOCID)
	}
}

// updateOciCertificateVersion persists the newly-promoted active version number
// on the pool's ExternalCertificate. The OCID and name never change across
// rotations, so only Version is updated; the full credentials struct is written
// back so unrelated fields (ExternalSecret, etc.) are preserved.
func (a *OciCertificateRotationActivity) updateOciCertificateVersion(ctx context.Context, pool *datamodel.Pool, newVersion int64) error {
	if pool.PoolCredentials == nil || pool.PoolCredentials.ExternalCertificate == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s has no ExternalCertificate to update", pool.UUID))
	}
	return a.persistOciCertificateVersion(ctx, pool, pool.PoolCredentials.ExternalCertificate, newVersion)
}

// persistOciCertificateVersion writes newVersion onto the given cert reference and persists pool_credentials.
func (a *OciCertificateRotationActivity) persistOciCertificateVersion(ctx context.Context, pool *datamodel.Pool, ref *datamodel.ExternalCredRef, newVersion int64) error {
	if ref == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("pool %s has no certificate reference to update", pool.UUID))
	}
	ref.Version = newVersion
	if err := a.SE.UpdatePoolFields(ctx, pool.UUID, map[string]interface{}{"pool_credentials": *pool.PoolCredentials}); err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// refreshOciCertCache invalidates and repopulates the cert-auth cache with the
// newly-promoted certificate. The cache is keyed by cert name (stable across
// versions) and is write-once, so the stale entry must be removed before the new
// bundle can land. Called only after a successful promote.
func (a *OciCertificateRotationActivity) refreshOciCertCache(ctx context.Context, certName string, certResp *hyperscalermodels.CustomCertificateResponse) {
	logger := util.GetLogger(ctx)
	if certName == "" {
		logger.Warnf("Empty certificate name, skipping cert-auth cache refresh")
		return
	}

	removeFromCertAuthCache(certName)
	addToCertAuthCache(certName, &models.Certificate{
		CommonName:               certResp.Certificate.SubjectCommonName,
		SignedCertificate:        certResp.Certificate.PemCertificate,
		PrivateKey:               certResp.Secret.SecretVersion.Value,
		InterMediateCertificates: certResp.Certificate.PemCertificateChain,
	})
	logger.Debugf("Refreshed cert-auth cache for %s", certName)
}

// ociPoolEligibleForRotation reports whether a pool may be rotated: it must not
// be mid-lifecycle (CREATING/DELETING/UPGRADING), must use certificate auth, and
// must carry an ExternalCertificate OCID handle.
func ociPoolEligibleForRotation(pool *datamodel.Pool) bool {
	if pool == nil || pool.PoolCredentials == nil {
		return false
	}
	switch pool.State {
	case "CREATING", "DELETING", "UPGRADING":
		return false
	}
	if pool.PoolCredentials.AuthType != env.USER_CERTIFICATE {
		return false
	}
	ec := pool.PoolCredentials.ExternalCertificate
	return ec != nil && ec.ExternalIdentifier != ""
}

// ontapServerCertName returns the ONTAP server-certificate name for a specific
// OCI certificate version. It is deterministic (same OCID + version always yields
// the same name, so Temporal retries of the same install are idempotent) and
// unique per version (each rotation installs under a distinct name).
//
// Why this is required on OCI but not GCP: ONTAP keys its server-certificate
// table by name and rejects a re-install under an existing name with a 409
// "duplicate entry". On GCP every rotation mints a brand-new certificate
// *resource* with a unique name ({deployment}-cert-{timestamp}), so installs
// never collide. On OCI we rotate by adding a *version* to the same certificate,
// whose OCID and name are stable across versions — so using the bare OCID as the
// ONTAP name collides on the second and later versions. Suffixing the OCI
// version number gives each version its own ONTAP cert (e.g. "<ocid>-v3").
func ontapServerCertName(certOCID string, versionNumber int64) string {
	return fmt.Sprintf("%s-v%d", certOCID, versionNumber)
}

// ociRotationUsername returns the ONTAP admin username used for the certificate
// subject CN, matching the create-time fallback (deployment name + "_admin").
func ociRotationUsername(pool *datamodel.Pool) string {
	if pool.PoolCredentials != nil && pool.PoolCredentials.Username != "" {
		return pool.PoolCredentials.Username
	}
	return fmt.Sprintf("%s_admin", pool.DeploymentName)
}

// ociEndpointMap builds the endpoint-address → host-DNS-name map used for
// certificate-auth providers, matching CreateNodeForProvider's USER_CERTIFICATE
// mapping (which uses the host DNS name as the value).
func ociEndpointMap(dbNodes []*datamodel.Node) map[string]string {
	out := make(map[string]string)
	for _, n := range dbNodes {
		if n != nil && n.EndpointAddress != "" {
			out[n.EndpointAddress] = n.HostDNSName
		}
	}
	return out
}

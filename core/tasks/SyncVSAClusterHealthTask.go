package tasks

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/inmemotasksprocessor"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/gcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// Default takeover reasons that require ephemeral disk JSWAP
// ref: https://confluence.ngage.netapp.com/pages/viewpage.action?pageId=1268443852#id-1PControlPlaneIntegrationforManuallySwitchingDataONTAPWriteModes-StopGap
var defaultRequiredTakeoverReasons = []string{
	"disabled",
	"Storage failover mailbox disks are in a degraded state",
	"Local node has encountered errors while reading the storage failover partner's mailbox disks",
	"Storage failover interconnect error",
	"Partner node halted after disabling takeover",
	"Mailbox disks are not healthy",
	"Local node missing partner disks",
	"Default",
}

// Unplanned failover detection constant
const UnplannedFailoverTakeoverReason = "Local node is already in takeover state"

// JSwapVersionThreshold is the ONTAP version threshold for JSWAP API behavior
// Versions >= this threshold will skip JSWAP API calls when feature flag is enabled
const JSwapVersionThreshold = "9.18.1"

// Feature flag to control JSWAP API behavior for ONTAP 9.18.1+
// When enabled (true), JSWAP API is skipped for ONTAP versions >= 9.18.1
// When disabled (false), JSWAP API is called for all versions (legacy behavior)
var enableJSwapVersionCheck = env.GetBool("ENABLE_JSWAP_VERSION_CHECK", true)

// getRequiredTakeoverReasons returns the list of takeover reasons from environment variable or defaults
func getRequiredTakeoverReasons() []string {
	// Try to get from environment variable first
	if envReasons := env.GetString("REQUIRED_TAKEOVER_REASONS", ""); envReasons != "" {
		// Comma-separated format only
		reasons := strings.Split(envReasons, ",")
		for i, reason := range reasons {
			reasons[i] = strings.TrimSpace(reason)
		}
		return reasons
	}

	// Return default reasons if environment variable is not set or invalid
	return defaultRequiredTakeoverReasons
}

var (
	SyncVSAClusterHealth                    = _syncVSAClusterHealth
	SyncVSAClusterHealthTask                = _syncVSAClusterHealthTask
	DetermineJSwapAction                    = determineJSwapAction
	ShouldJSwapToDiskForTakeoverStates      = shouldJSwapToDiskForTakeoverStates
	ShouldJSwapToDiskForTakeoverNotPossible = shouldJSwapToDiskForTakeoverNotPossible
	ShouldJSwapToDiskForUnplannedFailover   = shouldJSwapToDiskForUnplannedFailover
	ShouldJSwapToMemoryForTakeoverPossible  = shouldJSwapToMemoryForTakeoverPossible
	ExecuteJSwapAction                      = executeJSwapAction
	UpdatePoolToDegradedState               = updatePoolToDegradedState
	UpdatePoolToReadyStateFromHealth        = updatePoolToReadyState
	UpdatePoolToReadyState                  = updatePoolToReadyStateSimple
	UpdatePoolState                         = updatePoolState
	IsRequiredTakeoverReason                = isRequiredTakeoverReason
	HasNodeRequiredTakeoverReasonFromHealth = hasNodeRequiredTakeoverReasonFromHealth
	AnyNodeTakeoverNotPossible              = anyNodeTakeoverNotPossible
	ShouldTriggerTakeoverCheck              = shouldTriggerTakeoverCheck
	ShouldSkipVSAClusterHealthSync          = shouldSkipVSAClusterHealthSync
	GetVSAProviderUnit                      = _getVSAProviderUnit
	TriggerTakeoverCheckUnit                = _triggerTakeoverCheckUnit
	GetClusterHealthStatusUnit              = _getClusterHealthStatusUnit
	JSwapUnit                               = _jSwapUnit
)

// errSkipVSAClusterHealthSync is returned by GetVSAProviderUnit when the pool
// is PO/host-managed and health sync must no-op. The task treats this as a soft
// skip (info log), not a failure.
var errSkipVSAClusterHealthSync = errors.New("skip VSA cluster health sync for PO/host-managed pool")

// shouldSkipVSAClusterHealthSync reports whether SYNC_VSA_CLUSTER_HEALTH_STATUS
// must leave the pool alone. PO Extreme/Premium ONTAP-mode pools (and any
// host-managed pool) run on external ONTAP hosts; VSA HA/JSWAP degraded sync
// does not apply and must not flip them READY↔DEGRADED.
func shouldSkipVSAClusterHealthSync(pool *datamodel.Pool) bool {
	if pool == nil {
		return true
	}
	if strings.TrimSpace(pool.HostUUID) != "" {
		return true
	}
	return pool.APIAccessMode == common.ONTAPMode && common.IsPOPool(pool)
}

func _syncVSAClusterHealth(ctx context.Context, se database.Storage, correlationID string) {
	// Set up logger fields with correlation ID for proper context propagation
	loggerFields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}

	// Add both correlation ID and logger fields to context for proper propagation
	ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)
	logger := util.GetLogger(ctx)

	logger.Infof("[SyncVSAClusterHealth] Starting VSA Cluster Health Synchronization - CorrelationID: %s", correlationID)

	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("state", "in", []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateDegraded}),
	)
	pools, err := se.ListPoolUUIDs(ctx, filter)
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealth] CorrelationID: %s - Failed to list pools: %v", correlationID, err)
		return
	}

	logger.Infof("[SyncVSAClusterHealth] CorrelationID: %s - Found %d pools to process", correlationID, len(pools))
	beginPoolDegradedRun(time.Now())
	seedPoolDegradedGauge(pools)

	inMemoTasksProcessor, err := inmemotasksprocessor.NewInMemoTasksProcessor(len(pools), len(pools))
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealth] CorrelationID: %s - Failed to create in-memory tasks processor: %v", correlationID, err)
		return
	}

	for _, pool := range pools {
		taskId := pool.UUID
		// Set task timeout to 30 seconds - this ensures the entire task is forcefully terminated within 30 seconds
		taskCtx := inmemotasksprocessor.NewTaskCtxWithID(30*time.Second, taskId)
		// Add correlation ID and logger fields to the task context
		taskCtxWithCorrelationID := context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)
		taskCtxWithCorrelationID = context.WithValue(taskCtxWithCorrelationID, middleware.TemporalSLoggerKey, loggerFields)
		inMemoTasksProcessor.Add(inmemotasksprocessor.TaskFunc(SyncVSAClusterHealthTask), taskCtx, pool, se, taskCtxWithCorrelationID)
	}

	inMemoTasksProcessor.Run()
	prunePoolDegradedGauge(pools)
	markPoolDegradedRunComplete(time.Now())
	logger.Infof("[SyncVSAClusterHealth] CorrelationID: %s - Completed VSA Cluster Health Synchronization", correlationID)
}

func _syncVSAClusterHealthTask(imtpCtx interface{}, inputs ...interface{}) {
	ctx := imtpCtx.(*inmemotasksprocessor.IMTPContext)
	poolIdentifier := inputs[0].(*database.PoolIdentifier)
	se := inputs[1].(database.Storage)
	contextWithCorrelationID := inputs[2].(context.Context)

	// Extract correlation ID from context using utility function
	correlationID := utils.GetCoRelationIDFromContext(contextWithCorrelationID)

	// Set up logger fields with correlation ID
	loggerFields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}

	// Create a background context with correlation ID and logger fields for the unit functions
	bgCtx := context.WithValue(ctx.GetContext(), middleware.CorrelationContextKey, correlationID)
	bgCtx = context.WithValue(bgCtx, middleware.TemporalSLoggerKey, loggerFields)
	logger := util.GetLogger(bgCtx)

	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Processing pool %s", correlationID, poolIdentifier.UUID)

	// Skip JSWAP and DB state update when cluster upgrade is in progress for this pool
	hasUpgrade, err := gcp.HasActiveClusterUpgrade(bgCtx, se, poolIdentifier.UUID)
	if err != nil {
		logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to check cluster upgrade jobs, skipping health sync: %v", correlationID, poolIdentifier.UUID, err)
	}
	if hasUpgrade {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Skipping JSWAP and DB state update (cluster upgrade in progress)", correlationID, poolIdentifier.UUID)
		return
	}

	// Get VSA Provider (single GetPoolByUUID; PO/host-managed pools soft-skip here)
	providerResult := ctx.RunUnit(GetVSAProviderUnit, inmemotasksprocessor.UnitOptions{}, poolIdentifier, se, bgCtx)
	if providerResult.Err != nil {
		if errors.Is(providerResult.Err, errSkipVSAClusterHealthSync) {
			logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Skipping JSWAP and DB state update (PO/host-managed pool)", correlationID, poolIdentifier.UUID)
			return
		}
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to get VSA Provider: %v", correlationID, poolIdentifier.UUID, providerResult.Err)
		return
	}
	provider := providerResult.Result.(vsa.Provider)

	// Create a single REST client for this task to reuse across all ONTAP API calls
	ontapClient, err := provider.CreateRESTClient()
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to create ONTAP REST client: %v", correlationID, poolIdentifier.UUID, err)
		return
	}
	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Created reusable ONTAP REST client", correlationID, poolIdentifier.UUID)

	// Get cluster health status first
	clusterHealthResult := ctx.RunUnit(GetClusterHealthStatusUnit, inmemotasksprocessor.UnitOptions{}, provider, poolIdentifier.UUID, ontapClient, bgCtx)
	if clusterHealthResult.Err != nil {
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to get cluster health status: %v", correlationID, poolIdentifier.UUID, clusterHealthResult.Err)
		return
	}
	clusterHealth := clusterHealthResult.Result.(*vsa.ClusterHealthStatusResponse)

	ontapVersion, err := provider.GetONTAPVersion()
	if err != nil {
		logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to get ONTAP version: %v", correlationID, poolIdentifier.UUID, err)
		ontapVersion = nil
	}

	if shouldTriggerTakeoverCheck(clusterHealth, ontapVersion) {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - At least one node reports takeover state 'not_possible' and ONTAP < %s; triggering takeover_check to refresh reasons", correlationID, poolIdentifier.UUID, JSwapVersionThreshold)

		takeoverCheckResult := ctx.RunUnit(TriggerTakeoverCheckUnit, inmemotasksprocessor.UnitOptions{}, provider, poolIdentifier.UUID, ontapClient, bgCtx)
		if takeoverCheckResult.Err != nil {
			logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to trigger takeover check: %v", correlationID, poolIdentifier.UUID, takeoverCheckResult.Err)
			return
		}

		clusterHealthResult = ctx.RunUnit(GetClusterHealthStatusUnit, inmemotasksprocessor.UnitOptions{}, provider, poolIdentifier.UUID, ontapClient, bgCtx)
		if clusterHealthResult.Err != nil {
			logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to re-fetch cluster health status after takeover check: %v", correlationID, poolIdentifier.UUID, clusterHealthResult.Err)
			return
		}
		clusterHealth = clusterHealthResult.Result.(*vsa.ClusterHealthStatusResponse)
	} else if anyNodeTakeoverNotPossible(clusterHealth.Records) {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node reports takeover state 'not_possible' but ONTAP >= %s; skipping takeover_check PATCH", correlationID, poolIdentifier.UUID, JSwapVersionThreshold)
	} else {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - All nodes report healthy takeover state; skipping takeover_check PATCH", correlationID, poolIdentifier.UUID)
	}

	// Determine and execute JSWAP action
	jswapAction := determineJSwapAction(clusterHealth, poolIdentifier.UUID, logger, correlationID)
	executeJSwapAction(ctx, jswapAction, clusterHealth, provider, se, poolIdentifier, logger, correlationID, bgCtx, ontapClient, ontapVersion)

	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Task completed", correlationID, poolIdentifier.UUID)
}

// JSwapAction represents the type of JSWAP action to be performed
type JSwapAction int

const (
	JSwapActionNone JSwapAction = iota
	JSwapActionToDisk
	JSwapActionToMemory
)

// determineJSwapAction analyzes cluster health and determines the required JSWAP action
func determineJSwapAction(clusterHealth *vsa.ClusterHealthStatusResponse, poolUUID string, logger log.Logger, correlationID string) JSwapAction {
	// Check for unplanned failover scenario: takeover state "not_possible" with reason "Local node is already in takeover state"
	// and backing type is ephemeral_memory - this indicates unplanned failover requiring swap to disk
	if shouldJSwapToDiskForUnplannedFailover(clusterHealth.Records, poolUUID, logger, correlationID) {
		return JSwapActionToDisk
	}

	// Check for problematic takeover states
	if shouldJSwapToDiskForTakeoverStates(clusterHealth.Records, poolUUID, logger, correlationID) {
		return JSwapActionToDisk
	}

	// Check if any node has takeover_possible false
	if shouldJSwapToDiskForTakeoverNotPossible(clusterHealth.Records, poolUUID, logger, correlationID) {
		return JSwapActionToDisk
	}

	// Check if shouldJSwapToMemoryForTakeoverPossible is true for both nodes
	if shouldJSwapToMemoryForTakeoverPossible(clusterHealth.Records, poolUUID, logger, correlationID) {
		return JSwapActionToMemory
	}

	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - No JSWAP action required, cluster is healthy", correlationID, poolUUID)
	return JSwapActionNone
}

// anyNodeTakeoverNotPossible reports whether at least one node in the cluster
// has ha.takeover.state == "not_possible".
func anyNodeTakeoverNotPossible(nodes []vsa.NodeHealthStatus) bool {
	for _, node := range nodes {
		if node.Ha != nil && node.Ha.Takeover != nil && node.Ha.Takeover.State == vsa.TakeoverStateNotPossible {
			return true
		}
	}
	return false
}

// shouldTriggerTakeoverCheck reports whether SyncVSAClusterHealthTask should
// issue the takeover_check PATCH for this cycle.
//
// The PATCH is needed only when BOTH conditions hold:
//  1. At least one node reports ha.takeover.state == "not_possible" -- the only
//     state where ONTAP needs the simulation to populate ha.takeover_check.reasons.
//  2. The ONTAP version is < JSwapVersionThreshold (9.18.1). ONTAP >= 9.18.1
//     performs JSWAP dynamically, so VCP no longer needs the reasons[] list to
//     drive the manual JSWAP decision -- the GET response alone is sufficient
//     for keeping the pool's DEGRADED/READY DB state in sync.
//
// When the version is unknown (GetONTAPVersion returned an error) or the version
// feature flag is disabled, fall back to issuing the PATCH on the not_possible
// signal to preserve correctness on pre-9.18.1 clusters.
func shouldTriggerTakeoverCheck(clusterHealth *vsa.ClusterHealthStatusResponse, ontapVersion *string) bool {
	if clusterHealth == nil || !anyNodeTakeoverNotPossible(clusterHealth.Records) {
		return false
	}
	if !enableJSwapVersionCheck || ontapVersion == nil {
		return true
	}
	return IsJswapRequired(*ontapVersion, JSwapVersionThreshold)
}

// shouldJSwapToDiskForTakeoverStates checks for problematic takeover states
func shouldJSwapToDiskForTakeoverStates(nodes []vsa.NodeHealthStatus, poolUUID string, logger log.Logger, correlationID string) bool {
	for _, node := range nodes {
		if node.Ha != nil && node.Ha.Takeover != nil {
			switch node.Ha.Takeover.State {
			case vsa.TakeoverStateNotPossible:
				// Check if node has required takeover reasons
				if hasNodeRequiredTakeoverReasonFromHealth(node) {
					logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s takeover 'not_possible' with critical reason → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID)
					return true
				}
			case vsa.TakeoverStateInTakeover:
				logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s takeover 'in_takeover' → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID)
				return true
			case vsa.TakeoverStateInProgress:
				logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s takeover 'in_progress' → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID)
				return true
			case vsa.TakeoverStateFailed:
				logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s takeover 'failed' → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID)
				return true
			}
		}
	}
	return false
}

// shouldJSwapToDiskForTakeoverNotPossible checks if any node reports a non-healthy
// takeover state (anything other than "not_attempted").
func shouldJSwapToDiskForTakeoverNotPossible(nodes []vsa.NodeHealthStatus, poolUUID string, logger log.Logger, correlationID string) bool {
	for _, node := range nodes {
		if node.Ha != nil && node.Ha.Takeover != nil && node.Ha.Takeover.State != "" && node.Ha.Takeover.State != vsa.TakeoverStateNotAttempted {
			logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s takeover state %q (not 'not_attempted') → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID, node.Ha.Takeover.State)
			return true
		}
	}
	return false
}

// shouldJSwapToDiskForUnplannedFailover checks for unplanned failover scenario:
// takeover state "not_possible" with reason UnplannedFailoverTakeoverReason and backing type is ephemeral_memory
func shouldJSwapToDiskForUnplannedFailover(nodes []vsa.NodeHealthStatus, poolUUID string, logger log.Logger, correlationID string) bool {
	for _, node := range nodes {
		if node.Ha != nil && node.Ha.Takeover != nil && node.Ha.TakeoverCheck != nil {
			// Check for takeover state "not_possible" with specific reason
			if node.Ha.Takeover.State == vsa.TakeoverStateNotPossible {
				for _, reason := range node.Ha.TakeoverCheck.Reasons {
					if reason == UnplannedFailoverTakeoverReason {
						// Check if current backing type is ephemeral_memory (indicates unplanned failover)
						if node.NVLog != nil && node.NVLog.BackingType == string(vsa.JSWAPBackingTypeEphemeralMemory) {
							logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Node %s unplanned failover detected (takeover state 'not_possible', reason '%s', backing type 'ephemeral_memory') → JSWAP to ephemeral_disk", correlationID, poolUUID, node.UUID, UnplannedFailoverTakeoverReason)
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// shouldJSwapToMemoryForTakeoverPossible reports whether all nodes are in the
// healthy takeover state (ha.takeover.state == "not_attempted").
func shouldJSwapToMemoryForTakeoverPossible(nodes []vsa.NodeHealthStatus, poolUUID string, logger log.Logger, correlationID string) bool {
	if len(nodes) == 0 {
		return false
	}

	for _, node := range nodes {
		// If any node doesn't have the required fields or its takeover state is
		// anything other than "not_attempted", the cluster is not fully healthy.
		if node.Ha == nil || node.Ha.Takeover == nil || node.Ha.Takeover.State != vsa.TakeoverStateNotAttempted {
			return false
		}
	}

	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - All nodes report takeover state 'not_attempted' → JSWAP to ephemeral_memory", correlationID, poolUUID)
	return true
}

// executeJSwapAction executes the determined JSWAP action and updates pool state accordingly
func executeJSwapAction(ctx *inmemotasksprocessor.IMTPContext, action JSwapAction, clusterHealth *vsa.ClusterHealthStatusResponse,
	provider vsa.Provider, se database.Storage, poolIdentifier *database.PoolIdentifier, logger log.Logger, correlationID string, bgCtx context.Context, ontapClient ontapRest.RESTClient, ontapVersion *string) {
	switch action {
	case JSwapActionToDisk:
		updatePoolToDegradedState(ctx, clusterHealth, provider, se, poolIdentifier, logger, correlationID, bgCtx, ontapClient, ontapVersion)
	case JSwapActionToMemory:
		updatePoolToReadyState(ctx, clusterHealth, provider, se, poolIdentifier, logger, correlationID, bgCtx, ontapClient, ontapVersion)
	case JSwapActionNone:
		updatePoolToReadyStateSimple(se, poolIdentifier, logger, correlationID)
	}
}

// IsJswapRequired checks if JSWAP is required for the given ONTAP version
// Returns true if currentVersion < targetVersion (JSWAP required), false otherwise
// Only compares the first 3 parts (major.minor.patch), ignoring patch suffixes like "P2"
// Handles full ONTAP version strings like "NetApp Release 9.18.1: Mon May 24 08:07:35 UTC 2017"
func IsJswapRequired(currentVersion, targetVersion string) bool {
	// Extract base version from full ONTAP version strings (e.g., "NetApp Release 9.18.1: ..." -> "9.18.1")
	currentBase := extractBaseVersion(currentVersion)
	targetBase := extractBaseVersion(targetVersion)

	parts1 := strings.Split(currentBase, ".")
	parts2 := strings.Split(targetBase, ".")

	// Compare up to 3 parts (major, minor, patch)
	// Find the minimum of 3, len(parts1), and len(parts2)
	maxParts := 3
	if len(parts1) < maxParts {
		maxParts = len(parts1)
	}
	if len(parts2) < maxParts {
		maxParts = len(parts2)
	}

	for i := 0; i < maxParts; i++ {
		num1, err1 := strconv.Atoi(parts1[i])
		num2, err2 := strconv.Atoi(parts2[i])
		if err1 != nil || err2 != nil {
			return false // On error, default to false (not below)
		}
		if num1 < num2 {
			return true
		}
		if num1 > num2 {
			return false
		}
	}

	// If all compared parts are equal, a version with fewer parts is considered less
	// (e.g., "9.17" < "9.17.1" and "9.17.0" < "9.17.1")
	if len(parts1) < len(parts2) {
		return true
	}
	return false
}

// extractBaseVersion extracts the base version string from ONTAP version strings
// Handles formats like:
// - "9.17.1" -> "9.17.1"
// - "9.17.1P2" -> "9.17.1"
// - "NetApp Release 9.18.1: Mon May 24 08:07:35 UTC 2017" -> "9.18.1"
func extractBaseVersion(version string) string {
	// First, use the utility function to extract version from full ONTAP version strings
	extracted := utils.ExtractOntapVersion(version)
	if extracted == "" {
		// Fallback: if extraction failed, try to remove patch suffixes manually
		// This handles cases like "9.17.1P2" where ExtractOntapVersion might not work
		parts := strings.FieldsFunc(version, func(r rune) bool {
			return r == 'P' || r == 'p'
		})
		if len(parts) > 0 {
			extracted = strings.TrimSpace(parts[0])
		} else {
			extracted = version
		}
	}
	return extracted
}

// updatePoolToDegradedState updates pool to DEGRADED state based on cluster health analysis
// For ONTAP versions < 9.18.1, it calls the JSWAP API. For versions >= 9.18.1, it only updates the state.
func updatePoolToDegradedState(ctx *inmemotasksprocessor.IMTPContext, clusterHealth *vsa.ClusterHealthStatusResponse, provider vsa.Provider, se database.Storage, poolIdentifier *database.PoolIdentifier, logger log.Logger, correlationID string, bgCtx context.Context, ontapClient ontapRest.RESTClient, ontapVersion *string) {
	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Updating pool state to DEGRADED based on cluster health", correlationID, poolIdentifier.UUID)

	// Determine if JSWAP API should be called based on feature flag and ONTAP version
	shouldCallJSwapAPI := false
	if enableJSwapVersionCheck {
		// Feature flag enabled: Skip JSWAP for versions >= JSwapVersionThreshold
		if ontapVersion != nil {
			shouldCallJSwapAPI = IsJswapRequired(*ontapVersion, JSwapVersionThreshold)
		} else {
			// FALLBACK: Default to calling JSWAP API when version is unknown.
			// For ONTAP >= 9.18.1 it's a no-op, but missing it for 9.17.1 could lead to data loss.
			shouldCallJSwapAPI = true
			logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - ONTAP version not available, defaulting to call JSWAP API (safer fallback)", correlationID, poolIdentifier.UUID)
		}
	} else {
		// Feature flag disabled: Use legacy behavior (always call JSWAP)
		shouldCallJSwapAPI = true
		if ontapVersion == nil {
			logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - ONTAP version not available, will attempt JSWAP (legacy behavior)", correlationID, poolIdentifier.UUID)
		}
	}

	nodeCount := 0
	for _, node := range clusterHealth.Records {
		if node.NVLog != nil && node.NVLog.BackingType == string(vsa.JSWAPBackingTypeEphemeralMemory) {
			if shouldCallJSwapAPI {
				// Call JSWAP API for versions < 9.18.1
				jswapResult := ctx.RunUnit(JSwapUnit, inmemotasksprocessor.UnitOptions{}, provider, node.UUID, vsa.JSWAPBackingTypeEphemeralDisk, ontapClient)
				if jswapResult.Err != nil {
					logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - JSWAP to disk failed for node %s: %v", correlationID, poolIdentifier.UUID, node.UUID, jswapResult.Err)
				} else {
					logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - JSWAP to disk completed for node %s", correlationID, poolIdentifier.UUID, node.UUID)
				}
			}
			nodeCount++
		}
	}

	// Update pool state to DEGRADED
	err := updatePoolState(se, poolIdentifier, datamodel.LifeCycleStateDegraded, datamodel.LifeCycleStateDegradedDetails)
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to update pool state to DEGRADED: %v", correlationID, poolIdentifier.UUID, err)
	} else {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Pool state updated to DEGRADED (%d nodes analyzed)", correlationID, poolIdentifier.UUID, nodeCount)
	}
}

// updatePoolToReadyState updates pool to READY state based on cluster health analysis
// For ONTAP versions < JSwapVersionThreshold, it calls the JSWAP API. For versions >= JSwapVersionThreshold, it only updates the state.
func updatePoolToReadyState(ctx *inmemotasksprocessor.IMTPContext, clusterHealth *vsa.ClusterHealthStatusResponse, provider vsa.Provider, se database.Storage, poolIdentifier *database.PoolIdentifier, logger log.Logger, correlationID string, bgCtx context.Context, ontapClient ontapRest.RESTClient, ontapVersion *string) {
	logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Updating pool state to READY based on cluster health", correlationID, poolIdentifier.UUID)

	// Determine if JSWAP API should be called based on feature flag and ONTAP version
	shouldCallJSwapAPI := false
	if enableJSwapVersionCheck {
		// Feature flag enabled: Skip JSWAP for versions >= JSwapVersionThreshold
		if ontapVersion != nil {
			shouldCallJSwapAPI = IsJswapRequired(*ontapVersion, JSwapVersionThreshold)
		} else {
			// FALLBACK: Default to calling JSWAP API when version is unknown.
			// For ONTAP >= 9.18.1 it's a no-op, but missing it for 9.17.1 could lead to data loss.
			shouldCallJSwapAPI = true
			logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - ONTAP version not available, defaulting to call JSWAP API (safer fallback)", correlationID, poolIdentifier.UUID)
		}
	} else {
		// Feature flag disabled: Use legacy behavior (always call JSWAP)
		shouldCallJSwapAPI = true
		if ontapVersion == nil {
			logger.Warnf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - ONTAP version not available, will attempt JSWAP (legacy behavior)", correlationID, poolIdentifier.UUID)
		}
	}

	nodeCount := 0
	for _, node := range clusterHealth.Records {
		if node.NVLog != nil && node.NVLog.BackingType == string(vsa.JSWAPBackingTypeEphemeralDisk) {
			if shouldCallJSwapAPI {
				// Call JSWAP API for versions < 9.18.1
				jswapResult := ctx.RunUnit(JSwapUnit, inmemotasksprocessor.UnitOptions{}, provider, node.UUID, vsa.JSWAPBackingTypeEphemeralMemory, ontapClient)
				if jswapResult.Err != nil {
					logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - JSWAP to memory failed for node %s: %v", correlationID, poolIdentifier.UUID, node.UUID, jswapResult.Err)
				} else {
					logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - JSWAP to memory completed for node %s", correlationID, poolIdentifier.UUID, node.UUID)
				}
			}
			nodeCount++
		}
	}

	// Update pool state to READY
	err := updatePoolState(se, poolIdentifier, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails)
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to update pool state to READY: %v", correlationID, poolIdentifier.UUID, err)
	} else {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Pool state updated to READY (%d nodes analyzed)", correlationID, poolIdentifier.UUID, nodeCount)
	}
}

// updatePoolToReadyStateSimple updates pool to READY state when no JSWAP is needed
func updatePoolToReadyStateSimple(se database.Storage, poolIdentifier *database.PoolIdentifier, logger log.Logger, correlationID string) {
	err := updatePoolState(se, poolIdentifier, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails)
	if err != nil {
		logger.Errorf("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Failed to update pool state to READY: %v", correlationID, poolIdentifier.UUID, err)
	} else {
		logger.Infof("[SyncVSAClusterHealthTask] CorrelationID: %s - Pool %s - Pool state updated to READY", correlationID, poolIdentifier.UUID)
	}
}

// updatePoolState updates the lifecycle state of a pool using optimistic concurrency control
// Only updates pools that are in READY or DEGRADED state to prevent race conditions
func updatePoolState(se database.Storage, poolIdentifier *database.PoolIdentifier, newState string, stateDetails string) error {
	ctx := context.Background()
	logger := util.GetLogger(ctx)

	// Get current pool state for validation
	poolState, err := se.GetPoolStateByUUID(ctx, poolIdentifier.UUID)
	if err != nil {
		return fmt.Errorf("failed to get pool state for update: %v", err)
	}

	// Only update if pool is in expected states (READY or DEGRADED)
	// This prevents race conditions where pool may have transitioned to DELETING, UPDATING, etc.
	if poolState != datamodel.LifeCycleStateREADY && poolState != datamodel.LifeCycleStateDegraded {
		logger.Infof("Skipping pool state update - pool %s is in state %s (not READY/DEGRADED)", poolIdentifier.UUID, poolState)
		observePoolState(poolIdentifier, poolState)
		return nil // Not an error, just skip the update
	}

	// Check if the new state is the same as current state to avoid unnecessary database updates
	if poolState == newState {
		logger.Infof("Skipping pool state update - pool %s is already in state %s", poolIdentifier.UUID, poolState)
		observePoolState(poolIdentifier, poolState)
		return nil
	}

	// Use UpdatePoolFields with conditional update to ensure atomic state transition
	// This method includes the state validation as part of the database transaction
	updates := map[string]interface{}{
		"state":         newState,
		"state_details": stateDetails,
	}

	err = se.UpdatePoolFields(ctx, poolIdentifier.UUID, updates)
	if err != nil {
		return fmt.Errorf("failed to conditionally update pool state to %s: %v", newState, err)
	}

	logger.Infof("Successfully updated pool %s state from %s to %s with details: %s", poolIdentifier.UUID, poolState, newState, stateDetails)
	observePoolState(poolIdentifier, newState)
	return nil
}

// isRequiredTakeoverReason checks if the given reason matches any of the configured takeover reasons that require JSWAP to ephemeral_disk
func isRequiredTakeoverReason(reason string) bool {
	requiredReasons := getRequiredTakeoverReasons()

	for _, requiredReason := range requiredReasons {
		if reason == requiredReason {
			return true
		}
	}
	return false
}

// hasNodeRequiredTakeoverReasonFromHealth checks if a specific node from consolidated health status has any required takeover reasons
func hasNodeRequiredTakeoverReasonFromHealth(node vsa.NodeHealthStatus) bool {
	// If ha field is absent, it means takeover_possible: true (healthy node) - no required takeover reasons
	if node.Ha != nil && node.Ha.TakeoverCheck != nil {
		for _, reason := range node.Ha.TakeoverCheck.Reasons {
			if isRequiredTakeoverReason(reason) {
				return true
			}
		}
	}
	// If ha field is absent or no reasons present, no required takeover reasons
	return false
}

// _getVSAProviderUnit gets the pool and creates ONTAP provider
func _getVSAProviderUnit(ctx context.Context, inputs ...interface{}) (interface{}, error) {
	if len(inputs) < 3 {
		return nil, fmt.Errorf("insufficient parameters for GetVSAProviderUnit")
	}

	poolIdentifier := inputs[0].(*database.PoolIdentifier)
	se := inputs[1].(database.Storage)
	contextWithCorrelationID := inputs[2].(context.Context)

	logger := util.GetLogger(contextWithCorrelationID)

	// Extract correlation ID for logging using utility function
	correlationID := utils.GetCoRelationIDFromContext(contextWithCorrelationID)

	logger.Infof("[GetVSAProviderUnit] CorrelationID: %s - Getting VSA provider for pool: %s", correlationID, poolIdentifier.UUID)

	pool, err := se.GetPoolByUUID(contextWithCorrelationID, poolIdentifier.UUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool: %v", err)
	}
	if shouldSkipVSAClusterHealthSync(pool) {
		logger.Infof("[GetVSAProviderUnit] CorrelationID: %s - Skipping provider for PO/host-managed pool %s (service_level=%s api_access_mode=%s host_uuid=%s)",
			correlationID, poolIdentifier.UUID, pool.ServiceLevel, pool.APIAccessMode, pool.HostUUID)
		return nil, errSkipVSAClusterHealthSync
	}

	provider, err := backgroundactivities.GetOntapRestProviderForPoolFastConn(contextWithCorrelationID, se, pool)
	if err != nil || provider == nil {
		return nil, fmt.Errorf("failed to get ONTAP rest provider for pool %v: %v", pool.UUID, err)
	}

	return provider, nil
}

// _triggerTakeoverCheckUnit triggers takeover check for all nodes in the cluster
func _triggerTakeoverCheckUnit(ctx context.Context, inputs ...interface{}) (interface{}, error) {
	if len(inputs) < 4 {
		return nil, fmt.Errorf("insufficient parameters for TriggerTakeoverCheckUnit")
	}

	provider := inputs[0].(vsa.Provider)
	poolUUID := inputs[1].(string)
	ontapClient := inputs[2].(ontapRest.RESTClient)
	contextWithCorrelationID := inputs[3].(context.Context)

	logger := util.GetLogger(contextWithCorrelationID)

	// Extract correlation ID for logging using utility function
	correlationID := utils.GetCoRelationIDFromContext(contextWithCorrelationID)

	logger.Infof("[TriggerTakeoverCheckUnit] CorrelationID: %s - Triggering takeover check for all nodes in pool: %s", correlationID, poolUUID)

	nodes, err := provider.GetNodesWithClient(ontapClient)
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes for pool %s: %v", poolUUID, err)
	}

	type result struct {
		nodeUUID string
		success  bool
		err      error
	}

	goroutineCtx, cancel := context.WithCancel(contextWithCorrelationID)
	defer cancel() // Ensure all goroutines are cancelled when function returns

	results := make(chan result, len(nodes))

	// Launch goroutines with proper cancellation context
	for _, node := range nodes {
		go func(nodeUUID string) {
			// Check if context is cancelled before executing
			select {
			case <-goroutineCtx.Done():
				return // Exit goroutine if context cancelled
			default:
			}

			logger.Infof("[TriggerTakeoverCheckUnit] CorrelationID: %s - Triggering takeover check for node %s in pool %s", correlationID, nodeUUID, poolUUID)
			success, err := provider.TriggerTakeoverCheckWithClient(nodeUUID, ontapClient)

			// Send result or exit if context cancelled
			select {
			case results <- result{nodeUUID: nodeUUID, success: success, err: err}:
			case <-goroutineCtx.Done():
				return // Exit goroutine if context cancelled
			}
		}(node.ExternalUUID)
	}

	completedNodes := 0
	for completedNodes < len(nodes) {
		select {
		case res := <-results:
			completedNodes++

			if res.err != nil {
				logger.Errorf("[TriggerTakeoverCheckUnit] CorrelationID: %s - Failed to trigger takeover check for node %s: %v", correlationID, res.nodeUUID, res.err)
			} else if res.success {
				logger.Infof("[TriggerTakeoverCheckUnit] CorrelationID: %s - Successfully triggered takeover check for node %s - returning immediately", correlationID, res.nodeUUID)
				return true, nil // defer cancel() will clean up remaining goroutines
			} else {
				logger.Warnf("[TriggerTakeoverCheckUnit] CorrelationID: %s - Takeover check for node %s did not complete successfully", correlationID, res.nodeUUID)
			}
		case <-goroutineCtx.Done():
			return false, fmt.Errorf("context cancelled while waiting for takeover check results")
		}
	}

	return false, nil
}

// _getClusterHealthStatusUnit gets the consolidated cluster health status from the provider
func _getClusterHealthStatusUnit(ctx context.Context, inputs ...interface{}) (interface{}, error) {
	if len(inputs) < 4 {
		return nil, fmt.Errorf("insufficient parameters for GetClusterHealthStatusUnit")
	}

	provider := inputs[0].(vsa.Provider)
	poolUUID := inputs[1].(string)
	ontapClient := inputs[2].(ontapRest.RESTClient)
	contextWithCorrelationID := inputs[3].(context.Context)

	logger := util.GetLogger(contextWithCorrelationID)

	// Extract correlation ID for logging using utility function
	correlationID := utils.GetCoRelationIDFromContext(contextWithCorrelationID)

	logger.Infof("[GetClusterHealthStatusUnit] CorrelationID: %s - Getting consolidated cluster health status for pool: %s", correlationID, poolUUID)

	clusterHealthResponse, err := provider.GetClusterHealthStatusWithClient(ontapClient)
	if err != nil {
		return nil, fmt.Errorf("[GetClusterHealthStatusUnit] CorrelationID: %s - failed to get cluster health status for pool %s: %v", correlationID, poolUUID, err)
	}

	return clusterHealthResponse, nil
}

// _jSwapUnit performs JSWAP operation for a specific node
func _jSwapUnit(ctx context.Context, inputs ...interface{}) (interface{}, error) {
	if len(inputs) < 4 {
		return nil, fmt.Errorf("insufficient parameters for JSwapUnit")
	}

	provider := inputs[0].(vsa.Provider)
	nodeUUID := inputs[1].(string)
	backingType := inputs[2].(vsa.JSWAPBackingType)
	ontapClient := inputs[3].(ontapRest.RESTClient)

	success, err := provider.UpdateJSwapModeWithClient(nodeUUID, backingType, ontapClient)
	if err != nil {
		return nil, fmt.Errorf("JSWAP operation failed for node %s: %w", nodeUUID, err)
	}
	if !success {
		return nil, fmt.Errorf("JSWAP operation returned false for node %s", nodeUUID)
	}

	return success, nil
}

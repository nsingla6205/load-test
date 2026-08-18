package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Poller rebalance verify defaults (read once at process init).
var (
	harvestPollerLeaseNamespace = env.GetString("LEASE_NAMESPACE", "vcp")
	// Harvest DELETE /config/delete JSON body { "leaseName": "..." } removes the lease folder on the farm PVC.
	harvestDeleteLeaseConfigPath = env.GetString("HARVEST_DELETE_LEASE_CONFIG_PATH", "/config/delete")
	// Retry for transient harvest DELETE transport failures (e.g. read: connection reset by peer).
	rebalancePollerDeleteMaxAttempts    = env.GetInt("REBALANCE_POLLER_DELETE_MAX_ATTEMPTS", 3)
	rebalancePollerDeleteRetryIntervalM = env.GetInt("REBALANCE_POLLER_DELETE_RETRY_INTERVAL_MS", 500)
	pollerRebalanceBucketName           = strings.TrimSpace(env.GetString("POLLER_REBALANCE_BUCKET_NAME", ""))
)

// deleteKubernetesLeaseForEmptyHarvestPollers removes coordination.k8s.io leases for drained node groups (overridable in tests).
var deleteKubernetesLeaseForEmptyHarvestPollers = utils.DeleteKubernetesLease

// deleteRebalanceLeasePrefixInBucket removes all objects under one lease prefix in the configured rebalance bucket.
// Overridable in tests.
var (
	getRebalanceBucketClient              = hyperscaler.GetRebalanceBucketClient
	deleteRebalanceLeasePrefixInBucket    = deleteRebalanceLeasePrefixInBucketClient
	rebalanceHarvestDeleteHTTPTransport   http.RoundTripper // nil => http.Client default transport
)

// deleteHarvestLeaseConfigFolder removes the Harvest PVC lease directory for an empty lease via the farm REST API.
// Harvest DELETE /config/delete (configurable via HARVEST_DELETE_LEASE_CONFIG_PATH) expects JSON body
// { "leaseName": "<lease>" }. Harvest returns: 200 when the empty folder was removed; 404 when the folder
// was already absent (treated as success here); 400 when leaseName is missing; 409 when the folder still
// has config files; 500 on filesystem errors.
// Per-poller files use DELETE /config/{leaseName}/{prefix}{nodeID}.
// harvestRestProtocol and harvestEndPoint come from sibling activity files in package activities.
func deleteHarvestLeaseConfigFolder(ctx context.Context, leaseName string) error {
	leaseName = strings.TrimSpace(leaseName)
	if leaseName == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{"leaseName": leaseName})
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s://%s%s", harvestRestProtocol, harvestEndPoint, harvestDeleteLeaseConfigPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	var respBody []byte
	if resp.Body != nil {
		respBody, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
	respSnippet := strings.TrimSpace(string(respBody))
	if len(respSnippet) > 512 {
		respSnippet = respSnippet[:512] + "..."
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("delete harvest lease folder %s: bad request (missing leaseName?): %s", leaseName, respSnippet)
	case http.StatusConflict:
		return fmt.Errorf("delete harvest lease folder %s: folder not empty (HTTP 409); remove poller config files first: %s", leaseName, respSnippet)
	default:
		return fmt.Errorf("delete harvest lease folder %s: %s: %s", leaseName, resp.Status, respSnippet)
	}
}

// deleteRebalanceTargetHarvestPoller removes a poller YAML from Harvest for the given lease and node id
// (same REST path as unregister). Used for target rollback and for deleting the copy on the source lease after commit.
// harvestRestProtocol, harvestEndPoint, and leasePrefix come from sibling activity files in package activities.
func deleteRebalanceTargetHarvestPoller(ctx context.Context, leaseName string, nodeID int64) error {
	if leaseName == "" {
		return nil
	}
	logger := util.GetLogger(ctx)
	deleteURL := fmt.Sprintf(harvestRestProtocol+"://"+harvestEndPoint+"/config/%s/%s%d", leaseName, leasePrefix, nodeID)
	maxAttempts := rebalancePollerDeleteMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	retryInterval := time.Duration(rebalancePollerDeleteRetryIntervalM) * time.Millisecond
	if retryInterval < 0 {
		retryInterval = 0
	}

	client := &http.Client{Timeout: 30 * time.Second, Transport: rebalanceHarvestDeleteHTTPTransport}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			if !isRetryableHarvestDeleteError(err) || attempt >= maxAttempts || ctx.Err() != nil {
				return err
			}
			logger.Warnf("delete harvest poller node %d lease %s attempt %d/%d failed with retryable transport error: %v",
				nodeID, leaseName, attempt, maxAttempts, err)
			if sleepErr := sleepWithContext(ctx, retryInterval); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("delete harvest poller node %d lease %s: %s", nodeID, leaseName, resp.Status)
		}
		return nil
	}
	return nil
}

func isRetryableHarvestDeleteError(err error) bool {
	if err == nil {
		return false
	}
	// Context cancellation/deadline should stop retries immediately.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if isRetryableHarvestDeleteError(urlErr.Err) {
			return true
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if isRetryableHarvestDeleteError(opErr.Err) {
			return true
		}
	}
	var sysErr *os.SyscallError
	if errors.As(err, &sysErr) {
		if isRetryableHarvestDeleteError(sysErr.Err) {
			return true
		}
	}
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func isRebalanceBucketModeEnabled() bool {
	return strings.TrimSpace(pollerRebalanceBucketName) != ""
}

func deleteRebalanceLeasePrefixInBucketClient(ctx context.Context, bucketName, leaseName string) error {
	bucketName = strings.TrimSpace(bucketName)
	leaseName = strings.TrimSpace(leaseName)
	if bucketName == "" || leaseName == "" {
		return nil
	}
	client, err := getRebalanceBucketClient(ctx)
	if err != nil {
		return err
	}

	leasePrefix := strings.Trim(leaseName, "/") + "/"
	return client.DeleteObjectsWithPrefix(ctx, bucketName, leasePrefix)
}

func rollbackTargetHarvestUploads(ctx context.Context, staged []RebalanceStagedNode) {
	logger := util.GetLogger(ctx)
	for i := len(staged) - 1; i >= 0; i-- {
		s := staged[i]
		if s.TargetLeaseName == "" {
			continue
		}
		if err := deleteRebalanceTargetHarvestPoller(ctx, s.TargetLeaseName, s.NodeID); err != nil {
			logger.Warnf("rollback: failed to delete target harvest poller node %d lease %s: %v", s.NodeID, s.TargetLeaseName, err)
		} else {
			logger.Infof("rollback: deleted target harvest poller node %d lease %s", s.NodeID, s.TargetLeaseName)
		}
	}
}

// PollerRebalanceActivities implements snapshot and plan for harvest poller rebalancing.
type PollerRebalanceActivities struct {
	SE database.Storage
}

// HarvestNodeGroupsSnapshotResult is the DB snapshot of all node groups and poller counts.
type HarvestNodeGroupsSnapshotResult struct {
	Groups []datamodel.NodeGroupPollerCount
}

// GetNodeGroupsWithPollerCountsActivity returns aggregate poller counts per node group (ordered by count ascending).
func (a *PollerRebalanceActivities) GetNodeGroupsWithPollerCountsActivity(ctx context.Context) (*HarvestNodeGroupsSnapshotResult, error) {
	activity.RecordHeartbeat(ctx, "GetNodeGroupsWithPollerCountsActivity")
	rows, err := a.SE.ListNodeGroupsWithPollerCounts(ctx)
	if err != nil {
		return nil, err
	}
	return &HarvestNodeGroupsSnapshotResult{Groups: rows}, nil
}

// EmptyHarvestLeaseCandidate is a node group with zero pollers but a non-empty Kubernetes/Harvest lease name.
type EmptyHarvestLeaseCandidate struct {
	NodeGroupID int64
	LeaseName   string
}

// ListEmptyHarvestLeasesForCleanupActivity returns node groups that still reference a lease after all pollers moved away.
func (a *PollerRebalanceActivities) ListEmptyHarvestLeasesForCleanupActivity(ctx context.Context) ([]EmptyHarvestLeaseCandidate, error) {
	activity.RecordHeartbeat(ctx, "ListEmptyHarvestLeasesForCleanupActivity")
	logger := util.GetLogger(ctx)
	rows, err := a.SE.ListNodeGroupsWithPollerCounts(ctx)
	if err != nil {
		return nil, err
	}
	var out []EmptyHarvestLeaseCandidate
	for _, row := range rows {
		if row.Count != 0 {
			continue
		}
		lease := strings.TrimSpace(row.LeaseName)
		if lease == "" {
			continue
		}
		out = append(out, EmptyHarvestLeaseCandidate{NodeGroupID: row.NodeGroupID, LeaseName: lease})
	}
	logger.Infof("ListEmptyHarvestLeasesForCleanupActivity: %d empty lease candidate(s)", len(out))
	return out, nil
}

// CleanupEmptyLeaseParams identifies one node group lease to remove from Harvest, Kubernetes, and the DB.
type CleanupEmptyLeaseParams struct {
	NodeGroupID int64
	LeaseName   string
}

// CleanupEmptyLeaseActivity removes the Harvest lease folder via DELETE /config/delete (JSON body leaseName), deletes the
// coordination.k8s.io lease, then soft-deletes the node_groups row when the group still has zero pollers.
func (a *PollerRebalanceActivities) CleanupEmptyLeaseActivity(ctx context.Context, params *CleanupEmptyLeaseParams) error {
	activity.RecordHeartbeat(ctx, "CleanupEmptyLeaseActivity")
	logger := util.GetLogger(ctx)
	if params == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("cleanup empty lease params required"))
	}
	if params.NodeGroupID == 0 || strings.TrimSpace(params.LeaseName) == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("node group id and lease name required"))
	}

	n, err := a.SE.GetNodeGroupMapNodeCount(ctx, params.NodeGroupID)
	if err != nil {
		return err
	}
	if n > 0 {
		logger.Infof("CleanupEmptyLeaseActivity: skip node group %d lease %s (%d poller(s) assigned)", params.NodeGroupID, params.LeaseName, n)
		return nil
	}

	// Pre-check under lock before external deletes so we don't remove lease artifacts for a group that is already repopulated.
	shouldProceedCleanup := false
	err = a.SE.WithTransaction(ctx, func(tx dbutils.Transaction) error {
		g := tx.GORM().WithContext(ctx)
		var lockedGroup datamodel.NodeGroup
		if err := g.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", params.NodeGroupID).
			First(&lockedGroup).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warnf("CleanupEmptyLeaseActivity: node group %d already deleted, skip", params.NodeGroupID)
				return nil
			}
			return err
		}
		if strings.TrimSpace(lockedGroup.LeaseName) != params.LeaseName {
			logger.Infof("CleanupEmptyLeaseActivity: skip node group %d (lease drift before external cleanup: db %q params %q)", params.NodeGroupID, lockedGroup.LeaseName, params.LeaseName)
			return nil
		}
		var cnt int64
		if err := g.Model(&datamodel.NodeNodeGroupMap{}).
			Where("node_group_id = ? AND deleted_at IS NULL", params.NodeGroupID).
			Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			logger.Infof("CleanupEmptyLeaseActivity: skip node group %d; %d poller(s) assigned before external cleanup", params.NodeGroupID, cnt)
			return nil
		}
		shouldProceedCleanup = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("precheck empty lease cleanup for node group %d: %w", params.NodeGroupID, err)
	}
	if !shouldProceedCleanup {
		return nil
	}

	// Re-check right before external cleanup; a new assignment may have arrived after
	// the lock-based precheck transaction completed.
	n, err = a.SE.GetNodeGroupMapNodeCount(ctx, params.NodeGroupID)
	if err != nil {
		return fmt.Errorf("recheck node group %d count before external cleanup: %w", params.NodeGroupID, err)
	}
	if n > 0 {
		logger.Infof("CleanupEmptyLeaseActivity: skip external cleanup for node group %d lease %s (%d poller(s) assigned after precheck)",
			params.NodeGroupID, params.LeaseName, n)
		return nil
	}

	if isRebalanceBucketModeEnabled() {
		if err := deleteRebalanceLeasePrefixInBucket(ctx, pollerRebalanceBucketName, params.LeaseName); err != nil {
			return fmt.Errorf("delete rebalance bucket objects for lease %q in bucket %q: %w",
				params.LeaseName, pollerRebalanceBucketName, err)
		}
		logger.Infof("CleanupEmptyLeaseActivity: bucket mode enabled (%s), cleaned bucket objects for lease %s",
			pollerRebalanceBucketName, params.LeaseName)
	} else if err := deleteHarvestLeaseConfigFolder(ctx, params.LeaseName); err != nil {
		return fmt.Errorf("delete harvest lease folder for lease %q: %w", params.LeaseName, err)
	} else {
		logger.Infof("CleanupEmptyLeaseActivity: removed harvest config folder for lease %s (node group %d)", params.LeaseName, params.NodeGroupID)
	}

	// Re-check again before deleting the Kubernetes lease. If the group has been repopulated,
	// keep the lease object so holder election remains intact for active pollers.
	n, err = a.SE.GetNodeGroupMapNodeCount(ctx, params.NodeGroupID)
	if err != nil {
		return fmt.Errorf("recheck node group %d count before k8s lease delete: %w", params.NodeGroupID, err)
	}
	if n > 0 {
		logger.Infof("CleanupEmptyLeaseActivity: skip k8s lease delete for node group %d lease %s (%d poller(s) assigned after external cleanup)",
			params.NodeGroupID, params.LeaseName, n)
		return nil
	}

	leaseNS := strings.TrimSpace(harvestPollerLeaseNamespace)
	if leaseNS == "" {
		leaseNS = "vcp"
	}
	if err := deleteKubernetesLeaseForEmptyHarvestPollers(ctx, leaseNS, params.LeaseName); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.Warnf("CleanupEmptyLeaseActivity: k8s lease %s/%s already gone: %v", leaseNS, params.LeaseName, err)
		} else {
			return fmt.Errorf("delete k8s lease %s/%s: %w", leaseNS, params.LeaseName, err)
		}
	} else {
		logger.Infof("CleanupEmptyLeaseActivity: deleted k8s lease %s/%s for node group %d", leaseNS, params.LeaseName, params.NodeGroupID)
	}

	// Lock source node_group row again before final count+delete to avoid races with concurrent assigners.
	err = a.SE.WithTransaction(ctx, func(tx dbutils.Transaction) error {
		g := tx.GORM().WithContext(ctx)
		var lockedGroup datamodel.NodeGroup
		if err := g.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", params.NodeGroupID).
			First(&lockedGroup).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				logger.Warnf("CleanupEmptyLeaseActivity: node group %d already deleted", params.NodeGroupID)
				return nil
			}
			return err
		}
		if strings.TrimSpace(lockedGroup.LeaseName) != params.LeaseName {
			logger.Infof("CleanupEmptyLeaseActivity: skip deleting node group %d (lease drift after lock: db %q params %q)", params.NodeGroupID, lockedGroup.LeaseName, params.LeaseName)
			return nil
		}
		var cnt int64
		if err := g.Model(&datamodel.NodeNodeGroupMap{}).
			Where("node_group_id = ? AND deleted_at IS NULL", params.NodeGroupID).
			Count(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			logger.Infof("CleanupEmptyLeaseActivity: skip deleting node group %d; %d poller(s) assigned after external cleanup", params.NodeGroupID, cnt)
			return nil
		}
		if err := g.Where("id = ? AND deleted_at IS NULL", params.NodeGroupID).
			Delete(&datamodel.NodeGroup{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("soft-delete node group %d: %w", params.NodeGroupID, err)
	}
	logger.Infof("CleanupEmptyLeaseActivity: soft-deleted node group %d", params.NodeGroupID)
	return nil
}

// BuildPollerRebalancePlanParams configures tier thresholds for the greedy plan.
type BuildPollerRebalancePlanParams struct {
	NodeGroups       []datamodel.NodeGroupPollerCount
	MaxNodesPerGroup int
	EvictThreshold   int
	SoftThreshold    int
	IncludeTier2     bool
}

// HarvestRebalancePlanOutput is the planned atomic batches for one planning run.
type HarvestRebalancePlanOutput struct {
	Moves []HarvestRebalanceMove
}

// BuildPollerRebalancePlanActivity builds tiered fill-first moves from node group stats (HA-aware).
func (a *PollerRebalanceActivities) BuildPollerRebalancePlanActivity(ctx context.Context, params *BuildPollerRebalancePlanParams) (*HarvestRebalancePlanOutput, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "BuildPollerRebalancePlanActivity")
	if params == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("plan params required"))
	}
	nodeGroups := params.NodeGroups
	if len(nodeGroups) == 0 {
		return &HarvestRebalancePlanOutput{}, nil
	}

	nodesByGroup := make(map[int64][]int64)
	partnerOf := make(map[int64]int64)
	for _, nodeGroup := range nodeGroups {
		if nodeGroup.Count == 0 {
			nodesByGroup[nodeGroup.NodeGroupID] = nil
			continue
		}
		maps, err := a.SE.ListNodeNodeGroupMapsByNodeGroupID(ctx, nodeGroup.NodeGroupID)
		if err != nil {
			return nil, err
		}
		for _, m := range maps {
			nodesByGroup[nodeGroup.NodeGroupID] = append(nodesByGroup[nodeGroup.NodeGroupID], m.NodeID)
			if _, ok := partnerOf[m.NodeID]; !ok {
				pid, err := a.SE.GetHarvestHaSiblingNodeID(ctx, m.NodeID)
				if err != nil {
					return nil, err
				}
				partnerOf[m.NodeID] = pid
			}
		}
	}

	moves := buildHarvestRebalanceMoves(nodeGroups, nodesByGroup, partnerOf,
		params.MaxNodesPerGroup, params.EvictThreshold, params.SoftThreshold,
		params.IncludeTier2)
	logger.Infof("BuildPollerRebalancePlanActivity: %d atomic batches planned", len(moves))
	return &HarvestRebalancePlanOutput{Moves: moves}, nil
}

// RebalanceStagedNode records target port chosen at upload time; commit must use the same values.
// TargetLeaseName is used to DELETE uploaded YAML from the target lease on rollback.
// SourceLeaseName is the pre-move Harvest lease; after a successful DB commit we DELETE that YAML so the poller exists only on the target lease.
type RebalanceStagedNode struct {
	NodeID          int64
	SourceGroupID   int64
	TargetGroupID   int64
	SourceLeaseName string
	TargetLeaseName string
	Port            string
}

// RebalanceUploadStageResult is returned after DB commit and upload.
type RebalanceUploadStageResult struct {
	Staged []RebalanceStagedNode
}

// UploadValidateCommitRebalanceParams executes render+validate+commit in one DB transaction,
// then uploads rendered YAML after commit.
type UploadValidateCommitRebalanceParams struct {
	Moves            []HarvestRebalanceMove
	UploadURL        string
	MaxNodesPerGroup int
}

func cloneHarvestConfig(h *datamodel.HarvestConfig) (*datamodel.HarvestConfig, error) {
	if h == nil {
		return nil, errors.New("nil harvest config")
	}
	b, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	var out datamodel.HarvestConfig
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type renderedRebalanceUpload struct {
	NodeID       int64
	LeaseName    string
	RenderedYAML string
}

func parseGCSURL(uploadURL string) (bucket string, prefix string, isGCS bool, err error) {
	u, err := url.Parse(strings.TrimSpace(uploadURL))
	if err != nil {
		return "", "", false, err
	}
	if !strings.EqualFold(u.Scheme, "gs") {
		return "", "", false, nil
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", "", false, errors.New("gcs upload url missing bucket")
	}
	return strings.TrimSpace(u.Host), strings.Trim(strings.TrimSpace(u.Path), "/"), true, nil
}

func gcsRebalanceObject(prefix, leaseName string, nodeID int64) string {
	fileName := fmt.Sprintf("%s%d.yaml", leasePrefix, nodeID)
	parts := make([]string, 0, 3)
	if strings.TrimSpace(prefix) != "" {
		parts = append(parts, strings.Trim(prefix, "/"))
	}
	if strings.TrimSpace(leaseName) != "" {
		parts = append(parts, strings.Trim(leaseName, "/"))
	}
	parts = append(parts, fileName)
	return strings.Join(parts, "/")
}

func renderRebalanceYAML(hc *datamodel.HarvestConfig, pool *datamodel.Pool, credentials *vlm.OntapCredentials) (string, error) {
	if pool == nil || pool.PoolCredentials == nil {
		if pool != nil {
			return "", fmt.Errorf("pool %d has nil credentials", pool.ID)
		}
		return "", fmt.Errorf("pool is required to render rebalance yaml")
	}
	if !smHarvestAuthEnabled && credentials != nil {
		hc.PASSWORD = strconv.Quote(credentials.AdminPassword)
	} else {
		hc.PASSWORD = ""
		hc.AUTH_TYPE = pool.PoolCredentials.AuthType
		hc.SECRET_ID = pool.PoolCredentials.SecretID
		hc.SECRET_PROJECT = env.SecretManagerProjectID
	}
	return utils.RenderHarvestTemplate(hc)
}

func uploadRebalanceTarget(ctx context.Context, uploadURL, leaseName string, nodeID int64, yaml string) error {
	bucket, prefix, isGCS, err := parseGCSURL(uploadURL)
	if err != nil {
		return err
	}
	if isGCS {
		client, err := getRebalanceBucketClient(ctx)
		if err != nil {
			return err
		}
		obj := gcsRebalanceObject(prefix, leaseName, nodeID)
		return client.UploadObject(ctx, bucket, obj, []byte(yaml), "application/x-yaml")
	}
	resp, err := uploadYAMLFile(ctx, UploadYAMLFileInput{URL: uploadURL, YAML: yaml, LeaseName: leaseName, NodeID: nodeID})
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("no response from upload endpoint")
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload failed status=%s body=%s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

func deleteUploadedRebalanceTarget(ctx context.Context, uploadURL, leaseName string, nodeID int64) error {
	bucket, prefix, isGCS, err := parseGCSURL(uploadURL)
	if err != nil {
		return err
	}
	if isGCS {
		client, err := getRebalanceBucketClient(ctx)
		if err != nil {
			return err
		}
		return client.DeleteObject(ctx, bucket, gcsRebalanceObject(prefix, leaseName, nodeID))
	}
	return deleteRebalanceTargetHarvestPoller(ctx, leaseName, nodeID)
}

// prefetchRebalanceCredentials resolves ONTAP credentials per pool before any rebalance
// transaction opens. Credentials do not depend on port allocation; fetching them while
// target node_groups rows are held FOR UPDATE would extend lock time on Secret Manager misses.
func prefetchRebalanceCredentials(ctx context.Context, se database.Storage, moves []HarvestRebalanceMove) (map[int64]*vlm.OntapCredentials, error) {
	if smHarvestAuthEnabled || len(moves) == 0 {
		return nil, nil
	}
	nodeIDs := make(map[int64]struct{})
	for _, move := range moves {
		for _, nodeID := range move.NodeIDs {
			nodeIDs[nodeID] = struct{}{}
		}
	}
	credentialsByPool := make(map[int64]*vlm.OntapCredentials, len(nodeIDs))
	poolIDsSeen := make(map[int64]struct{}, len(nodeIDs))
	for nodeID := range nodeIDs {
		node, err := se.GetNodeByID(ctx, nodeID)
		if err != nil {
			return nil, fmt.Errorf("get node %d for credential prefetch: %w", nodeID, err)
		}
		if _, seen := poolIDsSeen[node.PoolID]; seen {
			continue
		}
		poolIDsSeen[node.PoolID] = struct{}{}
		pool, err := se.GetPoolByID(ctx, node.PoolID)
		if err != nil {
			return nil, fmt.Errorf("get pool %d for credential prefetch: %w", node.PoolID, err)
		}
		credentials, err := fetchOnTapCredentials(ctx, pool)
		if err != nil {
			return nil, err
		}
		if credentials == nil {
			return nil, fmt.Errorf("failed to get credentials for pool %d", pool.ID)
		}
		credentialsByPool[pool.ID] = credentials
	}
	return credentialsByPool, nil
}

func rollbackUploadedTargets(ctx context.Context, uploadURL string, staged []RebalanceStagedNode) {
	logger := util.GetLogger(ctx)
	for i := len(staged) - 1; i >= 0; i-- {
		s := staged[i]
		if s.TargetLeaseName == "" {
			continue
		}
		if err := deleteUploadedRebalanceTarget(ctx, uploadURL, s.TargetLeaseName, s.NodeID); err != nil {
			logger.Warnf("rollback: failed to delete target upload node %d lease %s: %v", s.NodeID, s.TargetLeaseName, err)
		}
	}
}

// UploadValidateCommitRebalanceActivity runs render, structural validation, and DB commit in one transaction,
// then uploads YAML after commit (outside lock).
func (a *PollerRebalanceActivities) UploadValidateCommitRebalanceActivity(ctx context.Context, params *UploadValidateCommitRebalanceParams) (res *RebalanceUploadStageResult, err error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "UploadValidateCommitRebalanceActivity started")
	if params == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("upload+validate+commit params required"))
	}
	if params.UploadURL == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("upload URL required"))
	}
	if params.MaxNodesPerGroup <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("max nodes per group must be > 0"))
	}
	if len(params.Moves) == 0 {
		return &RebalanceUploadStageResult{}, nil
	}

	var staged []RebalanceStagedNode
	var renderedUploads []renderedRebalanceUpload

	credentialsByPool, err := prefetchRebalanceCredentials(ctx, a.SE, params.Moves)
	if err != nil {
		return nil, err
	}

	nodeIDs := make([]int64, 0)
	nodeIDsSeen := make(map[int64]struct{})
	for _, move := range params.Moves {
		for _, nodeID := range move.NodeIDs {
			if _, seen := nodeIDsSeen[nodeID]; seen {
				continue
			}
			nodeIDsSeen[nodeID] = struct{}{}
			nodeIDs = append(nodeIDs, nodeID)
		}
	}

	err = a.SE.WithTransaction(ctx, func(tx dbutils.Transaction) error {
		g := tx.GORM().WithContext(ctx)
		lockTargetIDsSet := make(map[int64]struct{})
		for _, move := range params.Moves {
			lockTargetIDsSet[move.TargetGroupID] = struct{}{}
		}
		lockTargetGroupIDs := make([]int64, 0, len(lockTargetIDsSet))
		for id := range lockTargetIDsSet {
			lockTargetGroupIDs = append(lockTargetGroupIDs, id)
		}
		sort.Slice(lockTargetGroupIDs, func(i, j int) bool { return lockTargetGroupIDs[i] < lockTargetGroupIDs[j] })

		var lockedTargetGroups []datamodel.NodeGroup
		if err := g.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ? AND deleted_at IS NULL", lockTargetGroupIDs).
			Order("id ASC").
			Find(&lockedTargetGroups).Error; err != nil {
			return err
		}
		targetGroupByID := make(map[int64]*datamodel.NodeGroup, len(lockedTargetGroups))
		for i := range lockedTargetGroups {
			group := &lockedTargetGroups[i]
			targetGroupByID[group.ID] = group
		}

		for _, move := range params.Moves {
			targetGroup, ok := targetGroupByID[move.TargetGroupID]
			if !ok || strings.TrimSpace(targetGroup.LeaseName) == "" {
				return temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("target node group %d missing or has empty lease name", move.TargetGroupID),
					"HarvestRebalanceInvalidTargetGroup", nil)
			}
		}

		mappingsByNodeID, err := a.SE.GetActiveNodeNodeGroupMapsByNodeIDs(ctx, nodeIDs, tx)
		if err != nil {
			return err
		}

		seen := make(map[int64]struct{})
		reservedPortsByTarget := make(map[int64]map[int]struct{})
		for mi, move := range params.Moves {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("upload+validate+commit move %d/%d source=%d target=%d", mi+1, len(params.Moves), move.SourceGroupID, move.TargetGroupID))
			targetGroup := targetGroupByID[move.TargetGroupID]

			for _, nodeID := range move.NodeIDs {
				if _, dup := seen[nodeID]; dup {
					continue
				}
				seen[nodeID] = struct{}{}
				activity.RecordHeartbeat(ctx, fmt.Sprintf("render rebalance poller node %d", nodeID))

				mapping := mappingsByNodeID[nodeID]
				if mapping.NodeGroupID != move.SourceGroupID {
					msg := fmt.Sprintf("rebalance upload: node %d on group %d, move expects source %d",
						nodeID, mapping.NodeGroupID, move.SourceGroupID)
					logger.Error(msg)
					return temporal.NewNonRetryableApplicationError(msg, "HarvestRebalancePrepareSourceMismatch", nil)
				}
				if mapping.HarvestConfig == nil {
					return temporal.NewNonRetryableApplicationError(
						fmt.Sprintf("node %d has no harvest_config", nodeID), "HarvestRebalanceMissingHarvestConfig", nil)
				}
				sourceLeaseName := strings.TrimSpace(move.SourceLeaseName)
				if sourceLeaseName == "" {
					sourceLeaseName = strings.TrimSpace(mapping.HarvestConfig.LEASE_NAME)
				}

				portNum, err := database.GetFirstAvailablePortWithReserved(g, move.TargetGroupID, reservedPortsByTarget[move.TargetGroupID])
				if err != nil {
					return err
				}
				if reservedPortsByTarget[move.TargetGroupID] == nil {
					reservedPortsByTarget[move.TargetGroupID] = make(map[int]struct{})
				}
				reservedPortsByTarget[move.TargetGroupID][portNum] = struct{}{}
				port := strconv.Itoa(portNum)

				hc, err := cloneHarvestConfig(mapping.HarvestConfig)
				if err != nil {
					return err
				}
				hc.LEASE_NAME = targetGroup.LeaseName
				hc.PORT = port

				// Read node and pool on the open transaction (g) so they share the locked
				// snapshot and do not borrow a second pooled connection (avoids pool-exhaustion
				// deadlock while this tx holds a connection). Mirrors GetNodeByID (Unscoped, to
				// include soft-deleted nodes) and GetPoolByID (scoped) query semantics.
				node := &datamodel.Node{}
				if err := g.Unscoped().First(node, nodeID).Error; err != nil {
					return fmt.Errorf("get node %d in rebalance tx: %w", nodeID, err)
				}
				pool := &datamodel.Pool{}
				if err := g.Where("id = ?", node.PoolID).First(pool).Error; err != nil {
					return fmt.Errorf("get pool %d for node %d in rebalance tx: %w", node.PoolID, nodeID, err)
				}
				var credentials *vlm.OntapCredentials
				if !smHarvestAuthEnabled {
					var ok bool
					credentials, ok = credentialsByPool[pool.ID]
					if !ok || credentials == nil {
						return fmt.Errorf("failed to get credentials for pool %d", pool.ID)
					}
				}
				renderedYAML, err := renderRebalanceYAML(hc, pool, credentials)
				if err != nil {
					return fmt.Errorf("render rebalance yaml node %d: %w", nodeID, err)
				}

				staged = append(staged, RebalanceStagedNode{
					NodeID:          nodeID,
					SourceGroupID:   move.SourceGroupID,
					TargetGroupID:   move.TargetGroupID,
					SourceLeaseName: sourceLeaseName,
					TargetLeaseName: targetGroup.LeaseName,
					Port:            port,
				})
				renderedUploads = append(renderedUploads, renderedRebalanceUpload{NodeID: nodeID, LeaseName: targetGroup.LeaseName, RenderedYAML: renderedYAML})
			}
		}

		if err := validateRebalanceStagedUploads(ctx, staged); err != nil {
			return err
		}

		targetIDsSet := make(map[int64]struct{})
		for _, s := range staged {
			targetIDsSet[s.TargetGroupID] = struct{}{}
		}
		targetGroupIDs := make([]int64, 0, len(targetIDsSet))
		for id := range targetIDsSet {
			targetGroupIDs = append(targetGroupIDs, id)
		}
		sort.Slice(targetGroupIDs, func(i, j int) bool { return targetGroupIDs[i] < targetGroupIDs[j] })

		type groupCountRow struct {
			NodeGroupID int64 `gorm:"column:node_group_id"`
			Cnt         int64 `gorm:"column:cnt"`
		}
		var rows []groupCountRow
		if err := g.Model(&datamodel.NodeNodeGroupMap{}).
			Select("node_group_id, COUNT(*)::bigint AS cnt").
			Where("deleted_at IS NULL AND node_group_id IN ?", targetGroupIDs).
			Group("node_group_id").
			Scan(&rows).Error; err != nil {
			return err
		}
		currentByTarget := make(map[int64]int64, len(targetGroupIDs))
		for _, id := range targetGroupIDs {
			currentByTarget[id] = 0
		}
		for _, row := range rows {
			currentByTarget[row.NodeGroupID] = row.Cnt
		}

		commitMappingsByNodeID, err := a.SE.GetActiveNodeNodeGroupMapsByNodeIDs(ctx, nodeIDs, tx)
		if err != nil {
			return err
		}

		type pendingUpdate struct {
			nodeID    int64
			port      string
			targetID  int64
			targetRef *datamodel.NodeGroup
			mapping   *datamodel.NodeNodeGroupMap
		}
		var updates []pendingUpdate
		incomingByTarget := make(map[int64]int64)
		seenNodeIDs := make(map[int64]struct{})
		for i, s := range staged {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("commit rebalance node %d (%d/%d)", s.NodeID, i+1, len(staged)))
			if _, dup := seenNodeIDs[s.NodeID]; dup {
				continue
			}
			seenNodeIDs[s.NodeID] = struct{}{}

			targetGroup := targetGroupByID[s.TargetGroupID]
			if targetGroup == nil {
				return temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("target node group %d missing or deleted", s.TargetGroupID),
					"HarvestRebalanceInvalidTargetGroup", nil)
			}
			mapping := commitMappingsByNodeID[s.NodeID]
			if mapping.NodeGroupID == s.TargetGroupID {
				if mapping.HarvestConfig != nil &&
					mapping.HarvestConfig.LEASE_NAME == targetGroup.LeaseName &&
					mapping.HarvestConfig.PORT == s.Port {
					continue
				}
				msg := fmt.Sprintf("rebalance commit: node %d already on target group %d but harvest_config does not match expected lease/port",
					s.NodeID, s.TargetGroupID)
				logger.Error(msg)
				return temporal.NewNonRetryableApplicationError(msg, "HarvestRebalanceCommitDrift", nil)
			}
			if mapping.NodeGroupID != s.SourceGroupID {
				msg := fmt.Sprintf("rebalance commit: node %d on group %d, expected source %d (drift or duplicate commit)",
					s.NodeID, mapping.NodeGroupID, s.SourceGroupID)
				logger.Error(msg)
				return temporal.NewNonRetryableApplicationError(msg, "HarvestRebalanceCommitSourceMismatch", nil)
			}
			if mapping.HarvestConfig == nil {
				return temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("node %d has no harvest_config", s.NodeID), "HarvestRebalanceMissingHarvestConfig", nil)
			}
			incomingByTarget[s.TargetGroupID]++
			updates = append(updates, pendingUpdate{
				nodeID:    s.NodeID,
				port:      s.Port,
				targetID:  s.TargetGroupID,
				targetRef: targetGroup,
				mapping:   mapping,
			})
		}

		for targetID, incoming := range incomingByTarget {
			current := currentByTarget[targetID]
			if current+incoming > int64(params.MaxNodesPerGroup) {
				msg := fmt.Sprintf("rebalance commit capacity changed: target group %d has %d assigned, incoming %d exceeds max %d",
					targetID, current, incoming, params.MaxNodesPerGroup)
				logger.Warnf(msg)
				return temporal.NewApplicationError(msg, "HarvestRebalanceCapacityChanged")
			}
		}

		for i, u := range updates {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("commit rebalance DB update node %d (%d/%d)", u.nodeID, i+1, len(updates)))
			u.mapping.NodeGroupID = u.targetID
			u.mapping.NodeGroup = u.targetRef
			u.mapping.HarvestConfig.LEASE_NAME = u.targetRef.LeaseName
			u.mapping.HarvestConfig.PORT = u.port
			if err := g.Save(u.mapping).Error; err != nil {
				if database.IsHarvestNodeGroupPortUniqueViolation(err) {
					msg := fmt.Sprintf("rebalance commit port conflict: node %d target group %d port %s (concurrent assign)",
						u.nodeID, u.targetID, u.port)
					logger.Warnf(msg)
					return temporal.NewApplicationError(msg, "HarvestRebalancePortConflict")
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var uploaded []RebalanceStagedNode
	for i, u := range renderedUploads {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("upload rebalance poller node %d (%d/%d)", u.NodeID, i+1, len(renderedUploads)))
		if upErr := uploadRebalanceTarget(ctx, params.UploadURL, u.LeaseName, u.NodeID, u.RenderedYAML); upErr != nil {
			rollbackUploadedTargets(ctx, params.UploadURL, uploaded)
			return nil, fmt.Errorf("upload rebalance yaml node %d: %w", u.NodeID, upErr)
		}
		uploaded = append(uploaded, RebalanceStagedNode{NodeID: u.NodeID, TargetLeaseName: u.LeaseName})
	}

	for i, s := range staged {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("delete source harvest poller node %d (%d/%d)", s.NodeID, i+1, len(staged)))
		if s.SourceLeaseName == "" || s.SourceLeaseName == s.TargetLeaseName {
			continue
		}
		if err := deleteUploadedRebalanceTarget(ctx, params.UploadURL, s.SourceLeaseName, s.NodeID); err != nil {
			return nil, fmt.Errorf("delete source harvest poller after rebalance commit node %d lease %s: %w", s.NodeID, s.SourceLeaseName, err)
		}
	}

	logger.Infof("UploadValidateCommitRebalanceActivity: processed %d node mapping(s)", len(staged))
	return &RebalanceUploadStageResult{Staged: staged}, nil
}

func validateRebalanceStagedUploads(ctx context.Context, staged []RebalanceStagedNode) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "validateRebalanceStagedUploads started")
	if len(staged) == 0 {
		return nil
	}

	seenNodeIDs := make(map[int64]struct{}, len(staged))
	for i, s := range staged {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("validate upload node %d (%d/%d)", s.NodeID, i+1, len(staged)))
		if s.NodeID <= 0 {
			return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("validate: staged entry has invalid node id %d", s.NodeID))
		}
		if _, exists := seenNodeIDs[s.NodeID]; exists {
			return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("validate: duplicate staged node id %d", s.NodeID))
		}
		seenNodeIDs[s.NodeID] = struct{}{}
		if s.TargetLeaseName == "" {
			return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("validate: staged node %d has empty target lease name", s.NodeID))
		}
		p := strings.TrimSpace(s.Port)
		if p == "" {
			return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("validate: staged node %d has empty poller port", s.NodeID))
		}
	}
	logger.Infof("validateRebalanceStagedUploads: validated upload stage for %d node(s)", len(staged))
	return nil
}

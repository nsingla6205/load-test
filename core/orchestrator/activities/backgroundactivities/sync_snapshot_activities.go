package backgroundactivities

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/hydrationActivities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"gorm.io/gorm"
)

const (
	SnapshotTypeScheduled = "scheduled"
	SnapshotTypeAdHoc     = "ad-hoc"
	SnapshotTypeBackup    = "backup"

	FlexGroupConstituent = "flexgroup_constituent"
)

var (
	snapshotSyncChunkSize                = env.GetInt("CVS_SNAPSHOT_SYNC_CHUNK_SIZE", 200)
	hydrationEnabled                     = env.GetBool("GCP_HYDRATE_ENABLED", true)
	deleteStaleAdHocSnapshots            = env.GetBool("SNAPSHOT_SYNC_DELETE_STALE_ADHOC", true)
	snapshotSyncMaxConcurrency           = env.GetInt("SNAPSHOT_SYNC_MAX_CONCURRENCY", 20)
	ontapSnapshotFetchConcurrencyLimit   = env.GetInt("ONTAP_SNAPSHOT_FETCH_MAX_CONCURRENCY", 5)
	ontapSnapshotConfirmConcurrencyLimit = env.GetInt("ONTAP_SNAPSHOT_CONFIRM_MAX_CONCURRENCY", 20)

	scheduledRegExp  = regexp.MustCompile(`^(hourly|daily|weekly|monthly)\..*$`)
	snapmirrorRegExp = regexp.MustCompile(`^snapmirror\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}_.*$`)

	GetOntapRestProviderForPool         = _getOntapRestProviderForPool
	GetOntapRestProviderForPoolFastConn = _getOntapRestProviderForPoolFastConn
	filterOntapVolumesAndSnapshots      = _filterOntapVolumesAndSnapshots
	processSnapshotSync                 = _processSnapshotSync
	syncDeletedSnapshotsToDatabase      = _syncDeletedSnapshotsToDatabase
	syncNewSnapshotsToDatabase          = _syncNewSnapshotsToDatabase
	syncUpdatedSnapshotsToDatabase      = _syncUpdatedSnapshotsToDatabase
	confirmSnapshotsGoneFromOntap       = _confirmSnapshotsGoneFromOntap
	hydrateBatchSnapshotsToCCFE         = hydrationActivities.HydrateBatchSnapshotstoCCFE
)

type SyncSnapshotActivity struct {
	SE database.Storage
}

// ListPoolsUUIDPaginated returns a paginated list of pool identifiers in READY state from the database.
func (a *SyncSnapshotActivity) ListPoolsUUIDPaginated(ctx context.Context, offset, limit int) ([]*database.PoolIdentifier, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Listing pools paginated")

	filter := utils2.CreateFilterWithConditions(utils2.NewFilterCondition("state", "=", datamodel.LifeCycleStateREADY))
	pools, err := se.ListPoolUUIDsPaginated(ctx, filter, offset, limit)
	if err != nil {
		logger.Errorf("Failed to list pools: %v", err)
		return nil, fmt.Errorf("failed to list pools")
	}
	activity.RecordHeartbeat(ctx, "Pools listed successfully")

	logger.Infof("Found %d pools (offset: %d, limit: %d)", len(pools), offset, limit)
	return pools, nil
}

// GetTotalPoolCount Returns the total count of pools in READY state from the database.
func (a *SyncSnapshotActivity) GetTotalPoolCount(ctx context.Context) (int, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Getting total pool count")

	filter := utils2.CreateFilterWithConditions(utils2.NewFilterCondition("state", "=", datamodel.LifeCycleStateREADY))
	count, err := se.GetPoolsCount(ctx, filter)
	if err != nil {
		logger.Errorf("Failed to count pools: %v", err)
		return 0, fmt.Errorf("failed to count pools")
	}
	activity.RecordHeartbeat(ctx, "Pool count retrieved successfully")

	logger.Debugf("Total pools count: %d", count)
	return int(count), nil
}

func (a *SyncSnapshotActivity) FetchPoolByUUID(ctx context.Context, poolUUID string, accountID int64) (*datamodel.Pool, error) {
	activity.RecordHeartbeat(ctx, "Initializing pool fetch by UUID")
	logger := util.GetLogger(ctx)
	se := a.SE

	pool, err := se.GetPool(ctx, poolUUID, accountID)
	activity.RecordHeartbeat(ctx, "Fetched pool from database")
	if err != nil {
		logger.Errorf("Failed to get pool, error: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return database.ConvertPoolViewToPool(pool), nil
}

func _filterOntapVolumesAndSnapshots(volumes []*vsa.Volume, snapshots []*vsa.Snapshot) (map[string]*vsa.Volume, []*vsa.Snapshot) {
	volumeMap := make(map[string]*vsa.Volume, len(volumes))
	for _, volume := range volumes {
		if volume == nil || volume.IsSvmRoot == nil || *volume.IsSvmRoot {
			continue
		}
		if volume.UUID == nil || volume.Svm == nil || volume.Svm.Name == nil || volume.Name == nil {
			continue
		}
		volumeMap[*volume.UUID] = volume
		volumeMap[*volume.Svm.Name+*volume.Name] = volume
	}

	var filteredSnapshots []*vsa.Snapshot
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.Name == nil || snapshot.ProvenanceVolume == nil || snapshot.ProvenanceVolume.UUID == nil || snapshot.Volume == nil || snapshot.Volume.Name == nil {
			continue
		}

		name := *snapshot.Name

		vol, ok := volumeMap[*snapshot.ProvenanceVolume.UUID]
		if !ok || vol.Name == nil || *vol.Name != *snapshot.Volume.Name {
			vol, ok = volumeMap[*snapshot.Svm.Name+*snapshot.Volume.Name]
			if !ok {
				continue
			}
		}

		if vol.Style == nil || *vol.Style == FlexGroupConstituent {
			continue
		}

		var snapshotType string
		if snapshot.SnapmirrorLabel != nil || scheduledRegExp.MatchString(name) || snapmirrorRegExp.MatchString(name) {
			snapshotType = SnapshotTypeScheduled
		} else {
			snapshotType = SnapshotTypeAdHoc
		}

		if vol.ExternalUUID == "" {
			continue
		}

		snapshot.ExternalVolumeUUID = vol.ExternalUUID
		snapshot.Type = snapshotType
		filteredSnapshots = append(filteredSnapshots, snapshot)
	}

	return volumeMap, filteredSnapshots
}

func _syncUpdatedSnapshotsToDatabase(ctx context.Context, updatedSnapshots []string, updatedSSMap map[string]*vsa.Snapshot, se database.Storage, dbSnapshotsMap map[string]*datamodel.Snapshot) ([]*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)
	var snapshotsToUpdate []*datamodel.Snapshot
	for _, key := range updatedSnapshots {
		snapshot := updatedSSMap[key]
		dbSnapshot := dbSnapshotsMap[key]
		if snapshot == nil || dbSnapshot == nil {
			continue
		}
		name := dbSnapshot.Name
		if snapshot.Name != nil {
			name = *snapshot.Name
		}
		dbSnap := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				UUID: dbSnapshot.UUID,
				ID:   dbSnapshot.ID,
			},
			Name: name,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            snapshot.SizeInBytes,
				LogicalSizeUsedInBytes: snapshot.LogicalSizeUsedInBytes,
			},
		}
		dbSnap.SetExternalUUID(snapshot.ExternalUUID)
		snapshotsToUpdate = append(snapshotsToUpdate, dbSnap)
	}
	if len(snapshotsToUpdate) == 0 {
		return nil, nil
	}
	var allUpdated []*datamodel.Snapshot
	for i := 0; i < len(snapshotsToUpdate); i += snapshotSyncChunkSize {
		end := i + snapshotSyncChunkSize
		if end > len(snapshotsToUpdate) {
			end = len(snapshotsToUpdate)
		}
		chunk := snapshotsToUpdate[i:end]
		if err := se.BatchUpdateSnapshots(ctx, chunk); err != nil {
			logger.Errorf("Failed to update snapshots: %v", err)
			return nil, err
		}
		allUpdated = append(allUpdated, chunk...)
	}
	return allUpdated, nil
}

type snapshotVolumeExternalKey struct {
	volumeID     int64
	externalUUID string
}

func existingSnapshotVolumeExternalKeys(ctx context.Context, se database.Storage, candidates []*datamodel.Snapshot) (map[snapshotVolumeExternalKey]struct{}, error) {
	volumeIDs, externalUUIDs := uniqueSnapshotLookupKeys(candidates)
	existing := make(map[snapshotVolumeExternalKey]struct{})
	if len(volumeIDs) == 0 || len(externalUUIDs) == 0 {
		return existing, nil
	}

	// Look up existing (volume_id, external_uuid) pairs in UUID chunks, with all candidate volume IDs each call.
	chunkSize := snapshotSyncChunkSize
	if chunkSize <= 0 {
		chunkSize = 200
	}
	for i := 0; i < len(externalUUIDs); i += chunkSize {
		extEnd := i + chunkSize
		if extEnd > len(externalUUIDs) {
			extEnd = len(externalUUIDs)
		}
		pairs, err := se.ListSnapshotVolumeExternalUUIDs(ctx, volumeIDs, externalUUIDs[i:extEnd])
		if err != nil {
			return nil, err
		}
		for _, pair := range pairs {
			existing[snapshotVolumeExternalKey{volumeID: pair.VolumeID, externalUUID: pair.ExternalUUID}] = struct{}{}
		}
	}
	return existing, nil
}

func uniqueSnapshotLookupKeys(candidates []*datamodel.Snapshot) ([]int64, []string) {
	seenVolumes := make(map[int64]struct{}, len(candidates))
	seenExternal := make(map[string]struct{}, len(candidates))
	volumeIDs := make([]int64, 0, len(candidates))
	externalUUIDs := make([]string, 0, len(candidates))
	for _, snapshot := range candidates {
		externalUUID := snapshot.GetExternalUUID()
		if externalUUID == "" {
			continue
		}
		if _, ok := seenVolumes[snapshot.VolumeID]; !ok {
			seenVolumes[snapshot.VolumeID] = struct{}{}
			volumeIDs = append(volumeIDs, snapshot.VolumeID)
		}
		if _, ok := seenExternal[externalUUID]; !ok {
			seenExternal[externalUUID] = struct{}{}
			externalUUIDs = append(externalUUIDs, externalUUID)
		}
	}
	return volumeIDs, externalUUIDs
}

func _syncNewSnapshotsToDatabase(ctx context.Context, newSnapshots []string, newSSMap map[string]*vsa.Snapshot, se database.Storage, dbVolumeMap map[string]*datamodel.Volume, pool *datamodel.Pool) ([]*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)
	var candidates []*datamodel.Snapshot
	for _, key := range newSnapshots {
		snapshot := newSSMap[key]
		if snapshot == nil || snapshot.Name == nil {
			continue
		}
		vol := dbVolumeMap[snapshot.ExternalVolumeUUID]
		if vol == nil {
			continue
		}
		dbSnap := &datamodel.Snapshot{
			Name:      *snapshot.Name,
			VolumeID:  vol.ID,
			AccountID: pool.AccountID,
			Volume:    vol,
			Account:   pool.Account,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            snapshot.SizeInBytes,
				LogicalSizeUsedInBytes: snapshot.LogicalSizeUsedInBytes,
			},
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			Type:         snapshot.Type,
		}
		dbSnap.SetExternalUUID(snapshot.ExternalUUID)
		candidates = append(candidates, dbSnap)
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	existing, err := existingSnapshotVolumeExternalKeys(ctx, se, candidates)
	if err != nil {
		logger.Errorf("Failed to look up existing snapshots before insert: %v", err)
		return nil, err
	}

	var snapshotsToCreate []*datamodel.Snapshot
	for _, dbSnap := range candidates {
		externalUUID := dbSnap.GetExternalUUID()
		if externalUUID != "" {
			key := snapshotVolumeExternalKey{volumeID: dbSnap.VolumeID, externalUUID: externalUUID}
			if _, ok := existing[key]; ok {
				logger.Infof("Skipped inserting snapshot %s on volume %d; a row already exists for external UUID %s", dbSnap.Name, dbSnap.VolumeID, externalUUID)
				continue
			}
		}
		snapshotsToCreate = append(snapshotsToCreate, dbSnap)
	}
	if len(snapshotsToCreate) == 0 {
		return nil, nil
	}

	var createdSnapshots []*datamodel.Snapshot
	var allCreatedUUIDs []string
	for i := 0; i < len(snapshotsToCreate); i += snapshotSyncChunkSize {
		end := i + snapshotSyncChunkSize
		if end > len(snapshotsToCreate) {
			end = len(snapshotsToCreate)
		}
		chunk := snapshotsToCreate[i:end]
		createdUUIDs, err := se.BatchCreateSnapshots(ctx, chunk, true)
		if err != nil {
			logger.Errorf("Failed to create snapshots: %v", err)
			return nil, err
		}
		allCreatedUUIDs = append(allCreatedUUIDs, createdUUIDs...)
	}

	if len(allCreatedUUIDs) > 0 {
		createdSnapshots, err := se.BatchGetSnapshotsByUUIDs(ctx, allCreatedUUIDs)
		if err != nil {
			return nil, err
		}
		return createdSnapshots, nil
	}
	return createdSnapshots, nil
}

// snapshotsObservedForDelete returns the baseline rows selected for deletion, preserving the updated_at observed before the ONTAP read.
func snapshotsObservedForDelete(dbSnapshots []*datamodel.Snapshot, deleteIDs []int64) []*datamodel.Snapshot {
	if len(deleteIDs) == 0 {
		return nil
	}
	byID := make(map[int64]*datamodel.Snapshot, len(dbSnapshots))
	for _, snapshot := range dbSnapshots {
		if snapshot == nil || snapshot.ID == 0 {
			continue
		}
		byID[snapshot.ID] = snapshot
	}
	observed := make([]*datamodel.Snapshot, 0, len(deleteIDs))
	for _, id := range deleteIDs {
		snapshot, ok := byID[id]
		if !ok {
			continue
		}
		observed = append(observed, snapshot)
	}
	return observed
}

func _syncDeletedSnapshotsToDatabase(ctx context.Context, snapshots []*datamodel.Snapshot, se database.Storage) ([]*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)
	if len(snapshots) == 0 {
		return nil, nil
	}
	var deletedSnapshots []*datamodel.Snapshot
	chunkSize := snapshotSyncChunkSize
	if chunkSize <= 0 {
		chunkSize = 200
	}
	for i := 0; i < len(snapshots); i += chunkSize {
		end := i + chunkSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		deletedSnapshotChunk, err := se.BatchDeleteReadySnapshotsIfUnchanged(ctx, snapshots[i:end])
		if err != nil {
			logger.Errorf("Failed to delete snapshots: %v", err)
			return nil, err
		}
		deletedSnapshots = append(deletedSnapshots, deletedSnapshotChunk...)
	}
	return deletedSnapshots, nil
}

// positiveConcurrency returns limit when positive, otherwise fallback.
// A non-positive buffered channel capacity would deadlock every sender.
func positiveConcurrency(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	return limit
}

// tryAcquireSemaphore acquires one semaphore slot, or returns false if ctx is done.
// When true is returned the caller must release with `<-semaphore` (typically via defer).
// If both a slot and cancellation are ready, cancellation wins so a cancelled activity does not
// start more ONTAP work (Go select would otherwise choose non-deterministically).
func tryAcquireSemaphore(ctx context.Context, semaphore chan struct{}) bool {
	select {
	case <-ctx.Done():
		return false
	default:
	}
	select {
	case semaphore <- struct{}{}:
		// Re-check: slot and cancel can become ready together while waiting.
		if ctx.Err() != nil {
			<-semaphore
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

// _confirmSnapshotsGoneFromOntap keeps only candidates that ONTAP get-by-UUID reports as not found.
// Snapshots that still exist, or whose existence cannot be confirmed, are skipped so sync does not
// soft-delete on an incomplete or lagging list response.
//
// Concurrency notes:
//   - Each results[i] is written by at most one goroutine (no mutex).
//   - Semaphore acquire is cancellation-aware so queued workers do not block wg.Wait forever
//     or issue GetSnapshot after the activity is cancelled.
//   - In-flight GetSnapshot calls are not context-aware today; callers must treat a cancelled
//     ctx after return as abort (do not apply a partial confirmed set).
func _confirmSnapshotsGoneFromOntap(ctx context.Context, provider vsa.Provider, candidates []*datamodel.Snapshot) []*datamodel.Snapshot {
	logger := util.GetLogger(ctx)
	if len(candidates) == 0 || provider == nil {
		return nil
	}

	maxConcurrency := positiveConcurrency(ontapSnapshotConfirmConcurrencyLimit, 20)
	semaphore := make(chan struct{}, maxConcurrency)

	type confirmResult struct {
		snapshot *datamodel.Snapshot
		gone     bool
	}
	results := make([]confirmResult, len(candidates))
	var wg sync.WaitGroup
	var completed int64
	var mu sync.Mutex

	safeRecordHeartbeat(ctx, fmt.Sprintf("confirming %d snapshot deletes against ONTAP (concurrency %d)", len(candidates), maxConcurrency))

	for i, candidate := range candidates {
		if candidate == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, snapshot *datamodel.Snapshot) {
			defer wg.Done()
			if !tryAcquireSemaphore(ctx, semaphore) {
				return
			}
			defer func() { <-semaphore }()
			if ctx.Err() != nil {
				return
			}

			snapshotUUID := snapshot.GetExternalUUID()
			volumeUUID := dbSnapshotVolumeExternalUUID(snapshot)
			if snapshotUUID == "" || volumeUUID == "" {
				logger.Warnf("Skipped delete confirmation for snapshot id %d: missing external UUID or volume UUID", snapshot.ID)
				return
			}

			_, err := provider.GetSnapshot(snapshotUUID, volumeUUID)
			if err == nil {
				logger.Infof("Skipped deleting snapshot %s on volume %s; ONTAP get-by-UUID still found it", snapshotUUID, volumeUUID)
				heartbeatConfirmProgress(ctx, &mu, &completed, len(candidates))
				return
			}
			if customerrors.IsNotFoundErrForObjectTypeInChain(err, "Snapshot") {
				results[idx] = confirmResult{snapshot: snapshot, gone: true}
				heartbeatConfirmProgress(ctx, &mu, &completed, len(candidates))
				return
			}
			logger.Warnf("Skipped deleting snapshot %s on volume %s; ONTAP get-by-UUID was inconclusive: %v", snapshotUUID, volumeUUID, err)
			heartbeatConfirmProgress(ctx, &mu, &completed, len(candidates))
		}(i, candidate)
	}
	wg.Wait()

	confirmed := make([]*datamodel.Snapshot, 0, len(candidates))
	for _, result := range results {
		if result.gone && result.snapshot != nil {
			confirmed = append(confirmed, result.snapshot)
		}
	}
	return confirmed
}

// heartbeatConfirmProgress records a Temporal heartbeat every 50 completed confirms so long
// delete-confirm waves do not look idle under activity heartbeat timeouts.
func heartbeatConfirmProgress(ctx context.Context, mu *sync.Mutex, completed *int64, total int) {
	mu.Lock()
	*completed++
	count := *completed
	mu.Unlock()
	if count%50 == 0 || count == int64(total) {
		safeRecordHeartbeat(ctx, fmt.Sprintf("confirmed %d/%d snapshot deletes against ONTAP", count, total))
	}
}

func _getOntapRestProviderForPool(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
	logger := util.GetLogger(ctx)

	// PO / extreme ONTAP-mode pools talk to ONTAP via the hardware host, not VSA nodes.
	if pool != nil && strings.TrimSpace(pool.HostUUID) != "" && pool.IsDedicatedCapacity() {
		host, err := se.GetHost(ctx, pool.HostUUID)
		if err != nil {
			logger.Errorf("Failed to get host %s for pool %s: %v", pool.HostUUID, pool.UUID, err)
			if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		provider, err := vsa.GetProviderForHost(ctx, host)
		if err != nil {
			logger.Errorf("Failed to get ONTAP provider for host %s: %v", pool.HostUUID, err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		return provider, nil
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		logger.Errorf("Failed to get nodes for pool: %v", err)
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(nodes) == 0 {
		logger.Errorf("No nodes found for pool %s", pool.UUID)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("no nodes found for pool %s", pool.UUID))
	}

	if pool.PoolCredentials == nil {
		logger.Errorf("Pool credentials not found for pool %s", pool.UUID)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool credentials not found for pool %s", pool.UUID))
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get provider by node: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider, nil
}

func _getOntapRestProviderForPoolFastConn(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
	logger := util.GetLogger(ctx)

	// PO / extreme ONTAP-mode pools talk to ONTAP via the hardware host, not VSA nodes.
	if pool != nil && strings.TrimSpace(pool.HostUUID) != "" && pool.IsDedicatedCapacity() {
		host, err := se.GetHost(ctx, pool.HostUUID)
		if err != nil {
			if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		provider, err := vsa.GetProviderForHost(ctx, host)
		if err != nil {
			logger.Errorf("Failed to get ONTAP provider for host %s: %v", pool.HostUUID, err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		return provider, nil
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(nodes) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("no nodes found for pool %s", pool.UUID))
	}

	if pool.PoolCredentials == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool credentials not found for pool %s", pool.UUID))
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	provider, err := vsa.GetProviderByNodeWithFastConnection(ctx, node)
	if err != nil {
		logger.Errorf("Failed to get provider by node with fast connection: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider, nil
}

// snapshotNeedsSizeOrNameUpdate reports whether the ONTAP snapshot differs from the DB row in
// size or name. Nil SnapshotAttributes is treated as needing an update so sync can heal the row.
// A nil ONTAP name is ignored for the name comparison to avoid panics on incomplete list payloads.
func snapshotNeedsSizeOrNameUpdate(dbSnapshot *datamodel.Snapshot, snapshot *vsa.Snapshot) bool {
	if dbSnapshot == nil || snapshot == nil {
		return false
	}
	if dbSnapshot.SnapshotAttributes == nil {
		return true
	}
	if dbSnapshot.SnapshotAttributes.SizeInBytes != snapshot.SizeInBytes ||
		dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes != snapshot.LogicalSizeUsedInBytes {
		return true
	}
	if snapshot.Name != nil && dbSnapshot.Name != *snapshot.Name {
		return true
	}
	return false
}

func _processSnapshotSync(ctx context.Context, ontapVolumeMap map[string]*vsa.Volume, ontapSnapshots []*vsa.Snapshot, dbVolumeMap map[string]*datamodel.Volume, dbSnapshots []*datamodel.Snapshot, snapshotFetchSucceeded map[string]struct{}) (
	newSSMap map[string]*vsa.Snapshot, updatedSSMap map[string]*vsa.Snapshot,
	newIDs []string, updatedIDs []string, deleteIDs []int64) {
	logger := util.GetLogger(ctx)

	dbSnapshotMap := make(map[string]*datamodel.Snapshot, len(dbSnapshots))
	dbSnapshotExternalUUIDMap := make(map[string]struct{}, len(dbSnapshots))
	for _, dbSnapshot := range dbSnapshots {
		volumeExternalUUID := dbSnapshotVolumeExternalUUID(dbSnapshot)
		if volumeExternalUUID == "" {
			logger.Warn("Skipped snapshot during sync - database volume is missing.")
			continue
		}
		dbSnapshotExternalUUIDMap[dbSnapshot.GetExternalUUID()] = struct{}{}
		dbSnapshotMap[dbSnapshot.GetExternalUUID()+"."+volumeExternalUUID] = dbSnapshot
	}

	newSSMap = make(map[string]*vsa.Snapshot, len(ontapSnapshots))
	updatedSSMap = make(map[string]*vsa.Snapshot, len(ontapSnapshots))
	existingSSMap := make(map[string]*vsa.Snapshot, len(dbSnapshotMap))

	for _, snapshot := range ontapSnapshots {
		key := snapshot.ExternalUUID + "." + snapshot.ExternalVolumeUUID

		if dbSnapshot, ok := dbSnapshotMap[key]; ok {
			existingSSMap[key] = snapshot

			if snapshotNeedsSizeOrNameUpdate(dbSnapshot, snapshot) {
				updatedSSMap[key] = snapshot
				updatedIDs = append(updatedIDs, key)
			}
		} else {
			vol, volumeExistsInOntap := ontapVolumeMap[snapshot.ExternalVolumeUUID]
			if !volumeExistsInOntap {
				continue
			}
			_, isCloned := dbSnapshotExternalUUIDMap[snapshot.ExternalUUID]

			if (vol.Type != nil && *vol.Type == "dp") || snapshot.Type != SnapshotTypeAdHoc || isCloned {
				newSSMap[key] = snapshot
				newIDs = append(newIDs, key)
			}
		}
	}

	for _, dbSnapshot := range dbSnapshots {
		volumeExternalUUID := dbSnapshotVolumeExternalUUID(dbSnapshot)
		if volumeExternalUUID == "" {
			continue
		}
		key := dbSnapshot.GetExternalUUID() + "." + volumeExternalUUID
		_, isNew := newSSMap[key]
		_, stillExists := existingSSMap[key]
		if !stillExists && !isNew {
			_, volExistsInOntap := ontapVolumeMap[volumeExternalUUID]
			if !volExistsInOntap {
				logger.Warn("Skipped deleting snapshot from database - ONTAP volume is missing.")
				continue
			}
			if _, fetchSucceeded := snapshotFetchSucceeded[volumeExternalUUID]; !fetchSucceeded {
				logger.Warn("Skipped deleting snapshot from database - ONTAP snapshot list was not fetched for the volume.")
				continue
			}
			if !deleteStaleAdHocSnapshots && dbSnapshot.Type == SnapshotTypeAdHoc {
				logger.Warnf("Skipped deleting ad-hoc snapshot %s from database - SNAPSHOT_SYNC_DELETE_STALE_ADHOC is disabled.", dbSnapshot.GetExternalUUID())
				continue
			}
			if dbSnapshot.Type == SnapshotTypeAdHoc {
				logger.Infof("Deleting stale ad-hoc snapshot %s because it is missing from ONTAP.", dbSnapshot.GetExternalUUID())
			}

			deleteIDs = append(deleteIDs, dbSnapshot.ID)
		}
	}
	return
}

type GetOntapVolumesAndSnapshotsForPoolReturnValue struct {
	OntapVolumeMap         map[string]*vsa.Volume
	OntapSnapshots         []*vsa.Snapshot
	SnapshotFetchSucceeded map[string]struct{}
}

func (a *SyncSnapshotActivity) GetOntapVolumesAndSnapshotsForPool(ctx context.Context, pool *datamodel.Pool) (*GetOntapVolumesAndSnapshotsForPoolReturnValue, error) {
	logger := util.GetLogger(ctx)
	se := a.SE
	provider, err := GetOntapRestProviderForPool(ctx, se, pool)
	if err != nil || provider == nil {
		errMsg := fmt.Sprintf("Failed to get ONTAP rest provider for pool %v: %v", pool.UUID, err)
		logger.Error(errMsg)
		return nil, err
	}

	// Fetch ONTAP volumes with a 30-second timeout to prevent long retries.
	done := make(chan struct{})
	var ontapVolumes []*vsa.Volume
	var volumesErr error

	go func() {
		defer close(done)
		ontapVolumes, volumesErr = provider.GetVolumes()
	}()

	select {
	case <-done:
		if volumesErr != nil {
			errMsg := fmt.Sprintf("Failed to get ONTAP volumes for the pool: %s, %v", pool.UUID, volumesErr)
			logger.Error(errMsg)
			return nil, volumesErr
		}
	case <-time.After(30 * time.Second):
		errMsg := fmt.Sprintf("Timeout getting ONTAP volumes for pool %s after 30 seconds", pool.UUID)
		logger.Error(errMsg)
		return nil, errors.New(errMsg)
	case <-ctx.Done():
		errMsg := fmt.Sprintf("Context cancelled while getting ONTAP volumes for pool %s", pool.UUID)
		logger.Error(errMsg)
		return nil, ctx.Err()
	}

	// Get snapshots in parallel with controlled concurrency
	var ontapSnapshots []*vsa.Snapshot
	snapshotFetchSucceeded := make(map[string]struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex

	maxConcurrency := positiveConcurrency(ontapSnapshotFetchConcurrencyLimit, 5)
	semaphore := make(chan struct{}, maxConcurrency)

	for _, volume := range ontapVolumes {
		// Check if context is cancelled before starting new goroutine
		if ctx.Err() != nil {
			logger.Warnf("Context cancelled, stopping snapshot fetch for remaining volumes")
			break
		}

		wg.Add(1)
		go func(vol *vsa.Volume) {
			defer wg.Done()
			if !tryAcquireSemaphore(ctx, semaphore) {
				return
			}
			defer func() { <-semaphore }()

			// Check context cancellation before processing
			if ctx.Err() != nil {
				return
			}

			// Using CLI API instead of REST API to get accurate sizes (size and afs_used)
			var volumeSnapshots []*vsa.Snapshot
			var cliErr error
			if vol.Name != nil {
				// Get SVM name if available (optional - vserver filter is optional in CLI API)
				svmName := ""
				if vol.Svm != nil && vol.Svm.Name != nil {
					svmName = *vol.Svm.Name
				}
				volumeSnapshots, cliErr = provider.GetSnapshotsViaCLIAPI(ctx, *vol.Name, svmName)
				if cliErr != nil {
					logger.Errorf("Failed to get snapshots from ONTAP CLI API for volume %s: %v, falling back to REST API", *vol.Name, cliErr)
				}
			}
			// Fallback to REST API if CLI API failed or name is missing
			if cliErr != nil || vol.Name == nil {
				if vol.UUID == nil {
					logger.Warn("ALERT: Volume missing UUID, cannot fall back to REST API, skipping snapshot fetch")
					return
				}
				var restErr error
				volumeSnapshots, restErr = provider.GetSnapshots(*vol.UUID)
				if restErr != nil {
					logger.Errorf("ALERT: Failed to get snapshots from ONTAP REST API for volume %s: %v", *vol.UUID, restErr)
					return
				}
				logger.Infof("Successfully fetched snapshots for volume %s using REST API fallback", *vol.UUID)
			}
			// Check context cancellation before appending results
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			ontapSnapshots = append(ontapSnapshots, volumeSnapshots...)
			if volumeExternalUUID := ontapVolumeExternalUUID(vol); volumeExternalUUID != "" {
				snapshotFetchSucceeded[volumeExternalUUID] = struct{}{}
			}
			mu.Unlock()
		}(volume)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		logger.Warnf("Context cancelled while fetching ONTAP snapshots for pool %s: %v", pool.UUID, err)
		return nil, err
	}

	ontapVolumeMap, ontapSnapshots := filterOntapVolumesAndSnapshots(ontapVolumes, ontapSnapshots)

	return &GetOntapVolumesAndSnapshotsForPoolReturnValue{
		OntapVolumeMap:         ontapVolumeMap,
		OntapSnapshots:         ontapSnapshots,
		SnapshotFetchSucceeded: snapshotFetchSucceeded,
	}, nil
}

func dbSnapshotVolumeExternalUUID(dbSnapshot *datamodel.Snapshot) string {
	if dbSnapshot == nil || dbSnapshot.Volume == nil || dbSnapshot.Volume.VolumeAttributes == nil {
		return ""
	}
	return dbSnapshot.Volume.VolumeAttributes.ExternalUUID
}

func ontapVolumeExternalUUID(vol *vsa.Volume) string {
	if vol == nil {
		return ""
	}
	if vol.ExternalUUID != "" {
		return vol.ExternalUUID
	}
	if vol.UUID != nil {
		return *vol.UUID
	}
	return ""
}

type GetDBVolumeAndSnapshotsForPoolReturnValue struct {
	DBVolumeMap   map[string]*datamodel.Volume
	DBSnapshotMap map[string]*datamodel.Snapshot
	DBSnapshots   []*datamodel.Snapshot
}

func (a *SyncSnapshotActivity) GetDBVolumeAndSnapshotsForPool(ctx context.Context, pool *datamodel.Pool) (*GetDBVolumeAndSnapshotsForPoolReturnValue, error) {
	se := a.SE
	logger := util.GetLogger(ctx)
	dbVolumes, err := se.GetVolumesByPoolIDNoPreload(ctx, pool.ID)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get volumes from database for pool %s: %v", pool.UUID, err)
		logger.Error(errMsg)
		return nil, err
	}

	var (
		dbVolumeIDs []int64
		dbVolumeMap = make(map[string]*datamodel.Volume, len(dbVolumes))
	)
	for _, v := range dbVolumes {
		if v == nil || v.State == datamodel.LifeCycleStateDeleting || v.State == datamodel.LifeCycleStateDeleted {
			continue
		}
		dbVolumeIDs = append(dbVolumeIDs, v.ID)
		dbVolumeMap[v.VolumeAttributes.ExternalUUID] = v
	}

	dbSnapshots, err := se.GetSnapshotsByVolumeIDs(ctx, dbVolumeIDs)
	if err != nil {
		errMsg := fmt.Sprintf("Failed to get snapshots from database for pool %s: %v", pool.UUID, err)
		logger.Error(errMsg)
		return nil, err
	}

	var dbSnapshotMap = make(map[string]*datamodel.Snapshot, len(dbSnapshots))
	for _, dbSnapshot := range dbSnapshots {
		volumeExternalUUID := dbSnapshotVolumeExternalUUID(dbSnapshot)
		if volumeExternalUUID == "" {
			logger.Warn("Skipped snapshot during sync - database volume is missing.")
			continue
		}
		dbSnapshotMap[dbSnapshot.GetExternalUUID()+"."+volumeExternalUUID] = dbSnapshot
	}

	return &GetDBVolumeAndSnapshotsForPoolReturnValue{
		DBVolumeMap:   dbVolumeMap,
		DBSnapshotMap: dbSnapshotMap,
		DBSnapshots:   dbSnapshots,
	}, nil
}

type ProcessSnapshotsReturnValue struct {
	NewSSMap     map[string]*vsa.Snapshot
	UpdatedSSMap map[string]*vsa.Snapshot
	NewIDs       []string
	UpdatedIDs   []string
	DeleteIDs    []int64
}

func (a *SyncSnapshotActivity) ProcessSnapshots(ctx context.Context, ontapVolumeMap map[string]*vsa.Volume, ontapSnapshots []*vsa.Snapshot, dbVolumeMap map[string]*datamodel.Volume, dbSnapshots []*datamodel.Snapshot, snapshotFetchSucceeded map[string]struct{}) (*ProcessSnapshotsReturnValue, error) {
	newSSMap, updatedSSMap, newIds, updatedIDs, deleteIDs :=
		processSnapshotSync(ctx, ontapVolumeMap, ontapSnapshots, dbVolumeMap, dbSnapshots, snapshotFetchSucceeded)

	return &ProcessSnapshotsReturnValue{
		NewSSMap:     newSSMap,
		UpdatedSSMap: updatedSSMap,
		NewIDs:       newIds,
		UpdatedIDs:   updatedIDs,
		DeleteIDs:    deleteIDs,
	}, nil
}

func (a *SyncSnapshotActivity) SyncDeletedSnapshotsToDatabase(ctx context.Context, snapshots []*datamodel.Snapshot) ([]*datamodel.Snapshot, error) {
	return syncDeletedSnapshotsToDatabase(ctx, snapshots, a.SE)
}

func (a *SyncSnapshotActivity) SyncNewSnapshotsToDatabase(ctx context.Context, newSnapshots []string, newSSMap map[string]*vsa.Snapshot, dbVolumeMap map[string]*datamodel.Volume, pool *datamodel.Pool) ([]*datamodel.Snapshot, error) {
	return syncNewSnapshotsToDatabase(ctx, newSnapshots, newSSMap, a.SE, dbVolumeMap, pool)
}

func (a *SyncSnapshotActivity) SyncUpdatedSnapshotsToDatabase(ctx context.Context, updatedSnapshots []string, updatedSSMap map[string]*vsa.Snapshot, dbSnapshotsMap map[string]*datamodel.Snapshot) ([]*datamodel.Snapshot, error) {
	return syncUpdatedSnapshotsToDatabase(ctx, updatedSnapshots, updatedSSMap, a.SE, dbSnapshotsMap)
}

func (a *SyncSnapshotActivity) HydrateSnapshotsToCCFE(ctx context.Context, createdSnapshots []*datamodel.Snapshot, deletedSnapshots []*datamodel.Snapshot) error {
	logger := util.GetLogger(ctx)
	if len(createdSnapshots) == 0 && len(deletedSnapshots) == 0 {
		logger.Info("No snapshots to hydrate to CCFE, skipping hydration")
		return nil
	}

	if hydrationEnabled {
		return hydrateBatchSnapshotsToCCFE(ctx, createdSnapshots, deletedSnapshots)
	} else {
		logger.Warn("Hydration is disabled, skipping snapshot hydration to CCFE")
	}
	return nil
}

// hydrateSplitVolumeRegularAfterCloneSplit PATCHes CCFE so cloneDetails is cleared after a clone→regular split (used only from snapshot sync).
func (a *SyncSnapshotActivity) hydrateSplitVolumeRegularAfterCloneSplit(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)
	if volume == nil {
		return fmt.Errorf("volume is nil")
	}
	if volume.PoolID == 0 {
		logger.Warnf("Skipping split-regular CCFE hydration: empty pool ID for volume %s", volume.UUID)
		return nil
	}
	if volume.VolumeAttributes == nil {
		return fmt.Errorf("volume %s has nil volume attributes", volume.UUID)
	}
	project := volume.VolumeAttributes.AccountName
	if project == "" && volume.Account != nil {
		project = volume.Account.Name
	}
	if project == "" {
		return fmt.Errorf("volume %s has no account/project name for hydration", volume.UUID)
	}
	pool, err := a.SE.GetPoolByID(ctx, volume.PoolID)
	if err != nil {
		return err
	}
	location, err := utils.GetLocationFromVendorID(pool.VendorID)
	if err != nil {
		return err
	}
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	payload := models.VolumeUpdateCCFERequest{CloneDetails: nil}
	return orchcommon.HydrateUpdatedVolume(ctx, payload, location, project, volume.Name, token)
}

// hydrateSplitRegularVolumesPendingCCFE patches volumes whose clone→regular CCFE update was deferred after split.
func (a *SyncSnapshotActivity) hydrateSplitRegularVolumesPendingCCFE(ctx context.Context, dbVolumeMap map[string]*datamodel.Volume) error {
	logger := util.GetLogger(ctx)
	if !hydrationEnabled {
		logger.Warn("Hydration is disabled, skipping split-regular volume hydration to CCFE")
		return nil
	}

	for _, vol := range dbVolumeMap {
		if vol == nil || vol.VolumeAttributes == nil || !vol.VolumeAttributes.SplitRegularVolumeHydrationPending {
			continue
		}

		if err := a.hydrateSplitVolumeRegularAfterCloneSplit(ctx, vol); err != nil {
			return fmt.Errorf("split-regular CCFE hydrate for volume %s: %w", vol.UUID, err)
		}

		attrs := *vol.VolumeAttributes
		attrs.SplitRegularVolumeHydrationPending = false
		if err := a.SE.UpdateVolumeFields(ctx, vol.UUID, map[string]interface{}{
			"volume_attributes": &attrs,
		}); err != nil {
			return fmt.Errorf("clear split_regular_ccfe_hydration_pending for volume %s: %w", vol.UUID, err)
		}
	}
	return nil
}

// SyncSnapshotsForPoolBatchReturnValue represents the return value for batch processing
type SyncSnapshotsForPoolBatchReturnValue struct {
	TotalProcessed       int
	Successful           int
	Failed               int
	FailedResourceNames  []string
	FailedResourceErrors []ParentChildWorkflowError
}

// SyncSnapshotsForPoolBatchActivity processes a batch of pools for snapshot synchronization
func (a *SyncSnapshotActivity) SyncSnapshotsForPoolBatchActivity(ctx context.Context, poolIdentifiers []*database.PoolIdentifier) (*SyncSnapshotsForPoolBatchReturnValue, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting snapshot sync batch")
	if len(poolIdentifiers) == 0 {
		return &SyncSnapshotsForPoolBatchReturnValue{}, nil
	}

	logger.Infof("Starting batch processing for snapshot synchronization: total pools = %d", len(poolIdentifiers))
	result := &SyncSnapshotsForPoolBatchReturnValue{
		TotalProcessed: len(poolIdentifiers),
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Processing %d pools", len(poolIdentifiers)))

	// Process each pool in the batch with controlled concurrency
	var wg sync.WaitGroup
	var mu sync.Mutex
	var FailedResourceNames []string
	var FailedResourceErrors []ParentChildWorkflowError

	// Concurrent goroutines to process the snapshot sync for each pool.
	// Capacity must be > 0; a zero-capacity channel would deadlock every acquire.
	semaphore := make(chan struct{}, positiveConcurrency(snapshotSyncMaxConcurrency, 20))

	for _, poolIdentifier := range poolIdentifiers {
		// Check if context is cancelled before starting new goroutine
		if ctx.Err() != nil {
			logger.Warnf("Context cancelled, stopping snapshot sync for remaining pools")
			break
		}

		wg.Add(1)
		go func(pid *database.PoolIdentifier) {
			defer wg.Done()

			if !tryAcquireSemaphore(ctx, semaphore) {
				return
			}
			defer func() { <-semaphore }()

			// Check context cancellation before processing
			if ctx.Err() != nil {
				return
			}

			err := a.processPoolSnapshotSync(ctx, pid)

			mu.Lock()
			if err != nil {
				logger.Errorf("Failed to sync snapshots for pool %s: %v", pid.Name, err)
				result.Failed++
				FailedResourceNames = append(FailedResourceNames, pid.Name)
				FailedResourceErrors = append(FailedResourceErrors, ParentChildWorkflowError{
					ResourceName: pid.Name,
					Error:        err.Error(),
				})
			} else {
				result.Successful++
			}
			mu.Unlock()
		}(poolIdentifier)
	}
	// Wait for all goroutines to complete
	wg.Wait()
	activity.RecordHeartbeat(ctx, "Snapshot sync batch completed")
	logger.Infof("Snapshot batch processing completed: total pools = %d, successful = %d, failed = %d", result.TotalProcessed, result.Successful, result.Failed)

	if result.Failed > 0 {
		logger.Warnf("Snapshot sync failed for %d pools. Failed pools: %v", result.Failed, FailedResourceNames)
	}

	result.FailedResourceNames = FailedResourceNames
	result.FailedResourceErrors = FailedResourceErrors
	return result, nil
}

// processPoolSnapshotSync processes snapshot synchronization for a single pool
func (a *SyncSnapshotActivity) processPoolSnapshotSync(ctx context.Context, poolIdentifier *database.PoolIdentifier) error {
	baseLogger := util.GetLogger(ctx)
	logger := baseLogger.With(log.Fields{"poolName": poolIdentifier.Name})
	logger.Infof("Starting processPoolSnapshotSync for pool: %s", poolIdentifier.Name)

	// Fetch pool details
	pool, err := a.FetchPoolByUUID(ctx, poolIdentifier.UUID, poolIdentifier.AccountID)
	if err != nil {
		logger.Errorf("Failed to fetch pool %s: %v", poolIdentifier.Name, err)
		return fmt.Errorf("FetchPoolByUUID Failed: %w", err)
	}

	// Read the database baseline before ONTAP. A snapshot that becomes READY while the ONTAP
	// list is being fetched is absent from this baseline, so this pass cannot delete it.
	dbVolSnapshotResp, err := a.GetDBVolumeAndSnapshotsForPool(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to get database volumes and snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("GetDBVolumeAndSnapshotsForPool Failed: %w", err)
	}

	ontapVolSnapshotResp, err := a.GetOntapVolumesAndSnapshotsForPool(ctx, pool)
	if err != nil {
		logger.Errorf("Failed to get ONTAP volumes and snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("GetOntapVolumesAndSnapshotsForPool Failed: %w", err)
	}

	// Process snapshots to determine what needs to be created, updated, or deleted
	processSnapshotsResp, err := a.ProcessSnapshots(
		ctx,
		ontapVolSnapshotResp.OntapVolumeMap,
		ontapVolSnapshotResp.OntapSnapshots,
		dbVolSnapshotResp.DBVolumeMap,
		dbVolSnapshotResp.DBSnapshots,
		ontapVolSnapshotResp.SnapshotFetchSucceeded,
	)
	if err != nil {
		logger.Errorf("Failed to process snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("ProcessSnapshots Failed: %w", err)
	}

	// Confirm each delete candidate with ONTAP get-by-UUID before soft-deleting. A successful
	// but incomplete/lagging list must not remove rows that still exist on ONTAP.
	deleteCandidates := snapshotsObservedForDelete(dbVolSnapshotResp.DBSnapshots, processSnapshotsResp.DeleteIDs)
	if len(deleteCandidates) > 0 {
		provider, providerErr := GetOntapRestProviderForPool(ctx, a.SE, pool)
		if providerErr != nil || provider == nil {
			logger.Errorf("Failed to get ONTAP provider to confirm snapshot deletes for pool %s: %v", pool.Name, providerErr)
			return fmt.Errorf("confirm snapshot deletes: get ONTAP provider failed: %w", providerErr)
		}
		deleteCandidates = confirmSnapshotsGoneFromOntap(ctx, provider, deleteCandidates)
		// GetSnapshot is not context-aware; a cancel during confirm can leave a partial set.
		// Abort before applying deletes or continuing the pool sync.
		if err := ctx.Err(); err != nil {
			logger.Warnf("Context cancelled during snapshot delete confirmation for pool %s; skipping deletes and remaining sync: %v", pool.Name, err)
			return err
		}
	}

	// Sync deleted snapshots to database
	deletedSnapshots, err := a.SyncDeletedSnapshotsToDatabase(ctx, deleteCandidates)
	if err != nil {
		logger.Errorf("Failed to sync deleted snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("SyncDeletedSnapshotsToDatabase Failed: %w", err)
	}

	// Sync new snapshots to database
	createdSnapshots, err := a.SyncNewSnapshotsToDatabase(ctx, processSnapshotsResp.NewIDs, processSnapshotsResp.NewSSMap, dbVolSnapshotResp.DBVolumeMap, pool)
	if err != nil {
		logger.Errorf("Failed to sync new snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("SyncNewSnapshotsToDatabase Failed: %w", err)
	}

	// Sync updated snapshots to database
	_, err = a.SyncUpdatedSnapshotsToDatabase(ctx, processSnapshotsResp.UpdatedIDs, processSnapshotsResp.UpdatedSSMap, dbVolSnapshotResp.DBSnapshotMap)
	if err != nil {
		logger.Errorf("Failed to sync updated snapshots for pool %s: %v", pool.Name, err)
		return fmt.Errorf("SyncUpdatedSnapshotsToDatabase Failed: %w", err)
	}

	// Hydrate snapshots to CCFE
	err = a.HydrateSnapshotsToCCFE(ctx, createdSnapshots, deletedSnapshots)
	if err != nil {
		logger.Errorf("Failed to hydrate snapshots to CCFE for pool %s: %v", pool.Name, err)
		return fmt.Errorf("HydrateSnapshotsToCCFE Failed: %w", err)
	}
	// Hydrate volumes to CCFE
	if err = a.hydrateSplitRegularVolumesPendingCCFE(ctx, dbVolSnapshotResp.DBVolumeMap); err != nil {
		logger.Errorf("Failed split-regular volume CCFE hydration for pool %s: %v", pool.Name, err)
		return fmt.Errorf("hydrateSplitRegularVolumesPendingCCFE Failed: %w", err)
	}

	logger.Infof("Successfully completed snapshot synchronization for pool: %s", poolIdentifier.Name)
	return nil
}

package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpBatch "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/batch"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	commonutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// fetchBatchBackupsFromCVPFn is overridden in tests to avoid real CVP calls.
var fetchBatchBackupsFromCVPFn = fetchBatchBackupsFromCVP

// V1betaBatchListBackups returns the backups matching the provided UUID set for the given location.
// It fetches the VCP datastore and the CVP upstream in parallel, then merges and deduplicates on backup UUID
// (VCP data takes precedence). The client's ?fields= mask is applied on the returned rows; when a requested
// field is not known in either source, an empty placeholder is emitted so the wire JSON keeps the key.
func (h Handler) V1betaBatchListBackups(ctx context.Context, req *gcpgenserver.BatchBackupUUIDListV1beta, params gcpgenserver.V1betaBatchListBackupsParams) (gcpgenserver.V1betaBatchListBackupsRes, error) {
	if !backupEnabled {
		return &gcpgenserver.V1betaBatchListBackupsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "Backup feature is currently not enabled.",
		}, nil
	}

	httpReq := getHTTPRequestFromContext(ctx)
	if httpReq == nil {
		return &gcpgenserver.V1betaBatchListBackupsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}
	if batchAuthFn(httpReq) != nil {
		return &gcpgenserver.V1betaBatchListBackupsUnauthorized{
			Code:    http.StatusUnauthorized,
			Message: "Authentication failure",
		}, nil
	}

	if _, _, parsingErr := parseAndValidateRegionAndZone(params.LocationId); parsingErr != nil {
		return &gcpgenserver.V1betaBatchListBackupsBadRequest{
			Code:    parsingErr.Code,
			Message: parsingErr.Message,
		}, nil
	}

	if req == nil || len(req.BackupUuids) == 0 {
		return &gcpgenserver.V1betaBatchListBackupsBadRequest{
			Code:    http.StatusBadRequest,
			Message: "backupUuids is required and must have at least 1 item",
		}, nil
	}

	uuids := utils.DeduplicateSlice(req.BackupUuids)
	if len(uuids) > env.MaxBatchBackupUUIDs {
		return &gcpgenserver.V1betaBatchListBackupsBadRequest{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("backupUuids in body should have at most %d items", env.MaxBatchBackupUUIDs),
		}, nil
	}

	// Reject malformed UUIDs up front so we don't hit the VCP DB or CVP for
	// inputs that can never match a real backup. Validation runs against the
	// original (pre-dedup) list so the offending index in the message matches
	// what the caller sent.
	if msg := validateUUIDList(req.BackupUuids, "backupUuids"); msg != "" {
		return &gcpgenserver.V1betaBatchListBackupsBadRequest{
			Code:    http.StatusBadRequest,
			Message: msg,
		}, nil
	}

	fieldSet := batchBackupFieldsAsSet(params.Fields)

	if cvp.CVP_HOST == "" {
		return h.batchListBackupsVCPOnly(ctx, uuids, fieldSet)
	}
	return h.batchListBackupsVCPAndCVPParallel(ctx, uuids, params, fieldSet)
}

func (h Handler) batchListBackupsVCPOnly(ctx context.Context, backupUUIDs []string, fieldSet map[string]bool) (gcpgenserver.V1betaBatchListBackupsRes, error) {
	logger := util.GetLogger(ctx)

	backups, err := h.Orchestrator.GetBackupsByUUIDs(ctx, backupUUIDs)
	if err != nil {
		logger.Error("Failed to get backups by UUIDs from VCP", "error", err.Error())
		return &gcpgenserver.V1betaBatchListBackupsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "Internal server error while getting backups",
		}, nil
	}

	// Convert first, then reorder via mergeBatchBackupParallelLists so the VCP-only path
	// honors the same request-UUID ordering contract as the parallel path.
	vcpRows := make([]gcpgenserver.BatchBackupV1beta, 0, len(backups))
	for _, b := range backups {
		if b == nil {
			continue
		}
		vcpRows = append(vcpRows, convertBackupToBatchBackup(b, fieldSet))
	}
	ordered := mergeBatchBackupParallelLists(backupUUIDs, vcpRows, nil)
	return &gcpgenserver.V1betaBatchListBackupsOK{Backups: ordered}, nil
}

func (h Handler) batchListBackupsVCPAndCVPParallel(
	ctx context.Context,
	backupUUIDs []string,
	params gcpgenserver.V1betaBatchListBackupsParams,
	fieldSet map[string]bool,
) (gcpgenserver.V1betaBatchListBackupsRes, error) {
	logger := util.GetLogger(ctx)

	// BackupId is always preserved by applyBatchBackupFieldQuery, so the parallel fetches and the
	// merge step can reuse the caller's fieldSet as-is.
	var (
		vcpRows []gcpgenserver.BatchBackupV1beta
		vcpErr  error
		cvpRows []gcpgenserver.BatchBackupV1beta
		cvpErr  error
		wg      sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		backups, err := h.Orchestrator.GetBackupsByUUIDs(ctx, backupUUIDs)
		if err != nil {
			vcpErr = err
			return
		}
		vcpRows = make([]gcpgenserver.BatchBackupV1beta, 0, len(backups))
		for _, b := range backups {
			if b == nil {
				continue
			}
			vcpRows = append(vcpRows, convertBackupToBatchBackup(b, fieldSet))
		}
	}()
	go func() {
		defer wg.Done()
		cvpRows, cvpErr = fetchBatchBackupsFromCVPFn(ctx, backupUUIDs, params, fieldSet)
	}()
	wg.Wait()

	if vcpErr != nil && cvpErr != nil {
		logger.Error("Both VCP and CVP batch backup queries failed",
			"vcpError", vcpErr.Error(), "cvpError", cvpErr.Error())
		return &gcpgenserver.V1betaBatchListBackupsInternalServerError{
			Code:    http.StatusInternalServerError,
			Message: "An internal error occurred while getting backups from both CVP and VCP",
		}, nil
	}

	// Route partial-failure fallbacks through the same merge helper as the happy path so the
	// response preserves request-UUID ordering and always carries a non-nil Backups slice
	// (the encoder would otherwise omit the field for a nil slice).
	if vcpErr != nil {
		logger.Warn("VCP batch backup query failed, returning CVP results only", "error", vcpErr.Error())
		merged := mergeBatchBackupParallelLists(backupUUIDs, nil, cvpRows)
		return &gcpgenserver.V1betaBatchListBackupsOK{Backups: merged}, nil
	}
	if cvpErr != nil {
		logger.Warn("CVP batch backup query failed, returning VCP results only", "error", cvpErr.Error())
		merged := mergeBatchBackupParallelLists(backupUUIDs, vcpRows, nil)
		return &gcpgenserver.V1betaBatchListBackupsOK{Backups: merged}, nil
	}

	merged := mergeBatchBackupParallelLists(backupUUIDs, vcpRows, cvpRows)
	return &gcpgenserver.V1betaBatchListBackupsOK{Backups: merged}, nil
}

// mergeBatchBackupParallelLists combines VCP and CVP batch rows for the same UUID set.
// When a backup exists in both systems, VCP is authoritative (we drop the CVP duplicate) because
// the control plane owns create/update/delete for backups and has the freshest metadata. Output is
// ordered to match the requested UUID order so clients see stable indexing.
func mergeBatchBackupParallelLists(orderedUUIDs []string, vcpRows, cvpRows []gcpgenserver.BatchBackupV1beta) []gcpgenserver.BatchBackupV1beta {
	vcpByID := indexBatchBackupsByUUID(vcpRows)
	cvpByID := indexBatchBackupsByUUID(cvpRows)

	out := make([]gcpgenserver.BatchBackupV1beta, 0, len(orderedUUIDs))
	for _, id := range orderedUUIDs {
		if b, ok := vcpByID[id]; ok {
			out = append(out, b)
			continue
		}
		if b, ok := cvpByID[id]; ok {
			out = append(out, b)
		}
	}
	return out
}

func indexBatchBackupsByUUID(rows []gcpgenserver.BatchBackupV1beta) map[string]gcpgenserver.BatchBackupV1beta {
	m := make(map[string]gcpgenserver.BatchBackupV1beta, len(rows))
	for _, r := range rows {
		if !r.BackupId.Set || r.BackupId.Value == "" {
			continue
		}
		m[r.BackupId.Value] = r
	}
	return m
}

// fetchBatchBackupsFromCVP issues the CVP batch list backups request using the current context JWT
// and converts the payload into the proxy's response type. Only the field set that the CVP contract
// supports is forwarded; extras in the proxy-level set are applied after merge.
func fetchBatchBackupsFromCVP(
	ctx context.Context,
	backupUUIDs []string,
	params gcpgenserver.V1betaBatchListBackupsParams,
	fieldSet map[string]bool,
) ([]gcpgenserver.BatchBackupV1beta, error) {
	cvpClient := cvpClientFromContext(ctx)

	cvpParams := cvpBatch.NewV1betaBatchListBackupsParamsWithContext(ctx)
	cvpParams.SetBody(&cvpmodels.BackupUUIDListV1beta{
		BackupUUIDs: backupUUIDs,
	})
	cvpParams.SetLocationID(params.LocationId)
	if fields := batchListFieldStrings(params.Fields); len(fields) > 0 {
		cvpParams.SetFields(fields)
	}
	if params.XCorrelationID.IsSet() {
		v := params.XCorrelationID.Value
		cvpParams.SetXCorrelationID(&v)
	}

	resp, err := cvpClient.Batch.V1betaBatchListBackups(cvpParams)
	if err != nil {
		return nil, fmt.Errorf("CVP batch list backups failed: %w", err)
	}

	// Always return a non-nil slice so the ogen encoder emits "backups": []
	// (matching the VCP-only path) instead of omitting the field.
	var payload []*cvpmodels.BatchBackupV1beta
	if resp != nil && resp.Payload != nil {
		payload = resp.Payload.Backups
	}
	result := make([]gcpgenserver.BatchBackupV1beta, 0, len(payload))
	for _, p := range payload {
		if p == nil {
			continue
		}
		bp := convertCVPBatchBackupToGCPBatchBackup(p)
		applyBatchBackupFieldQuery(&bp, fieldSet)
		result = append(result, bp)
	}
	return result, nil
}

// batchBackupFieldsAsSet converts the typed field enum slice into a lookup set.
// Returns nil when the caller requested no fields, signalling "minimal projection" downstream.
func batchBackupFieldsAsSet(fields []gcpgenserver.V1betaBatchListBackupsFieldsItem) map[string]bool {
	if len(fields) == 0 {
		return nil
	}
	set := make(map[string]bool, len(fields))
	for _, f := range fields {
		set[string(f)] = true
	}
	return set
}

// convertBackupToBatchBackup transforms the VCP datastore model into the batch API wire type.
// When fieldSet is nil (no ?fields=), only backupId is populated; otherwise all known fields are
// populated and applyBatchBackupFieldQuery prunes/defaults them per the client mask.
func convertBackupToBatchBackup(backup *datamodel.Backup, fieldSet map[string]bool) gcpgenserver.BatchBackupV1beta {
	bp := gcpgenserver.BatchBackupV1beta{
		BackupId: gcpgenserver.NewOptString(backup.UUID),
	}
	if fieldSet == nil {
		applyBatchBackupFieldQuery(&bp, nil)
		return bp
	}

	if !backup.CreatedAt.IsZero() {
		bp.Created = gcpgenserver.NewOptNilDateTime(backup.CreatedAt)
	}
	bp.VolumeUsageBytes = gcpgenserver.NewOptNilInt64(backup.SizeInBytes)
	if backup.Type != "" {
		bp.BackupType = gcpgenserver.NewOptNilBatchBackupV1betaBackupType(
			gcpgenserver.BatchBackupV1betaBackupType(backup.Type),
		)
	}

	// Source volume / snapshot paths are derived the same way as the non-batch endpoint so clients
	// see consistent values whichever API they use.
	sourceVolumePath := utils.GetSourceVolumePathFromBackup(backup)
	bp.SourceVolume = gcpgenserver.NewOptNilString(sourceVolumePath)

	if backup.Attributes != nil && backup.Attributes.UseExistingSnapshot && backup.Attributes.SnapshotName != "" {
		bp.SourceSnapshot = gcpgenserver.NewOptNilString(utils.GetSourceSnapshotPathFromBackup(backup))
	}

	if backup.Description != "" {
		bp.Description = gcpgenserver.NewOptNilString(backup.Description)
	}

	bp.State = gcpgenserver.NewOptNilBatchBackupV1betaState(mapBackupStateToBatchState(backup.State))

	if backup.Name != "" {
		bp.ResourceId = gcpgenserver.NewOptNilString(backup.Name)
	}
	if backup.Attributes != nil && backup.Attributes.SnapshotID != "" {
		bp.SnapshotId = gcpgenserver.NewOptNilString(backup.Attributes.SnapshotID)
	}
	if backup.VolumeUUID != "" {
		bp.VolumeId = gcpgenserver.NewOptNilString(backup.VolumeUUID)
	}
	if backup.LatestLogicalBackupSize != 0 {
		bp.BackupChainBytes = gcpgenserver.NewOptNilInt64(backup.LatestLogicalBackupSize)
	}

	if backup.BackupVault != nil {
		bp.BackupVaultId = gcpgenserver.NewOptNilString(backup.BackupVault.UUID)

		if backup.BackupVault.SourceRegionName != nil && *backup.BackupVault.SourceRegionName != "" {
			bp.VolumeRegion = gcpgenserver.NewOptNilString(*backup.BackupVault.SourceRegionName)
		}
		// BackupRegion defaults to source region when dedicated backupRegion is not configured.
		switch {
		case backup.BackupVault.BackupRegionName != nil && *backup.BackupVault.BackupRegionName != "":
			bp.BackupRegion = gcpgenserver.NewOptNilString(*backup.BackupVault.BackupRegionName)
		case backup.BackupVault.SourceRegionName != nil && *backup.BackupVault.SourceRegionName != "":
			bp.BackupRegion = gcpgenserver.NewOptNilString(*backup.BackupVault.SourceRegionName)
		}

		var satisfiesPzi, satisfiesPzs bool
		bucketName := ""
		if backup.Attributes != nil {
			bucketName = backup.Attributes.BucketName
		}
		for _, bucket := range backup.BackupVault.BucketDetails {
			if bucket.BucketName == bucketName {
				satisfiesPzi = bucket.SatisfiesPzi
				satisfiesPzs = bucket.SatisfiesPzs
				break
			}
		}
		bp.SatisfiesPzi = gcpgenserver.NewOptNilBool(satisfiesPzi)
		bp.SatisfiesPzs = gcpgenserver.NewOptNilBool(satisfiesPzs)

		if backup.BackupVault.ImmutableAttributes != nil &&
			backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil &&
			*backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration > 0 &&
			commonutils.CheckIfBackupIsImmutable(backup) {
			expirationDate := backup.CreatedAt.AddDate(0, 0,
				int(*backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration))
			if !time.Now().After(expirationDate) {
				bp.EnforcedRetentionEndTime = gcpgenserver.NewOptNilDateTime(expirationDate)
			}
		}
	}

	if backup.AssetMetadata != nil {
		// Emit the object any time the datastore has asset metadata, even when ChildAssets is empty.
		// This matches the non-batch backup endpoint's behavior and avoids silently dropping the field
		// for backups that simply have no child assets recorded.
		var assets []gcpgenserver.ChildAssetV2
		for _, asset := range backup.AssetMetadata.ChildAssets {
			assets = append(assets, gcpgenserver.ChildAssetV2{
				AssetType:  gcpgenserver.NewOptString(asset.AssetType),
				AssetNames: asset.AssetNames,
			})
		}
		bp.AssetLocationMetadata = gcpgenserver.NewOptAssetLocationMetadataV2(
			gcpgenserver.AssetLocationMetadataV2{ChildAssets: assets},
		)
	}

	applyBatchBackupFieldQuery(&bp, fieldSet)
	return bp
}

// mapBackupStateToBatchState translates VCP lifecycle strings into the enum accepted by BatchBackupV1beta.
// The batch enum does not include UPDATING, so in-progress updates are reported as STATE_UNSPECIFIED to
// avoid clients receiving an unexpected enum value.
func mapBackupStateToBatchState(s string) gcpgenserver.BatchBackupV1betaState {
	switch s {
	case "":
		return gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED
	case datamodel.LifeCycleStateAvailable:
		return gcpgenserver.BatchBackupV1betaStateREADY
	case datamodel.LifeCycleStateUpdating:
		return gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED
	}
	v := gcpgenserver.BatchBackupV1betaState(s)
	switch v {
	case gcpgenserver.BatchBackupV1betaStateCREATING,
		gcpgenserver.BatchBackupV1betaStateREADY,
		gcpgenserver.BatchBackupV1betaStateUPLOADING,
		gcpgenserver.BatchBackupV1betaStateRESTORING,
		gcpgenserver.BatchBackupV1betaStateDISABLED,
		gcpgenserver.BatchBackupV1betaStateDELETING,
		gcpgenserver.BatchBackupV1betaStateDELETED,
		gcpgenserver.BatchBackupV1betaStateERROR,
		gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED:
		return v
	default:
		return gcpgenserver.BatchBackupV1betaStateSTATEUNSPECIFIED
	}
}

// convertCVPBatchBackupToGCPBatchBackup maps the generated CVP swagger model into the proxy's response
// type. Optional pointer fields from CVP translate to unset Opt fields here; the caller decides whether
// to keep, null, or default them based on the client's field mask.
func convertCVPBatchBackupToGCPBatchBackup(p *cvpmodels.BatchBackupV1beta) gcpgenserver.BatchBackupV1beta {
	bp := gcpgenserver.BatchBackupV1beta{}
	if p.BackupID != "" {
		bp.BackupId = gcpgenserver.NewOptString(p.BackupID)
	}
	if p.Created != nil {
		bp.Created = gcpgenserver.NewOptNilDateTime(time.Time(*p.Created))
	}
	if p.VolumeUsageBytes != nil {
		bp.VolumeUsageBytes = gcpgenserver.NewOptNilInt64(*p.VolumeUsageBytes)
	}
	if p.BackupType != nil && *p.BackupType != "" {
		bp.BackupType = gcpgenserver.NewOptNilBatchBackupV1betaBackupType(
			gcpgenserver.BatchBackupV1betaBackupType(*p.BackupType))
	}
	if p.SourceVolume != nil {
		bp.SourceVolume = gcpgenserver.NewOptNilString(*p.SourceVolume)
	}
	if p.BackupVaultID != nil {
		bp.BackupVaultId = gcpgenserver.NewOptNilString(*p.BackupVaultID)
	}
	if p.SourceSnapshot != nil {
		bp.SourceSnapshot = gcpgenserver.NewOptNilString(*p.SourceSnapshot)
	}
	if p.Description != nil {
		bp.Description = gcpgenserver.NewOptNilString(*p.Description)
	}
	if p.State != nil && *p.State != "" {
		bp.State = gcpgenserver.NewOptNilBatchBackupV1betaState(
			gcpgenserver.BatchBackupV1betaState(*p.State))
	}
	if p.ResourceID != nil {
		bp.ResourceId = gcpgenserver.NewOptNilString(*p.ResourceID)
	}
	if p.SnapshotID != nil {
		bp.SnapshotId = gcpgenserver.NewOptNilString(*p.SnapshotID)
	}
	if p.VolumeID != nil {
		bp.VolumeId = gcpgenserver.NewOptNilString(*p.VolumeID)
	}
	if p.BackupChainBytes != nil {
		bp.BackupChainBytes = gcpgenserver.NewOptNilInt64(*p.BackupChainBytes)
	}
	if p.SatisfiesPzs != nil {
		bp.SatisfiesPzs = gcpgenserver.NewOptNilBool(*p.SatisfiesPzs)
	}
	if p.SatisfiesPzi != nil {
		bp.SatisfiesPzi = gcpgenserver.NewOptNilBool(*p.SatisfiesPzi)
	}
	if p.VolumeRegion != nil {
		bp.VolumeRegion = gcpgenserver.NewOptNilString(*p.VolumeRegion)
	}
	if p.BackupRegion != nil {
		bp.BackupRegion = gcpgenserver.NewOptNilString(*p.BackupRegion)
	}
	if p.EnforcedRetentionEndTime != nil {
		bp.EnforcedRetentionEndTime = gcpgenserver.NewOptNilDateTime(time.Time(*p.EnforcedRetentionEndTime))
	}
	if p.AssetLocationMetadata != nil {
		// Translate the CVP child-asset slice into the proxy's type. CVP uses a slice of pointers, the
		// proxy uses a slice of values, so we copy each element.
		assets := make([]gcpgenserver.ChildAssetV2, 0, len(p.AssetLocationMetadata.ChildAssets))
		for _, ca := range p.AssetLocationMetadata.ChildAssets {
			if ca == nil {
				continue
			}
			assets = append(assets, gcpgenserver.ChildAssetV2{
				AssetType:  gcpgenserver.NewOptString(ca.AssetType),
				AssetNames: ca.AssetNames,
			})
		}
		bp.AssetLocationMetadata = gcpgenserver.NewOptAssetLocationMetadataV2(
			gcpgenserver.AssetLocationMetadataV2{ChildAssets: assets},
		)
	}
	return bp
}

// applyBatchBackupFieldQuery enforces the batch `fields` query in one pass.
//   - fieldSet nil: strip all optional fields so only backupId survives in JSON.
//   - fieldSet set: drop unrequested fields; for each requested field that has no value, emit JSON null
//     (via SetToNull) so the key is present but distinguishable from a real empty-string / zero value.
//
// BackupId is the primary key and is always preserved, regardless of the field mask, so clients can
// always match rows back to the input UUID list.
//
// assetLocationMetadata is a non-nullable object in the schema (Opt, not OptNil), so when requested
// without data we emit an empty object rather than JSON null to keep the response shape predictable.
func applyBatchBackupFieldQuery(bp *gcpgenserver.BatchBackupV1beta, fieldSet map[string]bool) {
	if fieldSet == nil {
		bp.Created.Reset()
		bp.VolumeUsageBytes.Reset()
		bp.BackupType.Reset()
		bp.SourceVolume.Reset()
		bp.BackupVaultId.Reset()
		bp.SourceSnapshot.Reset()
		bp.Description.Reset()
		bp.State.Reset()
		bp.ResourceId.Reset()
		bp.SnapshotId.Reset()
		bp.VolumeId.Reset()
		bp.BackupChainBytes.Reset()
		bp.SatisfiesPzs.Reset()
		bp.SatisfiesPzi.Reset()
		bp.VolumeRegion.Reset()
		bp.BackupRegion.Reset()
		bp.EnforcedRetentionEndTime.Reset()
		bp.AssetLocationMetadata.Reset()
		return
	}

	if fieldSet["created"] {
		if !bp.Created.Set {
			bp.Created.SetToNull()
		}
	} else {
		bp.Created.Reset()
	}
	if fieldSet["volumeUsageBytes"] {
		if !bp.VolumeUsageBytes.Set {
			bp.VolumeUsageBytes.SetToNull()
		}
	} else {
		bp.VolumeUsageBytes.Reset()
	}
	if fieldSet["backupType"] {
		if !bp.BackupType.Set {
			bp.BackupType.SetToNull()
		}
	} else {
		bp.BackupType.Reset()
	}
	if fieldSet["sourceVolume"] {
		if !bp.SourceVolume.Set {
			bp.SourceVolume.SetToNull()
		}
	} else {
		bp.SourceVolume.Reset()
	}
	if fieldSet["backupVaultId"] {
		if !bp.BackupVaultId.Set {
			bp.BackupVaultId.SetToNull()
		}
	} else {
		bp.BackupVaultId.Reset()
	}
	if fieldSet["sourceSnapshot"] {
		if !bp.SourceSnapshot.Set {
			bp.SourceSnapshot.SetToNull()
		}
	} else {
		bp.SourceSnapshot.Reset()
	}
	if fieldSet["description"] {
		if !bp.Description.Set {
			bp.Description.SetToNull()
		}
	} else {
		bp.Description.Reset()
	}
	if fieldSet["state"] {
		if !bp.State.Set {
			bp.State.SetToNull()
		}
	} else {
		bp.State.Reset()
	}
	if fieldSet["resourceId"] {
		if !bp.ResourceId.Set {
			bp.ResourceId.SetToNull()
		}
	} else {
		bp.ResourceId.Reset()
	}
	if fieldSet["snapshotId"] {
		if !bp.SnapshotId.Set {
			bp.SnapshotId.SetToNull()
		}
	} else {
		bp.SnapshotId.Reset()
	}
	if fieldSet["volumeId"] {
		if !bp.VolumeId.Set {
			bp.VolumeId.SetToNull()
		}
	} else {
		bp.VolumeId.Reset()
	}
	if fieldSet["backupChainBytes"] {
		if !bp.BackupChainBytes.Set {
			bp.BackupChainBytes.SetToNull()
		}
	} else {
		bp.BackupChainBytes.Reset()
	}
	if fieldSet["satisfiesPzs"] {
		if !bp.SatisfiesPzs.Set {
			bp.SatisfiesPzs.SetToNull()
		}
	} else {
		bp.SatisfiesPzs.Reset()
	}
	if fieldSet["satisfiesPzi"] {
		if !bp.SatisfiesPzi.Set {
			bp.SatisfiesPzi.SetToNull()
		}
	} else {
		bp.SatisfiesPzi.Reset()
	}
	if fieldSet["volumeRegion"] {
		if !bp.VolumeRegion.Set {
			bp.VolumeRegion.SetToNull()
		}
	} else {
		bp.VolumeRegion.Reset()
	}
	if fieldSet["backupRegion"] {
		if !bp.BackupRegion.Set {
			bp.BackupRegion.SetToNull()
		}
	} else {
		bp.BackupRegion.Reset()
	}
	if fieldSet["enforcedRetentionEndTime"] {
		if !bp.EnforcedRetentionEndTime.Set {
			bp.EnforcedRetentionEndTime.SetToNull()
		}
	} else {
		bp.EnforcedRetentionEndTime.Reset()
	}
	if fieldSet["assetLocationMetadata"] {
		// assetLocationMetadata is declared as a non-nullable object in the schema, so when a client
		// asks for it but the source has no metadata we emit an empty object rather than omitting
		// the key, keeping the response shape predictable.
		if !bp.AssetLocationMetadata.Set {
			bp.AssetLocationMetadata = gcpgenserver.NewOptAssetLocationMetadataV2(
				gcpgenserver.AssetLocationMetadataV2{},
			)
		}
	} else {
		bp.AssetLocationMetadata.Reset()
	}
}

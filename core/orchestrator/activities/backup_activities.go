package activities

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

const (
	BackupComment                      = "VCP-Backup"
	SmStatusTransferring               = "transferring"
	SmStatusSuccess                    = "success"
	SmStatusFailed                     = "failed"
	BackupRestoreCountIncrement        = "increment"
	BackupRestoreCountDecrement        = "decrement"
	expertModePoolAPIAccessMode        = "ONTAP"
	retryableSnapmirrorCreateErrorType = "RetryableSnapmirrorCreateError"
)

var (
	hydrationEnabled         = env.GetBool("GCP_HYDRATE_ENABLED", true)
	backupObjectStoreIpspace = env.GetString("OBJECT_STORE_IPSPACE", "cbs")
)

var getGcbdrBackupChainBackpropEnabled = func() bool {
	return env.GetBool("GCBDR_BACKUP_CHAIN_BACKPROP_ENABLED", false)
}

type dedicatedBackupHmacSecretPayload struct {
	AccessKeyID string `json:"access_key_id"`
	Secret      string `json:"secret"`
}

var (
	getGCPServiceForBackup = func(ctx context.Context) (hyperscaler.Services, error) {
		return hyperscaler.GetGCPService(ctx)
	}
)

// EventHistorySafetyThreshold is the threshold for triggering ContinueAsNew
// this is basically number of workflow event logs, every workflow has max 50100 events. so we made 45000 as a threshold for backup. and when we reach this limit, we create a new workflow and continue the execution from there.
const (
	EventHistorySafetyThreshold = 45000
)

type BackupActivity struct {
	SE database.Storage
}

type BackupWorkflowInput struct {
	Backup                 *datamodel.Backup
	BackupVault            *datamodel.BackupVault
	Volume                 *datamodel.Volume
	BackupVaultAccountName string
}

type ScheduledBackupParams struct {
	Backups       []*datamodel.Backup
	BackupPolicy  *datamodel.BackupPolicy
	OntapSnapshot *vsa.SnapshotProviderResponse
	Job           *datamodel.Job
}
type BackupActivitiesContext struct {
	// Initial inputs
	BackupWorkflowInit *BackupWorkflowInput
	// for Scheduled backup workflow
	ScheduledBackupParams *ScheduledBackupParams
	CorrelationID         string

	// Workflow state
	Node                   *models.Node
	ObjStoreName           string
	BucketDetails          *datamodel.BucketDetails
	BucketName             string
	ObjStore               *commonparams.CloudTarget
	SmSourcePath           string
	SmDestinationPath      string
	SnapmirrorRelationship *commonparams.SnapmirrorRelationship
	SnapshotName           string
	SnapshotResponse       *vsa.SnapshotProviderResponse
	TransferStatus         string
	DbSnapshot             *datamodel.Snapshot
	ObjStoreSnapshot       *vsa.SmObjectStoreEndpointSnapshot
	IsExpertMode           bool
	TemporaryRestoreHmac   *commonparams.TemporaryRestoreHmacHandle
}

// PollTransferStatusInput represents the input for the polling activity
type PollTransferStatusInput struct {
	BackupActivitiesContext *BackupActivitiesContext
	Node                    *models.Node
	SnapmirrorRelationship  *commonparams.SnapmirrorRelationship
	SnapshotName            string
	EventHistoryCount       int
	NextWaitTime            time.Duration
}

// SnapmirrorTransferStatus represents the status and progress of a snapmirror transfer
type SnapmirrorTransferStatus struct {
	Status           string // Transfer status: "transferring", "success", "failed"
	BytesTransferred *int64 // Bytes transferred (nil if not available)
}

// PollTransferStatusOutput represents the output of the polling activity
type PollTransferStatusOutput struct {
	BackupActivitiesContext *BackupActivitiesContext
	TransferComplete        bool
	ShouldContinueAsNew     bool
	ContinueAsNewReason     string
	NextWaitTime            time.Duration
	TransferStatus          *SnapmirrorTransferStatus // Includes status and bytes transferred
}

func (a BackupActivity) CreateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Creating backup %s in database", backup.UUID))
	return se.CreateBackup(ctx, backup)
}

// snapmirrorOntapRelationshipToCommon maps ONTAP SnapmirrorRelationship to common params (shared by GetSnapmirror and IsSnapmirrorDeleted).
func snapmirrorOntapRelationshipToCommon(snapmirror *ontapRest.SnapmirrorRelationship) *commonparams.SnapmirrorRelationship {
	if snapmirror == nil {
		return nil
	}
	resp := commonparams.SnapmirrorRelationship{UUID: snapmirror.UUID.String()}
	if snapmirror.Destination != nil && snapmirror.Destination.UUID != nil {
		resp.DestinationUUID = nillable.ToPointer(snapmirror.Destination.UUID.String())
	}
	if snapmirror.State != nil {
		resp.State = nillable.ToPointer(*snapmirror.State)
	}
	if snapmirror.Healthy != nil {
		resp.Healthy = nillable.ToPointer(*snapmirror.Healthy)
	}
	if len(snapmirror.SnapmirrorRelationshipInlineUnhealthyReason) > 0 {
		unhealthyReasons := make([]string, 0, len(snapmirror.SnapmirrorRelationshipInlineUnhealthyReason))
		for _, errObj := range snapmirror.SnapmirrorRelationshipInlineUnhealthyReason {
			if errObj != nil && errObj.Message != nil {
				unhealthyReasons = append(unhealthyReasons, *errObj.Message)
			}
		}
		if len(unhealthyReasons) > 0 {
			resp.UnhealthyReason = &unhealthyReasons
		}
	}
	resp.TotalTransferBytes = snapmirror.TotalTransferBytes
	return &resp
}

func (a BackupActivity) IsSnapmirrorDeleted(ctx context.Context, node *models.Node, params *commonparams.SnapmirrorRelationshipParams) (*commonparams.SnapmirrorDeletePrecheckResult, error) {
	recordHeartbeat(ctx, "is snapmirror-deleted check started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	snapmirror, err := provider.SnapmirrorRelationshipGet(params.DestinationPath, params.SourcePath)
	if err != nil {
		// Convert any error containing "not found" to a proper NotFoundErr
		err = errors.ConvertToNotFoundErrIfContainsMessage(err, "not found", "snapmirror relationship", nil)
		if errors.IsNotFoundErr(err) {
			return &commonparams.SnapmirrorDeletePrecheckResult{RelationshipMissing: true}, nil
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "is snapmirror-deleted check completed")
	return &commonparams.SnapmirrorDeletePrecheckResult{
		RelationshipMissing: false,
		Relationship:        snapmirrorOntapRelationshipToCommon(snapmirror),
	}, nil
}

func (a BackupActivity) GetBackup(ctx context.Context, backupVaultUUID, backupUUID, accountName string) (*datamodel.Backup, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup %s from database", backupUUID))
	return se.GetBackup(ctx, backupVaultUUID, backupUUID, accountName)
}

func (a BackupActivity) DeleteBackup(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Deleting backup %s from database", backupUUID))
	return se.DeleteBackup(ctx, backupUUID)
}

func (a BackupActivity) FinishBackup(ctx context.Context, backup *datamodel.Backup) error {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Finishing backup %s in database", backup.UUID))
	_, err := se.FinishBackup(ctx, backup)
	return err
}

func (a BackupActivity) UpdateBackupError(ctx context.Context, backup *datamodel.Backup, errorString string) error {
	if backup == nil || errorString == "" {
		return errors.New("invalid input")
	}
	se := a.SE
	backup.State = datamodel.LifeCycleStateError
	backup.StateDetails = errorString
	recordHeartbeat(ctx, fmt.Sprintf("Updating error state for backup %s in database", backup.UUID))
	_, err := se.UpdateBackupState(ctx, backup)
	return err
}

func (a BackupActivity) MarkBackupAvailable(ctx context.Context, backup *datamodel.Backup) error {
	if backup == nil {
		return errors.New("backup cannot be nil")
	}
	se := a.SE
	backup.State = datamodel.LifeCycleStateAvailable
	backup.StateDetails = datamodel.LifeCycleStateAvailableDetails
	recordHeartbeat(ctx, fmt.Sprintf("Marking backup %s available in database", backup.UUID))
	_, err := se.UpdateBackupState(ctx, backup)
	return err
}

// applyBackupAttributes runs fn on backup attributes when a backup record is present.
// Scheduled backup workflows may omit BackupWorkflowInit.Backup during node/object-store setup.
func applyBackupAttributes(backup *datamodel.Backup, fn func(attrs *datamodel.BackupAttributes)) {
	if backup == nil {
		return
	}
	if backup.Attributes == nil {
		backup.Attributes = &datamodel.BackupAttributes{}
	}
	fn(backup.Attributes)
}

// PrepareObjectStoreActivity prepares object store details
func (b *BackupActivity) PrepareObjectStoreActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	recordHeartbeat(ctx, "PrepareObjectStoreActivity started")
	objStoreName, err := getObjStoreName(backupActivitiesContext.BackupWorkflowInit.BackupVault, backupActivitiesContext.BackupWorkflowInit.Volume)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.ObjStoreName = objStoreName

	bucketDetails, err := getBucketDetails(backupActivitiesContext.BackupWorkflowInit.BackupVault, backupActivitiesContext.BackupWorkflowInit.Volume)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.BucketDetails = bucketDetails
	backupActivitiesContext.BucketName = bucketDetails.BucketName
	recordHeartbeat(ctx, "PrepareObjectStoreActivity completed")
	return backupActivitiesContext, nil
}

// GetOrCreateObjectStoreActivity gets or creates object store
func (b *BackupActivity) GetOrCreateObjectStoreActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	recordHeartbeat(ctx, "Getting or creating ONTAP object store")
	objStore, err := b.getOrCreateObjectStoreForBackup(ctx, backupActivitiesContext.Node, backupActivitiesContext.ObjStoreName, backupActivitiesContext.BucketName, backupActivitiesContext.BucketDetails, backupActivitiesContext.BackupWorkflowInit.Volume, nil)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.ObjStore = objStore
	applyBackupAttributes(backupActivitiesContext.BackupWorkflowInit.Backup, func(attrs *datamodel.BackupAttributes) {
		attrs.BucketName = backupActivitiesContext.BucketName
		attrs.ServiceAccountName = backupActivitiesContext.BucketDetails.ServiceAccountName
		attrs.ObjectStoreUUID = objStore.UUID
	})
	return backupActivitiesContext, nil
}

// PrepareSnapmirrorActivity prepares snapmirror paths
func (b *BackupActivity) PrepareSnapmirrorActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	smDestinationPath, err := getSmDestinationPath(backupActivitiesContext.BackupWorkflowInit.BackupVault, backupActivitiesContext.BackupWorkflowInit.Volume)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.SmDestinationPath = smDestinationPath
	backupActivitiesContext.SmSourcePath = getSmSourcePath(backupActivitiesContext.BackupWorkflowInit.Volume)

	return backupActivitiesContext, nil
}

// CreateSnapmirrorRelationshipActivity creates snapmirror relationship.
// Always lets ONTAP create a new destination endpoint; the returned DestinationUUID is persisted in the backup record.
func (b *BackupActivity) CreateSnapmirrorRelationshipActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	recordHeartbeat(ctx, "Creating ONTAP SnapMirror relationship")
	snapmirrorParams := &commonparams.SnapmirrorRelationshipParams{
		SourcePath:      backupActivitiesContext.SmSourcePath,
		DestinationPath: backupActivitiesContext.SmDestinationPath,
		SourceUUID:      nil,
		IsRestore:       false,
	}

	snapmirrorRelationship, err := b.SnapmirrorGetOrCreate(ctx, backupActivitiesContext.Node, snapmirrorParams)
	if err != nil {
		return nil, classifyCreateSnapmirrorRelationshipError(err)
	}

	backupActivitiesContext.SnapmirrorRelationship = snapmirrorRelationship
	if snapmirrorRelationship != nil && !nillable.IsNilOrEmpty(snapmirrorRelationship.DestinationUUID) {
		applyBackupAttributes(backupActivitiesContext.BackupWorkflowInit.Backup, func(attrs *datamodel.BackupAttributes) {
			attrs.EndpointUUID = *snapmirrorRelationship.DestinationUUID
		})
	} else {
		return backupActivitiesContext, vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("DestinationUUID not found in snapmirror relationship"))
	}

	return backupActivitiesContext, nil
}

func classifyCreateSnapmirrorRelationshipError(err error) error {
	if !isTransientSnapmirrorCreateError(err) {
		return err
	}

	var appErr *temporal.ApplicationError
	if vsaerrors.As(err, &appErr) && !appErr.NonRetryable() {
		return err
	}

	// Force known transient SnapMirror create failures to be retryable at this activity
	// boundary so the backup workflow activity retry policy can recover automatically.
	// Use WithCause so the original error is preserved as the cause chain rather than
	// being (mis)encoded as a serialized detail payload.
	return temporal.NewApplicationErrorWithCause(err.Error(), retryableSnapmirrorCreateErrorType, err)
}

// isTransientSnapmirrorCreateError reports whether a CreateSnapmirrorRelationship failure is a
// known-transient ONTAP condition that should be retried rather than treated as terminal.
func isTransientSnapmirrorCreateError(err error) bool {
	return isVLDBOfflineSnapmirrorCreateError(err) || isRelationshipModificationInProgressError(err)
}

func isVLDBOfflineSnapmirrorCreateError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	retryableIndicators := []string{
		"6620237",
		"2621556",
		"volume location database is offline",
		"snapmirror relationship database write failed",
	}

	for _, indicator := range retryableIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// isRelationshipModificationInProgressError detects ONTAP error 6619715, where a SnapMirror
// relationship modification is already in progress on the destination. ONTAP explicitly asks the
// caller to "wait a few minutes and try again", so this is transient and must not be surfaced as a
// NonRetryableErr (see VSCP-5862). The async ONTAP job failure is wrapped as NonRetryableErr in
// PollOntapJobActivity, which bypasses the transport-level retry allowlist; reclassifying it here
// lets the backup workflow retry the create with backoff.
func isRelationshipModificationInProgressError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	retryableIndicators := []string{
		"6619715",
		"modification of relationship",
		"is in progress. wait a few minutes",
	}

	for _, indicator := range retryableIndicators {
		if strings.Contains(errStr, indicator) {
			return true
		}
	}

	return false
}

// CreatingSnapshotActivity creates snapshot in database
func (b *BackupActivity) CreatingSnapshotActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	if !backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.UseExistingSnapshot {
		backupActivitiesContext.SnapshotName = backupActivitiesContext.BackupWorkflowInit.Backup.Name
	} else {
		// If UseExistingSnapshot is true, we use the existing snapshot name from the backup attributes
		if backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName == "" {
			return nil, errors.New("snapshot name is empty in backup attributes")
		}
		backupActivitiesContext.SnapshotName = backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName
	}
	snapshot := &datamodel.Snapshot{
		Name:               backupActivitiesContext.SnapshotName,
		Description:        BackupComment,
		VolumeID:           backupActivitiesContext.BackupWorkflowInit.Volume.ID,
		AccountID:          backupActivitiesContext.BackupWorkflowInit.Volume.AccountID,
		Volume:             backupActivitiesContext.BackupWorkflowInit.Volume,
		Account:            backupActivitiesContext.BackupWorkflowInit.Volume.Account,
		IsAppConsistent:    false,
		Type:               SnapshotTypeBackup,
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}
	var dbSnapshot *datamodel.Snapshot
	var err error
	// If UseExistingSnapshot is true, we do not create a new snapshot in the database
	if backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.UseExistingSnapshot && !backupActivitiesContext.IsExpertMode {
		recordHeartbeat(ctx, fmt.Sprintf("Fetching snapshot %s from database", snapshot.Name))
		dbSnapshot, err = b.SE.GetSnapshotByNameAndVolumeId(ctx, snapshot.Name, snapshot.AccountID, snapshot.VolumeID)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				return backupActivitiesContext, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.NewNotFoundErr("snapshot", &snapshot.Name))
			}
			logger.Errorf("Failed to get snapshot from database. Error: %v", err)
			return backupActivitiesContext, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
	} else {
		if !backupActivitiesContext.IsExpertMode {
			recordHeartbeat(ctx, fmt.Sprintf("Creating snapshot %s in database", snapshot.Name))
			dbSnapshot, err = b.SE.CreatingSnapshot(ctx, snapshot)
			if err != nil {
				logger.Errorf("Failed to create snapshot in database. Error: %v", err)
				return backupActivitiesContext, err
			}
		}
	}
	backupActivitiesContext.DbSnapshot = dbSnapshot
	return backupActivitiesContext, nil
}

// UpdateSnapshotActivity updates snapshot in database
func (b *BackupActivity) UpdateSnapshotActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	if backupActivitiesContext.DbSnapshot == nil && !backupActivitiesContext.IsExpertMode {
		return nil, errors.New("database snapshot is nil")
	}
	if !backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.UseExistingSnapshot {
		// Update the snapshot in the database
		if backupActivitiesContext.SnapshotResponse != nil {
			backupActivitiesContext.DbSnapshot.State = datamodel.LifeCycleStateREADY
			backupActivitiesContext.DbSnapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails
			backupActivitiesContext.DbSnapshot.SnapshotAttributes.SizeInBytes = backupActivitiesContext.SnapshotResponse.SizeInBytes
			backupActivitiesContext.DbSnapshot.SnapshotAttributes.ExternalUUID = backupActivitiesContext.SnapshotResponse.ExternalUUID
			backupActivitiesContext.DbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes = backupActivitiesContext.SnapshotResponse.LogicalSizeInBytes
		} else {
			now := time.Now()
			backupActivitiesContext.DbSnapshot.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
			backupActivitiesContext.DbSnapshot.State = datamodel.LifeCycleStateError
			backupActivitiesContext.DbSnapshot.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
		}
		recordHeartbeat(ctx, fmt.Sprintf("Updating snapshot %s in database", backupActivitiesContext.DbSnapshot.UUID))
		_, err := b.SE.UpdateSnapshot(ctx, backupActivitiesContext.DbSnapshot)
		if err != nil {
			logger.Errorf("Failed to update snapshot details in database. Error: %v", err)
			return nil, err
		}
	}
	return backupActivitiesContext, nil
}

// CreateSnapshotActivity creates snapshot in Ontap
func (b *BackupActivity) CreateSnapshotActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	var snapshotResponse *vsa.SnapshotProviderResponse
	var err error
	if backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.UseExistingSnapshot {
		// If UseExistingSnapshot is true, we do not create a new snapshot in Ontap
		if backupActivitiesContext.SnapshotName == "" {
			return nil, errors.New("snapshot name is empty in backup attributes")
		}
		logger.Infof("Using existing snapshot: %s", backupActivitiesContext.SnapshotName)
		var snapshotExternalUUID string
		if backupActivitiesContext.IsExpertMode {
			snapshotExternalUUID = backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotID
		} else {
			recordHeartbeat(ctx, fmt.Sprintf("Fetching snapshot %s from database", backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName))
			dbSnapshot, err := b.SE.GetSnapshotByNameAndVolumeId(ctx, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName, backupActivitiesContext.BackupWorkflowInit.Volume.AccountID, backupActivitiesContext.BackupWorkflowInit.Volume.ID)
			if err != nil {
				logger.Errorf("Failed to get snapshot from database. Error: %v", err)
				return nil, err
			}
			snapshotExternalUUID = dbSnapshot.SnapshotAttributes.ExternalUUID
		}

		snapshotResponse, err = b.SnapshotGet(ctx, backupActivitiesContext.Node, snapshotExternalUUID, backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID)
		if err != nil {
			logger.Errorf("Failed to get snapshot from Ontap. Error: %v", err)
			return nil, err
		}
	} else {
		snapshotResponse, err = b.SnapshotCreate(ctx, backupActivitiesContext.Node, backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID, backupActivitiesContext.SnapshotName, BackupComment)
		if err != nil {
			logger.Errorf("Failed to create snapshot in Ontap. Error: %v", err)
			return nil, err
		}
	}

	// Update the backupActivitiesContext with snapshot response
	backupActivitiesContext.SnapshotResponse = snapshotResponse
	if backupActivitiesContext.BackupWorkflowInit != nil && backupActivitiesContext.BackupWorkflowInit.Backup != nil && backupActivitiesContext.BackupWorkflowInit.Backup.Attributes != nil {
		backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName = backupActivitiesContext.SnapshotName
		if backupActivitiesContext.SnapshotResponse != nil {
			backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotID = backupActivitiesContext.SnapshotResponse.ExternalUUID
		}
	}
	return backupActivitiesContext, nil
}

// TransferSnapshotActivity initiates snapshot transfer
func (b *BackupActivity) TransferSnapshotActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	err := b.SnapmirrorTransfer(ctx, backupActivitiesContext.Node, backupActivitiesContext.SnapmirrorRelationship.UUID, backupActivitiesContext.SnapshotName)
	if err != nil {
		return nil, err
	}
	return backupActivitiesContext, nil
}

// CheckTransferStatusActivity checks transfer status
func (b *BackupActivity) CheckTransferStatusActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	transferStatus, err := b.GetSnapmirrorTransferStatus(ctx, backupActivitiesContext.Node, backupActivitiesContext.SnapmirrorRelationship.UUID, backupActivitiesContext.SnapshotName)
	if err != nil {
		return nil, err
	}
	if transferStatus.Status == SmStatusSuccess {
		backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotCreationTime = time.Now().String()
	}
	backupActivitiesContext.TransferStatus = transferStatus.Status
	return backupActivitiesContext, nil
}

func (a BackupActivity) GetSnapshotFromObjectStore(ctx context.Context, node *models.Node, objectStoreUUID, EndpointUUID, snapshotUUID string) (*vsa.SmObjectStoreEndpointSnapshot, error) {
	recordHeartbeat(ctx, "GetSnapshotFromObjectStore started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	smObjectStoreEndpointSnapshot, err := provider.SnapmirrorObjectStoreSnapshotGet(objectStoreUUID, EndpointUUID, snapshotUUID)
	recordHeartbeat(ctx, "GetSnapshotFromObjectStore completed")
	return smObjectStoreEndpointSnapshot, err
}

func (a BackupActivity) GetObjectStoreEndpointInfo(ctx context.Context, node *models.Node, objectStoreUUID, EndpointUUID string) (*vsa.SmObjectStoreEndpointt, error) {
	recordHeartbeat(ctx, "GetObjectStoreEndpointInfo started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	smObjectStoreEndpointt, err := provider.ObjectStoreEndpointInfoGet(objectStoreUUID, EndpointUUID)
	recordHeartbeat(ctx, "GetObjectStoreEndpointInfo completed")
	return smObjectStoreEndpointt, err
}

// GetObjectStoreEndpointActivity gets object store endpoint info
func (b *BackupActivity) GetObjectStoreEndpointActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	objStoreEndpointInfo, err := b.GetObjectStoreEndpointInfo(ctx, backupActivitiesContext.Node, backupActivitiesContext.ObjStore.UUID, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.EndpointUUID)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.BackupWorkflowInit.Backup.LatestLogicalBackupSize = *objStoreEndpointInfo.LogicalSize
	return backupActivitiesContext, nil
}

// GetObjectStoreSnapshotActivity gets snapshot from object store
func (b *BackupActivity) GetObjectStoreSnapshotActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	objStoreSnapshot, err := b.GetSnapshotFromObjectStore(ctx, backupActivitiesContext.Node, backupActivitiesContext.ObjStore.UUID, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.EndpointUUID, backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotID)
	if err != nil {
		return nil, err
	}
	backupActivitiesContext.ObjStoreSnapshot = objStoreSnapshot
	if objStoreSnapshot.LogicalSize != nil {
		backupActivitiesContext.BackupWorkflowInit.Backup.SizeInBytes = *objStoreSnapshot.LogicalSize
	} else {
		backupActivitiesContext.BackupWorkflowInit.Backup.SizeInBytes = 0
	}
	return backupActivitiesContext, nil
}

// UpdateBackupSizeActivity updates backup size fields in both backup and volume tables
func (b *BackupActivity) UpdateBackupSizeActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	backup := backupActivitiesContext.BackupWorkflowInit.Backup
	volumeUUID := backup.VolumeUUID

	recordHeartbeat(ctx, fmt.Sprintf("Updating size for backup %s in database", backup.UUID))
	_, err := b.SE.FinishBackup(ctx, backup)
	if err != nil {
		logger.Errorf("Failed to update backup %s with size information: %v", backup.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Set LatestLogicalBackupSize to 0 for all previous backups of the same volume in a single query
	// This ensures that only the latest backup has the correct size.
	// Skip for CrossProject (GCBDR) vaults
	isCrossProjectVault := backupActivitiesContext.BackupWorkflowInit.BackupVault != nil && backupActivitiesContext.BackupWorkflowInit.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject
	if !isCrossProjectVault && backupActivitiesContext.BackupWorkflowInit.Backup.LatestLogicalBackupSize != 0 {
		recordHeartbeat(ctx, fmt.Sprintf("Resetting previous backup sizes for volume %s", volumeUUID))
		err = b.SE.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volumeUUID, backup.UUID)
		if err != nil {
			logger.Errorf("Failed to reset LatestLogicalBackupSize for previous backups of volume %s: %v", volumeUUID, err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	if backupActivitiesContext.IsExpertMode {
		updates := make(map[string]interface{})
		backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection.BackupChainBytes = &backupActivitiesContext.BackupWorkflowInit.Backup.LatestLogicalBackupSize
		updates["data_protection"] = backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection
		recordHeartbeat(ctx, fmt.Sprintf("Updating expert mode volume %s backup size", volumeUUID))
		err = b.SE.UpdateExpertModeVolumeFields(ctx, volumeUUID, updates)
		if err != nil {
			logger.Errorf("Failed to update expert mode volume %s with latest logical backup size: %v", volumeUUID, err)
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	} else {
		if err = b.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volumeUUID); err != nil {
			return nil, err
		}
		if backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection != nil {
			recordHeartbeat(ctx, fmt.Sprintf("Reading backup chain size for volume %s", volumeUUID))
			sum, sumErr := b.SE.SumVolumeBackupChainBytes(ctx, volumeUUID)
			if sumErr == nil {
				backupActivitiesContext.BackupWorkflowInit.Volume.DataProtection.BackupChainBytes = &sum
			}
		}
	}

	logger.Debugf("Successfully updated backup size fields for backup %s and volume %s", backup.UUID, volumeUUID)
	return backupActivitiesContext, nil
}

// RecalculateAndUpdateVolumeBackupChainBytesActivity sets volumes.data_protection.backup_chain_bytes to the
// sum of latest_logical_backup_size across latest AVAILABLE backups per (backup_vault_id, endpoint_uuid).
// Regular volumes only; expert mode volumes are not updated by this activity.
func (a *BackupActivity) RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx context.Context, volumeUUID string) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Fetching volume %s for backup chain recalculation", volumeUUID))
	vol, err := a.SE.GetVolume(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get volume %s for backup chain bytes recalc: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Summing backup chain bytes for volume %s", volumeUUID))
	sum, err := a.SE.SumVolumeBackupChainBytes(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to sum backup chain bytes for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if vol.DataProtection == nil {
		vol.DataProtection = &datamodel.DataProtection{}
	}
	vol.DataProtection.BackupChainBytes = &sum
	volumeUpdates := map[string]interface{}{
		"data_protection": vol.DataProtection,
	}
	recordHeartbeat(ctx, fmt.Sprintf("Updating backup chain bytes for volume %s", volumeUUID))
	if err = a.SE.UpdateVolumeFields(ctx, volumeUUID, volumeUpdates); err != nil {
		logger.Errorf("Failed to update volume %s backup chain bytes: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Recalculated backup_chain_bytes=%d for volume %s", sum, volumeUUID)
	return nil
}

func (a *BackupActivity) BackPropagateGcbdrChainSizesForDeletedBackupActivity(ctx context.Context, volumeUUID, endpointUUID string, pivotBackupID, latestBackupID, delta int64) error {
	logger := util.GetLogger(ctx)

	if !getGcbdrBackupChainBackpropEnabled() {
		return nil
	}
	if pivotBackupID <= 0 || delta <= 0 {
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Back-propagating backup chain sizes for volume %s", volumeUUID))
	updated, err := a.SE.DecrementIntermediateBackupChainSizes(ctx, volumeUUID, endpointUUID, pivotBackupID, latestBackupID, delta)
	if err != nil {
		logger.Errorf("Failed to back-propagate chain size (ADC) for volume %s endpoint %s: %v", volumeUUID, endpointUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("GCBDR back-propagation (ADC): reduced %d intermediate backup(s) by %d for volume %s endpoint %s (pivot backup id %d, latest backup id %d)",
		updated, delta, volumeUUID, endpointUUID, pivotBackupID, latestBackupID)
	return nil
}

func (a *BackupActivity) UpdateVolumeLatestLogicalBackupSize(ctx context.Context, volume *datamodel.Volume, logicalSize int64) error {
	logger := util.GetLogger(ctx)
	isExpertModeVolume, err := a.IsExpertModeVolume(ctx, volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		return err
	}
	if isExpertModeVolume {
		volumeUpdates := make(map[string]interface{})
		volume.DataProtection.BackupChainBytes = &logicalSize
		volumeUpdates["data_protection"] = volume.DataProtection
		recordHeartbeat(ctx, fmt.Sprintf("Updating expert mode volume %s logical backup size", volume.UUID))
		err := a.SE.UpdateExpertModeVolumeFields(ctx, volume.VolumeAttributes.ExternalUUID, volumeUpdates)
		if err != nil {
			logger.Errorf("Failed to update volume %s with latest logical backup size: %v", volume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Infof("Successfully updated logical size %d for volume %s", logicalSize, volume.Name)
		return nil
	} else {
		return a.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volume.UUID)
	}
}

// SetGlobalLatestBackupLogicalSizeActivity sets the latest backup (across vaults) for the volume to the given size.
// Used after delete when vault switching is on so the remaining "latest" backup shows the summed chain bytes.
// If no backup exists (e.g. last backup deleted), returns nil without error.
func (a *BackupActivity) SetGlobalLatestBackupLogicalSizeActivity(ctx context.Context, volumeUUID string, size int64) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching latest backup for volume %s", volumeUUID))
	latest, err := a.SE.GetLatestBackupByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil // no backup left
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if latest == nil {
		return nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Updating latest backup %s logical size", latest.UUID))
	err = a.SE.UpdateBackupFields(ctx, latest.UUID, map[string]interface{}{"latest_logical_backup_size": size})
	if err != nil {
		logger.Errorf("Failed to set latest backup chain bytes for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return a.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volumeUUID)
}

// UpdateConstituentCountForBackup updates constituent count for large volume backups
func (b *BackupActivity) UpdateConstituentCountForBackup(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	if backupActivitiesContext.BackupWorkflowInit.Volume.LargeVolumeAttributes == nil || !backupActivitiesContext.BackupWorkflowInit.Volume.LargeVolumeAttributes.LargeCapacity {
		// No need to update constituent count for non-large volumes
		return backupActivitiesContext, nil
	}

	logger := util.GetLogger(ctx)
	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	if backupActivitiesContext.ScheduledBackupParams != nil && len(backupActivitiesContext.ScheduledBackupParams.Backups) > 0 {
		for _, bkp := range backupActivitiesContext.ScheduledBackupParams.Backups {
			recordHeartbeat(ctx, fmt.Sprintf("Updating constituent count for backup %s", bkp.UUID))
			_, err := b.SE.UpdateBackupConstituentCountFromVolume(ctx, bkp, volume)
			if err != nil {
				logger.Errorf("Failed to update Constituent count for a backup in scheduled backups")
				return nil, vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
	} else {
		backup := backupActivitiesContext.BackupWorkflowInit.Backup

		recordHeartbeat(ctx, fmt.Sprintf("Updating constituent count for backup %s", backup.UUID))
		_, err := b.SE.UpdateBackupConstituentCountFromVolume(ctx, backup, volume)
		if err != nil {
			logger.Errorf("Failed to update Constituent count for a backup")
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}
	return backupActivitiesContext, nil
}

// FinishBackupActivity finishes the backup
func (b *BackupActivity) FinishBackupActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	err := b.FinishBackup(ctx, backupActivitiesContext.BackupWorkflowInit.Backup)
	if err != nil {
		return nil, err
	}
	return backupActivitiesContext, nil
}

func (a BackupActivity) GetOrCreateObjectStore(ctx context.Context, node *models.Node, name, containerName string) (*commonparams.CloudTarget, error) {
	return a.getOrCreateObjectStoreForBackup(ctx, node, name, containerName, nil, nil, nil)
}

func (a BackupActivity) getOrCreateObjectStoreForBackup(ctx context.Context, node *models.Node, name, containerName string, bucketDetails *datamodel.BucketDetails, volume *datamodel.Volume, temporary *commonparams.TemporaryRestoreHmacHandle) (*commonparams.CloudTarget, error) {
	recordHeartbeat(ctx, "GetOrCreateObjectStore started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Handle both return values from CloudTargetGet
	objectStore, err := provider.CloudTargetGet(&name)
	// Check if the error is nil, which means the object store already exists
	if err == nil {
		// If no error, return the existing object store
		return &commonparams.CloudTarget{Name: *objectStore.Name, UUID: *objectStore.UUID}, nil
	}

	// Dedicated-capacity pools must use HMAC key auth; never fall back to default (non-key) auth.
	// Cross-pool restores may supply short-lived (temporary) HMAC credentials when the backup
	// vault has no persisted HmacSecretID. They also need an explicit IPspace, because ONTAP
	// verifies object store reachability over intercluster LIFs that these clusters keep outside
	// the "Default" IPspace. Other pool types keep ONTAP's default behaviour.
	var accessKey, secretKey, ipspace string
	if volume != nil && volume.Pool != nil && volume.Pool.IsDedicatedCapacity() {
		ipspace = backupObjectStoreIpspace
		var hmacErr error
		if temporary != nil && (bucketDetails == nil || bucketDetails.HmacSecretID == "") {
			accessKey, secretKey, hmacErr = decodeTemporaryRestoreHmacCredentials(temporary)
		} else {
			accessKey, secretKey, hmacErr = getBackupHmacCredentialsFromSM(ctx, bucketDetails)
		}
		if hmacErr != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(hmacErr)
		}
	}
	objectStore, err = provider.CloudTargetCreate(name, containerName, accessKey, secretKey, ipspace)
	if err == nil {
		recordHeartbeat(ctx, "GetOrCreateObjectStore completed")
		// If no error, return the existing object store
		return &commonparams.CloudTarget{Name: *objectStore.Name, UUID: *objectStore.UUID}, nil
	}

	return nil, errors.New("failed to get or create object store")
}

// getBackupHmacCredentialsFromSM loads backup HMAC credentials for dedicated-capacity pools from Secret Manager.
func getBackupHmacCredentialsFromSM(ctx context.Context, bucketDetails *datamodel.BucketDetails) (accessKeyID, secret string, err error) {
	if bucketDetails == nil || bucketDetails.HmacSecretID == "" {
		return "", "", fmt.Errorf("bucket details missing HmacSecretID")
	}
	gcpService, err := getGCPServiceForBackup(ctx)
	if err != nil {
		return "", "", err
	}
	customSecret, err := gcpService.GetSecretWithLatestVersion(env.SecretManagerProjectID, bucketDetails.HmacSecretID)
	if err != nil {
		return "", "", fmt.Errorf("failed to read HMAC secret %s: %w", bucketDetails.HmacSecretID, err)
	}
	if customSecret == nil || customSecret.SecretVersion == nil {
		return "", "", fmt.Errorf("HMAC secret %s returned empty payload", bucketDetails.HmacSecretID)
	}

	var payload dedicatedBackupHmacSecretPayload
	if err = json.Unmarshal([]byte(customSecret.SecretVersion.Value), &payload); err != nil {
		return "", "", fmt.Errorf("failed to parse HMAC secret payload: %w", err)
	}
	if payload.AccessKeyID == "" || payload.Secret == "" {
		return "", "", fmt.Errorf("HMAC secret payload incomplete for %s", bucketDetails.HmacSecretID)
	}
	return payload.AccessKeyID, payload.Secret, nil
}

const (
	temporaryRestoreServiceAccountPrefix = "vsa-bv-rst-"
	// TemporaryRestoreServiceAccountDisplay is the GCP display name for short-lived restore service accounts.
	TemporaryRestoreServiceAccountDisplay = "VSA temporary backup restore reader"
	maxGCPServiceAccountIDLength          = 30
	// TemporaryRestoreIAMPropagationWait is the workflow sleep after role attachment before HMAC creation.
	TemporaryRestoreIAMPropagationWait = 60 * time.Second
)

// TemporaryRestoreServiceAccountRoles are granted to temporary restore service accounts in the backup tenant.
var TemporaryRestoreServiceAccountRoles = []string{
	"roles/storage.hmacKeyAdmin",
	"roles/storage.objectAdmin",
}

// RequiresTemporaryRestoreHmac reports whether restore must provision short-lived credentials in the backup tenant.
func RequiresTemporaryRestoreHmac(pool *datamodel.Pool, bucketDetails *datamodel.BucketDetails) bool {
	return pool.IsDedicatedCapacity() &&
		bucketDetails != nil &&
		bucketDetails.HmacSecretID == "" &&
		bucketDetails.TenantProjectNumber != "" &&
		bucketDetails.BucketName != ""
}

// PopulateBucketDetailsForTemporaryRestore fills missing vault SA from in-memory backup metadata.
func PopulateBucketDetailsForTemporaryRestore(
	bucketDetails *datamodel.BucketDetails,
	backup *datamodel.Backup,
	backupVault *datamodel.BackupVault,
) {
	if bucketDetails == nil {
		return
	}
	if backup != nil && backup.Attributes != nil && bucketDetails.ServiceAccountName == "" {
		bucketDetails.ServiceAccountName = backup.Attributes.ServiceAccountName
	}
	if backup != nil && backup.BackupVault != nil {
		mergeBucketServiceAccountFromVault(bucketDetails, backup.BackupVault)
	}
	if backupVault != nil {
		mergeBucketServiceAccountFromVault(bucketDetails, backupVault)
	}
}

func mergeBucketServiceAccountFromVault(bucketDetails *datamodel.BucketDetails, vault *datamodel.BackupVault) {
	if bucketDetails == nil || vault == nil {
		return
	}
	for _, bd := range vault.BucketDetails {
		if bd == nil || !strings.EqualFold(bd.BucketName, bucketDetails.BucketName) {
			continue
		}
		if bucketDetails.ServiceAccountName == "" && bd.ServiceAccountName != "" {
			bucketDetails.ServiceAccountName = bd.ServiceAccountName
		}
		if bucketDetails.TenantProjectNumber == "" && bd.TenantProjectNumber != "" {
			bucketDetails.TenantProjectNumber = bd.TenantProjectNumber
		}
		return
	}
}

func temporaryRestoreServiceAccountID(ctx context.Context, bucketDetails *datamodel.BucketDetails) string {
	seed := bucketDetails.BucketName + "|" + bucketDetails.TenantProjectNumber
	if correlationID := utils.GetCoRelationIDFromContext(ctx); correlationID != "" {
		seed = correlationID + "|" + seed
	}
	hash := sha256.Sum256([]byte(seed))
	suffix := hex.EncodeToString(hash[:3])
	accountID := temporaryRestoreServiceAccountPrefix + suffix
	if len(accountID) > maxGCPServiceAccountIDLength {
		accountID = accountID[:maxGCPServiceAccountIDLength]
	}
	return accountID
}

// TemporaryRestoreServiceAccountID resolves the deterministic short-lived restore service account ID.
func (a *BackupActivity) TemporaryRestoreServiceAccountID(ctx context.Context, bucketDetails *datamodel.BucketDetails) (string, error) {
	recordHeartbeat(ctx, "TemporaryRestoreServiceAccountID started")
	if bucketDetails == nil || bucketDetails.TenantProjectNumber == "" || bucketDetails.BucketName == "" {
		return "", vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("backup tenant project and bucket name required for temporary restore service account"))
	}
	accountID := temporaryRestoreServiceAccountID(ctx, bucketDetails)
	recordHeartbeat(ctx, "TemporaryRestoreServiceAccountID completed")
	return accountID, nil
}

func decodeTemporaryRestoreHmacCredentials(handle *commonparams.TemporaryRestoreHmacHandle) (accessKey, secretKey string, err error) {
	if handle == nil {
		return "", "", fmt.Errorf("temporary restore HMAC handle is nil")
	}
	if handle.AccessKeyID != "" {
		accessKey = handle.AccessKeyID
	} else if handle.EncodedAccessKey != "" {
		accessKeyBytes, decodeErr := base64.StdEncoding.DecodeString(handle.EncodedAccessKey)
		if decodeErr != nil {
			return "", "", fmt.Errorf("failed to decode temporary access key: %w", decodeErr)
		}
		accessKey = string(accessKeyBytes)
	}
	secretKeyBytes, err := base64.StdEncoding.DecodeString(handle.EncodedSecretKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode temporary secret key: %w", err)
	}
	secretKey = string(secretKeyBytes)
	if accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("temporary restore HMAC credentials incomplete")
	}
	return accessKey, secretKey, nil
}

// DeleteTemporaryRestoreHmacKey deletes short-lived restore HMAC keys created for cross-pool HW restore.
func (a *BackupActivity) DeleteTemporaryRestoreHmacKey(ctx context.Context, handle *commonparams.TemporaryRestoreHmacHandle) error {
	if handle == nil {
		return nil
	}
	recordHeartbeat(ctx, "DeleteTemporaryRestoreHmacKey started")
	gcpService, err := getGCPServiceForBackup(ctx)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	accessKeyID := handle.AccessKeyID
	if accessKeyID == "" && handle.EncodedAccessKey != "" {
		accessKeyBytes, decodeErr := base64.StdEncoding.DecodeString(handle.EncodedAccessKey)
		if decodeErr != nil {
			return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to decode temporary access key: %w", decodeErr))
		}
		accessKeyID = string(accessKeyBytes)
	}
	if accessKeyID != "" && handle.ServiceAccount != "" {
		if err = gcpService.DeleteHmacKey(handle.TenantProjectNumber, accessKeyID, handle.ServiceAccount); err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to delete temporary restore HMAC key: %w", err))
		}
	}
	recordHeartbeat(ctx, "DeleteTemporaryRestoreHmacKey completed")
	return nil
}

// ValidateDedicatedCapacityRestoreBucketDetails ensures dedicated-capacity restores have HMAC credentials available.
func ValidateDedicatedCapacityRestoreBucketDetails(pool *datamodel.Pool, bucketDetails *datamodel.BucketDetails, temporary *commonparams.TemporaryRestoreHmacHandle) error {
	if pool == nil || !pool.IsDedicatedCapacity() {
		return nil
	}
	if bucketDetails == nil {
		return errors.NewUserInputValidationErr("dedicated-capacity restore requires backup vault bucket details")
	}
	if bucketDetails.HmacSecretID != "" || temporary != nil || RequiresTemporaryRestoreHmac(pool, bucketDetails) {
		return nil
	}
	if bucketDetails.HmacSecretID == "" && bucketDetails.ServiceAccountName == "" {
		return errors.NewUserInputValidationErr("backup vault service account required for cross-pool HW restore")
	}
	return errors.NewUserInputValidationErr(
		"dedicated-capacity restore requires hmac_secret_id on backup vault bucket details; attach backup vault before restore")
}

// GetOrCreateObjectStoreForRestore creates or reuses an ONTAP object store for restore operations.
// Dedicated-capacity pools use HMAC credentials from Secret Manager or temporary restore keys.
func (a BackupActivity) GetOrCreateObjectStoreForRestore(
	ctx context.Context,
	node *models.Node,
	name, containerName string,
	bucketDetails *datamodel.BucketDetails,
	pool *datamodel.Pool,
	temporary *commonparams.TemporaryRestoreHmacHandle,
) (*commonparams.CloudTarget, error) {
	if bucketDetails != nil && bucketDetails.HmacSecretID != "" {
		temporary = nil
	}
	if err := ValidateDedicatedCapacityRestoreBucketDetails(pool, bucketDetails, temporary); err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	volume := &datamodel.Volume{Pool: pool}
	return a.getOrCreateObjectStoreForBackup(ctx, node, name, containerName, bucketDetails, volume, temporary)
}

func (a BackupActivity) SnapmirrorGetOrCreate(ctx context.Context, node *models.Node, params *commonparams.SnapmirrorRelationshipParams) (*commonparams.SnapmirrorRelationship, error) {
	recordHeartbeat(ctx, "SnapmirrorGetOrCreate started")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("Getting SnapMirror relationship from ONTAP for destination %s", params.DestinationPath))
	snapmirror, err := provider.SnapmirrorRelationshipGet(params.DestinationPath, params.SourcePath)
	if err != nil {
		// Log the error but continue to create a new snapmirror relationship
		logger.Info(err.Error())
	}
	if snapmirror != nil {
		resp := commonparams.SnapmirrorRelationship{UUID: snapmirror.UUID.String()}
		if snapmirror.Destination != nil && snapmirror.Destination.UUID != nil {
			resp.DestinationUUID = nillable.ToPointer(snapmirror.Destination.UUID.String())
		}
		recordHeartbeat(ctx, fmt.Sprintf("Found existing SnapMirror relationship %s in ONTAP", resp.UUID))
		return &resp, nil
	}
	// Remove this once we start using cache to store token
	recordHeartbeat(ctx, "Fetching SMC license for SnapMirror relationship")
	smcLicense, err := GetSmcLicenseFromCloud(ctx)
	if err != nil {
		logger.Errorf("Failed to get SMC license from cloud: %v", err)
		return nil, errors.New("failed to get SMC license from cloud")
	}
	recordHeartbeat(ctx, "Generating node token for SnapMirror relationship")
	token, err := GenerateTokenForNode(ctx, node, &smcLicense)
	if err != nil {
		logger.Errorf("Failed to generate SMC token for node %s: %v", node.Name, err)
		return nil, errors.New("failed to generate SMC token for node")
	}
	if token == nil || *token == "" {
		logger.Error("SMC token is empty or nil")
		return nil, errors.New("SMC token is empty or nil")
	}
	recordHeartbeat(ctx, "Creating SnapMirror relationship in ONTAP")
	snapmirror, err = provider.SnapmirrorRelationshipCreate(params, token)
	if snapmirror != nil {
		resp := commonparams.SnapmirrorRelationship{UUID: snapmirror.UUID.String()}
		if snapmirror.Destination == nil || snapmirror.Destination.UUID == nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("DestinationUUID not found in snapmirror relationship")))
		}
		resp.DestinationUUID = nillable.ToPointer(snapmirror.Destination.UUID.String())
		recordHeartbeat(ctx, fmt.Sprintf("Finished creating SnapMirror relationship %s in ONTAP", resp.UUID))
		return &resp, nil
	}
	recordHeartbeat(ctx, "SnapMirror relationship creation in ONTAP failed")
	return nil, err
}

func (a BackupActivity) GetObjectStore(ctx context.Context, node *models.Node, name string) (*commonparams.CloudTarget, error) {
	recordHeartbeat(ctx, "GetObjectStore started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Handle both return values from CloudTargetGet
	objectStore, err := provider.CloudTargetGet(&name)
	if err != nil {
		// If there is an error, it means the object store does not exist
		return nil, errors.New("object store does not exist")
	}
	recordHeartbeat(ctx, "GetObjectStore completed")
	return &commonparams.CloudTarget{Name: *objectStore.Name, UUID: *objectStore.UUID}, nil
}

func (a BackupActivity) GetSnapmirror(ctx context.Context, node *models.Node, sourcePath, destinationPath string) (*commonparams.SnapmirrorRelationship, error) {
	recordHeartbeat(ctx, "GetSnapmirror started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	snapmirror, err := provider.SnapmirrorRelationshipGet(destinationPath, sourcePath)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err))
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, "GetSnapmirror completed")
	return snapmirrorOntapRelationshipToCommon(snapmirror), nil
}

func (a BackupActivity) SnapshotCreate(ctx context.Context, node *models.Node, volumeUUID, name, comment string) (*vsa.SnapshotProviderResponse, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Creating ONTAP snapshot %s", name))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider.CreateSnapshot(vsa.CreateSnapshotParams{
		VolumeUUID: volumeUUID,
		Name:       name,
		Comment:    comment,
	})
}

func (a BackupActivity) SnapmirrorTransfer(ctx context.Context, node *models.Node, snapmirrorUUID, snapshotName string) error {
	recordHeartbeat(ctx, "SnapmirrorTransfer started")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Remove this once we start using cache to store token
	recordHeartbeat(ctx, "Fetching SMC license for SnapMirror transfer")
	smcLicense, err := GetSmcLicenseFromCloud(ctx)
	if err != nil {
		logger.Errorf("Failed to get SMC license from cloud: %v", err)
		return errors.New("failed to get SMC license from cloud")
	}
	recordHeartbeat(ctx, "Generating node token for SnapMirror transfer")
	token, err := GenerateTokenForNode(ctx, node, &smcLicense)
	if err != nil {
		logger.Errorf("Failed to generate SMC token for node %s: %v", node.Name, err)
		return errors.New("failed to generate SMC token for node")
	}
	if token == nil || *token == "" {
		logger.Error("SMC token is empty or nil")
		return errors.New("SMC token is empty or nil")
	}
	recordHeartbeat(ctx, fmt.Sprintf("Starting SnapMirror transfer for snapshot %s", snapshotName))
	err = provider.SnapmirrorRelationshipTransferCreate(snapmirrorUUID, snapshotName, token)
	recordHeartbeat(ctx, "SnapmirrorTransfer completed")
	return err
}

func (a BackupActivity) GetSnapmirrorTransferStatus(ctx context.Context, node *models.Node, snapmirrorUUID, snapshotName string) (*SnapmirrorTransferStatus, error) {
	recordHeartbeat(ctx, "GetSnapmirrorTransferStatus started")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return &SnapmirrorTransferStatus{Status: SmStatusFailed}, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	rsp, err := provider.SnapmirrorRelationshipTransferGet(snapmirrorUUID, snapshotName)
	if err != nil {
		logger.Errorf("Snapmirror transfer failed with error: %v", err)
		status := SmStatusFailed
		if rsp != nil && rsp.State != nil {
			logger.Errorf("Snapmirror transfer failed with status: %s", *rsp.State)
			status = *rsp.State
		}
		return &SnapmirrorTransferStatus{Status: status}, err
	}
	if rsp == nil {
		logger.Infof("snapmirror transfer response is nil for uuid: %s and snapshot: %s", snapmirrorUUID, snapshotName)
		return &SnapmirrorTransferStatus{Status: SmStatusSuccess, BytesTransferred: nil}, nil
	}

	result := &SnapmirrorTransferStatus{
		BytesTransferred: rsp.BytesTransferred,
	}

	if rsp.State != nil {
		if *rsp.State == SmStatusFailed {
			result.Status = SmStatusFailed
			recordHeartbeat(ctx, "SnapmirrorTransferStatus failed")
			return result, errors.New("Snapmirror transfer failed with status: " + SmStatusFailed)
		}
		if *rsp.State == SmStatusSuccess {
			result.Status = SmStatusSuccess
			recordHeartbeat(ctx, "SnapmirrorTransferStatus completed")
			return result, nil
		}
		if *rsp.State == SmStatusTransferring {
			result.Status = SmStatusTransferring
			recordHeartbeat(ctx, "SnapmirrorTransferStatus transferring")
			return result, nil
		}
		result.Status = *rsp.State
	} else {
		result.Status = SmStatusFailed
	}
	recordHeartbeat(ctx, "SnapmirrorTransferStatus completed")
	return result, errors.New("Snapmirror transfer failed with status: " + result.Status)
}

func (a BackupActivity) DeleteBackupSnapshot(ctx context.Context, node *models.Node, snapshotUUID, volumeUUID string) error {
	if snapshotUUID == "" || volumeUUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(fmt.Errorf("invalid input: snapshotUUID and volumeUUID cannot be empty"))
	}
	recordHeartbeat(ctx, fmt.Sprintf("Deleting ONTAP snapshot %s", snapshotUUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider.DeleteSnapshot(snapshotUUID, volumeUUID)
}

func (a BackupActivity) SnapshotGet(ctx context.Context, node *models.Node, snapshotUUID, volumeUUID string) (*vsa.SnapshotProviderResponse, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP snapshot %s", snapshotUUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return provider.GetSnapshot(snapshotUUID, volumeUUID)
}

func (a BackupActivity) IsVolumeDeleted(ctx context.Context, volumeUUID string) (bool, error) {
	se := a.SE
	// Try regular volume first
	recordHeartbeat(ctx, fmt.Sprintf("Checking volume %s in database", volumeUUID))
	_, err := se.GetVolume(ctx, volumeUUID)
	if err == nil {
		return false, nil // Found in regular table
	}
	if !errors.IsNotFoundErr(err) {
		return false, err // Unexpected error
	}

	// Not found in regular table, try expert mode volumes
	recordHeartbeat(ctx, fmt.Sprintf("Checking expert mode volume %s in database", volumeUUID))
	_, err = se.GetExpertModeVolumeByExternalUUID(ctx, volumeUUID)
	if err == nil {
		return false, nil // Found in expert mode table
	}
	if errors.IsNotFoundErr(err) || vsaerrors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil // Not found in either table - deleted
	}
	return false, err // Unexpected error
}

func (a BackupActivity) GetVolume(ctx context.Context, volumeUUID string) (*datamodel.Volume, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching volume %s from database", volumeUUID))
	volume, err := se.GetVolume(ctx, volumeUUID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			// Volume not found in regular table, try expert mode volumes
			recordHeartbeat(ctx, fmt.Sprintf("Fetching expert mode volume %s from database", volumeUUID))
			expertModeVol, err := se.GetExpertModeVolumeByExternalUUID(ctx, volumeUUID)
			if err != nil {
				return nil, err
			}
			volume := ConvertExpertModeVolumeToVolume(expertModeVol)
			recordHeartbeat(ctx, fmt.Sprintf("Fetching pool %d for expert mode volume", expertModeVol.PoolID))
			pool, err := se.GetPoolByID(ctx, expertModeVol.PoolID)
			if err != nil {
				return nil, err
			}
			volume.VolumeAttributes.VendorSubnetID = pool.Network
			return volume, nil
		}
		return nil, err
	}
	return volume, nil
}

func ConvertExpertModeVolumeToVolume(expertModeVol *datamodel.ExpertModeVolumes) *datamodel.Volume {
	return &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID:      expertModeVol.UUID,
			ID:        expertModeVol.ID,
			CreatedAt: expertModeVol.CreatedAt,
			UpdatedAt: expertModeVol.UpdatedAt,
		},
		Name:           expertModeVol.Name,
		Description:    expertModeVol.Description,
		State:          expertModeVol.State,
		SizeInBytes:    expertModeVol.SizeInBytes,
		AccountID:      expertModeVol.AccountID,
		PoolID:         expertModeVol.PoolID,
		SvmID:          expertModeVol.SvmID,
		Account:        expertModeVol.Account,
		Pool:           expertModeVol.Pool,
		Svm:            expertModeVol.Svm,
		DataProtection: expertModeVol.BackupConfig,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: expertModeVol.ExternalUUID,
		},
	}
}

func (a BackupActivity) GetAccountByName(ctx context.Context, accountName string) (*datamodel.Account, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching account %s from database", accountName))
	account, err := se.GetAccount(ctx, accountName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return account, nil
}

func (a BackupActivity) GetBackupVault(ctx context.Context, backupVaultUUID string) (*datamodel.BackupVault, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup vault %s from database", backupVaultUUID))
	return se.GetBackupVault(ctx, backupVaultUUID)
}

func (a BackupActivity) GetBackupCountByVolumeUUID(ctx context.Context, volumeUUID string) (int64, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Counting backups for volume %s", volumeUUID))
	return se.BackupCountByVolumeID(ctx, volumeUUID)
}

func (a BackupActivity) GetBackupCountByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (int64, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Counting backups for volume %s in vault %d", volumeUUID, backupVaultID))
	return se.GetBackupCountByVolumeAndVault(ctx, volumeUUID, backupVaultID)
}

func (a BackupActivity) GetBackupCountByVolumeVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Counting backups for volume %s in vault %d and endpoint", volumeUUID, backupVaultID))
	return se.GetBackupCountByVolumeVaultAndEndpoint(ctx, volumeUUID, backupVaultID, endpointUUID)
}

// GetLatestBackupByVolumeAndVault returns the latest available backup for the volume in the given vault, or nil if none.
func (a BackupActivity) GetLatestBackupByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (*datamodel.Backup, error) {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Fetching latest backup for volume %s in vault %d", volumeUUID, backupVaultID))
	backup, err := se.GetLatestBackupByVolumeAndVault(ctx, volumeUUID, backupVaultID)
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return backup, nil
}

// DeleteSnapshotFromObjectStore Enhanced DeleteSnapshotFromObjectStore with idempotency
func (a BackupActivity) DeleteSnapshotFromObjectStore(ctx context.Context, node *models.Node, objectStoreUUID, EndpointUUID, snapshotUUID string) (*vsa.OntapAsyncResponse, error) {
	recordHeartbeat(ctx, "delete snapshot from object store started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	response, err := provider.SnapmirrorObjectStoreSnapshotDelete(objectStoreUUID, EndpointUUID, snapshotUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "delete snapshot from object store completed")
	return response, nil
}

// Enhanced DeleteSnapmirror with idempotency
func (a BackupActivity) DeleteSnapmirror(ctx context.Context, node *models.Node, snapmirrorUUID string) (*vsa.OntapAsyncResponse, error) {
	recordHeartbeat(ctx, "delete snapmirror started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	response, err := provider.SnapmirrorRelationshipDelete(snapmirrorUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "delete snapmirror finished")
	return response, nil
}

// backupNeedsSnapmirror reports whether a backup in this state can still need its endpoint's
// SnapMirror relationship. DELETING still counts, since a delete that fails leaves the backup in place.
func backupNeedsSnapmirror(state string) bool {
	switch state {
	case datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeleted:
		return false
	default:
		return true
	}
}

// DeleteSnapmirrorIfUnused deletes the relationship only when no backup outside excludeBackupUUIDs
// still needs it. Backups on endpointUUID share the relationship; those on other endpoints do not.
func (a BackupActivity) DeleteSnapmirrorIfUnused(ctx context.Context, node *models.Node, volumeUUID, endpointUUID string, excludeBackupUUIDs []string, snapmirrorUUID string) (*vsa.OntapAsyncResponse, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Checking backups of volume %s before deleting snapmirror %s", volumeUUID, snapmirrorUUID))
	backups, err := a.SE.GetBackupsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	excluded := make(map[string]struct{}, len(excludeBackupUUIDs))
	for _, backupUUID := range excludeBackupUUIDs {
		excluded[backupUUID] = struct{}{}
	}
	for _, backup := range backups {
		if _, ok := excluded[backup.UUID]; ok {
			continue
		}
		if !backupNeedsSnapmirror(backup.State) {
			continue
		}
		if !commonparams.BackupEndpointMatches(endpointUUID, commonparams.BackupEndpointUUID(backup)) {
			continue
		}
		logger.Infof("Skipping snapmirror %s delete for volume %s: backup %s is in state %s", snapmirrorUUID, volumeUUID, backup.UUID, backup.State)
		return nil, nil
	}

	return a.DeleteSnapmirror(ctx, node, snapmirrorUUID)
}

// DeleteCloudEndpoint Enhanced DeleteCloudEndpoint with idempotency
func (a BackupActivity) DeleteCloudEndpoint(ctx context.Context, node *models.Node, objectStoreUUID string, EndpointUUID string) (*vsa.OntapAsyncResponse, error) {
	recordHeartbeat(ctx, "delete cloud endpoint started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	response, err := provider.SnapmirrorObjectStoreEndpointDelete(objectStoreUUID, EndpointUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "delete cloud endpoint finished")
	return response, nil
}

// Enhanced DeleteSnapshotForBackup with idempotency
func (a BackupActivity) DeleteSnapshotForBackup(ctx context.Context, node *models.Node, snapshotUUID, volumeUUID string, useExistingSnapshot bool) error {
	recordHeartbeat(ctx, "delete snapshot started")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger := util.GetLogger(ctx)
	if useExistingSnapshot {
		// If using an existing snapshot, do not delete it
		logger.Warnf("Skipping deletion of snapshot with external uuid %s", snapshotUUID)
		return nil
	}

	logger.Infof("Deleting snapshot with external uuid %s", snapshotUUID)

	err = provider.DeleteSnapshot(snapshotUUID, volumeUUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, "delete snapshot finished")
	return nil
}

func (a BackupActivity) DeleteBackupSnapshotFromDB(ctx context.Context, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	if backup.Attributes == nil {
		// If attributes are nil, do nothing
		return nil
	}

	if backup.Attributes.UseExistingSnapshot {
		// If use_existing_snapshot is True, do nothing
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching volume %s for backup snapshot deletion", backup.VolumeUUID))
	volume, err := se.GetVolume(ctx, backup.VolumeUUID)
	if err != nil {
		logger.Errorf("Failed to get volume for backup %s: %v", backup.UUID, err)
		return fmt.Errorf("failed to get volume for backup %s: %v", backup.UUID, err)
	}

	if volume == nil {
		logger.Errorf("Volume for backup %s is nil", backup.UUID)
		return fmt.Errorf("volume not found for backup UUID: %s", backup.UUID)
	}

	// Get the snapshot by name and volume ID
	recordHeartbeat(ctx, fmt.Sprintf("Fetching snapshot %s from database", backup.Attributes.SnapshotName))
	snapshot, err := se.GetSnapshotByNameAndVolumeId(ctx, backup.Attributes.SnapshotName, volume.AccountID, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get snapshot by name %s for volume %s: %v", backup.Attributes.SnapshotName, backup.VolumeUUID, err)
		return err
	}

	// Check if the snapshot is already marked as deleted
	if snapshot.DeletedAt != nil && snapshot.DeletedAt.Valid {
		logger.Infof("Snapshot %s is already marked as deleted, skipping deletion for backup %s", snapshot.UUID, backup.UUID)
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Deleting snapshot %s from database", snapshot.UUID))
	_, err = se.DeleteSnapshot(ctx, snapshot.UUID)
	if err != nil {
		logger.Errorf("Failed to delete snapshot %s from database: %v", snapshot.UUID, err)
		originalErr := err
		markErr := a.markSnapshotAsError(ctx, snapshot, fmt.Sprintf("Failed to delete from database: %v", err))
		if markErr != nil {
			logger.Errorf("Failed to mark snapshot %s as error: %v", snapshot.Name, markErr)
		}
		return originalErr
	}

	logger.Infof("Successfully deleted snapshot %s from database for backup %s", snapshot.UUID, backup.UUID)
	return nil
}

func (j *BackupActivity) IsBackupShared(ctx context.Context, backup *datamodel.Backup) (bool, error) {
	se := j.SE
	recordHeartbeat(ctx, fmt.Sprintf("Checking whether backup %s is shared", backup.UUID))
	return se.IsBackupShared(ctx, backup)
}

func (a *BackupActivity) UpdateBackup(ctx context.Context, backup *datamodel.Backup) error {
	se := a.SE
	recordHeartbeat(ctx, fmt.Sprintf("Updating backup %s in database", backup.UUID))
	_, err := se.UpdateBackup(ctx, backup)
	if err != nil {
		return err
	}
	return nil
}

// GenerateObjectStoreNameForRestore generates a unique object store name for restore operations.
// It retrieves the base object store name from the backup and appends a random 4-character
// alphanumeric suffix to ensure uniqueness. The returned name follows the format:
// "RST-{objectStore}-{4 random alphanumeric characters}".
func (a *BackupActivity) GenerateObjectStoreNameForRestore(ctx context.Context, backupVault *datamodel.BackupVault, backup *datamodel.Backup) (string, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Generating restore object store name for backup %s", backup.UUID))
	objectStore, err := GetObjStoreNameFromBackup(backupVault, backup)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("RST-%s-%s", objectStore, utils.GenerateRandomAlphanumeric(4)), nil
}

func getObjStoreName(backupVault *datamodel.BackupVault, vol *datamodel.Volume) (string, error) {
	bucketDetails, err := getBucketDetails(backupVault, vol)
	if err != nil {
		return "", err
	}
	return bucketDetails.BucketName, nil
}

func getBucketDetails(backupVault *datamodel.BackupVault, vol *datamodel.Volume) (*datamodel.BucketDetails, error) {
	if backupVault.ServiceType == GCBDRServiceType {
		if len(backupVault.BucketDetails) > 0 && backupVault.BucketDetails[0].BucketName != "" {
			return backupVault.BucketDetails[0], nil
		}
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("no bucket details found for GCBDR vault %s", backupVault.Name))
	}
	for _, bucketDetail := range backupVault.BucketDetails {
		if bucketDetail.VendorSubnetID == vol.VolumeAttributes.VendorSubnetID && bucketDetail.BucketName != "" {
			return bucketDetail, nil
		}
	}
	return nil, vsaerrors.ExtractCustomError(fmt.Errorf("no matching bucket details found for volume %s in backup vault %s", vol.Name, backupVault.Name))
}

func getSmSourcePath(volume *datamodel.Volume) string {
	return fmt.Sprintf("%s:%s", volume.Svm.Name, volume.Name)
}

func smDestinationVolumeID(volume *datamodel.Volume) string {
	if volume == nil {
		return ""
	}
	if volume.Pool != nil && volume.Pool.APIAccessMode == expertModePoolAPIAccessMode &&
		volume.VolumeAttributes != nil && volume.VolumeAttributes.ExternalUUID != "" {
		return volume.VolumeAttributes.ExternalUUID
	}
	return volume.UUID
}

func getSmDestinationPath(backupVault *datamodel.BackupVault, volume *datamodel.Volume) (string, error) {
	objStoreName, err := getObjStoreName(backupVault, volume)
	if err != nil {
		return "", fmt.Errorf("failed to get object store name: %w", err)
	}
	return fmt.Sprintf("%s:/objstore/%s", objStoreName, smDestinationVolumeID(volume)), nil
}

func GetSmSourcePath(volume *datamodel.Volume) string {
	return fmt.Sprintf("%s:%s", volume.Svm.Name, volume.Name)
}

func GetObjStoreName(backupVault *datamodel.BackupVault, vol *datamodel.Volume) (string, error) {
	bucketDetails, err := GetBucketDetails(backupVault, vol)
	if err != nil {
		return "", err
	}
	return bucketDetails.BucketName, nil
}

func GetSmDestinationPath(backupVault *datamodel.BackupVault, volume *datamodel.Volume) (string, error) {
	objStoreName, err := GetObjStoreName(backupVault, volume)
	if err != nil {
		return "", fmt.Errorf("failed to get object store name: %w", err)
	}
	return fmt.Sprintf("%s:/objstore/%s", objStoreName, smDestinationVolumeID(volume)), nil
}

// GetBucketDetails returns the bucket associated with the given volume in the backup vault.
// GCBDR vaults use a single shared bucket (first entry), while regular vaults match by VendorSubnetID.
func GetBucketDetails(backupVault *datamodel.BackupVault, vol *datamodel.Volume) (*datamodel.BucketDetails, error) {
	if backupVault.ServiceType == GCBDRServiceType {
		if len(backupVault.BucketDetails) > 0 && backupVault.BucketDetails[0].BucketName != "" {
			return backupVault.BucketDetails[0], nil
		}
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("no bucket details found for GCBDR vault %s", backupVault.Name))
	}
	if vol.VolumeAttributes == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("volume %s has no volume attributes", vol.Name))
	}

	for _, bucketDetail := range backupVault.BucketDetails {
		if bucketDetail.VendorSubnetID == vol.VolumeAttributes.VendorSubnetID && bucketDetail.BucketName != "" {
			return bucketDetail, nil
		}
	}
	return nil, vsaerrors.ExtractCustomError(fmt.Errorf("no matching bucket details found for volume %s in backup vault %s", vol.Name, backupVault.Name))
}

func GetObjStoreNameFromBackup(backupVault *datamodel.BackupVault, backup *datamodel.Backup) (string, error) {
	bucketDetails, err := GetBucketDetailsFromBackup(backupVault, backup)
	if err != nil {
		return "", err
	}
	return bucketDetails.BucketName, nil
}

func GetBucketDetailsFromBackup(backupVault *datamodel.BackupVault, backup *datamodel.Backup) (*datamodel.BucketDetails, error) {
	if backup.Attributes == nil {
		return nil, vsaerrors.ExtractCustomError(fmt.Errorf("backup %s has no attributes", backup.Name))
	}

	for _, bucketDetail := range backupVault.BucketDetails {
		if bucketDetail.BucketName != "" && bucketDetail.BucketName == backup.Attributes.BucketName {
			return bucketDetail, nil
		}
	}
	return nil, vsaerrors.ExtractCustomError(fmt.Errorf("no matching bucket details found for backup %s", backup.Name))
}

// Activity methods for workflow execution

func (a BackupActivity) GetObjStoreNameActivity(ctx context.Context, backupVault *datamodel.BackupVault, volume *datamodel.Volume) (string, error) {
	return GetObjStoreName(backupVault, volume)
}

func (a BackupActivity) GetBucketDetailsActivity(ctx context.Context, backupVault *datamodel.BackupVault, volume *datamodel.Volume) (*datamodel.BucketDetails, error) {
	return GetBucketDetails(backupVault, volume)
}

func (a BackupActivity) GetObjStoreNameFromBackupActivity(ctx context.Context, backupVault *datamodel.BackupVault, backup *datamodel.Backup) (string, error) {
	return GetObjStoreNameFromBackup(backupVault, backup)
}

func (a BackupActivity) GetBucketDetailsFromBackupActivity(ctx context.Context, backupVault *datamodel.BackupVault, backup *datamodel.Backup) (*datamodel.BucketDetails, error) {
	return GetBucketDetailsFromBackup(backupVault, backup)
}

func (a BackupActivity) GetSmSourcePathActivity(ctx context.Context, volume *datamodel.Volume) (string, error) {
	return GetSmSourcePath(volume), nil
}

func (a BackupActivity) GetSmDestinationPathActivity(ctx context.Context, backupVault *datamodel.BackupVault, volume *datamodel.Volume) (string, error) {
	return GetSmDestinationPath(backupVault, volume)
}

// CleanupOldBackupSnapshotsActivity cleans up older backup snapshots for a volume, keeping only the latest one.
// When concurrent scheduled backups run, snapshots created at or after the oldest CREATING backup's created_at
// are preserved so in-progress transfers are not broken; older snapshots are still deleted.
func (a BackupActivity) CleanupOldBackupSnapshotsActivity(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Fetching active backup cutoff for volume %s", volume.UUID))
	cutoffTime, cutoffErr := a.SE.GetEarliestCreatingBackupTime(ctx, volume.UUID)
	if cutoffErr != nil {
		logger.Errorf("Failed to get earliest creating backup time for volume %s: %v", volume.Name, cutoffErr)
	}

	// Get all backup snapshots for this volume, ordered by creation time (newest first)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup snapshots for volume %s", volume.UUID))
	snapshots, err := a.SE.GetSnapshotsByTypeAndVolumeID(ctx, SnapshotTypeBackup, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get backup snapshots for volume %s: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if len(snapshots) <= 1 {
		logger.Infof("No cleanup needed for volume %s - found %d backup snapshots", volume.Name, len(snapshots))
		return nil
	}

	logger.Infof("Found %d backup snapshots for volume %s, evaluating cleanup", len(snapshots), volume.Name)

	// Process older snapshots (skip index 0 which is the newest)
	for i := 1; i < len(snapshots); i++ {
		snapshot := snapshots[i]
		recordHeartbeat(ctx, fmt.Sprintf("Processing backup snapshot %d of %d for volume %s", i+1, len(snapshots), volume.UUID))

		if cutoffTime != nil && !snapshot.CreatedAt.Before(*cutoffTime) {
			logger.Infof("Preserving snapshot %s for volume %s: created at %v (cutoff for in-progress backups %v)",
				snapshot.Name, volume.Name, snapshot.CreatedAt, *cutoffTime)
			continue
		}

		logger.Infof("Deleting older backup snapshot %s for volume %s", snapshot.Name, volume.Name)

		// Try to delete the snapshot from ONTAP first
		if snapshot.SnapshotAttributes != nil && snapshot.SnapshotAttributes.ExternalUUID != "" && volume.VolumeAttributes != nil && volume.VolumeAttributes.ExternalUUID != "" {
			err = a.DeleteBackupSnapshot(ctx, node, snapshot.SnapshotAttributes.ExternalUUID, volume.VolumeAttributes.ExternalUUID)
			if err != nil {
				logger.Errorf("Failed to delete snapshot %s from ONTAP: %v", snapshot.Name, err)
				// Mark snapshot as error state instead of failing the entire operation
				err = a.markSnapshotAsError(ctx, snapshot, fmt.Sprintf("Failed to delete from ONTAP: %v", err))
				if err != nil {
					logger.Errorf("Failed to mark snapshot %s as error: %v", snapshot.Name, err)
				}
				continue
			}
		}

		// Delete the snapshot from database
		recordHeartbeat(ctx, fmt.Sprintf("Deleting backup snapshot %s from database", snapshot.UUID))
		_, err = a.SE.DeleteSnapshot(ctx, snapshot.UUID)
		if err != nil && !errors.IsNotFoundErr(err) {
			logger.Errorf("Failed to delete snapshot %s from database: %v", snapshot.Name, err)
			// Mark snapshot as error state instead of failing the entire operation
			err = a.markSnapshotAsError(ctx, snapshot, fmt.Sprintf("Failed to delete from database: %v", err))
			if err != nil {
				logger.Errorf("Failed to mark snapshot %s as error: %v", snapshot.Name, err)
			}
			continue
		}

		// Hydrate snapshot deletion to CCFE
		snapshot.Volume.Pool = volume.Pool
		location := utils.GetLocation(*snapshot)
		snapshot.State = datamodel.LifeCycleStateDeleted
		snapshot.StateDetails = datamodel.LifeCycleStateDeletedDetails
		err = a.HydrateSnapshotDeletionToCCFEActivity(ctx, snapshot, volume.Name, location, volume.Account.Name)
		if err != nil {
			logger.Errorf("Failed to hydrate snapshot deletion to CCFE for snapshot %s: %v", snapshot.Name, err)
			continue
		}

		logger.Infof("Successfully deleted older backup snapshot %s for volume %s", snapshot.Name, volume.Name)
	}

	return nil
}

// markSnapshotAsError marks a snapshot as error state
func (a BackupActivity) markSnapshotAsError(ctx context.Context, snapshot *datamodel.Snapshot, errorMessage string) error {
	snapshot.State = datamodel.LifeCycleStateError
	snapshot.StateDetails = errorMessage
	recordHeartbeat(ctx, fmt.Sprintf("Marking snapshot %s as errored in database", snapshot.UUID))
	_, err := a.SE.UpdateSnapshot(ctx, snapshot)
	return err
}

// HydrateSnapshotToCCFEActivity hydrates the created snapshot to CCFE
func (a BackupActivity) HydrateSnapshotToCCFEActivity(ctx context.Context, snapshot *datamodel.Snapshot, volumeName, location, projectId string) error {
	logger := util.GetLogger(ctx)

	if !hydrationEnabled {
		logger.Info("Hydration is disabled, skipping snapshot hydration to CCFE")
		return nil
	}

	if snapshot == nil {
		logger.Warn("No database snapshot found, skipping hydration")
		return nil
	}

	// Generate callback token
	recordHeartbeat(ctx, "Generating callback token for snapshot hydration")
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		logger.Errorf("Failed to generate callback token for snapshot hydration: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Convert snapshot to GCP hydrate snapshot object
	gcpSnapshot := ConvertSnapshotToGCPHydrateSnapshot(*snapshot)

	// Create request
	request := models.Request{Snapshot: &gcpSnapshot}
	requests := []models.Request{request}

	// Hydrate to CCFE using the existing batch hydration function
	recordHeartbeat(ctx, fmt.Sprintf("Hydrating snapshot %s to CCFE", snapshot.UUID))
	err = commonparams.BatchHydrateCreatedSnapshots(ctx, logger, requests, volumeName, location, projectId, token)
	if err != nil {
		logger.Errorf("Failed to hydrate snapshot to CCFE: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully hydrated snapshot %s to CCFE", snapshot.Name)
	return nil
}

// ConvertSnapshotToGCPHydrateSnapshot converts a datamodel.Snapshot to a GCP-compatible snapshot object
func ConvertSnapshotToGCPHydrateSnapshot(snapshot datamodel.Snapshot) models.HydrateSnapshot {
	gcpSnapshot := models.HydrateSnapshot{
		ResourceId:   utils.RenameSnapshotName(snapshot.Name),
		SnapshotId:   snapshot.UUID,
		State:        commonparams.MapStateToGcpState(snapshot.State),
		StateDetails: snapshot.StateDetails,
		CreateTime:   snapshot.CreatedAt,
		VolumeName:   snapshot.Volume.Name,
		AccountName:  snapshot.Account.Name,
	}

	if snapshot.SnapshotAttributes != nil {
		gcpSnapshot.UsedBytes = snapshot.SnapshotAttributes.SizeInBytes
	}

	if snapshot.Description != "" {
		gcpSnapshot.Description = snapshot.Description
	}

	return gcpSnapshot
}

// HydrateSnapshotDeletionToCCFEActivity hydrates the deleted snapshot to CCFE
func (a BackupActivity) HydrateSnapshotDeletionToCCFEActivity(ctx context.Context, snapshot *datamodel.Snapshot, volumeName, location, projectId string) error {
	logger := util.GetLogger(ctx)

	if !hydrationEnabled {
		logger.Info("Hydration is disabled, skipping snapshot deletion hydration to CCFE")
		return nil
	}

	if snapshot == nil {
		logger.Warn("No database snapshot found, skipping deletion hydration")
		return nil
	}

	// Generate callback token
	recordHeartbeat(ctx, "Generating callback token for snapshot deletion hydration")
	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		logger.Errorf("Failed to generate callback token for snapshot deletion hydration: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Convert snapshot to GCP hydrate snapshot object for deletion
	gcpSnapshot := ConvertSnapshotToGCPHydrateSnapshot(*snapshot)

	// Create request for deletion
	request := models.Request{Snapshot: &gcpSnapshot}
	requests := []models.Request{request}

	// Hydrate deletion to CCFE using the existing batch hydration function
	recordHeartbeat(ctx, fmt.Sprintf("Hydrating snapshot deletion %s to CCFE", snapshot.UUID))
	err = commonparams.BatchHydrateDeletedSnapshots(ctx, logger, requests, volumeName, location, projectId, token)
	if err != nil {
		logger.Errorf("Failed to hydrate snapshot deletion to CCFE: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully hydrated snapshot deletion %s to CCFE", snapshot.Name)
	return nil
}

// IsLatestBackupAnyStateActivity checks if a backup is the latest for its volume regardless of state
func (b *BackupActivity) IsLatestBackupAnyStateActivity(ctx context.Context, backupUUID, volumeUUID string) (bool, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Checking latest backup %s for volume %s", backupUUID, volumeUUID))
	isLatest, err := b.SE.IsLatestBackupAnyState(ctx, backupUUID, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to check if backup is latest: %v", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return isLatest, nil
}

// IsLatestBackupInVaultActivity checks if a backup is the latest for its volume in the given vault regardless of state
func (b *BackupActivity) IsLatestBackupInVaultActivity(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64) (bool, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Checking latest backup %s in vault %d", backupUUID, backupVaultID))
	isLatest, err := b.SE.IsLatestBackupInVault(ctx, backupUUID, volumeUUID, backupVaultID)
	if err != nil {
		logger.Errorf("Failed to check if backup is latest in vault: %v", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return isLatest, nil
}

// IsLatestBackupInVaultAndInEndpointActivity checks if a backup is the latest for its volume
// scoped to the given backup vault and object-store endpoint.
func (b *BackupActivity) IsLatestBackupInVaultAndInEndpointActivity(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64, endpointUUID string) (bool, error) {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Checking latest backup %s in vault %d and endpoint", backupUUID, backupVaultID))
	isLatest, err := b.SE.IsLatestBackupInVaultAndInEndpoint(ctx, backupUUID, volumeUUID, backupVaultID, endpointUUID)
	if err != nil {
		logger.Errorf("Failed to check if backup is latest in vault and endpoint: %v", err)
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return isLatest, nil
}

// PollTransferStatusWithHistoryCheckActivity polls transfer status with event history monitoring and ContinueAsNew capability
// This activity combines CheckTransferStatusActivity with event history limit checking
// and triggers ContinueAsNew when history limits are reached
func (b *BackupActivity) PollTransferStatusWithHistoryCheckActivity(ctx context.Context, input *PollTransferStatusInput, currentTime time.Time) (*PollTransferStatusOutput, error) {
	logger := util.GetLogger(ctx)

	// Check transfer status using existing GetSnapmirrorTransferStatus logic
	// This works for both backup and restore workflows
	transferStatus, err := b.GetSnapmirrorTransferStatus(ctx, input.Node, input.SnapmirrorRelationship.UUID, input.SnapshotName)
	if err != nil {
		return nil, err
	}
	logger.Info("Polled snapmirror transfer status", "snapshotName", input.SnapshotName, "status", transferStatus.Status, "bytesTransferred", transferStatus.BytesTransferred)

	// Update the context with the new status
	input.BackupActivitiesContext.TransferStatus = transferStatus.Status

	// Check if we need to trigger ContinueAsNew based on event history
	shouldContinueAsNew := false
	continueAsNewReason := ""

	// Check if event history limit is reached
	if input.EventHistoryCount >= EventHistorySafetyThreshold {
		shouldContinueAsNew = true
		continueAsNewReason = "Event history limit reached"
	}

	// Check if transfer is complete
	transferComplete := false
	switch transferStatus.Status {
	case SmStatusSuccess:
		if input.BackupActivitiesContext.ScheduledBackupParams != nil {
			for _, bkp := range input.BackupActivitiesContext.ScheduledBackupParams.Backups {
				bkp.Attributes.SnapshotCreationTime = currentTime.String()
			}
		} else {
			input.BackupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotCreationTime = currentTime.String()
		}
		transferComplete = true
		logger.Info("Transfer completed successfully", "snapshotName", input.SnapshotName)
	case SmStatusFailed:
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(fmt.Errorf("snapmirror transfer failed for snapshot %s with status: %s", input.SnapshotName, transferStatus.Status))
	}

	return &PollTransferStatusOutput{
		BackupActivitiesContext: input.BackupActivitiesContext,
		TransferComplete:        transferComplete,
		ShouldContinueAsNew:     shouldContinueAsNew,
		ContinueAsNewReason:     continueAsNewReason,
		NextWaitTime:            input.NextWaitTime,
		TransferStatus:          transferStatus, // Include full transfer status with bytes
	}, nil
}

// CreateBackupMetadataIfFirstBackupActivity creates a BackupMetadata entry if this is the first backup for the volume
func (b *BackupActivity) CreateBackupMetadataIfFirstBackupActivity(ctx context.Context, volume *datamodel.Volume, isExpertMode bool) error {
	logger := util.GetLogger(ctx)

	var volumeUUID string
	if isExpertMode {
		volumeUUID = volume.VolumeAttributes.ExternalUUID
	} else {
		volumeUUID = volume.UUID
	}

	// Get all backups for this volume
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backups for volume %s", volumeUUID))
	backups, err := b.SE.GetBackupsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get backups for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if this is the first backup (count should be 1 since we just created one)
	if len(backups) == 1 {
		logger.Infof("This is the first backup for volume %s, creating BackupMetadata entry", volumeUUID)

		// Extract labels from volume
		var labels *datamodel.JSONB
		if volume.VolumeAttributes != nil && volume.VolumeAttributes.Labels != nil {
			labels = volume.VolumeAttributes.Labels
		} else {
			// If no labels exist, create empty JSONB
			labels = &datamodel.JSONB{}
		}

		// Create BackupMetadata entry with volume labels
		backupMetadata := &datamodel.BackupMetadata{
			VolumeUUID: volumeUUID,
			Labels:     labels,
		}

		recordHeartbeat(ctx, fmt.Sprintf("Creating backup metadata for volume %s", volumeUUID))
		_, err := b.SE.CreateBackupMetadata(ctx, backupMetadata)
		if err != nil {
			logger.Errorf("Failed to create BackupMetadata for volume %s: %v", volumeUUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		logger.Infof("Successfully created BackupMetadata entry for volume %s with labels", volumeUUID)
	} else {
		logger.Infof("Volume %s already has %d backups, skipping BackupMetadata creation", volumeUUID, len(backups))
	}

	return nil
}

// DeleteBackupMetadataIfLastBackupActivity deletes a BackupMetadata entry if this is the last backup for the volume
func (b *BackupActivity) DeleteBackupMetadataIfLastBackupActivity(ctx context.Context, volumeUUID string) error {
	logger := util.GetLogger(ctx)

	// Get all backups for this volume
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backups for volume %s", volumeUUID))
	backups, err := b.SE.GetBackupsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get backups for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if this is the last backup (count should be 0 since we just deleted one)
	if len(backups) == 0 {
		logger.Infof("This was the last backup for volume %s, deleting BackupMetadata entry", volumeUUID)

		// Delete BackupMetadata entry
		recordHeartbeat(ctx, fmt.Sprintf("Deleting backup metadata for volume %s", volumeUUID))
		err := b.SE.DeleteBackupMetadata(ctx, volumeUUID)
		if err != nil {
			logger.Errorf("Failed to delete BackupMetadata for volume %s: %v", volumeUUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		logger.Infof("Successfully deleted BackupMetadata entry for volume %s", volumeUUID)
	} else {
		logger.Infof("Volume %s still has %d backups, keeping BackupMetadata entry", volumeUUID, len(backups))
	}

	return nil
}

// UpdateBackupMetadataIfExistsActivity updates BackupMetadata labels if an entry exists for the volume
func (b *BackupActivity) UpdateBackupMetadataIfExistsActivity(ctx context.Context, volume *datamodel.Volume) error {
	logger := util.GetLogger(ctx)

	// Check if BackupMetadata entry exists for this volume
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup metadata for volume %s", volume.UUID))
	backupMetadata, err := b.SE.GetBackupMetadataByVolumeUUID(ctx, volume.UUID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			// No BackupMetadata entry exists yet - this is expected if no backups have been created
			logger.Infof("No BackupMetadata entry found for volume %s - will be created when first backup is made", volume.UUID)
			return nil
		}
		logger.Errorf("Failed to get BackupMetadata for volume %s: %v", volume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Update the labels in the existing BackupMetadata entry
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.Labels != nil {
		backupMetadata.Labels = volume.VolumeAttributes.Labels
	} else {
		// If no labels exist, set to empty JSONB
		backupMetadata.Labels = &datamodel.JSONB{}
	}
	recordHeartbeat(ctx, fmt.Sprintf("Updating backup metadata for volume %s", volume.UUID))
	_, err = b.SE.UpdateBackupMetadata(ctx, backupMetadata)
	if err != nil {
		logger.Errorf("Failed to update BackupMetadata for volume %s: %v", volume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated BackupMetadata labels for volume %s", volume.UUID)
	return nil
}

// DeleteRemoteBackupFromVCPActivity deletes the Backup from the remote region using Google Proxy Client
func (a *BackupActivity) DeleteRemoteBackupFromVCPActivity(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) error {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for backup %s", backupUUID))
	basePath, jwtToken, err := commonparams.GetRemoteRegionConfig(region, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", region, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	params := googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultParams{
		ProjectNumber:  projectNumber,
		LocationId:     region,
		BackupVaultId:  backupVaultUUID,
		BackupId:       backupUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Deleting remote backup %s in region %s", backupUUID, region))
	res, err := googleProxyClient.Invoker.V1betaInternalDeleteBackupUnderBackupVault(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaInternalDeleteBackupUnderBackupVault: %v", err)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to delete remote backup: %v", err),
			"InternalDeleteBackupUnderBackupVaultFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.InternalBackupV1beta:
		logger.Infof("Successfully deleted remote backup %s in region %s",
			backupUUID, region)
		return nil

	case *googleproxyclient.OperationV1beta:
		isDone := r.Done.Value
		logger.Infof("Delete operation returned for remote backup %s in region %s. Operation: %s, Done: %v",
			backupUUID, region, r.GetName().Value, isDone)

		if !isDone {
			logger.Warnf("Delete operation for remote backup %s not marked as done, but treating as synchronous", backupUUID)
		}

		logger.Infof("Successfully deleted remote backup %s (external UUID) in region %s",
			backupUUID, region)
		return nil

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultBadRequest:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultUnauthorized:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to delete remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultForbidden:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to delete remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultNotFound:
		logger.Warnf("Remote backup (corresponding to %s) not found when attempting to delete: %s", backupUUID, r.Message)
		return nil

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultConflict:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultUnprocessableEntity:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultInternalServerError:
		return temporal.NewApplicationError(
			fmt.Sprintf("Internal server error deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultInternalServerError",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultTooManyRequests:
		return temporal.NewApplicationError(
			fmt.Sprintf("Too many requests deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultTooManyRequests",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalDeleteBackupUnderBackupVaultNotImplemented:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Not implemented deleting remote backup: %s", r.Message),
			"V1betaInternalDeleteBackupUnderBackupVaultNotImplemented",
			errors.New(r.Message),
		)

	default:
		return temporal.NewApplicationError(
			fmt.Sprintf("Unexpected response type from internal delete backup endpoint: %T", r),
			"UnexpectedDeleteResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

func (a *BackupActivity) UpdateBackupRestoreCount(ctx context.Context, backupVaultUUID, backupUUID, accountName, operation string) error {
	se := a.SE
	logger := util.GetLogger(ctx)

	// Fetching latest backup so we have the most recent restore count
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup %s restore count", backupUUID))
	backup, err := se.GetBackup(ctx, backupVaultUUID, backupUUID, accountName)
	if err != nil {
		// For SDE/CVP backups that don't exist in VCP database, skip the restore count update
		// These backups are not persisted to VCP database during restore operations
		logger.Warnf("Backup %s not found in VCP database (likely SDE/CVP backup), skipping restore count update: %v", backupUUID, err)
		return nil
	}

	// Ensure backup attributes are initialized
	if backup.Attributes == nil {
		logger.Warnf("Backup %s has nil attributes, initializing with default values", backupUUID)
		backup.Attributes = &datamodel.BackupAttributes{}
	}

	if operation == BackupRestoreCountIncrement {
		backup.Attributes.RestoreVolumeCount++
	} else {
		backup.Attributes.RestoreVolumeCount--
	}
	updates := map[string]interface{}{
		"attributes": backup.Attributes,
	}
	recordHeartbeat(ctx, fmt.Sprintf("Updating backup %s restore count", backupUUID))
	err = se.UpdateBackupFields(ctx, backupUUID, updates)
	if err != nil {
		logger.Errorf("Failed to update backup %s: %v", backupUUID, err)
		return err
	}
	logger.Infof("Successfully updated backup restore count to %d for backup %s", backup.Attributes.RestoreVolumeCount, backupUUID)
	return nil
}

// CreateRemoteBackupFromVCPActivity creates the Backup in the remote region using Google Proxy Client
// This is done after UpdateBackupSizeActivity to ensure ExternalUUID is set
func (a *BackupActivity) CreateRemoteBackupFromVCPActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) error {
	// Check if this is a cross-region backup
	if backupActivitiesContext.BackupWorkflowInit.BackupVault.BackupVaultType != CrossRegionBackupType ||
		backupActivitiesContext.BackupWorkflowInit.BackupVault.BackupRegionName == nil {
		// Not a cross-region backup, skip
		return nil
	}

	logger := util.GetLogger(ctx)
	backup := backupActivitiesContext.BackupWorkflowInit.Backup
	backupVault := backupActivitiesContext.BackupWorkflowInit.BackupVault
	projectNumber := backupActivitiesContext.BackupWorkflowInit.Volume.Account.Name
	// A cross-project vault lives in a different project than the volume, and the remote
	// region scopes the vault to its owning account.
	if backupVault.ServiceType == datamodel.ServiceTypeCrossProject {
		if backupVault.Account != nil && backupVault.Account.Name != "" {
			projectNumber = backupVault.Account.Name
		} else {
			logger.Error("backup vault account is nil/empty for cross-project remote backup creation",
				"backupVaultUUID", backupVault.UUID, "backupUUID", backup.UUID)
			return vsaerrors.WrapAsTemporalApplicationError(errors.New("backup vault account is nil/empty for cross-project remote backup creation"))
		}
	}
	region := *backupActivitiesContext.BackupWorkflowInit.BackupVault.BackupRegionName

	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for backup %s", backup.UUID))
	basePath, jwtToken, err := commonparams.GetRemoteRegionConfig(region, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", region, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Convert backup to InternalBackupCreate format for the request
	// Internal API requires volume and snapshot information as they are not available in remote region DB
	// backupUUID is the ExternalUUID for cross-region backups - use backup.UUID (internal UUID from source region)
	backupCreate := googleproxyclient.InternalBackupCreateV1beta{
		ResourceId: backup.Name,
		BackupUUID: backup.UUID, // This becomes ExternalUUID in the remote region
		VolumeId:   backup.VolumeUUID,
		Description: googleproxyclient.OptString{
			Value: backup.Description,
			Set:   backup.Description != "",
		},
		VolumeUsageBytes: googleproxyclient.OptInt64{
			Value: backup.SizeInBytes,
			Set:   true,
		},
		BackupType: googleproxyclient.OptInternalBackupCreateV1betaBackupType{
			Value: googleproxyclient.InternalBackupCreateV1betaBackupType(backup.Type),
			Set:   true,
		},
		BackupChainBytes: googleproxyclient.OptInt64{
			Value: backup.LatestLogicalBackupSize,
			Set:   true,
		},
	}

	// Include volume information (required for cross-region)
	if backup.Attributes != nil {
		backupCreate.VolumeName = backup.Attributes.VolumeName
		if len(backup.Attributes.Protocols) > 0 {
			protocols := make([]googleproxyclient.InternalBackupCreateV1betaProtocolsItem, len(backup.Attributes.Protocols))
			for i, p := range backup.Attributes.Protocols {
				protocols[i] = googleproxyclient.InternalBackupCreateV1betaProtocolsItem(p)
			}
			backupCreate.Protocols = protocols
		}
		// Include useExistingSnapshot flag
		backupCreate.UseExistingSnapshot = googleproxyclient.NewOptBool(backup.Attributes.UseExistingSnapshot)
		// Include snapshot information if available (always include all details)
		if backup.Attributes.SnapshotID != "" {
			backupCreate.SnapshotId = googleproxyclient.NewOptString(backup.Attributes.SnapshotID)
		}
		if backup.Attributes.SnapshotName != "" {
			backupCreate.SnapshotName = googleproxyclient.NewOptString(backup.Attributes.SnapshotName)
		}
		// Include backup attributes for cross-region operations
		if backup.Attributes.BucketName != "" {
			backupCreate.BucketName = googleproxyclient.NewOptString(backup.Attributes.BucketName)
		}
		if backup.Attributes.EndpointUUID != "" {
			backupCreate.EndpointUuid = googleproxyclient.NewOptString(backup.Attributes.EndpointUUID)
		}
		backupCreate.IsRegionalHa = googleproxyclient.NewOptBool(backup.Attributes.IsRegionalHA)
		backupCreate.IsOntapBackup = googleproxyclient.NewOptBool(backup.Attributes != nil && backup.Attributes.IsExpertModeBackup)
		if backup.Attributes.CompletionTime != "" {
			// Parse the completion time string to time.Time for OptDateTime
			if completionTime, err := time.Parse(time.RFC3339, backup.Attributes.CompletionTime); err == nil {
				backupCreate.CompletionTime = googleproxyclient.NewOptDateTime(completionTime)
			}
		}
		if backup.Attributes.BackupPolicyName != "" {
			backupCreate.BackupPolicyName = googleproxyclient.NewOptString(backup.Attributes.BackupPolicyName)
		}
		if backup.Attributes.OntapVolumeStyle != "" {
			backupCreate.OntapVolumeStyle = googleproxyclient.NewOptString(backup.Attributes.OntapVolumeStyle)
		}
		if backup.Attributes.SourceVolumeZone != "" {
			backupCreate.SourceVolumeZone = googleproxyclient.NewOptString(backup.Attributes.SourceVolumeZone)
		}
		if backup.Attributes.ServiceAccountName != "" {
			backupCreate.ServiceAccountName = googleproxyclient.NewOptString(backup.Attributes.ServiceAccountName)
		}
		if backup.Attributes.SnapshotCreationTime != "" {
			// Parse the snapshot creation time string to time.Time for OptDateTime
			if snapshotCreationTime, err := time.Parse(time.RFC3339, backup.Attributes.SnapshotCreationTime); err == nil {
				backupCreate.SnapshotCreationTime = googleproxyclient.NewOptDateTime(snapshotCreationTime)
			}
		}
		if backup.Attributes.ConstituentCountOfBackup > 0 {
			backupCreate.ConstituentCountOfBackup = googleproxyclient.NewOptInt32(backup.Attributes.ConstituentCountOfBackup)
		}
	}

	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	if backup.Attributes != nil && backup.Attributes.IsExpertModeBackup &&
		volume != nil && volume.Pool != nil && volume.Pool.Name != "" &&
		backupVault.SourceRegionName != nil && *backupVault.SourceRegionName != "" {
		sourcePoolLocation := getSourceStoragePoolLocation(volume.Pool, backupVault.SourceRegionName)
		if sourcePoolLocation == "" {
			sourcePoolLocation = *backupVault.SourceRegionName
		}
		// The source pool always lives in the volume's project, even for cross-project vaults.
		sourcePoolPath := fmt.Sprintf("projects/%s/locations/%s/storagePools/%s",
			volume.Account.Name, sourcePoolLocation, volume.Pool.Name)
		backupCreate.SourceStoragePool = googleproxyclient.NewOptString(sourcePoolPath)
	}

	if backup.AssetMetadata != nil {
		backupCreate.AssetLocationMetadata = googleproxyclient.OptAssetLocationMetadataV2{
			Value: googleproxyclient.AssetLocationMetadataV2{
				ChildAssets: func() []googleproxyclient.ChildAssetV2 {
					var assets []googleproxyclient.ChildAssetV2
					for _, asset := range backup.AssetMetadata.ChildAssets {
						assets = append(assets, googleproxyclient.ChildAssetV2{
							AssetType:  googleproxyclient.OptString{Value: asset.AssetType, Set: true},
							AssetNames: asset.AssetNames,
						})
					}
					return assets
				}(),
			},
			Set: true,
		}
	}

	params := googleproxyclient.V1betaInternalCreateBackupParams{
		ProjectNumber:  projectNumber,
		LocationId:     region,
		BackupVaultId:  backupVault.UUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating remote backup %s in region %s", backup.UUID, region))
	res, err := googleProxyClient.Invoker.V1betaInternalCreateBackup(ctx, &backupCreate, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaInternalCreateBackup: %v", err)
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Failed to create remote backup: %v", err),
			"InternalCreateBackupFailed",
			err,
		)
	}

	switch r := res.(type) {
	case *googleproxyclient.InternalBackupV1beta:
		logger.Infof("Successfully created remote backup %s in region %s",
			backup.Name, region)
		if r.BackupId.Set && r.BackupId.Value != "" {
			updates := map[string]interface{}{
				"external_uuid": r.BackupId.Value,
			}
			recordHeartbeat(ctx, fmt.Sprintf("Storing remote identifier for backup %s", backup.UUID))
			if updateErr := a.SE.UpdateBackupFields(ctx, backup.UUID, updates); updateErr != nil {
				logger.Errorf("Failed to store cross-region backup UUID on source backup %s: %v", backup.UUID, updateErr)
			}
		}
		return nil

	case *googleproxyclient.OperationV1beta:
		isDone := r.Done.Value
		logger.Infof("Create operation returned for remote backup %s in region %s. Operation: %s, Done: %v",
			backup.Name, region, r.GetName().Value, isDone)

		if !isDone {
			logger.Warnf("Create operation for remote backup %s not marked as done, but treating as synchronous", backup.Name)
		}

		logger.Infof("Successfully created remote backup %s in region %s",
			backup.Name, region)
		return nil

	case *googleproxyclient.V1betaInternalCreateBackupBadRequest:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Bad request creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupBadRequest",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupUnauthorized:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unauthorized to create remote backup: %s", r.Message),
			"V1betaInternalCreateBackupUnauthorized",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupForbidden:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Forbidden to create remote backup: %s", r.Message),
			"V1betaInternalCreateBackupForbidden",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupConflict:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Conflict creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupConflict",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupUnprocessableEntity:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Unprocessable entity creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupUnprocessableEntity",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupInternalServerError:
		return temporal.NewApplicationError(
			fmt.Sprintf("Internal server error creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupInternalServerError",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupTooManyRequests:
		return temporal.NewApplicationError(
			fmt.Sprintf("Too many requests creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupTooManyRequests",
			errors.New(r.Message),
		)

	case *googleproxyclient.V1betaInternalCreateBackupNotImplemented:
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("Not implemented creating remote backup: %s", r.Message),
			"V1betaInternalCreateBackupNotImplemented",
			errors.New(r.Message),
		)

	default:
		return temporal.NewApplicationError(
			fmt.Sprintf("Unexpected response type from internal create backup endpoint: %T", r),
			"UnexpectedCreateResponseType",
			fmt.Errorf("unexpected response type: %T", r),
		)
	}
}

func getSourceStoragePoolLocation(pool *datamodel.Pool, sourceRegion *string) string {
	if pool == nil || pool.PoolAttributes == nil {
		return ""
	}
	if pool.PoolAttributes.IsRegionalHA {
		if sourceRegion == nil {
			return ""
		}
		return *sourceRegion
	}
	return pool.PoolAttributes.PrimaryZone
}

// UpdateRemoteBackupFromVCPActivity updates the Backup in the remote region using Google Proxy Client
func (a *BackupActivity) UpdateRemoteBackupFromVCPActivity(ctx context.Context, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)
	// Check if this is a cross-region backup
	if backup.BackupVault == nil || backup.BackupVault.BackupVaultType != "CROSS_REGION" || backup.BackupVault.BackupRegionName == nil {
		// Not a cross-region backup or missing required fields, skip
		logger.Infof("Skipping remote backup update for non-cross-region backup, backupID=%s", backup.UUID)
		return nil
	}
	backupVault, err := a.GetBackupVault(ctx, backup.BackupVault.UUID)
	if err != nil {
		logger.Errorf("Failed to get backup vault: %v, backupID=%s", err, backup.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	region := *backupVault.BackupRegionName

	// Get account name from backup vault
	var projectNumber string
	if backupVault.Account != nil {
		projectNumber = backupVault.Account.Name
	} else if backupVault.AccountVendorID != "" {
		// If account is not loaded, use AccountVendorID (project number)
		projectNumber = backupVault.AccountVendorID
	} else {
		logger.Warn("BackupVault account not loaded and AccountVendorID not available, cannot update remote backup", "backupID", backup.UUID)
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Loading remote region configuration for backup %s", backup.UUID))
	basePath, jwtToken, err := commonparams.GetRemoteRegionConfig(region, projectNumber)
	if err != nil {
		logger.Error("Failed to get remote region configuration", "region", region, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwtToken, logger)
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	backupUpdate := googleproxyclient.BackupUpdateV1beta{
		Description: backup.Description,
	}

	params := googleproxyclient.V1betaInternalUpdateBackupParams{
		ProjectNumber:  projectNumber,
		LocationId:     region,
		BackupVaultId:  backupVault.UUID,
		BackupId:       backup.UUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating remote backup %s in region %s", backup.UUID, region))
	res, err := googleProxyClient.Invoker.V1betaInternalUpdateBackup(ctx, &backupUpdate, params)
	if err != nil {
		logger.Errorf("Failed to update remote Backup: %v, region=%s, backupVaultID=%s, backupID=%s", err, region, nillable.FromPointer(backupVault.ExternalUUID), backup.ExternalUUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if the response indicates success
	if res == nil {
		logger.Error("Unexpected nil response from remote Backup update", "backupID", backup.ExternalUUID)
		return vsaerrors.WrapAsTemporalApplicationError(errors.NewNotFoundErr("remote backup", &backup.ExternalUUID))
	}

	logger.Infof("Successfully updated remote Backup, backupID=%s, region=%s", backup.ExternalUUID, region)
	return nil
}

// GetSnapshotNameByUUIDActivity retrieves the snapshot name using its UUID from the hyperscaler provider
func (a *BackupActivity) GetSnapshotNameByUUIDActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	node := backupActivitiesContext.Node

	if node == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("node is nil"))
	}

	recordHeartbeat(ctx, "Fetching snapshot name from ONTAP")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get provider: %w", err))
	}

	snapshot, err := provider.GetSnapshot(backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotID, backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get snapshot by UUID: %w", err))
	}
	backupActivitiesContext.BackupWorkflowInit.Backup.Attributes.SnapshotName = snapshot.Name
	backupActivitiesContext.SnapshotName = snapshot.Name
	return backupActivitiesContext, nil
}

// CheckAndAttachBackupVaultToVolume checks if backup vault is attached to volume, if not creates bucket and attaches it
func (a *BackupActivity) CheckAndAttachBackupVaultToVolume(ctx context.Context, backupActivitiesContext *BackupActivitiesContext, region string) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)

	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	backupVault := backupActivitiesContext.BackupWorkflowInit.BackupVault

	// Check if backup vault is already attached to volume
	var backupVaultID string
	if volume.DataProtection != nil && volume.DataProtection.BackupVaultID != "" {
		backupVaultID = volume.DataProtection.BackupVaultID
		// If backup vault is already attached and matches, no need to do anything
		if backupVaultID == backupVault.UUID {
			logger.Info("Backup vault already attached to volume", "volumeUUID", volume.UUID, "backupVaultUUID", backupVaultID)
			return backupActivitiesContext, nil
		}
	}

	// Backup vault is not attached or doesn't match, proceed to attach it
	logger.Info("Backup vault not attached to volume, proceeding to attach", "volumeUUID", volume.UUID, "backupVaultUUID", backupVault.UUID)

	// Check if backup vault exists in VCP
	volumeActivity := &VolumeCreateActivity{
		SE: a.SE,
	}
	var existingBackupVault *datamodel.BackupVault
	var err error

	// Try to get backup vault from VCP first. For GCBDR vaults the volume and vault may belong to
	// different accounts, so fall back to UUID-only lookup (same as normal volume backup flow).
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup vault %s for attachment", backupVault.UUID))
	existingBackupVault, err = getBackupVaultDetails(a.SE, ctx, backupVault.UUID, volume.AccountID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to check backup vault in VCP: %w", err)))
	}
	if existingBackupVault == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("backup vault with id %s not found", backupVault.UUID)))
	}

	backupRegion := region
	if existingBackupVault.BackupVaultType == CrossRegionBackupType && existingBackupVault.BackupRegionName != nil && *existingBackupVault.BackupRegionName != "" {
		backupRegion = *existingBackupVault.BackupRegionName
	} else if existingBackupVault.ServiceType == GCBDRServiceType && existingBackupVault.SourceRegionName != nil && *existingBackupVault.SourceRegionName != "" {
		// For GCBDR vaults, use SourceRegionName for bucket region
		backupRegion = *existingBackupVault.SourceRegionName
	}

	// Find tenancy details
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.VendorSubnetID == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("volume does not have VendorSubnetID")))
	}

	// Ensure DataProtection is set for CheckForBucketResourceName (it expects DataProtection.BackupVaultID)
	// For expert mode volumes, this might be nil, so we set it temporarily
	if volume.DataProtection == nil {
		volume.DataProtection = &datamodel.DataProtection{}
	}
	// Set the backup vault ID if not already set
	if volume.DataProtection.BackupVaultID == "" {
		volume.DataProtection.BackupVaultID = existingBackupVault.UUID
	}

	var tenancyDetails *commonparams.TenancyInfo
	// For GCBDR vaults, skip FindTenancy and use vault's tenant project directly
	if existingBackupVault.ServiceType != GCBDRServiceType {
		tenancyDetails, err = volumeActivity.FindTenancy(ctx, volume.VolumeAttributes.VendorSubnetID, volume.Account.Name, &backupRegion)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("failed to find tenancy: %w", err)))
		}
	} else {
		// For GCBDR, prepare tenancy details from vault's bucket details
		if existingBackupVault.BucketDetails != nil && len(existingBackupVault.BucketDetails) > 0 {
			tenancyDetails = &commonparams.TenancyInfo{
				RegionalTenantProject: existingBackupVault.BucketDetails[0].TenantProjectNumber,
			}
			logger.Infof("Using GCBDR vault's tenant project: %s", tenancyDetails.RegionalTenantProject)
		} else {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("GCBDR vault %s has no tenant project information", existingBackupVault.UUID)))
		}
	}
	// Check for bucket resource name
	bucketDetails, err := volumeActivity.CheckForBucketResourceName(ctx, volume)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, fmt.Errorf("failed to check for bucket resource name: %w", err)))
	}
	if bucketDetails == nil {
		bucketDetails = &commonparams.BucketDetails{}
	}

	// If bucket doesn't exist, create it
	if bucketDetails.BucketName == "" && bucketDetails.ServiceAccountName == "" && bucketDetails.TenantProjectNumber == "" {
		// Generate resource names
		resourceName, err := volumeActivity.GenerateResourceNames(ctx, volume, tenancyDetails, region)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to generate resource names: %w", err)))
		}

		// Create bucket
		bucketDetails, err = volumeActivity.CreateBucket(ctx, resourceName, tenancyDetails, backupRegion, nil)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to create bucket: %w", err)))
		}
		if existingBackupVault.ServiceType != GCBDRServiceType {
			bucketDetails.VendorSubnetID = volume.VolumeAttributes.VendorSubnetID
		}

		// Update backup vault with bucket details
		err = volumeActivity.UpdateBackupVaultWithBucketDetails(ctx, volume, bucketDetails)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to update backup vault with bucket details: %w", err)))
		}

		// Handle cross-region backup vaults
		remoteBV, err := volumeActivity.CheckOrCreateRemoteBackupVaultInVCP(ctx, volume, existingBackupVault, bucketDetails)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to check or create remote backup vault: %w", err)))
		}

		if remoteBV != nil {
			err = volumeActivity.UpdateRemoteBackupVaultWithBucketDetails(ctx, volume, existingBackupVault, remoteBV, bucketDetails)
			if err != nil {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to update remote backup vault with bucket details: %w", err)))
			}
		}

		// Setup cross-region permissions (only needed when bucket is new)
		if existingBackupVault.BackupVaultType == CrossRegionBackupType && existingBackupVault.BackupRegionName != nil && *existingBackupVault.BackupRegionName != "" {
			if volume.Pool == nil {
				return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("volume pool cannot be nil for cross-region backup setup")))
			}
			err = volumeActivity.SetupCrossRegionBackupPermissionsActivity(ctx, existingBackupVault, volume.Pool, bucketDetails)
			if err != nil {
				return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to setup cross-region backup permissions: %w", err)))
			}
		}
	}

	// Grant pool SA access to GCBDR bucket unconditionally — runs on every vault attachment
	// so that a pool attaching to an already-provisioned vault still receives the IAM grant.
	if existingBackupVault.ServiceType == GCBDRServiceType {
		if volume.Pool == nil {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("volume pool cannot be nil for GCBDR backup setup")))
		}
		err := volumeActivity.SetupCrossProjectBackupPermissions(ctx, volume.Pool, bucketDetails)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceProvisionError, fmt.Errorf("failed to setup cross-project backup permissions: %w", err)))
		}
		logger.Infof("Successfully granted pool SA access to GCBDR bucket %s", bucketDetails.BucketName)
	}

	// Convert commonparams.BucketDetails to datamodel.BucketDetails
	datamodelBucketDetails := &datamodel.BucketDetails{
		BucketName:          bucketDetails.BucketName,
		ServiceAccountName:  bucketDetails.ServiceAccountName,
		VendorSubnetID:      bucketDetails.VendorSubnetID,
		TenantProjectNumber: bucketDetails.TenantProjectNumber,
		SatisfiesPzi:        bucketDetails.SatisfiesPzi,
		SatisfiesPzs:        bucketDetails.SatisfiesPzs,
	}
	backupActivitiesContext.BackupWorkflowInit.BackupVault.BucketDetails = datamodel.BucketDetailsArray{datamodelBucketDetails}

	// Attach backup vault to volume
	// This function is only called for expert mode volumes, so we must fetch and update the expert mode volume
	recordHeartbeat(ctx, fmt.Sprintf("Fetching expert mode volume %s for vault attachment", volume.UUID))
	expertModeVol, err := a.SE.GetExpertModeVolumeByUUID(ctx, volume.UUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, fmt.Errorf("failed to get expert mode volume: %w", err)))
	}
	// This is an expert mode volume, update BackupConfig instead of DataProtection
	if expertModeVol.BackupConfig == nil {
		expertModeVol.BackupConfig = &datamodel.DataProtection{}
	}
	expertModeVol.BackupConfig.BackupVaultID = existingBackupVault.UUID

	// Update expert mode volume in database
	recordHeartbeat(ctx, fmt.Sprintf("Attaching backup vault to expert mode volume %s", volume.UUID))
	err = a.SE.UpdateExpertModeVolumeDataProtection(ctx, expertModeVol)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("failed to update expert mode volume with backup vault: %w", err)))
	}
	logger.Info("Successfully attached backup vault to expert mode volume", "volumeUUID", volume.UUID, "backupVaultUUID", existingBackupVault.UUID)
	return backupActivitiesContext, nil
}

// GetVolumesAndConstituentCountActivity gets volumes from provider and fetches constituent count
func (b *BackupActivity) GetVolumesAndConstituentCountActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	node := backupActivitiesContext.Node

	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP constituent count for volume %s", volume.UUID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to get provider: %w", err)))
	}

	// Get the specific volume from ONTAP using external UUID
	volumeResponse, err := provider.GetVolume(vsa.GetVolumeParams{
		UUID:    volume.VolumeAttributes.ExternalUUID,
		SvmName: volume.Svm.Name,
	})
	if err != nil {
		logger.Errorf("Failed to get volume from ONTAP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, fmt.Errorf("failed to get volume from ONTAP: %w", err)))
	}

	if volumeResponse == nil {
		logger.Warnf("Volume not found in ONTAP, volumeUUID: %s", volume.VolumeAttributes.ExternalUUID)
		return backupActivitiesContext, nil
	}

	// Get constituent count from the volume response
	if volumeResponse.ConstituentCount != nil {
		constituentCount := *volumeResponse.ConstituentCount
		logger.Infof("Found constituent count for volume %s: %d", volume.Name, constituentCount)
		applyBackupAttributes(backupActivitiesContext.BackupWorkflowInit.Backup, func(attrs *datamodel.BackupAttributes) {
			attrs.ConstituentCountOfBackup = constituentCount
			attrs.OntapVolumeStyle = "flexgroup"
		})
	} else {
		logger.Debugf("No constituent count found for volume %s (may not be a flexgroup volume)", volume.Name)
		applyBackupAttributes(backupActivitiesContext.BackupWorkflowInit.Backup, func(attrs *datamodel.BackupAttributes) {
			attrs.ConstituentCountOfBackup = 0
		})
	}

	return backupActivitiesContext, nil
}

// IsExpertModeVolume checks if a volume is an expert mode volume
func (a BackupActivity) IsExpertModeVolume(ctx context.Context, volumeUUID string) (bool, error) {
	se := a.SE
	// Try to get from expert mode volumes table
	recordHeartbeat(ctx, fmt.Sprintf("Checking expert mode volume %s in database", volumeUUID))
	_, err := se.GetExpertModeVolumeByExternalUUID(ctx, volumeUUID)
	if err == nil {
		return true, nil // Found in expert mode table
	}
	if errors.IsNotFoundErr(err) || strings.Contains(err.Error(), "record not found") {
		return false, nil // Not found in expert mode table
	}
	return false, vsaerrors.WrapAsTemporalApplicationError(err) // Unexpected error
}

// DetachBackupVaultFromVolume detaches a backup vault from an expert mode volume
func (a BackupActivity) DetachBackupVaultFromVolume(ctx context.Context, volume *datamodel.Volume, backupVault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get the expert mode volume
	recordHeartbeat(ctx, fmt.Sprintf("Fetching expert mode volume %s for vault detachment", volume.UUID))
	expertModeVolume, err := se.GetExpertModeVolumeByExternalUUID(ctx, volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		logger.Errorf("Failed to get expert mode volume %s: %v", volume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if BackupConfig exists and has the backup vault attached
	if expertModeVolume.BackupConfig == nil || expertModeVolume.BackupConfig.BackupVaultID == "" {
		logger.Infof("No backup vault attached to expert mode volume %s, nothing to detach", expertModeVolume.UUID)
		return nil
	}

	// Verify the attached backup vault matches the one being detached
	if expertModeVolume.BackupConfig.BackupVaultID != backupVault.UUID {
		logger.Warnf("Backup vault mismatch on volume %s: expected %s, found %s",
			expertModeVolume.UUID, backupVault.UUID, expertModeVolume.BackupConfig.BackupVaultID)
		return nil
	}

	// Clear the BackupVaultID to detach the backup vault
	expertModeVolume.BackupConfig.BackupVaultID = ""

	// Update the expert mode volume in the database
	recordHeartbeat(ctx, fmt.Sprintf("Detaching backup vault from expert mode volume %s", volume.UUID))
	err = se.UpdateExpertModeVolumeDataProtection(ctx, expertModeVolume)
	if err != nil {
		logger.Errorf("Failed to detach backup vault from expert mode volume %s: %v", expertModeVolume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully detached backup vault %s from expert mode volume %s", backupVault.UUID, expertModeVolume.UUID)
	return nil
}

// CleanupOldExpertModeSnapshotActivity cleans up older expert mode snapshots for a volume, keeping only the latest one
func (a BackupActivity) CleanupOldExpertModeSnapshotActivity(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	logger := util.GetLogger(ctx)

	// Get all expert mode Backups for this volume, ordered by creation time (newest first)
	recordHeartbeat(ctx, fmt.Sprintf("Fetching expert mode backups for volume %s", volume.UUID))
	backups, err := a.SE.GetExpertModeBackupsByVolumeExternalUUID(ctx, volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		logger.Errorf("Failed to get expert mode backups for volume %s: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// If we have more than 1 backup, delete the older ones (skip the first one which is the latest)
	if len(backups) > 1 {
		logger.Infof("Found %d expert mode backup snapshots for volume %s, cleaning up %d older backup snapshots",
			len(backups), volume.Name, len(backups)-1)

		// Process older backup snapshots (skip the first one which is the latest)
		for i := 1; i < len(backups); i++ {
			backup := backups[i]
			recordHeartbeat(ctx, fmt.Sprintf("Processing expert mode backup %d of %d for volume %s", i+1, len(backups), volume.UUID))

			if backup.Attributes == nil {
				logger.Warnf("Skipping backup %s for volume %s: nil Attributes", backup.Name, volume.Name)
				continue
			}
			if backup.Attributes.SnapshotID == "" {
				logger.Warnf("Skipping backup %s for volume %s: empty SnapshotID", backup.Name, volume.Name)
				continue
			}

			logger.Infof("Deleting older expert mode snapshot %s for volume %s", backup.Name, volume.Name)

			err = a.DeleteBackupSnapshot(ctx, node, backup.Attributes.SnapshotID, volume.VolumeAttributes.ExternalUUID)
			if err != nil {
				logger.Errorf("Failed to delete backup snapshot %s from ONTAP: %v", backup.Name, err)
				continue
			}
			logger.Infof("Successfully deleted older expert mode backup snapshot %s for volume %s", backup.Name, volume.Name)
		}
	} else {
		logger.Infof("No cleanup needed for volume %s - found %d expert mode backup snapshots", volume.Name, len(backups))
	}

	return nil
}

// GetVolumeProtocolsFromOntapActivity determines the protocols of an expert mode volume
// by inspecting the volume's NAS/SAN properties from ONTAP rather than SVM-level services.
func (b *BackupActivity) GetVolumeProtocolsFromOntapActivity(ctx context.Context, backupActivitiesContext *BackupActivitiesContext) (*BackupActivitiesContext, error) {
	logger := util.GetLogger(ctx)
	recordHeartbeat(ctx, "GetVolumeProtocolsFromOntapActivity in progress")

	volume := backupActivitiesContext.BackupWorkflowInit.Volume
	node := backupActivitiesContext.Node

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to get provider: %w", err)))
	}

	var svmName string
	if volume.Svm.Name == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, fmt.Errorf("volume SVM name is empty for volume %s", volume.Name)))
	} else {
		svmName = volume.Svm.Name
	}
	var protocols []string

	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP NAS details for volume %s", volume.UUID))
	nasDetails, err := provider.GetVolumeNASDetails(volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, fmt.Errorf("failed to get volume NAS details: %w", err)))
	}

	recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP SAN details for volume %s", volume.UUID))
	sanDetails, err := provider.GetVolumeSANDetails(svmName, volume.Name)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, fmt.Errorf("failed to get volume SAN details: %w", err)))
	}

	isSanVolume := sanDetails.HasLUNs || sanDetails.HasNamespaces

	if sanDetails.HasLUNs {
		protocols = append(protocols, utils.ProtocolISCSI)
	}
	if sanDetails.HasNamespaces {
		protocols = append(protocols, utils.ProtocolNVMe)
	}

	if !isSanVolume {
		if nasDetails.ExportPolicyName != "" {
			recordHeartbeat(ctx, fmt.Sprintf("Fetching ONTAP export protocols for volume %s", volume.UUID))
			rawProtocols, err := provider.GetExportPolicyProtocols(nasDetails.ExportPolicyName, svmName)
			if err != nil {
				logger.Warnf("Failed to get export policy protocols for %s: %v", nasDetails.ExportPolicyName, err)
			} else {
				protocols = append(protocols, mapAndDeduplicateProtocols(rawProtocols)...)
			}
		}

		if (nasDetails.SecurityStyle == "ntfs" || nasDetails.SecurityStyle == "mixed" || nasDetails.SecurityStyle == "unified") && !containsProtocol(protocols, utils.ProtocolSMB) {
			protocols = append(protocols, utils.ProtocolSMB)
		}
	}

	if len(protocols) == 0 {
		logger.Errorf("Could not determine protocols for expert mode volume %s: no NFS export policy, SMB security style, iSCSI LUNs, or NVMe namespaces found", volume.Name)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrExpertModeVolumeProtocolsUndetermined, fmt.Errorf("could not determine protocols for volume %s", volume.Name)),
		)
	}

	logger.Infof("Determined protocols for expert mode volume %s: %v", volume.Name, protocols)
	backupActivitiesContext.BackupWorkflowInit.Volume.VolumeAttributes.Protocols = protocols
	applyBackupAttributes(backupActivitiesContext.BackupWorkflowInit.Backup, func(attrs *datamodel.BackupAttributes) {
		attrs.Protocols = protocols
	})

	return backupActivitiesContext, nil
}

// mapAndDeduplicateProtocols takes raw ONTAP export policy protocol strings and maps them
// to our proxy constants, deduplicating the results.
func mapAndDeduplicateProtocols(rawProtocols []string) []string {
	seen := make(map[string]bool)
	var protocols []string
	for _, raw := range rawProtocols {
		for _, mapped := range mapOntapExportProtocol(raw) {
			if !seen[mapped] {
				seen[mapped] = true
				protocols = append(protocols, mapped)
			}
		}
	}
	return protocols
}

// mapOntapExportProtocol maps an ONTAP export policy rule protocol value to proxy protocol constants.
func mapOntapExportProtocol(ontapProto string) []string {
	switch ontapProto {
	case "nfs3":
		return []string{utils.ProtocolNFSv3}
	case "nfs4":
		return []string{utils.ProtocolNFSv4}
	case "nfs":
		return []string{utils.ProtocolNFSv3, utils.ProtocolNFSv4}
	case "cifs":
		return []string{utils.ProtocolSMB}
	case "any":
		return []string{utils.ProtocolNFSv3, utils.ProtocolNFSv4, utils.ProtocolSMB}
	default:
		return nil
	}
}

func containsProtocol(protocols []string, target string) bool {
	for _, p := range protocols {
		if p == target {
			return true
		}
	}
	return false
}

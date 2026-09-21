package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/replicationWorkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const (
	STORAGE_CLASS_SOFTWARE = "SOFTWARE"
)

var (
	createSnapshot                  = _createSnapshot
	updateSnapshot                  = _updateSnapshot
	getSnapshot                     = _getSnapshot
	VolumeOwnershipCheck            = _volumeOwnershipCheck
	ValidateSnapshotName            = _validateSnapshotName
	deleteSnapshot                  = _deleteSnapshot
	listSnapshots                   = _listSnapshots
	deleteSnapshots                 = _deleteSnapshots
	ConvertDatastoreSnapshotToModel = _convertDatastoreSnapshotToModel

	maxAppConsistentSnapshotCount = env.GetInt64("MAX_APP_CONSISTENT_SNAPSHOT_COUNT", 10)
)

const (
	snapshotNamePattern           = `^[\w()+.-]+$`
	snapshotNameErrorEmpty        = "Snapshot name must not be empty."
	snapshotNameError             = "Snapshot name can only include alphanumeric characters and the following special characters: ()-_+."
	snapshotNameErrorDots         = "Snapshot name cannot include consecutive dots: .."
	snapshotNameErrorSingleDot    = "Snapshot name cannot be a single dot."
	snapshotNameErrorIllegalNames = `Snapshot name cannot start with the following: "ref_ss_volmove", "snapmirror", "hourly.", "daily.", "weekly." or "monthly.".`
)

var illegalNamesRegexp = []*regexp.Regexp{
	regexp.MustCompile(`^ref_ss_volmove.*$`),
	regexp.MustCompile(`^snapmirror.*$`),
	regexp.MustCompile(`^(hourly|daily|weekly|monthly)\..*$`),
}

// CreateSnapshot creates the snapshot and adds to the specified volume belonging to the specified owner
func (o *GCPOrchestrator) CreateSnapshot(ctx context.Context, params *common.CreateSnapshotParams) (*models.Snapshot, string, error) {
	return createSnapshot(ctx, o.storage, o.temporal, params)
}

func _createSnapshot(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateSnapshotParams) (*models.Snapshot, string, error) {
	logger := util.GetLogger(ctx)

	if err := ValidateSnapshotName(nillable.GetString(&params.Name, "")); err != nil {
		logger.Errorf("Error creating snapshot: %v", err)
		return nil, "", err
	}

	account, err := se.GetAccount(ctx, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.AccountName, err)
		return nil, "", err
	}

	volume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return nil, "", err
	}

	if params.IsAppConsistent {
		appConsistentSnaps, err := se.GetAppConsistentSnapshotsForVolume(ctx, account.ID, volume.ID)
		if err != nil {
			return nil, "", err
		} else if int64(len(appConsistentSnaps)) >= maxAppConsistentSnapshotCount {
			return nil, "", customerrors.NewConflictErr(fmt.Sprintf("Volume already has the maximum number of app consistent snapshots (%d)", maxAppConsistentSnapshotCount))
		}
	}

	err = validateCreateSnapshotOperation(volume, params, account)
	if err != nil {
		return nil, "", err
	}

	// Check and return early if a snapshot with the same name is already in creation for this volume and account
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("name", "=", params.Name),
		utils2.NewFilterCondition("account_id", "=", account.ID),
		utils2.NewFilterCondition("volume_id", "=", volume.ID))
	existingSnapshots, err := se.GetSnapshotsWithCondition(ctx, *filter)
	if err != nil {
		logger.Errorf("Failed to get snapshots with conditions: %v. Error: %v", filter, err)
		return nil, "", err
	}

	if len(existingSnapshots) > 0 {
		filter := utils2.CreateFilterWithConditions(
			utils2.NewFilterCondition("resource_name", "=", params.Name),
			utils2.NewFilterCondition("account_id", "=", account.ID),
			utils2.NewFilterCondition("type", "=", string(datamodel.JobTypeCreateSnapshot)),
			utils2.NewFilterCondition("state", "!=", string(datamodel.JobsStateDONE)),
			utils2.NewFilterCondition("state", "!=", string(datamodel.JobsStateERROR)),
			utils2.NewFilterCondition("job_attributes ->> 'volume_uuid'", "=", volume.UUID))

		jobs, err := se.GetJobsWithCondition(ctx, *filter)
		if err != nil {
			logger.Errorf("Failed to get jobs with conditions: %v. Error: %v", filter, err)
			return nil, "", err
		}
		if len(jobs) > 0 {
			for _, job := range jobs {
				for _, snapshot := range existingSnapshots {
					if snapshot.Name == job.ResourceName {
						if snapshot.State == datamodel.LifeCycleStateREADY {
							logger.Warnf("Snapshot with name %s already exists", snapshot.Name)
							return nil, "", customerrors.NewConflictErr("snapshot already exists")
						} else {
							logger.Infof("Found ongoing snapshot creation job for account %s with name %s. Job UUID: %s", params.AccountName, params.Name, job.UUID)
							dataStoreSnap := ConvertDatastoreSnapshotToModel(snapshot)
							return dataStoreSnap, job.UUID, nil
						}
					}
				}
			}
		}
	}

	var dbSnapshot *datamodel.Snapshot
	var job *datamodel.Job
	snapshotAPISyncMode := env.GetBool("SNAPSHOT_API_SYNC_MODE", false)
	// Cleanup in case of error
	defer func() {
		if err != nil {
			if dbSnapshot != nil && dbSnapshot.UUID != "" {
				logger.Warnf("Error occurred, marking snapshot as ERROR. Snapshot UUID: %s", dbSnapshot.UUID)
				now := time.Now()
				dbSnapshot.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
				dbSnapshot.State = datamodel.LifeCycleStateError
				dbSnapshot.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
				if _, updateErr := se.UpdateSnapshot(ctx, dbSnapshot); updateErr != nil {
					logger.Errorf("Failed to mark snapshot as ERROR: %v", updateErr)
				}
			}
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
		}
	}()

	snapshot := &datamodel.Snapshot{
		Name:               params.Name,
		Description:        params.Description,
		VolumeID:           volume.ID,
		AccountID:          account.ID,
		Volume:             volume,
		Account:            account,
		IsAppConsistent:    params.IsAppConsistent,
		Type:               backgroundactivities.SnapshotTypeAdHoc,
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}

	dbSnapshot, err = se.CreatingSnapshot(ctx, snapshot)
	if err != nil {
		logger.Errorf("Failed to create snapshot in database. Error: %v", err)
		return nil, "", err
	}

	if snapshotAPISyncMode {
		var asyncSnapshot *models.Snapshot
		asyncSnapshot, err = createSnapshotSync(ctx, se, dbSnapshot, params, logger)
		if err != nil {
			return nil, "", err
		}
		return asyncSnapshot, "", nil
	}

	job = &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateSnapshot),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbSnapshot.UUID, // Storing the snapshot UUID
			VolumeUUID:   volume.UUID,     // Storing the volume UUID for idempotency check
		},
	}

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, "", err
	}

	// Async mode - use workflow
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	createSnapshotTimeout := workflowengine.GetCreateSnapshotWorkflowTimeout()
	err = workflowExecutor.ExecuteWorkflowWithRetry(
		ctx,
		job.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.CreateSnapshotWorkflow,
		createSnapshotTimeout,
		nil, // Use default WorkflowIDReusePolicy (REJECT_DUPLICATE)
		params,
		dbSnapshot,
	)
	if err != nil {
		logger.Errorf("Failed to start create snapshot workflow after retries. Error: %v ", err)
		return nil, "", err
	}

	dataStoreSnap := ConvertDatastoreSnapshotToModel(dbSnapshot)
	return dataStoreSnap, job.UUID, nil
}

func (o *GCPOrchestrator) GetSnapshot(ctx context.Context, params *common.GetSnapshotParams) (*models.Snapshot, error) {
	return getSnapshot(ctx, o.storage, params)
}

func _getSnapshot(ctx context.Context, se database.Storage, params *common.GetSnapshotParams) (*models.Snapshot, error) {
	logger := util.GetLogger(ctx)

	volume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return nil, err
	}

	snapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotUUID, volume.Account.ID, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get snapshot: %s. Error: %v", params.SnapshotUUID, err)
		return nil, err
	}

	dataStoreSnap := ConvertDatastoreSnapshotToModel(snapshot)
	return dataStoreSnap, nil
}

func (o *GCPOrchestrator) ListSnapshots(ctx context.Context, params *common.ListSnapshotsParams) ([]*models.Snapshot, error) {
	return listSnapshots(ctx, o.storage, params)
}

func _listSnapshots(ctx context.Context, se database.Storage, params *common.ListSnapshotsParams) ([]*models.Snapshot, error) {
	logger := util.GetLogger(ctx)

	volume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return nil, err
	}

	snapshots, err := se.GetSnapshotsByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get snapshots for volume: %s. Error: %v", params.VolumeID, err)
		return nil, err
	}

	var snapshotsToReturn []*models.Snapshot
	for _, snapshot := range snapshots {
		snapshotsToReturn = append(snapshotsToReturn, ConvertDatastoreSnapshotToModel(snapshot))
	}
	return snapshotsToReturn, nil
}

func (o *GCPOrchestrator) GetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*models.Snapshot, error) {
	dbSnaps, err := o.storage.BatchGetSnapshotsByUUIDs(ctx, snapshotUUIDs)
	if err != nil {
		return nil, err
	}
	out := make([]*models.Snapshot, len(dbSnaps))
	for i, s := range dbSnaps {
		out[i] = ConvertDatastoreSnapshotToModel(s)
	}
	return out, nil
}

func (o *GCPOrchestrator) GetMultipleSnapshots(ctx context.Context, volumeUuid string, accountName string, snapshotUUIDs []string) ([]*models.Snapshot, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			util.GetLogger(ctx).Warnf("Account with name %s not found in VCP, checking in CVP", accountName)
			return []*models.Snapshot{}, nil
		}
		return nil, err
	}

	volume, err := se.GetVolumeWithAccountID(ctx, volumeUuid, account.ID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			util.GetLogger(ctx).Warnf("Volume with uuid %s not found in VCP, checking in CVP", volumeUuid)
			return []*models.Snapshot{}, nil
		}
		return nil, err
	}

	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", account.ID),
		utils2.NewFilterCondition("volume_id", "=", volume.ID),
		utils2.NewFilterCondition("uuid", "in", snapshotUUIDs))

	dbSnapshots, err := se.GetSnapshotsWithCondition(ctx, *filter)
	if err != nil {
		return nil, err
	}

	modelSnapshots := make([]*models.Snapshot, len(dbSnapshots))
	for i, snapshot := range dbSnapshots {
		modelSnapshots[i] = ConvertDatastoreSnapshotToModel(snapshot)
	}
	return modelSnapshots, nil
}

// createSnapshotSync creates snapshot synchronously without using workflow
func createSnapshotSync(ctx context.Context, se database.Storage, dbSnapshot *datamodel.Snapshot, params *common.CreateSnapshotParams, logger log.Logger) (*models.Snapshot, error) {
	logger.Infof("Starting snapshot sync for volume %s", params.VolumeID)
	snapshotDescription := dbSnapshot.Description

	// Get provider using fast connection
	provider, err := backgroundactivities.GetOntapRestProviderForPoolFastConn(ctx, se, dbSnapshot.Volume.Pool)
	if err != nil {
		logger.Errorf("Failed to get ONTAP REST provider: %v", err)
		return nil, err
	}

	// Create snapshot synchronously with direct polling
	snapshotCreateResponse, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, logger)
	if err != nil {
		logger.Errorf("Failed to create snapshot in ONTAP: %v", err)
		return nil, err
	}

	// Restore original description
	dbSnapshot.Description = snapshotDescription

	// Update snapshot details in database
	if snapshotCreateResponse == nil {
		logger.Errorf("Snapshot create response is nil")
		return nil, fmt.Errorf("snapshot create response is nil")
	}

	dbSnapshot.State = datamodel.LifeCycleStateREADY
	dbSnapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails
	dbSnapshot.SetExternalUUID(snapshotCreateResponse.ExternalUUID)
	dbSnapshot.SnapshotAttributes.SizeInBytes = snapshotCreateResponse.SizeInBytes
	dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes = snapshotCreateResponse.LogicalSizeInBytes

	var updatedSnapshot *datamodel.Snapshot
	err = se.WithTransaction(ctx, func(tx utils2.Transaction) error {
		txDB := tx.GORM().WithContext(ctx)

		// Update snapshot
		dbSnapshotFromDB := &datamodel.Snapshot{}
		err := txDB.Preload("Account").Preload("Volume").
			First(dbSnapshotFromDB, &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: dbSnapshot.UUID}}).Error
		if err != nil {
			if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
				return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("snapshot", &dbSnapshot.UUID))
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}

		err = txDB.Model(&dbSnapshotFromDB).Updates(datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				DeletedAt: dbSnapshot.DeletedAt,
				UpdatedAt: time.Now(),
			},
			Name:               dbSnapshot.Name,
			Description:        dbSnapshot.Description,
			ExternalUUID:       dbSnapshot.ExternalUUID,
			SnapshotAttributes: dbSnapshot.SnapshotAttributes,
			State:              dbSnapshot.State,
			StateDetails:       dbSnapshot.StateDetails,
		}).Error
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}
		updatedSnapshot = dbSnapshotFromDB

		return nil
	})
	if err != nil {
		logger.Errorf("Failed to update snapshot in transaction: %v", err)
		return nil, err
	}

	dataStoreSnap := ConvertDatastoreSnapshotToModel(updatedSnapshot)
	return dataStoreSnap, nil
}

// createSnapshotSyncWithDirectPolling creates snapshot and polls job directly without workflow
func createSnapshotSyncWithDirectPolling(ctx context.Context, provider vsa.Provider, dbSnapshot *datamodel.Snapshot, logger log.Logger) (*vsa.SnapshotProviderResponse, error) {
	// Get the REST client from provider
	ontapProvider, ok := provider.(*vsa.OntapRestProvider)
	if !ok {
		return nil, fmt.Errorf("provider is not OntapRestProvider")
	}

	client, err := ontapProvider.CreateRESTClient()
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrONTAPClientCreationError, err)
	}

	// Create snapshot in ONTAP
	snapshot, job, err := client.Storage().SnapshotCreate(&ontapRest.SnapshotCreateParams{
		VolumeUUID: dbSnapshot.Volume.VolumeAttributes.ExternalUUID,
		Name:       dbSnapshot.Name,
		Comment:    nillable.ToPointer(dbSnapshot.Description),
	})
	if err != nil {
		if customerrors.IsConflictErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrCreateSnapshotConflict, err)
		}
		if customerrors.IsBadRequestErr(err) || strings.Contains(err.Error(), "Snapshots can only be created on read/write") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotNotAllowedForVolume, fmt.Errorf("snapshot creation operation not allowed for this volume"))
		}
		if strings.Contains(err.Error(), "No space left on device") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotInsufficientSpace, err)
		}
		if strings.Contains(err.Error(), "Cannot exceed maximum number of snapshots") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotMaximumLimitExceeded, err)
		}
		if !customerrors.IsNotFoundErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, customerrors.NewNotFoundErr("Volume", nil))
	}

	// If snapshot UUID is nil, or size/logical_size is missing, we need to poll for the job to complete
	if snapshot == nil || snapshot.UUID == nil || snapshot.Size == nil || snapshot.LogicalSize == nil {
		if job != nil {
			// Get polling timeout and interval from environment variables (in milliseconds)
			pollTimeoutMilliseconds := env.GetInt("SYNC_SNAPSHOT_ONTAP_JOB_POLL_TIMEOUT_MILLISECONDS", 40000)
			pollIntervalMilliseconds := env.GetInt("SYNC_SNAPSHOT_ONTAP_JOB_POLL_INTERVAL_MILLISECONDS", 2000)
			err = ontapRest.PollOntapJobDirectly(ctx, client, job.JobUUID, time.Duration(pollTimeoutMilliseconds)*time.Millisecond, time.Duration(pollIntervalMilliseconds)*time.Millisecond, logger)
			if err != nil {
				if strings.Contains(err.Error(), "Snapshots can only be created on read/write") {
					return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotNotAllowedForVolume, fmt.Errorf("snapshot creation operation not allowed for this volume"))
				}
				if strings.Contains(err.Error(), "No space left on device") {
					return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotInsufficientSpace, err)
				}
				if strings.Contains(err.Error(), "Cannot exceed maximum number of snapshots") {
					return nil, vsaerrors.NewVCPError(vsaerrors.ErrSnapshotMaximumLimitExceeded, err)
				}
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
			}
			// After polling completes, get snapshot details using the resource UUID from job
			snapshotUUID := job.ResourceUUID
			if snapshotUUID == "" {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: job resource UUID is empty after polling"))
			}
			// Get snapshot details
			snapshot, err = client.Storage().SnapshotGet(&ontapRest.SnapshotGetParams{
				BaseParams: ontapRest.BaseParams{Fields: []string{
					"size",
					"logical_size",
				}},
				UUID:       snapshotUUID,
				VolumeUUID: dbSnapshot.Volume.VolumeAttributes.ExternalUUID,
			})
			if err != nil {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, err)
			}
		} else {
			// If snapshot is missing Size or LogicalSize but no job is provided, we can't poll to get the details
			if snapshot != nil && snapshot.UUID != nil && (snapshot.Size == nil || snapshot.LogicalSize == nil) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: snapshot size or logical_size is missing and no job provided for polling"))
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: snapshot is nil and no job provided"))
		}
	}

	// Validate the Snapshot response
	if snapshot == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: snapshot is nil"))
	}
	if snapshot.Name == nil || snapshot.UUID == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: missing required fields"))
	}
	if snapshot.Size == nil || snapshot.LogicalSize == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrOntapRestAPIError, customerrors.NewBadRequestErr("invalid Snapshot create response from API: missing size or logical_size"))
	}

	return &vsa.SnapshotProviderResponse{
		ProviderResponse: vsa.ProviderResponse{
			Name:         *snapshot.Name,
			ExternalUUID: *snapshot.UUID,
		},
		SizeInBytes:        int64(0),
		LogicalSizeInBytes: *snapshot.LogicalSize,
	}, nil
}

func (o *GCPOrchestrator) UpdateSnapshot(ctx context.Context, params *common.UpdateSnapshotParams) (*models.Snapshot, string, error) {
	return updateSnapshot(ctx, o.storage, o.temporal, params)
}

func _updateSnapshot(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateSnapshotParams) (*models.Snapshot, string, error) {
	logger := util.GetLogger(ctx)

	account, err := se.GetAccount(ctx, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.AccountName, err)
		return nil, "", err
	}

	volume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return nil, "", err
	}

	snapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotUUID, account.ID, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get snapshot: %s. Error: %v", params.SnapshotUUID, err)
		return nil, "", err
	}

	if snapshot.State == datamodel.LifeCycleStateCreating || snapshot.State == datamodel.LifeCycleStateUpdating || snapshot.State == datamodel.LifeCycleStateDeleting {
		logger.Errorf("Snapshot %s cannot be update, while in transitioning state: %s", params.SnapshotUUID, snapshot.State)
		return nil, "", customerrors.NewConflictErr("Snapshot is in transition state and cannot be updated, state: " + snapshot.State)
	}
	snapshot.Description = params.Description // Only snapshot description is allowed to be updated in GCNV
	updatedSnapshot, err := se.UpdateSnapshot(ctx, snapshot)
	if err != nil {
		logger.Errorf("Failed to update snapshot: %s. Error: %v", params.SnapshotUUID, err)
		return nil, "", err
	}

	dataStoreSnap := ConvertDatastoreSnapshotToModel(updatedSnapshot)
	return dataStoreSnap, "", nil
}

func _convertDatastoreSnapshotToModel(snapshot *datamodel.Snapshot) *models.Snapshot {
	if snapshot == nil {
		return nil
	}

	res := &models.Snapshot{
		BaseModel: models.BaseModel{
			UUID:      snapshot.UUID,
			CreatedAt: snapshot.CreatedAt,
			UpdatedAt: snapshot.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(snapshot.DeletedAt),
		},
		Name:                  snapshot.Name,
		Description:           snapshot.Description,
		LifeCycleState:        snapshot.State,
		LifeCycleStateDetails: snapshot.StateDetails,
		VolumeUUID:            snapshot.Volume.UUID,
		VolumeName:            snapshot.Volume.Name,
		SizeInBytes:           uint64(snapshot.SnapshotAttributes.SizeInBytes),
		StorageClass:          STORAGE_CLASS_SOFTWARE,
		IsAppConsistent:       snapshot.IsAppConsistent,
	}
	return res
}

func validateCreateSnapshotOperation(volume *datamodel.Volume, params *common.CreateSnapshotParams, account *datamodel.Account) error {
	if params.Name == "" {
		return customerrors.NewUserInputValidationErr("Snapshot name is empty. Please provide a valid name.")
	}

	if volume.State == datamodel.LifeCycleStateCreating {
		return customerrors.NewConflictErr("Cannot create a snapshot when volume is in creating stage.")
	}
	if volume.State == datamodel.LifeCycleStateDeleting {
		return customerrors.NewConflictErr("Cannot create a snapshot when volume is in deleting stage.")
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		cloneState := volume.VolumeAttributes.CloneParentInfo.State
		if cloneState == datamodel.CloneStateSplitting {
			return customerrors.NewConflictErr("Cannot create a snapshot when volume is undergoing split operation.")
		}
	}

	// @TODO: Include DataProtection check when implemented

	return nil
}

// DeleteSnapshot deletes the specified snapshot
func (o *GCPOrchestrator) DeleteSnapshot(ctx context.Context, params *common.DeleteSnapshotParams) (*models.Snapshot, string, error) {
	return deleteSnapshot(ctx, o.storage, o.temporal, params)
}

// DeleteSnapshot deletes the specified snapshot from the specified volume belonging to the specified owner
func _deleteSnapshot(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteSnapshotParams) (*models.Snapshot, string, error) {
	logger := util.GetLogger(ctx)

	volume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return nil, "", err
	}

	if volume.State == datamodel.LifeCycleStateDeleting {
		return nil, "", customerrors.NewConflictErr("Volume of the snapshot is being deleted")
	}

	if volume.VolumeAttributes != nil && volume.VolumeAttributes.CloneParentInfo != nil {
		if volume.VolumeAttributes.CloneParentInfo.State == datamodel.CloneStateSplitting {
			return nil, "", customerrors.NewConflictErr("Snapshot deletion is not allowed when the volume is splitting")
		}
	}

	snapshot, err := se.GetSnapshotByUUID(ctx, params.SnapshotID, volume.Account.ID, volume.ID)
	if err != nil {
		return nil, "", err
	}

	if snapshot.Type == activities.SnapshotTypeBackup {
		return nil, "", customerrors.NewConflictErr("Cannot delete a snapshot that was generated for backups. This snapshot will be automatically deleted when the next backup is created.")
	}

	var existingDeleteJobUUID string
	snapshot.Volume = volume
	correlationID := utils.GetCoRelationIDFromContext(ctx)
	if snapshot.State == datamodel.LifeCycleStateCreating {
		existingDeleteJobUUID, _, err = database.ValidateCorrelationIDForCreatingResource(ctx, se, snapshot.UUID, "snapshot", datamodel.JobTypeCreateSnapshot, datamodel.JobTypeDeleteSnapshot, logger)
		if err != nil {
			logger.Warnf("Snapshot %s cannot be deleted: existing create job not present and state is in CREATING", snapshot.UUID)
			return nil, "", err
		}
		if existingDeleteJobUUID != "" {
			return ConvertDatastoreSnapshotToModel(snapshot), existingDeleteJobUUID, nil
		}
		logger.Infof("Create job found for snapshot %s with matching correlation ID, skipping state update to DELETING", snapshot.UUID)
	} else if utils.IsTransitionalState(snapshot.State) && snapshot.State != datamodel.LifeCycleStateDeleting {
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("Snapshot is in transition state and cannot be deleted, state: %s", snapshot.State))
	}

	existingJobUUID := database.GetExistingDeleteJobForDeletingState(ctx, se, snapshot.UUID, datamodel.JobTypeDeleteSnapshot, logger)
	if existingJobUUID != "" {
		return ConvertDatastoreSnapshotToModel(snapshot), existingJobUUID, nil
	}

	previousState := snapshot.State
	previousStateDetails := snapshot.StateDetails
	accountID := snapshot.AccountID
	if snapshot.Account != nil {
		accountID = snapshot.Account.ID
	}
	poolUUID := ""
	if volume.Pool != nil {
		poolUUID = volume.Pool.UUID
	}
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteSnapshot),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  snapshot.Name,
		AccountID:     sql.NullInt64{Int64: accountID, Valid: true},
		CorrelationID: correlationID,
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         snapshot.UUID, // Storing the snapshot UUID
			VolumeUUID:           volume.UUID,   // Storing the volume UUID for idempotency check
			PoolUUID:             poolUUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
			PayloadAttributes:    map[string]interface{}{"account_id": accountID, "volume_id": volume.ID},
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			// Revert snapshot state back to READY
			logger.Warnf("Error occurred during snapshot deletion, reverting snapshot state to READY. Snapshot UUID: %s", snapshot.UUID)
			snapshot.State = datamodel.LifeCycleStateREADY
			snapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails
			if _, updateErr := se.UpdateSnapshot(ctx, snapshot); updateErr != nil {
				logger.Errorf("Failed to revert snapshot state to READY: %v", updateErr)
			}
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create snapshot delete job in database %v", err)
		return nil, "", err
	}

	// Only update state to DELETING if snapshot is not in CREATING state (cancellation will be handled in workflow)
	if snapshot.State != datamodel.LifeCycleStateCreating {
		if err = se.DeletingSnapshot(ctx, snapshot); err != nil {
			return nil, "", err
		}
	}

	location, err := utils.GetLocationFromVendorID(volume.Pool.VendorID)
	if err != nil {
		logger.Error("Failed to get location from vendor ID: ", "error", err)
		return nil, "", err
	}

	// controlWorkflowID defines the workflow ID for the control workflow
	controlWorkflowID := workflows.GenerateControlWorkflowID(volume.Account.ID, location, volume.Pool.Name)
	workflowOptions := workflows.DefaultSequentialWorkflowOptions(controlWorkflowID, job.WorkflowID)
	deleteSnapshotTimeout := workflowengine.GetDeleteSnapshotWorkflowTimeout()
	workflowOptions.WorkflowRunTimeout = deleteSnapshotTimeout
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteSequentialWorkflow(
		ctx,
		workflowOptions,
		workflows.DeleteSnapshotWorkflow,
		params,
		snapshot,
	)
	if err != nil {
		logger.Error("Failed to start delete snapshot workflow after retries: ", "error", err)
		return nil, "", err
	}

	return ConvertDatastoreSnapshotToModel(snapshot), job.UUID, nil
}

func _volumeOwnershipCheck(ctx context.Context, se database.Storage, volumeUUID string, accountName string) (*datamodel.Volume, error) {
	logger := util.GetLogger(ctx)

	volume, err := se.VerifyVolumeOwnership(ctx, volumeUUID, accountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Errorf("Volume %s not found for account %s", volumeUUID, accountName)
			return nil, customerrors.NewUserInputValidationErr("Volume not found")
		}
		logger.Errorf("Failed to verify volume ownership: %v", err)
		return nil, customerrors.NewUserInputValidationErr("Volume not found. Please ensure the volume exists and belongs to your account.")
	}

	return volume, nil
}

func _validateSnapshotName(name string) error {
	if name == "" {
		return customerrors.NewUserInputValidationErr(snapshotNameErrorEmpty)
	}
	if match, _ := regexp.MatchString(snapshotNamePattern, name); !match {
		return customerrors.NewUserInputValidationErr(snapshotNameError)
	}
	if strings.Contains(name, "..") {
		return customerrors.NewUserInputValidationErr(snapshotNameErrorDots)
	}
	if name == "." {
		return customerrors.NewUserInputValidationErr(snapshotNameErrorSingleDot)
	}
	for _, reg := range illegalNamesRegexp {
		if reg.MatchString(name) {
			return customerrors.NewUserInputValidationErr(snapshotNameErrorIllegalNames)
		}
	}
	return nil
}

// DeleteSnapmirrorSnapshots deletes the snapmirror snapshots for the specified volume belonging to the specified owner
func (o *GCPOrchestrator) DeleteSnapmirrorSnapshots(ctx context.Context, params *common.SnapshotsInternalDeleteParams) (string, error) {
	return deleteSnapshots(ctx, o.storage, o.temporal, params)
}

// DeleteSnapshot deletes the specified snapshot from the specified volume belonging to the specified owner
func _deleteSnapshots(ctx context.Context, se database.Storage, temporal client.Client, params *common.SnapshotsInternalDeleteParams) (string, error) {
	logger := util.GetLogger(ctx)
	dbVolume, err := VolumeOwnershipCheck(ctx, se, params.VolumeID, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to validate volume ownership")
		return "", err
	}
	if dbVolume.State == datamodel.LifeCycleStateRetained {
		return "", customerrors.NewNotFoundErr("Volume", nil)
	}
	if dbVolume.State == datamodel.LifeCycleStateDeleting {
		return "", customerrors.NewConflictErr("Volume of the snapshot is being deleted.")
	}
	if dbVolume.State == datamodel.VolumeStateOffline {
		return "", customerrors.NewConflictErr("Volume is offline.")
	}

	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteSnapmirrorSnapshotsInternal),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  dbVolume.Name,
		AccountID:     sql.NullInt64{Int64: dbVolume.Account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbVolume.UUID,
			PoolUUID:     dbVolume.Pool.UUID,
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create snapshot delete job in database %v", err)
		return "", err
	}

	params.Volume = dbVolume
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflowWithRetry(
		ctx,
		job.WorkflowID,
		workflowengine.CustomerTaskQueue,
		replicationWorkflows.DeleteInternalSnapshotWorkflow,
		nil, // workflowRunTimeout
		nil, // Use default WorkflowIDReusePolicy (REJECT_DUPLICATE)
		params,
	)
	if err != nil {
		logger.Error("Failed to start delete snapshot workflow after retries: ", "error", err)
		return "", err
	}

	return job.UUID, nil
}

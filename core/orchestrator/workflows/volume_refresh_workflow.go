package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	VolumeRefreshWorkflowActivityTimeout = env.GetString("VOLUME_REFRESH_WORKFLOW_START_TO_CLOSE_TIMEOUT", "10m")
)

// VolumeRefreshWorkflowInput is the lightweight workflow payload passed to Temporal.
type VolumeRefreshWorkflowInput struct {
	AccountUUID string
	AccountName string
	AccountID   int64
	VolumeIDs   []string
}

type volumeMetricHydrationWorkflow struct {
	BaseWorkflow
	CompletionTime time.Time
}

// Enforcing the WorkflowInterface on volumeMetricHydrationWorkflow
var _ WorkflowInterface = &volumeMetricHydrationWorkflow{}

// VolumeRefreshWorkflow updates the volume fields by fetching the volume details from ONTAP
func VolumeRefreshWorkflow(ctx workflow.Context, input VolumeRefreshWorkflowInput) error {
	log := workflow.GetLogger(ctx)
	if len(input.VolumeIDs) == 0 {
		log.Error("VolumeRefreshWorkflow called with empty volume IDs")
		return temporal.NewNonRetryableApplicationError("no volumes provided", "VolumeRefreshWorkflowError", nil)
	}
	if input.AccountName == "" {
		log.Error("VolumeRefreshWorkflow called with empty account name")
		return temporal.NewNonRetryableApplicationError("account name is required", "VolumeRefreshWorkflowError", nil)
	}
	if input.AccountID == 0 {
		log.Error("VolumeRefreshWorkflow called with zero account ID")
		return temporal.NewNonRetryableApplicationError("account ID is required", "VolumeRefreshWorkflowError", nil)
	}

	volumeWf := new(volumeMetricHydrationWorkflow)
	err := volumeWf.Setup(ctx, input.AccountName)
	if err != nil {
		log.Error("Failed to setup VolumeRefreshWorkflow", "error", err)
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	_, customErr := volumeWf.Run(ctx, input)
	if customErr != nil {
		log.Error("Failed to run VolumeRefreshWorkflow", "error", customErr)
		volumeWf.Status = WorkflowStatusFailed
		volumeWf.CompletionTime = workflow.Now(ctx)
		return customErr
	}
	volumeWf.Status = WorkflowStatusCompleted
	volumeWf.CompletionTime = workflow.Now(ctx)
	return err
}

type VolumeRefreshWorkflowStatus struct {
	WorkflowStatus *WorkflowStatus
	CompletionTime *time.Time
}

func (wf *volumeMetricHydrationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	accountName := input.(string)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = accountName
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*VolumeRefreshWorkflowStatus, error) {
		return &VolumeRefreshWorkflowStatus{
			WorkflowStatus: &WorkflowStatus{
				ID:         wf.ID,
				Status:     wf.Status,
				CustomerID: wf.CustomerID,
			},
			CompletionTime: &wf.CompletionTime,
		}, nil
	})
}

func (wf *volumeMetricHydrationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	input := args[0].(VolumeRefreshWorkflowInput)
	volumeRefreshActivity := &activities.VolumeRefreshActivity{}
	// Setup activity options
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// Override the default StartToCloseTimeout
	startToCloseTimeout, parseErr := time.ParseDuration(VolumeRefreshWorkflowActivityTimeout)
	if parseErr != nil {
		log.Error("Failed to parse VOLUME_REFRESH_WORKFLOW_START_TO_CLOSE_TIMEOUT, using default of 10m", "timeout", VolumeRefreshWorkflowActivityTimeout, "error", parseErr)
		startToCloseTimeout = 10 * time.Minute
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: startToCloseTimeout,
		HeartbeatTimeout:    time.Duration(volumeHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var dbVolumes []*datamodel.Volume
	err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.GetVolumesForRefresh,
		&activities.GetVolumesForRefreshInput{
			AccountID: input.AccountID,
			VolumeIDs: input.VolumeIDs,
		}).Get(ctx, &dbVolumes)
	if err != nil {
		log.Error("GetVolumesForRefresh activity failed", "error", err)
		return nil, ConvertToVSAError(err)
	}
	if len(dbVolumes) == 0 {
		log.Warn("No volumes loaded for refresh")
		return nil, nil
	}

	// Step 1: Process volume pool mapping
	log.Info("Starting volume pool mapping processing")
	var poolMappingResult *activities.ProcessVolumePoolMappingResult
	err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.ProcessVolumePoolMapping,
		&activities.ProcessVolumePoolMappingInput{Volumes: dbVolumes}).Get(ctx, &poolMappingResult)
	if err != nil {
		log.Error("ProcessVolumePoolMapping activity failed", "error", err)
		return nil, ConvertToVSAError(err)
	}

	if len(poolMappingResult.PoolByUUID) == 0 {
		log.Warn("No valid pools found for volume refresh")
		return nil, nil
	}

	// Step 2: Get ONTAP volumes for all pools in parallel
	log.Debug("Fetching ONTAP volumes for pools in parallel", "poolCount", len(poolMappingResult.PoolByUUID))
	futures := make([]workflow.Future, 0, len(poolMappingResult.PoolByUUID))
	for _, poolUUID := range poolMappingResult.PoolUUIDs {
		pool := poolMappingResult.PoolByUUID[poolUUID]
		log.Debug("Scheduling GetOntapVolumes for pool", "name", pool.Name)
		future := workflow.ExecuteActivity(ctx, volumeRefreshActivity.GetOntapVolumes, pool)
		futures = append(futures, future)
	}

	// Step 3: Wait for all ONTAP volume fetches to complete
	ontapVolumesResults := make(map[string]*activities.GetOntapVolumesReturnValue)
	for i, future := range futures {
		var ontapVols *activities.GetOntapVolumesReturnValue
		futErr := future.Get(ctx, &ontapVols)
		if futErr != nil {
			poolUUID := poolMappingResult.PoolUUIDs[i]
			log.Error("GetOntapVolumes activity failed, skipping this pool", "pool_uuid", poolUUID, "error", futErr)
			continue
		}
		ontapVolumesResults[poolMappingResult.PoolUUIDs[i]] = ontapVols
		log.Debug("Successfully retrieved ONTAP volumes for pool", "poolUUID", poolMappingResult.PoolUUIDs[i])
	}

	// Step 4: Process volume matching and prepare updates
	log.Debug("Processing ONTAP volume matching and updates")
	var matchingResult *activities.ProcessOntapVolumeMatchingResult
	err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.ProcessOntapVolumeMatching,
		&activities.ProcessOntapVolumeMatchingInput{
			DbVolumes:           dbVolumes,
			OntapVolumesResults: ontapVolumesResults,
		}).Get(ctx, &matchingResult)
	if err != nil {
		log.Error("ProcessOntapVolumeMatching activity failed", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// Log processing summary
	log.Debug("Volume processing completed: matched, not found in ONTAP", "matchedCount", matchingResult.MatchedCount, "notFoundCount", matchingResult.NotFoundCount)

	if matchingResult.NotFoundCount > 0 {
		log.Warn("volumes were not found in ONTAP and will not be updated", "notFoundCount", matchingResult.NotFoundCount)
	}

	// Step 5: Sync updates to database if we have volumes to update
	if len(matchingResult.UpdatedVolumeByUUID) > 0 {
		log.Debug("Syncing volume updates to database", "updatedVolumeCount", len(matchingResult.UpdatedVolumeByUUID))
		err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.SyncUpdatedVolumesToDatabase,
			&activities.SyncUpdatedVolumesInput{
				UpdatedVolumeByUUID:               matchingResult.UpdatedVolumeByUUID,
				VolumesWithClonesSharedBytesReset: matchingResult.VolumesWithClonesSharedBytesReset,
			}).Get(ctx, nil)
		if err != nil {
			log.Error("SyncUpdatedVolumesToDatabase activity failed", "error", err)
			return nil, vsaerrors.ExtractCustomError(err)
		}
		log.Debug("Database sync completed successfully")
	} else {
		log.Debug("No volumes to update in database")
	}

	// Step 6: Update account metadata with workflow completion timestamp
	if input.AccountUUID != "" {
		accountUUID := input.AccountUUID
		completionTime := workflow.Now(ctx)

		log.Info("Updating account VolumeRefreshWorkflow completion timestamp to", "accountUUID", accountUUID, "completionTime", completionTime)

		err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.UpdateAccountVolumeRefreshTimestamp,
			&activities.UpdateAccountVolumeRefreshTimestampInput{
				AccountUUID: accountUUID,
				CompletedAt: completionTime,
			}).Get(ctx, nil)
		if err != nil {
			// Log error but don't fail the workflow - this is a metadata update
			log.Error("Failed to update account volume refresh timestamp", "error", err)
		} else {
			log.Info("Successfully updated account volume refresh timestamp for account", "accountUUID", accountUUID)
		}
	}

	return nil, nil
}

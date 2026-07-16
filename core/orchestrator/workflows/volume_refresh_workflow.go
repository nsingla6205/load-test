package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
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
	log := util.GetLogger(ctx)
	if len(input.VolumeIDs) == 0 {
		log.Errorf("VolumeRefreshWorkflow called with empty volume IDs")
		return temporal.NewNonRetryableApplicationError("no volumes provided", "VolumeRefreshWorkflowError", nil)
	}
	if input.AccountName == "" {
		log.Errorf("VolumeRefreshWorkflow called with empty account name")
		return temporal.NewNonRetryableApplicationError("account name is required", "VolumeRefreshWorkflowError", nil)
	}
	if input.AccountID == 0 {
		log.Errorf("VolumeRefreshWorkflow called with zero account ID")
		return temporal.NewNonRetryableApplicationError("account ID is required", "VolumeRefreshWorkflowError", nil)
	}

	volumeWf := new(volumeMetricHydrationWorkflow)
	err := volumeWf.Setup(ctx, input.AccountName)
	if err != nil {
		log.Errorf("Failed to setup VolumeRefreshWorkflow: %v", err)
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	_, customErr := volumeWf.Run(ctx, input)
	if customErr != nil {
		log.Errorf("Failed to run VolumeRefreshWorkflow: %v", customErr)
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
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

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
	log := util.GetLogger(ctx)
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
		log.Errorf("Failed to parse VOLUME_REFRESH_WORKFLOW_START_TO_CLOSE_TIMEOUT '%s', using default of 10m: %v",
			VolumeRefreshWorkflowActivityTimeout, parseErr)
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
		log.Errorf("GetVolumesForRefresh activity failed: %v", err)
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
		log.Errorf("ProcessVolumePoolMapping activity failed: %v", err)
		return nil, ConvertToVSAError(err)
	}

	if len(poolMappingResult.PoolByUUID) == 0 {
		log.Warn("No valid pools found for volume refresh")
		return nil, nil
	}

	// Step 2: Get ONTAP volumes for all pools in parallel
	log.Debugf("Fetching ONTAP volumes for %d pools in parallel", len(poolMappingResult.PoolByUUID))
	futures := make([]workflow.Future, 0, len(poolMappingResult.PoolByUUID))
	for _, poolUUID := range poolMappingResult.PoolUUIDs {
		pool := poolMappingResult.PoolByUUID[poolUUID]
		log.Debugf("Scheduling GetOntapVolumes for pool: %s", pool.Name)
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
			log.Errorf("GetOntapVolumes activity failed for pool %s: %v, skipping this pool", poolUUID, futErr)
			continue
		}
		ontapVolumesResults[poolMappingResult.PoolUUIDs[i]] = ontapVols
		log.Debugf("Successfully retrieved ONTAP volumes for pool %s", poolMappingResult.PoolUUIDs[i])
	}

	// Step 4: Process volume matching and prepare updates
	log.Debugf("Processing ONTAP volume matching and updates")
	var matchingResult *activities.ProcessOntapVolumeMatchingResult
	err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.ProcessOntapVolumeMatching,
		&activities.ProcessOntapVolumeMatchingInput{
			DbVolumes:           dbVolumes,
			OntapVolumesResults: ontapVolumesResults,
		}).Get(ctx, &matchingResult)
	if err != nil {
		log.Errorf("ProcessOntapVolumeMatching activity failed: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Log processing summary
	log.Debugf("Volume processing completed: %d matched, %d not found in ONTAP",
		matchingResult.MatchedCount, matchingResult.NotFoundCount)

	if matchingResult.NotFoundCount > 0 {
		log.Warnf("%d volumes were not found in ONTAP and will not be updated",
			matchingResult.NotFoundCount)
	}

	// Step 5: Sync updates to database if we have volumes to update
	if len(matchingResult.UpdatedVolumeByUUID) > 0 {
		log.Debugf("Syncing %d volume updates to database", len(matchingResult.UpdatedVolumeByUUID))
		err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.SyncUpdatedVolumesToDatabase,
			&activities.SyncUpdatedVolumesInput{
				UpdatedVolumeByUUID:               matchingResult.UpdatedVolumeByUUID,
				VolumesWithClonesSharedBytesReset: matchingResult.VolumesWithClonesSharedBytesReset,
			}).Get(ctx, nil)
		if err != nil {
			log.Errorf("SyncUpdatedVolumesToDatabase activity failed: %v", err)
			return nil, vsaerrors.ExtractCustomError(err)
		}
		log.Debugf("Database sync completed successfully")
	} else {
		log.Debugf("No volumes to update in database")
	}

	// Step 6: Update account metadata with workflow completion timestamp
	if input.AccountUUID != "" {
		accountUUID := input.AccountUUID
		completionTime := workflow.Now(ctx)

		log.Infof("Updating account %s VolumeRefreshWorkflow completion timestamp to %v",
			accountUUID, completionTime)

		err = workflow.ExecuteActivity(ctx, volumeRefreshActivity.UpdateAccountVolumeRefreshTimestamp,
			&activities.UpdateAccountVolumeRefreshTimestampInput{
				AccountUUID: accountUUID,
				CompletedAt: completionTime,
			}).Get(ctx, nil)
		if err != nil {
			// Log error but don't fail the workflow - this is a metadata update
			log.Errorf("Failed to update account volume refresh timestamp: %v", err)
		} else {
			log.Infof("Successfully updated account volume refresh timestamp for account %s", accountUUID)
		}
	}

	return nil, nil
}

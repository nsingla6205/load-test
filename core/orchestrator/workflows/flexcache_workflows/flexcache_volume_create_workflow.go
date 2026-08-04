package flexcache_workflows

import (
	"errors"
	"fmt"
	"time"

	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	clusterPeerTimeout  = env.GetDuration("CLUSTER_PEER_TIMEOUT", 60*time.Minute)
	clusterPeerInterval = env.GetDuration("CLUSTER_PEER_INTERVAL", 15*time.Second)
	forceEncryption     = env.GetBool("FLEXCACHE_FORCE_ENCRYPTION", false)

	// Guards the short job-status activities (GetJob/UpdateJobStatus/EnsureJobState); tunable without a rebuild.
	flexCacheJobHeartbeatTimeout = env.GetDuration("FLEXCACHE_JOB_HEARTBEAT_TIMEOUT", 30*time.Second)

	getClusterPeerTimeout  = _getClusterPeerTimeout
	getClusterPeerInterval = _getClusterPeerInterval
	shouldForceEncryption  = _shouldForceEncryption
)

const (
	CancelFlexCacheSignalName = "cancel-flexcache-creation"
)

func _getClusterPeerTimeout() time.Duration {
	return clusterPeerTimeout
}

func _getClusterPeerInterval() time.Duration {
	return clusterPeerInterval
}

func _shouldForceEncryption() bool {
	return forceEncryption
}

type flexCacheCreateWorkflow struct {
	workflows.BaseWorkflow
	establishPeeringJobUUID string
}

var _ workflows.WorkflowInterface = &flexCacheCreateWorkflow{}

// UpdateJobStatus updates the establish-peering job (or the sole establish job when UUID is set on the event),
// not the FlexCacheCreateVolume job whose UUID may match the workflow execution ID.
func (wf *flexCacheCreateWorkflow) UpdateJobStatus(ctx workflow.Context, status string, err error) error {
	jobUUID := wf.establishPeeringJobUUID
	if jobUUID == "" {
		jobUUID = wf.ID
	}
	if jobUUID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job uuid cannot be empty"))
	}

	updatedJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: jobUUID},
		State:     status,
	}
	if err != nil {
		var customError *vsaerrors.CustomError
		if vsaerrors.As(err, &customError) {
			updatedJob.TrackingID = customError.TrackingID
			updatedJob.ErrorDetails = customError.OriginalErr.Error()
		} else {
			updatedJob.TrackingID = 0
			updatedJob.ErrorDetails = err.Error()
		}
	}

	commonActivity := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		HeartbeatTimeout:    flexCacheJobHeartbeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})
	return workflow.ExecuteActivity(ctx, commonActivity.UpdateJobStatus, updatedJob).Get(ctx, nil)
}

// EnsureJobState checks the establish-peering job state (not the FlexCacheCreateVolume job
// whose UUID matches the workflow execution ID but is stored as DONE immediately on creation).
func (wf *flexCacheCreateWorkflow) EnsureJobState(ctx workflow.Context, expected datamodel.JobState) error {
	jobUUID := wf.establishPeeringJobUUID
	if jobUUID == "" {
		jobUUID = wf.ID
	}
	if jobUUID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError,
			errors.New("job uuid cannot be empty"))
	}

	commonActivity := activities.CommonActivities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		HeartbeatTimeout:    flexCacheJobHeartbeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	var job *datamodel.Job
	if err := workflow.ExecuteActivity(ctx, commonActivity.GetJob, jobUUID).Get(ctx, &job); err != nil {
		return err
	}

	if job == nil {
		return vsaerrors.NewVCPError(
			vsaerrors.ErrDatabaseDataNotFoundError,
			fmt.Errorf("job %s not found", jobUUID),
		)
	}

	if job.State != string(expected) {
		return vsaerrors.NewVCPError(
			vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("job %s is in state %s; expected %s", jobUUID, job.State, expected),
		)
	}

	return nil
}

// CreateFlexCacheWorkflow Volume Workflow process volume related requests from a customer.
func CreateFlexCacheWorkflow(ctx workflow.Context, params *common.CreateVolumeParams, volume *datamodel.Volume, event *flexcache.CreateFlexCacheEvent) (retErr error) {
	log := util.GetLogger(ctx)
	flexCacheWf := new(flexCacheCreateWorkflow)
	if event != nil {
		flexCacheWf.establishPeeringJobUUID = event.EstablishPeeringJobUUID
	}
	// Guard to detect whether we entered Run.
	enteredRun := false

	// Defer to catch panics and pre-Run failures, so we don’t leave the job stuck in PROCESSING.
	defer func() {
		if r := recover(); r != nil {
			// Panic before Run or during Run. If it was during Run,
			// Run’s own defer should handle job failure; we only handle pre-Run here.
			log.Errorf("panic in CreateFlexCacheWorkflow: %v", r)
			if !enteredRun {
				_ = flexCacheWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), fmt.Errorf("panic: %v", r))
			}
			retErr = fmt.Errorf("panic: %v", r)
		}
	}()

	if err := flexCacheWf.Setup(ctx, params); err != nil {
		log.Errorf("Failed to setup CreateFlexCacheWorkflow: %v", err)
		return err
	}
	if err := flexCacheWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}
	flexCacheWf.Status = workflows.WorkflowStatusRunning
	if err := flexCacheWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		log.Errorf("Failed to update job status to Processing for CreateFlexCacheWorkflow: %v", err)
		return err
	}

	// Hand-off job failure to Run’s defer.
	enteredRun = true
	_, customErr := flexCacheWf.Run(ctx, volume, params, event)
	if customErr != nil {
		log.Errorf("CreateFlexCacheWorkflow completed with error: %v", customErr)
		flexCacheWf.Status = workflows.WorkflowStatusFailed
		return customErr
	}
	flexCacheWf.Status = workflows.WorkflowStatusCompleted
	return nil
}

func (wf *flexCacheCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	createPoolParams := input.(*common.CreateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createPoolParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *flexCacheCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	dbVolume := args[0].(*datamodel.Volume)
	params := args[1].(*common.CreateVolumeParams)
	event := args[2].(*flexcache.CreateFlexCacheEvent)
	flexCacheVolumeCreateActivity := &flexcache_activities.FlexCacheVolumeCreateActivity{}
	flexCacheVolumeDeleteActivity := &flexcache_activities.FlexCacheVolumeDeleteActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	correlationID := ""
	if event != nil && event.CorrelationID != nil {
		correlationID = *event.CorrelationID
	}

	wfInfo := workflow.GetInfo(ctx)
	flexcacheResult := flexcache.CreateFlexCacheResult{
		Event:         event,
		DBVolume:      dbVolume,
		ActiveJobType: datamodel.JobTypeFlexCacheEstablishPeering,
		JobInput: &flexcache.JobActivityInput{
			ResourceName:  dbVolume.Name,
			ResourceUUID:  dbVolume.UUID,
			AccountID:     dbVolume.AccountID,
			WorkflowID:    wfInfo.WorkflowExecution.ID,
			CorrelationID: correlationID,
			Metadata: map[string]interface{}{
				"poolID": dbVolume.Pool.ID,
			},
		},
	}

	cancellationHandler := common.NewWorkflowCancellationHandler(ctx, CancelFlexCacheSignalName, dbVolume.UUID, "flexcache")

	rollbackManager := common.NewRollbackManager()
	defer func() {
		// Helper function to handle pre-rollback activities for flexcache
		handleFlexCachePreRollback := func(disconnectedCtx workflow.Context, vsaErr *vsaerrors.CustomError) {
			updateStateDetailsAndCode(&flexcacheResult, vsaErr)
			if err2 := workflow.ExecuteActivity(
				disconnectedCtx,
				flexCacheVolumeCreateActivity.UpdateVolumeDetailsOnErrorActivity,
				&flexcacheResult,
			).Get(disconnectedCtx, nil); err2 != nil {
				log.Errorf("Failed to update cache parameters in DB: %v", err2)
			}

			if flexcacheResult.ActiveJobType != "" {
				flexcacheResult.ErrorTrackingID = vsaErr.TrackingID
				flexcacheResult.ErrorMessage = vsaErr.Error()
				_ = workflow.ExecuteActivity(
					disconnectedCtx,
					flexCacheVolumeCreateActivity.FailJobActivity,
					&flexcacheResult,
				).Get(disconnectedCtx, nil)
			}

			// Update cluster peering row state to ERROR in DB if not PEERED
			if flexcacheResult.ClusterPeeringRow != nil && flexcacheResult.ClusterPeeringRow.State !=
				datamodel.CvpClusterPeeringStatusPEERED {
				err2 := workflow.ExecuteActivity(disconnectedCtx, flexCacheVolumeCreateActivity.UpdateClusterPeeringRowStateErrorInDBActivity, &flexcacheResult).Get(disconnectedCtx, nil)
				if err2 != nil {
					log.Errorf("Failed to update cluster peering row state in DB: %v", err2)
				}
			}
		}

		common.ExecuteDeferredCleanup(ctx, cancellationHandler, rollbackManager, err, wf.Logger, "flexcache", dbVolume.UUID,
			func(disconnectedCtx workflow.Context) error {
				vsaErr := workflows.ConvertToVSAError(err)
				handleFlexCachePreRollback(disconnectedCtx, vsaErr)
				return nil
			},
			func(disconnectedCtx workflow.Context, cancelErr error) {
				vsaErr := workflows.ConvertToVSAError(cancelErr)
				handleFlexCachePreRollback(disconnectedCtx, vsaErr)
			},
			nil) // shouldRollbackOnError
	}()

	// Active job helpers
	setActiveJob := func(jobType datamodel.JobType) {
		flexcacheResult.ActiveJobType = jobType
	}

	clearActiveJob := func() {
		flexcacheResult.ActiveJobType = ""
	}

	// Copy input cache parameters to DBVolume cache parameters
	// This is needed to set the CommandExpiryTime in DBVolume cache parameters
	// as we are using same workflow for establish peering, we can override the values here
	// if user has provided different values during create flexcache volume
	// we copy expiry time and peer ips from input params to DBVolume cache parameters
	if params != nil && params.CacheParameters != nil && dbVolume.CacheParameters != nil {
		flexcacheResult = *copyInputCacheParameters(params, &flexcacheResult)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	// Fetch nodes needed for FlexCache creation
	var dbNodes []*datamodel.Node
	if err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &dbVolume.Pool.ID).Get(ctx, &dbNodes); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	flexcacheResult.Node = vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbVolume.Pool.DeploymentName,
		OntapCredentials: dbVolume.Pool.PoolCredentials,
	})

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// Fetch cluster peering row from DB if exists
	err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.GetClusterPeeringRowFromDBActivity, &flexcacheResult).Get(ctx, &flexcacheResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// check the status of cluster peer before creating new cluster peer
	err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.EnsureClusterPeerInOntapActivity, &flexcacheResult).Get(ctx, &flexcacheResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if flexcacheResult.ClusterPeerAction == flexcache.ActionCreate {
		// delete cluster peer on ONTAP if already created in previous attempt and has problem before creating new one
		if flexcacheResult.ClusterPeer != nil {
			log.Debugf("Deleting existing cluster peer on ONTAP before creating a new one as a part of "+
				"Establish Peering for flexcache volume %s", dbVolume.Name)
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, flexCacheVolumeDeleteActivity.DeleteClusterPeerInOntapActivity,
				convertCreateResultToDeleteResult(&flexcacheResult)).Get(ctx, nil)
			if err != nil {
				log.Errorf("Failed to delete cluster peer on ONTAP before creating a new one: %v", err)
				return nil, workflows.ConvertToVSAError(err)
			}
			flexcacheResult.ClusterPeer = nil
		}

		// delete cluster peering row on DB if already created in previous attempt and has problem before creating new one
		if flexcacheResult.ClusterPeeringRow != nil {
			if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
				return nil, cancelErr
			}
			err = workflow.ExecuteActivity(ctx, flexCacheVolumeDeleteActivity.DeleteClusterPeeringRowInDBActivity, convertCreateResultToDeleteResult(&flexcacheResult)).Get(ctx, nil)
			if err != nil {
				log.Errorf("Failed to delete cluster peering row in DB before creating a new one: %v", err)
				return nil, workflows.ConvertToVSAError(err)
			}
			flexcacheResult.ClusterPeeringRow = nil
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// Create cluster peering row in DB if not exists
		err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.CreateClusterPeeringRowInDBActivity, &flexcacheResult).Get(ctx, &flexcacheResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Add cluster peering relationship to volume in DB when cluster peer is created
		// This is needed to track which volumes are using this cluster peering relationship
		// for cleanup during delete flexcache volume, if cluster peer creation fails
		// We call this activity here to ensure that the relationship is created in DB
		// before we proceed to create cluster peer on ONTAP
		err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.UpdateClusterPeeringInVolume, &flexcacheResult).Get(ctx, &flexcacheResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.CreateClusterPeerInOntapActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Update volume with cluster peering relationship in DB if existing peer for new volume or peer is ready
	// calling activity again here only for existing peer scenario where action is READY to avoid unnecessary
	// DB calls for create action and wait action
	if flexcacheResult.ClusterPeerAction == flexcache.ActionReady {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		// Add cluster peering relationship to volume in DB when cluster peer is created or ready (existing peer for new volume)
		err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.UpdateClusterPeeringInVolume, &flexcacheResult).Get(ctx, &flexcacheResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// CCFE compatibility: Complete peering job
	if err = workflow.ExecuteActivity(
		ctx,
		flexCacheVolumeCreateActivity.CompletePeeringJobActivity,
		&flexcacheResult,
	).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	clearActiveJob()

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// CCFE compatibility: Internal job
	if err = workflow.ExecuteActivity(
		ctx,
		flexCacheVolumeCreateActivity.StartInternalJobActivity,
		&flexcacheResult,
	).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	setActiveJob(datamodel.JobTypeFlexCacheInternalPeering)

	if flexcacheResult.ClusterPeerAction == flexcache.ActionCreate || flexcacheResult.ClusterPeerAction == flexcache.ActionWait {
		clusterPeerWaitCtx := getWaitContext(ctx, dbVolume.CacheParameters)
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteActivity(clusterPeerWaitCtx, flexCacheVolumeCreateActivity.WaitForClusterPeerActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			if temporal.IsTimeoutError(err) {
				err = vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrClusterPeerTimeout, err))
			}
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.UpdateClusterPeeringRowStatePeeredInDBActivity, &flexcacheResult).Get(ctx, &flexcacheResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.EnsureSVMPeerInOntapActivity, &flexcacheResult).Get(ctx, &flexcacheResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// delete SVM peer on ONTAP if already created in previous attempt and has problem before creating new one
	if flexcacheResult.SVMPeerAction == flexcache.ActionCreate && flexcacheResult.SVMPeer != nil {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		err = workflow.ExecuteActivity(ctx, flexCacheVolumeDeleteActivity.DeleteSVMPeeringInOntapActivity,
			convertCreateResultToDeleteResult(&flexcacheResult)).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		flexcacheResult.SVMPeer = nil
	}

	if flexcacheResult.SVMPeerAction == flexcache.ActionCreate {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.CreateSVMPeeringInOntapActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.HydrateFlexCacheState, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if flexcacheResult.SVMPeerAction == flexcache.ActionCreate || flexcacheResult.SVMPeerAction == flexcache.ActionWait {
		svmPeerWaitCtx := getWaitContext(ctx, nil)
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteActivity(svmPeerWaitCtx, flexCacheVolumeCreateActivity.WaitForSVMPeeringActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			if temporal.IsTimeoutError(err) {
				err = vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrSVMPeerTimeout, err))
			}
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	// Update the volume to be "creating" state because the volume is actually being created in ONTAP now that peering has been established
	// In the creating state we must wait for completion or an error before we can delete.
	if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.UpdateFlexCacheVolumeLifecycleStateActivity, &flexcacheResult, datamodel.LifeCycleStateCreating).Get(ctx, &flexcacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}

	var preUpdatedVolume *datamodel.Volume
	if err = workflow.ExecuteChildWorkflow(ctx, workflows.PreFileVolumeWorkflow, flexcacheResult.DBVolume, flexcacheResult.Node).Get(ctx, &preUpdatedVolume); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	flexcacheResult.DBVolume = preUpdatedVolume
	dbVolume = flexcacheResult.DBVolume

	if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.CreateFlexCacheVolumeInOntapActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	dbVolume = flexcacheResult.DBVolume

	protocols := dbVolume.VolumeAttributes.Protocols
	hasNFS := utils.IsNFSProtocols(protocols)
	hasSMB := utils.IsSMBProtocols(protocols)
	var postCreationChildWorkflows []interface{}
	if hasNFS {
		postCreationChildWorkflows = append(postCreationChildWorkflows, workflows.PostFileVolumeWorkflow)
	}
	if enableSmb && hasSMB {
		postCreationChildWorkflows = append(postCreationChildWorkflows, workflows.PostFileVolumeWorkflowForSMB)
	}

	for _, wfToRun := range postCreationChildWorkflows {
		var updatedVolume *datamodel.Volume
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteChildWorkflow(ctx, wfToRun, flexcacheResult.DBVolume, flexcacheResult.Node).Get(ctx, &updatedVolume); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		// Update the dbVolume with the changes from each child workflow
		if updatedVolume != nil {
			flexcacheResult.DBVolume = updatedVolume
			dbVolume = flexcacheResult.DBVolume
		}
	}

	if err = workflow.ExecuteActivity(ctx, activities.VolumeCreateActivity.UpdateVolumeAttributesInDB, &flexcacheResult.DBVolume.UUID, &flexcacheResult.DBVolume.VolumeAttributes).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if shouldForceEncryption() {
		if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
			return nil, cancelErr
		}
		if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.VerifyVolumeEncryptionActivity, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.UpdateFlexCacheVolumeLifecycleStateActivity, &flexcacheResult, datamodel.LifeCycleStateREADY).Get(ctx, &flexcacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if err = workflow.ExecuteActivity(ctx, flexCacheVolumeCreateActivity.HydrateFlexCacheState, &flexcacheResult).Get(ctx, &flexcacheResult); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if cancelErr := cancellationHandler.CheckCancellationSignal(ctx); cancelErr != nil {
		return nil, cancelErr
	}
	// CCFE compatibility: Complete internal job
	if err = workflow.ExecuteActivity(
		ctx,
		flexCacheVolumeCreateActivity.CompleteInternalJobActivity,
		&flexcacheResult,
	).Get(ctx, nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	clearActiveJob()

	return nil, nil
}

func updateStateDetailsAndCode(result *flexcache.CreateFlexCacheResult, err error) {
	var vsaErr *vsaerrors.CustomError
	if errors.As(err, &vsaErr) {
		switch vsaErr.TrackingID {
		case vsaerrors.ErrClusterPeerError:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.ErrorDuringClusterPeerCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.ErrorDuringClusterPeer
		case vsaerrors.ErrClusterPeerTimeout:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.ClusterPeeringExpiredCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.ClusterPeeringExpired
		case vsaerrors.ErrSVMPeerTimeout:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.SVMPeeringExpiredCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.SVMPeeringExpired
		case vsaerrors.ErrSVMPeerError:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.ErrorDuringSVMPeeringCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.ErrorDuringSVMPeering
		case vsaerrors.ErrUnencryptedVolume:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.ErrorUnencryptedVolumeCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.ErrorUnencryptedVolume
		default:
			result.DBVolume.CacheParameters.CacheStateDetailsCode = coremodels.DefaultCode
			result.DBVolume.CacheParameters.CacheStateDetails = datamodel.ErrorCreatingCacheVolume
		}
	}
}

func getWaitContext(ctx workflow.Context, cacheParams *datamodel.CacheParameters) workflow.Context {
	expiry := getClusterPeerTimeout()
	if cacheParams != nil && cacheParams.CommandExpiryTime != nil && cacheParams.CommandExpiryTime.After(workflow.Now(ctx)) {
		expiry = time.Until(*cacheParams.CommandExpiryTime)
	}

	interval := getClusterPeerInterval()
	activityOptions := workflow.ActivityOptions{
		// Total timeout for the activity
		ScheduleToCloseTimeout: expiry,
		// Timeout for a single activity execution
		StartToCloseTimeout: interval,
		// With an interval of 15s it will take ~10 mins to reach the maximum interval of 45s
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    interval,
			BackoffCoefficient: 1.05,
			MaximumInterval:    interval * 3,
		},
	}

	return workflow.WithActivityOptions(ctx, activityOptions)
}

func convertCreateResultToDeleteResult(createResult *flexcache.CreateFlexCacheResult) *flexcache.DeleteFlexCacheResult {
	return &flexcache.DeleteFlexCacheResult{
		DBVolume:          createResult.DBVolume,
		Node:              createResult.Node,
		ClusterPeeringRow: createResult.ClusterPeeringRow,
	}
}

func copyInputCacheParameters(params *common.CreateVolumeParams,
	flexcacheResult *flexcache.CreateFlexCacheResult) *flexcache.CreateFlexCacheResult {
	in := params.CacheParameters
	out := flexcacheResult.DBVolume.CacheParameters

	// use the input cache parameters only for CommandExpiryTime
	// assign nil if PeerExpiryTime is not provided in input, which sets the expiry time
	// to default value in flexcache create activity to 1 hour from current time
	if in.PeerExpiryTime != nil {
		t := *in.PeerExpiryTime
		out.CommandExpiryTime = &t
	} else {
		out.CommandExpiryTime = nil
	}

	// Copy peer IPs
	out.PeerIpAddresses = in.PeerIPAddresses
	return flexcacheResult
}

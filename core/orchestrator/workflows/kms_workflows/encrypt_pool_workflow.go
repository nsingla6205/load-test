package kms_workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type encryptPoolWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &encryptPoolWorkflow{}

// encryptPoolRunResult reports how far EncryptPoolWorkflow.Run progressed
type encryptPoolRunResult struct {
	// RekeyCompleted is true once CMEK→CMEK RekeyKmsForSvmActivity has succeeded (ONTAP and SVM already on the target key).
	RekeyCompleted bool
	// PreviousKmsConfigUUID is the pool's KMS config UUID before encrypt/rekey (empty for first-time encrypt).
	PreviousKmsConfigUUID string
}

// EncryptPoolWorkflow encrypts or migrates CMEK for a single pool.
func EncryptPoolWorkflow(ctx workflow.Context, params *common.EncryptPoolParams) (interface{}, error) {
	wf := new(encryptPoolWorkflow)
	if err := wf.Setup(ctx, params); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Always verify target (+ previous when present) after Run and any pool-FK compensation.
	var result *encryptPoolRunResult
	defer func() {
		wf.verifyKmsConfigsAfterEncrypt(ctx, params, result)
	}()

	runResult, runErr := wf.Run(ctx, params)
	result, _ = runResult.(*encryptPoolRunResult)
	if runErr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		if result != nil && result.RekeyCompleted {
			// ONTAP/SVM already hold the target key — do not revert lifecycle to the pre-encrypt
			// snapshot (that would split-brain against ONTAP). Finish the pool FK repoint instead.
			if reconcileErr := wf.reconcilePoolKmsAfterRekey(ctx, params); reconcileErr != nil {
				wf.Logger.Error("Failed to reconcile pool KMS association after successful rekey",
					"poolUUID", params.PoolUUID, "kmsConfigID", params.KmsConfigID, "error", reconcileErr)
			}
		} else if failErr := wf.failPoolState(ctx, params, runErr); failErr != nil {
			wf.Logger.Error("Failed to mark pool as errored after encrypt pool workflow failure", "error", failErr)
		}
		if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), runErr); err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		return nil, runErr
	}

	wf.Status = workflows.WorkflowStatusCompleted
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	return nil, nil
}

func (wf *encryptPoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params := input.(*common.EncryptPoolParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	logger := util.GetLogger(ctx)
	wf.Logger = logger.With(log.Fields{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
		"poolUUID":   params.PoolUUID,
	})

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *encryptPoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.EncryptPoolParams)

	poolActivity := &activities.PoolActivity{}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return &encryptPoolRunResult{}, workflows.ConvertToVSAError(err)
	}
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	// Verify reachability before touching the pool so we fail fast if the CMEK key is not  reachable
	var targetKmsConfig *datamodel.KmsConfig
	wf.Logger.Info("EncryptPool: pre-flight KMS reachability check", "kmsConfigID", params.KmsConfigID)
	targetKmsConfig, err = verifyKmsReachabilityBeforeEncrypt(ctx, params.KmsConfigID)
	if err != nil {
		wf.Logger.Error("KMS config reachability verification failed before encrypt pool", "kmsConfigID", params.KmsConfigID, "error", err)
		return &encryptPoolRunResult{}, workflows.ConvertToVSAError(err)
	}
	kmsConfigName := ""
	if targetKmsConfig != nil {
		kmsConfigName = targetKmsConfig.Name
	}
	wf.Logger.Info("EncryptPool: pre-flight KMS reachability check complete",
		"kmsConfigID", params.KmsConfigID, "kmsConfigName", kmsConfigName)

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: params.PoolUUID},
	}
	if err = workflow.ExecuteActivity(ctx, poolActivity.GetPool, pool).Get(ctx, &pool); err != nil {
		wf.Logger.Error("Pool not found for encrypt pool operation", "poolUUID", params.PoolUUID, "kmsConfigID", params.KmsConfigID, "error", err)
		return &encryptPoolRunResult{}, workflows.ConvertToVSAError(err)
	}

	runResult := &encryptPoolRunResult{}
	if pool.KmsConfig != nil {
		runResult.PreviousKmsConfigUUID = pool.KmsConfig.UUID
	}

	if pool.KmsConfigID.Valid {
		// If the pool is already associated with the exact target KMS config, skip the
		// asynchronous rekey entirely and revert the pool to its previous state.
		if targetKmsConfig != nil && pool.KmsConfigID.Int64 == targetKmsConfig.ID {
			wf.Logger.Info("EncryptPool: no-op — pool already on target KMS",
				"poolUUID", pool.UUID, "poolName", pool.Name, "kmsConfigID", params.KmsConfigID)
			if revertErr := wf.revertPoolState(ctx, params); revertErr != nil {
				wf.Logger.Error("Failed to revert pool state after no-op CMEK rekey", "poolUUID", params.PoolUUID, "error", revertErr)
			}
			return runResult, nil
		}

		// CMEK -> CMEK: the pool is associated with a different CMEK key. Rekey the SVM's
		// existing ONTAP external KEK to the target key instead of provisioning a fresh EKM config.
		wf.Logger.Info("EncryptPool: starting CMEK→CMEK rekey",
			"poolUUID", pool.UUID, "poolName", pool.Name,
			"previousKmsConfigUUID", runResult.PreviousKmsConfigUUID, "targetKmsConfigID", params.KmsConfigID)
		var rekeyErr *vsaerrors.CustomError
		runResult.RekeyCompleted, rekeyErr = rekeySingleVsaPool(ctx, wf.Logger, rekeySingleVsaPoolInput{
			Pool:        pool,
			KmsConfigID: params.KmsConfigID,
			AccountName: params.AccountName,
		})
		return runResult, rekeyErr
	}

	wf.Logger.Info("EncryptPool: starting first-time encrypt/migrate",
		"poolUUID", pool.UUID, "poolName", pool.Name, "targetKmsConfigID", params.KmsConfigID)
	return runResult, migrateSingleVsaPool(ctx, wf.Logger, retryPolicy, migrateSingleVsaPoolInput{
		Pool:        pool,
		KmsConfigID: params.KmsConfigID,
		AccountName: params.AccountName,
	})
}

// disconnectedActivityCtx returns a context detached from ctx's cancellation
func (wf *encryptPoolWorkflow) disconnectedActivityCtx(ctx workflow.Context) (workflow.Context, error) {
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	return workflow.WithActivityOptions(disconnectedCtx, activityOptions), nil
}

func (wf *encryptPoolWorkflow) revertPoolState(ctx workflow.Context, params *common.EncryptPoolParams) error {
	disconnectedCtx, err := wf.disconnectedActivityCtx(ctx)
	if err != nil {
		return err
	}

	previousState := params.PreviousState
	previousStateDetails := params.PreviousStateDetails
	if previousState == "" {
		previousState = datamodel.LifeCycleStateAvailable
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: params.PoolUUID}}
	poolActivity := &activities.PoolActivity{}
	return workflow.ExecuteActivity(disconnectedCtx, poolActivity.UpdatePoolState, pool, previousState, previousStateDetails).Get(disconnectedCtx, nil)
}

// failPoolState marks the pool as Error after any rekey/migration failure
func (wf *encryptPoolWorkflow) failPoolState(ctx workflow.Context, params *common.EncryptPoolParams, runErr *vsaerrors.CustomError) error {
	disconnectedCtx, err := wf.disconnectedActivityCtx(ctx)
	if err != nil {
		return err
	}

	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: params.PoolUUID}}
	poolActivity := &activities.PoolActivity{}
	wf.Logger.Info("EncryptPool: marking pool Error after failure", "poolUUID", params.PoolUUID, "error", errMsg)
	return workflow.ExecuteActivity(disconnectedCtx, poolActivity.FailedPoolActivity, pool, errMsg).Get(disconnectedCtx, nil)
}

// reconcilePoolKmsAfterRekey finishes the DB-side pool→KMS repoint after ONTAP rekey already succeeded
func (wf *encryptPoolWorkflow) reconcilePoolKmsAfterRekey(ctx workflow.Context, params *common.EncryptPoolParams) error {
	disconnectedCtx, err := wf.disconnectedActivityCtx(ctx)
	if err != nil {
		return err
	}

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: params.PoolUUID}}
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	wf.Logger.Info("EncryptPool: reconciling pool→KMS FK after successful rekey",
		"poolUUID", params.PoolUUID, "targetKmsConfigID", params.KmsConfigID)
	if err := workflow.ExecuteActivity(disconnectedCtx, kmsConfigActivity.UpdatePoolWithKmsConfigActivity, pool, params.KmsConfigID).Get(disconnectedCtx, nil); err != nil {
		return err
	}
	wf.Logger.Info("EncryptPool: pool→KMS FK reconcile complete",
		"poolUUID", params.PoolUUID, "targetKmsConfigID", params.KmsConfigID)
	return nil
}

// verifyKmsConfigsAfterEncrypt sets target (+ previous) KMS lifecycle from pool associations and
// reachability. Best-effort; runs on both success and failure after any pool-FK compensation.
func (wf *encryptPoolWorkflow) verifyKmsConfigsAfterEncrypt(ctx workflow.Context, params *common.EncryptPoolParams, result *encryptPoolRunResult) {
	disconnectedCtx, err := wf.disconnectedActivityCtx(ctx)
	if err != nil {
		wf.Logger.Error("Failed to populate retry policy for post-encrypt KMS verify", "error", err)
		return
	}
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}

	if params.KmsConfigID != "" {
		wf.Logger.Info("EncryptPool: verifying target KMS after encrypt", "kmsConfigID", params.KmsConfigID)
		if err := workflow.ExecuteActivity(disconnectedCtx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, params.KmsConfigID, false).Get(disconnectedCtx, nil); err != nil {
			wf.Logger.Error("Failed to verify target KMS config after encrypt pool (best-effort)",
				"kmsConfigID", params.KmsConfigID, "error", err)
		} else {
			wf.Logger.Info("EncryptPool: target KMS verify complete", "kmsConfigID", params.KmsConfigID)
		}
	}

	previousUUID := ""
	if result != nil {
		previousUUID = result.PreviousKmsConfigUUID
	}
	if previousUUID != "" && previousUUID != params.KmsConfigID {
		wf.Logger.Info("EncryptPool: verifying previous KMS after encrypt", "kmsConfigID", previousUUID)
		if err := workflow.ExecuteActivity(disconnectedCtx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, previousUUID, false).Get(disconnectedCtx, nil); err != nil {
			wf.Logger.Error("Failed to verify previous KMS config after encrypt pool (best-effort)",
				"kmsConfigID", previousUUID, "error", err)
		} else {
			wf.Logger.Info("EncryptPool: previous KMS verify complete", "kmsConfigID", previousUUID)
		}
	}
}

// verifyKmsReachabilityBeforeEncrypt ensures the CMEK key is reachable before the pool is
// migrated. It ensures the service account key exists, grants the impersonation role for SDE-created configs
func verifyKmsReachabilityBeforeEncrypt(ctx workflow.Context, kmsConfigID string) (*datamodel.KmsConfig, error) {
	if kmsConfigID == "" {
		return nil, nil
	}

	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	kmsConfig := &datamodel.KmsConfig{}

	if err := workflow.ExecuteActivity(ctx, kmsConfigActivity.GetKmsConfigActivity, kmsConfigID).Get(ctx, kmsConfig); err != nil {
		return nil, err
	}

	if err := workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateVSAKmsConfigSAKeyActivity, kmsConfig).Get(ctx, kmsConfig); err != nil {
		return nil, err
	}

	// VCP-created configs: skip GrantRoleActivity — the VCP SA has direct access to the customer's
	// KMS key. SDE-created configs: grant the impersonation role to the service account.
	if kmsConfig.KmsAttributes == nil || !kmsConfig.KmsAttributes.IsVCPCreated() {
		if err := workflow.ExecuteActivity(ctx, kmsConfigActivity.GrantRoleActivity, kmsConfig).Get(ctx, nil); err != nil {
			return nil, err
		}
	}

	// getVerifyError=true: return the error (and abort) if the key is not reachable.
	if err := workflow.ExecuteActivity(ctx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true).Get(ctx, nil); err != nil {
		return nil, err
	}
	return kmsConfig, nil
}

// migrateSingleVsaPoolInput carries the inputs for the per-pool VSA CMEK migration leg.
type migrateSingleVsaPoolInput struct {
	Pool        *datamodel.Pool
	KmsConfigID string
	AccountName string
}

// migrateSingleVsaPool runs the VSA (ONTAP) CMEK migration for a single pool:
// Either from non-cmek -> cmek or from cmek -> another cmek policy
func migrateSingleVsaPool(ctx workflow.Context, logger log.Logger, retryPolicy *workflows.WorkflowRetryPolicy, in migrateSingleVsaPoolInput) *vsaerrors.CustomError {
	pool := in.Pool
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	volumeActivity := &activities.VolumeCreateActivity{}
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}

	ekmParams := common.CreatePoolParams{
		AccountName: in.AccountName,
		KmsConfigId: in.KmsConfigID,
	}

	var dbNodes []*datamodel.Node
	if err := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, pool.ID).Get(ctx, &dbNodes); err != nil {
		logger.Error("Failed to get node for pool selected for CMEK encryption", "poolID", pool.ID, "error", err)
		return workflows.ConvertToVSAError(err)
	}

	nodeForPool := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})
	if len(nodeForPool.EndpointAddressesToHostNameMap) == 0 {
		logger.Error("Node for pool selected for CMEK encryption has no endpoint address", "poolID", pool.ID)
		return workflows.ConvertToVSAError(errors.New("node for pool selected for CMEK encryption does not have an endpoint address"))
	}

	svmForPool := datamodel.Svm{}
	if err := workflow.ExecuteActivity(ctx, svmActivity.GetSvmForPoolID, pool.ID).Get(ctx, &svmForPool); err != nil {
		logger.Error("Failed to get SVM for pool selected for CMEK encryption", "poolID", pool.ID, "error", err)
		return workflows.ConvertToVSAError(err)
	}
	if svmForPool.ID == 0 {
		logger.Error("No SVM found for pool selected for CMEK encryption", "poolID", pool.ID)
		return workflows.ConvertToVSAError(errors.New("no SVM found for pool selected for CMEK encryption"))
	}

	if err := createEkmForSvm(ctx, nodeForPool, &svmForPool, pool, ekmParams); err != nil {
		logger.Error("Failed to associate EKM to pool selected for CMEK encryption", "poolUUID", pool.UUID, "error", err)
		return workflows.ConvertToVSAError(err)
	}
	logger.Info("EncryptPool: EKM associated to SVM", "poolUUID", pool.UUID, "poolName", pool.Name, "svmUUID", svmForPool.UUID)

	var volumesForMigration []*datamodel.Volume
	if err := workflow.ExecuteActivity(ctx, volumeActivity.GetVolumesByPoolID, pool.ID).Get(ctx, &volumesForMigration); err != nil {
		logger.Error("Failed to retrieve volumes for pool selected for CMEK encryption", "poolUUID", pool.UUID, "error", err)
		return workflows.ConvertToVSAError(err)
	}

	if len(volumesForMigration) < 1 {
		logger.Info("EncryptPool: no volumes — pool ready for future CMEK volume creates", "poolUUID", pool.UUID, "poolName", pool.Name)
		if err := workflow.ExecuteActivity(ctx, poolActivity.CreatedPool, pool, nil).Get(ctx, nil); err != nil {
			logger.Error("Unable to set pool to ready after EKM association", "poolUUID", pool.UUID, "error", err)
			return workflows.ConvertToVSAError(err)
		}
		return nil
	}

	logger.Info("EncryptPool: migrating volumes to CMEK",
		"poolUUID", pool.UUID, "poolName", pool.Name, "volumeCount", len(volumesForMigration))
	startToCloseTimeout := utils.DetermineStartToCloseTimeoutBasedOnUsedSize(volumesForMigration)
	migrationOptions := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(startToCloseTimeout) * time.Minute,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	migrationCtx := workflow.WithActivityOptions(ctx, migrationOptions)
	if err := workflow.ExecuteActivity(migrationCtx, kmsConfigActivity.MigrateVsaPoolActivity, volumesForMigration, nodeForPool).Get(ctx, nil); err != nil {
		logger.Error("Failed to encrypt volumes for pool", "poolUUID", pool.UUID, "error", err)
		return workflows.ConvertToVSAError(err)
	}

	if err := workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolState, pool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil); err != nil {
		logger.Error("Unable to set pool to ready after CMEK encryption", "poolUUID", pool.UUID, "error", err)
		return workflows.ConvertToVSAError(err)
	}
	logger.Info("EncryptPool: first-time encrypt complete — pool READY", "poolUUID", pool.UUID, "poolName", pool.Name)

	return nil
}

// rekeySingleVsaPoolInput carries the inputs for the per-pool VSA CMEK -> CMEK rekey leg.
type rekeySingleVsaPoolInput struct {
	Pool        *datamodel.Pool
	KmsConfigID string
	AccountName string
}

// rekeySingleVsaPool runs the VSA (ONTAP) CMEK -> CMEK rekey for a single pool that already has an external KMS policy:
//
//	it reuses the existing ONTAP config's stored credentials.
//
// rekeyCompleted is true after RekeyKmsForSvmActivity succeeds so callers can compensate forward - update pool KmsConfig
func rekeySingleVsaPool(ctx workflow.Context, logger log.Logger, in rekeySingleVsaPoolInput) (rekeyCompleted bool, err *vsaerrors.CustomError) {
	pool := in.Pool
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}

	rekeyParams := common.CreatePoolParams{
		AccountName: in.AccountName,
		KmsConfigId: in.KmsConfigID,
	}

	var dbNodes []*datamodel.Node
	if actErr := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, pool.ID).Get(ctx, &dbNodes); actErr != nil {
		logger.Error("Failed to get node for pool selected for CMEK rekey", "poolID", pool.ID, "error", actErr)
		return false, workflows.ConvertToVSAError(actErr)
	}

	nodeForPool := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})
	if len(nodeForPool.EndpointAddressesToHostNameMap) == 0 {
		logger.Error("Node for pool selected for CMEK rekey has no endpoint address", "poolID", pool.ID)
		return false, workflows.ConvertToVSAError(errors.New("node for pool selected for CMEK rekey does not have an endpoint address"))
	}

	svmForPool := datamodel.Svm{}
	if actErr := workflow.ExecuteActivity(ctx, svmActivity.GetSvmForPoolID, pool.ID).Get(ctx, &svmForPool); actErr != nil {
		logger.Error("Failed to get SVM for pool selected for CMEK rekey", "poolID", pool.ID, "error", actErr)
		return false, workflows.ConvertToVSAError(actErr)
	}
	if svmForPool.ID == 0 {
		logger.Error("No SVM found for pool selected for CMEK rekey", "poolID", pool.ID)
		return false, workflows.ConvertToVSAError(errors.New("no SVM found for pool selected for CMEK rekey"))
	}

	// Rewrap the SVM's existing external KEK to the target key and repoint the SVM/KMS config.
	logger.Info("EncryptPool: invoking ONTAP rekey-external",
		"poolUUID", pool.UUID, "poolName", pool.Name, "svmUUID", svmForPool.UUID, "targetKmsConfigID", in.KmsConfigID)
	if actErr := workflow.ExecuteActivity(ctx, kmsConfigActivity.RekeyKmsForSvmActivity, &svmForPool, nodeForPool, rekeyParams).Get(ctx, &svmForPool); actErr != nil {
		logger.Error("Failed to rekey external KMS for pool selected for CMEK rekey", "poolUUID", pool.UUID, "error", actErr)
		return false, workflows.ConvertToVSAError(actErr)
	}
	rekeyCompleted = true
	logger.Info("EncryptPool: ONTAP rekey complete",
		"poolUUID", pool.UUID, "poolName", pool.Name, "svmUUID", svmForPool.UUID, "targetKmsConfigID", in.KmsConfigID)

	// Repoint the pool to the target KMS config.
	repointedPool := &datamodel.Pool{}
	logger.Info("EncryptPool: updating pool→KMS FK",
		"poolUUID", pool.UUID, "poolName", pool.Name, "targetKmsConfigID", in.KmsConfigID)
	if actErr := workflow.ExecuteActivity(ctx, kmsConfigActivity.UpdatePoolWithKmsConfigActivity, pool, in.KmsConfigID).Get(ctx, repointedPool); actErr != nil {
		logger.Error("Failed to update pool with target KMS config after CMEK rekey", "poolUUID", pool.UUID, "error", actErr)
		return rekeyCompleted, workflows.ConvertToVSAError(actErr)
	}
	logger.Info("EncryptPool: pool→KMS FK update complete",
		"poolUUID", pool.UUID, "poolName", pool.Name, "targetKmsConfigID", in.KmsConfigID)

	if actErr := workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolState, repointedPool, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil); actErr != nil {
		logger.Error("Unable to set pool to ready after CMEK rekey", "poolUUID", pool.UUID, "error", actErr)
		return rekeyCompleted, workflows.ConvertToVSAError(actErr)
	}
	logger.Info("EncryptPool: rekey complete — pool READY",
		"poolUUID", pool.UUID, "poolName", pool.Name, "targetKmsConfigID", in.KmsConfigID)

	return rekeyCompleted, nil
}

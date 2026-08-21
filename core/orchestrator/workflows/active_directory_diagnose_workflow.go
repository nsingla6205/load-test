package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// AD-diagnostics-specific activity timeouts
var (
	DiagnoseDBActivityStartToCloseTimeout    = env.GetString("AD_DIAGNOSE_DB_ACTIVITY_START_TO_CLOSE_TIMEOUT", "2m")
	DiagnoseDBActivityHeartbeatTimeout       = env.GetString("AD_DIAGNOSE_DB_ACTIVITY_HEARTBEAT_TIMEOUT", "30s")
	DiagnoseOntapActivityStartToCloseTimeout = env.GetString("AD_DIAGNOSE_ONTAP_ACTIVITY_START_TO_CLOSE_TIMEOUT", "10m")
	DiagnoseOntapActivityHeartbeatTimeout    = env.GetString("AD_DIAGNOSE_ONTAP_ACTIVITY_HEARTBEAT_TIMEOUT", "1m")
)

// ActiveDirectoryDiagnoseWorkflow drives the AD connectivity-check on a
// short-lived diagnosis SVM provisioned via VLM. It mirrors the structural
// shell of ActiveDirectoryUpdateWorkflow (Setup -> EnsureJobState -> Run ->
// finalize Job) so worker registration, retry policies, and job state
// transitions stay consistent across the AD workflow family.
type ActiveDirectoryDiagnoseWorkflow struct {
	BaseWorkflow
}

// DiagnoseActiveDirectoryWorkflow is the Temporal-registered entrypoint. It
// provisions a short-lived diagnosis SVM via VLM, runs the AD connectivity
// checks (DNS, CIFS domain join, optional LDAP/Kerberos), and always tears the
// SVM down again via a deferred cleanupManager. A connectivity-check failure is
// surfaced on the job/operation only; the shared AD is never marked ERROR (SDE
// parity) and is restored to the state implied by its real SVM usage on exit.
func DiagnoseActiveDirectoryWorkflow(
	ctx workflow.Context,
	params *common.DiagnoseActiveDirectoryParams,
	ad *models.ActiveDirectory,
) (interface{}, error) {
	log := util.GetLogger(ctx)
	wf := new(ActiveDirectoryDiagnoseWorkflow)

	if err := wf.Setup(ctx, params); err != nil {
		log.Errorf("Failed to setup DiagnoseActiveDirectoryWorkflow: %v", err)
		return nil, err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, err
	}

	wf.Status = WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		log.Errorf("Failed to update job status to Processing for DiagnoseActiveDirectoryWorkflow: %v", err)
		return nil, err
	}

	_, customErr := wf.Run(ctx, params, ad)
	if customErr != nil {
		log.Errorf("DiagnoseActiveDirectoryWorkflow completed with error: %v", customErr)
		wf.Status = WorkflowStatusFailed
		if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr); err != nil {
			log.Errorf("Failed to update job status to ERROR for DiagnoseActiveDirectoryWorkflow: %v", err)
			return nil, err
		}
		return nil, customErr
	}

	wf.Status = WorkflowStatusCompleted
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil); err != nil {
		log.Errorf("Failed to update job status to DONE for DiagnoseActiveDirectoryWorkflow: %v", err)
	}
	return nil, nil
}

// deriveDiagnoseActivityOptions clones the base activity options and overrides
// only the StartToClose and Heartbeat timeouts. The base's *RetryPolicy pointer
// is shared (never mutated) so the same retry semantics apply to every bucket.
func deriveDiagnoseActivityOptions(base workflow.ActivityOptions, startToClose, heartbeat string) (workflow.ActivityOptions, error) {
	s2c, err := time.ParseDuration(startToClose)
	if err != nil {
		return base, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	hb, err := time.ParseDuration(heartbeat)
	if err != nil {
		return base, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}
	opts := base
	opts.StartToCloseTimeout = s2c
	opts.HeartbeatTimeout = hb
	return opts, nil
}

func (wf *ActiveDirectoryDiagnoseWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	p := input.(*common.DiagnoseActiveDirectoryParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = p.ProjectNumber
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
		"poolUUID":   p.PoolUUID,
	})
	wf.Logger = util.GetLogger(ctx)

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

// Run executes the actual diagnose flow. Returns *vsaerrors.CustomError so
// the outer wrapper can finalize the Job row with TrackingID / ErrorDetails.
func (wf *ActiveDirectoryDiagnoseWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := util.GetLogger(ctx)

	if len(args) < 2 {
		return nil, ConvertToVSAError(vsaerrors.New("insufficient arguments provided to DiagnoseActiveDirectoryWorkflow"))
	}
	params, ok := args[0].(*common.DiagnoseActiveDirectoryParams)
	if !ok || params == nil {
		return nil, ConvertToVSAError(vsaerrors.New("DiagnoseActiveDirectoryWorkflow: invalid params"))
	}
	adIn, ok := args[1].(*models.ActiveDirectory)
	if !ok || adIn == nil {
		return nil, ConvertToVSAError(vsaerrors.New("DiagnoseActiveDirectoryWorkflow: invalid active directory"))
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError", "NonRetryableErr"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Derive AD-diagnostics-specific option sets from the base retry policy: a
	// tight budget for DB/in-memory activities and a larger one for ONTAP-REST
	// activities. Computed before the defer so the cleanup closure can capture
	// them.
	dbAO, err := deriveDiagnoseActivityOptions(ao, DiagnoseDBActivityStartToCloseTimeout, DiagnoseDBActivityHeartbeatTimeout)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ontapAO, err := deriveDiagnoseActivityOptions(ao, DiagnoseOntapActivityStartToCloseTimeout, DiagnoseOntapActivityHeartbeatTimeout)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	dbCtx := workflow.WithActivityOptions(ctx, dbAO)
	ontapCtx := workflow.WithActivityOptions(ctx, ontapAO)

	poolActivity := &activities.PoolActivity{}
	adReusedActivity := &active_directory_activities.ActiveDirectoryActivity{}
	diagActivity := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}

	// cleanupManager fires on every exit (success and failure) to tear down the
	// diagnosis SVM in LIFO order: soft-delete the DB row, unbind the CIFS server
	// (so VLM's ValidateSvmDelete does not refuse on an AD-joined SVM), then
	// delete the VLM SVM. There is intentionally no "mark AD as ERROR" step;
	// the AD is always restored from its live SVM usage after teardown.
	cleanupManager := common.NewRollbackManager()

	// diagnosisSVMRow holds the persisted SVM row so the deferred cleanup can
	// soft-delete it; captured by the defer closure.
	var diagnosisSVMRow *datamodel.Svm
	var runErr error

	defer func() {
		disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
		// RollbackManager applies one shared ActivityOptions to every rollback
		// activity, and the cleanup set mixes DB and ONTAP-REST activities. Use
		// the larger ONTAP budget as a safe upper bound for both.
		rollbackCtx := workflow.WithActivityOptions(disconnectedCtx, ontapAO)

		// Registered last so it runs first in LIFO; no-op if the row was never
		// persisted.
		if diagnosisSVMRow != nil {
			cleanupManager.AddActivity(diagActivity.SoftDeleteDiagnosisSVMFromDB, diagnosisSVMRow)
		}

		// Tear down the diagnosis SVM; empty manager is a no-op.
		cleanupManager.ExecuteRollback(rollbackCtx, runErr)

		// Restore the AD from its real (non-diagnosis) SVM usage. Runs after
		// teardown so the diagnosis row cannot skew the computation. This is a
		// DB activity, so it uses the tight DB budget.
		dbRollbackCtx := workflow.WithActivityOptions(disconnectedCtx, dbAO)
		if restoreErr := workflow.ExecuteActivity(
			dbRollbackCtx,
			diagActivity.RestoreADStateFromSVMUsage,
			adIn.UUID,
		).Get(dbRollbackCtx, nil); restoreErr != nil {
			logger.Errorf("Failed to restore AD %s state after diagnosis: %v", adIn.UUID, restoreErr)
		}
	}()

	// Step 1: Load pool from VCP DB (re-loaded so the workflow stays resumable
	// from scratch on worker restart).
	var pool *datamodel.Pool
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.LoadPoolForDiagnosis, params.PoolUUID).Get(dbCtx, &pool); runErr != nil {
		logger.Errorf("LoadPoolForDiagnosis failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 2: Re-load the AD record from DB so credentials/attributes are current (the caller passed a snapshot).
	var adRecord *datamodel.ActiveDirectory
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.GetActiveDirectoryRecordForPool, pool.ID).Get(dbCtx, &adRecord); runErr != nil {
		logger.Errorf("GetActiveDirectoryRecordForPool failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 3: Resolve node, VLMConfig, and ONTAP credentials, reusing the pool-level activities.
	var node *models.Node
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.GetNodeForDiagnosis, pool).Get(dbCtx, &node); runErr != nil {
		logger.Errorf("GetNodeForDiagnosis failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	var vlmConfig *vlm.VLMConfig
	if runErr = workflow.ExecuteActivity(dbCtx, poolActivity.ParseVlmConfig, pool).Get(dbCtx, &vlmConfig); runErr != nil {
		logger.Errorf("ParseVlmConfig failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	var credConfig *vlm.OntapCredentials
	if runErr = workflow.ExecuteActivity(dbCtx, poolActivity.CreateOnTapCredentials, pool).Get(dbCtx, &credConfig); runErr != nil {
		logger.Errorf("CreateOnTapCredentials failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 4: Build the VLM CreateSVMRequest and provision the diagnosis SVM.
	var createReq *vlm.CreateSVMRequest
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.PrepareCreateDiagnosisSVMRequest, pool, adRecord, vlmConfig, credConfig).Get(dbCtx, &createReq); runErr != nil {
		logger.Errorf("PrepareCreateDiagnosisSVMRequest failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	vsaClientWorkflowManager := GetNewVSAClientWorkflowManager()

	createResp, vlmErr := vsaClientWorkflowManager.CreateVSASVM(ctx, createReq)
	if vlmErr != nil {
		runErr = vlmErr
		logger.Errorf("vlmClient.CreateVSASVM failed for diagnosis SVM %s: %v", createReq.Name, vlmErr)
		return nil, ConvertToVSAError(vlmErr)
	}
	if createResp == nil {
		runErr = vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
			vsaerrors.New("vlmClient.CreateVSASVM returned nil response for diagnosis SVM"))
		return nil, ConvertToVSAError(runErr)
	}

	// Step 5: Register VLM teardown so VLM Delete fires on every subsequent exit.
	// Use params.ProjectNumber (== account name) for the worker queue;
	// the pool was loaded without the Account relation, so pool.Account would nil-panic here.
	vlmWorkerQueue := vlm.GetVLMWorkerQueue(wf.Logger, params.ProjectNumber)
	deleteSVMReq := &vlm.DeleteSVMRequest{
		Name:             createReq.Name,
		VLMConfig:        createResp.VLMConfig,
		OntapCredentials: createReq.OntapCredentials,
	}
	cleanupManager.AddWorkflow(vlmWorkerQueue, vlm.DeleteVSASVMWorkflowName, deleteSVMReq)

	// Step 6: Persist the diagnosis SVM row after VLM create, capturing it into diagnosisSVMRow so the deferred cleanup can soft-delete it.
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.SaveDiagnosisSVMToDB, pool, adRecord, createReq.Name, &createResp.VLMConfig).Get(dbCtx, &diagnosisSVMRow); runErr != nil {
		logger.Errorf("SaveDiagnosisSVMToDB failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// SaveDiagnosisSVMToDB guarantees a populated SvmDetails on success.
	if diagnosisSVMRow == nil || diagnosisSVMRow.SvmDetails == nil || diagnosisSVMRow.SvmDetails.ExternalUUID == "" {
		runErr = vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState,
			fmt.Errorf("diagnosis SVM row for %q has no external UUID", createReq.Name))
		logger.Errorf("SaveDiagnosisSVMToDB returned an unusable diagnosis SVM row: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 6.5: Pin the ONTAP REST endpoint to the node hosting the ILB NAS LIF;
	// CIFS/Kerberos calls must target that node. Falls back to the all-node endpoint if VLM did not return an ILB NAS LIF.
	var ilbNasNode *models.Node
	if runErr = workflow.ExecuteActivity(dbCtx, diagActivity.GetIlbNasLifHomeNodeForDiagnosis, pool, createReq.Name, &createResp.VLMConfig, node).Get(dbCtx, &ilbNasNode); runErr != nil {
		logger.Errorf("GetIlbNasLifHomeNodeForDiagnosis failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 6.6: Set the ILB NAS LIF service policy to "default-data-files";
	// VLM provisions it with a restricted policy that cannot carry DNS/AD traffic.
	if runErr = workflow.ExecuteActivity(ontapCtx, diagActivity.SetIlbNasLifServicePolicy, ilbNasNode, createReq.Name, &createResp.VLMConfig).Get(ontapCtx, nil); runErr != nil {
		logger.Errorf("SetIlbNasLifServicePolicy failed for diagnosis SVM %s: %v", createReq.Name, runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Register CIFS-server unbind so it runs before VLM Delete in LIFO; VLM's
	// ValidateSvmDelete refuses an SVM that still has an AD-joined CIFS server.
	// The activity treats NotFound as success, so registering it now covers
	// every later failure (DNS, CIFS create, LDAP, Kerberos).
	cleanupManager.AddActivity(
		diagActivity.DeleteDiagnosisCifsServer,
		ilbNasNode, adRecord, diagnosisSVMRow.SvmDetails.ExternalUUID,
	)

	// Step 7: Convert the AD record into a credential-bearing vsa.ActiveDirectory
	// (decrypts password) so the reused ONTAP activities can drive CIFS/DNS/Kerberos.
	var vsaAD *vsa.ActiveDirectory
	if runErr = workflow.ExecuteActivity(dbCtx, adReusedActivity.GetActiveDirectoryForPool, pool.ID).Get(dbCtx, &vsaAD); runErr != nil {
		logger.Errorf("GetActiveDirectoryForPool (vsa form) failed: %v", runErr)
		return nil, ConvertToVSAError(runErr)
	}

	svmName := diagnosisSVMRow.Name
	svmUUID := diagnosisSVMRow.SvmDetails.ExternalUUID

	// Step 8: Configure AD DNS on the diagnosis SVM.
	if runErr = workflow.ExecuteActivity(ontapCtx, adReusedActivity.CreateOrModifyADDNS, ilbNasNode, vsaAD, svmName, svmUUID).Get(ontapCtx, nil); runErr != nil {
		logger.Errorf("CreateOrModifyADDNS failed on diagnosis SVM %s: %v", svmName, runErr)
		return nil, ConvertToVSAError(runErr)
	}

	// Step 9: CIFS server create (AD machine-account create) is the actual
	// connectivity test; success here means the diagnosis passed.
	var cifsResult *active_directory_activities.GetOrCreateCifsServiceResult
	if runErr = workflow.ExecuteActivity(ontapCtx, adReusedActivity.GetOrCreateCifsService, ilbNasNode, vsaAD, svmName, svmUUID).Get(ontapCtx, &cifsResult); runErr != nil {
		logger.Errorf("GetOrCreateCifsService (domain join) failed on diagnosis SVM %s: %v", svmName, runErr)
		return nil, ConvertToVSAError(runErr)
	}
	logger.Infof("CIFS domain join succeeded on diagnosis SVM %s", svmName)

	// Managed AD is CIFS-only (SDE parity): the CIFS domain join above is the
	// only check, so LDAP and Kerberos are skipped regardless of pool.LdapEnabled
	// and ad.KdcIP.
	isManagedAD := vsaAD != nil && vsaAD.ManagedAD != nil && *vsaAD.ManagedAD

	// Step 10: Optional LDAP validation (gate: !ManagedAD && pool.LdapEnabled).
	// Uses the same ConfigureLdapForSVM activity as the volume-create LDAP path,
	// passing NFSv3+NFSv4 so the full LDAP path (incl. NFSv4 v4-id-domain) is
	// exercised.
	if !isManagedAD && pool.PoolAttributes != nil && pool.PoolAttributes.LdapEnabled {
		ldapProtocols := []string{utils.ProtocolNFSv3, utils.ProtocolNFSv4}
		if runErr = workflow.ExecuteActivity(ontapCtx, adReusedActivity.ConfigureLdapForSVM, ilbNasNode, adRecord, svmName, svmUUID, ldapProtocols).Get(ontapCtx, nil); runErr != nil {
			logger.Errorf("ConfigureLdapForSVM failed on diagnosis SVM %s: %v", svmName, runErr)
			return nil, ConvertToVSAError(runErr)
		}
	}

	// Step 11: Optional Kerberos validation (gate: !ManagedAD && ad.KdcIP != "").
	// Reuses the same EnsureKerberosConfigWorkflow as the NFSv4 volume path, so
	// the diagnosis exercises the full Kerberos path (name mapping, realm create,
	// enabling Kerberos on the ILB NAS LIF). The deferred VLM SVM delete reclaims
	// it, so this stays non-destructive. REQUEST_CANCEL mirrors the volume-create
	// call so the child is cancelled if the diagnose workflow is cancelled.
	if !isManagedAD && vsaAD != nil && vsaAD.KdcIP != "" {
		// Register the Kerberos teardown before enabling it so a partial enable
		// is still cleaned up; in LIFO this disables Kerberos on the LIF before
		// the SVM delete so ONTAP does not refuse it.
		cleanupManager.AddActivity(diagActivity.DeleteDiagnosisKerberosConfig, ilbNasNode, adRecord, svmName, svmUUID)

		krbChildCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			ParentClosePolicy: enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		})
		if runErr = workflow.ExecuteChildWorkflow(krbChildCtx, EnsureKerberosConfigWorkflow, ilbNasNode, vsaAD, svmName, svmUUID).Get(krbChildCtx, nil); runErr != nil {
			logger.Errorf("EnsureKerberosConfigWorkflow failed on diagnosis SVM %s: %v", svmName, runErr)
			return nil, ConvertToVSAError(runErr)
		}
	}

	logger.Infof("DiagnoseActiveDirectoryWorkflow succeeded for pool %s (diagnosis SVM %s)", pool.UUID, svmName)
	return nil, nil
}

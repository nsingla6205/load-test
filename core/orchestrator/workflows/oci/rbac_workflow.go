package oci

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ociRefreshRbacForPoolWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &ociRefreshRbacForPoolWorkflow{}

// OCIRefreshRbacForPoolWorkflow re-applies the RBAC role file on a single OCI pool via VLM.
//
// RBAC URL: params.RbacFileURL wins, otherwise OCI_EXPERT_MODE_RBAC_FILE_URL.
//
// Credentials are resolved the same way as pool create:
//   - ONTAP admin password comes from GetOnTapCredentialsForOCI so AuthType is
//     honored (vault lookup for USERNAME_PWD_SEC_MGR / USER_CERTIFICATE, plain
//     PoolCredentials.Password only for the default type).
//   - Expert-mode user password comes from OCI_EXPERT_MODE_PASSWORD;
func OCIRefreshRbacForPoolWorkflow(ctx workflow.Context, params *common.RefreshRbacForPoolParams, pool *datamodel.Pool) error {
	wf := new(ociRefreshRbacForPoolWorkflow)
	log := util.GetLogger(ctx)
	if err := wf.Setup(ctx, params); err != nil {
		return workflows.ConvertToVSAError(err)
	}

	wf.Status = workflows.WorkflowStatusRunning
	_, customErr := wf.Run(ctx, params, pool)
	if customErr != nil {
		log.Error("error in OCIRefreshRbacForPoolWorkflow", "error", customErr)
		wf.Status = workflows.WorkflowStatusFailed
		return workflows.ConvertToVSAError(customErr)
	}

	wf.Status = workflows.WorkflowStatusCompleted
	return nil
}

func (wf *ociRefreshRbacForPoolWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params, ok := input.(*common.RefreshRbacForPoolParams)
	if !ok || params == nil {
		return fmt.Errorf("OCIRefreshRbacForPoolWorkflow.Setup: invalid params (expected *RefreshRbacForPoolParams)")
	}
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"customerID": wf.CustomerID,
		"poolOCID":   params.PoolOCID,
	})
	wf.Logger = util.GetLogger(ctx)

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *ociRefreshRbacForPoolWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	var params *common.RefreshRbacForPoolParams
	var pool *datamodel.Pool
	if len(args) >= 2 {
		params, _ = args[0].(*common.RefreshRbacForPoolParams)
		pool, _ = args[1].(*datamodel.Pool)
	}
	if params == nil || pool == nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("OCIRefreshRbacForPoolWorkflow.Run: missing or invalid params/pool"))
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	poolActivity := &activities.PoolActivity{}

	var vlmConfig *vlm.VLMConfig
	if err := workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &vlmConfig); err != nil {
		wf.Logger.Error("Failed to parse VLM config", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	if pool.PoolCredentials == nil {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("pool credentials are required for RBAC refresh on pool %q", pool.UUID))
	}

	rbacFileURL := params.RbacFileURL
	if rbacFileURL == "" {
		rbacFileURL = ociExpertModeRbacURL
	}
	if rbacFileURL == "" {
		return nil, workflows.ConvertToVSAError(fmt.Errorf("rbacFilePath was not provided and OCI_EXPERT_MODE_RBAC_FILE_URL env var is not set"))
	}

	expertModePassword := ociExpertModePassword
	if expertModePassword == "" {
		if pool.PoolCredentials.ExpertModeSecret == nil || pool.PoolCredentials.ExpertModeSecret.ExternalIdentifier == "" {
			return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("PoolCredentials.ExpertModeSecret is required for pool %q when OCI_EXPERT_MODE_PASSWORD env var is not set", pool.UUID)))
		}
		ociAdminPassword := &common.OciAdminPassword{
			Ocid:    pool.PoolCredentials.ExpertModeSecret.ExternalIdentifier,
			Version: pool.PoolCredentials.ExpertModeSecret.Version,
		}
		expertModeCreds := &vlm.OntapCredentials{}
		if err := workflow.ExecuteActivity(ctx, poolActivity.GetExpertModeCredentialsForOCI, pool, ociAdminPassword).Get(ctx, expertModeCreds); err != nil {
			wf.Logger.Error("Failed to get expert mode password", "poolUUID", pool.UUID, "error", err)
			return nil, workflows.ConvertToVSAError(err)
		}
		if expertModeCreds.AdminPassword == "" {
			return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
				fmt.Errorf("expert mode password is empty for pool %q", pool.UUID)))
		}
		expertModePassword = expertModeCreds.AdminPassword
	}

	ontapCreds := &vlm.OntapCredentials{}
	if err := workflow.ExecuteActivity(ctx, poolActivity.GetOnTapCredentialsForOCI, pool).Get(ctx, ontapCreds); err != nil {
		wf.Logger.Error("Failed to fetch ONTAP admin credentials", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	rbacReq := &vlm.OntapExpertModeUserConfig{
		VLMConfig:                 *vlmConfig,
		OntapCredentials:          *ontapCreds,
		RbacFileURL:               rbacFileURL,
		Username:                  ociExpertModeUsername,
		AuthenticationType:        password,
		ExpertModeUserCredentials: vlm.OntapCredentials{AdminPassword: expertModePassword},
	}

	vsaClient := vlm.NewVSAClientWorkflowManager()
	rbacResp, err := vsaClient.CreateVSAExpertModeUser(ctx, rbacReq)
	if err != nil {
		wf.Logger.Error("VLM CreateVSAExpertModeUser failed", "poolUUID", pool.UUID, "error", err)
		return nil, handleActivityError(wf.Logger, err, "CreateVSAExpertModeUser", pool.UUID)
	}

	if err := workflow.ExecuteActivity(ctx, poolActivity.UpdateRbacInPoolWithURL,
		pool, rbacFileURL, rbacResp.RbacFileChecksum).Get(ctx, nil); err != nil {
		wf.Logger.Error("Failed to persist RBAC URL/checksum", "poolUUID", pool.UUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	wf.Logger.Info("OCI RBAC refresh completed", "poolOCID", params.PoolOCID, "poolUUID", pool.UUID, "rbacFileChecksum", rbacResp.RbacFileChecksum)
	return fmt.Sprintf("rbac refreshed for pool %s", pool.UUID), nil
}

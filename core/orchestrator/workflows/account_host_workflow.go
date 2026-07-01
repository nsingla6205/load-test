package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var accountHostActivityStartToClose = time.Duration(
	env.GetUint64("ACCOUNT_HOST_ACTIVITY_TIMEOUT_SEC", 600),
) * time.Second

// PinAccountHostsWorkflow provisions ONTAP expert-mode roles and service users for
// pinned account-host rows that still need credentials.
func PinAccountHostsWorkflow(ctx workflow.Context, params *orchcommon.PinAccountHostsWorkflowParams) error {
	wf := new(pinAccountHostsWorkflow)
	if err := wf.Setup(params); err != nil {
		return err
	}
	return wf.Run(ctx)
}

type pinAccountHostsWorkflow struct {
	params              *orchcommon.PinAccountHostsWorkflowParams
	retryPolicy         *WorkflowRetryPolicy
	commonActivity      *activities.CommonActivities
	accountHostActivity *activities.AccountHostActivity
}

func (wf *pinAccountHostsWorkflow) Setup(params *orchcommon.PinAccountHostsWorkflowParams) error {
	if params == nil {
		return fmt.Errorf("pin account hosts workflow params are required")
	}
	if len(params.AccountHostUUIDs) == 0 {
		return fmt.Errorf("pin account hosts workflow requires at least one account host UUID")
	}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return err
	}
	wf.params = params
	wf.retryPolicy = retryPolicy
	wf.commonActivity = &activities.CommonActivities{}
	wf.accountHostActivity = &activities.AccountHostActivity{}
	return nil
}

func (wf *pinAccountHostsWorkflow) Run(ctx workflow.Context) error {
	logger := util.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: accountHostActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        wf.retryPolicy.InitialInterval,
			BackoffCoefficient:     wf.retryPolicy.BackoffCoefficient,
			MaximumInterval:        wf.retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(wf.retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	if wf.params.JobUUID != "" {
		if err := workflow.ExecuteActivity(ctx, wf.commonActivity.UpdateJobStatus, &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: wf.params.JobUUID},
			State:     string(datamodel.JobsStatePROCESSING),
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	var failed int
	for _, accountHostUUID := range wf.params.AccountHostUUIDs {
		if err := provisionAccountHostCredentials(ctx, wf.accountHostActivity, accountHostUUID); err != nil {
			logger.Errorf("Failed to provision account host %s: %v", accountHostUUID, err)
			failed++
		}
	}

	if wf.params.JobUUID == "" {
		return nil
	}

	if failed > 0 {
		details := fmt.Sprintf("%d of %d account host(s) failed to provision ONTAP credentials", failed, len(wf.params.AccountHostUUIDs))
		return workflow.ExecuteActivity(ctx, wf.commonActivity.UpdateJobStatus, &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: wf.params.JobUUID},
			State:        string(datamodel.JobsStateERROR),
			ErrorDetails: details,
		}).Get(ctx, nil)
	}

	return workflow.ExecuteActivity(ctx, wf.commonActivity.UpdateJobStatus, &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: wf.params.JobUUID},
		State:     string(datamodel.JobsStateDONE),
	}).Get(ctx, nil)
}

func provisionAccountHostCredentials(
	ctx workflow.Context,
	accountHostActivity *activities.AccountHostActivity,
	accountHostUUID string,
) error {
	var prepared activities.PreparedAccountHostMapping
	if err := workflow.ExecuteActivity(ctx, accountHostActivity.PrepareAccountHostMapping,
		accountHostUUID).Get(ctx, &prepared); err != nil {
		return err
	}

	if err := workflow.ExecuteActivity(ctx, accountHostActivity.CreateAccountHostRole,
		&prepared).Get(ctx, &prepared); err != nil {
		return err
	}

	if err := workflow.ExecuteActivity(ctx, accountHostActivity.CreateAccountHostUser,
		&prepared).Get(ctx, nil); err != nil {
		rollbackAccountHostOntapProvisioning(ctx, accountHostActivity, &prepared)
		return err
	}
	return nil
}

func rollbackAccountHostOntapProvisioning(
	ctx workflow.Context,
	accountHostActivity *activities.AccountHostActivity,
	prepared *activities.PreparedAccountHostMapping,
) {
	if prepared == nil {
		return
	}
	_ = workflow.ExecuteActivity(ctx, accountHostActivity.RollbackAccountHostOntapProvisioning, prepared).Get(ctx, nil)
}

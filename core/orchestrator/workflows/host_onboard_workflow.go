package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var hostOnboardActivityStartToClose = time.Duration(
	env.GetUint64("HOST_ONBOARD_ACTIVITY_TIMEOUT_SEC", 600),
) * time.Second

// installHostServerCertificateRetryMaxAttempts overrides the default activity retry attempts
// (RETRY_MAX_ATTEMPTS, shared across most workflows) for InstallHostServerCertificate only. A
// higher value gives ONTAP more room to catch up on transient failures such as clock-skew-related
// certificate install rejections ("start date is later than the current date") without changing
// retry behavior for the other host onboarding activities.
var installHostServerCertificateRetryMaxAttempts = env.GetInt("HOST_ONBOARD_INSTALL_SERVER_CERT_RETRY_MAX_ATTEMPTS", 10)

// HostOnboardWorkflow completes async host onboarding against ONTAP.
type HostOnboardWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &HostOnboardWorkflow{}

// OnboardHostWorkflow is the Temporal entry point for host onboarding.
func OnboardHostWorkflow(ctx workflow.Context, params *orchcommon.OnboardHostWorkflowParams) error {
	hostWf := new(HostOnboardWorkflow)
	err := hostWf.Setup(ctx, params)
	if err != nil {
		return err
	}

	logger := util.GetLogger(ctx)
	logger.Infof("Starting host onboard workflow for host %s (jobUUID=%s)", params.HostUUID, params.JobUUID)
	hostWf.Status = WorkflowStatusRunning
	err = hostWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		hostWf.Status = WorkflowStatusFailed
		err = hostWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}

	_, customErr := hostWf.Run(ctx, params)
	if customErr != nil {
		hostWf.Status = WorkflowStatusFailed
		err = hostWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return err
	}

	hostWf.Status = WorkflowStatusCompleted
	err = hostWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Errorf("Failed to update host onboard job status to DONE: %v", err)
		return err
	}
	logger.Infof("Host onboard workflow completed successfully for host %s", params.HostUUID)
	return nil
}

func (wf *HostOnboardWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	hostOnboardParams := input.(*orchcommon.OnboardHostWorkflowParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = hostOnboardParams.HostUUID
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *HostOnboardWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*orchcommon.OnboardHostWorkflowParams)
	hostOnboardActivity := &activities.HostOnboardActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: hostOnboardActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	installServerCertAO := ao
	installServerCertRetryPolicy := *ao.RetryPolicy
	installServerCertRetryPolicy.MaximumAttempts = int32(installHostServerCertificateRetryMaxAttempts)
	installServerCertAO.RetryPolicy = &installServerCertRetryPolicy
	installServerCertCtx := workflow.WithActivityOptions(ctx, installServerCertAO)

	hostResult := orchcommon.HostOnboardResult{
		HostUUID: params.HostUUID,
		DbHost:   params.DbHost,
	}

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, hostOnboardActivity.MarkHostOnboardFailed, params.HostUUID, err.Error()).Get(ctx, nil)
		}
	}()

	if err = workflow.ExecuteActivity(ctx, hostOnboardActivity.DiscoverHost, &hostResult).Get(ctx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = workflow.ExecuteActivity(ctx, hostOnboardActivity.GenerateHostCertificate, &hostResult).Get(ctx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = workflow.ExecuteActivity(ctx, hostOnboardActivity.CreateHostCloudDNSRecord, &hostResult).Get(ctx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = workflow.ExecuteActivity(installServerCertCtx, hostOnboardActivity.InstallHostServerCertificate, &hostResult).Get(installServerCertCtx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = workflow.ExecuteActivity(ctx, hostOnboardActivity.InstallHostCACertificate, &hostResult).Get(ctx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err = workflow.ExecuteActivity(ctx, hostOnboardActivity.OnboardHost, &hostResult).Get(ctx, &hostResult); err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}

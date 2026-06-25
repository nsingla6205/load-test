package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/resource_events_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	finishProjectCVPJobRetryMaxAttempts           = env.GetInt("FINISH_PROJECT_CVP_CLIENT_RETRY_MAX_ATTEMPTS", 20)
	finishProjectInitialRetryIntervalForCVPClient = env.GetString("FINISH_PROJECT_CVP_CLIENT_RETRY_INTERVAL", "30s")
	finishProjectBackoffCoefficientForCVPClient   = env.GetFloat64("FINISH_PROJECT_CVP_CLIENT_BACKOFF_COEFFICIENT", 1.0)
	hardDeleteResources                           = env.GetBool("HARD_DELETE_RESOURCES", true)
)

// FinishProjectEventDeleteStateWorkflow is a workflow that handles the DELETE state for FinishProjectEvent.
type finishProjectEventDeleteStateWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

// FinishProjectEventDeleteStateWorkflow is a workflow that handles the DELETE state for FinishProjectEvent.
func FinishProjectEventDeleteStateWorkflow(ctx workflow.Context, params *common.FinishProjectEventParams) (interface{}, error) {
	log := util.GetLogger(ctx)
	finishProjectEventWorkflow := new(finishProjectEventDeleteStateWorkflow)
	var errRun *vsaerrors.CustomError

	err := finishProjectEventWorkflow.Setup(ctx, params)
	if err != nil {
		errRun = ConvertToVSAError(err)
		return nil, errRun
	}

	defer func() {
		if errRun != nil {
			finishProjectEventWorkflow.Status = WorkflowStatusFailed
			err := finishProjectEventWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errRun)
			if err != nil {
				log.Errorf("finishProjectEventDeleteStateWorkflow failed to update job status: %v", err)
			}
		} else {
			finishProjectEventWorkflow.Status = WorkflowStatusCompleted
			err := finishProjectEventWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
			if err != nil {
				log.Errorf("finishProjectEventDeleteStateWorkflow failed to update job status: %v", err)
			}
		}
	}()

	finishProjectEventWorkflow.Status = WorkflowStatusRunning
	err = finishProjectEventWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		errRun = ConvertToVSAError(err)
		return nil, errRun
	}

	_, errRun = finishProjectEventWorkflow.Run(ctx, params)
	if errRun != nil {
		log.Errorf("finishProjectEventDeleteStateWorkflow completed with error: %v", errRun)
		return nil, errRun
	}
	log.Infof("finishProjectEventDeleteStateWorkflow completed successfully")
	return nil, nil
}

func (s *finishProjectEventDeleteStateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	finishProjectEventDeleteStateParams := input.(*common.FinishProjectEventParams)
	info := workflow.GetInfo(ctx)
	s.CustomerID = finishProjectEventDeleteStateParams.ProjectNumber
	s.Status = WorkflowStatusCreated
	s.ID = info.WorkflowExecution.ID
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": s.ID, "customerID": s.CustomerID})
	logger := util.GetLogger(ctx)
	s.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         s.ID,
			Status:     s.Status,
			CustomerID: s.CustomerID,
		}, nil
	})
}

func (s *finishProjectEventDeleteStateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	finishProjectEventParams := args[0].(*common.FinishProjectEventParams)
	finishProjectEventActivity := &resource_events_activities.FinishProjectEventActivity{}
	logger := util.GetLogger(ctx)
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
		},
	}

	aoCVP := ao
	aoCVP.RetryPolicy.InitialInterval, err = time.ParseDuration(finishProjectInitialRetryIntervalForCVPClient)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	aoCVP.RetryPolicy.MaximumAttempts = int32(finishProjectCVPJobRetryMaxAttempts)
	aoCVP.RetryPolicy.BackoffCoefficient = finishProjectBackoffCoefficientForCVPClient

	ctx = workflow.WithActivityOptions(ctx, ao)
	ctxCVP := workflow.WithActivityOptions(ctx, aoCVP)

	if cvp.CVP_HOST == "" {
		return nil, nil
	}

	// TODO: add VSA cluster power on activity
	var result *common.FinishProjectEventResult
	err = workflow.ExecuteActivity(ctx, finishProjectEventActivity.FinishProjectEventForSDEActivity, finishProjectEventParams).Get(ctx, &result)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctxCVP, finishProjectEventActivity.PollFinishProjectEventSDEOperationActivity, finishProjectEventParams, &result).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	// skipping this code block if this is a zonal call
	if finishProjectEventParams.Zone == "" {
		// Delete hostGroup from VCP.
		HostGroupActivities := &activities.HostGroupUpdateActivity{}
		var listOfHostGroups []*datamodel.HostGroup
		errHostGroup := workflow.ExecuteActivity(ctx, HostGroupActivities.ListHostGroups, finishProjectEventParams.ProjectNumber).Get(ctx, &listOfHostGroups)
		if errHostGroup != nil {
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorListingResources,
					fmt.Errorf("error listing HostGroup %w", errHostGroup))))
		}
		if len(listOfHostGroups) > 0 {
			for _, hostGroup := range listOfHostGroups {
				errDeleteHG := workflow.ExecuteActivity(ctx, HostGroupActivities.DeleteHostGroup, hostGroup.UUID, hostGroup.AccountID).Get(ctx, nil)
				if errDeleteHG != nil {
					return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
						vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
							fmt.Errorf("error deleting HostGroup %w", errDeleteHG))))
				}
			}
		}
		// Delete Active Directory from VCP/CVP. Multiple ADs can exist per account.
		adActivities := &active_directory_activities.ActiveDirectoryDeleteActivity{}
		var activeDirectories []*datamodel.ActiveDirectory
		err = workflow.ExecuteActivity(ctx, adActivities.ListActiveDirectoriesActivity, finishProjectEventParams.ProjectNumber).Get(ctx, &activeDirectories)
		if err != nil {
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorListingResources,
					fmt.Errorf("error listing Active Directories %w", err))))
		}
		for _, ad := range activeDirectories {
			if ad == nil {
				continue
			}
			deleteParams := &common.DeleteActiveDirectoryParams{
				ProjectNumber:       finishProjectEventParams.ProjectNumber,
				AccountId:           ad.AccountId,
				ActiveDirectoryUUID: ad.UUID,
			}
			if err = workflow.ExecuteActivity(ctx, adActivities.DeleteVcpActiveDirectory, deleteParams).Get(ctx, nil); err != nil {
				return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
						fmt.Errorf("error deleting Active Directory from VCP %w", err))))
			}
		}
		// Delete KMS config from VCP. As this is common resource it might have deleted in SDE handle resource delete activity.
		kmsActivities := &kms_activities.KmsConfigActivity{}
		var kmsConfigs []*datamodel.KmsConfig
		err = workflow.ExecuteActivity(ctx, kmsActivities.ListKmsConfigActivity, finishProjectEventParams.ProjectNumber).Get(ctx, &kmsConfigs)
		if err != nil {
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorListingResources,
					fmt.Errorf("error listing KMS config %w", err))))
		}
		for _, kmsConfig := range kmsConfigs {
			if kmsConfig == nil {
				continue
			}
			err = workflow.ExecuteActivity(ctx, kmsActivities.DeleteKmsConfig, kmsConfig,
				&common.DeleteKmsConfigParams{KmsConfigID: kmsConfig.UUID}).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
						fmt.Errorf("error deleting KMS config %s: %w", kmsConfig.UUID, err))))
			}
		}

		// Check if pools exist before proceeding with backup cleanup
		poolActivity := &activities.PoolActivity{}
		var pools []*datamodel.Pool
		err = workflow.ExecuteActivity(ctx, poolActivity.GetPoolsByAccountName, finishProjectEventParams.ProjectNumber).Get(ctx, &pools)
		if err != nil {
			logger := util.GetLogger(ctx)
			logger.Errorf("Failed to check pools for project %s: %v", finishProjectEventParams.ProjectNumber, err)
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
					fmt.Errorf("error checking pools for backup cleanup %w", err))))
		}

		// Check if pools exist - if they do, skip backup cleanup
		if len(pools) > 0 {
			logger := util.GetLogger(ctx)
			logger.Warnf("Skipping backup cleanup for project %s because pools or volumes still exist",
				finishProjectEventParams.ProjectNumber)
		} else {
			// Cleanup backup resources for the project with default retry configuration
			backupVaultActivity := &activities.BackupVaultActivity{}
			backupPolicyActivity := &activities.BackupPolicyActivity{}

			// Use default retry policy for backup activities
			backupRetryPolicy, err := PopulateRetryPolicyParams()
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
			backupAO := workflow.ActivityOptions{
				StartToCloseTimeout: backupRetryPolicy.StartToCloseTimeout,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:        backupRetryPolicy.InitialInterval,
					BackoffCoefficient:     backupRetryPolicy.BackoffCoefficient,
					MaximumInterval:        backupRetryPolicy.MaximumInterval,
					MaximumAttempts:        int32(backupRetryPolicy.MaximumAttempts),
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctxBackup := workflow.WithActivityOptions(ctx, backupAO)

			// Cleanup backup resources - try to clean up as much as possible
			var backupCleanupErrors []error
			logger := util.GetLogger(ctx)

			// Cleanup backup vaults and their associated backups
			err = workflow.ExecuteActivity(ctxBackup, backupVaultActivity.CleanupBackupVaultsForAccount, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
			if err != nil {
				logger.Errorf("Failed to cleanup backup vaults for project %s: %v", finishProjectEventParams.ProjectNumber, err)
				backupCleanupErrors = append(backupCleanupErrors, err)
			} else {
				logger.Infof("Successfully cleaned up backup vaults for project %s", finishProjectEventParams.ProjectNumber)
			}

			// Cleanup backup policies and their temporal schedulers
			err = workflow.ExecuteActivity(ctxBackup, backupPolicyActivity.CleanupBackupPoliciesForAccount, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
			if err != nil {
				logger.Errorf("Failed to cleanup backup policies for project %s: %v", finishProjectEventParams.ProjectNumber, err)
				backupCleanupErrors = append(backupCleanupErrors, err)
			} else {
				logger.Infof("Successfully cleaned up backup policies for project %s", finishProjectEventParams.ProjectNumber)
			}

			// Log summary of backup cleanup
			if len(backupCleanupErrors) == 0 {
				logger.Infof("Backup cleanup completed successfully for project %s", finishProjectEventParams.ProjectNumber)
			} else {
				logger.Warnf("Backup cleanup completed with %d errors for project %s", len(backupCleanupErrors), finishProjectEventParams.ProjectNumber)
				// Note: We don't return error here to allow the workflow to continue with other cleanup activities
			}
		}

		err = workflow.ExecuteActivity(ctx, finishProjectEventActivity.DeleteServiceAccountsFromAccountID, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
		if err != nil {
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
					fmt.Errorf("error delete service account %w", err))))
		}
	}

	var RegionalResourceCheck bool
	err = workflow.ExecuteActivity(ctx, finishProjectEventActivity.VolumeAndPoolRegionalCheckActivity, finishProjectEventParams.ProjectNumber).Get(ctx, &RegionalResourceCheck)
	if err != nil {
		return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorListingResources,
				fmt.Errorf("error listing regional resource %w", err))))
	}

	if !RegionalResourceCheck {
		logger.Infof("Account has active resources hence ignoring deleting the account. Account will be deleted as part of next finish project event.")
		return nil, nil
	}

	err = workflow.ExecuteActivity(ctx, finishProjectEventActivity.DeleteAccountActivity, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventErrorDeletingResources,
				fmt.Errorf("error deleting account %w", err))))
	}

	var errRollBack error
	defer func() {
		if errRollBack != nil {
			err = workflow.ExecuteActivity(ctx, finishProjectEventActivity.RollbackAccountStateActivity, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
			if err != nil {
				s.Logger.Errorf("RollbackAccountStateActivity failed: %v", err)
			}
		}
	}()

	if hardDeleteResources {
		var canHardDelete bool
		errRollBack = workflow.ExecuteActivity(ctx, finishProjectEventActivity.VerifySoftDeletedResourcesForAccount, finishProjectEventParams.ProjectNumber).Get(ctx, &canHardDelete)
		if errRollBack != nil {
			return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventHardDeleteResources,
					fmt.Errorf("error verifying soft deleted resources %w", errRollBack))))
		}
		if canHardDelete {
			errRollBack = workflow.ExecuteActivity(ctx, finishProjectEventActivity.HardDeleteResourcesInOrder, finishProjectEventParams.ProjectNumber).Get(ctx, nil)
			if errRollBack != nil {
				return nil, ConvertToVSAError(vsaerrors.WrapAsNonRetryableTemporalApplicationError(
					vsaerrors.NewVCPError(vsaerrors.ErrFinishProjectEventHardDeleteResources,
						fmt.Errorf("error Hard deleting resources %w", errRollBack))))
			}
		}
	}

	return nil, ConvertToVSAError(err)
}

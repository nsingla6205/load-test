package workflows

import (
	"errors"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	JobActionDelete = "delete"
)

type quotaRuleDeleteWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

var _ WorkflowInterface = &quotaRuleDeleteWorkflow{}

// DeleteQuotaRuleWorkflow processes quota rule delete requests from a customer.
func DeleteQuotaRuleWorkflow(ctx workflow.Context, params *common.DeleteQuotaRulesParam, quotaRule *datamodel.QuotaRule) (gcpgenserver.V1betaDeleteQuotaRuleVCPRes, error) {
	logger := workflow.GetLogger(ctx)
	quotaRuleWf := new(quotaRuleDeleteWorkflow)
	err := quotaRuleWf.Setup(ctx, params)
	if err != nil {
		logger.Error("Quota rule delete workflow setup executed with error", "error", err)
		return nil, err
	}
	// Ensure job is available before updating status
	if err := quotaRuleWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		logger.Error("Quota rule delete workflow job state check executed with error", "error", err)
		return nil, ConvertToVSAError(err)
	}
	quotaRuleWf.Status = WorkflowStatusRunning
	err = quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Error("Update job status for quota rule delete executed with error", "error", err)
		return nil, err
	}
	_, customErr := quotaRuleWf.Run(ctx, quotaRule, params)
	if customErr != nil {
		logger.Error("Quota rule delete workflow run executed with error", "error", customErr)
		quotaRuleWf.Status = WorkflowStatusFailed
		jobUpdateErr := quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if jobUpdateErr != nil {
			logger.Error("Failed to update job status to Error for DeleteQuotaRuleWorkflow", "error", jobUpdateErr)
			return nil, jobUpdateErr
		}
		return nil, customErr
	}
	quotaRuleWf.Status = WorkflowStatusCompleted
	err = quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Error("Failed to update job status to Done for DeleteQuotaRuleWorkflow", "error", err)
	}
	logger.Debug("Delete Quota Rule workflow completed successfully")
	return nil, nil
}

// Setup initializes the workflow with the necessary parameters and sets up a query handler for status updates.
func (wf *quotaRuleDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteQuotaRuleParams := input.(*common.DeleteQuotaRulesParam)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteQuotaRuleParams.ProjectId
	wf.Status = WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

// Run executes the quota rule delete workflow.
func (wf *quotaRuleDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (result interface{}, customErr *vsaerrors.CustomError) {
	quotaRule := args[0].(*datamodel.QuotaRule)
	params := args[1].(*commonparams.DeleteQuotaRulesParam)
	logger := workflow.GetLogger(ctx)
	quotaRuleDeleteActivity := &activities.QuotaRuleDeleteActivity{}
	commonActivity := &activities.QuotaRuleCommonActivity{}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(startToCloseTimeoutQuotaRuleActivitySec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	cancellationActivity := &activities.CancellationActivity{}
	commonAct := &activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	ackTimeout, forceTimeout := commonparams.GetCancellationTimeouts("QUOTA_RULE")
	if cancelErr := commonparams.HandleCancellationForCreatingResource(ctx, logger,
		commonparams.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               quotaRule.UUID,
			ResourceState:              quotaRule.State,
			CreateJobType:              datamodel.JobTypeCreateQuotaRule,
			SignalName:                 CancelQuotaRuleSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonAct,
	); cancelErr != nil {
		logger.Warn("Error handling cancellation, proceeding with normal delete", "error", cancelErr)
	}

	// Defer function to mark the database entry in error state if any error occur
	defer func() {
		if err != nil {
			// On error, mark quota rule in error state
			quotaRule.State = datamodel.LifeCycleStateError
			quotaRule.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
			err2 := workflow.ExecuteActivity(ctx, commonActivity.UpdateQuotaRuleState, *quotaRule, false).Get(ctx, nil)
			if err2 != nil {
				logger.Error("Failed to update quota rule state in DB to error", "error", err2)
			}
		}
		if err == nil {
			err = workflow.ExecuteActivity(ctx, commonActivity.UpdateQuotaRuleState, *quotaRule, quotaRule.State == datamodel.LifeCycleStateCreating).Get(ctx, nil)
			if err != nil {
				logger.Error("Failed to update quota rule state on source", "error", err)
				customErr = ConvertToVSAError(err)
			}
		}
	}()

	dbQuotaRule := quotaRule

	// Fetch volume details as the first activity and perform DP check
	var volumeDetails *datamodel.Volume
	err = workflow.ExecuteActivity(ctx, commonActivity.GetVolumeByID, dbQuotaRule.VolumeID, dbQuotaRule.AccountID).Get(ctx, &volumeDetails)
	if err != nil {
		logger.Error("Failed to fetch volume details for DP validation", "error", err)
		return nil, ConvertToVSAError(err)
	}

	isDataProtection := false
	if volumeDetails != nil && volumeDetails.VolumeAttributes != nil {
		isDataProtection = volumeDetails.VolumeAttributes.IsDataProtection
	}

	// If volume is data protection, only delete from database (no ONTAP deletion needed).
	// Defer block will mark quota rule as DELETED on success.
	if isDataProtection {
		logger.Info("DP volume: quota rule delete skipped (DB-only), returning success")
		return nil, nil
	}

	// Preserve original quota rule data for revert in case source deletion fails
	originalQuotaRule := &datamodel.QuotaRule{
		QuotaType:      dbQuotaRule.QuotaType,
		QuotaTarget:    dbQuotaRule.QuotaTarget,
		DiskLimitInKib: dbQuotaRule.DiskLimitInKib,
		Description:    dbQuotaRule.Description,
		RQuota:         dbQuotaRule.RQuota,
		Name:           dbQuotaRule.Name,
	}

	// Replication sync: Delete quota rule on destination first (before deleting on source)
	var replications []*datamodel.VolumeReplication
	err = workflow.ExecuteActivity(ctx, commonActivity.GetVolumeReplication, volumeDetails.ID).Get(ctx, &replications)
	if err != nil {
		logger.Error("Failed to fetch volume replication details for destination sync", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// Store replication info for revert if needed
	var replicationForRevert *datamodel.VolumeReplication
	var destProjectNumberForRevert string
	var destinationQuotaRuleIdForRevert *string
	var jwtTokenForRevert *string
	var destinationDeleted, sourceDeletionCompleted bool // Flag to track if destination deletion succeeded

	// Check if there are any replications
	if len(replications) == 0 {
		logger.Info("No replications found for volume, skipping destination quotaRule delete")
	} else {
		// At present we can have only 1 replication for a region, so use the first replication
		replication := replications[0]
		if replication == nil || replication.ReplicationAttributes == nil {
			err = errors.New("replication is nil or has no attributes")
			return nil, ConvertToVSAError(err)
		} else {
			// Verify if this replication is eligible for destination sync
			// This activity validates that replication is eligible for quota sync (MIRRORED or UNINITIALIZED state)
			var isEligible bool
			err = workflow.ExecuteActivity(ctx, commonActivity.VerifyReplicationState,
				replication, params.LocationId).Get(ctx, &isEligible)
			if err != nil {
				logger.Error("Replication state validation failed for destination sync", "error", err)
				return nil, ConvertToVSAError(err)
			}

			// Sync quota rule deletion only if replication is eligible
			if isEligible {
				var destProjectNumber string
				destProjectNumber, err = utils.ParseProjectNumberFromURI(replication.RemoteUri)
				if err != nil {
					logger.Error("Failed to parse destination project number from RemoteUri", "error", err, "remoteUri", replication.RemoteUri)
					return nil, ConvertToVSAError(err)
				}

				// Store for revert
				replicationForRevert = replication
				destProjectNumberForRevert = destProjectNumber

				// Create JWT token once for reuse across all destination API calls
				var jwtToken *string
				err = workflow.ExecuteActivity(ctx, commonActivity.GetSignedDstTokenForQuotaRule, destProjectNumber).Get(ctx, &jwtToken)
				if err != nil {
					logger.Error("Failed to get JWT token for destination project", "destProjectNumber", destProjectNumber, "error", err)
					return nil, ConvertToVSAError(err)
				}

				// Store JWT token for revert
				jwtTokenForRevert = jwtToken

				// Fetch and match quota rule on destination volume
				var destinationQuotaRuleId *string
				err = workflow.ExecuteActivity(ctx, commonActivity.GetMatchingQuotaRuleOnDestination,
					replication.ReplicationAttributes.DestinationVolumeUUID, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, dbQuotaRule.Name, jwtToken).Get(ctx, &destinationQuotaRuleId)
				if err != nil {
					// Treat "matching quota rule not found" as success (already deleted on destination in a previous run).
					// TrackingID is the reliable check when error comes from activity (ExtractCustomError decodes it from ApplicationError details).
					ce := ConvertToVSAError(err)
					isMatchingNotFound := customerrors.IsNotFoundErr(err) ||
						(ce != nil && (ce.TrackingID == vsaerrors.ErrQuotaRuleNotFound || ce.TrackingID == vsaerrors.ErrMatchingQuotaRuleNotFoundOnDestination))
					if isMatchingNotFound {
						logger.Info("Matching quota rule not found on destination (may already be deleted), skipping destination delete", "destinationVolumeUUID", replication.ReplicationAttributes.DestinationVolumeUUID, "quotaRuleName", dbQuotaRule.Name)
						// destinationQuotaRuleId stays nil; destination delete block below is skipped
					} else {
						logger.Error("Failed to fetch matching quota rule from destination", "destinationVolumeUUID", replication.ReplicationAttributes.DestinationVolumeUUID, "quotaRuleName", dbQuotaRule.Name, "error", err)
						return nil, ConvertToVSAError(err)
					}
				}

				// Only delete on destination when we found a matching quota rule
				if destinationQuotaRuleId != nil {
					// Store for revert
					destinationQuotaRuleIdForRevert = destinationQuotaRuleId

					// Delete quota rule on destination via internal API
					var deleteOperationResult *activities.QuotaRuleOperationResult
					err = workflow.ExecuteActivity(ctx, quotaRuleDeleteActivity.DeleteQuotaRuleOnDestination,
						replication.ReplicationAttributes.DestinationVolumeUUID, *destinationQuotaRuleId, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, jwtToken).Get(ctx, &deleteOperationResult)
					if err != nil {
						logger.Error("Failed to delete quota rule on destination", "destinationVolumeUUID", replication.ReplicationAttributes.DestinationVolumeUUID, "destinationQuotaRuleId", *destinationQuotaRuleId, "error", err)
						// If destination deletion fails, we can't proceed with source deletion
						return nil, ConvertToVSAError(err)
					}

					// Poll for operation completion if async operation was started
					if deleteOperationResult != nil && deleteOperationResult.OperationName != "" && !deleteOperationResult.IsDone {
						logger.Info("Polling for quota rule delete operation completion on destination", "operationName", deleteOperationResult.OperationName)
						err = workflow.ExecuteActivity(ctx, commonActivity.DescribeQuotaRuleRemoteJob,
							deleteOperationResult.OperationName, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, jwtToken).Get(ctx, nil)
						if err != nil {
							logger.Error("Failed to wait for quota rule delete on destination", "operationName", deleteOperationResult.OperationName, "error", err)
							// If polling fails, we can't proceed with source deletion
							return nil, ConvertToVSAError(err)
						}
					}

					// Hydrate the quota rule deletion to CCFE after polling completes (or immediately if operation completed synchronously)
					// Use quota rule name and destination volume name for hydration (CCFE expects resource names)
					quotaRuleName := dbQuotaRule.Name
					if hydrationEnabled {
						err = workflow.ExecuteActivity(ctx, commonActivity.HydrateQuotaRuleDelete,
							quotaRuleName, replication.ReplicationAttributes.DestinationVolumeName,
							replication.ReplicationAttributes.DestinationLocation, destProjectNumber).Get(ctx, nil)
						if err != nil {
							logger.Error("Failed to hydrate quota rule delete to CCFE", "quotaRuleName", quotaRuleName, "error", err)
							return nil, ConvertToVSAError(err)
						}
						logger.Info("Successfully hydrated quota rule delete to CCFE", "quotaRuleName", quotaRuleName)
					}

					logger.Info("Successfully deleted quotaRule on destination", "destinationLocation", replication.ReplicationAttributes.DestinationLocation, "destinationVolumeUUID", replication.ReplicationAttributes.DestinationVolumeUUID, "destinationQuotaRuleId", *destinationQuotaRuleId)

					// Mark that destination deletion succeeded - this enables revert if source deletion fails
					destinationDeleted = true
				}
			} else {
				logger.Info("Replication is not eligible for sync", "destinationLocation", replication.ReplicationAttributes.DestinationLocation, "destinationVolumeUUID", replication.ReplicationAttributes.DestinationVolumeUUID)
			}
		}
	}

	// Defer function to revert destination quota rule deletion if source deletion fails
	// This is placed here (before source deletion operations) so it executes before the error state defer
	// Defer executes in LIFO order, so this will run first, then the error state defer
	defer func() {
		if err != nil && destinationDeleted && !sourceDeletionCompleted && replicationForRevert != nil &&
			replicationForRevert.ReplicationAttributes != nil && destinationQuotaRuleIdForRevert != nil {
			logger.Warn("Reverting destination quota rule deletion due to source deletion failure")
			// Get JWT token for revert (reuse if available, otherwise get new one)
			var jwtToken *string
			if jwtTokenForRevert != nil {
				jwtToken = jwtTokenForRevert
			} else {
				jwtTokenErr := workflow.ExecuteActivity(ctx, commonActivity.GetSignedDstTokenForQuotaRule, destProjectNumberForRevert).Get(ctx, &jwtToken)
				if jwtTokenErr != nil {
					logger.Error("Failed to get JWT token for revert", "error", jwtTokenErr)
					return
				}
			}

			var revertResult *activities.RevertQuotaRuleResult
			revertErr := workflow.ExecuteActivity(ctx, quotaRuleDeleteActivity.RevertQuotaRuleOnDestinationForDelete,
				replicationForRevert.ReplicationAttributes.DestinationVolumeUUID, originalQuotaRule,
				replicationForRevert.ReplicationAttributes.DestinationLocation, destProjectNumberForRevert, jwtToken).Get(ctx, &revertResult)
			if revertErr != nil {
				logger.Error("Failed to revert quota rule on destination", "error", revertErr)
			} else if revertResult != nil {
				// Poll for operation completion if async operation was started
				if revertResult.OperationResult != nil && revertResult.OperationResult.OperationName != "" && !revertResult.OperationResult.IsDone {
					logger.Info("Polling for quota rule revert operation completion on destination", "operationName", revertResult.OperationResult.OperationName)
					pollErr := workflow.ExecuteActivity(ctx, commonActivity.DescribeQuotaRuleRemoteJob,
						revertResult.OperationResult.OperationName, replicationForRevert.ReplicationAttributes.DestinationLocation, destProjectNumberForRevert, jwtToken).Get(ctx, nil)
					if pollErr != nil {
						logger.Error("Failed to wait for quota rule revert on destination", "operationName", revertResult.OperationResult.OperationName, "destinationVolumeUUID", replicationForRevert.ReplicationAttributes.DestinationVolumeUUID, "error", pollErr)
						return
					}
				}

				if hydrationEnabled && revertResult.QuotaRule != nil {
					// Hydrate the quota rule creation to CCFE after successful revert using the destination quota rule (revertResult.QuotaRule)
					hydrateErr := workflow.ExecuteActivity(ctx, commonActivity.HydrateQuotaRuleCreate,
						revertResult.QuotaRule, replicationForRevert.ReplicationAttributes.DestinationVolumeName,
						replicationForRevert.ReplicationAttributes.DestinationLocation, destProjectNumberForRevert).Get(ctx, nil)
					if hydrateErr != nil {
						logger.Error("Failed to hydrate quota rule create to CCFE after revert", "quotaRuleName", revertResult.QuotaRule.Name, "error", hydrateErr)
						return
					}
					logger.Info("Successfully hydrated quota rule create to CCFE after revert", "quotaRuleName", revertResult.QuotaRule.Name)
				}
			}
		}
	}()

	logger.Info("Start Source quotaRule deletion")

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, volumeDetails.PoolID).Get(ctx, &dbNodes)
	if err != nil {
		logger.Error("Failed to get nodes for pool", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// Create node structure for provider - this will be passed to activities
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volumeDetails.Pool.DeploymentName,
		OntapCredentials: volumeDetails.Pool.PoolCredentials,
	})

	// Get ONTAP quota UUID for the quota rule
	var quotaUUID string
	err = workflow.ExecuteActivity(ctx, commonActivity.GetOntapQuotaUUID,
		volumeDetails, node, dbQuotaRule.QuotaType, dbQuotaRule.QuotaTarget, JobActionDelete).Get(ctx, &quotaUUID)

	if err != nil {
		logger.Error("Failed to get quota UUID from ONTAP", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// Check if quotaUUID is empty (quota rule not found on ONTAP - already deleted)
	if quotaUUID == "" {
		logger.Info("Quota rule not found on ONTAP (already deleted), marking as deleted in database")
		return nil, nil
	}

	// Delete quota rule on source ONTAP
	var deleteQuotaRuleResp *vsa.JobStatus
	err = workflow.ExecuteActivity(ctx, quotaRuleDeleteActivity.DeleteQuotaRuleOnOntap,
		quotaUUID, node).Get(ctx, &deleteQuotaRuleResp)
	if err != nil {
		logger.Error("Failed to delete quota rule on source ONTAP", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// List quota rules to check if this is the last one
	var quotaRuleList []*datamodel.QuotaRule
	err = workflow.ExecuteActivity(ctx, quotaRuleDeleteActivity.ListQuotaRulesForVolume,
		volumeDetails.UUID, volumeDetails.AccountID).Get(ctx, &quotaRuleList)
	if err != nil {
		logger.Error("Failed to list quota rules for volume", "error", err)
		return nil, ConvertToVSAError(err)
	}

	// Handle delete response - check if failure requires quota disable or reinitialization
	if deleteQuotaRuleResp != nil && deleteQuotaRuleResp.State == vsa.JobRespFailure {
		message := strings.ToLower(deleteQuotaRuleResp.Message)
		if strings.Contains(message, strings.ToLower(vsa.DeletedRuleEnforced)) ||
			strings.Contains(message, strings.ToLower(vsa.DeleteRuleResizeFailed)) {
			// When last quota is getting deleted then disable the quota
			if len(quotaRuleList) == 1 {
				var quotaDisableResp *vsa.JobStatus
				err = workflow.ExecuteActivity(ctx, commonActivity.HandleQuotaEnableDisable,
					node, volumeDetails, false).Get(ctx, &quotaDisableResp)
				if err != nil {
					logger.Error("Failed to disable quota on volume", "error", err)
					return nil, ConvertToVSAError(err)
				}

				if quotaDisableResp != nil && quotaDisableResp.State == vsa.JobRespFailure {
					logger.Error("Quota disable failed", "message", quotaDisableResp.Message)
					err = customerrors.NewUserInputValidationErr(quotaDisableResp.Message)
					return nil, ConvertToVSAError(err)
				}
			} else {
				// Perform quota reinitialization
				err = workflow.ExecuteActivity(ctx, commonActivity.QuotaReinitialization,
					node, volumeDetails).Get(ctx, nil)
				if err != nil {
					logger.Error("Quota reinitialization failed", "error", err)
					return nil, ConvertToVSAError(err)
				}
			}
		} else {
			// Other failure - set err so defer marks quota rule in error state; revert will be handled by defer
			logger.Error("Quota delete failed with unexpected error", "message", deleteQuotaRuleResp.Message)
			err = customerrors.NewUserInputValidationErr(deleteQuotaRuleResp.Message)
			return nil, ConvertToVSAError(err)
		}
	}

	isRQuotaEnabled := dbQuotaRule.RQuota
	// Update RQuota on SVM if required (determined by orchestrator)
	// This must be done after deleting the quota rule from ONTAP
	if !isRQuotaEnabled {
		// Extract SVM ExternalUUID from volumeDetails
		if volumeDetails.Svm == nil || volumeDetails.Svm.SvmDetails == nil || volumeDetails.Svm.SvmDetails.ExternalUUID == "" {
			logger.Error("Volume has no SVM details or ExternalUUID")
			err = vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, customerrors.NewUserInputValidationErr("Volume has no SVM details or ExternalUUID"))
			return nil, ConvertToVSAError(err)
		}
		svmExternalUUID := volumeDetails.Svm.SvmDetails.ExternalUUID
		// Disable RQuota since we're deleting the quota rule
		err = workflow.ExecuteActivity(ctx, commonActivity.UpdateRQuotaOnSvm, svmExternalUUID, node, isRQuotaEnabled).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to disable RQuota on SVM", "error", err)
			return nil, ConvertToVSAError(err)
		}
	}

	// Mark that source deletion operations completed successfully
	// This prevents revert if only the final UpdateQuotaRuleState fails
	sourceDeletionCompleted = true

	logger.Info("Quota rule delete on ontap completed successfully")
	return nil, nil
}

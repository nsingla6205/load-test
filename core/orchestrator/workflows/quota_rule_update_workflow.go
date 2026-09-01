package workflows

import (
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	JobActionUpdate = "update"
)

type quotaRuleUpdateWorkflow struct {
	BaseWorkflow
	SE database.Storage
}

var _ WorkflowInterface = &quotaRuleUpdateWorkflow{}

// UpdateQuotaRuleWorkflow processes quota rule update requests from a customer.
func UpdateQuotaRuleWorkflow(ctx workflow.Context, params *common.UpdateQuotaRulesParam, quotaRule *datamodel.QuotaRule) (gcpgenserver.V1betaUpdateQuotaRuleRes, error) {
	logger := util.GetLogger(ctx)
	quotaRuleWf := new(quotaRuleUpdateWorkflow)
	err := quotaRuleWf.Setup(ctx, params)
	if err != nil {
		logger.Infof("Quota rule workflow setup executed with error: %v", err)
		return nil, err
	}
	// Ensure job is available before updating status
	if err := quotaRuleWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		logger.Infof("Quota rule workflow job state check executed with error: %v", err)
		return nil, ConvertToVSAError(err)
	}
	quotaRuleWf.Status = WorkflowStatusRunning
	err = quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Infof("Update job status for quota rule executed with error: %v", err)
		return nil, err
	}
	_, customErr := quotaRuleWf.Run(ctx, quotaRule, params)
	if customErr != nil {
		logger.Infof("Quota rule workflow run executed with error: %v", customErr)
		quotaRuleWf.Status = WorkflowStatusFailed
		jobUpdateErr := quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if jobUpdateErr != nil {
			logger.Errorf("Failed to update job status to Error for UpdateQuotaRuleWorkflow: %v", jobUpdateErr)
			return nil, jobUpdateErr
		}
		return nil, customErr
	}
	quotaRuleWf.Status = WorkflowStatusCompleted
	err = quotaRuleWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Errorf("Failed to update job status to Done for UpdateQuotaRuleWorkflow: %v", err)
	}
	logger.Debug("Update Quota Rule workflow completed successfully")
	return nil, nil
}

// Setup initializes the workflow with the necessary parameters and sets up a query handler for status updates.
func (wf *quotaRuleUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updateQuotaRuleParams := input.(*common.UpdateQuotaRulesParam)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updateQuotaRuleParams.ProjectId
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

// Run executes the quota rule update workflow.
func (wf *quotaRuleUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	quotaRule := args[0].(*datamodel.QuotaRule)
	params := args[1].(*common.UpdateQuotaRulesParam)
	logger := util.GetLogger(ctx)
	quotaRuleActivity := &activities.QuotaRuleUpdateActivity{}
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

	// Defer function to mark the database entry in error state if any error occurs
	defer func() {
		if err != nil {
			// On error, mark quota rule in error state
			quotaRule.State = datamodel.LifeCycleStateError
			quotaRule.StateDetails = datamodel.LifeCycleStateUpdateErrorDetails
			err2 := workflow.ExecuteActivity(ctx, commonActivity.UpdateQuotaRuleState, *quotaRule, false).Get(ctx, nil)
			if err2 != nil {
				logger.Errorf("Failed to update quota rule state in DB to error: %v", err2)
			}
		}
	}()
	dbQuotaRule := quotaRule
	originalDiskLimitInKib := dbQuotaRule.DiskLimitInKib
	newDiskLimitInKib := quotaRule.DiskLimitInKib
	newDescription := quotaRule.Description

	if params.DiskLimitInMib > 0 {
		newDiskLimitInKib = params.DiskLimitInMib * 1024
	}

	if params.Description != "" {
		newDescription = params.Description
	}

	// Fetch volume details as the first activity and perform DP check
	var volumeDetails *datamodel.Volume
	err = workflow.ExecuteActivity(ctx, commonActivity.GetVolumeByID, dbQuotaRule.VolumeID, dbQuotaRule.AccountID).Get(ctx, &volumeDetails)
	if err != nil {
		logger.Errorf("Failed to fetch volume details for DP validation: %v", err)
		return nil, ConvertToVSAError(err)
	}

	isDataProtection := false
	if volumeDetails != nil && volumeDetails.VolumeAttributes != nil {
		isDataProtection = volumeDetails.VolumeAttributes.IsDataProtection
	}

	// If volume is data protection, only update the database (no ONTAP or destination sync needed)
	if isDataProtection {
		dbQuotaRule.DiskLimitInKib = newDiskLimitInKib
		dbQuotaRule.Description = newDescription

		// Update quota rule state in database only (no ONTAP update needed)
		err = workflow.ExecuteActivity(ctx, commonActivity.UpdateQuotaRuleState, *dbQuotaRule, false).Get(ctx, nil)
		if err != nil {
			logger.Errorf("Failed to update quota rule state in database: %v", err)
			return nil, ConvertToVSAError(err)
		}
		// DB-only update (DP volume): return after successful update.
		logger.Info("DB update of quotaRule successful for DP volume")
		return nil, nil
	}

	var node *models.Node
	var quotaUUID string

	if params.DiskLimitInMib > 0 {
		var dbNodes []*datamodel.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, volumeDetails.PoolID).Get(ctx, &dbNodes)
		if err != nil {
			logger.Errorf("Failed to get nodes for pool: %v", err)
			return nil, ConvertToVSAError(err)
		}

		// Create node structure for provider - this will be passed to activities
		node = vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   volumeDetails.Pool.DeploymentName,
			OntapCredentials: volumeDetails.Pool.PoolCredentials,
		})

		// Get ONTAP quota UUID for the quota rule
		err = workflow.ExecuteActivity(ctx, commonActivity.GetOntapQuotaUUID,
			volumeDetails, node, dbQuotaRule.QuotaType, dbQuotaRule.QuotaTarget, JobActionUpdate).Get(ctx, &quotaUUID)
		if err != nil {
			logger.Errorf("Failed to get quota UUID from ONTAP: %v", err)
			return nil, ConvertToVSAError(err)
		}

		// Update quota rule on ONTAP
		err = workflow.ExecuteActivity(ctx, quotaRuleActivity.UpdateQuotaRulesOnOntap,
			quotaUUID, node, newDiskLimitInKib).Get(ctx, nil)
		if err != nil {
			logger.Errorf("Failed to update quota rule on ONTAP: %v", err)
			return nil, ConvertToVSAError(err)
		}
	}

	var replications []*datamodel.VolumeReplication
	err = workflow.ExecuteActivity(ctx, commonActivity.GetVolumeReplication, volumeDetails.ID).Get(ctx, &replications)
	if err != nil {
		logger.Errorf("Failed to fetch volume replication details for destination sync: %v", err)
		return nil, ConvertToVSAError(err)
	}

	// Check if there are any replications
	if len(replications) == 0 {
		logger.Infof("No replications found for volume, skipping destination quotaRule sync")
	} else {
		// At present we can have only 1 replication for a region, so use the first replication
		replication := replications[0]
		if replication == nil || replication.ReplicationAttributes == nil {
			return nil, ConvertToVSAError(errors.New("replication is nil or has no attributes"))
		} else {
			// Verify if this replication is eligible for destination sync
			// This activity validates that replication is eligible for quota sync (MIRRORED or UNINITIALIZED state)
			var isEligible bool
			err = workflow.ExecuteActivity(ctx, commonActivity.VerifyReplicationState,
				replication, params.LocationId).Get(ctx, &isEligible)
			if err != nil {
				logger.Errorf("Replication state validation failed for destination sync: %v", err)
				return nil, ConvertToVSAError(err)
			}

			// Sync quota rule only if replication is eligible
			if isEligible {
				destProjectNumber, parseErr := utils.ParseProjectNumberFromURI(replication.RemoteUri)
				if parseErr != nil {
					err = parseErr
					logger.Errorf("Failed to parse destination project number from RemoteUri: %v, remoteUri: %s", err, replication.RemoteUri)
					return nil, ConvertToVSAError(err)
				}

				// Create JWT token once for reuse across all destination API calls
				var jwtToken *string
				err = workflow.ExecuteActivity(ctx, commonActivity.GetSignedDstTokenForQuotaRule, destProjectNumber).Get(ctx, &jwtToken)
				if err != nil {
					logger.Errorf("Failed to get JWT token for destination project %s: %v", destProjectNumber, err)
					return nil, ConvertToVSAError(err)
				}

				// Fetch and match quota rule on destination volume
				var destinationQuotaRuleId *string
				err = workflow.ExecuteActivity(ctx, commonActivity.GetMatchingQuotaRuleOnDestination,
					replication.ReplicationAttributes.DestinationVolumeUUID, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, dbQuotaRule.Name, jwtToken).Get(ctx, &destinationQuotaRuleId)
				if err != nil {
					logger.Errorf("Failed to fetch matching quota rule from destination: destinationVolumeUUID=%s, quotaRuleName=%s, error=%v",
						replication.ReplicationAttributes.DestinationVolumeUUID, dbQuotaRule.Name, err)

					if params.DiskLimitInMib > 0 {
						// Revert source quota rule if it was updated
						logger.Errorf("Reverting source quota rule due to destination sync failure")
						revertErr := workflow.ExecuteActivity(ctx, quotaRuleActivity.RevertQuotaRulesOnSource,
							quotaUUID, node, originalDiskLimitInKib).Get(ctx, nil)
						if revertErr != nil {
							logger.Errorf("Failed to revert quota rule on source: %v", revertErr)
							return nil, ConvertToVSAError(revertErr)
						}
					}
					// Return original error so defer marks quota rule in error state
					return nil, ConvertToVSAError(err)
				}

				// Update quota rule on destination via internal API — only send fields the user explicitly requested
				diskLimitForDestination := int64(0)
				if params.DiskLimitInMib > 0 {
					diskLimitForDestination = newDiskLimitInKib
				}
				descriptionForDestination := ""
				if params.Description != "" {
					descriptionForDestination = newDescription
				}
				var updateOperationResult *activities.QuotaRuleOperationResult
				err = workflow.ExecuteActivity(ctx, quotaRuleActivity.UpdateQuotaRuleOnDestination,
					replication.ReplicationAttributes.DestinationVolumeUUID, *destinationQuotaRuleId, diskLimitForDestination, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, jwtToken, descriptionForDestination).Get(ctx, &updateOperationResult)
				if err != nil {
					logger.Errorf("Failed to update quota rule on destination: destinationVolumeUUID=%s, quotaRuleId=%s, error=%v",
						replication.ReplicationAttributes.DestinationVolumeUUID, *destinationQuotaRuleId, err)

					if params.DiskLimitInMib > 0 {
						// Revert source quota rule if it was updated
						logger.Warnf("Reverting source quota rule due to destination update failure")
						revertErr := workflow.ExecuteActivity(ctx, quotaRuleActivity.RevertQuotaRulesOnSource,
							quotaUUID, node, originalDiskLimitInKib).Get(ctx, nil)
						if revertErr != nil {
							logger.Errorf("Failed to revert quota rule on source: %v", revertErr)
							return nil, ConvertToVSAError(revertErr)
						}
					}
					// Return original error so defer marks quota rule in error state
					return nil, ConvertToVSAError(err)
				}

				// Poll for operation completion if async operation was started
				if updateOperationResult != nil && updateOperationResult.OperationName != "" && !updateOperationResult.IsDone {
					logger.Infof("Polling for quota rule update operation completion on destination: operationName=%s", updateOperationResult.OperationName)
					err = workflow.ExecuteActivity(ctx, commonActivity.DescribeQuotaRuleRemoteJob,
						updateOperationResult.OperationName, replication.ReplicationAttributes.DestinationLocation, destProjectNumber, jwtToken).Get(ctx, nil)
					if err != nil {
						logger.Errorf("Failed to wait for quota rule update on destination: operationName=%s, error=%v",
							updateOperationResult.OperationName, err)

						if params.DiskLimitInMib > 0 {
							// Revert source quota rule if destination update failed
							logger.Warnf("Reverting source quota rule due to destination operation failure")
							revertErr := workflow.ExecuteActivity(ctx, quotaRuleActivity.RevertQuotaRulesOnSource,
								quotaUUID, node, originalDiskLimitInKib).Get(ctx, nil)
							if revertErr != nil {
								logger.Errorf("Failed to revert quota rule on source: %v", revertErr)
								return nil, ConvertToVSAError(revertErr)
							}
						}
						// Return original error so defer marks quota rule in error state
						return nil, ConvertToVSAError(err)
					}
				}

				logger.Infof("Successfully synced quota rule to destination: location=%s, volumeUUID=%s, quotaRuleId=%s",
					replication.ReplicationAttributes.DestinationLocation, replication.ReplicationAttributes.DestinationVolumeUUID, *destinationQuotaRuleId)
			} else {
				logger.Infof("Replication is not eligible for sync: destinationLocation=%s, destinationVolumeUUID=%s",
					replication.ReplicationAttributes.DestinationLocation, replication.ReplicationAttributes.DestinationVolumeUUID)
			}
		}
	}
	dbQuotaRule.DiskLimitInKib = newDiskLimitInKib
	dbQuotaRule.Description = newDescription

	err = workflow.ExecuteActivity(ctx, commonActivity.UpdateQuotaRuleState, *dbQuotaRule, false).Get(ctx, nil)
	if err != nil {
		logger.Errorf("Failed to update quota rule state: %v", err)
		return nil, ConvertToVSAError(err)
	}

	logger.Infof("Quota rule update workflow completed successfully")
	return nil, nil
}

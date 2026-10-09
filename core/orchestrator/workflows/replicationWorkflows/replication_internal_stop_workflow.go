package replicationWorkflows

import (
	"errors"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	quotaRuleSyncEnabled = env.GetBool("QUOTA_RULE_SYNC", true)
)

type internalVolumeReplicationStopWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &internalVolumeReplicationStopWorkflow{}

func StopInternalVolumeReplicationWorkflow(ctx workflow.Context, replicationDb *datamodel.VolumeReplication, forceStop bool) (*vsa.VolumeReplication, error) {
	logger := workflow.GetLogger(ctx)
	stopRepWf := new(internalVolumeReplicationStopWorkflow)
	err := stopRepWf.Setup(ctx, replicationDb)
	if err != nil {
		return nil, err
	}
	stopRepWf.Status = workflows.WorkflowStatusRunning
	err = stopRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		stopRepWf.Status = workflows.WorkflowStatusFailed
		err = stopRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := stopRepWf.Run(ctx, replicationDb, forceStop)
	if customErr != nil {
		logger.Info("Internal Stop Volume Replication workflow run executed with error", "error", customErr)

		// Check if this is a quota rule failure (partial success case) and quotaRuleSync is enabled
		if quotaRuleSyncEnabled && isQuotaRuleFailure(customErr) {
			logger.Warn("Break replication succeeded but quota rule creation failed, marking workflow as completed")
			stopRepWf.Status = workflows.WorkflowStatusCompleted
			// Use vsaerrors.NewVCPError so it's recognized as CustomError in UpdateJobStatus
			quotaRuleErr := vsaerrors.NewVCPError(
				vsaerrors.ErrBreakReplicationQuotaRuleFailure,
				errors.New(datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure),
			)
			err = stopRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), quotaRuleErr)
			return nil, err
		}

		// For all other errors, mark workflow as failed
		stopRepWf.Status = workflows.WorkflowStatusFailed
		err = stopRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	stopRepWf.Status = workflows.WorkflowStatusCompleted
	err = stopRepWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

// isQuotaRuleFailure checks if the error is a quota rule failure error
func isQuotaRuleFailure(err *vsaerrors.CustomError) bool {
	if err == nil {
		return false
	}
	return err.TrackingID == vsaerrors.ErrBreakReplicationQuotaRuleFailure
}

func (wf *internalVolumeReplicationStopWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	replicationParams := input.(*datamodel.VolumeReplication)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = replicationParams.Account.Name
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *internalVolumeReplicationStopWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	dbReplication := args[0].(*datamodel.VolumeReplication)
	forceStop := args[1].(bool)
	replicationActivity := &replicationActivities.InternalStopVolumeReplicationActivity{}
	replicationCommonActivity := &replicationActivities.VolumeReplicationCreateActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableError", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	log := workflow.GetLogger(ctx)

	// Defer function to mark the database entry in error state if any error occurs
	defer func() {
		if err != nil {
			// On panic, mark volume replication in error state
			dbReplication.State = datamodel.LifeCycleStateError
			dbReplication.StateDetails = err.Error()
			err2 := workflow.ExecuteActivity(ctx, replicationCommonActivity.UpdateReplicationState, *dbReplication).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update volume state in DB to error", "error", err2)
			}
		}
	}()

	var vsaReplication *vsa.VolumeReplication
	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, dbReplication.Volume.PoolID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   dbReplication.Volume.Pool.DeploymentName,
		OntapCredentials: dbReplication.Volume.Pool.PoolCredentials,
	})

	err = workflow.ExecuteActivity(ctx, replicationActivity.AbortVolumeReplication, dbReplication, node, forceStop).Get(ctx, &vsaReplication)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.BreakVolumeReplication, dbReplication, node, forceStop).Get(ctx, &vsaReplication)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSnapMirrorFromOntap, dbReplication, node).Get(ctx, &vsaReplication)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeReplicationStopDetails, dbReplication, vsaReplication).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeToNonDPVolume, dbReplication).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Create quota rules on destination volume after successful break replication
	// Only process quota rules if quotaRuleSync is enabled
	var quotaRuleFailure bool = false
	var failedQuotaRules []*datamodel.QuotaRule

	if quotaRuleSyncEnabled {
		quotaRuleActivity := &activities.QuotaRuleCommonActivity{}

		// List quota rules for volume (from replication)
		var quotaRules []*datamodel.QuotaRule
		err = workflow.ExecuteActivity(ctx, quotaRuleActivity.ListQuotaRuleForVolume, dbReplication).Get(ctx, &quotaRules)
		if err != nil {
			// Log error and set failure flag, but continue with workflow
			log.Error("Failed to list quota rules for source volume", "error", err)
			quotaRuleFailure = true
		} else {
			if len(quotaRules) > 0 {
				// Quota rules found, process them using helper function
				log.Info("Found quota rules, creating them on destination volume", "count", len(quotaRules))
				failedQuotaRules = processQuotaRulesPostBreakReplication(ctx, dbReplication, quotaRules, node)
				if len(failedQuotaRules) > 0 {
					// Store failed quota rules for future use
					log.Warn("Quota rule creation completed with failed rules", "failedCount", len(failedQuotaRules))
					for _, failedRule := range failedQuotaRules {
						log.Warn("Failed quota rule", "uuid", failedRule.UUID, "name", failedRule.Name)
					}
					quotaRuleFailure = true
				} else {
					log.Info("Successfully created all quota rules on destination volume")
				}
			}
		}
	}

	// Handle quota rule failures - update states but don't fail the workflow
	if quotaRuleFailure {
		log.Warn("Break replication succeeded but quota rule operations failed")
		if len(failedQuotaRules) > 0 {
			// Update failed quota rules state to ERROR
			err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateQuotaRulesStateToError, failedQuotaRules).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to update quota rules state to ERROR", "error", err)
				return nil, workflows.ConvertToVSAError(
					vsaerrors.NewVCPError(
						vsaerrors.ErrDatabaseDataUpdateError,
						errors.New("update quota rule state transaction failed as part of break operation"),
					),
				)
			}
		}

		// Fetch updated replication from DB (post UpdateVolumeReplicationStopDetails) before setting error state
		var updatedReplication *datamodel.VolumeReplication
		err = workflow.ExecuteActivity(ctx, replicationActivity.GetReplicationFromDB, dbReplication.UUID).Get(ctx, &updatedReplication)
		if err != nil {
			log.Error("Failed to get updated replication from DB for quota error", "error", err)
			return nil, workflows.ConvertToVSAError(err)
		}

		// Update volume replication state to ERROR (even if listing failed or quota rule creation failed)
		err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeReplicationForQuotaError, updatedReplication).Get(ctx, nil)
		if err != nil {
			log.Error("Failed to update volume replication state for quota error", "error", err)
			return nil, workflows.ConvertToVSAError(err)
		}

		// Return quota-specific error to be handled specially by error handler
		// This allows the workflow to be marked as completed (partial success) instead of failed
		log.Warn("Break replication succeeded but quota rule operations failed")
		return nil, workflows.ConvertToVSAError(
			vsaerrors.NewVCPError(
				vsaerrors.ErrBreakReplicationQuotaRuleFailure,
				vsaerrors.New(datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure),
			),
		)
	}

	return nil, nil
}

// processQuotaRulesPostBreakReplication is a helper function that orchestrates the creation
// of quota rules on the destination volume after break replication. It executes a series
// of activities to handle the complete flow.
// It accepts a replication object and uses the volume from replication directly to avoid additional DB calls.
// Returns a list of failed quota rules for future processing.
func processQuotaRulesPostBreakReplication(
	ctx workflow.Context,
	replication *datamodel.VolumeReplication,
	quotaRules []*datamodel.QuotaRule,
	node *models.Node,
) []*datamodel.QuotaRule {
	log := workflow.GetLogger(ctx)

	destinationVolumeUUID := replication.ReplicationAttributes.DestinationVolumeUUID

	// Collect failed quota rules for return
	var failedQuotaRules []*datamodel.QuotaRule

	// Track last successful quota rule response for reinitialization check
	var lastQuotaRuleResponse *vsa.QuotaRuleProviderResponse

	// Initialize activity instances
	quotaRuleActivity := &activities.QuotaRuleCommonActivity{}
	quotaRuleCreateActivity := &activities.QuotaRuleCreateActivity{}
	var err error

	// Fetch authoritative volume details from database to get SVM external UUID
	// This ensures we have the complete volume object with all associations populated
	var volumeDetails *datamodel.Volume
	err = workflow.ExecuteActivity(ctx, quotaRuleActivity.GetVolumeByID,
		replication.Volume.ID, replication.Volume.AccountID).Get(ctx, &volumeDetails)
	if err != nil {
		log.Error("Failed to fetch volume details for quota rule processing", "error", err)
		// Return all quota rules as failed since we can't proceed without volume details
		return quotaRules
	}

	// Extract SVM external UUID from the fetched volume details
	// This is required for RQuota operations on ONTAP
	if volumeDetails.Svm == nil || volumeDetails.Svm.SvmDetails == nil {
		log.Error("SVM or SvmDetails is nil in volume details")
		// Return all quota rules as failed since we can't proceed without SVM details
		return quotaRules
	}
	svmExternalUUID := volumeDetails.Svm.SvmDetails.ExternalUUID

	// Volume must have VolumeAttributes with ExternalUUID for quota operations
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		log.Error("Volume has no ExternalUUID in VolumeAttributes", "volumeUUID", volumeDetails.UUID)
		return quotaRules
	}

	// Process each quota rule
	for i, quotaRule := range quotaRules {
		// Determine if this is the last rule (quota enable required on last rule)
		isLastRule := (i == len(quotaRules)-1)

		log.Info("Processing quota rule",
			"index", i+1, "total", len(quotaRules), "uuid", quotaRule.UUID, "quotaType", quotaRule.QuotaType, "target", quotaRule.QuotaTarget, "isLastRule", isLastRule)

		if isLastRule {
			log.Info("Enabling RQuota on SVM for quota rule", "uuid", quotaRule.UUID)
			err = workflow.ExecuteActivity(ctx, quotaRuleActivity.UpdateRQuotaOnSvm,
				svmExternalUUID, node, true).Get(ctx, nil)
			if err != nil {
				log.Error("Failed to enable RQuota on SVM", "error", err)
				failedQuotaRules = append(failedQuotaRules, quotaRule)
				continue
			}
		}

		var quotaRuleResponse *vsa.QuotaRuleProviderResponse
		var updateErr error

		// Try to update default quota if it's a default quota
		if quotaRule.QuotaTarget == "" {
			log.Info("Attempting to update existing default quota for rule", "uuid", quotaRule.UUID)
			updateErr = workflow.ExecuteActivity(ctx, quotaRuleCreateActivity.HandleDefaultQuotaRuleUpdate,
				volumeDetails, node, quotaRule.QuotaType, quotaRule.DiskLimitInKib).Get(ctx, nil)

			// If update failed with non-NotFoundErr, record failed rule and skip
			if updateErr != nil {
				isNotFound := customerrors.IsNotFoundErr(updateErr) ||
					strings.Contains(strings.ToLower(updateErr.Error()), "not found")
				if !isNotFound {
					log.Error("Failed to update default quota rule", "error", updateErr)
					failedQuotaRules = append(failedQuotaRules, quotaRule)
					continue
				}
			}
		}

		// Create quota if it's an individual quota OR if default quota was not found
		isNotFound := updateErr != nil && (customerrors.IsNotFoundErr(updateErr) ||
			strings.Contains(strings.ToLower(updateErr.Error()), "not found"))
		if quotaRule.QuotaTarget != "" || isNotFound {
			log.Info("Creating quota rule", "uuid", quotaRule.UUID, "quotaType", quotaRule.QuotaType, "target", quotaRule.QuotaTarget)
			err = workflow.ExecuteActivity(ctx, quotaRuleCreateActivity.CreateQuotaRuleOnONTAP,
				node, volumeDetails, quotaRule).Get(ctx, &quotaRuleResponse)
			if err != nil {
				log.Error("Failed to create quota rule", "error", err)
				failedQuotaRules = append(failedQuotaRules, quotaRule)
				continue
			}

			// Track the last successful response for potential reinitialization
			if quotaRuleResponse != nil {
				lastQuotaRuleResponse = quotaRuleResponse
			}

			// Step 3: Enable quota subsystem on last rule (only after quota creation)
			if isLastRule {
				log.Info("Last quota rule processed, enabling quota subsystem", "destinationVolumeUUID", destinationVolumeUUID)
				err = workflow.ExecuteActivity(ctx, quotaRuleActivity.HandleQuotaEnablementAndReinitialization,
					node, volumeDetails, lastQuotaRuleResponse).Get(ctx, nil)
				if err != nil {
					log.Error("Failed to enable quota subsystem", "error", err)
					failedQuotaRules = append(failedQuotaRules, quotaRule)
				}
			}
		}
	}

	return failedQuotaRules
}

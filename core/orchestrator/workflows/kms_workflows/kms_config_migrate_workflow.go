package kms_workflows

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errorcore "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type migrateKmsConfigWorkflow struct {
	workflows.BaseWorkflow
}

// Enforcing the WorkflowInterface on kmsMigrateWorkflow
var _ workflows.WorkflowInterface = &migrateKmsConfigWorkflow{}

const MigrationInfoPrefix = " CMEK Migration - Pools that were not migrated: "

// MigrateKmsConfigWorkflow processes KMS config migrate related requests from a customer
func MigrateKmsConfigWorkflow(ctx workflow.Context, params *common.MigrateKmsConfigParams) (interface{}, error) {
	kmsConfigWorkflow := new(migrateKmsConfigWorkflow)
	err := kmsConfigWorkflow.Setup(ctx, params)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	kmsConfigWorkflow.Status = workflows.WorkflowStatusRunning
	err = kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	vsaCmekMigrationSkippedPoolReason, errWorkflow := kmsConfigWorkflow.Run(ctx, params)
	if errWorkflow != nil {
		kmsConfigWorkflow.Status = workflows.WorkflowStatusFailed
		if vsaCmekMigrationSkippedPoolReason != MigrationInfoPrefix {
			kmsConfigWorkflow.Logger.Info(fmt.Sprintf("%s", vsaCmekMigrationSkippedPoolReason))
			errWorkflow = workflows.ConvertToVSAError(fmt.Errorf("%w \n%s", errWorkflow, vsaCmekMigrationSkippedPoolReason))
		}
		err = kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errWorkflow)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		return nil, workflows.ConvertToVSAError(errWorkflow)
	}

	kmsConfigWorkflow.Status = workflows.WorkflowStatusCompleted
	if vsaCmekMigrationSkippedPoolReason != MigrationInfoPrefix {
		err = kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), temporal.NewApplicationError(fmt.Sprintf("%s", vsaCmekMigrationSkippedPoolReason), "CustomErrorType"))
	} else {
		err = kmsConfigWorkflow.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	}
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}

func (kmsWorkflow *migrateKmsConfigWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	migrateParams := input.(*common.MigrateKmsConfigParams)
	info := workflow.GetInfo(ctx)
	kmsWorkflow.ID = info.WorkflowExecution.ID
	kmsWorkflow.CustomerID = migrateParams.AccountName
	kmsWorkflow.Status = workflows.WorkflowStatusCreated

	logger := util.GetLogger(ctx)
	kmsWorkflow.Logger = logger.With(log.Fields{
		"workflowID": kmsWorkflow.ID,
		"customerID": kmsWorkflow.CustomerID,
	})

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         kmsWorkflow.ID,
			Status:     kmsWorkflow.Status,
			CustomerID: kmsWorkflow.CustomerID,
		}, nil
	})
}

func (kmsWorkflow *migrateKmsConfigWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.MigrateKmsConfigParams)
	kmsConfigUUID := params.UUID
	sdeKmsConfigUUID := params.SdeUUID
	vsaCmekMigrationSkippedPoolReason := MigrationInfoPrefix

	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
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

	defer func() {
		// KmsConfig State is not dependent on an outcome of migration;
		// KmsConfig state is determined by Verify operation (same as SDE workflow)
		disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
		_ = workflow.ExecuteActivity(disconnectedCtx, kmsConfigActivity.VerifyVsaKmsReachabilityActivity, kmsConfigUUID, false).Get(disconnectedCtx, nil)
		if vsaCmekMigrationSkippedPoolReason != MigrationInfoPrefix {
			kmsWorkflow.Logger.Info(vsaCmekMigrationSkippedPoolReason)
		}
	}()

	paramsForSyncingAndEKMCreation := common.CreatePoolParams{
		AccountName: params.AccountName,
		Region:      params.LocationID,
		KmsConfigId: kmsConfigUUID,
	}

	shouldUseSDEMigration := utils.IsCVPHostConfigured()
	var existingKmsConfig datamodel.KmsConfig
	getErr := workflow.ExecuteActivity(ctx, kmsConfigActivity.GetKmsConfigActivity, kmsConfigUUID).Get(ctx, &existingKmsConfig)
	if getErr == nil {
		shouldUseSDEMigration = !utils.ShouldUseVCPForExistingKMS(&existingKmsConfig)
	} else {
		var appErr *temporal.ApplicationError
		if !(errorcore.As(getErr, &appErr) && appErr.NonRetryable() && appErr.Type() == kms_activities.ErrTypeKmsConfigNotFound) {
			return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(getErr)
		}
	}

	// SDE-created config path: migrate SDE resources, poll, describe, and sync VCP DB with SDE DB.
	if shouldUseSDEMigration {
		jwtToken := ""
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GetSignedTokenActivity, params.ProjectNumber).Get(ctx, &jwtToken)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)

		// Migrate KMS configuration in CVP
		var response *kms_configurations.V1betaEncryptVolumesAccepted
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.MigrateSdeKmsConfigActivity, params).Get(ctx, &response)
		if err != nil {
			kmsWorkflow.Logger.Error("Error encountered when KMS Migrate API was sent to SDE", log.Fields{
				"error":  err,
				"params": params,
			})
			return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
		}

		// Policy for polling the KMS migrate operation
		pollingOptions := workflow.ActivityOptions{
			StartToCloseTimeout: time.Duration(cvpMaxPollTimeout) * time.Minute,
			HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
			RetryPolicy: &temporal.RetryPolicy{
				BackoffCoefficient:     retryPolicy.BackoffCoefficient,
				InitialInterval:        time.Duration(cvpPollInterval) * time.Second,
				NonRetryableErrorTypes: []string{"PanicError"},
			},
		}
		pollingCtx := workflow.WithActivityOptions(ctx, pollingOptions)

		// Poll the KMS migration operation until completion/timeout
		err = workflow.ExecuteActivity(pollingCtx, kmsConfigActivity.PollMigrateSdeKmsConfigActivity, params, response).Get(ctx, nil)
		if err != nil {
			if strings.Contains(err.Error(), "operation failed:") {
				kmsWorkflow.Logger.Error("Error encountered during SDE migration; Setting CMEK policy in VCP to error state", log.Fields{
					"error":  err,
					"params": params,
				})
			}
			return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
		}

		// Describe SDE KMS config and sync VCP DB if needed
		var sdeKmsConfig cvpmodels.KmsConfigV1beta
		getKmsConfigParams := &common.GetKmsConfigParams{
			UUID:          sdeKmsConfigUUID,
			LocationID:    params.LocationID,
			ProjectNumber: params.AccountName,
		}
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.DescribeSDEKmsConfigurationActivity, getKmsConfigParams).Get(ctx, &sdeKmsConfig)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				kmsWorkflow.Logger.Info("KMS configuration not found in SDE DB after CMEK migration")
			}
			return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
		}

		if sdeKmsConfig.Description != nil {
			paramsForSyncingAndEKMCreation.Description = *sdeKmsConfig.Description
		}
		vsaKmsConfig := datamodel.KmsConfig{}
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GetKmsConfigActivity, kmsConfigUUID).Get(ctx, &vsaKmsConfig)
		if err != nil {
			var appErr *temporal.ApplicationError
			if errorcore.As(err, &appErr) && appErr.NonRetryable() && appErr.Type() == kms_activities.ErrTypeKmsConfigNotFound {
				kmsWorkflow.Logger.Info("KMS configuration not found in VCP DB - Syncing SDE and VCP DBs...")
				sdeKmsConfig.KmsState = datamodel.LifeCycleStateMigrating
				sdeKmsConfig.KmsStateDetails = datamodel.LifeCycleStateMigratingDetails
				createKmsConfigParams := kms_activities.ConvertToCreateKmsConfigParams(&sdeKmsConfig, &paramsForSyncingAndEKMCreation)
				errSync := syncBetweenSdeAndVsaDBs(ctx, createKmsConfigParams)
				if errSync != nil {
					kmsWorkflow.Logger.Error("VSA KMS configuration syncing with SDE DB has failed...", log.Fields{"error": errSync})
					return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(errSync)
				}
			} else {
				return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
			}
		}
	}

	// VCP path: KMS config already in VCP DB, no SDE migration/sync needed.
	// Proceed directly to pool/volume migration.

	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	volumeActivity := &activities.VolumeCreateActivity{}
	var poolsForAccount []*datamodel.Pool
	err = workflow.ExecuteActivity(ctx, poolActivity.GetPoolsByAccountName, params.AccountName).Get(ctx, &poolsForAccount)
	if err != nil {
		return vsaCmekMigrationSkippedPoolReason, workflows.ConvertToVSAError(err)
	}
	if len(poolsForAccount) < 1 {
		kmsWorkflow.Logger.Info("For the following pools migration was skipped:\n" + vsaCmekMigrationSkippedPoolReason)
		return vsaCmekMigrationSkippedPoolReason, nil
	}

	//     -----   Migrate VSA resources     -----

	// Pre-check section for VSA resources
	var poolsForMigration []*datamodel.Pool
	for _, pool := range poolsForAccount {
		switch pool.State {
		case datamodel.LifeCycleStateError:
			kmsWorkflow.Logger.Info(fmt.Sprintf("Pool with ID %s is in error state...skipping migration for this pool", pool.UUID))
			vsaCmekMigrationSkippedPoolReason += fmt.Sprintf("\n Pool with ID: %s is in error state...skipping migration for this pool", pool.UUID)
			continue
		case datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateDeleting:
			kmsWorkflow.Logger.Info(fmt.Sprintf("Pool with ID %s is in transitioning state %s ...skipping migration for this pool", pool.UUID, pool.State))
			vsaCmekMigrationSkippedPoolReason += fmt.Sprintf("\n Pool with ID: %s is in transitioning state...skipping migration for this pool", pool.UUID)
			continue
		case datamodel.LifeCycleStateDegraded:
			kmsWorkflow.Logger.Info(fmt.Sprintf("Pool with ID %s is in degraded state...skipping migration for this pool", pool.UUID))
			vsaCmekMigrationSkippedPoolReason += fmt.Sprintf("\n Pool with ID: %s is in degraded state...skipping migration for this pool", pool.UUID)
			continue
		}

		if !pool.KmsConfigID.Valid {
			poolsForMigration = append(poolsForMigration, pool)
		} else {
			kmsWorkflow.Logger.Info(fmt.Sprintf("Pool with ID %s is already having CMEK policy assocated to it ...skipping migration for this pool", pool.UUID))
			vsaCmekMigrationSkippedPoolReason += fmt.Sprintf("\n Pool with ID %s is already having CMEK policy assocated...skipping migration for this pool", pool.UUID)
			continue
		}
	}

	if len(poolsForMigration) < 1 {
		kmsWorkflow.Logger.Info("Pools requiring migration not present in VSA")
		return vsaCmekMigrationSkippedPoolReason, nil
	}

	// Begin migration of VSA resources
	var poolMigrationClientError, poolMigrationInternalError bool
	var future workflow.Future
	futures := make([]workflow.Future, 0, len(poolsForMigration))
	for index, pool := range poolsForMigration {
		// Determine Node for pool
		var dbNodes []*datamodel.Node
		errGetNode := workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, pool.ID).Get(ctx, &dbNodes)
		if errGetNode != nil {
			poolMigrationInternalError = true
			kmsWorkflow.Logger.Error(fmt.Sprintf("Failed to get node belonging to pool-id %s selected for CMEK migration", strconv.Itoa(int(pool.ID))), log.Fields{"error": errGetNode})

			err = workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, pool, errGetNode.Error()).Get(ctx, nil)
			if err != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf("Unable to update state of Pool to error, for failed migration of pool-id %s", pool.UUID), log.Fields{"error": err})
			}
			futures = append(futures, future)
			continue
		}

		nodeForPool := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   pool.DeploymentName,
			OntapCredentials: pool.PoolCredentials,
		})
		if len(nodeForPool.EndpointAddressesToHostNameMap) == 0 {
			poolMigrationInternalError = true
			errMsgNode := fmt.Sprintf("Node belonging to pool-id %s selected for CMEK migration does not have Endpoint Address", strconv.Itoa(int(pool.ID)))
			kmsWorkflow.Logger.Error(errMsgNode)

			err = workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, pool, errMsgNode).Get(ctx, nil)
			if err != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf("Unable to update state of Pool to error, for failed migration of pool-id %s", pool.UUID), log.Fields{"error": err})
			}
			futures = append(futures, future)
			continue
		}

		// Determine Svm for pool
		svmForPool := datamodel.Svm{}
		errGetSvm := workflow.ExecuteActivity(ctx, svmActivity.GetSvmForPoolID, pool.ID).Get(ctx, &svmForPool)
		if errGetSvm != nil || svmForPool.ID == 0 {
			poolMigrationInternalError = true
			kmsWorkflow.Logger.Error(fmt.Sprintf("Failed to get SVM belonging to pool-id %s selected for CMEK migration", strconv.Itoa(int(pool.ID))), log.Fields{"error": errGetSvm})
			errGetSvmMsg := fmt.Sprintf("Failed to get SVM belonging to pool-id %s selected for CMEK migration", strconv.Itoa(int(pool.ID)))
			if errGetSvm != nil {
				errGetSvmMsg = errGetSvm.Error()
			}

			errFailedPool := workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, pool, errGetSvmMsg).Get(ctx, nil)
			if errFailedPool != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf("Unable to update state of Pool to error, for failed migration of pool-id %s", pool.UUID), log.Fields{"error": errFailedPool})
			}
			futures = append(futures, future)
			continue
		}

		err = workflow.ExecuteActivity(ctx, poolActivity.UpdatingPool, pool).Get(ctx, nil)
		if err != nil {
			kmsWorkflow.Logger.Error(fmt.Sprintf("Failed to update pool state of pool-id %s to updating...skipping migration for this pool", strconv.Itoa(int(pool.ID))), log.Fields{"error": err})
			futures = append(futures, future)
			continue
		}

		// Create EKM for Svm associated with Pool
		errCreateEKM := createEkmForSvm(ctx, nodeForPool, &svmForPool, poolsForMigration[index], paramsForSyncingAndEKMCreation)
		if errCreateEKM != nil {
			customErr := errorcore.ExtractCustomError(errCreateEKM)
			if customErr != nil && customErr.TrackingID == errorcore.ErrKMSAlreadyExistsEKM {
				poolMigrationClientError = true
			} else {
				poolMigrationInternalError = true
			}
			kmsWorkflow.Logger.Error(fmt.Sprintf(
				"Failed to associate EKM to pool-id %s selected for CMEK migration...skipping migration for this pool", pool.UUID),
				log.Fields{"error": errCreateEKM})
			errFailedPool := workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, pool, errCreateEKM.Error()).Get(ctx, nil)
			if errFailedPool != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf(
					"Unable to update state of Pool to error, for failed migration of pool-id %s", pool.UUID),
					log.Fields{"error": errFailedPool})
			}
			futures = append(futures, future)
			continue
		}

		var volumesForMigration []*datamodel.Volume

		errGetVolume := workflow.ExecuteActivity(ctx, volumeActivity.GetVolumesByPoolID, pool.ID).Get(ctx, &volumesForMigration)
		if errGetVolume != nil {
			poolMigrationInternalError = true
			kmsWorkflow.Logger.Error(fmt.Sprintf("Failed to retrieve volumes belonging to pool-id %s selected for CMEK migration...skipping migration for this pool", pool.UUID), log.Fields{"error": errGetVolume})

			err = workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, pool, errGetVolume.Error()).Get(ctx, nil)
			if err != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf("Unable to update state of Pool to error, for failed migration of pool-id %s", pool.UUID), log.Fields{"error": err})
			}
			futures = append(futures, future)
			continue
		}

		if len(volumesForMigration) < 1 {
			kmsWorkflow.Logger.Info(fmt.Sprintf("Pool with ID %s does not have volumes - Pool has been migrated over to use EKM for future volume creations", pool.UUID))
			err = workflow.ExecuteActivity(ctx, poolActivity.CreatedPool, poolsForMigration[index], nil).Get(ctx, nil)
			if err != nil {
				kmsWorkflow.Logger.Error(fmt.Sprintf(
					"Unable to update state of Pool to Ready after association of EKM, pool-id %s", poolsForMigration[index].UUID),
					log.Fields{"error": err})
			}
			futures = append(futures, future)
			continue
		}

		// Calculate overall used space of volumes within pool to determine StartToCloseTimeout for each pool migration activity
		startToCloseTimeout := utils.DetermineStartToCloseTimeoutBasedOnUsedSize(volumesForMigration)

		activityOptionsVSAMigration := workflow.ActivityOptions{
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
		ctxVSAMigration := workflow.WithActivityOptions(ctx, activityOptionsVSAMigration)

		// Run pool migrations in parallel
		future = workflow.ExecuteActivity(ctxVSAMigration, kmsConfigActivity.MigrateVsaPoolActivity, volumesForMigration, nodeForPool)
		futures = append(futures, future)
	}

	for index, f := range futures {
		if f != nil {
			errFuture := f.Get(ctx, nil)
			if errFuture != nil {
				poolMigrationInternalError = true
				err = workflow.ExecuteActivity(ctx, poolActivity.FailedPoolActivity, poolsForMigration[index], errFuture.Error()).Get(ctx, nil)
				if err != nil {
					kmsWorkflow.Logger.Error(fmt.Sprintf(
						"Unable to update state of Pool to error, for failed migration of pool-id %s", poolsForMigration[index].UUID),
						log.Fields{"error": err})
				}
			} else {
				err = workflow.ExecuteActivity(ctx, poolActivity.UpdatePoolState, poolsForMigration[index], datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil)
				if err != nil {
					kmsWorkflow.Logger.Error(fmt.Sprintf(
						"Unable to update state of Pool to In_Use, after succesful migration of pool-id %s", poolsForMigration[index].UUID),
						log.Fields{"error": err})
				}
			}
		}
	}

	if poolMigrationInternalError {
		// At least one pool had an internal error → return 500
		return vsaCmekMigrationSkippedPoolReason, errorcore.NewVCPError(errorcore.ErrKMSMigration, errors.New("Migration failed for at least one of the Pools"))
	} else if poolMigrationClientError {
		// All pool failures were client errors → return 400
		return vsaCmekMigrationSkippedPoolReason, errorcore.NewVCPError(errorcore.ErrKMSMigrationClientError, errors.New("Migration failed for at least one of the Pools"))
	}
	return vsaCmekMigrationSkippedPoolReason, nil
}

func validateKmsConfigForMigration(kmsState string) error {
	if kmsState == datamodel.LifeCycleStateUpdating || kmsState == datamodel.LifeCycleStateMigrating {
		return errors.NewBadRequestErr(fmt.Sprintf("CMEK Configuration continues to be in transitioning state after SDE migration: %s", kmsState))
	}
	if kmsState != datamodel.LifeCycleStateREADY && kmsState != datamodel.LifeCycleStateInUse {
		return errors.NewBadRequestErr("CMEK Configuration needs to be in either Ready or In_Use state for migration")
	}
	return nil
}

func syncBetweenSdeAndVsaDBs(ctx workflow.Context, createKmsConfigParams *common.CreateKmsConfigParams) error {
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	kmsConfig := &datamodel.KmsConfig{KmsAttributes: &datamodel.KmsAttributes{}}
	var err error

	defer func() {
		if err != nil {
			deleteKmsConfigParams := common.DeleteKmsConfigParams{
				KmsConfigID: createKmsConfigParams.UUID,
			}
			_ = workflow.ExecuteActivity(ctx, kmsConfigActivity.DeleteKmsConfig, kmsConfig, &deleteKmsConfigParams).Get(ctx, nil)
		}
	}()

	// Create and sync the KMS configuration with the SDE KMS configuration
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateAndSyncKmsConfigActivity, createKmsConfigParams).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	// Create the service account key for the KMS configuration
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateVSAKmsConfigSAKeyActivity, kmsConfig).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	// VCP-created configs: skip GrantRoleActivity — the VCP SA has direct access to the customer's KMS key.
	// SDE-created configs: grant the necessary roles to the service account for impersonation.
	if kmsConfig.KmsAttributes == nil || !kmsConfig.KmsAttributes.IsVCPCreated() {
		err = workflow.ExecuteActivity(ctx, kmsConfigActivity.GrantRoleActivity, kmsConfig).Get(ctx, nil)
		if err != nil {
			return err
		}
	}

	// Access a crypto key using the KMS config in the VSA database to make sure key is reachable
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity, kmsConfig).Get(ctx, kmsConfig)
	if err != nil {
		return err
	}

	return nil
}

func createEkmForSvm(ctx workflow.Context, node *models.Node, svm *datamodel.Svm, pool *datamodel.Pool, paramsForSyncingAndEKMCreation common.CreatePoolParams) error {
	kmsConfigActivity := &kms_activities.KmsConfigActivity{}
	rotateKmsSAKeyActivity := &backgroundactivities.RotateKmsSAKeyActivity{}
	logger := util.GetLogger(ctx)
	var err error
	var releaseLock func()

	defer func() {
		if err != nil {
			// Only attempt cleanup if we actually created an EKM (UUID was populated).
			if svm.SvmDetails != nil && svm.SvmDetails.ExternalKmsConfigUUID != "" {
				_ = workflow.ExecuteActivity(ctx, kmsConfigActivity.DeleteEkmConfigActivity, node, svm).Get(ctx, nil)
			}
		}
	}()

	// Creates DNS to reach google KMS from the VSA cluster
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CreateDnsActivity, node).Get(ctx, nil)
	if err != nil {
		if !strings.Contains(err.Error(), "duplicate entry") {
			return err
		}
	}

	// Acquire the KMS rotation lock at service-account scope (resolved from the KMS config) before getting KMS
	//  and updating SVM with key ID, so this cannot race with a rotation of the same  service account's key.
	if paramsForSyncingAndEKMCreation.KmsConfigId != "" {
		logger.Info("Acquiring KMS rotation lock for EKM config", "kmsConfigUUID", paramsForSyncingAndEKMCreation.KmsConfigId)
		_, releaseLock, err = workflows.WithKmsRotationLockForKmsConfig(ctx, rotateKmsSAKeyActivity, paramsForSyncingAndEKMCreation.KmsConfigId)
		if err != nil {
			return err
		}
		defer releaseLock()
	}

	// Configure KMS for SVM
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.ConfigureKmsForSvmActivity, svm, node, paramsForSyncingAndEKMCreation).Get(ctx, svm)
	if err != nil {
		return err
	}

	// Release lock so we don't hold it during reachability/UpdatePool; defer handles release only on early return above
	if releaseLock != nil {
		releaseLock()
	}

	// Check if the KMS config is reachable from the VSA cluster
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.CheckVsaKmsConfigReachableActivity, svm, node).Get(ctx, nil)
	if err != nil {
		return err
	}

	// Update the Pool with the KMS config IDs
	err = workflow.ExecuteActivity(ctx, kmsConfigActivity.UpdatePoolWithKmsConfigActivity, pool, paramsForSyncingAndEKMCreation.KmsConfigId).Get(ctx, nil)
	if err != nil {
		return err
	}

	return nil
}

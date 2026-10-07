package workflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type backupVaultCreateWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

type backupVaultUpdateWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

type backupVaultDeleteWorkflow struct {
	BaseWorkflow
	SE *database.Storage
}

var (
	_ WorkflowInterface = &backupVaultCreateWorkflow{}
	_ WorkflowInterface = &backupVaultUpdateWorkflow{}
	_ WorkflowInterface = &backupVaultDeleteWorkflow{}
)

func CreateBackupVaultWorkflow(ctx workflow.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault) error {
	logger := workflow.GetLogger(ctx)
	bvWF := new(backupVaultCreateWorkflow)
	err := bvWF.Setup(ctx, params)
	if err != nil {
		return ConvertToVSAError(err)
	}
	if err := bvWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return ConvertToVSAError(err)
	}
	bvWF.Status = WorkflowStatusRunning
	err = bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return ConvertToVSAError(err)
	}
	_, customErr := bvWF.Run(ctx, backupVault, params)

	if customErr != nil {
		bvWF.Status = WorkflowStatusFailed
		err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Error when updating the job status", "error", err2)
		}
		return customErr
	}
	bvWF.Status = WorkflowStatusCompleted
	err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Error when updating the job status", "error", err2)
	}
	return nil
}

// Setup initializes the create backup vault workflow.
func (wf *backupVaultCreateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	BackupVaultParams := input.(*common.BackupVaultParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = BackupVaultParams.AccountName
	wf.Status = WorkflowStatusCreated
	// Set the query handler in a non-blocking way
	err := workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (wf *backupVaultCreateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backupVault := args[0].(*datamodel.BackupVault)
	bvCommonParams := args[1].(*common.BackupVaultParams)
	backupVaultActivity := &activities.BackupVaultActivity{}

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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Hydrate the cross-region backup vault to its destination region.
	remoteBv := &datamodel.BackupVault{}
	err = workflow.ExecuteActivity(ctx, backupVaultActivity.CreateRemoteBackupVault, bvCommonParams.OwnerID, backupVault).Get(ctx, &remoteBv)
	if err != nil {
		logger.Error("Failed to create remote backup vault in VCP", "backupVaultUUID", backupVault.UUID, "error", err)
		return nil, ConvertToVSAError(fmt.Errorf("CreateRemoteBackupVault failed: %w", err))
	}

	if remoteBv != nil {
		backupVault.ExternalUUID = &remoteBv.UUID
		updates := map[string]interface{}{
			"external_uuid": remoteBv.UUID,
		}
		err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultFields, backupVault.UUID, updates).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to store cross-region vault UUID on source vault", "error", err)
			return nil, ConvertToVSAError(fmt.Errorf("UpdateBackupVaultFields failed: %w", err))
		}
	}

	return remoteBv, nil
}

func UpdateBackupVaultWorkflow(ctx workflow.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault) (gcpgenserver.V1betaUpdateBackupVaultRes, error) {
	logger := workflow.GetLogger(ctx)
	bvWF := new(backupVaultUpdateWorkflow)
	err := bvWF.Setup(ctx, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err := bvWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	bvWF.Status = WorkflowStatusRunning
	err = bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	_, customErr := bvWF.Run(ctx, backupVault, params)

	if customErr != nil {
		bvWF.Status = WorkflowStatusFailed
		err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Error when updating the job status", "error", err2)
		}
		return nil, customErr
	}
	bvWF.Status = WorkflowStatusCompleted
	err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Error when updating the job status", "error", err2)
	}
	return nil, nil
}

// Setup UpdateBackupVaultWorkflow process pool related requests from a customer.
func (wf *backupVaultUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	BackupVaultParams := input.(*common.BackupVaultParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = BackupVaultParams.AccountName
	wf.Status = WorkflowStatusCreated
	// Set the query handler in a non-blocking way
	err := workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (wf *backupVaultUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backupVault := args[0].(*datamodel.BackupVault)
	bvCommonParams := args[1].(*common.BackupVaultParams)
	useVCPRegion := !utils.IsCVPHostConfigured()
	backupVaultActivity := &activities.BackupVaultActivity{}

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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultStateInCaseOfError, backupVault, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil)
		}
	}()

	var sdeBackupVault *datamodel.BackupVault
	if useVCPRegion || backupVault.ServiceType == datamodel.ServiceTypeCrossProject {
		err = workflow.ExecuteActivity(ctx, backupVaultActivity.ApplyBackupVaultUpdateParams, backupVault, bvCommonParams).Get(ctx, &sdeBackupVault)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
	} else {
		var jwtToken string
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, bvCommonParams.AccountName).Get(ctx, &jwtToken)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)

		sdeBackupVault = &datamodel.BackupVault{}
		err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultInSDE, bvCommonParams).Get(ctx, &sdeBackupVault)
		if err != nil {
			logger.Error("Failed to update backup vault in SDE", "error", err, "params", backupVault)
			return nil, ConvertToVSAError(fmt.Errorf("UpdateBackupVaultInSDE failed: %w", err))
		}
	}

	// CVS omits a cleared description from its response, which becomes a nil pointer.
	// Keep the requested value, including "", so the VCP row is cleared instead of keeping the previous text.
	if sdeBackupVault != nil && bvCommonParams.Description != nil {
		sdeBackupVault.Description = bvCommonParams.Description
	}

	dbBackupVault := &datamodel.BackupVault{}
	err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultInVCP, sdeBackupVault, backupVault).Get(ctx, &dbBackupVault)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if dbBackupVault.BackupVaultType == activities.CrossRegionBackupType && *dbBackupVault.BackupRegionName != "" {
		remoteParams := *bvCommonParams
		remoteParams.BackupRegion = dbBackupVault.BackupRegionName

		dbRemoteBackupVault := &datamodel.BackupVault{}
		err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateRemoteBackupVaultInVCP, &remoteParams, dbBackupVault).Get(ctx, &dbRemoteBackupVault)
		if err != nil {
			logger.Error("Failed to update remote backup vault in VCP", "error", err, "params", dbBackupVault)
			return nil, ConvertToVSAError(fmt.Errorf("UpdateRemoteBackupVaultInVCP failed: %w", err))
		}
	}

	return dbBackupVault, nil
}

func DeleteBackupVaultWorkflow(ctx workflow.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault) (gcpgenserver.V1betaDeleteBackupVaultRes, error) {
	logger := workflow.GetLogger(ctx)
	bvWF := new(backupVaultDeleteWorkflow)
	err := bvWF.Setup(ctx, params)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	if err := bvWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, ConvertToVSAError(err)
	}
	bvWF.Status = WorkflowStatusRunning
	err = bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	_, customErr := bvWF.Run(ctx, backupVault, params)

	if customErr != nil {
		bvWF.Status = WorkflowStatusFailed
		err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err2 != nil {
			logger.Error("Error when updating the job status", "error", err2)
		}
		return nil, customErr
	}
	bvWF.Status = WorkflowStatusCompleted
	err2 := bvWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		logger.Error("Error when updating the job status", "error", err2)
	}
	return nil, nil
}

// Setup UpdateBackupVaultWorkflow process pool related requests from a customer.
func (wf *backupVaultDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	BackupVaultParams := input.(*common.BackupVaultParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = BackupVaultParams.AccountName
	wf.Status = WorkflowStatusCreated
	// Set the query handler in a non-blocking way
	err := workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (wf *backupVaultDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backupVault := args[0].(*datamodel.BackupVault)
	bvCommonParams := args[1].(*common.BackupVaultParams)
	useVCPRegion := !utils.IsCVPHostConfigured()
	backupVaultActivity := &activities.BackupVaultActivity{}

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
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateDeletedBackupVaultStateInCaseOfError, backupVault, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil)
		}
	}()

	// Step 1: Soft-delete backup vault in VCP database (state → DELETED, DeletedAt set)
	var dbBackupVault *datamodel.BackupVault
	err = workflow.ExecuteActivity(ctx, backupVaultActivity.DeleteBackupVaultInVCP, bvCommonParams.BackupVaultID).Get(ctx, &dbBackupVault)
	if err != nil {
		logger.Error("Failed to delete backup vault in VCP", "error", err, "params", backupVault)
		return nil, ConvertToVSAError(fmt.Errorf("DeleteBackupVaultInVCP failed: %w", err))
	}

	// Step 2: Delete backup vault in SDE (if not cross-project, or when CVP_HOST is not set, i.e. a VCP-only region)
	var sdeBackupVault *datamodel.BackupVault
	if !useVCPRegion && backupVault.ServiceType != datamodel.ServiceTypeCrossProject {
		var jwtToken string
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, bvCommonParams.AccountName).Get(ctx, &jwtToken)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)

		err = workflow.ExecuteActivity(ctx, backupVaultActivity.GetBackupVaultFromSDE, bvCommonParams).Get(ctx, &sdeBackupVault)
		if err != nil {
			logger.Error("Failed to describe backup vault in SDE", "backupVaultUUID", backupVault.UUID, "error", err)
			return nil, ConvertToVSAError(fmt.Errorf("GetBackupVaultFromSDE failed: %w", err))
		}

		// VCP may not have a local record for an SDE backup vault, so we can only tell
		// whether this request targets the destination side after fetching it from SDE.
		if sdeBackupVault != nil && sdeBackupVault.BackupVaultType == activities.CrossRegionBackupType &&
			sdeBackupVault.BackupRegionName != nil && *sdeBackupVault.BackupRegionName != "" &&
			*sdeBackupVault.BackupRegionName == bvCommonParams.Region {
			err = vsaerrors.NewVCPError(vsaerrors.ErrCrossRegionBackupVaultDeleteFromDestinationRegion,
				fmt.Errorf("Remote backup vaults cannot be deleted"))
			return nil, ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, backupVaultActivity.DeleteBackupVaultInSDE, bvCommonParams).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete backup vault in SDE", "backupVaultUUID", backupVault.UUID, "error", err)
			return nil, ConvertToVSAError(fmt.Errorf("DeleteBackupVaultInSDE failed: %w", err))
		}
	}

	// Step 3: Delete remote backup vault in VCP (cross-region only)
	isCrossRegionBackupVault := false
	var backupRegion *string
	if dbBackupVault != nil {
		isCrossRegionBackupVault = dbBackupVault.BackupVaultType == activities.CrossRegionBackupType && dbBackupVault.BackupRegionName != nil && *dbBackupVault.BackupRegionName != ""
		backupRegion = dbBackupVault.BackupRegionName
	} else if sdeBackupVault != nil {
		isCrossRegionBackupVault = sdeBackupVault.BackupVaultType == activities.CrossRegionBackupType && sdeBackupVault.BackupRegionName != nil && *sdeBackupVault.BackupRegionName != ""
		backupRegion = sdeBackupVault.BackupRegionName
	}

	if isCrossRegionBackupVault {
		remoteParams := *bvCommonParams
		remoteParams.BackupRegion = backupRegion

		dbRemoteBackupVault := &datamodel.BackupVault{}
		err = workflow.ExecuteActivity(ctx, backupVaultActivity.DeleteRemoteBackupVaultInVCP, &remoteParams).Get(ctx, &dbRemoteBackupVault)
		if err != nil {
			logger.Error("Failed to delete remote backup vault in VCP", "error", err, "params", backupVault)
			return nil, ConvertToVSAError(fmt.Errorf("DeleteRemoteBackupVaultInVCP failed: %w", err))
		}
	}

	// Step 4: Delete associated buckets (non-fatal — retries already exhausted by Temporal retry policy)
	bucketErr := workflow.ExecuteActivity(ctx, backupVaultActivity.DeleteBackupVaultBuckets, backupVault).Get(ctx, nil)
	if bucketErr != nil {
		logger.Error("Failed to delete backup vault buckets", "error", bucketErr, "params", backupVault)
	}

	return dbBackupVault, nil
}

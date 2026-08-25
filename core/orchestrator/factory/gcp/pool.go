package gcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/validators"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

var (
	// Pool size limits
	minQuotaInBytesPool = utils.MinQuotaInBytesPool
	maxQuotaInBytesPool = utils.MaxQuotaInBytesPool
	// Feature flags
	addressSpaceMgmtEnabled = env.GetBool(env.EnvAddressSpaceMgmtEnabled, false)
	// Function variables
	createPool                     = _createPool
	updatePool                     = _updatePool
	ValidatePoolParams             = _validatePoolParams
	ValidateCreatePoolParams       = _validateCreatePoolParams
	ValidateAndSetUpdatePoolParams = _validateAndSetUpdatePoolParams
	deletePool                     = _deletePool
	GetPoolByName                  = _getPoolByName
)

const (
	ServiceLevelNameFLEX                 = "FLEX"
	TieringFullnessThresholdOntapDefault = 50
	VCP_ADMIN_CERT_UN_SUFFIX             = "_admin" // Suffix for VCP admin user certificate
	AdminUserName                        = "admin"
	gcnvadminRole                        = "gcnvadmin" // only for backward compatibility
)

// CreatePool creates the specified pool and adds it to the list of pools belonging to the specified owner
func (o *GCPOrchestrator) CreatePool(ctx context.Context, params *commonparams.CreatePoolParams) (*models.Pool, string, error) {
	return createPool(ctx, o.storage, o.temporal, params)
}

// CreateSvm is not implemented for GCP; use OCI orchestrator for SVM creation.
func (o *GCPOrchestrator) CreateSvm(ctx context.Context, params *commonparams.CreateSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// DeleteSvm is not implemented for GCP; use OCI orchestrator for SVM deletion.
func (o *GCPOrchestrator) DeleteSvm(ctx context.Context, params *commonparams.DeleteSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// UpdateSvm is not implemented for GCP
func (o *GCPOrchestrator) UpdateSvm(ctx context.Context, params *commonparams.UpdateSvmParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// createPool creates a new pool and triggers asynchronous creation processes.
func _createPool(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.CreatePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}
	err = ValidateCreatePoolParams(params, logger)
	if err != nil {
		return nil, "", err
	}

	if commonparams.IsPORequest(params) {
		if err = validatePOPoolHost(ctx, se, account, params); err != nil {
			return nil, "", err
		}
	}

	// SDE parity: reject pool create when the AD being attached is diagnosing.
	if params.ActiveDirectoryId != "" && params.ADExistsInVCP && params.ActiveDirectory != nil &&
		params.ActiveDirectory.State == datamodel.LifeCycleStateDiagnosing {
		return nil, "", customerrors.NewUserInputValidationErr(ActiveDirectoryDiagnosingBlockedMessage)
	}

	// TODO: check error code
	if err = persistAccountTrialMetadataIfSet(ctx, se, account, params.TrialMode); err != nil {
		logger.Error("Failed to update account trial metadata", "accountUUID", account.UUID, "error", err)
		return nil, "", err
	}

	// GCP-specific credential setup function
	setupGCPCredentials := func(poolObj *datamodel.Pool, params *commonparams.CreatePoolParams, accountName string) {
		if commonparams.IsPORequest(params) {
			return
		}
		userName := utils.GenerateUniqueUsername(poolObj.DeploymentName)
		switch env.AuthType {
		case env.USER_CERTIFICATE:
			poolObj.PoolCredentials = &datamodel.PoolCredentials{
				SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
				CertificateID: fmt.Sprintf("%s-cert", poolObj.DeploymentName),
				Password:      "",
				AuthType:      env.USER_CERTIFICATE,
				CaURI:         env.BuildCaURI(env.CaPoolDeployedProjectID, env.CaPoolName, env.CaName),
				Username:      fmt.Sprintf("%s%s", userName, VCP_ADMIN_CERT_UN_SUFFIX),
			}
		case env.USERNAME_PWD_SEC_MGR:
			poolObj.PoolCredentials = &datamodel.PoolCredentials{
				SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
				CertificateID: "",
				Password:      "",
				AuthType:      env.USERNAME_PWD_SEC_MGR,
				Username:      AdminUserName,
			}
		default:
			poolObj.PoolCredentials = &datamodel.PoolCredentials{
				SecretID:      "",
				CertificateID: "",
				Password:      env.NodePassword,
				AuthType:      env.USERNAME_PWD,
				Username:      AdminUserName,
			}
		}

		if params.Mode == commonparams.ONTAPMode {
			expUserName := fmt.Sprintf("%s_%s", userName, env.ExpertModeUserSuffix)
			poolObj.ExpertModeCredentials = createExpertModeUser(poolObj, expUserName)
		}
	}

	dbPool, err2 := common.CreatePoolInDB(ctx, se, params, account, logger, setupGCPCredentials, TieringFullnessThresholdOntapDefault)
	if err2 != nil {
		logger.Error("Failed to create pool in database", "error", err2)
		// Check if it's a specific error that should be passed through
		if customerrors.IsConflictErr(err2) {
			return nil, "", err2
		}
		return nil, "", errors.New("unable to process request, please try again later")
	}

	if params.KmsConfig != nil {
		// move the kms config to in-use state
		if _, err = se.UpdateKmsConfigState(ctx, params.KmsConfig.UUID, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails); err != nil {
			logger.Error("Failed to update KMS config state to InUse", "KMSConfigID", params.KmsConfig.ID, "error", err)
			return nil, "", errors.New("unable to process request, please try again later")
		}
	}

	defer func() {
		if err != nil {
			common.CleanupPoolOnError(ctx, se, dbPool, err)
		}
	}()

	job := common.CreatePoolJob(ctx, params, account, dbPool)
	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", errors.New("unable to process request, please try again later")
	}

	// When address space management is enabled, look up registered address ranges for this VPC
	// and pass them as RequestedRanges to the subnet creation operation.
	// Ranges in both CREATED and IN_USE state are included — IN_USE means another pool is already
	// using this range, and GCP will allocate a new subnet block from it for this pool.
	params.RequestedRanges = resolveRequestedRanges(ctx, se, logger, params.VendorSubNetID, addressSpaceMgmtEnabled)
	logger.Info("Address space management", "enabled", addressSpaceMgmtEnabled, "requestedRanges", params.RequestedRanges, "network", params.VendorSubNetID)

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			common.HandleCreatePoolError(ctx, se, createdJob, err)
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.CreatePoolWorkflow,
		workflowengine.GetCreatePoolWorkflowRunTimeout(params.LargeCapacity),
		params,
		dbPool,
	)
	if err != nil {
		logger.Error("Failed to start pool create workflow: ", "error", err)
		return nil, "", err
	}

	poolView := database.ConvertPoolToPoolView(dbPool)
	return common.ConvertDatastorePoolToModel(poolView, account.Name), createdJob.UUID, nil
}

// resolveRequestedRanges returns the list of address range names to pass as RequestedRanges
// to GCP Service Networking during pool subnet creation. Returns nil when the feature flag is
// off, when the VPC cannot be parsed, or when the DB lookup fails (with a warning logged).
// Both CREATED and IN_USE ranges are included: IN_USE means another pool already uses the
// range and GCP will carve a new subnet block from it for this pool.
func resolveRequestedRanges(ctx context.Context, se database.Storage, logger log.Logger, vendorSubNetID string, addressSpaceMgmtEnabled bool) []string {
	if !addressSpaceMgmtEnabled || vendorSubNetID == "" {
		logger.Info("resolveRequestedRanges: skipping — address space management disabled or no network", "enabled", addressSpaceMgmtEnabled, "network", vendorSubNetID)
		return nil
	}
	hostProjectNumber, vpcName, _ := utils.ParseProjectId(vendorSubNetID)
	if hostProjectNumber == "" || vpcName == "" {
		logger.Warn("resolveRequestedRanges: could not parse network, skipping", "network", vendorSubNetID)
		return nil
	}
	lifType := database.AddressRangeLifTypeDataLIF
	addressRanges, err := se.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		logger.Warn("Failed to list address ranges for address space management; proceeding without RequestedRanges", "error", err)
		return nil
	}
	var requestedRanges []string
	for _, ar := range addressRanges {
		if ar.AddressRangeState == database.AddressRangeStateCreated || ar.AddressRangeState == database.AddressRangeStateInUse {
			requestedRanges = append(requestedRanges, ar.Name)
		}
	}
	logger.Info("resolveRequestedRanges: resolved address ranges", "requestedRanges", requestedRanges, "totalFound", len(addressRanges), "network", vendorSubNetID)
	return requestedRanges
}

func createExpertModeUser(poolObj *datamodel.Pool, userName string) *datamodel.ExpertModeCredentials {
	switch env.AuthType {
	case env.USER_CERTIFICATE:
		return &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{
				// first user id expert user
				{
					SecretID:      "",
					CertificateID: fmt.Sprintf("%s-cert-%s", poolObj.DeploymentName, userName),
					Password:      "",
					AuthType:      env.USER_CERTIFICATE,
					Username:      userName,
				},
			},
		}
	case env.USERNAME_PWD_SEC_MGR:
		return &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{
				// first user id expert user
				{
					SecretID:      fmt.Sprintf("%s-secret-%s", poolObj.DeploymentName, userName),
					CertificateID: "",
					Password:      "",
					AuthType:      env.USERNAME_PWD_SEC_MGR,
					Username:      userName,
				},
			},
		}
	default:
		return &datamodel.ExpertModeCredentials{
			ExpertModeCredential: []*datamodel.ExpertModeCredential{
				// first user id expert user
				{
					SecretID:      "",
					CertificateID: "",
					// TODO change this to  expert mode password
					Password: env.NodePassword,
					AuthType: env.USERNAME_PWD,
					Username: userName,
				},
			},
		}
	}
}

// UpdatePool updates the specified pool
// RotateFabricPoolKeys is OCI-only; GCP returns NotImplementedYetErr so the
// shared OrchestratorFactory interface stays satisfied. The GCP cluster does
// not expose a fabric pool object-store credential rotation today.
func (o *GCPOrchestrator) RotateFabricPoolKeys(ctx context.Context, params *commonparams.RotateFabricPoolKeysParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

// ResetAdminPasswordForPool is OCI-only; GCP returns NotImplementedYetErr so
// the shared OrchestratorFactory interface stays satisfied. GCP has no OCI
// expert-mode admin (ociadmin) user to reset.
func (o *GCPOrchestrator) ResetAdminPasswordForPool(ctx context.Context, params *commonparams.ResetOciAdminPasswordParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *GCPOrchestrator) StartPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *GCPOrchestrator) ExecutePoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *GCPOrchestrator) FinishPoolMaintenance(ctx context.Context, params *commonparams.PoolMaintenanceParams) (string, error) {
	return "", customerrors.NewNotImplementedYetErr()
}

func (o *GCPOrchestrator) UpdatePool(ctx context.Context, params *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	return updatePool(ctx, o.storage, o.temporal, params)
}

// _updatePool updates an existing pool
func _updatePool(ctx context.Context, se database.Storage, temporal client.Client, params *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	// Get the pool by ID
	dbPoolView, err := se.GetPool(ctx, params.PoolId, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", customerrors.NewNotFoundErr("pool not found", nil)
		}
		return nil, "", err
	}
	dbPool := database.ConvertPoolViewToPool(dbPoolView)
	if commonparams.IsPOPool(dbPool) {
		if err = validatePOPoolUpdate(ctx, se, params, dbPool); err != nil {
			return nil, "", err
		}
	} else {
		err = ValidateAndSetUpdatePoolParams(params, dbPool)
		if err != nil {
			return nil, "", err
		}
	}

	if params.ActiveDirectoryConfigId != "" && dbPool.ActiveDirectoryID.Valid {
		return nil, "", customerrors.NewUserInputValidationErr("Active Directory configuration cannot be changed for pools already associated with an Active Directory")
	}

	if commonparams.IsPOPool(dbPool) && params.ActiveDirectoryConfigId != "" {
		return nil, "", customerrors.NewUserInputValidationErr(commonparams.POValidationActiveDirectoryMsg)
	}

	// Reject pool updates while the pool's existing AD is being diagnosed (SDE parity).
	if dbPool.ActiveDirectoryID.Valid {
		existingAd, adErr := se.GetActiveDirectoryForPoolByPoolID(ctx, dbPool.ID)
		if adErr == nil && existingAd != nil && existingAd.State == datamodel.LifeCycleStateDiagnosing {
			return nil, "", customerrors.NewUserInputValidationErr(ActiveDirectoryDiagnosingPoolUpdateMessage)
		}
	}

	if params.ActiveDirectoryConfigId != "" && params.IfADExistsInVCP {
		activeDir, adErr := se.GetActiveDirectoryByUuidAndAccountId(ctx, params.ActiveDirectoryConfigId, account.ID)
		if adErr != nil {
			var notFoundErr *customerrors.NotFoundErr
			if errors.As(adErr, &notFoundErr) {
				return nil, "", customerrors.NewUserInputValidationErr(fmt.Sprintf("Active Directory Config with ID %s not found", params.ActiveDirectoryConfigId))
			}
			return nil, "", adErr
		}

		if dbPool.ActiveDirectoryID.Valid && dbPool.ActiveDirectoryID.Int64 != activeDir.ID {
			return nil, "", customerrors.NewUserInputValidationErr("Active Directory configuration cannot be changed for pools already associated with an Active Directory")
		}

		// Also reject if the AD being attached is currently being diagnosed.
		if activeDir != nil && activeDir.State == datamodel.LifeCycleStateDiagnosing {
			return nil, "", customerrors.NewUserInputValidationErr(ActiveDirectoryDiagnosingBlockedMessage)
		}

		dbPool.ActiveDirectoryID = sql.NullInt64{
			Int64: activeDir.ID,
			Valid: true,
		}
	}

	poolCategory := models.GetPoolCategory(dbPool.LargeCapacity)
	previousState := dbPool.State
	previousStateDetails := dbPool.StateDetails
	job := &datamodel.Job{
		Type:          string(models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationUpdate, poolCategory)),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.PoolId,
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         dbPool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	var poolMarkedAsUpdating bool
	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
			// Mark pool in error state only if it was successfully marked as updating
			if poolMarkedAsUpdating {
				if _, poolErr := se.UpdatePoolState(ctx, dbPool, previousState, previousStateDetails); poolErr != nil {
					logger.Error("Failed to update pool state to ERROR", "poolID", dbPool.UUID, "error", poolErr)
				}
			}
		}
	}()

	pool, err := se.UpdatingPool(ctx, dbPool)
	if err != nil {
		return nil, "", err
	}

	poolMarkedAsUpdating = true
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.UpdatePoolWorkflow,
		workflowengine.GetUpdatePoolWorkflowRunTimeout(pool.LargeCapacity),
		params,
		pool,
		nil,
	)
	if err != nil {
		logger.Error("Failed to start pool update workflow: ", "error", err)
		return nil, "", err
	}
	poolView := database.ConvertPoolToPoolView(pool)
	poolModel := common.ConvertDatastorePoolToModel(poolView, account.Name)
	// Merge update params into the pool model for 202 response
	// Params already contain merged values (either new from request or existing from pool)
	mergedPoolModel := mergeUpdateParamsIntoPoolModel(poolModel, params)
	return mergedPoolModel, createdJob.UUID, nil
}

// mergeUpdateParamsIntoPoolModel merges update params into a pool model.
// Only auto tiering parameters and pool size are updated from params if they are provided.
// Reuses poolModel and only creates a shallow copy to avoid mutating the original.
func mergeUpdateParamsIntoPoolModel(poolModel *models.Pool, params *commonparams.UpdatePoolParams) *models.Pool {
	// Shallow copy poolModel to avoid mutating the original
	merged := *poolModel

	// Copy PoolAttributes to avoid mutating the original
	if merged.PoolAttributes != nil {
		poolAttributesCopy := *merged.PoolAttributes
		// Copy Labels map to avoid sharing the same map reference
		if poolAttributesCopy.Labels != nil {
			labelsCopy := make(map[string]string)
			for k, v := range poolAttributesCopy.Labels {
				labelsCopy[k] = v
			}
			poolAttributesCopy.Labels = labelsCopy
		}
		merged.PoolAttributes = &poolAttributesCopy
	}

	// Update pool size if provided in params
	if params.SizeInBytes > 0 {
		merged.SizeInBytes = params.SizeInBytes
	}

	// Update AutoTieringConfig only if auto tiering parameters are provided in params
	if params.AllowAutoTiering || params.HotTierSizeInBytes > 0 {
		merged.AllowAutoTiering = params.AllowAutoTiering
		// Create a copy of AutoTieringConfig if it exists, otherwise create new
		if merged.AutoTieringConfig != nil {
			// Shallow copy the existing config
			autoTieringConfigCopy := *merged.AutoTieringConfig
			merged.AutoTieringConfig = &autoTieringConfigCopy
		} else {
			// Create new AutoTieringConfig if it doesn't exist
			merged.AutoTieringConfig = &models.AutoTieringConfig{}
		}

		// Update only the fields that are provided in params
		if params.HotTierSizeInBytes > 0 {
			merged.AutoTieringConfig.HotTierSizeInBytes = params.HotTierSizeInBytes
		}
		merged.AutoTieringConfig.EnableHotTierAutoResize = params.EnableHotTierAutoResize
	}

	return &merged
}

// GetPool gets the specified pool
func (o *GCPOrchestrator) DescribePool(ctx context.Context, poolId string, accountName string) (*models.Pool, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	pool, err := se.DescribePool(ctx, poolId, account.ID)
	if err != nil {
		return nil, err
	}

	// For ONTAP mode pools, get used capacity and volume count from expert mode volume table
	if err = enrichSinglePoolWithExpertModeCapacity(ctx, se, pool); err != nil {
		return nil, err
	}

	return common.ConvertDatastorePoolToModel(pool, pool.Account.Name), nil
}

// _validatePoolParams is a unified validation function that works for both create and update operations
func _validatePoolParams(perf *validators.CustomPerformance, serviceLevel string) error {
	// Validate service level for create operations (only needed for create)
	if serviceLevel != "" && serviceLevel != ServiceLevelNameFLEX {
		return customerrors.NewUserInputValidationErr("Given service level not supported. Supported service level is " + ServiceLevelNameFLEX)
	}

	// First validate common parameters
	if err := validators.ValidateCommonPoolParams(perf); err != nil {
		return err
	}

	// Then validate pool-specific parameters
	poolValidator := validators.NewPoolValidator(perf.LargeCapacity)
	pipeline := validators.NewValidationPipeline(poolValidator)
	return pipeline.Execute(perf)
}

// setPOPerformanceParams normalizes PO pool performance parameters for persistence.
// When qosEnabled is false, throughput and IOPS are cleared so CreatePoolInDB stores 0.
// When enabled, derived offering QoS is computed from service level and pool size.
func setPOPerformanceParams(params *commonparams.CreatePoolParams, qosEnabled bool) error {
	if params.CustomPerformanceParams == nil {
		params.CustomPerformanceParams = &commonparams.CustomPerformanceParams{}
	}
	params.CustomPerformanceParams.Enabled = false
	params.CustomPerformanceParams.ThroughputMibps = 0
	params.CustomPerformanceParams.Iops = nil

	if !qosEnabled {
		return nil
	}

	qos, qosErr := commonparams.ComputePOQoS(params.ServiceLevel, params.SizeInBytes)
	if qosErr != nil {
		return qosErr
	}
	params.CustomPerformanceParams.ThroughputMibps = qos.MaxThroughput
	params.CustomPerformanceParams.Iops = &qos.MaxIOPS
	return nil
}

// _validateCreatePoolParams now just builds CustomPerformance and calls the unified validator
func _validateCreatePoolParams(params *commonparams.CreatePoolParams, logger log.Logger) error {
	// PO (Performance Offering) pools take dedicated edge validation and bypass
	// the FLEX-only + custom-performance pipeline: they reject manual QoS and use
	// HARDWARE EXTREME|PREMIUM service levels on a named ONTAP cluster.
	if commonparams.IsPORequest(params) {
		if err := commonparams.ValidatePORequest(params); err != nil {
			return customerrors.NewUserInputValidationErr(err.Error())
		}
		if err := setPOPerformanceParams(params, commonparams.POSvmQoSEnabled()); err != nil {
			return customerrors.NewUserInputValidationErr(err.Error())
		}
	} else {
		// Build CustomPerformance params first
		perf := validators.NewCustomPerformanceFromCreate(params)

		// Call unified validation with service level check
		if err := ValidatePoolParams(perf, params.ServiceLevel); err != nil {
			return err
		}
		params.CustomPerformanceParams.Iops = perf.Iops
	}

	if params.KmsConfig != nil {
		switch params.KmsConfig.State {
		case datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateInUse:
			break
		default:
			// For ccfe there is no state called created, instead there is key check pending
			state := params.KmsConfig.State
			if state == datamodel.LifeCycleStateCreated {
				state = datamodel.LifeCycleStateKeyCheckPending
			}
			return customerrors.NewUserInputValidationErr(fmt.Sprintf("Invalid KMS configuration state for pool creation: %s", state))
		}
	}

	return nil
}

// _validateAndSetUpdatePoolParams now just builds CustomPerformance and calls the unified validator
func _validateAndSetUpdatePoolParams(params *commonparams.UpdatePoolParams, pool *datamodel.Pool) error {
	// Auto_tier checks for update with existing pool values
	if params.AllowAutoTiering {
		if !pool.AllowAutoTiering && params.HotTierSizeInBytes < uint64(pool.SizeInBytes) {
			return customerrors.NewUserInputValidationErr("Given hot tier size is not supported. Hot tier size cannot be less than existing pool size")
		} else if pool.AllowAutoTiering && pool.AutoTieringConfig != nil && params.HotTierSizeInBytes < uint64(pool.AutoTieringConfig.HotTierSizeInBytes) {
			return customerrors.NewUserInputValidationErr("Given hot tier size is not supported. Hot tier size must be greater than existing hot tier size")
		}
	} else if pool.AllowAutoTiering {
		return customerrors.NewUserInputValidationErr("Auto tiering disable operation is not supported")
	}
	// Build CustomPerformance params first
	perf := validators.NewCustomPerformanceFromUpdate(params)
	perf.LargeCapacity = pool.LargeCapacity // Use existing pool type for validation
	if pool.PoolAttributes != nil {
		perf.IsTrueZonal = pool.PoolAttributes.IsTrueZonal
	}
	// Use existing pool QosType for validation when update parameter is empty
	if perf.QosType == "" {
		perf.QosType = pool.QosType
	}
	// Prevent changing pool type
	if params.LargeCapacity != nil && (*params.LargeCapacity != pool.LargeCapacity) {
		return customerrors.NewUserInputValidationErr("Given large capacity value is not supported. Large capacity cannot be changed for existing pool")
	}

	// qosType transition (auto <-> manual) is allowed; workflow performs the transition.

	// Call unified validation (no service level check needed for updates)
	err := ValidatePoolParams(perf, "")
	if err != nil {
		return err
	}
	params.TotalIops = perf.Iops
	return nil
}

// DeletePool deletes the specified pool
func (o *GCPOrchestrator) DeletePool(ctx context.Context, params *commonparams.DeletePoolParams) (*models.Pool, string, error) {
	return deletePool(ctx, o.temporal, o.storage, params)
}

// _deletePool deletes the specified pool and its associated resources.
func _deletePool(ctx context.Context, temporal client.Client, se database.Storage, params *commonparams.DeletePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		return nil, "", err
	}

	// Get the pool by ID
	pool, err := se.GetPool(ctx, params.PoolID, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", customerrors.NewNotFoundErr("pool not found", nil)
		}
		return nil, "", err
	}

	// For ONTAP mode pools, get used capacity and volume count from expert mode volume table
	err = enrichSinglePoolWithExpertModeCapacity(ctx, se, pool)
	if err != nil {
		return nil, "", err
	}

	if pool.VolumeCount > 0 {
		return nil, "", customerrors.NewBadRequestErr("pool cannot be deleted with active volumes")
	}

	poolCategory := models.GetPoolCategory(pool.LargeCapacity)
	deleteJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationDelete, poolCategory)
	createJobType := models.GetResourceJobType(models.ResourceTypePool, models.ResourceOperationCreate, poolCategory)
	var existingDeleteJobUUID string
	if pool.State == datamodel.LifeCycleStateCreating {
		existingDeleteJobUUID, _, err = database.ValidateCorrelationIDForCreatingResource(
			ctx, se, pool.UUID, "pool", createJobType, deleteJobType, logger)
		if err != nil {
			logger.Warnf("Pool %s cannot be deleted: existing create job not present and state is in CREATING", pool.UUID)
			return nil, "", err
		}
		if existingDeleteJobUUID != "" {
			return common.ConvertDatastorePoolToModel(pool, params.AccountName), existingDeleteJobUUID, nil
		}
		logger.Infof("Create job found for pool %s with matching correlation ID, skipping state update to DELETING", pool.UUID)
	} else if utils.IsTransitionalState(pool.State) && pool.State != datamodel.LifeCycleStateDeleting {
		logger.Errorf("Pool %s cannot be deleted, while in transitioning state: %s", pool.Name, pool.State)
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("pool is in transition state and cannot be deleted, state: %s", pool.State))
	}

	existingJobUUID := database.GetExistingDeleteJobForDeletingState(ctx, se, pool.UUID, deleteJobType, logger)
	if existingJobUUID != "" {
		return common.ConvertDatastorePoolToModel(pool, params.AccountName), existingJobUUID, nil
	}

	dbpool := database.ConvertPoolViewToPool(pool)
	previousState := dbpool.State
	previousStateDetails := dbpool.StateDetails
	job := &datamodel.Job{
		Type:          string(deleteJobType),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  pool.Name,
		AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         pool.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	createdJob, err := se.CreateJob(ctx, job)
	if err != nil {
		logger.Error("Failed to create job in database", "error", err)
		return nil, "", err
	}

	// Defer statement to mark job as errored if workflow fails to start
	defer func() {
		if err != nil {
			if jobErr := se.UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), 0, err.Error()); jobErr != nil {
				logger.Error("Failed to update job status to error", "jobID", createdJob.UUID, "error", jobErr)
			}
		}
	}()

	if dbpool.State != datamodel.LifeCycleStateCreating {
		if err = se.DeletingPool(ctx, dbpool); err != nil {
			return nil, "", err
		}
	}

	defer func() {
		if err != nil {
			if _, poolErr := se.UpdatePoolState(ctx, dbpool, previousState, previousStateDetails); poolErr != nil {
				logger.Error("Failed to update pool state to ERROR", "poolID", dbpool.UUID, "error", poolErr)
			}
		}
	}()

	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		createdJob.WorkflowID,
		workflowengine.CustomerTaskQueue,
		workflows.DeletePoolWorkflow,
		nil,
		params,
		dbpool,
	)
	if err != nil {
		logger.Error("Failed to start pool delete workflow: ", "error", err)
		return nil, "", err
	}

	pool.State = dbpool.State
	pool.StateDetails = dbpool.StateDetails
	return common.ConvertDatastorePoolToModel(pool, account.Name), createdJob.UUID, nil
}

// ListPools returns list of pools belonging to the specified owner
func (o *GCPOrchestrator) ListPools(ctx context.Context, accountName string, includeDeleted bool) ([]*models.Pool, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	filter := utils2.CreateFilterWithConditions(utils2.NewFilterCondition("account_id", "=", account.ID))
	filter.SetIncludeDeleted(includeDeleted)
	pools, err := se.ListPools(ctx, filter)
	if err != nil {
		return nil, err
	}

	// For ONTAP mode pools, get used capacity and volume count from expert mode volume table
	if err = enrichPoolsWithExpertModeCapacity(ctx, se, pools); err != nil {
		return nil, err
	}

	return convertDatastorePoolsToModel(pools, account.Name), nil
}

// ListAllPools returns list of non-deleted pools
func (o *GCPOrchestrator) ListAllPools(ctx context.Context) ([]*models.Pool, error) {
	se := o.storage

	pools, err := se.ListPools(ctx, nil)
	if err != nil {
		return nil, err
	}

	// For ONTAP mode pools, get used capacity and volume count from expert mode volume table
	if err = enrichPoolsWithExpertModeCapacity(ctx, se, pools); err != nil {
		return nil, err
	}

	return convertDatastorePoolsToModelWithoutAccountNameParam(pools), nil
}

// GetMultiplePools returns multiple pools with uuids belonging to the specified owner
func (o *GCPOrchestrator) GetMultiplePools(ctx context.Context, accountName string, poolUUIDs []string) ([]*models.Pool, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			util.GetLogger(ctx).Warnf("Account with name %s not found in VCP, checking in CVP", accountName)
			return []*models.Pool{}, nil
		}
		return nil, err
	}

	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("uuid", "in", poolUUIDs),
		utils2.NewFilterCondition("account_id", "=", account.ID))
	pools, err := se.ListPools(ctx, filter)
	if err != nil {
		return nil, err
	}

	// For ONTAP mode pools, get used capacity and volume count from expert mode volume table
	if err = enrichPoolsWithExpertModeCapacity(ctx, se, pools); err != nil {
		return nil, err
	}

	return convertDatastorePoolsToModel(pools, account.Name), nil
}

// GetPoolsByUUIDs returns pools matching the given UUIDs across all accounts.
func (o *GCPOrchestrator) GetPoolsByUUIDs(ctx context.Context, poolUUIDs []string, opts commonparams.PoolFetchOptions) ([]*models.Pool, error) {
	se := o.storage

	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("uuid", "in", poolUUIDs))

	preloadOpts := database.PoolPreloadOptions{
		KmsConfig:       opts.NeedKmsConfig,
		ActiveDirectory: opts.NeedActiveDirectory,
	}
	pools, err := se.ListPoolsSelective(ctx, filter, preloadOpts)
	if err != nil {
		return nil, err
	}

	if opts.NeedExpertModeCapacity {
		if err = enrichPoolsWithExpertModeCapacity(ctx, se, pools); err != nil {
			return nil, err
		}
	}

	return convertDatastorePoolsToModelWithoutAccountNameParam(pools), nil
}

// GetPoolByVendorID retrieves a pool by its VendorID.
func (o *GCPOrchestrator) GetPoolByVendorID(ctx context.Context, vendorID string, accountName string) (*models.Pool, error) {
	se := o.storage
	account, err := getOrCreateAccount(ctx, se, accountName)
	if err != nil {
		return nil, err
	}
	pool, err := se.GetPoolByVendorID(ctx, vendorID, account.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("pool not found", nil)
		}
		return nil, err
	}
	return common.ConvertDatastorePoolToModel(pool, pool.Account.Name), nil
}

// GetPoolByName retrieves a pool with the specified name and owner.
func (o *GCPOrchestrator) GetPoolByName(ctx context.Context, poolName string, accountName string, queryDepth int) (*models.Pool, error) {
	return GetPoolByName(ctx, o.storage, poolName, accountName, queryDepth)
}

func _getPoolByName(ctx context.Context, se database.Storage, poolName string, accountName string, queryDepth int) (*models.Pool, error) {
	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}

	conditions := [][]interface{}{{"name = ?", poolName}}
	conditions = append(conditions, []interface{}{"account_id = ?", account.ID})
	pools, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		return nil, err
	}

	if queryDepth > 0 {
		return convertDatastorePoolToModelWithClusterDetails(pools, accountName), nil
	}

	return common.ConvertDatastorePoolToModel(pools, accountName), nil
}

func convertDatastorePoolToModelWithClusterDetails(pools *datamodel.PoolView, accountName string) *models.Pool {
	pool := common.ConvertDatastorePoolToModel(pools, accountName)

	pool.ClusterDetails = &models.ClusterDetails{
		InterClusterLifs:      pools.ClusterDetails.InterclusterLifIPs,
		ExternalName:          pools.ClusterDetails.ExternalName,
		Network:               pools.ClusterDetails.Network,
		SubnetNames:           pools.ClusterDetails.SubnetNames,
		RegionalTenantProject: pools.ClusterDetails.RegionalTenantProject,
		SnHostProject:         pools.ClusterDetails.SnHostProject,
	}

	return pool
}

// CustomPerformanceParams is used to specify the custom performance parameters for a pool
type CustomPerformanceParams struct {
	Enabled    bool
	Throughput float64
	Iops       int64
}

func convertDatastorePoolsToModel(pools []*datamodel.PoolView, accountName string) []*models.Pool {
	var poolsList []*models.Pool
	for _, pool := range pools {
		p := common.ConvertDatastorePoolToModel(pool, accountName)
		poolsList = append(poolsList, p)
	}
	return poolsList
}

func convertDatastorePoolsToModelWithoutAccountNameParam(pools []*datamodel.PoolView) []*models.Pool {
	var poolsList []*models.Pool
	for _, pool := range pools {
		accountName := pool.Account.Name
		p := common.ConvertDatastorePoolToModel(pool, accountName)
		poolsList = append(poolsList, p)
	}
	return poolsList
}

func (o *GCPOrchestrator) GetExpertModePoolCreds(ctx context.Context, poolUUID string, accountName string, userName string) (*models.UserCredentials, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}
	pool, err := se.GetPool(ctx, poolUUID, account.ID)
	if err != nil {
		return nil, err
	}

	// Dedicated-capacity pools use a separate pinned-host credential path.
	if pool.IsDedicatedCapacity() {
		return nil, nil
	}

	if pool.State == datamodel.LifeCycleStateCreating {
		return nil, coreerrors.NewVCPError(coreerrors.ErrPoolInCreatingState, fmt.Errorf("pool %s is in creating state", pool.UUID))
	}
	if pool.State == datamodel.LifeCycleStateDeleting {
		return nil, coreerrors.NewVCPError(coreerrors.ErrPoolInDeletingState, fmt.Errorf("pool %s is in deleting state", pool.UUID))
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return nil, err
	}

	if userName == AdminUserName {
		if pool.PoolCredentials == nil {
			return nil, nil
		}
		useHostDNS := pool.PoolCredentials.AuthType == env.USER_CERTIFICATE
		endpointMappings := buildOntapEndpoints(nodes, useHostDNS)
		return &models.UserCredentials{
			SecretID:       pool.PoolCredentials.SecretID,
			CertificateID:  pool.PoolCredentials.CertificateID,
			Password:       pool.PoolCredentials.Password,
			AuthType:       pool.PoolCredentials.AuthType,
			OntapEndpoints: endpointMappings,
			CaURI:          pool.PoolCredentials.GetCaURIWithFallback(),
			Username:       pool.PoolCredentials.Username,
			ServiceLevel:   pool.ServiceLevel,
			AccountUUID:    account.UUID,
			DeploymentName: pool.DeploymentName,
		}, nil
	}

	// For other users, get credentials from ExpertModeCredentials
	if pool.ExpertModeCredentials == nil || pool.ExpertModeCredentials.ExpertModeCredential == nil || len(pool.ExpertModeCredentials.ExpertModeCredential) == 0 {
		return nil, nil
	}

	for _, expertModeCredential := range pool.ExpertModeCredentials.ExpertModeCredential {
		if matchesCredential(userName, expertModeCredential.Username) {
			useHostDNS := expertModeCredential.AuthType == env.USER_CERTIFICATE
			endpointMappings := buildOntapEndpoints(nodes, useHostDNS)
			return &models.UserCredentials{
				SecretID:       expertModeCredential.SecretID,
				CertificateID:  expertModeCredential.CertificateID,
				Password:       expertModeCredential.Password,
				AuthType:       expertModeCredential.AuthType,
				OntapEndpoints: endpointMappings,
				CaURI:          pool.PoolCredentials.GetCaURIWithFallback(),
				Username:       expertModeCredential.Username,
				ServiceLevel:   pool.ServiceLevel,
				AccountUUID:    account.UUID,
				DeploymentName: pool.DeploymentName,
			}, nil
		}
	}

	return nil, errors.New("expert mode user not found")
}

// GetDedicatedClusterPoolCreds resolves ONTAP credentials for a dedicated-capacity
// pool using the pinned account-host inventory rather than the pool's own credentials.
//
// Behaviour:
//   - If the pool is not dedicated-capacity, it returns (nil, nil) so callers fall
//     back to the standard GetExpertModePoolCreds flow (existing architecture).
//   - The cluster to target is clusterExternalName when provided (from the
//     x-netapp-cluster-name header); otherwise it falls back to the pool's
//     ClusterDetails.ExternalName.
//   - Username and secret id come from the pinned account_hosts service credentials;
//     the password is resolved later from Secret Manager using the secret id.
//   - The management endpoint is the pinned host's IP address.
//
// userName is accepted for parity with GetExpertModePoolCreds and future RBAC use;
// the dedicated flow currently always uses the account-host service username.
func (o *GCPOrchestrator) GetDedicatedClusterPoolCreds(ctx context.Context, poolUUID string, accountName string, clusterExternalName string, userName string) (*models.UserCredentials, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		return nil, err
	}
	pool, err := se.GetPool(ctx, poolUUID, account.ID)
	if err != nil {
		return nil, err
	}

	if pool.State == datamodel.LifeCycleStateCreating {
		return nil, coreerrors.NewVCPError(coreerrors.ErrPoolInCreatingState, fmt.Errorf("pool %s is in creating state", pool.UUID))
	}
	if pool.State == datamodel.LifeCycleStateDeleting {
		return nil, coreerrors.NewVCPError(coreerrors.ErrPoolInDeletingState, fmt.Errorf("pool %s is in deleting state", pool.UUID))
	}

	clusterName := strings.TrimSpace(clusterExternalName)
	if clusterName == "" {
		clusterName = strings.TrimSpace(pool.ClusterDetails.ExternalName)
	}
	if clusterName == "" {
		return nil, coreerrors.NewVCPError(coreerrors.ErrMissingRequiredInputError,
			fmt.Errorf("dedicated pool %s has no cluster name from header or pool cluster details", pool.UUID))
	}

	hostCred, err := se.GetDedicatedPinnedHostCredential(ctx, accountName, clusterName)
	if err != nil {
		return nil, err
	}
	if hostCred == nil {
		return nil, coreerrors.NewVCPError(coreerrors.ErrMissingRequiredInputError,
			fmt.Errorf("no pinned dedicated host credential found for account %s cluster %s", accountName, clusterName))
	}

	endpoint := hostCred.ManagementIP()

	if userName == AdminUserName {
		// Admin path: use the cluster admin credentials stored directly in the hosts table.
		if hostCred.Host == nil {
			return nil, nil
		}
		return &models.UserCredentials{
			Username:       hostCred.Host.Username,
			Password:       hostCred.Host.Password,
			AuthType:       env.USERNAME_PWD,
			ServiceLevel:   pool.ServiceLevel,
			AccountUUID:    account.UUID,
			DeploymentName: pool.DeploymentName,
			OntapEndpoints: []models.OntapEndpoint{
				{IP: endpoint, DNS: endpoint},
			},
		}, nil
	}

	authType := dedicatedServiceUserAuthType(hostCred)
	dnsEndpoint := endpoint
	if authType == env.USER_CERTIFICATE {
		dnsEndpoint = commonparams.BuildHostManagementTLSName(clusterName)
	}

	return &models.UserCredentials{
		Username:       hostCred.Username,
		SecretID:       hostCred.SecretId,
		CertificateID:  hostCred.CertificateID,
		Password:       hostCred.Password,
		AccountUUID:    account.UUID,
		AuthType:       authType,
		CaURI:          hostCred.CaURI,
		ServiceLevel:   pool.ServiceLevel,
		DeploymentName: pool.DeploymentName,
		OntapEndpoints: []models.OntapEndpoint{
			{IP: endpoint, DNS: dnsEndpoint},
		},
	}, nil
}

// dedicatedServiceUserAuthType returns the auth type for a pinned account-host
// service user. When AuthType is unset (0), it defaults to inline username/password
// (USERNAME_PWD). Callers should populate AuthType on HostCredential from
// account_hosts.service_credentials (e.g. USERNAME_PWD_SEC_MGR or USER_CERTIFICATE).
func dedicatedServiceUserAuthType(hostCred *database.HostCredential) int {
	if hostCred == nil {
		return env.USERNAME_PWD
	}
	if hostCred.AuthType != 0 {
		return hostCred.AuthType
	}
	return env.USERNAME_PWD
}

// matchesCredential checks if credential matches the incoming userName
func matchesCredential(role, credUsername string) bool {
	if role == gcnvadminRole {
		// If incoming role is "gcnvadmin", only match "gcnvadmin"
		return credUsername == role
	} else if role == env.ExpertModeUserSuffix {
		// If incoming role is just the suffix (e.g., "gadmin"), match any ending with "_gadmin"
		return strings.HasSuffix(credUsername, fmt.Sprintf("_%s", role))
	}
	return false
}

func buildOntapEndpoints(nodes []*datamodel.Node, useHostDNS bool) []models.OntapEndpoint {
	var endpointMappings []models.OntapEndpoint
	for _, node := range nodes {
		mapping := models.OntapEndpoint{
			IP: node.EndpointAddress,
		}
		if useHostDNS {
			mapping.DNS = node.HostDNSName
		} else {
			mapping.DNS = node.EndpointAddress
		}
		endpointMappings = append(endpointMappings, mapping)
	}
	return endpointMappings
}

// getPoolDeploymentName safely gets the deployment name from pool, returning empty string if pool or deployment name is empty
func getPoolDeploymentName(pool *datamodel.Pool) string {
	if pool == nil {
		return ""
	}
	return pool.DeploymentName
}

// getPoolIsRegionalHA safely gets the IsRegionalHA flag from pool attributes, returning false if pool or pool attributes is nil
func getPoolIsRegionalHA(pool *datamodel.Pool) bool {
	if pool == nil || pool.PoolAttributes == nil {
		return false
	}
	return pool.PoolAttributes.IsRegionalHA
}

// enrichSinglePoolWithExpertModeCapacity updates a single ONTAP mode pool with expert mode capacity and volume count
func enrichSinglePoolWithExpertModeCapacity(ctx context.Context, se database.Storage, pool *datamodel.PoolView) error {
	if pool.APIAccessMode == commonparams.ONTAPMode {
		capacity, getError := se.GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID)
		if getError != nil {
			util.GetLogger(ctx).Errorf("Failed to get expert mode capacity for pool %s: %v", pool.UUID, getError)
			return getError
		}
		if capacity == nil {
			return nil
		}
		pool.QuotaInBytes = uint64(capacity.TotalSize)
		pool.VolumeCount = capacity.VolumeCount
	}
	return nil
}

// enrichPoolsWithExpertModeCapacity updates ONTAP mode pools with expert mode capacity and volume count
func enrichPoolsWithExpertModeCapacity(ctx context.Context, se database.Storage, pools []*datamodel.PoolView) error {
	for _, pool := range pools {
		if err := enrichSinglePoolWithExpertModeCapacity(ctx, se, pool); err != nil {
			return err
		}
	}
	return nil
}

package oci

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const (
	// ociDeploymentHashLen is the number of hash characters used (prefix length + ociDeploymentHashLen ≤ VLM DeploymentID max length).
	ociDeploymentHashLen = 16
	// ociNameMaxLen is the OCI resource name max length.
	ociNameMaxLen            = 255
	VCP_ADMIN_CERT_UN_SUFFIX = "_admin" // Suffix for VCP admin user certificate
)

var (
	// ociDeploymentPrefix is the OCI deployment name prefix, from OCI_DEPLOYMENT_NAME_PREFIX (default "ocnv-").
	ociDeploymentPrefix         = env.GetString("OCI_DEPLOYMENT_NAME_PREFIX", "ocnv-")
	ociDeploymentNameValidChars = regexp.MustCompile(`[^a-z0-9-]`)
	ociThroughputThresholdGBps  = env.GetInt("OCI_THROUGHPUT_THRESHOLD_GBPS", 5)
	ociNodeCapacityMaxTiB       = env.GetFloat64("OCI_NODE_CAPACITY_MAX_TIB", 425)
)

// GenerateDeploymentNameFromOCID generates a deployment name from OCID following OCI naming conventions.
// OCI naming: lowercase alphanumeric and hyphens, max OCINameMaxLen chars.
// Format: OCIDeploymentPrefix (OCI_DEPLOYMENT_NAME_PREFIX) + first OCIDeploymentHashLen chars of hash (truncated to ensure uniqueness and compliance).
func GenerateDeploymentNameFromOCID(ocid string) string {
	if ocid == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(ocid))
	hashStr := hex.EncodeToString(hash[:])
	deploymentName := fmt.Sprintf("%s%s", ociDeploymentPrefix, hashStr[:ociDeploymentHashLen])
	deploymentName = strings.ToLower(deploymentName)
	deploymentName = ociDeploymentNameValidChars.ReplaceAllString(deploymentName, "")
	return clampOCIDeploymentNameLength(deploymentName)
}

// clampOCIDeploymentNameLength truncates to OCINameMaxLen when the name exceeds the OCI limit.
func clampOCIDeploymentNameLength(s string) string {
	if len(s) > ociNameMaxLen {
		return s[:ociNameMaxLen]
	}
	return s
}

func resolveWorkflowID(requestedID string) string {
	if id := strings.TrimSpace(requestedID); id != "" {
		return id
	}
	return uuid.NewString()
}

func isCrashResume(persistedWorkflowID, requestWorkflowID string) bool {
	return requestWorkflowID != "" && persistedWorkflowID == requestWorkflowID
}

func isWorkflowAlreadyStarted(err error) bool {
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	return errors.As(err, &alreadyStarted)
}

// prepareOCICreatePoolParams sets OCI-specific params: deployment name from PoolOCID and VendorID when empty.
// Returns the deployment name for logging.
func prepareOCICreatePoolParams(params *commonparams.CreatePoolParams) string {
	deploymentName := GenerateDeploymentNameFromOCID(params.PoolOCID)
	params.DeploymentName = deploymentName
	if params.VendorID == "" {
		params.VendorID = params.PoolOCID
	}
	return deploymentName
}

func preparePool(
	params *commonparams.CreatePoolParams,
	account *datamodel.Account,
	tieringFullnessThreshold int) *datamodel.Pool {
	poolObj := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		Name:                   params.Name,
		Account:                account,
		AccountID:              account.ID,
		VendorID:               params.VendorID,
		PoolExternalIdentifier: params.PoolOCID,
		Network:                params.VendorSubNetID,
		SizeInBytes:            int64(params.SizeInBytes),
		AllowAutoTiering:       params.AllowAutoTiering,
		Description:            params.Description,
		ServiceLevel:           params.ServiceLevel,
		QosType:                params.QosType,
		LargeCapacity:          params.LargeCapacity,
		ClusterDetails: datamodel.ClusterDetails{
			CompartmentOCID: params.CompartmentOCID,
		},
		AutoTieringConfig: &datamodel.AutoTieringConfig{
			HotTierSizeInBytes:       int64(params.HotTierSizeInBytes),
			EnableHotTierAutoResize:  params.EnableHotTierAutoResize,
			TieringStatus:            datamodel.TieringStatusResumed,
			TieringFullnessThreshold: int64(tieringFullnessThreshold),
		},
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone:   params.PrimaryZone,
			SecondaryZone: params.SecondaryZone,
			MediatorZone:  params.MediatorZone,
			Labels:        params.Labels,
			IsRegionalHA:  params.IsRegionalHA,
			LdapEnabled:   params.LdapEnabled,
			AccountName:   account.Name,
		},
		APIAccessMode: params.Mode,
	}

	if params.CustomPerformanceParams != nil {
		poolObj.PoolAttributes.ThroughputMibps = params.CustomPerformanceParams.ThroughputMibps
		if params.CustomPerformanceParams.Iops != nil {
			poolObj.PoolAttributes.Iops = *params.CustomPerformanceParams.Iops
		}
	}

	if params.KmsConfig != nil {
		poolObj.KmsConfigID = sql.NullInt64{
			Int64: params.KmsConfig.ID,
			Valid: true,
		}
	}

	if params.ActiveDirectoryId != "" && params.ADExistsInVCP {
		poolObj.ActiveDirectoryID = sql.NullInt64{
			Int64: params.ActiveDirectory.ID,
			Valid: true,
		}
	}

	// Use pre-generated deployment name if provided (e.g., from OCID), otherwise generate one
	if params.DeploymentName != "" {
		poolObj.DeploymentName = params.DeploymentName
	}

	if poolObj.Name == "" {
		poolObj.Name = poolObj.DeploymentName
	}

	switch env.AuthType {
	case env.USER_CERTIFICATE:
		userName := utils.GenerateUniqueUsername(poolObj.DeploymentName)
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
			CertificateID: fmt.Sprintf("%s-cert", poolObj.DeploymentName),
			Password:      "",
			AuthType:      env.USER_CERTIFICATE,
			Username:      fmt.Sprintf("%s%s", userName, VCP_ADMIN_CERT_UN_SUFFIX),
		}
	case env.USERNAME_PWD_SEC_MGR:
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      fmt.Sprintf("%s-secret", poolObj.DeploymentName),
			CertificateID: "",
			Password:      "",
			AuthType:      env.USERNAME_PWD_SEC_MGR,
			Username:      env.OCIOntapAdminUsername,
		}
	default:
		poolObj.PoolCredentials = &datamodel.PoolCredentials{
			SecretID:      "",
			CertificateID: "",
			Password:      env.NodePassword,
			AuthType:      env.USERNAME_PWD,
			Username:      env.OCIOntapAdminUsername,
		}
	}

	return poolObj
}

// CreatePool creates the specified pool and adds it to the list of pools belonging to the specified owner
func (o *OCIOrchestrator) CreatePool(ctx context.Context, params *commonparams.CreatePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	// Get or create account (compartment for OCI)
	account, err := common.GetOrCreateAccount(ctx, se, params.AccountName)
	if err != nil {
		logger.Error("Failed to get or create account", "accountName", params.AccountName, "error", err)
		return nil, "", err
	}

	deploymentName := prepareOCICreatePoolParams(params)
	logger.Infof("Generated deployment name from OCID: %s -> %s", params.PoolOCID, deploymentName)

	workflowID := resolveWorkflowID(params.WorkflowID)

	poolObj := preparePool(params, account, 0)
	poolObj.WorkflowID = workflowID
	dbPool, err := se.CreatingPool(ctx, poolObj)
	if err != nil {
		logger.Error("Failed to create pool in database", "poolName", params.Name, "error", err)
		return nil, "", err
	}
	logger.Infof("Pool created in CREATING state: uuid=%s, name=%s, deploymentName=%s, state=%s", dbPool.UUID, dbPool.Name, dbPool.DeploymentName, dbPool.State)

	defer func() {
		if err != nil {
			common.CleanupPoolOnError(ctx, se, dbPool, err)
		}
	}()

	// Start the OCI pool creation workflow
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCICreatePoolWorkflow,
		workflowengine.GetCreatePoolWorkflowRunTimeout(params.LargeCapacity),
		params,
		dbPool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("OCI pool create workflow already started; returning idempotent success", "workflowID", workflowID)
			err = nil
			poolView := database.ConvertPoolToPoolView(dbPool)
			return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
		}
		logger.Error("Failed to start pool create workflow", "workflowID", workflowID, "error", err)
		return nil, "", err
	}
	logger.Infof("OCI pool creation workflow started successfully: workflowID=%s", workflowID)

	poolView := database.ConvertPoolToPoolView(dbPool)
	return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
}

// updatePool starts OCIUpdatePoolWorkflow for the given params. Each invocation uses only this
// call's params (size, throughput, etc.); a later call may use a different payload. Validation
// compares the request to the current persisted pool (no shrink).
//
// Allowed entry states are READY and ERROR. ERROR is allowed because after a failed workflow
// ErroredResource flips the pool to ERROR; the client may then retry with the same or a different
// payload. Any in-progress state (UPDATING, CREATING, DELETING, etc.) is rejected with 409
// because an operation is already in flight. DELETED is rejected with 400 because the pool no
// longer exists from the caller's point of view.
//
// Note: a worker crash mid-update can leave the row stuck in UPDATING. With this policy the
// pool will stay 409 until an operator/background reconciler flips it to ERROR.
func (o *OCIOrchestrator) updatePool(ctx context.Context, params *commonparams.UpdatePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal

	account, err := common.GetAccount(ctx, se, params.AccountName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			logger.Error("Account not found", "accountName", params.AccountName, "error", err)
			accountName := params.AccountName
			return nil, "", customerrors.NewNotFoundErr("account", &accountName)
		}
		logger.Error("Failed to get account", "accountName", params.AccountName, "error", err)
		return nil, "", err
	}

	conditions := [][]interface{}{{"pool_external_identifier = ?", params.PoolExternalIdentifier}}
	conditions = append(conditions, []interface{}{"account_id = ?", account.ID})
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			logger.Error("Pool not found", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
			return nil, "", customerrors.NewNotFoundErr("pool", &params.PoolExternalIdentifier)
		}
		return nil, "", err
	}

	workflowID := resolveWorkflowID(params.WorkflowID)
	resume := poolView.State == datamodel.LifeCycleStateUpdating && isCrashResume(poolView.WorkflowID, workflowID)

	if !resume {
		if err = validateUpdatePoolState(poolView.State, params.PoolExternalIdentifier); err != nil {
			logger.Error("Invalid pool state", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
			return nil, "", err
		}
		if err = o.validateNoActiveClusterUpgrade(ctx, poolView.UUID, params.PoolExternalIdentifier); err != nil {
			logger.Error("Active cluster upgrade in progress", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
			return nil, "", err
		}
	} else {
		if upgradeErr := o.validateNoActiveClusterUpgrade(ctx, poolView.UUID, params.PoolExternalIdentifier); upgradeErr != nil {
			logger.Error("Active cluster upgrade in progress during pool update resume", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", upgradeErr)
			pool := database.ConvertPoolViewToPool(poolView)
			if _, rbErr := se.ErroredResource(ctx, pool, upgradeErr.Error()); rbErr != nil {
				logger.Error("Failed to transition pool to ERROR after upgrade block on resume", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", rbErr)
			}
			return nil, "", upgradeErr
		}
	}

	pool := database.ConvertPoolViewToPool(poolView)

	if params.HAPairs > 0 && pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
		logger.Error("Rejecting dataEndpointCount update for non-shared HA pool", "poolExternalIdentifier", params.PoolExternalIdentifier)
		return nil, "", customerrors.NewBadRequestErr("dataEndpointCount cannot be updated for non-shared HA pools")
	}

	if err = validateUpdatePoolDataEndpointCount(ctx, se, pool, params); err != nil {
		logger.Error("Invalid dataEndpointCount update", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
		return nil, "", err
	}

	if err = validateUpdatePoolThroughput(params); err != nil {
		logger.Error("Invalid pool throughput", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
		return nil, "", err
	}

	if err = validateUpdatePoolNodeCapacities(ctx, se, pool, params); err != nil {
		logger.Error("Invalid pool node capacities", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", err)
		return nil, "", err
	}

	if len(params.NodeCapacities) > 0 {
		params.SizeInBytes = computeUpdatePoolSizeInBytes(params, pool.PoolAttributes.IsRegionalHA)
	}

	pool.WorkflowID = workflowID

	if !resume {
		pool, err = se.UpdatingPool(ctx, pool)
		if err != nil {
			return nil, "", err
		}
	}

	defer func() {
		if err != nil {
			if _, rbErr := se.ErroredResource(ctx, pool, err.Error()); rbErr != nil {
				logger.Error("Failed to rollback pool to ERROR state", "poolExternalIdentifier", params.PoolExternalIdentifier, "error", rbErr)
			}
		}
	}()

	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIUpdatePoolWorkflow,
		workflowengine.GetUpdatePoolWorkflowRunTimeout(false),
		params,
		pool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("OCI pool update workflow already started; returning idempotent success", "workflowID", workflowID, "poolExternalIdentifier", params.PoolExternalIdentifier)
			err = nil
			poolView = database.ConvertPoolToPoolView(pool)
			return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
		}
		logger.Error("Failed to start pool update workflow", "workflowID", workflowID, "error", err)
		return nil, "", err
	}

	logger.Info("OCI pool update workflow started", "workflowID", workflowID, "poolExternalIdentifier", params.PoolExternalIdentifier)

	poolView = database.ConvertPoolToPoolView(pool)
	return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
}

func validateUpdatePoolState(state, poolExternalIdentifier string) error {
	switch state {
	case datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateError:
		return nil
	case datamodel.LifeCycleStateDeleted:
		return customerrors.NewBadRequestErr(fmt.Sprintf("pool %s is deleted and cannot be updated", poolExternalIdentifier))
	case datamodel.LifeCycleStateUpdating,
		datamodel.LifeCycleStateCreating,
		datamodel.LifeCycleStateDeleting,
		datamodel.LifeCycleStatePreparing,
		datamodel.LifeCycleStateMigrating:
		return customerrors.NewConflictErr(fmt.Sprintf("pool cannot be updated: an operation is already in progress (state=%s)", state))
	default:
		return customerrors.NewConflictErr(fmt.Sprintf("pool cannot be updated in state %s, must be READY or ERROR after a failed update", state))
	}
}

// validatePoolNotUnderUpgrade rejects the given operation when the pool has
// an active (PENDING or IN_PROGRESS) cluster upgrade job.
func (o *OCIOrchestrator) validatePoolNotUnderUpgrade(ctx context.Context, clusterUUID, poolExternalIdentifier, operation string) error {
	jobs, err := o.storage.GetClusterUpgradeJobsByClusterID(ctx, clusterUUID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("validatePoolNotUnderUpgrade: list cluster upgrade jobs for pool %q: %w", poolExternalIdentifier, err))
	}
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if job.Status == string(models.UpgradeStatusPending) || job.Status == string(models.UpgradeStatusInProgress) {
			return customerrors.NewConflictErr(
				fmt.Sprintf("%s is blocked on pool %s: a cluster upgrade is in progress (workflowID=%s, status=%s)",
					operation, poolExternalIdentifier, clusterUpgradeWorkflowIDFromJob(job), job.Status))
		}
	}
	return nil
}

func (o *OCIOrchestrator) validateNoActiveClusterUpgrade(ctx context.Context, clusterUUID, poolExternalIdentifier string) error {
	return o.validatePoolNotUnderUpgrade(ctx, clusterUUID, poolExternalIdentifier, "pool update")
}

func validateUpdatePoolThroughput(params *commonparams.UpdatePoolParams) error {
	if !params.CustomPerformanceEnabled {
		return nil
	}
	if params.TotalThroughputMibps == 0 {
		return customerrors.NewBadRequestErr("throughputGBps must be non-zero when provided")
	}
	capMibps := int64(float64(ociThroughputThresholdGBps) * workflowquery.MiBpsPerGBps)
	if params.TotalThroughputMibps >= capMibps {
		return customerrors.NewBadRequestErr(
			fmt.Sprintf("throughputGBps must be less than %d GBps", ociThroughputThresholdGBps))
	}
	return nil
}

func validateUpdatePoolDataEndpointCount(
	ctx context.Context,
	se database.Storage,
	pool *datamodel.Pool,
	params *commonparams.UpdatePoolParams,
) error {
	if params.HAPairs == 0 {
		return nil
	}
	if pool == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("validateUpdatePoolDataEndpointCount: pool is nil"))
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("validateUpdatePoolDataEndpointCount: list nodes for pool %q (id=%d): %w", pool.UUID, pool.ID, err))
	}

	currentHAPairs := uint64(len(nodes) / 2)
	if params.HAPairs <= currentHAPairs {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("dataEndpointCount cannot shrink pool %q: current dataEndpointCount=%d, requested dataEndpointCount=%d; dataEndpointCount can only be increased",
				pool.PoolExternalIdentifier, currentHAPairs*2, params.HAPairs*2))
	}
	return nil
}

func computeUpdatePoolSizeInBytes(params *commonparams.UpdatePoolParams, isRegionalHA bool) uint64 {
	var totalNodeGiB int64
	for _, nc := range params.NodeCapacities {
		totalNodeGiB += nc.SizeInGiB
	}
	if isRegionalHA {
		totalNodeGiB = totalNodeGiB / 2
	}
	return uint64(totalNodeGiB) * 1024 * 1024 * 1024
}

func validateUpdatePoolNodeCapacities(
	ctx context.Context,
	se database.Storage,
	pool *datamodel.Pool,
	params *commonparams.UpdatePoolParams,
) error {
	if pool == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError, fmt.Errorf("validateUpdatePoolNodeCapacities: pool is nil"))
	}
	if len(params.NodeCapacities) == 0 {
		return nil
	}

	maxGiB := int64(ociNodeCapacityMaxTiB * 1024)
	totalInputGiB := int64(0)
	for _, nc := range params.NodeCapacities {
		if nc.SizeInGiB > maxGiB {
			return customerrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities.sizeInGiB %d for node_uuid %q exceeds the configured per-node maximum of %d GiB (%g TiB)",
					nc.SizeInGiB, nc.NodeUUID, maxGiB, ociNodeCapacityMaxTiB))
		}
		totalInputGiB += nc.SizeInGiB
	}

	nodes, err := se.GetNodesByPoolID(ctx, pool.ID)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("validateUpdatePoolNodeCapacities: list nodes for pool %q (id=%d): %w", pool.UUID, pool.ID, err))
	}
	nodeByUUID := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		if n != nil && n.UUID != "" {
			nodeByUUID[n.UUID] = struct{}{}
		}
	}
	var unknown []string
	for _, nc := range params.NodeCapacities {
		if _, ok := nodeByUUID[strings.TrimSpace(nc.NodeUUID)]; !ok {
			unknown = append(unknown, fmt.Sprintf("%q", nc.NodeUUID))
		}
	}
	if len(unknown) > 0 {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("nodeCapacities references node_uuid(s) that are not part of pool %q: %s",
				pool.PoolExternalIdentifier, strings.Join(unknown, ", ")))
	}

	if len(params.NodeCapacities) != len(nodeByUUID) {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("nodeCapacities must cover every node in pool %q: pool has %d nodes, request has %d entries",
				pool.PoolExternalIdentifier, len(nodeByUUID), len(params.NodeCapacities)))
	}

	if err := validateNodeCapacityPerNodeRules(pool, params, nodes); err != nil {
		return err
	}

	var requestedPoolGiB int64
	if pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
		requestedPoolGiB = totalInputGiB / 2
	} else {
		requestedPoolGiB = totalInputGiB
	}
	currentPoolGiB := int64(pool.SizeInBytes / (1024 * 1024 * 1024))
	if requestedPoolGiB > 0 && currentPoolGiB > requestedPoolGiB {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("nodeCapacities cannot shrink pool %q: current size=%d GiB, requested size=%d GiB",
				pool.PoolExternalIdentifier, currentPoolGiB, requestedPoolGiB))
	}
	return nil
}

var rotateFabricPoolKeys = _rotateFabricPoolKeys

func (o *OCIOrchestrator) RotateFabricPoolKeys(ctx context.Context, params *commonparams.RotateFabricPoolKeysParams) (string, error) {
	return rotateFabricPoolKeys(ctx, o.storage, o.temporal, params)
}

func _rotateFabricPoolKeys(
	ctx context.Context,
	se database.Storage,
	temporal client.Client,
	params *commonparams.RotateFabricPoolKeysParams,
) (string, error) {
	logger := util.GetLogger(ctx)

	if params == nil {
		return "", customerrors.NewBadRequestErr("RotateFabricPoolKeysParams is required")
	}

	account, err := common.GetAccount(ctx, se, params.AccountName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			accountName := params.AccountName
			logger.Error("Account not found for fabric pool key rotation",
				"accountName", params.AccountName, "poolOCID", params.PoolOCID, "error", err)
			return "", customerrors.NewNotFoundErr("account", &accountName)
		}
		logger.Error("Failed to load account for fabric pool key rotation",
			"accountName", params.AccountName, "poolOCID", params.PoolOCID, "error", err)
		return "", err
	}

	conditions := [][]interface{}{{"pool_external_identifier = ?", params.PoolOCID}}
	conditions = append(conditions, []interface{}{"account_id = ?", account.ID})
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			poolOCID := params.PoolOCID
			logger.Error("Pool not found in caller's tenancy for fabric pool key rotation",
				"poolOCID", params.PoolOCID, "accountName", params.AccountName)
			return "", customerrors.NewNotFoundErr("pool", &poolOCID)
		}
		return "", err
	}

	if err = validateUpdatePoolState(poolView.State, params.PoolOCID); err != nil {
		logger.Error("Pool state rejects fabric pool key rotation",
			"poolOCID", params.PoolOCID, "state", poolView.State, "error", err)
		return "", err
	}

	if err = (&OCIOrchestrator{storage: se}).validateNoActiveClusterUpgrade(ctx, poolView.UUID, params.PoolOCID); err != nil {
		logger.Error("Active cluster upgrade in progress", "poolExternalIdentifier", params.PoolOCID, "error", err)
		return "", err
	}

	pool := database.ConvertPoolViewToPool(poolView)

	fabricPoolConfig, err := currentFabricPoolConfig(pool)
	if err != nil {
		logger.Error("Failed to parse stored VLMConfig for fabric pool key rotation",
			"poolOCID", params.PoolOCID, "poolUUID", pool.UUID, "error", err)
		return "", err
	}
	if fabricPoolConfig == nil {
		logger.Error("Fabric pool key rotation requested for a pool without a fabric pool configured",
			"poolOCID", params.PoolOCID, "poolUUID", pool.UUID)
		return "", customerrors.NewBadRequestErr(
			fmt.Sprintf("pool %s does not have a fabric pool configured; nothing to rotate", params.PoolOCID))
	}

	updatedPool, err := se.UpdatingPool(ctx, pool)
	if err != nil {
		logger.Error("Failed to transition pool to UPDATING for fabric pool key rotation",
			"poolOCID", params.PoolOCID, "error", err)
		return "", err
	}
	pool = updatedPool

	defer func() {
		if err != nil {
			if _, rbErr := se.ErroredResource(ctx, pool, err.Error()); rbErr != nil {
				logger.Error("Failed to roll pool back to ERROR after dispatch failure",
					"poolOCID", params.PoolOCID, "poolUUID", pool.UUID, "error", rbErr)
			}
		}
	}()

	workflowID := uuid.NewString()
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIRotateFabricPoolKeysWorkflow,
		workflowengine.GetRotateFabricPoolKeysWorkflowRunTimeout(),
		params,
		pool,
	)
	if err != nil {
		logger.Error("Failed to start OCI fabric pool key rotation workflow",
			"workflowID", workflowID,
			"poolOCID", params.PoolOCID,
			"poolUUID", pool.UUID,
			"error", err)
		return "", err
	}

	logger.Info("OCI fabric pool key rotation workflow started",
		"workflowID", workflowID,
		"poolOCID", params.PoolOCID,
		"poolUUID", pool.UUID)
	return workflowID, nil
}

type ociVLMConfigView struct {
	Deployment struct {
		ProviderConfig vlm.OCIConfig `json:"provider_config"`
	} `json:"deployment"`
}

func currentFabricPoolConfig(pool *datamodel.Pool) (*vlm.FabricPoolConfig, error) {
	if pool == nil || pool.VLMConfig == "" {
		return nil, nil
	}
	var cfg ociVLMConfigView
	if err := json.Unmarshal([]byte(pool.VLMConfig), &cfg); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVLMConfigParseError,
			fmt.Errorf("failed to parse stored VLMConfig for pool %q: %w", pool.UUID, err))
	}
	fpc := cfg.Deployment.ProviderConfig.FabricPoolConfig
	if fpc == (vlm.FabricPoolConfig{}) {
		return nil, nil
	}
	return &fpc, nil
}

func validateNodeCapacityPerNodeRules(
	pool *datamodel.Pool,
	params *commonparams.UpdatePoolParams,
	nodes []*datamodel.Node,
) error {
	sizeByUUID := make(map[string]int64, len(nodes))
	for _, n := range nodes {
		if n != nil && n.UUID != "" && n.NodeAttributes != nil {
			sizeByUUID[n.UUID] = n.NodeAttributes.SizeInGiB
		}
	}

	// TODO: heterogeneous (per-HA-pair) updates will require validating that the
	// two nodes of each HA pair request the same sizeInGiB. We cannot pair by DB
	// node ordering: there is no HA-pair identifier on the node and adjacency is
	// not guaranteed. When that flow lands, derive the pairs from the stored VLM
	// config (pool.VLMConfig -> vlm.VLMConfig.Cloud.HAPairs[i].VM1/VM2), which is
	// the consistent source of HA-pair grouping, mapping VMConfig.HostName to
	// Node.Name. Until then the pool is homogeneous-only, so we enforce a single
	// global rule: every node must request the same sizeInGiB.
	var firstUUID string
	var firstSize int64
	var firstSet bool
	for _, nc := range params.NodeCapacities {
		uuid := strings.TrimSpace(nc.NodeUUID)
		if cur := sizeByUUID[uuid]; cur > 0 && nc.SizeInGiB <= cur {
			return customerrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities cannot shrink node %q in pool %q: current size=%d GiB, requested size=%d GiB",
					nc.NodeUUID, pool.PoolExternalIdentifier, cur, nc.SizeInGiB))
		}
		if !firstSet {
			firstUUID = uuid
			firstSize = nc.SizeInGiB
			firstSet = true
			continue
		}
		if nc.SizeInGiB != firstSize {
			return customerrors.NewUserInputValidationErr(
				fmt.Sprintf("nodeCapacities must request the same sizeInGiB for every node in pool %q until heterogeneous updates are supported: node %q=%d GiB, node %q=%d GiB",
					pool.PoolExternalIdentifier, firstUUID, firstSize, uuid, nc.SizeInGiB))
		}
	}
	return nil
}

func (o *OCIOrchestrator) DeletePool(ctx context.Context, params *commonparams.DeletePoolParams) (*models.Pool, string, error) {
	logger := util.GetLogger(ctx)
	se := o.storage
	temporal := o.temporal
	workflowExecutor := workflows.NewWorkflowExecutor(temporal, logger)

	// Validate required request fields before database access.
	if params.PoolOCID == "" {
		return nil, "", customerrors.NewBadRequestErr("PoolOCID is required")
	}

	account, err := common.GetAccount(ctx, se, params.AccountName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			accountName := params.AccountName
			return nil, "", customerrors.NewNotFoundErr("account", &accountName)
		}
		logger.Error("Failed to get account", "accountName", params.AccountName, "error", err)
		return nil, "", err
	}

	conditions := [][]interface{}{{"pool_external_identifier = ?", params.PoolOCID}}
	conditions = append(conditions, []interface{}{"account_id = ?", account.ID})
	poolView, err := se.GetPoolByName(ctx, conditions)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || customerrors.IsNotFoundErr(err) {
			poolOCID := params.PoolOCID
			return nil, "", customerrors.NewNotFoundErr("pool", &poolOCID)
		}
		return nil, "", err
	}

	workflowID := resolveWorkflowID(params.WorkflowID)
	resume := poolView.State == datamodel.LifeCycleStateDeleting && isCrashResume(poolView.WorkflowID, workflowID)

	if !resume && utils.IsTransitionalState(poolView.State) {
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("pool is in transition state and cannot be deleted, state: %s", poolView.State))
	}

	if !resume {
		if err = o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "pool deletion"); err != nil {
			return nil, "", err
		}

		activeSvmExists, svmErr := se.ActiveSvmExistsByPoolID(ctx, poolView.ID)
		if svmErr != nil {
			logger.Error("Failed to check for existing SVMs before pool deletion", "poolUUID", poolView.UUID, "error", svmErr)
			return nil, "", svmErr
		}
		if activeSvmExists {
			return nil, "", customerrors.NewConflictErr("pool cannot be deleted while it has existing SVMs; delete the SVMs first")
		}
	} else {
		if upgradeErr := o.validatePoolNotUnderUpgrade(ctx, poolView.UUID, params.PoolOCID, "pool deletion"); upgradeErr != nil {
			logger.Error("Active cluster upgrade in progress during pool delete resume", "poolUUID", poolView.UUID, "error", upgradeErr)
			pool := database.ConvertPoolViewToPool(poolView)
			if _, rbErr := se.ErroredResource(ctx, pool, upgradeErr.Error()); rbErr != nil {
				logger.Error("Failed to transition pool to ERROR after upgrade block on resume", "poolUUID", poolView.UUID, "error", rbErr)
			}
			return nil, "", upgradeErr
		}
	}

	pool := database.ConvertPoolViewToPool(poolView)
	pool.WorkflowID = workflowID

	if !resume {
		if err = se.DeletingPool(ctx, pool); err != nil {
			return nil, "", err
		}
	}

	params.PoolID = pool.UUID
	err = workflowExecutor.ExecuteWorkflow(
		ctx,
		workflowID,
		workflowengine.CustomerTaskQueue,
		ociworkflows.OCIDeletePoolWorkflow,
		workflowengine.GetDeletePoolWorkflowRunTimeout(),
		params,
		pool,
	)
	if err != nil {
		if isWorkflowAlreadyStarted(err) {
			logger.Info("OCI pool delete workflow already started; returning idempotent success", "workflowID", workflowID)
			poolView.State = pool.State
			poolView.StateDetails = pool.StateDetails
			return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
		}
		return nil, "", err
	}

	poolView.State = pool.State
	poolView.StateDetails = pool.StateDetails
	return common.ConvertDatastorePoolToModel(poolView, account.Name), workflowID, nil
}

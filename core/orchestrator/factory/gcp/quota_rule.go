package gcp

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

const (
	// Quota type constants
	IndividualUserQuota                = "INDIVIDUAL_USER_QUOTA"
	IndividualGroupQuota               = "INDIVIDUAL_GROUP_QUOTA"
	DefaultUserQuota                   = "DEFAULT_USER_QUOTA"
	DefaultGroupQuota                  = "DEFAULT_GROUP_QUOTA"
	DiskQuotaLowerLimit          int64 = 4
	DiskQuotaUpperLimit          int64 = 1125899906842620
	VolumeQuotaRulesDefaultLimit       = 100
	kibToMibDivisor                    = 1024
	mibToKibMultiplier                 = 1024
	mibToBytesMultiplier               = 1024 * 1024
	// ONTAP mirror state constants
	OntapSnapmirrored  = "snapmirrored"
	OntapUninitialized = "uninitialized"
)

var (
	createQuotaRule                  = _createQuotaRule
	createQuotaRuleInternal          = _createQuotaRuleInternal
	updateQuotaRule                  = _updateQuotaRule
	updateQuotaRuleInternal          = _updateQuotaRuleInternal
	deleteQuotaRule                  = _deleteQuotaRule
	deleteQuotaRuleInternal          = _deleteQuotaRuleInternal
	listQuotaRules                   = _listQuotaRules
	validateQuotaRuleCreateParams    = _validateQuotaRuleCreateParams
	convertDatastoreQuotaRuleToModel = _convertDatastoreQuotaRuleToModel
	getDestinationReplication        = _getDestinationReplication
	internalUtilGetSignedToken       = auth.GetSignedJwtToken
	internalUtilGetPairedRegionURI   = utils.GetPairedRegionURI
	internalParseRegionAndZone       = utils.ParseRegionAndZone
	convertQuotaRulesV1betaToDM      = _convertQuotaRulesV1betaToDataModel
	replaceDstQuotaRulesWithSrcFn    = _replaceDstQuotaRulesWithSrc
)

// isTransitionState checks if state is in a transitioning state
// (CREATING, UPDATING, or DELETING). These states indicate the resource is currently
// undergoing an operation and should not be modified.
func isTransitionState(state string) bool {
	return state == datamodel.LifeCycleStateCreating ||
		state == datamodel.LifeCycleStateUpdating ||
		state == datamodel.LifeCycleStateDeleting
}

// validateQuotaRuleCreateParams validates quota rule creation parameters
func _validateQuotaRuleCreateParams(params *common.CreateQuotaRulesParam) error {
	if params.Name == "" {
		return customerrors.NewUserInputValidationErr("Quota rule name is required")
	}

	diskLimitInKiB := params.DiskLimitInMib * mibToKibMultiplier
	if diskLimitInKiB < DiskQuotaLowerLimit || diskLimitInKiB > DiskQuotaUpperLimit {
		return customerrors.NewUserInputValidationErr("DiskLimit is outside the permissible range")
	}

	if params.QuotaTarget == "" && (params.QuotaType == IndividualUserQuota || params.QuotaType == IndividualGroupQuota) {
		return customerrors.NewUserInputValidationErr(
			"quotaTarget has to be specified for Individual user/group quotaType. To create this quotaRule, change quotaType to DefaultUserQuota or DefaultGroupQuota")
	}

	if params.QuotaTarget != "" && (params.QuotaType == DefaultUserQuota || params.QuotaType == DefaultGroupQuota) {
		return customerrors.NewUserInputValidationErr(
			"quotaTarget cannot be specified for Default user/group quotaType. To create this quotaRule, change quotaType to IndividualUserQuota or IndividualGroupQuota")
	}
	return nil
}

// _getDestinationReplication fetches replication details from the destination region
func _getDestinationReplication(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
	return activities.GetReplicationDetails(ctx, basePath, projectNumber, locationID, volumeReplicationID, jwt)
}

// validateReplicationState validates that quota rules are not created on destination volumes with active replication
func validateReplicationState(ctx context.Context, se database.Storage, volume *datamodel.Volume, locationID string) error {
	logger := util.GetLogger(ctx)

	// Fetch replications for this volume using ListVolumeReplications with volume_id filter
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("volume_id", "=", volume.ID))

	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		logger.Error("Failed to list volume replications", "error", err)
		return err
	}

	// If no replications exist, validation passes
	if len(replications) == 0 {
		return nil
	}

	// Check each replication
	for _, replication := range replications {
		if replication.ReplicationAttributes == nil {
			return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed: replication attributes are missing")
		}

		if replication.HybridReplicationAttributes != nil {
			return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed on hybrid replication volumes")
		}

		// Check for cross-project replication: quota rule sync is not allowed
		// Only perform this check when RemoteUri is available (source side)
		if replication.RemoteUri != "" {
			if volume.Account == nil {
				logger.Error("Volume account is nil, cannot validate cross-project replication")
				return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed: volume account information is missing")
			}

			destProjectNumber, err := utils.ParseProjectNumberFromURI(replication.RemoteUri)
			if err != nil {
				logger.Error("Failed to parse destination project number from RemoteUri", "error", err, "remoteUri", replication.RemoteUri)
				return fmt.Errorf("failed to parse destination project number: %w", err)
			}

			srcProjectNumber := volume.Account.Name
			if destProjectNumber != srcProjectNumber {
				logger.Error("QuotaRule sync for cross project replication is not allowed",
					"srcProjectNumber", srcProjectNumber, "destProjectNumber", destProjectNumber)
				return customerrors.NewUserInputValidationErr("QuotaRule sync for cross project replication is not allowed")
			}
		}

		// Check for in-region replication: quota rule operations are not allowed
		// Only perform check if both SourceLocation and DestinationLocation are available
		if replication.ReplicationAttributes.SourceLocation != "" && replication.ReplicationAttributes.DestinationLocation != "" {
			// Parse source region first - if this fails, it's fatal
			sourceRegion, _, sourceParseErr := internalParseRegionAndZone(replication.ReplicationAttributes.SourceLocation)
			if sourceParseErr != nil {
				logger.Error("Failed to parse source location for in-region check", "error", sourceParseErr, "sourceLocation", replication.ReplicationAttributes.SourceLocation)
				return coreerrors.NewVCPError(coreerrors.ErrParseSourceLocation, fmt.Errorf("failed to parse source location: %w", sourceParseErr))
			}

			// Parse destination region - if this fails, it's fatal
			destRegion, _, destParseErr := internalParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
			if destParseErr != nil {
				logger.Error("Failed to parse destination location for in-region check", "error", destParseErr, "destinationLocation", replication.ReplicationAttributes.DestinationLocation)
				return coreerrors.NewVCPError(coreerrors.ErrParseDestinationLocation, fmt.Errorf("failed to parse destination location: %w", destParseErr))
			}

			// Both regions parsed successfully - check if they match (in-region replication)
			if sourceRegion == destRegion {
				logger.Error("QuotaRule Operation is not allowed on in-region replication volumes",
					"sourceRegion", sourceRegion, "destRegion", destRegion)
				return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed on in-region replication volumes")
			}
		}

		// Determine which replication to validate based on destination location
		var destinationReplication *models.VolumeReplication

		// Check if the current location is the destination location for this replication
		// If destination location matches the current locationID, we are on the destination side
		if replication.ReplicationAttributes.DestinationLocation == locationID {
			// Current location is the destination - use local replication data
			logger.Info("Current location matches destination location, using local replication data", "locationID", locationID)
			destinationReplication = &models.VolumeReplication{
				State:              replication.State,
				MirrorState:        replication.MirrorState,
				RelationshipStatus: replication.RelationshipStatus,
			}
		} else {
			// Current location is NOT the destination - this is the source side, fetch destination replication
			// Parse destination project number from RemoteUri (should be available on source side)
			// Note: Cross-project check is already done above when RemoteUri is available
			if replication.RemoteUri == "" {
				logger.Error("RemoteUri is empty on source side, cannot fetch destination replication")
				return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed: remote URI is missing for source replication")
			}

			destProjectNumber, err := utils.ParseProjectNumberFromURI(replication.RemoteUri)
			if err != nil {
				logger.Error("Failed to parse destination project number from RemoteUri", "error", err, "remoteUri", replication.RemoteUri)
				return fmt.Errorf("failed to parse destination project number: %w", err)
			}

			// Parse destination region from location
			destRegion, _, parseError := internalParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
			if parseError != nil {
				logger.Error("Failed to parse destination location", "error", parseError)
				return coreerrors.NewVCPError(coreerrors.ErrParseDestinationLocation, fmt.Errorf("failed to parse destination location: %w", parseError))
			}

			// Get destination region base path
			destBasePath, err := internalUtilGetPairedRegionURI(destRegion)
			if err != nil {
				logger.Error("Failed to get paired destination region URI", "error", err)
				return coreerrors.NewVCPError(coreerrors.ErrGetDstBasePath, err)
			}

			logger.Debug("Destination replication details",
				"projectNumber", destProjectNumber,
				"region", destRegion,
				"location", replication.ReplicationAttributes.DestinationLocation,
				"replicationUUID", replication.ReplicationAttributes.DestinationReplicationUUID)

			// Get JWT token for destination project (same project only)
			// Note: Cross-project check is already done above when RemoteUri is available
			if volume.Account == nil {
				logger.Error("Volume account is nil, cannot get signed token")
				return customerrors.NewUserInputValidationErr("QuotaRule Operation is not allowed: volume account information is missing")
			}

			// Same project: get token for current project
			dstToken, err := internalUtilGetSignedToken(destProjectNumber)
			if err != nil {
				logger.Error("Failed to get signed token", "error", err)
				return coreerrors.NewVCPError(coreerrors.ErrGetSignedToken, err)
			}

			// Fetch destination replication details
			destinationReplication, err = getDestinationReplication(ctx, destBasePath, destProjectNumber,
				replication.ReplicationAttributes.DestinationLocation,
				replication.ReplicationAttributes.DestinationReplicationUUID, dstToken)
			if err != nil {
				logger.Error("Failed to fetch destination replication", "error", err)
				return fmt.Errorf("failed to fetch destination replication for validation: %w", err)
			}
		}

		// Common validation logic for destination replication state
		// Check lifecycle state: Cannot modify quotas during replication state transitions
		// This check must come BEFORE mirror state checks
		if isTransitionState(destinationReplication.State) {
			logger.Errorf("Destination replication is in transitioning state: %s", destinationReplication.State)
			return customerrors.NewUserInputValidationErr(
				"Quota update not allowed on destination volume when in active replication")
		}

		// Check mirror state: MIRRORED or UNINITIALIZED indicates active replication
		// Only perform this check if the replication creation request is received on destination
		// (i.e., current locationID matches the destination location)
		if replication.ReplicationAttributes.DestinationLocation == locationID && destinationReplication.MirrorState != nil {
			mirrorState := *destinationReplication.MirrorState
			if mirrorState == string(gcpgenserver.ReplicationV1betaMirrorStateMIRRORED) ||
				mirrorState == string(gcpgenserver.ReplicationV1betaMirrorStateUNINITIALIZED) ||
				mirrorState == OntapSnapmirrored ||
				mirrorState == OntapUninitialized {
				logger.Errorf("QuotaRule Operation is not allowed when destination replication is in %s state", mirrorState)
				return customerrors.NewUserInputValidationErr(
					fmt.Sprintf("QuotaRule Operation is not allowed when destination replication is in %s state", mirrorState))
			}
		}
	}
	return nil
}

// ReplaceDstQuotaRulesWithSrc removes destination quota rules and adds source quota rules within a single transaction.
func _replaceDstQuotaRulesWithSrc(ctx context.Context, se database.Storage, req *gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta, params gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams) ([]*datamodel.QuotaRule, error) {
	logger := util.GetLogger(ctx)

	// Fetch volume to obtain volume and account IDs
	volume, err := se.GetVolume(ctx, params.VolumeId)
	if err != nil {
		return nil, err
	}

	// Convert destination quota rules to UUIDs array
	dstQuotaRuleUUIDs := make([]string, 0, len(req.DstQuotaRules))
	for _, dstRule := range req.DstQuotaRules {
		if quotaId, hasQuotaId := dstRule.QuotaId.Get(); hasQuotaId {
			dstQuotaRuleUUIDs = append(dstQuotaRuleUUIDs, quotaId)
		}
	}

	// Convert source quota rules to datamodel.QuotaRule array
	// IMPORTANT: UUIDs will be cleared/generated in ReplaceDstQuotaRulesWithSrc
	// Destination quota rules should have unique UUIDs, not reuse source UUIDs
	srcQuotaRules := make([]*datamodel.QuotaRule, 0, len(req.SrcQuotaRules))
	for _, srcRule := range req.SrcQuotaRules {
		quotaRule := convertQuotaRulesV1betaToDM(srcRule)
		// Clear UUID - ReplaceDstQuotaRulesWithSrc will always generate new UUID
		quotaRule.UUID = ""
		quotaRule.VolumeID = volume.ID
		quotaRule.AccountID = volume.AccountID
		// State will be set to CREATED in ReplaceDstQuotaRulesWithSrc
		srcQuotaRules = append(srcQuotaRules, quotaRule)
	}

	createdQuotaRules, err := se.ReplaceDstQuotaRulesWithSrc(ctx, volume.ID, volume.AccountID, dstQuotaRuleUUIDs, srcQuotaRules)
	if err != nil {
		return nil, err
	}

	logger.Infof("Successfully synced %d source quota rules and deleted %d destination rules for volume %s", len(srcQuotaRules), len(dstQuotaRuleUUIDs), params.VolumeId)
	return createdQuotaRules, nil
}

// _convertQuotaRulesV1betaToDataModel converts gcpgenserver.QuotaRulesV1beta to datamodel.QuotaRule
func _convertQuotaRulesV1betaToDataModel(clientRule gcpgenserver.QuotaRulesV1beta) *datamodel.QuotaRule {
	rule := &datamodel.QuotaRule{
		Name:           clientRule.ResourceId,
		DiskLimitInKib: clientRule.DiskLimitInMib * mibToKibMultiplier, // Convert MiB to KiB
	}

	if quotaId, hasQuotaId := clientRule.QuotaId.Get(); hasQuotaId {
		rule.UUID = quotaId
	}

	switch clientRule.QuotaType {
	case gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALUSERQUOTA:
		rule.QuotaType = IndividualUserQuota
	case gcpgenserver.QuotaRulesV1betaQuotaTypeINDIVIDUALGROUPQUOTA:
		rule.QuotaType = IndividualGroupQuota
	case gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTUSERQUOTA:
		rule.QuotaType = DefaultUserQuota
	case gcpgenserver.QuotaRulesV1betaQuotaTypeDEFAULTGROUPQUOTA:
		rule.QuotaType = DefaultGroupQuota
	default:
		rule.QuotaType = string(clientRule.QuotaType)
	}

	if quotaTarget, hasQuotaTarget := clientRule.QuotaTarget.Get(); hasQuotaTarget {
		rule.QuotaTarget = quotaTarget
	}

	if state, hasState := clientRule.State.Get(); hasState {
		rule.State = string(state)
	}

	if stateDetails, hasStateDetails := clientRule.StateDetails.Get(); hasStateDetails {
		rule.StateDetails = stateDetails
	}

	if description, hasDescription := clientRule.Description.Get(); hasDescription {
		rule.Description = description
	}

	return rule
}

// CreateQuotaRule creates a new quota rule for a volume
func (o *GCPOrchestrator) CreateQuotaRule(ctx context.Context, params *common.CreateQuotaRulesParam) (*models.QuotaRule, string, error) {
	return createQuotaRule(ctx, o.storage, o.temporal, params)
}

// CreateQuotaRuleInternal creates a new quota rule for a volume via internal VCP API
// This function is similar to CreateQuotaRule but skips replication state validation
// as it's intended for VCP-to-VCP communication where replication validation is handled separately
func (o *GCPOrchestrator) CreateQuotaRuleInternal(ctx context.Context, params *common.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return createQuotaRuleInternal(ctx, o.storage, o.temporal, params)
}

// UpdateQuotaRule updates an existing quota rule for a volume
func (o *GCPOrchestrator) UpdateQuotaRule(ctx context.Context, params *common.UpdateQuotaRulesParam) (*models.QuotaRule, string, error) {
	return updateQuotaRule(ctx, o.storage, o.temporal, params)
}

// UpdateQuotaRuleInternal updates an existing quota rule for a volume via internal VCP API
// This function is similar to UpdateQuotaRule but skips replication state validation
// as it's intended for VCP-to-VCP communication where replication validation is handled separately
func (o *GCPOrchestrator) UpdateQuotaRuleInternal(ctx context.Context, params *common.UpdateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return updateQuotaRuleInternal(ctx, o.storage, o.temporal, params)
}

// DeleteQuotaRule deletes a quota rule for a volume
func (o *GCPOrchestrator) DeleteQuotaRule(ctx context.Context, params *common.DeleteQuotaRulesParam) (*models.QuotaRule, string, error) {
	return deleteQuotaRule(ctx, o.storage, o.temporal, params)
}

// DeleteQuotaRuleInternal deletes a quota rule for a volume via internal VCP API
// This function is intended for VCP-to-VCP communication where replication validation is handled separately
func (o *GCPOrchestrator) DeleteQuotaRuleInternal(ctx context.Context, params *common.DeleteQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	return deleteQuotaRuleInternal(ctx, o.storage, o.temporal, params)
}

// ListQuotaRules lists all quota rules for a volume
func (o *GCPOrchestrator) ListQuotaRules(ctx context.Context, params *common.ListQuotaRulesParams) ([]*models.QuotaRule, error) {
	return listQuotaRules(ctx, o.storage, params)
}

// GetMultipleQuotaRules retrieves multiple quota rules by UUIDs for a volume
func (o *GCPOrchestrator) GetMultipleQuotaRules(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUIDs []string) ([]*models.QuotaRule, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		if customerrors.IsNotFoundErrForObjectTypeInChain(err, "account") {
			return nil, coreerrors.NewVCPError(coreerrors.ErrVolumeOrAccountNotFoundInVCP, err)
		}
		return nil, err
	}

	volume, err := se.GetVolumeWithAccountID(ctx, volumeUuid, account.ID)
	if err != nil {
		if customerrors.IsNotFoundErrForObjectTypeInChain(err, "volume") {
			return nil, coreerrors.NewVCPError(coreerrors.ErrVolumeOrAccountNotFoundInVCP, err)
		}
		return nil, err
	}

	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("account_id", "=", account.ID),
		dbutils.NewFilterCondition("volume_id", "=", volume.ID),
		dbutils.NewFilterCondition("uuid", "in", quotaRuleUUIDs))

	dbQuotaRules, err := se.GetQuotaRulesWithCondition(ctx, *filter)
	if err != nil {
		return nil, err
	}

	modelQuotaRules := make([]*models.QuotaRule, len(dbQuotaRules))
	for i, quotaRule := range dbQuotaRules {
		modelQuotaRules[i] = convertDatastoreQuotaRuleToModel(quotaRule)
	}
	return modelQuotaRules, nil
}

// GetQuotaRulesByUUIDs returns quota rules matching the given UUIDs (batch list; across accounts).
func (o *GCPOrchestrator) GetQuotaRulesByUUIDs(ctx context.Context, quotaRuleUUIDs []string) ([]*models.QuotaRule, error) {
	if len(quotaRuleUUIDs) == 0 {
		return nil, nil
	}
	se := o.storage
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("uuid", "in", quotaRuleUUIDs))
	dbQuotaRules, err := se.GetQuotaRulesWithCondition(ctx, *filter)
	if err != nil {
		return nil, err
	}
	modelQuotaRules := make([]*models.QuotaRule, len(dbQuotaRules))
	for i, quotaRule := range dbQuotaRules {
		modelQuotaRules[i] = convertDatastoreQuotaRuleToModel(quotaRule)
	}
	return modelQuotaRules, nil
}

// DescribeQuotaRule retrieves a single quota rule by UUID for a volume
func (o *GCPOrchestrator) DescribeQuotaRule(ctx context.Context, volumeUuid string, accountName string, quotaRuleUUID string) (*models.QuotaRule, error) {
	se := o.storage

	account, err := getAccountWithName(ctx, se, accountName)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			util.GetLogger(ctx).Warnf("Account with name %s not found in VCP, checking in CVP", accountName)
			return nil, err
		}
		return nil, err
	}

	_, err = se.GetVolumeWithAccountID(ctx, volumeUuid, account.ID)
	if err != nil {
		return nil, err
	}

	dbQuotaRule, err := se.GetQuotaRuleByUUID(ctx, quotaRuleUUID, account.ID)
	if err != nil {
		return nil, err
	}

	modelQuotaRule := convertDatastoreQuotaRuleToModel(dbQuotaRule)
	return modelQuotaRule, nil
}

// ReplaceDstQuotaRulesWithSrc replaces destination quota rules with source quota rules via internal API.
// Returns the created quota rules.
func (o *GCPOrchestrator) ReplaceDstQuotaRulesWithSrc(ctx context.Context, req *commonparams.UpdateDstWithSrcQuotaRulesV1beta, params commonparams.V1betaUpdateDestinationQuotaRulesVCPParams) ([]*datamodel.QuotaRule, error) {
	gcpReq := convertCommonUpdateDstWithSrcQuotaRulesToGcp(req)
	gcpParams := convertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp(params)
	return replaceDstQuotaRulesWithSrcFn(ctx, o.storage, gcpReq, gcpParams)
}

func _createQuotaRule(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateQuotaRulesParam) (*models.QuotaRule, string, error) {
	logger := util.GetLogger(ctx)

	account, err := getOrCreateAccount(ctx, se, params.ProjectId)
	if err != nil {
		return nil, "", err
	}

	if err := validateQuotaRuleCreateParams(params); err != nil {
		logger.Errorf("Quota rule validation failed: %v", err)
		return nil, "", err
	}

	// Retrieve volume using both account ID and volume UUID to validate that the volume belongs to the account
	volumeDataModel, err := se.GetVolumeWithAccountID(ctx, params.VolumeUUID, account.ID)
	if err != nil {
		logger.Errorf("Failed to retrieve volume: %v", err)
		return nil, "", err
	}

	// Validate the volume type and size for quota rule creation (SAN, FlexCache, and size checks)
	if err := validateVolumeType(ctx, volumeDataModel, params); err != nil {
		logger.Errorf("Volume type validation failed: %v", err)
		return nil, "", err
	}

	// Validate replication state: quota rules are not allowed on destination volumes with active replication.
	// Perform this validation before fetching existing quota rules to avoid unnecessary DB calls.
	if err := validateReplicationState(ctx, se, volumeDataModel, params.LocationId); err != nil {
		logger.Errorf("replication state validation failed: %v", err)
		return nil, "", err
	}

	// Fetch existing quota rules for this volume after replication validation
	existingQuotaRulesData, err := se.GetQuotaRulesByVolumeID(ctx, volumeDataModel.ID)
	if err != nil {
		logger.Errorf("Failed to fetch existing quota rules: %v", err)
		return nil, "", err
	}
	if err := validateQuotaRuleState(ctx, volumeDataModel.UUID, existingQuotaRulesData); err != nil {
		logger.Errorf("Quota rule state validation failed: %v", err)
		return nil, "", err
	}
	// Validate Quota Rules Limit: reject only when volume already has 100 rules (allows creating the 100th when 99 exist).
	if len(existingQuotaRulesData) >= VolumeQuotaRulesDefaultLimit {
		logger.Errorf("Quota rules limit validation failed: volume has %d quota rules, limit is %d",
			len(existingQuotaRulesData), VolumeQuotaRulesDefaultLimit)
		return nil, "", customerrors.NewUserInputValidationErr(
			"Volume quota rules limit reached")
	}

	// Validate name and (type,target) uniqueness using in-memory existing rules
	if err := validateQuotaRuleUniqueness(ctx, existingQuotaRulesData, params); err != nil {
		logger.Errorf("Quota rule uniqueness validation failed: %v", err)
		return nil, "", err
	}

	// Validate quota target by protocol
	if err := validateQuotaTargetByProtocol(ctx, params, volumeDataModel.VolumeAttributes.Protocols); err != nil {
		logger.Errorf("Quota target protocol validation failed: %v", err)
		return nil, "", err
	}

	// Determine if RQuota is required for this quota rule
	// This checks if the volume uses NFS and if this is the first quota rule in the SVM
	rquotaRequired, err := determineRQuota(ctx, se, volumeDataModel, false)
	if err != nil {
		logger.Errorf("RQuota determination failed: %v", err)
		return nil, "", err
	}
	logger.Debugf("RQuota determination for volume %s: required=%t", volumeDataModel.UUID, rquotaRequired)

	// Convert disk limit from MiB (input param) to KiB (database storage and ONTAP API)
	diskLimitInKib := params.DiskLimitInMib * mibToKibMultiplier

	// Create quota rule entry in database with CREATING state
	quotaRuleDataModel := &datamodel.QuotaRule{
		Name:           params.Name,
		VolumeID:       volumeDataModel.ID,
		AccountID:      volumeDataModel.AccountID,
		QuotaType:      params.QuotaType,
		QuotaTarget:    params.QuotaTarget,
		DiskLimitInKib: diskLimitInKib,
		State:          datamodel.LifeCycleStateCreating,
		StateDetails:   datamodel.LifeCycleStateCreatingDetails,
		RQuota:         rquotaRequired,
		Description:    params.Description,
	}

	var dbQuotaRule *datamodel.QuotaRule
	var job *datamodel.Job

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
			if dbQuotaRule != nil && dbQuotaRule.UUID != "" {
				logger.Warnf("Error occurred, marking quota rule in DB as deleted. Quota Rule UUID: %s", dbQuotaRule.UUID)
				if _, delErr := se.DeleteQuotaRule(ctx, dbQuotaRule.UUID); delErr != nil {
					logger.Errorf("Failed to delete quota rule: %v", delErr)
				}
			}
		}
	}()

	dbQuotaRule, err = se.CreatingQuotaRule(ctx, quotaRuleDataModel)
	if err != nil {
		logger.Errorf("Failed to create quota rule in database. Error: %v", err)
		return nil, "", err
	}

	// Create job entry in database with resource_uuid in job_attributes
	job = &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: volumeDataModel.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbQuotaRule.UUID,
		},
	}

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, "", err
	}

	// Start Temporal workflow for quota rule creation
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.CreateQuotaRuleWorkflow,
		params,
		dbQuotaRule,
	)

	if err != nil {
		logger.Errorf("Failed to start create quota rule workflow. Error: %v ", err)
		return nil, "", err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(dbQuotaRule)
	return dataStoreQuotaRule, job.UUID, nil
}

// _createQuotaRuleInternal creates a new quota rule for a volume via internal VCP API
// This function is similar to _createQuotaRule but skips replication state validation
// as it's intended for VCP-to-VCP communication where replication validation is handled separately
func _createQuotaRuleInternal(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	if err := validateQuotaRuleCreateParams(params); err != nil {
		logger.Errorf("Quota rule validation failed: %v", err)
		return nil, nil, err
	}

	volumeDataModel, err := se.GetVolume(ctx, params.VolumeUUID)
	if err != nil {
		logger.Errorf("Failed to retrieve volume: %v", err)
		return nil, nil, err
	}

	// Note: Replication state validation is skipped for internal VCP API calls
	// Replication validation is handled separately in the calling VCP instance

	// Fetch existing quota rules for this volume
	existingQuotaRulesData, err := se.GetQuotaRulesByVolumeID(ctx, volumeDataModel.ID)
	if err != nil {
		logger.Errorf("Failed to fetch existing quota rules: %v", err)
		return nil, nil, err
	}
	if err := validateQuotaRuleState(ctx, volumeDataModel.UUID, existingQuotaRulesData); err != nil {
		logger.Errorf("Quota rule state validation failed: %v", err)
		return nil, nil, err
	}
	// Validate Quota Rules Limit: reject only when volume already has 100 rules (allows creating the 100th when 99 exist).
	if len(existingQuotaRulesData) >= VolumeQuotaRulesDefaultLimit {
		logger.Errorf("Quota rules limit validation failed: volume has %d quota rules, limit is %d",
			len(existingQuotaRulesData), VolumeQuotaRulesDefaultLimit)
		return nil, nil, customerrors.NewUserInputValidationErr(
			"Volume quota rules limit reached, please contact support to request for higher limit.")
	}

	// Validate the volume type and size for quota rule creation (SAN, FlexCache, and size checks)
	if err := validateVolumeType(ctx, volumeDataModel, params); err != nil {
		logger.Errorf("Volume type/size validation failed: %v", err)
		return nil, nil, err
	}

	// Validate name and (type,target) uniqueness using in-memory existing rules
	if err := validateQuotaRuleUniqueness(ctx, existingQuotaRulesData, params); err != nil {
		logger.Errorf("Quota rule uniqueness validation failed: %v", err)
		return nil, nil, err
	}

	// Validate quota target by protocol
	if err := validateQuotaTargetByProtocol(ctx, params, volumeDataModel.VolumeAttributes.Protocols); err != nil {
		logger.Errorf("Quota target protocol validation failed: %v", err)
		return nil, nil, err
	}

	// Determine if RQuota is required for this quota rule
	// This checks if the volume uses NFS and if this is the first quota rule in the SVM
	rquotaRequired, err := determineRQuota(ctx, se, volumeDataModel, false)
	if err != nil {
		logger.Errorf("RQuota determination failed: %v", err)
		return nil, nil, err
	}
	logger.Debugf("RQuota determination for volume %s: required=%t", volumeDataModel.UUID, rquotaRequired)

	// Note: Data Protection volume handling has been moved to workflow
	// See CreateQuotaRuleForDataProtectionVolume activity in quota_rule_create_workflow
	// DP volumes only need database entry, no ONTAP operations

	// Convert disk limit from MiB (input param) to KiB (database storage and ONTAP API)
	const mibToKibMultiplier = 1024
	diskLimitInKib := params.DiskLimitInMib * mibToKibMultiplier

	// Create quota rule entry in database with CREATING state
	quotaRuleDataModel := &datamodel.QuotaRule{
		Name:           params.Name,
		VolumeID:       volumeDataModel.ID,
		AccountID:      volumeDataModel.AccountID,
		QuotaType:      params.QuotaType,
		QuotaTarget:    params.QuotaTarget,
		DiskLimitInKib: diskLimitInKib,
		State:          datamodel.LifeCycleStateCreating,
		StateDetails:   datamodel.LifeCycleStateCreatingDetails,
		RQuota:         rquotaRequired,
		Description:    params.Description,
	}

	var dbQuotaRule *datamodel.QuotaRule
	var job *datamodel.Job

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
			}
			if dbQuotaRule != nil && dbQuotaRule.UUID != "" {
				logger.Warnf("Error occurred, marking quota rule in DB as deleted. Quota Rule UUID: %s", dbQuotaRule.UUID)
				if _, delErr := se.DeleteQuotaRule(ctx, dbQuotaRule.UUID); delErr != nil {
					logger.Errorf("Failed to delete quota rule: %v", delErr)
				}
			}
		}
	}()

	dbQuotaRule, err = se.CreatingQuotaRule(ctx, quotaRuleDataModel)
	if err != nil {
		logger.Errorf("Failed to create quota rule in database. Error: %v", err)
		return nil, nil, err
	}

	// Create job entry in database with resource_uuid in job_attributes
	job = &datamodel.Job{
		Type:          string(datamodel.JobTypeCreateQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  params.Name,
		AccountID:     sql.NullInt64{Int64: volumeDataModel.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: dbQuotaRule.UUID,
		},
	}

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create quota rule in database. Error: %v", err)
		return nil, nil, err
	}

	// Start Temporal workflow for quota rule creation
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.CreateQuotaRuleWorkflow,
		params,
		dbQuotaRule,
	)

	if err != nil {
		logger.Errorf("Failed to start create quota rule workflow. Error: %v ", err)
		return nil, nil, err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(dbQuotaRule)
	return dataStoreQuotaRule, job, nil
}

func _updateQuotaRule(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateQuotaRulesParam) (*models.QuotaRule, string, error) {
	logger := util.GetLogger(ctx)

	// Get account for validation
	account, err := getAccountWithName(ctx, se, params.ProjectId)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.ProjectId, err)
		return nil, "", err
	}

	// Get quota rule by UUID
	quotaRuleDataModel, err := se.GetQuotaRuleByUUID(ctx, params.QuotaRuleUUID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get quota rule: %s. Error: %v", params.QuotaRuleUUID, err)
		return nil, "", err
	}

	// Validate that at least one field is being updated
	if params.DiskLimitInMib == 0 && params.Description == "" {
		logger.Errorf("No fields provided for update")
		return nil, "", customerrors.NewUserInputValidationErr("At least one field (diskLimitInMib or description) must be provided for update")
	}

	// Validate quota rule is not in transitioning state
	if isTransitionState(quotaRuleDataModel.State) {
		logger.Errorf("Quota rule %s cannot be updated while in transitioning state: %s", params.QuotaRuleUUID, quotaRuleDataModel.State)
		return nil, "", customerrors.NewConflictErr("Quota rule is in transition state and cannot be updated, state: " + quotaRuleDataModel.State)
	}

	// Validate disk limit if provided (independent condition check)
	var diskLimitInKiB int64
	if params.DiskLimitInMib > 0 {
		diskLimitInKiB = params.DiskLimitInMib * mibToKibMultiplier
		if diskLimitInKiB < DiskQuotaLowerLimit || diskLimitInKiB > DiskQuotaUpperLimit {
			logger.Errorf("DiskLimit validation failed: %d KiB is outside the permissible range", diskLimitInKiB)
			return nil, "", customerrors.NewUserInputValidationErr("DiskLimit is outside the permissible range")
		}
	}

	// Get volume to access pool for provider and replication validation
	volume, err := se.GetVolumeByIDAndAccountID(ctx, quotaRuleDataModel.VolumeID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get volume: %v", err)
		return nil, "", customerrors.NewUserInputValidationErr("Failed to get volume")
	}

	// Check if any other quota rule on the same volume is in CREATING state
	// This prevents race conditions with concurrent create operations that may be initializing the quota subsystem
	existingQuotaRulesData, err := se.GetQuotaRulesByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to fetch existing quota rules for volume state validation: %v", err)
		return nil, "", err
	}
	if err := validateQuotaRuleState(ctx, volume.UUID, existingQuotaRulesData); err != nil {
		logger.Errorf("Quota rule state validation failed: %v", err)
		return nil, "", err
	}

	// Validate volume size: quota rule disk limit cannot exceed volume size (only if disk limit is being updated)
	if params.DiskLimitInMib > 0 {
		diskLimitInBytes := uint64(params.DiskLimitInMib) * mibToBytesMultiplier
		if uint64(volume.SizeInBytes) < diskLimitInBytes {
			logger.Errorf("Volume size validation failed: quota rule size (%d bytes) exceeds volume size (%d bytes)", diskLimitInBytes, volume.SizeInBytes)
			return nil, "", customerrors.NewUserInputValidationErr(
				"quota rule size can not be greater than volume size, please pass quota rule size less than volume size")
		}
	}

	// Validate replication state: quota rules are not allowed on destination volumes with active replication.
	if err := validateReplicationState(ctx, se, volume, params.LocationId); err != nil {
		logger.Errorf("replication state validation failed: %v", err)
		return nil, "", err
	}

	// Create job entry in database
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  quotaRuleDataModel.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         quotaRuleDataModel.UUID,
			PreviousState:        quotaRuleDataModel.State,
			PreviousStateDetails: quotaRuleDataModel.StateDetails,
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
				// Mark quota rule as available after cleanup
				if quotaRuleDataModel != nil {
					quotaRuleDataModel.State = datamodel.LifeCycleStateAvailable
					quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateReadyDetails
					if _, updateErr := se.UpdateQuotaRule(ctx, quotaRuleDataModel); updateErr != nil {
						logger.Errorf("Failed to mark quota rule as available after error: %v", updateErr)
					}
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, "", err
	}

	quotaRuleDataModel.State = datamodel.LifeCycleStateUpdating
	quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	// Mark quota rule as UPDATING state in database
	updatedQuotaRule, err := se.UpdatingQuotaRule(ctx, quotaRuleDataModel)
	if err != nil {
		logger.Errorf("Failed to mark quota rule as updating: %v", err)
		return nil, "", err
	}

	// Start Temporal workflow for quota rule update
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.UpdateQuotaRuleWorkflow,
		params,
		updatedQuotaRule,
	)

	if err != nil {
		logger.Errorf("Failed to start update quota rule workflow. Error: %v ", err)
		return nil, "", err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(updatedQuotaRule)
	return dataStoreQuotaRule, job.UUID, nil
}

// _updateQuotaRuleInternal updates an existing quota rule for a volume via internal VCP API
// This function is similar to _updateQuotaRule but skips replication state validation
// as it's intended for VCP-to-VCP communication where replication validation is handled separately
func _updateQuotaRuleInternal(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	// Get account for validation
	account, err := getAccountWithName(ctx, se, params.ProjectId)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.ProjectId, err)
		return nil, nil, err
	}

	// Get quota rule by UUID
	quotaRuleDataModel, err := se.GetQuotaRuleByUUID(ctx, params.QuotaRuleUUID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get quota rule: %s. Error: %v", params.QuotaRuleUUID, err)
		return nil, nil, err
	}

	// Validate quota rule is not in transitioning state
	if isTransitionState(quotaRuleDataModel.State) {
		logger.Errorf("Quota rule %s cannot be updated while in transitioning state: %s", params.QuotaRuleUUID, quotaRuleDataModel.State)
		return nil, nil, customerrors.NewConflictErr("Quota rule is in transition state and cannot be updated, state: " + quotaRuleDataModel.State)
	}

	// Validate that at least one field is being updated
	if params.DiskLimitInMib == 0 && params.Description == "" {
		logger.Errorf("No fields provided for update")
		return nil, nil, customerrors.NewUserInputValidationErr("At least one field (diskLimitInMib or description) must be provided for update")
	}

	// Validate disk limit if provided (independent condition check)
	var diskLimitInKiB int64
	if params.DiskLimitInMib > 0 {
		diskLimitInKiB = params.DiskLimitInMib * mibToKibMultiplier
		if diskLimitInKiB < DiskQuotaLowerLimit || diskLimitInKiB > DiskQuotaUpperLimit {
			logger.Errorf("DiskLimit validation failed: %d KiB is outside the permissible range", diskLimitInKiB)
			return nil, nil, customerrors.NewUserInputValidationErr("DiskLimit is outside the permissible range")
		}
	}

	// Get volume to access pool for provider and volume size validation
	volume, err := se.GetVolumeByIDAndAccountID(ctx, quotaRuleDataModel.VolumeID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get volume: %v", err)
		return nil, nil, err
	}

	// Validate volume size: quota rule disk limit cannot exceed volume size (only if disk limit is being updated)
	if params.DiskLimitInMib > 0 {
		diskLimitInBytes := uint64(params.DiskLimitInMib) * mibToBytesMultiplier
		if uint64(volume.SizeInBytes) < diskLimitInBytes {
			logger.Errorf("Volume size validation failed: quota rule size (%d bytes) exceeds volume size (%d bytes)", diskLimitInBytes, volume.SizeInBytes)
			return nil, nil, customerrors.NewUserInputValidationErr(
				"quota rule size can not be greater than volume size, please pass quota rule size less than volume size")
		}
	}

	// Note: Replication state validation is skipped for internal VCP API calls
	// Replication validation is handled separately in the calling VCP instance

	// Create job entry in database
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeUpdateQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  quotaRuleDataModel.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         quotaRuleDataModel.UUID,
			PreviousState:        quotaRuleDataModel.State,
			PreviousStateDetails: quotaRuleDataModel.StateDetails,
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
				// Mark quota rule as available after cleanup
				if quotaRuleDataModel != nil {
					quotaRuleDataModel.State = datamodel.LifeCycleStateAvailable
					quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateReadyDetails
					if _, updateErr := se.UpdateQuotaRule(ctx, quotaRuleDataModel); updateErr != nil {
						logger.Errorf("Failed to mark quota rule as available after error: %v", updateErr)
					}
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, nil, err
	}

	// Update state to UPDATING
	quotaRuleDataModel.State = datamodel.LifeCycleStateUpdating
	quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateUpdatingDetails

	// Mark quota rule as UPDATING state in database
	updatedQuotaRule, err := se.UpdatingQuotaRule(ctx, quotaRuleDataModel)
	if err != nil {
		logger.Errorf("Failed to mark quota rule as updating: %v", err)
		return nil, nil, err
	}

	// Start Temporal workflow for quota rule update
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.UpdateQuotaRuleWorkflow,
		params,
		updatedQuotaRule,
	)

	if err != nil {
		logger.Errorf("Failed to start update quota rule workflow. Error: %v ", err)
		return nil, nil, err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(updatedQuotaRule)
	return dataStoreQuotaRule, job, nil
}

func _deleteQuotaRule(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteQuotaRulesParam) (*models.QuotaRule, string, error) {
	logger := util.GetLogger(ctx)

	// Get account for validation
	account, err := getAccountWithName(ctx, se, params.ProjectId)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.ProjectId, err)
		return nil, "", err
	}

	// Get quota rule by UUID
	quotaRuleDataModel, err := se.GetQuotaRuleByUUID(ctx, params.QuotaRuleUUID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get quota rule: %s. Error: %v", params.QuotaRuleUUID, err)
		return nil, "", err
	}

	var isCleanupDelete bool
	var existingDeleteJobUUID string

	if quotaRuleDataModel.State == datamodel.LifeCycleStateCreating {
		existingDeleteJobUUID, isCleanupDelete, err = database.ValidateCorrelationIDForCreatingResource(
			ctx, se, quotaRuleDataModel.UUID, "quota rule", datamodel.JobTypeCreateQuotaRule, datamodel.JobTypeDeleteQuotaRule, logger)
		if err != nil {
			logger.Warnf("Quota rule %s cannot be deleted: existing create job not present and state is in CREATING", quotaRuleDataModel.UUID)
			return nil, "", err
		}
		if existingDeleteJobUUID != "" {
			dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(quotaRuleDataModel)
			return dataStoreQuotaRule, existingDeleteJobUUID, nil
		}
	} else if utils.IsTransitionalState(quotaRuleDataModel.State) && quotaRuleDataModel.State != datamodel.LifeCycleStateDeleting {
		logger.Errorf("Quota rule %s cannot be deleted, while in transitioning state: %s", quotaRuleDataModel.Name, quotaRuleDataModel.State)
		return nil, "", customerrors.NewConflictErr(fmt.Sprintf("quota rule is in transition state and cannot be deleted, state: %s", quotaRuleDataModel.State))
	}

	existingJobUUID := database.GetExistingDeleteJobForDeletingState(ctx, se, quotaRuleDataModel.UUID, datamodel.JobTypeDeleteQuotaRule, logger)
	if existingJobUUID != "" {
		dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(quotaRuleDataModel)
		return dataStoreQuotaRule, existingJobUUID, nil
	}

	// Get volume to access pool for provider and replication validation
	volume, err := se.GetVolumeByIDAndAccountID(ctx, quotaRuleDataModel.VolumeID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get volume: %v", err)
		return nil, "", customerrors.NewUserInputValidationErr("Failed to get volume")
	}

	// Check if any other quota rule on the same volume is in CREATING state
	// This prevents race conditions with concurrent create operations that may be initializing the quota subsystem
	existingQuotaRulesData, err := se.GetQuotaRulesByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to fetch existing quota rules for volume state validation: %v", err)
		return nil, "", err
	}
	if err := validateQuotaRuleState(ctx, volume.UUID, existingQuotaRulesData); err != nil {
		logger.Errorf("Quota rule state validation failed: %v", err)
		return nil, "", err
	}

	// Validate replication state: quota rules are not allowed on destination volumes with active replication.
	if err := validateReplicationState(ctx, se, volume, params.LocationId); err != nil {
		logger.Errorf("replication state validation failed: %v", err)
		return nil, "", err
	}

	rquotaRequired, err := determineRQuota(ctx, se, volume, true)
	if err != nil {
		logger.Errorf("RQuota determination failed: %v", err)
		return nil, "", err
	}
	logger.Debugf("RQuota determination for volume %s: required=%t", volume.UUID, rquotaRequired)

	// Save previous state for error recovery
	previousState := quotaRuleDataModel.State
	previousStateDetails := quotaRuleDataModel.StateDetails
	previousRquota := quotaRuleDataModel.RQuota

	// Create job entry in database
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  quotaRuleDataModel.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         quotaRuleDataModel.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
				// Mark quota rule back to previous state after error (only if job was created)
				if quotaRuleDataModel != nil {
					quotaRuleDataModel.State = previousState
					quotaRuleDataModel.StateDetails = previousStateDetails
					quotaRuleDataModel.RQuota = previousRquota
					if _, updateErr := se.UpdateQuotaRule(ctx, quotaRuleDataModel); updateErr != nil {
						logger.Errorf("Failed to mark quota rule back to previous state after error: %v", updateErr)
					}
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, "", err
	}

	// Only update state to DELETING if not cleanup-delete (quota rule is not in CREATING state)
	if !isCleanupDelete {
		// Update state to DELETING
		quotaRuleDataModel.State = datamodel.LifeCycleStateDeleting
		quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateDeletingDetails
		quotaRuleDataModel.RQuota = rquotaRequired
		// Mark quota rule as DELETING state in database
		updatedQuotaRule, err := se.UpdatingQuotaRule(ctx, quotaRuleDataModel)
		if err != nil {
			logger.Errorf("Failed to mark quota rule as deleting: %v", err)
			return nil, "", err
		}
		quotaRuleDataModel = updatedQuotaRule
	}

	// Start Temporal workflow for quota rule delete
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.DeleteQuotaRuleWorkflow,
		params,
		quotaRuleDataModel,
	)

	if err != nil {
		logger.Errorf("Failed to start delete quota rule workflow. Error: %v ", err)
		return nil, "", err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(quotaRuleDataModel)
	return dataStoreQuotaRule, job.UUID, nil
}

func _deleteQuotaRuleInternal(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteQuotaRulesParam) (*models.QuotaRule, *datamodel.Job, error) {
	logger := util.GetLogger(ctx)

	// Get account for validation
	account, err := getAccountWithName(ctx, se, params.ProjectId)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.ProjectId, err)
		return nil, nil, err
	}

	// Get quota rule by UUID
	quotaRuleDataModel, err := se.GetQuotaRuleByUUID(ctx, params.QuotaRuleUUID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get quota rule: %s. Error: %v", params.QuotaRuleUUID, err)
		return nil, nil, err
	}

	if isTransitionState(quotaRuleDataModel.State) && quotaRuleDataModel.State != datamodel.LifeCycleStateDeleting {
		logger.Errorf("Quota rule %s cannot be deleted while in transitioning state: %s", params.QuotaRuleUUID, quotaRuleDataModel.State)
		return nil, nil, customerrors.NewConflictErr("Quota rule is in transition state and cannot be deleted, state: " + quotaRuleDataModel.State)
	}

	// Get volume to access account ID for job creation
	volume, err := se.GetVolumeByIDAndAccountID(ctx, quotaRuleDataModel.VolumeID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get volume: %v", err)
		return nil, nil, err
	}

	// Save previous state for error recovery
	previousState := quotaRuleDataModel.State
	previousStateDetails := quotaRuleDataModel.StateDetails

	// Create job entry in database
	job := &datamodel.Job{
		Type:          string(datamodel.JobTypeDeleteQuotaRule),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  quotaRuleDataModel.Name,
		AccountID:     sql.NullInt64{Int64: volume.AccountID, Valid: true},
		CorrelationID: utils.GetCoRelationIDFromContext(ctx),
		RequestID:     utils.GetRequestIDFromContext(ctx),
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID:         quotaRuleDataModel.UUID,
			PreviousState:        previousState,
			PreviousStateDetails: previousStateDetails,
		},
	}

	// Cleanup in case of error
	defer func() {
		if err != nil {
			if job != nil && job.UUID != "" {
				logger.Warnf("Error occurred, marking job entry in DB as deleted. Job UUID: %s", job.UUID)
				if delErr := se.DeleteJob(ctx, job.UUID, err.Error()); delErr != nil {
					logger.Errorf("Failed to delete job: %v", delErr)
				}
				// Mark quota rule back to previous state after error
				if quotaRuleDataModel != nil {
					quotaRuleDataModel.State = previousState
					quotaRuleDataModel.StateDetails = previousStateDetails
					if _, updateErr := se.UpdateQuotaRule(ctx, quotaRuleDataModel); updateErr != nil {
						logger.Errorf("Failed to mark quota rule back to previous state after error: %v", updateErr)
					}
				}
			}
		}
	}()

	job, err = se.CreateJob(ctx, job)
	if err != nil {
		logger.Errorf("Failed to create job in database. Error: %v", err)
		return nil, nil, err
	}

	// Update state to DELETING
	quotaRuleDataModel.State = datamodel.LifeCycleStateDeleting
	quotaRuleDataModel.StateDetails = datamodel.LifeCycleStateDeletingDetails

	// Mark quota rule as DELETING state in database
	updatedQuotaRule, err := se.UpdatingQuotaRule(ctx, quotaRuleDataModel)
	if err != nil {
		logger.Errorf("Failed to mark quota rule as deleting: %v", err)
		return nil, nil, err
	}

	// Start Temporal workflow for quota rule delete
	// Note: DeleteQuotaRuleWorkflow will need to be implemented separately
	_, err = temporal.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			ID:                    job.WorkflowID,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
		workflows.DeleteQuotaRuleWorkflow,
		params,
		updatedQuotaRule,
	)

	if err != nil {
		logger.Errorf("Failed to start delete quota rule workflow. Error: %v ", err)
		return nil, nil, err
	}

	dataStoreQuotaRule := convertDatastoreQuotaRuleToModel(updatedQuotaRule)
	return dataStoreQuotaRule, job, nil
}

// _listQuotaRules lists all quota rules for a volume
func _listQuotaRules(ctx context.Context, se database.Storage, params *common.ListQuotaRulesParams) ([]*models.QuotaRule, error) {
	logger := util.GetLogger(ctx)

	// Get account to validate volume belongs to the account
	account, err := getAccountWithName(ctx, se, params.AccountName)
	if err != nil {
		logger.Errorf("Failed to get account: %s. Error: %v", params.AccountName, err)
		return nil, err
	}

	// Retrieve volume using both account ID and volume UUID to validate that the volume belongs to the account
	volume, err := se.GetVolumeWithAccountID(ctx, params.VolumeID, account.ID)
	if err != nil {
		logger.Errorf("Failed to get volume: %s. Error: %v", params.VolumeID, err)
		return nil, err
	}

	// Fetch quota rules from database
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to get quota rules for volume: %s. Error: %v", params.VolumeID, err)
		return nil, err
	}

	// Convert datamodel quota rules to models
	var quotaRulesToReturn []*models.QuotaRule
	for _, quotaRule := range quotaRules {
		quotaRulesToReturn = append(quotaRulesToReturn, convertDatastoreQuotaRuleToModel(quotaRule))
	}
	return quotaRulesToReturn, nil
}

// _convertDatastoreQuotaRuleToModel converts a datamodel.QuotaRule to models.QuotaRule
func _convertDatastoreQuotaRuleToModel(quotaRule *datamodel.QuotaRule) *models.QuotaRule {
	if quotaRule == nil {
		return nil
	}

	// Convert disk limit from KiB (database storage) back to MiB (API response)
	diskLimitInMib := quotaRule.DiskLimitInKib / kibToMibDivisor

	result := &models.QuotaRule{
		BaseModel: models.BaseModel{
			UUID:      quotaRule.UUID,
			CreatedAt: quotaRule.CreatedAt,
			UpdatedAt: quotaRule.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(quotaRule.DeletedAt),
		},
		Name:                  quotaRule.Name,
		Description:           quotaRule.Description,
		LifeCycleState:        quotaRule.State,
		LifeCycleStateDetails: quotaRule.StateDetails,
		QuotaType:             quotaRule.QuotaType,
		QuotaTarget:           quotaRule.QuotaTarget,
		DiskLimitInMib:        diskLimitInMib,
	}
	return result
}

// validateVolumeType performs checks related to volume protocol/type and size.
// It validates that quota rules are not created for SAN (block) volumes,
// not created for FlexCache volumes, and that the requested quota size does not
// exceed the volume size.
func validateVolumeType(ctx context.Context, volumeDataModel *datamodel.Volume, params *common.CreateQuotaRulesParam) error {
	logger := util.GetLogger(ctx)

	// Block volume check: Quota rules are not allowed on SAN/block volumes
	if utils.IsSanProtocols(volumeDataModel.VolumeAttributes.Protocols) {
		logger.Errorf("Block volume check failed: quota rule creation is not supported for block volumes")
		return customerrors.NewUserInputValidationErr("quota rule creation is not supported for block (SAN) volumes")
	}

	// FlexCache Volume Check: FlexCache volumes don't support quotas
	if volumeDataModel.CacheParameters != nil {
		logger.Errorf("FlexCache volume check failed: quota rule creation is not supported for flexcache volumes")
		return customerrors.NewUserInputValidationErr("quota rule creation is not supported for flexcache volume")
	}
	// Validate volume size: quota rule size cannot exceed volume size
	diskLimitInBytes := uint64(params.DiskLimitInMib) * mibToBytesMultiplier
	if uint64(volumeDataModel.SizeInBytes) < diskLimitInBytes {
		logger.Errorf("Volume size validation failed: quota rule size (%d bytes) exceeds volume size (%d bytes)", diskLimitInBytes, volumeDataModel.SizeInBytes)
		return customerrors.NewUserInputValidationErr(
			"quota rule size can not be greater than volume size, please pass quota rule size less than volume size")
	}

	return nil
}

// validateQuotaRuleState rejects starting a new quota rule creation when another quota rule
// on the same volume is still in CREATING, to avoid parallel ONTAP operations.
func validateQuotaRuleState(ctx context.Context, volumeUUID string, existingRules []*datamodel.QuotaRule) error {
	logger := util.GetLogger(ctx)
	for _, r := range existingRules {
		if r.State == datamodel.LifeCycleStateCreating {
			logger.Errorf("Quota rule operation blocked: volume %s already has a quota rule in state %s (uuid=%s)",
				volumeUUID, r.State, r.UUID)
			return customerrors.NewUserInputValidationErr(
				"Another quota rule is being created for this volume. Please wait until it completes before creating another.")
		}
	}
	return nil
}

// validateQuotaRuleUniqueness checks uniqueness of quota rule name and the (type,target)
// combination against the existing quota rules fetched earlier. This avoids extra DB calls
// since existingQuotaRulesData is already available in the caller.
func validateQuotaRuleUniqueness(ctx context.Context, existingRules []*datamodel.QuotaRule, params *common.CreateQuotaRulesParam) error {
	logger := util.GetLogger(ctx)

	for _, ex := range existingRules {
		if ex.Name == params.Name {
			logger.Errorf("Name uniqueness validation failed: quota rule with name %s already exists for this volume", params.Name)
			return customerrors.NewConflictErr("quota rule with same name " + params.Name + " already exist for this volume")
		}
		if ex.QuotaType == params.QuotaType && ex.QuotaTarget == params.QuotaTarget {
			logger.Errorf("Type+Target uniqueness validation failed: quota rule with type %s and target %s already exists (name: %s)", params.QuotaType, params.QuotaTarget, ex.Name)
			return customerrors.NewConflictErr(
				"quota rule with same type " + params.QuotaType + " and target " + params.QuotaTarget +
					" already exist for this volume for volumeQuotaRuleName " + ex.Name)
		}
	}

	return nil
}

// hasProtocol checks if a protocol exists in the given protocol types array
func hasProtocol(protocol string, protocolTypes []string) bool {
	for _, protocolType := range protocolTypes {
		if protocolType == protocol {
			return true
		}
	}
	return false
}

// hasCIFS checks if CIFS (SMB) protocol exists in the given protocol types array
func hasCIFS(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolSMB, protocolTypes)
}

// hasNFSv3 checks if NFSv3 protocol exists in the given protocol types array
func hasNFSv3(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolNFSv3, protocolTypes)
}

// hasNFSv4 checks if NFSv4 protocol exists in the given protocol types array
func hasNFSv4(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolNFSv4, protocolTypes)
}

// hasDualProtocolForUserMapping checks if both SMB and NFS protocols exist (dual protocol)
func hasDualProtocolForUserMapping(protocolTypes []string) bool {
	return hasCIFS(protocolTypes) && (hasNFSv3(protocolTypes) || hasNFSv4(protocolTypes))
}

// validateQuotaTargetByProtocol performs protocol-specific quotaTarget validations.
// It checks SMB, NFS (v3/v4) and Dual Protocol rules and returns a user-validation
// error when the quotaTarget does not conform to the expected format for the
// volume's protocols.
func validateQuotaTargetByProtocol(ctx context.Context, params *common.CreateQuotaRulesParam, protocolTypes []string) error {
	logger := util.GetLogger(ctx)

	protocolTypesLength := len(protocolTypes)

	if hasCIFS(protocolTypes) {
		if params.QuotaType == IndividualGroupQuota || params.QuotaType == DefaultGroupQuota {
			logger.Errorf("Protocol type validation failed: group quota not allowed for SMB volume")
			return customerrors.NewUserInputValidationErr("Group Quota cannot be specified for a SMB and Dual Protocol volume. To create this quota rule the quotaType to DefaultUserQuota/IndividualUserQuota type")
		}

		if protocolTypesLength == 1 {
			re := regexp.MustCompile(`^S-1-[0-59]-\d{2}-\d{8,10}-\d{8,10}-\d{8,10}-[1-9]\d{3}`)
			if params.QuotaTarget != "" && !re.MatchString(params.QuotaTarget) {
				logger.Errorf("SMB quota target validation failed: invalid SID format: %s", params.QuotaTarget)
				return customerrors.NewUserInputValidationErr("quotaTarget is invalid. Please pass valid SID in quotaTarget for SMB volume")
			}
		}
	}

	if (hasNFSv3(protocolTypes) || hasNFSv4(protocolTypes)) && protocolTypesLength == 1 {
		re := regexp.MustCompile(`^[0-9]*$`)
		if !re.MatchString(params.QuotaTarget) {
			logger.Errorf("NFS quota target validation failed: non-numeric value: %s", params.QuotaTarget)
			return customerrors.NewUserInputValidationErr("quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
		} else if params.QuotaTarget != "" {
			numericQuotaTarget, err := strconv.Atoi(params.QuotaTarget)
			if err != nil {
				logger.Errorf("NFS quota target validation failed: conversion error: %s", params.QuotaTarget)
				return customerrors.NewUserInputValidationErr("quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
			}

			if params.QuotaTarget != "" && re.MatchString(params.QuotaTarget) && (numericQuotaTarget < 0 || numericQuotaTarget > 4294967295) {
				logger.Errorf("NFS quota target validation failed: out of range: %s", params.QuotaTarget)
				return customerrors.NewUserInputValidationErr("quotaTarget is invalid. Please pass numeric value for quotaTarget in range [0, 4294967295] for NFS volumes")
			}
		}
	}

	if hasDualProtocolForUserMapping(protocolTypes) {
		re := regexp.MustCompile(`^S-1-[0-59]-\d{2}-\d{8,10}-\d{8,10}-\d{8,10}-[1-9]\d{3}`)
		re1 := regexp.MustCompile(`^[0-9]*$`)
		if !(re.MatchString(params.QuotaTarget) || re1.MatchString(params.QuotaTarget)) {
			logger.Errorf("Dual protocol quota target validation failed: invalid format: %s", params.QuotaTarget)
			return customerrors.NewUserInputValidationErr("quotaTarget is invalid. Please pass numeric value in range [0, 4294967295] or SID for quotaTarget for dual protocol volumes")
		}
	}

	return nil
}

// determineRQuota determines if recursive quota (RQuota) should be enabled for this quota rule.
//   - ctx: Context for logging
//   - se: Database storage interface
//   - volumeDetails: Volume to check
//   - isDeleteAction: true if this is for a delete operation, false for create/update
//
// Returns:
//   - bool: true if RQuota is required, false otherwise
//   - error: Error if database operations fail
func determineRQuota(ctx context.Context, se database.Storage, volumeDetails *datamodel.Volume, isDeleteAction bool) (bool, error) {
	logger := util.GetLogger(ctx)

	// Get protocols from volume attributes
	var protocols []string
	if volumeDetails.VolumeAttributes != nil && volumeDetails.VolumeAttributes.Protocols != nil {
		protocols = volumeDetails.VolumeAttributes.Protocols
	}

	// Check if volume uses NFS protocol using helper functions
	if !(hasNFSv3(protocols) || hasNFSv4(protocols)) {
		if isDeleteAction {
			return true, nil
		}
		logger.Infof("Volume %s does not use NFS protocol, RQuota not required", volumeDetails.UUID)
		return false, nil
	}

	// Get quota count under the SVM using SvmID
	quotaCountUnderSVM, err := se.GetQuotaRuleCountBySvmID(ctx, volumeDetails.SvmID)
	if err != nil {
		logger.Errorf("Failed to get quota rule count for SVM ID %d: %v", volumeDetails.SvmID, err)
		return false, err
	}

	if isDeleteAction {
		// For delete: the delete workflow uses !isRQuotaEnabled to decide whether to disable rquota
		// on the SVM. We want to disable rquota only when this IS the last NFS quota rule (count==1).
		// Returning !(count==1) makes the value false when last (triggering !false=true in workflow)
		// and true when not last (triggering !true=false → no action), matching the workflow logic.
		rquotaRequired := !(quotaCountUnderSVM == 1)
		logger.Infof("RQuota determination for delete on volume %s (SvmID=%d): quotaCountUnderSVM=%d, rquotaRequired=%t",
			volumeDetails.UUID, volumeDetails.SvmID, quotaCountUnderSVM, rquotaRequired)
		return rquotaRequired, nil
	}

	// For create/update: RQuota should be enabled only if this is the very first quota rule in the entire SVM
	rquotaRequired := quotaCountUnderSVM == 0
	logger.Infof("RQuota determination for volume %s (SvmID=%d): quotaCountUnderSVM=%d, rquotaRequired=%t",
		volumeDetails.UUID, volumeDetails.SvmID, quotaCountUnderSVM, rquotaRequired)

	return rquotaRequired, nil
}

// convertCommonUpdateDstWithSrcQuotaRulesToGcp converts commonparams.UpdateDstWithSrcQuotaRulesV1beta to gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta
func convertCommonUpdateDstWithSrcQuotaRulesToGcp(req *commonparams.UpdateDstWithSrcQuotaRulesV1beta) *gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta {
	gcpReq := &gcpgenserver.UpdateDstWithSrcQuotaRulesV1beta{
		SrcQuotaRules: make([]gcpgenserver.QuotaRulesV1beta, 0, len(req.SrcQuotaRules)),
		DstQuotaRules: make([]gcpgenserver.QuotaRulesV1beta, 0, len(req.DstQuotaRules)),
	}

	for _, rule := range req.SrcQuotaRules {
		gcpRule := convertCommonQuotaRulesV1betaToGcp(rule)
		gcpReq.SrcQuotaRules = append(gcpReq.SrcQuotaRules, gcpRule)
	}

	for _, rule := range req.DstQuotaRules {
		gcpRule := convertCommonQuotaRulesV1betaToGcp(rule)
		gcpReq.DstQuotaRules = append(gcpReq.DstQuotaRules, gcpRule)
	}

	return gcpReq
}

// convertCommonQuotaRulesV1betaToGcp converts commonparams.QuotaRulesV1beta to gcpgenserver.QuotaRulesV1beta
func convertCommonQuotaRulesV1betaToGcp(rule commonparams.QuotaRulesV1beta) gcpgenserver.QuotaRulesV1beta {
	gcpRule := gcpgenserver.QuotaRulesV1beta{
		ResourceId:     rule.ResourceId,
		QuotaType:      gcpgenserver.QuotaRulesV1betaQuotaType(rule.QuotaType),
		DiskLimitInMib: rule.DiskLimitInMib,
	}

	if rule.QuotaId != nil {
		gcpRule.QuotaId = gcpgenserver.NewOptString(*rule.QuotaId)
	}
	if rule.QuotaTarget != nil {
		gcpRule.QuotaTarget = gcpgenserver.NewOptString(*rule.QuotaTarget)
	}
	if rule.State != nil {
		gcpRule.State = gcpgenserver.NewOptQuotaRulesV1betaState(gcpgenserver.QuotaRulesV1betaState(*rule.State))
	}
	if rule.StateDetails != nil {
		gcpRule.StateDetails = gcpgenserver.NewOptString(*rule.StateDetails)
	}
	if rule.Description != nil {
		gcpRule.Description = gcpgenserver.NewOptString(*rule.Description)
	}
	if rule.CreatedAt != nil {
		gcpRule.CreatedAt = gcpgenserver.NewOptDateTime(*rule.CreatedAt)
	}
	if rule.UpdatedAt != nil {
		gcpRule.UpdatedAt = gcpgenserver.NewOptDateTime(*rule.UpdatedAt)
	}

	return gcpRule
}

// convertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp converts commonparams.V1betaUpdateDestinationQuotaRulesVCPParams to gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams
func convertCommonV1betaUpdateDestinationQuotaRulesVCPParamsToGcp(params commonparams.V1betaUpdateDestinationQuotaRulesVCPParams) gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams {
	gcpParams := gcpgenserver.V1betaUpdateDestinationQuotaRulesVCPParams{
		ProjectNumber: params.ProjectNumber,
		LocationId:    params.LocationId,
		VolumeId:      params.VolumeId,
	}

	if params.XCorrelationID != nil {
		gcpParams.XCorrelationID = gcpgenserver.NewOptString(*params.XCorrelationID)
	}

	return gcpParams
}

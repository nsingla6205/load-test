package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// QuotaRuleCommonActivity contains activities shared between create and update workflows
type QuotaRuleCommonActivity struct {
	SE database.Storage
}

// QuotaRuleCreateActivity contains create-specific activities
type QuotaRuleCreateActivity struct {
	SE database.Storage
}

// QuotaRuleUpdateActivity contains update-specific activities
type QuotaRuleUpdateActivity struct {
	SE database.Storage
}

type QuotaRuleDeleteActivity struct {
	SE database.Storage
}

// getBasePathForQuotaRule gets the base path for a given location/region.
// This is a local helper to avoid import cycle with replicationActivities.
func getBasePathForQuotaRule(ctx context.Context, location string) (*string, error) {
	logger := util.GetLogger(ctx)
	logger.Debugf("getBasePathForQuotaRule")

	region, _, parseError := utils.ParseRegionAndZone(location)
	if parseError != nil {
		logger.Error("Parse Location Error")
		return nil, parseError
	}

	basePath, err := replication.InternalUtilGetPairedRegionURI(region)
	if err != nil {
		return nil, err
	}
	return &basePath, nil
}

const (
	// Quota type identifiers used in protocol validation
	QuotaIndividualGroup  = "INDIVIDUAL_GROUP_QUOTA"
	QuotaDefaultGroup     = "DEFAULT_GROUP_QUOTA"
	QuotaRuleEnable       = "Enable"
	QuotaRuleDisable      = "Disable"
	QuotaRuleActionDelete = "delete"
)

// GetVolumeByID fetches authoritative volume details from the database by numeric ID.
func (a *QuotaRuleCommonActivity) GetVolumeByID(ctx context.Context, volumeID int64, accountID int64) (*datamodel.Volume, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	volume, err := se.GetVolumeByIDAndAccountID(ctx, volumeID, accountID)
	if err != nil {
		logger.Errorf("GetVolumeByID failed for ID %d: %v", volumeID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return volume, nil
}

// ListQuotaRuleForVolume lists all quota rules for a volume (excluding deleted ones).
// This is used in break replication to get quota rules that need to be recreated and in delete workflows.
// It accepts a replication object and uses the volume from replication directly to avoid additional DB calls.
func (a *QuotaRuleCommonActivity) ListQuotaRuleForVolume(
	ctx context.Context,
	replication *datamodel.VolumeReplication,
) ([]*datamodel.QuotaRule, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Validate replication and volume are present
	if replication == nil || replication.Volume == nil {
		logger.Errorf("Replication or volume is nil")
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Replication and volume must be provided"))
	}

	volume := replication.Volume
	volumeUUID := volume.UUID

	// Get quota rules for the volume (excluding deleted) using volume ID from replication
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to list quota rules for volume %s: %v", volumeUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Found %d quota rules for volume %s", len(quotaRules), volumeUUID)
	return quotaRules, nil
}

// CreateQuotaRuleForDataProtectionVolume handles quota rule creation for DP (Data Protection) volumes.
// DP volumes are SnapMirror destinations where quotas are managed by the source volume.
// This activity only updates the database to set the quota rule state to AVAILABLE without any ONTAP operations.
func (a *QuotaRuleCreateActivity) CreateQuotaRuleForDataProtectionVolume(ctx context.Context, quotaRule *datamodel.QuotaRule) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Update the quota rule in database
	_, err := se.UpdateQuotaRule(ctx, quotaRule)
	if err != nil {
		logger.Errorf("Failed to create quota rule for DP volume: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// GetVolumeReplication fetches replication details for a volume.
// Returns the list of replications associated with the volume.
func (a *QuotaRuleCommonActivity) GetVolumeReplication(ctx context.Context, volumeID int64) ([]*datamodel.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Fetch replications for this volume using ListVolumeReplications with volume_id filter
	filter := dbutils.CreateFilterWithConditions(
		dbutils.NewFilterCondition("volume_id", "=", volumeID))

	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		logger.Errorf("Failed to list volume replications for volume_id %d: %v", volumeID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return replications, nil
}

// GetSignedDstTokenForQuotaRule retrieves and stores JWT token for destination project.
// This token will be reused across all destination API calls to avoid multiple token creations.
// Following the replication workflow pattern for consistency.
func (a *QuotaRuleCommonActivity) GetSignedDstTokenForQuotaRule(
	ctx context.Context,
	projectNumber string,
) (*string, error) {
	logger := util.GetLogger(ctx)
	jwtToken, err := auth.GetSignedJwtToken(projectNumber)
	if err != nil {
		logger.Errorf("Failed to get JWT token for destination project %s: %v", projectNumber, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Successfully obtained JWT token for destination project %s", projectNumber)
	return &jwtToken, nil
}

// GetOntapQuotaUUID retrieves the UUID of an existing quota rule from ONTAP.
// action: "delete" to allow empty quotaUUID (not found) as expected behavior, other values will return error if not found
func (a *QuotaRuleCommonActivity) GetOntapQuotaUUID(
	ctx context.Context,
	volumeDetails *datamodel.Volume,
	node *models.Node,
	quotaType string,
	target string,
	action string,
) (string, error) {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Get the volume UUID from VolumeAttributes
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return "", vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	// Get SVM name - check if it's already loaded
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details loaded", volumeDetails.UUID)
		return "", vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no SVM details"))
	}

	// Call provider to get quota UUID and type
	quotaUUID, _, err := provider.GetOntapQuotaUUIDAndType(ctx, volumeUUID, svmName, quotaType, target)
	if err != nil {
		logger.Errorf("Failed to get quota UUID and type: %v", err)
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if quotaUUID == "" {
		// For delete action, quota not found is expected (already deleted) - return empty string with nil error
		// For other actions (update, etc.), quota not found is an error
		if action == QuotaRuleActionDelete {
			logger.Infof("No quota UUID found for volume %s, quotaType %s, target %s - returning empty UUID (delete action)", volumeUUID, quotaType, target)
			return "", nil
		}
		logger.Errorf("No quota UUID found for volume %s, quotaType %s, target %s", volumeUUID, quotaType, target)
		return "", vsaerrors.WrapAsTemporalApplicationError(customerrors.NewNotFoundErr("Quota", nil))
	}

	return quotaUUID, nil
}

// HandleQuotaEnableDisable enables or disables quota on a volume.
// This activity calls QuotaEnableDisable and returns the response for workflow-level processing.
// Response validation is done in the workflow, following the sample code pattern.
func (a *QuotaRuleCommonActivity) HandleQuotaEnableDisable(
	ctx context.Context,
	node *models.Node,
	volumeDetails *datamodel.Volume,
	enable bool,
) (*vsa.JobStatus, error) {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Validate inputs
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	// Get SVM name
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details loaded", volumeDetails.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no SVM details"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	action := QuotaRuleEnable
	if !enable {
		action = QuotaRuleDisable
	}
	logger.Infof("%s quota on volume: %s", action, volumeUUID)

	// Call provider to enable/disable quota and return response for workflow processing
	jobStatus, err := provider.QuotaEnableDisable(ctx, volumeUUID, svmName, enable)
	if err != nil {
		logger.Errorf("Failed to %s quota: %v", action, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return jobStatus, nil
}

// QuotaReinitialization performs quota reinitialization (disable then enable).
func (a *QuotaRuleCommonActivity) QuotaReinitialization(
	ctx context.Context,
	node *models.Node,
	volumeDetails *datamodel.Volume,
) error {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	err = performQuotaReinitialization(ctx, provider, volumeDetails)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// GetReplicationDetails fetches replication details from the destination region
func GetReplicationDetails(ctx context.Context, basePath string, projectNumber string, locationID string, volumeReplicationID string, jwt string) (*models.VolumeReplication, error) {
	logger := util.GetLogger(ctx)

	logger.Debug(
		"Fetching destination replication for quota validation",
		"basePath", basePath,
		"projectNumber", projectNumber,
		"locationID", locationID,
		"volumeReplicationID", volumeReplicationID,
	)

	// Validate volumeReplicationID is not empty
	if volumeReplicationID == "" {
		logger.Error("volumeReplicationID is empty")
		return nil, coreerrors.NewVCPError(coreerrors.ErrInputValidationError,
			fmt.Errorf("volumeReplicationID cannot be empty"))
	}

	googleProxyClient := googleproxyclient.GetGProxyClient(basePath, jwt, logger)
	params := &googleproxyclient.V1betaGetMultipleReplicationsInternalParams{
		ProjectNumber:  projectNumber,
		LocationId:     locationID,
		XCorrelationID: googleproxyclient.NewOptString(utils.GetCoRelationIDFromContext(ctx)),
	}
	body := googleproxyclient.ReplicationIDListV1beta{ReplicationUUIDs: []string{volumeReplicationID}}

	response, err := googleProxyClient.Invoker.V1betaGetMultipleReplicationsInternal(ctx, &body, *params)
	if err != nil {
		logger.Error("Failed to fetch destination replication", "error", err)
		return nil, coreerrors.NewVCPError(coreerrors.ErrGoogleProxyInternalGetMultipleReplications, err)
	}

	replicationResponse, ok := response.(*googleproxyclient.V1betaGetMultipleReplicationsInternalOK)
	if !ok || replicationResponse == nil || len(replicationResponse.Replications) < 1 {
		logger.Error("Destination replication not found or invalid response")
		return nil, coreerrors.NewVCPError(coreerrors.ErrGoogleProxyInternalGetMultipleReplicationsNotFound,
			fmt.Errorf("destination replication %s not found", volumeReplicationID))
	}

	// Convert response to models.VolumeReplication
	dstReplication := ConvertGoogleProxyReplicationToModel(replicationResponse)
	return dstReplication, nil
}

// ConvertGoogleProxyReplicationToModel converts google-proxy replication response to models.VolumeReplication
func ConvertGoogleProxyReplicationToModel(response *googleproxyclient.V1betaGetMultipleReplicationsInternalOK) *models.VolumeReplication {
	if response == nil || response.Replications == nil || len(response.Replications) < 1 {
		return nil
	}

	rep := response.Replications[0]
	replication := &models.VolumeReplication{}

	if rep.MirrorState.Set {
		mirrorState := string(rep.MirrorState.Value)
		replication.MirrorState = &mirrorState
	}

	if rep.RelationshipStatus.Set {
		relationshipStatus := string(rep.RelationshipStatus.Value)
		replication.RelationshipStatus = &relationshipStatus
	}

	return replication
}

// VerifyReplicationState validates that a single replication is eligible for quota rule sync.
// This activity checks replication state to ensure quota operations are safe.
func (a *QuotaRuleCommonActivity) VerifyReplicationState(
	ctx context.Context,
	replication *datamodel.VolumeReplication,
	locationId string,
) (bool, error) {
	logger := util.GetLogger(ctx)

	// If replication is nil or has no attributes, it's not eligible
	if replication == nil || replication.ReplicationAttributes == nil {
		logger.Debugf("Replication is nil or has no attributes, not eligible for sync")
		return false, nil
	}

	// Parse current region from locationId
	currentRegion, _, err := utils.ParseRegionAndZone(locationId)
	if err != nil {
		logger.Errorf("Failed to parse region from locationId %s: %v", locationId, err)
		return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to parse region from locationId: %w", err))
	}

	// Parse destination region from destination location to compare with current region
	destRegion, _, err := utils.ParseRegionAndZone(replication.ReplicationAttributes.DestinationLocation)
	if err != nil {
		logger.Warnf("Failed to parse destination location %s: %v", replication.ReplicationAttributes.DestinationLocation, err)
		return false, nil
	}

	// Check if current region is NOT the destination region
	// Only perform mirror state check when we're on the source side (current region != destination region)
	if destRegion != currentRegion {
		// We are on the source side - fetch destination replication to get accurate mirror state
		// Parse destination project number from RemoteUri
		destProjectNumber, err := utils.ParseProjectNumberFromURI(replication.RemoteUri)
		if err != nil {
			logger.Errorf("Failed to parse destination project number from RemoteUri: %v, remoteUri: %s", err, replication.RemoteUri)
			return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to parse destination project number: %w", err))
		}

		// Get destination region base path
		destBasePath, err := utils.GetPairedRegionURI(destRegion)
		if err != nil {
			logger.Errorf("Failed to get paired destination region URI: %v", err)
			return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
		}

		logger.Debug("Fetching destination replication details",
			"projectNumber", destProjectNumber,
			"region", destRegion,
			"location", replication.ReplicationAttributes.DestinationLocation,
			"replicationUUID", replication.ReplicationAttributes.DestinationReplicationUUID)

		// Get JWT token for destination project
		dstToken, err := auth.GetSignedJwtToken(destProjectNumber)
		if err != nil {
			logger.Errorf("Failed to get signed token: %v", err)
			return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get signed token: %w", err))
		}

		// Fetch destination replication details
		destinationReplication, err := GetReplicationDetails(ctx, destBasePath, destProjectNumber,
			replication.ReplicationAttributes.DestinationLocation,
			replication.ReplicationAttributes.DestinationReplicationUUID, dstToken)
		if err != nil {
			logger.Errorf("Failed to fetch destination replication: %v", err)
			return false, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to fetch destination replication for validation: %w", err))
		}

		// Check if this replication is eligible for destination sync using destination replication's mirror state
		// Only MIRRORED or UNINITIALIZED states are eligible
		if destinationReplication.MirrorState != nil {
			mirrorState := *destinationReplication.MirrorState
			if mirrorState == string(gcpgenserver.ReplicationV1betaMirrorStateMIRRORED) ||
				mirrorState == string(gcpgenserver.ReplicationV1betaMirrorStateUNINITIALIZED) {
				// This replication is eligible for sync
				logger.Infof("Replication eligible for sync: mirrorState=%s, destinationLocation=%s, destinationVolumeUUID=%s, projectNumber=%s",
					*destinationReplication.MirrorState, replication.ReplicationAttributes.DestinationLocation,
					replication.ReplicationAttributes.DestinationVolumeUUID, destProjectNumber)
				return true, nil
			}
		}
	}

	logger.Debugf("Replication is not eligible for sync: destinationLocation=%s, destinationVolumeUUID=%s",
		replication.ReplicationAttributes.DestinationLocation, replication.ReplicationAttributes.DestinationVolumeUUID)
	return false, nil
}

// HydrateQuotaRuleDelete hydrates the deletion of a quota rule to CCFE (Google Cloud callback API).
// This activity is used to notify CCFE when a quota rule is deleted on a destination volume.
func (a *QuotaRuleCommonActivity) HydrateQuotaRuleDelete(
	ctx context.Context,
	quotaRuleId string,
	volumeId string,
	region string,
	projectId string,
) error {
	logger := util.GetLogger(ctx)

	// Generate callback token for CCFE authentication
	callbackToken, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		logger.Errorf("Failed to generate callback token for quota rule delete hydration: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to generate callback token: %w", err))
	}

	// Call the common hydration function to dehydrate the quota rule deletion
	err = common.HydrateQuotaRuleDelete(ctx, logger, quotaRuleId, volumeId, region, projectId, callbackToken)
	if err != nil {
		logger.Errorf("Failed to hydrate quota rule delete to CCFE: quotaRuleId=%s, volumeId=%s, error=%v", quotaRuleId, volumeId, err)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to hydrate quota rule delete: %w", err))
	}

	logger.Infof("Successfully hydrated quota rule delete to CCFE: quotaRuleId=%s, volumeId=%s", quotaRuleId, volumeId)
	return nil
}

// mapQuotaRuleToHydrateObject maps a datamodel.QuotaRule to models.QuotaRuleHydrateObject.
// Uses the control-plane quota rule UUID (BaseModel.UUID) for QuotaRuleId.
func mapQuotaRuleToHydrateObject(quotaRule *datamodel.QuotaRule) models.QuotaRuleHydrateObject {
	return models.QuotaRuleHydrateObject{
		ResourceId:  quotaRule.Name,
		QuotaRuleId: quotaRule.UUID,
	}
}

// HydrateQuotaRuleCreate hydrates the creation of a quota rule to CCFE (Google Cloud callback API).
// This activity is used to notify CCFE when a quota rule is created/recreated on a destination volume.
func (a *QuotaRuleCommonActivity) HydrateQuotaRuleCreate(
	ctx context.Context,
	quotaRule *datamodel.QuotaRule,
	volumeId string,
	region string,
	projectId string,
) error {
	logger := util.GetLogger(ctx)

	// Generate callback token for CCFE authentication
	callbackToken, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		logger.Errorf("Failed to generate callback token for quota rule create hydration: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to generate callback token: %w", err))
	}

	// Map datamodel.QuotaRule to models.QuotaRuleHydrateObject
	hydrateObject := mapQuotaRuleToHydrateObject(quotaRule)

	// Call the common hydration function to hydrate the quota rule creation
	err = common.HydrateQuotaRuleCreate(ctx, logger, hydrateObject, volumeId, region, projectId, callbackToken)
	if err != nil {
		logger.Errorf("Failed to hydrate quota rule create to CCFE: quotaRuleName=%s, volumeId=%s, error=%v", quotaRule.Name, volumeId, err)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to hydrate quota rule create: %w", err))
	}

	logger.Infof("Successfully hydrated quota rule create to CCFE: quotaRuleName=%s, volumeId=%s", quotaRule.Name, volumeId)
	return nil
}

// UpdateQuotaRulesOnOntap updates a quota rule's disk limit on ONTAP.
func (a *QuotaRuleUpdateActivity) UpdateQuotaRulesOnOntap(
	ctx context.Context,
	externalQuotaUUID string,
	node *models.Node,
	diskLimitInKibs int64,
) error {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for quota rule update: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	quotaModifyParams := &vsa.UpdateQuotaRuleParams{
		ExternalQuotaRuleUUID: externalQuotaUUID,
		DiskLimitInKibs:       diskLimitInKibs,
	}

	quotaModifyRuleResp, err := provider.UpdateQuotaRule(ctx, quotaModifyParams)
	if err != nil {
		logger.Errorf("Failed to update quota rule on ONTAP: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if quotaModifyRuleResp.IsFailure() {
		logger.Errorf("Quota update job failed: %s", quotaModifyRuleResp.Message)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewBadRequestErr(
			quotaModifyRuleResp.Message))
	}

	return nil
}

// RevertQuotaRulesOnSource reverts the quota rule on the source ONTAP back to its original value.
// This is used when destination sync fails to maintain source-destination synchronization.
func (a *QuotaRuleUpdateActivity) RevertQuotaRulesOnSource(
	ctx context.Context,
	externalQuotaUUID string,
	node *models.Node,
	originalDiskLimitInKib int64,
) error {
	logger := util.GetLogger(ctx)

	logger.Infof("Reverting quota rule on source ONTAP: quotaUUID=%s, originalDiskLimit=%d KiB",
		externalQuotaUUID, originalDiskLimitInKib)

	// Use the existing UpdateQuotaRulesOnOntap activity with the original value
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for quota rule update: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	quotaModifyParams := &vsa.UpdateQuotaRuleParams{
		ExternalQuotaRuleUUID: externalQuotaUUID,
		DiskLimitInKibs:       originalDiskLimitInKib,
	}

	quotaModifyRuleResp, err := provider.UpdateQuotaRule(ctx, quotaModifyParams)
	if err != nil {
		logger.Errorf("Failed to update quota rule on ONTAP: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if quotaModifyRuleResp.IsFailure() {
		logger.Errorf("Quota update job failed: %s", quotaModifyRuleResp.Message)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewBadRequestErr(
			quotaModifyRuleResp.Message))
	}

	return nil
}

// DeleteQuotaRuleOnOntap deletes a quota rule from ONTAP using the provider.
// This activity follows the same pattern as UpdateQuotaRulesOnOntap but for deletion.
func (a *QuotaRuleDeleteActivity) DeleteQuotaRuleOnOntap(
	ctx context.Context,
	externalQuotaUUID string,
	node *models.Node,
) (*vsa.JobStatus, error) {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for quota rule deletion: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	jobStatus, err := provider.DeleteQuotaRule(ctx, externalQuotaUUID)
	if err != nil {
		logger.Errorf("Failed to delete quota rule on ONTAP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return jobStatus, nil
}

// ListQuotaRulesForVolume lists all quota rules for a volume (excluding deleted ones).
// This is used to determine if quota should be disabled after deletion.
func (a *QuotaRuleDeleteActivity) ListQuotaRulesForVolume(
	ctx context.Context,
	volumeUUID string,
) ([]*datamodel.QuotaRule, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Get volume by UUID to get volume ID
	volume, err := se.GetVolume(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get volume %s: %v", volumeUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Get quota rules for the volume (excluding deleted)
	quotaRules, err := se.GetQuotaRulesByVolumeID(ctx, volume.ID)
	if err != nil {
		logger.Errorf("Failed to list quota rules for volume %s: %v", volumeUUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return quotaRules, nil
}

// DeleteQuotaRuleOnDestination calls the internal VCP API to delete a quota rule on a destination volume in a remote region.
// The endpoint is:
// DELETE /v1beta/internal/projects/{projectNumber}/locations/{locationId}/volumes/{volumeId}/quotaRule/{quotaRuleId}
func (a *QuotaRuleDeleteActivity) DeleteQuotaRuleOnDestination(
	ctx context.Context,
	destinationVolumeUUID string,
	destinationQuotaRuleId string,
	destinationRegion string,
	projectNumber string,
	jwtToken *string,
) (*QuotaRuleOperationResult, error) {
	logger := util.GetLogger(ctx)

	logger.Infof("Deleting quota rule on destination volume: volumeUUID=%s, quotaRuleId=%s, region=%s",
		destinationVolumeUUID, destinationQuotaRuleId, destinationRegion)

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for destination API call")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Get destination VCP base path (similar to GetDstBasePath in replication)
	dstBasePath, err := getBasePathForQuotaRule(ctx, destinationRegion)
	if err != nil {
		logger.Errorf("Failed to get destination base path for region %s: %v", destinationRegion, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
	}

	// Create Google Proxy client for the destination VCP region
	// This is the same pattern used in CreateQuotaRuleOnDestination
	googleProxyClient := googleproxyclient.GetGProxyClient(*dstBasePath, *jwtToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaDeleteQuotaRuleVCPParams{
		ProjectNumber:  projectNumber,
		LocationId:     destinationRegion,
		VolumeId:       destinationVolumeUUID,
		QuotaRuleId:    destinationQuotaRuleId,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the internal VCP API to delete quota rule on destination
	logger.Infof("Calling internal VCP API to delete quota rule on destination: volumeUUID=%s, quotaRuleId=%s",
		destinationVolumeUUID, destinationQuotaRuleId)
	res, err := googleProxyClient.Invoker.V1betaDeleteQuotaRuleVCP(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaDeleteQuotaRuleVCP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to delete quota rule on destination: %w", err))
	}

	// Handle response types (following the UpdateQuotaRuleOnDestination pattern)
	switch r := res.(type) {
	case *googleproxyclient.QuotaRulesVCPV1beta:
		logger.Infof("Successfully triggered quota rule deletion on destination: quotaId=%s, resourceId=%s, state=%s",
			r.QuotaId.Value, r.ResourceId, r.State.Value)

		// Check if state is DELETING - need to poll for completion
		if r.State.Value == googleproxyclient.QuotaRulesVCPV1betaStateDELETING {
			// Extract JobId from Jobs array if available for polling
			if len(r.Jobs) > 0 && r.Jobs[0].JobId.IsSet() {
				jobId := r.Jobs[0].JobId.Value
				logger.Infof("Quota rule deletion in progress on destination, returning JobId for polling: %s", jobId)
				return &QuotaRuleOperationResult{OperationName: jobId, IsDone: false}, nil
			}
			// No job ID available but still deleting - return as done (best effort)
			logger.Warnf("Quota rule is in DELETING state but no JobId found, assuming success")
		}
		// State is DELETED or other terminal state
		return &QuotaRuleOperationResult{IsDone: true}, nil
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPMethodNotAllowed:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPRequestTimeout:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPTooManyRequests:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPServiceUnavailable:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaDeleteQuotaRuleVCPInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New("unexpected response type from Google Proxy")))
	}
}

// RevertQuotaRuleOnDestinationForDelete reverts a quota rule deletion by recreating the quota rule on destination.
// This is used when source deletion fails after destination deletion has succeeded, to maintain source-destination synchronization.
// Since we delete on destination first, then on source, if source deletion fails we need to restore the quota rule on destination.
func (a *QuotaRuleDeleteActivity) RevertQuotaRuleOnDestinationForDelete(
	ctx context.Context,
	destinationVolumeUUID string,
	quotaRule *datamodel.QuotaRule,
	destinationRegion string,
	projectNumber string,
	jwtToken *string,
) (*RevertQuotaRuleResult, error) {
	logger := util.GetLogger(ctx)

	logger.Infof("Reverting quota rule deletion on destination by recreating: quotaType=%s, quotaTarget=%s, diskLimit=%d KiB, destinationVolumeUUID=%s",
		quotaRule.QuotaType, quotaRule.QuotaTarget, quotaRule.DiskLimitInKib, destinationVolumeUUID)

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for revert")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Use QuotaRuleCreateActivity to recreate the quota rule on destination via internal VCP API
	quotaRuleCreateActivity := &QuotaRuleCreateActivity{SE: a.SE}
	operationResult, err := quotaRuleCreateActivity.CreateQuotaRuleOnDestination(ctx, destinationVolumeUUID, quotaRule, destinationRegion, projectNumber, jwtToken)
	if err != nil {
		logger.Errorf("Failed to recreate quota rule on destination: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if operationResult == nil || operationResult.QuotaRule == nil {
		logger.Errorf("Revert succeeded but destination quota rule not returned; cannot hydrate")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("revert succeeded but destination quota rule not returned; cannot hydrate"))
	}

	logger.Infof("Successfully recreated quota rule on destination: quotaType=%s, quotaTarget=%s, destinationVolumeUUID=%s",
		quotaRule.QuotaType, quotaRule.QuotaTarget, destinationVolumeUUID)
	return &RevertQuotaRuleResult{
		OperationResult: operationResult,
		QuotaRule:       operationResult.QuotaRule,
	}, nil
}

// ReplicationSyncEligibility holds the result of checking replication sync eligibility.
type ReplicationSyncEligibility struct {
	Eligible              bool
	DestinationVolumeUUID string
	DestinationLocation   string
	DestinationProjectNum string // Destination GCP project number for API calls
}

// QuotaRuleOperationResult holds the result of a quota rule operation on destination.
// When an async operation is started, OperationName will contain the operation identifier
// that can be polled for completion. QuotaRule is the created destination quota rule when
// the API returns a full quota rule (e.g. from V1betaCreateQuotaRuleVCP).
type QuotaRuleOperationResult struct {
	OperationName string               // Operation name/ID for polling (empty if operation completed synchronously)
	IsDone        bool                 // True if operation completed synchronously
	QuotaRule     *datamodel.QuotaRule // Created destination quota rule when available (for hydration)
}

// RevertQuotaRuleResult holds the result of reverting a quota rule deletion
type RevertQuotaRuleResult struct {
	OperationResult *QuotaRuleOperationResult
	QuotaRule       *datamodel.QuotaRule
}

// UpdateRQuotaOnSvm enables or disables recursive quota on the SVM.
// This is used in both create and delete workflows to manage RQuota state.
func (a *QuotaRuleCommonActivity) UpdateRQuotaOnSvm(ctx context.Context, svmExternalUUID string, node *models.Node, rquota bool) error {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for SVM %s: %v", svmExternalUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Call ModifyRquota on the provider
	err = provider.ModifyRquota(ctx, svmExternalUUID, rquota)
	if err != nil {
		logger.Errorf("Failed to modify RQuota on SVM %s: %v", svmExternalUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// HandleDefaultQuotaRuleUpdate checks if a default quota rule already exists for the volume
// and updates it if found. This handles the case where a default quota was auto-created
// as a side effect of creating an individual quota.
func (a *QuotaRuleCreateActivity) HandleDefaultQuotaRuleUpdate(
	ctx context.Context,
	volumeDetails *datamodel.Volume,
	node *models.Node,
	quotaType string,
	diskLimitInKibs int64,
) error {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Get the volume UUID from VolumeAttributes
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	// Get SVM name - check if it's already loaded
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details loaded", volumeDetails.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no SVM details"))
	}

	// This searches through all quota rules on the volume
	// For default quotas, target is empty string
	quotaUUID, ontapQuotaType, err := provider.GetOntapQuotaUUIDAndType(ctx, volumeUUID, svmName, quotaType, "")
	if err != nil {
		logger.Errorf("Failed to get quota UUID and type: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if quotaUUID != "" {
		quotaModifyParams := &vsa.UpdateQuotaRuleParams{
			ExternalQuotaRuleUUID: quotaUUID,
			DiskLimitInKibs:       diskLimitInKibs,
		}

		quotaModifyRuleResp, err := provider.UpdateQuotaRule(ctx, quotaModifyParams)
		if err != nil {
			logger.Errorf("Failed to update default quota rule on ONTAP: %v", err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		if quotaModifyRuleResp.IsFailure() {
			logger.Errorf("Quota update job failed: %s", quotaModifyRuleResp.Message)
			return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUnavailableErr(
				quotaModifyRuleResp.Message))
		}

		// If explicit quota exists and same type of default quota rule is getting created,
		// reinitialization is not required
		convertedQuotaType := vsa.GetQuotaTypeForOntap(quotaType)
		if convertedQuotaType != ontapQuotaType {
			logger.Infof("Quota type mismatch detected: converted=%s, ontap=%s - triggering reinitialization",
				convertedQuotaType, ontapQuotaType)

			// Call helper function to perform quota reinitialization
			err = performQuotaReinitialization(ctx, provider, volumeDetails)
			if err != nil {
				logger.Errorf("Failed to reinitialize quota: %v", err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
		return nil
	}
	return customerrors.NewNotFoundErr("Default Quota", nil)
}

// CreateQuotaRuleOnONTAP creates a quota rule on ONTAP using the provider.
// This activity follows the spec pattern (Step 6 of handleQuotaCreationOnOntap):
// It directly uses the provider to call CreateQuotaRule with the necessary parameters.
func (a *QuotaRuleCreateActivity) CreateQuotaRuleOnONTAP(
	ctx context.Context,
	node *models.Node,
	volumeDetails *datamodel.Volume,
	quotaRule *datamodel.QuotaRule,
) (*vsa.QuotaRuleProviderResponse, error) {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Validate inputs
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	logger.Infof("Creating quota rule on ONTAP: volumeUUID=%s, quotaType=%s, quotaTarget=%s, diskLimit=%d KiB",
		volumeUUID, quotaRule.QuotaType, quotaRule.QuotaTarget, quotaRule.DiskLimitInKib)

	// Get SVM name from volume details
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details", volumeDetails.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no SVM details"))
	}

	// Prepare quota rule parameters for ONTAP (following spec Step 6)
	// DiskLimitInKib is already in KiB from database, which is what CreateQuotaRule expects
	params := vsa.CreateQuotaRuleParams{
		VolumeUUID:     volumeUUID,
		SVMName:        svmName,
		QuotaTarget:    quotaRule.QuotaTarget,
		QuotaType:      quotaRule.QuotaType,
		DiskLimitInKib: quotaRule.DiskLimitInKib,
		RQuota:         quotaRule.RQuota,
	}

	jobStatus, err := provider.CreateQuotaRule(ctx, params)
	if err != nil {
		logger.Errorf("Failed to create quota rule on ONTAP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Per spec, CreateQuotaRule returns *JobStatus (not QuotaRuleProviderResponse)
	// Convert to QuotaRuleProviderResponse for backward compatibility
	res := &vsa.QuotaRuleProviderResponse{
		State:   jobStatus.State,
		Message: jobStatus.Message,
	}

	logger.Infof("Successfully created quota rule on ONTAP with State: %s", res.State)
	return res, nil
}

// GetQuotaStatus retrieves the current quota system status for a volume.
func (a *QuotaRuleCreateActivity) GetQuotaStatus(
	ctx context.Context,
	node *models.Node,
	volumeDetails *datamodel.Volume,
) (*vsa.QuotaStatus, error) {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Validate inputs
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	logger.Infof("Getting quota status for volume: %s", volumeUUID)

	// Call provider to get quota status
	quotaStatus, err := provider.GetQuotaStatus(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get quota status from ONTAP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Quota status for volume %s: enabled=%t, state=%s",
		volumeUUID, quotaStatus.Enabled, quotaStatus.State)

	return quotaStatus, nil
}

func performQuotaReinitialization(
	ctx context.Context,
	provider vsa.Provider,
	volumeDetails *datamodel.Volume,
) error {
	logger := util.GetLogger(ctx)

	// Validate inputs
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return customerrors.NewUserInputValidationErr("Volume has no ExternalUUID")
	}

	// Get SVM name
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details loaded", volumeDetails.UUID)
		return customerrors.NewUserInputValidationErr("Volume has no SVM details")
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	logger.Infof("Starting quota reinitialization for volume: %s (disable then enable)", volumeUUID)

	quotaDisableResp, err := provider.QuotaEnableDisable(context.TODO(), volumeUUID, svmName, false)
	if err != nil {
		logger.Errorf("Failed to disable quota: %v", err)
		return err
	}

	if quotaDisableResp.State == vsa.JobRespFailure {
		// Delete the quota rule from ONTAP
		logger.Errorf("Quota disable failed: %s", quotaDisableResp.Message)
		return customerrors.NewUserInputValidationErr(quotaDisableResp.Message)
	}

	logger.Infof("Successfully disabled quota on volume: %s", volumeUUID)

	quotaEnableResp, err := provider.QuotaEnableDisable(context.TODO(), volumeUUID, svmName, true)
	if err != nil {
		logger.Errorf("Failed to enable quota: %v", err)
		return err
	}

	if quotaEnableResp.State == vsa.JobRespFailure {
		logger.Errorf("Quota enable failed: %s", quotaEnableResp.Message)
		return customerrors.NewUserInputValidationErr(quotaEnableResp.Message)
	}

	logger.Infof("Successfully enabled quota on volume: %s (reinitialization complete)", volumeUUID)
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

// HasCIFS checks if CIFS (SMB) protocol exists in the given protocol types array
func HasCIFS(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolSMB, protocolTypes)
}

// HasNFSv3 checks if NFSv3 protocol exists in the given protocol types array
func HasNFSv3(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolNFSv3, protocolTypes)
}

// HasNFSv4 checks if NFSv4 protocol exists in the given protocol types array
func HasNFSv4(protocolTypes []string) bool {
	return hasProtocol(utils.ProtocolNFSv4, protocolTypes)
}

// HasDualProtocolForUserMapping checks if both SMB and NFS protocols exist (dual protocol)
func HasDualProtocolForUserMapping(protocolTypes []string) bool {
	return HasCIFS(protocolTypes) && (HasNFSv3(protocolTypes) || HasNFSv4(protocolTypes))
}

// HandleQuotaEnablementAndReinitialization encapsulates the complete quota enablement logic
// including status checking, enabling quota if off, and handling reinitialization if needed
// based on quota rule response. This implements the logic from spec lines 470-484.
func (a *QuotaRuleCommonActivity) HandleQuotaEnablementAndReinitialization(
	ctx context.Context,
	node *models.Node,
	volumeDetails *datamodel.Volume,
	quotaRuleResponse *vsa.QuotaRuleProviderResponse,
) error {
	logger := util.GetLogger(ctx)

	// Create provider from node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Errorf("Failed to create provider for volume %s: %v", volumeDetails.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Validate inputs
	if volumeDetails.VolumeAttributes == nil || volumeDetails.VolumeAttributes.ExternalUUID == "" {
		logger.Errorf("Volume %s has no ExternalUUID in VolumeAttributes", volumeDetails.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no ExternalUUID"))
	}

	volumeUUID := volumeDetails.VolumeAttributes.ExternalUUID

	// Get SVM name
	var svmName string
	if volumeDetails.Svm != nil && volumeDetails.Svm.Name != "" {
		svmName = volumeDetails.Svm.Name
	} else {
		logger.Errorf("Volume %s has no SVM details loaded", volumeDetails.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewUserInputValidationErr(
			"Volume has no SVM details"))
	}

	logger.Infof("Checking quota status for volume: %s", volumeUUID)
	quotaStatus, err := provider.GetQuotaStatus(ctx, volumeUUID)
	if err != nil {
		logger.Errorf("Failed to get quota status: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Quota status for volume %s: enabled=%t, state=%s",
		volumeUUID, quotaStatus.Enabled, quotaStatus.State)

	if quotaStatus.State == vsa.QuotaStateOff {
		logger.Infof("Quota is OFF, enabling quota on volume: %s", volumeUUID)
		quotaEnableResp, err := provider.QuotaEnableDisable(ctx, volumeUUID, svmName, true)
		if err != nil {
			logger.Errorf("Failed to enable quota: %v", err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		if quotaEnableResp.State == vsa.JobRespFailure {
			// Check if failure is due to specific error conditions
			if !strings.Contains(quotaEnableResp.Message, vsa.QuotaStatusFailed) &&
				!strings.Contains(quotaEnableResp.Message, svmName) {
				logger.Errorf("Quota enable failed with non-retryable error: %s", quotaEnableResp.Message)
				return vsaerrors.WrapAsTemporalApplicationError(errors.New(
					quotaEnableResp.Message + " - please delete the quota rule and try again"))
			}
			// If message contains QuotaStatusFailed or svmName, handle as regular failure
			logger.Errorf("Quota enable failed: %s", quotaEnableResp.Message)
			return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewBadRequestErr(
				quotaEnableResp.Message))
		}

		logger.Infof("Successfully enabled quota on volume: %s", volumeUUID)
		return nil
	} else {
		logger.Infof("Quota is already ON for volume: %s, checking if reinitialization needed", volumeUUID)
		if quotaRuleResponse != nil && quotaRuleResponse.State == vsa.JobRespFailure {
			logger.Warnf("Quota rule operation returned failure state. Message: %s", quotaRuleResponse.Message)

			// Check if failure is due to resize or activation operation
			if strings.Contains(quotaRuleResponse.Message, vsa.ResizeOperationFailed) ||
				strings.Contains(quotaRuleResponse.Message, vsa.ActivationOperationFailed) {
				logger.Infof("Detected resize/activation failure - triggering quota reinitialization")

				// Perform quota reinitialization (disable then enable)
				err = performQuotaReinitialization(ctx, provider, volumeDetails)
				if err != nil {
					logger.Errorf("Quota reinitialization failed: %v", err)
					return vsaerrors.WrapAsTemporalApplicationError(err)
				}

				logger.Infof("Successfully reinitialized quota on volume: %s", volumeUUID)
				return nil
			}

			// Other failure - return error
			logger.Errorf("Quota rule operation failed with non-reinitialization error: %s", quotaRuleResponse.Message)
			return vsaerrors.WrapAsTemporalApplicationError(customerrors.NewBadRequestErr(
				quotaRuleResponse.Message))
		}

		logger.Infof("Quota is already enabled and no reinitialization needed for volume: %s", volumeUUID)
		return nil
	}
}

// UpdateQuotaRuleState updates the quota rule state in the database.
// If the quota rule is in CREATING state, it will transition to READY state
// and update the ExternalUUID. If in UPDATING state, it transitions to READY with updated fields.
func (a *QuotaRuleCommonActivity) UpdateQuotaRuleState(ctx context.Context, quotaRule datamodel.QuotaRule, isCleanupDelete bool) error {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Fetch current quota rule from DB to check its state
	currentQuotaRule, err := se.GetQuotaRuleByUUID(ctx, quotaRule.UUID, quotaRule.AccountID)
	if err != nil {
		logger.Errorf("Failed to fetch quota rule for state check: uuid=%s, error=%v", quotaRule.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if isCleanupDelete { // Cleanup delete case
		updatedAt := time.Now()
		currentQuotaRule.State = datamodel.LifeCycleStateDeleted
		currentQuotaRule.StateDetails = datamodel.LifeCycleStateDeletedDetails
		currentQuotaRule.UpdatedAt = updatedAt
		currentQuotaRule.DeletedAt = &gorm.DeletedAt{Time: updatedAt, Valid: true}
		logger.Infof("Cleanup delete case: marking quota rule as DELETED from CREATING state: uuid=%s", quotaRule.UUID)
	} else if quotaRule.State == datamodel.LifeCycleStateError {
		// If quotaRule.State is ERROR (set by defer blocks), set it to ERROR regardless of current state
		logger.Infof("Setting quota rule state to ERROR: uuid=%s", quotaRule.UUID)
		currentQuotaRule.State = datamodel.LifeCycleStateError
		currentQuotaRule.StateDetails = quotaRule.StateDetails
		currentQuotaRule.UpdatedAt = time.Now()
	} else if currentQuotaRule.State == datamodel.LifeCycleStateCreating {
		// If quota rule is in CREATING state, transition to READY
		logger.Infof("Quota rule is in CREATING state, transitioning to READY: uuid=%s", quotaRule.UUID)
		currentQuotaRule.State = datamodel.LifeCycleStateREADY
		currentQuotaRule.StateDetails = datamodel.LifeCycleStateReadyDetails
	} else if currentQuotaRule.State == datamodel.LifeCycleStateUpdating {
		currentQuotaRule.DiskLimitInKib = quotaRule.DiskLimitInKib
		currentQuotaRule.Description = quotaRule.Description
		currentQuotaRule.State = datamodel.LifeCycleStateREADY
		currentQuotaRule.StateDetails = datamodel.LifeCycleStateReadyDetails

		logger.Infof("Quota rule is in UPDATING state, transitioning to READY: uuid=%s", quotaRule.UUID)
		currentQuotaRule.UpdatedAt = time.Now()
	} else if currentQuotaRule.State == datamodel.LifeCycleStateDeleting {
		updatedAt := time.Now()
		currentQuotaRule.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
		currentQuotaRule.State = datamodel.LifeCycleStateDeleted
		currentQuotaRule.StateDetails = datamodel.LifeCycleStateDeletedDetails
		currentQuotaRule.UpdatedAt = updatedAt
		currentQuotaRule.DeletedAt = &gorm.DeletedAt{Time: updatedAt, Valid: true}
	}

	_, err = se.UpdateQuotaRule(ctx, currentQuotaRule)
	if err != nil {
		logger.Errorf("Failed to update quota rule state in DB: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Quota rule state: %s updated successfully in the db", quotaRule.Name)

	return nil
}

// DescribeQuotaRuleRemoteJob polls the status of an async quota rule operation on the destination VCP.
// This follows the same pattern as DescribeRemoteJob in replication activities.
// The activity will return an error if the operation is not yet complete (allowing Temporal to retry),
// return nil if completed successfully, or return a non-retryable error if the operation failed.
func (a *QuotaRuleCommonActivity) DescribeQuotaRuleRemoteJob(
	ctx context.Context,
	operationName string,
	destinationRegion string,
	projectNumber string,
	jwtToken *string,
) error {
	logger := util.GetLogger(ctx)

	if operationName == "" {
		logger.Debugf("No operation name provided, skipping polling")
		return nil
	}

	logger.Infof("Polling quota rule operation on destination: operationName=%s, region=%s, projectNumber=%s",
		operationName, destinationRegion, projectNumber)

	// Get destination VCP base path
	dstBasePath, err := getBasePathForQuotaRule(ctx, destinationRegion)
	if err != nil {
		logger.Errorf("Failed to get destination base path for region %s: %v", destinationRegion, err)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
	}

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for destination API call")
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Create Google Proxy client for the destination VCP region
	googleProxyClient := googleproxyclient.GetGProxyClient(*dstBasePath, *jwtToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare describe operation params
	describeParams := googleproxyclient.V1betaDescribeOperationParams{
		OperationId:    operationName,
		ProjectNumber:  projectNumber,
		LocationId:     destinationRegion,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	res, err := googleProxyClient.Invoker.V1betaDescribeOperation(ctx, describeParams)
	if err != nil {
		logger.Errorf("Failed to describe operation %s: %v", operationName, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobAPI, err)
	}

	operation, ok := res.(*googleproxyclient.OperationV1beta)
	if !ok {
		logger.Errorf("Unexpected response type from V1betaDescribeOperation: %T", res)
		return vsaerrors.NewVCPError(vsaerrors.ErrDescribingJobAPI, fmt.Errorf("unexpected response type: %T", res))
	}

	if operation.Done.Value {
		// Operation completed
		if operation.Error.IsSet() {
			// Operation failed
			errorMsg := operation.Error.Value.Message.Value
			logger.Errorf("Quota rule operation %s failed: %s", operationName, errorMsg)
			return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrJobFailed, fmt.Errorf("quota rule operation failed: %s", errorMsg)))
		}
		// Operation succeeded
		logger.Infof("Quota rule operation %s completed successfully", operationName)
		return nil
	}

	// Operation still in progress - return retryable error so Temporal will retry
	logger.Infof("Quota rule operation %s is still in progress, will retry", operationName)
	return vsaerrors.NewVCPError(vsaerrors.ErrJobNotFinished, fmt.Errorf("operation %s not finished", operationName))
}

// convertQuotaRulesVCPV1betaToDatamodel maps the internal VCP API response to datamodel.QuotaRule
// for use in hydration (destination quota rule identity: UUID, Name, etc.).
func convertQuotaRulesVCPV1betaToDatamodel(r *googleproxyclient.QuotaRulesVCPV1beta) *datamodel.QuotaRule {
	if r == nil {
		return nil
	}
	q := &datamodel.QuotaRule{
		Name:           r.ResourceId,
		QuotaType:      string(r.QuotaType),
		DiskLimitInKib: r.DiskLimitInMib * 1024,
	}
	if v, ok := r.QuotaId.Get(); ok {
		q.UUID = v
	}
	if v, ok := r.QuotaTarget.Get(); ok {
		q.QuotaTarget = v
	}
	if r.State.IsSet() {
		q.State = string(r.State.Value)
	}
	if v, ok := r.StateDetails.Get(); ok {
		q.StateDetails = v
	}
	if v, ok := r.Description.Get(); ok {
		q.Description = v
	}
	if v, ok := r.CreatedAt.Get(); ok {
		q.CreatedAt = v
	}
	if v, ok := r.UpdatedAt.Get(); ok {
		q.UpdatedAt = v
	}
	return q
}

// CreateQuotaRuleOnDestination calls the internal VCP API to create quota rule on destination volume.
func (a *QuotaRuleCreateActivity) CreateQuotaRuleOnDestination(
	ctx context.Context,
	destinationVolumeUUID string,
	quotaRule *datamodel.QuotaRule,
	destinationRegion string,
	projectNumber string,
	jwtToken *string,
) (*QuotaRuleOperationResult, error) {
	logger := util.GetLogger(ctx)

	logger.Infof("Creating quota rule on destination volume: volumeUUID=%s, region=%s, quotaType=%s, diskLimit=%d",
		destinationVolumeUUID, destinationRegion, quotaRule.QuotaType, quotaRule.DiskLimitInKib)

	// Get destination VCP base path (similar to GetDstBasePath in replication)
	dstBasePath, err := getBasePathForQuotaRule(ctx, destinationRegion)
	if err != nil {
		logger.Errorf("Failed to get destination base path for region %s: %v", destinationRegion, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
	}

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for destination API call")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Create Google Proxy client for the destination VCP region
	// This is the same pattern used in CreateReplicationOnDestination
	googleProxyClient := googleproxyclient.GetGProxyClient(*dstBasePath, *jwtToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaCreateQuotaRuleVCPParams{
		ProjectNumber:  projectNumber,
		LocationId:     destinationRegion,
		VolumeId:       destinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Convert disk limit from KiB to MiB (API expects MiB)
	diskLimitInMib := quotaRule.DiskLimitInKib / 1024

	// Prepare quota rule creation request body
	body := &googleproxyclient.QuotaRuleCreateV1beta{
		ResourceId:     quotaRule.Name,
		QuotaType:      googleproxyclient.QuotaRuleCreateV1betaQuotaType(quotaRule.QuotaType),
		QuotaTarget:    googleproxyclient.NewOptString(quotaRule.QuotaTarget),
		DiskLimitInMib: diskLimitInMib,
		Description:    googleproxyclient.NewOptString(quotaRule.Description),
	}

	// Call the internal VCP API to create quota rule on destination
	logger.Infof("Calling internal VCP API to create quota rule on destination: volumeUUID=%s", destinationVolumeUUID)
	res, err := googleProxyClient.Invoker.V1betaCreateQuotaRuleVCP(ctx, body, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaCreateQuotaRuleVCP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to create quota rule on destination: %w", err))
	}

	// Handle response types (following the CreateReplicationOnDestination pattern)
	switch r := res.(type) {
	case *googleproxyclient.QuotaRulesVCPV1beta:
		logger.Infof("Successfully created quota rule on destination: quotaId=%s, resourceId=%s, state=%s",
			r.QuotaId.Value, r.ResourceId, r.State.Value)

		destQuotaRule := convertQuotaRulesVCPV1betaToDatamodel(r)
		// Check if state is CREATING - need to poll for completion
		if r.State.Value == googleproxyclient.QuotaRulesVCPV1betaStateCREATING {
			// Extract JobId from Jobs array if available for polling
			if len(r.Jobs) > 0 && r.Jobs[0].JobId.IsSet() {
				jobId := r.Jobs[0].JobId.Value
				logger.Infof("Quota rule creation in progress on destination, returning JobId for polling: %s", jobId)
				return &QuotaRuleOperationResult{OperationName: jobId, IsDone: false, QuotaRule: destQuotaRule}, nil
			}
			// No job ID available but still creating - return as done (best effort)
			logger.Warnf("Quota rule is in CREATING state but no JobId found, assuming success")
		}
		// State is READY or other terminal state
		return &QuotaRuleOperationResult{IsDone: true, QuotaRule: destQuotaRule}, nil
	case *googleproxyclient.V1betaCreateQuotaRuleVCPBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPMethodNotAllowed:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPRequestTimeout:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPTooManyRequests:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPServiceUnavailable:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaCreateQuotaRuleVCPInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New("unexpected response type from Google Proxy")))
	}
}

// GetMatchingQuotaRuleOnDestination calls the VCP API to fetch quota rules from a destination volume in a remote region
// and returns the matching quota rule by name.
// The endpoint is:
// GET /v1beta/projects/{projectNumber}/locations/{locationId}/volumes/{volumeId}/quotaRules
func (a *QuotaRuleCommonActivity) GetMatchingQuotaRuleOnDestination(
	ctx context.Context,
	destinationVolumeUUID string,
	destinationRegion string,
	projectNumber string,
	quotaRuleName string,
	jwtToken *string,
) (*string, error) {
	logger := util.GetLogger(ctx)

	logger.Infof("Fetching quota rules from destination volume: volumeUUID=%s, region=%s, quotaRuleName=%s",
		destinationVolumeUUID, destinationRegion, quotaRuleName)

	// Get destination VCP base path (similar to GetDstBasePath in replication)
	dstBasePath, err := getBasePathForQuotaRule(ctx, destinationRegion)
	if err != nil {
		logger.Errorf("Failed to get destination base path for region %s: %v", destinationRegion, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
	}

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for destination API call")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Create Google Proxy client for the destination VCP region
	// This is the same pattern used in CreateReplicationOnDestination
	googleProxyClient := googleproxyclient.GetGProxyClient(*dstBasePath, *jwtToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaListAllQuotaRulesParams{
		ProjectNumber:  projectNumber,
		LocationId:     destinationRegion,
		VolumeId:       destinationVolumeUUID,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Call the VCP API to list quota rules on destination
	logger.Infof("Calling VCP API to list quota rules on destination: volumeUUID=%s", destinationVolumeUUID)
	res, err := googleProxyClient.Invoker.V1betaListAllQuotaRules(ctx, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaListAllQuotaRules: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to list quota rules on destination: %w", err))
	}

	// Handle response types (following the CreateQuotaRuleOnDestination pattern)
	switch r := res.(type) {
	case *googleproxyclient.V1betaListAllQuotaRulesOK:
		logger.Infof("Successfully fetched quota rules from destination: count=%d, volumeUUID=%s",
			len(r.QuotaRules), destinationVolumeUUID)

		// Find the matching quota rule by name (ResourceId)
		for _, quotaRule := range r.QuotaRules {
			if quotaRule.ResourceId == quotaRuleName {
				logger.Infof("Found matching quota rule on destination: resourceId=%s, quotaId=%s, volumeUUID=%s",
					quotaRule.ResourceId, quotaRule.QuotaId.Value, destinationVolumeUUID)

				destinationQuotaRuleId, hasQuotaId := quotaRule.QuotaId.Get()
				if !hasQuotaId {
					logger.Errorf("Matching quota rule found but QuotaId is not set: resourceId=%s, volumeUUID=%s",
						quotaRule.ResourceId, destinationVolumeUUID)
					return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleNotFound, customerrors.NewNotFoundErr("quota rule", &quotaRuleName)))
				}

				return &destinationQuotaRuleId, nil
			}
		}

		// No matching quota rule found
		logger.Errorf("No matching quota rule found on destination for quota rule name=%s: location=%s, volumeUUID=%s",
			quotaRuleName, destinationRegion, destinationVolumeUUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleNotFound, customerrors.NewNotFoundErr("quota rule", &quotaRuleName)))
	case *googleproxyclient.V1betaListAllQuotaRulesBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleBadRequest, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleUnauthorized, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleForbidden, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrMatchingQuotaRuleNotFoundOnDestination, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleConflict, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleBadRequest, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesTooManyRequests:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleTooManyRequests, errors.New(r.Message)))
	case *googleproxyclient.V1betaListAllQuotaRulesInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrQuotaRuleInternalServerError, errors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, errors.New("unexpected response type from Google Proxy")))
	}
}

// UpdateQuotaRuleOnDestination calls the internal VCP API to update a quota rule on a destination volume in a remote region.
// The endpoint is:
// PUT /v1beta/internal/projects/{projectNumber}/locations/{locationId}/volumes/{volumeId}/quotaRule/{quotaRuleId}
func (a *QuotaRuleUpdateActivity) UpdateQuotaRuleOnDestination(
	ctx context.Context,
	destinationVolumeUUID string,
	destinationQuotaRuleId string,
	diskLimitInKib int64,
	destinationRegion string,
	projectNumber string,
	jwtToken *string,
	description string,
) (*QuotaRuleOperationResult, error) {
	logger := util.GetLogger(ctx)

	logger.Infof("Updating quota rule on destination volume: volumeUUID=%s, quotaRuleId=%s, region=%s, diskLimit=%d",
		destinationVolumeUUID, destinationQuotaRuleId, destinationRegion, diskLimitInKib)

	// Get destination VCP base path (similar to GetDstBasePath in replication)
	dstBasePath, err := getBasePathForQuotaRule(ctx, destinationRegion)
	if err != nil {
		logger.Errorf("Failed to get destination base path for region %s: %v", destinationRegion, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to get destination base path: %w", err))
	}

	// Validate JWT token is provided
	if jwtToken == nil || *jwtToken == "" {
		logger.Errorf("JWT token is required for destination API call")
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("JWT token is required"))
	}

	// Create Google Proxy client for the destination VCP region
	// This is the same pattern used in CreateQuotaRuleOnDestination
	googleProxyClient := googleproxyclient.GetGProxyClient(*dstBasePath, *jwtToken, logger)

	// Get correlation ID for tracing
	correlationID := utils.GetCoRelationIDFromContext(ctx)

	// Prepare API parameters
	params := googleproxyclient.V1betaUpdateQuotaRuleVCPParams{
		ProjectNumber:  projectNumber,
		LocationId:     destinationRegion,
		VolumeId:       destinationVolumeUUID,
		QuotaRuleId:    destinationQuotaRuleId,
		XCorrelationID: googleproxyclient.NewOptString(correlationID),
	}

	// Convert disk limit from KiB to MiB (API expects MiB)
	diskLimitInMib := diskLimitInKib / 1024

	// Prepare quota rule update request body
	body := &googleproxyclient.QuotaRulesUpdateV1beta{}

	// Set disk limit if it's being updated (non-zero)
	if diskLimitInKib > 0 {
		body.DiskLimitInMib = googleproxyclient.NewOptInt64(diskLimitInMib)
	}

	if description != "" {
		body.Description = googleproxyclient.NewOptString(description)
	}

	// Call the internal VCP API to update quota rule on destination
	logger.Infof("Calling internal VCP API to update quota rule on destination: volumeUUID=%s, quotaRuleId=%s",
		destinationVolumeUUID, destinationQuotaRuleId)
	res, err := googleProxyClient.Invoker.V1betaUpdateQuotaRuleVCP(ctx, body, params)
	if err != nil {
		logger.Errorf("Failed to call V1betaUpdateQuotaRuleVCP: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("failed to update quota rule on destination: %w", err))
	}

	// Handle response types (following the CreateQuotaRuleOnDestination pattern)
	switch r := res.(type) {
	case *googleproxyclient.QuotaRulesVCPV1beta:
		logger.Infof("Successfully triggered update quota rule on destination: quotaId=%s, resourceId=%s, state=%s",
			r.QuotaId.Value, r.ResourceId, r.State.Value)

		// Check if state is UPDATING - need to poll for completion
		if r.State.Value == googleproxyclient.QuotaRulesVCPV1betaStateUPDATING {
			// Extract JobId from Jobs array if available for polling
			if len(r.Jobs) > 0 && r.Jobs[0].JobId.IsSet() {
				jobId := r.Jobs[0].JobId.Value
				logger.Infof("Quota rule update in progress on destination, returning JobId for polling: %s", jobId)
				return &QuotaRuleOperationResult{OperationName: jobId, IsDone: false}, nil
			}
			// No job ID available but still updating - return as done (best effort)
			logger.Warnf("Quota rule is in UPDATING state but no JobId found, assuming success")
		}
		// State is READY or other terminal state
		return &QuotaRuleOperationResult{IsDone: true}, nil
	case *googleproxyclient.OperationV1beta:
		logger.Infof("Quota rule update operation started on destination: operationName=%s", r.Name.Value)
		return &QuotaRuleOperationResult{OperationName: r.Name.Value, IsDone: false}, nil
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPBadRequest:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPUnauthorized:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPForbidden:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPNotFound:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPConflict:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPMethodNotAllowed:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPRequestTimeout:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPUnprocessableEntity:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPTooManyRequests:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPServiceUnavailable:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	case *googleproxyclient.V1betaUpdateQuotaRuleVCPInternalServerError:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New(r.Message)))
	default:
		return nil, vsaerrors.WrapAsTemporalApplicationError(coreerrors.NewVCPError(coreerrors.ErrCreateInternalQuotaRule, customerrors.New("unexpected response type from Google Proxy")))
	}
}

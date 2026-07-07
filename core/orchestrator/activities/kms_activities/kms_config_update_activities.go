package kms_activities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// EnrichUpdateKmsConfigParamsFromKeyURI parses KeyUri into key components and grant.
func EnrichUpdateKmsConfigParamsFromKeyURI(params *common.UpdateKmsConfigParams) {
	if params == nil || params.KeyUri == "" {
		return
	}
	parsedKeyFullPath, parseErr := utils.ParseKeyFullPathResource(params.KeyUri)
	if parseErr != nil {
		return
	}
	params.KeyName = parsedKeyFullPath.CryptoKey
	params.KeyRingLocation = parsedKeyFullPath.Location
	params.KeyRing = parsedKeyFullPath.KeyRing
	params.KeyProjectID = parsedKeyFullPath.ProjectID
	params.KmsGrant = parsedKeyFullPath.Grant
}

// IsGrantOnlyKmsKeyUpdate reports whether the update changes only kms_grant while
// leaving the underlying crypto key path components unchanged.
func IsGrantOnlyKmsKeyUpdate(kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) bool {
	if kmsConfig == nil || params == nil || params.KeyName == "" {
		return false
	}
	return kmsConfig.KeyName == params.KeyName &&
		kmsConfig.KeyRing == params.KeyRing &&
		kmsConfig.KeyRingLocation == params.KeyRingLocation &&
		kmsConfig.KeyProjectID == params.KeyProjectID &&
		kmsConfig.KmsGrant != params.KmsGrant
}

func resolveKmsKeyUpdateState(currentState string, grantOnly bool, hasGrant bool) (state string, stateDetails string, setState bool) {
	if grantOnly {
		switch currentState {
		case datamodel.LifeCycleStateInUse:
			return datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails, true
		case datamodel.LifeCycleStateCreated, datamodel.LifeCycleStateKeyCheckPending:
			return datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, true
		default:
			return "", "", false
		}
	}
	if hasGrant {
		return datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, true
	}
	return datamodel.LifeCycleStateCreated, datamodel.LifeCycleStateCreatedDetails, true
}

func UpdateKmsConfig(se database.Storage, ctx context.Context, kmsConfig *datamodel.KmsConfig, params *common.UpdateKmsConfigParams) error {
	logger := util.GetLogger(ctx)
	updateFields := make(map[string]interface{})

	if params.ResourceID != "" {
		updateFields["resource_id"] = params.ResourceID
	}
	if params.Description != nil {
		updateFields["description"] = *params.Description
	}
	if params.KeyName != "" {
		updateFields["key_name"] = params.KeyName
		updateFields["key_ring"] = params.KeyRing
		updateFields["key_ring_location"] = params.KeyRingLocation
		updateFields["key_project_id"] = params.KeyProjectID
		// Always write the grant when the key path changes so it is cleared
		// when the new path has no grant suffix.
		updateFields["kms_grant"] = params.KmsGrant

		grantOnly := IsGrantOnlyKmsKeyUpdate(kmsConfig, params)
		if state, stateDetails, ok := resolveKmsKeyUpdateState(kmsConfig.State, grantOnly, params.KmsGrant != ""); ok {
			updateFields["state"] = state
			updateFields["state_details"] = stateDetails
		}
	}
	err := se.UpdateKmsConfig(ctx, kmsConfig.UUID, updateFields)
	if err != nil {
		return err
	}
	logger.Debug("KmsConfig:%s update successfully in the db", kmsConfig.Name)

	return nil
}

func (a *KmsConfigActivity) UpdateKmsConfigState(ctx context.Context, kmsConfig *datamodel.KmsConfig, state, stateDetails string) error {
	activity.RecordHeartbeat(ctx, "Starting UpdateKmsConfigState activity")
	defer activity.RecordHeartbeat(ctx, "Finished UpdateKmsConfigState activity")
	logger := util.GetLogger(ctx)
	se := a.SE

	activity.RecordHeartbeat(ctx, "Updating KMS configuration state to %s", state)
	_, err := se.UpdateKmsConfigState(ctx, kmsConfig.UUID, state, stateDetails)
	if err != nil {
		// When a CREATE workflow is cancelled and rolled back, the KMS config record may be deleted before the DELETE workflow
		// attempts to update its state. Since the record no longer exists, there's nothing to update, and this should not
		// cause the workflow to fail. The desired end state (resource cleaned up) is achieved regardless of whether the state update succeeds.
		if errors.IsNotFoundErr(err) {
			logger.Info("KMS config not found, skipping state update", "kms_config_uuid", kmsConfig.UUID)
			return nil
		}
		return err
	}
	logger.Debug("KmsConfig state:%s update successfully in the db", kmsConfig.Name)

	return nil
}

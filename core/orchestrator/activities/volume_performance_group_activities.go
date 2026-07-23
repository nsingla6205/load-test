package activities

import (
	"context"
	"fmt"
	"regexp"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/mqos"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// VolumePerformanceGroupActivity handles VPG-related activities
type VolumePerformanceGroupActivity struct {
	SE database.Storage
}

// GetPoolViewByPoolID returns the pool view for the given pool ID (used by VPG update workflow).
func (a *VolumePerformanceGroupActivity) GetPoolViewByPoolID(ctx context.Context, poolID int64) (*datamodel.PoolView, error) {
	activity.RecordHeartbeat(ctx, "Getting pool view for VPG update")
	pool, err := a.SE.GetPoolByID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	poolView, err := a.SE.GetPool(ctx, pool.UUID, pool.AccountID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return poolView, nil
}

// CreateVPGInDB creates a VPG in the database (atomic cap via CreateVolumePerformanceGroupAtomic).
func (a *VolumePerformanceGroupActivity) CreateVPGInDB(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) (*datamodel.VolumePerformanceGroup, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Creating VPG in database")

	createdVPG, err := mqos.CreateVolumePerformanceGroupAtomic(ctx, a.SE, vpg)
	if err != nil {
		logger.Error("Failed to create VPG in database", "error", err, "vpg_name", vpg.Name)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("VPG created in database", "vpg_id", createdVPG.UUID, "vpg_name", createdVPG.Name)
	return createdVPG, nil
}

// GetVolumePerformanceGroupByUUID retrieves a VolumePerformanceGroup from the database by UUID.
func (a *VolumePerformanceGroupActivity) GetVolumePerformanceGroupByUUID(ctx context.Context, vpgUUID string) (*datamodel.VolumePerformanceGroup, error) {
	if vpgUUID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("vpgUUID is empty"))
	}
	activity.RecordHeartbeat(ctx, "Fetching VPG by UUID")
	vpg, err := a.SE.GetVolumePerformanceGroupByUUID(ctx, vpgUUID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return vpg, nil
}

// CreateQoSPolicyInONTAP creates a QoS policy in ONTAP and returns the policy ID (UUID/name)
// This is called before creating the VPG in the database so the VPG can be created with the QPG UUID already set
func (a *VolumePerformanceGroupActivity) CreateQoSPolicyInONTAP(
	ctx context.Context,
	vpg *datamodel.VolumePerformanceGroup,
	node *models.Node,
) (string, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Creating QoS policy in ONTAP")

	// Get SVM for the pool
	svm, err := a.SE.GetSvmForPoolID(ctx, vpg.PoolID)
	if err != nil {
		logger.Error("Failed to get SVM for QoS policy creation", "error", err, "pool_id", vpg.PoolID)
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Get provider for the node
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider for QoS policy creation", "error", err)
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	isShared := vpg.IsShared()
	// Create the QoS policy in ONTAP
	createQosParams := vsa.CreateQoSGroupPolicyParams{
		Name:          vpg.Name,
		SvmName:       svm.Name,
		MaxThroughput: vpg.ThroughputMibps,
		MaxIOPS:       vpg.Iops,
		IsShared:      nillable.GetBoolPtr(isShared),
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating QoS policy: %s", vpg.Name))
	qosPolicyResp, err := provider.CreateQoSGroupPolicy(createQosParams)
	if err != nil {
		logger.Error("Failed to create QoS policy in ONTAP", "error", err, "vpg_name", vpg.Name)
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy created in ONTAP", "qos_policy", qosPolicyResp.Name, "vpg_name", vpg.Name)
	return qosPolicyResp.UUID, nil
}

// UpdateVPGWithOntapID updates the VPG row with the ONTAP QoS policy ID after successful creation in ONTAP.
func (a *VolumePerformanceGroupActivity) UpdateVPGWithOntapID(ctx context.Context, vpgUUID, ontapQosPolicyID string) error {
	activity.RecordHeartbeat(ctx, "Updating VPG with Ontap ID")
	vpg, err := a.SE.GetVolumePerformanceGroupByUUID(ctx, vpgUUID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	vpg.OntapQosPolicyID = ontapQosPolicyID
	return vsaerrors.WrapAsTemporalApplicationError(a.SE.UpdateVolumePerformanceGroup(ctx, vpg))
}

// uuidPattern matches a canonical UUID (8-4-4-4-12 hex). Used to decide whether to pass
// the policy identifier as UUID or Name to DeleteQoSGroupPolicy (ONTAP expects UUID format in the uuid field).
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// DeleteQoSPolicyInONTAP deletes a QoS policy from ONTAP by policy UUID or policy name.
// When the first argument is a transition name (e.g. from TransitionVPGNameFromPoolName), it is not a UUID;
// the activity passes it as Name so ONTAP accepts it. When it is a real UUID (e.g. from CreateQoSPolicyInONTAP),
// it is passed as UUID.
func (a *VolumePerformanceGroupActivity) DeleteQoSPolicyInONTAP(
	ctx context.Context,
	qosPolicyID string,
	vpgName string,
	poolID int64,
	node *models.Node,
) error {
	logger := util.GetLogger(ctx)
	if qosPolicyID == "" && vpgName == "" {
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Deleting QoS policy from ONTAP - policyID: %s, vpgName: %s", qosPolicyID, vpgName))

	svm, err := a.SE.GetSvmForPoolID(ctx, poolID)
	if err != nil {
		logger.Error("Failed to get SVM for QoS policy deletion", "error", err, "pool_id", poolID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider for QoS policy deletion", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	deleteQosParams := vsa.DeleteQoSGroupPolicyParams{SvmName: svm.Name}
	if qosPolicyID != "" {
		if uuidPattern.MatchString(qosPolicyID) {
			deleteQosParams.UUID = qosPolicyID
		} else {
			deleteQosParams.Name = qosPolicyID
		}
	} else {
		logger.Warn("OntapQosPolicyID not set; falling back to name-based QoS policy deletion",
			"vpg_name", vpgName, "pool_id", poolID)
		deleteQosParams.Name = vpgName
	}

	err = provider.DeleteQoSGroupPolicy(deleteQosParams)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			logger.Debug("QoS policy already deleted", "qos_policy_id", qosPolicyID, "vpg_name", vpgName)
			return nil
		}
		logger.Error("Failed to delete QoS policy from ONTAP", "qos_policy_id", qosPolicyID, "vpg_name", vpgName, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Deleted QoS policy from ONTAP - policyID: %s, vpgName: %s", qosPolicyID, vpgName))
	logger.Info("Deleted QoS policy from ONTAP", "qos_policy_id", qosPolicyID, "vpg_name", vpgName)
	return nil
}

// UpdateQoSPolicyInONTAP updates an existing QoS policy in ONTAP (name, throughput, IOPS).
// Uses the existing policy name (OntapQosPolicyID) to find the policy and then updates it.
func (a *VolumePerformanceGroupActivity) UpdateQoSPolicyInONTAP(
	ctx context.Context,
	vpg *datamodel.VolumePerformanceGroup,
	pool *datamodel.PoolView,
	node *models.Node,
	newName string,
	maxThroughputMibps int64,
	maxIops int64,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Updating QoS policy in ONTAP")

	if vpg.OntapQosPolicyID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(utilErrors.NewUserInputValidationErr("VPG has no ONTAP QoS policy ID"))
	}

	svm, err := a.SE.GetSvmForPoolID(ctx, pool.ID)
	if err != nil {
		logger.Error("Failed to get SVM for QoS policy update", "error", err, "pool_id", pool.ID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider for QoS policy update", "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	findParams := vsa.FindQoSGroupPolicyParams{
		UUID:    vpg.OntapQosPolicyID,
		SvmName: svm.Name,
	}
	qosResp, err := provider.FindQoSGroupPolicy(findParams)
	if err != nil {
		customErr := vsaerrors.ExtractCustomError(err)
		if customErr != nil && customErr.IsError(vsaerrors.ErrResourceNotFound) && newName != "" {
			findParams = vsa.FindQoSGroupPolicyParams{
				Name:    newName,
				SvmName: svm.Name,
			}
			qosResp, err = provider.FindQoSGroupPolicy(findParams)
		}
		if err != nil {
			logger.Error("Failed to find QoS policy for update", "policy_uuid", vpg.OntapQosPolicyID, "error", err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	// Only send a new name to ONTAP when it actually changes; otherwise ONTAP treats it as a rename
	// and can fail with "Policy with new name already exists" when the name is unchanged.
	nameToSet := newName
	if nameToSet == qosResp.Name {
		nameToSet = ""
	}
	updateParams := vsa.UpdateQoSGroupPolicyParams{
		UUID:          qosResp.UUID,
		Name:          nameToSet,
		SvmName:       svm.Name,
		MaxThroughput: maxThroughputMibps,
		MaxIOPS:       maxIops,
	}
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Updating QoS policy: %s -> %s", vpg.OntapQosPolicyID, newName))
	if err := provider.UpdateQoSGroupPolicy(updateParams); err != nil {
		logger.Error("Failed to update QoS policy in ONTAP", "error", err, "vpg_name", vpg.Name)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy updated in ONTAP", "policy_name", newName, "vpg_id", vpg.UUID)
	return nil
}

// HardDeleteVPGInDB permanently removes the VPG row from the database.
func (a *VolumePerformanceGroupActivity) HardDeleteVPGInDB(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Hard-deleting VPG from database")
	if err := a.SE.HardDeleteVolumePerformanceGroup(ctx, vpg); err != nil {
		logger.Error("Failed to hard-delete VPG from database", "error", err, "vpg_id", vpg.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Info("VPG hard-deleted from database", "vpg_id", vpg.UUID)
	return nil
}

// UpdateVPGInDB updates the VolumePerformanceGroup row in the database (name, throughput, iops, ontap_qos_policy_id).
func (a *VolumePerformanceGroupActivity) UpdateVPGInDB(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Updating VPG in database")
	if err := a.SE.UpdateVolumePerformanceGroup(ctx, vpg); err != nil {
		logger.Error("Failed to update VPG in database", "error", err, "vpg_id", vpg.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Info("VPG updated in database", "vpg_id", vpg.UUID)
	return nil
}

// UpdateVPGStateInDB updates only the state and state_details of a VPG in the database.
func (a *VolumePerformanceGroupActivity) UpdateVPGStateInDB(ctx context.Context, vpgUUID, state, stateDetails string) error {
	activity.RecordHeartbeat(ctx, "Updating VPG state in database")
	if err := a.SE.UpdateVolumePerformanceGroupState(ctx, vpgUUID, state, stateDetails); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

// ListVolumePerformanceGroupsByPoolID returns all VPGs for a pool (used by manual→auto transition).
func (a *VolumePerformanceGroupActivity) ListVolumePerformanceGroupsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.VolumePerformanceGroup, error) {
	activity.RecordHeartbeat(ctx, "Listing VPGs by pool ID")
	vpgs, err := a.SE.ListVolumePerformanceGroupsByPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return vpgs, nil
}

// RestoreAutoGeneratedVPG re-creates an auto-generated VPG in ONTAP and DB and reassigns volumes to it.
// Used as rollback when manual→auto transition fails after CleanupAutoGeneratedVPG.
func (a *VolumePerformanceGroupActivity) RestoreAutoGeneratedVPG(
	ctx context.Context,
	vpg *datamodel.VolumePerformanceGroup,
	volumes []*datamodel.Volume,
	node *models.Node,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Restoring auto-generated VPG for rollback")

	if vpg == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("vpg is nil"))
	}

	// 1. Create QoS policy in ONTAP
	qosPolicyID, err := a.CreateQoSPolicyInONTAP(ctx, vpg, node)
	if err != nil {
		return err
	}
	vpgCopy := *vpg
	vpgCopy.OntapQosPolicyID = qosPolicyID

	// 2. Create VPG in DB
	createdVPG, err := a.CreateVPGInDB(ctx, &vpgCopy)
	if err != nil {
		logger.Error("Failed to create VPG in DB during restore", "error", err)
		return err
	}

	// 3. Assign each volume to the VPG in ONTAP and DB
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	policyName := vpg.Name
	for _, vol := range volumes {
		if vol.VolumeAttributes == nil || vol.VolumeAttributes.ExternalUUID == "" {
			continue
		}
		// ONTAP volume update expects qos.policy.name; use VPG name (policy name in ONTAP), not the UUID returned from CreateQoSPolicyInONTAP.
		updateVolumeParams := vsa.UpdateVolumeParams{
			UUID:          vol.VolumeAttributes.ExternalUUID,
			QosPolicyName: &policyName,
		}
		if err := provider.UpdateVolume(updateVolumeParams); err != nil {
			logger.Error("Failed to assign QoS to volume during restore", "volumeUUID", vol.UUID, "error", err)
			return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("assign QoS to volume %s during restore: %w", vol.UUID, err))
		}
		updates := map[string]interface{}{"volume_performance_group_id": createdVPG.ID}
		if err := a.SE.UpdateVolumeFields(ctx, vol.UUID, updates); err != nil {
			logger.Error("Failed to update volume VPG in DB during restore", "volumeUUID", vol.UUID, "error", err)
			return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("update volume %s VPG in DB during restore: %w", vol.UUID, err))
		}
	}

	activity.RecordHeartbeat(ctx, "Finished RestoreAutoGeneratedVPG")
	return nil
}

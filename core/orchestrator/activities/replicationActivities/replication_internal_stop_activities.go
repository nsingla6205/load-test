package replicationActivities

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerror "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type InternalStopVolumeReplicationActivity struct {
	SE database.Storage
}

func (j *InternalStopVolumeReplicationActivity) GetReplicationFromDB(ctx context.Context, uuid string) (*datamodel.VolumeReplication, error) {
	se := j.SE

	replication, err := se.GetVolumeReplication(ctx, uuid)
	if err != nil {
		return nil, vsaerror.NewVCPError(vsaerror.ErrDatabaseDataReadError, err)
	}
	return replication, nil
}

// buildSnapmirrorLookupParams builds the ONTAP lookup parameters for a replication record.
func buildSnapmirrorLookupParams(replication *datamodel.VolumeReplication) *vsa.VolumeReplication {
	attributes := replication.ReplicationAttributes
	return &vsa.VolumeReplication{
		ExternalUUID:          attributes.ExternalUUID,
		SourceSVMName:         attributes.SourceSvmName,
		SourceVolumeName:      attributes.SourceVolumeName,
		DestinationSVMName:    attributes.DestinationSvmName,
		DestinationVolumeName: attributes.DestinationVolumeName,
	}
}

// getSnapmirrorForStop fetches the snapmirror relationship for vsaReplication.
func getSnapmirrorForStop(provider vsa.Provider, vsaReplication *vsa.VolumeReplication) (*vsa.VolumeReplication, error) {
	var snapmirror *vsa.VolumeReplication
	var err error
	if vsaReplication.ExternalUUID == "" {
		snapmirror, err = provider.GetVolumeReplicationFromSrcAndDstPath(vsaReplication)
	} else {
		snapmirror, err = provider.GetVolumeReplication(vsaReplication)
	}
	if errors.IsNotFoundErr(err) {
		return nil, nil
	}
	return snapmirror, err
}

func (j *InternalStopVolumeReplicationActivity) BreakVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node, forceStop bool) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider by node", "error", err)
		return nil, vsaerror.WrapAsTemporalApplicationError(err)
	}
	vsaReplication := buildSnapmirrorLookupParams(replication)
	snapmirror, err := getSnapmirrorForStop(provider, vsaReplication)
	if err != nil {
		logger.Error("Failed to get volume replication details", "error", err)
		return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderGetVolumeReplication, err))
	}
	if snapmirror == nil {
		logger.Info("Snapmirror relationship not found in ONTAP, skipping break volume replication",
			"replicationUUID", replication.UUID)
		return vsaReplication, nil
	}
	if snapmirror.RelationshipStatus == datamodel.SnapmirrorRelationshipTransferring {
		if forceStop {
			if snapmirror.TransferUUID != "" {
				abortVolRep := &vsa.VolumeReplication{
					RelationshipID:     snapmirror.RelationshipID,
					TransferUUID:       snapmirror.TransferUUID,
					RelationshipStatus: datamodel.SnapmirrorRelationshipAborted,
				}
				if _, abortErr := provider.AbortVolumeReplication(abortVolRep); abortErr != nil {
					logger.Error("Failed to abort volume replication after break failure", "error", abortErr)
					return nil, vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderAbortVolumeReplication, errors.New("An abort of the active transfer was attempted and failed; please retry.")))
				}
			}
		} else {
			return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrBreakReplicationStateTransferring, errors.New("Replication is in transferring state, cannot stop replication.")))
		}
	}
	if snapmirror.MirrorState == datamodel.OntapUninitialized {
		return snapmirror, nil
	}
	snapmirror.MirrorState = datamodel.OntapBrokenOff
	_, err = provider.BreakVolumeReplication(snapmirror)
	if err != nil {
		logger.Error("Failed to break volume replication", "error", err)
		return nil, vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderBreakVolumeReplication, err))
	}
	return snapmirror, nil
}

func (j *InternalStopVolumeReplicationActivity) AbortVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node, forcestop bool) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	if !forcestop {
		logger.Info("Force is not set to true, skipping abort volume replication")
		return nil, nil
	}
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider by node", "error", err)
		return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrGCPClientInitializationError, err))
	}
	vsaReplication := buildSnapmirrorLookupParams(replication)
	snapmirror, err := getSnapmirrorForStop(provider, vsaReplication)
	if err != nil {
		logger.Error("Failed to get volume replication details", "error", err)
		return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderGetVolumeReplication, err))
	}

	if snapmirror == nil {
		logger.Info("Snapmirror relationship not found in ONTAP, skipping abort volume replication",
			"replicationUUID", replication.UUID)
		return vsaReplication, nil
	}

	if snapmirror.RelationshipStatus != datamodel.SnapmirrorRelationshipTransferring {
		logger.Info("Replication is not in transferring state, skipping abort volume replication")
		return vsaReplication, nil
	}

	if snapmirror.TransferUUID == "" {
		return vsaReplication, nil
	}

	vsaReplication.RelationshipID = snapmirror.RelationshipID
	vsaReplication.TransferUUID = snapmirror.TransferUUID
	vsaReplication.RelationshipStatus = datamodel.SnapmirrorRelationshipAborted
	_, err = provider.AbortVolumeReplication(vsaReplication)
	if err != nil {
		logger.Error("Failed to abort volume replication", "error", err)
		return nil, vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderAbortVolumeReplication, err))
	}
	return vsaReplication, nil
}

func (j *InternalStopVolumeReplicationActivity) GetSnapMirrorFromOntap(ctx context.Context, dbReplication *datamodel.VolumeReplication, node *models.Node) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider by node", "error", err)
		return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrGCPClientInitializationError, err))
	}
	replicationParams := convertToSnapmirrorGetParams(dbReplication, dbReplication.Account.Name)
	ontapRep, err := provider.GetReplicationDetails(ctx, replicationParams)
	if err != nil {
		logger.Errorf("Failed to get replication details from Ontap for replication %s: %v", dbReplication.UUID, err)
		return nil, vsaerror.WrapAsNonRetryableTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrProviderGetVolumeReplication, err))
	}
	return ontapRep, nil
}

func (j *InternalStopVolumeReplicationActivity) UpdateVolumeReplicationStopDetails(ctx context.Context, replication *datamodel.VolumeReplication, vsaReplication *vsa.VolumeReplication) error {
	se := j.SE

	replication.State = datamodel.LifeCycleStateAvailable
	replication.StateDetails = datamodel.LifeCycleStateAvailableDetails
	replication.MirrorState = &vsaReplication.MirrorState
	replication.RelationshipStatus = &vsaReplication.RelationshipStatus
	replication.TotalTransferBytes = vsaReplication.TotalTransferBytes
	replication.TotalTransferTimeSecs = vsaReplication.TotalTransferTimeSecs
	replication.LastTransferSize = vsaReplication.LastTransferSize
	replication.LastTransferError = vsaReplication.LastTransferError
	replication.LastTransferDuration = vsaReplication.LastTransferDuration
	replication.LastTransferEndTime = vsaReplication.LastTransferEndTime
	replication.LagTime = vsaReplication.LagTime
	replication.LastUpdatedFromOntap = time.Now()
	replication.ProgressLastUpdated = &replication.LastUpdatedFromOntap

	if err := se.UpdateVolumeReplication(ctx, replication); err != nil {
		return vsaerror.NewVCPError(vsaerror.ErrDatabaseDataUpdateError, err)
	}

	return nil
}

func (j *InternalStopVolumeReplicationActivity) UpdateVolumeToNonDPVolume(ctx context.Context, replication *datamodel.VolumeReplication) error {
	se := j.SE
	updates := make(map[string]interface{})
	if replication.Volume.VolumeAttributes != nil {
		replication.Volume.VolumeAttributes.IsDataProtection = false
	}
	updates["volume_attributes"] = replication.Volume.VolumeAttributes
	err := se.UpdateVolumeFields(ctx, replication.ReplicationAttributes.DestinationVolumeUUID, updates)
	if err != nil {
		return vsaerror.NewVCPError(vsaerror.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// UpdateQuotaRulesStateToError updates the state of failed quota rules to ERROR
// This is called after break replication when quota rule creation fails
// Returns error immediately on first failure (fail-fast approach)
func (j *InternalStopVolumeReplicationActivity) UpdateQuotaRulesStateToError(
	ctx context.Context,
	failedQuotaRules []*datamodel.QuotaRule,
) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	logger.Infof("Updating %d failed quota rules to ERROR state", len(failedQuotaRules))

	for _, quotaRule := range failedQuotaRules {
		// Fetch current quota rule from DB
		currentQuotaRule, err := se.GetQuotaRuleByUUID(ctx, quotaRule.UUID, quotaRule.AccountID)
		if err != nil {
			logger.Errorf("Failed to fetch quota rule for state update: uuid=%s, error=%v", quotaRule.UUID, err)
			return vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrDatabaseDataReadError, err))
		}

		// Update state to ERROR
		currentQuotaRule.State = datamodel.LifeCycleStateError
		currentQuotaRule.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
		currentQuotaRule.UpdatedAt = time.Now()

		_, err = se.UpdateQuotaRule(ctx, currentQuotaRule)
		if err != nil {
			logger.Errorf("Failed to update quota rule state to ERROR: uuid=%s, error=%v", quotaRule.UUID, err)
			return vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrDatabaseDataUpdateError, err))
		}

		logger.Infof("Successfully updated quota rule to ERROR state: uuid=%s, name=%s", quotaRule.UUID, quotaRule.Name)
	}

	logger.Infof("Successfully updated all %d failed quota rules to ERROR state", len(failedQuotaRules))
	return nil
}

// UpdateVolumeReplicationForQuotaError updates the volume replication state to ERROR
// when quota rule creation fails after successful break operation
func (j *InternalStopVolumeReplicationActivity) UpdateVolumeReplicationForQuotaError(
	ctx context.Context,
	replication *datamodel.VolumeReplication,
) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	logger.Infof("Updating volume replication to ERROR state due to quota rule failures: uuid=%s", replication.UUID)

	replication.State = datamodel.LifeCycleStateError
	replication.StateDetails = datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure

	if err := se.UpdateVolumeReplication(ctx, replication); err != nil {
		logger.Errorf("Failed to update volume replication state: uuid=%s, error=%v", replication.UUID, err)
		return vsaerror.WrapAsTemporalApplicationError(vsaerror.NewVCPError(vsaerror.ErrDatabaseDataUpdateError, err))
	}

	logger.Infof("Successfully updated volume replication to ERROR state: uuid=%s", replication.UUID)
	return nil
}

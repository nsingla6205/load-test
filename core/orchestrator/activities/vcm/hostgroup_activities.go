package vcm

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// HostGroupActivity performs ONTAP igroup operations for VCM workflows.
type HostGroupActivity struct{}

// ValidateHostGroupsExistInONTAP verifies each referenced host group (igroup) exists on the target SVM.
func (a HostGroupActivity) ValidateHostGroupsExistInONTAP(ctx context.Context, hostGroupNames []string, svmName string, node *models.Node) error {
	orchestrator.RecordHeartbeat(ctx, "Starting ValidateHostGroupsExistInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	for _, hostGroupName := range hostGroupNames {
		orchestrator.RecordHeartbeat(ctx, "Checking host group exists in ONTAP", "hostGroup", hostGroupName, "svm", svmName)
		exists, _, err := provider.IgroupExists(hostGroupName, &svmName)
		if err != nil {
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		if !exists {
			return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrAllHostGroupsNotFoundError, fmt.Errorf("host group %q not found on SVM %q", hostGroupName, svmName)))
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Finished ValidateHostGroupsExistInONTAP activity")
	return nil
}

// CreateIgroup creates an igroup in ONTAP. If the igroup already exists with the same osType and IQNs, the call succeeds idempotently.
func (HostGroupActivity) CreateIgroup(ctx context.Context, svmName string, host *common.HostParams, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM CreateIgroup activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	orchestrator.RecordHeartbeat(ctx, "Checking if igroup exists in ONTAP")
	igroupExists, existingIgroup, err := provider.IgroupExists(host.HostName, &svmName)
	if err != nil {
		return err
	}
	if igroupExists {
		if vsa.IgroupMatchesCreateRequest(existingIgroup, host.OsType, host.HostIQNs) {
			logger.Debug("Igroup already exists with matching configuration", "name", host.HostName)
			orchestrator.RecordHeartbeat(ctx, "Finished VCM CreateIgroup activity")
			return nil
		}
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHostGroupAlreadyExists,
			fmt.Errorf("host group %q already exists with different configuration", host.HostName)))
	}

	orchestrator.RecordHeartbeat(ctx, "Creating igroup in ONTAP")
	_, err = provider.IgroupCreate(vsa.IgroupCreateParams{
		IgroupName: host.HostName,
		SvmName:    svmName,
		OsType:     host.OsType,
		Initiator:  host.HostIQNs,
	})
	if err != nil {
		return err
	}
	logger.Debug("Igroup created successfully", "name", host.HostName)
	orchestrator.RecordHeartbeat(ctx, "Finished VCM CreateIgroup activity")
	return nil
}

// UpdateIgroup syncs an existing ONTAP igroup's initiator list to desiredHosts.
func (HostGroupActivity) UpdateIgroup(ctx context.Context, igroupName string, desiredHosts []string, svmName string, node *models.Node) (string, error) {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM UpdateIgroup activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}

	orchestrator.RecordHeartbeat(ctx, "Syncing igroup initiators in ONTAP")
	found, osType, err := vsa.SyncIgroupInitiators(provider, igroupName, desiredHosts, &svmName)
	if err != nil {
		return "", vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !found {
		return "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrHostGroupNotFound, fmt.Errorf("igroup %q not found", igroupName)))
	}

	logger.Debug("Igroup updated successfully", "name", igroupName, "svm", svmName)
	orchestrator.RecordHeartbeat(ctx, "Finished VCM UpdateIgroup activity")
	return osType, nil
}

// DeleteIgroup deletes an ONTAP initiator group by name within the target SVM.
func (HostGroupActivity) DeleteIgroup(ctx context.Context, igroupName string, svmName string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM DeleteIgroup activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	orchestrator.RecordHeartbeat(ctx, "Fetching Igroup details")
	igroup, err := provider.IgroupGet(&igroupName, &svmName)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			logger.Debugf("Igroup %s is already deleted, skipping", igroupName)
			orchestrator.RecordHeartbeat(ctx, "Finished VCM DeleteIgroup activity")
			return nil
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if igroup != nil && igroup.UUID != nil {
		err = provider.IgroupDelete(*igroup.UUID)
		if err != nil {
			var customErr *vsaerrors.CustomError
			if vsaerrors.As(err, &customErr) && customErr.TrackingID == vsaerrors.ErrHostGroupHasLUNMaps {
				return vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
			}
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Debug("Igroup deleted successfully", "name", igroupName)
	} else {
		logger.Debugf("Igroup %s not found", igroupName)
	}
	orchestrator.RecordHeartbeat(ctx, "Finished VCM DeleteIgroup activity")
	return nil
}

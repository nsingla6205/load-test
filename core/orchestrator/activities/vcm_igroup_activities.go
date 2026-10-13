package activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

// HostGroupActivity performs ONTAP igroup operations for VCM workflows.
type HostGroupActivity struct{}

// ValidateHostGroupsExistInONTAP verifies each referenced host group (igroup) exists on the target SVM.
func (a HostGroupActivity) ValidateHostGroupsExistInONTAP(ctx context.Context, hostGroupNames []string, svmName string, node *models.Node) error {
	orchestrator.RecordHeartbeat(ctx, "Starting ValidateHostGroupsExistInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return apperror.WrapAsAppError(err, false)
	}

	for _, hostGroupName := range hostGroupNames {
		orchestrator.RecordHeartbeat(ctx, "Checking host group exists in ONTAP", "hostGroup", hostGroupName, "svm", svmName)
		exists, _, err := provider.IgroupExists(hostGroupName, &svmName)
		if err != nil {
			return apperror.WrapAsAppError(err, false)
		}
		if !exists {
			return apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrAllHostGroupsNotFoundError, fmt.Errorf("host group %q not found on SVM %q", hostGroupName, svmName)), false)
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Finished ValidateHostGroupsExistInONTAP activity")
	return nil
}

// CreateIgroup creates an igroup in ONTAP and returns its UUID.
// If the igroup already exists with the same osType and IQNs, the call succeeds idempotently and returns the existing UUID.
func (HostGroupActivity) CreateIgroup(ctx context.Context, svmName string, host *common.HostParams, node *models.Node) (string, error) {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM CreateIgroup activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return "", apperror.WrapAsAppError(err, false)
	}

	orchestrator.RecordHeartbeat(ctx, "Checking if igroup exists in ONTAP")
	igroupExists, existingIgroup, err := provider.IgroupExists(host.HostName, &svmName)
	if err != nil {
		return "", err
	}
	if igroupExists {
		if vsa.IgroupMatchesCreateRequest(existingIgroup, host.OsType, host.HostIQNs) {
			if existingIgroup.UUID == nil || strings.TrimSpace(*existingIgroup.UUID) == "" {
				return "", apperror.WrapAsAppError(fmt.Errorf("existing igroup %q is missing uuid", host.HostName), false)
			}
			igroupUUID := *existingIgroup.UUID
			logger.Debug("Igroup already exists with matching configuration", "name", host.HostName, "uuid", igroupUUID)
			orchestrator.RecordHeartbeat(ctx, "Finished VCM CreateIgroup activity")
			return igroupUUID, nil
		}
		return "", apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrHostGroupAlreadyExists,
			fmt.Errorf("host group %q already exists with different configuration", host.HostName)), true)
	}

	orchestrator.RecordHeartbeat(ctx, "Creating igroup in ONTAP")
	igroupUUID, err := provider.IgroupCreate(vsa.IgroupCreateParams{
		IgroupName: host.HostName,
		SvmName:    svmName,
		OsType:     host.OsType,
		Initiator:  host.HostIQNs,
	})
	if err != nil {
		return "", err
	}
	logger.Debug("Igroup created successfully", "name", host.HostName, "uuid", igroupUUID)
	orchestrator.RecordHeartbeat(ctx, "Finished VCM CreateIgroup activity")
	return igroupUUID, nil
}

// UpdateIgroup syncs an existing ONTAP igroup's initiator list to desiredHosts.
func (HostGroupActivity) UpdateIgroup(ctx context.Context, igroupUUID string, desiredHosts []string, svmName string, node *models.Node) (models.HostGroup, error) {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM UpdateIgroup activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return models.HostGroup{}, apperror.WrapAsAppError(err, false)
	}

	orchestrator.RecordHeartbeat(ctx, "Syncing igroup initiators in ONTAP")
	name, osType, err := vsa.SyncIgroupInitiators(provider, igroupUUID, desiredHosts, svmName)
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			return models.HostGroup{}, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrHostGroupNotFound, fmt.Errorf("igroup %q not found", igroupUUID)), true)
		}
		return models.HostGroup{}, apperror.WrapAsAppError(err, false)
	}

	logger.Debug("Igroup updated successfully", "uuid", igroupUUID, "name", name, "svm", svmName)
	orchestrator.RecordHeartbeat(ctx, "Finished VCM UpdateIgroup activity")
	return models.HostGroup{Name: name, OSType: osType}, nil
}

// DeleteIgroup deletes an ONTAP initiator group by UUID from the deployment SVM.
func (HostGroupActivity) DeleteIgroup(ctx context.Context, igroupUUID string, svmName string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting VCM DeleteIgroup activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return apperror.WrapAsAppError(err, false)
	}

	// Verify the igroup belongs to the deployment SVM before deleting it by cluster-wide UUID.
	orchestrator.RecordHeartbeat(ctx, "Checking igroup exists on SVM", "uuid", igroupUUID, "svm", svmName)
	igroup, err := provider.IgroupGet(vsa.IgroupGetParams{UUID: igroupUUID, SvmName: svmName})
	if err != nil {
		if utilErrors.IsNotFoundErr(err) {
			logger.Debug("Igroup not found on SVM; delete is a no-op", "uuid", igroupUUID, "svm", svmName)
			orchestrator.RecordHeartbeat(ctx, "Finished VCM DeleteIgroup activity")
			return nil
		}
		return apperror.WrapAsAppError(err, false)
	}
	if igroup == nil {
		logger.Debug("Igroup not found on SVM; delete is a no-op", "uuid", igroupUUID, "svm", svmName)
		orchestrator.RecordHeartbeat(ctx, "Finished VCM DeleteIgroup activity")
		return nil
	}

	orchestrator.RecordHeartbeat(ctx, "Deleting igroup in ONTAP")
	err = provider.IgroupDelete(igroupUUID)
	if err != nil {
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) && customErr.TrackingID == vsaerrors.ErrHostGroupHasLUNMaps {
			return apperror.WrapAsAppError(err, true)
		}
		return apperror.WrapAsAppError(err, false)
	}
	logger.Debug("Igroup deleted successfully", "uuid", igroupUUID, "svm", svmName)
	orchestrator.RecordHeartbeat(ctx, "Finished VCM DeleteIgroup activity")
	return nil
}

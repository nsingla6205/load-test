package vcm_workflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const (
	PhasePre  = "pre"
	PhasePost = "post"
)

// VcmVolumeChildWorkflowParams carries inputs for VCM volume create child workflows.
type VcmVolumeChildWorkflowParams struct {
	Volume            *datamodel.Volume
	Node              *models.Node
	VolCreateResponse *vsa.VolumeResponse
	HostGroupNames    []string
}

// selectVcmVolumeChildWorkflow selects the VCM child workflow for the given protocol and phase.
func selectVcmVolumeChildWorkflow(protocols []string, phase string) (interface{}, error) {
	if utils.IsSanProtocols(protocols) {
		switch phase {
		case PhasePre:
			return VcmPreBlockVolumeWorkflow, nil
		case PhasePost:
			return VcmPostBlockVolumeWorkflow, nil
		default:
			return nil, fmt.Errorf("invalid phase: %s", phase)
		}
	}
	if utils.IsNasProtocols(protocols) {
		switch phase {
		case PhasePre:
			return VcmPreFileVolumeWorkflow, nil
		case PhasePost:
			hasNFS := utils.IsNFSProtocols(protocols)
			hasSMB := utils.IsSMBProtocols(protocols)
			if hasNFS && hasSMB {
				return []interface{}{VcmPostFileVolumeWorkflowForSMB, VcmPostFileVolumeWorkflow}, nil
			}
			if hasSMB {
				return VcmPostFileVolumeWorkflowForSMB, nil
			}
			return VcmPostFileVolumeWorkflow, nil
		default:
			return nil, fmt.Errorf("invalid phase: %s", phase)
		}
	}
	return nil, fmt.Errorf("unsupported or unspecified protocol: %v", protocols)
}

// VcmPreBlockVolumeWorkflow validates referenced host groups and prepares block volume creation.
func VcmPreBlockVolumeWorkflow(ctx *orchestrator.Context, params VcmVolumeChildWorkflowParams) (*datamodel.Volume, error) {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())
	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.ValidateHostGroupsExistInONTAP, params.HostGroupNames, params.Volume.Svm.Name, params.Node).Get(activityCtx, nil); err != nil {
		return nil, err
	}

	return params.Volume, nil
}

// VcmPreFileVolumeWorkflow handles pre-provisioning for VCM file volumes.
func VcmPreFileVolumeWorkflow(ctx *orchestrator.Context, params VcmVolumeChildWorkflowParams) (*datamodel.Volume, error) {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())

	var isOntapClusterHealthy *bool
	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.GetOntapClusterHealth, params.Node).Get(activityCtx, &isOntapClusterHealthy); err != nil {
		return nil, err
	}
	if isOntapClusterHealthy == nil || !*isOntapClusterHealthy {
		return nil, fmt.Errorf("ONTAP cluster is down")
	}

	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateExportPolicyInOntap, params.Volume, params.Node).Get(activityCtx, nil); err != nil {
		return nil, err
	}

	return params.Volume, nil
}

// VcmPostBlockVolumeWorkflow handles post-provisioning for VCM block volumes.
func VcmPostBlockVolumeWorkflow(ctx *orchestrator.Context, params VcmVolumeChildWorkflowParams) (*datamodel.Volume, error) {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())

	var lun vsa.LunResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateLun, params.Volume, params.Node, params.VolCreateResponse.AvailableSpace).Get(activityCtx, &lun); err != nil {
		return nil, err
	}

	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateLunMap, params.Volume, &common.CreateLunMapParams{
		LunName:   lun.Name,
		SvmName:   params.Volume.Svm.Name,
		HostNames: params.HostGroupNames,
	}, params.Node).Get(activityCtx, nil); err != nil {
		return nil, err
	}

	if params.Volume.VolumeAttributes.BlockDevices != nil && len(*params.Volume.VolumeAttributes.BlockDevices) > 0 {
		blockDevices := *params.Volume.VolumeAttributes.BlockDevices
		for i := range blockDevices {
			blockDevices[i].Name = utils.ExtractLunNameFromPath(lun.Name)
			blockDevices[i].Identifier = lun.SerialNumber
			blockDevices[i].Size = lun.Size
			blockDevices[i].LunUUID = lun.ExternalUUID
		}
		params.Volume.VolumeAttributes.BlockDevices = &blockDevices
	}

	return params.Volume, nil
}

// VcmPostFileVolumeWorkflow handles post-provisioning for VCM NFS file volumes.
// TODO(VCM): implement NFS post steps (AD/LDAP-dependent flows) when VCM pool support lands.
func VcmPostFileVolumeWorkflow(ctx *orchestrator.Context, params VcmVolumeChildWorkflowParams) (*datamodel.Volume, error) {
	return params.Volume, nil
}

// VcmPostFileVolumeWorkflowForSMB handles post-provisioning for VCM SMB file volumes.
// TODO(VCM): implement SMB share creation when VCM SMB flows are enabled.
func VcmPostFileVolumeWorkflowForSMB(ctx *orchestrator.Context, params VcmVolumeChildWorkflowParams) (*datamodel.Volume, error) {
	return params.Volume, nil
}

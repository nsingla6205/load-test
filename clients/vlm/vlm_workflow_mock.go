package vlm

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/workflow"
)

type VSAClientWorkflowManagerMock struct {
}

func (vlmManager *VSAClientWorkflowManagerMock) ValidateClusterHealth(ctx workflow.Context, validateClusterHealthRequest *ValidateClusterHealthRequest) error {
	logger := util.GetLogger(ctx)
	logger.Info("Mock ValidateClusterHealth")
	return nil
}

func (vlmManager *VSAClientWorkflowManagerMock) ClusterPowerOp(ctx workflow.Context, clusterPowerOpRequest *ClusterPowerOpReq) error {
	logger := util.GetLogger(ctx)
	logger.Info("Mock ClusterPowerOp")
	return nil
}

func (vlmManager *VSAClientWorkflowManagerMock) GetClusterZiZsDetails(ctx workflow.Context, req *GetResourceInfoReq) (*GetResourceInfoResp, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock GetClusterZiZsDetails")

	// Create a mock response with sample compliance data
	mockResponse := &GetResourceInfoResp{
		ProjectID:    req.ProjectID,
		DeploymentID: req.DeploymentID,
		ResourceInfo: ResourceInformation{
			GCPRI: map[string][]GCPResourceInformation{
				"pool": {
					{
						SatisfiesPzi: true,  // Mock ZI compliance
						SatisfiesPzs: false, // Mock ZS compliance
						AssetType:    "pool",
						AssetLink:    "mock-asset-link",
					},
				},
			},
		},
	}

	return mockResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) CreateVSAClusterDeployment(ctx workflow.Context, createVSAClusterDeploymentRequest *CreateVSAClusterDeploymentRequest, taskQueue string) (*CreateVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock CreateVSAClusterDeployment", "taskQueue", taskQueue)
	createVSAClusterDeploymentResponse := &CreateVSAClusterDeploymentResponse{
		VLMConfig: createVSAClusterDeploymentRequest.VLMConfig,
	}

	return createVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) CreateVSASVM(ctx workflow.Context, createSVMRequest *CreateSVMRequest) (*CreateSVMResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock CreateVSASVM")
	createSVMResponse := &CreateSVMResponse{
		VLMConfig: createSVMRequest.VLMConfig,
	}

	return createSVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) DeleteVSASVM(ctx workflow.Context, deleteSVMRequest *DeleteSVMRequest) (*DeleteSVMResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock DeleteVSASVM")
	deleteSVMResponse := &DeleteSVMResponse{
		VLMConfig: deleteSVMRequest.VLMConfig,
	}

	return deleteSVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) ModifyVSASVMWorkflow(ctx workflow.Context, req *ModifySVMRequest) (*ModifySVMResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock ModifyVSASVMWorkflow")
	modifySVMResponse := &ModifySVMResponse{
		VLMConfig: req.VLMConfig,
	}

	return modifySVMResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) DeleteVSAClusterDeployment(ctx workflow.Context, deleteVSAClusterDeploymentRequest *DeleteVSAClusterDeploymentRequest, ontapVersion string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Mock DeleteVSAClusterDeployment")
	return nil
}

func (vlmManager *VSAClientWorkflowManagerMock) UpdateVSAClusterDeployment(ctx workflow.Context, updateVSAClusterDeploymentRequest *UpdateVSAClusterDeploymentRequest, ontapVersion string) (*UpdateVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock UpdateVSAClusterDeployment")
	updateVSAClusterDeploymentResponse := &UpdateVSAClusterDeploymentResponse{
		VLMConfig:    updateVSAClusterDeploymentRequest.VLMConfig,
		UpdateStatus: DeploymentUpdateStatus{},
	}

	return updateVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) UpgradeVSAClusterDeployment(ctx workflow.Context, upgradeVSAClusterDeploymentRequest *UpdateVSAClusterDeploymentRequest, ontapVersion string) (*UpgradeVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock UpgradeVSAClusterDeployment")
	upgradeVSAClusterDeploymentResponse := &UpgradeVSAClusterDeploymentResponse{
		VLMConfig:    upgradeVSAClusterDeploymentRequest.VLMConfig,
		OntapVersion: upgradeVSAClusterDeploymentRequest.OntapUpgrade.OntapUpgradeTargetImageVersion,
	}

	return upgradeVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) UpgradeVSAClusterDeploymentWorkflow(ctx workflow.Context, req *UpdateVSAClusterDeploymentRequest) (*UpgradeVSAClusterDeploymentResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock UpgradeVSAClusterDeploymentWorkflow")
	upgradeVSAClusterDeploymentResponse := &UpgradeVSAClusterDeploymentResponse{
		VLMConfig:    req.VLMConfig,
		OntapVersion: req.OntapUpgrade.OntapUpgradeTargetImageVersion,
	}

	return upgradeVSAClusterDeploymentResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) UpgradeVSAMediatorWorkflow(ctx workflow.Context, req *UpdateMediatorRequest) (*UpdateMediatorResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock UpgradeVSAMediatorWorkflow")
	upgradeVSAMediatorResponse := &UpdateMediatorResponse{
		VLMConfig: req.VLMConfig,
	}

	return upgradeVSAMediatorResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) UpdateLicenseWorkflow(ctx workflow.Context, req *UpdateLicenseRequest) error {
	logger := util.GetLogger(ctx)
	logger.Info("Mock UpdateLicenseWorkflow", "vsaManagementIP", req.VSAManagementIP)
	return nil
}

func (vlmManager *VSAClientWorkflowManagerMock) CreateVSAExpertModeUser(ctx workflow.Context, createVSAExpertModeUserRequest *OntapExpertModeUserConfig) (OntapExpertModeUserResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock GetVSAClusterDeployment")
	return OntapExpertModeUserResponse{}, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) ZoneSwitch(ctx workflow.Context, req *ZoneSwitchRequest) (*ZoneSwitchResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock ZoneSwitch")
	zoneSwitchResponse := &ZoneSwitchResponse{}
	return zoneSwitchResponse, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) MaintenanceWorkflow(ctx workflow.Context, req *MaintenanceRequest) (*MaintenanceResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock Maintenance")
	resp := &MaintenanceResponse{VLMConfig: req.VLMConfig}
	for _, target := range req.Targets {
		for _, nodeName := range target.NodeNames {
			resp.Results = append(resp.Results, PairMaintenanceResult{
				HaPairIndex: target.HaPairIndex,
				NodeName:    nodeName,
				Succeeded:   true,
			})
		}
	}
	return resp, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) VSAMediatorMaintenanceWorkflow(ctx workflow.Context, req *MediatorMaintenanceRequest) (*VLMConfig, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock VSAMediatorMaintenance")
	updatedConfig := req.VLMConfig
	return &updatedConfig, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) VSAMediatorITCWorkflow(ctx workflow.Context, req *MediatorMaintenanceRequest) (*VLMConfig, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock VSAMediatorITC")
	updatedConfig := req.VLMConfig
	return &updatedConfig, nil
}

func (vlmManager *VSAClientWorkflowManagerMock) RotateFabricPoolKeys(ctx workflow.Context, req *RotateFabricPoolKeysRequest) (*RotateFabricPoolKeysResponse, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Mock RotateFabricPoolKeys")
	resp := &RotateFabricPoolKeysResponse{VLMConfig: req.VLMConfig}
	ociCfg, err := resp.VLMConfig.Deployment.ProviderConfig.AsOCI()
	if err != nil {
		return nil, fmt.Errorf("mock RotateFabricPoolKeys: %w", err)
	}
	ociCfg.FabricPoolConfig.SecretOcid = req.NewSecretOcid
	resp.VLMConfig.Deployment.ProviderConfig = ProviderConfigWrapper{ProviderConfig: ociCfg}
	return resp, nil
}

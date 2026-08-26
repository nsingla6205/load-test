// This file mirrors the VLM GetVSAClusterDeployment workflow contract, matching
// lifecycle-manager/pkg/vlmconfig.
package vlm

import "time"

// GetVSAClusterDeploymentWorkflowName is the registered name of the VLM GetVSAClusterDeployment workflow.
const GetVSAClusterDeploymentWorkflowName = "vlm.GetVSAClusterDeploymentWorkflow"

// GetVSAClusterDeploymentWorkflowExecutionTimeout bounds the Get child workflow execution.
const GetVSAClusterDeploymentWorkflowExecutionTimeout = 15 * time.Minute

// GetVSAClusterDeploymentRequest is the input to the VLM GetVSAClusterDeployment workflow.
type GetVSAClusterDeploymentRequest struct {
	DeploymentID     string           `json:"deployment_id"`
	CloudProvider    string           `json:"cloud_provider"`
	ProjectID        string           `json:"project_id"`
	Region           string           `json:"region"`
	Zone1            string           `json:"zone1"`
	Zone2            string           `json:"zone2"`
	MediatorZone     string           `json:"mediator_zone"`
	DeploymentType   string           `json:"deployment_type"`
	OntapCredentials OntapCredentials `json:"ontap_credentials"`
}

// GetVSAClusterDeploymentResponse is the VLM GetVSAClusterDeployment result.
type GetVSAClusterDeploymentResponse struct {
	VLMConfig VLMConfig `json:"vlm_config"`
}

package vlm

import (
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const enableVlmWorkflowTimeoutOverridesEnv = "ENABLE_VLM_WORKFLOW_TIMEOUT_OVERRIDES"

var applyWorkflowTimeoutOverridesOnce sync.Once

func init() {
	// Apply once at process start so VLM child WorkflowExecutionTimeout values
	// pick up ConfigMap/env overrides. Worker restart is required after ConfigMap changes.
	applyWorkflowTimeoutOverrides()
}

// applyWorkflowTimeoutOverrides mutates WorkflowExecutionTimeoutMap from environment variables.
// It is a no-op unless ENABLE_VLM_WORKFLOW_TIMEOUT_OVERRIDES is true (OCI chart: true).
// Unset or false (GCP) leaves WorkflowExecutionTimeoutMap unchanged.
//
// Overrides are applied at most once per process via sync.Once.
//
// Env names match existing ConfigMap keys (…_WF_TIMEOUT_MINUTES). Empty or unset
// ConfigMap values keep the hardcoded map in config.go. Invalid (≤0) env values
// are ignored and the map default is used instead.
func applyWorkflowTimeoutOverrides() {
	if !env.GetBool(enableVlmWorkflowTimeoutOverridesEnv, false) {
		return
	}
	applyWorkflowTimeoutOverridesOnce.Do(func() {
		WorkflowExecutionTimeoutMap["DefaultWorkflowExecutionTimeout"] = timeoutFromEnv("VLM_DEFAULT_WORKFLOW_EXECUTION_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap["DefaultWorkflowExecutionTimeout"])
		WorkflowExecutionTimeoutMap[CreateVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[CreateVSAClusterDeploymentWorkflowName])
		WorkflowExecutionTimeoutMap[CreateVSASVMWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_SVM_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[CreateVSASVMWorkflowName])
		WorkflowExecutionTimeoutMap[ModifyVSASVMWorkflowName] = timeoutFromEnv("VLM_MODIFY_VSA_SVM_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[ModifyVSASVMWorkflowName])
		WorkflowExecutionTimeoutMap[DeleteVSASVMWorkflowName] = timeoutFromEnv("VLM_DELETE_VSA_SVM_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[DeleteVSASVMWorkflowName])
		WorkflowExecutionTimeoutMap[DeleteVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_DELETE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[DeleteVSAClusterDeploymentWorkflowName])
		WorkflowExecutionTimeoutMap[UpdateVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_UPDATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[UpdateVSAClusterDeploymentWorkflowName])
		WorkflowExecutionTimeoutMap[UpgradeVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_UPGRADE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[UpgradeVSAClusterDeploymentWorkflowName])
		WorkflowExecutionTimeoutMap[ClusterPowerCycleWorkflowName] = timeoutFromEnv("VLM_CLUSTER_POWER_OP_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[ClusterPowerCycleWorkflowName])
		WorkflowExecutionTimeoutMap[UpdateVSAMediatorWorkflowName] = timeoutFromEnv("VLM_UPDATE_VSA_MEDIATOR_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[UpdateVSAMediatorWorkflowName])
		WorkflowExecutionTimeoutMap[UpdateLicenseWorkflowName] = timeoutFromEnv("VLM_UPDATE_LICENSE_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[UpdateLicenseWorkflowName])
		WorkflowExecutionTimeoutMap[CreateVSAExpertModeUserWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_EXPERT_MODE_USER_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[CreateVSAExpertModeUserWorkflowName])
		WorkflowExecutionTimeoutMap[RotateFabricPoolKeysWorkflowName] = timeoutFromEnv("VLM_ROTATE_FABRIC_POOL_KEYS_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[RotateFabricPoolKeysWorkflowName])
		WorkflowExecutionTimeoutMap[MaintenanceWorkflowName] = timeoutFromEnv("VLM_MAINTENANCE_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[MaintenanceWorkflowName])
		WorkflowExecutionTimeoutMap[MediatorMaintenanceRebootMigrateWorkflowName] = timeoutFromEnv("VLM_MEDIATOR_MAINTENANCE_REBOOT_MIGRATE_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[MediatorMaintenanceRebootMigrateWorkflowName])
		WorkflowExecutionTimeoutMap[MediatorMaintenanceRecreateWorkflowName] = timeoutFromEnv("VLM_MEDIATOR_MAINTENANCE_RECREATE_WF_TIMEOUT_MINUTES", WorkflowExecutionTimeoutMap[MediatorMaintenanceRecreateWorkflowName])
	})
}

// timeoutFromEnv reads minutes from env; unset, empty, or invalid (≤0) values keep defaultTimeout
// from WorkflowExecutionTimeoutMap in config.go.
func timeoutFromEnv(envKey string, defaultTimeout time.Duration) time.Duration {
	n := env.GetInt(envKey, int(defaultTimeout/time.Minute))
	if n <= 0 {
		return defaultTimeout
	}
	return time.Duration(n) * time.Minute
}

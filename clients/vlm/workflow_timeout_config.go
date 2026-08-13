package vlm

import (
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

var applyWorkflowTimeoutOverridesOnce sync.Once

func init() {
	// Apply once at process start so VLM child WorkflowExecutionTimeout values
	// pick up ConfigMap/env overrides. Worker restart is required after ConfigMap changes.
	applyWorkflowTimeoutOverrides()
}

// applyWorkflowTimeoutOverrides mutates WorkflowExecutionTimeoutMap from environment variables.
// It is safe to call multiple times; overrides are applied at most once per process unless
// resetWorkflowTimeoutOverridesForTest is used in tests.
//
// Env names match existing ConfigMap keys (…_WF_TIMEOUT_MINUTES). Defaults match the
// hardcoded map in config.go so a binary-only deploy does not change timeouts.
// Invalid (≤0) env values are ignored and the default is used instead.
func applyWorkflowTimeoutOverrides() {
	applyWorkflowTimeoutOverridesOnce.Do(func() {
		WorkflowExecutionTimeoutMap["DefaultWorkflowExecutionTimeout"] = timeoutFromEnv("VLM_DEFAULT_WORKFLOW_EXECUTION_TIMEOUT_MINUTES", 10)
		WorkflowExecutionTimeoutMap[CreateVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[CreateVSASVMWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_SVM_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[ModifyVSASVMWorkflowName] = timeoutFromEnv("VLM_MODIFY_VSA_SVM_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[DeleteVSASVMWorkflowName] = timeoutFromEnv("VLM_DELETE_VSA_SVM_WF_TIMEOUT_MINUTES", 25)
		WorkflowExecutionTimeoutMap[DeleteVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_DELETE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[UpdateVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_UPDATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", 120)
		WorkflowExecutionTimeoutMap[UpgradeVSAClusterDeploymentWorkflowName] = timeoutFromEnv("VLM_UPGRADE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES", 300)
		WorkflowExecutionTimeoutMap[ClusterPowerCycleWorkflowName] = timeoutFromEnv("VLM_CLUSTER_POWER_OP_WF_TIMEOUT_MINUTES", 40)
		WorkflowExecutionTimeoutMap[UpdateVSAMediatorWorkflowName] = timeoutFromEnv("VLM_UPDATE_VSA_MEDIATOR_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[UpdateLicenseWorkflowName] = timeoutFromEnv("VLM_UPDATE_LICENSE_WF_TIMEOUT_MINUTES", 10)
		WorkflowExecutionTimeoutMap[CreateVSAExpertModeUserWorkflowName] = timeoutFromEnv("VLM_CREATE_VSA_EXPERT_MODE_USER_WF_TIMEOUT_MINUTES", 45)
		WorkflowExecutionTimeoutMap[RotateFabricPoolKeysWorkflowName] = timeoutFromEnv("VLM_ROTATE_FABRIC_POOL_KEYS_WF_TIMEOUT_MINUTES", 10)
		WorkflowExecutionTimeoutMap[MaintenanceWorkflowName] = timeoutFromEnv("VLM_MAINTENANCE_WF_TIMEOUT_MINUTES", 120)
		WorkflowExecutionTimeoutMap[MediatorMaintenanceRebootMigrateWorkflowName] = timeoutFromEnv("VLM_MEDIATOR_MAINTENANCE_REBOOT_MIGRATE_WF_TIMEOUT_MINUTES", 30)
		WorkflowExecutionTimeoutMap[MediatorMaintenanceRecreateWorkflowName] = timeoutFromEnv("VLM_MEDIATOR_MAINTENANCE_RECREATE_WF_TIMEOUT_MINUTES", 30)
	})
}

// timeoutFromEnv reads minutes from env; invalid (≤0) values fall back to defaultMinutes.
func timeoutFromEnv(envKey string, defaultMinutes int) time.Duration {
	n := env.GetInt(envKey, defaultMinutes)
	if n <= 0 {
		n = defaultMinutes
	}
	return time.Duration(n) * time.Minute
}

// resetWorkflowTimeoutOverridesForTest clears the sync.Once guard so tests can re-apply overrides
// after changing environment variables. Test-only.
func resetWorkflowTimeoutOverridesForTest() {
	applyWorkflowTimeoutOverridesOnce = sync.Once{}
}

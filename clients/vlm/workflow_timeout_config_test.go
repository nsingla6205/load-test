package vlm

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// timeoutEnvKeys lists every env var that applyWorkflowTimeoutOverrides may read.
var timeoutEnvKeys = []string{
	"VLM_DEFAULT_WORKFLOW_EXECUTION_TIMEOUT_MINUTES",
	"VLM_CREATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES",
	"VLM_CREATE_VSA_SVM_WF_TIMEOUT_MINUTES",
	"VLM_MODIFY_VSA_SVM_WF_TIMEOUT_MINUTES",
	"VLM_DELETE_VSA_SVM_WF_TIMEOUT_MINUTES",
	"VLM_DELETE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES",
	"VLM_UPDATE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES",
	"VLM_UPGRADE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES",
	"VLM_CLUSTER_POWER_OP_WF_TIMEOUT_MINUTES",
	"VLM_UPDATE_VSA_MEDIATOR_WF_TIMEOUT_MINUTES",
	"VLM_UPDATE_LICENSE_WF_TIMEOUT_MINUTES",
	"VLM_CREATE_VSA_EXPERT_MODE_USER_WF_TIMEOUT_MINUTES",
	"VLM_ROTATE_FABRIC_POOL_KEYS_WF_TIMEOUT_MINUTES",
	"VLM_MAINTENANCE_WF_TIMEOUT_MINUTES",
	"VLM_MEDIATOR_MAINTENANCE_REBOOT_MIGRATE_WF_TIMEOUT_MINUTES",
	"VLM_MEDIATOR_MAINTENANCE_RECREATE_WF_TIMEOUT_MINUTES",
}

func withClearedTimeoutEnv(t *testing.T) {
	t.Helper()
	saved := make(map[string]string, len(timeoutEnvKeys))
	for _, key := range timeoutEnvKeys {
		if val, ok := os.LookupEnv(key); ok {
			saved[key] = val
		}
		require.NoError(t, os.Unsetenv(key))
	}
	t.Cleanup(func() {
		for _, key := range timeoutEnvKeys {
			require.NoError(t, os.Unsetenv(key))
		}
		for key, val := range saved {
			require.NoError(t, os.Setenv(key, val))
		}
		resetWorkflowTimeoutOverridesForTest()
		applyWorkflowTimeoutOverrides()
	})
	resetWorkflowTimeoutOverridesForTest()
	applyWorkflowTimeoutOverrides()
}

func TestApplyWorkflowTimeoutOverridesDefaults(t *testing.T) {
	withClearedTimeoutEnv(t)

	expected := map[string]time.Duration{
		"DefaultWorkflowExecutionTimeout":            10 * time.Minute,
		CreateVSAClusterDeploymentWorkflowName:       30 * time.Minute,
		CreateVSASVMWorkflowName:                     30 * time.Minute,
		ModifyVSASVMWorkflowName:                     30 * time.Minute,
		DeleteVSASVMWorkflowName:                     25 * time.Minute,
		DeleteVSAClusterDeploymentWorkflowName:       30 * time.Minute,
		UpdateVSAClusterDeploymentWorkflowName:       120 * time.Minute,
		UpgradeVSAClusterDeploymentWorkflowName:      300 * time.Minute,
		ClusterPowerCycleWorkflowName:                40 * time.Minute,
		UpdateVSAMediatorWorkflowName:                30 * time.Minute,
		UpdateLicenseWorkflowName:                    10 * time.Minute,
		CreateVSAExpertModeUserWorkflowName:          45 * time.Minute,
		RotateFabricPoolKeysWorkflowName:             10 * time.Minute,
		MaintenanceWorkflowName:                      120 * time.Minute,
		MediatorMaintenanceRebootMigrateWorkflowName: 30 * time.Minute,
		MediatorMaintenanceRecreateWorkflowName:      30 * time.Minute,
	}

	for workflowName, want := range expected {
		got, ok := WorkflowExecutionTimeoutMap[workflowName]
		require.True(t, ok, "missing timeout for %q", workflowName)
		require.Equal(t, want, got, "timeout for %q", workflowName)
	}
}

func TestApplyWorkflowTimeoutOverridesEnvOverride(t *testing.T) {
	withClearedTimeoutEnv(t)

	tests := []struct {
		envKey       string
		envValue     string
		workflowName string
		want         time.Duration
	}{
		{
			envKey:       "VLM_UPGRADE_VSA_CLUSTER_DEPLOYMENT_WF_TIMEOUT_MINUTES",
			envValue:     "45",
			workflowName: UpgradeVSAClusterDeploymentWorkflowName,
			want:         45 * time.Minute,
		},
		{
			envKey:       "VLM_MAINTENANCE_WF_TIMEOUT_MINUTES",
			envValue:     "200",
			workflowName: MaintenanceWorkflowName,
			want:         200 * time.Minute,
		},
		{
			envKey:       "VLM_MEDIATOR_MAINTENANCE_REBOOT_MIGRATE_WF_TIMEOUT_MINUTES",
			envValue:     "45",
			workflowName: MediatorMaintenanceRebootMigrateWorkflowName,
			want:         45 * time.Minute,
		},
		{
			envKey:       "VLM_MEDIATOR_MAINTENANCE_RECREATE_WF_TIMEOUT_MINUTES",
			envValue:     "40",
			workflowName: MediatorMaintenanceRecreateWorkflowName,
			want:         40 * time.Minute,
		},
		{
			envKey:       "VLM_CREATE_VSA_SVM_WF_TIMEOUT_MINUTES",
			envValue:     "0",
			workflowName: CreateVSASVMWorkflowName,
			want:         30 * time.Minute, // invalid → default
		},
		{
			envKey:       "VLM_CREATE_VSA_SVM_WF_TIMEOUT_MINUTES",
			envValue:     "-5",
			workflowName: CreateVSASVMWorkflowName,
			want:         30 * time.Minute, // invalid → default
		},
		{
			envKey:       "VLM_MAINTENANCE_WF_TIMEOUT_MINUTES",
			envValue:     "0",
			workflowName: MaintenanceWorkflowName,
			want:         120 * time.Minute, // invalid → default
		},
	}

	for _, tt := range tests {
		t.Run(tt.envKey+"="+tt.envValue, func(t *testing.T) {
			for _, key := range timeoutEnvKeys {
				require.NoError(t, os.Unsetenv(key))
			}
			require.NoError(t, os.Setenv(tt.envKey, tt.envValue))
			resetWorkflowTimeoutOverridesForTest()
			applyWorkflowTimeoutOverrides()
			require.Equal(t, tt.want, WorkflowExecutionTimeoutMap[tt.workflowName])
		})
	}
}

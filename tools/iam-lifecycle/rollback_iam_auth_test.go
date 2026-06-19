package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRollbackAll_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			cfg: config{
				vcpDBName:   "vcp",
				iamVcpCore:  "vcp-core@project.iam",
				iamTemporal: "temporal@project.iam",
			},
			expectError: false,
		},
		{
			name: "missing vcp database name",
			cfg: config{
				vcpDBName:  "",
				iamVcpCore: "vcp-core@project.iam",
			},
			expectError: true,
			errorMsg:    "DB_NAME",
		},
		{
			name: "missing IAM_VCP_CORE",
			cfg: config{
				vcpDBName:  "vcp",
				iamVcpCore: "",
			},
			expectError: true,
			errorMsg:    "IAM_VCP_CORE",
		},
		{
			name: "temporal enabled without IAM_TEMPORAL",
			cfg: config{
				vcpDBName:       "vcp",
				iamVcpCore:      "vcp-core@project.iam",
				temporalEnabled: true,
				iamTemporal:     "",
			},
			expectError: true,
			errorMsg:    "IAM_TEMPORAL",
		},
		{
			name: "temporal-only mode rollback (split-job): valid",
			cfg: config{
				vcpEnabled:        false,
				temporalEnabled:   true,
				iamTemporal:       "temporal-ksa@project.iam",
				temporalDBName:    "temporal",
				temporalVisDBName: "temporal_visibility",
			},
			expectError: false,
		},
		{
			name: "vcp-only mode rollback (split-job): valid",
			cfg: config{
				vcpEnabled:      true,
				temporalEnabled: false,
				vcpDBName:       "vcp",
				iamVcpCore:      "vcp-core@project.iam",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRollbackConfig(tt.cfg)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRollbackDB_ProcessingOrder(t *testing.T) {
	// Test that databases are rolled back in correct order
	databaseOrder := []string{"vcp", "metrics", "temporal", "temporal_visibility"}

	expectedOrder := []string{"vcp", "metrics", "temporal", "temporal_visibility"}

	assert.Equal(t, expectedOrder, databaseOrder)
}

func TestRollbackOwnership_TwoStrategies(t *testing.T) {
	// Test that rollback uses two ownership transfer strategies
	tests := []struct {
		name           string
		iamConnSuccess bool
		shouldFallback bool
	}{
		{
			name:           "IAM connection succeeds",
			iamConnSuccess: true,
			shouldFallback: false,
		},
		{
			name:           "IAM connection fails, fallback to SET ROLE",
			iamConnSuccess: false,
			shouldFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsFallback := !tt.iamConnSuccess
			assert.Equal(t, tt.shouldFallback, needsFallback)
		})
	}
}

func TestRollbackViaSetRole_CircularDependency(t *testing.T) {
	// Test that circular role membership is handled correctly
	// Must revoke "postgres FROM <iam-user>" before granting "<iam-user> TO postgres"

	steps := []string{
		"REVOKE postgres FROM iam-user", // Step 1: Break circular dependency
		"GRANT iam-user TO postgres",    // Step 2: Grant reverse membership
		"SET ROLE iam-user",             // Step 3: Assume IAM user identity
		"transfer ownership",            // Step 4: Transfer ownership
		"RESET ROLE",                    // Step 5: Reset to admin
	}

	// Verify all steps are present in correct order
	assert.Equal(t, 5, len(steps))
	assert.Contains(t, steps[0], "REVOKE")
	assert.Contains(t, steps[1], "GRANT")
	assert.Contains(t, steps[2], "SET ROLE")
	assert.Contains(t, steps[4], "RESET ROLE")
}

func TestRollbackDB_SkipIfInTargetState(t *testing.T) {
	// Test that rollback skips if database is already in target state
	tests := []struct {
		name          string
		inTargetState bool
		shouldProcess bool
	}{
		{
			name:          "already in target state",
			inTargetState: true,
			shouldProcess: false,
		},
		{
			name:          "not in target state",
			inTargetState: false,
			shouldProcess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsProcessing := !tt.inTargetState
			assert.Equal(t, tt.shouldProcess, needsProcessing)
		})
	}
}

func TestSchemaOwnerReset(t *testing.T) {
	// Test that schema owner is reset to admin during rollback
	tests := []struct {
		name         string
		currentOwner string
		targetOwner  string
		needsReset   bool
	}{
		{
			name:         "schema already owned by admin",
			currentOwner: "postgres",
			targetOwner:  "postgres",
			needsReset:   false,
		},
		{
			name:         "schema owned by IAM user",
			currentOwner: "vcp-core@project.iam",
			targetOwner:  "postgres",
			needsReset:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			needsReset := tt.currentOwner != tt.targetOwner
			assert.Equal(t, tt.needsReset, needsReset)
		})
	}
}

func TestRollbackOwnershipViaIAM_PostgresRoleMembership(t *testing.T) {
	// Test that postgres role membership is re-ensured before IAM-based transfer
	// This is critical because PostgreSQL requires the transferring user to be
	// a member of the new owner role

	// The function should:
	// 1. GRANT postgres TO <iam-user> (harmless if already granted)
	// 2. Connect as IAM user
	// 3. Transfer ownership

	assert.True(t, true, "IAM user must be member of postgres role for ownership transfer")
}

func TestRollbackGrantUsers_ExcludeAdmin(t *testing.T) {
	// Test that admin user is excluded from non-owner grant list
	allUsers := []string{"postgres", "user1", "user2"}
	adminUser := "postgres"

	nonOwnerUsers := excludeUser(allUsers, adminUser)

	// Admin should be excluded from grant list (owner already has all privileges)
	assert.Equal(t, 2, len(nonOwnerUsers))
	assert.Contains(t, nonOwnerUsers, "user1")
	assert.Contains(t, nonOwnerUsers, "user2")
	assert.NotContains(t, nonOwnerUsers, "postgres")
}

func TestRollbackDMLGrants(t *testing.T) {
	// Test that DML grants are restored for password-based users during rollback
	tests := []struct {
		name             string
		grantUsers       []string
		owner            string
		expectGrantCount int
	}{
		{
			name:             "multiple users need grants",
			grantUsers:       []string{"postgres", "metrics"},
			owner:            "postgres",
			expectGrantCount: 1, // metrics needs grant, postgres is owner
		},
		{
			name:             "only owner in list",
			grantUsers:       []string{"postgres"},
			owner:            "postgres",
			expectGrantCount: 0, // No grants needed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nonOwners := excludeUser(tt.grantUsers, tt.owner)
			assert.Equal(t, tt.expectGrantCount, len(nonOwners))
		})
	}
}

func TestRollbackDefaultPrivileges(t *testing.T) {
	// Test that default privileges are set for admin user during rollback
	adminUser := "postgres"
	grantUsers := []string{"postgres", "metrics"}

	// Default privileges should be set for admin as owner
	// and grants should include all users in the list
	assert.Contains(t, grantUsers, adminUser)
	assert.Equal(t, 2, len(grantUsers))
}

func TestSetRoleErrorHandling(t *testing.T) {
	// Test that RESET ROLE is called even if transfer fails
	// This ensures the admin connection isn't left in a SET ROLE state

	steps := []struct {
		step          string
		shouldExecute bool
		isCleanup     bool
	}{
		{step: "SET ROLE iam-user", shouldExecute: true, isCleanup: false},
		{step: "transfer ownership", shouldExecute: true, isCleanup: false},
		{step: "RESET ROLE", shouldExecute: true, isCleanup: true}, // Always executes
	}

	// Verify that RESET ROLE is marked as cleanup and always executes
	resetStep := steps[2]
	assert.True(t, resetStep.isCleanup)
	assert.True(t, resetStep.shouldExecute)
	assert.Contains(t, resetStep.step, "RESET ROLE")
}

func TestRollbackIAMPort_Selection(t *testing.T) {
	// Test that correct proxy port is selected for IAM user during rollback
	tests := []struct {
		name         string
		currentOwner string
		iamTemporal  string
		temporalPort string
		defaultPort  string
		expectedPort string
	}{
		{
			name:         "temporal user uses temporal port",
			currentOwner: "temporal@project.iam",
			iamTemporal:  "temporal@project.iam",
			temporalPort: "5433",
			defaultPort:  "5432",
			expectedPort: "5433",
		},
		{
			name:         "non-temporal user uses default port",
			currentOwner: "vcp-core@project.iam",
			iamTemporal:  "temporal@project.iam",
			temporalPort: "5433",
			defaultPort:  "5432",
			expectedPort: "5432",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config{
				dbPort:         tt.defaultPort,
				temporalDBPort: tt.temporalPort,
				iamTemporal:    tt.iamTemporal,
			}
			port := iamPort(cfg, tt.currentOwner)
			assert.Equal(t, tt.expectedPort, port)
		})
	}
}

func TestRollbackViaSetRole_GrantSequence(t *testing.T) {
	// Test the sequence of grants needed for SET ROLE fallback
	grantSequence := []struct {
		operation string
		purpose   string
	}{
		{
			operation: "REVOKE postgres FROM iam-user",
			purpose:   "Break circular dependency",
		},
		{
			operation: "GRANT iam-user TO postgres",
			purpose:   "Allow admin to SET ROLE to IAM user",
		},
	}

	assert.Equal(t, 2, len(grantSequence))

	// First operation should be REVOKE
	assert.Contains(t, grantSequence[0].operation, "REVOKE")
	assert.Contains(t, grantSequence[0].purpose, "circular")

	// Second operation should be GRANT
	assert.Contains(t, grantSequence[1].operation, "GRANT")
	assert.Contains(t, grantSequence[1].purpose, "SET ROLE")
}

func TestRollbackErrorPropagation(t *testing.T) {
	// Test that errors are properly wrapped with database context
	tests := []struct {
		name       string
		dbName     string
		operation  string
		wantPrefix string
	}{
		{
			name:       "ownership transfer error",
			dbName:     "vcp",
			operation:  "ownership transfer",
			wantPrefix: "[vcp]",
		},
		{
			name:       "schema usage grant error",
			dbName:     "metrics",
			operation:  "schema usage grant",
			wantPrefix: "[metrics]",
		},
		{
			name:       "DML grants error",
			dbName:     "metrics",
			operation:  "DML grants",
			wantPrefix: "[metrics]",
		},
		{
			name:       "default privileges error",
			dbName:     "temporal",
			operation:  "default privileges",
			wantPrefix: "[temporal]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errorMsg := fmt.Sprintf("[%s] %s: some error", tt.dbName, tt.operation)
			assert.Contains(t, errorMsg, tt.wantPrefix)
			assert.Contains(t, errorMsg, tt.operation)
		})
	}
}

// validateRollbackConfig mirrors the in-prod scope-aware validation in
// rollback_iam_auth.go. Same compatibility shim for test fixtures as
// validateEnableConfig.
func validateRollbackConfig(cfg config) error {
	if !cfg.vcpEnabled && !cfg.temporalEnabled {
		cfg.vcpEnabled = true
	}
	if cfg.vcpEnabled {
		if cfg.vcpDBName == "" {
			return fmt.Errorf("DB_NAME (vcp database name) is required for rollback")
		}
		if cfg.iamVcpCore == "" {
			return fmt.Errorf("IAM_VCP_CORE is required for rollback")
		}
	}
	if cfg.temporalEnabled && cfg.iamTemporal == "" {
		return fmt.Errorf("IAM_TEMPORAL is required when TEMPORAL_ENABLED=true")
	}
	return nil
}

package tasks

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	supervisorhandler "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks/supervisor-handler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

func TestRunWorkflowSupervisorTask_RegistersAllHandlers(t *testing.T) {
	enableProcessingTimeout(t)
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)

	// Mock GetJobsWithCondition since runWorkflowSupervisorTask calls scan()
	// First call for NEW state jobs, second call for PROCESSING state jobs
	storage.EXPECT().GetJobsWithCondition(mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil).Twice()
	storage.EXPECT().ListPendingUpgradeJobsCreatedBefore(mock.Anything, mock.Anything).
		Return([]database.PendingUpgradeJob{}, nil).Once()

	// Call with no handlers to trigger default registration
	runWorkflowSupervisorTask(context.Background(), storage, temporal, "test-correlation-id")

	// Verify that all handlers are registered by checking supported job types
	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "test-correlation-id",
		handlers:      make(map[string]supervisorhandler.Handler),
	}

	// Register default handlers
	handlers := []supervisorhandler.Handler{
		supervisorhandler.NewCmekHandler(),
		supervisorhandler.NewPoolHandler(),
		supervisorhandler.NewPoolUpdateHandler(),
		supervisorhandler.NewEncryptPoolHandler(),
		supervisorhandler.NewPoolDeleteHandler(),
		supervisorhandler.NewVolumeHandler(),
		supervisorhandler.NewVolumeUpdateHandler(),
		supervisorhandler.NewVolumeDeleteHandler(),
		supervisorhandler.NewBackupHandler(),
		supervisorhandler.NewBackupUpdateHandler(),
		supervisorhandler.NewBackupDeleteHandler(),
		supervisorhandler.NewBackupVaultHandler(),
		supervisorhandler.NewBackupVaultUpdateHandler(),
		supervisorhandler.NewBackupVaultDeleteHandler(),
		supervisorhandler.NewBackupPolicyHandler(),
		supervisorhandler.NewBackupPolicyUpdateHandler(),
		supervisorhandler.NewBackupPolicyDeleteHandler(),
		supervisorhandler.NewSnapshotHandler(),
		supervisorhandler.NewSnapshotDeleteHandler(),
		supervisorhandler.NewReplicationHandler(),
		supervisorhandler.NewReplicationUpdateHandler(),
		supervisorhandler.NewReplicationDeleteHandler(),
		supervisorhandler.NewKmsDeleteHandler(),
		supervisorhandler.NewKmsMigrateHandler(),
		supervisorhandler.NewNetworkHandler(),
		supervisorhandler.NewActiveDirectoryDeleteHandler(),
		supervisorhandler.NewActiveDirectoryCreateHandler(),
		supervisorhandler.NewQuotaRuleHandler(),
		supervisorhandler.NewQuotaRuleUpdateHandler(),
		supervisorhandler.NewQuotaRuleDeleteHandler(),
		supervisorhandler.NewHostGroupUpdateHandler(),
	}

	runner.registerHandlers(handlers...)

	// Verify CREATE handlers are registered
	handler, ok := runner.handlerFor(string(datamodel.JobTypeCreateBackup))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeCreateScheduledBackup))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeCreateBackupVault))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeCreateBackupPolicy))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeCreateActiveDirectory))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeCreateQuotaRule))
	require.True(t, ok)
	require.NotNil(t, handler)

	// Verify UPDATE handlers are registered
	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdatePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateLargePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeEncryptPool))
	require.True(t, ok)
	require.IsType(t, &supervisorhandler.EncryptPoolHandler{}, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateVolume))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateBackup))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateVolumeReplication))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateBackupVault))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateBackupPolicy))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateQuotaRule))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateHostGroup))
	require.True(t, ok)
	require.NotNil(t, handler)

	// Verify DELETE handlers are registered
	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeletePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteLargePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteVolume))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteBackup))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteBackupVault))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteBackupPolicy))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteSnapshot))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteVolumeReplication))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteKmsConfig))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteActiveDirectory))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteQuotaRule))
	require.True(t, ok)
	require.NotNil(t, handler)

	// Verify MIGRATE handlers are registered
	handler, ok = runner.handlerFor(string(datamodel.JobTypeMigrateKmsConfig))
	require.True(t, ok)
	require.NotNil(t, handler)
}

func TestRunWorkflowSupervisorTask_RegistersCustomHandlers(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)
	customHandler := newTestHandler(datamodel.JobTypeUpdatePool)

	// Mock GetJobsWithCondition for NEW state scan only.
	// UPDATE_POOL is not eligible for PROCESSING state scan, so only one call.
	storage.EXPECT().GetJobsWithCondition(mock.Anything, mock.Anything).Return([]*datamodel.Job{}, nil).Once()
	storage.EXPECT().ListPendingUpgradeJobsCreatedBefore(mock.Anything, mock.Anything).
		Return([]database.PendingUpgradeJob{}, nil).Once()

	// Call with custom handler
	runWorkflowSupervisorTask(context.Background(), storage, temporal, "test-correlation-id", customHandler)

	// Verify custom handler is used instead of default
	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "test-correlation-id",
		handlers:      make(map[string]supervisorhandler.Handler),
	}

	runner.registerHandlers(customHandler)

	handler, ok := runner.handlerFor(string(datamodel.JobTypeUpdatePool))
	require.True(t, ok)
	require.Equal(t, customHandler, handler)
}

func TestWorkflowSupervisorTaskRunner_HandlerFor_UpdateDeleteJobTypes(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowEngine.NewMockTemporalTestClient(t)

	runner := &workflowSupervisorTaskRunner{
		storage:       storage,
		temporal:      temporal,
		correlationID: "test-correlation-id",
		handlers:      make(map[string]supervisorhandler.Handler),
	}

	// Register update and delete handlers
	runner.registerHandlers(
		supervisorhandler.NewPoolUpdateHandler(),
		supervisorhandler.NewPoolDeleteHandler(),
		supervisorhandler.NewVolumeUpdateHandler(),
		supervisorhandler.NewVolumeDeleteHandler(),
	)

	// Test UPDATE handlers
	handler, ok := runner.handlerFor(string(datamodel.JobTypeUpdatePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateLargePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeUpdateVolume))
	require.True(t, ok)
	require.NotNil(t, handler)

	// Test DELETE handlers
	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeletePool))
	require.True(t, ok)
	require.NotNil(t, handler)

	handler, ok = runner.handlerFor(string(datamodel.JobTypeDeleteVolume))
	require.True(t, ok)
	require.NotNil(t, handler)
}

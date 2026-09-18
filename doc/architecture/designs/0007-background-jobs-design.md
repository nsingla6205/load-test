# Background Jobs Design

## Context

The VSA Control Plane requires a robust system for managing background jobs that are essential for service operation and customer-initiated scheduled tasks. These jobs need to:

- Execute workflows at regular intervals using Temporal Schedules
- Handle both customer-initiated and service-essential background jobs
- Provide flexibility to update, pause, resume and delete schedules at runtime
- Ensure only one worker pod manages schedule creation to avoid conflicts
- Support high availability and fault tolerance
- Be easily configurable and maintainable

## Decision

We implement a comprehensive background jobs system using Temporal Schedules with the following architecture:

### 1. Job Types

**Customer Background Jobs**: User-initiated scheduled jobs (backups, backup policies)
**Service Background Jobs**: System-essential operational jobs (VSA sync, cleanup, monitoring)

### 2. Configuration Management

- **Embedded JSON Configuration**: Use `admin_background_jobs.json` for service job specifications
- **Database Persistence**: Store job specs in `ADMIN_JOB_SPECS` table with states
- **Environment Overrides**: Support feature flags and environment-based configuration

### 3. Scheduling Framework

- **Generic Scheduler Interface**: Abstract interface supporting multiple backend systems
- **Temporal Scheduler Implementation**: Concrete implementation using Temporal Schedules
- **JobManagerWorkflow**: Auxiliary workflow for managing schedule lifecycle

### 4. Job Lifecycle Management

- **States**: CREATING, UPDATING, DELETING, SCHEDULED, DELETED
- **Workflow ID Conflict Policy**: Use "Fail" policy to ensure single execution
- **Task Queue Separation**: Use `background-workflows` task queue for isolation

## Implementation Details

### Database Schema

```sql
CREATE TABLE admin_job_specs (
    id SERIAL PRIMARY KEY,
    uuid TEXT NOT NULL,
    job_type TEXT UNIQUE NOT NULL,
    cron_expression TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'CREATING',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);
```

### Job Specifications

Service background jobs are defined in `admin_background_jobs.json`:

```json
{
  "SYNC_VSA_SNAPSHOTS": {
    "jobType": "SYNC_VSA_SNAPSHOTS",
    "cronExpression": "*/5 * * * *",
    "state": "CREATING"
  },
  "ROTATE_KMS_SERVICE_ACCOUNTS": {
    "jobType": "ROTATE_KMS_SERVICE_ACCOUNTS",
    "cronExpression": "0 0 * * *",
    "state": "CREATING"
  }
}
```

### Scheduler Interface

```go
type Scheduler interface {
    Create(ctx context.Context, params CreateScheduleParams) (*ScheduleResponse, error)
    Update(ctx context.Context, params UpdateScheduleParams) (*ScheduleResponse, error)
    Delete(ctx context.Context, params DeleteScheduleParams) (*ScheduleResponse, error)
    Pause(ctx context.Context, params PauseScheduleParams) (*ScheduleResponse, error)
    Unpause(ctx context.Context, params UnpauseScheduleParams) (*ScheduleResponse, error)
    Describe(ctx context.Context, params DescribeScheduleParams) (*ScheduleDescription, error)
}
```

### JobManagerWorkflow Activities

1. **CreateScheduleActivity**: Creates Temporal schedules for jobs in CREATING state
2. **UpdateScheduleActivity**: Updates existing schedules for jobs in UPDATING state
3. **DeleteScheduleActivity**: Deletes schedules for jobs in DELETING state

### Job Type to Workflow Mapping

```go
var JobTypeToWorkflow = map[string]interface{}{
    SyncVsaSnapshots:              backgroundworkflows.SnapshotsSyncParentWorkflow,
    RotateKmsServiceAccounts:      background_kms_workflows.RotateKmsSAKeyWorkflow,
    OrphanJobScheduler:            backgroundworkflows.OrphanJobSchedulerWorkflow,
    SyncLatestBackupLogicalSize:   backgroundworkflows.SyncLatestBackupLogicalSizeWorkflow,
    HardDeleteResourcesAndAccount: backgroundworkflows.HardDeleteResourcesAndAccountWorkflow,
    CleanupHydratedMetricsTable:   backgroundworkflows.CleanupHydratedMetricsTableWorkflow,
    CleanupAggregatedUsageTable:   backgroundworkflows.CleanupAggregatedUsageTableWorkflow,
    SyncVsaAutoTiering:            backgroundworkflows.SyncVSAAutoTieringWorkflow,
    DeleteResources:               backgroundworkflows.ResourceCleanupParentWorkflow,
    SyncBackupZiZsMetadata:        backgroundworkflows.SyncBackupZiZsWorkflow,
}
```

## Current Background Jobs

| Job Type | Schedule | Purpose |
|----------|----------|---------|
| SYNC_VSA_SNAPSHOTS | Every 5 minutes | Sync VSA snapshots to VCP |
| ROTATE_KMS_SERVICE_ACCOUNTS | Daily at midnight | Rotate KMS service account keys |
| HARD_DELETE_RESOURCES_AND_ACCOUNT | Daily at 8 AM | Hard delete resources and accounts |
| ORPHANED_JOB_SCHEDULER | Every 2 minutes | Process orphaned jobs |
| SYNC_LATEST_BACKUP_LOGICAL_SIZE | Every 5 minutes | Sync backup logical sizes |
| CLEANUP_HYDRATED_METRICS_TABLE | Daily at 10:00 UTC | Cleanup metrics data |
| CLEANUP_AGGREGATED_USAGE_TABLE | Daily at 22:00 UTC | Cleanup usage data |
| CLEANUP_JOBS_TABLE | Daily at 16:00 UTC | Cleanup jobs data |
| CLEANUP_BACKUP_CHAIN_HISTORY | Daily at 04:00 UTC | Cleanup backup chain history data |
| SYNC_VSA_AUTO_TIERING | Every 5 minutes | Sync auto-tiering data |
| DELETE_RESOURCES | Daily at midnight | Delete resources |
| SYNC_BACKUP_ZIZS_METADATA | Every 12 hours | Sync backup metadata |

## Error Handling and Recovery

### Retry Policies

- **Default Max Retries**: 3 attempts
- **Exponential Backoff**: 5s initial, 2.0 coefficient, 15s maximum
- **Non-Retryable Errors**: PanicError types

### Workflow Error Handling

- **Activity Failures**: Log errors but continue execution
- **Schedule Creation Failures**: Fail fast with clear error messages
- **Database Sync Failures**: Retry with exponential backoff

## Configuration Management

### Environment Variables

- `AUTO_TIERING_ENABLED`: Controls auto-tiering sync jobs
- `METRICS_DB_CLEANUP_ENABLED`: Controls metrics cleanup jobs
- `DELETE_RESOURCES_CRON_EXPRESSION`: Overrides delete resources schedule

### Feature Flags

Jobs can be enabled/disabled based on feature flags:

```go
// Remove auto-tiering jobs if feature disabled
if !env.GetBool("AUTO_TIERING_ENABLED", false) {
    delete(adminJobSpecs, "SYNC_VSA_AUTO_TIERING")
}

// Remove metrics cleanup if feature disabled
if !env.GetBool("METRICS_DB_CLEANUP_ENABLED", false) {
    delete(adminJobSpecs, "CLEANUP_HYDRATED_METRICS_TABLE")
    delete(adminJobSpecs, "CLEANUP_AGGREGATED_USAGE_TABLE")
    delete(adminJobSpecs, "CLEANUP_JOBS_TABLE")
    delete(adminJobSpecs, "CLEANUP_BACKUP_CHAIN_HISTORY")
}
```

## Monitoring and Observability

### Logging

- **Structured Logging**: JSON-formatted logs with correlation IDs
- **Activity Logging**: Detailed logs for each schedule operation
- **Error Logging**: Comprehensive error logging with context

### Health Checks

- **Workflow Status**: Query handlers for workflow status
- **Schedule Status**: Monitor individual schedule health
- **Job Execution**: Track job execution success/failure rates

### Metrics

- **Schedule Creation**: Success/failure rates
- **Job Execution**: Duration and success rates
- **Error Rates**: Failure patterns and trends

## Testing Strategy

### Unit Tests

- **Activity Testing**: Individual activity function testing
- **Scheduler Testing**: Scheduler interface implementation testing
- **Mock Implementations**: Comprehensive mocking for isolated testing

### Integration Tests

- **Workflow Testing**: End-to-end workflow execution testing
- **Database Testing**: Database operations testing
- **Temporal Integration**: Temporal schedule operations testing

## Deployment and Operations

### Startup Process

1. **Load Specifications**: Load job specs from embedded JSON
2. **Database Sync**: Synchronize with database
3. **Launch Workflow**: Start JobManagerWorkflow
4. **Create Schedules**: Create Temporal schedules
5. **Update Schedules**: Update existing schedules
6. **Delete Schedules**: Remove obsolete schedules

### Configuration Changes

1. **Update JSON**: Modify `admin_background_jobs.json`
2. **Deploy Application**: Deploy updated application
3. **Automatic Sync**: System syncs changes on startup

## Consequences

### Positive

- **Scalability**: Supports high-volume job execution
- **Reliability**: Fault-tolerant with retry mechanisms
- **Maintainability**: Easy to add/modify/remove jobs
- **Observability**: Comprehensive logging and monitoring
- **Flexibility**: Environment-based configuration

### Negative

- **Deployment Dependency**: Job changes require application restart
- **Single Point of Failure**: JobManagerWorkflow failure blocks startup
- **Configuration Complexity**: Multiple configuration sources
- **Temporal Dependency**: Tightly coupled to Temporal Schedules

### Risks

- **Schedule Conflicts**: Non-idempotent schedule creation
- **Resource Exhaustion**: High-frequency jobs may impact performance
- **Data Consistency**: Database and Temporal state synchronization
- **Error Propagation**: Activity failures may cascade

## Future Enhancements

### Dynamic Management

- **API Endpoints**: REST APIs for schedule management
- **Real-time Updates**: Live schedule modifications
- **Configuration UI**: Web interface for job management

### Advanced Scheduling

- **Calendar-based**: Support calendar-based scheduling
- **Conditional**: Conditional job execution
- **Dependencies**: Job dependency management

### Enhanced Monitoring

- **Custom Metrics**: Job-specific metrics
- **Alerting**: Automated failure notifications
- **Dashboard**: Visual schedule monitoring

## References

- [Temporal Schedules Documentation](https://docs.temporal.io/workflows#schedules)
- [Background Workflows Documentation](../../workflows/background/scheduled-backup-workflows.md)
- [JobManagerWorkflow Implementation](../../../core/orchestrator/workflows/jobmanagerworkflows/job_manager_workflow.go)
- [Scheduler Framework](../../../core/scheduler/scheduler.go)
- [Admin Job Specifications](../../../core/scheduler/adminbackgroundjobs/admin_background_jobs.json)

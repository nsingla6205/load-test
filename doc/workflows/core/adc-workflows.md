# ADC Workflows

This document describes the ADC (Application Data Controller) workflows in the VSA Control Plane system, including ADC deployment, management, and cleanup operations.

## Overview

ADC workflows manage the Application Data Controller service, which handles data collection and monitoring for VSA clusters. These workflows ensure proper ADC deployment, configuration, and cleanup operations.

## Workflow Types

### 1. ADC Workflow

**File**: `core/orchestrator/workflows/adc_workflow.go`

**Purpose**: Manages ADC deployment, configuration, and cleanup operations.

**Entry Point**: `ADCWorkflow(ctx workflow.Context, params *common.DeleteBackupParams, backupVault *datamodel.BackupVault, backup *datamodel.Backup, account *datamodel.Account)`

#### Workflow Structure

```go
type AdcWF struct {
    BaseWorkflow
    cloudDeletionIntiated bool
}
```

#### Configuration

**Environment Variables**:
```go
var (
    adcPort        = 443
    adcImage       = env.GetString("ADC_IMAGE", "")
    adcRestPort    = env.GetInt("ADC_REST_PORT", 80)
    adcRegion      = env.GetString("ADC_REGION", "")
    adcProjectID   = env.GetString("ADC_PROJECT", "")
    adcProvideType = env.GetString("ADC_PROVIDE_TYPE", "GoogleCloud")
    adcStorageURL  = env.GetString("ADC_STORAGE_URL", "storage.googleapis.com")
    adcCertSecret  = env.GetString("ADC_CERT_SECRET_NAME", "adc-cert")
)
```

**Polling Configuration**:
```go
var (
    adcMaxPollingAttempts  = 60
    adcMaxCloudRunAttempts = 20
)
```

#### Progressive Sleep Phases

The ADC workflow implements progressive sleep phases for long-running operations:

**Phase 1 (0-5 minutes)**:
- Sleep Duration: 5 seconds
- Purpose: Initial rapid polling

**Phase 2 (5-15 minutes)**:
- Sleep Duration: 10 seconds
- Purpose: Moderate polling

**Phase 3 (15 minutes - 1 hour 15 minutes)**:
- Sleep Duration: 5 minutes
- Purpose: Slower polling for longer operations

**Phase 4 (1 hour 15 minutes - 6 days)**:
- Sleep Duration: 10 minutes
- Purpose: Very slow polling for very long operations

**Maximum Time Limit**: 6 days (6 * 24 * time.Hour)

#### Activities

**ADC Deployment Activities**:
- `DeployADCService`: Deploys ADC service to cloud
- `ConfigureADCService`: Configures ADC service settings
- `ValidateADCDeployment`: Validates ADC deployment
- `UpdateADCStatus`: Updates ADC status in database

**ADC Management Activities**:
- `StartADCService`: Starts ADC service
- `StopADCService`: Stops ADC service
- `RestartADCService`: Restarts ADC service
- `MonitorADCHealth`: Monitors ADC service health

**ADC Cleanup Activities**:
- `DeleteADCService`: Deletes ADC service from cloud
- `CleanupADCResources`: Cleans up ADC resources
- `UpdateADCStateInDB`: Updates ADC state in database
- `ValidateADCCleanup`: Ensures safe cleanup

**Cloud Integration Activities**:
- `CreateCloudRunService`: Creates Cloud Run service
- `ConfigureCloudRunService`: Configures Cloud Run service
- `ValidateCloudRunService`: Validates Cloud Run service
- `DeleteCloudRunService`: Deletes Cloud Run service

#### Execution Flow

1. **Pre-deployment Phase**:
   - Validate ADC parameters
   - Check cloud resource availability
   - Prepare ADC configuration

2. **ADC Deployment**:
   - Deploy ADC service to cloud
   - Configure ADC service
   - Validate deployment

3. **ADC Management**:
   - Start ADC service
   - Monitor ADC health
   - Handle service operations

4. **ADC Cleanup**:
   - Stop ADC service
   - Delete ADC service
   - Clean up resources

5. **Validation and Cleanup**:
   - Validate ADC operations
   - Update database records
   - Handle rollback if errors occur

#### Progressive Polling Implementation

```go
func (wf *AdcWF) progressiveSleep(ctx workflow.Context, startTime time.Time) error {
    elapsed := time.Since(startTime)
    
    switch {
    case elapsed < firstPhaseThreshold:
        return workflow.Sleep(ctx, firstPhaseSleepDuration)
    case elapsed < secondPhaseThreshold:
        return workflow.Sleep(ctx, secondPhaseSleepDuration)
    case elapsed < thirdPhaseThreshold:
        return workflow.Sleep(ctx, thirdPhaseSleepDuration)
    default:
        return workflow.Sleep(ctx, fourthPhaseSleepDuration)
    }
}
```

## ADC Service Management

### Service Configuration

**ADC Service Settings**:
- **Object-store port**: 443 (HTTPS)
- **Cloud Run REST port**: Configurable via `ADC_REST_PORT` (default: 80). This value is applied to the ADC `REST_PORT` environment variable, Cloud Run container port, and startup probe.
- **Image**: Configurable via `ADC_IMAGE` environment variable
- **Region**: Configurable via `ADC_REGION` environment variable
- **Project ID**: Configurable via `ADC_PROJECT` environment variable
- **Provider Type**: GoogleCloud (default)
- **Storage URL**: storage.googleapis.com (default)
- **Certificate Secret**: adc-cert (default)

### Cloud Run Integration

**Cloud Run Service**:
- **Service Type**: Google Cloud Run
- **Max Attempts**: 20 (configurable)
- **Polling Attempts**: 60 (configurable)
- **Timeout**: 6 days maximum

### Health Monitoring

**Health Check Activities**:
- `CheckADCHealth`: Monitors ADC service health
- `ValidateADCConnectivity`: Validates ADC connectivity
- `MonitorADCPerformance`: Monitors ADC performance
- `UpdateADCHealthStatus`: Updates ADC health status

## Error Handling

### Error Types

**Non-Retryable Errors**:
- `PanicError`: System panic errors
- `ValidationError`: Parameter validation errors
- `CloudResourceNotFoundError`: Missing cloud resource errors

**Retryable Errors**:
- `NetworkTimeoutError`: Network operation timeouts
- `CloudOperationError`: Cloud operation failures
- `ADCServiceError`: ADC service operation failures

### Rollback Management

ADC workflows implement rollback for failed operations:

```go
defer func() {
    if err != nil {
        err2 := workflow.ExecuteActivity(ctx, adcActivity.UpdateADCStateInDB, 
            adc.UUID, models.LifeCycleStateError, models.LifeCycleStateCreationErrorDetails).Get(ctx, nil)
        if err2 != nil {
            log.Errorf("Failed to update ADC state in DB to error: %v", err2)
        }
    }
}()
```

## Configuration

### Retry Policies

All ADC workflows use configurable retry policies:

```go
retryPolicy, err := PopulateRetryPolicyParams()
if err != nil {
    return nil, ConvertToVSAError(err)
}
```

### Activity Options

```go
ao := workflow.ActivityOptions{
    StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:        retryPolicy.InitialInterval,
        BackoffCoefficient:     retryPolicy.BackoffCoefficient,
        MaximumInterval:        retryPolicy.MaximumInterval,
        MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
        NonRetryableErrorTypes: []string{"PanicError"},
    },
}
```

## Monitoring and Metrics

### ADC Metrics

**Available Metrics**:
- ADC deployment duration
- ADC service health status
- Cloud Run service metrics
- Data collection metrics

### Health Checks

**ADC Health Monitoring**:
- ADC service connectivity status
- Cloud Run service status
- Data collection status
- Performance metrics

## Performance Considerations

### ADC Performance

**Optimization Features**:
- Progressive sleep phases for long operations
- Parallel ADC operations
- Resource utilization monitoring
- Cloud Run auto-scaling

### Resource Management

**Resource Limits**:
- Concurrent ADC operations
- Cloud Run service limits
- Network bandwidth limits
- Storage limits

## Security Considerations

### ADC Security

**Access Control**:
- Role-based access control
- ADC service permissions
- Audit logging for all operations

### Cloud Security

**Cloud Protection**:
- Secure cloud communications
- Certificate management
- Secret management
- Network security

## Testing

### Test Coverage

Each ADC workflow has comprehensive test coverage:

- **Unit Tests**: Individual workflow function testing
- **Integration Tests**: End-to-end workflow testing
- **Mock Activities**: Mock implementations for testing
- **Error Scenarios**: Testing failure and retry scenarios
- **Cloud Integration Tests**: Testing with actual cloud resources

### Test Files

- `adc_workflow_test.go`: Main test file
- Mock implementations for all activities
- Test data fixtures for various scenarios

## Related Documentation

- [Backup Workflows](./backup-workflows.md)
- [Pool Workflows](./pool-workflows.md)
- [Temporal Debugging Guide](../../guides/temporal-debugging.md)
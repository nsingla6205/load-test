# Pool Workflows

This document describes the pool-related workflows in the VSA Control Plane system, including pool creation, management, and configuration operations.

## Overview

Pool workflows manage the complete lifecycle of storage pools in the VSA Control Plane, from creation to deletion. They handle VSA cluster provisioning, network configuration, KMS setup, and resource management.

## Workflow Types

### 1. Create Pool Workflow

**File**: `core/orchestrator/workflows/pool_workflows.go`

**Purpose**: Creates new storage pools with VSA clusters and associated infrastructure.

**Entry Point**: `CreatePoolWorkflow(ctx workflow.Context, params *common.CreatePoolParams, pool *datamodel.Pool)`

#### Workflow Structure

```go
type createPoolWorkflow struct {
    BaseWorkflow
    SE *database.Storage
}
```

#### Activities

**Pool Creation Activities**:
- `CreatePoolInDB`: Creates pool record in database
- `ValidatePoolParameters`: Validates pool creation parameters
- `CheckResourceAvailability`: Verifies resource availability
- `CreateVSAInstances`: Provisions VSA instances
- `ConfigureNetwork`: Sets up network configuration
- `SetupKMS`: Configures Key Management Service
- `ValidatePoolCreation`: Validates pool creation success

**VSA Management Activities**:
- `DeployVSAInstances`: Deploys VSA instances to GCP
- `ConfigureVSACluster`: Configures VSA cluster settings
- `SetupVSANetworking`: Configures VSA networking
- `ValidateVSAHealth`: Validates VSA cluster health

**Network Activities**:
- `CreateVPC`: Creates Virtual Private Cloud
- `CreateSubnets`: Creates network subnets
- `ConfigureFirewall`: Sets up firewall rules
- `SetupNAT`: Configures Network Address Translation

**KMS Activities**:
- `ConfigureKMSForSVM`: Configures KMS for Storage Virtual Machine
- `ValidateKMSReachability`: Validates KMS connectivity
- `SetupKMSKeys`: Sets up encryption keys

#### Execution Flow

1. **Pre-creation Phase**:
   - Validate pool parameters
   - Check resource availability
   - Prepare infrastructure configuration
   - Initialize cancellation handler for CCFE cleanup support

2. **Infrastructure Creation**:
   - Create VPC and subnets
   - Configure firewall rules
   - Set up NAT gateway
   - Check for cancellation signals at checkpoints

3. **VSA Deployment**:
   - Deploy VSA instances
   - Configure VSA cluster
   - Set up VSA networking
   - Check for cancellation signals at checkpoints

4. **KMS Configuration**:
   - Configure KMS for SVM
   - Validate KMS connectivity
   - Set up encryption keys
   - Check for cancellation signals at checkpoints

5. **Validation and Cleanup**:
   - Validate pool creation
   - Update database records
   - Handle rollback if errors occur
   - Handle rollback if cancellation detected

#### Cancellation Handling

The create pool workflow supports graceful cancellation when a delete request is received while the pool is in `CREATING` state:

- **Cancellation Signal**: `"cancel-pool-creation"` signal can be sent to the workflow
- **Checkpoints**: Cancellation is checked at strategic points throughout the workflow:
  - Before image digest validation
  - After finding tenancy project
  - After data subnet sequential poller
  - After saving pool with cluster details
  - After network configuration
  - Before service account creation
  - Before auto-tier bucket creation
  - Before ONTAP credentials creation
  - Before VM identification
  - Before secondary zone identification
  - Before serial number allocation
  - Before VSA cluster deployment creation

- **Rollback on Cancellation**: When cancellation is detected:
  - Workflow stops executing new activities
  - Rollback manager executes cleanup of partially created resources
  - Pool state is updated appropriately
  - Child workflows are cancelled via `PARENT_CLOSE_POLICY_REQUEST_CANCEL`

- **Child Workflow Cancellation**: Child workflows automatically cancel when parent is cancelled:
  - `DataSubnetSequentialPoller`: Cancelled when parent is cancelled
  - `ConfigureNetworkWorkflow`: Cancelled when parent is cancelled

#### Configuration

**Environment Variables**:
```go
var (
    setupNwHeartbeatTimeout                              = env.GetUint64("SETUP_NW_HEARTBEAT_TIMEOUT_SEC", 300)
    vmrsConfigPath                                       = env.GetString("VMRS_CONFIG_PATH", "/config/vmrs_gcp.yaml")
    maxNodesPerGroup                                     = env.GetInt("MAX_NODES_PER_GROUP", 200)
    enableMetrics                                        = env.GetBool("ENABLE_METRICS", false)
    enableUniqueSerialNumberGeneration                   = env.GetBool("ENABLE_UNIQUE_SERIAL_NUMBER_GENERATION", false)
    vsaImageName                                         = env.GetString("VSA_IMAGE_NAME", "x-9-18-1p3")
    mediatorImage                                        = env.GetString("VSA_MEDIATOR_IMAGE_NAME", "cvo-mediator-x-9-18-1p3")    
    vsaFilesImageName                                    = env.GetString("VSA_FILES_IMAGE_NAME", "x-9-18-1p3")
	filesMediatorImage                                   = env.GetString("VSA_FILES_MEDIATOR_IMAGE_NAME", "cvo-mediator-x-9-18-1p3")
    waitTimeForGCPOperationInSec                         = env.GetInt("WAIT_TIME_FOR_GCP_OPERATION_IN_SEC", 10)
    disableVsaCleanupOnVLMFailure                        = env.GetBool("DISABLE_VSA_CLEANUP_ON_VLM_FAILURE", false)
    enableAutoVolOfflineCronForGCPKMS                    = env.GetBool("ENABLE_AUTO_VOL_OFFLINE_CRON_FOR_GCP_KMS", true)
    ginLoggingFeatureFlag                                = env.GetBool("GIN_LOGGING_FEATURE", false)
    poolWorkflowCancellationAckTimeout                   = env.GetUint64("POOL_WORKFLOW_CANCELLATION_TIMEOUT", 5)       // in minutes
    poolWorkflowForceTerminationAckTimeout               = env.GetUint64("POOL_WORKFLOW_FORCE_CANCEL_WAIT_TIMEOUT", 30) // in seconds
)
```

### 2. Pool Data Subnet Workflow

**Purpose**: Manages data subnet creation and configuration for pools.

**Entry Point**: `PoolDataSubnetWorkflow(ctx workflow.Context, params *common.PoolDataSubnetParams, pool *datamodel.Pool)`

#### Workflow Structure

```go
type poolDataSubnetWorkFlow struct {
    BaseWorkflow
    SE             *database.Storage
    TenancyDetails *common.TenancyInfo
}
```

#### Activities

**Subnet Management Activities**:
- `CreateDataSubnet`: Creates data subnet
- `ConfigureSubnetRouting`: Configures subnet routing
- `ValidateSubnetConfiguration`: Validates subnet setup
- `UpdatePoolSubnetInfo`: Updates pool subnet information

### 3. Pool Update Workflow

**Purpose**: Updates existing pool configurations and properties.

**Entry Point**: `UpdatePoolWorkflow(ctx workflow.Context, params *common.UpdatePoolParams, pool *datamodel.Pool)`

#### Activities

**Pool Update Activities**:
- `UpdatePoolInDB`: Updates pool information in database
- `UpdateVSAConfiguration`: Updates VSA cluster configuration
- `ValidatePoolUpdate`: Validates update parameters
- `ApplyPoolChanges`: Applies configuration changes

### 4. Pool Delete Workflow

**Purpose**: Safely deletes pools with proper resource cleanup. Handles deletion of pools in `CREATING` state by cancelling the ongoing create workflow.

**Entry Point**: `DeletePoolWorkflow(ctx workflow.Context, params *common.DeletePoolParams, pool *datamodel.Pool)`

#### Activities

**Pool Deletion Activities**:
- `DeletePoolFromVSA`: Removes pool from VSA cluster
- `CleanupPoolResources`: Cleans up associated resources
- `DeleteVSAInstances`: Deletes VSA instances
- `CleanupNetworkResources`: Cleans up network resources
- `UpdatePoolStateInDB`: Updates pool state to deleted

**Cancellation Activities** (when pool is in `CREATING` state):
- `GetCreateJobByResourceUUID`: Retrieves the create job associated with the pool
- `IsWorkflowRunningActivity`: Checks if the create workflow is still running
- `SendCancelSignalActivity`: Sends cancellation signal to the create workflow
- `WaitForWorkflowCancellationAckActivity`: Waits for graceful cancellation acknowledgment
- `ForceCancelWorkflowActivity`: Forcefully terminates the workflow if timeout exceeded
- `UpdateJobStatus`: Updates create job state to `ERROR` with cancellation details

#### Cancellation Flow for CREATING Pools

When a delete request is received for a pool in `CREATING` state:

1. **Correlation ID Validation**: Validates that the delete request has a matching correlation ID with the create request
2. **Create Workflow Detection**: Retrieves the create job and identifies the associated workflow
3. **Workflow Status Check**: Checks if the create workflow is still running
4. **Cancellation Signal**: Sends cancellation signal to the create workflow
5. **Graceful Cancellation Wait**: Waits for cancellation acknowledgment (default: 5 minutes)
6. **Force Termination**: If timeout exceeded, forcefully terminates the workflow (default: 30 seconds wait)
7. **Job State Update**: Updates create job state to `ERROR` with cancellation details
8. **Proceed with Deletion**: Continues with normal deletion flow

#### Conditional Resource Cleanup

The delete workflow conditionally skips cleanup for resources that haven't been created yet:

- **DNS Records**: Only cleaned up if pool credentials exist
- **VSA Cluster Deployment**: Only cleaned up if deployment name and cluster details exist
- **Service Account**: Only cleaned up if cluster details exist
- **Data Subnets**: Only cleaned up if cluster details exist

## VLM Worker Multi-Deployment Selection (Create/Delete)

The following sequence captures how VCP selects a VLM worker queue/version for pool create and delete workflows after the multi-deployment routing changes.

```mermaid
sequenceDiagram
    autonumber
    participant GP as Google Proxy/API
    participant CPW as CreatePoolWorkflow
    participant DEL as DeletePoolWorkflow
    participant RES as Resolver (pool_workflows.go)
    participant FF as Feature Flags (utils/vlm_feature_flag.go)
    participant VLM as VLM Client Workflow Manager
    participant TW as Temporal Queue (vsa-lifecycle-manager-<version>)

    rect rgb(240, 248, 255)
        Note over GP,TW: Create Pool Flow (queue selection)
        GP->>CPW: Start CreatePoolWorkflow(...)
        CPW->>RES: ResolveVLMWorkerQueueForPool(logger, pool)
        RES->>FF: GetVLMWorkerFeatureConfig(feature)
        alt Feature match found in VLM_WORKER_FEATURE_FLAG
            FF-->>RES: feature + vlmWorkerVersion + image overrides
            RES-->>CPW: queue = GetVLMWorkerQueueForVersion(vlmWorkerVersion)
        else No feature match
            RES-->>CPW: queue = GetVLMWorkerQueue(logger, account) (legacy fallback)
        end
        CPW->>VLM: CreateVSAClusterDeployment(request, queue)
        VLM->>TW: Execute on selected versioned VLM queue
    end

    rect rgb(245, 255, 245)
        Note over GP,TW: Delete Pool Flow (version selection)
        GP->>DEL: Start DeletePoolWorkflow(...)
        DEL->>RES: ResolveVLMWorkerVersionForDelete(pool)
        alt pool.BuildInfo.VLMWorkerVersion exists
            RES-->>DEL: use persisted version from build info
        else BuildInfo version missing
            RES->>FF: GetVLMWorkerFeatureConfig(feature)
            alt Feature match found
                FF-->>RES: vlmWorkerVersion from feature map
                RES-->>DEL: use feature-derived version
            else No feature match
                RES-->>DEL: use legacy allowlist ONTAP-derived version
            end
        end
        DEL->>VLM: DeleteVSAClusterDeployment(request, version)
        VLM->>TW: Execute on versioned VLM queue for that version
    end
```

## VSA Management

### VSA Instance Configuration

**Image Configuration**:
- **VSA Image**: `x-9-18-1` (default)
- **VSA Files Image**: `x-9-18-1` (default)
- **Mediator Image**: `cvo-mediator-x-9-18-1` (default)

**Instance Settings**:
- **Max Nodes Per Group**: 200 (configurable)
- **Heartbeat Timeout**: 300 seconds (configurable)
- **GCP Operation Wait Time**: 10 seconds (configurable)

### Network Configuration

**VPC and Subnet Management**:
- Creates VPC for each pool
- Configures subnets for different purposes
- Sets up firewall rules
- Configures NAT gateway

**Network Types**:
- **Management Network**: For VSA management
- **Data Network**: For data traffic
- **RSM Network**: For RSM operations
- **IC Network**: For Interconnect

## KMS Integration

### KMS Configuration

**KMS Activities**:
- `ConfigureKmsConfigForSvmActivity`: Configures KMS for SVM
- `VerifyKmsConfigReachability`: Verifies KMS connectivity
- `SetupKMSKeys`: Sets up encryption keys

**KMS Features**:
- **Auto Volume Offline**: Enabled by default for GCP KMS
- **KMS Reachability**: Validated during pool creation
- **Encryption Keys**: Automatically configured

## Error Handling

### Rollback Management

Pool workflows implement comprehensive rollback:

```go
rollbackManager := common.NewRollbackManager()
defer func() {
    if err != nil {
        rollbackManager.ExecuteRollback(ctx, err)
    }
}()
```

### Error Types

**Non-Retryable Errors**:
- `PanicError`: System panic errors
- `ValidationError`: Parameter validation errors
- `ResourceNotFoundError`: Missing resource errors

**Retryable Errors**:
- `NetworkTimeoutError`: Network operation timeouts
- `GCPOperationError`: GCP operation failures
- `VSAClusterError`: VSA cluster operation failures

## Monitoring and Metrics

### Metrics Collection

**Enabled Metrics** (when `ENABLE_METRICS=true`):
- Pool creation duration
- VSA deployment metrics
- Network configuration metrics
- KMS operation metrics

### Health Checks

**Pool Health Monitoring**:
- VSA cluster health status
- Network connectivity status
- KMS reachability status
- Resource utilization metrics

## Configuration Management

### VMRS Configuration

**Configuration File**: `/config/vmrs_gcp.yaml` (default)

**VMRS Features**:
- Volume Management Resource Service
- Resource allocation and management
- Performance optimization

### Feature Flags

**Available Feature Flags**:
- `ENABLE_METRICS`: Enable metrics collection
- `ENABLE_UNIQUE_SERIAL_NUMBER_GENERATION`: Enable unique serial number generation
- `DISABLE_VSA_CLEANUP_ON_VLM_FAILURE`: Disable VSA cleanup on VLM failure
- `ENABLE_AUTO_VOL_OFFLINE_CRON_FOR_GCP_KMS`: Enable auto volume offline for GCP KMS
- `GIN_LOGGING_FEATURE`: Enable Gin logging

**Cancellation Configuration**:
- `POOL_WORKFLOW_CANCELLATION_TIMEOUT`: Duration to wait for graceful cancellation acknowledgment (default: 5 minutes)
- `POOL_WORKFLOW_FORCE_CANCEL_WAIT_TIMEOUT`: Duration to wait for force termination acknowledgment (default: 30 seconds)

## Testing

### Test Coverage

Each pool workflow has comprehensive test coverage:

- **Unit Tests**: Individual workflow function testing
- **Integration Tests**: End-to-end workflow testing
- **Mock Activities**: Mock implementations for testing
- **Error Scenarios**: Testing failure and retry scenarios
- **GCP Integration Tests**: Testing with actual GCP resources

### Test Files

- `pool_workflows_test.go`: Main test file
- Mock implementations for all activities
- Test data fixtures for various scenarios

## Performance Considerations

### Resource Limits

**Node Limits**:
- **Max Nodes Per Group**: 200 (configurable)
- **Concurrent Operations**: Limited by GCP quotas
- **Memory Usage**: Optimized for large-scale deployments

### Timeout Configuration

**Operation Timeouts**:
- **Setup Network Heartbeat**: 300 seconds
- **GCP Operation Wait**: 10 seconds
- **VSA Deployment**: Variable based on instance count

## Security Considerations

### Network Security

**Firewall Rules**:
- Restrictive inbound rules
- Secure outbound connections
- VPC-level security groups

### KMS Security

**Encryption**:
- Customer-managed encryption keys
- KMS key rotation
- Secure key storage

## Related Documentation

- [Volume Workflows](./volume-workflows.md)
- [Cluster Workflows](./cluster-workflows.md)
- [KMS Workflows](../kms/kms-workflows.md)
- [Temporal Debugging Guide](../../guides/temporal-debugging.md)
- [CCFE Cleanup Acknowledgment Design](../../architecture/designs/0018-ccfe-cleanup-acknowledgment.md)
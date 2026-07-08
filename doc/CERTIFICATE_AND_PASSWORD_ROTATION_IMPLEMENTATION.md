# Certificate and Password Rotation Implementation

## Overview

This document describes the comprehensive implementation of certificate and password rotation for VSA (Virtual Storage Appliance) clusters. The system provides automated rotation of certificates and passwords used for VCP (VSA Control Plane) to VSA communication, following the same pattern as the existing KMS key rotation functionality.

The system supports:
- **Auth Type 1**: Password-based authentication with automatic password rotation
- **Auth Type 2**: Certificate-based authentication with both certificate rotation and password rotation capabilities

## Architecture

### Workflow Structure

```
RotateVsaCertificateAndPasswordWorkflow (Main Workflow)
├── Auth Type 2 (Certificate + Password Authentication)
│   ├── Certificate Rotation
│   │   └── RotatePoolCertificateWorkflow (Child Workflow)
│   │       ├── CertificateNeedsRotation
│   │       ├── GenerateAndCreateCertificateForVSACluster
│   │       ├── InstallCertificateOnVSA
│   │       └── UpdateCertificateCache
│   └── Password Rotation
│       └── RotatePoolPasswordWorkflow (Child Workflow)
│           ├── CreateNewSecretAndUpdateDatabase
│           ├── UpdateVSAPassword
│           ├── ValidateNewPasswordConnectivity
│           ├── SwapSecretIDs
│           ├── UpdateCacheWithNewSecret
│           └── CleanupPreviousSecret
└── Auth Type 1 (Password Authentication Only)
    └── Password Rotation
        └── RotatePoolPasswordWorkflow (Child Workflow)
            ├── CreateNewSecretAndUpdateDatabase
            ├── UpdateVSAPassword
            ├── ValidateNewPasswordConnectivity
            ├── SwapSecretIDs
            ├── UpdateCacheWithNewSecret
            └── CleanupPreviousSecret
```

### File Organization

- **`rotate_vsa_certificate_password_workflow.go`** - Main workflow orchestrating both certificate and password rotation
- **`certificate_rotation_activities.go`** - Certificate-specific activities
- **`password_rotation_activities.go`** - Password-specific activities  
- **`rotation_shared_activities.go`** - Shared utilities and common functions
- **`rotate_pool_certificate_workflow.go`** - Child workflow for certificate rotation
- **`rotate_pool_password_workflow.go`** - Child workflow for password rotation

## Authentication Types

### Auth Type 0: Basic Authentication
- Username/password stored directly in database
- **Not supported for rotation**

### Auth Type 1: Secret Manager Authentication
- Username/password stored in the cloud provider's secret manager
- **Supports password rotation only**
- Uses `secret_id` and `secret_id_new` for staging

### Auth Type 2: Certificate Authentication
- Client certificates for authentication
- **Supports both certificate rotation and password rotation**
- Uses `certificate_id` and `certificate_id_new` for certificate staging
- Uses `secret_id` and `secret_id_new` for password staging
- **Note**: Auth Type 2 pools can have both certificate and password credentials that can be rotated independently

## Certificate Rotation Process

### 1. Certificate Needs Rotation Check
```go
func (a *RotateVcpToVsaCertificateActivity) CertificateNeedsRotation(ctx context.Context, poolUUID string) (bool, error)
```
- Checks certificate expiration date
- Returns `true` if certificate expires within 30 days
- Handles certificate parsing and validation

### 2. Certificate Generation and Creation
```go
func (a *RotateVcpToVsaCertificateActivity) GenerateAndCreateCertificateForVSACluster(ctx context.Context, poolUUID string) (*CertificateGenerationResponse, error)
```
- Generates new client certificate using the cloud provider's certificate authority service
- Creates new secret in the cloud provider's secret manager for private key
- Returns certificate ID and secret ID for staging

### 3. Certificate Installation
```go
func (a *RotateVcpToVsaCertificateActivity) InstallCertificateOnVSA(ctx context.Context, poolUUID string) error
```
- Installs new certificate on VSA cluster
- Updates SSL configuration
- Tests connectivity with new certificate

### 4. Certificate Cache Update
```go
func (a *RotateVcpToVsaCertificateActivity) UpdateCertificateCache(ctx context.Context, certificateID string) error
```
- Updates in-memory cache with new certificate
- Removes old certificate from cache

## Password Rotation Process

### 1. Secret Creation and Database Update
```go
func (a *RotateVcpToVsaCertificateActivity) CreateNewSecretAndUpdateDatabase(ctx context.Context, poolUUID string) error
```
- Generates new strong password (16 characters)
- Creates new secret in the cloud provider's secret manager
- Updates database with `secret_id_new`

### 2. VSA Password Update
```go
func (a *RotateVcpToVsaCertificateActivity) UpdateVSAPassword(ctx context.Context, poolUUID string) error
```
- Updates password on all VSA cluster nodes
- Uses ONTAP REST API for password updates
- Handles primary and secondary nodes

### 3. Password Connectivity Validation
```go
func (a *RotateVcpToVsaCertificateActivity) ValidateNewPasswordConnectivity(ctx context.Context, poolUUID string) error
```
- Tests connectivity with new password
- Validates authentication works correctly
- Ensures no service disruption

### 4. Secret ID Swapping
```go
func (a *RotateVcpToVsaCertificateActivity) SwapSecretIDs(ctx context.Context, poolUUID string) error
```
- Swaps `secret_id` and `secret_id_new` in database
- Activates new password as primary
- Preserves old secret ID for cleanup

### 5. Cache Update
```go
func (a *RotateVcpToVsaCertificateActivity) UpdateCacheWithNewSecret(ctx context.Context, poolUUID string) error
```
- Updates in-memory cache with new secret
- Ensures immediate availability of new credentials

### 6. Old Secret Cleanup
```go
func (a *RotateVcpToVsaCertificateActivity) CleanupPreviousSecret(ctx context.Context, poolUUID, oldSecretID string) error
```
- Removes old secret from cache
- Deletes old secret from the cloud provider's secret manager
- Performed at start of next rotation cycle

## Certificate Lifecycle

1. **Creation**: New certificates are created via `GenerateAndCreateCertificateForVSACluster`
2. **Testing**: Connectivity is tested before committing to the new certificate
3. **Caching**: Valid certificates are cached using `AddToCertAuthCache`
4. **Rotation**: Old certificates are revoked and cleaned up
5. **Rollback**: Failed rotations can rollback to previous certificate if available

## Dependencies

### Hyperscaler Services
- `ResolveCloudService`: Resolves the active cloud provider's service instance
- `GenerateAndCreateCertificateForVSACluster`: Creates new certificates
- `RevokeCertificateAndDeleteFromCacheAndSecretManager`: Cleans up old certificates
- `GetCertificateFromCacheOrSecretManager`: Retrieves certificates

### Database
- `GetMultiplePools`: Lists pools with certificate auth
- `GetPoolByUUID`: Gets specific pool details

### Cache Management
- `AddToCertAuthCache`: Adds certificates to cache
- `RemoveFromCertAuthCache`: Removes certificates from cache
- `GetCertAuthCache`: Retrieves certificates from cache

## Security Features

### Password Security
- **No password logging**: Passwords are excluded from Temporal workflow history using `json:"-"` tags
- **Masked logging**: Only first 4 characters of passwords are logged for debugging
- **Strong password generation**: 16-character passwords with mixed case, numbers, and symbols
- **Secure storage**: Passwords stored in the cloud provider's secret manager with proper access controls

### Certificate Security
- **Immutable certificates**: GCP CAS certificates cannot be deleted, only revoked
- **Staged activation**: New certificates are staged before activation to prevent service disruption
- **Automatic cleanup**: Old certificates are revoked during next rotation cycle

## Error Handling and Rollback

### Rollback Strategy
- **Certificate rollback**: Revert to previous certificate if new one fails
- **Password rollback**: Revert to previous password if new one fails connectivity test
- **Database consistency**: Ensure database state remains consistent during failures
- **Cache synchronization**: Keep cache in sync with database state

### Error Types
- `ErrCertificateGenerationFailed` - Certificate creation failure
- `ErrPasswordSecretCreationFailed` - Secret creation failure
- `ErrVSAClusterPasswordUpdateFailed` - VSA password update failure
- `ErrPasswordConnectivityTestFailed` - Connectivity test failure
- `ErrGCPResourceFetchError` - GCP resource access failure

## Configuration

### Environment Variables

#### Certificate Rotation
- `ENABLE_VSA_CERTIFICATE_ROTATION`: Enable/disable certificate rotation (default: false)
- `CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE`: Percentage of certificate lifetime after which rotation should occur (default: 75, meaning 75% of lifetime)
- `MINIMUM_CERTIFICATE_LIFETIME`: Minimum required certificate lifetime for service startup (default: 5184000s, meaning 2 months)
- `ENABLE_VSA_EXPIRED_CERTIFICATES_ROTATION`: Enable/disable rotation of expired certificates (default: false)

#### Password Rotation
- `ENABLE_VSA_PASSWORD_ROTATION`: Enable/disable password rotation (default: false)
- `ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION`: Enable/disable password rotation for AuthType 1 pools (default: false)

### Usage Examples

#### Certificate Rotation Threshold
```bash
# Default behavior (75% of lifetime)
export CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE="75"
```

#### Expired Certificate Rotation
```bash
# Enable rotation of expired certificates
export ENABLE_VSA_EXPIRED_CERTIFICATES_ROTATION="true"

# Disable rotation of expired certificates (default)
export ENABLE_VSA_EXPIRED_CERTIFICATES_ROTATION="false"
```

#### Minimum Certificate Lifetime
```bash
# Default minimum (2 months)
export MINIMUM_CERTIFICATE_LIFETIME="5184000s"

# Shorter minimum (1 month)
export MINIMUM_CERTIFICATE_LIFETIME="2592000s"

# Longer minimum (1 year)
export MINIMUM_CERTIFICATE_LIFETIME="31536000s"

# Using different duration formats
export MINIMUM_CERTIFICATE_LIFETIME="2m"    # 2 months
export MINIMUM_CERTIFICATE_LIFETIME="8760h" # 1 year in hours
```

### Constants
- `certificateValidityDays = 90`: Certificates are valid for 90 days
- `rotationThresholdDays = 30`: Rotate certificates when they expire within 30 days (legacy, now configurable via CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE)

### Job Configuration
- **Job Type**: `ROTATE_VSA_CERTIFICATE_AND_PASSWORD`
- **Cron Expression**: `0/5 * * * *` (every 5 minutes)
- **State**: `CREATING` (initial state)

## Monitoring and Logging

### Logging Levels
- **INFO**: High-level operation status and results
- **DEBUG**: Detailed operation steps (no sensitive data)
- **ERROR**: Failure conditions and error details
- **WARN**: Non-critical issues and skipped operations

### Key Metrics
- Rotation success/failure rates
- Certificate expiration monitoring
- Password rotation completion times
- Connectivity test results

## Monitoring and Observability

- **Logs**: Structured logging throughout the rotation process
- **Metrics**: Integration with existing temporal workflow metrics
- **Alerts**: Failed rotations are logged as errors for alerting
- **Status Tracking**: Certificate expiration info can be retrieved via `GetCertificateExpirationInfo`

## Testing

### Test Coverage
- **Workflow Tests**: Main workflow execution scenarios
- **Activity Tests**: Individual activity functionality
- **Integration Tests**: End-to-end rotation flows
- **Error Handling Tests**: Failure scenarios and rollback

### Test Files
- `rotate_vsa_certificate_password_workflow_test.go` - Workflow tests
- `rotation_shared_activities_test.go` - Shared utility tests
- `rotate_pool_certificate_workflow_test.go` - Certificate workflow tests
- `rotate_pool_password_workflow_test.go` - Password workflow tests

### Test Scenarios
- Successful certificate rotation
- Disabled certificate rotation
- No pools with certificate auth
- Partial rotation failures
- Certificate connectivity testing
- Certificate expiration checking
- Password rotation for AuthType 1 and 2
- Parallel execution of certificate and password rotation

## Deployment

### Prerequisites
- Cloud provider secret manager access
- Cloud provider certificate authority service access
- VSA cluster network connectivity
- Proper IAM permissions for secret and certificate management

### Deployment Steps
1. Deploy updated workflow and activity code
2. Update job configuration in `admin_background_jobs.json`
3. Verify environment variables are set correctly
4. Monitor initial rotation cycles for proper operation

## Troubleshooting

### Common Issues
1. **Secret not found**: Check the cloud provider's secret manager permissions and secret existence
2. **Certificate generation failure**: Verify the cloud provider's certificate authority service access and certificate template
3. **VSA connectivity failure**: Check network connectivity and authentication
4. **Database update failure**: Verify database permissions and connection

### Debug Steps
1. Check workflow execution logs in Temporal UI
2. Verify cloud provider resource access and permissions
3. Test VSA connectivity manually
4. Review database state and consistency

## Usage Example

The certificate and password rotation is automatically handled by the job manager. To manually trigger or configure:

1. **Enable the features**:
   ```bash
   export ENABLE_VSA_CERTIFICATE_ROTATION=true
   export ENABLE_VSA_PASSWORD_ROTATION=true
   export ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION=true
   export ENABLE_VSA_EXPIRED_CERTIFICATES_ROTATION=true
   ```

2. **Configure job schedule**: Update the admin job specs in the database with appropriate cron expression

3. **Monitor execution**: Check temporal workflow execution logs for rotation status

## Security Considerations

- Certificates are stored securely in the cloud provider's secret manager
- Old certificates are properly revoked before cleanup
- Certificate private keys are handled securely throughout the process
- Rollback capability ensures service availability during rotation failures
- Passwords are never logged and are stored securely in the cloud provider's secret manager
- Early certificate revocation provides natural grace period for ongoing operations

## Future Enhancements

### Planned Features
- **Rotation scheduling**: More flexible scheduling options
- **Notification system**: Alerts for rotation failures
- **Metrics dashboard**: Real-time rotation status monitoring
- **Multi-region support**: Cross-region certificate and secret management
- **Certificate Expiration Parsing**: Parse actual certificate expiration dates instead of using time-based heuristics
- **Certificate Inventory**: Track certificate status across all pools
- **Advanced Testing**: More comprehensive connectivity testing with VSA clusters
- **Batch Processing**: Optimize rotation for large numbers of pools

### Performance Optimizations
- **Parallel processing**: Concurrent rotation for multiple pools
- **Batch operations**: Bulk secret and certificate operations
- **Caching improvements**: Enhanced cache management strategies

---

*Last Updated: January 9, 2025*
*Version: 3.0 - Comprehensive Documentation*

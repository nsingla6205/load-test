# VSA Control Plane - Coding Guidelines

This document outlines the coding standards and guidelines for the VSA Control Plane project, based on our automated linting configuration.

## Table of Contents

1. [Import Organization](#import-organization)
2. [Architectural Boundaries](#architectural-boundaries)
3. [Context Handling](#context-handling)
4. [Code Quality Standards](#code-quality-standards)
5. [Error Handling](#error-handling)
6. [Static Analysis Rules](#static-analysis-rules)
7. [Excluded Files and Directories](#excluded-files-and-directories)

## Import Organization

### Import Grouping with GCI

All Go files must organize imports into two distinct groups, separated by blank lines:

```go
import (
    // Standard library imports
    "context"
    "fmt"
    "net/http"
    "os"
    "testing"

    // Third-party and local imports
    "github.com/stretchr/testify/assert"
    "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
    "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
)
```

**Rules:**
- Standard library packages come first
- Third-party and local packages come second
- Groups are separated by a blank line
- Within each group, imports are sorted alphabetically
- Generated files are automatically skipped

### Import Aliases

When using import aliases, ensure they are consistent and meaningful:

```go
import (
    cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
    coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
    ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
)
```

**Avoid duplicate imports:**
```go
// ❌ Bad - duplicate imports
import (
    "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
    coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
)

// ✅ Good - use only the aliased version
import (
    coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
)
```

## Architectural Boundaries

### Core Module Restrictions

The `core/` directory is protected from cloud-specific dependencies to maintain architectural purity and portability.

**Prohibited imports in `core/**/*.go` files:**

- `cloud.google.com/go` and all subpackages
- `google.golang.org/api` and all subpackages
- `google.golang.org/genproto` and all subpackages
- `github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google` and subpackages

**Hyperscaler Module Restrictions**

All files outside the `hyperscaler/google/` directory are restricted from importing Google Cloud SDK packages to maintain clean separation.

**Prohibited imports in non-Google hyperscaler files:**

- `cloud.google.com/go` and all subpackages
- `google.golang.org/api` and all subpackages
- `google.golang.org/genproto` and all subpackages

**Rationale:**
- Maintains separation between business logic and cloud provider specifics
- Enables easier testing with mocks
- Supports potential multi-cloud implementations
- Prevents tight coupling to Google Cloud services
- Enforces proper abstraction layers

**Example:**
```go
// ❌ Bad - in core/ directory
package core

import (
    "cloud.google.com/go/storage" // Prohibited!
)

// ✅ Good - in core/ directory
package core

import (
    "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler" // Interface only
)

// ✅ Good - cloud-specific implementation in proper location
package google // in hyperscaler/google/

import (
    "cloud.google.com/go/storage" // Allowed here
)
```

### Cloud-Agnostic Design Principles

#### 1. Interface Abstraction

Define cloud-agnostic interfaces for all external dependencies:

```go
// ✅ Good - Generic interface
type Services interface {
    CreateServiceAccount(projectID, accountID, displayName, email string) (*models.ServiceAccount, error)
    DeleteServiceAccount(email string) error
    CreateServiceAccountKey(ctx context.Context, email string) (*models.ServiceAccountKey, error)
    AccessCryptoKey(config *models.EncryptionKey) error
}

// ✅ Good - Cloud-specific implementation
type GcpServices struct {
    // GCP-specific fields
}

func (g *GcpServices) CreateServiceAccount(projectID, accountID, displayName, email string) (*models.ServiceAccount, error) {
    // GCP-specific implementation
}
```

#### 2. Dependency Injection

Use dependency injection to enable testability and cloud provider abstraction:

```go
// ✅ Good - Constructor with dependency injection
type PoolActivity struct {
    SE          database.Storage
    Hyperscaler hyperscaler.Services // Generic interface
}

func NewPoolActivity(se database.Storage, hyperscaler hyperscaler.Services) *PoolActivity {
    return &PoolActivity{
        SE:          se,
        Hyperscaler: hyperscaler,
    }
}

// ✅ Good - Cloud-agnostic usage
func (activity *PoolActivity) CreateServiceAccount(ctx context.Context, params CreateParams) error {
    return activity.Hyperscaler.CreateServiceAccount(params.ProjectID, params.AccountID, params.DisplayName, params.Email)
}
```

#### 3. Configuration Management

Externalize configuration using structured config objects:

```go
// ✅ Good - Structured configuration
type Config struct {
    SnapshotSyncChunkSize int
    HydrationEnabled      bool
    ScheduledRegExp       *regexp.Regexp
    SnapmirrorRegExp      *regexp.Regexp
}

func DefaultConfig() *Config {
    return &Config{
        SnapshotSyncChunkSize: env.GetInt("CVS_SNAPSHOT_SYNC_CHUNK_SIZE", 200),
        HydrationEnabled:      env.GetBool("GCP_HYDRATE_ENABLED", true),
        ScheduledRegExp:       regexp.MustCompile(`^(hourly|daily|weekly|monthly)\..*$`),
        SnapmirrorRegExp:      regexp.MustCompile(`^snapmirror\.[0-9a-f-]{36}_.*$`),
    }
}

// ❌ Bad - Global variables
var (
    snapshotSyncChunkSize = env.GetInt("CVS_SNAPSHOT_SYNC_CHUNK_SIZE", 200)
    hydrationEnabled      = env.GetBool("GCP_HYDRATE_ENABLED", true)
)
```

## Context Handling

### Context as First Parameter

All functions that accept `context.Context` must have it as the first parameter:

```go
// ✅ Good
func ProcessData(ctx context.Context, data string, options ProcessOptions) error {
    // implementation
}

func CreateResource(ctx context.Context, client APIClient, resource Resource) (*Result, error) {
    // implementation  
}

// ❌ Bad
func ProcessData(data string, ctx context.Context, options ProcessOptions) error {
    // implementation
}

func CreateResource(client APIClient, ctx context.Context, resource Resource) (*Result, error) {
    // implementation
}
```

**Rules:**
- `context.Context` is always the first parameter
- Function signatures must be updated when adding context
- All callers must be updated to match the new signature
- This applies to both public and private functions

## Code Quality Standards

### Variable Assignment

Variables should be declared and assigned efficiently:

```go
// ✅ Good - combined declaration and assignment
result := processData(input)

// ❌ Bad - separate declaration and assignment (disabled S1021)
var result ProcessResult
result = processData(input)
```

**Note:** The S1021 staticcheck rule is disabled in our configuration, allowing separate variable declaration and assignment when needed for clarity or specific use cases.

### Inefficient Assignments

Avoid assignments to variables that are never used:

```go
// ❌ Bad
func processItems(items []Item) {
    count := 0
    for _, item := range items {
        count++ // count is never used after this
        processItem(item)
    }
}

// ✅ Good  
func processItems(items []Item) {
    for _, item := range items {
        processItem(item)
    }
}
```

### Go Vet Compliance

Code must pass all `go vet` checks including:
- Suspicious constructs
- Printf format string validation
- Unreachable code detection
- Variable shadowing (when significant)

## Error Handling

### Unchecked Errors

All errors must be properly handled or explicitly ignored:

```go
// ✅ Good
result, err := riskyOperation()
if err != nil {
    return fmt.Errorf("failed to perform operation: %w", err)
}

// ✅ Good - explicit ignore when safe
_ = file.Close() // Close on read-only file, error not critical

// ❌ Bad
result, _ := riskyOperation() // Ignoring potentially critical error
```

### Type Assertions

Type assertions should include the ok check:

```go
// ✅ Good
if handler, ok := obj.(http.Handler); ok {
    handler.ServeHTTP(w, r)
}

// ❌ Bad  
handler := obj.(http.Handler) // Could panic
```

## Static Analysis Rules

### Enabled Checks

Our static analysis enforces:

- **govet**: Suspicious constructs and Printf validation
- **staticcheck**: Advanced static analysis (with S1021 disabled)
- **ineffassign**: Unused variable assignments
- **errcheck**: Unchecked error returns
- **importas**: Consistent import aliasing
- **revive**: Code style and best practices

### Disabled Checks

The following checks are intentionally disabled:

- **gocyclo**: Cyclomatic complexity (use judgment for complex business logic)
- **dupl**: Code duplication (some patterns are acceptable)
- **gosec**: Security checks (handled by other tools/processes)

## Excluded Files and Directories

The following are automatically excluded from linting:

### Directories
- `vendor/` - Third-party dependencies
- `clients/core-api/` - Generated API clients
- `clients/cvp/` - Generated CVP clients
- `clients/ontap-rest/` - Generated ONTAP clients
- `vcp-core/servergen` - Generated server code
- `google-proxy/api/gcp-servergen/` - Generated GCP server code
- `telemetry/api/telemetry-servergen/` - Generated telemetry server code
- `cicd/cmd/lint/` - Linting tools

### File Patterns
- `*_mock.go` - Generated mock files
- `*_gen.go` - Generated code files
- `*_test.go` - Test files (partial exclusion for some rules)

## Best Practices

### 1. Function Design

#### Function Decomposition for Testability

Break down large monolithic functions into smaller, focused functions:

```go
// ❌ Bad - Monolithic function
func (activity *SyncSnapshotActivity) SynchronizeSnapshots(ctx context.Context, poolId int64) error {
    // 200+ lines of mixed logic
    // Direct environment variable access
    // Embedded business logic
    // Hard to test individual components
}

// ✅ Good - Decomposed functions
func (activity *SyncSnapshotActivity) SynchronizeSnapshots(ctx context.Context, poolId int64) error {
    return activity.synchronizePoolSnapshots(ctx, poolId)
}

func (activity *SyncSnapshotActivity) synchronizePoolSnapshots(ctx context.Context, poolId int64) error {
    ontapSnapshots, err := activity.getOntapSnapshots(ctx, poolId)
    if err != nil {
        return err
    }

    dbVolumes, err := activity.getDBVolumes(ctx, poolId)
    if err != nil {
        return err
    }

    dbSnapshots, err := activity.getDBSnapshots(ctx, poolId)
    if err != nil {
        return err
    }

    return activity.processSyncOperations(ctx, ontapSnapshots, dbVolumes, dbSnapshots)
}

// Individual testable functions
func (activity *SyncSnapshotActivity) getOntapSnapshots(ctx context.Context, poolId int64) ([]*vsa.Snapshot, error)
func (activity *SyncSnapshotActivity) getDBVolumes(ctx context.Context, poolId int64) ([]*vsa.Volume, error)
func (activity *SyncSnapshotActivity) getDBSnapshots(ctx context.Context, poolId int64) ([]*vsa.Snapshot, error)
func (activity *SyncSnapshotActivity) processSyncOperations(ctx context.Context, ontapSnapshots []*vsa.Snapshot, dbVolumes []*vsa.Volume, dbSnapshots []*vsa.Snapshot) error
```

#### Constructor Functions

Implement multiple constructor patterns for flexibility:

```go
// ✅ Good - Multiple constructor patterns
type SyncSnapshotActivity struct {
    SE           database.Storage
    Hyperscaler  hyperscaler.Services
    Config       *Config
    Dependencies *Dependencies
}

// Simple constructor with defaults
func NewSyncSnapshotActivity(se database.Storage, hyperscaler hyperscaler.Services) *SyncSnapshotActivity {
    return NewSyncSnapshotActivityWithDeps(se, hyperscaler, DefaultConfig(), DefaultDependencies())
}

// Advanced constructor with custom dependencies (for testing)
func NewSyncSnapshotActivityWithDeps(
    se database.Storage,
    hyperscaler hyperscaler.Services,
    config *Config,
    deps *Dependencies,
) *SyncSnapshotActivity {
    return &SyncSnapshotActivity{
        SE:           se,
        Hyperscaler:  hyperscaler,
        Config:       config,
        Dependencies: deps,
    }
}

// Default configuration factory
func DefaultConfig() *Config {
    return &Config{
        SnapshotSyncChunkSize: env.GetInt("CVS_SNAPSHOT_SYNC_CHUNK_SIZE", 200),
        HydrationEnabled:      env.GetBool("GCP_HYDRATE_ENABLED", true),
        ScheduledRegExp:       regexp.MustCompile(`^(hourly|daily|weekly|monthly)\..*$`),
        SnapmirrorRegExp:      regexp.MustCompile(`^snapmirror\.[0-9a-f-]{36}_.*$`),
    }
}

// Default dependencies factory
func DefaultDependencies() *Dependencies {
    return &Dependencies{
        FilterOntapVolumesAndSnapshots:         _filterOntapVolumesAndSnapshots,
        ProcessSnapshotSync:                    _processSnapshotSync,
        SyncDeletedSnapshotsToDatabase:         _syncDeletedSnapshotsToDatabase,
        SyncNewSnapshotsToDatabase:            _syncNewSnapshotsToDatabase,
        SyncUpdatedSnapshotsToDatabase:        _syncUpdatedSnapshotsToDatabase,
    }
}
```

#### Function Design Principles

- **Keep functions focused and single-purpose**
- **Use meaningful parameter names**
- **Follow the context-first parameter convention**
- **Return errors as the last return value**
- **Break down large functions for testability**
- **Make business logic functions public for individual testing**
- **Use dependency injection for external dependencies**

### 2. Package Organization
- Group related functionality in logical packages
- Avoid circular dependencies
- Keep the `core/` package cloud-agnostic
- Use clear, descriptive package names

### 3. Documentation
- Document exported functions and types
- Include usage examples for complex APIs
- Explain architectural decisions in comments
- Keep documentation up-to-date with code changes

### 4. Testing

#### Unit Testing with Dependency Injection

Use dependency injection to enable comprehensive unit testing:

```go
// ✅ Good - Testable with dependency injection
func TestCreateServiceAccount_Success(t *testing.T) {
    mockStorage := database.NewMockStorage(t)
    mockHyperscaler := hyperscaler.NewMockServices(t)
    
    activity := NewPoolActivity(mockStorage, mockHyperscaler)
    
    expectedSA := &models.ServiceAccount{
        Name:  "test-sa",
        Email: "test-sa@project.iam.gserviceaccount.com",
    }
    
    mockHyperscaler.On("CreateServiceAccount", "project-123", "test-sa", "Test SA", "test-sa@project.iam.gserviceaccount.com").Return(expectedSA, nil)
    
    result, err := activity.CreateServiceAccount(context.Background(), CreateParams{
        ProjectID:   "project-123",
        AccountID:   "test-sa",
        DisplayName: "Test SA",
        Email:       "test-sa@project.iam.gserviceaccount.com",
    })
    
    assert.NoError(t, err)
    assert.Equal(t, expectedSA, result)
    mockHyperscaler.AssertExpectations(t)
}
```

#### Function-Level Testing

Break down large functions to enable isolated testing:

```go
// ✅ Good - Testable individual functions
func TestFilterOntapVolumesAndSnapshots(t *testing.T) {
    config := &Config{
        ScheduledRegExp:  regexp.MustCompile(`^hourly.*`),
        SnapmirrorRegExp: regexp.MustCompile(`^snapmirror\..*`),
    }
    
    volumes := []*vsa.Volume{
        {Name: "test-volume", SnapmirrorRelationships: []string{}},
    }
    
    snapshots := []*vsa.Snapshot{
        {Name: "hourly.test", Volume: "test-volume"},
        {Name: "snapmirror.test", Volume: "test-volume"},
    }
    
    result, filtered := FilterOntapVolumesAndSnapshots(volumes, snapshots, config)
    
    assert.Equal(t, 1, len(result))
    assert.Equal(t, 1, len(filtered))
}
```

#### Configuration Testing

Test different configurations systematically:

```go
// ✅ Good - Configuration-driven testing
func TestWithDifferentConfigurations(t *testing.T) {
    testCases := []struct {
        name   string
        config *Config
        expect int
    }{
        {
            name:   "Small chunk size",
            config: &Config{SnapshotSyncChunkSize: 5},
            expect: 5,
        },
        {
            name:   "Hydration disabled",
            config: &Config{HydrationEnabled: false},
            expect: 0,
        },
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            activity := NewSyncSnapshotActivityWithDeps(mockSE, mockHyperscaler, tc.config, deps)
            // Test specific behavior with this configuration
        })
    }
}
```

#### Error Condition Testing

Test error conditions with controlled mocks:

```go
// ✅ Good - Error condition testing
func TestSyncDeletedSnapshots_DatabaseError(t *testing.T) {
    mockSE := database.NewMockStorage(t)
    
    mockSE.On("BatchDeleteSnapshots", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
    
    result, err := SyncDeletedSnapshotsToDatabase(context.Background(), []int64{1, 2}, mockSE, 10)
    
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "db error")
    assert.Nil(t, result)
}
```

#### Testing Best Practices

- **Write tests for public APIs**
- **Use table-driven tests for multiple scenarios**
- **Mock external dependencies with interfaces**
- **Test error conditions explicitly**
- **Use dependency injection for testability**
- **Test configuration variations**
- **Maintain good test coverage for critical paths**
- **Write fast unit tests without external dependencies**

### 5. Code Reviews
- Ensure lint rules pass before submitting PRs
- Review architectural boundary violations carefully
- Check for proper error handling
- Validate import organization and dependencies

## Enforcement

These guidelines are enforced through:

- **Automated linting** in CI/CD pipeline
- **Pre-commit hooks** (recommended)
- **Code review process**
- **Regular lint rule updates**

For questions about specific cases or exceptions, please discuss with the team lead or create an issue for clarification.

## Migration Strategies

### From Global Variables to Dependency Injection

#### Old Pattern (Deprecated):

```go
// ❌ Bad - Global variables
var (
    snapshotSyncChunkSize = env.GetInt("CVS_SNAPSHOT_SYNC_CHUNK_SIZE", 200)
    hydrationEnabled      = env.GetBool("GCP_HYDRATE_ENABLED", true)
    
    filterOntapVolumesAndSnapshots = _filterOntapVolumesAndSnapshots
    processSnapshotSync           = _processSnapshotSync
)

func TestSomething(t *testing.T) {
    // Save original global variables
    originalFunction := filterOntapVolumesAndSnapshots
    defer func() {
        filterOntapVolumesAndSnapshots = originalFunction
    }()
    
    // Override global function
    filterOntapVolumesAndSnapshots = func(...) { /* mock behavior */ }
    
    // Call function directly
    err := SomeGlobalFunction(params...)
}
```

#### New Pattern (Recommended):

```go
// ✅ Good - Dependency injection
type Config struct {
    SnapshotSyncChunkSize int
    HydrationEnabled      bool
}

type Dependencies struct {
    FilterOntapVolumesAndSnapshots FilterOntapVolumesAndSnapshotsFunc
    ProcessSnapshotSync           ProcessSnapshotSyncFunc
}

func TestSomething(t *testing.T) {
    ctx := context.TODO()
    mockStorage := database.NewMockStorage(t)
    mockHyperscaler := hyperscaler.NewMockServices(t)
    
    // Create configuration
    config := &Config{
        SnapshotSyncChunkSize: 10,
        HydrationEnabled:      true,
    }
    
    // Create custom dependencies with mocked functions
    deps := &Dependencies{
        FilterOntapVolumesAndSnapshots: func(volumes []*vsa.Volume, snapshots []*vsa.Snapshot, cfg *Config) (map[string]*vsa.Volume, []*vsa.Snapshot) {
            // Custom test behavior
            return make(map[string]*vsa.Volume), []*vsa.Snapshot{}
        },
        ProcessSnapshotSync: func(ctx context.Context, ontapVolumeMap map[string]*vsa.Volume, ontapSnapshots []*vsa.Snapshot, dbVolumeMap map[string]*datamodel.Volume, dbSnapshots []*datamodel.Snapshot) (
            map[string]*vsa.Snapshot, map[string]*vsa.Snapshot, map[string]*vsa.Snapshot, []string, []string, []int64, []string) {
            // Custom test behavior
            return map[string]*vsa.Snapshot{}, map[string]*vsa.Snapshot{}, map[string]*vsa.Snapshot{}, []string{}, []string{}, []int64{}, []string{}
        },
    }
    
    // Create activity with custom dependencies
    activity := NewSyncSnapshotActivityWithDeps(mockStorage, mockHyperscaler, config, deps)
    
    // Call method on activity
    err := activity.SynchronizeSnapshots(ctx, testPools)
}
```

### From Direct Cloud Dependencies to Abstraction

#### Old Pattern (Deprecated):

```go
// ❌ Bad - Direct cloud dependencies
import (
    "google.golang.org/api/iam/v1"
    "cloud.google.com/go/secretmanager/apiv1"
)

type PoolActivity struct {
    SE             database.Storage
    IamService     *iam.Service                    // Direct GCP IAM dependency
    SecretManager  *secretmanager.Client           // Direct GCP Secret Manager dependency
}

func (j *PoolActivity) CreateServiceAccount(ctx context.Context, projectID string, saAccountID string) (*iam.ServiceAccount, error) {
    // Direct GCP API calls
    createReq := &iam.CreateServiceAccountRequest{
        AccountId: saAccountID,
        ServiceAccount: &iam.ServiceAccount{
            DisplayName: "VSA Service Account",
        },
    }
    
    return j.IamService.Projects.ServiceAccounts.Create("projects/"+projectID, createReq).Do()
}
```

#### New Pattern (Recommended):

```go
// ✅ Good - Cloud-agnostic with dependency injection
import (
    "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
    hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

type PoolActivity struct {
    SE          database.Storage
    Hyperscaler hyperscaler.Services // Generic hyperscaler interface
}

func (j *PoolActivity) CreateServiceAccount(ctx context.Context, projectID string, saAccountID string, saDisplayName string) (*hyperscalermodels.ServiceAccount, error) {
    // Cloud-agnostic service account creation
    saEmail := utils.ConstructServiceAccountEmail(saAccountID, projectID)
    
    sa, err := j.Hyperscaler.CreateServiceAccount1(projectID, saAccountID, saDisplayName, saEmail)
    if err != nil {
        return nil, vsaerrors.WrapAsTemporalApplicationError(err)
    }
    return sa, nil
}
```

### Migration Steps

#### 1. Phase 1: Introduce Interfaces
- Define cloud-agnostic interfaces
- Create constructor functions
- Maintain backward compatibility

```go
// Add new constructor while keeping old struct initialization working
func NewPoolActivity(se database.Storage, hyperscaler hyperscaler.Services) *PoolActivity {
    return &PoolActivity{
        SE:          se,
        Hyperscaler: hyperscaler,
    }
}
```

#### 2. Phase 2: Move Cloud-Specific Code
- Move provider-specific implementations to `hyperscaler/google/`
- Update import statements
- Replace direct API calls with interface methods

#### 3. Phase 3: Add Comprehensive Tests
- Write unit tests with dependency injection
- Test error conditions with mocks
- Add configuration testing

#### 4. Phase 4: Remove Deprecated Patterns
- Remove global variables
- Remove direct cloud dependencies from business logic
- Update documentation

### Common Migration Errors and Solutions

#### Error: `undefined: filterOntapVolumesAndSnapshots`
**Solution**: Remove global variable assignments and use dependency injection instead.

```go
// ❌ Remove this
filterOntapVolumesAndSnapshots = mockFunction

// ✅ Use this instead
deps := &Dependencies{
    FilterOntapVolumesAndSnapshots: mockFunction,
}
activity := NewSyncSnapshotActivityWithDeps(mockSE, mockHyperscaler, config, deps)
```

#### Error: `unknown field ChunkSize in struct literal of type Config`
**Solution**: Use the correct field name `SnapshotSyncChunkSize`.

```go
// ❌ Wrong field name
config := &Config{ChunkSize: 10}

// ✅ Correct field name
config := &Config{SnapshotSyncChunkSize: 10}
```

#### Error: Function signature mismatch
**Solution**: Check the function type definitions in the Dependencies struct and match them exactly.

```go
// ✅ Match the exact signature
FilterOntapVolumesAndSnapshots: func(volumes []*vsa.Volume, snapshots []*vsa.Snapshot, config *Config) (map[string]*vsa.Volume, []*vsa.Snapshot) {
    // Implementation
},
```

### Backward Compatibility

Ensure migrations don't break existing code:

```go
// ✅ Old way still works
activity := &SyncSnapshotActivity{
    SE:         storage,
    Hyperscaler: hyperscaler,
}

// ✅ New way with dependency injection
activity := NewSyncSnapshotActivity(storage, hyperscaler)

// ✅ Advanced usage with custom configuration
config := &Config{SnapshotSyncChunkSize: 10}
deps := &Dependencies{/* custom implementations */}
activity := NewSyncSnapshotActivityWithDeps(storage, hyperscaler, config, deps)
``` 
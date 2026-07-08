package backgroundactivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/testsuite"
)

func TestResourceDeleteActivity_GetTotalResourceCount(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.GetTotalResourceCount)

	// Mock the database call
	mockStorage.On("GetResourcesCount", mock.Anything).Return(int64(3), nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.GetTotalResourceCount)

	assert.NoError(t, err)
	var count int
	err = encodedValue.Get(&count)
	assert.NoError(t, err)
	assert.Equal(t, 3, count)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_GetTotalResourceCount_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.GetTotalResourceCount)

	// Mock the database call to return error
	mockStorage.On("GetResourcesCount", mock.Anything).Return(int64(0), assert.AnError)

	_, err := env.ExecuteActivity(activityInstance.GetTotalResourceCount)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_ListResourcesPaginated(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.ListResourcesPaginated)

	// Mock the database call
	allResources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceType: "bucket", ResourceName: "test-bucket-1"},
		{ID: 2, ResourceType: "bucket", ResourceName: "test-bucket-2"},
		{ID: 3, ResourceType: "bucket", ResourceName: "test-bucket-3"},
		{ID: 4, ResourceType: "bucket", ResourceName: "test-bucket-4"},
		{ID: 5, ResourceType: "bucket", ResourceName: "test-bucket-5"},
	}
	mockStorage.On("ListPendingResourceDeletions", mock.Anything, 1, 2).Return(allResources, nil)

	// Test pagination
	encodedValue, err := env.ExecuteActivity(activityInstance.ListResourcesPaginated, 1, 2)

	assert.NoError(t, err)
	var resources []*datamodel.PendingResourceDeletions
	err = encodedValue.Get(&resources)
	assert.NoError(t, err)
	assert.Len(t, resources, 5) // The mock returns all resources, not paginated
	assert.Equal(t, "test-bucket-1", resources[0].ResourceName)
	assert.Equal(t, "test-bucket-2", resources[1].ResourceName)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_ListResourcesPaginated_EmptyResult(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.ListResourcesPaginated)

	// Mock the database call to return empty list
	mockStorage.On("ListPendingResourceDeletions", mock.Anything, 0, 10).Return([]*datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.ListResourcesPaginated, 0, 10)

	assert.NoError(t, err)
	var resources []*datamodel.PendingResourceDeletions
	err = encodedValue.Get(&resources)
	assert.NoError(t, err)
	assert.Len(t, resources, 0)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_EmptyList(t *testing.T) {
	activityInstance := &ResourceDeleteActivity{}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, []*datamodel.PendingResourceDeletions{})

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 0, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 0, result.Failed)
}

func TestResourceDeleteActivity_CleanupPendingResources_GCPServiceInitializationFails(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return error
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return nil, errors.New("GCP service initialization failed")
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	_, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_BucketDeletionSucceeds(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return success
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		return true, nil
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock successful update
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), true, "").Return(&datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 1, result.Successful)
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.FailedResourceNames)
	assert.Empty(t, result.FailedResourceErrors)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_BucketDeletionFailsWithError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return failure with error
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		return false, errors.New("bucket deletion failed")
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock successful update with error message
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), false, "bucket deletion failed").Return(&datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"bucket1"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 1)
	assert.Equal(t, "bucket1", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "bucket deletion failed", result.FailedResourceErrors[0].Error)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_BucketDeletionFailsWithoutError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return failure without error
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		return false, nil
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock successful update with default error message
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), false, "Resource deletion failed").Return(&datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"bucket1"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 1)
	assert.Equal(t, "bucket1", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "Resource deletion failed", result.FailedResourceErrors[0].Error)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_BucketDeletionFailsWithCustomError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return failure with custom error
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		customErr := &vsaerrors.CustomError{OriginalErr: errors.New("original error")}
		return false, customErr
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock successful update with original error message
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), false, "original error").Return(&datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"bucket1"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 1)
	assert.Equal(t, "bucket1", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "original error", result.FailedResourceErrors[0].Error)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_UnsupportedResourceType(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "resource1", ResourceType: "UNSUPPORTED_TYPE"},
	}

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"resource1"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 1)
	assert.Equal(t, "resource1", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "Unsupported resource type: UNSUPPORTED_TYPE", result.FailedResourceErrors[0].Error)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_MixedResourceTypes(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return success for first, failure for second
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	callCount := 0
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		callCount++
		if callCount == 1 {
			return true, nil // First bucket succeeds
		}
		return false, errors.New("second bucket failed") // Second bucket fails
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
		{ID: 2, ResourceName: "bucket2", ResourceType: "BUCKET"},
		{ID: 3, ResourceName: "resource3", ResourceType: "UNSUPPORTED_TYPE"},
	}

	// Mock updates
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), true, "").Return(&datamodel.PendingResourceDeletions{}, nil)
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(2), false, "second bucket failed").Return(&datamodel.PendingResourceDeletions{}, nil)

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 3, result.TotalProcessed)
	assert.Equal(t, 1, result.Successful)
	assert.Equal(t, 2, result.Failed)
	assert.Equal(t, []string{"bucket2", "resource3"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 2)
	assert.Equal(t, "bucket2", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "second bucket failed", result.FailedResourceErrors[0].Error)
	assert.Equal(t, "resource3", result.FailedResourceErrors[1].ResourceName)
	assert.Equal(t, "Unsupported resource type: UNSUPPORTED_TYPE", result.FailedResourceErrors[1].Error)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_UpdatePendingResourceDeletionFailsAfterSuccessfulDeletion(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return success
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		return true, nil
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock update to fail
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), true, "").Return(nil, errors.New("update failed"))

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful) // Should not count as successful due to update failure
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.FailedResourceNames)
	assert.Empty(t, result.FailedResourceErrors)
	mockStorage.AssertExpectations(t)
}

func TestResourceDeleteActivity_CleanupPendingResources_UpdatePendingResourceDeletionFailsAfterFailedDeletion(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activityInstance := &ResourceDeleteActivity{SE: mockStorage}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activityInstance.CleanupPendingResources)

	// Mock hyperscaler2.ResolveCloudService to return success
	originalGetGCPService := hyperscaler2.ResolveCloudService
	hyperscaler2.ResolveCloudService = func(ctx context.Context) (hyperscaler2.Services, error) {
		return &google.GcpServices{}, nil
	}
	defer func() { hyperscaler2.ResolveCloudService = originalGetGCPService }()

	// Mock activities.DeleteGCPBucket to return failure
	originalDeleteGCPBucket := activities.DeleteGCPBucket
	activities.DeleteGCPBucket = func(ctx context.Context, bucketName string, gcpService hyperscaler2.Services) (bool, error) {
		return false, errors.New("deletion failed")
	}
	defer func() { activities.DeleteGCPBucket = originalDeleteGCPBucket }()

	resources := []*datamodel.PendingResourceDeletions{
		{ID: 1, ResourceName: "bucket1", ResourceType: "BUCKET"},
	}

	// Mock update to fail
	mockStorage.On("UpdatePendingResourceDeletion", mock.Anything, int64(1), false, "deletion failed").Return(nil, errors.New("update failed"))

	encodedValue, err := env.ExecuteActivity(activityInstance.CleanupPendingResources, resources)

	assert.NoError(t, err)
	var result *ResourceCleanupBatchReturnValue
	err = encodedValue.Get(&result)
	assert.NoError(t, err)
	assert.Equal(t, 1, result.TotalProcessed)
	assert.Equal(t, 0, result.Successful)
	assert.Equal(t, 1, result.Failed)
	assert.Equal(t, []string{"bucket1"}, result.FailedResourceNames)
	assert.Len(t, result.FailedResourceErrors, 1)
	assert.Equal(t, "bucket1", result.FailedResourceErrors[0].ResourceName)
	assert.Equal(t, "deletion failed", result.FailedResourceErrors[0].Error)
	mockStorage.AssertExpectations(t)
}

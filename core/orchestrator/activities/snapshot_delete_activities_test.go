package activities_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

func TestDeleteSnapshot_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.SnapshotDeleteActivity{
		SE: mockStorage,
	}
	snapshotID := "test-snapshot-id"
	expectedSnapshot := &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshotID}}

	mockStorage.On("DeleteSnapshot", mock.Anything, snapshotID).Return(expectedSnapshot, nil)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshot)

	_, err := env.ExecuteActivity(activity.DeleteSnapshot, expectedSnapshot)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapshot_Failure(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.SnapshotDeleteActivity{SE: mockStorage}
	snapshotID := "test-snapshot-id"
	expectedError := errors.New("snapshot not found")

	mockStorage.On("DeleteSnapshot", mock.Anything, snapshotID).Return(nil, expectedError)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshot)

	_, err := env.ExecuteActivity(activity.DeleteSnapshot, &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshotID}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), expectedError.Error())
	mockStorage.AssertExpectations(t)
}

func TestDeleteSnapshotInONTAP_Success(t *testing.T) {
	// Arrange
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := activities.SnapshotDeleteActivity{}
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "uuid-123"},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	node := &models.Node{}

	// Mock the DeleteSnapshot method
	mockProvider.On("DeleteSnapshot", snapshot.ExternalUUID, snapshot.Volume.VolumeAttributes.ExternalUUID).Return(nil)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshotInONTAP)

	_, err := env.ExecuteActivity(activity.DeleteSnapshotInONTAP, snapshot, node)

	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotInONTAP_Failure(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := activities.SnapshotDeleteActivity{}
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "uuid-123"},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	node := &models.Node{}
	expectedError := errors.New("failed to delete snapshot in ONTAP")

	// Mock the DeleteSnapshot method
	mockProvider.On("DeleteSnapshot", snapshot.ExternalUUID, snapshot.Volume.VolumeAttributes.ExternalUUID).Return(expectedError)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshotInONTAP)

	_, err := env.ExecuteActivity(activity.DeleteSnapshotInONTAP, snapshot, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), expectedError.Error())
	mockProvider.AssertExpectations(t)
}

func TestDeleteSnapshotInONTAP_GetproviderByNodeFailure(t *testing.T) {
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider by node")
	}

	activity := activities.SnapshotDeleteActivity{}
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "uuid-123"},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	node := &models.Node{}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshotInONTAP)

	_, err := env.ExecuteActivity(activity.DeleteSnapshotInONTAP, snapshot, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get provider by node")
}

func TestDeleteSnapshotInONTAP_SnapshotInUse(t *testing.T) {
	mockProvider := new(vsa.MockProvider) // Use the mock provider
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }() // Restore original function after test

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := activities.SnapshotDeleteActivity{}
	snapshot := &datamodel.Snapshot{
		ExternalUUID:       "uuid-123",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "uuid-123"},
		BaseModel: datamodel.BaseModel{
			UUID: "test-snapshot-id",
		},
		Volume: &datamodel.Volume{
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "volume-uuid-123"},
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-id",
			},
		},
		Name: "test-snapshot",
	}
	node := &models.Node{}
	expectedError := errors.New("snapshot is in use by a volume clone")

	// Mock the DeleteSnapshot method to return "snapshot is in use" error
	mockProvider.On("DeleteSnapshot", snapshot.ExternalUUID, snapshot.Volume.VolumeAttributes.ExternalUUID).Return(expectedError)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.DeleteSnapshotInONTAP)

	_, err := env.ExecuteActivity(activity.DeleteSnapshotInONTAP, snapshot, node)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "snapshot is in use")
	mockProvider.AssertExpectations(t)
}

func TestUpdateDeleteSnapshotDetails_Failure(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := activities.SnapshotDeleteActivity{SE: mockStorage}
	snapshot := &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "test-snapshot-id"}}
	expectedError := errors.New("update failed")

	mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(nil, expectedError)

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UpdateDeleteSnapshotDetails)

	_, err := env.ExecuteActivity(activity.UpdateDeleteSnapshotDetails, snapshot)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), expectedError.Error())
	mockStorage.AssertExpectations(t)
}

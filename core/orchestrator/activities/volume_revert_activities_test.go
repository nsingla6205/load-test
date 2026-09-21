package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/hydrationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

func TestVolumeRevertActivity_RevertVolume_Success(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)

	// Save original function and restore after test
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeRevertActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RevertVolume)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "external-snapshot-uuid"},
	}

	node := &models.Node{
		Name: "test-node",
	}

	params := vsa.RevertVolumeParams{
		VolumeID:   "external-volume-uuid",
		SnapshotID: "external-snapshot-uuid",
	}

	// Setup mocks
	mockProvider.On("RevertVolume", vsa.RevertVolumeParams{
		VolumeID:        "external-volume-uuid",
		SnapshotID:      "external-snapshot-uuid",
		SnapshotName:    "test-snapshot",
		SvmName:         "test-svm",
		PreRevertVolume: volume,
	}).Return(nil)

	mockStorage.On("RevertedVolume", mock.Anything, volume, snapshot).Return([]*datamodel.Snapshot{}, nil)

	// Act
	_, err := env.ExecuteActivity(activity.RevertVolume, volume, snapshot, node, params)

	// Assert
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestVolumeRevertActivity_RevertVolume_GetProviderByNodeFailure(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)

	// Save original function and restore after test
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	// Mock GetProviderByNode to return error
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("failed to get provider")
	}

	activity := VolumeRevertActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RevertVolume)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "external-snapshot-uuid"},
	}

	node := &models.Node{
		Name: "test-node",
	}

	params := vsa.RevertVolumeParams{
		VolumeID:   "external-volume-uuid",
		SnapshotID: "external-snapshot-uuid",
	}

	// Act
	_, err := env.ExecuteActivity(activity.RevertVolume, volume, snapshot, node, params)

	// Assert
	assert.Error(t, err)
}

func TestVolumeRevertActivity_RevertVolume_RevertVolumeFailure(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)

	// Save original function and restore after test
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeRevertActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RevertVolume)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "external-snapshot-uuid"},
	}

	node := &models.Node{
		Name: "test-node",
	}

	params := vsa.RevertVolumeParams{
		VolumeID:   "external-volume-uuid",
		SnapshotID: "external-snapshot-uuid",
	}

	expectedError := errors.New("volume revert failed")

	// Setup mocks
	mockProvider.On("RevertVolume", vsa.RevertVolumeParams{
		VolumeID:        "external-volume-uuid",
		SnapshotID:      "external-snapshot-uuid",
		SnapshotName:    "test-snapshot",
		SvmName:         "test-svm",
		PreRevertVolume: volume,
	}).Return(expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.RevertVolume, volume, snapshot, node, params)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
}

func TestVolumeRevertActivity_RevertVolume_RevertedVolumeFailure(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)

	// Save original function and restore after test
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	activity := VolumeRevertActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RevertVolume)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "external-snapshot-uuid"},
	}

	node := &models.Node{
		Name: "test-node",
	}

	params := vsa.RevertVolumeParams{
		VolumeID:   "external-volume-uuid",
		SnapshotID: "external-snapshot-uuid",
	}

	expectedError := errors.New("failed to update volume in database")

	// Setup mocks
	mockProvider.On("RevertVolume", vsa.RevertVolumeParams{
		VolumeID:        "external-volume-uuid",
		SnapshotID:      "external-snapshot-uuid",
		SnapshotName:    "test-snapshot",
		SvmName:         "test-svm",
		PreRevertVolume: volume,
	}).Return(nil)

	mockStorage.On("RevertedVolume", mock.Anything, volume, snapshot).Return([]*datamodel.Snapshot{}, expectedError)

	// Act
	_, err := env.ExecuteActivity(activity.RevertVolume, volume, snapshot, node, params)

	// Assert
	assert.Error(t, err)
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestVolumeRevertActivity_RevertVolume_HydrationFailure(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	mockProvider := new(vsa.MockProvider)

	// Save original function and restore after test
	originalGetProviderByNode := vsa.GetProviderByNode
	defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

	// Mock GetProviderByNode to return the mock provider
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return mockProvider, nil
	}

	// Save original hydration function and restore after test
	originalHydrateBatchSnapshotstoCCFE := hydrationActivities.HydrateBatchSnapshotstoCCFE
	defer func() { hydrationActivities.HydrateBatchSnapshotstoCCFE = originalHydrateBatchSnapshotstoCCFE }()

	// Mock HydrateBatchSnapshotstoCCFE to return an error
	hydrationActivities.HydrateBatchSnapshotstoCCFE = func(ctx context.Context, createdSnapshots []*datamodel.Snapshot, deletedSnapshots []*datamodel.Snapshot) error {
		return errors.New("hydration failed")
	}

	activity := VolumeRevertActivity{
		SE: mockStorage,
	}

	// Create Temporal test environment for activity context
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.RevertVolume)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "volume-uuid",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-volume-uuid",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
	}

	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "snapshot-uuid",
		},
		Name:               "test-snapshot",
		ExternalUUID:       "external-snapshot-uuid",
		SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "external-snapshot-uuid"},
	}

	node := &models.Node{
		Name: "test-node",
	}

	params := vsa.RevertVolumeParams{
		VolumeID:   "external-volume-uuid",
		SnapshotID: "external-snapshot-uuid",
	}

	// Create some snapshots to be returned by RevertedVolume
	returnedSnapshots := []*datamodel.Snapshot{
		{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "returned-snapshot-uuid",
			},
			Name: "returned-snapshot",
		},
	}

	// Setup mocks
	mockProvider.On("RevertVolume", vsa.RevertVolumeParams{
		VolumeID:        "external-volume-uuid",
		SnapshotID:      "external-snapshot-uuid",
		SnapshotName:    "test-snapshot",
		SvmName:         "test-svm",
		PreRevertVolume: volume,
	}).Return(nil)

	mockStorage.On("RevertedVolume", mock.Anything, volume, snapshot).Return(returnedSnapshots, nil)

	// Act
	_, err := env.ExecuteActivity(activity.RevertVolume, volume, snapshot, node, params)

	// Assert - should not return error even if hydration fails (lines 52-54)
	assert.NoError(t, err)
	mockProvider.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

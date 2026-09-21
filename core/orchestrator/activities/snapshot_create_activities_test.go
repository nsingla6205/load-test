package activities_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
)

func TestCreateSnapshotInONTAP(t *testing.T) {
	t.Run("WhenSnapshotIsCreatedSuccessfully", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := activities.SnapshotCreateActivity{}
		snapshot := &datamodel.Snapshot{
			Name:        "test-snapshot",
			Description: "test-description",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
				},
			},
		}
		node := &models.Node{}
		expectedResponse := &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				Name:         "snapshot-uuid",
				ExternalUUID: "abcdef-123456",
			},
			SizeInBytes: 1024,
		}

		mockProvider.On("CreateSnapshot", vsa.CreateSnapshotParams{
			VolumeUUID: "volume-uuid",
			Name:       "test-snapshot",
			Comment:    "test-description",
		}).Return(expectedResponse, nil)

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateSnapshotInONTAP)

		encodedValue, _ := env.ExecuteActivity(activity.CreateSnapshotInONTAP, snapshot, node)

		var result *vsa.SnapshotProviderResponse
		err := encodedValue.Get(&result)
		assert.NoError(t, err)

		assert.NoError(t, err)
		assert.Equal(t, expectedResponse, result)
		mockProvider.AssertExpectations(t)
	})

	t.Run("WhenSnapshotCreationFails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := activities.SnapshotCreateActivity{}
		snapshot := &datamodel.Snapshot{
			Name:        "test-snapshot",
			Description: "test-description",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
				},
			},
		}
		node := &models.Node{}
		expectedError := errors.New("failed to create snapshot")

		mockProvider.On("CreateSnapshot", vsa.CreateSnapshotParams{
			VolumeUUID: "volume-uuid",
			Name:       "test-snapshot",
			Comment:    "test-description",
		}).Return(nil, expectedError)

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateSnapshotInONTAP)

		_, err := env.ExecuteActivity(activity.CreateSnapshotInONTAP, snapshot, node)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), expectedError.Error())
		mockProvider.AssertExpectations(t)
	})

	t.Run("WhenGetProviderByNodeFails", func(t *testing.T) {
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider by node")
		}

		activity := activities.SnapshotCreateActivity{}
		snapshot := &datamodel.Snapshot{
			Name:        "test-snapshot",
			Description: "test-description",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "volume-uuid",
				},
			},
		}
		node := &models.Node{}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateSnapshotInONTAP)

		_, err := env.ExecuteActivity(activity.CreateSnapshotInONTAP, snapshot, node)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get provider by node")
	})
}

func TestUpdateSnapshotDetails(t *testing.T) {
	t.Run("WhenUpdateIsSuccessful", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.SnapshotCreateActivity{SE: mockStorage}
		snapshot := &datamodel.Snapshot{
			Name:               "test-snapshot",
			Description:        "test-description",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}

		// Mock UpdateSnapshot to accept any snapshot with matching state after modification.
		// The external UUID must reach both the column and the attributes JSON.
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.MatchedBy(func(s *datamodel.Snapshot) bool {
			return s.State == datamodel.LifeCycleStateREADY &&
				s.StateDetails == datamodel.LifeCycleStateAvailableDetails &&
				s.ExternalUUID == "abcdef-123456" &&
				s.SnapshotAttributes.ExternalUUID == "abcdef-123456"
		})).Return(nil, nil)

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateSnapshotDetails)

		_, err := env.ExecuteActivity(activity.UpdateSnapshotDetails, snapshot, &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "abcdef-123456",
				Name:         "test-snapshot",
			},
			SizeInBytes:        1024,
			LogicalSizeInBytes: 1024,
		})

		assert.NoError(t, err)
		// Note: When using Temporal's test environment, the snapshot is serialized/deserialized,
		// so modifications inside the activity don't affect the original object.
		// The correctness is verified through the mock expectations above.
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenUpdateFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.SnapshotCreateActivity{SE: mockStorage}
		snapshot := &datamodel.Snapshot{
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		expectedError := errors.New("failed to update snapshot")

		mockStorage.On("UpdateSnapshot", mock.Anything, mock.Anything).Return(nil, expectedError)

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateSnapshotDetails)

		_, err := env.ExecuteActivity(activity.UpdateSnapshotDetails, snapshot, &vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "abcdef-123456",
				Name:         "test-snapshot",
			},
			SizeInBytes:        1024,
			LogicalSizeInBytes: 1024,
		})

		assert.Error(t, err)
		assert.Contains(t, err.Error(), expectedError.Error())
		mockStorage.AssertExpectations(t)
	})

	t.Run("WhenUpdateIsSuccessfulWithError", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := activities.SnapshotCreateActivity{SE: mockStorage}
		snapshot := &datamodel.Snapshot{
			Name:         "test-snapshot",
			Description:  "test-description",
			ExternalUUID: "abcdef-123456",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            1024,
				LogicalSizeUsedInBytes: 1024,
			},
		}

		// Mock UpdateSnapshot to accept any snapshot with ERROR state after modification
		mockStorage.On("UpdateSnapshot", mock.Anything, mock.MatchedBy(func(s *datamodel.Snapshot) bool {
			return s.State == datamodel.LifeCycleStateError && s.StateDetails == datamodel.LifeCycleStateCreationErrorDetails
		})).Return(nil, nil)

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateSnapshotDetails)

		_, err := env.ExecuteActivity(activity.UpdateSnapshotDetails, snapshot, nil)

		assert.NoError(t, err)
		// Note: When using Temporal's test environment, the snapshot is serialized/deserialized,
		// so modifications inside the activity don't affect the original object.
		// The correctness is verified through the mock expectations above.
		mockStorage.AssertExpectations(t)
	})
}

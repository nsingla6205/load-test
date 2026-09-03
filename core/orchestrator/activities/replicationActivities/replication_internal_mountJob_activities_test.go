package replicationActivities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	active_directory_activities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"gorm.io/gorm"
)

func TestCheckMountJob(t *testing.T) {
	t.Run("ReturnsErrorWhenGetVolumeReplicationFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, errors.New("failed to get volume replication"))
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get volume replication")
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsNilWhenMirrorStateIsSnapmirrored", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{MirrorState: "snapmirrored", RelationshipStatus: "idle"}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})
	// VSCP-7686: mirror state snapmirrored is terminal for the mount job even when a
	// transfer is still in progress, so a non-idle relationship status must not block it.
	t.Run("ReturnsNilWhenMirrorStateIsSnapmirroredAndRelationshipStatusIsTransferring", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{
			MirrorState:        datamodel.OntapSnapmirrored,
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
		}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsErrorWhenGetProviderByNodeError", func(tt *testing.T) {
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password:      "password-1",
						SecretID:      "",
						CertificateID: "",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get provider")
	})
	t.Run("ReturnsErrorWhenMirrorStateIsNotSnapmirrored", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{MirrorState: "initializing"}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "replication is not in snapmirrored state yet")
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsNonRetryableErrorWhenUnhealthyReasonIsNotEmpty", func(tt *testing.T) {
		unhealthyReason := "Failed to create snapshot snapmirror.cf577972-ebc5-11f0-9e62-1f0c28031e5c_2160302447.2026-01-21_044853 on volume gcnv-2df2feed3ab3bef-svm-01:mdvol103."
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{
			UnhealthyReason: unhealthyReason,
		}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		// Use start time past retry window so activity returns non-retryable error
		checkMountStart := time.Now().Add(-mountJobRetryWindow - time.Minute)
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", checkMountStart)

		assert.Error(tt, err)
		assert.True(tt, utilErrors.IsNonRetryableErr(err), "Expected non-retryable error")
		assert.Contains(tt, err.Error(), unhealthyReason)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsRetryableApplicationErrorWhenScheduledUpdateOrSnapshotErrorWithinRetryWindow", func(tt *testing.T) {
		unhealthyReason := "Scheduled update failed."
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{
			UnhealthyReason: unhealthyReason,
		}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		// Use current time so we are within retry window -> activity returns retryable ApplicationError (line 49)
		checkMountStart := time.Now()
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", checkMountStart)

		assert.Error(tt, err)
		assert.False(tt, utilErrors.IsNonRetryableErr(err), "Expected retryable error (ApplicationError), not non-retryable")
		assert.Contains(tt, err.Error(), "Retrying mount job due to scheduled update failure or snapshot creation error")
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsNilWhenUnhealthyReasonContainsTransferAbortedAndNoCurrentTransfer", func(tt *testing.T) {
		unhealthyReason := "Transfer aborted."
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{
			UnhealthyReason:     unhealthyReason,
			CurrentTransferType: "",
		}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("ReturnsNilWhenUnhealthyReasonIsEmpty", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetVolumeReplication", mock.Anything).Return(&vsa.VolumeReplication{
			UnhealthyReason:    "",
			MirrorState:        "snapmirrored",
			RelationshipStatus: "idle",
		}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbRep := &datamodel.VolumeReplication{
			Account: &datamodel.Account{Name: "test-account"},
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "replication-uuid-1",
			},
			AccountID: 1,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					ID:   1,
					UUID: "volume-uuid-1",
				},
				PoolID: 1,
				Pool: &datamodel.Pool{
					PoolCredentials: &datamodel.PoolCredentials{
						Password: "password-1",
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeName: "destination-volume-name-1",
				DestinationHostName:   "destination-host-name-1",
				DestinationSvmName:    "destination-svm-name-1",
				ExternalUUID:          "external-uuid-1",
				ReplicationSchedule:   "10minutely",
			},
		}
		err := activity.CheckMountJob(context.Background(), dbRep, node, "test-account", time.Now())

		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})
}

func TestGetReplicationFromOntap(t *testing.T) {
	t.Run("ReturnsReplicationWhenGetReplicationDetailsSucceeds", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetReplicationDetails", mock.Anything, mock.Anything).Return(&vsa.VolumeReplication{}, nil)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbReplication := &datamodel.VolumeReplication{
			Account:               &datamodel.Account{Name: "test-account"},
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		replication, err := activity.GetReplicationFromOntap(context.Background(), dbReplication, node, "test-account")

		assert.NoError(tt, err)
		assert.NotNil(tt, replication)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenGetReplicationDetailsFails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("GetReplicationDetails", mock.Anything, mock.Anything).Return(nil, errors.New("failed to get replication details"))
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := &MountJobActivity{}
		node := &models.Node{}
		dbReplication := &datamodel.VolumeReplication{
			Account:               &datamodel.Account{Name: "test-account"},
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}

		replication, err := activity.GetReplicationFromOntap(context.Background(), dbReplication, node, "test-account")

		assert.Error(tt, err)
		assert.Nil(tt, replication)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "failed to get replication details")
		mockProvider.AssertExpectations(tt)
	})
}

func TestUpdateReplicationInDB(t *testing.T) {
	t.Run("ReturnsNilWhenUpdateVolumeReplicationTransferStatsSucceeds", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplicationTransferStats", mock.Anything, mock.Anything).Return(nil)

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: nil,
				},
			},
		}
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
			},
		}

		err := activity.UpdateReplicationInDB(context.Background(), replication, lunDetails)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenUpdateVolumeReplicationTransferStatsFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplicationTransferStats", mock.Anything, mock.Anything).Return(errors.New("update failed"))

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					FileProperties: nil,
				},
			},
		}
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
			},
		}

		err := activity.UpdateReplicationInDB(context.Background(), replication, lunDetails)

		assert.Error(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "update failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenLunDetailsIsNil", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplication", mock.Anything, mock.Anything).Return(nil)

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols: []string{"ISCSI"},
				},
			},
		}

		err := activity.UpdateReplicationInDB(context.Background(), replication, nil)

		assert.Error(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateError, replication.State)
		assert.Contains(tt, replication.StateDetails, "zero or multiple LUNs found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenLunDetailsHasMultipleLUNs", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplication", mock.Anything, mock.Anything).Return(nil)

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols: []string{"ISCSI"},
				},
			},
		}
		lunDetails := []*vsa.LunResponse{
			{ProviderResponse: vsa.ProviderResponse{Name: "/test/vol/lun1"}},
			{ProviderResponse: vsa.ProviderResponse{Name: "/test/vol/lun2"}},
		}

		err := activity.UpdateReplicationInDB(context.Background(), replication, lunDetails)

		assert.Error(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateError, replication.State)
		assert.Contains(tt, replication.StateDetails, "zero or multiple LUNs found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenLunDetailsIsEmpty", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplication", mock.Anything, mock.Anything).Return(nil)

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols: []string{"ISCSI"},
				},
			},
		}
		lunDetails := []*vsa.LunResponse{}

		err := activity.UpdateReplicationInDB(context.Background(), replication, lunDetails)

		assert.Error(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateError, replication.State)
		assert.Contains(tt, replication.StateDetails, "zero or multiple LUNs found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenUpdateVolumeReplicationFailsOnValidationError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("UpdateVolumeReplication", mock.Anything, mock.Anything).Return(errors.New("db update failed"))

		activity := &MountJobActivity{SE: mockStorage}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols: []string{"ISCSI"},
				},
			},
		}

		err := activity.UpdateReplicationInDB(context.Background(), replication, nil)

		assert.Error(tt, err)
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "db update failed")
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetReplication(t *testing.T) {
	t.Run("GetReplicationSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		uuid := "uuid"
		expectedReplication := &datamodel.VolumeReplication{}

		mockStorage.On("GetVolumeReplication", ctx, uuid).Return(expectedReplication, nil)

		replication, err := activity.GetReplication(ctx, uuid)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedReplication, replication)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("GetReplicationError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		uuid := "uuid"

		mockStorage.On("GetVolumeReplication", ctx, uuid).Return(nil, gorm.ErrInvalidDB)

		node, err := activity.GetReplication(ctx, uuid)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, node)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetLunDetailsFromOntap(t *testing.T) {
	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider by node")
		}
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{}
		_, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.Error(tt, err)
	})
	t.Run("WhenGetLunFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, errors.New("failed to get LUN"))
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		_, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		lunDetails := &vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{
				Name:         "/test/vol/lun_vol",
				ExternalUUID: "lun-uuid",
			},
			Size:         1073741824,
			SerialNumber: "123412214",
		}
		mockProvider.On("LunList", mock.Anything).Return([]*vsa.LunResponse{lunDetails}, nil)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					BlockDevices: &[]datamodel.BlockDevice{{}},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Equal(tt, 1, len(res))
		assert.Equal(tt, lunDetails, res[0])
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenVolumeAttributesContainsLun", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		lunDetails := &vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{
				Name:         "/test/vol/lun_vol",
				ExternalUUID: "lun-uuid",
			},
			Size:         1073741824,
			SerialNumber: "123412214",
		}
		// GetLunDetailsFromOntap only passes SvmName and VolumeName to LunList (LunName is not set)
		lunParams := vsa.LunGetParams{
			SvmName:    "svm-name",
			VolumeName: "volume-name",
			LunName:    "",
		}
		mockProvider.On("LunList", lunParams).Return([]*vsa.LunResponse{lunDetails}, nil)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					BlockDevices: &[]datamodel.BlockDevice{{
						Name: "lun_source-volume-name1",
					}},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Equal(tt, 1, len(res))
		assert.Equal(tt, lunDetails, res[0])
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenGetLunReturnsMultipleLUNs_Succeeds", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		// Return multiple LUNs - GetLunDetailsFromOntap should return them successfully
		// Validation of count happens in UpdateReplicationInDB, not here
		lun1 := &vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{
				Name:         "/test/vol/lun1",
				ExternalUUID: "lun-uuid-1",
			},
		}
		lun2 := &vsa.LunResponse{
			ProviderResponse: vsa.ProviderResponse{
				Name:         "/test/vol/lun2",
				ExternalUUID: "lun-uuid-2",
			},
		}
		expectedLUNs := []*vsa.LunResponse{lun1, lun2}
		mockProvider.On("LunList", mock.Anything).Return(expectedLUNs, nil)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		assert.Equal(tt, 2, len(res))
		assert.Equal(tt, expectedLUNs, res)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetLunFailsWithNotFoundErr_ReturnsNilNil", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		notFoundErr := utilErrors.NewNotFoundErr("LUN", nil)
		customErr := &errors2.CustomError{
			OriginalErr: notFoundErr,
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, customErr)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.Nil(tt, res)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenGetLunFailsWithUnexpectedResponseError_ReturnsError", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		originalErr := errors.New("multiple LUNs are not supported")
		customErr := &errors2.CustomError{
			OriginalErr: originalErr,
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, customErr)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		_, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenGetLunFailsWithNotFoundErr_ReturnsNilNil_NoAbortBreak", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		// Return NotFound error - should return nil, nil without calling abort/break
		notFoundErr := utilErrors.NewNotFoundErr("LUN", nil)
		customErr := &errors2.CustomError{
			OriginalErr: notFoundErr,
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, customErr)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.Nil(tt, res)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenGetLunFailsWithNotFoundErr_ReturnsNilNil_NoBreak", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		// Return NotFound error - should return nil, nil without calling break
		notFoundErr := utilErrors.NewNotFoundErr("LUN", nil)
		customErr := &errors2.CustomError{
			OriginalErr: notFoundErr,
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, customErr)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.Nil(tt, res)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenGetLunFailsWithNotFoundErr_ReturnsNilNil_NoUpdate", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		// Return NotFound error - should return nil, nil without calling update
		notFoundErr := utilErrors.NewNotFoundErr("LUN", nil)
		customErr := &errors2.CustomError{
			OriginalErr: notFoundErr,
		}
		mockProvider.On("LunList", mock.Anything).Return(nil, customErr)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		res, err := activity.GetLunDetailsFromOntap(ctx, replication, node)
		assert.NoError(tt, err)
		assert.Nil(tt, res)
		mockProvider.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateVolumeLunDetailsInDB(t *testing.T) {
	t.Run("WhenUpdateVolumeFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
				Size:         1073741824,
				SerialNumber: "123412214",
			},
		}
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					BlockDevices: &[]datamodel.BlockDevice{{}},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
			},
		}
		mockStorage.On("UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update volume"))
		err := activity.UpdateVolumeDetailsInDB(ctx, replication, lunDetails)
		assert.Error(tt, err)
	})
	t.Run("WhenSuccess_UpdatesBlockDevicesAndMountStatus", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockStorage := database.NewMockStorage(tt)
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
				Size:         1073741824,
				SerialNumber: "123412214",
				OSType:       "LINUX",
			},
		}
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:    []string{"ISCSI"}, // Required for block device update
					BlockDevices: &[]datamodel.BlockDevice{{}},
					Mounted:      false, // Initially not mounted
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
				DestinationVolumeUUID: "dest-volume-uuid",
			},
		}

		// Capture the updates made to the volume
		var capturedUpdates map[string]interface{}
		mockStorage.On("UpdateVolumeFields", ctx, "dest-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
			capturedUpdates = updates
			return true
		})).Return(nil)

		err := activity.UpdateVolumeDetailsInDB(ctx, replication, lunDetails)
		assert.NoError(tt, err)

		// Verify the updates
		volumeAttrs, ok := capturedUpdates["volume_attributes"].(*datamodel.VolumeAttributes)
		assert.True(tt, ok, "volume_attributes should be present in updates")
		assert.True(tt, volumeAttrs.Mounted, "Mounted flag should be set to true")

		// Verify BlockDevice updates
		assert.NotNil(tt, volumeAttrs.BlockDevices)
		blockDevices := *volumeAttrs.BlockDevices
		assert.Equal(tt, 1, len(blockDevices))
		assert.Equal(tt, "lun_vol", blockDevices[0].Name)
		assert.Equal(tt, int64(1073741824), blockDevices[0].Size)
		assert.Equal(tt, "lun-uuid", blockDevices[0].LunUUID)
		assert.Equal(tt, "123412214", blockDevices[0].Identifier)
		assert.Equal(tt, "LINUX", blockDevices[0].OSType)
	})
	t.Run("WhenSuccess_NonISCSIProtocol_OnlyUpdatesMountStatus", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:    []string{"NFSV3"}, // Non-ISCSI protocol
					BlockDevices: &[]datamodel.BlockDevice{{}},
					Mounted:      false, // Initially not mounted
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
				DestinationVolumeUUID: "dest-volume-uuid",
			},
		}
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
				Size:         1073741824,
				SerialNumber: "123412214",
			},
		}

		// Capture the updates made to the volume
		var capturedUpdates map[string]interface{}
		mockStorage.On("UpdateVolumeFields", ctx, "dest-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
			capturedUpdates = updates
			return true
		})).Return(nil)

		err := activity.UpdateVolumeDetailsInDB(ctx, replication, lunDetails)
		assert.NoError(tt, err)

		// Verify the updates
		volumeAttrs, ok := capturedUpdates["volume_attributes"].(*datamodel.VolumeAttributes)
		assert.True(tt, ok, "volume_attributes should be present in updates")
		assert.True(tt, volumeAttrs.Mounted, "Mounted flag should be set to true")

		// Verify BlockDevices are NOT updated for non-ISCSI protocols
		// The original BlockDevices should remain unchanged
		assert.NotNil(tt, volumeAttrs.BlockDevices)
		blockDevices := *volumeAttrs.BlockDevices
		assert.Equal(tt, 1, len(blockDevices))
		// The block device should remain empty (not updated with lunDetails)
		assert.Equal(tt, "", blockDevices[0].Name)
		assert.Equal(tt, int64(0), blockDevices[0].Size)
	})
	t.Run("WhenSuccess_ProtocolsNil_OnlyUpdatesMountStatus", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					Protocols:    nil, // Protocols is nil
					BlockDevices: &[]datamodel.BlockDevice{{}},
					Mounted:      false, // Initially not mounted
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName:    "svm-name",
				DestinationVolumeName: "volume-name",
				SourceVolumeName:      "source-volume-name",
				DestinationVolumeUUID: "dest-volume-uuid",
			},
		}
		lunDetails := []*vsa.LunResponse{
			{
				ProviderResponse: vsa.ProviderResponse{
					Name:         "/test/vol/lun_vol",
					ExternalUUID: "lun-uuid",
				},
				Size:         1073741824,
				SerialNumber: "123412214",
			},
		}

		// Capture the updates made to the volume
		var capturedUpdates map[string]interface{}
		mockStorage.On("UpdateVolumeFields", ctx, "dest-volume-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
			capturedUpdates = updates
			return true
		})).Return(nil)

		err := activity.UpdateVolumeDetailsInDB(ctx, replication, lunDetails)
		assert.NoError(tt, err)

		// Verify the updates
		volumeAttrs, ok := capturedUpdates["volume_attributes"].(*datamodel.VolumeAttributes)
		assert.True(tt, ok, "volume_attributes should be present in updates")
		assert.True(tt, volumeAttrs.Mounted, "Mounted flag should be set to true")

		// Verify BlockDevices are NOT updated when Protocols is nil
		assert.NotNil(tt, volumeAttrs.BlockDevices)
		blockDevices := *volumeAttrs.BlockDevices
		assert.Equal(tt, 1, len(blockDevices))
		// The block device should remain empty (not updated with lunDetails)
		assert.Equal(tt, "", blockDevices[0].Name)
	})
}

func TestMountVolume(t *testing.T) {
	// Setup context for tests
	ctx := context.Background()

	t.Run("Success_MountsVolumeWithCorrectJunctionPath", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		// Setup mock provider
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Create test data
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenGetProviderByNodeFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		expectedError := errors.New("failed to get provider")
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, expectedError
		}

		// Create test data
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get provider")
	})

	t.Run("Error_WhenProviderMountVolumeFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		// Setup mock provider
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		providerError := errors.New("failed to mount volume in ONTAP")
		mockProvider.On("MountVolume", expectedMountParams).Return(nil, providerError)

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Create test data
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to mount volume in ONTAP")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Success_WithEmptyCreationToken", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		// Setup mock provider
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/", // Empty creation token results in "/" junction path
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Create test data with empty creation token
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "", // Empty creation token
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Success_WithComplexCreationToken", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		// Setup mock provider
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid-complex",
			JunctionPath: "/my-complex-volume-name-123",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Create test data with complex creation token
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "complex-test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid-complex",
					CreationToken: "my-complex-volume-name-123",
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "192.168.1.100",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Success_SMBVolume_CreatesCifsShare", func(tt *testing.T) {
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
			vsa.GetProviderByNode = originalGetProviderByNode
			vsa.SetTestHooks(vsa.TestHooks{})
		}()

		// Setup mock ONTAP client for CreateJunctionPathForCifsShare
		mockClient := new(ontapRest.MockRESTClient)
		mockNAS := new(ontapRest.MockNASClient)
		mockClient.On("NAS").Return(mockNAS)
		mockNAS.On("CifsShareCreate", mock.MatchedBy(func(params *ontapRest.CifsShareCreateParams) bool {
			return params != nil && params.SvmName != nil && *params.SvmName == "destination-svm-name" &&
				params.Path == "/test-creation-token" && params.Name == "test-creation-token"
		})).Return(nil)

		// Set up test hooks for ONTAP client
		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockClient, nil
			},
		})
		defer cleanupHooks()

		// Setup mock provider for MountVolume
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		// Create OntapRestProvider for CreateJunctionPathForCifsShare
		ontapProvider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Override vsa.GetProviderByNode to return OntapRestProvider for getOntapRestProvider
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return ontapProvider, nil
		}

		// Create test data with SMB protocol
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
					Protocols:     []string{"SMB"},
					FileProperties: &datamodel.FileProperties{
						SMBShareSettings: []string{"browsable", "oplocks"},
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName: "destination-svm-name",
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Mock CreateJunctionPathForCifsShare by wrapping MountVolume in a test activity
		// This allows us to execute it through the Temporal test environment which provides activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		adActivity := active_directory_activities.ActiveDirectoryActivity{}
		env.RegisterActivity(adActivity.CreateJunctionPathForCifsShare)

		// Create a test activity that wraps MountVolume
		// This allows us to execute it through the test environment to get activity context
		// The function signature must match what ExecuteActivity expects
		mountVolumeWrapper := func(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
			mountActivity := &MountJobActivity{}
			return mountActivity.MountVolume(ctx, replication, node)
		}
		env.RegisterActivity(mountVolumeWrapper)

		// Execute through test environment to get activity context for CreateJunctionPathForCifsShare
		// When MountVolume calls CreateJunctionPathForCifsShare, it will have the activity context
		// ExecuteActivity automatically provides the context as the first parameter
		_, err := env.ExecuteActivity(mountVolumeWrapper, replication, node)

		// Verify results
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
		mockNAS.AssertExpectations(tt)
	})

	t.Run("Success_NonSMBVolume_DoesNotCreateCifsShare", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		// Setup mock provider
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Create test data with NFS protocol (non-SMB)
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
					Protocols:     []string{"NFSV3"},
				},
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Execute test
		activity := &MountJobActivity{}
		err := activity.MountVolume(ctx, replication, node)

		// Verify results - should succeed without creating CIFS share
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_SMBVolume_CreateCifsShareFails", func(tt *testing.T) {
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
			vsa.GetProviderByNode = originalGetProviderByNode
			vsa.SetTestHooks(vsa.TestHooks{})
		}()

		// Setup mock ONTAP client that fails
		mockClient := new(ontapRest.MockRESTClient)
		mockNAS := new(ontapRest.MockNASClient)
		mockClient.On("NAS").Return(mockNAS)
		mockNAS.On("CifsShareCreate", mock.Anything).Return(errors.New("failed to create CIFS share"))

		// Set up test hooks for ONTAP client
		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockClient, nil
			},
		})
		defer cleanupHooks()

		// Setup mock provider for MountVolume
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		// Create OntapRestProvider for CreateJunctionPathForCifsShare
		ontapProvider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Override vsa.GetProviderByNode to return OntapRestProvider for getOntapRestProvider
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return ontapProvider, nil
		}

		// Create test data with SMB protocol
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
					Protocols:     []string{"SMB"},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName: "destination-svm-name",
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Mock CreateJunctionPathForCifsShare by wrapping MountVolume in a test activity
		// This allows us to execute it through the Temporal test environment which provides activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		adActivity := active_directory_activities.ActiveDirectoryActivity{}
		env.RegisterActivity(adActivity.CreateJunctionPathForCifsShare)

		// Create a test activity that wraps MountVolume
		// This allows us to execute it through the test environment to get activity context
		mountVolumeWrapper := func(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
			mountActivity := &MountJobActivity{}
			return mountActivity.MountVolume(ctx, replication, node)
		}
		env.RegisterActivity(mountVolumeWrapper)

		// Execute through test environment to get activity context for CreateJunctionPathForCifsShare
		// When MountVolume calls CreateJunctionPathForCifsShare, it will have the activity context
		// ExecuteActivity automatically provides the context as the first parameter
		_, err := env.ExecuteActivity(mountVolumeWrapper, replication, node)

		// Verify results - should fail with CIFS share creation error (classified as SMB domain error)
		assert.Error(tt, err)
		var appErr *temporal.ApplicationError
		if assert.True(tt, errors.As(err, &appErr)) {
			expected := errors2.GetErrorMessageByTrackingID(errors2.ErrSMBUnclassified).Message
			assert.Equal(tt, expected, appErr.Message())
		}
		mockProvider.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
		mockNAS.AssertExpectations(tt)
	})

	t.Run("Success_SMBVolume_WithSMBShareProperties", func(tt *testing.T) {
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
			vsa.GetProviderByNode = originalGetProviderByNode
			vsa.SetTestHooks(vsa.TestHooks{})
		}()

		// Setup mock ONTAP client for CreateJunctionPathForCifsShare
		mockClient := new(ontapRest.MockRESTClient)
		mockNAS := new(ontapRest.MockNASClient)
		mockClient.On("NAS").Return(mockNAS)
		expectedSMBProperties := []string{"browsable", "encrypt_data", "oplocks"}
		mockNAS.On("CifsShareCreate", mock.MatchedBy(func(params *ontapRest.CifsShareCreateParams) bool {
			if params == nil || params.SvmName == nil || *params.SvmName != "destination-svm-name" {
				return false
			}
			if params.Path != "/test-creation-token" || params.Name != "test-creation-token" {
				return false
			}
			// Check if share properties match (order may differ)
			if len(params.ShareProperties) != len(expectedSMBProperties) {
				return false
			}
			propsMap := make(map[string]bool)
			for _, prop := range params.ShareProperties {
				propsMap[prop] = true
			}
			for _, expectedProp := range expectedSMBProperties {
				if !propsMap[expectedProp] {
					return false
				}
			}
			return true
		})).Return(nil)

		// Set up test hooks for ONTAP client
		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockClient, nil
			},
		})
		defer cleanupHooks()

		// Setup mock provider for MountVolume
		mockProvider := new(vsa.MockProvider)
		expectedMountParams := vsa.MountVolumeParams{
			UUID:         "external-volume-uuid",
			JunctionPath: "/test-creation-token",
		}
		mockProvider.On("MountVolume", expectedMountParams).Return(&vsa.OntapAsyncResponse{}, nil)

		// Create OntapRestProvider for CreateJunctionPathForCifsShare
		ontapProvider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Override vsa.GetProviderByNode to return OntapRestProvider for getOntapRestProvider
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return ontapProvider, nil
		}

		// Create test data with SMB protocol and SMB share properties
		replication := &datamodel.VolumeReplication{
			Volume: &datamodel.Volume{
				Name: "test-volume",
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID:  "external-volume-uuid",
					CreationToken: "test-creation-token",
					Protocols:     []string{"SMB"},
					FileProperties: &datamodel.FileProperties{
						SMBShareSettings: expectedSMBProperties,
					},
				},
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationSvmName: "destination-svm-name",
			},
		}
		node := &models.Node{
			EndpointAddress: "127.0.0.1",
		}

		// Mock CreateJunctionPathForCifsShare by wrapping MountVolume in a test activity
		// This allows us to execute it through the Temporal test environment which provides activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		adActivity := active_directory_activities.ActiveDirectoryActivity{}
		env.RegisterActivity(adActivity.CreateJunctionPathForCifsShare)

		// Create a test activity that wraps MountVolume
		// This allows us to execute it through the test environment to get activity context
		mountVolumeWrapper := func(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
			mountActivity := &MountJobActivity{}
			return mountActivity.MountVolume(ctx, replication, node)
		}
		env.RegisterActivity(mountVolumeWrapper)

		// Execute through test environment to get activity context for CreateJunctionPathForCifsShare
		// When MountVolume calls CreateJunctionPathForCifsShare, it will have the activity context
		// ExecuteActivity automatically provides the context as the first parameter
		_, err := env.ExecuteActivity(mountVolumeWrapper, replication, node)

		// Verify results
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
		mockClient.AssertExpectations(tt)
		mockNAS.AssertExpectations(tt)
	})
}

func TestAbortVolumeReplicationForMount(t *testing.T) {
	t.Run("Success_AbortsVolumeReplication", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
			TransferUUID:       "transfer-uuid",
			RelationshipID:     "relationship-id",
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(testReplication, nil)

		err := activity.AbortVolumeReplicationForMount(ctx, replication, node)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenAbortVolumeReplicationFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
			TransferUUID:       "transfer-uuid",
			RelationshipID:     "relationship-id",
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(nil, errors.New("failed to abort"))

		err := activity.AbortVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenGetProviderFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		err := activity.AbortVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
	})

	t.Run("Success_WhenReplicationNotInTransferringState", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)

		err := activity.AbortVolumeReplicationForMount(ctx, replication, node)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenGetVolumeReplicationFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, errors.New("failed to get replication"))

		err := activity.AbortVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})
}

func TestBreakVolumeReplicationForMount(t *testing.T) {
	t.Run("Success_BreaksVolumeReplication", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
			MirrorState:        datamodel.OntapSnapmirrored,
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(testReplication, nil)

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenBreakVolumeReplicationFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
			MirrorState:        datamodel.OntapSnapmirrored,
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(nil, errors.New("failed to break"))

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenGetProviderFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
	})

	t.Run("Success_WhenReplicationIsUninitialized", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
			MirrorState:        datamodel.OntapUninitialized,
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenReplicationIsInTransferringState", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		// BreakVolumeReplicationForMount uses forceStop=true. Transferring + non-empty TransferUUID
		// triggers AbortVolumeReplication; if abort fails, the activity returns an error (it does
		// not return ErrBreakReplicationStateTransferring, which applies only when forceStop=false).
		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipID:     "relationship-id",
			TransferUUID:       "transfer-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
			MirrorState:        datamodel.OntapSnapmirrored,
		}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(nil, errors.New("abort failed"))

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("Error_WhenGetVolumeReplicationFails", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()

		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := &MountJobActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, errors.New("failed to get replication"))

		err := activity.BreakVolumeReplicationForMount(ctx, replication, node)
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})
}

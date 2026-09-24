package replicationActivities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
)

func TestUpdateVolumeToNonDPVolume(t *testing.T) {
	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)

		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeUUID: "destination-volume-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					IsDataProtection: false,
				},
			},
		}
		updates := make(map[string]interface{})
		updates["volume_attributes"] = params.Volume.VolumeAttributes
		mockStorage.On("UpdateVolumeFields", ctx, params.ReplicationAttributes.DestinationVolumeUUID, updates).Return(errors.New("update error"))
		err := activity.UpdateVolumeToNonDPVolume(ctx, params)
		assert.Error(t, err)
		// The error gets wrapped with NewVCPError, so we need to check the wrapped error
		var customErr *vsaerrors.CustomError
		assert.True(t, vsaerrors.As(err, &customErr))
		assert.Equal(t, "update error", customErr.OriginalErr.Error())
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)

		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationVolumeUUID: "destination-volume-uuid",
			},
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					IsDataProtection: false,
				},
			},
		}
		updates := make(map[string]interface{})
		updates["volume_attributes"] = params.Volume.VolumeAttributes
		mockStorage.On("UpdateVolumeFields", ctx, params.ReplicationAttributes.DestinationVolumeUUID, updates).Return(nil)
		err := activity.UpdateVolumeToNonDPVolume(ctx, params)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateVolumeReplicationStopDetails(t *testing.T) {
	t.Run("WhenError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)

		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationVolumeName: "destination-volume-name",
				DestinationSvmName:    "destination-svm-name",
			},
		}
		vsaResumeResponse := &vsa.VolumeReplication{
			MirrorState:           "snapmirrored",
			RelationshipStatus:    "idle",
			TotalTransferBytes:    1000,
			TotalTransferTimeSecs: 60,
			LastTransferSize:      500,
			LastTransferError:     "",
			LastTransferDuration:  30,
		}
		mockStorage.On("UpdateVolumeReplication", ctx, params).Return(errors.New("update error"))
		err := activity.UpdateVolumeReplicationStopDetails(ctx, params, vsaResumeResponse)
		assert.Error(t, err)
		// The error gets wrapped with NewVCPError, so we need to check the wrapped error
		var customErr *vsaerrors.CustomError
		assert.True(t, vsaerrors.As(err, &customErr))
		assert.Equal(t, "update error", customErr.OriginalErr.Error())
		mockStorage.AssertExpectations(tt)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)

		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationVolumeName: "destination-volume-name",
				DestinationSvmName:    "destination-svm-name",
			},
		}
		vsaResumeResponse := &vsa.VolumeReplication{
			MirrorState:           "snapmirrored",
			RelationshipStatus:    "idle",
			TotalTransferBytes:    1000,
			TotalTransferTimeSecs: 60,
			LastTransferSize:      500,
			LastTransferError:     "",
			LastTransferDuration:  30,
		}
		mockStorage.On("UpdateVolumeReplication", ctx, params).Return(nil)
		err := activity.UpdateVolumeReplicationStopDetails(ctx, params, vsaResumeResponse)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetReplicationFromDB(t *testing.T) {
	t.Run("WhenReplicationExists", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.Background()
		replicationUUID := "test-replication-uuid"
		expectedReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "test-replication-uuid",
			},
		}

		mockStorage.On("GetVolumeReplication", ctx, replicationUUID).Return(expectedReplication, nil)

		replication, err := activity.GetReplicationFromDB(ctx, replicationUUID)
		assert.NoError(tt, err)
		assert.NotNil(tt, replication)
		assert.Equal(tt, expectedReplication, replication)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenReplicationDoesNotExist", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.Background()
		replicationUUID := "non-existent-replication-uuid"

		mockStorage.On("GetVolumeReplication", ctx, replicationUUID).Return(nil, errors.NewNotFoundErr("Replication", nil))

		replication, err := activity.GetReplicationFromDB(ctx, replicationUUID)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		// The error gets wrapped with NewVCPError, so we need to check the wrapped error
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "Replication not found", customErr.OriginalErr.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.Background()
		replicationUUID := "test-replication-uuid"

		mockStorage.On("GetVolumeReplication", ctx, replicationUUID).Return(nil, errors.New("database error"))

		replication, err := activity.GetReplicationFromDB(ctx, replicationUUID)
		assert.Error(tt, err)
		assert.Nil(tt, replication)
		// The error gets wrapped with NewVCPError, so we need to check the wrapped error
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "database error", customErr.OriginalErr.Error())
		mockStorage.AssertExpectations(tt)
	})
}

func TestBreakVolumeReplication(t *testing.T) {
	t.Run("BreakVolumeReplicationSucceeds", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{}) // Ensure logger is added to context
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}
		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			MirrorState:        datamodel.OntapBrokenOff,
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, datamodel.OntapBrokenOff, snapmirror.MirrorState)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationLooksUpByPathWhenExternalUUIDIsEmpty", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "",
				SourceSvmName:         "source-svm",
				SourceVolumeName:      "source-volume",
				DestinationSvmName:    "destination-svm",
				DestinationVolumeName: "destination-volume",
			},
		}
		node := &models.Node{}
		testReplication := &vsa.VolumeReplication{
			MirrorState:        datamodel.OntapSnapmirrored,
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplicationFromSrcAndDstPath", mock.MatchedBy(func(v *vsa.VolumeReplication) bool {
			return v.SourcePath() == "source-svm:source-volume" && v.DestinationPath() == "destination-svm:destination-volume"
		})).Return(testReplication, nil)
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, datamodel.OntapBrokenOff, snapmirror.MirrorState)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationSkipsWhenSnapmirrorIsNil", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, nil)

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationSkipsWhenSnapmirrorNotFound", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				SourceSvmName:         "source-svm",
				SourceVolumeName:      "source-volume",
				DestinationSvmName:    "destination-svm",
				DestinationVolumeName: "destination-volume",
			},
		}
		node := &models.Node{}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplicationFromSrcAndDstPath", mock.Anything).
			Return(nil, errors.NewNotFoundErr("snapmirror", nil))

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationSkipsWhenUninitialized", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{}) // Ensure logger is added to context
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}
		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			MirrorState:        datamodel.OntapUninitialized,
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, datamodel.OntapUninitialized, snapmirror.MirrorState)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationFailsToGetDetails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{}) // Ensure logger is added to context
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, errors.New("failed to get details"))

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		// Check that the error is a VCPError with ErrProviderGetVolumeReplication
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrProviderGetVolumeReplication, customErr.TrackingID)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("BreakVolumeReplicationFailsToBreak", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{}) // Ensure logger is added to context
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}
		testReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			MirrorState:        datamodel.OntapBrokenOff,
			RelationshipStatus: datamodel.SnapmirrorRelationshipIdle,
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(nil, errors.New("failed to break replication"))

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		// Break failure is retryable (wrapped as Temporal ApplicationError with tracking ID)
		var appErr *temporal.ApplicationError
		assert.True(tt, vsaerrors.As(err, &appErr))
		var trackingID int
		var errorDetails string
		_ = appErr.Details(&trackingID, &errorDetails)
		assert.Equal(tt, vsaerrors.ErrProviderBreakVolumeReplication, trackingID)
		assert.False(tt, appErr.NonRetryable())
		mockProvider.AssertExpectations(tt)
	})

	t.Run("BreakVolumeReplicationFailsToBreakWithForceStopAndTransferringCallsAbort", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}
		transferringReplication := &vsa.VolumeReplication{
			ExternalUUID:       "external-uuid",
			RelationshipID:     "rel-id",
			TransferUUID:       "transfer-uuid",
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
			MirrorState:        datamodel.OntapSnapmirrored,
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(transferringReplication, nil).Once()
		mockProvider.On("AbortVolumeReplication", mock.MatchedBy(func(v *vsa.VolumeReplication) bool {
			return v.RelationshipID == "rel-id" && v.TransferUUID == "transfer-uuid" && v.RelationshipStatus == datamodel.SnapmirrorRelationshipAborted
		})).Return(transferringReplication, nil).Once()
		mockProvider.On("BreakVolumeReplication", mock.Anything).Return(nil, errors.New("break failed")).Once()

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, true)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		var appErr *temporal.ApplicationError
		assert.True(tt, vsaerrors.As(err, &appErr))
		var trackingID int
		var errorDetails string
		_ = appErr.Details(&trackingID, &errorDetails)
		assert.Equal(tt, vsaerrors.ErrProviderBreakVolumeReplication, trackingID)
		assert.False(tt, appErr.NonRetryable())
		mockProvider.AssertExpectations(tt)
	})

	t.Run("BreakVolumeReplicationFailsWhenTransferring", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.BreakVolumeReplication(ctx, replication, node, false)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		var appErr *temporal.ApplicationError
		assert.True(tt, vsaerrors.As(err, &appErr))
		var trackingID int
		var errorDetails string
		_ = appErr.Details(&trackingID, &errorDetails)
		assert.Equal(tt, vsaerrors.ErrBreakReplicationStateTransferring, trackingID)
		mockProvider.AssertExpectations(tt)
	})
}

func TestAbortVolumeReplication(t *testing.T) {
	t.Run("AbortVolumeReplicationSucceeds", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, datamodel.SnapmirrorRelationshipAborted, snapmirror.RelationshipStatus)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationLooksUpByPathWhenExternalUUIDIsEmpty", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "",
				SourceSvmName:         "source-svm",
				SourceVolumeName:      "source-volume",
				DestinationSvmName:    "destination-svm",
				DestinationVolumeName: "destination-volume",
			},
		}
		node := &models.Node{}
		testReplication := &vsa.VolumeReplication{
			RelationshipStatus: datamodel.SnapmirrorRelationshipTransferring,
			RelationshipID:     "relationship-id",
			TransferUUID:       "transfer-uuid",
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplicationFromSrcAndDstPath", mock.MatchedBy(func(v *vsa.VolumeReplication) bool {
			return v.SourcePath() == "source-svm:source-volume" && v.DestinationPath() == "destination-svm:destination-volume"
		})).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, datamodel.SnapmirrorRelationshipAborted, snapmirror.RelationshipStatus)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationSkipsWhenSnapmirrorIsNil", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, nil)

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationSkipsWhenSnapmirrorNotFound", func(tt *testing.T) {
		defer func() {
			activitiesGetProviderByNode = vsa.GetProviderByNode
		}()
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				SourceSvmName:         "source-svm",
				SourceVolumeName:      "source-volume",
				DestinationSvmName:    "destination-svm",
				DestinationVolumeName: "destination-volume",
			},
		}
		node := &models.Node{}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplicationFromSrcAndDstPath", mock.Anything).
			Return(nil, errors.NewNotFoundErr("snapmirror", nil))

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationForceStopNotSet", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.Nil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationForceStopNotSet", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		replication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "external-uuid",
			},
		}
		node := &models.Node{}

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, false)
		assert.NoError(tt, err)
		assert.Nil(tt, snapmirror)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationFailsToGetProvider", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		// Check that the error is a VCPError with ErrGCPClientInitializationError
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrGCPClientInitializationError, customErr.TrackingID)
	})

	t.Run("AbortVolumeReplicationFailsToGetDetails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(nil, errors.New("failed to get details"))

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		// Check that the error is a VCPError with ErrProviderGetVolumeReplication
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrProviderGetVolumeReplication, customErr.TrackingID)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("AbortVolumeReplicationTransferUUIDMissing", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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
			TransferUUID:       "",
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapmirror)
		assert.Equal(tt, "", snapmirror.TransferUUID)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("AbortVolumeReplicationFailsToAbort", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

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
		}
		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetVolumeReplication", mock.Anything).Return(testReplication, nil)
		mockProvider.On("AbortVolumeReplication", mock.Anything).Return(nil, errors.New("failed to abort replication"))

		snapmirror, err := activity.AbortVolumeReplication(ctx, replication, node, true)
		assert.Error(tt, err)
		assert.Nil(tt, snapmirror)
		// Activity wraps the VCPError in a Temporal ApplicationError; extract and assert TrackingID
		extracted := vsaerrors.ExtractCustomError(err)
		assert.NotNil(tt, extracted)
		assert.Equal(tt, vsaerrors.ErrProviderAbortVolumeReplication, extracted.TrackingID)
		mockProvider.AssertExpectations(tt)
	})
}

func TestGetSnapMirrorFromOntap(t *testing.T) {
	t.Run("GetSnapMirrorFromOntap_Success", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		dbReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Account: &datamodel.Account{
				Name: "account-name",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationVolumeName: "destination-volume-name",
				DestinationSvmName:    "destination-svm-name",
				DestinationHostName:   "destination-host-name",
			},
		}
		node := &models.Node{}
		expectedOntapRep := &vsa.VolumeReplication{ExternalUUID: "ontap-uuid"}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		mockProvider.On("GetReplicationDetails", mock.Anything, mock.Anything).Return(expectedOntapRep, nil)

		ontapRep, err := activity.GetSnapMirrorFromOntap(ctx, dbReplication, node)
		assert.NoError(t, err)
		assert.Equal(t, expectedOntapRep, ontapRep)
		mockProvider.AssertExpectations(t)
	})
	t.Run("GetSnapMirrorFromOntap_FailsToGetProvider", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		dbReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Account: &datamodel.Account{
				Name: "account-name",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationVolumeName: "destination-volume-name",
				DestinationSvmName:    "destination-svm-name",
				DestinationHostName:   "destination-host-name",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		ontapRep, err := activity.GetSnapMirrorFromOntap(ctx, dbReplication, node)
		assert.Error(tt, err)
		assert.Nil(tt, ontapRep)
		// Check that the error is a VCPError with ErrGCPClientInitializationError
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrGCPClientInitializationError, customErr.TrackingID)
	})

	t.Run("GetSnapMirrorFromOntap_FailsToGetReplicationDetails", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockStorage := database.NewMockStorage(t)
		activity := InternalStopVolumeReplicationActivity{SE: mockStorage}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		dbReplication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			Account: &datamodel.Account{
				Name: "account-name",
			},
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID:          "external-uuid",
				DestinationVolumeName: "destination-volume-name",
				DestinationSvmName:    "destination-svm-name",
				DestinationHostName:   "destination-host-name",
			},
		}
		node := &models.Node{}

		activitiesGetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		mockProvider.On("GetReplicationDetails", mock.Anything, mock.Anything).Return(nil, errors.New("failed to get replication details"))

		ontapRep, err := activity.GetSnapMirrorFromOntap(ctx, dbReplication, node)
		assert.Error(tt, err)
		assert.Nil(tt, ontapRep)
		// Check that the error is a VCPError with ErrProviderGetVolumeReplication
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, vsaerrors.ErrProviderGetVolumeReplication, customErr.TrackingID)
	})
}

func TestUpdateQuotaRulesStateToError(t *testing.T) {
	t.Run("EmptyFailedList_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Empty list should return nil without any DB calls
		err := activity.UpdateQuotaRulesStateToError(ctx, []*datamodel.QuotaRule{})
		assert.NoError(t, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SingleFailure_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		failedQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:      "test-quota-rule",
			AccountID: int64(123),
		}

		currentQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:         "test-quota-rule",
			AccountID:    int64(123),
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}

		mockStorage.On("GetQuotaRuleByUUID", ctx, "quota-rule-uuid-1", int64(123)).Return(currentQuotaRule, nil)
		mockStorage.On("UpdateQuotaRule", ctx, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == "quota-rule-uuid-1" &&
				qr.State == datamodel.LifeCycleStateError &&
				qr.StateDetails == datamodel.LifeCycleStateCreationErrorDetails
		})).Return(currentQuotaRule, nil)

		err := activity.UpdateQuotaRulesStateToError(ctx, []*datamodel.QuotaRule{failedQuotaRule})
		assert.NoError(t, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DatabaseReadError_Failure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		failedQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:      "test-quota-rule",
			AccountID: int64(123),
		}

		mockStorage.On("GetQuotaRuleByUUID", ctx, "quota-rule-uuid-1", int64(123)).Return(nil, errors.New("database read error"))

		err := activity.UpdateQuotaRulesStateToError(ctx, []*datamodel.QuotaRule{failedQuotaRule})
		assert.Error(tt, err)
		// Extract CustomError from potentially wrapped Temporal application error
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(tt, customErr, "CustomError should not be nil")
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DatabaseUpdateError_Failure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		failedQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:      "test-quota-rule",
			AccountID: int64(123),
		}

		currentQuotaRule := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:         "test-quota-rule",
			AccountID:    int64(123),
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}

		mockStorage.On("GetQuotaRuleByUUID", ctx, "quota-rule-uuid-1", int64(123)).Return(currentQuotaRule, nil)
		mockStorage.On("UpdateQuotaRule", ctx, mock.Anything).Return(nil, errors.New("database update error"))

		err := activity.UpdateQuotaRulesStateToError(ctx, []*datamodel.QuotaRule{failedQuotaRule})
		assert.Error(tt, err)
		// Extract CustomError from potentially wrapped Temporal application error
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(tt, customErr, "CustomError should not be nil")
		assert.Equal(tt, vsaerrors.ErrDatabaseDataUpdateError, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("MultipleQuotaRules_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		failedQuotaRule1 := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:      "test-quota-rule-1",
			AccountID: int64(123),
		}

		failedQuotaRule2 := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-2",
			},
			Name:      "test-quota-rule-2",
			AccountID: int64(123),
		}

		currentQuotaRule1 := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-1",
			},
			Name:         "test-quota-rule-1",
			AccountID:    int64(123),
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}

		currentQuotaRule2 := &datamodel.QuotaRule{
			BaseModel: datamodel.BaseModel{
				UUID: "quota-rule-uuid-2",
			},
			Name:         "test-quota-rule-2",
			AccountID:    int64(123),
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}

		mockStorage.On("GetQuotaRuleByUUID", ctx, "quota-rule-uuid-1", int64(123)).Return(currentQuotaRule1, nil)
		mockStorage.On("UpdateQuotaRule", ctx, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == "quota-rule-uuid-1" && qr.State == datamodel.LifeCycleStateError
		})).Return(currentQuotaRule1, nil)

		mockStorage.On("GetQuotaRuleByUUID", ctx, "quota-rule-uuid-2", int64(123)).Return(currentQuotaRule2, nil)
		mockStorage.On("UpdateQuotaRule", ctx, mock.MatchedBy(func(qr *datamodel.QuotaRule) bool {
			return qr.UUID == "quota-rule-uuid-2" && qr.State == datamodel.LifeCycleStateError
		})).Return(currentQuotaRule2, nil)

		err := activity.UpdateQuotaRulesStateToError(ctx, []*datamodel.QuotaRule{failedQuotaRule1, failedQuotaRule2})
		assert.NoError(t, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateVolumeReplicationForQuotaError(t *testing.T) {
	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}

		mockStorage.On("UpdateVolumeReplication", ctx, mock.MatchedBy(func(rep *datamodel.VolumeReplication) bool {
			return rep.UUID == "replication-uuid" &&
				rep.State == datamodel.LifeCycleStateError &&
				rep.StateDetails == datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure
		})).Return(nil)

		err := activity.UpdateVolumeReplicationForQuotaError(ctx, replication)
		assert.NoError(t, err)
		assert.Equal(t, datamodel.LifeCycleStateError, replication.State)
		assert.Equal(t, datamodel.VolumeReplicationBreakRelationshipQuotaRuleFailure, replication.StateDetails)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DatabaseUpdateError_Failure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := InternalStopVolumeReplicationActivity{
			SE: mockStorage,
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID: "replication-uuid",
			},
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}

		mockStorage.On("UpdateVolumeReplication", ctx, mock.Anything).Return(errors.New("database update error"))

		err := activity.UpdateVolumeReplicationForQuotaError(ctx, replication)
		assert.Error(tt, err)
		// Extract CustomError from potentially wrapped Temporal application error
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(tt, customErr, "CustomError should not be nil")
		assert.Equal(tt, vsaerrors.ErrDatabaseDataUpdateError, customErr.TrackingID)
		mockStorage.AssertExpectations(tt)
	})
}

package replicationActivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestDeleteVolumeReplicationInternal(t *testing.T) {
	t.Run("WhenError", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}

		// Prepare test data
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(nil, errors.New("database error"))
		_, err := activity.DeleteVolumeReplication(ctx, params, node, false)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "database error", customErr.OriginalErr.Error())
		mockProvider.AssertExpectations(t)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}

		// Prepare test data
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}

		expectedResponse := &vsa.VolumeReplication{}
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(expectedResponse, nil)

		res, err := activity.DeleteVolumeReplication(ctx, params, node, false)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		mockProvider.AssertExpectations(tt)
	})
	t.Run("WhenSuccessWithIsCleanup", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
		node := &models.Node{}

		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}

		expectedResponse := &vsa.VolumeReplication{}
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(expectedResponse, nil)

		res, err := activity.DeleteVolumeReplication(ctx, params, node, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		mockProvider.AssertExpectations(tt)
	})
}

const peerClusterUnreachableErr = `Failed to contact peer cluster "gcnv-80a3aaf473e9028-r34" at addresses: 10.29.16.28, 10.29.16.29. RPC: Remote system error [from mgwd on node "gcnv-4a6102c8cbcf5fa-01" (VSID: -1) to mgwd at 10.29.16.28, 10.29.16.29]`

func TestDeleteVolumeReplicationInternal_PeerClusterUnreachableRetry(t *testing.T) {
	newReplicationParams := func(hybrid bool) *datamodel.VolumeReplication {
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}
		if hybrid {
			onpremType := string(datamodel.HybridReplicationParametersReplicationTypeONPREM)
			params.HybridReplicationAttributes = &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &onpremType,
			}
		}
		return params
	}

	t.Run("HybridRetriesWithDestinationOnlyAndSucceeds", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{SE: database.NewMockStorage(t)}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// First attempt (destinationOnly=false) fails with peer-unreachable error.
		mockProvider.On("DeleteVolumeReplication", mock.MatchedBy(func(p *vsa.DeleteVolumeReplicationParams) bool {
			return p.DestinationOnly != nil && !*p.DestinationOnly
		})).Return(nil, errors.New(peerClusterUnreachableErr)).Once()
		// Retry (destinationOnly=true) succeeds.
		expectedResponse := &vsa.VolumeReplication{}
		mockProvider.On("DeleteVolumeReplication", mock.MatchedBy(func(p *vsa.DeleteVolumeReplicationParams) bool {
			return p.DestinationOnly != nil && *p.DestinationOnly
		})).Return(expectedResponse, nil).Once()

		res, err := activity.DeleteVolumeReplication(ctx, newReplicationParams(true), &models.Node{}, false)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedResponse, res)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("HybridRetryAlsoFailsReturnsError", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{SE: database.NewMockStorage(t)}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		mockProvider.On("DeleteVolumeReplication", mock.MatchedBy(func(p *vsa.DeleteVolumeReplicationParams) bool {
			return p.DestinationOnly != nil && !*p.DestinationOnly
		})).Return(nil, errors.New(peerClusterUnreachableErr)).Once()
		mockProvider.On("DeleteVolumeReplication", mock.MatchedBy(func(p *vsa.DeleteVolumeReplicationParams) bool {
			return p.DestinationOnly != nil && *p.DestinationOnly
		})).Return(nil, errors.New("destination delete failed")).Once()

		res, err := activity.DeleteVolumeReplication(ctx, newReplicationParams(true), &models.Node{}, false)

		assert.Nil(tt, res)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "destination delete failed", customErr.OriginalErr.Error())
		mockProvider.AssertExpectations(tt)
	})

	t.Run("NonHybridDoesNotRetry", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{SE: database.NewMockStorage(t)}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		// Only one call expected since non-hybrid replication should not retry.
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(nil, errors.New(peerClusterUnreachableErr)).Once()

		res, err := activity.DeleteVolumeReplication(ctx, newReplicationParams(false), &models.Node{}, false)

		assert.Nil(tt, res)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, peerClusterUnreachableErr, customErr.OriginalErr.Error())
		mockProvider.AssertExpectations(tt)
	})

	t.Run("HybridWithUnrelatedErrorDoesNotRetry", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{SE: database.NewMockStorage(t)}
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(nil, errors.New("some other error")).Once()

		res, err := activity.DeleteVolumeReplication(ctx, newReplicationParams(true), &models.Node{}, false)

		assert.Nil(tt, res)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "some other error", customErr.OriginalErr.Error())
		mockProvider.AssertExpectations(tt)
	})
}

func TestIsPeerClusterUnreachableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"NilError", nil, false},
		{"PeerClusterUnreachable", errors.New(peerClusterUnreachableErr), true},
		{"MixedCase", errors.New("FAILED TO CONTACT PEER CLUSTER xyz RPC: REMOTE SYSTEM ERROR"), true},
		{"OnlyContactFragment", errors.New("Failed to contact peer cluster xyz"), true},
		{"OnlyRPCFragment", errors.New("RPC: Remote system error"), false},
		{"UnrelatedError", errors.New("connection refused"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.expected, isPeerClusterUnreachableError(tc.err))
		})
	}
}

func TestIsHybridReplication(t *testing.T) {
	hybridWithType := func(replicationType string) *datamodel.VolumeReplication {
		return &datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &replicationType,
			},
		}
	}

	t.Run("NilReplication", func(tt *testing.T) {
		assert.False(tt, isHybridReplication(nil))
	})
	t.Run("NilHybridAttributes", func(tt *testing.T) {
		assert.False(tt, isHybridReplication(&datamodel.VolumeReplication{}))
	})
	t.Run("NilReplicationType", func(tt *testing.T) {
		assert.False(tt, isHybridReplication(&datamodel.VolumeReplication{
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{},
		}))
	})
	t.Run("OnpremReplicationType", func(tt *testing.T) {
		assert.True(tt, isHybridReplication(hybridWithType(string(datamodel.HybridReplicationParametersReplicationTypeONPREM))))
	})
	t.Run("MigrationReplicationType", func(tt *testing.T) {
		assert.True(tt, isHybridReplication(hybridWithType(string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION))))
	})
	t.Run("ContinuousReplicationTypeNotEligible", func(tt *testing.T) {
		assert.False(tt, isHybridReplication(hybridWithType(string(datamodel.HybridReplicationParametersReplicationTypeCONTINUOUS))))
	})
	t.Run("ReverseReplicationTypeNotEligible", func(tt *testing.T) {
		assert.False(tt, isHybridReplication(hybridWithType(string(datamodel.HybridReplicationParametersReplicationTypeREVERSE))))
	})
}

func TestReplicationTypeForVSADelete(t *testing.T) {
	onpremType := string(datamodel.HybridReplicationParametersReplicationTypeONPREM)
	t.Run("PrefersHybridReplicationType", func(tt *testing.T) {
		volumeReplication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ReplicationType: "async",
			},
			HybridReplicationAttributes: &datamodel.HybridReplicationAttribute{
				HybridReplicationType: &onpremType,
			},
		}
		assert.Equal(tt, onpremType, replicationTypeForVSADelete(volumeReplication))
	})
	t.Run("FallsBackToReplicationAttributes", func(tt *testing.T) {
		volumeReplication := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ReplicationType: "async",
			},
		}
		assert.Equal(tt, "async", replicationTypeForVSADelete(volumeReplication))
	})
}

func TestUpdateReplicationStateInDB(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := InternalVolumeReplicationDeleteActivity{
		SE: mockStorage,
	}
	ctx := context.Background()
	volumeRep := &datamodel.VolumeReplication{}

	mockStorage.On("UpdateVolumeReplicationStates", ctx, volumeRep).Return(nil)

	err := activity.UpdateReplicationStateInDBForDelete(ctx, volumeRep)
	assert.NoError(t, err)
	assert.Equal(t, datamodel.LifeCycleStateError, volumeRep.State)
	assert.Equal(t, datamodel.LifeCycleStateDeletionErrorDetails, volumeRep.StateDetails)
	mockStorage.AssertExpectations(t)
}

func TestUpdateReplicationStateInDB_Error(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := InternalVolumeReplicationDeleteActivity{
		SE: mockStorage,
	}
	ctx := context.Background()
	volumeRep := &datamodel.VolumeReplication{}

	mockStorage.On("UpdateVolumeReplicationStates", ctx, volumeRep).Return(errors.New("database error"))

	err := activity.UpdateReplicationStateInDBForDelete(ctx, volumeRep)
	var customErr *vsaerrors.CustomError
	assert.True(t, vsaerrors.As(err, &customErr))
	assert.Equal(t, "database error", customErr.OriginalErr.Error())
	mockStorage.AssertExpectations(t)
}

func TestCleanupReplicationAfterReverse(t *testing.T) {
	t.Run("WhenProviderReturnsError", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), "logger", struct{}{})
		node := &models.Node{}
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(nil, errors.New("provider error"))
		_, err := activity.CleanupReplicationAfterReverse(ctx, params, node)
		var customErr *vsaerrors.CustomError
		assert.True(tt, vsaerrors.As(err, &customErr))
		assert.Equal(tt, "provider error", customErr.OriginalErr.Error())
		mockProvider.AssertExpectations(tt)
	})

	t.Run("WhenProviderReturnsSuccess", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		activity := InternalVolumeReplicationDeleteActivity{
			SE: database.NewMockStorage(t),
		}
		ctx := context.WithValue(context.Background(), "logger", struct{}{})
		node := &models.Node{}
		params := &datamodel.VolumeReplication{
			ReplicationAttributes: &datamodel.ReplicationDetails{
				EndpointType:          "type",
				SourceHostName:        "src-host",
				SourceSvmName:         "src-svm",
				SourceVolumeName:      "src-vol",
				DestinationHostName:   "dst-host",
				DestinationSvmName:    "dst-svm",
				ReplicationSchedule:   "schedule",
				DestinationVolumeName: "dst-vol",
				ExternalUUID:          "uuid",
			},
		}
		expectedResponse := &vsa.VolumeReplication{}
		mockProvider.On("DeleteVolumeReplication", mock.Anything).Return(expectedResponse, nil)
		res, err := activity.CleanupReplicationAfterReverse(ctx, params, node)
		assert.NoError(tt, err)
		assert.NotNil(tt, res)
		mockProvider.AssertExpectations(tt)
	})
}

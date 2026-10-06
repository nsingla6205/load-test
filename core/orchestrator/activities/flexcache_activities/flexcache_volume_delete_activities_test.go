package flexcache_activities

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/flexcache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
)

func TestFlexCacheVolumeDeleteActivity_RefreshDBVolumeForDeleteActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}

		staleVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-refresh-uuid"},
			State:     datamodel.LifeCycleStatePreparing,
		}
		refreshedVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-refresh-uuid"},
			State:     datamodel.LifeCycleStateCreating,
		}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: staleVol}

		mockStorage.EXPECT().DescribeVolume(ctx, staleVol.UUID).Return(refreshedVol, nil)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf("refreshed volume %s from database for delete workflow", refreshedVol.UUID)

		out, err := activity.RefreshDBVolumeForDeleteActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Same(tt, result, out)
		assert.Equal(tt, refreshedVol, out.DBVolume)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DescribeVolumeFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}

		vol := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "vol-refresh-uuid"}}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		mockStorage.EXPECT().DescribeVolume(ctx, vol.UUID).Return(nil, assert.AnError)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)

		out, err := activity.RefreshDBVolumeForDeleteActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, out)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_UnmountVolumeInOntapActivity(t *testing.T) {
	dbVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm:       &datamodel.Svm{Name: "svm-name"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}
	node := &models.Node{
		Name: "test-node",
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().UnmountVolume("external-uuid").Return(&vsa.OntapAsyncResponse{JobUUID: "unmount-job-uuid"}, nil)
		logger.EXPECT().Debugf("FlexCache volume unmount job for volume with UUID %s initiated successfully", "volume-uuid")

		resp, err := activity.UnmountVolumeInOntapActivity(ctx, flexcacheResult)

		assert.NoError(tt, err, "UnmountVolumeInOntapActivity should complete successfully")
		assert.NotNil(tt, resp)
		assert.NotNil(tt, resp.UnmountJobResponse)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.UnmountVolumeInOntapActivity(ctx, flexcacheResult)

		assert.Error(tt, err, "Function should return an error when GetProviderByNode fails")
		assert.Nil(tt, resp)
	})

	t.Run("WhenUnmountVolumeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().UnmountVolume("external-uuid").Return(nil, assert.AnError)

		resp, err := activity.UnmountVolumeInOntapActivity(ctx, flexcacheResult)

		assert.Error(tt, err, "Function should return an error when UnmountVolume fails")
		assert.Nil(tt, resp)
	})

	t.Run("WhenNoExternalUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: &datamodel.Volume{
				Name: "test-volume",
				Svm:  &datamodel.Svm{Name: "svm-name"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "",
				},
			},
			Node: node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		logger.EXPECT().Debug("no external UUID found for the volume, skipping unmount")

		resp, err := activity.UnmountVolumeInOntapActivity(ctx, flexcacheResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
	})
}

func TestFlexCacheVolumeDeleteActivity_DisableFlexCacheWritebackInOntapActivity(t *testing.T) {
	dbVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}
	node := &models.Node{Name: "test-node"}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: dbVolume, Node: node}
		expectedWritebackEnabled := false
		expectedResponse := &vsa.OntapAsyncResponse{JobUUID: "disable-writeback-job-uuid"}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().UpdateFlexCacheVolume(vsa.UpdateFlexCacheVolumeParams{
			UUID:             "external-uuid",
			WritebackEnabled: &expectedWritebackEnabled,
		}).Return(expectedResponse, nil)
		logger.EXPECT().Debugf("FlexCache writeback disable for volume with UUID %s initiated successfully", "volume-uuid")

		resp, err := activity.DisableFlexCacheWritebackInOntapActivity(ctx, result)

		assert.NoError(tt, err)
		assert.Same(tt, expectedResponse, resp.DisableWritebackJobResponse)
	})

	t.Run("WhenUpdateFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: dbVolume, Node: node}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().UpdateFlexCacheVolume(mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.DisableFlexCacheWritebackInOntapActivity(ctx, result)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: dbVolume, Node: node}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.DisableFlexCacheWritebackInOntapActivity(ctx, result)

		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("WhenNoExternalUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		result := &flexcache.DeleteFlexCacheResult{
			DBVolume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "",
				},
			},
			Node: node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		logger.EXPECT().Debug("no external UUID found for the volume, skipping writeback disable")

		resp, err := activity.DisableFlexCacheWritebackInOntapActivity(ctx, result)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Nil(tt, resp.DisableWritebackJobResponse)
	})
}

func TestFlexCacheVolumeDeleteActivity_DeleteFlexCacheVolumeInOntapActivity(t *testing.T) {
	dbVolume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		Name:      "test-volume",
		Svm:       &datamodel.Svm{Name: "svm-name"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: "external-uuid",
		},
	}
	node := &models.Node{
		Name: "test-node",
	}

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().DeleteFlexCacheVolume("external-uuid", "test-volume").Return(&vsa.OntapAsyncResponse{JobUUID: "delete-job-uuid"}, nil)
		logger.EXPECT().Debugf("FlexCache volume delete job for volume with UUID %s initiated successfully", "volume-uuid")

		resp, err := activity.DeleteFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)

		assert.NoError(tt, err, "DeleteFlexCacheVolumeInOntapActivity should complete successfully")
		assert.NotNil(tt, resp)
		assert.NotNil(tt, resp.DeleteJobResponse)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.DeleteFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)

		assert.Error(tt, err, "Function should return an error when GetProviderByNode fails")
		assert.Nil(tt, resp)
	})

	t.Run("WhenDeleteFlexCacheVolumeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: dbVolume,
			Node:     node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().DeleteFlexCacheVolume("external-uuid", "test-volume").Return(nil, assert.AnError)

		resp, err := activity.DeleteFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)

		assert.Error(tt, err, "Function should return an error when DeleteFlexCacheVolume fails")
		assert.Nil(tt, resp)
	})

	t.Run("WhenNoExternalUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: database.NewMockStorage(tt)}
		ctx := context.Background()
		flexcacheResult := &flexcache.DeleteFlexCacheResult{
			DBVolume: &datamodel.Volume{
				Name: "test-volume",
				Svm:  &datamodel.Svm{Name: "svm-name"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "",
				},
			},
			Node: node,
		}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		logger.EXPECT().Debug("no external UUID found for the volume, skipping delete")

		resp, err := activity.DeleteFlexCacheVolumeInOntapActivity(ctx, flexcacheResult)

		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Nil(tt, resp.DeleteJobResponse)
	})
}

func TestFlexCacheVolumeDeleteActivity_DeleteSVMPeeringInOntapActivity(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid-svm-peer"},
			Name:             "flexcache-vol",
			Svm:              &datamodel.Svm{Name: "local-svm"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid"},
		}
	}

	createNode := func() *models.Node { return &models.Node{Name: "test-node"} }

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm"}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(&vsa.SvmPeer{UUID: "svm-peer-uuid"}, nil)
		mockProvider.EXPECT().DeleteSVMPeer("svm-peer-uuid", false).Return(nil)
		logger.EXPECT().Debugf("SVM peering with UUID %s deleted successfully", "svm-peer-uuid")

		resp, err := activity.DeleteSVMPeeringInOntapActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.DeleteSVMPeeringInOntapActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("SVMPeerNotFound", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		storage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: storage}
		ctx := context.Background()

		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm-missing"}
		res := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)

		notFoundErr := customerrors.NewNotFoundErr("svm peer", nil)
		provider.EXPECT().
			GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(nil, notFoundErr)

		logger.EXPECT().
			Debugf("SVM peer not found for svm=%s peer_svm=%s, skipping delete", vol.Svm.Name, vol.CacheParameters.PeerSvmName)

		out, err := activity.DeleteSVMPeeringInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, res, out)
		storage.AssertExpectations(tt)
	})

	t.Run("WhenDeleteSVMPeerFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm"}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(&vsa.SvmPeer{UUID: "svm-peer-uuid"}, nil)
		mockProvider.EXPECT().DeleteSVMPeer("svm-peer-uuid", false).Return(assert.AnError)

		resp, err := activity.DeleteSVMPeeringInOntapActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("WhenNoSvmPeerUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm"}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(&vsa.SvmPeer{UUID: "svm-peer-uuid"}, assert.AnError)

		resp, err := activity.DeleteSVMPeeringInOntapActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("FlexCacheInUse", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		storage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: storage}
		ctx := context.Background()
		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm"}
		res := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().
			GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(&vsa.SvmPeer{UUID: "svm-peer-uuid"}, nil)
		inUseErr := customerrors.New("The peer relationship is in use by FlexCache: details")
		provider.EXPECT().DeleteSVMPeer("svm-peer-uuid", false).Return(inUseErr)
		logger.EXPECT().
			Infof("Skipping SVM peer delete for %s: still in use (%s); leaving svm_peer_uuid unchanged", "svm-peer-uuid", inUseErr.Error())

		out, err := activity.DeleteSVMPeeringInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, res, out)
		storage.AssertExpectations(tt)
	})

	t.Run("SnapMirrorInUse", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		provider := vsa.NewMockProvider(tt)
		storage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: storage}
		ctx := context.Background()
		vol := baseVolume()
		vol.CacheParameters = &datamodel.CacheParameters{PeerSvmName: "peer-svm"}
		res := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(provider, nil)
		provider.EXPECT().
			GetSVMPeer(&vol.Svm.Name, &vol.CacheParameters.PeerSvmName).
			Return(&vsa.SvmPeer{UUID: "svm-peer-uuid"}, nil)
		inUseErr := customerrors.New("Relationship is in use by SnapMirror in local cluster: details")
		provider.EXPECT().DeleteSVMPeer("svm-peer-uuid", false).Return(inUseErr)
		logger.EXPECT().
			Infof("Skipping SVM peer delete for %s: still in use (%s); leaving svm_peer_uuid unchanged", "svm-peer-uuid", inUseErr.Error())

		out, err := activity.DeleteSVMPeeringInOntapActivity(ctx, res)
		assert.NoError(tt, err)
		assert.Equal(tt, res, out)
		storage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_DeleteClusterPeerInOntapActivity(t *testing.T) {
	baseVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid-cluster-peer"},
			Name:             "flexcache-vol",
			Svm:              &datamodel.Svm{Name: "local-svm"},
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid"},
		}
	}
	createNode := func() *models.Node { return &models.Node{Name: "test-node"} }
	clusterPeerUUID := "cluster-peer-uuid"
	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode(),
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().DeleteClusterPeer("cluster-peer-uuid").Return(nil)
		logger.EXPECT().Debugf("Cluster peering with UUID %s deleted successfully", "cluster-peer-uuid")

		resp, err := activity.DeleteClusterPeerInOntapActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetProviderByNodeFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode()}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(nil, assert.AnError)

		resp, err := activity.DeleteClusterPeerInOntapActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("WhenDeleteClusterPeerFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode(),
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		mockProvider.EXPECT().DeleteClusterPeer("cluster-peer-uuid").Return(assert.AnError)

		resp, err := activity.DeleteClusterPeerInOntapActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
	})

	t.Run("WhenDeleteClusterPeerNotFound", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode(),
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: clusterPeerUUID}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)
		notFoundErr := customerrors.NewNotFoundErr("cluster peer", nil)
		mockProvider.EXPECT().DeleteClusterPeer(clusterPeerUUID).Return(notFoundErr)
		logger.EXPECT().Debugf("Cluster peer not found for UUID %s, skipping delete", clusterPeerUUID)

		resp, err := activity.DeleteClusterPeerInOntapActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNoClusterPeerUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockProvider := vsa.NewMockProvider(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		ctx := context.Background()
		vol := baseVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, Node: createNode(),
			ClusterPeeringRow: &datamodel.ClusterPeerings{OntapPeerUUID: ""}}

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().hyperscalerGetProviderByNode(ctx, mock.Anything).Return(mockProvider, nil)

		resp, err := activity.DeleteClusterPeerInOntapActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
	})
}

func TestFlexCacheVolumeDeleteActivity_DeleteClusterPeeringRowInDBActivity(t *testing.T) {
	ctx := context.Background()

	makeVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-del-cpr"},
			Name:      "flexcache-vol",
			AccountID: 11,
			PoolID:    22,
			CacheParameters: &datamodel.CacheParameters{
				PeerClusterName: "peer-cluster-A",
			},
		}
	}

	makeRow := func() *datamodel.ClusterPeerings {
		return &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{ID: 77, UUID: "cluster-peering-row-uuid"},
			AccountID: 11,
			PoolID:    22,
			State:     "PEERED",
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		row := makeRow()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, ClusterPeeringRow: row}

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)

		mockStorage.EXPECT().
			DeleteClusterPeeringRow(ctx, row).
			Return(nil).Once()

		logger.EXPECT().Debugf("Cluster peering row with ID %d deleted successfully", row.ID)

		resp, err := activity.DeleteClusterPeeringRowInDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DeleteClusterPeeringRowFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		row := makeRow()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, ClusterPeeringRow: row}

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)

		mockStorage.EXPECT().
			DeleteClusterPeeringRow(ctx, row).
			Return(assert.AnError).Once()

		resp, err := activity.DeleteClusterPeeringRowInDBActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(t *testing.T) {
	ctx := context.Background()

	makeVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-counts"},
			Name:      "flexcache-vol",
			AccountID: 101,
			PoolID:    202,
			CacheParameters: &datamodel.CacheParameters{
				PeerClusterName: "peer-cluster-X",
			},
		}
	}

	makeRow := func() *datamodel.ClusterPeerings {
		return &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{ID: 909, UUID: "cluster-peering-row-counts"},
			AccountID: 101,
			PoolID:    202,
			State:     "PEERED",
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		row := makeRow()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, ClusterPeeringRow: makeRow()}

		mockStorage.EXPECT().
			GetVolumeReplicationCountByClusterPeerID(ctx, row.ID).
			Return(int64(5), nil).Once()
		mockStorage.EXPECT().
			GetFlexCacheVolumeCountByClusterPeerID(ctx, row.ID).
			Return(int64(3), nil).Once()

		resp, err := activity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, resp)
		assert.Equal(tt, int64(5), resp.VolumeReplicationCountOnClusterPeering)
		assert.Equal(tt, int64(3), resp.FlexCacheVolumeCountOnClusterPeering)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ClusterPeerNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		resp, err := activity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(ctx, result)
		assert.NoError(tt, err, "Should not error when cluster peer is not found")
		assert.NotNil(tt, resp)
		assert.Equal(tt, int64(0), resp.VolumeReplicationCountOnClusterPeering)
		assert.Equal(tt, int64(0), resp.FlexCacheVolumeCountOnClusterPeering)
		assert.Nil(tt, resp.ClusterPeeringRow, "ClusterPeeringRow should remain nil when not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReplicationCountFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		row := makeRow()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, ClusterPeeringRow: row}

		mockStorage.EXPECT().
			GetVolumeReplicationCountByClusterPeerID(ctx, row.ID).
			Return(int64(0), assert.AnError).Once()

		resp, err := activity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FlexCacheCountFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		row := makeRow()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol, ClusterPeeringRow: row}

		mockStorage.EXPECT().
			GetVolumeReplicationCountByClusterPeerID(ctx, row.ID).
			Return(int64(7), nil).Once()
		mockStorage.EXPECT().
			GetFlexCacheVolumeCountByClusterPeerID(ctx, row.ID).
			Return(int64(0), assert.AnError).Once()

		resp, err := activity.GetFlexCacheAndReplicationCountsOnClusterPeeringActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, resp)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_UpdateClusterPeeringRowStateDeletedInDBActivity(t *testing.T) {
	ctx := context.Background()

	makeRow := func() *datamodel.ClusterPeerings {
		return &datamodel.ClusterPeerings{
			BaseModel: datamodel.BaseModel{UUID: "cluster-peering-row-uuid"},
			AccountID: 11,
			PoolID:    22,
			State:     "PEERED",
		}
	}

	makeResult := func(row *datamodel.ClusterPeerings) *flexcache.DeleteFlexCacheResult {
		return &flexcache.DeleteFlexCacheResult{
			DBVolume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "vol-uuid-del-cpr"},
				AccountID: 11,
				PoolID:    22,
			},
			ClusterPeeringRow: row,
		}
	}

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		row := makeRow()
		result := makeResult(row)

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockStorage.
			On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
				return r == row && r.State == datamodel.CvpClusterPeeringStatusDELETED
			})).
			Return(nil).Once()
		logger.EXPECT().Infof("Cluster peering row with UUID %s updated to state %s", row.UUID, datamodel.CvpClusterPeeringStatusDELETED)

		updated, err := activity.UpdateClusterPeeringRowStateDeletedInDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, updated)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusDELETED, updated.ClusterPeeringRow.State)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		row := makeRow()
		result := makeResult(row)

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mockStorage.
			On("UpdateClusterPeeringRow", ctx, mock.MatchedBy(func(r *datamodel.ClusterPeerings) bool {
				return r == row && r.State == datamodel.CvpClusterPeeringStatusDELETED
			})).
			Return(assert.AnError).Once()
		logger.EXPECT().Errorf("Failed to update cluster peering row in DB: %v", assert.AnError)

		updated, err := activity.UpdateClusterPeeringRowStateDeletedInDBActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, updated)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_GetClusterPeeringFromDBActivity(t *testing.T) {
	ctx := context.Background()

	makeVolume := func() *datamodel.Volume {
		return &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			AccountID: 11,
			PoolID:    22,
			Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 11}},
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 22}},
			ClusterPeerID: sql.NullInt64{
				Int64: 99,
				Valid: true,
			},
			CacheParameters: &datamodel.CacheParameters{
				PeerClusterName: "peer-cluster-A",
			},
		}
	}

	t.Run("SuccessExistingRow", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		existing := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{ID: vol.ClusterPeerID.Int64, UUID: "peer-row-1"},
			AccountID:      vol.AccountID,
			PoolID:         vol.PoolID,
			OnprempCluster: vol.CacheParameters.PeerClusterName,
		}

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf(mock.Anything, mock.Anything).Maybe()

		mockStorage.EXPECT().
			GetClusterPeeringRowByID(mock.Anything, vol.ClusterPeerID.Int64).
			Return(existing, nil).Once()

		out, err := act.GetClusterPeeringFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, out)
		assert.Equal(tt, existing, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("NotFoundReturnsNilRow", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		notFoundErr := customerrors.NewNotFoundErr("cluster peering row", nil)

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().
			Debugf(
				"Cluster peering row not found for cluster_peer_id=%d",
				vol.ClusterPeerID.Int64,
			).Once()

		mockStorage.EXPECT().
			GetClusterPeeringRowByID(mock.Anything, vol.ClusterPeerID.Int64).
			Return(nil, notFoundErr).Once()

		out, err := act.GetClusterPeeringFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.NotNil(tt, out)
		assert.Nil(tt, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().
			Errorf(
				"Failed to get cluster peering row from database: %v",
				assert.AnError,
			).Once()

		mockStorage.EXPECT().
			GetClusterPeeringRowByID(mock.Anything, vol.ClusterPeerID.Int64).
			Return(nil, assert.AnError).Once()

		out, err := act.GetClusterPeeringFromDBActivity(ctx, result)
		assert.Error(tt, err)
		assert.Nil(tt, out)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsLookupWhenClusterPeerIDUnset", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		vol := makeVolume()
		vol.ClusterPeerID = sql.NullInt64{}
		result := &flexcache.DeleteFlexCacheResult{DBVolume: vol}

		logger := log.NewMockLogger(tt)
		mm := newMonkeyMockAndPatch(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().
			Debugf("cluster_peer_id not set on volume; skipping cluster peering row lookup").
			Once()

		out, err := act.GetClusterPeeringFromDBActivity(ctx, result)
		assert.NoError(tt, err)
		assert.Nil(tt, out.ClusterPeeringRow)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_CancelPrepopulateJobsForVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		volumeUUID := "volume-uuid-123"

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Infof("Cancelling prepopulate jobs for volume %s", volumeUUID)
		mockStorage.EXPECT().CancelPrepopulateJobsForVolume(ctx, volumeUUID).Return(nil).Once()
		logger.EXPECT().Infof("Successfully cancelled prepopulate jobs for volume %s", volumeUUID)

		err := activity.CancelPrepopulateJobsForVolume(ctx, volumeUUID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCancelFails", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		volumeUUID := "volume-uuid-456"

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Infof("Cancelling prepopulate jobs for volume %s", volumeUUID)
		mockStorage.EXPECT().CancelPrepopulateJobsForVolume(ctx, volumeUUID).Return(assert.AnError).Once()
		logger.EXPECT().Errorf("Failed to cancel prepopulate jobs for volume %s: %v", volumeUUID, assert.AnError)

		err := activity.CancelPrepopulateJobsForVolume(ctx, volumeUUID)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WithEmptyVolumeUUID", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mockStorage := database.NewMockStorage(tt)
		activity := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		volumeUUID := ""

		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Infof("Cancelling prepopulate jobs for volume %s", volumeUUID)
		mockStorage.EXPECT().CancelPrepopulateJobsForVolume(ctx, volumeUUID).Return(nil).Once()
		logger.EXPECT().Infof("Successfully cancelled prepopulate jobs for volume %s", volumeUUID)

		err := activity.CancelPrepopulateJobsForVolume(ctx, volumeUUID)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestFlexCacheVolumeDeleteActivity_CancelFlexCacheCreateWorkflowIfPreparingActivity(t *testing.T) {
	ctx := context.Background()

	resultWith := func(vol *datamodel.Volume) *flexcache.DeleteFlexCacheResult {
		return &flexcache.DeleteFlexCacheResult{DBVolume: vol}
	}

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}
		job := &datamodel.Job{WorkflowID: "workflow-id"}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		temporal.EXPECT().CancelWorkflow(ctx, job.WorkflowID, "").Return(nil)
		mockStorage.EXPECT().CancelRunningJobsForResource(ctx, vol.UUID).Return(nil)

		logger.EXPECT().Infof("successfully cancelled create workflow %s for volume %s", job.WorkflowID, vol.Name)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("CancelWorkflowError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}
		job := &datamodel.Job{WorkflowID: "workflow-id"}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		temporal.EXPECT().CancelWorkflow(ctx, job.WorkflowID, "").Return(assert.AnError)

		logger.EXPECT().Errorf("failed to cancel create workflow %s for volume %s: %v", job.WorkflowID, vol.Name, assert.AnError)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		mockStorage.AssertNotCalled(tt, "CancelRunningJobsForResource", mock.Anything, mock.Anything)
		temporal.AssertExpectations(tt)
	})

	t.Run("CancelWorkflowReturnsNotFound_StillClearsRunningJobs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}
		job := &datamodel.Job{WorkflowID: "workflow-id"}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		temporal.EXPECT().CancelWorkflow(ctx, job.WorkflowID, "").Return(serviceerror.NewNotFound("workflow missing"))

		logger.EXPECT().Debugf("create workflow not found in Temporal (workflowID=%s volume=%s); clearing running jobs for resource",
			job.WorkflowID, vol.Name)
		mockStorage.EXPECT().CancelRunningJobsForResource(ctx, vol.UUID).Return(nil)
		logger.EXPECT().Infof("cleared running jobs for volume %s (create workflow %s was not running in Temporal)",
			vol.Name, job.WorkflowID)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("VolumeNotInPreparingState", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStateAvailable,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf("cannot cancel create workflow for volume %s as it is not in PREPARING state", vol.Name)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		temporal.AssertNotCalled(tt, "CancelWorkflow", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("NotFlexCacheVolume", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			State:     datamodel.LifeCycleStatePreparing,
		}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf("skip cancel create workflow for volume %s: not a FlexCache volume", vol.Name)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		temporal.AssertNotCalled(tt, "CancelWorkflow", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("CreateJobNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}
		objectID := "job-id"
		notFoundErr := customerrors.NewNotFoundErr("Job", &objectID)

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, notFoundErr)

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf("no create job found for volume %s", vol.Name)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		temporal.AssertNotCalled(tt, "CancelWorkflow", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("GetJobByResourceUUID_OtherError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, assert.AnError)

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Errorf("error retrieving create job for volume %s: %v", vol.Name, assert.AnError)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		temporal.AssertNotCalled(tt, "CancelWorkflow", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("CancelRunningJobsForResource_Error", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "peer-cluster"},
		}
		job := &datamodel.Job{WorkflowID: "workflow-id"}

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		temporal.EXPECT().CancelWorkflow(ctx, job.WorkflowID, "").Return(nil)
		mockStorage.EXPECT().CancelRunningJobsForResource(ctx, vol.UUID).Return(assert.AnError)

		logger.EXPECT().Errorf("failed to cancel running jobs for volume %s: %v", vol.Name, assert.AnError)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.CancelFlexCacheCreateWorkflowIfPreparingActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		temporal.AssertExpectations(tt)
	})
}

func describeWorkflowExecutionWithStatus(st enumspb.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status: st,
		},
	}
}

func TestFlexCacheVolumeDeleteActivity_WaitForFlexCacheCreateWorkflowTerminalActivity(t *testing.T) {
	ctx := context.Background()
	resultWith := func(vol *datamodel.Volume) *flexcache.DeleteFlexCacheResult {
		return &flexcache.DeleteFlexCacheResult{DBVolume: vol}
	}

	t.Run("Skip_NoCreateJob", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		objectID := "job"
		notFoundErr := customerrors.NewNotFoundErr("Job", &objectID)
		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, notFoundErr)

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Debugf("skip wait for create workflow for volume %s: no create job", vol.Name)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_TerminalImmediately", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(
			describeWorkflowExecutionWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED), nil)
		logger.EXPECT().Debugf("create workflow %s reached terminal status %s", job.WorkflowID, mock.Anything)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_RunningThenCanceled", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(
			describeWorkflowExecutionWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), nil).Once()
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(
			describeWorkflowExecutionWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED), nil).Once()
		logger.EXPECT().Debugf("create workflow %s reached terminal status %s", job.WorkflowID, mock.Anything)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_DescribeNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(nil, serviceerror.NewNotFound("missing"))
		logger.EXPECT().Debugf("create workflow %s no longer present in Temporal; done waiting", job.WorkflowID)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Describe_NilWorkflowExecutionInfo", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "v-1"},
			Name:      "vol1",
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(
			&workflowservice.DescribeWorkflowExecutionResponse{}, nil)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("GetJobError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(nil, errors.New("db down"))

		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		logger.EXPECT().Errorf("error retrieving create job while waiting for create workflow: volume %s: %v", vol.Name, mock.Anything)

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DescribeWorkflowError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(ctx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(ctx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(ctx, job.WorkflowID, "").Return(nil, errors.New("temporal unavailable"))

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(ctx, resultWith(vol))
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("ContextCancelledWhileWaiting", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		cancelCtx, cancel := context.WithCancel(context.Background())
		vol := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "v-1"},
			Name:            "vol1",
			State:           datamodel.LifeCycleStatePreparing,
			CacheParameters: &datamodel.CacheParameters{PeerClusterName: "p"},
		}
		job := &datamodel.Job{WorkflowID: "wf-1"}

		mockStorage.EXPECT().GetJobByResourceUUID(cancelCtx, vol.UUID, string(datamodel.JobTypeFlexCacheCreateVolume)).Return(job, nil)
		mm := newMonkeyMockAndPatch(tt)
		logger := log.NewMockLogger(tt)
		mm.EXPECT().utilGetLogger(cancelCtx).Return(logger)
		mm.EXPECT().fetchTemporalClientForFlexCacheDelete(mock.Anything).Return(temporal)
		temporal.EXPECT().DescribeWorkflowExecution(cancelCtx, job.WorkflowID, "").Return(
			describeWorkflowExecutionWithStatus(enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING), nil).Once()
		cancel()

		act := &FlexCacheVolumeDeleteActivity{SE: mockStorage}
		err := act.WaitForFlexCacheCreateWorkflowTerminalActivity(cancelCtx, resultWith(vol))
		assert.Error(tt, err)
		assert.Equal(tt, context.Canceled, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	common "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func TestGCPUpdateVolumeDatabaseValue_PartialPatch(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	pool := &datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{}}
	account := &datamodel.Account{Name: "test-account"}
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "dp",
		Pool:      pool,
		Account:   account,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: true,
			CreationToken:    "keep-me",
		},
	}
	updated := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "rw",
		Pool:      pool,
		Account:   account,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
			CreationToken:    "keep-me",
		},
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Once()
	storage.EXPECT().
		UpdateVolumeFields(mock.Anything, volumeUUID, mock.MatchedBy(func(updates map[string]interface{}) bool {
			state, hasState := updates["state"]
			attrs, hasAttrs := updates["volume_attributes"]
			if !hasState || !hasAttrs {
				return false
			}
			if state != "rw" {
				return false
			}
			va, ok := attrs.(*datamodel.VolumeAttributes)
			return ok && va.IsDataProtection == false && va.CreationToken == "keep-me"
		})).
		Return(nil).Once()
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(updated, nil).Once()

	state := "rw"
	isDataProtection := false
	patch := &common.UpdateVolumeDatabaseValueParams{
		State: &state,
		VolumeAttributes: &common.UpdateVolumeAttributesPatch{
			IsDataProtection: &isDataProtection,
		},
	}

	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, patch)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, volumeUUID, result.UUID)
	require.Equal(t, "rw", result.LifeCycleState)
	require.Equal(t, false, result.IsDataProtection)
}

func TestGCPUpdateVolumeDatabaseValue_NoFieldsSkipsUpdate(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel:        datamodel.BaseModel{UUID: volumeUUID},
		State:            "rw",
		Pool:             &datamodel.Pool{PoolAttributes: &datamodel.PoolAttributes{}},
		Account:          &datamodel.Account{Name: "test-account"},
		VolumeAttributes: &datamodel.VolumeAttributes{},
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Twice()

	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &common.UpdateVolumeDatabaseValueParams{})
	require.NoError(t, err)
	require.NotNil(t, result)
	storage.AssertNotCalled(t, "UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything)
}

func TestGCPUpdateVolumeDatabaseValue_GetVolumeError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(nil, errors.New("not found")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &common.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestGCPUpdateVolumeDatabaseValue_UpdateVolumeFieldsError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "dp",
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Once()
	storage.EXPECT().
		UpdateVolumeFields(mock.Anything, volumeUUID, mock.Anything).
		Return(errors.New("update failed")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &common.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestGCPUpdateVolumeDatabaseValue_SecondGetVolumeError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "dp",
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Once()
	storage.EXPECT().
		UpdateVolumeFields(mock.Anything, volumeUUID, mock.Anything).
		Return(nil).Once()
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(nil, errors.New("refetch failed")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &common.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

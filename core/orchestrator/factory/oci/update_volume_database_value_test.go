package oci

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func TestOCIUpdateVolumeDatabaseValue_PartialPatch(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &OCIOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "2ndr",
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: true,
		},
	}
	updated := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "rw",
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
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
			return ok && va.IsDataProtection == false
		})).
		Return(nil).Once()
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(updated, nil).Once()

	state := "rw"
	isDataProtection := false
	patch := &commonparams.UpdateVolumeDatabaseValueParams{
		State: &state,
		VolumeAttributes: &commonparams.UpdateVolumeAttributesPatch{
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

func TestOCIUpdateVolumeDatabaseValue_NoFieldsSkipsUpdate(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &OCIOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}, State: "rw"}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Twice()

	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &commonparams.UpdateVolumeDatabaseValueParams{})
	require.NoError(t, err)
	require.NotNil(t, result)
	storage.AssertNotCalled(t, "UpdateVolumeFields", mock.Anything, mock.Anything, mock.Anything)
}

func TestOCIUpdateVolumeDatabaseValue_GetVolumeError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &OCIOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(nil, errors.New("not found")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &commonparams.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestOCIUpdateVolumeDatabaseValue_UpdateVolumeFieldsError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &OCIOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "2ndr",
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Once()
	storage.EXPECT().
		UpdateVolumeFields(mock.Anything, volumeUUID, mock.Anything).
		Return(errors.New("update failed")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &commonparams.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestOCIUpdateVolumeDatabaseValue_SecondGetVolumeError(t *testing.T) {
	ctx := context.Background()
	storage := database.NewMockStorage(t)
	orch := &OCIOrchestrator{storage: storage}

	volumeUUID := "f943038a-c2a1-4d96-8115-281ea02bd22d"
	existing := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		State:     "2ndr",
	}

	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(existing, nil).Once()
	storage.EXPECT().
		UpdateVolumeFields(mock.Anything, volumeUUID, mock.Anything).
		Return(nil).Once()
	storage.EXPECT().GetVolume(mock.Anything, volumeUUID).Return(nil, errors.New("refetch failed")).Once()

	state := "rw"
	result, err := orch.UpdateVolumeDatabaseValue(ctx, volumeUUID, &commonparams.UpdateVolumeDatabaseValueParams{State: &state})
	require.Error(t, err)
	require.Nil(t, result)
}

func TestConvertOCIDataStoreVolumeToModel_Nil(t *testing.T) {
	require.Nil(t, convertOCIDataStoreVolumeToModel(nil))
}

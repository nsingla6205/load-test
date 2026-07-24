package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestBuildVolumeDatabaseValueUpdates_NilPatch(t *testing.T) {
	dbVolume := &datamodel.Volume{State: "rw"}
	updates := BuildVolumeDatabaseValueUpdates(dbVolume, nil)
	assert.Empty(t, updates)
}

func TestBuildVolumeDatabaseValueUpdates_EmptyPatchProducesNoUpdates(t *testing.T) {
	dbVolume := &datamodel.Volume{State: "rw"}
	updates := BuildVolumeDatabaseValueUpdates(dbVolume, &UpdateVolumeDatabaseValueParams{})
	assert.Empty(t, updates)
}

func TestBuildVolumeDatabaseValueUpdates_StateAndStateDetailsOnly(t *testing.T) {
	dbVolume := &datamodel.Volume{State: "dp", StateDetails: "hidden dp volume"}
	state := "rw"
	stateDetails := ""
	patch := &UpdateVolumeDatabaseValueParams{State: &state, StateDetails: &stateDetails}

	updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
	assert.Equal(t, "rw", updates["state"])
	assert.Equal(t, "", updates["state_details"])
	_, hasAttrs := updates["volume_attributes"]
	assert.False(t, hasAttrs, "volume_attributes should be untouched when not present in patch")
	_, hasDeletedAt := updates["deleted_at"]
	assert.False(t, hasDeletedAt, "deleted_at should be untouched when not present in patch")
}

func TestBuildVolumeDatabaseValueUpdates_DeletedAtSetAndClear(t *testing.T) {
	now := time.Now()

	t.Run("set", func(t *testing.T) {
		dbVolume := &datamodel.Volume{}
		patch := &UpdateVolumeDatabaseValueParams{DeletedAtSet: true, DeletedAt: &now}
		updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
		assert.Contains(t, updates, "deleted_at")
		assert.NotNil(t, updates["deleted_at"])
	})

	t.Run("clear/undelete", func(t *testing.T) {
		dbVolume := &datamodel.Volume{}
		patch := &UpdateVolumeDatabaseValueParams{DeletedAtSet: true, DeletedAt: nil}
		updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
		assert.Contains(t, updates, "deleted_at")
		assert.Nil(t, updates["deleted_at"])
	})

	t.Run("omitted", func(t *testing.T) {
		dbVolume := &datamodel.Volume{}
		patch := &UpdateVolumeDatabaseValueParams{}
		updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
		assert.NotContains(t, updates, "deleted_at")
	})
}

func TestBuildVolumeDatabaseValueUpdates_VolumeAttributesPartialMerge(t *testing.T) {
	dbVolume := &datamodel.Volume{
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken:    "keep-me",
			IsDataProtection: true,
			Mounted:          true,
		},
	}
	isDataProtection := false
	patch := &UpdateVolumeDatabaseValueParams{
		VolumeAttributes: &UpdateVolumeAttributesPatch{
			IsDataProtection: &isDataProtection,
		},
	}

	updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
	attrs, ok := updates["volume_attributes"].(*datamodel.VolumeAttributes)
	assert.True(t, ok)
	assert.Equal(t, false, attrs.IsDataProtection, "isDataProtection should be overwritten")
	assert.Equal(t, "keep-me", attrs.CreationToken, "creationToken should be left unchanged")
	assert.Equal(t, true, attrs.Mounted, "mounted should be left unchanged")
	// dbVolume should be mutated in place too, since callers persist dbVolume.VolumeAttributes.
	assert.Same(t, dbVolume.VolumeAttributes, attrs)
}

func TestBuildVolumeDatabaseValueUpdates_VolumeAttributesCreatedWhenNil(t *testing.T) {
	dbVolume := &datamodel.Volume{}
	isDataProtection := true
	patch := &UpdateVolumeDatabaseValueParams{
		VolumeAttributes: &UpdateVolumeAttributesPatch{IsDataProtection: &isDataProtection},
	}

	updates := BuildVolumeDatabaseValueUpdates(dbVolume, patch)
	assert.NotNil(t, dbVolume.VolumeAttributes)
	attrs, ok := updates["volume_attributes"].(*datamodel.VolumeAttributes)
	assert.True(t, ok)
	assert.Equal(t, true, attrs.IsDataProtection)
}

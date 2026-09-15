package vcm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasPersistablePayload(t *testing.T) {
	assert.False(t, HasPersistablePayload(""))
	assert.False(t, HasPersistablePayload("null"))
	assert.True(t, HasPersistablePayload(`{"volumeName":"vol-1"}`))
}

func TestMarshalPayload(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		payload, err := MarshalPayload(nil)
		require.NoError(t, err)
		assert.Empty(t, payload)
	})

	t.Run("struct", func(t *testing.T) {
		payload, err := MarshalPayload(struct {
			VolumeName string `json:"volumeName"`
		}{VolumeName: "vol-1"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"volumeName":"vol-1"}`, payload)
	})

	t.Run("marshal error", func(t *testing.T) {
		_, err := MarshalPayload(make(chan int))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "vcm: marshal payload")
	})
}

func TestUnmarshalPayload(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		out, err := UnmarshalPayload[struct {
			VolumeName string `json:"volumeName"`
		}]("")
		require.NoError(t, err)
		assert.Empty(t, out.VolumeName)
	})

	t.Run("valid payload", func(t *testing.T) {
		out, err := UnmarshalPayload[struct {
			VolumeName string `json:"volumeName"`
		}](`{"volumeName":"vol-1"}`)
		require.NoError(t, err)
		assert.Equal(t, "vol-1", out.VolumeName)
	})

	t.Run("invalid payload", func(t *testing.T) {
		_, err := UnmarshalPayload[struct {
			VolumeName string `json:"volumeName"`
		}](`{invalid`)
		assert.Error(t, err)
	})
}

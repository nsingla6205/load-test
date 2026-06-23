package api

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
)

func TestConvertToPoolOCI(t *testing.T) {
	t.Run("nil pool returns empty poolResponse", func(t *testing.T) {
		out := convertToPoolOCI(nil)
		require.NotNil(t, out)
		assert.Equal(t, &poolResponse{}, out)
	})

	t.Run("maps core fields and lifecycle state", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{
				UUID:      "pool-uuid-1",
				CreatedAt: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
				UpdatedAt: time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC),
			},
			Name:                 "my-pool",
			Description:          "desc",
			State:                "creating",
			AccountName:          "ocid1.compartment..aaa",
			Region:               "eu-frankfurt-1",
			VendorSubNetID:       "ocid1.subnet..bbb",
			SizeInBytes:          1 << 40,
			TotalThroughputMibps: 100,
			TotalIops:            3000,
		}
		out := convertToPoolOCI(pool)
		require.NotNil(t, out)
		assert.Equal(t, "pool-uuid-1", out.ID)
		assert.Equal(t, "ocid1.compartment..aaa", out.CompartmentId)
		assert.Equal(t, "my-pool", out.ResourceName)
		assert.Equal(t, "eu-frankfurt-1", out.Region)
		assert.Equal(t, "ocid1.subnet..bbb", out.SubnetId)
		assert.Equal(t, int64(1<<40), out.SizeInBytes)
		assert.Equal(t, "CREATING", out.LifecycleState)
		assert.Equal(t, "desc", out.Description)
		assert.Equal(t, 100.0, out.ThroughputMibps)
		assert.Equal(t, int64(3000), out.Iops)
		require.NotNil(t, out.TimeCreated)
		assert.True(t, pool.CreatedAt.Equal(*out.TimeCreated))
		require.NotNil(t, out.TimeUpdated)
		assert.True(t, pool.UpdatedAt.Equal(*out.TimeUpdated))
	})

	t.Run("omits time pointers when CreatedAt and UpdatedAt are zero", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "u"},
			Name:      "p",
			State:     "ACTIVE",
		}
		out := convertToPoolOCI(pool)
		assert.Nil(t, out.TimeCreated)
		assert.Nil(t, out.TimeUpdated)
	})

	t.Run("maps PoolAttributes zones and tags when labels non-empty", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "u"},
			Name:      "p",
			State:     "ACTIVE",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone:   "ad-1",
				SecondaryZone: "ad-2",
				Labels:        map[string]string{"env": "test"},
			},
		}
		out := convertToPoolOCI(pool)
		assert.Equal(t, "ad-1", out.PrimaryAvailabilityDomain)
		assert.Equal(t, "ad-2", out.SecondaryAvailabilityDomain)
		require.NotNil(t, out.Tags)
		assert.Equal(t, "test", out.Tags["env"])
	})

	t.Run("does not set Tags when PoolAttributes labels empty", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel: models.BaseModel{UUID: "u"},
			Name:      "p",
			State:     "ACTIVE",
			PoolAttributes: &models.PoolAttributes{
				PrimaryZone: "z1",
				Labels:      map[string]string{},
			},
		}
		out := convertToPoolOCI(pool)
		assert.Nil(t, out.Tags)
	})

	t.Run("uses CustomPerformanceParams when enabled", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel:            models.BaseModel{UUID: "u"},
			Name:                 "p",
			State:                "ACTIVE",
			TotalThroughputMibps: 50,
			TotalIops:            1000,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    true,
				Throughput: 200,
				Iops:       5000,
			},
		}
		out := convertToPoolOCI(pool)
		assert.Equal(t, 200.0, out.ThroughputMibps)
		assert.Equal(t, int64(5000), out.Iops)
	})

	t.Run("uses totals when CustomPerformanceParams disabled", func(t *testing.T) {
		pool := &models.Pool{
			BaseModel:            models.BaseModel{UUID: "u"},
			Name:                 "p",
			State:                "ACTIVE",
			TotalThroughputMibps: 77,
			TotalIops:            2000,
			CustomPerformanceParams: &models.CustomPerformanceParams{
				Enabled:    false,
				Throughput: 999,
				Iops:       9999,
			},
		}
		out := convertToPoolOCI(pool)
		assert.Equal(t, 77.0, out.ThroughputMibps)
		assert.Equal(t, int64(2000), out.Iops)
	})
}

func TestMapLifecycleState(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"CREATING", "CREATING"},
		{"creating", "CREATING"},
		{"ACTIVE", "ACTIVE"},
		{"AVAILABLE", "ACTIVE"},
		{"available", "ACTIVE"},
		{"UPDATING", "UPDATING"},
		{"DELETING", "DELETING"},
		{"DELETED", "DELETED"},
		{"FAILED", "FAILED"},
		{"unknown", "STATUS_UNKNOWN"},
		{"", "STATUS_UNKNOWN"},
	}
	for _, tt := range tests {
		name := tt.in
		if name == "" {
			name = "empty_input"
		}
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, mapLifecycleState(tt.in))
		})
	}
}

func TestEncodePoolV1(t *testing.T) {
	t.Run("encodes poolResponse to JSON bytes", func(t *testing.T) {
		ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
		in := &poolResponse{
			ID:             "id-1",
			CompartmentId:  "comp",
			ResourceName:    "dn",
			LifecycleState: "ACTIVE",
			TimeCreated:    &ts,
		}
		raw, err := encodePoolV1(in)
		require.NoError(t, err)
		require.NotNil(t, raw)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(raw, &decoded))
		assert.Equal(t, "id-1", decoded["id"])
		assert.Equal(t, "comp", decoded["compartmentId"])
		assert.Equal(t, "dn", decoded["resourceName"])
		assert.Equal(t, "ACTIVE", decoded["lifecycleState"])
		assert.Contains(t, decoded, "timeCreated")
	})
}

func TestEncodePoolV1_MarshalError(t *testing.T) {
	t.Parallel()
	orig := jsonMarshalPoolResponse
	t.Cleanup(func() { jsonMarshalPoolResponse = orig })
	jsonMarshalPoolResponse = func(_ *poolResponse) ([]byte, error) {
		return nil, errors.New("marshal failed")
	}
	_, err := encodePoolV1(&poolResponse{ID: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "marshal failed")
}

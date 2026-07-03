package common

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// Helper function to convert HydratedMetrics to DataPoint for testing
func hydratedMetricsToDataPoints(metrics []datamodel2.HydratedMetrics) []DataPoint {
	var dataPoints []DataPoint
	for _, metric := range metrics {
		dataPoints = append(dataPoints, DataPoint{
			Timestamp: metric.MetricTimestamp,
			Quantity:  metric.Quantity,
		})
	}
	// Sort data points by timestamp as required by aggregation functions
	sort.Slice(dataPoints, func(i, j int) bool {
		return dataPoints[i].Timestamp.Before(dataPoints[j].Timestamp)
	})
	return dataPoints
}

func TestIntegral(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		metrics  []datamodel2.HydratedMetrics
		expected float64
	}{
		{
			name:     "empty metrics",
			metrics:  []datamodel2.HydratedMetrics{},
			expected: 0,
		},
		{
			name: "single metric",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        100,
				},
			},
			expected: 0,
		},
		{
			name: "two metrics",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        100,
				},
				{
					MetricTimestamp: now,
					Quantity:        200,
				},
			},
			expected: 200, // 200 * 1 hour = 200 (uses second value * duration)
		},
		{
			name: "three metrics unsorted",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        100,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        200,
				},
			},
			expected: 500, // After sorting: 200 * 1 hour + 300 * 1 hour = 500 (uses each subsequent value * duration)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Integral(hydratedMetricsToDataPoints(tt.metrics))
			assert.InDelta(t, tt.expected, got, 0.001, "Integral calculation did not match expected value")
		})
	}
}

func TestCounter(t *testing.T) {
	now := time.Now()
	logger := util.GetLogger(context.Background())
	tests := []struct {
		name         string
		metrics      []datamodel2.HydratedMetrics
		measuredType metadata.MeasuredType
		resourceUUID string
		expected     float64
	}{
		{
			name:         "empty metrics",
			metrics:      []datamodel2.HydratedMetrics{},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-1",
			expected:     0,
		},
		{
			name: "single metric",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        100,
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-2",
			expected:     0,
		},
		{
			name: "two metrics",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        100,
				},
				{
					MetricTimestamp: now,
					Quantity:        250,
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-3",
			expected:     150, // 250 - 100 = 150
		},
		{
			name: "three metrics unsorted",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        50,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        150,
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-4",
			expected:     250, // 300 - 50 = 250
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := CounterDelta(hydratedMetricsToDataPoints(tt.metrics), logger, tt.measuredType, tt.resourceUUID)
			assert.InDelta(t, tt.expected, got, 0.001, "Counter calculation did not match expected value")
		})
	}
}

func TestSum(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		metrics  []datamodel2.HydratedMetrics
		expected float64
	}{
		{
			name:     "empty metrics",
			metrics:  []datamodel2.HydratedMetrics{},
			expected: 0,
		},
		{
			name: "single metric",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        100,
				},
			},
			expected: 100,
		},
		{
			name: "multiple metrics",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        50,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        150,
				},
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
			},
			expected: 500, // 50 + 150 + 300 = 500
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sum(hydratedMetricsToDataPoints(tt.metrics))
			assert.InDelta(t, tt.expected, got, 0.001, "Sum calculation did not match expected value")
		})
	}
}

func TestFirst(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		metrics  []datamodel2.HydratedMetrics
		expected float64
	}{
		{
			name:     "empty metrics",
			metrics:  []datamodel2.HydratedMetrics{},
			expected: 0,
		},
		{
			name: "single metric",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        100,
				},
			},
			expected: 100,
		},
		{
			name: "multiple metrics sorted",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        50,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        150,
				},
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
			},
			expected: 50, // First (earliest) value is 50
		},
		{
			name: "multiple metrics unsorted",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        50,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        150,
				},
			},
			expected: 50, // First (earliest) value is 50
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := First(hydratedMetricsToDataPoints(tt.metrics))
			assert.InDelta(t, tt.expected, got, 0.001, "First calculation did not match expected value")
		})
	}
}

func TestCounterDelta_CounterReset(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()
	tests := []struct {
		name         string
		metrics      []datamodel2.HydratedMetrics
		measuredType metadata.MeasuredType
		resourceUUID string
		expected     float64
	}{
		{
			name: "counter reset - quantity drops below 25% threshold",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        200, // This should trigger counter reset (200 < 1000 * 0.25)
				},
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-reset",
			expected:     300, // Only counts from reset point: 200 (reset value) + (300-200) = 300
		},
		{
			name: "anomalous dip - quantity drops but above 25% threshold",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-2 * time.Hour),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        500, // This is above 25% threshold (1000 * 0.25 = 250), so skip this point
				},
				{
					MetricTimestamp: now,
					Quantity:        1200,
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-anomaly",
			expected:     200, // Only counts 1200 - 1000 = 200, skips the anomalous 500 value
		},
		{
			name: "negative quantity difference but not counter reset",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-1 * time.Hour),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now,
					Quantity:        800, // 800 > 1000 * 0.25 = 250, so this is anomalous dip, skip it
				},
			},
			measuredType: metadata.AllocatedSize,
			resourceUUID: "test-uuid-neg",
			expected:     0, // No valid delta computed since anomalous point was skipped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := CounterDelta(hydratedMetricsToDataPoints(tt.metrics), logger, tt.measuredType, tt.resourceUUID)
			assert.InDelta(t, tt.expected, got, 0.001, "CounterDelta with reset scenario did not match expected value")
		})
	}
}

// TestCounterDelta_CoolTierWriteSpecialHandling tests the special handling for CoolTierDataWriteSizeRaw
func TestCounterDelta_CoolTierWriteSpecialHandling(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()
	tests := []struct {
		name         string
		metrics      []datamodel2.HydratedMetrics
		resourceUUID string
		expected     float64
	}{
		{
			name: "cool tier write - skip when value decreases",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-15 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        900, // Decrease - should be skipped
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "pool-uuid-write",
			expected:     1000, // (1500-1000) + (2000-1500) = 500 + 500 = 1000 (900 is skipped, lastPoint stays at 1500)
		},
		{
			name: "cool tier write - normal increase",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "pool-uuid-write-normal",
			expected:     1000, // 2000 - 1000 = 1000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := CounterDelta(hydratedMetricsToDataPoints(tt.metrics), logger, metadata.CoolTierDataWriteSizeRaw, tt.resourceUUID)
			assert.InDelta(t, tt.expected, got, 0.001, "CoolTierDataWriteSizeRaw handling did not match expected value")
		})
	}
}

// TestCounterDelta_CoolTierReadSpecialHandling tests the special handling for CoolTierDataReadSizeRaw
func TestCounterDelta_CoolTierReadSpecialHandling(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()
	tests := []struct {
		name         string
		metrics      []datamodel2.HydratedMetrics
		resourceUUID string
		expected     float64
	}{
		{
			name: "cool tier read - skip when value decreases to zero",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-15 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        0, // Decrease to zero - should be skipped
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "pool-uuid-read",
			expected:     1000, // 1500-1000=500 (valid), skip 0, 2000-1500=500 (valid, last valid was 1500), total=1000
		},
		{
			name: "cool tier read - allow decrease to non-zero (uses standard logic)",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        200, // Decrease but not to zero - standard counter reset logic applies
				},
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
			},
			resourceUUID: "pool-uuid-read-reset",
			expected:     300, // Counter reset: 200 (reset value) + (300-200) = 300
		},
		{
			name: "cool tier read - normal increase",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "pool-uuid-read-normal",
			expected:     1000, // 2000 - 1000 = 1000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := CounterDelta(hydratedMetricsToDataPoints(tt.metrics), logger, metadata.CoolTierDataReadSizeRaw, tt.resourceUUID)
			assert.InDelta(t, tt.expected, got, 0.001, "CoolTierDataReadSizeRaw handling did not match expected value")
		})
	}
}

// TestCounterDelta_XregionReplicationSpecialHandling tests the special handling for XregionReplicationTotalTransferBytes
func TestCounterDelta_XregionReplicationSpecialHandling(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()
	tests := []struct {
		name         string
		metrics      []datamodel2.HydratedMetrics
		resourceUUID string
		expected     float64
	}{
		{
			name: "xregion replication - skip when value decreases to zero",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-15 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        0, // Decrease to zero - should be skipped
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "replication-uuid",
			expected:     1000, // 1500-1000 + (2000-1500) = 500 + 500 = 1000
		},
		{
			name: "xregion replication - allow decrease to non-zero (uses standard logic)",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        200, // Decrease but not to zero - standard counter reset logic applies
				},
				{
					MetricTimestamp: now,
					Quantity:        300,
				},
			},
			resourceUUID: "replication-uuid-reset",
			expected:     300, // Counter reset: 200 (reset value) + (300-200) = 300
		},
		{
			name: "xregion replication - normal increase",
			metrics: []datamodel2.HydratedMetrics{
				{
					MetricTimestamp: now.Add(-10 * time.Minute),
					Quantity:        1000,
				},
				{
					MetricTimestamp: now.Add(-5 * time.Minute),
					Quantity:        1500,
				},
				{
					MetricTimestamp: now,
					Quantity:        2000,
				},
			},
			resourceUUID: "replication-uuid-normal",
			expected:     1000, // 2000 - 1000 = 1000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := CounterDelta(hydratedMetricsToDataPoints(tt.metrics), logger, metadata.XregionReplicationTotalTransferBytes, tt.resourceUUID)
			assert.InDelta(t, tt.expected, got, 0.001, "XregionReplicationTotalTransferBytes handling did not match expected value")
		})
	}
}

// TestCounterDelta_CoolTierBoundaryConditions tests boundary conditions for cool tier metrics
func TestCounterDelta_CoolTierBoundaryConditions(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("CoolTierWrite - all samples are decrements", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        5000,
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        4000, // Decrement - skip
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        3000, // Decrement - skip
			},
			{
				MetricTimestamp: now,
				Quantity:        2000, // Decrement - skip
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-all-decrements")
		// All samples are decrements, so nothing is aggregated
		assert.Equal(t, 0.0, got, "Expected 0 when all samples are decrements")
	})

	t.Run("CoolTierWrite - oscillating between increase and decrease", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-20 * time.Minute),
				Quantity:        1000,
			},
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        1500, // +500
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        1200, // Decrement - skip
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        1800, // +300 (from 1500, not 1200)
			},
			{
				MetricTimestamp: now,
				Quantity:        2200, // +400
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-oscillating")
		// (1500-1000) + (1800-1500) + (2200-1800) = 500 + 300 + 400 = 1200
		assert.InDelta(t, 1200.0, got, 0.001, "Expected correct aggregation skipping decrements")
	})

	t.Run("CoolTierRead - oscillating between zero and non-zero", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-20 * time.Minute),
				Quantity:        1000,
			},
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        1500, // +500
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        800, // +800 (from 1500, not 0)
			},
			{
				MetricTimestamp: now,
				Quantity:        0, // Drop to zero again - skip
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataReadSizeRaw, "pool-oscillating-zero")
		// (1500-1000) + skip 0 + (800-1500 skipped) = 500, then (2300-1500) = 800
		// Actually: (1500-1000) = 500, skip 0, (800-1500)=-700 but 800 > 1500*0.25=375, so skip anomalous dip
		// So only first increment counts
		assert.InDelta(t, 500.0, got, 0.001, "Expected only valid increments")
	})

	t.Run("CoolTierRead - starting from zero", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        0,
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        500, // +500
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        1000, // +500
			},
			{
				MetricTimestamp: now,
				Quantity:        1500, // +500
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-start-zero")
		// 500 + 500 + 500 = 1500
		assert.InDelta(t, 1500.0, got, 0.001, "Expected correct aggregation from zero start")
	})

	t.Run("CoolTierWrite - lastPoint exactly zero then increase", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        0,
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        1000, // +1000
			},
			{
				MetricTimestamp: now,
				Quantity:        1500, // +500
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-zero-start")
		// 1000 + 500 = 1500
		assert.InDelta(t, 1500.0, got, 0.001, "Expected correct aggregation from zero")
	})

	t.Run("XregionReplication - multiple zeros in sequence", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-20 * time.Minute),
				Quantity:        1000,
			},
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        1500, // +500
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        0, // Still zero - no change, delta=0
			},
			{
				MetricTimestamp: now,
				Quantity:        2000, // +2000 from last valid point (1500)
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.XregionReplicationTotalTransferBytes, "repl-multiple-zeros")
		// (1500-1000) + skip 0 + (2000-1500) = 500 + 500 = 1000
		assert.InDelta(t, 1000.0, got, 0.001, "Expected correct handling of multiple zeros")
	})

	t.Run("CoolTierWrite - single data point", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now,
				Quantity:        1000,
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-single")
		// Need at least 2 points
		assert.Equal(t, 0.0, got, "Expected 0 for single data point")
	})

	t.Run("CoolTierWrite - decrease to negative value is skipped", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        1000,
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        -100, // Invalid negative value, should be skipped (any decrease)
			},
			{
				MetricTimestamp: now,
				Quantity:        200, // This is also a decrease from 1000, should be skipped
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "pool-negative")
		// CoolTierDataWriteSizeRaw skips ANY decrease, so both -100 and 200 are skipped
		// Result: 0
		assert.Equal(t, 0.0, got, "Expected 0 when all samples decrease for CoolTierWrite")
	})

	t.Run("CoolTierRead - pure alternating oscillation between 0 and non-zero every sample", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-30 * time.Minute),
				Quantity:        1000,
			},
			{
				MetricTimestamp: now.Add(-25 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now.Add(-20 * time.Minute),
				Quantity:        1500, // Increase from last valid (1000), delta=500
			},
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        2000, // Increase from last valid (1500), delta=500
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now,
				Quantity:        2500, // Increase from last valid (2000), delta=500
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataReadSizeRaw, "pool-pure-oscillation")
		// (1500-1000) + (2000-1500) + (2500-2000) = 500 + 500 + 500 = 1500
		// All zeros are skipped, lastPoint maintained at last non-zero value
		assert.InDelta(t, 1500.0, got, 0.001, "Expected correct aggregation with pure 0/non-zero oscillation")
	})

	t.Run("XregionReplication - pure alternating oscillation between 0 and non-zero every sample", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{
				MetricTimestamp: now.Add(-30 * time.Minute),
				Quantity:        500,
			},
			{
				MetricTimestamp: now.Add(-25 * time.Minute),
				Quantity:        0, // Drop to zero - skip (replication paused)
			},
			{
				MetricTimestamp: now.Add(-20 * time.Minute),
				Quantity:        800, // Replication resumed, delta=300 from last valid (500)
			},
			{
				MetricTimestamp: now.Add(-15 * time.Minute),
				Quantity:        0, // Drop to zero - skip (replication paused again)
			},
			{
				MetricTimestamp: now.Add(-10 * time.Minute),
				Quantity:        1200, // Replication resumed, delta=400 from last valid (800)
			},
			{
				MetricTimestamp: now.Add(-5 * time.Minute),
				Quantity:        0, // Drop to zero - skip
			},
			{
				MetricTimestamp: now,
				Quantity:        1800, // Replication resumed, delta=600 from last valid (1200)
			},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.XregionReplicationTotalTransferBytes, "repl-pure-oscillation")
		// (800-500) + (1200-800) + (1800-1200) = 300 + 400 + 600 = 1300
		// All zeros are skipped, lastPoint maintained at last non-zero value
		assert.InDelta(t, 1300.0, got, 0.001, "Expected correct aggregation with pure 0/non-zero oscillation during replication pause/resume cycles")
	})
}

// TestCounterDelta_EdgeCasesAllMetricTypes tests edge cases across all counter metric types
func TestCounterDelta_EdgeCasesAllMetricTypes(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("Empty data points", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "empty")
		assert.Equal(t, 0.0, got, "Expected 0 for empty data points")
	})

	t.Run("All identical values", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now, Quantity: 1000},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.AllocatedSize, "identical")
		assert.Equal(t, 0.0, got, "Expected 0 for all identical values")
	})

	t.Run("Very large values", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1e15},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 1e15 + 1e12},
			{MetricTimestamp: now, Quantity: 1e15 + 2e12},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "large-values")
		assert.InDelta(t, 2e12, got, 1e9, "Expected correct handling of large values")
	})

	t.Run("Very small increments", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 1000.001},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000.002},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 1000.003},
			{MetricTimestamp: now, Quantity: 1000.004},
		}
		got, _ := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.AllocatedSize, "tiny-increments")
		assert.InDelta(t, 0.003, got, 0.0001, "Expected correct handling of tiny increments")
	})
}

func TestCounterDelta_VolumeATRawMetrics(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("CoolTierDataWriteSizeRaw volume skips any decrease", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 2000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 1500},
			{MetricTimestamp: now, Quantity: 3000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "vol-write-test")
		assert.InDelta(t, 2000.0, got, 0.001, "Should skip decrease from 2000->1500 and compute (2000-1000)+(3000-2000)")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 3000.0, *lastVal)
	})

	t.Run("CoolTierDataReadSizeRaw volume skips only decrease to zero", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 2000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 0},
			{MetricTimestamp: now, Quantity: 3000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataReadSizeRaw, "vol-read-test")
		assert.InDelta(t, 2000.0, got, 0.001, "Should skip 2000->0 and compute deltas for increasing values")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 3000.0, *lastVal)
	})
}

func TestCounterDelta_CbsCrossRegionBackupTransferBytes(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("first window with zero baseline - full counter is the delta", func(t *testing.T) {
		// Zero baseline prepended (as done by calculateCounterDeltaWithAggregatedHistory on first window)
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-35 * time.Minute), Quantity: 0},
			{MetricTimestamp: now.Add(-30 * time.Minute), Quantity: 272043},
			{MetricTimestamp: now.Add(-25 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now, Quantity: 387575},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-first-window")
		assert.InDelta(t, 387575.0, got, 0.001, "Expected full counter as delta with zero baseline")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 387575.0, *lastVal)
	})

	t.Run("normal monotonic increase across windows", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 272043},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 503159},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 618707},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-normal-inc")
		assert.InDelta(t, 346664.0, got, 0.001, "Delta should be 618707 - 272043 = 346664")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 618707.0, *lastVal)
	})

	t.Run("counter decrease to zero - treated as counter reset", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 503159},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 0},
			{MetricTimestamp: now, Quantity: 618707},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-drop-zero")
		// 503159-387575=115584, counter reset to 0 (quantity=0), 618707-0=618707 => 734291
		assert.InDelta(t, 734291.0, got, 0.001, "Zero treated as counter reset, not skipped")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 618707.0, *lastVal)
	})

	t.Run("counter reset - new backup replaces completed one", func(t *testing.T) {
		// Old backup finished at 618707, new backup starts at 272043.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 618707},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 272043},
			{MetricTimestamp: now, Quantity: 387575},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-new-backup")
		// Counter reset: 272043 (reset value) + (387575-272043=115532) = 387575
		assert.InDelta(t, 387575.0, got, 0.001, "Non-zero decrease should be treated as counter reset")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 387575.0, *lastVal)
	})

	t.Run("flat counter - no transfer activity", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 387575},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-flat")
		assert.InDelta(t, 0.0, got, 0.001, "Flat counter should produce zero delta")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 387575.0, *lastVal)
	})

	t.Run("single data point - no delta possible", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now, Quantity: 272043},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-single")
		assert.Equal(t, 0.0, got, "Single point should return zero delta")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 272043.0, *lastVal)
	})

	t.Run("real SDE two-backup scenario", func(t *testing.T) {
		// Based on SDE aggregated_usages for volume 9c9905b2 window 13:00-14:00.
		// SDE reported 0.36962032 MiB (387575 bytes with zero baseline).
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-35 * time.Minute), Quantity: 0},      // zero baseline
			{MetricTimestamp: now.Add(-30 * time.Minute), Quantity: 272043}, // 13:30
			{MetricTimestamp: now.Add(-25 * time.Minute), Quantity: 387575}, // 13:35
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 387575}, // 13:40
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575}, // 13:45
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 387575}, // 13:50
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 387575},  // 13:55
			{MetricTimestamp: now, Quantity: 387575},                        // 14:00
		}
		gotBytes, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "sde-real")
		gotMiB := gotBytes / (1024 * 1024)
		assert.InDelta(t, 0.36962032, gotMiB, 0.00001, "Must match SDE quantity of 0.36962032 MiB")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 387575.0, *lastVal)
	})

	t.Run("counter reset below 25% threshold", func(t *testing.T) {
		// Drop from 1000000 to 100000 (10%) — hits the generic < 25% reset path
		// before the CBS-specific branch. Result should be the same: counter reset.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 100000},
			{MetricTimestamp: now, Quantity: 200000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-reset-low")
		// Counter reset: 100000 + (200000-100000) = 200000
		assert.InDelta(t, 200000.0, got, 0.001, "Drop below 25% should also trigger counter reset")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 200000.0, *lastVal)
	})

	t.Run("multiple resets within one window", func(t *testing.T) {
		// Backup A finishes at 500000, backup B runs to 300000, then backup C starts at 100000.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-25 * time.Minute), Quantity: 400000},
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 500000},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 300000}, // reset (new backup B)
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 400000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 100000}, // reset (new backup C)
			{MetricTimestamp: now, Quantity: 200000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-multi-reset")
		// (500000-400000) + reset(300000) + (400000-300000) + reset(100000) + (200000-100000)
		// = 100000 + 300000 + 100000 + 100000 + 100000 = 700000
		assert.InDelta(t, 700000.0, got, 0.001, "Multiple resets should each contribute their reset value plus deltas")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 200000.0, *lastVal)
	})

	t.Run("cache-hit second window with flat counter", func(t *testing.T) {
		// Simulates second aggregation window where the cache value is prepended.
		// Cache = 387575, new data points are all 387575. Delta = 0.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 387575}, // synthetic from cache
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 387575},
			{MetricTimestamp: now, Quantity: 387575},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-cache-flat")
		assert.InDelta(t, 0.0, got, 0.001, "Flat counter with cache prepend should produce zero delta")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 387575.0, *lastVal)
	})

	t.Run("cache-hit second window with increase", func(t *testing.T) {
		// Cache = 387575 prepended, new points increase to 618707.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 387575}, // synthetic from cache
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 503159},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 618707},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-cache-inc")
		// 618707 - 387575 = 231132
		assert.InDelta(t, 231132.0, got, 0.001, "Delta from cached baseline to last point")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 618707.0, *lastVal)
	})

	t.Run("empty data points", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-empty")
		assert.Equal(t, 0.0, got, "Empty points should return zero")
		assert.Nil(t, lastVal)
	})
}

// TestCounterDelta_CbsCrossRegionBackupTransferBytes_AdditionalScenarios covers
// additional edge cases for the CBS-specific counter reset logic.
func TestCounterDelta_CbsCrossRegionBackupTransferBytes_AdditionalScenarios(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("back-to-back resets without increase between", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 500000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 300000}, // reset (backup B replaces A)
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 100000},  // reset again (backup C replaces B)
			{MetricTimestamp: now, Quantity: 250000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-back-to-back-reset")
		// reset(300000) + reset(100000) + (250000-100000) = 300000 + 100000 + 150000 = 550000
		assert.InDelta(t, 550000.0, got, 0.001, "Back-to-back resets should each contribute their value")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 250000.0, *lastVal)
	})

	t.Run("non-zero dip above 25% treated as reset for CBS", func(t *testing.T) {
		// For non-CBS metrics, 600 > 1000*0.25=250 would be skipped as anomalous.
		// For CBS, any non-zero decrease is a counter reset.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 600}, // 60% of previous — anomalous for others, reset for CBS
			{MetricTimestamp: now, Quantity: 900},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-dip-above-25pct")
		// reset(600) + (900-600) = 600 + 300 = 900
		assert.InDelta(t, 900.0, got, 0.001, "CBS treats any non-zero decrease as reset, not anomalous dip")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 900.0, *lastVal)
	})

	t.Run("zero baseline then mid-window reset", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-25 * time.Minute), Quantity: 0},      // zero baseline
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 200000}, // first backup
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 400000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 150000}, // reset (new backup)
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 300000},
			{MetricTimestamp: now, Quantity: 450000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-zero-then-reset")
		// 200000 + (400000-200000) + reset(150000) + (300000-150000) + (450000-300000)
		// = 200000 + 200000 + 150000 + 150000 + 150000 = 850000
		assert.InDelta(t, 850000.0, got, 0.001, "Zero baseline followed by mid-window reset")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 450000.0, *lastVal)
	})

	t.Run("two data points with reset", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 500000},
			{MetricTimestamp: now, Quantity: 100000}, // reset
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-two-pt-reset")
		// reset(100000)
		assert.InDelta(t, 100000.0, got, 0.001, "Two-point reset should use current value as delta")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 100000.0, *lastVal)
	})

	t.Run("all zeros", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 0},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 0},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 0},
			{MetricTimestamp: now, Quantity: 0},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-all-zeros")
		assert.Equal(t, 0.0, got, "All zeros should produce zero delta")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 0.0, *lastVal)
	})

	t.Run("decrease to zero then consecutive zeros then increase", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-25 * time.Minute), Quantity: 300000},
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 500000},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 0}, // reset to 0
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 0}, // flat at 0
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 0},  // flat at 0
			{MetricTimestamp: now, Quantity: 200000},                   // new backup starts
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-zeros-then-increase")
		// (500000-300000) + reset(0) + 0 + 0 + (200000-0) = 200000 + 0 + 0 + 0 + 200000 = 400000
		assert.InDelta(t, 400000.0, got, 0.001, "Zeros followed by new backup should be handled correctly")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 200000.0, *lastVal)
	})

	t.Run("monotonic decrease every sample - all resets", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 800000},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 600000}, // reset
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 400000}, // reset
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 200000},  // reset
			{MetricTimestamp: now, Quantity: 100000},                        // reset
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-all-decreasing")
		// reset(600000) + reset(400000) + reset(200000) + reset(100000) = 1300000
		assert.InDelta(t, 1300000.0, got, 0.001, "Each decrease treated as a reset contributing its value")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 100000.0, *lastVal)
	})

	t.Run("realistic large backup sizes with reset", func(t *testing.T) {
		// 10 GiB and 5 GiB backups
		tenGiB := 10.0 * 1024 * 1024 * 1024
		fiveGiB := 5.0 * 1024 * 1024 * 1024
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: tenGiB * 0.5},
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: tenGiB},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: fiveGiB * 0.3}, // reset (new smaller backup)
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: fiveGiB * 0.7},
			{MetricTimestamp: now, Quantity: fiveGiB},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-large-backup")
		// (tenGiB - tenGiB*0.5) + reset(fiveGiB*0.3) + (fiveGiB*0.7 - fiveGiB*0.3) + (fiveGiB - fiveGiB*0.7)
		// = tenGiB*0.5 + fiveGiB*0.3 + fiveGiB*0.4 + fiveGiB*0.3
		// = 5GiB + 1.5GiB + 2GiB + 1.5GiB = 10GiB
		expectedDelta := tenGiB*0.5 + fiveGiB*0.3 + fiveGiB*0.4 + fiveGiB*0.3
		assert.InDelta(t, expectedDelta, got, 1.0, "Large backup sizes with reset should compute correctly")
		assert.NotNil(t, lastVal)
		assert.InDelta(t, fiveGiB, *lastVal, 1.0)
	})

	t.Run("reset exactly at 25% boundary", func(t *testing.T) {
		// point.Quantity == lastPoint.Quantity * 0.25 — NOT less than, so the generic
		// <25% branch is false, but CBS OR-branch triggers the reset anyway.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 250}, // exactly 25%
			{MetricTimestamp: now, Quantity: 400},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-exact-25pct")
		// CBS OR-branch: reset(250) + (400-250) = 250 + 150 = 400
		assert.InDelta(t, 400.0, got, 0.001, "Exactly at 25% boundary should still reset for CBS")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 400.0, *lastVal)
	})

	t.Run("cache-hit second window with counter reset across windows", func(t *testing.T) {
		// Previous window ended at 618707 (cached). New window's data starts at 150000 (new backup).
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-20 * time.Minute), Quantity: 618707}, // synthetic from cache
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 150000}, // reset (new backup)
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 300000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 450000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-cache-cross-window-reset")
		// reset(150000) + (300000-150000) + (450000-300000) = 150000 + 150000 + 150000 = 450000
		assert.InDelta(t, 450000.0, got, 0.001, "Cross-window reset from cached value should be handled")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 450000.0, *lastVal)
	})

	t.Run("single increase followed by reset to same value", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-15 * time.Minute), Quantity: 100000},
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 500000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 100000}, // reset back to original value
			{MetricTimestamp: now, Quantity: 300000},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CbsCrossRegionVolumeBackupTransferBytes, "cbs-reset-to-same")
		// (500000-100000) + reset(100000) + (300000-100000) = 400000 + 100000 + 200000 = 700000
		assert.InDelta(t, 700000.0, got, 0.001, "Reset back to original starting value")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 300000.0, *lastVal)
	})
}

// TestCounterDelta_NonCbsMetricDipUnchanged verifies that the CBS-specific
// counter reset does not affect other counter metric types.
func TestCounterDelta_NonCbsMetricDipUnchanged(t *testing.T) {
	logger := util.GetLogger(context.Background())
	now := time.Now()

	t.Run("XregionReplication non-zero dip above 25% is still skipped", func(t *testing.T) {
		// 500 is above 1000*0.25=250, so this is an anomalous dip for non-CBS metrics.
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 500},
			{MetricTimestamp: now, Quantity: 1200},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.XregionReplicationTotalTransferBytes, "xreg-dip")
		// 500 is skipped as anomalous dip, delta = 1200 - 1000 = 200
		assert.InDelta(t, 200.0, got, 0.001, "Non-CBS dip above 25% should still be skipped")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 1200.0, *lastVal)
	})

	t.Run("CoolTierDataReadSizeRaw non-zero dip above 25% is still skipped", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 800},
			{MetricTimestamp: now, Quantity: 1500},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataReadSizeRaw, "cold-read-dip")
		// 800 is skipped as anomalous dip (800 > 1000*0.25), delta = 1500 - 1000 = 500
		assert.InDelta(t, 500.0, got, 0.001, "CoolTierRead dip above 25% should still be skipped")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 1500.0, *lastVal)
	})

	t.Run("CoolTierDataWriteSizeRaw any decrease is still skipped", func(t *testing.T) {
		metrics := []datamodel2.HydratedMetrics{
			{MetricTimestamp: now.Add(-10 * time.Minute), Quantity: 1000},
			{MetricTimestamp: now.Add(-5 * time.Minute), Quantity: 900},
			{MetricTimestamp: now, Quantity: 1500},
		}
		got, lastVal := CounterDelta(hydratedMetricsToDataPoints(metrics), logger, metadata.CoolTierDataWriteSizeRaw, "cold-write-dip")
		// 900 is skipped (CoolTierWrite skips any decrease), delta = 1500 - 1000 = 500
		assert.InDelta(t, 500.0, got, 0.001, "CoolTierWrite any decrease should still be skipped")
		assert.NotNil(t, lastVal)
		assert.Equal(t, 1500.0, *lastVal)
	})
}

package metadata

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeasuredType_String(t *testing.T) {
	tests := []struct {
		name     string
		mt       MeasuredType
		expected string
	}{
		{
			name:     "PoolAllocatedSize string conversion",
			mt:       PoolAllocatedSize,
			expected: "POOL_ALLOCATED_SIZE",
		},
		{
			name:     "AllocatedUsed string conversion",
			mt:       AllocatedUsed,
			expected: "ALLOCATED_USED",
		},
		{
			name:     "UnknownMeasuredType string conversion",
			mt:       UnknownMeasuredType,
			expected: "UNKNOWN_MEASURED_TYPE",
		},
		{
			name:     "Custom MeasuredType string conversion",
			mt:       MeasuredType("CUSTOM_METRIC"),
			expected: "CUSTOM_METRIC",
		},
		{
			name:     "VolumeAllocatedThroughput string conversion",
			mt:       VolumeAllocatedThroughput,
			expected: "VOLUME_ALLOCATED_THROUGHPUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mt.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewMeasuredType(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedType   MeasuredType
		expectedExists bool
	}{
		{
			name:           "Valid pool_allocated_size metric",
			input:          "pool_allocated_size",
			expectedType:   PoolAllocatedSize,
			expectedExists: true,
		},
		{
			name:           "Valid volume_size_used metric",
			input:          "volume_space_logical_used",
			expectedType:   LogicalSize,
			expectedExists: true,
		},
		{
			name:           "Valid volume_snapshot_reserve_used metric",
			input:          "volume_snapshot_reserve_used",
			expectedType:   SnapshotSize,
			expectedExists: true,
		},
		{
			name:           "Valid MT volume_snapshots_size_used metric",
			input:          "volume_snapshots_size_used",
			expectedType:   SnapshotSize,
			expectedExists: true,
		},
		{
			name:           "Valid volume_size_total metric",
			input:          "volume_capacity",
			expectedType:   AllocatedSize,
			expectedExists: true,
		},
		{
			name:           "Valid volume_write_ops metric",
			input:          "volume_write_ops",
			expectedType:   WriteOps,
			expectedExists: true,
		},
		{
			name:           "Valid unknown_measured_type metric",
			input:          "unknown_measured_type",
			expectedType:   UnknownMeasuredType,
			expectedExists: true,
		},
		{
			name:           "Case insensitive - uppercase input",
			input:          "POOL_ALLOCATED_SIZE",
			expectedType:   PoolAllocatedSize,
			expectedExists: true,
		},
		{
			name:           "Invalid metric type",
			input:          "invalid_metric",
			expectedType:   MeasuredType(""),
			expectedExists: false,
		},
		{
			name:           "Empty string input",
			input:          "",
			expectedType:   MeasuredType(""),
			expectedExists: false,
		},
		{
			name:           "Non-existent metric type",
			input:          "non_existent_metric_type",
			expectedType:   MeasuredType(""),
			expectedExists: false,
		},
		{
			name:           "Valid VolumeAllocatedThroughput type with proper case",
			input:          "VOLUME_ALLOCATED_THROUGHPUT",
			expectedType:   VolumeAllocatedThroughput,
			expectedExists: false, // Not in CombinedKeyResourceTypeMeasuredTypeMap currently
		},
		{
			name:           "Valid VolumeAllocatedThroughput type with lowercase",
			input:          "volume_allocated_throughput",
			expectedType:   VolumeAllocatedThroughput,
			expectedExists: false, // Not in CombinedKeyResourceTypeMeasuredTypeMap currently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, exists := NewMeasuredType(tt.input)
			assert.Equal(t, tt.expectedExists, exists)
			if tt.expectedExists {
				assert.Equal(t, tt.expectedType, result)
			} else {
				assert.Equal(t, MeasuredType(""), result)
			}
		})
	}
}

func TestCombinedKeyResourceTypeMeasuredTypeMap_Initialization(t *testing.T) {
	// Test that the map is properly initialized
	assert.NotNil(t, CombinedKeyResourceTypeMeasuredTypeMap)
	assert.NotEmpty(t, CombinedKeyResourceTypeMeasuredTypeMap)

	// Test specific mappings
	tests := []struct {
		key                  string
		expectedMeasuredType MeasuredType
		expectedResourceType ResourceType
	}{
		{
			key:                  "pool_allocated_size",
			expectedMeasuredType: PoolAllocatedSize,
			expectedResourceType: VolumePool,
		},
		{
			key:                  "volume_space_logical_used",
			expectedMeasuredType: LogicalSize,
			expectedResourceType: Volume,
		},
		{
			key:                  "volume_write_ops",
			expectedMeasuredType: WriteOps,
			expectedResourceType: Volume,
		},
		{
			key:                  "unknown_measured_type",
			expectedMeasuredType: UnknownMeasuredType,
			expectedResourceType: ResourceType(""), // Empty resource type for unknown
		},
	}

	for _, tt := range tests {
		t.Run("mapping_"+tt.key, func(t *testing.T) {
			mapping, exists := CombinedKeyResourceTypeMeasuredTypeMap[tt.key]
			assert.True(t, exists, "Key %s should exist in the map", tt.key)
			assert.Equal(t, tt.expectedMeasuredType, mapping.MeasuredType)
			assert.Equal(t, tt.expectedResourceType, mapping.ResourceType)
		})
	}
}

func TestCombinedKeyResourceTypeMeasuredType_Structure(t *testing.T) {
	// Test the structure itself
	combined := CombinedKeyResourceTypeMeasuredType{
		ResourceType: VolumePool,
		MeasuredType: PoolAllocatedSize,
	}

	assert.Equal(t, VolumePool, combined.ResourceType)
	assert.Equal(t, PoolAllocatedSize, combined.MeasuredType)
}

func TestNewMeasuredType_EdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedExists bool
	}{
		{
			name:           "Input with spaces",
			input:          " pool_allocated_size ",
			expectedExists: false, // Spaces should make it not match
		},
		{
			name:           "Input with special characters",
			input:          "pool_allocated_size!",
			expectedExists: false,
		},
		{
			name:           "Numeric input",
			input:          "123",
			expectedExists: false,
		},
		{
			name:           "Input with underscores variations",
			input:          "pool__allocated__size",
			expectedExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, exists := NewMeasuredType(tt.input)
			assert.Equal(t, tt.expectedExists, exists)
		})
	}
}

func TestMeasuredType_Constants(t *testing.T) {
	// Test that all constants are properly defined
	constants := []struct {
		name     string
		constant MeasuredType
		expected string
	}{
		{"UnknownMeasuredType", UnknownMeasuredType, "UNKNOWN_MEASURED_TYPE"},
		{"PoolAllocatedSize", PoolAllocatedSize, "POOL_ALLOCATED_SIZE"},
		{"AllocatedUsed", AllocatedUsed, "ALLOCATED_USED"},
		{"LogicalSize", LogicalSize, "LOGICAL_SIZE"},
		{"SnapshotSize", SnapshotSize, "SNAPSHOT_SIZE"},
		{"AllocatedSize", AllocatedSize, "ALLOCATED_SIZE"},
		{"TotalLogicalSize", TotalLogicalSize, "TOTAL_LOGICAL_SIZE"},
	}

	for _, c := range constants {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.expected, string(c.constant))
		})
	}
}

// Test for VolumeAllocatedThroughput metric type
func TestMeasuredType_VolumeAllocatedThroughput(t *testing.T) {
	tests := []struct {
		name     string
		mt       MeasuredType
		expected string
	}{
		{
			name:     "VolumeAllocatedThroughput string conversion",
			mt:       VolumeAllocatedThroughput,
			expected: "VOLUME_ALLOCATED_THROUGHPUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.mt.String()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test for NewMeasuredType with VolumeAllocatedThroughput
func TestNewMeasuredType_VolumeAllocatedThroughput(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedType   MeasuredType
		expectedExists bool
	}{
		{
			name:           "Valid VolumeAllocatedThroughput type with proper case",
			input:          "VOLUME_ALLOCATED_THROUGHPUT",
			expectedType:   VolumeAllocatedThroughput,
			expectedExists: false, // Not in CombinedKeyResourceTypeMeasuredTypeMap currently
		},
		{
			name:           "Valid VolumeAllocatedThroughput type with lowercase",
			input:          "volume_allocated_throughput",
			expectedType:   VolumeAllocatedThroughput,
			expectedExists: false, // Not in CombinedKeyResourceTypeMeasuredTypeMap currently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, exists := NewMeasuredType(tt.input)
			assert.Equal(t, tt.expectedExists, exists)
			if tt.expectedExists {
				assert.Equal(t, tt.expectedType, result)
			}
		})
	}
}

func TestIsValidMeasuredTypeValue(t *testing.T) {
	t.Run("known values", func(t *testing.T) {
		assert.True(t, IsValidMeasuredTypeValue(string(AllocatedSize)))
		assert.True(t, IsValidMeasuredTypeValue(string(BackupLogicalSize)))
		assert.True(t, IsValidMeasuredTypeValue(string(CbsCrossRegionVolumeRestoreTransferBytes)))
	})

	t.Run("invalid values", func(t *testing.T) {
		assert.False(t, IsValidMeasuredTypeValue(""))
		assert.False(t, IsValidMeasuredTypeValue("NOT_A_REAL_TYPE"))
		assert.False(t, IsValidMeasuredTypeValue(string(UnknownMeasuredType)))
	})
}

func TestMeasuredTypeValueHelpers(t *testing.T) {
	assert.Equal(t, "ALLOCATED_SIZE", MeasuredTypeValue(AllocatedSize))
	assert.Equal(t, AllocatedSize, MeasuredTypeFromValue("ALLOCATED_SIZE"))
	assert.True(t, MeasuredTypeEquals("ALLOCATED_SIZE", AllocatedSize))
}

func TestValidateMeasuredTypeValue(t *testing.T) {
	t.Run("valid value", func(t *testing.T) {
		err := ValidateMeasuredTypeValue(string(AllocatedSize))
		assert.NoError(t, err)
	})

	t.Run("invalid value", func(t *testing.T) {
		err := ValidateMeasuredTypeValue("bad-measured-type")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid measured_type")
	})
}

// TestAllMeasuredTypeConstsArePersisted guards against enum drift: every
// MeasuredType constant (except UnknownMeasuredType) must appear in
// allPersistedMeasuredTypeValues(), otherwise ValidateMeasuredTypeValue will
// reject it at persist time.
func TestAllMeasuredTypeConstsArePersisted(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "MetricType.go", nil, 0)
	require.NoError(t, err)

	declared := map[string]MeasuredType{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		id, ok := vs.Type.(*ast.Ident)
		if !ok || id.Name != "MeasuredType" {
			return true
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue // e.g. `var result MeasuredType` with no assigned value
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok {
				continue
			}
			val, uErr := strconv.Unquote(lit.Value)
			require.NoError(t, uErr)
			declared[name.Name] = MeasuredType(val)
		}
		return true
	})
	require.NotEmpty(t, declared, "expected to parse MeasuredType constants")

	persisted := map[MeasuredType]struct{}{}
	for _, mt := range allPersistedMeasuredTypeValues() {
		persisted[mt] = struct{}{}
	}

	for name, val := range declared {
		if name == "UnknownMeasuredType" {
			continue // intentionally excluded
		}
		_, ok := persisted[val]
		assert.Truef(t, ok, "const %s (%q) missing from allPersistedMeasuredTypeValues()", name, val)
	}
}

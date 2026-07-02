package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
)

func TestNewGoogleMetric(t *testing.T) {
	record := "test record"
	gm := NewGoogleMetric(record)

	assert.NotNil(t, gm)
	assert.Equal(t, record, gm.Record)
}

func TestNewInvalidGoogleMetricException(t *testing.T) {
	msg := "test error message"
	err := NewInvalidGoogleMetricException(msg)

	assert.NotNil(t, err)
	assert.Equal(t, msg, err.Error())
}

func TestGoogleMetric_GetType(t *testing.T) {
	tests := []struct {
		name     string
		record   interface{}
		expected GoogleType
	}{
		{
			name:     "BillingMetric type",
			record:   &datamodel.AggregatedUsage{},
			expected: BillingMetric,
		},
		{
			name:     "HydratedMetric type",
			record:   &entity.HydratedMetric{},
			expected: HydratedMetric,
		},
		{
			name:     "Unknown type",
			record:   "invalid record",
			expected: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result := gm.GetType()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetAsUsageBillingMetric(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
	}{
		{
			name:      "Valid AggregatedUsage",
			record:    &datamodel.AggregatedUsage{ID: 1},
			expectErr: false,
		},
		{
			name:      "Nil record",
			record:    nil,
			expectErr: true,
		},
		{
			name:      "Invalid type conversion",
			record:    &entity.HydratedMetric{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetAsUsageBillingMetric()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, int64(1), result.ID)
			}
		})
	}
}

func TestGoogleMetric_GetAsHydratedMetric(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
	}{
		{
			name:      "Valid HydratedMetric",
			record:    &entity.HydratedMetric{CorrelationID: "test"},
			expectErr: false,
		},
		{
			name:      "Nil record",
			record:    nil,
			expectErr: true,
		},
		{
			name:      "Invalid type conversion",
			record:    &datamodel.AggregatedUsage{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetAsHydratedMetric()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "test", result.CorrelationID)
			}
		})
	}
}

func TestGoogleMetric_GetCustomerId(t *testing.T) {
	customerID := "test-customer-123"
	accountName := "test-account"

	tests := []struct {
		name        string
		record      interface{}
		expected    string
		expectErr   bool
		expectPanic bool
	}{
		{
			name: "BillingMetric with valid customer ID",
			record: &datamodel.AggregatedUsage{
				VendorCustomerID: &customerID,
			},
			expected:  customerID,
			expectErr: false,
		},
		{
			name: "HydratedMetric with valid account name",
			record: &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					AccountName: &accountName,
				},
			},
			expected:  accountName,
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  "",
			expectErr: true,
		},
		{
			name: "BillingMetric with nil customer ID - should return error",
			record: &datamodel.AggregatedUsage{
				VendorCustomerID: nil,
			},
			expected:    "",
			expectErr:   true,
			expectPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)

			if tt.expectPanic {
				assert.Panics(t, func() {
					_, _ = gm.GetCustomerId()
				})
				return
			}

			result, err := gm.GetCustomerId()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGoogleMetric_GetCustomerId_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "BillingMetric with GetAsUsageBillingMetric error",
			record:    "invalid record", // This will cause an error in GetAsUsageBillingMetric
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
		{
			name:      "HydratedMetric with GetAsHydratedMetric error",
			record:    "invalid record", // This will cause an error in GetAsHydratedMetric
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetCustomerId()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetResourceName(t *testing.T) {
	resourceDisplayName := "test-resource"

	tests := []struct {
		name      string
		record    interface{}
		expected  string
		expectErr bool
	}{
		{
			name:      "BillingMetric with nil ResourceName returns error",
			record:    &datamodel.AggregatedUsage{},
			expected:  "",
			expectErr: true,
		},
		{
			name: "HydratedMetric with valid resource display name",
			record: &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					ResourceDisplayName: &resourceDisplayName,
				},
			},
			expected:  resourceDisplayName,
			expectErr: false,
		},
		{
			name: "HydratedMetric with nil resource display name",
			record: &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					ResourceDisplayName: nil,
				},
			},
			expected:  "",
			expectErr: true,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetResourceName()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetResourceName_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "BillingMetric with GetAsUsageBillingMetric error",
			record:    "invalid record", // This will cause an error in GetAsUsageBillingMetric
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
		{
			name:      "HydratedMetric with GetAsHydratedMetric error",
			record:    "invalid record", // This will cause an error in GetAsHydratedMetric
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetResourceName()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetMeasuredType(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expected  metadata.MeasuredType
		expectErr bool
	}{
		{
			name: "BillingMetric with measured type",
			record: &datamodel.AggregatedUsage{
				MeasuredType: string(metadata.LogicalSize),
			},
			expected:  metadata.LogicalSize,
			expectErr: false,
		},
		{
			name: "HydratedMetric with measured type",
			record: &entity.HydratedMetric{
				MeasuredType: metadata.AllocatedSize,
			},
			expected:  metadata.AllocatedSize,
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetMeasuredType()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetMeasuredType_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetMeasuredType()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetQuantity(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expected  int64
		expectErr bool
	}{
		{
			name: "BillingMetric with quantity",
			record: &datamodel.AggregatedUsage{
				Quantity: 123.45,
			},
			expected:  123,
			expectErr: false,
		},
		{
			name: "HydratedMetric with quantity",
			record: &entity.HydratedMetric{
				Quantity: 456.78,
			},
			expected:  456,
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  0,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetQuantity()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetQuantity_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetQuantity()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Equal(t, int64(0), result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetDoubleQuantity(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expected  float64
		expectErr bool
	}{
		{
			name: "HydratedMetric with double quantity",
			record: &entity.HydratedMetric{
				Quantity: 456.78,
			},
			expected:  456.78,
			expectErr: false,
		},
		{
			name:      "BillingMetric should return error",
			record:    &datamodel.AggregatedUsage{},
			expected:  0,
			expectErr: true,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  0,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetDoubleQuantity()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetDoubleQuantity_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Only hydrated metrics have a double-valued quantity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetDoubleQuantity()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Equal(t, float64(0), result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetStringQuantity(t *testing.T) {
	// This method always returns an error according to the implementation
	gm := NewGoogleMetric(&datamodel.AggregatedUsage{})
	result, err := gm.GetStringQuantity()

	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "Invalid GoogleMetric type")
}

func TestGoogleMetric_GetResourceType(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expected  metadata.ResourceType
		expectErr bool
	}{
		{
			name: "BillingMetric with resource type",
			record: &datamodel.AggregatedUsage{
				ResourceType: string(metadata.Volume),
			},
			expected:  metadata.Volume,
			expectErr: false,
		},
		{
			name: "HydratedMetric with resource type",
			record: &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					ResourceType: metadata.VolumePool,
				},
			},
			expected:  metadata.VolumePool,
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetResourceType()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetResourceType_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetResourceType()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetTags(t *testing.T) {
	billingLabels := `{"tag1":"value1","tag2":"value2"}`

	tests := []struct {
		name      string
		record    interface{}
		expected  string
		expectErr bool
	}{
		{
			name: "BillingMetric with billing labels",
			record: &datamodel.AggregatedUsage{
				BillingLabels: &billingLabels,
			},
			expected:  billingLabels,
			expectErr: false,
		},
		{
			name: "BillingMetric with nil billing labels",
			record: &datamodel.AggregatedUsage{
				BillingLabels: nil,
			},
			expected:  "",
			expectErr: false,
		},
		{
			name:      "HydratedMetric returns empty string",
			record:    &entity.HydratedMetric{},
			expected:  "",
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetTags()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "Only billing metrics have tag information")
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetTags_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Only billing metrics have tag information",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetTags()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Equal(t, "", result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetStartTime(t *testing.T) {
	startTime := time.Now()
	unixNano := entity.UnixNano(startTime.UnixNano())

	tests := []struct {
		name      string
		record    interface{}
		expected  int64
		expectErr bool
	}{
		{
			name: "BillingMetric with start time",
			record: &datamodel.AggregatedUsage{
				AggregationStart: startTime,
			},
			expected:  startTime.Unix(),
			expectErr: false,
		},
		{
			name: "HydratedMetric with timestamp",
			record: &entity.HydratedMetric{
				Timestamp: unixNano,
			},
			expected:  startTime.Unix(),
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  0,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetStartTime()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetStartTime_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetStartTime()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Equal(t, int64(0), result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetEndTime(t *testing.T) {
	endTime := time.Now()
	unixNano := entity.UnixNano(endTime.UnixNano())

	tests := []struct {
		name      string
		record    interface{}
		expected  int64
		expectErr bool
	}{
		{
			name: "BillingMetric with end time",
			record: &datamodel.AggregatedUsage{
				AggregationEnd: endTime,
			},
			expected:  endTime.Unix(),
			expectErr: false,
		},
		{
			name: "HydratedMetric with timestamp",
			record: &entity.HydratedMetric{
				Timestamp: unixNano,
			},
			expected:  endTime.Unix(),
			expectErr: false,
		},
		{
			name:      "Invalid type",
			record:    "invalid",
			expected:  0,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetEndTime()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetEndTime_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetEndTime()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Equal(t, int64(0), result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_Validate(t *testing.T) {
	customerID := "test-customer-123"
	accountName := "test-account"

	tests := []struct {
		name            string
		record          interface{}
		expectedMissing []string
	}{
		{
			name: "Valid BillingMetric",
			record: &datamodel.AggregatedUsage{
				VendorCustomerID: &customerID,
				MeasuredType:     string(metadata.LogicalSize),
				Quantity:         100.0,
			},
			expectedMissing: []string{},
		},
		{
			name: "Valid HydratedMetric",
			record: &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					AccountName: &accountName,
				},
				MeasuredType: metadata.LogicalSize,
				Quantity:     100.0,
			},
			expectedMissing: []string{},
		},
		{
			name: "BillingMetric missing measured type",
			record: &datamodel.AggregatedUsage{
				VendorCustomerID: &customerID,
				MeasuredType:     "",
				Quantity:         100.0,
			},
			expectedMissing: []string{"measuredType"},
		},
		{
			name:            "Invalid type",
			record:          "invalid",
			expectedMissing: []string{"customerId", "measuredType", "quantity", "type"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result := gm.Validate()

			assert.Equal(t, len(tt.expectedMissing), len(result))
			for _, missing := range tt.expectedMissing {
				assert.Contains(t, result, missing)
			}
		})
	}
}

// TestGetRegion_BillingMetric tests GetRegion for BillingMetric
func TestGetRegion_BillingMetric(t *testing.T) {
	region := "us-east-1"
	billingMetric := &datamodel.AggregatedUsage{RegionName: &region}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetRegion()
	assert.NoError(t, err)
	assert.Equal(t, region, result)
}

// TestGetRegion_BillingMetric_NilRegion tests GetRegion for BillingMetric with nil region
func TestGetRegion_BillingMetric_NilRegion(t *testing.T) {
	billingMetric := &datamodel.AggregatedUsage{RegionName: nil}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetRegion()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetRegion_BillingMetric_Error tests GetRegion for BillingMetric with error
func TestGetRegion_BillingMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetRegion()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// TestGetRegion_HydratedMetric tests GetRegion for HydratedMetric
func TestGetRegion_HydratedMetric(t *testing.T) {
	region := "us-west-2"
	hydratedMetric := &entity.HydratedMetric{Metadata: metadata.ResourceMetadata{RegionName: &region}}
	googleMetric := NewGoogleMetric(hydratedMetric)

	result, err := googleMetric.GetRegion()
	assert.NoError(t, err)
	assert.Equal(t, region, result)
}

// TestGetRegion_HydratedMetric_NilRegion tests GetRegion for HydratedMetric with nil region
func TestGetRegion_HydratedMetric_NilRegion(t *testing.T) {
	hydratedMetric := &entity.HydratedMetric{Metadata: metadata.ResourceMetadata{RegionName: nil}}
	googleMetric := NewGoogleMetric(hydratedMetric)

	result, err := googleMetric.GetRegion()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetRegion_HydratedMetric_Error tests GetRegion for HydratedMetric with error
func TestGetRegion_HydratedMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetRegion()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// TestGetRegion_InvalidType tests GetRegion for invalid metric type
func TestGetRegion_InvalidType(t *testing.T) {
	googleMetric := &GoogleMetric{Record: "invalid"}

	result, err := googleMetric.GetRegion()
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "Invalid GoogleMetric type")
}

// TestGetResourceUUID_BillingMetric tests GetResourceUUID for BillingMetric
func TestGetResourceUUID_BillingMetric(t *testing.T) {
	resourceUUID := "resource-uuid-123"
	billingMetric := &datamodel.AggregatedUsage{ResourceUUID: resourceUUID}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetResourceUUID()
	assert.NoError(t, err)
	assert.Equal(t, resourceUUID, result)
}

// TestGetResourceUUID_BillingMetric_EmptyUUID tests GetResourceUUID for BillingMetric with empty UUID
func TestGetResourceUUID_BillingMetric_EmptyUUID(t *testing.T) {
	billingMetric := &datamodel.AggregatedUsage{ResourceUUID: ""}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetResourceUUID()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetResourceUUID_BillingMetric_Error tests GetResourceUUID for BillingMetric with error
func TestGetResourceUUID_BillingMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetResourceUUID()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// TestGetResourceUUID_HydratedMetric tests GetResourceUUID for HydratedMetric
func TestGetResourceUUID_HydratedMetric(t *testing.T) {
	resourceUUID := "resource-uuid-456"
	hydratedMetric := &entity.HydratedMetric{Metadata: metadata.ResourceMetadata{ResourceUUID: &resourceUUID}}
	googleMetric := NewGoogleMetric(hydratedMetric)

	result, err := googleMetric.GetResourceUUID()
	assert.NoError(t, err)
	assert.Equal(t, resourceUUID, result)
}

// TestGetResourceUUID_HydratedMetric_NilUUID tests GetResourceUUID for HydratedMetric with nil UUID
func TestGetResourceUUID_HydratedMetric_NilUUID(t *testing.T) {
	hydratedMetric := &entity.HydratedMetric{Metadata: metadata.ResourceMetadata{ResourceUUID: nil}}
	googleMetric := NewGoogleMetric(hydratedMetric)

	result, err := googleMetric.GetResourceUUID()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetResourceUUID_HydratedMetric_Error tests GetResourceUUID for HydratedMetric with error
func TestGetResourceUUID_HydratedMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetResourceUUID()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// TestGetResourceUUID_InvalidType tests GetResourceUUID for invalid metric type
func TestGetResourceUUID_InvalidType(t *testing.T) {
	googleMetric := &GoogleMetric{Record: "invalid"}

	result, err := googleMetric.GetResourceUUID()
	assert.Error(t, err)
	assert.Empty(t, result)
	assert.Contains(t, err.Error(), "Invalid GoogleMetric type")
}

// TestGetLabels_BillingMetric tests GetLabels for BillingMetric
func TestGetLabels_BillingMetric(t *testing.T) {
	labels := `{"env":"prod","team":"backend"}`
	billingMetric := &datamodel.AggregatedUsage{BillingLabels: &labels}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetLabels()
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "prod", result["env"])
	assert.Equal(t, "backend", result["team"])
}

// TestGetLabels_BillingMetric_NilLabels tests GetLabels for BillingMetric with nil labels
func TestGetLabels_BillingMetric_NilLabels(t *testing.T) {
	billingMetric := &datamodel.AggregatedUsage{BillingLabels: nil}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetLabels()
	assert.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetLabels_BillingMetric_InvalidJSON tests GetLabels for BillingMetric with invalid JSON
func TestGetLabels_BillingMetric_InvalidJSON(t *testing.T) {
	invalidJSON := `{"invalid": json}`
	billingMetric := &datamodel.AggregatedUsage{BillingLabels: &invalidJSON}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetLabels()
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestGetLabels_BillingMetric_Error tests GetLabels for BillingMetric with error
func TestGetLabels_BillingMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetLabels()
	assert.Error(t, err)
	assert.Nil(t, result)
}

// TestGetLabels_HydratedMetric tests GetLabels for HydratedMetric
func TestGetLabels_HydratedMetric(t *testing.T) {
	googleMetric := NewGoogleMetric(&entity.HydratedMetric{})

	result, err := googleMetric.GetLabels()
	assert.NoError(t, err)
	assert.Nil(t, result)
}

// TestGetLabels_InvalidType tests GetLabels for invalid metric type
func TestGetLabels_InvalidType(t *testing.T) {
	googleMetric := &GoogleMetric{Record: "invalid"}

	result, err := googleMetric.GetLabels()
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Invalid GoogleMetric type")
}

func TestGoogleMetric_GetServiceLevel_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "HydratedMetric type not supported",
			record:    &entity.HydratedMetric{},
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetServiceLevel()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetServiceLevel_WithBillingMetricFields(t *testing.T) {
	tests := []struct {
		name         string
		serviceLevel string
		expected     string
	}{
		{
			name:         "Service level 1",
			serviceLevel: "1",
			expected:     "1",
		},
		{
			name:         "Service level 2",
			serviceLevel: "2",
			expected:     "2",
		},
		{
			name:         "Service level 3",
			serviceLevel: "3",
			expected:     "3",
		},
		{
			name:         "Empty service level",
			serviceLevel: "",
			expected:     "",
		},
		{
			name:         "Alphanumeric service level",
			serviceLevel: "level-1",
			expected:     "level-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &datamodel.AggregatedUsage{
				ServiceLevel: tt.serviceLevel,
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetServiceLevel()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetReplicationType_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "HydratedMetric type not supported",
			record:    &entity.HydratedMetric{},
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetReplicationType()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetReplicationType_WithBillingMetricFields(t *testing.T) {
	tests := []struct {
		name            string
		replicationType string
		expected        string
	}{
		{
			name:            "Cross region replication",
			replicationType: "CROSS_REGION_REPLICATION",
			expected:        "CROSS_REGION_REPLICATION",
		},
		{
			name:            "Hybrid replication",
			replicationType: "HYBRID_REPLICATION",
			expected:        "HYBRID_REPLICATION",
		},
		{
			name:            "Empty replication type",
			replicationType: "",
			expected:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &datamodel.AggregatedUsage{
				ReplicationType: tt.replicationType,
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetReplicationType()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetSourceRegion_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "HydratedMetric type not supported",
			record:    &entity.HydratedMetric{},
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetSourceRegion()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetSourceRegion_WithBillingMetricFields(t *testing.T) {
	srcRegion1 := "us-east1"
	srcRegion2 := "us-west1"
	tests := []struct {
		name         string
		sourceRegion *string
		expected     string
	}{
		{
			name:         "Valid source region",
			sourceRegion: &srcRegion1,
			expected:     "us-east1",
		},
		{
			name:         "Different source region",
			sourceRegion: &srcRegion2,
			expected:     "us-west1",
		},
		{
			name:         "Nil source region",
			sourceRegion: nil,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &datamodel.AggregatedUsage{
				SourceRegion: tt.sourceRegion,
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetSourceRegion()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetDestinationRegion_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "HydratedMetric type not supported",
			record:    &entity.HydratedMetric{},
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetDestinationRegion()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetDestinationRegion_WithBillingMetricFields(t *testing.T) {
	dstRegion1 := "us-east1"
	dstRegion2 := "us-west1"
	tests := []struct {
		name              string
		destinationRegion *string
		expected          string
	}{
		{
			name:              "Valid source region",
			destinationRegion: &dstRegion1,
			expected:          "us-east1",
		},
		{
			name:              "Different source region",
			destinationRegion: &dstRegion2,
			expected:          "us-west1",
		},
		{
			name:              "Nil source region",
			destinationRegion: nil,
			expected:          "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &datamodel.AggregatedUsage{
				DestinationRegion: tt.destinationRegion,
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetDestinationRegion()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetResourceUUID_ErrorCases(t *testing.T) {
	tests := []struct {
		name      string
		record    interface{}
		expectErr bool
		errMsg    string
	}{
		{
			name:      "Invalid record type",
			record:    "invalid record",
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
		{
			name:      "Nil record",
			record:    nil,
			expectErr: true,
			errMsg:    "Invalid GoogleMetric type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gm := NewGoogleMetric(tt.record)
			result, err := gm.GetResourceUUID()

			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Empty(t, result)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGoogleMetric_GetResourceUUID_WithBillingMetricFields(t *testing.T) {
	tests := []struct {
		name         string
		resourceUUID string
		expected     string
	}{
		{
			name:         "Valid resource UUID",
			resourceUUID: "123e4567-e89b-12d3-a456-426614174000",
			expected:     "123e4567-e89b-12d3-a456-426614174000",
		},
		{
			name:         "Different resource UUID",
			resourceUUID: "987fcdeb-51a2-43d1-b789-123456789abc",
			expected:     "987fcdeb-51a2-43d1-b789-123456789abc",
		},
		{
			name:         "Empty resource UUID",
			resourceUUID: "",
			expected:     "",
		},
		{
			name:         "Short UUID",
			resourceUUID: "abc123",
			expected:     "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &datamodel.AggregatedUsage{
				ResourceUUID: tt.resourceUUID,
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetResourceUUID()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGoogleMetric_GetResourceUUID_WithHydratedMetricFields(t *testing.T) {
	tests := []struct {
		name         string
		resourceUUID *string
		expected     string
	}{
		{
			name:         "Valid resource UUID",
			resourceUUID: stringPtr("123e4567-e89b-12d3-a456-426614174000"),
			expected:     "123e4567-e89b-12d3-a456-426614174000",
		},
		{
			name:         "Different resource UUID",
			resourceUUID: stringPtr("987fcdeb-51a2-43d1-b789-123456789abc"),
			expected:     "987fcdeb-51a2-43d1-b789-123456789abc",
		},
		{
			name:         "Nil resource UUID",
			resourceUUID: nil,
			expected:     "",
		},
		{
			name:         "Empty resource UUID",
			resourceUUID: stringPtr(""),
			expected:     "",
		},
		{
			name:         "Short UUID",
			resourceUUID: stringPtr("abc123"),
			expected:     "abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &entity.HydratedMetric{
				Metadata: metadata.ResourceMetadata{
					ResourceUUID: tt.resourceUUID,
				},
			}
			gm := NewGoogleMetric(record)
			result, err := gm.GetResourceUUID()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetZone_BillingMetric_WithZone tests GetZone for BillingMetric with zone set
func TestGetZone_BillingMetric_WithZone(t *testing.T) {
	zone := "us-central1-a"
	billingMetric := &datamodel.AggregatedUsage{Zone: &zone}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetZone()
	assert.NoError(t, err)
	assert.Equal(t, "us-central1-a", result)
}

// TestGetZone_BillingMetric_NilZone tests GetZone for BillingMetric with nil zone
func TestGetZone_BillingMetric_NilZone(t *testing.T) {
	billingMetric := &datamodel.AggregatedUsage{Zone: nil}
	googleMetric := NewGoogleMetric(billingMetric)

	result, err := googleMetric.GetZone()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetZone_BillingMetric_Error tests GetZone for BillingMetric with nil record
func TestGetZone_BillingMetric_Error(t *testing.T) {
	googleMetric := NewGoogleMetric(nil)

	result, err := googleMetric.GetZone()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// TestGetZone_HydratedMetric tests GetZone for HydratedMetric returns empty
func TestGetZone_HydratedMetric(t *testing.T) {
	hydratedMetric := &entity.HydratedMetric{Metadata: metadata.ResourceMetadata{}}
	googleMetric := NewGoogleMetric(hydratedMetric)

	result, err := googleMetric.GetZone()
	assert.NoError(t, err)
	assert.Empty(t, result)
}

// TestGetZone_InvalidMetric tests GetZone for invalid metric type
func TestGetZone_InvalidMetric(t *testing.T) {
	googleMetric := NewGoogleMetric("invalid")

	result, err := googleMetric.GetZone()
	assert.Error(t, err)
	assert.Empty(t, result)
}

// Helper function to create string pointers for testing
func stringPtr(s string) *string {
	return &s
}

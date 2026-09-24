package collector

import (
	"testing"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/stretchr/testify/assert"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/performance"
)

func TestNewGoogleProvider_ReturnsGoogleVolumeMetricsProvider(t *testing.T) {
	mockTenantProvider := new(MockTenantProjectProvider)
	mockClient := new(MockMonitoringClient)
	sink := &performance.GoogleSink{}

	testMetrics := []common.MetricItem{
		{
			Metric:       "volume_read_ops",
			ResourceType: "netapp_volume",
		},
	}

	provider := NewGoogleProvider(mockTenantProvider, mockClient, testMetrics, sink)

	gProvider, ok := provider.(*GoogleVolumeMetricsProvider)
	assert.True(t, ok, "Expected type *GoogleVolumeMetricsProvider")
	assert.NotNil(t, gProvider, "GoogleVolumeMetricsProvider should not be nil")
	assert.Equal(t, mockTenantProvider, gProvider.tenantProjectProvider)
	assert.Equal(t, mockClient, gProvider.client)
}

func TestNewGoogleVolumeMetricsProvider_InitializesFields(t *testing.T) {
	mockTenantProvider := new(MockTenantProjectProvider)
	mockClient := new(MockMonitoringClient)
	sink := &performance.GoogleSink{}
	testMetrics := []common.MetricItem{
		{
			Metric:       "volume_read_ops",
			ResourceType: "netapp_volume",
		},
	}
	provider := NewGoogleVolumeMetricsProvider(mockTenantProvider, mockClient, testMetrics, sink)

	assert.Equal(t, mockTenantProvider, provider.tenantProjectProvider)
	assert.Equal(t, mockClient, provider.client)
}
func TestMetricClientWrapper_IsNil(t *testing.T) {
	// Case 1: client is nil
	wrapperNil := &MetricClientWrapper{Client: nil}
	if !wrapperNil.IsNil() {
		t.Errorf("Expected IsNil() to return true when client is nil")
	}

	// Case 2: client is not nil
	mockClient := new(monitoring.MetricClient)
	wrapperNotNil := &MetricClientWrapper{Client: mockClient}
	if wrapperNotNil.IsNil() {
		t.Errorf("Expected IsNil() to return false when client is not nil")
	}
}

func TestNewMetricClientWrapperReturnsWrapperWithClient(t *testing.T) {
	mockClient := new(monitoring.MetricClient)
	wrapper := NewMetricClientWrapper(mockClient)
	assert.Equal(t, mockClient, wrapper.Client)
}

func TestNewGoogleTenantProjectProviderSetsDatastore(t *testing.T) {
	mockStore := new(database.MockStorage)
	provider := NewGoogleTenantProjectProvider(mockStore)
	assert.Equal(t, mockStore, provider.vcpDatastore)
}

func TestNewGoogleVolumeMetricsProviderInitializesFields(t *testing.T) {
	mockTenantProvider := new(MockTenantProjectProvider)
	mockClient := new(MockMonitoringClient)
	sink := &performance.GoogleSink{}
	testMetrics := []common.MetricItem{
		{
			Metric:       "volume_read_ops",
			ResourceType: "netapp_volume",
		},
	}
	provider := NewGoogleVolumeMetricsProvider(mockTenantProvider, mockClient, testMetrics, sink)
	assert.Equal(t, mockTenantProvider, provider.tenantProjectProvider)
	assert.Equal(t, mockClient, provider.client)
}

func TestNewGoogleProviderReturnsGoogleVolumeMetricsProvider(t *testing.T) {
	mockTenantProvider := new(MockTenantProjectProvider)
	mockClient := new(MockMonitoringClient)
	sink := &performance.GoogleSink{}
	testMetrics := []common.MetricItem{
		{
			Metric:       "volume_read_ops",
			ResourceType: "netapp_volume",
		},
	}
	provider := NewGoogleProvider(mockTenantProvider, mockClient, testMetrics, sink)
	assert.IsType(t, &GoogleVolumeMetricsProvider{}, provider)
}

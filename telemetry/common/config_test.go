package common

import (
	"testing"
	"time"
)

func TestLoadsConfigWithDefaultValues(t *testing.T) {
	config := LoadConfig()

	if config.PerformanceRootUrl != "https://servicecontrol.googleapis.com" {
		t.Fatalf("Expected PerformanceRootUrl to mirror default root, got %s", config.PerformanceRootUrl)
	}
	if config.UsageRootUrl != "https://servicecontrol.googleapis.com" {
		t.Fatalf("Expected UsageRootUrl to mirror default root, got %s", config.UsageRootUrl)
	}
	if config.OperationBatchSize != 200 {
		t.Fatalf("Expected OperationBatchSize to be 200, got %d", config.OperationBatchSize)
	}
	if config.PusherServiceName != "autopush-netapp.sandbox.googleapis.com" {
		t.Fatalf("Expected PusherServiceName to be 'autopush-netapp.sandbox.googleapis.com', got %s", config.PusherServiceName)
	}
	if config.PusherServiceProject != "netapp-au-se1-autopush-sde-tst" {
		t.Fatalf("Expected PusherServiceProject to be 'netapp-au-se1-autopush-sde-tst', got %s", config.PusherServiceProject)
	}
}

func TestLoadsConfigWithCustomEnvironmentValues(t *testing.T) {
	t.Setenv("ROOT_URL", "https://custom-root.example.com")
	t.Setenv("PERFORMANCE_ROOT_URL", "https://perf-root.example.com")
	t.Setenv("USAGE_ROOT_URL", "https://usage-root.example.com")

	config := LoadConfig()

	if config.OperationBatchSize != 200 {
		t.Fatalf("Expected OperationBatchSize to be 200, got %d", config.OperationBatchSize)
	}
	if config.PusherServiceName != "autopush-netapp.sandbox.googleapis.com" {
		t.Fatalf("Expected PusherServiceName to be 'autopush-netapp.sandbox.googleapis.com', got %s", config.PusherServiceName)
	}
	if config.PusherServiceProject != "netapp-au-se1-autopush-sde-tst" {
		t.Fatalf("Expected PusherServiceProject to be 'netapp-au-se1-autopush-sde-tst', got %s", config.PusherServiceProject)
	}

	if config.PerformanceRootUrl != "https://perf-root.example.com" {
		t.Fatalf("Expected PerformanceRootUrl to be 'https://perf-root.example.com', got %s", config.PerformanceRootUrl)
	}
	if config.UsageRootUrl != "https://usage-root.example.com" {
		t.Fatalf("Expected UsageRootUrl to be 'https://usage-root.example.com', got %s", config.UsageRootUrl)
	}
}

func TestHandlesMissingEnvironmentVariablesGracefully(t *testing.T) {
	config := LoadConfig()

	if config.PerformanceRootUrl != "https://servicecontrol.googleapis.com" {
		t.Fatalf("Expected PerformanceRootUrl to default to 'https://servicecontrol.googleapis.com', got %s", config.PerformanceRootUrl)
	}
	if config.UsageRootUrl != "https://servicecontrol.googleapis.com" {
		t.Fatalf("Expected UsageRootUrl to default to 'https://servicecontrol.googleapis.com', got %s", config.UsageRootUrl)
	}
	if config.OperationBatchSize != 200 {
		t.Fatalf("Expected OperationBatchSize to default to 200, got %d", config.OperationBatchSize)
	}
	if config.PusherServiceName != "autopush-netapp.sandbox.googleapis.com" {
		t.Fatalf("Expected PusherServiceName to default to 'autopush-netapp.sandbox.googleapis.com', got %s", config.PusherServiceName)
	}
	if config.PusherServiceProject != "netapp-au-se1-autopush-sde-tst" {
		t.Fatalf("Expected PusherServiceProject to default to 'netapp-au-se1-autopush-sde-tst', got %s", config.PusherServiceProject)
	}
}
func TestRegionNameDefaultValue(t *testing.T) {
	t.Setenv("LOCAL_REGION", "")
	config := LoadConfig()

	if config.RegionName != "" {
		t.Fatalf("Expected RegionName to default to empty string, got %s", config.RegionName)
	}
}

func TestRegionNameWithEnvironmentVariable(t *testing.T) {
	// Set environment variable for this test
	t.Setenv("LOCAL_REGION", "us-west-1")

	config := LoadConfig()

	if config.RegionName != "us-west-1" {
		t.Fatalf("Expected RegionName to be 'us-west-1', got %s", config.RegionName)
	}
}

func TestEnableLargeVolumesBillingDefaultValue(t *testing.T) {
	config := LoadConfig()

	// Default should be false (billing disabled for Large Volumes until GA)
	if config.EnableLargeVolumesBilling {
		t.Fatalf("Expected EnableLargeVolumesBilling to default to false, got %v", config.EnableLargeVolumesBilling)
	}
}

func TestEnableLargeVolumesBillingWithEnvironmentVariable(t *testing.T) {
	// Test setting to true (enable billing for Large Volumes when GA)
	t.Setenv("ENABLE_LARGE_VOLUMES_BILLING", "true")

	config := LoadConfig()

	if !config.EnableLargeVolumesBilling {
		t.Fatalf("Expected EnableLargeVolumesBilling to be true when env var is 'true', got %v", config.EnableLargeVolumesBilling)
	}
}

func TestEnableTenantQuotaProjectDefaultValue(t *testing.T) {
	config := LoadConfig()

	if !config.EnableTenantQuotaProject {
		t.Fatalf("Expected EnableTenantQuotaProject to default to true, got %v", config.EnableTenantQuotaProject)
	}
}

func TestEnableTenantQuotaProjectWithEnvironmentVariable(t *testing.T) {
	t.Setenv("ENABLE_TENANT_QUOTA_PROJECT", "false")

	config := LoadConfig()

	if config.EnableTenantQuotaProject {
		t.Fatalf("Expected EnableTenantQuotaProject to be false when env var is 'false', got %v", config.EnableTenantQuotaProject)
	}
}

func TestEnableBackupVaultMetricsDefaultValue(t *testing.T) {
	config := LoadConfig()

	if config.EnableBackupVaultMetrics != false {
		t.Fatalf("Expected EnableBackupVaultMetrics to default to false, got %v", config.EnableBackupVaultMetrics)
	}
}

func TestEnableBackupVaultMetricsWithEnvironmentVariable(t *testing.T) {
	t.Setenv("ENABLE_BACKUP_VAULT_METRICS", "true")

	config := LoadConfig()

	if config.EnableBackupVaultMetrics != true {
		t.Fatalf("Expected EnableBackupVaultMetrics to be true, got %v", config.EnableBackupVaultMetrics)
	}
}

func TestEnableBackupHistoryFormatterDefaultValue(t *testing.T) {
	config := LoadConfig()

	if config.EnableBackupHistoryFormatter {
		t.Fatalf("Expected EnableBackupHistoryFormatter to default to false, got %v", config.EnableBackupHistoryFormatter)
	}
}

func TestEnableBackupHistoryFormatterWithEnvironmentVariable(t *testing.T) {
	t.Setenv("ENABLE_BACKUP_HISTORY_FORMATTER", "true")

	config := LoadConfig()

	if !config.EnableBackupHistoryFormatter {
		t.Fatalf("Expected EnableBackupHistoryFormatter to be true, got %v", config.EnableBackupHistoryFormatter)
	}
}

func TestEnableGcbdrBmfBillingDefaultValue(t *testing.T) {
	config := LoadConfig()

	if config.EnableGcbdrBmfBilling {
		t.Fatalf("Expected EnableGcbdrBmfBilling to default to false, got %v", config.EnableGcbdrBmfBilling)
	}
}

func TestEnableGcbdrBmfBillingWithEnvironmentVariable(t *testing.T) {
	t.Setenv("ENABLE_GCBDR_BMF_BILLING", "true")

	config := LoadConfig()

	if !config.EnableGcbdrBmfBilling {
		t.Fatalf("Expected EnableGcbdrBmfBilling to be true when env var is 'true', got %v", config.EnableGcbdrBmfBilling)
	}
}

func TestEnableCrossProjectBackupUsedMetricDefaultValue(t *testing.T) {
	config := LoadConfig()

	if config.EnableCrossProjectBackupUsedMetric {
		t.Fatalf("Expected EnableCrossProjectBackupUsedMetric to default to false, got %v", config.EnableCrossProjectBackupUsedMetric)
	}
}

func TestEnableCrossProjectBackupUsedMetricWithEnvironmentVariable(t *testing.T) {
	t.Setenv("ENABLE_CROSS_PROJECT_BACKUP_USED_METRIC", "true")

	config := LoadConfig()

	if !config.EnableCrossProjectBackupUsedMetric {
		t.Fatalf("Expected EnableCrossProjectBackupUsedMetric to be true when env var is 'true', got %v", config.EnableCrossProjectBackupUsedMetric)
	}
}

func TestShutdownConfigDefaults(t *testing.T) {
	config := LoadConfig()
	if config.ShutdownBudget != DefaultShutdownBudget {
		t.Fatalf("expected shutdown budget %s, got %s", DefaultShutdownBudget, config.ShutdownBudget)
	}
	if config.HTTPShutdownTimeout != DefaultHTTPShutdownTimeout {
		t.Fatalf("expected http shutdown timeout %s, got %s", DefaultHTTPShutdownTimeout, config.HTTPShutdownTimeout)
	}
}

func TestShutdownConfigOverrides(t *testing.T) {
	t.Setenv("TELEMETRY_SHUTDOWN_BUDGET", "15s")
	t.Setenv("TELEMETRY_HTTP_SHUTDOWN_TIMEOUT", "4s")

	config := LoadConfig()
	if config.ShutdownBudget != 15*time.Second {
		t.Fatalf("expected shutdown budget 15s, got %s", config.ShutdownBudget)
	}
	if config.HTTPShutdownTimeout != 4*time.Second {
		t.Fatalf("expected http shutdown timeout 4s, got %s", config.HTTPShutdownTimeout)
	}
}

func TestShutdownConfigNonPositiveDefaults(t *testing.T) {
	t.Setenv("TELEMETRY_SHUTDOWN_BUDGET", "0")
	t.Setenv("TELEMETRY_HTTP_SHUTDOWN_TIMEOUT", "-1s")

	config := LoadConfig()
	if config.ShutdownBudget != DefaultShutdownBudget {
		t.Fatalf("expected default shutdown budget %s, got %s", DefaultShutdownBudget, config.ShutdownBudget)
	}
	if config.HTTPShutdownTimeout != DefaultHTTPShutdownTimeout {
		t.Fatalf("expected default http shutdown timeout %s, got %s", DefaultHTTPShutdownTimeout, config.HTTPShutdownTimeout)
	}
}

func TestLoadMetricsConfigFromBytesReturnsValidConfig(t *testing.T) {
	config := LoadMetricsConfigFromBytes()

	if config == nil {
		t.Fatalf("Expected config to not be nil")
	}
	if config.VolumeMetrics == nil {
		t.Fatalf("Expected VolumeMetrics to not be nil")
	}
}

func TestLoadMetricsConfigFromBytesContainsExpectedMetrics(t *testing.T) {
	config := LoadMetricsConfigFromBytes()

	if len(config.VolumeMetrics) == 0 {
		t.Fatalf("Expected VolumeMetrics to contain at least one metric")
	}

	for i, metric := range config.VolumeMetrics {
		if metric.Metric == "" {
			t.Fatalf("Expected metric at index %d to have non-empty Metric field", i)
		}
		if metric.ResourceType == "" {
			t.Fatalf("Expected metric at index %d to have non-empty ResourceType field", i)
		}
	}
}

func TestLoadMetricsConfigFromBytesHandlesValidYamlStructure(t *testing.T) {
	config := LoadMetricsConfigFromBytes()

	foundValidMetric := false
	for _, metric := range config.VolumeMetrics {
		if metric.Metric != "" && metric.ResourceType != "" {
			foundValidMetric = true
			break
		}
	}

	if !foundValidMetric {
		t.Fatalf("Expected at least one metric with both Metric and ResourceType fields populated")
	}
}

func TestLoadMetricsConfigFromBytesWithEmptyYamlReturnsEmptyConfig(t *testing.T) {
	originalMetricListYAML := metricListYAML
	defer func() {
		metricListYAML = originalMetricListYAML
	}()

	metricListYAML = []byte("")

	config := LoadMetricsConfigFromBytes()

	if config == nil {
		t.Fatalf("Expected config to not be nil")
	}
	if len(config.VolumeMetrics) != 0 {
		t.Fatalf("Expected VolumeMetrics to be empty, got %d metrics", len(config.VolumeMetrics))
	}
}

func TestLoadMetricsConfig_MTSnapshotIsFlagGated(t *testing.T) {
	const snapshotMetric = "volume_snapshots_size_used"

	for _, metric := range LoadMetricsConfig(false).VolumeMetrics {
		if metric.Metric == snapshotMetric {
			t.Fatalf("MT snapshot metric must not be present when disabled")
		}
	}

	var matches int
	for _, metric := range LoadMetricsConfig(true).VolumeMetrics {
		if metric.Metric == snapshotMetric {
			matches++
			if metric.MetricType != "usage" {
				t.Fatalf("expected MT snapshot metric to be usage, got %q", metric.MetricType)
			}
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly one MT snapshot metric, got %d", matches)
	}
}

func TestLoadConfig_MTHarvestVolumeMetricsDefaultAndOverride(t *testing.T) {
	t.Setenv("ENABLE_MT_HARVEST_VOLUME_METRICS", "")
	if !LoadConfig().EnableMTHarvestVolumeMetrics {
		t.Fatal("expected MT Harvest volume metrics to default to true")
	}

	t.Setenv("ENABLE_MT_HARVEST_VOLUME_METRICS", "false")
	if LoadConfig().EnableMTHarvestVolumeMetrics {
		t.Fatal("expected MT Harvest volume metrics to honor the environment")
	}

	t.Setenv("ENABLE_MT_HARVEST_VOLUME_METRICS", "true")
	if !LoadConfig().EnableMTHarvestVolumeMetrics {
		t.Fatal("expected MT Harvest volume metrics to honor the environment")
	}
}

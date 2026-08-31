package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	prometheus_model "github.com/prometheus/client_model/go"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// Helper: common test volumes
func getTestVolumes() []*datamodel.Volume {
	return []*datamodel.Volume{
		{Name: "volume1", State: "active", AccountID: 123, Account: &datamodel.Account{Name: "account1"}, AutoTieringEnabled: true, LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{LargeCapacity: true}, SnapshotPolicy: &datamodel.SnapshotPolicy{IsEnabled: true}},
		{Name: "volume2", State: "inactive", AccountID: 456, Account: &datamodel.Account{Name: "account2"}, AutoTieringEnabled: true, LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{LargeCapacity: true}},
		{Name: "volume3", State: "active", AccountID: 789, Account: nil, LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{LargeCapacity: true}, SnapshotPolicy: &datamodel.SnapshotPolicy{IsEnabled: true}},
		{Name: "volE", State: "READY"},
		{Name: "volF", State: "READY"},
	}
}

// Helper: expected keys for each metric type
func getExpectedKeys(metric string) []string {
	switch metric {
	case "autotier":
		return []string{"volume1_active_account1", "volume2_inactive_account2"}
	case "crr":
		return []string{"volume1_active_account1", "volume3_active_"}
	case "largevolume":
		return []string{"volume1_active_account1", "volume2_inactive_account2", "volume3_active_"}
	case "cbs":
		return []string{}
	case "eligibility":
		return []string{"volE_READY", "volF_READY"}
	default:
		return nil
	}
}

// Helper: check metric labels
func metricHasLabels(mLabels []*prometheus_model.LabelPair, expected map[string]string) bool {
	for k, v := range expected {
		found := false
		for _, label := range mLabels {
			if *label.Name == k && *label.Value == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestIncJobStatusCounter(t *testing.T) {
	ctx := context.Background()
	IncJobStatusCounter(ctx, "errorDetails", "state")
	metric, err := JobStatusCounter.GetMetricWithLabelValues("test_project_id", "errorDetails", "state")
	if err != nil {
		t.Errorf("Failed to get metric: %v", err)
	}
	if metric == nil {
		t.Error("Metric not found")
	}
}

func TestEmitAutoTierEnabledMetric(t *testing.T) {
	RegisterAutoTierEnabledGauge()
	volumes := getTestVolumes()
	EmitAutoTierEnabledMetric(volumes)
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}
	expectedKeys := getExpectedKeys("autotier")
	for _, key := range expectedKeys {
		found := false
		for _, mf := range metrics {
			if *mf.Name == "gcnv_vsa_autotier_volume" {
				for _, m := range mf.Metric {
					expectedLabelValues := strings.Split(key, "_")
					expected := map[string]string{
						"name":         expectedLabelValues[0],
						"state":        expectedLabelValues[1],
						"account_name": expectedLabelValues[2],
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("AutoTierEnabledMetric gauge not found for key %s", key)
		}
	}
}

func TestEmitCRREnabledMetric(t *testing.T) {
	RegisterCRREnabledGauge()
	volumes := getTestVolumes()
	EmitCRREnabledMetric(volumes)
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}
	expectedKeys := getExpectedKeys("crr")
	for _, key := range expectedKeys {
		found := false
		for _, mf := range metrics {
			if *mf.Name == "gcnv_vsa_crr_volume" {
				for _, m := range mf.Metric {
					expectedLabelValues := strings.Split(key, "_")
					expected := map[string]string{
						"name":         expectedLabelValues[0],
						"state":        expectedLabelValues[1],
						"account_name": expectedLabelValues[2],
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("CRREnabledMetric gauge not found for key %s", key)
		}
	}
}

func TestEmitLargeVolumeEnabledMetric(t *testing.T) {
	RegisterLargeVolumeEnabledGauge()
	volumes := getTestVolumes()
	EmitLargeVolumeEnabledMetric(volumes)
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}
	expectedKeys := getExpectedKeys("largevolume")
	for _, key := range expectedKeys {
		found := false
		for _, mf := range metrics {
			if *mf.Name == "gcnv_vsa_large_volume" {
				for _, m := range mf.Metric {
					expectedLabelValues := strings.Split(key, "_")
					expected := map[string]string{
						"name":         expectedLabelValues[0],
						"state":        expectedLabelValues[1],
						"account_name": expectedLabelValues[2],
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("LargeVolumeEnabledMetric gauge not found for key %s", key)
		}
	}
}

func TestEmitEligibilityStringMetric(t *testing.T) {
	RegisterEligibilityStringGauge()
	volumes := getTestVolumes()
	EmitEligibilityStringMetric(volumes, nil)
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}
	expectedKeys := getExpectedKeys("eligibility")
	for _, key := range expectedKeys {
		found := false
		for _, mf := range metrics {
			if *mf.Name == "gcnv_volumes_eligibility" {
				for _, m := range mf.Metric {
					expectedLabelValues := strings.Split(key, "_")
					expected := map[string]string{
						"name":       expectedLabelValues[0],
						"state":      expectedLabelValues[1],
						"created_by": "vcp",
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("EligibilityStringMetric gauge not found for key %s", key)
		}
	}
}

func TestEmitEligibilityStringMetric_ExpertModeVolumes(t *testing.T) {
	prometheus.Unregister(eligibilityStringGauge)
	eligibilityStringGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gcnv_volumes_eligibility",
			Help: "Total number of volumes for eligibility string",
		},
		[]string{"name", "state", "created_by"},
	)
	RegisterEligibilityStringGauge()

	expertVols := []*datamodel.ExpertModeVolumes{
		{Name: "expert-vol1", State: "AVAILABLE"},
		{Name: "expert-vol2", State: "AVAILABLE"},
	}
	EmitEligibilityStringMetric(nil, expertVols)

	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	for _, ev := range expertVols {
		found := false
		for _, mf := range gathered {
			if *mf.Name == "gcnv_volumes_eligibility" {
				for _, m := range mf.Metric {
					expected := map[string]string{
						"name":       ev.Name,
						"state":      ev.State,
						"created_by": "ontap-proxy",
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("EligibilityStringMetric gauge not found for expert mode volume %s", ev.Name)
		}
	}
}

func TestEmitEligibilityStringMetric_MixedVolumes(t *testing.T) {
	prometheus.Unregister(eligibilityStringGauge)
	eligibilityStringGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gcnv_volumes_eligibility",
			Help: "Total number of volumes for eligibility string",
		},
		[]string{"name", "state", "created_by"},
	)
	RegisterEligibilityStringGauge()

	vcpVols := []*datamodel.Volume{
		{Name: "vcp-vol1", State: "READY"},
	}
	expertVols := []*datamodel.ExpertModeVolumes{
		{Name: "expert-vol1", State: "AVAILABLE"},
	}
	EmitEligibilityStringMetric(vcpVols, expertVols)

	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	expectedLabels := []map[string]string{
		{"name": "vcp-vol1", "state": "READY", "created_by": "vcp"},
		{"name": "expert-vol1", "state": "AVAILABLE", "created_by": "ontap-proxy"},
	}

	for _, expected := range expectedLabels {
		found := false
		for _, mf := range gathered {
			if *mf.Name == "gcnv_volumes_eligibility" {
				for _, m := range mf.Metric {
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("EligibilityStringMetric gauge not found for labels %v", expected)
		}
	}
}

func TestEmitCBSEnabledMetric(t *testing.T) {
	RegisterCBSEnabledGauge()
	volumes := getTestVolumes()
	EmitCBSEnabledMetric(volumes)
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
	}
	// You need to define expectedKeys for CBS, similar to getExpectedKeys("crr")
	expectedKeys := getExpectedKeys("cbs")
	for _, key := range expectedKeys {
		found := false
		for _, mf := range metrics {
			if *mf.Name == "gcnv_vsa_cbs_volume" {
				for _, m := range mf.Metric {
					expectedLabelValues := strings.Split(key, "_")
					expected := map[string]string{
						"name":         expectedLabelValues[0],
						"state":        expectedLabelValues[1],
						"account_name": expectedLabelValues[2],
					}
					if metricHasLabels(m.Label, expected) {
						found = true
						break
					}
				}
			}
		}
		if !found {
			t.Errorf("CBSEnabledMetric gauge not found for key %s", key)
		}
	}
}

func TestRegisterAutoTierEnabledGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(autoTierEnabledGauge)

	// Should register without error
	RegisterAutoTierEnabledGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterAutoTierEnabledGauge()

	// Check that the collector is still a GaugeVec
	if autoTierEnabledGauge == nil {
		t.Error("autoTierEnabledGauge is nil after registration")
	}
}

func TestRegisterLargeVolumeEnabledGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(largeVolumeEnabledGauge)

	// Should register without error
	RegisterLargeVolumeEnabledGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterLargeVolumeEnabledGauge()

	// Check that the collector is still a GaugeVec
	if largeVolumeEnabledGauge == nil {
		t.Error("largeVolumeEnabledGauge is nil after registration")
	}
}

func TestRegisterCRREnabledGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(crrEnabledGauge)

	// Should register without error
	RegisterCRREnabledGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterCRREnabledGauge()

	// Check that the collector is still a GaugeVec
	if crrEnabledGauge == nil {
		t.Error("crrEnabledGauge is nil after registration")
	}
}

func TestRegisterCBSEnabledGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(cbsEnabledGauge)

	// Should register without error
	RegisterCBSEnabledGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterCBSEnabledGauge()

	// Check that the collector is still a GaugeVec
	if cbsEnabledGauge == nil {
		t.Error("cbsEnabledGauge is nil after registration")
	}
}

func TestRegisterEligibilityStringGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(eligibilityStringGauge)

	// Should register without error
	RegisterEligibilityStringGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterEligibilityStringGauge()

	// Check that the collector is still a GaugeVec
	if eligibilityStringGauge == nil {
		t.Error("EligibilityString is nil after registration")
	}
}

func TestRegisterBackupSizeGauge(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(backupSizeGauge)

	// Should register without error
	RegisterBackupSizeGauge()
	// Register again to trigger AlreadyRegisteredError
	RegisterBackupSizeGauge()

	// Check that the collector is still a GaugeVec
	if backupSizeGauge == nil {
		t.Error("backupSizeGauge is nil after registration")
	}
}

func TestRegisterCertificateRotationFailureCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(CertificateRotationFailureCounter)

	// Should register without error
	RegisterCertificateRotationFailureCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterCertificateRotationFailureCounter()

	// Check that the collector is still a CounterVec
	if CertificateRotationFailureCounter == nil {
		t.Error("CertificateRotationFailureCounter is nil after registration")
	}
}

func TestRegisterOciCertificateRotationSuccessCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(OciCertificateRotationSuccessCounter)

	// Should register without error
	RegisterOciCertificateRotationSuccessCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterOciCertificateRotationSuccessCounter()

	// Check that the collector is still a CounterVec
	if OciCertificateRotationSuccessCounter == nil {
		t.Error("OciCertificateRotationSuccessCounter is nil after registration")
	}
}

func TestRegisterOciCertificateRotationFailureCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(OciCertificateRotationFailureCounter)

	// Should register without error
	RegisterOciCertificateRotationFailureCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterOciCertificateRotationFailureCounter()

	// Check that the collector is still a CounterVec
	if OciCertificateRotationFailureCounter == nil {
		t.Error("OciCertificateRotationFailureCounter is nil after registration")
	}
}

func TestRegisterPasswordRotationFailureCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(PasswordRotationFailureCounter)

	// Should register without error
	RegisterPasswordRotationFailureCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterPasswordRotationFailureCounter()

	// Check that the collector is still a CounterVec
	if PasswordRotationFailureCounter == nil {
		t.Error("PasswordRotationFailureCounter is nil after registration")
	}
}

func TestEmitCertificateRotationFailure(t *testing.T) {
	RegisterCertificateRotationFailureCounter()

	poolUUID := "test-pool-uuid-123"
	poolName := "test-pool-name"
	failureType := "certificate_rotation"
	errorType := "connection timeout"

	// Emit the metric
	EmitCertificateRotationFailure(poolUUID, poolName, failureType, errorType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_certificate_rotation_failures_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"pool_uuid":    poolUUID,
					"pool_name":    poolName,
					"failure_type": failureType,
					"error_type":   errorType,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					// Verify counter value is incremented
					if m.Counter != nil && *m.Counter.Value >= 1.0 {
						break
					}
				}
			}
		}
	}

	if !found {
		t.Error("CertificateRotationFailureCounter metric not found with expected labels")
	}
}

func TestEmitOciCertificateRotationSuccess(t *testing.T) {
	// Unregister first to ensure clean state, and clean up after to avoid
	// polluting the shared default registry for other tests.
	prometheus.Unregister(OciCertificateRotationSuccessCounter)
	RegisterOciCertificateRotationSuccessCounter()
	defer prometheus.Unregister(OciCertificateRotationSuccessCounter)

	poolUUID := "test-pool-uuid-success-123"
	poolExternalIdentifier := "ocid1.pool.oc1..success"
	certType := "server"

	// Emit the metric
	EmitOciCertificateRotationSuccess(poolUUID, poolExternalIdentifier, certType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_oci_certificate_rotation_successes_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"pool_uuid":                poolUUID,
					"pool_external_identifier": poolExternalIdentifier,
					"cert_type":                certType,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					// Verify counter value is incremented
					if m.Counter != nil && *m.Counter.Value >= 1.0 {
						break
					}
				}
			}
		}
	}

	if !found {
		t.Error("OciCertificateRotationSuccessCounter metric not found with expected labels")
	}
}

func TestEmitOciCertificateRotationFailure(t *testing.T) {
	// Unregister first to ensure clean state, and clean up after to avoid
	// polluting the shared default registry for other tests.
	prometheus.Unregister(OciCertificateRotationFailureCounter)
	RegisterOciCertificateRotationFailureCounter()
	defer prometheus.Unregister(OciCertificateRotationFailureCounter)

	poolUUID := "test-pool-uuid-failure-123"
	poolExternalIdentifier := "ocid1.pool.oc1..failure"
	failureType := "oci_certificate_rotation"
	errorType := "staging"

	// Emit the metric
	EmitOciCertificateRotationFailure(poolUUID, poolExternalIdentifier, failureType, errorType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_oci_certificate_rotation_failures_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"pool_uuid":                poolUUID,
					"pool_external_identifier": poolExternalIdentifier,
					"failure_type":             failureType,
					"error_type":               errorType,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					// Verify counter value is incremented
					if m.Counter != nil && *m.Counter.Value >= 1.0 {
						break
					}
				}
			}
		}
	}

	if !found {
		t.Error("OciCertificateRotationFailureCounter metric not found with expected labels")
	}
}

func TestEmitCertificateRotationFailure_TruncatesLongErrorType(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(CertificateRotationFailureCounter)
	RegisterCertificateRotationFailureCounter()

	poolUUID := "test-pool-uuid-456"
	poolName := "test-pool-name-2"
	failureType := "certificate_rotation"
	// Create a very long error type (> 200 chars)
	longErrorType := strings.Repeat("a", 250)

	// Emit the metric
	EmitCertificateRotationFailure(poolUUID, poolName, failureType, longErrorType)

	// Gather metrics and verify truncation
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	expectedErrorType := strings.Repeat("a", 200) + "..."
	for _, mf := range metrics {
		if *mf.Name == "vcp_certificate_rotation_failures_total" {
			for _, m := range mf.Metric {
				// Check that this metric has the expected labels
				expectedLabels := map[string]string{
					"pool_uuid":    poolUUID,
					"pool_name":    poolName,
					"failure_type": failureType,
					"error_type":   expectedErrorType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					// Verify the error_type is truncated correctly
					for _, label := range m.Label {
						if *label.Name == "error_type" {
							errorTypeValue := *label.Value
							if errorTypeValue != expectedErrorType {
								t.Errorf("Expected error_type to be %q (length %d), got %q (length %d)",
									expectedErrorType, len(expectedErrorType), errorTypeValue, len(errorTypeValue))
							}
							if !strings.HasSuffix(errorTypeValue, "...") {
								t.Error("Long error type should be truncated with '...' suffix")
							}
							break
						}
					}
					break
				}
			}
		}
	}

	if !found {
		t.Error("CertificateRotationFailureCounter metric not found with expected labels")
	}
}

func TestEmitPasswordRotationFailure(t *testing.T) {
	RegisterPasswordRotationFailureCounter()

	poolUUID := "test-pool-uuid-789"
	poolName := "test-pool-name-3"
	failureType := "password_rotation"
	errorType := "authentication failed"

	// Emit the metric
	EmitPasswordRotationFailure(poolUUID, poolName, failureType, errorType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_password_rotation_failures_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"pool_uuid":    poolUUID,
					"pool_name":    poolName,
					"failure_type": failureType,
					"error_type":   errorType,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					// Verify counter value is incremented
					if m.Counter != nil && *m.Counter.Value >= 1.0 {
						break
					}
				}
			}
		}
	}

	if !found {
		t.Error("PasswordRotationFailureCounter metric not found with expected labels")
	}
}

func TestEmitPasswordRotationFailure_TruncatesLongErrorType(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(PasswordRotationFailureCounter)
	RegisterPasswordRotationFailureCounter()

	poolUUID := "test-pool-uuid-101"
	poolName := "test-pool-name-4"
	failureType := "password_rotation"
	// Create a very long error type (> 200 chars)
	longErrorType := strings.Repeat("b", 300)

	// Emit the metric
	EmitPasswordRotationFailure(poolUUID, poolName, failureType, longErrorType)

	// Gather metrics and verify truncation
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	expectedErrorType := strings.Repeat("b", 200) + "..."
	for _, mf := range metrics {
		if *mf.Name == "vcp_password_rotation_failures_total" {
			for _, m := range mf.Metric {
				// Check that this metric has the expected labels
				expectedLabels := map[string]string{
					"pool_uuid":    poolUUID,
					"pool_name":    poolName,
					"failure_type": failureType,
					"error_type":   expectedErrorType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					// Verify the error_type is truncated correctly
					for _, label := range m.Label {
						if *label.Name == "error_type" {
							errorTypeValue := *label.Value
							if errorTypeValue != expectedErrorType {
								t.Errorf("Expected error_type to be %q (length %d), got %q (length %d)",
									expectedErrorType, len(expectedErrorType), errorTypeValue, len(errorTypeValue))
							}
							if !strings.HasSuffix(errorTypeValue, "...") {
								t.Error("Long error type should be truncated with '...' suffix")
							}
							break
						}
					}
					break
				}
			}
		}
	}

	if !found {
		t.Error("PasswordRotationFailureCounter metric not found with expected labels")
	}
}

func TestRegisterKmsKeyLimitReachedCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(KmsKeyLimitReachedCounter)

	// Should register without error
	RegisterKmsKeyLimitReachedCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterKmsKeyLimitReachedCounter()

	// Check that the collector is still a CounterVec
	if KmsKeyLimitReachedCounter == nil {
		t.Error("KmsKeyLimitReachedCounter is nil after registration")
	}
}

func TestRegisterKmsRotationFailureCounter(t *testing.T) {
	// Unregister first to ensure clean state
	prometheus.Unregister(KmsRotationFailureCounter)

	// Should register without error
	RegisterKmsRotationFailureCounter()
	// Register again to trigger AlreadyRegisteredError
	RegisterKmsRotationFailureCounter()

	// Check that the collector is still a CounterVec
	if KmsRotationFailureCounter == nil {
		t.Error("KmsRotationFailureCounter is nil after registration")
	}
}

func TestEmitKmsKeyLimitReached(t *testing.T) {
	RegisterKmsKeyLimitReachedCounter()

	kmsConfigUUID := "test-kms-config-uuid-123"
	limitType := "pending_deletion"

	// Emit the metric
	EmitKmsKeyLimitReached(kmsConfigUUID, limitType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_kms_key_rotation_key_limit_reached_total" {
			for _, m := range mf.Metric {
				expectedLabels := map[string]string{
					"kms_config_uuid": kmsConfigUUID,
					"limit_type":      limitType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Error("KmsKeyLimitReachedCounter metric not found with expected labels")
	}
}

func TestEmitKmsKeyLimitReached_TotalKeysLimitType(t *testing.T) {
	prometheus.Unregister(KmsKeyLimitReachedCounter)
	RegisterKmsKeyLimitReachedCounter()

	kmsConfigUUID := "test-kms-config-uuid-456"
	limitType := "total_keys"

	// Emit the metric
	EmitKmsKeyLimitReached(kmsConfigUUID, limitType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_kms_key_rotation_key_limit_reached_total" {
			for _, m := range mf.Metric {
				expectedLabels := map[string]string{
					"kms_config_uuid": kmsConfigUUID,
					"limit_type":      limitType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Error("KmsKeyLimitReachedCounter metric not found with total_keys limit type")
	}
}

func TestEmitKmsRotationFailure(t *testing.T) {
	RegisterKmsRotationFailureCounter()

	kmsConfigUUID := "test-kms-config-uuid-789"
	serviceAccountEmail := "test-sa@project.iam.gserviceaccount.com"
	failureType := "pool_migration"

	// Emit the metric
	EmitKmsRotationFailure(kmsConfigUUID, serviceAccountEmail, failureType)

	// Gather metrics and verify
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_kms_key_rotation_failure_total" {
			for _, m := range mf.Metric {
				expectedLabels := map[string]string{
					"kms_config_uuid":       kmsConfigUUID,
					"service_account_email": serviceAccountEmail,
					"failure_type":          failureType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					break
				}
			}
		}
	}

	if !found {
		t.Error("KmsRotationFailureCounter metric not found with expected labels")
	}
}

func TestEmitKmsRotationFailure_MultipleCalls(t *testing.T) {
	prometheus.Unregister(KmsRotationFailureCounter)
	RegisterKmsRotationFailureCounter()

	kmsConfigUUID := "test-kms-config-uuid-multiple"
	serviceAccountEmail := "test-multiple-sa@project.iam.gserviceaccount.com"
	failureType := "pool_migration"

	// Emit the metric multiple times
	EmitKmsRotationFailure(kmsConfigUUID, serviceAccountEmail, failureType)
	EmitKmsRotationFailure(kmsConfigUUID, serviceAccountEmail, failureType)
	EmitKmsRotationFailure(kmsConfigUUID, serviceAccountEmail, failureType)

	// Gather metrics and verify counter value is 3
	metrics, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metrics {
		if *mf.Name == "vcp_kms_key_rotation_failure_total" {
			for _, m := range mf.Metric {
				expectedLabels := map[string]string{
					"kms_config_uuid":       kmsConfigUUID,
					"service_account_email": serviceAccountEmail,
					"failure_type":          failureType,
				}
				if metricHasLabels(m.Label, expectedLabels) {
					found = true
					if m.Counter.GetValue() != 3 {
						t.Errorf("Expected counter value to be 3, got %v", m.Counter.GetValue())
					}
					break
				}
			}
		}
	}

	if !found {
		t.Error("KmsRotationFailureCounter metric not found after multiple calls")
	}
}

func TestRegisterZoneSwitchWorkflowCounter(t *testing.T) {
	prometheus.Unregister(ZoneSwitchWorkflowCounter)

	RegisterZoneSwitchWorkflowCounter()
	RegisterZoneSwitchWorkflowCounter()

	if ZoneSwitchWorkflowCounter == nil {
		t.Error("ZoneSwitchWorkflowCounter is nil after registration")
	}
}

func TestEmitZoneSwitchWorkflowMetric(t *testing.T) {
	RegisterZoneSwitchWorkflowCounter()

	poolUUID := "test-pool-uuid-zone"
	poolName := "test-pool-name-zone"
	action := "switch"
	status := "success"
	failureStep := "none"

	EmitZoneSwitchWorkflowMetric(poolUUID, poolName, action, status, failureStep)

	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Errorf("Failed to gather metrics: %v", err)
		return
	}

	found := false
	for _, mf := range metricsCollected {
		if *mf.Name == "vcp_zone_switch_workflow_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"pool_uuid":    poolUUID,
					"pool_name":    poolName,
					"action":       action,
					"status":       status,
					"failure_step": failureStep,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					if m.Counter != nil && *m.Counter.Value >= 1.0 {
						break
					}
				}
			}
		}
	}

	if !found {
		t.Error("ZoneSwitchWorkflowCounter metric not found with expected labels")
	}
}

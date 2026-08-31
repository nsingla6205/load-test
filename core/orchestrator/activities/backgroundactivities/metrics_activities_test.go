package backgroundactivities

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	prometheus_model "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
)

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

func TestEmitCertificateRotationFailureMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterCertificateRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.CertificateRotationFailureCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-cert"
	poolName := "test-pool-name-cert"
	failureType := "certificate_rotation"
	errorType := "test error for certificate rotation"

	// Execute the activity
	err := EmitCertificateRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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

	assert.True(t, found, "CertificateRotationFailureCounter metric should be emitted with correct labels")
}

func TestEmitCertificateRotationFailureMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterCertificateRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.CertificateRotationFailureCounter)

	ctx := context.Background()
	poolUUID := ""
	poolName := ""
	failureType := ""
	errorType := ""

	// Execute the activity - should not panic
	err := EmitCertificateRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitOciCertificateRotationSuccessMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterOciCertificateRotationSuccessCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.OciCertificateRotationSuccessCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-cert-success"
	poolExternalIdentifier := "ocid1.pool.oc1..cert-success"
	certType := "server"

	// Execute the activity
	err := EmitOciCertificateRotationSuccessMetric(ctx, poolUUID, poolExternalIdentifier, certType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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

	assert.True(t, found, "OciCertificateRotationSuccessCounter metric should be emitted with correct labels")
}

func TestEmitOciCertificateRotationSuccessMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterOciCertificateRotationSuccessCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.OciCertificateRotationSuccessCounter)

	ctx := context.Background()

	// Execute the activity - should not panic
	err := EmitOciCertificateRotationSuccessMetric(ctx, "", "", "")

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitOciCertificateRotationFailureMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterOciCertificateRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.OciCertificateRotationFailureCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-cert-failure"
	poolExternalIdentifier := "ocid1.pool.oc1..cert-failure"
	failureType := "oci_certificate_rotation"
	errorType := "staging"

	// Execute the activity
	err := EmitOciCertificateRotationFailureMetric(ctx, poolUUID, poolExternalIdentifier, failureType, errorType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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

	assert.True(t, found, "OciCertificateRotationFailureCounter metric should be emitted with correct labels")
}

func TestEmitOciCertificateRotationFailureMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterOciCertificateRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.OciCertificateRotationFailureCounter)

	ctx := context.Background()

	// Execute the activity - should not panic
	err := EmitOciCertificateRotationFailureMetric(ctx, "", "", "", "")

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitPasswordRotationFailureMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterPasswordRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.PasswordRotationFailureCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-pwd"
	poolName := "test-pool-name-pwd"
	failureType := "password_rotation"
	errorType := "test error for password rotation"

	// Execute the activity
	err := EmitPasswordRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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

	assert.True(t, found, "PasswordRotationFailureCounter metric should be emitted with correct labels")
}

func TestEmitPasswordRotationFailureMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterPasswordRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.PasswordRotationFailureCounter)

	ctx := context.Background()
	poolUUID := ""
	poolName := ""
	failureType := ""
	errorType := ""

	// Execute the activity - should not panic
	err := EmitPasswordRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitCertificateRotationFailureMetric_MultipleCalls(t *testing.T) {
	// Register the metric first
	metrics.RegisterCertificateRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.CertificateRotationFailureCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-multi"
	poolName := "test-pool-name-multi"
	failureType := "certificate_rotation"
	errorType := "test error"

	// Execute the activity multiple times
	err1 := EmitCertificateRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)
	err2 := EmitCertificateRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no errors
	assert.NoError(t, err1)
	assert.NoError(t, err2)

	// Gather metrics and verify counter is incremented
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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
					// Verify counter value is at least 2 (called twice)
					if m.Counter != nil && *m.Counter.Value >= 2.0 {
						break
					}
				}
			}
		}
	}

	assert.True(t, found, "CertificateRotationFailureCounter should be incremented multiple times")
}

func TestEmitPasswordRotationFailureMetric_MultipleCalls(t *testing.T) {
	// Register the metric first
	metrics.RegisterPasswordRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.PasswordRotationFailureCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-pwd-multi"
	poolName := "test-pool-name-pwd-multi"
	failureType := "password_rotation"
	errorType := "test error"

	// Execute the activity multiple times
	err1 := EmitPasswordRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)
	err2 := EmitPasswordRotationFailureMetric(ctx, poolUUID, poolName, failureType, errorType)

	// Verify no errors
	assert.NoError(t, err1)
	assert.NoError(t, err2)

	// Gather metrics and verify counter is incremented
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
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
					// Verify counter value is at least 2 (called twice)
					if m.Counter != nil && *m.Counter.Value >= 2.0 {
						break
					}
				}
			}
		}
	}

	assert.True(t, found, "PasswordRotationFailureCounter should be incremented multiple times")
}

func TestEmitKmsKeyLimitReachedMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterKmsKeyLimitReachedCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.KmsKeyLimitReachedCounter)

	ctx := context.Background()
	kmsConfigUUID := "test-kms-config-uuid-activity"
	limitType := "pending_deletion"

	// Execute the activity
	err := EmitKmsKeyLimitReachedMetric(ctx, kmsConfigUUID, limitType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
		if *mf.Name == "vcp_kms_key_rotation_key_limit_reached_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"kms_config_uuid": kmsConfigUUID,
					"limit_type":      limitType,
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

	assert.True(t, found, "KmsKeyLimitReachedCounter metric should be emitted with correct labels")
}

func TestEmitKmsKeyLimitReachedMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterKmsKeyLimitReachedCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.KmsKeyLimitReachedCounter)

	ctx := context.Background()
	kmsConfigUUID := ""
	limitType := ""

	// Execute the activity - should not panic
	err := EmitKmsKeyLimitReachedMetric(ctx, kmsConfigUUID, limitType)

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitKmsRotationFailureMetric(t *testing.T) {
	// Register the metric first
	metrics.RegisterKmsRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.KmsRotationFailureCounter)

	ctx := context.Background()
	kmsConfigUUID := "test-kms-config-uuid-failure"
	serviceAccountEmail := "test-sa@project.iam.gserviceaccount.com"
	failureType := "pool_migration"

	// Execute the activity
	err := EmitKmsRotationFailureMetric(ctx, kmsConfigUUID, serviceAccountEmail, failureType)

	// Verify no error
	assert.NoError(t, err)

	// Gather metrics and verify
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
		if *mf.Name == "vcp_kms_key_rotation_failure_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"kms_config_uuid":       kmsConfigUUID,
					"service_account_email": serviceAccountEmail,
					"failure_type":          failureType,
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

	assert.True(t, found, "KmsRotationFailureCounter metric should be emitted with correct labels")
}

func TestEmitKmsRotationFailureMetric_WithEmptyValues(t *testing.T) {
	// Register the metric first
	metrics.RegisterKmsRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.KmsRotationFailureCounter)

	ctx := context.Background()
	kmsConfigUUID := ""
	serviceAccountEmail := ""
	failureType := ""

	// Execute the activity - should not panic
	err := EmitKmsRotationFailureMetric(ctx, kmsConfigUUID, serviceAccountEmail, failureType)

	// Verify no error
	assert.NoError(t, err)
}

func TestEmitKmsRotationFailureMetric_MultipleCalls(t *testing.T) {
	// Register the metric first
	metrics.RegisterKmsRotationFailureCounter()
	// Clean up after test
	defer prometheus.Unregister(metrics.KmsRotationFailureCounter)

	ctx := context.Background()
	kmsConfigUUID := "test-kms-config-uuid-multi"
	serviceAccountEmail := "test-multi-sa@project.iam.gserviceaccount.com"
	failureType := "pool_migration"

	// Execute the activity multiple times
	err1 := EmitKmsRotationFailureMetric(ctx, kmsConfigUUID, serviceAccountEmail, failureType)
	err2 := EmitKmsRotationFailureMetric(ctx, kmsConfigUUID, serviceAccountEmail, failureType)
	err3 := EmitKmsRotationFailureMetric(ctx, kmsConfigUUID, serviceAccountEmail, failureType)

	// Verify no errors
	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)

	// Gather metrics and verify counter is incremented
	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

	found := false
	for _, mf := range metricsCollected {
		if *mf.Name == "vcp_kms_key_rotation_failure_total" {
			for _, m := range mf.Metric {
				expected := map[string]string{
					"kms_config_uuid":       kmsConfigUUID,
					"service_account_email": serviceAccountEmail,
					"failure_type":          failureType,
				}
				if metricHasLabels(m.Label, expected) {
					found = true
					// Verify counter value is at least 3 (called three times)
					if m.Counter != nil && *m.Counter.Value >= 3.0 {
						break
					}
				}
			}
		}
	}

	assert.True(t, found, "KmsRotationFailureCounter should be incremented multiple times")
}

func TestEmitZoneSwitchWorkflowMetric(t *testing.T) {
	metrics.RegisterZoneSwitchWorkflowCounter()
	defer prometheus.Unregister(metrics.ZoneSwitchWorkflowCounter)

	ctx := context.Background()
	poolUUID := "test-pool-uuid-zone"
	poolName := "test-pool-name-zone"
	action := "switch"
	status := "success"
	failureStep := "none"

	err := EmitZoneSwitchWorkflowMetric(ctx, poolUUID, poolName, action, status, failureStep)
	assert.NoError(t, err)

	metricsCollected, err := prometheus.DefaultGatherer.Gather()
	assert.NoError(t, err)

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

	assert.True(t, found, "ZoneSwitchWorkflowCounter metric should be emitted with correct labels")
}

func TestEmitZoneSwitchWorkflowMetric_WithEmptyValues(t *testing.T) {
	metrics.RegisterZoneSwitchWorkflowCounter()
	defer prometheus.Unregister(metrics.ZoneSwitchWorkflowCounter)

	ctx := context.Background()
	err := EmitZoneSwitchWorkflowMetric(ctx, "", "", "", "", "")
	assert.NoError(t, err)
}

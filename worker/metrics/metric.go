package metrics

import (
	"context"
	"log"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/helper"
)

const CreatedByLabelValue = "vcp"
const CreatedByOntapProxyLabelValue = "ontap-proxy"

type VolumeDetails struct {
	Name        string
	State       string
	AccountID   int64
	AccountName string
}

type BackupDetailForMetric struct {
	VolName     string
	AccountName string
	Size        int64
}

var JobStatusCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_job_status_updates",
		Help: "Total number of job status updates",
	},
	[]string{"project_id", "error_details", "state"},
)

// Counter for certificate rotation failures
var CertificateRotationFailureCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_certificate_rotation_failures_total",
		Help: "Total number of certificate rotation failures",
	},
	[]string{"pool_uuid", "pool_name", "failure_type", "error_type"},
)

// OciCertificateRotationSuccessCounter counts successful OCI certificate rotations.
// cert_type is a bounded label (server | client | service) identifying which
// certificate completed rotation. pool_external_identifier is the OCI-native pool
// identifier (OCID) and it lets OCI operators correlate the metric with their own tooling.
// pool_name is intentionally omitted (mutable/redundant); it lives in the "rotation completed" logs.
var OciCertificateRotationSuccessCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_oci_certificate_rotation_successes_total",
		Help: "Total number of successful OCI certificate rotations",
	},
	[]string{"pool_uuid", "pool_external_identifier", "cert_type"},
)

// OciCertificateRotationFailureCounter counts OCI certificate-rotation failures. It is
// kept separate from the shared CertificateRotationFailureCounter (used by GCP) so the
// OCI metric can carry pool_external_identifier without altering the GCP series.
var OciCertificateRotationFailureCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_oci_certificate_rotation_failures_total",
		Help: "Total number of OCI certificate rotation failures",
	},
	[]string{"pool_uuid", "pool_external_identifier", "failure_type", "error_type"},
)

// Counter for password rotation failures
var PasswordRotationFailureCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_password_rotation_failures_total",
		Help: "Total number of password rotation failures",
	},
	[]string{"pool_uuid", "pool_name", "failure_type", "error_type"},
)

// Counter for regional HA zone switch workflow outcomes (scraped by OTEL → Google Cloud Monitoring).
var ZoneSwitchWorkflowCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_zone_switch_workflow_total",
		Help: "Total number of zone switch workflow attempts by outcome",
	},
	[]string{"pool_uuid", "pool_name", "action", "status", "failure_step"},
)

// KmsKeyLimitReachedCounter Counter for KMS key limit reached (rotation blocked due to too many keys)
var KmsKeyLimitReachedCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_kms_key_rotation_key_limit_reached_total",
		Help: "Total times KMS key rotation was blocked due to key limit reached",
	},
	[]string{"kms_config_uuid", "limit_type"},
)

// KmsRotationFailureCounter Counter for KMS key rotation failures per KMS config
var KmsRotationFailureCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_kms_key_rotation_failure_total",
		Help: "Total KMS key rotation failures per KMS config",
	},
	[]string{"kms_config_uuid", "service_account_email", "failure_type"},
)

// TrialAccountSyncRunCounter tracks trial account sync activity runs by result.
var TrialAccountSyncRunCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_trial_account_sync_runs_total",
		Help: "Total number of trial account sync activity runs by result",
	},
	[]string{"result"},
)

// TrialAccountSyncAccountOutcomeCounter tracks per-account outcomes during trial sync.
var TrialAccountSyncAccountOutcomeCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_trial_account_sync_account_outcomes_total",
		Help: "Total number of per-account outcomes during trial account sync",
	},
	[]string{"outcome"},
)

// TrialInfoCreateOutcomeCounter tracks trial info handling outcomes in create API path.
var TrialInfoCreateOutcomeCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vcp_trial_info_create_outcomes_total",
		Help: "Total number of trial info handling outcomes from create APIs",
	},
	[]string{"outcome"},
)

// Gauge for CMEK backup rewrite errors — uses the same metric name as CBS/SDE
// (cbs_cmek_rewrite_error_gauge) so that a single alert rule covers both services.
var CmekBackupRewriteErrorGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "cbs_cmek_rewrite_error_gauge",
		Help: "Number of times CMEK object rewrite error occurred",
	},
	[]string{"error", "bucket_name", "owner_id", "backup_vault_uuid"},
)

// Gauge for AutoTier enabled
var autoTierEnabledGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_vsa_autotier_volume",
		Help: "Total number of volumes with autotier enabled",
	},
	[]string{"name", "state", "account_name"},
)

// Gauge for large volume enabled
var largeVolumeEnabledGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_vsa_large_volume",
		Help: "Total number of volumes with large volume enabled",
	},
	[]string{"name", "state", "account_name"},
)

// Gauge for CBS enabled
var cbsEnabledGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_vsa_cbs_volume",
		Help: "Total number of volumes with CBS enabled",
	},
	[]string{"name", "state", "account_name"},
)

// Gauge for CRR enabled
var crrEnabledGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_vsa_crr_volume",
		Help: "Total number of volumes with CRR enabled",
	},
	[]string{"name", "state", "account_name"},
)

// Gauge for eligibility string volumes
var eligibilityStringGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_volumes_eligibility",
		Help: "Total number of volumes for eligibility string",
	},
	[]string{"name", "state", "created_by"},
)

// Gauge for backup size
var backupSizeGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "gcnv_backup_size_bytes",
		Help: "Total size of the backups in bytes",
	},
	[]string{"name", "account_name"},
)

func IncJobStatusCounter(ctx context.Context, errorDetails, state string) {
	projectID := helper.GetProjectID(ctx)
	if len(errorDetails) > 1024 {
		errorDetails = errorDetails[:1024]
	}
	JobStatusCounter.WithLabelValues(
		projectID,
		errorDetails,
		state,
	).Inc()
}

func RegisterJobStatusCounter() {
	err := prometheus.Register(JobStatusCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			JobStatusCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register JobStatusCounter: %v", err)
		}
	}
}

func RegisterAutoTierEnabledGauge() {
	err := prometheus.Register(autoTierEnabledGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			autoTierEnabledGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register autoTierEnabledGauge: %v", err)
		}
	}
}

func RegisterLargeVolumeEnabledGauge() {
	err := prometheus.Register(largeVolumeEnabledGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			largeVolumeEnabledGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register largeVolumeEnabledGauge: %v", err)
		}
	}
}

func RegisterCRREnabledGauge() {
	err := prometheus.Register(crrEnabledGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			crrEnabledGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register crrEnabledGauge: %v", err)
		}
	}
}

func RegisterCBSEnabledGauge() {
	err := prometheus.Register(cbsEnabledGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			cbsEnabledGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register cbsEnabledGauge: %v", err)
		}
	}
}

func RegisterEligibilityStringGauge() {
	err := prometheus.Register(eligibilityStringGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			eligibilityStringGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register eligibilityStringGauge: %v", err)
		}
	}
}

func RegisterBackupSizeGauge() {
	err := prometheus.Register(backupSizeGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			backupSizeGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register backupSizeGauge: %v", err)
		}
	}
}

func RegisterCertificateRotationFailureCounter() {
	err := prometheus.Register(CertificateRotationFailureCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			CertificateRotationFailureCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register CertificateRotationFailureCounter: %v", err)
		}
	}
}

func RegisterOciCertificateRotationSuccessCounter() {
	err := prometheus.Register(OciCertificateRotationSuccessCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			OciCertificateRotationSuccessCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register OciCertificateRotationSuccessCounter: %v", err)
		}
	}
}

func RegisterOciCertificateRotationFailureCounter() {
	err := prometheus.Register(OciCertificateRotationFailureCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			OciCertificateRotationFailureCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register OciCertificateRotationFailureCounter: %v", err)
		}
	}
}

func RegisterPasswordRotationFailureCounter() {
	err := prometheus.Register(PasswordRotationFailureCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			PasswordRotationFailureCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register PasswordRotationFailureCounter: %v", err)
		}
	}
}

func RegisterZoneSwitchWorkflowCounter() {
	err := prometheus.Register(ZoneSwitchWorkflowCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			ZoneSwitchWorkflowCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register ZoneSwitchWorkflowCounter: %v", err)
		}
	}
}

// EmitCertificateRotationFailure emits a metric when certificate rotation fails
func EmitCertificateRotationFailure(poolUUID, poolName, failureType, errorType string) {
	// Truncate error type if too long to avoid label cardinality issues
	if len(errorType) > 200 {
		errorType = errorType[:200] + "..."
	}
	CertificateRotationFailureCounter.WithLabelValues(
		poolUUID,
		poolName,
		failureType,
		errorType,
	).Inc()
}

// EmitOciCertificateRotationSuccess emits a metric when an OCI certificate rotation
// completes successfully. certType is a bounded label (server | client | service)
// identifying which certificate was rotated. poolExternalIdentifier is the OCI-native
// pool identifier (OCID).
func EmitOciCertificateRotationSuccess(poolUUID, poolExternalIdentifier, certType string) {
	OciCertificateRotationSuccessCounter.WithLabelValues(
		poolUUID,
		poolExternalIdentifier,
		certType,
	).Inc()
}

// EmitOciCertificateRotationFailure emits a metric when an OCI certificate rotation
// fails. failureType should be a bounded value (e.g. "oci_certificate_rotation") and
// errorType is a bounded phase category. poolExternalIdentifier is the OCI-native pool identifier (OCID).
func EmitOciCertificateRotationFailure(poolUUID, poolExternalIdentifier, failureType, errorType string) {
	// Truncate error type if too long to avoid label cardinality issues
	if len(errorType) > 200 {
		errorType = errorType[:200] + "..."
	}
	OciCertificateRotationFailureCounter.WithLabelValues(
		poolUUID,
		poolExternalIdentifier,
		failureType,
		errorType,
	).Inc()
}

// EmitZoneSwitchWorkflowMetric records a zone switch workflow success or failure.
func EmitZoneSwitchWorkflowMetric(poolUUID, poolName, action, status, failureStep string) {
	ZoneSwitchWorkflowCounter.WithLabelValues(
		poolUUID,
		poolName,
		action,
		status,
		failureStep,
	).Inc()
}

// EmitPasswordRotationFailure emits a metric when password rotation fails
func EmitPasswordRotationFailure(poolUUID, poolName, failureType, errorType string) {
	// Truncate error type if too long to avoid label cardinality issues
	if len(errorType) > 200 {
		errorType = errorType[:200] + "..."
	}
	PasswordRotationFailureCounter.WithLabelValues(
		poolUUID,
		poolName,
		failureType,
		errorType,
	).Inc()
}

// RegisterKmsKeyLimitReachedCounter registers the KMS key limit reached counter
func RegisterKmsKeyLimitReachedCounter() {
	err := prometheus.Register(KmsKeyLimitReachedCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			KmsKeyLimitReachedCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register KmsKeyLimitReachedCounter: %v", err)
		}
	}
}

// RegisterKmsRotationFailureCounter registers the KMS rotation failure counter
func RegisterKmsRotationFailureCounter() {
	err := prometheus.Register(KmsRotationFailureCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			KmsRotationFailureCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register KmsRotationFailureCounter: %v", err)
		}
	}
}

func RegisterTrialAccountSyncRunCounter() {
	err := prometheus.Register(TrialAccountSyncRunCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			TrialAccountSyncRunCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register TrialAccountSyncRunCounter: %v", err)
		}
	}
}

func RegisterTrialAccountSyncAccountOutcomeCounter() {
	err := prometheus.Register(TrialAccountSyncAccountOutcomeCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			TrialAccountSyncAccountOutcomeCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register TrialAccountSyncAccountOutcomeCounter: %v", err)
		}
	}
}

func RegisterTrialInfoCreateOutcomeCounter() {
	err := prometheus.Register(TrialInfoCreateOutcomeCounter)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			TrialInfoCreateOutcomeCounter = are.ExistingCollector.(*prometheus.CounterVec)
		} else {
			log.Printf("Failed to register TrialInfoCreateOutcomeCounter: %v", err)
		}
	}
}

// EmitKmsKeyLimitReached emits a metric when KMS key rotation is blocked due to key limit
func EmitKmsKeyLimitReached(kmsConfigUUID, limitType string) {
	KmsKeyLimitReachedCounter.WithLabelValues(
		kmsConfigUUID,
		limitType,
	).Inc()
}

// RegisterCmekBackupRewriteErrorGauge registers the CMEK backup rewrite error gauge
func RegisterCmekBackupRewriteErrorGauge() {
	err := prometheus.Register(CmekBackupRewriteErrorGauge)
	if err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			CmekBackupRewriteErrorGauge = are.ExistingCollector.(*prometheus.GaugeVec)
		} else {
			log.Printf("Failed to register CmekBackupRewriteErrorGauge: %v", err)
		}
	}
}

// AddCMEKRewriteErrorResult records a CMEK rotation failure. failureType must
// be a fixed category (e.g. "bucket_rotation_failed") to keep Prometheus label
// cardinality low.
func AddCMEKRewriteErrorResult(bucketName, ownerID, backupVaultUUID, failureType string) {
	CmekBackupRewriteErrorGauge.WithLabelValues(failureType, bucketName, ownerID, backupVaultUUID).Inc()
}

// EmitKmsRotationFailure emits a metric when KMS key rotation fails for a KMS config
func EmitKmsRotationFailure(kmsConfigUUID, serviceAccountEmail, failureType string) {
	KmsRotationFailureCounter.WithLabelValues(
		kmsConfigUUID,
		serviceAccountEmail,
		failureType,
	).Inc()
}

func EmitTrialAccountSyncRun(result string) {
	TrialAccountSyncRunCounter.WithLabelValues(result).Inc()
}

func EmitTrialAccountSyncAccountOutcome(outcome string) {
	TrialAccountSyncAccountOutcomeCounter.WithLabelValues(outcome).Inc()
}

func EmitTrialInfoCreateOutcome(outcome string) {
	TrialInfoCreateOutcomeCounter.WithLabelValues(outcome).Inc()
}

// Aggregate and emit metrics for Autotier enabled volumes

func EmitAutoTierEnabledMetric(volumes []*datamodel.Volume) {
	autoTierEnabledGauge.Reset()
	type autoTierKey struct {
		Name        string
		State       string
		AccountName string
	}
	counts := make(map[autoTierKey]int)
	for _, v := range volumes {
		if v.AutoTieringEnabled {
			accountName := ""
			if v.Account != nil {
				accountName = v.Account.Name
			}
			key := autoTierKey{
				Name:        v.Name,
				State:       v.State,
				AccountName: accountName,
			}
			counts[key]++
		}
	}
	for key, count := range counts {
		autoTierEnabledGauge.WithLabelValues(
			key.Name,
			key.State,
			key.AccountName,
		).Set(float64(count))
	}
}

// Aggregate and emit metrics for CRR enabled volumes

func EmitCRREnabledMetric(volumes []*datamodel.Volume) {
	crrEnabledGauge.Reset()
	type crrKey struct {
		Name        string
		State       string
		AccountName string
	}
	counts := make(map[crrKey]int)
	for _, v := range volumes {
		if v.SnapshotPolicy != nil && v.SnapshotPolicy.IsEnabled {
			accountName := ""
			if v.Account != nil {
				accountName = v.Account.Name
			}
			key := crrKey{
				Name:        v.Name,
				State:       v.State,
				AccountName: accountName,
			}
			counts[key]++
		}
	}
	for key, count := range counts {
		crrEnabledGauge.WithLabelValues(
			key.Name,
			key.State,
			key.AccountName,
		).Set(float64(count))
	}
}

// Aggregate and emit metrics for LargeVolume enabled volumes

func EmitLargeVolumeEnabledMetric(volumes []*datamodel.Volume) {
	largeVolumeEnabledGauge.Reset()
	type largeVolumeKey struct {
		Name        string
		State       string
		AccountName string
	}
	counts := make(map[largeVolumeKey]int)
	for _, v := range volumes {
		if v.LargeVolumeAttributes != nil && v.LargeVolumeAttributes.LargeCapacity {
			accountName := ""
			if v.Account != nil {
				accountName = v.Account.Name
			}
			key := largeVolumeKey{
				Name:        v.Name,
				State:       v.State,
				AccountName: accountName,
			}
			counts[key]++
		}
	}
	for key, count := range counts {
		largeVolumeEnabledGauge.WithLabelValues(
			key.Name,
			key.State,
			key.AccountName,
		).Set(float64(count))
	}
}

// Aggregate and emit metrics for CBS enabled volumes

func EmitCBSEnabledMetric(volumes []*datamodel.Volume) {
	cbsEnabledGauge.Reset()
	type cbsKey struct {
		Name        string
		State       string
		AccountName string
	}
	counts := make(map[cbsKey]int)
	for _, v := range volumes {
		if v.DataProtection != nil && v.DataProtection.BackupVaultID != "" {
			accountName := ""
			if v.Account != nil {
				accountName = v.Account.Name
			}
			key := cbsKey{
				Name:        v.Name,
				State:       v.State,
				AccountName: accountName,
			}
			counts[key]++
		}
	}
	for key, count := range counts {
		cbsEnabledGauge.WithLabelValues(
			key.Name,
			key.State,
			key.AccountName,
		).Set(float64(count))
	}
}

// Aggregate and emit metrics for Eligibility String

func EmitEligibilityStringMetric(volumes []*datamodel.Volume, expertModeVolumes []*datamodel.ExpertModeVolumes) {
	eligibilityStringGauge.Reset()
	type eligibilityKey struct {
		Name      string
		State     string
		CreatedBy string
	}
	counts := make(map[eligibilityKey]int)
	for _, v := range volumes {
		key := eligibilityKey{
			Name:      v.Name,
			State:     v.State,
			CreatedBy: CreatedByLabelValue,
		}
		counts[key]++
	}
	for _, v := range expertModeVolumes {
		key := eligibilityKey{
			Name:      v.Name,
			State:     v.State,
			CreatedBy: CreatedByOntapProxyLabelValue,
		}
		counts[key]++
	}
	for key, count := range counts {
		eligibilityStringGauge.WithLabelValues(
			key.Name,
			key.State,
			key.CreatedBy,
		).Set(float64(count))
	}
}

// Aggregate and emit metrics for Backup Size
func EmitBackupDetailsMetric(details []BackupDetailForMetric) {
	backupSizeGauge.Reset()
	for _, d := range details {
		backupSizeGauge.WithLabelValues(
			d.VolName,
			d.AccountName,
		).Set(float64(d.Size))
	}
}

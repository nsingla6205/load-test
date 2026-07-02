package env

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-openapi/errors"
	"github.com/goccy/go-yaml"
)

// BuildCaURI builds a ca_uri string from individual CA components.
// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
// Uses environment variables as fallback for empty values.
func BuildCaURI(caPoolDeployedProjectID, caPoolName, caName string) string {
	// Use env vars as fallback for empty values
	if caPoolDeployedProjectID == "" {
		caPoolDeployedProjectID = CaPoolDeployedProjectID
	}
	if caPoolName == "" {
		caPoolName = CaPoolName
	}
	if caName == "" {
		caName = CaName
	}
	// If all values are still empty after fallback, return empty string
	if caPoolDeployedProjectID == "" && caPoolName == "" && caName == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s", caPoolDeployedProjectID, caPoolName, caName)
}

// ParseCaURI parses a ca_uri string into individual CA components.
// Format: ca_pool_deployed_project_id/ca_pool_name/ca_name
// Returns empty strings if caURI is empty or invalid.
func ParseCaURI(caURI string) (caPoolDeployedProjectID, caPoolName, caName string) {
	if caURI == "" {
		// Fallback to environment variables
		return CaPoolDeployedProjectID, CaPoolName, CaName
	}

	parts := strings.Split(caURI, "/")
	if len(parts) != 3 {
		// Invalid format, fallback to environment variables
		return CaPoolDeployedProjectID, CaPoolName, CaName
	}

	caPoolDeployedProjectID = parts[0]
	caPoolName = parts[1]
	caName = parts[2]

	// Fallback to environment variables for empty parts
	if caPoolDeployedProjectID == "" {
		caPoolDeployedProjectID = CaPoolDeployedProjectID
	}
	if caPoolName == "" {
		caPoolName = CaPoolName
	}
	if caName == "" {
		caName = CaName
	}

	return caPoolDeployedProjectID, caPoolName, caName
}

const (
	Admin    = "admin"
	LocalEnv = "local"

	// EnvAddressSpaceMgmtEnabled is the feature flag that enables address space management.
	// When true, VCP passes pre-registered address ranges as RequestedRanges to GCP Service
	// Networking during pool subnet creation, and tracks range state (IN_USE/CREATED) on
	// pool create/delete.
	EnvAddressSpaceMgmtEnabled = "ADDRESS_SPACE_MGMT_ENABLED"
)

func init() {
	if GetBool("LOCAL_ENV", false) {
		volumeEnvPath = "./config/config.yaml"
	}
	LogLevel = GetString("LOGGER_LEVEL", "info")
	LoggerType = GetString("LOGGER_TYPE", "slog")
	SlogHandlerType = GetString("SLOG_HANDLER_TYPE", "json")
	ExporterType = GetString("EXPORTER_TYPE", "stdout")
	AddSource = GetBool("ADD_LOG_SOURCE_FILE", false)
	ServiceName = GetString("OTEL_SERVICE_NAME", "VCP-VSA")
	OtelGoogleProjectID = GetString("OTEL_GOOGLE_PROJECT_ID", "")
}

func getFromVolumeOrEnv(key string) (string, error) {
	// 1. Try from localConfig map if useVolumeEnv is true
	if useVolumeEnv {
		if val, ok := localConfig[key]; ok && val != "" {
			return val, nil
		}
		// 2. If not already loaded, try to load from volume file (YAML)
		data, err := os.ReadFile(volumeEnvPath)
		if err == nil {
			tempConfig := map[string]string{}
			if yamlErr := yaml.Unmarshal(data, &tempConfig); yamlErr == nil {
				// Cache loaded config
				localConfig = tempConfig
				if val, ok := localConfig[key]; ok && val != "" {
					return val, nil
				}
			}
		}
	}
	// 3. Fallback to os.Getenv
	if val, found := os.LookupEnv(key); found {
		return val, nil
	}
	// 4. Not found anywhere
	return "", fmt.Errorf("environment variable or config key '%s' not found", key)
}

// IsEnvSet returns true if the specified environment variable is set, false otherwise.
func IsEnvSet(key string) bool {
	_, found := os.LookupEnv(key)
	return found
}

// GetStringFromEnvIfNil returns the specified string if non-nil.
// If nil, returns the specified environment variable if set.
// If the environment variable is not set, returns the specified default.
func GetStringFromEnvIfNil(val *string, key, def string) *string {
	if val == nil {
		v := GetString(key, def)
		val = &v
	}
	return val
}

// GetStrings returns all the environment variables that match the pattern given by regex.
func GetStrings(regex string) (variables map[string]string, err error) {
	variables = map[string]string{}
	re, err := regexp.Compile(regex)
	if err != nil {
		return variables, err
	}
	for _, element := range os.Environ() {
		variable := strings.SplitN(element, "=", 2) // Split only on the first "="
		varName := variable[0]
		if re.MatchString(varName) {
			variables[varName] = variable[1]
		}
	}
	return variables, nil
}

// GetString returns the specified environment variable.
// If the environment variable is not set, returns the specified default.
func GetString(key, def string) string {
	if env, err := getFromVolumeOrEnv(key); err == nil {
		return env
	}
	return def
}

// GetIntFromEnvIfNil returns the specified integer if non-nil.
// If nil, returns the integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetIntFromEnvIfNil(val *int, key string, def int) *int {
	if val == nil {
		v := GetInt(key, def)
		val = &v
	}
	return val
}

// GetInt returns an integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetInt(key string, def int) int {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.Atoi(val); err1 == nil {
			return intVal
		}
	}
	return def
}

// GetIntNotNegative returns an integer representation of the specified environment variable.
// If the environment variable is not set, is not valid, or is negative, returns the specified default.
func GetIntNotNegative(key string, def int) int {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.Atoi(val); err1 == nil && intVal >= 0 {
			return intVal
		}
	}
	return def
}

// GetUintFromEnvIfNil returns the specified unsigned integer if non-nil.
// If nil, returns the unsigned integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetUintFromEnvIfNil(val *uint, key string, def uint) *uint {
	if val == nil {
		v := GetUint(key, def)
		val = &v
	}
	return val
}

// GetUint returns an unsigned integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetUint(key string, def uint) uint {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.ParseUint(val, 10, 32); err1 == nil {
			return uint(intVal)
		}
	}
	return def
}

// GetUint16 returns a 16-bit unsigned integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetUint16(key string, def uint16) uint16 {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.ParseUint(val, 10, 16); err1 == nil {
			return uint16(intVal)
		}
	}
	return def
}

// GetInt64FromEnvIfNil returns the specified 64-bit integer if non-nil.
// If nil, returns the 64-bit integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetInt64FromEnvIfNil(val *int64, key string, def int64) *int64 {
	if val == nil {
		v := GetInt64(key, def)
		val = &v
	}
	return val
}

// GetInt64 returns a 64-bit integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetInt64(key string, def int64) int64 {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.ParseInt(val, 10, 64); err1 == nil {
			return intVal
		}
	}
	return def
}

// GetUint64FromEnvIfNil returns the specified 64-bit unsigned integer if non-nil.
// If nil, returns the 64-bit unsigned integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetUint64FromEnvIfNil(val *uint64, key string, def uint64) *uint64 {
	if val == nil {
		v := GetUint64(key, def)
		val = &v
	}
	return val
}

// GetUint64 returns a 64-bit unsigned integer representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetUint64(key string, def uint64) uint64 {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.ParseUint(val, 10, 64); err1 == nil {
			return intVal
		}
	}
	return def
}

// GetFloat64FromEnvIfNil returns the specified 64-bit floating-point number if non-nil.
// If nil, returns the 64-bit floating-point number representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetFloat64FromEnvIfNil(val *float64, key string, def float64) *float64 {
	if val == nil {
		v := GetFloat64(key, def)
		val = &v
	}
	return val
}

// GetFloat64 returns a 64-bit floating-point number representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetFloat64(key string, def float64) float64 {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if intVal, err1 := strconv.ParseFloat(val, 64); err1 == nil {
			return intVal
		}
	}
	return def
}

// GetFloat64NotNegative returns a float64 env var, falling back to def if the value is negative.
func GetFloat64NotNegative(key string, def float64) float64 {
	if val, err := getFromVolumeOrEnv(key); err == nil {
		if f, err1 := strconv.ParseFloat(val, 64); err1 == nil && f >= 0 {
			return f
		}
	}
	return def
}

// GetCertificateRotationThresholdPercentage reads CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE as a percentage value (0-100)
// and converts it to a decimal (0-1) for internal calculations.
// If the value is > 1, it's treated as a percentage (e.g., 75 = 75%) and divided by 100.
// If the value is <= 1, it's treated as a decimal for backward compatibility (e.g., 0.75 = 75%).
// Default: 75 (meaning 75% of certificate lifetime)
func GetCertificateRotationThresholdPercentage() float64 {
	threshold := GetFloat64("CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE", 75.0) // Default to 75%

	// If value is > 1, treat as percentage and convert to decimal
	if threshold > 1.0 {
		return threshold / 100.0
	}
	// If value is <= 1, treat as decimal (backward compatibility)
	return threshold
}

// GetCertificateRotationPoolStates reads CERTIFICATE_ROTATION_POOL_STATES as a comma-separated list of pool states
// (e.g. "READY,DEGRADED"). Pools in these states are eligible for certificate/password rotation.
// Default: "READY,DEGRADED"
func GetCertificateRotationPoolStates() []string {
	raw := GetString("CERTIFICATE_ROTATION_POOL_STATES", "READY,DEGRADED")
	if raw == "" {
		return []string{"READY", "DEGRADED"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"READY", "DEGRADED"}
	}
	return out
}

// GetOCICertificateRotationThresholdPercentage reads OCI_CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE
// as a percentage value (0-100) and converts it to a decimal (0-1) for internal calculations.
// OCI cert rotation is gated independently from GCP so OCI pools can be tuned and rolled out
// separately. If the value is > 1, it is treated as a percentage (e.g. 75 = 75%) and divided by 100;
// if <= 1, it is treated as a decimal for backward compatibility (e.g. 0.75 = 75%).
// Default: 75 (meaning 75% of the active certificate version's lifetime).
func GetOCICertificateRotationThresholdPercentage() float64 {
	threshold := GetFloat64("OCI_CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE", 75.0)

	if threshold > 1.0 {
		return threshold / 100.0
	}
	return threshold
}

// GetOCICertificateRotationPoolStates reads OCI_CERTIFICATE_ROTATION_POOL_STATES as a
// comma-separated list of pool states (e.g. "READY,DEGRADED"). OCI pools in these states are
// eligible for certificate rotation. Kept separate from GetCertificateRotationPoolStates so OCI
// rollout can be scoped independently of GCP. Default: "READY,DEGRADED".
func GetOCICertificateRotationPoolStates() []string {
	raw := GetString("OCI_CERTIFICATE_ROTATION_POOL_STATES", "READY,DEGRADED")
	if raw == "" {
		return []string{"READY", "DEGRADED"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"READY", "DEGRADED"}
	}
	return out
}

// GetOCICertificateRotationCronExpression reads OCI_CERTIFICATE_ROTATION_CRON_EXPRESSION,
// the cron cadence for the OCI certificate-rotation Temporal Schedule. Whitespace is
// trimmed and an empty/whitespace-only override falls back to the default. Default:
// "0 0 * * *" (daily at midnight UTC), matching the GCP rotation cadence.
func GetOCICertificateRotationCronExpression() string {
	const defaultCron = "0 0 * * *"
	cron := strings.TrimSpace(GetString("OCI_CERTIFICATE_ROTATION_CRON_EXPRESSION", defaultCron))
	if cron == "" {
		return defaultCron
	}
	return cron
}

// GetBoolFromEnvIfNil returns the specified boolean if non-nil.
// If nil, returns the boolean representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetBoolFromEnvIfNil(val *bool, key string, def bool) *bool {
	if val == nil {
		v := GetBool(key, def)
		val = &v
	}
	return val
}

// GetBool returns a boolean representation of the specified environment variable.
// If the environment variable is not set or is not valid, returns the specified default.
func GetBool(key string, def bool) bool {
	val, err := getFromVolumeOrEnv(key)
	if err != nil {
		return def
	}
	env := strings.ToLower(val)
	if env != "" {
		if val, err := strconv.Atoi(env); err == nil {
			return val != 0
		}
		if strings.HasPrefix(env, "true") || strings.HasPrefix(env, "yes") {
			return true
		}
		if strings.HasPrefix(env, "false") || strings.HasPrefix(env, "no") {
			return false
		}
	}
	return def
}

func GetDuration(key string, def time.Duration) time.Duration {
	val, err := getFromVolumeOrEnv(key)
	if err != nil {
		return def
	}
	if val != "" {
		if durationVal, parseErr := time.ParseDuration(val); parseErr == nil {
			return durationVal
		}
	}

	return def
}

const (
	USERNAME_PWD         = 0 // Username/Password authentication
	USERNAME_PWD_SEC_MGR = 1 // Username/Password authentication with secret manager
	USER_CERTIFICATE     = 2 // Certificate authentication
)

var (
	Env                          = GetString("ENV", "")
	AuthType                     = GetInt("VSA_AUTH_TYPE", USERNAME_PWD) // 0 for username/password, 1 for username/password in secret manager and 2 for certificate authentication
	Region                       = GetString("LOCAL_REGION", "")
	CaName                       = GetString("CA_NAME", "")
	CaPoolName                   = GetString("CA_POOL_NAME", "")
	CaPoolDeployedProjectID      = GetString("CA_POOL_DEPLOYED_PROJECT_ID", "")
	SecretManagerProjectID       = GetString("SECRET_MANAGER_PROJECT_ID", "")
	VsaDeployedDnsName           = GetString("VSA_DEPLOYED_DNS_NAME", "")
	VsaManagedZone               = GetString("VSA_MANAGED_ZONE", "")
	CertificateLifetime          = GetString("CERTIFICATE_LIFETIME", "94608000s")        // Default to 3 years
	CertificateRotationThreshold = GetCertificateRotationThresholdPercentage()           // Default to 75% of lifetime
	MinimumCertificateLifetime   = GetString("MINIMUM_CERTIFICATE_LIFETIME", "5184000s") // Default to 2 months
	NodePassword                 = GetString("VSA_NODE_PASSWORD", "")
	CloudDNSCacheTTL             = GetInt64("CLOUD_DNS_CACHE_TTL", 300) // Default to 300 seconds
	PrivateKeyBits               = GetInt("PRIVATE_KEY_BITS", 4096)     // Default to 4096 bits for RSA keys

	MgmtFirewallSourceRanges = GetString("MGMT_FIREWALL_SOURCE_RANGES", "")
	RsmFirewallSourceRanges  = GetString("RSM_FIREWALL_SOURCE_RANGES", "")
	IcFirewallSourceRanges   = GetString("IC_FIREWALL_SOURCE_RANGES", "")
	DataFirewallSourceRanges = GetString("DATA_FIREWALL_SOURCE_RANGES", "")

	MgmtRegionalNatIP = GetString("MGMT_REGIONAL_NAT_IP", "")

	MgmtNetworkIpRange = GetString("MGMT_NETWORK_IP_RANGE", "198.18.0.0/20")
	RsmNetworkIpRange  = GetString("RSM_NETWORK_IP_RANGE", "198.18.16.0/20")
	IcNetworkIpRange   = GetString("IC_NETWORK_IP_RANGE", "198.18.32.0/20")

	WorkerTaskQueue = GetString("WORKER_TASK_QUEUE", "customer-workflows")

	NLFLicenseSecretPath = GetString("NLF_LICENSE_SECRET_PATH", "")
	// Get current VCP version from environment
	CurrentOntapVersionDetails = GetString("ONTAP_VERSION_DETAILS", "9.17.1P2")
	// Get experimental ONTAP version from environment (for allowlisted accounts)
	ExperimentalOntapVersionDetails = GetString("ONTAP_VERSION_DETAILS_EXPERIMENTAL", "")
	// File support ONTAP version (used for file protocol support check)
	FileSupportOntapVersion = GetString("FILE_SUPPORT_ONTAP_VERSION", "9.18.1")
	// ONTAP Image Version Match Configuration
	SkipOntapImageVersionMatch = GetBool("SKIP_ONTAP_IMAGE_VERSION_MATCH", false)
	// EnableBackupVaultVCPOrchestration when true makes VCP the orchestrator for backup vault
	EnableBackupVaultVCPOrchestration = GetBool("ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION", false)

	ExpertModeUserSuffix     = GetString("EXPERT_MODE_USER_SUFFIX", "gadmin")
	PrivExpertModeUserSuffix = GetString("PRIV_EXPERT_MODE_USER_SUFFIX", "padmin")
	// AccountHostRoleNameTemplate renders ONTAP role names with a {name_suffix} placeholder.
	AccountHostRoleNameTemplate = GetString("ACCOUNT_HOST_ROLE_NAME_TEMPLATE", "gcnv_r_{name_suffix}")
	// AccountHostUserNameTemplate renders ONTAP service account names with a {name_suffix} placeholder.
	AccountHostUserNameTemplate = GetString("ACCOUNT_HOST_USER_NAME_TEMPLATE", "gcnv_u_{name_suffix}")

	// Hyperscaler is the cloud provider: "gcp" or "oci". Read from env HYPERSCALER at init.
	// In OCI deployments this is set via the vcp-worker ConfigMap (global.hyperscaler in values.yaml).
	Hyperscaler = GetString("HYPERSCALER", "gcp")

	MaxBatchPoolUUIDs            = GetInt("MAX_BATCH_POOL_UUIDS", 1000)
	MaxBatchSnapshotUUIDs        = GetInt("MAX_BATCH_SNAPSHOT_UUIDS", 1000)
	MaxBatchActiveDirectoryUUIDs = GetInt("MAX_BATCH_AD_UUIDS", 1000)
	MaxBatchBackupVaultUUIDs     = GetInt("MAX_BATCH_BACKUP_VAULT_UUIDS", 1000)
	MaxBatchBackupPolicyUUIDs    = GetInt("MAX_BATCH_BACKUP_POLICY_UUIDS", 1000)
	MaxBatchQuotaRuleUUIDs       = GetInt("MAX_BATCH_QUOTA_RULE_UUIDS", 1000)
	MaxBatchBackupUUIDs          = GetInt("MAX_BATCH_BACKUP_UUIDS", 1000)
	MaxBatchReplicationURIs      = GetInt("MAX_BATCH_REPLICATION_URIS", 700)
	MaxBatchKmsConfigUUIDs       = GetInt("MAX_BATCH_KMS_CONFIG_UUIDS", 1000)

	// OCISecretDeletionRetentionDays controls how many days after scheduling a
	// deletion the OCI Vault secret is permanently removed. OCI enforces a
	// minimum of 1 day; the service default is 7 days.
	OCISecretDeletionRetentionDays = GetInt("OCI_SECRET_DELETION_RETENTION_DAYS", 7)

	// OCICompartmentOCID is the OCID of the OCI compartment where VCP-managed
	// secrets (ONTAP admin passwords) are created.
	OCICompartmentOCID = GetString("OCI_COMPARTMENT_OCID", "")

	// OCIVaultOCID is the OCID of the OCI Vault used to store and retrieve
	// VCP-managed secrets (ONTAP admin passwords). Used by both the create
	// and delete credential paths so they always reference the same vault.
	OCIVaultOCID = GetString("OCI_VAULT_OCID", "")

	// OCIMasterKeyOCID is the OCID of the master encryption key used to encrypt
	// VCP-managed secrets in OCI Vault.
	OCIMasterKeyOCID = GetString("OCI_MASTER_KEY_OCID", "")

	// OCIAuthType controls which OCI SDK authentication method is used:
	//   "config_file"           — reads from ~/.oci/config (local development)
	//   "env"                   — reads from OCI_TENANCY, OCI_USER, etc. env vars
	//   "instance_principal"    — for workloads on OCI compute instances
	//   "oke_workload_identity" — for pods on OKE with workload identity federation
	// Defaults to "oke_workload_identity".
	OCIAuthType = GetString("OCI_AUTH_TYPE", "oke_workload_identity")

	// OCIOntapAdminUsername is the ONTAP cluster admin username applied at
	// pool-creation time on OCI. Captured once at process start so the
	// orchestrator factory and the OCI pool workflow agree on a single value
	OCIOntapAdminUsername = GetString("OCI_ONTAP_ADMIN_USERNAME", "admin")

	// OCIIssuerCAOCID is the OCID of the OCI private Certificate Authority that
	// signs internally-managed certificates created for VSA clusters. The OCI
	// Certificates Service generates the key pair, builds the CSR, and signs
	// the certificate against this CA in a single API call
	OCIIssuerCAOCID = GetString("OCI_ISSUER_CA_OCID", "")

	// OCICertificateValidityDays is the validity window for internally-managed
	// certificates, expressed in days. Mirrors GCP's CERTIFICATE_LIFETIME
	// (which is in seconds). Default is 1000 days.
	OCICertificateValidityDays = GetInt("OCI_CERTIFICATE_VALIDITY_DAYS", 1000)

	// OCICertificateDeletionRetentionDays controls how many days after scheduling
	// a deletion the OCI certificate is permanently removed. OCI enforces a
	// minimum of 1 day; the service default is 7 days.
	OCICertificateDeletionRetentionDays = GetInt("OCI_CERTIFICATE_DELETION_RETENTION_DAYS", 7)

	// EnableOCIVsaCertificateRotation gates the OCI VSA certificate-rotation flow.
	// It is read by both the rotation dispatcher workflow (self-gate) and the
	// schedule reconciler (create vs delete the Temporal Schedule). Gated
	// independently of GCP so OCI can be rolled out separately. Default: false.
	EnableOCIVsaCertificateRotation = GetBool("ENABLE_OCI_VSA_CERTIFICATE_ROTATION", false)

	// OCICertificateRotationBatchSize is the number of pools fetched per list call
	// when the dispatcher fans out rotation (bounds the Temporal activity
	// result size). Default: 10 pools per batch.
	OCICertificateRotationBatchSize = GetInt("OCI_CERTIFICATE_ROTATION_BATCH_SIZE", 10)

	// OCICertificateRotationChildWorkflowTimeoutMinutes bounds each per-pool
	// rotation workflow run (Temporal WorkflowRunTimeout), in minutes.
	//
	// This must comfortably exceed the chained per-phase activity budget: the
	// per-pool workflow runs several sequential activities, each with its own
	// StartToCloseTimeout (5m) × MaximumAttempts (2) from
	// PopulateRotationRetryPolicyParams. If the run timeout is too small a slow
	// phase (e.g. CleanupAndStage can spend ~180s on two 90s WaitForCertificateActive
	// calls) can exhaust the run before later phases or their retries get to run.
	// The rotation self-heals on the next sweep (idempotent, reuses the PENDING
	// version), but a tight timeout makes rotations take multiple sweeps and shows
	// noisy terminated runs in Temporal. Default: 15 minutes — clears the realistic
	// worst case (waits + ONTAP round-trips + a phase retry) while still bounding
	// genuinely stuck runs.
	OCICertificateRotationChildWorkflowTimeoutMinutes = GetInt("OCI_CERTIFICATE_ROTATION_CHILD_WORKFLOW_TIMEOUT_MINUTES", 15)

	// OCIVsaDnsZoneOCID is the OCID of the OCI DNS zone in which per-node
	// A records are registered for VSA cluster management LIFs. This zone's
	// suffix MUST match VsaDeployedDnsName so the certificate's wildcard SAN
	// (issued for `*.<deploymentName>.<VsaDeployedDnsName>` in
	// _createCertificateForVSAClusterOCI) covers the FQDNs registered here.
	//
	// Using the zone OCID (not the zone name) means the OCI SDK does NOT
	// require a separate ViewId even for PRIVATE zones — see
	// dns.UpdateRRSetRequest.ViewId: "Required when accessing a private zone
	// by name".
	OCIVsaDnsZoneOCID = GetString("OCI_VSA_DNS_ZONE_OCID", "")

	// OCIVsaDnsScope controls the scope query parameter on every OCI DNS
	// request: "PRIVATE" (default — VSA management LIFs are private IPs
	// resolvable only inside the VCN) or "GLOBAL" (public zone, useful for
	// local-dev sandbox testing). Mirrors the GCP managed-zone visibility
	// model, which is implicit in GCP's VSA_MANAGED_ZONE configuration.
	OCIVsaDnsScope = strings.ToUpper(GetString("OCI_VSA_DNS_SCOPE", "PRIVATE"))

	// Worker concurrency caps and rate limits.
	// Each queue is tuned independently — there is no shared base variable.
	// All values are overridable via env.
	//
	// Concurrency caps (MaxConcurrent*): block the worker from polling new tasks when all slots
	// are occupied. 0 = SDK default (1000, effectively unbounded for our workloads).
	//
	// Rate limits (*PerSecond): control task pickup speed, not concurrent count. 0 = unlimited.

	// customer-workflows queue
	CustomerWorkerMaxConcurrentActivityExecution      = GetIntNotNegative("CUSTOMER_WORKER_MAX_CONCURRENT_ACTIVITY_EXECUTION", 0)            // 0 = SDK default (1000)
	CustomerWorkerMaxConcurrentWorkflowTaskExecution  = GetIntNotNegative("CUSTOMER_WORKER_MAX_CONCURRENT_WORKFLOW_TASK_EXECUTION", 0)       // 0 = SDK default (1000)
	CustomerWorkerMaxConcurrentLocalActivityExecution = GetIntNotNegative("CUSTOMER_WORKER_MAX_CONCURRENT_LOCAL_ACTIVITY_EXECUTION", 0)      // 0 = SDK default (1000)
	CustomerWorkerActivitiesPerSecond                 = GetFloat64NotNegative("CUSTOMER_WORKER_ACTIVITIES_PER_SECOND", 0)                    // 0 = unlimited
	CustomerWorkerTaskQueueActivitiesPerSecond        = GetFloat64NotNegative("CUSTOMER_WORKER_TASK_QUEUE_ACTIVITIES_PER_SECOND", 0)         // 0 = unlimited

	// background-workflows queue
	BackgroundWorkerMaxConcurrentActivityExecution      = GetIntNotNegative("BACKGROUND_WORKER_MAX_CONCURRENT_ACTIVITY_EXECUTION", 0)        // 0 = SDK default (1000)
	BackgroundWorkerMaxConcurrentWorkflowTaskExecution  = GetIntNotNegative("BACKGROUND_WORKER_MAX_CONCURRENT_WORKFLOW_TASK_EXECUTION", 0)   // 0 = SDK default (1000)
	BackgroundWorkerMaxConcurrentLocalActivityExecution = GetIntNotNegative("BACKGROUND_WORKER_MAX_CONCURRENT_LOCAL_ACTIVITY_EXECUTION", 0)  // 0 = SDK default (1000)
	BackgroundWorkerActivitiesPerSecond                 = GetFloat64NotNegative("BACKGROUND_WORKER_ACTIVITIES_PER_SECOND", 0)                // 0 = unlimited
	BackgroundWorkerTaskQueueActivitiesPerSecond        = GetFloat64NotNegative("BACKGROUND_WORKER_TASK_QUEUE_ACTIVITIES_PER_SECOND", 0)     // 0 = unlimited

	// OCIUseTLSSNIOverride selects which connect-by-cert strategy the OCI
	// cert-auth pool path uses to bring up mTLS against the freshly-
	// provisioned ONTAP cluster:
	//
	//   - true  (DEFAULT for OCI): "skip OCI Private DNS records, dial the
	//     management LIF by IP, and override the TLS SNI to a synthetic
	//     name (mgmt.<deploymentName>.<VsaDeployedDnsName>) that matches
	//     the wildcard SAN baked into the cluster certificate". This is
	//     the steady-state OCI behaviour: it removes the
	//     OCICreateCloudDNSRecords + WaitForNodeDNS publish-race from the
	//     pool-create critical path while keeping full mTLS.
	//
	//   - false (opt-out / fallback): "create per-node OCI Private DNS A
	//     records and connect by FQDN". Flip the env var to this value
	//     when an environment needs the legacy DNS-records path —
	//     typically for debugging (nslookup-able node FQDNs) or as a
	//     stop-gap if a regression on the SNI path is suspected. The
	//     legacy path is preserved end-to-end in pool_workflows.go,
	//     pool_activities.go, provider_factory.go and rest_client.go, so
	//     turning the flag off is a clean revert with no schema change.
	//
	// Only consulted on the OCI hyperscaler (env.GetHyperscaler() ==
	// commonparams.ProviderOCI). GCP code paths never read this flag and
	// keep their existing CreateCloudDNSRecords + Cloud DNS behaviour.
	OCIUseTLSSNIOverride = GetBool("OCI_USE_TLS_SNI_OVERRIDE", true)
)

// networkEnvVariables holds the environment variables related to firewall of network configuration for source ranges
var NetworkSourceRanges = map[string]string{
	"MGMT_FIREWALL_SOURCE_RANGES": MgmtFirewallSourceRanges,
	"RSM_FIREWALL_SOURCE_RANGES":  RsmFirewallSourceRanges,
	"IC_FIREWALL_SOURCE_RANGES":   IcFirewallSourceRanges,
	"DATA_FIREWALL_SOURCE_RANGES": DataFirewallSourceRanges,
	"MGMT_REGIONAL_NAT_IP":        MgmtRegionalNatIP,
}

// networkEnvVariables holds the environment variables related to subnet in network configuration for ip ranges
var NetworkIpRanges = map[string]string{
	"MGMT_NETWORK_IP_RANGE": MgmtNetworkIpRange,
	"RSM_NETWORK_IP_RANGE":  RsmNetworkIpRange,
	"IC_NETWORK_IP_RANGE":   IcNetworkIpRange,
}

func ValidateEnvironmentVariables() error {
	// OCI deployments do not use GCP-specific env vars (CA, Secret Manager, Cloud DNS, etc.)
	if Hyperscaler == "oci" {
		if WorkerTaskQueue == "" {
			return errors.New(500, "WORKER_TASK_QUEUE must be set for worker configuration")
		}
		return nil
	}
	// GCP validations
	if Region == "" {
		return errors.New(500, "LOCAL_REGION must be set for authentication")
	}
	if CaName == "" {
		return errors.New(500, "CA_NAME must be set for authentication")
	}
	if CaPoolName == "" {
		return errors.New(500, "CA_POOL_NAME must be set for authentication")
	}
	if CaPoolDeployedProjectID == "" {
		return errors.New(500, "CA_POOL_DEPLOYED_PROJECT_ID must be set for authentication")
	}
	if SecretManagerProjectID == "" {
		return errors.New(500, "SECRET_MANAGER_PROJECT_ID must be set for authentication")
	}
	if VsaDeployedDnsName == "" {
		return errors.New(500, "VSA_DEPLOYED_DNS_NAME must be set for authentication")
	}
	if VsaManagedZone == "" {
		return errors.New(500, "VSA_MANAGED_ZONE must be set for authentication")
	}
	if CertificateLifetime == "" {
		return errors.New(500, "CERTIFICATE_LIFETIME must be set for authentication")
	}
	if CloudDNSCacheTTL == 0 {
		return errors.New(500, "CLOUD_DNS_CACHE_TTL must be set for authentication")
	}
	if NodePassword == "" {
		return errors.New(500, "VSA_NODE_PASSWORD must be set for authentication")
	}
	if PrivateKeyBits == 0 {
		return errors.New(500, "PRIVATE_KEY_BITS must be set for authentication")
	}
	if WorkerTaskQueue == "" {
		return errors.New(500, "WORKER_TASK_QUEUE must be set for worker configuration")
	}
	return validateNetworkEnvVariables()
}

func validateNetworkEnvVariables() error {
	for envVariableName, envVariableValue := range NetworkSourceRanges {
		if err := validateSourceRanges(envVariableName, envVariableValue); err != nil {
			return err
		}
	}
	for envVariableName, envVariableValue := range NetworkIpRanges {
		basicErrorString := " must be set for subnet for VSA deployment"
		if err := validateIpRange(envVariableValue, basicErrorString, envVariableName); err != nil {
			return err
		}
	}
	return nil
}

func validateSourceRanges(envVariableName, sourceRanges string) error {
	basicErrorString := " must be set for firewall for VSA deployment"
	if sourceRanges == "" {
		return errors.New(500, "%s"+basicErrorString, envVariableName)
	}
	ranges := strings.Split(sourceRanges, ",")
	for _, rangeStr := range ranges {
		if err := validateIpRange(rangeStr, basicErrorString, envVariableName); err != nil {
			return err
		}
	}
	return nil
}

func validateIpRange(ipRange, basicErrorString, envVariableName string) error {
	if ipRange == "" {
		return errors.New(500, basicErrorString, envVariableName)
	}
	if strings.Contains(ipRange, " ") {
		return errors.New(500, "%s%s. Can't contain empty spaces in: %s", envVariableName, basicErrorString, ipRange)
	}
	// Validate CIDR format using net.ParseCIDR
	if _, _, err := net.ParseCIDR(ipRange); err != nil {
		return errors.New(500, "%s%s. Invalid CIDR format in: %s", envVariableName, basicErrorString, ipRange)
	}
	return nil
}

// ValidateCertificateLifetime validates that the certificate lifetime meets the minimum requirement
// This prevents the worker service from starting if the certificate lifetime is too short
func ValidateCertificateLifetime() error {
	if Hyperscaler == "oci" {
		return nil // OCI does not use GCP certificate lifecycle
	}
	// Get current values from environment variables (not package-level variables)
	certificateLifetime := GetString("CERTIFICATE_LIFETIME", "94608000s")
	minimumCertificateLifetime := GetString("MINIMUM_CERTIFICATE_LIFETIME", "5184000s")

	// Parse the certificate lifetime duration
	duration, err := time.ParseDuration(certificateLifetime)
	if err != nil {
		return fmt.Errorf("invalid certificate lifetime format '%s': %w. Expected format like '94608000s' (seconds)", certificateLifetime, err)
	}

	// Parse the minimum certificate lifetime duration
	minimumDuration, err := time.ParseDuration(minimumCertificateLifetime)
	if err != nil {
		return fmt.Errorf("invalid minimum certificate lifetime format '%s': %w. Expected format like '5184000s' (seconds)", minimumCertificateLifetime, err)
	}

	// Check if certificate lifetime is less than the minimum requirement
	if duration < minimumDuration {
		return fmt.Errorf("certificate lifetime '%s' (%v) is less than minimum required lifetime '%s' (%v). Worker service cannot start with such a short certificate lifetime",
			certificateLifetime, duration, minimumCertificateLifetime, minimumDuration)
	}

	return nil
}

// GetHyperscaler returns the hyperscaler provider from environment variable, defaulting to "gcp"
func GetHyperscaler() string {
	return strings.ToLower(Hyperscaler)
}

var IsLocalEnv = isLocalEnv

func isLocalEnv() bool {
	return Env == LocalEnv
}

package utils

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	goerrors "errors"
	"fmt"
	"math"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	errs "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"go.temporal.io/sdk/workflow"
	"gorm.io/gorm"
)

var (
	localRegion                                = env.GetString("LOCAL_REGION", "local")
	PairedRegions                              = env.GetString("VCP_PAIRED_REGIONS", "")
	MinQuotaInBytesPool                        = env.GetUint64("MIN_QUOTA_IN_BYTES_POOL", 1*TiBInBytes)                  // 1 TiB
	MaxQuotaInBytesPool                        = env.GetUint64("MAX_QUOTA_IN_BYTES_POOL", 425*TiBInBytes)                // 425 TiB
	MinQuotaInBytesVolumeForVolume             = env.GetUint64("MIN_QUOTA_IN_BYTES_VOLUME", 1073741824)                  // 1 GiB
	MaxQuotaInBytesVolumeForVolume             = env.GetUint64("MAX_QUOTA_IN_BYTES_VOLUME", 140737488355328)             // 128 TiB
	MaxQuotaInBytesForFileVolume               = env.GetUint64("MAX_QUOTA_IN_BYTES_FILE_VOLUME", 329853488332800)        // 300 TiB
	MinQuotaInBytesLargeVolume                 = env.GetUint64("MIN_QUOTA_IN_BYTES_LARGE_VOLUME", 5277655813325)         // 4.8 TiB
	MinQuotaInBytesLargeVolumeWithCV           = env.GetUint64("MIN_QUOTA_IN_BYTES_LARGE_VOLUME_WITH_CV", 2638827906662) // 2.4 TiB
	MaxQuotaInBytesLargeVolume                 = env.GetUint64("MAX_QUOTA_IN_BYTES_LARGE_VOLUME", 20*PiBInBytes)         // 20 PiB
	MinSizeGranularity                         = env.GetUint64("MIN_SIZE_GRANULARITY", 1*GiBInBytes)                     // 1 GiB
	MinCustomThroughput                        = env.GetUint64("MIN_CUSTOM_THROUGHPUT", 64)                              // 64 MiBps (for pools)
	MinVolumeThroughput                        = env.GetUint64("MIN_VOLUME_THROUGHPUT", 1)                               // 1 MiBps (for volumes in manual pools)
	MaxCustomThroughput                        = env.GetUint64("MAX_CUSTOM_THROUGHPUT", 5120)                            // 5120 MiBps
	MinCustomIops                              = env.GetUint64("MIN_CUSTOM_IOPS", 1024)                                  // 1024 IOPS
	MaxCustomIops                              = env.GetUint64("MAX_CUSTOM_IOPS", 160000)                                // 160000 IOPS
	IopsPerMiBps                               = env.GetUint64("IOPS_PER_MIBPS", 16)                                     // 16 IOPS per MiBps (for auto-calculation)
	MinLvCoolTierCapacity                      = env.GetUint64("MIN_LV_POOL_COOL_TIER_CAPACITY", 6*TiBInBytes)           // 6TiB
	MaxLvPoolCapacity                          = env.GetUint64("MAX_LV_POOL_CAPACITY", 20*PiBInBytes)                    // 20PiB
	MaxLvHotTierCapacity                       = env.GetUint64("MAX_LV_HOT_TIER_POOL_CAPACITY", 2792232327315456)        // 2.48 PiB
	MinLvThroughput                            = env.GetUint64("MIN_LV_THROUGHPUT", 64)
	MaxLvThroughput                            = env.GetUint64("MAX_LV_THROUGHPUT", 60*1000) // convert to megabit per second
	MinLvCustomIops                            = env.GetUint64("MIN_LV_CUSTOM_IOPS", IopsPerMiBps*MinLvThroughput)
	MaxLvCustomIops                            = env.GetUint64("MAX_LV_CUSTOM_IOPS", IopsPerMiBps*MaxLvThroughput)
	MinTrueZonalThroughput                     = env.GetUint64("MIN_TRUE_ZONAL_THROUGHPUT", MinLvThroughput)
	MaxTrueZonalThroughput                     = env.GetUint64("MAX_TRUE_ZONAL_THROUGHPUT", MaxLvThroughput)
	MinTrueZonalCustomIops                     = env.GetUint64("MIN_TRUE_ZONAL_CUSTOM_IOPS", IopsPerMiBps*MinTrueZonalThroughput)
	MaxTrueZonalCustomIops                     = env.GetUint64("MAX_TRUE_ZONAL_CUSTOM_IOPS", IopsPerMiBps*MaxTrueZonalThroughput)
	MinTrueZonal24NodeThroughput               = env.GetUint64("MIN_TRUE_ZONAL_24_NODE_THROUGHPUT", MinTrueZonalThroughput)
	MaxTrueZonal24NodeThroughput               = env.GetUint64("MAX_TRUE_ZONAL_24_NODE_THROUGHPUT", 84000)
	MinTrueZonal24NodeCustomIops               = env.GetUint64("MIN_TRUE_ZONAL_24_NODE_CUSTOM_IOPS", IopsPerMiBps*MinTrueZonal24NodeThroughput)
	MaxTrueZonal24NodeCustomIops               = env.GetUint64("MAX_TRUE_ZONAL_24_NODE_CUSTOM_IOPS", 2318400)
	MaxTrueZonal24NodePoolCapacity             = env.GetUint64("MAX_TRUE_ZONAL_24_NODE_POOL_CAPACITY", 5*PiBInBytes)                 // 5 PiB without auto-tiering
	MaxTrueZonal24NodePoolCapacityWithAutoTier = env.GetUint64("MAX_TRUE_ZONAL_24_NODE_POOL_CAPACITY_WITH_AUTO_TIER", 50*PiBInBytes) // 50 PiB with auto-tiering
	MinHotTierSize                             = env.GetUint64("MIN_HOT_TIER_SIZE", 1099511627776)                                   // 1 TiB
	MinHotTierSizeLargeVolumes                 = env.GetUint64("MIN_HOT_TIER_SIZE_LARGE_VOLUMES", 6*TiBInBytes)
	ValidateSAKeyInGCP                         = env.GetBool("VALIDATE_SA_KEY_IN_GCP", true)
	CmekGlobalProjectID                        = env.GetString("CMEK_GLOBAL_PROJECT_ID", "")
	RegionNumberMap                            = env.GetString("REGION_NUMBER_MAP", DefaultRegionNumberMap)
	EnableMultiAD                              = env.GetBool("ENABLE_MULTI_AD", false)
	MaxNumberOfADPerAccount                    = env.GetInt("MAX_NUMBER_OF_AD_PER_ACCOUNT", 5)
	EnableMultipleCmek                         = env.GetBool("ENABLE_MULTIPLE_CMEK", false)
	MaxNumberOfKmsPerAccount                   = env.GetInt("MAX_NUMBER_OF_KMS_PER_ACCOUNT", 3)
	ParseRegionAndZone                         = _parseRegionAndZone
	ParseAndValidateRegionAndZone              = _parseAndValidateRegionAndZone
	GetPairedRegionURI                         = _getPairedRegionURI
	GetVolumeUriFromCcfeUri                    = _getVolumeUriFromCcfeUri
	ConvertStringToMap                         = _convertStringToMap
	ConvertBytesToGib                          = _convertBytesToGib
	ValidateCcfeReplicationUri                 = _validateCcfeReplicationUri
	ValidateOperationUri                       = _validateOperationUri
	RenameSnapshotName                         = _renameSnapshotName
	ConvertToGcpResourceName                   = _convertToGcpResourceName
	CheckForGcpNamingConvention                = _checkForGcpNamingConvention
	ParseProjectNumberFromURI                  = _parseProjectNumberFromURI
	GetReplicationNameFromURI                  = _getReplicationNameFromURI
	quotaLimitExceededRegex                    = regexp.MustCompile(`^Quota limit`)
	sleep                                      = _sleep
	exponentialBackOffErrors                   = []int{429}
	maxExpBackOffDelay                         = time.Duration(80) * time.Second
	jitterBase                                 = time.Millisecond
	generateRandomString                       = _generateRandomString
	ReplicationUriRegex                        = "^projects\\/([^\\/]+)\\/locations/([^\\/]+)/volumes\\/([^\\/]+)\\/replications\\/([^\\/]+)$"
	OperationUriRegex                          = "^/v1beta/projects/([^/]+)/locations/([^/]+)/operations/([^/]+)$"
	GetLocation                                = _getLocation
	GetBackupRegion                            = _getBackupRegion
	GetSourceVolumePathFromBackup              = _getSourceVolumePathFromBackup
	GetSourceSnapshotPathFromBackup            = _getSourceSnapshotPathFromBackup
	GenerateStrongPassword                     = _generateStrongPassword
	ValidateStrongPassword                     = _validateStrongPassword
	ParsePEMCertificate                        = _parsePEMCertificate
	CalculateRequiredVolumeSize                = _calculateRequiredVolumeSize
	// FileProtocolSupported controls whether file-based protocols (NFS/CIFS) are allowed
	FileProtocolSupported                              = env.GetBool("FILES_PROTOCOL_SUPPORT", false)
	experimentalVersionAllowlistedAccounts             = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))
	experimentalCustomHaLargeVolumeAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", ""))
	IsAllSquashEnabled                                 = env.GetBool("IS_ALL_SQUASH_ENABLED", true)
	isProberProject                                    = ParseCommaSeparatedStringToMap(env.GetString("PROBER_PROJECT_LIST", ""))
	AutoTieringEnabled                                 = env.GetBool("AUTO_TIERING_ENABLED", false)
	immutableBackupEnabled                             = env.GetBool("IMMUTABLE_BACKUP_ENABLED", false)
	crossRegionBackupEnabled                           = env.GetBool("CROSS_REGION_BACKUP_ENABLED", false)
	RestoreVolumeBufferEnabled                         = env.GetBool("RESTORE_VOLUME_BUFFER_ENABLED", false)
	EnableBackupVaultSwitching                         = env.GetBool("ENABLE_BACKUP_VAULT_SWITCHING", false)
	EnableGCNVVaultSwitching                           = env.GetBool("ENABLE_GCNV_VAULT_SWITCHING", false)
	enableKerberos                                     = env.GetBool("ENABLE_KERBEROS", false)
	EnableJobResourceUUIDIndex                         = env.GetBool("ENABLE_JOB_RESOURCE_UUID_INDEX", false)
	// TODO: Remove FORCE_VCP_KMS_PATH_FOR_TESTING once CMEK VPC-SC testing is complete.
	ForceVCPKMSPathForTesting               = env.GetBool("FORCE_VCP_KMS_PATH_FOR_TESTING", false)
	remoteBackupVaultHydrationNameMaxLength = 63
	remoteBackupVaultHydrationNamePrefix    = "-destination-"

	// Will match ONTAP version strings like "9.7.1", "9.8.2P3", "10.1.0", "10.3.1P2", etc.
	ontapVersionRegex = regexp.MustCompile(`\d+\.\d+\.\d+(?:P\d+)?`)
	// Matches full ONTAP release tokens reported by hardware hosts (P+X, X, D, RC suffixes).
	// Hotfix builds (e.g. 9.18.1P3_HF1) are intentionally excluded.
	hardwareOntapVersionRegex = regexp.MustCompile(`\d+\.\d+\.\d+(?:P\d+(?:X\d+)?|X\d+|D\d+|RC\d*(?:X\d+)?)?`)
	jsonMarshalFn             = json.Marshal
	jsonUnmarshalFn           = json.Unmarshal
)

// MaxLargeVolumeQuota returns the maximum large-volume size.
// A 24-node true-zonal pool (12 HA pairs) allows 5 PiB, or 50 PiB when auto-tiering is enabled.
// Other large volumes keep the shared hot-tier cap, or MaxQuotaInBytesLargeVolume when auto-tiering is enabled.
func MaxLargeVolumeQuota(is24NodeTrueZonal, autoTieringEnabled bool) uint64 {
	if is24NodeTrueZonal {
		if autoTieringEnabled {
			return MaxTrueZonal24NodePoolCapacityWithAutoTier
		}
		return MaxTrueZonal24NodePoolCapacity
	}
	if autoTieringEnabled {
		return MaxQuotaInBytesLargeVolume
	}
	return MaxLvHotTierCapacity
}

// IsCVPHostSet returns true when CVP_HOST is configured, indicating SDE mode is active.
func IsCVPHostSet() bool {
	if ForceVCPKMSPathForTesting {
		return false
	}
	return cvp.CVP_HOST != ""
}

// IsCVPHostConfigured returns true when CVP_HOST is configured.
// Unlike IsCVPHostSet, this check ignores test overrides.
func IsCVPHostConfigured() bool {
	return cvp.CVP_HOST != ""
}

// DefaultRegionNumberMap is the default JSON mapping of GCP regions to numeric codes.
// Used for node serial number generation and CMEK service account naming.
const DefaultRegionNumberMap = `{"africa-south1": "01","asia-east1": "02","asia-east2": "03","asia-northeast1": "04","asia-northeast2": "05","asia-northeast3": "06","asia-south1": "07","asia-south2": "08","asia-southeast1": "09","asia-southeast2": "10","australia-southeast1": "11","australia-southeast2": "12","europe-central2": "13","europe-north1": "14","europe-north2": "15","europe-southwest1": "16","europe-west1": "17","europe-west10": "18","europe-west12": "19","europe-west2": "20","europe-west3": "21","europe-west4": "22","europe-west6": "23","europe-west8": "24","europe-west9": "25","me-central1": "26","me-central2": "27","me-west1": "28","northamerica-northeast1": "29","northamerica-northeast2": "30","northamerica-south1": "31","southamerica-east1": "32","southamerica-west1": "33","us-central1": "34","us-east1": "35","us-east4": "36","us-east5": "37","us-south1": "38","us-west1": "39","us-west2": "40","us-west3": "41","us-west4": "42"}`

const (
	lowercaseLetters           = "abcdefghijklmnopqrstuvwxyz"
	uppercaseLetters           = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits                     = "0123456789"
	strongPasswordAllChars     = lowercaseLetters + uppercaseLetters + digits
	specialChars               = "!@#$%^&*()-_=+[]{}|;:,.<>?/`~"
	MinStrongPasswordLength    = 8
	QosTypeAuto                = "auto"
	QosTypeManual              = "manual"
	GiBInBytes                 = 1073741824
	TiBInBytes                 = 1099511627776
	PiBInBytes                 = 1125899906842624
	PercentageBase             = 100.0
	ImmutableBackupVaultErrMsg = "Immutable backup vaults are not supported for this region"
	BackupTypeMANUAL           = "MANUAL"
	BackupTypeSCHEDULED        = "SCHEDULED"
	// Pool QosType validation errors
	ErrMsgPoolAutoQosTypeCannotSpecifyThroughput   = "Pool has auto QoS type. Cannot specify throughputMibps. Volumes inherit QoS from the pool."
	ErrMsgPoolAutoQosTypeCannotSpecifyIops         = "Pool has auto QoS type. Cannot specify iops. Volumes inherit QoS from the pool."
	ErrMsgPoolAutoQosTypeCannotSpecifyVpgId        = "Pool has auto QoS type. Cannot specify volumePerformanceGroupId. Volumes inherit QoS from the pool."
	ErrMsgPoolManualQosTypeRequiresThroughputOrVpg = "Pool has manual QoS type. Either throughputMibps (with iops) or volumePerformanceGroupId must be provided."
	ErrMsgMQoSDestPoolNotAllowed                   = "Manual QoS (MQoS) is not allowed for destination pools in replication relationships."
	ErrMsgVpgMutuallyExclusiveWithQos              = "Cannot specify throughputMibps or iops with volumePerformanceGroupId. They are mutually exclusive."
	ErrMsgVpgAssignmentNotEnabled                  = "Volume performance group assignment is not enabled"
	ErrMsgMqosNotEnabledThroughput                 = "Manual QoS (MQOS) is not enabled. throughputMibps parameter is not supported."
	ErrMsgMqosNotEnabledIops                       = "Manual QoS (MQOS) is not enabled. iops parameter is not supported."
	ErrMsgMqosNotEnabledVpgId                      = "Manual QoS (MQOS) is not enabled. volumePerformanceGroupId parameter is not supported."
	// ActiveDirectoryGroupBuiltInBackupOperators defines the name of the built-in backup operators group
	ActiveDirectoryGroupBuiltInBackupOperators = `BUILTIN\Backup Operators`

	// ActiveDirectoryGroupBuiltInAdministrators defines the name of the built-in administrators group
	ActiveDirectoryGroupBuiltInAdministrators = `BUILTIN\Administrators`

	// ActiveDirectorySeSecurityPrivilege defines the name of the SE security privilege
	ActiveDirectorySeSecurityPrivilege = `SeSecurityPrivilege`
	wildCardForAllowlist               = "*"
	UnixSecurityStyle                  = "unix"
	NtfsSecurityStyle                  = "ntfs"
)

var strongPasswordCharCategories = []string{lowercaseLetters, uppercaseLetters, digits}

func ValidateIPv4Address(ipAddr string) bool {
	parsedIP := net.ParseIP(ipAddr)
	return parsedIP != nil && parsedIP.To4() != nil
}

// ValidateIPRange validates that start/end/gateway/netmask are valid IPv4 values
// and that gateway is reachable from both start and end addresses.
func ValidateIPRange(start, end, gateway, netmask string) error {
	if strings.TrimSpace(start) == "" {
		return fmt.Errorf("must specify start IP address")
	}
	if strings.TrimSpace(end) == "" {
		return fmt.Errorf("must specify end IP address")
	}
	if strings.TrimSpace(gateway) == "" {
		return fmt.Errorf("must specify gateway")
	}
	if strings.TrimSpace(netmask) == "" {
		return fmt.Errorf("must specify netmask")
	}

	startIP := net.ParseIP(start)
	if startIP == nil || startIP.To4() == nil || !startIP.Equal(startIP.To4()) {
		return fmt.Errorf("'%s' is not a valid IPv4 address", start)
	}
	endIP := net.ParseIP(end)
	if endIP == nil || endIP.To4() == nil || !endIP.Equal(endIP.To4()) {
		return fmt.Errorf("'%s' is not a valid IPv4 address", end)
	}
	gatewayIP := net.ParseIP(gateway)
	if gatewayIP == nil || gatewayIP.To4() == nil || !gatewayIP.Equal(gatewayIP.To4()) {
		return fmt.Errorf("'%s' is not a valid IPv4 address", gateway)
	}
	netmaskIP := net.ParseIP(netmask)
	if netmaskIP == nil || netmaskIP.To4() == nil || !netmaskIP.Equal(netmaskIP.To4()) || !isNetmask(netmaskIP) {
		return fmt.Errorf("'%s' is not a valid IPv4 netmask", netmask)
	}

	if compareIP(startIP, endIP) > 0 {
		return fmt.Errorf("specified start IP is higher than specified end IP")
	}

	lowerNet := &net.IPNet{IP: startIP, Mask: net.IPMask(netmaskIP)}
	if !lowerNet.Contains(gatewayIP) {
		return fmt.Errorf("specified gateway is unreachable from start IP")
	}
	upperNet := &net.IPNet{IP: endIP, Mask: net.IPMask(netmaskIP)}
	if !upperNet.Contains(gatewayIP) {
		return fmt.Errorf("specified gateway is unreachable from end IP")
	}

	return nil
}

func isNetmask(ip net.IP) bool {
	if ip == nil || ip.To4() == nil {
		return false
	}

	ones, bits := net.IPMask(ip.To4()).Size()
	return bits == 32 && ones > 0
}

func compareIP(ip1, ip2 net.IP) int {
	if len(ip1) != len(ip2) {
		if ip1.To4() != nil && ip2.To4() != nil {
			return compareIP(ip1.To4(), ip2.To4())
		}
		if ip1.To4() != nil {
			return -1
		}
		return 1
	}
	for i := 0; i < len(ip1); i++ {
		if ip1[i] < ip2[i] {
			return -1
		}
		if ip1[i] > ip2[i] {
			return 1
		}
	}
	return 0
}

// ItemsInSliceUnique checks if items in the slice are unique, returns false if not
func ItemsInSliceUnique(in []string) bool {
	seen := make(map[string]bool, len(in))
	for _, elem := range in {
		if !seen[strings.ToLower(elem)] {
			seen[strings.ToLower(elem)] = true
		} else {
			return false
		}
	}
	return true
}

// DeduplicateSlice returns a copy of elements with duplicate strings removed, preserving the order of first occurrence.
func DeduplicateSlice(elements []string) []string {
	uniqueElements := make(map[string]bool, len(elements))
	deduplicatedElements := make([]string, 0, len(elements))

	for _, element := range elements {
		if !uniqueElements[element] {
			uniqueElements[element] = true
			deduplicatedElements = append(deduplicatedElements, element)
		}
	}
	return deduplicatedElements
}

// ContainsString checks if items in the slice match the inputted string, returns false if not
func ContainsString(arr []string, elem string) bool {
	for _, obj := range arr {
		if strings.TrimSpace(obj) == elem {
			return true
		}
	}
	return false
}

func ContainsStringCaseInsensitive(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

func ContainsInt(arr []int, elem int) bool {
	for _, obj := range arr {
		if obj == elem {
			return true
		}
	}
	return false
}

func ContainsFloat64(arr []float64, elem float64) bool {
	for _, obj := range arr {
		if obj == elem {
			return true
		}
	}
	return false
}

// DeepCopyPool creates a deep copy of a datamodel.Pool so callers can safely
// mutate nested fields without affecting the original object.
func DeepCopyPool(pool *datamodel.Pool) (*datamodel.Pool, error) {
	if pool == nil {
		return nil, fmt.Errorf("pool is nil")
	}

	b, err := jsonMarshalFn(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal pool: %w", err)
	}

	var copied datamodel.Pool
	if err := jsonUnmarshalFn(b, &copied); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pool copy: %w", err)
	}

	return &copied, nil
}

func IsSliceEqual(slice1 []string, slice2 []string) bool {
	if len(slice1) != len(slice2) {
		return false
	}
	for _, elem := range slice1 {
		if !ContainsString(slice2, elem) {
			return false
		}
	}
	return true
}

// IsDuplicateUUID checks if uuid exists in the map.
func IsDuplicateUUID(keys map[string]bool, uuid string) bool {
	if _, value := keys[uuid]; !value {
		return false
	}
	return true
}

// EnvToInt32Conversion int to int32 Incorrect conversion for CodeQL
func EnvToInt32Conversion(envVal string, def int32) int32 {
	parseVal, err := strconv.ParseInt(envVal, 10, 32)
	if err != nil {
		return def
	} else {
		return int32(parseVal)
	}
}

func CheckForRetriableError(errorMessage string, retriableErrors []string) bool {
	// Iterate through each error message and check if it is retriableError
	if errorMessage == "" {
		return false
	}
	for _, retriableError := range retriableErrors {
		if strings.Contains(errorMessage, retriableError) {
			return true
		}
	}
	return false
}

func _parseAndValidateRegionAndZone(locationID string) (string, string, *gcpgenserver.Error) {
	region, zone, err := ParseRegionAndZone(locationID)
	if err != nil {
		code := float64(400)
		return "", "", &gcpgenserver.Error{Code: code, Message: err.Error()}
	}
	if region != localRegion {
		code := float64(400)
		msg := "Invalid region. Region can only be " + localRegion
		return "", "", &gcpgenserver.Error{Code: code, Message: msg}
	}
	return region, zone, nil
}

func _parseRegionAndZone(locationID string) (string, string, error) {
	var region string
	var zone string
	pattern := regexp.MustCompile(`^([a-z]+-[a-z]+\d+)(-[a-z])?$`)
	if pattern.MatchString(locationID) {
		if pattern.FindStringSubmatch(locationID)[2] == "" {
			// locationID represents a region
			region = locationID
			zone = ""
		} else {
			// locationID represents a zone
			region = pattern.FindStringSubmatch(locationID)[1]
			zone = pattern.FindStringSubmatch(locationID)[0]
		}
	} else {
		// locationID represents neither region nor zone.
		msg := "LocationID represents neither a region nor a zone"
		return "", "", errors.New(msg)
	}
	return region, zone, nil
}

// GetJWTTokenFromContext gets the JWT token from the context
func GetJWTTokenFromContext(ctx context.Context) string {
	if header, ok := ctx.Value(middleware.HeaderContextKey).(http.Header); ok {
		return header.Get("Authorization")
	}
	return ""
}

// ParseProjectId parses the remoteAccount id and returns project number and network name
func ParseProjectId(network string) (string, string, error) {
	tmp := strings.Split(network, "/")
	if len(tmp) < 5 {
		return "", "", fmt.Errorf("parseProjectId failed for network : %s", network)
	}
	return tmp[len(tmp)-4], tmp[len(tmp)-1], nil
}

// BytesToGigabytes converts bytes to gigabytes
func BytesToGigabytes(sizeInBytes uint64) uint64 {
	return sizeInBytes / 1024 / 1024 / 1024
}

// GigabytesToBytes converts gigabytes to bytes
func GigabytesToBytes(sizeInGigabytes uint64) uint64 {
	return uint64(sizeInGigabytes * 1024 * 1024 * 1024)
}

type Unit int

const (
	B Unit = 1 << (10 * iota)
	KiB
	MiB
	GiB
	TiB
	PiB
)

func ConvertToBytes(size float64, unit Unit) (int64, error) {
	switch unit {
	case B, KiB, MiB, GiB, TiB, PiB:
		return int64(size * float64(unit)), nil
	default:
		return 0, fmt.Errorf("invalid unit: %v", unit)
	}
}

// GetOptString safely converts a pointer to a string into an OptString.
func GetOptString(value *string) gcpgenserver.OptString {
	if value != nil {
		return gcpgenserver.NewOptString(*value)
	}
	return gcpgenserver.OptString{}
}

// GetOptInt64 safely converts a pointer to an int64 into an OptInt64.
func GetOptInt64(value *int64) gcpgenserver.OptInt64 {
	if value != nil {
		return gcpgenserver.NewOptInt64(*value)
	}
	return gcpgenserver.OptInt64{}
}

// GetOptBool safely converts a pointer to a bool into an OptBool.
func GetOptBool(value *bool) gcpgenserver.OptBool {
	if value != nil {
		return gcpgenserver.NewOptBool(*value)
	}
	return gcpgenserver.OptBool{}
}

// GetOptDateTime safely converts a pointer to a strfmt.DateTime into an OptDateTime.
func GetOptDateTime(value *strfmt.DateTime) gcpgenserver.OptDateTime {
	if value != nil {
		return gcpgenserver.NewOptDateTime(time.Time(*value))
	}
	return gcpgenserver.OptDateTime{}
}

func SafeString(value *string) gcpgenserver.OptNilString {
	if value == nil {
		return gcpgenserver.OptNilString{}
	}
	return gcpgenserver.NewOptNilString(*value)
}

func SafeFloat64(value *float64) gcpgenserver.OptNilFloat64 {
	if value == nil {
		return gcpgenserver.OptNilFloat64{}
	}
	return gcpgenserver.NewOptNilFloat64(*value)
}

func SafeTime(value *strfmt.DateTime) gcpgenserver.OptNilDateTime {
	if value == nil {
		return gcpgenserver.OptNilDateTime{}
	}
	return gcpgenserver.NewOptNilDateTime(time.Time(*value))
}

func SafeInt32(value *int32) gcpgenserver.OptNilInt32 {
	if value == nil {
		return gcpgenserver.OptNilInt32{}
	}
	return gcpgenserver.NewOptNilInt32(*value)
}

func SafeInt64(value *int64) gcpgenserver.OptNilInt64 {
	if value == nil {
		return gcpgenserver.OptNilInt64{}
	}
	return gcpgenserver.NewOptNilInt64(*value)
}

func SafeInt64ToInt32(value *int64) gcpgenserver.OptNilInt32 {
	if value == nil {
		return gcpgenserver.OptNilInt32{}
	}
	return gcpgenserver.NewOptNilInt32(int32(*value))
}

func SafeBool(value *bool) gcpgenserver.OptNilBool {
	if value == nil {
		return gcpgenserver.NewOptNilBool(false)
	}
	return gcpgenserver.NewOptNilBool(*value)
}

func SafeOptFloat64(value *float64) gcpgenserver.OptFloat64 {
	if value == nil {
		return gcpgenserver.OptFloat64{}
	}
	return gcpgenserver.NewOptFloat64(*value)
}

func SafeOptNilFloat64(value *float64) gcpgenserver.OptNilFloat64 {
	if value == nil {
		return gcpgenserver.OptNilFloat64{}
	}
	return gcpgenserver.NewOptNilFloat64(*value)
}

// _getPairedRegionURI retrieves the URI of the paired region for the given region from a predefined in the configmap.
// Returns an error if the paired regions are not defined or the region is not found in the mapping.
func _getPairedRegionURI(region string) (string, error) {
	if PairedRegions == "" {
		return "", errors.New("paired regions not defined for this region")
	}
	sMap, err := ConvertStringToMap(PairedRegions)
	if err != nil {
		return "", err
	}

	uri, ok := sMap[region]
	if !ok {
		return "", errors.New("region not found in paired regions list")
	}
	return uri, nil
}

func _getVolumeUriFromCcfeUri(uri string) string {
	uriMap, err := CFFEURIToMap(uri)
	if err != nil {
		// Return empty string if parsing fails
		return ""
	}

	volumeName := uriMap["volumes"]
	projects := uriMap["projects"]
	locations := uriMap["locations"]

	volumeUri := fmt.Sprintf("projects/%s/locations/%s/volumes/%s", projects, locations, volumeName)
	return volumeUri
}

// _convertStringToMap converts a JSON-formatted string into a map[string]string.
// It expects the input string to be a valid JSON object with string keys and values.
// Returns an error if the JSON is invalid or cannot be unmarshalled into the map.
func _convertStringToMap(s string) (map[string]string, error) {
	var mapSlice map[string]string
	sMapBytes := []byte(s)
	err := json.Unmarshal(sMapBytes, &mapSlice)
	if err != nil {
		return nil, errors.New("error when unmarshalling response")
	}
	return mapSlice, nil
}

func generateJitter() time.Duration {
	return jitterBase * time.Duration(GenerateRandomInRange(100)+100) // [100, 200] ms jitter
}

// _sleep is a helper function to sleep for a given duration according to the error type
func _sleep(retryDelay time.Duration, err error, attempt int) {
	// Retry in Exponential backoff way for error codes defined in exponentialBackOffErrors
	_, httpcode := err.(*errs.CustomError).GetHttpCode()
	if ContainsInt(exponentialBackOffErrors, httpcode) {
		var nextRetry int64 = 1 << attempt
		retryDelay = min(maxExpBackOffDelay, time.Duration(nextRetry)*retryDelay+generateJitter())
	}
	time.Sleep(retryDelay)
}

// RetrierOnCodes retries the function fn on specific HTTP error codes.
func RetrierOnCodes(logger log.Logger, fn func() error, retryCodes []int, maxRetries int, retryDelay time.Duration) error {
	shouldSleep := false
	var err error
	for i := 0; i < maxRetries; i++ {
		if shouldSleep {
			sleep(retryDelay, err, i)
			logger.Debug("Retrying function", "attempt", i+1)
		}
		shouldSleep = true
		err = fn()
		if err != nil {
			customErr := err.(*errs.CustomError)
			_, httpcode := customErr.GetHttpCode()
			// Get original error message once
			originalErrMsg := ""
			if customErr.OriginalErr != nil {
				originalErrMsg = customErr.OriginalErr.Error()
			}

			if httpcode == 429 {
				quotaLimitExceededMatch := quotaLimitExceededRegex.FindStringSubmatch(err.(*errs.CustomError).GetMessage())
				if quotaLimitExceededMatch != nil {
					return err
				}
			}

			if ContainsInt(retryCodes, httpcode) {
				logger.Errorf("Got a retryable error code while calling server: %s, attempt: %d, httpCode: %d, originalError: %s", err.Error(), i+1, httpcode, originalErrMsg)
				continue
			}

			innerErr := customErr.Unwrap()
			if innerErr != nil {
				if goerrors.Is(innerErr, syscall.ECONNREFUSED) || goerrors.Is(innerErr, syscall.ETIMEDOUT) {
					logger.Warnf("Got a connection error while calling server: %s, attempt: %d, originalError: %s", err.Error(), i+1, originalErrMsg)
					continue
				}
				if neterror, ok := innerErr.(net.Error); ok && neterror.Timeout() {
					logger.Warnf("Got a timeout error while calling server: %s, attempt: %d, originalError: %s", err.Error(), i+1, originalErrMsg)
					continue
				}
			}

			logger.Errorf("Got a non-retryable error while calling server: %s, attempt: %d, httpCode: %d, originalError: %s", err.Error(), i+1, httpcode, originalErrMsg)
			return err
		}
		return err
	}
	return err
}

func _convertBytesToGib(bytes float64) int64 {
	gib := bytes / 1024 / 1024 / 1024

	return int64(math.Round(gib))
}

func RemovePrefix(str string, prefix string) string {
	if strings.HasPrefix(str, prefix) {
		return strings.TrimPrefix(str, prefix)
	}
	return str
}

func GetTimeNow() time.Time {
	return time.Now()
}

func GetCoRelationIDFromContext(ctx context.Context) string {
	if header, ok := ctx.Value(middleware.CorrelationContextKey).(http.Header); ok {
		return header.Get(string(middleware.CorrelationIDName))
	} else if fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if _, ok := fields[string(middleware.RequestCorrelationID)]; !ok {
			// If the correlation ID is not present in the fields, generate a new one
			correlationID := RandomUUID()
			fields[string(middleware.RequestCorrelationID)] = correlationID
			return correlationID
		}

		return fields[string(middleware.RequestCorrelationID)].(string)
	}
	// Check if correlation ID is directly stored in context (new pattern)
	if corrID, ok := ctx.Value(middleware.CorrelationContextKey).(string); ok {
		return corrID
	}
	return ""
}

// _validateStrongPassword checks that password satisfies the same complexity policy as GenerateStrongPassword:
// minimum length, letters+digits only, and at least one lowercase, uppercase, and digit.
func _validateStrongPassword(password string) error {
	if len(password) < MinStrongPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinStrongPasswordLength)
	}

	var hasLower, hasUpper, hasDigit bool
	for i := 0; i < len(password); i++ {
		c := password[i]
		switch {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			return fmt.Errorf("password must contain only letters and digits")
		}
	}
	if !hasLower || !hasUpper || !hasDigit {
		return fmt.Errorf("password must contain at least one lowercase letter, one uppercase letter, and one digit")
	}
	return nil
}

func _generateStrongPassword(length int) (string, error) {
	if length < MinStrongPasswordLength {
		return "", fmt.Errorf("password length should be at least %d characters", MinStrongPasswordLength)
	}

	password := make([]byte, length)

	// Ensure the password contains at least one character from each category
	for i := 0; i < len(strongPasswordCharCategories); i++ {
		char, err := randomCharFrom(strongPasswordCharCategories[i])
		if err != nil {
			return "", err
		}
		password[i] = char
	}

	// Fill the remaining characters randomly
	for i := len(strongPasswordCharCategories); i < length; i++ {
		char, err := randomCharFrom(strongPasswordAllChars)
		if err != nil {
			return "", err
		}
		password[i] = char
	}

	// Shuffle the password to ensure randomness
	shuffle(password)

	return string(password), nil
}

func randomCharFrom(chars string) (byte, error) {
	maxValue := big.NewInt(int64(len(chars)))
	n, err := rand.Int(rand.Reader, maxValue)
	if err != nil {
		return 0, err
	}
	return chars[n.Int64()], nil
}

func shuffle(data []byte) {
	for i := len(data) - 1; i > 0; i-- {
		j, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		data[i], data[j.Int64()] = data[j.Int64()], data[i]
	}
}

func ConvertJsonToModel(jsonb []byte, model any) error {
	err := json.Unmarshal(jsonb, &model)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to unmarshal json: %v", err))
	}

	return nil
}

func RemapJSON(src any, model any) error {
	jsonb, err := json.Marshal(src)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to marshal json: %v", err))
	}

	err = json.Unmarshal(jsonb, model)
	if err != nil {
		return errors.New(fmt.Sprintf("Failed to unmarshal json: %v", err))
	}

	return nil
}

// SplitIntSliceIntoChunks splits the given slice into multiple slices of length lim
func SplitIntSliceIntoChunks(buf []int64, lim int) [][]int64 {
	var chunk []int64
	chunks := make([][]int64, 0, (len(buf)/lim)+1)
	for len(buf) >= lim {
		chunk, buf = buf[:lim], buf[lim:]
		chunks = append(chunks, chunk)
	}
	if len(buf) > 0 {
		chunks = append(chunks, buf[:])
	}
	return chunks
}

// SplitStringSliceIntoChunks splits the given slice into multiple slices of length lim
func SplitStringSliceIntoChunks(buf []string, lim int) [][]string {
	var chunk []string
	chunks := make([][]string, 0, (len(buf)/lim)+1)
	for len(buf) >= lim {
		chunk, buf = buf[:lim], buf[lim:]
		chunks = append(chunks, chunk)
	}
	if len(buf) > 0 {
		chunks = append(chunks, buf[:])
	}
	return chunks
}

func GetRequestIDFromContext(ctx context.Context) string {
	if fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if requestID, ok := fields[string(middleware.RequestID)]; ok {
			return requestID.(string)
		}
	}
	return ""
}

// GetAuthTokenFromContext gets the JWT token from the context
func GetAuthTokenFromContext(ctx context.Context) string {
	if token, ok := ctx.Value(middleware.AuthorizationToken).(string); ok {
		return token
	}
	return ""
}

// GetCVPJWTFromContext gets the JWT token from the context
func GetCVPJWTFromContext(ctx context.Context) string {
	jwtToken := GetAuthTokenFromContext(ctx)
	if jwtToken == "" {
		jwtToken = GetJWTTokenFromContext(ctx)
	}
	return jwtToken
}

// GetResourcesNameForBackup generates unique service account name, email, and bucket name
func GetResourcesNameForBackup(gcpRegion, tenantProjectRegion, tenantProjectNumber, backupVaultUUID string) (email, bucketName, serviceAccountId string, err error) {
	const maxServiceAccountLength = 30
	const maxBucketNameLength = 60

	// Generate a deterministic hash based on BackupVault+TenantProjectNumber+TenantProjectRegion combination
	combinedInput := fmt.Sprintf("%s-%s-%s", backupVaultUUID, tenantProjectNumber, tenantProjectRegion)
	hash := sha256.Sum256([]byte(combinedInput))
	// Use first 6 characters of hex encoding for deterministic "random" code
	randCode := hex.EncodeToString(hash[:3]) // 3 bytes = 6 hex chars

	// Generate service account ID
	baseServiceAccountId := "vsa-backup-" + sliceRegionForServiceAccount(gcpRegion)
	if len(baseServiceAccountId)+len(randCode) > maxServiceAccountLength {
		baseServiceAccountId = baseServiceAccountId[:maxServiceAccountLength-len(randCode)]
	}
	serviceAccountId = baseServiceAccountId + randCode

	// Generate email
	email = fmt.Sprintf("%s@%s.iam.gserviceaccount.com", serviceAccountId, tenantProjectNumber)

	// Generate bucket name
	baseBucketName := fmt.Sprintf("vsa-backup-%s", backupVaultUUID)
	if len(baseBucketName)+1+len(randCode) > maxBucketNameLength {
		baseBucketName = baseBucketName[:maxBucketNameLength-1-len(randCode)]
	}
	bucketName = baseBucketName + "-" + randCode

	return email, bucketName, serviceAccountId, nil
}

// sliceRegionForServiceAccount ensures the region part of the service account name is within limits
func sliceRegionForServiceAccount(region string) string {
	const maxRegionLength = 25
	if len(region) > maxRegionLength {
		return region[:maxRegionLength]
	}
	return region
}

// generateRandomString generates a random alphanumeric string of the given length
func _generateRandomString(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[n.Int64()]
	}
	return string(result), nil
}

func GetLunName(volumeName string) string {
	return "lun_" + volumeName
}

// ExtractLunNameFromPath extracts the LUN name from a full LUN name string. eg: "/vol/volume1752243551/lun_volume1752243551" to "lun_volume1752243551"
func ExtractLunNameFromPath(fullLunName string) string {
	trimmed := strings.TrimSpace(fullLunName)
	if trimmed == "" {
		return ""
	}
	// Use filepath.Base to get the last component of the path
	return filepath.Base(trimmed)
}

func IsTransitionalState(state string) bool {
	transitionalStates := map[string]struct{}{
		datamodel.LifeCycleStateCreating:  {},
		datamodel.LifeCycleStateUpdating:  {},
		datamodel.LifeCycleStateDeleting:  {},
		datamodel.LifeCycleStateReverting: {},
		datamodel.LifeCycleStateSplitting: {},
	}
	_, exists := transitionalStates[state]
	return exists
}

var compiledRegex = regexp.MustCompile(ReplicationUriRegex)
var compiledOperationRegex = regexp.MustCompile(OperationUriRegex)

func _validateCcfeReplicationUri(uri string) error {
	uriList := strings.Split(uri, "/")
	if len(uriList) < 7 {
		return fmt.Errorf("replicationURIs should match %s", ReplicationUriRegex)
	}

	valid := compiledRegex.MatchString(uri)
	if !valid {
		return fmt.Errorf("replicationURIs should match %s", ReplicationUriRegex)
	}

	return nil
}

func _validateOperationUri(uri string) (string, error) {
	uriList := strings.Split(uri, "/")
	if len(uriList) < 8 {
		return "", fmt.Errorf("OperationURIs should match %s", OperationUriRegex)
	}

	valid := compiledOperationRegex.MatchString(uri)
	if !valid {
		return "", fmt.Errorf("OperationURIs should match %s", OperationUriRegex)
	}

	// Extract operation ID from URI (last part after last slash)
	parts := strings.Split(uri, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1], nil
	}

	return "", fmt.Errorf("OperationURIs should match %s", OperationUriRegex)
}

// CFFEURIToMap Takes CFFEURI and returns a map of that string split by /
func CFFEURIToMap(uri string) (map[string]string, error) {
	out := make(map[string]string)
	// URI must be in the format /projects/project_number/locations/locationid/volumes/volume_name/replications/replication_name
	// validate the URI
	err := ValidateCcfeReplicationUri(uri)
	if err != nil {
		return out, err
	}

	uriSlice := strings.Split(uri, "/")
	out[uriSlice[0]] = uriSlice[1]
	out[uriSlice[2]] = uriSlice[3]
	out[uriSlice[4]] = uriSlice[5]
	out[uriSlice[6]] = uriSlice[7]
	return out, nil
}

func _parseProjectNumberFromURI(uri string) (string, error) {
	uriMap, err := CFFEURIToMap(uri)
	if err != nil {
		return "", err
	}

	return uriMap["projects"], nil
}

// _getReplicationNameFromURI extracts the replication name from a CFFE URI
// URI format: "projects/45110233509/locations/australia-southeast1-a/volumes/mrasrc1255/replications/replicationtest581"
func _getReplicationNameFromURI(uri string) (string, error) {
	uriMap, err := CFFEURIToMap(uri)
	if err != nil {
		return "", err
	}

	return uriMap["replications"], nil
}

// LoadJsonFromFile reads a JSON file from the given path and unmarshals its contents into the provided variable v.
// The generic type T allows unmarshalling into any Go type.
// Returns an error if the file does not exist, cannot be read, or if unmarshalling fails.
func LoadJsonFromFile[T any](path string, v *T) error {
	_, err := os.Stat(path)
	if !os.IsNotExist(err) {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		err = json.Unmarshal(data, &v)
		if err != nil {
			return err
		}
		return nil
	}

	return err
}

func GetEncryptionType(kmsConfigId *string) string {
	var encryptionType string
	if nillable.IsNilOrEmpty(kmsConfigId) {
		encryptionType = "SERVICE_MANAGED"
	} else {
		encryptionType = "CLOUD_KMS"
	}
	return encryptionType
}

func GetSMCSecretName() string {
	return env.GetString("GCP_SMC_SECRET_NAME", "")
}

func GetSMCSecretVersionName() string {
	return env.GetString("GCP_SMC_SECRET_VERSION_NAME", "latest")
}

func _renameSnapshotName(name string) string {
	// Snapmirror names are 76 chars from Ontap but Callback api only supports 64 chars
	if strings.HasPrefix(name, "snapmirror.") {
		nameArr := strings.Split(name, ".")
		dateTime := nameArr[len(nameArr)-1]
		name = fmt.Sprintf("replication-%s", dateTime)
	}

	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")

	if strings.HasPrefix(name, "weekly-on-") {
		name = ConvertToGcpResourceName(name)
	}

	if strings.HasPrefix(name, "monthly-on-") {
		name = ConvertToGcpResourceName(name)
	}

	name = strings.ReplaceAll(name, "+", "-")

	return name
}

// _convertToGcpResourceName Function which check if given string matches with the 1p format and if not then return date stamp
func _convertToGcpResourceName(name string) string {
	is1pCompliant := CheckForGcpNamingConvention(name)
	var final string
	if !is1pCompliant {
		res := []string{}
		nameArr := strings.Split(name, "-")
		// Extracting date stamp i.e 2023-12-01-1310
		for i := len(nameArr) - 4; i <= len(nameArr)-1; i++ {
			res = append(res, nameArr[i])
		}
		final = strings.Join(res, "-")
		switch nameArr[0] {
		case "weekly":
			final = fmt.Sprintf("weekly-%s", final)
		case "monthly":
			final = fmt.Sprintf("monthly-%s", final)
		}
		return final
	} else {
		return name
	}
}

// _checkForGcpNamingConvention Checks if a given string follows the GCP naming convention using a regular expression.
func _checkForGcpNamingConvention(entry string) bool {
	matched, err := regexp.MatchString(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`, entry)
	if err != nil {
		return false
	}
	return matched
}

func _getLocation(snapshot datamodel.Snapshot) string {
	var location string

	if snapshot.Volume == nil || snapshot.Volume.Pool == nil || snapshot.Volume.Pool.PoolAttributes == nil {
		return ""
	}

	isRegionalHA := snapshot.Volume.Pool.PoolAttributes.IsRegionalHA
	if isRegionalHA {
		zone := snapshot.Volume.Pool.PoolAttributes.SecondaryZone
		if zone == "" {
			zone = snapshot.Volume.Pool.PoolAttributes.PrimaryZone
		}
		parts := strings.Split(zone, "-")
		if len(parts) < 3 {
			return zone
		}
		location = strings.Join(parts[:len(parts)-1], "-")
	} else {
		location = snapshot.Volume.Pool.PoolAttributes.PrimaryZone
	}
	return location
}

func _getBackupRegion(volume *datamodel.Volume) (string, error) {
	if volume == nil || volume.Pool == nil || volume.Pool.PoolAttributes == nil {
		return "", errors.New("Volume or Pool Attributes is nil when extracting backup region")
	}
	region, _, err := ParseRegionAndZone(volume.Pool.PoolAttributes.PrimaryZone)
	if err != nil {
		return "", err
	}
	return region, nil
}

func GetHgUUIDs(hgDetails []datamodel.HostGroupDetail) []string {
	var uuids []string
	for _, detail := range hgDetails {
		uuids = append(uuids, detail.HostGroupUUID)
	}
	return uuids
}

func GetArrayDiff(existingList []string, newList []string) ([]string, []string) {
	toCreate := make([]string, 0)
	toDelete := make([]string, 0)
	for _, newItem := range newList {
		if !ContainsString(existingList, newItem) {
			toCreate = append(toCreate, newItem)
		}
	}

	for _, existingItem := range existingList {
		if !ContainsString(newList, existingItem) {
			toDelete = append(toDelete, existingItem)
		}
	}
	return toCreate, toDelete
}

// _parsePEMCertificate takes a PEM-encoded certificate string and returns a CertPool containing the certificate.
func _parsePEMCertificate(pemCerts []string, typeOfCertificate string) (*x509.CertPool, error) {
	byteCert := x509.NewCertPool()

	for _, pemCert := range pemCerts {
		// Convert the PEM-encoded certificate string to bytes
		certBytes := []byte(pemCert)

		// Parse the PEM block
		var block *pem.Block
		var rest = certBytes
		var cert []byte

		for {
			block, rest = pem.Decode(rest)
			if block == nil {
				break
			}
			if block.Type == typeOfCertificate {
				cert = block.Bytes
				break
			}
		}

		if cert == nil {
			return nil, errors.New("Failed to parse certificate")
		}

		// Create a CertPool and add the parsed certificate
		if !byteCert.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: typeOfCertificate, Bytes: cert})) {
			return nil, errors.New("Failed to append certificate to cert pool")
		}
	}
	return byteCert, nil
}

// GetVPCNameFromSubnetID extracts the VPC name from a given vendor subnet ID.
func GetVPCNameFromSubnetID(vendorSubNetID string) string {
	parts := strings.Split(vendorSubNetID, "/")
	return parts[len(parts)-1]
}

// Given an accountID and projectID, return the serviceAccountEmail to use.
func ConstructServiceAccountEmail(accountID string, projectID string) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", accountID, projectID)
}

// GenerateOperationURL generates the formatted URL
func GenerateOperationURL(projectNumber, locationId, operationID string) string {
	return fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", projectNumber, locationId, operationID)
}

// ParseCommaSeparatedStringToMap parses a comma-separated string into a map[string]struct{}
func ParseCommaSeparatedStringToMap(input string) map[string]struct{} {
	if input == "" {
		return make(map[string]struct{})
	}

	items := SplitAndTrimString(input, ",")
	parsedItems := make(map[string]struct{}, len(items))

	for _, item := range items {
		parsedItems[item] = struct{}{}
	}

	return parsedItems
}

// SplitAndTrimString splits input by delimiter, trims each token, and drops empty tokens.
func SplitAndTrimString(input, delimiter string) []string {
	if input == "" {
		return []string{}
	}

	items := strings.Split(input, delimiter)
	parsedItems := make([]string, 0, len(items))
	for _, item := range items {
		trimmedItem := strings.TrimSpace(item)
		if trimmedItem != "" {
			parsedItems = append(parsedItems, trimmedItem)
		}
	}
	return parsedItems
}

// DefaultString returns fallback when value is empty.
func DefaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// TrimOrNil trims whitespace from a string pointer and returns nil when the input is nil or empty after trimming.
func TrimOrNil(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// TrimOrEmpty trims whitespace from a string pointer and returns an empty string when the input is nil.
func TrimOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// BoolOrFalse returns the bool value when the pointer is non-nil; otherwise false.
func BoolOrFalse(value *bool) bool {
	return value != nil && *value
}

// IsFileProtocolSupported returns true only if the file protocol support flag is enabled
// and the provided accountID is in the allowlisted accounts config map array.
// If no allowlisted accounts are configured, it returns false even if the flag is enabled.
func IsFileProtocolSupported(accountID string) bool {
	// First check if the flag is enabled
	if !FileProtocolSupported {
		return false
	}

	// If no allowlisted accounts are configured, return false
	if len(experimentalVersionAllowlistedAccounts) == 0 {
		return false
	}

	if _, exists := experimentalVersionAllowlistedAccounts[wildCardForAllowlist]; exists {
		if len(experimentalVersionAllowlistedAccounts) == 1 {
			// If the only entry is "*", allow all accounts
			return true
		}
		// If wildcard is mixed with other accounts, it's an invalid configuration
		return false
	}

	// Check if the accountID is in the allowlisted accounts
	// Exact matching (account IDs are typically numbered strings)
	_, exists := experimentalVersionAllowlistedAccounts[accountID]
	return exists
}

// IsCustomHaLargeVolumeAllowlisted returns true when accountID is listed in EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS,
// allowing LvHaPairsForLargeVolume to use EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY (default 12 HA pairs / 24 nodes).
func IsCustomHaLargeVolumeAllowlisted(accountID string) bool {
	if len(experimentalCustomHaLargeVolumeAllowlistedAccounts) == 0 {
		return false
	}
	_, exists := experimentalCustomHaLargeVolumeAllowlistedAccounts[accountID]
	return exists
}

// LvHaPairsForLargeVolume returns the HA pair count for large-volume logic: defaultHAPairs (typically from
// NUMBER_OF_HA_PAIRS_LARGE_CAPACITY) when the account is not in EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS;
// otherwise EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY (default 12, i.e. 24-node cluster).
func LvHaPairsForLargeVolume(accountID string, defaultHAPairs int64) int64 {
	if !IsCustomHaLargeVolumeAllowlisted(accountID) {
		return defaultHAPairs
	}
	return env.GetInt64("EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", 12)
}

// IsAccountAllowlisted returns true if the provided accountID is in the allowlisted accounts config map.
// This is separate from file support checks and is used for image selection.
func IsAccountAllowlisted(accountID string) bool {
	// If no allowlisted accounts are configured, return false
	if len(experimentalVersionAllowlistedAccounts) == 0 {
		return false
	}

	// Check if the accountID is in the allowlisted accounts
	// Exact matching (account IDs are typically numbered strings)
	_, exists := experimentalVersionAllowlistedAccounts[accountID]
	return exists
}

// IsFileProtocolSupportedV2 returns true if file protocol support is enabled based on:
// 1. FileProtocolSupported flag is enabled
// 2. The cluster's ONTAP version is known and well-formed
// Every supported ONTAP version now provides file protocols, so no minimum version
// is compared. The version is still required because an unknown version means the
// cluster's capabilities are unknown, which must not be treated as file-capable.
// This version does not check account allowlisting, only the flag and ONTAP version.
// Note: Callers are expected to pass already-extracted versions.
func IsFileProtocolSupportedV2(ontapVersion string) bool {
	// First check if the flag is enabled
	if !FileProtocolSupported {
		return false
	}

	// Check if ONTAP version is provided
	if ontapVersion == "" {
		return false
	}

	// Validate that the version matches the expected format (callers pass extracted versions)
	return ontapVersionRegex.MatchString(ontapVersion)
}

// GetOntapVersionBasedOnAllowlisting returns the appropriate ONTAP version based on account allowlisting.
// If the account is allowlisted and experimental ONTAP version is configured, returns experimental version.
// Otherwise, returns the current/default ONTAP version.
func GetOntapVersionBasedOnAllowlisting(accountID string) string {
	// Check if experimental version is configured
	if env.ExperimentalOntapVersionDetails == "" {
		return env.CurrentOntapVersionDetails
	}

	if IsAccountAllowlisted(accountID) {
		return env.ExperimentalOntapVersionDetails
	}

	return env.CurrentOntapVersionDetails
}

// IsProberProject checks if the given project number is a prober project by search it in PROBER_PROJECT_LIST.
func IsProberProject(projectNumber string) bool {
	// Check if the project number is in the allowlisted prober projects
	_, exists := isProberProject[projectNumber]
	return exists
}

// SetFileProtocolSupportedForTesting is a test helper function that allows tests to set
// the file protocol support flag by setting the environment variable.
// This should only be used in tests.
func SetFileProtocolSupportedForTesting(enabled bool) {
	err := os.Setenv("FILES_PROTOCOL_SUPPORT", strconv.FormatBool(enabled))
	if err != nil {
		return
	}
	// Re-read the environment variable to update the cached value
	FileProtocolSupported = env.GetBool("FILES_PROTOCOL_SUPPORT", false)
}

// EnableAllSquashForTesting is a test helper function that allows tests to enable
// the allSquash support flag by setting the environment variable.
// This should only be used in tests.
func EnableAllSquashForTesting(enabled bool) {
	err := os.Setenv("IS_ALL_SQUASH_ENABLED", strconv.FormatBool(enabled))
	if err != nil {
		return
	}
	// Re-read the environment variable to update the cached value
	IsAllSquashEnabled = env.GetBool("IS_ALL_SQUASH_ENABLED", true)
}

// SetExperimentalVersionAllowlistedAccountsForTesting is a test helper function that allows tests to set
// the allowlisted accounts by setting the environment variable.
// This should only be used in tests.
func SetExperimentalVersionAllowlistedAccountsForTesting(accounts string) {
	err := os.Setenv("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", accounts)
	if err != nil {
		return
	}
	// Re-parse the accounts to update the cached value
	experimentalVersionAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_VERSION_ALLOWLISTED_ACCOUNTS", ""))
}

// SetExperimentalCustomHaLargeVolumeAllowlistedAccountsForTesting sets EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS for tests.
func SetExperimentalCustomHaLargeVolumeAllowlistedAccountsForTesting(accounts string) {
	_ = os.Setenv("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", accounts)
	experimentalCustomHaLargeVolumeAllowlistedAccounts = ParseCommaSeparatedStringToMap(env.GetString("EXPERIMENTAL_CUSTOM_HA_LARGE_VOLUME_ALLOWLISTED_ACCOUNTS", ""))
}

func GetSnHostProject(pool *datamodel.Pool) string {
	if pool == nil {
		return ""
	}
	return pool.SnHostProject
}

// SetRestoreVolumeBufferEnabledForTesting is a test helper function that allows tests to set
// the restore volume buffer flag by setting the environment variable.
// This should only be used in tests.
func SetRestoreVolumeBufferEnabledForTesting(enabled bool) {
	err := os.Setenv("RESTORE_VOLUME_BUFFER_ENABLED", strconv.FormatBool(enabled))
	if err != nil {
		return
	}
	// Re-read the environment variable to update the cached value
	RestoreVolumeBufferEnabled = env.GetBool("RESTORE_VOLUME_BUFFER_ENABLED", false)
}

// _calculateRequiredVolumeSize calculates the required volume size based on backup size
// If RESTORE_VOLUME_BUFFER_ENABLED is true or Not SAN Protocol, returns 20% more than backup size
// Otherwise, returns ceil of backup size + 1 GiB
func _calculateRequiredVolumeSize(backupSizeInBytes int64, backupAttribute datamodel.BackupAttributes) int64 {
	if RestoreVolumeBufferEnabled || !IsSanProtocols(backupAttribute.Protocols) {
		// 20% more than backup size
		return int64(float64(backupSizeInBytes) * 1.20)
	}
	// ceil of backup size + 1 GiB
	return int64(math.Ceil(float64(backupSizeInBytes+GiBInBytes)/float64(GiBInBytes))) * GiBInBytes
}

// GetLocationFromVendorID extracts the location from a vendor ID.
func GetLocationFromVendorID(vendorID string) (string, error) {
	// vendorID is in the format: "/projects/project123/locations/location123/pools/pool123"
	parts := strings.Split(vendorID, "/")

	if len(parts) != 7 {
		return "", errors.NewUserInputValidationErr("invalid vendor ID, expected format: /projects/{project}/locations/{location}/pools/{pool}, found: " + vendorID)
	}

	return parts[len(parts)-3], nil
}

// GetCorrelationIDFromWorkflowContextLoggerFields retrieves the correlation ID from the workflow context logger fields.
func GetCorrelationIDFromWorkflowContextLoggerFields(ctx workflow.Context) (string, error) {
	if fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if _, ok := fields[string(middleware.RequestCorrelationID)]; !ok {
			return "", fmt.Errorf("no correlation ID found in context")
		}

		return fields[string(middleware.RequestCorrelationID)].(string), nil
	} else {
		return "", fmt.Errorf("correlation ID not found in workflow context logger")
	}
}

func GetOpcWorkRequestIDFromWorkflowContextLoggerFields(ctx workflow.Context) (string, error) {
	if fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		val, ok := fields[string(middleware.RequestOpcWorkRequestID)]
		if !ok {
			return "", fmt.Errorf("no opc-work-request-id found in context")
		}
		id, ok := val.(string)
		if !ok {
			return "", fmt.Errorf("opc-work-request-id in context is not a string")
		}
		return id, nil
	}
	return "", fmt.Errorf("opc-work-request-id not found in workflow context logger")
}

// IsImmutableBackupEnabled returns whether immutable backup validation is enabled
func IsImmutableBackupEnabled() bool {
	return immutableBackupEnabled
}

// SetImmutableBackupEnabledForTest allows tests to override the immutable backup feature flag
// This should only be used in tests
func SetImmutableBackupEnabledForTest(enabled bool) {
	immutableBackupEnabled = enabled
}

// IsCrossRegionBackupEnabled returns whether cross-region backup operations are enabled
func IsCrossRegionBackupEnabled() bool {
	return crossRegionBackupEnabled
}

// SetCrossRegionBackupEnabledForTest allows tests to override the cross-region backup feature flag
// This should only be used in tests
func SetCrossRegionBackupEnabledForTest(enabled bool) {
	crossRegionBackupEnabled = enabled
}

// SetEnableBackupVaultSwitchingForTest allows tests to override the backup vault switching feature flag
// This should only be used in tests
func SetEnableBackupVaultSwitchingForTest(enabled bool) {
	EnableBackupVaultSwitching = enabled
}

// SetEnableGCNVVaultSwitchingForTest allows tests to override the GCNV vault switching feature flag.
// This should only be used in tests.
func SetEnableGCNVVaultSwitchingForTest(enabled bool) {
	EnableGCNVVaultSwitching = enabled
}

// GetSourceVolumePathFromBackup gets the source volume path from a backup object
func _getSourceVolumePathFromBackup(backup *datamodel.Backup) string {
	var sourceVolumeZone string
	if backup.Attributes.IsRegionalHA || backup.Attributes.SourceVolumeZone == "" {
		sourceVolumeZone = *backup.BackupVault.SourceRegionName
	} else {
		sourceVolumeZone = backup.Attributes.SourceVolumeZone
	}
	accountIdentifier := backup.Attributes.AccountIdentifier
	if backup.BackupVault != nil &&
		backup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject &&
		backup.Attributes.VolumeAccountName != "" {
		accountIdentifier = backup.Attributes.VolumeAccountName
	}
	return fmt.Sprintf("projects/%s/locations/%s/volumes/%s",
		accountIdentifier,
		sourceVolumeZone,
		backup.Attributes.VolumeName)
}

// GetSourceSnapshotPathFromBackup gets the source snapshot path from a backup object
func _getSourceSnapshotPathFromBackup(backup *datamodel.Backup) string {
	var sourceVolumeZone string
	if backup.Attributes.IsRegionalHA || backup.Attributes.SourceVolumeZone == "" {
		sourceVolumeZone = *backup.BackupVault.SourceRegionName
	} else {
		sourceVolumeZone = backup.Attributes.SourceVolumeZone
	}
	accountIdentifier := backup.Attributes.AccountIdentifier
	if backup.BackupVault != nil &&
		backup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject &&
		backup.Attributes.VolumeAccountName != "" {
		accountIdentifier = backup.Attributes.VolumeAccountName
	}
	return fmt.Sprintf("projects/%s/locations/%s/volumes/%s/snapshots/%s",
		accountIdentifier,
		sourceVolumeZone,
		backup.Attributes.VolumeName,
		RenameSnapshotName(backup.Attributes.SnapshotName))
}

// IsFilesProtocol checks if the protocol is NFSv3 or NFSv4 or SMB
func IsFilesProtocol(protocolName string) bool {
	return protocolName == string(gcpgenserver.ProtocolsV1betaNFSV3) || protocolName == string(gcpgenserver.ProtocolsV1betaNFSV4) || protocolName == string(gcpgenserver.ProtocolsV1betaSMB)
}

func GetNLFSecretPath() string {
	secretUri := ""
	if env.NLFLicenseSecretPath != "" && env.SecretManagerProjectID != "" {
		secretUri = fmt.Sprintf("projects/%s/secrets/%s", env.SecretManagerProjectID, env.NLFLicenseSecretPath)
	}
	return secretUri
}

func ExtractOntapVersion(input string) string {
	match := ontapVersionRegex.FindString(input)
	return match
}

// ExtractOntapRBACVersion extracts the ONTAP version token from host release strings for
// RBAC bucket path resolution (e.g. "NetApp Release 9.17.1P9X3: ..." -> "9.17.1P9X3").
// It supports additional suffix formats (P+X, X-only, D, RC) that ExtractOntapVersion omits.
func ExtractOntapRBACVersion(input string) string {
	match := hardwareOntapVersionRegex.FindString(input)
	return match
}

// CompareOntapVersion compares two ONTAP version strings.
// Returns:
//   - 1 if version1 > version2
//   - 0 if version1 == version2
//   - -1 if version1 < version2
//
// Handles versions like "9.17.1", "9.18.1", "9.18.1P2", "9.18.1X29", etc.
// Patch levels (P2, P3, X29, etc.) are ignored for comparison purposes.
//
// Retained as a general-purpose comparator alongside IsOntapVersionGreaterOrEqual; neither has a
// production caller since file protocol support stopped being gated on a minimum ONTAP version.
func CompareOntapVersion(version1, version2 string) int {
	// Extract base versions (remove patch suffixes like P2)
	v1 := ExtractOntapVersion(version1)
	v2 := ExtractOntapVersion(version2)

	if v1 == "" || v2 == "" {
		return 0 // Can't compare if extraction failed
	}

	// Strip patch levels (P2, P3, X29, etc.) for comparison purposes
	// We only want the base version (e.g., "9.18.1" from "9.18.1P2" or "9.18.1X29")
	// Check for both "P" and "X" patch formats
	if idx := strings.IndexAny(v1, "PXD"); idx != -1 {
		v1 = v1[:idx]
	}
	if idx := strings.IndexAny(v2, "PXD"); idx != -1 {
		v2 = v2[:idx]
	}

	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxParts := 3
	if len(parts1) < maxParts {
		maxParts = len(parts1)
	}
	if len(parts2) < maxParts {
		maxParts = len(parts2)
	}

	for i := 0; i < maxParts; i++ {
		num1, err1 := strconv.Atoi(parts1[i])
		num2, err2 := strconv.Atoi(parts2[i])
		if err1 != nil || err2 != nil {
			return 0 // On error, return 0 (equal)
		}
		if num1 < num2 {
			return -1
		}
		if num1 > num2 {
			return 1
		}
	}

	// If all compared parts are equal, a version with fewer parts is considered less
	if len(parts1) < len(parts2) {
		return -1
	}
	if len(parts1) > len(parts2) {
		return 1
	}
	return 0
}

// IsOntapVersionGreaterOrEqual checks if the given ONTAP version is greater than or equal to the target version.
//
// Retained as a generic, tested utility. It has no production callers since file protocol support
// stopped being gated on a minimum ONTAP version, but it stays available so a future version
// comparison reuses this rather than reintroducing an ad-hoc check at the call site.
func IsOntapVersionGreaterOrEqual(version, targetVersion string) bool {
	return CompareOntapVersion(version, targetVersion) >= 0
}

func ConvertLabelsMapToJSONB(labels map[string]string) *datamodel.JSONB {
	if len(labels) == 0 {
		return nil
	}

	jsonbMap := make(datamodel.JSONB)
	for key, value := range labels {
		jsonbMap[key] = value
	}

	return &jsonbMap
}

// ConvertTimeToOptDateTime converts *time.Time to OptDateTime
func ConvertTimeToOptDateTime(t *time.Time) oasgenserver.OptDateTime {
	if t == nil {
		return oasgenserver.OptDateTime{}
	}
	return oasgenserver.NewOptDateTime(*t)
}

// ConvertStringToOptString converts string to OptString
func ConvertStringToOptString(s string) oasgenserver.OptString {
	if s == "" {
		return oasgenserver.OptString{}
	}
	return oasgenserver.NewOptString(s)
}

// ComparePointerStringSlices checks if two slices, one of string pointers and one of strings, are equal in length and value.
func ComparePointerStringSlices(slice1 []*string, slice2 []string) bool {
	if len(slice1) != len(slice2) {
		return false
	}

	for i := range slice1 {
		if *slice1[i] != slice2[i] {
			return false
		}
	}

	return true
}

// FetchTieringPolicyAsPerVolumeType returns the supported tiering policy depending on the volume type.
func FetchTieringPolicyAsPerVolumeType(fileVolume bool) string {
	if fileVolume {
		return ontapmodels.VolumeInlineTieringPolicyAuto
	}
	return ontapmodels.VolumeInlineTieringPolicySnapshotOnly
}

// GenerateRbacFilePath generates the RBAC file path by replacing the placeholder with the value from the environment variable.
func GenerateRbacFilePath(template, configurablePart string) string {
	// Replace the placeholder with the actual configurable part
	return strings.Replace(template, "%s", configurablePart, 1)
}

func IsRuleKerberosSupported(nFSv4, kerberos5ReadWrite, kerberos5ReadOnly, kerberos5pReadWrite,
	kerberos5pReadOnly, kerberos5iReadOnly, kerberos5iReadWrite bool) bool {
	return enableKerberos && nFSv4 && (kerberos5ReadWrite || kerberos5ReadOnly || kerberos5pReadWrite || kerberos5pReadOnly || kerberos5iReadOnly || kerberos5iReadWrite)
}

// GetONTAPSnapshotNameFromCBSDisplaySnapshotName trims the schedule prefix (daily-, weekly-, monthly-) from the CBS display snapshot name and returns the rest, matching what is stored in the database.
func GetONTAPSnapshotNameFromCBSDisplaySnapshotName(cbsSnapshotName string) string {
	prefixes := []string{"daily.", "weekly.", "monthly.", "snapmirror.", "daily-", "weekly-", "monthly-"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(cbsSnapshotName, prefix) {
			return cbsSnapshotName[len(prefix):]
		}
	}
	return cbsSnapshotName
}

// ExtractSnapshotNameFromCVPBackup extracts the snapshot name from a CVP backup object.
func ExtractSnapshotNameFromCVPBackup(backup *cvpModels.BackupV1beta, backupName string) string {
	if backup.SourceSnapshot != nil && *backup.SourceSnapshot != "" {
		parts := strings.Split(*backup.SourceSnapshot, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	if backup.BackupType == "MANUAL" {
		return backupName
	}

	if backup.BackupType == "SCHEDULED" {
		return GetONTAPSnapshotNameFromCBSDisplaySnapshotName(backupName)
	}

	// Default: return empty string if none of the conditions match
	return ""
}

// DeletedAtOrNil converts gorm.DeletedAt to *time.Time, returning nil if deletedAt is invalid
func DeletedAtOrNil(deletedAt *gorm.DeletedAt) *time.Time {
	if deletedAt != nil && deletedAt.Valid {
		return &deletedAt.Time
	}
	return nil
}

// ConvertJSONBToMap converts a JSONB object to a map[string]string
func ConvertJSONBToMap(jsonb *datamodel.JSONB) map[string]string {
	result := make(map[string]string)
	if jsonb == nil {
		return result
	}

	for k, v := range *jsonb {
		// attempt a type assertion
		if strVal, ok := v.(string); ok {
			result[k] = strVal
		} else {
			// fallback: convert using fmt.Sprintf
			result[k] = fmt.Sprintf("%v", v)
		}
	}
	return result
}

// ConvertSourceBackupVaultNameToRemoteBackupVaultName converts source backup vault name to remote backup vault name
func ConvertSourceBackupVaultNameToRemoteBackupVaultName(sourceBackupVaultName, backupVaultUUID string) string {
	return sourceBackupVaultName[:min(len(sourceBackupVaultName), remoteBackupVaultHydrationNameMaxLength-len(remoteBackupVaultHydrationNamePrefix)-4)] + remoteBackupVaultHydrationNamePrefix + strings.Split(backupVaultUUID, "-")[0][:4]
}

// DeriveSubjectCNFromPEM extracts the subject Common Name from the certificate PEM.
// Use this when installing client-ca/server-ca trust anchors: ONTAP indexes the
// trust store by the CA certificate's subject CN.
func DeriveSubjectCNFromPEM(pemCertificate string) string {
	if pemCertificate == "" {
		return ""
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cert.Subject.CommonName)
}

// DeriveCANameFromPEM extracts the issuer Common Name from the certificate PEM.
// ONTAP derives the same CA name when it installs the cert, and uses it for
// "security ssl modify".
func DeriveCANameFromPEM(pemCertificate string) string {
	if pemCertificate == "" {
		return ""
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cert.Issuer.CommonName)
}

// DNSNamesFromPEM extracts the Subject Alternative Name DNS entries from the certificate PEM.
func DNSNamesFromPEM(pemCertificate string) []string {
	cert, err := parseCertificatePEM(pemCertificate)
	if err != nil {
		return nil
	}
	return cert.DNSNames
}

func parseCertificatePEM(pemCertificate string) (*x509.Certificate, error) {
	if strings.TrimSpace(pemCertificate) == "" {
		return nil, fmt.Errorf("empty certificate PEM")
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	return cert, nil
}

// SerialHexFromPEM extracts the certificate serial number from the PEM and renders
// it as ONTAP expects for "security ssl modify -serial": uppercase hex, no
// separators, zero-padded to an even number of digits.
func SerialHexFromPEM(pemCertificate string) (string, error) {
	if pemCertificate == "" {
		return "", fmt.Errorf("empty certificate PEM")
	}
	block, _ := pem.Decode([]byte(pemCertificate))
	if block == nil {
		return "", fmt.Errorf("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}
	if cert.SerialNumber == nil {
		return "", fmt.Errorf("certificate has no serial number")
	}

	hexSerial := fmt.Sprintf("%X", cert.SerialNumber)
	if len(hexSerial)%2 != 0 {
		hexSerial = "0" + hexSerial
	}
	return hexSerial, nil
}

// SubnetCIDRInAnyRange returns true only when the entire subnet CIDR block is fully contained within
// at least one of the provided address ranges. Both the network address and the last (broadcast) IP
// of the subnet must lie inside the range, ensuring a subnet that straddles a range boundary is
// correctly rejected.
func SubnetCIDRInAnyRange(subnetCIDR string, addressRanges []*datamodel.AddressRange) bool {
	_, subnetNet, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return false
	}
	// Derive the last IP of the subnet: flip all host bits to 1.
	lastIP := make(net.IP, len(subnetNet.IP))
	for i := range subnetNet.IP {
		lastIP[i] = subnetNet.IP[i] | ^subnetNet.Mask[i]
	}
	for _, ar := range addressRanges {
		_, rangeNet, err := net.ParseCIDR(ar.AddressRangeCidr)
		if err != nil {
			continue
		}
		if rangeNet.Contains(subnetNet.IP) && rangeNet.Contains(lastIP) {
			return true
		}
	}
	return false
}

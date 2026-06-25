package utils

import (
	"encoding/base64"
	"encoding/json"
	stdErrors "errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"
)

// Constants for error messages, regex patterns, and expected parts
const (
	ErrInvalidResourceFormat      = "invalid key full path format"
	ErrInvalidServiceAccountEmail = "invalid service account email format"
	ServiceAccountEmailPattern    = `^([a-zA-Z0-9-]+)-([a-zA-Z0-9-]+)@(\d+)\.iam\.gserviceaccount\.com$`
	ExpectedResourceParts         = 8
	ExpectedResourcePartsWithGrant = 10
	ExpectedServiceAccountParts   = 4
	enabledKeyState               = "ENABLED"
	StoragePoolCreatingStateError = "Storage pool present which is in creating state"
)

var (
	kmsSupportedEncryption    = []string{"GOOGLE_SYMMETRIC_ENCRYPTION", "EXTERNAL_SYMMETRIC_ENCRYPTION"}
	cmekRegionNameOverrideMap = parseCmekRegionOverrideMap()
)

// ShouldUseVCPForExistingKMS returns true when an existing KMS config should
// execute the VCP path:
//   - CVP host is not configured, or
//   - the config is explicitly VCP-created.
func ShouldUseVCPForExistingKMS(kmsConfig *datamodel.KmsConfig) bool {
	return !IsCVPHostConfigured() || (kmsConfig.KmsAttributes != nil && kmsConfig.KmsAttributes.IsVCPCreated())
}

// parseCmekRegionOverrideMap reads the CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE env var (JSON string map)
// and falls back to a default map matching CVN's defaults.
func parseCmekRegionOverrideMap() map[string]string {
	defaultMap := map[string]string{
		"asia-southeast1": "asse1",
		"asia-southeast2": "asse2",
	}
	raw := env.GetString("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", "")
	if raw == "" {
		return defaultMap
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return defaultMap
	}
	return m
}

// GetShortRegion shortens a GCP region name for use in service account IDs.
// Example: us-east1 -> usea1, europe-southwest1 -> euso1.
func GetShortRegion(region string) string {
	if region == "" {
		return ""
	}

	if val, ok := cmekRegionNameOverrideMap[region]; ok {
		return val
	}

	var shortRegion string
	lastNumber := "0"
	re := regexp.MustCompile("[0-9]+")
	match := re.FindAllString(region, -1)
	if len(match) > 0 {
		lastNumber = match[len(match)-1]
	}
	splitRegion := strings.Split(region, "-")
	for _, str := range splitRegion {
		if len(str) < 2 {
			shortRegion += str
		} else {
			shortRegion += str[:2]
		}
	}
	return shortRegion + lastNumber
}

// ParsedKeyFullPathResource represents the parsed components of the input resource string.
type ParsedKeyFullPathResource struct {
	ProjectID string
	Location  string
	KeyRing   string
	CryptoKey string
	Grant     string
}

// ParseKeyFullPathResource parses the input string and returns a ParsedKeyFullPathResource struct.
// example projects/123/locations/australia-southeast1/keyRings/name/cryptoKeys/name2
// example projects/123/locations/australia-southeast1/keyRings/name/cryptoKeys/name2/grants/grant-id
func ParseKeyFullPathResource(resource string) (*ParsedKeyFullPathResource, error) {
	parts := strings.Split(resource, "/")

	if len(parts) != ExpectedResourceParts && len(parts) != ExpectedResourcePartsWithGrant {
		return nil, errors.NewBadRequestErr(ErrInvalidResourceFormat)
	}

	parsed := &ParsedKeyFullPathResource{
		ProjectID: parts[1],
		Location:  parts[3],
		KeyRing:   parts[5],
		CryptoKey: parts[7],
	}

	if len(parts) == ExpectedResourcePartsWithGrant {
		parsed.Grant = parts[9]
	}

	return parsed, nil
}

// String reconstructs the full path (without the grant suffix) from the
// ParsedKeyFullPathResource struct.
func (p ParsedKeyFullPathResource) String() string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", p.ProjectID, p.Location, p.KeyRing, p.CryptoKey)
}

// StringWithGrant reconstructs the full path including the optional grant suffix
// (.../cryptoKeys/<key>/grants/<grant>). When no grant is set it is equivalent to String().
func (p ParsedKeyFullPathResource) StringWithGrant() string {
	base := p.String()
	if p.Grant != "" {
		return fmt.Sprintf("%s/grants/%s", base, p.Grant)
	}
	return base
}

// ParsedServiceAccount represents the parsed components of a GCP service account email.
type ParsedServiceAccount struct {
	Prefix            string
	CustomerProjectID string
	GlobalProjectID   string
}

// ParseServiceAccountEmail parses a GCP service account email and returns its components.
func ParseServiceAccountEmail(email string) (*ParsedServiceAccount, error) {
	re := regexp.MustCompile(ServiceAccountEmailPattern)

	matches := re.FindStringSubmatch(email)
	if len(matches) != ExpectedServiceAccountParts {
		return nil, errors.New(ErrInvalidServiceAccountEmail)
	}

	return &ParsedServiceAccount{
		Prefix:            matches[1],
		CustomerProjectID: matches[2],
		GlobalProjectID:   matches[3],
	}, nil
}

// DetermineStartToCloseTimeoutBasedOnUsedSize returns the maxtimeout for polling for pool or volume based on its used size
func DetermineStartToCloseTimeoutBasedOnUsedSize(volumesForMigration []*datamodel.Volume) int64 {
	var usedSpaceInGB float64

	if len(volumesForMigration) == 1 {
		usedSpaceInGB = usedSpaceInGB + float64(volumesForMigration[0].UsedBytes/1024/1024/1024)
	} else {
		for _, volume := range volumesForMigration {
			if volume != nil {
				usedSpaceInGB = usedSpaceInGB + float64(volume.UsedBytes/1024/1024/1024)
			}
		}
	}

	// Define constants for StartToCloseTimeout (in minutes)
	// Our experiments have shown roughly a 6-9x increase in encryption times for 10x increase in occupied size
	const (
		TimeoutLowOccupied     int64 = 15
		TimeoutLessThan100GB   int64 = 30
		TimeoutLessThan500GB   int64 = 150
		TimeoutLessThan1000GB  int64 = 300
		TimeoutLessThan5000GB  int64 = 1500
		TimeoutLessThan10000GB int64 = 3000
		MaximumTimeout         int64 = 10000
	)

	var startToCloseTimeout int64
	switch {
	case usedSpaceInGB < 10.0:
		startToCloseTimeout = TimeoutLowOccupied
	case usedSpaceInGB < 100.0:
		startToCloseTimeout = TimeoutLessThan100GB
	case usedSpaceInGB < 500.0:
		startToCloseTimeout = TimeoutLessThan500GB
	case usedSpaceInGB < 1000.0:
		startToCloseTimeout = TimeoutLessThan1000GB
	case usedSpaceInGB < 5000.0:
		startToCloseTimeout = TimeoutLessThan5000GB
	case usedSpaceInGB < 10000.0:
		startToCloseTimeout = TimeoutLessThan10000GB
	case usedSpaceInGB >= 10000.0:
		startToCloseTimeout = MaximumTimeout
	default:
		return MaximumTimeout
	}
	return startToCloseTimeout
}

func ReturnEncryptRequest(plainText string) *cloudkms.EncryptRequest {
	encodedText := base64.StdEncoding.EncodeToString([]byte(plainText))
	return &cloudkms.EncryptRequest{
		Plaintext: encodedText,
	}
}

// ValidateKeyProperties verifies that the crypto key returned by Google is enabled as well as supported
func ValidateKeyProperties(key *cloudkms.CryptoKey, keyName, keyRing string) error {
	if key == nil {
		return errors.NewTransientErr("Key access verification failed - Unable to get Crypto key from Google")
	}
	if key.Primary == nil || !slices.Contains(kmsSupportedEncryption, key.Primary.Algorithm) {
		return errors.NewNonRetryableErr(fmt.Sprintf("Failed to validate KMS key due to precondition failure: Specified key %v in %v algorithm is not supported", keyName, keyRing))
	}
	if key.Primary.State != enabledKeyState {
		return errors2.NewVCPError(
			errors2.ErrKMSKeyDisabledOrDestroyed,
			fmt.Errorf("failed to validate KMS key due to precondition failure: Specified key %v in %v is not enabled", keyName, keyRing),
		)
	}
	return nil
}

// IsKmsKeyUnreachable inspects Google API errors (and plain errors) to detect Cloud EKM
// reachability issues and returns the user-facing message when matched.
func IsKmsKeyUnreachable(err error) (string, bool) {
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) {
		body := gerr.Body
		if body != "" {
			lowerBody := strings.ToLower(body)
			if strings.Contains(lowerBody, "key_unreachable") || strings.Contains(lowerBody, "cloud ekm") || strings.Contains(lowerBody, "unreachable") {
				if gerr.Message != "" {
					return gerr.Message, true
				}
				return body, true
			}
		}
		if gerr.Message != "" {
			lowerMsg := strings.ToLower(gerr.Message)
			if strings.Contains(lowerMsg, "key_unreachable") || strings.Contains(lowerMsg, "cloud ekm") || strings.Contains(lowerMsg, "unreachable") {
				return gerr.Message, true
			}
		}
	}
	if err != nil {
		msg := err.Error()
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "key_unreachable") || strings.Contains(lowerMsg, "cloud ekm") || strings.Contains(lowerMsg, "unreachable") {
			return msg, true
		}
	}
	return "", false
}

// IsKmsPermissionDenied inspects Google API errors to detect permission denied
// (403) errors and returns the user-facing message when matched.
func IsKmsPermissionDenied(err error) (string, bool) {
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) {
		if gerr.Code == 403 {
			if gerr.Message != "" {
				return gerr.Message, true
			}
			return "Permission denied accessing KMS key", true
		}
	}
	// Also check error message string for permission_denied
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "permission_denied") || strings.Contains(msg, "permission denied") {
			return err.Error(), true
		}
	}
	return "", false
}

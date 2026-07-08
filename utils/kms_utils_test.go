package utils

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func TestParseKeyFullPathResource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ParsedKeyFullPathResource
		hasError bool
	}{
		{
			name:  "valid key full path without grant",
			input: "projects/project-id/locations/location/keyRings/key-ring/cryptoKeys/crypto-key",
			expected: ParsedKeyFullPathResource{
				ProjectID: "project-id",
				Location:  "location",
				KeyRing:   "key-ring",
				CryptoKey: "crypto-key",
			},
			hasError: false,
		},
		{
			name:  "valid key full path with grant",
			input: "projects/project-id/locations/location/keyRings/key-ring/cryptoKeys/crypto-key/grants/grant-id",
			expected: ParsedKeyFullPathResource{
				ProjectID: "project-id",
				Location:  "location",
				KeyRing:   "key-ring",
				CryptoKey: "crypto-key",
				Grant:     "grant-id",
			},
			hasError: false,
		},
		{
			name:     "invalid resource string",
			input:    "invalid/resource/string",
			expected: ParsedKeyFullPathResource{},
			hasError: true,
		},
		{
			name:     "invalid path with 9 parts",
			input:    "projects/project-id/locations/location/keyRings/key-ring/cryptoKeys/crypto-key/extra",
			expected: ParsedKeyFullPathResource{},
			hasError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := ParseKeyFullPathResource(test.input)
			if test.hasError {
				assert.Nil(t, result)
				assert.NotNil(t, err)
			} else {
				assert.NotNil(t, result)
				assert.Nil(t, err)
				assert.Equal(t, test.expected.ProjectID, result.ProjectID)
				assert.Equal(t, test.expected.Location, result.Location)
				assert.Equal(t, test.expected.KeyRing, result.KeyRing)
				assert.Equal(t, test.expected.CryptoKey, result.CryptoKey)
				assert.Equal(t, test.expected.Grant, result.Grant)
			}
		})
	}
}

func TestStringWithGrant(t *testing.T) {
	tests := []struct {
		name     string
		parsed   ParsedKeyFullPathResource
		expected string
	}{
		{
			name: "with grant suffix",
			parsed: ParsedKeyFullPathResource{
				ProjectID: "project-id",
				Location:  "location",
				KeyRing:   "key-ring",
				CryptoKey: "crypto-key",
				Grant:     "grant-id",
			},
			expected: "projects/project-id/locations/location/keyRings/key-ring/cryptoKeys/crypto-key/grants/grant-id",
		},
		{
			name: "without grant falls back to String",
			parsed: ParsedKeyFullPathResource{
				ProjectID: "project-id",
				Location:  "location",
				KeyRing:   "key-ring",
				CryptoKey: "crypto-key",
			},
			expected: "projects/project-id/locations/location/keyRings/key-ring/cryptoKeys/crypto-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.parsed.StringWithGrant())
		})
	}
}

func TestParseServiceAccountEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected *ParsedServiceAccount
		hasError bool
	}{
		{
			input: "n-cmek-auso1-1234@5678.iam.gserviceaccount.com",
			expected: &ParsedServiceAccount{
				Prefix:            "n-cmek-auso1",
				CustomerProjectID: "1234",
				GlobalProjectID:   "5678",
			},
			hasError: false,
		},
		{
			input:    "invalid-email-format",
			expected: nil,
			hasError: true,
		},
	}

	for _, test := range tests {
		result, err := ParseServiceAccountEmail(test.input)
		if test.hasError {
			if err == nil {
				t.Errorf("expected error for input %s, got nil", test.input)
			} else if err.Error() != ErrInvalidServiceAccountEmail {
				t.Errorf("expected error message %s, got %s", ErrInvalidServiceAccountEmail, err.Error())
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for input %s: %v", test.input, err)
			}
			if !reflect.DeepEqual(result, test.expected) {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
		}
	}
}

func TestGetShortRegion(t *testing.T) {
	t.Run("WhenGetShortRegionSuccess", func(t *testing.T) {
		shortRegion := GetShortRegion("us-east1")
		assert.Equal(t, "usea1", shortRegion)
	})
	t.Run("WhenRegionWithoutNumber", func(t *testing.T) {
		shortRegion := GetShortRegion("us-east")
		assert.Equal(t, "usea0", shortRegion)
	})
	t.Run("WhenRegionLengthIsTwo", func(t *testing.T) {
		shortRegion := GetShortRegion("us")
		assert.Equal(t, "us0", shortRegion)
	})
	t.Run("WhenRegionSegmentLengthIsOne", func(t *testing.T) {
		shortRegion := GetShortRegion("u-s1")
		assert.Equal(t, "us11", shortRegion)
	})
	t.Run("WhenRegionWithoutHyphens", func(t *testing.T) {
		shortRegion := GetShortRegion("europe")
		assert.Equal(t, "eu0", shortRegion)
	})
	t.Run("WhenRegionBlank", func(t *testing.T) {
		shortRegion := GetShortRegion("")
		assert.Equal(t, "", shortRegion)
	})
	t.Run("CustomOverrideMapRegions", func(t *testing.T) {
		origMap := cmekRegionNameOverrideMap
		defer func() { cmekRegionNameOverrideMap = origMap }()

		cmekRegionNameOverrideMap = map[string]string{
			"custom-region-1": "cust1",
			"another-region":  "anre",
		}

		assert.Equal(t, "cust1", GetShortRegion("custom-region-1"))
		assert.Equal(t, "anre", GetShortRegion("another-region"))
	})
	t.Run("DefaultOverrideForAsiaSoutheast", func(t *testing.T) {
		origMap := cmekRegionNameOverrideMap
		defer func() { cmekRegionNameOverrideMap = origMap }()
		cmekRegionNameOverrideMap = map[string]string{
			"asia-southeast1": "asse1",
			"asia-southeast2": "asse2",
		}

		assert.Equal(t, "asse1", GetShortRegion("asia-southeast1"))
		assert.Equal(t, "asse2", GetShortRegion("asia-southeast2"))
	})
}

func TestShouldUseVCPForExistingKMS(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("ReturnsFalseWhenCVPHostConfiguredAndNotVCPCreated", func(t *testing.T) {
		cvp.CVP_HOST = "localhost:8009"
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: nil}
		assert.False(t, ShouldUseVCPForExistingKMS(kmsConfig))
	})

	t.Run("ReturnsTrueWhenCVPHostNotConfigured", func(t *testing.T) {
		cvp.CVP_HOST = ""
		kmsConfig := &datamodel.KmsConfig{KmsAttributes: nil}
		assert.True(t, ShouldUseVCPForExistingKMS(kmsConfig))
	})
}

func TestParseCmekRegionOverrideMap(t *testing.T) {
	t.Run("ReturnsDefaultWhenEnvIsMissing", func(t *testing.T) {
		orig := os.Getenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE")
		defer func() { _ = os.Setenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", orig) }()
		_ = os.Unsetenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE")

		overrideMap := parseCmekRegionOverrideMap()
		assert.Equal(t, "asse1", overrideMap["asia-southeast1"])
		assert.Equal(t, "asse2", overrideMap["asia-southeast2"])
	})

	t.Run("ReturnsDefaultWhenEnvIsInvalidJSON", func(t *testing.T) {
		orig := os.Getenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE")
		defer func() { _ = os.Setenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", orig) }()
		_ = os.Setenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", "{invalid-json}")

		overrideMap := parseCmekRegionOverrideMap()
		assert.Equal(t, "asse1", overrideMap["asia-southeast1"])
		assert.Equal(t, "asse2", overrideMap["asia-southeast2"])
	})

	t.Run("ReturnsEnvMapWhenValidJSON", func(t *testing.T) {
		orig := os.Getenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE")
		defer func() { _ = os.Setenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", orig) }()
		_ = os.Setenv("CMEK_SERVICE_ACCOUNT_REGION_NAME_OVERRIDE", `{"custom-region-1":"cust1"}`)

		overrideMap := parseCmekRegionOverrideMap()
		assert.Equal(t, "cust1", overrideMap["custom-region-1"])
	})
}

func TestDetermineStartToCloseTimeoutBasedOnUsedSize(t *testing.T) {
	// Generated using GitHub copilot
	tests := []struct {
		name            string
		volumes         []*datamodel.Volume
		expectedTimeout int64
	}{
		{
			name: "Low occupied space (<10GB)",
			volumes: []*datamodel.Volume{
				{UsedBytes: 5 * 1024 * 1024 * 1024}, // 5GB
			},
			expectedTimeout: 15,
		},
		{
			name: "Less than 100GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 50 * 1024 * 1024 * 1024}, // 50GB
			},
			expectedTimeout: 30,
		},
		{
			name: "Less than 500GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 300 * 1024 * 1024 * 1024}, // 300GB
			},
			expectedTimeout: 150,
		},
		{
			name: "Less than 1000GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 800 * 1024 * 1024 * 1024}, // 800GB
			},
			expectedTimeout: 300,
		},
		{
			name: "Less than 5000GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 4000 * 1024 * 1024 * 1024}, // 4000GB
			},
			expectedTimeout: 1500,
		},
		{
			name: "Less than 10000GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 9000 * 1024 * 1024 * 1024}, // 9000GB
			},
			expectedTimeout: 3000,
		},
		{
			name: "Greater than or equal to 10000GB",
			volumes: []*datamodel.Volume{
				{UsedBytes: 12000 * 1024 * 1024 * 1024}, // 12000GB
			},
			expectedTimeout: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := DetermineStartToCloseTimeoutBasedOnUsedSize(tt.volumes)
			assert.Equal(t, tt.expectedTimeout, timeout)
		})
	}
}

func TestValidateKeyProperties(t *testing.T) {
	keyName := "KeyName"
	keyRing := "KeyRing"
	t.Run("WhenKeyIsNil", func(tt *testing.T) {
		var cryptoKey *hyperscalermodels.CryptoKey = nil

		err := ValidateKeyProperties(cryptoKey, keyName, keyRing)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Transient error: Key access verification failed - Unable to get crypto key from the cloud provider")
	})
	t.Run("WhenKeyPrimaryIsNil", func(tt *testing.T) {
		cryptoKey := hyperscalermodels.CryptoKey{HasPrimary: false}
		err := ValidateKeyProperties(&cryptoKey, keyName, keyRing)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Failed to validate KMS key due to precondition failure: Specified key KeyName in KeyRing algorithm is not supported")
	})
	t.Run("WhenKeyAlgorithmDoesNotMatch", func(tt *testing.T) {
		err := ValidateKeyProperties(&hyperscalermodels.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "GOOGLE_ASYMMETRIC_ENCRYPTION"}, keyName, keyRing)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Failed to validate KMS key due to precondition failure: Specified key KeyName in KeyRing algorithm is not supported")
	})
	t.Run("WhenKeyHasBeenDisabled", func(tt *testing.T) {
		err := ValidateKeyProperties(&hyperscalermodels.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "GOOGLE_SYMMETRIC_ENCRYPTION", PrimaryState: "DISABLED"}, keyName, keyRing)
		assert.Error(tt, err)
		var cerr *errors2.CustomError
		assert.True(tt, errors.As(err, &cerr))
		assert.Equal(tt, errors2.ErrKMSKeyDisabledOrDestroyed, cerr.TrackingID)
		assert.Contains(tt, cerr.Error(), "KMS key is disabled or destroyed")
	})
	t.Run("WhenValidateKeyPropertiesSuccessful", func(tt *testing.T) {
		err := ValidateKeyProperties(&hyperscalermodels.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "EXTERNAL_SYMMETRIC_ENCRYPTION", PrimaryState: enabledKeyState}, keyName, keyRing)
		assert.NoError(tt, err)
	})
}

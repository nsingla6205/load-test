package env

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestValidateOCIClientCertConfig(t *testing.T) {
	origFlag := OCISeparateMTLSClientCert
	origIssuer := OCIIssuerCAOCID
	origClientIssuer := OCIClientIssuerCAOCID
	origCompartment := OCICertCompartmentOCID
	t.Cleanup(func() {
		OCISeparateMTLSClientCert = origFlag
		OCIIssuerCAOCID = origIssuer
		OCIClientIssuerCAOCID = origClientIssuer
		OCICertCompartmentOCID = origCompartment
	})

	tests := []struct {
		name         string
		flag         bool
		issuer       string
		clientIssuer string
		compartment  string
		wantErr      bool
		wantContains []string
	}{
		{
			name: "feature disabled is always valid",
			flag: false,
		},
		{
			name:         "enabled with all set is valid",
			flag:         true,
			issuer:       "ocid1.certauthority.oc1..server",
			clientIssuer: "ocid1.certauthority.oc1..client",
			compartment:  "ocid1.compartment.oc1..comp",
		},
		{
			name:         "enabled missing client CA errors",
			flag:         true,
			issuer:       "ocid1.certauthority.oc1..server",
			compartment:  "ocid1.compartment.oc1..comp",
			wantErr:      true,
			wantContains: []string{"OCI_CLIENT_ISSUER_CA_OCID"},
		},
		{
			name:         "enabled missing server CA errors",
			flag:         true,
			clientIssuer: "ocid1.certauthority.oc1..client",
			compartment:  "ocid1.compartment.oc1..comp",
			wantErr:      true,
			wantContains: []string{"OCI_ISSUER_CA_OCID"},
		},
		{
			name:         "enabled missing all lists every var",
			flag:         true,
			wantErr:      true,
			wantContains: []string{"OCI_ISSUER_CA_OCID", "OCI_CLIENT_ISSUER_CA_OCID", "OCI_CERT_COMPARTMENT_OCID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			OCISeparateMTLSClientCert = tt.flag
			OCIIssuerCAOCID = tt.issuer
			OCIClientIssuerCAOCID = tt.clientIssuer
			OCICertCompartmentOCID = tt.compartment

			err := ValidateOCIClientCertConfig()
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			for _, s := range tt.wantContains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
}

func TestValidateOCIServiceCertConfig(t *testing.T) {
	origFlag := OCIEnableServiceONTAPCert
	origServiceIssuer := OCIServiceIssuerCAOCID
	origCompartment := OCICertCompartmentOCID
	t.Cleanup(func() {
		OCIEnableServiceONTAPCert = origFlag
		OCIServiceIssuerCAOCID = origServiceIssuer
		OCICertCompartmentOCID = origCompartment
	})

	tests := []struct {
		name          string
		flag          bool
		serviceIssuer string
		compartment   string
		wantErr       bool
		wantContains  []string
	}{
		{
			name: "feature disabled is always valid",
			flag: false,
		},
		{
			name:          "enabled with all set is valid",
			flag:          true,
			serviceIssuer: "ocid1.certauthority.oc1..service",
			compartment:   "ocid1.compartment.oc1..comp",
		},
		{
			name:         "enabled missing service CA errors",
			flag:         true,
			compartment:  "ocid1.compartment.oc1..comp",
			wantErr:      true,
			wantContains: []string{"OCI_SERVICE_ISSUER_CA_OCID"},
		},
		{
			name:          "enabled missing compartment errors",
			flag:          true,
			serviceIssuer: "ocid1.certauthority.oc1..service",
			wantErr:       true,
			wantContains:  []string{"OCI_CERT_COMPARTMENT_OCID"},
		},
		{
			name:         "enabled missing all lists every var",
			flag:         true,
			wantErr:      true,
			wantContains: []string{"OCI_SERVICE_ISSUER_CA_OCID", "OCI_CERT_COMPARTMENT_OCID"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			OCIEnableServiceONTAPCert = tt.flag
			OCIServiceIssuerCAOCID = tt.serviceIssuer
			OCICertCompartmentOCID = tt.compartment

			err := ValidateOCIServiceCertConfig()
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			for _, s := range tt.wantContains {
				assert.Contains(t, err.Error(), s)
			}
		})
	}
}

func TestGetOCICertificateRotationThresholdPercentage(t *testing.T) {
	const key = "OCI_CERTIFICATE_ROTATION_THRESHOLD_PERCENTAGE"

	tests := []struct {
		name     string
		set      bool
		value    string
		expected float64
	}{
		{name: "unset returns default 75%", set: false, expected: 0.75},
		{name: "percentage form is divided by 100", set: true, value: "80", expected: 0.80},
		{name: "decimal form is returned as-is", set: true, value: "0.5", expected: 0.5},
		{name: "exactly 1 treated as decimal", set: true, value: "1", expected: 1.0},
		{name: "just above 1 treated as percentage", set: true, value: "1.5", expected: 0.015},
		{name: "invalid value falls back to default", set: true, value: "not-a-number", expected: 0.75},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				_ = os.Unsetenv(key)
			}
			assert.InDelta(t, tt.expected, GetOCICertificateRotationThresholdPercentage(), 1e-9)
		})
	}
}

func TestGetOCICertificateRotationPoolStates(t *testing.T) {
	const key = "OCI_CERTIFICATE_ROTATION_POOL_STATES"

	tests := []struct {
		name     string
		set      bool
		value    string
		expected []string
	}{
		{name: "unset returns default", set: false, expected: []string{"READY", "DEGRADED"}},
		{name: "empty string returns default", set: true, value: "", expected: []string{"READY", "DEGRADED"}},
		{name: "single state", set: true, value: "READY", expected: []string{"READY"}},
		{name: "multiple states", set: true, value: "READY,DEGRADED,UPDATING", expected: []string{"READY", "DEGRADED", "UPDATING"}},
		{name: "trims surrounding whitespace", set: true, value: " READY , DEGRADED ", expected: []string{"READY", "DEGRADED"}},
		{name: "drops empty entries", set: true, value: "READY,,DEGRADED,", expected: []string{"READY", "DEGRADED"}},
		{name: "only separators returns default", set: true, value: ",,,", expected: []string{"READY", "DEGRADED"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				_ = os.Unsetenv(key)
			}
			assert.Equal(t, tt.expected, GetOCICertificateRotationPoolStates())
		})
	}
}

func TestGetOCITempPasswordResetPoolStates(t *testing.T) {
	const key = "OCI_TEMP_PASSWORD_RESET_POOL_STATES"

	tests := []struct {
		name     string
		set      bool
		value    string
		expected []string
	}{
		{name: "unset returns default", set: false, expected: []string{"READY", "DEGRADED"}},
		{name: "empty string returns default", set: true, value: "", expected: []string{"READY", "DEGRADED"}},
		{name: "single state", set: true, value: "READY", expected: []string{"READY"}},
		{name: "multiple states", set: true, value: "READY,DEGRADED,UPDATING", expected: []string{"READY", "DEGRADED", "UPDATING"}},
		{name: "trims surrounding whitespace", set: true, value: " READY , DEGRADED ", expected: []string{"READY", "DEGRADED"}},
		{name: "drops empty entries", set: true, value: "READY,,DEGRADED,", expected: []string{"READY", "DEGRADED"}},
		{name: "only separators returns default", set: true, value: ",,,", expected: []string{"READY", "DEGRADED"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				_ = os.Unsetenv(key)
			}
			assert.Equal(t, tt.expected, GetOCITempPasswordResetPoolStates())
		})
	}
}

func TestGetOCITempPasswordVerificationCronExpression(t *testing.T) {
	const key = "OCI_TEMP_PASSWORD_VERIFICATION_CRON_EXPRESSION"
	const defaultCron = "0 0 * * *"

	tests := []struct {
		name     string
		set      bool
		value    string
		expected string
	}{
		{name: "unset returns default", set: false, expected: defaultCron},
		{name: "empty string returns default", set: true, value: "", expected: defaultCron},
		{name: "whitespace-only returns default", set: true, value: "   ", expected: defaultCron},
		{name: "custom cron is honored", set: true, value: "0 * * * *", expected: "0 * * * *"},
		{name: "surrounding whitespace trimmed", set: true, value: "  0 */6 * * *  ", expected: "0 */6 * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				_ = os.Unsetenv(key)
			}
			assert.Equal(t, tt.expected, GetOCITempPasswordVerificationCronExpression())
		})
	}
}

func TestOCITempPasswordResetWindow(t *testing.T) {
	orig := OCITempPasswordResetAfterMinutes
	t.Cleanup(func() { OCITempPasswordResetAfterMinutes = orig })

	t.Run("positive minutes", func(t *testing.T) {
		OCITempPasswordResetAfterMinutes = 180
		assert.Equal(t, 180*time.Minute, OCITempPasswordResetWindow())
	})

	t.Run("zero falls back to default 24h", func(t *testing.T) {
		OCITempPasswordResetAfterMinutes = 0
		assert.Equal(t, 24*time.Hour, OCITempPasswordResetWindow())
	})

	t.Run("negative falls back to default 24h", func(t *testing.T) {
		OCITempPasswordResetAfterMinutes = -5
		assert.Equal(t, 24*time.Hour, OCITempPasswordResetWindow())
	})
}

func TestGetOCICertificateRotationCronExpression(t *testing.T) {
	const key = "OCI_CERTIFICATE_ROTATION_CRON_EXPRESSION"
	const defaultCron = "0 0 * * *"

	tests := []struct {
		name     string
		set      bool
		value    string
		expected string
	}{
		{name: "unset returns default", set: false, expected: defaultCron},
		{name: "empty string returns default", set: true, value: "", expected: defaultCron},
		{name: "whitespace-only returns default", set: true, value: "   ", expected: defaultCron},
		{name: "custom cron is honored", set: true, value: "0 */6 * * *", expected: "0 */6 * * *"},
		{name: "surrounding whitespace trimmed", set: true, value: "  0 12 * * *  ", expected: "0 12 * * *"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv(key, tt.value)
			} else {
				_ = os.Unsetenv(key)
			}
			assert.Equal(t, tt.expected, GetOCICertificateRotationCronExpression())
		})
	}
}

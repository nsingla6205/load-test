package env

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

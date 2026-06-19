package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple identifier",
			input:    "tablename",
			expected: `"tablename"`,
		},
		{
			name:     "IAM user with special characters",
			input:    "vcp-core@project.iam",
			expected: `"vcp-core@project.iam"`,
		},
		{
			name:     "identifier with embedded quotes",
			input:    `table"name`,
			expected: `"table""name"`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: `""`,
		},
		{
			name:     "identifier with dots and dashes",
			input:    "vcp-core@project.iam.gserviceaccount.com",
			expected: `"vcp-core@project.iam.gserviceaccount.com"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := qi(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestQS(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple string",
			input:    "value",
			expected: `'value'`,
		},
		{
			name:     "string with single quote",
			input:    "it's",
			expected: `'it''s'`,
		},
		{
			name:     "string with multiple single quotes",
			input:    "O'Brien's",
			expected: `'O''Brien''s'`,
		},
		{
			name:     "empty string",
			input:    "",
			expected: `''`,
		},
		{
			name:     "IAM user",
			input:    "vcp-core@project.iam",
			expected: `'vcp-core@project.iam'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := qs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestJoinQI(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{
			name:     "single identifier",
			input:    []string{"user1"},
			expected: `"user1"`,
		},
		{
			name:     "multiple identifiers",
			input:    []string{"user1", "user2", "user3"},
			expected: `"user1", "user2", "user3"`,
		},
		{
			name:     "IAM users",
			input:    []string{"vcp-core@project.iam", "vcp-worker@project.iam"},
			expected: `"vcp-core@project.iam", "vcp-worker@project.iam"`,
		},
		{
			name:     "empty slice",
			input:    []string{},
			expected: "",
		},
		{
			name:     "identifiers with special characters",
			input:    []string{"user@domain", "user-name"},
			expected: `"user@domain", "user-name"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinQI(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAllIAMUsers(t *testing.T) {
	cfg := config{
		iamVcpCore:         "vcp-core@project.iam",
		iamVcpWorker:       "vcp-worker@project.iam",
		iamClhSA:           "clh-sa@project.iam",
		iamTemporal:        "temporal@project.iam",
		iamMetricsProducer: "metrics@project.iam",
	}

	result := allIAMUsers(cfg)

	assert.Equal(t, 5, len(result))
	assert.Contains(t, result, "vcp-core@project.iam")
	assert.Contains(t, result, "vcp-worker@project.iam")
	assert.Contains(t, result, "clh-sa@project.iam")
	assert.Contains(t, result, "temporal@project.iam")
	assert.Contains(t, result, "metrics@project.iam")
}

func TestRoleMembershipUsers(t *testing.T) {
	cfg := config{
		iamVcpCore:         "vcp-core@project.iam",
		iamVcpWorker:       "vcp-worker@project.iam",
		iamClhSA:           "clh-sa@project.iam",
		iamTemporal:        "temporal@project.iam",
		iamMetricsProducer: "metrics@project.iam",
	}

	result := roleMembershipUsers(cfg)

	// Should exclude metrics producer (doesn't need DDL/migration access)
	assert.Equal(t, 4, len(result))
	assert.Contains(t, result, "vcp-core@project.iam")
	assert.Contains(t, result, "vcp-worker@project.iam")
	assert.Contains(t, result, "clh-sa@project.iam")
	assert.Contains(t, result, "temporal@project.iam")
	assert.NotContains(t, result, "metrics@project.iam")
}

func TestRoleMembershipUsers_VCPOnlyScope(t *testing.T) {
	// VCP IAM job: iamTemporal is unset because the chart doesn't pass it.
	// The empty entry must be filtered to avoid `GRANT postgres TO ""`,
	// which Postgres rejects as a zero-length delimited identifier.
	cfg := config{
		iamVcpCore:   "vcp-core@project.iam",
		iamVcpWorker: "vcp-worker@project.iam",
		iamClhSA:     "clh-sa@project.iam",
		iamTemporal:  "",
	}

	result := roleMembershipUsers(cfg)

	assert.Equal(t, 3, len(result))
	assert.NotContains(t, result, "")
	assert.Contains(t, result, "vcp-core@project.iam")
}

func TestRoleMembershipUsers_TemporalOnlyScope(t *testing.T) {
	// Temporal IAM job: VCP IAM fields are unset.
	cfg := config{
		iamTemporal: "temporal-ksa@project.iam",
	}

	result := roleMembershipUsers(cfg)

	assert.Equal(t, 1, len(result))
	assert.Equal(t, "temporal-ksa@project.iam", result[0])
}

func TestVcpGrantUsers_FiltersEmpty(t *testing.T) {
	// If IAM_METRICS_PRODUCER is somehow not set, the empty entry must
	// not leak into GRANT statements.
	cfg := config{
		iamVcpCore:         "vcp-core@project.iam",
		iamVcpWorker:       "vcp-worker@project.iam",
		iamClhSA:           "clh-sa@project.iam",
		iamMetricsProducer: "",
	}

	result := vcpGrantUsers(cfg)

	assert.NotContains(t, result, "")
	assert.Equal(t, 4, len(result)) // vcp-core, vcp-worker, clh-sa, postgres
}

func TestVcpGrantUsers(t *testing.T) {
	cfg := config{
		iamVcpCore:         "vcp-core@project.iam",
		iamVcpWorker:       "vcp-worker@project.iam",
		iamClhSA:           "clh-sa@project.iam",
		iamMetricsProducer: "metrics@project.iam",
	}

	result := vcpGrantUsers(cfg)

	assert.Equal(t, 5, len(result))
	assert.Contains(t, result, "vcp-core@project.iam")
	assert.Contains(t, result, "vcp-worker@project.iam")
	assert.Contains(t, result, "clh-sa@project.iam")
	assert.Contains(t, result, "metrics@project.iam")
	assert.Contains(t, result, "postgres")
}

func TestMetricsGrantUsers(t *testing.T) {
	cfg := config{
		iamVcpCore:         "vcp-core@project.iam",
		iamVcpWorker:       "vcp-worker@project.iam",
		iamClhSA:           "clh-sa@project.iam",
		iamMetricsProducer: "metrics@project.iam",
	}

	result := metricsGrantUsers(cfg)

	assert.Equal(t, 6, len(result))
	assert.Contains(t, result, "vcp-core@project.iam")
	assert.Contains(t, result, "vcp-worker@project.iam")
	assert.Contains(t, result, "clh-sa@project.iam")
	assert.Contains(t, result, "metrics@project.iam")
	assert.Contains(t, result, "postgres")
	assert.Contains(t, result, "metrics")
}

func TestTemporalGrantUsers(t *testing.T) {
	cfg := config{
		iamTemporal: "temporal@project.iam",
	}

	result := temporalGrantUsers(cfg)

	assert.Equal(t, 2, len(result))
	assert.Contains(t, result, "temporal@project.iam")
	assert.Contains(t, result, "postgres")
}

func TestExcludeUser(t *testing.T) {
	tests := []struct {
		name     string
		users    []string
		exclude  string
		expected []string
	}{
		{
			name:     "exclude existing user",
			users:    []string{"user1", "user2", "user3"},
			exclude:  "user2",
			expected: []string{"user1", "user3"},
		},
		{
			name:     "exclude non-existing user",
			users:    []string{"user1", "user2", "user3"},
			exclude:  "user4",
			expected: []string{"user1", "user2", "user3"},
		},
		{
			name:     "exclude from single user list",
			users:    []string{"user1"},
			exclude:  "user1",
			expected: nil, // Function returns nil for empty result
		},
		{
			name:     "exclude from empty list",
			users:    []string{},
			exclude:  "user1",
			expected: nil, // Function returns nil for empty result
		},
		{
			name:     "exclude duplicate users",
			users:    []string{"user1", "user2", "user1"},
			exclude:  "user1",
			expected: []string{"user2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := excludeUser(tt.users, tt.exclude)
			if tt.expected == nil {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestIAMPort(t *testing.T) {
	tests := []struct {
		name         string
		cfg          config
		iamUser      string
		expectedPort string
	}{
		{
			name: "temporal user with temporal port set",
			cfg: config{
				dbPort:         "5432",
				temporalDBPort: "5433",
				iamTemporal:    "temporal@project.iam",
			},
			iamUser:      "temporal@project.iam",
			expectedPort: "5433",
		},
		{
			name: "temporal user without temporal port",
			cfg: config{
				dbPort:         "5432",
				temporalDBPort: "",
				iamTemporal:    "temporal@project.iam",
			},
			iamUser:      "temporal@project.iam",
			expectedPort: "5432",
		},
		{
			name: "non-temporal user",
			cfg: config{
				dbPort:         "5432",
				temporalDBPort: "5433",
				iamTemporal:    "temporal@project.iam",
			},
			iamUser:      "vcp-core@project.iam",
			expectedPort: "5432",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := iamPort(tt.cfg, tt.iamUser)
			assert.Equal(t, tt.expectedPort, result)
		})
	}
}

func TestEnvOr(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		fallback string
		envValue string
		expected string
	}{
		{
			name:     "env var set",
			key:      "TEST_VAR",
			fallback: "default",
			envValue: "custom",
			expected: "custom",
		},
		{
			name:     "env var not set",
			key:      "TEST_VAR_UNSET",
			fallback: "default",
			envValue: "",
			expected: "default",
		},
		{
			name:     "env var empty string",
			key:      "TEST_VAR_EMPTY",
			fallback: "default",
			envValue: "",
			expected: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up
			defer func() { _ = os.Unsetenv(tt.key) }()

			if tt.envValue != "" {
				_ = os.Setenv(tt.key, tt.envValue)
			}

			result := envOr(tt.key, tt.fallback)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		expected bool
	}{
		{
			name:     "true string",
			key:      "TEST_BOOL_TRUE",
			envValue: "true",
			expected: true,
		},
		{
			name:     "1 string",
			key:      "TEST_BOOL_ONE",
			envValue: "1",
			expected: true,
		},
		{
			name:     "false string",
			key:      "TEST_BOOL_FALSE",
			envValue: "false",
			expected: false,
		},
		{
			name:     "0 string",
			key:      "TEST_BOOL_ZERO",
			envValue: "0",
			expected: false,
		},
		{
			name:     "empty string",
			key:      "TEST_BOOL_EMPTY",
			envValue: "",
			expected: false,
		},
		{
			name:     "other string",
			key:      "TEST_BOOL_OTHER",
			envValue: "yes",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up
			defer func() { _ = os.Unsetenv(tt.key) }()

			_ = os.Setenv(tt.key, tt.envValue)

			result := envBool(tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestQI_SQLInjectionPrevention(t *testing.T) {
	// Test that embedded quotes are properly escaped
	maliciousInput := `"; DROP TABLE users; --`
	result := qi(maliciousInput)
	expected := `"""; DROP TABLE users; --"`
	assert.Equal(t, expected, result)

	// Verify that the escaped identifier can be safely used in SQL
	// (would need actual DB connection to fully test, but escaping is validated)
	assert.Contains(t, result, `""`) // Double quote should be escaped
}

func TestQS_SQLInjectionPrevention(t *testing.T) {
	// Test that embedded quotes are properly escaped
	maliciousInput := `'; DROP TABLE users; --`
	result := qs(maliciousInput)
	expected := `'''; DROP TABLE users; --'`
	assert.Equal(t, expected, result)

	// Verify that the escaped string can be safely used in SQL
	assert.Contains(t, result, `''`) // Single quote should be escaped
}

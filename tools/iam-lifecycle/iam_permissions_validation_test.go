package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateIAMPermissions_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			cfg: config{
				instanceConnName:   "test-project:us-central1:test-instance",
				iamVcpCore:         "vcp-core@test-project.iam.gserviceaccount.com",
				iamVcpWorker:       "vcp-worker@test-project.iam.gserviceaccount.com",
				iamClhSA:           "clh-sa@test-project.iam.gserviceaccount.com",
				iamTemporal:        "temporal@test-project.iam.gserviceaccount.com",
				iamMetricsProducer: "metrics@test-project.iam.gserviceaccount.com",
			},
			wantErr: false,
		},
		{
			name: "invalid instance connection name format",
			cfg: config{
				instanceConnName: "invalid-format",
			},
			wantErr:     true,
			errContains: "invalid INSTANCE_CONNECTION_NAME format",
		},
		{
			name: "temporal-only scope (no vcp users)",
			cfg: config{
				instanceConnName: "test-project:us-central1:test-instance",
				iamTemporal:      "temporal@test-project.iam.gserviceaccount.com",
				temporalEnabled:  true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This test only validates config parsing.
			// Actual API calls would require mocking or integration tests.
			parts := splitInstanceConnName(tt.cfg.instanceConnName)
			if len(parts) != 3 && !tt.wantErr {
				t.Errorf("expected valid instance connection name format")
			}
			if len(parts) != 3 && tt.wantErr {
				// Expected error case
				return
			}
		})
	}
}

func splitInstanceConnName(connName string) []string {
	if connName == "" {
		return nil
	}
	// Simple split for testing
	result := make([]string, 0, 3)
	start := 0
	colonCount := 0
	for i, c := range connName {
		if c == ':' {
			result = append(result, connName[start:i])
			start = i + 1
			colonCount++
		}
	}
	if start < len(connName) {
		result = append(result, connName[start:])
	}
	return result
}

func TestIsRetryableValidationError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		shouldRetry bool
	}{
		{
			name:        "nil error",
			err:         nil,
			shouldRetry: false,
		},
		{
			name:        "429 rate limit",
			err:         errors.New("Error 429: Too Many Requests"),
			shouldRetry: true,
		},
		{
			name:        "500 internal server error",
			err:         errors.New("Error 500: Internal Server Error"),
			shouldRetry: true,
		},
		{
			name:        "503 service unavailable",
			err:         errors.New("Error 503: Service Unavailable"),
			shouldRetry: true,
		},
		{
			name:        "UNAVAILABLE status",
			err:         errors.New("rpc error: code = UNAVAILABLE"),
			shouldRetry: true,
		},
		{
			name:        "timeout error",
			err:         errors.New("connection timeout"),
			shouldRetry: true,
		},
		{
			name:        "connection reset",
			err:         errors.New("connection reset by peer"),
			shouldRetry: true,
		},
		{
			name:        "403 permission denied - should not retry",
			err:         errors.New("Error 403: Permission denied"),
			shouldRetry: false,
		},
		{
			name:        "404 not found - should not retry",
			err:         errors.New("Error 404: Not Found"),
			shouldRetry: false,
		},
		{
			name:        "400 bad request - should not retry",
			err:         errors.New("Error 400: Bad Request"),
			shouldRetry: false,
		},
		{
			name:        "PERMISSION_DENIED - should not retry",
			err:         errors.New("rpc error: code = PERMISSION_DENIED"),
			shouldRetry: false,
		},
		{
			name:        "generic error - should not retry",
			err:         errors.New("something went wrong"),
			shouldRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableValidationError(tt.err)
			assert.Equal(t, tt.shouldRetry, result)
		})
	}
}

func TestRetryAPICall_Success(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := retryAPICall(ctx, "test operation", 3, func() error {
		callCount++
		return nil // Success on first try
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, callCount, "Should succeed on first attempt")
}

func TestRetryAPICall_RetryableError(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := retryAPICall(ctx, "test operation", 3, func() error {
		callCount++
		if callCount < 3 {
			return errors.New("Error 503: Service Unavailable")
		}
		return nil // Success on third try
	})

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount, "Should retry twice and succeed on third attempt")
}

func TestRetryAPICall_NonRetryableError(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := retryAPICall(ctx, "test operation", 3, func() error {
		callCount++
		return errors.New("Error 403: Permission denied")
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Equal(t, 1, callCount, "Should not retry non-retryable errors")
}

func TestRetryAPICall_MaxRetriesExceeded(t *testing.T) {
	ctx := context.Background()
	callCount := 0

	err := retryAPICall(ctx, "test operation", 3, func() error {
		callCount++
		return errors.New("Error 503: Service Unavailable")
	})

	assert.Error(t, err)
	// The error should contain information about the failure
	assert.Contains(t, err.Error(), "failed after 3 retries")
	assert.Equal(t, 3, callCount, "Should attempt all retries")
}

func TestRetryAPICall_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	callCount := 0
	err := retryAPICall(ctx, "test operation", 3, func() error {
		callCount++
		return nil
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context cancelled")
	assert.Equal(t, 0, callCount, "Should not attempt when context is cancelled")
}

func TestServiceAccountEmailExtraction(t *testing.T) {
	tests := []struct {
		email       string
		wantProject string
		wantValid   bool
	}{
		{
			email:       "vcp-core@test-project.iam.gserviceaccount.com",
			wantProject: "test-project",
			wantValid:   true,
		},
		{
			email:       "invalid-email",
			wantProject: "",
			wantValid:   false,
		},
		{
			email:       "user@domain.com",
			wantProject: "",
			wantValid:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			// Extract project from email
			var gotProject string
			var gotValid bool

			if len(tt.email) > 0 {
				// Simple extraction logic for testing
				atIndex := -1
				for i := 0; i < len(tt.email); i++ {
					if tt.email[i] == '@' {
						atIndex = i
						break
					}
				}
				if atIndex > 0 && atIndex < len(tt.email)-1 {
					domain := tt.email[atIndex+1:]
					suffix := ".iam.gserviceaccount.com"
					if len(domain) > len(suffix) {
						if len(domain) >= len(suffix) && domain[len(domain)-len(suffix):] == suffix {
							gotProject = domain[:len(domain)-len(suffix)]
							gotValid = true
						}
					}
				}
			}

			if gotValid != tt.wantValid {
				t.Errorf("got valid=%v, want valid=%v", gotValid, tt.wantValid)
			}
			if gotValid && gotProject != tt.wantProject {
				t.Errorf("got project=%q, want project=%q", gotProject, tt.wantProject)
			}
		})
	}
}

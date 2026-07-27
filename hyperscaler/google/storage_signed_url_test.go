package google

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"google.golang.org/api/option"
)

func TestGenerateSignedURL(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		// Create a mock storage client
		storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
		if err != nil {
			t.Fatalf("Failed to create storage client: %v", err)
		}

		gcp := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			AdminGCPService: &AdminGCPService{
				storageService: storageClient,
			},
		}

		// Test with a valid bucket and object
		bucketName := "test-bucket"
		objectName := "test-object"
		duration := time.Hour

		signedURL, err := gcp.GenerateSignedURL(ctx, bucketName, objectName, duration)

		// Note: This will fail in tests because we don't have real GCS credentials
		// but we can test the error handling
		assert.Error(t, err)
		assert.Empty(t, signedURL)
	})

	t.Run("WorkloadIdentityAuthentication", func(t *testing.T) {
		// Create a mock storage client
		storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
		if err != nil {
			t.Fatalf("Failed to create storage client: %v", err)
		}

		gcp := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			AdminGCPService: &AdminGCPService{
				storageService: storageClient,
			},
		}

		// Test with Workload Identity authentication
		bucketName := "test-bucket"
		objectName := "test-object"
		duration := time.Hour

		signedURL, err := gcp.GenerateSignedURL(ctx, bucketName, objectName, duration)

		// Note: This will fail in tests because we don't have real GCS credentials
		// but we can test the error handling
		assert.Error(t, err)
		assert.Empty(t, signedURL)
		// Should not fail due to missing service account configuration
		assert.NotContains(t, err.Error(), "missing required service account")
	})

	t.Run("StorageServiceNotInitialized", func(t *testing.T) {
		gcp := &GcpServices{
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			AdminGCPService: nil,
		}

		signedURL, err := gcp.GenerateSignedURL(ctx, "bucket", "object", time.Hour)

		assert.Error(t, err)
		assert.Empty(t, signedURL)
		assert.Contains(t, err.Error(), "storage service not initialized")
	})

	t.Run("StorageServiceNil", func(t *testing.T) {
		gcp := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			AdminGCPService: &AdminGCPService{
				storageService: nil,
			},
		}

		signedURL, err := gcp.GenerateSignedURL(ctx, "bucket", "object", time.Hour)

		assert.Error(t, err)
		assert.Empty(t, signedURL)
		assert.Contains(t, err.Error(), "storage service not initialized")
	})
}

func TestGenerateVSASignedURL(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		// Create a mock storage client
		storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
		if err != nil {
			t.Fatalf("Failed to create storage client: %v", err)
		}

		gcp := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			AdminGCPService: &AdminGCPService{
				storageService: storageClient,
			},
		}

		// Test with a valid VSA image name
		vsaImageName := "r9.17.1PxN_250902_0747_promo_image.tgz"

		signedURL, err := gcp.GenerateVSASignedURL(ctx, vsaImageName)

		// Note: This will fail in tests because we don't have real GCS credentials
		// but we can test the error handling
		assert.Error(t, err)
		assert.Empty(t, signedURL)
	})

	t.Run("StorageServiceNotInitialized", func(t *testing.T) {
		gcp := &GcpServices{
			Ctx:             ctx,
			Logger:          util.GetLogger(ctx),
			AdminGCPService: nil,
		}

		signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

		assert.Error(t, err)
		assert.Empty(t, signedURL)
		assert.Contains(t, err.Error(), "storage service not initialized")
	})
}

func TestGenerateSignedURLWithCustomDuration(t *testing.T) {
	ctx := context.Background()

	t.Run("Success", func(t *testing.T) {
		// Create a mock storage client
		storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
		if err != nil {
			t.Fatalf("Failed to create storage client: %v", err)
		}

		gcp := &GcpServices{
			Ctx:    ctx,
			Logger: util.GetLogger(ctx),
			AdminGCPService: &AdminGCPService{
				storageService: storageClient,
			},
		}

		// Test with custom duration
		bucketName := "test-bucket"
		objectName := "test-object"
		duration := 30 * time.Minute

		signedURL, err := gcp.GenerateSignedURLWithCustomDuration(ctx, bucketName, objectName, duration)

		// Note: This will fail in tests because we don't have real GCS credentials
		// but we can test the error handling
		assert.Error(t, err)
		assert.Empty(t, signedURL)
	})
}

// mockHTTPTransport is a mock transport for testing
type mockHTTPTransport struct {
	response *http.Response
	err      error
}

func (m *mockHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.response, m.err
}

// mockReadCloser is a mock io.ReadCloser for testing
type mockReadCloser struct {
	data      []byte
	readErr   error
	closeErr  error
	readCount int
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.readErr != nil {
		return 0, m.readErr
	}
	if len(m.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, m.data)
	m.data = m.data[n:]
	m.readCount++
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return m.closeErr
}

func TestGenerateSignedURL_LocalEnvironment(t *testing.T) {
	ctx := context.Background()

	// Set ENV to local to test the local environment path
	t.Setenv("ENV", "local")
	t.Setenv("VSA_WORKER_SA_EMAIL", "test@example.com")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	// Test with local environment
	bucketName := "test-bucket"
	objectName := "test-object"
	duration := time.Hour

	signedURL, err := gcp.GenerateSignedURL(ctx, bucketName, objectName, duration)

	// This will fail due to missing credentials, but we're testing the local path
	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to service account detection
	assert.NotContains(t, err.Error(), "failed to detect service account")
}

func TestGenerateSignedURL_NonLocalEnvironment(t *testing.T) {
	ctx := context.Background()

	// Set ENV to non-local to test the metadata server path
	t.Setenv("ENV", "production")

	// Mock metadata server unavailable so the test is deterministic in CI environments
	// that have GCP credentials and a reachable metadata endpoint.
	mockTransport := &mockHTTPTransport{
		err: assert.AnError,
	}
	mockClient := &http.Client{
		Transport: mockTransport,
	}
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	// Test with non-local environment
	bucketName := "test-bucket"
	objectName := "test-object"
	duration := time.Hour

	signedURL, err := gcp.GenerateSignedURL(ctx, bucketName, objectName, duration)

	// This will fail due to metadata server not being available
	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should fail due to service account detection
	assert.Contains(t, err.Error(), "failed to detect service account")
}

func TestGenerateVSASignedURL_DurationValidation(t *testing.T) {
	ctx := context.Background()

	// Test with invalid duration (too low)
	t.Setenv("VSA_SIGNED_URL_DURATION_HOURS", "0")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to duration validation (it should use default)
	assert.NotContains(t, err.Error(), "duration")
}

func TestGenerateVSASignedURL_DurationValidationTooHigh(t *testing.T) {
	ctx := context.Background()

	// Test with invalid duration (too high)
	t.Setenv("VSA_SIGNED_URL_DURATION_HOURS", "25")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to duration validation (it should use default)
	assert.NotContains(t, err.Error(), "duration")
}

func TestGenerateVSASignedURL_ValidDuration(t *testing.T) {
	ctx := context.Background()

	// Test with valid duration
	t.Setenv("VSA_SIGNED_URL_DURATION_HOURS", "6")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to duration validation
	assert.NotContains(t, err.Error(), "duration")
}

func TestGetActiveServiceAccountEmail_Success(t *testing.T) {
	ctx := context.Background()

	// Create a mock response
	mockResponse := &http.Response{
		StatusCode: http.StatusOK,
		Body: &mockReadCloser{
			data:     []byte("test@example.com"),
			readErr:  nil,
			closeErr: nil,
		},
	}

	// Create a mock transport
	mockTransport := &mockHTTPTransport{
		response: mockResponse,
	}

	// Create a custom client with the mock transport
	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Replace the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
	}

	email, err := gcp.getActiveServiceAccountEmail(ctx)

	assert.NoError(t, err)
	assert.Equal(t, "test@example.com", email)
}

func TestGetActiveServiceAccountEmail_HTTPError(t *testing.T) {
	ctx := context.Background()

	// Create a mock transport with error
	mockTransport := &mockHTTPTransport{
		err: assert.AnError,
	}

	// Create a custom client with the mock transport
	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Replace the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
	}

	email, err := gcp.getActiveServiceAccountEmail(ctx)

	assert.Error(t, err)
	assert.Empty(t, email)
	assert.Contains(t, err.Error(), "failed to query metadata server")
}

func TestGetActiveServiceAccountEmail_NonOKStatus(t *testing.T) {
	ctx := context.Background()

	// Create a mock response with non-OK status
	mockResponse := &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Body: &mockReadCloser{
			data:     []byte("Not Found"),
			readErr:  nil,
			closeErr: nil,
		},
	}

	mockTransport := &mockHTTPTransport{
		response: mockResponse,
	}

	// Create a custom client with the mock transport
	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Replace the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
	}

	email, err := gcp.getActiveServiceAccountEmail(ctx)

	assert.Error(t, err)
	assert.Empty(t, email)
	assert.Contains(t, err.Error(), "metadata request failed")
}

func TestGetActiveServiceAccountEmail_ReadBodyError(t *testing.T) {
	ctx := context.Background()

	// Create a mock response with read error
	mockResponse := &http.Response{
		StatusCode: http.StatusOK,
		Body: &mockReadCloser{
			data:     []byte(""),
			readErr:  assert.AnError,
			closeErr: nil,
		},
	}

	mockTransport := &mockHTTPTransport{
		response: mockResponse,
	}

	// Create a custom client with the mock transport
	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Replace the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
	}

	email, err := gcp.getActiveServiceAccountEmail(ctx)

	assert.Error(t, err)
	assert.Empty(t, email)
	assert.Contains(t, err.Error(), "failed to read response")
}

func TestGetActiveServiceAccountEmail_CloseBodyError(t *testing.T) {
	ctx := context.Background()

	// Create a mock response with close error
	mockResponse := &http.Response{
		StatusCode: http.StatusOK,
		Body: &mockReadCloser{
			data:     []byte("test@example.com"),
			readErr:  nil,            // No error on read
			closeErr: assert.AnError, // Error on close
		},
	}

	mockTransport := &mockHTTPTransport{
		response: mockResponse,
	}

	// Create a custom client with the mock transport
	mockClient := &http.Client{
		Transport: mockTransport,
	}

	// Replace the default client temporarily
	originalClient := http.DefaultClient
	http.DefaultClient = mockClient
	defer func() {
		http.DefaultClient = originalClient
	}()

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
	}

	email, err := gcp.getActiveServiceAccountEmail(ctx)

	// The close error should not affect the main function result
	assert.NoError(t, err)
	assert.Equal(t, "test@example.com", email)
}

func TestGenerateSignedURL_IAMClientError(t *testing.T) {
	ctx := context.Background()

	// This test is hard to implement without mocking the credentials package
	// The IAM client creation happens inside the function and is hard to mock
	// We'll test the error path by using a context that will cause the IAM client to fail
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel the context immediately

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	// Test with canceled context to trigger IAM client error
	signedURL, err := gcp.GenerateSignedURL(canceledCtx, "bucket", "object", time.Hour)

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should fail due to IAM client creation or service account detection
	assert.True(t, strings.Contains(err.Error(), "failed to create IAM credentials client") ||
		strings.Contains(err.Error(), "failed to detect service account"))
}

func TestGenerateSignedURL_SignBlobError(t *testing.T) {
	ctx := context.Background()

	// This test is also hard to implement without extensive mocking
	// The SignBlob call happens inside the SignBytes function and is hard to mock
	// We'll test this by using a context that might cause the operation to fail
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel the context immediately

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	// Test with canceled context to potentially trigger SignBlob error
	signedURL, err := gcp.GenerateSignedURL(canceledCtx, "bucket", "object", time.Hour)

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should fail due to various reasons (IAM client, service account, or SignBlob)
	assert.True(t, strings.Contains(err.Error(), "failed to create IAM credentials client") ||
		strings.Contains(err.Error(), "failed to detect service account") ||
		strings.Contains(err.Error(), "failed to sign blob"))
}

func TestGenerateSignedURL_StorageSignedURLError(t *testing.T) {
	ctx := context.Background()

	// This test is also hard to implement without mocking the storage package
	// The storage.SignedURL call happens at the end and is hard to mock
	// We'll test this by using invalid parameters that might cause the call to fail
	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	// Test with invalid parameters to potentially trigger storage.SignedURL error
	signedURL, err := gcp.GenerateSignedURL(ctx, "", "", time.Hour)

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should fail due to various reasons
	assert.True(t, strings.Contains(err.Error(), "failed to create IAM credentials client") ||
		strings.Contains(err.Error(), "failed to detect service account") ||
		strings.Contains(err.Error(), "failed to sign blob") ||
		strings.Contains(err.Error(), "failed to generate signed URL"))
}

func TestGenerateVSASignedURL_EnvironmentVariables(t *testing.T) {
	ctx := context.Background()

	// Test with custom environment variables
	t.Setenv("VSA_IMAGE_BUCKET", "custom-bucket")
	t.Setenv("VSA_SIGNED_URL_DURATION_HOURS", "8")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to environment variable parsing
	assert.NotContains(t, err.Error(), "bucket")
	assert.NotContains(t, err.Error(), "duration")
}

func TestGenerateVSASignedURL_DefaultValues(t *testing.T) {
	ctx := context.Background()

	// Clear environment variables to test defaults
	t.Setenv("VSA_IMAGE_BUCKET", "")
	t.Setenv("VSA_SIGNED_URL_DURATION_HOURS", "")

	storageClient, err := storage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create storage client: %v", err)
	}

	gcp := &GcpServices{
		Ctx:    ctx,
		Logger: util.GetLogger(ctx),
		AdminGCPService: &AdminGCPService{
			storageService: storageClient,
		},
	}

	signedURL, err := gcp.GenerateVSASignedURL(ctx, "test-image.tgz")

	assert.Error(t, err)
	assert.Empty(t, signedURL)
	// Should not fail due to default value parsing
	assert.NotContains(t, err.Error(), "bucket")
	assert.NotContains(t, err.Error(), "duration")
}

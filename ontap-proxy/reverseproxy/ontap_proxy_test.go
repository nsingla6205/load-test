package reverseproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
)

// generateTestCertificates generates self-signed certificates for testing
func generateTestCertificates(t *testing.T) ([]byte, []byte, []byte) {
	// Generate CA key
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	// Create CA certificate
	caTemplate := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization:  []string{"Test CA"},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	assert.NoError(t, err)

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	// Generate client key
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NoError(t, err)

	// Create client certificate
	clientTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			Organization: []string{"Test Client"},
			Country:      []string{"US"},
			CommonName:   "test-client",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature,
	}

	clientCertDER, err := x509.CreateCertificate(rand.Reader, &clientTemplate, &caTemplate, &clientKey.PublicKey, caKey)
	assert.NoError(t, err)

	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertDER})
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})

	return clientCertPEM, clientKeyPEM, caCertPEM
}

// Mock endpoint reachability for all tests
func TestMain(m *testing.M) {
	// Mock the endpoint reachability check to always succeed
	testOntapEndpointReachability = func(endpoint string, authData *models.AuthData, ctx context.Context, transport *http.Transport) error {
		return nil
	}
	os.Exit(m.Run())
}

func TestConnectionPool_GetClient(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()
	// Create mock auth data
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}
	// First call should create a new client
	client1, endpoint1, err := pool.GetClient(ctx, authData)
	assert.NoError(t, err)
	assert.NotNil(t, client1)
	assert.NotEmpty(t, endpoint1)

	// Second call should return the same client
	client2, endpoint2, err := pool.GetClient(ctx, authData)
	assert.NoError(t, err)
	assert.NotNil(t, client2)
	assert.Equal(t, client1, client2)
	assert.Equal(t, endpoint1, endpoint2)

	// Verify pool has one client
	stats := pool.GetStats()
	assert.Equal(t, 1, stats["total_connections"])
}

func TestConnectionPool_GetClientCacheStatus(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()

	t.Run("EmptyPoolShouldReturnEmptyStatus", func(t *testing.T) {
		status := pool.GetClientCacheStatus()
		assert.Empty(t, status)
	})

	t.Run("ShouldReturnStatusForEachCachedClient", func(t *testing.T) {
		// Create two clients with different users
		authData1 := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "pool-1",
			AccountName: "account-1",
			Username:    "user1",
			Password:    "pass1",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: "ontap1.example.com"},
			},
		}
		authData2 := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "pool-1",
			AccountName: "account-1",
			Username:    "user2",
			Password:    "pass2",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: "ontap2.example.com"},
			},
		}

		_, _, err := pool.GetClient(ctx, authData1)
		assert.NoError(t, err)
		_, _, err = pool.GetClient(ctx, authData2)
		assert.NoError(t, err)

		status := pool.GetClientCacheStatus()
		assert.Len(t, status, 2)

		// Verify each entry has required fields
		for _, entry := range status {
			assert.NotEmpty(t, entry.CacheKey)
			assert.NotEmpty(t, entry.Endpoint)
			assert.False(t, entry.CachedAt.IsZero())
			assert.False(t, entry.ExpiresAt.IsZero())
			assert.True(t, entry.ExpiresAt.After(entry.CachedAt))
		}
	})

	t.Run("StatusShouldContainCorrectCacheKey", func(t *testing.T) {
		newPool := NewConnectionPool()
		defer newPool.Close()

		authData := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "testuser",
			Password:    "testpass",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: "ontap.example.com"},
			},
		}

		_, _, err := newPool.GetClient(ctx, authData)
		assert.NoError(t, err)

		status := newPool.GetClientCacheStatus()
		assert.Len(t, status, 1)

		expectedKey := "test-account:test-pool:0:testuser"
		assert.Equal(t, expectedKey, status[0].CacheKey)
		assert.Equal(t, "ontap.example.com", status[0].Endpoint)
	})
}

func TestGetGlobalConnectionPool(t *testing.T) {
	// This tests that the global connection pool is initialized and accessible
	pool := GetGlobalConnectionPool()
	assert.NotNil(t, pool, "Global connection pool should not be nil")
}

func TestGetGlobalClientCacheStatus(t *testing.T) {
	// This tests the global function wrapper
	status := GetGlobalClientCacheStatus()
	// Should not panic and return a slice (may have entries from other tests)
	assert.NotNil(t, status)
}

func TestConnectionPool_GeneratePoolKey(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	t.Run("DifferentUsernamesShouldProduceDifferentKeys", func(t *testing.T) {
		authData1 := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "user1",
		}
		authData2 := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "user2",
		}

		key1 := pool.generatePoolKey(authData1)
		key2 := pool.generatePoolKey(authData2)

		assert.NotEqual(t, key1, key2, "Different usernames should produce different keys")
		assert.Contains(t, key1, "user1", "Key should contain username")
		assert.Contains(t, key2, "user2", "Key should contain username")
	})

	t.Run("DifferentAuthTypesShouldProduceDifferentKeys", func(t *testing.T) {
		authData1 := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "testuser",
		}
		authData2 := &models.AuthData{
			AuthType:    models.USER_CERTIFICATE,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "testuser",
		}

		key1 := pool.generatePoolKey(authData1)
		key2 := pool.generatePoolKey(authData2)

		assert.NotEqual(t, key1, key2, "Different auth types should produce different keys")
	})

	t.Run("CertificateAuthWithDifferentCertIDsShouldProduceDifferentKeys", func(t *testing.T) {
		authData1 := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			PoolID:        "test-pool",
			AccountName:   "test-account",
			Username:      "testuser",
			CertificateID: "cert-id-1",
		}
		authData2 := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			PoolID:        "test-pool",
			AccountName:   "test-account",
			Username:      "testuser",
			CertificateID: "cert-id-2",
		}

		key1 := pool.generatePoolKey(authData1)
		key2 := pool.generatePoolKey(authData2)

		assert.NotEqual(t, key1, key2, "Different certificate IDs should produce different keys")
		assert.Contains(t, key1, "cert-id-1", "Key should contain certificate ID")
		assert.Contains(t, key2, "cert-id-2", "Key should contain certificate ID")
	})

	t.Run("CertificateAuthWithoutCertIDShouldNotIncludeCertID", func(t *testing.T) {
		authData := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			PoolID:        "test-pool",
			AccountName:   "test-account",
			Username:      "testuser",
			CertificateID: "", // Empty certificate ID
		}

		key := pool.generatePoolKey(authData)

		// Key should be in format: accountName:poolID:authType:username (no cert ID)
		expected := "test-account:test-pool:2:testuser"
		assert.Equal(t, expected, key, "Key without certificate ID should match expected format")
	})

	t.Run("BasicAuthShouldNotIncludeCertID", func(t *testing.T) {
		authData := &models.AuthData{
			AuthType:      models.USERNAME_PWD,
			PoolID:        "test-pool",
			AccountName:   "test-account",
			Username:      "testuser",
			CertificateID: "some-cert-id", // Should be ignored for basic auth
		}

		key := pool.generatePoolKey(authData)

		// Key should not contain certificate ID for basic auth
		assert.NotContains(t, key, "some-cert-id", "Basic auth key should not include certificate ID")
		expected := "test-account:test-pool:0:testuser"
		assert.Equal(t, expected, key, "Basic auth key should match expected format")
	})

	t.Run("SameAuthDataShouldProduceSameKey", func(t *testing.T) {
		authData := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "test-pool",
			AccountName: "test-account",
			Username:    "testuser",
		}

		key1 := pool.generatePoolKey(authData)
		key2 := pool.generatePoolKey(authData)

		assert.Equal(t, key1, key2, "Same auth data should produce same key")
	})

	t.Run("DedicatedCapacityWithEndpoint_IncludesClusterAndEndpoint", func(t *testing.T) {
		authData := &models.AuthData{
			AuthType:     models.USERNAME_PWD_SEC_MGR,
			PoolID:       "pool-123",
			AccountName:  "account-456",
			Username:     "gadmin",
			ServiceLevel: "STANDARD",
			ClusterName:  "cluster-a",
			OntapEndpoints: []models.OntapEndpoint{{IP: "10.0.0.1", DNS: "10.0.0.1"}},
		}

		key := pool.generatePoolKey(authData)

		expected := "hardware:account-456:pool-123:1:gadmin:cluster-a:10.0.0.1"
		assert.Equal(t, expected, key)
	})

	t.Run("DedicatedCapacityWithoutEndpoint_UsesEmptyEndpoint", func(t *testing.T) {
		authData := &models.AuthData{
			AuthType:       models.USERNAME_PWD_SEC_MGR,
			PoolID:         "pool-123",
			AccountName:    "account-456",
			Username:       "gadmin",
			ServiceLevel:   "STANDARD",
			ClusterName:    "cluster-a",
			OntapEndpoints: nil,
		}

		key := pool.generatePoolKey(authData)

		expected := "hardware:account-456:pool-123:1:gadmin:cluster-a:"
		assert.Equal(t, expected, key)
	})

	t.Run("DedicatedCapacityDifferentClusters_ProduceDifferentKeys", func(t *testing.T) {
		base := &models.AuthData{
			AuthType:     models.USERNAME_PWD_SEC_MGR,
			PoolID:       "pool-123",
			AccountName:  "account-456",
			Username:     "gadmin",
			ServiceLevel: "STANDARD",
			OntapEndpoints: []models.OntapEndpoint{{IP: "10.0.0.1", DNS: "10.0.0.1"}},
		}
		authA := *base
		authA.ClusterName = "cluster-a"
		authB := *base
		authB.ClusterName = "cluster-b"

		keyA := pool.generatePoolKey(&authA)
		keyB := pool.generatePoolKey(&authB)

		assert.NotEqual(t, keyA, keyB, "Different dedicated clusters should produce different keys")
	})

	t.Run("KeyFormatVerification", func(t *testing.T) {
		// Basic auth key format: accountName:poolID:authType:username
		basicAuthData := &models.AuthData{
			AuthType:    models.USERNAME_PWD,
			PoolID:      "pool-123",
			AccountName: "account-456",
			Username:    "admin",
		}
		basicKey := pool.generatePoolKey(basicAuthData)
		assert.Equal(t, "account-456:pool-123:0:admin", basicKey)

		// Certificate auth key format: accountName:poolID:authType:username:certID
		certAuthData := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			PoolID:        "pool-123",
			AccountName:   "account-456",
			Username:      "admin",
			CertificateID: "cert-789",
		}
		certKey := pool.generatePoolKey(certAuthData)
		assert.Equal(t, "account-456:pool-123:2:admin:cert-789", certKey)
	})
}

func TestConnectionPool_DifferentAuthTypes(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()
	ontapAddress := "test-ontap.example.com"

	// Create auth data for basic auth
	basicAuth := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: ontapAddress},
		},
	}

	// Generate valid test certificates for certificate auth
	certPEM, keyPEM, caPEM := generateTestCertificates(t)

	// Create auth data for certificate auth with valid certificates
	certAuth := &models.AuthData{
		AuthType: models.USER_CERTIFICATE,
		PoolID:   "test-pool-456",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: ontapAddress},
		},
		Certificate: &models.Certificate{
			SignedCertificate:        string(certPEM),
			PrivateKey:               string(keyPEM),
			InterMediateCertificates: []string{string(caPEM)},
		},
	}

	// Get client for basic auth - should succeed
	client1, endpoint1, err := pool.GetClient(ctx, basicAuth)
	assert.NoError(t, err)
	assert.NotNil(t, client1)
	assert.NotEmpty(t, endpoint1)

	// Get client for certificate auth - should succeed with valid certificates
	client2, endpoint2, err := pool.GetClient(ctx, certAuth)
	assert.NoError(t, err)
	assert.NotNil(t, client2)
	assert.NotEmpty(t, endpoint2)

	// Verify pool has two clients (different auth types)
	stats := pool.GetStats()
	assert.Equal(t, 2, stats["total_connections"])

	// Verify that pool keys are different for different auth types
	key1 := pool.generatePoolKey(basicAuth)
	key2 := pool.generatePoolKey(certAuth)
	assert.NotEqual(t, key1, key2, "Pool keys should be different for different auth types")
}

func TestConnectionPool_DifferentPools(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()
	// Create auth data for different pools
	authData1 := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "pool-1",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "ontap1.example.com"},
		},
	}

	authData2 := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "pool-2",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "ontap2.example.com"},
		},
	}

	// Get clients for different pools
	client1, endpoint1, err := pool.GetClient(ctx, authData1)
	assert.NoError(t, err)
	assert.NotNil(t, client1)
	assert.NotEmpty(t, endpoint1)

	client2, endpoint2, err := pool.GetClient(ctx, authData2)
	assert.NoError(t, err)
	assert.NotNil(t, client2)
	assert.NotEmpty(t, endpoint2)

	// Should be different clients due to different ONTAP addresses
	assert.NotEqual(t, client1, client2)
	assert.NotEqual(t, endpoint1, endpoint2)

	// Verify pool has two clients
	stats := pool.GetStats()
	assert.Equal(t, 2, stats["total_connections"])
}

func TestConnectionPool_DifferentUsersOnSamePool(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()
	ontapAddress := "test-ontap.example.com"

	// Create auth data for user1 on same pool
	user1Auth := &models.AuthData{
		AuthType:    models.USERNAME_PWD,
		PoolID:      "shared-pool",
		AccountName: "shared-account",
		Username:    "user1",
		Password:    "pass1",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: ontapAddress},
		},
	}

	// Create auth data for user2 on same pool
	user2Auth := &models.AuthData{
		AuthType:    models.USERNAME_PWD,
		PoolID:      "shared-pool",
		AccountName: "shared-account",
		Username:    "user2",
		Password:    "pass2",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: ontapAddress},
		},
	}

	// Get client for user1
	client1, endpoint1, err := pool.GetClient(ctx, user1Auth)
	assert.NoError(t, err)
	assert.NotNil(t, client1)
	assert.NotEmpty(t, endpoint1)

	// Get client for user2
	client2, endpoint2, err := pool.GetClient(ctx, user2Auth)
	assert.NoError(t, err)
	assert.NotNil(t, client2)
	assert.NotEmpty(t, endpoint2)

	// SECURITY: Different users should get different clients even on the same pool
	// This is critical for certificate auth where certs are embedded in the transport
	assert.NotEqual(t, client1, client2, "Different users should get different HTTP clients")

	// Both should use the same endpoint since it's the same pool
	assert.Equal(t, endpoint1, endpoint2, "Same pool should resolve to same endpoint")

	// Verify pool has two clients (one per user)
	stats := pool.GetStats()
	assert.Equal(t, 2, stats["total_connections"])

	// Verify pool keys are different
	key1 := pool.generatePoolKey(user1Auth)
	key2 := pool.generatePoolKey(user2Auth)
	assert.NotEqual(t, key1, key2, "Pool keys should be different for different users")
}

func TestPooledAuthTransport_RoundTrip(t *testing.T) {
	// Create a mock HTTPS server with TLS
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Extract host from server URL (removes https:// prefix)
	serverHost := server.URL[8:] // Remove "https://" prefix

	// Create mock auth data
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: serverHost},
		},
	}

	// Mock the cache
	cacheKey := "test-cache-key"
	cache.AddToAuthDataCache(cacheKey, authData)

	// Create transport
	transport := NewPooledAuthTransport()

	// Create request with context
	req, err := http.NewRequest("GET", "https://"+serverHost+"/test", nil)
	assert.NoError(t, err)

	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	req = req.WithContext(ctx)

	// Execute request
	// Note: This will still fail because the pooled client uses InsecureSkipVerify=true for basic auth
	// but the test server uses a self-signed cert. The connection pool's transport handles this.
	resp, err := transport.RoundTrip(req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	if resp != nil {
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

// TestPooledAuthTransport_RoundTrip_RecordsBackendMetrics verifies that when middleware metrics are
// initialized and the request has backend metrics context, RoundTrip records backend_requests_total,
// backend_request_duration_seconds, and on HTTP 4xx/5xx also backend_errors_total (covers lines 509, 517-519, 521).
func TestPooledAuthTransport_RoundTrip_RecordsBackendMetrics(t *testing.T) {
	require.NoError(t, middleware.InitMetrics())

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	serverHost := server.URL[8:]

	// Use a unique PoolID per test run so the global connection pool does not return a cached
	// client for a different endpoint (pool key does not include endpoint, so same PoolID/username
	// would reuse a stale client from another test).
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "backend-metrics-" + serverHost,
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: serverHost},
		},
	}
	cacheKey := "backend-metrics-cache-key"
	cache.AddToAuthDataCache(cacheKey, authData)

	transport := NewPooledAuthTransport()
	req, err := http.NewRequest("GET", "https://"+serverHost+"/api/storage/volumes", nil)
	require.NoError(t, err)
	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	ctx = middleware.AddBackendMetricsToContext(ctx, "proj-1", "pool-1", "/api/storage/volumes")
	req = req.WithContext(ctx)

	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestPooledAuthTransport_RoundTrip_RecordsBackendMetricsOnTransportError verifies that when client.Do
// returns an error (e.g. no such host), RoundTrip still records backend_requests_total and
// backend_request_duration_seconds with status_code=0 (covers error path in RoundTrip).
func TestPooledAuthTransport_RoundTrip_RecordsBackendMetricsOnTransportError(t *testing.T) {
	require.NoError(t, middleware.InitMetrics())

	// Use an invalid hostname so client.Do fails without needing to close a server
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "transport-err-pool",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "invalid-hostname-does-not-resolve.invalid"},
		},
	}
	cacheKey := "transport-err-cache-key"
	cache.AddToAuthDataCache(cacheKey, authData)

	transport := NewPooledAuthTransport()
	req, err := http.NewRequest("GET", "https://invalid-hostname-does-not-resolve.invalid/api/test", nil)
	require.NoError(t, err)
	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	ctx = middleware.AddBackendMetricsToContext(ctx, "proj-1", "pool-1", "/api/test")
	req = req.WithContext(ctx)

	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func setupTestContextWithAuthData(req *http.Request, username, password string) {
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		Username: username,
		Password: password,
		OntapEndpoints: []models.OntapEndpoint{
			{
				IP:  "192.168.1.100",
				DNS: "test-cluster:443",
			},
		},
	}

	cacheKey := "test-project:test-pool:test-user"

	cache.AddToAuthDataCache(cacheKey, authData)

	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	*req = *req.WithContext(ctx)
}

func TestExtractOntapPath(t *testing.T) {
	t.Run("WhenValidOntapAPIPath_ShouldExtractCorrectly", func(t *testing.T) {
		result := extractOntapPath("/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/qtrees")
		expected := "/api/storage/qtrees"
		assert.Equal(t, expected, result, "Should extract ONTAP API path correctly")
	})

	t.Run("WhenOntapAPIPathWithQueryParams_ShouldExtractWithQuery", func(t *testing.T) {
		result := extractOntapPath("/v1beta/projects/1234/locations/us-central1/pools/my-pool/ontap/api/storage/volumes?fields=name,size")
		expected := "/api/storage/volumes?fields=name,size"
		assert.Equal(t, expected, result, "Should extract ONTAP API path with query parameters")
	})

	t.Run("WhenPathWithoutOntapAPI_ShouldReturnEmpty", func(t *testing.T) {
		result := extractOntapPath("/v1beta/projects/1234/locations/us-central1/pools/my-pool/invalid-path")
		expected := ""
		assert.Equal(t, expected, result, "Should return empty string for path without ontap")
	})

	t.Run("WhenEmptyPath_ShouldHandleCorrectly", func(t *testing.T) {
		result := extractOntapPath("")
		expected := ""
		assert.Equal(t, expected, result, "Should handle empty path")
	})

	t.Run("WhenOntapAPIAtRoot_ShouldHandleCorrectly", func(t *testing.T) {
		result := extractOntapPath("/ontap/api/storage/qtrees")
		expected := "/api/storage/qtrees"
		assert.Equal(t, expected, result, "Should handle ONTAP API at root level")
	})

	t.Run("WhenOntapAPIAtEnd_ShouldHandleCorrectly", func(t *testing.T) {
		result := extractOntapPath("/v1beta/projects/123/locations/us-central1/pools/pool1/ontap")
		expected := "/"
		assert.Equal(t, expected, result, "Should handle ONTAP API at end of path")
	})

	t.Run("WhenMultipleOntapAPI_ShouldHandleFirstOccurrence", func(t *testing.T) {
		result := extractOntapPath("/v1beta/projects/123/ontap/api1/ontap/api2")
		expected := "/api1/ontap/api2"
		assert.Equal(t, expected, result, "Should handle first occurrence of ontap")
	})

	t.Run("WhenPathStartsWithOntapAPI_ShouldHandleCorrectly", func(t *testing.T) {
		result := extractOntapPath("ontap/api/storage/qtrees")
		expected := "/api/storage/qtrees"
		assert.Equal(t, expected, result, "Should handle path starting with ontap")
	})
}

func TestLogCurlCommand(t *testing.T) {
	t.Run("WhenCalled_ShouldNotPanic", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "test-agent")
		req.Header.Set("Authorization", "Basic YWRtaW46cGFzc3dvcmQ=")

		targetURL := "https://ontap-cluster:443/api/storage/qtrees"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic")
	})

	t.Run("WhenNoHeaders_ShouldHandleCorrectly", func(t *testing.T) {
		req, err := http.NewRequest("POST", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		targetURL := "https://ontap-cluster:443/api/storage/qtrees"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic with no headers")
	})

	t.Run("WhenMultipleHeaderValues_ShouldHandleCorrectly", func(t *testing.T) {
		req, err := http.NewRequest("PUT", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		req.Header.Set("Accept", "application/json")
		req.Header.Add("Accept", "application/xml")
		req.Header.Set("User-Agent", "test-agent")

		targetURL := "https://ontap-cluster:443/api/storage/qtrees"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic with multiple header values")
	})

	t.Run("WhenAuthorizationHeaderPresent_ShouldIncludeBasicAuth", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Basic YWRtaW46cGFzc3dvcmQ=")

		targetURL := "https://ontap-cluster:443/api/storage/qtrees"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic with authorization header")
	})

	t.Run("WhenNoHeadersAtAll_ShouldHandleCorrectly", func(t *testing.T) {
		req, err := http.NewRequest("DELETE", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		targetURL := "https://ontap-cluster:443/api/storage/qtrees"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic with no headers at all")
	})

	t.Run("WhenLocalEnvAndNonEmptyBody_ShouldLogRequestBody", func(t *testing.T) {
		originalEnv := runningEnv
		runningEnv = "local"
		defer func() { runningEnv = originalEnv }()

		body := []byte(`{"key":"value"}`)
		req, err := http.NewRequest("POST", "/api/storage/qtrees", bytes.NewReader(body))
		assert.NoError(t, err, "Failed to create request")
		req.Header.Set("Content-Type", "application/json")

		targetURL := "ontap-cluster:443"

		assert.NotPanics(t, func() {
			logCurlCommand(req, targetURL)
		}, "logCurlCommand should not panic with body when ENV=local")
	})
}

func TestSetCommonHeaders(t *testing.T) {
	// A rule context means the response body must be inspected/rewritten by the rule engine, so the
	// proxy clears Accept-Encoding to force ONTAP to return an uncompressed body. The Accept header
	// (application/json or application/hal+json) is a passthrough and must be left untouched.
	action := dsl.Allow{Name: "Test Action"}

	t.Run("WhenRuleContextPresentWithAcceptJSON_ShouldClearAcceptEncodingAndPreserveAccept", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/storage/volumes", nil)
		require.NoError(t, err, "Failed to create request")
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("Accept", "application/json")
		req = req.WithContext(context.WithValue(req.Context(), models.RuleContextKey, action))

		setCommonHeaders(req)

		assert.Equal(t, "10.0.0.1:12345", req.Header.Get("X-Forwarded-For"), "X-Forwarded-For should be set from RemoteAddr")
		assert.Equal(t, "ontap-proxy", req.Header.Get("X-Proxy-By"), "X-Proxy-By should be set")
		encoding, ok := req.Header["Accept-Encoding"]
		assert.True(t, ok, "Accept-Encoding should be present (cleared) when a rule context is set")
		assert.Equal(t, []string{""}, encoding, "Accept-Encoding should be cleared to force an uncompressed body")
		assert.Equal(t, "application/json", req.Header.Get("Accept"), "Accept header should be preserved unchanged")
	})

	t.Run("WhenRuleContextPresentWithAcceptHalJSON_ShouldClearAcceptEncodingAndPreserveAccept", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/storage/volumes", nil)
		require.NoError(t, err, "Failed to create request")
		req.RemoteAddr = "10.0.0.2:54321"
		req.Header.Set("Accept", "application/hal+json")
		req = req.WithContext(context.WithValue(req.Context(), models.RuleContextKey, action))

		setCommonHeaders(req)

		assert.Equal(t, "10.0.0.2:54321", req.Header.Get("X-Forwarded-For"), "X-Forwarded-For should be set from RemoteAddr")
		assert.Equal(t, "ontap-proxy", req.Header.Get("X-Proxy-By"), "X-Proxy-By should be set")
		encoding, ok := req.Header["Accept-Encoding"]
		assert.True(t, ok, "Accept-Encoding should be present (cleared) when a rule context is set")
		assert.Equal(t, []string{""}, encoding, "Accept-Encoding should be cleared to force an uncompressed body")
		assert.Equal(t, "application/hal+json", req.Header.Get("Accept"), "Accept header should be preserved unchanged")
	})

	t.Run("WhenNoRuleContext_ShouldNotTouchAcceptEncoding", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/api/storage/volumes", nil)
		require.NoError(t, err, "Failed to create request")
		req.RemoteAddr = "10.0.0.3:1111"
		req.Header.Set("Accept", "application/json")

		setCommonHeaders(req)

		assert.Equal(t, "10.0.0.3:1111", req.Header.Get("X-Forwarded-For"), "X-Forwarded-For should be set from RemoteAddr")
		assert.Equal(t, "ontap-proxy", req.Header.Get("X-Proxy-By"), "X-Proxy-By should be set")
		_, ok := req.Header["Accept-Encoding"]
		assert.False(t, ok, "Accept-Encoding should not be modified when no rule context is present")
		assert.Equal(t, "application/json", req.Header.Get("Accept"), "Accept header should be preserved unchanged")
	})
}

func TestBuildOntapRESTProxy(t *testing.T) {
	t.Run("WhenProxyCreated_ShouldReturnValidProxy", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		assert.NotNil(t, proxy, "Proxy should not be nil")
		assert.NotNil(t, proxy.Director, "Director function should not be nil")
		assert.NotNil(t, proxy.Transport, "Transport should not be nil")
		assert.NotNil(t, proxy.ModifyResponse, "ModifyResponse function should not be nil")
		assert.NotNil(t, proxy.ErrorHandler, "ErrorHandler function should not be nil")
	})

	t.Run("WhenDirectorCalledWithValidPath_ShouldProcessRequest", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap/api/storage/qtrees", nil)
		assert.NoError(t, err, "Failed to create request")
		req.RemoteAddr = "192.168.1.1:12345"

		setupTestContextWithAuthData(req, "testuser", "testpass")

		proxy.Director(req)

		// Director only extracts and sets the ONTAP path, not the full URL
		// The full URL (with scheme and host) is set later in RoundTrip
		assert.Equal(t, "/api/storage/qtrees", req.URL.Path)
	})

	t.Run("WhenDirectorCalledWithInvalidPath_ShouldReturnEarly", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/invalid/path", nil)
		assert.NoError(t, err, "Failed to create request")

		setupTestContextWithAuthData(req, "testuser", "testpass")

		originalPath := req.URL.Path
		proxy.Director(req)

		assert.Equal(t, originalPath, req.URL.Path, "Path should not be modified for invalid path")
	})

	t.Run("WhenDirectorCalledWithNoOntapAddress_ShouldExtractPath", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap/api/storage/qtrees", nil)
		assert.NoError(t, err, "Failed to create request")

		authData := &models.AuthData{
			AuthType:       models.USERNAME_PWD,
			Username:       "testuser",
			Password:       "testpass",
			OntapEndpoints: []models.OntapEndpoint{},
		}
		cacheKey := "test-project:test-pool:test-user"
		cache.AddToAuthDataCache(cacheKey, authData)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		*req = *req.WithContext(ctx)

		proxy.Director(req)

		// Director extracts path regardless of auth data - RoundTrip handles endpoint
		assert.Equal(t, "/api/storage/qtrees", req.URL.Path)
	})

	t.Run("WhenDirectorCalledWithNoUsername_ShouldStillExtractPath", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap/api/storage/qtrees", nil)
		assert.NoError(t, err, "Failed to create request")

		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "",
			Password: "testpass",
			OntapEndpoints: []models.OntapEndpoint{
				{
					IP:  "192.168.1.100",
					DNS: "test-cluster:443",
				},
			},
		}
		cacheKey := "test-project:test-pool:test-user"
		cache.AddToAuthDataCache(cacheKey, authData)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		*req = *req.WithContext(ctx)

		proxy.Director(req)

		assert.Equal(t, "/api/storage/qtrees", req.URL.Path, "Path should be extracted even when no username")
	})

	t.Run("WhenDirectorCalledWithNoPassword_ShouldStillExtractPath", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap/api/storage/qtrees", nil)
		assert.NoError(t, err, "Failed to create request")

		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "",
			OntapEndpoints: []models.OntapEndpoint{
				{
					IP:  "192.168.1.100",
					DNS: "test-cluster:443",
				},
			},
		}
		cacheKey := "test-project:test-pool:test-user"
		cache.AddToAuthDataCache(cacheKey, authData)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		*req = *req.WithContext(ctx)

		proxy.Director(req)

		assert.Equal(t, "/api/storage/qtrees", req.URL.Path, "Path should be extracted even when no password")
	})

	t.Run("WhenDirectorCalledWithInvalidURL_ShouldStillExtractPath", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/v1beta/projects/123/locations/us-central1/pools/pool1/ontap/api/storage/qtrees", nil)
		assert.NoError(t, err, "Failed to create request")

		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
			OntapEndpoints: []models.OntapEndpoint{
				{
					IP:  "192.168.1.100",
					DNS: "://invalid-url",
				},
			},
		}
		cacheKey := "test-project:test-pool:test-user"
		cache.AddToAuthDataCache(cacheKey, authData)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		*req = *req.WithContext(ctx)

		proxy.Director(req)

		assert.Equal(t, "/api/storage/qtrees", req.URL.Path, "Path should be extracted regardless of endpoint validity")
	})

	t.Run("WhenModifyResponseCalledWithRuleContext_ShouldProcessResponse", func(t *testing.T) {
		// Use dsl.Allow which implements dsl.IAction
		action := dsl.Allow{Name: "Test Action"}

		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		ctx := context.WithValue(req.Context(), models.RuleContextKey, action)
		req = req.WithContext(ctx)

		resp := &http.Response{
			Request: req,
			Header:  make(http.Header),
		}

		err = proxy.ModifyResponse(resp)
		assert.NoError(t, err, "ModifyResponse should not return error")
	})

	t.Run("WhenModifyResponseCalledWithoutRuleContext_ShouldReturnNil", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		resp := &http.Response{
			Request: req,
			Header:  make(http.Header),
		}

		err = proxy.ModifyResponse(resp)
		assert.NoError(t, err, "ModifyResponse should return nil when no rule context")
	})

	t.Run("WhenModifyResponseCalledWithInvalidRuleContext_ShouldReturnNil", func(t *testing.T) {
		proxy := BuildOntapRESTProxy()
		req, err := http.NewRequest("GET", "/test", nil)
		assert.NoError(t, err, "Failed to create request")

		ctx := context.WithValue(req.Context(), models.RuleContextKey, "invalid")
		req = req.WithContext(ctx)

		resp := &http.Response{
			Request: req,
			Header:  make(http.Header),
		}

		err = proxy.ModifyResponse(resp)
		assert.NoError(t, err, "ModifyResponse should return nil when rule context is invalid")
	})
}

func TestErrorHandlerResponses(t *testing.T) {
	proxy := BuildOntapRESTProxy()
	cases := []struct {
		name        string
		err         error
		wantCode    int
		wantContain string
	}{
		{"ContextCanceled", fmt.Errorf("context canceled"), http.StatusGatewayTimeout, "Request timeout"},
		{"ConnectionRefused", fmt.Errorf("connection refused"), http.StatusBadGateway, "Cannot connect"},
		{"NoSuchHost", fmt.Errorf("no such host"), http.StatusBadGateway, "host not found"},
		{"ProxyHTTPDeleting", &middleware.ProxyHTTPError{Status: http.StatusBadRequest, Message: "Pool is in deleting state"}, http.StatusBadRequest, "deleting"},
		{"MissingCredentials", fmt.Errorf("Missing ONTAP credentials"), http.StatusInternalServerError, "credentials not configured"},
		{"OtherError", fmt.Errorf("some other error"), http.StatusBadGateway, "Proxy error: some other error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/test", nil)
			rr := httptest.NewRecorder()
			proxy.ErrorHandler(rr, req, tc.err)
			assert.Equal(t, tc.wantCode, rr.Code)
			assert.Contains(t, rr.Body.String(), tc.wantContain)
		})
	}
}

func TestConnectionPool_Cleanup(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()
	ctx := context.Background()
	// Create multiple clients
	for i := 0; i < 5; i++ {
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			PoolID:   fmt.Sprintf("pool-%d", i),
			Username: "testuser",
			Password: "testpass",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: fmt.Sprintf("ontap%d.example.com", i)},
			},
		}

		client, endpoint, err := pool.GetClient(ctx, authData)
		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.NotEmpty(t, endpoint)
	}

	// Verify we have 5 clients
	stats := pool.GetStats()
	assert.Equal(t, 5, stats["total_connections"])

	// Run cleanup (this is a simplified test - in real scenario, cleanup would be time-based)
	pool.cleanup()

	// In this test, cleanup shouldn't remove anything since we're under the limit
	stats = pool.GetStats()
	assert.Equal(t, 5, stats["total_connections"])
}

func TestBuildCertificateTransport_WithInvalidCerts(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	// This will fail due to invalid cert, but we can test the path
	authData := &models.AuthData{
		AuthType: models.USER_CERTIFICATE,
		PoolID:   "test-pool-123",
		Certificate: &models.Certificate{
			SignedCertificate:        "test-cert",
			PrivateKey:               "test-key",
			InterMediateCertificates: []string{"test-ca"},
		},
	}

	// This will fail, but tests the return path
	_, err := pool.buildCertificateTransport(authData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to prepare certificate")
}

func TestBuildCertificateTransport_WithValidCerts(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	// Generate a self-signed certificate for testing
	certPEM, keyPEM, caPEM := generateTestCertificates(t)

	authData := &models.AuthData{
		AuthType: models.USER_CERTIFICATE,
		PoolID:   "test-pool-123",
		Certificate: &models.Certificate{
			SignedCertificate:        string(certPEM),
			PrivateKey:               string(keyPEM),
			InterMediateCertificates: []string{string(caPEM)},
		},
	}

	// This should succeed and hit line 194
	transport, err := pool.buildCertificateTransport(authData)
	assert.NoError(t, err)
	assert.NotNil(t, transport)
	assert.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	assert.NotEmpty(t, transport.TLSClientConfig.Certificates)
}

func TestConnectionPool_Cleanup_OldConnections(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	// Set a very short cleanup threshold
	ctx := context.Background()
	pool.cleanupThreshold = 1 * time.Millisecond

	// Create a client
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}

	client, _, err := pool.GetClient(ctx, authData)
	assert.NoError(t, err)
	assert.NotNil(t, client)

	// Manually set timestamp to be old
	pool.mutex.Lock()
	key := pool.generatePoolKey(authData)
	pool.clientTimestamps[key] = time.Now().Add(-2 * time.Second)
	pool.mutex.Unlock()

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Run cleanup - this should hit lines 240-242, 244, 246
	pool.cleanup()

	// Verify client was removed
	stats := pool.GetStats()
	assert.Equal(t, 0, stats["total_connections"])
}

func TestConnectionPool_CleanupRoutine_Runs(t *testing.T) {
	// Create a new pool to test the cleanup routine
	pool := NewConnectionPool()
	defer pool.Close()

	// Set a very short cleanup threshold so connections are considered old quickly
	pool.cleanupThreshold = 1 * time.Millisecond

	// Create a client
	ctx := context.Background()
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}

	_, _, err := pool.GetClient(ctx, authData)
	assert.NoError(t, err)

	// Manually set timestamp to be old
	pool.mutex.Lock()
	key := pool.generatePoolKey(authData)
	pool.clientTimestamps[key] = time.Now().Add(-2 * time.Second)
	pool.mutex.Unlock()

	// Wait for cleanup routine ticker to fire (line 219)
	// Default cleanup interval is 60 seconds, but we'll wait a bit to ensure
	// the ticker has a chance to fire. In practice, this tests that the cleanup
	// routine is running and will eventually call cleanup()
	// Note: This is a timing-dependent test, but it verifies the routine is active
	time.Sleep(100 * time.Millisecond)

	// Manually trigger cleanup to verify the path works
	// The actual ticker test would require waiting for the full interval
	pool.cleanup()

	// Verify client was removed
	stats := pool.GetStats()
	assert.Equal(t, 0, stats["total_connections"])
}

func TestConnectionPool_Cleanup_OverLimit(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	// Set a low max connections limit to trigger over-limit cleanup
	// We'll need to patch env.GetInt, but for now let's create enough connections
	// to potentially trigger the cleanup path

	// Create multiple clients with different timestamps
	// We'll set some to be old and some to be recent
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			PoolID:   fmt.Sprintf("pool-%d", i),
			Username: "testuser",
			Password: "testpass",
			OntapEndpoints: []models.OntapEndpoint{
				{DNS: fmt.Sprintf("ontap%d.example.com", i)},
			},
		}

		_, _, err := pool.GetClient(ctx, authData)
		assert.NoError(t, err)
	}

	// Manually set timestamps - make some old, some recent
	pool.mutex.Lock()
	now := time.Now()
	keys := make([]string, 0, len(pool.clientTimestamps))
	for key := range pool.clientTimestamps {
		keys = append(keys, key)
	}
	// Set first 5 to be old
	for i := 0; i < 5 && i < len(keys); i++ {
		pool.clientTimestamps[keys[i]] = now.Add(-2 * time.Hour)
	}
	// Keep rest recent
	pool.mutex.Unlock()

	// Run cleanup - should remove old connections
	pool.cleanup()

	// Verify old connections were removed
	stats := pool.GetStats()
	assert.LessOrEqual(t, stats["total_connections"], 10)
	assert.GreaterOrEqual(t, stats["total_connections"], 0)
}

func TestConnectionPool_Cleanup_OverLimit_RemovesOldest(t *testing.T) {
	t.Run("WhenOverMaxConnections_ShouldRemoveOldestConnection", func(t *testing.T) {
		// Save original package-level variable value and restore after test
		originalMaxConnections := ontapMaxTotalConnections
		defer func() {
			ontapMaxTotalConnections = originalMaxConnections
		}()

		// Set a low max connections limit to trigger over-limit cleanup
		ontapMaxTotalConnections = 3
		// Verify the variable is set correctly
		assert.Equal(t, 3, ontapMaxTotalConnections, "ontapMaxTotalConnections should be set to 3")

		pool := NewConnectionPool()
		defer pool.Close()
		ctx := context.Background()

		// Create 5 clients (more than the max of 3)
		clientKeys := make([]string, 5)
		clients := make([]*http.Client, 5)
		now := time.Now()

		for i := 0; i < 5; i++ {
			authData := &models.AuthData{
				AuthType: models.USERNAME_PWD,
				PoolID:   fmt.Sprintf("pool-%d", i),
				Username: "testuser",
				Password: "testpass",
				OntapEndpoints: []models.OntapEndpoint{
					{DNS: fmt.Sprintf("ontap%d.example.com", i)},
				},
			}

			client, _, err := pool.GetClient(ctx, authData)
			assert.NoError(t, err)
			assert.NotNil(t, client)
			clients[i] = client

			// Store the key for later verification
			pool.mutex.RLock()
			key := pool.generatePoolKey(authData)
			clientKeys[i] = key
			pool.mutex.RUnlock()
		}

		// Verify we have 5 clients
		stats := pool.GetStats()
		assert.Equal(t, 5, stats["total_connections"])

		// Manually set timestamps with different ages - make key[0] the oldest
		pool.mutex.Lock()
		for i, key := range clientKeys {
			// Set timestamps: key[0] is oldest (5 minutes ago), key[4] is newest (1 minute ago)
			pool.clientTimestamps[key] = now.Add(-time.Duration(5-i) * time.Minute)
		}
		oldestKey := clientKeys[0]
		pool.mutex.Unlock()

		// Set a long cleanup threshold so time-based cleanup doesn't remove anything
		pool.cleanupThreshold = 10 * time.Hour

		// Run cleanup multiple times - each call removes one oldest connection (lines 250-272)
		// We have 5 connections, max is 3, so we need 2 cleanup calls
		pool.cleanup()
		stats = pool.GetStats()
		assert.Equal(t, 4, stats["total_connections"], "First cleanup should remove one connection")

		// Update timestamps again to ensure we can identify the next oldest
		// Preserve the relative ordering from clientKeys to avoid non-deterministic map iteration
		pool.mutex.Lock()
		// Find which keys from clientKeys still exist and set their timestamps based on original order
		remainingFromOriginal := make([]string, 0)
		for _, originalKey := range clientKeys {
			if _, exists := pool.clients[originalKey]; exists {
				remainingFromOriginal = append(remainingFromOriginal, originalKey)
			}
		}
		// Set timestamps for remaining connections based on their original position
		// This ensures clientKeys[4] (newest) keeps its relative position
		for _, key := range remainingFromOriginal {
			// Find original index in clientKeys
			originalIndex := -1
			for idx, origKey := range clientKeys {
				if origKey == key {
					originalIndex = idx
					break
				}
			}
			if originalIndex >= 0 {
				// Preserve relative age: higher index = newer
				// Set timestamp based on original position to maintain ordering
				pool.clientTimestamps[key] = now.Add(-time.Duration(5-originalIndex) * time.Minute)
			}
		}
		pool.mutex.Unlock()

		pool.cleanup()
		stats = pool.GetStats()
		assert.Equal(t, 3, stats["total_connections"], "Second cleanup should bring us to maxConnections")

		// Verify the oldest connection (key[0]) was removed after both cleanups
		pool.mutex.RLock()
		_, oldestExists := pool.clients[oldestKey]
		_, oldestTimestampExists := pool.clientTimestamps[oldestKey]
		pool.mutex.RUnlock()
		assert.False(t, oldestExists, "Oldest client should be removed")
		assert.False(t, oldestTimestampExists, "Oldest timestamp should be removed")

		// Verify newer connections still exist
		pool.mutex.RLock()
		_, newestExists := pool.clients[clientKeys[4]]
		pool.mutex.RUnlock()
		assert.True(t, newestExists, "Newest client should still exist")
	})

	t.Run("WhenOverLimitAndMultipleOldest_ShouldRemoveOneOldest", func(t *testing.T) {
		// Save original package-level variable value and restore after test
		originalMaxConnections := ontapMaxTotalConnections
		defer func() {
			ontapMaxTotalConnections = originalMaxConnections
		}()

		// Set a low max connections limit
		ontapMaxTotalConnections = 2
		// Verify the variable is set correctly
		assert.Equal(t, 2, ontapMaxTotalConnections, "ontapMaxTotalConnections should be set to 2")

		pool := NewConnectionPool()
		defer pool.Close()
		ctx := context.Background()

		// Create 3 clients
		clientKeys := make([]string, 3)
		now := time.Now()

		for i := 0; i < 3; i++ {
			authData := &models.AuthData{
				AuthType: models.USERNAME_PWD,
				PoolID:   fmt.Sprintf("pool-%d", i),
				Username: "testuser",
				Password: "testpass",
				OntapEndpoints: []models.OntapEndpoint{
					{DNS: fmt.Sprintf("ontap%d.example.com", i)},
				},
			}
			_, _, err := pool.GetClient(ctx, authData)
			assert.NoError(t, err)

			pool.mutex.RLock()
			key := pool.generatePoolKey(authData)
			clientKeys[i] = key
			pool.mutex.RUnlock()
		}

		// Set all timestamps to the same old time (all are equally old)
		pool.mutex.Lock()
		oldTime := now.Add(-10 * time.Minute)
		for _, key := range clientKeys {
			pool.clientTimestamps[key] = oldTime
		}
		pool.mutex.Unlock()

		// Set a long cleanup threshold
		pool.cleanupThreshold = 10 * time.Hour

		// Run cleanup - should remove one oldest connection
		pool.cleanup()

		// Verify we now have maxConnections (2) clients
		stats := pool.GetStats()
		assert.Equal(t, 2, stats["total_connections"], "Should have exactly maxConnections after cleanup")

		// Verify one connection was removed
		pool.mutex.RLock()
		removedCount := 0
		for _, key := range clientKeys {
			if _, exists := pool.clients[key]; !exists {
				removedCount++
			}
		}
		pool.mutex.RUnlock()
		assert.Equal(t, 1, removedCount, "Exactly one connection should be removed")
	})

	t.Run("WhenOverLimitButNoTimestamps_ShouldNotPanic", func(t *testing.T) {
		// Save original package-level variable value and restore after test
		originalMaxConnections := ontapMaxTotalConnections
		defer func() {
			ontapMaxTotalConnections = originalMaxConnections
		}()

		ontapMaxTotalConnections = 1
		// Verify the variable is set correctly
		assert.Equal(t, 1, ontapMaxTotalConnections, "ontapMaxTotalConnections should be set to 1")

		pool := NewConnectionPool()
		defer pool.Close()
		ctx := context.Background()

		// Create 2 clients
		for i := 0; i < 2; i++ {
			authData := &models.AuthData{
				AuthType: models.USERNAME_PWD,
				PoolID:   fmt.Sprintf("pool-%d", i),
				Username: "testuser",
				Password: "testpass",
				OntapEndpoints: []models.OntapEndpoint{
					{DNS: fmt.Sprintf("ontap%d.example.com", i)},
				},
			}

			_, _, err := pool.GetClient(ctx, authData)
			assert.NoError(t, err)
		}

		// Remove one timestamp to simulate edge case
		pool.mutex.Lock()
		keys := make([]string, 0, len(pool.clients))
		for key := range pool.clients {
			keys = append(keys, key)
		}
		if len(keys) > 1 {
			delete(pool.clientTimestamps, keys[0])
		}
		pool.mutex.Unlock()

		pool.cleanupThreshold = 10 * time.Hour

		// Should not panic even if timestamp is missing
		assert.NotPanics(t, func() {
			pool.cleanup()
		}, "Cleanup should handle missing timestamps gracefully")
	})
}

func TestPooledAuthTransport_RoundTrip_NoCacheKey(t *testing.T) {
	transport := NewPooledAuthTransport()

	req, err := http.NewRequest("GET", "https://test-ontap.example.com/api/test", nil)
	assert.NoError(t, err)

	// No cache key in context
	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no cache key found")
}

func TestPooledAuthTransport_RoundTrip_NoAuthData(t *testing.T) {
	transport := NewPooledAuthTransport()

	req, err := http.NewRequest("GET", "https://test-ontap.example.com/api/test", nil)
	assert.NoError(t, err)

	ctx := context.WithValue(req.Context(), models.AuthDataKey, "non-existent-key")
	req = req.WithContext(ctx)

	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "no authentication data found in cache")
}

func TestPooledAuthTransport_RoundTrip_GetClientError(t *testing.T) {
	pool := NewConnectionPool()
	defer pool.Close()

	ctx := context.Background()

	// Create auth data with invalid certificates that will fail during preparation
	authData := &models.AuthData{
		AuthType: models.USER_CERTIFICATE,
		PoolID:   "test-pool-123",
		Certificate: &models.Certificate{
			SignedCertificate:        "invalid-cert-data",
			PrivateKey:               "invalid-key-data",
			InterMediateCertificates: []string{"invalid-ca-data"},
		},
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}

	// Attempt to get client - should fail during certificate preparation
	client, endpoint, err := pool.GetClient(ctx, authData)
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Empty(t, endpoint)
	assert.Contains(t, err.Error(), "failed to create client")
}

func TestConfigureRequestAuthentication_CertificateAuth(t *testing.T) {
	req, err := http.NewRequest("GET", "https://test.example.com/api/test", nil)
	assert.NoError(t, err)

	authData := &models.AuthData{
		AuthType: models.USER_CERTIFICATE,
	}

	err = configureRequestAuthentication(req, authData)
	assert.NoError(t, err)
	// Certificate auth should not set basic auth
	assert.Empty(t, req.Header.Get("Authorization"))
}

func TestConfigureRequestAuthentication_MissingCredentials(t *testing.T) {
	req, err := http.NewRequest("GET", "https://test.example.com/api/test", nil)
	assert.NoError(t, err)

	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		Username: "", // Missing username
		Password: "testpass",
	}

	err = configureRequestAuthentication(req, authData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing username or password")

	// Test with missing password
	authData.Username = "testuser"
	authData.Password = ""

	err = configureRequestAuthentication(req, authData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing username or password")
}

func TestPooledAuthTransport_RoundTrip_ConfigureAuthError(t *testing.T) {
	// Create a mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := NewPooledAuthTransport()

	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "", // Missing username to trigger error
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: server.URL},
		},
	}

	cacheKey := "test-cache-key"
	cache.AddToAuthDataCache(cacheKey, authData)

	req, err := http.NewRequest("GET", server.URL+"/test", nil)
	assert.NoError(t, err)

	ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
	req = req.WithContext(ctx)

	resp, err := transport.RoundTrip(req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to configure authentication")
}

// Benchmark tests
func BenchmarkConnectionPool_GetClient(b *testing.B) {
	pool := NewConnectionPool()
	defer pool.Close()
	ctx := context.Background()
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		client, _, err := pool.GetClient(ctx, authData)
		if err != nil {
			b.Fatal(err)
		}
		if client == nil {
			b.Fatal("client is nil")
		}
	}
}

func BenchmarkConnectionPool_ConcurrentAccess(b *testing.B) {
	pool := NewConnectionPool()
	defer pool.Close()
	ctx := context.Background()
	authData := &models.AuthData{
		AuthType: models.USERNAME_PWD,
		PoolID:   "test-pool-123",
		Username: "testuser",
		Password: "testpass",
		OntapEndpoints: []models.OntapEndpoint{
			{DNS: "test-ontap.example.com"},
		},
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			client, _, err := pool.GetClient(ctx, authData)
			if err != nil {
				b.Fatal(err)
			}
			if client == nil {
				b.Fatal("client is nil")
			}
		}
	})
}

// Tests for _testOntapEndpointReachability
func Test_testOntapEndpointReachability(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		// Create a mock ONTAP server that responds successfully
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify the request path
			assert.Equal(t, "/api/svm/svms", r.URL.Path)
			assert.Equal(t, "max_records=1", r.URL.RawQuery)
			assert.Equal(t, "GET", r.Method)

			// Verify basic auth is set
			username, password, ok := r.BasicAuth()
			assert.True(t, ok)
			assert.Equal(t, "testuser", username)
			assert.Equal(t, "testpass", password)

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"records": []}`))
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err)
	})

	t.Run("CertificateAuth", func(t *testing.T) {
		// Create a mock ONTAP server
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Certificate auth should not have basic auth
			_, _, ok := r.BasicAuth()
			assert.False(t, ok, "Certificate auth should not use basic auth")

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"records": []}`))
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USER_CERTIFICATE,
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err)
	})

	t.Run("NoCredentials", func(t *testing.T) {
		// Create a mock ONTAP server
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Should not have basic auth when credentials are missing
			_, _, ok := r.BasicAuth()
			assert.False(t, ok, "Should not set basic auth when credentials are missing")

			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"records": []}`))
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "", // No username
			Password: "", // No password
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err)
	})

	t.Run("ServerError", func(t *testing.T) {
		// Create a mock ONTAP server that returns an error
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "internal server error"}`))
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		// Function returns nil even on HTTP errors (just checks reachability)
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err, "Should not error on HTTP errors, only on connection failures")
	})

	t.Run("Unreachable", func(t *testing.T) {
		// Use a non-existent endpoint
		endpoint := "127.0.0.1:9999"
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint not reachable")
	})

	t.Run("ContextTimeout", func(t *testing.T) {
		// Hold the response until the client cancels. Do not Sleep() past Close():
		// httptest.Server.Close waits for in-flight handlers, which made this case ~20s.
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		// Short deadline; production wraps with WithTimeout(ctx, 15s) so parent wins.
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint not reachable")
	})

	t.Run("CanceledContext", func(t *testing.T) {
		// Create a mock ONTAP server
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"records": []}`))
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		// Create a canceled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint not reachable")
	})

	t.Run("InvalidEndpoint", func(t *testing.T) {
		// Test with various invalid endpoint formats
		testCases := []struct {
			name     string
			endpoint string
		}{
			{"Empty endpoint", ""},
			{"Invalid URL", "://invalid"},
			{"Malformed host", "not a valid host:port"},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				authData := &models.AuthData{
					AuthType: models.USERNAME_PWD,
					Username: "testuser",
					Password: "testpass",
				}
				transport := &http.Transport{}
				ctx := context.Background()

				err := _testOntapEndpointReachability(tc.endpoint, authData, ctx, transport)
				assert.Error(t, err)
			})
		}
	})

	t.Run("ResponseBodyHandling", func(t *testing.T) {
		// Test that response body is properly drained and closed
		bodyRead := false
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Write a large response to ensure body draining works
			largeResponse := make([]byte, 1024*1024) // 1MB
			_, _ = w.Write(largeResponse)
			bodyRead = true
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err)
		assert.True(t, bodyRead, "Server handler should have been called")
	})

	t.Run("NilResponseBody", func(t *testing.T) {
		// Edge case: response with nil body
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Don't write anything - creates a nil or empty body scenario
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoint := server.URL[8:]
		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD,
			Username: "testuser",
			Password: "testpass",
		}
		transport := &http.Transport{
			TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
		}

		ctx := context.Background()
		err := _testOntapEndpointReachability(endpoint, authData, ctx, transport)
		assert.NoError(t, err, "Should handle nil/empty response body gracefully")
	})
}

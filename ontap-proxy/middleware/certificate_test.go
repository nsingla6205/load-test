package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalergoogle "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

var originalGetGCPService = hyperscaler.GetGCPService
var originalGetCertificateAndPrivateKeyByID = vsa.GetCertificateAndPrivateKeyByID
var originalGetPasswordFromCacheOrSecretManager = vsa.GetPasswordFromCacheOrSecretManager

func setupMocks() {
	hyperscaler.GetGCPService = func(ctx context.Context) (*hyperscalergoogle.GcpServices, error) {
		return &hyperscalergoogle.GcpServices{}, nil
	}
	vsa.GetCertificateAndPrivateKeyByID = func(gcpService hyperscaler.Services, caDeployedProjectID, secretManagerProjectID, region, caPoolName, certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
		return &hyperscalermodels.CustomCertificateResponse{
			Certificate: &hyperscalermodels.CustomCertificate{
				PemCertificate:      "-----BEGIN CERTIFICATE-----\nMOCK_CERTIFICATE\n-----END CERTIFICATE-----",
				SubjectCommonName:   "test-cert.example.com",
				PemCertificateChain: []string{"-----BEGIN CERTIFICATE-----\nMOCK_CHAIN\n-----END CERTIFICATE-----"},
			},
			Secret: &hyperscalermodels.CustomSecret{
				SecretVersion: &hyperscalermodels.CustomSecretVersion{
					Value: "-----BEGIN PRIVATE KEY-----\nMOCK_PRIVATE_KEY\n-----END PRIVATE KEY-----",
				},
			},
		}, nil
	}
	vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
		return "mock-password", nil
	}
}

func restoreMocks() {
	hyperscaler.GetGCPService = originalGetGCPService
	vsa.GetCertificateAndPrivateKeyByID = originalGetCertificateAndPrivateKeyByID
	vsa.GetPasswordFromCacheOrSecretManager = originalGetPasswordFromCacheOrSecretManager
}

func TestCertificateMiddleware(t *testing.T) {
	t.Run("WhenNoCacheKeyInContext_ShouldReturn500", func(t *testing.T) {
		middleware := CertificateMiddleware()

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Cache key not available")
	})

	t.Run("WhenNoAuthDataInCache_ShouldReturn500", func(t *testing.T) {
		middleware := CertificateMiddleware()

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, "test-key")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		cache.RemoveFromAuthDataCache("test-key")

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Authentication data not available in cache")
	})

	t.Run("WhenAuthTypeIsUSER_CERTIFICATE_WithExistingCertificate_ShouldCallNext", func(t *testing.T) {
		middleware := CertificateMiddleware()

		authData := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			CertificateID: "test-cert-id",
			Certificate: &models.Certificate{
				SignedCertificate: "existing-cert",
				CommonName:        "existing.example.com",
			},
			PoolID: "test-pool",
		}

		cacheKey := "test-key"
		cache.AddToAuthDataCache(cacheKey, authData)

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		nextHandlerCalled := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextHandlerCalled = true
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.True(t, nextHandlerCalled, "Next handler should be called")
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenAuthTypeIsUSER_CERTIFICATE_WithMissingCertificate_ShouldFetchAndUpdateCache", func(t *testing.T) {
		setupMocks()
		defer restoreMocks()

		middleware := CertificateMiddleware()

		authData := &models.AuthData{
			AuthType:      models.USER_CERTIFICATE,
			CertificateID: "test-cert-id",
			Certificate:   nil, // No certificate
			PoolID:        "test-pool",
		}

		cacheKey := "test-key"
		cache.AddToAuthDataCache(cacheKey, authData)

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		nextHandlerCalled := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextHandlerCalled = true
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.True(t, nextHandlerCalled, "Next handler should be called")
		assert.Equal(t, http.StatusOK, w.Code)

		updatedAuthData, exists := cache.GetFromAuthDataCache(cacheKey)
		assert.True(t, exists, "Auth data should exist in cache")
		assert.NotNil(t, updatedAuthData.Certificate, "Certificate should be added to auth data")
		assert.Equal(t, "test-cert.example.com", updatedAuthData.Certificate.CommonName, "Certificate common name should match")
	})

	t.Run("WhenAuthTypeIsUSERNAME_PWD_SEC_MGR_WithExistingPassword_ShouldCallNext", func(t *testing.T) {
		middleware := CertificateMiddleware()

		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD_SEC_MGR,
			SecretID: "test-secret-id",
			Password: "existing-password",
			PoolID:   "test-pool",
		}

		cacheKey := "test-key"
		cache.AddToAuthDataCache(cacheKey, authData)

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		nextHandlerCalled := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextHandlerCalled = true
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.True(t, nextHandlerCalled, "Next handler should be called")
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("WhenAuthTypeIsUSERNAME_PWD_SEC_MGR_WithMissingPassword_ShouldFetchAndUpdateCache", func(t *testing.T) {
		setupMocks()
		defer restoreMocks()

		middleware := CertificateMiddleware()

		authData := &models.AuthData{
			AuthType: models.USERNAME_PWD_SEC_MGR,
			SecretID: "test-secret-id",
			Password: "", // No password
			PoolID:   "test-pool",
		}

		cacheKey := "test-key"
		cache.AddToAuthDataCache(cacheKey, authData)

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		nextHandlerCalled := false
		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextHandlerCalled = true
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.True(t, nextHandlerCalled, "Next handler should be called")
		assert.Equal(t, http.StatusOK, w.Code)

		updatedAuthData, exists := cache.GetFromAuthDataCache(cacheKey)
		assert.True(t, exists, "Auth data should exist in cache")
		assert.Equal(t, "mock-password", updatedAuthData.Password, "Password should be added to auth data")
	})

	t.Run("WhenAuthTypeDoesNotRequireFetching_ShouldCallNext", func(t *testing.T) {
		testCases := []struct {
			name     string
			authData *models.AuthData
		}{
			{
				name: "USERNAME_PWD",
				authData: &models.AuthData{
					AuthType: models.USERNAME_PWD,
					Username: "testuser",
					Password: "testpass",
					PoolID:   "test-pool",
				},
			},
			{
				name: "USER_CERTIFICATE_WithEmptyCertificateID",
				authData: &models.AuthData{
					AuthType:      models.USER_CERTIFICATE,
					CertificateID: "", // Empty certificate ID
					Certificate:   nil,
					PoolID:        "test-pool",
				},
			},
			{
				name: "USERNAME_PWD_SEC_MGR_WithEmptySecretID",
				authData: &models.AuthData{
					AuthType: models.USERNAME_PWD_SEC_MGR,
					SecretID: "", // Empty secret ID
					Password: "",
					PoolID:   "test-pool",
				},
			},
			{
				name: "UnknownAuthType",
				authData: &models.AuthData{
					AuthType: 999, // Unknown auth type
					PoolID:   "test-pool",
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				middleware := CertificateMiddleware()

				cacheKey := "test-key"
				cache.AddToAuthDataCache(cacheKey, tc.authData)

				req := httptest.NewRequest("GET", "/test", nil)
				ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
				req = req.WithContext(ctx)
				w := httptest.NewRecorder()

				nextHandlerCalled := false
				nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					nextHandlerCalled = true
				})

				middleware(nextHandler).ServeHTTP(w, req)

				assert.True(t, nextHandlerCalled, "Next handler should be called for %s", tc.name)
				assert.Equal(t, http.StatusOK, w.Code, "Should return 200 for %s", tc.name)
			})
		}
	})
}

func TestGetCertificateFromSecretManager(t *testing.T) {
	t.Run("WhenGCPServiceSucceeds_ShouldReturnCertificate", func(t *testing.T) {
		setupMocks()
		defer restoreMocks()

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting certificate from secret manager", "certificateID", "test-cert-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "test-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "test-pool-name").Return()
		logger.On("InfoContext", mock.Anything, "Successfully retrieved certificate from secret manager", "certificateID", "test-cert-id").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "test-project-id/test-pool-name/test-ca-name",
		}

		cert, err := getCertificateFromSecretManager(context.Background(), authData, logger)

		assert.NoError(t, err, "Should not return error when GCP service succeeds")
		assert.NotNil(t, cert, "Certificate should not be nil")
		assert.Equal(t, "test-cert.example.com", cert.CommonName, "Common name should match")
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nMOCK_CERTIFICATE\n-----END CERTIFICATE-----", cert.SignedCertificate, "Signed certificate should match")
		assert.Equal(t, "-----BEGIN PRIVATE KEY-----\nMOCK_PRIVATE_KEY\n-----END PRIVATE KEY-----", cert.PrivateKey, "Private key should match")
		assert.Len(t, cert.InterMediateCertificates, 1, "Should have one intermediate certificate")
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nMOCK_CHAIN\n-----END CERTIFICATE-----", cert.InterMediateCertificates[0], "Intermediate certificate should match")

		logger.AssertExpectations(t)
	})

	t.Run("WhenGCPServiceFails_ShouldReturnError", func(t *testing.T) {
		hyperscaler.GetGCPService = func(ctx context.Context) (*hyperscalergoogle.GcpServices, error) {
			return nil, assert.AnError
		}
		defer restoreMocks()

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting certificate from secret manager", "certificateID", "test-cert-id").Return()
		logger.On("ErrorContext", mock.Anything, "Failed to get GCP service", "error", assert.AnError, "certificateID", "test-cert-id").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
		}

		cert, err := getCertificateFromSecretManager(context.Background(), authData, logger)

		assert.Error(t, err, "Should return error when GCP service fails")
		assert.Nil(t, cert, "Certificate should be nil")
		assert.Contains(t, err.Error(), "failed to get GCP service", "Error should contain expected message")

		logger.AssertExpectations(t)
	})

	t.Run("WhenGetCertificateAndPrivateKeyByIDFails_ShouldReturnError", func(t *testing.T) {
		hyperscaler.GetGCPService = func(ctx context.Context) (*hyperscalergoogle.GcpServices, error) {
			return &hyperscalergoogle.GcpServices{}, nil
		}
		vsa.GetCertificateAndPrivateKeyByID = func(gcpService hyperscaler.Services, caDeployedProjectID, secretManagerProjectID, region, caPoolName, certificateID string) (*hyperscalermodels.CustomCertificateResponse, error) {
			return nil, assert.AnError
		}
		defer restoreMocks()

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting certificate from secret manager", "certificateID", "test-cert-id").Return()
		logger.On("DebugContext", mock.Anything, "Using environment variables for CA config", "certificateID", "test-cert-id").Return()
		logger.On("ErrorContext", mock.Anything, "Failed to get certificate and private key", "error", assert.AnError, "certificateID", "test-cert-id").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
		}

		cert, err := getCertificateFromSecretManager(context.Background(), authData, logger)

		assert.Error(t, err, "Should return error when GetCertificateAndPrivateKeyByID fails")
		assert.Nil(t, cert, "Certificate should be nil")
		assert.Contains(t, err.Error(), "failed to get certificate and private key", "Error should contain expected message")

		logger.AssertExpectations(t)
	})
}

func TestGetPasswordFromSecretManager(t *testing.T) {
	t.Run("WhenSecretManagerSucceeds_ShouldReturnPassword", func(t *testing.T) {
		setupMocks()
		defer restoreMocks()

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting password from cache or secret manager", "secretID", "test-secret-id").Return()
		logger.On("InfoContext", mock.Anything, "Password fetched and cached", "secretID", "test-secret-id").Return()

		secretID := "test-secret-id"

		password, err := getPasswordFromSecretManager(context.Background(), secretID, logger)

		assert.NoError(t, err, "Should not return error when secret manager succeeds")
		assert.Equal(t, "mock-password", password, "Password should match expected value")

		logger.AssertExpectations(t)
	})

	t.Run("WhenSecretManagerFails_ShouldReturnError", func(t *testing.T) {
		vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
			return "", assert.AnError
		}
		defer restoreMocks()

		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting password from cache or secret manager", "secretID", "test-secret-id").Return()
		logger.On("ErrorContext", mock.Anything, "Failed to get password from secret manager", "error", assert.AnError, "secretID", "test-secret-id").Return()

		secretID := "test-secret-id"

		password, err := getPasswordFromSecretManager(context.Background(), secretID, logger)

		assert.Error(t, err, "Should return error when secret manager fails")
		assert.Empty(t, password, "Password should be empty")
		assert.Contains(t, err.Error(), "failed to get password from secret manager", "Error should contain expected message")

		logger.AssertExpectations(t)
	})
}

func TestCertificateMiddleware_ErrorCases(t *testing.T) {
	testCases := []struct {
		name          string
		authData      *models.AuthData
		setupMock     func()
		expectedError string
	}{
		{
			name: "USER_CERTIFICATE_FetchFails",
			authData: &models.AuthData{
				AuthType:      models.USER_CERTIFICATE,
				CertificateID: "test-cert-id",
				Certificate:   nil, // No certificate
				PoolID:        "test-pool",
			},
			setupMock: func() {
				hyperscaler.GetGCPService = func(ctx context.Context) (*hyperscalergoogle.GcpServices, error) {
					return nil, assert.AnError
				}
			},
			expectedError: "Failed to fetch certificate",
		},
		{
			name: "USERNAME_PWD_SEC_MGR_FetchFails",
			authData: &models.AuthData{
				AuthType: models.USERNAME_PWD_SEC_MGR,
				SecretID: "test-secret-id",
				Password: "", // No password
				PoolID:   "test-pool",
			},
			setupMock: func() {
				vsa.GetPasswordFromCacheOrSecretManager = func(ctx context.Context, secretID string) (string, error) {
					return "", assert.AnError
				}
			},
			expectedError: "Failed to fetch password",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupMock()
			defer restoreMocks()

			middleware := CertificateMiddleware()

			cacheKey := "test-key"
			cache.AddToAuthDataCache(cacheKey, tc.authData)

			req := httptest.NewRequest("GET", "/test", nil)
			ctx := context.WithValue(req.Context(), models.AuthDataKey, cacheKey)
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()

			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("Next handler should not be called")
			})

			middleware(nextHandler).ServeHTTP(w, req)

			assert.Equal(t, http.StatusInternalServerError, w.Code)
			assert.Contains(t, w.Body.String(), tc.expectedError)
		})
	}
}

func TestCertificateMiddleware_EdgeCases(t *testing.T) {
	t.Run("WhenAuthDataIsNil_ShouldReturn500", func(t *testing.T) {
		middleware := CertificateMiddleware()

		req := httptest.NewRequest("GET", "/test", nil)
		ctx := context.WithValue(req.Context(), models.AuthDataKey, "test-key")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()

		cache.AddToAuthDataCache("test-key", nil)

		nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("Next handler should not be called")
		})

		middleware(nextHandler).ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Authentication data not available in cache")
	})
}

func TestParseCaURIFromAuthData(t *testing.T) {
	// Save original env values
	originalCaName := env.CaName
	originalCaPoolName := env.CaPoolName
	originalCaPoolDeployedProjectID := env.CaPoolDeployedProjectID
	defer func() {
		env.CaName = originalCaName
		env.CaPoolName = originalCaPoolName
		env.CaPoolDeployedProjectID = originalCaPoolDeployedProjectID
	}()

	// Set test environment variables
	env.CaName = "env-ca-name"
	env.CaPoolName = "env-ca-pool-name"
	env.CaPoolDeployedProjectID = "env-project-id"

	t.Run("WhenAuthDataIsNil_ShouldUseEnvVars", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using environment variables for CA config", "certificateID", "").Return()

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(nil, logger)

		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIIsEmpty_ShouldUseEnvVars", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using environment variables for CA config", "certificateID", "test-cert-id").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIIsValid_ShouldParseCorrectly", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "test-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "test-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "test-project-id/test-pool-name/test-ca-name",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "test-ca-name", caName)
		assert.Equal(t, "test-pool-name", caPoolName)
		assert.Equal(t, "test-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasEmptyProjectID_ShouldFallbackToEnvVar", func(t *testing.T) {
		logger := &log.MockLogger{}
		// ParseCaURI already applies env var fallback, so parseCaURIFromAuthData receives non-empty value
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "env-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "test-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "/test-pool-name/test-ca-name",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "test-ca-name", caName)
		assert.Equal(t, "test-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasEmptyPoolName_ShouldFallbackToEnvVar", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "test-project-id").Return()
		// ParseCaURI already applies env var fallback, so parseCaURIFromAuthData receives non-empty value
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "env-ca-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "test-project-id//test-ca-name",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "test-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "test-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasEmptyCaName_ShouldFallbackToEnvVar", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "test-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "test-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "test-project-id/test-pool-name/",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		// Line 129: caName is empty, should fallback to env.CaName
		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "test-pool-name", caPoolName)
		assert.Equal(t, "test-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasAllNonEmptyParts_ShouldUseAllPoolCredentials", func(t *testing.T) {
		logger := &log.MockLogger{}
		// Test lines 115-116: else branch when caPoolDeployedProjectID is NOT empty
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "project-123").Return()
		// Test lines 122-123: else branch when caPoolName is NOT empty
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "pool-456").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "project-123/pool-456/ca-789",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		// All parts should be from CaURI, not env vars
		assert.Equal(t, "ca-789", caName)
		assert.Equal(t, "pool-456", caPoolName)
		assert.Equal(t, "project-123", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasInvalidFormat_ShouldFallbackToEnvVars", func(t *testing.T) {
		logger := &log.MockLogger{}
		// When ParseCaURI gets invalid format, it returns env vars, so parseCaURIFromAuthData receives non-empty values
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "env-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "env-ca-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "invalid-format", // Invalid format - not 3 parts
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		// ParseCaURI will return env vars for invalid format
		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenCaURIHasAllEmptyParts_ShouldUseEnvVars", func(t *testing.T) {
		logger := &log.MockLogger{}
		// ParseCaURI already applies env var fallback, so parseCaURIFromAuthData receives non-empty values
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolDeployedProjectID", "certificateID", "test-cert-id", "caPoolDeployedProjectID", "env-project-id").Return()
		logger.On("DebugContext", mock.Anything, "Using pool credential for caPoolName", "certificateID", "test-cert-id", "caPoolName", "env-ca-pool-name").Return()

		authData := &models.AuthData{
			CertificateID: "test-cert-id",
			CaURI:         "///", // All parts empty
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})

	t.Run("WhenAuthDataHasNoCertificateID_ShouldStillWork", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("DebugContext", mock.Anything, "Using environment variables for CA config", "certificateID", "").Return()

		authData := &models.AuthData{
			CaURI: "",
		}

		caName, caPoolName, caPoolDeployedProjectID := parseCaURIFromAuthData(authData, logger)

		assert.Equal(t, "env-ca-name", caName)
		assert.Equal(t, "env-ca-pool-name", caPoolName)
		assert.Equal(t, "env-project-id", caPoolDeployedProjectID)
		logger.AssertExpectations(t)
	})
}

func TestGetCertificateFromSecretManager_ErrorCases(t *testing.T) {
	t.Run("WhenAuthDataIsNil_ShouldReturnError", func(t *testing.T) {
		logger := &log.MockLogger{}

		cert, err := getCertificateFromSecretManager(context.Background(), nil, logger)

		assert.Error(t, err, "Should return error when authData is nil")
		assert.Nil(t, cert, "Certificate should be nil")
		assert.Contains(t, err.Error(), "authData is nil", "Error should contain expected message")
	})

	t.Run("WhenCertificateIDIsEmpty_ShouldReturnError", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("InfoContext", mock.Anything, "Getting certificate from secret manager", "certificateID", "").Return()

		authData := &models.AuthData{
			CertificateID: "", // Empty certificate ID
		}

		cert, err := getCertificateFromSecretManager(context.Background(), authData, logger)

		assert.Error(t, err, "Should return error when certificateID is empty")
		assert.Nil(t, cert, "Certificate should be nil")
		assert.Contains(t, err.Error(), "certificateID is empty in authData", "Error should contain expected message")
	})
}

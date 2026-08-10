package auth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-openapi/runtime/middleware"
	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	servergenModel "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type testProducer struct {
	writer io.Writer
	data   interface{}
	err    error
}

func (prod *testProducer) Produce(writer io.Writer, data interface{}) error {
	prod.writer = writer
	prod.data = data
	return prod.err
}

func TestAuthenticatedGCP(t *testing.T) {
	var jwtToken *jwt.Token
	var jwtErr error
	jwtParseWithClaimsMock := func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
		return jwtToken, jwtErr
	}
	jwtParseWithClaims = jwtParseWithClaimsMock
	defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

	req := &http.Request{}
	req.Header = make(http.Header)
	req.Header.Set("Authorization", "")
	mockLogger := log.NewLogger()
	req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

	t.Run("WhenParsingJWTWithClaimsReturnsError", func(tt *testing.T) {
		jwtErr = errors.New("something went wrong")
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
	t.Run("WhenParsingJWTWithClaimsReturnsValidationError", func(tt *testing.T) {
		jwtErr = &jwt.ValidationError{Errors: jwt.ValidationErrorMalformed, Inner: errors.New("malformed token")}
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
	t.Run("WhenParsingJWTWithClaimsReturnsNilToken", func(tt *testing.T) {
		jwtToken = nil
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
	t.Run("WhenParsingJWTWithClaimsReturnsInvalidToken", func(tt *testing.T) {
		jwtToken = &jwt.Token{}
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
	t.Run("WhenParsingJWTWithClaimsReturnsTokenWithMissingClaims", func(tt *testing.T) {
		jwtToken = &jwt.Token{Valid: true}
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
	t.Run("WhenProjectNumberInURLDoesn'tMatchToken", func(tt *testing.T) {
		req.URL = &url.URL{
			Host: "localhost:8000",
			Path: "/v1beta/projects/123/locations/:locationId/operations/:operationId",
		}
		jwtToken = &jwt.Token{Valid: true, Claims: &googleClaims{
			Google: &google{ConsumerProjectNumber: 123456},
		}}
		responder := AuthenticatedGCP(req, false, nil)
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
}

func Test_authenticationResponderGCP_WriteResponse(t *testing.T) {
	t.Run("WhenCallToProduceReturnsError", func(tt *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				tt.Log("Recovered from panic")
			}
		}()
		responder := &authenticationResponderGCP{message: "something went wrong"}
		rec := httptest.NewRecorder()
		prod := &testProducer{err: errors.New("failed to produce")}
		responder.WriteResponse(rec, prod)
		tt.Fail()
	})

	responder := &authenticationResponderGCP{code: 403, message: "something went wrong"}
	rec := httptest.NewRecorder()
	prod := &testProducer{}
	responder.WriteResponse(rec, prod)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Response code %v does not match expected one", rec.Code)
	}
	if prod.writer != rec {
		t.Error("Writer passed into producer does not match expected one")
	}
	if prod.data == nil {
		t.Error("No data passed into producer")
	} else {
		if modelsError, ok := prod.data.(servergenModel.Error); !ok {
			t.Error("Type of data passed into producer does not match expected one")
		} else {
			code := float64(http.StatusForbidden)
			message := "something went wrong"
			expectedModelsError := servergenModel.Error{Code: code, Message: message}
			if !reflect.DeepEqual(modelsError, expectedModelsError) {
				t.Error("Data passed into producer does not match expected one")
			}
		}
	}
}

func TestJWTKeyFunc(t *testing.T) {
	var certificateMap map[string]string
	var certificateErr error
	fetchGoogleCertificates = func(string, string) (map[string]string, error) {
		return certificateMap, certificateErr
	}
	t.Run("WhenTokenHeaderHasNoKid", func(tt *testing.T) {
		key, err := jwtKeyFunc(&jwt.Token{})
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenTokenHeaderHasInvalidKid", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": false},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenTokenSigningMethodIsWrong", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: jwt.SigningMethodHS256,
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenTokenClaimsAreMissing", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenTokenClaimsAreInvalid", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &jwt.RegisteredClaims{},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenTokenClaimsDoNotHaveClaimField", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenGoogleFieldDoesNotHavePermissions", func(tt *testing.T) {
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{Google: &google{}},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
	})
	t.Run("WhenFetchingGoogleCertificatesReturnsError", func(tt *testing.T) {
		certificateErr = errors.New("something went wrong")
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{Google: &google{
				ConsumerProjectNumber: 323,
			}},
		}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
		certificateErr = nil
	})
	t.Run("WhenGoogleCertificateIsNotFound", func(tt *testing.T) {
		certificateMap = map[string]string{"wild-bill": "certified cowboy"}
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{Google: &google{
				ConsumerProjectNumber: 323,
			}}}
		key, err := jwtKeyFunc(token)
		if err == nil {
			tt.Error("Error not returned")
		}
		if key != nil {
			tt.Error("Key unexpectedly returned")
		}
		certificateMap = nil
	})
	t.Run("WhenIssuerIsWrong", func(tt *testing.T) {
		gcpAcceptedServiceAccounts = "ttt,sss"
		defer func() {
			gcpAcceptedServiceAccounts = ""
		}()
		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		certificateMap = map[string]string{"billy": "THE kid"}
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Audience: []string{"gcpURL"},
					Issuer:   "gcpSA",
				},
				Google: &google{
					ConsumerProjectNumber: 323,
				}}}
		key, err := jwtKeyFunc(token)
		require.EqualError(tt, err, "invalid issuer or audience in JWT")
		require.Nil(tt, key)
		certificateMap = nil
		jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM
	})
	t.Run("WhenGoogleSuccessful", func(tt *testing.T) {
		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() {
			validateIssuerAndAudience = _validateIssuerAndAudience
		}()
		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		certificateMap = map[string]string{"billy": "THE kid"}
		token := &jwt.Token{
			Header: map[string]interface{}{"kid": "billy"},
			Method: &jwt.SigningMethodRSA{},
			Claims: &googleClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Audience: []string{"gcpURL"},
					Issuer:   "gcpSA",
				},
				Google: &google{
					ConsumerProjectNumber: 323,
				}}}
		key, err := jwtKeyFunc(token)
		if err != nil {
			tt.Error("Error unexpectedly returned - " + err.Error())
		}
		if key == nil {
			tt.Error("Key not returned")
		}
		certificateMap = nil
		jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM
	})
	fetchGoogleCertificates = _fetchGoogleCertificates
}

func TestFetchCerts(t *testing.T) {
	var jwk []byte
	var jwkErr error
	fetchJwk = func(jwkurl string) ([]byte, error) {
		return jwk, jwkErr
	}
	t.Run("WhenFetchingJWKReturnsError", func(tt *testing.T) {
		jwkErr = errors.New("something went wrong")
		jwkSleepRetryInterval = time.Duration(1) * time.Millisecond
		defer func() {
			jwkSleepRetryInterval = time.Duration(2) * time.Second
		}()
		certs, err := _fetchGoogleCertificates("test", "kid-rock")
		if err == nil {
			tt.Error("Error not returned")
		}
		if certs != nil {
			tt.Error("Certificates unexpectedly returned")
		}
		jwkErr = nil
	})
	t.Run("WhenUnmarshallingJWKReturnsError", func(tt *testing.T) {
		jwk = nil
		certs, err := _fetchGoogleCertificates("test", "kid-rock")
		if err == nil {
			tt.Error("Error not returned")
		}
		if certs != nil {
			tt.Error("Certificates unexpectedly returned")
		}
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		jwkString := `{"kid-rock":"bleh","billy-the-kid":"not-wild-bill"}`
		jwk = []byte(jwkString)
		certs, err := _fetchGoogleCertificates("test", "kid-rock")
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
		if certs == nil {
			tt.Error("Certificates not returned")
		} else {
			if len(certs) != 2 {
				tt.Errorf("Certificate count %v does not match expected one", len(certs))
			}
		}
		jwk = nil
	})
	t.Run("WhenSuccessfulUsingCache", func(tt *testing.T) {
		certificatesCache = map[string]*certCache{"kid-rock": {time.Now(), "bleh"}}
		certs, err := _fetchGoogleCertificates("test", "kid-rock")
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
		if certs == nil {
			tt.Error("Certificates not returned")
		} else {
			if len(certs) != 1 {
				tt.Errorf("Certificate count %v does not match expected one", len(certs))
			}
		}
	})
	t.Run("WhenSuccessfulAfterRetryFetchJwk", func(tt *testing.T) {
		jwkErr = errors.New("something went wrong")
		jwkString := `{"kid-rock":"bleh","billy-the-kid":"not-wild-bill"}`
		jwkSleepRetryInterval = time.Duration(10) * time.Millisecond
		defer func() {
			jwkSleepRetryInterval = time.Duration(2) * time.Second
		}()
		go func() {
			time.Sleep(20 * time.Millisecond)
			jwkErr = nil
			jwk = []byte(jwkString)
		}()
		certs, err := _fetchGoogleCertificates("test", "kid-rock")
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
		if certs == nil {
			tt.Error("Certificates not returned")
		}
		jwk = nil
	})
	fetchJwk = _fetchJwk
	certificatesCache = map[string]*certCache{}
}

func TestCleanpCertCache(t *testing.T) {
	t.Run("ClearCertCacheOnError", func(tt *testing.T) {
		certificatesCache = map[string]*certCache{"kid-rock": {time.Now(), "bleh"}}
		cleanupCertCache()
		assert.Equal(tt, 0, len(certificatesCache))
	})
}

func TestFetchJWK(t *testing.T) {
	var httpResp *http.Response
	var httpErr error
	jwkClientGet = func(url string) (*http.Response, error) {
		return httpResp, httpErr
	}
	t.Run("WhenHTTPGetReturnsError", func(tt *testing.T) {
		// Arrange
		httpErr = errors.New("something went wrong")

		// Act
		jwk, err := fetchJwk("")

		// Assert
		assert.Exactly(tt, httpErr, err)
		assert.Nil(tt, jwk)
		httpErr = nil
	})
	t.Run("WhenReadingResponseBodyReturnsError", func(tt *testing.T) {
		// Arrange
		httpResp = httptest.NewRecorder().Result()

		mockError := errors.New("mockError")
		mockBody := &bodyReadCloser{readError: mockError}
		httpResp.Body = mockBody

		// Act
		jwk, err := fetchJwk("")

		// Assert
		assert.Exactly(tt, mockError, err)
		assert.Empty(tt, jwk)
		assert.True(tt, mockBody.closed)
		n, _ := httpResp.Body.Read(make([]byte, 1))
		assert.Zero(tt, n, "Response Body should be read til the end, so the Transport can be reused by Go")
	})
	t.Run("WhenSuccessful", func(tt *testing.T) {
		// Arrange
		httpResp = httptest.NewRecorder().Result()

		mockJWK := []byte("JWK")
		mockBody := &bodyReadCloser{body: bytes.NewBuffer(mockJWK)}
		httpResp.Body = mockBody

		// Act
		jwk, err := fetchJwk("")

		// Assert
		assert.NoError(tt, err)
		assert.Equal(tt, mockJWK, jwk)
		assert.True(tt, mockBody.closed)
		n, _ := httpResp.Body.Read(make([]byte, 1))
		assert.Zero(tt, n, "Response Body should be read til the end, so the Transport can be reused by Go")
	})
	jwkClientGet = _jwkClientGet
}

func TestValidateProjectNumber(t *testing.T) {
	t.Run("WhenValidateProjectNumberFails", func(tt *testing.T) {
		req := &http.Request{}
		req.Header = make(http.Header)
		req.Header.Set("Authorization", "")
		req.URL = &url.URL{
			Host: "localhost:8000",
			Path: "/v1beta/projects/123/locations/:locationId/operations/:operationId",
		}
		err := validateProjectNumber(req, "1234")
		if err == nil {
			tt.Error("Error expected")
		}
	})
	t.Run("WhenValidateProjectNumberSucceeds", func(tt *testing.T) {
		req := &http.Request{}
		req.Header = make(http.Header)
		req.Header.Set("Authorization", "")
		req.URL = &url.URL{
			Host: "localhost:8000",
			Path: "/v1beta/projects/123/locations/:locationId/operations/:operationId",
		}
		err := validateProjectNumber(req, "123")
		if err != nil {
			tt.Error("Error unexpectedly returned")
		}
	})
	t.Run("WhenURLDoesNotContainProjects", func(tt *testing.T) {
		req := &http.Request{}
		req.Header = make(http.Header)
		req.Header.Set("Authorization", "")
		req.URL = &url.URL{
			Host: "localhost:8000",
			Path: "/v1beta/locations/:locationId/operations/:operationId",
		}
		err := validateProjectNumber(req, "123")
		if err == nil {
			tt.Error("Error expected when URL does not contain /projects")
		}
		if err != nil && !strings.Contains(err.Error(), "invalid URL, URL does not contain /projects") {
			tt.Errorf("Expected error about missing /projects, got: %v", err)
		}
	})
}

func TestAuthMiddleware_BypassForHealthAndMetrics(t *testing.T) {
	called := false
	var capturedHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		path string
	}{
		{path: "/health"},
		{path: "/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			called = false
			capturedHeaders = nil
			req := httptest.NewRequest("GET", tt.path, nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rr := httptest.NewRecorder()
			handler := AuthMiddleware(true)(next)
			handler.ServeHTTP(rr, req)
			assert.True(t, called, "Handler should be called for %s", tt.path)
			assert.Equal(t, http.StatusOK, rr.Code)
			assert.NotNil(t, capturedHeaders, "Headers should be injected into context for %s", tt.path)
			assert.Equal(t, "Bearer test-token", capturedHeaders.Get("Authorization"))
		})
	}
}

func TestAuthMiddleware_DoesNotBypassForOtherRoutes(t *testing.T) {
	paths := []string{"/api", "/v1/resource", "/random"}
	for _, path := range paths {
		t.Run(path, func(tt *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					tt.Errorf("Expected panic for %s but none occurred", path)
				}
			}()
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest("GET", path, nil)
			rr := httptest.NewRecorder()
			handler := AuthMiddleware(true)(next)
			handler.ServeHTTP(rr, req)
			tt.Errorf("Handler should not return normally for %s", path)
		})
	}
}

func TestAuthMiddleware_SuccessfulAuthentication(t *testing.T) {
	t.Run("WhenAuthenticationSucceeds", func(tt *testing.T) {
		// Mock the environment to be non-local so authentication is required
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		// Mock successful JWT parsing
		var jwtToken *jwt.Token
		var jwtErr error
		jwtParseWithClaimsMock := func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return jwtToken, jwtErr
		}
		jwtParseWithClaims = jwtParseWithClaimsMock
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		// Set up a valid token
		jwtToken = &jwt.Token{
			Valid: true,
			Claims: &googleClaims{
				Google: &google{ConsumerProjectNumber: 123},
			},
		}

		// Mock successful certificate fetching
		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		// Mock successful RSA key parsing
		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		// Mock successful issuer/audience validation
		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(true)(next) // Skip project number validation
		handler.ServeHTTP(rr, req)

		assert.True(tt, called, "Next handler should be called on successful authentication")
		assert.Equal(tt, http.StatusOK, rr.Code)
	})
}

func TestAuthenticatedGCP_SkipProjectNumberValidation(t *testing.T) {
	t.Run("WhenSkipProjectNumberValidationIsTrue", func(tt *testing.T) {
		// Mock the environment to be non-local so authentication is required
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		// Mock successful JWT parsing
		var jwtToken *jwt.Token
		var jwtErr error
		jwtParseWithClaimsMock := func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return jwtToken, jwtErr
		}
		jwtParseWithClaims = jwtParseWithClaimsMock
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		// Set up a valid token
		jwtToken = &jwt.Token{
			Valid: true,
			Claims: &googleClaims{
				Google: &google{ConsumerProjectNumber: 123},
			},
		}

		// Mock successful certificate fetching
		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		// Mock successful RSA key parsing
		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		// Mock successful issuer/audience validation
		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		req := &http.Request{}
		req.Header = make(http.Header)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		handlerCalled := false
		handler := func() middleware.Responder {
			handlerCalled = true
			return nil
		}

		responder := AuthenticatedGCP(req, true, handler) // Skip project number validation

		assert.True(tt, handlerCalled, "Handler should be called when skipProjectNumberValidation is true")
		assert.Nil(tt, responder, "Responder should be nil when handler returns nil")
	})

	t.Run("WhenSkipProjectNumberValidationIsFalseAndValidationFails", func(tt *testing.T) {
		// Mock the environment to be non-local so authentication is required
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		// Mock successful JWT parsing
		var jwtToken *jwt.Token
		var jwtErr error
		jwtParseWithClaimsMock := func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return jwtToken, jwtErr
		}
		jwtParseWithClaims = jwtParseWithClaimsMock
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		// Set up a valid token with different project number
		jwtToken = &jwt.Token{
			Valid: true,
			Claims: &googleClaims{
				Google: &google{ConsumerProjectNumber: 123456},
			},
		}

		// Mock successful certificate fetching
		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		// Mock successful RSA key parsing
		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		// Mock successful issuer/audience validation
		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		req := &http.Request{}
		req.Header = make(http.Header)
		req.Header.Set("Authorization", "Bearer valid-token")
		req.URL = &url.URL{
			Host: "localhost:8000",
			Path: "/v1beta/projects/123/locations/:locationId/operations/:operationId",
		}
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		handlerCalled := false
		handler := func() middleware.Responder {
			handlerCalled = true
			return nil
		}

		responder := AuthenticatedGCP(req, false, handler) // Don't skip project number validation

		assert.False(tt, handlerCalled, "Handler should not be called when project number validation fails")
		if responder == nil {
			tt.Error("Responder not returned")
		} else {
			if authResponder, ok := responder.(*authenticationResponderGCP); !ok {
				tt.Error("Responder type does not match expected one")
			} else {
				if authResponder.code != http.StatusUnauthorized {
					tt.Error("Wrong error code in responder")
				}
			}
		}
	})
}

type bodyReadCloser struct {
	body      io.Reader
	closed    bool
	readError error
}

func (body *bodyReadCloser) Read(p []byte) (n int, err error) {
	if body.readError != nil {
		return 0, body.readError
	}
	return body.body.Read(p)
}

func (body *bodyReadCloser) Close() error {
	body.closed = true
	return nil
}

func TestAuthMiddleware_BypassForCvnInfraEndpoints(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
	}{
		{name: "version", path: "/version"},
		{name: "iprange list", path: "/v1/iprange"},
		{name: "iprange by uuid", path: "/v1/iprange/abc-123"},
		{name: "addressRange list", path: "/v1/addressRange"},
		{name: "addressRange by uuid", path: "/v1/addressRange/abc-123"},
		{name: "host list", path: "/v1/host"},
		{name: "account list", path: "/v1/account"},
		{name: "account by uuid", path: "/v1/account/uuid-1"},
		{name: "region", path: "/v1/region"},
		{name: "zone", path: "/v1/zone"},
		{name: "interclusterLif", path: "/v1/interclusterLif"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest("GET", tt.path, nil)
			rr := httptest.NewRecorder()
			handler := AuthMiddleware(true)(next)
			handler.ServeHTTP(rr, req)
			assert.True(t, called, "Handler should be called for %s", tt.path)
			assert.Equal(t, http.StatusOK, rr.Code, "Status code should be OK for %s", tt.path)
		})
	}
}

func TestAuthMiddleware_BypassForExpertMode(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name string
		path string
	}{
		{
			name: "expert mode root path",
			path: "/v1/expertMode",
		},
		{
			name: "expert mode with trailing slash",
			path: "/v1/expertMode/",
		},
		{
			name: "expert mode with sub-path",
			path: "/v1/expertMode/test",
		},
		{
			name: "expert mode with nested path",
			path: "/v1/expertMode/projects/123/locations/us-east1/volumes",
		},
		{
			name: "expert mode with query parameters",
			path: "/v1/expertMode/test?param=value",
		},
		{
			name: "expert mode with multiple path segments",
			path: "/v1/expertMode/api/v1/resource/123/action",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest("GET", tt.path, nil)
			rr := httptest.NewRecorder()
			handler := AuthMiddleware(true)(next)
			handler.ServeHTTP(rr, req)
			assert.True(t, called, "Handler should be called for %s", tt.path)
			assert.Equal(t, http.StatusOK, rr.Code, "Status code should be OK for %s", tt.path)
		})
	}
}

func TestAuthMiddleware_BatchVolumePathPreservesHeaderContext(t *testing.T) {
	var (
		called        bool
		gotHeader     string
		headerPresent bool
	)

	originalEnv := runningEnv
	runningEnv = "local"
	defer func() { runningEnv = originalEnv }()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		headers, ok := r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
		headerPresent = ok
		if ok {
			gotHeader = headers.Get("authorization")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1beta/locations/us-east4/batch/volumes", nil)
	req.Header.Set("authorization", "Bearer batch-token")
	rr := httptest.NewRecorder()

	handler := AuthMiddleware(true)(next)
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, headerPresent)
	assert.Equal(t, "Bearer batch-token", gotHeader)
}

func TestAuthMiddleware_DoesNotBypassForNonExpertModePaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "v1 path without expertMode",
			path: "/v1/api",
		},
		{
			name: "v1beta path",
			path: "/v1beta/projects/123",
		},
		{
			name: "path containing expertMode but not as prefix",
			path: "/api/expertMode",
		},
		{
			name: "path with expertMode in middle",
			path: "/v1/api/expertMode/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Expected panic for %s but none occurred", tt.path)
				}
			}()

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest("GET", tt.path, nil)
			rr := httptest.NewRecorder()
			handler := AuthMiddleware(true)(next)
			handler.ServeHTTP(rr, req)
			t.Errorf("Handler should not return normally for %s", tt.path)
		})
	}
}

func TestAuthMiddleware_BypassPathsInjectHeaders(t *testing.T) {
	t.Run("shouldSkipAuthPath bypasses", func(t *testing.T) {
		tests := []struct {
			name string
			path string
		}{
			{
				name: "expert mode sub-path",
				path: "/v1/expertMode/projects/123/locations/us-east1/volumes",
			},
			{
				name: "health endpoint",
				path: "/health",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var capturedHeaders http.Header
				next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
					w.WriteHeader(http.StatusOK)
				})

				req := httptest.NewRequest("POST", tt.path, nil)
				req.Header.Set("Authorization", "Bearer my-jwt")
				req.Header.Set("X-Correlation-ID", "test-corr-id")
				rr := httptest.NewRecorder()
				handler := AuthMiddleware(false)(next)
				handler.ServeHTTP(rr, req)

				assert.Equal(t, http.StatusOK, rr.Code)
				assert.NotNil(t, capturedHeaders, "Headers should be injected into context for skipped path %s", tt.path)
				assert.Equal(t, "Bearer my-jwt", capturedHeaders.Get("Authorization"))
				assert.Equal(t, "test-corr-id", capturedHeaders.Get("X-Correlation-ID"))
			})
		}
	})

	t.Run("isBatchAuthPath bypasses with local env", func(t *testing.T) {
		origEnv := runningEnv
		runningEnv = "local"
		defer func() { runningEnv = origEnv }()

		tests := []struct {
			name string
			path string
		}{
			{
				name: "batch pool endpoint",
				path: "/v1beta/locations/us-central1-a/batch/pools",
			},
			{
				name: "batch active directory endpoint",
				path: "/v1beta/locations/us-east4/batch/activeDirectories",
			},
			{
				name: "batch host groups endpoint",
				path: "/v1beta/locations/us-east4/batch/hostGroups",
			},
			{
				name: "batch snapshots endpoint",
				path: "/v1beta/locations/us-central1-a/batch/snapshots",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var capturedHeaders http.Header
				next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
					w.WriteHeader(http.StatusOK)
				})

				req := httptest.NewRequest("POST", tt.path, nil)
				req.Header.Set("Authorization", "Bearer my-jwt")
				req.Header.Set("X-Correlation-ID", "test-corr-id")
				rr := httptest.NewRecorder()
				handler := AuthMiddleware(false)(next)
				handler.ServeHTTP(rr, req)

				assert.Equal(t, http.StatusOK, rr.Code)
				assert.NotNil(t, capturedHeaders, "Headers should be injected into context for batch path %s", tt.path)
				assert.Equal(t, "Bearer my-jwt", capturedHeaders.Get("Authorization"))
				assert.Equal(t, "test-corr-id", capturedHeaders.Get("X-Correlation-ID"))
			})
		}
	})
}

func TestShouldSkipAuthPath(t *testing.T) {
	t.Run("exact path match", func(tt *testing.T) {
		assert.True(tt, shouldSkipAuthPath("/health"))
		assert.True(tt, shouldSkipAuthPath("/metrics"))
		assert.True(tt, shouldSkipAuthPath("/v1/expertMode"))
		assert.True(tt, shouldSkipAuthPath("/version"))
		assert.True(tt, shouldSkipAuthPath("/v1/iprange"))
		assert.True(tt, shouldSkipAuthPath("/v1/addressRange"))
		assert.True(tt, shouldSkipAuthPath("/v1/host"))
		assert.True(tt, shouldSkipAuthPath("/v1/interclusterLif"))
		assert.False(tt, shouldSkipAuthPath("/api"))
	})

	t.Run("prefix path match", func(tt *testing.T) {
		assert.True(tt, shouldSkipAuthPath("/v1/expertMode/"))
		assert.True(tt, shouldSkipAuthPath("/v1/expertMode/test"))
		assert.True(tt, shouldSkipAuthPath("/v1/expertMode/projects/123"))
		assert.True(tt, shouldSkipAuthPath("/v1/iprange/uuid-1"))
		assert.True(tt, shouldSkipAuthPath("/v1/addressRange/uuid-1"))
		assert.False(tt, shouldSkipAuthPath("/v1beta/locations/us-east4/batch/pools"), "batch paths use isBatchAuthPath, not shouldSkipAuthPath")
		assert.False(tt, shouldSkipAuthPath("/v1beta/locations/us-east4/batch/activeDirectories"), "batch paths use isBatchAuthPath, not shouldSkipAuthPath")
		assert.False(tt, shouldSkipAuthPath("/v1beta/locations/us-east4/batch/volumes"))
		assert.False(tt, shouldSkipAuthPath("/v1beta/locations/us-east4/batch/replications"))
		assert.False(tt, shouldSkipAuthPath("/v1/api"))
		assert.False(tt, shouldSkipAuthPath("/api/expertMode"))
	})

	t.Run("case sensitive matching", func(tt *testing.T) {
		assert.False(tt, shouldSkipAuthPath("/Health"))
		assert.True(tt, shouldSkipAuthPath("/health"))
		assert.False(tt, shouldSkipAuthPath("/v1/ExpertMode/test"))
		assert.True(tt, shouldSkipAuthPath("/v1/expertMode/test"))
	})
}

func TestAuthMiddleware_BatchAuthPath(t *testing.T) {
	t.Run("WhenBatchHostGroupPathSucceeds", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		jwtParseWithClaims = func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return &jwt.Token{
				Valid: true,
				Claims: &googleClaims{
					Google: &google{ConsumerProjectNumber: 123},
				},
			}, nil
		}
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		called := false
		var capturedHeaders http.Header
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1/batch/hostGroups", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(false)(next)
		handler.ServeHTTP(rr, req)

		assert.True(tt, called, "Next handler should be called for batch auth path")
		assert.Equal(tt, http.StatusOK, rr.Code)
		assert.NotNil(tt, capturedHeaders)
		assert.Equal(tt, "Bearer valid-token", capturedHeaders.Get("Authorization"))
	})

	t.Run("WhenBatchSnapshotsPathSucceedsWithoutProjectInURL", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		jwtParseWithClaims = func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return &jwt.Token{
				Valid: true,
				Claims: &googleClaims{
					Google: &google{ConsumerProjectNumber: 123},
				},
			}, nil
		}
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		called := false
		var capturedHeaders http.Header
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1-a/batch/snapshots?fields=resourceId", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(false)(next)
		handler.ServeHTTP(rr, req)

		assert.True(tt, called, "Next handler should be called for batch snapshots path without /projects in URL")
		assert.Equal(tt, http.StatusOK, rr.Code)
		assert.NotNil(tt, capturedHeaders)
		assert.Equal(tt, "Bearer valid-token", capturedHeaders.Get("Authorization"))
	})

	t.Run("WhenBatchKmsConfigsPathSucceedsWithoutProjectInURL", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		jwtParseWithClaims = func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return &jwt.Token{
				Valid: true,
				Claims: &googleClaims{
					Google: &google{ConsumerProjectNumber: 123},
				},
			}, nil
		}
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		called := false
		var capturedHeaders http.Header
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1/batch/kmsConfigs?fields=resourceId", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(false)(next)
		handler.ServeHTTP(rr, req)

		assert.True(tt, called, "Next handler should be called for batch kmsConfigs path without /projects in URL")
		assert.Equal(tt, http.StatusOK, rr.Code)
		assert.NotNil(tt, capturedHeaders)
		assert.Equal(tt, "Bearer valid-token", capturedHeaders.Get("Authorization"))
	})

	t.Run("WhenBatchReplicationsPathSucceedsWithoutProjectInURL", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		jwtParseWithClaims = func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return &jwt.Token{
				Valid: true,
				Claims: &googleClaims{
					Google: &google{ConsumerProjectNumber: 123},
				},
			}, nil
		}
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		fetchGoogleCertificates = func(string, string) (map[string]string, error) {
			return map[string]string{"test-kid": "test-cert"}, nil
		}
		defer func() { fetchGoogleCertificates = _fetchGoogleCertificates }()

		jwtParseRSAPublicKeyFromPEM = func(key []byte) (*rsa.PublicKey, error) {
			return &rsa.PublicKey{}, nil
		}
		defer func() { jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM }()

		validateIssuerAndAudience = func(claims googleClaims) bool {
			return true
		}
		defer func() { validateIssuerAndAudience = _validateIssuerAndAudience }()

		called := false
		var capturedHeaders http.Header
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
			w.WriteHeader(http.StatusOK)
		})

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1-a/batch/replications?fields=state", nil)
		req.Header.Set("Authorization", "Bearer valid-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(false)(next)
		handler.ServeHTTP(rr, req)

		assert.True(tt, called, "Next handler should be called for batch replications path without /projects in URL")
		assert.Equal(tt, http.StatusOK, rr.Code)
		assert.NotNil(tt, capturedHeaders)
		assert.Equal(tt, "Bearer valid-token", capturedHeaders.Get("Authorization"))
	})

	t.Run("WhenBatchAuthFails", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "test"
		defer func() { runningEnv = originalEnv }()

		jwtParseWithClaims = func(tokenString string, claims jwt.Claims, keyFunc jwt.Keyfunc, options ...jwt.ParserOption) (*jwt.Token, error) {
			return nil, errors.New("invalid token")
		}
		defer func() { jwtParseWithClaims = jwt.ParseWithClaims }()

		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		})

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1/batch/hostGroups", nil)
		req.Header.Set("Authorization", "Bearer bad-token")
		mockLogger := log.NewLogger()
		req = req.WithContext(context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, mockLogger))

		rr := httptest.NewRecorder()
		handler := AuthMiddleware(false)(next)
		handler.ServeHTTP(rr, req)

		assert.False(tt, called, "Next handler should not be called when auth fails")
		assert.Equal(tt, http.StatusUnauthorized, rr.Code)
	})
}

func TestBatchAuthenticatedGCP(t *testing.T) {
	t.Run("WhenRunningLocally", func(tt *testing.T) {
		originalEnv := runningEnv
		runningEnv = "local"
		defer func() { runningEnv = originalEnv }()

		handlerCalled := false
		handler := func() middleware.Responder {
			handlerCalled = true
			return nil
		}

		req := httptest.NewRequest("POST", "/v1beta/locations/us-central1/batch/pools", nil)
		responder := BatchAuthenticatedGCP(req, handler)

		assert.True(tt, handlerCalled)
		assert.Nil(tt, responder)
	})
}

func TestShouldSkipVCMAuthPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "health", path: "/health", want: true},
		{name: "ready", path: "/ready", want: true},
		{name: "metrics", path: "/metrics", want: true},
		{name: "version", path: "/version", want: false},
		{name: "expert mode", path: "/v1/expertMode", want: false},
		{name: "expert mode child", path: "/v1/expertMode/resource", want: false},
		{name: "VCP host", path: "/v1/host", want: false},
		{name: "health child", path: "/health/details", want: false},
		{name: "ready child", path: "/ready/details", want: false},
		{name: "metrics child", path: "/metrics/details", want: false},
		{name: "case sensitive health", path: "/Health", want: false},
		{name: "case sensitive ready", path: "/Ready", want: false},
		{name: "case sensitive metrics", path: "/Metrics", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldSkipVCMAuthPath(tt.path))
		})
	}
}

func TestVCMAuthMiddleware_BypassForHealthReadyAndMetrics(t *testing.T) {
	called := false
	var capturedHeaders http.Header
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		capturedHeaders, _ = r.Context().Value(utilsmiddleware.HeaderContextKey).(http.Header)
		w.WriteHeader(http.StatusOK)
	})

	for _, path := range []string{"/health", "/ready", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			called = false
			capturedHeaders = nil
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rr := httptest.NewRecorder()
			VCMAuthMiddleware(true)(next).ServeHTTP(rr, req)

			assert.True(t, called)
			assert.Equal(t, http.StatusOK, rr.Code)
			assert.NotNil(t, capturedHeaders)
			assert.Equal(t, "Bearer test-token", capturedHeaders.Get("Authorization"))
		})
	}
}

func TestVCMAuthMiddleware_DoesNotBypassVCPPaths(t *testing.T) {
	originalEnv := runningEnv
	runningEnv = "test"
	t.Cleanup(func() { runningEnv = originalEnv })

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for _, path := range []string{"/version", "/v1/expertMode", "/v1/host"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			ctx := context.WithValue(req.Context(), utilsmiddleware.ContextSLoggerKey, log.NewLogger())
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()
			VCMAuthMiddleware(true)(next).ServeHTTP(rr, req)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})
	}
}

func TestIsBatchAuthPath(t *testing.T) {
	t.Run("batch hostgroup path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/hostGroups"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1/batch/hostGroups"))
	})

	t.Run("batch pools path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/pools"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1/batch/pools"))
	})

	t.Run("batch volumes path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/volumes"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1/batch/volumes"))
	})

	t.Run("batch snapshots path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1-a/batch/snapshots"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/snapshots"))
	})

	t.Run("batch replications path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1-a/batch/replications"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/replications"))
	})

	t.Run("batch kmsConfigs path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-central1/batch/kmsConfigs"))
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/kmsConfigs"))
	})

	t.Run("batch backupVaults path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/backupVaults"))
	})

	t.Run("batch backupPolicies path", func(tt *testing.T) {
		assert.True(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/backupPolicies"))
	})

	t.Run("other batch resources are not matched", func(tt *testing.T) {
		assert.False(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/unknownResource"))
	})

	t.Run("non-batch paths", func(tt *testing.T) {
		assert.False(tt, isBatchAuthPath("/v1beta/locations/us-east4/batch/unknownResource"))
		assert.False(tt, isBatchAuthPath("/v1beta/projects/123/locations/us-east4/storage/pools"))
		assert.False(tt, isBatchAuthPath("/health"))
		assert.False(tt, isBatchAuthPath("/v1beta/locations/us-east4/hostGroups"))
	})

	t.Run("paths without v1beta locations prefix", func(tt *testing.T) {
		assert.False(tt, isBatchAuthPath("/v1/locations/us-east4/batch/hostGroups"))
		assert.False(tt, isBatchAuthPath("/api/batch/hostGroups"))
	})
}

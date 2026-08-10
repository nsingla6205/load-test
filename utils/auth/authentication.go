package auth

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/go-openapi/runtime/middleware"
	"github.com/golang-jwt/jwt/v4"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	logger "golang.org/x/exp/slog"
)

var (
	jwkClient *http.Client
	mutex     sync.Mutex

	runningEnv                  = env.GetString("ENV", "")
	certificatesCache           = map[string]*certCache{}
	certCacheExpiration         = time.Duration(env.GetInt("CERT_CACHE_EXPIRATION_MINUTES", 120)) * time.Minute
	certCacheSleepRetryInterval = time.Duration(env.GetInt("CERT_CACHE_SLEEP_RETRY_INTERVAL_SECONDS", 5)) * time.Second
	jwkTimeout                  = time.Duration(env.GetInt("JWK_TIMEOUT_SECONDS", 10)) * time.Second
	gcpAcceptedServiceAccounts  = env.GetString("GCP_AUTH_ACCEPTED_SERVICE_ACCOUNTS", "")
	gcpServiceURL               = env.GetString("GCP_SERVICE_URL", "")
	jwkURL                      = env.GetString("JWK_KIDS_URL", "https://www.googleapis.com/service_accounts/v1/metadata/x509/")

	jwkRetryCount          = 5
	jwkSleepRetryInterval  = time.Duration(2) * time.Second
	certCacheMaxRetryCount = 3
	retryErrors            = []string{"crypto/rsa: verification error", "context deadline exceeded"}

	jwtParseWithClaims          = jwt.ParseWithClaims
	jwtParseRSAPublicKeyFromPEM = jwt.ParseRSAPublicKeyFromPEM
	fetchGoogleCertificates     = _fetchGoogleCertificates
	fetchJwk                    = _fetchJwk
	jwkClientGet                = _jwkClientGet
	validateIssuerAndAudience   = _validateIssuerAndAudience

	// authSkipExactPaths and authSkipPrefixPaths bypass JWT in AuthMiddleware for
	// internal service-to-service callers (Istio mTLS + AuthorizationPolicy), e.g.
	// ontap-proxy (/v1/expertMode) and CVN (/v1 infra parity endpoints including addressRange).
	authSkipExactPaths = []string{
		"/health",
		"/metrics",
		"/v1/expertMode",
		"/version",
		"/v1/iprange",
		"/v1/addressRange",
		"/v1/host",
		"/v1/account",
		"/v1/region",
		"/v1/zone",
		"/v1/svm",
		"/v1/interclusterLif",
	}
	authSkipPrefixPaths = []string{
		"/v1/expertMode/",
		"/v1/iprange/",
		"/v1/addressRange/",
		"/v1/host/",
		"/v1/account/",
	}
	vcmAuthSkipExactPaths = []string{
		"/health",
		"/ready",
		"/metrics",
	}
	batchAuthPrefix   = "/v1beta/locations/"
	batchAuthSuffixes = []string{"/batch/hostGroups", "/batch/pools", "/batch/activeDirectories", "/batch/volumes", "/batch/snapshots", "/batch/backupVaults", "/batch/backupPolicies", "/batch/backups", "/batch/replications", "/batch/kmsConfigs"}
)

type googleClaims struct {
	jwt.RegisteredClaims
	Google *google `json:"google,omitempty"`
}

type google struct {
	ConsumerProjectNumber int `json:"project_number"`
}

type certCache struct {
	time      time.Time
	publickey string
}
type authenticationResponderGCP struct {
	code    int
	message string
}

func (ar *authenticationResponderGCP) WriteResponse(rw http.ResponseWriter, producer runtime.Producer) {
	rw.WriteHeader(ar.code)
	if ar.message != "" {
		code := float64(ar.code)
		message := ar.message
		payload := gcpserver.Error{Code: code, Message: message}
		if err := producer.Produce(rw, payload); err != nil {
			panic(err) // let the recovery middleware deal with this
		}
	}
}

func shouldSkipAuthPath(path string) bool {
	return shouldSkipAuthPathFor(path, authSkipExactPaths, authSkipPrefixPaths)
}

func shouldSkipVCMAuthPath(path string) bool {
	return shouldSkipAuthPathFor(path, vcmAuthSkipExactPaths, nil)
}

func shouldSkipAuthPathFor(path string, exactPaths, prefixPaths []string) bool {
	for _, exactPath := range exactPaths {
		if path == exactPath {
			return true
		}
	}
	for _, prefixPath := range prefixPaths {
		if strings.HasPrefix(path, prefixPath) {
			return true
		}
	}
	return false
}

func isBatchAuthPath(path string) bool {
	if !strings.HasPrefix(path, batchAuthPrefix) {
		return false
	}
	for _, suffix := range batchAuthSuffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// AuthMiddleware returns a middleware handler that can be parameterized by skipProjectNumberValidation.
func AuthMiddleware(skipProjectNumberValidation bool) func(http.Handler) http.Handler {
	return authMiddleware(skipProjectNumberValidation, shouldSkipAuthPath)
}

// VCMAuthMiddleware returns authentication middleware with VCM-specific bypass paths.
func VCMAuthMiddleware(skipProjectNumberValidation bool) func(http.Handler) http.Handler {
	return authMiddleware(skipProjectNumberValidation, shouldSkipVCMAuthPath)
}

func authMiddleware(skipProjectNumberValidation bool, shouldSkipPath func(string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipPath(r.URL.Path) {
				ctx := context.WithValue(r.Context(), utilsmiddleware.HeaderContextKey, r.Header)
				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
				return
			}

			if isBatchAuthPath(r.URL.Path) {
				responder := AuthenticatedGCP(r, true, func() middleware.Responder {
					ctx := context.WithValue(r.Context(), utilsmiddleware.HeaderContextKey, r.Header)
					r = r.WithContext(ctx)
					next.ServeHTTP(w, r)
					return nil
				})
				if responder != nil {
					responder.WriteResponse(w, runtime.JSONProducer())
				}
				return
			}

			responder := AuthenticatedGCP(r, skipProjectNumberValidation, func() middleware.Responder {
				ctx := context.WithValue(r.Context(), utilsmiddleware.HeaderContextKey, r.Header)
				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
				return nil
			})
			if responder != nil {
				responder.WriteResponse(w, runtime.JSONProducer())
			}
		})
	}
}

// AuthenticatedGCP authenticates the specified request and, if authentication fails, responds with an authentication failure.
// If the authentication succeeds, invokes the specified handler function and responds with its results.
func AuthenticatedGCP(req *http.Request, skipProjectNumberValidation bool, handler func() middleware.Responder) middleware.Responder {
	if runningEnv == "local" {
		return handler()
	}
	slogger := req.Context().Value(utilsmiddleware.ContextSLoggerKey).(log.Logger)
	authorizationHeader := req.Header.Get("authorization")

	token, err := jwtParseWithClaims(authorizationHeader, &googleClaims{}, jwtKeyFunc)
	// clean the cache and force fetch the certs on errors
	for retryCount := 1; retryCount < certCacheMaxRetryCount && err != nil && shouldRetry(err, retryErrors); retryCount++ {
		time.Sleep(certCacheSleepRetryInterval)
		cleanupCertCache()
		token, err = jwtParseWithClaims(authorizationHeader, &googleClaims{}, jwtKeyFunc)
	}
	if err != nil {
		slogger.Error("Authentication failure", err)
		return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
	}
	if token == nil || !token.Valid {
		slogger.Error("Authentication failure", "err", "Received a nil token after parsing")
		return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
	}

	claims, _ := token.Claims.(*googleClaims)
	if claims == nil || claims.Google == nil {
		slogger.Error("Authentication failure", "err", "Claims missing from JWT")
		return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
	}

	if !skipProjectNumberValidation {
		tokenProjectNumber := strconv.Itoa(claims.Google.ConsumerProjectNumber)
		err1 := validateProjectNumber(req, tokenProjectNumber)
		if err1 != nil {
			slogger.Error("Authentication failure", err1)
			return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
		}
	}

	return handler()
}

func BatchAuthenticatedGCP(req *http.Request, handler func() middleware.Responder) middleware.Responder {
	if runningEnv == "local" {
		return handler()
	}
	authorizationHeader := req.Header.Get("authorization")
	slogger := req.Context().Value(utilsmiddleware.ContextSLoggerKey).(log.Logger)
	token, err := jwtParseWithClaims(authorizationHeader, &googleClaims{}, jwtKeyFunc)
	// clean the cache and force fetch the certs on errors
	for retryCount := 1; retryCount < certCacheMaxRetryCount && err != nil && shouldRetry(err, retryErrors); retryCount++ {
		time.Sleep(certCacheSleepRetryInterval)
		cleanupCertCache()
		token, err = jwtParseWithClaims(authorizationHeader, &googleClaims{}, jwtKeyFunc)
	}
	if err != nil {
		slogger.Error("Authentication failure", "error", err)
		return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
	}
	if token == nil || !token.Valid {
		slogger.Error("Authentication failure", "error", "Received a nil token after parsing")
		return &authenticationResponderGCP{code: http.StatusUnauthorized, message: "Authentication failure"}
	}

	return handler()
}

func jwtKeyFunc(token *jwt.Token) (interface{}, error) {
	var certificates map[string]string
	var err error

	kid := token.Header["kid"]
	kidString, ok := kid.(string)
	if !ok {
		return nil, errors.New("invalid kid field in JWT")
	}
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, errors.New("invalid signing method on token")
	}
	claims, ok := token.Claims.(*googleClaims)
	if !ok {
		return nil, errors.New("claims missing from JWT")
	}
	if !validateIssuerAndAudience(*claims) {
		return nil, errors.New("invalid issuer or audience in JWT")
	}
	certificates, err = fetchGoogleCertificates(claims.Issuer, kidString)
	if err != nil {
		return nil, err
	}
	certificate, ok := certificates[kidString]
	if !ok {
		return nil, errors.New("Certificate for kid " + kidString + " not found at issuer")
	}
	key, err := jwtParseRSAPublicKeyFromPEM([]byte(certificate))
	if key == nil {
		return nil, err // must explicitly return nil
	}

	return key, err
}

func _fetchGoogleCertificates(issuer string, keyString string) (certificates map[string]string, err error) {
	certificates, valid := getCertsFromCacheWithValidity()
	if valid && certificates != nil {
		if _, ok := certificates[keyString]; ok {
			return certificates, nil
		}
	}

	jwkKidURL := jwkURL + issuer
	jwk, err := fetchJwk(jwkKidURL)
	for retryCount := 1; retryCount <= jwkRetryCount && err != nil; retryCount++ {
		logger.Info("Failed to fetch Google JWK, retrying", err, "retryCount", retryCount)
		time.Sleep(jwkSleepRetryInterval)
		jwk, err = fetchJwk(jwkKidURL)
	}
	if err != nil {
		err = errors.New("unable to fetch Google JWK")
		return
	}
	err = json.Unmarshal(jwk, &certificates)
	if err != nil {
		err = errors.New("unable to parse Google JWK")
		return
	}
	updateCertCache(time.Now(), certificates)
	return
}

func _validateIssuerAndAudience(claims googleClaims) bool {
	lst := strings.Split(gcpAcceptedServiceAccounts, ",")
	if utils.ContainsString(lst, claims.Issuer) && claims.VerifyAudience(gcpServiceURL, true) {
		return true
	}
	return false
}

func _fetchJwk(jwkURL string) (jwk []byte, err error) {
	res, err := jwkClientGet(jwkURL)
	if err != nil {
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			logger.Error("Failed to close response body", err)
		}
	}(res.Body)

	if res.StatusCode != http.StatusOK {
		// defer io.Copy(ioUtil.Discard, res.Body)
		err = errors.New("failed to fetch remote JWK (status != 200)")
		return
	}

	jwk, err = io.ReadAll(res.Body)
	return
}

func _jwkClientGet(url string) (*http.Response, error) {
	if jwkClient == nil {
		// Starting with Go 1.6, the http package has transparent support for the
		// HTTP/2 protocol when using HTTPS. Programs that must disable HTTP/2
		// can do so by setting Transport.TLSNextProto to a non-nil, empty map.
		jwkClient = &http.Client{
			Transport: &http.Transport{
				TLSNextProto: map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
				TLSClientConfig: &tls.Config{
					Renegotiation: tls.RenegotiateOnceAsClient,
				},
			},
			Timeout: jwkTimeout,
		}
	}
	return jwkClient.Get(url)
}

func getCertsFromCacheWithValidity() (map[string]string, bool) {
	mutex.Lock()
	defer mutex.Unlock()

	certificate := make(map[string]string)
	for key, value := range certificatesCache {
		if time.Since(value.time) < certCacheExpiration {
			certificate[key] = value.publickey
			return certificate, true
		}
	}
	return nil, false
}

func cleanupCertCache() {
	updateCertCache(time.Time{}, nil)
}

func updateCertCache(time time.Time, certificates map[string]string) {
	mutex.Lock()
	defer mutex.Unlock()

	if certificates == nil {
		for key := range certificatesCache {
			delete(certificatesCache, key)
		}
	}
	for key, value := range certificates {
		certificatesCache[key] = &certCache{time: time, publickey: value}
	}
}

func shouldRetry(err error, errs []string) bool {
	for _, e := range errs {
		if strings.Contains(err.Error(), e) {
			return true
		}
	}
	return false
}

func validateProjectNumber(req *http.Request, tokenProjectNumber string) error {
	// Check if the URL contains /projects and if it does, then check if the project number matches the token project number
	if !strings.Contains(req.URL.String(), "/projects") {
		return errors.New("invalid URL, URL does not contain /projects")
	}

	splitURL := strings.Split(strings.SplitAfter(req.URL.String(), "/projects")[1], "/")
	urlProjectNumber := splitURL[1]
	if urlProjectNumber != tokenProjectNumber {
		return errors.New("invalid JWT, Project number doesn't match the request project number")
	}
	return nil
}

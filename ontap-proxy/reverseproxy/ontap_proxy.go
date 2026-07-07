package reverseproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	ontapProxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var (
	extractOntapPath              = utils.ExtractOntapPathRaw
	testOntapEndpointReachability = _testOntapEndpointReachability

	// Environment variable values
	ontapMaxIdleConns                 = env.GetInt("ONTAP_MAX_IDLE_CONNS", 200)
	ontapMaxIdleConnsPerHost          = env.GetInt("ONTAP_MAX_IDLE_CONNS_PER_HOST", 50)
	ontapIdleConnTimeoutSeconds       = env.GetInt("ONTAP_IDLE_CONN_TIMEOUT_SECONDS", 120)
	ontapTLSHandshakeTimeoutSeconds   = env.GetInt("ONTAP_TLS_HANDSHAKE_TIMEOUT_SECONDS", 15)
	ontapCleanupThresholdSeconds      = env.GetInt("ONTAP_CLEANUP_THRESHOLD_SECONDS", 300)
	ontapClientTimeoutSeconds         = env.GetInt("ONTAP_CLIENT_TIMEOUT_SECONDS", 60)
	ontapCleanupIntervalSeconds       = env.GetInt("ONTAP_CLEANUP_INTERVAL_SECONDS", 60)
	ontapMaxTotalConnections          = env.GetInt("ONTAP_MAX_TOTAL_CONNECTIONS", 1000)
	ontapMaxConnsPerHost              = env.GetInt("ONTAP_MAX_CONNS_PER_HOST", 100)
	ontapResponseHeaderTimeoutSeconds = env.GetInt("ONTAP_RESPONSE_HEADER_TIMEOUT_SECONDS", 30)
	ontapExpectContinueTimeoutSeconds = env.GetInt("ONTAP_EXPECT_CONTINUE_TIMEOUT_SECONDS", 1)
	runningEnv                        = env.GetString("ENV", "")

	// Retry tunables for the chained retry transport. Retries apply only to
	// idempotent requests on transient transport failures (HTTP status codes are not retried).
	ontapRetryMaxRetries    = env.GetInt("ONTAP_RETRY_MAX_RETRIES", 3)
	ontapRetryBaseBackoffMs = env.GetInt("ONTAP_RETRY_BASE_BACKOFF_MS", 100)
	ontapRetryMaxBackoffMs  = env.GetInt("ONTAP_RETRY_MAX_BACKOFF_MS", 2000)
)

const localEnv = "local"

// ConnectionPool manages HTTP clients for different ONTAP clusters and auth types
type ConnectionPool struct {
	clients          map[string]*http.Client
	clientEndpoints  map[string]string    // Maps pool key to selected endpoint
	clientTimestamps map[string]time.Time // Track last used time
	mutex            sync.RWMutex

	// Configuration
	maxIdleConns        int
	maxIdleConnsPerHost int
	idleConnTimeout     time.Duration
	tlsHandshakeTimeout time.Duration
	cleanupThreshold    time.Duration

	// Cleanup
	cleanupTicker *time.Ticker
	stopCleanup   chan bool
}

// Global connection pool instance
var globalConnectionPool *ConnectionPool

// Initialize the global connection pool
func init() {
	globalConnectionPool = NewConnectionPool()
}

// GetGlobalConnectionPool returns the global connection pool instance
// This allows other packages (like handlers) to reuse the connection pool
func GetGlobalConnectionPool() *ConnectionPool {
	return globalConnectionPool
}

// NewConnectionPool creates a new connection pool with optimized settings
func NewConnectionPool() *ConnectionPool {
	pool := &ConnectionPool{
		clients:          make(map[string]*http.Client),
		clientEndpoints:  make(map[string]string),
		clientTimestamps: make(map[string]time.Time),

		// Configuration from environment variables with defaults
		maxIdleConns:        ontapMaxIdleConns,
		maxIdleConnsPerHost: ontapMaxIdleConnsPerHost,
		idleConnTimeout:     time.Duration(ontapIdleConnTimeoutSeconds) * time.Second,
		tlsHandshakeTimeout: time.Duration(ontapTLSHandshakeTimeoutSeconds) * time.Second,
		cleanupThreshold:    time.Duration(ontapCleanupThresholdSeconds) * time.Second,

		stopCleanup: make(chan bool),
	}

	// Start cleanup routine
	pool.startCleanupRoutine()

	return pool
}

// GetClient returns a pooled HTTP client for the given auth data
func (p *ConnectionPool) GetClient(ctx context.Context, authData *models.AuthData) (*http.Client, string, error) {
	key := p.generatePoolKey(authData)

	// Try to get existing client (fast path with read lock)
	p.mutex.RLock()
	if client, exists := p.clients[key]; exists {
		p.mutex.RUnlock()
		// Update last used timestamp
		p.mutex.Lock()
		p.clientTimestamps[key] = time.Now()
		selectedEndpoint := p.clientEndpoints[key]
		p.mutex.Unlock()
		return client, selectedEndpoint, nil
	}
	p.mutex.RUnlock()

	// Create new client (slow path with write lock)
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Double-check after acquiring write lock (prevents race condition)
	if client, exists := p.clients[key]; exists {
		p.clientTimestamps[key] = time.Now()
		selectedEndpoint := p.clientEndpoints[key]
		return client, selectedEndpoint, nil
	}

	// Create new client
	client, selectedEndpoint, err := p.createClient(ctx, authData)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create client: %w", err)
	}

	// Store client and the endpoint it's using
	p.clients[key] = client
	p.clientEndpoints[key] = selectedEndpoint
	p.clientTimestamps[key] = time.Now()
	return client, selectedEndpoint, nil
}

// generatePoolKey creates a unique key for the connection pool.
// The key includes AccountName, PoolID, AuthType, and Username to ensure:
//  1. Different users get different clients (especially important for certificate auth where
//     the certificate is embedded in the TLS transport)
//  2. Different auth types don't share clients (prevents mixing certificate and basic auth transports)
//  3. For certificate auth, includes CertificateID to distinguish different certificates
func (p *ConnectionPool) generatePoolKey(authData *models.AuthData) string {
	baseKey := fmt.Sprintf("%s:%s:%d:%s", authData.AccountName, authData.PoolID, authData.AuthType, authData.Username)

	// For dedicated-hardware credentials, the same pool can front multiple
	// clusters. Include the cluster identity and management endpoint so distinct
	// clusters never share a pooled client/endpoint.
	if authData.ServiceLevel != "" && authData.ServiceLevel != models.ServiceLevelFLEX {
		endpoint := ""
		if len(authData.OntapEndpoints) > 0 {
			endpoint = authData.OntapEndpoints[0].IP
		}
		return fmt.Sprintf("hardware:%s:%s:%s", baseKey, authData.ClusterName, endpoint)
	}

	// For certificate auth, add certificate identifier for additional safety
	if authData.AuthType == models.USER_CERTIFICATE && authData.CertificateID != "" {
		return fmt.Sprintf("%s:%s", baseKey, authData.CertificateID)
	}

	return baseKey
}

// createClient creates a new HTTP client with optimized transport
// It iterates through all endpoints and tests each one until it finds a reachable endpoint
func (p *ConnectionPool) createClient(ctx context.Context, authData *models.AuthData) (*http.Client, string, error) {
	logger := util.GetLogger(ctx)

	if len(authData.OntapEndpoints) == 0 {
		return nil, "", fmt.Errorf("no ONTAP endpoints found in authData")
	}

	// Build transport once before the loop
	transport, err := p.buildOptimizedTransport(authData)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build transport: %w", err)
	}

	// Try each endpoint until we find a reachable one
	var lastErr error
	for _, endpoint := range authData.OntapEndpoints {
		testAddress := endpoint.DNS
		err = testOntapEndpointReachability(testAddress, authData, ctx, transport)

		if err != nil {
			logger.WarnContext(ctx, "ONTAP endpoint not reachable, trying next endpoint",
				"endpoint", testAddress,
				"poolID", authData.PoolID,
				"error", err)
			lastErr = err
			continue
		}

		// Endpoint is reachable, create client. The base *http.Transport is wrapped
		// with a chained retry transport so transient backend failures are retried
		// for idempotent requests (covers both the reverse-proxy and RestOntapClient paths).
		client := &http.Client{
			Transport: newRetryTransport(transport),
			Timeout:   time.Duration(ontapClientTimeoutSeconds) * time.Second,
		}

		return client, testAddress, nil
	}

	if lastErr != nil {
		return nil, "", fmt.Errorf("failed to create client for any endpoint: %w", lastErr)
	}

	return nil, "", fmt.Errorf("no valid endpoints found in authData")
}

// GetStats returns connection pool statistics
func (p *ConnectionPool) GetStats() map[string]interface{} {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return map[string]interface{}{
		"total_connections": len(p.clients),
		"max_idle_conns":    p.maxIdleConns,
		"max_idle_per_host": p.maxIdleConnsPerHost,
		"idle_timeout":      p.idleConnTimeout.String(),
	}
}

// ClientCacheEntryStatus contains non-sensitive cache entry metadata for connection pool
type ClientCacheEntryStatus struct {
	CacheKey  string    `json:"cache_key"`
	Endpoint  string    `json:"endpoint"`
	CachedAt  time.Time `json:"cached_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetClientCacheStatus returns cache status information for all pooled clients without sensitive data
func (p *ConnectionPool) GetClientCacheStatus() []ClientCacheEntryStatus {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	entries := make([]ClientCacheEntryStatus, 0, len(p.clients))
	for key := range p.clients {
		cachedAt := p.clientTimestamps[key]
		entries = append(entries, ClientCacheEntryStatus{
			CacheKey:  key,
			Endpoint:  p.clientEndpoints[key],
			CachedAt:  cachedAt,
			ExpiresAt: cachedAt.Add(p.cleanupThreshold),
		})
	}
	return entries
}

// GetGlobalClientCacheStatus returns cache status for the global connection pool
func GetGlobalClientCacheStatus() []ClientCacheEntryStatus {
	return globalConnectionPool.GetClientCacheStatus()
}

// Close closes the connection pool and cleans up resources
func (p *ConnectionPool) Close() {
	p.stopCleanup <- true

	p.mutex.Lock()
	defer p.mutex.Unlock()

	// Close all clients
	for _, client := range p.clients {
		closeIdleConnections(client)
	}

	p.clients = make(map[string]*http.Client)
	p.clientEndpoints = make(map[string]string)
	p.clientTimestamps = make(map[string]time.Time)
}

// startCleanupRoutine starts the background cleanup routine
func (p *ConnectionPool) startCleanupRoutine() {
	p.cleanupTicker = time.NewTicker(time.Duration(ontapCleanupIntervalSeconds) * time.Second)

	go func() {
		for {
			select {
			case <-p.cleanupTicker.C:
				p.cleanup()
			case <-p.stopCleanup:
				p.cleanupTicker.Stop()
				return
			}
		}
	}()
}

// closeIdleConnections closes idle connections for a pooled client's transport.
// The transport may be the base *http.Transport or a chained wrapper (e.g.
// retryTransport); both expose CloseIdleConnections.
func closeIdleConnections(client *http.Client) {
	if client == nil {
		return
	}
	if t, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
		t.CloseIdleConnections()
	}
}

// cleanup removes unused connections based on age and count
func (p *ConnectionPool) cleanup() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	maxConnections := ontapMaxTotalConnections
	now := time.Now()
	threshold := now.Add(-p.cleanupThreshold)

	// First, remove connections older than the threshold
	for key, lastUsed := range p.clientTimestamps {
		if lastUsed.Before(threshold) {
			if client, exists := p.clients[key]; exists {
				closeIdleConnections(client)
				delete(p.clients, key)
			}
			delete(p.clientEndpoints, key)
			delete(p.clientTimestamps, key)
		}
	}

	// If still over limit, remove oldest connections
	if len(p.clients) > maxConnections {
		oldestKey := ""
		oldestTime := now
		for key, lastUsed := range p.clientTimestamps {
			if lastUsed.Before(oldestTime) {
				oldestTime = lastUsed
				oldestKey = key
			}
		}

		if oldestKey != "" {
			if client, exists := p.clients[oldestKey]; exists {
				closeIdleConnections(client)
				delete(p.clients, oldestKey)
			}
			delete(p.clientEndpoints, oldestKey)
			delete(p.clientTimestamps, oldestKey)
		}
	}
}

// buildOptimizedTransport creates an optimized HTTP transport based on auth type
func (p *ConnectionPool) buildOptimizedTransport(authData *models.AuthData) (*http.Transport, error) {
	switch authData.AuthType {
	case models.USER_CERTIFICATE:
		return p.buildCertificateTransport(authData)
	case models.USERNAME_PWD, models.USERNAME_PWD_SEC_MGR:
		return p.buildBasicAuthTransport()
	default:
		return p.buildBasicAuthTransport()
	}
}

// createBaseTransport creates a transport with base settings and the provided TLS config
func (p *ConnectionPool) createBaseTransport(tlsConfig *tls.Config) *http.Transport {
	return &http.Transport{
		// Connection Pooling Settings
		MaxIdleConns:        p.maxIdleConns,
		MaxIdleConnsPerHost: p.maxIdleConnsPerHost,
		MaxConnsPerHost:     ontapMaxConnsPerHost,
		IdleConnTimeout:     p.idleConnTimeout,

		// Performance Settings
		DisableKeepAlives:  false, // Enable keep-alive
		DisableCompression: false, // Enable compression
		ForceAttemptHTTP2:  true,  // Enable HTTP/2

		// Timeout Settings
		TLSHandshakeTimeout:   p.tlsHandshakeTimeout,
		ResponseHeaderTimeout: time.Duration(ontapResponseHeaderTimeoutSeconds) * time.Second,
		ExpectContinueTimeout: time.Duration(ontapExpectContinueTimeoutSeconds) * time.Second,

		// Proxy and Environment
		Proxy: http.ProxyFromEnvironment,

		// TLS Configuration
		TLSClientConfig: tlsConfig,
	}
}

// buildCertificateTransport creates transport with certificate authentication
func (p *ConnectionPool) buildCertificateTransport(authData *models.AuthData) (*http.Transport, error) {
	if authData.Certificate == nil {
		return nil, fmt.Errorf("certificate not found for certificate authentication")
	}

	cert := &models.Certificate{
		SignedCertificate:        authData.Certificate.SignedCertificate,
		PrivateKey:               authData.Certificate.PrivateKey,
		InterMediateCertificates: authData.Certificate.InterMediateCertificates,
		CommonName:               authData.Certificate.CommonName,
		RootCaCertificate:        authData.Certificate.RootCaCertificate,
	}

	rootCA, clientCert, err := getAPICallCertificate(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare certificate: %w", err)
	}

	return p.createBaseTransport(&tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
		RootCAs:            rootCA,
		Certificates:       []tls.Certificate{clientCert},
	}), nil
}

// buildBasicAuthTransport creates transport with basic authentication
func (p *ConnectionPool) buildBasicAuthTransport() (*http.Transport, error) {
	return p.createBaseTransport(&tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
	}), nil
}

// _testOntapEndpointReachability tests if an ONTAP endpoint is reachable via HTTP
func _testOntapEndpointReachability(endpoint string, authData *models.AuthData, ctx context.Context, transport *http.Transport) error {
	testURL := fmt.Sprintf("https://%s/api/svm/svms?max_records=1", endpoint)

	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	testReq, err := http.NewRequestWithContext(testCtx, "GET", testURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create test request: %w", err)
	}

	// Configure authentication for test request
	if authData.AuthType != models.USER_CERTIFICATE {
		if authData.Username != "" && authData.Password != "" {
			testReq.SetBasicAuth(authData.Username, authData.Password)
		}
	}

	testClient := &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}

	resp, err := testClient.Do(testReq)
	if err != nil {
		return fmt.Errorf("endpoint not reachable: %w", err)
	}
	defer func() {
		if resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()

	return nil
}

// PooledAuthTransport implements http.RoundTripper with connection pooling
type PooledAuthTransport struct {
	pool *ConnectionPool
}

// NewPooledAuthTransport creates a new pooled auth transport
func NewPooledAuthTransport() *PooledAuthTransport {
	return &PooledAuthTransport{
		pool: globalConnectionPool,
	}
}

// RoundTrip executes the request using a pooled connection
func (pat *PooledAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	logger := util.GetLogger(req.Context())

	cacheKey := cache.GetAuthDataKeyFromContext(req.Context())
	if cacheKey == "" {
		return nil, fmt.Errorf("no cache key found in request context")
	}

	authData, exists := cache.GetFromAuthDataCache(cacheKey)
	if !exists || authData == nil {
		return nil, fmt.Errorf("no authentication data found in cache for key: %s", cacheKey)
	}

	client, selectedEndpoint, err := pat.pool.GetClient(req.Context(), authData)
	if err != nil {
		logger.ErrorContext(req.Context(), "Failed to get pool client", "error", err)
		return nil, fmt.Errorf("failed to get pool client: %w", err)
	}

	// Clone request to avoid RequestURI issues with httputil.ReverseProxy
	newReq := req.Clone(req.Context())
	newReq.RequestURI = ""

	// Set URL scheme and host for the selected endpoint
	if selectedEndpoint != "" {
		newReq.URL.Scheme = "https"
		newReq.URL.Host = selectedEndpoint
		newReq.Host = selectedEndpoint
	}

	// Configure authentication
	if err = configureRequestAuthentication(newReq, authData); err != nil {
		logger.ErrorContext(req.Context(), "Failed to configure authentication", "error", err)
		return nil, fmt.Errorf("failed to configure authentication: %w", err)
	}

	// Set common headers
	setCommonHeaders(newReq)

	logger.DebugContext(req.Context(), "Using pooled connection",
		"ontapAddress", selectedEndpoint,
		"authType", authData.AuthType,
		"poolID", authData.PoolID,
	)

	logCurlCommand(newReq, selectedEndpoint)

	// Run backend request; start time is stored on the outgoing request so ModifyResponse can record metrics after ProcessResponseModification.
	start := time.Now()
	newReq = newReq.WithContext(middleware.AddBackendRequestStartToContext(newReq.Context(), start))
	resp, err := client.Do(newReq)
	if err != nil {
		// No response — record metrics here (ModifyResponse is not called when transport fails).
		duration := time.Since(start).Seconds()
		projectID, poolID, path := middleware.GetBackendMetricsFromContext(req.Context())
		method := req.Method
		middleware.RecordBackendRequest(req.Context(), method, projectID, poolID, path, 0)
		middleware.RecordBackendDuration(req.Context(), duration, method, projectID, poolID, path, 0)
		return nil, err
	}
	return resp, nil
}

// configureRequestAuthentication configures authentication for the request
func configureRequestAuthentication(req *http.Request, authData *models.AuthData) error {
	if authData.AuthType == models.USER_CERTIFICATE {
		return nil
	}
	if authData.Username == "" || authData.Password == "" {
		return fmt.Errorf("missing username or password for basic authentication")
	}
	req.SetBasicAuth(authData.Username, authData.Password)
	return nil
}

// setCommonHeaders sets common headers for the request
func setCommonHeaders(req *http.Request) {
	req.Header.Set("X-Forwarded-For", req.RemoteAddr)
	req.Header.Set("X-Proxy-By", "ontap-proxy")
	req.Header.Set("Accept", "application/json")
	if req.Context().Value(models.RuleContextKey) != nil {
		req.Header.Set("Accept-Encoding", "")
	}
}

// BuildOntapRESTProxy creates the reverse proxy with connection pooling
func BuildOntapRESTProxy() *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			logger := util.GetLogger(req.Context())

			ontapPath := extractOntapPath(req.URL.Path)
			if ontapPath == "" {
				logger.ErrorContext(req.Context(), "Could not extract ONTAP path", "path", req.URL.Path)
				return
			}

			req.URL.Path = ontapPath
		},
		Transport:      NewPooledAuthTransport(),
		ModifyResponse: middleware.ProcessResponseAndRecordBackendMetrics,
		ErrorHandler:   handleProxyError,
	}

	return proxy
}

// handleProxyError handles errors from the reverse proxy
func handleProxyError(w http.ResponseWriter, r *http.Request, err error) {
	logger := util.GetLogger(r.Context())
	logger.ErrorContext(r.Context(), "Error handling request", "error", err, "path", r.URL.Path)

	err = middleware.TryReconcileHostLookupErrorIfApplicable(r.Context(), err)

	projectID, poolID, path := middleware.GetBackendMetricsFromContext(r.Context())
	if err != nil {
		if code := middleware.BackendErrorCodeForMetric(0, err); code != "" {
			middleware.RecordBackendError(r.Context(), r.Method, projectID, poolID, path, code)
		}
	}

	var proxyHTTP *middleware.ProxyHTTPError
	if errors.As(err, &proxyHTTP) {
		utils.WriteErrorResponse(w, proxyHTTP.Status, proxyHTTP.Message)
		return
	}

	if strings.Contains(err.Error(), "context canceled") {
		utils.WriteErrorResponse(w, http.StatusGatewayTimeout, "Request timeout - ONTAP cluster not responding")
	} else if strings.Contains(err.Error(), "connection refused") {
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Cannot connect to ONTAP cluster")
	} else if strings.Contains(strings.ToLower(err.Error()), "no such host") {
		utils.WriteErrorResponse(w, http.StatusBadGateway, "ONTAP cluster host not found")
	} else if strings.Contains(err.Error(), "Missing ONTAP credentials") {
		utils.WriteErrorResponse(w, http.StatusInternalServerError, "ONTAP credentials not configured")
	} else {
		utils.WriteErrorResponse(w, http.StatusBadGateway, "Proxy error: "+err.Error())
	}
}

// logCurlCommand logs the equivalent curl command for debugging
func logCurlCommand(req *http.Request, endpoint string) {
	logger := util.GetLogger(req.Context())

	curlCmd := fmt.Sprintf("curl -X %s", req.Method)

	for key, values := range req.Header {
		if key != "Authorization" {
			for _, value := range values {
				curlCmd += fmt.Sprintf(" -H \"%s: %s\"", key, value)
			}
		}
	}

	if req.Header.Get("Authorization") != "" {
		curlCmd += " -u \"username:password\""
	}

	url := fmt.Sprintf("https://%s%s", endpoint, req.URL.Path)
	if req.URL.RawQuery != "" {
		url += "?" + req.URL.RawQuery
	}
	curlCmd += fmt.Sprintf(" \"%s\"", url)

	if req.Body != nil && runningEnv == localEnv {
		body, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			if len(body) > 0 {
				curlCmd += fmt.Sprintf(" -d '%s'", string(body))
				logger.InfoContext(req.Context(), "Request body", "body", log.Sanitize(curlCmd))
			}
		}
	}

	logger.InfoContext(req.Context(), "Equivalent curl command", "command", log.Sanitize(curlCmd))
}

// getAPICallCertificate prepares certificates for API calls
func getAPICallCertificate(cert *models.Certificate) (*x509.CertPool, tls.Certificate, error) {
	if len(cert.InterMediateCertificates) > 0 && cert.SignedCertificate != "" && cert.PrivateKey != "" {
		rootCA, err := ontapProxyutils.ParsePEMCertificate(cert.InterMediateCertificates, "CERTIFICATE")
		if err != nil {
			return nil, tls.Certificate{}, fmt.Errorf("error parsing root CA certificate: %v", err)
		}

		signedCertPem := []byte(cert.SignedCertificate)
		privateKeyPem := []byte(cert.PrivateKey)

		clientCert, err := tls.X509KeyPair(signedCertPem, privateKeyPem)
		if err != nil {
			return nil, tls.Certificate{}, fmt.Errorf("error loading client certificate and key: %v", err)
		}

		return rootCA, clientCert, nil
	}
	return nil, tls.Certificate{}, fmt.Errorf("invalid certificate parameters: ensure SignedCertificate, PrivateKey, and InterMediateCertificates are set correctly")
}

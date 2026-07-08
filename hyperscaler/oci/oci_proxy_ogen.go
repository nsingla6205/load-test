package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oracle/oci-go-sdk/v65/certificatesmanagement"
	"github.com/oracle/oci-go-sdk/v65/dns"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"github.com/oracle/oci-go-sdk/v65/vault"
	ocicloudproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/oci-cloud-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// This file is the sole transport for the OCI cloud proxy: every Category-A
// operation is issued through the generated ogen client in
// clients/oci-cloud-proxy-client. The hand-written transport (oci_proxy_client.go)
// and per-op functions (oci_proxy_ops.go) have been removed.
//
// Because the proxy spec models bodies as opaque JSON, each adapter converts the
// OCI SDK request "details" to an opaque JSON body and decodes the opaque
// response back into the corresponding SDK type, so ociProxyWrapper and core/
// callers are unchanged.
//
// FSN-OGEN-MIGRATION — DUMMY / STUBBED CONTRACT.
// The OpenAPI spec these adapters target
// (clients/oci-cloud-proxy-client/oci-cloud-proxy-api.yaml) is a PLACEHOLDER:
// its paths, params, and opaque bodies are ASSUMED by VCP, not yet published by
// FSN. These adapters therefore run against stubbed contracts. When FSN ships the
// real spec, replace that YAML, re-run `go generate ./...`, and adjust the field
// mappings below if any request/response shapes differ — the wrapper and core/
// callers do not change.

// Ensure proxy errors satisfy the SDK service-error check used by callers.
var _ interface {
	GetHTTPStatusCode() int
	GetCode() string
	GetMessage() string
	GetOpcRequestID() string
} = (*ociProxyAPIError)(nil)

// ociProxyAPIError is the error shape surfaced for non-2xx proxy responses. It
// mirrors OCI service errors enough for ociProxyResourceNotFoundCheck and logging.
// code/message/opcReqID are decoded from the passthrough OCI error body, and
// proxyGenerated reflects the X-Proxy-Error header (true = the proxy produced the
// error itself rather than forwarding one from OCI).
type ociProxyAPIError struct {
	statusCode     int
	code           string
	message        string
	opcReqID       string
	proxyGenerated bool
}

func (e *ociProxyAPIError) Error() string {
	if e == nil {
		return ""
	}
	origin := "OCI"
	if e.proxyGenerated {
		origin = "proxy"
	}
	if e.code != "" {
		return fmt.Sprintf("%s error (%s, HTTP %d): %s", origin, e.code, e.statusCode, e.message)
	}
	if e.message != "" {
		return fmt.Sprintf("%s error (HTTP %d): %s", origin, e.statusCode, e.message)
	}
	return fmt.Sprintf("%s error (HTTP %d)", origin, e.statusCode)
}

func (e *ociProxyAPIError) GetHTTPStatusCode() int  { return e.statusCode }
func (e *ociProxyAPIError) GetCode() string         { return e.code }
func (e *ociProxyAPIError) GetMessage() string      { return e.message }
func (e *ociProxyAPIError) GetOpcRequestID() string { return e.opcReqID }

// isOCIProxyNotFound returns true when err represents an HTTP 404 from the proxy.
func isOCIProxyNotFound(err error) bool {
	var apiErr *ociProxyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.GetHTTPStatusCode() == http.StatusNotFound
	}
	return false
}

func requiredProxyPathID(fieldName string, id *string) (string, error) {
	v := strings.TrimSpace(derefString(id))
	if v == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	return v, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// ogen client + shared helpers
// ──────────────────────────────────────────────────────────────────────────────

// validateOCIProxyBaseURL ensures OCI_PROXY_BASE_URL is an absolute URL with a
// host and (by default) the https scheme. Plaintext http is rejected unless
// OCI_PROXY_ALLOW_HTTP is set. The opt-in is intended for local dev / a network-isolated in-cluster
// proxy where cleartext is an accepted, conscious tradeoff.
func validateOCIProxyBaseURL(base string) error {
	if base == "" {
		return fmt.Errorf("OCI cloud proxy is enabled but OCI_PROXY_BASE_URL is not set")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid OCI_PROXY_BASE_URL %q: must be an absolute URL with a host", base)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if env.OCIProxyAllowHTTP {
			return nil
		}
		return fmt.Errorf("OCI_PROXY_BASE_URL must use https (set OCI_PROXY_ALLOW_HTTP=true to allow http for local dev / in-cluster), got %q", base)
	default:
		return fmt.Errorf("OCI_PROXY_BASE_URL must use http or https, got scheme %q", u.Scheme)
	}
}

func newOgenProxyClient() (*ocicloudproxyclient.Client, error) {
	base := strings.TrimRight(strings.TrimSpace(env.OCIProxyBaseURL), "/")
	if err := validateOCIProxyBaseURL(base); err != nil {
		return nil, err
	}
	timeoutSec := env.OCIProxyHTTPTimeoutSec
	if timeoutSec < 1 {
		timeoutSec = env.OCIProxyHTTPTimeoutSecDefault
	}
	return ocicloudproxyclient.NewProxyClient(
		base,
		time.Duration(timeoutSec)*time.Second,
		ociProxyPathPrefixesFromEnv(),
	)
}

// ogenCommonHeaders returns the shared proxy header params: X-Proxy-Request-Id
// (from workflow/correlation context) and X-Proxy-Validate-Only (env).
//
// opc-request-id is intentionally not forwarded: the OCI cloud proxy generates a
// unique opc-request-id per request on its end, so VCP must not set it.
func ogenCommonHeaders(ctx context.Context) (ocicloudproxyclient.OptString, ocicloudproxyclient.OptBool) {
	// X-Proxy-Request-Id must always be present and unique per request: prefer the
	// per-request opc-work-request-id propagated from the OCI proxy frontend, and
	// fall back to a freshly generated UUID so the header is never empty or reused.
	id := ociProxyWorkflowRequestID(ctx)
	if id == "" {
		id = uuid.NewString()
	}
	reqID := ocicloudproxyclient.NewOptString(id)
	validateOnly := ocicloudproxyclient.NewOptBool(env.OCIProxyValidateOnly)
	return reqID, validateOnly
}

// ogenOpaqueBody converts an OCI SDK request-details struct into the opaque JSON
// body the proxy expects.
func ogenOpaqueBody(details any) (ocicloudproxyclient.OpaqueJson, error) {
	raw, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	var body ocicloudproxyclient.OpaqueJson
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return body, nil
}

// ogenDecode re-serializes the opaque JSON response and decodes it into the SDK
// type (which handles any polymorphic fields via its own UnmarshalJSON).
func ogenDecode(res *ocicloudproxyclient.OpaqueJson, dest any) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}

// ogenStatus maps a non-OK generated result to its HTTP status code. The error
// responses are shared types across all operations.
func ogenStatus(res any) int {
	switch res.(type) {
	case *ocicloudproxyclient.Error400Headers:
		return http.StatusBadRequest
	case *ocicloudproxyclient.Error401Headers:
		return http.StatusUnauthorized
	case *ocicloudproxyclient.Error403Headers:
		return http.StatusForbidden
	case *ocicloudproxyclient.Error404Headers:
		return http.StatusNotFound
	case *ocicloudproxyclient.Error409Headers:
		return http.StatusConflict
	case *ocicloudproxyclient.Error412Headers:
		return http.StatusPreconditionFailed
	case *ocicloudproxyclient.Error429Headers:
		return http.StatusTooManyRequests
	case *ocicloudproxyclient.Error500Headers:
		return http.StatusInternalServerError
	case *ocicloudproxyclient.Error502Headers:
		return http.StatusBadGateway
	case *ocicloudproxyclient.Error503Headers:
		return http.StatusServiceUnavailable
	case *ocicloudproxyclient.Error504Headers:
		return http.StatusGatewayTimeout
	}
	return 0
}

// ogenErrorResponse is implemented by every generated Error<code>Headers type.
// It exposes the X-Proxy-Error header (proxy-generated vs OCI-forwarded) and the
// passthrough OCI error body, so error diagnostics survive the opaque-JSON layer.
type ogenErrorResponse interface {
	GetXProxyError() ocicloudproxyclient.OptBool
	GetResponse() ocicloudproxyclient.OpaqueJson
}

// proxyErrorBody is the OCI (or proxy) error payload carried in a non-2xx
// Response. OCI service errors serialize as {"code": "...", "message": "..."}.
type proxyErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ogenResultError(op string, res any) error {
	apiErr := &ociProxyAPIError{statusCode: ogenStatus(res)}

	// Decode the OCI error body (code/message) and capture the X-Proxy-Error
	if er, ok := res.(ogenErrorResponse); ok {
		if xp := er.GetXProxyError(); xp.IsSet() {
			apiErr.proxyGenerated = xp.Value
		}
		body := er.GetResponse()
		var parsed proxyErrorBody
		if err := ogenDecode(&body, &parsed); err == nil {
			apiErr.code = strings.TrimSpace(parsed.Code)
			apiErr.message = strings.TrimSpace(parsed.Message)
		}
	}

	if apiErr.message == "" {
		apiErr.message = fmt.Sprintf("%s: OCI cloud proxy returned %T", op, res)
	}
	return apiErr
}

// ──────────────────────────────────────────────────────────────────────────────
// Secrets / Vault
// ──────────────────────────────────────────────────────────────────────────────

func ogenCreateSecret(ctx context.Context, client *ocicloudproxyclient.Client, req vault.CreateSecretRequest) (vault.CreateSecretResponse, error) {
	if client == nil {
		return vault.CreateSecretResponse{}, fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	body, err := ogenOpaqueBody(req.CreateSecretDetails)
	if err != nil {
		return vault.CreateSecretResponse{}, err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	params := ocicloudproxyclient.CreateSecretParams{
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	}
	if v := strings.TrimSpace(derefString(req.OpcRetryToken)); v != "" {
		params.OpcRetryToken = ocicloudproxyclient.NewOptString(v)
	}
	res, err := client.CreateSecret(ctx, body, params)
	if err != nil {
		return vault.CreateSecretResponse{}, err
	}
	ok, isOK := res.(*ocicloudproxyclient.OpaqueJson)
	if !isOK {
		return vault.CreateSecretResponse{}, ogenResultError("CreateSecret", res)
	}
	var secret vault.Secret
	if err := ogenDecode(ok, &secret); err != nil {
		return vault.CreateSecretResponse{}, err
	}
	return vault.CreateSecretResponse{Secret: secret}, nil
}

func ogenScheduleSecretDeletion(ctx context.Context, client *ocicloudproxyclient.Client, req vault.ScheduleSecretDeletionRequest) error {
	if client == nil {
		return fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	secretID, err := requiredProxyPathID("SecretId", req.SecretId)
	if err != nil {
		return err
	}
	body, err := ogenOpaqueBody(req.ScheduleSecretDeletionDetails)
	if err != nil {
		return err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	res, err := client.ScheduleSecretDeletion(ctx, body, ocicloudproxyclient.ScheduleSecretDeletionParams{
		SecretId:           secretID,
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	})
	if err != nil {
		return err
	}
	switch res.(type) {
	case *ocicloudproxyclient.OpaqueJson, *ocicloudproxyclient.ScheduleSecretDeletionNoContent:
		return nil
	}
	return ogenResultError("ScheduleSecretDeletion", res)
}

// ──────────────────────────────────────────────────────────────────────────────
// Certificates
// ──────────────────────────────────────────────────────────────────────────────

func ogenCreateCertificate(ctx context.Context, client *ocicloudproxyclient.Client, req certificatesmanagement.CreateCertificateRequest) (certificatesmanagement.CreateCertificateResponse, error) {
	if client == nil {
		return certificatesmanagement.CreateCertificateResponse{}, fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	body, err := ogenOpaqueBody(req.CreateCertificateDetails)
	if err != nil {
		return certificatesmanagement.CreateCertificateResponse{}, err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	params := ocicloudproxyclient.CreateCertificateParams{
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	}
	if v := strings.TrimSpace(derefString(req.OpcRetryToken)); v != "" {
		params.OpcRetryToken = ocicloudproxyclient.NewOptString(v)
	}
	res, err := client.CreateCertificate(ctx, body, params)
	if err != nil {
		return certificatesmanagement.CreateCertificateResponse{}, err
	}
	ok, isOK := res.(*ocicloudproxyclient.OpaqueJson)
	if !isOK {
		return certificatesmanagement.CreateCertificateResponse{}, ogenResultError("CreateCertificate", res)
	}
	var cert certificatesmanagement.Certificate
	if err := ogenDecode(ok, &cert); err != nil {
		return certificatesmanagement.CreateCertificateResponse{}, err
	}
	return certificatesmanagement.CreateCertificateResponse{Certificate: cert}, nil
}

func ogenScheduleCertificateDeletion(ctx context.Context, client *ocicloudproxyclient.Client, req certificatesmanagement.ScheduleCertificateDeletionRequest) error {
	if client == nil {
		return fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	certID, err := requiredProxyPathID("CertificateId", req.CertificateId)
	if err != nil {
		return err
	}
	body, err := ogenOpaqueBody(req.ScheduleCertificateDeletionDetails)
	if err != nil {
		return err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	res, err := client.ScheduleCertificateDeletion(ctx, body, ocicloudproxyclient.ScheduleCertificateDeletionParams{
		CertificateId:      certID,
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	})
	if err != nil {
		return err
	}
	switch res.(type) {
	case *ocicloudproxyclient.OpaqueJson, *ocicloudproxyclient.ScheduleCertificateDeletionNoContent:
		return nil
	}
	return ogenResultError("ScheduleCertificateDeletion", res)
}

// ──────────────────────────────────────────────────────────────────────────────
// DNS
// ──────────────────────────────────────────────────────────────────────────────

func ogenUpdateRRSet(ctx context.Context, client *ocicloudproxyclient.Client, req dns.UpdateRRSetRequest) (dns.UpdateRRSetResponse, error) {
	if client == nil {
		return dns.UpdateRRSetResponse{}, fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	zone, err := requiredProxyPathID("ZoneNameOrId", req.ZoneNameOrId)
	if err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	domain, err := requiredProxyPathID("Domain", req.Domain)
	if err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	rtype, err := requiredProxyPathID("Rtype", req.Rtype)
	if err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	body, err := ogenOpaqueBody(req.UpdateRrSetDetails)
	if err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	params := ocicloudproxyclient.UpdateRRSetParams{
		ZoneNameOrId:       zone,
		Domain:             domain,
		Rtype:              rtype,
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	}
	if s := string(req.Scope); s != "" {
		params.Scope = ocicloudproxyclient.NewOptString(s)
	}
	res, err := client.UpdateRRSet(ctx, body, params)
	if err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	ok, isOK := res.(*ocicloudproxyclient.OpaqueJson)
	if !isOK {
		return dns.UpdateRRSetResponse{}, ogenResultError("UpdateRRSet", res)
	}
	var collection dns.RecordCollection
	if err := ogenDecode(ok, &collection); err != nil {
		return dns.UpdateRRSetResponse{}, err
	}
	return dns.UpdateRRSetResponse{RecordCollection: collection}, nil
}

func ogenDeleteRRSet(ctx context.Context, client *ocicloudproxyclient.Client, req dns.DeleteRRSetRequest) error {
	if client == nil {
		return fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	zone, err := requiredProxyPathID("ZoneNameOrId", req.ZoneNameOrId)
	if err != nil {
		return err
	}
	domain, err := requiredProxyPathID("Domain", req.Domain)
	if err != nil {
		return err
	}
	rtype, err := requiredProxyPathID("Rtype", req.Rtype)
	if err != nil {
		return err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	params := ocicloudproxyclient.DeleteRRSetParams{
		ZoneNameOrId:       zone,
		Domain:             domain,
		Rtype:              rtype,
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	}
	if s := string(req.Scope); s != "" {
		params.Scope = ocicloudproxyclient.NewOptString(s)
	}
	res, err := client.DeleteRRSet(ctx, params)
	if err != nil {
		return err
	}
	switch res.(type) {
	case *ocicloudproxyclient.OpaqueJson, *ocicloudproxyclient.DeleteRRSetNoContent:
		return nil
	}
	return ogenResultError("DeleteRRSet", res)
}

// ──────────────────────────────────────────────────────────────────────────────
// Object Storage PAR
// ──────────────────────────────────────────────────────────────────────────────

func ogenCreatePreauthenticatedRequest(ctx context.Context, client *ocicloudproxyclient.Client, req objectstorage.CreatePreauthenticatedRequestRequest) (objectstorage.CreatePreauthenticatedRequestResponse, error) {
	if client == nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, fmt.Errorf("OCI cloud proxy client is not initialized")
	}
	namespace, err := requiredProxyPathID("NamespaceName", req.NamespaceName)
	if err != nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, err
	}
	bucket, err := requiredProxyPathID("BucketName", req.BucketName)
	if err != nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, err
	}
	body, err := ogenOpaqueBody(req.CreatePreauthenticatedRequestDetails)
	if err != nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, err
	}
	reqID, vOnly := ogenCommonHeaders(ctx)
	res, err := client.CreatePreauthenticatedRequest(ctx, body, ocicloudproxyclient.CreatePreauthenticatedRequestParams{
		NamespaceName:      namespace,
		BucketName:         bucket,
		XProxyRequestID:    reqID,
		XProxyValidateOnly: vOnly,
	})
	if err != nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, err
	}
	ok, isOK := res.(*ocicloudproxyclient.OpaqueJson)
	if !isOK {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, ogenResultError("CreatePreauthenticatedRequest", res)
	}
	var par objectstorage.PreauthenticatedRequest
	if err := ogenDecode(ok, &par); err != nil {
		return objectstorage.CreatePreauthenticatedRequestResponse{}, err
	}
	return objectstorage.CreatePreauthenticatedRequestResponse{PreauthenticatedRequest: par}, nil
}

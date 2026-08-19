package cvn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	goopenapiRuntime "github.com/go-openapi/runtime"
	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	cvnlog "github.com/greenqloud/cloud-volumes-network/app/utils/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
)

func TestNewFromContext(t *testing.T) {
	t.Cleanup(func() {
		cvnHost = ""
		aak = ""
		ask = ""
		cvnNew = sdk.New
	})

	t.Run("returns error when CVN_HOST is missing", func(t *testing.T) {
		cvnHost = ""
		aak = "key"
		ask = "secret"

		_, err := NewFromContext(context.Background(), "account")
		require.Error(t, err)

		var appErr *temporal.ApplicationError
		require.True(t, errors.As(err, &appErr))
		assert.True(t, appErr.NonRetryable())

		var trackingID int
		var detail string
		require.NoError(t, appErr.Details(&trackingID, &detail))
		assert.Equal(t, vsaerrors.ErrCVNHostNotFound, trackingID)
		assert.Equal(t, "CVN_HOST is not configured", detail)
	})

	t.Run("returns error when admin credentials are missing", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = ""
		ask = ""

		_, err := NewFromContext(context.Background(), "account")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ADMIN_API_KEY")
	})

	t.Run("builds client with vcp platform and correlation id", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = "key"
		ask = "secret"

		var captured *sdk.Config
		cvnNew = func(cfg *sdk.Config) sdk.CVN {
			captured = cfg
			return sdk.New(cfg)
		}

		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-123")
		client, err := NewFromContext(ctx, "account-456")
		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, captured)
		assert.Equal(t, "cvn.local", captured.Host)
		assert.Equal(t, "key", captured.AAK)
		assert.Equal(t, "secret", captured.ASK)
		assert.Equal(t, "corr-123", captured.CorrelationID)
		assert.Equal(t, "account-456", captured.AccountID)
		assert.Equal(t, platformVCP, captured.Platform)
	})

	t.Run("builds client with correlation id from temporal propagated context", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = "key"
		ask = "secret"

		var captured *sdk.Config
		cvnNew = func(cfg *sdk.Config) sdk.CVN {
			captured = cfg
			return sdk.New(cfg)
		}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{
			string(middleware.RequestCorrelationID): "corr-from-temporal",
		})
		client, err := NewFromContext(ctx, "account-456")
		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, captured)
		assert.Equal(t, "corr-from-temporal", captured.CorrelationID)
	})
}

// TestNewCVNAPIFromContext pins the raw-swagger-client construction path
// used when a caller needs to reach a generated endpoint directly (bypassing
// sdk.CVN's convenience wrappers). Every failure branch is exercised so
// misconfiguration surfaces as a typed error and the happy path returns a
// non-nil *cvnapi.CVN wired with the right host / transport.
func TestNewCVNAPIFromContext(t *testing.T) {
	t.Cleanup(func() {
		cvnHost = ""
		aak = ""
		ask = ""
	})

	t.Run("returns non-retryable ErrCVNHostNotFound when CVN_HOST is missing", func(t *testing.T) {
		cvnHost = ""
		aak = "key"
		ask = "secret"

		_, err := NewCVNAPIFromContext(context.Background(), "account")
		require.Error(t, err)

		var appErr *temporal.ApplicationError
		require.True(t, errors.As(err, &appErr))
		assert.True(t, appErr.NonRetryable(), "missing CVN_HOST is a config error, must be non-retryable")

		var trackingID int
		var detail string
		require.NoError(t, appErr.Details(&trackingID, &detail))
		assert.Equal(t, vsaerrors.ErrCVNHostNotFound, trackingID)
		assert.Equal(t, "CVN_HOST is not configured", detail)
	})

	t.Run("returns error when admin credentials are missing", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = ""
		ask = ""

		_, err := NewCVNAPIFromContext(context.Background(), "account")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ADMIN_API_KEY")
	})

	t.Run("returns error when only one of AAK/ASK is missing", func(t *testing.T) {
		// The guard is `aak == "" || ask == ""`. Confirm the OR arm on ASK
		// alone triggers the same error surface — otherwise a half-configured
		// deployment could slip through and fail at wire time instead of at
		// client construction.
		cvnHost = "cvn.local"
		aak = "key"
		ask = ""

		_, err := NewCVNAPIFromContext(context.Background(), "account")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ADMIN_API_KEY")
	})

	t.Run("returns non-nil raw cvnapi client on success", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = "key"
		ask = "secret"

		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-abc")
		client, err := NewCVNAPIFromContext(ctx, "account-xyz")
		require.NoError(t, err)
		require.NotNil(t, client, "raw *cvnapi.CVN must be returned when config is valid")
		// The generated struct groups tag-scoped clients as public fields.
		// Exercise a stable one so a future SDK bump that renames Networks
		// away surfaces here rather than at first call site.
		require.NotNil(t, client.Networks)
	})
}

// stubClientRequest is a minimal goopenapiRuntime.ClientRequest that records
// every SetHeaderParam call. Only SetHeaderParam is exercised by
// buildCVNAuthHeaders; every other method is a no-op / zero-value stub so
// the type still satisfies the interface. If SetHeaderParam is invoked with
// a header name equal to failOnHeader, it returns forcedErr — this simulates
// the (rare) case where the underlying transport rejects a header and lets
// the test cover the auth closure's five error-return paths.
type stubClientRequest struct {
	headers      http.Header
	failOnHeader string
	forcedErr    error
}

func newStubClientRequest() *stubClientRequest {
	return &stubClientRequest{headers: http.Header{}}
}

func (s *stubClientRequest) SetHeaderParam(name string, values ...string) error {
	if s.failOnHeader != "" && name == s.failOnHeader {
		return s.forcedErr
	}
	for _, v := range values {
		s.headers.Add(name, v)
	}
	return nil
}

func (s *stubClientRequest) GetHeaderParams() http.Header                    { return s.headers }
func (*stubClientRequest) SetQueryParam(string, ...string) error             { return nil }
func (*stubClientRequest) SetFormParam(string, ...string) error              { return nil }
func (*stubClientRequest) SetPathParam(string, string) error                 { return nil }
func (*stubClientRequest) GetQueryParams() url.Values                        { return url.Values{} }
func (*stubClientRequest) SetFileParam(string, ...goopenapiRuntime.NamedReadCloser) error {
	return nil
}
func (*stubClientRequest) SetBodyParam(interface{}) error                            { return nil }
func (*stubClientRequest) SetTimeout(time.Duration) error                            { return nil }
func (*stubClientRequest) GetMethod() string                                         { return "" }
func (*stubClientRequest) GetPath() string                                           { return "" }
func (*stubClientRequest) GetBody() []byte                                           { return nil }
func (*stubClientRequest) GetBodyParam() interface{}                                 { return nil }
func (*stubClientRequest) GetFileParam() map[string][]goopenapiRuntime.NamedReadCloser {
	return nil
}

// TestBuildCVNAuthHeaders_SetsAllRequiredHeaders proves the exact wire
// contract every CVN request must carry: the admin API + secret keys, the
// per-account routing identifier, the VCP platform tag, and the correlation
// id. If any of these five keys or values drift, cross-service tracing
// breaks and/or CVN routes to the wrong tenant — so the assertions are
// intentionally strict on both the header name and value.
func TestBuildCVNAuthHeaders_SetsAllRequiredHeaders(t *testing.T) {
	t.Cleanup(func() {
		aak = ""
		ask = ""
	})
	aak = "admin-api-key-x"
	ask = "admin-secret-key-y"

	auth := buildCVNAuthHeaders("account-42", "corr-99")

	req := newStubClientRequest()
	require.NoError(t, auth(req))

	assert.Equal(t, "admin-api-key-x", req.headers.Get("api-key"))
	assert.Equal(t, "admin-secret-key-y", req.headers.Get("secret-key"))
	assert.Equal(t, "account-42", req.headers.Get(cvnlog.AccountIdentifierKey))
	assert.Equal(t, platformVCP, req.headers.Get(cvnlog.PlatformKey))
	assert.Equal(t, "corr-99", req.headers.Get("x-correlation-id"))
}

// TestBuildCVNAuthHeaders_PropagatesEachHeaderError verifies that a
// SetHeaderParam failure at any of the five header positions short-circuits
// the auth closure with the transport's underlying error. Every branch is
// exercised so a future refactor that swallows one of them fails here.
func TestBuildCVNAuthHeaders_PropagatesEachHeaderError(t *testing.T) {
	t.Cleanup(func() {
		aak = ""
		ask = ""
	})
	aak = "k"
	ask = "s"

	forced := errors.New("transport rejected header")

	for _, hdr := range []string{"api-key", "secret-key", cvnlog.AccountIdentifierKey, cvnlog.PlatformKey, "x-correlation-id"} {
		hdr := hdr
		t.Run(hdr, func(t *testing.T) {
			auth := buildCVNAuthHeaders("account", "corr")
			req := newStubClientRequest()
			req.failOnHeader = hdr
			req.forcedErr = forced

			err := auth(req)
			require.Error(t, err)
			assert.Same(t, forced, err, "auth must return the transport's error unwrapped so callers see the wire-level cause")
		})
	}
}

// Sanity: compile-time proof stubClientRequest fully satisfies the swagger
// runtime interface. If a future runtime bump adds a method, this line breaks
// at compile time and forces the stub to keep up rather than silently
// falling out of coverage.
var _ goopenapiRuntime.ClientRequest = (*stubClientRequest)(nil)

// Guard against an unused-import lint if io ends up unused after refactors.
var _ = io.EOF

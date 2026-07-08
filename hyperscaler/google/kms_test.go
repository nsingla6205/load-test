package google

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// newTestKmsService spins up an httptest server that answers Get and :encrypt
// requests with the supplied status/body and returns a cloudkms.Service pointed at it.
// It also records whether a Get (non-encrypt) request was received and the encrypt path.
func newTestKmsService(t *testing.T, getStatus int, getBody string, encStatus int, encBody string, getCalled *bool, encryptPath *string) *cloudkms.Service {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, ":encrypt") {
			if encryptPath != nil {
				*encryptPath = r.URL.Path
			}
			w.WriteHeader(encStatus)
			_, _ = w.Write([]byte(encBody))
			return
		}
		if getCalled != nil {
			*getCalled = true
		}
		w.WriteHeader(getStatus)
		_, _ = w.Write([]byte(getBody))
	}))
	t.Cleanup(server.Close)

	svc, err := cloudkms.NewService(
		context.Background(),
		option.WithoutAuthentication(),
		option.WithHTTPClient(server.Client()),
		option.WithEndpoint(server.URL+"/"),
	)
	assert.NoError(t, err)
	return svc
}

// withBuildKmsService overrides the package-level buildKmsService for the duration of a test.
func withBuildKmsService(t *testing.T, svc *cloudkms.Service, err error) {
	t.Helper()
	orig := buildKmsService
	t.Cleanup(func() { buildKmsService = orig })
	buildKmsService = func(ctx context.Context, access *hyperscalermodels.KmsAccess) (*cloudkms.Service, error) {
		return svc, err
	}
}

const keyPath = "projects/p/locations/l/keyRings/r/cryptoKeys/k"

func TestGetKmsCryptoKey(t *testing.T) {
	gcp := &GcpServices{}
	ctx := context.Background()
	access := &hyperscalermodels.KmsAccess{IsVCPCreated: true}

	t.Run("MapsCryptoKeyOnSuccess", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusOK, `{"primary":{"algorithm":"GOOGLE_SYMMETRIC_ENCRYPTION","state":"ENABLED"}}`, http.StatusOK, "", nil, nil)
		withBuildKmsService(t, svc, nil)

		key, err := gcp.GetKmsCryptoKey(ctx, access, keyPath)
		assert.NoError(t, err)
		assert.NotNil(t, key)
		assert.True(t, key.HasPrimary)
		assert.Equal(t, "GOOGLE_SYMMETRIC_ENCRYPTION", key.PrimaryAlgorithm)
		assert.Equal(t, "ENABLED", key.PrimaryState)
	})

	t.Run("ReturnsTerminalPermissionDenied", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusForbidden, `{"error":{"code":403,"message":"Permission denied on key"}}`, http.StatusOK, "", nil, nil)
		withBuildKmsService(t, svc, nil)

		_, err := gcp.GetKmsCryptoKey(ctx, access, keyPath)
		assert.Error(t, err)
		var cerr *vsaerrors.CustomError
		assert.True(t, errors.As(err, &cerr))
		assert.Equal(t, vsaerrors.ErrKMSPermissionDenied, cerr.TrackingID)
	})

	t.Run("ReturnsTerminalKeyUnreachable", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusBadRequest, `{"error":{"code":400,"message":"key_unreachable: Cloud EKM unreachable"}}`, http.StatusOK, "", nil, nil)
		withBuildKmsService(t, svc, nil)

		_, err := gcp.GetKmsCryptoKey(ctx, access, keyPath)
		assert.Error(t, err)
		var cerr *vsaerrors.CustomError
		assert.True(t, errors.As(err, &cerr))
		assert.Equal(t, vsaerrors.ErrKMSKeyUnreachable, cerr.TrackingID)
	})

	t.Run("ReturnsRawErrorForTransientFailure", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusInternalServerError, `{"error":{"code":500,"message":"internal error"}}`, http.StatusOK, "", nil, nil)
		withBuildKmsService(t, svc, nil)

		_, err := gcp.GetKmsCryptoKey(ctx, access, keyPath)
		assert.Error(t, err)
		// Transient errors are returned unwrapped so the caller treats them as retriable.
		var cerr *vsaerrors.CustomError
		assert.False(t, errors.As(err, &cerr))
	})

	t.Run("PropagatesBuildServiceError", func(t *testing.T) {
		withBuildKmsService(t, nil, errors.New("build failure"))
		_, err := gcp.GetKmsCryptoKey(ctx, access, keyPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "build failure")
	})
}

func TestEncryptWithKmsKey(t *testing.T) {
	gcp := &GcpServices{}
	ctx := context.Background()
	access := &hyperscalermodels.KmsAccess{IsVCPCreated: true}

	t.Run("SucceedsOnOK", func(t *testing.T) {
		var encryptPath string
		svc := newTestKmsService(t, http.StatusOK, "", http.StatusOK, `{"name":"k","ciphertext":"Y2lwaGVy"}`, nil, &encryptPath)
		withBuildKmsService(t, svc, nil)

		err := gcp.EncryptWithKmsKey(ctx, access, keyPath, "test")
		assert.NoError(t, err)
		assert.Contains(t, encryptPath, ":encrypt")
	})

	t.Run("ReturnsTerminalPermissionDenied", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusOK, "", http.StatusForbidden, `{"error":{"code":403,"message":"caller lacks permission"}}`, nil, nil)
		withBuildKmsService(t, svc, nil)

		err := gcp.EncryptWithKmsKey(ctx, access, keyPath, "test")
		assert.Error(t, err)
		var cerr *vsaerrors.CustomError
		assert.True(t, errors.As(err, &cerr))
		assert.Equal(t, vsaerrors.ErrKMSPermissionDenied, cerr.TrackingID)
	})

	t.Run("ReturnsRawErrorForTransientFailure", func(t *testing.T) {
		svc := newTestKmsService(t, http.StatusOK, "", http.StatusInternalServerError, `{"error":{"code":500,"message":"internal error"}}`, nil, nil)
		withBuildKmsService(t, svc, nil)

		err := gcp.EncryptWithKmsKey(ctx, access, keyPath, "test")
		assert.Error(t, err)
		var cerr *vsaerrors.CustomError
		assert.False(t, errors.As(err, &cerr))
	})
}

func TestReturnEncryptRequest(t *testing.T) {
	req := returnEncryptRequest("test")
	assert.NotNil(t, req)
	// Plaintext is base64-encoded "test".
	assert.Equal(t, "dGVzdA==", req.Plaintext)
}

func TestClassifyKmsError(t *testing.T) {
	t.Run("UnreachableMapsToVCPError", func(t *testing.T) {
		err := classifyKmsError(errors.New("FAILED_PRECONDITION: KMS key in Cloud EKM is unreachable"))
		var cerr *vsaerrors.CustomError
		assert.True(t, errors.As(err, &cerr))
		assert.Equal(t, vsaerrors.ErrKMSKeyUnreachable, cerr.TrackingID)
	})
	t.Run("PermissionDeniedMapsToVCPError", func(t *testing.T) {
		err := classifyKmsError(&googleapi.Error{Code: 403, Message: "Permission denied"})
		var cerr *vsaerrors.CustomError
		assert.True(t, errors.As(err, &cerr))
		assert.Equal(t, vsaerrors.ErrKMSPermissionDenied, cerr.TrackingID)
	})
	t.Run("UnrelatedErrorReturnedUnchanged", func(t *testing.T) {
		orig := errors.New("some other failure")
		got := classifyKmsError(orig)
		assert.Equal(t, orig, got)
	})
}

func TestIsKmsKeyUnreachable(t *testing.T) {
	t.Run("MatchesGoogleApiBody", func(t *testing.T) {
		err := &googleapi.Error{Code: 412, Body: `{"error":{"message":"KEY_UNREACHABLE"}}`}
		msg, ok := isKmsKeyUnreachable(err)
		assert.True(t, ok)
		assert.NotEmpty(t, msg)
	})
	t.Run("MatchesPlainMessage", func(t *testing.T) {
		msg, ok := isKmsKeyUnreachable(errors.New("Cloud EKM is unreachable"))
		assert.True(t, ok)
		assert.Contains(t, msg, "Cloud EKM")
	})
	t.Run("ReturnsFalseForUnrelated", func(t *testing.T) {
		_, ok := isKmsKeyUnreachable(errors.New("some other failure"))
		assert.False(t, ok)
	})
}

func TestIsKmsPermissionDenied(t *testing.T) {
	t.Run("MatchesGoogleApi403", func(t *testing.T) {
		msg, ok := isKmsPermissionDenied(&googleapi.Error{Code: 403, Message: "Permission denied"})
		assert.True(t, ok)
		assert.Contains(t, msg, "Permission denied")
	})
	t.Run("MatchesPlainPermissionDenied", func(t *testing.T) {
		msg, ok := isKmsPermissionDenied(errors.New("PERMISSION_DENIED: nope"))
		assert.True(t, ok)
		assert.Contains(t, msg, "PERMISSION_DENIED")
	})
	t.Run("ReturnsFalseForNon403", func(t *testing.T) {
		_, ok := isKmsPermissionDenied(&googleapi.Error{Code: 404, Message: "not found"})
		assert.False(t, ok)
	})
	t.Run("ReturnsFalseForNil", func(t *testing.T) {
		_, ok := isKmsPermissionDenied(nil)
		assert.False(t, ok)
	})
}

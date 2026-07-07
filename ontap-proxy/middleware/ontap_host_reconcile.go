package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/cache"
	ontapproxyutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ProxyHTTPError is an alias for the shared HTTP-shaped error returned from transport
// and Core credential paths (errors.As).
type ProxyHTTPError = ontapproxyutils.HTTPError

func coreFetchErrorToProxyHTTP(err error) *ontapproxyutils.HTTPError {
	var fe *ontapproxyutils.HTTPError
	if errors.As(err, &fe) {
		switch fe.Status {
		case http.StatusInternalServerError:
			return &ontapproxyutils.HTTPError{Status: http.StatusBadGateway, Message: "Service unavailable"}
		case http.StatusServiceUnavailable:
			return &ontapproxyutils.HTTPError{Status: http.StatusServiceUnavailable, Message: fe.Message}
		default:
			return &ontapproxyutils.HTTPError{Status: fe.Status, Message: fe.Message}
		}
	}
	return &ontapproxyutils.HTTPError{Status: http.StatusBadGateway, Message: "ONTAP cluster host not found"}
}

// ReconcilePoolCredentialsAfterHostLookupFailure drops cached credentials, calls Core
// with the same pool identity, and repopulates the cache on success. It is used when
// the ONTAP hostname no longer resolves, which may indicate a deleted pool or stale cache.
func ReconcilePoolCredentialsAfterHostLookupFailure(ctx context.Context, cacheKey string) error {
	logger := util.GetLogger(ctx)
	poolDetails, err := poolDetailsFromCacheKey(cacheKey)
	if err != nil {
		logger.ErrorContext(ctx, "Invalid auth cache key for reconcile", "error", err, "cacheKey", cacheKey)
		return &ontapproxyutils.HTTPError{Status: http.StatusBadGateway, Message: "ONTAP cluster host not found"}
	}
	cache.RemoveFromAuthDataCache(cacheKey)
	credentials, err := fetchCredentialsFunc(ctx, poolDetails, "", logger)
	if err != nil {
		logger.ErrorContext(ctx, "Core reconcile after host lookup failure", "error", err, "poolID", poolDetails.PoolID)
		return coreFetchErrorToProxyHTTP(err)
	}
	authData := authDataFromFetchedCredentials(poolDetails, credentials)
	cache.AddToAuthDataCache(cacheKey, authData)
	return nil
}

func isHostLookupFailureError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "no such host") || strings.Contains(errMsg, "context deadline exceeded")
}

// TryReconcileHostLookupErrorIfApplicable calls Core to refresh credentials when the transport
// error looks like DNS/host lookup failure and the request has cache key + auth data. If Core
// returns a typed HTTP error (e.g. pool deleting), that error replaces err so the proxy can
// surface the right status. On successful reconcile, err is unchanged (this request still failed
// ONTAP; cache is updated for the next). Used by the reverse proxy ErrorHandler.
func TryReconcileHostLookupErrorIfApplicable(ctx context.Context, err error) error {
	if !isHostLookupFailureError(err) {
		return err
	}
	cacheKey := cache.GetAuthDataKeyFromContext(ctx)
	if cacheKey == "" {
		return err
	}
	recErr := ReconcilePoolCredentialsAfterHostLookupFailure(ctx, cacheKey)
	if recErr == nil {
		return err
	}
	var pe *ontapproxyutils.HTTPError
	if errors.As(recErr, &pe) {
		return recErr
	}
	return err
}

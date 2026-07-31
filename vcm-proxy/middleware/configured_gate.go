package middleware

import (
	"encoding/json"
	"net/http"
	"slices"

	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

const notConfiguredHTTPStatus = http.StatusServiceUnavailable

// ConfiguredChecker reports whether VCM has a reachable database configuration.
type ConfiguredChecker interface {
	Configured() bool
}

// AllowedRoute permits requests when VCM is not yet configured.
type AllowedRoute struct {
	Path    string
	Methods []string // empty means any HTTP method
}

// DefaultAllowlist routes that remain accessible before configuration completes.
var DefaultAllowlist = []AllowedRoute{
	{Path: "/health", Methods: []string{http.MethodGet}},
	{Path: "/ready", Methods: []string{http.MethodGet}},
	{Path: "/v1/config", Methods: []string{http.MethodPost}},
	{Path: "/metrics", Methods: []string{http.MethodGet}},
}

// ConfiguredGate blocks requests until the checker reports configured, except for allowlisted routes.
func ConfiguredGate(checker ConfiguredChecker, allowlist []AllowedRoute) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if checker == nil || checker.Configured() || isAllowlisted(r.URL.Path, r.Method, allowlist) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(notConfiguredHTTPStatus)
			_ = json.NewEncoder(w).Encode(&vcmserver.ErrorResponseV1{
				Code:    notConfiguredHTTPStatus,
				Message: "VCM is not configured",
			})
		})
	}
}

func isAllowlisted(path, method string, allowlist []AllowedRoute) bool {
	for _, route := range allowlist {
		if route.Path != path {
			continue
		}
		if len(route.Methods) == 0 || slices.Contains(route.Methods, method) {
			return true
		}
	}
	return false
}

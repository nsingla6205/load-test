package hyperscaler

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

var (
	cloudResolver       = NewCloudServiceResolver(env.GetHyperscaler())
	useProviderDI       = env.GetBool("USE_HYPERSCALER_PROVIDER_DI", false)
	ResolveCloudService = resolveCloudService
)

// SetCloudResolver binds the cloud resolver once at worker boot. The kind is decided
// from env.GetHyperscaler() at registration time, not per activity call.
func SetCloudResolver(r func(context.Context) (Services, error)) {
	cloudResolver = r
}

func resolveCloudService(ctx context.Context) (Services, error) {
	if !useProviderDI {
		return legacyResolveCloudService(ctx)
	}
	return cloudResolver(ctx)
}

func legacyResolveCloudService(ctx context.Context) (Services, error) {
	switch Hyperscaler(env.GetHyperscaler()) {
	case HyperscalerGCP:
		// Return a true nil interface on error rather than a *GcpServices
		// typed-nil wrapped in Services, so callers' `svc != nil` checks behave correctly.
		svc, err := GetGCPService(ctx)
		if err != nil {
			return nil, err
		}
		return svc, nil
	default:
		// OCI still uses GetOCIService directly.
		return UnsupportedServices{}, nil
	}
}

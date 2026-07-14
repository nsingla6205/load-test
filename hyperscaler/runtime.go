package hyperscaler

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

var (
	cloudResolver       = NewCloudServiceResolver(env.GetHyperscaler())
	ResolveCloudService = resolveCloudService
)

// SetCloudResolver binds the cloud resolver once at worker boot. The kind is decided
// from env.GetHyperscaler() at registration time, not per activity call.
func SetCloudResolver(r func(context.Context) (Services, error)) {
	cloudResolver = r
}

func resolveCloudService(ctx context.Context) (Services, error) {
	return cloudResolver(ctx)
}

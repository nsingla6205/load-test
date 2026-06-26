package hyperscaler

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
)

// Hyperscaler identifies the hyperscaler a worker is running as. It is resolved once at
// worker boot (from env.GetHyperscaler()) and passed to NewCloudServiceResolver.
type Hyperscaler string

const (
	HyperscalerGCP Hyperscaler = "gcp"
	HyperscalerOCI Hyperscaler = "oci"
)

// NewCloudServiceResolver returns a resolver bound to a single cloud, selected by
// the deployment kind at worker boot. The returned function builds a
// request-scoped Services per call (preserving per-request ctx/logger) but never
// re-decides the cloud.
func NewCloudServiceResolver(hyperscaler string) func(ctx context.Context) (Services, error) {
	switch Hyperscaler(hyperscaler) {
	case HyperscalerOCI:
		return func(ctx context.Context) (Services, error) {
			return UnsupportedServices{}, nil
		}
	case HyperscalerGCP:
		return func(ctx context.Context) (Services, error) {
			gcpSvc, err := GetGCPService(ctx)
			if err != nil {
				return nil, err
			}
			return gcpSvc, nil
		}
	default:
		return func(ctx context.Context) (Services, error) {
			return UnsupportedServices{}, nil
		}
	}
}

// Compile-time guarantee that GCP's concrete client satisfies the Services union.
var _ Services = (*google.GcpServices)(nil)

// Package certificate contains certificate-policy helpers shared across layers.
package certificate

import (
	"errors"
	"fmt"
	"time"
)

// IssuingCALifetimeFloor returns the greater of the configured CA lifetime floor
// and the requested leaf-certificate lifetime.
func IssuingCALifetimeFloor(configuredFloor, leafLifetime string) (time.Duration, error) {
	configured, err := time.ParseDuration(configuredFloor)
	if err != nil {
		return 0, fmt.Errorf("invalid MINIMUM_ISSUING_CA_REMAINING_LIFETIME %q: %w", configuredFloor, err)
	}
	leaf, err := time.ParseDuration(leafLifetime)
	if err != nil {
		return 0, fmt.Errorf("invalid CERTIFICATE_LIFETIME %q: %w", leafLifetime, err)
	}
	if configured <= 0 || leaf <= 0 {
		return 0, errors.New("issuing CA and certificate lifetimes must be positive")
	}
	if leaf > configured {
		return leaf, nil
	}
	return configured, nil
}

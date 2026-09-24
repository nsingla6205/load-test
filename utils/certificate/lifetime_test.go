package certificate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssuingCALifetimeFloor(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		leaf       string
		want       time.Duration
		wantError  string
	}{
		{name: "configured floor wins", configured: "48h", leaf: "24h", want: 48 * time.Hour},
		{name: "leaf lifetime wins", configured: "24h", leaf: "48h", want: 48 * time.Hour},
		{name: "equal boundary", configured: "24h", leaf: "24h", want: 24 * time.Hour},
		{name: "invalid configured floor", configured: "invalid", leaf: "24h", wantError: "MINIMUM_ISSUING_CA_REMAINING_LIFETIME"},
		{name: "invalid leaf lifetime", configured: "24h", leaf: "invalid", wantError: "CERTIFICATE_LIFETIME"},
		{name: "non-positive floor", configured: "0s", leaf: "24h", wantError: "must be positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := IssuingCALifetimeFloor(test.configured, test.leaf)
			if test.wantError != "" {
				require.ErrorContains(t, err, test.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

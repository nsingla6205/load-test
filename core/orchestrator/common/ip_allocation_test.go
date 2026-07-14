package common

import (
	"strconv"
	"testing"

	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateIPAddressesFromRange(t *testing.T) {
	const (
		startIP  = "10.0.0.10"
		endIP    = "10.0.0.20"
		gateway  = "10.0.0.1"
		netmask  = "255.255.255.0"
	)

	t.Run("allocatesSingleIP", func(t *testing.T) {
		ips, err := AllocateIPAddressesFromRange(startIP, endIP, gateway, netmask, 1, nil)
		require.NoError(t, err)
		require.Len(t, ips, 1)
		assert.Equal(t, "10.0.0.10", ips[0])
	})

	t.Run("skipsAllocatedIPs", func(t *testing.T) {
		allocated := map[string]bool{"10.0.0.10": true, "10.0.0.11": true}
		ips, err := AllocateIPAddressesFromRange(startIP, endIP, gateway, netmask, 2, allocated)
		require.NoError(t, err)
		require.Equal(t, []string{"10.0.0.12", "10.0.0.13"}, ips)
	})

	t.Run("returnsUnavailableWhenExhausted", func(t *testing.T) {
		allocated := map[string]bool{}
		for i := 10; i <= 20; i++ {
			allocated["10.0.0."+strconv.Itoa(i)] = true
		}
		_, err := AllocateIPAddressesFromRange(startIP, endIP, gateway, netmask, 1, allocated)
		require.Error(t, err)
		assert.True(t, customerrors.IsUnavailableErr(err))
		assert.Contains(t, err.Error(), ErrNoIPAddressesAvailable)
	})

	t.Run("rejectsInvalidCount", func(t *testing.T) {
		_, err := AllocateIPAddressesFromRange(startIP, endIP, gateway, netmask, 0, nil)
		require.Error(t, err)
	})
}

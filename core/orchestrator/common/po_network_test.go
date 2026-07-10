package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestPORequiredDataLifCount(t *testing.T) {
	tests := []struct {
		name      string
		nodeCount int
		want      int
	}{
		{name: "six nodes", nodeCount: 6, want: 6},
		{name: "zero nodes", nodeCount: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PORequiredDataLifCount(tt.nodeCount))
		})
	}
}

func TestPOHostAddresses(t *testing.T) {
	t.Run("selects distinct hosts skipping network, broadcast and gateway", func(t *testing.T) {
		addrs, netmask, err := POHostAddresses("10.1.1.0/24", "10.1.1.1", 4)
		require.NoError(t, err)
		assert.Equal(t, "255.255.255.0", netmask)
		assert.Equal(t, []string{"10.1.1.2", "10.1.1.3", "10.1.1.4", "10.1.1.5"}, addrs)
	})

	t.Run("errors when range too small", func(t *testing.T) {
		// /30 has 4 addresses: network, 2 hosts, broadcast. One host is the
		// gateway, leaving a single usable address.
		_, _, err := POHostAddresses("10.1.1.0/30", "10.1.1.1", 4)
		require.Error(t, err)
	})

	t.Run("rejects non-positive count", func(t *testing.T) {
		_, _, err := POHostAddresses("10.1.1.0/24", "10.1.1.1", 0)
		require.Error(t, err)
	})

	t.Run("rejects invalid CIDR", func(t *testing.T) {
		_, _, err := POHostAddresses("not-a-cidr", "10.1.1.1", 1)
		require.Error(t, err)
	})
}

func TestPONetmaskFromCIDR(t *testing.T) {
	mask, err := PONetmaskFromCIDR("10.1.1.0/26")
	require.NoError(t, err)
	assert.Equal(t, "255.255.255.192", mask)
}

func TestPOIPSpaceName(t *testing.T) {
	assert.Equal(t, "ipspace_acctuuid_100", POIPSpaceName("acct-uuid", "100"))
}

func TestPOBroadcastDomainName(t *testing.T) {
	assert.Equal(t, "bdomain_acctuuid_100", POBroadcastDomainName("acct-uuid", "100"))
}

func TestPODataLifName(t *testing.T) {
	assert.Equal(t, "svm_acct_a1b2c3d4", PODataLifName("svm_acct", "a1b2c3d4"))
}

func TestPOPoolSubnetMaskSize(t *testing.T) {
	assert.Equal(t, 27, POPoolSubnetMaskSize(27))
	assert.Equal(t, 28, POPoolSubnetMaskSize(28))
	assert.Equal(t, PODefaultSubnetMaskSize, POPoolSubnetMaskSize(24))
}

func TestPOCVNNetworkTier(t *testing.T) {
	bw, flex := POCVNNetworkTier(ServiceLevelExtreme)
	assert.Equal(t, "BPS_50G", bw)
	assert.True(t, flex)

	bw, flex = POCVNNetworkTier(ServiceLevelPremium)
	assert.Equal(t, "BPS_50G", bw)
	assert.False(t, flex)
}

func TestPOCIDRFromStartIPAndNetmask(t *testing.T) {
	cidr, err := POCIDRFromStartIPAndNetmask("10.1.1.4", "255.255.255.0")
	require.NoError(t, err)
	assert.Equal(t, "10.1.1.0/24", cidr)
}

func TestPODataPathFromIPRange(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ir := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "range-1"},
			StartIP:   "10.1.1.4",
			Netmask:   "255.255.255.0",
			Gateway:   "10.1.1.1",
		}
		cidr, gw, err := PODataPathFromIPRange(ir)
		require.NoError(t, err)
		assert.Equal(t, "10.1.1.0/24", cidr)
		assert.Equal(t, "10.1.1.1", gw)
	})

	t.Run("nil range", func(t *testing.T) {
		_, _, err := PODataPathFromIPRange(nil)
		require.Error(t, err)
	})

	t.Run("missing gateway", func(t *testing.T) {
		ir := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "range-1"},
			StartIP:   "10.1.1.4",
			Netmask:   "255.255.255.0",
		}
		_, _, err := PODataPathFromIPRange(ir)
		require.Error(t, err)
	})

	t.Run("invalid start IP", func(t *testing.T) {
		_, err := POCIDRFromStartIPAndNetmask("not-an-ip", "255.255.255.0")
		require.Error(t, err)
	})

	t.Run("invalid netmask", func(t *testing.T) {
		_, err := POCIDRFromStartIPAndNetmask("10.1.1.4", "not-a-mask")
		require.Error(t, err)
	})

	t.Run("invalid CIDR in data path", func(t *testing.T) {
		ir := &datamodel.IPRange{
			BaseModel: datamodel.BaseModel{UUID: "range-1"},
			StartIP:   "not-an-ip",
			Netmask:   "255.255.255.0",
			Gateway:   "10.1.1.1",
		}
		_, _, err := PODataPathFromIPRange(ir)
		require.Error(t, err)
	})
}

func TestPONetmaskFromCIDR_Errors(t *testing.T) {
	_, err := PONetmaskFromCIDR("not-a-cidr")
	require.Error(t, err)
}

func TestPOHostAddresses_NonIPv4(t *testing.T) {
	_, _, err := POHostAddresses("2001:db8::/32", "2001:db8::1", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not IPv4")
}

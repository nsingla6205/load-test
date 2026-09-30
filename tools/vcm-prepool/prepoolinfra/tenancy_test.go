package prepoolinfra

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
)

func Test_FindReusableDataSubnet_PrefixAndMask_SelectsExactTenant(t *testing.T) {
	subnets := []models.Subnet{
		{Name: "gcnv-unrelated", IpCidrRange: "10.0.0.0/28"},
		{Name: "vsa-1234-bbbb", IpCidrRange: "10.137.33.16/28"},
		{Name: "vsa-123-cccc", IpCidrRange: "10.137.33.32/28"},
		{Name: "vsa-123-dddd", IpCidrRange: "10.1.0.0/26"},
	}

	got := findReusableDataSubnet(&subnets, "vsa-123-", 28)
	require.NotNil(t, got)
	assert.Equal(t, "vsa-123-cccc", got.Name)

	gotLV := findReusableDataSubnet(&subnets, "vsa-123-", 26)
	require.NotNil(t, gotLV)
	assert.Equal(t, "vsa-123-dddd", gotLV.Name)

	assert.Nil(t, findReusableDataSubnet(&subnets, "vsa-123-", 24))
	assert.Nil(t, findReusableDataSubnet(&subnets, "vsa-999-", 28))
	assert.Nil(t, findReusableDataSubnet(nil, "vsa-123-", 28))
	assert.Nil(t, findReusableDataSubnet(&[]models.Subnet{}, "vsa-123-", 28))
}

func Test_CidrPrefixLength_ValidAndInvalid_ReturnsExpected(t *testing.T) {
	ones, ok := cidrPrefixLength("10.137.33.16/28")
	assert.True(t, ok)
	assert.Equal(t, 28, ones)

	_, ok = cidrPrefixLength("not-a-cidr")
	assert.False(t, ok)
}

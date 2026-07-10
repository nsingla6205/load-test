package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputePOQoS(t *testing.T) {
	t.Run("extreme tier at default multiplier", func(t *testing.T) {
		got, err := ComputePOQoS(ServiceLevelExtreme, uint64(BytesPerTiB))
		require.NoError(t, err)
		assert.Equal(t, POExtremeQoSPolicyName, got.PolicyName)
		assert.Equal(t, int64(defaultPOExtremeMaxMibpsPerTiB), got.MaxThroughput)
	})

	t.Run("premium tier at default multiplier", func(t *testing.T) {
		got, err := ComputePOQoS(ServiceLevelPremium, uint64(BytesPerTiB))
		require.NoError(t, err)
		assert.Equal(t, POPremiumQoSPolicyName, got.PolicyName)
		assert.Equal(t, int64(defaultPOPremiumMaxMibpsPerTiB), got.MaxThroughput)
	})

	t.Run("scales throughput with pool size", func(t *testing.T) {
		got, err := ComputePOQoS(ServiceLevelExtreme, uint64(BytesPerTiB)*4)
		require.NoError(t, err)
		assert.Equal(t, int64(defaultPOExtremeMaxMibpsPerTiB)*4, got.MaxThroughput)
	})

	t.Run("rejects unsupported service level", func(t *testing.T) {
		_, err := ComputePOQoS("STANDARD", uint64(BytesPerTiB))
		require.Error(t, err)
	})
}

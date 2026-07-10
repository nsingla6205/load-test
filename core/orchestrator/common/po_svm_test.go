package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizePOAccountUUID(t *testing.T) {
	assert.Equal(t, "acctuuid1234", NormalizePOAccountUUID("acct-uuid-1234"))
}

func TestGetSVMPrefix(t *testing.T) {
	assert.Equal(t, "svm_acctuuid1234_", GetSVMPrefix("acct-uuid-1234"))
}

func TestDeterministicPOSVMPostfix(t *testing.T) {
	poolUUID := "b08241a4-7e11-450c-a86a-5e452b9f1e80"
	postfix := DeterministicPOSVMPostfix(poolUUID)
	assert.Len(t, postfix, POSVMPostfixLen)
	assert.Equal(t, postfix, DeterministicPOSVMPostfix(poolUUID))
	assert.NotEqual(t, postfix, DeterministicPOSVMPostfix("other-pool-uuid"))
}

func TestGenerateSVMPostfix(t *testing.T) {
	t.Run("positive length", func(t *testing.T) {
		postfix, err := GenerateSVMPostfix(8)
		require.NoError(t, err)
		assert.Len(t, postfix, 8)
	})

	t.Run("non-positive length", func(t *testing.T) {
		_, err := GenerateSVMPostfix(0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "postfix length must be positive")
	})
}

func TestGeneratePODataLifPostfixes(t *testing.T) {
	t.Run("returns unique postfixes", func(t *testing.T) {
		postfixes, err := GeneratePODataLifPostfixes(4)
		require.NoError(t, err)
		require.Len(t, postfixes, 4)
		seen := make(map[string]bool, len(postfixes))
		for _, p := range postfixes {
			assert.Len(t, p, POSVMPostfixLen)
			assert.False(t, seen[p])
			seen[p] = true
		}
	})

	t.Run("non-positive count", func(t *testing.T) {
		_, err := GeneratePODataLifPostfixes(0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lif postfix count must be positive")
	})
}

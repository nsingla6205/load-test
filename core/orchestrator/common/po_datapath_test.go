package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDataPathParams_Validate(t *testing.T) {
	valid := DataPathParams{
		AccountUUID:     "acct",
		RemoteAccountID: "projects/p/global/networks/net",
		MicrozoneUUID:   "host",
		SubnetMaskSize:  MinSubnetMaskSize,
		VlanBandwidth:   VlanBandwidth50G,
	}

	t.Run("valid params", func(t *testing.T) {
		require.NoError(t, valid.Validate())
	})

	t.Run("empty account uuid", func(t *testing.T) {
		p := valid
		p.AccountUUID = ""
		require.Error(t, p.Validate())
	})

	t.Run("empty remote account id", func(t *testing.T) {
		p := valid
		p.RemoteAccountID = "  "
		require.Error(t, p.Validate())
	})

	t.Run("invalid subnet mask size", func(t *testing.T) {
		p := valid
		p.SubnetMaskSize = 24
		err := p.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "subnet mask size")
	})

	t.Run("empty microzone uuid", func(t *testing.T) {
		p := valid
		p.MicrozoneUUID = "  "
		require.Error(t, p.Validate())
	})
}

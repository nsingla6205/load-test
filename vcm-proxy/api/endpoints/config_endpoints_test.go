package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

func TestSetConfigV1_ReturnsNilError(t *testing.T) {
	handler := Handler{
		ConfigStore: vcmconfig.NewStore(log.NewLogger(), 0),
	}

	err := handler.SetConfigV1(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{
			Project:  "p",
			Instance: "i",
			Database: "d",
		},
	}, vcmserver.SetConfigV1Params{})
	require.NoError(t, err)
}

func TestSetConfigV1_NilConfigStoreStillSucceeds(t *testing.T) {
	handler := Handler{}
	err := handler.SetConfigV1(context.Background(), &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{
			Project:  "p",
			Instance: "i",
			Database: "d",
		},
	}, vcmserver.SetConfigV1Params{})
	assert.NoError(t, err)
}

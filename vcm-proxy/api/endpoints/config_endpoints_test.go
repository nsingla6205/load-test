package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
)

func TestSetConfigV1_ReturnsNilError(t *testing.T) {
	handler := Handler{
		ConfigStore: vcmconfig.NewStore(log.NewLogger()),
	}

	err := handler.SetConfigV1(context.Background(), &vcmserver.SetConfigRequestV1{
		DbConfig: vcmserver.DbConfigV1{
			Project:  "p",
			Instance: "i",
			Database: "d",
		},
	})
	require.NoError(t, err)
}

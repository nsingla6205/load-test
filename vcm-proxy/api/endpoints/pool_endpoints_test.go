package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

func TestPoolCreateV1_ReturnsStubSuccess(t *testing.T) {
	response, err := (Handler{}).PoolCreateV1(context.Background(), &vcmserver.PoolRequestV1{})

	require.NoError(t, err)
	require.IsType(t, &vcmserver.PoolCreateV1OK{}, response)
}

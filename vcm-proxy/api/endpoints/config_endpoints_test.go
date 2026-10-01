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

func TestGetConfigV1_ReturnsAppliedConfig(t *testing.T) {
	store := vcmconfig.NewStore(log.NewLogger(), 0)
	applied := &vcmserver.SetConfigRequestV1{
		StateDbConfig: vcmserver.StateDbConfigV1{
			Version:  vcmserver.NewOptInt64(7),
			Project:  "p",
			Instance: "i",
			Database: "d",
		},
		ClusterConfig: vcmserver.NewOptClusterConfigV1(vcmserver.ClusterConfigV1{
			Version: vcmserver.NewOptInt64(3),
		}),
	}
	store.Apply(context.Background(), applied)

	handler := Handler{ConfigStore: store}
	got, err := handler.GetConfigV1(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(7), got.StateDbConfig.Version.Value)
	assert.Equal(t, "p", got.StateDbConfig.Project)
	assert.Equal(t, "i", got.StateDbConfig.Instance)
	assert.Equal(t, "d", got.StateDbConfig.Database)
	require.True(t, got.ClusterConfig.IsSet())
	assert.Equal(t, int64(3), got.ClusterConfig.Value.Version.Value)
}

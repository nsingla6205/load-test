package prepoolinfra

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpannerEmulatorDBConfig_HostNotSet_ReturnsNil(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "")

	assert.False(t, SpannerEmulatorEnabled())
}

func TestSpannerEmulatorEnabled_HostSet_ReturnsTrue(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")

	assert.True(t, SpannerEmulatorEnabled())
}

func TestEnsureSpannerEmulatorDatabase_HostNotSet_SkipsCreation(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "")

	err := ensureSpannerEmulatorDatabase(context.Background(), nil)

	require.NoError(t, err)
}

func TestEnsureSpannerEmulatorDatabase_HostSetWithoutConfig_ReturnsError(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")

	err := ensureSpannerEmulatorDatabase(context.Background(), nil)

	assert.ErrorContains(t, err, "dbConfig is required")
}

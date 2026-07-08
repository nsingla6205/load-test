package vcm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpannerConfig_ResolveDatabasePath(t *testing.T) {
	t.Run("full path", func(t *testing.T) {
		cfg := SpannerConfig{DatabasePath: "projects/p/instances/i/databases/d"}
		path, err := cfg.ResolveDatabasePath()
		require.NoError(t, err)
		assert.Equal(t, "projects/p/instances/i/databases/d", path)
	})

	t.Run("from parts", func(t *testing.T) {
		cfg := SpannerConfig{
			Project:  "local-dev",
			Instance: "local-instance",
			Database: "vcm",
		}
		path, err := cfg.ResolveDatabasePath()
		require.NoError(t, err)
		assert.Equal(t, "projects/local-dev/instances/local-instance/databases/vcm", path)
	})

	t.Run("missing config", func(t *testing.T) {
		_, err := (SpannerConfig{}).ResolveDatabasePath()
		assert.Error(t, err)
	})
}

func TestSpannerConfig_resolveDSN(t *testing.T) {
	t.Run("plain path", func(t *testing.T) {
		dsn, err := SpannerConfig{DatabasePath: "projects/p/instances/i/databases/d"}.resolveDSN()
		require.NoError(t, err)
		assert.Equal(t, "projects/p/instances/i/databases/d", dsn)
	})

	t.Run("create-if-missing adds emulator auto-config", func(t *testing.T) {
		dsn, err := SpannerConfig{
			DatabasePath:    "projects/p/instances/i/databases/d",
			CreateIfMissing: true,
		}.resolveDSN()
		require.NoError(t, err)
		assert.Equal(t, "projects/p/instances/i/databases/d?auto_config_emulator=true", dsn)
	})

	t.Run("missing config errors", func(t *testing.T) {
		_, err := SpannerConfig{}.resolveDSN()
		assert.Error(t, err)
	})
}

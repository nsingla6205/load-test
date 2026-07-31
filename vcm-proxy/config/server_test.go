package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()

	value, wasSet := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv(key, value))
			return
		}
		require.NoError(t, os.Unsetenv(key))
	})
}

func TestLoadServerConfig_Defaults(t *testing.T) {
	for _, key := range []string{
		"VCM_PROXY_HOST",
		"VCM_PROXY_PORT",
		"READ_TIMEOUT",
		"WRITE_TIMEOUT",
		"IDLE_TIMEOUT",
		"READ_HEADER_TIMEOUT",
		"VCM_WORKFLOW_UI_ENABLED",
	} {
		unsetEnv(t, key)
	}
	t.Setenv("LOCAL_REGION", "")

	cfg := LoadServerConfig()

	require.Equal(t, defaultHost, cfg.Host)
	require.Equal(t, defaultPort, cfg.Port)
	require.Equal(t, defaultReadTimeout, cfg.ReadTimeout)
	require.Equal(t, defaultWriteTimeout, cfg.WriteTimeout)
	require.Equal(t, defaultIdleTimeout, cfg.IdleTimeout)
	require.Equal(t, defaultReadHeaderTimeout, cfg.ReadHeaderTimeout)
	require.False(t, cfg.WorkflowUIEnabled)
}

func TestLoadServerConfig_Overrides(t *testing.T) {
	t.Setenv("VCM_PROXY_HOST", "127.0.0.1")
	t.Setenv("VCM_PROXY_PORT", "9092")
	t.Setenv("READ_TIMEOUT", "10s")
	t.Setenv("WRITE_TIMEOUT", "20s")
	t.Setenv("IDLE_TIMEOUT", "30s")
	t.Setenv("READ_HEADER_TIMEOUT", "1s")
	t.Setenv("VCM_WORKFLOW_UI_ENABLED", "false")

	cfg := LoadServerConfig()

	require.Equal(t, "127.0.0.1", cfg.Host)
	require.Equal(t, "9092", cfg.Port)
	require.Equal(t, 10*time.Second, cfg.ReadTimeout)
	require.Equal(t, 20*time.Second, cfg.WriteTimeout)
	require.Equal(t, 30*time.Second, cfg.IdleTimeout)
	require.Equal(t, time.Second, cfg.ReadHeaderTimeout)
	require.False(t, cfg.WorkflowUIEnabled)
}

func TestLoadServerConfig_InvalidDurationsUseDefaults(t *testing.T) {
	t.Setenv("READ_TIMEOUT", "invalid")
	t.Setenv("WRITE_TIMEOUT", "invalid")
	t.Setenv("IDLE_TIMEOUT", "invalid")
	t.Setenv("READ_HEADER_TIMEOUT", "invalid")

	cfg := LoadServerConfig()

	require.Equal(t, defaultReadTimeout, cfg.ReadTimeout)
	require.Equal(t, defaultWriteTimeout, cfg.WriteTimeout)
	require.Equal(t, defaultIdleTimeout, cfg.IdleTimeout)
	require.Equal(t, defaultReadHeaderTimeout, cfg.ReadHeaderTimeout)
}

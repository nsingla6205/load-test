package common

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

func TestLoadConfig(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "us-central1")
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
		}()
		config := LoadConfig()
		if config == nil {
			t.Errorf("expected config to be set")
		}
	})
	t.Run("FailureOnWrongRegion", func(t *testing.T) {
		if os.Getenv("BE_CRASHER") == "1" {
			_ = os.Setenv("LOCAL_REGION", "invalid-region")
			_ = os.Setenv("REGION_NUMBER_MAP", `{"us-central1": "34"}`)
			LoadConfig() // This will call os.Exit(1)
			return
		}

		cmd := exec.Command(os.Args[0], "-test.run=TestLoadConfig/FailureOnWrongRegion")
		cmd.Env = append(os.Environ(), "BE_CRASHER=1")
		err := cmd.Run()

		if exitError, ok := err.(*exec.ExitError); ok {
			waitStatus, ok := exitError.Sys().(syscall.WaitStatus)
			if !ok {
				t.Fatal("Expected syscall.WaitStatus from process exit")
			}
			assert.Equal(t, 1, waitStatus.ExitStatus())
		} else {
			t.Fatal("Expected exit code 1, but process didn't exit")
		}
	})
}

func TestValidateRegionMap(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "us-central1")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{"us-central1": "34"}`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
	t.Run("RegionNotMapped", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "unknown-region")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{"africa-south1": "01"`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if err == nil {
			t.Errorf("expected error for unmapped region, got nil")
		}
	})
	t.Run("InvalidJSON", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "us-central1")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{africa-south1}`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if err == nil {
			t.Errorf("expected error for invalid JSON, got nil")
		}
	})
	t.Run("DuplicateRegionNumber", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "us-central1")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{"africa-south1": "34","us-central1": "34"}`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if !strings.Contains(err.Error(), "duplicate region code value found") {
			t.Errorf("expected error for duplicate region code")
		}
	})
	t.Run("EmptyRegionMap", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "us-central1")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{}`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if err == nil {
			t.Errorf("expected error for empty region map, got nil")
		}
	})
	t.Run("EmptyLocalRegion", func(t *testing.T) {
		err := os.Setenv("LOCAL_REGION", "")
		if err != nil {
			return
		}
		err = os.Setenv("REGION_NUMBER_MAP", `{"us-central1": "34"}`)
		if err != nil {
			return
		}
		defer func() {
			err := os.Unsetenv("LOCAL_REGION")
			if err != nil {
				t.Errorf("Failed to unset region")
			}
			err = os.Unsetenv("REGION_NUMBER_MAP")
			if err != nil {
				return
			}
		}()
		region := env.GetString("LOCAL_REGION", "")
		regionMapJsonForNodeSerialNumber := env.GetString("REGION_NUMBER_MAP", "")
		err = validateRegionMap(region, regionMapJsonForNodeSerialNumber)
		if err == nil {
			t.Errorf("expected error for empty local region, got nil")
		}
	})
}

func TestLoadConfig_MetricsDBEnvVars(t *testing.T) {
	// Set environment variables for metrics DB
	_ = os.Setenv("LOCAL_REGION", "us-central1")
	_ = os.Setenv("METRICS_DB_TYPE", "sqlite")
	_ = os.Setenv("METRICS_DB_HOST", "localhost")
	_ = os.Setenv("METRICS_DB_PORT", "1234")
	_ = os.Setenv("METRICS_DB_USER", "metricsuser")
	_ = os.Setenv("METRICS_DB_PASSWORD", "metricspass")
	_ = os.Setenv("METRICS_DB_NAME", "metricsdb")
	_ = os.Setenv("METRICS_DB_SSL_MODE", "require")
	_ = os.Setenv("METRICS_DB_TIMEZONE", "Asia/Kolkata")
	_ = os.Setenv("METRICS_DB_MAX_OPEN_CONNS", "99")
	_ = os.Setenv("METRICS_DB_MAX_IDLE_CONNS", "77")
	_ = os.Setenv("METRICS_DB_CONN_MAX_LIFETIME", "2h")

	cfg := LoadConfig()
	assert.NotNil(t, cfg)
	assert.Equal(t, "sqlite", cfg.MetricsDBType)
	assert.Equal(t, "localhost", cfg.MetricsDBHost)
	assert.Equal(t, "1234", cfg.MetricsDBPort)
	assert.Equal(t, "metricsuser", cfg.MetricsDBUser)
	assert.Equal(t, "metricspass", cfg.MetricsDBPassword)
	assert.Equal(t, "metricsdb", cfg.MetricsDBName)
	assert.Equal(t, "require", cfg.MetricsDBSSLMode)
	assert.Equal(t, 99, cfg.MetricsDBMaxOpenConns)
	assert.Equal(t, 77, cfg.MetricsDBMaxIdleConns)
	assert.Equal(t, 2*time.Hour, cfg.MetricsDBConnMaxLifetime)
	assert.Equal(t, "Asia/Kolkata", cfg.MetricsDBTimeZone.String())

	// Clean up
	_ = os.Unsetenv("LOCAL_REGION")
	_ = os.Unsetenv("METRICS_DB_TYPE")
	_ = os.Unsetenv("METRICS_DB_HOST")
	_ = os.Unsetenv("METRICS_DB_PORT")
	_ = os.Unsetenv("METRICS_DB_USER")
	_ = os.Unsetenv("METRICS_DB_PASSWORD")
	_ = os.Unsetenv("METRICS_DB_NAME")
	_ = os.Unsetenv("METRICS_DB_SSL_MODE")
	_ = os.Unsetenv("METRICS_DB_TIMEZONE")
	_ = os.Unsetenv("METRICS_DB_MAX_OPEN_CONNS")
	_ = os.Unsetenv("METRICS_DB_MAX_IDLE_CONNS")
	_ = os.Unsetenv("METRICS_DB_CONN_MAX_LIFETIME")
}

func TestLoadConfig_RefreshHarvestOnUpgrade(t *testing.T) {
	t.Run("DefaultValueIsFalse", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("REFRESH_HARVEST_ON_UPGRADE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.False(t, cfg.RefreshHarvestOnUpgrade, "REFRESH_HARVEST_ON_UPGRADE defaults to false")
	})

	t.Run("DisabledWhenEnvVarFalse", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("REFRESH_HARVEST_ON_UPGRADE", "false")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("REFRESH_HARVEST_ON_UPGRADE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.False(t, cfg.RefreshHarvestOnUpgrade)
	})

	t.Run("EnabledWhenEnvVarTrue", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("REFRESH_HARVEST_ON_UPGRADE", "true")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("REFRESH_HARVEST_ON_UPGRADE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.True(t, cfg.RefreshHarvestOnUpgrade)
	})
}

func TestLoadConfig_SnapshotAPISyncMode(t *testing.T) {
	t.Run("DefaultValueIsAsync", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("SNAPSHOT_API_SYNC_MODE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.Equal(t, false, cfg.SnapshotAPISyncMode)
	})

	t.Run("SyncModeWhenEnvVarSet", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("SNAPSHOT_API_SYNC_MODE", "true")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("SNAPSHOT_API_SYNC_MODE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.Equal(t, true, cfg.SnapshotAPISyncMode)
	})

	t.Run("AsyncModeWhenEnvVarSet", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("SNAPSHOT_API_SYNC_MODE", "false")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("SNAPSHOT_API_SYNC_MODE")
		}()

		cfg := LoadConfig()
		assert.NotNil(t, cfg)
		assert.Equal(t, false, cfg.SnapshotAPISyncMode)
	})
}

func TestLoadConfig_InvalidTimezone(t *testing.T) {
	t.Run("InvalidDBTimezone", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("DB_TIMEZONE", "Invalid/Timezone")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("DB_TIMEZONE")
		}()

		cfg := LoadConfig()
		assert.Nil(t, cfg) // Should return nil on invalid timezone
	})

	t.Run("InvalidMetricsDBTimezone", func(t *testing.T) {
		_ = os.Setenv("LOCAL_REGION", "us-central1")
		_ = os.Setenv("METRICS_DB_TIMEZONE", "Invalid/Timezone")
		defer func() {
			_ = os.Unsetenv("LOCAL_REGION")
			_ = os.Unsetenv("METRICS_DB_TIMEZONE")
		}()

		cfg := LoadConfig()
		assert.Nil(t, cfg) // Should return nil on invalid metrics DB timezone
	})
}

func TestParseDuration(t *testing.T) {
	t.Run("ValidDuration", func(t *testing.T) {
		duration := parseDuration("30s")
		assert.Equal(t, 30*time.Second, duration)
	})

	t.Run("InvalidDuration", func(t *testing.T) {
		duration := parseDuration("invalid")
		assert.Equal(t, time.Duration(0), duration) // Should return 0 on error
	})

	t.Run("EmptyDuration", func(t *testing.T) {
		duration := parseDuration("")
		assert.Equal(t, time.Duration(0), duration) // Should return 0 on error
	})
}

func TestLoadTelemetryConfig(t *testing.T) {
	_ = os.Setenv("LOCAL_REGION", "us-central1")
	defer func() {
		_ = os.Unsetenv("LOCAL_REGION")
	}()

	cfg := LoadTelemetryConfig()
	assert.NotNil(t, cfg)
	// LoadTelemetryConfig should return the same as LoadConfig
	cfg2 := LoadConfig()
	assert.NotNil(t, cfg2)
	assert.Equal(t, cfg.GCPPort, cfg2.GCPPort)
}

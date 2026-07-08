package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigFromEnv(t *testing.T) {
	t.Run("reads all variables", func(t *testing.T) {
		t.Setenv("NAMESPACE", "vsa-prod")
		t.Setenv("DRY_RUN", "true")
		t.Setenv("DB_HOST", "db.example.com")
		t.Setenv("DB_PORT", "5433")
		t.Setenv("DB_NAME", "mydb")
		t.Setenv("DB_USER", "admin")
		t.Setenv("DB_PASSWORD", "secret")
		t.Setenv("DB_SSL_MODE", "require")

		cfg := configFromEnv()

		assert.Equal(t, "vsa-prod", cfg.namespace)
		assert.True(t, cfg.dryRun)
		assert.Equal(t, "db.example.com", cfg.dbHost)
		assert.Equal(t, "5433", cfg.dbPort)
		assert.Equal(t, "mydb", cfg.dbName)
		assert.Equal(t, "admin", cfg.dbUser)
		assert.Equal(t, "secret", cfg.dbPassword)
		assert.Equal(t, "require", cfg.dbSSLMode)
		assert.False(t, cfg.cloudSqlIAMAuthEnabled)
	})

	t.Run("cloud sql iam auth enabled only when set to true", func(t *testing.T) {
		t.Setenv("CLOUD_SQL_IAM_AUTH_ENABLED", "true")
		assert.True(t, configFromEnv().cloudSqlIAMAuthEnabled)

		t.Setenv("CLOUD_SQL_IAM_AUTH_ENABLED", "1")
		assert.False(t, configFromEnv().cloudSqlIAMAuthEnabled)

		t.Setenv("CLOUD_SQL_IAM_AUTH_ENABLED", "")
		assert.False(t, configFromEnv().cloudSqlIAMAuthEnabled)
	})

	t.Run("applies defaults when optional vars are unset", func(t *testing.T) {
		t.Setenv("DB_PORT", "")
		t.Setenv("DB_NAME", "")
		t.Setenv("DB_SSL_MODE", "")

		cfg := configFromEnv()

		assert.Equal(t, "5432", cfg.dbPort)
		assert.Equal(t, "vcp", cfg.dbName)
		assert.Equal(t, "disable", cfg.dbSSLMode)
	})

	t.Run("dry_run is false unless explicitly true", func(t *testing.T) {
		t.Setenv("DRY_RUN", "1")
		assert.False(t, configFromEnv().dryRun)

		t.Setenv("DRY_RUN", "")
		assert.False(t, configFromEnv().dryRun)

		t.Setenv("DRY_RUN", "true")
		assert.True(t, configFromEnv().dryRun)
	})
}

func TestEnvDefault(t *testing.T) {
	t.Run("returns env value when set", func(t *testing.T) {
		t.Setenv("MY_KEY", "custom")
		assert.Equal(t, "custom", envDefault("MY_KEY", "fallback"))
	})

	t.Run("returns default when env is unset", func(t *testing.T) {
		t.Setenv("MY_KEY", "")
		assert.Equal(t, "fallback", envDefault("MY_KEY", "fallback"))
	})
}

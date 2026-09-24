package utils

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dsnTestConfig() DbConfig {
	return DbConfig{
		Type:              Postgres,
		Host:              "db.internal",
		Port:              "5432",
		User:              "telemetry",
		Password:          "app-secret",
		Name:              "telemetrydb",
		SSLMode:           "require",
		TimeZone:          "UTC",
		ConnectionTimeout: 5,
		AdminUser:         "admin",
		AdminPassword:     "admin-secret",
	}
}

func TestPostgresDSN(t *testing.T) {
	dsn, err := PostgresDSN(dsnTestConfig(), false)
	require.NoError(t, err)

	assert.Equal(t,
		"postgres://telemetry:app-secret@db.internal:5432/telemetrydb?connect_timeout=5&sslmode=require&timezone=UTC",
		dsn)
}

// TestPostgresDSN_AdminUsesAdminCredentials pins the isAdmin switch to the
// credentials only: an admin connection still targets the same database.
func TestPostgresDSN_AdminUsesAdminCredentials(t *testing.T) {
	config := dsnTestConfig()

	dsn, err := PostgresDSN(config, true)
	require.NoError(t, err)

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	password, _ := u.User.Password()
	assert.Equal(t, config.AdminUser, u.User.Username())
	assert.Equal(t, config.AdminPassword, password)
	assert.Equal(t, "/"+config.Name, u.Path)
}

func TestPostgresDSN_EmptyTypeDefaultsToPostgres(t *testing.T) {
	config := dsnTestConfig()
	config.Type = ""

	dsn, err := PostgresDSN(config, false)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(dsn, Postgres+"://"), "got %q", dsn)
}

// TestPostgresDSN_EscapesCredentials matters because the DSN is handed to drivers
// that parse it as a URL: an unescaped password would silently move the host or
// truncate the connection string.
func TestPostgresDSN_EscapesCredentials(t *testing.T) {
	config := dsnTestConfig()
	config.Password = "p@ss:w/ord?#"

	dsn, err := PostgresDSN(config, false)
	require.NoError(t, err)

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	password, ok := u.User.Password()
	require.True(t, ok)
	assert.Equal(t, config.Password, password, "the password must survive a round trip")
	assert.Equal(t, config.Host+":"+config.Port, u.Host, "escaping must not corrupt the host")
}

package vcm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// uniqueSQLiteDSN keeps each test isolated yet shareable across pooled conns.
func uniqueSQLiteDSN(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("file:vcm-%s?mode=memory&cache=shared", t.Name())
}

func TestGormStore_ConnectBranches(t *testing.T) {
	t.Run("connect twice exercises already-connected and pool tuning", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{
			DSN:             uniqueSQLiteDSN(t),
			MaxOpenConns:    1,
			MaxIdleConns:    1,
			ConnMaxLifetime: time.Minute,
		}, log.NewLogger())
		require.NoError(t, s.Connect(false))
		t.Cleanup(func() { _ = s.Close() })
		// Second connect returns via the already-connected health check.
		require.NoError(t, s.Connect(false))
	})

	t.Run("connect with unsupported driver errors", func(t *testing.T) {
		s := newGormStore(Driver("mysql"), SQLConfig{}, log.NewLogger())
		assert.Error(t, s.Connect(false))
	})

	t.Run("migrate before connect errors", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
		assert.Error(t, s.Migrate(context.Background()))
	})

	t.Run("close before connect is a no-op", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
		assert.NoError(t, s.Close())
	})
}

// TestOpen_SQLiteLifecycle drives the generic Open factory end to end for the
// SQLite driver: build store, connect-with-retry, run migration on start, then
// health-check. No domain schema is involved (Migrate is a no-op).
func TestOpen_SQLiteLifecycle(t *testing.T) {
	dsn := uniqueSQLiteDSN(t)
	store, err := Open(context.Background(), Config{
		Driver: DriverSQLite,
		SQLite: SQLConfig{DSN: dsn, MaxOpenConns: 1},
	}, log.NewLogger(), true)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, store.HealthCheck())
	require.NoError(t, store.Migrate(context.Background()))
	assert.Equal(t, dsn, store.DatabasePath())
}

// TestOpen_FactoryGuards covers the Open factory guards: an unknown driver, the
// fail-fast empty-Postgres-DSN path, and Spanner path-resolution failure. All
// fail before any connection attempt.
func TestOpen_FactoryGuards(t *testing.T) {
	_, err := Open(context.Background(), Config{Driver: "mysql"}, log.NewLogger(), false)
	assert.Error(t, err)

	_, err = Open(context.Background(), Config{Driver: DriverPostgres}, log.NewLogger(), false)
	assert.Error(t, err)

	_, err = Open(context.Background(), Config{Driver: DriverSpanner, Spanner: SpannerConfig{}}, log.NewLogger(), false)
	assert.Error(t, err)
}

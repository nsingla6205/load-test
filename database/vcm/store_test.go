package vcm

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestConfig_DriverOrDefault(t *testing.T) {
	assert.Equal(t, DriverSpanner, Config{}.driverOrDefault())
	assert.Equal(t, DriverSpanner, Config{Driver: DriverSpanner}.driverOrDefault())
	assert.Equal(t, DriverPostgres, Config{Driver: DriverPostgres}.driverOrDefault())
	assert.Equal(t, DriverSQLite, Config{Driver: DriverSQLite}.driverOrDefault())
}

func TestGormStore_DisplayPath(t *testing.T) {
	t.Run("postgres hides dsn", func(t *testing.T) {
		s := newGormStore(DriverPostgres, SQLConfig{DSN: "host=secret user=admin password=hunter2"}, log.NewLogger())
		assert.Equal(t, "postgres", s.DatabasePath())
	})

	t.Run("sqlite reports dsn", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{DSN: "vcm.db"}, log.NewLogger())
		assert.Equal(t, "vcm.db", s.DatabasePath())
	})

	t.Run("sqlite empty dsn defaults to shared memory", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
		assert.Equal(t, "file:vcm?mode=memory&cache=shared", s.DatabasePath())
	})

	t.Run("spanner reports path without query options", func(t *testing.T) {
		s := newGormStore(DriverSpanner, SQLConfig{DSN: "projects/p/instances/i/databases/d?auto_config_emulator=true"}, log.NewLogger())
		assert.Equal(t, "projects/p/instances/i/databases/d", s.DatabasePath())
	})
}

func TestGormStore_Dialector(t *testing.T) {
	t.Run("postgres without dsn errors", func(t *testing.T) {
		s := newGormStore(DriverPostgres, SQLConfig{}, log.NewLogger())
		_, err := s.dialector()
		assert.Error(t, err)
	})

	t.Run("spanner without dsn errors", func(t *testing.T) {
		s := newGormStore(DriverSpanner, SQLConfig{}, log.NewLogger())
		_, err := s.dialector()
		assert.Error(t, err)
	})

	t.Run("spanner with path ok", func(t *testing.T) {
		s := newGormStore(DriverSpanner, SQLConfig{DSN: "projects/p/instances/i/databases/d"}, log.NewLogger())
		d, err := s.dialector()
		require.NoError(t, err)
		assert.NotNil(t, d)
	})

	t.Run("postgres with dsn ok", func(t *testing.T) {
		s := newGormStore(DriverPostgres, SQLConfig{DSN: "host=localhost"}, log.NewLogger())
		d, err := s.dialector()
		require.NoError(t, err)
		assert.NotNil(t, d)
	})

	t.Run("sqlite ok", func(t *testing.T) {
		s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
		d, err := s.dialector()
		require.NoError(t, err)
		assert.NotNil(t, d)
	})

	t.Run("unsupported driver errors", func(t *testing.T) {
		s := newGormStore(Driver("mysql"), SQLConfig{}, log.NewLogger())
		_, err := s.dialector()
		assert.Error(t, err)
	})
}

func TestGormStore_SetupDatabaseIsNoOp(t *testing.T) {
	s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
	assert.NoError(t, s.SetupDatabase(context.Background()))
}

func TestGormStore_HealthCheckBeforeConnect(t *testing.T) {
	s := newGormStore(DriverSQLite, SQLConfig{}, log.NewLogger())
	assert.Error(t, s.HealthCheck())
}

// alwaysFailStore is a Storage whose Connect always fails, used to exercise the
// retry path without a real backend.
type alwaysFailStore struct {
	Storage
	attempts int
}

func (s *alwaysFailStore) Connect(bool) error {
	s.attempts++
	return fmt.Errorf("connect boom")
}

func TestConnectWithRetry_AbortsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &alwaysFailStore{}
	err := connectWithRetry(ctx, log.NewLogger(), store)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	// A cancelled context must short-circuit the backoff, not run all attempts.
	assert.Less(t, store.attempts, connectRetryAttempts)
}

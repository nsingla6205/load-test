package vcm

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// Open connects a store for the backend selected by cfg.Driver (with connection
// retry and optional migrate-on-start) and returns the backend-agnostic Storage.
func Open(ctx context.Context, cfg Config, logger log.Logger, runMigrationOnStart bool) (Storage, error) {
	driver := cfg.driverOrDefault()

	sqlCfg, err := resolveSQLConfig(driver, cfg)
	if err != nil {
		return nil, err
	}

	store := newGormStore(driver, sqlCfg, logger)

	if err := connectWithRetry(ctx, logger, store); err != nil {
		_ = store.Close()
		return nil, err
	}

	if runMigrationOnStart {
		if err := store.Migrate(ctx); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("vcm: migration failed: %w", err)
		}
	}

	return store, nil
}

// resolveSQLConfig maps the selected driver's settings to the GORM store's DSN
// form, failing fast on non-transient misconfiguration before any retry loop.
func resolveSQLConfig(driver Driver, cfg Config) (SQLConfig, error) {
	switch driver {
	case DriverSpanner:
		dsn, err := cfg.Spanner.resolveDSN()
		if err != nil {
			return SQLConfig{}, err
		}
		return SQLConfig{DSN: dsn}, nil
	case DriverPostgres:
		if cfg.Postgres.DSN == "" {
			return SQLConfig{}, fmt.Errorf("vcm: postgres driver requires a DSN")
		}
		return cfg.Postgres, nil
	case DriverSQLite:
		return cfg.SQLite, nil
	default:
		return SQLConfig{}, fmt.Errorf("vcm: unsupported storage driver %q", cfg.Driver)
	}
}

// Bounded retry tuning: capped exponential backoff with a finite attempt count
// so an unavailable backend fails terminally instead of hanging startup.
const (
	connectRetryAttempts  = 10
	connectRetryBaseDelay = 500 * time.Millisecond
	connectRetryMaxDelay  = 30 * time.Second
)

// connectWithRetry connects the store, retrying transient failures with capped
// exponential backoff. It returns a terminal error after connectRetryAttempts
// failures and aborts early when ctx is cancelled.
func connectWithRetry(ctx context.Context, logger log.Logger, store Storage) error {
	var lastErr error
	for attempt := 0; attempt < connectRetryAttempts; attempt++ {
		err := store.Connect(false)
		if err == nil {
			return nil
		}
		lastErr = err

		delay := connectRetryBaseDelay * time.Duration(1<<uint(attempt))
		if delay > connectRetryMaxDelay {
			delay = connectRetryMaxDelay
		}
		// Raw error may carry project paths / DSN credentials: debug only.
		logger.Error("Failed to connect to VCM store, retrying...",
			"attempt", attempt+1,
			"max_attempts", connectRetryAttempts,
			"retry_delay", delay.String())
		logger.Debug("VCM store connection error",
			"attempt", attempt+1,
			"error", lastErr.Error())

		select {
		case <-ctx.Done():
			return fmt.Errorf("vcm: connect aborted after %d attempts: %w", attempt+1, ctx.Err())
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("vcm: failed to connect after %d attempts: %w", connectRetryAttempts, lastErr)
}

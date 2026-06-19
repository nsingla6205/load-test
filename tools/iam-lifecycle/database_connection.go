package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/lib/pq"
)

func connectAdmin(cfg config, dbName string) (*sql.DB, error) {
	dsn := adminDSN(cfg, dbName)
	return openWithRetry(dsn)
}

func connectIAM(cfg config, dbName, iamUser, port string) (*sql.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s",
		cfg.dbHost, port, iamUser, dbName, cfg.dbSSLMode)
	return openWithRetry(dsn)
}

func adminDSN(cfg config, dbName string) string {
	if cfg.adminPass == "" {
		return fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s",
			cfg.dbHost, cfg.dbPort, cfg.adminUser, dbName, cfg.dbSSLMode)
	}
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.dbHost, cfg.dbPort, cfg.adminUser, cfg.adminPass, dbName, cfg.dbSSLMode)
}

// databaseExists returns true if the named database exists, false if it does
// not, and an error if the existence check itself failed (connection error,
// permission denied, etc.).
//
// We connect to the `postgres` system database (always present on PostgreSQL
// servers) and query pg_database. This avoids the chicken-and-egg problem
// where the database we want to check doesn't exist yet, so we can't connect
// to it to find out.
func databaseExists(cfg config, dbName string) (bool, error) {
	sysDB, err := connectAdmin(cfg, systemDBName)
	if err != nil {
		return false, fmt.Errorf("connect to %s for existence check of %q: %w", systemDBName, dbName, err)
	}
	defer func() { _ = sysDB.Close() }()
	var exists bool
	if err := sysDB.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		return false, fmt.Errorf("query pg_database for %q: %w", dbName, err)
	}
	return exists, nil
}

// systemDBName is the always-present PostgreSQL system database. We use it as
// a stable connection point for cross-database operations (existence checks,
// role-level GRANTs that don't bind to a specific DB).
const systemDBName = "postgres"

func openWithRetry(dsn string) (*sql.DB, error) {
	return openWithRetryN(dsn, 10)
}

func openWithRetryN(dsn string, maxRetries int) (*sql.DB, error) {
	// Add connect_timeout if not present (prevents indefinite hangs)
	if !containsTimeout(dsn) {
		dsn += " connect_timeout=10"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	var lastErr error
	for i := range maxRetries {
		if err := db.Ping(); err == nil {
			slog.Info("database connection established", "attempt", i+1)
			return db, nil
		} else {
			lastErr = err
			delay := min(time.Duration(500*(1<<uint(i)))*time.Millisecond, 30*time.Second)
			slog.Warn("db connect retry", "attempt", i+1, "delay", delay, "error", sanitizeError(err))
			time.Sleep(delay)
		}
	}
	_ = db.Close()
	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, sanitizeError(lastErr))
}

func containsTimeout(dsn string) bool {
	return containsString(dsn, "connect_timeout") || containsString(dsn, "timeout")
}

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// Remove password from error messages
	if containsString(msg, "password") {
		return fmt.Errorf("authentication error (credentials redacted)")
	}
	return err
}

func execSQL(db *sql.DB, stmt string) error {
	preview := stmt
	if len(preview) > 120 {
		preview = preview[:120] + "..."
	}
	// Sanitize any potential sensitive data from logs
	preview = sanitizeSQL(preview)
	slog.Debug("SQL", "stmt", preview)
	if _, err := db.Exec(stmt); err != nil {
		slog.Error("SQL failed", "error", err, "stmt", preview)
		return err
	}
	return nil
}

func sanitizeSQL(sql string) string {
	// Simple check - if it contains password-like keywords, redact
	if containsString(sql, "PASSWORD") || containsString(sql, "password") {
		return "[SQL statement with credentials redacted]"
	}
	return sql
}

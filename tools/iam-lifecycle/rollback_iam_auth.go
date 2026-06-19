package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

func rollbackAll(cfg config) error {
	if !cfg.vcpEnabled && !cfg.temporalEnabled {
		return fmt.Errorf("nothing to rollback: VCP_ENABLED=false and TEMPORAL_ENABLED=false")
	}

	// VCP-scope validation only applies when this job manages VCP DBs.
	if cfg.vcpEnabled {
		if cfg.vcpDBName == "" {
			return fmt.Errorf("DB_NAME (vcp database name) is required for rollback")
		}
		if cfg.iamVcpCore == "" {
			return fmt.Errorf("IAM_VCP_CORE is required for rollback")
		}
	}
	// Temporal-scope validation only applies when this job manages Temporal DBs.
	if cfg.temporalEnabled {
		if cfg.iamTemporal == "" {
			return fmt.Errorf("IAM_TEMPORAL is required when TEMPORAL_ENABLED=true")
		}
		if cfg.temporalDBName == "" {
			return fmt.Errorf("TEMPORAL_DB_NAME is required when TEMPORAL_ENABLED=true")
		}
		if cfg.temporalVisDBName == "" {
			return fmt.Errorf("TEMPORAL_VISIBILITY_DB_NAME is required when TEMPORAL_ENABLED=true")
		}
	}

	if cfg.vcpEnabled {
		if err := rollbackDB(cfg, cfg.vcpDBName, cfg.iamVcpCore, []string{"postgres"}); err != nil {
			return err
		}
		if err := rollbackDB(cfg, cfg.metricsDBName, cfg.iamVcpCore, []string{"postgres", "metrics"}); err != nil {
			return err
		}
	}
	if cfg.temporalEnabled {
		if err := rollbackDB(cfg, cfg.temporalDBName, cfg.iamTemporal, []string{"postgres"}); err != nil {
			return err
		}
		if err := rollbackDB(cfg, cfg.temporalVisDBName, cfg.iamTemporal, []string{"postgres"}); err != nil {
			return err
		}
	}
	slog.Info("rollback complete for all databases in scope",
		"vcp_enabled", cfg.vcpEnabled,
		"temporal_enabled", cfg.temporalEnabled)
	return nil
}

func rollbackDB(cfg config, dbName, currentOwner string, grantUsers []string) error {
	slog.Info("rollback: processing", "db", dbName)

	// If the database doesn't exist there is nothing to roll back. This can
	// happen on a fresh install where IAM auth was enabled then disabled
	// before the schema setup created the DB.
	exists, err := databaseExists(cfg, dbName)
	if err != nil {
		return fmt.Errorf("[%s] existence check: %w", dbName, err)
	}
	if !exists {
		slog.Info("rollback: database does not exist, nothing to roll back, skipping", "db", dbName)
		return nil
	}

	adminDB, err := connectAdmin(cfg, dbName)
	if err != nil {
		return fmt.Errorf("[%s] admin connect: %w", dbName, err)
	}
	defer func() { _ = adminDB.Close() }()

	if isDBInTargetState(adminDB, cfg.adminUser, grantUsers) {
		slog.Info("rollback: ownership + grants + default privs all correct, skipping", "db", dbName)
		return nil
	}

	if needsOwnershipTransfer(adminDB, cfg.adminUser) {
		slog.Info("R1: ownership transfer needed, transferring back to postgres", "db", dbName, "from", currentOwner)
		if err := rollbackOwnershipViaIAM(cfg, adminDB, dbName, currentOwner); err != nil {
			slog.Warn("IAM-based transfer failed, falling back to SET ROLE", "db", dbName, "error", err)
			if err := rollbackOwnershipViaSetRole(adminDB, dbName, currentOwner, cfg.adminUser); err != nil {
				return fmt.Errorf("[%s] rollback ownership transfer: %w", dbName, err)
			}
		}
	} else {
		slog.Info("R1: ownership already with postgres, skipping", "db", dbName)
	}

	var schemaOwner string
	if err := adminDB.QueryRow(`
		SELECT r.rolname FROM pg_namespace n
		JOIN pg_roles r ON n.nspowner = r.oid
		WHERE n.nspname = 'public'`).Scan(&schemaOwner); err != nil {
		slog.Warn("could not check schema owner, will reset anyway", "db", dbName, "error", err)
	} else if schemaOwner == cfg.adminUser {
		slog.Info("R3: schema already owned by admin, skipping ALTER SCHEMA", "db", dbName)
	}
	if schemaOwner != cfg.adminUser {
		slog.Info("R3: resetting schema owner to admin", "db", dbName, "current_owner", schemaOwner)
		if err := rollbackSchemaOwnership(cfg, adminDB, dbName, currentOwner); err != nil {
			return fmt.Errorf("[%s] schema owner reset: %w", dbName, err)
		}
	}

	// Clean up any temporary role memberships created during ownership
	// transfers above. Both directions may have been granted depending on
	// which transfer path (IAM-connect vs SET ROLE fallback) was used.
	for _, stmt := range []string{
		fmt.Sprintf(`REVOKE %s FROM %s`, qi("postgres"), qi(currentOwner)),
		fmt.Sprintf(`REVOKE %s FROM %s`, qi(currentOwner), qi(cfg.adminUser)),
	} {
		if err := execSQL(adminDB, stmt); err != nil {
			if !strings.Contains(err.Error(), "not a member") {
				slog.Warn("could not revoke temporary role membership", "db", dbName, "stmt", stmt, "error", err)
			}
		}
	}

	if err := grantSchemaUsage(adminDB, grantUsers); err != nil {
		return fmt.Errorf("[%s] rollback schema usage grant: %w", dbName, err)
	}
	nonOwnerUsers := excludeUser(grantUsers, cfg.adminUser)
	if len(nonOwnerUsers) > 0 {
		if err := grantDML(adminDB, nonOwnerUsers); err != nil {
			return fmt.Errorf("[%s] rollback DML grants: %w", dbName, err)
		}
	}
	if err := setDefaultPrivileges(adminDB, cfg.adminUser, grantUsers); err != nil {
		return fmt.Errorf("[%s] rollback default privileges: %w", dbName, err)
	}

	slog.Info("rollback done", "db", dbName)
	return nil
}

func rollbackOwnershipViaIAM(cfg config, adminDB *sql.DB, dbName, currentOwner string) error {
	// Re-ensure postgres role membership in case a previous partial rollback
	// revoked it. Harmless if already granted.
	if err := execSQL(adminDB, fmt.Sprintf(`GRANT postgres TO %s`, qi(currentOwner))); err != nil {
		if !strings.Contains(err.Error(), "already a member") {
			slog.Warn("could not grant postgres role membership, may fail during ownership transfer", "user", currentOwner, "error", err)
		}
	}

	port := iamPort(cfg, currentOwner)
	slog.Info("R1-IAM: connecting as IAM owner", "db", dbName, "user", currentOwner, "port", port)

	dsn := fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s",
		cfg.dbHost, port, currentOwner, dbName, cfg.dbSSLMode)
	iamDB, err := openWithRetryN(dsn, 5)
	if err != nil {
		return fmt.Errorf("connect as %s: %w", currentOwner, err)
	}
	defer func() { _ = iamDB.Close() }()

	return transferOwnership(iamDB, "postgres")
}

func rollbackOwnershipViaSetRole(adminDB *sql.DB, dbName, currentOwner, adminUser string) error {
	slog.Info("R1-SETROLE: fallback", "db", dbName, "from", currentOwner)

	// Best-effort revoke to break circular dependency - non-critical if it fails
	if err := execSQL(adminDB, fmt.Sprintf(`REVOKE %s FROM %s`, qi(adminUser), qi(currentOwner))); err != nil {
		slog.Warn("could not revoke role (may not exist), continuing", "role", adminUser, "from", currentOwner, "error", err)
	}
	if err := execSQL(adminDB, fmt.Sprintf(`GRANT %s TO %s`, qi(currentOwner), qi(adminUser))); err != nil {
		return fmt.Errorf("[%s] grant %s to admin: %w", dbName, currentOwner, err)
	}
	if err := execSQL(adminDB, fmt.Sprintf(`SET ROLE %s`, qi(currentOwner))); err != nil {
		return fmt.Errorf("[%s] SET ROLE %s: %w", dbName, currentOwner, err)
	}
	if err := transferOwnership(adminDB, "postgres"); err != nil {
		_, _ = adminDB.Exec(`RESET ROLE`)
		return fmt.Errorf("[%s] transfer ownership: %w", dbName, err)
	}
	if _, err := adminDB.Exec(`RESET ROLE`); err != nil {
		return fmt.Errorf("[%s] RESET ROLE: %w", dbName, err)
	}
	return nil
}

// rollbackSchemaOwnership transfers the public schema back to the admin user.
// Preferred path: connect as the IAM user (who owns the schema and already has
// GRANT postgres TO <iam>) so that ALTER SCHEMA succeeds without needing
// postgres to be a member of the IAM role. Falls back to a temporary reverse
// grant on the admin session if the IAM connection fails.
func rollbackSchemaOwnership(cfg config, adminDB *sql.DB, dbName, currentOwner string) error {
	port := iamPort(cfg, currentOwner)
	slog.Info("R3-IAM: connecting as IAM owner for schema rollback", "db", dbName, "user", currentOwner, "port", port)

	dsn := fmt.Sprintf("host=%s port=%s user=%s dbname=%s sslmode=%s",
		cfg.dbHost, port, currentOwner, dbName, cfg.dbSSLMode)
	iamDB, err := openWithRetryN(dsn, 5)
	if err == nil {
		defer func() { _ = iamDB.Close() }()
		if schemaErr := execSQL(iamDB, fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s`, qi(cfg.adminUser))); schemaErr == nil {
			slog.Info("R3-IAM: schema ownership transferred back to admin", "db", dbName)
			return nil
		} else {
			slog.Warn("R3-IAM: ALTER SCHEMA failed via IAM connection, falling back to admin session", "db", dbName, "error", schemaErr)
		}
	} else {
		slog.Warn("R3-IAM: could not connect as IAM user, falling back to admin session", "db", dbName, "error", err)
	}

	// Fallback: temporary reverse grant on the admin session, same pattern as
	// enable path but in the opposite direction.
	slog.Info("R3-fallback: using temporary reverse grant on admin session", "db", dbName)
	// Revoke opposite grant first to avoid circular role membership.
	if err := execSQL(adminDB, fmt.Sprintf(`REVOKE %s FROM %s`, qi(cfg.adminUser), qi(currentOwner))); err != nil {
		if !strings.Contains(err.Error(), "not a member") {
			slog.Warn("could not revoke opposite grant before reverse grant", "db", dbName, "error", err)
		}
	}
	if err := execSQL(adminDB, fmt.Sprintf(`GRANT %s TO %s`, qi(currentOwner), qi(cfg.adminUser))); err != nil {
		return fmt.Errorf("[%s] temporary reverse grant for schema rollback: %w", dbName, err)
	}
	schemaErr := execSQL(adminDB, fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s`, qi(cfg.adminUser)))
	if err := execSQL(adminDB, fmt.Sprintf(`REVOKE %s FROM %s`, qi(currentOwner), qi(cfg.adminUser))); err != nil {
		slog.Warn("could not revoke temporary reverse grant during rollback", "db", dbName, "error", err)
	}
	return schemaErr
}

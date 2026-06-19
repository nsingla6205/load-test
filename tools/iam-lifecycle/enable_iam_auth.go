package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

func enableAll(cfg config) error {
	if !cfg.vcpEnabled && !cfg.temporalEnabled {
		return fmt.Errorf("nothing to enable: VCP_ENABLED=false and TEMPORAL_ENABLED=false")
	}

	// VCP-scope validation only applies when this job manages VCP DBs.
	// metrics DB is always managed alongside VCP.
	if cfg.vcpEnabled {
		if cfg.vcpDBName == "" {
			return fmt.Errorf("DB_NAME (vcp database name) is required")
		}
		if cfg.iamVcpCore == "" {
			return fmt.Errorf("IAM_VCP_CORE is required")
		}
		if cfg.iamVcpWorker == "" {
			return fmt.Errorf("IAM_VCP_WORKER is required")
		}
		if cfg.metricsDBName == "" {
			return fmt.Errorf("METRICS_DB_NAME is required when VCP_ENABLED=true")
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
		if err := enableDB(cfg, cfg.vcpDBName, cfg.iamVcpCore, cfg.iamVcpCore, vcpGrantUsers(cfg)); err != nil {
			return err
		}
		if err := enableDB(cfg, cfg.metricsDBName, cfg.iamVcpCore, cfg.iamVcpCore, metricsGrantUsers(cfg)); err != nil {
			return err
		}
	}

	if cfg.temporalEnabled {
		// Grant CREATEDB to temporal-ksa (issued from temporal DB context — works
		// in either VCP-mode or Temporal-only mode since CREATEDB is a role-level
		// privilege not bound to a specific database).
		if err := grantCreateDBToTemporal(cfg); err != nil {
			return err
		}

		if err := enableDB(cfg, cfg.temporalDBName, cfg.iamTemporal, cfg.iamTemporal, temporalGrantUsers(cfg)); err != nil {
			return err
		}
		if err := enableDB(cfg, cfg.temporalVisDBName, cfg.iamTemporal, cfg.iamTemporal, temporalGrantUsers(cfg)); err != nil {
			return err
		}
	}

	slog.Info("enable complete for all databases in scope",
		"vcp_enabled", cfg.vcpEnabled,
		"temporal_enabled", cfg.temporalEnabled)
	return nil
}

// grantCreateDBToTemporal grants CREATEDB to the temporal IAM role so
// Temporal's schema management can create the temporal/temporal_visibility
// databases on first install.
//
// We always connect via the `postgres` system database. CREATEDB is a
// role-level (not database-level) privilege, and `postgres` is the only
// database guaranteed to exist on a fresh server — connecting via vcp or
// temporal would race against their own creation on a fresh install.
func grantCreateDBToTemporal(cfg config) error {
	db, err := connectAdmin(cfg, systemDBName)
	if err != nil {
		return fmt.Errorf("connect to %s for CREATEDB grant: %w", systemDBName, err)
	}
	defer func() { _ = db.Close() }()
	var hasCreateDB bool
	if err := db.QueryRow(`SELECT rolcreatedb FROM pg_roles WHERE rolname = $1`, cfg.iamTemporal).Scan(&hasCreateDB); err != nil {
		slog.Warn("could not check CREATEDB, will grant anyway", "user", cfg.iamTemporal, "error", err)
	}
	if hasCreateDB {
		slog.Info("CREATEDB already granted, skipping", "user", cfg.iamTemporal)
		return nil
	}
	slog.Info("granting CREATEDB to temporal-ksa", "user", cfg.iamTemporal, "via_db", systemDBName)
	if err := execSQL(db, fmt.Sprintf(`ALTER ROLE %s CREATEDB`, qi(cfg.iamTemporal))); err != nil {
		return fmt.Errorf("grant CREATEDB to temporal role: %w", err)
	}
	return nil
}

func enableDB(cfg config, dbName, targetOwner, dmlUser string, grantUsers []string) error {
	slog.Info("enable: processing", "db", dbName)

	// Fresh-install path: the database may not exist yet because the
	// downstream schema setup (e.g. Temporal's `temporal-sql-tool create-database`)
	// hasn't run. In IAM-auth mode the schema setup connects as the IAM user,
	// so the new DB will be owned by that user from creation — no IAM
	// lifecycle work is needed for it. Skip cleanly with a clear log.
	exists, err := databaseExists(cfg, dbName)
	if err != nil {
		return fmt.Errorf("[%s] existence check: %w", dbName, err)
	}
	if !exists {
		slog.Info("enable: database does not exist yet, skipping (will be created with IAM ownership by downstream schema setup)",
			"db", dbName, "expected_owner_after_creation", targetOwner)
		return nil
	}

	adminDB, err := connectAdmin(cfg, dbName)
	if err != nil {
		return fmt.Errorf("[%s] admin connect: %w", dbName, err)
	}
	defer func() { _ = adminDB.Close() }()

	if isDBInTargetState(adminDB, targetOwner, grantUsers) {
		slog.Info("enable: ownership + grants + default privs all correct, skipping", "db", dbName)
		return nil
	}

	if needsOwnershipTransfer(adminDB, targetOwner) {
		slog.Info("phase 1A: ownership transfer needed", "db", dbName, "to", targetOwner)
		if err := transferOwnership(adminDB, targetOwner); err != nil {
			return fmt.Errorf("[%s] ownership transfer: %w", dbName, err)
		}
	} else {
		slog.Info("phase 1A: ownership already correct, skipping", "db", dbName, "owner", targetOwner)
	}

	var schemaOwner string
	if err := adminDB.QueryRow(`
		SELECT r.rolname FROM pg_namespace n
		JOIN pg_roles r ON n.nspowner = r.oid
		WHERE n.nspname = 'public'`).Scan(&schemaOwner); err != nil {
		slog.Warn("could not check schema owner, will set anyway", "db", dbName, "error", err)
	}
	if schemaOwner != targetOwner {
		slog.Info("phase 1A: transferring schema ownership", "db", dbName, "from", schemaOwner, "to", targetOwner)
		if err := transferSchemaOwnershipToTarget(adminDB, cfg, dbName, targetOwner); err != nil {
			return fmt.Errorf("[%s] schema ownership transfer: %w", dbName, err)
		}
	} else {
		slog.Info("phase 1A: schema already owned by target, skipping", "db", dbName, "owner", targetOwner)
	}

	// Permanent one-way membership for rollback and IAM-side ownership ops.
	// Must run after the temporary reverse grant above is revoked to avoid
	// Cloud SQL circular role membership (postgres <-> IAM).
	if err := grantRoleMemberships(adminDB, roleMembershipUsers(cfg)); err != nil {
		return fmt.Errorf("[%s] role membership grant: %w", dbName, err)
	}

	// Object-level grants (schema USAGE, DML, default privileges) must be
	// issued by the object owner. After the ownership transfer above, the IAM
	// user owns all tables, sequences, and the schema — postgres (non-superuser)
	// can no longer grant on them. Connect as the IAM owner for these grants.
	port := iamPort(cfg, dmlUser)
	slog.Info("phase 1B: object grants as IAM owner", "db", dbName, "as", dmlUser, "port", port)
	ownerDB, err := connectIAM(cfg, dbName, dmlUser, port)
	if err != nil {
		return fmt.Errorf("[%s] connect as %s: %w", dbName, dmlUser, err)
	}
	defer func() { _ = ownerDB.Close() }()

	if err := grantSchemaUsage(ownerDB, grantUsers); err != nil {
		return fmt.Errorf("[%s] schema usage grant: %w", dbName, err)
	}
	nonOwnerUsers := excludeUser(grantUsers, dmlUser)
	if len(nonOwnerUsers) > 0 {
		if err := grantDML(ownerDB, nonOwnerUsers); err != nil {
			return fmt.Errorf("[%s] DML grants: %w", dbName, err)
		}
	}
	if err := setDefaultPrivileges(ownerDB, dmlUser, grantUsers); err != nil {
		return fmt.Errorf("[%s] default privileges: %w", dbName, err)
	}

	slog.Info("enable done", "db", dbName)
	return nil
}

// transferSchemaOwnershipToTarget moves public schema ownership to the IAM role.
//
// Cloud SQL requires the session user to be a member of the new owner role.
// A leftover GRANT postgres TO <iam> (forward grant) blocks GRANT <iam> TO postgres
// (reverse grant) with "is a member of role postgres". When the forward grant
// exists, revoke it temporarily, apply the reverse grant, transfer, then restore.
func transferSchemaOwnershipToTarget(db *sql.DB, cfg config, dbName, targetOwner string) error {
	admin := cfg.adminUser

	adminInTarget, err := isRoleMember(db, admin, targetOwner)
	if err != nil {
		return fmt.Errorf("check admin membership in target role: %w", err)
	}
	if adminInTarget {
		slog.Info("admin already member of target role, transferring schema directly", "db", dbName)
		return execSQL(db, fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s`, qi(targetOwner)))
	}

	targetInAdmin, err := isRoleMember(db, targetOwner, admin)
	if err != nil {
		return fmt.Errorf("check target membership in admin role: %w", err)
	}
	restoredForward := false
	if targetInAdmin {
		slog.Info("target already member of admin role, revoking forward grant before reverse grant",
			"db", dbName, "target", targetOwner)
		if err := execSQL(db, fmt.Sprintf(`REVOKE %s FROM %s`, qi(admin), qi(targetOwner))); err != nil {
			if !isRoleGrantError(err, "not a member") {
				return fmt.Errorf("revoke forward grant before schema transfer: %w", err)
			}
		} else {
			restoredForward = true
		}
		defer func() {
			if !restoredForward {
				return
			}
			if err := execSQL(db, fmt.Sprintf(`GRANT %s TO %s`, qi(admin), qi(targetOwner))); err != nil {
				if !isRoleGrantError(err, "already a member") {
					slog.Warn("could not restore forward grant after schema transfer",
						"db", dbName, "target", targetOwner, "error", err)
				}
			}
		}()
	}

	if err := execSQL(db, fmt.Sprintf(`GRANT %s TO %s`, qi(targetOwner), qi(admin))); err != nil {
		if isRoleGrantError(err, "already a member") {
			slog.Info("reverse grant already present, continuing schema transfer", "db", dbName)
		} else {
			return fmt.Errorf("temporary reverse grant for schema transfer: %w", err)
		}
	}

	schemaErr := execSQL(db, fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s`, qi(targetOwner)))

	if err := execSQL(db, fmt.Sprintf(`REVOKE %s FROM %s`, qi(targetOwner), qi(admin))); err != nil {
		if !isRoleGrantError(err, "not a member") {
			slog.Warn("could not revoke temporary reverse grant", "db", dbName, "target", targetOwner, "error", err)
		}
	}

	return schemaErr
}

func isDBInTargetState(db *sql.DB, targetOwner string, grantUsers []string) bool {
	var schemaOK int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_namespace n
		JOIN pg_roles r ON n.nspowner = r.oid
		WHERE n.nspname = 'public' AND r.rolname = $1`, targetOwner,
	).Scan(&schemaOK); err != nil || schemaOK == 0 {
		slog.Info("state check: schema not owned by target", "target", targetOwner)
		return false
	}

	var wrongOwners int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public' AND tableowner != $1`, targetOwner,
	).Scan(&wrongOwners); err != nil || wrongOwners > 0 {
		slog.Info("state check: tables not owned by target", "target", targetOwner, "count", wrongOwners)
		return false
	}

	for _, u := range grantUsers {
		var hasUsage bool
		if err := db.QueryRow(
			`SELECT has_schema_privilege($1, 'public', 'USAGE')`, u,
		).Scan(&hasUsage); err != nil || !hasUsage {
			slog.Info("state check: grant user missing schema USAGE", "user", u)
			return false
		}
	}

	var tableCount int
	if err := db.QueryRow(
		`SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`,
	).Scan(&tableCount); err != nil {
		return false
	}

	// Verify every non-owner grant user has SELECT on all tables (skip if no tables).
	if tableCount > 0 {
		for _, u := range grantUsers {
			if u == targetOwner {
				continue
			}
			var missing int
			if err := db.QueryRow(`
				SELECT count(*) FROM pg_tables t
				WHERE t.schemaname = 'public'
				AND NOT has_table_privilege($1,
					format('%I.%I', t.schemaname, t.tablename), 'SELECT')`, u,
			).Scan(&missing); err != nil || missing > 0 {
				slog.Info("state check: grant user missing DML", "user", u, "missing_tables", missing)
				return false
			}
		}
	}

	// Verify default privileges exist for the target owner (tables + sequences).
	var defAclCount int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_default_acl da
		JOIN pg_namespace n ON da.defaclnamespace = n.oid
		JOIN pg_roles r ON da.defaclrole = r.oid
		WHERE n.nspname = 'public' AND r.rolname = $1`, targetOwner,
	).Scan(&defAclCount); err != nil || defAclCount == 0 {
		slog.Info("state check: default privileges missing", "target", targetOwner)
		return false
	}

	return true
}

func needsOwnershipTransfer(db *sql.DB, targetOwner string) bool {
	var count int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public' AND tableowner != $1`, targetOwner,
	).Scan(&count); err != nil {
		return true
	}
	return count > 0
}

func transferOwnership(db *sql.DB, target string) error {
	if err := execSQL(db, fmt.Sprintf(`DO $$ DECLARE r text; BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname='public' AND tableowner != %s LOOP
    BEGIN EXECUTE format('ALTER TABLE public.%%I OWNER TO %s', r);
    EXCEPTION WHEN insufficient_privilege THEN RAISE NOTICE 'skip table %%', r; END;
  END LOOP;
END $$`, qs(target), qi(target))); err != nil {
		return fmt.Errorf("transfer table ownership to %s: %w", target, err)
	}

	var skippedTables int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_tables
		WHERE schemaname = 'public' AND tableowner != $1`, target,
	).Scan(&skippedTables); err != nil {
		slog.Warn("could not verify table ownership transfer", "target", target, "error", err)
	} else if skippedTables > 0 {
		return fmt.Errorf("ownership transfer incomplete: %d tables still not owned by %s (insufficient_privilege)", skippedTables, target)
	}

	if err := execSQL(db, fmt.Sprintf(`DO $$ DECLARE r text; BEGIN
  FOR r IN SELECT sequencename FROM pg_sequences WHERE schemaname='public' AND sequenceowner != %s LOOP
    BEGIN EXECUTE format('ALTER SEQUENCE public.%%I OWNER TO %s', r);
    EXCEPTION WHEN insufficient_privilege THEN RAISE NOTICE 'skip seq %%', r; END;
  END LOOP;
END $$`, qs(target), qi(target))); err != nil {
		return fmt.Errorf("transfer sequence ownership to %s: %w", target, err)
	}

	var skippedSeqs int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_sequences
		WHERE schemaname = 'public' AND sequenceowner != $1`, target,
	).Scan(&skippedSeqs); err != nil {
		slog.Warn("could not verify sequence ownership transfer", "target", target, "error", err)
	} else if skippedSeqs > 0 {
		return fmt.Errorf("ownership transfer incomplete: %d sequences still not owned by %s (insufficient_privilege)", skippedSeqs, target)
	}

	if err := execSQL(db, fmt.Sprintf(`DO $$ DECLARE r text; BEGIN
  FOR r IN SELECT viewname FROM pg_views WHERE schemaname='public' AND viewowner != %s LOOP
    BEGIN EXECUTE format('ALTER VIEW public.%%I OWNER TO %s', r);
    EXCEPTION WHEN insufficient_privilege THEN RAISE NOTICE 'skip view %%', r; END;
  END LOOP;
END $$`, qs(target), qi(target))); err != nil {
		return fmt.Errorf("transfer view ownership to %s: %w", target, err)
	}

	var skippedViews int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_views
		WHERE schemaname = 'public' AND viewowner != $1`, target,
	).Scan(&skippedViews); err != nil {
		slog.Warn("could not verify view ownership transfer", "target", target, "error", err)
	} else if skippedViews > 0 {
		return fmt.Errorf("ownership transfer incomplete: %d views still not owned by %s (insufficient_privilege)", skippedViews, target)
	}

	return nil
}

func grantRoleMemberships(db *sql.DB, users []string) error {
	for _, u := range users {
		var isMember bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM pg_auth_members m
				JOIN pg_roles gr ON m.roleid = gr.oid
				JOIN pg_roles mr ON m.member = mr.oid
				WHERE gr.rolname = 'postgres' AND mr.rolname = $1
			)`, u).Scan(&isMember); err != nil {
			slog.Warn("could not check role membership, will grant anyway", "user", u, "error", err)
		} else if isMember {
			slog.Info("role membership already exists, skipping", "user", u)
			continue
		}
		slog.Info("granting postgres role membership", "user", u)
		if err := execSQL(db, fmt.Sprintf(`GRANT postgres TO %s`, qi(u))); err != nil {
			if strings.Contains(err.Error(), "already a member") {
				slog.Info("role membership already exists (confirmed by server)", "user", u)
				continue
			}
			return fmt.Errorf("grant postgres to %s: %w", u, err)
		}
	}
	return nil
}

func grantSchemaUsage(db *sql.DB, users []string) error {
	var missing []string
	for _, u := range users {
		var has bool
		if err := db.QueryRow(
			`SELECT has_schema_privilege($1, 'public', 'USAGE')`, u,
		).Scan(&has); err != nil {
			slog.Warn("could not check schema privilege, will grant anyway", "user", u, "error", err)
			missing = append(missing, u)
			continue
		}
		if has {
			slog.Info("schema USAGE already granted, skipping", "user", u)
		} else {
			missing = append(missing, u)
		}
	}
	if len(missing) == 0 {
		slog.Info("schema USAGE already granted to all users, skipping")
		return nil
	}
	slog.Info("granting schema USAGE", "users", missing)
	return execSQL(db, fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, joinQI(missing)))
}

func grantDML(db *sql.DB, users []string) error {
	var missing []string
	for _, u := range users {
		var count int
		if err := db.QueryRow(`
			SELECT count(*) FROM pg_tables t
			WHERE t.schemaname = 'public'
			AND NOT has_table_privilege($1,
				format('%I.%I', t.schemaname, t.tablename), 'SELECT')`, u,
		).Scan(&count); err != nil {
			slog.Warn("could not check DML privileges, will grant anyway", "user", u, "error", err)
			missing = append(missing, u)
			continue
		}
		if count > 0 {
			slog.Info("user missing DML on tables", "user", u, "missing_tables", count)
			missing = append(missing, u)
		} else {
			slog.Info("DML already granted, skipping", "user", u)
		}
	}
	if len(missing) == 0 {
		slog.Info("DML already granted to all users, skipping")
		return nil
	}
	slog.Info("granting DML", "users", missing)
	list := joinQI(missing)
	if err := execSQL(db, fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %s`, list)); err != nil {
		return err
	}
	return execSQL(db, fmt.Sprintf(`GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO %s`, list))
}

func setDefaultPrivileges(db *sql.DB, ownerRole string, users []string) error {
	var defAclCount int
	if err := db.QueryRow(`
		SELECT count(*) FROM pg_default_acl da
		JOIN pg_namespace n ON da.defaclnamespace = n.oid
		JOIN pg_roles r ON da.defaclrole = r.oid
		WHERE n.nspname = 'public' AND r.rolname = $1`, ownerRole,
	).Scan(&defAclCount); err != nil {
		slog.Warn("could not check default privileges, will set anyway", "owner", ownerRole, "error", err)
	} else if defAclCount >= 2 {
		slog.Info("default privileges already configured, skipping", "owner", ownerRole, "acl_entries", defAclCount)
		return nil
	} else {
		slog.Info("default privileges incomplete, setting", "owner", ownerRole, "existing_entries", defAclCount)
	}
	list := joinQI(users)
	if err := execSQL(db, fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s`, qi(ownerRole), list)); err != nil {
		return err
	}
	return execSQL(db, fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO %s`, qi(ownerRole), list))
}

package main

import (
	"database/sql"
	"os"
	"strings"
)

func qi(id string) string {
	escaped := strings.ReplaceAll(id, `"`, `""`)
	return `"` + escaped + `"`
}

func qs(val string) string {
	escaped := strings.ReplaceAll(val, `'`, `''`)
	return `'` + escaped + `'`
}

func joinQI(ids []string) string {
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = qi(id)
	}
	return strings.Join(q, ", ")
}

// allIAMUsers returns IAM DB users that this job should ensure exist in Cloud SQL.
// Empty entries are filtered out so callers in scoped-mode (e.g. Temporal-only or
// VCP-only) don't try to create users they don't manage.
func allIAMUsers(cfg config) []string {
	candidates := []string{cfg.iamVcpCore, cfg.iamVcpWorker, cfg.iamClhSA, cfg.iamTemporal, cfg.iamMetricsProducer}
	out := make([]string, 0, len(candidates))
	for _, u := range candidates {
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// roleMembershipUsers returns IAM users to grant `postgres` role membership.
// Empty entries are filtered so split-mode jobs (where out-of-scope IAM
// fields are unset) don't issue `GRANT postgres TO ""`, which Postgres
// rejects as a zero-length delimited identifier.
func roleMembershipUsers(cfg config) []string {
	return nonEmpty(cfg.iamVcpCore, cfg.iamVcpWorker, cfg.iamClhSA, cfg.iamTemporal)
}

// vcpGrantUsers / metricsGrantUsers / temporalGrantUsers are also filtered
// for empty entries so a missing IAM env var in one scope doesn't leak an
// empty identifier into GRANT/REVOKE statements at runtime.
func vcpGrantUsers(cfg config) []string {
	return nonEmpty(cfg.iamVcpCore, cfg.iamVcpWorker, cfg.iamClhSA, cfg.iamMetricsProducer, "postgres")
}

func metricsGrantUsers(cfg config) []string {
	return nonEmpty(cfg.iamVcpCore, cfg.iamVcpWorker, cfg.iamClhSA, cfg.iamMetricsProducer, "postgres", "metrics")
}

func temporalGrantUsers(cfg config) []string {
	return nonEmpty(cfg.iamTemporal, "postgres")
}

func nonEmpty(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// isRoleMember reports whether `member` has been granted `role` (GRANT role TO member).
func isRoleMember(db *sql.DB, member, role string) (bool, error) {
	var isMember bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM pg_auth_members m
			JOIN pg_roles gr ON m.roleid = gr.oid
			JOIN pg_roles mr ON m.member = mr.oid
			WHERE gr.rolname = $1 AND mr.rolname = $2
		)`, role, member).Scan(&isMember)
	return isMember, err
}

func isRoleGrantError(err error, substring string) bool {
	return err != nil && strings.Contains(err.Error(), substring)
}

func excludeUser(users []string, exclude string) []string {
	var out []string
	for _, u := range users {
		if u != exclude {
			out = append(out, u)
		}
	}
	return out
}

func iamPort(cfg config, iamUser string) string {
	if cfg.temporalDBPort != "" && iamUser == cfg.iamTemporal {
		return cfg.temporalDBPort
	}
	return cfg.dbPort
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "true" || v == "1"
}

// envBoolDefault returns the boolean value of an env var, falling back to
// `defaultVal` when the env var is unset or empty. Use this for flags whose
// default behaviour should be `true` (existing deployments should not break
// when they don't set the var explicitly).
func envBoolDefault(key string, defaultVal bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	return v == "true" || v == "1"
}

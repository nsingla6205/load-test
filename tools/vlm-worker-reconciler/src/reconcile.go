package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// buildDSN constructs the pq connection URL for the given config.
//
// Uses net/url so all field values are properly percent-encoded, preventing
// DSN parameter injection via values that contain spaces or pq keywords.
// Empty password is omitted from the URL userinfo rather than passed as an
// empty field, avoiding a pq parser bug where "password= sslmode=disable"
// is read as password="sslmode=disable" leaving sslmode unset (defaults to
// "require"), which causes every connection to fail with an SSLRequest.
func buildDSN(cfg config) string {
	sslMode := cfg.dbSSLMode
	if cfg.cloudSqlIAMAuthEnabled {
		// Cloud SQL Proxy handles encryption on the backend; the local leg must
		// be plain TCP so pq doesn't send an SSLRequest the proxy would reject.
		sslMode = "disable"
	}
	var userInfo *url.Userinfo
	if cfg.dbPassword != "" {
		userInfo = url.UserPassword(cfg.dbUser, cfg.dbPassword)
	} else {
		userInfo = url.User(cfg.dbUser)
	}
	q := url.Values{}
	q.Set("sslmode", sslMode)
	q.Set("connect_timeout", "10")
	u := url.URL{
		Scheme:   "postgres",
		User:     userInfo,
		Host:     cfg.dbHost + ":" + cfg.dbPort,
		Path:     "/" + cfg.dbName,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func openDB(cfg config) (*sql.DB, error) {
	return sql.Open("postgres", buildDSN(cfg))
}

func queryActiveVersions(ctx context.Context, db *sql.DB) ([]string, error) {
	qctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := db.QueryContext(qctx, `
		SELECT DISTINCT build_info->>'ontapVersion'
		FROM pools
		WHERE state <> 'DELETED'
		  AND build_info->>'ontapVersion' IS NOT NULL
		  AND build_info->>'ontapVersion' != ''`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v != "" {
			versions = append(versions, v)
		}
	}
	return versions, rows.Err()
}

// runWith is the fully injectable reconciliation core; all infrastructure is passed in.
// Returns an exit code (always 0 — the job exits gracefully even on partial failures)
// and a jobOutcome that callers use for metrics.
func runWith(ctx context.Context, cfg config, k8s k8sClientInterface, db *sql.DB, log *slog.Logger) (int, jobOutcome) {
	// ── Query active ONTAP versions ────────────────────────────────────────────

	log.Info("querying active ONTAP versions from database")
	dbStart := time.Now()
	rawVersions, err := queryActiveVersions(ctx, db)
	if err != nil {
		log.Error("database query failed — skipping reconciliation",
			"error", err, "ms", time.Since(dbStart).Milliseconds())
		return 0, outcomeSkipped
	}
	log.Info("DB query complete", "ms", time.Since(dbStart).Milliseconds())

	activeVersions := normalizeVersions(rawVersions)
	vStrs := make([]string, len(activeVersions))
	for i, v := range activeVersions {
		vStrs[i] = v.String()
	}
	log.Info("active versions", "count", len(activeVersions), "versions", strings.Join(vStrs, ", "))

	// G4: Never act on an empty active set.
	if len(activeVersions) == 0 {
		log.Warn("G4: active version set is empty — skipping all scale-down to prevent mass outage")
		return 0, outcomeSkipped
	}

	// ── List vlm-worker Deployments ────────────────────────────────────────────

	log.Info("listing vlm-worker Deployments", "namespace", cfg.namespace)
	deployments, err := k8s.listVLMWorkerDeployments(ctx)
	if err != nil {
		log.Error("failed to list Deployments — skipping reconciliation", "error", err)
		return 0, outcomeSkipped
	}
	if len(deployments) == 0 {
		log.Warn("no vlm-worker Deployments found — nothing to reconcile")
		return 0, outcomeSuccess
	}
	log.Info("found Deployments", "count", len(deployments))

	// ── Classify each Deployment ───────────────────────────────────────────────

	var keepActive, scaleToZero []deploymentItem

	for _, d := range deployments {
		depVer, ok := deployNameToVersion(d.Name)
		if !ok {
			log.Warn("cannot parse version from deployment name — keeping active (safe default)",
				"deployment", d.Name)
			keepActive = append(keepActive, d)
			continue
		}
		keep, reason := shouldKeep(depVer, activeVersions)
		if keep {
			log.Info("KEEP", "deployment", d.Name, "version", depVer.String(), "reason", reason)
			keepActive = append(keepActive, d)
		} else {
			log.Info("SCALE-TO-0", "deployment", d.Name, "version", depVer.String(),
				"reason", "no active version requires this worker")
			scaleToZero = append(scaleToZero, d)
		}
	}

	// G5: Never scale every worker to 0.
	if len(keepActive) == 0 {
		log.Error("G5: hierarchy logic would scale ALL workers to 0 — aborting to prevent full outage")
		return 0, outcomeSkipped
	}

	// ── Apply scale actions ────────────────────────────────────────────────────

	log.Info("result", "keep", len(keepActive), "scale_to_zero", len(scaleToZero))

	if len(scaleToZero) == 0 {
		log.Info("nothing to scale down — all workers are active")
	}

	var scaleFailed int
	for _, d := range scaleToZero {
		if d.Replicas == 0 {
			log.Info("SKIP — already at 0 replicas", "deployment", d.Name)
			continue
		}
		if cfg.dryRun {
			log.Info("DRY-RUN: would scale to 0", "deployment", d.Name, "current_replicas", d.Replicas)
			continue
		}
		log.Info("scaling to 0", "deployment", d.Name, "current_replicas", d.Replicas)
		if err := k8s.scaleDeployment(ctx, d.Name, 0); err != nil {
			log.Error("failed to scale deployment — continuing", "deployment", d.Name, "error", err)
			scaleFailed++
			continue
		}
		log.Info("scaled to 0", "deployment", d.Name)
	}

	if scaleFailed > 0 {
		log.Warn("reconciler complete with partial failures", "scale_failures", scaleFailed)
		return 0, outcomePartialFailure
	}
	log.Info("reconciler complete")
	return 0, outcomeSuccess
}

// waitForCloudSQLProxy polls the proxy's DB port (5432) via a plain TCP dial
// until it accepts connections or the retry budget is exhausted.
//
// cloud-sql-proxy v2 binds the local port only after the Cloud SQL tunnel is
// fully established, so a successful TCP dial means the proxy is ready for
// real DB traffic. This approach requires no extra proxy flags (no
// --health-check) and matches the pattern used by the vcp-dbmigrate Job.
func waitForCloudSQLProxy(ctx context.Context, port string, maxRetries int, log *slog.Logger) error {
	addr := "127.0.0.1:" + port
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if i == maxRetries-1 {
			return fmt.Errorf("proxy port %s not ready after %d retries: %w", port, maxRetries, err)
		}
		log.Info("waiting for cloud-sql-proxy tunnel", "attempt", i+1, "of", maxRetries)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("proxy port %s not ready: max retries is zero", port)
}

// cloudSQLProxyAdminPort is the admin port passed to cloud-sql-proxy via
// --admin-port in the Helm chart. Both must match.
const cloudSQLProxyAdminPort = "9091"

// shutdownCloudSQLProxy signals the cloud-sql-proxy sidecar to exit via its
// --quitquitquit admin endpoint. Called at the end of every run() path when
// IAM auth is enabled so the Job pod completes instead of staying 1/2 NotReady.
func shutdownCloudSQLProxy(log *slog.Logger) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://127.0.0.1:"+cloudSQLProxyAdminPort+"/quitquitquit", "", nil)
	if err != nil {
		log.Warn("could not reach cloud-sql-proxy admin endpoint", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		log.Warn("cloud-sql-proxy shutdown returned non-200", "status", resp.Status)
		return
	}
	log.Info("cloud-sql-proxy shutdown signal sent")
}

// run builds real infrastructure from cfg and delegates to runWith.
func run(ctx context.Context, cfg config, log *slog.Logger) int {
	log.Info("VLM Worker Reconciler", "namespace", cfg.namespace, "dry_run", cfg.dryRun)

	if cfg.cloudSqlIAMAuthEnabled {
		defer shutdownCloudSQLProxy(log)
	}

	m := setupMetrics(ctx, cfg.gcpProjectID, log)
	start := time.Now()

	// For IAM auth, only DB_USER is required; the proxy handles authentication.
	if cfg.dbUser == "" || (!cfg.cloudSqlIAMAuthEnabled && cfg.dbPassword == "") {
		log.Warn("DB credentials missing or secret not synced — skipping reconciliation")
		m.emitAndFlush(ctx, outcomeSkipped, time.Since(start), log)
		return 0
	}

	db, err := openDB(cfg)
	if err != nil {
		log.Error("failed to open DB — skipping reconciliation", "error", err)
		m.emitAndFlush(ctx, outcomeSkipped, time.Since(start), log)
		return 0
	}
	defer func() { _ = db.Close() }()

	// When IAM auth is enabled the cloud-sql-proxy sidecar starts concurrently
	// with the reconciler. Poll the proxy's DB port via a plain TCP dial to
	// confirm the tunnel is ready before attempting any DB operation.
	// cloud-sql-proxy v2 binds the port only after the Cloud SQL tunnel is fully
	// established, so a successful dial is a reliable readiness signal — no extra
	// proxy flags needed. This matches the pattern used by the vcp-dbmigrate Job.
	//
	// Retry budget: a quarter of activeDeadlineSeconds (each retry sleeps 2s) is
	// reserved for proxy readiness; the remaining three-quarters are left for
	// actual reconciliation + proxy shutdown.
	if cfg.cloudSqlIAMAuthEnabled {
		maxRetries := (cfg.activeDeadlineSeconds / 2) / 2
		if maxRetries < 1 {
			maxRetries = 1
		}
		if err := waitForCloudSQLProxy(ctx, cfg.dbPort, maxRetries, log); err != nil {
			log.Error("cloud-sql-proxy not ready after retries — skipping reconciliation", "retries", maxRetries, "error", err)
			m.emitAndFlush(ctx, outcomeSkipped, time.Since(start), log)
			return 0
		}
	}

	k8s, err := newK8sClient(cfg.namespace)
	if err != nil {
		log.Error("failed to init K8s client — skipping reconciliation", "error", err)
		m.emitAndFlush(ctx, outcomeSkipped, time.Since(start), log)
		return 0
	}

	code, outcome := runWith(ctx, cfg, k8s, db, log)
	m.emitAndFlush(ctx, outcome, time.Since(start), log)
	return code
}

package main

import (
	"context"
	"log/slog"
	"os"
	"time"
)

type config struct {
	iamAuthEnabled bool

	dbHost    string
	dbPort    string
	dbSSLMode string

	adminUser string
	adminPass string

	vcpDBName         string
	metricsDBName     string
	temporalDBName    string
	temporalVisDBName string

	iamVcpCore         string
	iamVcpWorker       string
	iamClhSA           string
	iamTemporal        string
	iamMetricsProducer string

	// vcpEnabled controls whether the job manages the VCP databases (vcp
	// and metrics). The metrics DB is always managed alongside VCP — if
	// it doesn't exist on the server, the per-DB existence check skips
	// it cleanly. When false, the job runs in scoped mode (e.g. Temporal-
	// only) and skips all VCP work. Defaults to true for backwards
	// compatibility with single-job deployments that don't set
	// VCP_ENABLED explicitly.
	vcpEnabled      bool
	temporalEnabled bool

	// Port for the second Cloud SQL Proxy that impersonates temporal-ksa.
	// Falls back to dbPort when not set (single-proxy mode).
	temporalDBPort string

	// Cloud SQL instance connection name: "project:region:instance"
	instanceConnName string
}

func loadConfig() config {
	return config{
		iamAuthEnabled: envBool("CLOUD_SQL_IAM_AUTH_ENABLED"),

		dbHost:    envOr("DB_HOST", "127.0.0.1"),
		dbPort:    envOr("DB_PORT", "5432"),
		dbSSLMode: envOr("DB_SSL_MODE", "disable"),

		adminUser: envOr("DB_ADMIN_USER", "postgres"),
		adminPass: os.Getenv("DB_ADMIN_PASSWORD"),

		vcpDBName:         envOr("DB_NAME", "vcp"),
		metricsDBName:     envOr("METRICS_DB_NAME", "metrics"),
		temporalDBName:    envOr("TEMPORAL_DB_NAME", "temporal"),
		temporalVisDBName: envOr("TEMPORAL_VISIBILITY_DB_NAME", "temporal_visibility"),

		iamVcpCore:         os.Getenv("IAM_VCP_CORE"),
		iamVcpWorker:       os.Getenv("IAM_VCP_WORKER"),
		iamClhSA:           os.Getenv("IAM_CLH_SA"),
		iamTemporal:        os.Getenv("IAM_TEMPORAL"),
		iamMetricsProducer: os.Getenv("IAM_METRICS_PRODUCER"),

		vcpEnabled:      envBoolDefault("VCP_ENABLED", true),
		temporalEnabled: envBool("TEMPORAL_ENABLED"),

		temporalDBPort: envOr("TEMPORAL_DB_PORT", ""),

		instanceConnName: os.Getenv("INSTANCE_CONNECTION_NAME"),
	}
}

func main() {
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	go func() {
		<-ctx.Done()
		if ctx.Err() == context.DeadlineExceeded {
			slog.Error("job timeout exceeded - may need to increase deadline")
		}
	}()

	cfg := loadConfig()
	defer shutdownProxy()
	defer cleanupAdminSecret()

	slog.Info("starting IAM lifecycle job", "iam_auth_enabled", cfg.iamAuthEnabled,
		"vcp_enabled", cfg.vcpEnabled, "temporal_enabled", cfg.temporalEnabled)

	// Early scope check: at least one domain must be enabled
	if !cfg.vcpEnabled && !cfg.temporalEnabled {
		slog.Error("no databases in scope: both VCP_ENABLED and TEMPORAL_ENABLED are false")
		return 1
	}

	if !cfg.iamAuthEnabled {
		return runDisablePath(cfg, enableAll, rollbackAll)
	}

	missing := missingIAMUsers(cfg, false)
	if len(missing) > 0 {
		slog.Error("IAM auth enabled but IAM user env vars not set", "missing", missing)
		return 1
	}

	if err := validateIAMPermissions(ctx, cfg); err != nil {
		slog.Error("IAM permission validation failed", "error", err)
		return 1
	}

	if err := ensureIAMDBUsers(cfg); err != nil {
		slog.Error("failed to ensure IAM DB users", "error", err)
		return 1
	}

	return runEnablePath(cfg, enableAll, rollbackAll)
}

type dbLifecycleFunc func(config) error

func runEnablePath(cfg config, enableFn, rollbackFn dbLifecycleFunc) int {
	slog.Info("IAM auth enabled — checking if enable needed")
	if err := enableFn(cfg); err != nil {
		slog.Error("enable failed, attempting rollback to restore postgres ownership", "error", err)
		if rbErr := rollbackFn(cfg); rbErr != nil {
			slog.Error("CRITICAL: automatic rollback also failed — manual intervention required",
				"enable_error", err,
				"rollback_error", rbErr)
		} else {
			slog.Info("automatic rollback succeeded — all databases restored to postgres ownership")
		}
		return 1
	}
	slog.Info("IAM lifecycle job completed successfully")
	return 0
}

func runDisablePath(cfg config, enableFn, rollbackFn dbLifecycleFunc) int {
	slog.Info("IAM auth disabled — checking if rollback needed")
	if err := rollbackFn(cfg); err != nil {
		missing := missingIAMUsers(cfg, false)
		if len(missing) > 0 {
			slog.Error("CRITICAL: rollback failed and re-enable skipped because required IAM env vars are not set — manual intervention required",
				"rollback_error", err,
				"missing", missing)
			return 1
		}
		slog.Error("rollback failed, attempting re-enable to restore IAM ownership", "error", err)
		// Ensure IAM DB users exist before re-enable (mirrors normal enable path)
		if ensureErr := ensureIAMDBUsers(cfg); ensureErr != nil {
			slog.Error("CRITICAL: re-enable skipped because IAM DB user creation failed — manual intervention required",
				"rollback_error", err,
				"ensure_users_error", ensureErr)
			return 1
		}
		if reErr := enableFn(cfg); reErr != nil {
			slog.Error("CRITICAL: re-enable also failed — manual intervention required",
				"rollback_error", err,
				"reenable_error", reErr)
		} else {
			slog.Info("re-enable succeeded — all databases restored to IAM ownership")
		}
		return 1
	}
	return 0
}

// missingIAMUsers returns IAM env-var names that are required for the
// current scope (vcpEnabled / temporalEnabled) but not set. The metrics
// DB is always managed when vcpEnabled is true, so IAM_METRICS_PRODUCER
// is required during enable. On rollback, IAM_METRICS_PRODUCER is not
// strictly required because rollback only strips grants and transfers
// ownership back to postgres.
func missingIAMUsers(cfg config, isRollback bool) []string {
	var required []struct{ k, v string }
	if cfg.vcpEnabled {
		required = append(required,
			struct{ k, v string }{"IAM_VCP_CORE", cfg.iamVcpCore},
			struct{ k, v string }{"IAM_VCP_WORKER", cfg.iamVcpWorker},
			struct{ k, v string }{"IAM_CLH_SA", cfg.iamClhSA},
		)
		if !isRollback {
			required = append(required, struct{ k, v string }{"IAM_METRICS_PRODUCER", cfg.iamMetricsProducer})
		}
	}
	if cfg.temporalEnabled {
		required = append(required, struct{ k, v string }{"IAM_TEMPORAL", cfg.iamTemporal})
	}

	var out []string
	for _, kv := range required {
		if kv.v == "" {
			out = append(out, kv.k)
		}
	}
	return out
}

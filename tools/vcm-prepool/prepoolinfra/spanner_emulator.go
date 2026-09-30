package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// SpannerDBConfig identifies the database created in the local Spanner emulator.
type SpannerDBConfig struct {
	Project  string `json:"project"`
	Instance string `json:"instance"`
	Database string `json:"database"`
}

// SpannerEmulatorEnabled reports whether this process is configured to use the
// local Spanner emulator.
func SpannerEmulatorEnabled() bool {
	return strings.TrimSpace(env.GetString("SPANNER_EMULATOR_HOST", "")) != ""
}

// ensureSpannerEmulatorDatabase creates the configured instance and database
// when SPANNER_EMULATOR_HOST is set. It never provisions production Spanner.
func ensureSpannerEmulatorDatabase(ctx context.Context, dbConfig *SpannerDBConfig) error {
	if !SpannerEmulatorEnabled() {
		slog.Info("Spanner emulator is not configured; skipping database creation")
		return nil
	}
	if dbConfig == nil {
		return fmt.Errorf("dbConfig is required when SPANNER_EMULATOR_HOST is set")
	}

	store, err := vcmdb.Open(ctx, vcmdb.Config{
		Driver: vcmdb.DriverSpanner,
		Spanner: vcmdb.SpannerConfig{
			Project:         dbConfig.Project,
			Instance:        dbConfig.Instance,
			Database:        dbConfig.Database,
			CreateIfMissing: true,
		},
	}, log.NewLogger(), false)
	if err != nil {
		return fmt.Errorf("create Spanner emulator instance/database: %w", err)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("close Spanner emulator bootstrap connection: %w", err)
	}

	slog.Info("Spanner emulator database ready",
		"project", dbConfig.Project,
		"instance", dbConfig.Instance,
		"database", dbConfig.Database)
	return nil
}

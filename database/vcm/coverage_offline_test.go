package vcm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// TestOpenGorm_ConnectError covers the openGorm failure path: a refused Postgres
// DSN plus a cancelled context short-circuits the retry loop and returns an error.
func TestOpenGorm_ConnectError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Open(ctx, Config{
		Driver:   DriverPostgres,
		Postgres: SQLConfig{DSN: "host=127.0.0.1 port=1 user=x dbname=y sslmode=disable"},
	}, log.NewLogger(), false)
	assert.Error(t, err)
}

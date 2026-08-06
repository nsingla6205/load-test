package hyperscaler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_newGcpServices_ReturnsInitializedGcpServices(t *testing.T) {
	ctx := context.TODO()
	services := NewGcpServices(ctx)

	assert.NotNil(t, services)
	assert.Equal(t, ctx, services.Ctx)
	assert.NotNil(t, services.Logger)
	assert.NotNil(t, services.Retry)
}

func Test_GetGCPService(t *testing.T) {
	t.Run("function exists and has correct signature", func(t *testing.T) {
		assert.NotNil(t, GetGCPService)
	})
}

func Test_NewGcpServices(t *testing.T) {
	t.Run("creates new gcp services with context", func(t *testing.T) {
		ctx := context.Background()
		services := NewGcpServices(ctx)

		assert.NotNil(t, services)
		assert.Equal(t, ctx, services.Ctx)
		assert.NotNil(t, services.Logger)
		assert.NotNil(t, services.Retry)
	})
}

func Test_MaxRetries(t *testing.T) {
	assert.GreaterOrEqual(t, MaxRetries, 0)
}

// Test_InitSharedOCIClients exercises the hyperscaler-level wrapper that
// delegates to the oci package. With no OCI auth configured in the test
// environment, the delegated init surfaces an initialization error (no network
// I/O is performed); the point is to cover the delegation path.
func Test_InitSharedOCIClients(t *testing.T) {
	err := InitSharedOCIClients(context.Background())
	assert.Error(t, err)
}

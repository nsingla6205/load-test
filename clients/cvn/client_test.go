package cvn

import (
	"context"
	"errors"
	"testing"

	"github.com/greenqloud/cloud-volumes-network/app/pkg/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
)

func TestNewFromContext(t *testing.T) {
	t.Cleanup(func() {
		cvnHost = ""
		aak = ""
		ask = ""
		cvnNew = sdk.New
	})

	t.Run("returns error when CVN_HOST is missing", func(t *testing.T) {
		cvnHost = ""
		aak = "key"
		ask = "secret"

		_, err := NewFromContext(context.Background(), "account")
		require.Error(t, err)

		var appErr *temporal.ApplicationError
		require.True(t, errors.As(err, &appErr))
		assert.True(t, appErr.NonRetryable())

		var trackingID int
		var detail string
		require.NoError(t, appErr.Details(&trackingID, &detail))
		assert.Equal(t, vsaerrors.ErrCVNHostNotFound, trackingID)
		assert.Equal(t, "CVN_HOST is not configured", detail)
	})

	t.Run("returns error when admin credentials are missing", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = ""
		ask = ""

		_, err := NewFromContext(context.Background(), "account")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ADMIN_API_KEY")
	})

	t.Run("builds client with vcp platform and correlation id", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = "key"
		ask = "secret"

		var captured *sdk.Config
		cvnNew = func(cfg *sdk.Config) sdk.CVN {
			captured = cfg
			return sdk.New(cfg)
		}

		ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-123")
		client, err := NewFromContext(ctx, "account-456")
		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, captured)
		assert.Equal(t, "cvn.local", captured.Host)
		assert.Equal(t, "key", captured.AAK)
		assert.Equal(t, "secret", captured.ASK)
		assert.Equal(t, "corr-123", captured.CorrelationID)
		assert.Equal(t, "account-456", captured.AccountID)
		assert.Equal(t, platformVCP, captured.Platform)
	})

	t.Run("builds client with correlation id from temporal propagated context", func(t *testing.T) {
		cvnHost = "cvn.local"
		aak = "key"
		ask = "secret"

		var captured *sdk.Config
		cvnNew = func(cfg *sdk.Config) sdk.CVN {
			captured = cfg
			return sdk.New(cfg)
		}

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{
			string(middleware.RequestCorrelationID): "corr-from-temporal",
		})
		client, err := NewFromContext(ctx, "account-456")
		require.NoError(t, err)
		require.NotNil(t, client)
		require.NotNil(t, captured)
		assert.Equal(t, "corr-from-temporal", captured.CorrelationID)
	})
}

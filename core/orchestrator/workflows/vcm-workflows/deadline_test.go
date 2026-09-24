package vcm_workflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// deadlineTestBackend is a minimal Backend that only supplies Now for deadline checks.
type deadlineTestBackend struct {
	now time.Time
	orchestrator.Backend
}

func (b *deadlineTestBackend) Now(*orchestrator.Context) time.Time { return b.now }

type nopDeadlineLogger struct{}

func (nopDeadlineLogger) WithFields(orchestrator.Fields) orchestrator.UnifiedLogger { return nopDeadlineLogger{} }
func (nopDeadlineLogger) With(orchestrator.Fields) orchestrator.UnifiedLogger       { return nopDeadlineLogger{} }
func (nopDeadlineLogger) WithError(error) orchestrator.UnifiedLogger                { return nopDeadlineLogger{} }
func (nopDeadlineLogger) WithTime(time.Time) orchestrator.UnifiedLogger             { return nopDeadlineLogger{} }
func (nopDeadlineLogger) Info(string, ...any)                                      {}
func (nopDeadlineLogger) Error(string, ...any)                                     {}
func (nopDeadlineLogger) Debug(string, ...any)                                     {}
func (nopDeadlineLogger) Warn(string, ...any)                                      {}
func (nopDeadlineLogger) Printf(string, ...any)                                    {}
func (nopDeadlineLogger) Print(...any)                                             {}
func (nopDeadlineLogger) Println(...any)                                           {}
func (nopDeadlineLogger) Fatal(...any)                                             {}
func (nopDeadlineLogger) Fatalf(string, ...any)                                    {}
func (nopDeadlineLogger) Fatalln(...any)                                           {}
func (nopDeadlineLogger) Panic(...any)                                             {}
func (nopDeadlineLogger) Panicf(string, ...any)                                    {}
func (nopDeadlineLogger) Panicln(...any)                                           {}
func (nopDeadlineLogger) Infof(string, ...any)                                     {}
func (nopDeadlineLogger) Errorf(string, ...any)                                    {}
func (nopDeadlineLogger) Debugf(string, ...any)                                    {}
func (nopDeadlineLogger) Warnf(string, ...any)                                     {}
func (nopDeadlineLogger) Trace(...any)                                             {}
func (nopDeadlineLogger) Tracef(string, ...any)                                    {}
func (nopDeadlineLogger) Traceln(...any)                                           {}
func (nopDeadlineLogger) Warnln(...any)                                            {}
func (nopDeadlineLogger) Errorln(...any)                                           {}
func (nopDeadlineLogger) Debugln(...any)                                           {}
func (nopDeadlineLogger) Infoln(...any)                                            {}

type deadlineTestBackendCtx struct{}

func withDeadlineTestLogger(t *testing.T) {
	t.Helper()
	orchestrator.ChainWorkflowLoggerFactory(func(ctx interface{}) (orchestrator.UnifiedLogger, error) {
		if _, ok := ctx.(deadlineTestBackendCtx); ok {
			return nopDeadlineLogger{}, nil
		}
		return nil, errors.New("not a deadline test context")
	})
}

func deadlineTestContext(now time.Time) *orchestrator.Context {
	return orchestrator.NewContextWithBackend(&deadlineTestBackend{now: now}, deadlineTestBackendCtx{})
}

func TestCheckDeadline(t *testing.T) {
	withDeadlineTestLogger(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	ctx := deadlineTestContext(now)

	t.Run("zero deadline is never enforced", func(t *testing.T) {
		assert.NoError(t, checkDeadline(ctx, time.Time{}, "start"))
	})

	t.Run("future deadline passes", func(t *testing.T) {
		assert.NoError(t, checkDeadline(ctx, now.Add(time.Minute), "start"))
	})

	t.Run("elapsed deadline fails with ErrOperationDeadlineExceeded", func(t *testing.T) {
		err := checkDeadline(ctx, now.Add(-time.Minute), "create-volume")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrOperationDeadlineExceeded, customErr.TrackingID)
		require.NotNil(t, customErr.OriginalErr)
		assert.Contains(t, customErr.OriginalErr.Error(), "create-volume")
	})

	t.Run("deadline exactly now fails", func(t *testing.T) {
		err := checkDeadline(ctx, now, "get-svm")
		require.Error(t, err)
		var customErr *vsaerrors.CustomError
		require.ErrorAs(t, err, &customErr)
		assert.Equal(t, vsaerrors.ErrOperationDeadlineExceeded, customErr.TrackingID)
	})
}

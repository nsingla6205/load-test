package workflows

import (
	"testing"
	"time"
)

// withFastWorkflowRetry shortens ExecuteWorkflowWithRetry backoff for this test only.
func withFastWorkflowRetry(t *testing.T) {
	t.Helper()
	orig := TemporalWorkflowRetryDelay
	TemporalWorkflowRetryDelay = time.Millisecond
	t.Cleanup(func() { TemporalWorkflowRetryDelay = orig })
}

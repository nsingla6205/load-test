package gcp

import (
	"testing"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
)

// withFastWorkflowRetry shortens ExecuteWorkflowWithRetry backoff for this test only.
func withFastWorkflowRetry(t *testing.T) {
	t.Helper()
	orig := workflows.TemporalWorkflowRetryDelay
	workflows.TemporalWorkflowRetryDelay = time.Millisecond
	t.Cleanup(func() { workflows.TemporalWorkflowRetryDelay = orig })
}

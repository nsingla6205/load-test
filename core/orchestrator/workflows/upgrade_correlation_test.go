package workflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestResolveUpgradeWorkflowCorrelationID(t *testing.T) {
	tests := []struct {
		name           string
		explicitID     string
		fallbackID     string
		loggerCorrID   string
		setupLogger    bool
		expected       string
	}{
		{
			name:       "PrefersExplicitID",
			explicitID: "explicit-corr",
			fallbackID: "job-uuid",
			setupLogger: true,
			loggerCorrID: "logger-corr",
			expected:   "explicit-corr",
		},
		{
			name:       "UsesLoggerFieldWhenExplicitEmpty",
			explicitID: "",
			fallbackID: "job-uuid",
			setupLogger: true,
			loggerCorrID: "logger-corr",
			expected:   "logger-corr",
		},
		{
			name:       "FallsBackToJobID",
			explicitID: "",
			fallbackID: "job-uuid",
			setupLogger: false,
			expected:   "job-uuid",
		},
		{
			name:       "EmptyWhenNoSources",
			explicitID: "",
			fallbackID: "",
			setupLogger: false,
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			if tt.setupLogger {
				env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
				encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{
					string(middleware.RequestCorrelationID): tt.loggerCorrID,
				})
				env.SetHeader(&commonpb.Header{
					Fields: map[string]*commonpb.Payload{
						"logParam": encodedValue,
					},
				})
			}

			env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
				return resolveUpgradeWorkflowCorrelationID(ctx, tt.explicitID, tt.fallbackID), nil
			})

			assert.True(t, env.IsWorkflowCompleted())
			var result string
			err := env.GetWorkflowResult(&result)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

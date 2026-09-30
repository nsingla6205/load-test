package gowfbackend

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestGowfWorkflowLoggerFactory_WrongType(t *testing.T) {
	logger, err := GowfWorkflowLoggerFactory("not-a-gowf-context")
	assert.Nil(t, logger)
	assert.Error(t, err)
}

func newTestGowfLogger() *GowfWorkflowLogger {
	return &GowfWorkflowLogger{logger: log.NewLogger(), fields: orchestrator.Fields{}}
}

func TestGowfWorkflowLogger_WithErrorAttachesError(t *testing.T) {
	logger := newTestGowfLogger().WithError(errors.New("disk full")).(*GowfWorkflowLogger)
	assert.Equal(t, "disk full", logger.fields[orchestrator.ErrorField])
}

func TestGowfWorkflowLogger_WithErrorNilIsNoOp(t *testing.T) {
	logger := newTestGowfLogger().WithError(nil).(*GowfWorkflowLogger)
	_, ok := logger.fields[orchestrator.ErrorField]
	assert.False(t, ok)
}

func TestGowfWorkflowLogger_WithTimeAttachesTime(t *testing.T) {
	ts := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	logger := newTestGowfLogger().WithTime(ts).(*GowfWorkflowLogger)
	assert.Equal(t, ts, logger.fields[orchestrator.TimeField])
}

// Request-scoped fields must nest under "requestFields" so a Cloud Logging
// query written against the Temporal-backed VCP services also matches VCM.
func TestPartitionFields_NestsRequestFieldsAndKeepsErrorTopLevel(t *testing.T) {
	requestFields, topLevel := partitionFields(orchestrator.Fields{
		orchestrator.RequestCorrelationID: "corr-1",
		orchestrator.RequestDeploymentID:  "deploy-1",
		"jobID":                           "job-1",
		orchestrator.ErrorField:           "disk full",
		orchestrator.TimeField:            "2026-06-19T10:00:00Z",
	})

	assert.Equal(t, log.Fields{
		orchestrator.RequestCorrelationID: "corr-1",
		orchestrator.RequestDeploymentID:  "deploy-1",
		"jobID":                           "job-1",
	}, requestFields)
	assert.Equal(t, log.Fields{
		orchestrator.ErrorField: "disk full",
		orchestrator.TimeField:  "2026-06-19T10:00:00Z",
	}, topLevel)
}

func TestGowfWorkflowLogger_BoundWithoutFieldsReusesBaseLogger(t *testing.T) {
	logger := newTestGowfLogger()
	assert.Equal(t, logger.logger, logger.bound())
}

func TestGowfWorkflowLogger_BoundGroupsRequestFieldsAndKeepsErrorTopLevel(t *testing.T) {
	base := log.NewMockLogger(t)
	withRequest := log.NewMockLogger(t)
	withTop := log.NewMockLogger(t)
	base.EXPECT().WithFields("requestFields", log.Fields{
		orchestrator.RequestCorrelationID: "corr-1",
	}).Return(withRequest)
	withRequest.EXPECT().With(log.Fields{
		orchestrator.ErrorField: "disk full",
	}).Return(withTop)

	logger := &GowfWorkflowLogger{
		logger: base,
		fields: orchestrator.Fields{
			orchestrator.RequestCorrelationID: "corr-1",
			orchestrator.ErrorField:           "disk full",
		},
	}
	assert.Same(t, withTop, logger.bound())
}

func TestGowfWorkflowLogger_LevelMethodsDelegateToBoundLogger(t *testing.T) {
	base := log.NewMockLogger(t)
	logger := &GowfWorkflowLogger{logger: base}

	base.EXPECT().Info("info").Return()
	base.EXPECT().Error("error").Return()
	base.EXPECT().Debug("debug").Return()
	base.EXPECT().Warn("warn").Return()
	base.EXPECT().Info("info formatted").Return()
	base.EXPECT().Error("error formatted").Return()
	base.EXPECT().Debug("debug formatted").Return()
	base.EXPECT().Warn("warn formatted").Return()

	logger.Info("info")
	logger.Error("error")
	logger.Debug("debug")
	logger.Warn("warn")
	logger.Infof("info %s", "formatted")
	logger.Errorf("error %s", "formatted")
	logger.Debugf("debug %s", "formatted")
	logger.Warnf("warn %s", "formatted")
}

// stubValues stands in for a workflow context so field assembly can be tested
// without a workflow runtime.
type stubValues map[interface{}]interface{}

func (s stubValues) Value(key interface{}) interface{} { return s[key] }

// Workflow logs must carry requestID, traceMethod and traceURL, not just the
// correlation id, so they match the HTTP access lines for the same request.
func TestRequestLogFields_IncludesPropagatedTraceFields(t *testing.T) {
	fields := requestLogFields(stubValues{
		middleware.TemporalSLoggerKey: log.Fields{
			"requestCorrelationID": "corr-abc",
			"requestID":            "req-123",
			"traceMethod":          "POST",
			"traceURL":             "/v1/config",
		},
	})

	assert.Equal(t, "corr-abc", fields[orchestrator.RequestCorrelationID])
	assert.Equal(t, "req-123", fields["requestID"])
	assert.Equal(t, "POST", fields["traceMethod"])
	assert.Equal(t, "/v1/config", fields["traceURL"])
}

// The dedicated id keys are set closer to the dispatch site, so they win over
// a stale value inside the propagated map.
func TestRequestLogFields_DedicatedIDKeysOverrideMap(t *testing.T) {
	fields := requestLogFields(stubValues{
		middleware.TemporalSLoggerKey: log.Fields{
			"requestCorrelationID": "stale",
			"requestID":            "req-123",
		},
		orchestrator.CorrelationIDKey: "corr-fresh",
		orchestrator.DeploymentIDKey:  "gcnv-1",
	})

	assert.Equal(t, "corr-fresh", fields[orchestrator.RequestCorrelationID])
	assert.Equal(t, "gcnv-1", fields[orchestrator.RequestDeploymentID])
	assert.Equal(t, "req-123", fields["requestID"])
}

func TestRequestLogFields_EmptyIDsAreOmitted(t *testing.T) {
	fields := requestLogFields(stubValues{
		orchestrator.CorrelationIDKey: "",
		orchestrator.DeploymentIDKey:  "",
	})
	assert.Empty(t, fields)
}

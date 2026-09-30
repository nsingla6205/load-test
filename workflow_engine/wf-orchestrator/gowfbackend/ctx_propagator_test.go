package gowfbackend

import (
	"context"
	"testing"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func ingressFields() log.Fields {
	return log.Fields{
		"requestCorrelationID": "corr-abc",
		"requestID":            "req-123",
		"traceMethod":          "POST",
		"traceURL":             "/v1/config",
	}
}

// The full ingress field set must survive dispatch, otherwise workflow and
// activity lines carry only the correlation id while the HTTP access lines for
// the same request carry all four fields.
func TestPropagator_RequestFieldsMapSurvivesRoundTrip(t *testing.T) {
	p := NewContextMapPropagator()
	m := gowf.Metadata{}

	src := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, ingressFields())
	require.NoError(t, p.Inject(src, &m))

	got, err := p.Extract(context.Background(), &m)
	require.NoError(t, err)

	assert.Equal(t, ingressFields(), got.Value(middleware.TemporalSLoggerKey))
}

func TestPropagator_ScalarIDsSurviveRoundTrip(t *testing.T) {
	p := NewContextMapPropagator()
	m := gowf.Metadata{}

	src := context.WithValue(context.Background(), orchestrator.CorrelationIDKey, "corr-abc")
	src = context.WithValue(src, orchestrator.DeploymentIDKey, "gcnv-1")
	require.NoError(t, p.Inject(src, &m))

	got, err := p.Extract(context.Background(), &m)
	require.NoError(t, err)

	assert.Equal(t, "corr-abc", got.Value(orchestrator.CorrelationIDKey))
	assert.Equal(t, "gcnv-1", got.Value(orchestrator.DeploymentIDKey))
}

func TestPropagator_AbsentAndEmptyValuesAreNotInjected(t *testing.T) {
	p := NewContextMapPropagator()
	m := gowf.Metadata{}

	src := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	src = context.WithValue(src, orchestrator.CorrelationIDKey, "")
	require.NoError(t, p.Inject(src, &m))

	assert.Empty(t, m.Get(logParamWireKey))
	assert.Empty(t, m.Get("correlationID"))
}

// Malformed metadata must not fail the dispatch; losing log fields is the
// lesser cost.
func TestPropagator_MalformedLogParamIsDroppedNotFatal(t *testing.T) {
	p := NewContextMapPropagator()
	m := gowf.Metadata{}
	m.Set(logParamWireKey, "{not-json")

	got, err := p.Extract(context.Background(), &m)
	require.NoError(t, err)
	assert.Nil(t, got.Value(middleware.TemporalSLoggerKey))
}

func TestEncodeMetadataValue_UnsupportedTypeIsSkipped(t *testing.T) {
	_, ok := encodeMetadataValue(42)
	assert.False(t, ok)
}

func TestEncodeMetadataValue_UnmarshalableFieldIsDropped(t *testing.T) {
	encoded, ok := encodeMetadataValue(log.Fields{"ch": make(chan int)})
	assert.False(t, ok)
	assert.Empty(t, encoded)
}

func TestDecodeMetadataValue_NonLogParamPassesThroughAsString(t *testing.T) {
	v, ok := decodeMetadataValue("correlationID", "corr-abc")
	require.True(t, ok)
	assert.Equal(t, "corr-abc", v)
}

package temporalbackend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type fakeHeaderWriter struct {
	data map[string]*commonpb.Payload
}

func newFakeHeaderWriter() *fakeHeaderWriter {
	return &fakeHeaderWriter{data: map[string]*commonpb.Payload{}}
}

func (w *fakeHeaderWriter) Set(key string, value *commonpb.Payload) {
	w.data[key] = value
}

type fakeHeaderReader struct {
	data map[string]*commonpb.Payload
}

func (r *fakeHeaderReader) Get(key string) (*commonpb.Payload, bool) {
	v, ok := r.data[key]
	return v, ok
}

func (r *fakeHeaderReader) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for k, v := range r.data {
		if err := handler(k, v); err != nil {
			return err
		}
	}
	return nil
}

func mustPayload(t *testing.T, v interface{}) *commonpb.Payload {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(v)
	require.NoError(t, err)
	return p
}

func TestNewContextMapPropagator_NotNil(t *testing.T) {
	assert.NotNil(t, NewContextMapPropagator())
}

func TestContextMapPropagator_InjectAndExtract_RoundTrip(t *testing.T) {
	p := NewContextMapPropagator()

	ctx := context.Background()
	ctx = context.WithValue(ctx, orchestrator.CorrelationIDKey, "corr-123")
	ctx = context.WithValue(ctx, orchestrator.DeploymentIDKey, "dep-456")

	writer := newFakeHeaderWriter()
	require.NoError(t, p.Inject(ctx, writer))
	assert.Contains(t, writer.data, "correlationID")
	assert.Contains(t, writer.data, "deploymentID")

	reader := &fakeHeaderReader{data: writer.data}
	outCtx, err := p.Extract(context.Background(), reader)
	require.NoError(t, err)
	assert.Equal(t, "corr-123", outCtx.Value(orchestrator.CorrelationIDKey))
	assert.Equal(t, "dep-456", outCtx.Value(orchestrator.DeploymentIDKey))
}

func TestContextMapPropagator_Inject_SkipsMissingValues(t *testing.T) {
	p := NewContextMapPropagator()
	writer := newFakeHeaderWriter()
	require.NoError(t, p.Inject(context.Background(), writer))
	assert.Empty(t, writer.data)
}

func TestContextMapPropagator_Extract_IgnoresUnknownKeys(t *testing.T) {
	p := NewContextMapPropagator()
	reader := &fakeHeaderReader{data: map[string]*commonpb.Payload{
		"unrelated": mustPayload(t, "value"),
	}}
	outCtx, err := p.Extract(context.Background(), reader)
	require.NoError(t, err)
	assert.Nil(t, outCtx.Value(orchestrator.CorrelationIDKey))
}

package gowfbackend

import (
	"context"
	"encoding/json"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// ContextPropagateKeys lists the metadata keys that travel with a workflow
// across the worker boundary. Values are looked up by their context-keys in
// the source context (correlationID -> orchestrator.CorrelationIDKey, etc.) and
// stored under the matching wire-protocol metadata key.
//
// Kept identical to temporalbackend.ContextPropagateKeys so logs in either
// backend look the same to downstream tools.
var ContextPropagateKeys = []string{
	logParamWireKey,
	"correlationID",
	"deploymentID",
}

// logParamWireKey carries the ingress request-fields map (requestCorrelationID,
// requestID, traceMethod, traceURL) rather than a single id. It matches the
// Temporal propagator's "logParam" entry so a workflow logs the same field set
// under either backend.
const logParamWireKey = "logParam"

// keyMap returns the wire-key -> context-key mapping. Built lazily once and
// shared across all propagator instances.
func keyMap() map[string]interface{} {
	m := make(map[string]interface{}, len(ContextPropagateKeys))
	for _, k := range ContextPropagateKeys {
		switch k {
		case logParamWireKey:
			m[k] = middleware.TemporalSLoggerKey
		case "correlationID":
			m[k] = orchestrator.CorrelationIDKey
		case "deploymentID":
			m[k] = orchestrator.DeploymentIDKey
		default:
			m[k] = k
		}
	}
	return m
}

// encodeMetadataValue renders a propagated context value for the string-valued
// go-workflows metadata. The request-fields map is carried as JSON; Temporal
// can put a map on the wire natively, gowf metadata cannot. The bool reports
// whether the value is worth propagating at all.
func encodeMetadataValue(v interface{}) (string, bool) {
	switch val := v.(type) {
	case string:
		return val, val != ""
	case log.Fields:
		if len(val) == 0 {
			return "", false
		}
		encoded, err := json.Marshal(val)
		if err != nil {
			// Field maps hold ingress strings, so this is not expected. Dropping
			// the entry costs log attribution on the dispatched workflow; failing
			// the dispatch over it would be worse.
			return "", false
		}
		return string(encoded), true
	default:
		return "", false
	}
}

// decodeMetadataValue reverses encodeMetadataValue. Every wire key other than
// logParam is a plain string.
func decodeMetadataValue(wireKey, raw string) (interface{}, bool) {
	if wireKey != logParamWireKey {
		return raw, true
	}
	var fields log.Fields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		// Malformed metadata: prefer losing the fields over failing the workflow.
		return nil, false
	}
	return fields, true
}

// gowfContextMapPropagator implements gowf.ContextPropagator. It moves
// correlationID and deploymentID across the activity / sub-workflow
// boundary so per-request structured logs survive the dispatch.
type gowfContextMapPropagator struct {
	keys map[string]interface{}
}

// NewContextMapPropagator returns a propagator suitable for passing as a
// cschleiden worker option. Wire-format mirrors the Temporal-side
// equivalent: a single string-valued metadata entry per key.
func NewContextMapPropagator() gowf.ContextPropagator {
	orchestrator.Infof("Creating gowfContextMapPropagator with keys: %v", ContextPropagateKeys)
	return &gowfContextMapPropagator{keys: keyMap()}
}

func (p *gowfContextMapPropagator) Inject(ctx context.Context, m *gowf.Metadata) error {
	for wireKey, ctxKey := range p.keys {
		if s, ok := encodeMetadataValue(ctx.Value(ctxKey)); ok {
			m.Set(wireKey, s)
		}
	}
	return nil
}

func (p *gowfContextMapPropagator) Extract(ctx context.Context, m *gowf.Metadata) (context.Context, error) {
	for wireKey, ctxKey := range p.keys {
		raw := m.Get(wireKey)
		if raw == "" {
			continue
		}
		if v, ok := decodeMetadataValue(wireKey, raw); ok {
			ctx = context.WithValue(ctx, ctxKey, v)
		}
	}
	ctx = extractScheduleToCloseToContext(ctx, m)
	return ctx, nil
}

func (p *gowfContextMapPropagator) InjectFromWorkflow(ctx gowf.Context, m *gowf.Metadata) error {
	for wireKey, ctxKey := range p.keys {
		if s, ok := encodeMetadataValue(ctx.Value(ctxKey)); ok {
			m.Set(wireKey, s)
		}
	}
	injectScheduleToCloseFromWorkflow(ctx, m)
	return nil
}

func (p *gowfContextMapPropagator) ExtractToWorkflow(ctx gowf.Context, m *gowf.Metadata) (gowf.Context, error) {
	for wireKey, ctxKey := range p.keys {
		raw := m.Get(wireKey)
		if raw == "" {
			continue
		}
		if v, ok := decodeMetadataValue(wireKey, raw); ok {
			ctx = gowf.WithValue(ctx, ctxKey, v)
		}
	}
	return ctx, nil
}

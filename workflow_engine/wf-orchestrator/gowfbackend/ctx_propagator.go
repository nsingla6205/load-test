package gowfbackend

import (
	"context"

	gowf "github.com/cschleiden/go-workflows/workflow"
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
	"correlationID",
	"deploymentID",
}

// keyMap returns the wire-key -> context-key mapping. Built lazily once and
// shared across all propagator instances.
func keyMap() map[string]interface{} {
	m := make(map[string]interface{}, len(ContextPropagateKeys))
	for _, k := range ContextPropagateKeys {
		switch k {
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
		v := ctx.Value(ctxKey)
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			m.Set(wireKey, s)
		}
	}
	return nil
}

func (p *gowfContextMapPropagator) Extract(ctx context.Context, m *gowf.Metadata) (context.Context, error) {
	for wireKey, ctxKey := range p.keys {
		if v := m.Get(wireKey); v != "" {
			ctx = context.WithValue(ctx, ctxKey, v)
		}
	}
	ctx = extractScheduleToCloseToContext(ctx, m)
	return ctx, nil
}

func (p *gowfContextMapPropagator) InjectFromWorkflow(ctx gowf.Context, m *gowf.Metadata) error {
	for wireKey, ctxKey := range p.keys {
		v := ctx.Value(ctxKey)
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			m.Set(wireKey, s)
		}
	}
	injectScheduleToCloseFromWorkflow(ctx, m)
	return nil
}

func (p *gowfContextMapPropagator) ExtractToWorkflow(ctx gowf.Context, m *gowf.Metadata) (gowf.Context, error) {
	for wireKey, ctxKey := range p.keys {
		if v := m.Get(wireKey); v != "" {
			ctx = gowf.WithValue(ctx, ctxKey, v)
		}
	}
	return ctx, nil
}

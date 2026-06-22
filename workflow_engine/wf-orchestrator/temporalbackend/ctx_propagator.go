package temporalbackend

import (
	"context"

	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// ContextPropagateKeys lists metadata keys propagated across workflow boundaries.
// Kept in sync with lib/log/util.ContextPropagateKeys.
var ContextPropagateKeys = []string{
	"correlationID",
	"deploymentID",
}

type contextMapPropagator struct {
	keySet map[string]interface{}
}

// NewContextMapPropagator returns a Temporal context propagator for correlation/deployment IDs.
func NewContextMapPropagator() workflow.ContextPropagator {
	keyMap := make(map[string]interface{}, len(ContextPropagateKeys))
	for _, key := range ContextPropagateKeys {
		switch key {
		case "correlationID":
			keyMap[key] = orchestrator.CorrelationIDKey
		case "deploymentID":
			keyMap[key] = orchestrator.DeploymentIDKey
		default:
			keyMap[key] = key
		}
	}
	orchestrator.Infof("Creating contextMapPropagator with keys: %v", keyMap)
	return &contextMapPropagator{keyMap}
}

func (s *contextMapPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	for key, ctxKey := range s.keySet {
		if value := ctx.Value(ctxKey); value != nil {
			encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
			if err != nil {
				return err
			}
			writer.Set(key, encodedValue)
		}
	}
	return nil
}

func (s *contextMapPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	for key, ctxKey := range s.keySet {
		if value := ctx.Value(ctxKey); value != nil {
			encodedValue, err := converter.GetDefaultDataConverter().ToPayload(value)
			if err != nil {
				return err
			}
			writer.Set(key, encodedValue)
		}
	}
	return nil
}

func (s *contextMapPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	if err := reader.ForEachKey(func(key string, value *commonpb.Payload) error {
		if ctxKey, ok := s.keySet[key]; ok {
			var decodedValue string
			err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
			if err != nil {
				return err
			}
			ctx = context.WithValue(ctx, ctxKey, decodedValue)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (s *contextMapPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if err := reader.ForEachKey(func(key string, value *commonpb.Payload) error {
		if ctxKey, ok := s.keySet[key]; ok {
			var decodedValue string
			err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
			if err != nil {
				return err
			}
			ctx = workflow.WithValue(ctx, ctxKey, decodedValue)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ctx, nil
}

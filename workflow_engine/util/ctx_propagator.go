package util

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// Should be in accordance with the keys defined in the client/vlm/config.go file.
// Due to cyclic dependency issues, we cannot import the vlm package here.
const (
	vlmCorrelationIDKey = "x-correlation-id"
	vlmDeploymentIDKey  = "x-deployment-id"
)

// ContextPropagateKeys defines keys which are to be propagated.
var ContextPropagateKeys = []string{
	// Params to construct back the logger in workflow/activity
	// Propagating the log params & not the logger object itself
	// since logger object (having protected fields) is not json serializable.
	// Any param which is context propagated in temporal
	// should be json serializable
	"logParam",
	"Authorization",

	// VLM specific keys
	"correlationID",
	"deploymentID",
	"opcWorkRequestID",
}

// contextMapPropagator propagates the keySet across a workflow,
// interpreting the key as strings & value as interface.
type contextMapPropagator struct {
	// keySet value defines vaules to be propagated as string interface pair
	keySet map[string]interface{}
}

// NewContextMapPropagator returns a new string context propagator
func NewContextMapPropagator() workflow.ContextPropagator {
	keyMap := make(map[string]interface{}, len(ContextPropagateKeys))
	for _, key := range ContextPropagateKeys {
		switch key {
		case "logParam":
			keyMap[key] = middleware.TemporalSLoggerKey
		case "Authorization":
			keyMap[key] = middleware.AuthorizationToken
		case "correlationID":
			keyMap[key] = vlmCorrelationIDKey
		case "deploymentID":
			keyMap[key] = vlmDeploymentIDKey
		case "opcWorkRequestID":
			keyMap[key] = middleware.OPCWorkRequestIDHeaderName
		default:
			keyMap[key] = key
		}
	}
	return &contextMapPropagator{keyMap}
}

// Inject injects values from context into headers for propagation
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

// InjectFromWorkflow injects values from workflow context into headers for propagation
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

// Extract extracts values from headers and puts them into context
func (s *contextMapPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	if err := reader.ForEachKey(func(key string, value *commonpb.Payload) error {
		if ctxKey, ok := s.keySet[key]; ok {
			switch key {
			case "logParam":
				var decodedValue log.Fields
				err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
				if err != nil {
					return err
				}
				ctx = context.WithValue(ctx, ctxKey, decodedValue)
			default:
				var decodedValue string
				err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
				if err != nil {
					return err
				}
				ctx = context.WithValue(ctx, ctxKey, decodedValue)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ctx, nil
}

// ExtractToWorkflow extracts values from headers and puts them into workflow context
func (s *contextMapPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	if err := reader.ForEachKey(func(key string, value *commonpb.Payload) error {
		if ctxKey, ok := s.keySet[key]; ok {
			switch key {
			case "logParam":
				var decodedValue log.Fields
				err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
				if err != nil {
					return err
				}
				ctx = workflow.WithValue(ctx, ctxKey, decodedValue)
			default:
				var decodedValue string
				err := converter.GetDefaultDataConverter().FromPayload(value, &decodedValue)
				if err != nil {
					return err
				}
				ctx = workflow.WithValue(ctx, ctxKey, decodedValue)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ctx, nil
}

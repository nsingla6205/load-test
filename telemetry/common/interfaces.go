package common

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// UsageSink defines the interface for delivering usage metrics to external systems
type UsageSink interface {
	DeliverMetrics(ctx context.Context, metrics []datamodel.AggregatedUsage, aggregationEndTime time.Time) (int, error)
}

// IsBillableMetric checks if a given resource type and measured type combination represents a billable metric
func IsBillableMetric(ctx context.Context, resourceType metadata.ResourceType, measuredType metadata.MeasuredType) bool {
	logger := util.GetLogger(ctx)
	// Create the lookup key
	key := metadata.CombinedKeyResourceTypeMeasuredType{
		ResourceType: resourceType,
		MeasuredType: measuredType,
	}

	// Look up the key in the pre-computed map
	jobDef, exists := DefaultAggregationJobDefinitions[key]
	if !exists {
		logger.Warnf("No job definition found for resource type %s and measured type %s", resourceType, measuredType)
		return false // If the key does not exist, return false
	}
	return jobDef.IsBillable // Return true if the key exists, false otherwise
}

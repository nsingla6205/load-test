package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type CollectMetrics struct {
	Data          string `json:"data"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type CollectMetricsPayload struct {
	ProjectID string    `json:"project_id"`
	Timestamp time.Time `json:"timestamp"`
}

func NewCollectMetrics(projectID string, timestamp time.Time) *CollectMetrics {
	payload := CollectMetricsPayload{
		ProjectID: projectID,
		Timestamp: timestamp,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		// In case of marshal error, fallback to just using projectID
		return &CollectMetrics{
			Data: projectID,
		}
	}

	return &CollectMetrics{
		Data: string(jsonData),
	}
}

func (e CollectMetrics) Perform(ctx context.Context, p interface{}, attempt int32) error {
	proc, ok := p.(common.VCPProcessor)
	if !ok {
		return fmt.Errorf("invalid processor type: %T", p)
	}

	// Layer correlation-id logging onto the bounded job context.
	if e.CorrelationID != "" {
		// Set up logger fields with correlation ID
		loggerFields := log.Fields{
			"requestCorrelationID": e.CorrelationID,
		}
		// Create a logger with the correlation ID fields
		logger := log.NewLogger().WithFields("requestFields", loggerFields)

		// Set both the logger and the fields in context for maximum compatibility
		ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
	}
	logger := util.GetLogger(ctx)
	logger.Infof("Processing collect metrics job with correlation ID: %s, attempt: %d, project: %s", e.CorrelationID, attempt, e.Data)

	// Try to parse as JSON first
	var payload CollectMetricsPayload
	err := json.Unmarshal([]byte(e.Data), &payload)
	if err != nil {
		logger.Errorf("Failed to Unmarshal Job Data with correlation ID %s for project %s: %v", e.CorrelationID, payload.ProjectID, err)
		return err
	} else {
		// Use projectID from parsed payload
		err = proc.CollectMetrics(ctx, payload.ProjectID, payload.Timestamp)
	}

	if err != nil {
		if e.CorrelationID != "" {
			logger := util.GetLogger(ctx)
			logger.Errorf("Failed to collect metrics with correlation ID %s for project %s: %v", e.CorrelationID, e.Data, err)
		}
		return err
	}

	if e.CorrelationID != "" {
		logger := util.GetLogger(ctx)
		logger.Infof("Successfully collected metrics with correlation ID: %s for project: %s", e.CorrelationID, e.Data)
	}

	return nil
}

func (e CollectMetrics) Load(data string) (utils.Job, error) {
	var cm CollectMetrics
	err := json.Unmarshal([]byte(data), &cm)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal CollectMetrics: %v", err)
	}
	return cm, nil
}

package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type ProcessPerformanceMetrics struct {
	Data          string `json:"data"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// PerformanceMetricsPayload contains the immutable end of a collection window.
type PerformanceMetricsPayload struct {
	Timestamp time.Time `json:"timestamp"`
}

// NewProcessPerformanceMetrics creates a job for a stable collection window.
func NewProcessPerformanceMetrics(timestamp time.Time) (*ProcessPerformanceMetrics, error) {
	payload := PerformanceMetricsPayload{
		Timestamp: timestamp.UTC(),
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal performance metrics payload: %w", err)
	}

	return &ProcessPerformanceMetrics{
		Data: string(jsonData),
	}, nil
}

func (e ProcessPerformanceMetrics) Perform(ctx context.Context, p interface{}, attempt int32) error {
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
	logger.Infof("Processing performance metrics job with correlation ID: %s, attempt: %d", e.CorrelationID, attempt)

	var payload PerformanceMetricsPayload
	if err := json.Unmarshal([]byte(e.Data), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal performance metrics payload: %w", err)
	}
	if payload.Timestamp.IsZero() {
		if strings.TrimSpace(e.Data) != "{}" {
			return fmt.Errorf("performance metrics payload timestamp is required")
		}
		payload.Timestamp = time.Now().UTC().Truncate(time.Minute)
		logger.Warnf(
			"Legacy performance metrics payload has no timestamp; using execution time %s as window end",
			payload.Timestamp.Format(time.RFC3339),
		)
	}

	err := proc.ProcessPerformanceMetrics(ctx, payload.Timestamp)
	if err != nil {
		if e.CorrelationID != "" {
			logger := util.GetLogger(ctx)
			logger.Errorf("Failed to process performance metrics with correlation ID %s: %v", e.CorrelationID, err)
		}
		return err
	}

	if e.CorrelationID != "" {
		logger := util.GetLogger(ctx)
		logger.Infof("Successfully processed performance metrics with correlation ID: %s", e.CorrelationID)
	}

	return nil
}

func (e ProcessPerformanceMetrics) Load(data string) (utils.Job, error) {
	var job ProcessPerformanceMetrics
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return ProcessPerformanceMetrics{}, err
	}
	return job, nil
}

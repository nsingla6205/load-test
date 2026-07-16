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

type ProcessUsageMetrics struct {
	Data          string `json:"data"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

type UsageMetricsPayload struct {
	Timestamp            time.Time `json:"timestamp"`
	AggregationStartTime time.Time `json:"aggregation_start_time"`
	AggregationEndTime   time.Time `json:"aggregation_end_time"`
}

func NewProcessUsageMetrics(timeStamp time.Time, aggregationStartTime time.Time, aggregationEndTime time.Time) *ProcessUsageMetrics {
	payload := UsageMetricsPayload{
		Timestamp:            timeStamp,
		AggregationStartTime: aggregationStartTime,
		AggregationEndTime:   aggregationEndTime,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return &ProcessUsageMetrics{
			Data: timeStamp.String(),
		}
	}
	return &ProcessUsageMetrics{
		Data: string(jsonData),
	}
}

func (e ProcessUsageMetrics) Perform(ctx context.Context, p interface{}, attempt int32) error {
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
	logger.Infof("Processing usage metrics job with correlation ID: %s, attempt: %d", e.CorrelationID, attempt)

	var payload UsageMetricsPayload
	err := json.Unmarshal([]byte(e.Data), &payload)
	if err != nil {
		logger.Errorf("Failed to Unmarshal Job Data with correlation ID %s for raw data %s: %v", e.CorrelationID, e.Data, err)
		return err
	}

	err = proc.ProcessUsageMetrics(ctx, payload.AggregationEndTime)

	if err != nil {
		if e.CorrelationID != "" {
			logger := util.GetLogger(ctx)
			logger.Errorf("Failed to process usage metrics with correlation ID %s: %v", e.CorrelationID, err)
		}
		return err
	}

	if e.CorrelationID != "" {
		logger := util.GetLogger(ctx)
		logger.Infof("Successfully processed usage metrics with correlation ID: %s", e.CorrelationID)
	}

	return nil
}

func (e ProcessUsageMetrics) Load(data string) (utils.Job, error) {
	var job ProcessUsageMetrics
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return ProcessUsageMetrics{}, err
	}
	return job, nil
}

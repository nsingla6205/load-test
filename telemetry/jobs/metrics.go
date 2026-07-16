package jobs

import (
	"context"
	"encoding/json"
	"fmt"

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

func NewProcessPerformanceMetrics(data string) *ProcessPerformanceMetrics {
	return &ProcessPerformanceMetrics{
		Data: data,
	}
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

	err := proc.ProcessPerformanceMetrics(ctx)
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

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

// ProcessBillingSubmission handles Submission processing for unsent billing records
type ProcessBillingSubmission struct {
	Data          string `json:"data"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ProcessBillingSubmissionPayload contains parameters for the billing Submission job
type ProcessBillingSubmissionPayload struct {
	AggregationEndTime time.Time `json:"aggregation_end_time,omitempty"`
}

// NewDeliverBillingMetrics creates a new billing delivery job
func NewDeliverBillingMetrics(aggregationEndTime time.Time) *ProcessBillingSubmission {
	payload := ProcessBillingSubmissionPayload{
		AggregationEndTime: aggregationEndTime,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		// Fallback to empty data if marshal fails
		return &ProcessBillingSubmission{
			Data: "{}",
		}
	}

	return &ProcessBillingSubmission{
		Data: string(jsonData),
	}
}

// Perform processes unsent billing records and handles recursive Submission scheduling.
// ctx is the bounded, cancellable job context; it is threaded into the billing
// push so a hung submission fails (and retries) instead of hanging, and so a
// reclaimed job stops in-flight instead of double-submitting.
func (e ProcessBillingSubmission) Perform(ctx context.Context, p interface{}, attempt int32) error {
	proc, ok := p.(common.VCPProcessor)
	if !ok {
		return fmt.Errorf("invalid processor type: %T", p)
	}

	// Layer correlation-id logging onto the job context for better logging.
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
	logger.Infof("Processing billing Submission job with correlation ID: %s, attempt: %d ", e.CorrelationID, attempt)

	// Parse job payload
	var payload ProcessBillingSubmissionPayload
	err := json.Unmarshal([]byte(e.Data), &payload)
	if err != nil {
		logger.Errorf("Failed to unmarshal billing Submission job data: %v", err)
		return err
	}

	// Use the new ProcessBillingSubmission method instead of ProcessUsageMetrics
	err = proc.ProcessBillingSubmission(ctx, payload.AggregationEndTime)
	if err != nil {
		logger.Errorf("Failed to process billing Submission with correlation ID %s: %v", e.CorrelationID, err)
		return err
	}

	logger.Infof("Successfully processed billing Submission job with correlation ID: %s", e.CorrelationID)
	return nil
}

// Load deserializes job data back into a ProcessBillingSubmission struct
func (e ProcessBillingSubmission) Load(data string) (utils.Job, error) {
	var SubmissionJob ProcessBillingSubmission
	err := json.Unmarshal([]byte(data), &SubmissionJob)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal ProcessBillingSubmission: %v", err)
	}
	return SubmissionJob, nil
}

package common

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
)

type VCPProcessor interface {
	ProcessPerformanceMetrics(ctx context.Context, timestamp time.Time) error
	ProcessUsageMetrics(ctx context.Context, timestamp time.Time) error
	CollectMetrics(ctx context.Context, projectId string, timestamp time.Time) error
	ProcessBizOps(ctx context.Context, params *utils.BizOpsReportParams) error
	ProcessBillingSubmission(ctx context.Context, aggregationEndTime time.Time) error
}

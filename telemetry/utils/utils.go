package utils

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
)

const (
	dateLayout = "2006-01-02"
	UTC        = "UTC"
	PST        = "PST"

	// BizOpsSinkGCS identifies the durable BizOps report sink.
	BizOpsSinkGCS = monitoring.SinkTypeGCS
)

var (
	PerformanceQueue  = "performance"
	UsageQueue        = "usage"
	CollectionQueue   = "collection"
	BizOpsReportQueue = "bizops"
	BillingRetryQueue = "billing-retry"
)

type BizOpsReportParams struct {
	StartDate time.Time `json:"StartDate"`
	EndDate   time.Time `json:"EndDate"`
	TimeZone  string    `json:"TimeZone"`
	SinkType  string    `json:"SinkType"`
}

func ParseBizOpsReportParams(bizOpsReportParams *BizOpsReportParams) error {
	var timezone string
	switch bizOpsReportParams.TimeZone {
	case UTC:
		timezone = bizOpsReportParams.TimeZone
	case PST:
		timezone = "America/Los_Angeles"
	default:
		return fmt.Errorf("the time zone must be set to 'UTC' or 'PST' - received: '%s'", bizOpsReportParams.TimeZone)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return fmt.Errorf("load location failure: %v", err)
	}
	if bizOpsReportParams.StartDate.IsZero() {
		bizOpsReportParams.StartDate = time.Now().Add(time.Hour * -24).In(location)
	}
	bizOpsReportParams.StartDate = bizOpsReportParams.StartDate.Truncate(time.Hour * 24)
	_, offset := bizOpsReportParams.StartDate.In(location).Zone()
	bizOpsReportParams.StartDate = bizOpsReportParams.StartDate.Add(time.Second * time.Duration(-offset)).UTC()
	bizOpsReportParams.EndDate = bizOpsReportParams.StartDate.Add(time.Hour * 24).UTC()
	return nil
}

func ValidateResourceMetadata(resourceMetadata metadata.ResourceMetadata) error {
	if resourceMetadata.ResourceName == nil {
		return fmt.Errorf("ResourceName is nil")
	}
	if resourceMetadata.RegionName == nil {
		return fmt.Errorf("RegionName is nil")
	}
	if resourceMetadata.DeploymentName == nil {
		return fmt.Errorf("DeploymentName is nil")
	}
	return nil
}

func PrepareAggregationTime(t time.Time, targetMinute int) time.Time {
	totalMinutes := t.Hour()*60 + t.Minute()

	adjustedMinutes := totalMinutes - targetMinute

	var minutesToSubtract int

	// If we're before the first target minute mark of the day, go to previous day's target minute
	if adjustedMinutes < 0 {
		// For times before target minute, we need to go back to target minute of previous day
		// Minutes to subtract = current minutes + (60 - target minute)
		minutesToSubtract = totalMinutes + (60 - targetMinute)
	} else {
		// For normal cases, calculate excess minutes beyond the previous target minute mark
		excessMinutes := adjustedMinutes % 60
		minutesToSubtract = excessMinutes
	}

	result := t.Add(-time.Duration(minutesToSubtract) * time.Minute)

	return result.Truncate(time.Minute)
}

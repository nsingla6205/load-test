package main

import (
	"context"
	"fmt"
	"log"
	"time"

	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/datamodel"
	"google.golang.org/genproto/googleapis/api/metric"
	"google.golang.org/genproto/googleapis/api/monitoredres"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func pushCustomMetrics(ctx context.Context, client *monitoring.MetricClient, volume *datamodel.Volume, replication *datamodel.VolumeReplication, projectID string) error {
	projectName := fmt.Sprintf("projects/%s", projectID)

	// Sample data for each metric type
	metricsData := map[string]float64{
		"volume_space_logical_used":       float64(volume.UsedBytes),
		"volume_capacity":                 float64(volume.SizeInBytes),
		"snapmirror_total_transfer_bytes": float64(replication.TotalTransferBytes),
	}

	// Process metrics in smaller batches
	batchSize := 5
	successCount := 0
	errorCount := 0

	metricKeys := make([]string, 0, len(metricsData))
	for k := range metricsData {
		metricKeys = append(metricKeys, k)
	}

	for i := 0; i < len(metricKeys); i += batchSize {
		end := i + batchSize
		if end > len(metricKeys) {
			end = len(metricKeys)
		}

		var timeSeries []*monitoringpb.TimeSeries

		for j := i; j < end; j++ {
			metricName := metricKeys[j]
			value := metricsData[metricName]

			dataPoint := &monitoringpb.Point{
				Interval: &monitoringpb.TimeInterval{
					EndTime: timestamppb.Now(),
				},
				Value: &monitoringpb.TypedValue{
					Value: &monitoringpb.TypedValue_DoubleValue{
						DoubleValue: value,
					},
				},
			}

			labels := map[string]string{
				"volume":          volume.Name,
				"datacenter":      "australia-southeast1",
				"cluster":         "cluster-01",
				"project_id":      projectID,
				"deployment_name": volume.Pool.DeploymentName,
				"project":         volume.Pool.Account.Name,
			}

			if metricName == "snapmirror_total_transfer_bytes" {
				labels["relationship_id"] = replication.ReplicationAttributes.ExternalUUID
			}

			ts := &monitoringpb.TimeSeries{
				Metric: &metric.Metric{
					Type:   fmt.Sprintf("custom.googleapis.com/%s", metricName),
					Labels: labels,
				},
				Resource: &monitoredres.MonitoredResource{
					Type: "generic_task",
					Labels: map[string]string{
						"project_id": projectID,
						"location":   "australia-southeast1",
						"namespace":  "default",
						"job":        "metric-sender",
						"task_id":    "1",
					},
				},
				Points: []*monitoringpb.Point{dataPoint},
			}

			timeSeries = append(timeSeries, ts)
		}

		// Create the request with batch of time series
		req := &monitoringpb.CreateTimeSeriesRequest{
			Name:       projectName,
			TimeSeries: timeSeries,
		}

		// Write the time series data with retry
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			err := client.CreateTimeSeries(ctx, req)
			if err == nil {
				successCount += len(timeSeries)
				break
			}

			if retry < maxRetries-1 {
				log.Printf("Batch %d failed (attempt %d/%d), retrying in 2 seconds: %v", i/batchSize+1, retry+1, maxRetries, err)
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("Batch %d failed after %d attempts: %v", i/batchSize+1, maxRetries, err)
				errorCount += len(timeSeries)
			}
		}

		// Small delay between batches to avoid rate limiting
		if end < len(metricKeys) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	fmt.Printf("[%s] Metrics push completed - Success: %d, Failed: %d\n",
		time.Now().Format("2006-01-02 15:04:05"), successCount, errorCount)

	return nil
}

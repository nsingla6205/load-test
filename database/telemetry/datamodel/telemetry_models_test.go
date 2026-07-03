package datamodel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHydratedMetrics(t *testing.T) {
	t.Run("standard initialization", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Millisecond)
		metrics := HydratedMetrics{
			MetricTimestamp: now,
			MeasuredType:    "ALLOCATED_SIZE",
			ResourceType:    "VOLUME",
			Quantity:        42.5,
			ResourceName:    "vol-1",
			ConsumerID:      "customer-123",
			Location:        "us-west",
			Metadata:        []byte(`{"key":"value"}`),
		}

		// Verify all fields with assertions
		assert.Equal(t, now, metrics.MetricTimestamp, "MetricTimestamp should match")
		assert.Equal(t, "ALLOCATED_SIZE", metrics.MeasuredType, "MeasuredType should match")
		assert.Equal(t, "VOLUME", metrics.ResourceType, "ResourceType should match")
		assert.Equal(t, 42.5, metrics.Quantity, "Quantity should match")
		assert.Equal(t, "vol-1", metrics.ResourceName, "ResourceName should match")
		assert.Equal(t, "customer-123", metrics.ConsumerID, "ConsumerID should match")
		assert.Equal(t, "us-west", metrics.Location, "Location should match")
		assert.Equal(t, []byte(`{"key":"value"}`), metrics.Metadata, "Metadata should match")
	})

	t.Run("zero values", func(t *testing.T) {
		// Test with zero/default values
		metrics := HydratedMetrics{}

		assert.Equal(t, time.Time{}, metrics.MetricTimestamp, "MetricTimestamp should be zero value")
		assert.Equal(t, "", metrics.MeasuredType, "MeasuredType should be empty")
		assert.Equal(t, "", metrics.ResourceType, "ResourceType should be empty")
		assert.Equal(t, 0.0, metrics.Quantity, "Quantity should be zero")
		assert.Equal(t, "", metrics.ResourceName, "ResourceName should be empty")
		assert.Equal(t, "", metrics.ConsumerID, "ConsumerID should be empty")
		assert.Equal(t, "", metrics.Location, "Location should be empty")
		assert.Nil(t, metrics.Metadata, "Metadata should be nil")
	})
}

func TestAggregatedUsage(t *testing.T) {
	t.Run("full initialization", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Millisecond)
		start := now
		end := now.Add(time.Hour)
		accountUuid := "123e4567-e89b-12d3-a456-426614174000"
		resourceName := "vol-1"
		regionName := "us-west"
		sourceRegion := "us-east"
		destinationRegion := "eu-west"
		billingLabels := "tag1,tag2"
		replicationDstVolumeID := "dst-vol-1"
		doubleEncryption := true
		errorMessage := ""
		submission := "sub-123"
		lastCounterValue := 100.5
		lastTransferType := "update"

		usage := AggregatedUsage{
			ID:                     1,
			VendorCustomerID:       &accountUuid,
			AggregationStart:       start,
			AggregationEnd:         end,
			MeasuredType:           "ALLOCATED_SIZE",
			Quantity:               42.5,
			ResourceName:           &resourceName,
			ResourceType:           "VOLUME",
			AggregationType:        "HOURLY",
			LastCounterValue:       &lastCounterValue,
			LastTransferType:       &lastTransferType,
			RegionName:             &regionName,
			SourceRegion:           &sourceRegion,
			DestinationRegion:      &destinationRegion,
			BillingLabels:          &billingLabels,
			CreatedAt:              start,
			UpdatedAt:              end,
			ReplicationDstVolumeID: &replicationDstVolumeID,
			DoubleEncryption:       &doubleEncryption,
			State:                  Unsubmitted,
			ErrorCount:             0,
			ErrorMessage:           &errorMessage,
			Submission:             &submission,
			IsBillable:             true,
		}

		// Verify all fields with assertions
		assert.Equal(t, int64(1), usage.ID)
		assert.Equal(t, "123e4567-e89b-12d3-a456-426614174000", *usage.VendorCustomerID)
		assert.Equal(t, start, usage.AggregationStart)
		assert.Equal(t, end, usage.AggregationEnd)
		assert.Equal(t, "ALLOCATED_SIZE", usage.MeasuredType)
		assert.Equal(t, 42.5, usage.Quantity)
		assert.Equal(t, "vol-1", *usage.ResourceName)
		assert.Equal(t, "VOLUME", usage.ResourceType)
		assert.Equal(t, "HOURLY", usage.AggregationType)
		assert.Equal(t, 100.5, *usage.LastCounterValue)
		require.NotNil(t, usage.LastTransferType)
		assert.Equal(t, "update", *usage.LastTransferType)
		assert.Equal(t, "us-west", *usage.RegionName)
		assert.Equal(t, "us-east", *usage.SourceRegion)
		assert.Equal(t, "eu-west", *usage.DestinationRegion)
		assert.Equal(t, "tag1,tag2", *usage.BillingLabels)
		assert.Equal(t, start, usage.CreatedAt)
		assert.Equal(t, end, usage.UpdatedAt)
		assert.Equal(t, "dst-vol-1", *usage.ReplicationDstVolumeID)
		assert.True(t, *usage.DoubleEncryption)
		assert.Equal(t, Unsubmitted, usage.State)
		assert.Equal(t, int32(0), usage.ErrorCount)
		assert.Equal(t, "", *usage.ErrorMessage)
		assert.Equal(t, "sub-123", *usage.Submission)
		assert.True(t, usage.IsBillable)
	})

	t.Run("minimal initialization", func(t *testing.T) {
		// Test with minimal required fields
		now := time.Now().UTC().Truncate(time.Millisecond)
		usage := AggregatedUsage{
			ID:               100,
			AggregationStart: now,
			AggregationEnd:   now.Add(time.Hour),
			MeasuredType:     "THROUGHPUT",
			Quantity:         50.0,
			ResourceType:     "VOLUME",
			AggregationType:  "DAILY",
			State:            Submitted,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		assert.Equal(t, int64(100), usage.ID)
		assert.Equal(t, "THROUGHPUT", usage.MeasuredType)
		assert.Equal(t, "VOLUME", usage.ResourceType)
		assert.Equal(t, Submitted, usage.State)
		assert.Equal(t, 50.0, usage.Quantity)
		assert.Equal(t, "DAILY", usage.AggregationType)

		// Verify nil pointer fields are nil
		assert.Nil(t, usage.VendorCustomerID)
		assert.Nil(t, usage.ResourceName)
		assert.Nil(t, usage.LastCounterValue)
		assert.Nil(t, usage.LastTransferType)
		assert.Nil(t, usage.RegionName)
		assert.Nil(t, usage.SourceRegion)
		assert.Nil(t, usage.DestinationRegion)
		assert.Nil(t, usage.BillingLabels)
		assert.Nil(t, usage.ReplicationDstVolumeID)
		assert.Nil(t, usage.DoubleEncryption)
		assert.Nil(t, usage.ErrorMessage)
		assert.Nil(t, usage.Submission)

		// Verify default values
		assert.Equal(t, int32(0), usage.ErrorCount)
		assert.False(t, usage.IsBillable)
	})
}

func TestJob(t *testing.T) {
	t.Run("full initialization", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Millisecond)
		startedAt := now.Add(time.Minute)
		finishedAt := now.Add(2 * time.Minute)
		scheduledAt := now.Add(-time.Hour)

		job := Job{
			ID:          100,
			TypeName:    "metrics-aggregation",
			Status:      "completed",
			Queue:       "high-priority",
			Data:        `{"config": "test"}`,
			Error:       "",
			Attempt:     3,
			CreatedAt:   now,
			StartedAt:   startedAt,
			FinishedAt:  finishedAt,
			ScheduledAt: scheduledAt,
		}

		// Verify all fields
		assert.Equal(t, int64(100), job.ID)
		assert.Equal(t, "metrics-aggregation", job.TypeName)
		assert.Equal(t, "completed", job.Status)
		assert.Equal(t, "high-priority", job.Queue)
		assert.Equal(t, `{"config": "test"}`, job.Data)
		assert.Equal(t, "", job.Error)
		assert.Equal(t, int32(3), job.Attempt)
		assert.Equal(t, now, job.CreatedAt)
		assert.Equal(t, startedAt, job.StartedAt)
		assert.Equal(t, finishedAt, job.FinishedAt)
		assert.Equal(t, scheduledAt, job.ScheduledAt)
	})

	t.Run("minimal initialization", func(t *testing.T) {
		// Test with minimal required fields
		job := Job{
			ID:       200,
			TypeName: "data-processing",
			Status:   "pending",
			Queue:    "default",
		}

		assert.Equal(t, int64(200), job.ID)
		assert.Equal(t, "data-processing", job.TypeName)
		assert.Equal(t, "pending", job.Status)
		assert.Equal(t, "default", job.Queue)

		// Verify default values
		assert.Equal(t, "", job.Data)
		assert.Equal(t, "", job.Error)
		assert.Equal(t, int32(0), job.Attempt)
		assert.True(t, job.CreatedAt.IsZero())
		assert.True(t, job.StartedAt.IsZero())
		assert.True(t, job.FinishedAt.IsZero())
		assert.True(t, job.ScheduledAt.IsZero())
	})

	t.Run("error state", func(t *testing.T) {
		// Test job in error state
		now := time.Now().UTC().Truncate(time.Millisecond)

		job := Job{
			ID:        300,
			TypeName:  "failing-job",
			Status:    "failed",
			Queue:     "retry",
			Data:      `{"retry": true}`,
			Error:     "Database connection timeout",
			Attempt:   5,
			CreatedAt: now,
			StartedAt: now.Add(time.Minute),
		}

		assert.Equal(t, int64(300), job.ID)
		assert.Equal(t, "failing-job", job.TypeName)
		assert.Equal(t, "failed", job.Status)
		assert.Equal(t, "retry", job.Queue)
		assert.Equal(t, `{"retry": true}`, job.Data)
		assert.Equal(t, "Database connection timeout", job.Error)
		assert.Equal(t, int32(5), job.Attempt)
		assert.Equal(t, now, job.CreatedAt)
		assert.Equal(t, now.Add(time.Minute), job.StartedAt)
		assert.True(t, job.FinishedAt.IsZero())  // Not finished yet
		assert.True(t, job.ScheduledAt.IsZero()) // Not scheduled
	})
}

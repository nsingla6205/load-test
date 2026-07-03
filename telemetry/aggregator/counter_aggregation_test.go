package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// TestCalculateCounterDeltaWithAggregatedHistory tests the new method for calculating counter delta with cached history
func TestCalculateCounterDeltaWithAggregatedHistory(t *testing.T) {
	processor := &BillingProvider{}

	ctx := context.Background()
	logger := log.NewLogger()
	now := time.Now()
	aggregationStartTime := now

	resourceKey := ResourceKey{
		ResourceName: "test-volume",
		ConsumerID:   "customer-123",
		ResourceType: metadata.Volume,
	}
	resourceUUID := "test-volume-uuid"

	t.Run("No counter cache - fallback to standard delta", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 100},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 200},
			{Timestamp: aggregationStartTime.Add(30 * time.Minute), Quantity: 150}, // Counter reset (anomalous dip, will be skipped)
		}

		// Empty cache
		counterCache := make(CounterAggregationCache)

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.AllocatedSize, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Should calculate delta without previous value: (200-100) = 100, the dip to 150 is skipped as anomalous
		expectedDelta := float64(100)
		assert.Equal(t, expectedDelta, res.billed)
	})

	t.Run("With cached counter value - enhanced delta calculation", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 120},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 170},
		}

		// Cache with previous counter value using CounterAggregationCacheResourceKey as key
		lastCounterValue := float64(100)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.AllocatedSize,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.AllocatedSize, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Should calculate delta with previous value: (120-100) + (170-120) = 20 + 50 = 70
		expectedDelta := float64(70)
		assert.Equal(t, expectedDelta, res.billed)
	})

	t.Run("Empty data points", func(t *testing.T) {
		dataPoints := []common.DataPoint{}
		counterCache := make(CounterAggregationCache)

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.AllocatedSize, aggregationStartTime, counterCache, resourceUUID, logger, false)

		assert.Equal(t, float64(0), res.billed)
	})

	t.Run("CBS backup transfer cache miss prepends zero baseline", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 272043},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 387575},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 387575},
		}

		counterCache := make(CounterAggregationCache)

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Zero baseline prepended: [0, 272043, 387575, 387575]
		// Delta = 272043 + 115532 + 0 = 387575
		assert.InDelta(t, 387575.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 387575.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache hit uses cached value not zero", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 503159},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 618707},
		}

		lastCounterValue := float64(387575)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Cached 387575 prepended: [387575, 503159, 618707]
		// Delta = 115584 + 115548 = 231132
		assert.InDelta(t, 231132.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 618707.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache miss single data point", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 272043},
		}

		counterCache := make(CounterAggregationCache)

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Zero baseline prepended: [0, 272043] → delta = 272043
		assert.InDelta(t, 272043.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 272043.0, *res.lastCounter)
	})

	t.Run("Non-CBS metric cache miss does not prepend zero baseline", func(t *testing.T) {
		// Same data as CBS test above but with a non-CBS metric type.
		// Without zero baseline: [272043, 387575] → delta = 115532
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 272043},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 387575},
		}

		counterCache := make(CounterAggregationCache)

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.CoolTierDataReadSizeRaw, aggregationStartTime, counterCache, resourceUUID, logger, false)

		assert.InDelta(t, 115532.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 387575.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache hit with cross-window reset", func(t *testing.T) {
		// Previous window cached at 618707. New data starts lower due to a new backup.
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 150000},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 300000},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 450000},
		}

		lastCounterValue := float64(618707)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Cached 618707 prepended: [618707, 150000, 300000, 450000]
		// reset(150000) + (300000-150000) + (450000-300000) = 150000 + 150000 + 150000 = 450000
		assert.InDelta(t, 450000.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 450000.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache miss with all zero data points", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 0},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 0},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 0},
		}

		counterCache := make(CounterAggregationCache)

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Zero baseline prepended: [0, 0, 0, 0] → all flat, delta = 0
		assert.InDelta(t, 0.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 0.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache hit with reset to zero in data", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 500000},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 0},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 200000},
		}

		lastCounterValue := float64(387575)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Cached 387575 prepended: [387575, 500000, 0, 200000]
		// (500000-387575) + reset(0) + (200000-0) = 112425 + 0 + 200000 = 312425
		assert.InDelta(t, 312425.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 200000.0, *res.lastCounter)
	})

	t.Run("CBS backup transfer cache miss empty data points", func(t *testing.T) {
		dataPoints := []common.DataPoint{}
		counterCache := make(CounterAggregationCache)

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Empty data returns zero, no zero baseline prepended for empty input
		assert.Equal(t, 0.0, res.billed)
		assert.Nil(t, res.lastCounter)
	})

	t.Run("CBS backup transfer cache hit with multiple resets in window", func(t *testing.T) {
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(5 * time.Minute), Quantity: 500000},
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 300000}, // reset
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 400000},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 100000}, // reset
			{Timestamp: aggregationStartTime.Add(25 * time.Minute), Quantity: 250000},
		}

		lastCounterValue := float64(400000)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		backupResourceKey := ResourceKey{
			ResourceName: "test-backup",
			ConsumerID:   "customer-123",
			ResourceType: metadata.Backup,
		}
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, backupResourceKey, dataPoints, metadata.CbsCrossRegionVolumeBackupTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, false)

		// Cached 400000 prepended: [400000, 500000, 300000, 400000, 100000, 250000]
		// (500000-400000) + reset(300000) + (400000-300000) + reset(100000) + (250000-100000)
		// = 100000 + 300000 + 100000 + 100000 + 150000 = 750000
		assert.InDelta(t, 750000.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.Equal(t, 250000.0, *res.lastCounter)
	})

	t.Run("Hybrid replication splits at first positive update sample (initialize rolled into skipped prefix)", func(t *testing.T) {
		// New behavior: split point is the first positive sample with transfer_type == "update".
		// Positive initialize samples that precede the first update are now part of the skipped
		// (baseline) prefix instead of triggering the split.
		initTT := TransferTypeInitial
		updateTT := TransferTypeUpdate
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(5 * time.Minute), Quantity: 0, TransferType: nil},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 100, TransferType: &initTT},
			{Timestamp: aggregationStartTime.Add(25 * time.Minute), Quantity: 160, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(35 * time.Minute), Quantity: 220, TransferType: &updateTT},
		}
		counterCache := make(CounterAggregationCache)
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)
		// Cache miss for hybrid replication now also prepends a zero baseline (same as CBS path).
		// pointsForDelta becomes [0, 0, 100, 160, 220].
		// Split at first positive update (Quantity=160) → prefix=[0,0,100,160], suffix=[160,220].
		// Prefix delta = 0 + 0 + 100 + 60 = 160; suffix delta = 60.
		assert.InDelta(t, 160.0, res.skippedQty, 0.001)
		assert.InDelta(t, 60.0, res.billed, 0.001)
		require.NotNil(t, res.skippedSegmentEndCounter)
		assert.InDelta(t, 160.0, *res.skippedSegmentEndCounter, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 220.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.segmentSplitAt)
		assert.True(t, res.segmentSplitAt.Equal(aggregationStartTime.Add(25*time.Minute)))
		// Split-point sample (suffix[0]) carries transfer_type=update.
		require.NotNil(t, res.skippedSegmentEndTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.skippedSegmentEndTransferType)
		// Last sample in window carries transfer_type=update.
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication cache miss prepends zero baseline so first window counts full transfer", func(t *testing.T) {
		// Regression: cache miss for hybrid replication should now use a zero baseline so the
		// initial transfer bytes are accounted for in the skipped (baseline) segment rather than
		// being dropped. Before the change, only CBS cross-region backup got this treatment.
		updateTT := TransferTypeUpdate
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(5 * time.Minute), Quantity: 500, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 800, TransferType: &updateTT},
		}
		counterCache := make(CounterAggregationCache)
		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Zero baseline prepended → [0, 500, 800].
		// Split at first positive update (index 1 of pointsForDelta) → prefix=[0,500], suffix=[500,800].
		// Skipped delta = 500, billed delta = 300.
		assert.InDelta(t, 500.0, res.skippedQty, 0.001)
		assert.InDelta(t, 300.0, res.billed, 0.001)
		require.NotNil(t, res.skippedSegmentEndCounter)
		assert.InDelta(t, 500.0, *res.skippedSegmentEndCounter, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 800.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.skippedSegmentEndTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.skippedSegmentEndTransferType)
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication cache hit with positive cached value bypasses split (plain CounterDelta over cached baseline)", func(t *testing.T) {
		// When a prior counter value exists in the cache and is > 0, the synthetic prepended
		// point has TransferType=nil, which triggers the second guard in
		// replicationCounterPointsSplitTillFirstUpdate (`points[0].Quantity > 0 && TransferType == nil`),
		// short-circuiting it. The function then falls back to plain CounterDelta over
		// [cachedValue, ...dataPoints], i.e. baseline-skipping no longer applies after the very
		// first window.
		updateTT := TransferTypeUpdate
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(5 * time.Minute), Quantity: 1200, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(15 * time.Minute), Quantity: 1500, TransferType: &updateTT},
		}

		lastCounterValue := float64(1000)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &lastCounterValue},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Cached 1000 prepended → [1000, 1200, 1500]. Split bypassed (guard 2 hits).
		// Plain CounterDelta: (1200-1000) + (1500-1200) = 200 + 300 = 500.
		assert.InDelta(t, 500.0, res.billed, 0.001)
		assert.InDelta(t, 0.0, res.skippedQty, 0.001)
		assert.Nil(t, res.skippedSegmentEndCounter)
		assert.Nil(t, res.segmentSplitAt)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 1500.0, *res.lastCounter, 0.001)
		// No split happened → skipped transfer type stays nil; billable last_transfer_type
		// comes from the last data point in the window.
		assert.Nil(t, res.skippedSegmentEndTransferType)
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication with single positive update sample accounts the full transfer as baseline", func(t *testing.T) {
		// Single data point case (still len>=2 after zero-baseline prepend): the only positive
		// update sample becomes the split point. The full transfer is captured in the skipped
		// (baseline) segment and the billable suffix has no delta to bill.
		updateTT := TransferTypeUpdate
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(5 * time.Minute), Quantity: 750, TransferType: &updateTT},
		}
		counterCache := make(CounterAggregationCache)

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Zero baseline prepended → [0, 750]. Split at index 1 → prefix=[0,750], suffix=[750].
		// Skipped delta = 750, billed delta = 0 (suffix has a single point).
		assert.InDelta(t, 750.0, res.skippedQty, 0.001)
		assert.InDelta(t, 0.0, res.billed, 0.001)
		require.NotNil(t, res.skippedSegmentEndCounter)
		assert.InDelta(t, 750.0, *res.skippedSegmentEndCounter, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 750.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.segmentSplitAt)
		assert.True(t, res.segmentSplitAt.Equal(aggregationStartTime.Add(5*time.Minute)))
		// Split point and last sample are both the single positive update sample.
		require.NotNil(t, res.skippedSegmentEndTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.skippedSegmentEndTransferType)
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication with empty data points returns zero without panic", func(t *testing.T) {
		// len(dataPoints) == 0 short-circuits before the split logic, so the panic-prone
		// replicationCounterPointsSplitTillFirstUpdate path is not hit at all.
		counterCache := make(CounterAggregationCache)

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, []common.DataPoint{}, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		assert.InDelta(t, 0.0, res.billed, 0.001)
		assert.InDelta(t, 0.0, res.skippedQty, 0.001)
		assert.Nil(t, res.lastCounter)
		assert.Nil(t, res.skippedSegmentEndCounter)
		assert.Nil(t, res.segmentSplitAt)
	})

	t.Run("Hybrid replication cache hit with cached initialize TransferType + no update in window treats whole window as baseline", func(t *testing.T) {
		// Cross-window baseline continuation: prior cycle ended in baseline (LastTransferType=
		// "initialize" cached), this cycle still has only initialize samples. The hardened split
		// function returns split=false, and the caller's in-baseline branch treats the entire
		// window as a non-billable baseline segment instead of falling through to plain
		// CounterDelta (which would incorrectly bill the bytes).
		initTT := TransferTypeInitial
		cachedCounter := float64(200)
		cachedTT := initTT
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &cachedCounter, LastTransferType: &cachedTT},
		}
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 300, TransferType: &initTT},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 400, TransferType: &initTT},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Synthetic [200, init] + [300, init, 400, init] → no update found.
		// Caller treats the whole window as baseline: skippedQty = delta over all = 200; billed = 0.
		assert.InDelta(t, 0.0, res.billed, 0.001)
		assert.InDelta(t, 200.0, res.skippedQty, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 400.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.skippedSegmentEndCounter)
		assert.InDelta(t, 400.0, *res.skippedSegmentEndCounter, 0.001)
		// Both transfer types track the last data point (still "initialize") so the cache hands
		// off baseline state to the next cycle.
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeInitial, *res.lastTransferType)
		require.NotNil(t, res.skippedSegmentEndTransferType)
		assert.Equal(t, TransferTypeInitial, *res.skippedSegmentEndTransferType)
		// No within-window split, so segmentSplitAt stays nil → append helper keeps [start, end].
		assert.Nil(t, res.segmentSplitAt)
	})

	t.Run("Hybrid replication cache hit with cached initialize TransferType + update appears mid-window splits at the update sample", func(t *testing.T) {
		// Cross-window cutover: prior cycle was baseline, current cycle contains the cutover.
		// Split lands on the first positive "update" sample; the prefix (synthetic + initialize
		// samples + cutover sample) becomes the non-billable baseline; the suffix bills.
		initTT := TransferTypeInitial
		updateTT := TransferTypeUpdate
		cachedCounter := float64(200)
		cachedTT := initTT
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &cachedCounter, LastTransferType: &cachedTT},
		}
		cutoverAt := aggregationStartTime.Add(20 * time.Minute)
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 300, TransferType: &initTT},
			{Timestamp: cutoverAt, Quantity: 400, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(30 * time.Minute), Quantity: 550, TransferType: &updateTT},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Synthetic [200, init] + [300:init, 400:update, 550:update].
		// Split at index 2 (first positive update) → prefix=[200,300,400], suffix=[400,550].
		// Skipped delta = 200; billed delta = 150.
		assert.InDelta(t, 200.0, res.skippedQty, 0.001)
		assert.InDelta(t, 150.0, res.billed, 0.001)
		require.NotNil(t, res.skippedSegmentEndCounter)
		assert.InDelta(t, 400.0, *res.skippedSegmentEndCounter, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 550.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.segmentSplitAt)
		assert.True(t, res.segmentSplitAt.Equal(cutoverAt))
		// Skipped baseline row carries the cutover sample's transfer_type (update);
		// billable row carries the last sample's transfer_type (update).
		require.NotNil(t, res.skippedSegmentEndTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.skippedSegmentEndTransferType)
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication cache hit with cached update TransferType bills full delta with no baseline row", func(t *testing.T) {
		// Past-baseline path: cached LastTransferType="update" → synthetic point carries TT=update
		// → split function finds the synthetic point itself as splitIndex=0 → prefix=[synthetic]
		// (zero delta), suffix bills the full window's delta. No non-billable baseline row.
		updateTT := TransferTypeUpdate
		cachedCounter := float64(200)
		cachedTT := updateTT
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &cachedCounter, LastTransferType: &cachedTT},
		}
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 300, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 400, TransferType: &updateTT},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Synthetic [200, update] + [300:update, 400:update]. splitIndex=0.
		// prefix=[200] (delta 0), suffix=[200,300,400] (delta 200).
		assert.InDelta(t, 0.0, res.skippedQty, 0.001)
		assert.InDelta(t, 200.0, res.billed, 0.001)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 400.0, *res.lastCounter, 0.001)
		require.NotNil(t, res.lastTransferType)
		assert.Equal(t, TransferTypeUpdate, *res.lastTransferType)
	})

	t.Run("Hybrid replication cache hit with legacy nil TransferType falls back to plain CounterDelta", func(t *testing.T) {
		// Legacy row from before LastTransferType existed: cached value has TT=nil. The
		// synthetic prepended point trips guard 2 of the split function (Quantity>0 &&
		// TransferType==nil), so split=false. Because the cached TT is nil, the caller's
		// inBaselineMode returns false and the code falls through to plain CounterDelta,
		// preserving pre-Commit-3 behavior.
		updateTT := TransferTypeUpdate
		cachedCounter := float64(200)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: resourceUUID,
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		counterCache := CounterAggregationCache{
			cacheKey: &CounterAggregationCacheValue{LastCounterValue: &cachedCounter, LastTransferType: nil},
		}
		dataPoints := []common.DataPoint{
			{Timestamp: aggregationStartTime.Add(10 * time.Minute), Quantity: 300, TransferType: &updateTT},
			{Timestamp: aggregationStartTime.Add(20 * time.Minute), Quantity: 400, TransferType: &updateTT},
		}

		res := processor.calculateCounterDeltaWithAggregatedHistory(ctx, resourceKey, dataPoints, metadata.XregionReplicationTotalTransferBytes, aggregationStartTime, counterCache, resourceUUID, logger, true)

		// Plain CounterDelta over [200, 300, 400] = 200.
		assert.InDelta(t, 200.0, res.billed, 0.001)
		assert.InDelta(t, 0.0, res.skippedQty, 0.001)
		assert.Nil(t, res.skippedSegmentEndCounter)
		assert.Nil(t, res.segmentSplitAt)
		require.NotNil(t, res.lastCounter)
		assert.InDelta(t, 400.0, *res.lastCounter, 0.001)
	})
}

// TestPreloadCounterValues tests the bulk counter value fetching
func TestPreloadCounterValues(t *testing.T) {
	mockDB := database.NewMockStorage(t)
	processor := &BillingProvider{
		metricsDB: mockDB,
		config: &common.TelemetryConfig{
			PoolVolumeLabelPageSize: 5000,
		},
	}

	ctx := context.Background()
	logger := log.NewLogger()
	now := time.Now()
	aggregationStartTime := now
	aggregationEndTime := now.Add(1 * time.Hour)

	t.Run("Successfully preload counter values", func(t *testing.T) {
		// Mock aggregated usage records
		counterValue1 := float64(100)
		counterValue2 := float64(200)

		usageRecords := []datamodel2.AggregatedUsage{
			{
				ResourceUUID:     "volume-1",
				ResourceName:     &[]string{"volume-1"}[0],
				VendorCustomerID: &[]string{"customer-123"}[0],
				ResourceType:     string(metadata.Volume),
				MeasuredType:     string(metadata.AllocatedSize),
				AggregationType:  "CounterAggregation",
				LastCounterValue: &counterValue1,
				AggregationEnd:   now.Add(-1 * time.Hour),
			},
			{
				ResourceUUID:     "volume-2",
				ResourceName:     &[]string{"volume-2"}[0],
				VendorCustomerID: &[]string{"customer-456"}[0],
				ResourceType:     string(metadata.Volume),
				MeasuredType:     string(metadata.LogicalSize),
				AggregationType:  "CounterAggregation",
				LastCounterValue: &counterValue2,
				AggregationEnd:   now.Add(-2 * time.Hour),
			},
		}

		mockDB.On("GetLatestAggregatedUsageForAllResources", mock.Anything, "CounterAggregation", mock.Anything, mock.Anything).Return(usageRecords, nil).Once()
		mockDB.On("GetLatestAggregatedUsageForAllResources", mock.Anything, "CounterAggregation", mock.Anything, mock.Anything).Return([]datamodel2.AggregatedUsage{}, nil).Maybe()

		result, err := processor.preloadCounterValues(ctx, aggregationStartTime, aggregationEndTime, logger)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 2, len(result))

		// Check first record - use CounterAggregationCacheResourceKey as key
		cacheKey1 := CounterAggregationCacheResourceKey{
			ResourceUUID: "volume-1",
			MeasuredType: metadata.AllocatedSize,
		}
		assert.Contains(t, result, cacheKey1)
		require.NotNil(t, result[cacheKey1])
		require.NotNil(t, result[cacheKey1].LastCounterValue)
		assert.Equal(t, counterValue1, *result[cacheKey1].LastCounterValue)
		assert.Nil(t, result[cacheKey1].LastTransferType, "DB record has no last_transfer_type set")

		// Check second record - use CounterAggregationCacheResourceKey as key
		cacheKey2 := CounterAggregationCacheResourceKey{
			ResourceUUID: "volume-2",
			MeasuredType: metadata.LogicalSize,
		}
		assert.Contains(t, result, cacheKey2)
		require.NotNil(t, result[cacheKey2])
		require.NotNil(t, result[cacheKey2].LastCounterValue)
		assert.Equal(t, counterValue2, *result[cacheKey2].LastCounterValue)
		assert.Nil(t, result[cacheKey2].LastTransferType, "DB record has no last_transfer_type set")

		mockDB.AssertExpectations(t)
	})

	t.Run("Database error", func(t *testing.T) {
		mockDB2 := database.NewMockStorage(t)
		processor2 := &BillingProvider{
			metricsDB: mockDB2,
			config: &common.TelemetryConfig{
				PoolVolumeLabelPageSize: 5000,
			},
		}

		mockDB2.On("GetLatestAggregatedUsageForAllResources", mock.Anything, "CounterAggregation", mock.Anything, mock.Anything).Return([]datamodel2.AggregatedUsage{}, assert.AnError)

		result, err := processor2.preloadCounterValues(ctx, aggregationStartTime, aggregationEndTime, logger)

		assert.Error(t, err)
		assert.Nil(t, result)
		mockDB2.AssertExpectations(t)
	})

	t.Run("Propagates last_transfer_type from DB record into cache entry", func(t *testing.T) {
		mockDB3 := database.NewMockStorage(t)
		processor3 := &BillingProvider{
			metricsDB: mockDB3,
			config: &common.TelemetryConfig{
				PoolVolumeLabelPageSize: 5000,
			},
		}

		counterValue := float64(512)
		transferType := TransferTypeUpdate
		usageRecords := []datamodel2.AggregatedUsage{
			{
				ResourceUUID:     "replication-1",
				ResourceName:     &[]string{"replication-1"}[0],
				VendorCustomerID: &[]string{"customer-1"}[0],
				ResourceType:     string(metadata.VolumeReplicationRelationship),
				MeasuredType:     string(metadata.XregionReplicationTotalTransferBytes),
				AggregationType:  "CounterAggregation",
				LastCounterValue: &counterValue,
				LastTransferType: &transferType,
				AggregationEnd:   now.Add(-1 * time.Hour),
			},
		}

		mockDB3.On("GetLatestAggregatedUsageForAllResources", mock.Anything, "CounterAggregation", mock.Anything, mock.Anything).Return(usageRecords, nil).Once()
		mockDB3.On("GetLatestAggregatedUsageForAllResources", mock.Anything, "CounterAggregation", mock.Anything, mock.Anything).Return([]datamodel2.AggregatedUsage{}, nil).Maybe()

		result, err := processor3.preloadCounterValues(ctx, aggregationStartTime, aggregationEndTime, logger)

		require.NoError(t, err)
		require.NotNil(t, result)
		cacheKey := CounterAggregationCacheResourceKey{
			ResourceUUID: "replication-1",
			MeasuredType: metadata.XregionReplicationTotalTransferBytes,
		}
		require.Contains(t, result, cacheKey)
		entry := result[cacheKey]
		require.NotNil(t, entry)
		require.NotNil(t, entry.LastCounterValue)
		assert.Equal(t, counterValue, *entry.LastCounterValue)
		require.NotNil(t, entry.LastTransferType)
		assert.Equal(t, TransferTypeUpdate, *entry.LastTransferType)

		mockDB3.AssertExpectations(t)
	})
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/connection"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	metricsdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	telemetrydatamodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const (
	charset = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// PrecomputedRandomValues holds pools of precomputed random values for optimization
type PrecomputedRandomValues struct {
	RandomStrings8  []string  // Precomputed random strings of length 8
	RandomStrings10 []string  // Precomputed random strings of length 10
	RandomStrings12 []string  // Precomputed random strings of length 12
	UUIDs           []string  // Precomputed UUIDs
	LocationIndices []int     // Precomputed location indices
	RandomFloats    []float64 // Precomputed random floats for probability checks
	RandomInts      []int     // Precomputed random ints for selections
	index10         int       // Current index for RandomStrings10
	index12         int       // Current index for RandomStrings12
	uuidIndex       int       // Current index for UUIDs
	locationIndex   int       // Current index for LocationIndices
	floatIndex      int       // Current index for RandomFloats
	intIndex        int       // Current index for RandomInts
}

// precomputeRandomValues generates pools of random values upfront
func precomputeRandomValues(count int) *PrecomputedRandomValues {
	precomputed := &PrecomputedRandomValues{
		RandomStrings8:  make([]string, count),
		RandomStrings10: make([]string, count),
		RandomStrings12: make([]string, count),
		UUIDs:           make([]string, count),
		LocationIndices: make([]int, count),
		RandomFloats:    make([]float64, count),
		RandomInts:      make([]int, count),
	}

	// Precompute random strings
	for i := 0; i < count; i++ {
		precomputed.RandomStrings8[i] = generateRandomString(8)
		precomputed.RandomStrings10[i] = generateRandomString(10)
		precomputed.RandomStrings12[i] = generateRandomString(12)
		precomputed.UUIDs[i] = utils.RandomUUID()
		precomputed.RandomFloats[i] = rand.Float64()
		precomputed.RandomInts[i] = rand.Intn(1000) // Large range for flexibility
	}

	// Precompute location indices (for 6 locations)
	for i := 0; i < count; i++ {
		precomputed.LocationIndices[i] = rand.Intn(6)
	}

	return precomputed
}

// getRandomString10 returns a precomputed random string of length 10, cycling through the pool
func (p *PrecomputedRandomValues) getRandomString10() string {
	if len(p.RandomStrings10) == 0 {
		return generateRandomString(10)
	}
	str := p.RandomStrings10[p.index10]
	p.index10 = (p.index10 + 1) % len(p.RandomStrings10)
	return str
}

// getRandomString12 returns a precomputed random string of length 12, cycling through the pool
func (p *PrecomputedRandomValues) getRandomString12() string {
	if len(p.RandomStrings12) == 0 {
		return generateRandomString(12)
	}
	str := p.RandomStrings12[p.index12]
	p.index12 = (p.index12 + 1) % len(p.RandomStrings12)
	return str
}

// getUUID returns a precomputed UUID, cycling through the pool
func (p *PrecomputedRandomValues) getUUID() string {
	if len(p.UUIDs) == 0 {
		return utils.RandomUUID()
	}
	uuid := p.UUIDs[p.uuidIndex]
	p.uuidIndex = (p.uuidIndex + 1) % len(p.UUIDs)
	return uuid
}

// getLocationIndex returns a precomputed location index, cycling through the pool
func (p *PrecomputedRandomValues) getLocationIndex(max int) int {
	if len(p.LocationIndices) == 0 {
		return rand.Intn(max)
	}
	idx := p.LocationIndices[p.locationIndex] % max
	p.locationIndex = (p.locationIndex + 1) % len(p.LocationIndices)
	return idx
}

// getRandomFloat returns a precomputed random float, cycling through the pool
func (p *PrecomputedRandomValues) getRandomFloat() float64 {
	if len(p.RandomFloats) == 0 {
		return rand.Float64()
	}
	f := p.RandomFloats[p.floatIndex]
	p.floatIndex = (p.floatIndex + 1) % len(p.RandomFloats)
	return f
}

// getRandomPoolTieringStatus returns a random tiering status for the pool
func (p *PrecomputedRandomValues) getRandomPoolTieringStatus() datamodel.TieringStatus {
	myStrings := []datamodel.TieringStatus{datamodel.TieringStatusPaused, datamodel.TieringStatusPaused, datamodel.TieringStatusPartiallyPaused, datamodel.TieringStatusPartiallyResumed}
	randomIndex := rand.Intn(len(myStrings))
	return myStrings[randomIndex]
}

// getRandomInt returns a precomputed random int, cycling through the pool
func (p *PrecomputedRandomValues) getRandomInt(max int) int {
	if len(p.RandomInts) == 0 {
		return rand.Intn(max)
	}
	idx := p.RandomInts[p.intIndex] % max
	p.intIndex = (p.intIndex + 1) % len(p.RandomInts)
	return idx
}

// generateRandomString generates a random string of given length
func generateRandomString(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// generateBackupLabels generates random labels for a backup (0-30 labels)
func generateBackupLabels() *datamodel.JSONB {
	labelCount := rand.Intn(31) // 0-30 labels
	if labelCount == 0 {
		return nil // Return nil for no labels
	}

	labels := make(datamodel.JSONB)
	labelKeys := []string{
		"environment", "team", "project", "application", "owner", "cost-center",
		"department", "region", "zone", "tier", "backup-policy", "retention",
		"compliance", "security", "classification", "data-type", "lifecycle",
		"version", "release", "build", "deployment", "cluster", "namespace",
		"service", "component", "module", "feature", "priority", "criticality",
		"backup-type", "source", "destination",
	}

	// Generate random label key-value pairs
	for i := 0; i < labelCount && i < len(labelKeys); i++ {
		key := labelKeys[rand.Intn(len(labelKeys))]
		// Make sure we don't duplicate keys
		if _, exists := labels[key]; exists {
			// If key already exists, use a numbered variant
			key = fmt.Sprintf("%s-%d", key, i+1)
		}
		// Generate random value
		value := generateRandomString(8)
		labels[key] = value
	}

	// If we need more labels than available keys, add numbered labels
	for len(labels) < labelCount {
		key := fmt.Sprintf("label-%d", len(labels)+1)
		value := generateRandomString(8)
		labels[key] = value
	}

	return &labels
}

// loadEnvFile loads environment variables from a .env file
func loadEnvFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open env file %s: %w", filename, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			// Log error but don't fail the function
			_ = closeErr
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE format
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
				value = value[1 : len(value)-1]
			}
			// Only set if not already set in environment
			if os.Getenv(key) == "" {
				if err := os.Setenv(key, value); err != nil {
					// Log error but continue processing other variables
					_ = err
				}
			}
		}
	}

	return scanner.Err()
}

// ResourceInfo holds information about a resource
type ResourceInfo struct {
	Name           string
	UUID           string
	ExternalUUID   string
	DeploymentName string
	ConsumerID     string
	Location       string
	ResourceType   metadata.ResourceType
	IsRegionalHA   bool
}

// generateQuantity generates a simulated quantity value based on measured type
func generateQuantity(measuredType metadata.MeasuredType, baseValue float64) float64 {
	// Add some random variation (±10%)
	variation := 1.0 + (rand.Float64()*0.2 - 0.1) // ±10%

	switch measuredType {
	case metadata.PoolAllocatedSize, metadata.AllocatedSize:
		// Size in bytes - typically in GB range
		return baseValue * 1024 * 1024 * 1024 * variation // GB to bytes
	case metadata.AllocatedUsed:
		// Used size in bytes
		return baseValue * 1024 * 1024 * 1024 * 0.7 * variation // 70% of allocated
	case metadata.LogicalSize:
		// Logical size in bytes
		return baseValue * 1024 * 1024 * 1024 * 0.8 * variation // 80% of allocated
	case metadata.SnapshotSize:
		// Snapshot size in bytes
		return baseValue * 1024 * 1024 * 1024 * 0.1 * variation // 10% of allocated
	case metadata.PoolTotalThroughputMibps:
		// Throughput in MiB/s
		return baseValue * variation // MiB/s
	case metadata.PoolTotalIops:
		// IOPS
		return baseValue * variation // IOPS
	case metadata.BackupLogicalSize:
		// Backup size in bytes
		return baseValue * 1024 * 1024 * 1024 * 0.5 * variation // 50% of logical size
	case metadata.BackupEnabledVolumeAllocatedSize:
		// Backup enabled volume allocated size
		return baseValue * 1024 * 1024 * 1024 * 0.6 * variation
	case metadata.XregionReplicationTotalTransferBytes:
		// Replication transfer bytes
		return baseValue * 1024 * 1024 * variation // MB to bytes
	case metadata.VolumeAllocatedThroughput:
		// Volume throughput in MiB/s
		return baseValue * variation
	default:
		return baseValue * variation
	}
}

// generateMetricsForResource generates hydrated metrics for a resource and ALL measure types
func generateMetricsForResource(resource ResourceInfo, timestamp time.Time, jobDefs map[metadata.CombinedKeyResourceTypeMeasuredType]common.AggregationJobDefinition) []telemetrydatamodel.HydratedMetrics {
	var metrics []telemetrydatamodel.HydratedMetrics

	// Determine the actual resource type (handle RegionalHA variants)
	actualResourceType := resource.ResourceType
	if resource.IsRegionalHA {
		if resource.ResourceType == metadata.VolumePool {
			actualResourceType = metadata.VolumePoolRegionalHA
		} else if resource.ResourceType == metadata.Volume {
			actualResourceType = metadata.VolumeRegionalHA
		}
	}

	// Define all measure types for each resource type
	var measureTypes []metadata.MeasuredType
	switch actualResourceType {
	case metadata.VolumePool, metadata.VolumePoolRegionalHA:
		measureTypes = []metadata.MeasuredType{
			metadata.PoolAllocatedSize,
			metadata.AllocatedUsed,
			metadata.PoolTotalThroughputMibps,
			metadata.PoolTotalIops,
		}
		// Also add backup-related metrics if applicable
		if actualResourceType == metadata.VolumePoolRegionalHA {
			measureTypes = append(measureTypes, metadata.BackupEnabledVolumeAllocatedSize)
		}
	case metadata.Volume, metadata.VolumeRegionalHA:
		measureTypes = []metadata.MeasuredType{
			metadata.AllocatedSize,
			metadata.LogicalSize,
			metadata.SnapshotSize,
			metadata.BackupEnabledVolumeAllocatedSize,
			metadata.VolumeAllocatedThroughput,
		}
	case metadata.Backup:
		measureTypes = []metadata.MeasuredType{
			metadata.BackupLogicalSize,
			metadata.BackupEnabledVolumeAllocatedSize,
		}
	case metadata.VolumeReplicationRelationship:
		measureTypes = []metadata.MeasuredType{
			metadata.XregionReplicationTotalTransferBytes,
		}
	default:
		// For unknown types, use job definitions
		for key, jobDef := range jobDefs {
			if key.ResourceType == actualResourceType {
				measureTypes = append(measureTypes, jobDef.MeasuredType)
			}
		}
	}

	// Generate metrics for all measure types for this resource type
	for _, measuredType := range measureTypes {
		// Generate base value (random between 10-1000 GB for size metrics, 100-10000 for IOPS/throughput)
		var baseValue float64
		if measuredType == metadata.PoolTotalIops || measuredType == metadata.PoolTotalThroughputMibps || measuredType == metadata.VolumeAllocatedThroughput {
			baseValue = 100 + rand.Float64()*9900 // 100-10000
		} else {
			baseValue = 10 + rand.Float64()*990 // 10-1000 GB
		}

		quantity := generateQuantity(measuredType, baseValue)

		// Use resource name for VOLUME_POOL, VOLUME, and BACKUP, ExternalUUID for replications, UUID for others
		resourceName := resource.UUID
		if actualResourceType == metadata.VolumePool || actualResourceType == metadata.VolumePoolRegionalHA ||
			actualResourceType == metadata.Volume || actualResourceType == metadata.VolumeRegionalHA ||
			actualResourceType == metadata.Backup {
			// Use resource name from VCP database for pools, volumes, and backups (volume name for backups)
			resourceName = resource.Name
		} else if actualResourceType == metadata.VolumeReplicationRelationship && resource.ExternalUUID != "" {
			// Use ExternalUUID for replications
			resourceName = resource.ExternalUUID
		}

		metric := telemetrydatamodel.HydratedMetrics{
			MetricTimestamp: timestamp,
			MeasuredType:    metadata.MeasuredTypeValue(measuredType),
			ResourceType:    metadata.ResourceTypeValue(actualResourceType),
			Quantity:        quantity,
			ResourceName:    resourceName,
			ConsumerID:      resource.ConsumerID,
			Location:        resource.Location,
			DeploymentName:  resource.DeploymentName,
			Metadata:        []byte("{}"), // Empty metadata for simulated data
		}

		metrics = append(metrics, metric)
	}

	return metrics
}

// PoolInfo holds information about a pool for volume distribution
type PoolInfo struct {
	Name           string
	DeploymentName string
	AccountID      string
	Location       string
	ResourceType   metadata.ResourceType
	IsRegionalHA   bool
}

// generateResources generates simulated resources
func generateResources(accountCount, poolCount, volumeCount, backupCount, replicationCount int, logger log.Logger) []ResourceInfo {
	var resources []ResourceInfo

	// Predefined locations for variety
	locations := []string{"us-central1", "us-east1", "us-west1", "europe-west1", "asia-east1", "asia-southeast1"}

	// Precompute random values for optimization
	// Estimate total resources needed: accounts + pools + volumes + backups + replications
	totalResources := accountCount + poolCount + volumeCount + backupCount + replicationCount
	// Add buffer for deployment name generation and other uses
	precomputed := precomputeRandomValues(totalResources + 1000)

	// Create accounts first
	logger.Infof("Creating %d accounts...", accountCount)
	accounts := make([]string, accountCount)
	for i := 0; i < accountCount; i++ {
		accounts[i] = fmt.Sprintf("account-%s", precomputed.getRandomString12())
	}

	// Track unique deployment names for pools
	poolDeploymentNames := make(map[string]bool)

	// Track pools for volume distribution
	var pools []PoolInfo

	// Distribute pools across accounts (each account can have 0-50 pools)
	logger.Infof("Distributing %d pools across %d accounts (0-50 pools per account)...", poolCount, accountCount)

	// Track pools per account
	accountPoolCounts := make(map[string]int)
	for _, accountID := range accounts {
		accountPoolCounts[accountID] = 0
	}

	poolIndex := 0
	for poolIndex < poolCount {
		// Find accounts that can still have more pools (less than 50)
		availableAccounts := make([]string, 0)
		for accountID, count := range accountPoolCounts {
			if count < 50 {
				availableAccounts = append(availableAccounts, accountID)
			}
		}

		// If no accounts available, break (shouldn't happen if poolCount <= accountCount * 50)
		if len(availableAccounts) == 0 {
			logger.Warnf("All accounts have reached maximum pools (50), but %d pools remaining", poolCount-poolIndex)
			break
		}

		// Pick a random account from available accounts
		accountID := availableAccounts[precomputed.getRandomInt(len(availableAccounts))]

		// Determine how many pools this account will have (0-50, but ensure we don't exceed total poolCount)
		maxPoolsForAccount := 50 - accountPoolCounts[accountID]
		remainingPools := poolCount - poolIndex
		if remainingPools < maxPoolsForAccount {
			maxPoolsForAccount = remainingPools
		}

		// Random number of pools for this account (0 to maxPoolsForAccount)
		poolsForAccount := precomputed.getRandomInt(maxPoolsForAccount + 1)
		if poolsForAccount > remainingPools {
			poolsForAccount = remainingPools
		}

		// Generate pools for this account
		for j := 0; j < poolsForAccount && poolIndex < poolCount; j++ {
			isRegionalHA := precomputed.getRandomFloat() < 0.2 // 20% are RegionalHA
			resourceType := metadata.VolumePool
			if isRegionalHA {
				resourceType = metadata.VolumePoolRegionalHA
			}

			// Generate unique deployment name with format "gcnv_" for pools
			var deploymentName string
			for {
				deploymentName = fmt.Sprintf("gcnv_%s", precomputed.getRandomString10())
				if !poolDeploymentNames[deploymentName] {
					poolDeploymentNames[deploymentName] = true
					break
				}
			}

			location := locations[precomputed.getLocationIndex(len(locations))]
			poolName := fmt.Sprintf("pool-%d", poolIndex+1)

			poolInfo := PoolInfo{
				Name:           poolName,
				DeploymentName: deploymentName,
				AccountID:      accountID,
				Location:       location,
				ResourceType:   resourceType,
				IsRegionalHA:   isRegionalHA,
			}
			pools = append(pools, poolInfo)

			resources = append(resources, ResourceInfo{
				Name:           poolName,
				UUID:           precomputed.getUUID(),
				DeploymentName: deploymentName,
				ConsumerID:     accountID,
				Location:       location,
				ResourceType:   resourceType,
				IsRegionalHA:   isRegionalHA,
			})

			// Increment pool count for this account
			accountPoolCounts[accountID]++
			poolIndex++
		}
	}

	logger.Infof("Generated %d pools distributed across %d accounts", len(pools), accountCount)

	// Distribute volumes across pools (each pool can have 0-500 volumes)
	logger.Infof("Distributing %d volumes across %d pools (0-500 volumes per pool)...", volumeCount, len(pools))

	// Track volumes per pool
	poolVolumeCounts := make(map[string]int)
	for _, pool := range pools {
		poolVolumeCounts[pool.Name] = 0
	}

	volumeIndex := 0
	for volumeIndex < volumeCount {
		if len(pools) == 0 {
			// No pools available, cannot create volumes without pools
			logger.Warnf("No pools available, cannot create volumes. Skipping %d volumes", volumeCount-volumeIndex)
			break
		}

		// Find pools that can still have more volumes (less than 500)
		availablePools := make([]PoolInfo, 0)
		for _, pool := range pools {
			if poolVolumeCounts[pool.Name] < 500 {
				availablePools = append(availablePools, pool)
			}
		}

		// If no pools available, break (shouldn't happen if volumeCount <= len(pools) * 500)
		if len(availablePools) == 0 {
			logger.Warnf("All pools have reached maximum volumes (500), but %d volumes remaining", volumeCount-volumeIndex)
			break
		}

		// Pick a random pool from available pools
		pool := availablePools[precomputed.getRandomInt(len(availablePools))]

		// Determine how many volumes this pool will have (0-500, but ensure we don't exceed total volumeCount)
		maxVolumesForPool := 500 - poolVolumeCounts[pool.Name]
		remainingVolumes := volumeCount - volumeIndex
		if remainingVolumes < maxVolumesForPool {
			maxVolumesForPool = remainingVolumes
		}

		// Random number of volumes for this pool (0 to maxVolumesForPool)
		volumesForPool := precomputed.getRandomInt(maxVolumesForPool + 1)
		if volumesForPool > remainingVolumes {
			volumesForPool = remainingVolumes
		}

		// Generate volumes for this pool
		for j := 0; j < volumesForPool && volumeIndex < volumeCount; j++ {
			isRegionalHA := precomputed.getRandomFloat() < 0.15 // 15% are RegionalHA
			resourceType := metadata.Volume
			if isRegionalHA {
				resourceType = metadata.VolumeRegionalHA
			}

			resources = append(resources, ResourceInfo{
				Name:           fmt.Sprintf("volume-%d", volumeIndex+1),
				UUID:           precomputed.getUUID(),
				DeploymentName: pool.DeploymentName,
				ConsumerID:     pool.AccountID,
				Location:       pool.Location,
				ResourceType:   resourceType,
				IsRegionalHA:   isRegionalHA,
			})

			// Increment volume count for this pool
			poolVolumeCounts[pool.Name]++
			volumeIndex++
		}
	}

	logger.Infof("Generated %d volumes distributed across %d pools", volumeCount, len(pools))

	logger.Infof("Generating %d backups...", backupCount)
	// Generate backups - assign to random accounts
	for i := 0; i < backupCount; i++ {
		// Pick a random account
		accountID := accounts[precomputed.getRandomInt(len(accounts))]

		// Use account name as deployment name for backups
		deploymentName := accountID

		resources = append(resources, ResourceInfo{
			Name:           fmt.Sprintf("backup-%d", i+1),
			UUID:           precomputed.getUUID(),
			DeploymentName: deploymentName,
			ConsumerID:     accountID,
			Location:       locations[precomputed.getLocationIndex(len(locations))],
			ResourceType:   metadata.Backup,
			IsRegionalHA:   false,
		})
	}

	logger.Infof("Generating %d replication relationships...", replicationCount)
	// Generate replication relationships - assign to pools (use pool's deployment name)
	for i := 0; i < replicationCount; i++ {
		// Pick a random pool to associate the replication with
		if len(pools) == 0 {
			logger.Warn("No pools available for replications, skipping")
			break
		}
		pool := pools[precomputed.getRandomInt(len(pools))]

		// Use pool's deployment name for replications
		deploymentName := pool.DeploymentName

		resources = append(resources, ResourceInfo{
			Name:           fmt.Sprintf("replication-%d", i+1),
			UUID:           precomputed.getUUID(),
			DeploymentName: deploymentName,
			ConsumerID:     pool.AccountID,
			Location:       pool.Location,
			ResourceType:   metadata.VolumeReplicationRelationship,
			IsRegionalHA:   false,
		})
	}

	logger.Infof("Generated %d total resources (pools: %d, volumes: %d, backups: %d, replications: %d) across %d accounts",
		len(resources), poolCount, volumeCount, backupCount, replicationCount, accountCount)

	return resources
}

// getResourcesFromVCP fetches all resources from VCP database
func getResourcesFromVCP(ctx context.Context, vcpDB database.Storage, logger log.Logger) ([]ResourceInfo, error) {
	var resources []ResourceInfo

	// Predefined locations for variety
	locations := []string{"us-central1", "us-east1", "us-west1", "europe-west1", "asia-east1", "asia-southeast1"}

	// Precompute random values for optimization (estimate based on typical resource counts)
	// This is used for random location selection when deployment name is not available
	precomputed := precomputeRandomValues(1000)

	// Fetch pools with pagination to get all pools
	logger.Info("Fetching pools from VCP database...")
	poolConditions := [][]interface{}{
		{"deleted_at IS NULL"},
	}

	poolOffset := 0
	poolLimit := 10000 // Batch size for pagination
	allPools := make([]*datamodel.PoolView, 0)
	totalPoolsFetched := 0

	for {
		poolPagination := &dbutils.Pagination{
			Offset: poolOffset,
			Limit:  poolLimit,
		}

		pools, err := vcpDB.ListPoolsWithPagination(ctx, poolConditions, poolPagination)
		if err != nil {
			return nil, fmt.Errorf("failed to list pools (offset %d): %w", poolOffset, err)
		}

		// Break if no more pools
		if len(pools) == 0 {
			break
		}

		allPools = append(allPools, pools...)
		totalPoolsFetched += len(pools)

		// If we got fewer pools than the limit, we've reached the end
		if len(pools) < poolLimit {
			break
		}

		// Update offset for next iteration
		poolOffset += poolLimit
	}

	logger.Infof("Fetched %d pools in total", totalPoolsFetched)

	for _, pool := range allPools {
		resourceType := metadata.VolumePool
		isRegionalHA := false
		if pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
			isRegionalHA = true
			resourceType = metadata.VolumePoolRegionalHA
		}

		location := locations[precomputed.getLocationIndex(len(locations))] // Random location
		if pool.DeploymentName != "" {
			// Try to extract region from deployment name if possible
			location = pool.DeploymentName
		}

		accountName := ""
		if pool.Account != nil {
			accountName = pool.Account.Name
		}

		resources = append(resources, ResourceInfo{
			Name:           pool.Name,
			UUID:           pool.UUID,
			DeploymentName: pool.DeploymentName,
			ConsumerID:     accountName,
			Location:       location,
			ResourceType:   resourceType,
			IsRegionalHA:   isRegionalHA,
		})
	}

	logger.Infof("Processed %d pools", len(allPools))

	// Create a map of pool ID to pool deployment name for quick lookup
	poolIDToDeploymentName := make(map[int64]string)
	poolIDToIsRegionalHA := make(map[int64]bool)
	for _, pool := range allPools {
		if pool.ID > 0 {
			poolIDToDeploymentName[pool.ID] = pool.DeploymentName
			if pool.PoolAttributes != nil {
				poolIDToIsRegionalHA[pool.ID] = pool.PoolAttributes.IsRegionalHA
			}
		}
	}

	// Fetch volumes with pagination to get all volumes
	logger.Info("Fetching volumes from VCP database...")
	volumeConditions := [][]interface{}{
		{"deleted_at IS NULL"},
	}

	volumeOffset := 0
	volumeLimit := 10000 // Batch size for pagination
	allVolumes := make([]*datamodel.Volume, 0)
	totalVolumesFetched := 0

	for {
		volumePagination := &dbutils.Pagination{
			Offset: volumeOffset,
			Limit:  volumeLimit,
		}

		// Use ListVolumesWithPagination instead of ListAllVolumes to get pool_id and preload Pool relationship
		volumes, err := vcpDB.ListVolumesWithPagination(ctx, volumeConditions, volumePagination)
		if err != nil {
			return nil, fmt.Errorf("failed to list volumes (offset %d): %w", volumeOffset, err)
		}

		// Break if no more volumes
		if len(volumes) == 0 {
			break
		}

		allVolumes = append(allVolumes, volumes...)
		totalVolumesFetched += len(volumes)

		// If we got fewer volumes than the limit, we've reached the end
		if len(volumes) < volumeLimit {
			break
		}

		// Update offset for next iteration
		volumeOffset += volumeLimit
	}

	logger.Infof("Fetched %d volumes in total", totalVolumesFetched)

	for _, volume := range allVolumes {
		resourceType := metadata.Volume
		isRegionalHA := false
		deploymentName := ""

		// Try to get deployment name from volume.Pool first (if preloaded)
		if volume.Pool != nil {
			if volume.Pool.PoolAttributes != nil && volume.Pool.PoolAttributes.IsRegionalHA {
				isRegionalHA = true
				resourceType = metadata.VolumeRegionalHA
			}
			deploymentName = volume.Pool.DeploymentName
		} else if volume.PoolID > 0 {
			// If Pool relationship is not loaded, look it up in the map
			if depName, exists := poolIDToDeploymentName[volume.PoolID]; exists {
				deploymentName = depName
			}
			// Check if pool is RegionalHA
			if isHA, exists := poolIDToIsRegionalHA[volume.PoolID]; exists && isHA {
				isRegionalHA = true
				resourceType = metadata.VolumeRegionalHA
			}
		}

		location := locations[precomputed.getLocationIndex(len(locations))] // Random location
		if deploymentName != "" {
			location = deploymentName
		}

		accountName := ""
		if volume.Account != nil {
			accountName = volume.Account.Name
		}

		// If deploymentName is still empty, try to get it from pool
		if deploymentName == "" {
			if volume.Pool != nil {
				deploymentName = volume.Pool.DeploymentName
			} else if volume.PoolID > 0 {
				// If Pool relationship is not loaded, look it up in the map
				if depName, exists := poolIDToDeploymentName[volume.PoolID]; exists {
					deploymentName = depName
				}
			}
		}

		resources = append(resources, ResourceInfo{
			Name:           volume.Name,
			UUID:           volume.UUID,
			DeploymentName: deploymentName,
			ConsumerID:     accountName,
			Location:       location,
			ResourceType:   resourceType,
			IsRegionalHA:   isRegionalHA,
		})
	}

	logger.Infof("Processed %d volumes", len(allVolumes))

	// Fetch backups with pagination to get all backups
	logger.Info("Fetching backups from VCP database...")
	backupConditions := [][]interface{}{
		{"deleted_at IS NULL"},
	}

	backupOffset := 0
	backupLimit := 10000 // Batch size for pagination
	allBackups := make([]*datamodel.Backup, 0)
	totalBackupsFetched := 0

	for {
		backupPagination := &dbutils.Pagination{
			Offset: backupOffset,
			Limit:  backupLimit,
		}

		backups, err := vcpDB.GetBackupMetrics(ctx, backupConditions, backupPagination)
		if err != nil {
			logger.Warnf("Failed to list backups (offset %d): %v (continuing without backups)", backupOffset, err)
			break
		}

		// Break if no more backups
		if len(backups) == 0 {
			break
		}

		allBackups = append(allBackups, backups...)
		totalBackupsFetched += len(backups)

		// If we got fewer backups than the limit, we've reached the end
		if len(backups) < backupLimit {
			break
		}

		// Update offset for next iteration
		backupOffset += backupLimit
	}

	logger.Infof("Fetched %d backups in total", totalBackupsFetched)
	backupCount := 0

	for _, backup := range allBackups {
		volumeName := ""
		if backup.Attributes != nil {
			volumeName = backup.Attributes.VolumeName
		}

		// Get account name from BackupVault's Account (the account hosting the backup vault)
		accountName := ""
		deploymentName := ""

		if backup.BackupVault != nil {
			// deployment_name should be the backup vault name
			deploymentName = backup.BackupVault.Name

			// Get account name - Account relationship might not be preloaded, so fetch it if needed
			if backup.BackupVault.Account != nil {
				accountName = backup.BackupVault.Account.Name
			} else if backup.BackupVault.AccountID > 0 {
				// Account relationship not preloaded, fetch it from database
				var account datamodel.Account
				db := vcpDB.DB().WithContext(ctx)
				if err := db.Where("id = ?", backup.BackupVault.AccountID).First(&account).Error; err == nil {
					accountName = account.Name
				} else {
					logger.Warnf("Failed to fetch account with ID %d for backup vault %s: %v", backup.BackupVault.AccountID, backup.BackupVault.Name, err)
				}
			}
		}

		location := locations[precomputed.getLocationIndex(len(locations))] // Random location
		if deploymentName != "" {
			location = deploymentName
		}

		resources = append(resources, ResourceInfo{
			Name:           volumeName,
			UUID:           backup.UUID,
			DeploymentName: deploymentName,
			ConsumerID:     accountName,
			Location:       location,
			ResourceType:   metadata.Backup,
			IsRegionalHA:   false,
		})
		backupCount++
	}

	if totalBackupsFetched > 0 {
		logger.Infof("Processed %d backups", backupCount)
	}

	// Fetch replications with pagination to get all replications
	logger.Info("Fetching replications from VCP database...")
	replicationConditions := [][]interface{}{
		{"volume_replications.deleted_at IS NULL"},
	}

	replicationOffset := 0
	replicationLimit := 10000 // Batch size for pagination
	allReplications := make([]*datamodel.VolumeReplication, 0)
	totalReplicationsFetched := 0

	for {
		replicationPagination := &dbutils.Pagination{
			Offset: replicationOffset,
			Limit:  replicationLimit,
		}

		replications, err := vcpDB.ListVolumeReplicationsWithPagination(ctx, replicationConditions, replicationPagination)
		if err != nil {
			logger.Warnf("Failed to list replications (offset %d): %v (continuing without replications)", replicationOffset, err)
			break
		}

		// Break if no more replications
		if len(replications) == 0 {
			break
		}

		allReplications = append(allReplications, replications...)
		totalReplicationsFetched += len(replications)

		// If we got fewer replications than the limit, we've reached the end
		if len(replications) < replicationLimit {
			break
		}

		// Update offset for next iteration
		replicationOffset += replicationLimit
	}

	logger.Infof("Fetched %d replications in total", totalReplicationsFetched)
	replicationCount := 0

	for _, replication := range allReplications {
		location := locations[precomputed.getLocationIndex(len(locations))] // Random location
		deploymentName := ""
		if replication.Volume != nil && replication.Volume.Pool != nil {
			deploymentName = replication.Volume.Pool.DeploymentName
			if deploymentName != "" {
				location = deploymentName
			}
		}

		accountName := ""
		if replication.Account != nil {
			accountName = replication.Account.Name
		}

		// Extract ExternalUUID from ReplicationAttributes
		externalUUID := ""
		if replication.ReplicationAttributes != nil {
			externalUUID = replication.ReplicationAttributes.ExternalUUID
		}

		resources = append(resources, ResourceInfo{
			Name:           replication.Name,
			UUID:           replication.UUID,
			ExternalUUID:   externalUUID,
			DeploymentName: deploymentName,
			ConsumerID:     accountName,
			Location:       location,
			ResourceType:   metadata.VolumeReplicationRelationship,
			IsRegionalHA:   false,
		})
		replicationCount++
	}

	if totalReplicationsFetched > 0 {
		logger.Infof("Processed %d replications", replicationCount)
	}

	logger.Infof("Fetched %d total resources from VCP database (pools: %d, volumes: %d, backups: %d, replications: %d)",
		len(resources), totalPoolsFetched, totalVolumesFetched, backupCount, replicationCount)

	return resources, nil
}

// insertResourcesIntoVCP inserts generated resources into the VCP database
func insertResourcesIntoVCP(ctx context.Context, vcpDB database.Storage, resources []ResourceInfo, logger log.Logger) error {
	// Track created accounts, pools, SVMs, and volumes for relationships
	accountMap := make(map[string]*datamodel.Account) // account name -> account
	poolMap := make(map[string]*datamodel.Pool)       // pool name -> pool
	svmMap := make(map[int64]*datamodel.Svm)          // pool ID -> svm
	volumeMap := make(map[string]*datamodel.Volume)   // volume name -> volume

	// Precompute random values for optimization
	precomputed := precomputeRandomValues(len(resources) + 500)

	// Get GORM DB for batch operations
	db := vcpDB.DB().WithContext(ctx)
	batchSize := 100 // Batch size for inserts

	// First pass: Collect and batch create accounts
	logger.Info("Collecting accounts for batch insert...")
	accountsToCreate := make([]*datamodel.Account, 0)
	accountNameSet := make(map[string]bool)
	for _, resource := range resources {
		if resource.ResourceType == metadata.VolumePool || resource.ResourceType == metadata.VolumePoolRegionalHA ||
			resource.ResourceType == metadata.Volume || resource.ResourceType == metadata.VolumeRegionalHA ||
			resource.ResourceType == metadata.Backup || resource.ResourceType == metadata.VolumeReplicationRelationship {
			if !accountNameSet[resource.ConsumerID] {
				accountNameSet[resource.ConsumerID] = true
				accountsToCreate = append(accountsToCreate, &datamodel.Account{
					BaseModel: datamodel.BaseModel{
						UUID: precomputed.getUUID(),
					},
					Name:  resource.ConsumerID,
					State: datamodel.AccountStateEnabled,
				})
			}
		}
	}

	// Batch create accounts
	if len(accountsToCreate) > 0 {
		logger.Infof("Batch inserting %d accounts...", len(accountsToCreate))
		// First, try to get existing accounts
		for _, account := range accountsToCreate {
			existingAccount, err := vcpDB.GetAccount(ctx, account.Name)
			if err == nil {
				accountMap[account.Name] = existingAccount
			}
		}

		// Create only new accounts in batches
		newAccounts := make([]*datamodel.Account, 0)
		for _, account := range accountsToCreate {
			if _, exists := accountMap[account.Name]; !exists {
				newAccounts = append(newAccounts, account)
			}
		}

		if len(newAccounts) > 0 {
			// Use GORM CreateInBatches
			if err := db.CreateInBatches(newAccounts, batchSize).Error; err != nil {
				logger.Warnf("Failed to batch create accounts: %v, falling back to individual creates", err)
				// Fallback to individual creates
				for _, account := range newAccounts {
					createdAccount, err := vcpDB.CreateAccount(ctx, account)
					if err != nil {
						existingAccount, getErr := vcpDB.GetAccount(ctx, account.Name)
						if getErr == nil {
							accountMap[account.Name] = existingAccount
						}
					} else {
						accountMap[account.Name] = createdAccount
					}
				}
			} else {
				// Reload accounts to get IDs
				for _, account := range newAccounts {
					createdAccount, err := vcpDB.GetAccount(ctx, account.Name)
					if err == nil {
						accountMap[account.Name] = createdAccount
					}
				}
			}
		}
	}
	logger.Infof("Processed %d accounts", len(accountMap))

	// Second pass: Collect and batch create pools and SVMs
	logger.Info("Collecting pools for batch insert...")
	poolsToCreate := make([]*datamodel.Pool, 0)
	svmsToCreate := make([]*datamodel.Svm, 0)
	poolResourceMap := make(map[string]ResourceInfo) // pool name -> resource info

	for _, resource := range resources {
		if resource.ResourceType == metadata.VolumePool || resource.ResourceType == metadata.VolumePoolRegionalHA {
			account, exists := accountMap[resource.ConsumerID]
			if !exists {
				logger.Warnf("Account %s not found for pool %s", resource.ConsumerID, resource.Name)
				continue
			}

			// Enable auto tiering for 10% of pools
			allowAutoTiering := precomputed.getRandomFloat() < 0.1 // 10% chance
			var autoTieringConfig *datamodel.AutoTieringConfig
			if allowAutoTiering {
				// Generate random auto tiering configuration
				hotTierSizeInBytes := int64(precomputed.getRandomInt(1000)+100) * 1024 * 1024 * 1024 // 100GB to 1TB
				autoTieringConfig = &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:       hotTierSizeInBytes,
					EnableHotTierAutoResize:  precomputed.getRandomFloat() < 0.5, // 50% chance
					BucketName:               fmt.Sprintf("bucket-%s", precomputed.getRandomString10()),
					TieringStatus:            precomputed.getRandomPoolTieringStatus(),
					HotTierConsumption:       int64(precomputed.getRandomInt(80) + 10), // 10-90%
					ColdTierConsumption:      int64(precomputed.getRandomInt(80) + 10), // 10-90%
					TieringFullnessThreshold: int64(precomputed.getRandomInt(20) + 80), // 80-100%
				}
			}

			// Create pool
			pool := &datamodel.Pool{
				BaseModel: datamodel.BaseModel{
					UUID: utils.RandomUUID(),
				},
				Name:              resource.Name,
				AccountID:         account.ID,
				Account:           account,
				DeploymentName:    resource.DeploymentName,
				VendorID:          fmt.Sprintf("vendor-%s", generateRandomString(8)),
				AllowAutoTiering:  allowAutoTiering,
				AutoTieringConfig: autoTieringConfig,
				State:             datamodel.LifeCycleStateAvailable,
				StateDetails:      datamodel.LifeCycleStateAvailableDetails,
				PoolAttributes: &datamodel.PoolAttributes{
					IsRegionalHA: resource.IsRegionalHA,
					PrimaryZone:  resource.Location + "-a",
				},
			}
			poolsToCreate = append(poolsToCreate, pool)
			poolResourceMap[resource.Name] = resource
		}
	}

	// Batch create pools
	if len(poolsToCreate) > 0 {
		logger.Infof("Batch inserting %d pools...", len(poolsToCreate))
		if err := db.CreateInBatches(poolsToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create pools: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, pool := range poolsToCreate {
				createdPool, err := vcpDB.CreatingPool(ctx, pool)
				if err != nil {
					logger.Warnf("Failed to create pool %s: %v", pool.Name, err)
					continue
				}
				poolMap[pool.Name] = createdPool
			}
		} else {
			// After CreateInBatches, pools should have their IDs and UUIDs populated
			// Reload pools to get IDs and populate poolMap
			for _, pool := range poolsToCreate {
				// Get pool by UUID (GORM should populate UUID after CreateInBatches)
				if pool.UUID != "" {
					createdPool, err := vcpDB.GetPoolByUUID(ctx, pool.UUID)
					if err == nil {
						poolMap[pool.Name] = createdPool
						continue
					}
				}
				// Fallback: try to get by name and account ID using ListPoolsWithPagination
				poolConditions := [][]interface{}{
					{"name = ?", pool.Name},
					{"account_id = ?", pool.AccountID},
					{"deleted_at IS NULL"},
				}
				pools, err := vcpDB.ListPoolsWithPagination(ctx, poolConditions, &dbutils.Pagination{Limit: 1, Offset: 0})
				if err == nil && len(pools) > 0 {
					// Convert PoolView to Pool using GetPoolByUUID
					poolView := pools[0]
					if poolView != nil {
						poolByUUID, err := vcpDB.GetPoolByUUID(ctx, poolView.UUID)
						if err == nil {
							poolMap[pool.Name] = poolByUUID
						}
					}
				}
			}
		}

		// Create SVMs for pools
		for _, pool := range poolsToCreate {
			if createdPool, exists := poolMap[pool.Name]; exists {
				resource := poolResourceMap[pool.Name]
				account := accountMap[resource.ConsumerID]
				svm := &datamodel.Svm{
					BaseModel: datamodel.BaseModel{
						UUID: utils.RandomUUID(),
					},
					Name:         fmt.Sprintf("svm-%s", pool.Name),
					AccountID:    account.ID,
					PoolID:       createdPool.ID,
					State:        datamodel.LifeCycleStateAvailable,
					StateDetails: datamodel.LifeCycleStateAvailableDetails,
				}
				svmsToCreate = append(svmsToCreate, svm)
			}
		}

		// Batch create SVMs
		if len(svmsToCreate) > 0 {
			logger.Infof("Batch inserting %d SVMs...", len(svmsToCreate))
			if err := db.CreateInBatches(svmsToCreate, batchSize).Error; err != nil {
				logger.Warnf("Failed to batch create SVMs: %v, falling back to individual creates", err)
				// Fallback to individual creates
				for _, svm := range svmsToCreate {
					createdSvm, err := vcpDB.CreateSVM(ctx, svm)
					if err != nil {
						logger.Warnf("Failed to create SVM for pool ID %d: %v", svm.PoolID, err)
						// Try to get existing SVM
						existingSvm, getErr := vcpDB.GetSvmForPoolID(ctx, svm.PoolID)
						if getErr == nil {
							svmMap[svm.PoolID] = existingSvm
						}
						continue
					}
					svmMap[svm.PoolID] = createdSvm
				}
			} else {
				// Reload SVMs to get IDs
				for _, svm := range svmsToCreate {
					existingSvm, err := vcpDB.GetSvmForPoolID(ctx, svm.PoolID)
					if err == nil {
						svmMap[svm.PoolID] = existingSvm
					}
				}
			}
		}
	}

	logger.Infof("Created %d accounts, %d pools, and %d SVMs", len(accountMap), len(poolMap), len(svmMap))

	// Third pass: Collect and batch create volumes (need pools)
	logger.Info("Collecting volumes for batch insert...")
	volumesToCreate := make([]*datamodel.Volume, 0)
	volumeResourceMap := make(map[string]ResourceInfo) // volume name -> resource info

	// Ensure we have at least one pool
	if len(poolMap) == 0 {
		logger.Warn("No pools available, creating a default pool...")
		// Create a default pool from first account
		if len(accountMap) == 0 {
			return fmt.Errorf("no accounts available to create default pool")
		}
		var firstAccount *datamodel.Account
		for _, account := range accountMap {
			firstAccount = account
			break
		}
		defaultPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			Name:           "default-pool",
			AccountID:      firstAccount.ID,
			Account:        firstAccount,
			DeploymentName: "default-deployment",
			VendorID:       fmt.Sprintf("vendor-%s", generateRandomString(8)),
			State:          datamodel.LifeCycleStateAvailable,
			StateDetails:   datamodel.LifeCycleStateAvailableDetails,
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: false,
				PrimaryZone:  "us-central1-a",
			},
		}
		createdPool, err := vcpDB.CreatingPool(ctx, defaultPool)
		if err != nil {
			return fmt.Errorf("failed to create default pool: %w", err)
		}
		poolMap["default-pool"] = createdPool

		// Create SVM for default pool
		defaultSvm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			Name:         "svm-default-pool",
			AccountID:    firstAccount.ID,
			PoolID:       createdPool.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		createdSvm, err := vcpDB.CreateSVM(ctx, defaultSvm)
		if err != nil {
			return fmt.Errorf("failed to create SVM for default pool: %w", err)
		}
		svmMap[createdPool.ID] = createdSvm
	}

	// Get current volume count for each pool from the database (batch query)
	poolVolumeCounts := make(map[int64]int64) // pool ID -> volume count
	if len(poolMap) > 0 {
		// Collect all pool IDs
		poolIDs := make([]int64, 0, len(poolMap))
		for _, pool := range poolMap {
			poolIDs = append(poolIDs, pool.ID)
		}

		// Batch query volume counts for all pools
		db := vcpDB.DB().WithContext(ctx)
		var volumeCounts []struct {
			PoolID int64
			Count  int64
		}
		if err := db.Model(&datamodel.Volume{}).
			Select("pool_id, COUNT(*) as count").
			Where("pool_id IN ? AND deleted_at IS NULL", poolIDs).
			Group("pool_id").
			Scan(&volumeCounts).Error; err == nil {
			for _, vc := range volumeCounts {
				poolVolumeCounts[vc.PoolID] = vc.Count
			}
		} else {
			logger.Warnf("Failed to batch query volume counts: %v, falling back to individual queries", err)
			// Fallback to individual queries
			for poolName, pool := range poolMap {
				volumeCount, err := vcpDB.GetVolumeCountByPoolID(ctx, pool.ID)
				if err != nil {
					logger.Warnf("Failed to get volume count for pool %s (ID: %d): %v, assuming 0", poolName, pool.ID, err)
					poolVolumeCounts[pool.ID] = 0
				} else {
					poolVolumeCounts[pool.ID] = volumeCount
				}
			}
		}
		// Initialize counts to 0 for pools not found in the query
		for _, pool := range poolMap {
			if _, exists := poolVolumeCounts[pool.ID]; !exists {
				poolVolumeCounts[pool.ID] = 0
			}
		}
	}

	// Track volumes we're creating per pool
	poolVolumeCountsBeingCreated := make(map[int64]int64) // pool ID -> volumes being created

	for _, resource := range resources {
		if resource.ResourceType == metadata.Volume || resource.ResourceType == metadata.VolumeRegionalHA {
			account, exists := accountMap[resource.ConsumerID]
			if !exists {
				logger.Warnf("Account %s not found for volume %s", resource.ConsumerID, resource.Name)
				continue
			}

			// Find pools that can still have more volumes (less than 500)
			availablePools := make([]*datamodel.Pool, 0)
			for _, pool := range poolMap {
				currentCount := poolVolumeCounts[pool.ID] + poolVolumeCountsBeingCreated[pool.ID]
				if currentCount < 500 {
					availablePools = append(availablePools, pool)
				}
			}

			// If no pools available, skip this volume
			if len(availablePools) == 0 {
				logger.Warnf("All pools have reached maximum volumes (500), skipping volume %s", resource.Name)
				continue
			}

			// Pick a random pool from available pools
			pool := availablePools[rand.Intn(len(availablePools))]

			// Get SVM for this pool
			svm := svmMap[pool.ID]
			if svm == nil {
				// Try to get existing SVM
				existingSvm, getErr := vcpDB.GetSvmForPoolID(ctx, pool.ID)
				if getErr != nil {
					logger.Warnf("No SVM found for pool %s, skipping volume %s", pool.Name, resource.Name)
					continue
				}
				svm = existingSvm
				svmMap[pool.ID] = svm
			}

			// Create volume
			volume := &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					UUID: utils.RandomUUID(),
				},
				Name:         resource.Name,
				AccountID:    account.ID,
				Account:      account,
				PoolID:       pool.ID,
				Pool:         pool,
				SvmID:        svm.ID,
				Svm:          svm,
				State:        datamodel.LifeCycleStateAvailable,
				StateDetails: datamodel.LifeCycleStateAvailableDetails,
			}
			volumesToCreate = append(volumesToCreate, volume)
			volumeResourceMap[resource.Name] = resource

			// Track that we're creating a volume for this pool
			poolVolumeCountsBeingCreated[pool.ID]++
		}
	}

	// Batch create volumes
	if len(volumesToCreate) > 0 {
		logger.Infof("Batch inserting %d volumes...", len(volumesToCreate))
		if err := db.CreateInBatches(volumesToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create volumes: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, volume := range volumesToCreate {
				// Check if volume already exists before trying to create it
				existingVolume, getErr := vcpDB.GetVolumeByNameAndAccountID(ctx, volume.Name, volume.AccountID)
				if getErr == nil && existingVolume != nil {
					// Volume already exists, add it to volumeMap
					logger.Debugf("Volume %s already exists, skipping creation", volume.Name)
					volumeMap[volume.Name] = existingVolume
					continue
				}

				// Try to create the volume
				createdVolume, err := vcpDB.CreateVolume(ctx, volume)
				if err != nil {
					// Check if error is due to volume already existing
					var customErr *vsaerrors.CustomError
					if vsaerrors.As(err, &customErr) {
						// Check if it's an input validation error (volume already exists)
						if customErr.TrackingID == vsaerrors.ErrInputValidationError {
							logger.Debugf("Volume %s already exists (input validation error), trying to fetch it", volume.Name)
							// Try to get the existing volume
							existingVolume, getErr := vcpDB.GetVolumeByNameAndAccountID(ctx, volume.Name, volume.AccountID)
							if getErr == nil && existingVolume != nil {
								volumeMap[volume.Name] = existingVolume
								continue
							}
						}
					}
					// Check if it's a user input validation error (volume already exists)
					if customerrors.IsUserInputValidationErr(err) {
						logger.Debugf("Volume %s already exists (user input validation error), trying to fetch it", volume.Name)
						// Try to get the existing volume
						existingVolume, getErr := vcpDB.GetVolumeByNameAndAccountID(ctx, volume.Name, volume.AccountID)
						if getErr == nil && existingVolume != nil {
							volumeMap[volume.Name] = existingVolume
							continue
						}
					}
					logger.Warnf("Failed to create volume %s: %v", volume.Name, err)
					continue
				}
				volumeMap[volume.Name] = createdVolume
			}
		} else {
			// After CreateInBatches, batch reload volumes by UUIDs
			volumeUUIDs := make([]string, 0, len(volumesToCreate))
			volumeUUIDToName := make(map[string]string) // UUID -> name
			for _, volume := range volumesToCreate {
				if volume.UUID != "" {
					volumeUUIDs = append(volumeUUIDs, volume.UUID)
					volumeUUIDToName[volume.UUID] = volume.Name
				}
			}

			// Batch query volumes by UUIDs
			if len(volumeUUIDs) > 0 {
				var fetchedVolumes []*datamodel.Volume
				if err := db.Preload("Account").Preload("Pool").Preload("Svm").
					Where("uuid IN ?", volumeUUIDs).
					Find(&fetchedVolumes).Error; err == nil {
					for _, vol := range fetchedVolumes {
						if name, exists := volumeUUIDToName[vol.UUID]; exists {
							volumeMap[name] = vol
						}
					}
				} else {
					logger.Warnf("Failed to batch fetch volumes by UUIDs: %v, falling back to individual queries", err)
				}
			}

			// For volumes not found by UUID, batch query by name and account ID
			missingVolumes := make([]*datamodel.Volume, 0)
			for _, volume := range volumesToCreate {
				if _, exists := volumeMap[volume.Name]; !exists {
					missingVolumes = append(missingVolumes, volume)
				}
			}

			if len(missingVolumes) > 0 {
				// Group by account ID for batch queries
				accountIDToVolumes := make(map[int64][]*datamodel.Volume)
				for _, volume := range missingVolumes {
					accountIDToVolumes[volume.AccountID] = append(accountIDToVolumes[volume.AccountID], volume)
				}

				// Batch query by account ID and names
				for accountID, volumes := range accountIDToVolumes {
					volumeNames := make([]string, 0, len(volumes))
					for _, vol := range volumes {
						volumeNames = append(volumeNames, vol.Name)
					}

					var fetchedVolumes []*datamodel.Volume
					if err := db.Preload("Account").Preload("Pool").Preload("Svm").
						Where("account_id = ? AND name IN ? AND deleted_at IS NULL", accountID, volumeNames).
						Find(&fetchedVolumes).Error; err == nil {
						for _, vol := range fetchedVolumes {
							volumeMap[vol.Name] = vol
						}
					}
				}
			}
		}
	}

	logger.Infof("Created %d volumes", len(volumeMap))

	// Fourth pass: Batch create backup vaults (one per account)
	logger.Info("Collecting backup vaults for batch insert...")
	backupVaultMap := make(map[int64]*datamodel.BackupVault) // account ID -> backup vault
	backupVaultsToCreate := make([]*datamodel.BackupVault, 0)

	for accountName, account := range accountMap {
		// Check if vault already exists
		vaultName := fmt.Sprintf("vault-%s", account.Name)
		existingVault, err := vcpDB.GetBackupVaultByNameAndOwnerID(ctx, vaultName, fmt.Sprintf("%d", account.ID))
		if err == nil && existingVault != nil {
			backupVaultMap[account.ID] = existingVault
		} else {
			backupVault := &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID: utils.RandomUUID(),
				},
				Name:                  vaultName,
				AccountID:             account.ID,
				Account:               account,
				LifeCycleState:        datamodel.LifeCycleStateAvailable,
				LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
				BackupVaultType:       "STANDARD",
				AccountVendorID:       fmt.Sprintf("vendor-%s", generateRandomString(8)),
			}
			backupVaultsToCreate = append(backupVaultsToCreate, backupVault)
		}
		_ = accountName // Suppress unused variable warning
	}

	// Batch create backup vaults
	if len(backupVaultsToCreate) > 0 {
		logger.Infof("Batch inserting %d backup vaults...", len(backupVaultsToCreate))
		if err := db.CreateInBatches(backupVaultsToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create backup vaults: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, vault := range backupVaultsToCreate {
				createdVault, err := vcpDB.CreatingBackupVault(ctx, vault)
				if err != nil {
					logger.Warnf("Failed to create backup vault for account %s: %v", vault.Account.Name, err)
					continue
				}
				backupVaultMap[vault.AccountID] = createdVault
			}
		} else {
			// Reload backup vaults to get IDs
			for _, vault := range backupVaultsToCreate {
				existingVault, err := vcpDB.GetBackupVaultByNameAndOwnerID(ctx, vault.Name, fmt.Sprintf("%d", vault.AccountID))
				if err == nil && existingVault != nil {
					backupVaultMap[vault.AccountID] = existingVault
				}
			}
		}
	}
	logger.Infof("Created %d backup vaults", len(backupVaultMap))

	// Fifth pass: Collect and batch create backups (randomly associated with volumes)
	logger.Info("Collecting backups for batch insert...")
	// Rebuild volumeNames list to ensure it only contains volumes that exist in volumeMap
	volumeNames := make([]string, 0, len(volumeMap))
	for name, volume := range volumeMap {
		if volume != nil {
			volumeNames = append(volumeNames, name)
		}
	}

	// Track volumes that have backups and their labels
	volumeBackupLabels := make(map[string]*datamodel.JSONB) // volume UUID -> labels
	backupsToCreate := make([]*datamodel.Backup, 0)

	// Collect backups to create
	for _, resource := range resources {
		if resource.ResourceType == metadata.Backup {
			// Get account for this backup
			account, exists := accountMap[resource.ConsumerID]
			if !exists {
				logger.Warnf("Account %s not found for backup %s", resource.ConsumerID, resource.Name)
				continue
			}

			// Get backup vault for this account
			backupVault, exists := backupVaultMap[account.ID]
			if !exists {
				logger.Warnf("Backup vault not found for account %s, skipping backup %s", account.Name, resource.Name)
				continue
			}

			// Pick a random volume for this backup
			if len(volumeNames) == 0 {
				logger.Warnf("No volumes available for backup %s, skipping", resource.Name)
				continue
			}
			randomVolumeName := volumeNames[precomputed.getRandomInt(len(volumeNames))]
			volume := volumeMap[randomVolumeName]
			if volume == nil {
				logger.Warnf("Volume %s not found in volumeMap for backup %s, skipping", randomVolumeName, resource.Name)
				continue
			}

			// Generate labels for this backup (0-30 labels)
			labels := generateBackupLabels()

			// Create AssetMetadata with labels
			var assetMetadata *datamodel.AssetMetadata
			if labels != nil {
				// Create a map with both ChildAssets and Labels
				assetMetadataMap := make(map[string]interface{})
				assetMetadataMap["child_assets"] = []interface{}{}
				assetMetadataMap["labels"] = *labels

				// Marshal to JSON bytes
				jsonBytes, err := json.Marshal(assetMetadataMap)
				if err != nil {
					logger.Warnf("Failed to marshal AssetMetadata with labels for backup %s: %v", resource.Name, err)
					assetMetadata = &datamodel.AssetMetadata{
						ChildAssets: []datamodel.ChildAsset{},
					}
				} else {
					// Use Scan to load the JSONB data into AssetMetadata
					assetMetadata = &datamodel.AssetMetadata{}
					if err := assetMetadata.Scan(jsonBytes); err != nil {
						logger.Warnf("Failed to scan AssetMetadata with labels for backup %s: %v", resource.Name, err)
						assetMetadata = &datamodel.AssetMetadata{
							ChildAssets: []datamodel.ChildAsset{},
						}
					}
				}

				// Track labels for this volume (will be used to create BackupMetadata)
				volumeBackupLabels[volume.UUID] = labels
			} else {
				assetMetadata = &datamodel.AssetMetadata{
					ChildAssets: []datamodel.ChildAsset{},
				}
			}

			// Create BackupAttributes with VolumeName and AccountIdentifier
			backupAttributes := &datamodel.BackupAttributes{
				VolumeName:        volume.Name,
				AccountIdentifier: account.Name,
			}

			// Create backup
			backup := &datamodel.Backup{
				BaseModel: datamodel.BaseModel{
					UUID: utils.RandomUUID(),
				},
				Name:          resource.Name,
				VolumeUUID:    volume.UUID,
				BackupVaultID: backupVault.ID,
				BackupVault:   backupVault,
				State:         datamodel.LifeCycleStateAvailable,
				StateDetails:  datamodel.LifeCycleStateAvailableDetails,
				Type:          "MANUAL",
				SizeInBytes:   rand.Int63n(100*1024*1024*1024) + 1024*1024*1024, // Random size between 1GB and 100GB
				Attributes:    backupAttributes,
				AssetMetadata: assetMetadata,
			}
			backupsToCreate = append(backupsToCreate, backup)
		}
	}

	// Batch create backups
	if len(backupsToCreate) > 0 {
		logger.Infof("Batch inserting %d backups...", len(backupsToCreate))
		if err := db.CreateInBatches(backupsToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create backups: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, backup := range backupsToCreate {
				_, err := vcpDB.CreateBackup(ctx, backup)
				if err != nil {
					logger.Warnf("Failed to create backup %s: %v", backup.Name, err)
					continue
				}
			}
		}
	}

	logger.Infof("Created %d backups", len(backupsToCreate))

	// Batch create BackupMetadata for volumes that have backups
	logger.Info("Collecting BackupMetadata for batch insert...")
	backupMetadataToCreate := make([]*datamodel.BackupMetadata, 0)
	for volumeUUID, labels := range volumeBackupLabels {
		// Create BackupMetadata for this volume
		backupMetadata := &datamodel.BackupMetadata{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			VolumeUUID: volumeUUID,
			Labels:     labels,
		}
		backupMetadataToCreate = append(backupMetadataToCreate, backupMetadata)
	}

	// Batch create BackupMetadata
	if len(backupMetadataToCreate) > 0 {
		logger.Infof("Batch inserting %d BackupMetadata entries...", len(backupMetadataToCreate))
		if err := db.CreateInBatches(backupMetadataToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create BackupMetadata: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, backupMetadata := range backupMetadataToCreate {
				_, err := vcpDB.CreateBackupMetadata(ctx, backupMetadata)
				if err != nil {
					logger.Warnf("Failed to create BackupMetadata for volume %s: %v", backupMetadata.VolumeUUID, err)
					continue
				}
			}
		}
	}

	logger.Infof("Created %d BackupMetadata entries", len(backupMetadataToCreate))

	// Sixth pass: Collect and batch create replications (randomly associated with volumes)
	logger.Info("Collecting replications for batch insert...")
	replicationsToCreate := make([]*datamodel.VolumeReplication, 0)
	// Map to store JSONB bytes with resourceType for each replication
	replicationJSONBMap := make(map[string][]byte) // replication UUID -> JSONB bytes with resourceType

	// Collect replications to create
	for _, resource := range resources {
		if resource.ResourceType == metadata.VolumeReplicationRelationship {
			// Get account for this replication
			account, exists := accountMap[resource.ConsumerID]
			if !exists {
				logger.Warnf("Account %s not found for replication %s", resource.ConsumerID, resource.Name)
				continue
			}

			// Pick a random volume for this replication
			if len(volumeNames) == 0 {
				logger.Warnf("No volumes available for replication %s, skipping", resource.Name)
				continue
			}
			randomVolumeName := volumeNames[precomputed.getRandomInt(len(volumeNames))]
			volume := volumeMap[randomVolumeName]
			if volume == nil {
				logger.Warnf("Volume %s not found in volumeMap for replication %s, skipping", randomVolumeName, resource.Name)
				continue
			}

			// Get pool for the volume
			pool := volume.Pool
			if pool == nil {
				logger.Warnf("Pool not found for volume %s in replication %s, skipping", randomVolumeName, resource.Name)
				continue
			}

			// Get source location from volume's pool deployment name
			sourceLocation := ""
			if pool != nil {
				sourceLocation = pool.DeploymentName
			}

			// Pick a destination volume and pool (could be same or different)
			// For simplicity, we'll use a different volume/pool if available, otherwise use the same
			destinationVolume := volume
			destinationPool := pool
			destinationLocation := sourceLocation

			// Try to pick a different volume/pool for destination if available
			if len(volumeNames) > 1 {
				// Pick a random volume that's different from the source volume
				for attempts := 0; attempts < 10; attempts++ {
					randomDestVolumeName := volumeNames[rand.Intn(len(volumeNames))]
					if randomDestVolumeName != randomVolumeName {
						destVolume := volumeMap[randomDestVolumeName]
						if destVolume != nil && destVolume.Pool != nil {
							destinationVolume = destVolume
							destinationPool = destVolume.Pool
							destinationLocation = destVolume.Pool.DeploymentName
							break
						}
					}
				}
			}

			// Generate schedule (random schedule: daily, weekly, monthly, or manual)
			scheduleOptions := []string{"daily", "weekly", "monthly", "manual"}
			schedule := scheduleOptions[rand.Intn(len(scheduleOptions))]

			// Generate replication type (random type: CROSS_REGION_REPLICATION, ONPREM_REPLICATION, etc.)
			replicationTypeOptions := []string{"CROSS_REGION_REPLICATION", "ONPREM_REPLICATION", "SAME_REGION_REPLICATION"}
			replicationType := replicationTypeOptions[rand.Intn(len(replicationTypeOptions))]

			// Create replication
			replicationUUID := utils.RandomUUID()
			externalUUID := utils.RandomUUID() // Generate external UUID for replication
			endpointType := "src"              // Default to source
			if rand.Float64() < 0.5 {
				endpointType = "dst" // 50% chance of being destination
			}

			replicationAttributes := &datamodel.ReplicationDetails{
				EndpointType:        endpointType,
				ExternalUUID:        externalUUID,
				ReplicationSchedule: schedule,
				ReplicationType:     replicationType,
			}

			// Set source fields
			replicationAttributes.SourceReplicationUUID = replicationUUID
			replicationAttributes.SourceVolumeUUID = volume.UUID
			replicationAttributes.SourceVolumeName = volume.Name
			if pool != nil {
				replicationAttributes.SourcePoolUUID = pool.UUID
				replicationAttributes.SourceLocation = sourceLocation
			}

			// Set destination fields (for all replications)
			if destinationVolume != nil {
				replicationAttributes.DestinationVolumeUUID = destinationVolume.UUID
				replicationAttributes.DestinationVolumeName = destinationVolume.Name
			}
			if destinationPool != nil {
				replicationAttributes.DestinationPoolUUID = destinationPool.UUID
				replicationAttributes.DestinationLocation = destinationLocation
			}

			// Set endpoint-specific fields
			if endpointType == "src" {
				// Source endpoint - already set above
			} else {
				// Destination endpoint
				replicationAttributes.DestinationReplicationUUID = replicationUUID
			}

			// Add resourceType to the JSONB by creating a map with all fields including resourceType
			// Since ReplicationDetails is stored as JSONB, we can add resourceType even if not in struct
			// We'll marshal the struct first, then add resourceType to the map, and store it as JSONB
			var attributesJSONBytesWithResourceType []byte
			attributesJSONBytes, err := json.Marshal(replicationAttributes)
			if err != nil {
				logger.Warnf("Failed to marshal ReplicationDetails for replication %s: %v", resource.Name, err)
			} else {
				// Unmarshal to map to add resourceType
				replicationAttributesMap := make(map[string]interface{})
				if err := json.Unmarshal(attributesJSONBytes, &replicationAttributesMap); err != nil {
					logger.Warnf("Failed to unmarshal ReplicationDetails for replication %s: %v", resource.Name, err)
				} else {
					// Add resourceType to the map
					replicationAttributesMap["resource_type"] = "VOLUME_REPLICATION_RELATIONSHIP"
					// Marshal back to JSON bytes with resourceType included
					attributesJSONBytesWithResourceType, err = json.Marshal(replicationAttributesMap)
					if err != nil {
						logger.Warnf("Failed to marshal ReplicationDetails with resourceType for replication %s: %v", resource.Name, err)
					} else {
						// Scan the JSON with resourceType back into the struct
						// Note: resourceType will be lost during Scan since it's not in the struct
						// But we'll store the JSONB bytes with resourceType and use it when saving
						if scanErr := replicationAttributes.Scan(attributesJSONBytesWithResourceType); scanErr != nil {
							logger.Warnf("Failed to scan ReplicationDetails with resourceType for replication %s: %v", resource.Name, scanErr)
						}
					}
				}
			}

			volumeReplication := &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{
					UUID: replicationUUID,
				},
				Name:                  resource.Name,
				AccountID:             account.ID,
				Account:               account,
				VolumeID:              volume.ID,
				Volume:                volume,
				State:                 datamodel.LifeCycleStateAvailable,
				StateDetails:          datamodel.LifeCycleStateAvailableDetails,
				Uri:                   fmt.Sprintf("replication://%s", resource.Name),
				RemoteUri:             fmt.Sprintf("replication://remote-%s", resource.Name),
				ReplicationAttributes: replicationAttributes,
				Healthy:               true,
				TotalProgress:         100,
			}

			// Store the JSONB bytes with resourceType for later use when saving
			// We'll use GORM's Set method to set the JSONB directly with resourceType included
			if attributesJSONBytesWithResourceType != nil {
				replicationJSONBMap[replicationUUID] = attributesJSONBytesWithResourceType
			}

			replicationsToCreate = append(replicationsToCreate, volumeReplication)
		}
	}

	// Batch create replications
	if len(replicationsToCreate) > 0 {
		logger.Infof("Batch inserting %d replications...", len(replicationsToCreate))
		if err := db.CreateInBatches(replicationsToCreate, batchSize).Error; err != nil {
			logger.Warnf("Failed to batch create replications: %v, falling back to individual creates", err)
			// Fallback to individual creates
			for _, replication := range replicationsToCreate {
				// Set the JSONB with resourceType if available
				if jsonBWithResourceType, exists := replicationJSONBMap[replication.UUID]; exists {
					// Use GORM's Set method to set the JSONB directly with resourceType
					db.Model(replication).Where("uuid = ?", replication.UUID).Update("replication_attributes", jsonBWithResourceType)
				}
				_, err := vcpDB.CreateVolumeReplication(ctx, replication)
				if err != nil {
					logger.Warnf("Failed to create replication %s: %v", replication.Name, err)
					continue
				}
				// Update the JSONB with resourceType after creation
				if jsonBWithResourceType, exists := replicationJSONBMap[replication.UUID]; exists {
					db.Model(&datamodel.VolumeReplication{}).Where("uuid = ?", replication.UUID).Update("replication_attributes", jsonBWithResourceType)
				}
			}
		} else {
			// After batch creation, update the JSONB with resourceType for each replication
			for _, replication := range replicationsToCreate {
				if jsonBWithResourceType, exists := replicationJSONBMap[replication.UUID]; exists {
					// Use GORM's Update method to set the JSONB directly with resourceType
					if err := db.Model(&datamodel.VolumeReplication{}).Where("uuid = ?", replication.UUID).Update("replication_attributes", jsonBWithResourceType).Error; err != nil {
						logger.Warnf("Failed to update replication_attributes with resourceType for replication %s: %v", replication.UUID, err)
					}
				}
			}
		}
	}

	logger.Infof("Created %d replications", len(replicationsToCreate))

	return nil
}

func main() {
	// Parse command line flags
	var ( // --pools=1 --volumes=1 --backups=1 --replications=1
		accountCount         = flag.Int("accounts", 2, "Number of accounts to generate")
		poolCount            = flag.Int("pools", 2, "Number of pools to generate")
		volumeCount          = flag.Int("volumes", 10, "Number of volumes to generate")
		backupCount          = flag.Int("backups", 50, "Number of backups to generate")
		replicationCount     = flag.Int("replications", 50, "Number of replication relationships to generate")
		useExistingResources = flag.Bool("use-existing-resources", false, "Skip resource creation and use existing resources from VCP database")
		generatePastHour     = flag.Bool("generate-past-hour", false, "Generate metrics for the past hour (12 timestamps at 5-minute intervals) all at once for quick testing")
		timestampStr         = flag.String("timestamp", "", "Generate metrics for a specific timestamp (RFC3339 format, e.g., '2024-01-15T10:30:00Z'). If not set, uses current time")
		migrate              = flag.Bool("migrate", false, "Run database migrations on VCP and telemetry databases")
		dropRecreate         = flag.Bool("drop-recreate", false, "Drop and recreate both VCP and telemetry databases (WARNING: This will delete all data)")
		ignoreSignals        = flag.Bool("ignore-signals", false, "Ignore SIGINT/SIGTERM signals and continue processing (not recommended for production)")
	)
	flag.Parse()

	// Load environment variables from tel_local.env
	var envFile string
	wd, err := os.Getwd()
	if err == nil {
		currentDir := wd
		for {
			potentialFile := filepath.Join(currentDir, "tel_local.env")
			if _, statErr := os.Stat(potentialFile); statErr == nil {
				envFile = potentialFile
				break
			}
			parentDir := filepath.Dir(currentDir)
			if parentDir == currentDir {
				break
			}
			currentDir = parentDir
		}
	}

	if envFile != "" {
		if loadErr := loadEnvFile(envFile); loadErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Warning: Failed to load env file %s: %v\n", envFile, loadErr)
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "Loaded environment variables from %s\n", envFile)
		}
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: tel_local.env not found. Make sure it exists in the project root.\n")
	}

	var ctx context.Context
	var cancel context.CancelFunc

	if *ignoreSignals {
		// Use background context directly - it cannot be canceled
		// This ensures the process continues even if signals are received
		ctx = context.Background()
		cancel = func() {} // No-op cancel function
		tempLogger := util.GetLogger(ctx)
		tempLogger.Warn("Signal handling disabled - process will continue even if SIGINT/SIGTERM is received")
		tempLogger.Warn("Use Ctrl+C twice or kill -9 to force termination")
		tempLogger.Infof("ignoreSignals flag is set to: %v", *ignoreSignals)
	} else {
		// Create a context that cancels on SIGINT/SIGTERM for graceful shutdown
		ctx, cancel = signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	}
	defer cancel()

	ctx = context.WithValue(ctx, middleware.CorrelationContextKey, "generate-hydrated-metrics")
	logger := util.GetLogger(ctx)

	logger.Info("Starting Hydrated Metrics Generator")

	// Initialize VCP database connection
	logger.Info("Initializing VCP database connection...")
	vcpDbConn, err := connection.GetVcpDbConnection(ctx, logger)
	if err != nil {
		logger.Error("Failed to initialize VCP database connection", "error", err.Error())
		os.Exit(1)
	}
	defer connection.CloseDatabase(vcpDbConn, logger)
	logger.Info("Successfully connected to VCP database")

	// Initialize metrics database connection
	logger.Info("Initializing metrics database connection...")
	telemetryDbConn, err := connection.GetTelemetryDbConnection(ctx, logger)
	if err != nil {
		logger.Error("Failed to initialize telemetry database connection", "error", err.Error())
		os.Exit(1)
	}
	defer connection.CloseTelemetryDatabase(telemetryDbConn, logger)
	logger.Info("Successfully connected to telemetry database")

	// Handle drop and recreate option (must be done before migrations)
	if *dropRecreate {
		logger.Warn("WARNING: Dropping and recreating databases. All data will be deleted!")
		if err := dropAndRecreateDatabases(ctx, vcpDbConn, telemetryDbConn, logger); err != nil {
			logger.Error("Failed to drop and recreate databases", "error", err.Error())
			os.Exit(1)
		}
		logger.Info("Databases dropped and recreated successfully")
	}

	// Handle migration option
	if *migrate {
		logger.Info("Running database migrations...")
		if err := runMigrations(ctx, vcpDbConn, telemetryDbConn, logger); err != nil {
			logger.Error("Database migration failed", "error", err.Error())
			os.Exit(1)
		}
		logger.Info("Database migrations completed successfully")
	}

	// Initialize random seed
	rand.Seed(time.Now().UnixNano())

	var resources []ResourceInfo

	if !*useExistingResources {
		// Generate resources
		resources = generateResources(*accountCount, *poolCount, *volumeCount, *backupCount, *replicationCount, logger)

		if len(resources) == 0 {
			logger.Warn("No resources generated. Exiting.")
			os.Exit(0)
		}

		logger.Infof("Generated %d total resources", len(resources))

		// Insert resources into VCP database
		logger.Info("Inserting resources into VCP database...")
		if err := insertResourcesIntoVCP(ctx, vcpDbConn, resources, logger); err != nil {
			logger.Error("Failed to insert resources into VCP database", "error", err.Error())
			os.Exit(1)
		}
		logger.Info("Successfully inserted resources into VCP database")
	}

	logger.Info("Fetching existing resources from VCP database...")
	resources, err = getResourcesFromVCP(ctx, vcpDbConn, logger)
	if err != nil {
		logger.Error("Failed to fetch resources from VCP database", "error", err.Error())
		os.Exit(1)
	}
	if len(resources) == 0 {
		logger.Warn("No resources found in VCP database. Exiting.")
		os.Exit(0)
	}
	logger.Infof("Fetched %d total resources from VCP database", len(resources))

	// Get all job definitions
	jobDefs := common.DefaultAggregationJobDefinitions
	logger.Infof("Generating metrics for %d job definitions", len(jobDefs))

	// Get batch size from config
	telemetryConfig := common.LoadConfig()
	batchSize := int(telemetryConfig.PushBatchSize)
	if batchSize <= 0 {
		batchSize = 1000
	}

	// Handle timestamp flag - generate metrics for a specific timestamp
	if *timestampStr != "" {
		// Parse the timestamp string
		timestamp, err := time.Parse(time.RFC3339, *timestampStr)
		if err != nil {
			// Try parsing with common formats
			formats := []string{
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02T15:04:05Z07:00",
				"2006-01-02 15:04:05",
				"2006-01-02T15:04:05",
			}
			parsed := false
			for _, format := range formats {
				if t, err := time.Parse(format, *timestampStr); err == nil {
					timestamp = t
					parsed = true
					break
				}
			}
			if !parsed {
				logger.Errorf("Failed to parse timestamp '%s'. Supported formats: RFC3339 (e.g., '2006-01-02T15:04:05Z'), '2006-01-02 15:04:05', or '2006-01-02T15:04:05'", *timestampStr)
				os.Exit(1)
			}
		}

		logger.Infof("Generating metrics for specified timestamp: %v", timestamp)

		// Check context before starting
		if !*ignoreSignals && ctx.Err() != nil {
			logger.Warnf("Context already canceled before starting timestamp generation. Error: %v", ctx.Err())
			logger.Info("Use --ignore-signals flag to continue processing")
			return
		}

		insertMetricsWithTimestamp(ctx, logger, telemetryDbConn, resources, jobDefs, batchSize, timestamp, *ignoreSignals)
		logger.Info("Successfully generated metrics for specified timestamp. Exiting.")
		return
	}

	if *generatePastHour {
		// Generate metrics for the past hour (12 timestamps at 5-minute intervals)
		logger.Info("Generating metrics for the past hour (12 timestamps at 5-minute intervals)...")

		// Check context before starting
		if ctx.Err() != nil {
			logger.Warnf("Context already canceled before starting past hour generation. Error: %v", ctx.Err())
			if !*ignoreSignals {
				logger.Info("Use --ignore-signals flag to continue processing")
				return
			}
		}

		now := time.Now()

		// Generate 12 timestamps: now, now-5min, now-10min, ..., now-55min
		for i := 0; i < 12; i++ {
			// Check context before each timestamp (only if ignoreSignals is false)
			if !*ignoreSignals && ctx.Err() != nil {
				logger.Warnf("Context canceled during past hour generation at timestamp %d/%d. Error: %v", i+1, 12, ctx.Err())
				logger.Info("Use --ignore-signals flag to continue processing")
				return
			}

			timestamp := now.Add(-time.Duration(i*5) * time.Minute)
			logger.Infof("Generating metrics for timestamp %d/%d: %v", i+1, 12, timestamp)
			insertMetricsWithTimestamp(ctx, logger, telemetryDbConn, resources, jobDefs, batchSize, timestamp, *ignoreSignals)
		}

		logger.Info("Successfully generated metrics for the past hour. Exiting.")
		return
	}

	// Run insertion loop every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	logger.Info("Starting metric generation loop (every 5 minutes). Press Ctrl+C to stop.")

	// Run first insertion immediately
	insertMetrics(ctx, logger, telemetryDbConn, resources, jobDefs, batchSize, *ignoreSignals)

	// Then run every 5 minutes
	for {
		select {
		case <-ticker.C:
			insertMetrics(ctx, logger, telemetryDbConn, resources, jobDefs, batchSize, *ignoreSignals)
		case <-ctx.Done():
			logger.Info("Stopping metric generation")
			return
		}
	}
}

func insertMetrics(ctx context.Context, logger log.Logger, telemetryDbConn metricsdb.Storage, resources []ResourceInfo, jobDefs map[metadata.CombinedKeyResourceTypeMeasuredType]common.AggregationJobDefinition, batchSize int, ignoreSignals bool) {
	timestamp := time.Now()
	insertMetricsWithTimestamp(ctx, logger, telemetryDbConn, resources, jobDefs, batchSize, timestamp, ignoreSignals)
}

func insertMetricsWithTimestamp(ctx context.Context, logger log.Logger, telemetryDbConn metricsdb.Storage, resources []ResourceInfo, jobDefs map[metadata.CombinedKeyResourceTypeMeasuredType]common.AggregationJobDefinition, batchSize int, timestamp time.Time, ignoreSignals bool) {
	logger.Infof("Generating metrics for timestamp: %v (ignoreSignals=%v)", timestamp, ignoreSignals)

	// Check if context is already canceled at the start (only if ignoreSignals is false)
	if !ignoreSignals && ctx.Err() != nil {
		logger.Warnf("Context already canceled before starting metric insertion. Error: %v", ctx.Err())
		logger.Info("This usually means a shutdown signal (SIGINT/SIGTERM) was received")
		logger.Info("To continue processing despite canceled context, use --ignore-signals flag")
		return
	}

	// If ignoreSignals is true and context is canceled, log but continue
	if ignoreSignals && ctx.Err() != nil {
		logger.Warnf("Context was canceled but --ignore-signals is set, continuing anyway. Error: %v", ctx.Err())
	}

	// Process resources in batches of 10,000 to avoid memory issues
	const resourceBatchSize = 10000
	totalResources := len(resources)
	totalMetricsInserted := 0
	startTime := time.Now()

	logger.Infof("Starting to process %d resources in batches of %d", totalResources, resourceBatchSize)

	// Process resources in batches
	for i := 0; i < totalResources; i += resourceBatchSize {
		// Check if parent context is canceled (for graceful shutdown)
		// Only check if ignoreSignals is false
		if !ignoreSignals {
			select {
			case <-ctx.Done():
				logger.Warnf("Context canceled, stopping metric insertion. Processed %d/%d resources", i, totalResources)
				if i > 0 {
					logger.Infof("Successfully processed %d resources before cancellation", i)
				}
				return
			default:
				// Continue processing
			}
		}

		end := i + resourceBatchSize
		if end > totalResources {
			end = totalResources
		}

		batchResources := resources[i:end]
		batchNum := (i / resourceBatchSize) + 1
		totalBatches := (totalResources + resourceBatchSize - 1) / resourceBatchSize

		logger.Infof("Processing resource batch %d/%d (resources %d-%d of %d)", batchNum, totalBatches, i+1, end, totalResources)

		// Generate metrics for this batch of resources
		var batchMetrics []telemetrydatamodel.HydratedMetrics
		for _, resource := range batchResources {
			metrics := generateMetricsForResource(resource, timestamp, jobDefs)
			batchMetrics = append(batchMetrics, metrics...)
		}

		if len(batchMetrics) == 0 {
			logger.Warnf("No metrics generated for batch %d. Skipping insertion.", batchNum)
			continue
		}

		logger.Infof("Generated %d metrics for batch %d (%d resources)", len(batchMetrics), batchNum, len(batchResources))

		// Retry logic for batch insertion with exponential backoff
		// This handles transient context cancellation errors
		maxRetries := 3
		var err error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			// Create a new context with timeout for this batch insertion attempt
			// Use background context to completely isolate from parent context cancellation
			// This ensures the database operation can complete even if parent context is canceled
			batchCtx, batchCancel := context.WithTimeout(context.Background(), 30*time.Minute)

			// Insert metrics for this batch using the isolated context
			err = telemetryDbConn.CreateHydratedMetricsBatch(batchCtx, batchMetrics, batchSize)
			batchCancel() // Cancel immediately after use

			// Check if error is due to context cancellation
			// Check both the error message and context state before canceling
			isContextError := false
			if err != nil {
				errMsg := err.Error()
				isContextError = strings.Contains(errMsg, "context canceled") ||
					strings.Contains(errMsg, "context deadline exceeded") ||
					batchCtx.Err() == context.Canceled ||
					batchCtx.Err() == context.DeadlineExceeded
			}

			if err != nil && isContextError {
				if attempt < maxRetries {
					// Exponential backoff: 1s, 2s, 4s
					backoff := time.Duration(1<<uint(attempt-1)) * time.Second
					logger.Warnf("Batch %d insertion failed due to context error (attempt %d/%d), retrying after %v: %v",
						batchNum, attempt, maxRetries, backoff, err)
					time.Sleep(backoff)
					continue
				}
			}

			// If no error or non-context error, break retry loop
			break
		}

		// After the operation completes, check if parent context was canceled
		// Only check and act on cancellation if ignoreSignals is false
		// When ignoreSignals is true, we use context.Background() which cannot be canceled
		var parentCanceled bool
		if ignoreSignals {
			// When ignoreSignals is true, never treat context as canceled
			// context.Background() cannot be canceled, so this should always be false
			parentCanceled = false
			// Double-check: verify context is not canceled (should never happen with Background())
			if ctx.Err() != nil {
				logger.Warnf("WARNING: Context is canceled even though ignoreSignals=true and using context.Background(). This should not happen. Continuing anyway.")
			}
		} else {
			parentCanceled = ctx.Err() != nil
		}

		if err != nil {
			// Check if error is due to context cancellation
			if err.Error() == "context canceled" || strings.Contains(err.Error(), "context canceled") {
				logger.Errorf("Failed to insert hydrated metrics for batch %d after %d attempts: context canceled", batchNum, maxRetries)
			} else {
				logger.Errorf("Failed to insert hydrated metrics for batch %d: %v", batchNum, err)
			}

			// If parent context was canceled, stop processing (unless ignore-signals is set)
			// This check should never be true when ignoreSignals is true
			if parentCanceled {
				logger.Warnf("Received shutdown signal (SIGINT/SIGTERM), stopping metric insertion after batch %d failure", batchNum)
				logger.Info("To continue processing despite signals, use --ignore-signals flag (not recommended)")
				return
			}
			// Continue with next batch even if this one fails
			continue
		}

		// If parent context was canceled during insertion, stop after this successful batch (unless ignore-signals is set)
		// This check should never be true when ignoreSignals is true
		if parentCanceled {
			logger.Warnf("Received shutdown signal (SIGINT/SIGTERM), stopping metric insertion after successful batch %d", batchNum)
			logger.Infof("Processed %d batches successfully before shutdown", batchNum)
			logger.Infof("DEBUG: parentCanceled=%v, ignoreSignals=%v, ctx.Err()=%v", parentCanceled, ignoreSignals, ctx.Err())
			logger.Info("To continue processing despite signals, use --ignore-signals flag (not recommended)")
			return
		}

		totalMetricsInserted += len(batchMetrics)
		logger.Infof("Successfully inserted batch %d: %d metrics (total so far: %d)", batchNum, len(batchMetrics), totalMetricsInserted)

		// Clear batch metrics from memory to free up space
		batchMetrics = nil
	}

	elapsed := time.Since(startTime)
	logger.Infof("Successfully inserted %d total metrics for %d resources in %v (resource batch size: %d, insert batch size: %d)", totalMetricsInserted, totalResources, elapsed, resourceBatchSize, batchSize)
}

// dropAndRecreateDatabases drops and recreates both VCP and telemetry databases
func dropAndRecreateDatabases(ctx context.Context, vcpDbConn database.Storage, telemetryDbConn metricsdb.Storage, logger log.Logger) error {
	// Drop and recreate VCP database
	logger.Info("Dropping VCP database schema...")
	if err := dropDatabaseSchema(ctx, vcpDbConn, logger); err != nil {
		return fmt.Errorf("failed to drop VCP database schema: %w", err)
	}
	logger.Info("VCP database schema dropped successfully")

	logger.Info("Recreating VCP database schema...")
	if err := vcpDbConn.Migrate(ctx); err != nil {
		return fmt.Errorf("VCP database recreation failed: %w", err)
	}
	logger.Info("VCP database recreated successfully")

	// Drop and recreate telemetry database
	logger.Info("Dropping telemetry database schema...")
	if err := dropDatabaseSchema(ctx, telemetryDbConn, logger); err != nil {
		return fmt.Errorf("failed to drop telemetry database schema: %w", err)
	}
	logger.Info("Telemetry database schema dropped successfully")

	logger.Info("Recreating telemetry database schema...")
	if err := telemetryDbConn.Migrate(ctx); err != nil {
		return fmt.Errorf("telemetry database recreation failed: %w", err)
	}
	logger.Info("Telemetry database recreated successfully")

	return nil
}

// dropDatabaseSchema drops all tables in the database by dropping and recreating the public schema
func dropDatabaseSchema(ctx context.Context, db interface{}, logger log.Logger) error {
	// Get the GORM DB using WithContext (same pattern as used elsewhere in the code)
	// DB() returns *gorm.DB, and WithContext(ctx) also returns *gorm.DB
	var gormDB *gorm.DB
	switch v := db.(type) {
	case database.Storage:
		gormDB = v.DB().WithContext(ctx)
	case metricsdb.Storage:
		gormDB = v.DB().WithContext(ctx)
	default:
		return fmt.Errorf("unsupported database type")
	}

	if gormDB == nil {
		return fmt.Errorf("GORM DB is nil")
	}

	// For PostgreSQL, drop and recreate the public schema
	// This is safer than dropping individual tables as it handles all dependencies
	logger.Info("Dropping public schema (this will delete all tables)...")
	result := gormDB.Exec("DROP SCHEMA IF EXISTS public CASCADE")
	if result.Error != nil {
		return fmt.Errorf("failed to drop public schema: %w", result.Error)
	}

	logger.Info("Creating public schema...")
	result = gormDB.Exec("CREATE SCHEMA public")
	if result.Error != nil {
		return fmt.Errorf("failed to create public schema: %w", result.Error)
	}

	logger.Info("Granting permissions on public schema...")
	result = gormDB.Exec("GRANT ALL ON SCHEMA public TO public")
	if result.Error != nil {
		// This might fail if the user doesn't have permission, but it's not critical
		logger.Warnf("Failed to grant permissions on public schema (non-critical): %v", result.Error)
	}

	return nil
}

// runMigrations runs database migrations on both VCP and telemetry databases
func runMigrations(ctx context.Context, vcpDbConn database.Storage, telemetryDbConn metricsdb.Storage, logger log.Logger) error {
	// Migrate VCP database
	logger.Info("Running migrations on VCP database...")
	if err := vcpDbConn.Migrate(ctx); err != nil {
		return fmt.Errorf("VCP database migration failed: %w", err)
	}
	logger.Info("VCP database migration completed successfully")

	// Migrate telemetry database
	logger.Info("Running migrations on telemetry database...")
	if err := telemetryDbConn.Migrate(ctx); err != nil {
		return fmt.Errorf("telemetry database migration failed: %w", err)
	}
	logger.Info("Telemetry database migration completed successfully")

	return nil
}

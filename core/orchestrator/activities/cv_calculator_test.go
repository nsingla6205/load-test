package activities

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

func TestCalculateAggregatesForConstituentVolumesWithSpaceLimits(t *testing.T) {
	// Helper function to create test aggregates
	createTestAggregates := func(configs []struct {
		name        string
		state       string
		volumeCount int64
		size        int64
	}) []*vsa.Aggregate {
		var aggregates []*vsa.Aggregate
		for _, config := range configs {
			aggregates = append(aggregates, &vsa.Aggregate{
				Name:        config.name,
				State:       config.state,
				VolumeCount: config.volumeCount,
				Size:        config.size,
			})
		}
		return aggregates
	}

	// Helper function to create exactly 12 test aggregates with default online state
	createTwelveTestAggregates := func(volumeCounts []int64) []*vsa.Aggregate {
		if len(volumeCounts) == 0 {
			// Default all to 0 volume count
			volumeCounts = make([]int64, 12)
		}
		if len(volumeCounts) != 12 {
			panic("Must provide exactly 12 volume counts or empty slice for defaults")
		}

		var aggregates []*vsa.Aggregate
		for i := 0; i < 12; i++ {
			aggregates = append(aggregates, &vsa.Aggregate{
				Name:                 fmt.Sprintf("aggr%d", i+1),
				State:                "online",
				VolumeCount:          volumeCounts[i],
				TotalProvisionedSize: 0,
				Size:                 utils.TiBInBytes,
			})
		}
		return aggregates
	}

	// Helper function to count occurrences of each aggregate name in result
	countAggregateOccurrences := func(result []string) map[string]int64 {
		counts := make(map[string]int64)
		for _, name := range result {
			counts[name]++
		}
		return counts
	}
	ctx := context.Background()

	t.Run("Success Cases", func(t *testing.T) {
		t.Run("SingleConstituent_AssignsToFirstAggregate", func(t *testing.T) {
			// Arrange - 12 aggregates with all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			vmInstanceType := "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, 5*utils.GiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 1)
			assert.Equal(t, "aggr1", result.Aggregates[0])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("TwoConstituents_TwelveAggregates_EvenDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			vmInstanceType := "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 2, 5*utils.GiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 2)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr1"])
			assert.Equal(t, int64(1), counts["aggr2"])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("ThreeConstituents_TwelveAggregates_GreedyDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 3, 5*utils.GiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 3)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr1"]) // First CV goes to aggr1
			assert.Equal(t, int64(1), counts["aggr2"]) // Second CV goes to aggr2
			assert.Equal(t, int64(1), counts["aggr3"]) // Third CV goes to aggr3
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("TwelveConstituents_TwelveAggregates_OnePerAggregate", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			vmInstanceType := "c3-standard-16-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 12, 5*utils.GiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 12)
			counts := countAggregateOccurrences(result.Aggregates)
			// Each aggregate should get exactly 1 constituent
			for i := 1; i <= 12; i++ {
				assert.Equal(t, int64(1), counts[fmt.Sprintf("aggr%d", i)])
			}
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("ThreeAggregates_UniformSize", func(t *testing.T) {
			// Arrange - 3 aggregates with varying existing volumes
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 0, utils.TiBInBytes},
				{"aggr2", "online", 0, utils.TiBInBytes},
				{"aggr3", "online", 0, utils.TiBInBytes},
			})
			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 3, utils.TiBInBytes, 6, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 3)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr1"])
			assert.Equal(t, int64(1), counts["aggr2"])
			assert.Equal(t, int64(1), counts["aggr3"])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("FourAggregates_DifferentSizes", func(t *testing.T) {
			// Arrange - 4 aggregates with varying existing volumes
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 3, utils.TiBInBytes / 4},
				{"aggr2", "online", 10, utils.TiBInBytes},
				{"aggr3", "online", 2, utils.TiBInBytes / 4},
				{"aggr4", "online", 20, utils.TiBInBytes},
			})
			// Act
			vmInstanceType := "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 4, utils.TiBInBytes, 8, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 2)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr2"])
			assert.Equal(t, int64(1), counts["aggr4"])
			assert.Equal(t, int64(2), result.AggrMultiplier)
		})

		t.Run("FourAggregates_DifferentSizes_Second", func(t *testing.T) {
			// Arrange - 4 aggregates with varying existing volumes
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 3, utils.TiBInBytes / 4},
				{"aggr2", "online", 10, utils.TiBInBytes},
				{"aggr3", "online", 2, utils.TiBInBytes / 4},
				{"aggr4", "online", 200, utils.TiBInBytes},
			})
			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 6, utils.TiBInBytes, 8, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 2)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr2"])
			assert.Equal(t, int64(1), counts["aggr4"])
			assert.Equal(t, int64(3), result.AggrMultiplier)
		})

		t.Run("FourAggregates_DifferentSizes_Third", func(t *testing.T) {
			// Arrange - 4 aggregates with varying existing volumes
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 3, utils.TiBInBytes / 4},
				{"aggr2", "online", 10, utils.TiBInBytes},
				{"aggr3", "online", 2, utils.TiBInBytes / 4},
				{"aggr4", "online", 249, utils.TiBInBytes},
			})
			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 6, utils.TiBInBytes, 8, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 6)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(5), counts["aggr2"])
			assert.Equal(t, int64(1), counts["aggr1"])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		checkLen := func(counts map[string]int64, val int64) {
			assert.Equal(t, val-1, counts["aggr1"])
			for i := 2; i <= 12; i++ {
				index := "aggr" + fmt.Sprint(i)
				assert.Equal(t, val, counts[index])
			}
		}

		t.Run("4CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 2987, utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 249)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("8CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 5987, utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 499)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("22CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-22-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 11987, utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 999)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})
	})

	t.Run("Edge Cases", func(t *testing.T) {
		// size of cv is bigger than available size of all aggregate
		vmInstanceType := "c3-standard-4-lssd"
		t.Run("TwoAggregates_CVIsBigger", func(t *testing.T) {
			// Arrange - 4 aggregates with varying existing volumes
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 3, utils.TiBInBytes},
				{"aggr2", "online", 10, utils.TiBInBytes},
			})
			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes*2, 4, vmInstanceType, false)

			// Assert
			assert.NotNil(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
		})

		t.Run("EmptyAggregates_ReturnsError", func(t *testing.T) {
			// Arrange
			var aggregates []*vsa.Aggregate

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 8, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("LargeVolumeConstituentCountIsZero", func(t *testing.T) {
			// Arrange
			var aggregates []*vsa.Aggregate

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 0, utils.TiBInBytes, 8, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInvalidConstituentVolumeCount (5011)
			assert.Equal(t, "The constituent volume count must be greater than zero.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("AllAggregatesOffline_ReturnsError", func(t *testing.T) {
			// Arrange - 4 aggregates all offline
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "offline", 3, utils.TiBInBytes},
				{"aggr2", "offline", 10, utils.TiBInBytes},
			})
			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 4, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOfflineAggregateError (5015)
			assert.Equal(t, "Storage aggregate is not in online state and cannot accommodate volumes.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("AllAggregatesAtCapacity_ReturnsError", func(t *testing.T) {
			// Arrange - 12 aggregates all at max capacity (1000)
			aggregates := createTwelveTestAggregates([]int64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000})

			// Act
			vmInstanceType = "c3-standard-16-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrNoAggregatesWithCapacity (5012)
			assert.Equal(t, "All storage aggregates have reached their maximum capacity and cannot accommodate additional volumes.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("SingleAggregate_ReturnsError", func(t *testing.T) {
			// Arrange - only one aggregate available (should fail validation)
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 0, 0},
			})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("VeryLargeConstituents_MaxDistribution", func(t *testing.T) {
			// Arrange - test with max aggregates and large constituent count
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act - 1200 constituents (100 per aggregate)
			vmInstanceType = "c3-standard-22-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1200, 12*utils.TiBInBytes, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 12)
			counts := countAggregateOccurrences(result.Aggregates)
			// Each aggregate should get exactly 100 constituents
			for i := 1; i <= 12; i++ {
				assert.Equal(t, int64(1), counts[fmt.Sprintf("aggr%d", i)])
			}
			assert.Equal(t, int64(100), result.AggrMultiplier, "HCF should be 100 for 1200 constituents")
		})

		t.Run("4CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 1, 12 * utils.TiBInBytes},
				{"aggr2", "online", 0, 12 * utils.TiBInBytes},
				{"aggr3", "online", 0, 12 * utils.TiBInBytes},
				{"aggr4", "online", 0, 12 * utils.TiBInBytes},
				{"aggr5", "online", 0, 12 * utils.TiBInBytes},
				{"aggr6", "online", 0, 12 * utils.TiBInBytes},
			})
			// Act
			vmInstanceType = "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1500, 12*utils.TiBInBytes, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("8CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 1, 12 * utils.TiBInBytes},
				{"aggr2", "online", 0, 12 * utils.TiBInBytes},
				{"aggr3", "online", 0, 12 * utils.TiBInBytes},
				{"aggr4", "online", 0, 12 * utils.TiBInBytes},
				{"aggr5", "online", 0, 12 * utils.TiBInBytes},
				{"aggr6", "online", 0, 12 * utils.TiBInBytes},
			})
			// Act
			vmInstanceType = "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 3000, 12*utils.TiBInBytes, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("22CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 1, 12 * utils.TiBInBytes},
				{"aggr2", "online", 0, 12 * utils.TiBInBytes},
				{"aggr3", "online", 0, 12 * utils.TiBInBytes},
				{"aggr4", "online", 0, 12 * utils.TiBInBytes},
				{"aggr5", "online", 0, 12 * utils.TiBInBytes},
				{"aggr6", "online", 0, 12 * utils.TiBInBytes},
			})
			// Act
			vmInstanceType = "c3-standard-22-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 6000, 12*utils.TiBInBytes, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("TrueZonalPool_UsesTotalNodesForAggregateValidation", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
				size        int64
			}{
				{"aggr1", "online", 0, utils.TiBInBytes},
				{"aggr2", "online", 0, utils.TiBInBytes},
				{"aggr3", "online", 0, utils.TiBInBytes},
				{"aggr4", "online", 0, utils.TiBInBytes},
			})

			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 4, utils.TiBInBytes, 4, "c3-standard-4-lssd", true)

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Len(t, result.Aggregates, 4)
		})
	})
}

func TestCalculateAggregatesForConstituentVolumesWithCVLimits(t *testing.T) {
	// Helper function to create test aggregates
	createTestAggregates := func(configs []struct {
		name        string
		state       string
		volumeCount int64
	}) []*vsa.Aggregate {
		var aggregates []*vsa.Aggregate
		for _, config := range configs {
			aggregates = append(aggregates, &vsa.Aggregate{
				Name:        config.name,
				State:       config.state,
				VolumeCount: config.volumeCount,
			})
		}
		return aggregates
	}

	// Helper function to create exactly 12 test aggregates with default online state
	createTwelveTestAggregates := func(volumeCounts []int64) []*vsa.Aggregate {
		if len(volumeCounts) == 0 {
			// Default all to 0 volume count
			volumeCounts = make([]int64, 12)
		}
		if len(volumeCounts) != 12 {
			panic("Must provide exactly 12 volume counts or empty slice for defaults")
		}

		var aggregates []*vsa.Aggregate
		for i := 0; i < 12; i++ {
			aggregates = append(aggregates, &vsa.Aggregate{
				Name:        fmt.Sprintf("aggr%d", i+1),
				State:       "online",
				VolumeCount: volumeCounts[i],
			})
		}
		return aggregates
	}

	// Helper function to count occurrences of each aggregate name in result
	countAggregateOccurrences := func(result []string) map[string]int64 {
		counts := make(map[string]int64)
		for _, name := range result {
			counts[name]++
		}
		return counts
	}

	checkLen := func(counts map[string]int64, val int64) {
		assert.Equal(t, val-1, counts["aggr1"])
		for i := 2; i <= 12; i++ {
			index := "aggr" + fmt.Sprint(i)
			assert.Equal(t, val, counts[index])
		}
	}

	ctx := context.Background()
	vmInstanceType := "c3-standard-4-lssd"

	t.Run("Success Cases", func(t *testing.T) {
		t.Run("SingleConstituent_AssignsToFirstAggregate", func(t *testing.T) {
			// Arrange - 12 aggregates with all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 1)
			assert.Equal(t, "aggr1", result.Aggregates[0])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("TwoConstituents_TwelveAggregates_EvenDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 2, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 2)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr1"])
			assert.Equal(t, int64(1), counts["aggr2"])
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("ThreeConstituents_TwelveAggregates_GreedyDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 3, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 3)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(1), counts["aggr1"]) // First CV goes to aggr1
			assert.Equal(t, int64(1), counts["aggr2"]) // Second CV goes to aggr2
			assert.Equal(t, int64(1), counts["aggr3"]) // Third CV goes to aggr3
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("TwelveConstituents_TwelveAggregates_OnePerAggregate", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 12, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 12)
			counts := countAggregateOccurrences(result.Aggregates)
			// Each aggregate should get exactly 1 constituent
			for i := 1; i <= 12; i++ {
				assert.Equal(t, int64(1), counts[fmt.Sprintf("aggr%d", i)])
			}
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("ExistingVolumes_ProperDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates with varying existing volumes
			aggregates := createTwelveTestAggregates([]int64{10, 5, 0, 20, 15, 2, 8, 12, 3, 25, 1, 7})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 3, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 3)
			counts := countAggregateOccurrences(result.Aggregates)
			// Should prioritize aggregates with least existing volumes: aggr3 (0), aggr11 (1), aggr6 (2)
			assert.Equal(t, int64(2), counts["aggr3"])  // 0 existing volumes
			assert.Equal(t, int64(1), counts["aggr11"]) // 1 existing volume
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("PartialCapacity_StopsWhenFull", func(t *testing.T) {
			// Arrange - 12 aggregates with mixed capacity situations
			aggregates := createTwelveTestAggregates([]int64{199, 0, 198, 6, 195, 10, 190, 15, 185, 20, 180, 25})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 10, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 5)
			counts := countAggregateOccurrences(result.Aggregates)
			assert.Equal(t, int64(4), counts["aggr2"])
			assert.Equal(t, int64(1), counts["aggr4"])
			assert.Equal(t, int64(2), result.AggrMultiplier)
		})

		t.Run("OneHundredConstituents_TwelveAggregates_EvenDistribution", func(t *testing.T) {
			// Arrange - 12 aggregates all starting at 0
			aggregates := createTwelveTestAggregates([]int64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 100, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 100)
			counts := countAggregateOccurrences(result.Aggregates)
			// Should distribute as evenly as possible across 12 aggregates: 100/12 = 8.33, so some get 8, some get 9
			minCount := int64(8) // 100 / 12 = 8.33... so minimum is 8
			maxCount := int64(9) // maximum is 9
			for i := 1; i <= 12; i++ {
				aggrName := fmt.Sprintf("aggr%d", i)
				count := counts[aggrName]
				assert.True(t, count >= minCount && count <= maxCount, "Aggregate %s should have %d or %d constituents, got %d", aggrName, minCount, maxCount, count)
			}
			assert.True(t, result.AggrMultiplier > 0, "HCF should be positive")
		})

		t.Run("4CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 2987, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 249)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("8CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 5987, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 499)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})

		t.Run("22CPUsWithMaxCVCount", func(t *testing.T) {
			// Arrange - 12 aggregates with 0 volumes

			aggregates := createTwelveTestAggregates([]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			// Act
			vmInstanceType := "c3-standard-22-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 11987, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			counts := countAggregateOccurrences(result.Aggregates)
			checkLen(counts, 999)
			assert.Equal(t, int64(1), result.AggrMultiplier)
		})
	})

	t.Run("Edge Cases", func(t *testing.T) {
		t.Run("ZeroConstituents_ReturnsAvailableAggregates", func(t *testing.T) {
			// Arrange - 12 aggregates with different volume counts
			aggregates := createTwelveTestAggregates([]int64{0, 50, 100, 25, 75, 10, 60, 30, 40, 80, 20, 90})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 0, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.ElementsMatch(t, []string{}, result.Aggregates)
		})
		t.Run("EmptyAggregates_ReturnsError", func(t *testing.T) {
			// Arrange
			var aggregates []*vsa.Aggregate

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 5, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("AllAggregatesOffline_ReturnsError", func(t *testing.T) {
			// Arrange - 12 aggregates all offline
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "offline", 0}, {"aggr2", "offline", 0}, {"aggr3", "offline", 0},
				{"aggr4", "offline", 0}, {"aggr5", "offline", 0}, {"aggr6", "offline", 0},
				{"aggr7", "offline", 0}, {"aggr8", "offline", 0}, {"aggr9", "offline", 0},
				{"aggr10", "offline", 0}, {"aggr11", "offline", 0}, {"aggr12", "offline", 0},
			})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 5, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOfflineAggregateError (5015)
			assert.Equal(t, "Storage aggregate is not in online state and cannot accommodate volumes.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("AllAggregatesAtCapacity_ReturnsError", func(t *testing.T) {
			// Arrange - 12 aggregates all at max capacity (1000)
			aggregates := createTwelveTestAggregates([]int64{1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000})

			// Act
			vmInstanceType = "c3-standard-44-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 5, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrNoAggregatesWithCapacity (5012)
			assert.Equal(t, "All storage aggregates have reached their maximum capacity and cannot accommodate additional volumes.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("SingleAggregate_ReturnsError", func(t *testing.T) {
			// Arrange - only one aggregate available (should fail validation)
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 0},
			})

			// Act
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 5, 24, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("VeryLargeConstituents_MaxDistribution", func(t *testing.T) {
			// Arrange - test with max aggregates and large constituent count
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 0}, {"aggr2", "online", 0}, {"aggr3", "online", 0},
				{"aggr4", "online", 0}, {"aggr5", "online", 0}, {"aggr6", "online", 0},
				{"aggr7", "online", 0}, {"aggr8", "online", 0}, {"aggr9", "online", 0},
				{"aggr10", "online", 0}, {"aggr11", "online", 0}, {"aggr12", "online", 0},
			})

			// Act - 1200 constituents (100 per aggregate)
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1200, 24, vmInstanceType, false)

			// Assert
			assert.NoError(t, err)
			assert.Len(t, result.Aggregates, 12)
			counts := countAggregateOccurrences(result.Aggregates)
			// Each aggregate should get exactly 100 constituents
			for i := 1; i <= 12; i++ {
				assert.Equal(t, int64(1), counts[fmt.Sprintf("aggr%d", i)])
			}
			assert.Equal(t, int64(100), result.AggrMultiplier, "HCF should be 100 for 1200 constituents")
		})

		t.Run("4CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 1},
				{"aggr2", "online", 0},
				{"aggr3", "online", 0},
				{"aggr4", "online", 0},
				{"aggr5", "online", 0},
				{"aggr6", "online", 0},
			})
			// Act
			vmInstanceType = "c3-standard-4-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1500, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("8CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 1},
				{"aggr2", "online", 0},
				{"aggr3", "online", 0},
				{"aggr4", "online", 0},
				{"aggr5", "online", 0},
				{"aggr6", "online", 0},
			})
			// Act
			vmInstanceType = "c3-standard-8-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 3000, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("22CPUsWithMaxCVCount", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 1},
				{"aggr2", "online", 0},
				{"aggr3", "online", 0},
				{"aggr4", "online", 0},
				{"aggr5", "online", 0},
				{"aggr6", "online", 0},
			})
			// Act
			vmInstanceType = "c3-standard-22-lssd"
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 6000, 12, vmInstanceType, false)

			// Assert
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
			assert.Nil(t, result)
		})

		t.Run("TrueZonalPool_UsesTotalNodesForAggregateValidation", func(t *testing.T) {
			aggregates := createTestAggregates([]struct {
				name        string
				state       string
				volumeCount int64
			}{
				{"aggr1", "online", 0},
				{"aggr2", "online", 0},
				{"aggr3", "online", 0},
				{"aggr4", "online", 0},
			})

			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 4, 4, "c3-standard-4-lssd", true)

			assert.NoError(t, err)
			assert.NotNil(t, result)
			assert.Len(t, result.Aggregates, 4)
		})
	})
}

// TestVCPErrorHandling tests that all error conditions return proper VCP errors with correct error codes
func TestVCPErrorHandling(t *testing.T) {
	ctx := context.Background()

	t.Run("CalculateAggregatesForConstituentVolumesWithSpaceLimits_VCPErrors", func(t *testing.T) {
		// Test invalid constituent volume count
		t.Run("InvalidConstituentVolumeCount_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 0, Size: utils.TiBInBytes},
			}

			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 0, utils.TiBInBytes, 2, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrInvalidConstituentVolumeCount (5011)
			assert.Equal(t, "The constituent volume count must be greater than zero.", err.Error())
		})

		// Test aggregate count mismatch
		t.Run("AggregateCountMismatch_ReturnsVCPError", func(t *testing.T) {
			// Provide only 1 aggregate when 2 are expected (totalNodes=4, expectedAggregates=2)
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 0, Size: utils.TiBInBytes},
			}

			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
		})

		// Test aggregate not online
		t.Run("AggregateNotOnline_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "offline", VolumeCount: 0, Size: utils.TiBInBytes},
				{Name: "aggr2", State: "online", VolumeCount: 0, Size: utils.TiBInBytes},
			}

			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrOfflineAggregateError (5015)
			assert.Equal(t, "Storage aggregate is not in online state and cannot accommodate volumes.", err.Error())
		})

		// Test no aggregates with available capacity
		t.Run("NoAggregatesWithCapacity_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 249, Size: utils.TiBInBytes}, // At max capacity for c3-standard-4-lssd
				{Name: "aggr2", State: "online", VolumeCount: 249, Size: utils.TiBInBytes}, // At max capacity
			}

			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrNoAggregatesWithCapacity (5012)
			assert.Equal(t, "All storage aggregates have reached their maximum capacity and cannot accommodate additional volumes.", err.Error())
		})

		// Test insufficient total aggregate capacity
		t.Run("InsufficientTotalCapacity_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 0, Size: utils.GiBInBytes, TotalProvisionedSize: 0},
				{Name: "aggr2", State: "online", VolumeCount: 0, Size: utils.GiBInBytes, TotalProvisionedSize: 0},
			}

			// Request more CVs than can fit based on space limitations
			result, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1000, utils.TiBInBytes, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
		})
	})

	t.Run("CalculateAggregatesForConstituentVolumesWithCVLimits_VCPErrors", func(t *testing.T) {
		// Test aggregate count mismatch
		t.Run("AggregateCountMismatch_ReturnsVCPError", func(t *testing.T) {
			// Provide only 1 aggregate when 2 are expected (totalNodes=4, expectedAggregates=2)
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 0},
			}

			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
		})

		// Test aggregate not online
		t.Run("AggregateNotOnline_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "maintenance", VolumeCount: 0},
				{Name: "aggr2", State: "online", VolumeCount: 0},
			}

			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrOfflineAggregateError (5015)
			assert.Equal(t, "Storage aggregate is not in online state and cannot accommodate volumes.", err.Error())
		})

		// Test no aggregates with available capacity
		t.Run("NoAggregatesWithCapacity_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 249}, // At max capacity for c3-standard-4-lssd
				{Name: "aggr2", State: "online", VolumeCount: 249}, // At max capacity
			}

			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 1, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrNoAggregatesWithCapacity (5012)
			assert.Equal(t, "All storage aggregates have reached their maximum capacity and cannot accommodate additional volumes.", err.Error())
		})

		// Test insufficient total aggregate capacity
		t.Run("InsufficientTotalCapacity_ReturnsVCPError", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 248}, // Only 1 CV available
				{Name: "aggr2", State: "online", VolumeCount: 248}, // Only 1 CV available
			}

			// Request more CVs than available capacity (total available = 2, request = 10)
			result, err := CalculateAggregatesForConstituentVolumesWithCVLimits(ctx, aggregates, 10, 4, "c3-standard-4-lssd", false)

			assert.Error(t, err)
			assert.Nil(t, result)
			// Check that it returns the standardized VCP error message for ErrInsufficientAggregateCapacity (5013)
			assert.Equal(t, "Insufficient storage capacity available across aggregates to fulfill the volume creation request.", err.Error())
		})
	})

	// Test that VCP errors contain the expected error codes by checking error type
	t.Run("VCPErrorTypes", func(t *testing.T) {
		// Test ErrInvalidConstituentVolumeCount scenarios
		t.Run("InvalidConstituentVolumeCount_ErrorCode5011", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{
				{Name: "aggr1", State: "online", VolumeCount: 0, Size: utils.TiBInBytes},
			}

			// Test invalid constituent count (uses ErrInvalidConstituentVolumeCount)
			_, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, -1, utils.TiBInBytes, 2, "c3-standard-4-lssd", false)
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrInvalidConstituentVolumeCount (5011)
			assert.Equal(t, "The constituent volume count must be greater than zero.", err.Error())
		})

		// Test ErrOntapAggregateCountMismatch scenarios
		t.Run("OntapAggregateCountMismatch_AggregateCountMismatch", func(t *testing.T) {
			aggregates := []*vsa.Aggregate{}

			// Test aggregate count mismatch (uses ErrOntapAggregateCountMismatch)
			_, err := CalculateAggregatesForConstituentVolumesWithSpaceLimits(ctx, aggregates, 1, utils.TiBInBytes, 4, "c3-standard-4-lssd", false)
			assert.Error(t, err)
			// Check that it returns the standardized VCP error message for ErrOntapAggregateCountMismatch (5014)
			assert.Equal(t, "Some aggregates may be unavailable/offline to fulfil this request.", err.Error())
		})
	})
}

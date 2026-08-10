package validators

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// LargeCapacityPoolValidator implements validation for large capacity pools
type LargeCapacityPoolValidator struct{}

func getLargeCapacityPerformanceLimits(perf *CustomPerformance) (uint64, uint64, uint64, uint64) {
	if perf.IsTrueZonal {
		return minTrueZonalThroughput, maxTrueZonalThroughput, minTrueZonalCustomIops, maxTrueZonalCustomIops
	}

	return minLvThroughput, maxLvThroughput, minLvCustomIops, maxLvCustomIops
}

func largeCapacityErrorSuffix(perf *CustomPerformance) string {
	if perf.IsTrueZonal {
		return " for True Zonal Large Capacity pools"
	}
	return " for Large Capacity pools"
}

func (v *LargeCapacityPoolValidator) ValidateSize(perf *CustomPerformance) error {
	if perf.SizeInBytes < minLvCoolTierCapacity {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("SizeInBytes must be at least %s (%d bytes) for Large Capacity pools",
				utils.FmtUint64Bytes(minLvCoolTierCapacity), minLvCoolTierCapacity))
	}

	maxCapacity := maxLvHotTierCapacity
	if perf.AllowAutoTiering {
		maxCapacity = maxLvPoolCapacity
	}

	if perf.SizeInBytes > maxCapacity {
		tierMsg := ""
		if perf.AllowAutoTiering {
			tierMsg = " when AllowAutoTiering is true"
		}
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("SizeInBytes must be less than or equal to %s (%d bytes)%s for Large Capacity pools",
				utils.FmtUint64Bytes(maxCapacity), maxCapacity, tierMsg))
	}

	return nil
}

func (v *LargeCapacityPoolValidator) ValidateThroughput(perf *CustomPerformance) error {
	// Check for negative values first
	if perf.ThroughputMibps < 0 {
		return customerrors.NewUserInputValidationErr(
			"TotalThroughputMibps must be set and must be greater than 0")
	}

	minThroughput, maxThroughput, _, _ := getLargeCapacityPerformanceLimits(perf)
	if err := ValidateThroughputRange(perf.ThroughputMibps, minThroughput, maxThroughput); err != nil {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("%s%s", err.Error(), largeCapacityErrorSuffix(perf)))
	}

	return nil
}

func (v *LargeCapacityPoolValidator) ValidateIops(perf *CustomPerformance) error {
	_, _, minIops, maxIops := getLargeCapacityPerformanceLimits(perf)
	// Use shared IOPS validation logic with large capacity pool parameters
	return validateIopsCommon(perf, minIops, maxIops, utils.IopsPerMiBps, largeCapacityErrorSuffix(perf))
}

func (v *LargeCapacityPoolValidator) ValidateAutoTierParams(perf *CustomPerformance) error {
	return validateAutoTierCommon(perf, minHotTierSizeLargeVolumes)
}

package validators

import (
	"fmt"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// Common validation constants used across validators
var (
	// Pool size limits
	minQuotaInBytesPool        = utils.MinQuotaInBytesPool
	maxQuotaInBytesPool        = utils.MaxQuotaInBytesPool
	minSizeGranularity         = utils.MinSizeGranularity
	minHotTierSize             = utils.MinHotTierSize
	minHotTierSizeLargeVolumes = utils.MinHotTierSizeLargeVolumes

	// Performance limits (shared)
	minCustomThroughput = utils.MinCustomThroughput
	maxCustomThroughput = utils.MaxCustomThroughput
	minCustomIops       = utils.MinCustomIops
	maxCustomIops       = utils.MaxCustomIops
	iopsPerMiBps        = utils.IopsPerMiBps

	// Large capacity specific limits
	minLvCoolTierCapacity  = utils.MinLvCoolTierCapacity
	maxLvPoolCapacity      = utils.MaxLvPoolCapacity
	maxLvHotTierCapacity   = utils.MaxLvHotTierCapacity
	minLvThroughput        = utils.MinLvThroughput
	maxLvThroughput        = utils.MaxLvThroughput
	minLvCustomIops        = utils.MinLvCustomIops
	maxLvCustomIops        = utils.MaxLvCustomIops
	minTrueZonalThroughput = utils.MinTrueZonalThroughput
	maxTrueZonalThroughput = utils.MaxTrueZonalThroughput
	minTrueZonalCustomIops = utils.MinTrueZonalCustomIops
	maxTrueZonalCustomIops = utils.MaxTrueZonalCustomIops
	AutoTieringEnabled     = utils.AutoTieringEnabled
	enableMqos             = env.GetBool("ENABLE_MQOS", true)
)

// CustomPerformance represents performance parameters that can be used for both create and update operations
type CustomPerformance struct {
	SizeInBytes        uint64
	ThroughputMibps    int64
	Iops               *int64
	AllowAutoTiering   bool
	HotTierSizeInBytes uint64
	QosType            string
	LargeCapacity      bool
	IsTrueZonal        bool
}

// NewCustomPerformanceFromCreate creates CustomPerformance from CreatePoolParams
func NewCustomPerformanceFromCreate(params *commonparams.CreatePoolParams) *CustomPerformance {
	var throughput int64
	var iops *int64

	if params.CustomPerformanceParams != nil {
		throughput = params.CustomPerformanceParams.ThroughputMibps
		iops = params.CustomPerformanceParams.Iops
	}

	return &CustomPerformance{
		SizeInBytes:        params.SizeInBytes,
		ThroughputMibps:    throughput,
		Iops:               iops,
		AllowAutoTiering:   params.AllowAutoTiering,
		HotTierSizeInBytes: params.HotTierSizeInBytes,
		QosType:            params.QosType,
		LargeCapacity:      params.LargeCapacity,
		IsTrueZonal:        params.IsTrueZonal,
	}
}

// NewCustomPerformanceFromUpdate creates CustomPerformance from UpdatePoolParams
func NewCustomPerformanceFromUpdate(params *commonparams.UpdatePoolParams) *CustomPerformance {
	return &CustomPerformance{
		SizeInBytes:        params.SizeInBytes,
		ThroughputMibps:    params.TotalThroughputMibps,
		Iops:               params.TotalIops,
		AllowAutoTiering:   params.AllowAutoTiering,
		HotTierSizeInBytes: params.HotTierSizeInBytes,
		QosType:            params.QosType,
	}
}

// PoolValidator defines the interface for pool validation strategies
type PoolValidator interface {
	ValidateSize(perf *CustomPerformance) error
	ValidateThroughput(perf *CustomPerformance) error
	ValidateIops(perf *CustomPerformance) error
	ValidateAutoTierParams(perf *CustomPerformance) error
}

// NewPoolValidator creates the appropriate validator based on pool type
func NewPoolValidator(isLargeCapacity bool) PoolValidator {
	if isLargeCapacity {
		return &LargeCapacityPoolValidator{}
	}
	return &StandardPoolValidator{}
}

// ValidationPipeline represents a sequence of validation steps
type ValidationPipeline struct {
	validator PoolValidator
	steps     []func(PoolValidator, *CustomPerformance) error
}

func NewValidationPipeline(validator PoolValidator) *ValidationPipeline {
	return &ValidationPipeline{
		validator: validator,
		steps: []func(PoolValidator, *CustomPerformance) error{
			func(v PoolValidator, p *CustomPerformance) error { return v.ValidateSize(p) },
			func(v PoolValidator, p *CustomPerformance) error { return v.ValidateThroughput(p) },
			func(v PoolValidator, p *CustomPerformance) error { return v.ValidateIops(p) },
			func(v PoolValidator, p *CustomPerformance) error { return v.ValidateAutoTierParams(p) },
		},
	}
}

func (vp *ValidationPipeline) Execute(perf *CustomPerformance) error {
	for _, step := range vp.steps {
		if err := step(vp.validator, perf); err != nil {
			return err
		}
	}
	return nil
}

// Common IOPS validation function to eliminate duplication
func validateIopsCommon(perf *CustomPerformance, minIops, maxIops uint64, iopsPerMiBpsValue uint64, errorSuffix string) error {
	// Check if IOPS needs to be calculated from throughput (when IOPS is nil)
	if perf.Iops == nil && perf.ThroughputMibps > 0 {
		// Calculate IOPS from throughput
		calculatedIops := perf.ThroughputMibps * int64(iopsPerMiBpsValue)
		if calculatedIops > int64(maxIops) {
			calculatedIops = int64(maxIops)
		}
		perf.Iops = &calculatedIops
	}

	// Check for negative values (after potential calculation)
	if perf.Iops != nil && *perf.Iops < 0 {
		baseMsg := "TotalIops must be set and must be greater than 0"
		if errorSuffix != "" {
			baseMsg = fmt.Sprintf("Iops must be set and must be greater than %d%s", minIops, errorSuffix)
		}
		return customerrors.NewUserInputValidationErr(baseMsg)
	}

	// Validate IOPS range and business rules (only if IOPS is set)
	if perf.Iops != nil {
		if err := ValidateIopsRange(*perf.Iops, minIops, maxIops); err != nil {
			if errorSuffix != "" {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf("%s%s", err.Error(), errorSuffix))
			}
			return err
		}

		// Validate IOPS meets minimum requirement for throughput (throughput * 16)
		if perf.ThroughputMibps > 0 {
			minimumIops := perf.ThroughputMibps * 16
			if *perf.Iops < minimumIops {
				return customerrors.NewUserInputValidationErr(fmt.Sprintf(
					"TotalIops must be at least %d IOPS (minimum for %d MiBps throughput)%s",
					minimumIops, perf.ThroughputMibps, errorSuffix))
			}
		}
	}

	return nil
}

// Common validateAutoTierCommon validation function to eliminate duplication
func validateAutoTierCommon(perf *CustomPerformance, minHotTierSize uint64) error {
	if !perf.AllowAutoTiering {
		return nil // No validation needed if auto-tiering is disabled
	}
	if !AutoTieringEnabled && (perf.AllowAutoTiering || perf.HotTierSizeInBytes > 0) {
		return customerrors.NewUserInputValidationErr("Auto-Tiering feature is currently not enabled")
	}

	// 1. HotTierSizeInBytes must be less than or equal to pool size
	if perf.HotTierSizeInBytes > perf.SizeInBytes {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("Hot-tier size %s exceeds the total storage pool capacity %s.",
				utils.FmtUint64Bytes(perf.HotTierSizeInBytes),
				utils.FmtUint64Bytes(perf.SizeInBytes)))
	}

	// 2. HotTierSizeInBytes must be >= minimum size
	if perf.HotTierSizeInBytes < minHotTierSize {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("HotTierSizeInBytes must be between %s and a value less than the pool size %s.",
				utils.FmtUint64Bytes(minHotTierSize),
				utils.FmtUint64Bytes(perf.SizeInBytes)))
	}
	return nil
}

// ValidateCommonPoolParams validates parameters that are common to all pool types
func ValidateCommonPoolParams(perf *CustomPerformance) error {
	if perf.SizeInBytes%minSizeGranularity != 0 {
		return customerrors.NewUserInputValidationErr(
			fmt.Sprintf("Given pool size must be a multiple of %s", utils.FmtUint64Bytes(minSizeGranularity)))
	}

	if perf.QosType != utils.QosTypeAuto {
		if perf.QosType == utils.QosTypeManual {
			if !enableMqos {
				return customerrors.NewUserInputValidationErr(
					"Manual QoS is not enabled. Supported QoS type is auto")
			}
			return nil
		}
		if enableMqos {
			return customerrors.NewUserInputValidationErr(
				fmt.Sprintf("Given QoS type not supported for Unified Flex Storage Pool. Received '%s'. Supported QoS types are auto and manual", perf.QosType))
		}
		return customerrors.NewUserInputValidationErr(
			"Given QoS type not supported for Unified Flex Storage Pool. Supported QoS type is auto")
	}

	return nil
}

// Helper functions for performance parameters calculation and validation
func ValidateThroughputRange(throughput int64, minThroughput uint64, maxThroughput uint64) error {
	if t := uint64(throughput); t < minThroughput || t > maxThroughput {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf(
			"TotalThroughputMibps must be between %d and %d MiBps", minThroughput, maxThroughput))
	}
	return nil
}

func ValidateIopsRange(iops int64, minIops uint64, maxIops uint64) error {
	if t := uint64(iops); t < minIops || t > maxIops {
		return customerrors.NewUserInputValidationErr(fmt.Sprintf(
			"TotalIops must be between %d and %d IOPS", minIops, maxIops))
	}
	return nil
}

// Note: ValidateCommonPoolParamsForUpdate is no longer used since we now use the unified validation approach
// with CustomPerformance struct. The function has been removed to eliminate code duplication.

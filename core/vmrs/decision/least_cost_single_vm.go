// This file implements the DecisionMaker interface to make decisions based on a single VM's performance characteristics.

package decision

import (
	"math"
	"sort"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
)

// LeastCostSingleVMDecisionMaker implements the DecisionMaker interface for a single VM.
type LeastCostSingleVMDecisionMaker struct {
	config *vmrs.VMRSConfig
	// Sorted list of VMs based on their relative cost.
	vmsSortedByCost []vmrs.VMPerfLimit
	// Overall workload overheads computed from the VMRSConfig.
	overallWorkloadHeadroom vmrs.PerfAmplificationFactors
}

// Given a VMRSConfig, this function returns a new SingleVMDecisionMaker instance that is ready to make decisions based on the provided configuration.
func NewLeastCostSingleVMDecisionMaker(config *vmrs.VMRSConfig) *LeastCostSingleVMDecisionMaker {
	totalWorkloadHeadroom := computeWorkloadHeadroom(config)
	// When the customer requests for X IOPS/Y MiB/s, we need to over provision the disk so that the customer receives the requested performance, after accounting for the various overheads (ontap/workload).
	workloadHeadroomForCustomerScaling := vmrs.PerfAmplificationFactors{
		IOPS:       1.0 + totalWorkloadHeadroom.IOPS,
		Throughput: 1.0 + totalWorkloadHeadroom.Throughput,
	}

	// In order to determine if the VM can satisfy the customer request, we need to scale down the performance limits by the workload headroom.
	workloadHeadroomForDeterminingVM := vmrs.PerfAmplificationFactors{
		IOPS:       1.0 - totalWorkloadHeadroom.IOPS,
		Throughput: 1.0 - totalWorkloadHeadroom.Throughput,
	}
	return &LeastCostSingleVMDecisionMaker{
		config:                  config,
		vmsSortedByCost:         sortVMsByCost(config, workloadHeadroomForDeterminingVM),
		overallWorkloadHeadroom: workloadHeadroomForCustomerScaling,
	}
}

// FindOptimalVMs This implementation of DecisionMaker loops over the VMRSConfig, looking for the lowest cost VM that satisfies the customer request and volume count requirements.
func (d *LeastCostSingleVMDecisionMaker) FindOptimalVMs(config *vmrs.VMRSConfig, customerRequest vmrs.CustomerRequestedPerformance, currentConfig *vlm.VLMConfig) (*vmrs.Decision, error) {
	// VMSelectionPolicy is intentionally not applied here: active-active requires large-capacity
	// (see isTrueZonal in google-proxy pool_endpoints.go), which always routes to the large-volume
	// strategy, so single-VM only serves active-passive pools where any qualified VM is eligible.

	// Try to find a VM that meets performance requirements
	// If we have currentConfig, we can check if we need to consider volume count limits
	var currentVolumeCount int64 = 0
	var volumeLimits map[string]common.VolumeCountRange
	var currentInstance = ""

	// ConfigForPoolInstanceScaling is supposed to be non-nil only when we are dealing with volume create or delete operations on a pool.
	if currentConfig != nil && customerRequest.ConfigForPoolInstanceScaling != nil && customerRequest.ConfigForPoolInstanceScaling.VolLimitPerInstanceMap != nil {
		currentVolumeCount = customerRequest.ConfigForPoolInstanceScaling.CurrentVolCount
		volumeLimits = customerRequest.ConfigForPoolInstanceScaling.VolLimitPerInstanceMap
		currentInstance = customerRequest.ConfigForPoolInstanceScaling.CurrentInstanceType
	}

	canonicalCurrent, haveCanonicalCurrent := "", false
	if currentInstance != "" {
		canonicalCurrent, haveCanonicalCurrent = vmrs.CanonicalVMTypeInCatalog(d.vmsSortedByCost, currentInstance)
	}

	for _, vm := range d.vmsSortedByCost {
		if customerRequest.DesiredIOPS <= vm.OntapLimits.IOPS && customerRequest.DesiredThroughputInMiBs <= vm.OntapLimits.ThroughputInMiBs &&
			customerRequest.DesiredCapacityInGiB <= vm.OntapLimits.CapacityInGiB {
			if currentVolumeCount > 0 { // operation related to volume count limits
				if (haveCanonicalCurrent && vm.VMType == canonicalCurrent) ||
					currentVolumeCount < volumeLimits[vm.VMType].MinVolumeCount || currentVolumeCount > volumeLimits[vm.VMType].MaxVolumeCount { // skip the current instance type to avoid no-op scaling
					continue
				}
			}
			// The VM satisfies the customer request limits. When provisioning the VM, we need to upscale the customer requested performance by the overheads specified in the VMRSConfig.
			// Scale up the customer requested performance to account for the various overheads (ontap/workload/hotspotting) specified in the VMRSConfig.
			// We add a 1.0 to the overall workload headroom to account for the base performance that is always available.
			scaledCustomerRequest := d.config.ScaleCustomerRequestedPerformance(customerRequest, d.overallWorkloadHeadroom)

			// But, we also don't want to overprovision by more than what we can actually use.
			// If the VM supports a max. of V IOPS or MiB/s, we can provision the disk with a maximum of V * MaxOverprovisioningFactor IOPS or MiB/s.
			//
			// An example:
			// Let's say the customer requests 5120 MiB/s for the cluster and the VM supports a max. of 5200 MiB/s.
			// Let's assume that the amplification factor for IOPS is 1.1, and the workload headroom is 0.3 (30% headroom), and hotspotting is 0.1 (10% headroom).
			// In this case, the scaled customer request would be:
			// 5120 * 1.1 * 1.3 * 1.1 = 8053 MiB/s.
			// We don't want to provision 8 disks with 8053/8 = 1006.625 MiB/s each, because it's wasteful.
			//
			// Let's assume that VMRSConfig specifies a max. overprovisioning factor of 1.1 for throughput.
			// In this case, we can provision the cluster with a maximum of 5200 * 1.1 = 5720 MiB/s.
			// For 8 disks, this would mean that we can provision each disk with 5720/8 = 715 MiB/s.
			diskProvisioningLimits := vmrs.CustomerRequestedPerformance{
				DesiredIOPS:             min(scaledCustomerRequest.DesiredIOPS, int64(math.Ceil(float64(vm.DiskLimits.IOPS)*config.HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.IOPS))),
				DesiredThroughputInMiBs: min(scaledCustomerRequest.DesiredThroughputInMiBs, int64(math.Ceil(float64(vm.DiskLimits.ThroughputInMiBs)*config.HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.Throughput))),
				DesiredCapacityInGiB:    scaledCustomerRequest.DesiredCapacityInGiB,
			}

			return &vmrs.Decision{
				ChosenVMs:               []string{vm.VMType},
				StoragePoolRequirements: diskProvisioningLimits,
			}, nil
		}
	}

	// No VM satisfies the request
	return nil, &vmrs.NoSuitableVMError{
		Message:         "no suitable VM found for the customer request",
		CustomerRequest: customerRequest,
	}
}

// Compute the overall workload headroom as specified in the VMRSConfig.
func computeWorkloadHeadroom(config *vmrs.VMRSConfig) vmrs.PerfAmplificationFactors {
	totalHeadroom := vmrs.PerfAmplificationFactors{
		IOPS:       0.0,
		Throughput: 0.0,
	}

	// Iterate through each workload overhead and accumulate the amplification factors.
	for _, headroom := range config.HyperscalerPerfLimits.OntapOverheads.WorkloadHeadroom {
		totalHeadroom.IOPS += headroom.Headroom.IOPS
		totalHeadroom.Throughput += headroom.Headroom.Throughput
	}

	return totalHeadroom
}

// sortVMsByCost sorts the VMs in the VMRSConfig based on their relative cost.
func sortVMsByCost(config *vmrs.VMRSConfig, workloadHeadroom vmrs.PerfAmplificationFactors) []vmrs.VMPerfLimit {
	sortedVMs := make([]vmrs.VMPerfLimit, len(config.HyperscalerPerfLimits.DiskPerfLimits[0].QualifiedVMs))
	copy(sortedVMs, config.HyperscalerPerfLimits.DiskPerfLimits[0].QualifiedVMs)

	// Sort the VMs based on their relative cost
	sort.Slice(sortedVMs, func(i, j int) bool {
		return sortedVMs[i].RelativeCost < sortedVMs[j].RelativeCost
	})

	for i := range sortedVMs {
		sortedVMs[i].OntapLimits.IOPS = int64(math.Ceil(float64(sortedVMs[i].OntapLimits.IOPS) * workloadHeadroom.IOPS))
		sortedVMs[i].OntapLimits.ThroughputInMiBs = int64(math.Ceil(float64(sortedVMs[i].OntapLimits.ThroughputInMiBs) * workloadHeadroom.Throughput))
	}

	return sortedVMs
}

// GetVMsSortedByCost returns the sorted list of VMs by cost for external access
func (d *LeastCostSingleVMDecisionMaker) GetVMsSortedByCost() []vmrs.VMPerfLimit {
	return d.vmsSortedByCost
}

// CompareVMScalingDirection compares two VM types and determines if the change represents scaling up or down.
// Returns true if scaling up (new VM is more expensive), false if scaling down (new VM is cheaper).
// Returns error if either VM type is not found in the configuration.
func (d *LeastCostSingleVMDecisionMaker) CompareVMScalingDirection(currentInstanceType, newInstanceType string) (bool, error) {
	// Use shared utility function to find both VMs with early break optimization
	currentVM, newVM := vmrs.FindVMsByType(d.vmsSortedByCost, currentInstanceType, newInstanceType)

	// Validate that we found both VM types
	if currentVM == nil {
		return false, &vmrs.NoSuitableVMError{
			Message:         "current VM type not found in sorted list",
			CustomerRequest: vmrs.CustomerRequestedPerformance{},
		}
	}
	if newVM == nil {
		return false, &vmrs.NoSuitableVMError{
			Message:         "new VM type not found in sorted list",
			CustomerRequest: vmrs.CustomerRequestedPerformance{},
		}
	}

	// Determine scaling direction using RelativeCost
	// Higher RelativeCost means more expensive VM
	isScalingUp := newVM.RelativeCost > currentVM.RelativeCost

	return isScalingUp, nil
}

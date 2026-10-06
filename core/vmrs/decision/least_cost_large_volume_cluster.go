// Decision maker for large volume (FlexGroup) clusters using non-linear scaling.

package decision

import (
	"fmt"
	"math"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

var (
	LVHaPair = env.GetInt("NUMBER_OF_HA_PAIRS_LARGE_CAPACITY", 6)
)

// LargeVolumeHAPairs returns the number of HA pairs a large-volume cluster is deployed with:
// NUMBER_OF_HA_PAIRS_LARGE_CAPACITY, or EXPERIMENTAL_NUMBER_OF_HA_PAIRS_LARGE_CAPACITY when
// accountID is allowlisted. An empty accountID yields the non-allowlisted default.
//
// This is the single source of the HA pair count. The intercluster LIF carve calls it directly
// rather than running a VMRS decision, so the carve covers exactly the cluster FindOptimalVMs
// will later describe.
//
// accountID must be the same identifier FindOptimalVMs reads from
// Deployment.Labels["account_id"] — the account name, not a numeric database id — or the two
// callers disagree on how many HA pairs the pool gets.
func LargeVolumeHAPairs(accountID string) int {
	if accountID == "" {
		return LVHaPair
	}
	return int(utils.LvHaPairsForLargeVolume(accountID, int64(LVHaPair)))
}

func EffectiveLargeVolumeHAPairs(configured int64, accountID string) int64 {
	if configured != 0 {
		return configured
	}
	return int64(LargeVolumeHAPairs(accountID))
}

// LeastCostLargeVolumeClusterDecisionMaker implements the DecisionMaker interface for large volume (FlexGroup) clusters.
// It uses non-linear scaling factors to account for cluster overhead and selects optimal homogeneous VM configurations.
type LeastCostLargeVolumeClusterDecisionMaker struct {
	config                        *vmrs.VMRSConfig
	vmsSortedByCost               []vmrs.VMPerfLimit
	overallWorkloadHeadroom       vmrs.PerfAmplificationFactors
	nonLinearScalingActivePassive *vmrs.NonLinearScalingFactors
	nonLinearScalingActiveActive  *vmrs.NonLinearScalingFactors
}

// NewLeastCostLargeVolumeClusterDecisionMaker creates a new decision maker for large volume clusters.
// It pre-computes sorted VMs by cost and applies workload headroom for VM selection optimization.
// It scopes catalog selection to the process-wide MACHINE_FAMILY_TYPE (vmrs.MachineFamily);
// use NewLeastCostLargeVolumeClusterDecisionMakerForFamily to scope to an explicit family.
func NewLeastCostLargeVolumeClusterDecisionMaker(config *vmrs.VMRSConfig) *LeastCostLargeVolumeClusterDecisionMaker {
	return NewLeastCostLargeVolumeClusterDecisionMakerForFamily(config, vmrs.MachineFamily)
}

// NewLeastCostLargeVolumeClusterDecisionMakerForFamily is like
// NewLeastCostLargeVolumeClusterDecisionMaker but filters the VMRS catalog to the
// supplied machine family instead of the process-wide MACHINE_FAMILY_TYPE, so
// pool updates can keep an existing pool on the family it was created with.
func NewLeastCostLargeVolumeClusterDecisionMakerForFamily(config *vmrs.VMRSConfig, family vmrs.MachineFamilyType) *LeastCostLargeVolumeClusterDecisionMaker {
	totalWorkloadHeadroom := computeWorkloadHeadroom(config)
	forDetermining := vmrs.PerfAmplificationFactors{IOPS: 1.0 - totalWorkloadHeadroom.IOPS, Throughput: 1.0 - totalWorkloadHeadroom.Throughput}
	forProvisioning := vmrs.PerfAmplificationFactors{IOPS: 1.0 + totalWorkloadHeadroom.IOPS, Throughput: 1.0 + totalWorkloadHeadroom.Throughput}
	return &LeastCostLargeVolumeClusterDecisionMaker{
		config:                        config,
		vmsSortedByCost:               sortVMsByCost(config, forDetermining, family),
		overallWorkloadHeadroom:       forProvisioning,
		nonLinearScalingActivePassive: config.HyperscalerPerfLimits.NonLinearScalingActivePassive,
		nonLinearScalingActiveActive:  config.HyperscalerPerfLimits.NonLinearScalingActiveActive,
	}
}

// CompareVMScalingDirection compares two VM types and determines if the change represents scaling up or down.
// Returns true if scaling up (new VM is more expensive), false if scaling down (new VM is cheaper).
// Returns error if either VM type is not found in the configuration.
func (d *LeastCostLargeVolumeClusterDecisionMaker) CompareVMScalingDirection(currentInstanceType, newInstanceType string) (bool, error) {
	// Resolve against the FULL catalog (all families), not the family-filtered
	// vmsSortedByCost, so an existing pool whose instance type belongs to a
	// different family than the configured MACHINE_FAMILY_TYPE can still be
	// compared. Scaling direction is a pure RelativeCost comparison.
	// allQualifiedVMs surfaces an empty/malformed catalog as a typed
	// *vmrs.InvalidConfigError, so a config-loading problem isn't misattributed to
	// the generic "VM type not found" errors below.
	catalog, err := allQualifiedVMs(d.config)
	if err != nil {
		return false, err
	}
	currentVM, newVM := vmrs.FindVMsByType(catalog, currentInstanceType, newInstanceType)

	// Validate that we found both VM types with specific error messages
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
	return newVM.RelativeCost > currentVM.RelativeCost, nil
}

// FindOptimalVMs finds the optimal VM configuration for large volume clusters.
// It applies non-linear scaling factors to customer requirements, distributes load across HA pairs,
// and returns a homogeneous cluster configuration with the cheapest suitable VM type.
func (d *LeastCostLargeVolumeClusterDecisionMaker) FindOptimalVMs(config *vmrs.VMRSConfig, customerRequest vmrs.CustomerRequestedPerformance, currentConfig *vlm.VLMConfig) (*vmrs.Decision, error) {
	accountID := ""
	if currentConfig != nil && currentConfig.Deployment.Labels != nil {
		accountID = currentConfig.Deployment.Labels["account_id"]
	}
	requiredHAPairs := int(EffectiveLargeVolumeHAPairs(int64(customerRequest.LvHAPairs), accountID))
	scaledIOPSPerHaPair, scaledThroughputPerHaPair, err := d.applyNonLinearScaling(customerRequest.DesiredIOPS, customerRequest.DesiredThroughputInMiBs, requiredHAPairs, customerRequest.ScalingTopology.Topology)
	if err != nil {
		return nil, fmt.Errorf("failed to apply non-linear scaling: %w", err)
	}

	scaledCustomerReq := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             scaledIOPSPerHaPair,
		DesiredThroughputInMiBs: scaledThroughputPerHaPair,
		DesiredCapacityInGiB:    customerRequest.DesiredCapacityInGiB,
		ScalingTopology:         customerRequest.ScalingTopology,
	}

	vmType, err := d.findOptimalVMTypeForCluster(scaledCustomerReq, requiredHAPairs)
	if err != nil {
		return nil, err
	}

	layout := d.generateHomogeneousClusterLayout(vmType, requiredHAPairs, scaledCustomerReq)

	scaledWithOverheads := d.config.ScaleCustomerRequestedPerformance(customerRequest, d.overallWorkloadHeadroom)
	numNodes := requiredHAPairs * 2
	limits := vmrs.CustomerRequestedPerformance{
		DesiredIOPS:             min64(scaledWithOverheads.DesiredIOPS, int64(math.Ceil(float64(vmType.DiskLimits.IOPS)*config.HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.IOPS))*int64(numNodes)),
		DesiredThroughputInMiBs: min64(scaledWithOverheads.DesiredThroughputInMiBs, int64(math.Ceil(float64(vmType.DiskLimits.ThroughputInMiBs)*config.HyperscalerPerfLimits.MaxDiskOverprovisioningFactors.Throughput))*int64(numNodes)),
		DesiredCapacityInGiB:    scaledWithOverheads.DesiredCapacityInGiB,
	}

	return &vmrs.Decision{ChosenVMs: layout.VMTypes, StoragePoolRequirements: limits, ClusterMetadata: layout.ClusterMetadata}, nil
}

// findOptimalVMTypeForCluster finds the cheapest VM type that can satisfy the per-node requirements
// for a cluster with the specified number of HA pairs. It distributes the total requirements evenly
// across all nodes and selects the first (cheapest) VM that meets the per-node capacity, throughput, and IOPS needs.
func (d *LeastCostLargeVolumeClusterDecisionMaker) findOptimalVMTypeForCluster(scaledCustomerReq vmrs.CustomerRequestedPerformance, haPairs int) (*vmrs.VMPerfLimit, error) {
	selectionPolicy := scaledCustomerReq.ScalingTopology.VMSelectionPolicy
	vms, err := vmrs.FilterCandidatesByPolicy(d.vmsSortedByCost, selectionPolicy)
	if err != nil {
		return nil, fmt.Errorf("VM candidate selection failed for %s topology (eligibleFamilies=%v, minimumMachineType=%q, requireLSSD=%t): %w",
			scaledCustomerReq.ScalingTopology.Topology, selectionPolicy.EligibleFamilies, selectionPolicy.MinimumMachineType, selectionPolicy.RequireLSSD, err)
	}

	// Active-passive is the default; only explicit active-active gives LIFs on both nodes.
	var numLIFs int
	if scaledCustomerReq.ScalingTopology.Topology == vmrs.TopologyActiveActive {
		numLIFs = haPairs * 2 // Both nodes in each HA pair have LIFs
	} else {
		numLIFs = haPairs // Only active nodes have LIFs
	}
	capacityPerNode := scaledCustomerReq.DesiredCapacityInGiB / int64(numLIFs)
	// vms may alias d.vmsSortedByCost (when the policy is unrestricted), so the returned
	// pointer references shared state and must be treated as read-only by callers.
	for i := range vms {
		vm := &vms[i]
		if vm.OntapLimits.CapacityInGiB >= capacityPerNode && vm.OntapLimits.ThroughputInMiBs >= scaledCustomerReq.DesiredThroughputInMiBs && vm.OntapLimits.IOPS >= scaledCustomerReq.DesiredIOPS {
			return vm, nil
		}
	}
	return nil, fmt.Errorf("no suitable VM type found for cluster requirements")
}

// ClusterLayout represents the physical layout and configuration of a large volume cluster.
// It contains the VM types for each node, cluster sizing information, and metadata about the deployment.
type ClusterLayout struct {
	VMTypes         []string
	NumNodes        int
	NumHAPairs      int
	NumLIFs         int
	ClusterMetadata *vmrs.ClusterMetadata
}

// generateHomogeneousClusterLayout creates a cluster layout where all nodes use the same VM type.
// It calculates the per-node resource distribution and generates cluster metadata for deployment.
// Returns a ClusterLayout with uniform VM types across all nodes in the cluster.
func (d *LeastCostLargeVolumeClusterDecisionMaker) generateHomogeneousClusterLayout(vm *vmrs.VMPerfLimit, haPairs int, scaledCustomerReq vmrs.CustomerRequestedPerformance) *ClusterLayout {
	numNodes := haPairs * 2

	// Calculate numLIFs based on scaling mode.
	// Active-Active: Both nodes in each HA pair have LIFs (haPairs * 2).
	// Active-Passive: Only active node in each HA pair has LIF (haPairs * 1).
	var numLIFs int
	if scaledCustomerReq.ScalingTopology.Topology == vmrs.TopologyActiveActive {
		numLIFs = haPairs * 2 // Both active and passive nodes have LIFs
	} else {
		numLIFs = haPairs // Only active nodes have LIFs
	}
	capacityPerNode := scaledCustomerReq.DesiredCapacityInGiB / int64(numLIFs)
	throughputPerNode := scaledCustomerReq.DesiredThroughputInMiBs / 2
	iopsPerNode := scaledCustomerReq.DesiredIOPS / 2
	vmTypes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		vmTypes[i] = vm.VMType
	}
	meta := &vmrs.ClusterMetadata{NumHAPairs: haPairs, NumNodes: numNodes, NumLIFs: numLIFs, IsHomogeneous: true, VMType: vm.VMType, CapacityPerNode: capacityPerNode, ThroughputPerNode: throughputPerNode, IOPSPerNode: iopsPerNode}
	return &ClusterLayout{VMTypes: vmTypes, NumNodes: numNodes, NumHAPairs: haPairs, NumLIFs: numLIFs, ClusterMetadata: meta}
}

// applyNonLinearScaling applies non-linear scaling factors to IOPS and throughput based on the number of HA pairs.
// For clusters with multiple HA pairs, performance doesn't scale linearly due to coordination overhead.
// The caller supplies the effective topology (resolved once via the ScalingTopology)
// Returns scaled IOPS and throughput values that account for cluster inefficiencies, or an error if scaling configuration is missing.
func (d *LeastCostLargeVolumeClusterDecisionMaker) applyNonLinearScaling(customerIOPS, customerThroughput int64, haPairs int, topology vmrs.DeploymentTopology) (int64, int64, error) {
	// An unset topology defaults to active-passive, matching the default deployment topology.
	if topology == "" {
		topology = vmrs.TopologyActivePassive
	}
	var scalingConfig *vmrs.NonLinearScalingFactors
	// Choose scaling configuration based on topology. Active-passive is the
	// default, only explicit active-active uses the active-active factors.
	if topology == vmrs.TopologyActiveActive {
		scalingConfig = d.nonLinearScalingActiveActive
	} else {
		scalingConfig = d.nonLinearScalingActivePassive
	}

	// Return error if no scaling configuration is available.
	if scalingConfig == nil {
		return 0, 0, fmt.Errorf("no non-linear scaling configuration found for %s topology", topology)
	}

	iopsFactor, err := d.getScalingFactor(scalingConfig.IOPSFactors, haPairs, scalingConfig.BaseFactor)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get IOPS scaling factor: %w", err)
	}

	thrFactor, err := d.getScalingFactor(scalingConfig.ThroughputFactors, haPairs, scalingConfig.BaseFactor)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get throughput scaling factor: %w", err)
	}

	iopsFactor = math.Min(iopsFactor, scalingConfig.MaxScalingFactor)
	thrFactor = math.Min(thrFactor, scalingConfig.MaxScalingFactor)
	return int64(math.Ceil(float64(customerIOPS) / iopsFactor)), int64(math.Ceil(float64(customerThroughput) / thrFactor)), nil
}

// getScalingFactor retrieves the scaling factor for a given number of HA pairs.
// If an exact match is found, returns that factor. Otherwise, returns an error.
// No longer falls back to baseFactor - requires explicit configuration for the HA pair count.
func (d *LeastCostLargeVolumeClusterDecisionMaker) getScalingFactor(factors map[int]float64, haPairs int, baseFactor float64) (float64, error) {
	if factor, ok := factors[haPairs]; ok {
		return factor, nil
	}
	return 0, fmt.Errorf("no scaling factor configured for %d HA pairs", haPairs)
}

// min64 returns the smaller of two int64 values.
// This utility function is used for capacity and performance limit calculations.
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

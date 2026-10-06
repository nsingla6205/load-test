package vmrs

// DeploymentTopology is the semantic deployment mode of a storage pool.
type DeploymentTopology string

const (
	TopologyActivePassive      DeploymentTopology = "active-passive"
	TopologyActiveActive       DeploymentTopology = "active-active"
	TopologyZonalActivePassive DeploymentTopology = "zonal-active-passive"
)

// UsesSharedHADeployment reports whether a pool with this topology deploys as VLM's
// shared_ha type.
func (t DeploymentTopology) UsesSharedHADeployment() bool {
	return t == TopologyActiveActive || t == TopologyZonalActivePassive
}

// RequiresMediator reports whether a pool with this topology needs a mediator provisioned.
// shared_ha deployments have no mediator VM, so neither single-zone topology uses one.
func (t DeploymentTopology) RequiresMediator() bool {
	return !t.UsesSharedHADeployment()
}

// EnableAAConfig reports whether active-active configuration must be enabled on the deployment.
func (t DeploymentTopology) EnableAAConfig() bool {
	return t == TopologyActiveActive
}

// VMSelectionPolicy constrains the set of VM candidates a decision maker may choose from.
type VMSelectionPolicy struct {
	// EligibleFamilies is the set of machine-family prefixes (e.g. "c3") a candidate must
	// belong to. Empty means any family is eligible.
	EligibleFamilies []string
	// MinimumMachineType, when set, is the floor machine type: candidates cheaper than this
	// type (by RelativeCost) are discarded.
	MinimumMachineType string
	// RequireLSSD, when true, keeps only candidates that have LSSD.
	RequireLSSD bool
}

// IsUnrestricted tell whether the policy imposes no candidate restriction.
func (p VMSelectionPolicy) IsUnrestricted() bool {
	return len(p.EligibleFamilies) == 0 && p.MinimumMachineType == "" && !p.RequireLSSD
}

// ScalingTopology captures the topology-specific decision inputs consumed by the VMRS decision
// makers.
type ScalingTopology struct {
	Topology          DeploymentTopology
	VMSelectionPolicy VMSelectionPolicy
}

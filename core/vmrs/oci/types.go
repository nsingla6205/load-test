// Package oci implements VMRS (VM Right Sizing) for OCI block-volume + VM
// DenseIO.E5.Flex deployments.
//
// Selection model
// ---------------
// The reference table is encoded as:
//
//	throughput:                  Map keyed by headline throughput in
//	                             decimal GB/s. The selector indexes
//	                             directly with ceil(req.throughput_gbs)
//	                             for O(1) flex lookup.
//	  <int GB/s>:
//	    instances:               Map of qualified OCI VM shapes that
//	                             deliver this throughput tier. Map key
//	                             is the shape name (LaunchInstance.shape).
//	                             Today only VM.DenseIO.E5.Flex is
//	                             qualified; future shapes (E4.Flex, bare
//	                             metal, etc.) sit alongside as siblings.
//	      <shape>:
//	        ocpus:               OCPUs to provision (shapeConfig.ocpus).
//	        memory_gbs:          Memory in decimal GB to provision
//	                             (shapeConfig.memoryInGBs).
//	        vpu:                 Per-shape VPU table (different shapes
//	                             can have different per-VM capacity
//	                             floors at the same VPU band).
//	          vpu_<N>:           VPU band (vpu_30 in today's catalogue).
//	            capacity_throughput_tb  Minimum capacity PER VSA VM (per
//	                                    node), in decimal TB, at which
//	                                    the cell delivers its row of
//	                                    (full tier throughput, listed
//	                                    IOPS). NOT per-disk and NOT
//	                                    per-HA-pair — see VPUCell's
//	                                    doc comment for the AA/AP
//	                                    multiplier rules.
//	            iops                    IOPS at that capacity.
//
// The selector picks `throughput[bucket]` in O(1), then iterates the
// tier's instances in ascending OCPU order (alphabetical-on-shape-name
// as a deterministic tiebreak) and for each instance walks its `vpu:`
// map ascending in VPU number (vpu_<N>) — returning
// the first (instance, VPU) pair whose capacity floor fits. IOPS is
// accepted on the request for forward-compat but is not used for
// filtering today (see the SingleVMSelector doc).
//
// Note: there is no cost field anywhere in the catalogue. VPU numbers
// are a perf rank, not a price rank; the ascending-VPU walk reflects
// "try the lower-perf band first because its capacity floor is higher,
// then step up if it doesn't fit" — not any cost optimization.
//
// Assumption: integer 1-GB/s tier steps
// -------------------------------------
// The throughput catalogue increases in 1-GB/s steps (1, 2, 3, …). The
// selector computes the tier with `bucket = ceil(req.throughput_gbs)`,
// so any fractional request silently rounds up to the next integer
// tier. If sub-GB/s granularity ever becomes a real requirement (e.g.
// adding a 1.5 GB/s tier), both the YAML keys and this bucketing logic
// must be changed together — don't add a fractional key alone, the
// ceil-based lookup wouldn't find it.
package oci

import (
	"fmt"
	"regexp"
	"strconv"
)

// Config is the top-level VMRS configuration for OCI. It is decoded from
// config/vmrs_oci.yaml.
type Config struct {
	// Throughput maps headline GB/s → tier. Integer keys; the selector
	// looks up ceil(request_gbs) directly.
	Throughput map[int]ThroughputTier `yaml:"throughput"`
}

// ThroughputTier is one entry under `throughput:` — the qualified VM
// shapes for this tier. Each shape carries its own provisioning details
// and per-volume VPU table.
type ThroughputTier struct {
	// Instances maps OCI shape name (LaunchInstance.shape) → per-shape
	// provisioning details. Today contains a single entry for
	// "VM.DenseIO.E5.Flex"; designed so additional shapes can be added
	// without restructuring the YAML.
	Instances map[string]Instance `yaml:"instances"`
}

// Instance is the per-shape provisioning info for one throughput tier.
// More fields (boot_volume_gb, …) can be added here as LaunchInstance
// requirements grow.
type Instance struct {
	// OCPUs is the OCPU count this shape provisions at this throughput
	// tier. Passed to LaunchInstance.shapeConfig.ocpus verbatim.
	OCPUs int `yaml:"ocpus"`

	// MemoryGBs is the memory in decimal GB this shape provisions at
	// this throughput tier. Passed to
	// LaunchInstance.shapeConfig.memoryInGBs verbatim. Must stay within
	// OCI's per-shape flex memory range (E5.Flex: 1–16 GB per OCPU). The
	// catalogue carries memory per (tier, shape) rather than as a
	// per-OCPU multiplier so the perf team can deviate from a flat ratio
	// for specific tiers without code changes.
	MemoryGBs float64 `yaml:"memory_gbs"`

	// VPU maps "vpu_<N>" → VPUCell. The VPU table belongs to the
	// instance (not the tier) because different shapes can deliver
	// different per-volume capacities at the same VPU band.
	VPU map[string]VPUCell `yaml:"vpu"`
}

// VPUCell is one (throughput × VPU) cell of the reference table.
//
// IMPORTANT: capacity numbers in this struct are PER VSA VM (per node),
// not per HA pair and not per individual block volume / disk. The
// catalogue mirrors the OCI perf-team "OCPU to Capacity Per VM" table
// — for a 1 GB/s tier under vpu_30 the floor is 1.25 TB per VM, which
// becomes 1.46 TB per HA pair in Active-Active (both VMs serve) and
// 0.73 TB per HA pair in Active-Passive (only primary serves). The
// pool-create workflow's computeOCIVMRSInput is responsible for
// dividing the customer's total pool (capacity, throughput) by
// totalActiveVMs before populating CustomerRequest, so that selector
// inputs land in the same unit space as these floors.
type VPUCell struct {
	// CapacityThroughputTB is the minimum capacity per VM (decimal TB)
	// at which this cell delivers (full tier throughput, listed IOPS).
	// A request below this floor falls through to a higher VPU within
	// the same tier.
	CapacityThroughputTB float64 `yaml:"capacity_throughput_tb"`

	// IOPS is the IOPS delivered at CapacityThroughputTB. Under vpu_30
	// these values are below the instance IOPS cap (throughput-bound).
	IOPS int64 `yaml:"iops"`
}

// CustomerRequest is the input to the Selector.
type CustomerRequest struct {
	// DesiredCapacityTB is the storage the customer wants, in decimal TB.
	DesiredCapacityTB float64

	// DesiredThroughputGBs is the target throughput in decimal GB/s. The
	// selector picks throughput[ceil(this)] from the catalogue.
	DesiredThroughputGBs float64

	// DesiredIOPS is currently IGNORED by the selector — selection is
	// driven by (capacity, throughput) only. The field is retained on
	// the request struct so callers can already plumb their IOPS target
	// through and the API won't change when IOPS-based filtering is
	// re-introduced. Until then, passing nil or any positive value
	// produces the same result. See SingleVMSelector's algorithm doc
	// for the rationale.
	DesiredIOPS *int64
}

// Decision is the Selector's output.
type Decision struct {
	// VMShape is the chosen OCI VM shape name (the key under
	// `instances:` in YAML), e.g. "VM.DenseIO.E5.Flex".
	// Feeds LaunchInstance.shape verbatim.
	VMShape string

	// OCPUs is the OCPU count to provision for this tier+shape.
	// Feeds LaunchInstance.shapeConfig.ocpus verbatim.
	OCPUs int

	// MemoryGBs is the memory (decimal GB) to provision for this
	// tier+shape. Feeds LaunchInstance.shapeConfig.memoryInGBs verbatim.
	MemoryGBs float64

	// ThroughputGBs is the headline throughput tier (the YAML map key)
	// the request landed on.
	ThroughputGBs int

	// VPUName is the YAML key — e.g. "vpu_50".
	VPUName string
	// VPU parsed from VPUName ("vpu_50" → 50).
	VPU int

	// CapacityTB echoes the requested capacity (decimal TB).
	CapacityTB float64

	// IOPS is VPUCell.IOPS for the chosen (throughput tier, VPU).
	IOPS int64
}

// ---------------------------------------------------------------------------
// Key parsing helpers
// ---------------------------------------------------------------------------

var vpuKeyRe = regexp.MustCompile(`^vpu_(\d+)$`)

// parseVPUNumber extracts the VPU number from a "vpu_<N>" key.
func parseVPUNumber(key string) (int, error) {
	m := vpuKeyRe.FindStringSubmatch(key)
	if m == nil {
		return 0, fmt.Errorf("invalid vpu key %q (expected vpu_<N>)", key)
	}
	// The regex (\d+) guarantees the captured group is non-empty digits,
	// but Atoi can still fail on overflow (e.g. vpu_99999999999999999999).
	// Surface that as "invalid key" rather than silently dropping it and
	// then producing a misleading "non-positive VPU number" below.
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid vpu key %q (expected vpu_<N>): %w", key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("vpu key %q has non-positive VPU number", key)
	}
	return n, nil
}

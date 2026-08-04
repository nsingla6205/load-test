// Package oci implements VMRS (VM Right Sizing) for OCI block-volume + VM
// DenseIO.E5.Flex deployments.
//
// Selection model
// ---------------
// The reference table is encoded as:
//
//	throughput:                  Map keyed by customer-visible headroom
//	                             throughput in decimal MB/s. The selector
//	                             indexes directly with req.throughput_mbps,
//	                             which must be an exact tier key (a positive
//	                             multiple of 800); non-multiples are rejected.
//	  <int MB/s>:                Keys advance in 800 MB/s steps:
//	                             800, 1600, 2400, 3200, 4000, 4800.
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
//	            min_capacity_tb         Minimum capacity PER VSA VM (per
//	                                    node), in decimal TB, needed to
//	                                    deliver this tier. NOT per-disk and
//	                                    NOT per-HA-pair — see VPUCell's
//	                                    doc comment for the AA/AP
//	                                    multiplier rules.
//	            default_iops            Pool-level IOPS used when the
//	                                    customer omits IOPS.
//	            max_iops                Maximum pool-level IOPS the customer
//	                                    may request; echoed as Decision.IOPS.
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
// Assumption: 800 MB/s headroom tier steps
// -----------------------------------------
// The throughput catalogue advances in 800 MB/s steps matching OCI's
// headroom values (800, 1600, …, 4800 MB/s). Requests must land on an
// exact tier key: Select rejects any throughput that is not a positive
// multiple of 800 rather than rounding. (Downgrade detection during IOPS
// resolution does round the *current* stored throughput up to a tier via
// ceil(current/800)*800 — see ResolveIOPSFromDecision.) If new tiers are
// added at a different step size, both the YAML keys and this step size
// must be updated together.
package oci

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// headroomStepMBps is the catalogue step size in MB/s. All tier keys are
// multiples of this value (800, 1600, …, 4800). Used to validate request
// throughput (which must be an exact multiple) and for downgrade detection
// in IOPS resolution (ceil(current/step)*step).
const headroomStepMBps = 800

// Config is the top-level VMRS configuration for OCI. It is decoded from
// config/vmrs_oci.yaml.
type Config struct {
	// Throughput maps customer-visible headroom MB/s → tier. Integer keys
	// in 800 MB/s steps (800, 1600, …, 4800); the selector looks up the
	// requested MB/s directly and rejects any non-multiple of 800.
	Throughput map[int]ThroughputTier `yaml:"throughput"`
}

// ThroughputTier is one entry under `throughput:` — the qualified VM
// shapes for this tier. Each shape carries its own provisioning details
// and per-volume VPU table. IOPS bounds live under instances.<shape>.vpu.vpu_30.
// The YAML map key itself is the customer-effective headroom in MB/s.
type ThroughputTier struct {
	// ThroughputWithHeadroom is the OCI max-egress throughput (MB/s) from the
	// catalogue (e.g. 1000 when the headroom tier key is 800).
	ThroughputWithHeadroom int64 `yaml:"throughput_with_headroom,omitempty"`

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
// catalogue mirrors the OCI perf-team "per Node" table — for the
// 800 MB/s headroom tier under vpu_30 the floor is 1.26 TB per VM,
// which becomes ~2.52 TB per HA pair in Active-Active (both VMs serve)
// and ~1.26 TB per HA pair in Active-Passive (only primary serves).
// The DecideOCIVMRS activity is responsible for dividing the customer's
// total pool (capacity, throughput) by totalActiveVMs before populating
// CustomerRequest, so that selector inputs land in the same unit space
// as these floors.
type VPUCell struct {
	// DefaultIOPS is the pool-level IOPS used when the customer omits IOPS.
	DefaultIOPS int64 `yaml:"default_iops"`
	// MaxIOPS is the maximum pool-level IOPS allowed for this tier. It is
	// also echoed back as Decision.IOPS for the chosen cell.
	MaxIOPS int64 `yaml:"max_iops"`
	// MinCapacityTB is the minimum capacity per VM (decimal TB) needed to
	// deliver this tier. A request below this floor falls through to a
	// higher VPU within the same tier.
	MinCapacityTB float64 `yaml:"min_capacity_tb"`
}

// CustomerRequest is the input to the Selector.
type CustomerRequest struct {
	// DesiredCapacityTB is the storage the customer wants, in decimal TB.
	DesiredCapacityTB float64

	// DesiredThroughputMBps is the target throughput in decimal MB/s.
	// It must be an exact tier key (a positive multiple of 800); the
	// selector looks up throughput[this] and rejects any other value.
	DesiredThroughputMBps float64

	// DesiredIOPS is the customer's explicit IOPS (nil = use tier default).
	// It does NOT influence shape/VPU selection (that is driven by
	// capacity + throughput only — see SingleVMSelector's algorithm doc).
	// Decide uses it to resolve Decision.ResolvedIOPS: explicit values are
	// validated within [DefaultIOPS, MaxIOPS] of the chosen tier.
	DesiredIOPS *int64

	// CurrentThroughputMBps is the pool's current throughput in MB/s (0 on create).
	// Decide uses it to detect a throughput downgrade when resolving IOPS:
	// a downgrade with no explicit DesiredIOPS preserves the current IOPS,
	// capped at the new tier's MaxIOPS.
	CurrentThroughputMBps int64

	// CurrentIOPS is the pool's current IOPS (0 on create or when unknown). On a
	// throughput downgrade or same-tier update with no explicit DesiredIOPS, the
	// resolved IOPS is clamp(CurrentIOPS, DefaultIOPS, MaxIOPS) of the new tier.
	CurrentIOPS int64
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

	// Throughput is the YAML map key (customer-visible headroom MB/s)
	// the request landed on (e.g. 800 for the 8-OCPU tier). Sent to VLM
	// on SPConfig.Throughput.
	Throughput int

	// VPUName is the YAML key — e.g. "vpu_30".
	VPUName string
	// VPU parsed from VPUName ("vpu_30" → 30).
	VPU int

	// CapacityTB echoes the requested capacity (decimal TB).
	CapacityTB float64

	// IOPS is VPUCell.MaxIOPS for the chosen (throughput tier, VPU).
	IOPS int64

	// ThroughputWithHeadroom is the OCI max-egress throughput (MB/s) from
	// the catalogue (e.g. 1000 when Throughput is 800).
	ThroughputWithHeadroom int64

	// DefaultIOPS is the pool-level IOPS used when the customer omits
	// IOPS (sourced from the tier's vpu_30 cell).
	DefaultIOPS int64

	// ResolvedIOPS is the final IOPS to send to VLM, set by Decide after
	// applying the IOPS rules (nil→default, explicit→validated,
	// downgrade or same-tier with no explicit→clamp(current, default, max)).
	ResolvedIOPS int64
}

// ---------------------------------------------------------------------------
// Pool-level IOPS resolution
// ---------------------------------------------------------------------------

// ResolveIOPSFromDecision validates or defaults IOPS using the Decision.
// Rules (evaluated in order):
//  1. requestedIOPS != nil → validate within [DefaultIOPS, MaxIOPS]; return error if out of range.
//  2. throughput same-or-downgrade (currentThroughputMBps > 0, target tier ≤ current tier)
//     with no explicit IOPS → preserve currentIOPS, clamped to [DefaultIOPS, MaxIOPS].
//     If currentIOPS is 0 (unknown), fall back to DefaultIOPS.
//  3. otherwise (upgrade or create) → DefaultIOPS.
func ResolveIOPSFromDecision(decision *Decision, requestedIOPS *int64, currentThroughputMBps, currentIOPS int64) (int64, error) {
	if decision == nil {
		return 0, fmt.Errorf("decision is nil")
	}
	if requestedIOPS != nil {
		return validateIOPSRange(*requestedIOPS, decision.DefaultIOPS, decision.IOPS, decision.Throughput)
	}
	if currentThroughputMBps > 0 {
		oldBucket := int(math.Ceil(float64(currentThroughputMBps)/headroomStepMBps)) * headroomStepMBps
		if oldBucket < headroomStepMBps {
			oldBucket = headroomStepMBps
		}
		// Same tier (capacity-only update) or downgrade: preserve the customer's
		// current IOPS. Clamp to [DefaultIOPS, MaxIOPS] of the new tier so we
		// never send a value outside the tier's valid range.
		if decision.Throughput <= oldBucket {
			if currentIOPS > 0 {
				preserved := currentIOPS
				if preserved > decision.IOPS {
					preserved = decision.IOPS
				}
				if preserved < decision.DefaultIOPS {
					preserved = decision.DefaultIOPS
				}
				return preserved, nil
			}
			// currentIOPS unknown (0): safe fallback is DefaultIOPS.
			return decision.DefaultIOPS, nil
		}
	}
	return decision.DefaultIOPS, nil
}

func validateIOPSRange(iops, minIOPS, maxIOPS int64, tierMBps int) (int64, error) {
	if iops < minIOPS {
		return 0, fmt.Errorf(
			"IOPS %d is below the minimum %d for the %d MB/s throughput tier",
			iops, minIOPS, tierMBps,
		)
	}
	if iops > maxIOPS {
		return 0, fmt.Errorf(
			"IOPS %d exceeds the maximum %d for the %d MB/s throughput tier",
			iops, maxIOPS, tierMBps,
		)
	}
	return iops, nil
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

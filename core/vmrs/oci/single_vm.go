// SingleVMSelector implements the Selector interface for OCI.
//
// Algorithm
// ---------
//
//  1. Pick the throughput tier in O(1):
//
//         bucket = ceil(req.DesiredThroughputGBs)
//         tier   = config.Throughput[bucket]
//
//     If `bucket` isn't in the map (e.g., request > 6 GB/s today), return
//     *NoFeasibleSelectionError.
//
//  2. Walk the tier's instances in ascending OCPU order (alphabetical on
//     shape name as a deterministic tiebreak when OCPUs are equal — only
//     matters once multiple shapes coexist at the same tier). For each
//     instance, walk its `vpu:` map in ascending VPU number (vpu_<N>) and
//     return the first cell whose `capacity_throughput_tb` is ≤ the
//     requested capacity.
//
//     Why ascending VPU order: OCI VPU numbers are a perf rank, not a
//     price rank, and the catalogue carries no pricing. When multiple
//     VPU bands coexist at a tier, lower VPU bands require *more* capacity
//     to hit the same throughput tier, so trying them first lets the
//     larger-capacity customer land on a lower-VPU cell that still fits,
//     and steps up to higher VPU bands only when the lower band's
//     capacity floor exceeds the request. There is no "cheapest" tiebreak
//     baked in. Today's catalogue carries a single vpu_30 band per tier.
//
//     IOPS handling: req.DesiredIOPS is intentionally NOT consulted for
//     filtering today — selection is driven by (capacity, throughput)
//     only. The chosen cell's iops is still echoed back in the Decision
//     so callers see what the volume will deliver. When IOPS-based
//     filtering is re-introduced, requests already populating
//     DesiredIOPS will start being honored without an API change.
//
//     Today every tier has exactly one instance (VM.DenseIO.E5.Flex), so
//     the instance loop is trivial; the structure is in place for future
//     tiers that qualify multiple shapes.
//
//     For the canonical example — req: 9.00 TB, 6 GB/s — bucket=6,
//     instance=VM.DenseIO.E5.Flex (48 OCPUs), and:
//         vpu_30: floor 7.5 TB  →  9.00 ≥ 7.5, MATCH → returned
//
//  3. If no (instance, VPU) pair fits → *NoFeasibleSelectionError.

package oci

import (
	"fmt"
	"math"
	"sort"
)

// SingleVMSelector implements the "single VM, ascending-VPU" selection
// strategy described in the package-level algorithm comment.
//
// Naming note: the GCP-side VMRS calls its analogous strategy
// LeastCostSingleVM because GCP machine types + disks carry explicit
// cost weights that the selector minimizes. OCI's catalogue has no such
// pricing data — VPU numbers are a perf rank only — so the OCI version
// is just SingleVM (single VM, no cost minimization).
type SingleVMSelector struct {
	// tiers is the catalogue keyed by throughput bucket (decimal GB/s).
	tiers map[int]preparedTier

	// maxBucket is the largest throughput key in the catalogue — used
	// only for diagnostic messages when a request exceeds the table.
	maxBucket int
}

// preparedTier is the parsed, sorted view of one throughput tier.
type preparedTier struct {
	// instances is sorted ascending: OCPUs, then shape name. Today there
	// is exactly one instance per tier, so the ordering only matters
	// once multiple shapes coexist at the same tier.
	instances []preparedInstance
}

// preparedInstance is one entry from a tier's `instances:` map with its
// VPU list pre-sorted ascending by VPU number (vpu_<N>).
type preparedInstance struct {
	shape     string // YAML key, e.g. "VM.DenseIO.E5.Flex"
	ocpus     int
	memoryGBs float64
	vpus      []vpuEntry
}

// vpuEntry is the parsed view of one YAML vpu_<N> entry.
type vpuEntry struct {
	name   string // YAML key, e.g. "vpu_50"
	number int    // 50 for "vpu_50"
	cell   VPUCell
}

// NewSingleVMSelector builds the selector and validates the catalogue:
// throughput keys must be positive, each tier must have at least one
// instance with positive OCPUs and positive memory_gbs, each instance
// must have at least one VPU cell with positive capacity/IOPS, and vpu
// keys must match vpu_<N>.
func NewSingleVMSelector(cfg *Config) (*SingleVMSelector, error) {
	if cfg == nil {
		return nil, &InvalidConfigError{Message: "config cannot be nil"}
	}
	if len(cfg.Throughput) == 0 {
		return nil, &InvalidConfigError{Message: "config has no throughput tiers"}
	}

	tiers := make(map[int]preparedTier, len(cfg.Throughput))
	maxBucket := 0
	for bucket, tier := range cfg.Throughput {
		if bucket <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf("throughput key %d must be > 0", bucket),
			}
		}

		instances, err := prepareInstances(bucket, tier.Instances)
		if err != nil {
			return nil, err
		}

		tiers[bucket] = preparedTier{instances: instances}
		if bucket > maxBucket {
			maxBucket = bucket
		}
	}

	return &SingleVMSelector{
		tiers:     tiers,
		maxBucket: maxBucket,
	}, nil
}

// prepareInstances validates and sorts the `instances:` map for one tier.
// Each instance's VPU list is also validated and sorted ascending.
// Instance sort key is (OCPUs asc, shape name asc) — purely for
// deterministic iteration when multiple shapes coexist at one tier.
func prepareInstances(bucket int, in map[string]Instance) ([]preparedInstance, error) {
	if len(in) == 0 {
		return nil, &InvalidConfigError{
			Message: fmt.Sprintf("throughput %d: no instances qualified", bucket),
		}
	}
	out := make([]preparedInstance, 0, len(in))
	for shape, inst := range in {
		if shape == "" {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf("throughput %d: empty instance shape key", bucket),
			}
		}
		if inst.OCPUs <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf("throughput %d shape %q: ocpus must be > 0", bucket, shape),
			}
		}
		if inst.MemoryGBs <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf("throughput %d shape %q: memory_gbs must be > 0", bucket, shape),
			}
		}
		vpus, err := prepareVPUs(bucket, shape, inst.VPU)
		if err != nil {
			return nil, err
		}
		out = append(out, preparedInstance{
			shape:     shape,
			ocpus:     inst.OCPUs,
			memoryGBs: inst.MemoryGBs,
			vpus:      vpus,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ocpus != out[j].ocpus {
			return out[i].ocpus < out[j].ocpus
		}
		return out[i].shape < out[j].shape
	})
	return out, nil
}

// prepareVPUs validates and sorts the `vpu:` map for one instance. Sort
// key is VPU number ascending — the selector walks vpu_<N> bands and
// returns the first whose capacity floor fits the request.
func prepareVPUs(bucket int, shape string, in map[string]VPUCell) ([]vpuEntry, error) {
	if len(in) == 0 {
		return nil, &InvalidConfigError{
			Message: fmt.Sprintf("throughput %d shape %q: no VPU cells", bucket, shape),
		}
	}
	out := make([]vpuEntry, 0, len(in))
	for vpuKey, cell := range in {
		num, err := parseVPUNumber(vpuKey)
		if err != nil {
			return nil, &InvalidConfigError{Message: err.Error()}
		}
		if cell.CapacityThroughputTB <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf(
					"throughput %d shape %q vpu %q: capacity_throughput_tb must be > 0",
					bucket, shape, vpuKey),
			}
		}
		if cell.IOPS <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf(
					"throughput %d shape %q vpu %q: iops must be > 0",
					bucket, shape, vpuKey),
			}
		}
		out = append(out, vpuEntry{name: vpuKey, number: num, cell: cell})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	return out, nil
}

// Select implements Selector.Select.
func (s *SingleVMSelector) Select(req CustomerRequest) (*Decision, error) {
	if req.DesiredThroughputGBs <= 0 {
		return nil, &NoFeasibleSelectionError{
			Message: "DesiredThroughputGBs must be > 0",
			Request: req,
		}
	}
	if req.DesiredCapacityTB <= 0 {
		return nil, &NoFeasibleSelectionError{
			Message: "DesiredCapacityTB must be > 0",
			Request: req,
		}
	}
	// req.DesiredIOPS is intentionally not validated or used today; see
	// the package-level algorithm comment for the rationale and the
	// forward path.

	// Assumption: catalogue tiers advance in integer 1-GB/s steps, so
	// ceil(request) is a lossless map into the YAML key space. Adding
	// fractional tiers requires updating this bucketing logic too — see
	// the package doc comment on types.go.
	bucket := int(math.Ceil(req.DesiredThroughputGBs))
	tier, ok := s.tiers[bucket]
	if !ok {
		return nil, &NoFeasibleSelectionError{
			Message: fmt.Sprintf(
				"no throughput tier delivers %.2f GB/s (largest catalogued is %d GB/s)",
				req.DesiredThroughputGBs, s.maxBucket,
			),
			Request: req,
		}
	}

	// Walk instances in ascending OCPU order; for each, walk VPUs in
	// ascending VPU number (vpu_<N>). Return the first
	// (instance, VPU) pair whose capacity floor fits the request. IOPS
	// is *not* used for filtering today — see the package-level
	// algorithm comment.
	for _, inst := range tier.instances {
		for _, v := range inst.vpus {
			if req.DesiredCapacityTB < v.cell.CapacityThroughputTB {
				continue
			}
			return &Decision{
				VMShape:       inst.shape,
				OCPUs:         inst.ocpus,
				MemoryGBs:     inst.memoryGBs,
				ThroughputGBs: bucket,
				VPUName:       v.name,
				VPU:           v.number,
				CapacityTB:    req.DesiredCapacityTB,
				IOPS:          v.cell.IOPS,
			}, nil
		}
	}

	return nil, &NoFeasibleSelectionError{
		Message: fmt.Sprintf(
			"no instance/VPU at throughput %d GB/s fits capacity=%.2f TB",
			bucket, req.DesiredCapacityTB,
		),
		Request: req,
	}
}

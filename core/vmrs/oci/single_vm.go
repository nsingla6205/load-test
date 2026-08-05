// SingleVMSelector implements the Selector interface for OCI.
//
// Algorithm
// ---------
//
//  1. Pick the throughput tier in O(1):
//
//         tier = config.Throughput[req.DesiredThroughputMBps]
//
//     Tier keys are the fixed set {800, 1600, 2400, 3200, 4000, 4800} MB/s and must
//     match the customer-visible headroom values exactly. Select rejects any input
//     that is not a positive integer multiple of 800; no silent rounding occurs.
//
//  2. Walk the tier's instances in ascending OCPU order (alphabetical on
//     shape name as a deterministic tiebreak when OCPUs are equal — only
//     matters once multiple shapes coexist at the same tier). For each
//     instance, walk its `vpu:` map in ascending VPU number (vpu_<N>) and
//     return the first cell whose `min_capacity_tib` is ≤ the
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
//     only. The chosen cell's max_iops is still echoed back in the
//     Decision so callers see the tier's ceiling. When IOPS-based
//     filtering is re-introduced, requests already populating
//     DesiredIOPS will start being honored without an API change.
//
//     Today every tier has exactly one instance (VM.DenseIO.E5.Flex), so
//     the instance loop is trivial; the structure is in place for future
//     tiers that qualify multiple shapes.
//
//     For the canonical example — req: 9.00 TiB, 4800 MB/s — bucket=4800,
//     instance=VM.DenseIO.E5.Flex (48 OCPUs), and:
//         vpu_30: floor 7.58 TiB  →  9.00 ≥ 7.58, MATCH → returned
//
//  3. If no (instance, VPU) pair fits → *NoFeasibleSelectionError.

package oci

import (
	"fmt"
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
	// tiers is the catalogue keyed by headroom MB/s (800, 1600, …, 4800).
	tiers map[int]preparedTier

	// maxBucket is the largest headroom key in the catalogue — used
	// only for diagnostic messages when a request exceeds the table.
	maxBucket int
}

// preparedTier is the parsed, sorted view of one throughput tier.
type preparedTier struct {
	// instances is sorted ascending: OCPUs, then shape name. Today there
	// is exactly one instance per tier, so the ordering only matters
	// once multiple shapes coexist at the same tier.
	instances []preparedInstance

	// throughputWithHeadroom is ThroughputTier.ThroughputWithHeadroom — OCI
	// max-egress catalogue value (e.g. 1000 when the tier key is 800).
	throughputWithHeadroom int64
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
		if tier.ThroughputWithHeadroom <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf("throughput %d: throughput_with_headroom must be > 0, got %d", bucket, tier.ThroughputWithHeadroom),
			}
		}

		tiers[bucket] = preparedTier{
			instances:              instances,
			throughputWithHeadroom: tier.ThroughputWithHeadroom,
		}
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
		if cell.MinCapacityTiB <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf(
					"throughput %d shape %q vpu %q: min_capacity_tib must be > 0",
					bucket, shape, vpuKey),
			}
		}
		if cell.MaxIOPS <= 0 {
			return nil, &InvalidConfigError{
				Message: fmt.Sprintf(
					"throughput %d shape %q vpu %q: max_iops must be > 0",
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
	if req.DesiredThroughputMBps <= 0 {
		return nil, &NoFeasibleSelectionError{
			Message: "DesiredThroughputMBps must be > 0",
			Request: req,
		}
	}
	// Customers may only request exact headroom tier values (800, 1600, …, 4800 MB/s).
	// Reject non-integer or non-multiple-of-step inputs so callers always land on a
	// well-defined tier rather than being silently rounded up.
	mbps := int(req.DesiredThroughputMBps)
	if float64(mbps) != req.DesiredThroughputMBps || mbps%headroomStepMBps != 0 {
		return nil, &NoFeasibleSelectionError{
			Message: fmt.Sprintf(
				"DesiredThroughputMBps must be a positive multiple of %d (got %.0f); valid values: 800, 1600, 2400, 3200, 4000, 4800",
				headroomStepMBps, req.DesiredThroughputMBps,
			),
			Request: req,
		}
	}
	if req.DesiredCapacityTiB <= 0 {
		return nil, &NoFeasibleSelectionError{
			Message: "DesiredCapacityTiB must be > 0",
			Request: req,
		}
	}
	// req.DesiredIOPS is intentionally not validated or used today; see
	// the package-level algorithm comment for the rationale and the
	// forward path.

	// Tier keys are headroom MB/s in 800 MB/s steps. The request is already
	// validated to be an exact multiple, so bucket == mbps.
	bucket := mbps
	tier, ok := s.tiers[bucket]
	if !ok {
		return nil, &NoFeasibleSelectionError{
			Message: fmt.Sprintf(
				"no throughput tier delivers %.0f MB/s (largest catalogued is %d MB/s)",
				req.DesiredThroughputMBps, s.maxBucket,
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
			if req.DesiredCapacityTiB < v.cell.MinCapacityTiB {
				continue
			}
			return &Decision{
				VMShape:                inst.shape,
				OCPUs:                  inst.ocpus,
				MemoryGBs:              inst.memoryGBs,
				Throughput:             bucket,
				VPUName:                v.name,
				VPU:                    v.number,
				CapacityTiB:            req.DesiredCapacityTiB,
				IOPS:                   v.cell.MaxIOPS,
				ThroughputWithHeadroom: tier.throughputWithHeadroom,
				DefaultIOPS:            v.cell.DefaultIOPS,
			}, nil
		}
	}

	return nil, &NoFeasibleSelectionError{
		Message: fmt.Sprintf(
			"no instance/VPU at throughput %d MB/s fits capacity=%.2f TiB",
			bucket, req.DesiredCapacityTiB,
		),
		Request: req,
	}
}

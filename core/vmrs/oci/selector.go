package oci

// Selector chooses a (flex tier, VPU band) pair that satisfies the
// customer-requested (capacity, throughput, optional IOPS).
//
// This is the OCI analogue of vmrs.DecisionMaker. The interface is kept
// minimal — a single Select call — because OCI sizing is fully expressible
// as: "given (capacity, throughput, iops?), give me (flex, VPU, achieved
// perf)".
type Selector interface {
	// Select returns the chosen Decision, or a *NoFeasibleSelectionError
	// when no qualified (flex, VPU) combination fits the request.
	Select(req CustomerRequest) (*Decision, error)
}

// NewSelector constructs the default Selector implementation. Today only
// one strategy exists — single-VM, ascending-VPU (see single_vm.go); the
// factory is kept so future strategies can be added without changing
// callers.
//
// Long-lived callers (services, daemons) should call NewSelector once at
// startup and reuse the returned Selector across many Select calls so the
// catalogue is preprocessed only once. One-shot callers (CLIs, tests) can
// use the package-level Decide for a single-call entry point.
func NewSelector(cfg *Config) (Selector, error) {
	if cfg == nil {
		return nil, &InvalidConfigError{Message: "config cannot be nil"}
	}
	return NewSingleVMSelector(cfg)
}

// Decide is a one-call entry point: it builds a transient selector from
// cfg, selects the (shape, VPU) that fits the request, and resolves IOPS
// into Decision.ResolvedIOPS so the caller receives a complete decision.
// IOPS resolution rules:
//   - explicit DesiredIOPS → validated within [DefaultIOPS, MaxIOPS]
//   - throughput downgrade (CurrentThroughputMBps > 0, target tier lower)
//     with no explicit IOPS → preserve CurrentIOPS (capped at new tier's MaxIOPS)
//   - otherwise → DefaultIOPS of the chosen tier
//
// Use this from CLIs, tests, or any caller that selects once and throws
// the result away. For services that select repeatedly, prefer
// NewSelector(cfg) + Selector.Select (note: Select does not resolve IOPS).
func Decide(cfg *Config, req CustomerRequest) (*Decision, error) {
	sel, err := NewSelector(cfg)
	if err != nil {
		return nil, err
	}
	decision, err := sel.Select(req)
	if err != nil {
		return nil, err
	}
	resolvedIOPS, err := ResolveIOPSFromDecision(decision, req.DesiredIOPS, req.CurrentThroughputMBps, req.CurrentIOPS)
	if err != nil {
		return nil, err
	}
	decision.ResolvedIOPS = resolvedIOPS
	return decision, nil
}

package common

import (
	"fmt"
	"strconv"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// BytesPerTiB is the number of bytes in a tebibyte, used to scale the per-TiB
// QoS multipliers by pool size.
const BytesPerTiB float64 = 1024 * 1024 * 1024 * 1024

// PO QoS policy names, one per tier so describe/delete stay consistent.
const (
	POExtremeQoSPolicyName = "po-extreme-qos"
	POPremiumQoSPolicyName = "po-premium-qos"
)

// Default per-TiB QoS multipliers, modeled on SDE's High/Medium performance
// modes (EXTREME→high, PREMIUM→medium). Overridable via env (see §6/§8).
const (
	defaultPOExtremeMaxMibpsPerTiB float64 = 128
	defaultPOExtremeMinIopsPerTiB  float64 = 0
	defaultPOPremiumMaxMibpsPerTiB float64 = 64
	defaultPOPremiumMinIopsPerTiB  float64 = 0
)

// POQoSResult is the computed QoS offering for a PO pool.
type POQoSResult struct {
	PolicyName    string
	MaxThroughput int64
	MaxIOPS       int64
}

// ComputePOQoS derives the QoS ceilings for a PO (HARDWARE) pool the way SDE
// does: a per-TiB multiplier selected by the offering (service level) times the
// pool size. EXTREME maps to the "high" multiplier, PREMIUM to "medium". The
// multipliers are env-configurable. This is a pure function of its inputs plus
// the env snapshot, so it is read once inside the QoS activity.
func ComputePOQoS(serviceLevel string, sizeInBytes uint64) (POQoSResult, error) {
	var policyName string
	var maxMibpsPerTiB, minIopsPerTiB float64

	switch serviceLevel {
	case ServiceLevelExtreme:
		policyName = POExtremeQoSPolicyName
		maxMibpsPerTiB = env.GetFloat64(env.EnvPOExtremeMaxMibpsPerTiB, defaultPOExtremeMaxMibpsPerTiB)
		minIopsPerTiB = env.GetFloat64(env.EnvPOExtremeMinIopsPerTiB, defaultPOExtremeMinIopsPerTiB)
	case ServiceLevelPremium:
		policyName = POPremiumQoSPolicyName
		maxMibpsPerTiB = env.GetFloat64(env.EnvPOPremiumMaxMibpsPerTiB, defaultPOPremiumMaxMibpsPerTiB)
		minIopsPerTiB = env.GetFloat64(env.EnvPOPremiumMinIopsPerTiB, defaultPOPremiumMinIopsPerTiB)
	default:
		return POQoSResult{}, fmt.Errorf("unsupported service level %q for HARDWARE QoS", serviceLevel)
	}

	sizeInTiB := float64(sizeInBytes) / BytesPerTiB
	maxThroughput := maxMibpsPerTiB * sizeInTiB
	minIops := minIopsPerTiB * sizeInTiB

	// Round throughput consistently with SDE (%.3f) before truncating to the
	// integer ceiling ONTAP expects.
	maxThroughputRounded, _ := strconv.ParseFloat(fmt.Sprintf("%.3f", maxThroughput), 64)

	return POQoSResult{
		PolicyName:    policyName,
		MaxThroughput: int64(maxThroughputRounded),
		MaxIOPS:       int64(minIops),
	}, nil
}

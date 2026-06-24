package activities

import (
	"context"
	"fmt"
	"math/rand"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// ociFaultDomains is the set of fault domains available in every OCI availability
// domain. OCI guarantees exactly these three in every AD.
var ociFaultDomains = []string{"FAULT-DOMAIN-1", "FAULT-DOMAIN-2", "FAULT-DOMAIN-3"}

// SelectFaultDomainsRequest carries the per-leg availability domains and any
// caller-supplied fault domain overrides for an OCI pool create.
type SelectFaultDomainsRequest struct {
	PrimaryZone   string
	SecondaryZone string
	MediatorZone  string

	// Optional caller overrides. When non-empty the value is used as-is and no
	// automatic selection is performed for that leg.
	PrimaryFaultDomain   string
	SecondaryFaultDomain string
	MediatorFaultDomain  string
}

// SelectFaultDomainsResult is the resolved fault domain per leg.
type SelectFaultDomainsResult struct {
	PrimaryFaultDomain   string
	SecondaryFaultDomain string
	MediatorFaultDomain  string
}

// SelectFaultDomains resolves the OCI fault domain for each VM of a pool.
func (j *PoolActivity) SelectFaultDomains(ctx context.Context, req SelectFaultDomainsRequest) (*SelectFaultDomainsResult, error) {
	activity.RecordHeartbeat(ctx, "Selecting OCI fault domains")
	logger := util.GetLogger(ctx)

	res := &SelectFaultDomainsResult{
		PrimaryFaultDomain:   req.PrimaryFaultDomain,
		SecondaryFaultDomain: req.SecondaryFaultDomain,
		MediatorFaultDomain:  req.MediatorFaultDomain,
	}

	// Validate any caller-supplied overrides against the known fault domain set.
	// A bad override is a user-input error and must not be silently accepted.
	overrides := []struct {
		leg         string
		faultDomain string
	}{
		{"primary", req.PrimaryFaultDomain},
		{"secondary", req.SecondaryFaultDomain},
		{"mediator", req.MediatorFaultDomain},
	}
	for _, override := range overrides {
		if override.faultDomain != "" && !isValidFaultDomain(override.faultDomain) {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				fmt.Errorf("invalid %s fault domain %q: must be one of %v", override.leg, override.faultDomain, ociFaultDomains))
		}
	}

	if len(ociFaultDomains) == 0 {
		logger.Info("No OCI fault domain candidates; using caller-provided fault domains only")
		return res, nil
	}

	counts, err := j.SE.GetFaultDomainCountsByZone(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Work on a mutable copy so picks within this pool spread across fault
	// domains even when two legs share an availability domain (shared HA).
	working := cloneFaultDomainCounts(counts)

	if res.PrimaryFaultDomain == "" {
		res.PrimaryFaultDomain = pickLeastLoadedFaultDomain(working, req.PrimaryZone, ociFaultDomains)
		incFaultDomainCount(working, req.PrimaryZone, res.PrimaryFaultDomain)
	}
	if res.SecondaryFaultDomain == "" {
		res.SecondaryFaultDomain = pickLeastLoadedFaultDomain(working, req.SecondaryZone, ociFaultDomains)
		incFaultDomainCount(working, req.SecondaryZone, res.SecondaryFaultDomain)
	}
	if res.MediatorFaultDomain == "" {
		res.MediatorFaultDomain = ociFaultDomains[rand.Intn(len(ociFaultDomains))]
	}

	logger.Info("Selected OCI fault domains",
		"primary_zone", req.PrimaryZone, "primary_fault_domain", res.PrimaryFaultDomain,
		"secondary_zone", req.SecondaryZone, "secondary_fault_domain", res.SecondaryFaultDomain,
		"mediator_zone", req.MediatorZone, "mediator_fault_domain", res.MediatorFaultDomain)
	return res, nil
}

// pickLeastLoadedFaultDomain returns the candidate fault domain with the fewest
// nodes in the given availability domain. Candidates not present in counts are
// treated as zero, so an empty fault domain is selectable. Ties are broken by
// the order of candidates, making the result deterministic for a fixed input.
func pickLeastLoadedFaultDomain(counts map[string]map[string]int64, zone string, candidates []string) string {
	zoneCounts := counts[zone]
	leastLoaded := candidates[0]
	leastCount := faultDomainCount(zoneCounts, leastLoaded)
	for _, candidate := range candidates[1:] {
		if count := faultDomainCount(zoneCounts, candidate); count < leastCount {
			leastLoaded, leastCount = candidate, count
		}
	}
	return leastLoaded
}

func isValidFaultDomain(faultDomain string) bool {
	for _, candidate := range ociFaultDomains {
		if candidate == faultDomain {
			return true
		}
	}
	return false
}

func faultDomainCount(zoneCounts map[string]int64, faultDomain string) int64 {
	if zoneCounts == nil {
		return 0
	}
	return zoneCounts[faultDomain]
}

func incFaultDomainCount(counts map[string]map[string]int64, zone, faultDomain string) {
	if counts[zone] == nil {
		counts[zone] = make(map[string]int64)
	}
	counts[zone][faultDomain]++
}

func cloneFaultDomainCounts(in map[string]map[string]int64) map[string]map[string]int64 {
	out := make(map[string]map[string]int64, len(in))
	for zone, zoneCounts := range in {
		clonedZoneCounts := make(map[string]int64, len(zoneCounts))
		for faultDomain, count := range zoneCounts {
			clonedZoneCounts[faultDomain] = count
		}
		out[zone] = clonedZoneCounts
	}
	return out
}

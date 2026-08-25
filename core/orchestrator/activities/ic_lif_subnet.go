package activities

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vmrs_decision "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vmrs/decision"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	lifTypeIntercluster = "interclusterLIF"
	// interclusterLIFIPsPerHAPair is the number of intercluster LIF IPs per HA pair (VM1 + VM2).
	interclusterLIFIPsPerHAPair = 2
	// dataSubnetCidrBlockEnv mirrors hyperscaler/google/networks.go DATA_SUBNET_CIDR_BLOCK.
	dataSubnetCidrBlockEnv  = "DATA_SUBNET_CIDR_BLOCK"
	defaultDataSubnetPrefix = 28
)

var (
	icLifAddressSpaceEnabled    = func() bool { return env.GetBool(env.EnvIcLifAddressSpaceEnabled, false) }
	resolveCloudServiceForICLif = hyperscaler2.ResolveCloudService
	icLifDataSubnetStillExists  = _icLifDataSubnetStillExists
)

// maxHAPairsOnSubnet returns how many HA pairs a primary data subnet of the given prefix can
// hold. Managing the intercluster LIFs out of a secondary range does not change this: the
// primary subnet budget stays TOTAL_IP_PER_HA_PAIR either way.
func maxHAPairsOnSubnet(primaryPrefix int) int {
	if primaryPrefix < 0 || primaryPrefix > 32 {
		return 0
	}
	if totalIPPerHAPair <= 0 {
		return 0
	}
	totalIPs := 1 << (32 - primaryPrefix)
	usable := totalIPs - 4
	if usable <= 0 {
		return 0
	}
	return usable / totalIPPerHAPair
}

func requiredSecondaryIPs(haPairs int) int {
	if haPairs <= 0 {
		return 0
	}
	return haPairs * interclusterLIFIPsPerHAPair
}

// resolveLargeCapacityHAPairs returns the HA pair count a large-capacity pool will be deployed
// with. It reads the same configuration the VMRS decision maker uses, so the intercluster carve
// covers exactly the cluster IdentifyVMs will later describe, without paying for a VMRS catalog
// load. VMRS never derives the HA pair count from the requested capacity, IOPS or throughput;
// those only select the VM type.
//
// params.AccountName must stay the value CreatePoolWorkflow puts in IdentifyVMsRequest.AccountID,
// which IdentifyVMs sets as Deployment.Labels["account_id"] for FindOptimalVMs. Passing anything
// else here (a numeric account id, a project id, an empty string) carves an allowlisted account's
// range for the default HA pair count while its cluster is deployed with the larger one.
func resolveLargeCapacityHAPairs(params commonparams.CreatePoolParams) int {
	return vmrs_decision.LargeVolumeHAPairs(params.AccountName)
}

// resolveHAPairsForSecondaryCarve returns the number of HA pairs the intercluster secondary range
// must cover. A large-capacity pool gets a dedicated data subnet, so the carve follows its
// configured HA pair count directly. Standard pools share a fixed-size subnet, so the carve covers
// as many HA pairs as that subnet can ever hold.
func standardDataSubnetPrefix() int {
	prefix := int(env.GetInt64(dataSubnetCidrBlockEnv, defaultDataSubnetPrefix))
	if prefix < 0 || prefix > 32 {
		return defaultDataSubnetPrefix
	}
	return prefix
}

func resolveHAPairsForSecondaryCarve(params commonparams.CreatePoolParams) int {
	if params.LargeCapacity {
		return resolveLargeCapacityHAPairs(params)
	}
	return maxHAPairsOnSubnet(standardDataSubnetPrefix())
}

type icLifCarvePlan struct {
	// secondaryCIDRs are the plain secondary range CIDRs to request. The
	// cloud-specific GCP SecondaryIpRangeSpec values are built by the caller in
	// pool_activities.go so this core file stays free of google.golang.org/api.
	secondaryCIDRs            []string
	carvedCIDR                string
	parentInterclusterRangeID int64
}

type interclusterParentRange struct {
	ID   int64
	CIDR string
}

func buildSecondaryIpRangeSpecsForICLif(ctx context.Context, se database.Storage, params commonparams.CreatePoolParams) (*icLifCarvePlan, error) {
	if len(params.SecondaryIpRange) == 0 {
		return nil, nil
	}
	baseRanges, err := parseSecondaryCIDRs(params.SecondaryIpRange)
	if err != nil {
		return nil, err
	}
	if !icLifAddressSpaceEnabled() {
		return &icLifCarvePlan{secondaryCIDRs: baseRanges}, nil
	}

	requiredHAPairs := resolveHAPairsForSecondaryCarve(params)
	requiredIPs := requiredSecondaryIPs(requiredHAPairs)
	if requiredIPs <= 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid required IC LIF IP count: %d", requiredIPs)))
	}

	parents, err := listActiveInterclusterParentRanges(ctx, se, params.VendorSubNetID)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("no active interclusterLIF address range found")))
	}
	candidateCIDRs := make([]string, 0, len(parents))
	for _, p := range parents {
		candidateCIDRs = append(candidateCIDRs, p.CIDR)
	}

	reservedCIDRs, err := listReservedInterclusterIPCIDRs(ctx, se, params.VendorSubNetID)
	if err != nil {
		return nil, err
	}
	carvedCIDR, parentCIDR, err := carveFreeCIDRBlock(candidateCIDRs, reservedCIDRs, requiredIPs)
	if err != nil {
		return nil, err
	}
	parentID := parentIDForCarvedCIDR(parents, parentCIDR, carvedCIDR)
	if parentID == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("carved IC LIF block %s could not be attributed to an active interclusterLIF address range", carvedCIDR)))
	}
	return &icLifCarvePlan{
		secondaryCIDRs:            []string{carvedCIDR},
		carvedCIDR:                carvedCIDR,
		parentInterclusterRangeID: parentID,
	}, nil
}

func listActiveInterclusterParentRanges(ctx context.Context, se database.Storage, vendorSubNetID string) ([]interclusterParentRange, error) {
	hostProjectNumber, vpcName, err := utils.ParseProjectId(vendorSubNetID)
	if err != nil || hostProjectNumber == "" || vpcName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid vendor subnet id %q", vendorSubNetID)))
	}
	lifType := database.AddressRangeLifTypeInterclusterLIF
	ranges, err := se.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	parents := make([]interclusterParentRange, 0, len(ranges))
	for _, ar := range ranges {
		if ar.AddressRangeState != database.AddressRangeStateCreated && ar.AddressRangeState != database.AddressRangeStateInUse {
			continue
		}
		if strings.TrimSpace(ar.AddressRangeCidr) == "" {
			continue
		}
		parents = append(parents, interclusterParentRange{ID: ar.ID, CIDR: ar.AddressRangeCidr})
	}
	return parents, nil
}

func parentIDForCarvedCIDR(parents []interclusterParentRange, parentCIDR, carvedCIDR string) int64 {
	if parentCIDR != "" {
		for _, p := range parents {
			if p.CIDR == parentCIDR {
				return p.ID
			}
		}
	}
	for _, p := range parents {
		if cidrContainsCIDR(p.CIDR, carvedCIDR) {
			return p.ID
		}
	}
	return 0
}

func cidrContainsCIDR(parentCIDR, childCIDR string) bool {
	return database.CidrContainsCIDR(parentCIDR, childCIDR)
}

func parseSecondaryCIDRs(cidrs []string) ([]string, error) {
	parsed := make([]string, 0, len(cidrs))
	for _, raw := range cidrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid secondary IP range CIDR %q: %w", cidr, err)))
		}
		parsed = append(parsed, cidr)
	}
	if len(parsed) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("secondary IP range list is empty after validation")))
	}
	return parsed, nil
}

// gcpSecondaryRangeNameDiscriminatorLen is the number of hex characters of the pool discriminator
// appended to a secondary range name. Eight is enough that two pools racing for the same block
// never collide, and short enough to stay well inside the 63-character GCP limit.
const gcpSecondaryRangeNameDiscriminatorLen = 8

const icLifSecondaryRangeNamePrefix = "ic-lif-"

var sameCIDRSecondaryRangeConflictRE = regexp.MustCompile(`\(([^)]+)\) conflicts with existing range [^(]+\(([^)]+)\)`)

// gcpSecondaryRangeName builds the GCP name for a pool's carved intercluster secondary range.
//
// The name carries a per-pool discriminator so a Temporal retry of the same pool rebuilds the
// same GCP range name (idempotent attach). Concurrent creates for the same VPC no longer race
// on first-fit: they claim distinct CIDRs under ClaimCarvedInterclusterIPRange before patch.
// Distinct names still matter for leftover GCP secondaries that VCP never persisted.
func gcpSecondaryRangeName(cidr, poolUUID string) string {
	const prefix = icLifSecondaryRangeNamePrefix
	name := sanitizeGCPResourceName(prefix+cidr, prefix)
	discriminator := poolNameDiscriminator(poolUUID)
	if discriminator == "" {
		return name
	}
	// Reserve room for the discriminator before truncating, so it is never the part cut off.
	maxBase := 63 - len(discriminator) - 1
	if len(name) > maxBase {
		name = strings.TrimRight(name[:maxBase], "-")
	}
	return name + "-" + discriminator
}

// poolNameDiscriminator returns a short, stable, DNS-safe fingerprint of a pool UUID, or "" when
// no UUID is available.
func poolNameDiscriminator(poolUUID string) string {
	trimmed := strings.TrimSpace(poolUUID)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])[:gcpSecondaryRangeNameDiscriminatorLen]
}

// sanitizeGCPResourceName lowercases value and replaces every character GCP does not accept in a
// resource name with a hyphen, keeping prefix intact and the result within 63 characters.
func sanitizeGCPResourceName(value, prefix string) string {
	suffix := strings.ToLower(strings.TrimPrefix(value, prefix))
	var b strings.Builder
	for _, r := range suffix {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	suffix = strings.Trim(b.String(), "-")
	if suffix == "" {
		suffix = "range"
	}
	name := prefix + suffix
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	for len(name) > len(prefix) && name[len(name)-1] == '-' {
		name = name[:len(name)-1]
	}
	return name
}

func listReservedInterclusterIPCIDRs(ctx context.Context, se database.Storage, vendorSubNetID string) ([]string, error) {
	if se == nil {
		return nil, nil
	}
	hostProjectNumber, vpcName, err := utils.ParseProjectId(vendorSubNetID)
	if err != nil || hostProjectNumber == "" || vpcName == "" {
		return nil, nil
	}
	ranges, err := se.ListInterclusterIPRangesForVPC(ctx, hostProjectNumber, vpcName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	reserved := make([]string, 0, len(ranges))
	for _, ir := range ranges {
		if ir.StartIP != "" && ir.EndIP != "" {
			cidr, cidrErr := cidrFromStartEnd(ir.StartIP, ir.EndIP)
			if cidrErr == nil {
				reserved = append(reserved, cidr)
			}
		}
	}
	return reserved, nil
}

func cidrFromStartEnd(startIP, endIP string) (string, error) {
	return database.CidrFromStartEnd(startIP, endIP)
}

func gcpSecondaryCarveBlockSize(requiredIPs int) (blockSize int, prefix int) {
	return database.GcpSecondaryCarveBlockSize(requiredIPs)
}

func carveFreeCIDRBlock(candidateCIDRs []string, reservedCIDRs []string, requiredIPs int) (carvedCIDR, parentCIDR string, err error) {
	return database.CarveFreeCIDRBlock(candidateCIDRs, reservedCIDRs, requiredIPs)
}

func cidrToStartEndMask(cidr string) (startIP, endIP, mask string, err error) {
	return database.CidrToStartEndMask(cidr)
}

func lookupInterclusterIPRangeID(ctx context.Context, se database.Storage, accountUUID, subnetName string) (int64, error) {
	if se == nil || subnetName == "" || accountUUID == "" {
		return 0, nil
	}
	network := subnetName
	ranges, err := se.GetIPRanges(ctx, &accountUUID, &network)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	for _, ir := range ranges {
		if ir.LifType == database.LifTypeInterclusterLIF {
			return ir.ID, nil
		}
	}
	return 0, nil
}

func findManagedICLifSecondary(secondaries []hyperscalermodels.SecondaryIpRange, parentCIDRs []string) (cidr, rangeName string, err error) {
	type match struct {
		cidr string
		name string
	}
	matches := make([]match, 0, 1)
	for _, sr := range secondaries {
		cidr := strings.TrimSpace(sr.IpCidrRange)
		name := strings.TrimSpace(sr.RangeName)
		if cidr == "" || !strings.HasPrefix(name, icLifSecondaryRangeNamePrefix) {
			continue
		}
		for _, parentCIDR := range parentCIDRs {
			if cidrContainsCIDR(parentCIDR, cidr) {
				matches = append(matches, match{cidr: cidr, name: name})
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", "", nil
	case 1:
		return matches[0].cidr, matches[0].name, nil
	default:
		return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("subnet has multiple managed interclusterLIF secondary ranges: cannot adopt")))
	}
}

func secondaryRangeCIDRPresent(secondaries []hyperscalermodels.SecondaryIpRange, carvedCIDR string) bool {
	carvedCIDR = strings.TrimSpace(carvedCIDR)
	if carvedCIDR == "" {
		return false
	}
	for _, sr := range secondaries {
		if strings.TrimSpace(sr.IpCidrRange) == carvedCIDR {
			return true
		}
	}
	return false
}

func isSameCIDRSecondaryRangeConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	var customErr *vsaerrors.CustomError
	if errors.As(err, &customErr) && customErr.OriginalErr != nil {
		msg = customErr.OriginalErr.Error()
	}
	m := sameCIDRSecondaryRangeConflictRE.FindStringSubmatch(msg)
	return len(m) == 3 && strings.TrimSpace(m[1]) == strings.TrimSpace(m[2])
}

func ensureResultFromAdopt(adopted *database.InterclusterCarveResult) *commonparams.EnsureICSecondaryRangeResult {
	if adopted == nil || adopted.IPRange == nil {
		return &commonparams.EnsureICSecondaryRangeResult{}
	}
	return &commonparams.EnsureICSecondaryRangeResult{
		CarvedSecondaryCIDR:       adopted.CarvedCIDR,
		ParentInterclusterRangeID: adopted.ParentID,
		IPRangeID:                 adopted.IPRange.ID,
	}
}

func (j *PoolActivity) adoptInterclusterRangeFromGCP(ctx context.Context, params commonparams.CreatePoolParams, subnetName, accountUUID, carvedCIDR string) (*database.InterclusterCarveResult, error) {
	hostProjectNumber, vpcName, err := utils.ParseProjectId(params.VendorSubNetID)
	if err != nil || hostProjectNumber == "" || vpcName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid vendor subnet id %q", params.VendorSubNetID)))
	}
	parents, err := listActiveInterclusterParentRanges(ctx, j.SE, params.VendorSubNetID)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("no active interclusterLIF address range found")))
	}
	region, err := j.SE.GetRegionByName(ctx, params.Region)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	specs := make([]database.InterclusterParentSpec, 0, len(parents))
	for _, p := range parents {
		specs = append(specs, database.InterclusterParentSpec{ID: p.ID, CIDR: p.CIDR})
	}
	adopted, err := j.SE.AdoptInterclusterIPRange(ctx, database.InterclusterAdoptClaim{
		HostProjectNumber: hostProjectNumber,
		VPCName:           vpcName,
		CarvedCIDR:        carvedCIDR,
		Parents:           specs,
		Network:           subnetName,
		AccountUUID:       accountUUID,
		RegionUUID:        region.UUID,
	})
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return adopted, nil
}

func (j *PoolActivity) tryAdoptGCPInterclusterSecondary(ctx context.Context, service hyperscaler2.Services, params commonparams.CreatePoolParams, subnetName, accountUUID, snHostProject string) (*commonparams.EnsureICSecondaryRangeResult, error) {
	subnet, err := service.GetSubnetwork(snHostProject, params.Region, subnetName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if subnet == nil {
		return nil, nil
	}
	parents, err := listActiveInterclusterParentRanges(ctx, j.SE, params.VendorSubNetID)
	if err != nil {
		return nil, err
	}
	parentCIDRs := make([]string, 0, len(parents))
	for _, p := range parents {
		parentCIDRs = append(parentCIDRs, p.CIDR)
	}
	carvedCIDR, rangeName, findErr := findManagedICLifSecondary(subnet.SecondaryIpRanges, parentCIDRs)
	if findErr != nil {
		return nil, findErr
	}
	if carvedCIDR == "" {
		return nil, nil
	}
	adopted, err := j.adoptInterclusterRangeFromGCP(ctx, params, subnetName, accountUUID, carvedCIDR)
	if err != nil {
		return nil, err
	}
	util.GetLogger(ctx).Info("Adopted intercluster secondary range already attached on GCP",
		"subnet", subnetName, "cidr", carvedCIDR, "gcpRangeName", rangeName, "ipRangeID", adopted.IPRange.ID)
	return ensureResultFromAdopt(adopted), nil
}

// CheckInterclusterCarveAvailability reports whether a free intercluster block can still be carved
// for this pool. It runs the same computation as the carve but persists nothing and touches no
// cloud resource, so the caller can fail closed before a data subnet is created. The carve itself
// only runs after the subnet exists, so without this check an exhausted address range is discovered
// too late and the subnet is left behind when the create unwinds.
//
// It is a no-op when IC LIF address-space management is off or the pool has no parent
// interclusterLIF range.
func (j *PoolActivity) CheckInterclusterCarveAvailability(ctx context.Context, params commonparams.CreatePoolParams) error {
	if !icLifAddressSpaceEnabled() || len(params.SecondaryIpRange) == 0 {
		return nil
	}
	recordHeartbeat(ctx, "Checking intercluster LIF address space availability before creating the data subnet")
	if _, err := buildSecondaryIpRangeSpecsForICLif(ctx, j.SE, params); err != nil {
		return err
	}
	return nil
}

// EnsureInterclusterSecondaryRangeOp makes sure the data subnet carries a carved intercluster
// secondary range. It is a no-op when IC LIF address-space management is off or the pool has no
// parent interclusterLIF range.
//
// Concurrent creates are serialized by ClaimCarvedInterclusterIPRange (advisory lock, then
// first-fit carve, then INSERT). When GCP already carries a managed ic-lif secondary on a reused
// subnet but ip_ranges is empty, the range is adopted before carve. A leftover GCP overlap
// releases the claim and recarves once unless the conflict is the same CIDR already on the subnet,
// in which case the GCP range is adopted instead of carving forward.
func (j *PoolActivity) EnsureInterclusterSecondaryRangeOp(ctx context.Context, params commonparams.CreatePoolParams, poolUUID, snHostProject, subnetName string) (*commonparams.EnsureICSecondaryRangeResult, error) {
	if !icLifAddressSpaceEnabled() || len(params.SecondaryIpRange) == 0 || subnetName == "" {
		return &commonparams.EnsureICSecondaryRangeResult{}, nil
	}
	recordHeartbeat(ctx, fmt.Sprintf("Ensuring intercluster secondary range on subnet %s in project %s", subnetName, snHostProject))

	account, err := j.SE.GetAccount(ctx, params.AccountName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	existing, err := lookupInterclusterIPRange(ctx, j.SE, account.UUID, subnetName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return j.patchExistingInterclusterClaim(ctx, params, poolUUID, snHostProject, subnetName, existing)
	}

	if snHostProject == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("cannot attach intercluster secondary range to subnet %s: SN host project is empty", subnetName)))
	}

	service, err := resolveCloudServiceForICLif(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if adopted, adoptErr := j.tryAdoptGCPInterclusterSecondary(ctx, service, params, subnetName, account.UUID, snHostProject); adoptErr != nil {
		return nil, adoptErr
	} else if adopted != nil {
		return adopted, nil
	}

	claim, err := j.claimInterclusterCarve(ctx, params, subnetName, account.UUID, nil)
	if err != nil {
		return nil, err
	}
	if claim == nil || claim.CarvedCIDR == "" {
		return &commonparams.EnsureICSecondaryRangeResult{}, nil
	}

	result, patchErr := j.patchClaimedInterclusterRange(ctx, service, params, poolUUID, snHostProject, subnetName, claim)
	if patchErr == nil {
		return result, nil
	}
	if !claim.Inserted {
		return nil, patchErr
	}
	if !isSecondaryRangePatchConflict(patchErr) {
		j.releaseInsertedInterclusterCarveClaim(ctx, claim)
		return nil, patchErr
	}
	if isSameCIDRSecondaryRangeConflict(patchErr) {
		util.GetLogger(ctx).Info("Intercluster secondary range already on subnet with different GCP name, adopting",
			"subnet", subnetName, "cidr", claim.CarvedCIDR, "error", patchErr)
		j.releaseInsertedInterclusterCarveClaim(ctx, claim)
		adopted, adoptErr := j.adoptInterclusterRangeFromGCP(ctx, params, subnetName, account.UUID, claim.CarvedCIDR)
		if adoptErr != nil {
			return nil, adoptErr
		}
		return ensureResultFromAdopt(adopted), nil
	}

	util.GetLogger(ctx).Info("GCP rejected claimed intercluster secondary range, releasing claim and recarving once",
		"subnet", subnetName, "cidr", claim.CarvedCIDR, "error", patchErr)
	j.releaseInsertedInterclusterCarveClaim(ctx, claim)

	retryClaim, retryErr := j.claimInterclusterCarve(ctx, params, subnetName, account.UUID, []string{claim.CarvedCIDR})
	if retryErr != nil {
		return nil, retryErr
	}
	if retryClaim == nil || retryClaim.CarvedCIDR == "" {
		return nil, patchErr
	}
	result, patchErr = j.patchClaimedInterclusterRange(ctx, service, params, poolUUID, snHostProject, subnetName, retryClaim)
	if patchErr != nil {
		if retryClaim.Inserted && isSameCIDRSecondaryRangeConflict(patchErr) {
			j.releaseInsertedInterclusterCarveClaim(ctx, retryClaim)
			adopted, adoptErr := j.adoptInterclusterRangeFromGCP(ctx, params, subnetName, account.UUID, retryClaim.CarvedCIDR)
			if adoptErr != nil {
				return nil, adoptErr
			}
			return ensureResultFromAdopt(adopted), nil
		}
		j.releaseInsertedInterclusterCarveClaim(ctx, retryClaim)
		return nil, patchErr
	}
	return result, nil
}

func (j *PoolActivity) claimInterclusterCarve(ctx context.Context, params commonparams.CreatePoolParams, subnetName, accountUUID string, extraReservedCIDRs []string) (*database.InterclusterCarveResult, error) {
	requiredIPs := requiredSecondaryIPs(resolveHAPairsForSecondaryCarve(params))
	if requiredIPs <= 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid required IC LIF IP count: %d", requiredIPs)))
	}
	parents, err := listActiveInterclusterParentRanges(ctx, j.SE, params.VendorSubNetID)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("no active interclusterLIF address range found")))
	}
	hostProjectNumber, vpcName, err := utils.ParseProjectId(params.VendorSubNetID)
	if err != nil || hostProjectNumber == "" || vpcName == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("invalid vendor subnet id %q", params.VendorSubNetID)))
	}
	region, err := j.SE.GetRegionByName(ctx, params.Region)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	specs := make([]database.InterclusterParentSpec, 0, len(parents))
	for _, p := range parents {
		specs = append(specs, database.InterclusterParentSpec{ID: p.ID, CIDR: p.CIDR})
	}
	claimed, err := j.SE.ClaimCarvedInterclusterIPRange(ctx, database.InterclusterCarveClaim{
		HostProjectNumber:  hostProjectNumber,
		VPCName:            vpcName,
		RequiredIPs:        requiredIPs,
		Parents:            specs,
		Network:            subnetName,
		AccountUUID:        accountUUID,
		RegionUUID:         region.UUID,
		ExtraReservedCIDRs: extraReservedCIDRs,
	})
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if claimed == nil || claimed.IPRange == nil || claimed.CarvedCIDR == "" {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, fmt.Errorf("intercluster carve claim returned an empty result")))
	}
	return claimed, nil
}

func (j *PoolActivity) patchClaimedInterclusterRange(ctx context.Context, service hyperscaler2.Services, params commonparams.CreatePoolParams, poolUUID, snHostProject, subnetName string, claim *database.InterclusterCarveResult) (*commonparams.EnsureICSecondaryRangeResult, error) {
	rangeName := gcpSecondaryRangeName(claim.CarvedCIDR, poolUUID)
	operationName, err := service.AddSubnetSecondaryRangeOp(snHostProject, params.Region, subnetName, rangeName, claim.CarvedCIDR)
	if err != nil {
		return nil, err
	}
	util.GetLogger(ctx).Info("Started intercluster secondary range patch",
		"subnet", subnetName, "project", snHostProject, "region", params.Region,
		"rangeName", rangeName, "cidr", claim.CarvedCIDR, "operation", operationName, "ipRangeID", claim.IPRange.ID)
	return &commonparams.EnsureICSecondaryRangeResult{
		OperationName:             operationName,
		Project:                   snHostProject,
		CarvedSecondaryCIDR:       claim.CarvedCIDR,
		ParentInterclusterRangeID: claim.ParentID,
		IPRangeID:                  claim.IPRange.ID,
		ReleaseClaimOnPatchFailure: claim.Inserted,
	}, nil
}

func (j *PoolActivity) patchExistingInterclusterClaim(ctx context.Context, params commonparams.CreatePoolParams, poolUUID, snHostProject, subnetName string, existing *datamodel.IPRange) (*commonparams.EnsureICSecondaryRangeResult, error) {
	carvedCIDR, err := cidrFromStartEnd(existing.StartIP, existing.EndIP)
	if err != nil || carvedCIDR == "" {
		util.GetLogger(ctx).Info("Subnet already has a carved intercluster range, skipping patch",
			"subnet", subnetName, "ipRangeID", existing.ID)
		return &commonparams.EnsureICSecondaryRangeResult{IPRangeID: existing.ID}, nil
	}
	if snHostProject == "" {
		return &commonparams.EnsureICSecondaryRangeResult{IPRangeID: existing.ID}, nil
	}
	service, err := resolveCloudServiceForICLif(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	subnet, err := service.GetSubnetwork(snHostProject, params.Region, subnetName)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if subnet != nil && secondaryRangeCIDRPresent(subnet.SecondaryIpRanges, carvedCIDR) {
		util.GetLogger(ctx).Info("Intercluster secondary range already attached on GCP, skipping patch",
			"subnet", subnetName, "ipRangeID", existing.ID, "cidr", carvedCIDR)
		return &commonparams.EnsureICSecondaryRangeResult{
			IPRangeID:           existing.ID,
			CarvedSecondaryCIDR: carvedCIDR,
		}, nil
	}
	parentID := int64(0)
	if existing.AddressRangeID.Valid {
		parentID = existing.AddressRangeID.Int64
	}
	claim := &database.InterclusterCarveResult{
		IPRange:    existing,
		CarvedCIDR: carvedCIDR,
		ParentID:   parentID,
	}
	result, patchErr := j.patchClaimedInterclusterRange(ctx, service, params, poolUUID, snHostProject, subnetName, claim)
	if patchErr != nil {
		return nil, patchErr
	}
	return result, nil
}

func (j *PoolActivity) releaseInsertedInterclusterCarveClaim(ctx context.Context, claim *database.InterclusterCarveResult) {
	if claim == nil || !claim.Inserted || claim.IPRange == nil {
		return
	}
	j.releaseInterclusterCarveClaim(ctx, claim.IPRange.ID)
}

func (j *PoolActivity) releaseInterclusterCarveClaim(ctx context.Context, ipRangeID int64) {
	if ipRangeID == 0 {
		return
	}
	if _, err := j.SE.DeleteIPRangeByID(ctx, ipRangeID); err != nil {
		util.GetLogger(ctx).Warn("Failed to release intercluster IP range claim after patch failure",
			"ipRangeID", ipRangeID, "error", err)
	}
}

func lookupInterclusterIPRange(ctx context.Context, se database.Storage, accountUUID, subnetName string) (*datamodel.IPRange, error) {
	if se == nil || subnetName == "" || accountUUID == "" {
		return nil, nil
	}
	network := subnetName
	ranges, err := se.GetIPRanges(ctx, &accountUUID, &network)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	for _, ir := range ranges {
		if ir.LifType == database.LifTypeInterclusterLIF {
			return ir, nil
		}
	}
	return nil, nil
}

func isSecondaryRangePatchConflict(err error) bool {
	if err == nil {
		return false
	}
	customErr := vsaerrors.ExtractCustomError(err)
	return customErr.IsError(vsaerrors.ErrResourceStateConflictError)
}

func (j *PoolActivity) PersistInterclusterIPRange(ctx context.Context, params commonparams.CreatePoolParams, poolUUID, subnetName, carvedCIDR string, parentAddressRangeID int64) (int64, error) {
	if !icLifAddressSpaceEnabled() {
		return 0, nil
	}
	account, err := j.SE.GetAccount(ctx, params.AccountName)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	existing, err := lookupInterclusterIPRange(ctx, j.SE, account.UUID, subnetName)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		parentID := parentAddressRangeID
		if parentID == 0 && existing.AddressRangeID.Valid {
			parentID = existing.AddressRangeID.Int64
		}
		j.markInterclusterParentInUse(ctx, poolUUID, carvedCIDR, parentID)
		return existing.ID, nil
	}
	if carvedCIDR == "" {
		return 0, nil
	}
	startIP, endIP, mask, err := cidrToStartEndMask(carvedCIDR)
	if err != nil {
		return 0, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err))
	}
	region, err := j.SE.GetRegionByName(ctx, params.Region)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	network := subnetName
	ir := &datamodel.IPRange{
		Network:     &network,
		StartIP:     startIP,
		EndIP:       endIP,
		Netmask:     mask,
		LifType:     database.LifTypeInterclusterLIF,
		AccountUUID: account.UUID,
		RegionUUID:  region.UUID,
	}
	if parentAddressRangeID > 0 {
		ir.AddressRangeID = sql.NullInt64{Int64: parentAddressRangeID, Valid: true}
	}
	created, err := j.SE.CreateIPRange(ctx, ir)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	util.GetLogger(ctx).Info("Persisted intercluster IP range", "poolUUID", poolUUID, "ipRangeID", created.ID, "subnet", subnetName, "cidr", carvedCIDR)
	j.markInterclusterParentInUse(ctx, poolUUID, carvedCIDR, parentAddressRangeID)
	return created.ID, nil
}

func (j *PoolActivity) markInterclusterParentInUse(ctx context.Context, poolUUID, carvedCIDR string, parentAddressRangeID int64) {
	if parentAddressRangeID <= 0 {
		return
	}
	updated, markErr := j.SE.MarkAddressRangeInUseByID(ctx, parentAddressRangeID)
	switch {
	case markErr != nil:
		util.GetLogger(ctx).Warn("Failed to mark intercluster address range IN_USE",
			"poolUUID", poolUUID, "addressRangeID", parentAddressRangeID, "error", markErr)
	case updated:
		util.GetLogger(ctx).Info("Marked intercluster address range IN_USE",
			"poolUUID", poolUUID, "addressRangeID", parentAddressRangeID, "cidr", carvedCIDR)
	default:
		util.GetLogger(ctx).Info("Intercluster address range already IN_USE",
			"poolUUID", poolUUID, "addressRangeID", parentAddressRangeID)
	}
}

// ReleaseInterclusterIPRangeClaim soft-deletes a persist-first carve that GCP never accepted.
func (j *PoolActivity) ReleaseInterclusterIPRangeClaim(ctx context.Context, ipRangeID int64) error {
	if ipRangeID == 0 {
		return nil
	}
	_, err := j.SE.DeleteIPRangeByID(ctx, ipRangeID)
	if err != nil {
		var customErr *vsaerrors.CustomError
		if errors.As(err, &customErr) && customErr.TrackingID == vsaerrors.ErrDatabaseDataNotFoundError {
			return nil
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *PoolActivity) DeleteInterclusterLifsForPool(ctx context.Context, poolID int64) error {
	if poolID == 0 {
		return nil
	}
	return j.SE.DeleteInterclusterLifsByPoolID(ctx, poolID)
}

// DeleteInterclusterIPRangeForSubnet soft-deletes the carved interclusterLIF ip_ranges row for a subnet
// when the subnet is being released (last pool on subnet, or all co-tenant pools are deleting).
func (j *PoolActivity) DeleteInterclusterIPRangeForSubnet(ctx context.Context, pool *datamodel.Pool) error {
	if pool == nil || !icLifAddressSpaceEnabled() || len(pool.ClusterDetails.SubnetNames) == 0 {
		return nil
	}
	subnetName := pool.ClusterDetails.SubnetNames[len(pool.ClusterDetails.SubnetNames)-1]
	accountName := ""
	if pool.Account != nil {
		accountName = pool.Account.Name
	}
	if strings.TrimSpace(subnetName) == "" || strings.TrimSpace(accountName) == "" || pool.AccountID == 0 {
		return nil
	}
	poolsUsingSubnet, err := getPoolsBySubnetwork(ctx, j.SE, strconv.FormatInt(pool.AccountID, 10), subnetName, pool.Network)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !shouldReleaseSharedDataSubnet(poolsUsingSubnet) {
		util.GetLogger(ctx).Info("Skipping intercluster IP range delete; other pools still use subnet",
			"subnet", subnetName, "pool", pool.Name, "poolsOnSubnet", len(poolsUsingSubnet))
		return nil
	}
	snHostProject := pool.ClusterDetails.SnHostProject
	if snHostProject == "" && len(poolsUsingSubnet) > 0 {
		snHostProject = poolsUsingSubnet[0].ClusterDetails.SnHostProject
	}
	stillExists, existsErr := icLifDataSubnetStillExists(ctx, snHostProject, subnetName)
	if existsErr != nil {
		return vsaerrors.WrapAsTemporalApplicationError(existsErr)
	}
	if stillExists {
		util.GetLogger(ctx).Info("Skipping intercluster IP range delete; subnet still exists in GCP",
			"subnet", subnetName, "pool", pool.Name, "snHostProject", snHostProject)
		return nil
	}
	ipRangeID, err := j.LookupInterclusterIPRangeForSubnet(ctx, accountName, subnetName)
	if err != nil {
		return err
	}
	if ipRangeID == 0 {
		return nil
	}
	deleted, err := j.SE.DeleteIPRangeByID(ctx, ipRangeID)
	if err != nil {
		var customErr *vsaerrors.CustomError
		if errors.As(err, &customErr) && customErr.TrackingID == vsaerrors.ErrDatabaseDataNotFoundError {
			return nil
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	util.GetLogger(ctx).Info("Deleted intercluster IP range for subnet", "subnet", subnetName, "ipRangeID", ipRangeID)

	// Hand the parent range back to CREATED once its last carved block is gone, so the
	// operator can delete or re-register it. The release is a no-op while other pools
	// still hold carved blocks from the same parent.
	if deleted != nil && deleted.AddressRangeID.Valid && deleted.AddressRangeID.Int64 > 0 {
		parentID := deleted.AddressRangeID.Int64
		released, relErr := j.SE.ReleaseInterclusterAddressRangeIfUnused(ctx, parentID)
		switch {
		case relErr != nil:
			util.GetLogger(ctx).Warn("Failed to release intercluster address range",
				"subnet", subnetName, "addressRangeID", parentID, "error", relErr)
		case released:
			util.GetLogger(ctx).Info("Released intercluster address range back to CREATED",
				"subnet", subnetName, "addressRangeID", parentID)
		default:
			util.GetLogger(ctx).Info("Intercluster address range still in use by other pools, not released",
				"subnet", subnetName, "addressRangeID", parentID)
		}
	}
	return nil
}

func _icLifDataSubnetStillExists(ctx context.Context, snHostProject, subnetName string) (bool, error) {
	snHostProject = strings.TrimSpace(snHostProject)
	subnetName = strings.TrimSpace(subnetName)
	if snHostProject == "" || subnetName == "" || Region == "" {
		// Cannot verify GCP state; keep the ip_ranges row so a later carve does not reuse a
		// CIDR that is still attached as a secondary range on a live subnet.
		return true, nil
	}
	service, err := resolveCloudServiceForICLif(ctx)
	if err != nil {
		return false, err
	}
	_, err = service.GetSubnetwork(snHostProject, Region, subnetName)
	if err == nil {
		return true, nil
	}
	errMsg := strings.ToLower(err.Error())
	if strings.Contains(errMsg, "notfound") || strings.Contains(errMsg, "not found") {
		return false, nil
	}
	return false, err
}

func (j *PoolActivity) LookupInterclusterIPRangeForSubnet(ctx context.Context, accountName, subnetName string) (int64, error) {
	account, err := j.SE.GetAccount(ctx, accountName)
	if err != nil {
		return 0, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return lookupInterclusterIPRangeID(ctx, j.SE, account.UUID, subnetName)
}

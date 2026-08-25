package database

import (
	"fmt"
	"math/bits"
	"net"
	"strconv"
	"strings"

	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

const gcpSecondaryIpRangeMinPrefix = 29

// InterclusterParentSpec is a parent address_ranges row used for IC LIF carving.
type InterclusterParentSpec struct {
	ID   int64
	CIDR string
}

// InterclusterCarveClaim is the input to ClaimCarvedInterclusterIPRange.
type InterclusterCarveClaim struct {
	HostProjectNumber string
	VPCName           string
	RequiredIPs       int
	Parents           []InterclusterParentSpec
	Network           string
	AccountUUID       string
	RegionUUID        string
	// ExtraReservedCIDRs are CIDRs to skip even if they are not in ip_ranges (leftover GCP
	// secondaries from a stranded subnet or a manual range).
	ExtraReservedCIDRs []string
}

// InterclusterAdoptClaim persists a GCP-attached IC LIF block into ip_ranges under the carve lock.
type InterclusterAdoptClaim struct {
	HostProjectNumber string
	VPCName           string
	CarvedCIDR        string
	Parents           []InterclusterParentSpec
	Network           string
	AccountUUID       string
	RegionUUID        string
}

// CarveFreeCIDRBlock returns the first free aligned CIDR that can hold requiredIPs.
func CarveFreeCIDRBlock(candidateCIDRs []string, reservedCIDRs []string, requiredIPs int) (carvedCIDR, parentCIDR string, err error) {
	if requiredIPs <= 0 {
		return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("required IP count must be positive: %d", requiredIPs)))
	}
	blockSize, prefix := GcpSecondaryCarveBlockSize(requiredIPs)
	for _, cidr := range candidateCIDRs {
		_, network, parseErr := net.ParseCIDR(cidr)
		if parseErr != nil {
			continue
		}
		start, end, ok := cidrBounds(network)
		if !ok {
			continue
		}
		for blockStart := alignUp(start, uint32(blockSize)); blockStart <= end; blockStart += uint32(blockSize) {
			blockEnd := blockStart + uint32(blockSize) - 1
			if blockEnd > end {
				break
			}
			blockCIDR := fmt.Sprintf("%s/%d", uint32ToIPv4(blockStart).String(), prefix)
			if blockOverlapsAnyReserved(blockCIDR, reservedCIDRs) {
				continue
			}
			return blockCIDR, cidr, nil
		}
	}
	return "", "", vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("intercluster LIF address space exhausted: no free block for %d IPs in ranges %v",
				requiredIPs, candidateCIDRs)))
}

// GcpSecondaryCarveBlockSize returns the aligned block size and prefix for requiredIPs.
func GcpSecondaryCarveBlockSize(requiredIPs int) (blockSize int, prefix int) {
	blockSize = max(nextPowerOfTwo(requiredIPs), 1<<(32-gcpSecondaryIpRangeMinPrefix))
	prefix = 32 - bits.TrailingZeros32(uint32(blockSize))
	return blockSize, prefix
}

func blockOverlapsAnyReserved(blockCIDR string, reservedCIDRs []string) bool {
	for _, reserved := range reservedCIDRs {
		if cidrRangesOverlap(blockCIDR, reserved) {
			return true
		}
	}
	return false
}

func cidrRangesOverlap(a, b string) bool {
	_, netA, err := net.ParseCIDR(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	_, netB, err := net.ParseCIDR(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	startA, endA, okA := cidrBounds(netA)
	startB, endB, okB := cidrBounds(netB)
	if !okA || !okB {
		return false
	}
	return startA <= endB && startB <= endA
}

func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func cidrBounds(network *net.IPNet) (uint32, uint32, bool) {
	ip := network.IP.To4()
	if ip == nil {
		return 0, 0, false
	}
	start := ipv4ToUint32(ip)
	maskSize, bitsTotal := network.Mask.Size()
	if bitsTotal != 32 || maskSize < 0 {
		return 0, 0, false
	}
	size := uint32(1 << (32 - maskSize))
	return start, start + size - 1, true
}

func alignUp(value uint32, alignment uint32) uint32 {
	if alignment == 0 {
		return value
	}
	if value%alignment == 0 {
		return value
	}
	return value + alignment - (value % alignment)
}

func ipv4ToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
}

func uint32ToIPv4(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// CidrFromStartEnd reconstructs a CIDR from inclusive IPv4 bounds.
func CidrFromStartEnd(startIP, endIP string) (string, error) {
	start := net.ParseIP(strings.TrimSpace(startIP)).To4()
	end := net.ParseIP(strings.TrimSpace(endIP)).To4()
	if start == nil || end == nil {
		return "", fmt.Errorf("invalid start/end IP")
	}
	startN := ipv4ToUint32(start)
	endN := ipv4ToUint32(end)
	if endN < startN {
		return "", fmt.Errorf("end before start")
	}
	size := endN - startN + 1
	if size == 0 || (size&(size-1)) != 0 {
		return "", fmt.Errorf("range size %d is not a power of two", size)
	}
	prefix := 32 - bits.TrailingZeros32(size)
	return fmt.Sprintf("%s/%d", start.String(), prefix), nil
}

// CidrToStartEndMask returns inclusive start/end IPs and the prefix length as a string.
func CidrToStartEndMask(cidr string) (startIP, endIP, mask string, err error) {
	_, ipNet, parseErr := net.ParseCIDR(strings.TrimSpace(cidr))
	if parseErr != nil {
		return "", "", "", parseErr
	}
	start, end, ok := cidrBounds(ipNet)
	if !ok {
		return "", "", "", fmt.Errorf("invalid CIDR bounds for %s", cidr)
	}
	ones, _ := ipNet.Mask.Size()
	return uint32ToIPv4(start).String(), uint32ToIPv4(end).String(), strconv.Itoa(ones), nil
}

func parentIDForCarvedCIDR(parents []InterclusterParentSpec, parentCIDR, carvedCIDR string) int64 {
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

// CidrContainsCIDR reports whether childCIDR lies entirely within parentCIDR.
func CidrContainsCIDR(parentCIDR, childCIDR string) bool {
	return cidrContainsCIDR(parentCIDR, childCIDR)
}

func cidrContainsCIDR(parentCIDR, childCIDR string) bool {
	_, parentNet, err := net.ParseCIDR(strings.TrimSpace(parentCIDR))
	if err != nil {
		return false
	}
	_, childNet, err := net.ParseCIDR(strings.TrimSpace(childCIDR))
	if err != nil {
		return false
	}
	start, end, ok := cidrBounds(childNet)
	if !ok {
		return false
	}
	return parentNet.Contains(uint32ToIPv4(start)) && parentNet.Contains(uint32ToIPv4(end))
}

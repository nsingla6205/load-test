package common

import (
	"fmt"
	"net"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// POIPSpaceName returns the ONTAP ipspace name for a PO pool datapath:
// ipspace_<accountUUID>_<vlanID> (account UUID with dashes removed).
func POIPSpaceName(accountUUID, vlanID string) string {
	return fmt.Sprintf("ipspace_%s_%s", NormalizePOAccountUUID(accountUUID), vlanID)
}

// POBroadcastDomainName returns the ONTAP broadcast-domain name for a PO pool
// datapath: bdomain_<accountUUID>_<vlanID> (account UUID with dashes removed).
func POBroadcastDomainName(accountUUID, vlanID string) string {
	return fmt.Sprintf("bdomain_%s_%s", NormalizePOAccountUUID(accountUUID), vlanID)
}

const poIPSpaceNamePrefix = "ipspace_"

// POBroadcastDomainFromIPSpace derives the PO broadcast-domain name from a
// persisted ONTAP ipspace name (ipspace_<account>_<vlan> -> bdomain_<account>_<vlan>).
func POBroadcastDomainFromIPSpace(ipSpace string) (string, error) {
	ipSpace = strings.TrimSpace(ipSpace)
	if ipSpace == "" {
		return "", fmt.Errorf("ipspace must not be empty")
	}
	if !strings.HasPrefix(ipSpace, poIPSpaceNamePrefix) {
		return "", fmt.Errorf("ipspace %q must start with %q", ipSpace, poIPSpaceNamePrefix)
	}
	return "bdomain_" + strings.TrimPrefix(ipSpace, poIPSpaceNamePrefix), nil
}

// ValidatePOIPSpaceForAccount checks that ipSpace belongs to the given account
// using the PO naming convention ipspace_<accountUUIDNoDashes>_...
func ValidatePOIPSpaceForAccount(accountUUID, ipSpace string) error {
	ipSpace = strings.TrimSpace(ipSpace)
	if ipSpace == "" {
		return fmt.Errorf("ipspace must not be empty")
	}
	expectedPrefix := poIPSpaceNamePrefix + NormalizePOAccountUUID(accountUUID) + "_"
	if !strings.HasPrefix(ipSpace, expectedPrefix) {
		return fmt.Errorf("ipspace %q does not match account %q (expected prefix %q)", ipSpace, accountUUID, expectedPrefix)
	}
	return nil
}

// PODataLifName returns a data LIF name: <svmName>_<8hex postfix>.
func PODataLifName(svmName, postfix string) string {
	return fmt.Sprintf("%s_%s", svmName, postfix)
}

// PODefaultSubnetMaskSize is the default CVN tenant network prefix length for PO
// pools. CVN accepts only 27 or 28.
const PODefaultSubnetMaskSize = 27

// POPoolSubnetMaskSize returns a valid CVN subnet mask size, defaulting to
// PODefaultSubnetMaskSize when the configured value is not 27 or 28.
func POPoolSubnetMaskSize(configured int) int {
	if configured == MinSubnetMaskSize || configured == MaxSubnetMaskSize {
		return configured
	}
	return PODefaultSubnetMaskSize
}

// POCVNNetworkTier maps a PO service level to CVN vlan bandwidth and FlexGroup flag.
func POCVNNetworkTier(serviceLevel string) (vlanBandwidth string, isFlexGroup bool) {
	bw := POVlanBandwidth()
	if serviceLevel == ServiceLevelExtreme {
		return bw, true
	}
	return bw, false
}

// POCIDRFromStartIPAndNetmask builds a CIDR string from a persisted ip_ranges row.
func POCIDRFromStartIPAndNetmask(startIP, netmask string) (string, error) {
	ip := net.ParseIP(startIP)
	maskIP := net.ParseIP(netmask)
	if ip == nil || maskIP == nil {
		return "", fmt.Errorf("invalid start IP %q or netmask %q", startIP, netmask)
	}
	mask := net.IPMask(maskIP.To4())
	ones, bits := mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("invalid netmask %q", netmask)
	}
	network := ip.Mask(mask)
	return fmt.Sprintf("%s/%d", network.String(), ones), nil
}

// PODataPathFromIPRange maps a persisted ip_ranges row to PO datapath CIDR and gateway.
func PODataPathFromIPRange(ir *datamodel.IPRange) (cidr, gateway string, err error) {
	if ir == nil {
		return "", "", fmt.Errorf("nil IP range")
	}
	if strings.TrimSpace(ir.Gateway) == "" {
		return "", "", fmt.Errorf("IP range %s has no gateway", ir.UUID)
	}
	cidr, err = POCIDRFromStartIPAndNetmask(ir.StartIP, ir.Netmask)
	if err != nil {
		return "", "", err
	}
	return cidr, ir.Gateway, nil
}

// PONetmaskFromCIDR returns the dotted-decimal netmask for the given CIDR range
// (e.g. "10.0.0.0/24" -> "255.255.255.0").
func PONetmaskFromCIDR(cidr string) (string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid CVN IP range %q: %w", cidr, err)
	}
	mask := ipNet.Mask
	if len(mask) != net.IPv4len {
		// Normalize IPv4 masks stored as 16-byte slices.
		if v4 := net.IP(mask).To4(); v4 != nil {
			mask = net.IPMask(v4)
		}
	}
	return net.IP(mask).String(), nil
}

// POHostAddresses selects up to count usable host addresses from the CVN CIDR
// range, skipping the network address, the broadcast address and the gateway.
// It returns the selected addresses and the range's dotted-decimal netmask.
//
// This replaces the previous behavior of using the raw CIDR string as a LIF
// address. Addresses are enumerated deterministically (ascending) so the same
// inputs always yield the same assignment, which is required for workflow
// determinism.
func POHostAddresses(cidr, gateway string, count int) ([]string, string, error) {
	if count <= 0 {
		return nil, "", fmt.Errorf("address count must be positive, got %d", count)
	}
	ip, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid CVN IP range %q: %w", cidr, err)
	}
	ip4 := ip.To4()
	if ip4 == nil || ipNet.IP.To4() == nil {
		return nil, "", fmt.Errorf("CVN IP range %q is not IPv4", cidr)
	}

	netmask, err := PONetmaskFromCIDR(cidr)
	if err != nil {
		return nil, "", err
	}

	network := ipNet.IP.To4()
	broadcast := make(net.IP, net.IPv4len)
	for i := 0; i < net.IPv4len; i++ {
		broadcast[i] = network[i] | ^ipNet.Mask[i]
	}

	gatewayIP := net.ParseIP(gateway).To4()

	addresses := make([]string, 0, count)
	for cur := cloneIPv4(network); ipNet.Contains(cur) && len(addresses) < count; cur = nextIPv4(cur) {
		if cur.Equal(network) || cur.Equal(broadcast) {
			continue
		}
		if gatewayIP != nil && cur.Equal(gatewayIP) {
			continue
		}
		addresses = append(addresses, cur.String())
	}

	if len(addresses) < count {
		return nil, "", fmt.Errorf("CVN IP range %q holds %d usable addresses, need %d", cidr, len(addresses), count)
	}
	return addresses, netmask, nil
}

func cloneIPv4(ip net.IP) net.IP {
	out := make(net.IP, net.IPv4len)
	copy(out, ip.To4())
	return out
}

func nextIPv4(ip net.IP) net.IP {
	out := cloneIPv4(ip)
	for i := net.IPv4len - 1; i >= 0; i-- {
		out[i]++
		if out[i] != 0 {
			break
		}
	}
	return out
}

// PORequiredDataLifCount returns the number of NFS data LIFs to pre-create for a PO
// pool: one per cluster node.
func PORequiredDataLifCount(nodeCount int) int {
	if nodeCount <= 0 {
		return 0
	}
	return nodeCount
}

// POInterclusterServicePolicy is the ONTAP service policy for intercluster LIFs.
const POInterclusterServicePolicy = "default-intercluster"

// PODefaultICLifSubnetMaskSize is the default CVN secondary-range prefix for IC LIFs.
const PODefaultICLifSubnetMaskSize = 28

// POICLifHexSuffixLen is the length (in hex characters) of the per-LIF random
// tail appended after the account-scoped prefix. Six hex chars give 2^24
// values per batch, which is comfortably unique for the small per-account
// node counts we generate at a time.
const POICLifHexSuffixLen = 6

// GeneratePOICLifNames returns count unique intercluster LIF names of the
// form "ic-lif-<accountUUID without dashes>-<6hex>". Dash stripping matches
// the data-path naming convention (ipspace_/bdomain_/svm_ all embed the
// account UUID via NormalizePOAccountUUID) and keeps the LIF name's three
// hyphens as pure structural separators. The 6-hex tail ensures per-batch
// uniqueness within an account. Total length is 7 + 32 + 1 + 6 = 46 chars,
// well under the 63-char ONTAP LIF limit.
//
// The hex tail is drawn from the same crypto/rand primitive
// (GenerateSVMPostfix) as GeneratePODataLifPostfixes so both LIF-naming
// paths share one randomness source.
//
// Intended for use inside a Temporal workflow.SideEffect so randomness
// does not break replay determinism — mirrors GeneratePODataLifPostfixes.
func GeneratePOICLifNames(accountUUID string, count int) ([]string, error) {
	if count <= 0 {
		return nil, fmt.Errorf("ic lif name count must be positive, got %d", count)
	}
	if accountUUID == "" {
		return nil, fmt.Errorf("account UUID must not be empty")
	}
	prefix := "ic-lif-" + NormalizePOAccountUUID(accountUUID) + "-"
	used := make(map[string]bool, count)
	result := make([]string, 0, count)
	for attempts := 0; len(result) < count && attempts < count*10; attempts++ {
		hex6, err := GenerateSVMPostfix(POICLifHexSuffixLen)
		if err != nil {
			return nil, err
		}
		if used[hex6] {
			continue
		}
		used[hex6] = true
		result = append(result, prefix+hex6)
	}
	if len(result) < count {
		return nil, fmt.Errorf("unable to generate %d unique ic lif names", count)
	}
	return result, nil
}

// POICLifConfig holds configuration for PO intercluster LIF ensure.
type POICLifConfig struct {
	Enabled        bool
	SubnetMaskSize int
	BasePort       string
}

// DefaultPOICLifConfig loads PO IC LIF config from environment.
// Subnet mask must be 27 or 28; other values fall back to PODefaultICLifSubnetMaskSize.
func DefaultPOICLifConfig() POICLifConfig {
	mask := env.GetInt(env.EnvPOICLifSubnetMaskSize, PODefaultICLifSubnetMaskSize)
	if mask != MinSubnetMaskSize && mask != MaxSubnetMaskSize {
		mask = PODefaultICLifSubnetMaskSize
	}
	return POICLifConfig{
		Enabled:        env.GetBool(env.EnvEnableICLifsForPOPools, true),
		SubnetMaskSize: mask,
		BasePort:       env.GetString(env.EnvPOLifBasePort, "a0a"),
	}
}

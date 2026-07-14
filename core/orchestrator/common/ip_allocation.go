package common

import (
	"fmt"
	"net"

	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// ErrNoIPAddressesAvailable is returned when an IP range cannot satisfy the requested count.
// Callers (e.g. preSelectIPRange) match this exact message for CVS parity.
const ErrNoIPAddressesAvailable = "No IP addresses available"

// AllocateIPAddressesFromRange selects up to count host addresses from [start, end],
// skipping network, broadcast, gateway, and any address present in allocatedIPs.
// Addresses are enumerated in ascending order for workflow determinism.
func AllocateIPAddressesFromRange(start, end, gateway, netmask string, count int, allocatedIPs map[string]bool) ([]string, error) {
	if count <= 0 {
		return nil, fmt.Errorf("address count must be positive, got %d", count)
	}
	if err := validateIPRangeBounds(start, end, gateway, netmask); err != nil {
		return nil, err
	}

	startIP := net.ParseIP(start).To4()
	endIP := net.ParseIP(end).To4()
	netmaskIP := net.ParseIP(netmask).To4()
	gatewayIP := net.ParseIP(gateway).To4()

	networkIP, broadcastIP := networkAndBroadcastIPForRange(startIP, endIP, netmaskIP)

	if allocatedIPs == nil {
		allocatedIPs = make(map[string]bool)
	}
	allocatedIPs[gatewayIP.String()] = true
	allocatedIPs[networkIP] = true
	allocatedIPs[broadcastIP] = true

	allocations := make([]string, 0, count)
	for cur := cloneIPv4(startIP); compareIPv4(cur, endIP) <= 0 && len(allocations) < count; cur = nextIPv4(cur) {
		ip := cur.String()
		if allocatedIPs[ip] {
			continue
		}
		allocations = append(allocations, ip)
	}

	if len(allocations) == count {
		return allocations, nil
	}
	return nil, customerrors.NewUnavailableErr(ErrNoIPAddressesAvailable)
}

func validateIPRangeBounds(start, end, gateway, netmask string) error {
	if start == "" || end == "" || gateway == "" || netmask == "" {
		return fmt.Errorf("start, end, gateway, and netmask must be non-empty")
	}
	startIP := net.ParseIP(start)
	endIP := net.ParseIP(end)
	gatewayIP := net.ParseIP(gateway)
	netmaskIP := net.ParseIP(netmask)
	if startIP == nil || endIP == nil || gatewayIP == nil || netmaskIP == nil {
		return fmt.Errorf("invalid start, end, gateway, or netmask")
	}
	if compareIPv4(startIP.To4(), endIP.To4()) > 0 {
		return fmt.Errorf("start IP is higher than end IP")
	}
	return nil
}

func networkAndBroadcastIPForRange(startIP, endIP, netmaskIP net.IP) (string, string) {
	netmask := net.IPMask(netmaskIP)
	networkIP := startIP.Mask(netmask)
	broadcastIP := make(net.IP, len(networkIP))
	copy(broadcastIP, networkIP)
	for i := 0; i < len(netmask); i++ {
		broadcastIP[i] |= ^netmask[i]
	}
	return networkIP.String(), broadcastIP.String()
}

func compareIPv4(ip1, ip2 net.IP) int {
	a := ip1.To4()
	b := ip2.To4()
	for i := 0; i < net.IPv4len; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

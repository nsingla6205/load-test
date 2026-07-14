package common

import (
	"fmt"
	"strings"
)

// Vlan bandwidth tiers accepted by CVN for datapath creation.
const (
	VlanBandwidth10G = "BPS_10G"
	VlanBandwidth50G = "BPS_50G"
)

// Valid subnet mask sizes for CVN tenant networks (SDE MaximumTenantNetworkSize=27,
// MinimumTenantNetworkSize=28).
const (
	MinSubnetMaskSize = 27
	MaxSubnetMaskSize = 28
)

// DataPathParams is the request to create (or ensure) a CVN datapath. Field names
// align with the SDE cloud-volumes-network SDK. ZoneUUID is resolved from the
// zones table (scoped by LOCAL_REGION) in CVNActivity before calling CVN.
type DataPathParams struct {
	AccountUUID     string
	RemoteAccountID string
	ZoneUUID        string
	MicrozoneUUID   string
	SubnetMaskSize  int
	VlanBandwidth   string
	IsMultiLIF      bool
	IsFlexGroup     bool
	PoolUUID        string
	PoolID          int64
	// IpRangeUUID is an optional pre-selected data LIF ip_range (CVS preSelectIPRange parity).
	// When empty, CVN provisions or reuses the default range for the datapath.
	IpRangeUUID string
}

// DataPathResponse captures the outputs of a CVN datapath create that the PO
// pool path feeds into SVM LIF creation.
type DataPathResponse struct {
	DatapathID  string
	VlanID      string
	IPRange     string
	IpRangeUUID string
	Gateway     string
}

// Validate checks required datapath create inputs before calling CVN.
func (p DataPathParams) Validate() error {
	if strings.TrimSpace(p.AccountUUID) == "" {
		return fmt.Errorf("account uuid must not be empty")
	}
	if strings.TrimSpace(p.RemoteAccountID) == "" {
		return fmt.Errorf("remote account id must not be empty")
	}
	if strings.TrimSpace(p.MicrozoneUUID) == "" {
		return fmt.Errorf("microzone uuid must not be empty")
	}
	if p.SubnetMaskSize != MinSubnetMaskSize && p.SubnetMaskSize != MaxSubnetMaskSize {
		return fmt.Errorf("subnet mask size must be %d or %d, got %d", MinSubnetMaskSize, MaxSubnetMaskSize, p.SubnetMaskSize)
	}
	if strings.TrimSpace(p.VlanBandwidth) == "" {
		return fmt.Errorf("vlan bandwidth must not be empty")
	}
	return nil
}

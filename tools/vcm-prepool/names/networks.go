package names

import (
	"strings"

	hyperscaler_models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const (
	MgmtVpcName              = "mgmt-e0a-vpc-01"
	MgmtSubnetName           = "mgmt-e0a-subnet-01"
	MgmtInternalFirewallName = "ingress-" + MgmtVpcName + "-internal"
	MgmtPublicFirewallName   = "ingress-" + MgmtVpcName + "-public"

	IcVpcName      = "ic-e0b-vpc-01"
	IcSubnet       = "ic-e0b-subnet-01"
	IcFirewallName = "ingress-" + IcVpcName

	RsmVpcName = "rsm-e0c-vpc-01"

	RsmSubnetName   = "rsm-e0c-subnet-01"
	RsmFirewallName = "ingress-" + RsmVpcName

	FirewallPriority        int64  = 1000
	IngressTrafficDirection string = "INGRESS"

	MgmtInternalFirewallPortRules = "all"
)

type InternalVSANetwork struct {
	VpcName     string
	SubnetName  string
	IpCidrRange string
	Firewalls   []hyperscaler_models.Firewall
}

var (
	MgmtNetworkIpRange             = env.GetString("MGMT_NETWORK_IP_RANGE", "198.18.0.0/20")
	mgmtPublicFirewallSourceRanges = env.GetString("MGMT_REGIONAL_NAT_IP", "34.0.0.0/8,35.0.0.0/8,202.3.112.0/20,216.240.16.0/20,217.70.208.0/20,167.103.0.0/16,136.226.0.0/16,202.3.0.0/16")
	RsmNetworkIpRange              = env.GetString("RSM_NETWORK_IP_RANGE", "198.18.16.0/20")
	IcNetworkIpRange               = env.GetString("IC_NETWORK_IP_RANGE", "198.18.32.0/20")

	MgmtPublicFirewallPortRules = env.GetString("MGMT_FIREWALL_PORT_RULES", "tcp,22,443")
	RSMFirewallPortRules        = env.GetString("RSM_FIREWALL_PORT_RULES", "tcp,udp")
	IcFirewallPortRules         = env.GetString("IC_FIREWALL_PORT_RULES", "tcp,udp")

	mgmtInternalFirewallSourceRanges = env.GetString("MGMT_FIREWALL_SOURCE_RANGES", "198.18.0.0/15")
	icFirewallSourceRanges           = env.GetString("IC_FIREWALL_SOURCE_RANGES", "198.18.0.0/15")
	rsmFirewallSourceRanges          = env.GetString("RSM_FIREWALL_SOURCE_RANGES", "198.18.0.0/15")
	dataFirewallSourceRanges         = env.GetString("DATA_FIREWALL_SOURCE_RANGES", "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16")

	// Same env defaults as hyperscaler/google CreateTPSubnetOp (DATA_SUBNET_CIDR_BLOCK / _LV).
	dataSubnetIPPrefixLength   = env.GetInt64("DATA_SUBNET_CIDR_BLOCK", 28)
	lvDataSubnetIPPrefixLength = env.GetInt64("DATA_SUBNET_CIDR_BLOCK_LV", 26)
)

// DataSubnetPrefixLength is the SN-host data subnet mask CreateTPSubnetOp will allocate.
func DataSubnetPrefixLength(largeCapacity bool) int {
	if largeCapacity {
		return int(lvDataSubnetIPPrefixLength)
	}
	return int(dataSubnetIPPrefixLength)
}

var InternalVSANetworks = map[string]InternalVSANetwork{
	MgmtVpcName: {
		VpcName:     MgmtVpcName,
		SubnetName:  MgmtSubnetName,
		IpCidrRange: MgmtNetworkIpRange,
		Firewalls: []hyperscaler_models.Firewall{
			{
				Name:             MgmtInternalFirewallName,
				SourceRanges:     splitNonEmpty(mgmtInternalFirewallSourceRanges),
				AllowedPortRules: strings.Split(MgmtInternalFirewallPortRules, ","),
				Priority:         FirewallPriority,
				Direction:        IngressTrafficDirection,
			},
			{
				Name:             MgmtPublicFirewallName,
				SourceRanges:     splitNonEmpty(mgmtPublicFirewallSourceRanges),
				AllowedPortRules: strings.Split(MgmtPublicFirewallPortRules, ","),
				Priority:         FirewallPriority,
				Direction:        IngressTrafficDirection,
			},
		},
	},
	IcVpcName: {
		VpcName:     IcVpcName,
		SubnetName:  IcSubnet,
		IpCidrRange: IcNetworkIpRange,
		Firewalls: []hyperscaler_models.Firewall{
			{
				Name:             IcFirewallName,
				SourceRanges:     splitNonEmpty(icFirewallSourceRanges),
				AllowedPortRules: strings.Split(IcFirewallPortRules, ","),
				Priority:         FirewallPriority,
				Direction:        IngressTrafficDirection,
			},
		},
	},
	RsmVpcName: {
		VpcName:     RsmVpcName,
		SubnetName:  RsmSubnetName,
		IpCidrRange: RsmNetworkIpRange,
		Firewalls: []hyperscaler_models.Firewall{
			{
				Name:             RsmFirewallName,
				SourceRanges:     splitNonEmpty(rsmFirewallSourceRanges),
				AllowedPortRules: strings.Split(RSMFirewallPortRules, ","),
				Priority:         FirewallPriority,
				Direction:        IngressTrafficDirection,
			},
		},
	},
}

var DataFirewalls = []hyperscaler_models.Firewall{
	{
		Name:             "vsa-iscsi-data-fw",
		SourceRanges:     splitNonEmpty(dataFirewallSourceRanges),
		AllowedPortRules: strings.Split("tcp,3260", ","),
		Priority:         FirewallPriority,
		Direction:        IngressTrafficDirection,
	},
	{
		Name:             "vsa-intercluster-data-fw",
		SourceRanges:     splitNonEmpty(dataFirewallSourceRanges),
		AllowedPortRules: strings.Split("tcp,10566,11104,11105", ","),
		Priority:         FirewallPriority,
		Direction:        IngressTrafficDirection,
	},
	{
		Name:             "vsa-nfs-data-fw",
		SourceRanges:     splitNonEmpty(dataFirewallSourceRanges),
		AllowedPortRules: strings.Split("tcp,111,635,2049,4045,63001-65000,udp,111,4046", ","),
		Priority:         FirewallPriority,
		Direction:        IngressTrafficDirection,
	},
}

func splitNonEmpty(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	values := strings.Split(value, ",")
	out := make([]string, 0, len(values))
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

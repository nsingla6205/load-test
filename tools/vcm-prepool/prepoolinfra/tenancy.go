package prepoolinfra

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
	"google.golang.org/api/servicenetworking/v1"
)

// Pre-pool data subnets are unique per tenant name prefix, SN-host region, and mask.
// Standard HA uses /28; large-capacity uses /26. Setup always provisions the standard mask.
// TODO: Later I will setup API for LV Prepool Setup, then will remove following.
const largeCapacityDataSubnet = false

type TenancyInfo struct {
	TenantProject   string
	SNHostProject   string
	DataNetworkName string
	DataSubnetName  string
}

func (p *provisioner) findTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion string) (string, error) {
	slog.Info("finding tenant project",
		"consumerNetwork", consumerNetwork, "customerProjectNumber", customerProjectNumber, "tenantProjectRegion", tenantProjectRegion)
	tp, err := p.GCPService.GetTenantProject(consumerNetwork, customerProjectNumber, tenantProjectRegion)
	if err != nil {
		slog.Error("failed to resolve tenant project", "error", err)
		return "", fmt.Errorf("resolve tenant project: %w", err)
	}

	return tp, nil
}

func (p *provisioner) getTenancyInfo(ctx context.Context, consumerNetwork, tenantProject, tenantProjectRegion string) (TenancyInfo, error) {
	slog.Info("getting tenancy info",
		"consumerNetwork", consumerNetwork, "tenancyProject", tenantProject, "tenantProjectRegion", tenantProjectRegion)

	svc := p.GCPService

	// Step 1: Resolve SN host project via GetXpnHost
	snHostProject, err := p.GCPService.GetSnHost(tenantProject)
	if err != nil {
		return TenancyInfo{}, fmt.Errorf("get SN host project for %s: %w", tenantProject, err)
	}
	slog.Info("SN host project resolved", "snHostProject", snHostProject)

	// Step 2: Reuse a subnet that matches tenant name prefix, region, and mask.
	subnetPrefix := names.GenerateDataSubnetPrefix(tenantProject)
	requiredPrefixLen := names.DataSubnetPrefixLength(largeCapacityDataSubnet)
	var dataSubnet *models.Subnet
	if snHostProject != "" {
		subnets, err := svc.ListSubnetworks(snHostProject, tenantProjectRegion)
		if err != nil {
			return TenancyInfo{}, fmt.Errorf("list subnets in SN host %s region %s: %w", snHostProject, tenantProjectRegion, err)
		}
		dataSubnet = findReusableDataSubnet(subnets, subnetPrefix, requiredPrefixLen)
		if dataSubnet != nil {
			slog.Info("found existing data subnet",
				"subnet", dataSubnet.Name, "network", dataSubnet.Network,
				"ipCidrRange", dataSubnet.IpCidrRange, "subnetPrefix", subnetPrefix,
				"prefixLength", requiredPrefixLen)
		}
	}

	// Step 3: No match for this prefix + region + mask → create via Service Networking
	if dataSubnet == nil {
		slog.Info("no existing data subnet found, creating via Service Networking",
			"tenantProject", tenantProject, "consumerNetwork", consumerNetwork,
			"region", tenantProjectRegion, "subnetPrefix", subnetPrefix,
			"prefixLength", requiredPrefixLen)

		subnetName := names.GenerateDataSubnetName(tenantProject)

		opName, err := svc.CreateTPSubnetOp(tenantProject, consumerNetwork,
			tenantProjectRegion, subnetName, largeCapacityDataSubnet, []string{})
		if err != nil {
			return TenancyInfo{}, fmt.Errorf("create data subnet via Service Networking: %w", err)
		}
		slog.Info("data subnet creation started", "operation", *opName)

		opResult, err := waitForOperation(ctx, "data subnet", *opName, func() (*models.ComputeOperation, error) {
			return svc.GetServiceNetOpStatus(*opName)
		})
		if err != nil {
			return TenancyInfo{}, err
		}
		slog.Info("data subnet operation completed", "operation", *opName)

		// Parse the subnet from the operation response
		snSubnet := &servicenetworking.Subnetwork{}
		if err := json.Unmarshal(opResult.Response, snSubnet); err != nil {
			return TenancyInfo{}, fmt.Errorf("parse subnet from operation response: %w", err)
		}
		dataSubnet = &models.Subnet{
			Name:    snSubnet.Name,
			Network: snSubnet.Network,
		}
		slog.Info("data subnet created",
			"subnet", dataSubnet.Name, "network", dataSubnet.Network)
	}

	// Step 4: Extract network name from subnet.Network
	// e.g. "projects/<sn-host-project>/global/networks/<network-name>"
	dataNetworkName := extractNetworkName(dataSubnet.Network)
	if snHostProject == "" {
		snHostProject = extractProjectFromNetwork(dataSubnet.Network)
	}

	slog.Info("tenancy info resolved",
		"snHostProject", snHostProject, "dataNetwork", dataNetworkName,
		"dataSubnet", dataSubnet.Name)

	return TenancyInfo{
		TenantProject:   tenantProject,
		SNHostProject:   snHostProject,
		DataNetworkName: dataNetworkName,
		DataSubnetName:  dataSubnet.Name,
	}, nil
}

// findReusableDataSubnet returns the first subnet whose name has subnetPrefix
// (region is already applied by ListSubnetworks) and whose CIDR mask matches
// requiredPrefixLen.
func findReusableDataSubnet(subnets *[]models.Subnet, subnetPrefix string, requiredPrefixLen int) *models.Subnet {
	if subnets == nil {
		return nil
	}
	var match *models.Subnet
	for i := range *subnets {
		s := &(*subnets)[i]
		if !strings.HasPrefix(s.Name, subnetPrefix) {
			continue
		}
		prefixLen, ok := cidrPrefixLength(s.IpCidrRange)
		if !ok || prefixLen != requiredPrefixLen {
			continue
		}
		if match != nil {
			slog.Warn("multiple SN-host data subnets share prefix, region, and mask; using the first",
				"selected", match.Name, "alsoMatched", s.Name,
				"subnetPrefix", subnetPrefix, "prefixLength", requiredPrefixLen)
			continue
		}
		found := *s
		match = &found
	}
	return match
}

func cidrPrefixLength(cidr string) (int, bool) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, false
	}
	ones, bits := ipNet.Mask.Size()
	if bits == 0 {
		return 0, false
	}
	return ones, true
}

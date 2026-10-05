package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
	"google.golang.org/api/compute/v1"
)

type CreatedVPCs struct {
	MgmtVPC string
	IcVPC   string
	RsmVPC  string
}

func (p *provisioner) createVPCs(ctx context.Context, tenantProject string) (CreatedVPCs, error) {
	slog.Info("creating VPCs", "tenantProject", tenantProject)

	if err := runConcurrent(ctx, internalNetworks(), func(ctx context.Context, net names.InternalVSANetwork) error {
		if err := p.createVPC(ctx, tenantProject, net); err != nil {
			return fmt.Errorf("create VPC %s: %w", net.VpcName, err)
		}
		return nil
	}); err != nil {
		return CreatedVPCs{}, err
	}

	return CreatedVPCs{
		MgmtVPC: names.MgmtVpcName,
		IcVPC:   names.IcVpcName,
		RsmVPC:  names.RsmVpcName,
	}, nil
}

func (p *provisioner) createVPC(ctx context.Context, tenantProject string, net names.InternalVSANetwork) error {
	svc := p.GCPService
	existing, err := svc.GetVPCNetwork(tenantProject, net.VpcName)
	if err == nil && existing != nil {
		slog.Info("VPC already exists, skipping", "vpc", net.VpcName)
		return nil
	}

	vpcNetwork := &models.VPCNetwork{Name: net.VpcName, ProjectName: tenantProject}
	opName, err := svc.CreateVPC(vpcNetwork)
	if err != nil {
		return err
	}
	slog.Info("VPC creation started", "vpc", net.VpcName, "operation", opName)

	_, err = waitForOperation(ctx, "VPC", opName, func() (*models.ComputeOperation, error) {
		return svc.GetComputeGlobalOpStatus(tenantProject, opName)
	})
	if err != nil {
		return err
	}
	slog.Info("VPC creation completed", "vpc", net.VpcName, "operation", opName)
	return nil
}

type CreatedSubnets struct {
	MgmtSubnet string
	IcSubnet   string
	RsmSubnet  string
}

func (p *provisioner) createSubnets(ctx context.Context, tenantProject, region string) (CreatedSubnets, error) {
	slog.Info("creating subnets", "tenantProject", tenantProject, "region", region)

	if err := runConcurrent(ctx, internalNetworks(), func(ctx context.Context, net names.InternalVSANetwork) error {
		if err := p.createSubnet(ctx, tenantProject, region, net); err != nil {
			return fmt.Errorf("create subnet %s: %w", net.SubnetName, err)
		}
		return nil
	}); err != nil {
		return CreatedSubnets{}, err
	}

	return CreatedSubnets{
		MgmtSubnet: names.MgmtSubnetName,
		IcSubnet:   names.IcSubnet,
		RsmSubnet:  names.RsmSubnetName,
	}, nil
}

func (p *provisioner) createSubnet(ctx context.Context, tenantProject, region string, net names.InternalVSANetwork) error {
	svc := p.GCPService
	existing, err := svc.GetSubnetwork(tenantProject, region, net.SubnetName)
	if err == nil && existing != nil {
		slog.Info("subnet already exists, skipping", "subnet", net.SubnetName)
		return nil
	}

	subnetRequest := &models.Subnet{
		Name:        net.SubnetName,
		Network:     fmt.Sprintf("projects/%s/global/networks/%s", tenantProject, net.VpcName),
		IpCidrRange: net.IpCidrRange,
		Region:      &region,
		ProjectName: tenantProject,
	}
	opName, err := svc.CreateSubnetwork(subnetRequest)
	if err != nil {
		return err
	}
	slog.Info("subnet creation started", "subnet", net.SubnetName, "operation", opName)

	_, err = waitForOperation(ctx, "subnet", opName, func() (*models.ComputeOperation, error) {
		return svc.GetComputeRegionalOpStatus(tenantProject, region, opName)
	})
	if err != nil {
		return err
	}
	slog.Info("subnet creation completed", "subnet", net.SubnetName, "operation", opName)
	return nil
}

// insertFirewallIfNotExists creates a firewall rule if it doesn't already exist.
func (p *provisioner) insertFirewallIfNotExists(ctx context.Context, fwReq *models.Firewall) error {
	svc := p.GCPService
	existing, err := svc.GetFirewall(fwReq.ProjectName, fwReq.Name)
	if err == nil && existing != nil {
		slog.Info("activity: firewall already exists, skipping", "firewall", fwReq.Name)
		return nil
	}

	opName, err := svc.InsertFirewall(fwReq)
	if err != nil {
		return err
	}
	slog.Info("activity: firewall creation started", "firewall", fwReq.Name, "operation", opName)
	if _, err := waitForOperation(ctx, "firewall", opName, func() (*models.ComputeOperation, error) {
		return svc.GetComputeGlobalOpStatus(fwReq.ProjectName, opName)
	}); err != nil {
		return err
	}
	slog.Info("activity: firewall creation completed", "firewall", fwReq.Name, "operation", opName)
	return nil
}

func (p *provisioner) createFirewalls(ctx context.Context, tenantProject, dataNetwork, snHostProject string) (int, error) {
	slog.Info("creating firewalls", "tenantProject", tenantProject, "dataNetwork", dataNetwork, "snHostProject", snHostProject)

	if snHostProject == "" {
		snHostProject = tenantProject
	}
	dataNetworkName := extractNetworkName(dataNetwork)

	firewalls := make([]*models.Firewall, 0, len(names.InternalVSANetworks)+len(names.DataFirewalls))
	for _, net := range names.InternalVSANetworks {
		for _, fw := range net.Firewalls {
			firewalls = append(firewalls, &models.Firewall{
				Name:             fw.Name,
				AllowedPortRules: fw.AllowedPortRules,
				SourceRanges:     fw.SourceRanges,
				VPCNetworkName:   net.VpcName,
				ProjectName:      tenantProject,
				Priority:         fw.Priority,
				Direction:        fw.Direction,
			})
		}
	}
	for _, fw := range names.DataFirewalls {
		firewalls = append(firewalls, &models.Firewall{
			Name:             fw.Name,
			AllowedPortRules: fw.AllowedPortRules,
			SourceRanges:     fw.SourceRanges,
			VPCNetworkName:   dataNetworkName,
			ProjectName:      snHostProject,
			Priority:         fw.Priority,
			Direction:        fw.Direction,
		})
	}

	if err := runConcurrent(ctx, firewalls, func(ctx context.Context, fwReq *models.Firewall) error {
		if err := p.insertFirewallIfNotExists(ctx, fwReq); err != nil {
			return fmt.Errorf("create firewall %s: %w", fwReq.Name, err)
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return len(firewalls), nil
}

func internalNetworks() []names.InternalVSANetwork {
	nets := make([]names.InternalVSANetwork, 0, len(names.InternalVSANetworks))
	for _, net := range names.InternalVSANetworks {
		nets = append(nets, net)
	}
	return nets
}

// deleteNetworks removes firewalls, then subnets, then VPCs created by Setup.
// Each phase runs in parallel (same bounded workers as VSA cluster cleanup);
// phases stay sequential so subnets wait for firewalls and VPCs wait for subnets.
// Data-plane firewalls live in the SN host project; internal mgmt/IC/RSM
// resources live in the tenant project.
func (p *provisioner) deleteNetworks(ctx context.Context, tenantProject, region, snHostProject string) error {
	slog.Info("deleting networks", "tenantProject", tenantProject, "region", region, "snHostProject", snHostProject)

	if err := p.deleteFirewalls(ctx, tenantProject, snHostProject); err != nil {
		return err
	}
	if err := p.deleteSubnets(ctx, tenantProject, region); err != nil {
		return err
	}
	if err := p.deleteVPCs(ctx, tenantProject); err != nil {
		return err
	}
	return nil
}

type firewallRef struct {
	project string
	name    string
}

func (p *provisioner) deleteFirewalls(ctx context.Context, tenantProject, snHostProject string) error {
	if strings.TrimSpace(snHostProject) == "" {
		return fmt.Errorf("SN host project is required to delete data-plane firewalls")
	}

	firewalls := make([]firewallRef, 0, len(names.InternalVSANetworks)+len(names.DataFirewalls))
	for _, net := range names.InternalVSANetworks {
		for _, fw := range net.Firewalls {
			firewalls = append(firewalls, firewallRef{project: tenantProject, name: fw.Name})
		}
	}
	for _, fw := range names.DataFirewalls {
		firewalls = append(firewalls, firewallRef{project: snHostProject, name: fw.Name})
	}

	return runConcurrent(ctx, firewalls, func(ctx context.Context, fw firewallRef) error {
		if err := p.deleteFirewall(ctx, fw.project, fw.name); err != nil {
			return fmt.Errorf("delete firewall %s: %w", fw.name, err)
		}
		return nil
	})
}

func (p *provisioner) deleteSubnets(ctx context.Context, tenantProject, region string) error {
	subnets := make([]string, 0, len(names.InternalVSANetworks))
	for _, net := range names.InternalVSANetworks {
		subnets = append(subnets, net.SubnetName)
	}
	return runConcurrent(ctx, subnets, func(ctx context.Context, name string) error {
		if err := p.deleteSubnet(ctx, tenantProject, region, name); err != nil {
			return fmt.Errorf("delete subnet %s: %w", name, err)
		}
		return nil
	})
}

func (p *provisioner) deleteVPCs(ctx context.Context, tenantProject string) error {
	vpcs := make([]string, 0, len(names.InternalVSANetworks))
	for _, net := range names.InternalVSANetworks {
		vpcs = append(vpcs, net.VpcName)
	}
	return runConcurrent(ctx, vpcs, func(ctx context.Context, name string) error {
		if err := p.deleteVPC(ctx, tenantProject, name); err != nil {
			return fmt.Errorf("delete VPC %s: %w", name, err)
		}
		return nil
	})
}

func (p *provisioner) deleteFirewall(ctx context.Context, project, name string) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	return p.deleteAndWaitGlobal(ctx, project, "firewall", name, func() (*compute.Operation, error) {
		return computeService.Firewalls.Delete(project, name).Context(ctx).Do()
	})
}

func (p *provisioner) deleteSubnet(ctx context.Context, project, region, name string) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	return p.deleteAndWaitRegional(ctx, project, region, "subnet", name, func() (*compute.Operation, error) {
		return computeService.Subnetworks.Delete(project, region, name).Context(ctx).Do()
	})
}

func (p *provisioner) deleteVPC(ctx context.Context, project, name string) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	return p.deleteAndWaitGlobal(ctx, project, "VPC", name, func() (*compute.Operation, error) {
		return computeService.Networks.Delete(project, name).Context(ctx).Do()
	})
}

func (p *provisioner) deleteAndWaitGlobal(
	ctx context.Context,
	project, resourceType, name string,
	call func() (*compute.Operation, error),
) error {
	return p.deleteAndWait(ctx, resourceType, name, call, func(opName string) (*models.ComputeOperation, error) {
		return p.GCPService.GetComputeGlobalOpStatus(project, opName)
	})
}

func (p *provisioner) deleteAndWaitRegional(
	ctx context.Context,
	project, region, resourceType, name string,
	call func() (*compute.Operation, error),
) error {
	return p.deleteAndWait(ctx, resourceType, name, call, func(opName string) (*models.ComputeOperation, error) {
		return p.GCPService.GetComputeRegionalOpStatus(project, region, opName)
	})
}

func (p *provisioner) deleteAndWait(
	ctx context.Context,
	resourceType, name string,
	call func() (*compute.Operation, error),
	getStatus func(opName string) (*models.ComputeOperation, error),
) error {
	computeOperation, err := startDeletion(ctx, "delete "+resourceType+" "+name, call)
	if err != nil {
		return fmt.Errorf("delete %s %s: %w", resourceType, name, err)
	}
	if computeOperation == nil {
		slog.Info(resourceType+" already gone, skipping", resourceType, name)
		return nil
	}
	slog.Info(resourceType+" deletion started", resourceType, name, "operation", computeOperation.Name)
	_, err = waitForOperation(ctx, resourceType, computeOperation.Name, func() (*models.ComputeOperation, error) {
		return getStatus(computeOperation.Name)
	})
	if err != nil {
		return fmt.Errorf("wait for %s delete: %w", resourceType, err)
	}
	slog.Info(resourceType+" deletion completed", resourceType, name)
	return nil
}

func (p *provisioner) ListIPAddressesByLabels(ctx context.Context, tenantProject, region string, m map[string]string) (*compute.AddressList, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if region == "" {
		return nil, fmt.Errorf("list IP addresses: region is required")
	}
	call := computeService.Addresses.List(tenantProject, region)
	if filter := labelFilter(m); filter != "" {
		call = call.Filter(filter)
	}
	ipAddresses, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list IP addresses: %w", err)
	}
	return ipAddresses, nil
}

func (p *provisioner) ListIPAddressesByNamePrefix(ctx context.Context, tenantProject, region, deploymentName string) ([]*compute.Address, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if region == "" {
		return nil, fmt.Errorf("list IP addresses by prefix: region is required")
	}
	filter := fmt.Sprintf("name eq \"%s-.*\"", deploymentName)
	resp, err := computeService.Addresses.List(tenantProject, region).
		Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list IP addresses by prefix: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Items, nil
}

func (p *provisioner) ListForwardingRulesByLabels(ctx context.Context, tenantProject, region string, m map[string]string) (*compute.ForwardingRuleList, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if region == "" {
		return nil, fmt.Errorf("list forwarding rules: region is required")
	}
	call := computeService.ForwardingRules.List(tenantProject, region)
	if filter := labelFilter(m); filter != "" {
		call = call.Filter(filter)
	}
	rules, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list forwarding rules: %w", err)
	}
	return rules, nil
}

// ListForwardingRulesByNamePrefix returns forwarding rules whose name starts
// with deploymentName followed by a hyphen. This is a fallback for resources
// that VLM does not label with deployment_id.
func (p *provisioner) ListForwardingRulesByNamePrefix(ctx context.Context, tenantProject, region, deploymentName string) ([]*compute.ForwardingRule, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if region == "" {
		return nil, fmt.Errorf("list forwarding rules by prefix: region is required")
	}
	filter := fmt.Sprintf("name eq \"%s-.*\"", deploymentName)
	resp, err := computeService.ForwardingRules.List(tenantProject, region).
		Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list forwarding rules by prefix: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Items, nil
}

func (p *provisioner) DeleteForwardingRule(ctx context.Context, tenantProject string, rule *compute.ForwardingRule) error {
	region := resourceID(rule.Region)
	return p.deleteAndWaitRegional(ctx, tenantProject, region, "forwarding rule", rule.Name, func() (*compute.Operation, error) {
		computeService, err := p.GCPService.GetComputeService(ctx)
		if err != nil {
			return nil, fmt.Errorf("get compute service: %w", err)
		}
		return computeService.ForwardingRules.Delete(tenantProject, region, rule.Name).Context(ctx).Do()
	})
}

func (p *provisioner) DeleteIPAddress(ctx context.Context, tenantProject string, ipAddress *compute.Address) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	region := resourceID(ipAddress.Region)
	computeOperation, err := startDeletion(ctx, "delete IP address "+ipAddress.Name, func() (*compute.Operation, error) {
		return computeService.Addresses.Delete(tenantProject, region, ipAddress.Name).Context(ctx).Do()
	})
	if err != nil {
		return fmt.Errorf("delete IP address: %w", err)
	}
	if computeOperation == nil {
		return nil
	}
	_, err = waitForOperation(ctx, "IP address", computeOperation.Name, func() (*models.ComputeOperation, error) {
		op, err := computeService.RegionOperations.Get(tenantProject, region, computeOperation.Name).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		result := &models.ComputeOperation{
			Name:     op.Name,
			Status:   op.Status,
			Progress: op.Progress,
			Done:     op.Status == "DONE",
		}
		if op.Error != nil && len(op.Error.Errors) > 0 {
			result.ErrorResponse = op.Error.Errors[0].Message
		}
		return result, nil
	})
	if err != nil {
		return fmt.Errorf("wait for operation: %w", err)
	}
	return nil
}

// extractNetworkName extracts the network name from a full resource path.
// e.g. "projects/123/global/networks/default" → "default"
func extractNetworkName(network string) string {
	if idx := strings.LastIndex(network, "/"); idx >= 0 {
		return network[idx+1:]
	}
	return network
}

// extractProjectFromNetwork extracts the project from "projects/<project>/global/networks/<name>"
func extractProjectFromNetwork(network string) string {
	parts := strings.Split(network, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

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

// createJumpHost provisions the SSH firewall rule and then the e2-micro VM
// on the management subnet. Both operations are idempotent: an existing
// resource with the expected name is reused.
func (p *provisioner) createJumpHost(ctx context.Context, tenantProject, region string) error {
	slog.Info("creating jump host resources", "tenantProject", tenantProject, "region", region)

	// 1. Firewall first — must exist before the tagged VM is useful.
	// Uses the Compute API directly so target tags can be set without
	// changing the shared hyperscaler Firewall model.
	if err := p.insertJumpHostFirewallIfNotExists(ctx, tenantProject); err != nil {
		return fmt.Errorf("create jump host firewall: %w", err)
	}

	// 2. VM on the management subnet.
	zone := names.JumpHostZone(region)
	if err := p.insertJumpHostIfNotExists(ctx, tenantProject, region, zone); err != nil {
		return fmt.Errorf("create jump host instance: %w", err)
	}
	return nil
}

// insertJumpHostFirewallIfNotExists creates the target-tagged SSH firewall
// rule for the jump host. It calls the Compute API directly because the
// shared models.Firewall struct does not carry TargetTags.
func (p *provisioner) insertJumpHostFirewallIfNotExists(ctx context.Context, tenantProject string) error {
	svc := p.GCPService
	existing, err := svc.GetFirewall(tenantProject, names.JumpHostFirewallName)
	if err == nil && existing != nil {
		slog.Info("jump host firewall already exists, skipping", "firewall", names.JumpHostFirewallName)
		return nil
	}

	computeService, err := svc.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}

	fw := &compute.Firewall{
		Name:        names.JumpHostFirewallName,
		Description: "Allow SSH for " + names.JumpHostFirewallName,
		Network:     fmt.Sprintf("projects/%s/global/networks/%s", tenantProject, names.MgmtVpcName),
		Allowed: []*compute.FirewallAllowed{
			{IPProtocol: "tcp", Ports: []string{"22"}},
		},
		SourceRanges: names.JumpHostSSHSourceRanges(),
		TargetTags:   []string{names.JumpHostTag},
		Direction:    names.IngressTrafficDirection,
		Priority:     names.JumpHostFirewallPriority,
	}

	op, err := computeService.Firewalls.Insert(tenantProject, fw).Context(ctx).Do()
	if err != nil {
		// Handle race: another request may have created it concurrently.
		if strings.Contains(err.Error(), "alreadyExists") {
			slog.Info("jump host firewall created concurrently, skipping", "firewall", names.JumpHostFirewallName)
			return nil
		}
		return fmt.Errorf("insert jump host firewall: %w", err)
	}
	slog.Info("jump host firewall creation started", "firewall", names.JumpHostFirewallName, "operation", op.Name)

	if _, err := waitForOperation(ctx, "firewall", op.Name, func() (*models.ComputeOperation, error) {
		return svc.GetComputeGlobalOpStatus(tenantProject, op.Name)
	}); err != nil {
		return err
	}
	slog.Info("jump host firewall creation completed", "firewall", names.JumpHostFirewallName)
	return nil
}

// insertJumpHostIfNotExists creates the jump-host VM when it does not already
// exist. The instance uses an ephemeral external IP so sshuttle can reach it
// through IAP without a NAT gateway.
func (p *provisioner) insertJumpHostIfNotExists(ctx context.Context, tenantProject, region, zone string) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}

	// Idempotency: check whether the VM already exists.
	existing, err := computeService.Instances.Get(tenantProject, zone, names.JumpHostName).Context(ctx).Do()
	if err == nil && existing != nil {
		slog.Info("jump host instance already exists, skipping", "instance", names.JumpHostName, "zone", zone)
		return nil
	}
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("describe jump host: %w", err)
	}

	labels := names.JumpHostLabels()
	instance := &compute.Instance{
		Name:        names.JumpHostName,
		MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", zone, names.JumpHostMachineType),
		Tags:        &compute.Tags{Items: []string{names.JumpHostTag}},
		Labels:      labels,
		Disks: []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					DiskSizeGb:  int64(names.JumpHostBootDiskGB),
					DiskType:    fmt.Sprintf("zones/%s/diskTypes/%s", zone, names.JumpHostBootDiskType),
					SourceImage: fmt.Sprintf("projects/%s/global/images/family/%s", names.JumpHostImageProject, names.JumpHostImageFamily),
				},
			},
		},
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Subnetwork: fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", tenantProject, region, names.MgmtSubnetName),
				StackType:  "IPV4_ONLY",
				AccessConfigs: []*compute.AccessConfig{
					{
						Name: "External NAT",
						Type: "ONE_TO_ONE_NAT",
					},
				},
			},
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{Key: "enable-oslogin", Value: stringPtr("TRUE")},
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible: false,
		},
	}

	op, err := computeService.Instances.Insert(tenantProject, zone, instance).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("insert jump host: %w", err)
	}
	slog.Info("jump host creation started", "instance", names.JumpHostName, "operation", op.Name)

	_, err = waitForOperation(ctx, "instance", op.Name, func() (*models.ComputeOperation, error) {
		zoneOp, err := computeService.ZoneOperations.Get(tenantProject, zone, op.Name).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		result := &models.ComputeOperation{
			Name:     zoneOp.Name,
			Status:   zoneOp.Status,
			Progress: zoneOp.Progress,
			Done:     zoneOp.Status == "DONE",
		}
		if zoneOp.Error != nil && len(zoneOp.Error.Errors) > 0 {
			result.ErrorResponse = zoneOp.Error.Errors[0].Message
		}
		return result, nil
	})
	if err != nil {
		return fmt.Errorf("wait for jump host creation: %w", err)
	}

	slog.Info("jump host creation completed", "instance", names.JumpHostName, "zone", zone)
	return nil
}

// deleteJumpHost removes the jump-host VM and then its SSH firewall rule.
// Missing resources are treated as success (idempotent).
func (p *provisioner) deleteJumpHost(ctx context.Context, tenantProject, region string) error {
	slog.Info("deleting jump host resources", "tenantProject", tenantProject, "region", region)

	zone := names.JumpHostZone(region)

	// 1. Delete the VM first.
	if err := p.deleteJumpHostInstance(ctx, tenantProject, zone); err != nil {
		return fmt.Errorf("delete jump host instance: %w", err)
	}

	// 2. Delete the firewall rule.
	if err := p.deleteFirewall(ctx, tenantProject, names.JumpHostFirewallName); err != nil {
		return fmt.Errorf("delete jump host firewall: %w", err)
	}

	return nil
}

func (p *provisioner) deleteJumpHostInstance(ctx context.Context, tenantProject, zone string) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}

	computeOperation, err := startDeletion(ctx, "delete instance "+names.JumpHostName, func() (*compute.Operation, error) {
		return computeService.Instances.Delete(tenantProject, zone, names.JumpHostName).Context(ctx).Do()
	})
	if err != nil {
		return fmt.Errorf("delete jump host: %w", err)
	}
	if computeOperation == nil {
		slog.Info("jump host instance already gone, skipping", "instance", names.JumpHostName)
		return nil
	}

	slog.Info("jump host deletion started", "instance", names.JumpHostName, "operation", computeOperation.Name)
	_, err = waitForOperation(ctx, "instance", computeOperation.Name, func() (*models.ComputeOperation, error) {
		op, err := computeService.ZoneOperations.Get(tenantProject, zone, computeOperation.Name).Context(ctx).Do()
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
		return fmt.Errorf("wait for jump host deletion: %w", err)
	}
	slog.Info("jump host deletion completed", "instance", names.JumpHostName)
	return nil
}

func stringPtr(s string) *string {
	return &s
}

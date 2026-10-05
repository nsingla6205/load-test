package prepoolinfra

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/compute/v1"
)

// deploymentIDLabelKey is the label VLM stamps on the resources it creates for a
// deployment. GCP label keys are lowercase-only, so this must stay snake_case to
// match; see vlm.DeploymentIDTagKey.
const (
	deploymentIDLabelKey = "deployment_id"
	cleanupConcurrency   = 8
)

type vsaClusterResources struct {
	instances       []*compute.Instance
	disks           []*compute.Disk
	forwardingRules []*compute.ForwardingRule
	ipAddresses     []*compute.Address
}

func (p *provisioner) deleteVSACluster(
	ctx context.Context,
	deploymentName string,
	tenantProject string,
	region string,
	zones []string,
) error {
	deploymentLabels := map[string]string{deploymentIDLabelKey: deploymentName}
	resources, err := p.discoverVSAClusterResources(ctx, tenantProject, region, zones, deploymentName, deploymentLabels)
	if err != nil {
		return err
	}

	slog.Info("VSA cluster resources matched",
		"deploymentName", deploymentName,
		"labelSelector", labelFilter(deploymentLabels),
		"instances", len(resources.instances),
		"disks", len(resources.disks),
		"forwardingRules", len(resources.forwardingRules),
		"ipAddresses", len(resources.ipAddresses),
		"zones", zones)

	// All instances can be stopped concurrently. Deletion remains a separate
	// phase so no delete races a still-running VM.
	if err := runConcurrent(ctx, resources.instances, func(ctx context.Context, instance *compute.Instance) error {
		if err := p.ShutdownInstance(ctx, tenantProject, instance); err != nil {
			return fmt.Errorf("shutdown instance %s: %w", instance.Name, err)
		}
		slog.Info("instance shutdown", "instanceName", instance.Name)
		return nil
	}); err != nil {
		return err
	}

	if err := runConcurrent(ctx, resources.instances, func(ctx context.Context, instance *compute.Instance) error {
		if err := p.DeleteInstance(ctx, tenantProject, instance); err != nil {
			return fmt.Errorf("delete instance %s: %w", instance.Name, err)
		}
		slog.Info("instance deleted", "instanceName", instance.Name)
		return nil
	}); err != nil {
		return err
	}

	if len(resources.ipAddresses) == 0 {
		slog.Warn("no IP addresses matched deployment label or name prefix", "deploymentName", deploymentName, "region", region)
	}

	// Forwarding rules hold references to IP addresses, so they must be
	// deleted first. Otherwise Addresses.Delete returns 400
	// resourceInUseByAnotherResource.
	if err := runConcurrent(ctx, resources.forwardingRules, func(ctx context.Context, rule *compute.ForwardingRule) error {
		if err := p.DeleteForwardingRule(ctx, tenantProject, rule); err != nil {
			return fmt.Errorf("delete forwarding rule %s: %w", rule.Name, err)
		}
		slog.Info("forwarding rule deleted", "forwardingRuleName", rule.Name)
		return nil
	}); err != nil {
		return err
	}

	// Once instances and forwarding rules are gone, their persistent disks
	// and reserved addresses are independent and can be deleted in the same
	// bounded worker group.
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(cleanupConcurrency)
	for _, disk := range resources.disks {
		group.Go(func() error {
			if err := p.DeleteDisk(groupCtx, tenantProject, disk); err != nil {
				return fmt.Errorf("delete disk %s: %w", disk.Name, err)
			}
			slog.Info("disk deleted", "diskName", disk.Name)
			return nil
		})
	}
	for _, ipAddress := range resources.ipAddresses {
		group.Go(func() error {
			if err := p.DeleteIPAddress(groupCtx, tenantProject, ipAddress); err != nil {
				return fmt.Errorf("delete IP address %s: %w", ipAddress.Name, err)
			}
			slog.Info("IP address deleted", "ipAddressName", ipAddress.Name)
			return nil
		})
	}
	return group.Wait()
}

func (p *provisioner) discoverVSAClusterResources(
	ctx context.Context,
	tenantProject string,
	region string,
	zones []string,
	deploymentName string,
	labels map[string]string,
) (vsaClusterResources, error) {
	var resources vsaClusterResources
	var mu sync.Mutex

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(cleanupConcurrency)

	// --- zonal resources: instances and disks (per zone) ---
	for _, zone := range zones {
		group.Go(func() error {
			instances, err := p.discoverInstances(groupCtx, tenantProject, zone, deploymentName, labels)
			if err != nil {
				return fmt.Errorf("discover instances in zone %s: %w", zone, err)
			}
			mu.Lock()
			resources.instances = append(resources.instances, instances...)
			mu.Unlock()
			return nil
		})
		group.Go(func() error {
			disks, err := p.discoverDisks(groupCtx, tenantProject, zone, deploymentName, labels)
			if err != nil {
				return fmt.Errorf("discover disks in zone %s: %w", zone, err)
			}
			mu.Lock()
			resources.disks = append(resources.disks, disks...)
			mu.Unlock()
			return nil
		})
	}

	// --- regional resources: IP addresses and forwarding rules ---
	group.Go(func() error {
		addrs, err := p.discoverIPAddresses(groupCtx, tenantProject, region, deploymentName, labels)
		if err != nil {
			return fmt.Errorf("discover IP addresses: %w", err)
		}
		mu.Lock()
		resources.ipAddresses = append(resources.ipAddresses, addrs...)
		mu.Unlock()
		return nil
	})
	group.Go(func() error {
		fwdRules, err := p.discoverForwardingRules(groupCtx, tenantProject, region, deploymentName, labels)
		if err != nil {
			return fmt.Errorf("discover forwarding rules: %w", err)
		}
		mu.Lock()
		resources.forwardingRules = append(resources.forwardingRules, fwdRules...)
		mu.Unlock()
		return nil
	})

	if err := group.Wait(); err != nil {
		return vsaClusterResources{}, err
	}
	return resources, nil
}

func runConcurrent[T any](ctx context.Context, items []T, operation func(context.Context, T) error) error {
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(cleanupConcurrency)
	for _, item := range items {
		group.Go(func() error {
			return operation(groupCtx, item)
		})
	}
	return group.Wait()
}

// unionByName merges items from a label-based query and a name-prefix query.
// Items found only via prefix (unlabeled) are logged as warnings. T must have
// a Name string field accessible via the getName function.
func unionByName[T any](resourceType, deploymentName string, labeled, byPrefix []T, getName func(T) string) []T {
	seen := make(map[string]struct{}, len(labeled))
	merged := make([]T, 0, len(labeled)+len(byPrefix))
	for _, item := range labeled {
		seen[getName(item)] = struct{}{}
		merged = append(merged, item)
	}
	for _, item := range byPrefix {
		name := getName(item)
		if _, ok := seen[name]; ok {
			continue
		}
		slog.Warn(resourceType+" matched by name prefix but missing deployment label; include in cleanup as fallback",
			"resource", name,
			"deploymentName", deploymentName,
			"expectedLabel", deploymentIDLabelKey)
		merged = append(merged, item)
	}
	return merged
}

func (p *provisioner) discoverInstances(
	ctx context.Context,
	tenantProject, zone, deploymentName string,
	labels map[string]string,
) ([]*compute.Instance, error) {
	labeled, err := p.ListInstancesByLabels(ctx, tenantProject, zone, labels)
	if err != nil {
		return nil, err
	}
	byPrefix, err := p.ListInstancesByNamePrefix(ctx, tenantProject, zone, deploymentName)
	if err != nil {
		return nil, err
	}
	var labeledItems []*compute.Instance
	if labeled != nil {
		labeledItems = labeled.Items
	}
	return unionByName("instance", deploymentName, labeledItems, byPrefix, func(i *compute.Instance) string { return i.Name }), nil
}

func (p *provisioner) discoverDisks(
	ctx context.Context,
	tenantProject, zone, deploymentName string,
	labels map[string]string,
) ([]*compute.Disk, error) {
	labeled, err := p.ListDisksByLabels(ctx, tenantProject, zone, labels)
	if err != nil {
		return nil, err
	}
	byPrefix, err := p.ListDisksByNamePrefix(ctx, tenantProject, zone, deploymentName)
	if err != nil {
		return nil, err
	}
	var labeledItems []*compute.Disk
	if labeled != nil {
		labeledItems = labeled.Items
	}
	return unionByName("disk", deploymentName, labeledItems, byPrefix, func(d *compute.Disk) string { return d.Name }), nil
}

func (p *provisioner) discoverIPAddresses(
	ctx context.Context,
	tenantProject, region, deploymentName string,
	labels map[string]string,
) ([]*compute.Address, error) {
	labeled, err := p.ListIPAddressesByLabels(ctx, tenantProject, region, labels)
	if err != nil {
		return nil, err
	}
	byPrefix, err := p.ListIPAddressesByNamePrefix(ctx, tenantProject, region, deploymentName)
	if err != nil {
		return nil, err
	}
	var labeledItems []*compute.Address
	if labeled != nil {
		labeledItems = labeled.Items
	}
	return unionByName("IP address", deploymentName, labeledItems, byPrefix, func(a *compute.Address) string { return a.Name }), nil
}

// discoverForwardingRules returns the union of forwarding rules found by
// deployment label and by name prefix. VLM currently does not label forwarding
// rules with deployment_id, so the name-prefix query is a fallback. Rules found
// only via prefix (unlabeled) are logged as warnings so the gap is visible.
func (p *provisioner) discoverForwardingRules(
	ctx context.Context,
	tenantProject, region, deploymentName string,
	labels map[string]string,
) ([]*compute.ForwardingRule, error) {
	labeled, err := p.ListForwardingRulesByLabels(ctx, tenantProject, region, labels)
	if err != nil {
		return nil, err
	}
	byPrefix, err := p.ListForwardingRulesByNamePrefix(ctx, tenantProject, region, deploymentName)
	if err != nil {
		return nil, err
	}
	var labeledItems []*compute.ForwardingRule
	if labeled != nil {
		labeledItems = labeled.Items
	}
	return unionByName("forwarding rule", deploymentName, labeledItems, byPrefix, func(r *compute.ForwardingRule) string { return r.Name }), nil
}

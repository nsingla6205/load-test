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
	instances   []*compute.Instance
	disks       []*compute.Disk
	ipAddresses []*compute.Address
}

func (p *provisioner) deleteVSACluster(
	ctx context.Context,
	deploymentName string,
	tenantProject string,
	region string,
	zones []string,
) error {
	deploymentLabels := map[string]string{deploymentIDLabelKey: deploymentName}
	resources, err := p.discoverVSAClusterResources(ctx, tenantProject, region, zones, deploymentLabels)
	if err != nil {
		return err
	}

	slog.Info("VSA cluster resources matched",
		"deploymentName", deploymentName,
		"labelSelector", labelFilter(deploymentLabels),
		"instances", len(resources.instances),
		"disks", len(resources.disks),
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
		slog.Warn("no IP addresses matched deployment label", "deploymentName", deploymentName, "region", region)
	}

	// Once instances are gone, their persistent disks and reserved addresses
	// are independent and can be deleted in the same bounded worker group.
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
	labels map[string]string,
) (vsaClusterResources, error) {
	var resources vsaClusterResources
	var mu sync.Mutex

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(cleanupConcurrency)
	for _, zone := range zones {
		group.Go(func() error {
			instances, err := p.ListInstancesByLabels(groupCtx, tenantProject, zone, labels)
			if err != nil {
				return fmt.Errorf("list compute instances in zone %s: %w", zone, err)
			}
			if instances != nil {
				mu.Lock()
				resources.instances = append(resources.instances, instances.Items...)
				mu.Unlock()
			}
			return nil
		})
		group.Go(func() error {
			disks, err := p.ListDisksByLabels(groupCtx, tenantProject, zone, labels)
			if err != nil {
				return fmt.Errorf("list compute disks in zone %s: %w", zone, err)
			}
			if disks != nil {
				mu.Lock()
				resources.disks = append(resources.disks, disks.Items...)
				mu.Unlock()
			}
			return nil
		})
	}
	group.Go(func() error {
		ipAddresses, err := p.ListIPAddressesByLabels(groupCtx, tenantProject, region, labels)
		if err != nil {
			return fmt.Errorf("list IP addresses: %w", err)
		}
		if ipAddresses != nil {
			mu.Lock()
			resources.ipAddresses = append(resources.ipAddresses, ipAddresses.Items...)
			mu.Unlock()
		}
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

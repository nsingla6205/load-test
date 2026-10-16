package prepoolinfra

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// labelFilter renders labels as a Compute list filter expression, e.g.
// `labels.deploymentId = "gcnv-abc" AND labels.role = "node"`. Keys are sorted
// so the expression is stable. An empty map yields an empty filter, which
// callers must treat as "no filter" rather than passing it to the API.
func labelFilter(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	terms := make([]string, 0, len(keys))
	for _, key := range keys {
		terms = append(terms, fmt.Sprintf("labels.%s = %q", key, labels[key]))
	}
	return strings.Join(terms, " AND ")
}

// resourceID returns the last path segment of a Compute self-link, or the input
// unchanged if it is already a short name. List responses populate Zone and
// Region as URLs; mutation APIs require the short name.
func resourceID(selfLink string) string {
	if idx := strings.LastIndex(selfLink, "/"); idx >= 0 {
		return selfLink[idx+1:]
	}
	return selfLink
}

// isNotFound reports whether err is a Compute 404. During cleanup that means the
// resource is already gone, which is the desired end state.
func isNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == http.StatusNotFound
	}
	return false
}

// startDeletion issues a mutating call that returns a long-running operation,
// retrying transient provider errors. A nil operation means the resource no
// longer exists and there is nothing left to wait for. Retries are safe because
// a 502 may hide a request that already succeeded; the retry then sees a 404.
func startDeletion(
	ctx context.Context,
	description string,
	call func() (*compute.Operation, error),
) (*compute.Operation, error) {
	var operation *compute.Operation
	err := retryTransient(ctx, description, func() error {
		op, err := call()
		if err != nil {
			if isNotFound(err) {
				operation = nil
				return nil
			}
			return err
		}
		operation = op
		return nil
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

// getZoneList returns the names of the zones that belong to region in projectID.
// projectID may be a project number, so zones are matched on the region path
// suffix rather than a full region URL: the URL Compute returns always carries
// the project ID and would never match a number-based filter.
func (p *provisioner) getZoneList(ctx context.Context, projectID, region string) ([]string, error) {
	if region == "" {
		return nil, fmt.Errorf("list zones: region is required")
	}

	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}

	var zones []string
	regionSuffix := "/regions/" + region
	err = computeService.Zones.List(projectID).Pages(ctx, func(page *compute.ZoneList) error {
		for _, zone := range page.Items {
			if strings.HasSuffix(zone.Region, regionSuffix) {
				zones = append(zones, zone.Name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list zones: %w", err)
	}
	if len(zones) == 0 {
		return nil, fmt.Errorf("list zones: no zones found for project %s in region %s", projectID, region)
	}
	return zones, nil
}

func (p *provisioner) ListInstancesByLabels(ctx context.Context, projectID, zone string, labels map[string]string) (*compute.InstanceList, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list instances: zone is required")
	}
	call := computeService.Instances.List(projectID, zone)
	if filter := labelFilter(labels); filter != "" {
		call = call.Filter(filter)
	}
	instances, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	return instances, nil
}

func (p *provisioner) ListInstancesByNamePrefix(ctx context.Context, projectID, zone, deploymentName string) ([]*compute.Instance, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list instances by prefix: zone is required")
	}
	filter := fmt.Sprintf("name eq \"%s-.*\"", deploymentName)
	resp, err := computeService.Instances.List(projectID, zone).
		Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list instances by prefix: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Items, nil
}

func (p *provisioner) ShutdownInstance(ctx context.Context, tenantProject string, instance *compute.Instance) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	// Compute rejects Stop on a VM with Local SSDs unless the caller states what
	// happens to that data. Reclaim deletes these instances next, so the Local
	// SSD contents are discarded deliberately.
	zone := resourceID(instance.Zone)
	computeOperation, err := startDeletion(ctx, "stop instance "+instance.Name, func() (*compute.Operation, error) {
		return computeService.Instances.Stop(tenantProject, zone, instance.Name).
			DiscardLocalSsd(true).
			Context(ctx).
			Do()
	})
	if err != nil {
		return fmt.Errorf("shutdown instance: %w", err)
	}
	if computeOperation == nil {
		return nil
	}

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
		return fmt.Errorf("wait for operation: %w", err)
	}
	return nil
}

func (p *provisioner) DeleteInstance(ctx context.Context, tenantProject string, instance *compute.Instance) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	zone := resourceID(instance.Zone)
	computeOperation, err := startDeletion(ctx, "delete instance "+instance.Name, func() (*compute.Operation, error) {
		return computeService.Instances.Delete(tenantProject, zone, instance.Name).Context(ctx).Do()
	})
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	if computeOperation == nil {
		return nil
	}

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
		return fmt.Errorf("wait for operation: %w", err)
	}
	return nil
}

func (p *provisioner) ListDisksByLabels(ctx context.Context, tenantProject, zone string, m map[string]string) (*compute.DiskList, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list disks: zone is required")
	}
	call := computeService.Disks.List(tenantProject, zone)
	if filter := labelFilter(m); filter != "" {
		call = call.Filter(filter)
	}
	disks, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list disks: %w", err)
	}
	return disks, nil
}

func (p *provisioner) ListDisksByNamePrefix(ctx context.Context, tenantProject, zone, deploymentName string) ([]*compute.Disk, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list disks by prefix: zone is required")
	}
	filter := fmt.Sprintf("name eq \"%s-.*\"", deploymentName)
	resp, err := computeService.Disks.List(tenantProject, zone).
		Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list disks by prefix: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Items, nil
}

func (p *provisioner) DeleteDisk(ctx context.Context, tenantProject string, disk *compute.Disk) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	zone := resourceID(disk.Zone)
	computeOperation, err := startDeletion(ctx, "delete disk "+disk.Name, func() (*compute.Operation, error) {
		return computeService.Disks.Delete(tenantProject, zone, disk.Name).Context(ctx).Do()
	})
	if err != nil {
		return fmt.Errorf("delete disk: %w", err)
	}
	if computeOperation == nil {
		return nil
	}
	_, err = waitForOperation(ctx, "disk", computeOperation.Name, func() (*models.ComputeOperation, error) {
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
		return fmt.Errorf("wait for operation: %w", err)
	}
	return nil
}

func (p *provisioner) ListNetworkEndpointGroupsByLabels(ctx context.Context, tenantProject, zone string, labels map[string]string) (*compute.NetworkEndpointGroupList, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list network endpoint groups: zone is required")
	}
	call := computeService.NetworkEndpointGroups.List(tenantProject, zone)
	if filter := labelFilter(labels); filter != "" {
		call = call.Filter(filter)
	}
	groups, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list network endpoint groups: %w", err)
	}
	return groups, nil
}

func (p *provisioner) ListNetworkEndpointGroupsByNamePrefix(ctx context.Context, tenantProject, zone, deploymentName string) ([]*compute.NetworkEndpointGroup, error) {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return nil, fmt.Errorf("get compute service: %w", err)
	}
	if zone == "" {
		return nil, fmt.Errorf("list network endpoint groups by prefix: zone is required")
	}
	filter := fmt.Sprintf("name eq \"%s-.*\"", deploymentName)
	resp, err := computeService.NetworkEndpointGroups.List(tenantProject, zone).
		Filter(filter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list network endpoint groups by prefix: %w", err)
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Items, nil
}

func (p *provisioner) DeleteNetworkEndpointGroup(ctx context.Context, tenantProject string, neg *compute.NetworkEndpointGroup) error {
	computeService, err := p.GCPService.GetComputeService(ctx)
	if err != nil {
		return fmt.Errorf("get compute service: %w", err)
	}
	zone := resourceID(neg.Zone)
	computeOperation, err := startDeletion(ctx, "delete network endpoint group "+neg.Name, func() (*compute.Operation, error) {
		return computeService.NetworkEndpointGroups.Delete(tenantProject, zone, neg.Name).Context(ctx).Do()
	})
	if err != nil {
		return fmt.Errorf("delete network endpoint group: %w", err)
	}
	if computeOperation == nil {
		return nil
	}
	_, err = waitForOperation(ctx, "network endpoint group", computeOperation.Name, func() (*models.ComputeOperation, error) {
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
		return fmt.Errorf("wait for operation: %w", err)
	}
	return nil
}

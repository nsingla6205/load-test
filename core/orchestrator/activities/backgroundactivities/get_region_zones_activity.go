package backgroundactivities

import (
	"context"
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// RegionZoneLister is the subset of *hyperscaler.Services used to enumerate
// the zones in a (project, region) pair. Defined here so the activity can
// be unit-tested without spinning up a real GCP client.
//
// projectNumber is the project the worker SA must have compute IAM on; in
// production the activity passes env.SecretManagerProjectID (the
// control-plane host project) because zone topology is GCP-wide and that
// is the project we already require compute API access in for Secret
// Manager and Private CA reads.
type RegionZoneLister interface {
	GetZones(projectNumber, region string) ([]string, error)
}

// gcpServiceForZones is overridable in tests. It must return something
// that satisfies RegionZoneLister; in production it returns the resolved
// hyperscaler.Services via the same path the rest of the orchestrator uses.
var gcpServiceForZones = func(ctx context.Context) (RegionZoneLister, error) {
	svc, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, err
	}
	return svc, nil
}

// GetRegionZonesActivity is registered on the background worker and is the
// activity body of GetRegionZonesWorkflow. It returns the list of zones in
// LOCAL_REGION as reported by compute.zones.list, going through the
// control-plane host project so we don't depend on per-tenant compute IAM.
//
// The leaked-resources pool detector kicks off the wrapping workflow once
// per detector tick to learn which zones it should fan a CCFE
// FetchStoragePoolsWorkflow out to per VCP account, so the diff covers
// every zone in the region (including ones where VCP currently has no
// pool, which is exactly how we surface "in_ccfe_not_in_vcp" leaks against
// pools that exist only in CCFE).
//
// The GCP field is optional. When nil (the default registered on the
// worker) the activity lazily resolves the cloud service via
// hyperscaler.ResolveCloudService at call time, matching how every other
// background activity acquires its cloud client. Tests substitute the field
// with a fake so they don't need real compute API credentials.
type GetRegionZonesActivity struct {
	GCP RegionZoneLister
}

// GetRegionZones returns the zone names reported for region by GCP. An
// empty SECRET_MANAGER_PROJECT_ID short-circuits to (nil, nil) so the
// workflow caller can degrade to region-only enumeration instead of
// retrying forever on a missing env var that won't get filled in by
// retrying. Real GCP errors propagate back so Temporal's RetryPolicy can
// retry the call.
func (a *GetRegionZonesActivity) GetRegionZones(ctx context.Context, region string) ([]string, error) {
	logger := util.GetLogger(ctx)

	if region == "" {
		return nil, errors.New("GetRegionZonesActivity.GetRegionZones: region is empty")
	}

	project := env.SecretManagerProjectID
	if project == "" {
		logger.Warnf("GetRegionZonesActivity.GetRegionZones: SECRET_MANAGER_PROJECT_ID is empty; returning no zones (region=%s)", region)
		return nil, nil
	}

	gcp := a.gcp()
	if gcp == nil {
		var err error
		gcp, err = gcpServiceForZones(ctx)
		if err != nil {
			logger.Warnf("GetRegionZonesActivity.GetRegionZones: cloud service initialization failed: %v", err)
			return nil, err
		}
	}

	zones, err := gcp.GetZones(project, region)
	if err != nil {
		logger.Warnf("GetRegionZonesActivity.GetRegionZones: GetZones failed project=%s region=%s: %v", project, region, err)
		return nil, err
	}

	out := make([]string, 0, len(zones))
	for _, z := range zones {
		if z != "" {
			out = append(out, z)
		}
	}
	logger.Infof("GetRegionZonesActivity.GetRegionZones: resolved %d zone(s) in region=%s via control-plane project=%s",
		len(out), region, project)
	return out, nil
}

func (a *GetRegionZonesActivity) gcp() RegionZoneLister {
	if a == nil {
		return nil
	}
	return a.GCP
}

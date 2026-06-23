package api

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
)

type poolResponse struct {
	ID                          string            `json:"id,omitempty"`
	CompartmentId               string            `json:"compartmentId,omitempty"`
	ResourceName                string            `json:"resourceName,omitempty"`
	Region                      string            `json:"region,omitempty"`
	SubnetId                    string            `json:"subnetId,omitempty"`
	SizeInBytes                 int64             `json:"sizeInBytes,omitempty"`
	LifecycleState              string            `json:"lifecycleState,omitempty"`
	PrimaryAvailabilityDomain   string            `json:"primaryAvailabilityDomain,omitempty"`
	SecondaryAvailabilityDomain string            `json:"secondaryAvailabilityDomain,omitempty"`
	MediatorAvailabilityDomain  string            `json:"mediatorAvailabilityDomain,omitempty"`
	PoolOCID                    string            `json:"poolOCID,omitempty"`
	ThroughputMibps             float64           `json:"throughputMibps,omitempty"`
	Iops                        int64             `json:"iops,omitempty"`
	Description                 string            `json:"description,omitempty"`
	Tags                        map[string]string `json:"tags,omitempty"`
	TimeCreated                 *time.Time        `json:"timeCreated,omitempty"`
	TimeUpdated                 *time.Time        `json:"timeUpdated,omitempty"`
}

func convertToPoolOCI(pool *models.Pool) *poolResponse {
	if pool == nil {
		return &poolResponse{}
	}
	out := &poolResponse{
		ID:             pool.UUID,
		CompartmentId:  pool.AccountName,
		ResourceName:   pool.Name,
		PoolOCID:       pool.PoolOCID,
		Region:         pool.Region,
		SubnetId:       pool.VendorSubNetID,
		SizeInBytes:    int64(pool.SizeInBytes),
		LifecycleState: mapLifecycleState(pool.State),
		Description:    pool.Description,
	}
	if !pool.CreatedAt.IsZero() {
		out.TimeCreated = &pool.CreatedAt
	}
	if !pool.UpdatedAt.IsZero() {
		out.TimeUpdated = &pool.UpdatedAt
	}
	if pool.PoolAttributes != nil {
		out.PrimaryAvailabilityDomain = pool.PoolAttributes.PrimaryZone
		out.SecondaryAvailabilityDomain = pool.PoolAttributes.SecondaryZone
		if len(pool.PoolAttributes.Labels) > 0 {
			out.Tags = pool.PoolAttributes.Labels
		}
	}
	if pool.CustomPerformanceParams != nil && pool.CustomPerformanceParams.Enabled {
		out.ThroughputMibps = pool.CustomPerformanceParams.Throughput
		out.Iops = pool.CustomPerformanceParams.Iops
	} else {
		out.ThroughputMibps = pool.TotalThroughputMibps
		out.Iops = pool.TotalIops
	}
	return out
}

func mapLifecycleState(state string) string {
	switch strings.ToUpper(state) {
	case "CREATING":
		return "CREATING"
	case "ACTIVE", "AVAILABLE":
		return "ACTIVE"
	case "UPDATING":
		return "UPDATING"
	case "DELETING":
		return "DELETING"
	case "DELETED":
		return "DELETED"
	case "FAILED":
		return "FAILED"
	default:
		return "STATUS_UNKNOWN"
	}
}

// jsonMarshalPoolResponse is swappable in tests to cover the encode error path.
var jsonMarshalPoolResponse = func(p *poolResponse) ([]byte, error) {
	return json.Marshal(p)
}

func encodePoolV1(pool *poolResponse) (jx.Raw, error) {
	data, err := jsonMarshalPoolResponse(pool)
	if err != nil {
		return nil, err
	}
	return data, nil
}

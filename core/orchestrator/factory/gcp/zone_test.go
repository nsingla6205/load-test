package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestGCPOrchestrator_CreateZone(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	region, err := store.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	zone := &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}
	created, err := orch.CreateZone(ctx, zone, region.UUID)
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.Equal(t, "us-west1-a", created.Name)
	assert.Equal(t, "America/Los_Angeles", created.Timezone)
	assert.Equal(t, region.ID, created.RegionID.Int64)
}

func TestGCPOrchestrator_ListZones(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	region, err := store.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	_, err = store.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	zones, err := orch.ListZones(ctx)
	require.NoError(t, err)
	require.Len(t, zones, 1)
	assert.Equal(t, "us-west1-a", zones[0].Name)
	assert.Equal(t, region.ID, zones[0].RegionID.Int64)
}

func TestGCPOrchestrator_UpdateZone(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	region, err := store.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	created, err := store.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	updated, err := orch.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)
	assert.Equal(t, "us-west1-b", updated.Name)
}

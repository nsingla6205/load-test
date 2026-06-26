package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestGCPOrchestrator_CreateRegion(t *testing.T) {
	_, _, orch, _ := setup(t)
	ctx := context.Background()

	region := &datamodel.Region{Name: "us-west1"}
	created, err := orch.CreateRegion(ctx, region)
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.Equal(t, "us-west1", created.Name)
}

func TestGCPOrchestrator_ListRegions(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	region := &datamodel.Region{Name: "us-west1"}
	_, err := store.CreateRegion(ctx, region)
	require.NoError(t, err)

	regions, err := orch.ListRegions(ctx)
	require.NoError(t, err)
	require.Len(t, regions, 1)
	assert.Equal(t, "us-west1", regions[0].Name)
}

func TestGCPOrchestrator_UpdateRegion(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := context.Background()

	region := &datamodel.Region{Name: "us-west1"}
	created, err := store.CreateRegion(ctx, region)
	require.NoError(t, err)

	created.Name = "us-west2"
	updated, trackingID, err := orch.UpdateRegion(ctx, created)
	require.NoError(t, err)
	assert.Equal(t, "", trackingID)
	assert.Equal(t, "us-west2", updated.Name)
}

func TestGCPOrchestrator_UpdateRegion_Error(t *testing.T) {
	_, _, orch, _ := setup(t)
	ctx := context.Background()

	updated, trackingID, err := orch.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: "missing-region-uuid"},
		Name:      "us-west2",
	})
	require.Error(t, err)
	assert.Nil(t, updated)
	assert.Equal(t, "", trackingID)
}

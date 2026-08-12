package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
)

// inventoryParams builds a V1betaListProjectInventoryParams for tests.
func inventoryParams(project string, view ...oasgenserver.InventoryView) oasgenserver.V1betaListProjectInventoryParams {
	p := oasgenserver.V1betaListProjectInventoryParams{
		ProjectNumber: project,
		LocationId:    "us-east1",
	}
	if len(view) > 0 {
		p.View = oasgenserver.NewOptInventoryView(view[0])
	}
	return p
}

// --- Handler: delegates to h.Orchestrator.GetProjectInventory ---

func TestV1betaListProjectInventory_OrchestratorError_Returns500(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(nil, errors.New("db connection lost"))

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-1"))

	require.NoError(t, err)
	errRes, ok := res.(*oasgenserver.V1betaListProjectInventoryInternalServerError)
	require.True(t, ok)
	assert.Equal(t, float64(500), errRes.Code)
	assert.Contains(t, errRes.Message, "db connection lost")
}

func TestV1betaListProjectInventory_EmptyClusters_Returns404(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(&commonparams.ProjectInventoryResult{Clusters: nil}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-2"))

	require.NoError(t, err)
	notFound, ok := res.(*oasgenserver.V1betaListProjectInventoryNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
	assert.Contains(t, notFound.Message, "proj-2")
}

func TestV1betaListProjectInventory_DefaultView_PassesBasicToOrchestrator(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.MatchedBy(func(p *commonparams.GetProjectInventoryParams) bool {
			return p.ProjectNumber == "proj-3" && p.ShowVolumes == false
		})).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{{Name: "cluster-a", ProvisioningModel: "THICK"}},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-3"))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters, 1)
	assert.Equal(t, "cluster-a", inv.Clusters[0].Name)
}

func TestV1betaListProjectInventory_FullView_PassesShowVolumesToOrchestrator(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.MatchedBy(func(p *commonparams.GetProjectInventoryParams) bool {
			return p.ProjectNumber == "proj-3b" && p.ShowVolumes == true
		})).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{{Name: "cluster-a", ProvisioningModel: "THICK"}},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-3b", oasgenserver.InventoryViewFULL))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters, 1)
	assert.Equal(t, "cluster-a", inv.Clusters[0].Name)
}

func TestV1betaListProjectInventory_MapsClusterFieldsCorrectly(t *testing.T) {
	aggrUID := uuid.New()
	volUID := uuid.New()

	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{
				{
					Name:              "cluster-a",
					ProvisioningModel: "THIN",
					Size:              1000,
					UsedSize:          400,
					Aggregates: []commonparams.InventoryAggregate{
						{
							Name:          "aggr1",
							UUID:          aggrUID.String(),
							State:         "online",
							VolumeCount:   2,
							Size:          500,
							UsedSize:      200,
							AvailableSize: 300,
						},
					},
					SVMs: []commonparams.InventorySVM{
						{
							Name: "svm1",
							Volumes: []commonparams.InventoryVolume{
								{Name: "vol-a", UUID: volUID.String()},
							},
						},
					},
				},
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-4", oasgenserver.InventoryViewFULL))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters, 1)

	c := inv.Clusters[0]
	assert.Equal(t, "cluster-a", c.Name)
	assert.Equal(t, oasgenserver.InventoryClusterProvisioningModelTHIN, c.ProvisioningModel)
	assert.Equal(t, int64(1000), c.SizeInBytes.Value)
	assert.Equal(t, int64(400), c.ProvisionedSizeByPoolsInBytes.Value)

	require.Len(t, c.Aggregates, 1)
	a := c.Aggregates[0]
	assert.Equal(t, "aggr1", a.Name)
	assert.Equal(t, aggrUID, a.UUID.Value)
	assert.Equal(t, "online", a.State.Value)
	assert.Equal(t, int64(2), a.VolumeCount.Value)
	assert.Equal(t, int64(500), a.SizeInBytes.Value)
	assert.Equal(t, int64(200), a.UsedSizeInBytes.Value)
	assert.Equal(t, int64(300), a.AvailableSizeInBytes.Value)

	require.Len(t, c.Svms, 1)
	s := c.Svms[0]
	assert.Equal(t, "svm1", s.Name)
	require.Len(t, s.Volumes, 1)
	assert.Equal(t, "vol-a", s.Volumes[0].Name)
	assert.Equal(t, volUID, s.Volumes[0].UUID)
}

func TestV1betaListProjectInventory_ClusterWithZeroSize_SizeFieldsNotSet(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{
				{Name: "cluster-a", ProvisioningModel: "THICK", Size: 0, UsedSize: 0},
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-5"))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters, 1)
	assert.False(t, inv.Clusters[0].SizeInBytes.Set, "zero size should not set OptInt64")
	assert.False(t, inv.Clusters[0].ProvisionedSizeByPoolsInBytes.Set)
}

func TestV1betaListProjectInventory_VolumeWithInvalidUUID_Skipped(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{
				{
					Name:              "cluster-a",
					ProvisioningModel: "THICK",
					SVMs: []commonparams.InventorySVM{
						{
							Name: "svm1",
							Volumes: []commonparams.InventoryVolume{
								{Name: "vol-bad", UUID: "not-a-uuid"},
								{Name: "vol-good", UUID: uuid.New().String()},
							},
						},
					},
				},
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-6", oasgenserver.InventoryViewFULL))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters[0].Svms[0].Volumes, 1)
	assert.Equal(t, "vol-good", inv.Clusters[0].Svms[0].Volumes[0].Name)
}

func TestV1betaListProjectInventory_MultipleClusters_AllMapped(t *testing.T) {
	mockOrch := factory.NewMockOrchestratorFactory(t)
	mockOrch.EXPECT().
		GetProjectInventory(mock.Anything, mock.Anything).
		Return(&commonparams.ProjectInventoryResult{
			Clusters: []commonparams.InventoryCluster{
				{Name: "cluster-a", ProvisioningModel: "THICK"},
				{Name: "cluster-b", ProvisioningModel: "THIN"},
			},
		}, nil)

	h := Handler{Orchestrator: mockOrch}
	res, err := h.V1betaListProjectInventory(context.Background(), inventoryParams("proj-7"))

	require.NoError(t, err)
	inv, ok := res.(*oasgenserver.ProjectInventory)
	require.True(t, ok)
	require.Len(t, inv.Clusters, 2)
	assert.Equal(t, "cluster-a", inv.Clusters[0].Name)
	assert.Equal(t, "cluster-b", inv.Clusters[1].Name)
}

package gcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func TestListExpertModeVolumeTenancy(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(database.MockStorage)
	orch := GCPOrchestrator{storage: mockStorage}

	mockStorage.On(
		"ListExpertModeVolumeTenancyByCluster",
		ctx,
		"cluster-01",
		int64(0),
		2,
	).Return([]*database.ExpertModeVolumeTenancyRow{
		{
			CursorID:       42,
			VolumeUUID:     "uuid-1",
			SVMName:        "svm_a",
			VolumeName:     "vol_1",
			Project:        "111",
			PoolName:       "pool-a",
			TenantProject:  "tenant-a",
			DeploymentName: "deploy-a",
		},
		{CursorID: 43, VolumeUUID: "uuid-2"},
	}, nil)

	items, nextCursor, err := orch.ListExpertModeVolumeTenancy(ctx, commonparams.ListExpertModeVolumeTenancyParams{
		ClusterName: "cluster-01",
		Limit:       1,
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "uuid-1", items[0].UUID)
	assert.Equal(t, "42", nextCursor)
	mockStorage.AssertExpectations(t)
}

func TestListExpertModeVolumeTenancy_EmptyNextCursorOnShortPage(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(database.MockStorage)
	orch := GCPOrchestrator{storage: mockStorage}

	mockStorage.On(
		"ListExpertModeVolumeTenancyByCluster",
		ctx,
		"cluster-01",
		int64(10),
		3,
	).Return([]*database.ExpertModeVolumeTenancyRow{
		{CursorID: 11, VolumeUUID: "uuid-2", SVMName: "svm_b", VolumeName: "vol_2"},
	}, nil)

	_, nextCursor, err := orch.ListExpertModeVolumeTenancy(ctx, commonparams.ListExpertModeVolumeTenancyParams{
		ClusterName: "cluster-01",
		CursorID:    10,
		Limit:       2,
	})
	require.NoError(t, err)
	assert.Empty(t, nextCursor)
}

func TestListExpertModeVolumeTenancy_NormalizesLimitBeforePagination(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(database.MockStorage)
	orch := GCPOrchestrator{storage: mockStorage}

	mockStorage.On(
		"ListExpertModeVolumeTenancyByCluster",
		ctx,
		"cluster-01",
		int64(0),
		commonparams.MaxExpertModeVolumeTenancyQueryLimit,
	).Return([]*database.ExpertModeVolumeTenancyRow{}, nil)

	_, nextCursor, err := orch.ListExpertModeVolumeTenancy(ctx, commonparams.ListExpertModeVolumeTenancyParams{
		ClusterName: "cluster-01",
		Limit:       10000,
	})
	require.NoError(t, err)
	assert.Empty(t, nextCursor)
	mockStorage.AssertExpectations(t)
}

func TestListExpertModeVolumeTenancy_MaxPageUsesLookahead(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(database.MockStorage)
	orch := GCPOrchestrator{storage: mockStorage}

	rows := make([]*database.ExpertModeVolumeTenancyRow, commonparams.MaxExpertModeVolumeTenancyQueryLimit)
	for i := range rows {
		rows[i] = &database.ExpertModeVolumeTenancyRow{CursorID: int64(i + 1)}
	}
	mockStorage.On(
		"ListExpertModeVolumeTenancyByCluster",
		ctx,
		"cluster-01",
		int64(0),
		commonparams.MaxExpertModeVolumeTenancyQueryLimit,
	).Return(rows, nil)

	items, nextCursor, err := orch.ListExpertModeVolumeTenancy(ctx, commonparams.ListExpertModeVolumeTenancyParams{
		ClusterName: "cluster-01",
		Limit:       commonparams.MaxExpertModeVolumeTenancyPageLimit,
	})

	require.NoError(t, err)
	assert.Len(t, items, commonparams.MaxExpertModeVolumeTenancyPageLimit)
	assert.Equal(t, "5000", nextCursor)
	mockStorage.AssertExpectations(t)
}

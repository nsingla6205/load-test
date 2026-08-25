package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func arTestCtx() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, middleware.ContextSLoggerKey, log.NewLogger())
}

func TestGCPOrchestrator_CreateAddressRange(t *testing.T) {
	_, _, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-range",
		AddressRangeCidr:  "10.0.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	created, err := orch.CreateAddressRange(ctx, ar)
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.Equal(t, ar.Name, created.Name)
}

func TestGCPOrchestrator_GetAddressRange(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-get-range",
		AddressRangeCidr:  "10.1.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	created, err := store.CreateAddressRange(ctx, ar)
	require.NoError(t, err)

	got, err := orch.GetAddressRange(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, created.UUID, got.UUID)
}

func TestGCPOrchestrator_ListAddressRanges(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-list-range",
		AddressRangeCidr:  "10.2.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	_, err := store.CreateAddressRange(ctx, ar)
	require.NoError(t, err)

	results, err := orch.ListAddressRanges(ctx, "123", "vpc1", nil, nil)
	require.NoError(t, err)
	assert.Len(t, results, 1)
}

func TestGCPOrchestrator_UpdateAddressRange(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-update-range",
		AddressRangeCidr:  "10.3.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	created, err := store.CreateAddressRange(ctx, ar)
	require.NoError(t, err)

	created.ApplyRouteAggregation = true
	updated, err := orch.UpdateAddressRange(ctx, created)
	require.NoError(t, err)
	assert.True(t, updated.ApplyRouteAggregation)
}

func TestGCPOrchestrator_UpdateAddressRangeState(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-state-range",
		AddressRangeCidr:  "10.4.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	created, err := store.CreateAddressRange(ctx, ar)
	require.NoError(t, err)

	updated, err := orch.UpdateAddressRangeState(ctx, created.UUID, "IN_USE", nil)
	require.NoError(t, err)
	assert.Equal(t, "IN_USE", updated.AddressRangeState)
}

func TestGCPOrchestrator_DeleteAddressRange(t *testing.T) {
	_, store, orch, _ := setup(t)
	ctx := arTestCtx()

	ar := &datamodel.AddressRange{
		Name:              "test-delete-range",
		AddressRangeCidr:  "10.5.0.0/24",
		Network:           "projects/123/global/networks/vpc1",
		VpcName:           "vpc1",
		HostProjectNumber: "123",
		LifType:           "dataLIF",
	}
	created, err := store.CreateAddressRange(ctx, ar)
	require.NoError(t, err)

	deleted, err := orch.DeleteAddressRange(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, "DELETED", deleted.AddressRangeState)
}

// resolveRequestedRanges tests

func TestResolveRequestedRanges_Disabled(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result := resolveRequestedRanges(ctx, store, logger, "projects/123/global/networks/vpc1", false)
	assert.Nil(t, result)
}

func TestResolveRequestedRanges_EmptyVendorSubNetID(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result := resolveRequestedRanges(ctx, store, logger, "", true)
	assert.Nil(t, result)
}

func TestResolveRequestedRanges_InvalidVendorSubNetID(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	// Does not match the expected format — ParseProjectId returns empty strings.
	result := resolveRequestedRanges(ctx, store, logger, "invalid-id", true)
	assert.Nil(t, result)
}

func TestResolveRequestedRanges_ListError_ReturnsNil(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	mockStore := database.NewMockStorage(t)
	lifType := database.AddressRangeLifTypeDataLIF
	mockStore.On("ListAddressRanges", ctx, "123", "vpc1", (*string)(nil), &lifType).
		Return(nil, errors.New("db error"))

	result := resolveRequestedRanges(ctx, mockStore, logger, "projects/123/global/networks/vpc1", true)
	assert.Nil(t, result)
	mockStore.AssertExpectations(t)
}

func TestResolveRequestedRanges_ReturnsCreatedAndInUseNames(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	// Seed address ranges with different states.
	ranges := []*datamodel.AddressRange{
		{Name: "range-created", AddressRangeCidr: "10.0.0.0/24", Network: "n", VpcName: "vpc1", HostProjectNumber: "123", LifType: "dataLIF"},
		{Name: "range-inuse", AddressRangeCidr: "10.1.0.0/24", Network: "n", VpcName: "vpc1", HostProjectNumber: "123", LifType: "dataLIF"},
		{Name: "range-disabled", AddressRangeCidr: "10.2.0.0/24", Network: "n", VpcName: "vpc1", HostProjectNumber: "123", LifType: "dataLIF"},
	}
	for _, r := range ranges {
		_, err := store.CreateAddressRange(ctx, r)
		require.NoError(t, err)
	}
	// Set second range to IN_USE, third to DISABLED.
	all, err := store.ListAddressRanges(ctx, "123", "vpc1", nil, nil)
	require.NoError(t, err)
	for _, ar := range all {
		switch ar.Name {
		case "range-inuse":
			_, err = store.UpdateAddressRangeState(ctx, ar.UUID, database.AddressRangeStateInUse, nil)
			require.NoError(t, err)
		case "range-disabled":
			ar.AddressRangeState = database.AddressRangeStateDisabled
			_, err = store.UpdateAddressRange(ctx, ar)
			require.NoError(t, err)
		}
	}

	result := resolveRequestedRanges(ctx, store, logger, "projects/123/global/networks/vpc1", true)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "range-created")
	assert.Contains(t, result, "range-inuse")
}

func TestResolveRequestedRanges_EmptyRanges_ReturnsNil(t *testing.T) {
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result := resolveRequestedRanges(ctx, store, logger, "projects/123/global/networks/vpc-empty", true)
	assert.Nil(t, result)
}

func TestResolveSecondaryIpRange_MultipleInterclusterParents(t *testing.T) {
	t.Setenv(env.EnvIcLifAddressSpaceEnabled, "true")
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	ranges := []*datamodel.AddressRange{
		{Name: "ic-a", AddressRangeCidr: "10.44.0.0/16", Network: "n", VpcName: "vpc1", HostProjectNumber: "123", LifType: database.AddressRangeLifTypeInterclusterLIF},
		{Name: "ic-b", AddressRangeCidr: "10.45.0.0/16", Network: "n", VpcName: "vpc1", HostProjectNumber: "123", LifType: database.AddressRangeLifTypeInterclusterLIF},
	}
	for _, r := range ranges {
		_, err := store.CreateAddressRange(ctx, r)
		require.NoError(t, err)
	}

	result, err := resolveSecondaryIpRange(ctx, store, logger, "projects/123/global/networks/vpc1", nil)
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Contains(t, result, "10.44.0.0/16")
	assert.Contains(t, result, "10.45.0.0/16")
}

func TestResolveSecondaryIpRange_Disabled(t *testing.T) {
	t.Setenv(env.EnvIcLifAddressSpaceEnabled, "false")
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result, err := resolveSecondaryIpRange(ctx, store, logger, "projects/123/global/networks/vpc1", []string{"keep-me"})
	require.NoError(t, err)
	assert.Equal(t, []string{"keep-me"}, result)
}

// The next three cases pin the fail-closed contract: while the flag is on, the pool create must
// not silently continue on the legacy IC path when the parent range cannot be resolved.

func TestResolveSecondaryIpRange_UnparseableNetwork_Fails(t *testing.T) {
	t.Setenv(env.EnvIcLifAddressSpaceEnabled, "true")
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result, err := resolveSecondaryIpRange(ctx, store, logger, "not-a-network", nil)
	require.Error(t, err)
	assert.Nil(t, result)

	var customErr *coreerrors.CustomError
	require.True(t, coreerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(coreerrors.ErrInputValidationError))
}

func TestResolveSecondaryIpRange_ListError_Fails(t *testing.T) {
	t.Setenv(env.EnvIcLifAddressSpaceEnabled, "true")
	ctx := arTestCtx()
	logger := log.NewLogger()
	mockStore := database.NewMockStorage(t)

	lifType := database.AddressRangeLifTypeInterclusterLIF
	mockStore.On("ListAddressRanges", ctx, "123", "vpc1", (*string)(nil), &lifType).
		Return(nil, errors.New("db error"))

	result, err := resolveSecondaryIpRange(ctx, mockStore, logger, "projects/123/global/networks/vpc1", nil)
	require.Error(t, err)
	assert.Nil(t, result)

	var customErr *coreerrors.CustomError
	require.True(t, coreerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(coreerrors.ErrDatabaseDataReadError))
	mockStore.AssertExpectations(t)
}

func TestResolveSecondaryIpRange_NoActiveParent_KeepsLegacyPath(t *testing.T) {
	// An empty result is a legitimate "no IC parent registered for this VPC" state, so it stays a
	// fallback rather than a create failure.
	t.Setenv(env.EnvIcLifAddressSpaceEnabled, "true")
	ctx := arTestCtx()
	logger := log.NewLogger()
	_, store, _, _ := setup(t)

	result, err := resolveSecondaryIpRange(ctx, store, logger, "projects/123/global/networks/vpc-none", nil)
	require.NoError(t, err)
	assert.Empty(t, result)
}

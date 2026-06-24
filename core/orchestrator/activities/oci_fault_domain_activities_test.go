package activities_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

func TestSelectFaultDomains_AutoSelectsFromEmptyDB(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(&act)

	// DB has no nodes yet — all zones are unconstrained.
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(map[string]map[string]int64{}, nil)

	req := activities.SelectFaultDomainsRequest{
		PrimaryZone:   "us-ashburn-1-ad-1",
		SecondaryZone: "us-ashburn-1-ad-2",
		MediatorZone:  "us-ashburn-1-ad-3",
	}
	val, err := env.ExecuteActivity(act.SelectFaultDomains, req)
	require.NoError(t, err)

	var result activities.SelectFaultDomainsResult
	require.NoError(t, val.Get(&result))

	// With no existing load all three legs are auto-selected (non-empty).
	assert.NotEmpty(t, result.PrimaryFaultDomain)
	assert.NotEmpty(t, result.SecondaryFaultDomain)
	assert.NotEmpty(t, result.MediatorFaultDomain)
}

func TestSelectFaultDomains_PicksLeastLoadedFaultDomain(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(&act)

	// FD-1 is overloaded in ad-1; FD-2 and FD-3 have fewer nodes.
	// The selector must prefer FD-2 (fewest nodes in ad-1).
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(map[string]map[string]int64{
			"us-ashburn-1-ad-1": {
				"FAULT-DOMAIN-1": 5,
				"FAULT-DOMAIN-2": 1,
				"FAULT-DOMAIN-3": 3,
			},
		}, nil)

	req := activities.SelectFaultDomainsRequest{
		PrimaryZone: "us-ashburn-1-ad-1",
	}
	val, err := env.ExecuteActivity(act.SelectFaultDomains, req)
	require.NoError(t, err)

	var result activities.SelectFaultDomainsResult
	require.NoError(t, val.Get(&result))

	assert.Equal(t, "FAULT-DOMAIN-2", result.PrimaryFaultDomain,
		"should pick the least-loaded fault domain")
}

func TestSelectFaultDomains_DBError_ReturnsError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(&act)

	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(nil, errors.New("db connection refused"))

	req := activities.SelectFaultDomainsRequest{
		PrimaryZone: "us-ashburn-1-ad-1",
	}
	_, err := env.ExecuteActivity(act.SelectFaultDomains, req)
	assert.Error(t, err)
}

func TestSelectFaultDomains_CallerOverrides_SkipsAutoPick(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(&act)

	// DB returns existing counts but caller-provided values must be preserved.
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(map[string]map[string]int64{
			"us-ashburn-1-ad-1": {"FAULT-DOMAIN-1": 10},
		}, nil)

	req := activities.SelectFaultDomainsRequest{
		PrimaryZone:          "us-ashburn-1-ad-1",
		PrimaryFaultDomain:   "FAULT-DOMAIN-3",
		SecondaryFaultDomain: "FAULT-DOMAIN-2",
		MediatorFaultDomain:  "FAULT-DOMAIN-1",
	}
	val, err := env.ExecuteActivity(act.SelectFaultDomains, req)
	require.NoError(t, err)

	var result activities.SelectFaultDomainsResult
	require.NoError(t, val.Get(&result))

	assert.Equal(t, "FAULT-DOMAIN-3", result.PrimaryFaultDomain, "caller override must be preserved")
	assert.Equal(t, "FAULT-DOMAIN-2", result.SecondaryFaultDomain, "caller override must be preserved")
	assert.Equal(t, "FAULT-DOMAIN-1", result.MediatorFaultDomain, "caller override must be preserved")
}

func TestSelectFaultDomains_MultipleZones_IndependentPicks(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(&act)

	// All three fault domains are listed for each zone so pickLeastLoaded
	// produces a deterministic result without relying on absent-means-zero.
	// ad-1: FD-2 has the lowest count → primary picks FD-2.
	// ad-2: FD-1 has the lowest count → secondary picks FD-1.
	mockStorage.On("GetFaultDomainCountsByZone", mock.Anything).
		Return(map[string]map[string]int64{
			"us-ashburn-1-ad-1": {
				"FAULT-DOMAIN-1": 10,
				"FAULT-DOMAIN-2": 1,
				"FAULT-DOMAIN-3": 5,
			},
			"us-ashburn-1-ad-2": {
				"FAULT-DOMAIN-1": 1,
				"FAULT-DOMAIN-2": 10,
				"FAULT-DOMAIN-3": 5,
			},
		}, nil)

	req := activities.SelectFaultDomainsRequest{
		PrimaryZone:   "us-ashburn-1-ad-1",
		SecondaryZone: "us-ashburn-1-ad-2",
	}
	val, err := env.ExecuteActivity(act.SelectFaultDomains, req)
	require.NoError(t, err)

	var result activities.SelectFaultDomainsResult
	require.NoError(t, val.Get(&result))

	assert.Equal(t, "FAULT-DOMAIN-2", result.PrimaryFaultDomain,
		"primary should pick least loaded in ad-1")
	assert.Equal(t, "FAULT-DOMAIN-1", result.SecondaryFaultDomain,
		"secondary should pick least loaded in ad-2")
}

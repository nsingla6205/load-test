package activities_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

// newPOICLifActivityWithClient wires a POICLifActivity with an injected
// ClusterClient factory (mirrors newPOActivityWithClient in the sibling test
// file). The optional storage lets callers assert DB calls when the activity
// under test doesn't need the cluster client.
func newPOICLifActivityWithClient(client activities.POClusterClient, se database.Storage) *activities.POICLifActivity {
	return &activities.POICLifActivity{
		SE: se,
		ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
			return client, nil
		},
	}
}

// runActivity is a thin helper that registers and executes a Temporal activity
// so tests can assert on both the returned value and the wrapped error path
// (Temporal wraps returned errors as ApplicationError).
func runActivity(t *testing.T, act interface{}, args ...interface{}) (converter.EncodedValue, error) {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(act)
	return env.ExecuteActivity(act, args...)
}

func TestPOICLifPrecheck_RejectsInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		in   activities.POICLifPrecheckInput
	}{
		{"zero account", activities.POICLifPrecheckInput{AccountID: 0, HostUUID: poTestHostUUID}},
		{"negative account", activities.POICLifPrecheckInput{AccountID: -1, HostUUID: poTestHostUUID}},
		{"empty host", activities.POICLifPrecheckInput{AccountID: 1, HostUUID: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			act := &activities.POICLifActivity{SE: database.NewMockStorage(t)}
			_, err := runActivity(t, act.POICLifPrecheck, tc.in)
			require.Error(t, err)
		})
	}
}

func TestPOICLifPrecheck_RequiresStorage(t *testing.T) {
	act := &activities.POICLifActivity{}
	_, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: 1, HostUUID: poTestHostUUID,
	})
	require.Error(t, err)
}

func TestPOICLifPrecheck_PropagatesGetHostError(t *testing.T) {
	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(nil, errors.New("db off"))

	act := &activities.POICLifActivity{SE: se}
	_, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: 1, HostUUID: poTestHostUUID,
	})
	require.Error(t, err)
}

func TestPOICLifPrecheck_ReportsCompleteness(t *testing.T) {
	const accountID = int64(11)
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 22, UUID: poTestHostUUID}}
	nodeNames := []string{"node-1", "node-2"}
	nodeIDs := map[string]int64{"node-1": 100, "node-2": 200}
	existing := []*datamodel.Lif{
		{BaseModel: datamodel.BaseModel{UUID: "lif-1"}, NodeID: 100, IPAddress: "10.0.0.10"},
		{BaseModel: datamodel.BaseModel{UUID: "lif-2"}, NodeID: 200, IPAddress: "10.0.0.11"},
	}

	client := &mockPOClusterClient{}
	client.On("GetNodeNames").Return(nodeNames, nil).Once()

	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(host, nil).Once()
	se.EXPECT().GetNodesByHostIDAndNames(mock.Anything, host.ID, nodeNames).Return(nodeIDs, nil).Once()
	se.EXPECT().GetInterclusterLifsForAccountHost(mock.Anything, accountID, host.ID).Return(existing, nil).Once()

	act := newPOICLifActivityWithClient(client, se)
	val, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: accountID, HostUUID: poTestHostUUID,
	})
	require.NoError(t, err)

	var out activities.POICLifPrecheckOutput
	require.NoError(t, val.Get(&out))
	assert.True(t, out.Complete)
}

func TestPOICLifPrecheck_IncompleteReturnsFalse(t *testing.T) {
	const accountID = int64(11)
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 22, UUID: poTestHostUUID}}
	nodeIDs := map[string]int64{"node-1": 100, "node-2": 200}

	client := &mockPOClusterClient{}
	client.On("GetNodeNames").Return([]string{"node-1", "node-2"}, nil).Once()

	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(host, nil).Once()
	se.EXPECT().GetNodesByHostIDAndNames(mock.Anything, host.ID, []string{"node-1", "node-2"}).Return(nodeIDs, nil).Once()
	// Only one IC LIF row present — set is incomplete.
	se.EXPECT().GetInterclusterLifsForAccountHost(mock.Anything, accountID, host.ID).Return([]*datamodel.Lif{
		{BaseModel: datamodel.BaseModel{UUID: "lif-1"}, NodeID: 100, IPAddress: "10.0.0.10"},
	}, nil).Once()

	act := newPOICLifActivityWithClient(client, se)
	val, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: accountID, HostUUID: poTestHostUUID,
	})
	require.NoError(t, err)

	var out activities.POICLifPrecheckOutput
	require.NoError(t, val.Get(&out))
	assert.False(t, out.Complete)
}

func TestPOICLifPrecheck_EmptyNodesIsError(t *testing.T) {
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 22, UUID: poTestHostUUID}}

	client := &mockPOClusterClient{}
	client.On("GetNodeNames").Return([]string{}, nil).Once()

	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(host, nil).Once()

	act := newPOICLifActivityWithClient(client, se)
	_, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: 1, HostUUID: poTestHostUUID,
	})
	require.Error(t, err)
}

func TestPOICLifPrecheck_PropagatesGetNodesError(t *testing.T) {
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 22, UUID: poTestHostUUID}}

	client := &mockPOClusterClient{}
	client.On("GetNodeNames").Return([]string{"node-1"}, nil).Once()

	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(host, nil).Once()
	se.EXPECT().GetNodesByHostIDAndNames(mock.Anything, host.ID, []string{"node-1"}).
		Return(nil, errors.New("db reads")).Once()

	act := newPOICLifActivityWithClient(client, se)
	_, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: 1, HostUUID: poTestHostUUID,
	})
	require.Error(t, err)
}

func TestPOICLifPrecheck_PropagatesGetInterclusterLifsError(t *testing.T) {
	host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 22, UUID: poTestHostUUID}}
	nodeIDs := map[string]int64{"node-1": 100}

	client := &mockPOClusterClient{}
	client.On("GetNodeNames").Return([]string{"node-1"}, nil).Once()

	se := database.NewMockStorage(t)
	se.EXPECT().GetHost(mock.Anything, poTestHostUUID).Return(host, nil).Once()
	se.EXPECT().GetNodesByHostIDAndNames(mock.Anything, host.ID, []string{"node-1"}).Return(nodeIDs, nil).Once()
	se.EXPECT().GetInterclusterLifsForAccountHost(mock.Anything, int64(1), host.ID).
		Return(nil, errors.New("lifs off")).Once()

	act := newPOICLifActivityWithClient(client, se)
	_, err := runActivity(t, act.POICLifPrecheck, activities.POICLifPrecheckInput{
		AccountID: 1, HostUUID: poTestHostUUID,
	})
	require.Error(t, err)
}

func TestAllocatePOInterclusterLifIPs(t *testing.T) {
	const accountID = int64(11)
	const rangeUUID = "range-uuid-1"

	t.Run("PropagatesRepoError", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().AllocateInterclusterLifIPs(mock.Anything, rangeUUID,
			mock.MatchedBy(func(lifs []*datamodel.Lif) bool { return len(lifs) == 1 })).
			Return(nil, errors.New("no capacity")).Once()

		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.AllocatePOInterclusterLifIPs, activities.AllocatePOInterclusterLifIPsInput{
			IPRangeUUID:  rangeUUID,
			AccountID:    accountID,
			Reservations: []activities.POICLifReservation{{Name: "ic-lif-1", NodeID: 100}},
		})
		require.Error(t, err)
	})

	t.Run("MapsRepoRowsToAllocatedAndFiltersNil", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().AllocateInterclusterLifIPs(mock.Anything, rangeUUID, mock.MatchedBy(func(lifs []*datamodel.Lif) bool {
			// Contract: activity forwards Name+AccountID+NodeID per reservation.
			return len(lifs) == 2 &&
				lifs[0].Name == "ic-lif-1" && lifs[0].AccountID == accountID && lifs[0].NodeID == 100 &&
				lifs[1].Name == "ic-lif-2" && lifs[1].AccountID == accountID && lifs[1].NodeID == 200
		})).Return([]*datamodel.Lif{
			{BaseModel: datamodel.BaseModel{UUID: "lif-1"}, Name: "ic-lif-1", IPAddress: "10.0.0.10", SubnetMask: "255.255.255.0", NodeID: 100},
			nil,
			{BaseModel: datamodel.BaseModel{UUID: "lif-2"}, Name: "ic-lif-2", IPAddress: "10.0.0.11", SubnetMask: "255.255.255.0", NodeID: 200},
		}, nil).Once()

		act := &activities.POICLifActivity{SE: se}
		val, err := runActivity(t, act.AllocatePOInterclusterLifIPs, activities.AllocatePOInterclusterLifIPsInput{
			IPRangeUUID: rangeUUID,
			AccountID:   accountID,
			Reservations: []activities.POICLifReservation{
				{Name: "ic-lif-1", NodeID: 100},
				{Name: "ic-lif-2", NodeID: 200},
			},
		})
		require.NoError(t, err)

		var out []*activities.POICLifAllocated
		require.NoError(t, val.Get(&out))
		require.Len(t, out, 2, "nil rows from the repo must be filtered out")
		assert.Equal(t, "lif-1", out[0].UUID)
		assert.Equal(t, "10.0.0.10", out[0].IPAddress)
		assert.Equal(t, int64(100), out[0].NodeID)
		assert.Equal(t, "lif-2", out[1].UUID)
	})

	t.Run("RequiresStorage", func(t *testing.T) {
		act := &activities.POICLifActivity{}
		_, err := runActivity(t, act.AllocatePOInterclusterLifIPs, activities.AllocatePOInterclusterLifIPsInput{
			IPRangeUUID: rangeUUID, AccountID: accountID,
			Reservations: []activities.POICLifReservation{{Name: "x", NodeID: 1}},
		})
		require.Error(t, err)
	})

	// Input-validation contract lives at the activity layer (see
	// validatePOICLifReservations); the DB layer trusts these.
	t.Run("RejectsEmptyReservations", func(t *testing.T) {
		se := database.NewMockStorage(t)
		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.AllocatePOInterclusterLifIPs, activities.AllocatePOInterclusterLifIPsInput{
			IPRangeUUID: rangeUUID, AccountID: accountID,
		})
		require.Error(t, err)
	})

	t.Run("RejectsInvalidReservationFields", func(t *testing.T) {
		cases := []struct {
			name        string
			accountID   int64
			reservation activities.POICLifReservation
		}{
			{"empty name", accountID, activities.POICLifReservation{Name: " ", NodeID: 1}},
			{"zero account", 0, activities.POICLifReservation{Name: "x", NodeID: 1}},
			{"zero node", accountID, activities.POICLifReservation{Name: "x", NodeID: 0}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				se := database.NewMockStorage(t)
				act := &activities.POICLifActivity{SE: se}
				_, err := runActivity(t, act.AllocatePOInterclusterLifIPs, activities.AllocatePOInterclusterLifIPsInput{
					IPRangeUUID:  rangeUUID,
					AccountID:    c.accountID,
					Reservations: []activities.POICLifReservation{c.reservation},
				})
				require.Error(t, err)
			})
		}
	})
}

func TestUpdatePOInterclusterLifExternalUUID(t *testing.T) {
	t.Run("RequiresStorage", func(t *testing.T) {
		act := &activities.POICLifActivity{}
		_, err := runActivity(t, act.UpdatePOInterclusterLifExternalUUID,
			activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "l", ExternalUUID: "e"})
		require.Error(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().UpdateInterclusterLifExternalUUID(mock.Anything, "lif-1", "ext-1").Return(nil).Once()

		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.UpdatePOInterclusterLifExternalUUID,
			activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "lif-1", ExternalUUID: "ext-1"})
		require.NoError(t, err)
	})

	t.Run("PropagatesRepoError", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().UpdateInterclusterLifExternalUUID(mock.Anything, "lif-1", "ext-1").
			Return(errors.New("update failed")).Once()

		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.UpdatePOInterclusterLifExternalUUID,
			activities.UpdatePOInterclusterLifExternalUUIDInput{LifUUID: "lif-1", ExternalUUID: "ext-1"})
		require.Error(t, err)
	})
}

func TestGetPOInterclusterLifsForAccountHost(t *testing.T) {
	t.Run("RequiresStorage", func(t *testing.T) {
		act := &activities.POICLifActivity{}
		_, err := runActivity(t, act.GetPOInterclusterLifsForAccountHost,
			activities.GetPOInterclusterLifsForAccountHostInput{AccountID: 1, HostID: 2})
		require.Error(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		expected := []*datamodel.Lif{
			{BaseModel: datamodel.BaseModel{UUID: "lif-1"}, Name: "ic-1"},
		}
		se := database.NewMockStorage(t)
		se.EXPECT().GetInterclusterLifsForAccountHost(mock.Anything, int64(1), int64(2)).Return(expected, nil).Once()

		act := &activities.POICLifActivity{SE: se}
		val, err := runActivity(t, act.GetPOInterclusterLifsForAccountHost,
			activities.GetPOInterclusterLifsForAccountHostInput{AccountID: 1, HostID: 2})
		require.NoError(t, err)
		var got []*datamodel.Lif
		require.NoError(t, val.Get(&got))
		require.Len(t, got, 1)
		assert.Equal(t, "lif-1", got[0].UUID)
	})

	t.Run("PropagatesRepoError", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().GetInterclusterLifsForAccountHost(mock.Anything, int64(1), int64(2)).
			Return(nil, errors.New("boom")).Once()

		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.GetPOInterclusterLifsForAccountHost,
			activities.GetPOInterclusterLifsForAccountHostInput{AccountID: 1, HostID: 2})
		require.Error(t, err)
	})
}

func TestResolveHostID(t *testing.T) {
	t.Run("RequiresStorage", func(t *testing.T) {
		act := &activities.POICLifActivity{}
		_, err := runActivity(t, act.ResolveHostID, "host-uuid-1")
		require.Error(t, err)
	})

	t.Run("Success", func(t *testing.T) {
		host := &datamodel.Host{BaseModel: datamodel.BaseModel{ID: 77, UUID: "host-uuid-1"}}
		se := database.NewMockStorage(t)
		se.EXPECT().GetHost(mock.Anything, "host-uuid-1").Return(host, nil).Once()

		act := &activities.POICLifActivity{SE: se}
		val, err := runActivity(t, act.ResolveHostID, "host-uuid-1")
		require.NoError(t, err)
		var got int64
		require.NoError(t, val.Get(&got))
		assert.Equal(t, int64(77), got)
	})

	t.Run("PropagatesRepoError", func(t *testing.T) {
		se := database.NewMockStorage(t)
		se.EXPECT().GetHost(mock.Anything, "host-uuid-1").Return(nil, errors.New("not found")).Once()

		act := &activities.POICLifActivity{SE: se}
		_, err := runActivity(t, act.ResolveHostID, "host-uuid-1")
		require.Error(t, err)
	})
}

func TestCreatePOInterclusterLif_RejectsEmptyName(t *testing.T) {
	client := &mockPOClusterClient{}
	outerAct := &activities.POOntapActivity{
		ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) { return client, nil },
	}
	_, err := runActivity(t, outerAct.CreatePOInterclusterLif, activities.CreatePOInterclusterLifInput{
		HostUUID: poTestHostUUID,
		Lif: activities.POInterclusterLifSpec{
			IPSpace: "ipspace_acct_100",
		},
	})
	require.Error(t, err)
	client.AssertNotCalled(t, "CreateInterclusterLif", mock.Anything)
}

func TestCreatePOInterclusterLif_PropagatesClientError(t *testing.T) {
	client := &mockPOClusterClient{}
	client.On("CreateInterclusterLif", mock.Anything).Return(nil, errors.New("ontap 500")).Once()

	outerAct := &activities.POOntapActivity{
		ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) { return client, nil },
	}
	_, err := runActivity(t, outerAct.CreatePOInterclusterLif, activities.CreatePOInterclusterLifInput{
		HostUUID: poTestHostUUID,
		Lif: activities.POInterclusterLifSpec{
			Name:    "ic-lif-1",
			IPSpace: "ipspace_acct_100",
		},
	})
	require.Error(t, err)
}

// silenceUnusedImport keeps go vet quiet if the mockPOClusterClient field
// aliasing shifts.
var _ = vsa.CreateNetworkIPRouteParams{}

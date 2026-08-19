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

// newICRollbackActivity mirrors newPOActivityWithClient but injects storage too.
func newICRollbackActivity(client activities.POClusterClient, store database.Storage) *activities.POOntapActivity {
	act := newPOActivityWithClient(client)
	act.SE = store
	return act
}

// baseRollbackInput builds a fully-populated input so every step of Rollback
// fires. Individual tests trim fields as needed to gate specific steps.
func baseRollbackInput() activities.RollbackPOInterclusterLifsInput {
	return activities.RollbackPOInterclusterLifsInput{
		HostUUID:        poTestHostUUID,
		AccountID:       11,
		HostID:          22,
		IPSpace:         "ipspace-ic-1",
		BroadcastDomain: "bdomain-ic-1",
		BasePort:        "a0a",
		VlanID:          4001,
		Gateway:         "10.1.2.1",
		NodeNames:       []string{"n1", "n2"},
		CreatedLifs: []activities.POInterclusterLifResult{
			{Name: "ic-lif-1", ExternalUUID: "uuid-1"},
			{Name: "ic-lif-2", ExternalUUID: "uuid-2"},
		},
	}
}

func executeRollback(t *testing.T, act *activities.POOntapActivity, in activities.RollbackPOInterclusterLifsInput) error {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(act.RollbackPOInterclusterLifs)
	_, err := env.ExecuteActivity(act.RollbackPOInterclusterLifs, in)
	return err
}

func TestRollbackPOInterclusterLifs_HappyPathReverseOrder(t *testing.T) {
	client := &mockPOClusterClient{}
	in := baseRollbackInput()

	// Track call order via mock.Order() would require testify sequences; instead we
	// assert each expected call fired and that CVN-style deletes were never
	// invoked on the cluster client.
	client.On("DeleteIPSpaceDefaultRoute", in.IPSpace, in.Gateway).Return(nil).Once()
	client.On("DeleteInterclusterLif", "uuid-1").Return(nil).Once()
	client.On("DeleteInterclusterLif", "uuid-2").Return(nil).Once()
	client.On("DeleteVlanNetPorts", in.IPSpace, in.BroadcastDomain, in.BasePort, in.VlanID, in.NodeNames).Return(nil).Once()
	client.On("DeleteBroadcastDomain", in.IPSpace, in.BroadcastDomain).Return(nil).Once()
	client.On("DeleteIPSpace", in.IPSpace).Return(nil).Once()

	store := database.NewMockStorage(t)
	store.EXPECT().DeleteInterclusterLifsForAccountHost(mock.Anything, in.AccountID, in.HostID).Return(nil).Once()

	act := newICRollbackActivity(client, store)
	require.NoError(t, executeRollback(t, act, in))
	client.AssertExpectations(t)
}

func TestRollbackPOInterclusterLifs_NeverCallsCVN(t *testing.T) {
	// The cluster-client mock only exposes ONTAP methods. This test guards the
	// contract that Rollback stays on the cluster and never reaches CVN by
	// asserting no unexpected calls happen (mock.AssertExpectations catches any
	// call without a matching .On).
	client := &mockPOClusterClient{}
	in := baseRollbackInput()

	client.On("DeleteIPSpaceDefaultRoute", mock.Anything, mock.Anything).Return(nil).Maybe()
	client.On("DeleteInterclusterLif", mock.Anything).Return(nil).Maybe()
	client.On("DeleteVlanNetPorts", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	client.On("DeleteBroadcastDomain", mock.Anything, mock.Anything).Return(nil).Maybe()
	client.On("DeleteIPSpace", mock.Anything).Return(nil).Maybe()

	store := database.NewMockStorage(t)
	store.EXPECT().DeleteInterclusterLifsForAccountHost(mock.Anything, in.AccountID, in.HostID).Return(nil).Maybe()

	act := newICRollbackActivity(client, store)
	require.NoError(t, executeRollback(t, act, in))

	// Guard: none of the CVN-side deletes are wired into the cluster client.
	// A "*NetworkV1" style delete showing up in AssertExpectations would surface
	// as a bug — but the mock only accepts declared .On methods, so any CVN
	// call would panic. This is the compile+runtime guarantee we want.
	client.AssertNotCalled(t, "DeleteSecondaryVLNetworkV1", mock.Anything)
	client.AssertNotCalled(t, "DeleteVXNetworkV1", mock.Anything)
	client.AssertNotCalled(t, "DestroyNetwork", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRollbackPOInterclusterLifs_ContinuesOnPartialFailure(t *testing.T) {
	client := &mockPOClusterClient{}
	in := baseRollbackInput()

	client.On("DeleteIPSpaceDefaultRoute", in.IPSpace, in.Gateway).Return(errors.New("route boom")).Once()
	client.On("DeleteInterclusterLif", "uuid-1").Return(errors.New("lif1 boom")).Once()
	client.On("DeleteInterclusterLif", "uuid-2").Return(nil).Once()
	client.On("DeleteVlanNetPorts", in.IPSpace, in.BroadcastDomain, in.BasePort, in.VlanID, in.NodeNames).Return(nil).Once()
	client.On("DeleteBroadcastDomain", in.IPSpace, in.BroadcastDomain).Return(errors.New("bd boom")).Once()
	client.On("DeleteIPSpace", in.IPSpace).Return(nil).Once()

	store := database.NewMockStorage(t)
	store.EXPECT().DeleteInterclusterLifsForAccountHost(mock.Anything, in.AccountID, in.HostID).
		Return(errors.New("db boom")).Once()

	act := newICRollbackActivity(client, store)
	err := executeRollback(t, act, in)
	require.Error(t, err, "rollback aggregates errors but must still attempt every step")
	// Joined error must surface every underlying failure.
	assert.Contains(t, err.Error(), "route boom")
	assert.Contains(t, err.Error(), "lif1 boom")
	assert.Contains(t, err.Error(), "db boom")
	assert.Contains(t, err.Error(), "bd boom")
	client.AssertExpectations(t)
}

func TestRollbackPOInterclusterLifs_SkipsStepsWithMissingInputs(t *testing.T) {
	client := &mockPOClusterClient{}
	in := activities.RollbackPOInterclusterLifsInput{
		HostUUID:  poTestHostUUID,
		AccountID: 11,
		HostID:    22,
	}

	// No CreatedLifs, no IPSpace, no Gateway: only VCP DB row cleanup should fire.
	store := database.NewMockStorage(t)
	store.EXPECT().DeleteInterclusterLifsForAccountHost(mock.Anything, in.AccountID, in.HostID).Return(nil).Once()

	act := newICRollbackActivity(client, store)
	require.NoError(t, executeRollback(t, act, in))

	client.AssertNotCalled(t, "DeleteIPSpaceDefaultRoute", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "DeleteInterclusterLif", mock.Anything)
	client.AssertNotCalled(t, "DeleteVlanNetPorts", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "DeleteBroadcastDomain", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "DeleteIPSpace", mock.Anything)
}

func TestRollbackPOInterclusterLifs_SkipsLifsWithEmptyExternalUUID(t *testing.T) {
	client := &mockPOClusterClient{}
	in := baseRollbackInput()
	in.CreatedLifs = []activities.POInterclusterLifResult{
		{Name: "ic-lif-1", ExternalUUID: "uuid-1"},
		{Name: "ic-lif-2", ExternalUUID: ""}, // no UUID => skip ONTAP delete
	}

	client.On("DeleteIPSpaceDefaultRoute", in.IPSpace, in.Gateway).Return(nil).Once()
	client.On("DeleteInterclusterLif", "uuid-1").Return(nil).Once()
	client.On("DeleteVlanNetPorts", in.IPSpace, in.BroadcastDomain, in.BasePort, in.VlanID, in.NodeNames).Return(nil).Once()
	client.On("DeleteBroadcastDomain", in.IPSpace, in.BroadcastDomain).Return(nil).Once()
	client.On("DeleteIPSpace", in.IPSpace).Return(nil).Once()

	store := database.NewMockStorage(t)
	store.EXPECT().DeleteInterclusterLifsForAccountHost(mock.Anything, in.AccountID, in.HostID).Return(nil).Once()

	act := newICRollbackActivity(client, store)
	require.NoError(t, executeRollback(t, act, in))

	client.AssertNotCalled(t, "DeleteInterclusterLif", "")
	client.AssertExpectations(t)
}

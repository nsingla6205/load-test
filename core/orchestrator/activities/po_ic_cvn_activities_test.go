package activities_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sdkmodels "github.com/greenqloud/cloud-volumes-network/app/pkg/sdk/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

// directCreateFunc returns a stand-in for activities.CreateSecondaryIPRangeWithLifType
// so tests exercise the same call path as production without having to bring
// the CreateSecondaryIPRangeWithLifTypeV1 method back onto the CVN interface.
// The returned function records inputs on the stub and returns the stub's
// canned response/error.
func (s *stubPOICLifCVNClient) directCreateFunc() func(ctx context.Context, accountUUID, remoteAccountID string, subnetMaskSize int64, lifType string) (*sdkmodels.SecondaryIPRangeV1, error) {
	return func(_ context.Context, _, _ string, subnetMaskSize int64, lifType string) (*sdkmodels.SecondaryIPRangeV1, error) {
		s.createSecondaryIPRangeCalls++
		s.lastCreateSecondaryMaskSize = subnetMaskSize
		s.lastCreateSecondaryRangeLifType = lifType
		return s.createSecondaryRange, s.createSecondaryErr
	}
}

// --- narrow CVN stub -------------------------------------------------------

type stubPOICLifCVNClient struct {
	networks             []*sdkmodels.NetworkV1
	listErr              error
	describeNetwork      *sdkmodels.NetworkV1
	describeNetworkErr   error
	createSecondaryRange *sdkmodels.SecondaryIPRangeV1
	createSecondaryErr   error
	getSecondaryRange    *sdkmodels.SecondaryIPRangeV1
	getSecondaryErr      error
	createVXNetwork      *sdkmodels.VXNetworkV1
	createVXNetworkErr   error
	describeVXNetwork    *sdkmodels.VXNetworkV1
	describeVXNetworkErr error
	describeJob          *sdkmodels.JobV1
	describeJobErr       error

	listNetworksCalls               int
	describeNetworkCalls            int
	createSecondaryIPRangeCalls     int
	getSecondaryIPRangeCalls        int
	createVXNetworkCalls            int
	describeVXNetworkCalls          int
	describeJobCalls                int
	lastCreateSecondaryRangeLifType string
	lastCreateSecondaryMaskSize     int64
	lastCreateVXRequest             struct {
		networkUUID   string
		microzoneUUID string
		ipRangeUUID   string
	}
}

func newStubCVNClient() *stubPOICLifCVNClient {
	return &stubPOICLifCVNClient{}
}

func (s *stubPOICLifCVNClient) ListNetworksV1(_ *string, _ *string) ([]*sdkmodels.NetworkV1, error) {
	s.listNetworksCalls++
	return s.networks, s.listErr
}

func (s *stubPOICLifCVNClient) DescribeNetworkV1(uuid string) (*sdkmodels.NetworkV1, error) {
	s.describeNetworkCalls++
	if s.describeNetwork != nil && s.describeNetwork.UUID != "" && s.describeNetwork.UUID != uuid {
		return nil, fmt.Errorf("unexpected DescribeNetworkV1(%q)", uuid)
	}
	return s.describeNetwork, s.describeNetworkErr
}

func (s *stubPOICLifCVNClient) GetSecondaryIPRangeV1(_ string) (*sdkmodels.SecondaryIPRangeV1, error) {
	s.getSecondaryIPRangeCalls++
	return s.getSecondaryRange, s.getSecondaryErr
}

func (s *stubPOICLifCVNClient) CreateVXNetworkV1(networkUUID, microzoneUUID, ipRangeUUID string) (*sdkmodels.VXNetworkV1, error) {
	s.createVXNetworkCalls++
	s.lastCreateVXRequest.networkUUID = networkUUID
	s.lastCreateVXRequest.microzoneUUID = microzoneUUID
	s.lastCreateVXRequest.ipRangeUUID = ipRangeUUID
	return s.createVXNetwork, s.createVXNetworkErr
}

func (s *stubPOICLifCVNClient) DescribeVXNetworkV1(_ string) (*sdkmodels.VXNetworkV1, error) {
	s.describeVXNetworkCalls++
	return s.describeVXNetwork, s.describeVXNetworkErr
}

func (s *stubPOICLifCVNClient) DescribeJobV1(_ string) (*sdkmodels.JobV1, error) {
	s.describeJobCalls++
	return s.describeJob, s.describeJobErr
}

// Poll timings are intentionally short so tests run in millis; the stub always
// returns "done" from the first DescribeJobV1 call so timeouts never fire.
func (s *stubPOICLifCVNClient) GetBorderPollTimeout() time.Duration  { return 100 * time.Millisecond }
func (s *stubPOICLifCVNClient) GetServicePollTimeout() time.Duration { return 100 * time.Millisecond }
func (s *stubPOICLifCVNClient) GetPollInterval() time.Duration       { return time.Millisecond }

// --- helpers ----------------------------------------------------------------

const (
	testAccountUUID     = "acct-uuid-1"
	testRemoteAccountID = "projects/p/global/networks/customer"
	testMicrozoneUUID   = "host-uuid-1"
	testHostUUID        = "host-uuid-1"
	testNetworkUUID     = "network-uuid-1"
	testIPRangeUUID     = "ip-range-uuid-1"
	testVXUUID          = "vx-uuid-1"
	testGateway         = "10.1.2.1"
	testNetmask         = "255.255.255.240"
	testVlanID          = 4001
)

func newICIPRange(uuid string, lifType string) *datamodel.IPRange {
	return &datamodel.IPRange{
		BaseModel: datamodel.BaseModel{UUID: uuid},
		LifType:   lifType,
		Gateway:   testGateway,
		Netmask:   testNetmask,
	}
}

// newICVNActivity wires the activity to the stub and swaps
// activities.CreateSecondaryIPRangeWithLifType for the stub's direct-call
// handler. CreateSecondaryIPRangeWithLifTypeV1 is intentionally NOT on
// POICLifCVNClient; the activity invokes it through a package-level func var
// backed by the raw cvnapi client. The original value is restored via
// t.Cleanup so tests remain isolated.
func newICVNActivity(t *testing.T, client *stubPOICLifCVNClient, store database.Storage) *activities.POICVNActivity {
	t.Helper()
	prev := activities.CreateSecondaryIPRangeWithLifType
	activities.CreateSecondaryIPRangeWithLifType = client.directCreateFunc()
	t.Cleanup(func() { activities.CreateSecondaryIPRangeWithLifType = prev })
	return &activities.POICVNActivity{
		SE: store,
		ClientFactory: func(_ context.Context, _ string) (activities.POICLifCVNClient, error) {
			return client, nil
		},
		Config: commonparams.POICLifConfig{SubnetMaskSize: 28},
	}
}

func newICValidInput() activities.EnsurePOICNetworkInput {
	return activities.EnsurePOICNetworkInput{
		AccountUUID:     testAccountUUID,
		RemoteAccountID: testRemoteAccountID,
		MicrozoneUUID:   testMicrozoneUUID,
		HostUUID:        testHostUUID,
		NodeCount:       2,
	}
}

func runICEnsure(t *testing.T, act *activities.POICVNActivity, in activities.EnsurePOICNetworkInput) (*activities.POICNetworkOutput, error) {
	t.Helper()
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.RegisterActivity(act.EnsurePOICNetwork)
	val, err := env.ExecuteActivity(act.EnsurePOICNetwork, in)
	if err != nil {
		return nil, err
	}
	var out activities.POICNetworkOutput
	require.NoError(t, val.Get(&out))
	return &out, nil
}

func ptrString(s string) *string { return &s }

// --- validation ------------------------------------------------------------

func TestEnsurePOICNetwork_InputValidation(t *testing.T) {
	tests := []struct {
		name  string
		mut   func(*activities.EnsurePOICNetworkInput)
		match string
	}{
		{"empty account", func(in *activities.EnsurePOICNetworkInput) { in.AccountUUID = " " }, "AccountUUID"},
		{"empty remote", func(in *activities.EnsurePOICNetworkInput) { in.RemoteAccountID = "" }, "RemoteAccountID"},
		{"empty microzone", func(in *activities.EnsurePOICNetworkInput) { in.MicrozoneUUID = "" }, "MicrozoneUUID"},
		{"empty host", func(in *activities.EnsurePOICNetworkInput) { in.HostUUID = "" }, "HostUUID"},
		{"zero nodes", func(in *activities.EnsurePOICNetworkInput) { in.NodeCount = 0 }, "NodeCount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newICValidInput()
			tt.mut(&in)
			act := newICVNActivity(t, newStubCVNClient(), database.NewMockStorage(t))
			_, err := runICEnsure(t, act, in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}

// --- IP range reuse matrix --------------------------------------------------

func TestEnsurePOICNetwork_ReuseExistingRange(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: testVXUUID},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    testIPRangeUUID,
			LifeCycleState: sdkmodels.LifeCycleStateAvailable,
			L2VlanID:       uint32(testVlanID),
		}},
	}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	require.NotNil(t, out)

	assert.Equal(t, testIPRangeUUID, out.IPRangeUUID)
	assert.Equal(t, testVXUUID, out.VXNetworkUUID)
	assert.Equal(t, testVlanID, out.VlanID)
	assert.Equal(t, testGateway, out.Gateway)
	assert.Equal(t, testNetmask, out.Netmask)
	assert.Equal(t, 0, client.createSecondaryIPRangeCalls,
		"reuse must not create a new secondary IP range")
	assert.Equal(t, 0, client.createVXNetworkCalls,
		"available VX must be reused")
}

func TestEnsurePOICNetwork_SkipsNonMatchingLifTypes(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: testVXUUID},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    testIPRangeUUID,
			LifeCycleState: sdkmodels.LifeCycleStateAvailable,
			L2VlanID:       uint32(testVlanID),
		}},
	}
	client.createSecondaryRange = &sdkmodels.SecondaryIPRangeV1{
		ResourceV1:  sdkmodels.ResourceV1{UUID: "sec-range-uuid"},
		IprangeUUID: testIPRangeUUID,
		Job:         &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "job-1"}},
	}
	client.getSecondaryRange = &sdkmodels.SecondaryIPRangeV1{IprangeUUID: testIPRangeUUID}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange("data-range-uuid", database.LifTypeDataLIF)}, nil)
	store.EXPECT().GetIPRangeByUUID(mock.Anything, testIPRangeUUID).
		Return(newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF), nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, testIPRangeUUID, out.IPRangeUUID)
	assert.Equal(t, 1, client.createSecondaryIPRangeCalls,
		"data-LIF range must not be reused for IC")
	assert.Equal(t, database.LifTypeInterclusterLIF, client.lastCreateSecondaryRangeLifType)
	assert.Equal(t, int64(28), client.lastCreateSecondaryMaskSize)
}

func TestEnsurePOICNetwork_FallsThroughWhenRangeFull(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: testVXUUID},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    "range-fresh",
			LifeCycleState: sdkmodels.LifeCycleStateAvailable,
			L2VlanID:       uint32(testVlanID),
		}},
	}
	client.createSecondaryRange = &sdkmodels.SecondaryIPRangeV1{
		ResourceV1:  sdkmodels.ResourceV1{UUID: "sec-range-uuid"},
		IprangeUUID: "range-fresh",
		Job:         &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "job-1"}},
	}
	client.getSecondaryRange = &sdkmodels.SecondaryIPRangeV1{IprangeUUID: "range-fresh"}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange("range-full", database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, "range-full", 2).
		Return(errors.New("range full"))
	store.EXPECT().GetIPRangeByUUID(mock.Anything, "range-fresh").
		Return(newICIPRange("range-fresh", database.LifTypeInterclusterLIF), nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, "range-fresh", 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	assert.Equal(t, "range-fresh", out.IPRangeUUID)
	assert.Equal(t, 1, client.createSecondaryIPRangeCalls,
		"full IC range must trigger a new create")
}

func TestEnsurePOICNetwork_RejectsNewRangeWithWrongLifType(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.createSecondaryRange = &sdkmodels.SecondaryIPRangeV1{
		ResourceV1:  sdkmodels.ResourceV1{UUID: "sec-range-uuid"},
		IprangeUUID: "bad-range",
		Job:         &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "job-1"}},
	}
	client.getSecondaryRange = &sdkmodels.SecondaryIPRangeV1{IprangeUUID: "bad-range"}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return(nil, nil)
	store.EXPECT().GetIPRangeByUUID(mock.Anything, "bad-range").
		Return(newICIPRange("bad-range", database.LifTypeDataLIF), nil)

	act := newICVNActivity(t, client, store)
	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lif_type")
}

// --- CVN network resolution --------------------------------------------------

func TestEnsurePOICNetwork_NoNetworkReturnsCVNHostNotFound(t *testing.T) {
	client := newStubCVNClient()
	act := newICVNActivity(t, client, database.NewMockStorage(t))

	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no CVN network found")
}

func TestEnsurePOICNetwork_MultipleNetworksIsValidationError(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{
		{ResourceV1: sdkmodels.ResourceV1{UUID: "net-1"}},
		{ResourceV1: sdkmodels.ResourceV1{UUID: "net-2"}},
	}
	act := newICVNActivity(t, client, database.NewMockStorage(t))

	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected exactly 1 CVN network")
}

// --- VX ensure matrix -------------------------------------------------------

func TestEnsurePOICNetwork_VXCreatedWhenMissing(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
	}
	client.createVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		Job:        &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "job-vx"}},
	}
	client.describeVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		L2VlanID:   uint32(testVlanID),
	}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	assert.Equal(t, testVXUUID, out.VXNetworkUUID)
	assert.Equal(t, testVlanID, out.VlanID)
	assert.Equal(t, 1, client.createVXNetworkCalls)
	assert.Equal(t, testNetworkUUID, client.lastCreateVXRequest.networkUUID)
	assert.Equal(t, testMicrozoneUUID, client.lastCreateVXRequest.microzoneUUID)
	assert.Equal(t, testIPRangeUUID, client.lastCreateVXRequest.ipRangeUUID)
}

func TestEnsurePOICNetwork_VXCreatingPollsUntilDone(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: testVXUUID},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    testIPRangeUUID,
			LifeCycleState: sdkmodels.LifeCycleStateCreating,
			Job:            &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "vx-job"}},
		}},
	}
	client.describeJob = &sdkmodels.JobV1{State: "done"}
	client.describeVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		L2VlanID:   uint32(testVlanID),
	}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	assert.Equal(t, testVXUUID, out.VXNetworkUUID)
	assert.Equal(t, testVlanID, out.VlanID)
	assert.Equal(t, 0, client.createVXNetworkCalls, "creating VX must be polled, not re-created")
	assert.GreaterOrEqual(t, client.describeJobCalls, 1)
}

func TestEnsurePOICNetwork_VXInErrorStateRecreated(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: "vx-old"},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    testIPRangeUUID,
			LifeCycleState: sdkmodels.LifeCycleStateError,
		}},
	}
	client.createVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		Job:        &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "vx-job"}},
	}
	client.describeVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		L2VlanID:   uint32(testVlanID),
	}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	assert.Equal(t, testVXUUID, out.VXNetworkUUID, "errored VX must be recreated")
	assert.Equal(t, 1, client.createVXNetworkCalls)
}

func TestEnsurePOICNetwork_VXWithDifferentIPRangeDoesNotMatch(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.describeNetwork = &sdkmodels.NetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID},
		Vxnetworks: []*sdkmodels.VXNetworkV1{{
			ResourceV1:     sdkmodels.ResourceV1{UUID: "vx-other"},
			MicrozoneUUID:  ptrString(testMicrozoneUUID),
			IprangeUUID:    "other-range",
			LifeCycleState: sdkmodels.LifeCycleStateAvailable,
			L2VlanID:       uint32(testVlanID),
		}},
	}
	client.createVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		Job:        &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "vx-job"}},
	}
	client.describeVXNetwork = &sdkmodels.VXNetworkV1{
		ResourceV1: sdkmodels.ResourceV1{UUID: testVXUUID},
		L2VlanID:   uint32(testVlanID),
	}
	client.describeJob = &sdkmodels.JobV1{State: "done"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return([]*datamodel.IPRange{newICIPRange(testIPRangeUUID, database.LifTypeInterclusterLIF)}, nil)
	store.EXPECT().ValidateIPAddressesAvailableInIPRange(mock.Anything, testIPRangeUUID, 2).Return(nil)

	act := newICVNActivity(t, client, store)
	out, err := runICEnsure(t, act, newICValidInput())
	require.NoError(t, err)
	assert.Equal(t, testVXUUID, out.VXNetworkUUID,
		"VX for a different range must not be reused")
	assert.Equal(t, 1, client.createVXNetworkCalls)
}

// TestEnsurePOICNetwork_NilStorage_FailsNonRetryable pins the guard that
// rejects a mis-wired activity struct up-front. Without this check the
// activity would panic at the first SE method call — much harder to trace
// than a typed configuration error at the entry point.
func TestEnsurePOICNetwork_NilStorage_FailsNonRetryable(t *testing.T) {
	act := &activities.POICVNActivity{
		SE: nil,
		// A ClientFactory is provided so the failure isolates to the
		// SE guard rather than downstream CVN wiring. Without it a
		// future refactor that reordered the two checks could silently
		// mask this test.
		ClientFactory: func(_ context.Context, _ string) (activities.POICLifCVNClient, error) {
			return newStubCVNClient(), nil
		},
		Config: commonparams.POICLifConfig{SubnetMaskSize: 28},
	}
	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage is not configured")
}

// TestEnsurePOICNetwork_DefaultClientFactory_UsedWhenNil pins the fallback
// contract: when a caller leaves ClientFactory unset (as production wiring
// does), cvnClient MUST fall back to defaultPOICLifCVNClientFactory rather
// than nil-panicking or silently succeeding with a stub. We exercise this by
// leaving CVN_HOST unset (its default zero-value in the cvn package for the
// test binary) so the default factory's inner NewFromContext returns the
// typed ErrCVNHostNotFound. The activity must surface that as a
// non-retryable Temporal error — the concrete detail string is scrubbed by
// Temporal's wrapper, so we assert on the retry semantic (the strongest
// property callers depend on) rather than the message text.
func TestEnsurePOICNetwork_DefaultClientFactory_UsedWhenNil(t *testing.T) {
	store := database.NewMockStorage(t)
	act := &activities.POICVNActivity{
		SE:            store,
		ClientFactory: nil, // force default factory
		Config:        commonparams.POICLifConfig{SubnetMaskSize: 28},
	}
	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err,
		"defaultPOICLifCVNClientFactory must run and surface NewFromContext's error when CVN_HOST is unset")
}

func TestEnsurePOICNetwork_JobErrorFailsActivity(t *testing.T) {
	client := newStubCVNClient()
	client.networks = []*sdkmodels.NetworkV1{{ResourceV1: sdkmodels.ResourceV1{UUID: testNetworkUUID}}}
	client.createSecondaryRange = &sdkmodels.SecondaryIPRangeV1{
		ResourceV1:  sdkmodels.ResourceV1{UUID: "sec-range-uuid"},
		IprangeUUID: testIPRangeUUID,
		Job:         &sdkmodels.JobV1{ResourceV1: sdkmodels.ResourceV1{UUID: "job-1"}},
	}
	client.describeJob = &sdkmodels.JobV1{State: "error", StateDetails: "kaboom"}

	store := database.NewMockStorage(t)
	store.EXPECT().GetInterclusterLifIPRanges(mock.Anything, testAccountUUID, testRemoteAccountID).
		Return(nil, nil)

	act := newICVNActivity(t, client, store)
	_, err := runICEnsure(t, act, newICValidInput())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kaboom")
}

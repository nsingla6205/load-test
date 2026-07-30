package activities_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

// stubProvider satisfies vsa.Provider (via the embedded interface) but does NOT
// implement vsa.ExpertModePasswordProvider, so the type assertion in the
// set/expire activities fails.
type stubProvider struct {
	vsa.Provider
}

// stubPwdProvider satisfies both vsa.Provider and vsa.ExpertModePasswordProvider
// and records the arguments passed to the two ONTAP calls.
type stubPwdProvider struct {
	vsa.Provider
	resetErr     error
	expireErr    error
	resetCalled  bool
	expireCalled bool
	gotCluster   string
	gotUsername  string
	gotPassword  string
}

func (s *stubPwdProvider) ResetLoginPassword(_ context.Context, vserver, username, newPassword string) error {
	s.resetCalled = true
	s.gotCluster, s.gotUsername, s.gotPassword = vserver, username, newPassword
	return s.resetErr
}

func (s *stubPwdProvider) ExpireLoginPassword(_ context.Context, vserver, username string) error {
	s.expireCalled = true
	s.gotCluster, s.gotUsername = vserver, username
	return s.expireErr
}

// ---------------------------------------------------------------------------
// RecordTempPasswordState
// ---------------------------------------------------------------------------

func TestRecordTempPasswordState_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(act.RecordTempPasswordState)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}}

	// The write must go through the map path and set active=true with a
	// non-nil creation timestamp so the reconciler can compute the window.
	mockStorage.EXPECT().
		UpdatePoolFields(mock.Anything, "pool-uuid-1", mock.MatchedBy(func(updates map[string]interface{}) bool {
			raw, ok := updates["external_temp_credentials"]
			if !ok {
				return false
			}
			etc, ok := raw.(datamodel.ExternalTempCredentials)
			return ok && etc.TempPasswordActive && etc.TempPasswordCreatedAt != nil
		})).
		Return(nil).Once()

	_, err := env.ExecuteActivity(act.RecordTempPasswordState, pool)
	require.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestRecordTempPasswordState_NilOrEmptyPool(t *testing.T) {
	cases := []struct {
		name string
		pool *datamodel.Pool
	}{
		{name: "nil pool", pool: nil},
		{name: "empty uuid", pool: &datamodel.Pool{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			env.RegisterActivity(act.RecordTempPasswordState)

			_, err := env.ExecuteActivity(act.RecordTempPasswordState, tc.pool)
			require.Error(t, err)
			// Must never attempt a DB write without a valid pool identifier.
			mockStorage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestRecordTempPasswordState_UpdateFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(act.RecordTempPasswordState)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}}
	mockStorage.EXPECT().
		UpdatePoolFields(mock.Anything, "pool-uuid-1", mock.Anything).
		Return(errors.New("db down")).Once()

	_, err := env.ExecuteActivity(act.RecordTempPasswordState, pool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
	mockStorage.AssertExpectations(t)
}

func validResetPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "pool-uuid-1", ID: 1},
		PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
	}
}

// ---------------------------------------------------------------------------
// SetOciAdminPasswordOnVSA — validation / early-exit branches
// ---------------------------------------------------------------------------

func TestSetOciAdminPasswordOnVSA_InputValidation(t *testing.T) {
	cases := []struct {
		name        string
		pool        *datamodel.Pool
		clusterName string
		username    string
		tempPass    string
	}{
		{name: "nil pool", pool: nil, clusterName: "cl", username: "ociadmin", tempPass: "pw"},
		{
			name:        "nil pool credentials",
			pool:        &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}},
			clusterName: "cl", username: "ociadmin", tempPass: "pw",
		},
		{name: "empty cluster", pool: validResetPool(), clusterName: "", username: "ociadmin", tempPass: "pw"},
		{name: "empty username", pool: validResetPool(), clusterName: "cl", username: "", tempPass: "pw"},
		{name: "empty tempPassword", pool: validResetPool(), clusterName: "cl", username: "ociadmin", tempPass: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			env.RegisterActivity(act.SetOciAdminPasswordOnVSA)

			_, err := env.ExecuteActivity(act.SetOciAdminPasswordOnVSA,
				tc.pool, tc.clusterName, tc.username, tc.tempPass)
			require.Error(t, err)
			// Validation must fail before any node lookup.
			mockStorage.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
		})
	}
}

func TestSetOciAdminPasswordOnVSA_NoNodesFound(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).Return([]*datamodel.Node{}, nil).Once()

	_, err := env.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no nodes found")
	mockStorage.AssertExpectations(t)
}

func TestSetOciAdminPasswordOnVSA_GetNodesError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	env.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).Return(nil, errors.New("db down")).Once()

	// A node-lookup failure must abort the set (surfaced as a DB read error);
	// the underlying message is wrapped by the VCP error, so we only assert the
	// activity failed and did not proceed to the ONTAP call.
	_, err := env.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// SetOciAdminPasswordOnVSA — provider resolution / ONTAP-call branches
// ---------------------------------------------------------------------------

func TestSetOciAdminPasswordOnVSA_GetProviderError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, errors.New("provider build failed")
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestSetOciAdminPasswordOnVSA_NilProvider(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return nil, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestSetOciAdminPasswordOnVSA_ProviderMissingExpertModeSupport(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return &stubProvider{}, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expert-mode password")
	mockStorage.AssertExpectations(t)
}

func TestSetOciAdminPasswordOnVSA_SetFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.SetOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	stub := &stubPwdProvider{resetErr: errors.New("password reuse not allowed")}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster", "ociadmin", "pw")
	require.Error(t, err)
	assert.True(t, stub.resetCalled)
	assert.False(t, stub.expireCalled, "the set activity must never expire the password")
	mockStorage.AssertExpectations(t)
}

func TestSetOciAdminPasswordOnVSA_HappyPath(t *testing.T) {
	cases := []struct {
		name     string
		authType int
		nodes    []*datamodel.Node
	}{
		{
			name:     "password auth mirrors endpoint address",
			authType: env.USERNAME_PWD,
			nodes:    []*datamodel.Node{{EndpointAddress: "10.0.0.1"}, {EndpointAddress: "10.0.0.2"}},
		},
		{
			name:     "certificate auth maps to host dns name",
			authType: env.USER_CERTIFICATE,
			nodes: []*datamodel.Node{
				{EndpointAddress: "10.0.0.1", HostDNSName: "node1.example.com"},
				{EndpointAddress: "", HostDNSName: "skip-empty"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			testEnv := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			testEnv.RegisterActivity(act.SetOciAdminPasswordOnVSA)

			pool := validResetPool()
			pool.PoolCredentials.AuthType = tc.authType
			mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).Return(tc.nodes, nil).Once()

			stub := &stubPwdProvider{}
			orig := vsa.GetProviderByNode
			vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
				return stub, nil
			}
			t.Cleanup(func() { vsa.GetProviderByNode = orig })

			_, err := testEnv.ExecuteActivity(act.SetOciAdminPasswordOnVSA, pool, "cluster1", "ociadmin", "tempPw!")
			require.NoError(t, err)
			assert.True(t, stub.resetCalled)
			assert.False(t, stub.expireCalled, "the set activity must never expire the password")
			assert.Equal(t, "cluster1", stub.gotCluster)
			assert.Equal(t, "ociadmin", stub.gotUsername)
			assert.Equal(t, "tempPw!", stub.gotPassword)
			mockStorage.AssertExpectations(t)
		})
	}
}

// ---------------------------------------------------------------------------
// ExpireOciAdminPasswordOnVSA — validation / early-exit branches
// ---------------------------------------------------------------------------

func TestExpireOciAdminPasswordOnVSA_InputValidation(t *testing.T) {
	cases := []struct {
		name        string
		pool        *datamodel.Pool
		clusterName string
		username    string
	}{
		{name: "nil pool", pool: nil, clusterName: "cl", username: "ociadmin"},
		{
			name:        "nil pool credentials",
			pool:        &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}},
			clusterName: "cl", username: "ociadmin",
		},
		{name: "empty cluster", pool: validResetPool(), clusterName: "", username: "ociadmin"},
		{name: "empty username", pool: validResetPool(), clusterName: "cl", username: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			env.RegisterActivity(act.ExpireOciAdminPasswordOnVSA)

			_, err := env.ExecuteActivity(act.ExpireOciAdminPasswordOnVSA,
				tc.pool, tc.clusterName, tc.username)
			require.Error(t, err)
			// Validation must fail before any node lookup.
			mockStorage.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
		})
	}
}

// ---------------------------------------------------------------------------
// ExpireOciAdminPasswordOnVSA — provider resolution / ONTAP-call branches
// ---------------------------------------------------------------------------

func TestExpireOciAdminPasswordOnVSA_ProviderMissingExpertModeSupport(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.ExpireOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return &stubProvider{}, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.ExpireOciAdminPasswordOnVSA, pool, "cluster", "ociadmin")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expert-mode password")
	mockStorage.AssertExpectations(t)
}

func TestExpireOciAdminPasswordOnVSA_ExpireFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.ExpireOciAdminPasswordOnVSA)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	stub := &stubPwdProvider{expireErr: errors.New("expire failed")}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.ExpireOciAdminPasswordOnVSA, pool, "cluster", "ociadmin")
	require.Error(t, err)
	assert.False(t, stub.resetCalled, "the expire activity must never set the password")
	assert.True(t, stub.expireCalled)
	mockStorage.AssertExpectations(t)
}

func TestExpireOciAdminPasswordOnVSA_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.ExpireOciAdminPasswordOnVSA)

	pool := validResetPool()
	pool.PoolCredentials.AuthType = env.USERNAME_PWD
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	stub := &stubPwdProvider{}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.ExpireOciAdminPasswordOnVSA, pool, "cluster1", "ociadmin")
	require.NoError(t, err)
	assert.False(t, stub.resetCalled, "the expire activity must never set the password")
	assert.True(t, stub.expireCalled)
	assert.Equal(t, "cluster1", stub.gotCluster)
	assert.Equal(t, "ociadmin", stub.gotUsername)
	mockStorage.AssertExpectations(t)
}

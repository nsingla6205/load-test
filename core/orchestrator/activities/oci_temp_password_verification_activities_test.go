package activities_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"go.temporal.io/sdk/testsuite"
)

// ---------------------------------------------------------------------------
// CheckOciAdminPasswordResetConsumed
// ---------------------------------------------------------------------------

func TestCheckOciAdminPasswordResetConsumed_InputValidation(t *testing.T) {
	cases := []struct {
		name     string
		pool     *datamodel.Pool
		username string
		since    time.Time
	}{
		{name: "nil pool", pool: nil, username: "ociadmin", since: time.Now()},
		{
			name:     "nil pool credentials",
			pool:     &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "u"}},
			username: "ociadmin", since: time.Now(),
		},
		{name: "empty username", pool: validResetPool(), username: "", since: time.Now()},
		{name: "zero since", pool: validResetPool(), username: "ociadmin", since: time.Time{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			testEnv := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			testEnv.RegisterActivity(act.CheckOciAdminPasswordResetConsumed)

			_, err := testEnv.ExecuteActivity(act.CheckOciAdminPasswordResetConsumed, tc.pool, tc.username, tc.since)
			require.Error(t, err)
			// Validation must fail before any node lookup.
			mockStorage.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
		})
	}
}

func TestCheckOciAdminPasswordResetConsumed_ConsumedAndNotConsumed(t *testing.T) {
	cases := []struct {
		name          string
		auditConsumed bool
		want          bool
	}{
		{name: "consumed when a record exists", auditConsumed: true, want: true},
		{name: "not consumed when no records", auditConsumed: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			testEnv := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			testEnv.RegisterActivity(act.CheckOciAdminPasswordResetConsumed)

			pool := validResetPool()
			mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
				Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

			since := time.Now().Add(-2 * time.Hour).UTC()
			stub := &stubPwdProvider{auditConsumed: tc.auditConsumed}
			orig := vsa.GetProviderByNode
			vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
				return stub, nil
			}
			t.Cleanup(func() { vsa.GetProviderByNode = orig })

			var consumed bool
			val, err := testEnv.ExecuteActivity(act.CheckOciAdminPasswordResetConsumed, pool, "ociadmin", since)
			require.NoError(t, err)
			require.NoError(t, val.Get(&consumed))
			assert.Equal(t, tc.want, consumed)
			assert.True(t, stub.auditCalled)
			assert.Equal(t, "ociadmin", stub.gotAuditUser)
			assert.WithinDuration(t, since, stub.gotAuditSince, time.Second)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestCheckOciAdminPasswordResetConsumed_AuditError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.CheckOciAdminPasswordResetConsumed)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	stub := &stubPwdProvider{auditErr: errors.New("ontap audit query failed")}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.CheckOciAdminPasswordResetConsumed, pool, "ociadmin", time.Now().UTC())
	require.Error(t, err)
	assert.True(t, stub.auditCalled)
	mockStorage.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// GenerateAndSetOciAdminTempPassword
// ---------------------------------------------------------------------------

func TestGenerateAndSetOciAdminTempPassword_InputValidation(t *testing.T) {
	cases := []struct {
		name        string
		pool        *datamodel.Pool
		clusterName string
		username    string
	}{
		{name: "nil pool", pool: nil, clusterName: "cl", username: "ociadmin"},
		{name: "empty cluster", pool: validResetPool(), clusterName: "", username: "ociadmin"},
		{name: "empty username", pool: validResetPool(), clusterName: "cl", username: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			testEnv := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			testEnv.RegisterActivity(act.GenerateAndSetOciAdminTempPassword)

			_, err := testEnv.ExecuteActivity(act.GenerateAndSetOciAdminTempPassword, tc.pool, tc.clusterName, tc.username)
			require.Error(t, err)
			mockStorage.AssertNotCalled(t, "GetNodesByPoolID", mock.Anything, mock.Anything)
		})
	}
}

func TestGenerateAndSetOciAdminTempPassword_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GenerateAndSetOciAdminTempPassword)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	origGen := utils.GenerateStrongPassword
	utils.GenerateStrongPassword = func(length int) (string, error) { return "GENERATED-PW", nil }
	t.Cleanup(func() { utils.GenerateStrongPassword = origGen })

	stub := &stubPwdProvider{}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.GenerateAndSetOciAdminTempPassword, pool, "cluster1", "ociadmin")
	require.NoError(t, err)
	assert.True(t, stub.resetCalled)
	assert.False(t, stub.expireCalled, "generate+set must never expire the password itself")
	assert.Equal(t, "cluster1", stub.gotCluster)
	assert.Equal(t, "ociadmin", stub.gotUsername)
	assert.Equal(t, "GENERATED-PW", stub.gotPassword, "the freshly generated password must be the one set")
	mockStorage.AssertExpectations(t)
}

func TestGenerateAndSetOciAdminTempPassword_GenerateFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GenerateAndSetOciAdminTempPassword)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	origGen := utils.GenerateStrongPassword
	utils.GenerateStrongPassword = func(length int) (string, error) { return "", errors.New("entropy failure") }
	t.Cleanup(func() { utils.GenerateStrongPassword = origGen })

	stub := &stubPwdProvider{}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.GenerateAndSetOciAdminTempPassword, pool, "cluster1", "ociadmin")
	require.Error(t, err)
	assert.False(t, stub.resetCalled, "no password must be set when generation fails")
	mockStorage.AssertExpectations(t)
}

func TestCheckOciAdminPasswordResetConsumed_ProviderResolveFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.CheckOciAdminPasswordResetConsumed)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return(nil, errors.New("nodes unavailable")).Once()

	_, err := testEnv.ExecuteActivity(act.CheckOciAdminPasswordResetConsumed, pool, "ociadmin", time.Now().UTC())
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestGenerateAndSetOciAdminTempPassword_ProviderResolveFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GenerateAndSetOciAdminTempPassword)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return(nil, errors.New("nodes unavailable")).Once()

	_, err := testEnv.ExecuteActivity(act.GenerateAndSetOciAdminTempPassword, pool, "cluster1", "ociadmin")
	require.Error(t, err)
	mockStorage.AssertExpectations(t)
}

func TestGenerateAndSetOciAdminTempPassword_ResetFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.GenerateAndSetOciAdminTempPassword)

	pool := validResetPool()
	mockStorage.EXPECT().GetNodesByPoolID(mock.Anything, pool.ID).
		Return([]*datamodel.Node{{EndpointAddress: "10.0.0.1"}}, nil).Once()

	origGen := utils.GenerateStrongPassword
	utils.GenerateStrongPassword = func(length int) (string, error) { return "GENERATED-PW", nil }
	t.Cleanup(func() { utils.GenerateStrongPassword = origGen })

	stub := &stubPwdProvider{resetErr: errors.New("ontap reset failed")}
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
		return stub, nil
	}
	t.Cleanup(func() { vsa.GetProviderByNode = orig })

	_, err := testEnv.ExecuteActivity(act.GenerateAndSetOciAdminTempPassword, pool, "cluster1", "ociadmin")
	require.Error(t, err)
	assert.True(t, stub.resetCalled)
	mockStorage.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// ClearTempPasswordState
// ---------------------------------------------------------------------------

func TestClearTempPasswordState_HappyPath(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.ClearTempPasswordState)

	createdAt := time.Now().Add(-2 * time.Hour).UTC()
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
		ExternalTempCredentials: &datamodel.ExternalTempCredentials{
			TempPasswordActive:    true,
			TempPasswordCreatedAt: &createdAt,
		},
	}
	mockStorage.EXPECT().
		UpdatePoolFields(mock.Anything, "pool-uuid-1", mock.MatchedBy(func(updates map[string]interface{}) bool {
			raw, ok := updates["external_temp_credentials"]
			if !ok {
				return false
			}
			etc, ok := raw.(datamodel.ExternalTempCredentials)
			return ok && !etc.TempPasswordActive && etc.TempPasswordCreatedAt != nil &&
				etc.TempPasswordCreatedAt.Equal(createdAt)
		})).
		Return(nil).Once()

	_, err := testEnv.ExecuteActivity(act.ClearTempPasswordState, pool)
	require.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestClearTempPasswordState_NilOrEmptyPool(t *testing.T) {
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
			testEnv := ts.NewTestActivityEnvironment()

			mockStorage := database.NewMockStorage(t)
			act := activities.PoolActivity{SE: mockStorage}
			testEnv.RegisterActivity(act.ClearTempPasswordState)

			_, err := testEnv.ExecuteActivity(act.ClearTempPasswordState, tc.pool)
			require.Error(t, err)
			mockStorage.AssertNotCalled(t, "UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestClearTempPasswordState_UpdateFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	testEnv := ts.NewTestActivityEnvironment()

	mockStorage := database.NewMockStorage(t)
	act := activities.PoolActivity{SE: mockStorage}
	testEnv.RegisterActivity(act.ClearTempPasswordState)

	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}}
	mockStorage.EXPECT().
		UpdatePoolFields(mock.Anything, "pool-uuid-1", mock.Anything).
		Return(errors.New("db down")).Once()

	_, err := testEnv.ExecuteActivity(act.ClearTempPasswordState, pool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db down")
	mockStorage.AssertExpectations(t)
}

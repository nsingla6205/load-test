package backgroundworkflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

type VerifyOciTempPasswordWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func TestVerifyOciTempPasswordWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(VerifyOciTempPasswordWorkflowTestSuite))
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	verifyActivity := &backgroundactivities.OciTempPasswordVerificationActivity{}
	s.env.RegisterActivity(verifyActivity.ListOciPoolsWithActiveTempPassword)

	poolActivity := &activities.PoolActivity{}
	s.env.RegisterActivity(poolActivity.ParseVlmConfig)
	s.env.RegisterActivity(poolActivity.CheckOciAdminPasswordResetConsumed)
	s.env.RegisterActivity(poolActivity.ClearTempPasswordState)
	s.env.RegisterActivity(poolActivity.GenerateAndSetOciAdminTempPassword)
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) AfterTest(_, _ string) {
	s.env.AssertExpectations(s.T())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) withVerificationEnabled() {
	orig := env.EnableOCITempPasswordResetCron
	env.EnableOCITempPasswordResetCron = true
	s.T().Cleanup(func() { env.EnableOCITempPasswordResetCron = orig })
}

func activePool(uuid string, createdAt *time.Time) *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: uuid},
		PoolCredentials: &datamodel.PoolCredentials{Password: "pw"},
		ExternalTempCredentials: &datamodel.ExternalTempCredentials{
			TempPasswordActive:    true,
			TempPasswordCreatedAt: createdAt,
		},
	}
}

func vlmConfigWithCluster(name string) *vlm.VLMConfig {
	return &vlm.VLMConfig{VsaCluster: vlm.VsaClusterConfig{ClusterName: name}}
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) onList(pools ...*datamodel.Pool) {
	s.env.OnActivity((&backgroundactivities.OciTempPasswordVerificationActivity{}).ListOciPoolsWithActiveTempPassword,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil).Once()
}

func poolIs(uuid string) any {
	return mock.MatchedBy(func(p *datamodel.Pool) bool { return p != nil && p.UUID == uuid })
}

// ---------------------------------------------------------------------------
// Gating
// ---------------------------------------------------------------------------

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestDisabled_NoOp() {
	orig := env.EnableOCITempPasswordResetCron
	env.EnableOCITempPasswordResetCron = false
	s.T().Cleanup(func() { env.EnableOCITempPasswordResetCron = orig })

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// ---------------------------------------------------------------------------
// Inline per-pool processing (single workflow execution)
// ---------------------------------------------------------------------------

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestConsumed_ClearsFlag() {
	s.withVerificationEnabled()
	created := time.Now().Add(-2 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).ClearTempPasswordState,
		mock.Anything, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "GenerateAndSetOciAdminTempPassword", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestWithinWindow_NoOp() {
	s.withVerificationEnabled()
	created := time.Now().Add(-1 * time.Hour) // well within the 24h default window
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "ClearTempPasswordState", mock.Anything, mock.Anything)
	s.env.AssertNotCalled(s.T(), "GenerateAndSetOciAdminTempPassword", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestElapsed_Rotates() {
	s.withVerificationEnabled()
	created := time.Now().Add(-48 * time.Hour) // past the 24h default window
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).GenerateAndSetOciAdminTempPassword,
		mock.Anything, mock.Anything, "cluster1", mock.Anything).Return(nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).ClearTempPasswordState,
		mock.Anything, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestNoActiveTempPassword_Skipped() {
	s.withVerificationEnabled()
	pool := &datamodel.Pool{
		BaseModel:               datamodel.BaseModel{UUID: "pool-a"},
		ExternalTempCredentials: &datamodel.ExternalTempCredentials{TempPasswordActive: false},
	}
	s.onList(pool)

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	// No cluster resolution or audit check when there's nothing active to verify.
	s.env.AssertNotCalled(s.T(), "ParseVlmConfig", mock.Anything, mock.Anything)
}

// One pool failing does not abort the sweep: pool-a fails (empty cluster name),
// pool-b is still processed and the workflow completes without error.
func (s *VerifyOciTempPasswordWorkflowTestSuite) TestOnePoolFails_OthersStillProcessed() {
	s.withVerificationEnabled()
	created := time.Now().Add(-1 * time.Hour)
	s.onList(activePool("pool-a", &created), activePool("pool-b", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, poolIs("pool-a")).Return(vlmConfigWithCluster(""), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, poolIs("pool-b")).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, poolIs("pool-b"), mock.Anything, mock.Anything).Return(false, nil).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestEmptyList_NoOp() {
	s.withVerificationEnabled()
	s.onList()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "ParseVlmConfig", mock.Anything, mock.Anything)
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestListFails_WorkflowErrors() {
	s.withVerificationEnabled()
	s.env.OnActivity((&backgroundactivities.OciTempPasswordVerificationActivity{}).ListOciPoolsWithActiveTempPassword,
		mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("db down")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestBatchSizeFallback_WhenNonPositive() {
	s.withVerificationEnabled()
	orig := env.OCITempPasswordVerificationBatchSize
	env.OCITempPasswordVerificationBatchSize = 0
	s.T().Cleanup(func() { env.OCITempPasswordVerificationBatchSize = orig })

	s.env.OnActivity((&backgroundactivities.OciTempPasswordVerificationActivity{}).ListOciPoolsWithActiveTempPassword,
		mock.Anything, 0, 10).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: nil, HasMore: false}, nil).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestPagination_ProcessesSecondBatch() {
	s.withVerificationEnabled()
	created := time.Now().Add(-1 * time.Hour)
	poolA := activePool("pool-a", &created)
	poolB := activePool("pool-b", &created)

	s.env.OnActivity((&backgroundactivities.OciTempPasswordVerificationActivity{}).ListOciPoolsWithActiveTempPassword,
		mock.Anything, 0, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: []*datamodel.Pool{poolA}, HasMore: true}, nil).Once()
	s.env.OnActivity((&backgroundactivities.OciTempPasswordVerificationActivity{}).ListOciPoolsWithActiveTempPassword,
		mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: []*datamodel.Pool{poolB}, HasMore: false}, nil).Once()

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Twice()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Twice()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestMissingCreatedAt_Skipped() {
	s.withVerificationEnabled()
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-a"},
		ExternalTempCredentials: &datamodel.ExternalTempCredentials{
			TempPasswordActive:    true,
			TempPasswordCreatedAt: nil,
		},
	}
	s.onList(pool)

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	s.env.AssertNotCalled(s.T(), "ParseVlmConfig", mock.Anything, mock.Anything)
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestParseVlmConfigFails_Isolated() {
	s.withVerificationEnabled()
	created := time.Now().Add(-1 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(nil, errors.New("bad vlm")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestCheckConsumedFails_Isolated() {
	s.withVerificationEnabled()
	created := time.Now().Add(-1 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("audit failed")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestClearStateFails_Isolated() {
	s.withVerificationEnabled()
	created := time.Now().Add(-2 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).ClearTempPasswordState,
		mock.Anything, mock.Anything).Return(errors.New("db write failed")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestRotateGenerateFails_Isolated() {
	s.withVerificationEnabled()
	created := time.Now().Add(-48 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).GenerateAndSetOciAdminTempPassword,
		mock.Anything, mock.Anything, "cluster1", mock.Anything).Return(errors.New("set failed")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *VerifyOciTempPasswordWorkflowTestSuite) TestRotateRecordFails_Isolated() {
	s.withVerificationEnabled()
	created := time.Now().Add(-48 * time.Hour)
	s.onList(activePool("pool-a", &created))

	s.env.OnActivity((&activities.PoolActivity{}).ParseVlmConfig,
		mock.Anything, mock.Anything).Return(vlmConfigWithCluster("cluster1"), nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).CheckOciAdminPasswordResetConsumed,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).GenerateAndSetOciAdminTempPassword,
		mock.Anything, mock.Anything, "cluster1", mock.Anything).Return(nil).Once()
	s.env.OnActivity((&activities.PoolActivity{}).ClearTempPasswordState,
		mock.Anything, mock.Anything).Return(errors.New("clear failed")).Once()

	s.env.ExecuteWorkflow(VerifyOciTempPasswordsWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

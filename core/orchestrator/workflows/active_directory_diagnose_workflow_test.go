package workflows

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// diagnoseWFFixture bundles the input params + activity return values shared
// across DiagnoseActiveDirectoryWorkflow tests. Individual tests tweak the
// fields (LdapEnabled / ManagedAD / KdcIP) to exercise the optional branches.
type diagnoseWFFixture struct {
	params          *common.DiagnoseActiveDirectoryParams
	adModel         *models.ActiveDirectory
	pool            *datamodel.Pool
	adRecord        *datamodel.ActiveDirectory
	node            *models.Node
	vlmConfig       *vlm.VLMConfig
	creds           *vlm.OntapCredentials
	createReq       *vlm.CreateSVMRequest
	createResp      *vlm.CreateSVMResponse
	diagnosisSVMRow *datamodel.Svm
	vsaAD           *vsa.ActiveDirectory
	job             *datamodel.Job
}

func newDiagnoseWFFixture() *diagnoseWFFixture {
	return &diagnoseWFFixture{
		params: &common.DiagnoseActiveDirectoryParams{
			ProjectNumber: "1234567890",
			PoolUUID:      "pool-uuid-1",
			ResourceType:  "AD",
		},
		adModel: &models.ActiveDirectory{BaseModel: models.BaseModel{UUID: "ad-uuid-1"}},
		pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 7, UUID: "pool-uuid-1"},
			PoolAttributes: &datamodel.PoolAttributes{LdapEnabled: false},
		},
		adRecord:  &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 3, UUID: "ad-uuid-1"}},
		node:      &models.Node{Name: "node-a"},
		vlmConfig: &vlm.VLMConfig{},
		creds:     &vlm.OntapCredentials{AdminPassword: "pw"},
		createReq: &vlm.CreateSVMRequest{Name: "diag-svm", OntapCredentials: vlm.OntapCredentials{AdminPassword: "pw"}},
		createResp: &vlm.CreateSVMResponse{VLMConfig: vlm.VLMConfig{
			Svm: map[string]vlm.SvmConfig{"diag-svm": {Svmname: "diag-svm", Svmuuid: "ext-uuid"}},
		}},
		diagnosisSVMRow: &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{UUID: "svm-row-uuid"},
			Name:       "diag-svm",
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "ext-uuid", IsDirectoryDiagnosis: true},
		},
		vsaAD: &vsa.ActiveDirectory{Domain: "example.com"},
		job:   &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}, State: string(datamodel.JobsStateNEW)},
	}
}

func newDiagnoseWFEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logger": encodedValue}})
	env.SetTestTimeout(1 * time.Minute)
	return env
}

// registerDiagnoseActivities registers every activity and child workflow the
// diagnose workflow can invoke so OnActivity/OnWorkflow can stub them.
func registerDiagnoseActivities(env *testsuite.TestWorkflowEnvironment) {
	commonActivity := activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	adReused := active_directory_activities.ActiveDirectoryActivity{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}

	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(poolActivity.ParseVlmConfig)
	env.RegisterActivity(poolActivity.CreateOnTapCredentials)
	env.RegisterActivity(adReused.GetActiveDirectoryForPool)
	env.RegisterActivity(adReused.CreateOrModifyADDNS)
	env.RegisterActivity(adReused.GetOrCreateCifsService)
	env.RegisterActivity(adReused.ConfigureLdapForSVM)
	env.RegisterActivity(diag.LoadPoolForDiagnosis)
	env.RegisterActivity(diag.GetActiveDirectoryRecordForPool)
	env.RegisterActivity(diag.GetNodeForDiagnosis)
	env.RegisterActivity(diag.PrepareCreateDiagnosisSVMRequest)
	env.RegisterActivity(diag.SaveDiagnosisSVMToDB)
	env.RegisterActivity(diag.GetIlbNasLifHomeNodeForDiagnosis)
	env.RegisterActivity(diag.SetIlbNasLifServicePolicy)
	env.RegisterActivity(diag.SoftDeleteDiagnosisSVMFromDB)
	env.RegisterActivity(diag.DeleteDiagnosisCifsServer)
	env.RegisterActivity(diag.DeleteDiagnosisKerberosConfig)
	env.RegisterActivity(diag.RestoreADStateFromSVMUsage)

	env.RegisterWorkflow(EnsureKerberosConfigWorkflow)
	// The rollback manager appends the customer-facing error message as a
	// trailing string arg when executing rollback workflows.
	env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, req *vlm.DeleteSVMRequest, _ string) error { return nil },
		workflow.RegisterOptions{Name: vlm.DeleteVSASVMWorkflowName},
	)
}

// mockDiagnoseCleanup stubs the teardown steps that run once the workflow has
// registered the full rollback: the diagnosis SVM row soft-delete, the CIFS
// unbind, the VLM SVM delete child workflow, and the AD state restore. Each is
// Once() so a skipped or repeated teardown fails env.AssertExpectations.
// Tests taking the Kerberos branch additionally stub DeleteDiagnosisKerberosConfig.
func mockDiagnoseCleanup(env *testsuite.TestWorkflowEnvironment) {
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}
	env.OnActivity(diag.SoftDeleteDiagnosisSVMFromDB, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(diag.DeleteDiagnosisCifsServer, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnWorkflow(vlm.DeleteVSASVMWorkflowName, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnActivity(diag.RestoreADStateFromSVMUsage, mock.Anything, mock.Anything).Return(nil).Once()
}

// mockDiagnoseRestoreOnly stubs the single teardown step that runs when the
// workflow fails before the diagnosis SVM exists: nothing was registered on the
// rollback manager, so only the AD state restore executes.
func mockDiagnoseRestoreOnly(env *testsuite.TestWorkflowEnvironment) {
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}
	env.OnActivity(diag.RestoreADStateFromSVMUsage, mock.Anything, mock.Anything).Return(nil).Once()
}

// assertNoDiagnoseCleanup asserts no teardown step ran at all, for failures
// that return before the deferred cleanup is installed.
func assertNoDiagnoseCleanup(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	env.AssertActivityNumberOfCalls(t, "RestoreADStateFromSVMUsage", 0)
	env.AssertActivityNumberOfCalls(t, "SoftDeleteDiagnosisSVMFromDB", 0)
	env.AssertActivityNumberOfCalls(t, "DeleteDiagnosisCifsServer", 0)
	env.AssertActivityNumberOfCalls(t, "DeleteDiagnosisKerberosConfig", 0)
	env.AssertWorkflowNumberOfCalls(t, vlm.DeleteVSASVMWorkflowName, 0)
}

// withMockVSAManager swaps GetNewVSAClientWorkflowManager for a mock that
// returns the configured CreateVSASVM result.
func withMockVSAManager(createResp *vlm.CreateSVMResponse, createErr error) func() {
	mockMgr := new(vlm.MockVlmWorkflowClient)
	mockMgr.On("CreateVSASVM", mock.Anything, mock.Anything).Return(createResp, createErr)
	orig := GetNewVSAClientWorkflowManager
	GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockMgr }
	return func() { GetNewVSAClientWorkflowManager = orig }
}

// mockDiagnoseUpToCifs stubs the job scaffolding + the linear happy-path
// activities through GetOrCreateCifsService.
func mockDiagnoseUpToCifs(env *testsuite.TestWorkflowEnvironment, f *diagnoseWFFixture) {
	commonActivity := activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	adReused := active_directory_activities.ActiveDirectoryActivity{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}

	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(f.job, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(diag.LoadPoolForDiagnosis, mock.Anything, mock.Anything).Return(f.pool, nil)
	env.OnActivity(diag.GetActiveDirectoryRecordForPool, mock.Anything, mock.Anything).Return(f.adRecord, nil)
	env.OnActivity(diag.GetNodeForDiagnosis, mock.Anything, mock.Anything).Return(f.node, nil)
	env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).Return(f.vlmConfig, nil)
	env.OnActivity(poolActivity.CreateOnTapCredentials, mock.Anything, mock.Anything).Return(f.creds, nil)
	env.OnActivity(diag.PrepareCreateDiagnosisSVMRequest, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.createReq, nil)
	env.OnActivity(diag.SaveDiagnosisSVMToDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.diagnosisSVMRow, nil)
	env.OnActivity(diag.GetIlbNasLifHomeNodeForDiagnosis, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.node, nil)
	env.OnActivity(diag.SetIlbNasLifServicePolicy, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(adReused.GetActiveDirectoryForPool, mock.Anything, mock.Anything).Return(f.vsaAD, nil)
	env.OnActivity(adReused.CreateOrModifyADDNS, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(adReused.GetOrCreateCifsService, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&active_directory_activities.GetOrCreateCifsServiceResult{}, nil)
}

func TestDiagnoseActiveDirectoryWorkflow_SuccessNoLdapNoKerberos(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(f.createResp, nil)
	defer restore()

	mockDiagnoseUpToCifs(env, f)
	mockDiagnoseCleanup(env)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	// LDAP and Kerberos are disabled for this fixture.
	env.AssertActivityNumberOfCalls(t, "ConfigureLdapForSVM", 0)
	env.AssertWorkflowNumberOfCalls(t, "EnsureKerberosConfigWorkflow", 0)
	env.AssertActivityNumberOfCalls(t, "DeleteDiagnosisKerberosConfig", 0)
	env.AssertExpectations(t)
}

func TestDiagnoseActiveDirectoryWorkflow_SuccessWithLdapAndKerberos(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	f.pool.PoolAttributes.LdapEnabled = true
	f.vsaAD.KdcIP = "10.1.2.3"
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(f.createResp, nil)
	defer restore()

	mockDiagnoseUpToCifs(env, f)
	mockDiagnoseCleanup(env)

	adReused := active_directory_activities.ActiveDirectoryActivity{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}
	env.OnActivity(adReused.ConfigureLdapForSVM, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	env.OnWorkflow(EnsureKerberosConfigWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	// The KDC branch registers a Kerberos teardown before enabling Kerberos.
	env.OnActivity(diag.DeleteDiagnosisKerberosConfig, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestDiagnoseActiveDirectoryWorkflow_ManagedADSkipsLdapAndKerberos(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	// Even with LDAP + KDC configured, Managed AD forces CIFS-only.
	f.pool.PoolAttributes.LdapEnabled = true
	f.vsaAD.KdcIP = "10.1.2.3"
	managed := true
	f.vsaAD.ManagedAD = &managed
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(f.createResp, nil)
	defer restore()

	mockDiagnoseUpToCifs(env, f)
	mockDiagnoseCleanup(env)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertActivityNumberOfCalls(t, "ConfigureLdapForSVM", 0)
	env.AssertWorkflowNumberOfCalls(t, "EnsureKerberosConfigWorkflow", 0)
	env.AssertActivityNumberOfCalls(t, "DeleteDiagnosisKerberosConfig", 0)
	env.AssertExpectations(t)
}

func TestDiagnoseActiveDirectoryWorkflow_LoadPoolFails(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(f.createResp, nil)
	defer restore()

	commonActivity := activities.CommonActivities{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(f.job, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(diag.LoadPoolForDiagnosis, mock.Anything, mock.Anything).Return((*datamodel.Pool)(nil), errors.New("pool load failed"))
	// Nothing was registered on the cleanup manager yet, so only the AD state
	// restore runs.
	mockDiagnoseRestoreOnly(env)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNumberOfCalls(t, "SoftDeleteDiagnosisSVMFromDB", 0)
	env.AssertWorkflowNumberOfCalls(t, vlm.DeleteVSASVMWorkflowName, 0)
	env.AssertExpectations(t)
}

func TestDiagnoseActiveDirectoryWorkflow_CreateVSASVMFails(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(nil, errors.New("vlm create failed"))
	defer restore()

	commonActivity := activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(f.job, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(diag.LoadPoolForDiagnosis, mock.Anything, mock.Anything).Return(f.pool, nil)
	env.OnActivity(diag.GetActiveDirectoryRecordForPool, mock.Anything, mock.Anything).Return(f.adRecord, nil)
	env.OnActivity(diag.GetNodeForDiagnosis, mock.Anything, mock.Anything).Return(f.node, nil)
	env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).Return(f.vlmConfig, nil)
	env.OnActivity(poolActivity.CreateOnTapCredentials, mock.Anything, mock.Anything).Return(f.creds, nil)
	env.OnActivity(diag.PrepareCreateDiagnosisSVMRequest, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.createReq, nil)
	// The VLM SVM was never created, so no VLM delete is registered.
	mockDiagnoseRestoreOnly(env)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNumberOfCalls(t, "SoftDeleteDiagnosisSVMFromDB", 0)
	env.AssertWorkflowNumberOfCalls(t, vlm.DeleteVSASVMWorkflowName, 0)
	env.AssertExpectations(t)
}

// mockDiagnoseJobScaffolding stubs only the job-state activities the workflow
// runs before Run derives the AD-diagnostics ActivityOptions.
func mockDiagnoseJobScaffolding(env *testsuite.TestWorkflowEnvironment, f *diagnoseWFFixture) {
	commonActivity := activities.CommonActivities{}
	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(f.job, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
}

func TestDiagnoseActiveDirectoryWorkflow_InvalidDBTimeoutFails(t *testing.T) {
	orig := DiagnoseDBActivityStartToCloseTimeout
	DiagnoseDBActivityStartToCloseTimeout = "invalid-duration"
	defer func() { DiagnoseDBActivityStartToCloseTimeout = orig }()

	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)
	mockDiagnoseJobScaffolding(env, f)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	// Run returns before the deferred cleanup is installed, so no teardown runs
	// at all — not even the AD state restore.
	assertNoDiagnoseCleanup(t, env)
}

func TestDiagnoseActiveDirectoryWorkflow_InvalidOntapTimeoutFails(t *testing.T) {
	orig := DiagnoseOntapActivityStartToCloseTimeout
	DiagnoseOntapActivityStartToCloseTimeout = "invalid-duration"
	defer func() { DiagnoseOntapActivityStartToCloseTimeout = orig }()

	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)
	mockDiagnoseJobScaffolding(env, f)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assertNoDiagnoseCleanup(t, env)
}

func TestDiagnoseActiveDirectoryWorkflow_CifsJoinFailsRunsCleanup(t *testing.T) {
	env := newDiagnoseWFEnv(t)
	f := newDiagnoseWFFixture()
	registerDiagnoseActivities(env)

	restore := withMockVSAManager(f.createResp, nil)
	defer restore()

	commonActivity := activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	adReused := active_directory_activities.ActiveDirectoryActivity{}
	diag := &active_directory_activities.ActiveDirectoryDiagnoseActivity{}

	env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(f.job, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(diag.LoadPoolForDiagnosis, mock.Anything, mock.Anything).Return(f.pool, nil)
	env.OnActivity(diag.GetActiveDirectoryRecordForPool, mock.Anything, mock.Anything).Return(f.adRecord, nil)
	env.OnActivity(diag.GetNodeForDiagnosis, mock.Anything, mock.Anything).Return(f.node, nil)
	env.OnActivity(poolActivity.ParseVlmConfig, mock.Anything, mock.Anything).Return(f.vlmConfig, nil)
	env.OnActivity(poolActivity.CreateOnTapCredentials, mock.Anything, mock.Anything).Return(f.creds, nil)
	env.OnActivity(diag.PrepareCreateDiagnosisSVMRequest, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.createReq, nil)
	env.OnActivity(diag.SaveDiagnosisSVMToDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.diagnosisSVMRow, nil)
	env.OnActivity(diag.GetIlbNasLifHomeNodeForDiagnosis, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(f.node, nil)
	env.OnActivity(diag.SetIlbNasLifServicePolicy, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(adReused.GetActiveDirectoryForPool, mock.Anything, mock.Anything).Return(f.vsaAD, nil)
	env.OnActivity(adReused.CreateOrModifyADDNS, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(adReused.GetOrCreateCifsService, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return((*active_directory_activities.GetOrCreateCifsServiceResult)(nil), errors.New("domain join failed"))
	// The CIFS unbind is registered before the join is attempted, so the full
	// teardown (minus Kerberos) still runs on this failure.
	mockDiagnoseCleanup(env)

	env.ExecuteWorkflow(DiagnoseActiveDirectoryWorkflow, f.params, f.adModel)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertActivityNumberOfCalls(t, "ConfigureLdapForSVM", 0)
	env.AssertActivityNumberOfCalls(t, "DeleteDiagnosisKerberosConfig", 0)
	env.AssertExpectations(t)
}

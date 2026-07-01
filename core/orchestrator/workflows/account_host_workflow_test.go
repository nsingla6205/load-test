package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/testsuite"
)

const (
	testAccountHostUUID = "bf51bf57-c88b-e38c-0f9e-bc588cfe6ed2"
	testSecondHostUUID  = "0fd5305c-e724-2598-2605-2f61e716cd63"
	testHostUUID        = "9760acf5-4638-11e7-9bdb-020073ca3333"
	testPinJobUUID      = "d3456789-01ab-cdef-0123-456789abcdef"
)

func preparedMapping(accountHostUUID string) activities.PreparedAccountHostMapping {
	return activities.PreparedAccountHostMapping{
		MappingUUID:   accountHostUUID,
		HostUUID:      testHostUUID,
		RoleName:      "gcnvadmin",
		RoleOwnerUUID: "95be11a7-0000-0000-0000-000000000001",
	}
}

func TestPinAccountHostsWorkflow(t *testing.T) {
	t.Run("WhenProvisioningSucceeds", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		prepared := preparedMapping(testAccountHostUUID)
		commonActivity := &activities.CommonActivities{}
		accountHostActivity := &activities.AccountHostActivity{}

		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(accountHostActivity.PrepareAccountHostMapping)
		env.RegisterActivity(accountHostActivity.CreateAccountHostRole)
		env.RegisterActivity(accountHostActivity.CreateAccountHostUser)

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testAccountHostUUID).
			Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostRole, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostUser, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(nil).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStateDONE)
		})).Return(nil).Once()

		env.ExecuteWorkflow(PinAccountHostsWorkflow, &orchcommon.PinAccountHostsWorkflowParams{
			JobUUID:          testPinJobUUID,
			AccountName:        "771149402920",
			AccountHostUUIDs: []string{testAccountHostUUID},
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("WhenPartialFailureMarksJobError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		prepared := preparedMapping(testAccountHostUUID)
		commonActivity := &activities.CommonActivities{}
		accountHostActivity := &activities.AccountHostActivity{}

		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(accountHostActivity.PrepareAccountHostMapping)
		env.RegisterActivity(accountHostActivity.CreateAccountHostRole)
		env.RegisterActivity(accountHostActivity.CreateAccountHostUser)

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testAccountHostUUID).
			Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostRole, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostUser, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(nil).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testSecondHostUUID).
			Return(nil, errors.New("prepare failed")).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID &&
				job.State == string(datamodel.JobsStateERROR) &&
				job.ErrorDetails == "1 of 2 account host(s) failed to provision ONTAP credentials"
		})).Return(nil).Once()

		env.ExecuteWorkflow(PinAccountHostsWorkflow, &orchcommon.PinAccountHostsWorkflowParams{
			JobUUID:          testPinJobUUID,
			AccountName:        "771149402920",
			AccountHostUUIDs: []string{testAccountHostUUID, testSecondHostUUID},
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("WhenAllHostsFailSetsErrorDetails", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		commonActivity := &activities.CommonActivities{}
		accountHostActivity := &activities.AccountHostActivity{}

		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(accountHostActivity.PrepareAccountHostMapping)

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testAccountHostUUID).
			Return(nil, errors.New("prepare failed")).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testSecondHostUUID).
			Return(nil, errors.New("prepare failed again")).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID &&
				job.State == string(datamodel.JobsStateERROR) &&
				job.ErrorDetails == "2 of 2 account host(s) failed to provision ONTAP credentials"
		})).Return(nil).Once()

		env.ExecuteWorkflow(PinAccountHostsWorkflow, &orchcommon.PinAccountHostsWorkflowParams{
			JobUUID:          testPinJobUUID,
			AccountName:        "771149402920",
			AccountHostUUIDs: []string{testAccountHostUUID, testSecondHostUUID},
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("WhenUserCreateFailureRollsBackOntapRole", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()

		prepared := preparedMapping(testAccountHostUUID)
		prepared.AdminUsername = "gcnv_u_abcdefghij"

		commonActivity := &activities.CommonActivities{}
		accountHostActivity := &activities.AccountHostActivity{}

		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(accountHostActivity.PrepareAccountHostMapping)
		env.RegisterActivity(accountHostActivity.CreateAccountHostRole)
		env.RegisterActivity(accountHostActivity.CreateAccountHostUser)
		env.RegisterActivity(accountHostActivity.RollbackAccountHostOntapProvisioning)

		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil).Once()
		env.OnActivity(accountHostActivity.PrepareAccountHostMapping, mock.Anything, testAccountHostUUID).
			Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostRole, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(&prepared, nil).Once()
		env.OnActivity(accountHostActivity.CreateAccountHostUser, mock.Anything, mock.MatchedBy(func(p *activities.PreparedAccountHostMapping) bool {
			return p != nil && p.MappingUUID == testAccountHostUUID
		})).Return(errors.New("user create failed")).Once()
		env.OnActivity(accountHostActivity.RollbackAccountHostOntapProvisioning, mock.Anything, &prepared).
			Return(nil).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.UUID == testPinJobUUID && job.State == string(datamodel.JobsStateERROR)
		})).Return(nil).Once()

		env.ExecuteWorkflow(PinAccountHostsWorkflow, &orchcommon.PinAccountHostsWorkflowParams{
			JobUUID:          testPinJobUUID,
			AccountName:        "771149402920",
			AccountHostUUIDs: []string{testAccountHostUUID},
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
	})

	t.Run("WhenParamsNil", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.ExecuteWorkflow(PinAccountHostsWorkflow, nil)
		require.True(t, env.IsWorkflowCompleted())
		require.Error(t, env.GetWorkflowError())
	})

	t.Run("WhenAccountHostUUIDsEmpty", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.ExecuteWorkflow(PinAccountHostsWorkflow, &orchcommon.PinAccountHostsWorkflowParams{})
		require.True(t, env.IsWorkflowCompleted())
		require.Error(t, env.GetWorkflowError())
	})
}

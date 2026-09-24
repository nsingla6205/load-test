package backgroundworkflows

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/testsuite"
)

type ForcePoolCertificateRotationWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	s.env.RegisterWorkflow(ForceRotatePoolCertificateWorkflow)
	s.env.RegisterWorkflow(RotatePoolPasswordWorkflow)

	rotateCertificateActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	s.env.RegisterActivity(rotateCertificateActivity.GetPoolContext)
	s.env.RegisterActivity(rotateCertificateActivity.CertificateNeedsRotation)
	s.env.RegisterActivity(rotateCertificateActivity.RotatePoolCertificateWithContext)

	controlWorkflowActivity := &backgroundactivities.ControlWorkflowActivity{}
	s.env.RegisterActivity(controlWorkflowActivity.ExecutePoolForcedCertificateRotationSequentially)
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// setRotationFlags applies the forced-rotation gate flags for the duration of one test.
func (s *ForcePoolCertificateRotationWorkflowTestSuite) setRotationFlags(forced, rotation string) {
	for name, value := range map[string]string{
		"ENABLE_FORCED_CERTIFICATE_ROTATION": forced,
		"ENABLE_VSA_CERTIFICATE_ROTATION":    rotation,
	} {
		original, existed := os.LookupEnv(name)
		s.Require().NoError(os.Setenv(name, value))
		s.T().Cleanup(func() {
			if existed {
				_ = os.Setenv(name, original)
				return
			}
			_ = os.Unsetenv(name)
		})
	}
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestQueuesRotationWhenEnabled() {
	s.setRotationFlags("true", "true")
	poolUUID := "test-pool-uuid"

	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolForcedCertificateRotationSequentially,
		mock.Anything, poolUUID, mock.Anything).Return(nil).Once()

	s.env.ExecuteWorkflow(ForcePoolCertificateRotationWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestRejectedWhenForcedRotationDisabled() {
	s.setRotationFlags("false", "true")

	s.env.ExecuteWorkflow(ForcePoolCertificateRotationWorkflow, "test-pool-uuid")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "ENABLE_FORCED_CERTIFICATE_ROTATION=false")
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestRejectedWhenCertificateRotationDisabled() {
	s.setRotationFlags("true", "false")

	s.env.ExecuteWorkflow(ForcePoolCertificateRotationWorkflow, "test-pool-uuid")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "ENABLE_VSA_CERTIFICATE_ROTATION=false")
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestRejectsEmptyPoolUUID() {
	s.setRotationFlags("true", "true")

	s.env.ExecuteWorkflow(ForcePoolCertificateRotationWorkflow, "   ")

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "pool UUID is required")
}

func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestPropagatesQueueFailure() {
	s.setRotationFlags("true", "true")
	poolUUID := "test-pool-uuid"

	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolForcedCertificateRotationSequentially,
		mock.Anything, poolUUID, mock.Anything).Return(errors.New("queue failed"))

	s.env.ExecuteWorkflow(ForcePoolCertificateRotationWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// newTestPoolContext builds a pool context with the given force flag. The forced
// workflow must hand the activity a context with Force set, because the activity
// re-derives rotation eligibility itself; a workflow-only skip would leave the
// activity to decide the certificate is current and quietly do nothing.
func newTestPoolContext(poolUUID string, forceCertRotationOnly bool) *backgroundactivities.PoolContext {
	return &backgroundactivities.PoolContext{
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: poolUUID},
			Name:           "test-pool",
			State:          "READY",
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
				SecretID:      "secret-1",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
		PoolUUID: poolUUID,
		Force:    forceCertRotationOnly,
	}
}

// TestForcedChildSkipsEligibilityCheckAndPasswordRotation proves the forced child workflow
// rotates without consulting CertificateNeedsRotation. That activity and
// RotatePoolPasswordWorkflow are deliberately left unmocked: if the forced path called
// either one, the test environment would fail instead of completing cleanly.
func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestForcedChildSkipsEligibilityCheckAndPasswordRotation() {
	poolUUID := "test-pool-uuid"

	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).GetPoolContext,
		mock.Anything, poolUUID).Return(newTestPoolContext(poolUUID, false), nil).Once()
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).RotatePoolCertificateWithContext,
		mock.Anything, newTestPoolContext(poolUUID, true)).Return(nil).Once()

	s.env.ExecuteWorkflow(ForceRotatePoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestScheduledChildDoesNotSetForce guards the boundary: the scheduled path must keep
// handing the activity Force=false so it applies its normal eligibility rules.
func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestScheduledChildDoesNotSetForce() {
	poolUUID := "test-pool-uuid"

	s.env.RegisterWorkflow(RotatePoolCertificateWorkflow)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).GetPoolContext,
		mock.Anything, poolUUID).Return(newTestPoolContext(poolUUID, false), nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).CertificateNeedsRotation,
		mock.Anything, poolUUID).Return(true, nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).RotatePoolCertificateWithContext,
		mock.Anything, newTestPoolContext(poolUUID, false)).Return(nil).Once()
	s.env.OnWorkflow(RotatePoolPasswordWorkflow, mock.Anything, poolUUID).Return(nil)

	s.env.ExecuteWorkflow(RotatePoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestForcedChildFailsOnRotationError checks that a forced run surfaces the rotation
// failure instead of swallowing it the way the scheduled path does.
func (s *ForcePoolCertificateRotationWorkflowTestSuite) TestForcedChildFailsOnRotationError() {
	poolUUID := "test-pool-uuid"

	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).GetPoolContext,
		mock.Anything, poolUUID).Return(newTestPoolContext(poolUUID, false), nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).RotatePoolCertificateWithContext,
		mock.Anything, newTestPoolContext(poolUUID, true)).Return(errors.New("certificate rotation failed"))

	s.env.ExecuteWorkflow(ForceRotatePoolCertificateWorkflow, poolUUID)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "certificate rotation failed")
}

func TestForcePoolCertificateRotationWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(ForcePoolCertificateRotationWorkflowTestSuite))
}

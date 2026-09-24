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

type CertificateRotationWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *CertificateRotationWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	// Register child workflows
	s.env.RegisterWorkflow(RotatePoolCertificateWorkflow)
	s.env.RegisterWorkflow(RotatePoolPasswordWorkflow)

	// Register activities
	rotateCertificateActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	s.env.RegisterActivity(rotateCertificateActivity.ListPoolsWithCertificateAuth)
	s.env.RegisterActivity(rotateCertificateActivity.ListPoolsWithPasswordAuth)
	s.env.RegisterActivity(rotateCertificateActivity.GetPoolContext)
	s.env.RegisterActivity(rotateCertificateActivity.CertificateNeedsRotation)
	s.env.RegisterActivity(rotateCertificateActivity.RotatePoolCertificateWithContext)
	s.env.RegisterActivity(rotateCertificateActivity.RotatePoolPasswordWithContext)

	// Register control workflow activities
	controlWorkflowActivity := &backgroundactivities.ControlWorkflowActivity{}
	s.env.RegisterActivity(controlWorkflowActivity.ExecutePoolCertificateRotationSequentially)
	s.env.RegisterActivity(controlWorkflowActivity.ExecutePoolPasswordRotationSequentially)
}

func (s *CertificateRotationWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// setEnvVarsForTest sets environment variables for testing and returns a cleanup function
func (s *CertificateRotationWorkflowTestSuite) setEnvVarsForTest(certEnabled, passwordEnabled, authType1Enabled bool) func() {
	// Store original values
	originalCert := os.Getenv("ENABLE_VSA_CERTIFICATE_ROTATION")
	originalPassword := os.Getenv("ENABLE_VSA_PASSWORD_ROTATION")
	originalAuthType1 := os.Getenv("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION")

	// Set test values
	if certEnabled {
		if err := os.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "true"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_CERTIFICATE_ROTATION: %v", err)
		}
	} else {
		if err := os.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", "false"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_CERTIFICATE_ROTATION: %v", err)
		}
	}

	if passwordEnabled {
		if err := os.Setenv("ENABLE_VSA_PASSWORD_ROTATION", "true"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_PASSWORD_ROTATION: %v", err)
		}
	} else {
		if err := os.Setenv("ENABLE_VSA_PASSWORD_ROTATION", "false"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_PASSWORD_ROTATION: %v", err)
		}
	}

	if authType1Enabled {
		if err := os.Setenv("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION", "true"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION: %v", err)
		}
	} else {
		if err := os.Setenv("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION", "false"); err != nil {
			s.T().Fatalf("Failed to set ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION: %v", err)
		}
	}

	// Return cleanup function
	return func() {
		if originalCert != "" {
			if err := os.Setenv("ENABLE_VSA_CERTIFICATE_ROTATION", originalCert); err != nil {
				s.T().Errorf("Failed to restore ENABLE_VSA_CERTIFICATE_ROTATION: %v", err)
			}
		} else {
			if err := os.Unsetenv("ENABLE_VSA_CERTIFICATE_ROTATION"); err != nil {
				s.T().Errorf("Failed to unset ENABLE_VSA_CERTIFICATE_ROTATION: %v", err)
			}
		}
		if originalPassword != "" {
			if err := os.Setenv("ENABLE_VSA_PASSWORD_ROTATION", originalPassword); err != nil {
				s.T().Errorf("Failed to restore ENABLE_VSA_PASSWORD_ROTATION: %v", err)
			}
		} else {
			if err := os.Unsetenv("ENABLE_VSA_PASSWORD_ROTATION"); err != nil {
				s.T().Errorf("Failed to unset ENABLE_VSA_PASSWORD_ROTATION: %v", err)
			}
		}
		if originalAuthType1 != "" {
			if err := os.Setenv("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION", originalAuthType1); err != nil {
				s.T().Errorf("Failed to restore ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION: %v", err)
			}
		} else {
			if err := os.Unsetenv("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION"); err != nil {
				s.T().Errorf("Failed to unset ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION: %v", err)
			}
		}
	}
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_Success() {
	// Mock pools with certificate authentication
	pools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "pool-1"},
			DeploymentName: "test-deployment-1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "pool-2"},
			DeploymentName: "test-deployment-2",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-2",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}

	// Set up activity mocks for pool listing
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil)

	// Mock control workflow activities - these should return nil to indicate success
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "pool-1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "pool-2", mock.Anything).Return(nil)

	// Set environment variables for test
	cleanup := s.setEnvVarsForTest(true, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_Disabled() {
	// Set environment variables for test - all disabled
	cleanup := s.setEnvVarsForTest(false, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_NoPools() {
	// Return empty pool list
	var pools []*datamodel.Pool

	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil)

	// Set environment variables for test
	cleanup := s.setEnvVarsForTest(true, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PartialFailure() {
	// Mock pools with certificate authentication
	pools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "pool-1"},
			DeploymentName: "test-deployment-1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "pool-2"},
			DeploymentName: "test-deployment-2",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-2",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}

	// Set up activity mocks for pool listing
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil)

	// Mock control workflow activities - one success, one failure to queue
	// Note: Parent workflow should succeed even if some child workflows fail to queue,
	// as long as it successfully triggered refresh for all pools it could
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "pool-1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "pool-2", mock.Anything).Return(errors.New("failed to queue certificate rotation"))

	// Set environment variables for test
	cleanup := s.setEnvVarsForTest(true, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	// Parent workflow should succeed even if some child workflows fail to queue
	// Individual pool failures are tracked at the child workflow level
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_WithPasswordRotation() {
	// Mock pools with certificate authentication (auth type 2)
	pools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "pool-1"},
			DeploymentName: "test-deployment-1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
				SecretID:      "secret-1",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}

	// Set up activity mocks for pool listing
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: pools, HasMore: false}, nil)

	// Mock control workflow activities
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "pool-1", mock.Anything).Return(nil)

	// Set environment variables for test
	cleanup := s.setEnvVarsForTest(true, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_ParallelExecution() {
	// Mock pools with certificate authentication (auth type 2)
	certPools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "cert-pool-1"},
			DeploymentName: "test-deployment-cert-1",
			PoolCredentials: &datamodel.PoolCredentials{
				CertificateID: "cert-1",
				AuthType:      env.USER_CERTIFICATE,
			},
		},
	}

	// Mock pools with password authentication (auth type 1)
	passwordPools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "password-pool-1"},
			DeploymentName: "test-deployment-password-1",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID: "secret-1",
				AuthType: env.USERNAME_PWD_SEC_MGR,
			},
		},
	}

	// Set up activity mocks for parallel pool listing
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: certPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: passwordPools, HasMore: false}, nil)

	// Mock control workflow activities
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "cert-pool-1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "password-pool-1", mock.Anything).Return(nil)

	// Set environment variables for test - all enabled
	cleanup := s.setEnvVarsForTest(true, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PasswordRotationOnly() {
	// Mock pools with password authentication (auth type 1)
	passwordPools := []*datamodel.Pool{
		{
			BaseModel:      datamodel.BaseModel{UUID: "password-pool-1"},
			DeploymentName: "test-deployment-password-1",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID: "secret-1",
				AuthType: env.USERNAME_PWD_SEC_MGR,
			},
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "password-pool-2"},
			DeploymentName: "test-deployment-password-2",
			PoolCredentials: &datamodel.PoolCredentials{
				SecretID: "secret-2",
				AuthType: env.USERNAME_PWD_SEC_MGR,
			},
		},
	}

	// Set up activity mocks for password pool listing
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: passwordPools, HasMore: false}, nil)

	// Mock control workflow activities
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "password-pool-1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "password-pool-2", mock.Anything).Return(nil)

	// Set environment variables for test - only password rotation enabled
	cleanup := s.setEnvVarsForTest(false, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PoolListingFailure() {
	// Mock pool listing failure
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to list certificate pools"))

	// Set environment variables for test
	cleanup := s.setEnvVarsForTest(true, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_EnvironmentVariables() {
	// Test different environment variable combinations

	// Test case 1: All disabled
	cleanup := s.setEnvVarsForTest(false, false, false)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_PasswordListFails covers lines 153-154 (return on ListPoolsWithPasswordAuth error).
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PasswordListFails() {
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to list password pools"))

	cleanup := s.setEnvVarsForTest(false, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_PasswordRotationPartialFailure covers lines 169-170, 186-187, 202, 204.
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PasswordRotationPartialFailure() {
	passwordPools := []*datamodel.Pool{
		{
			BaseModel:       datamodel.BaseModel{UUID: "pw-pool-1"},
			PoolCredentials: &datamodel.PoolCredentials{SecretID: "secret-1", AuthType: env.USERNAME_PWD_SEC_MGR},
		},
		{
			BaseModel:       datamodel.BaseModel{UUID: "pw-pool-2"},
			PoolCredentials: &datamodel.PoolCredentials{SecretID: "secret-2", AuthType: env.USERNAME_PWD_SEC_MGR},
		},
	}
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: passwordPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "pw-pool-1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "pw-pool-2", mock.Anything).Return(errors.New("failed to queue"))

	cleanup := s.setEnvVarsForTest(false, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_PasswordRotationDisabled covers line 184 (logger when password rotation disabled).
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_PasswordRotationDisabled() {
	cleanup := s.setEnvVarsForTest(false, false, true) // cert off, password off, authType1 on
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_AuthType1PasswordRotationDisabled covers lines 186-187.
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_AuthType1PasswordRotationDisabled() {
	cleanup := s.setEnvVarsForTest(false, true, false) // password on, authType1 off
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_BothRotationsPartialFailure covers lines 197-198, 202, 204 (summary warnings).
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_BothRotationsPartialFailure() {
	certPools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "c1"}, PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1", AuthType: env.USER_CERTIFICATE}},
	}
	passwordPools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "p1"}, PoolCredentials: &datamodel.PoolCredentials{SecretID: "s1", AuthType: env.USERNAME_PWD_SEC_MGR}},
	}
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: certPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: passwordPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "c1", mock.Anything).Return(errors.New("cert queue failed"))
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "p1", mock.Anything).Return(errors.New("password queue failed"))

	cleanup := s.setEnvVarsForTest(true, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestRotateVcpToVsaCertificatesWorkflow_BothSucceedWithPools covers lines 211, 214 (success summary when no failures).
func (s *CertificateRotationWorkflowTestSuite) TestRotateVcpToVsaCertificatesWorkflow_BothSucceedWithPools() {
	certPools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "c1"}, PoolCredentials: &datamodel.PoolCredentials{CertificateID: "cert-1", AuthType: env.USER_CERTIFICATE}},
	}
	passwordPools := []*datamodel.Pool{
		{BaseModel: datamodel.BaseModel{UUID: "p1"}, PoolCredentials: &datamodel.PoolCredentials{SecretID: "s1", AuthType: env.USERNAME_PWD_SEC_MGR}},
	}
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithCertificateAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: certPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.RotateVcpToVsaCertificateActivity{}).ListPoolsWithPasswordAuth, mock.Anything, mock.Anything, mock.Anything).
		Return(&backgroundactivities.ListPoolsBatchResult{Pools: passwordPools, HasMore: false}, nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolCertificateRotationSequentially, mock.Anything, "c1", mock.Anything).Return(nil)
	s.env.OnActivity((&backgroundactivities.ControlWorkflowActivity{}).ExecutePoolPasswordRotationSequentially, mock.Anything, "p1", mock.Anything).Return(nil)

	cleanup := s.setEnvVarsForTest(true, true, true)
	defer cleanup()

	s.env.ExecuteWorkflow(RotateVsaCertificateAndPasswordWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func TestCertificateRotationWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(CertificateRotationWorkflowTestSuite))
}

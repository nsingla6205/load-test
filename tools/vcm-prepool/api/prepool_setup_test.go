package api

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/prepoolinfra"
)

type stubProvisioner struct{}

func (stubProvisioner) Setup(_ context.Context, _, _, _, _ string, _ *prepoolinfra.SpannerDBConfig, _ prepoolinfra.SetupOptions) (prepoolinfra.PrePoolInfra, error) {
	return prepoolinfra.PrePoolInfra{}, nil
}

func (stubProvisioner) Reclaim(context.Context, string, string, string, string) error {
	return nil
}

func (stubProvisioner) ReclaimAll(context.Context, string, string, string, string) error {
	return nil
}

func (stubProvisioner) DeleteVSACluster(context.Context, string, string, string, string) error {
	return nil
}

func (stubProvisioner) DeleteJumpHost(context.Context, string, string, string) error {
	return nil
}

func TestHandler_RunJobs_CreatesFreshProvisionerForEachJob(t *testing.T) {
	var factoryCalls atomic.Int32
	handler := &Handler{
		Context: context.Background(),
		ProvisionerFactory: func(context.Context) (prepoolinfra.Provisioner, error) {
			factoryCalls.Add(1)
			return stubProvisioner{}, nil
		},
		Jobs: NewJobStore(),
	}

	setupRequest := PrepoolSetupRequest{DeploymentName: "setup"}
	setupJob, created := handler.Jobs.CreateIfNoActive(JobOperationSetup, setupRequest)
	require.True(t, created)
	handler.runSetupJob(setupJob.ID, setupRequest)

	reclaimRequest := PrepoolSetupRequest{DeploymentName: "reclaim"}
	reclaimJob, created := handler.Jobs.CreateIfNoActive(JobOperationReclaimDeployment, reclaimRequest)
	require.True(t, created)
	handler.runReclaimDeploymentJob(reclaimJob.ID, reclaimRequest)

	deleteRequest := PrepoolSetupRequest{DeploymentName: "delete"}
	deleteJob, created := handler.Jobs.CreateIfNoActive(JobOperationDeleteVSACluster, deleteRequest)
	require.True(t, created)
	handler.runDeleteVSAClusterJob(deleteJob.ID, deleteRequest)

	reclaimAllRequest := PrepoolSetupRequest{DeploymentName: "reclaim-all"}
	reclaimAllJob, created := handler.Jobs.CreateIfNoActive(JobOperationReclaimAll, reclaimAllRequest)
	require.True(t, created)
	handler.runReclaimAllJob(reclaimAllJob.ID, reclaimAllRequest)

	deleteJHRequest := PrepoolSetupRequest{DeploymentName: "delete-jh"}
	deleteJHJob, created := handler.Jobs.CreateIfNoActive(JobOperationDeleteJumpHost, deleteJHRequest)
	require.True(t, created)
	handler.runDeleteJumpHostJob(deleteJHJob.ID, deleteJHRequest)

	assert.Equal(t, int32(5), factoryCalls.Load())
	assert.Equal(t, JobStateDone, handler.Jobs.Get(setupJob.ID).State)
	assert.Equal(t, JobStateDone, handler.Jobs.Get(reclaimJob.ID).State)
	assert.Equal(t, JobStateDone, handler.Jobs.Get(deleteJob.ID).State)
	assert.Equal(t, JobStateDone, handler.Jobs.Get(reclaimAllJob.ID).State)
	assert.Equal(t, JobStateDone, handler.Jobs.Get(deleteJHJob.ID).State)
}

func Test_PrepoolSetupRequest_CreateJumpHost_DefaultsFalse(t *testing.T) {
	var req PrepoolSetupRequest
	assert.False(t, req.CreateJumpHost, "createJumpHost should default to false (zero-value)")
}

func Test_ValidatePrepoolSetupRequest_Inputs_ReturnExpectedResult(t *testing.T) {
	validRequest := PrepoolSetupRequest{
		DeploymentName:        "gcnv-8d0ff6a0b456f30",
		ConsumerNetwork:       "projects/customer-project/global/networks/customer-network",
		CustomerProjectNumber: "123456789012",
	}
	tests := []struct {
		name      string
		mutate    func(*PrepoolSetupRequest)
		wantError string
	}{
		{name: "valid request"},
		{
			name:      "invalid deployment",
			mutate:    func(req *PrepoolSetupRequest) { req.DeploymentName = "../deployment" },
			wantError: "deploymentId",
		},
		{
			name:      "invalid project number",
			mutate:    func(req *PrepoolSetupRequest) { req.CustomerProjectNumber = "project-name" },
			wantError: "projectId",
		},
		{
			name: "deployment too long",
			mutate: func(req *PrepoolSetupRequest) {
				req.DeploymentName = "gcnv-" + strings.Repeat("a", 16)
			},
			wantError: "1-20",
		},
		{
			name:      "invalid network path",
			mutate:    func(req *PrepoolSetupRequest) { req.ConsumerNetwork = "../../network" },
			wantError: "network must use",
		},
		{
			name: "invalid network project",
			mutate: func(req *PrepoolSetupRequest) {
				req.ConsumerNetwork = "projects/INVALID/global/networks/customer-network"
			},
			wantError: "project identifier",
		},
		{
			name: "invalid network name",
			mutate: func(req *PrepoolSetupRequest) {
				req.ConsumerNetwork = "projects/customer-project/global/networks/INVALID"
			},
			wantError: "network name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest
			if test.mutate != nil {
				test.mutate(&request)
			}

			err := validatePrepoolSetupRequest(request)

			if test.wantError == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, test.wantError)
		})
	}
}

func Test_ValidateDeleteJumpHostRequest_Inputs_ReturnExpectedResult(t *testing.T) {
	validRequest := PrepoolSetupRequest{
		ConsumerNetwork:       "projects/customer-project/global/networks/customer-network",
		CustomerProjectNumber: "123456789012",
	}
	tests := []struct {
		name      string
		mutate    func(*PrepoolSetupRequest)
		wantError string
	}{
		{name: "missing deployment id"},
		{
			name:      "optional valid deployment id",
			mutate:    func(req *PrepoolSetupRequest) { req.DeploymentName = "gcnv-8d0ff6a0b456f30" },
		},
		{
			name:      "invalid deployment id",
			mutate:    func(req *PrepoolSetupRequest) { req.DeploymentName = "../deployment" },
			wantError: "deploymentId",
		},
		{
			name:      "invalid project number",
			mutate:    func(req *PrepoolSetupRequest) { req.CustomerProjectNumber = "project-name" },
			wantError: "projectId",
		},
		{
			name:      "invalid network path",
			mutate:    func(req *PrepoolSetupRequest) { req.ConsumerNetwork = "../../network" },
			wantError: "network must use",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest
			if test.mutate != nil {
				test.mutate(&request)
			}

			err := validateDeleteJumpHostRequest(request)

			if test.wantError == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestValidateSpannerEmulatorDBConfig_Inputs_ReturnExpectedResult(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")

	validConfig := &prepoolinfra.SpannerDBConfig{
		Project:  "test-project",
		Instance: "vcm-local-test",
		Database: "vcm",
	}
	tests := []struct {
		name      string
		config    *prepoolinfra.SpannerDBConfig
		mutate    func(*prepoolinfra.SpannerDBConfig)
		wantError string
	}{
		{name: "valid config", config: validConfig},
		{name: "missing config", wantError: "dbConfig is required"},
		{
			name:      "invalid project",
			config:    validConfig,
			mutate:    func(config *prepoolinfra.SpannerDBConfig) { config.Project = "../project" },
			wantError: "dbConfig.project",
		},
		{
			name:      "invalid instance",
			config:    validConfig,
			mutate:    func(config *prepoolinfra.SpannerDBConfig) { config.Instance = "1invalid" },
			wantError: "dbConfig.instance",
		},
		{
			name:      "invalid database",
			config:    validConfig,
			mutate:    func(config *prepoolinfra.SpannerDBConfig) { config.Database = "A" },
			wantError: "dbConfig.database",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			if config != nil {
				configCopy := *config
				config = &configCopy
			}
			if test.mutate != nil {
				test.mutate(config)
			}

			err := validateSpannerEmulatorDBConfig(config)

			if test.wantError == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, test.wantError)
		})
	}
}

func TestValidateSpannerEmulatorDBConfig_EmulatorDisabled_AllowsMissingConfig(t *testing.T) {
	t.Setenv("SPANNER_EMULATOR_HOST", "")

	assert.NoError(t, validateSpannerEmulatorDBConfig(nil))
}

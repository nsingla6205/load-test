package vlm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newVLMMaintenanceEnv() *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{
		"requestCorrelationID": "test-correlation-id",
	})
	env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})
	return env
}

func validMaintenanceRequest() *MaintenanceRequest {
	cfg := VLMConfig{}
	cfg.Deployment.DeploymentID = "dep-1"
	cfg.Deployment.Labels = map[string]string{accountIDLabelKey: "acct-1"}
	return &MaintenanceRequest{
		VLMConfig:       cfg,
		MaintenanceType: MaintenanceTypeRebootMigrate,
		Targets:         []MaintenanceTarget{{HaPairIndex: 0, NodeNames: []string{"node1"}}},
	}
}

func validMediatorMaintenanceRequest() *MediatorMaintenanceRequest {
	cfg := VLMConfig{}
	cfg.Deployment.DeploymentID = "dep-1"
	cfg.Deployment.Labels = map[string]string{accountIDLabelKey: "acct-1"}
	return &MediatorMaintenanceRequest{
		VLMConfig:     cfg,
		HAPairIndices: []int{1},
	}
}

func TestMaintenanceWorkflow_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(req *MaintenanceRequest) *MaintenanceRequest
	}{
		{name: "nil request", mutate: func(_ *MaintenanceRequest) *MaintenanceRequest { return nil }},
		{name: "empty deployment ID", mutate: func(req *MaintenanceRequest) *MaintenanceRequest {
			req.VLMConfig.Deployment.DeploymentID = ""
			return req
		}},
		{name: "no targets", mutate: func(req *MaintenanceRequest) *MaintenanceRequest {
			req.Targets = nil
			return req
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newVLMMaintenanceEnv()
			req := c.mutate(validMaintenanceRequest())
			vlmManager := NewVSAClientWorkflowManager()

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				_, err := vlmManager.MaintenanceWorkflow(ctx, req)
				return err
			})

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
		})
	}
}

func TestMaintenanceWorkflow_Success(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MaintenanceRequest) (*MaintenanceResponse, error) {
			return &MaintenanceResponse{Results: []PairMaintenanceResult{
				{HaPairIndex: 0, NodeName: "node1", Succeeded: true},
			}, VLMConfig: VLMConfig{Deployment: DeploymentConfig{DeploymentID: "dep-1-updated"}}}, nil
		},
		workflow.RegisterOptions{Name: MaintenanceWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	var resp *MaintenanceResponse
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		var err error
		resp, err = vlmManager.MaintenanceWorkflow(ctx, validMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Len(t, resp.Results, 1)
	assert.True(t, resp.Results[0].Succeeded)
	assert.Equal(t, "dep-1-updated", resp.VLMConfig.Deployment.DeploymentID)
}

func TestMaintenanceWorkflow_ChildWorkflowError(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MaintenanceRequest) (*MaintenanceResponse, error) {
			return nil, errors.New("vlm maintenance failed")
		},
		workflow.RegisterOptions{Name: MaintenanceWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := vlmManager.MaintenanceWorkflow(ctx, validMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestVSAMediatorMaintenanceWorkflow_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest
	}{
		{name: "nil request", mutate: func(_ *MediatorMaintenanceRequest) *MediatorMaintenanceRequest { return nil }},
		{name: "empty deployment ID", mutate: func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest {
			req.VLMConfig.Deployment.DeploymentID = ""
			return req
		}},
		{name: "no HA pair indices", mutate: func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest {
			req.HAPairIndices = nil
			return req
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newVLMMaintenanceEnv()
			req := c.mutate(validMediatorMaintenanceRequest())
			vlmManager := NewVSAClientWorkflowManager()

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				_, err := vlmManager.VSAMediatorMaintenanceWorkflow(ctx, req)
				return err
			})

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
		})
	}
}

func TestVSAMediatorMaintenanceWorkflow_Success(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			updated := &VLMConfig{}
			updated.Deployment.DeploymentID = "dep-1"
			return &MediatorMaintenanceResponse{VLMConfig: *updated}, nil
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRebootMigrateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	var resp *VLMConfig
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		var err error
		resp, err = vlmManager.VSAMediatorMaintenanceWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Equal(t, "dep-1", resp.Deployment.DeploymentID)
}

func TestVSAMediatorMaintenanceWorkflow_MissingWrappedConfig(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			return &MediatorMaintenanceResponse{}, nil
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRebootMigrateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := vlmManager.VSAMediatorMaintenanceWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "VLM config parsing")
}

func TestVSAMediatorMaintenanceWorkflow_ChildWorkflowError(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			return nil, errors.New("mediator maintenance failed")
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRebootMigrateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := vlmManager.VSAMediatorMaintenanceWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestVSAMediatorITCWorkflow_ValidationErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest
	}{
		{name: "nil request", mutate: func(_ *MediatorMaintenanceRequest) *MediatorMaintenanceRequest { return nil }},
		{name: "empty deployment ID", mutate: func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest {
			req.VLMConfig.Deployment.DeploymentID = ""
			return req
		}},
		{name: "no HA pair indices", mutate: func(req *MediatorMaintenanceRequest) *MediatorMaintenanceRequest {
			req.HAPairIndices = nil
			return req
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := newVLMMaintenanceEnv()
			req := c.mutate(validMediatorMaintenanceRequest())
			vlmManager := NewVSAClientWorkflowManager()

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				_, err := vlmManager.VSAMediatorITCWorkflow(ctx, req)
				return err
			})

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
		})
	}
}

func TestVSAMediatorITCWorkflow_Success(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			updated := &VLMConfig{}
			updated.Deployment.DeploymentID = "dep-1"
			updated.Cloud.HAPairs = []HAPair{{
				Mediator: VMConfig{Name: "mediator1", VMInstanceID: "ocid1.instance..m-new"},
			}}
			return &MediatorMaintenanceResponse{VLMConfig: *updated}, nil
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRecreateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	var resp *VLMConfig
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		var err error
		resp, err = vlmManager.VSAMediatorITCWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Equal(t, "dep-1", resp.Deployment.DeploymentID)
	require.Len(t, resp.Cloud.HAPairs, 1)
	assert.Equal(t, "ocid1.instance..m-new", resp.Cloud.HAPairs[0].Mediator.VMInstanceID)
}

func TestVSAMediatorITCWorkflow_MissingWrappedConfig(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			return &MediatorMaintenanceResponse{}, nil
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRecreateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := vlmManager.VSAMediatorITCWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "VLM config parsing")
}

func TestVSAMediatorITCWorkflow_ChildWorkflowError(t *testing.T) {
	env := newVLMMaintenanceEnv()
	env.RegisterWorkflowWithOptions(
		func(_ workflow.Context, _ MediatorMaintenanceRequest) (*MediatorMaintenanceResponse, error) {
			return nil, errors.New("mediator ITC failed")
		},
		workflow.RegisterOptions{Name: MediatorMaintenanceRecreateWorkflowName},
	)

	vlmManager := NewVSAClientWorkflowManager()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		_, err := vlmManager.VSAMediatorITCWorkflow(ctx, validMediatorMaintenanceRequest())
		return err
	})

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

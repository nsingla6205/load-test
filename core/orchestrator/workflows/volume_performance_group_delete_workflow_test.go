package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func setupVPGDeleteWorkflowEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	mockStorage := database.NewMockStorage(t)
	vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
	commonActivity := activities.CommonActivities{SE: mockStorage}

	env.RegisterWorkflow(DeleteVolumePerformanceGroupWorkflow)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
	env.RegisterActivity(commonActivity.GetNode)
	env.RegisterActivity(vpgActivity.DeleteQoSPolicyInONTAP)
	env.RegisterActivity(vpgActivity.HardDeleteVPGInDB)

	return env
}

func TestDeleteVolumePerformanceGroupWorkflow(t *testing.T) {
	t.Run("FailsWhenProcessingStatusUpdateFails", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)
		commonActivity := activities.CommonActivities{}
		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(errors.New("processing status failed"))

		params := &DeleteVolumePerformanceGroupWorkflowParams{
			VPG:         &datamodel.VolumePerformanceGroup{},
			AccountName: "test-account",
		}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.ErrorContains(tt, env.GetWorkflowError(), "processing status failed")
	})

	t.Run("Success", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "test-vpg",
			PoolID:           1,
			OntapQosPolicyID: "qos-uuid",
		}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		commonActivity := activities.CommonActivities{}
		vpgActivity := activities.VolumePerformanceGroupActivity{}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(errors.New("done status failed")).Times(10)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity(vpgActivity.DeleteQoSPolicyInONTAP, mock.Anything, "qos-uuid", "test-vpg", int64(1), mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, vpg).Return(nil)

		params := &DeleteVolumePerformanceGroupWorkflowParams{VPG: vpg, AccountName: "test-account"}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("FailsWhenGetPoolViewFails", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
			PoolID:    1,
		}
		commonActivity := activities.CommonActivities{}
		vpgActivity := activities.VolumePerformanceGroupActivity{}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(nil, assert.AnError)

		params := &DeleteVolumePerformanceGroupWorkflowParams{VPG: vpg, AccountName: "test-account"}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("FailsWhenNoNodes", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
			PoolID:    1,
		}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}},
		}
		commonActivity := activities.CommonActivities{}
		vpgActivity := activities.VolumePerformanceGroupActivity{}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return([]*datamodel.Node{}, nil)

		params := &DeleteVolumePerformanceGroupWorkflowParams{VPG: vpg, AccountName: "test-account"}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("FailsWhenDeleteQoSPolicyFails", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "test-vpg",
			PoolID:           1,
			OntapQosPolicyID: "qos-uuid",
		}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		commonActivity := activities.CommonActivities{}
		vpgActivity := activities.VolumePerformanceGroupActivity{}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity(vpgActivity.DeleteQoSPolicyInONTAP, mock.Anything, "qos-uuid", "test-vpg", int64(1), mock.Anything).Return(errors.New("ontap delete failed"))

		params := &DeleteVolumePerformanceGroupWorkflowParams{VPG: vpg, AccountName: "test-account"}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("FailsWhenHardDeleteVPGFails", func(tt *testing.T) {
		env := setupVPGDeleteWorkflowEnv(tt)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "test-vpg",
			PoolID:           1,
			OntapQosPolicyID: "qos-uuid",
		}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		commonActivity := activities.CommonActivities{}
		vpgActivity := activities.VolumePerformanceGroupActivity{}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity(vpgActivity.DeleteQoSPolicyInONTAP, mock.Anything, "qos-uuid", "test-vpg", int64(1), mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.HardDeleteVPGInDB, mock.Anything, vpg).Return(errors.New("db delete failed"))

		params := &DeleteVolumePerformanceGroupWorkflowParams{VPG: vpg, AccountName: "test-account"}
		env.ExecuteWorkflow(DeleteVolumePerformanceGroupWorkflow, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})
}

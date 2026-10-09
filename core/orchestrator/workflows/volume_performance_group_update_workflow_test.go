package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestUpdateVolumePerformanceGroupWorkflow(t *testing.T) {
	t.Run("FailsWhenProcessingStatusUpdateFails", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		commonActivity := activities.CommonActivities{}
		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(errors.New("processing status failed"))

		params := &common.UpdateVolumePerformanceGroupParams{AccountName: "12345"}
		vpg := &datamodel.VolumePerformanceGroup{}
		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.ErrorContains(tt, env.GetWorkflowError(), "processing status failed")
	})

	t.Run("Success_WithNameAndThroughput", func(tt *testing.T) {
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

		mockStorage := database.NewMockStorage(tt)
		vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(vpgActivity.UpdateQoSPolicyInONTAP)
		env.RegisterActivity(vpgActivity.UpdateVPGInDB)

		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(errors.New("done status failed")).Times(10)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity(vpgActivity.UpdateQoSPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "new-name", int64(200), int64(600)).Return(nil)
		env.OnActivity(vpgActivity.UpdateVPGInDB, mock.Anything, mock.Anything).Return(nil)

		throughput := int64(200)
		iops := int64(600)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "12345",
			PoolID:                   "pool-uuid",
			VolumePerformanceGroupID: "vpg-uuid",
			Name:                     "new-name",
			ThroughputMibps:          &throughput,
			Iops:                     &iops,
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "old-name",
			OntapQosPolicyID: "old-name",
			PoolID:           1,
			ThroughputMibps:  100,
			Iops:             500,
			AllocationType:   models.AllocationTypeShared,
		}

		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ThroughputOnly_EmptyNameUsesVpgName", func(tt *testing.T) {
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

		mockStorage := database.NewMockStorage(tt)
		vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(vpgActivity.UpdateQoSPolicyInONTAP)
		env.RegisterActivity(vpgActivity.UpdateVPGInDB)

		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		// params.Name empty -> newName = vpg.Name = "current-name"; only throughput changed
		env.OnActivity(vpgActivity.UpdateQoSPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "current-name", int64(150), int64(500)).Return(nil)
		env.OnActivity(vpgActivity.UpdateVPGInDB, mock.Anything, mock.Anything).Return(nil)

		throughput := int64(150)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "12345",
			PoolID:                   "pool-uuid",
			VolumePerformanceGroupID: "vpg-uuid",
			Name:                     "", // no name change
			ThroughputMibps:          &throughput,
			Iops:                     nil, // use vpg.Iops
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "current-name",
			OntapQosPolicyID: "current-name",
			PoolID:           1,
			ThroughputMibps:  100,
			Iops:             500,
			AllocationType:   models.AllocationTypeShared,
		}

		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_WithDescriptionAndLabels", func(tt *testing.T) {
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

		mockStorage := database.NewMockStorage(tt)
		vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(vpgActivity.UpdateQoSPolicyInONTAP)
		env.RegisterActivity(vpgActivity.UpdateVPGInDB)

		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "test-deployment",
				PoolCredentials: &datamodel.PoolCredentials{Password: "pwd"},
			},
		}
		nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, EndpointAddress: "127.0.0.1"}}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return(nodes, nil)
		env.OnActivity(vpgActivity.UpdateQoSPolicyInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "current-name", int64(100), int64(500)).Return(nil)
		env.OnActivity(vpgActivity.UpdateVPGInDB, mock.Anything, mock.MatchedBy(func(v *datamodel.VolumePerformanceGroup) bool {
			return v.Description == "new description" && v.Labels != nil
		})).Return(nil)

		newDesc := "new description"
		newLabels := datamodel.JSONB{"env": "staging"}
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "12345",
			PoolID:                   "pool-uuid",
			VolumePerformanceGroupID: "vpg-uuid",
			Name:                     "",
			Description:              &newDesc,
			Labels:                   &newLabels,
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "current-name",
			OntapQosPolicyID: "current-name",
			PoolID:           1,
			ThroughputMibps:  100,
			Iops:             500,
			AllocationType:   models.AllocationTypeShared,
			Description:      "old description",
		}

		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FailsWhenGetPoolViewFails", func(tt *testing.T) {
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

		mockStorage := database.NewMockStorage(tt)
		vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(vpgActivity.UpdateQoSPolicyInONTAP)
		env.RegisterActivity(vpgActivity.UpdateVPGInDB)

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(nil, assert.AnError)

		params := &common.UpdateVolumePerformanceGroupParams{AccountName: "12345", PoolID: "pool-uuid", VolumePerformanceGroupID: "vpg-uuid"}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"}, PoolID: 1}

		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FailsWhenNoNodes", func(tt *testing.T) {
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

		mockStorage := database.NewMockStorage(tt)
		vpgActivity := activities.VolumePerformanceGroupActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}

		env.RegisterWorkflow(UpdateVolumePerformanceGroupWorkflow)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)
		env.RegisterActivity(vpgActivity.GetPoolViewByPoolID)
		env.RegisterActivity(commonActivity.GetNode)
		env.RegisterActivity(vpgActivity.UpdateQoSPolicyInONTAP)
		env.RegisterActivity(vpgActivity.UpdateVPGInDB)

		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "wf-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(vpgActivity.GetPoolViewByPoolID, mock.Anything, int64(1)).Return(poolView, nil)
		env.OnActivity(commonActivity.GetNode, mock.Anything, int64(1)).Return([]*datamodel.Node{}, nil)

		params := &common.UpdateVolumePerformanceGroupParams{AccountName: "12345", PoolID: "pool-uuid", VolumePerformanceGroupID: "vpg-uuid"}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"}, PoolID: 1}

		env.ExecuteWorkflow(UpdateVolumePerformanceGroupWorkflow, params, vpg)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		mockStorage.AssertExpectations(tt)
	})
}

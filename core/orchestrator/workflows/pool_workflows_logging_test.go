package workflows

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/temporalbackend"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type qosTransitionTestInput struct {
	Direction       string
	FailingActivity string
}

func testQosTransitionWorkflow(ctx workflow.Context, input qosTransitionTestInput) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})
	orchCtx := temporalbackend.NewTemporalContext(ctx)
	poolActivity := &activities.PoolActivity{}
	svmActivity := &activities.SvmActivity{}
	dbPool := qosTransitionTestPool()
	updateParams := &common.UpdatePoolParams{
		QosType:              utils.QosTypeManual,
		SizeInBytes:          uint64(dbPool.SizeInBytes),
		TotalThroughputMibps: dbPool.PoolAttributes.ThroughputMibps,
		TotalIops:            &dbPool.PoolAttributes.Iops,
	}

	if input.Direction == "manual_to_auto" {
		updateParams.QosType = utils.QosTypeAuto
		dbPool.QosType = utils.QosTypeManual
		return runPoolQosTypeTransitionManualToAuto(
			orchCtx,
			orchCtx,
			nil,
			common.NewRollbackManager(),
			poolActivity,
			svmActivity,
			dbPool,
			updateParams,
			dbPool,
		)
	}

	return runPoolQosTypeTransitionAutoToManual(
		orchCtx,
		orchCtx,
		nil,
		common.NewRollbackManager(),
		poolActivity,
		svmActivity,
		dbPool,
		updateParams,
		dbPool,
	)
}

func qosTransitionTestPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{ID: 100, UUID: "pool-uuid"},
		Name:           "test-pool",
		QosType:        utils.QosTypeAuto,
		DeploymentName: "test-deployment",
		SizeInBytes:    2 * 1024 * 1024 * 1024 * 1024,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 128,
			Iops:            2048,
		},
		AutoTieringConfig: &datamodel.AutoTieringConfig{},
	}
}

func newQosTransitionTestEnvironment(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	mockStorage := database.NewMockStorage(t)
	env.RegisterWorkflow(testQosTransitionWorkflow)
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	env.RegisterActivity(&activities.SvmActivity{SE: mockStorage})
	env.RegisterActivity(&activities.VolumeCreateActivity{SE: mockStorage})
	env.RegisterActivity(&activities.VolumePerformanceGroupActivity{SE: mockStorage})
	env.RegisterActivity(&activities.VolumeUpdateActivity{SE: mockStorage})
	env.RegisterActivity(&activities.VolumeDeleteActivity{SE: mockStorage})
	return env
}

func activityError(failingActivity, activity string) error {
	if failingActivity == activity {
		return errors.New("forced activity failure")
	}
	return nil
}

func mockAutoToManualTransitionActivities(env *testsuite.TestWorkflowEnvironment, failingActivity string) {
	nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node-1"}}
	volume := &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: "volume-uuid"}, PoolID: 100}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "test-pool-vpg",
		OntapQosPolicyID: "qos-policy-uuid",
	}

	env.OnActivity("GetNode", mock.Anything, mock.Anything).
		Return(nodes, activityError(failingActivity, "GetNode")).Maybe()
	env.OnActivity("RemoveQoSPolicyFromSVM", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "RemoveQoSPolicyFromSVM")).Maybe()
	env.OnActivity("DeleteQoSPolicyInONTAP", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "DeleteQoSPolicyInONTAP")).Maybe()
	env.OnActivity("CreateQoSPolicyInONTAP", mock.Anything, mock.Anything, mock.Anything).
		Return("qos-policy-uuid", activityError(failingActivity, "CreateQoSPolicyInONTAP")).Maybe()
	env.OnActivity("CreateVPGInDB", mock.Anything, mock.Anything).
		Return(vpg, activityError(failingActivity, "CreateVPGInDB")).Maybe()
	env.OnActivity("GetPoolVolumesForQosTransition", mock.Anything, mock.Anything).
		Return([]*datamodel.Volume{volume}, activityError(failingActivity, "GetPoolVolumesForQosTransition")).Maybe()
	env.OnActivity("AssignQoSPolicyToVolume", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "AssignQoSPolicyToVolume")).Maybe()
	env.OnActivity("UpdateVolumePerformanceGroupInDBForVolume", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UpdateVolumePerformanceGroupInDBForVolume")).Maybe()
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UpdatePoolFields")).Maybe()
	env.OnActivity("UpdateAutoTieringHotTierInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UpdateAutoTieringHotTierInfo")).Maybe()
	env.OnActivity("UpdatedPool", mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), activityError(failingActivity, "UpdatedPool")).Maybe()
}

func mockManualToAutoTransitionActivities(env *testsuite.TestWorkflowEnvironment, failingActivity string) {
	nodes := []*datamodel.Node{{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node-1"}}
	volume := &datamodel.Volume{
		BaseModel:                datamodel.BaseModel{UUID: "volume-uuid"},
		PoolID:                   100,
		VolumePerformanceGroupID: sql.NullInt64{Int64: 1, Valid: true},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:      "test-vpg",
		PoolID:    100,
	}

	env.OnActivity("GetNode", mock.Anything, mock.Anything).
		Return(nodes, activityError(failingActivity, "GetNode")).Maybe()
	env.OnActivity("GetPoolVolumesForQosTransition", mock.Anything, mock.Anything).
		Return([]*datamodel.Volume{volume}, activityError(failingActivity, "GetPoolVolumesForQosTransition")).Maybe()
	env.OnActivity("ListVolumePerformanceGroupsByPoolID", mock.Anything, mock.Anything).
		Return([]*datamodel.VolumePerformanceGroup{vpg}, activityError(failingActivity, "ListVolumePerformanceGroupsByPoolID")).Maybe()
	env.OnActivity("UnassignQoSPolicyFromVolume", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UnassignQoSPolicyFromVolume")).Maybe()
	env.OnActivity("UpdateVolumePerformanceGroupInDBForVolume", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UpdateVolumePerformanceGroupInDBForVolume")).Maybe()
	env.OnActivity("DereferencePoolVolumesFromVPGs", mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "DereferencePoolVolumesFromVPGs")).Maybe()
	env.OnActivity("DeleteVPGByID", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "DeleteVPGByID")).Maybe()
	env.OnActivity("DeleteQoSPolicyInONTAP", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "DeleteQoSPolicyInONTAP")).Maybe()
	env.OnActivity("ModifyQoSPolicyAndApplyToSVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "ModifyQoSPolicyAndApplyToSVM")).Maybe()
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(activityError(failingActivity, "UpdatePoolFields")).Maybe()
}

func TestQosTransitionLogging_ActivityFailures(t *testing.T) {
	tests := []qosTransitionTestInput{
		{Direction: "auto_to_manual", FailingActivity: "GetNode"},
		{Direction: "auto_to_manual", FailingActivity: "DeleteQoSPolicyInONTAP"},
		{Direction: "auto_to_manual", FailingActivity: "CreateQoSPolicyInONTAP"},
		{Direction: "auto_to_manual", FailingActivity: "CreateVPGInDB"},
		{Direction: "auto_to_manual", FailingActivity: "AssignQoSPolicyToVolume"},
		{Direction: "auto_to_manual", FailingActivity: "UpdatePoolFields"},
		{Direction: "auto_to_manual", FailingActivity: "UpdatedPool"},
		{Direction: "manual_to_auto", FailingActivity: "GetNode"},
		{Direction: "manual_to_auto", FailingActivity: "GetPoolVolumesForQosTransition"},
		{Direction: "manual_to_auto", FailingActivity: "ListVolumePerformanceGroupsByPoolID"},
		{Direction: "manual_to_auto", FailingActivity: "UnassignQoSPolicyFromVolume"},
		{Direction: "manual_to_auto", FailingActivity: "UpdateVolumePerformanceGroupInDBForVolume"},
		{Direction: "manual_to_auto", FailingActivity: "DereferencePoolVolumesFromVPGs"},
		{Direction: "manual_to_auto", FailingActivity: "DeleteQoSPolicyInONTAP"},
		{Direction: "manual_to_auto", FailingActivity: "ModifyQoSPolicyAndApplyToSVM"},
		{Direction: "manual_to_auto", FailingActivity: "UpdatePoolFields"},
	}

	for _, test := range tests {
		t.Run(test.Direction+"/"+test.FailingActivity, func(t *testing.T) {
			env := newQosTransitionTestEnvironment(t)
			if test.Direction == "manual_to_auto" {
				mockManualToAutoTransitionActivities(env, test.FailingActivity)
			} else {
				mockAutoToManualTransitionActivities(env, test.FailingActivity)
			}

			env.ExecuteWorkflow(testQosTransitionWorkflow, test)

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
			env.AssertExpectations(t)
		})
	}
}
